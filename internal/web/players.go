package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/osmandagli/fitet-web-scrapper/internal/fitet"
)

const (
	// rankingMaxAge: rankings are published roughly monthly, so a daily re-check is plenty.
	rankingMaxAge = 24 * time.Hour
	// retryMissing: how long to wait before searching again for a name that wasn't found.
	retryMissing = 7 * 24 * time.Hour
	// rankClassMaxAge: how often to look for a newer ranking publication.
	rankClassMaxAge = 6 * time.Hour
)

// playerInfo is what we know about one athlete of the group.
type playerInfo struct {
	Ref        fitet.AthleteRef `json:"ref"`
	Missing    bool             `json:"missing,omitempty"` // searched, not found
	SearchedAt time.Time        `json:"searched_at"`
	Ranking    *fitet.Ranking   `json:"ranking,omitempty"`
	RankingAt  time.Time        `json:"ranking_at"`
	RankClass  int              `json:"rank_class"` // ID_CLASS the ranking came from

	// Details breaks the ranking points down by tournament; it only changes
	// with a new ranking publication, so it is fetched once per publication.
	// DetailsClass is the publication it was read for (also when there was none).
	Details      *fitet.RankingDetails `json:"details,omitempty"`
	DetailsClass int                   `json:"details_class,omitempty"`
}

// groupPlayers lists every singles player who appears in a score sheet.
func (s *Server) groupPlayers() []string {
	seen := map[string]bool{}
	var names []string
	for _, sh := range s.snap.Sheets {
		for _, g := range sh.Games {
			if g.Double {
				continue
			}
			for _, p := range append(append([]string{}, g.Left...), g.Right...) {
				if !seen[p] {
					seen[p] = true
					names = append(names, p)
				}
			}
		}
	}
	return names
}

// resolvePlayers finds the FITET id and current ranking of every group player
// we don't know yet (or whose ranking is stale). Requests are spaced out.
func (s *Server) resolvePlayers(ctx context.Context) {
	s.mu.RLock()
	names := s.groupPlayers()
	class, classAt := s.rankClass, s.rankClassAt
	s.mu.RUnlock()

	wait := func(first *bool) bool {
		if *first {
			*first = false
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(requestDelay):
			return true
		}
	}
	first := true

	if time.Since(classAt) > rankClassMaxAge {
		if !wait(&first) {
			return
		}
		if c, err := s.Client.LatestRankingClass(ctx); err != nil {
			log.Printf("ranking publication: %v", err)
		} else {
			class = c
			s.mu.Lock()
			s.rankClass, s.rankClassAt = c, time.Now()
			s.mu.Unlock()
		}
	}

	changed := 0
	for _, name := range names {
		s.mu.RLock()
		info := s.players[name]
		s.mu.RUnlock()
		now := time.Now()

		if info == nil || (info.Missing && now.Sub(info.SearchedAt) > retryMissing) {
			if !wait(&first) {
				return
			}
			ref, ok, err := s.lookupGroupPlayer(ctx, name)
			if err != nil {
				log.Printf("player search %q: %v", name, err)
				continue
			}
			info = &playerInfo{Ref: ref, Missing: !ok, SearchedAt: now}
			s.mu.Lock()
			s.players[name] = info
			s.mu.Unlock()
			changed++
		}
		if info.Missing || class == 0 {
			continue
		}
		// A ranking cached before rankings recorded DetailsPath is read again once.
		unchecked := info.Ranking != nil && info.Ranking.DetailsPath == "" && info.DetailsClass != class
		if info.Ranking == nil || info.RankClass != class || now.Sub(info.RankingAt) >= rankingMaxAge || unchecked {
			if !wait(&first) {
				return
			}
			r, err := s.Client.Ranking(ctx, info.Ref.ID, class)
			if err != nil {
				log.Printf("ranking %q: %v", name, err)
				continue
			}
			updated := *info
			updated.Ranking, updated.RankingAt, updated.RankClass = r, now, class
			info = &updated
			s.mu.Lock()
			s.players[name] = info
			s.mu.Unlock()
			changed++
		}
		if info.DetailsClass == info.RankClass {
			continue // already read for this publication
		}
		if info.Ranking.DetailsPath == "" { // no breakdown published: don't ask again
			updated := *info
			updated.DetailsClass = info.RankClass
			s.mu.Lock()
			s.players[name] = &updated
			s.mu.Unlock()
			changed++
			continue
		}
		if !wait(&first) {
			return
		}
		d, err := s.Client.RankingDetails(ctx, info.Ranking.DetailsPath)
		if err != nil {
			log.Printf("points detail %q: %v", name, err)
			continue
		}
		updated := *info
		updated.Details, updated.DetailsClass = d, info.RankClass
		s.mu.Lock()
		s.players[name] = &updated
		s.mu.Unlock()
		changed++
	}

	if changed > 0 {
		s.mu.Lock()
		s.bump(time.Now())
		s.mu.Unlock()
		log.Printf("updated %d player record(s)", changed)
		s.savePlayers()
	}
}

// lookupGroupPlayer searches by full name. Homonyms are told apart by checking
// whose season actually includes our group (e.g. "C2 GIR C").
func (s *Server) lookupGroupPlayer(ctx context.Context, name string) (fitet.AthleteRef, bool, error) {
	refs, err := s.Client.FindAthletes(ctx, name)
	if err != nil {
		return fitet.AthleteRef{}, false, err
	}
	var exact []fitet.AthleteRef
	for _, r := range refs {
		if strings.EqualFold(r.Name, name) {
			exact = append(exact, r)
		}
	}
	switch len(exact) {
	case 0:
		return fitet.AthleteRef{}, false, nil
	case 1:
		return exact[0], true, nil
	}

	s.mu.RLock()
	group := strings.TrimPrefix(s.snap.GroupName, "Serie ")
	s.mu.RUnlock()
	for _, r := range exact {
		season, err := s.Client.AthleteSeason(ctx, r.ID)
		if err != nil {
			continue
		}
		for _, m := range season.Team {
			if group != "" && strings.HasPrefix(m.Event, group) {
				return r, true, nil
			}
		}
	}
	return exact[0], true, nil
}

// playerByName finds a known player by full name, or by the abbreviated
// doubles form ("BARGAGLI P") when that is unambiguous.
func (s *Server) playerByName(name string) *playerInfo {
	if p := s.players[name]; p != nil && !p.Missing {
		return p
	}
	name = nameKey(name) // tournament draws write names in title case
	if p := s.players[name]; p != nil && !p.Missing {
		return p
	}
	var found *playerInfo
	for full, p := range s.players {
		if !p.Missing && abbreviate(full) == name {
			if found != nil {
				return nil
			}
			found = p
		}
	}
	return found
}

// playerURL links a player name to its page. Unknown names go through the
// search, which redirects when it finds exactly one athlete. A static site has
// no search, so it only links players whose page was built, and "" otherwise.
func (s *Server) playerURL(name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := s.playerByName(name)
	if s.Static {
		if p == nil || s.pages[p.Ref.ID] == nil {
			return ""
		}
		return fmt.Sprintf("/player/%d", p.Ref.ID)
	}
	if p != nil {
		return fmt.Sprintf("/player/%d", p.Ref.ID)
	}
	return "/player/find?name=" + url.QueryEscape(name)
}

// rankingOf returns the cached ranking for a player name, or nil.
func (s *Server) rankingOf(name string) *fitet.Ranking {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p := s.playerByName(name); p != nil {
		return p.Ranking
	}
	return nil
}

func (s *Server) playersPath() string {
	if s.CacheDir == "" {
		return ""
	}
	return s.CacheDir + "/players.json"
}

func (s *Server) loadPlayers() {
	b, err := os.ReadFile(s.playersPath())
	if err != nil {
		return
	}
	var c map[string]*playerInfo
	if err := json.Unmarshal(b, &c); err != nil {
		log.Printf("ignoring unreadable player cache: %v", err)
		return
	}
	s.players = c
	log.Printf("loaded %d cached player(s)", len(c))
}

func (s *Server) savePlayers() {
	if s.playersPath() == "" {
		return
	}
	s.mu.RLock()
	b, err := json.Marshal(s.players)
	s.mu.RUnlock()
	if err == nil {
		err = writeFileAtomic(s.playersPath(), b)
	}
	if err != nil {
		log.Printf("saving player cache: %v", err)
	}
}

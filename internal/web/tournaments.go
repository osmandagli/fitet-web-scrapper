package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/osmandagli/fitet-web-scrapper/internal/fitet"
)

const (
	// resultsFinalAfter: results are re-read until this long after a
	// tournament's first day (late corrections), then served from the cache.
	resultsFinalAfter = 4 * 24 * time.Hour
	// entriesMaxAge: how often to re-read the entry lists of upcoming tournaments.
	entriesMaxAge = 6 * time.Hour
	// cardWinners: event winners listed on a tournament card.
	cardWinners = 6
)

// tournament is one regional tournament of the season, with its results
// (played) or entry lists (upcoming).
type tournament struct {
	fitet.TournamentRef
	Name      string            `json:"name"`
	Where     string            `json:"where"`
	Events    []tournamentEvent `json:"events"`
	FetchedAt time.Time         `json:"fetched_at"`
}

type tournamentEvent struct {
	Name    string            `json:"name"`
	Gara    int               `json:"gara,omitempty"`
	Groups  []fitet.DrawGroup `json:"groups,omitempty"`
	Bracket *fitet.Bracket    `json:"bracket,omitempty"`
	Entries []fitet.Entry     `json:"entries,omitempty"`
}

func (t *tournament) date() time.Time { return dmy(t.Date) }

// refreshTournaments reads the committee's tournament lists and fetches what
// is new: results of tournaments not final yet, entries of upcoming ones.
func (s *Server) refreshTournaments(ctx context.Context) {
	if s.Committee == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	first := true
	wait := func() bool {
		if first {
			first = false
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(requestDelay):
			return true
		}
	}

	refs := map[int]*fitet.TournamentRef{}
	var order []int
	failed := 0
	for _, kind := range fitet.TournamentKinds {
		if !wait() {
			return
		}
		list, err := s.Client.Tournaments(ctx, s.Committee, kind)
		if err != nil {
			log.Printf("tournament list %d: %v", kind, err)
			failed++
			continue
		}
		for _, r := range list {
			if have := refs[r.ID]; have != nil {
				have.Kinds = append(have.Kinds, kind)
				have.Played = have.Played || r.Played
				continue
			}
			r := r
			refs[r.ID] = &r
			order = append(order, r.ID)
		}
	}
	if failed == len(fitet.TournamentKinds) {
		return // portal down: keep what we have
	}

	changed := 0
	for _, id := range order {
		ref := *refs[id]
		s.mu.RLock()
		have := s.tournaments[id]
		s.mu.RUnlock()
		if have != nil && !needsFetch(have, ref, time.Now()) {
			if !equalKinds(have.Kinds, ref.Kinds) {
				s.mu.Lock()
				updated := *have
				updated.Kinds = ref.Kinds
				s.tournaments[id] = &updated
				s.mu.Unlock()
				changed++
			}
			continue
		}
		t, err := s.fetchTournament(ctx, ref, wait)
		if err != nil {
			log.Printf("tournament %d (%s): %v", id, ref.Place, err)
			if have != nil || ctx.Err() != nil {
				continue // keep what we had
			}
			// List it anyway; a zero FetchedAt retries it next time.
			t = &tournament{TournamentRef: ref}
		}
		s.mu.Lock()
		s.tournaments[id] = t
		s.mu.Unlock()
		changed++
	}
	if changed > 0 {
		s.mu.Lock()
		s.bump(time.Now())
		s.mu.Unlock()
		log.Printf("updated %d tournament(s)", changed)
		s.saveTournaments()
	}
}

func needsFetch(have *tournament, ref fitet.TournamentRef, now time.Time) bool {
	switch {
	case have.Played != ref.Played:
		return true // results just came out
	case ref.Played:
		return have.FetchedAt.Before(dmy(ref.Date).Add(resultsFinalAfter))
	default:
		return now.Sub(have.FetchedAt) > entriesMaxAge
	}
}

func equalKinds(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *Server) fetchTournament(ctx context.Context, ref fitet.TournamentRef, wait func() bool) (*tournament, error) {
	if !wait() {
		return nil, ctx.Err()
	}
	info, err := s.Client.TournamentInfo(ctx, s.Committee, ref.ID, ref.Played)
	if err != nil {
		return nil, err
	}
	t := &tournament{TournamentRef: ref, Name: info.Name, Where: info.Where, FetchedAt: time.Now()}

	if !ref.Played {
		for _, ev := range info.Events {
			if !wait() {
				return nil, ctx.Err()
			}
			entries, err := s.Client.Entries(ctx, s.Committee, ref.ID, ev.Gara)
			if err != nil {
				log.Printf("entries %d/%d: %v", ref.ID, ev.Gara, err)
			}
			t.Events = append(t.Events, tournamentEvent{Name: ev.Name, Gara: ev.Gara, Entries: entries})
		}
		return t, nil
	}

	if info.DrawsPath == "" {
		for _, ev := range info.Events {
			t.Events = append(t.Events, tournamentEvent{Name: ev.Name, Gara: ev.Gara})
		}
		return t, nil
	}
	if !wait() {
		return nil, ctx.Err()
	}
	pages, err := s.Client.DrawPages(ctx, info.DrawsPath)
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for _, p := range pages {
		i, ok := idx[p.Event]
		if !ok {
			i = len(t.Events)
			idx[p.Event] = i
			t.Events = append(t.Events, tournamentEvent{Name: p.Event})
		}
		if !wait() {
			return nil, ctx.Err()
		}
		if p.Knockout {
			if t.Events[i].Bracket, err = s.Client.Knockout(ctx, p.Path); err != nil {
				log.Printf("draw %s: %v", p.Path, err)
			}
		} else if t.Events[i].Groups, err = s.Client.Groups(ctx, p.Path); err != nil {
			log.Printf("groups %s: %v", p.Path, err)
		}
	}
	return t, nil
}

// --- cache on disk -----------------------------------------------------------

func (s *Server) tournamentsPath() string {
	if s.CacheDir == "" {
		return ""
	}
	return filepath.Join(s.CacheDir, "tournaments.json")
}

func (s *Server) loadTournaments() {
	if s.tournamentsPath() == "" {
		return
	}
	b, err := os.ReadFile(s.tournamentsPath())
	if err != nil {
		return
	}
	var c map[int]*tournament
	if err := json.Unmarshal(b, &c); err != nil {
		log.Printf("ignoring unreadable tournament cache: %v", err)
		return
	}
	s.tournaments = c
	log.Printf("loaded %d cached tournament(s)", len(c))
}

func (s *Server) saveTournaments() {
	if s.tournamentsPath() == "" {
		return
	}
	s.mu.RLock()
	b, err := json.Marshal(s.tournaments)
	s.mu.RUnlock()
	if err == nil {
		err = writeFileAtomic(s.tournamentsPath(), b)
	}
	if err != nil {
		log.Printf("saving tournament cache: %v", err)
	}
}

// --- who's who ---------------------------------------------------------------

// nameKey compares names across pages: score sheets and entry lists are in
// capitals, tournament draws in title case.
func nameKey(name string) string { return strings.ToUpper(strings.Join(strings.Fields(name), " ")) }

// groupTeams maps every singles player of the group to their team.
func (s *Server) groupTeams() map[string]string {
	teams := map[string]string{}
	for _, sh := range s.snap.Sheets {
		for _, g := range sh.Games {
			if g.Double {
				continue
			}
			for _, p := range g.Left {
				teams[nameKey(p)] = sh.LeftTeam
			}
			for _, p := range g.Right {
				teams[nameKey(p)] = sh.RightTeam
			}
		}
	}
	return teams
}

// who classifies a name for highlighting: "me", "mate" (my team), "grp"
// (another team of the group) or "".
func (s *Server) who(name string) string {
	if s.isMe(name) {
		return "me"
	}
	s.mu.RLock()
	ver := s.snap.Version
	s.teamsMu.Lock()
	if s.teams == nil || s.teamsVer != ver {
		s.teams, s.teamsVer = s.groupTeams(), ver
	}
	team, ok := s.teams[nameKey(name)]
	s.teamsMu.Unlock()
	s.mu.RUnlock()
	switch {
	case !ok:
		return ""
	case s.isMine(team):
		return "mate"
	}
	return "grp"
}

// --- views -------------------------------------------------------------------

// placing is how far a player got in one event. Rank orders placings, best first.
type placing struct {
	Name  string
	Team  string // their team in our group
	Mine  bool   // plays for my team
	Event string
	Stage string // "W" winner, a stage code (F, SF, QF, OF, 16, 32…), "G" out in the groups, "E" entered
	Group int    // group number, for "G"
	Pos   int    // group position, for "G"
	Live  bool   // still in the draw
	rank  int
}

type tournamentCard struct {
	ID       int
	Name     string
	Place    string
	Date     time.Time
	Kinds    []int
	Played   bool
	Events   int
	Entries  int
	Winners  []podiumLine // played: winner of each event
	More     int          // winners left out of a list card
	Ours     []placing    // group players who played / entered
	FITETURL string
}

type podiumLine struct {
	Event    string
	Winner   string
	Finalist string
}

type tournamentsView struct {
	Snapshot
	Every    time.Duration
	Upcoming []tournamentCard
	Played   []tournamentCard
}

type tournamentPage struct {
	Snapshot
	Every  time.Duration
	Card   tournamentCard
	Events []eventPage
}

type eventPage struct {
	Anchor   string
	Name     string
	Winner   string
	Finalist string
	Semis    []string
	Final    []koRound // the last rounds, drawn as a bracket
	Rounds   []koRound // the whole draw, round by round
	Groups   []fitet.DrawGroup
	Entries  []fitet.Entry
	Ours     []placing
}

type koRound struct {
	Name    string // stage code: F, SF, QF, OF, 16, 32, 64…
	Matches []fitet.KOMatch
}

// stageCode names round r of n by how far it is from the final.
func stageCode(r, n int) string {
	switch n - 1 - r {
	case 0:
		return "F"
	case 1:
		return "SF"
	case 2:
		return "QF"
	case 3:
		return "OF"
	}
	return strconv.Itoa(1 << (n - r)) // round of 32, 64…
}

func (t *tournament) fitetURL(anno, com int) string {
	if t.Played {
		return fmt.Sprintf("https://portale.fitet.org/risultati/tornei/visualizza_risultati.php?IDT=%d&ANNO=%d&COM=%d", t.ID, anno, com)
	}
	return fmt.Sprintf("https://portale.fitet.org/risultati/tornei/dati_sintetici.php?IDT=%d&TIPO=%d&COM=%d", t.ID, t.Kinds[0], com)
}

// kindName is the English name of a tournament list (fitet.TournamentKinds).
func kindName(kind int) string {
	switch kind {
	case 100:
		return "Open"
	case 101:
		return "Category"
	case 102:
		return "Youth"
	case 103:
		return "Ordinary"
	case 105:
		return "Veterans"
	case 107:
		return "Promotional"
	case 108:
		return "Para"
	case 110:
		return "Regional championships"
	case 111:
		return "Provincial"
	case 112:
		return "Women's day"
	}
	return strconv.Itoa(kind)
}

func (s *Server) tournamentCard(t *tournament, teams map[string]string) tournamentCard {
	c := tournamentCard{ID: t.ID, Name: t.Name, Place: t.Place, Date: t.date(), Kinds: t.Kinds, Played: t.Played,
		Events: len(t.Events), FITETURL: t.fitetURL(s.Group.ANNO, s.Committee)}
	if c.Name == "" {
		c.Name = t.Place
	}
	for _, ev := range t.Events {
		c.Entries += len(ev.Entries)
		w, f, _ := podium(ev)
		if w != "" {
			c.Winners = append(c.Winners, podiumLine{Event: ev.Name, Winner: w, Finalist: f})
		}
		c.Ours = append(c.Ours, s.placings(ev, teams)...)
	}
	sortPlacings(c.Ours)
	if !t.Played { // entered in several events: name them once
		seen := map[string]bool{}
		var once []placing
		for _, p := range c.Ours {
			if !seen[nameKey(p.Name)] {
				seen[nameKey(p.Name)] = true
				once = append(once, p)
			}
		}
		c.Ours = once
	}
	return c
}

// podium gives the winner, the finalist and the losing semi-finalists.
func podium(ev tournamentEvent) (winner, finalist string, semis []string) {
	if b := ev.Bracket; b != nil && len(b.Rounds) > 0 {
		final := b.Rounds[len(b.Rounds)-1]
		if len(final) == 1 && final[0].Winner != "" {
			winner, finalist = final[0].Winner, final[0].Loser()
		}
		if len(b.Rounds) > 1 {
			for _, m := range b.Rounds[len(b.Rounds)-2] {
				if l := m.Loser(); l != "" {
					semis = append(semis, l)
				}
			}
		}
		return
	}
	// Groups only: a single group decides the event.
	if len(ev.Groups) == 1 && len(ev.Groups[0].Standings) > 1 && ev.Groups[0].Standings[0].Played > 0 {
		st := ev.Groups[0].Standings
		return st[0].Name, st[1].Name, nil
	}
	return "", "", nil
}

// placings finds the group players in one event and how far they got.
func (s *Server) placings(ev tournamentEvent, teams map[string]string) []placing {
	var out []placing
	mine := func(team string) bool { return s.isMine(team) }
	add := func(p placing) {
		team, ok := teams[nameKey(p.Name)]
		if !ok && !s.isMe(p.Name) {
			return
		}
		p.Team, p.Mine, p.Event = team, mine(team) || s.isMe(p.Name), ev.Name
		out = append(out, p)
	}

	for _, e := range ev.Entries {
		add(placing{Name: e.Name, Stage: "E"})
	}
	inDraw := map[string]bool{}
	if b := ev.Bracket; b != nil {
		last := map[string]int{}
		var names []string
		for r, round := range b.Rounds {
			for _, m := range round {
				for _, n := range []string{m.A, m.B} {
					if n == "" {
						continue
					}
					if _, ok := last[n]; !ok {
						names = append(names, n)
					}
					last[n] = r
				}
			}
		}
		for _, n := range names {
			inDraw[n] = true
			r := last[n]
			p := placing{Name: n, Stage: stageCode(r, len(b.Rounds)), rank: 100 - (len(b.Rounds) - r)}
			for _, m := range b.Rounds[r] {
				if m.A != n && m.B != n {
					continue
				}
				switch {
				case m.Winner == "":
					p.Live = true
				case m.Winner == n && r == len(b.Rounds)-1:
					p.Stage, p.rank = "W", 100
				}
			}
			add(p)
		}
	}
	for _, g := range ev.Groups {
		for _, st := range g.Standings {
			if !inDraw[st.Name] {
				add(placing{Name: st.Name, Stage: "G", Group: g.No, Pos: st.Pos, rank: 10 - st.Pos})
			}
		}
	}
	return out
}

func sortPlacings(ps []placing) {
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].Mine != ps[j].Mine {
			return ps[i].Mine
		}
		return ps[i].rank > ps[j].rank
	})
}

func (s *Server) eventPage(i int, ev tournamentEvent, teams map[string]string) eventPage {
	e := eventPage{Anchor: fmt.Sprintf("e%d", i+1), Name: ev.Name, Groups: ev.Groups, Ours: s.placings(ev, teams)}
	sortPlacings(e.Ours)
	e.Winner, e.Finalist, e.Semis = podium(ev)
	if b := ev.Bracket; b != nil {
		n := len(b.Rounds)
		for r, round := range b.Rounds {
			kr := koRound{Name: stageCode(r, n), Matches: round}
			e.Rounds = append(e.Rounds, kr)
			if n-r <= 3 {
				e.Final = append(e.Final, kr)
			}
		}
	}
	e.Entries = append([]fitet.Entry(nil), ev.Entries...)
	sort.SliceStable(e.Entries, func(i, j int) bool { return e.Entries[i].Points > e.Entries[j].Points })
	return e
}

func (s *Server) buildTournamentsView() tournamentsView {
	v := tournamentsView{Snapshot: s.snap, Every: s.Every}
	teams := s.groupTeams()
	for _, t := range s.tournaments {
		c := s.tournamentCard(t, teams)
		if len(c.Winners) > cardWinners {
			c.More, c.Winners = len(c.Winners)-cardWinners, c.Winners[:cardWinners]
		}
		if t.Played {
			v.Played = append(v.Played, c)
		} else {
			v.Upcoming = append(v.Upcoming, c)
		}
	}
	sort.Slice(v.Upcoming, func(i, j int) bool { return v.Upcoming[i].Date.Before(v.Upcoming[j].Date) })
	sort.Slice(v.Played, func(i, j int) bool { return v.Played[i].Date.After(v.Played[j].Date) })
	return v
}

func (s *Server) buildTournamentPage(id int) (tournamentPage, bool) {
	t := s.tournaments[id]
	if t == nil {
		return tournamentPage{}, false
	}
	teams := s.groupTeams()
	p := tournamentPage{Snapshot: s.snap, Every: s.Every, Card: s.tournamentCard(t, teams)}
	for i, ev := range t.Events {
		p.Events = append(p.Events, s.eventPage(i, ev, teams))
	}
	return p, true
}

// nextTournament is the first upcoming tournament, for the group page.
func (s *Server) nextTournament() *tournamentCard {
	v := s.buildTournamentsView()
	if len(v.Upcoming) == 0 {
		return nil
	}
	return &v.Upcoming[0]
}

// tournamentURL links a player's tournament (dd/mm/yyyy) to its page, if we have it.
func (s *Server) tournamentURL(date string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.tournaments {
		if t.Played && t.Date == date {
			return fmt.Sprintf("/tornei/%d", t.ID)
		}
	}
	return ""
}

func (s *Server) handleTournaments(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	v := s.buildTournamentsView()
	s.mu.RUnlock()
	s.render(w, r, "tournaments.html", v)
}

func (s *Server) handleTournament(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.mu.RLock()
	v, ok := s.buildTournamentPage(id)
	s.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.render(w, r, "tournament.html", v)
}

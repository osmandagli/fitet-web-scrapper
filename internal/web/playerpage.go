package web

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/osmandagli/fitet-web-scrapper/internal/fitet"
)

// playerPage is a player's full FITET record, fetched when someone opens it.
type playerPage struct {
	Season  *fitet.AthleteSeason
	Ranking *fitet.Ranking
	RankErr string
	At      time.Time
}

func (s *Server) loadPlayerPage(ctx context.Context, id int) (*playerPage, error) {
	s.pageMu.Lock()
	defer s.pageMu.Unlock()

	s.mu.RLock()
	cached, class := s.pages[id], s.rankClass
	s.mu.RUnlock()
	if cached != nil && time.Since(cached.At) < s.Every {
		return cached, nil
	}

	season, err := s.Client.AthleteSeason(ctx, id)
	if err != nil {
		return nil, err
	}
	p := &playerPage{Season: season, At: time.Now()}
	if class == 0 {
		if class, err = s.Client.LatestRankingClass(ctx); err == nil {
			s.mu.Lock()
			s.rankClass, s.rankClassAt = class, time.Now()
			s.mu.Unlock()
		}
	}
	if r := s.cachedRanking(id, class); r != nil {
		p.Ranking = r
	} else if class != 0 {
		if p.Ranking, err = s.Client.Ranking(ctx, id, class); err != nil {
			p.RankErr = "Not in the national ranking" // translated by the template
			log.Printf("ranking %d: %v", id, err)
		}
	}

	s.mu.Lock()
	s.pages[id] = p
	s.mu.Unlock()
	return p, nil
}

// cachedRanking reuses a group player's ranking from the current publication,
// saving a request.
func (s *Server) cachedRanking(id, class int) *fitet.Ranking {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.players {
		if p.Ref.ID == id && !p.Missing && p.Ranking != nil && class != 0 && p.RankClass == class {
			return p.Ranking
		}
	}
	return nil
}

func (s *Server) handlePlayer(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := s.loadPlayerPage(r.Context(), id)
	if err != nil {
		log.Printf("player %d: %v", id, err)
		http.Error(w, "Couldn't load this player from the FITET portal: "+err.Error(), http.StatusBadGateway)
		return
	}
	s.mu.RLock()
	v := s.buildPlayerView(id, p)
	s.mu.RUnlock()
	s.render(w, r, "player.html", v)
}

// handlePlayerFind resolves a name to a player page: known group players and
// unique search hits redirect, otherwise a list of candidates is shown.
func (s *Server) handlePlayerFind(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		http.NotFound(w, r)
		return
	}
	s.mu.RLock()
	known := s.playerByName(name)
	s.mu.RUnlock()
	if known != nil {
		http.Redirect(w, r, fmt.Sprintf("%s/player/%d", langPrefix(langOf(r)), known.Ref.ID), http.StatusFound)
		return
	}

	refs, err := s.Client.FindAthletes(r.Context(), name)
	if err != nil {
		http.Error(w, "Search failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	var exact []fitet.AthleteRef
	for _, ref := range refs {
		if strings.EqualFold(ref.Name, name) {
			exact = append(exact, ref)
		}
	}
	if len(exact) == 1 {
		http.Redirect(w, r, fmt.Sprintf("%s/player/%d", langPrefix(langOf(r)), exact[0].ID), http.StatusFound)
		return
	}
	if len(exact) > 1 {
		refs = exact
	}
	if len(refs) > 30 {
		refs = refs[:30]
	}
	s.mu.RLock()
	v := struct {
		Snapshot
		Every time.Duration
		Query string
		Refs  []fitet.AthleteRef
	}{s.snap, s.Every, name, refs}
	s.mu.RUnlock()
	s.render(w, r, "find.html", v)
}

// --- view --------------------------------------------------------------------

type playerView struct {
	Snapshot
	Every       time.Duration
	ID          int
	Name        string
	Season      *fitet.AthleteSeason
	Ranking     *fitet.Ranking
	RankErr     string
	Team        string // the group team they play for, if any
	FetchedAt   time.Time
	All         record
	Totals      []record
	Tournaments []tournamentView
	Chart       *rankChart
}

type record struct {
	Label     string
	Won, Lost int
	SetsFor   int
	SetsAgst  int
}

func (r *record) add(m fitet.AthleteMatch) {
	if m.Won {
		r.Won++
	} else {
		r.Lost++
	}
	r.SetsFor += m.For
	r.SetsAgst += m.Against
}

type tournamentView struct {
	fitet.Tournament
	record
	Events []eventView // one per category entered, in the order played
}

// eventView is one category ("gara") of a tournament, e.g.
// "Singolo M/F Open3 451-2500M | 26-200F" or "Singolo Maschile 4^ cat".
type eventView struct {
	Name string
	record
	Matches []fitet.AthleteMatch
	Reached string // stage code of the furthest round, e.g. "QF"
	Winner  bool
}

// groupEvents splits a tournament's matches by category. Names are compared
// case-insensitively: the portal writes "4^ cat" and "4^ CAT" for the same thing.
func groupEvents(matches []fitet.AthleteMatch) []eventView {
	var out []eventView
	idx := map[string]int{}
	for _, m := range matches {
		key := strings.ToUpper(m.Event)
		i, ok := idx[key]
		if !ok {
			i = len(out)
			idx[key] = i
			out = append(out, eventView{Name: m.Event})
		}
		out[i].add(m)
		out[i].Matches = append(out[i].Matches, m)
	}
	for i := range out {
		last := out[i].Matches[len(out[i].Matches)-1]
		out[i].Reached = last.Stage
		out[i].Winner = strings.EqualFold(last.Stage, "F") && last.Won
	}
	return out
}

func (s *Server) buildPlayerView(id int, p *playerPage) playerView {
	v := playerView{Snapshot: s.snap, Every: s.Every, ID: id, Season: p.Season, Ranking: p.Ranking,
		RankErr: p.RankErr, Name: p.Season.Name, FetchedAt: p.At}

	for _, sh := range s.snap.Sheets {
		for _, g := range sh.Games {
			if contains(g.Left, v.Name) {
				v.Team = sh.LeftTeam
			} else if contains(g.Right, v.Name) {
				v.Team = sh.RightTeam
			}
		}
	}

	// Labels are translated by the template.
	tour, team, po := record{Label: "Tournaments"}, record{Label: "Team championships"}, record{Label: "Play-off / out"}
	for _, t := range p.Season.Tournaments {
		tv := tournamentView{Tournament: t, Events: groupEvents(t.Matches)}
		for _, m := range t.Matches {
			tv.add(m)
			tour.add(m)
			v.All.add(m)
		}
		v.Tournaments = append(v.Tournaments, tv)
	}
	for _, m := range p.Season.Team {
		team.add(m)
		v.All.add(m)
	}
	for _, m := range p.Season.Playoff {
		po.add(m)
		v.All.add(m)
	}
	for _, r := range []record{tour, team, po} {
		if r.Won+r.Lost > 0 {
			v.Totals = append(v.Totals, r)
		}
	}
	// Most recent tournament first.
	sort.SliceStable(v.Tournaments, func(i, j int) bool {
		return dmy(v.Tournaments[i].Date).After(dmy(v.Tournaments[j].Date))
	})
	if p.Ranking != nil {
		v.Chart = newRankChart(p.Ranking.History)
	}
	return v
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func dmy(s string) time.Time {
	t, _ := time.Parse("02/01/2006", s)
	return t
}

// --- ranking chart -----------------------------------------------------------

// rankChart is a points-over-time line, drawn as an SVG path in a 1000x100
// box stretched to the card (non-scaling strokes), with HTML labels placed
// by percentage so text never distorts.
type rankChart struct {
	Path   string
	YTicks []tick
	XTicks []tick
	Last   chartPoint
	Points []chartPoint // for the hover readout
}

type tick struct {
	Pct   float64
	Label string  // year, on the x axis
	Value float64 // points, on the y axis
}

// chartPoint is formatted in the page's language by the browser.
type chartPoint struct {
	X        float64 `json:"x"` // 0-100, left
	Y        float64 `json:"y"` // 0-100, top
	Date     string  `json:"d"` // 2006-01-02
	Points   float64 `json:"v"`
	Position int     `json:"p,omitempty"`
	Category string  `json:"c"`
}

func newRankChart(history []fitet.RankPoint) *rankChart {
	type pt struct {
		t time.Time
		fitet.RankPoint
	}
	var pts []pt
	for _, h := range history {
		t, err := time.Parse("2006-01-02", h.Date)
		if err == nil {
			pts = append(pts, pt{t, h})
		}
	}
	if len(pts) < 2 {
		return nil
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].t.Before(pts[j].t) })

	lo, hi := pts[0].Points, pts[0].Points
	for _, p := range pts {
		lo, hi = min(lo, p.Points), max(hi, p.Points)
	}
	step := niceStep(hi - lo)
	lo, hi = (lo/step)*step, ((hi+step-1)/step)*step
	if hi == lo {
		hi = lo + step
	}
	t0, t1 := pts[0].t, pts[len(pts)-1].t
	span := t1.Sub(t0).Seconds()

	c := &rankChart{}
	var b strings.Builder
	for i, p := range pts {
		x := 100 * p.t.Sub(t0).Seconds() / span
		y := 100 * float64(hi-p.Points) / float64(hi-lo)
		// Rankings are published monthly; a long gap means the athlete wasn't
		// ranked, so break the line instead of drawing a made-up trend.
		if i == 0 || p.t.Sub(pts[i-1].t) > rankGap {
			if i > 0 {
				b.WriteByte(' ')
			}
			fmt.Fprintf(&b, "M%.1f %.2f", x*10, y)
		} else {
			fmt.Fprintf(&b, " L%.1f %.2f", x*10, y)
		}
		c.Points = append(c.Points, chartPoint{X: x, Y: y, Date: p.t.Format("2006-01-02"),
			Points: float64(p.Points), Position: p.Position, Category: p.Category})
	}
	c.Path = b.String()
	c.Last = c.Points[len(c.Points)-1]
	for v := lo; v <= hi; v += step {
		c.YTicks = append(c.YTicks, tick{Pct: 100 * float64(hi-v) / float64(hi-lo), Value: float64(v)})
	}
	years := t1.Year() - t0.Year()
	every := 1
	for _, n := range []int{1, 2, 5, 10} {
		every = n
		if years/n <= 7 {
			break
		}
	}
	for y := t0.Year() + 1; y <= t1.Year(); y++ {
		if y%every != 0 {
			continue
		}
		t := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
		c.XTicks = append(c.XTicks, tick{Pct: 100 * t.Sub(t0).Seconds() / span, Label: strconv.Itoa(y)})
	}
	return c
}

// rankGap is the longest pause between ranking publications still drawn as a line.
const rankGap = 400 * 24 * time.Hour

// niceStep picks a round gridline step giving about 2-4 lines over span.
func niceStep(span int) int {
	for _, s := range []int{50, 100, 250, 500, 1000, 2000, 2500, 5000, 10000} {
		if span/s <= 4 {
			return s
		}
	}
	return 20000
}

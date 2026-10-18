package web

import (
	"sort"
	"strings"
	"time"

	"github.com/osmandagli/fitet-web-scrapper/internal/fitet"
)

// eventPoints is what one tournament event added to a player's ranking:
// points for the placing reached (when the tournament is among those that
// count) plus a fixed amount per group-stage win.
type eventPoints struct {
	Pos       string // stage reached, FITET's code: GQ, 32F, 16F, OF, QF, SF, F…
	Level     string // Gara rif.: "Open3", "TRP 4"…
	Placement float64
	Counted   bool // the placement counts (only the best few tournaments do)
	PerWin    float64
	Active    int // group wins that count
	Wins      float64
	Total     float64
}

// tournamentDays: an entry dated this long after a tournament's first day
// still belongs to it (two-day tournaments, events on the second day).
const tournamentDays = 2 * 24 * time.Hour

// sameTournament tells whether a breakdown row (name, dd/mm/yyyy) is the
// tournament called name (or a title containing it) starting on start. Names
// are written differently on different pages ("2 torneo pred Villa Guardia" /
// "2^ Torneo pred Villa Guardia"), so the date decides when they don't match.
func sameTournament(name string, start time.Time, rowName, rowDate string) bool {
	if a, b := nameKey(name), nameKey(rowName); a != "" && b != "" && (a == b || strings.Contains(a, b)) {
		return true
	}
	d := dmy(rowDate)
	return !start.IsZero() && !d.IsZero() && !d.Before(start) && d.Sub(start) <= tournamentDays
}

// eventPointsFor finds one event in a points breakdown; nil if it isn't there.
func eventPointsFor(d *fitet.RankingDetails, tournament string, start time.Time, event string) *eventPoints {
	if d == nil {
		return nil
	}
	var ep eventPoints
	found := false
	for _, p := range d.Placements {
		if nameKey(p.Event) == nameKey(event) && sameTournament(tournament, start, p.Tournament, p.Date) {
			ep.Pos, ep.Level, ep.Placement, ep.Counted, found = p.Pos, p.Level, p.Points, p.Counted, true
		}
	}
	for _, g := range d.GroupWins {
		if nameKey(g.Event) == nameKey(event) && sameTournament(tournament, start, g.Tournament, g.Date) {
			ep.PerWin, ep.Active, ep.Wins, found = g.PerWin, g.Active, g.Points, true
		}
	}
	if !found {
		return nil
	}
	ep.Total = ep.Wins
	if ep.Counted {
		ep.Total += ep.Placement
	}
	return &ep
}

// pointsView is the ranking points breakdown on a player page.
type pointsView struct {
	*fitet.RankingDetails
	AsOf string // ranking publication, dd/mm/yyyy
	Rows []pointsRow
}

type pointsRow struct {
	Date       string // dd/mm/yyyy
	Tournament string
	Event      string
	eventPoints
}

func newPointsView(d *fitet.RankingDetails, asOf string) *pointsView {
	if d == nil {
		return nil
	}
	v := &pointsView{RankingDetails: d, AsOf: asOf}
	idx := map[string]int{}
	row := func(date, tournament, event string) *pointsRow {
		key := nameKey(tournament) + "|" + nameKey(event)
		i, ok := idx[key]
		if !ok {
			i = len(v.Rows)
			idx[key] = i
			v.Rows = append(v.Rows, pointsRow{Date: date, Tournament: tournament, Event: event})
		}
		return &v.Rows[i]
	}
	for _, p := range d.Placements {
		r := row(p.Date, p.Tournament, p.Event)
		r.Pos, r.Level, r.Placement, r.Counted = p.Pos, p.Level, p.Points, p.Counted
	}
	for _, g := range d.GroupWins {
		r := row(g.Date, g.Tournament, g.Event)
		r.PerWin, r.Active, r.Wins = g.PerWin, g.Active, g.Points
	}
	for i := range v.Rows {
		r := &v.Rows[i]
		r.Total = r.Wins
		if r.Counted {
			r.Total += r.Placement
		}
	}
	sort.SliceStable(v.Rows, func(i, j int) bool { return dmy(v.Rows[i].Date).After(dmy(v.Rows[j].Date)) })
	return v
}

// playerInfoByID finds a group player by FITET id. Callers hold s.mu.
func (s *Server) playerInfoByID(id int) *playerInfo {
	for _, p := range s.players {
		if !p.Missing && p.Ref.ID == id {
			return p
		}
	}
	return nil
}

// pendingRanking tells whether a tournament is newer than the ranking
// publication the player's breakdown comes from.
func pendingRanking(info *playerInfo, start time.Time) bool {
	if info == nil || info.Ranking == nil {
		return false
	}
	asOf := dmy(info.Ranking.AsOf)
	return !asOf.IsZero() && start.After(asOf)
}

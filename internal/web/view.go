package web

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/osmandagli/fitet-web-scrapper/internal/fitet"
)

// newBadgeFor is how long a freshly published result is flagged as "new".
const newBadgeFor = 24 * time.Hour

// recentMatches is how many played matches the team page shows in detail.
const recentMatches = 5

type view struct {
	Snapshot
	Team      string
	Every     time.Duration
	MyRow     *fitet.Standing
	NextMatch *teamMatch
	Form      []teamMatch // my team's played matches, oldest first
	MyPlayed  int
	MyTotal   int
	Rounds    []roundView

	NextTournament *tournamentCard // nil without tournaments
}

type matchView struct {
	fitet.Match
	When     time.Time
	New      bool
	Mine     bool
	HomeWins bool
	AwayWins bool
}

type roundView struct {
	Number   int
	Leg      string // "Andata" / "Ritorno"
	From, To time.Time
	Matches  []matchView
	Done     bool
	Open     bool // expanded by default: last completed round and the next one
}

// teamMatch is a match seen from one team's side.
type teamMatch struct {
	matchView
	Opponent string
	AtHome   bool
	Result   string // "V" win, "P" loss, "N" draw, "" not played
	For      int    // games won by this team
	Against  int
	Sheet    *sheetView // nil until the score sheet has been fetched
}

type sheetView struct {
	Side   string // this team's letters: "ABC" or "XYZ"
	Ours   []fitet.Slot
	Theirs []fitet.Slot
	Games  []gameRow
}

type gameRow struct {
	No          int
	Double      bool
	OurLetter   string
	TheirLetter string
	Our, Their  []string
	Sets        []string // "11-9", from this team's side
	OurSets     int
	TheirSets   int
	Won         bool
}

func (s *Server) matchView(m fitet.Match, now time.Time) matchView {
	mv := matchView{Match: m, When: m.When(), Mine: s.isMine(m.Home) || s.isMine(m.Away)}
	if m.Played {
		mv.HomeWins = m.HomeScore > m.AwayScore
		mv.AwayWins = m.AwayScore > m.HomeScore
		if t, ok := s.newSince[m.ID]; ok && now.Sub(t) < newBadgeFor {
			mv.New = true
		}
	}
	return mv
}

// teamMatches returns all of team's matches in round order, from its side.
func (s *Server) teamMatches(team string, now time.Time) []teamMatch {
	var out []teamMatch
	for _, m := range s.snap.Matches {
		if m.Home != team && m.Away != team {
			continue
		}
		tm := teamMatch{matchView: s.matchView(m, now), AtHome: m.Home == team, Opponent: m.Away}
		if !tm.AtHome {
			tm.Opponent = m.Home
		}
		if m.Played {
			tm.For, tm.Against = m.HomeScore, m.AwayScore
			if !tm.AtHome {
				tm.For, tm.Against = tm.Against, tm.For
			}
			switch {
			case tm.For > tm.Against:
				tm.Result = "V"
			case tm.For < tm.Against:
				tm.Result = "P"
			default:
				tm.Result = "N"
			}
			if sh := s.snap.Sheets[m.ID]; sh != nil {
				tm.Sheet = sheetFor(sh, team, tm.AtHome)
			}
		}
		out = append(out, tm)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Round < out[j].Round })
	return out
}

// sheetFor turns a score sheet into team's point of view.
func sheetFor(sh *fitet.Sheet, team string, atHome bool) *sheetView {
	left := atHome // the home team is the left column
	switch team {
	case sh.LeftTeam:
		left = true
	case sh.RightTeam:
		left = false
	}

	sv := &sheetView{Ours: sh.Lineup(left), Theirs: sh.Lineup(!left)}
	if sh.LeftSide != "" {
		sv.Side = sh.LeftSide
		if !left {
			sv.Side = map[string]string{"ABC": "XYZ", "XYZ": "ABC"}[sh.LeftSide]
		}
	}
	for _, g := range sh.Games {
		r := gameRow{No: g.No, Double: g.Double,
			OurLetter: g.LeftLetter, TheirLetter: g.RightLetter, Our: g.Left, Their: g.Right,
			OurSets: g.LeftSets, TheirSets: g.RightSets}
		if !left {
			r.OurLetter, r.TheirLetter = r.TheirLetter, r.OurLetter
			r.Our, r.Their = r.Their, r.Our
			r.OurSets, r.TheirSets = r.TheirSets, r.OurSets
		}
		r.Won = r.OurSets > r.TheirSets
		for _, set := range g.Sets {
			a, b := set[0], set[1]
			if !left {
				a, b = b, a
			}
			r.Sets = append(r.Sets, fmt.Sprintf("%d-%d", a, b))
		}
		sv.Games = append(sv.Games, r)
	}
	return sv
}

func (s *Server) buildView(now time.Time) view {
	v := view{Snapshot: s.snap, Team: s.Team, Every: s.Every, NextTournament: s.nextTournament()}

	for i := range s.snap.Standings {
		if s.isMine(s.snap.Standings[i].Team) {
			v.MyRow = &s.snap.Standings[i]
		}
	}
	if v.MyRow != nil {
		for _, tm := range s.teamMatches(v.MyRow.Team, now) {
			v.MyTotal++
			if tm.Played {
				v.MyPlayed++
				v.Form = append(v.Form, tm)
			} else if v.NextMatch == nil {
				next := tm
				v.NextMatch = &next
			}
		}
	}

	byRound := map[int][]matchView{}
	maxRound := 0
	for _, m := range s.snap.Matches {
		byRound[m.Round] = append(byRound[m.Round], s.matchView(m, now))
		maxRound = max(maxRound, m.Round)
	}
	for r := 1; r <= maxRound; r++ {
		ms := byRound[r]
		if len(ms) == 0 {
			continue
		}
		sort.Slice(ms, func(i, j int) bool { return ms[i].When.Before(ms[j].When) })
		rv := roundView{Number: r, Leg: "Andata", Matches: ms, Done: true, From: ms[0].When, To: ms[len(ms)-1].When}
		if r > maxRound/2 {
			rv.Leg = "Ritorno"
		}
		for _, m := range ms {
			if !m.Played {
				rv.Done = false
			}
		}
		v.Rounds = append(v.Rounds, rv)
	}

	// Expand the last completed round and the first unfinished one.
	lastDone, firstOpen := -1, -1
	for i, r := range v.Rounds {
		if r.Done {
			lastDone = i
		} else if firstOpen < 0 {
			firstOpen = i
		}
	}
	for _, i := range []int{lastDone, firstOpen} {
		if i >= 0 {
			v.Rounds[i].Open = true
		}
	}
	return v
}

// --- team page ---------------------------------------------------------------

type teamView struct {
	Snapshot
	Every    time.Duration
	Row      fitet.Standing
	Mine     bool
	Form     []teamMatch // played, oldest first
	Recent   []teamMatch // played, newest first, with lineups
	Upcoming []teamMatch
	Players  []playerStat
	Played   int
	Total    int
}

type playerStat struct {
	Name      string
	Matches   int
	SinglesW  int
	SinglesL  int
	DoublesW  int
	DoublesL  int
	Positions string // e.g. "A ×2 · B ×1"
	SetsWon   int
	SetsLost  int
}

func (s *Server) buildTeamView(teamID int, now time.Time) (teamView, bool) {
	v := teamView{Snapshot: s.snap, Every: s.Every}
	found := false
	for _, st := range s.snap.Standings {
		if st.TeamID == teamID {
			v.Row, found = st, true
		}
	}
	if !found {
		return v, false
	}
	v.Mine = s.isMine(v.Row.Team)

	all := s.teamMatches(v.Row.Team, now)
	var played []teamMatch
	for _, tm := range all {
		v.Total++
		if tm.Played {
			played = append(played, tm)
		} else if len(v.Upcoming) < 3 {
			v.Upcoming = append(v.Upcoming, tm)
		}
	}
	v.Played = len(played)
	v.Form = played
	for i := len(played) - 1; i >= 0 && len(v.Recent) < recentMatches; i-- {
		v.Recent = append(v.Recent, played[i])
	}
	v.Players = playerStats(played)
	return v, true
}

func playerStats(played []teamMatch) []playerStat {
	type acc struct {
		playerStat
		matches   map[int]bool
		positions map[string]int
	}
	stats := map[string]*acc{}
	get := func(name string) *acc {
		if stats[name] == nil {
			stats[name] = &acc{playerStat: playerStat{Name: name}, matches: map[int]bool{}, positions: map[string]int{}}
		}
		return stats[name]
	}

	// Doubles use abbreviated names ("DAGLI OSMAN B"); map them to full names
	// seen in singles when the abbreviation matches.
	full := map[string]string{}
	for _, tm := range played {
		if tm.Sheet == nil {
			continue
		}
		for _, g := range tm.Sheet.Games {
			if !g.Double {
				for _, p := range g.Our {
					full[abbreviate(p)] = p
				}
			}
		}
	}
	resolve := func(p string) string {
		if f, ok := full[p]; ok {
			return f
		}
		return p
	}

	for _, tm := range played {
		if tm.Sheet == nil {
			continue
		}
		for _, sl := range tm.Sheet.Ours {
			if sl.Letter == "D" {
				continue
			}
			for _, p := range sl.Players {
				get(p).positions[sl.Letter]++
			}
		}
		for _, g := range tm.Sheet.Games {
			if g.OurSets+g.TheirSets == 0 {
				continue
			}
			for _, p := range g.Our {
				a := get(resolve(p))
				a.matches[tm.ID] = true
				switch {
				case g.Double && g.Won:
					a.DoublesW++
				case g.Double:
					a.DoublesL++
				case g.Won:
					a.SinglesW++
				default:
					a.SinglesL++
				}
				if !g.Double {
					a.SetsWon += g.OurSets
					a.SetsLost += g.TheirSets
				}
			}
		}
	}

	out := make([]playerStat, 0, len(stats))
	for _, a := range stats {
		a.Matches = len(a.matches)
		var parts []string
		for _, l := range []string{"A", "B", "C", "X", "Y", "Z"} {
			if n := a.positions[l]; n > 0 {
				parts = append(parts, fmt.Sprintf("%s ×%d", l, n))
			}
		}
		a.Positions = strings.Join(parts, " · ")
		out = append(out, a.playerStat)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.SinglesW+a.SinglesL != b.SinglesW+b.SinglesL {
			return a.SinglesW+a.SinglesL > b.SinglesW+b.SinglesL
		}
		if a.SinglesW != b.SinglesW {
			return a.SinglesW > b.SinglesW
		}
		return a.Name < b.Name
	})
	return out
}

// abbreviate gives the doubles form of a full name: "DAGLI OSMAN BUGRA" -> "DAGLI OSMAN B".
func abbreviate(name string) string {
	w := strings.Fields(name)
	if len(w) < 2 {
		return name
	}
	last := []rune(w[len(w)-1])
	return strings.Join(w[:len(w)-1], " ") + " " + string(last[0])
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

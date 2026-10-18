package fitet

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Sheet is the referto (score sheet) of one team match: every individual game
// with its players and set scores.
type Sheet struct {
	MatchID   int    `json:"match_id"`
	LeftTeam  string `json:"left_team"` // team whose players are in the left column (the home team)
	RightTeam string `json:"right_team"`
	// LeftSide is "ABC" or "XYZ" when the game order matches a known formula
	// (see AssignLetters), empty otherwise.
	LeftSide string `json:"left_side,omitempty"`
	Games    []Game `json:"games"`
}

type Game struct {
	No          int      `json:"no"`
	Double      bool     `json:"double"`
	Left        []string `json:"left"`                   // 1 player, or 2 abbreviated names for a double
	Right       []string `json:"right"`                  //
	LeftLetter  string   `json:"left_letter,omitempty"`  // "A".."C" / "X".."Z"; "D" for the double
	RightLetter string   `json:"right_letter,omitempty"` //
	Sets        [][2]int `json:"sets"`                   // played sets only, left-right points
	LeftSets    int      `json:"left_sets"`
	RightSets   int      `json:"right_sets"`
}

func (g Game) LeftWon() bool  { return g.LeftSets > g.RightSets }
func (g Game) RightWon() bool { return g.RightSets > g.LeftSets }
func (g Game) Played() bool   { return g.LeftSets+g.RightSets > 0 }

// Sheet fetches a match's score sheet. formula is the FORMULA value from the
// calendar link (19 for the Lombardia regional championships).
func (c *Client) Sheet(ctx context.Context, cam, matchID, formula int) (*Sheet, error) {
	body, err := c.Get(ctx, fmt.Sprintf("campionati/giornata.php?INCONTRO=%d&CAM=%d&FORMULA=%d", matchID, cam, formula))
	if err != nil {
		return nil, err
	}
	s, err := ParseSheet(body)
	if err != nil {
		return nil, fmt.Errorf("match %d: %w", matchID, err)
	}
	s.MatchID = matchID
	return s, nil
}

// ParseSheet parses giornata.php. The header row (containing "1^ set") names
// the two teams; each game row has 15 cells:
//
//	no | left player(s) | right player(s) | s1L s1R ... s5L s5R | sets L | sets R
func ParseSheet(body []byte) (*Sheet, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	s := &Sheet{}
	var parseErr error
	doc.Find("tr").Each(func(_ int, tr *goquery.Selection) {
		if parseErr != nil {
			return
		}
		td := tr.ChildrenFiltered("td")
		cell := func(i int) string { return clean(td.Eq(i).Text()) }

		if s.LeftTeam == "" && td.Length() >= 4 && cell(3) == "1^ set" {
			s.LeftTeam, s.RightTeam = cell(1), cell(2)
			return
		}
		if td.Length() != 15 || td.Eq(0).Find(`a[href*="dettaglio_partita"]`).Length() == 0 {
			return
		}

		no, err := strconv.Atoi(cell(0))
		if err != nil {
			parseErr = fmt.Errorf("game number %q: %w", cell(0), err)
			return
		}
		nums := make([]int, 12)
		for i := range nums {
			if nums[i], err = strconv.Atoi(cell(i + 3)); err != nil {
				parseErr = fmt.Errorf("game %d column %d: %q is not a number", no, i+3, cell(i+3))
				return
			}
		}
		g := Game{No: no, Left: splitPlayers(cell(1)), Right: splitPlayers(cell(2)),
			LeftSets: nums[10], RightSets: nums[11]}
		g.Double = len(g.Left) == 2
		for i := 0; i < 10; i += 2 {
			if nums[i]+nums[i+1] > 0 {
				g.Sets = append(g.Sets, [2]int{nums[i], nums[i+1]})
			}
		}
		s.Games = append(s.Games, g)
	})
	if parseErr != nil {
		return nil, parseErr
	}
	if len(s.Games) == 0 {
		return nil, fmt.Errorf("no games found: score sheet layout may have changed")
	}
	s.AssignLetters()
	return s, nil
}

// splitPlayers turns "BARGAGLI P - DAGLI OSMAN B" into its two doubles players.
func splitPlayers(s string) []string {
	parts := strings.Split(s, " - ")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// miniSwaythlingDoppio is the game order of the regional men's championships
// (Serie C2/D1/D2/D3), Regolamento Attività a Squadre 2026/27 – parte
// specifica, art. 1.6.1 "Mini Swaythling con doppio":
//
//	A-X, B-Y, C-Z, Doppio, B-X, A-Z, C-Y
var miniSwaythlingDoppio = [7][2]string{
	{"A", "X"}, {"B", "Y"}, {"C", "Z"}, {"D", "D"}, {"B", "X"}, {"A", "Z"}, {"C", "Y"},
}

// AssignLetters labels each game with the A/B/C vs X/Y/Z positions of the
// Mini Swaythling con doppio formula.
//
// The portal doesn't say which team was ABC, so it's inferred: games 1-3 fix
// who is A/B/C and X/Y/Z, and in games 5-7 the pairings differ depending on
// which column is ABC (e.g. game 5 is B-X: left B & right X if the left team is
// ABC, but left X & right B otherwise). The hypothesis that explains more of
// games 5-7 wins; a reserve coming in can break one pairing, not the vote.
func (s *Sheet) AssignLetters() {
	if len(s.Games) != len(miniSwaythlingDoppio) || !s.Games[3].Double {
		return
	}
	for i, g := range s.Games {
		if i != 3 && (len(g.Left) != 1 || len(g.Right) != 1) {
			return
		}
	}
	p := func(game int, left bool) string { // single player of game (1-based)
		if left {
			return s.Games[game-1].Left[0]
		}
		return s.Games[game-1].Right[0]
	}

	// Left = ABC: g5 B-X = (L2,R1), g6 A-Z = (L1,R3), g7 C-Y = (L3,R2)
	// Left = XYZ: g5 B-X = (L1,R2), g6 A-Z = (L3,R1), g7 C-Y = (L2,R3)
	score := func(pairs [3][2]int) int {
		n := 0
		for i, pr := range pairs {
			g := 5 + i
			if p(g, true) == p(pr[0], true) {
				n++
			}
			if p(g, false) == p(pr[1], false) {
				n++
			}
		}
		return n
	}
	leftABC := score([3][2]int{{2, 1}, {1, 3}, {3, 2}})
	leftXYZ := score([3][2]int{{1, 2}, {3, 1}, {2, 3}})

	s.LeftSide = "ABC"
	if leftXYZ > leftABC {
		s.LeftSide = "XYZ"
	}
	for i := range s.Games {
		abc, xyz := miniSwaythlingDoppio[i][0], miniSwaythlingDoppio[i][1]
		if s.LeftSide == "ABC" {
			s.Games[i].LeftLetter, s.Games[i].RightLetter = abc, xyz
		} else {
			s.Games[i].LeftLetter, s.Games[i].RightLetter = xyz, abc
		}
	}
}

// Slot is one position in a team's lineup and who filled it.
type Slot struct {
	Letter  string   `json:"letter"`  // A/B/C, X/Y/Z, or D for the double
	Players []string `json:"players"` // usually one; more if a reserve came in. Two names for D.
	Won     int      `json:"won"`
	Lost    int      `json:"lost"`
}

// Lineup returns the formation one side fielded, in letter order
// (A, B, C, Doppio or X, Y, Z, Doppio). left selects the left-column team.
func (s *Sheet) Lineup(left bool) []Slot {
	if s.LeftSide == "" {
		return nil
	}
	order := []string{"A", "B", "C", "D"}
	if (s.LeftSide == "XYZ") == left {
		order = []string{"X", "Y", "Z", "D"}
	}
	slots := make([]Slot, len(order))
	idx := map[string]int{}
	for i, l := range order {
		slots[i].Letter = l
		idx[l] = i
	}

	for _, g := range s.Games {
		letter, players, won := g.RightLetter, g.Right, g.RightWon()
		if left {
			letter, players, won = g.LeftLetter, g.Left, g.LeftWon()
		}
		sl := &slots[idx[letter]]
		if g.Double {
			sl.Players = players
		} else {
			for _, pl := range players {
				if !contains(sl.Players, pl) {
					sl.Players = append(sl.Players, pl)
				}
			}
		}
		if g.Played() {
			if won {
				sl.Won++
			} else {
				sl.Lost++
			}
		}
	}
	return slots
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

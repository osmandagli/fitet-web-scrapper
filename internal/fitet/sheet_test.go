package fitet

import (
	"fmt"
	"os"
	"reflect"
	"testing"
)

func loadSheet(t *testing.T, id int) *Sheet {
	t.Helper()
	raw, err := os.ReadFile(fmt.Sprintf("testdata/giornata_%d.html", id))
	if err != nil {
		t.Fatal(err)
	}
	body, err := toUTF8(raw)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ParseSheet(body)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseSheet(t *testing.T) {
	s := loadSheet(t, 387563) // Bonacossa - 2 5:2 Asca "A", round 1

	if s.LeftTeam != "SSD TT BONACOSSA - 2" || s.RightTeam != `asd tt asca "A"` {
		t.Errorf("teams = %q vs %q", s.LeftTeam, s.RightTeam)
	}
	if len(s.Games) != 7 {
		t.Fatalf("got %d games", len(s.Games))
	}

	g1 := s.Games[0]
	want := Game{No: 1, Left: []string{"DAGLI OSMAN BUGRA"}, Right: []string{"FACCHETTI SIMONE"},
		LeftLetter: "A", RightLetter: "X",
		Sets: [][2]int{{9, 11}, {11, 6}, {8, 11}, {8, 11}}, LeftSets: 1, RightSets: 3}
	if !reflect.DeepEqual(g1, want) {
		t.Errorf("game 1:\n got %+v\nwant %+v", g1, want)
	}

	d := s.Games[3]
	if !d.Double || !reflect.DeepEqual(d.Left, []string{"BARGAGLI P", "DAGLI OSMAN B"}) || d.LeftLetter != "D" || !d.LeftWon() {
		t.Errorf("double parsed wrong: %+v", d)
	}

	won := 0
	for _, g := range s.Games {
		if g.LeftWon() {
			won++
		}
	}
	if won != 5 {
		t.Errorf("left team won %d games, want 5", won)
	}
}

func TestLineup(t *testing.T) {
	s := loadSheet(t, 387563)
	if s.LeftSide != "ABC" {
		t.Fatalf("LeftSide = %q", s.LeftSide)
	}

	home := s.Lineup(true)
	want := []Slot{
		{Letter: "A", Players: []string{"DAGLI OSMAN BUGRA"}, Won: 1, Lost: 1},
		{Letter: "B", Players: []string{"BARGAGLI PAOLO"}, Won: 2},
		{Letter: "C", Players: []string{"FANELLI MATTIA"}, Won: 1, Lost: 1},
		{Letter: "D", Players: []string{"BARGAGLI P", "DAGLI OSMAN B"}, Won: 1},
	}
	if !reflect.DeepEqual(home, want) {
		t.Errorf("home lineup:\n got %+v\nwant %+v", home, want)
	}

	away := s.Lineup(false)
	if away[0].Letter != "X" || away[0].Players[0] != "FACCHETTI SIMONE" ||
		away[1].Players[0] != "MATHY LUCA" || away[2].Players[0] != "CRIPPA BEATRICE" {
		t.Errorf("away lineup: %+v", away)
	}
}

// The home team is not always ABC, and reserves may come in after the double
// (Regolamento art. 1.6.2): both happen in round 1 of C2 GIR C.
func TestLineupSidesAndSubstitutions(t *testing.T) {
	cases := []struct {
		id       int
		leftSide string
		left     bool
		letter   string
		players  []string
	}{
		{387560, "XYZ", false, "C", []string{"PIVATO DIEGO", "GUARDA CRISTIANO"}},   // Saronno reserve in game 7
		{387561, "XYZ", false, "A", []string{"BUCCHERI GIUSEPPE", "AROSIO WALTER"}}, // Monza reserve in game 6
		{387561, "XYZ", true, "X", []string{"STEFANOV STEFAN"}},
		{387562, "ABC", true, "D", []string{"VITALI F", "VAGLIETTI G"}}, // reserve used only in the double
	}
	for _, c := range cases {
		s := loadSheet(t, c.id)
		if s.LeftSide != c.leftSide {
			t.Errorf("%d: LeftSide = %q, want %q", c.id, s.LeftSide, c.leftSide)
		}
		var got []string
		for _, sl := range s.Lineup(c.left) {
			if sl.Letter == c.letter {
				got = sl.Players
			}
		}
		if !reflect.DeepEqual(got, c.players) {
			t.Errorf("%d left=%v slot %s = %v, want %v", c.id, c.left, c.letter, got, c.players)
		}
	}
}

func TestAssignLettersRightSideABC(t *testing.T) {
	// Same sheet with columns swapped: the right team must come out as ABC.
	s := loadSheet(t, 387563)
	for i := range s.Games {
		g := &s.Games[i]
		g.Left, g.Right = g.Right, g.Left
	}
	s.AssignLetters()
	if s.LeftSide != "XYZ" || s.Games[4].RightLetter != "B" || s.Games[4].LeftLetter != "X" {
		t.Errorf("swapped sheet: LeftSide=%q game5=%s-%s", s.LeftSide, s.Games[4].LeftLetter, s.Games[4].RightLetter)
	}
}

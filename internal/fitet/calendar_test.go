package fitet

import (
	"os"
	"testing"
)

func TestParseCalendar(t *testing.T) {
	raw, err := os.ReadFile("testdata/calendario_c2_gir_c.html")
	if err != nil {
		t.Fatal(err)
	}
	body, err := toUTF8(raw)
	if err != nil {
		t.Fatal(err)
	}
	matches, err := ParseCalendar(body)
	if err != nil {
		t.Fatal(err)
	}

	// 8 teams, double round robin: 14 rounds x 4 matches.
	if len(matches) != 56 {
		t.Fatalf("got %d matches, want 56", len(matches))
	}

	byID := map[int]Match{}
	for _, m := range matches {
		byID[m.ID] = m
	}

	first := byID[387560]
	want := Match{ID: 387560, Formula: 19, Round: 1, Date: "10/10/2026", Time: "16.00",
		Home: "ASD US Villa Romanò", Away: "ASD TT SARONNO CAREPHARM",
		Played: true, HomeScore: 6, AwayScore: 1}
	if first != want {
		t.Errorf("first leg:\n got %+v\nwant %+v", first, want)
	}

	ret := byID[387591]
	if ret.Round != 8 || ret.Home != "ASD TT SARONNO CAREPHARM" || ret.Away != "ASD US Villa Romanò" ||
		ret.Date != "16/01/2027" || ret.Played {
		t.Errorf("return leg parsed wrong: %+v", ret)
	}

	played := 0
	for _, m := range matches {
		if m.Played {
			played++
		}
	}
	if played != 4 {
		t.Errorf("got %d played matches in fixture, want 4", played)
	}
}

func TestGroupNameAndWhen(t *testing.T) {
	raw, _ := os.ReadFile("testdata/calendario_c2_gir_c.html")
	body, _ := toUTF8(raw)
	if got := GroupName(body); got != "Serie C2 GIR C" {
		t.Errorf("GroupName = %q", got)
	}
	m := Match{Date: "10/10/2026", Time: "16.00"}
	if got := m.When().Format("2006-01-02 15:04 MST"); got != "2026-10-10 16:00 CEST" {
		t.Errorf("When = %s", got)
	}
}

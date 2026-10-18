package fitet

import (
	"os"
	"testing"
)

func TestParseStandings(t *testing.T) {
	raw, err := os.ReadFile("testdata/classifica_c2_gir_c.html")
	if err != nil {
		t.Fatal(err)
	}
	body, err := toUTF8(raw)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ParseStandings(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 8 {
		t.Fatalf("got %d teams, want 8", len(rows))
	}

	want := Standing{Pos: 1, TeamID: 39020, Team: "ASD US Villa Romanò", Points: 3, Played: 1, Won: 1,
		GamesWon: 6, GamesLost: 1, SetsWon: 20, SetsLost: 7, PointsWon: 280, PointsLost: 211}
	if rows[0] != want {
		t.Errorf("first row:\n got %+v\nwant %+v", rows[0], want)
	}

	var bonacossa *Standing
	for i := range rows {
		if rows[i].Team == "SSD TT BONACOSSA - 2" {
			bonacossa = &rows[i]
		}
	}
	if bonacossa == nil || bonacossa.GamesWon != 5 || bonacossa.GamesLost != 2 || bonacossa.SetsWon != 17 {
		t.Errorf("Bonacossa row wrong: %+v", bonacossa)
	}
}

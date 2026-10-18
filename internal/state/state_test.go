package state

import (
	"path/filepath"
	"testing"

	"github.com/osmandagli/fitet-web-scrapper/internal/fitet"
)

func TestUpdate(t *testing.T) {
	unplayed := fitet.Match{ID: 1, Round: 2, Home: "A", Away: "B"}
	played := fitet.Match{ID: 2, Round: 1, Home: "C", Away: "D", Played: true, HomeScore: 5, AwayScore: 2}

	s := &State{Matches: map[int]fitet.Match{1: unplayed, 2: played}}

	// Nothing changed -> no notifications.
	if c := s.Update([]fitet.Match{unplayed, played}); len(c) != 0 {
		t.Fatalf("expected no changes, got %+v", c)
	}

	// Match 1 gets a result, match 2 gets corrected.
	nowPlayed := unplayed
	nowPlayed.Played, nowPlayed.HomeScore, nowPlayed.AwayScore = true, 4, 3
	fixed := played
	fixed.HomeScore, fixed.AwayScore = 4, 3

	c := s.Update([]fitet.Match{nowPlayed, fixed})
	if len(c) != 2 {
		t.Fatalf("expected 2 changes, got %+v", c)
	}
	if c[0].Kind != Corrected || c[0].OldScore != "5 - 2" {
		t.Errorf("first change should be round-1 correction, got %+v", c[0])
	}
	if c[1].Kind != NewResult || c[1].Match.ID != 1 {
		t.Errorf("second change should be new result for match 1, got %+v", c[1])
	}
}

func TestSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.json")

	s, ok, err := Load(path)
	if err != nil || ok {
		t.Fatalf("first load: ok=%v err=%v", ok, err)
	}
	s.Update([]fitet.Match{{ID: 7, Home: "Villa Romanò", Played: true, HomeScore: 6, AwayScore: 1}})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}

	s2, ok, err := Load(path)
	if err != nil || !ok {
		t.Fatalf("second load: ok=%v err=%v", ok, err)
	}
	if s2.Matches[7] != s.Matches[7] {
		t.Errorf("round trip mismatch: %+v vs %+v", s2.Matches[7], s.Matches[7])
	}
}

// Package state remembers which match results we have already seen, so that
// only new or corrected results trigger a notification.
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/osmandagli/fitet-web-scrapper/internal/fitet"
)

type State struct {
	Matches map[int]fitet.Match `json:"matches"`
}

// Load returns the saved state, or ok=false if there is none yet (first run).
func Load(path string) (s *State, ok bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &State{Matches: map[int]fitet.Match{}}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	s = &State{}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, false, err
	}
	if s.Matches == nil {
		s.Matches = map[int]fitet.Match{}
	}
	return s, true, nil
}

// Save writes atomically so a crash mid-write can't corrupt the state file.
func (s *State) Save(path string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type ChangeKind string

const (
	NewResult ChangeKind = "new"
	Corrected ChangeKind = "corrected" // score changed after it was first published
)

type Change struct {
	Kind     ChangeKind
	Match    fitet.Match
	OldScore string // set for Corrected
}

// Update replaces the stored matches with current and reports result changes.
// Rescheduling (date/time edits) is stored but not reported.
func (s *State) Update(current []fitet.Match) []Change {
	var changes []Change
	for _, m := range current {
		old, seen := s.Matches[m.ID]
		switch {
		case m.Played && (!seen || !old.Played):
			changes = append(changes, Change{Kind: NewResult, Match: m})
		case m.Played && old.Played && m.Score() != old.Score():
			changes = append(changes, Change{Kind: Corrected, Match: m, OldScore: old.Score()})
		}
	}

	s.Matches = make(map[int]fitet.Match, len(current))
	for _, m := range current {
		s.Matches[m.ID] = m
	}

	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Match.Round != changes[j].Match.Round {
			return changes[i].Match.Round < changes[j].Match.Round
		}
		return changes[i].Match.ID < changes[j].Match.ID
	})
	return changes
}

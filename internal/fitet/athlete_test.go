package fitet

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	body, err := toUTF8(raw)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestParseAthleteSearch(t *testing.T) {
	refs, err := parseAthleteSearch(fixture(t, "search_perego.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := []AthleteRef{{ID: 767879, Name: "PEREGO ANDREA", Born: "16/08/2011", Card: "973978"}}
	if !reflect.DeepEqual(refs, want) {
		t.Errorf("got %+v", refs)
	}
	if refs, err := parseAthleteSearch([]byte("  ")); err != nil || refs != nil {
		t.Errorf("empty body: %v %v", refs, err)
	}
}

func TestFindAthletesSendsAjaxHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
			return // the real endpoint answers with nothing
		}
		if r.URL.Query().Get("term") != "PEREGO ANDREA" {
			t.Errorf("term = %q", r.URL.Query().Get("term"))
		}
		w.Write(fixture(t, "search_perego.json"))
	}))
	defer srv.Close()
	c := NewClient()
	c.BaseURL = srv.URL
	refs, err := c.FindAthletes(context.Background(), "PEREGO ANDREA")
	if err != nil || len(refs) != 1 || refs[0].ID != 767879 {
		t.Errorf("refs=%+v err=%v", refs, err)
	}
}

func TestParseAthleteSeason(t *testing.T) {
	s, err := ParseAthleteSeason(fixture(t, "atleta_812366.html"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "DAGLI OSMAN BUGRA" || s.Season != "2026/27" {
		t.Errorf("header: %q %q", s.Name, s.Season)
	}
	if len(s.Tournaments) != 1 || s.Tournaments[0].Date != "29/08/2026" || len(s.Tournaments[0].Matches) != 3 {
		t.Fatalf("tournaments: %+v", s.Tournaments)
	}

	// "CARMINATI MATTEO  DAGLI OSMAN BUGRA  3 - 1": listed second, so it's a 1-3 loss.
	got := s.Tournaments[0].Matches[1]
	want := AthleteMatch{Event: "Singolo M/F Open3 451-2500M | 26-200F", Stage: "GQ",
		Opponent: "CARMINATI MATTEO", For: 1, Against: 3, Won: false, Ranked: true}
	if got != want {
		t.Errorf("tournament match:\n got %+v\nwant %+v", got, want)
	}
	if !s.Tournaments[0].Matches[0].Won || s.Tournaments[0].Matches[2].Stage != "3T" {
		t.Errorf("tournament matches: %+v", s.Tournaments[0].Matches)
	}

	if len(s.Team) != 2 || s.Team[0].Opponent != "FACCHETTI SIMONE" || s.Team[0].Won ||
		s.Team[1].Opponent != "CRIPPA BEATRICE" || !s.Team[1].Won || s.Team[1].Event != "C2 GIR C (1^ gior.)" {
		t.Errorf("team: %+v", s.Team)
	}
	if len(s.Playoff) != 0 {
		t.Errorf("playoff: %+v", s.Playoff)
	}
}

func TestParseAthleteSeasonMany(t *testing.T) {
	s, err := ParseAthleteSeason(fixture(t, "atleta_813751.html"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, tr := range s.Tournaments {
		n += len(tr.Matches)
	}
	if len(s.Tournaments) != 6 || n != 26 || len(s.Team) != 2 {
		t.Errorf("got %d tournaments, %d matches, %d team matches", len(s.Tournaments), n, len(s.Team))
	}
	last := s.Tournaments[2].Matches[4] // Cortemaggiore quarter-final
	if last.Stage != "QF" || last.Opponent != "MILANI DAVIDE" || last.Won {
		t.Errorf("QF: %+v", last)
	}
}

func TestStageName(t *testing.T) {
	for code, want := range map[string]string{"GQ": "Group", "3T": "Round 3", "OF": "Last 16", "QF": "Quarter-final", "XX": "XX"} {
		if got := StageName(code); got != want {
			t.Errorf("StageName(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestParseRanking(t *testing.T) {
	r, err := ParseRanking(fixture(t, "ranking_813751.html"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "BARGAGLI PAOLO" || r.Country != "Italia" || r.Category != "4^" || r.Points != 6219.81 ||
		r.Position != 1087 || r.Sector != "Master 65-70" || r.Region != "Lombardia" ||
		r.Club != "S.S. Dilettantistica T.T. Bonacossa Milano S.R.L." || r.Best != "n.5 il 30/06/1983" || r.AsOf != "27/09/2026" {
		t.Errorf("card: %+v", r)
	}
	if r.StartPoints != 5791.81 || r.Gained != 428 || r.Played != 26 || r.Won != 15 || r.Lost != 11 {
		t.Errorf("season: start=%v gained=%v %d %d-%d", r.StartPoints, r.Gained, r.Played, r.Won, r.Lost)
	}
	if len(r.History) == 0 || r.History[0] != (RankPoint{Date: "2026-09-27", Position: 1087, Points: 6220, Category: "4"}) {
		t.Errorf("history[0]: %+v", r.History)
	}
	if r.Gender != "M" || r.RankingName() != "men's ranking" {
		t.Errorf("gender %q", r.Gender)
	}
	if r.CategoryLabel() != "4ª cat" {
		t.Errorf("label %q", r.CategoryLabel())
	}
}

func TestParseRankingForeign(t *testing.T) {
	r, err := ParseRanking(fixture(t, "ranking_812366.html"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Category != "Straniero" || r.Points != 6159.48 || r.Position != 0 || r.Best != "" || r.CategoryLabel() != "Foreign" {
		t.Errorf("foreign card: %+v", r)
	}
	if r.Played != 3 || r.Won != 1 || r.Lost != 2 || r.Gained != 87 {
		t.Errorf("season: %+v", r)
	}
	// History goes back to the first ranking, and is not duplicated across tabs.
	dates := map[string]bool{}
	for _, h := range r.History {
		if dates[h.Date] {
			t.Errorf("duplicate history date %s", h.Date)
		}
		dates[h.Date] = true
	}
	if !dates["2024-09-29"] || r.History[0].Points != 6159 {
		t.Errorf("history: %+v", r.History)
	}
}

func TestLatestRankingClass(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture(t, "menu.html"))
	}))
	defer srv.Close()
	c := NewClient()
	c.BaseURL = srv.URL + "/risultati"
	id, err := c.LatestRankingClass(context.Background())
	if err != nil || id != 247 {
		t.Errorf("id=%d err=%v", id, err)
	}
}

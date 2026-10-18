package fitet

import (
	"os"
	"strings"
	"testing"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	b, err = toUTF8(b)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseTournamentList(t *testing.T) {
	ts, err := ParseTournamentList(readTestdata(t, "tornei_elenco_100.html"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 4 {
		t.Fatalf("got %d tournaments, want 4: %+v", len(ts), ts)
	}
	if got := ts[0]; got.ID != 1 || got.Date != "29/08/2026" || got.Place != "Saronno (VA)" || !got.Played || got.Kinds[0] != 100 {
		t.Errorf("first: %+v", got)
	}
	if got := ts[3]; got.ID != 8 || got.Played || got.Place != "Vedano Olona (VA)" {
		t.Errorf("upcoming: %+v", got)
	}

	ts, err = ParseTournamentList(readTestdata(t, "tornei_elenco_vuoto.html"), 110)
	if err != nil || len(ts) != 0 {
		t.Errorf("empty list: %v %+v", err, ts)
	}
}

func TestParseTournamentInfo(t *testing.T) {
	info, err := ParseTournamentInfo(readTestdata(t, "tornei_gare_1.html"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "Torneo Open memorial 'Christian Noviello'" || info.Where != "Saronno (VA) - 29/08/2026" ||
		info.DrawsPath != "tornei/tabelloni/1_4_home.html" || len(info.Events) != 4 {
		t.Errorf("played: %+v", info)
	}
	if e := info.Events[1]; e.Gara != 1022 || e.Name != "Singolo M/F Open3 451-2500M | 26-200F" {
		t.Errorf("event: %+v", e)
	}

	info, err = ParseTournamentInfo(readTestdata(t, "tornei_iscrizioni_8.html"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "Torneo Open città di Vedano Olona" || !strings.Contains(info.Where, "dal 31/10/2026") ||
		info.DrawsPath != "" || len(info.Events) != 4 {
		t.Errorf("upcoming: %+v", info)
	}
}

func TestParseDrawIndex(t *testing.T) {
	pages, err := ParseDrawIndex(readTestdata(t, "tornei_tabelloni_1_4_home.html"), "tornei/tabelloni")
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 8 {
		t.Fatalf("got %d pages", len(pages))
	}
	if p := pages[3]; p.Event != "Singolo M/F Open3 451-2500M | 26-200F" || !p.Knockout || p.Path != "tornei/tabelloni/1_4_home-SXO52-0003.html" {
		t.Errorf("page: %+v", p)
	}
	if pages[2].Knockout {
		t.Error("gironi page marked as knockout")
	}
}

func TestParseGroups(t *testing.T) {
	gs, err := ParseGroups(readTestdata(t, "tornei_gironi_open3.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(gs) < 20 {
		t.Fatalf("got %d groups", len(gs))
	}
	g := gs[0]
	if g.No != 1 || len(g.Matches) != 3 || len(g.Standings) != 3 {
		t.Fatalf("group 1: %+v", g)
	}
	if m := g.Matches[0]; m.A != "Pappalardo Simone Sebastiano" || m.B != "Tregambe Roman" || m.SetsA != 1 || m.SetsB != 3 ||
		strings.Join(m.Sets, " ") != "7-11 12-10 14-16 10-12" {
		t.Errorf("match: %+v", m)
	}
	if s := g.Standings[0]; s.Pos != 1 || s.Name != "Tregambe Roman" || s.Points != 4 || s.Played != 2 || s.Won != 2 || s.Lost != 0 {
		t.Errorf("standing: %+v", s)
	}
}

func TestParseBracket(t *testing.T) {
	b, err := ParseBracket(readTestdata(t, "tornei_tabellone_open3.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Rounds) != 6 || len(b.Rounds[0]) != 32 || len(b.Rounds[5]) != 1 {
		t.Fatalf("rounds: %d, first %d", len(b.Rounds), len(b.Rounds[0]))
	}
	// Round 1: Tregambe had a bye.
	if m := b.Rounds[0][0]; m.A != "Tregambe Roman" || m.B != "" || m.Winner != "Tregambe Roman" || m.WonBy != "" {
		t.Errorf("bye: %+v", m)
	}
	if m := b.Rounds[0][1]; m.A != "Giannotti Federico" || m.B != "Loche Daniele" || m.Winner != "Giannotti Federico" ||
		m.WonBy != "3-1" || strings.Join(m.Sets, " ") != "11-8 11-3 6-11 11-8" {
		t.Errorf("round 1: %+v", m)
	}
	final := b.Rounds[5][0]
	if final.A != "Noviello Nicholas" || final.B != "Gemma Ivan" || final.Winner != "Gemma Ivan" || final.Loser() != "Noviello Nicholas" ||
		final.WonBy != "3-0" || strings.Join(final.Sets, " ") != "11-9 13-11 11-9" {
		t.Errorf("final: %+v", final)
	}
}

func TestWinnerSets(t *testing.T) {
	sets, by := winnerSets("5,-12,-8,8,9")
	if strings.Join(sets, " ") != "11-5 12-14 8-11 11-8 11-9" || by != "3-2" {
		t.Errorf("%v %s", sets, by)
	}
	if sets, by := winnerSets("-0,12,11,10"); strings.Join(sets, " ") != "0-11 14-12 13-11 12-10" || by != "3-1" {
		t.Errorf("%v %s", sets, by)
	}
}

func TestParseEntries(t *testing.T) {
	es, err := ParseEntries(readTestdata(t, "tornei_iscrizioni_gara_1022.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 48 {
		t.Fatalf("got %d entries", len(es))
	}
	if e := es[0]; e.Name != "BARGAGLI PAOLO" || e.Points != 6220 || e.Club != "S.S. Dilettantistica T.T. Bonacossa Milano S.R.L." {
		t.Errorf("first: %+v", e)
	}
	if es[1].Points != 6812 {
		t.Errorf("points with star: %+v", es[1])
	}
}

func TestParseRankingDetails(t *testing.T) {
	d, err := ParseRankingDetails(readTestdata(t, "punti_813751.html"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Start != 5792 || d.Championship != 0 || d.Tournaments != 338 || d.TournamentWins != 90 || d.Final != 6220 {
		t.Errorf("summary: %+v", d)
	}
	if len(d.Matches) != 26 || len(d.Placements) != 6 || len(d.GroupWins) != 6 {
		t.Fatalf("got %d matches, %d placements, %d group wins", len(d.Matches), len(d.Placements), len(d.GroupWins))
	}
	if m := d.Matches[1]; m.Opponent != "CAMPANALE MARCO" || m.Won || m.Counted || m.Phase != "GIR" {
		t.Errorf("discarded loss: %+v", m)
	}
	if p := d.Placements[0]; p.Tournament != "Torneo Open di Montichiari" || p.Points != 107 || p.Pos != "QF" || p.Level != "Open4" || !p.Counted {
		t.Errorf("placement: %+v", p)
	}
	if g := d.GroupWins[1]; g.Tournament != "MEMORIAL LUCA DE LUCA" || g.PerWin != 10 || g.Active != 2 || g.Points != 20 {
		t.Errorf("group wins: %+v", g)
	}

	d, err = ParseRankingDetails(readTestdata(t, "punti_812366.html"))
	if err != nil || d.Final != 6159 || d.Placements[0].Pos != "32F" || d.GroupWins[0].Points != 15 {
		t.Errorf("Dagli: %v %+v", err, d)
	}
}

func TestRankingDetailsPath(t *testing.T) {
	r, err := ParseRanking(readTestdata(t, "ranking_813751.html"))
	if err != nil {
		t.Fatal(err)
	}
	if r.DetailsPath != "new_rank/html/247/Maschile/00/0c/6a/b7/details_page_1306.html" {
		t.Errorf("details path %q", r.DetailsPath)
	}
}

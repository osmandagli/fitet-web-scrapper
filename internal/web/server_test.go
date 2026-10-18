package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/osmandagli/fitet-web-scrapper/internal/fitet"
)

// fakePortal serves the saved FITET pages from internal/fitet/testdata.
func fakePortal(t *testing.T) *httptest.Server {
	t.Helper()
	pages := map[string]string{
		"/campionati/Calendario.asp":         "../fitet/testdata/calendario_c2_gir_c.html",
		"/campionati/classifica_squadre.php": "../fitet/testdata/classifica_c2_gir_c.html",
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, ok := pages[r.URL.Path]
		id := r.URL.Query().Get("ATLETA")
		switch r.URL.Path {
		case "/menu.php":
			file, ok = "../fitet/testdata/menu.html", true
		case "/new_rank/ajax.php":
			// Two group players are "registered"; everyone else isn't found.
			hits := map[string]string{
				"DAGLI OSMAN BUGRA": `[{"id":"812366","value":"DAGLI OSMAN BUGRA","label":"DAGLI OSMAN BUGRA (14\/09\/2000) [1011752]"}]`,
				"BARGAGLI PAOLO":    `[{"id":"813751","value":"BARGAGLI PAOLO","label":"BARGAGLI PAOLO (10\/11\/1959) [1012569]"}]`,
				"ROSSI":             `[{"id":"1","value":"ROSSI MARIO","label":"x"},{"id":"2","value":"ROSSI LUCA","label":"y"}]`,
			}
			w.Write([]byte(hits[r.URL.Query().Get("term")]))
			return
		case "/new_rank/dettaglioatleta_unica.php":
			file, ok = "../fitet/testdata/ranking_"+id+".html", true
		case "/incontri_atleta_acc.php":
			file, ok = "../fitet/testdata/atleta_"+id+".html", true
		}
		if ok {
			if _, err := os.Stat(file); err != nil {
				ok = false
			}
		}
		if r.URL.Path == "/campionati/giornata.php" {
			file = "../fitet/testdata/giornata_" + r.URL.Query().Get("INCONTRO") + ".html"
			if _, err := os.Stat(file); err == nil {
				ok = true
			}
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Error(err)
		}
		w.Write(b)
	}))
}

func init() { requestDelay = 0 }

func TestIndexPage(t *testing.T) {
	portal := fakePortal(t)
	defer portal.Close()

	client := fitet.NewClient()
	client.BaseURL = portal.URL
	s := NewServer(client, Options{Group: fitet.Group{CAM: 408, ANNO: 41}, Team: "BONACOSSA - 2",
		Player: "DAGLI OSMAN", Every: 15 * time.Minute, CacheDir: t.TempDir()})
	s.refresh(context.Background())

	app := httptest.NewServer(s.Handler())
	defer app.Close()

	resp, err := http.Get(app.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	html := string(body)

	for _, want := range []string{
		"Serie C2 GIR C",
		"ASD US Villa Romanò", // encoding survived
		`class="mine"`,        // my team highlighted in standings
		`@ <a class="tl" href="/team/39196">ASD TT MONZA - ABC Nursery</a>`, // next match card, linked
		`<span class="pill V"`,              // won round 1
		"Giornata 14",                       // return leg rounds present, in Italian
		"1<span class=\"sub\"> / 14</span>", // our matches, not the whole group's
		`href="/team/39067"`,                // team names link to team pages
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page missing %q", want)
		}
	}

	// The English site lives under /en/ and links stay inside it.
	resp, err = http.Get(app.URL + "/en/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{"Round 14", "Standings", `<html lang="en">`, `href="/en/team/39067"`, `<a href="/" lang="it"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("English page missing %q", want)
		}
	}

	resp, err = http.Get(app.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	var st status
	json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if st.Version != 3 || st.Error != "" { // 1 = results, 2 = score sheets, 3 = players
		t.Errorf("status = %+v", st)
	}

	// Same data again: version must not bump (so open pages don't reload).
	s.refresh(context.Background())
	if s.snap.Version != 3 {
		t.Errorf("version bumped without changes: %d", s.snap.Version)
	}
}

func TestTeamPage(t *testing.T) {
	portal := fakePortal(t)
	defer portal.Close()

	client := fitet.NewClient()
	client.BaseURL = portal.URL
	cache := t.TempDir()
	opt := Options{Group: fitet.Group{CAM: 408, ANNO: 41}, Team: "BONACOSSA - 2",
		Player: "DAGLI OSMAN", Every: 15 * time.Minute, CacheDir: cache}
	s := NewServer(client, opt)
	s.refresh(context.Background())
	if len(s.snap.Sheets) != 4 {
		t.Fatalf("fetched %d sheets, want 4", len(s.snap.Sheets))
	}

	app := httptest.NewServer(s.Handler())
	defer app.Close()

	get := func(path string) string {
		resp, err := http.Get(app.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: %s", path, resp.Status)
		}
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}

	// Our team: lineup ABC with me as A.
	html := get("/team/39067")
	for _, want := range []string{
		"SSD TT BONACOSSA - 2",
		"Formazione · ABC",
		`<span class="me">DAGLI OSMAN BUGRA</span>`,
		"BARGAGLI PAOLO",
		"A ×1", // positions column
		"11-6", // a set from our side (game 1, set 2)
	} {
		if !strings.Contains(html, want) {
			t.Errorf("team page missing %q", want)
		}
	}

	// Saronno played away as ABC and brought a reserve in at C.
	html = get("/team/39024")
	for _, want := range []string{"Formazione · ABC", "PIVATO DIEGO", `<span class="sub-in">→ <a class="tl" href="/player/find?name=GUARDA&#43;CRISTIANO">GUARDA CRISTIANO</a>`} {
		if !strings.Contains(html, want) {
			t.Errorf("Saronno page missing %q", want)
		}
	}

	resp, _ := http.Get(app.URL + "/team/999")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown team: %s", resp.Status)
	}

	// A restarted server reuses the cache instead of re-downloading.
	portal.Close()
	s2 := NewServer(client, opt)
	if len(s2.snap.Sheets) != 4 {
		t.Errorf("cache reload: %d sheets", len(s2.snap.Sheets))
	}
}

func TestPlayerPages(t *testing.T) {
	portal := fakePortal(t)
	defer portal.Close()

	client := fitet.NewClient()
	client.BaseURL = portal.URL
	s := NewServer(client, Options{Group: fitet.Group{CAM: 408, ANNO: 41}, Team: "BONACOSSA - 2",
		Player: "DAGLI OSMAN", Every: 15 * time.Minute, CacheDir: t.TempDir()})
	s.refresh(context.Background())

	if p := s.players["BARGAGLI PAOLO"]; p == nil || p.Ref.ID != 813751 || p.Ranking == nil || p.Ranking.Position != 1087 {
		t.Fatalf("Bargagli not resolved: %+v", p)
	}
	if p := s.players["FACCHETTI SIMONE"]; p == nil || !p.Missing {
		t.Errorf("unknown player should be marked missing: %+v", p)
	}

	app := httptest.NewServer(s.Handler())
	defer app.Close()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string) (int, string, string) {
		resp, err := noRedirect.Get(app.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Location"), string(b)
	}

	// Team page: ranking beside names, names link to player pages.
	_, _, html := get("/team/39067")
	for _, want := range []string{
		`<div class="rk">4ª cat · 6.219,81 punti · <span title="classifica maschile">#1087</span></div>`,
		`<div class="rk">Straniero · 6.159,48 punti</div>`,
		`href="/player/812366"`,
		`href="/player/find?name=FACCHETTI&#43;SIMONE"`, // unknown: goes through search
	} {
		if !strings.Contains(html, want) {
			t.Errorf("team page missing %q", want)
		}
	}

	// Player page: full FITET season + ranking.
	code, _, html := get("/player/813751")
	if code != 200 {
		t.Fatalf("player page: %d", code)
	}
	for _, want := range []string{
		"BARGAGLI PAOLO", "6.219,81", "n. 1087 nella classifica maschile", "n.5 il 30/06/1983", "Master 65-70",
		"MEMORIAL LUCA DE LUCA del 05/09/2026 a Nerviano (MI)",
		"Quarti di finale", "Campionati a squadre", `<path d="M0.0 `,
		`<span class="ev-name" title="Categoria giocata">Singolo Maschile 4^ cat</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("player page missing %q", want)
		}
	}
	_, _, html = get("/en/player/813751")
	for _, want := range []string{
		"6,219.81", "#1087 in the men&#39;s ranking", "Quarter-final", "Team championships",
		`<span class="ev-name" title="Category played">Singolo Maschile 4^ cat</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("English player page missing %q", want)
		}
	}

	// Abbreviated doubles names and full names redirect to the known player.
	if code, loc, _ := get("/player/find?name=BARGAGLI+P"); code != http.StatusFound || loc != "/player/813751" {
		t.Errorf("find abbreviated: %d %q", code, loc)
	}
	if code, loc, _ := get("/en/player/find?name=BARGAGLI+P"); code != http.StatusFound || loc != "/en/player/813751" {
		t.Errorf("English find: %d %q", code, loc)
	}
	// Homonyms get a chooser.
	if code, _, html := get("/player/find?name=ROSSI"); code != 200 || !strings.Contains(html, `href="/player/2"`) {
		t.Errorf("chooser: %d", code)
	}
}

func TestRankChartBreaksAtGaps(t *testing.T) {
	c := newRankChart([]fitet.RankPoint{
		{Date: "2026-09-27", Points: 6220}, {Date: "2026-08-30", Points: 6100},
		{Date: "2001-06-30", Points: 2500}, {Date: "1990-06-30", Points: 100},
	})
	if strings.Count(c.Path, "M") != 3 { // 1990 | 2001 | 2026 pair
		t.Errorf("path should break at gaps: %s", c.Path)
	}
	if len(c.XTicks) > 8 {
		t.Errorf("too many year ticks: %d", len(c.XTicks))
	}
	if c.Last.Points != 6220 || c.Last.X != 100 {
		t.Errorf("last point: %+v", c.Last)
	}
}

func TestGroupEvents(t *testing.T) {
	ev := groupEvents([]fitet.AthleteMatch{
		{Event: "Singolo Maschile 4^ cat", Stage: "GQ", Won: true},
		{Event: "Singolo M/F Open3", Stage: "GQ", Won: false},
		{Event: "Singolo Maschile 4^ CAT", Stage: "F", Won: true}, // same category, other spelling
		{Event: "Singolo M/F Open3", Stage: "OF", Won: false},
	})
	if len(ev) != 2 || ev[0].Name != "Singolo Maschile 4^ cat" || len(ev[0].Matches) != 2 || !ev[0].Winner {
		t.Errorf("4^ cat event: %+v", ev)
	}
	if ev[1].Reached != "OF" || ev[1].Winner || ev[1].Lost != 2 {
		t.Errorf("open event: %+v", ev[1])
	}
}

func TestStaticBuild(t *testing.T) {
	portal := fakePortal(t)
	defer portal.Close()

	client := fitet.NewClient()
	client.BaseURL = portal.URL
	s := NewServer(client, Options{Group: fitet.Group{CAM: 408, ANNO: 41}, Team: "BONACOSSA - 2",
		Player: "DAGLI OSMAN", Every: 6 * time.Hour, CacheDir: t.TempDir()})
	out := t.TempDir()
	if err := s.Build(context.Background(), out); err != nil {
		t.Fatal(err)
	}

	read := func(path string) string {
		b, err := os.ReadFile(out + "/" + path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	html := read("index.html")
	for _, want := range []string{"Serie C2 GIR C", `href="/team/39067"`, "aggiornato "} {
		if !strings.Contains(html, want) {
			t.Errorf("index missing %q", want)
		}
	}
	for _, unwanted := range []string{`id="refresh"`, "/api/status"} {
		if strings.Contains(html, unwanted) {
			t.Errorf("static index still has %q", unwanted)
		}
	}

	html = read("team/39067.html")
	for _, want := range []string{
		`href="/player/812366"`, `href="/player/813751"`,
		"FACCHETTI SIMONE", // not found on FITET: plain text, no search link
	} {
		if !strings.Contains(html, want) {
			t.Errorf("team page missing %q", want)
		}
	}
	if strings.Contains(html, "/player/find") {
		t.Error("static team page links to the player search")
	}
	read("team/39024.html")
	if html = read("player/813751.html"); !strings.Contains(html, "BARGAGLI PAOLO") || !strings.Contains(html, "6.219,81") {
		t.Error("player page incomplete")
	}
	html = read("en/team/39067.html")
	for _, want := range []string{"Recent matches", `href="/en/player/812366"`, `href="/team/39067" lang="it"`} {
		if !strings.Contains(html, want) {
			t.Errorf("English team page missing %q", want)
		}
	}
	if html = read("en/player/813751.html"); !strings.Contains(html, "6,219.81") {
		t.Error("English player page incomplete")
	}
}

func TestStaticBuildRefusesWithoutData(t *testing.T) {
	portal := fakePortal(t)
	portal.Close() // FITET unreachable

	client := fitet.NewClient()
	client.BaseURL = portal.URL
	s := NewServer(client, Options{Group: fitet.Group{CAM: 408, ANNO: 41}, Every: 6 * time.Hour})
	out := t.TempDir()
	if err := s.Build(context.Background(), out); err == nil {
		t.Fatal("build should fail when the portal can't be reached")
	}
	if _, err := os.Stat(out + "/index.html"); err == nil {
		t.Error("wrote a site without data")
	}
}

// Every text a template translates must have an Italian version.
func TestItalianCoversTemplates(t *testing.T) {
	files, _ := filepath.Glob("templates/*.html")
	re := regexp.MustCompile(`\{\{-?\s*\(?tf? "([^"]+)"`)
	keys := map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			keys[m[1]] = true
		}
	}
	// Texts that come from Go code rather than template literals.
	for _, k := range []string{"Not in the national ranking", "Tournaments", "Team championships", "Play-off / out",
		"Foreign", "men's ranking", "women's ranking", "national ranking"} {
		keys[k] = true
	}
	if len(keys) < 50 {
		t.Fatalf("found only %d texts; is the pattern still right?", len(keys))
	}
	for k := range keys {
		if _, ok := italian[k]; !ok {
			t.Errorf("no Italian for %q", k)
		}
	}
}

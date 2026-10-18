// Package web serves a local results page for one FITET group and keeps it
// up to date by re-scraping the portal in the background.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/osmandagli/fitet-web-scrapper/internal/fitet"
)

//go:embed templates/*.html
var templateFS embed.FS

const (
	// minManualRefresh limits how often the "refresh" button may hit the portal.
	minManualRefresh = time.Minute
)

// requestDelay spaces out background requests so we don't hammer the portal.
var requestDelay = 700 * time.Millisecond

// Snapshot is everything scraped for the group at one point in time.
type Snapshot struct {
	GroupName string               `json:"group_name"`
	Standings []fitet.Standing     `json:"standings"`
	Matches   []fitet.Match        `json:"matches"`
	Sheets    map[int]*fitet.Sheet `json:"sheets"`     // score sheets of played matches, by match id
	CheckedAt time.Time            `json:"checked_at"` // last fetch attempt that succeeded
	ChangedAt time.Time            `json:"changed_at"` // last time the data actually changed
	Version   int                  `json:"version"`    // bumps when data changes; the page reloads on it
	Error     string               `json:"error,omitempty"`
}

type Options struct {
	Group    fitet.Group
	Team     string        // substring identifying "my" team, e.g. "BONACOSSA - 2"
	Player   string        // substring identifying me in lineups, e.g. "DAGLI OSMAN"
	Every    time.Duration // how often to re-scrape
	CacheDir string        // where score sheets and players are cached between runs; "" = no cache
	Static   bool          // pages are pre-rendered files: no live polling, no refresh button
}

type Server struct {
	Client *fitet.Client
	Options

	tmpl    map[string]*template.Template // by language
	fetchMu sync.Mutex                    // one scrape at a time

	mu        sync.RWMutex
	snap      Snapshot
	sheetFor  map[int]string // match id -> score the cached sheet belongs to
	attempted time.Time
	newSince  map[int]time.Time // match id -> when its result first appeared while we were running

	players     map[string]*playerInfo // by full name, as written on score sheets
	rankClass   int                    // latest ranking publication (ID_CLASS)
	rankClassAt time.Time

	pageMu sync.Mutex // one on-demand player page fetch at a time
	pages  map[int]*playerPage
}

func NewServer(c *fitet.Client, opt Options) *Server {
	s := &Server{
		Client:   c,
		Options:  opt,
		snap:     Snapshot{Sheets: map[int]*fitet.Sheet{}},
		sheetFor: map[int]string{},
		newSince: map[int]time.Time{},
		players:  map[string]*playerInfo{},
		pages:    map[int]*playerPage{},
	}
	funcs := template.FuncMap{
		"mine":   s.isMine,
		"me":     s.isMe,
		"iso":    func(t time.Time) string { return t.Format(time.RFC3339) },
		"sub":    func(a, b int) int { return a - b },
		"signed": func(n int) string { return fmt.Sprintf("%+d", n) },
		"pct":    pct,
		"rank":   s.rankingOf,
		"static": func() bool { return s.Static },
	}
	for k, f := range s.langFuncs(langEN) {
		funcs[k] = f
	}
	base := template.Must(template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html"))
	s.tmpl = map[string]*template.Template{}
	for _, l := range langs {
		s.tmpl[l] = template.Must(base.Clone()).Funcs(s.langFuncs(l))
	}
	s.loadCache()
	s.loadPlayers()
	return s
}

// Run scrapes immediately, then every s.Every until ctx is cancelled.
func (s *Server) Run(ctx context.Context) {
	s.refresh(ctx)
	t := time.NewTicker(s.Every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.refresh(ctx)
		}
	}
}

func (s *Server) refresh(ctx context.Context) {
	if !s.fetchMu.TryLock() {
		return // a scrape is already running
	}
	defer s.fetchMu.Unlock()

	s.mu.Lock()
	s.attempted = time.Now()
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	name, matches, err := s.Client.Calendar(ctx, s.Group)
	var standings []fitet.Standing
	if err == nil {
		standings, err = s.Client.Standings(ctx, s.Group.CAM)
	}
	if err != nil {
		log.Printf("refresh failed: %v", err)
		s.mu.Lock()
		s.snap.Error = err.Error() // keep showing the last good data
		s.mu.Unlock()
		return
	}
	s.applyResults(name, matches, standings)
	s.fetchSheets(ctx, matches)
	s.resolvePlayers(ctx)
}

func (s *Server) applyResults(name string, matches []fitet.Match, standings []fitet.Standing) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	old := s.snap
	if old.Version > 0 {
		prev := make(map[int]fitet.Match, len(old.Matches))
		for _, m := range old.Matches {
			prev[m.ID] = m
		}
		for _, m := range matches {
			if p, ok := prev[m.ID]; m.Played && (!ok || p.Score() != m.Score()) {
				s.newSince[m.ID] = now
				log.Printf("new result: %s", m)
			}
		}
	}

	s.snap.Error = ""
	s.snap.CheckedAt = now
	if old.Version == 0 || name != old.GroupName ||
		!reflect.DeepEqual(matches, old.Matches) || !reflect.DeepEqual(standings, old.Standings) {
		s.snap.GroupName, s.snap.Matches, s.snap.Standings = name, matches, standings
		s.bump(now)
		log.Printf("data updated (version %d): %d matches, %d teams", s.snap.Version, len(matches), len(standings))
	}
}

// fetchSheets downloads score sheets for played matches we don't have yet, or
// whose score changed since we fetched them (a correction).
func (s *Server) fetchSheets(ctx context.Context, matches []fitet.Match) {
	var todo []fitet.Match
	s.mu.RLock()
	for _, m := range matches {
		if m.Played && s.sheetFor[m.ID] != m.Score() {
			todo = append(todo, m)
		}
	}
	s.mu.RUnlock()
	if len(todo) == 0 {
		return
	}

	got := 0
	for i, m := range todo {
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(requestDelay):
			}
		}
		formula := m.Formula
		if formula == 0 {
			formula = 19
		}
		sh, err := s.Client.Sheet(ctx, s.Group.CAM, m.ID, formula)
		if err != nil {
			log.Printf("score sheet %d: %v", m.ID, err)
			continue
		}
		s.mu.Lock()
		s.snap.Sheets[m.ID] = sh
		s.sheetFor[m.ID] = m.Score()
		s.mu.Unlock()
		got++
	}
	if got > 0 {
		s.mu.Lock()
		s.bump(time.Now())
		s.mu.Unlock()
		log.Printf("fetched %d score sheet(s)", got)
		s.saveCache()
	}
}

func (s *Server) bump(now time.Time) {
	s.snap.ChangedAt = now
	s.snap.Version++
}

// --- sheet cache on disk -----------------------------------------------------

type cachedSheet struct {
	Score string       `json:"score"`
	Sheet *fitet.Sheet `json:"sheet"`
}

func (s *Server) sheetsPath() string {
	if s.CacheDir == "" {
		return ""
	}
	return filepath.Join(s.CacheDir, "sheets.json")
}

func (s *Server) loadCache() {
	if s.CacheDir == "" {
		return
	}
	b, err := os.ReadFile(s.sheetsPath())
	if err != nil {
		return
	}
	var c map[int]cachedSheet
	if err := json.Unmarshal(b, &c); err != nil {
		log.Printf("ignoring unreadable sheet cache %s: %v", s.sheetsPath(), err)
		return
	}
	for id, cs := range c {
		if cs.Sheet != nil {
			s.snap.Sheets[id] = cs.Sheet
			s.sheetFor[id] = cs.Score
		}
	}
	log.Printf("loaded %d cached score sheet(s)", len(c))
}

func (s *Server) saveCache() {
	if s.CacheDir == "" {
		return
	}
	s.mu.RLock()
	c := make(map[int]cachedSheet, len(s.snap.Sheets))
	for id, sh := range s.snap.Sheets {
		c[id] = cachedSheet{Score: s.sheetFor[id], Sheet: sh}
	}
	s.mu.RUnlock()

	b, err := json.Marshal(c)
	if err == nil {
		err = writeFileAtomic(s.sheetsPath(), b)
	}
	if err != nil {
		log.Printf("saving sheet cache: %v", err)
	}
}

func writeFileAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// --- HTTP --------------------------------------------------------------------

// Handler serves the Italian site at / and the English one under /en/.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /team/{id}", s.handleTeam)
	mux.HandleFunc("GET /player/find", s.handlePlayerFind)
	mux.HandleFunc("GET /player/{id}", s.handlePlayer)
	mux.HandleFunc("GET /api/data", s.handleData)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("POST /api/refresh", s.handleRefresh)

	root := http.NewServeMux()
	for _, l := range langs[1:] {
		root.Handle(langPrefix(l)+"/", http.StripPrefix(langPrefix(l), withLang(l, mux)))
	}
	root.Handle("/", withLang(langs[0], mux))
	return root
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	v := s.buildView(time.Now())
	s.mu.RUnlock()
	s.render(w, r, "index.html", v)
}

func (s *Server) handleTeam(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.mu.RLock()
	v, ok := s.buildTeamView(id, time.Now())
	s.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.render(w, r, "team.html", v)
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, v any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl[langOf(r)].ExecuteTemplate(w, name, v); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

func (s *Server) handleData(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, s.snap)
}

type status struct {
	Version   int       `json:"version"`
	CheckedAt time.Time `json:"checked_at"`
	Error     string    `json:"error,omitempty"`
	Fetching  bool      `json:"fetching"`
}

func (s *Server) status() status {
	fetching := !s.fetchMu.TryLock()
	if !fetching {
		s.fetchMu.Unlock()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return status{Version: s.snap.Version, CheckedAt: s.snap.CheckedAt, Error: s.snap.Error, Fetching: fetching}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.status())
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	tooSoon := time.Since(s.attempted) < minManualRefresh
	s.mu.RUnlock()
	if !tooSoon {
		s.refresh(r.Context())
	}
	writeJSON(w, s.status())
}

// --- template helpers --------------------------------------------------------

func (s *Server) isMine(team string) bool {
	return s.Team != "" && containsFold(team, s.Team)
}

// isMe matches full names ("DAGLI OSMAN BUGRA") and the abbreviated doubles
// form ("DAGLI OSMAN B").
func (s *Server) isMe(player string) bool {
	return s.Player != "" && (containsFold(player, s.Player) || containsFold(s.Player, player))
}

// teamURL links a team name to its page; "" if the team isn't in the standings.
// Handlers release s.mu before rendering, so this takes its own read lock.
func (s *Server) teamURL(team string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, st := range s.snap.Standings {
		if st.Team == team {
			return fmt.Sprintf("/team/%d", st.TeamID)
		}
	}
	return ""
}

func ordinal(n int) string {
	suffix := "th"
	switch {
	case n%100 >= 11 && n%100 <= 13:
	case n%10 == 1:
		suffix = "st"
	case n%10 == 2:
		suffix = "nd"
	case n%10 == 3:
		suffix = "rd"
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

// points formats ranking points the English way: 6219.81 -> "6,219.81".
func points(f float64) string {
	if f < 0 {
		return "−" + points(-f)
	}
	whole := int64(f)
	frac := int64((f-float64(whole))*100 + 0.5)
	if frac == 100 {
		whole, frac = whole+1, 0
	}
	str := fmt.Sprintf("%d", whole)
	for i := len(str) - 3; i > 0; i -= 3 {
		str = str[:i] + "," + str[i:]
	}
	if frac == 0 {
		return str
	}
	return fmt.Sprintf("%s.%02d", str, frac)
}

func pct(won, lost int) string {
	if won+lost == 0 {
		return "–"
	}
	return fmt.Sprintf("%.0f%%", 100*float64(won)/float64(won+lost))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

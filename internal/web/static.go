package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Build scrapes the portal once and writes the whole site as static files to
// dir, for hosting without a running server (e.g. Cloudflare Pages):
//
//	index.html, team/<id>.html, player/<id>.html      (Italian)
//	en/index.html, en/team/<id>.html, ...            (English)
//
// Pages are written as "<name>.html" so that /team/39067 resolves on hosts
// with pretty URLs. Only group players get a page; other names are plain text.
func (s *Server) Build(ctx context.Context, dir string) error {
	s.Static = true
	s.refresh(ctx)

	s.mu.RLock()
	err, matches := s.snap.Error, len(s.snap.Matches)
	s.mu.RUnlock()
	if err != "" {
		return fmt.Errorf("refresh: %s", err) // don't publish a site without data
	}
	if matches == 0 {
		return errors.New("refresh: no matches found")
	}

	s.loadGroupPlayerPages(ctx)

	s.mu.RLock()
	v := s.buildView(time.Now())
	var teams []teamView
	for _, st := range s.snap.Standings {
		if tv, ok := s.buildTeamView(st.TeamID, time.Now()); ok {
			teams = append(teams, tv)
		}
	}
	var players []playerView
	for id, p := range s.pages {
		players = append(players, s.buildPlayerView(id, p))
	}
	s.mu.RUnlock()
	sort.Slice(players, func(i, j int) bool { return players[i].ID < players[j].ID })

	for _, l := range langs {
		root := filepath.Join(dir, filepath.FromSlash(langPrefix(l)))
		if err := s.writePage(l, filepath.Join(root, "index.html"), "index.html", v); err != nil {
			return err
		}
		for _, tv := range teams {
			if err := s.writePage(l, filepath.Join(root, "team", fmt.Sprintf("%d.html", tv.Row.TeamID)), "team.html", tv); err != nil {
				return err
			}
		}
		for _, pv := range players {
			if err := s.writePage(l, filepath.Join(root, "player", fmt.Sprintf("%d.html", pv.ID)), "player.html", pv); err != nil {
				return err
			}
		}
	}
	log.Printf("wrote %s in %d languages: index, %d team page(s), %d player page(s)", dir, len(langs), len(teams), len(players))
	return nil
}

// loadGroupPlayerPages fetches the full record of every known group player.
// A player whose page fails to load is skipped, and their name stays unlinked.
func (s *Server) loadGroupPlayerPages(ctx context.Context) {
	s.mu.RLock()
	var ids []int
	for _, p := range s.players {
		if !p.Missing {
			ids = append(ids, p.Ref.ID)
		}
	}
	s.mu.RUnlock()
	sort.Ints(ids)

	for i, id := range ids {
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(requestDelay):
			}
		}
		if _, err := s.loadPlayerPage(ctx, id); err != nil {
			log.Printf("player %d: %v", id, err)
		}
	}
}

func (s *Server) writePage(lang, path, tmpl string, v any) error {
	var b bytes.Buffer
	if err := s.tmpl[lang].ExecuteTemplate(&b, tmpl, v); err != nil {
		return fmt.Errorf("render %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}

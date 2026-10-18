// fitet-watch checks a FITET group's calendar and notifies you when new match
// results ("conclusions") are published.
//
//	go run ./cmd/fitet-watch                 # check once
//	go run ./cmd/fitet-watch -every 30m      # keep checking
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/osmandagli/fitet-web-scrapper/internal/fitet"
	"github.com/osmandagli/fitet-web-scrapper/internal/notify"
	"github.com/osmandagli/fitet-web-scrapper/internal/state"
)

func main() {
	var (
		cam       = flag.Int("cam", 408, "group id (CAM); 408 = Lombardia C2 GIR C")
		anno      = flag.Int("anno", 41, "season id (ANNO); 41 = 2026/27")
		team      = flag.String("team", "BONACOSSA - 2", "highlight matches of teams containing this text")
		statePath = flag.String("state", defaultStatePath(), "where seen results are stored")
		every     = flag.Duration("every", 0, "repeat the check at this interval (e.g. 30m); 0 = check once")
		desktop   = flag.Bool("desktop", true, "show a desktop notification (Windows toast on WSL, notify-send on Linux)")
		ntfyTopic = flag.String("ntfy", "", "also push to this ntfy.sh topic (phone notifications)")
		test      = flag.Bool("test-notify", false, "send a test notification and exit")
	)
	flag.Parse()
	log.SetFlags(log.LstdFlags)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	notifier := buildNotifier(*desktop, *ntfyTopic)
	if *test {
		if err := notifier.Notify(ctx, "FITET test", "Notifications are working 🏓"); err != nil {
			log.Fatal(err)
		}
		log.Println("test notification sent")
		return
	}

	w := &watcher{
		client:    fitet.NewClient(),
		group:     fitet.Group{CAM: *cam, ANNO: *anno},
		team:      *team,
		statePath: *statePath,
		notifier:  notifier,
	}

	if err := w.check(ctx); err != nil {
		log.Printf("check failed: %v", err)
		if *every == 0 {
			os.Exit(1)
		}
	}
	if *every == 0 {
		return
	}
	if *every < 5*time.Minute {
		log.Fatal("-every must be at least 5m: be gentle with the FITET server")
	}

	t := time.NewTicker(*every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.check(ctx); err != nil {
				log.Printf("check failed: %v", err) // keep going, the site is flaky sometimes
			}
		}
	}
}

type watcher struct {
	client    *fitet.Client
	group     fitet.Group
	team      string
	statePath string
	notifier  notify.Notifier
}

func (w *watcher) check(ctx context.Context) error {
	_, matches, err := w.client.Calendar(ctx, w.group)
	if err != nil {
		return err
	}
	st, existed, err := state.Load(w.statePath)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	changes := st.Update(matches)
	if err := st.Save(w.statePath); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	if !existed {
		// First run: record what's already published without spamming you.
		log.Printf("first run: saved %d existing results as baseline (%s)", len(changes), w.statePath)
		for _, c := range changes {
			fmt.Println("  " + w.line(c))
		}
		return nil
	}
	if len(changes) == 0 {
		log.Println("no new results")
		return nil
	}

	lines := make([]string, len(changes))
	for i, c := range changes {
		lines[i] = w.line(c)
		fmt.Println("  " + lines[i])
	}
	title := fmt.Sprintf("FITET: %d new result(s)", len(changes))
	if len(changes) == 1 {
		title = "FITET: new result"
	}
	if err := w.notifier.Notify(ctx, title, strings.Join(lines, "\n")); err != nil {
		// State is already saved, so we won't re-notify; make the failure visible.
		return fmt.Errorf("notify: %w", err)
	}
	return nil
}

func (w *watcher) line(c state.Change) string {
	m := c.Match
	s := fmt.Sprintf("G%d %s %s %s", m.Round, m.Home, m.Score(), m.Away)
	if c.Kind == state.Corrected {
		s += fmt.Sprintf(" (corrected, was %s)", c.OldScore)
	}
	if w.team != "" && (strings.Contains(m.Home, w.team) || strings.Contains(m.Away, w.team)) {
		s = "⭐ " + s
	}
	return s
}

func buildNotifier(desktop bool, ntfyTopic string) notify.Notifier {
	var ns notify.Multi
	if desktop {
		if _, err := exec.LookPath("powershell.exe"); err == nil {
			ns = append(ns, notify.WindowsToast{})
		} else if _, err := exec.LookPath("notify-send"); err == nil {
			ns = append(ns, notify.DesktopNotify{})
		} else {
			log.Println("no desktop notifier found; results will only be printed")
		}
	}
	if ntfyTopic != "" {
		ns = append(ns, notify.Ntfy{Topic: ntfyTopic})
	}
	return ns
}

func defaultStatePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "fitet-state.json"
	}
	return filepath.Join(dir, "fitet-web-scrapper", "state.json")
}

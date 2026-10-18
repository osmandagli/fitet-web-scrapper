// fitet-build scrapes a FITET group once and writes the results site as
// static files, ready to upload to a static host such as Cloudflare Pages.
//
//	go run ./cmd/fitet-build -out public -cache .cache
//
// Keep the -cache directory between runs: score sheets and players already
// downloaded are not asked for again.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	_ "time/tzdata" // match times are Europe/Rome; don't depend on the OS tz database

	"github.com/osmandagli/fitet-web-scrapper/internal/fitet"
	"github.com/osmandagli/fitet-web-scrapper/internal/web"
)

func main() {
	var (
		cam    = flag.Int("cam", 408, "group id (CAM); 408 = Lombardia C2 GIR C")
		anno   = flag.Int("anno", 41, "season id (ANNO); 41 = 2026/27")
		team   = flag.String("team", "BONACOSSA - 2", "highlight teams containing this text")
		player = flag.String("player", "DAGLI OSMAN", "highlight this player in lineups")
		cache  = flag.String("cache", "", "cache directory for score sheets and players (default: ~/.config/fitet-web-scrapper/<cam>-<anno>)")
		out    = flag.String("out", "public", "directory to write the site to")
	)
	flag.Parse()

	// "Updated 18 Oct · 21:40" should be Italian time, also on a UTC build machine.
	if rome, err := time.LoadLocation("Europe/Rome"); err == nil {
		time.Local = rome
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *cache == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			*cache = filepath.Join(dir, "fitet-web-scrapper", fmt.Sprintf("%d-%d", *cam, *anno))
		}
	}

	srv := web.NewServer(fitet.NewClient(), web.Options{
		Group:    fitet.Group{CAM: *cam, ANNO: *anno},
		Team:     *team,
		Player:   *player,
		Every:    6 * time.Hour,
		CacheDir: *cache,
	})
	if err := srv.Build(ctx, *out); err != nil {
		log.Fatal(err)
	}
}

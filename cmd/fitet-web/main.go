// fitet-web serves a local results page for a FITET group and keeps it fresh.
//
//	go run ./cmd/fitet-web            # then open http://localhost:8080
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
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
		addr   = flag.String("addr", "127.0.0.1:8080", "listen address (use 0.0.0.0:8080 to open it from your phone on the same Wi-Fi)")
		cam    = flag.Int("cam", 408, "group id (CAM); 408 = Lombardia C2 GIR C")
		anno   = flag.Int("anno", 41, "season id (ANNO); 41 = 2026/27")
		team   = flag.String("team", "BONACOSSA - 2", "highlight teams containing this text")
		player = flag.String("player", "DAGLI OSMAN", "highlight this player in lineups")
		cache  = flag.String("cache", "", "cache directory for score sheets and players (default: ~/.config/fitet-web-scrapper/<cam>-<anno>)")
		every  = flag.Duration("every", 6*time.Hour, "how often to re-check the FITET portal")
	)
	flag.Parse()
	if *every < 5*time.Minute {
		log.Fatal("-every must be at least 5m: be gentle with the FITET server")
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
		Every:    *every,
		CacheDir: *cache,
	})
	go srv.Run(ctx)

	httpSrv := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	log.Printf("serving on http://%s (checking FITET every %s)", *addr, *every)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

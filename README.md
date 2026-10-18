# fitet-web-scrapper

Personal tracker for FITET table-tennis results (portale.fitet.org).

## fitet-web — local results website

```sh
go build -o bin/fitet-web ./cmd/fitet-web
./bin/fitet-web                      # open http://localhost:8080
```

Shows your team card (position, next match, form), the standings and every round's
results/fixtures. It re-checks FITET every 6 hours (`-every`, e.g. `-every 15m` on match day), and open pages reload by
themselves when something changes; results that appeared while it was running get a
**New** badge for 24h. "Refresh now" forces a check (at most once a minute).

Tap any team to open its page (`/team/<id>`): record, form, per-player stats
(singles/doubles W–L, positions played) and the lineup of each recent match — who
played A/B/C or X/Y/Z, the double, substitutions, and all 7 games with set scores.

Lineups follow the regional men's formula *Mini Swaythling con doppio*
(Regolamento Attività a Squadre 2026/27 – parte specifica, art. 1.6.1):
A–X, B–Y, C–Z, Doppio, B–X, A–Z, C–Y, all 7 games always played
(7-0/6-1/5-2 → 3-0 pts, 4-3 → 2-1). The portal doesn't say which team was ABC —
it isn't always the home team — so it's inferred from how games 5–7 re-pair the
players of games 1–3 (`fitet.Sheet.AssignLetters`). Score sheets are cached in
`~/.config/fitet-web-scrapper/<cam>-<anno>/sheets.json` and only re-downloaded if a score changes.

Tap any player (`/player/<ATLETA id>`) for their full FITET record: national ranking
(category, points, position, best ever), points gained this season, the ranking history
chart, and every singles match of the season — individual tournaments (with stage
reached), team championships and play-offs — with each opponent's points. Team pages
show each player's ranking next to their name.

Group players are matched to FITET ids through the ranking search and cached in
`<cache>/players.json` (rankings refreshed daily); other names go through
`/player/find?name=…`, which redirects on a single match or lists homonyms.

- Pages are in Italian; the English version is under `/en/` (IT/EN switch at the top). Texts live in `internal/web/lang.go`
- `-addr 0.0.0.0:8080` to open it from your phone on the same Wi-Fi
- `-player "DAGLI OSMAN"` highlights you in lineups
- `/api/data` returns everything as JSON (handy for the future dashboard)

Start it automatically when you log into Windows:

```powershell
schtasks /Create /TN "FITET web" /SC ONLOGON /TR "wsl.exe -e /home/osman/fitet-web-scrapper/bin/fitet-web"
```

## fitet-build — static site (Cloudflare Pages)

```sh
go run ./cmd/fitet-build -out public -cache .cache
```

Scrapes the group once and writes the same pages as plain files: `index.html`,
`team/<id>.html` and `player/<id>.html` for every group player found on FITET.
Italian pages are at the root and English ones under `en/`. Static hosts with pretty URLs (Cloudflare Pages) serve them at `/team/<id>`, like
the local server. Differences from `fitet-web`: no "Refresh now" button and no live
reload (the header says when it was built, in Italian time), and players outside the
group aren't linked because there is no `/player/find` search. Keep `-cache` between runs so
only new score sheets are downloaded (cold build ≈ 75 s, warm ≈ 20 s). The build
exits non-zero, without writing anything, if FITET can't be reached.

Check it locally with `npx wrangler pages dev public`.

## fitet-watch

Checks a group's calendar and notifies you when new match results are published
(or a published score gets corrected).

```sh
go build -o bin/fitet-watch ./cmd/fitet-watch

./bin/fitet-watch -test-notify        # check that notifications show up
./bin/fitet-watch                     # check once (first run just saves a baseline)
./bin/fitet-watch -every 30m          # keep checking while the terminal is open
./bin/fitet-watch -ntfy my-secret-topic   # also push to phone via the ntfy app
```

Defaults: Lombardia **C2 GIR C** 2026/27 (`-cam 408 -anno 41`), matches of
`BONACOSSA - 2` marked with ⭐. State lives in `~/.config/fitet-web-scrapper/state.json`.

Other groups: open `https://portale.fitet.org/risultati/regioni/menu_reg.php?REG=4`
(Lombardia) and take the `CAM=` number from the link.

### Run automatically (Windows Task Scheduler → WSL)

Runs every hour even when no terminal is open:

```powershell
schtasks /Create /TN "FITET watch" /SC HOURLY /TR "wsl.exe -e /home/osman/fitet-web-scrapper/bin/fitet-watch"
```

## Portal notes

| Page | Path under `/risultati/` |
|---|---|
| Lombardia groups | `regioni/menu_reg.php?REG=4` |
| Calendar + results | `campionati/Calendario.asp?CAM=…&ANNO=…` |
| Standings | `campionati/classifica_squadre.php?CAM=…` |
| Player stats | `campionati/statistiche_atleti.php?CAM=…` |
| Match sheet (frameset) | `campionati/giornata_home.php?INCONTRO=…&CAM=…&FORMULA=19` |
| Match sheet (games) | `campionati/giornata.php?INCONTRO=…&CAM=…&FORMULA=19` |
| Player history (current season) | `incontri_atleta_acc.php?ATLETA=…` |
| Player search (JSON, needs `X-Requested-With`) | `new_rank/ajax.php?term=…` |
| Ranking detail (same ATLETA id) | `new_rank/dettaglioatleta_unica.php?ATLETA=…&ID_CLASS=…&ZU=1&AVVERSARIO=0` |
| Latest ranking ID_CLASS | first `testaclassifica_auto.php?ID_CLASS=` link in `/menu.php` |

Pages are mostly Windows-1252, some UTF-8 with BOM; `fitet.Client.Get` normalises both.

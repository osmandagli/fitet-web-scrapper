package web

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/osmandagli/fitet-web-scrapper/internal/fitet"
)

// Pages are in Italian at / and in English under /en/. Templates write their
// texts in English through t / tf; Italian comes from the dictionary below,
// so a missing entry falls back to English instead of breaking the page.

const (
	langIT = "it"
	langEN = "en"
)

// langs lists the site languages; the first one is served at the root.
var langs = []string{langIT, langEN}

func langPrefix(lang string) string {
	if lang == langIT {
		return ""
	}
	return "/" + lang
}

type langKey struct{}

func withLang(lang string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), langKey{}, lang)))
	})
}

func langOf(r *http.Request) string {
	if l, ok := r.Context().Value(langKey{}).(string); ok {
		return l
	}
	return langIT
}

// langFuncs are the template functions whose output depends on the language.
func (s *Server) langFuncs(lang string) template.FuncMap {
	tr := func(en string) string {
		if lang == langIT {
			if it, ok := italian[en]; ok {
				return it
			}
		}
		return en
	}
	prefix := langPrefix(lang)
	return template.FuncMap{
		"lang": func() string { return lang },
		"t":    tr,
		"tf":   func(format string, args ...any) string { return fmt.Sprintf(tr(format), args...) },
		"date": func(t time.Time) string { return formatDate(lang, t, true) },
		"short": func(t time.Time) string {
			return formatDate(lang, t, false)
		},
		"ordinal": func(n int) string {
			if lang == langIT {
				return fmt.Sprintf("%dº", n)
			}
			return ordinal(n)
		},
		"points": func(f float64) string { return localPoints(lang, f) },
		"stage":  func(code string) string { return stageName(lang, code) },
		"every":  func(d time.Duration) string { return formatEvery(lang, d) },
		// home is the group page in this language; alt is path in the other one.
		"home": func() string { return prefix + "/" },
		"alt": func(path string) string {
			if lang == langIT {
				return "/en" + path
			}
			return path
		},
		"day":   func(t time.Time) string { return formatDay(lang, t) },
		"round": func(code string) string { return roundName(lang, code, false) },
		"out":   func(code string) string { return roundName(lang, code, true) },
		"fpos":  func(code string) string { return fitetPos(lang, code) },
		"tournamentURL": func(date, title string) string {
			if u := s.tournamentURL(date, title); u != "" {
				return prefix + u
			}
			return ""
		},
		"teamURL": func(team string) string {
			if u := s.teamURL(team); u != "" {
				return prefix + u
			}
			return ""
		},
		"playerURL": func(name string) string {
			if u := s.playerURL(name); u != "" {
				return prefix + u
			}
			return ""
		},
	}
}

var (
	itDays   = []string{"dom", "lun", "mar", "mer", "gio", "ven", "sab"}
	itMonths = []string{"gen", "feb", "mar", "apr", "mag", "giu", "lug", "ago", "set", "ott", "nov", "dic"}
)

// formatDate gives "Mon 2 Jan · 15:04" / "lun 2 gen · 15:04", or just the day.
func formatDate(lang string, t time.Time, full bool) string {
	if lang != langIT {
		if full {
			return t.Format("Mon 2 Jan · 15:04")
		}
		return t.Format("2 Jan")
	}
	day := fmt.Sprintf("%d %s", t.Day(), itMonths[t.Month()-1])
	if full {
		return fmt.Sprintf("%s %s · %s", itDays[t.Weekday()], day, t.Format("15:04"))
	}
	return day
}

// formatDay gives "Sat 29 Aug 2026" / "sab 29 ago 2026".
func formatDay(lang string, t time.Time) string {
	if lang != langIT {
		return t.Format("Mon 2 Jan 2006")
	}
	return fmt.Sprintf("%s %d %s %d", itDays[t.Weekday()], t.Day(), itMonths[t.Month()-1], t.Year())
}

// localPoints writes ranking points in the page's convention: 6,219.81 / 6.219,81.
func localPoints(lang string, f float64) string {
	p := points(f)
	if lang != langIT {
		return p
	}
	return strings.NewReplacer(",", ".", ".", ",").Replace(p)
}

func formatEvery(lang string, d time.Duration) string {
	var n int
	var unit string
	switch {
	case d%time.Hour == 0:
		n, unit = int(d/time.Hour), "h"
	case d%time.Minute == 0:
		n, unit = int(d/time.Minute), "min"
	default:
		return d.String()
	}
	if lang == langIT && unit == "h" {
		if n == 1 {
			return "ora"
		}
		return fmt.Sprintf("%d ore", n)
	}
	return fmt.Sprintf("%d %s", n, unit)
}

// stageName translates the portal's tournament stage codes ("QF", "32T").
func stageName(lang, code string) string {
	if lang != langIT {
		return fitet.StageName(code)
	}
	switch strings.ToUpper(code) {
	case "GQ":
		return "Girone"
	case "F":
		return "Finale"
	case "SF":
		return "Semifinale"
	case "QF":
		return "Quarti di finale"
	case "OF":
		return "Ottavi di finale"
	}
	if n, ok := roundNumber(code); ok {
		return "Turno " + n
	}
	return code
}

// roundName names a knockout round (stageCode) as a heading ("Quarter-finals")
// or as where a player went out ("Quarter-final").
func roundName(lang, code string, out bool) string {
	type names struct{ en, enOut, it, itOut string }
	var n names
	switch code {
	case "F":
		n = names{"Final", "Finalist", "Finale", "Finalista"}
	case "SF":
		n = names{"Semi-finals", "Semi-final", "Semifinali", "Semifinale"}
	case "QF":
		n = names{"Quarter-finals", "Quarter-final", "Quarti di finale", "Quarti"}
	case "OF":
		n = names{"Last 16", "Last 16", "Ottavi di finale", "Ottavi"}
	case "32":
		n = names{"Last 32", "Last 32", "Sedicesimi di finale", "Sedicesimi"}
	case "64":
		n = names{"Last 64", "Last 64", "Trentaduesimi di finale", "Trentaduesimi"}
	default:
		n = names{"Last " + code, "Last " + code, "Turno a " + code, "Turno a " + code}
	}
	switch {
	case lang == langIT && out:
		return n.itOut
	case lang == langIT:
		return n.it
	case out:
		return n.enOut
	}
	return n.en
}

// fitetPos names the stage reached as the ranking writes it: GQ (groups),
// QF, OF…, and "16F" for the sedicesimi (the last 32).
func fitetPos(lang, code string) string {
	if code == "GQ" {
		if lang == langIT {
			return "Girone"
		}
		return "Groups"
	}
	if n, err := strconv.Atoi(strings.TrimSuffix(code, "F")); err == nil && strings.HasSuffix(code, "F") {
		return roundName(lang, strconv.Itoa(2*n), true)
	}
	return roundName(lang, code, true)
}

// roundNumber reads codes like "3T" (round 3 of the draw).
func roundNumber(code string) (string, bool) {
	n := strings.TrimSuffix(strings.ToUpper(code), "T")
	if n == code || n == "" {
		return "", false
	}
	if _, err := strconv.Atoi(n); err != nil {
		return "", false
	}
	return n, true
}

// italian translates the English texts used in the templates.
var italian = map[string]string{
	// shared
	"Results":                       "Risultati",
	"Loading…":                      "Caricamento…",
	"Fetching results…":             "Caricamento risultati…",
	"Refresh now":                   "Aggiorna ora",
	"Checked":                       "Controllato",
	"Updated":                       "Aggiornato",
	"just now":                      "ora",
	"%d min ago":                    "%d min fa",
	"%d h ago":                      "%d h fa",
	"%d days ago":                   "%d giorni fa",
	"Local server not running":      "Server locale non attivo",
	"Data from":                     "Dati da",
	"checked every %s":              "controllato ogni %s",
	"updated %s":                    "aggiornato %s",
	"home":                          "casa",
	"away":                          "trasferta",
	"pts":                           "punti",
	"Form":                          "Forma",
	"Matches played":                "Incontri disputati",
	"V = win · P = loss · N = draw": "V = vittoria · P = sconfitta · N = pareggio",
	"R%d":                           "G%d",
	"Couldn't reach the FITET portal: %s. Showing the last data I had.": "Impossibile raggiungere il portale FITET: %s. Mostro gli ultimi dati disponibili.",
	"New":  "Nuovo",
	"Done": "Conclusa",

	// group page
	"%dW %dD %dL":                       "%dV %dN %dP",
	"games %d–%d · sets %d–%d":          "partite %d–%d · set %d–%d",
	"singles %d–%d":                     "singolari %d–%d",
	"doubles %d–%d":                     "doppi %d–%d",
	"Next match":                        "Prossimo incontro",
	"round %d":                          "giornata %d",
	"%d left this season":               "%d ancora da giocare",
	"Standings":                         "Classifica",
	"Standings, results and lineups":    "Classifica, risultati e formazioni",
	"Search on FITET ↗":                 "Cerca su FITET ↗",
	"No matching player in this group.": "Nessun giocatore corrispondente in questo girone.",
	"tap a team to see its lineups":     "tocca una squadra per vedere le formazioni",
	"Team":                              "Squadra",
	"Pts":                               "Pt",
	"P":                                 "G",
	"W":                                 "V",
	"D":                                 "N",
	"L":                                 "P",
	"Games":                             "Partite",
	"Sets":                              "Set",
	"Points":                            "Punti",
	"Results & fixtures":                "Risultati e calendario",
	"Round %d":                          "Giornata %d",
	"Fetching results from the FITET portal…": "Caricamento dei risultati dal portale FITET…",
	"this page will update by itself.":        "la pagina si aggiornerà da sola.",

	// team page
	"%s place":                             "%s posto",
	"Record":                               "Bilancio",
	"Players":                              "Giocatori",
	"Player":                               "Giocatore",
	"ranking · points · national position": "categoria · punti · posizione nazionale",
	"Matches appeared in":                  "Incontri giocati",
	"M":                                    "I",
	"Singles":                              "Singolari",
	"Win %":                                "% vittorie",
	"Doubles":                              "Doppi",
	"Positions":                            "Posizioni",
	"(W)":                                  "(F)",
	"Recent matches":                       "Ultimi incontri",
	"Lineup":                               "Formazione",
	"Opponent":                             "Avversari",
	"All %d games":                         "Tutte le %d partite",
	"Score sheet not downloaded yet, it will appear after the next check.":  "Referto non ancora scaricato, apparirà al prossimo controllo.",
	"Score sheet not downloaded yet, it will appear after the next update.": "Referto non ancora scaricato, apparirà al prossimo aggiornamento.",
	"No matches played yet.": "Nessun incontro ancora disputato.",
	"Coming up":              "Prossimi incontri",
	"Lineups follow the regional formula (Regolamento Attività a Squadre 2026/27, art. 1.6.1): A–X, B–Y, C–Z, Doppio, B–X, A–Z, C–Y.": "Le formazioni seguono la formula regionale (Regolamento Attività a Squadre 2026/27, art. 1.6.1): A–X, B–Y, C–Z, Doppio, B–X, A–Z, C–Y.",

	// player page
	"season %s":        "stagione %s",
	"Fetched":          "Scaricato",
	"On FITET ↗":       "Su FITET ↗",
	"Ranking":          "Classifica",
	"Ranking · %s":     "Classifica · %s",
	"#%d in the %s":    "n. %d nella %s",
	"men's ranking":    "classifica maschile",
	"women's ranking":  "classifica femminile",
	"national ranking": "classifica nazionale",
	"Foreign":          "Straniero",
	"foreign players have no national position": "gli stranieri non hanno posizione nazionale",
	"This season":                          "Questa stagione",
	"from %s · %d–%d in ranked matches":    "da %s · %d–%d in incontri validi per la classifica",
	"Best ever":                            "Miglior risultato",
	"highest national position":            "miglior posizione nazionale",
	"Not available":                        "Non disponibile",
	"Not in the national ranking":          "Non presente nella classifica nazionale",
	"Matches %s":                           "Incontri %s",
	"sets %d–%d":                           "set %d–%d",
	"Ranking points":                       "Punti in classifica",
	"Every ranking publication":            "Ogni pubblicazione della classifica",
	" since %s":                            " dal %s",
	"Hover or use ← → for details.":        "Passa sopra il grafico o usa ← → per i dettagli.",
	"Ranking points over time, now %s":     "Punti in classifica nel tempo, ora %s",
	"Show as table":                        "Mostra come tabella",
	"Ranking of":                           "Classifica del",
	"Position":                             "Posizione",
	"By competition":                       "Per competizione",
	"Competition":                          "Competizione",
	"W–L":                                  "V–P",
	"Total":                                "Totale",
	"Tournaments":                          "Tornei",
	"Team championships":                   "Campionati a squadre",
	"Play-off / out":                       "Play-off / play-out",
	"Category played":                      "Categoria giocata",
	"🏆 Winner":                             "🏆 Vincitore",
	"reached %s":                           "fase raggiunta: %s",
	"not ranked":                           "non valido",
	"No matches recorded this season yet.": "Nessun incontro registrato in questa stagione.",
	"From the FITET portal: match record (“scheda incontri”) and national ranking": "Dal portale FITET: scheda incontri e classifica nazionale",
	" of %s": " del %s",
	"Matches marked “not ranked” don't count for the ranking.": "Gli incontri segnati “non valido” non contano per la classifica.",

	// player search
	"Find %s":       "Cerca %s",
	"← Back":        "← Indietro",
	"Player search": "Ricerca giocatore",
	"%d athlete(s) match this name on FITET. Pick the right one:": "%d atleti con questo nome su FITET. Scegli quello giusto:",
	"Name": "Nome",
	"Born": "Nato il",
	"Card": "Tessera",
	"No athlete called “%s” was found in the FITET ranking.": "Nessun atleta di nome “%s” trovato nella classifica FITET.",
	"English": "English",

	// tournaments
	"Open":                 "Open",
	"Upcoming tournaments": "Prossimi tornei",
	"not counted":          "non conteggiato",
	"start of season":      "inizio stagione",
	"Points appear with the next ranking publication.": "I punti compariranno con la prossima pubblicazione della classifica.",
	"tournament placings":                              "piazzamenti nei tornei",
	"next ranking":                                     "prossima classifica",
	"group-stage wins":                                 "vittorie nei gironi",
	"Reached":                                          "Fase raggiunta",
	"Matches earn no points by themselves: a tournament gives points for the stage reached in each event (only the best tournaments count) plus a fixed amount per group-stage win.": "I singoli incontri non danno punti: un torneo assegna punti per la fase raggiunta in ogni gara (contano solo i tornei migliori) più una quota fissa per ogni vittoria nei gironi.",
	"1 group win × %s":   "1 vittoria nel girone × %s",
	"%d group wins × %s": "%d vittorie nei gironi × %s",
	"Group wins":         "Vittorie nei gironi",
	"ranking of %s":      "classifica del %s",
	"team championship":  "campionato a squadre",
	"Ranking points the event gave, from the latest ranking publication": "Punti in classifica ottenuti nella gara, dall'ultima pubblicazione della classifica",
	"Tournament":           "Torneo",
	"Placing":              "Piazzamento",
	"ranking points":       "punti in classifica",
	"protection":           "protezione",
	"placing %s":           "piazzamento %s",
	"Home page":            "Pagina iniziale",
	"+ %d more events":     "+ altre %d gare",
	"Championship":         "Campionato",
	"Regional tournaments": "Tornei regionali",
	"Results and entries of the season's tournaments in Lombardy. ★ = your team, bold = players of your group.": "Risultati e iscritti dei tornei della stagione in Lombardia. ★ = la tua squadra, in grassetto = giocatori del tuo girone.",
	"%d events · %d entries":                 "%d gare · %d iscrizioni",
	"%d events":                              "%d gare",
	"Entered from your group:":               "Iscritti dal tuo girone:",
	"Entered from your group":                "Iscritti dal tuo girone",
	"Entry lists":                            "Liste iscritti",
	"Your group:":                            "Il tuo girone:",
	"Draws and groups":                       "Tabelloni e gironi",
	"No tournament results yet this season.": "Ancora nessun risultato dei tornei in questa stagione.",
	"Next tournament":                        "Prossimo torneo",
	"%d from your group entered":             "%d iscritti dal tuo girone",
	"Your group in this tournament":          "Il tuo girone in questo torneo",
	"★ = your team":                          "★ = la tua squadra",
	"Event":                                  "Gara",
	"Events":                                 "Gare",
	"Result":                                 "Risultato",
	"Find a player…":                         "Cerca un giocatore…",
	"%d matches":                             "%d risultati",
	"Winner":                                 "Vincitore",
	"Finalist":                               "Finalista",
	"Semi-finals":                            "Semifinali",
	"Full draw":                              "Tabellone completo",
	"%d rounds":                              "%d turni",
	"bye":                                    "bye",
	"Groups":                                 "Gironi",
	"%d groups":                              "%d gironi",
	"Group %d":                               "Girone %d",
	"Group %d · %s":                          "Girone %d · %s",
	"Entries":                                "Iscritti",
	"%d players, by ranking points":          "%d giocatori, per punti in classifica",
	"Club":                                   "Società",
	"No draw published for this event.":      "Nessun tabellone pubblicato per questa gara.",
	"tournament results and entry lists":     "risultati dei tornei e liste iscritti",
	"Entered":                                "Iscritto",
	"still in":                               "in gara",
	"Category":                               "Categoria",
	"Youth":                                  "Giovanili",
	"Ordinary":                               "Ordinari",
	"Veterans":                               "Master",
	"Promotional":                            "Promozionali",
	"Para":                                   "Paralimpici",
	"Regional championships":                 "Campionati regionali",
	"Provincial":                             "Provinciali",
	"Women's day":                            "Giornata rosa",
}

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
	"%dW %dD %dL":                   "%dV %dN %dP",
	"games %d–%d · sets %d–%d":      "partite %d–%d · set %d–%d",
	"Next match":                    "Prossimo incontro",
	"round %d":                      "giornata %d",
	"%d left this season":           "%d ancora da giocare",
	"Standings":                     "Classifica",
	"tap a team to see its lineups": "tocca una squadra per vedere le formazioni",
	"Team":                          "Squadra",
	"Pts":                           "Pt",
	"P":                             "G",
	"W":                             "V",
	"D":                             "N",
	"L":                             "P",
	"Games":                         "Partite",
	"Sets":                          "Set",
	"Points":                        "Punti",
	"Results & fixtures":            "Risultati e calendario",
	"Round %d":                      "Giornata %d",
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
}

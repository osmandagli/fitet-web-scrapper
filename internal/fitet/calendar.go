package fitet

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Match is a single team fixture (incontro) in a group.
type Match struct {
	ID        int    `json:"id"`      // INCONTRO id, stable across scrapes
	Formula   int    `json:"formula"` // FORMULA id from the result link, needed to open the score sheet
	Round     int    `json:"round"`
	Date      string `json:"date"` // dd/mm/yyyy as shown on the portal
	Time      string `json:"time"`
	Home      string `json:"home"`
	Away      string `json:"away"`
	Played    bool   `json:"played"`
	HomeScore int    `json:"home_score"`
	AwayScore int    `json:"away_score"`
}

func (m Match) Score() string {
	if !m.Played {
		return "-"
	}
	return fmt.Sprintf("%d - %d", m.HomeScore, m.AwayScore)
}

func (m Match) String() string {
	return fmt.Sprintf("G%d %s  %s %s %s", m.Round, m.Date, m.Home, m.Score(), m.Away)
}

var rome = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		return time.UTC
	}
	return loc
}()

// When is the scheduled start in Italian time; zero if the portal's date is unparseable.
func (m Match) When() time.Time {
	t, err := time.ParseInLocation("02/01/2006 15.04", m.Date+" "+m.Time, rome)
	if err != nil {
		t, _ = time.ParseInLocation("02/01/2006", m.Date, rome)
	}
	return t
}

// GroupName extracts e.g. "Serie C2 GIR C" from the calendar page heading.
func GroupName(body []byte) string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return ""
	}
	var name string
	doc.Find("p, td, font, b").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if m := groupNameRe.FindStringSubmatch(clean(s.Text())); m != nil {
			name = m[1]
			return false
		}
		return true
	})
	return name
}

var groupNameRe = regexp.MustCompile(`^Calendario Incontri (.+)$`)

// Group identifies a championship group. For Lombardia C2 GIR C in 2026/27
// that is CAM=408, ANNO=41 (see regioni/menu_reg.php?REG=4 for the others).
type Group struct {
	CAM  int
	ANNO int
}

// Calendar fetches the group's fixtures and results, plus the group's display name.
func (c *Client) Calendar(ctx context.Context, g Group) (name string, matches []Match, err error) {
	body, err := c.Get(ctx, fmt.Sprintf("campionati/Calendario.asp?CAM=%d&ANNO=%d", g.CAM, g.ANNO))
	if err != nil {
		return "", nil, err
	}
	matches, err = ParseCalendar(body)
	return GroupName(body), matches, err
}

var (
	roundRe = regexp.MustCompile(`^(\d+)\^\s*Giornata`)
	scoreRe = regexp.MustCompile(`^(\d+)\s*-\s*(\d+)$`)
)

// ParseCalendar parses Calendario.asp. Every round is its own <table> headed by
// "N^ Giornata"; each fixture row carries both legs:
//
//	date | time | score | HOME | AWAY | score | time | date
//	\______ first leg ______/        \__ return leg (AWAY at HOME) __/
//
// so return-leg matches are emitted with round N + number of rounds.
func ParseCalendar(body []byte) ([]Match, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	var first, second []Match
	var parseErr error
	doc.Find("table").Each(func(_ int, t *goquery.Selection) {
		if parseErr != nil {
			return
		}
		m := roundRe.FindStringSubmatch(clean(t.Find("p.intestazione").First().Text()))
		if m == nil {
			return
		}
		round, _ := strconv.Atoi(m[1])

		t.Find("tr").Each(func(_ int, tr *goquery.Selection) {
			if parseErr != nil {
				return
			}
			td := tr.Find("td")
			if td.Length() != 8 || td.Eq(2).Find("a").Length() == 0 {
				return // spacer / header row
			}
			cell := func(i int) string { return clean(td.Eq(i).Text()) }
			home, away := cell(3), cell(4)

			a, err := buildMatch(td.Eq(2), round, cell(0), cell(1), home, away)
			if err != nil {
				parseErr = err
				return
			}
			b, err := buildMatch(td.Eq(5), round, cell(7), cell(6), away, home)
			if err != nil {
				parseErr = err
				return
			}
			first = append(first, a)
			second = append(second, b)
		})
	})
	if parseErr != nil {
		return nil, parseErr
	}
	if len(first) == 0 {
		return nil, fmt.Errorf("no matches found: calendar page layout may have changed")
	}

	rounds := 0
	for _, m := range first {
		rounds = max(rounds, m.Round)
	}
	for i := range second {
		second[i].Round += rounds
	}
	return append(first, second...), nil
}

func buildMatch(scoreCell *goquery.Selection, round int, date, tm, home, away string) (Match, error) {
	href, _ := scoreCell.Find("a").Attr("href")
	u, err := url.Parse(href)
	if err != nil {
		return Match{}, fmt.Errorf("bad match link %q: %w", href, err)
	}
	id, err := strconv.Atoi(u.Query().Get("INCONTRO"))
	if err != nil {
		return Match{}, fmt.Errorf("match link %q has no INCONTRO id", href)
	}

	formula, _ := strconv.Atoi(u.Query().Get("FORMULA"))
	m := Match{ID: id, Formula: formula, Round: round, Date: date, Time: tm, Home: home, Away: away}
	if s := scoreRe.FindStringSubmatch(clean(scoreCell.Text())); s != nil {
		m.Played = true
		m.HomeScore, _ = strconv.Atoi(s[1])
		m.AwayScore, _ = strconv.Atoi(s[2])
	}
	return m, nil
}

func clean(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, " ", " ")), " ")
}

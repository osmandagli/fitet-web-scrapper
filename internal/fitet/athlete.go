package fitet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// AthleteRef is a search hit. ID is the portal's ATLETA id, used both by the
// match history (incontri_atleta_acc.php) and the ranking (new_rank/*).
type AthleteRef struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Born string `json:"born"` // dd/mm/yyyy
	Card string `json:"card"` // tessera
}

var refLabelRe = regexp.MustCompile(`\((\d{2}/\d{2}/\d{4})\)\s*\[(\d+)\]`)

// FindAthletes searches the ranking's autocomplete endpoint by (part of) name.
func (c *Client) FindAthletes(ctx context.Context, term string) ([]AthleteRef, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.BaseURL+"/new_rank/ajax.php?term="+url.QueryEscape(term), nil)
	if err != nil {
		return nil, err
	}
	// The endpoint answers with an empty body unless it looks like the page's own AJAX call.
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", c.BaseURL+"/new_rank/testaclassifica_auto.php")
	body, err := c.do(req)
	if err != nil {
		return nil, err
	}
	return parseAthleteSearch(body)
}

func parseAthleteSearch(body []byte) ([]AthleteRef, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, nil
	}
	var hits []struct {
		ID    string `json:"id"`
		Value string `json:"value"`
		Label string `json:"label"`
	}
	if err := json.Unmarshal(body, &hits); err != nil {
		return nil, fmt.Errorf("athlete search: %w", err)
	}
	var out []AthleteRef
	for _, h := range hits {
		id, err := strconv.Atoi(h.ID)
		if err != nil {
			continue // e.g. "#" placeholder rows
		}
		r := AthleteRef{ID: id, Name: clean(h.Value)}
		if m := refLabelRe.FindStringSubmatch(h.Label); m != nil {
			r.Born, r.Card = m[1], m[2]
		}
		out = append(out, r)
	}
	return out, nil
}

// AthleteSeason is a player's "scheda incontri": every singles match of the
// current season, across individual tournaments and team championships.
type AthleteSeason struct {
	ID          int            `json:"id"`
	Name        string         `json:"name"`
	Season      string         `json:"season"` // "2026/27"
	Tournaments []Tournament   `json:"tournaments"`
	Team        []AthleteMatch `json:"team"`    // campionati a squadre
	Playoff     []AthleteMatch `json:"playoff"` // play off / out
}

type Tournament struct {
	Title   string         `json:"title"` // "Torneo Open ... del 29/08/2026 a Saronno (VA)"
	Date    string         `json:"date"`  // dd/mm/yyyy, parsed from the title when present
	Matches []AthleteMatch `json:"matches"`
}

type AthleteMatch struct {
	Event    string `json:"event"`           // "Singolo M/F Open3 451-2500M | 26-200F" or "C2 GIR C (1^ gior.)"
	Stage    string `json:"stage,omitempty"` // tournaments: GQ, 3T, OF, QF, SF, F...
	Opponent string `json:"opponent"`
	For      int    `json:"for"` // games (sets) won by the athlete
	Against  int    `json:"against"`
	Won      bool   `json:"won"`
	Ranked   bool   `json:"ranked"` // "valido per la classifica"
}

func (c *Client) AthleteSeason(ctx context.Context, id int) (*AthleteSeason, error) {
	body, err := c.Get(ctx, fmt.Sprintf("incontri_atleta_acc.php?ATLETA=%d", id))
	if err != nil {
		return nil, err
	}
	s, err := ParseAthleteSeason(body)
	if err != nil {
		return nil, fmt.Errorf("athlete %d: %w", id, err)
	}
	s.ID = id
	return s, nil
}

var (
	seasonRe     = regexp.MustCompile(`Stagione agonistica (\d{4}/\d{2})`)
	stageRe      = regexp.MustCompile(`^(.*?)-\(([^)]+)\)$`)
	titleDateRe  = regexp.MustCompile(`\bdel (\d{2}/\d{2}/\d{4})`)
	matchScoreRe = regexp.MustCompile(`(\d+)\s*-\s*(\d+)`)
)

// ParseAthleteSeason parses incontri_atleta_acc.php. Three accordion panels
// (#collapseOne/Two/Three: tournaments, team championships, play-offs) hold
// rows of: [ranked icon] event | player 1 | player 2 | score 1 | "- score 2" | [result icon].
// In tournaments a row with a single bold cell starts a new tournament.
func ParseAthleteSeason(body []byte) (*AthleteSeason, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	s := &AthleteSeason{Name: clean(doc.Find("p b").First().Text())}
	if m := seasonRe.FindStringSubmatch(clean(doc.Text())); m != nil {
		s.Season = m[1]
	}
	if s.Name == "" {
		return nil, fmt.Errorf("no athlete name: page layout may have changed")
	}

	panel := func(id string, each func(tr *goquery.Selection, td *goquery.Selection)) {
		doc.Find("#" + id + " tr").Each(func(_ int, tr *goquery.Selection) {
			each(tr, tr.ChildrenFiltered("td"))
		})
	}
	row := func(td *goquery.Selection) (AthleteMatch, bool) {
		if td.Length() < 6 {
			return AthleteMatch{}, false
		}
		p1, p2 := clean(td.Eq(2).Text()), clean(td.Eq(3).Text())
		sc := matchScoreRe.FindStringSubmatch(clean(td.Eq(4).Text()) + " " + clean(td.Eq(5).Text()))
		if sc == nil {
			return AthleteMatch{}, false
		}
		a, _ := strconv.Atoi(sc[1])
		b, _ := strconv.Atoi(sc[2])
		m := AthleteMatch{Event: clean(td.Eq(1).Text()), Opponent: p2, For: a, Against: b}
		if !strings.EqualFold(p1, s.Name) && strings.EqualFold(p2, s.Name) {
			m.Opponent, m.For, m.Against = p1, b, a
		}
		m.Won = m.For > m.Against
		if src, ok := td.Eq(0).Find("img").Attr("src"); ok {
			m.Ranked = strings.Contains(src, "save")
		}
		if st := stageRe.FindStringSubmatch(m.Event); st != nil {
			m.Event, m.Stage = strings.TrimSpace(st[1]), st[2]
		}
		return m, true
	}

	panel("collapseOne", func(tr, td *goquery.Selection) {
		if td.Length() == 1 && tr.Find("b").Length() > 0 {
			t := Tournament{Title: clean(td.Text())}
			if m := titleDateRe.FindStringSubmatch(t.Title); m != nil {
				t.Date = m[1]
			}
			s.Tournaments = append(s.Tournaments, t)
			return
		}
		if m, ok := row(td); ok {
			if len(s.Tournaments) == 0 {
				s.Tournaments = append(s.Tournaments, Tournament{})
			}
			last := &s.Tournaments[len(s.Tournaments)-1]
			last.Matches = append(last.Matches, m)
		}
	})
	panel("collapseTwo", func(_, td *goquery.Selection) {
		if m, ok := row(td); ok {
			s.Team = append(s.Team, m)
		}
	})
	panel("collapseThree", func(_, td *goquery.Selection) {
		if m, ok := row(td); ok {
			s.Playoff = append(s.Playoff, m)
		}
	})
	return s, nil
}

// StageName translates the portal's tournament stage codes.
func StageName(code string) string {
	switch strings.ToUpper(code) {
	case "GQ":
		return "Group"
	case "F":
		return "Final"
	case "SF":
		return "Semi-final"
	case "QF":
		return "Quarter-final"
	case "OF":
		return "Last 16"
	}
	if n := strings.TrimSuffix(strings.ToUpper(code), "T"); n != code && n != "" {
		if _, err := strconv.Atoi(n); err == nil {
			return "Round " + n
		}
	}
	return code
}

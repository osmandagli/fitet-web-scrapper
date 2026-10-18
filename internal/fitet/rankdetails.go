package fitet

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// RankingDetails is how an athlete's ranking points were made up in one
// ranking publication ("Dettaglio Punti"). Matches earn no points by
// themselves: tournaments give points for the placing reached in each event,
// plus a fixed amount for each group-stage win.
type RankingDetails struct {
	Start          float64 `json:"start"`           // Punti iniziali: carried over from last season
	Championship   float64 `json:"championship"`    // campionato
	Tournaments    float64 `json:"tournaments"`     // tornei: placing points that count
	TournamentWins float64 `json:"tournament_wins"` // vittorie tornei: group-stage wins
	Protection     float64 `json:"protection"`      // protezione
	Final          float64 `json:"final"`           // finali

	Placements []PlacementPoints `json:"placements,omitempty"`
	GroupWins  []GroupWinPoints  `json:"group_wins,omitempty"`
	Matches    []RankedMatch     `json:"matches,omitempty"`
}

// PlacementPoints is what reaching a stage of one tournament event was worth.
// Only the best few tournaments count (Counted).
type PlacementPoints struct {
	Date       string  `json:"date"` // dd/mm/yyyy, the day of the event
	Tournament string  `json:"tournament"`
	Event      string  `json:"event"`
	Level      string  `json:"level"` // Gara rif.: "Open3", "TRP 4"…
	Pos        string  `json:"pos"`   // stage reached: GQ (groups), 32F, 16F, OF, QF, SF, F…
	Points     float64 `json:"points"`
	Counted    bool    `json:"counted"`
}

// GroupWinPoints is what the group-stage wins of one event were worth.
type GroupWinPoints struct {
	Tournament string  `json:"tournament"`
	Date       string  `json:"date"`
	Event      string  `json:"event"`
	PerWin     float64 `json:"per_win"`
	Played     int     `json:"played"`
	Won        int     `json:"won"`
	Active     int     `json:"active"` // wins that count
	Points     float64 `json:"points"`
}

// RankedMatch is a tournament match as the ranking sees it.
type RankedMatch struct {
	Date       string `json:"date"`
	Tournament string `json:"tournament"`
	Event      string `json:"event"`
	Opponent   string `json:"opponent"`
	Phase      string `json:"phase"` // GIR (groups) or TAB (knockout)
	Won        bool   `json:"won"`
	Counted    bool   `json:"counted"` // false for rows the portal greys out ("discarded")
}

// RankingDetails reads the points breakdown at Ranking.DetailsPath.
func (c *Client) RankingDetails(ctx context.Context, path string) (*RankingDetails, error) {
	body, err := c.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	return ParseRankingDetails(body)
}

var summaryRe = regexp.MustCompile(`(?i)(punti iniziali|campionato|vittorie tornei|tornei|protezione|finali)\s+(-?[\d.]+)`)

// ParseRankingDetails parses a details_page_<n>.html: a summary line, then
// tables under h3 headings for matches, placing points and group wins.
func ParseRankingDetails(body []byte) (*RankingDetails, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	summary := clean(doc.Find("div.subtle").First().Text())
	if summary == "" {
		return nil, fmt.Errorf("points detail: no summary found, layout may have changed")
	}
	d := &RankingDetails{}
	for _, m := range summaryRe.FindAllStringSubmatch(summary, -1) {
		v, _ := strconv.ParseFloat(m[2], 64)
		switch strings.ToLower(m[1]) {
		case "punti iniziali":
			d.Start = v
		case "campionato":
			d.Championship = v
		case "tornei":
			d.Tournaments = v
		case "vittorie tornei":
			d.TournamentWins = v
		case "protezione":
			d.Protection = v
		case "finali":
			d.Final = v
		}
	}

	num := func(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	doc.Find("h3").Each(func(_ int, h *goquery.Selection) {
		title := strings.ToLower(clean(h.Text()))
		h.NextFiltered("table").Find("tbody tr").Each(func(_ int, tr *goquery.Selection) {
			td := tr.ChildrenFiltered("td")
			cell := func(i int) string { return clean(td.Eq(i).Text()) }
			switch {
			case strings.HasPrefix(title, "incontri") && td.Length() == 7:
				class, _ := tr.Attr("class")
				d.Matches = append(d.Matches, RankedMatch{Date: cell(0), Tournament: cell(1), Event: cell(2),
					Opponent: cell(4), Phase: cell(5), Won: cell(6) == "V", Counted: !strings.Contains(class, "discarded")})
			case strings.Contains(title, "posizionamento") && td.Length() == 8:
				d.Placements = append(d.Placements, PlacementPoints{Date: cell(0), Tournament: cell(2), Points: num(cell(3)),
					Event: cell(4), Level: cell(5), Pos: cell(6), Counted: strings.HasPrefix(cell(7), "S")})
			case strings.Contains(title, "vinti nei gironi") && td.Length() == 8:
				d.GroupWins = append(d.GroupWins, GroupWinPoints{Tournament: cell(0), Date: cell(1), Event: cell(2),
					PerWin: num(cell(3)), Played: atoi(cell(4)), Won: atoi(cell(5)), Active: atoi(cell(6)), Points: num(cell(7))})
			}
		})
	})
	return d, nil
}

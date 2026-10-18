package fitet

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/PuerkitoBio/goquery"
)

// Standing is one row of the group table (classifica squadre).
// Field comments give the portal's column header.
type Standing struct {
	Pos          int    `json:"pos"`
	TeamID       int    `json:"team_id"` // SQUADRA
	Team         string `json:"team"`
	Points       int    `json:"points"`        // Punti
	Played       int    `json:"played"`        // ID  incontri disputati
	Won          int    `json:"won"`           // IV
	Drawn        int    `json:"drawn"`         // IPa
	Lost         int    `json:"lost"`          // IP
	GamesWon     int    `json:"games_won"`     // PaV partite vinte
	GamesLost    int    `json:"games_lost"`    // PaP
	SetsWon      int    `json:"sets_won"`      // SV
	SetsLost     int    `json:"sets_lost"`     // SP
	PointsWon    int    `json:"points_won"`    // PV
	PointsLost   int    `json:"points_lost"`   // PP
	PenaltyPoint int    `json:"penalty_point"` // Pe
}

func (c *Client) Standings(ctx context.Context, cam int) ([]Standing, error) {
	body, err := c.Get(ctx, fmt.Sprintf("campionati/classifica_squadre.php?CAM=%d", cam))
	if err != nil {
		return nil, err
	}
	return ParseStandings(body)
}

// ParseStandings parses classifica_squadre.php. Team rows are the <tr>s with
// 14 cells whose first cell links to DatiSquadra.asp; the portal lists them
// already sorted by position.
func ParseStandings(body []byte) ([]Standing, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	var rows []Standing
	var parseErr error
	doc.Find("tr").Each(func(_ int, tr *goquery.Selection) {
		if parseErr != nil {
			return
		}
		td := tr.ChildrenFiltered("td")
		if td.Length() != 14 {
			return
		}
		href, ok := td.Eq(0).Find(`a[href*="DatiSquadra"]`).Attr("href")
		if !ok {
			return
		}
		u, err := url.Parse(href)
		if err != nil {
			parseErr = fmt.Errorf("bad team link %q: %w", href, err)
			return
		}

		var nums [12]int
		for i := range nums {
			s := clean(td.Eq(i + 2).Text())
			n, err := strconv.Atoi(s)
			if err != nil {
				parseErr = fmt.Errorf("standings column %d: %q is not a number", i+2, s)
				return
			}
			nums[i] = n
		}
		teamID, _ := strconv.Atoi(u.Query().Get("SQUADRA"))
		rows = append(rows, Standing{
			Pos: len(rows) + 1, TeamID: teamID, Team: clean(td.Eq(1).Text()),
			Points: nums[0], Played: nums[1], Won: nums[2], Drawn: nums[3], Lost: nums[4],
			GamesWon: nums[5], GamesLost: nums[6], SetsWon: nums[7], SetsLost: nums[8],
			PointsWon: nums[9], PointsLost: nums[10], PenaltyPoint: nums[11],
		})
	})
	if parseErr != nil {
		return nil, parseErr
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no teams found: standings page layout may have changed")
	}
	return rows, nil
}

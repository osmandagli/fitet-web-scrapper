package fitet

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// TournamentKinds are the TOR codes of a regional committee's tournament
// lists (the "tornei" menu of regioni/menu_tor.php). A tournament can be in
// more than one list, e.g. a Categoria tournament that also has youth events.
var TournamentKinds = []int{100, 101, 103, 105, 102, 110, 111, 112, 108, 107}

// TournamentRef is one entry of a season's tournament list.
type TournamentRef struct {
	ID     int    `json:"id"`    // IDT, numbered per committee and season
	Date   string `json:"date"`  // first day, dd/mm/yyyy
	Place  string `json:"place"` // "Saronno (VA)"
	Kinds  []int  `json:"kinds"`
	Played bool   `json:"played"` // results published (otherwise only entries)
}

// Tournaments lists the current season's tournaments of one kind for a
// regional committee (COMIT, e.g. 4 = Lombardia).
func (c *Client) Tournaments(ctx context.Context, com, kind int) ([]TournamentRef, error) {
	body, err := c.Get(ctx, fmt.Sprintf("tornei/elenco_tornei.php?TOR=%d&COMIT=%d&ID=0", kind, com))
	if err != nil {
		return nil, err
	}
	return ParseTournamentList(body, kind)
}

// ParseTournamentList parses tornei/elenco_tornei.php: one link per
// tournament, to its results when played or to its entries when not.
func ParseTournamentList(body []byte, kind int) ([]TournamentRef, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var out []TournamentRef
	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		u, err := url.Parse(href)
		if err != nil {
			return
		}
		played := strings.HasPrefix(u.Path, "visualizza_risultati.php")
		if !played && !strings.HasPrefix(u.Path, "dati_sintetici.php") {
			return
		}
		id, err := strconv.Atoi(u.Query().Get("IDT"))
		if err != nil {
			return
		}
		// "29/08/2026<br>Saronno (VA)"
		html, _ := a.Html()
		date, place, _ := strings.Cut(html, "<br")
		if i := strings.Index(place, ">"); i >= 0 {
			place = place[i+1:]
		}
		out = append(out, TournamentRef{ID: id, Date: clean(date), Place: clean(unescape(place)),
			Kinds: []int{kind}, Played: played})
	})
	return out, nil
}

func unescape(s string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(s))
	if err != nil {
		return s
	}
	return doc.Text()
}

// TournamentInfo is a tournament's name and events.
type TournamentInfo struct {
	Name      string            `json:"name"`
	Where     string            `json:"where"`                // "Saronno (VA) - 29/08/2026" or "… dal 31/10/2026 al 01/11/2026"
	DrawsPath string            `json:"draws_path,omitempty"` // index of the final draws, played tournaments only
	Events    []TournamentEvent `json:"events"`
}

type TournamentEvent struct {
	Gara int    `json:"gara"`
	Name string `json:"name"` // "Singolo M/F Open3 451-2500M | 26-200F"
}

// TournamentInfo reads the event list of a tournament: the results page when
// it has been played, the entries page otherwise.
func (c *Client) TournamentInfo(ctx context.Context, com, id int, played bool) (*TournamentInfo, error) {
	page := "iscrizioni.php"
	if played {
		page = "elenco_gare.php"
	}
	body, err := c.Get(ctx, fmt.Sprintf("tornei/%s?IDT=%d&COM=%d", page, id, com))
	if err != nil {
		return nil, err
	}
	return ParseTournamentInfo(body)
}

// ParseTournamentInfo parses tornei/elenco_gare.php and tornei/iscrizioni.php:
// a heading, the name, the place and dates, then one link per event.
func ParseTournamentInfo(body []byte) (*TournamentInfo, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var lines []string
	doc.Find("p.dettagli > b").Each(func(_ int, b *goquery.Selection) {
		lines = append(lines, clean(b.Text()))
	})
	if len(lines) < 3 {
		return nil, fmt.Errorf("tournament page: no name found, layout may have changed")
	}
	t := &TournamentInfo{Name: lines[1], Where: lines[2]}
	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		u, err := url.Parse(href)
		if err != nil {
			return
		}
		switch {
		case strings.HasSuffix(u.Path, "_home.html"):
			t.DrawsPath = path.Join("tornei", u.Path)
		case u.Query().Get("GARA") != "":
			gara, err := strconv.Atoi(u.Query().Get("GARA"))
			if err == nil {
				t.Events = append(t.Events, TournamentEvent{Gara: gara, Name: clean(a.Text())})
			}
		}
	})
	return t, nil
}

// DrawPage is one phase of one event in a tournament's final draws.
type DrawPage struct {
	Event    string `json:"event"` // "Singolo M/F Open3 451-2500M | 26-200F"
	Knockout bool   `json:"knockout"`
	Path     string `json:"path"`
}

// DrawPages reads the index of a tournament's final draws (TournamentInfo.DrawsPath).
func (c *Client) DrawPages(ctx context.Context, drawsPath string) ([]DrawPage, error) {
	body, err := c.Get(ctx, drawsPath)
	if err != nil {
		return nil, err
	}
	return ParseDrawIndex(body, path.Dir(drawsPath))
}

// ParseDrawIndex parses tornei/tabelloni/<IDT>_<COM>_home.html: links named
// "<event> - gironi" and "<event> - eliminatoria diretta".
func ParseDrawIndex(body []byte, dir string) ([]DrawPage, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var out []DrawPage
	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		name := clean(a.Text())
		ev, phase, ok := cutLast(name, " - ")
		if !ok || strings.Contains(href, "/") || !strings.HasSuffix(href, ".html") {
			return
		}
		out = append(out, DrawPage{Event: ev, Knockout: !strings.EqualFold(phase, "gironi"), Path: path.Join(dir, href)})
	})
	return out, nil
}

func cutLast(s, sep string) (before, after string, ok bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// DrawGroup is one qualifying group of an event.
type DrawGroup struct {
	No        int             `json:"no"`
	Matches   []GroupMatch    `json:"matches"`
	Standings []GroupStanding `json:"standings"`
}

type GroupMatch struct {
	A     string   `json:"a"`
	B     string   `json:"b"`
	SetsA int      `json:"sets_a"`
	SetsB int      `json:"sets_b"`
	Sets  []string `json:"sets"` // "7-11", from A's side
}

type GroupStanding struct {
	Pos    int    `json:"pos"`
	Name   string `json:"name"`
	Points int    `json:"points"` // PTS
	Played int    `json:"played"` // G
	Won    int    `json:"won"`    // V
	Lost   int    `json:"lost"`   // P
}

// Groups reads the group phase of an event (a DrawPage that isn't Knockout).
func (c *Client) Groups(ctx context.Context, drawPath string) ([]DrawGroup, error) {
	body, err := c.Get(ctx, drawPath)
	if err != nil {
		return nil, err
	}
	return ParseGroups(body)
}

var groupHeadRe = regexp.MustCompile(`^(Incontri|Classifica) girone (\d+)$`)

// ParseGroups parses a "fase gironi" draw page: per group, a table of
// matches ("Incontri girone N") and a table of standings ("Classifica girone N").
func ParseGroups(body []byte) ([]DrawGroup, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var out []DrawGroup
	byNo := map[int]int{}
	doc.Find("table").Each(func(_ int, table *goquery.Selection) {
		rows := table.Find("tr")
		m := groupHeadRe.FindStringSubmatch(clean(rows.First().Find("td").First().Text()))
		if m == nil {
			return
		}
		no, _ := strconv.Atoi(m[2])
		i, ok := byNo[no]
		if !ok {
			i = len(out)
			byNo[no] = i
			out = append(out, DrawGroup{No: no})
		}
		g := &out[i]
		rows.Slice(1, goquery.ToEnd).Each(func(_ int, tr *goquery.Selection) {
			td := tr.Find("td")
			cell := func(j int) string { return clean(td.Eq(j).Text()) }
			switch {
			case m[1] == "Incontri" && td.Length() >= 4:
				gm := GroupMatch{A: cell(0), B: cell(1)}
				a, b, _ := strings.Cut(cell(2), "-")
				gm.SetsA, _ = strconv.Atoi(a)
				gm.SetsB, _ = strconv.Atoi(b)
				for _, s := range strings.Split(cell(3), ",") {
					if s = strings.TrimSpace(s); s != "" {
						gm.Sets = append(gm.Sets, s)
					}
				}
				g.Matches = append(g.Matches, gm)
			case m[1] == "Classifica" && td.Length() >= 6:
				var n [5]int
				for j, col := range []int{0, 2, 3, 4, 5} {
					n[j], _ = strconv.Atoi(cell(col))
				}
				g.Standings = append(g.Standings, GroupStanding{Pos: n[0], Name: cell(1),
					Points: n[1], Played: n[2], Won: n[3], Lost: n[4]})
			}
		})
	})
	if len(out) == 0 {
		return nil, fmt.Errorf("no groups found: draw page layout may have changed")
	}
	return out, nil
}

// Bracket is the knockout phase of an event, first round first. The last
// round holds the final.
type Bracket struct {
	Rounds [][]KOMatch `json:"rounds"`
}

// KOMatch is one knockout match. A or B is "" for a bye; Winner is "" while
// the match hasn't been played.
type KOMatch struct {
	A      string   `json:"a"`
	B      string   `json:"b"`
	Winner string   `json:"winner,omitempty"`
	Sets   []string `json:"sets,omitempty"`   // "11-9", from the winner's side
	WonBy  string   `json:"won_by,omitempty"` // "3-1"; "" for byes and walkovers
}

// Loser is the player knocked out in m ("" for a bye or an unplayed match).
func (m KOMatch) Loser() string {
	switch m.Winner {
	case "":
		return ""
	case m.A:
		return m.B
	}
	return m.A
}

// Knockout reads the knockout phase of an event (a Knockout DrawPage).
func (c *Client) Knockout(ctx context.Context, drawPath string) (*Bracket, error) {
	body, err := c.Get(ctx, drawPath)
	if err != nil {
		return nil, err
	}
	return ParseBracket(body)
}

var clubCodeRe = regexp.MustCompile(`\s*\(\d+\)$`)

// ParseBracket parses a "fase eliminatoria" draw page. It is one table where
// a cell's rowspan says the round: 1 = the draw, 2 = winners of the first
// round, 4 = of the second, and so on. Cells read "Name (club code)<br>score",
// the score being the loser's points per set from the winner's side: "9,-12"
// is 11-9, 12-14. "< X >" is an empty slot (a bye).
func ParseBracket(body []byte) (*Bracket, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	type cell struct{ name, score string }
	var cols [][]cell // by round: 0 = draw
	doc.Find("table").First().Find("td").Each(func(_ int, td *goquery.Selection) {
		span := 1
		if s, ok := td.Attr("rowspan"); ok {
			span, _ = strconv.Atoi(s)
		}
		round := 0
		for n := span; n > 1; n /= 2 {
			round++
		}
		for len(cols) <= round {
			cols = append(cols, nil)
		}
		html, _ := td.Html()
		name, score, _ := strings.Cut(html, "<br")
		if i := strings.Index(score, ">"); i >= 0 {
			score = score[i+1:]
		}
		n := clean(unescape(name))
		if n == "< X >" {
			n = ""
		}
		cols[round] = append(cols[round], cell{clubCodeRe.ReplaceAllString(n, ""), clean(unescape(score))})
	})
	if len(cols) < 2 {
		return nil, fmt.Errorf("no knockout draw found: draw page layout may have changed")
	}

	b := &Bracket{}
	for r := 1; r < len(cols); r++ {
		prev := cols[r-1]
		var round []KOMatch
		for i := 0; 2*i+1 < len(prev); i++ {
			m := KOMatch{A: prev[2*i].name, B: prev[2*i+1].name}
			if i < len(cols[r]) {
				w := cols[r][i]
				m.Winner = w.name
				m.Sets, m.WonBy = winnerSets(w.score)
			}
			round = append(round, m)
		}
		b.Rounds = append(b.Rounds, round)
	}
	return b, nil
}

// winnerSets expands "5,-12,-8,8,9" to 11-5, 12-14, 8-11, 11-8, 11-9 and "3-2".
func winnerSets(score string) ([]string, string) {
	if score == "" {
		return nil, ""
	}
	var sets []string
	won, lost := 0, 0
	for _, s := range strings.Split(score, ",") {
		s = strings.TrimSpace(s)
		neg := strings.HasPrefix(s, "-")
		n, err := strconv.Atoi(strings.TrimPrefix(s, "-"))
		if err != nil {
			return nil, ""
		}
		other := max(11, n+2)
		if neg {
			sets = append(sets, fmt.Sprintf("%d-%d", n, other))
			lost++
		} else {
			sets = append(sets, fmt.Sprintf("%d-%d", other, n))
			won++
		}
	}
	return sets, fmt.Sprintf("%d-%d", won, lost)
}

// Entry is an athlete entered in an event of an upcoming tournament.
type Entry struct {
	Name   string `json:"name"`
	Points int    `json:"points"` // ranking points at entry
	Club   string `json:"club"`
}

// Entries lists who has entered an event of an upcoming tournament.
func (c *Client) Entries(ctx context.Context, com, id, gara int) ([]Entry, error) {
	body, err := c.Get(ctx, fmt.Sprintf("tornei/iscrizioni_gara.php?GARA=%d&IDT=%d&COM=%d", gara, id, com))
	if err != nil {
		return nil, err
	}
	return ParseEntries(body)
}

// ParseEntries parses tornei/iscrizioni_gara.php: rows of number, name,
// (blank), points ("6812 *": a woman's points, scaled by 0.75), club.
func ParseEntries(body []byte) ([]Entry, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var out []Entry
	doc.Find("tr").Each(func(_ int, tr *goquery.Selection) {
		td := tr.ChildrenFiltered("td")
		if td.Length() != 5 || td.Eq(0).Find("p.position").Length() == 0 {
			return
		}
		pts, _ := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(clean(td.Eq(3).Text()), "*")))
		out = append(out, Entry{Name: clean(td.Eq(1).Text()), Points: pts, Club: clean(td.Eq(4).Text())})
	})
	return out, nil
}

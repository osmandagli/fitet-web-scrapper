package fitet

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Ranking is an athlete's entry in the national individual ranking
// (classifica), from new_rank/dettaglioatleta_unica.php.
type Ranking struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	Country  string  `json:"country,omitempty"` // flag title, e.g. "Italia"
	Sector   string  `json:"sector"`            // Settore agonistico: "Seniores", "Master 65-70"...
	Club     string  `json:"club"`
	Region   string  `json:"region"`
	Category string  `json:"category"`           // "4^", "5^"..., or "Straniero"
	Points   float64 `json:"points"`             // e.g. 6219.81
	Position int     `json:"position,omitempty"` // position in the national men's or women's ranking; 0 for foreign athletes
	// Gender is "M"/"F" when the portal shows its placeholder photo (m-/f-vuoto.png),
	// which tells which ranking Position refers to; "" when a real photo hides it.
	Gender string `json:"gender,omitempty"`
	Best   string `json:"best,omitempty"` // Record classifica: "n.5 il 30/06/1983"
	AsOf   string `json:"as_of"`          // date of the ranking, dd/mm/yyyy

	// Season summary of ranking-valid matches.
	StartPoints float64 `json:"start_points"`
	Gained      float64 `json:"gained"`
	Played      int     `json:"played"`
	Won         int     `json:"won"`
	Lost        int     `json:"lost"`

	History []RankPoint `json:"history"` // newest first

	// DetailsPath is the page breaking the points down by tournament
	// (RankingDetails), e.g. "new_rank/html/247/Maschile/…/details_page_1306.html".
	DetailsPath string `json:"details_path,omitempty"`
}

type RankPoint struct {
	Date     string `json:"date"` // yyyy-mm-dd
	Position int    `json:"position,omitempty"`
	Points   int    `json:"points"`
	Category string `json:"category"`
}

// RankingName says which list Position belongs to.
func (r *Ranking) RankingName() string {
	switch r.Gender {
	case "M":
		return "men's ranking"
	case "F":
		return "women's ranking"
	}
	return "national ranking"
}

// CategoryLabel renders the category for display: "4ª cat" or "Foreign".
func (r *Ranking) CategoryLabel() string {
	if r == nil || r.Category == "" {
		return ""
	}
	if n := strings.TrimSuffix(r.Category, "^"); n != r.Category {
		return n + "ª cat"
	}
	if strings.EqualFold(r.Category, "Straniero") {
		return "Foreign"
	}
	return r.Category
}

var rankClassRe = regexp.MustCompile(`testaclassifica_auto\.php\?ID_CLASS=(\d+)`)

// LatestRankingClass returns the ID_CLASS of the most recent ranking
// publication (the portal menu lists them newest first).
func (c *Client) LatestRankingClass(ctx context.Context) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(c.BaseURL, "/risultati")+"/menu.php", nil)
	if err != nil {
		return 0, err
	}
	body, err := c.do(req)
	if err != nil {
		return 0, err
	}
	m := rankClassRe.FindSubmatch(body)
	if m == nil {
		return 0, fmt.Errorf("no ranking found in portal menu")
	}
	return strconv.Atoi(string(m[1]))
}

func (c *Client) Ranking(ctx context.Context, id, class int) (*Ranking, error) {
	q := url.Values{"ATLETA": {strconv.Itoa(id)}, "ID_CLASS": {strconv.Itoa(class)}, "ZU": {"1"}, "AVVERSARIO": {"0"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/new_rank/dettaglioatleta_unica.php?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", c.BaseURL+"/new_rank/testaclassifica_auto.php")
	body, err := c.do(req)
	if err != nil {
		return nil, err
	}
	r, err := ParseRanking(body)
	if err != nil {
		return nil, fmt.Errorf("ranking of %d: %w", id, err)
	}
	r.ID = id
	return r, nil
}

var (
	pointsRe = regexp.MustCompile(`punti\s+([\d.]+,\d+)`)
	asOfRe   = regexp.MustCompile(`AL (\d{2}/\d{2}/\d{4})`)
)

// ParseRanking parses dettaglioatleta_unica.php: a label/value card
// (p.style_scheda followed by p.style_scheda2), the position in p.classifica,
// a season summary table and the ranking history table.
func ParseRanking(body []byte) (*Ranking, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	r := &Ranking{Name: clean(doc.Find("p.titolo").First().Text())}
	if r.Name == "" {
		return nil, fmt.Errorf("no athlete in ranking page (not ranked?)")
	}
	r.Country, _ = doc.Find("p.titolo img").Attr("title")
	r.Position, _ = strconv.Atoi(clean(doc.Find("p.classifica").First().Text()))
	doc.Find("img").EachWithBreak(func(_ int, img *goquery.Selection) bool {
		src, _ := img.Attr("src")
		switch {
		case strings.HasSuffix(src, "/m-vuoto.png"):
			r.Gender = "M"
		case strings.HasSuffix(src, "/f-vuoto.png"):
			r.Gender = "F"
		default:
			return true
		}
		return false
	})

	doc.Find("p.style_scheda").Each(func(_ int, p *goquery.Selection) {
		val := clean(p.Closest("td").Next().Text())
		switch clean(p.Text()) {
		case "Settore Agonistico":
			r.Sector = val
		case "Societa' appartenenza":
			r.Club = val
		case "Regione":
			r.Region = val
		case "Categoria":
			r.Category = strings.TrimSpace(strings.SplitN(val, " ", 2)[0])
			if m := pointsRe.FindStringSubmatch(val); m != nil {
				r.Points = itNumber(m[1])
			}
		case "Record classifica":
			if val != "n. il" {
				r.Best = val
			}
		}
	})

	// Season summary: the first row of numbers in #fragment-1.
	var cells []string
	doc.Find("#fragment-1 td").Each(func(_ int, td *goquery.Selection) {
		cells = append(cells, clean(td.Text()))
	})
	if m := asOfRe.FindStringSubmatch(clean(doc.Find("#fragment-1").Text())); m != nil {
		r.AsOf = m[1]
	}
	var nums []string
	for _, c := range cells {
		if c != "" && (c[0] >= '0' && c[0] <= '9') {
			nums = append(nums, c)
		}
	}
	// PUNTI INIZIALI [date] | PUNTI TOTALI | PUNTEGGIO FINALE | GIOCATE | VINTE | PERSE | % ...
	if len(nums) >= 6 {
		r.StartPoints = itNumber(strings.Fields(nums[0])[0])
		r.Gained = itNumber(nums[1])
		r.Played, _ = strconv.Atoi(nums[3])
		r.Won, _ = strconv.Atoi(nums[4])
		r.Lost, _ = strconv.Atoi(nums[5])
	}

	// "Dettaglio Punti" tab: an iframe with a static page per athlete and publication.
	if src, ok := doc.Find("#fragment-3 iframe").Attr("src"); ok {
		src = strings.Join(strings.Fields(src), "") // the portal wraps long src attributes
		if i := strings.Index(src, "new_rank/"); i >= 0 {
			r.DetailsPath = src[i:]
		}
	}

	seen := map[string]bool{} // the history table is repeated in two tabs
	doc.Find("tr").Each(func(_ int, tr *goquery.Selection) {
		td := tr.ChildrenFiltered("td")
		if td.Length() != 4 {
			return
		}
		date := clean(td.Eq(0).Text())
		if len(date) != 10 || date[4] != '-' || seen[date] {
			return
		}
		pts, err := strconv.Atoi(clean(td.Eq(2).Text()))
		if err != nil {
			return
		}
		pos, _ := strconv.Atoi(clean(td.Eq(1).Text()))
		seen[date] = true
		r.History = append(r.History, RankPoint{Date: date, Position: pos, Points: pts, Category: clean(td.Eq(3).Text())})
	})
	return r, nil
}

// itNumber parses Italian formatted numbers: "6.219,81" -> 6219.81.
func itNumber(s string) float64 {
	s = strings.ReplaceAll(strings.TrimSpace(s), ".", "")
	f, _ := strconv.ParseFloat(strings.Replace(s, ",", ".", 1), 64)
	return f
}

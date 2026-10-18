// Package fitet scrapes the FITET results portal (portale.fitet.org).
package fitet

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

const BaseURL = "https://portale.fitet.org/risultati"

type Client struct {
	HTTP    *http.Client
	BaseURL string
}

func NewClient() *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		BaseURL: BaseURL,
	}
}

// Get fetches a portal page and returns its body as UTF-8.
// path is relative to BaseURL, e.g. "campionati/Calendario.asp?CAM=408&ANNO=41".
func (c *Client) Get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/"+path, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

// do sends req and returns the body as UTF-8.
func (c *Client) do(req *http.Request) ([]byte, error) {
	req.Header.Set("User-Agent", "fitet-web-scrapper/0.1 (personal results tracker)")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", req.URL.Path, resp.Status)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return toUTF8(raw)
}

// toUTF8 normalises the portal's mixed encodings: some pages are UTF-8 with a
// BOM, most are Windows-1252 (e.g. "Romanò" arrives as a single 0xF2 byte).
func toUTF8(b []byte) ([]byte, error) {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if utf8.Valid(b) {
		return b, nil
	}
	return charmap.Windows1252.NewDecoder().Bytes(b)
}

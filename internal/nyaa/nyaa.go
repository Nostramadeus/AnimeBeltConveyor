// Package nyaa fetches and parses nyaa.si RSS search feeds and builds
// magnet links (SPEC 4, nyaa.si).
package nyaa

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UserAgent is sent with every request (SPEC 4).
const UserAgent = "AnimeBeltConveyor/0.1 (+github.com/Nostramadeus/AnimeBeltConveyor)"

// DefaultBase is the default Conveyor.NyaaBase.
const DefaultBase = "https://nyaa.si"

// MinInterval is the minimum gap between two nyaa requests (SPEC 4).
const MinInterval = 2 * time.Second

// Trackers are appended to every magnet link.
var Trackers = []string{
	"udp://open.stealth.si:80/announce",
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://exodus.desync.com:6969/announce",
	"udp://tracker.torrent.eu.org:451/announce",
}

// HTTPClient is used by Fetch. Tests or callers may replace it.
var HTTPClient = &http.Client{Timeout: 30 * time.Second}

// maxBody caps how much of a feed is read (a full nyaa page is ~100 KB).
const maxBody = 10 << 20

// Item is one RSS item.
type Item struct {
	Title      string
	Link       string // .torrent URL
	GUID       string // https://nyaa.si/view/<id>
	ID         int64  // nyaa id from GUID, 0 if not found (releases.nyaa_id)
	PubDate    time.Time
	Seeders    int
	Leechers   int
	Downloads  int
	InfoHash   string // 40-hex btih as nyaa sends it
	Size       string // "1.4 GiB"
	SizeBytes  int64
	CategoryID string // "1_2"
	Category   string // "Anime - English-translated"
	Trusted    bool
	Remake     bool
}

// rawItem mirrors the XML. Tags carry no namespace so the nyaa: fields
// match whatever namespace URI the mirror declares.
type rawItem struct {
	Title      string `xml:"title"`
	Link       string `xml:"link"`
	GUID       string `xml:"guid"`
	PubDate    string `xml:"pubDate"`
	Seeders    string `xml:"seeders"`
	Leechers   string `xml:"leechers"`
	Downloads  string `xml:"downloads"`
	InfoHash   string `xml:"infoHash"`
	CategoryID string `xml:"categoryId"`
	Category   string `xml:"category"`
	Size       string `xml:"size"`
	Trusted    string `xml:"trusted"`
	Remake     string `xml:"remake"`
}

type feed struct {
	Items []rawItem `xml:"channel>item"`
}

// FeedURL builds {base}/?page=rss&q={query}&c={category}&f=0.
func FeedURL(base, query, category string) string {
	v := url.Values{}
	v.Set("page", "rss")
	v.Set("q", query)
	v.Set("c", category)
	v.Set("f", "0")
	return strings.TrimRight(base, "/") + "/?" + v.Encode()
}

// Fetch downloads and parses one RSS search. It does not throttle; the
// caller runs requests one at a time through a Throttle.
func Fetch(ctx context.Context, base, query, category string) ([]Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, FeedURL(base, query, category), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/rss+xml, application/xml;q=0.9, */*;q=0.1")
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("nyaa: %s returned %s", req.URL.Host, resp.Status)
	}
	return ParseRSS(io.LimitReader(resp.Body, maxBody))
}

// ParseRSS parses a nyaa RSS document.
func ParseRSS(r io.Reader) ([]Item, error) {
	var f feed
	if err := xml.NewDecoder(r).Decode(&f); err != nil {
		return nil, fmt.Errorf("nyaa: bad RSS: %w", err)
	}
	items := make([]Item, 0, len(f.Items))
	for _, ri := range f.Items {
		it := Item{
			Title:      strings.TrimSpace(ri.Title),
			Link:       strings.TrimSpace(ri.Link),
			GUID:       strings.TrimSpace(ri.GUID),
			Seeders:    num(ri.Seeders),
			Leechers:   num(ri.Leechers),
			Downloads:  num(ri.Downloads),
			InfoHash:   strings.TrimSpace(ri.InfoHash),
			Size:       strings.TrimSpace(ri.Size),
			CategoryID: strings.TrimSpace(ri.CategoryID),
			Category:   strings.TrimSpace(ri.Category),
			Trusted:    yes(ri.Trusted),
			Remake:     yes(ri.Remake),
		}
		it.ID = IDFromGUID(it.GUID)
		it.SizeBytes = ParseSize(it.Size)
		it.PubDate = parseDate(ri.PubDate)
		items = append(items, it)
	}
	return items, nil
}

func num(s string) int { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }

func yes(s string) bool { return strings.EqualFold(strings.TrimSpace(s), "yes") }

func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

var reViewID = regexp.MustCompile(`/view/(\d+)`)
var reTrailingDigits = regexp.MustCompile(`(\d+)/?$`)

// IDFromGUID extracts the nyaa id from "https://nyaa.si/view/<id>".
// It returns 0 when there is no id.
func IDFromGUID(guid string) int64 {
	m := reViewID.FindStringSubmatch(guid)
	if m == nil {
		m = reTrailingDigits.FindStringSubmatch(strings.TrimSpace(guid))
	}
	if m == nil {
		return 0
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	return n
}

var reSize = regexp.MustCompile(`(?i)^\s*([\d.,]+)\s*([KMGTP]i?B|B|Bytes?)?\s*$`)

var sizeUnits = map[string]float64{
	"": 1, "b": 1, "byte": 1, "bytes": 1,
	"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40, "pib": 1 << 50,
	"kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12, "pb": 1e15,
}

// ParseSize turns nyaa's size string ("1.4 GiB", "700.5 MiB", "512 Bytes")
// into bytes. Unknown input gives 0.
func ParseSize(s string) int64 {
	m := reSize.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
	if err != nil || v < 0 {
		return 0
	}
	mult, ok := sizeUnits[strings.ToLower(m[2])]
	if !ok {
		return 0
	}
	return int64(math.Round(v * mult))
}

// Magnet builds magnet:?xt=urn:btih:<hash>&dn=<title>&tr=... with the four
// SPEC trackers. Spaces in the title are encoded as %20.
func Magnet(infoHash, title string) string {
	var b strings.Builder
	b.WriteString("magnet:?xt=urn:btih:")
	b.WriteString(strings.TrimSpace(infoHash))
	if title != "" {
		b.WriteString("&dn=")
		b.WriteString(escape(title))
	}
	for _, tr := range Trackers {
		b.WriteString("&tr=")
		b.WriteString(escape(tr))
	}
	return b.String()
}

func escape(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

// MaxTorrentBytes caps a downloaded .torrent file (SPEC v1.1 section 2).
const MaxTorrentBytes = 8 << 20

// TorrentTimeout bounds one .torrent download.
const TorrentTimeout = 20 * time.Second

// FetchTorrent downloads a .torrent file (http/https only, 20 s, at most 8 MB).
// It does not throttle; the caller runs it through the same Throttle as Fetch.
// A body that is not a bencoded dictionary (an HTML error page, say) is refused
// so it never lands in a torrent app's watch folder.
func FetchTorrent(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("nyaa: not a torrent download link: %q", rawURL)
	}
	ctx, cancel := context.WithTimeout(ctx, TorrentTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/x-bittorrent, */*;q=0.1")
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("nyaa: %s returned %s", u.Host, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxTorrentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxTorrentBytes {
		return nil, fmt.Errorf("nyaa: .torrent file is larger than %d MB", MaxTorrentBytes>>20)
	}
	if len(b) < 2 || b[0] != 'd' || b[len(b)-1] != 'e' {
		return nil, errors.New("nyaa: the download is not a .torrent file")
	}
	return b, nil
}

// Throttle spaces calls at least Gap apart. Safe for concurrent use.
type Throttle struct {
	mu   sync.Mutex
	gap  time.Duration
	next time.Time
}

// NewThrottle returns a Throttle with the given gap, raised to MinInterval
// (2 s) when smaller.
func NewThrottle(gap time.Duration) *Throttle {
	if gap < MinInterval {
		gap = MinInterval
	}
	return &Throttle{gap: gap}
}

// Gap is the enforced spacing.
func (t *Throttle) Gap() time.Duration { return t.gap }

// Wait blocks until the caller's slot comes up: the first call returns at
// once, each later call at least Gap after the previous slot. It returns
// ctx.Err() if ctx ends first.
func (t *Throttle) Wait(ctx context.Context) error {
	t.mu.Lock()
	now := time.Now()
	slot := now
	if t.next.After(now) {
		slot = t.next
	}
	t.next = slot.Add(t.gap)
	t.mu.Unlock()

	d := time.Until(slot)
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

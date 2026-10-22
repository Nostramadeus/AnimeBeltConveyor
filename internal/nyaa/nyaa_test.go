package nyaa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/parse"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/frieren.rss")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFetch(t *testing.T) {
	body := fixture(t)
	var gotQuery url.Values
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		gotUA = r.Header.Get("User-Agent")
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write(body)
	}))
	defer srv.Close()

	items, err := Fetch(context.Background(), srv.URL+"/", "Sousou no Frieren", "1_2")
	if err != nil {
		t.Fatal(err)
	}
	if gotUA != UserAgent {
		t.Errorf("User-Agent %q", gotUA)
	}
	for k, v := range map[string]string{"page": "rss", "q": "Sousou no Frieren", "c": "1_2", "f": "0"} {
		if gotQuery.Get(k) != v {
			t.Errorf("query %s = %q, want %q", k, gotQuery.Get(k), v)
		}
	}
	if len(items) != 5 {
		t.Fatalf("got %d items, want 5", len(items))
	}
	first := items[0]
	want := Item{
		Title:      "[SubsPlease] Sousou no Frieren - 05 (1080p) [6E8B4C2F].mkv",
		Link:       "https://nyaa.si/download/1712345.torrent",
		GUID:       "https://nyaa.si/view/1712345",
		ID:         1712345,
		PubDate:    time.Date(2023, 10, 6, 16, 30, 12, 0, time.UTC),
		Seeders:    1532,
		Leechers:   41,
		Downloads:  25012,
		InfoHash:   "3f2a9c1e7b4d5a6f8e9d0c1b2a3f4e5d6c7b8a90",
		Size:       "1.4 GiB",
		SizeBytes:  1503238554,
		CategoryID: "1_2",
		Category:   "Anime - English-translated",
		Trusted:    true,
	}
	if first != want {
		t.Errorf("item 0\n got  %+v\n want %+v", first, want)
	}
	if items[3].Trusted || items[3].Seeders != 97 || items[3].ID != 1712801 {
		t.Errorf("item 3: %+v", items[3])
	}

	// end to end with the parser: groups and the one batch
	groups := map[string]bool{}
	batches := 0
	for _, it := range items {
		r := parse.Parse(it.Title)
		groups[r.Group] = true
		if r.IsBatch {
			batches++
			if it.SizeBytes != ParseSize("38.2 GiB") {
				t.Errorf("batch size %d", it.SizeBytes)
			}
		}
	}
	if batches != 1 || len(groups) != 4 {
		t.Errorf("batches %d groups %v", batches, groups)
	}
}

func TestFetchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	if _, err := Fetch(context.Background(), srv.URL, "x", "1_2"); err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("want 429 error, got %v", err)
	}
}

func TestFetchBadXML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body>Cloudflare"))
	}))
	defer srv.Close()
	if _, err := Fetch(context.Background(), srv.URL, "x", "1_2"); err == nil {
		t.Error("want error for broken XML")
	}
}

func TestFetchCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Fetch(ctx, srv.URL, "x", "1_2"); err == nil {
		t.Error("want error for canceled context")
	}
}

func TestEmptyFeed(t *testing.T) {
	items, err := ParseRSS(strings.NewReader(`<?xml version="1.0"?><rss version="2.0"><channel><title>x</title></channel></rss>`))
	if err != nil || len(items) != 0 {
		t.Errorf("empty feed: %v %v", items, err)
	}
}

func TestFeedURL(t *testing.T) {
	got := FeedURL("https://nyaa.si/", "Sousou no Frieren & co", "1_2")
	want := "https://nyaa.si/?c=1_2&f=0&page=rss&q=Sousou+no+Frieren+%26+co"
	if got != want {
		t.Errorf("FeedURL = %q, want %q", got, want)
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"1.4 GiB":   1503238554,
		"700.5 MiB": 734527488,
		"512 KiB":   524288,
		"123 Bytes": 123,
		"2 TiB":     2199023255552,
		"1.5 GB":    1500000000,
		"1,024 MiB": 1073741824,
		"":          0,
		"lots":      0,
		"1.4 XiB":   0,
	}
	for in, want := range cases {
		if got := ParseSize(in); got != want {
			t.Errorf("ParseSize(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestMagnet(t *testing.T) {
	got := Magnet("3f2a9c1e7b4d5a6f8e9d0c1b2a3f4e5d6c7b8a90", "[SubsPlease] Sousou no Frieren - 05 (1080p) [6E8B4C2F].mkv")
	want := "magnet:?xt=urn:btih:3f2a9c1e7b4d5a6f8e9d0c1b2a3f4e5d6c7b8a90" +
		"&dn=%5BSubsPlease%5D%20Sousou%20no%20Frieren%20-%2005%20%281080p%29%20%5B6E8B4C2F%5D.mkv" +
		"&tr=udp%3A%2F%2Fopen.stealth.si%3A80%2Fannounce" +
		"&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337%2Fannounce" +
		"&tr=udp%3A%2F%2Fexodus.desync.com%3A6969%2Fannounce" +
		"&tr=udp%3A%2F%2Ftracker.torrent.eu.org%3A451%2Fannounce"
	if got != want {
		t.Errorf("Magnet\n got  %s\n want %s", got, want)
	}
	// round trip through net/url: dn and all four trackers come back intact
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.ParseQuery(u.RawQuery)
	if q.Get("dn") != "[SubsPlease] Sousou no Frieren - 05 (1080p) [6E8B4C2F].mkv" || len(q["tr"]) != 4 {
		t.Errorf("round trip: %v", q)
	}
	if strings.Contains(Magnet("ab", "a+b c"), "+") {
		t.Error("plus sign must be escaped")
	}
}

func TestIDFromGUID(t *testing.T) {
	cases := map[string]int64{
		"https://nyaa.si/view/1712345":  1712345,
		"https://nyaa.si/view/1712345/": 1712345,
		"https://nyaa.land/view/42?x=1": 42,
		"1712345":                       1712345,
		"https://nyaa.si/":              0,
		"":                              0,
	}
	for in, want := range cases {
		if got := IDFromGUID(in); got != want {
			t.Errorf("IDFromGUID(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestThrottle(t *testing.T) {
	if g := NewThrottle(10 * time.Millisecond).Gap(); g != MinInterval {
		t.Errorf("gap below minimum not raised: %v", g)
	}
	if g := NewThrottle(5 * time.Second).Gap(); g != 5*time.Second {
		t.Errorf("gap %v", g)
	}
	// timing with a short gap (constructed directly to skip the 2 s floor)
	th := &Throttle{gap: 60 * time.Millisecond}
	ctx := context.Background()
	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := th.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if el := time.Since(start); el < 115*time.Millisecond {
		t.Errorf("3 waits took %v, want >= 120ms", el)
	}
	// canceled context returns promptly with an error
	th2 := &Throttle{gap: time.Hour}
	th2.Wait(ctx)
	cctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := th2.Wait(cctx); err == nil {
		t.Error("want ctx error")
	}
}

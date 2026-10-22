package nyaa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchTorrent(t *testing.T) {
	big := "d" + strings.Repeat("x", MaxTorrentBytes) + "e"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.torrent":
			if r.Header.Get("User-Agent") != UserAgent {
				http.Error(w, "no UA", 400)
				return
			}
			w.Write([]byte("d4:infod4:name3:abcee"))
		case "/big.torrent":
			w.Write([]byte(big))
		case "/html":
			w.Write([]byte("<!doctype html><title>Error</title>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	b, err := FetchTorrent(ctx, srv.URL+"/ok.torrent")
	if err != nil || string(b) != "d4:infod4:name3:abcee" {
		t.Fatalf("ok: %q %v", b, err)
	}
	for _, path := range []string{"/big.torrent", "/html", "/missing"} {
		if _, err := FetchTorrent(ctx, srv.URL+path); err == nil {
			t.Errorf("%s: want an error", path)
		}
	}
	for _, u := range []string{"", "file:///C:/x.torrent", "magnet:?xt=urn:btih:abc"} {
		if _, err := FetchTorrent(ctx, u); err == nil {
			t.Errorf("%q: want an error", u)
		}
	}
}

package qbit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const hashHex = "c9e15763f722f23e98a29decdfae341b98d53056"

type fakeQbit struct {
	mu         sync.Mutex
	sid        string
	logins     int
	categories map[string]bool
	added      []map[string]string
	torrents   []Torrent
}

func newFake() (*fakeQbit, *httptest.Server) {
	f := &fakeQbit{categories: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Referer") == "" {
			http.Error(w, "csrf", http.StatusUnauthorized)
			return
		}
		r.ParseForm()
		if r.Form.Get("username") != "admin" || r.Form.Get("password") != "secret" {
			w.Write([]byte("Fails."))
			return
		}
		f.logins++
		f.sid = "sid" + string(rune('0'+f.logins))
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: f.sid, Path: "/"})
		w.Write([]byte("Ok."))
	})
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie("SID")
			f.mu.Lock()
			ok := err == nil && c.Value == f.sid && f.sid != ""
			f.mu.Unlock()
			if !ok {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /api/v2/app/version", auth(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("v5.0.1")) }))
	mux.HandleFunc("POST /api/v2/torrents/createCategory", auth(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.categories[r.Form.Get("category")] {
			http.Error(w, "exists", http.StatusConflict)
			return
		}
		f.categories[r.Form.Get("category")] = true
	}))
	mux.HandleFunc("POST /api/v2/torrents/add", auth(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		m := map[string]string{}
		for k, v := range r.MultipartForm.Value {
			m[k] = v[0]
		}
		f.mu.Lock()
		f.added = append(f.added, m)
		f.mu.Unlock()
		w.Write([]byte("Ok."))
	}))
	mux.HandleFunc("GET /api/v2/torrents/info", auth(func(w http.ResponseWriter, r *http.Request) {
		cat, hashes := r.URL.Query().Get("category"), r.URL.Query().Get("hashes")
		f.mu.Lock()
		defer f.mu.Unlock()
		out := []Torrent{}
		for _, t := range f.torrents {
			if (cat == "" || t.Category == cat) && (hashes == "" || strings.Contains(hashes, t.Hash)) {
				out = append(out, t)
			}
		}
		json.NewEncoder(w).Encode(out)
	}))
	return f, httptest.NewServer(mux)
}

func TestLoginAddInfoStatus(t *testing.T) {
	f, srv := newFake()
	defer srv.Close()
	ctx := context.Background()
	c := New(srv.URL+"/", "admin", "secret")

	v, err := c.Version(ctx)
	if err != nil || v != "v5.0.1" {
		t.Fatalf("Version = %q %v", v, err)
	}
	magnet := "magnet:?xt=urn:btih:" + strings.ToUpper(hashHex) + "&dn=Frieren+05"
	h, err := c.Add(ctx, magnet, `D:\Anime\Frieren`, "abc")
	if err != nil || h != hashHex {
		t.Fatalf("Add = %q %v", h, err)
	}
	if len(f.added) != 1 || f.added[0]["urls"] != magnet || f.added[0]["savepath"] != `D:\Anime\Frieren` ||
		f.added[0]["category"] != "abc" || f.added[0]["autoTMM"] != "false" || !f.categories["abc"] {
		t.Fatalf("add form = %+v cats=%v", f.added, f.categories)
	}
	// second add: category already exists (409) must not fail
	if _, err := c.Add(ctx, magnet, "", "abc"); err != nil {
		t.Fatalf("second Add: %v", err)
	}
	if _, ok := f.added[1]["savepath"]; ok {
		t.Fatal("empty savepath should not be sent")
	}

	f.torrents = []Torrent{
		{Hash: hashHex, Name: "Frieren 05", Progress: 0.4, State: "downloading", Category: "abc", ContentPath: `D:\Anime\Frieren\x.mkv`},
		{Hash: "ffff", Name: "other", Category: "linux"},
	}
	ts, err := c.Info(ctx, "abc")
	if err != nil || len(ts) != 1 || ts[0].Name != "Frieren 05" {
		t.Fatalf("Info = %+v %v", ts, err)
	}
	p, st, cp, err := c.Status(ctx, strings.ToUpper(hashHex))
	if err != nil || p != 0.4 || st != "downloading" || cp != `D:\Anime\Frieren\x.mkv` {
		t.Fatalf("Status = %v %q %q %v", p, st, cp, err)
	}
	if _, _, _, err := c.Status(ctx, "0000000000000000000000000000000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown hash: %v", err)
	}
	if f.logins != 1 {
		t.Fatalf("logins = %d, want 1 (session reused)", f.logins)
	}
}

func TestReloginOnExpiredSession(t *testing.T) {
	f, srv := newFake()
	defer srv.Close()
	ctx := context.Background()
	c := New(srv.URL, "admin", "secret")
	if _, err := c.Version(ctx); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.sid = "expired-on-server"
	f.mu.Unlock()
	if _, err := c.Version(ctx); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
	if f.logins != 2 {
		t.Fatalf("logins = %d, want 2", f.logins)
	}
}

func TestBadLogin(t *testing.T) {
	_, srv := newFake()
	defer srv.Close()
	c := New(srv.URL, "admin", "wrong")
	if _, err := c.Version(context.Background()); !errors.Is(err, ErrLogin) {
		t.Fatalf("want ErrLogin, got %v", err)
	}
}

func TestHashFromMagnet(t *testing.T) {
	cases := map[string]string{
		"magnet:?xt=urn:btih:" + hashHex + "&dn=x":                  hashHex,
		"magnet:?dn=x&xt=urn:btih:" + strings.ToUpper(hashHex):      hashHex,
		"magnet:?xt=urn:btih:ZHQVOY7XELZD5GFCTXWN7LRUDOMNKMCW&tr=y": hashHex,
	}
	for in, want := range cases {
		got, err := HashFromMagnet(in)
		if err != nil || got != want {
			t.Errorf("HashFromMagnet(%q) = %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"http://x", "magnet:?dn=nohash", "magnet:?xt=urn:btih:1234"} {
		if _, err := HashFromMagnet(bad); err == nil {
			t.Errorf("HashFromMagnet(%q) should fail", bad)
		}
	}
}

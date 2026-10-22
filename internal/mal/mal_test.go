package mal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

type seen struct {
	mu      sync.Mutex
	methods []string
	paths   []string
	auth    []string
	forms   []url.Values
}

func fixture(t *testing.T, name, base string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(strings.ReplaceAll(string(b), "{{BASE}}", base))
}

func fakeMAL(t *testing.T, s *seen) (*httptest.Server, *Client) {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		s.mu.Lock()
		s.methods = append(s.methods, r.Method)
		s.paths = append(s.paths, r.URL.RequestURI())
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		s.forms = append(s.forms, r.PostForm)
		s.mu.Unlock()
		base := srv.URL + "/v2"
		w.Header().Set("Content-Type", "application/json")
		send := func(status int, name string) {
			w.WriteHeader(status)
			w.Write(fixture(t, name, base))
		}
		if r.URL.Path == "/oauth2/token" {
			f := r.PostForm
			switch {
			case f.Get("grant_type") == "authorization_code" && f.Get("code") == "goodcode" && f.Get("code_verifier") == "verifier-abc" && f.Get("client_id") == "cid":
				send(200, "token.json")
			case f.Get("grant_type") == "refresh_token" && f.Get("refresh_token") == "def50200malrefresh" && f.Get("client_id") == "cid":
				send(200, "token.json")
			default:
				send(400, "token_error.json")
			}
			return
		}
		if r.Header.Get("Authorization") != "Bearer good" {
			send(401, "error_401.json")
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/users/@me/animelist":
			if r.URL.Query().Get("offset") == "2" {
				send(200, "animelist_p2.json")
			} else {
				send(200, "animelist_p1.json")
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v2/users/@me":
			send(200, "me.json")
		case r.Method == http.MethodPatch && r.URL.Path == "/v2/anime/59978/my_list_status":
			send(200, "list_status.json")
		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"error":"not_found","message":""}`))
		}
	}))
	t.Cleanup(srv.Close)
	c := New()
	c.BaseURL = srv.URL + "/v2"
	c.TokenURL = srv.URL + "/oauth2/token"
	c.now = func() time.Time { return fixedNow }
	return srv, c
}

func TestVerifier(t *testing.T) {
	re := regexp.MustCompile(`^[A-Za-z0-9_-]{43,128}$`)
	a, b := NewVerifier(), NewVerifier()
	if !re.MatchString(a) || len(a) != 86 {
		t.Errorf("verifier %q (len %d) not RFC 7636", a, len(a))
	}
	if a == b {
		t.Error("two verifiers are equal")
	}
	if Challenge(a) != a {
		t.Error("plain challenge must equal the verifier")
	}
}

func TestAuthURL(t *testing.T) {
	got := AuthURL("cid", "http://127.0.0.1:7700/api/auth/mal/callback", "verifier-abc", "st4te")
	want := "https://myanimelist.net/v1/oauth2/authorize?client_id=cid&code_challenge=verifier-abc&code_challenge_method=plain" +
		"&redirect_uri=http%3A%2F%2F127.0.0.1%3A7700%2Fapi%2Fauth%2Fmal%2Fcallback&response_type=code&state=st4te"
	if got != want {
		t.Errorf("AuthURL\n got  %s\n want %s", got, want)
	}
}

func TestExchangeAndRefresh(t *testing.T) {
	s := &seen{}
	_, c := fakeMAL(t, s)
	ctx := context.Background()
	redirect := "http://127.0.0.1:7700/api/auth/mal/callback"
	tok, err := c.Exchange(ctx, "cid", "", redirect, "goodcode", "verifier-abc")
	if err != nil {
		t.Fatal(err)
	}
	want := Token{AccessToken: "def50200malaccess", RefreshToken: "def50200malrefresh", TokenType: "Bearer", ExpiresIn: 2678400, ExpiresAt: fixedNow.Unix() + 2678400}
	if tok != want {
		t.Errorf("token\n got  %+v\n want %+v", tok, want)
	}
	f := s.forms[0]
	if s.methods[0] != "POST" || f.Get("redirect_uri") != redirect || f.Has("client_secret") {
		t.Errorf("exchange form %v", f)
	}
	// with a secret
	if _, err := c.Exchange(ctx, "cid", "sec", redirect, "goodcode", "verifier-abc"); err != nil || s.forms[1].Get("client_secret") != "sec" {
		t.Errorf("secret form %v err %v", s.forms[1], err)
	}
	// wrong verifier -> MAL error with code
	_, err = c.Exchange(ctx, "cid", "", redirect, "goodcode", "wrong")
	if e, ok := err.(*Error); !ok || e.Status != 400 || e.Code != "invalid_grant" {
		t.Errorf("bad verifier: %v", err)
	}
	tok, err = c.Refresh(ctx, "cid", "", "def50200malrefresh")
	if err != nil || tok.AccessToken != "def50200malaccess" {
		t.Errorf("refresh %+v %v", tok, err)
	}
	if _, err := c.Refresh(ctx, "cid", "", "stale"); err == nil {
		t.Error("stale refresh token must fail")
	}
}

func TestUserListPaging(t *testing.T) {
	s := &seen{}
	_, c := fakeMAL(t, s)
	res, err := c.UserList(context.Background(), "good")
	if err != nil {
		t.Fatal(err)
	}
	want := []ListEntry{
		{52991, "Sousou no Frieren", "completed", 28, 10},
		{59978, "Sousou no Frieren 2nd Season", "watching", 3, 0},
		{21, "One Piece", "on_hold", 1088, 8},
		{5114, "Fullmetal Alchemist: Brotherhood", "plan_to_watch", 0, 0},
		{30276, "One Punch Man", "dropped", 6, 4},
	}
	if len(res) != len(want) {
		t.Fatalf("got %d entries: %+v", len(res), res)
	}
	for i := range want {
		if res[i] != want[i] {
			t.Errorf("entry %d: got %+v want %+v", i, res[i], want[i])
		}
	}
	if len(s.paths) != 2 || s.paths[0] != "/v2/users/@me/animelist?fields=list_status&limit=1000&nsfw=true" || s.auth[0] != "Bearer good" || s.auth[1] != "Bearer good" {
		t.Errorf("requests %v auth %v", s.paths, s.auth)
	}
	locals := []string{"completed", "watching", "paused", "planning", "dropped"}
	for i, e := range res {
		if LocalStatus(e.Status) != locals[i] {
			t.Errorf("LocalStatus(%q) = %q", e.Status, LocalStatus(e.Status))
		}
	}
}

func TestUserListRefusesForeignPagingHost(t *testing.T) {
	var hits int
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer evil.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[],"paging":{"next":"` + evil.URL + `/v2/users/@me/animelist?offset=1000"}}`))
	}))
	defer srv.Close()
	c := New()
	c.BaseURL = srv.URL + "/v2"
	if _, err := c.UserList(context.Background(), "good"); err == nil {
		t.Error("want error for a paging link to another host")
	}
	if hits != 0 {
		t.Error("token was sent to another host")
	}
}

func TestUnauthorized(t *testing.T) {
	_, c := fakeMAL(t, &seen{})
	_, err := c.UserList(context.Background(), "expired")
	if e, ok := err.(*Error); !ok || e.Status != 401 || e.Code != "invalid_token" || !strings.Contains(err.Error(), "expired") {
		t.Errorf("want 401 invalid_token, got %v", err)
	}
}

func TestPushProgressAndMe(t *testing.T) {
	s := &seen{}
	_, c := fakeMAL(t, s)
	ctx := context.Background()
	if err := c.PushProgress(ctx, "good", 59978, 5); err != nil {
		t.Fatal(err)
	}
	if s.methods[0] != "PATCH" || s.paths[0] != "/v2/anime/59978/my_list_status" || s.forms[0].Get("num_watched_episodes") != "5" {
		t.Errorf("push: %v %v %v", s.methods, s.paths, s.forms)
	}
	if err := c.PushProgress(ctx, "", 1, 1); err == nil {
		t.Error("empty token must fail")
	}
	if err := c.PushProgress(ctx, "good", 1, 1); err == nil {
		t.Error("unknown anime must fail")
	}
	u, err := c.Me(ctx, "good")
	if err != nil || u != (User{1234567, "ExampleUser"}) {
		t.Errorf("me %+v %v", u, err)
	}
}

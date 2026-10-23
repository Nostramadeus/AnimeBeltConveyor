package anilist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type gqlReq struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type recorder struct {
	mu     sync.Mutex
	reqs   []gqlReq
	auth   []string
	sleeps []time.Duration
}

func (r *recorder) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.reqs) }

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var fixedNow = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// fakeAniList answers like graphql.anilist.co from the testdata fixtures.
func fakeAniList(t *testing.T, rec *recorder) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			tokenHandler(t, w, r)
			return
		}
		var req gqlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || r.Method != http.MethodPost {
			http.Error(w, `{"errors":[{"message":"bad request"}]}`, 400)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, `{"errors":[{"message":"content type"}]}`, 400)
			return
		}
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, req)
		rec.auth = append(rec.auth, r.Header.Get("Authorization"))
		rec.mu.Unlock()
		q, v := req.Query, req.Variables
		name, status := "", 200
		switch {
		case strings.Contains(q, "SaveMediaListEntry"), strings.Contains(q, "Viewer"):
			if r.Header.Get("Authorization") != "Bearer tok123" {
				w.WriteHeader(400)
				w.Write([]byte(`{"errors":[{"message":"Invalid token","status":400}],"data":null}`))
				return
			}
			name = "save.json"
			if strings.Contains(q, "Viewer") {
				name = "viewer.json"
			}
		case strings.Contains(q, "MediaListCollection"):
			name = "userlist.json"
			if v["user"] != "ExampleUser" {
				name, status = "userlist_private.json", 404
			}
		case strings.Contains(q, "Page(perPage: 25)"):
			name = "search_frieren.json"
		case strings.Contains(q, "format_in"):
			name = "season_p1.json"
			if v["page"] == 2.0 {
				name = "season_p2.json"
			}
		case strings.Contains(q, "Media(id: $id, type: ANIME) { id episodes"):
			switch v["id"] {
			case 145064.0:
				name = "relations_145064.json"
			case 113415.0:
				name = "relations_113415.json"
			default:
				name, status = "notfound.json", 404
			}
		case strings.Contains(q, "Media(id: $id, idMal: $idMal"):
			switch {
			case v["id"] == 145064.0 && v["page"] == 1.0:
				name = "media_145064_p1.json"
			case v["idMal"] == 52991.0:
				name = "media_154587_real.json"
			case v["id"] == 154587.0 && v["page"] == 1.0:
				name = "media_154587_p1.json"
			case v["id"] == 154587.0 && v["page"] == 2.0:
				name = "media_154587_p2.json"
			default:
				name, status = "notfound.json", 404
			}
		default:
			t.Errorf("unexpected query %q", q)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write(fixture(t, name))
	}))
	t.Cleanup(srv.Close)
	c := New()
	c.Endpoint = srv.URL
	c.TokenURL = srv.URL + "/oauth/token"
	c.now = func() time.Time { return fixedNow }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		rec.mu.Lock()
		rec.sleeps = append(rec.sleeps, d)
		rec.mu.Unlock()
		return ctx.Err()
	}
	return srv, c
}

func tokenHandler(t *testing.T, w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	json.NewDecoder(r.Body).Decode(&body)
	w.Header().Set("Content-Type", "application/json")
	if body["grant_type"] != "authorization_code" || body["client_id"] != "1234" || body["client_secret"] != "s3cret" ||
		body["redirect_uri"] != "http://127.0.0.1:7700/api/auth/anilist/callback" || body["code"] != "goodcode" {
		w.WriteHeader(400)
		w.Write(fixture(t, "token_error.json"))
		return
	}
	w.Write(fixture(t, "token.json"))
}

func TestMediaPagesAiringSchedule(t *testing.T) {
	rec := &recorder{}
	_, c := fakeAniList(t, rec)
	m, err := c.Media(context.Background(), 154587)
	if err != nil {
		t.Fatal(err)
	}
	want := MediaSummary{
		AniListID: 154587, MalID: 52991,
		TitleRomaji: "Sousou no Frieren", TitleEnglish: "Frieren: Beyond Journey’s End", TitleNative: "葬送のフリーレン",
		CoverURL:  "https://s4.anilist.co/file/anilistcdn/media/anime/cover/large/bx154587-qQTzQnEJJ3oB.jpg",
		BannerURL: "https://s4.anilist.co/file/anilistcdn/media/anime/banner/154587-ivXNJ23SM1xB.jpg",
		Color:     "#bbf1a1", Format: "TV", Status: "FINISHED", Season: "FALL", SeasonYear: 2023, EpisodesTotal: 28,
	}
	if m.MediaSummary != want {
		t.Errorf("summary\n got  %+v\n want %+v", m.MediaSummary, want)
	}
	if m.DurationMin != 24 || len(m.Synonyms) != 15 || m.Synonyms[0] != "Frieren at the Funeral" {
		t.Errorf("duration %d synonyms %d %v", m.DurationMin, len(m.Synonyms), m.Synonyms[:1])
	}
	// the recorded schedule covers episodes 5-28 (1-4 premiered together)
	if len(m.Airing) != 24 || m.Airing[0] != (AiringNode{5, 1696600800}) || m.Airing[23].Episode != 28 {
		t.Errorf("airing: %d nodes, first %+v last %+v", len(m.Airing), m.Airing[0], m.Airing[len(m.Airing)-1])
	}
	if rec.count() != 2 || rec.reqs[0].Variables["page"] != 1.0 || rec.reqs[1].Variables["page"] != 2.0 {
		t.Errorf("want 2 paged requests, got %+v", rec.reqs)
	}
	if rec.auth[0] != "" {
		t.Error("public query sent an Authorization header")
	}
}

func TestMediaByMalID(t *testing.T) {
	rec := &recorder{}
	_, c := fakeAniList(t, rec)
	m, err := c.MediaByMalID(context.Background(), 52991)
	if err != nil {
		t.Fatal(err)
	}
	if m.AniListID != 154587 || len(m.Airing) != 24 {
		t.Errorf("got id %d with %d airing nodes", m.AniListID, len(m.Airing))
	}
	if rec.count() != 1 || rec.reqs[0].Variables["idMal"] != 52991.0 || rec.reqs[0].Variables["id"] != nil {
		t.Errorf("request vars %+v", rec.reqs[0].Variables)
	}
}

func TestMediaNotFound(t *testing.T) {
	_, c := fakeAniList(t, &recorder{})
	_, err := c.Media(context.Background(), 1)
	e, ok := err.(*Error)
	if !ok || e.Status != 404 || len(e.Messages) != 1 || e.Messages[0] != "Not Found." {
		t.Errorf("want 404 Not Found, got %v", err)
	}
}

func TestSearch(t *testing.T) {
	rec := &recorder{}
	_, c := fakeAniList(t, rec)
	res, err := c.Search(context.Background(), "frieren")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 || res[0].AniListID != 154587 || res[1].NextAiringEp != 4 || res[1].NextAiringAt != 1769792400 {
		t.Fatalf("search: %+v", res)
	}
	if res[0].AverageScore != 91 || res[1].EpisodesTotal != 0 || res[1].BannerURL != "" || res[2].TitleEnglish != "" || res[2].Color != "" || res[2].AverageScore != 0 {
		t.Errorf("nulls must map to zero values: %+v %+v", res[1], res[2])
	}
	if rec.reqs[0].Variables["q"] != "frieren" {
		t.Errorf("vars %+v", rec.reqs[0].Variables)
	}
}

func TestSeasonPages(t *testing.T) {
	rec := &recorder{}
	_, c := fakeAniList(t, rec)
	res, err := c.Season(context.Background(), "fall", 2023)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 4 || res[3].AniListID != 1003 || res[2].MalID != 0 || res[2].Format != "ONA" {
		t.Fatalf("season: %+v", res)
	}
	v := rec.reqs[0].Variables
	if rec.count() != 2 || v["season"] != "FALL" || v["year"] != 2023.0 {
		t.Errorf("requests %d vars %+v", rec.count(), v)
	}
}

func TestUserList(t *testing.T) {
	_, c := fakeAniList(t, &recorder{})
	res, err := c.UserList(context.Background(), "ExampleUser")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 4 {
		t.Fatalf("want 4 entries (duplicate removed), got %d: %+v", len(res), res)
	}
	want := []struct {
		id       int
		status   string
		local    string
		progress int
		score    float64
	}{
		{182255, "CURRENT", "watching", 3, 0},
		{1001, "REPEATING", "watching", 1, 7.5},
		{154587, "COMPLETED", "completed", 28, 9.5},
		{1003, "PLANNING", "planning", 0, 0},
	}
	for i, w := range want {
		e := res[i]
		if e.MediaID != w.id || e.Status != w.status || LocalStatus(e.Status) != w.local || e.Progress != w.progress || e.Score != w.score || e.Media.AniListID != w.id {
			t.Errorf("entry %d: %+v", i, e)
		}
	}
	if LocalStatus("PAUSED") != "paused" || LocalStatus("DROPPED") != "dropped" {
		t.Error("LocalStatus")
	}
	if _, err := c.UserList(context.Background(), "someone_private"); err == nil || !strings.Contains(err.Error(), "Private User") {
		t.Errorf("want Private User error, got %v", err)
	}
}

func TestPushProgressAndViewer(t *testing.T) {
	rec := &recorder{}
	_, c := fakeAniList(t, rec)
	ctx := context.Background()
	if err := c.PushProgress(ctx, "tok123", 182255, 5); err != nil {
		t.Fatal(err)
	}
	if rec.auth[0] != "Bearer tok123" || rec.reqs[0].Variables["id"] != 182255.0 || rec.reqs[0].Variables["progress"] != 5.0 {
		t.Errorf("push request: auth %q vars %+v", rec.auth[0], rec.reqs[0].Variables)
	}
	if !strings.HasPrefix(strings.TrimSpace(rec.reqs[0].Query), "mutation") {
		t.Error("push must be a mutation")
	}
	if err := c.PushProgress(ctx, "", 1, 1); err == nil {
		t.Error("empty token must fail")
	}
	if err := c.PushProgress(ctx, "bad", 1, 1); err == nil || !strings.Contains(err.Error(), "Invalid token") {
		t.Errorf("bad token: %v", err)
	}
	v, err := c.Viewer(ctx, "tok123")
	if err != nil || v != (Viewer{5551234, "ExampleUser"}) {
		t.Errorf("viewer %+v %v", v, err)
	}
}

func TestAuthURL(t *testing.T) {
	got := AuthURL("1234", "http://127.0.0.1:7700/api/auth/anilist/callback")
	want := "https://anilist.co/api/v2/oauth/authorize?client_id=1234&redirect_uri=http%3A%2F%2F127.0.0.1%3A7700%2Fapi%2Fauth%2Fanilist%2Fcallback&response_type=code"
	if got != want {
		t.Errorf("AuthURL\n got  %s\n want %s", got, want)
	}
}

func TestExchange(t *testing.T) {
	_, c := fakeAniList(t, &recorder{})
	ctx := context.Background()
	redirect := "http://127.0.0.1:7700/api/auth/anilist/callback"
	tok, err := c.Exchange(ctx, "1234", "s3cret", redirect, "goodcode")
	if err != nil {
		t.Fatal(err)
	}
	want := Token{AccessToken: "eyJ0eXAiOiJKV1QiLCJhbGciOiJSUzI1NiJ9.fake.access", RefreshToken: "def50200fake-refresh",
		TokenType: "Bearer", ExpiresIn: 31536000, ExpiresAt: fixedNow.Unix() + 31536000}
	if tok != want {
		t.Errorf("token\n got  %+v\n want %+v", tok, want)
	}
	_, err = c.Exchange(ctx, "1234", "s3cret", redirect, "badcode")
	if e, ok := err.(*Error); !ok || e.Status != 400 || !strings.Contains(err.Error(), "invalid_request") {
		t.Errorf("bad code: %v", err)
	}
}

func TestRetryOnce429(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	failures := 1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n <= failures {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(429)
			w.Write([]byte(`{"errors":[{"message":"Too Many Requests.","status":429}],"data":null}`))
			return
		}
		w.Write(fixture(t, "search_frieren.json"))
	}))
	defer srv.Close()
	var sleeps []time.Duration
	c := New()
	c.Endpoint = srv.URL
	c.now = func() time.Time { return fixedNow }
	c.sleep = func(ctx context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil }

	res, err := c.Search(context.Background(), "frieren")
	if err != nil || len(res) != 3 {
		t.Fatalf("after one 429: %v %d", err, len(res))
	}
	if calls != 2 || !containsDur(sleeps, 7*time.Second) {
		t.Errorf("calls %d sleeps %v", calls, sleeps)
	}

	// two 429 in a row: one retry only, then the error
	calls, failures = 0, 99
	_, err = c.Search(context.Background(), "frieren")
	if e, ok := err.(*Error); !ok || e.Status != 429 || calls != 2 {
		t.Errorf("want 429 after 2 calls, got %v after %d", err, calls)
	}
}

func containsDur(ds []time.Duration, d time.Duration) bool {
	for _, x := range ds {
		if x == d {
			return true
		}
	}
	return false
}

func TestTokenBucket(t *testing.T) {
	c := New()
	var sleeps []time.Duration
	c.now = func() time.Time { return fixedNow } // clock frozen: tokens accumulate debt
	c.sleep = func(ctx context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil }
	for i := 0; i < 3; i++ {
		c.take(context.Background())
	}
	want := []time.Duration{0, time.Second, 2 * time.Second}
	for i := range want {
		if sleeps[i] != want[i] {
			t.Fatalf("sleeps %v, want %v", sleeps, want)
		}
	}
	// after the bucket refills, the next call is immediate
	c.now = func() time.Time { return fixedNow.Add(10 * time.Second) }
	c.take(context.Background())
	if sleeps[3] != 0 {
		t.Errorf("refilled bucket waited %v", sleeps[3])
	}
}

func TestRetryAfter(t *testing.T) {
	c := New()
	c.now = func() time.Time { return fixedNow }
	h := http.Header{}
	if d := c.retryAfter(h); d != 2*time.Second {
		t.Errorf("default %v", d)
	}
	h.Set("Retry-After", fixedNow.Add(30*time.Second).Format(http.TimeFormat))
	if d := c.retryAfter(h); d != 30*time.Second {
		t.Errorf("http date %v", d)
	}
	h.Set("Retry-After", "999")
	if d := c.retryAfter(h); d != c.MaxRetryWait {
		t.Errorf("cap %v", d)
	}
}

func TestJSONShape(t *testing.T) {
	b, _ := json.Marshal(Media{MediaSummary: MediaSummary{AniListID: 1, TitleRomaji: "x"}, Airing: []AiringNode{{1, 2}}})
	s := string(b)
	for _, k := range []string{`"anilist_id":1`, `"title_romaji":"x"`, `"episodes_total"`, `"next_airing_at"`, `"duration_min"`, `"airing":[{"episode":1,"airingAt":2}]`} {
		if !strings.Contains(s, k) {
			t.Errorf("JSON %s lacks %s", s, k)
		}
	}
}

func TestQueriesMentionSpecFields(t *testing.T) {
	for _, f := range []string{"idMal", "averageScore", "romaji", "english", "native", "synonyms", "extraLarge", "color", "bannerImage", "format", "status", "season", "seasonYear", "episodes", "duration", "nextAiringEpisode", "airingSchedule(page: $page, perPage: 50)"} {
		if !strings.Contains(QueryMedia, f) {
			t.Errorf("QueryMedia lacks %s", f)
		}
	}
	if !strings.Contains(QuerySeason, "format_in: [TV, TV_SHORT, ONA], sort: POPULARITY_DESC") {
		t.Error("QuerySeason filter")
	}
}

func TestQueriesAskForRelations(t *testing.T) {
	want := "relations { edges { relationType node { id type format episodes seasonYear title { romaji english } startDate { year month day } } } }"
	if !strings.Contains(QueryMedia, want) || !strings.Contains(QueryRelations, want) {
		t.Error("QueryMedia / QueryRelations lack the relations block")
	}
	if strings.Contains(QueryRelations, "airingSchedule") || !strings.Contains(QueryRelations, "id episodes") {
		t.Error("QueryRelations must be the small query (id episodes relations, no airing schedule)")
	}
}

// SPEC v1.3 section 1.2: prequel selection.
func TestPickPrequel(t *testing.T) {
	edge := func(rel string, id int, typ, format string, eps, y, m, d int) rawEdge {
		var e rawEdge
		e.RelationType = rel
		e.Node.ID, e.Node.Type, e.Node.Format, e.Node.Episodes = id, typ, format, eps
		e.Node.StartDate.Year, e.Node.StartDate.Month, e.Node.StartDate.Day = y, m, d
		return e
	}
	cases := []struct {
		name  string
		edges []rawEdge
		want  int // id, 0 = nil
	}{
		{"none", nil, 0},
		{"only sequel and source", []rawEdge{edge("SEQUEL", 1, "ANIME", "TV", 12, 2024, 1, 1), edge("SOURCE", 2, "MANGA", "MANGA", 0, 2010, 1, 1)}, 0},
		{"movie, OVA, special and manga never count", []rawEdge{
			edge("PREQUEL", 3, "ANIME", "MOVIE", 1, 2022, 1, 1), edge("PREQUEL", 4, "ANIME", "OVA", 2, 2022, 1, 1),
			edge("PREQUEL", 5, "ANIME", "SPECIAL", 1, 2022, 1, 1), edge("PREQUEL", 6, "MANGA", "MANGA", 0, 2022, 1, 1)}, 0},
		{"TV beats the movie", []rawEdge{edge("PREQUEL", 131573, "ANIME", "MOVIE", 1, 2021, 12, 24), edge("PREQUEL", 113415, "ANIME", "TV", 24, 2020, 10, 3)}, 113415},
		{"TV_SHORT and ONA count", []rawEdge{edge("PREQUEL", 7, "ANIME", "TV_SHORT", 12, 2019, 1, 1)}, 7},
		{"greatest start date wins over a greater id", []rawEdge{edge("PREQUEL", 90, "ANIME", "TV", 12, 2019, 4, 1), edge("PREQUEL", 10, "ANIME", "ONA", 12, 2021, 1, 9)}, 10},
		{"same date: greatest id", []rawEdge{edge("PREQUEL", 11, "ANIME", "TV", 12, 2020, 1, 1), edge("PREQUEL", 12, "ANIME", "TV", 12, 2020, 1, 1)}, 12},
		{"an unknown date: greatest id", []rawEdge{edge("PREQUEL", 30, "ANIME", "TV", 12, 2024, 1, 1), edge("PREQUEL", 31, "ANIME", "TV", 12, 0, 0, 0), edge("PREQUEL", 20, "ANIME", "TV", 12, 2025, 1, 1)}, 31},
	}
	for _, c := range cases {
		got := pickPrequel(c.edges)
		gotID := 0
		if got != nil {
			gotID = got.ID
		}
		if gotID != c.want {
			t.Errorf("%s: picked %d, want %d", c.name, gotID, c.want)
		}
	}
	e := edge("PREQUEL", 113415, "ANIME", "TV", 24, 2020, 10, 3)
	e.Node.Title.Romaji, e.Node.Title.English = "Jujutsu Kaisen", "JUJUTSU KAISEN"
	p := pickPrequel([]rawEdge{e})
	if *p != (RelNode{ID: 113415, Format: "TV", Episodes: 24, StartDate: 20201003, TitleRomaji: "Jujutsu Kaisen", TitleEnglish: "JUJUTSU KAISEN"}) {
		t.Errorf("RelNode = %+v", *p)
	}
}

func TestMediaReadsPrequel(t *testing.T) {
	rec := &recorder{}
	_, c := fakeAniList(t, rec)
	m, err := c.Media(context.Background(), 145064)
	if err != nil {
		t.Fatal(err)
	}
	if m.Prequel == nil || m.Prequel.ID != 113415 || m.Prequel.Episodes != 24 || m.Prequel.Format != "TV" || m.Prequel.StartDate != 20201003 ||
		m.Prequel.TitleRomaji != "Jujutsu Kaisen" || m.Prequel.TitleEnglish != "JUJUTSU KAISEN" {
		t.Fatalf("prequel = %+v", m.Prequel)
	}
	if rec.count() != 1 {
		t.Errorf("requests = %d, want 1", rec.count())
	}
	// Frieren's fixture has no relations block: no prequel.
	f, err := c.Media(context.Background(), 154587)
	if err != nil || f.Prequel != nil {
		t.Fatalf("frieren prequel = %+v %v", f.Prequel, err)
	}
}

func TestPrequelHop(t *testing.T) {
	rec := &recorder{}
	_, c := fakeAniList(t, rec)
	ctx := context.Background()
	eps, p, err := c.Prequel(ctx, 145064)
	if err != nil || eps != 23 || p == nil || p.ID != 113415 || p.Episodes != 24 || p.TitleRomaji != "Jujutsu Kaisen" {
		t.Fatalf("Prequel(145064) = %d %+v %v", eps, p, err)
	}
	eps, p, err = c.Prequel(ctx, 113415)
	if err != nil || eps != 24 || p != nil {
		t.Fatalf("Prequel(113415) = %d %+v %v, want 24 nil", eps, p, err)
	}
	if rec.count() != 2 || rec.reqs[0].Variables["id"] != 145064.0 {
		t.Errorf("requests %+v", rec.reqs)
	}
	if _, _, err := c.Prequel(ctx, 1); err == nil {
		t.Error("unknown id must fail")
	}
}

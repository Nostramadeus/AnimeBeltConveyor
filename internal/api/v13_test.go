package api

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// SPEC v1.3 section 2.2: which failed release still shows as the current one.
func TestCurrentReleaseFailedExpiry(t *testing.T) {
	const now = int64(10_000_000)
	day := int64(24 * 60 * 60)
	rel := func(id int64, ep int, status string, statusAt, firstSeen int64) store.Release {
		return store.Release{ID: id, Episode: store.Ptr(ep), Status: status, StatusAt: statusAt, FirstSeen: firstSeen}
	}
	cases := []struct {
		name string
		rels []store.Release
		want int64 // 0 = none
	}{
		{"fresh failed shows", []store.Release{rel(1, 3, store.RelFailed, now-day, now-2*day)}, 1},
		{"failed older than 7 days hidden", []store.Release{rel(1, 3, store.RelFailed, now-8*day, now-9*day)}, 0},
		{"exactly 7 days still shows", []store.Release{rel(1, 3, store.RelFailed, now-7*day, now-9*day)}, 1},
		{"old row without status_at uses first_seen", []store.Release{rel(1, 3, store.RelFailed, 0, now-8*day)}, 0},
		{"old row without status_at, recent first_seen", []store.Release{rel(1, 3, store.RelFailed, 0, now-day)}, 1},
		{"failed with a newer candidate hidden", []store.Release{rel(1, 3, store.RelFailed, now, now), rel(2, 3, store.RelCandidate, now, now)}, 0},
		{"failed with a newer skipped row hidden", []store.Release{rel(1, 3, store.RelFailed, now, now), rel(2, 3, store.RelSkipped, now, now)}, 0},
		{"failed with a newer replaced row still shown", []store.Release{rel(1, 3, store.RelFailed, now, now), rel(2, 3, store.RelReplaced, now, now)}, 1},
		{"older candidate does not hide it", []store.Release{rel(1, 3, store.RelCandidate, now, now), rel(2, 3, store.RelFailed, now, now)}, 2},
		{"newer release of another episode does not hide it", []store.Release{rel(1, 3, store.RelFailed, now, now), rel(2, 4, store.RelCandidate, now, now)}, 1},
		{"newer covering batch hides it", []store.Release{rel(1, 3, store.RelFailed, now, now),
			{ID: 2, Episode: store.Ptr(1), EpisodeEnd: store.Ptr(12), IsBatch: true, Status: store.RelCandidate}}, 0},
		{"done still wins over failed", []store.Release{rel(1, 3, store.RelDone, now, now), rel(2, 3, store.RelFailed, now, now)}, 1},
		{"a newer failed hides the older failed, shows itself", []store.Release{rel(1, 3, store.RelFailed, now, now), rel(2, 3, store.RelFailed, now, now)}, 2},
	}
	for _, c := range cases {
		got := currentRelease(c.rels, 3, now)
		var id int64
		if got != nil {
			id = got.ID
		}
		if id != c.want {
			t.Errorf("%s: current = %d, want %d", c.name, id, c.want)
		}
	}
}

func TestAbsNumbersAndClearFailed(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	sh := store.Show{TitleRomaji: "Jujutsu Kaisen 2nd Season", Status: "RELEASING", AbsOffset: store.Ptr(24), EpisodesTotal: store.Ptr(23)}
	if err := e.db.CreateShow(ctx, &sh); err != nil {
		t.Fatal(err)
	}
	e.db.EnsureEpisodes(ctx, sh.ID, 3)
	plain := store.Show{TitleRomaji: "Plain", Status: "RELEASING", AbsOffset: store.Ptr(0)}
	e.db.CreateShow(ctx, &plain)
	e.db.EnsureEpisodes(ctx, plain.ID, 1)
	path := "/api/shows/" + strconv.FormatInt(sh.ID, 10)

	type detail struct {
		Show struct {
			AbsOffset     *int `json:"abs_offset"`
			EpisodeOffset int  `json:"episode_offset"`
		} `json:"show"`
		Episodes []struct {
			Number    int  `json:"number"`
			AbsNumber *int `json:"abs_number"`
			Release   *struct {
				ID     int64  `json:"id"`
				Status string `json:"status"`
			} `json:"release"`
		} `json:"episodes"`
	}
	get := func(p string) detail {
		t.Helper()
		code, b := e.do(t, "GET", p, nil)
		if code != 200 {
			t.Fatalf("GET %s -> %d %s", p, code, b)
		}
		return decodeT[detail](t, b)
	}
	d := get(path)
	if d.Show.AbsOffset == nil || *d.Show.AbsOffset != 24 || len(d.Episodes) != 3 || d.Episodes[2].AbsNumber == nil || *d.Episodes[2].AbsNumber != 27 {
		t.Fatalf("detail = %+v", d)
	}
	if p := get("/api/shows/" + strconv.FormatInt(plain.ID, 10)); p.Episodes[0].AbsNumber != nil {
		t.Fatal("abs_number must be null when the offset is 0")
	}
	// follow-up A: a stored -1 (chain unknown) is shown as null and gives no absolute numbers
	unk := store.Show{TitleRomaji: "Unknown chain", Status: "FINISHED", AbsOffset: store.Ptr(-1)}
	e.db.CreateShow(ctx, &unk)
	e.db.EnsureEpisodes(ctx, unk.ID, 1)
	if u := get("/api/shows/" + strconv.FormatInt(unk.ID, 10)); u.Show.AbsOffset != nil || u.Episodes[0].AbsNumber != nil {
		t.Fatalf("abs_offset -1 must be null in the API: %+v", u)
	}
	// manual offset wins and round-trips; 0 = back to the AniList value
	if code, b := e.do(t, "PATCH", path, map[string]any{"episode_offset": 170}); code != 200 {
		t.Fatalf("PATCH -> %d %s", code, b)
	}
	if d := get(path); d.Show.EpisodeOffset != 170 || *d.Show.AbsOffset != 24 || *d.Episodes[0].AbsNumber != 171 {
		t.Fatalf("after PATCH 170: %+v", d)
	}
	e.do(t, "PATCH", path, map[string]any{"episode_offset": 0})
	if d := get(path); d.Show.EpisodeOffset != 0 || *d.Episodes[0].AbsNumber != 25 {
		t.Fatalf("after PATCH 0: %+v", d)
	}
	// calendar items carry abs_number too
	at := e.srv.now().Unix() - 60
	e.db.UpsertEpisodeAiring(ctx, sh.ID, 2, &at)
	code, b := e.do(t, "GET", "/api/calendar", nil)
	cal := decodeT[[]struct {
		ShowID    int64 `json:"show_id"`
		Episode   int   `json:"episode"`
		AbsNumber *int  `json:"abs_number"`
	}](t, b)
	if code != 200 || len(cal) != 1 || cal[0].AbsNumber == nil || *cal[0].AbsNumber != 26 {
		t.Fatalf("calendar = %d %s", code, b)
	}

	// two failed releases (ep 1 and ep 2) and a done one (ep 3)
	mk := func(nyaa int64, ep int, status string) int64 {
		r := store.Release{ShowID: sh.ID, Episode: store.Ptr(ep), NyaaID: nyaa, Title: "r", PublishedAt: 1, Status: status}
		if _, err := e.db.UpsertRelease(ctx, &r); err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	f1, f2 := mk(1, 1, store.RelFailed), mk(2, 2, store.RelFailed)
	mk(3, 3, store.RelDone)
	if d := get(path); d.Episodes[0].Release == nil || d.Episodes[0].Release.Status != "failed" || d.Episodes[1].Release == nil {
		t.Fatalf("fresh failed must show: %+v", d.Episodes)
	}
	clear := path + "/releases/clear-failed"
	type cleared struct {
		Cleared int `json:"cleared"`
	}
	// one episode
	code, b = e.do(t, "POST", clear, map[string]any{"episode": 2})
	if code != 200 || decodeT[cleared](t, b).Cleared != 1 {
		t.Fatalf("clear ep 2 -> %d %s", code, b)
	}
	if d := get(path); d.Episodes[1].Release != nil || d.Episodes[0].Release == nil {
		t.Fatalf("after clear ep 2: %+v", d.Episodes)
	}
	if r, _ := e.db.GetRelease(ctx, f2); r.Status != store.RelSkipped || r.StatusNote != "cleared by user" {
		t.Fatalf("cleared release = %s %q", r.Status, r.StatusNote)
	}
	// the whole show, no body
	code, b = e.do(t, "POST", clear, nil)
	if code != 200 || decodeT[cleared](t, b).Cleared != 1 {
		t.Fatalf("clear all -> %d %s", code, b)
	}
	if r, _ := e.db.GetRelease(ctx, f1); r.Status != store.RelSkipped {
		t.Fatalf("ep 1 release = %s", r.Status)
	}
	if code, b = e.do(t, "POST", clear, nil); code != 200 || decodeT[cleared](t, b).Cleared != 0 {
		t.Fatalf("clear again -> %d %s", code, b)
	}
	// errors
	if code, _ := e.do(t, "POST", "/api/shows/999/releases/clear-failed", nil); code != 404 {
		t.Fatalf("unknown show -> %d", code)
	}
	for _, body := range []any{map[string]any{"episode": "two"}, map[string]any{"episode": 0}, map[string]any{"episode": 1.5}, []int{1}} {
		if code, b := e.do(t, "POST", clear, body); code != 400 {
			t.Fatalf("bad body %v -> %d %s", body, code, b)
		}
	}
	if code, _ := e.do(t, "POST", clear, map[string]any{"episode": nil}); code != 200 {
		t.Fatalf("episode null -> %d", code)
	}
	if code, _ := e.do(t, "POST", clear, nil, "Origin", "https://evil.example"); code != 403 {
		t.Fatalf("cross-origin clear -> %d", code)
	}
	if code, _ := e.do(t, "POST", clear, nil, "Sec-Fetch-Site", "cross-site"); code != 403 {
		t.Fatalf("cross-site clear -> %d", code)
	}
}

// Follow-up B: POST /api/quit answers {ok:true} and fires Quit(); cross-site
// and cross-origin posts are refused and do not fire it; GET is not a route.
func TestQuit(t *testing.T) {
	e := newTestEnv(t)
	fired := func() bool {
		select {
		case <-e.srv.Quit():
			return true
		default:
			return false
		}
	}
	if code, _ := e.do(t, "POST", "/api/quit", nil, "Origin", "https://evil.example"); code != 403 || fired() {
		t.Fatalf("cross-origin quit -> %d, fired %v", code, fired())
	}
	if code, _ := e.do(t, "POST", "/api/quit", nil, "Sec-Fetch-Site", "cross-site"); code != 403 || fired() {
		t.Fatalf("cross-site quit -> %d, fired %v", code, fired())
	}
	if code, _ := e.do(t, "POST", "/api/quit", nil, "Host", "evil.example:7700"); code != 403 || fired() {
		t.Fatalf("foreign host quit -> %d, fired %v", code, fired())
	}
	if code, _ := e.do(t, "GET", "/api/quit", nil); code == 200 || fired() {
		t.Fatalf("GET quit -> %d, fired %v", code, fired())
	}
	code, b := e.do(t, "POST", "/api/quit", nil, "Origin", "http://127.0.0.1:7700", "Sec-Fetch-Site", "same-origin")
	if code != 200 || strings.TrimSpace(string(b)) != `{"ok":true}` {
		t.Fatalf("quit -> %d %s", code, b)
	}
	select {
	case <-e.srv.Quit():
	case <-time.After(2 * time.Second):
		t.Fatal("quit signal not fired")
	}
	// a second quit (double click) is harmless
	if code, _ := e.do(t, "POST", "/api/quit", nil); code != 200 {
		t.Fatalf("second quit -> %d", code)
	}
}

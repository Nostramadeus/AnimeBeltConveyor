package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/events"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/sched"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

type fakeBrain struct {
	sched.OfflineBrain
	media map[int]sched.Media
}

func (f *fakeBrain) Media(_ context.Context, id int) (sched.Media, error) {
	m, ok := f.media[id]
	if !ok {
		return sched.Media{}, errors.New("not found on AniList")
	}
	return m, nil
}

func (f *fakeBrain) NyaaSearch(context.Context, string, string, string) ([]sched.NyaaItem, error) {
	return nil, nil
}

func (f *fakeBrain) Search(_ context.Context, q string) ([]sched.MediaSummary, error) {
	var out []sched.MediaSummary
	for _, m := range f.media {
		if strings.Contains(strings.ToLower(m.TitleRomaji), strings.ToLower(q)) {
			out = append(out, m.MediaSummary)
		}
	}
	return out, nil
}

// nopLauncher records what would have been opened; nothing ever starts.
type nopLauncher struct {
	opened []string
	dirs   []string
	urls   []string
}

func (n *nopLauncher) Open(_, file string) (<-chan struct{}, error) {
	n.opened = append(n.opened, file)
	return nil, nil
}
func (n *nopLauncher) Reveal(string) error      { return nil }
func (n *nopLauncher) OpenDir(dir string) error { n.dirs = append(n.dirs, dir); return nil }
func (n *nopLauncher) OpenURL(u string) error   { n.urls = append(n.urls, u); return nil }

type testEnv struct {
	h    http.Handler
	db   *store.DB
	set  *settings.Manager
	ev   *events.Logger
	srv  *Server
	sch  *sched.Sched
	dir  string // the test's temp dir (data dir)
	root string // the download folder
	l    *nopLauncher
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "abc.db"))
	if err != nil {
		t.Fatal(err)
	}
	set, _ := settings.NewManager(db)
	root := filepath.Join(dir, "lib")
	os.MkdirAll(root, 0o755)
	c := set.Get()
	c.Library.Folders = []settings.Folder{{Path: root, Active: true, Download: true}}
	if _, err := set.Update(c); err != nil {
		t.Fatal(err)
	}
	ev := events.New(db)
	now := time.Now().Unix()
	fb := &fakeBrain{media: map[int]sched.Media{
		154587: {
			MediaSummary: sched.MediaSummary{ID: 154587, TitleRomaji: "Sousou no Frieren", TitleEnglish: "Frieren", Status: "RELEASING", Episodes: 4, Duration: 24},
			Airing:       []sched.AiringNode{{Episode: 1, AiringAt: now - 3600}, {Episode: 2, AiringAt: now + 86400}},
		},
	}}
	sch := sched.New(db, set, ev, fb)
	l := &nopLauncher{}
	sch.SetLauncher(l)
	web := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>ABC</title>")}, "embed.go": {Data: []byte("package web")}}
	srv := New(db, set, ev, sch, web)
	srv.ping = 50 * time.Millisecond
	srv.SetDirs(dir, filepath.Join(dir, "app"))
	sch.SetMagnetHandler(func() (string, error) { return `"C:\Apps\qbittorrent.exe" "%1"`, nil })
	t.Cleanup(func() { sch.Stop(); db.Close() })
	return &testEnv{h: srv.Handler(), db: db, set: set, ev: ev, srv: srv, sch: sch, dir: dir, root: root, l: l}
}

func (e *testEnv) do(t *testing.T, method, path string, body any, hdr ...string) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Host = "127.0.0.1:7700"
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i] == "Host" {
			req.Host = hdr[i+1]
		} else {
			req.Header.Set(hdr[i], hdr[i+1])
		}
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func decodeT[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("bad JSON %s: %v", b, err)
	}
	return v
}

func TestStateAndSettings(t *testing.T) {
	e := newTestEnv(t)
	code, b := e.do(t, "GET", "/api/state", nil)
	if code != 200 {
		t.Fatalf("state %d %s", code, b)
	}
	st := decodeT[map[string]any](t, b)
	for _, k := range []string{"settings_ok", "qbit", "root_ok", "counts", "now", "downloader", "download_dir", "folders_active", "exe_dir", "data_dir", "setup_done"} {
		if _, ok := st[k]; !ok {
			t.Errorf("state missing %s: %s", k, b)
		}
	}
	if st["root_ok"] != true || st["settings_ok"] != true {
		t.Fatalf("state = %s", b)
	}

	code, b = e.do(t, "GET", "/api/settings", nil)
	got := decodeT[settings.Settings](t, b)
	if code != 200 || got.Server.Port != 7700 || got.Library.DownloadDir() != e.root {
		t.Fatalf("settings %d %s", code, b)
	}

	code, b = e.do(t, "PUT", "/api/settings", map[string]any{"server": map[string]any{"port": 80}, "watched": map[string]any{"fraction": 3}})
	if code != 400 {
		t.Fatalf("invalid PUT -> %d %s", code, b)
	}
	ve := decodeT[struct {
		Error  string            `json:"error"`
		Fields map[string]string `json:"fields"`
	}](t, b)
	if ve.Fields["server.port"] == "" || ve.Fields["watched.fraction"] == "" || !strings.Contains(ve.Error, "server.port") {
		t.Fatalf("validation reply = %s", b)
	}

	code, b = e.do(t, "PUT", "/api/settings", map[string]any{"conveyor": map[string]any{"poll_interval_min": 30}})
	saved := decodeT[settings.Settings](t, b)
	if code != 200 || saved.Conveyor.PollIntervalMin != 30 || saved.Library.DownloadDir() != e.root || saved.Conveyor.NyaaBase != "https://nyaa.si" {
		t.Fatalf("partial PUT -> %d %s", code, b)
	}
	if e.set.Get().Conveyor.PollIntervalMin != 30 {
		t.Fatal("PUT not applied")
	}
	// point at a closed port: the developer may have a real qBittorrent on 8080
	qc := e.set.Get()
	qc.Qbit.URL = "http://127.0.0.1:1"
	if _, err := e.set.Update(qc); err != nil {
		t.Fatal(err)
	}
	code, b = e.do(t, "GET", "/api/settings/test/qbit", nil)
	if r := decodeT[map[string]any](t, b); code != 200 || r["ok"] != false || r["error"] == "" {
		t.Fatalf("test qbit (nothing listening) -> %d %s", code, b)
	}
}

func TestShowsFlow(t *testing.T) {
	e := newTestEnv(t)
	code, b := e.do(t, "POST", "/api/shows", map[string]any{"anilist_id": 154587})
	if code != 200 {
		t.Fatalf("add -> %d %s", code, b)
	}
	card := decodeT[ShowFull](t, b)
	if card.ID == 0 || card.Title != "Sousou no Frieren" || card.Progress.Aired != 1 || card.Next == nil || card.Next.Episode != 2 || card.ListStatus != "watching" {
		t.Fatalf("card = %s", b)
	}
	if code, _ := e.do(t, "POST", "/api/shows", map[string]any{"anilist_id": 154587}); code != 409 {
		t.Fatalf("duplicate -> %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/shows", map[string]any{"anilist_id": 1}); code != 502 {
		t.Fatalf("unknown media -> %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/shows", map[string]any{"anilist_id": 154587, "list_status": "bogus"}); code != 400 {
		t.Fatalf("bad list_status -> %d", code)
	}
	id := itoa(card.ID)

	// The show add queued a rescan; let it finish before planting the file by hand.
	e.srv.sch.WaitIdle()
	// A file for episode 1 shows up in up next.
	path := filepath.Join(e.root, "Sousou no Frieren", "[SubsPlease] Sousou no Frieren - 01 (1080p).mkv")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("x"), 0o644)
	sid := card.ID
	fs := []store.File{{Path: path, Size: 1, ShowID: &sid, Episode: store.Ptr(1), ReleaseGroup: "SubsPlease", Resolution: "1080p"}}
	e.db.UpsertFiles(context.Background(), fs)

	code, b = e.do(t, "GET", "/api/shows", nil)
	cards := decodeT[[]ShowCard](t, b)
	if code != 200 || len(cards) != 1 || cards[0].Progress.OnDisk != 1 {
		t.Fatalf("list -> %d %s", code, b)
	}
	if code, b := e.do(t, "GET", "/api/shows?status=planning", nil); code != 200 || string(bytes.TrimSpace(b)) != "[]" {
		t.Fatalf("filtered list -> %d %s", code, b)
	}

	code, b = e.do(t, "GET", "/api/shows/"+id, nil)
	detail := decodeT[struct {
		Show     ShowFull        `json:"show"`
		Episodes []EpisodeRow    `json:"episodes"`
		Releases []store.Release `json:"releases"`
		Files    []FileOut       `json:"files"`
	}](t, b)
	if code != 200 || len(detail.Episodes) != 4 || detail.Episodes[0].File == nil || detail.Episodes[0].File.Name != filepath.Base(path) ||
		len(detail.Files) != 1 || detail.Files[0].Name == "" || detail.Show.NyaaQuery != "Sousou no Frieren" {
		t.Fatalf("detail -> %d %s", code, b)
	}

	code, b = e.do(t, "GET", "/api/upnext", nil)
	up := decodeT[[]EpisodeRow](t, b)
	if code != 200 || len(up) != 1 || up[0].Number != 1 || up[0].File.ID != fs[0].ID {
		t.Fatalf("upnext -> %d %s", code, b)
	}

	code, b = e.do(t, "PATCH", "/api/shows/"+id, map[string]any{"group_order": []string{"Erai-raws", " "}, "episode_offset": 12, "resolution": nil, "auto_download": false})
	p := decodeT[ShowFull](t, b)
	if code != 200 || len(p.GroupOrder) != 1 || p.EpisodeOffset != 12 || p.AutoDownload == nil || *p.AutoDownload {
		t.Fatalf("patch -> %d %s", code, b)
	}
	if code, _ := e.do(t, "PATCH", "/api/shows/"+id, map[string]any{"title_romaji": "x"}); code != 400 {
		t.Fatalf("patch read-only field -> %d", code)
	}
	if code, _ := e.do(t, "PATCH", "/api/shows/"+id, map[string]any{"list_status": "nope"}); code != 400 {
		t.Fatalf("patch bad status -> %d", code)
	}
	// The offset PATCH queued a re-match scan that makes the planted file
	// unmatched (ep 1 - 12). Put the offset back and wait for the scans, so the
	// checks below do not race them.
	e.do(t, "PATCH", "/api/shows/"+id, map[string]any{"episode_offset": 0})
	e.srv.sch.WaitIdle()

	code, b = e.do(t, "POST", "/api/shows/"+id+"/episodes/1/watched", map[string]any{"watched": true})
	row := decodeT[EpisodeRow](t, b)
	if code != 200 || row.WatchedAt == nil || row.WatchedSource != "manual" {
		t.Fatalf("watched -> %d %s", code, b)
	}
	if code, b := e.do(t, "GET", "/api/upnext", nil); code != 200 || string(bytes.TrimSpace(b)) != "[]" {
		t.Fatalf("upnext after watch -> %s", b)
	}
	if code, _ := e.do(t, "POST", "/api/shows/"+id+"/episodes/1/watched", map[string]any{}); code != 400 {
		t.Fatalf("watched without flag -> %d", code)
	}

	from, to := time.Now().Unix()-86400, time.Now().Unix()+3*86400
	code, b = e.do(t, "GET", "/api/calendar?from="+itoa(from)+"&to="+itoa(to), nil)
	cal := decodeT[[]CalendarItem](t, b)
	if code != 200 || len(cal) != 2 || !cal[0].OnDisk || !cal[0].Watched || cal[1].OnDisk {
		t.Fatalf("calendar -> %d %s", code, b)
	}

	code, b = e.do(t, "POST", "/api/files/"+itoa(fs[0].ID)+"/open", nil)
	if code != 200 || len(e.l.opened) != 1 {
		t.Fatalf("open -> %d %s", code, b)
	}
	if code, _ := e.do(t, "POST", "/api/files/999/open", nil); code != 404 {
		t.Fatalf("open unknown -> %d", code)
	}

	code, b = e.do(t, "GET", "/api/events?after=0&limit=50", nil)
	evs := decodeT[[]store.Event](t, b)
	kinds := map[string]bool{}
	for _, ev := range evs {
		kinds[ev.Kind] = true
	}
	if code != 200 || !kinds["show.added"] || !kinds["episode.watched"] {
		t.Fatalf("events -> %d %s", code, b)
	}

	code, b = e.do(t, "GET", "/api/search?q=frieren", nil)
	media := decodeT[[]map[string]any](t, b)
	if code != 200 || len(media) != 1 || media[0]["tracked"] != true || media[0]["anilist_id"].(float64) != 154587 {
		t.Fatalf("search -> %d %s", code, b)
	}

	code, b = e.do(t, "DELETE", "/api/shows/"+id+"?delete_files=0", nil)
	if code != 200 {
		t.Fatalf("delete -> %d %s", code, b)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("delete_files=0 removed the file")
	}
	if code, _ := e.do(t, "GET", "/api/shows/"+id, nil); code != 404 {
		t.Fatalf("deleted show -> %d", code)
	}
}

func TestLocalOnlyGuard(t *testing.T) {
	e := newTestEnv(t)
	if code, _ := e.do(t, "GET", "/api/state", nil, "Host", "evil.example:7700"); code != 403 {
		t.Fatalf("foreign Host -> %d", code)
	}
	if code, _ := e.do(t, "GET", "/api/state", nil, "Host", "localhost:7700"); code != 200 {
		t.Fatalf("localhost -> %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/scan/run", nil, "Origin", "https://evil.example"); code != 403 {
		t.Fatalf("cross-origin POST -> %d", code)
	}
	if code, _ := e.do(t, "PUT", "/api/settings", map[string]any{}, "Sec-Fetch-Site", "cross-site"); code != 403 {
		t.Fatalf("cross-site PUT -> %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/scan/run", nil, "Origin", "http://127.0.0.1:7700", "Sec-Fetch-Site", "same-origin"); code != 200 {
		t.Fatalf("same-origin POST -> %d", code)
	}
}

func TestStaticAndUnknownRoutes(t *testing.T) {
	e := newTestEnv(t)
	code, b := e.do(t, "GET", "/", nil)
	if code != 200 || !strings.Contains(string(b), "<title>ABC</title>") {
		t.Fatalf("index -> %d %s", code, b)
	}
	if code, _ := e.do(t, "GET", "/embed.go", nil); code != 404 {
		t.Fatalf("embed.go served: %d", code)
	}
	code, b = e.do(t, "GET", "/api/nope", nil)
	if code != 404 || !strings.Contains(string(b), `"error"`) {
		t.Fatalf("unknown api -> %d %s", code, b)
	}
	if code, _ := e.do(t, "DELETE", "/api/accounts/anilist", nil); code != 404 {
		t.Fatalf("delete missing account -> %d", code)
	}
	if code, b := e.do(t, "POST", "/api/import/mal", nil); code != 401 {
		t.Fatalf("import mal without account -> %d %s", code, b)
	}
	if code, b := e.do(t, "GET", "/api/auth/anilist/start", nil); code != 400 {
		t.Fatalf("anilist start without client id -> %d %s", code, b)
	}
	c := e.set.Get()
	c.Push.MALClientID = "cid"
	e.set.Update(c)
	code, b = e.do(t, "GET", "/api/auth/mal/start", nil)
	u := decodeT[map[string]string](t, b)["url"]
	if code != 200 || !strings.Contains(u, "client_id=cid") || !strings.Contains(u, "code_challenge_method=plain") ||
		!strings.Contains(u, "redirect_uri=http%3A%2F%2F127.0.0.1%3A7700%2Fapi%2Fauth%2Fmal%2Fcallback") {
		t.Fatalf("mal start -> %d %s", code, b)
	}
	if code, _ := e.do(t, "GET", "/api/auth/mal/callback?code=x&state=forged", nil); code != 400 {
		t.Fatalf("forged state -> %d", code)
	}
}

func TestSSEStream(t *testing.T) {
	e := newTestEnv(t)
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/events/stream", nil)
	req.Host = "127.0.0.1:7700"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	go func() {
		for e.ev.Subscribers() == 0 {
			time.Sleep(10 * time.Millisecond)
		}
		e.ev.Log(events.ScanDone, 0, 0, "hello", nil)
	}()
	sc := bufio.NewScanner(resp.Body)
	var sawEvent, sawData, sawPing bool
	for sc.Scan() && !(sawEvent && sawData && sawPing) {
		line := sc.Text()
		switch {
		case line == "event: scan.done":
			sawEvent = true
		case sawEvent && strings.HasPrefix(line, "data: ") && strings.Contains(line, `"message":"hello"`):
			sawData = true
		case line == "event: ping":
			sawPing = true
		}
	}
	if !sawEvent || !sawData || !sawPing {
		t.Fatalf("stream: event=%v data=%v ping=%v err=%v", sawEvent, sawData, sawPing, sc.Err())
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

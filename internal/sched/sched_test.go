package sched

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/events"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/player"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/scan"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// fakeBrain is the real offline brain (parse, match, pick) with fake network calls.
type fakeBrain struct {
	OfflineBrain
	mu      sync.Mutex
	media   map[int]Media
	nyaa    []NyaaItem
	list    []ListEntry
	pushes  []string
	pushErr error

	torrent    []byte // FetchTorrent body
	torrentErr error
	fetched    []string // URLs passed to FetchTorrent

	prequels     map[int]prequelHop // Prequel answers by AniList id (v13_test.go)
	prequelErr   error
	prequelCalls []int
}

func (f *fakeBrain) FetchTorrent(_ context.Context, url string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetched = append(f.fetched, url)
	return f.torrent, f.torrentErr
}

func (f *fakeBrain) Media(_ context.Context, id int) (Media, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.media[id]
	if !ok {
		return Media{}, errors.New("no such media")
	}
	return m, nil
}

func (f *fakeBrain) NyaaSearch(context.Context, string, string, string) ([]NyaaItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]NyaaItem{}, f.nyaa...), nil
}

func (f *fakeBrain) AniListUserList(context.Context, string) ([]ListEntry, error) { return f.list, nil }

func (f *fakeBrain) AniListPush(_ context.Context, token string, id, progress int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pushErr != nil {
		return f.pushErr
	}
	f.pushes = append(f.pushes, token+":"+itoa(id)+":"+itoa(progress))
	return nil
}

type env struct {
	s     *Sched
	db    *store.DB
	set   *settings.Manager
	brain *fakeBrain
	root  string
	clock time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "abc.db"))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "lib")
	os.MkdirAll(root, 0o755)
	set, _ := settings.NewManager(db)
	c := set.Get()
	c.Library.Folders = []settings.Folder{{Path: root, Active: true, Download: true}}
	if _, err := set.Update(c); err != nil {
		t.Fatal(err)
	}
	e := &env{db: db, set: set, brain: &fakeBrain{media: map[int]Media{}}, root: root, clock: time.Unix(1_700_000_000, 0)}
	e.s = New(db, set, events.New(db), e.brain)
	e.s.now = func() time.Time { return e.clock }
	e.s.nyaaSpace = 0
	e.s.diskFree = func(string) (int64, error) { return 1 << 40, nil } // 1 TiB, so the real drive never decides a test
	e.s.windows = func([]string) []player.Playing { return nil }
	e.s.launch = &fakeLauncher{}
	t.Cleanup(func() { e.s.Stop(); db.Close() })
	return e
}

func (e *env) update(t *testing.T, fn func(*settings.Settings)) {
	t.Helper()
	c := e.set.Get()
	fn(&c)
	if _, err := e.set.Update(c); err != nil {
		t.Fatal(err)
	}
}

func (e *env) show(t *testing.T, sh store.Show) store.Show {
	t.Helper()
	if err := e.db.CreateShow(context.Background(), &sh); err != nil {
		t.Fatal(err)
	}
	return sh
}

func (e *env) kinds(t *testing.T) []string {
	t.Helper()
	evs, _ := e.db.ListEvents(context.Background(), 0, 1000)
	var out []string
	for i := len(evs) - 1; i >= 0; i-- {
		out = append(out, evs[i].Kind)
	}
	return out
}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func count(list []string, v string) int {
	n := 0
	for _, x := range list {
		if x == v {
			n++
		}
	}
	return n
}

type fakeLauncher struct {
	mu     sync.Mutex
	opened []string
	reveal []string
	dirs   []string
	urls   []string
	urlErr error
	done   chan struct{}
}

func (f *fakeLauncher) OpenDir(dir string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dirs = append(f.dirs, dir)
	return nil
}

func (f *fakeLauncher) OpenURL(u string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.urlErr != nil {
		return f.urlErr
	}
	f.urls = append(f.urls, u)
	return nil
}

func (f *fakeLauncher) Open(player, file string) (<-chan struct{}, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, file)
	if f.done != nil {
		return f.done, nil
	}
	return nil, nil
}

func (f *fakeLauncher) Reveal(file string) error {
	f.mu.Lock()
	f.reveal = append(f.reveal, file)
	f.mu.Unlock()
	return nil
}

func touch(t *testing.T, p string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---- accumulator ----

func TestAccumulatorFakeClock(t *testing.T) {
	a := newAccumulator()
	t0 := time.Unix(1000, 0)
	gap := 15 * time.Second
	a.Observe(t0, []int64{1}, gap)
	if a.Seconds(1) != 0 {
		t.Fatal("first sighting must add nothing")
	}
	a.Observe(t0.Add(5*time.Second), []int64{1, 2}, gap)
	a.Observe(t0.Add(10*time.Second), []int64{1, 2}, gap)
	if a.Seconds(1) != 10 || a.Seconds(2) != 5 {
		t.Fatalf("secs = %v %v", a.Seconds(1), a.Seconds(2))
	}
	a.Observe(t0.Add(1000*time.Second), []int64{1}, gap) // PC slept: capped
	if a.Seconds(1) != 25 {
		t.Fatalf("gap not capped: %v", a.Seconds(1))
	}
	a.Observe(t0.Add(1005*time.Second), nil, gap)
	a.Observe(t0.Add(1010*time.Second), []int64{1}, gap) // re-sighting after a miss adds nothing
	if a.Seconds(1) != 25 {
		t.Fatalf("re-sighting added time: %v", a.Seconds(1))
	}
	if !a.TryMark(1) || a.TryMark(1) {
		t.Fatal("TryMark must succeed exactly once")
	}
	a.Observe(t0.Add(8*time.Hour), nil, gap)
	if a.Seconds(1) != 0 || !a.TryMark(1) {
		t.Fatal("entries older than 6h must be forgotten")
	}
}

func TestMatchWindow(t *testing.T) {
	files := []store.File{
		{ID: 1, Path: filepath.Join("lib", "[SubsPlease] Frieren - 01 (1080p).mkv")},
		{ID: 2, Path: filepath.Join("lib", "[SubsPlease] Frieren - 01 (1080p) [v2].mkv")},
		{ID: 3, Path: filepath.Join("lib", "01.mkv")},
	}
	if id := matchWindow("[SubsPlease] Frieren - 01 (1080p).mkv - mpv", files); id != 1 {
		t.Fatalf("mpv title -> %d", id)
	}
	if id := matchWindow("[SubsPlease] Frieren - 01 (1080p) [v2] - VLC media player", files); id != 2 {
		t.Fatalf("VLC title without ext -> %d", id)
	}
	if id := matchWindow("01 - mpv", files); id != 0 {
		t.Fatalf("short stem must not match: %d", id)
	}
}

// ---- player loop ----

func TestPlayerLoopMarksWatchedAtFraction(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sh := e.show(t, store.Show{TitleRomaji: "Frieren", DurationMin: store.Ptr(1), AniListID: 5})
	path := filepath.Join(e.root, "[SubsPlease] Frieren - 03 (1080p).mkv")
	e.db.UpsertFiles(ctx, []store.File{{Path: path, ShowID: &sh.ID, Episode: store.Ptr(3)}})
	e.s.windows = func(f []string) []player.Playing {
		return []player.Playing{{Title: "[SubsPlease] Frieren - 03 (1080p).mkv - mpv"}}
	}
	e.update(t, func(c *settings.Settings) { c.Watched.Fraction = 0.5; c.Watched.PollSec = 5; c.Push.AniList = true })
	e.db.UpsertAccount(ctx, store.Account{Service: "anilist", AccessToken: "tok"})

	for i := 0; i < 6; i++ { // 0,5,...,25 s: below 30 s
		if err := e.s.playerRun(ctx); err != nil {
			t.Fatal(err)
		}
		e.clock = e.clock.Add(5 * time.Second)
	}
	if ep, _ := e.db.GetEpisode(ctx, sh.ID, 3); ep.WatchedAt != nil {
		t.Fatal("watched too early")
	}
	e.s.playerRun(ctx) // 30 s
	ep, err := e.db.GetEpisode(ctx, sh.ID, 3)
	if err != nil || ep.WatchedAt == nil || ep.WatchedSource != "player" {
		t.Fatalf("not watched at threshold: %+v %v", ep, err)
	}
	e.s.playerRun(ctx)
	if n := count(e.kinds(t), events.EpisodeWatched); n != 1 {
		t.Fatalf("episode.watched logged %d times", n)
	}
	e.s.push.flush(ctx)
	if len(e.brain.pushes) != 1 || e.brain.pushes[0] != "tok:5:3" {
		t.Fatalf("pushes = %v", e.brain.pushes)
	}
	if !has(e.kinds(t), events.PushOK) {
		t.Fatal("push.ok missing")
	}
}

func TestOnOpenModeAndLauncher(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sh := e.show(t, store.Show{TitleRomaji: "A"})
	path := filepath.Join(e.root, "A - 01.mkv")
	touch(t, path)
	fs := []store.File{{Path: path, ShowID: &sh.ID, Episode: store.Ptr(1)}}
	e.db.UpsertFiles(ctx, fs)
	e.update(t, func(c *settings.Settings) { c.Watched.Mode = "on_open" })
	if err := e.s.OpenFile(ctx, fs[0].ID); err != nil {
		t.Fatal(err)
	}
	fl := e.s.launch.(*fakeLauncher)
	if len(fl.opened) != 1 || fl.opened[0] != path {
		t.Fatalf("launcher opened %v", fl.opened)
	}
	if ep, _ := e.db.GetEpisode(ctx, sh.ID, 1); ep.WatchedAt == nil {
		t.Fatal("on_open did not mark watched")
	}
	if err := e.s.RevealFile(ctx, fs[0].ID); err != nil || len(fl.reveal) != 1 {
		t.Fatalf("reveal: %v %v", err, fl.reveal)
	}
	os.Remove(path)
	if err := e.s.OpenFile(ctx, fs[0].ID); !errors.Is(err, ErrFileMissing) {
		t.Fatalf("missing file: %v", err)
	}
}

func TestLaunchedPlayerCountsWithoutWindowTitle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sh := e.show(t, store.Show{TitleRomaji: "A", DurationMin: store.Ptr(1)})
	path := filepath.Join(e.root, "A - 02.mkv")
	touch(t, path)
	fs := []store.File{{Path: path, ShowID: &sh.ID, Episode: store.Ptr(2)}}
	e.db.UpsertFiles(ctx, fs)
	fl := e.s.launch.(*fakeLauncher)
	fl.done = make(chan struct{})
	if err := e.s.OpenFile(ctx, fs[0].ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		e.s.playerRun(ctx)
		e.clock = e.clock.Add(5 * time.Second)
	}
	if ep, _ := e.db.GetEpisode(ctx, sh.ID, 2); ep.WatchedAt == nil {
		t.Fatal("tracked process time did not count")
	}
	close(fl.done)
	if len(e.s.opens.alive()) != 0 {
		t.Fatal("exited player still tracked")
	}
}

// ---- conveyor ----

type fakeQbit struct {
	mu     sync.Mutex
	added  []string
	state  map[string]float64 // hash -> progress; a hash not listed is "not found"
	status map[string]string  // hash -> qBittorrent state (default "downloading")
}

func newFakeQbit(t *testing.T) (*fakeQbit, string) {
	f := &fakeQbit{state: map[string]float64{}, status: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/torrents/createCategory", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("POST /api/v2/torrents/add", func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(1 << 20)
		f.mu.Lock()
		f.added = append(f.added, r.FormValue("savepath")+"|"+r.FormValue("urls"))
		f.mu.Unlock()
		w.Write([]byte("Ok."))
	})
	mux.HandleFunc("GET /api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) {
		h := r.URL.Query().Get("hashes")
		f.mu.Lock()
		p, ok := f.state[h]
		st := f.status[h]
		f.mu.Unlock()
		if !ok {
			w.Write([]byte("[]"))
			return
		}
		if st == "" {
			st = "downloading"
		}
		w.Write([]byte(`[{"hash":"` + h + `","progress":` + strconv.FormatFloat(p, 'f', -1, 64) + `,"state":"` + st + `","content_path":""}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, srv.URL
}

const h1 = "1111111111111111111111111111111111111111"
const h2 = "2222222222222222222222222222222222222222"

func TestConveyorPicksPreferredGroupAndAdds(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	q, url := newFakeQbit(t)
	e.update(t, func(c *settings.Settings) { c.Downloader.Mode = settings.ModeQbit; c.Qbit.URL = url })
	sh := e.show(t, store.Show{TitleRomaji: "Sousou no Frieren", Status: "RELEASING"})
	aired := e.clock.Add(-2 * time.Hour).Unix()
	e.db.UpsertEpisodeAiring(ctx, sh.ID, 5, &aired)
	future := e.clock.Add(48 * time.Hour).Unix()
	e.db.UpsertEpisodeAiring(ctx, sh.ID, 6, &future)
	e.brain.nyaa = []NyaaItem{
		{ID: 1, Title: "[Erai-raws] Sousou no Frieren - 05 [1080p][HEVC].mkv", InfoHash: h1, Seeders: 50, PublishedAt: e.clock.Add(-time.Hour)},
		{ID: 2, Title: "[SubsPlease] Sousou no Frieren - 05 (1080p) [ABCD1234].mkv", InfoHash: h2, Seeders: 10, PublishedAt: e.clock.Add(-time.Hour)},
		{ID: 3, Title: "[SubsPlease] Sousou no Frieren - 05 (720p) [ABCD1234].mkv", InfoHash: "3333333333333333333333333333333333333333", Seeders: 99, PublishedAt: e.clock.Add(-time.Hour)},
		{ID: 4, Title: "[SubsPlease] Other Show - 05 (1080p).mkv", InfoHash: "4444444444444444444444444444444444444444", PublishedAt: e.clock.Add(-time.Hour)},
		{ID: 5, Title: "[SubsPlease] Sousou no Frieren - 01 (1080p).mkv", InfoHash: "5555555555555555555555555555555555555555", PublishedAt: e.clock.Add(-30 * 24 * time.Hour)},
	}
	if err := e.s.conveyorRun(ctx, 0); err != nil {
		t.Fatal(err)
	}
	rels, _ := e.db.ListReleases(ctx, sh.ID, nil)
	if len(rels) != 3 {
		t.Fatalf("want 3 matched in-lookback candidates, got %d", len(rels))
	}
	var added *store.Release
	for i := range rels {
		if rels[i].Status == store.RelAdded {
			added = &rels[i]
		}
	}
	if added == nil || added.NyaaID != 2 || added.QbitHash != h2 {
		t.Fatalf("SubsPlease 1080p should be added: %+v", rels)
	}
	want := filepath.Join(e.root, "Sousou no Frieren")
	if len(q.added) != 1 || !strings.HasPrefix(q.added[0], want+"|magnet:?xt=urn:btih:"+h2) {
		t.Fatalf("qbit add = %v", q.added)
	}
	k := e.kinds(t)
	if !has(k, events.ReleaseFound) || !has(k, events.ReleaseAdded) {
		t.Fatalf("events = %v", k)
	}
	// second pass: release is active, nothing new is added
	if err := e.s.conveyorRun(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if len(q.added) != 1 {
		t.Fatalf("re-added: %v", q.added)
	}

	// qbit sync: downloading, then done
	if err := e.s.qbitRun(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.db.GetRelease(ctx, added.ID); r.Status != store.RelSkipped || r.StatusNote != "removed in qBittorrent" {
		t.Fatalf("unknown hash (removed in qBittorrent) should be skipped, got %s %q", r.Status, r.StatusNote)
	}
	e.db.SetReleaseQbit(ctx, added.ID, store.RelAdded, h2, "")
	q.state[h2] = 0.5
	e.s.qbitRun(ctx)
	if r, _ := e.db.GetRelease(ctx, added.ID); r.Status != store.RelDownloading {
		t.Fatalf("want downloading, got %s", r.Status)
	}
	q.state[h2] = 1
	e.s.qbitRun(ctx)
	if r, _ := e.db.GetRelease(ctx, added.ID); r.Status != store.RelDone {
		t.Fatalf("want done, got %s", r.Status)
	}
	if !has(e.kinds(t), events.DownloadDone) {
		t.Fatal("download.done missing")
	}
}

func TestConveyorWaitsThenFallsBackAndManualWithoutQbit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sh := e.show(t, store.Show{TitleRomaji: "Frieren", Status: "RELEASING"})
	aired := e.clock.Add(-1 * time.Hour).Unix()
	e.db.UpsertEpisodeAiring(ctx, sh.ID, 1, &aired)
	e.brain.nyaa = []NyaaItem{{ID: 9, Title: "[Judas] Frieren - 01 [1080p].mkv", InfoHash: h1, Seeders: 5, PublishedAt: e.clock}}
	e.s.conveyorRun(ctx, 0)
	e.s.conveyorRun(ctx, 0)
	if n := count(e.kinds(t), events.ReleaseWaiting); n != 1 {
		t.Fatalf("release.waiting logged %d times, want 1", n)
	}
	e.clock = e.clock.Add(12 * time.Hour)
	e.s.conveyorRun(ctx, 0)
	rels, _ := e.db.ListReleases(ctx, sh.ID, nil)
	if rels[0].Status != store.RelChosen {
		t.Fatalf("qbit off: want chosen, got %s", rels[0].Status)
	}
	// manual release becomes done when its file appears
	touch(t, filepath.Join(e.root, "Frieren", "[Judas] Frieren - 01 [1080p].mkv"))
	if err := e.s.scanRun(ctx, false); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.db.GetRelease(ctx, rels[0].ID); r.Status != store.RelDone {
		t.Fatalf("chosen release not done after file found: %s", r.Status)
	}
}

func TestAutoReplaceV2DeletesOldFileInsideRoot(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	q, url := newFakeQbit(t)
	e.update(t, func(c *settings.Settings) { c.Downloader.Mode = settings.ModeQbit; c.Qbit.URL = url })
	sh := e.show(t, store.Show{TitleRomaji: "Frieren", Status: "RELEASING"})
	aired := e.clock.Add(-24 * time.Hour).Unix()
	e.db.UpsertEpisodeAiring(ctx, sh.ID, 2, &aired)
	v1 := filepath.Join(e.root, "Frieren", "[SubsPlease] Frieren - 02 (1080p).mkv")
	touch(t, v1)
	e.s.scanRun(ctx, false)
	e.brain.nyaa = []NyaaItem{{ID: 7, Title: "[SubsPlease] Frieren - 02v2 (1080p).mkv", InfoHash: h1, Seeders: 5, PublishedAt: e.clock}}
	e.s.conveyorRun(ctx, 0)
	if len(q.added) != 1 {
		t.Fatalf("v2 not added: %v", q.added)
	}
	v2 := filepath.Join(e.root, "Frieren", "[SubsPlease] Frieren - 02v2 (1080p).mkv")
	touch(t, v2)
	q.state[h1] = 1
	e.s.qbitRun(ctx) // done -> scan save path -> v2 found -> v1 deleted
	e.s.scanRun(ctx, false)
	if _, err := os.Stat(v1); !os.IsNotExist(err) {
		t.Fatalf("old v1 file should be deleted: %v", err)
	}
	if _, err := os.Stat(v2); err != nil {
		t.Fatal("v2 must stay")
	}
}

// ---- scan ----

func TestScanFoundMissingAndFolderFallback(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sh := e.show(t, store.Show{TitleRomaji: "Sousou no Frieren", EpisodesTotal: store.Ptr(28)})
	f1 := filepath.Join(e.root, "x", "[SubsPlease] Sousou no Frieren - 01 (1080p).mkv")
	f2 := filepath.Join(e.root, "Sousou no Frieren", "Episode - 02.mkv") // name has no title: folder supplies it
	recap := filepath.Join(e.root, "x", "[SubsPlease] Sousou no Frieren - 01.5 (1080p).mkv")
	other := filepath.Join(e.root, "x", "random video.mkv")
	for _, p := range []string{f1, recap, other} {
		touch(t, p)
	}
	touch(t, f2)
	if err := e.s.scanRun(ctx, true); err != nil {
		t.Fatal(err)
	}
	if files, _ := e.db.FilesForShow(ctx, sh.ID); len(files) != 2 {
		t.Fatalf("want f1 and f2 (folder fallback) matched, got %d", len(files))
	}
	all, _ := e.db.AllFiles(ctx)
	if len(all) != 4 {
		t.Fatalf("want 4 rows, got %d", len(all))
	}
	k := e.kinds(t)
	if count(k, events.FileFound) < 1 || !has(k, events.ScanDone) {
		t.Fatalf("events = %v", k)
	}
	byPath := map[string]store.File{}
	for _, f := range all {
		byPath[f.Path] = f
	}
	if f := byPath[f1]; f.ShowID == nil || *f.Episode != 1 {
		t.Fatalf("f1 not matched: %+v", f)
	}
	if f := byPath[recap]; f.ShowID != nil {
		t.Fatalf("recap must not match: %+v", f)
	}
	// rescan with nothing changed logs nothing new
	before := len(e.kinds(t))
	e.s.scanRun(ctx, false)
	if len(e.kinds(t)) != before {
		t.Fatalf("idle rescan logged events: %v", e.kinds(t)[before:])
	}
	os.Remove(f1)
	e.s.scanRun(ctx, false)
	if f, _ := e.db.GetFileByPath(ctx, f1); f.Present {
		t.Fatal("deleted file still present")
	}
	if !has(e.kinds(t), events.FileMissing) {
		t.Fatal("file.missing missing")
	}
	// a folder that does not exist (unplugged drive) next to a readable one
	e.update(t, func(c *settings.Settings) {
		c.Library.Folders = append(c.Library.Folders, settings.Folder{Path: filepath.Join(filepath.Dir(e.root), "gone-drive"), Active: true})
	})
	e.s.scanRun(ctx, false)
	if f, _ := e.db.GetFileByPath(ctx, other); !f.Present {
		t.Fatal("file under a readable root marked missing")
	}
}

// SPEC v1.1 section 1: a folder whose Stat fails at scan time is skipped and its
// files are NOT marked missing; they stay present and come back with it.
func TestScanSkipsUnreachableFolder(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sh := e.show(t, store.Show{TitleRomaji: "Sousou no Frieren", EpisodesTotal: store.Ptr(28)})
	drive := filepath.Join(filepath.Dir(e.root), "usb")
	ep := filepath.Join(drive, "[SubsPlease] Sousou no Frieren - 03 (1080p).mkv")
	touch(t, ep)
	e.update(t, func(c *settings.Settings) {
		c.Library.Folders = append(c.Library.Folders, settings.Folder{Path: drive, Active: true})
	})
	if err := e.s.scanRun(ctx, false); err != nil {
		t.Fatal(err)
	}
	if f, _ := e.db.GetFileByPath(ctx, ep); !f.Present || f.ShowID == nil || *f.ShowID != sh.ID {
		t.Fatalf("file on the second folder not found: %+v", f)
	}
	unplugged := drive + "-away"
	if err := os.Rename(drive, unplugged); err != nil {
		t.Fatal(err)
	}
	before := count(e.kinds(t), events.FileMissing)
	if err := e.s.scanRun(ctx, true); err != nil {
		t.Fatal(err)
	}
	if f, _ := e.db.GetFileByPath(ctx, ep); !f.Present {
		t.Fatal("file on an unreachable folder was marked missing")
	}
	if count(e.kinds(t), events.FileMissing) != before {
		t.Fatal("file.missing logged for an unreachable folder")
	}
	// the watcher reporting the whole root as gone is treated the same way
	e.s.onFileGone(drive)
	if f, _ := e.db.GetFileByPath(ctx, ep); !f.Present {
		t.Fatal("watcher root removal marked files missing")
	}
	os.Rename(unplugged, drive)
	e.s.scanRun(ctx, false)
	if f, _ := e.db.GetFileByPath(ctx, ep); !f.Present {
		t.Fatal("file not present after the folder came back")
	}
}

// Deactivating or forgetting a folder hides its files (present=0) without
// per-file events and logs one scan.done; files also under an active folder stay.
func TestDeactivatedFolderHidesFiles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.show(t, store.Show{TitleRomaji: "Sousou no Frieren", EpisodesTotal: store.Ptr(28)})
	old := filepath.Join(filepath.Dir(e.root), "old")
	a := filepath.Join(old, "[SubsPlease] Sousou no Frieren - 01 (1080p).mkv")
	b := filepath.Join(old, "[SubsPlease] Sousou no Frieren - 02 (1080p).mkv")
	keep := filepath.Join(e.root, "[SubsPlease] Sousou no Frieren - 03 (1080p).mkv")
	for _, p := range []string{a, b, keep} {
		touch(t, p)
	}
	e.update(t, func(c *settings.Settings) {
		c.Library.Folders = append(c.Library.Folders, settings.Folder{Path: old, Label: "Old drive", Active: true})
	})
	e.s.scanRun(ctx, false)
	missingBefore := count(e.kinds(t), events.FileMissing)
	e.update(t, func(c *settings.Settings) { c.Library.Folders[1].Active = false })
	for _, p := range []string{a, b} {
		if f, _ := e.db.GetFileByPath(ctx, p); f.Present {
			t.Fatalf("%s still present after its folder became a past folder", p)
		}
	}
	if f, _ := e.db.GetFileByPath(ctx, keep); !f.Present {
		t.Fatal("file under the still-active folder hidden")
	}
	if count(e.kinds(t), events.FileMissing) != missingBefore {
		t.Fatal("per-file events logged for a removed folder")
	}
	evs, _ := e.db.ListEvents(ctx, 0, 1)
	if len(evs) != 1 || evs[0].Kind != events.ScanDone || evs[0].Message != "Folder Old drive removed from the library: 2 files hidden" {
		t.Fatalf("last event = %+v", evs)
	}
	// reactivating is just a rescan: the files come back
	e.update(t, func(c *settings.Settings) { c.Library.Folders[1].Active = true })
	e.s.scanRun(ctx, false)
	if f, _ := e.db.GetFileByPath(ctx, a); !f.Present {
		t.Fatal("file not back after reactivation")
	}
	// forgetting the folder (removed from the list) hides them again
	e.update(t, func(c *settings.Settings) { c.Library.Folders = c.Library.Folders[:1] })
	if f, _ := e.db.GetFileByPath(ctx, a); f.Present {
		t.Fatal("file present after the folder was forgotten")
	}
}

// ---- add show / airing / import ----

func TestAddShowAndAiringRefresh(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	at := e.clock.Add(-time.Hour).Unix()
	next := e.clock.Add(6 * 24 * time.Hour).Unix()
	e.brain.media[154587] = Media{
		MediaSummary: MediaSummary{ID: 154587, IDMal: 52991, TitleRomaji: "Sousou no Frieren", TitleEnglish: "Frieren", Status: "RELEASING",
			Episodes: 3, Duration: 24, NextAiringEp: 2, NextAiringAt: next},
		Synonyms: []string{"Frieren: Beyond Journey's End"},
		Airing:   []AiringNode{{Episode: 1, AiringAt: at}, {Episode: 2, AiringAt: next}},
	}
	sh, created, err := e.s.AddShow(ctx, 154587, "")
	if err != nil || !created {
		t.Fatalf("AddShow: %v %v", created, err)
	}
	if sh.MALID != 52991 || *sh.EpisodesTotal != 3 || sh.ListStatus != "watching" || *sh.NextAiringEp != 2 {
		t.Fatalf("show = %+v", sh)
	}
	eps, _ := e.db.ListEpisodes(ctx, sh.ID)
	if len(eps) != 3 || eps[0].AiredAt == nil || *eps[0].AiredAt != at || eps[2].AiredAt != nil {
		t.Fatalf("episodes = %+v", eps)
	}
	if _, created, _ := e.s.AddShow(ctx, 154587, ""); created {
		t.Fatal("second add created a duplicate")
	}
	// refresh with no change: no airing.updated
	if err := e.s.airingRun(ctx); err != nil {
		t.Fatal(err)
	}
	if has(e.kinds(t), events.AiringUpdated) {
		t.Fatal("airing.updated without a change")
	}
	m := e.brain.media[154587]
	m.Airing = append(m.Airing, AiringNode{Episode: 3, AiringAt: next + 7*86400})
	e.brain.media[154587] = m
	e.s.airingRun(ctx)
	if !has(e.kinds(t), events.AiringUpdated) {
		t.Fatal("airing.updated missing after schedule change")
	}
	if _, _, err := e.s.AddShow(ctx, 1, ""); err == nil {
		t.Fatal("unknown media must fail")
	}
}

func TestImportAniList(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.brain.media[10] = Media{MediaSummary: MediaSummary{ID: 10, TitleRomaji: "Ten", Episodes: 12, Status: "FINISHED"}}
	existing := e.show(t, store.Show{AniListID: 20, TitleRomaji: "Twenty", ListStatus: "planning"})
	e.brain.list = []ListEntry{
		{MediaID: 10, Status: "COMPLETED", Progress: 12, Score: 8},
		{MediaID: 20, Status: "CURRENT", Progress: 2},
		{MediaID: 30, Status: "PLANNING"}, // Media lookup fails: skipped
	}
	added, updated, err := e.s.ImportAniList(ctx, "someone")
	if err != nil || added != 1 || updated != 1 {
		t.Fatalf("import = %d %d %v", added, updated, err)
	}
	ten, _ := e.db.GetShowByAniList(ctx, 10)
	if ten.ListStatus != "completed" || *ten.Score != 8 {
		t.Fatalf("ten = %+v", ten)
	}
	if m, _ := e.db.MaxWatched(ctx, ten.ID); m != 12 {
		t.Fatalf("ten watched up to %d", m)
	}
	tw, _ := e.db.GetShow(ctx, existing.ID)
	if tw.ListStatus != "watching" {
		t.Fatalf("existing show status = %s", tw.ListStatus)
	}
	if ep, _ := e.db.GetEpisode(ctx, existing.ID, 2); ep.WatchedSource != "import" {
		t.Fatalf("import watched source = %q", ep.WatchedSource)
	}
}

func TestPushRetriesThenFails(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.s.pushRetry = 0
	sh := e.show(t, store.Show{AniListID: 7, TitleRomaji: "Seven"})
	e.db.UpsertAccount(ctx, store.Account{Service: "anilist", AccessToken: "tok"})
	e.update(t, func(c *settings.Settings) { c.Push.AniList = true })
	e.brain.pushErr = errors.New("HTTP 500")
	if _, err := e.s.SetWatched(ctx, sh.ID, 1, true, "manual"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		e.s.push.flush(ctx)
	}
	k := e.kinds(t)
	if count(k, events.PushFailed) != 1 || has(k, events.PushOK) {
		t.Fatalf("events = %v", k)
	}
	e.s.push.flush(ctx) // job dropped: no more attempts
	if count(e.kinds(t), events.PushFailed) != 1 {
		t.Fatal("dropped job retried")
	}
}

// ---- loops ----

func TestLoopNeverOverlapsAndIntervalFollowsSettings(t *testing.T) {
	e := newEnv(t)
	l := e.s.conveyor
	l.mu.Lock()
	if e.s.RunConveyorNow() {
		t.Fatal("started while busy")
	}
	l.mu.Unlock()
	if got := l.interval(e.set.Get()); got != 15*time.Minute {
		t.Fatalf("interval = %v", got)
	}
	e.update(t, func(c *settings.Settings) { c.Conveyor.PollIntervalMin = 3 })
	if got := l.interval(e.set.Get()); got != 3*time.Minute {
		t.Fatalf("interval after change = %v", got)
	}
	select {
	case <-l.reset:
	default:
		t.Fatal("settings change did not signal the loop")
	}
	e.update(t, func(c *settings.Settings) { c.Watched.Mode = "manual" })
	if e.s.playerL.interval(e.set.Get()) != 0 {
		t.Fatal("manual mode should disable the player loop")
	}
}

func TestErrorEventsAreDeduplicated(t *testing.T) {
	e := newEnv(t)
	l := &loop{name: "x", run: func(context.Context) error { return errors.New("boom") }}
	e.s.tryRun(l)
	e.s.tryRun(l)
	if n := count(e.kinds(t), events.Error); n != 1 {
		t.Fatalf("error events = %d, want 1", n)
	}
}

// ---- helpers ----

func TestPathsAndNames(t *testing.T) {
	c := settings.Defaults()
	dl := filepath.Join(t.TempDir(), "Anime")
	past := filepath.Join(t.TempDir(), "Old")
	c.Library.Folders = []settings.Folder{{Path: past, Active: false}, {Path: filepath.Join(t.TempDir(), "Extra"), Active: true}, {Path: dl, Active: true, Download: true}}
	sh := store.Show{TitleRomaji: `Re:Zero kara "Hajimeru"?`}
	if got := SavePath(c, sh); got != filepath.Join(dl, "Re Zero kara Hajimeru") {
		t.Fatalf("SavePath = %q", got)
	}
	c.Qbit.SavePathStyle = "root"
	if SavePath(c, sh) != dl {
		t.Fatal("root style")
	}
	sh.Folder = "Custom"
	if SavePath(c, sh) != filepath.Join(dl, "Custom") {
		t.Fatal("relative folder override")
	}
	sh.Folder = past
	if SavePath(c, sh) != past {
		t.Fatal("absolute folder override")
	}
	c.Library.Folders[2].Download = false
	sh.Folder = ""
	if SavePath(c, sh) != "" {
		t.Fatal("no download folder -> qbit default")
	}
	sh.Folder = "Custom"
	if SavePath(c, sh) != "" {
		t.Fatal("relative override without a download folder -> qbit default")
	}
	if NyaaQuery(store.Show{TitleRomaji: "Kaguya-sama wa Kokurasetai: Ultra Romantic"}) != "Kaguya sama wa Kokurasetai Ultra Romantic" {
		t.Fatal("NyaaQuery punctuation")
	}
	if NyaaQuery(store.Show{TitleRomaji: "x", SearchQuery: "custom q"}) != "custom q" {
		t.Fatal("NyaaQuery override")
	}
	for in, want := range map[string]string{"CURRENT": "watching", "plan_to_watch": "planning", "on_hold": "paused", "COMPLETED": "completed", "DROPPED": "dropped", "REPEATING": "watching"} {
		if ListStatusFrom(in) != want {
			t.Errorf("ListStatusFrom(%s) = %s", in, ListStatusFrom(in))
		}
	}
	if SafeName("a/b\\c:d*e?f\"g<h>i|j. ") != "a b c d e f g h i j" {
		t.Fatalf("SafeName = %q", SafeName("a/b\\c:d*e?f\"g<h>i|j. "))
	}
}

func TestScheduleGapCountsAsAired(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sh := e.show(t, store.Show{TitleRomaji: "Sousou no Frieren", Status: "RELEASING"})
	e.db.EnsureEpisodes(ctx, sh.ID, 6) // 1-4 have no airing date (AniList schedule starts at 5)
	at := e.clock.Add(-48 * time.Hour).Unix()
	e.db.UpsertEpisodeAiring(ctx, sh.ID, 5, &at)
	e.brain.nyaa = []NyaaItem{
		{ID: 31, Title: "[SubsPlease] Sousou no Frieren - 03 (1080p).mkv", InfoHash: h1, Seeders: 5, PublishedAt: e.clock},
		{ID: 36, Title: "[SubsPlease] Sousou no Frieren - 06 (1080p).mkv", InfoHash: h2, Seeders: 5, PublishedAt: e.clock},
	}
	e.s.conveyorRun(ctx, 0)
	rels, _ := e.db.ListReleases(ctx, sh.ID, nil)
	status := map[int64]string{}
	for _, r := range rels {
		status[r.NyaaID] = r.Status
	}
	// SPEC v1.1 section 9 rule 4: an episode without an air date is not auto-downloaded.
	if status[31] != store.RelCandidate || status[36] != store.RelCandidate {
		t.Fatalf("undated episodes must wait for the user: %v", status)
	}
	// Turned on by hand, ep 3 counts as aired (before aired ep 5); ep 6 (after) does not.
	e.db.SetEpisodeAuto(ctx, sh.ID, 3, store.Ptr(1))
	e.db.SetEpisodeAuto(ctx, sh.ID, 6, store.Ptr(1))
	e.s.conveyorRun(ctx, 0)
	rels, _ = e.db.ListReleases(ctx, sh.ID, nil)
	for _, r := range rels {
		status[r.NyaaID] = r.Status
	}
	if status[31] != store.RelChosen || status[36] != store.RelCandidate {
		t.Fatalf("ep 3 (undated, before aired ep 5) should be chosen, ep 6 (undated, after) not: %v", status)
	}
	cards, _ := e.db.ShowStats(ctx, e.clock.Unix())
	if cards[sh.ID].Aired != 5 {
		t.Fatalf("aired = %d, want 5", cards[sh.ID].Aired)
	}
}

// SPEC v1.1 section 8: the full scan sees files only down to each folder's scan
// depth; lowering the depth hides deeper files quietly (no file.missing per file)
// and raising it brings them back.
func TestScanDepth(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.show(t, store.Show{TitleRomaji: "Sousou no Frieren", EpisodesTotal: store.Ptr(28)})
	top := filepath.Join(e.root, "[SubsPlease] Sousou no Frieren - 01 (1080p).mkv")
	show := filepath.Join(e.root, "Frieren", "[SubsPlease] Sousou no Frieren - 02 (1080p).mkv")
	deep := filepath.Join(e.root, "Frieren", "Season 1", "[SubsPlease] Sousou no Frieren - 03 (1080p).mkv")
	for _, p := range []string{top, show, deep} {
		touch(t, p)
	}
	present := func() string {
		var out []string
		for _, p := range []string{top, show, deep} {
			f, err := e.db.GetFileByPath(ctx, p)
			out = append(out, strconv.FormatBool(err == nil && f.Present))
		}
		return strings.Join(out, ",")
	}
	setScan := func(v string) {
		e.update(t, func(c *settings.Settings) { c.Library.Folders[0].Scan = v })
		if err := e.s.scanRun(ctx, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.s.scanRun(ctx, false); err != nil { // default "shows"
		t.Fatal(err)
	}
	if got := present(); got != "true,true,false" {
		t.Fatalf("shows: %s", got)
	}
	setScan(settings.ScanDeep)
	if got := present(); got != "true,true,true" {
		t.Fatalf("deep: %s", got)
	}
	missing := count(e.kinds(t), events.FileMissing)
	e.update(t, func(c *settings.Settings) { c.Qbit.SavePathStyle = "root" }) // flat download folder needs this
	setScan(settings.ScanFlat)
	if got := present(); got != "true,false,false" {
		t.Fatalf("flat: %s", got)
	}
	if count(e.kinds(t), events.FileMissing) != missing {
		t.Fatal("file.missing logged for files hidden by a lower scan depth")
	}
	evs, _ := e.db.ListEvents(ctx, 0, 1)
	if len(evs) != 1 || evs[0].Kind != events.ScanDone || !strings.Contains(evs[0].Message, "2 hidden") {
		t.Fatalf("last event = %+v", evs)
	}
	// A file that really vanished inside the depth is still reported missing.
	os.Remove(top)
	e.s.scanRun(ctx, false)
	if count(e.kinds(t), events.FileMissing) != missing+1 {
		t.Fatal("deleted file inside the depth not reported")
	}
	// The watcher callback drops files deeper than the depth too.
	e.s.onFileSeen(scan.FileSeen{Path: show, Size: 5, Mtime: 1})
	if got := present(); got != "false,false,false" {
		t.Fatalf("onFileSeen ignored the depth: %s", got)
	}
	setScan(settings.ScanShows)
	if got := present(); got != "false,true,false" {
		t.Fatalf("back to shows: %s", got)
	}
}

func TestLibraryDirsDepth(t *testing.T) {
	base := t.TempDir()
	var c settings.Settings
	c.Library.Folders = []settings.Folder{
		{Path: filepath.Join(base, "a"), Active: true, Scan: settings.ScanFlat},
		{Path: filepath.Join(base, "past"), Active: false, Scan: settings.ScanDeep},
		{Path: filepath.Join(base, "b"), Active: true, Scan: settings.ScanDeep},
		{Path: filepath.Join(base, "c"), Active: true},
	}
	got := libraryDirs(c)
	want := []scan.Dir{{Path: filepath.Join(base, "a"), MaxDepth: 0}, {Path: filepath.Join(base, "b"), MaxDepth: -1}, {Path: filepath.Join(base, "c"), MaxDepth: 1}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("libraryDirs = %+v", got)
	}
}

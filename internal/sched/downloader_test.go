package sched

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/events"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// modeEnv is a show with one aired episode and one SubsPlease 1080p candidate.
func modeEnv(t *testing.T, torrentURL string) (*env, store.Show, string) {
	t.Helper()
	e := newEnv(t)
	sh := e.show(t, store.Show{TitleRomaji: "Sousou no Frieren", Status: "RELEASING"})
	aired := e.clock.Add(-2 * time.Hour).Unix()
	e.db.UpsertEpisodeAiring(context.Background(), sh.ID, 5, &aired)
	title := "[SubsPlease] Sousou no Frieren - 05 (1080p) [ABCD1234].mkv"
	e.brain.nyaa = []NyaaItem{{ID: 2, Title: title, InfoHash: h2, TorrentURL: torrentURL, Seeders: 10, PublishedAt: e.clock.Add(-time.Hour)}}
	return e, sh, title
}

func onlyRelease(t *testing.T, e *env, showID int64) store.Release {
	t.Helper()
	rels, err := e.db.ListReleases(context.Background(), showID, nil)
	if err != nil || len(rels) != 1 {
		t.Fatalf("want 1 release, got %d (%v)", len(rels), err)
	}
	return rels[0]
}

// lastAdded returns the data of the newest release.added event.
func lastAdded(t *testing.T, e *env) (string, map[string]any) {
	t.Helper()
	evs, _ := e.db.ListEvents(context.Background(), 0, 1000)
	for _, ev := range evs {
		if ev.Kind == events.ReleaseAdded {
			var d map[string]any
			json.Unmarshal(ev.Data, &d)
			return ev.Message, d
		}
	}
	t.Fatal("no release.added event")
	return "", nil
}

func TestMagnetModeUsesTheOSHandler(t *testing.T) {
	e, sh, _ := modeEnv(t, "")
	ctx := context.Background()
	e.update(t, func(c *settings.Settings) { c.Downloader.Mode = settings.ModeMagnet })
	if err := e.s.conveyorRun(ctx, 0); err != nil {
		t.Fatal(err)
	}
	r := onlyRelease(t, e, sh.ID)
	fl := e.s.launch.(*fakeLauncher)
	if r.Status != store.RelAdded || r.QbitHash != "" || r.StatusNote != "sent to the magnet link handler" {
		t.Fatalf("release = %s %q %q", r.Status, r.QbitHash, r.StatusNote)
	}
	if len(fl.urls) != 1 || !strings.HasPrefix(fl.urls[0], "magnet:?xt=urn:btih:"+h2) {
		t.Fatalf("opener got %v", fl.urls)
	}
	msg, d := lastAdded(t, e)
	if !strings.Contains(msg, "sent to your torrent app (magnet link)") || d["mode"] != "magnet" || d["manual"] != false {
		t.Fatalf("event %q %v", msg, d)
	}
	// the qbit sync loop has nothing to poll; the scanner finishes it by file
	if err := e.s.qbitRun(ctx); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(e.root, "Downloads", "[SubsPlease] Sousou no Frieren - 05 (1080p) [ABCD1234].mkv"))
	e.s.scanRun(ctx, false)
	if r, _ := e.db.GetRelease(ctx, r.ID); r.Status != store.RelDone {
		t.Fatalf("magnet release not done after its file appeared: %s", r.Status)
	}
}

func TestMagnetHandlerFailureStaysChosenWithoutEventSpam(t *testing.T) {
	e, sh, _ := modeEnv(t, "")
	ctx := context.Background()
	e.update(t, func(c *settings.Settings) { c.Downloader.Mode = settings.ModeMagnet })
	e.s.launch.(*fakeLauncher).urlErr = errors.New("no handler")
	e.s.conveyorRun(ctx, 0)
	e.s.conveyorRun(ctx, 0) // retried: same reason, no second event
	r := onlyRelease(t, e, sh.ID)
	if r.Status != store.RelChosen || r.StatusNote != "magnet link handler: no handler" {
		t.Fatalf("release = %s %q", r.Status, r.StatusNote)
	}
	if n := count(e.kinds(t), events.ReleaseAdded); n != 1 {
		t.Fatalf("release.added logged %d times, want 1", n)
	}
	msg, d := lastAdded(t, e)
	if !strings.Contains(msg, "chosen, add it manually") || d["manual"] != true || d["magnet"] == "" {
		t.Fatalf("event %q %v", msg, d)
	}
	// the handler works again: the next pass sends it
	e.s.launch.(*fakeLauncher).urlErr = nil
	e.s.conveyorRun(ctx, 0)
	if r := onlyRelease(t, e, sh.ID); r.Status != store.RelAdded {
		t.Fatalf("retry after the handler came back: %s", r.Status)
	}
}

func TestTorrentFileModeWritesThroughPart(t *testing.T) {
	e, sh, title := modeEnv(t, "https://nyaa.si/download/2.torrent")
	ctx := context.Background()
	watch := filepath.Join(filepath.Dir(e.root), "watch")
	os.MkdirAll(watch, 0o755)
	e.brain.torrent = []byte("d4:infod4:name3:abcee")
	e.update(t, func(c *settings.Settings) {
		c.Downloader.Mode = settings.ModeTorrentFile
		c.Downloader.TorrentDir = watch
	})
	if err := e.s.conveyorRun(ctx, 0); err != nil {
		t.Fatal(err)
	}
	name := SafeName(title) + ".torrent"
	r := onlyRelease(t, e, sh.ID)
	if r.Status != store.RelAdded || r.QbitHash != "" || r.StatusNote != "saved "+name+" to the watch folder" {
		t.Fatalf("release = %s %q %q", r.Status, r.QbitHash, r.StatusNote)
	}
	b, err := os.ReadFile(filepath.Join(watch, name))
	if err != nil || string(b) != "d4:infod4:name3:abcee" {
		t.Fatalf("watch file: %q %v", b, err)
	}
	entries, _ := os.ReadDir(watch)
	if len(entries) != 1 {
		t.Fatalf("watch folder should hold only the .torrent (no .part): %v", entries)
	}
	if len(e.brain.fetched) != 1 || e.brain.fetched[0] != "https://nyaa.si/download/2.torrent" {
		t.Fatalf("fetched %v", e.brain.fetched)
	}
	if msg, d := lastAdded(t, e); !strings.Contains(msg, ".torrent saved to the watch folder") || d["mode"] != "torrent_file" {
		t.Fatalf("event %q %v", msg, d)
	}
}

func TestTorrentFileModeFailures(t *testing.T) {
	e, sh, title := modeEnv(t, "https://nyaa.si/download/2.torrent")
	ctx := context.Background()
	watch := filepath.Join(filepath.Dir(e.root), "watch")
	os.MkdirAll(watch, 0o755)
	e.brain.torrent = []byte("d4:infod4:name3:abcee")
	e.update(t, func(c *settings.Settings) {
		c.Downloader.Mode = settings.ModeTorrentFile
		c.Downloader.TorrentDir = watch
	})
	// the final name is taken by a folder: the rename fails, the .part is removed
	blocker := filepath.Join(watch, SafeName(title)+".torrent")
	os.MkdirAll(filepath.Join(blocker, "x"), 0o755)
	e.s.conveyorRun(ctx, 0)
	r := onlyRelease(t, e, sh.ID)
	if r.Status != store.RelChosen || !strings.HasPrefix(r.StatusNote, ".torrent: ") {
		t.Fatalf("rename failure: %s %q", r.Status, r.StatusNote)
	}
	if _, err := os.Stat(blocker + ".part"); !os.IsNotExist(err) {
		t.Fatal(".part file left behind")
	}
	os.RemoveAll(blocker)
	// the download fails
	e.brain.torrentErr = errors.New("nyaa: download.nyaa.si returned 404 Not Found")
	e.s.conveyorRun(ctx, 0)
	if r := onlyRelease(t, e, sh.ID); r.Status != store.RelChosen || r.StatusNote != ".torrent: nyaa: download.nyaa.si returned 404 Not Found" {
		t.Fatalf("fetch failure: %s %q", r.Status, r.StatusNote)
	}
}

func TestTorrentFileModeWithoutTorrentURLUsesMagnet(t *testing.T) {
	e, sh, _ := modeEnv(t, "")
	ctx := context.Background()
	watch := filepath.Join(filepath.Dir(e.root), "watch")
	os.MkdirAll(watch, 0o755)
	e.update(t, func(c *settings.Settings) {
		c.Downloader.Mode = settings.ModeTorrentFile
		c.Downloader.TorrentDir = watch
	})
	e.s.conveyorRun(ctx, 0)
	r := onlyRelease(t, e, sh.ID)
	if r.Status != store.RelAdded || r.StatusNote != "sent to the magnet link handler" || len(e.s.launch.(*fakeLauncher).urls) != 1 || len(e.brain.fetched) != 0 {
		t.Fatalf("empty TorrentURL should go to the magnet handler: %s %q", r.Status, r.StatusNote)
	}
}

func TestQbitModeMissingDownloadFolder(t *testing.T) {
	e, sh, _ := modeEnv(t, "")
	ctx := context.Background()
	q, url := newFakeQbit(t)
	e.update(t, func(c *settings.Settings) { c.Downloader.Mode = settings.ModeQbit; c.Qbit.URL = url })
	gone := e.root + "-unplugged"
	os.Rename(e.root, gone)
	e.s.conveyorRun(ctx, 0)
	r := onlyRelease(t, e, sh.ID)
	if r.Status != store.RelChosen || r.StatusNote != "download folder not reachable: "+e.root || len(q.added) != 0 {
		t.Fatalf("missing download folder: %s %q, qbit adds %v", r.Status, r.StatusNote, q.added)
	}
	// the drive comes back: the next pass sends it to qBittorrent
	os.Rename(gone, e.root)
	e.s.conveyorRun(ctx, 0)
	if r := onlyRelease(t, e, sh.ID); r.Status != store.RelAdded || r.QbitHash != h2 || len(q.added) != 1 {
		t.Fatalf("after the folder came back: %s %q %v", r.Status, r.QbitHash, q.added)
	}
	if msg, d := lastAdded(t, e); !strings.Contains(msg, "sent to qBittorrent") || d["mode"] != "qbittorrent" {
		t.Fatalf("event %q %v", msg, d)
	}
}

func TestForceAddFollowsTheMode(t *testing.T) {
	e, sh, _ := modeEnv(t, "")
	ctx := context.Background()
	e.update(t, func(c *settings.Settings) {
		c.Conveyor.GroupOrder = []string{"NoSuchGroup"}
		c.Conveyor.FallbackHours = 100
	})
	e.s.conveyorRun(ctx, 0) // waiting for the preferred group: stays a candidate
	r := onlyRelease(t, e, sh.ID)
	if r.Status != store.RelCandidate {
		t.Fatalf("want candidate, got %s", r.Status)
	}
	got, err := e.s.ForceAdd(ctx, r.ID) // manual mode
	if err != nil || got.Status != store.RelChosen || got.Magnet == "" {
		t.Fatalf("manual ForceAdd: %+v %v", got, err)
	}
	e.update(t, func(c *settings.Settings) { c.Downloader.Mode = settings.ModeMagnet })
	got, err = e.s.ForceAdd(ctx, r.ID)
	if err != nil || got.Status != store.RelAdded || len(e.s.launch.(*fakeLauncher).urls) != 1 {
		t.Fatalf("magnet ForceAdd: %+v %v", got, err)
	}
}

func TestDownloaderStateAndOpenFolder(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.s.SetMagnetHandler(func() (string, error) { return "", ErrNoMagnetHandler })
	if mode, ok, _ := e.s.DownloaderState(ctx); mode != "manual" || !ok {
		t.Fatalf("manual: %s %v", mode, ok)
	}
	e.update(t, func(c *settings.Settings) { c.Downloader.Mode = settings.ModeMagnet })
	if _, ok, msg := e.s.DownloaderState(ctx); ok || msg != "no app is registered for magnet links" {
		t.Fatalf("magnet without handler: %v %q", ok, msg)
	}
	watch := filepath.Join(filepath.Dir(e.root), "watch")
	os.MkdirAll(watch, 0o755)
	e.update(t, func(c *settings.Settings) {
		c.Downloader.Mode = settings.ModeTorrentFile
		c.Downloader.TorrentDir = watch
	})
	if _, ok, _ := e.s.DownloaderState(ctx); !ok {
		t.Fatal("torrent_file with an existing folder should be ok")
	}
	os.Remove(watch)
	if _, ok, msg := e.s.DownloaderState(ctx); ok || !strings.Contains(msg, "not reachable") {
		t.Fatalf("torrent_file with a missing folder: %v %q", ok, msg)
	}
	fl := e.s.launch.(*fakeLauncher)
	if err := e.s.OpenFolder(e.root); err != nil || len(fl.dirs) != 1 || fl.dirs[0] != e.root {
		t.Fatalf("OpenFolder: %v %v", err, fl.dirs)
	}
	if err := e.s.OpenFolder(watch); !errors.Is(err, ErrFolderMissing) || len(fl.dirs) != 1 {
		t.Fatalf("OpenFolder on a missing folder: %v %v", err, fl.dirs)
	}
}

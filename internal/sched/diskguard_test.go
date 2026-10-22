package sched

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/events"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// fakeDisk replaces Sched.diskFree: a settable free figure plus the paths read.
type fakeDisk struct {
	mu    sync.Mutex
	free  int64
	err   error
	paths []string
}

func (d *fakeDisk) read(p string) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.paths = append(d.paths, p)
	return d.free, d.err
}

func (d *fakeDisk) set(free int64, err error) {
	d.mu.Lock()
	d.free, d.err = free, err
	d.mu.Unlock()
}

func (d *fakeDisk) reads() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.paths...)
}

func waitingEvents(t *testing.T, e *env, sub string) []store.Event {
	t.Helper()
	evs, _ := e.db.ListEvents(context.Background(), 0, 1000)
	var out []store.Event
	for _, ev := range evs {
		if ev.Kind == events.ReleaseWaiting && strings.Contains(ev.Message, sub) {
			out = append(out, ev)
		}
	}
	return out
}

// SPEC v1.1 section 10: the conveyor skips the hand-off below min_free_gib and
// logs once; ForceAdd and Download all missing ignore the guard.
func TestFreeSpaceGuard(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	q, url := newFakeQbit(t)
	e.update(t, func(c *settings.Settings) { c.Downloader.Mode = settings.ModeQbit; c.Qbit.URL = url })
	disk := &fakeDisk{free: 6 << 30} // 6 GiB free, release 1.4 GiB, keeps 5 GiB (default)
	e.s.diskFree = disk.read
	sh := e.show(t, store.Show{TitleRomaji: "Frieren", Status: "RELEASING"})
	at := e.clock.Add(-time.Hour).Unix()
	for n := 1; n <= 4; n++ {
		e.db.UpsertEpisodeAiring(ctx, sh.ID, n, &at)
	}
	size := int64(1400 << 20) // about 1.4 GiB
	item := func(id int64, ep, hash string) NyaaItem {
		return NyaaItem{ID: id, Title: "[SubsPlease] Frieren - " + ep + " (1080p).mkv", InfoHash: hash, Seeders: 9, PublishedAt: e.clock, SizeBytes: size}
	}
	e.brain.nyaa = []NyaaItem{item(1, "01", h1)}

	e.s.conveyorRun(ctx, 0)
	if q.count() != 0 {
		t.Fatalf("hand-off below the threshold: %d adds", q.count())
	}
	if st := statusByEp(t, e, sh.ID); st[1] != store.RelCandidate {
		t.Fatalf("ep 1 = %q, want candidate", st[1])
	}
	want := "Frieren ep 1: waiting, only 6.0 GiB free on "
	w := waitingEvents(t, e, want)
	if len(w) != 1 || !strings.HasSuffix(w[0].Message, " (keeps 5 GiB free)") {
		t.Fatalf("want one %q event, got %+v", want, w)
	}
	if r := disk.reads(); len(r) == 0 || r[0] != SavePath(e.set.Get(), sh) {
		t.Fatalf("free space read at %v, want the save path %q", r, SavePath(e.set.Get(), sh))
	}
	// Free space keeps shrinking: still one event (noteOnce keyed on the reason, not the figure).
	disk.set(5<<30, nil)
	e.s.conveyorRun(ctx, 0)
	e.s.conveyorRun(ctx, 0)
	if n := len(waitingEvents(t, e, "GiB free on ")); n != 1 || q.count() != 0 {
		t.Fatalf("waiting events = %d, adds = %d; want 1 and 0", n, q.count())
	}

	// ForceAdd ignores the guard.
	rels, _ := e.db.ListReleases(ctx, sh.ID, nil)
	if r, err := e.s.ForceAdd(ctx, rels[0].ID); err != nil || r.Status != store.RelAdded || q.count() != 1 {
		t.Fatalf("ForceAdd = %s %v, adds %d", r.Status, err, q.count())
	}

	// Download all missing ignores it too (ep 2, still 5 GiB free).
	e.brain.nyaa = append(e.brain.nyaa, item(2, "02", h2))
	e.s.conveyorRun(ctx, 0) // stores the candidate, the guard blocks the automatic add
	if q.count() != 1 {
		t.Fatalf("ep 2 added automatically below the threshold: %d adds", q.count())
	}
	if added, _, err := e.s.DownloadMissing(ctx, sh.ID); err != nil || added != 1 || q.count() != 2 {
		t.Fatalf("DownloadMissing added %d (%v), adds %d", added, err, q.count())
	}

	// Enough room: ep 3 goes automatically.
	e.brain.nyaa = append(e.brain.nyaa, item(3, "03", hashOf(3)))
	disk.set(100<<30, nil)
	e.s.conveyorRun(ctx, 0)
	if st := statusByEp(t, e, sh.ID); st[3] != store.RelAdded || q.count() != 3 {
		t.Fatalf("ep 3 = %q with room, adds %d", st[3], q.count())
	}

	// min_free_gib 0 = off.
	e.brain.nyaa = append(e.brain.nyaa, item(4, "04", hashOf(4)))
	disk.set(1<<20, nil)
	e.update(t, func(c *settings.Settings) { c.Conveyor.MinFreeGiB = 0 })
	e.s.conveyorRun(ctx, 0)
	if st := statusByEp(t, e, sh.ID); st[4] != store.RelAdded {
		t.Fatalf("min_free_gib 0: ep 4 = %q", st[4])
	}
}

func TestFreeSpaceGuardModes(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T, free int64, noFolder bool) (*env, *fakeDisk, store.Show) {
		e := newEnv(t)
		disk := &fakeDisk{free: free}
		e.s.diskFree = disk.read
		e.s.magnetHandler = func() (string, error) { return "torrent-app.exe", nil }
		e.update(t, func(c *settings.Settings) {
			c.Downloader.Mode = settings.ModeMagnet
			if noFolder {
				c.Library.Folders = nil
			}
		})
		sh := e.show(t, store.Show{TitleRomaji: "Frieren", Status: "RELEASING"})
		at := e.clock.Add(-time.Hour).Unix()
		e.db.UpsertEpisodeAiring(ctx, sh.ID, 1, &at)
		e.brain.nyaa = []NyaaItem{{ID: 1, Title: "[SubsPlease] Frieren - 01 (1080p).mkv", InfoHash: h1, Seeders: 9, PublishedAt: e.clock, SizeBytes: 1 << 30}}
		return e, disk, sh
	}

	// Magnet mode reads DownloadDir() and waits when it is low.
	e, disk, sh := setup(t, 2<<30, false)
	e.s.conveyorRun(ctx, 0)
	if st, r := statusByEp(t, e, sh.ID), disk.reads(); st[1] != store.RelCandidate || len(r) == 0 || r[0] != e.root {
		t.Fatalf("magnet low disk: ep 1 = %q, read %v (want %q)", st[1], r, e.root)
	}
	// An error reading the free space does not block the hand-off.
	disk.set(0, errors.New("unreadable"))
	e.s.conveyorRun(ctx, 0)
	if st := statusByEp(t, e, sh.ID); st[1] != store.RelAdded {
		t.Fatalf("unreadable free space: ep 1 = %q", st[1])
	}

	// Magnet mode without a download folder skips the check.
	e2, disk2, sh2 := setup(t, 1, true)
	e2.s.conveyorRun(ctx, 0)
	if st, r := statusByEp(t, e2, sh2.ID), disk2.reads(); st[1] != store.RelAdded || len(r) != 0 {
		t.Fatalf("no download folder: ep 1 = %q, reads %v", st[1], r)
	}

	// Manual mode hands nothing off, so nothing is checked.
	e3, disk3, sh3 := setup(t, 1, false)
	e3.update(t, func(c *settings.Settings) { c.Downloader.Mode = settings.ModeManual })
	e3.s.conveyorRun(ctx, 0)
	if st, r := statusByEp(t, e3, sh3.ID), disk3.reads(); st[1] != store.RelChosen || len(r) != 0 {
		t.Fatalf("manual mode: ep 1 = %q, reads %v", st[1], r)
	}
}

// size_preference reaches Pick through rulesFor.
func TestSizePreferenceReachesPick(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		pref string
		want int64 // nyaa id of the release that ends chosen
	}{{settings.SizeSmall, 2}, {settings.SizeAny, 1}} {
		e := newEnv(t)
		e.update(t, func(c *settings.Settings) { c.Conveyor.SizePreference = tc.pref })
		sh := e.show(t, store.Show{TitleRomaji: "Frieren", Status: "RELEASING"})
		at := e.clock.Add(-time.Hour).Unix()
		e.db.UpsertEpisodeAiring(ctx, sh.ID, 1, &at)
		e.brain.nyaa = []NyaaItem{
			{ID: 1, Title: "[SubsPlease] Frieren - 01 (1080p).mkv", InfoHash: h1, Seeders: 90, PublishedAt: e.clock, SizeBytes: 1500 << 20},
			{ID: 2, Title: "[SubsPlease] Frieren - 01 (1080p) [HEVC].mkv", InfoHash: h2, Seeders: 9, PublishedAt: e.clock, SizeBytes: 600 << 20},
		}
		e.s.conveyorRun(ctx, 0)
		rels, _ := e.db.ListReleases(ctx, sh.ID, nil)
		var got int64
		for _, r := range rels {
			if r.Status == store.RelChosen {
				got = r.NyaaID
			}
		}
		if got != tc.want {
			t.Errorf("size_preference %q: chosen nyaa id %d, want %d (%+v)", tc.pref, got, tc.want, rels)
		}
	}
}

func (f *fakeQbit) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.added)
}

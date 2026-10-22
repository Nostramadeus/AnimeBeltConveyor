package sched

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/events"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// SPEC v1.1 section 9: auto-download guard rails.

func TestAutoAllowedRules(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := settings.Defaults() // lookback_days 14
	day := int64(24 * 3600)
	recent := store.Ptr(now.Unix() - 2*day)
	old := store.Ptr(now.Unix() - 30*day)
	edge := store.Ptr(now.Unix() - 14*day) // exactly lookback_days ago: still inside
	floor := store.Ptr(5)
	cases := []struct {
		name   string
		e      store.Episode
		from   *int
		ok     bool
		reason string
	}{
		{"1 turned off beats everything", store.Episode{Number: 9, AiredAt: recent, Auto: store.Ptr(0)}, nil, false, "turned off"},
		{"1 turned off before the floor check", store.Episode{Number: 2, AiredAt: recent, Auto: store.Ptr(0)}, floor, false, "turned off"},
		{"2 below auto_from", store.Episode{Number: 4, AiredAt: recent}, floor, false, "before episode 5"},
		{"2 floor beats auto=1", store.Episode{Number: 4, AiredAt: recent, Auto: store.Ptr(1)}, floor, false, "before episode 5"},
		{"2 at auto_from is allowed", store.Episode{Number: 5, AiredAt: recent}, floor, true, ""},
		{"3 auto=1 even if old", store.Episode{Number: 1, AiredAt: old, Auto: store.Ptr(1)}, nil, true, ""},
		{"3 auto=1 without air date", store.Episode{Number: 1, Auto: store.Ptr(1)}, nil, true, ""},
		{"4 no air date", store.Episode{Number: 3}, nil, false, "no air date"},
		{"5 older than lookback", store.Episode{Number: 1, AiredAt: old}, nil, false, "older than 14 days"},
		{"5 edge is inside", store.Episode{Number: 1, AiredAt: edge}, nil, true, ""},
		{"6 recent", store.Episode{Number: 9, AiredAt: recent}, nil, true, ""},
	}
	for _, tc := range cases {
		ok, why := autoAllowed(tc.e, store.Show{AutoFrom: tc.from}, now, c)
		if ok != tc.ok || why != tc.reason {
			t.Errorf("%s: got (%v, %q), want (%v, %q)", tc.name, ok, why, tc.ok, tc.reason)
		}
	}
	c.Conveyor.LookbackDays = 1
	if _, why := autoAllowed(store.Episode{AiredAt: old}, store.Show{}, now, c); why != "older than 1 day" {
		t.Errorf("lookback 1: %q", why)
	}
}

func hashOf(n int) string { return fmt.Sprintf("%040x", n) }

// seventeen creates a RELEASING show whose episode n aired (17-n) weeks and one
// hour ago, with one SubsPlease 1080p candidate per episode uploaded an hour ago
// (recent uploads of old episodes are what started 6 downloads at once).
// sizes overrides a candidate's size in GiB (default 1.4); a size < 0 leaves the
// episode without a candidate.
func seventeen(t *testing.T, e *env, sizes map[int]float64) store.Show {
	t.Helper()
	ctx := context.Background()
	sh := e.show(t, store.Show{TitleRomaji: "Sousou no Frieren", Status: "RELEASING"})
	e.brain.nyaa = nil
	for n := 1; n <= 17; n++ {
		at := e.clock.Add(-time.Duration(17-n)*7*24*time.Hour - time.Hour).Unix()
		e.db.UpsertEpisodeAiring(ctx, sh.ID, n, &at)
		gib, ok := sizes[n]
		if !ok {
			gib = 1.4
		}
		if gib < 0 {
			continue
		}
		e.brain.nyaa = append(e.brain.nyaa, NyaaItem{ID: int64(100 + n), InfoHash: hashOf(n), Seeders: 10,
			Title:       fmt.Sprintf("[SubsPlease] Sousou no Frieren - %02d (1080p).mkv", n),
			PublishedAt: e.clock.Add(-time.Hour), SizeBytes: int64(gib * (1 << 30))})
	}
	return sh
}

func statusByEp(t *testing.T, e *env, showID int64) map[int]string {
	t.Helper()
	rels, _ := e.db.ListReleases(context.Background(), showID, nil)
	out := map[int]string{}
	for _, r := range rels {
		if r.Episode != nil {
			out[*r.Episode] = r.Status
		}
	}
	return out
}

func reasons(t *testing.T, e *env, sh store.Show) map[int]string {
	t.Helper()
	ctx := context.Background()
	eps, _ := e.db.ListEpisodes(ctx, sh.ID)
	rels, _ := e.db.ListReleases(ctx, sh.ID, nil)
	files, _ := e.db.FilesForShow(ctx, sh.ID)
	sh, _ = e.db.GetShow(ctx, sh.ID)
	ac := NewAutoContext(e.set.Get(), sh, eps, e.clock)
	out := map[int]string{}
	for _, ep := range eps {
		has := false
		for _, f := range files {
			has = has || (f.Present && f.Episode != nil && *f.Episode == ep.Number)
		}
		out[ep.Number] = ac.Reason(ep, has, rels)
	}
	return out
}

func TestConveyorOnlyRecentEpisodesOfANewShow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	q, url := newFakeQbit(t)
	e.update(t, func(c *settings.Settings) { c.Downloader.Mode = settings.ModeQbit; c.Qbit.URL = url })
	sh := seventeen(t, e, nil)
	if err := e.s.conveyorRun(ctx, 0); err != nil {
		t.Fatal(err)
	}
	st := statusByEp(t, e, sh.ID)
	if len(st) != 17 {
		t.Fatalf("want 17 stored candidates, got %d", len(st))
	}
	for n := 1; n <= 17; n++ {
		want := store.RelCandidate
		if n >= 16 { // aired 1 h and 7 days ago; ep 15 aired 14 days + 1 h ago
			want = store.RelAdded
		}
		if st[n] != want {
			t.Errorf("ep %d: %s, want %s", n, st[n], want)
		}
	}
	if len(q.added) != 2 {
		t.Fatalf("qBittorrent got %d torrents, want 2: %v", len(q.added), q.added)
	}
	k := e.kinds(t)
	if has(k, events.ReleaseWaiting) || has(k, events.ReleaseSkipped) {
		t.Fatalf("old episodes must stay silent: %v", k)
	}
	r := reasons(t, e, sh)
	if r[1] != "older than 14 days" || r[15] != "older than 14 days" || r[16] != "" || r[17] != "" {
		t.Fatalf("auto reasons = %v", r)
	}
	// A second pass adds nothing new.
	e.s.conveyorRun(ctx, 0)
	if len(q.added) != 2 {
		t.Fatalf("second pass added more: %v", q.added)
	}
	// auto_from 17: ep 16 would now be "before episode 17" (it is active, so "").
	sh.AutoFrom = store.Ptr(17)
	e.db.UpdateShowPrefs(ctx, &sh)
	if r := reasons(t, e, sh); r[3] != "before episode 17" || r[16] != "" {
		t.Fatalf("reasons with auto_from = %v", r)
	}
	// Turned on by hand, an old episode is downloaded on the next pass.
	sh.AutoFrom = nil
	e.db.UpdateShowPrefs(ctx, &sh)
	e.db.SetEpisodeAuto(ctx, sh.ID, 3, store.Ptr(1))
	e.s.conveyorRun(ctx, 0)
	if st := statusByEp(t, e, sh.ID); st[3] != store.RelAdded || st[4] != store.RelCandidate || len(q.added) != 3 {
		t.Fatalf("auto=1 episode not added: ep3 %s ep4 %s, %d adds", st[3], st[4], len(q.added))
	}
}

func TestChosenReleaseOfAnOldEpisodeIsNotResent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sh := seventeen(t, e, nil)
	e.s.conveyorRun(ctx, 0) // manual mode: 16 and 17 chosen
	rels, _ := e.db.ListReleases(ctx, sh.ID, nil)
	for _, r := range rels {
		if *r.Episode == 2 {
			e.db.SetReleaseQbit(ctx, r.ID, store.RelChosen, "", "") // chosen before the rule existed
		}
	}
	q, url := newFakeQbit(t)
	e.update(t, func(c *settings.Settings) { c.Downloader.Mode = settings.ModeQbit; c.Qbit.URL = url })
	e.s.conveyorRun(ctx, 0)
	if len(q.added) != 2 { // 16 and 17 re-sent, 2 is not
		t.Fatalf("adds = %v", q.added)
	}
	for _, a := range q.added {
		if strings.Contains(a, hashOf(2)) {
			t.Fatalf("chosen release of an old episode re-sent: %v", q.added)
		}
	}
}

func TestMaxReleaseSizeSkipReason(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sh := e.show(t, store.Show{TitleRomaji: "Frieren", Status: "RELEASING"})
	at := e.clock.Add(-time.Hour).Unix()
	e.db.UpsertEpisodeAiring(ctx, sh.ID, 1, &at)
	e.brain.nyaa = []NyaaItem{{ID: 1, Title: "[SubsPlease] Frieren - 01 (1080p).mkv", InfoHash: h1, Seeders: 9,
		PublishedAt: e.clock, SizeBytes: 7730941132}} // 7.2 GiB, default limit 4
	e.s.conveyorRun(ctx, 0)
	rels, _ := e.db.ListReleases(ctx, sh.ID, nil)
	if len(rels) != 1 || rels[0].Status != store.RelCandidate {
		t.Fatalf("too big release must stay a candidate: %+v", rels)
	}
	evs, _ := e.db.ListEvents(ctx, 0, 100)
	found := false
	for _, ev := range evs {
		found = found || (ev.Kind == events.ReleaseSkipped && strings.Contains(ev.Message, "too big: 7.2 GiB"))
	}
	if !found {
		t.Fatalf("no release.skipped with the size reason: %+v", evs)
	}
	// ForceAdd is manual and ignores the limit.
	if r, err := e.s.ForceAdd(ctx, rels[0].ID); err != nil || r.Status != store.RelChosen {
		t.Fatalf("ForceAdd = %s %v", r.Status, err)
	}
	// 0 = no limit: a second too-big episode is picked.
	e.update(t, func(c *settings.Settings) { c.Conveyor.MaxReleaseGiB = 0 })
	e.db.UpsertEpisodeAiring(ctx, sh.ID, 2, &at)
	e.brain.nyaa = append(e.brain.nyaa, NyaaItem{ID: 2, Title: "[SubsPlease] Frieren - 02 (1080p).mkv", InfoHash: h2, Seeders: 9,
		PublishedAt: e.clock, SizeBytes: 9 << 30})
	e.s.conveyorRun(ctx, 0)
	if st := statusByEp(t, e, sh.ID); st[2] != store.RelChosen {
		t.Fatalf("no limit: ep 2 = %s", st[2])
	}
}

func TestRemovedFromQbitTurnsAutoOff(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	q, url := newFakeQbit(t)
	e.update(t, func(c *settings.Settings) { c.Downloader.Mode = settings.ModeQbit; c.Qbit.URL = url })
	sh := e.show(t, store.Show{TitleRomaji: "Frieren", Status: "RELEASING"})
	at := e.clock.Add(-time.Hour).Unix()
	e.db.UpsertEpisodeAiring(ctx, sh.ID, 1, &at)
	e.db.UpsertEpisodeAiring(ctx, sh.ID, 2, &at)
	h3 := hashOf(3)
	e.brain.nyaa = []NyaaItem{
		{ID: 1, Title: "[SubsPlease] Frieren - 01 (1080p).mkv", InfoHash: h1, Seeders: 9, PublishedAt: e.clock},
		{ID: 3, Title: "[SubsPlease] Frieren - 02 (1080p).mkv", InfoHash: h3, Seeders: 9, PublishedAt: e.clock},
	}
	e.s.conveyorRun(ctx, 0)
	if len(q.added) != 2 {
		t.Fatalf("adds = %v", q.added)
	}
	// ep 1: the user removed the torrent (qBittorrent no longer knows the hash).
	// ep 2: qBittorrent itself reports an error.
	q.mu.Lock()
	q.state[h3], q.status[h3] = 0.3, "error"
	q.mu.Unlock()
	if err := e.s.qbitRun(ctx); err != nil {
		t.Fatal(err)
	}
	ep1, _ := e.db.GetEpisode(ctx, sh.ID, 1)
	ep2, _ := e.db.GetEpisode(ctx, sh.ID, 2)
	if ep1.Auto == nil || *ep1.Auto != 0 {
		t.Fatalf("removed torrent: auto = %v, want 0", ep1.Auto)
	}
	if ep2.Auto != nil {
		t.Fatalf("errored torrent must not turn auto off: %v", *ep2.Auto)
	}
	evs, _ := e.db.ListEvents(ctx, 0, 100)
	var failed, skipped []string
	for _, ev := range evs {
		switch ev.Kind {
		case events.DownloadFailed:
			failed = append(failed, ev.Message)
		case events.ReleaseSkipped:
			skipped = append(skipped, ev.Message)
		}
	}
	// SPEC v1.3 section 2.1: removed in the client = release.skipped, not download.failed.
	if !strings.Contains(strings.Join(skipped, "\n"), "Frieren ep 1: torrent is no longer in qBittorrent; not downloading it again (press Add to retry)") ||
		strings.Join(failed, "\n") != "Frieren ep 2: qBittorrent reports error" {
		t.Fatalf("release.skipped = %q, download.failed = %q", skipped, failed)
	}
	if rels, _ := e.db.ListReleases(ctx, sh.ID, store.Ptr(1)); len(rels) != 1 || rels[0].Status != store.RelSkipped || rels[0].StatusNote != "removed in qBittorrent" {
		t.Fatalf("removed release = %+v", rels)
	}
	if rels, _ := e.db.ListReleases(ctx, sh.ID, store.Ptr(2)); len(rels) != 1 || rels[0].Status != store.RelFailed {
		t.Fatalf("errored release = %+v", rels)
	}
	if r := reasons(t, e, sh); r[1] != AutoRemoved || r[2] != "" {
		t.Fatalf("reasons = %v", r)
	}
	// A new candidate for ep 1 appears: the next pass adds nothing for it.
	e.brain.nyaa = append(e.brain.nyaa, NyaaItem{ID: 2, Title: "[SubsPlease] Frieren - 01v2 (1080p).mkv", InfoHash: h2, Seeders: 9, PublishedAt: e.clock})
	e.s.conveyorRun(ctx, 0)
	if len(q.added) != 2 {
		t.Fatalf("removed episode re-added: %v", q.added)
	}
	// Press Add: ForceAdd works and leaves auto at 0.
	rels, _ := e.db.ListReleases(ctx, sh.ID, store.Ptr(1))
	var cand int64
	for _, r := range rels {
		if r.Status == store.RelCandidate {
			cand = r.ID
		}
	}
	if r, err := e.s.ForceAdd(ctx, cand); err != nil || r.Status != store.RelAdded || len(q.added) != 3 {
		t.Fatalf("ForceAdd = %s %v, adds %d", r.Status, err, len(q.added))
	}
	if ep1, _ := e.db.GetEpisode(ctx, sh.ID, 1); ep1.Auto == nil || *ep1.Auto != 0 {
		t.Fatalf("ForceAdd changed auto: %v", ep1.Auto)
	}
	if r := reasons(t, e, sh); r[1] != "" {
		t.Fatalf("active release: reason = %q", r[1])
	}
}

func TestRemovedBatchTurnsOffItsRange(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sh := e.show(t, store.Show{TitleRomaji: "Frieren", Status: "FINISHED"})
	e.db.EnsureEpisodes(ctx, sh.ID, 5)
	e.s.turnOffAuto(ctx, store.Release{ShowID: sh.ID, IsBatch: true, Episode: store.Ptr(2), EpisodeEnd: store.Ptr(3)})
	eps, _ := e.db.ListEpisodes(ctx, sh.ID)
	for _, ep := range eps {
		off := ep.Auto != nil && *ep.Auto == 0
		if off != (ep.Number == 2 || ep.Number == 3) {
			t.Errorf("ep %d auto = %v", ep.Number, ep.Auto)
		}
	}
}

func TestDownloadMissing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	q, url := newFakeQbit(t)
	e.update(t, func(c *settings.Settings) { c.Downloader.Mode = settings.ModeQbit; c.Qbit.URL = url })
	// ep 4 has no candidate, ep 5's only candidate is bigger than max_release_gib.
	sh := seventeen(t, e, map[int]float64{4: -1, 5: 9})
	e.s.conveyorRun(ctx, 0)                                  // adds 16 and 17
	e.db.SetWatched(ctx, sh.ID, 2, e.clock.Unix(), "import") // watched without a file: not missing
	count, bytes, err := e.s.DownloadMissingPreview(ctx, sh.ID)
	// 1..15 minus 2 (watched) and 4 (no candidate) = 13; ep 5 counts (no size limit here).
	small := 1.4
	wantBytes := 12*int64(small*(1<<30)) + 9<<30
	if err != nil || count != 13 || bytes != wantBytes {
		t.Fatalf("preview = %d %d %v, want 13 %d", count, bytes, err, wantBytes)
	}
	if len(q.added) != 2 {
		t.Fatalf("preview had side effects: %v", q.added)
	}
	added, skipped, err := e.s.DownloadMissing(ctx, sh.ID)
	if err != nil || added != 13 || skipped != 1 {
		t.Fatalf("download missing = %d %d %v, want 13 1", added, skipped, err)
	}
	if len(q.added) != 15 {
		t.Fatalf("qBittorrent got %d, want 15", len(q.added))
	}
	st := statusByEp(t, e, sh.ID)
	if st[1] != store.RelAdded || st[5] != store.RelAdded || st[2] != store.RelCandidate {
		t.Fatalf("statuses = %v", st)
	}
	evs, _ := e.db.ListEvents(ctx, 0, 200)
	bulk := 0
	for _, ev := range evs {
		if ev.Kind == events.ReleaseAdded && strings.Contains(string(ev.Data), `"bulk":true`) {
			bulk++
			if ev.Message != "Download all missing: 13 added, 1 without a release" {
				t.Errorf("summary = %q", ev.Message)
			}
		}
	}
	if bulk != 1 {
		t.Fatalf("want one bulk summary event, got %d", bulk)
	}
	if n, _, _ := e.s.DownloadMissingPreview(ctx, sh.ID); n != 0 {
		t.Fatalf("preview after = %d, want 0", n)
	}
	if _, _, err := e.s.DownloadMissing(ctx, 999); err == nil {
		t.Fatal("unknown show should fail")
	}
}

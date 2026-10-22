package sched

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/events"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

func (e *env) queued() int {
	e.s.push.mu.Lock()
	defer e.s.push.mu.Unlock()
	return e.s.push.queued
}

func (e *env) pushCount() int {
	e.brain.mu.Lock()
	defer e.brain.mu.Unlock()
	return len(e.brain.pushes)
}

// SPEC v1.1 section 11: watched-through marks only unwatched episodes, never
// unmarks, queues one push, logs one event, and checks its bounds.
func TestWatchedThrough(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sh := e.show(t, store.Show{TitleRomaji: "Juukishi", AniListID: 5, EpisodesTotal: store.Ptr(12), ListStatus: "watching"})
	e.update(t, func(c *settings.Settings) { c.Push.AniList = true })
	e.db.UpsertAccount(ctx, store.Account{Service: "anilist", AccessToken: "tok"})
	early := e.clock.Add(-time.Hour).Unix()
	e.db.SetWatched(ctx, sh.ID, 3, early, "player")
	e.db.SetWatched(ctx, sh.ID, 12, early, "import") // beyond the range: must stay watched
	e.db.EnsureEpisodes(ctx, sh.ID, 4)

	newly, err := e.s.WatchedThrough(ctx, sh.ID, 9)
	if err != nil || newly != 8 {
		t.Fatalf("WatchedThrough(9) = %d, %v; want 8 new", newly, err)
	}
	eps, _ := e.db.ListEpisodes(ctx, sh.ID)
	for _, ep := range eps {
		switch {
		case ep.Number == 3:
			if ep.WatchedAt == nil || *ep.WatchedAt != early || ep.WatchedSource != "player" {
				t.Fatalf("ep 3 was rewritten: %+v", ep)
			}
		case ep.Number <= 9:
			if ep.WatchedAt == nil || *ep.WatchedAt != e.clock.Unix() || ep.WatchedSource != "manual" {
				t.Fatalf("ep %d = %+v, want watched now by manual", ep.Number, ep)
			}
		case ep.Number == 12:
			if ep.WatchedAt == nil {
				t.Fatal("ep 12 was unmarked")
			}
		default:
			if ep.WatchedAt != nil {
				t.Fatalf("ep %d marked: %+v", ep.Number, ep)
			}
		}
	}

	// one event with the SPEC message and data
	evs, _ := e.db.ListEvents(ctx, 0, 100)
	var watched []store.Event
	for _, ev := range evs {
		if ev.Kind == events.EpisodeWatched {
			watched = append(watched, ev)
		}
	}
	if len(watched) != 1 {
		t.Fatalf("episode.watched events = %d, want 1", len(watched))
	}
	ev := watched[0]
	if ev.Message != "Juukishi: episodes 1-9 marked watched (8 new)" || !strings.Contains(string(ev.Data), `"bulk":true`) ||
		!strings.Contains(string(ev.Data), `"through":9`) || !strings.Contains(string(ev.Data), `"newly":8`) {
		t.Fatalf("event = %q %s", ev.Message, ev.Data)
	}

	// one push, with progress = the highest watched number (12 here)
	if q := e.queued(); q != 1 {
		t.Fatalf("push enqueued %d times, want 1", q)
	}
	e.s.push.flush(ctx)
	if e.pushCount() != 1 || e.brain.pushes[0] != "tok:5:12" {
		t.Fatalf("pushes = %v", e.brain.pushes)
	}

	// a lower or equal number changes nothing: no unmark, no event, no push
	for _, n := range []int{5, 9} {
		if newly, err := e.s.WatchedThrough(ctx, sh.ID, n); err != nil || newly != 0 {
			t.Fatalf("WatchedThrough(%d) again = %d, %v", n, newly, err)
		}
	}
	if ep, _ := e.db.GetEpisode(ctx, sh.ID, 9); ep.WatchedAt == nil {
		t.Fatal("ep 9 unmarked by a lower number")
	}
	if n := count(e.kinds(t), events.EpisodeWatched); n != 1 || e.queued() != 1 {
		t.Fatalf("no-op logged %d events, queued %d pushes", n, e.queued())
	}

	// bounds: 1 .. episodes_total + 50
	for _, n := range []int{0, -3, 63} {
		_, err := e.s.WatchedThrough(ctx, sh.ID, n)
		var te *ThroughError
		if !errors.As(err, &te) {
			t.Fatalf("WatchedThrough(%d) err = %v, want ThroughError", n, err)
		}
	}
	if ep, _ := e.db.GetEpisode(ctx, sh.ID, 10); ep.WatchedAt != nil {
		t.Fatal("a rejected call wrote episodes")
	}
	if newly, err := e.s.WatchedThrough(ctx, sh.ID, 62); err != nil || newly != 52 { // 10, 11, 13..62
		t.Fatalf("WatchedThrough(62) = %d, %v", newly, err)
	}
	if _, err := e.s.WatchedThrough(ctx, 999, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown show err = %v", err)
	}
}

// Without episodes_total the bound is the highest known episode (row, present
// file or next airing episode) + 50.
func TestWatchedThroughUnknownTotal(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sh := e.show(t, store.Show{TitleRomaji: "Long Runner", Status: "RELEASING"})
	e.db.EnsureEpisodes(ctx, sh.ID, 4)
	var te *ThroughError
	if _, err := e.s.WatchedThrough(ctx, sh.ID, 55); !errors.As(err, &te) {
		t.Fatalf("55 with 4 known episodes: err = %v", err)
	}
	if newly, err := e.s.WatchedThrough(ctx, sh.ID, 54); err != nil || newly != 54 {
		t.Fatalf("54 = %d, %v", newly, err)
	}
	e.db.UpsertFiles(ctx, []store.File{{Path: e.root + "/Long Runner - 70.mkv", ShowID: &sh.ID, Episode: store.Ptr(70)}})
	if _, err := e.s.WatchedThrough(ctx, sh.ID, 121); !errors.As(err, &te) {
		t.Fatalf("121 with a file for 70: err = %v", err)
	}
	if _, err := e.s.WatchedThrough(ctx, sh.ID, 120); err != nil {
		t.Fatalf("120 with a file for 70: %v", err)
	}
}

// GET /api/missing's set: aired, not watched, no present file, no active
// release; only shows with list_status watching.
func TestMissing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	past := e.clock.Add(-48 * time.Hour).Unix()
	future := e.clock.Add(48 * time.Hour).Unix()
	airAll := func(sh store.Show, upTo int) {
		for n := 1; n <= upTo; n++ {
			e.db.UpsertEpisodeAiring(ctx, sh.ID, n, &past)
		}
	}
	rel := func(sh store.Show, ep, end int, batch bool, status string) {
		t.Helper()
		r := store.Release{ShowID: sh.ID, NyaaID: int64(sh.ID*100 + int64(ep)), Title: "r", Episode: store.Ptr(ep), IsBatch: batch, PublishedAt: past}
		if batch {
			r.EpisodeEnd = store.Ptr(end)
		}
		if _, err := e.db.UpsertRelease(ctx, &r); err != nil {
			t.Fatal(err)
		}
		if err := e.db.SetReleaseStatus(ctx, r.ID, status, ""); err != nil {
			t.Fatal(err)
		}
	}

	a := e.show(t, store.Show{TitleRomaji: "A", ListStatus: "watching", Status: "RELEASING"})
	airAll(a, 8)
	e.db.UpsertEpisodeAiring(ctx, a.ID, 9, &future)
	e.db.SetWatched(ctx, a.ID, 1, past, "manual")                                                             // watched
	e.db.UpsertFiles(ctx, []store.File{{Path: e.root + "/A - 02.mkv", ShowID: &a.ID, Episode: store.Ptr(2)}}) // on disk
	rel(a, 3, 0, false, store.RelAdded)
	rel(a, 4, 0, false, store.RelDownloading)
	rel(a, 5, 5, true, store.RelDone) // batch 5-5 done
	rel(a, 6, 0, false, store.RelChosen)
	rel(a, 7, 0, false, store.RelFailed)
	// 8: nothing at all; 9: not aired

	b := e.show(t, store.Show{TitleRomaji: "B", ListStatus: "planning", Status: "RELEASING"})
	airAll(b, 3)
	c := e.show(t, store.Show{TitleRomaji: "C", ListStatus: "watching", Status: "RELEASING"})
	airAll(c, 2)
	e.db.SetWatched(ctx, c.ID, 1, past, "manual")
	e.db.SetWatched(ctx, c.ID, 2, past, "manual")
	d := e.show(t, store.Show{TitleRomaji: "D", ListStatus: "watching", Status: "FINISHED"})
	e.db.EnsureEpisodes(ctx, d.ID, 2) // finished, undated: counts as aired

	got, err := e.s.Missing(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := map[int64][]int{}
	for _, x := range got {
		m[x.Show.ID] = x.Episodes
	}
	if len(got) != 2 || !slices.Equal(m[a.ID], []int{6, 7, 8}) || !slices.Equal(m[d.ID], []int{1, 2}) {
		t.Fatalf("Missing = %+v", m)
	}
}

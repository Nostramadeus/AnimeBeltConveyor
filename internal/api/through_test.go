package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// SPEC v1.1 section 11: POST /api/shows/{id}/watched-through, GET /api/missing
// and /api/state.counts.missing.
func TestWatchedThroughAndMissingRoutes(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	past := time.Now().Add(-48 * time.Hour).Unix()
	future := time.Now().Add(48 * time.Hour).Unix()
	mk := func(title string, total int, aired int) store.Show {
		t.Helper()
		sh := store.Show{TitleRomaji: title, ListStatus: "watching", Status: "RELEASING", EpisodesTotal: store.Ptr(total), Color: "#123456", CoverURL: "https://x/" + title}
		if err := e.db.CreateShow(ctx, &sh); err != nil {
			t.Fatal(err)
		}
		for n := 1; n <= total; n++ {
			at := past
			if n > aired {
				at = future
			}
			e.db.UpsertEpisodeAiring(ctx, sh.ID, n, &at)
		}
		return sh
	}
	a := mk("Juukishi", 12, 13) // all 12 aired
	b := mk("Other", 4, 3)      // 1-3 aired
	e.db.UpsertFiles(ctx, []store.File{{Path: e.root + "/Juukishi - 08.mkv", ShowID: &a.ID, Episode: store.Ptr(8)}})
	e.db.SetWatched(ctx, b.ID, 1, time.Now().Add(time.Hour).Unix(), "manual") // b stays the most recently active show

	type missingRow struct {
		ShowID       int64  `json:"show_id"`
		Title        string `json:"title"`
		CoverURL     string `json:"cover_url"`
		Color        string `json:"color"`
		Count        int    `json:"count"`
		FirstEpisode int    `json:"first_episode"`
		LastEpisode  int    `json:"last_episode"`
	}
	getMissing := func() []missingRow {
		t.Helper()
		code, body := e.do(t, "GET", "/api/missing", nil)
		if code != 200 {
			t.Fatalf("missing -> %d %s", code, body)
		}
		return decodeT[[]missingRow](t, body)
	}
	stateMissing := func() int {
		t.Helper()
		code, body := e.do(t, "GET", "/api/state", nil)
		if code != 200 {
			t.Fatalf("state -> %d %s", code, body)
		}
		return decodeT[struct {
			Counts struct {
				Missing int `json:"missing"`
			} `json:"counts"`
		}](t, body).Counts.Missing
	}

	m := getMissing()
	if len(m) != 2 || m[0].ShowID != b.ID || m[0].Count != 2 || m[0].FirstEpisode != 2 || m[0].LastEpisode != 3 ||
		m[1].ShowID != a.ID || m[1].Count != 11 || m[1].FirstEpisode != 1 || m[1].LastEpisode != 12 ||
		m[1].Title != "Juukishi" || m[1].Color != "#123456" || m[1].CoverURL != "https://x/Juukishi" {
		t.Fatalf("missing = %+v", m)
	}
	if n := stateMissing(); n != 13 {
		t.Fatalf("state counts.missing = %d, want 13", n)
	}

	id := itoa(a.ID)
	code, body := e.do(t, "POST", "/api/shows/"+id+"/watched-through", map[string]any{"number": 9})
	if code != 200 {
		t.Fatalf("watched-through -> %d %s", code, body)
	}
	d := decodeT[struct {
		Show struct {
			ID       int64 `json:"id"`
			Progress struct {
				Watched int `json:"watched"`
			} `json:"progress"`
		} `json:"show"`
		Episodes []struct {
			Number        int    `json:"number"`
			WatchedAt     *int64 `json:"watched_at"`
			WatchedSource string `json:"watched_source"`
		} `json:"episodes"`
		Files []any `json:"files"`
	}](t, body)
	if d.Show.ID != a.ID || d.Show.Progress.Watched != 9 || len(d.Episodes) != 12 || len(d.Files) != 1 {
		t.Fatalf("detail = %s", body)
	}
	for _, ep := range d.Episodes {
		if (ep.WatchedAt != nil) != (ep.Number <= 9) || (ep.Number <= 9 && ep.WatchedSource != "manual") {
			t.Fatalf("ep %+v", ep)
		}
	}
	if m := getMissing(); len(m) != 2 || m[1].Count != 3 || m[1].FirstEpisode != 10 || m[1].LastEpisode != 12 {
		t.Fatalf("missing after = %+v", m)
	}
	if n := stateMissing(); n != 5 {
		t.Fatalf("state counts.missing after = %d, want 5", n)
	}
	evs, _ := e.db.ListEvents(ctx, 0, 50)
	if len(evs) == 0 || evs[0].Message != "Juukishi: episodes 1-9 marked watched (9 new)" {
		t.Fatalf("latest event = %+v", evs)
	}

	// bounds and bad bodies: 400 with fields.number
	for _, bad := range []any{map[string]any{"number": 0}, map[string]any{"number": 63}, map[string]any{"number": "9"},
		map[string]any{"number": 2.5}, map[string]any{}} {
		code, body := e.do(t, "POST", "/api/shows/"+id+"/watched-through", bad)
		if code != 400 || !strings.Contains(string(body), `"number"`) {
			t.Fatalf("body %v -> %d %s", bad, code, body)
		}
	}
	if code, _ := e.do(t, "POST", "/api/shows/"+id+"/watched-through", map[string]any{"number": 62}); code != 200 {
		t.Fatalf("number 62 (total + 50) -> %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/shows/999/watched-through", map[string]any{"number": 1}); code != 404 {
		t.Fatalf("unknown show -> %d", code)
	}

	// nothing missing: empty array, not null
	e.do(t, "POST", "/api/shows/"+itoa(b.ID)+"/watched-through", map[string]any{"number": 4})
	if code, body := e.do(t, "GET", "/api/missing", nil); code != 200 || strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("empty missing -> %d %s", code, body)
	}
	if n := stateMissing(); n != 0 {
		t.Fatalf("state counts.missing at the end = %d", n)
	}
}

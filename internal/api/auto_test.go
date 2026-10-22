package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// SPEC v1.1 section 9: auto_from, the episode auto switch, auto/auto_reason on
// EpisodeRow and the download-missing preview + action.
func TestAutoDownloadRoutes(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	sh := store.Show{TitleRomaji: "Frieren", Status: "RELEASING"}
	if err := e.db.CreateShow(ctx, &sh); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for n, ago := range map[int]time.Duration{1: 30 * 24 * time.Hour, 2: 20 * 24 * time.Hour, 3: -7 * 24 * time.Hour} {
		at := now.Add(-ago).Unix()
		e.db.UpsertEpisodeAiring(ctx, sh.ID, n, &at)
	}
	for n, gib := range map[int]int64{1: 1, 2: 2} {
		hash := strings.Repeat(string(rune('0'+n)), 40)
		rel := store.Release{ShowID: sh.ID, NyaaID: int64(n), Title: "[SubsPlease] Frieren - 0" + itoa(int64(n)) + " (1080p).mkv",
			InfoHash: hash, Magnet: "magnet:?xt=urn:btih:" + hash, ReleaseGroup: "SubsPlease", Resolution: "1080p", Version: 1,
			Episode: store.Ptr(n), Seeders: store.Ptr(9), SizeBytes: store.Ptr(gib << 30), PublishedAt: now.Unix()}
		if _, err := e.db.UpsertRelease(ctx, &rel); err != nil {
			t.Fatal(err)
		}
	}
	id := itoa(sh.ID)
	type row struct {
		Number     int    `json:"number"`
		Auto       *int   `json:"auto"`
		AutoReason string `json:"auto_reason"`
	}
	type detail struct {
		Show struct {
			AutoFrom *int `json:"auto_from"`
		} `json:"show"`
		Episodes []row `json:"episodes"`
	}
	get := func() detail {
		t.Helper()
		code, b := e.do(t, "GET", "/api/shows/"+id, nil)
		if code != 200 {
			t.Fatalf("get -> %d %s", code, b)
		}
		return decodeT[detail](t, b)
	}
	d := get()
	if d.Show.AutoFrom != nil || len(d.Episodes) != 3 {
		t.Fatalf("detail = %+v", d)
	}
	if ep := d.Episodes[0]; ep.Auto != nil || ep.AutoReason != "older than 14 days" {
		t.Fatalf("ep 1 = %+v", ep)
	}
	if ep := d.Episodes[2]; ep.AutoReason != "" {
		t.Fatalf("future ep 3 = %+v", ep)
	}

	// PATCH auto_from
	code, b := e.do(t, "PATCH", "/api/shows/"+id, map[string]any{"auto_from": 2})
	if code != 200 || !strings.Contains(string(b), `"auto_from":2`) {
		t.Fatalf("patch auto_from -> %d %s", code, b)
	}
	e.sch.WaitIdle()
	if ep := get().Episodes[0]; ep.AutoReason != "before episode 2" {
		t.Fatalf("ep 1 with auto_from 2 = %+v", ep)
	}
	if code, b := e.do(t, "PATCH", "/api/shows/"+id, map[string]any{"auto_from": 0}); code != 400 || !strings.Contains(string(b), `"auto_from"`) {
		t.Fatalf("auto_from 0 -> %d %s", code, b)
	}
	if code, b := e.do(t, "PATCH", "/api/shows/"+id, map[string]any{"auto_from": nil}); code != 200 || !strings.Contains(string(b), `"auto_from":null`) {
		t.Fatalf("auto_from null -> %d %s", code, b)
	}
	e.sch.WaitIdle()

	// POST episodes/{n}/auto
	code, b = e.do(t, "POST", "/api/shows/"+id+"/episodes/1/auto", map[string]any{"auto": 0})
	if r := decodeT[row](t, b); code != 200 || r.Number != 1 || r.Auto == nil || *r.Auto != 0 || r.AutoReason != "turned off" {
		t.Fatalf("auto 0 -> %d %s", code, b)
	}
	for _, bad := range []any{map[string]any{"auto": 5}, map[string]any{"auto": "yes"}, map[string]any{}} {
		if code, b := e.do(t, "POST", "/api/shows/"+id+"/episodes/1/auto", bad); code != 400 || !strings.Contains(string(b), `"auto"`) {
			t.Fatalf("bad body %v -> %d %s", bad, code, b)
		}
	}
	if code, _ := e.do(t, "POST", "/api/shows/999/episodes/1/auto", map[string]any{"auto": 1}); code != 404 {
		t.Fatalf("unknown show -> %d", code)
	}

	// download-missing preview: eps 1 and 2 (3 has not aired), 1 + 2 GiB.
	code, b = e.do(t, "GET", "/api/shows/"+id+"/download-missing", nil)
	prev := decodeT[struct {
		Count int   `json:"count"`
		Bytes int64 `json:"bytes"`
	}](t, b)
	if code != 200 || prev.Count != 2 || prev.Bytes != 3<<30 {
		t.Fatalf("preview -> %d %s", code, b)
	}
	code, b = e.do(t, "POST", "/api/shows/"+id+"/download-missing", nil)
	res := decodeT[struct {
		Added   int `json:"added"`
		Skipped int `json:"skipped"`
	}](t, b)
	if code != 200 || res.Added != 2 || res.Skipped != 0 {
		t.Fatalf("download-missing -> %d %s", code, b)
	}
	rels, _ := e.db.ListReleases(ctx, sh.ID, nil)
	for _, r := range rels {
		if r.Status != store.RelChosen { // manual mode
			t.Fatalf("release %d = %s, want chosen", r.ID, r.Status)
		}
	}
	if code, b := e.do(t, "GET", "/api/shows/"+id+"/download-missing", nil); code != 200 || !strings.Contains(string(b), `"count":0`) {
		t.Fatalf("preview after -> %d %s", code, b)
	}
	if code, _ := e.do(t, "GET", "/api/shows/999/download-missing", nil); code != 404 {
		t.Fatalf("unknown show preview -> %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/shows/999/download-missing", nil); code != 404 {
		t.Fatalf("unknown show download-missing -> %d", code)
	}

	// back to the rule (null) queues a conveyor pass; the reply carries auto null.
	code, b = e.do(t, "POST", "/api/shows/"+id+"/episodes/1/auto", map[string]any{"auto": nil})
	e.sch.WaitIdle()
	if r := decodeT[row](t, b); code != 200 || r.Auto != nil {
		t.Fatalf("auto null -> %d %s", code, b)
	}
}

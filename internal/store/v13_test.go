package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// SPEC v1.3: migrate_3.sql adds shows.abs_offset and releases.status_at to a
// v1.2 (schema_version 2) database and turns "removed from qBittorrent" failures
// into skipped rows; other failures stay failed.
func TestMigrate3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abc.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"schema.sql", "migrate_2.sql"} {
		b, _ := sqlFS.ReadFile(f)
		if _, err := raw.Exec(string(b)); err != nil {
			t.Fatal(f, err)
		}
	}
	for _, q := range []string{
		`INSERT INTO meta(key,value) VALUES('schema_version','2')`,
		`INSERT INTO shows(id, title_romaji, episode_offset, added_at, updated_at) VALUES(7, 'Frieren', 0, 1, 1)`,
		`INSERT INTO releases(id, show_id, episode, title, published_at, status, status_note, first_seen) VALUES(1, 7, 1, 'a', 1, 'failed', 'removed from qBittorrent', 100)`,
		`INSERT INTO releases(id, show_id, episode, title, published_at, status, status_note, first_seen) VALUES(2, 7, 2, 'b', 1, 'failed', 'qBittorrent state error', 200)`,
		`INSERT INTO releases(id, show_id, episode, title, published_at, status, first_seen) VALUES(3, 7, 3, 'c', 1, 'done', 300)`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	raw.Close()

	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if v, _, _ := d.Meta(ctx, "schema_version"); v != "3" {
		t.Fatalf("schema_version = %q, want 3", v)
	}
	sh, err := d.GetShow(ctx, 7)
	if err != nil || sh.AbsOffset != nil || EffectiveOffset(sh) != 0 {
		t.Fatalf("show after migrate = %+v %v", sh, err)
	}
	r1, _ := d.GetRelease(ctx, 1)
	r2, _ := d.GetRelease(ctx, 2)
	r3, _ := d.GetRelease(ctx, 3)
	if r1.Status != RelSkipped || r1.StatusNote != "removed in qBittorrent" {
		t.Fatalf("removed release = %s %q, want skipped 'removed in qBittorrent'", r1.Status, r1.StatusNote)
	}
	if r2.Status != RelFailed || r2.StatusNote != "qBittorrent state error" || r3.Status != RelDone {
		t.Fatalf("other rows changed: %+v %+v", r2, r3)
	}
	if r1.StatusAt != 0 || r2.StatusAt != 0 || r2.FirstSeen != 200 {
		t.Fatalf("old rows must keep status_at NULL (0): %d %d", r1.StatusAt, r2.StatusAt)
	}
	d.Close()
	d2, err := Open(path) // reopening must not re-run migrate_3 (duplicate column)
	if err != nil {
		t.Fatal(err)
	}
	d2.Close()
}

func TestAbsOffsetAndEffectiveOffset(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	s := Show{TitleRomaji: "Jujutsu Kaisen 2nd Season", AbsOffset: Ptr(24)}
	if err := d.CreateShow(ctx, &s); err != nil {
		t.Fatal(err)
	}
	got, _ := d.GetShow(ctx, s.ID)
	if got.AbsOffset == nil || *got.AbsOffset != 24 || EffectiveOffset(got) != 24 {
		t.Fatalf("abs_offset after create = %v", got.AbsOffset)
	}
	// prefs do not touch it; meta writes it
	got.EpisodeOffset = 12
	if err := d.UpdateShowPrefs(ctx, &got); err != nil {
		t.Fatal(err)
	}
	g2, _ := d.GetShow(ctx, s.ID)
	if *g2.AbsOffset != 24 || EffectiveOffset(g2) != 12 {
		t.Fatalf("manual offset must win: abs %v eff %d", g2.AbsOffset, EffectiveOffset(g2))
	}
	g2.AbsOffset = nil
	if err := d.UpdateShowMeta(ctx, &g2); err != nil {
		t.Fatal(err)
	}
	g3, _ := d.GetShow(ctx, s.ID)
	if g3.AbsOffset != nil {
		t.Fatalf("abs_offset not cleared: %v", *g3.AbsOffset)
	}
	g3.EpisodeOffset = 0
	if EffectiveOffset(g3) != 0 {
		t.Fatal("nil abs_offset and no manual offset must be 0")
	}
	g3.AbsOffset = Ptr(0)
	if err := d.UpdateShowMeta(ctx, &g3); err != nil {
		t.Fatal(err)
	}
	if g4, _ := d.GetShow(ctx, s.ID); g4.AbsOffset == nil || *g4.AbsOffset != 0 {
		t.Fatalf("abs_offset 0 must stay 0 (not NULL): %v", g4.AbsOffset)
	}
}

func TestReleaseStatusAt(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	old := Now
	t.Cleanup(func() { Now = old })
	Now = func() int64 { return 1000 }
	s := Show{TitleRomaji: "A"}
	d.CreateShow(ctx, &s)
	r := Release{ShowID: s.ID, Episode: Ptr(1), NyaaID: 1, Title: "a", PublishedAt: 1}
	d.UpsertRelease(ctx, &r)
	if got, _ := d.GetRelease(ctx, r.ID); got.StatusAt != 1000 || got.FirstSeen != 1000 {
		t.Fatalf("insert: status_at %d first_seen %d, want 1000", got.StatusAt, got.FirstSeen)
	}
	Now = func() int64 { return 2000 }
	d.SetReleaseStatus(ctx, r.ID, RelFailed, "x")
	if got, _ := d.GetRelease(ctx, r.ID); got.StatusAt != 2000 {
		t.Fatalf("SetReleaseStatus: status_at %d, want 2000", got.StatusAt)
	}
	Now = func() int64 { return 3000 }
	d.SetReleaseQbit(ctx, r.ID, RelAdded, "h", "")
	if got, _ := d.GetRelease(ctx, r.ID); got.StatusAt != 3000 || got.FirstSeen != 1000 {
		t.Fatalf("SetReleaseQbit: status_at %d first_seen %d", got.StatusAt, got.FirstSeen)
	}
}

func TestClearFailedReleases(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	s := Show{TitleRomaji: "A"}
	d.CreateShow(ctx, &s)
	other := Show{TitleRomaji: "B"}
	d.CreateShow(ctx, &other)
	mk := func(sid int64, nyaa int64, ep int, end int, batch bool, status string) int64 {
		r := Release{ShowID: sid, Episode: Ptr(ep), NyaaID: nyaa, Title: "r", PublishedAt: 1, Status: status, IsBatch: batch}
		if end > 0 {
			r.EpisodeEnd = Ptr(end)
		}
		if _, err := d.UpsertRelease(ctx, &r); err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	f1 := mk(s.ID, 1, 1, 0, false, RelFailed)
	f2 := mk(s.ID, 2, 2, 0, false, RelFailed)
	fb := mk(s.ID, 3, 1, 12, true, RelFailed) // batch 1-12 covers ep 2
	ok := mk(s.ID, 4, 2, 0, false, RelDone)
	fo := mk(other.ID, 5, 2, 0, false, RelFailed)

	n, err := d.ClearFailedReleases(ctx, s.ID, Ptr(2), "cleared by user")
	if err != nil || n != 2 {
		t.Fatalf("clear ep 2 = %d %v, want 2 (single + batch)", n, err)
	}
	st := func(id int64) Release { r, _ := d.GetRelease(ctx, id); return r }
	if r := st(f2); r.Status != RelSkipped || r.StatusNote != "cleared by user" {
		t.Fatalf("ep 2 release = %s %q", r.Status, r.StatusNote)
	}
	if st(fb).Status != RelSkipped || st(f1).Status != RelFailed || st(ok).Status != RelDone || st(fo).Status != RelFailed {
		t.Fatal("clear touched the wrong rows")
	}
	n, _ = d.ClearFailedReleases(ctx, s.ID, nil, "cleared by user")
	if n != 1 || st(f1).Status != RelSkipped || st(fo).Status != RelFailed {
		t.Fatalf("clear all = %d", n)
	}
	if n, _ := d.ClearFailedReleases(ctx, s.ID, nil, "cleared by user"); n != 0 {
		t.Fatalf("second clear = %d, want 0", n)
	}
}

func TestMaxEpisodeNumbers(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	a := Show{TitleRomaji: "A"}
	d.CreateShow(ctx, &a)
	b := Show{TitleRomaji: "B"}
	d.CreateShow(ctx, &b)
	d.EnsureEpisodes(ctx, a.ID, 4)
	d.EnsureEpisode(ctx, a.ID, 9)
	m, err := d.MaxEpisodeNumbers(ctx)
	if err != nil || m[a.ID] != 9 || len(m) != 1 {
		t.Fatalf("MaxEpisodeNumbers = %v %v", m, err)
	}
	if _, ok := m[b.ID]; ok {
		t.Fatal("show without episodes must be absent")
	}
}

// Follow-up A: -1 = chain computed but unknown; it is no offset, and
// SetAbsOffset writes only that column.
func TestAbsOffsetUnknown(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	s := Show{TitleRomaji: "A"}
	d.CreateShow(ctx, &s)
	if err := d.SetAbsOffset(ctx, s.ID, Ptr(-1)); err != nil {
		t.Fatal(err)
	}
	got, _ := d.GetShow(ctx, s.ID)
	if got.AbsOffset == nil || *got.AbsOffset != -1 || EffectiveOffset(got) != 0 {
		t.Fatalf("abs_offset %v, effective %d; want -1 and 0", got.AbsOffset, EffectiveOffset(got))
	}
	got.EpisodeOffset = 5
	if EffectiveOffset(got) != 5 {
		t.Fatal("manual offset must still win")
	}
	if err := d.SetAbsOffset(ctx, s.ID, nil); err != nil {
		t.Fatal(err)
	}
	if g, _ := d.GetShow(ctx, s.ID); g.AbsOffset != nil {
		t.Fatal("SetAbsOffset(nil) must write NULL")
	}
	if err := d.SetAbsOffset(ctx, 999, Ptr(3)); err == nil {
		t.Fatal("unknown show must fail")
	}
}

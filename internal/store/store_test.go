package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "abc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestOpenMigrateIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abc.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	v, ok, err := d.Meta(ctx, "schema_version")
	if err != nil || !ok || v != "2" {
		t.Fatalf("schema_version = %q %v %v", v, ok, err)
	}
	var mode string
	if err := d.SQL().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q %v", mode, err)
	}
	var fk int
	if err := d.SQL().QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys = %d %v", fk, err)
	}
	if err := d.PutSetting("app", `{"a":1}`); err != nil {
		t.Fatal(err)
	}
	d.Close()

	d2, err := Open(path) // second open re-runs schema.sql: must not fail or lose data
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	s, ok, err := d2.GetSetting("app")
	if err != nil || !ok || s != `{"a":1}` {
		t.Fatalf("setting after reopen = %q %v %v", s, ok, err)
	}
	if _, ok, _ := d2.GetSetting("missing"); ok {
		t.Fatal("missing setting reported present")
	}
}

func TestShowsCRUDAndCascade(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	s := Show{AniListID: 154587, TitleRomaji: "Sousou no Frieren", TitleEnglish: "Frieren", Synonyms: []string{"Frieren: Beyond Journey's End"},
		Status: "RELEASING", EpisodesTotal: Ptr(28)}
	if err := d.CreateShow(ctx, &s); err != nil {
		t.Fatal(err)
	}
	if s.ID == 0 || s.ListStatus != "watching" || s.AddedAt == 0 {
		t.Fatalf("create did not fill defaults: %+v", s)
	}
	got, err := d.GetShow(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TitleEnglish != "Frieren" || got.Synonyms[0] != "Frieren: Beyond Journey's End" || *got.EpisodesTotal != 28 ||
		got.GroupOrder != nil || got.AutoDownload != nil || got.DurationMin != nil {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if _, err := d.GetShowByAniList(ctx, 154587); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GetShow(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	dup := Show{AniListID: 154587, TitleRomaji: "dup"}
	if err := d.CreateShow(ctx, &dup); err == nil {
		t.Fatal("duplicate anilist_id accepted")
	}

	got.GroupOrder = []string{"Erai-raws"}
	got.AutoDownload = Ptr(false)
	got.EpisodeOffset = 12
	got.ListStatus = "paused"
	if err := d.UpdateShowPrefs(ctx, &got); err != nil {
		t.Fatal(err)
	}
	got.Status = "FINISHED"
	got.NextAiringAt = Ptr(int64(1700000000))
	if err := d.UpdateShowMeta(ctx, &got); err != nil {
		t.Fatal(err)
	}
	g2, _ := d.GetShow(ctx, s.ID)
	if g2.GroupOrder[0] != "Erai-raws" || *g2.AutoDownload || g2.EpisodeOffset != 12 || g2.Status != "FINISHED" || *g2.NextAiringAt != 1700000000 {
		t.Fatalf("updates lost: %+v", g2)
	}
	if l, _ := d.ListShows(ctx, "paused"); len(l) != 1 {
		t.Fatalf("ListShows(paused) = %d", len(l))
	}
	if l, _ := d.ListShows(ctx, "watching"); len(l) != 0 {
		t.Fatalf("ListShows(watching) = %d", len(l))
	}

	// children, then delete: episodes and releases cascade, files keep the row with show_id NULL.
	if _, err := d.EnsureEpisodes(ctx, s.ID, 3); err != nil {
		t.Fatal(err)
	}
	r := Release{ShowID: s.ID, Episode: Ptr(1), NyaaID: 1, Title: "x", PublishedAt: 1}
	if _, err := d.UpsertRelease(ctx, &r); err != nil {
		t.Fatal(err)
	}
	f := []File{{Path: `C:\a\x.mkv`, Size: 1, Mtime: 1, ShowID: &s.ID, Episode: Ptr(1)}}
	if err := d.UpsertFiles(ctx, f); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteShow(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if eps, _ := d.ListEpisodes(ctx, s.ID); len(eps) != 0 {
		t.Fatalf("episodes not cascaded: %d", len(eps))
	}
	if _, err := d.GetRelease(ctx, r.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("release not cascaded: %v", err)
	}
	ff, err := d.GetFile(ctx, f[0].ID)
	if err != nil || ff.ShowID != nil {
		t.Fatalf("file after show delete: %+v %v", ff, err)
	}
	if err := d.DeleteShow(ctx, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestEpisodes(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	s := Show{TitleRomaji: "A"}
	d.CreateShow(ctx, &s)
	n, err := d.EnsureEpisodes(ctx, s.ID, 3)
	if err != nil || n != 3 {
		t.Fatalf("EnsureEpisodes = %d %v", n, err)
	}
	if n, _ := d.EnsureEpisodes(ctx, s.ID, 4); n != 1 {
		t.Fatalf("EnsureEpisodes second = %d", n)
	}
	ch, err := d.UpsertEpisodeAiring(ctx, s.ID, 2, Ptr(int64(1000)))
	if err != nil || !ch {
		t.Fatalf("airing set: %v %v", ch, err)
	}
	if ch, _ := d.UpsertEpisodeAiring(ctx, s.ID, 2, Ptr(int64(1000))); ch {
		t.Fatal("same airing reported as change")
	}
	if ch, _ := d.UpsertEpisodeAiring(ctx, s.ID, 9, Ptr(int64(5000))); !ch {
		t.Fatal("new episode via airing not reported")
	}
	if ch, _ := d.SetWatched(ctx, s.ID, 2, 1234, "manual"); !ch {
		t.Fatal("SetWatched first")
	}
	if ch, _ := d.SetWatched(ctx, s.ID, 2, 9999, "player"); ch {
		t.Fatal("SetWatched second should be no-op")
	}
	e, _ := d.GetEpisode(ctx, s.ID, 2)
	if *e.WatchedAt != 1234 || e.WatchedSource != "manual" || *e.AiredAt != 1000 {
		t.Fatalf("episode = %+v", e)
	}
	if m, _ := d.MaxWatched(ctx, s.ID); m != 2 {
		t.Fatalf("MaxWatched = %d", m)
	}
	if ch, _ := d.ClearWatched(ctx, s.ID, 2); !ch {
		t.Fatal("ClearWatched")
	}
	if ch, _ := d.ClearWatched(ctx, s.ID, 2); ch {
		t.Fatal("ClearWatched twice")
	}
	if m, _ := d.MaxWatched(ctx, s.ID); m != 0 {
		t.Fatalf("MaxWatched after clear = %d", m)
	}
	d.SetDubAired(ctx, s.ID, 3, Ptr(int64(2000)))
	between, _ := d.EpisodesAiringBetween(ctx, 900, 2500)
	if len(between) != 2 || between[0].Number != 2 || between[1].Number != 3 {
		t.Fatalf("EpisodesAiringBetween = %+v", between)
	}
	if eps, _ := d.ListEpisodes(ctx, s.ID); len(eps) != 5 {
		t.Fatalf("ListEpisodes = %d", len(eps))
	}
}

func TestFiles(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	s := Show{TitleRomaji: "A"}
	d.CreateShow(ctx, &s)
	Now = func() int64 { return 100 }
	defer func() { Now = defaultNow }()
	fs := []File{
		{Path: "/lib/A - 01.mkv", Size: 10, Mtime: 5, ShowID: &s.ID, Episode: Ptr(1), ReleaseGroup: "SubsPlease", Parsed: json.RawMessage(`{"Title":"A"}`)},
		{Path: "/lib/unknown.mkv", Size: 3, Mtime: 5},
	}
	if err := d.UpsertFiles(ctx, fs); err != nil {
		t.Fatal(err)
	}
	if fs[0].ID == 0 || fs[1].ID == 0 || fs[0].FirstSeen != 100 {
		t.Fatalf("ids/first_seen not filled: %+v", fs)
	}
	Now = func() int64 { return 200 }
	upd := []File{{Path: "/lib/A - 01.mkv", Size: 11, Mtime: 6, ShowID: &s.ID, Episode: Ptr(1), Version: 2}}
	if err := d.UpsertFiles(ctx, upd); err != nil {
		t.Fatal(err)
	}
	if upd[0].ID != fs[0].ID || upd[0].FirstSeen != 100 {
		t.Fatalf("update changed id or first_seen: %+v", upd[0])
	}
	got, _ := d.GetFileByPath(ctx, "/lib/A - 01.mkv")
	if got.Size != 11 || got.Version != 2 || got.LastSeen != 200 || !got.Present {
		t.Fatalf("after update: %+v", got)
	}
	if err := d.MarkFilesMissing(ctx, []int64{got.ID}); err != nil {
		t.Fatal(err)
	}
	if m, _ := d.PresentMatchedFiles(ctx); len(m) != 0 {
		t.Fatalf("missing file still present: %d", len(m))
	}
	if err := d.TouchFiles(ctx, []int64{got.ID}, 300); err != nil {
		t.Fatal(err)
	}
	if m, _ := d.PresentMatchedFiles(ctx); len(m) != 1 || m[0].LastSeen != 300 {
		t.Fatalf("touch did not restore: %+v", m)
	}
	if all, _ := d.AllFiles(ctx); len(all) != 2 {
		t.Fatalf("AllFiles = %d", len(all))
	}
	if fl, _ := d.FilesForShow(ctx, s.ID); len(fl) != 1 {
		t.Fatalf("FilesForShow = %d", len(fl))
	}
}

func TestReleases(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	s := Show{TitleRomaji: "A"}
	d.CreateShow(ctx, &s)
	r := Release{ShowID: s.ID, Episode: Ptr(5), NyaaID: 42, Title: "[SubsPlease] A - 05 (1080p)", InfoHash: "ABCDEF", Seeders: Ptr(10), PublishedAt: 50}
	isNew, err := d.UpsertRelease(ctx, &r)
	if err != nil || !isNew || r.ID == 0 {
		t.Fatalf("insert: %v %v %d", isNew, err, r.ID)
	}
	again := Release{ShowID: s.ID, Episode: Ptr(5), NyaaID: 42, Title: "same", Seeders: Ptr(99), PublishedAt: 50}
	isNew, err = d.UpsertRelease(ctx, &again)
	if err != nil || isNew || again.ID != r.ID {
		t.Fatalf("re-upsert: %v %v %d", isNew, err, again.ID)
	}
	if err := d.SetReleaseQbit(ctx, r.ID, RelAdded, "abcdef", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := d.GetRelease(ctx, r.ID)
	if *got.Seeders != 99 || got.Title != "[SubsPlease] A - 05 (1080p)" || got.Status != RelAdded || got.QbitHash != "abcdef" || got.InfoHash != "abcdef" {
		t.Fatalf("release = %+v", got)
	}
	batch := Release{ShowID: s.ID, Episode: Ptr(1), EpisodeEnd: Ptr(12), IsBatch: true, NyaaID: 43, Title: "batch", PublishedAt: 60}
	d.UpsertRelease(ctx, &batch)
	other := Release{ShowID: s.ID, Episode: Ptr(6), NyaaID: 44, Title: "six", PublishedAt: 70}
	d.UpsertRelease(ctx, &other)

	l, _ := d.ListReleases(ctx, s.ID, Ptr(5))
	if len(l) != 2 || l[0].ID != batch.ID || l[1].ID != r.ID {
		t.Fatalf("ListReleases(ep5) = %+v", l)
	}
	if l, _ := d.ListReleases(ctx, 0, nil); len(l) != 3 {
		t.Fatalf("ListReleases(all) = %d", len(l))
	}
	if l, _ := d.ReleasesByStatus(ctx, RelAdded, RelDownloading); len(l) != 1 {
		t.Fatalf("ReleasesByStatus = %d", len(l))
	}
	if !batch.Covers(12) || batch.Covers(13) || !r.Covers(5) || r.Covers(6) {
		t.Fatal("Covers wrong")
	}
	if !(Release{IsBatch: true}).Covers(99) {
		t.Fatal("open batch should cover everything")
	}
	if err := d.SetReleaseStatus(ctx, 999, RelSkipped, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("status on missing release: %v", err)
	}
}

func TestEventsAndAccounts(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		e := Event{Kind: "scan.done", Message: "m"}
		if err := d.InsertEvent(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}
	l, _ := d.ListEvents(ctx, 0, 3)
	if len(l) != 3 || l[0].ID != 5 || string(l[0].Data) != "{}" {
		t.Fatalf("ListEvents = %+v", l)
	}
	if l, _ := d.ListEvents(ctx, 3, 100); len(l) != 2 {
		t.Fatalf("ListEvents(after 3) = %d", len(l))
	}

	a := Account{Service: "anilist", UserName: "me", AccessToken: "tok"}
	if err := d.UpsertAccount(ctx, a); err != nil {
		t.Fatal(err)
	}
	d.SetAccountPush(ctx, "anilist", true)
	got, err := d.GetAccount(ctx, "anilist")
	if err != nil || got.AccessToken != "tok" || !got.PushEnabled {
		t.Fatalf("account = %+v %v", got, err)
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "tok") {
		t.Fatalf("token leaked into JSON: %s", b)
	}
	if l, _ := d.ListAccounts(ctx); len(l) != 1 {
		t.Fatal("ListAccounts")
	}
	if err := d.DeleteAccount(ctx, "anilist"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GetAccount(ctx, "anilist"); !errors.Is(err, ErrNotFound) {
		t.Fatal("account not deleted")
	}
}

func TestStatsAndCounts(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	s := Show{TitleRomaji: "A", AddedAt: 10}
	d.CreateShow(ctx, &s)
	p := Show{TitleRomaji: "B", ListStatus: "planning"}
	d.CreateShow(ctx, &p)
	d.UpsertEpisodeAiring(ctx, s.ID, 1, Ptr(int64(100)))
	d.UpsertEpisodeAiring(ctx, s.ID, 2, Ptr(int64(200)))
	d.UpsertEpisodeAiring(ctx, s.ID, 3, Ptr(int64(1000)))
	d.SetWatched(ctx, s.ID, 1, 150, "manual")
	d.UpsertFiles(ctx, []File{
		{Path: "a1", ShowID: &s.ID, Episode: Ptr(1), FirstSeen: 120, LastSeen: 120},
		{Path: "a2", ShowID: &s.ID, Episode: Ptr(2), FirstSeen: 220, LastSeen: 220},
		{Path: "a2v2", ShowID: &s.ID, Episode: Ptr(2), Version: 2, FirstSeen: 230, LastSeen: 230},
	})
	st, err := d.ShowStats(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	got := st[s.ID]
	if got.Watched != 1 || got.OnDisk != 2 || got.Aired != 2 || got.LastActivity != 230 {
		t.Fatalf("ShowStat = %+v", got)
	}
	c, err := d.StateCounts(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	if c.Watching != 1 || c.OnDiskUnwatched != 1 || c.AiringThisWeek != 1 {
		t.Fatalf("Counts = %+v", c)
	}
}

// SPEC v1.1 section 9: migrate_2.sql adds episodes.auto and shows.auto_from to an
// existing v1.1 (schema_version 1) database without touching its rows.
func TestMigrate2AddsAutoColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abc.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	schema, _ := sqlFS.ReadFile("schema.sql")
	if _, err := raw.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO meta(key,value) VALUES('schema_version','1')`,
		`INSERT INTO shows(id, title_romaji, added_at, updated_at) VALUES(7, 'Frieren', 1, 1)`,
		`INSERT INTO episodes(show_id, number, aired_at) VALUES(7, 3, 1000)`,
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
	defer d.Close()
	ctx := context.Background()
	if v, _, _ := d.Meta(ctx, "schema_version"); v != "2" {
		t.Fatalf("schema_version = %q, want 2", v)
	}
	hasCol := func(table, col string) bool {
		rows, err := d.SQL().Query(`SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var n string
			rows.Scan(&n)
			if n == col {
				return true
			}
		}
		return false
	}
	if !hasCol("episodes", "auto") || !hasCol("shows", "auto_from") {
		t.Fatal("migrate_2 columns missing")
	}
	sh, err := d.GetShow(ctx, 7)
	if err != nil || sh.TitleRomaji != "Frieren" || sh.AutoFrom != nil {
		t.Fatalf("show after migrate = %+v %v", sh, err)
	}
	e, err := d.GetEpisode(ctx, 7, 3)
	if err != nil || e.AiredAt == nil || *e.AiredAt != 1000 || e.Auto != nil {
		t.Fatalf("episode after migrate = %+v %v", e, err)
	}
	// round trips
	if err := d.SetEpisodeAuto(ctx, 7, 3, Ptr(0)); err != nil {
		t.Fatal(err)
	}
	if err := d.SetEpisodeAuto(ctx, 7, 9, Ptr(1)); err != nil { // creates the row
		t.Fatal(err)
	}
	if e, _ := d.GetEpisode(ctx, 7, 3); e.Auto == nil || *e.Auto != 0 {
		t.Fatalf("auto = %v, want 0", e.Auto)
	}
	if e, _ := d.GetEpisode(ctx, 7, 9); e.Auto == nil || *e.Auto != 1 {
		t.Fatalf("auto = %v, want 1", e.Auto)
	}
	if err := d.SetEpisodesAutoRange(ctx, 7, 1, -1, nil); err != nil {
		t.Fatal(err)
	}
	if e, _ := d.GetEpisode(ctx, 7, 9); e.Auto != nil {
		t.Fatalf("auto after range reset = %v, want nil", *e.Auto)
	}
	sh.AutoFrom = Ptr(5)
	if err := d.UpdateShowPrefs(ctx, &sh); err != nil {
		t.Fatal(err)
	}
	if sh, _ := d.GetShow(ctx, 7); sh.AutoFrom == nil || *sh.AutoFrom != 5 {
		t.Fatalf("auto_from = %v, want 5", sh.AutoFrom)
	}
	d.Close()
	d2, err := Open(path) // reopening must not re-run migrate_2 (duplicate column)
	if err != nil {
		t.Fatal(err)
	}
	d2.Close()
}

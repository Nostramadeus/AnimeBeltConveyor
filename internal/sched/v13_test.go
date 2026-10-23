package sched

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/events"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// prequelHop is one fake answer of Brain.Prequel.
type prequelHop struct {
	eps  int
	next *RelNode
}

func (f *fakeBrain) Prequel(_ context.Context, id int) (int, *RelNode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prequelCalls = append(f.prequelCalls, id)
	if f.prequelErr != nil {
		return 0, nil, f.prequelErr
	}
	h, ok := f.prequels[id]
	if !ok {
		return 0, nil, errors.New("no such media")
	}
	return h.eps, h.next, nil
}

func (f *fakeBrain) calls() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int{}, f.prequelCalls...)
}

func (f *fakeBrain) resetCalls() {
	f.mu.Lock()
	f.prequelCalls = nil
	f.mu.Unlock()
}

// SPEC v1.3 section 1.2: the chain walk. Follow-up A: a chain that cannot be
// summed is stored as AbsUnknown (-1); a failed AniList request stores nothing
// (NULL, retried by the next airing pass).
func TestAbsOffsetWalk(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b := e.brain
	val := func(p *int) string {
		if p == nil {
			return "nil"
		}
		return itoa(*p)
	}
	walk := func(self int, p *RelNode) (string, error) {
		v, err := e.s.walkPrequels(ctx, self, RelTitles{Romaji: "Show 4th Season"}, p)
		return val(v), err
	}
	// no prequel: known 0, no request
	if got, err := walk(1, nil); got != "0" || err != nil || len(b.calls()) != 0 {
		t.Fatalf("no prequel = %s %v, calls %v", got, err, b.calls())
	}
	// Black Clover 2026 -> Black Clover (170) -> nothing before it
	b.prequels = map[int]prequelHop{97940: {eps: 170}}
	if got, err := walk(999, &RelNode{ID: 97940, Episodes: 170, TitleRomaji: "Show"}); got != "170" || err != nil {
		t.Fatalf("one hop = %s %v", got, err)
	}
	// three seasons: 12 + 13 + 24, the edge counts are summed (the fetched own count is ignored)
	b.prequels = map[int]prequelHop{
		3: {eps: 999, next: &RelNode{ID: 2, Episodes: 13, TitleRomaji: "Show 2nd Season"}},
		2: {eps: 13, next: &RelNode{ID: 1, Episodes: 12, TitleRomaji: "Show"}},
		1: {eps: 12},
	}
	s4 := &RelNode{ID: 3, Episodes: 24, TitleRomaji: "Show 3rd Season"}
	b.resetCalls()
	if got, err := walk(4, s4); got != "49" || err != nil || len(b.calls()) != 3 {
		t.Fatalf("chain = %s %v, calls %v", got, err, b.calls())
	}
	// a prequel with an unknown episode count: cannot be summed, no error
	b.prequels[2] = prequelHop{eps: 13, next: &RelNode{ID: 1, Episodes: 0, TitleRomaji: "Show"}}
	if got, err := walk(4, s4); got != "nil" || err != nil {
		t.Fatalf("unknown count = %s %v, want nil nil", got, err)
	}
	// a fetch error: nil plus the error
	b.prequels[2] = prequelHop{eps: 13, next: &RelNode{ID: 1, Episodes: 12, TitleRomaji: "Show"}}
	b.prequelErr = errors.New("AniList down")
	if got, err := walk(4, s4); got != "nil" || err == nil {
		t.Fatalf("fetch error = %s %v, want nil + error", got, err)
	}
	b.prequelErr = nil
	// a cycle back to an earlier node: cannot be summed
	b.prequels[1] = prequelHop{eps: 12, next: &RelNode{ID: 3, Episodes: 24, TitleRomaji: "Show 3rd Season"}}
	if got, err := walk(4, s4); got != "nil" || err != nil {
		t.Fatalf("cycle = %s %v, want nil nil", got, err)
	}
	// endless chain: stops at 30 hops
	b.prequels = map[int]prequelHop{}
	for id := 100; id < 200; id++ {
		b.prequels[id] = prequelHop{eps: 1, next: &RelNode{ID: id + 1, Episodes: 1, TitleRomaji: "Show"}}
	}
	b.resetCalls()
	if got, err := walk(1, &RelNode{ID: 100, Episodes: 1, TitleRomaji: "Show"}); got != "nil" || err != nil || len(b.calls()) != maxPrequelHops {
		t.Fatalf("hop cap = %s %v after %d calls", got, err, len(b.calls()))
	}
	// 30 prequels is still fine
	b.prequels[129] = prequelHop{eps: 1}
	if got, _ := walk(1, &RelNode{ID: 100, Episodes: 1, TitleRomaji: "Show"}); got != "30" {
		t.Fatalf("30 hops = %s, want 30", got)
	}
	// what gets stored
	if val(absToStore(store.Ptr(24), nil)) != "24" || val(absToStore(store.Ptr(0), nil)) != "0" ||
		val(absToStore(nil, nil)) != "-1" || absToStore(nil, errors.New("down")) != nil {
		t.Fatal("absToStore mapping")
	}
}

// Follow-up A: the periodic pass backfills shows of any status whose abs_offset
// is NULL, once; -1 (chain unknown) is not retried; Refresh re-walks it.
func TestAbsOffsetBackfill(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b := e.brain
	// finished sequel, migrated with NULL
	fin := e.show(t, store.Show{AniListID: 145064, TitleRomaji: "Jujutsu Kaisen 2nd Season", Status: "FINISHED"})
	// finished show whose chain cannot be summed (a prequel without an episode count)
	unk := e.show(t, store.Show{AniListID: 500, TitleRomaji: "Long Runner Part 2", Status: "FINISHED"})
	// finished show that already has a value: never asked
	known := e.show(t, store.Show{AniListID: 600, TitleRomaji: "Known", Status: "FINISHED", AbsOffset: store.Ptr(12)})
	b.prequels = map[int]prequelHop{
		145064: {eps: 23, next: &RelNode{ID: 113415, Episodes: 24, TitleRomaji: "Jujutsu Kaisen"}},
		113415: {eps: 24},
		500:    {eps: 12, next: &RelNode{ID: 499, Episodes: 0, TitleRomaji: "Long Runner"}},
	}
	b.prequelErr = errors.New("AniList down")
	e.s.airingRun(ctx)
	if got, _ := e.db.GetShow(ctx, fin.ID); got.AbsOffset != nil {
		t.Fatalf("AniList down: abs_offset = %d, want NULL (retried)", *got.AbsOffset)
	}
	b.prequelErr = nil
	b.resetCalls()
	if err := e.s.airingRun(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.db.GetShow(ctx, fin.ID); got.AbsOffset == nil || *got.AbsOffset != 24 {
		t.Fatalf("finished show backfill = %v, want 24", got.AbsOffset)
	}
	if got, _ := e.db.GetShow(ctx, unk.ID); got.AbsOffset == nil || *got.AbsOffset != AbsUnknown || store.EffectiveOffset(got) != 0 {
		t.Fatalf("unknown chain = %v, want -1 and no effective offset", got.AbsOffset)
	}
	for _, id := range b.calls() {
		if id == 600 {
			t.Fatal("a show with a value was asked again")
		}
	}
	if ev := absEvents(t, e); len(ev) != 1 || !strings.HasPrefix(ev[0], "Jujutsu Kaisen 2nd Season: absolute numbering starts at 25") {
		t.Fatalf("events = %q (no event for -1)", ev)
	}
	// next pass: nothing left to compute, no requests
	b.resetCalls()
	e.s.airingRun(ctx)
	if len(b.calls()) != 0 {
		t.Fatalf("second pass asked AniList again: %v", b.calls())
	}
	// Refresh re-walks a -1 regardless (Media has the prequel now known)
	b.media[500] = Media{MediaSummary: MediaSummary{ID: 500, TitleRomaji: "Long Runner Part 2", Status: "FINISHED", Episodes: 12},
		Prequel: &RelNode{ID: 499, Format: "TV", Episodes: 50, TitleRomaji: "Long Runner"}}
	b.prequels[499] = prequelHop{eps: 50}
	if _, err := e.s.RefreshShow(ctx, unk.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.db.GetShow(ctx, unk.ID); got.AbsOffset == nil || *got.AbsOffset != 50 {
		t.Fatalf("refresh of a -1 show = %v, want 50", got.AbsOffset)
	}
	if got, _ := e.db.GetShow(ctx, known.ID); *got.AbsOffset != 12 {
		t.Fatal("known value changed")
	}
	// an added show whose chain cannot be summed is stored as -1 at once
	b.media[700] = Media{MediaSummary: MediaSummary{ID: 700, TitleRomaji: "Unknown Show 2nd Season", Status: "RELEASING", Episodes: 12},
		Prequel: &RelNode{ID: 698, Format: "TV", Episodes: 0, TitleRomaji: "Unknown Show"}}
	s7, _, err := e.s.AddShow(ctx, 700, "")
	if err != nil || s7.AbsOffset == nil || *s7.AbsOffset != AbsUnknown {
		t.Fatalf("add with an unknown chain = %v %v, want -1", s7.AbsOffset, err)
	}
	b.resetCalls()
	e.s.airingRun(ctx) // releasing, but -1 is "computed": the refresh does not walk again
	for _, id := range b.calls() {
		if id == 698 || id == 700 {
			t.Fatalf("airing pass re-walked a -1 show: %v", b.calls())
		}
	}
}

func absEvents(t *testing.T, e *env) []string {
	t.Helper()
	evs, _ := e.db.ListEvents(context.Background(), 0, 1000)
	var out []string
	for _, ev := range evs {
		if ev.Kind == events.AiringUpdated && strings.Contains(ev.Message, "absolute numbering") {
			out = append(out, ev.Message+" "+string(ev.Data))
		}
	}
	return out
}

// Add and Refresh always walk; the periodic airing pass only walks shows whose
// abs_offset is unknown.
func TestAbsOffsetAddRefreshAiring(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b := e.brain
	b.media[145064] = Media{MediaSummary: MediaSummary{ID: 145064, TitleRomaji: "Jujutsu Kaisen 2nd Season", Status: "RELEASING", Episodes: 23},
		Prequel: &RelNode{ID: 113415, Format: "TV", Episodes: 24, TitleRomaji: "Jujutsu Kaisen"}}
	b.prequels = map[int]prequelHop{113415: {eps: 24}}
	sh, created, err := e.s.AddShow(ctx, 145064, "")
	if err != nil || !created || sh.AbsOffset == nil || *sh.AbsOffset != 24 {
		t.Fatalf("AddShow: %v %v %v", created, err, sh.AbsOffset)
	}
	if got, _ := e.db.GetShow(ctx, sh.ID); got.AbsOffset == nil || *got.AbsOffset != 24 {
		t.Fatalf("stored abs_offset = %v", got.AbsOffset)
	}
	if ev := absEvents(t, e); len(ev) != 1 || !strings.HasPrefix(ev[0], "Jujutsu Kaisen 2nd Season: absolute numbering starts at 25") || !strings.Contains(ev[0], `"abs_offset":24`) {
		t.Fatalf("events = %q", ev)
	}
	// periodic pass: known -> no request
	b.resetCalls()
	if err := e.s.airingRun(ctx); err != nil {
		t.Fatal(err)
	}
	if len(b.calls()) != 0 {
		t.Fatalf("airingRun walked a known show: %v", b.calls())
	}
	// unknown (NULL) -> the periodic pass walks it, no new event for the same value
	got, _ := e.db.GetShow(ctx, sh.ID)
	got.AbsOffset = nil
	e.db.UpdateShowMeta(ctx, &got)
	e.s.airingRun(ctx)
	if len(b.calls()) != 1 {
		t.Fatalf("airingRun calls = %v, want 1", b.calls())
	}
	if got, _ := e.db.GetShow(ctx, sh.ID); got.AbsOffset == nil || *got.AbsOffset != 24 {
		t.Fatalf("abs_offset after airingRun = %v", got.AbsOffset)
	}
	if ev := absEvents(t, e); len(ev) != 2 {
		t.Fatalf("NULL -> 24 is a change: events = %q", ev)
	}
	// Refresh always walks; a changed value is logged
	b.prequels[113415] = prequelHop{eps: 24, next: &RelNode{ID: 1, Episodes: 2, TitleRomaji: "Jujutsu Kaisen (2019)"}}
	b.prequels[1] = prequelHop{eps: 2}
	b.resetCalls()
	if _, err := e.s.RefreshShow(ctx, sh.ID); err != nil {
		t.Fatal(err)
	}
	if len(b.calls()) != 2 {
		t.Fatalf("refresh calls = %v", b.calls())
	}
	if got, _ := e.db.GetShow(ctx, sh.ID); *got.AbsOffset != 26 {
		t.Fatalf("abs_offset after refresh = %d", *got.AbsOffset)
	}
	if ev := absEvents(t, e); len(ev) != 3 || !strings.HasPrefix(ev[0], "Jujutsu Kaisen 2nd Season: absolute numbering starts at 27") {
		t.Fatalf("events = %q", ev)
	}
	// same value again: no event
	e.s.RefreshShow(ctx, sh.ID)
	if ev := absEvents(t, e); len(ev) != 3 {
		t.Fatalf("unchanged value logged: %q", ev)
	}
	// AniList down during the walk: the add still works, abs_offset NULL (not computed; the next airing pass retries), no event
	b.prequelErr = errors.New("AniList down")
	b.media[200] = Media{MediaSummary: MediaSummary{ID: 200, TitleRomaji: "Sequel 2nd Season", Status: "FINISHED", Episodes: 12},
		Prequel: &RelNode{ID: 199, Format: "TV", Episodes: 12, TitleRomaji: "Sequel"}}
	s2, created, err := e.s.AddShow(ctx, 200, "")
	if err != nil || !created || s2.AbsOffset != nil {
		t.Fatalf("add with failing walk: %v %v %v", created, err, s2.AbsOffset)
	}
	// no prequel: 0, no event
	b.media[300] = Media{MediaSummary: MediaSummary{ID: 300, TitleRomaji: "Original", Status: "FINISHED", Episodes: 12}}
	s3, _, _ := e.s.AddShow(ctx, 300, "")
	if s3.AbsOffset == nil || *s3.AbsOffset != 0 {
		t.Fatalf("no prequel: abs_offset = %v, want 0", s3.AbsOffset)
	}
	if ev := absEvents(t, e); len(ev) != 3 {
		t.Fatalf("nil / 0 must not log: %q", ev)
	}
}

// SPEC v1.3 section 1.3: nyaa releases and files on disk are matched with the
// show's abs_offset, and a batch range end is shifted like its start.
func TestAbsOffsetConveyorAndScan(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sh := e.show(t, store.Show{TitleRomaji: "Black Clover", Status: "RELEASING", AbsOffset: store.Ptr(170)})
	e.db.EnsureEpisodes(ctx, sh.ID, 4) // EpisodesKnown 4, total unknown
	e.brain.nyaa = []NyaaItem{
		{ID: 1, Title: "[SubsPlease] Black Clover - 173 (1080p) [ABCD1234].mkv", InfoHash: h1, Seeders: 9, PublishedAt: e.clock},
		{ID: 2, Title: "[Erai-raws] Black Clover - 03 [1080p].mkv", InfoHash: h2, Seeders: 9, PublishedAt: e.clock},
		{ID: 3, Title: "[Group] Black Clover (171-174) [1080p]", InfoHash: hashOf(3), Seeders: 9, PublishedAt: e.clock},
	}
	e.s.conveyorRun(ctx, 0)
	rels, _ := e.db.ListReleases(ctx, sh.ID, nil)
	byNyaa := map[int64]store.Release{}
	for _, r := range rels {
		byNyaa[r.NyaaID] = r
	}
	if r := byNyaa[1]; r.Episode == nil || *r.Episode != 3 {
		t.Fatalf("SubsPlease 173 = %+v, want ep 3", r)
	}
	if r := byNyaa[2]; r.Episode == nil || *r.Episode != 3 {
		t.Fatalf("Erai-raws 03 = %+v, want ep 3", r)
	}
	if r := byNyaa[3]; !r.IsBatch || r.Episode == nil || *r.Episode != 1 || r.EpisodeEnd == nil || *r.EpisodeEnd != 4 {
		t.Fatalf("batch 171-174 = %+v, want 1-4", r)
	}
	touch(t, filepath.Join(e.root, "Black Clover", "[SubsPlease] Black Clover - 172 (1080p).mkv"))
	touch(t, filepath.Join(e.root, "Black Clover", "[Erai-raws] Black Clover - 04 [1080p].mkv"))
	if err := e.s.scanRun(ctx, true); err != nil {
		t.Fatal(err)
	}
	files, _ := e.db.FilesForShow(ctx, sh.ID)
	got := map[string]int{}
	for _, f := range files {
		got[filepath.Base(f.Path)] = deref(f.Episode)
	}
	if got["[SubsPlease] Black Clover - 172 (1080p).mkv"] != 2 || got["[Erai-raws] Black Clover - 04 [1080p].mkv"] != 4 {
		t.Fatalf("files = %v", got)
	}
}

// Follow-up D: the six chains from the brief. The walk stops at the first
// prequel of another series (JoJo restarts its numbering every part).
func TestChainStopsAtSeries(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	b := e.brain
	const (
		jojo2012 = "JoJo no Kimyou na Bouken (2012)"
		sc       = "JoJo no Kimyou na Bouken: Stardust Crusaders"
		so       = "JoJo no Kimyou na Bouken: Stone Ocean"
		sbr      = "JoJo no Kimyou na Bouken: Steel Ball Run"
	)
	b.prequels = map[int]prequelHop{
		9001:   {eps: 12, next: &RelNode{ID: 9000, Format: "ONA", Episodes: 38, TitleRomaji: so}}, // SBR -> Stone Ocean
		9000:   {eps: 38, next: &RelNode{ID: 8000, Format: "TV", Episodes: 39, TitleRomaji: "JoJo no Kimyou na Bouken: Ougon no Kaze"}},
		2001:   {eps: 24, next: &RelNode{ID: 2000, Format: "TV", Episodes: 26, TitleRomaji: jojo2012}}, // Stardust Crusaders -> 2012
		97940:  {eps: 170},                                                                             // Black Clover: nothing before it
		3002:   {eps: 24, next: &RelNode{ID: 3001, Format: "TV", Episodes: 24, TitleRomaji: "Kusuriya no Hitorigoto"}},
		3001:   {eps: 24},
		113415: {eps: 24}, // Jujutsu Kaisen: JJK 0 is a movie, pickPrequel never offers it
	}
	media := func(id int, romaji string, p *RelNode) Media {
		return Media{MediaSummary: MediaSummary{ID: id, TitleRomaji: romaji}, Prequel: p}
	}
	cases := []struct {
		name  string
		m     Media
		want  string
		calls []int // the prequels fetched; a stop costs no request
	}{
		{"SBR 2nd & 3rd STAGE stops at Stone Ocean", media(9002, sbr+" - 2nd & 3rd STAGE", &RelNode{ID: 9001, Format: "ONA", Episodes: 12, TitleRomaji: sbr}), "12", []int{9001}},
		{"Egypt-hen counts Stardust Crusaders, stops at the 2012 series", media(2002, sc+" - Egypt-hen", &RelNode{ID: 2001, Format: "TV", Episodes: 24, TitleRomaji: sc}), "24", []int{2001}},
		{"Stardust Crusaders: the 2012 series is another part", media(2001, sc, &RelNode{ID: 2000, Format: "TV", Episodes: 26, TitleRomaji: jojo2012}), "0", nil},
		{"Black Clover 2nd Season", media(195604, "Black Clover 2nd Season", &RelNode{ID: 97940, Format: "TV", Episodes: 170, TitleRomaji: "Black Clover"}), "170", []int{97940}},
		{"Kusuriya 3rd Season", media(3003, "Kusuriya no Hitorigoto 3rd Season", &RelNode{ID: 3002, Format: "TV", Episodes: 24, TitleRomaji: "Kusuriya no Hitorigoto 2nd Season"}), "48", []int{3002, 3001}},
		{"Jujutsu Kaisen 2nd Season", media(145064, "Jujutsu Kaisen 2nd Season", &RelNode{ID: 113415, Format: "TV", Episodes: 24, TitleRomaji: "Jujutsu Kaisen"}), "24", []int{113415}},
	}
	for _, c := range cases {
		b.resetCalls()
		got := e.s.absOffset(ctx, c.m)
		v := "nil"
		if got != nil {
			v = itoa(*got)
		}
		calls := b.calls()
		same := len(calls) == len(c.calls)
		for i := range calls {
			same = same && calls[i] == c.calls[i]
		}
		if v != c.want || !same {
			t.Errorf("%s: abs_offset %s (calls %v), want %s (calls %v)", c.name, v, calls, c.want, c.calls)
		}
	}
}

func TestSameSeries(t *testing.T) {
	cases := []struct {
		a, b RelTitles
		want bool
	}{
		{RelTitles{Romaji: "Black Clover 2nd Season"}, RelTitles{Romaji: "Black Clover"}, true},
		{RelTitles{Romaji: "Shingeki no Kyojin: The Final Season"}, RelTitles{Romaji: "Shingeki no Kyojin Season 3 Part 2"}, true},
		{RelTitles{Romaji: "JoJo no Kimyou na Bouken: Stardust Crusaders"}, RelTitles{Romaji: "JoJo no Kimyou na Bouken (2012)"}, false}, // equality, not prefix
		{RelTitles{Romaji: "Tensei shitara Slime 2", English: "That Time I Got Reincarnated as a Slime Season 2"},
			RelTitles{Romaji: "Tensei shitara Slime Datta Ken", English: "That Time I Got Reincarnated as a Slime"}, true}, // English alone is enough
		{RelTitles{Romaji: "", English: ""}, RelTitles{Romaji: "", English: ""}, false}, // empty never matches
		{RelTitles{Romaji: "Show", English: ""}, RelTitles{Romaji: "Other", English: ""}, false},
	}
	for _, c := range cases {
		if got := sameSeries(c.a, c.b); got != c.want {
			t.Errorf("sameSeries(%+v, %+v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

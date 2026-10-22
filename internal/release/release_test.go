package release

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

var (
	aired = time.Date(2026, 10, 18, 15, 0, 0, 0, time.UTC)
	early = aired.Add(2 * time.Hour)  // inside the 12 h fallback window
	late  = aired.Add(13 * time.Hour) // after it
)

func rules() Rules {
	return Rules{
		GroupOrder:             []string{"SubsPlease", "Erai-raws"},
		Resolution:             "1080p",
		FallbackHours:          12,
		SkipBatchesWhileAiring: true,
		ShowAiring:             true,
		MinSeeders:             1,
		AutoReplaceV2:          true,
	}
}

func c(id int64, group, res string, ver, seeders int) Candidate {
	return Candidate{ID: id, Group: group, Resolution: res, Version: ver, Seeders: seeders,
		PublishedAt: aired.Add(time.Duration(id) * time.Minute), Episode: 5}
}

func pick(t *testing.T, cands []Candidate, r Rules, now time.Time, wantID int64, wantReason string) {
	t.Helper()
	got, reason := Pick(cands, r, now, aired)
	var id int64
	if got != nil {
		id = got.ID
	}
	if id != wantID {
		t.Errorf("Pick chose %d (%q), want %d", id, reason, wantID)
	}
	if wantReason != "" && reason != wantReason {
		t.Errorf("Pick reason %q, want %q", reason, wantReason)
	}
	if got == nil && reason == "" {
		t.Errorf("nil pick without a reason")
	}
}

func TestEmpty(t *testing.T) {
	pick(t, nil, rules(), late, 0, ReasonNoCandidates)
}

// Rule 1: drops.
func TestRule1WrongResolution(t *testing.T) {
	cands := []Candidate{c(1, "SubsPlease", "720p", 1, 50), c(2, "SubsPlease", "2160p", 1, 50)}
	pick(t, cands, rules(), late, 0, ReasonNoResolution)
	// target present: other resolutions ignored even with more seeders
	cands = append(cands, c(3, "Other", "1080p", 1, 2))
	pick(t, cands, rules(), late, 3, "")
	// empty target = any resolution
	r := rules()
	r.Resolution = ""
	// equal version and seeders: the newer upload (id 2) wins
	pick(t, cands[:2], r, late, 2, "")
}

func TestRule1LowerResolutionAfterFallback(t *testing.T) {
	r := rules()
	r.AllowLowerResAfterFallback = true
	cands := []Candidate{c(1, "Other", "720p", 1, 50), c(2, "SubsPlease", "2160p", 1, 99), c(3, "Other", "", 1, 10)}
	// before fallback: wait
	pick(t, cands, r, early, 0, ReasonWaitingRes)
	// after fallback: lower (and unknown) allowed, higher never; most seeders among lower
	pick(t, cands, r, late, 1, "fallback: lower resolution 720p")
	// group order still applies inside the lower pool
	cands = append(cands, c(4, "Erai-raws", "480p", 1, 3))
	pick(t, cands, r, late, 4, "preferred group Erai-raws at lower resolution 480p")
}

func TestRule1Seeders(t *testing.T) {
	r := rules()
	r.MinSeeders = 5
	cands := []Candidate{c(1, "SubsPlease", "1080p", 1, 4), c(2, "Erai-raws", "1080p", 1, 5)}
	pick(t, cands, r, early, 2, "")
	pick(t, cands[:1], r, late, 0, ReasonFiltered)
}

func TestRule1BatchesWhileAiring(t *testing.T) {
	b := c(1, "SubsPlease", "1080p", 1, 500)
	b.IsBatch = true
	single := c(2, "Other", "1080p", 1, 3)
	r := rules()
	pick(t, []Candidate{b, single}, r, late, 2, "")
	r.ShowAiring = false // finished show: batches allowed
	pick(t, []Candidate{b, single}, r, late, 1, "")
	r.ShowAiring, r.SkipBatchesWhileAiring = true, false
	pick(t, []Candidate{b, single}, r, late, 1, "")
}

func TestRule1Recap(t *testing.T) {
	rc := c(1, "SubsPlease", "1080p", 1, 100)
	rc.IsRecap = true
	pick(t, []Candidate{rc}, rules(), late, 0, ReasonFiltered)
	pick(t, []Candidate{rc, c(2, "Other", "1080p", 1, 1)}, rules(), late, 2, "")
}

// Rule 2: earliest group in GroupOrder; highest version, then most seeders.
func TestRule2PreferredGroup(t *testing.T) {
	cands := []Candidate{
		c(1, "Erai-raws", "1080p", 1, 900),
		c(2, "SubsPlease", "1080p", 1, 10),
		c(3, "SubsPlease", "1080p", 1, 40),
		c(4, "Other", "1080p", 3, 999),
	}
	pick(t, cands, rules(), early, 3, "preferred group SubsPlease")
	// a v2 from the preferred group beats more seeders on v1
	cands = append(cands, c(5, "SubsPlease", "1080p", 2, 1))
	pick(t, cands, rules(), early, 5, "")
	// group names compare case-insensitively
	pick(t, []Candidate{c(6, "subsplease", "1080p", 1, 1), c(7, "Other", "1080p", 1, 99)}, rules(), early, 6, "")
	// second listed group present, first absent: it is chosen without waiting
	pick(t, []Candidate{c(1, "Erai-raws", "1080p", 1, 900), c(4, "Other", "1080p", 3, 999)}, rules(), early, 1, "preferred group Erai-raws")
}

// Rule 3: no listed group and inside the fallback window -> wait.
func TestRule3Wait(t *testing.T) {
	cands := []Candidate{c(1, "Other", "1080p", 1, 100), c(2, "Another", "1080p", 1, 50)}
	pick(t, cands, rules(), early, 0, ReasonWaitingGroup)
	if !IsWaiting(ReasonWaitingGroup) || !IsWaiting(ReasonWaitingRes) || IsWaiting(ReasonFiltered) {
		t.Error("IsWaiting")
	}
	// exactly FallbackHours later: no longer waiting
	got, _ := Pick(cands, rules(), aired.Add(12*time.Hour), aired)
	if got == nil || got.ID != 1 {
		t.Errorf("at the fallback boundary got %v", got)
	}
	// no GroupOrder: nothing to wait for
	r := rules()
	r.GroupOrder = nil
	pick(t, cands, r, early, 1, "fallback: best of the rest")
	// unknown air time: no wait
	got, _ = Pick(cands, rules(), early, time.Time{})
	if got == nil || got.ID != 1 {
		t.Errorf("zero airedAt got %v", got)
	}
}

// Rule 4: best of the rest: version, seeders, newest.
func TestRule4BestOfRest(t *testing.T) {
	cands := []Candidate{c(1, "A", "1080p", 1, 100), c(2, "B", "1080p", 2, 5), c(3, "C", "1080p", 1, 300)}
	pick(t, cands, rules(), late, 2, "fallback: best of the rest")
	cands = []Candidate{c(1, "A", "1080p", 1, 100), c(3, "C", "1080p", 1, 300)}
	pick(t, cands, rules(), late, 3, "")
	// same version and seeders: newest (higher id = later PublishedAt here)
	cands = []Candidate{c(4, "A", "1080p", 1, 100), c(9, "C", "1080p", 1, 100), c(6, "D", "1080p", 1, 100)}
	pick(t, cands, rules(), late, 9, "")
}

// Rule 5: file already on disk.
func TestRule5ShouldReplace(t *testing.T) {
	file := File{Path: "x.mkv", Group: "SubsPlease", Resolution: "1080p", Version: 1}
	r := rules()
	if !ShouldReplace(file, c(1, "SubsPlease", "1080p", 2, 10), r) {
		t.Error("v2 same group should replace")
	}
	if !ShouldReplace(file, c(1, "subsplease", "1080p", 3, 10), r) {
		t.Error("group compare must ignore case")
	}
	if ShouldReplace(file, c(1, "SubsPlease", "1080p", 1, 10), r) {
		t.Error("same version must be skipped")
	}
	if ShouldReplace(File{Group: "SubsPlease", Resolution: "1080p", Version: 3}, c(1, "SubsPlease", "1080p", 2, 10), r) {
		t.Error("lower version must be skipped")
	}
	if ShouldReplace(file, c(1, "Erai-raws", "1080p", 2, 10), r) {
		t.Error("different group must not replace")
	}
	if ShouldReplace(File{Version: 1}, c(1, "SubsPlease", "1080p", 2, 10), r) {
		t.Error("unknown file group must not replace")
	}
	if ShouldReplace(file, c(1, "SubsPlease", "1080p", 2, 0), r) {
		t.Error("below MinSeeders must not replace")
	}
	if ShouldReplace(file, c(1, "SubsPlease", "720p", 2, 10), r) {
		t.Error("resolution unlike both file and target must not replace")
	}
	low := File{Group: "SubsPlease", Resolution: "720p", Version: 1}
	if !ShouldReplace(low, c(1, "SubsPlease", "720p", 2, 10), r) {
		t.Error("v2 at the file's own resolution should replace")
	}
	r.AutoReplaceV2 = false
	if ShouldReplace(file, c(1, "SubsPlease", "1080p", 2, 10), r) {
		t.Error("AutoReplaceV2 off must not replace")
	}
}

func TestInsideRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Anime")
	in := filepath.Join(root, "Show", "ep.mkv")
	cases := []struct {
		root, path string
		want       bool
	}{
		{root, in, true},
		{root, root, false},
		{root, filepath.Join(root, "..", "other.mkv"), false},
		{root, filepath.Join(root, "Show", "..", "..", "x.mkv"), false},
		{root, root + "2" + string(filepath.Separator) + "x.mkv", false},
		{"", in, false},
		{root, "", false},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases, struct {
			root, path string
			want       bool
		}{root, `Z:\elsewhere\x.mkv`, false})
	}
	for _, tc := range cases {
		if got := InsideRoot(tc.root, tc.path); got != tc.want {
			t.Errorf("InsideRoot(%q, %q) = %v, want %v", tc.root, tc.path, got, tc.want)
		}
	}
}

func TestResolutionHeight(t *testing.T) {
	for in, want := range map[string]int{"1080p": 1080, "720P": 720, "1920x1080": 1080, "4K": 2160, "2160p": 2160, "": 0, "junk": 0, "480": 480} {
		if got := ResolutionHeight(in); got != want {
			t.Errorf("ResolutionHeight(%q) = %d, want %d", in, got, want)
		}
	}
}

// SPEC v1.1 section 9: max_release_gib.
func TestMaxSize(t *testing.T) {
	sized := func(cand Candidate, gib float64) Candidate {
		cand.SizeBytes = int64(gib * GiB)
		return cand
	}
	r := rules()
	r.MaxSizeBytes = 4 * GiB
	big := sized(c(1, "SubsPlease", "1080p", 1, 50), 7.2)
	bigger := sized(c(2, "Erai-raws", "1080p", 1, 50), 9)
	pick(t, []Candidate{big, bigger}, r, late, 0, "too big: 7.2 GiB")
	if !TooBig(big, r) {
		t.Error("7.2 GiB should be too big for 4 GiB")
	}
	// a fitting candidate wins even from a less preferred group
	small := sized(c(3, "Erai-raws", "1080p", 1, 5), 1.4)
	pick(t, []Candidate{big, small}, r, late, 3, "preferred group Erai-raws")
	// unknown size (0) is never too big
	unknown := c(4, "Other", "1080p", 1, 5)
	pick(t, []Candidate{big, unknown}, r, late, 4, "")
	// exactly the limit is allowed
	pick(t, []Candidate{sized(c(5, "SubsPlease", "1080p", 1, 5), 4)}, r, late, 5, "")
	// 0 = no limit
	r.MaxSizeBytes = 0
	pick(t, []Candidate{big}, r, late, 1, "preferred group SubsPlease")
	// a too-big candidate at another resolution keeps the old reason
	r.MaxSizeBytes = 4 * GiB
	pick(t, []Candidate{sized(c(6, "SubsPlease", "720p", 1, 5), 9)}, r, late, 0, ReasonFiltered)
	if FormatGiB(7730941132) != "7.2 GiB" {
		t.Errorf("FormatGiB = %q", FormatGiB(7730941132))
	}
}

// SPEC v1.1 section 10: size_preference "small" vs "any".
func TestSizePreference(t *testing.T) {
	sz := func(cand Candidate, gib float64) Candidate {
		cand.SizeBytes = int64(gib * GiB)
		return cand
	}
	r := rules()
	r.GroupOrder = []string{"ASW", "SubsPlease"}
	cases := []struct {
		name        string
		cands       []Candidate
		now         time.Time
		small, anyP int64 // winner with PreferSmall / without
	}{
		{"same group: smallest wins over most seeded",
			[]Candidate{sz(c(1, "SubsPlease", "1080p", 1, 90), 1.4), sz(c(2, "SubsPlease", "1080p", 1, 10), 0.6)}, late, 2, 1},
		{"preferred group beats a smaller unlisted release",
			[]Candidate{sz(c(1, "SubsPlease", "1080p", 1, 5), 1.4), sz(c(2, "Tiny", "1080p", 1, 99), 0.2)}, late, 1, 1},
		{"earlier listed group beats a smaller later group",
			[]Candidate{sz(c(1, "ASW", "1080p", 1, 5), 0.9), sz(c(2, "SubsPlease", "1080p", 1, 99), 0.3)}, late, 1, 1},
		{"unknown size sorts after known sizes",
			[]Candidate{c(1, "SubsPlease", "1080p", 1, 99), sz(c(2, "SubsPlease", "1080p", 1, 3), 1.4)}, late, 2, 1},
		{"two unknown sizes: most seeders",
			[]Candidate{c(1, "SubsPlease", "1080p", 1, 3), c(2, "SubsPlease", "1080p", 1, 30)}, late, 2, 2},
		{"higher version still wins over a smaller v1",
			[]Candidate{sz(c(1, "SubsPlease", "1080p", 1, 50), 0.5), sz(c(2, "SubsPlease", "1080p", 2, 5), 1.4)}, late, 2, 2},
		{"fallback tier: smallest of the rest",
			[]Candidate{sz(c(1, "Big", "1080p", 1, 80), 2.8), sz(c(2, "Judas", "1080p", 1, 4), 0.7), sz(c(3, "Mid", "1080p", 1, 40), 1.3)}, late, 2, 1},
		{"equal size: most seeders",
			[]Candidate{sz(c(1, "SubsPlease", "1080p", 1, 3), 1.4), sz(c(2, "SubsPlease", "1080p", 1, 30), 1.4)}, late, 2, 2},
		{"below min_seeders never wins by size",
			[]Candidate{sz(c(1, "SubsPlease", "1080p", 1, 0), 0.2), sz(c(2, "SubsPlease", "1080p", 1, 8), 1.4)}, late, 2, 2},
		{"still waits for a preferred group",
			[]Candidate{sz(c(1, "Judas", "1080p", 1, 8), 0.4)}, early, 0, 0},
	}
	for _, tc := range cases {
		for _, small := range []bool{true, false} {
			rr := r
			rr.PreferSmall = small
			want := tc.anyP
			if small {
				want = tc.small
			}
			got, reason := Pick(tc.cands, rr, tc.now, aired)
			var id int64
			if got != nil {
				id = got.ID
			}
			if id != want {
				t.Errorf("%s (PreferSmall=%v): got %d (%s), want %d", tc.name, small, id, reason, want)
			}
		}
	}
	// With no target resolution, PreferSmall keeps the higher resolution first.
	rr := r
	rr.Resolution, rr.PreferSmall = "", true
	pick(t, []Candidate{sz(c(1, "SubsPlease", "480p", 1, 9), 0.2), sz(c(2, "SubsPlease", "1080p", 1, 9), 1.4)}, rr, late, 2, "")
}

// Package release picks one release per (show, episode) from the nyaa
// candidates, following the rules in SPEC 5 (internal/release).
package release

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Candidate is one nyaa release for a (show, episode).
//
// Field order and types match sched.Candidate in the core lane, so the two
// convert with a plain struct conversion. Title and EpisodeEnd are carried
// for the caller; Pick does not read them.
type Candidate struct {
	ID          int64
	Title       string
	Group       string
	Resolution  string // "1080p", "" = unknown
	Version     int    // 1 unless v2, v3, ...
	Episode     int
	EpisodeEnd  int
	IsBatch     bool
	IsRecap     bool // parse.Result.IsRecap() (a 05.5 episode); rule 1 drops it
	Seeders     int
	PublishedAt time.Time
	SizeBytes   int64 // 0 = unknown (never too big)
}

// Rules are the effective rules for one show (per-show overrides already
// merged over the global settings by the caller).
type Rules struct {
	GroupOrder                 []string // preferred groups, best first
	Resolution                 string   // target, "" = any
	FallbackHours              float64
	SkipBatchesWhileAiring     bool
	ShowAiring                 bool // show status is RELEASING
	MinSeeders                 int
	AutoReplaceV2              bool
	AllowLowerResAfterFallback bool
	MaxSizeBytes               int64 // candidates bigger than this are never picked; 0 = no limit
	// PreferSmall is conveyor.size_preference == "small" (SPEC v1.1 section 10):
	// inside the chosen tier, among candidates of the same version and
	// resolution, the smallest known size wins; unknown sizes (0) go last.
	PreferSmall bool
}

// File is the episode file already on disk.
type File struct {
	Path       string
	Group      string
	Resolution string
	Version    int
}

// Reasons returned by Pick when it chooses nothing. Callers can compare
// against these to tell "wait" (release.waiting) from "nothing usable".
const (
	ReasonNoCandidates = "no candidates"
	ReasonFiltered     = "no candidate passed the filters"
	ReasonNoResolution = "no candidate at the preferred resolution"
	ReasonWaitingRes   = "waiting for preferred resolution"
	ReasonWaitingGroup = "waiting for preferred group"
	// ReasonTooBigPrefix starts the reason when every candidate that passed the
	// filters is bigger than Rules.MaxSizeBytes: "too big: 7.2 GiB" (the smallest).
	ReasonTooBigPrefix = "too big: "
)

// GiB is 2^30 bytes.
const GiB = 1 << 30

// TooBig reports whether a candidate exceeds the size limit (unknown size never does).
func TooBig(c Candidate, rules Rules) bool {
	return rules.MaxSizeBytes > 0 && c.SizeBytes > rules.MaxSizeBytes
}

// FormatGiB formats a byte count as "7.2 GiB".
func FormatGiB(n int64) string {
	return strconv.FormatFloat(float64(n)/GiB, 'f', 1, 64) + " GiB"
}

// IsWaiting reports whether a Pick reason means "try again later".
func IsWaiting(reason string) bool {
	return reason == ReasonWaitingGroup || reason == ReasonWaitingRes
}

// Pick applies SPEC 5 rules 1-4 and returns the chosen candidate (a pointer
// into cands) and a human-readable reason. chosen is nil when nothing is
// picked; the reason is then one of the Reason* constants.
//
//  1. Drop: recaps, seeders < MinSeeders, batches while airing when
//     SkipBatchesWhileAiring, resolutions other than the target. Lower
//     resolutions (and unknown ones) are kept only when no candidate is at
//     the target, AllowLowerResAfterFallback is set and FallbackHours have
//     passed since airedAt. Higher resolutions are always dropped.
//     Candidates bigger than MaxSizeBytes are dropped too; when that leaves
//     no candidate at the target resolution the reason is "too big: X GiB".
//  2. If any candidate is from a group in GroupOrder: take the group listed
//     earliest among those present; highest version, then most seeders.
//  3. Else, when GroupOrder is not empty and now-airedAt < FallbackHours:
//     wait.
//  4. Else best of the rest: highest version, most seeders, newest.
//
// With PreferSmall the order inside the tier of rule 2 or 4 is: highest
// version, highest resolution, smallest known size (unknown last), most
// seeders, newest. Group order still comes first: a preferred group's
// release beats a smaller one from an unlisted group.
//
// Rule 5 (file already on disk) is ShouldReplace.
func Pick(cands []Candidate, rules Rules, now, airedAt time.Time) (*Candidate, string) {
	if len(cands) == 0 {
		return nil, ReasonNoCandidates
	}
	fallbackPassed := airedAt.IsZero() || now.Sub(airedAt) >= hours(rules.FallbackHours)
	target := ResolutionHeight(rules.Resolution)

	var atTarget, lower []int
	var tooBig int64 // smallest too-big size among candidates at the target resolution
	for i := range cands {
		c := &cands[i]
		if !passesBasic(c, rules) {
			continue
		}
		h := ResolutionHeight(c.Resolution)
		atT := target == 0 || h == target
		if TooBig(*c, rules) {
			if atT && (tooBig == 0 || c.SizeBytes < tooBig) {
				tooBig = c.SizeBytes
			}
			continue
		}
		switch {
		case atT:
			atTarget = append(atTarget, i)
		case h < target: // includes unknown (0)
			lower = append(lower, i)
		}
	}
	pool := atTarget
	lowerRes := false
	if len(pool) == 0 {
		switch {
		case tooBig > 0 && (len(lower) == 0 || !rules.AllowLowerResAfterFallback):
			return nil, ReasonTooBigPrefix + FormatGiB(tooBig)
		case len(lower) == 0:
			return nil, ReasonFiltered
		case !rules.AllowLowerResAfterFallback:
			return nil, ReasonNoResolution
		case !fallbackPassed:
			return nil, ReasonWaitingRes
		}
		pool, lowerRes = lower, true
	}

	// rule 2
	bestRank := -1
	for _, i := range pool {
		if r := groupRank(cands[i].Group, rules.GroupOrder); r >= 0 && (bestRank < 0 || r < bestRank) {
			bestRank = r
		}
	}
	if bestRank >= 0 {
		var preferred []int
		for _, i := range pool {
			if groupRank(cands[i].Group, rules.GroupOrder) == bestRank {
				preferred = append(preferred, i)
			}
		}
		c := &cands[best(cands, preferred, rules.PreferSmall)]
		reason := "preferred group " + c.Group
		if lowerRes {
			reason += " at lower resolution " + c.Resolution
		}
		return c, reason
	}

	// rule 3
	if len(rules.GroupOrder) > 0 && !fallbackPassed {
		return nil, ReasonWaitingGroup
	}

	// rule 4
	c := &cands[best(cands, pool, rules.PreferSmall)]
	reason := "fallback: best of the rest"
	if lowerRes {
		reason = "fallback: lower resolution " + c.Resolution
	}
	return c, reason
}

// ShouldReplace is rule 5 for an episode that already has a file on disk.
// It returns true only when AutoReplaceV2 is on, the candidate is a higher
// version from the same group, the candidate passes the rule-1 filters
// (seeders, recap, batch while airing) and its resolution equals the file's
// or the target. false means skip the candidate.
//
// The caller then marks the old release `replaced` and deletes the old file
// only after the new one is `done`, and only if InsideRoot(root, path).
func ShouldReplace(existing File, cand Candidate, rules Rules) bool {
	if !rules.AutoReplaceV2 || cand.Version <= existing.Version {
		return false
	}
	g := strings.TrimSpace(existing.Group)
	if g == "" || !strings.EqualFold(g, strings.TrimSpace(cand.Group)) {
		return false
	}
	if !passesBasic(&cand, rules) {
		return false
	}
	h := ResolutionHeight(cand.Resolution)
	target := ResolutionHeight(rules.Resolution)
	return h == ResolutionHeight(existing.Resolution) || target == 0 || h == target
}

// InsideRoot reports whether path is strictly inside root (not root itself,
// not a sibling with a common prefix, not reached through ".."). Use it
// before deleting any file. An empty root is never safe.
func InsideRoot(root, path string) bool {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" {
		return false
	}
	ar, err1 := filepath.Abs(root)
	ap, err2 := filepath.Abs(path)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(ar, ap)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func passesBasic(c *Candidate, rules Rules) bool {
	if c.IsRecap || c.Seeders < rules.MinSeeders {
		return false
	}
	return !(c.IsBatch && rules.ShowAiring && rules.SkipBatchesWhileAiring)
}

func hours(h float64) time.Duration { return time.Duration(h * float64(time.Hour)) }

func groupRank(group string, order []string) int {
	g := strings.TrimSpace(group)
	if g == "" {
		return -1
	}
	for i, o := range order {
		if strings.EqualFold(g, strings.TrimSpace(o)) {
			return i
		}
	}
	return -1
}

// best returns the index (into cands) of the best candidate among idx:
// highest version, then most seeders, then newest, then first listed.
// With preferSmall, after the version: highest resolution, then the smallest
// known size (size 0 = unknown sorts after every known size).
func best(cands []Candidate, idx []int, preferSmall bool) int {
	b := idx[0]
	for _, i := range idx[1:] {
		x, y := &cands[i], &cands[b]
		switch {
		case x.Version != y.Version:
			if x.Version > y.Version {
				b = i
			}
		case preferSmall && ResolutionHeight(x.Resolution) != ResolutionHeight(y.Resolution):
			if ResolutionHeight(x.Resolution) > ResolutionHeight(y.Resolution) {
				b = i
			}
		case preferSmall && x.SizeBytes != y.SizeBytes:
			if smaller(x.SizeBytes, y.SizeBytes) {
				b = i
			}
		case x.Seeders != y.Seeders:
			if x.Seeders > y.Seeders {
				b = i
			}
		case x.PublishedAt.After(y.PublishedAt):
			b = i
		}
	}
	return b
}

// smaller reports whether size a sorts before size b: known sizes ascending,
// unknown (0 or negative) after every known size.
func smaller(a, b int64) bool {
	switch {
	case a <= 0:
		return false
	case b <= 0:
		return true
	}
	return a < b
}

var reHeight = regexp.MustCompile(`(?i)^(\d{3,4})p$`)

// ResolutionHeight turns "1080p", "1080", "1920x1080" or "4K" into 1080 /
// 2160; unknown or empty gives 0.
func ResolutionHeight(s string) int {
	s = strings.TrimSpace(strings.ToLower(s))
	switch s {
	case "":
		return 0
	case "4k", "uhd":
		return 2160
	}
	if m := reHeight.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	if _, h, ok := strings.Cut(strings.ReplaceAll(s, "×", "x"), "x"); ok {
		n, _ := strconv.Atoi(h)
		return n
	}
	n, _ := strconv.Atoi(s)
	return n
}

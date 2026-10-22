package sched

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/events"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// Auto-download guard rails (SPEC v1.1 section 9).

// auto_reason values that are not built from a number.
const (
	AutoTurnedOff = "turned off"
	AutoNoAirDate = "no air date"
	AutoRemoved   = "removed in qBittorrent"
)

// noteRemoved is the status note of a release whose torrent the user removed
// in qBittorrent (status skipped since v1.3; migrate_3.sql renames old rows).
const noteRemoved = "removed in qBittorrent"

// ErrBadAuto is returned by SetEpisodeAuto for a value other than nil, 0 or 1.
var ErrBadAuto = errors.New("auto must be null, 0 or 1")

// autoAllowed reports whether an aired episode without a present file and
// without an active release may be downloaded by the conveyor on its own.
// why is the EpisodeRow.auto_reason when it may not.
func autoAllowed(e store.Episode, sh store.Show, now time.Time, c settings.Settings) (ok bool, why string) {
	if e.Auto != nil && *e.Auto == 0 {
		return false, AutoTurnedOff
	}
	if sh.AutoFrom != nil && e.Number < *sh.AutoFrom {
		return false, fmt.Sprintf("before episode %d", *sh.AutoFrom)
	}
	if e.Auto != nil && *e.Auto == 1 {
		return true, ""
	}
	if e.AiredAt == nil {
		return false, AutoNoAirDate
	}
	days := c.Conveyor.LookbackDays
	if *e.AiredAt < now.Add(-time.Duration(days)*24*time.Hour).Unix() {
		return false, olderThan(days)
	}
	return true, ""
}

func olderThan(days int) string {
	if days == 1 {
		return "older than 1 day"
	}
	return fmt.Sprintf("older than %d days", days)
}

// maxAiredNumber is the highest episode number with aired_at <= now.
func maxAiredNumber(eps []store.Episode, now time.Time) int {
	n := 0
	for _, e := range eps {
		if e.AiredAt != nil && *e.AiredAt <= now.Unix() {
			n = max(n, e.Number)
		}
	}
	return n
}

// AutoContext holds what Reason needs for the episodes of one show.
type AutoContext struct {
	c        settings.Settings
	sh       store.Show
	now      time.Time
	maxAired int
}

// NewAutoContext builds the context for the episodes of sh (eps = all its rows).
func NewAutoContext(c settings.Settings, sh store.Show, eps []store.Episode, now time.Time) AutoContext {
	return AutoContext{c: c, sh: sh, now: now, maxAired: maxAiredNumber(eps, now)}
}

// Reason is EpisodeRow.auto_reason: "" when the episode is downloaded by the
// rule or the rule does not matter (not aired yet, a present file, an active
// release, or watched without a file, which the conveyor never fetches);
// otherwise why the conveyor leaves it alone. "turned off" becomes
// "removed in qBittorrent" when the latest handed-off release of the episode
// was skipped because its torrent was removed.
func (a AutoContext) Reason(e store.Episode, hasFile bool, rels []store.Release) string {
	if hasFile || e.WatchedAt != nil || !aired(e, a.sh, a.now, a.maxAired) {
		return ""
	}
	var latest *store.Release
	for i := range rels {
		r := &rels[i]
		if !r.Covers(e.Number) {
			continue
		}
		if activeStatus(r.Status) {
			return ""
		}
		if r.Status != store.RelCandidate && (latest == nil || r.ID > latest.ID) {
			latest = r
		}
	}
	ok, why := autoAllowed(e, a.sh, a.now, a.c)
	if ok {
		return ""
	}
	if why == AutoTurnedOff && latest != nil && latest.Status == store.RelSkipped && strings.HasPrefix(latest.StatusNote, noteRemoved) {
		return AutoRemoved
	}
	return why
}

// turnOffAuto sets auto = 0 on the episode(s) a removed release covered.
func (s *Sched) turnOffAuto(ctx context.Context, r store.Release) {
	off := store.Ptr(0)
	switch {
	case !r.IsBatch && r.Episode != nil:
		s.st.SetEpisodeAuto(ctx, r.ShowID, *r.Episode, off)
	case r.IsBatch && r.Episode == nil:
		s.st.SetEpisodesAutoRange(ctx, r.ShowID, 0, -1, off)
	case r.IsBatch:
		end := *r.Episode
		if r.EpisodeEnd != nil {
			end = *r.EpisodeEnd
		}
		s.st.SetEpisodesAutoRange(ctx, r.ShowID, *r.Episode, end, off)
	}
}

// SetEpisodeAuto sets an episode's auto switch (POST /api/shows/{id}/episodes/{n}/auto):
// nil = follow the rule, 0 = never, 1 = always. Turning it on (or back to the
// rule) queues a conveyor pass for the show.
func (s *Sched) SetEpisodeAuto(ctx context.Context, showID int64, number int, auto *int) error {
	if auto != nil && *auto != 0 && *auto != 1 {
		return ErrBadAuto
	}
	if _, err := s.st.GetShow(ctx, showID); err != nil {
		return err
	}
	if number <= 0 {
		return store.ErrNotFound
	}
	if err := s.st.SetEpisodeAuto(ctx, showID, number, auto); err != nil {
		return err
	}
	if auto == nil || *auto == 1 {
		s.RunConveyorForShow(showID)
	}
	return nil
}

// missingData is what the "missing" set of one show is computed from.
type missingData struct {
	sh       store.Show
	eps      []store.Episode
	have     map[int]bool // episode numbers with a present file
	rels     []store.Release
	now      time.Time
	maxAired int
}

func (s *Sched) loadMissing(ctx context.Context, sh store.Show) (missingData, error) {
	md := missingData{sh: sh, have: map[int]bool{}, now: s.now()}
	var err error
	if md.eps, err = s.st.ListEpisodes(ctx, sh.ID); err != nil {
		return md, err
	}
	files, err := s.st.FilesForShow(ctx, sh.ID)
	if err != nil {
		return md, err
	}
	for _, f := range files {
		if f.Present && f.Episode != nil {
			md.have[*f.Episode] = true
		}
	}
	if md.rels, err = s.st.ListReleases(ctx, sh.ID, nil); err != nil {
		return md, err
	}
	md.maxAired = maxAiredNumber(md.eps, md.now)
	return md, nil
}

// missing reports whether e is "missing": aired, not watched, no present file
// and no active release (added, downloading or done). The same set feeds
// Download all missing and GET /api/missing (SPEC v1.1 sections 9 and 11).
func (md missingData) missing(e store.Episode) bool {
	if !aired(e, md.sh, md.now, md.maxAired) || md.have[e.Number] || e.WatchedAt != nil {
		return false
	}
	for i := range md.rels {
		if r := &md.rels[i]; activeStatus(r.Status) && r.Covers(e.Number) {
			return false
		}
	}
	return true
}

// MissingShow is one show of GET /api/missing: its missing episode numbers,
// ascending (never empty).
type MissingShow struct {
	Show     store.Show
	Episodes []int
}

// Missing lists, for every show with list_status watching, the episodes that
// are aired, not watched, without a present file and without an active
// release. Shows with none are left out. Order: store order (the API sorts).
func (s *Sched) Missing(ctx context.Context) ([]MissingShow, error) {
	shows, err := s.st.ListShows(ctx, "watching")
	if err != nil {
		return nil, err
	}
	out := []MissingShow{}
	for _, sh := range shows {
		md, err := s.loadMissing(ctx, sh)
		if err != nil {
			return nil, err
		}
		var nums []int
		for _, e := range md.eps { // ListEpisodes is ordered by number
			if md.missing(e) {
				nums = append(nums, e.Number)
			}
		}
		if len(nums) > 0 {
			out = append(out, MissingShow{Show: sh, Episodes: nums})
		}
	}
	return out, nil
}

// missingPlan selects, for every aired episode without a present file, not
// watched and without an active release, the release to add: its chosen
// release when there is one (except in manual mode, where chosen is already
// the end state), else release.Pick over its candidates with the show's rules
// but without the auto rule and without max_release_gib. skipped counts
// episodes with no acceptable candidate. A batch covering several episodes is
// listed once.
func (s *Sched) missingPlan(ctx context.Context, c settings.Settings, showID int64) (sh store.Show, picks []store.Release, skipped int, err error) {
	sh, err = s.st.GetShow(ctx, showID)
	if err != nil {
		return sh, nil, 0, err
	}
	md, err := s.loadMissing(ctx, sh)
	if err != nil {
		return sh, nil, 0, err
	}
	now, rels := md.now, md.rels
	planned := map[int64]bool{}
	rules := rulesFor(c, sh, nil)
	rules.MaxSizeBytes = 0
	for _, e := range md.eps {
		if !md.missing(e) {
			continue
		}
		active := false
		var chosen *store.Release
		var cands []Candidate
		for i := range rels {
			r := &rels[i]
			if !r.Covers(e.Number) {
				continue
			}
			switch {
			case activeStatus(r.Status) || planned[r.ID]:
				active = true
			case r.Status == store.RelChosen:
				chosen = r
			case r.Status == store.RelCandidate:
				cands = append(cands, candidateOf(*r))
			}
		}
		if active {
			continue
		}
		if chosen != nil {
			if c.Downloader.Mode != settings.ModeManual {
				planned[chosen.ID] = true
				picks = append(picks, *chosen)
			}
			continue
		}
		pick, _ := s.brain.Pick(cands, rules, now, airedTime(e))
		if pick == nil {
			skipped++
			continue
		}
		for i := range rels {
			if rels[i].ID == pick.ID {
				planned[pick.ID] = true
				picks = append(picks, rels[i])
				break
			}
		}
	}
	return sh, picks, skipped, nil
}

// DownloadMissingPreview is GET /api/shows/{id}/download-missing: how many
// releases "Download all missing" would add and their total size (unknown
// sizes count 0). No side effects.
func (s *Sched) DownloadMissingPreview(ctx context.Context, showID int64) (count int, bytes int64, err error) {
	_, picks, _, err := s.missingPlan(ctx, s.set.Get(), showID)
	if err != nil {
		return 0, 0, err
	}
	for _, p := range picks {
		if p.SizeBytes != nil {
			bytes += *p.SizeBytes
		}
	}
	return len(picks), bytes, nil
}

// DownloadMissing is POST /api/shows/{id}/download-missing: ForceAdd every
// release the preview lists. added counts releases ForceAdd accepted (handed
// to the downloader, or marked chosen in manual mode or when the hand-off
// failed); skipped counts episodes without an acceptable candidate.
func (s *Sched) DownloadMissing(ctx context.Context, showID int64) (added, skipped int, err error) {
	s.bulkMu.Lock()
	defer s.bulkMu.Unlock()
	sh, picks, skipped, err := s.missingPlan(ctx, s.set.Get(), showID)
	if err != nil {
		return 0, 0, err
	}
	for _, p := range picks {
		if _, err := s.ForceAdd(ctx, p.ID); err == nil {
			added++
		}
	}
	s.ev.Log(events.ReleaseAdded, sh.ID, 0, fmt.Sprintf("Download all missing: %d added, %d without a release", added, skipped),
		map[string]any{"bulk": true, "added": added, "skipped": skipped})
	return added, skipped, nil
}

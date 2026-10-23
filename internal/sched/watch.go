package sched

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/events"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// accumulator counts how long each file has been open in a player.
// Time only counts between two consecutive sightings, capped at maxGap, so a
// sleeping PC or a skipped poll does not add hours.
type accumulator struct {
	mu       sync.Mutex
	secs     map[int64]float64
	lastSeen map[int64]time.Time
	marked   map[int64]bool
	prevSeen map[int64]bool
	prev     time.Time
}

func newAccumulator() *accumulator {
	return &accumulator{secs: map[int64]float64{}, lastSeen: map[int64]time.Time{}, marked: map[int64]bool{}, prevSeen: map[int64]bool{}}
}

// Observe records that the files in seen are open at now.
func (a *accumulator) Observe(now time.Time, seen []int64, maxGap time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var dt time.Duration
	if !a.prev.IsZero() {
		dt = min(max(now.Sub(a.prev), 0), maxGap)
	}
	a.prev = now
	cur := make(map[int64]bool, len(seen))
	for _, id := range seen {
		cur[id] = true
		if a.prevSeen[id] {
			a.secs[id] += dt.Seconds()
		} else if _, ok := a.secs[id]; !ok {
			a.secs[id] = 0
		}
		a.lastSeen[id] = now
	}
	a.prevSeen = cur
	for id, t := range a.lastSeen {
		if now.Sub(t) > 6*time.Hour {
			delete(a.secs, id)
			delete(a.lastSeen, id)
			delete(a.marked, id)
		}
	}
}

// Seconds returns the accumulated open time of a file.
func (a *accumulator) Seconds(id int64) float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.secs[id]
}

// TryMark marks id done once; false when it was already marked.
func (a *accumulator) TryMark(id int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.marked[id] {
		return false
	}
	a.marked[id] = true
	return true
}

// openTracker remembers files opened through the app with a player process we can watch.
type openTracker struct {
	mu sync.Mutex
	m  map[int64]<-chan struct{}
}

func newOpenTracker() *openTracker { return &openTracker{m: map[int64]<-chan struct{}{}} }

func (o *openTracker) add(fileID int64, done <-chan struct{}) {
	o.mu.Lock()
	o.m[fileID] = done
	o.mu.Unlock()
}

// alive returns files whose launched player is still running and forgets the rest.
func (o *openTracker) alive() []int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []int64
	for id, done := range o.m {
		select {
		case <-done:
			delete(o.m, id)
		default:
			out = append(out, id)
		}
	}
	return out
}

// runtimeSec is the watched threshold base: duration_min or DefaultRuntimeMin.
func runtimeSec(c settings.Settings, sh store.Show) float64 {
	m := c.Watched.DefaultRuntimeMin
	if sh.DurationMin != nil && *sh.DurationMin > 0 {
		m = *sh.DurationMin
	}
	return float64(m) * 60
}

// matchWindow returns the id of the file whose name appears in a window title
// (longest name wins), or 0. The name without extension counts when it has at
// least 6 characters.
func matchWindow(winTitle string, files []store.File) int64 {
	lt := strings.ToLower(winTitle)
	var best int64
	bestLen := 0
	for _, f := range files {
		base := strings.ToLower(filepath.Base(f.Path))
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		n := 0
		switch {
		case strings.Contains(lt, base):
			n = len(base)
		case len(stem) >= 6 && strings.Contains(lt, stem):
			n = len(stem)
		}
		if n > bestLen {
			best, bestLen = f.ID, n
		}
	}
	return best
}

// playerRun is one poll of the player loop.
func (s *Sched) playerRun(ctx context.Context) error {
	c := s.set.Get()
	if c.Watched.Mode == "manual" {
		return nil
	}
	files, err := s.st.PresentMatchedFiles(ctx)
	if err != nil {
		return err
	}
	byID := make(map[int64]store.File, len(files))
	for _, f := range files {
		byID[f.ID] = f
	}
	seen := map[int64]bool{}
	for _, w := range s.windows(c.Watched.PlayerTitles) {
		if id := matchWindow(w.Title, files); id != 0 {
			seen[id] = true
		}
	}
	for _, id := range s.opens.alive() {
		seen[id] = true
	}
	ids := make([]int64, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	poll := time.Duration(c.Watched.PollSec) * time.Second
	s.acc.Observe(s.now(), ids, 3*poll)
	for _, id := range ids {
		f, ok := byID[id]
		if !ok {
			continue
		}
		sh, err := s.st.GetShow(ctx, *f.ShowID)
		if err != nil {
			continue
		}
		threshold := 0.0
		if c.Watched.Mode == "fraction" {
			threshold = c.Watched.Fraction * runtimeSec(c, sh)
		}
		if s.acc.Seconds(id) >= threshold && s.acc.TryMark(id) {
			if _, err := s.SetWatched(ctx, sh.ID, *f.Episode, true, "player"); err != nil {
				return err
			}
		}
	}
	return nil
}

// SetWatched marks or unmarks an episode. Marking logs episode.watched and queues
// list pushes; unmarking logs episode.unwatched. changed is false for no-ops.
func (s *Sched) SetWatched(ctx context.Context, showID int64, ep int, watched bool, source string) (bool, error) {
	sh, err := s.st.GetShow(ctx, showID)
	if err != nil {
		return false, err
	}
	if watched {
		changed, err := s.st.SetWatched(ctx, showID, ep, s.now().Unix(), source)
		if err != nil || !changed {
			return changed, err
		}
		s.ev.Log(events.EpisodeWatched, showID, ep, title(sh)+" "+epLabel(ep)+" watched", map[string]any{"source": source})
		s.push.enqueue(showID)
		return true, nil
	}
	changed, err := s.st.ClearWatched(ctx, showID, ep)
	if err != nil || !changed {
		return changed, err
	}
	s.ev.Log(events.EpisodeUnwatched, showID, ep, title(sh)+" "+epLabel(ep)+" marked unwatched", nil)
	return true, nil
}

// ThroughError is returned by WatchedThrough for an episode number out of range.
// Msg is the field message (fields.number in the API reply).
type ThroughError struct{ Msg string }

func (e *ThroughError) Error() string { return "number " + e.Msg }

// throughSlack is how far past the last episode "watched through" may reach.
const throughSlack = 50

// WatchedThrough marks episodes 1..through of a show watched (source "manual",
// watched_at now) and never unmarks one (POST /api/shows/{id}/watched-through,
// SPEC v1.1 section 11). The writes reuse the single-episode SQL in one
// transaction; afterwards one episode.watched event is logged and ONE list push
// is queued per service (the pusher sends the highest watched number), not one
// per episode. newly = episodes it marked; when 0 nothing is logged or pushed.
// Bounds: through >= 1 and <= episodes_total + 50 when the total is known, else
// <= the highest known episode (row or present file) + 50.
func (s *Sched) WatchedThrough(ctx context.Context, showID int64, through int) (newly int, err error) {
	sh, err := s.st.GetShow(ctx, showID)
	if err != nil {
		return 0, err
	}
	if through < 1 {
		return 0, &ThroughError{Msg: "must be 1 or more"}
	}
	if sh.EpisodesTotal != nil && *sh.EpisodesTotal > 0 {
		if limit := *sh.EpisodesTotal + throughSlack; through > limit {
			return 0, &ThroughError{Msg: fmt.Sprintf("must be at most %d (the show has %d episodes)", limit, *sh.EpisodesTotal)}
		}
	} else {
		known, err := s.st.MaxEpisodeNumber(ctx, showID)
		if err != nil {
			return 0, err
		}
		if sh.NextAiringEp != nil {
			known = max(known, *sh.NextAiringEp)
		}
		if limit := known + throughSlack; through > limit {
			return 0, &ThroughError{Msg: fmt.Sprintf("must be at most %d (%d past the highest known episode)", limit, throughSlack)}
		}
	}
	newly, err = s.st.SetWatchedThrough(ctx, showID, through, s.now().Unix(), "manual")
	if err != nil || newly == 0 {
		return newly, err
	}
	s.ev.Log(events.EpisodeWatched, showID, through, fmt.Sprintf("%s: episodes 1-%d marked watched (%d new)", title(sh), through, newly),
		map[string]any{"through": through, "newly": newly, "bulk": true})
	s.push.enqueue(showID)
	return newly, nil
}

// ErrFileMissing is returned when a file row exists but the file is not on disk.
var ErrFileMissing = errors.New("file is not on disk")

// OpenFile opens a file in the configured player or the OS default handler
// (POST /api/files/{id}/open). A player we launch ourselves is tracked so the
// fraction rule counts while it runs even if its window title is not recognised.
// In on_open mode the episode is marked watched right away.
func (s *Sched) OpenFile(ctx context.Context, id int64) error {
	_, err := s.OpenFileAt(ctx, id)
	return err
}

// OpenResult says how OpenFileAt launched the file.
type OpenResult struct {
	SkippedSec int    `json:"skipped_sec"` // seconds skipped at the start (0 = opened from the beginning)
	WantedSec  int    `json:"wanted_sec"`  // seconds the rules asked to skip (0 = none); > 0 with SkippedSec 0 = the player has no known start flag
	Player     string `json:"player"`      // program name used, "" when unknown
	Tested     bool   `json:"tested"`      // the start flag was verified on a real machine (SMPlayer); false = from the player's docs
}

// OpenFileAt opens file id in the player, skipping the seconds the rules give
// (Show.IntroSkipSec, else open.intro_skip_sec for files tagged as
// streaming-service rips) when the player has a known start flag.
func (s *Sched) OpenFileAt(ctx context.Context, id int64) (OpenResult, error) {
	var res OpenResult
	f, err := s.st.GetFile(ctx, id)
	if err != nil {
		return res, err
	}
	if _, err := os.Stat(f.Path); err != nil {
		return res, ErrFileMissing
	}
	c := s.set.Get()
	var show *store.Show
	if f.ShowID != nil {
		if sh, err := s.st.GetShow(ctx, *f.ShowID); err == nil {
			show = &sh
		}
	}
	res.WantedSec = introSkipFor(c, show, f.Path)
	done, player, skipped, err := s.launch.OpenAt(c.Open.PlayerPath, f.Path, res.WantedSec)
	if err != nil {
		return res, err
	}
	res.Player = player
	if skipped {
		res.SkippedSec = res.WantedSec
		_, _, res.Tested, _ = StartArgs(player, res.WantedSec)
	}
	if done != nil {
		s.opens.add(f.ID, done)
	}
	if c.Watched.Mode == "on_open" && f.ShowID != nil && f.Episode != nil {
		if s.acc.TryMark(f.ID) {
			if _, err := s.SetWatched(ctx, *f.ShowID, *f.Episode, true, "player"); err != nil {
				return res, err
			}
		}
	}
	return res, nil
}

// RevealFile shows the file in the OS file manager (POST /api/files/{id}/reveal).
func (s *Sched) RevealFile(ctx context.Context, id int64) error {
	f, err := s.st.GetFile(ctx, id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(f.Path); err != nil {
		return ErrFileMissing
	}
	return s.launch.Reveal(f.Path)
}

// Launcher starts external programs. Tests replace it so nothing ever opens.
type Launcher interface {
	// Open plays file with playerPath ("" = OS default handler). done is closed
	// when the launched player exits; nil when the player cannot be tracked.
	Open(playerPath, file string) (done <-chan struct{}, err error)
	// OpenAt is Open with a start position (seconds). When ABC knows the
	// player's start flag (StartArgs) the player gets it and skipped is true;
	// otherwise the file opens like Open. player = the program name used.
	OpenAt(playerPath, file string, startSec int) (done <-chan struct{}, player string, skipped bool, err error)
	// Reveal shows file selected in the OS file manager.
	Reveal(file string) error
	// OpenDir opens a folder in the OS file manager.
	OpenDir(dir string) error
	// OpenURL hands a URL (here: a magnet link) to the OS handler for its scheme.
	OpenURL(u string) error
}

// SetLauncher replaces the launcher (tests).
func (s *Sched) SetLauncher(l Launcher) { s.launch = l }

// SetMagnetHandler replaces the magnet handler lookup (tests).
func (s *Sched) SetMagnetHandler(fn func() (string, error)) { s.magnetHandler = fn }

// OpenFolder opens a folder in the OS file manager. The caller checks that the
// folder is one ABC may open; this only refuses folders that do not exist.
func (s *Sched) OpenFolder(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return ErrFolderMissing
	}
	return s.launch.OpenDir(dir)
}

// ErrFolderMissing is returned by OpenFolder for a folder that is not on disk.
var ErrFolderMissing = errors.New("folder does not exist")

// MagnetHandler returns the program registered for magnet: links (Windows
// registry; "unknown" on other systems) or ErrNoMagnetHandler.
func (s *Sched) MagnetHandler() (string, error) { return s.magnetHandler() }

// Package sched runs the background loops (SPEC section 6): airing refresh,
// conveyor (nyaa -> release pick -> qBittorrent), qBittorrent sync, folder scan,
// player watch and list pushes. It also holds the actions the API triggers.
package sched

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/diskfree"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/events"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/player"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/qbit"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/scan"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// Sched owns the loops. Create with New, then Start; Stop on shutdown.
type Sched struct {
	st    *store.DB
	set   *settings.Manager
	ev    *events.Logger
	brain Brain

	// Hooks for tests; defaults are the real thing.
	now        func() time.Time
	windows    func(filters []string) []player.Playing
	launch     Launcher
	nyaaSpace  time.Duration // pause between nyaa requests (SPEC 4: >= 2 s)
	scanDelay  time.Duration // watcher debounce (SPEC 6: 3 s)
	pushRetry  time.Duration // delay before a failed push is retried
	airingIval time.Duration
	diskFree   func(path string) (int64, error) // free bytes on the volume holding path (SPEC v1.1 section 10)

	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started atomic.Bool

	airing, conveyor, qsync, scanL, playerL *loop

	qmu       sync.Mutex
	qb        *qbit.Client
	qbKey     string
	qbOK      bool
	qbErr     string
	qbChecked time.Time

	fileMu     sync.Mutex // serialises writes to the files table (full scan, watcher, qbit path scans)
	wmu        sync.Mutex
	watcher    *scan.Watcher
	watchRoots []string // library roots that existed when the watcher started

	addMu  sync.Mutex // serialises release adds
	bulkMu sync.Mutex // serialises Download all missing (SPEC v1.1 section 9)

	noteMu sync.Mutex
	noted  map[string]string // "show:ep" -> last waiting/skipped reason logged

	acc   *accumulator
	opens *openTracker
	push  *pusher

	magnetHandler func() (string, error) // registered magnet: handler lookup (tests replace it)
}

// New builds a scheduler. brain may be nil (DefaultBrain is used).
func New(st *store.DB, set *settings.Manager, ev *events.Logger, brain Brain) *Sched {
	if brain == nil {
		brain = DefaultBrain()
	}
	s := &Sched{
		st: st, set: set, ev: ev, brain: brain,
		now:        time.Now,
		windows:    player.Current,
		launch:     osLauncher{},
		nyaaSpace:  2 * time.Second,
		scanDelay:  3 * time.Second,
		pushRetry:  time.Minute,
		airingIval: 6 * time.Hour,
		diskFree:   diskfree.Free,
		noted:      map[string]string{},
		acc:        newAccumulator(),
		opens:      newOpenTracker(),

		magnetHandler: osMagnetHandler,
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.push = newPusher(s)
	s.airing = &loop{name: "airing", run: s.airingRun, interval: func(settings.Settings) time.Duration { return s.airingIval }}
	s.conveyor = &loop{name: "conveyor", run: func(ctx context.Context) error { return s.conveyorRun(ctx, 0) },
		interval: func(c settings.Settings) time.Duration {
			if !c.Conveyor.Enabled {
				return 0
			}
			return time.Duration(c.Conveyor.PollIntervalMin) * time.Minute
		}}
	s.qsync = &loop{name: "qbit", run: s.qbitRun, interval: func(c settings.Settings) time.Duration {
		if c.Downloader.Mode != settings.ModeQbit {
			return 0
		}
		return time.Minute
	}}
	s.scanL = &loop{name: "scan", run: func(ctx context.Context) error { return s.scanRun(ctx, false) },
		interval: func(c settings.Settings) time.Duration { return time.Duration(c.Library.ScanIntervalMin) * time.Minute }}
	s.playerL = &loop{name: "player", run: s.playerRun, interval: func(c settings.Settings) time.Duration {
		if c.Watched.Mode == "manual" {
			return 0
		}
		return time.Duration(c.Watched.PollSec) * time.Second
	}}
	for _, l := range s.loops() {
		l.reset = make(chan struct{}, 1)
	}
	set.OnChange(s.settingsChanged)
	return s
}

func (s *Sched) loops() []*loop {
	return []*loop{s.airing, s.conveyor, s.qsync, s.scanL, s.playerL}
}

// Brain returns the brain in use (the API calls search/season/auth through it).
func (s *Sched) Brain() Brain { return s.brain }

// Start launches every loop (each runs once now, then on its interval) and the watcher.
func (s *Sched) Start() {
	s.started.Store(true)
	for _, l := range s.loops() {
		s.wg.Add(1)
		go s.runLoop(l)
	}
	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.push.run(s.ctx) }()
	s.restartWatcher()
}

// Stop cancels all loops and waits for them.
func (s *Sched) Stop() {
	s.cancel()
	s.wg.Wait()
	s.wmu.Lock()
	if s.watcher != nil {
		s.watcher.Close()
		s.watcher = nil
	}
	s.wmu.Unlock()
}

// loop is one periodic job. mu makes sure a loop never overlaps itself.
type loop struct {
	name     string
	mu       sync.Mutex
	interval func(settings.Settings) time.Duration // 0 = disabled until settings change
	run      func(ctx context.Context) error
	reset    chan struct{}

	emu     sync.Mutex
	lastErr string
}

func (s *Sched) runLoop(l *loop) {
	defer s.wg.Done()
	iv := l.interval(s.set.Get())
	if iv > 0 {
		s.tryRun(l)
	}
	for {
		var tick <-chan time.Time
		var t *time.Timer
		if iv > 0 {
			t = time.NewTimer(iv)
			tick = t.C
		}
		select {
		case <-s.ctx.Done():
			if t != nil {
				t.Stop()
			}
			return
		case <-l.reset:
			if t != nil {
				t.Stop()
			}
			prev := iv
			iv = l.interval(s.set.Get())
			if prev == 0 && iv > 0 {
				s.tryRun(l) // just enabled: run now
			}
		case <-tick:
			s.tryRun(l)
		}
	}
}

// tryRun runs l unless it is already running. Returns false when skipped.
func (s *Sched) tryRun(l *loop) bool {
	if !l.mu.TryLock() {
		return false
	}
	defer l.mu.Unlock()
	s.runLocked(l, l.run)
	return true
}

// runLocked runs fn (caller holds l.mu) and logs an error event when the error
// message differs from the previous run's (no event spam while a service is down).
func (s *Sched) runLocked(l *loop, fn func(context.Context) error) {
	err := fn(s.ctx)
	if s.ctx.Err() != nil {
		return
	}
	l.emu.Lock()
	defer l.emu.Unlock()
	if err == nil {
		l.lastErr = ""
		return
	}
	slog.Warn("loop failed", "loop", l.name, "err", err)
	if msg := err.Error(); msg != l.lastErr {
		l.lastErr = msg
		s.ev.Log(events.Error, 0, 0, l.name+": "+msg, map[string]any{"loop": l.name})
	}
}

// startAsync starts l in a goroutine unless it is running. Returns whether it started.
func (s *Sched) startAsync(l *loop, fn func(context.Context) error) bool {
	if !l.mu.TryLock() {
		return false
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer l.mu.Unlock()
		s.runLocked(l, fn)
	}()
	return true
}

// queue runs fn under l's mutex, waiting for a running pass to finish first.
func (s *Sched) queue(l *loop, fn func(context.Context) error) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		l.mu.Lock()
		defer l.mu.Unlock()
		if s.ctx.Err() == nil {
			s.runLocked(l, fn)
		}
	}()
}

// RunConveyorNow starts a conveyor pass (POST /api/conveyor/run). Manual runs work
// even when Conveyor.Enabled is off. Returns false when a pass is already running.
func (s *Sched) RunConveyorNow() bool {
	return s.startAsync(s.conveyor, func(ctx context.Context) error { return s.conveyorRun(ctx, 0) })
}

// RunConveyorForShow queues a conveyor pass for one show (after add / refresh).
func (s *Sched) RunConveyorForShow(showID int64) {
	s.queue(s.conveyor, func(ctx context.Context) error { return s.conveyorRun(ctx, showID) })
}

// RunScanNow starts a full scan (POST /api/scan/run). Returns false when one is running.
func (s *Sched) RunScanNow() bool {
	return s.startAsync(s.scanL, func(ctx context.Context) error { return s.scanRun(ctx, true) })
}

// rescanSoon queues a full scan so files get re-matched after shows change.
func (s *Sched) rescanSoon() {
	s.queue(s.scanL, func(ctx context.Context) error { return s.scanRun(ctx, false) })
}

func (s *Sched) settingsChanged(old, new settings.Settings) {
	for _, l := range s.loops() {
		select {
		case l.reset <- struct{}{}:
		default:
		}
	}
	newDirs := new.Library.ActiveDirs()
	// libraryDirs carries the scan depth too: changing a folder's depth re-watches and rescans.
	if !slices.Equal(libraryDirs(old), libraryDirs(new)) || !slices.Equal(old.Library.VideoExts, new.Library.VideoExts) {
		s.hideRemovedFolders(old, newDirs)
		if s.started.Load() {
			s.restartWatcher()
			s.rescanSoon()
		}
	}
	if old.Qbit.URL != new.Qbit.URL || old.Qbit.Username != new.Qbit.Username || old.Qbit.Password != new.Qbit.Password ||
		old.Downloader.Mode != new.Downloader.Mode {
		s.qmu.Lock()
		s.qb, s.qbKey, s.qbOK, s.qbErr, s.qbChecked = nil, "", false, "", time.Time{}
		s.qmu.Unlock()
	}
	ctx := context.Background()
	if old.Push.AniList != new.Push.AniList {
		s.st.SetAccountPush(ctx, "anilist", new.Push.AniList)
	}
	if old.Push.MAL != new.Push.MAL {
		s.st.SetAccountPush(ctx, "mal", new.Push.MAL)
	}
}

// libraryDirs returns the active library folders with their scan depth, in list
// order, no duplicates (SPEC v1.1 sections 1 and 8).
func libraryDirs(c settings.Settings) []scan.Dir {
	var out []scan.Dir
	for _, f := range c.Library.Folders {
		if !f.Active || f.Path == "" {
			continue
		}
		if slices.ContainsFunc(out, func(d scan.Dir) bool { return settings.SamePath(d.Path, f.Path) }) {
			continue
		}
		out = append(out, scan.Dir{Path: f.Path, MaxDepth: f.ScanDepth()})
	}
	return out
}

// hideRemovedFolders marks files missing (present=0, no per-file events) when
// their folder left the active list and no still-active folder contains them,
// then logs one scan.done event per folder.
func (s *Sched) hideRemovedFolders(old settings.Settings, newDirs []string) {
	var removed []string
	for _, d := range old.Library.ActiveDirs() {
		if !slices.ContainsFunc(newDirs, func(n string) bool { return settings.SamePath(n, d) }) {
			removed = append(removed, d)
		}
	}
	if len(removed) == 0 {
		return
	}
	ctx := context.Background()
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	all, err := s.st.AllFiles(ctx)
	if err != nil {
		slog.Warn("hide removed folder", "err", err)
		return
	}
	for _, d := range removed {
		var ids []int64
		for _, f := range all {
			if f.Present && scan.Under(f.Path, d) && !underAny(f.Path, newDirs) {
				ids = append(ids, f.ID)
			}
		}
		if err := s.st.MarkFilesMissing(ctx, ids); err != nil {
			slog.Warn("hide removed folder", "dir", d, "err", err)
			continue
		}
		name := d
		if i, ok := old.Library.Find(d); ok && old.Library.Folders[i].Label != "" {
			name = old.Library.Folders[i].Label
		}
		s.ev.Log(events.ScanDone, 0, 0, fmt.Sprintf("Folder %s removed from the library: %d files hidden", name, len(ids)),
			map[string]any{"path": d, "hidden": len(ids)})
	}
}

func (s *Sched) restartWatcher() {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if s.watcher != nil {
		s.watcher.Close()
		s.watcher = nil
	}
	c := s.set.Get()
	dirs := libraryDirs(c)
	s.watchRoots = reachable(scan.Paths(dirs))
	if len(dirs) == 0 {
		return
	}
	w, err := scan.NewWatcher(dirs, c.Library.VideoExts, s.scanDelay, s.onFileSeen, s.onFileGone)
	if err != nil {
		slog.Warn("watcher not started", "err", err)
		return
	}
	s.watcher = w
}

// reachable returns the dirs that exist right now.
func reachable(dirs []string) []string {
	var out []string
	for _, d := range dirs {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			out = append(out, d)
		}
	}
	return out
}

// watchRootsChanged reports whether the set of readable library roots differs
// from the one the watcher was started with (a drive was plugged in or out).
func (s *Sched) watchRootsChanged(walked []string) bool {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return !slices.Equal(walked, s.watchRoots)
}

// sleep waits d or until ctx ends; false when ctx ended.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// noteOnce logs a release.waiting / release.skipped event for (show, ep) only
// when the reason changed since the last one.
func (s *Sched) noteOnce(kind string, show store.Show, ep int, reason string) {
	s.noteOnceKey(kind, show, ep, reason, reason, map[string]any{"reason": reason})
}

// noteOnceKey is noteOnce with a separate dedupe key, for reasons whose text
// carries a changing number (the free-space message): the event is logged
// only when kind+key differs from the last note of the episode.
func (s *Sched) noteOnceKey(kind string, show store.Show, ep int, key, reason string, data map[string]any) {
	k := itoa64(show.ID) + ":" + itoa(ep)
	s.noteMu.Lock()
	same := s.noted[k] == kind+key
	s.noted[k] = kind + key
	s.noteMu.Unlock()
	if !same {
		s.ev.Log(kind, show.ID, ep, title(show)+" "+epLabel(ep)+": "+reason, data)
	}
}

func (s *Sched) clearNote(showID int64, ep int) {
	s.noteMu.Lock()
	delete(s.noted, itoa64(showID)+":"+itoa(ep))
	s.noteMu.Unlock()
}

// WaitIdle blocks until every queued or running pass has finished. Only valid when
// Start has not been called (tests), because the loop goroutines never finish.
func (s *Sched) WaitIdle() { s.wg.Wait() }

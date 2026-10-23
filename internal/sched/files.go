package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/events"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/qbit"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/scan"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// ---- scan loop ----

// scanRun walks the active library folders (each down to its scan depth), parses +
// matches every video file, upserts rows, marks vanished files missing (only under
// folders that could be read) and logs file.found / file.missing. Files that are
// now deeper than their folder's scan depth are hidden without per-file events.
// manual runs always log scan.done.
func (s *Sched) scanRun(ctx context.Context, manual bool) error {
	walked, err := s.scanLocked(ctx, manual)
	// A library drive was plugged in or out since the watcher started: re-watch.
	// Done after fileMu is released (closing the watcher waits for its callbacks).
	if walked != nil && s.started.Load() && s.watchRootsChanged(walked) {
		s.restartWatcher()
	}
	return err
}

// scanLocked is scanRun's body. Settings are read after taking fileMu so a scan
// that waited behind a folder removal never walks the removed folder.
func (s *Sched) scanLocked(ctx context.Context, manual bool) ([]string, error) {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	c := s.set.Get()
	dirs := libraryDirs(c)
	if len(dirs) == 0 {
		if manual {
			s.ev.Log(events.ScanDone, 0, 0, "Scan skipped: no library folder set", nil)
		}
		return nil, nil
	}
	walked, err := s.scanDirs(ctx, c, dirs, manual)
	if walked == nil {
		walked = []string{}
	}
	return walked, err
}

func (s *Sched) scanDirs(ctx context.Context, c settings.Settings, dirs []scan.Dir, manual bool) ([]string, error) {
	all, err := s.st.AllFiles(ctx)
	if err != nil {
		return nil, err
	}
	known := make(map[string]store.File, len(all))
	for _, f := range all {
		known[f.Path] = f
	}
	shows, err := s.st.ListShows(ctx, "")
	if err != nil {
		return nil, err
	}
	titles := s.titles(ctx, shows)
	seen := map[string]bool{}
	var upserts []store.File
	var touch []int64
	walked, err := scan.Walk(ctx, dirs, c.Library.VideoExts, func(fs scan.FileSeen) {
		seen[fs.Path] = true
		f := s.buildFile(fs, titles)
		if old, had := known[fs.Path]; had && sameFile(old, f) {
			touch = append(touch, old.ID)
			return
		}
		upserts = append(upserts, f)
	})
	if err != nil {
		return nil, err
	}
	if err := s.st.UpsertFiles(ctx, upserts); err != nil {
		return nil, err
	}
	if err := s.st.TouchFiles(ctx, touch, s.now().Unix()); err != nil {
		return nil, err
	}
	// A file not seen under a readable folder is gone when that folder's depth
	// covers it. When no active folder's depth covers it (the depth was lowered),
	// it is hidden quietly: present=0, no file.missing event per file. A file
	// covered only by an unreadable folder is left alone.
	var walkedDirs []scan.Dir
	for _, d := range dirs {
		if slices.Contains(walked, d.Path) {
			walkedDirs = append(walkedDirs, d)
		}
	}
	var gone []store.File
	var hidden []int64
	for _, old := range all {
		if !old.Present || seen[old.Path] || !underAny(old.Path, walked) {
			continue
		}
		switch {
		case scan.Covered(old.Path, walkedDirs):
			gone = append(gone, old)
		case !scan.Covered(old.Path, dirs):
			hidden = append(hidden, old.ID)
		}
	}
	if err := s.markGone(ctx, gone); err != nil {
		return nil, err
	}
	if err := s.st.MarkFilesMissing(ctx, hidden); err != nil {
		return nil, err
	}
	found := 0
	for _, f := range upserts {
		old, had := known[f.Path]
		if newlyMatched(old, had, f) {
			s.fileFound(ctx, f)
			found++
		}
	}
	if manual || found > 0 || len(gone) > 0 || len(hidden) > 0 {
		msg := fmt.Sprintf("Scan: %d video files, %d new episode files, %d missing", len(seen), found, len(gone))
		if len(hidden) > 0 {
			msg += fmt.Sprintf(", %d hidden (deeper than the folder's scan depth)", len(hidden))
		}
		s.ev.Log(events.ScanDone, 0, 0, msg,
			map[string]any{"files": len(seen), "found": found, "missing": len(gone), "hidden": len(hidden)})
	}
	return walked, nil
}

func underAny(p string, roots []string) bool {
	for _, r := range roots {
		if scan.Under(p, r) {
			return true
		}
	}
	return false
}

func sameFile(old, f store.File) bool {
	return old.Present && old.Size == f.Size && old.Mtime == f.Mtime && eqInt64(old.ShowID, f.ShowID) &&
		eqInt(old.Episode, f.Episode) && old.Version == f.Version && old.ReleaseGroup == f.ReleaseGroup && old.Resolution == f.Resolution
}

func newlyMatched(old store.File, had bool, f store.File) bool {
	if f.ShowID == nil || f.Episode == nil {
		return false
	}
	return !had || !old.Present || !eqInt64(old.ShowID, f.ShowID) || !eqInt(old.Episode, f.Episode)
}

// buildFile parses + matches one file into a files row (not yet written).
func (s *Sched) buildFile(fs scan.FileSeen, titles []ShowTitles) store.File {
	r := s.brain.Parse(filepath.Base(fs.Path))
	f := store.File{Path: fs.Path, Size: fs.Size, Mtime: fs.Mtime, ReleaseGroup: r.Group, Resolution: r.Resolution, Version: max(r.Version, 1)}
	if b, err := json.Marshal(r); err == nil {
		f.Parsed = b
	}
	if sid, ep, ok := s.matchFile(r, fs.Path, titles); ok {
		id := int64(sid)
		f.ShowID, f.Episode = &id, &ep
	}
	return f
}

// matchFile matches a parsed file name. When the name alone does not match but
// has an episode number, the parent folder name supplies the title
// (Root/<Show>/05.mkv). TODO(spec): folder fallback is not in SPEC 6.
func (s *Sched) matchFile(r ParseResult, path string, titles []ShowTitles) (int, int, bool) {
	if s.brain.IsRecap(r) || (r.IsBatch && r.Episode == 0) {
		return 0, 0, false
	}
	if sid, ep, ok, _, _ := s.brain.Match(r, titles); ok && ep > 0 {
		return sid, ep, true
	}
	if r.Episode <= 0 {
		return 0, 0, false
	}
	dir := s.brain.Parse(filepath.Base(filepath.Dir(path)))
	if dir.Title == "" {
		return 0, 0, false
	}
	r2 := r
	r2.Title = dir.Title
	if r2.Season == 0 {
		r2.Season = dir.Season
	}
	if sid, ep, ok, _, _ := s.brain.Match(r2, titles); ok && ep > 0 {
		return sid, ep, true
	}
	return 0, 0, false
}

// ingest handles one file reported by the watcher or a download scan. Caller holds fileMu.
func (s *Sched) ingest(ctx context.Context, fs scan.FileSeen, titles []ShowTitles) {
	f := s.buildFile(fs, titles)
	old, err := s.st.GetFileByPath(ctx, fs.Path)
	had := err == nil
	if had && sameFile(old, f) {
		s.st.TouchFiles(ctx, []int64{old.ID}, s.now().Unix())
		return
	}
	batch := []store.File{f}
	if err := s.st.UpsertFiles(ctx, batch); err != nil {
		s.ev.Log(events.Error, 0, 0, "scan: "+err.Error(), map[string]any{"path": fs.Path})
		return
	}
	if newlyMatched(old, had, batch[0]) {
		s.fileFound(ctx, batch[0])
	}
}

func (s *Sched) currentTitles(ctx context.Context) []ShowTitles {
	shows, err := s.st.ListShows(ctx, "")
	if err != nil {
		return nil
	}
	return s.titles(ctx, shows)
}

// titles is Titles with the highest known episode numbers read in one query
// (one per conveyor or scan run, not per show). A failed read leaves
// EpisodesKnown 0.
func (s *Sched) titles(ctx context.Context, shows []store.Show) []ShowTitles {
	known, err := s.st.MaxEpisodeNumbers(ctx)
	if err != nil {
		known = nil
	}
	return Titles(shows, known)
}

// onFileSeen is the watcher callback for a new or changed video file.
func (s *Sched) onFileSeen(fs scan.FileSeen) {
	ctx := s.ctx
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	// The watcher may still report a folder that just left the library, or a
	// file deeper than the folder's scan depth allows.
	if !scan.Covered(fs.Path, libraryDirs(s.set.Get())) {
		return
	}
	s.ingest(ctx, fs, s.currentTitles(ctx))
}

// onFileGone is the watcher callback for a vanished file or folder.
func (s *Sched) onFileGone(p string) {
	ctx := s.ctx
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	// A whole library root vanishing is an unplugged drive or network share, not
	// deleted episodes: keep its files (the next scan skips it the same way).
	for _, d := range s.set.Get().Library.ActiveDirs() {
		if scan.Under(d, p) {
			return
		}
	}
	all, err := s.st.AllFiles(ctx)
	if err != nil {
		return
	}
	var gone []store.File
	for _, f := range all {
		if f.Present && (f.Path == p || scan.Under(f.Path, p)) {
			gone = append(gone, f)
		}
	}
	s.markGone(ctx, gone)
}

func (s *Sched) markGone(ctx context.Context, gone []store.File) error {
	if len(gone) == 0 {
		return nil
	}
	ids := make([]int64, len(gone))
	for i, g := range gone {
		ids[i] = g.ID
	}
	if err := s.st.MarkFilesMissing(ctx, ids); err != nil {
		return err
	}
	for _, g := range gone {
		if g.ShowID == nil {
			continue
		}
		sh, _ := s.st.GetShow(ctx, *g.ShowID)
		ep := deref(g.Episode)
		// Deleted or moved outside ABC. A finished release is never picked again, so say so.
		next := "ABC will not download it again by itself; press Add on the episode if you want it back"
		if e, err := s.st.GetEpisode(ctx, *g.ShowID, ep); err == nil && e.WatchedAt != nil {
			next = "already watched, so nothing is lost"
		}
		s.ev.Log(events.FileMissing, *g.ShowID, ep, title(sh)+" "+epLabel(ep)+": file gone from disk ("+filepath.Base(g.Path)+"). "+next+".",
			map[string]any{"file_id": g.ID, "path": g.Path})
	}
	return nil
}

// fileFound logs file.found for a newly matched file and links releases.
func (s *Sched) fileFound(ctx context.Context, f store.File) {
	sid, ep := *f.ShowID, *f.Episode
	s.st.EnsureEpisode(ctx, sid, ep)
	sh, _ := s.st.GetShow(ctx, sid)
	s.ev.Log(events.FileFound, sid, ep, title(sh)+" "+epLabel(ep)+" on disk ("+filepath.Base(f.Path)+")",
		map[string]any{"file_id": f.ID, "path": f.Path, "release_group": f.ReleaseGroup, "resolution": f.Resolution, "version": f.Version})
	s.clearNote(sid, ep)
	rels, err := s.st.ListReleases(ctx, sid, &ep)
	if err == nil {
		qbitOn := s.set.Get().Downloader.Mode == settings.ModeQbit
		for _, r := range rels {
			// A manual (chosen) release is done once its episode shows up. Releases in
			// qBittorrent are finished by the qbit sync loop; releases handed to another
			// app (magnet / watch folder: no qbit hash) or left over after qBittorrent
			// was switched off are finished here by the file appearing.
			inFlight := r.Status == store.RelAdded || r.Status == store.RelDownloading
			if r.Status == store.RelChosen || (inFlight && (!qbitOn || r.QbitHash == "")) {
				s.st.SetReleaseStatus(ctx, r.ID, store.RelDone, r.StatusNote)
			}
		}
	}
	s.finishReplacements(ctx, sid, ep)
}

// scanPath ingests one file or folder (a finished download's content path).
func (s *Sched) scanPath(ctx context.Context, p string) {
	if p == "" {
		return
	}
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	titles := s.currentTitles(ctx)
	fi, err := os.Stat(p)
	if err != nil {
		return
	}
	c := s.set.Get()
	exts := c.Library.VideoExts
	// Inside the library, the folder's scan depth applies (the full scan would
	// hide a deeper file again); a path outside every library folder is
	// ingested as before.
	dirs := libraryDirs(c)
	inScope := func(path string) bool { return scan.Covered(path, dirs) || !underAny(path, scan.Paths(dirs)) }
	if !fi.IsDir() {
		if scan.HasExt(p, scan.ExtSet(exts)) && inScope(p) {
			s.ingest(ctx, scan.FileSeen{Path: p, Size: fi.Size(), Mtime: fi.ModTime().Unix()}, titles)
		}
		return
	}
	var found []scan.FileSeen
	scan.Walk(ctx, []scan.Dir{{Path: p, MaxDepth: -1}}, exts, func(fs scan.FileSeen) {
		if inScope(fs.Path) {
			found = append(found, fs)
		}
	})
	for _, fs := range found {
		s.ingest(ctx, fs, titles)
	}
}

// finishReplacements deletes the old file of a finished v2 replacement once a
// newer file for the episode is on disk. Only files inside the download folder are deleted.
func (s *Sched) finishReplacements(ctx context.Context, showID int64, ep int) {
	rels, err := s.st.ListReleases(ctx, showID, &ep)
	if err != nil {
		return
	}
	root := s.set.Get().Library.DownloadDir()
	for _, r := range rels {
		if r.Status != store.RelDone || !strings.HasPrefix(r.StatusNote, replacesPrefix) {
			continue
		}
		idText, _, _ := strings.Cut(strings.TrimPrefix(r.StatusNote, replacesPrefix), ";")
		oldID, err := strconv.ParseInt(strings.TrimSpace(idText), 10, 64)
		if err != nil {
			continue
		}
		old, err := s.st.GetFile(ctx, oldID)
		if err != nil {
			continue
		}
		files, _ := s.st.FilesForShow(ctx, showID)
		newer := false
		for _, f := range files {
			if f.Present && f.ID != old.ID && f.Episode != nil && *f.Episode == ep && f.Version > old.Version {
				newer = true
			}
		}
		if !newer {
			continue // new file not on disk yet; try again on the next file.found
		}
		note := "replaced file " + idText
		if old.Present && r.QbitHash == "" {
			// Handed to another torrent app (magnet / watch folder): ABC cannot tell
			// whether the new file is complete, so the old one is never deleted.
			note += " (old file kept: the new download was not tracked by qBittorrent)"
		} else if old.Present && root != "" && scan.Under(old.Path, root) && filepath.Clean(old.Path) != filepath.Clean(root) {
			if err := os.Remove(old.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.ev.Log(events.Error, showID, ep, "could not delete old version "+old.Path+": "+err.Error(), nil)
				continue
			}
			s.markGone(ctx, []store.File{old})
			note += " (old file deleted)"
		} else if old.Present {
			note += " (old file kept: outside the download folder)"
		}
		s.st.SetReleaseStatus(ctx, r.ID, store.RelDone, note)
	}
}

// ---- qbit sync loop ----

// qbitRun polls qBittorrent for releases that are added / downloading.
func (s *Sched) qbitRun(ctx context.Context) error {
	c := s.set.Get()
	if c.Downloader.Mode != settings.ModeQbit {
		return nil
	}
	rels, err := s.st.ReleasesByStatus(ctx, store.RelAdded, store.RelDownloading)
	if err != nil || len(rels) == 0 {
		return err
	}
	qb := s.qbitClient(c)
	for _, r := range rels {
		if r.QbitHash == "" {
			continue
		}
		p, state, content, err := qb.Status(ctx, r.QbitHash)
		ep := deref(r.Episode)
		if errors.Is(err, qbit.ErrNotFound) {
			s.dropProgress(r.QbitHash)
			sh, _ := s.st.GetShow(ctx, r.ShowID)
			// SPEC v1.1 section 9: the user removed it, so the episode stops auto-downloading.
			// SPEC v1.3 section 2.1: that is a skip, not a failure.
			s.st.SetReleaseStatus(ctx, r.ID, store.RelSkipped, noteRemoved)
			s.turnOffAuto(ctx, r)
			s.ev.Log(events.ReleaseSkipped, r.ShowID, ep, title(sh)+" "+epLabel(ep)+": torrent is no longer in qBittorrent; not downloading it again (press Add to retry)",
				map[string]any{"release_id": r.ID, "auto": 0})
			continue
		}
		if err != nil {
			s.setQbitState(false, err)
			return err
		}
		s.setQbitState(true, nil)
		s.setProgress(r.QbitHash, p)
		switch {
		case p >= 1:
			s.dropProgress(r.QbitHash)
			sh, _ := s.st.GetShow(ctx, r.ShowID)
			s.st.SetReleaseStatus(ctx, r.ID, store.RelDone, r.StatusNote)
			s.ev.Log(events.DownloadDone, r.ShowID, ep, title(sh)+" "+epLabel(ep)+": download finished", map[string]any{"release_id": r.ID, "path": content})
			if content == "" {
				content = SavePath(c, sh)
			}
			s.scanPath(ctx, content)
			if ep > 0 {
				s.fileMu.Lock()
				s.finishReplacements(ctx, r.ShowID, ep)
				s.fileMu.Unlock()
			}
		case qbit.FailedState(state):
			s.dropProgress(r.QbitHash)
			sh, _ := s.st.GetShow(ctx, r.ShowID)
			s.st.SetReleaseStatus(ctx, r.ID, store.RelFailed, "qBittorrent state "+state)
			s.ev.Log(events.DownloadFailed, r.ShowID, ep, title(sh)+" "+epLabel(ep)+": qBittorrent reports "+state, map[string]any{"release_id": r.ID})
		case r.Status == store.RelAdded:
			s.st.SetReleaseStatus(ctx, r.ID, store.RelDownloading, r.StatusNote)
		}
	}
	return nil
}

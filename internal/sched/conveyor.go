package sched

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/diskfree"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/events"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/qbit"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// conveyorRun fetches nyaa RSS for each watching show with auto download on
// (only = one show id, 0 = all), stores candidates, then picks and adds releases.
func (s *Sched) conveyorRun(ctx context.Context, only int64) error {
	c := s.set.Get()
	shows, err := s.st.ListShows(ctx, "")
	if err != nil {
		return err
	}
	titles := s.titles(ctx, shows)
	byID := map[int64]store.Show{}
	for _, sh := range shows {
		byID[sh.ID] = sh
	}
	var errs []error
	fetched := 0
	budget := s.newBudget(ctx, c)
	for _, sh := range shows {
		if only != 0 && sh.ID != only {
			continue
		}
		if sh.ListStatus != "watching" || !autoDownload(sh) {
			continue
		}
		if fetched > 0 && !sleep(ctx, s.nyaaSpace) {
			return ctx.Err()
		}
		fetched++
		items, err := s.brain.NyaaSearch(ctx, c.Conveyor.NyaaBase, NyaaQuery(sh), c.Conveyor.NyaaCategory)
		if err != nil {
			errs = append(errs, fmt.Errorf("nyaa %q: %w", NyaaQuery(sh), err))
			continue
		}
		newByShow := s.storeCandidates(ctx, c, items, titles, byID)
		for id, n := range newByShow {
			s.ev.Log(events.ReleaseFound, id, 0, fmt.Sprintf("%d new release(s) for %s", n, title(byID[id])), map[string]any{"count": n})
		}
		if err := s.pickForShow(ctx, c, sh, budget); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", title(sh), err))
		}
	}
	if len(errs) > 0 {
		return firstErrs(errs)
	}
	return nil
}

// storeCandidates parses + matches RSS items and upserts them as candidates.
// Returns the number of new rows per show.
func (s *Sched) storeCandidates(ctx context.Context, c settings.Settings, items []NyaaItem, titles []ShowTitles, byID map[int64]store.Show) map[int64]int {
	cutoff := s.now().Add(-time.Duration(c.Conveyor.LookbackDays) * 24 * time.Hour)
	out := map[int64]int{}
	for _, it := range items {
		if !it.PublishedAt.IsZero() && it.PublishedAt.Before(cutoff) {
			continue
		}
		r := s.brain.Parse(it.Title)
		sid, ep, ok, _, shift := s.brain.Match(r, titles)
		if !ok {
			continue
		}
		rel := store.Release{
			ShowID: int64(sid), NyaaID: it.ID, Title: it.Title, InfoHash: strings.ToLower(it.InfoHash),
			TorrentURL: it.TorrentURL, ReleaseGroup: r.Group, Resolution: r.Resolution, Version: max(r.Version, 1),
			IsBatch: r.IsBatch, Seeders: store.Ptr(it.Seeders), Leechers: store.Ptr(it.Leechers),
			PublishedAt: it.PublishedAt.Unix(),
		}
		if it.SizeBytes > 0 {
			rel.SizeBytes = store.Ptr(it.SizeBytes)
		}
		if it.PublishedAt.IsZero() {
			rel.PublishedAt = s.now().Unix()
		}
		if ep > 0 {
			rel.Episode = store.Ptr(ep)
		}
		if r.IsBatch && r.EpisodeEnd > 0 {
			// the range end is read the same way as its start (SPEC v1.3 section 1.3)
			rel.EpisodeEnd = store.Ptr(r.EpisodeEnd - shift)
		}
		if s.brain.IsRecap(r) {
			rel.StatusNote = "recap"
		}
		if rel.InfoHash != "" {
			rel.Magnet = s.brain.Magnet(rel.InfoHash, it.Title)
		}
		isNew, err := s.st.UpsertRelease(ctx, &rel)
		if err != nil {
			continue
		}
		if isNew {
			out[rel.ShowID]++
		}
	}
	return out
}

// aired reports whether episode e counts as aired at now. TODO(spec): SPEC only says
// aired_at <= now; also counted as aired: episodes of FINISHED shows without a date,
// and undated episodes numbered below the highest aired one (AniList schedules can
// start mid-season, e.g. Frieren has nodes for 5-28 only).
func aired(e store.Episode, sh store.Show, now time.Time, maxAired int) bool {
	if e.AiredAt != nil {
		return *e.AiredAt <= now.Unix()
	}
	return sh.Status == "FINISHED" || e.Number < maxAired
}

func airedTime(e store.Episode) time.Time {
	if e.AiredAt == nil {
		return time.Time{}
	}
	return time.Unix(*e.AiredAt, 0)
}

func activeStatus(st string) bool {
	return st == store.RelAdded || st == store.RelDownloading || st == store.RelDone
}

// rulesFor builds the effective rules for a show (per-show overrides first).
func rulesFor(c settings.Settings, sh store.Show, file *store.File) Rules {
	r := Rules{
		GroupOrder: c.Conveyor.GroupOrder, Resolution: c.Conveyor.Resolution, FallbackHours: c.Conveyor.FallbackHours,
		SkipBatchesWhileAiring: c.Conveyor.SkipBatchesWhileAiring, Airing: sh.Status == "RELEASING",
		AutoReplaceV2: c.Conveyor.AutoReplaceV2, MinSeeders: c.Conveyor.MinSeeders,
		MaxSizeBytes: int64(c.Conveyor.MaxReleaseGiB * (1 << 30)),
		PreferSmall:  c.Conveyor.SizePreference != settings.SizeAny,
	}
	if sh.GroupOrder != nil {
		r.GroupOrder = sh.GroupOrder
	}
	if sh.Resolution != "" {
		r.Resolution = sh.Resolution
	}
	if file != nil {
		r.HaveFile, r.FileVersion, r.FileGroup, r.FileResolution = true, file.Version, file.ReleaseGroup, file.Resolution
	}
	return r
}

func candidateOf(r store.Release) Candidate {
	c := Candidate{ID: r.ID, Title: r.Title, Group: r.ReleaseGroup, Resolution: r.Resolution, Version: r.Version,
		IsBatch: r.IsBatch, IsRecap: r.StatusNote == "recap", PublishedAt: time.Unix(r.PublishedAt, 0)}
	if r.Episode != nil {
		c.Episode = *r.Episode
	}
	if r.EpisodeEnd != nil {
		c.EpisodeEnd = *r.EpisodeEnd
	}
	if r.Seeders != nil {
		c.Seeders = *r.Seeders
	}
	if r.SizeBytes != nil {
		c.SizeBytes = *r.SizeBytes
	}
	return c
}

// pickForShow runs release.Pick for every aired episode that has no file and no
// active release (or, with AutoReplaceV2, a file with a lower version). An
// episode without a file is handled only when autoAllowed (SPEC v1.1 section 9);
// the others get no events and their chosen releases are not re-sent.
func (s *Sched) pickForShow(ctx context.Context, c settings.Settings, sh store.Show, budget *diskBudget) error {
	eps, err := s.st.ListEpisodes(ctx, sh.ID)
	if err != nil {
		return err
	}
	files, err := s.st.FilesForShow(ctx, sh.ID)
	if err != nil {
		return err
	}
	best := map[int]*store.File{}
	for i := range files {
		f := &files[i]
		if !f.Present || f.Episode == nil {
			continue
		}
		if b := best[*f.Episode]; b == nil || f.Version > b.Version {
			best[*f.Episode] = f
		}
	}
	rels, err := s.st.ListReleases(ctx, sh.ID, nil)
	if err != nil {
		return err
	}
	now := s.now()
	maxAired := maxAiredNumber(eps, now)
	for _, e := range eps {
		if !aired(e, sh, now, maxAired) {
			continue
		}
		file := best[e.Number]
		// TODO(spec): watched episodes without a file are not downloaded again.
		if file == nil && e.WatchedAt != nil {
			continue
		}
		var active, chosen *store.Release
		var cands []Candidate
		for i := range rels {
			r := &rels[i]
			if !r.Covers(e.Number) {
				continue
			}
			switch {
			case activeStatus(r.Status):
				if active == nil || active.Status == store.RelDone {
					active = r // an in-flight download wins over a finished one
				}
			case r.Status == store.RelChosen:
				chosen = r
			case r.Status == store.RelCandidate:
				cands = append(cands, candidateOf(*r))
			}
		}
		if active != nil && (file == nil || active.Version > file.Version || active.Status != store.RelDone) {
			continue
		}
		if file != nil && !c.Conveyor.AutoReplaceV2 {
			continue
		}
		if file == nil {
			if ok, _ := autoAllowed(e, sh, now, c); !ok {
				continue
			}
		}
		if chosen != nil {
			if c.Downloader.Mode != settings.ModeManual && file == nil && s.roomFor(c, sh, e.Number, *chosen, budget) {
				s.addRelease(ctx, c, sh, *chosen, nil)
			}
			continue
		}
		if len(cands) == 0 {
			continue
		}
		pick, reason := s.brain.Pick(cands, rulesFor(c, sh, file), now, airedTime(e))
		if pick == nil {
			if file == nil && reason != "" {
				kind := events.ReleaseSkipped
				if strings.HasPrefix(strings.ToLower(reason), "waiting") { // release.IsWaiting reasons start with "waiting"
					kind = events.ReleaseWaiting
				}
				s.noteOnce(kind, sh, e.Number, reason)
			}
			continue
		}
		var rel *store.Release
		for i := range rels {
			if rels[i].ID == pick.ID {
				rel = &rels[i]
			}
		}
		if rel == nil {
			continue
		}
		if file != nil && rel.Version <= file.Version {
			continue // brain picked something not better than the file on disk
		}
		if !s.roomFor(c, sh, e.Number, *rel, budget) {
			continue // stays a candidate; picked again when there is room
		}
		updated, _ := s.addRelease(ctx, c, sh, *rel, file)
		*rel = updated
		s.clearNote(sh.ID, e.Number)
	}
	return nil
}

// lowDiskKey is the noteOnce key of the free-space wait, so the event is not
// logged again every pass while the free GiB figure in its text changes.
const lowDiskKey = "low disk space"

// diskBudget is the free-space ledger of one conveyor pass: per volume, the
// bytes that downloads already in flight still need, plus the full size of
// every release this pass has handed off. The guard subtracts it from the
// free figure the OS reports, so a pass with 3 GiB free cannot start ten
// 700 MiB episodes that then all stall at a fraction. A smaller release later
// in the pass can still fit when a bigger one did not.
type diskBudget struct {
	reserved map[string]int64 // volume -> bytes spoken for
}

// downloadDirFor is the folder the guard reads for a show: qBittorrent mode =
// the show's save path (the download folder unless the show has an absolute
// folder), magnet / torrent_file = DownloadDir(); "" = nothing is downloaded.
func downloadDirFor(c settings.Settings, sh store.Show) string {
	switch c.Downloader.Mode {
	case settings.ModeQbit:
		return SavePath(c, sh)
	case settings.ModeMagnet, settings.ModeTorrentFile:
		return c.Library.DownloadDir()
	}
	return ""
}

// newBudget reserves, per volume, what every added / downloading release still
// needs: its size minus the progress qBittorrent last reported (full size when
// no progress is known yet, or in magnet / torrent_file mode where another app
// downloads). Releases without a size reserve nothing. Store errors leave the
// budget empty, so the guard falls back to the plain free-space check.
func (s *Sched) newBudget(ctx context.Context, c settings.Settings) *diskBudget {
	b := &diskBudget{reserved: map[string]int64{}}
	if !(c.Conveyor.MinFreeGiB > 0) || c.Downloader.Mode == settings.ModeManual {
		return b
	}
	rels, err := s.st.ReleasesByStatus(ctx, store.RelAdded, store.RelDownloading)
	if err != nil {
		return b
	}
	shows := map[int64]store.Show{}
	for _, r := range rels {
		if r.SizeBytes == nil || *r.SizeBytes <= 0 {
			continue
		}
		sh, ok := shows[r.ShowID]
		if !ok {
			sh, err = s.st.GetShow(ctx, r.ShowID)
			if err != nil {
				continue
			}
			shows[r.ShowID] = sh
		}
		dir := downloadDirFor(c, sh)
		if dir == "" {
			continue
		}
		remaining := *r.SizeBytes - int64(float64(*r.SizeBytes)*s.progressOf(r.QbitHash))
		if remaining > 0 {
			b.reserved[diskfree.Volume(dir)] += remaining
		}
	}
	return b
}

// roomFor is the free-space guard before an AUTOMATIC hand-off (SPEC v1.1
// section 10): false (and one release.waiting event per episode) when
// free - reserved - size_bytes < conveyor.min_free_gib, where reserved is the
// pass budget (running downloads + releases handed off earlier in the pass).
// On true the release's size joins the budget. ForceAdd and Download all
// missing do not call it. No download folder, manual mode (nothing is
// downloaded), min_free_gib 0 or a free space that cannot be read = no check.
func (s *Sched) roomFor(c settings.Settings, sh store.Show, ep int, rel store.Release, budget *diskBudget) bool {
	if !(c.Conveyor.MinFreeGiB > 0) {
		return true
	}
	dir := downloadDirFor(c, sh)
	if dir == "" {
		return true
	}
	free, err := s.diskFree(dir)
	if err != nil {
		return true
	}
	var size int64
	if rel.SizeBytes != nil && *rel.SizeBytes > 0 {
		size = *rel.SizeBytes
	}
	vol := diskfree.Volume(dir)
	var reserved int64
	if budget != nil {
		reserved = budget.reserved[vol]
	}
	keep := int64(c.Conveyor.MinFreeGiB * (1 << 30))
	if free-reserved-size >= keep {
		if budget != nil {
			budget.reserved[vol] += size
		}
		return true
	}
	gib := func(n int64) string { return strconv.FormatFloat(float64(max(n, 0))/(1<<30), 'f', 1, 64) + " GiB" }
	reason := "waiting, only " + gib(free) + " free on " + vol
	if reserved > 0 {
		reason += " and " + gib(reserved) + " of downloads still running"
	}
	reason += " (keeps " + strconv.FormatFloat(c.Conveyor.MinFreeGiB, 'f', -1, 64) + " GiB free)"
	s.noteOnceKey(events.ReleaseWaiting, sh, ep, lowDiskKey, reason, map[string]any{
		"reason": lowDiskKey, "free_bytes": free, "reserved_bytes": reserved, "min_free_gib": c.Conveyor.MinFreeGiB,
		"size_bytes": size, "volume": vol, "release_id": rel.ID,
	})
	return false
}

// qbitClient returns a client for the current settings (re-created when they change).
func (s *Sched) qbitClient(c settings.Settings) *qbit.Client {
	key := c.Qbit.URL + "\x00" + c.Qbit.Username + "\x00" + c.Qbit.Password
	s.qmu.Lock()
	defer s.qmu.Unlock()
	if s.qb == nil || s.qbKey != key {
		s.qb, s.qbKey = qbit.New(c.Qbit.URL, c.Qbit.Username, c.Qbit.Password), key
	}
	return s.qb
}

func (s *Sched) setQbitState(ok bool, err error) {
	s.qmu.Lock()
	s.qbOK, s.qbChecked = ok, s.now()
	s.qbErr = ""
	if err != nil {
		s.qbErr = err.Error()
	}
	s.qmu.Unlock()
}

// QbitState reports whether qBittorrent is the downloader and reachable. When the
// last check is older than 30 s it checks again (2 s timeout).
func (s *Sched) QbitState(ctx context.Context) (enabled, connected bool) {
	c := s.set.Get()
	if c.Downloader.Mode != settings.ModeQbit {
		return false, false
	}
	s.qmu.Lock()
	fresh := !s.qbChecked.IsZero() && s.now().Sub(s.qbChecked) < 30*time.Second
	ok := s.qbOK
	s.qmu.Unlock()
	if fresh {
		return true, ok
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := s.qbitClient(c).Version(cctx)
	s.setQbitState(err == nil, err)
	return true, err == nil
}

// qbitErr returns the last qBittorrent error message.
func (s *Sched) qbitErr() string {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	return s.qbErr
}

// DownloaderState reports whether the configured downloader can take releases
// (GET /api/state): qbittorrent = connected; magnet = a handler is registered
// (Windows; always true elsewhere); torrent_file = the watch folder exists;
// manual = true. msg explains a false ok.
func (s *Sched) DownloaderState(ctx context.Context) (mode string, ok bool, msg string) {
	c := s.set.Get()
	mode = c.Downloader.Mode
	switch mode {
	case settings.ModeQbit:
		if _, connected := s.QbitState(ctx); !connected {
			msg = "qBittorrent is not reachable"
			if e := s.qbitErr(); e != "" {
				msg += ": " + e
			}
			return mode, false, msg
		}
		return mode, true, ""
	case settings.ModeMagnet:
		if _, err := s.magnetHandler(); err != nil {
			return mode, false, err.Error()
		}
		return mode, true, ""
	case settings.ModeTorrentFile:
		if c.Downloader.TorrentDir == "" {
			return mode, false, "no watch folder set"
		}
		if !dirExists(c.Downloader.TorrentDir) {
			return mode, false, "watch folder not reachable: " + c.Downloader.TorrentDir
		}
		return mode, true, ""
	}
	return mode, true, ""
}

// QbitTest checks the saved qBittorrent settings (GET /api/settings/test/qbit).
func (s *Sched) QbitTest(ctx context.Context) (string, error) {
	c := s.set.Get()
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	v, err := qbit.New(c.Qbit.URL, c.Qbit.Username, c.Qbit.Password).Version(cctx)
	if c.Downloader.Mode == settings.ModeQbit {
		s.setQbitState(err == nil, err)
	}
	return v, err
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// replacesNote marks a release that replaces an older file once it is done.
const replacesPrefix = "replaces file "

// joinNote appends part to a status note ("a; b").
func joinNote(note, part string) string {
	if note == "" {
		return part
	}
	if part == "" {
		return note
	}
	return note + "; " + part
}

// addRelease hands a release to the configured downloader (SPEC v1.1 section 2):
// qBittorrent (status added + hash), the OS magnet handler or a .torrent file in
// the watch folder (status added, no hash; the scanner finishes them by file).
// In manual mode, or when the hand-off fails, the release is marked chosen so the
// UI offers the magnet. replaces is the file a v2 release supersedes.
func (s *Sched) addRelease(ctx context.Context, c settings.Settings, sh store.Show, rel store.Release, replaces *store.File) (store.Release, error) {
	s.addMu.Lock()
	defer s.addMu.Unlock()
	cur, err := s.st.GetRelease(ctx, rel.ID)
	if err != nil {
		return rel, err
	}
	if activeStatus(cur.Status) {
		return cur, nil
	}
	magnet := cur.Magnet
	if magnet == "" && cur.InfoHash != "" {
		magnet = s.brain.Magnet(cur.InfoHash, cur.Title)
	}
	base := ""
	if replaces != nil {
		base = replacesPrefix + strconv.FormatInt(replaces.ID, 10)
	}
	ep := deref(cur.Episode)
	mode := c.Downloader.Mode
	label := title(sh) + " " + epLabel(ep) + ": "
	data := func(manual bool, note string) map[string]any {
		return map[string]any{"release_id": cur.ID, "title": cur.Title, "manual": manual, "magnet": magnet, "note": note, "mode": mode}
	}
	added := func(hash, note, msg string) (store.Release, error) {
		if err := s.st.SetReleaseQbit(ctx, cur.ID, store.RelAdded, hash, note); err != nil {
			return cur, err
		}
		s.markReplaced(ctx, sh.ID, ep, replaces, cur.ID)
		s.ev.Log(events.ReleaseAdded, sh.ID, ep, label+msg+" ("+groupRes(cur)+")", data(false, note))
		return s.st.GetRelease(ctx, cur.ID)
	}
	byMagnet := func() (store.Release, string, error) {
		if magnet == "" {
			return cur, "no magnet link", nil
		}
		if err := s.launch.OpenURL(magnet); err != nil {
			return cur, "magnet link handler: " + err.Error(), nil
		}
		r, err := added("", joinNote(base, "sent to the magnet link handler"), "sent to your torrent app (magnet link)")
		return r, "", err
	}

	var fail string // why the hand-off did not happen ("" in manual mode)
	switch mode {
	case settings.ModeQbit:
		dl := c.Library.DownloadDir()
		switch {
		case dl != "" && !dirExists(dl):
			fail = "download folder not reachable: " + dl
		case magnet == "":
			fail = "no magnet link"
		default:
			hash, err := s.qbitClient(c).Add(ctx, magnet, SavePath(c, sh), c.Qbit.Category)
			s.setQbitState(err == nil, err)
			if err == nil {
				return added(hash, base, "sent to qBittorrent")
			}
			fail = "qBittorrent: " + err.Error()
		}
	case settings.ModeMagnet:
		r, why, err := byMagnet()
		if why == "" {
			return r, err
		}
		fail = why
	case settings.ModeTorrentFile:
		if cur.TorrentURL == "" {
			r, why, err := byMagnet()
			if why == "" {
				return r, err
			}
			fail = why
			break
		}
		name, err := s.saveTorrent(ctx, c.Downloader.TorrentDir, cur)
		if err == nil {
			return added("", joinNote(base, "saved "+name+" to the watch folder"), ".torrent saved to the watch folder")
		}
		fail = ".torrent: " + err.Error()
	}
	if fail == "" && magnet == "" && cur.TorrentURL == "" {
		fail = "no magnet or torrent link"
	}
	note := joinNote(base, fail)
	// A chosen release is retried every conveyor pass; log again only when the
	// reason changed, so an unplugged drive or a closed torrent app does not
	// add the same event every 15 minutes.
	repeat := cur.Status == store.RelChosen && cur.StatusNote == note
	if err := s.st.SetReleaseQbit(ctx, cur.ID, store.RelChosen, "", note); err != nil {
		return cur, err
	}
	s.markReplaced(ctx, sh.ID, ep, replaces, cur.ID)
	if !repeat {
		s.ev.Log(events.ReleaseAdded, sh.ID, ep, label+"chosen, add it manually ("+groupRes(cur)+")", data(true, note))
	}
	return s.st.GetRelease(ctx, cur.ID)
}

// saveTorrent downloads rel's .torrent and writes <dir>/<SafeName(title)>.torrent
// through a .part name and a rename, so a watching torrent app never reads half a
// file. Returns the file name.
func (s *Sched) saveTorrent(ctx context.Context, dir string, rel store.Release) (string, error) {
	if dir == "" {
		return "", errors.New("no watch folder set")
	}
	if !dirExists(dir) {
		return "", errors.New("watch folder not reachable: " + dir)
	}
	body, err := s.brain.FetchTorrent(ctx, rel.TorrentURL)
	if err != nil {
		return "", err
	}
	name := SafeName(rel.Title) + ".torrent"
	final := filepath.Join(dir, name)
	part := final + ".part"
	if err := os.WriteFile(part, body, 0o644); err != nil {
		os.Remove(part)
		return "", err
	}
	if err := os.Rename(part, final); err != nil {
		os.Remove(part)
		return "", err
	}
	return name, nil
}

// markReplaced marks done releases of the same episode as replaced when a v2 is chosen.
func (s *Sched) markReplaced(ctx context.Context, showID int64, ep int, replaces *store.File, newID int64) {
	if replaces == nil || ep <= 0 {
		return
	}
	rels, err := s.st.ListReleases(ctx, showID, &ep)
	if err != nil {
		return
	}
	for _, r := range rels {
		if r.ID != newID && r.Status == store.RelDone {
			s.st.SetReleaseStatus(ctx, r.ID, store.RelReplaced, "replaced by release "+strconv.FormatInt(newID, 10))
		}
	}
}

func groupRes(r store.Release) string {
	parts := []string{}
	if r.ReleaseGroup != "" {
		parts = append(parts, r.ReleaseGroup)
	}
	if r.Resolution != "" {
		parts = append(parts, r.Resolution)
	}
	if r.Version > 1 {
		parts = append(parts, "v"+strconv.Itoa(r.Version))
	}
	if len(parts) == 0 {
		return r.Title
	}
	return strings.Join(parts, " ")
}

// ForceAdd adds a candidate to qBittorrent now (POST /api/releases/{id}/add).
func (s *Sched) ForceAdd(ctx context.Context, id int64) (store.Release, error) {
	rel, err := s.st.GetRelease(ctx, id)
	if err != nil {
		return rel, err
	}
	sh, err := s.st.GetShow(ctx, rel.ShowID)
	if err != nil {
		return rel, err
	}
	if activeStatus(rel.Status) {
		return rel, errors.New("release is already " + rel.Status)
	}
	if rel.Status != store.RelCandidate && rel.Status != store.RelChosen {
		// skipped / failed / replaced: back to candidate so addRelease accepts it
		if err := s.st.SetReleaseStatus(ctx, id, store.RelCandidate, ""); err != nil {
			return rel, err
		}
	}
	return s.addRelease(ctx, s.set.Get(), sh, rel, nil)
}

// Skip marks a release skipped (POST /api/releases/{id}/skip).
func (s *Sched) Skip(ctx context.Context, id int64) (store.Release, error) {
	rel, err := s.st.GetRelease(ctx, id)
	if err != nil {
		return rel, err
	}
	if err := s.st.SetReleaseStatus(ctx, id, store.RelSkipped, "skipped by user"); err != nil {
		return rel, err
	}
	ep := deref(rel.Episode)
	sh, _ := s.st.GetShow(ctx, rel.ShowID)
	s.ev.Log(events.ReleaseSkipped, rel.ShowID, ep, title(sh)+" "+epLabel(ep)+": skipped "+groupRes(rel), map[string]any{"release_id": id})
	return s.st.GetRelease(ctx, id)
}

package api

import (
	"context"
	"path/filepath"
	"sort"

	"github.com/Nostramadeus/AnimeBeltConveyor/internal/sched"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/settings"
	"github.com/Nostramadeus/AnimeBeltConveyor/internal/store"
)

// ShowCard is the list/grid shape (SPEC 8).
type ShowCard struct {
	ID            int64    `json:"id"`
	AniListID     int64    `json:"anilist_id"`
	Title         string   `json:"title"`
	TitleRomaji   string   `json:"title_romaji"`
	TitleEnglish  string   `json:"title_english"`
	CoverURL      string   `json:"cover_url"`
	BannerURL     string   `json:"banner_url"`
	Color         string   `json:"color"`
	Status        string   `json:"status"`
	Format        string   `json:"format"`
	EpisodesTotal *int     `json:"episodes_total"`
	ListStatus    string   `json:"list_status"`
	Score         *float64 `json:"score"`
	Progress      Progress `json:"progress"`
	Next          *NextEp  `json:"next"`
	LastActivity  int64    `json:"last_activity"`
}

type Progress struct {
	Watched int `json:"watched"`
	OnDisk  int `json:"on_disk"`
	Aired   int `json:"aired"`
}

type NextEp struct {
	Episode int   `json:"episode"`
	AiredAt int64 `json:"aired_at"`
}

// ShowFull is ShowCard plus every per-show field (ShowDetail.show, PATCH reply).
type ShowFull struct {
	ShowCard
	MALID            int64    `json:"mal_id"`
	TitleNative      string   `json:"title_native"`
	Synonyms         []string `json:"synonyms"`
	Season           string   `json:"season"`
	SeasonYear       *int     `json:"season_year"`
	DurationMin      *int     `json:"duration_min"`
	SearchQuery      string   `json:"search_query"`
	EpisodeOffset    int      `json:"episode_offset"`
	GroupOrder       []string `json:"group_order"`
	Resolution       string   `json:"resolution"`
	AutoDownload     *bool    `json:"auto_download"`
	Folder           string   `json:"folder"`
	AutoFrom         *int     `json:"auto_from"`  // episodes below it are never auto-downloaded; null = no floor
	AbsOffset        *int     `json:"abs_offset"` // episodes before this show in its series (AniList prequel chain); null = unknown
	SavePath         string   `json:"save_path"`  // where downloads go with current settings
	NyaaQuery        string   `json:"nyaa_query"`
	IntroSkipSec     *int     `json:"intro_skip_sec"`
	SubtitleDelaySec *float64 `json:"subtitle_delay_sec"`
	SubtitleScale    *float64 `json:"subtitle_scale"`
	AddedAt          int64    `json:"added_at"`
	UpdatedAt        int64    `json:"updated_at"`
}

// EpisodeRow is one episode with its best file and current release (SPEC 8).
type EpisodeRow struct {
	ShowID        int64       `json:"show_id"`
	ShowTitle     string      `json:"show_title"`
	CoverURL      string      `json:"cover_url"`
	BannerURL     string      `json:"banner_url"`
	Color         string      `json:"color"`
	Number        int         `json:"number"`
	AbsNumber     *int        `json:"abs_number"` // number + effective offset when that offset > 0, else null (SPEC v1.3 section 1.4)
	Title         string      `json:"title"`
	AiredAt       *int64      `json:"aired_at"`
	DubAiredAt    *int64      `json:"dub_aired_at"`
	WatchedAt     *int64      `json:"watched_at"`
	WatchedSource string      `json:"watched_source"`
	File          *FileRef    `json:"file"`
	Release       *ReleaseRef `json:"release"`
	Auto          *int        `json:"auto"`        // null = follow the rule, 0 = never, 1 = always auto-download
	AutoReason    string      `json:"auto_reason"` // why the conveyor leaves it alone; "" = it does not (SPEC v1.1 section 9)
	WaitReason    string      `json:"wait_reason"` // the conveyor's last "waiting ..." reason for this episode (low disk, group not out yet); "" = none
}

type FileRef struct {
	ID           int64  `json:"id"`
	Path         string `json:"path"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	ReleaseGroup string `json:"release_group"`
	Resolution   string `json:"resolution"`
	Version      int    `json:"version"`
}

type ReleaseRef struct {
	ID      int64  `json:"id"`
	Status  string `json:"status"`
	Title   string `json:"title"`
	Seeders *int   `json:"seeders"`
}

// FileOut is a files row plus its base name (ShowDetail.files).
type FileOut struct {
	store.File
	Name string `json:"name"`
}

// CalendarItem is one calendar cell (SPEC 8).
type CalendarItem struct {
	ShowID        int64  `json:"show_id"`
	Title         string `json:"title"`
	Episode       int    `json:"episode"`
	AbsNumber     *int   `json:"abs_number"` // like EpisodeRow.abs_number
	AiredAt       *int64 `json:"aired_at"`
	DubAiredAt    *int64 `json:"dub_aired_at"`
	OnDisk        bool   `json:"on_disk"`
	Watched       bool   `json:"watched"`
	ReleaseStatus string `json:"release_status"`
}

// cardCtx holds the aggregates needed to build many cards with few queries.
type cardCtx struct {
	c     settings.Settings
	now   int64
	stats map[int64]store.ShowStat
	next  map[int64]store.Episode
}

func (s *Server) newCardCtx(ctx context.Context) (*cardCtx, error) {
	now := s.now().Unix()
	stats, err := s.st.ShowStats(ctx, now)
	if err != nil {
		return nil, err
	}
	next, err := s.st.NextAiring(ctx, now)
	if err != nil {
		return nil, err
	}
	return &cardCtx{c: s.set.Get(), now: now, stats: stats, next: next}, nil
}

func (cc *cardCtx) card(sh store.Show) ShowCard {
	st := cc.stats[sh.ID]
	aired := st.Aired
	// TODO(spec): finished shows often have no airing schedule; count all their episodes as aired.
	if sh.Status == "FINISHED" && sh.EpisodesTotal != nil && *sh.EpisodesTotal > aired {
		aired = *sh.EpisodesTotal
	}
	card := ShowCard{
		ID: sh.ID, AniListID: sh.AniListID, Title: sched.DisplayTitle(cc.c, sh), TitleRomaji: sh.TitleRomaji, TitleEnglish: sh.TitleEnglish,
		CoverURL: sh.CoverURL, BannerURL: sh.BannerURL, Color: sh.Color, Status: sh.Status, Format: sh.Format,
		EpisodesTotal: sh.EpisodesTotal, ListStatus: sh.ListStatus, Score: sh.Score,
		Progress:     Progress{Watched: st.Watched, OnDisk: st.OnDisk, Aired: aired},
		LastActivity: max(st.LastActivity, sh.AddedAt),
	}
	if e, ok := cc.next[sh.ID]; ok && e.AiredAt != nil {
		card.Next = &NextEp{Episode: e.Number, AiredAt: *e.AiredAt}
	} else if sh.NextAiringEp != nil && sh.NextAiringAt != nil && *sh.NextAiringAt > cc.now {
		card.Next = &NextEp{Episode: *sh.NextAiringEp, AiredAt: *sh.NextAiringAt}
	}
	return card
}

func (cc *cardCtx) full(sh store.Show) ShowFull {
	syn := sh.Synonyms
	if syn == nil {
		syn = []string{}
	}
	return ShowFull{
		ShowCard: cc.card(sh), MALID: sh.MALID, TitleNative: sh.TitleNative, Synonyms: syn, Season: sh.Season,
		SeasonYear: sh.SeasonYear, DurationMin: sh.DurationMin, SearchQuery: sh.SearchQuery, EpisodeOffset: sh.EpisodeOffset,
		GroupOrder: sh.GroupOrder, Resolution: sh.Resolution, AutoDownload: sh.AutoDownload, Folder: sh.Folder, AutoFrom: sh.AutoFrom,
		AbsOffset: apiAbsOffset(sh.AbsOffset), SavePath: sched.SavePath(cc.c, sh), NyaaQuery: sched.NyaaQuery(sh), AddedAt: sh.AddedAt, UpdatedAt: sh.UpdatedAt,
		IntroSkipSec: sh.IntroSkipSec, SubtitleDelaySec: sh.SubtitleDelaySec, SubtitleScale: sh.SubtitleScale,
	}
}

// bestFiles maps episode -> best present file (highest version, then newest mtime).
func bestFiles(files []store.File) map[int]store.File {
	out := map[int]store.File{}
	for _, f := range files {
		if !f.Present || f.Episode == nil {
			continue
		}
		b, ok := out[*f.Episode]
		if !ok || f.Version > b.Version || (f.Version == b.Version && f.Mtime > b.Mtime) {
			out[*f.Episode] = f
		}
	}
	return out
}

// releaseRank orders statuses for "the current release" of an episode:
// in-flight first, then done, then failed. Candidates/skipped/replaced are not shown.
var releaseRank = map[string]int{
	store.RelDownloading: 1, store.RelAdded: 2, store.RelChosen: 3, store.RelDone: 4, store.RelFailed: 5,
}

// failedTTL is how long a failed release stays the current release of its
// episode (SPEC v1.3 section 2.2).
const failedTTL = 7 * 24 * 60 * 60

// currentRelease is the release shown for episode ep (rels = all releases of
// the show, now = unix seconds). A failed release is left out when a newer
// release (greater id, any status except replaced) covers the same episode,
// or when its status was set more than 7 days ago (status_at, else first_seen).
func currentRelease(rels []store.Release, ep int, now int64) *store.Release {
	var best *store.Release
	for i := range rels {
		r := &rels[i]
		rk, ok := releaseRank[r.Status]
		if !ok || !r.Covers(ep) {
			continue
		}
		if r.Status == store.RelFailed && failedHidden(rels, r, ep, now) {
			continue
		}
		if best == nil || rk < releaseRank[best.Status] || (rk == releaseRank[best.Status] && r.ID > best.ID) {
			best = r
		}
	}
	return best
}

func failedHidden(rels []store.Release, f *store.Release, ep int, now int64) bool {
	at := f.StatusAt
	if at == 0 {
		at = f.FirstSeen
	}
	if now-at > failedTTL {
		return true
	}
	for i := range rels {
		o := &rels[i]
		if o.ID > f.ID && o.Status != store.RelReplaced && o.Covers(ep) {
			return true
		}
	}
	return false
}

// apiAbsOffset is abs_offset as the API shows it: null when not computed or
// when the chain is unknown (stored -1).
func apiAbsOffset(v *int) *int {
	if v == nil || *v <= -1 {
		return nil
	}
	return v
}

// absNumber is number + the show's effective offset when that offset is
// positive, else nil.
func absNumber(sh store.Show, number int) *int {
	if o := store.EffectiveOffset(sh); o > 0 {
		n := number + o
		return &n
	}
	return nil
}

func fileRef(f store.File) *FileRef {
	return &FileRef{ID: f.ID, Path: f.Path, Name: filepath.Base(f.Path), Size: f.Size, ReleaseGroup: f.ReleaseGroup, Resolution: f.Resolution, Version: f.Version}
}

func episodeRow(c settings.Settings, ac sched.AutoContext, sh store.Show, e store.Episode, files map[int]store.File, rels []store.Release, now int64) EpisodeRow {
	row := EpisodeRow{
		ShowID: sh.ID, ShowTitle: sched.DisplayTitle(c, sh), CoverURL: sh.CoverURL, BannerURL: sh.BannerURL, Color: sh.Color,
		Number: e.Number, AbsNumber: absNumber(sh, e.Number), Title: e.Title, AiredAt: e.AiredAt, DubAiredAt: e.DubAiredAt,
		WatchedAt: e.WatchedAt, WatchedSource: e.WatchedSource, Auto: e.Auto,
	}
	f, hasFile := files[e.Number]
	if hasFile {
		row.File = fileRef(f)
	}
	row.AutoReason = ac.Reason(e, hasFile, rels)
	if r := currentRelease(rels, e.Number, now); r != nil {
		row.Release = &ReleaseRef{ID: r.ID, Status: r.Status, Title: r.Title, Seeders: r.Seeders}
	}
	return row
}

// showRows builds EpisodeRows for one show; episodes with a file but no row are included.
func (s *Server) showRows(ctx context.Context, c settings.Settings, sh store.Show) ([]EpisodeRow, []store.File, []store.Release, error) {
	eps, err := s.st.ListEpisodes(ctx, sh.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	files, err := s.st.FilesForShow(ctx, sh.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	rels, err := s.st.ListReleases(ctx, sh.ID, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	best := bestFiles(files)
	ac := sched.NewAutoContext(c, sh, eps, s.now())
	have := map[int]bool{}
	for _, e := range eps {
		have[e.Number] = true
	}
	for n := range best {
		if !have[n] {
			eps = append(eps, store.Episode{ShowID: sh.ID, Number: n})
		}
	}
	sort.Slice(eps, func(i, j int) bool { return eps[i].Number < eps[j].Number })
	rows := make([]EpisodeRow, 0, len(eps))
	now := s.now().Unix()
	for _, e := range eps {
		row := episodeRow(c, ac, sh, e, best, rels, now)
		if s.sch != nil {
			row.WaitReason = s.sch.WaitReason(sh.ID, e.Number)
		}
		rows = append(rows, row)
	}
	return rows, files, rels, nil
}

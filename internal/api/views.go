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
	MALID         int64    `json:"mal_id"`
	TitleNative   string   `json:"title_native"`
	Synonyms      []string `json:"synonyms"`
	Season        string   `json:"season"`
	SeasonYear    *int     `json:"season_year"`
	DurationMin   *int     `json:"duration_min"`
	SearchQuery   string   `json:"search_query"`
	EpisodeOffset int      `json:"episode_offset"`
	GroupOrder    []string `json:"group_order"`
	Resolution    string   `json:"resolution"`
	AutoDownload  *bool    `json:"auto_download"`
	Folder        string   `json:"folder"`
	AutoFrom      *int     `json:"auto_from"` // episodes below it are never auto-downloaded; null = no floor
	SavePath      string   `json:"save_path"` // where downloads go with current settings
	NyaaQuery     string   `json:"nyaa_query"`
	AddedAt       int64    `json:"added_at"`
	UpdatedAt     int64    `json:"updated_at"`
}

// EpisodeRow is one episode with its best file and current release (SPEC 8).
type EpisodeRow struct {
	ShowID        int64       `json:"show_id"`
	ShowTitle     string      `json:"show_title"`
	CoverURL      string      `json:"cover_url"`
	BannerURL     string      `json:"banner_url"`
	Color         string      `json:"color"`
	Number        int         `json:"number"`
	Title         string      `json:"title"`
	AiredAt       *int64      `json:"aired_at"`
	DubAiredAt    *int64      `json:"dub_aired_at"`
	WatchedAt     *int64      `json:"watched_at"`
	WatchedSource string      `json:"watched_source"`
	File          *FileRef    `json:"file"`
	Release       *ReleaseRef `json:"release"`
	Auto          *int        `json:"auto"`        // null = follow the rule, 0 = never, 1 = always auto-download
	AutoReason    string      `json:"auto_reason"` // why the conveyor leaves it alone; "" = it does not (SPEC v1.1 section 9)
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
		SavePath: sched.SavePath(cc.c, sh), NyaaQuery: sched.NyaaQuery(sh), AddedAt: sh.AddedAt, UpdatedAt: sh.UpdatedAt,
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

func currentRelease(rels []store.Release, ep int) *store.Release {
	var best *store.Release
	for i := range rels {
		r := &rels[i]
		rk, ok := releaseRank[r.Status]
		if !ok || !r.Covers(ep) {
			continue
		}
		if best == nil || rk < releaseRank[best.Status] || (rk == releaseRank[best.Status] && r.ID > best.ID) {
			best = r
		}
	}
	return best
}

func fileRef(f store.File) *FileRef {
	return &FileRef{ID: f.ID, Path: f.Path, Name: filepath.Base(f.Path), Size: f.Size, ReleaseGroup: f.ReleaseGroup, Resolution: f.Resolution, Version: f.Version}
}

func episodeRow(c settings.Settings, ac sched.AutoContext, sh store.Show, e store.Episode, files map[int]store.File, rels []store.Release) EpisodeRow {
	row := EpisodeRow{
		ShowID: sh.ID, ShowTitle: sched.DisplayTitle(c, sh), CoverURL: sh.CoverURL, BannerURL: sh.BannerURL, Color: sh.Color,
		Number: e.Number, Title: e.Title, AiredAt: e.AiredAt, DubAiredAt: e.DubAiredAt, WatchedAt: e.WatchedAt, WatchedSource: e.WatchedSource,
		Auto: e.Auto,
	}
	f, hasFile := files[e.Number]
	if hasFile {
		row.File = fileRef(f)
	}
	row.AutoReason = ac.Reason(e, hasFile, rels)
	if r := currentRelease(rels, e.Number); r != nil {
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
	for _, e := range eps {
		rows = append(rows, episodeRow(c, ac, sh, e, best, rels))
	}
	return rows, files, rels, nil
}

package store

import (
	"context"
	"database/sql"
	"errors"
)

// Show is one row of the shows table. Text columns map NULL <-> "".
// AniListID/MALID 0 means NULL. GroupOrder nil means NULL (use global setting).
type Show struct {
	ID            int64    `json:"id"`
	AniListID     int64    `json:"anilist_id"`
	MALID         int64    `json:"mal_id"`
	TitleRomaji   string   `json:"title_romaji"`
	TitleEnglish  string   `json:"title_english"`
	TitleNative   string   `json:"title_native"`
	Synonyms      []string `json:"synonyms"`
	CoverURL      string   `json:"cover_url"`
	BannerURL     string   `json:"banner_url"`
	Color         string   `json:"color"`
	Format        string   `json:"format"`
	Status        string   `json:"status"`
	Season        string   `json:"season"`
	SeasonYear    *int     `json:"season_year"`
	EpisodesTotal *int     `json:"episodes_total"`
	DurationMin   *int     `json:"duration_min"`
	NextAiringEp  *int     `json:"next_airing_ep"`
	NextAiringAt  *int64   `json:"next_airing_at"`
	ListStatus    string   `json:"list_status"`
	Score         *float64 `json:"score"`
	SearchQuery   string   `json:"search_query"`
	EpisodeOffset int      `json:"episode_offset"`
	GroupOrder    []string `json:"group_order"`
	Resolution    string   `json:"resolution"`
	AutoDownload  *bool    `json:"auto_download"`
	Folder        string   `json:"folder"`
	AutoFrom      *int     `json:"auto_from"` // episodes below it are never auto-downloaded; nil = no floor (migrate_2)
	// AbsOffset is the number of episodes before this show in its series, from
	// AniList's prequel chain (migrate_3): nil = not computed yet, -1 = computed
	// but the chain is unknown, 0 = no prequel. EpisodeOffset stays the manual override.
	AbsOffset *int  `json:"abs_offset"`
	AddedAt   int64 `json:"added_at"`
	UpdatedAt int64 `json:"updated_at"`
}

// EffectiveOffset is the offset between release numbering and AniList
// numbering used everywhere (SPEC v1.3 section 1.1): the manual EpisodeOffset
// when set, else the AniList-derived AbsOffset when it is above 0, else 0
// (an AbsOffset <= 0, including -1 = chain unknown, is no offset).
func EffectiveOffset(s Show) int {
	if s.EpisodeOffset != 0 {
		return s.EpisodeOffset
	}
	if s.AbsOffset != nil && *s.AbsOffset > 0 {
		return *s.AbsOffset
	}
	return 0
}

// SetAbsOffset writes only abs_offset (the periodic backfill of shows the
// airing refresh skips).
func (d *DB) SetAbsOffset(ctx context.Context, id int64, abs *int) error {
	return d.exec1(ctx, `UPDATE shows SET abs_offset=? WHERE id=?`, nint(abs), id)
}

// ListStatuses are the valid values of shows.list_status.
var ListStatuses = []string{"watching", "planning", "completed", "paused", "dropped"}

const showCols = `id, anilist_id, mal_id, title_romaji, title_english, title_native, synonyms,
	cover_url, banner_url, color, format, status, season, season_year, episodes_total, duration_min,
	next_airing_ep, next_airing_at, list_status, score, search_query, episode_offset, group_order,
	resolution, auto_download, folder, auto_from, abs_offset, added_at, updated_at`

func scanShow(r scanner) (Show, error) {
	var s Show
	var anilist, mal, seasonYear, epTotal, dur, nextEp, nextAt, auto, autoFrom, absOffset sql.NullInt64
	var eng, native, syn, cover, banner, color, format, status, season, query, groups, res, folder sql.NullString
	var score sql.NullFloat64
	err := r.Scan(&s.ID, &anilist, &mal, &s.TitleRomaji, &eng, &native, &syn,
		&cover, &banner, &color, &format, &status, &season, &seasonYear, &epTotal, &dur,
		&nextEp, &nextAt, &s.ListStatus, &score, &query, &s.EpisodeOffset, &groups,
		&res, &auto, &folder, &autoFrom, &absOffset, &s.AddedAt, &s.UpdatedAt)
	if err != nil {
		return s, err
	}
	s.AniListID = anilist.Int64
	s.MALID = mal.Int64
	s.TitleEnglish, s.TitleNative = eng.String, native.String
	s.Synonyms = parseList(syn)
	if s.Synonyms == nil {
		s.Synonyms = []string{}
	}
	s.CoverURL, s.BannerURL, s.Color = cover.String, banner.String, color.String
	s.Format, s.Status, s.Season = format.String, status.String, season.String
	s.SeasonYear, s.EpisodesTotal, s.DurationMin = ptrInt(seasonYear), ptrInt(epTotal), ptrInt(dur)
	s.NextAiringEp, s.NextAiringAt = ptrInt(nextEp), ptrInt64(nextAt)
	s.Score = ptrFloat(score)
	s.SearchQuery = query.String
	s.GroupOrder = parseList(groups)
	s.Resolution = res.String
	s.AutoDownload = ptrBool(auto)
	s.Folder = folder.String
	s.AutoFrom = ptrInt(autoFrom)
	s.AbsOffset = ptrInt(absOffset)
	return s, nil
}

// CreateShow inserts s and sets s.ID (and AddedAt/UpdatedAt when zero).
func (d *DB) CreateShow(ctx context.Context, s *Show) error {
	now := Now()
	if s.AddedAt == 0 {
		s.AddedAt = now
	}
	if s.UpdatedAt == 0 {
		s.UpdatedAt = now
	}
	if s.ListStatus == "" {
		s.ListStatus = "watching"
	}
	if s.Synonyms == nil {
		s.Synonyms = []string{}
	}
	res, err := d.db.ExecContext(ctx, `INSERT INTO shows(`+showCols[4:]+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		nid(s.AniListID), nid(s.MALID), s.TitleRomaji, nstr(s.TitleEnglish), nstr(s.TitleNative), jsonList(s.Synonyms),
		nstr(s.CoverURL), nstr(s.BannerURL), nstr(s.Color), nstr(s.Format), nstr(s.Status), nstr(s.Season),
		nint(s.SeasonYear), nint(s.EpisodesTotal), nint(s.DurationMin), nint(s.NextAiringEp), nint64(s.NextAiringAt),
		s.ListStatus, nfloat(s.Score), nstr(s.SearchQuery), s.EpisodeOffset, njsonList(s.GroupOrder),
		nstr(s.Resolution), nbool(s.AutoDownload), nstr(s.Folder), nint(s.AutoFrom), nint(s.AbsOffset), s.AddedAt, s.UpdatedAt)
	if err != nil {
		return err
	}
	s.ID, err = res.LastInsertId()
	return err
}

// GetShow returns the show with id, or ErrNotFound.
func (d *DB) GetShow(ctx context.Context, id int64) (Show, error) {
	s, err := scanShow(d.db.QueryRowContext(ctx, `SELECT `+showCols+` FROM shows WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

// GetShowByAniList returns the show with the AniList id, or ErrNotFound.
func (d *DB) GetShowByAniList(ctx context.Context, anilistID int64) (Show, error) {
	s, err := scanShow(d.db.QueryRowContext(ctx, `SELECT `+showCols+` FROM shows WHERE anilist_id=?`, anilistID))
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

// ListShows returns all shows, or only those with listStatus when non-empty, ordered by title.
func (d *DB) ListShows(ctx context.Context, listStatus string) ([]Show, error) {
	q := `SELECT ` + showCols + ` FROM shows`
	var args []any
	if listStatus != "" {
		q += ` WHERE list_status=?`
		args = append(args, listStatus)
	}
	q += ` ORDER BY title_romaji COLLATE NOCASE`
	rows, err := d.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Show{}
	for rows.Next() {
		s, err := scanShow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UpdateShowMeta writes the AniList-derived columns of s (not the user's
// overrides), abs_offset included.
func (d *DB) UpdateShowMeta(ctx context.Context, s *Show) error {
	s.UpdatedAt = Now()
	if s.Synonyms == nil {
		s.Synonyms = []string{}
	}
	return d.exec1(ctx, `UPDATE shows SET anilist_id=?, mal_id=?, title_romaji=?, title_english=?, title_native=?, synonyms=?,
		cover_url=?, banner_url=?, color=?, format=?, status=?, season=?, season_year=?, episodes_total=?, duration_min=?,
		next_airing_ep=?, next_airing_at=?, abs_offset=?, updated_at=? WHERE id=?`,
		nid(s.AniListID), nid(s.MALID), s.TitleRomaji, nstr(s.TitleEnglish), nstr(s.TitleNative), jsonList(s.Synonyms),
		nstr(s.CoverURL), nstr(s.BannerURL), nstr(s.Color), nstr(s.Format), nstr(s.Status), nstr(s.Season),
		nint(s.SeasonYear), nint(s.EpisodesTotal), nint(s.DurationMin), nint(s.NextAiringEp), nint64(s.NextAiringAt),
		nint(s.AbsOffset), s.UpdatedAt, s.ID)
}

// UpdateShowPrefs writes the user-editable columns of s (list status, score, overrides).
func (d *DB) UpdateShowPrefs(ctx context.Context, s *Show) error {
	s.UpdatedAt = Now()
	return d.exec1(ctx, `UPDATE shows SET list_status=?, score=?, search_query=?, episode_offset=?, group_order=?,
		resolution=?, auto_download=?, folder=?, auto_from=?, updated_at=? WHERE id=?`,
		s.ListStatus, nfloat(s.Score), nstr(s.SearchQuery), s.EpisodeOffset, njsonList(s.GroupOrder),
		nstr(s.Resolution), nbool(s.AutoDownload), nstr(s.Folder), nint(s.AutoFrom), s.UpdatedAt, s.ID)
}

// DeleteShow removes a show; episodes and releases cascade, files keep their row with show_id NULL.
func (d *DB) DeleteShow(ctx context.Context, id int64) error {
	return d.exec1(ctx, `DELETE FROM shows WHERE id=?`, id)
}

// exec1 runs a statement that must affect at least one row, else ErrNotFound.
func (d *DB) exec1(ctx context.Context, q string, args ...any) error {
	res, err := d.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

package store

import (
	"context"
	"database/sql"
	"errors"
)

// Episode is one row of the episodes table.
type Episode struct {
	ShowID        int64  `json:"show_id"`
	Number        int    `json:"number"`
	AiredAt       *int64 `json:"aired_at"`
	DubAiredAt    *int64 `json:"dub_aired_at"`
	Title         string `json:"title"`
	WatchedAt     *int64 `json:"watched_at"`
	WatchedSource string `json:"watched_source"`
	Auto          *int   `json:"auto"` // nil = follow the rule, 0 = never auto-download, 1 = always (migrate_2)
}

const episodeCols = `show_id, number, aired_at, dub_aired_at, title, watched_at, watched_source, auto`

func scanEpisode(r scanner) (Episode, error) {
	var e Episode
	var aired, dub, watched, auto sql.NullInt64
	var title, source sql.NullString
	if err := r.Scan(&e.ShowID, &e.Number, &aired, &dub, &title, &watched, &source, &auto); err != nil {
		return e, err
	}
	e.AiredAt, e.DubAiredAt, e.WatchedAt = ptrInt64(aired), ptrInt64(dub), ptrInt64(watched)
	e.Auto = ptrInt(auto)
	e.Title, e.WatchedSource = title.String, source.String
	return e, nil
}

func (d *DB) queryEpisodes(ctx context.Context, q string, args ...any) ([]Episode, error) {
	rows, err := d.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Episode{}
	for rows.Next() {
		e, err := scanEpisode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EnsureEpisodes creates rows 1..upTo for a show when missing. Returns how many were added.
func (d *DB) EnsureEpisodes(ctx context.Context, showID int64, upTo int) (int, error) {
	if upTo <= 0 {
		return 0, nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	added := 0
	for n := 1; n <= upTo; n++ {
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO episodes(show_id, number) VALUES(?,?)`, showID, n)
		if err != nil {
			return 0, err
		}
		k, _ := res.RowsAffected()
		added += int(k)
	}
	return added, tx.Commit()
}

// EnsureEpisode creates the row for (showID, number) when missing.
func (d *DB) EnsureEpisode(ctx context.Context, showID int64, number int) error {
	_, err := d.db.ExecContext(ctx, `INSERT OR IGNORE INTO episodes(show_id, number) VALUES(?,?)`, showID, number)
	return err
}

// UpsertEpisodeAiring sets aired_at for (showID, number), creating the row if needed.
// changed is true when a row was created or aired_at changed.
func (d *DB) UpsertEpisodeAiring(ctx context.Context, showID int64, number int, airedAt *int64) (bool, error) {
	res, err := d.db.ExecContext(ctx, `INSERT INTO episodes(show_id, number, aired_at) VALUES(?,?,?)
		ON CONFLICT(show_id, number) DO UPDATE SET aired_at=excluded.aired_at
		WHERE episodes.aired_at IS NOT excluded.aired_at`, showID, number, nint64(airedAt))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SetDubAired sets dub_aired_at for (showID, number), creating the row if needed.
func (d *DB) SetDubAired(ctx context.Context, showID int64, number int, at *int64) (bool, error) {
	res, err := d.db.ExecContext(ctx, `INSERT INTO episodes(show_id, number, dub_aired_at) VALUES(?,?,?)
		ON CONFLICT(show_id, number) DO UPDATE SET dub_aired_at=excluded.dub_aired_at
		WHERE episodes.dub_aired_at IS NOT excluded.dub_aired_at`, showID, number, nint64(at))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SetEpisodeTitle sets the episode title (creating the row if needed).
func (d *DB) SetEpisodeTitle(ctx context.Context, showID int64, number int, title string) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO episodes(show_id, number, title) VALUES(?,?,?)
		ON CONFLICT(show_id, number) DO UPDATE SET title=excluded.title`, showID, number, nstr(title))
	return err
}

// setWatchedSQL marks one episode watched (creating the row) unless it already is.
// Args: show_id, number, watched_at, watched_source.
const setWatchedSQL = `INSERT INTO episodes(show_id, number, watched_at, watched_source) VALUES(?,?,?,?)
		ON CONFLICT(show_id, number) DO UPDATE SET watched_at=excluded.watched_at, watched_source=excluded.watched_source
		WHERE episodes.watched_at IS NULL`

// SetWatched marks (showID, number) watched at `at` from source (player, manual, import),
// creating the row if needed. changed is false when it was already watched.
func (d *DB) SetWatched(ctx context.Context, showID int64, number int, at int64, source string) (bool, error) {
	res, err := d.db.ExecContext(ctx, setWatchedSQL, showID, number, at, source)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SetWatchedThrough marks episodes 1..through of a show watched at `at` from
// source, in one transaction, with the same per-episode write as SetWatched:
// rows are created when missing and episodes already watched keep their
// watched_at and source (never unmarked). newly counts the episodes it marked.
func (d *DB) SetWatchedThrough(ctx context.Context, showID int64, through int, at int64, source string) (newly int, err error) {
	if through <= 0 {
		return 0, nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, setWatchedSQL)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	for n := 1; n <= through; n++ {
		res, err := stmt.ExecContext(ctx, showID, n, at, source)
		if err != nil {
			return 0, err
		}
		k, _ := res.RowsAffected()
		newly += int(k)
	}
	return newly, tx.Commit()
}

// MaxEpisodeNumber returns the highest episode number a show has as an episode
// row or a present file (0 when none).
func (d *DB) MaxEpisodeNumber(ctx context.Context, showID int64) (int, error) {
	var n int
	err := d.db.QueryRowContext(ctx, `SELECT MAX(
		COALESCE((SELECT MAX(number) FROM episodes WHERE show_id=?), 0),
		COALESCE((SELECT MAX(episode) FROM files WHERE show_id=? AND present=1), 0))`, showID, showID).Scan(&n)
	return n, err
}

// ClearWatched marks (showID, number) unwatched. changed is false when it was not watched.
func (d *DB) ClearWatched(ctx context.Context, showID int64, number int) (bool, error) {
	res, err := d.db.ExecContext(ctx, `UPDATE episodes SET watched_at=NULL, watched_source=NULL
		WHERE show_id=? AND number=? AND watched_at IS NOT NULL`, showID, number)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SetEpisodeAuto sets episodes.auto for (showID, number) (nil = follow the rule,
// 0 = never, 1 = always), creating the row if needed.
func (d *DB) SetEpisodeAuto(ctx context.Context, showID int64, number int, auto *int) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO episodes(show_id, number, auto) VALUES(?,?,?)
		ON CONFLICT(show_id, number) DO UPDATE SET auto=excluded.auto`, showID, number, nint(auto))
	return err
}

// SetEpisodesAutoRange sets auto for the existing episode rows from..to of a show
// (to < 0 = no upper end). Used for a removed batch torrent.
func (d *DB) SetEpisodesAutoRange(ctx context.Context, showID int64, from, to int, auto *int) error {
	if to < 0 {
		to = 1 << 30
	}
	_, err := d.db.ExecContext(ctx, `UPDATE episodes SET auto=? WHERE show_id=? AND number BETWEEN ? AND ?`, nint(auto), showID, from, to)
	return err
}

// GetEpisode returns one episode or ErrNotFound.
func (d *DB) GetEpisode(ctx context.Context, showID int64, number int) (Episode, error) {
	e, err := scanEpisode(d.db.QueryRowContext(ctx, `SELECT `+episodeCols+` FROM episodes WHERE show_id=? AND number=?`, showID, number))
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// ListEpisodes returns a show's episodes ordered by number.
func (d *DB) ListEpisodes(ctx context.Context, showID int64) ([]Episode, error) {
	return d.queryEpisodes(ctx, `SELECT `+episodeCols+` FROM episodes WHERE show_id=? ORDER BY number`, showID)
}

// EpisodesAiringBetween returns episodes whose aired_at or dub_aired_at is in [from, to).
func (d *DB) EpisodesAiringBetween(ctx context.Context, from, to int64) ([]Episode, error) {
	return d.queryEpisodes(ctx, `SELECT `+episodeCols+` FROM episodes
		WHERE (aired_at >= ? AND aired_at < ?) OR (dub_aired_at >= ? AND dub_aired_at < ?)
		ORDER BY COALESCE(aired_at, dub_aired_at), show_id, number`, from, to, from, to)
}

// MaxWatched returns the highest watched episode number of a show (0 when none).
func (d *DB) MaxWatched(ctx context.Context, showID int64) (int, error) {
	var n sql.NullInt64
	err := d.db.QueryRowContext(ctx, `SELECT MAX(number) FROM episodes WHERE show_id=? AND watched_at IS NOT NULL`, showID).Scan(&n)
	return int(n.Int64), err
}

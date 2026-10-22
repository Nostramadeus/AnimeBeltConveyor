package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// File is one row of the files table. ShowID/Episode nil = not matched.
type File struct {
	ID           int64           `json:"id"`
	Path         string          `json:"path"`
	Size         int64           `json:"size"`
	Mtime        int64           `json:"mtime"`
	ShowID       *int64          `json:"show_id"`
	Episode      *int            `json:"episode"`
	ReleaseGroup string          `json:"release_group"`
	Resolution   string          `json:"resolution"`
	Version      int             `json:"version"`
	Parsed       json.RawMessage `json:"parsed"`
	Present      bool            `json:"present"`
	FirstSeen    int64           `json:"first_seen"`
	LastSeen     int64           `json:"last_seen"`
}

const fileCols = `id, path, size, mtime, show_id, episode, release_group, resolution, version, parsed, present, first_seen, last_seen`

func scanFile(r scanner) (File, error) {
	var f File
	var show, ep sql.NullInt64
	var group, res sql.NullString
	var parsed string
	var present int
	if err := r.Scan(&f.ID, &f.Path, &f.Size, &f.Mtime, &show, &ep, &group, &res, &f.Version, &parsed, &present, &f.FirstSeen, &f.LastSeen); err != nil {
		return f, err
	}
	f.ShowID, f.Episode = ptrInt64(show), ptrInt(ep)
	f.ReleaseGroup, f.Resolution = group.String, res.String
	if parsed == "" {
		parsed = "{}"
	}
	f.Parsed = json.RawMessage(parsed)
	f.Present = present != 0
	return f, nil
}

func (d *DB) queryFiles(ctx context.Context, q string, args ...any) ([]File, error) {
	rows, err := d.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []File{}
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// UpsertFiles inserts or updates files by path in one transaction, marking them
// present and setting last_seen (first_seen too for new rows). IDs are filled in.
func (d *DB) UpsertFiles(ctx context.Context, files []File) error {
	if len(files) == 0 {
		return nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO files(path, size, mtime, show_id, episode, release_group, resolution, version, parsed, present, first_seen, last_seen)
		VALUES(?,?,?,?,?,?,?,?,?,1,?,?)
		ON CONFLICT(path) DO UPDATE SET size=excluded.size, mtime=excluded.mtime, show_id=excluded.show_id,
			episode=excluded.episode, release_group=excluded.release_group, resolution=excluded.resolution,
			version=excluded.version, parsed=excluded.parsed, present=1, last_seen=excluded.last_seen
		RETURNING id, first_seen`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := Now()
	for i := range files {
		f := &files[i]
		if f.LastSeen == 0 {
			f.LastSeen = now
		}
		if f.FirstSeen == 0 {
			f.FirstSeen = f.LastSeen
		}
		if f.Version == 0 {
			f.Version = 1
		}
		parsed := string(f.Parsed)
		if parsed == "" {
			parsed = "{}"
		}
		if err := stmt.QueryRowContext(ctx, f.Path, f.Size, f.Mtime, nint64(f.ShowID), nint(f.Episode),
			nstr(f.ReleaseGroup), nstr(f.Resolution), f.Version, parsed, f.FirstSeen, f.LastSeen).Scan(&f.ID, &f.FirstSeen); err != nil {
			return err
		}
		f.Present = true
	}
	return tx.Commit()
}

// TouchFiles sets last_seen=ts (and present=1) for unchanged files found by a scan.
func (d *DB) TouchFiles(ctx context.Context, ids []int64, ts int64) error {
	return d.execEach(ctx, `UPDATE files SET last_seen=?, present=1 WHERE id=?`, ids, func(id int64) []any { return []any{ts, id} })
}

// MarkFilesMissing sets present=0 for the given ids.
func (d *DB) MarkFilesMissing(ctx context.Context, ids []int64) error {
	return d.execEach(ctx, `UPDATE files SET present=0 WHERE id=?`, ids, func(id int64) []any { return []any{id} })
}

// execEach runs one prepared statement per id inside a single transaction.
func (d *DB) execEach(ctx context.Context, q string, ids []int64, args func(int64) []any) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, id := range ids {
		if _, err := stmt.ExecContext(ctx, args(id)...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetFile returns one file or ErrNotFound.
func (d *DB) GetFile(ctx context.Context, id int64) (File, error) {
	f, err := scanFile(d.db.QueryRowContext(ctx, `SELECT `+fileCols+` FROM files WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	return f, err
}

// GetFileByPath returns one file or ErrNotFound.
func (d *DB) GetFileByPath(ctx context.Context, path string) (File, error) {
	f, err := scanFile(d.db.QueryRowContext(ctx, `SELECT `+fileCols+` FROM files WHERE path=?`, path))
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	return f, err
}

// AllFiles returns every file row (present or not).
func (d *DB) AllFiles(ctx context.Context) ([]File, error) {
	return d.queryFiles(ctx, `SELECT `+fileCols+` FROM files`)
}

// FilesForShow returns all file rows matched to a show, present first, by episode.
func (d *DB) FilesForShow(ctx context.Context, showID int64) ([]File, error) {
	return d.queryFiles(ctx, `SELECT `+fileCols+` FROM files WHERE show_id=? ORDER BY present DESC, episode, version DESC`, showID)
}

// PresentMatchedFiles returns present files that are matched to a show and episode.
func (d *DB) PresentMatchedFiles(ctx context.Context) ([]File, error) {
	return d.queryFiles(ctx, `SELECT `+fileCols+` FROM files WHERE present=1 AND show_id IS NOT NULL AND episode IS NOT NULL`)
}

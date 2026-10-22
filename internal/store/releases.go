package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Release statuses.
const (
	RelCandidate   = "candidate"
	RelChosen      = "chosen"
	RelAdded       = "added"
	RelDownloading = "downloading"
	RelDone        = "done"
	RelSkipped     = "skipped"
	RelReplaced    = "replaced"
	RelFailed      = "failed"
)

// Release is one row of the releases table. NyaaID 0 means NULL.
type Release struct {
	ID           int64  `json:"id"`
	ShowID       int64  `json:"show_id"`
	Episode      *int   `json:"episode"`
	EpisodeEnd   *int   `json:"episode_end"`
	NyaaID       int64  `json:"nyaa_id"`
	Title        string `json:"title"`
	InfoHash     string `json:"info_hash"`
	Magnet       string `json:"magnet"`
	TorrentURL   string `json:"torrent_url"`
	ReleaseGroup string `json:"release_group"`
	Resolution   string `json:"resolution"`
	Version      int    `json:"version"`
	IsBatch      bool   `json:"is_batch"`
	Seeders      *int   `json:"seeders"`
	Leechers     *int   `json:"leechers"`
	SizeBytes    *int64 `json:"size_bytes"`
	PublishedAt  int64  `json:"published_at"`
	Status       string `json:"status"`
	QbitHash     string `json:"qbit_hash"`
	StatusNote   string `json:"status_note"`
	FirstSeen    int64  `json:"first_seen"`
}

// Covers reports whether the release contains episode n (single episode or batch range).
// A batch with no range covers every episode.
func (r Release) Covers(n int) bool {
	if r.Episode != nil && *r.Episode == n && !r.IsBatch {
		return true
	}
	if !r.IsBatch {
		return false
	}
	if r.Episode == nil {
		return true
	}
	end := *r.Episode
	if r.EpisodeEnd != nil {
		end = *r.EpisodeEnd
	}
	return n >= *r.Episode && n <= end
}

const releaseCols = `id, show_id, episode, episode_end, nyaa_id, title, info_hash, magnet, torrent_url, release_group,
	resolution, version, is_batch, seeders, leechers, size_bytes, published_at, status, qbit_hash, status_note, first_seen`

func scanRelease(r scanner) (Release, error) {
	var x Release
	var ep, epEnd, nyaa, seed, leech, size sql.NullInt64
	var hash, magnet, turl, group, res, qhash, note sql.NullString
	var batch int
	err := r.Scan(&x.ID, &x.ShowID, &ep, &epEnd, &nyaa, &x.Title, &hash, &magnet, &turl, &group,
		&res, &x.Version, &batch, &seed, &leech, &size, &x.PublishedAt, &x.Status, &qhash, &note, &x.FirstSeen)
	if err != nil {
		return x, err
	}
	x.Episode, x.EpisodeEnd = ptrInt(ep), ptrInt(epEnd)
	x.NyaaID = nyaa.Int64
	x.InfoHash, x.Magnet, x.TorrentURL, x.ReleaseGroup = hash.String, magnet.String, turl.String, group.String
	x.Resolution = res.String
	x.IsBatch = batch != 0
	x.Seeders, x.Leechers, x.SizeBytes = ptrInt(seed), ptrInt(leech), ptrInt64(size)
	x.QbitHash, x.StatusNote = qhash.String, note.String
	return x, nil
}

func (d *DB) queryReleases(ctx context.Context, q string, args ...any) ([]Release, error) {
	rows, err := d.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Release{}
	for rows.Next() {
		x, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// UpsertRelease inserts r, or when a row with the same nyaa_id exists, refreshes its
// swarm numbers (seeders, leechers, size) and leaves status alone. r.ID is filled in;
// isNew reports whether a row was inserted.
func (d *DB) UpsertRelease(ctx context.Context, r *Release) (bool, error) {
	if r.Status == "" {
		r.Status = RelCandidate
	}
	if r.Version == 0 {
		r.Version = 1
	}
	if r.FirstSeen == 0 {
		r.FirstSeen = Now()
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if r.NyaaID != 0 {
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM releases WHERE nyaa_id=?`, r.NyaaID).Scan(&id)
		if err == nil {
			r.ID = id
			if _, err := tx.ExecContext(ctx, `UPDATE releases SET seeders=?, leechers=?, size_bytes=COALESCE(?, size_bytes) WHERE id=?`,
				nint(r.Seeders), nint(r.Leechers), nint64(r.SizeBytes), id); err != nil {
				return false, err
			}
			return false, tx.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO releases(show_id, episode, episode_end, nyaa_id, title, info_hash, magnet, torrent_url,
		release_group, resolution, version, is_batch, seeders, leechers, size_bytes, published_at, status, qbit_hash, status_note, first_seen)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ShowID, nint(r.Episode), nint(r.EpisodeEnd), nid(r.NyaaID), r.Title, nstr(strings.ToLower(r.InfoHash)), nstr(r.Magnet), nstr(r.TorrentURL),
		nstr(r.ReleaseGroup), nstr(r.Resolution), r.Version, b2i(r.IsBatch), nint(r.Seeders), nint(r.Leechers), nint64(r.SizeBytes),
		r.PublishedAt, r.Status, nstr(r.QbitHash), nstr(r.StatusNote), r.FirstSeen)
	if err != nil {
		return false, err
	}
	if r.ID, err = res.LastInsertId(); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// GetRelease returns one release or ErrNotFound.
func (d *DB) GetRelease(ctx context.Context, id int64) (Release, error) {
	x, err := scanRelease(d.db.QueryRowContext(ctx, `SELECT `+releaseCols+` FROM releases WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return x, ErrNotFound
	}
	return x, err
}

// ListReleases returns releases, filtered by show (0 = all) and episode (nil = all;
// batches covering the episode are included), newest first.
func (d *DB) ListReleases(ctx context.Context, showID int64, episode *int) ([]Release, error) {
	q := `SELECT ` + releaseCols + ` FROM releases WHERE 1=1`
	var args []any
	if showID != 0 {
		q += ` AND show_id=?`
		args = append(args, showID)
	}
	if episode != nil {
		q += ` AND (episode=? OR (is_batch=1 AND (episode IS NULL OR (episode<=? AND COALESCE(episode_end, episode)>=?))))`
		args = append(args, *episode, *episode, *episode)
	}
	q += ` ORDER BY published_at DESC, id DESC`
	return d.queryReleases(ctx, q, args...)
}

// ReleasesByStatus returns releases whose status is one of statuses.
func (d *DB) ReleasesByStatus(ctx context.Context, statuses ...string) ([]Release, error) {
	if len(statuses) == 0 {
		return []Release{}, nil
	}
	q := `SELECT ` + releaseCols + ` FROM releases WHERE status IN (?` + strings.Repeat(",?", len(statuses)-1) + `) ORDER BY id`
	args := make([]any, len(statuses))
	for i, s := range statuses {
		args[i] = s
	}
	return d.queryReleases(ctx, q, args...)
}

// SetReleaseStatus sets status and status_note.
func (d *DB) SetReleaseStatus(ctx context.Context, id int64, status, note string) error {
	return d.exec1(ctx, `UPDATE releases SET status=?, status_note=? WHERE id=?`, status, nstr(note), id)
}

// SetReleaseQbit sets status, qbit_hash and status_note in one write.
func (d *DB) SetReleaseQbit(ctx context.Context, id int64, status, hash, note string) error {
	return d.exec1(ctx, `UPDATE releases SET status=?, qbit_hash=?, status_note=? WHERE id=?`, status, nstr(hash), nstr(note), id)
}

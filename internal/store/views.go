package store

import "context"

// ShowStat holds per-show aggregates used by ShowCard.
type ShowStat struct {
	Watched      int   // episodes with watched_at set
	OnDisk       int   // distinct episode numbers with a present file
	Aired        int   // highest episode number with aired_at <= now (AniList schedules can skip early episodes)
	LastActivity int64 // max(added_at, newest file first_seen, newest watched_at)
}

// ShowStats returns aggregates for every show keyed by show id.
func (d *DB) ShowStats(ctx context.Context, now int64) (map[int64]ShowStat, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT s.id,
		(SELECT COUNT(*) FROM episodes e WHERE e.show_id=s.id AND e.watched_at IS NOT NULL),
		(SELECT COUNT(DISTINCT f.episode) FROM files f WHERE f.show_id=s.id AND f.present=1 AND f.episode IS NOT NULL),
		(SELECT COALESCE(MAX(e.number), 0) FROM episodes e WHERE e.show_id=s.id AND e.aired_at IS NOT NULL AND e.aired_at<=?),
		MAX(s.added_at,
			COALESCE((SELECT MAX(f.first_seen) FROM files f WHERE f.show_id=s.id), 0),
			COALESCE((SELECT MAX(e.watched_at) FROM episodes e WHERE e.show_id=s.id), 0))
		FROM shows s`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]ShowStat{}
	for rows.Next() {
		var id int64
		var st ShowStat
		if err := rows.Scan(&id, &st.Watched, &st.OnDisk, &st.Aired, &st.LastActivity); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}

// Counts are the dashboard numbers in GET /api/state.
type Counts struct {
	Watching        int `json:"watching"`
	OnDiskUnwatched int `json:"on_disk_unwatched"`
	AiringThisWeek  int `json:"airing_this_week"`
}

// StateCounts computes Counts for shows with list_status watching.
// "This week" = aired_at in [now, now+7 days).
func (d *DB) StateCounts(ctx context.Context, now int64) (Counts, error) {
	var c Counts
	err := d.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM shows WHERE list_status='watching'),
		(SELECT COUNT(*) FROM (SELECT DISTINCT f.show_id, f.episode FROM files f
			JOIN shows s ON s.id=f.show_id AND s.list_status='watching'
			LEFT JOIN episodes e ON e.show_id=f.show_id AND e.number=f.episode
			WHERE f.present=1 AND f.episode IS NOT NULL AND e.watched_at IS NULL)),
		(SELECT COUNT(*) FROM episodes e JOIN shows s ON s.id=e.show_id AND s.list_status='watching'
			WHERE e.aired_at>=? AND e.aired_at<?)`, now, now+7*86400).
		Scan(&c.Watching, &c.OnDiskUnwatched, &c.AiringThisWeek)
	return c, err
}

// NextAiring returns, per show, the first episode whose aired_at is after now.
func (d *DB) NextAiring(ctx context.Context, now int64) (map[int64]Episode, error) {
	eps, err := d.queryEpisodes(ctx, `SELECT `+episodeCols+` FROM episodes e
		WHERE e.aired_at > ? AND e.number = (SELECT MIN(x.number) FROM episodes x WHERE x.show_id = e.show_id AND x.aired_at > ?)`, now, now)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]Episode, len(eps))
	for _, e := range eps {
		out[e.ShowID] = e
	}
	return out, nil
}

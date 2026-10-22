-- v1.1 section 9: auto-download guard rails.
-- episodes.auto: NULL = follow the rule, 0 = never auto-download, 1 = always (even if old).
ALTER TABLE episodes ADD COLUMN auto INTEGER;
-- shows.auto_from: NULL = no floor; N = episodes below N are never auto-downloaded.
ALTER TABLE shows ADD COLUMN auto_from INTEGER;
-- Backfill: episodes whose torrent the user already removed in qBittorrent (v1.1 wrote this note)
-- must not come back on the first pass after the update.
UPDATE episodes SET auto = 0
WHERE auto IS NULL AND EXISTS (
  SELECT 1 FROM releases r
  WHERE r.show_id = episodes.show_id AND r.status = 'failed' AND r.status_note = 'removed from qBittorrent'
    AND episodes.number BETWEEN r.episode AND COALESCE(r.episode_end, r.episode)
);

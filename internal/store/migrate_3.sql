-- v1.3 section 1: absolute numbering. NULL = not computed yet; -1 = computed, chain unknown; 0 = no prequel; N = episodes before this show in the series.
ALTER TABLE shows ADD COLUMN abs_offset INTEGER;
-- v1.3 section 2: when a status was last set (unix seconds). NULL for old rows (treated as first_seen).
ALTER TABLE releases ADD COLUMN status_at INTEGER;
-- v1.3 section 2.1: a torrent the user removed in the client is "skipped", not "failed".
UPDATE releases SET status='skipped', status_note='removed in qBittorrent'
WHERE status='failed' AND status_note='removed from qBittorrent';

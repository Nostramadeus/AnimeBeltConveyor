CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);           -- schema_version
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);        -- value = JSON

CREATE TABLE IF NOT EXISTS shows (
  id INTEGER PRIMARY KEY,
  anilist_id INTEGER UNIQUE,
  mal_id INTEGER,
  title_romaji TEXT NOT NULL,
  title_english TEXT,
  title_native TEXT,
  synonyms TEXT NOT NULL DEFAULT '[]',          -- JSON array of strings
  cover_url TEXT, banner_url TEXT, color TEXT,  -- color = AniList coverImage.color hex or NULL
  format TEXT,                                  -- TV, TV_SHORT, MOVIE, OVA, ONA, SPECIAL
  status TEXT,                                  -- RELEASING, FINISHED, NOT_YET_RELEASED, CANCELLED, HIATUS
  season TEXT, season_year INTEGER,
  episodes_total INTEGER,                       -- NULL when unknown
  duration_min INTEGER,                         -- per-episode runtime, NULL when unknown
  next_airing_ep INTEGER, next_airing_at INTEGER, -- unix seconds UTC
  list_status TEXT NOT NULL DEFAULT 'watching', -- watching, planning, completed, paused, dropped
  score REAL,
  -- per-show overrides (NULL = use global setting)
  search_query TEXT,                            -- nyaa query override
  episode_offset INTEGER NOT NULL DEFAULT 0,    -- file ep number minus AniList ep number (absolute numbering)
  group_order TEXT,                             -- JSON array of group names
  resolution TEXT,                              -- "1080p" etc
  auto_download INTEGER,                        -- NULL/1/0
  folder TEXT,                                  -- save folder override
  -- auto_from INTEGER is added by migrate_2.sql (do not add columns here: migrations run on top)
  added_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS episodes (
  show_id INTEGER NOT NULL REFERENCES shows(id) ON DELETE CASCADE,
  number INTEGER NOT NULL,
  aired_at INTEGER,          -- sub air time, unix UTC, from AniList airingSchedule
  dub_aired_at INTEGER,      -- from animeschedule, nullable
  title TEXT,
  watched_at INTEGER,
  watched_source TEXT,       -- player, manual, import
  -- auto INTEGER is added by migrate_2.sql
  PRIMARY KEY (show_id, number)
);

CREATE TABLE IF NOT EXISTS files (
  id INTEGER PRIMARY KEY,
  path TEXT NOT NULL UNIQUE,
  size INTEGER NOT NULL, mtime INTEGER NOT NULL,
  show_id INTEGER REFERENCES shows(id) ON DELETE SET NULL,
  episode INTEGER,           -- AniList-numbered episode (after offset)
  release_group TEXT, resolution TEXT, version INTEGER NOT NULL DEFAULT 1,
  parsed TEXT NOT NULL DEFAULT '{}',   -- JSON of parse result
  present INTEGER NOT NULL DEFAULT 1,  -- 0 when the file vanished
  first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS files_show_ep ON files(show_id, episode);

CREATE TABLE IF NOT EXISTS releases (
  id INTEGER PRIMARY KEY,
  show_id INTEGER NOT NULL REFERENCES shows(id) ON DELETE CASCADE,
  episode INTEGER,                     -- NULL for batches
  episode_end INTEGER,                 -- batch range end
  nyaa_id INTEGER UNIQUE,              -- from guid
  title TEXT NOT NULL,
  info_hash TEXT, magnet TEXT, torrent_url TEXT,
  release_group TEXT, resolution TEXT, version INTEGER NOT NULL DEFAULT 1,
  is_batch INTEGER NOT NULL DEFAULT 0,
  seeders INTEGER, leechers INTEGER, size_bytes INTEGER,
  published_at INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'candidate',  -- candidate, chosen, added, downloading, done, skipped, replaced, failed
  qbit_hash TEXT, status_note TEXT,
  first_seen INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS releases_show_ep ON releases(show_id, episode);

CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,
  kind TEXT NOT NULL,        -- see 7
  show_id INTEGER, episode INTEGER,
  message TEXT NOT NULL,
  data TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS events_ts ON events(ts);

CREATE TABLE IF NOT EXISTS accounts (
  service TEXT PRIMARY KEY,  -- anilist, mal
  user_id TEXT, user_name TEXT,
  access_token TEXT, refresh_token TEXT, expires_at INTEGER,
  push_enabled INTEGER NOT NULL DEFAULT 0
);

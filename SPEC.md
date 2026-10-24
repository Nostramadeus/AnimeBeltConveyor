# AnimeBeltConveyor (ABC) — build spec

Written by Fable 5.1, 2026-10-22. This file is the contract. Workers implement what is here; if the spec is silent on a decision, write the question in your PROGRESS file and pick the simplest safe default, marked `TODO(spec)` in code.

## 0. What it is

A local app. One Go binary, SQLite file, web UI in the browser at `http://127.0.0.1:7700`.
Loop it automates ("the conveyor"):

```
AniList airing schedule ──> show X ep N airs at T
                                  │
nyaa.si RSS search ──> candidates for (X, N) ──> release rules pick one
                                  │
qBittorrent WebUI ──> magnet added, save path root/<Show>/
                                  │
folder scanner ──> file appears ──> episode N = "on disk"
                                  │
player watcher (mpv/VLC window) ──> open long enough ──> episode N = "watched"
                                  │
optional: push progress to AniList / MAL
```

Every rule and threshold above is a setting, editable in the UI. Nothing is hard-coded.

## 1. Stack and layout

- Go 1.27 (`go`, `GOPATH=%USERPROFILE%\go`, `GOCACHE=%LOCALAPPDATA%\go-build`). Module path `github.com/Nostramadeus/AnimeBeltConveyor`.
- SQLite via `modernc.org/sqlite` (pure Go, no cgo). File: `<datadir>/abc.db`.
- `<datadir>` = `%APPDATA%\abc` on Windows, `~/.config/abc` on Linux, `~/Library/Application Support/abc` on macOS. Override with `--data <dir>`.
- Web UI = static files in `web/`, embedded with `embed.FS`, served at `/`. Plain HTML, CSS, JS. No framework, no build step, no npm.
- Dependencies allowed: `modernc.org/sqlite`, `github.com/fsnotify/fsnotify`, `golang.org/x/sys` (Windows window enumeration). Anything else: ask in PROGRESS first.
- Logging: `log/slog`, text handler, to stderr and to `<datadir>/abc.log` (rotating is not needed in v1).
- Ports: HTTP `7700` (setting `server.port`). Bind `127.0.0.1` only.

```
cmd/abc/main.go                  flags: --data, --port, --no-browser; starts everything
internal/store/                  sqlite open, migrations (schema.sql embedded), typed queries
internal/settings/               typed settings struct, defaults, load/save from `settings` table, change hooks
internal/anilist/                GraphQL client (no auth for public data; OAuth for list push)
internal/mal/                    MAL API v2 client (OAuth2 PKCE) — import + push only
internal/animeschedule/          animeschedule.net client for dub air times (optional, off by default)
internal/parse/                  filename / torrent-title parser (anitomy-style)
internal/match/                  "does this parsed title belong to show X, which episode" logic
internal/release/                picks one candidate per (show, episode) from rules
internal/nyaa/                   RSS fetch + parse, magnet building
internal/qbit/                   qBittorrent WebUI v2 client
internal/scan/                   root + extra folder scan, fsnotify watcher, file table upkeep
internal/player/                 watched detection; `player_windows.go`, `player_other.go` (stub)
internal/sched/                  the loops (airing refresh, nyaa poll, scan, player poll, pushes)
internal/api/                    HTTP JSON API + SSE
internal/events/                 event log writer (the "conveyor feed")
web/index.html, web/app.js, web/styles.css, web/mock.js
```

## 2. Database schema (`internal/store/schema.sql`)

```sql
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
```

Migrations: `meta.schema_version`; `schema.sql` is idempotent for v1. Later versions add `migrate_N.sql` files applied in order.

## 3. Settings (`internal/settings`)

One Go struct, JSON-tagged, stored as one JSON blob under `settings.key = 'app'`. `GET /api/settings` returns it; `PUT /api/settings` replaces it (validated) and fires change hooks (scheduler re-reads intervals, scanner re-reads folders, qbit client re-logs-in).

```go
type Settings struct {
  Server    struct{ Port int `json:"port"` }                                              // 7700
  Library   struct{ Root string `json:"root"`; Extra []string `json:"extra"`; ScanIntervalMin int `json:"scan_interval_min"`; VideoExts []string `json:"video_exts"` } // Root "", Extra [], 10, [mkv mp4 avi webm ts m4v]
  Qbit      struct{ Enabled bool; URL string; Username, Password string; Category string; SavePathStyle string } // false, "http://127.0.0.1:8080", "", "", "abc", "show_folder" | "root"
  Conveyor  struct{ Enabled bool; PollIntervalMin int; NyaaBase string; NyaaCategory string; GroupOrder []string; Resolution string; FallbackHours float64; SkipBatchesWhileAiring bool; AutoReplaceV2 bool; MinSeeders int; LookbackDays int } // true, 15, "https://nyaa.si", "1_2", ["SubsPlease","Erai-raws"], "1080p", 12, true, true, 1, 14
  Watched   struct{ Mode string; Fraction float64; DefaultRuntimeMin int; PollSec int; PlayerTitles []string } // "fraction" | "on_open" | "manual"; 0.5; 24; 5; ["mpv","VLC media player","MPC-HC","MPC-BE","PotPlayer"]
  Titles    struct{ Language string } // "romaji" | "english"
  Dubs      struct{ Enabled bool; ShowInCalendar bool } // false, true
  Push      struct{ AniList bool; MAL bool } // false, false (accounts table holds tokens)
  Open      struct{ PlayerPath string } // "" = OS default handler
  UI        struct{ Theme string; Accent string; ReducedMotion bool } // "glass", "", false   ("glass" | "floating" | "light": only "glass" is built in v1; others are placeholders in the selector, greyed out)
  Notify    struct{ Toast bool } // false (v1: placeholder, no implementation)
}
```

Validation: Port 1024-65535; Fraction 0.05-1; intervals ≥ 1; Root must exist if non-empty; URL parseable.

## 4. External services

### AniList (`internal/anilist`)
- Endpoint `https://graphql.anilist.co`. No auth for public queries. Rate limit: 90 req/min; the client has a token bucket of 1 req/sec and retries once on 429 using `Retry-After`.
- Queries:
  - `Search(q string) []MediaSummary` — `Page(perPage:10){media(search:$q,type:ANIME){...}}`
  - `Media(id int) Media` — fields: id, idMal, title{romaji english native}, synonyms, coverImage{extraLarge color}, bannerImage, format, status, season, seasonYear, episodes, duration, nextAiringEpisode{episode airingAt}, airingSchedule(perPage:50, page:N){nodes{episode airingAt}} (page through all).
  - `Season(season, year) []MediaSummary` — `Page(perPage:50){media(season:$s,seasonYear:$y,type:ANIME,format_in:[TV,TV_SHORT,ONA],sort:POPULARITY_DESC)}` page through.
  - `UserList(userName or token) []ListEntry` — `MediaListCollection(userName:$u,type:ANIME)` → status, progress, score, mediaId.
  - `PushProgress(token, mediaId, progress)` — `SaveMediaListEntry(mediaId:$id, progress:$p)`.
- OAuth: implicit grant is not usable from a local server without a registered redirect; v1 uses the "authorization code" flow with a user-created AniList API client (user pastes client id + secret in Settings; redirect URI `http://127.0.0.1:7700/api/auth/anilist/callback`). Document the steps in the Settings page.

### MyAnimeList (`internal/mal`)
- API v2 `https://api.myanimelist.net/v2`. OAuth2 with PKCE (code_challenge = code_verifier, method plain, as MAL requires). User pastes client id from `https://myanimelist.net/apiconfig`. Redirect `http://127.0.0.1:7700/api/auth/mal/callback`.
- `UserList(token)` → `/users/@me/animelist?fields=list_status&limit=1000` paged.
- `PushProgress(token, malId, num_watched_episodes)` → `PATCH /anime/{id}/my_list_status`.
- Import maps MAL ids to AniList via `Media(idMal:)` lookups.

### animeschedule.net (`internal/animeschedule`)
- Public API `https://animeschedule.net/api/v3/timetables/dub?year=&week=` (and `/sub`). Needs a free API token (`Authorization: Bearer`). Setting field `Dubs.Token`. Matched to shows by AniList id (`anilist_id` field in their response) when present, else by normalized romaji title. Off by default; when off nothing is fetched.

### nyaa.si (`internal/nyaa`)
- RSS: `GET {NyaaBase}/?page=rss&q={query}&c={category}&f=0`. User-Agent `AnimeBeltConveyor/0.1 (+github.com/Nostramadeus/AnimeBeltConveyor)`. Sequential requests, ≥ 2 s apart, one query per show per poll.
- Item fields: title, link (.torrent url), guid (`https://nyaa.si/view/<id>` → nyaa_id), pubDate, `nyaa:seeders`, `nyaa:leechers`, `nyaa:infoHash`, `nyaa:size` (string like "1.4 GiB" → bytes), `nyaa:categoryId`.
- Magnet: `magnet:?xt=urn:btih:<hash>&dn=<urlencoded title>` + trackers `udp://open.stealth.si:80/announce`, `udp://tracker.opentrackr.org:1337/announce`, `udp://exodus.desync.com:6969/announce`, `udp://tracker.torrent.eu.org:451/announce`.
- Query per show: `search_query` override if set, else `title_romaji` with punctuation stripped. The parser + matcher does the real filtering, the query just narrows.

### qBittorrent (`internal/qbit`)
- WebUI API v2: `POST /api/v2/auth/login` (cookie SID), `POST /api/v2/torrents/add` (form: `urls`=magnet, `savepath`, `category`, `rename` optional), `GET /api/v2/torrents/info?category=abc`, `GET /api/v2/torrents/properties?hash=`.
- `Add(magnet, savePath, category) (hash)`: hash parsed from the magnet (btih, lowercase 40-hex; base32 converted).
- `Status(hash) (progress float, state string, contentPath string)`.
- If qbit is disabled or unreachable, releases still get `status='chosen'` and the UI shows a "Copy magnet / Open magnet" button (fallback C from the grill).

## 5. Parsing and matching

### `internal/parse`
`Parse(name string) Result` where
```go
type Result struct {
  Title string; Episode int; EpisodeEnd int; Season int; Version int; Group string
  Resolution string; Source string; Codec string; Audio string; Ext string
  IsBatch bool; Raw string; Confidence float64
}
```
Handles, with table tests for every line:
- `[SubsPlease] Sousou no Frieren - 05 (1080p) [ABCD1234].mkv`
- `[Erai-raws] Sousou no Frieren - 05 [1080p][HEVC][Multiple Subtitle][ENG].mkv`
- `[Judas] Frieren - Beyond Journey's End - S01E05v2 [1080p][HEVC x265 10bit][Multi-Subs].mkv`
- `[EMBER] Sousou no Frieren (2023) (Season 1) [BDRip] [1080p HEVC 10 bits] (Frieren Beyond Journey's End)` → batch
- `Sousou.no.Frieren.S01E05.1080p.WEB.H264-VARYG.mkv`
- `[Group] Title 2nd Season - 03 [720p].mkv`, `Title S2 - 03`, `Title Season 2 - 03`, `Title II - 03`
- `[Group] Title - 1041 (1080p)` (absolute numbering, One Piece)
- `[Group] Title - 05.5 ...` → Episode 5, flag recap in Raw (treat as skip)
- `[Group] Title - 01-12 (Batch)`, `[Group] Title (01-12) [1080p]`, `Title - Vol.1`
- `[Group] Title - 05 END`, `[Group] Title - 05 [v2]`, `Title - 05v2`
- Movies with no episode → Episode 0, IsBatch false.
Existing Go port `github.com/nssteinbrenner/anitogo` may be used if its licence is MIT/BSD/Apache; vendor nothing, just `go get`. If its output fails any of the test lines above, wrap it with fixes rather than forking.

### `internal/match`
`Match(r parse.Result, shows []ShowTitles) (showID int, episode int, ok bool, score float64)`
- Normalize: lowercase, NFKC, strip punctuation except digits/letters/spaces, collapse spaces, drop leading "the".
- Candidate titles per show: romaji, english, native, synonyms, plus `search_query`. Also generated season variants: for a show whose romaji ends in `2nd Season`/`Season 2`/`Part 2`/` II`, add the base title with `season=2` requirement.
- Score = exact normalized equality 1.0; token-set Jaccard ≥ 0.85 → that value; else no match. Season number must agree when both sides have one. Episode = `r.Episode - show.episode_offset`; reject if ≤ 0 or > episodes_total+1 when total known.
- Tie between two shows: prefer the one with `list_status='watching'`, then the most recent `season_year`.

### `internal/release`
`Pick(cands []Candidate, rules Rules, now time.Time, airedAt time.Time) (chosen *Candidate, reason string)`
Rules (per show overrides fall back to global settings):
1. Drop candidates: wrong resolution (unless rule allows lower when none at target after fallback), seeders < MinSeeders, batches while show status is RELEASING and SkipBatchesWhileAiring, recap/`.5` episodes.
2. If any candidate from the group earliest in GroupOrder exists → highest version, then most seeders.
3. Else if `now - airedAt < FallbackHours` → wait (return nil, "waiting for preferred group").
4. Else best of the rest: highest version, then most seeders, then newest.
5. Already have a file on disk for this episode with version ≥ candidate version → skip. If AutoReplaceV2 and candidate version > file version and same group → choose, mark old release `replaced`, delete old file only after new one is `done` (and only inside Root; never delete outside Root).

## 6. Loops (`internal/sched`)
All intervals from settings; each loop: `run once at start, then ticker`; each run writes an event on error; loops never overlap themselves (mutex, skip if busy).
- **airing**: every 6 h, and on show add: refresh `Media(id)` for shows with status RELEASING or NOT_YET_RELEASED; upsert episodes.aired_at from airingSchedule; set next_airing. Event `airing.updated` only when something changed.
- **conveyor** (nyaa): every PollIntervalMin; for each show with auto_download on and list_status watching: fetch RSS, parse+match each item, upsert releases (candidate), then for each (show, episode) where aired (aired_at ≤ now) and no file present and no release in added/downloading/done: `release.Pick`; if chosen → qbit add (or mark chosen for manual) → status added, event `release.added`. Also runs on demand via `POST /api/conveyor/run`.
- **qbit sync**: every 1 min when any release is added/downloading: update status; on progress 1.0 → `done`, event `download.done`; trigger a scan of that save path.
- **scan**: every ScanIntervalMin plus fsnotify (debounced 3 s): walk Root and Extra; for new/changed video files parse+match → files row; mark present=0 for vanished. Event `file.found` on newly matched episodes. Link `releases.status='done'` by matching path under the save folder.
- **player**: every PollSec: `player.Current() []Playing{Path or Title}`; for each window title, find a files row whose base name is contained in the title; accumulate seconds in memory per file; when accumulated ≥ Fraction × runtime (duration_min or DefaultRuntimeMin) → mark episode watched (source player), event `episode.watched`, then if push enabled → push. Mode `on_open`: mark on first sighting. Mode `manual`: loop disabled.
- **push**: on watched; retries queued in memory, drop after 3 failures with an event.

Windows `player.Current()`: `EnumWindows` + `GetWindowTextW` via `golang.org/x/sys/windows` (`NewLazySystemDLL("user32.dll")`), filter visible windows whose title contains any of `Watched.PlayerTitles`. Non-Windows: return nil, and the Settings page shows "player detection: not available on this OS, use manual ticks or `on_open` via the in-app Open button".

In-app open (`POST /api/files/{id}/open`): if `Open.PlayerPath` set → exec it with the path; else Windows `rundll32 url.dll,FileProtocolHandler <path>` (no console window), macOS `open`, Linux `xdg-open`. Also starts a 1-shot watch so `on_open`/fraction logic works even if title detection fails: fraction mode counts time since open if the player process we launched is still alive.

## 7. Event kinds
`show.added`, `show.removed`, `airing.updated`, `release.found`, `release.added`, `release.waiting`, `release.skipped`, `download.done`, `download.failed`, `file.found`, `file.missing`, `episode.watched`, `episode.unwatched`, `push.ok`, `push.failed`, `scan.done`, `error`.

## 8. HTTP API (`internal/api`) — JSON, all under `/api`
Errors: `{"error":"message"}` with 4xx/5xx. Times are unix seconds UTC; the UI formats in local time.

```
GET  /api/state                     → {settings_ok, qbit:{enabled,connected}, root_ok, counts:{watching,on_disk_unwatched,airing_this_week}, now}
GET  /api/settings                  → Settings
PUT  /api/settings                  ← Settings → Settings (validated)
GET  /api/settings/test/qbit        → {ok, version|error}
GET  /api/settings/test/anilist     → {ok|error}

GET  /api/shows?status=watching     → [ShowCard]  (all when no status)
POST /api/shows            {anilist_id, list_status?}  → ShowCard  (fetches media, creates episodes, kicks airing+conveyor for it)
GET  /api/shows/{id}                → ShowDetail {show, episodes:[EpisodeRow], releases:[...], files:[...]}
PATCH /api/shows/{id}      {list_status?, search_query?, episode_offset?, group_order?, resolution?, auto_download?, folder?, score?}
DELETE /api/shows/{id}?delete_files=0
POST /api/shows/{id}/episodes/{n}/watched    {watched:true|false}
POST /api/shows/{id}/refresh                 (re-fetch AniList + run conveyor for this show)

GET  /api/search?q=                 → [MediaSummary] (AniList)
GET  /api/season?season=FALL&year=2026 → [MediaSummary] with `tracked:bool`

GET  /api/upnext                    → [EpisodeRow] episodes on disk, unwatched, ordered by show last activity then number
GET  /api/calendar?from=&to=        → [CalendarItem{show_id,title,episode,aired_at,dub_aired_at,on_disk,watched,release_status}]
GET  /api/events?after=0&limit=100  → [Event]
GET  /api/events/stream             → SSE: `event: <kind>\ndata: <json>\n\n` for every new event, plus `ping` every 25 s

GET  /api/releases?show_id=&episode=    → [Release]
POST /api/releases/{id}/add         (force add this candidate to qbit)
POST /api/releases/{id}/skip
POST /api/conveyor/run              → {started:true}
POST /api/scan/run                  → {started:true}

POST /api/files/{id}/open
POST /api/files/{id}/reveal         (explorer /select, open -R, xdg-open dir)

GET  /api/auth/anilist/start        → {url}        GET /api/auth/anilist/callback?code=
GET  /api/auth/mal/start            → {url}        GET /api/auth/mal/callback?code=
POST /api/import/anilist            {user_name}   → {added, updated}   (public list, no token needed)
POST /api/import/mal                → needs token  → {added, updated}
GET  /api/accounts                  → [{service,user_name,push_enabled}]
DELETE /api/accounts/{service}
```

Shapes:
```
ShowCard   {id, anilist_id, title (per Titles.Language), title_romaji, title_english, cover_url, banner_url, color, status, format, episodes_total, list_status, score,
            progress:{watched, on_disk, aired}, next:{episode, aired_at}|null, last_activity}
EpisodeRow {show_id, show_title, cover_url, banner_url, color, number, title, aired_at, dub_aired_at, watched_at, watched_source,
            file:{id, path, name, size, release_group, resolution, version}|null,
            release:{id, status, title, seeders}|null}
```

## 9. Web UI (`web/`)

Direction A from the design page the design reference page (not part of this repo): dark frosted glass over the blurred banner of the show you touched last. Read that page before writing CSS. Fonts: Inter (variable) self-hosted is not allowed (no files > 1 MB in repo); use `font-family: Inter, "Segoe UI", system-ui, sans-serif` and `font-variant-numeric: tabular-nums`. No Google Fonts calls (app is offline-first).

Pages (hash router `#/`, `#/library`, `#/add`, `#/calendar`, `#/settings`, `#/show/{id}`):
1. **Dashboard** `#/`: hero = first Up-next episode (banner behind, 60% dark overlay + 24px blur; title, "Episode N", group/res chip, buttons Play (open file), Mark watched, Reveal). Below: "Up next" rail (cards with poster, progress bar watched/on-disk/aired as three stacked segments), "This week" strip (7 columns, local days, chips per episode with state dot: grey aired-not-found, amber downloading, green on disk, dim watched), "Conveyor" feed (events, newest first, live via SSE), small stat tiles (watching, on disk unwatched, airing this week, qbit status).
2. **Library** `#/library`: tabs by list_status; poster grid (2:3 posters, hover tilt ≤ 8°, respects reduced-motion); badge `on_disk / aired` bottom-left, watched tick when complete; search box; sort by last activity / title / next airing.
3. **Show** `#/show/{id}`: banner header; per-show settings panel (query, offset, group order, resolution, auto); episode table rows: number, title, aired (local), state chips, file name, actions (Play, Watched toggle, Pick release dropdown of candidates with seeders, Skip).
4. **Add** `#/add`: AniList search + current season grid with "Track" buttons; "Import from AniList username" and "Import MAL (login)" buttons.
5. **Calendar** `#/calendar`: week view, prev/next, rows = shows, cells = episodes; sub and dub times when dubs enabled.
6. **Settings** `#/settings`: one form section per settings group in section 3, every field with a one-line help text; Test buttons for qbit and AniList; account connect buttons; "Theme" selector with glass active and floating/light disabled with "(planned)". Save = PUT. Validation errors shown inline.
Empty states: first run (no Root set) → dashboard shows a 3-step setup card: pick root folder, enable qBittorrent WebUI (with the exact qBittorrent menu path), add a show.

Mock mode: `web/mock.js` loaded when `?mock=1`, intercepts `fetch` for `/api/*` with realistic data (6 shows, 3 up next, events) so the UI can be developed and screenshotted without the Go server. Real cover/banner URLs from AniList CDN are fine in mock.

Accessibility and quality rules: all text on glass or solid, never on raw banner; contrast ≥ 4.5:1 for body text (check with a quick script in the browser console if unsure); keyboard focus visible; `prefers-reduced-motion` disables tilt and blur animation; works at 1280 and at 390 px width.

## 10. Build, run, test
```
set GOPATH=%USERPROFILE%\go
set GOCACHE=%LOCALAPPDATA%\go-build
go build -o abc.exe ./cmd/abc
abc.exe --data .dev-data --no-browser
go test ./...
```
- `go vet ./...` clean. Unit tests required for parse, match, release, nyaa (RSS fixture), qbit (httptest), anilist (httptest), settings validation, store migrations.
- Never auto-open a browser from a test or an agent run (`--no-browser`). the user opens the URL.
- No popups: anything launched for testing must be headless or hidden.
- One `go build` at a time per agent. No parallel test runners beyond `go test ./...` default.

## 11. Lanes
- **core** (Go): store, settings, events, sched, api, qbit, scan, player, cmd/abc. Owns `go.mod`.
- **brain** (Go, pure packages, no DB): parse, match, release, nyaa, anilist, mal, animeschedule. Each exposes the interfaces in sections 4-5 and ships tests. Must not import `internal/store`.
- **ui** (web/): all static files + mock.js. Must not touch Go files.
Integration (wiring brain into core loops) is done by core after brain's packages exist; until then core compiles against stub implementations in `internal/sched/stubs.go` behind the same function signatures, then deletes the stubs.

## 12. v1.1 addendum
Folders, downloader modes, setup guide and icon: see `SPEC-v1.1.md` (wins over this file where they differ).

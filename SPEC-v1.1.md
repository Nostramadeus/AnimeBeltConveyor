# SPEC v1.1 addendum — folders, downloader modes, setup guide, icon (2026-10-22)

Extends SPEC.md. Where this file and SPEC.md disagree, this file wins. The owner's request, verbatim intent:
"let me decide where to download files into on boot or as a setting, past folders the app knows about and new
folders as well, robust; a tutorial on bootup for qBittorrent and what to do with a different torrent app;
open the folder from the app; a cool icon for the exe."

## 1. Settings changes (`internal/settings`)

```go
// Folder is one library folder. Paths are absolute and filepath.Clean'ed.
type Folder struct {
    Path     string `json:"path"`
    Label    string `json:"label"`     // optional, shown instead of the path's last segment
    Download bool   `json:"download"`  // new episodes are saved here; at most one ACTIVE folder has it
    Active   bool   `json:"active"`    // scanned. false = "past folder": remembered, not scanned, one click to re-add
    AddedAt  int64  `json:"added_at"`  // unix seconds
}

type Library struct {
    Folders         []Folder `json:"folders"`
    Root            string   `json:"root,omitempty"`  // LEGACY (v1 blob). Normalize folds it into Folders then clears it.
    Extra           []string `json:"extra,omitempty"` // LEGACY, same.
    ScanIntervalMin int      `json:"scan_interval_min"`
    VideoExts       []string `json:"video_exts"`
}
func (l Library) DownloadDir() string   // path of the active folder with Download=true, "" if none
func (l Library) ActiveDirs() []string  // paths of active folders, in list order, no duplicates
func (l Library) Find(path string) (int, bool) // index by cleaned path (case-insensitive on Windows)

type Downloader struct {
    Mode       string `json:"mode"`        // "qbittorrent" | "magnet" | "torrent_file" | "manual"
    TorrentDir string `json:"torrent_dir"` // torrent_file mode: the folder the torrent app watches for .torrent files
}
// Settings gets `Downloader Downloader `json:"downloader"`` after Library.

type Qbit struct {
    Enabled bool `json:"enabled,omitempty"` // LEGACY: Normalize maps true -> Downloader.Mode "qbittorrent" when Mode is "", then clears it
    // URL, Username, Password, Category, SavePathStyle unchanged
}

type Open struct {
    PlayerPath    string `json:"player_path"`
    FolderOnStart bool   `json:"folder_on_start"` // open the download folder in the file manager when ABC starts (main.go, after listen)
}

type UI struct {
    // Theme, Accent, ReducedMotion unchanged
    SetupDone bool `json:"setup_done"` // the setup guide was finished or skipped
}
```

Defaults: Folders [], Downloader {Mode "manual", TorrentDir ""}, FolderOnStart false, SetupDone false. Everything else as before.

Normalize (in this order):
1. Legacy fold: if Root != "" → append Folder{Path: Root, Download: true, Active: true, AddedAt: now} unless Find(Root) hits; each Extra → Folder{Active: true}; then Root = "", Extra = nil.
2. Legacy downloader: if Downloader.Mode == "" → "qbittorrent" when Qbit.Enabled else "manual"; Qbit.Enabled = false.
3. Each Folder: Path = filepath.Clean(strings.TrimSpace(Path)); Label trimmed; drop entries with empty Path; de-duplicate by Find (first wins, but Download/Active are OR'ed into the kept one). TorrentDir cleaned the same way ("" stays "").
4. If more than one active folder has Download=true keep the first, clear the others. If the Download folder is inactive, clear its Download flag.

Validate (field keys):
- `library.folders`: any non-absolute path → "paths must be absolute". Existence is NOT validated here (an unplugged drive must never block saving settings); it is reported by GET /api/folders and /api/state.
- `downloader.mode`: not one of the four → `must be "qbittorrent", "magnet", "torrent_file" or "manual"`.
- `downloader.torrent_dir`: required and must be an existing directory when mode == "torrent_file"; when set in other modes it must be absolute, existence not checked.
- `qbit.url`: checked when mode == "qbittorrent" or URL != "" (as before; the Enabled test goes away).
- Existing rules unchanged. `library.root` errors no longer exist.

Everywhere the v1 code used `Library.Root`: use `DownloadDir()`. `libraryDirs(c)` = `ActiveDirs()`. `SavePath` returns "" (qBittorrent's own default) when DownloadDir() == "". `show.folder` relative paths resolve against DownloadDir(). Delete guards (v2 replacement, delete-show files) stay "only inside DownloadDir()"; ABC never deletes under any other folder.

Sched OnChange: when ActiveDirs() changed → queue a rescan (as before). When a folder became inactive or was removed: mark `present=0` for files under it that are not under any still-active folder, no per-file events, one `scan.done` event "Folder <label or path> removed from the library: N files hidden" (data: {path, hidden}). Reactivating a folder is just a rescan (the files come back with `file.found` as now).

Scanner robustness (already true, keep it true and add a test): a folder whose Stat fails at scan time is skipped and its files are NOT marked missing. GET /api/folders reports `exists:false` for it.

## 2. Downloader hand-off (`internal/sched/conveyor.go`, addRelease)

`if c.Qbit.Enabled && file == nil` becomes `if c.Downloader.Mode != "manual" && file == nil`. addRelease by mode:

| mode | action | success status | failure |
|---|---|---|---|
| qbittorrent | as v1. New: if DownloadDir() != "" and it does not exist → do not call qBittorrent; status `chosen`, note `download folder not reachable: <path>` | `added` (+ qbit_hash) | `chosen` + note |
| magnet | `OpenURL(magnet)` = the OS magnet: handler (whatever torrent app registered it) | `added`, qbit_hash "", note `sent to the magnet link handler` | `chosen` + note |
| torrent_file | download `release.TorrentURL` (new `brain.FetchTorrent(ctx, url) ([]byte, error)`, nyaa client HTTP + the 2 s throttle, 20 s timeout, max 8 MB) and write `<TorrentDir>/<SafeName(title)>.torrent` via a `.part` temp name + rename, so the watching client never reads a half file. Empty TorrentURL → behave like `magnet`. | `added`, note `saved <file> to the watch folder` | `chosen` + note |
| manual | as v1 (`chosen`, "add it manually") | — | — |

Event `release.added` messages: "<show> <ep>: sent to qBittorrent (…)", "…: sent to your torrent app (magnet link) (…)", "…: .torrent saved to the watch folder (…)", "…: chosen, add it manually (…)" with data `{release_id, title, manual:bool, magnet, note, mode}`.
The qbit sync loop skips `added` releases whose qbit_hash is "" (nothing to poll); the scanner links them to `done` by path as before (SPEC 6). ForceAdd (POST /api/releases/{id}/add) uses the same mode logic; in manual mode it returns the release as `chosen` with the magnet (as v1).

## 3. API additions (`internal/api`)

```
GET  /api/folders            → [FolderState]
     FolderState = Folder + {exists:bool, free_bytes:int64|null, file_count:int}   (file_count = present files under it)
POST /api/folders            {path, label?, download?:bool, create?:bool} → [FolderState]
     path → filepath.Abs + Clean. Not a directory: create=true → os.MkdirAll (400 {"error":"cannot create the folder: <err>","fields":{"path":...}} on failure);
     create=false → 400 {"error":"folder does not exist","fields":{"path":"folder does not exist"}}.
     Same path already known: reactivate it (Active=true) and apply label/download; never a duplicate row.
     First active folder ever → Download=true automatically. download=true moves the flag off the previous folder.
     Saves settings through the Manager (hooks fire → rescan).
PATCH /api/folders           {path, label?, download?:bool, active?:bool} → [FolderState]
     404 unknown path. active=false = "past folder". download=true on an inactive folder → 400 "activate the folder first".
     download=false on the download folder clears it (no download folder → qBittorrent uses its own default).
DELETE /api/folders?path=    → [FolderState]   forgets the folder completely (files hidden like deactivation)
GET  /api/fs/list?path=      → {path, parent:string|null, dirs:[{name, path, free_bytes?}], error?:string}
     path "" → the roots: Windows = every drive letter via windows.GetLogicalDrives, {name:"C:\", path:"C:\", free_bytes}; unix = "/" plus $HOME, /mnt, /media, /Volumes when they exist.
     Otherwise the subdirectories of path, sorted case-insensitively, skipping names starting with "." or "$", "System Volume Information", and at a drive root also "Windows", "Program Files", "Program Files (x86)", "ProgramData", "Recovery", "PerfLogs".
     Unreadable path → 200 with dirs:[] and error:"access denied" (never 500). Non-absolute path → 400.
GET  /api/fs/suggest         → [{path, label, exists:bool, free_bytes:int64|null}]
     Order: <exe dir>\Anime ("Next to abc.exe"); <UserHomeDir>\Videos\Anime ("Your Videos folder"; ~/Videos on unix);
     <X>:\Anime for every FIXED drive other than the system drive ("Drive X:", DRIVE_FIXED via windows.GetDriveType);
     then <system drive>:\Anime. Non-Windows: $HOME/Anime instead of the drive rows. Duplicates by path removed.
POST /api/folders/open       {path} → {ok:true}
     Opens the folder in the file manager (explorer "<path>" / open / xdg-open; reuse the launcher with a new Reveal-less OpenDir).
     Allowed paths: any library folder (active or past), downloader.torrent_dir, the save folder of any show, the data dir. Else 403.
     Missing folder → 409 {"error":"folder does not exist"}.
POST /api/shows/{id}/open-folder → {ok:true}   = /api/folders/open on SavePath(show); 409 "no episode downloaded yet, the folder does not exist" when missing; 409 "no download folder set" when SavePath is "".
GET  /api/settings/test/magnet → {ok:true, handler:string} | {ok:false, error}
     Windows: read HKEY_CLASSES_ROOT\magnet\shell\open\command default value (golang.org/x/sys/windows/registry); missing → ok:false "no app is registered for magnet links".
     Other OS: {ok:true, handler:"unknown"} (xdg-open decides).
GET  /api/state  adds:
     downloader:{mode, ok:bool, error:string}   ok = qbittorrent: connected; magnet: handler registered (Windows) / true; torrent_file: torrent_dir exists; manual: true
     download_dir:{path, exists:bool}|null, folders_active:int, exe_dir:string, data_dir:string, setup_done:bool
     root_ok stays and means download_dir != null && exists (compat with the v1 UI).
```
Localhost origin/host guard applies to all of these as to every /api route.

## 4. main.go

- Data dir precedence: `--data` flag → `<exe dir>\data` when that directory exists (portable mode) → os.UserConfigDir()/abc. Log which one. README documents: "create a `data` folder next to abc.exe to make it portable".
- After listening: if `open.folder_on_start` and DownloadDir() exists → OpenDir(DownloadDir()) (never when `--no-browser` is set; sessions must not pop windows).
- `--version` flag prints the version string (ldflags `-X main.version=`), default "dev".

## 5. Icon and build (`assets/icon/`, `build.ps1`, `build.sh`)

- Fable produces `assets/icon/abc.svg` (master), `abc-256.png`, `abc.ico` (256/48/32/16 PNG-compressed entries) and `web/icon.svg` (same drawing) before the core lane finishes. Until then the lane may use any placeholder .ico to test the build step.
- Core lane: `build.ps1` and `build.sh` (stdlib only): if `cmd/abc/rsrc_windows_amd64.syso` is missing or older than `assets/icon/abc.ico`, run `go run github.com/akavel/rsrc@v0.10.2 -ico assets/icon/abc.ico -arch amd64 -o cmd/abc/rsrc_windows_amd64.syso` (rsrc is MIT; it is a build tool, not a dependency of the binary; `go run pkg@version` does not touch go.mod). Then `go build -ldflags "-X main.version=<git describe or dev>" -o abc.exe ./cmd/abc`. The .syso and .ico ARE committed so a plain `go build` also yields the icon. Keep the console window (no `-H windowsgui`): without a tray icon the console is the only way to see and stop ABC.
- `web/index.html`: `<link rel="icon" href="icon.svg">`; the header mark becomes `<img src="icon.svg" alt="" class="logo">` next to the name.

## 6. Web UI (`web/`)

### 6.1 Setup guide `#/setup`
Opens automatically at boot when `state.setup_done` is false and the hash is empty or `#/`. Deep links to other pages still work. Settings › "Setup guide" button and Home's compact setup card link to it. Finishing OR skipping PUTs `ui.setup_done=true`. Layout: one glass panel, a 5-dot stepper on top (done = check, current = accent), Back / Next / "Skip setup" (text button, bottom-left). Each step saves on Next (settings or folders API) and shows inline errors from `fields`. Keyboard: Enter = Next. Works at 390 px.

Step 1 "Welcome": the one-line pitch + the belt diagram as a 5-chip row: AniList schedule → nyaa.si → your torrent app → your folder → your player. Three short lines: what ABC does by itself, what you click (Play), what it never does (no uploads of your data; nothing is sent anywhere but AniList/nyaa/your torrent app).

Step 2 "Where do episodes go?": suggestion cards from GET /api/fs/suggest (label, path, free space, "exists" chip), a "Browse…" button (folder picker modal, 6.3), a typed-path field, checkbox "Create the folder if it does not exist" (default on). Below: "Already have anime somewhere? Add those folders too, ABC scans them but never writes there" with the same picker → POST /api/folders {download:false}. Next requires one download folder.

Step 3 "Which app downloads?": four radio cards (mode):
- **qBittorrent** (recommended): numbered steps with the exact menu path: `Tools › Options… › Web UI`, tick **Web User Interface (Remote control)**, port **8080**, tick **Bypass authentication for clients on localhost** (or set a username and password and type them below), press **OK**. Fields: address, username, password. **Test** button (GET /api/settings/test/qbit) with the result inline. A note: "qBittorrent saves each episode into <download folder>\<Show>\ by itself; nothing else to set."
- **Another torrent app, via magnet links**: "ABC hands each magnet link to the app Windows uses for magnet links (Deluge, Transmission, uTorrent, BitTorrent, Tixati, Free Download Manager, … all register themselves). Set that app's download folder to **<download folder>** so ABC finds the files." Buttons: **Test** (GET /api/settings/test/magnet shows the registered app or "none"). Note: "The app decides the sub-folder; ABC matches episodes by file name, so any layout works."
- **Another torrent app, via a watch folder**: "Most torrent apps can auto-load .torrent files from a folder. Pick that folder here, then turn the feature on in your app:" list: Deluge `Preferences › Plugins › AutoAdd`; Transmission `Edit › Preferences › Downloading › Automatically add .torrent files from`; uTorrent / BitTorrent `Options › Preferences › Directories › Automatically load .torrents from`; qBittorrent `Tools › Options › Downloads › Monitored Folder`. Folder picker for torrent_dir. Same "set its download folder to <download folder>" note.
- **I'll add them by hand**: "ABC lists each chosen release with a magnet button and a copy button. Nothing downloads on its own."
Next saves downloader.mode (+ qbit.* / torrent_dir).

Step 4 "Watching": titles.language select, watched.mode select + fraction, the player-titles list collapsed under "My player is not listed". One line: "ABC reads player window titles only on this PC."

Step 5 "First shows": the Add page's search box and this-season grid embedded (reuse the components) plus "Import from AniList / MyAnimeList username" (existing import). Finish button → setup_done → `#/` with a toast "Setup done. The conveyor runs every <poll> minutes; press Run conveyor to start now."

### 6.2 Settings changes
- **Library** section: the `library.root` / `library.extra` fields are replaced by a folder manager: one row per folder (label or last path segment, full path small, chips: "Downloads go here" (accent), "Not reachable" (bad), "Past folder" (muted), file count). Row actions: Open (POST /api/folders/open), Make download folder, Remove (→ past), and for past folders Re-add and Forget. "Add folder" button → folder picker modal. Keep scan_interval_min and video_exts. Add `open.folder_on_start` here as "Open the download folder when ABC starts".
- New **Downloads** section before qBittorrent: the four mode cards from step 3 (same copy, same tests). The **qBittorrent** section keeps url/category/username/password/save_path_style; `qbit.enabled` is gone; the section is shown dimmed unless mode == qbittorrent, with a one-line "Pick qBittorrent under Downloads to use these".
- **Server** section: show data_dir and exe_dir (read-only, from state) with "Open data folder" / "Open ABC's folder" buttons (POST /api/folders/open).
- Settings header: "Setup guide" button → `#/setup`.

### 6.3 Folder picker modal (shared)
Title, suggestion chips (GET /api/fs/suggest, only in the download-folder context), breadcrumb of the current path, a list of sub-folders from GET /api/fs/list (click = enter, "Up" = parent, roots view when path ""), a typed-path field that stays in sync, checkbox "Create it if missing", Choose / Cancel. Errors (access denied) show inline, never block. Escape closes. Focus trapped. Works at 390 px (list scrolls).

### 6.4 Open folder everywhere
- Home: top action row gets "Open folder" (download folder) when state.download_dir exists; and a banner when download_dir is set but `exists:false`: "Download folder <path> is not reachable. Plug the drive in, or pick another folder." with buttons Retry (refetch state) and Pick folder (→ Settings library).
- Show page header: "Open folder" → POST /api/shows/{id}/open-folder; 409 → toast with the message.
- Episode rows keep Reveal (explorer /select) as v1.
- Home setup card (v1, three steps) becomes compact: shown only while download_dir is null or downloader.mode is "manual" and no show exists; it says which steps are missing and links to `#/setup`.

### 6.5 Mock mode (`web/mock.js`)
Implements every route in section 3 against an in-memory folder list and a fake drive tree (C:\ with Users\You\Videos, D:\ with Anime, Games; E:\ marked not reachable). `?setup=1` starts with setup_done=false. `?missing=1` makes the download folder unreachable. The existing mock click-through list gets: wizard end-to-end, add folder via browser, make download folder, remove → past → re-add, open-folder 409 toast.

## 7. Tests the lanes must add
Core: settings legacy fold (root+extra blob → folders, enabled → mode), Validate cases, folders API CRUD + reactivate + first-folder-gets-download, fs/list on a temp tree (hidden names skipped, unreadable → 200), suggest de-dup, folders/open 403 for a path outside the allow-list, addRelease per mode (magnet via an injected opener, torrent_file writes `<SafeName>.torrent` via .part, missing download dir → chosen), deactivation hides files, scanner skips an unreadable folder without marking files missing, data dir precedence (portable `data` dir).
UI: the mock click-through list in 6.5, contrast ≥ 4.5:1 on the new panels, screenshots of setup steps 2 and 3, the folder picker, and Settings › Library at 1280 and 390 px.

## 8. Per-folder scan depth (2026-10-23, owner request: "recursive scan should be off by default and turned on per path")

Settings: `Folder` gets `Scan string `json:"scan"``: `"flat"` = this folder only; `"shows"` = the folder and one level of subfolders (default; qBittorrent saves `<folder>/<Show>/episode.mkv`); `"deep"` = every subfolder. Normalize: "" or unknown → "shows". Validate: the download folder may not be "flat" while `qbit.save_path_style == "show_folder"` → `library.folders`: "the download folder needs at least 'show folders' because episodes are saved one level down".

Scanner: `scan.Walk` and `scan.NewWatcher` take `[]scan.Dir{Path string, MaxDepth int}` (flat 0, shows 1, deep -1 = unlimited) instead of `[]string`; a file deeper than MaxDepth is not seen, a folder deeper is skipped. The watcher's live watch depth is min(MaxDepth, WatchDepth) (deep → 2). `libraryDirs` builds the list from the active folders. The full rescan (every `scan_interval_min`) and the live watch both respect the depth.

API: `POST /api/folders` and `PATCH /api/folders` accept `scan`; FolderState carries it. Everything else unchanged.

UI: each folder row in Settings › Library (and the same rows in the setup guide, if any) gets a select "Scan: this folder only / show folders (one level) / all subfolders" that PATCHes right away, with the help line "One level is what qBittorrent needs. All subfolders on a whole drive is slow." The picker's "Add" uses "shows". Mock: supports `scan`.

## 9. Auto-download guard rails (2026-10-23, owner request: a freshly added show must not pull its whole backlog; a torrent the user removed must not come back)

Observed: adding a show with 17 aired episodes started 6 downloads at once, because `lookback_days` only filters nyaa upload dates and recent uploads cover old episodes. Also: when the user removes a torrent in qBittorrent, the release goes `failed` and the next pass would pick another candidate for the same episode.

### Settings (`conveyor`)
- `lookback_days` (existing, default 14) gains a second meaning, the one its help text already claims: an episode is auto-downloaded only if it AIRED within the last `lookback_days`. The nyaa upload-date filter in storeCandidates stays as it is.
- `max_release_gib float64 `json:"max_release_gib"`` default 4, 0 = no limit: a candidate whose `size_bytes` exceeds it is never picked (Pick reason "too big: 7.2 GiB"); applies to auto picks, ForceAdd stays manual and ignores it. Validate: >= 0.

### Schema (migration file `internal/store/migrate_2.sql`, see store.go: migrate_N.sql applied in order)
- `episodes.auto INTEGER` (NULL = follow the rule, 0 = never auto-download, 1 = always auto-download even if old).
- `shows.auto_from INTEGER` (NULL = no floor; N = episodes below N are never auto-downloaded).

### Rule (conveyor per-episode loop, replaces nothing else)
An aired episode without a present file and without an active release is auto-handled only when `autoAllowed(e, sh, now, c)`:
1. `e.Auto == 0` → no ("turned off").
2. `sh.AutoFrom != nil && e.Number < *sh.AutoFrom` → no ("before episode N").
3. `e.Auto == 1` → yes.
4. `e.AiredAt == nil` → no ("no air date").
5. `aired_at < now - lookback_days` → no ("older than 14 days").
6. else yes.
Episodes that fail the rule get no release.waiting / release.skipped events (silence, they are listed in the UI). `chosen` releases of such episodes are not re-sent either (a chosen one came from a pass before the rule, or from ForceAdd in manual mode: ForceAdd keeps working).

### Automatic `auto = 0`
- qbit sync loop, the "removed from qBittorrent" branch: besides marking the release failed, set the episode's auto = 0 and log `release.failed` as "<show> ep N: torrent is no longer in qBittorrent; not downloading it again (press Add to retry)". A torrent qBittorrent itself reports as errored keeps the old message and does NOT set auto = 0.
- ForceAdd on an episode with auto == 0 leaves auto as is (the one explicit add is enough; if it is removed again it stays off).

### API
- `PATCH /api/shows/{id}` accepts `auto_from: int|null`.
- `POST /api/shows/{id}/episodes/{n}/auto {auto: null|0|1}` → EpisodeRow.
- `POST /api/shows/{id}/download-missing` → `{added:int, skipped:int}`: for every aired episode without a present file and without an active release, pick by the rules ignoring autoAllowed and ignoring max_release_gib, ForceAdd it. `skipped` = episodes with no acceptable candidate. One event per added release as ForceAdd logs; one `conveyor.*`-style summary event "Download all missing: N added, M without a release" (kind `release.added`, data {bulk:true}).
- `GET /api/shows/{id}/download-missing` → `{count:int, bytes:int64}` preview for the confirm dialog (same selection, no side effects).
- EpisodeRow gains `auto: null|0|1` and `auto_reason: string` ("" when auto applies or does not matter, else one of "turned off", "before episode N", "no air date", "older than 14 days", "removed from qBittorrent" (when auto==0 and the latest release of the episode is failed with that note)). ShowDetail.show gains `auto_from`.

### UI
- Episode row (show page, calendar tooltip untouched): when aired, no file, no active release and `auto_reason != ""`: chip "Not auto · <reason>" (muted, `title` = "ABC downloads by itself only episodes that aired in the last N days. Press Add to get this one."), the candidate select + Add stay. A small toggle icon on the row: "Auto-download this episode" (sets auto 1) / "Stop auto-downloading" (sets auto 0); shown as a tertiary button with aria-label.
- The existing X (skip release) button on an episode row ALSO calls the episode auto endpoint with 0, so one click means "leave this episode alone".
- Show page header: button "Download all missing" (shown when GET preview count > 0) → confirm dialog "Add N releases, about X GiB, to qBittorrent?" (or "...hand N magnet links to your torrent app" / "...save N .torrent files" / in manual mode "mark N releases chosen" per downloader mode) → POST → toast with added/skipped.
- Show settings (the existing per-show form): field "Auto-download from episode" (int, empty = all) with help "Episodes before this number are never downloaded by themselves."
- Settings › Conveyor: `lookback_days` label "Auto-download episodes that aired in the last (days)", help "Older episodes wait for you: press Add on the episode, or Download all missing on the show." New field `max_release_gib` "Skip releases bigger than (GiB)", help "0 = no limit. Protects against mislabeled batches and remuxes.".
- Add page / setup step 5: under the Track button nothing changes; after Track, the toast says "Tracking <show>. Episodes from the last 14 days download by themselves; older ones are on the show page." (14 = lookback_days).
- mock.js: episodes carry auto/auto_reason, the two endpoints, the preview.

### Tests
Core: autoAllowed table test (all six rules), conveyor pass with a 17-episode show where only the last-14-days episodes get releases, max_release_gib skip reason, removed-from-qBittorrent sets auto=0 and the next pass adds nothing for that episode, download-missing adds the old ones and returns counts, migration adds both columns to an existing v1.1 database. UI: mock click-through additions (chip shown, toggle, X sets auto 0, download-all dialog + toast).

## 10. Free-space guard and size preference (2026-10-23, owner request: "don't download automatically if there's < 2 GB free", "prefer small downloads, ASW / Judas have smaller 1080p encodes, not hardcoded")

### Settings (`conveyor`)
- `min_free_gib float64 `json:"min_free_gib"`` default 5, 0 = off. Before every AUTOMATIC hand-off (conveyor pass, not ForceAdd / download-missing) the free space of the volume holding `DownloadDir()` is read; if `free - release.size_bytes < min_free_gib` the release is not added. One `release.waiting` event per episode per reason (noteOnce): "<show> ep N: waiting, only 1.8 GiB free on F: (keeps 5 GiB free)". In magnet / torrent_file modes the user's client picks the folder, so the check uses DownloadDir() when set, else is skipped. Validate: >= 0.
- `size_preference string `json:"size_preference"`` = "small" (default) | "any". Pick (internal/release) tie-breaking inside the chosen tier: today the best candidate is chosen by seeders. With "small": within the same group tier (same preferred group, or the fallback tier) and the same resolution, the SMALLEST candidate with `seeders >= min_seeders` wins; candidates with unknown size (size_bytes 0) sort after known ones. With "any": behaviour as today (most seeders). Group order still comes first: a preferred group's release beats a smaller release from an unlisted group; the preference only orders candidates that are otherwise equal.
- Help text for `group_order` in the UI gains one line: "Groups known for small HEVC 1080p encodes: ASW, Judas, EMBER. Add them here if you want small files first." (text only, nothing hardcoded in the rules).

### Code placement
- Free-space reading lives in a new `internal/diskfree` package (`Free(path string) (int64, error)`, Windows via GetDiskFreeSpaceEx on the volume of the nearest existing ancestor, unix via statfs) and `internal/api` folders.go / suggest switch to it (no duplicate implementations).
- `/api/state.download_dir` gains `free_bytes`.

### UI
- Settings › Conveyor: "Keep this much free on the download drive (GiB)" (`min_free_gib`, 0 = off), "When releases are otherwise equal" select (`size_preference`: "take the smallest file" / "take the most seeded"). The group-order help line above.
- Home: when `download_dir.free_bytes < min_free_gib` show the banner "Only X GiB free on <drive>. Automatic downloads are paused until there is more room." (bad tone, with "Open folder").
- Episode rows: the waiting reason already surfaces through release.waiting events in the feed; no new chip.
- mock: fields + banner via `&lowdisk=1`.

### Tests
Core: Pick with size_preference small vs any (same tier smallest wins, preferred group still beats smaller unlisted, unknown size last), the conveyor skips the hand-off below the threshold and logs once, ForceAdd ignores it, diskfree returns > 0 for the temp dir. UI: settings fields, banner in mock.

## 11. Bulk "watched up to here" and an honest Home hero (2026-10-23, owner request: "no way to mark a prefix of episodes watched"; "Home says caught up while Juukishi has unwatched episodes")

### API
- `POST /api/shows/{id}/watched-through {number:int}` → ShowDetail. Marks every episode 1..number that is not yet watched as watched (watched_source "manual", watched_at now); never unmarks. Pushes progress ONCE to AniList / MAL when push is on (progress = max watched number), not once per episode. Logs one event `episode.watched` "<show>: episodes 1-N marked watched (K new)" with data {through:N, newly:K, bulk:true}. 400 when number < 1 or above episodes_total (when known) + 50.
- `GET /api/missing` → [{show_id, title, cover_url, color, count, first_episode, last_episode}] = per show with list_status watching: aired episodes, not watched, no present file, no active release (added/downloading/done). Ordered by the show's last_activity desc. Empty array when none.
- `/api/state.counts` gains `missing` (sum of the above).

### UI
- Episode row: next to the Watched button, a tertiary icon button "Up to here" (aria-label "Mark episodes 1 to N watched", title the same) → POST watched-through → re-render + toast "Episodes 1-N marked watched". Hidden on a row that is already watched when every lower one is watched too (nothing to do).
- Show header, under the progress bar: "Watched through episode [ N ] Set" (number input, prefilled with the highest watched number; Set posts; Enter submits).
- Home hero, when nothing is on disk: if `GET /api/missing` is non-empty → title "Nothing to play yet", line "N aired episodes have no file: <show A> 7-13, <show B> 1-3" (up to 3 shows, then "and M more"), buttons "Download all missing" for the first show (same confirm dialog as the show page) and "Open <show>". Else the existing "You are caught up" with the next airing time. The "On disk, unwatched" stat tile stays; add a 5th tile? No: replace the tile subtitle with "ready to play · N aired without a file" when N > 0.
- mock: the two endpoints, `missing` count, click-through: up-to-here on ep 9 marks 1-9, header Set 17, hero shows the missing line with lowdisk off and no files, "caught up" when missing is empty.

### Tests
Core: watched-through marks only unwatched, never unmarks, single push call (fake pusher counts), event once, bounds; missing excludes watched / on-disk / active-release episodes and non-watching shows; state count. UI: the click-through above, screenshots of the hero variant and a row at 1280 and 390.

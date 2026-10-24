# SPEC v1.3 — absolute episode numbers, failed-release cleanup (2026-10-23)

Two Approved rows from BACKLOG.md. Decisions below are final (Fable); the builder does not invent rules. If a case is not covered here, stop and write the question in PROGRESS-v13.md.

Read first: SPEC.md (sections 2, 5, 8), SPEC-v1.1.md (sections 9-10), AI-HANDOFF.md. Code style, test style and the mock-mode UI (`web/mock.js`) are as they are in the repo.

## 1. Absolute episode numbers

Problem: sequels have two numberings. AniList numbers "Black Clover 2nd Season" from episode 1; many release groups (SubsPlease, most batch groups) number from episode 1 of the whole series, so season episode 3 is released as "173". Other groups (Erai-raws) release the same episode as "2nd Season - 03". ABC must match both and show both numbers.

### 1.1 Data
- `internal/store/migrate_3.sql` (new; bump `SchemaVersion` to 3). Content, in this order:
  ```sql
  -- v1.3 section 1: absolute numbering. NULL = not computed yet / unknown; 0 = no prequel; N = episodes before this show in the series.
  ALTER TABLE shows ADD COLUMN abs_offset INTEGER;
  -- v1.3 section 2: when a status was last set (unix seconds). NULL for old rows (treated as first_seen).
  ALTER TABLE releases ADD COLUMN status_at INTEGER;
  -- v1.3 section 2.1: a torrent the user removed in the client is "skipped", not "failed".
  UPDATE releases SET status='skipped', status_note='removed in qBittorrent'
  WHERE status='failed' AND status_note='removed from qBittorrent';
  ```
- `store.Show` gains `AbsOffset *int` (`json:"abs_offset"`, NULL <-> nil). Read and written in every shows query/insert/update that touches per-show columns. `shows.episode_offset` keeps its meaning: the MANUAL offset, 0 = none.
- Effective offset of a show, one helper used everywhere (`store` or `sched`, builder's choice, exported): `func EffectiveOffset(s Show) int`: `EpisodeOffset` when != 0, else `*AbsOffset` when AbsOffset != nil, else 0.

### 1.2 AniList: the prequel chain
- `QueryMedia` adds, on the Media object (every page is fine, only page 1 is read):
  `relations { edges { relationType node { id type format episodes seasonYear startDate { year month day } } } }`.
- `anilist.Media` gains `Prequel *RelNode` and `anilist.RelNode{ID, Format string, Episodes int, SeasonYear int, StartDate int64 /* yyyymmdd as a sortable int, 0 unknown */}`. Prequel selection rule (function `pickPrequel(edges)`, unit-tested): among edges with `relationType == "PREQUEL"`, `node.type == "ANIME"`, `node.format` in {TV, TV_SHORT, ONA}: pick the one with the greatest StartDate; ties and unknown dates: greatest id. Movies, OVAs, specials and non-anime are never part of the chain. No such edge -> nil.
- New Brain method `Prequel(ctx, id int) (episodes int, prequel *RelNode, err error)`: fetches one media by id with a small query `QueryRelations` (`id episodes relations { edges { ... } }`, one request, no airing schedule). `OfflineBrain` returns `ErrOffline`. `sched.Media` gains `Prequel *RelNode` (mirror of the anilist struct) filled by `mediaOf`.
- Chain walk (`sched.absOffset(ctx, m Media) (offset *int)`, unit-tested with a fake brain):
  1. `p := m.Prequel`; if nil -> return 0 (known: no prequel).
  2. `sum := 0`; loop at most 30 hops with a visited set: if `p.Episodes <= 0` -> return nil (a prequel count is unknown). `sum += p.Episodes`. Fetch `eps, next, err := brain.Prequel(ctx, p.ID)`; on error -> return nil (leave unknown, log at debug, never fail the add). The node's own Episodes from the edge is used for the sum; the fetch only supplies the next hop. If `next == nil` -> return &sum. Else `p = next`.
  3. Cycle or hop cap -> nil.
- When it runs: `createFromMedia` (add) and `RefreshShow` (the user's Refresh) always run the walk and write `abs_offset`. `airingRun` (periodic) runs the walk only for shows whose `abs_offset IS NULL` (unknown), so the steady-state cost is zero extra AniList requests. MAL-import adds go through createFromMedia and get it too.
- Logging: when the computed value differs from the stored one, log an `airing.updated` event: "<title>: absolute numbering starts at <sum+1>" with data `{abs_offset}`. No event for 0 or nil.

### 1.3 Matching (files on disk and nyaa releases, same code path)
`match.ShowTitles` gains `AbsOffset int` (0 = none/unknown) and `EpisodesKnown int` (highest episode number that exists in the show's episodes table; 0 = none). `sched.ShowTitles` mirrors it; `sched.Titles` fills both (`store.MaxEpisodeNumbers(ctx) (map[int64]int, error)`, one query: `SELECT show_id, MAX(number) FROM episodes GROUP BY show_id`; Titles gains a ctx + store call, or the caller passes the map: builder's choice, keep it one query per conveyor/scan run, not per show).

`episodeFor` becomes (O = `EpisodeOffset` if != 0 else `AbsOffset`):
- `r.Episode == 0`: unchanged (batch without range, movie rule).
- `O == 0`: unchanged (ep = n; reject n <= 0; reject n > EpisodesTotal+1 when total known).
- `O > 0`, n = r.Episode, `bound` = EpisodesTotal if > 0 else EpisodesKnown:
  - season interpretation `S = n`, valid when n >= 1 and (bound == 0 ? n <= O : n <= bound). (No +1 slack here; with a positive offset a number one past the total is read as absolute. When nothing is known about the length, a number above the offset cannot be a season number.)
  - absolute interpretation `A = n - O`, valid when A >= 1 and (bound == 0 || A <= bound+1).
  - preference: (a) the release title carries a season marker (`r.Season > 0`, or `seasonSuffix(Normalize(r.Title))` returns num > 0) -> season; (b) else the show's own titles carry a season marker (any candidate from `candidates(s)` with `season > 0` that came from a suffix match, i.e. `seasonSuffix` on romaji/english/synonyms returned num > 0) and the release title does not -> absolute; (c) else season.
  - result: the preferred interpretation when valid, else the other when valid, else reject.
- Worked examples (these become table tests in `match_test.go`):
  | show | O | total | known | release title / n | expected |
  |---|---|---|---|---|---|
  | Jujutsu Kaisen 2nd Season | 24 | 23 | 23 | "Jujutsu Kaisen - 25" | ep 1 |
  | Jujutsu Kaisen 2nd Season | 24 | 23 | 23 | "Jujutsu Kaisen 2nd Season - 01" | ep 1 |
  | Jujutsu Kaisen 2nd Season | 24 | 23 | 23 | "Jujutsu Kaisen - 05" | ep 5 (show has a marker, release lacks one -> absolute preferred, A = -19 invalid -> season 5) |
  | Black Clover (2026 entry, title without marker) | 170 | 0 | 4 | "Black Clover - 173" | ep 3 |
  | Black Clover (2026) | 170 | 0 | 4 | "Black Clover - 03" | ep 3 |
  | Black Clover (2026) | 170 | 51 | 51 | "Black Clover - 52" | ep 52 is > total -> season invalid; A = -118 invalid -> reject |
  | Steel Ball Run 2nd & 3rd STAGE (synonym "Steel Ball Run Part 2") | 1 | 24 | 24 | "Steel Ball Run - 05" | ep 4 (show has marker, release lacks -> absolute) |
  | Steel Ball Run 2nd & 3rd STAGE | 1 | 24 | 24 | "Steel Ball Run Part 2 - 04" | ep 4 (season) |
  | manual episode_offset 12, total 12 | 12 | 12 | 12 | "Title - 13" | ep 1 |
  | manual episode_offset 12, total 12 | 12 | 12 | 12 | "Title - 01" | ep 1 |
  | O = 0 | 0 | 12 | 12 | "Title - 13" | ep 13 (unchanged slack) |
  Note: "2nd & 3rd STAGE" is not one of the existing suffix regexes. Add `stage` to `reOrdSeason` (`(\d{1,2})(?:st|nd|rd|th) (?:season|stage|part|cour)`) and accept an `&`-joined pair by normalising "2nd & 3rd stage" -> marker num 2 (first number). Add these cases to the existing candidates tests.
- `storeCandidates` (conveyor.go line ~96) and the scanner use the same `EffectiveOffset` for `EpisodeEnd` (batch range end): `EpisodeEnd - O` only when the batch start was read as absolute; otherwise unchanged. Simplest: `Match` returns the chosen interpretation's shift (`shift int`, 0 or O) as an extra return value, or sets it on a result struct; callers subtract `shift` from `EpisodeEnd`. Keep the Brain interface signature change minimal and mirror it in `OfflineBrain`/`RealBrain`.

### 1.4 API
- `ShowFull` gains `AbsOffset *int json:"abs_offset"`. `episode_offset` stays.
- `EpisodeRow`, `CalendarItem` and whatever `/api/upnext` returns gain `AbsNumber *int json:"abs_number"`: `number + O` when O > 0, else null.
- `PATCH /api/shows/{id}` field `episode_offset` unchanged (integer >= 0). The UI converts (1.5).
- `POST /api/shows/{id}/refresh` (existing Refresh) recomputes `abs_offset` (1.2).
- mock.js: show id of the mock "sequel" (pick one existing mock show, or add one named "Jujutsu Kaisen 2nd Season") gets `abs_offset: 24` and its episodes `abs_number`. Add `&seq=1` query flag only if the existing mock shape makes that easier; otherwise always present.

### 1.5 UI
- Episode table `#` cell: the season number as today; when `abs_number` is set, a second line `<div class="sub dim">173</div>` (13 px, muted) under it. Column stays narrow (min-width so 4 digits fit).
- Every "Episode N" / "Ep N" text where the row has `abs_number`: show "Episode 3 (173)" / "Ep 3 (173)". Places: `home.js` heroCard + upCard, `app.js` showHead "Play episode N" button and "Next Ep N" chip (use `abs_offset` from the show for the chip: `next.episode + abs_offset`), `shows.js` Play chip ("Ep 3 (173)"), calendar week entries and `calChips` ("Ep 3 (173)"), toasts that say "Episode N marked watched" keep the plain N (short). aria-labels keep the plain N.
- "Rules for this show" panel: the "Episode offset" field becomes **Absolute numbering starts at** (`id="ss-abs"`, `name="abs_start"`, type number, min 1, step 1). Value: `episode_offset + 1` when `episode_offset != 0`, else empty. Placeholder: `abs_offset != null ? String(abs_offset + 1) + ' (from AniList)' : 'unknown'`. Help text: "The number this show's first episode has when the whole series is counted from episode 1. Groups like SubsPlease number sequels that way. Empty = use AniList's prequel chain. Example: 171 means episode 3 is released as "173"." Validation: empty, or an integer >= 1; error text "Whole number 1 or more, or empty for automatic." Save: empty -> `episode_offset: 0`; N -> `episode_offset: N - 1`. The override counter counts it when `episode_offset != 0` (unchanged logic, new label).
- Nothing changes visually for shows with O = 0.

## 2. Failed-release cleanup

Problem: red "Failed" chips stay on an episode forever, including after the user removed the torrent in qBittorrent on purpose.

### 2.1 Removed in the client = skipped, not failed
- `qbitRun` (`internal/sched/files.go`, the `qbit.ErrNotFound` branch): status becomes `skipped`, note `"removed in qBittorrent"`. Constant `noteRemoved = "removed in qBittorrent"`, `AutoRemoved = "removed in qBittorrent"` (the auto_reason text). The event text stays as it is ("torrent is no longer in qBittorrent; not downloading it again (press Add to retry)"), kind `download.failed` becomes `release.skipped`.
- `AutoContext.Reason`: the "removed" branch checks `latest.Status == store.RelSkipped && strings.HasPrefix(latest.StatusNote, noteRemoved)` (was RelFailed + the old note). Update the tests that assert the old text/status.
- `turnOffAuto` unchanged (the episode still stops auto-downloading).
- Old rows: the migrate_3 UPDATE above.
- "Failed" now means a real failure: a qBittorrent error state, a magnet hand-off error, a .torrent fetch error, or whatever else already sets RelFailed.

### 2.2 Failed chips expire
- `SetReleaseStatus` and `SetReleaseQbit` also write `status_at = now`. Insert writes `status_at = first_seen`. `store.Release` gains `StatusAt int64 json:"status_at"` (0 when NULL).
- `currentRelease` (`internal/api/views.go`): a `failed` release is ignored when either
  (a) another release of the same show covers the same episode and has a greater `ID` (any status except `replaced`), or
  (b) `now - statusAt > 7*24h` where `statusAt = StatusAt if != 0 else FirstSeen`.
  Pass `now` and the full `rels` slice in (both already available in `showRows`). Unit tests: fresh failed shows; failed older than 7 days hidden; failed with a newer candidate hidden; failed with a newer *replaced* row still shown.
- The Releases panel on the show page still lists every release with its status chip (unchanged).
- The `auto_reason` and "Not found yet" chips take over the cell when the failed chip is hidden (no code change expected; verify in mock mode).

### 2.3 Clear by hand
- New route `POST /api/shows/{id}/releases/clear-failed`, body optional JSON `{"episode": N}`. Sets every `failed` release of the show (or only those covering episode N) to `skipped`, note `"cleared by user"`. Response `{"cleared": n}`. 404 for an unknown show, 400 for a bad body. Same origin/host guard as every other POST. No event is logged; the show page re-renders from the response. Auto flags are not touched (clearing never re-enables auto-download; the chip explains why an episode is not auto).
- Show page `⋯` menu (showHead): item **Clear failed releases** with the count as `menu-sub` ("3 releases"), shown only when `d.releases` has at least one `failed`. Click -> POST without body -> toast "Cleared N failed releases" -> rerender.
- Episode `⋯` menu: item **Clear** (icon `x`, aria-label "Clear the failed release for episode N") shown when the row's current release has status `failed`. Click -> POST with `{episode: N}` -> toast "Cleared" -> rerender.
- mock.js: implement the route (flip statuses in the mock data, return the count) so the click-through works offline; give one mock episode a fresh `failed` release and one an old one (status_at 10 days ago) so both branches of 2.2 are visible.

## 3. Out of scope (do not build)
- `/api/missing` counting chosen releases (open item from v1.2), native clients, anything in Ideas.
- Changing how auto-download decides (section 9 of SPEC-v1.1) beyond the note/status rename in 2.1.

## 4. Verification (the builder runs all of it and reports results, not claims)
1. `gofmt -l .` empty; `go vet ./...`; `go test -count=1 ./...` all packages green (set `GOPATH=%USERPROFILE%\go GOCACHE=%LOCALAPPDATA%\go-build`, Go binary `go`; `-timeout 120s`).
2. Migration dry run: copy `.dev-data\abc.db` to the scratch folder, open it with the new binary (`--data <scratch copy dir> --no-browser --port 7702`), confirm `schema_version = 3`, `abs_offset` column present, no failed+removed rows left. Never touch `%APPDATA%\abc` or the live `.dev-data`.
3. Live run on port 7702 against that scratch copy (headless, no browser window, muted): `GET /api/shows/{id}` shows `abs_offset`; PATCH `episode_offset` round-trips; `POST .../releases/clear-failed` returns a count and the row disappears from the episode's current release; cross-origin POST still 403.
4. Mock-mode click-through in a headless browser (the repo's existing way, see web/mock.js and the v1.2 builder's notes in AI-HANDOFF.md): the `#` cell shows two numbers on the sequel show; hero/card/chip texts read "Episode 3 (27)" style; "Absolute numbering starts at" saves empty and 171 and shows the right placeholder; Clear failed releases and the per-episode Clear both work and toast; the old failed release is hidden, the fresh one shown. Zero console errors.
5. Headless screenshots (2000 and 390 px) of the sequel show page and Home, saved as `web/.shot-v13-*.png`; the builder looks at them and fixes what is wrong before reporting.
6. README: one bullet each under the feature tour ("Sequels show both numbers: episode 3 (173)" and "Failed downloads clear themselves after 7 days or with one click"). No screenshots needed.

Report format (PROGRESS-v13.md and the final message): model id first line; done / not done per section number; every command run with its result; open questions.

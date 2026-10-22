/* Mock backend for the ABC web UI. app.js loads this file only when the URL has ?mock=1.
   It replaces window.fetch for "/api/..." and window.EventSource for "/api/events/stream",
   so every page works with no Go server (file:// or any static host).
   Extra URL switches:
     &fresh=1    first run: SPEC 3 defaults, no shows, no events (setup card + empty states)
     &mocklive=0 no scripted live events
     &setup=1    ui.setup_done=false: the setup guide opens at boot (SPEC-v1.1 6.1)
     &missing=1  drive D: is unplugged, so the download folder D:\Anime is not reachable (6.4 banner)
     &nofiles=1  no files on disk and no "done" releases: Home shows "Nothing to play yet" (SPEC-v1.1 11)
     &many=1     12 shows (6 more from the Fall 2026 pool on every list; still 3 with files, 2 missing) for grid screenshots (SPEC-v1.2 7)
   v1.1 (SPEC-v1.1 section 3): folders, fs/list, fs/suggest, folders/open, shows/{id}/open-folder and
   settings/test/magnet run against an in-memory folder list and a fake drive tree:
     C:\ (Users\You\Videos, Downloads\Torrents\watch, AppData\Roaming\abc, Apps\abc), D:\ (Anime, Games),
     E:\ listed but not reachable (access denied).
   Show metadata (ids, titles, cover/banner URLs, colours) is real AniList data fetched 2026-10-22.
   Airing times, files, releases and events are invented and placed relative to "now". */
(() => {
  'use strict';
  const H = 3600; const D = 86400; const W = 7 * D;
  const P = new URLSearchParams(location.search);
  const FRESH = P.get('fresh') === '1';
  const LIVE = P.get('mocklive') !== '0';
  const SETUP = P.get('setup') === '1';
  const MISSING = P.get('missing') === '1';
  const LOWDISK = P.get('lowdisk') === '1'; // SPEC-v1.1 10: D: has 1.8 GiB free, below min_free_gib 5
  // SPEC-v1.1 11: &nofiles=1 = nothing on disk (no files, no "done" releases), so Home shows "Nothing to play yet".
  const NOFILES = P.get('nofiles') === '1';
  const MANY = P.get('many') === '1';
  const now = () => Math.floor(Date.now() / 1000);
  const T0 = now();
  const r30 = (t) => Math.round(t / 1800) * 1800;
  const clone = (o) => JSON.parse(JSON.stringify(o));
  const CDN = 'https://s4.anilist.co/file/anilistcdn/media/anime/';
  const pad = (n) => String(n).padStart(2, '0');
  const hex8 = (a, b) => ((Math.imul(a * 2654435761 + b * 40503, 2246822519) >>> 0).toString(16).toUpperCase() + '00000000').slice(0, 8);
  const hash40 = (a, b) => Array.from({ length: 5 }, (_, i) => hex8(a + i, b * 7 + i)).join('').toLowerCase();
  const TRACKERS = ['udp://open.stealth.si:80/announce', 'udp://tracker.opentrackr.org:1337/announce', 'udp://exodus.desync.com:6969/announce', 'udp://tracker.torrent.eu.org:451/announce'];
  const magnet = (h, title) => `magnet:?xt=urn:btih:${h}&dn=${encodeURIComponent(title)}${TRACKERS.map((t) => '&tr=' + encodeURIComponent(t)).join('')}`;

  // ---------------------------------------------------------------- settings (SPEC 3 defaults)
  const DEFAULTS = {
    server: { port: 7700 },
    library: { folders: [], scan_interval_min: 10, video_exts: ['mkv', 'mp4', 'avi', 'webm', 'ts', 'm4v'] },
    downloader: { mode: 'manual', torrent_dir: '' },
    qbit: { url: 'http://127.0.0.1:8080', username: '', password: '', category: 'abc', save_path_style: 'show_folder' },
    conveyor: { enabled: true, poll_interval_min: 15, nyaa_base: 'https://nyaa.si', nyaa_category: '1_2', group_order: ['SubsPlease', 'Erai-raws'], resolution: '1080p', fallback_hours: 12, skip_batches_while_airing: true, auto_replace_v2: true, min_seeders: 1, lookback_days: 14, max_release_gib: 4, min_free_gib: 5, size_preference: 'small' },
    watched: { mode: 'fraction', fraction: 0.5, default_runtime_min: 24, poll_sec: 5, player_titles: ['mpv', 'VLC media player', 'MPC-HC', 'MPC-BE', 'PotPlayer'] },
    titles: { language: 'romaji' },
    dubs: { enabled: false, show_in_calendar: true, token: '' },
    push: { anilist: false, mal: false, anilist_client_id: '', anilist_client_secret: '', mal_client_id: '' },
    open: { player_path: '', folder_on_start: false },
    ui: { theme: 'glass', accent: '', reduced_motion: false, setup_done: false },
    notify: { toast: false },
  };
  const settings = clone(DEFAULTS);
  settings.ui.setup_done = !SETUP;
  if (!FRESH) {
    // Populated demo: D:\Anime is the download folder, Videos is scanned too, an old drive is a past folder,
    // and qBittorrent is the downloader but not reachable.
    settings.library.folders = [
      // Videos has no scan value on purpose: the server normalizes "" to "shows" (SPEC-v1.1 8).
      { path: 'D:\\Anime', label: '', download: true, active: true, added_at: 1729000000, scan: 'shows' },
      { path: 'C:\\Users\\You\\Videos', label: '', download: false, active: true, added_at: 1729000100 },
      { path: 'E:\\Old anime', label: 'Old USB drive', download: false, active: false, added_at: 1720000000, scan: 'deep' },
    ];
    settings.downloader.mode = 'qbittorrent';
    // &dlmode=magnet|torrent_file|manual: try the other downloader wordings (torrent_file uses the download folder as watch folder).
    if (['magnet', 'torrent_file', 'manual'].includes(P.get('dlmode'))) { settings.downloader.mode = P.get('dlmode'); if (P.get('dlmode') === 'torrent_file') settings.downloader.torrent_dir = 'D:\\Anime'; }
  }

  // ---------------------------------------------------------------- shows
  const DEFS = [
    { id: 1, anilist_id: 154587, mal_id: 52991, title_romaji: 'Sousou no Frieren', title_english: 'Frieren: Beyond Journey’s End', title_native: '葬送のフリーレン', synonyms: ['Frieren at the Funeral'],
      cover: 'cover/large/bx154587-qQTzQnEJJ3oB.jpg', banner: 'banner/154587-ivXNJ23SM1xB.jpg', color: '#bbf1a1', format: 'TV', status: 'RELEASING', season: 'FALL', season_year: 2023, episodes_total: 28, duration_min: 24,
      list_status: 'watching', score: null, group: 'SubsPlease', aired: 9, lastAt: r30(T0 - 26 * H), disk: [1, 8], watched: [1, 7], activity: -40 * 60, dubLag: 14 * D },
    { id: 2, anilist_id: 21, mal_id: 21, title_romaji: 'ONE PIECE', title_english: 'ONE PIECE', title_native: 'ワンピース', synonyms: ['ワンピース'],
      cover: 'cover/large/bx21-ELSYx3yMPcKM.jpg', banner: 'banner/21-wf37VakJmZqs.jpg', color: '#e49335', format: 'TV', status: 'RELEASING', season: 'FALL', season_year: 1999, episodes_total: null, duration_min: 24,
      list_status: 'watching', score: null, group: 'SubsPlease', aired: 1180, lastAt: r30(T0 - 4 * D + 90 * 60), disk: [1170, 1180], watched: [1, 1179], activity: -20 * H },
    { id: 3, anilist_id: 171018, mal_id: 57334, title_romaji: 'Dandadan', title_english: 'DAN DA DAN', title_native: 'ダンダダン', synonyms: ['DAN DA DAN: FIRST ENCOUNTER'],
      cover: 'cover/large/bx171018-60q1B6GK2Ghb.jpg', banner: 'banner/171018-SpwPNAduszXl.jpg', color: '#e47850', format: 'TV', status: 'RELEASING', season: 'FALL', season_year: 2024, episodes_total: 12, duration_min: 24,
      list_status: 'watching', score: null, group: 'SubsPlease', aired: 6, lastAt: r30(T0 - 2 * H - 10 * 60), disk: [1, 5], watched: [1, 4], activity: -55 * 60, dubLag: 21 * D },
    { id: 4, anilist_id: 170942, mal_id: 57181, title_romaji: 'Ao no Hako', title_english: 'Blue Box', title_native: 'アオのハコ', synonyms: ['青之箱'],
      cover: 'cover/large/bx170942-KKcLfQzV57nG.jpg', banner: 'banner/170942-v2GO5YNO0Q5I.jpg', color: '#5daee4', format: 'ONA', status: 'RELEASING', season: 'FALL', season_year: 2024, episodes_total: 25, duration_min: 24,
      list_status: 'watching', score: null, group: 'SubsPlease', aired: 14, lastAt: r30(T0 - 5 * D + 2 * H), disk: [1, 14], watched: [1, 14], activity: -3 * D },
    { id: 5, anilist_id: 153518, mal_id: 52701, title_romaji: 'Dungeon Meshi', title_english: 'Delicious in Dungeon', title_native: 'ダンジョン飯', synonyms: ['Dungeon Food'],
      cover: 'cover/large/bx153518-IVXPDY5ph3kO.jpg', banner: 'banner/153518-7uRvV7SLqmHV.jpg', color: '#e48650', format: 'TV', status: 'FINISHED', season: 'WINTER', season_year: 2024, episodes_total: 24, duration_min: 25,
      list_status: 'completed', score: 9, group: 'EMBER', aired: 24, lastAt: 1718290800, disk: [1, 24], watched: [1, 24], activity: -60 * D },
    { id: 6, anilist_id: 166531, mal_id: 55791, title_romaji: '[Oshi no Ko] 2nd Season', title_english: 'OSHI NO KO Season 2', title_native: '【推しの子】第2期', synonyms: ['[OSHI NO KO] Season 2'],
      cover: 'cover/large/bx166531-dAL5MsqDHUkj.jpg', banner: 'banner/166531-vUu7iDwUkC67.jpg', color: '#e44343', format: 'TV', status: 'FINISHED', season: 'SUMMER', season_year: 2024, episodes_total: 13, duration_min: 24,
      list_status: 'planning', score: null, group: 'SubsPlease', aired: 13, lastAt: 1728216000, disk: null, watched: null, activity: -10 * D },
  ];
  // Fall 2026 season list (AniList, POPULARITY_DESC), used by /api/season and /api/search.
  const SEASON_POOL = [
    [195516, 'Kusuriya no Hitorigoto 3rd Season', 'The Apothecary Diaries Season 3', 'cover/large/bx195516-MJpUZlOberqH.jpg', 'banner/195516-B5M0cPaQbQXI.jpg', '#e4a128', 'TV', 'RELEASING', 12, 85],
    [195604, 'Black Clover 2nd Season', 'Black Clover Season 2', 'cover/large/bx195604-tSZcfKbVqSEG.jpg', 'banner/195604-lQpCzTziSA17.jpg', '#f1a143', 'TV', 'RELEASING', null, 87],
    [195539, 'Cyberpunk: Edgerunners 2', 'Cyberpunk: Edgerunners 2', 'cover/large/bx195539-4LdfmKIHzuxr.png', 'banner/195539-lO5SUdp4SOrq.jpg', '#fe286b', 'ONA', 'FINISHED', 10, 85],
    [159042, 'Tensei Shitara Ken Deshita 2nd Season', 'Reincarnated as a Sword Season 2', 'cover/large/bx159042-GGFwlDskc5vR.png', 'banner/159042-X0mqyaUFBkms.jpg', '#f19328', 'ONA', 'RELEASING', 12, 75],
    [189123, 'Ao no Hako Season 2', 'Blue Box Season 2', 'cover/large/bx189123-0secXELIhkIW.jpg', 'banner/189123-s2oirY2r1Svy.jpg', '#e4ae50', 'TV', 'RELEASING', 12, 81],
    [212888, 'Tempal: Item no Chikara', 'Overgeared', 'cover/large/bx212888-NhyvqPt5aIuJ.jpg', 'banner/212888-ZsnNegBCKXQq.jpg', '#f19335', 'ONA', 'RELEASING', 12, 76],
    [210482, 'JoJo no Kimyou na Bouken: Steel Ball Run - 2nd & 3rd STAGE', "STEEL BALL RUN JoJo's Bizarre Adventure 2nd - 3rd STAGE", 'cover/large/bx210482-P1VNKbqdJ6Zj.jpg', 'banner/210482-pYAD0IbzNbgS.jpg', '#f1ae50', 'ONA', 'RELEASING', 11, 86],
    [205909, 'Hotaru no Yomeiri', 'Firefly Wedding', 'cover/large/bx205909-DM0fAzNQulod.jpg', 'banner/205909-hGeuFYrdhf9S.jpg', '#aebb35', 'TV', 'RELEASING', 12, 75],
    [191656, 'Seitokai ni mo Ana wa Aru!', 'Even the Student Council Has Its Holes!', 'cover/large/bx191656-xFHtxM8SUTdU.png', 'banner/191656-JQILdzh3FQxN.jpg', '#5dbbe4', 'TV', 'RELEASING', 12, 73],
    [178083, 'Tokyo Revengers: Santen Sensou-hen', 'Tokyo Revengers: War of the Three Titans Arc', 'cover/large/bx178083-bg7pg6TCHwtG.jpg', null, '#c99350', 'TV', 'RELEASING', 13, 76],
    [213805, 'Koori no Jouheki 2nd Season', 'The Ramparts of Ice Season 2', 'cover/large/bx213805-Nokui3uWlIlw.png', null, '#e4a143', 'TV', 'RELEASING', null, 81],
    [172192, 'Kikansha no Mahou wa Tokubetsu desu 2nd Season', "A Returner's Magic Should be Special Season 2", 'cover/large/bx172192-W7P5xEUaxF3y.jpg', 'banner/172192-jibcmb7wdbzk.jpg', '#f19328', 'TV', 'RELEASING', null, 69],
    [185756, 'Tensei Kizoku, Kantei Skill de Nariagaru 3rd Season', "As a Reincarnated Aristocrat, I'll Use My Appraisal Skill to Rise in the World Season 3", 'cover/large/bx185756-xPCl0RyQ7fXD.jpg', null, '#d6f1c9', 'TV', 'RELEASING', null, 70],
  ].map(([id, ro, en, cover, banner, color, format, status, eps, score]) => ({
    anilist_id: id, title_romaji: ro, title_english: en, cover_url: CDN + cover, banner_url: banner ? CDN + banner : null, color, format, status, season: 'FALL', season_year: 2026, episodes: eps, average_score: score,
  }));
  const POOL = [...SEASON_POOL, ...DEFS.map((d) => ({ anilist_id: d.anilist_id, title_romaji: d.title_romaji, title_english: d.title_english, cover_url: CDN + d.cover, banner_url: CDN + d.banner, color: d.color, format: d.format, status: d.status, season: d.season, season_year: d.season_year, episodes: d.episodes_total, average_score: null }))];

  const shows = [];
  const episodes = {}; // show id -> [{number, aired_at, dub_aired_at, title, watched_at, watched_source}]
  const files = [];
  const releases = [];
  let fileId = 1; let relId = 1; let showSeq = 100;
  const inR = (r, n) => !!r && n >= r[0] && n <= r[1];

  function buildShow(d) {
    const s = {
      id: d.id, anilist_id: d.anilist_id, mal_id: d.mal_id, title_romaji: d.title_romaji, title_english: d.title_english, title_native: d.title_native, synonyms: d.synonyms || [],
      cover_url: d.cover_url || CDN + d.cover, banner_url: d.banner_url !== undefined ? d.banner_url : CDN + d.banner, color: d.color, format: d.format, status: d.status, season: d.season, season_year: d.season_year,
      episodes_total: d.episodes_total, duration_min: d.duration_min, list_status: d.list_status, score: d.score,
      search_query: null, episode_offset: 0, group_order: null, resolution: null, auto_download: null, folder: null, auto_from: null,
      added_at: T0 - 90 * D, updated_at: T0 + d.activity, _activity: T0 + d.activity,
    };
    if (d.id === 2) s.search_query = 'One Piece';
    shows.push(s);
    const maxEp = d.episodes_total || d.aired + 3;
    const lastWatched = d.watched ? d.watched[1] : 0;
    const list = [];
    for (let n = 1; n <= maxEp; n++) {
      const aired = d.lastAt + (n - d.aired) * W;
      const watched = inR(d.watched, n) ? Math.min(aired + 5 * H, T0 + d.activity - (lastWatched - n) * 120) : null;
      list.push({ number: n, aired_at: aired, dub_aired_at: d.dubLag ? aired + d.dubLag : null, title: null, watched_at: watched, watched_source: watched ? (n === lastWatched ? 'player' : 'import') : null, auto: null });
      if (inR(d.disk, n)) {
        const name = `[${d.group}] ${d.title_romaji.replace(/[[\]]/g, '')} - ${pad(n)} (1080p) [${hex8(d.id, n)}].mkv`;
        files.push({ id: fileId++, path: `D:\\Anime\\${d.title_romaji.replace(/[[\]:]/g, '')}\\${name}`, name, size: 1300000000 + ((n * 7919) % 400) * 1000000, mtime: aired + 2 * H, show_id: d.id, episode: n, release_group: d.group, resolution: '1080p', version: 1, present: 1, first_seen: aired + 2 * H, last_seen: T0 });
      }
    }
    episodes[d.id] = list;
  }
  function addRelease(showId, ep, group, res, seeders, status, extra = {}) {
    const s = shows.find((x) => x.id === showId);
    const bare = s.title_romaji.replace(/[[\]]/g, '');
    const title = extra.title || `[${group}] ${bare} - ${pad(ep)} (${res})${extra.suffix || ''}`;
    const h40 = hash40(showId * 31 + ep, relId);
    const ep0 = episodes[showId].find((e) => e.number === ep);
    const r = {
      id: relId++, show_id: showId, episode: ep, episode_end: null, nyaa_id: 1980000 + relId * 13, title, info_hash: h40, magnet: magnet(h40, title), torrent_url: `https://nyaa.si/download/${1980000 + relId * 13}.torrent`,
      release_group: group, resolution: res, version: extra.version || 1, is_batch: 0, seeders, leechers: Math.round(seeders / 9), size_bytes: res === '720p' ? 720000000 : 1420000000,
      published_at: (ep0 ? ep0.aired_at : T0) + (extra.after || 40 * 60), status, qbit_hash: null, status_note: extra.note || null, first_seen: (ep0 ? ep0.aired_at : T0) + (extra.after || 45 * 60),
    };
    releases.push(r);
    return r;
  }
  if (!FRESH) {
    DEFS.forEach(buildShow);
    addRelease(1, 8, 'SubsPlease', '1080p', 2210, 'done');
    addRelease(1, 9, 'SubsPlease', '1080p', 1430, 'chosen', { note: 'qBittorrent unreachable: magnet link ready' });
    addRelease(1, 9, 'Erai-raws', '1080p', 512, 'candidate', { suffix: ' [HEVC][Multiple Subtitle]', after: 70 * 60 });
    addRelease(1, 9, 'ToonsHub', '1080p', 180, 'candidate', { title: '[ToonsHub] Sousou no Frieren S01E09 1080p WEB-DL AAC2.0 H.264', after: 95 * 60 });
    addRelease(1, 9, 'SubsPlease', '720p', 211, 'skipped', { note: 'wrong resolution' });
    addRelease(2, 1180, 'SubsPlease', '1080p', 3920, 'done');
    addRelease(3, 6, 'Erai-raws', '1080p', 96, 'candidate', { suffix: ' [HEVC][Multiple Subtitle]', after: 50 * 60, note: 'waiting for SubsPlease (fallback after 12 h)' });
    addRelease(3, 6, 'ASW', '1080p', 41, 'candidate', { title: '[ASW] Dandadan - 06 [1080p HEVC x265 10Bit][AAC]', after: 70 * 60 });
    addRelease(4, 14, 'SubsPlease', '1080p', 1610, 'done');
    // SPEC-v1.1 9 demo: [Oshi no Ko] S2 finished long ago, so none of its episodes are auto-downloaded.
    // Candidates for 1-12 (13 has none, so Download all missing reports 1 skipped); ep 3 was removed in qBittorrent.
    for (let n = 1; n <= 12; n++) addRelease(6, n, 'SubsPlease', '1080p', 300 + n * 17, 'candidate');
    addRelease(6, 3, 'SubsPlease', '1080p', 320, 'failed', { note: 'torrent is no longer in qBittorrent' });
    episodes[6].find((e) => e.number === 3).auto = 0;
    if (MANY) {
      // SPEC-v1.2 7: six more shows, none with files and none missing (caught up, paused, planning, dropped),
      // so the grid fills up while Play next keeps 3 cards and Needs you keeps 2.
      const pool = (aid) => SEASON_POOL.find((x) => x.anilist_id === aid);
      const extra = [
        [195516, 'watching', 4, [1, 4], 1 * D + 5 * H, -5 * H],
        [195604, 'watching', 3, [1, 3], 3 * D + 1 * H, -26 * H],
        [189123, 'watching', 2, [1, 2], 5 * D + 2 * H, -2 * D],
        [210482, 'paused', 5, [1, 2], 2 * D + 6 * H, -12 * D],
        [195539, 'planning', 10, null, -60 * D, -20 * D],
        [178083, 'dropped', 8, [1, 3], 4 * D, -30 * D],
      ];
      extra.forEach(([aid, list, aired, watched, nextIn, activity], i) => {
        const p = pool(aid);
        const done = p.status === 'FINISHED';
        buildShow({ id: 7 + i, anilist_id: aid, mal_id: null, title_romaji: p.title_romaji, title_english: p.title_english, title_native: null, synonyms: [], cover_url: p.cover_url, banner_url: p.banner_url, color: p.color,
          format: p.format, status: p.status, season: p.season, season_year: p.season_year, episodes_total: p.episodes || (done ? aired : null), duration_min: 24, list_status: list, score: null, group: 'SubsPlease',
          aired, lastAt: done ? r30(T0 + nextIn) : r30(T0 + nextIn - W), disk: null, watched, activity });
      });
      showSeq = 200;
    }
    if (NOFILES) {
      files.length = 0;
      for (let i = releases.length - 1; i >= 0; i--) if (releases[i].status === 'done') releases.splice(i, 1);
    }
  }

  // ---------------------------------------------------------------- accounts + events
  const accounts = FRESH ? [] : [{ service: 'anilist', user_name: 'abc-demo', push_enabled: false }];
  const events = [];
  let evId = 0;
  const ev = (ago, kind, showId, ep, message, data = {}) => events.push({ id: ++evId, ts: T0 - ago, kind, show_id: showId, episode: ep, message, data });
  if (!FRESH) {
    const fr = DEFS[0]; const op = DEFS[1]; const dd = DEFS[2]; const bb = DEFS[3];
    const ago = (t) => T0 - t;
    [
      [ago(bb.lastAt) - 50 * 60, 'release.found', 4, 14, 'Found 3 releases for Ao no Hako - 14'],
      [ago(bb.lastAt) - 52 * 60, 'release.added', 4, 14, '[SubsPlease] Ao no Hako - 14 (1080p) added to qBittorrent'],
      [ago(bb.lastAt) - 75 * 60, 'download.done', 4, 14, 'Downloaded [SubsPlease] Ao no Hako - 14 (1080p)'],
      [ago(bb.lastAt) - 76 * 60, 'file.found', 4, 14, 'Ao no Hako 14 is on disk'],
      [ago(op.lastAt) - 45 * 60, 'release.found', 2, 1180, 'Found 2 releases for ONE PIECE - 1180'],
      [ago(op.lastAt) - 46 * 60, 'release.added', 2, 1180, '[SubsPlease] One Piece - 1180 (1080p) added to qBittorrent'],
      [ago(op.lastAt) - 80 * 60, 'download.done', 2, 1180, 'Downloaded [SubsPlease] One Piece - 1180 (1080p)'],
      [ago(op.lastAt) - 81 * 60, 'file.found', 2, 1180, 'ONE PIECE 1180 is on disk'],
      [3 * D + 40 * 60, 'episode.watched', 4, 11, 'Ao no Hako 11 marked watched'],
      [3 * D + 39 * 60, 'episode.watched', 4, 12, 'Ao no Hako 12 marked watched'],
      [3 * D + 38 * 60, 'episode.watched', 4, 13, 'Ao no Hako 13 marked watched'],
      [3 * D + 37 * 60, 'episode.watched', 4, 14, 'Ao no Hako 14 marked watched'],
      [10 * D + 3 * 60, 'release.found', 6, 1, 'Found 4 releases for [Oshi no Ko] 2nd Season - 01'],
      [10 * D + 2 * 60, 'release.found', 6, 2, 'Found 3 releases for [Oshi no Ko] 2nd Season - 02'],
      [10 * D + 1 * 60, 'release.found', 6, 3, 'Found 5 releases for [Oshi no Ko] 2nd Season - 03'],
      [2 * D + 3 * H, 'file.missing', 5, 3, 'File gone: [EMBER] Dungeon Meshi - 03 (1080p).mkv'],
      [2 * D, 'scan.done', null, null, 'Scan finished: 1,214 files, 1 missing, 0 new'],
      [ago(fr.lastAt) - 35 * 60, 'release.found', 1, 9, 'Found 4 releases for Sousou no Frieren - 09'],
      [ago(fr.lastAt) - 36 * 60, 'release.skipped', 1, 9, 'Skipped [SubsPlease] Sousou no Frieren - 09 (720p): wrong resolution'],
      [ago(fr.lastAt) - 36 * 60 - 5, 'release.added', 1, 9, 'Chose [SubsPlease] Sousou no Frieren - 09 (1080p). qBittorrent unreachable: magnet link ready'],
      [ago(fr.lastAt) - 36 * 60 - 6, 'error', null, null, 'qBittorrent: connection refused at http://127.0.0.1:8080'],
      [20 * H, 'episode.watched', 2, 1179, 'ONE PIECE 1179 watched (mpv, 14 min)'],
      [6 * H, 'airing.updated', 4, 15, 'Airing schedule refreshed for 4 shows'],
      [2 * H - 50 * 60, 'release.found', 3, 6, 'Found 2 releases for Dandadan - 06'],
      [2 * H - 51 * 60, 'release.waiting', 3, 6, 'Dandadan - 06: waiting for SubsPlease. Erai-raws is out; fallback after 12 h'],
      [55 * 60, 'episode.watched', 3, 4, 'Dandadan 4 watched (mpv, 13 min)'],
      [40 * 60, 'episode.watched', 1, 7, 'Sousou no Frieren 7 watched (mpv, 12 min)'],
    ].sort((a, b) => b[0] - a[0]).forEach(([a, k, s, e, m]) => ev(a, k, s, e, m));
  }

  // ---------------------------------------------------------------- derived shapes (SPEC 8)
  const titleOf = (s) => (settings.titles.language === 'english' ? s.title_english || s.title_romaji : s.title_romaji);
  const fileOf = (sid, n) => files.find((f) => f.show_id === sid && f.episode === n && f.present);
  const curRelease = (sid, n) => {
    const rs = releases.filter((r) => r.show_id === sid && r.episode === n);
    return rs.find((r) => ['added', 'downloading', 'chosen'].includes(r.status)) || rs.find((r) => r.status === 'done') || rs.find((r) => r.status === 'failed') || null;
  };
  function card(s) {
    const eps = episodes[s.id] || [];
    const t = now();
    const next = eps.find((e) => e.aired_at > t);
    const lastAct = Math.max(s._activity || 0, ...eps.map((e) => e.watched_at || 0));
    return {
      id: s.id, anilist_id: s.anilist_id, title: titleOf(s), title_romaji: s.title_romaji, title_english: s.title_english, cover_url: s.cover_url, banner_url: s.banner_url, color: s.color,
      status: s.status, format: s.format, episodes_total: s.episodes_total, list_status: s.list_status, score: s.score,
      progress: { watched: eps.filter((e) => e.watched_at).length, on_disk: files.filter((f) => f.show_id === s.id && f.present).length, aired: eps.filter((e) => e.aired_at <= t).length },
      next: next ? { episode: next.number, aired_at: next.aired_at } : null, last_activity: lastAct,
    };
  }
  const ACTIVE = ['added', 'downloading', 'chosen'];
  /** SPEC-v1.1 9 autoAllowed, as a reason string: "" = auto applies or does not matter (file or active release). */
  function autoReason(s, e) {
    if (fileOf(s.id, e.number)) return '';
    const rs = releases.filter((r) => r.show_id === s.id && r.episode === e.number);
    if (rs.some((r) => ACTIVE.includes(r.status))) return '';
    if (e.auto === 0) {
      const last = rs.filter((r) => r.status !== 'candidate').sort((a, b) => b.id - a.id)[0];
      return last && last.status === 'failed' && /no longer in qBittorrent/.test(last.status_note || '') ? 'removed from qBittorrent' : 'turned off';
    }
    if (s.auto_from != null && e.number < s.auto_from) return `before episode ${s.auto_from}`;
    if (e.auto === 1) return '';
    if (e.aired_at == null) return 'no air date';
    const days = settings.conveyor.lookback_days;
    if (e.aired_at < now() - days * D) return `older than ${days} days`;
    return '';
  }
  /** Same selection as the server's download-missing: aired, no file, no active release, best candidate by seeders. */
  function missingPicks(s) {
    const t = now(); const picks = []; let skipped = 0;
    for (const e of episodes[s.id] || []) {
      if (e.aired_at == null || e.aired_at > t || fileOf(s.id, e.number)) continue;
      const rs = releases.filter((r) => r.show_id === s.id && r.episode === e.number);
      if (rs.some((r) => ACTIVE.includes(r.status))) continue;
      const best = rs.filter((r) => r.status === 'candidate').sort((a, b) => b.seeders - a.seeders)[0];
      if (best) picks.push(best); else skipped++;
    }
    return { picks, skipped };
  }
  function epRow(s, e) {
    const f = fileOf(s.id, e.number);
    const r = curRelease(s.id, e.number);
    return {
      auto: e.auto ?? null, auto_reason: autoReason(s, e),
      show_id: s.id, show_title: titleOf(s), cover_url: s.cover_url, banner_url: s.banner_url, color: s.color, number: e.number, title: e.title, aired_at: e.aired_at, dub_aired_at: e.dub_aired_at,
      watched_at: e.watched_at, watched_source: e.watched_source,
      file: f ? { id: f.id, path: f.path, name: f.name, size: f.size, release_group: f.release_group, resolution: f.resolution, version: f.version } : null,
      release: r ? { id: r.id, status: r.status, title: r.title, seeders: r.seeders } : null,
    };
  }
  /** SPEC-v1.1 11 GET /api/missing: watching shows only; aired, not watched, no present file, and no release
      that is added / downloading / done. Ordered by the show's last_activity, newest first. */
  const MISSING_ACTIVE = ['added', 'downloading', 'done'];
  function missingList() {
    const t = now(); const out = [];
    for (const s of shows.filter((x) => x.list_status === 'watching')) {
      const nums = [];
      for (const e of episodes[s.id] || []) {
        if (e.aired_at == null || e.aired_at > t || e.watched_at || fileOf(s.id, e.number)) continue;
        if (releases.some((r) => r.show_id === s.id && r.episode === e.number && MISSING_ACTIVE.includes(r.status))) continue;
        nums.push(e.number);
      }
      if (!nums.length) continue;
      const c = card(s);
      out.push({ show_id: s.id, title: c.title, cover_url: s.cover_url, color: s.color, count: nums.length, first_episode: nums[0], last_episode: nums[nums.length - 1], _act: c.last_activity });
    }
    return out.sort((a, b) => b._act - a._act).map(({ _act, ...x }) => x);
  }
  function showDetail(s) {
    const c = card(s);
    return {
      show: { ...c, mal_id: s.mal_id, title_native: s.title_native, synonyms: s.synonyms, season: s.season, season_year: s.season_year, duration_min: s.duration_min, search_query: s.search_query, episode_offset: s.episode_offset, group_order: s.group_order, resolution: s.resolution, auto_download: s.auto_download, folder: s.folder, auto_from: s.auto_from, added_at: s.added_at, updated_at: s.updated_at },
      episodes: (episodes[s.id] || []).map((e) => epRow(s, e)),
      releases: releases.filter((r) => r.show_id === s.id),
      files: files.filter((f) => f.show_id === s.id),
    };
  }
  function upnext() {
    const out = [];
    for (const s of shows.filter((x) => x.list_status === 'watching')) {
      for (const e of episodes[s.id] || []) if (!e.watched_at && fileOf(s.id, e.number)) out.push({ s, e, act: card(s).last_activity });
    }
    return out.sort((a, b) => b.act - a.act || a.e.number - b.e.number).map((x) => epRow(x.s, x.e));
  }
  function localWeek() {
    // Same week start as the UI: the browser locale's first day (Intl weekInfo), Monday when unknown.
    let fd = 1;
    try { const loc = new Intl.Locale(navigator.language || 'en'); const wi = typeof loc.getWeekInfo === 'function' ? loc.getWeekInfo() : loc.weekInfo; if (wi && wi.firstDay) fd = wi.firstDay % 7; } catch { /* default */ }
    const d = new Date(); d.setHours(0, 0, 0, 0);
    d.setDate(d.getDate() - ((d.getDay() - fd + 7) % 7));
    const from = Math.floor(d / 1000);
    return [from, from + W];
  }
  function state() {
    const [from, to] = localWeek();
    let airing = 0;
    for (const s of shows.filter((x) => x.list_status === 'watching')) airing += (episodes[s.id] || []).filter((e) => e.aired_at >= from && e.aired_at < to).length;
    const dd = downloadDir();
    const mode = settings.downloader.mode;
    const dl = { mode, ok: true, error: '' };
    if (mode === 'qbittorrent') { dl.ok = false; dl.error = `connection refused: ${settings.qbit.url}`; }
    if (mode === 'torrent_file') { dl.ok = exists(settings.downloader.torrent_dir); dl.error = dl.ok ? '' : `watch folder not reachable: ${settings.downloader.torrent_dir}`; }
    return {
      settings_ok: true,
      qbit: { enabled: mode === 'qbittorrent', connected: false },
      root_ok: !!dd && exists(dd),
      counts: { watching: shows.filter((s) => s.list_status === 'watching').length, on_disk_unwatched: upnext().length, airing_this_week: airing, missing: missingList().reduce((a, x) => a + x.count, 0) },
      now: now(),
      downloader: dl,
      download_dir: dd ? { path: dd, exists: exists(dd), free_bytes: exists(dd) ? freeOf(dd) : null } : null,
      folders_active: FL().filter((f) => f.active).length,
      exe_dir: EXE_DIR,
      data_dir: DATA_DIR,
      setup_done: !!settings.ui.setup_done,
    };
  }
  function calendar(from, to) {
    const out = [];
    for (const s of shows) {
      if (s.list_status === 'dropped') continue;
      for (const e of episodes[s.id] || []) {
        const inSub = e.aired_at >= from && e.aired_at < to;
        const inDub = e.dub_aired_at && e.dub_aired_at >= from && e.dub_aired_at < to;
        if (!inSub && !inDub) continue;
        const r = curRelease(s.id, e.number);
        out.push({ show_id: s.id, title: titleOf(s), episode: e.number, aired_at: e.aired_at, dub_aired_at: e.dub_aired_at, on_disk: !!fileOf(s.id, e.number), watched: !!e.watched_at, release_status: r ? r.status : null });
      }
    }
    return out;
  }
  const media = (m) => ({ ...m, tracked: shows.some((s) => s.anilist_id === m.anilist_id) });

  // ---------------------------------------------------------------- v1.1: fake drives, library folders
  const GB = 1024 ** 3;
  const DRIVES = { 'C:\\': { free: 212 * GB, fixed: true, system: true }, 'D:\\': { free: LOWDISK ? 1.8 * GB : 1460 * GB, fixed: true }, 'E:\\': { free: null, fixed: false, dead: true } };
  const EXE_DIR = 'C:\\Users\\You\\Apps\\abc';
  const DATA_DIR = 'C:\\Users\\You\\AppData\\Roaming\\abc';
  const TREE = {}; // lower-case path -> {path, kids: Set of child names}
  const norm = (p) => {
    let s = String(p || '').trim().replace(/\//g, '\\').replace(/\\{2,}/g, '\\');
    if (/^[a-z]:$/i.test(s)) s += '\\';
    if (s.length > 3) s = s.replace(/\\+$/, '');
    if (/^[a-z]:/.test(s)) s = s[0].toUpperCase() + s.slice(1);
    return s;
  };
  const key = (p) => norm(p).toLowerCase();
  const isAbs = (p) => /^[a-z]:\\/i.test(norm(p));
  const driveOf = (p) => (isAbs(p) ? norm(p).slice(0, 3) : '');
  const parentOf = (p) => { const n = norm(p); if (/^[A-Z]:\\$/.test(n)) return null; const i = n.lastIndexOf('\\'); return i <= 2 ? n.slice(0, 3) : n.slice(0, i); };
  function mkdir(p) {
    const n = norm(p);
    if (TREE[key(n)]) return;
    const par = parentOf(n);
    if (par) { mkdir(par); TREE[key(par)].kids.add(n.slice(n.lastIndexOf('\\') + 1)); }
    TREE[key(n)] = { path: n, kids: new Set() };
  }
  ['C:\\Users\\You\\Videos\\Captures', 'C:\\Users\\You\\Documents', 'C:\\Users\\You\\Desktop', 'C:\\Users\\You\\Downloads\\Torrents\\watch', DATA_DIR, EXE_DIR, 'C:\\Users\\Public',
    'C:\\Windows\\System32', 'C:\\Program Files\\qBittorrent', 'C:\\Program Files (x86)', 'C:\\ProgramData', 'C:\\$Recycle.Bin', 'C:\\PerfLogs',
    'D:\\Anime', 'D:\\Games\\Steam', 'D:\\System Volume Information', 'D:\\.trash', 'E:\\Old anime'].forEach(mkdir);
  // A show folder exists only once an episode was downloaded into it.
  DEFS.filter((d) => d.disk).forEach((d) => mkdir('D:\\Anime\\' + d.title_romaji.replace(/[[\]:]/g, '')));
  const reachable = (p) => { const dr = driveOf(p); return !!dr && !!DRIVES[dr] && !DRIVES[dr].dead && !(MISSING && dr === 'D:\\'); };
  const exists = (p) => reachable(p) && !!TREE[key(p)];
  const freeOf = (p) => (reachable(p) ? DRIVES[driveOf(p)].free : null);
  const HIDDEN_AT_ROOT = ['windows', 'program files', 'program files (x86)', 'programdata', 'recovery', 'perflogs'];
  const FL = () => settings.library.folders;
  const findF = (p) => FL().findIndex((f) => key(f.path) === key(p));
  const downloadDir = () => { const f = FL().find((x) => x.active && x.download); return f ? f.path : ''; };
  const under = (file, dir) => key(file).startsWith(key(dir).replace(/\\$/, '') + '\\');
  const safeName = (s) => String(s).replace(/[[\]:<>"/\\|?*]/g, '').trim();
  const showDir = (s) => (s.folder ? (isAbs(s.folder) ? norm(s.folder) : norm(downloadDir() + '\\' + s.folder)) : downloadDir() ? downloadDir() + '\\' + safeName(s.title_romaji) : '');
  /** Files show while they sit under an active folder. An unreachable folder keeps its files (scanner skips it). */
  function syncPresence() { for (const f of files) f.present = FL().some((x) => x.active && under(f.path, x.path)) ? 1 : 0; }
  // SPEC-v1.1 8: scan depth per folder. "" or unknown -> "shows".
  const SCANS = ['flat', 'shows', 'deep'];
  const normScan = (v) => (SCANS.includes(v) ? v : 'shows');
  const FLAT_DL = "the download folder needs at least 'show folders' because episodes are saved one level down";
  /** Same rule as the server's settings validation: null, or the library.folders message. */
  const folderRule = (folders, style) => ((folders || []).some((f) => f.active && f.download && normScan(f.scan) === 'flat') && style === 'show_folder' ? FLAT_DL : null);
  function folderStates() {
    return FL().map((f) => ({ ...clone(f), scan: normScan(f.scan), exists: exists(f.path), free_bytes: exists(f.path) ? freeOf(f.path) : null, file_count: files.filter((x) => x.present && under(x.path, f.path)).length }));
  }
  function fsList(path) {
    if (!path) return { status: 200, body: { path: '', parent: null, dirs: Object.keys(DRIVES).filter((d) => !(MISSING && d === 'D:\\')).map((d) => ({ name: d, path: d, free_bytes: freeOf(d) })) } };
    if (!isAbs(path)) return { status: 400, body: { error: 'path must be absolute' } };
    const n = norm(path);
    const base = { path: n, parent: parentOf(n), dirs: [] };
    if (!reachable(n)) return { status: 200, body: { ...base, error: 'access denied' } };
    const t = TREE[key(n)];
    if (!t) return { status: 200, body: { ...base, error: 'folder does not exist' } };
    const atRoot = /^[A-Z]:\\$/.test(n);
    const dirs = [...t.kids]
      .filter((nm) => !/^[.$]/.test(nm) && nm !== 'System Volume Information' && !(atRoot && HIDDEN_AT_ROOT.includes(nm.toLowerCase())))
      .sort((a, b) => a.localeCompare(b, undefined, { sensitivity: 'base' }))
      .map((nm) => ({ name: nm, path: (atRoot ? n : n + '\\') + nm }));
    return { status: 200, body: { ...base, dirs } };
  }
  function suggest() {
    const list = [
      { path: EXE_DIR + '\\Anime', label: 'Next to abc.exe' },
      { path: 'C:\\Users\\You\\Videos\\Anime', label: 'Your Videos folder' },
      ...Object.keys(DRIVES).filter((d) => DRIVES[d].fixed && !DRIVES[d].system && reachable(d)).map((d) => ({ path: d + 'Anime', label: `Drive ${d.slice(0, 2)}` })),
      { path: 'C:\\Anime', label: 'Drive C:' },
    ];
    const seen = new Set();
    return list.filter((s) => !seen.has(key(s.path)) && seen.add(key(s.path))).map((s) => ({ ...s, exists: exists(s.path), free_bytes: freeOf(s.path) }));
  }
  const fieldErr = (k, m) => ({ status: 400, body: { error: m, fields: { [k]: m } } });
  function addFolder(b) {
    b = b || {};
    const p = norm(b.path);
    if (!p || !isAbs(p)) return fieldErr('path', 'paths must be absolute');
    if (!exists(p)) {
      if (!b.create) return fieldErr('path', 'folder does not exist');
      if (!reachable(p)) return fieldErr('path', 'cannot create the folder: the drive is not reachable');
      mkdir(p);
    }
    const firstActive = !FL().some((f) => f.active);
    let i = findF(p);
    // Try the change on a copy first so a rejected add leaves the list untouched.
    const next = clone(FL());
    if (i < 0) { next.push({ path: p, label: '', download: false, active: true, added_at: now(), scan: 'shows' }); i = next.length - 1; }
    const f = next[i];
    f.active = true;
    if (b.label != null) f.label = String(b.label).trim();
    if (b.scan != null) f.scan = normScan(b.scan);
    // First active folder gets the download flag unless the body says download:false (same as internal/api/folders.go).
    if (b.download === true || (firstActive && b.download !== false)) { for (const o of next) o.download = false; f.download = true; }
    const rule = folderRule(next, settings.qbit.save_path_style);
    if (rule) return fieldErr('library.folders', rule);
    settings.library.folders = next;
    syncPresence();
    setTimeout(() => emit('scan.done', null, null, `Scan finished: ${f.label || p} added to the library`), 600);
    return { status: 200, body: folderStates() };
  }
  function patchFolder(b) {
    b = b || {};
    const i = findF(b.path);
    if (i < 0) return { status: 404, body: { error: 'unknown folder' } };
    if (b.scan != null && !SCANS.includes(b.scan)) return fieldErr('scan', 'must be "flat", "shows" or "deep"');
    const next = clone(FL());
    const f = next[i];
    if (b.download === true && !(f.active || b.active === true)) return { status: 400, body: { error: 'activate the folder first' } };
    const before = files.filter((x) => x.present).length;
    if (b.active === false) { f.active = false; f.download = false; }
    if (b.active === true) f.active = true;
    if (b.download === true) { for (const o of next) o.download = false; f.download = true; }
    if (b.download === false) f.download = false;
    if (b.label != null) f.label = String(b.label).trim();
    if (b.scan != null) f.scan = b.scan;
    const rule = folderRule(next, settings.qbit.save_path_style);
    if (rule) return fieldErr('library.folders', rule);
    settings.library.folders = next;
    if (b.scan != null) setTimeout(() => emit('scan.done', null, null, `Scan finished: ${f.label || f.path} rescanned (${b.scan === 'flat' ? 'this folder only' : b.scan === 'deep' ? 'all subfolders' : 'show folders'})`), 600);
    syncPresence();
    const hidden = before - files.filter((x) => x.present).length;
    if (b.active === false) emit('scan.done', null, null, `Folder ${f.label || f.path} removed from the library: ${hidden} files hidden`, { path: f.path, hidden });
    return { status: 200, body: folderStates() };
  }
  function openAllowed(p) {
    const k = key(p);
    // exe_dir is allowed for Settings > Server "Open ABC's folder" (internal/api/folders.go openAllowed does the same).
    return FL().some((f) => key(f.path) === k) || (!!settings.downloader.torrent_dir && key(settings.downloader.torrent_dir) === k)
      || shows.some((s) => showDir(s) && key(showDir(s)) === k) || k === key(DATA_DIR) || k === key(EXE_DIR);
  }

  // ---------------------------------------------------------------- live events
  const sources = [];
  function emit(kind, showId, ep, message, data = {}) {
    const e = { id: ++evId, ts: now(), kind, show_id: showId, episode: ep, message, data };
    events.push(e);
    for (const es of sources) es._emit(kind, e);
    return e;
  }
  if (LIVE && !FRESH) {
    setTimeout(() => {
      const r = addRelease(3, 6, 'SubsPlease', '1080p', 820, 'candidate', { after: 2 * H + 15 * 60 });
      r.published_at = now(); r.first_seen = now();
      emit('release.found', 3, 6, 'Found [SubsPlease] Dandadan - 06 (1080p)');
      setTimeout(() => {
        r.status = 'chosen'; r.status_note = 'qBittorrent unreachable: magnet link ready';
        emit('release.added', 3, 6, 'Chose [SubsPlease] Dandadan - 06 (1080p). qBittorrent unreachable: magnet link ready');
      }, 3000);
    }, 9000);
    setTimeout(() => emit('scan.done', null, null, 'Scan finished: 1,214 files, 0 new'), 30000);
  }
  class MockEventSource {
    constructor(url) {
      this.url = String(url); this.readyState = 0; this.withCredentials = false;
      this.onopen = null; this.onmessage = null; this.onerror = null; this._l = {};
      sources.push(this);
      setTimeout(() => { this.readyState = 1; if (this.onopen) this.onopen({ type: 'open' }); }, 120);
      this._ping = setInterval(() => this._emit('ping', { ts: now() }), 25000);
    }
    addEventListener(t, fn) { (this._l[t] ||= []).push(fn); }
    removeEventListener(t, fn) { this._l[t] = (this._l[t] || []).filter((x) => x !== fn); }
    close() { this.readyState = 2; clearInterval(this._ping); }
    _emit(type, data) {
      if (this.readyState !== 1) return;
      const e = { type, data: JSON.stringify(data), lastEventId: data && data.id ? String(data.id) : '' };
      for (const fn of this._l[type] || []) fn(e);
      if (type === 'message' && this.onmessage) this.onmessage(e);
    }
  }
  MockEventSource.CONNECTING = 0; MockEventSource.OPEN = 1; MockEventSource.CLOSED = 2;
  const RealES = window.EventSource;
  function PatchedES(url, opts) { return /^\/api\//.test(String(url)) ? new MockEventSource(url) : new RealES(url, opts); }
  PatchedES.CONNECTING = 0; PatchedES.OPEN = 1; PatchedES.CLOSED = 2;
  window.EventSource = PatchedES;

  // ---------------------------------------------------------------- routes
  const err = (status, message) => ({ status, body: { error: message } });
  const okb = (body) => ({ status: 200, body });
  const showById = (id) => shows.find((s) => s.id === Number(id));
  /** Same shape as the server: null, or 400 body {error, fields: {"group.field": "message"}} (SPEC-v1.1 1). */
  function validateSettings(s) {
    const fields = {};
    const n = (v) => typeof v === 'number' && Number.isFinite(v);
    if (!n(s.server?.port) || s.server.port < 1024 || s.server.port > 65535) fields['server.port'] = 'must be 1024-65535';
    if (!n(s.watched?.fraction) || s.watched.fraction < 0.05 || s.watched.fraction > 1) fields['watched.fraction'] = 'must be 0.05-1';
    for (const p of [['library', 'scan_interval_min'], ['conveyor', 'poll_interval_min'], ['watched', 'poll_sec'], ['watched', 'default_runtime_min'], ['conveyor', 'lookback_days']]) {
      if (!n(s[p[0]]?.[p[1]]) || s[p[0]][p[1]] < 1) fields[`${p[0]}.${p[1]}`] = 'must be at least 1';
    }
    if (!n(s.conveyor?.max_release_gib) || s.conveyor.max_release_gib < 0) fields['conveyor.max_release_gib'] = 'must be 0 or more';
    if (!n(s.conveyor?.min_free_gib) || s.conveyor.min_free_gib < 0) fields['conveyor.min_free_gib'] = 'must be 0 or more';
    if (!['small', 'any'].includes(s.conveyor?.size_preference)) fields['conveyor.size_preference'] = 'must be "small" or "any"';
    const mode = s.downloader?.mode;
    if (!['qbittorrent', 'magnet', 'torrent_file', 'manual'].includes(mode)) fields['downloader.mode'] = 'must be "qbittorrent", "magnet", "torrent_file" or "manual"';
    const td = s.downloader?.torrent_dir || '';
    if (mode === 'torrent_file' && !td) fields['downloader.torrent_dir'] = 'required for the watch folder mode';
    else if (td && !isAbs(td)) fields['downloader.torrent_dir'] = 'paths must be absolute';
    else if (mode === 'torrent_file' && !exists(td)) fields['downloader.torrent_dir'] = 'folder does not exist';
    if (mode === 'qbittorrent' || s.qbit?.url) { try { new URL(s.qbit.url); } catch { fields['qbit.url'] = 'is not a valid URL'; } }
    try { new URL(s.conveyor?.nyaa_base); } catch { fields['conveyor.nyaa_base'] = 'is not a valid URL'; }
    if ((s.library?.folders || []).some((f) => !isAbs(f.path))) fields['library.folders'] = 'paths must be absolute';
    else if (folderRule(s.library?.folders, s.qbit?.save_path_style)) fields['library.folders'] = FLAT_DL;
    const keys = Object.keys(fields);
    return keys.length ? { error: keys.map((k) => `${k}: ${fields[k]}`).join('; '), fields } : null;
  }
  /** SPEC-v1.1 2: hand a release to the downloader by mode (used by releases/{id}/add and download-missing). */
  function handOff(r) {
    for (const o of releases) if (o !== r && o.show_id === r.show_id && o.episode === r.episode && ACTIVE.includes(o.status)) o.status = 'candidate';
    const mode = settings.downloader.mode; const td = settings.downloader.torrent_dir;
    const s = showById(r.show_id);
    const label = `${s ? titleOf(s) : 'Show'} ${r.episode}`;
    let status = 'chosen'; let note = 'add it manually'; let msg = `${label}: chosen, add it manually (${r.title})`;
    if (mode === 'qbittorrent') {
      note = downloadDir() && !exists(downloadDir()) ? `download folder not reachable: ${downloadDir()}` : 'qBittorrent unreachable: magnet link ready';
      msg = `${label}: chosen, ${note} (${r.title})`;
    } else if (mode === 'magnet') { status = 'added'; note = 'sent to the magnet link handler'; msg = `${label}: sent to your torrent app (magnet link) (${r.title})`; }
    else if (mode === 'torrent_file') {
      if (exists(td)) { status = 'added'; note = `saved ${safeName(r.title)}.torrent to the watch folder`; msg = `${label}: .torrent saved to the watch folder (${r.title})`; }
      else { note = `watch folder not reachable: ${td}`; msg = `${label}: chosen, ${note} (${r.title})`; }
    }
    r.status = status; r.status_note = note;
    emit('release.added', r.show_id, r.episode, msg, { release_id: r.id, title: r.title, manual: status === 'chosen', magnet: r.magnet, note, mode });
  }
  const ROUTES = [
    ['GET', /^\/api\/state$/, () => okb(state())],
    ['GET', /^\/api\/settings$/, () => okb(clone(settings))],
    ['PUT', /^\/api\/settings$/, (m, b) => {
      const e = validateSettings(b || {});
      if (e) return { status: 400, body: e };
      for (const k of Object.keys(settings)) delete settings[k];
      Object.assign(settings, clone(b));
      settings.library.folders = (settings.library.folders || []).map((f) => ({ ...f, path: norm(f.path), scan: normScan(f.scan) }));
      syncPresence();
      return okb(clone(settings));
    }],
    ['GET', /^\/api\/settings\/test\/qbit$/, () => okb({ ok: false, error: `connection refused: ${settings.qbit.url}` })],
    ['GET', /^\/api\/settings\/test\/magnet$/, () => okb({ ok: true, handler: '"C:\\Program Files\\qBittorrent\\qbittorrent.exe" "%1"' })],
    ['GET', /^\/api\/settings\/test\/anilist$/, () => okb({ ok: true })],
    ['GET', /^\/api\/folders$/, () => okb(folderStates())],
    ['POST', /^\/api\/folders$/, (m, b) => addFolder(b)],
    ['PATCH', /^\/api\/folders$/, (m, b) => patchFolder(b)],
    ['DELETE', /^\/api\/folders$/, (m, b, u) => {
      const i = findF(u.searchParams.get('path'));
      if (i < 0) return err(404, 'unknown folder');
      FL().splice(i, 1);
      syncPresence();
      return okb(folderStates());
    }],
    ['GET', /^\/api\/fs\/list$/, (m, b, u) => fsList(u.searchParams.get('path') || '')],
    ['GET', /^\/api\/fs\/suggest$/, () => okb(suggest())],
    ['POST', /^\/api\/folders\/open$/, (m, b) => {
      const p = b && b.path;
      if (!p || !openAllowed(p)) return err(403, 'ABC only opens its own folders');
      return exists(p) ? okb({ ok: true }) : err(409, 'folder does not exist');
    }],
    ['POST', /^\/api\/shows\/(\d+)\/open-folder$/, (m) => {
      const s = showById(m[1]);
      if (!s) return err(404, 'Show not found');
      const dir = showDir(s);
      if (!dir) return err(409, 'no download folder set');
      return exists(dir) ? okb({ ok: true }) : err(409, 'no episode downloaded yet, the folder does not exist');
    }],
    ['GET', /^\/api\/shows$/, (m, b, u) => { const st = u.searchParams.get('status'); return okb(shows.filter((s) => !st || s.list_status === st).map(card)); }],
    ['POST', /^\/api\/shows$/, (m, b) => {
      const aid = Number(b && b.anilist_id);
      if (shows.some((s) => s.anilist_id === aid)) return err(409, 'Already tracked');
      const p = POOL.find((x) => x.anilist_id === aid);
      if (!p) return err(404, `AniList id ${aid} not found`);
      const id = ++showSeq;
      buildShow({ id, anilist_id: aid, mal_id: null, title_romaji: p.title_romaji, title_english: p.title_english, title_native: null, synonyms: [], cover_url: p.cover_url, banner_url: p.banner_url, color: p.color, format: p.format, status: p.status, season: p.season, season_year: p.season_year,
        episodes_total: p.episodes, duration_min: 24, list_status: (b && b.list_status) || 'watching', score: null, group: 'SubsPlease', aired: p.status === 'RELEASING' ? 3 : (p.episodes || 12), lastAt: r30(now() - 3 * D), disk: null, watched: null, activity: 0 });
      const s = showById(id);
      emit('show.added', id, null, `Added ${titleOf(s)}`);
      return okb(card(s));
    }],
    ['GET', /^\/api\/shows\/(\d+)$/, (m) => {
      const s = showById(m[1]);
      if (!s) return err(404, 'Show not found');
      return okb(showDetail(s));
    }],
    // SPEC-v1.1 11: mark 1..number watched (never unmarks), one push, one event; returns ShowDetail.
    ['POST', /^\/api\/shows\/(\d+)\/watched-through$/, (m, b) => {
      const s = showById(m[1]);
      if (!s) return err(404, 'Show not found');
      const n = b && b.number;
      const max = s.episodes_total ? s.episodes_total + 50 : Infinity;
      if (!Number.isInteger(n) || n < 1 || n > max) return err(400, s.episodes_total ? `number must be between 1 and ${max}` : 'number must be 1 or more');
      let newly = 0; const t = now();
      for (const e of episodes[s.id] || []) if (e.number <= n && !e.watched_at) { e.watched_at = t; e.watched_source = 'manual'; newly++; }
      s._activity = t;
      emit('episode.watched', s.id, null, `${titleOf(s)}: episodes 1-${n} marked watched (${newly} new)`, { through: n, newly, bulk: true });
      return okb(showDetail(s));
    }],
    ['GET', /^\/api\/missing$/, () => okb(missingList())],
    ['PATCH', /^\/api\/shows\/(\d+)$/, (m, b) => {
      const s = showById(m[1]);
      if (!s) return err(404, 'Show not found');
      for (const k of ['list_status', 'search_query', 'episode_offset', 'group_order', 'resolution', 'auto_download', 'folder', 'score', 'auto_from']) if (b && k in b) s[k] = b[k];
      s.updated_at = now();
      return okb(card(s));
    }],
    ['DELETE', /^\/api\/shows\/(\d+)$/, (m) => {
      const i = shows.findIndex((s) => s.id === Number(m[1]));
      if (i < 0) return err(404, 'Show not found');
      const [s] = shows.splice(i, 1);
      emit('show.removed', null, null, `Removed ${titleOf(s)}`);
      return okb({ ok: true });
    }],
    ['POST', /^\/api\/shows\/(\d+)\/episodes\/(\d+)\/watched$/, (m, b) => {
      const s = showById(m[1]);
      const e = s && (episodes[s.id] || []).find((x) => x.number === Number(m[2]));
      if (!e) return err(404, 'Episode not found');
      const w = !!(b && b.watched);
      e.watched_at = w ? now() : null; e.watched_source = w ? 'manual' : null;
      s._activity = now();
      emit(w ? 'episode.watched' : 'episode.unwatched', s.id, e.number, `${titleOf(s)} ${e.number} ${w ? 'marked watched' : 'marked unwatched'}`);
      return okb(epRow(s, e));
    }],
    ['POST', /^\/api\/shows\/(\d+)\/refresh$/, (m) => {
      const s = showById(m[1]);
      if (!s) return err(404, 'Show not found');
      setTimeout(() => emit('airing.updated', s.id, null, `${titleOf(s)}: airing schedule checked, no change`), 900);
      return okb({ started: true });
    }],
    ['GET', /^\/api\/search$/, (m, b, u) => {
      const qq = (u.searchParams.get('q') || '').toLowerCase().trim();
      if (!qq) return okb([]);
      return okb(POOL.filter((x) => [x.title_romaji, x.title_english].some((t) => t && t.toLowerCase().includes(qq))).slice(0, 10).map(media));
    }],
    ['GET', /^\/api\/season$/, (m, b, u) => {
      const se = u.searchParams.get('season'); const yr = Number(u.searchParams.get('year'));
      return okb(POOL.filter((x) => x.season === se && x.season_year === yr).map(media));
    }],
    ['GET', /^\/api\/upnext$/, () => okb(upnext())],
    ['GET', /^\/api\/calendar$/, (m, b, u) => okb(calendar(Number(u.searchParams.get('from')) || 0, Number(u.searchParams.get('to')) || now() + W))],
    ['GET', /^\/api\/events$/, (m, b, u) => {
      const after = Number(u.searchParams.get('after')) || 0;
      const limit = Number(u.searchParams.get('limit')) || 100;
      const list = events.filter((e) => e.id > after);
      return okb(u.searchParams.has('after') ? list.slice(0, limit) : list.slice(-limit));
    }],
    ['GET', /^\/api\/releases$/, (m, b, u) => {
      const sid = Number(u.searchParams.get('show_id')); const ep = u.searchParams.get('episode');
      return okb(releases.filter((r) => (!sid || r.show_id === sid) && (ep == null || ep === '' || r.episode === Number(ep))));
    }],
    ['POST', /^\/api\/releases\/(\d+)\/add$/, (m) => {
      const r = releases.find((x) => x.id === Number(m[1]));
      if (!r) return err(404, 'Release not found');
      handOff(r);
      const s = showById(r.show_id);
      return okb({ ...r, show_title: s ? titleOf(s) : '' });
    }],
    // SPEC-v1.1 9: per-episode auto (null = rule, 0 = never, 1 = always)
    ['POST', /^\/api\/shows\/(\d+)\/episodes\/(\d+)\/auto$/, (m, b) => {
      const s = showById(m[1]);
      const e = s && (episodes[s.id] || []).find((x) => x.number === Number(m[2]));
      if (!e) return err(404, 'Episode not found');
      const v = b ? b.auto : undefined;
      if (!(v === null || v === 0 || v === 1)) return err(400, 'auto must be null, 0 or 1');
      e.auto = v;
      return okb(epRow(s, e));
    }],
    ['GET', /^\/api\/shows\/(\d+)\/download-missing$/, (m) => {
      const s = showById(m[1]);
      if (!s) return err(404, 'Show not found');
      const { picks } = missingPicks(s);
      return okb({ count: picks.length, bytes: picks.reduce((a, r) => a + (r.size_bytes || 0), 0) });
    }],
    ['POST', /^\/api\/shows\/(\d+)\/download-missing$/, (m) => {
      const s = showById(m[1]);
      if (!s) return err(404, 'Show not found');
      const { picks, skipped } = missingPicks(s);
      for (const r of picks) handOff(r);
      emit('release.added', s.id, null, `${titleOf(s)}: Download all missing: ${picks.length} added, ${skipped} without a release`, { bulk: true });
      return okb({ added: picks.length, skipped });
    }],
    ['POST', /^\/api\/releases\/(\d+)\/skip$/, (m) => {
      const r = releases.find((x) => x.id === Number(m[1]));
      if (!r) return err(404, 'Release not found');
      r.status = 'skipped'; r.status_note = 'skipped by you';
      emit('release.skipped', r.show_id, r.episode, `Skipped ${r.title}`);
      return okb(r);
    }],
    ['POST', /^\/api\/conveyor\/run$/, () => {
      setTimeout(() => {
        const waiting = !releases.some((r) => r.show_id === 3 && r.episode === 6 && r.status === 'chosen');
        if (shows.length && waiting) emit('release.waiting', 3, 6, 'Dandadan - 06: waiting for SubsPlease. Erai-raws is out; fallback after 10 h');
        else emit('scan.done', null, null, `Conveyor checked ${shows.filter((s) => s.list_status === 'watching').length} shows, nothing new`);
      }, 1200);
      return okb({ started: true });
    }],
    ['POST', /^\/api\/scan\/run$/, () => { setTimeout(() => emit('scan.done', null, null, `Scan finished: ${files.length.toLocaleString()} files, 0 new`), 1500); return okb({ started: true }); }],
    ['POST', /^\/api\/files\/(\d+)\/open$/, (m) => (files.some((f) => f.id === Number(m[1])) ? okb({ ok: true }) : err(404, 'File not found'))],
    ['POST', /^\/api\/files\/(\d+)\/reveal$/, (m) => (files.some((f) => f.id === Number(m[1])) ? okb({ ok: true }) : err(404, 'File not found'))],
    ['GET', /^\/api\/auth\/(anilist|mal)\/start$/, (m) => {
      if (!accounts.some((a) => a.service === m[1])) accounts.push({ service: m[1], user_name: 'abc-demo', push_enabled: false });
      return okb({ url: '#/settings?section=push' }); // the real server returns the provider's OAuth page
    }],
    ['POST', /^\/api\/import\/anilist$/, (m, b) => (b && b.user_name ? okb({ added: 2, updated: 4 }) : err(400, 'user_name is required'))],
    ['POST', /^\/api\/import\/mal$/, () => (accounts.some((a) => a.service === 'mal') ? okb({ added: 1, updated: 5 }) : err(401, 'MyAnimeList is not connected. Connect it in Settings first.'))],
    ['GET', /^\/api\/accounts$/, () => okb(clone(accounts))],
    ['DELETE', /^\/api\/accounts\/(anilist|mal)$/, (m) => { const i = accounts.findIndex((a) => a.service === m[1]); if (i >= 0) accounts.splice(i, 1); return okb({ ok: true }); }],
  ];

  const realFetch = window.fetch ? window.fetch.bind(window) : null;
  window.fetch = async (input, init = {}) => {
    const raw = typeof input === 'string' ? input : input && input.url;
    if (!/^\/api\//.test(String(raw))) return realFetch(input, init);
    const u = new URL(raw, 'http://mock.local');
    const method = String(init.method || 'GET').toUpperCase();
    let body = null;
    if (init.body) { try { body = JSON.parse(init.body); } catch { body = null; } }
    await new Promise((r) => setTimeout(r, 60 + Math.random() * 120));
    let res = null;
    for (const [mth, re, fn] of ROUTES) {
      if (mth !== method) continue;
      const m = u.pathname.match(re);
      if (m) { res = fn(m, body, u); break; }
    }
    if (!res) res = err(404, `mock: no route for ${method} ${u.pathname}`);
    return new Response(JSON.stringify(res.body), { status: res.status, headers: { 'Content-Type': 'application/json' } });
  };
  window.__ABC_MOCK__ = { settings, shows, episodes, files, releases, events, emit, get folders() { return settings.library.folders; }, exists, downloadDir };
  console.info('[ABC] mock backend active', [FRESH && 'fresh', SETUP && 'setup', MISSING && 'missing', LOWDISK && 'lowdisk', NOFILES && 'nofiles', MANY && 'many'].filter(Boolean).join(' '));
})();

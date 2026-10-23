/* AnimeBeltConveyor web UI. Plain JS, no framework, no build step, no external requests.
   API contract: SPEC.md section 8. Settings shape: SPEC.md section 3. Pages: SPEC.md section 9.
   Loads web/mock.js first when the URL has ?mock=1 (fake fetch + EventSource, no server needed). */
'use strict';
(() => {
  // ------------------------------------------------------------------ utils
  const $ = (s, r = document) => r.querySelector(s);
  const $$ = (s, r = document) => Array.from(r.querySelectorAll(s));
  const ESC = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };
  const esc = (v) => String(v ?? '').replace(/[&<>"']/g, (c) => ESC[c]);
  class Raw { constructor(s) { this.s = s; } toString() { return this.s; } }
  const raw = (s) => new Raw(String(s));
  const val = (v) => (v == null || v === false ? '' : v instanceof Raw ? v.s : Array.isArray(v) ? v.map(val).join('') : esc(v));
  /** Tagged template: interpolated values are HTML-escaped unless wrapped in raw() / produced by h``. */
  function h(strings, ...vals) {
    let out = strings[0];
    for (let i = 0; i < vals.length; i++) out += val(vals[i]) + strings[i + 1];
    return new Raw(out);
  }
  const clamp = (n, a, b) => Math.min(b, Math.max(a, n));
  const nowSec = () => Math.floor(Date.now() / 1000);
  const isHex = (c) => typeof c === 'string' && /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.test(c.trim());
  const debounce = (fn, ms) => { let t; return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); }; };
  const baseName = (p) => String(p || '').split(/[\\/]/).pop();
  const plural = (n, one, many) => `${n} ${n === 1 ? one : many || one + 's'}`;
  /** SPEC-v1.3 1.5: "3 (173)" when the episode has an absolute number, else "3". */
  const epNum = (n, abs) => (abs != null && abs !== '' ? `${n} (${abs})` : String(n));
  /** The show's effective offset (manual episode_offset first, else AniList's abs_offset); 0 = none. */
  const effOffset = (s) => (s && Number(s.episode_offset)) || (s && Number(s.abs_offset)) || 0;
  function safeUrl(u) {
    if (typeof u !== 'string') return '';
    u = u.trim();
    if (!u) return '';
    if (/^https?:\/\//i.test(u) || /^\/(?!\/)/.test(u) || /^data:image\//i.test(u)) return u;
    return '';
  }
  const cssUrl = (u) => { u = safeUrl(u); return u ? `url("${u.replace(/["\\\n\r]/g, (c) => encodeURIComponent(c))}")` : ''; };
  const safeMagnet = (m) => (typeof m === 'string' && /^magnet:\?/i.test(m) ? m : '');
  const getPath = (o, p) => p.split('.').reduce((a, k) => (a == null ? undefined : a[k]), o);
  function setPath(o, p, v) {
    const ks = p.split('.');
    let cur = o;
    for (let i = 0; i < ks.length - 1; i++) {
      if (cur[ks[i]] == null || typeof cur[ks[i]] !== 'object') cur[ks[i]] = {};
      cur = cur[ks[i]];
    }
    cur[ks[ks.length - 1]] = v;
  }
  const clone = (o) => JSON.parse(JSON.stringify(o ?? null));
  /** Selectors that find the focused element again after its region is re-rendered (null when focus is elsewhere).
      An item inside a ⋯ menu falls back to that menu's button, since the menu is closed after a repaint. */
  function focusKey(regions) {
    const a = document.activeElement;
    if (!a || a === document.body || !regions.some((r) => r && r.contains(a))) return null;
    const out = [];
    if (a.id) out.push('#' + CSS.escape(a.id));
    if (a.dataset.act) out.push(`[data-act="${a.dataset.act}"]${a.dataset.ep ? `[data-ep="${a.dataset.ep}"]` : ''}${a.dataset.file ? `[data-file="${a.dataset.file}"]` : ''}`);
    if (a.dataset.menu) out.push(`[data-menu="${a.dataset.menu}"]`);
    if (a.dataset.pickToggle) out.push(`[data-pick-toggle="${a.dataset.pickToggle}"]`);
    const pop = a.closest('.menu-pop');
    if (pop && pop.id) out.push(`[data-menu="${pop.id}"]`);
    return out.length ? out : null;
  }
  /** Focus the first visible element matching any selector, in order. */
  const focusFirst = (...sels) => { for (const sel of sels) { const el = $$(sel).find((x) => x.getClientRects().length > 0); if (el) { el.focus(); return el; } } return null; };
  const store = {
    get(k) { try { return JSON.parse(localStorage.getItem('abc.' + k)); } catch { return null; } },
    set(k, v) { try { localStorage.setItem('abc.' + k, JSON.stringify(v)); } catch { /* storage off */ } },
  };

  // ------------------------------------------------------------------ time + number formatting (local time)
  const F = {
    time: new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' }),
    wd: new Intl.DateTimeFormat(undefined, { weekday: 'short' }),
    day: new Intl.DateTimeFormat(undefined, { day: 'numeric' }),
    md: new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric' }),
    mdy: new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric', year: 'numeric' }),
    date: new Intl.DateTimeFormat(undefined, { weekday: 'short', month: 'short', day: 'numeric' }),
    full: new Intl.DateTimeFormat(undefined, { dateStyle: 'full', timeStyle: 'short' }),
    rel: new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' }),
    num: new Intl.NumberFormat(undefined),
  };
  const toDate = (ts) => new Date(ts * 1000);
  const fTime = (ts) => (ts ? F.time.format(toDate(ts)) : '');
  const fFull = (ts) => (ts ? F.full.format(toDate(ts)) : '');
  const fNum = (n) => (n == null ? '?' : F.num.format(n));
  const cap = (s) => (s ? s[0].toUpperCase() + s.slice(1) : s);
  function fRel(ts) {
    if (!ts) return '';
    const diff = ts - nowSec();
    const a = Math.abs(diff);
    if (a < 45) return diff >= 0 ? 'in a moment' : 'just now';
    const table = [[3600, 60, 'minute'], [86400, 3600, 'hour'], [86400 * 7, 86400, 'day'], [86400 * 30, 86400 * 7, 'week'], [86400 * 365, 86400 * 30, 'month'], [Infinity, 86400 * 365, 'year']];
    for (const [lim, div, unit] of table) if (a < lim) return F.rel.format(Math.round(diff / div), unit);
    return '';
  }
  const dayKey = (dt) => `${dt.getFullYear()}-${dt.getMonth() + 1}-${dt.getDate()}`;
  function dayDiff(ts) {
    const a = new Date(); a.setHours(0, 0, 0, 0);
    const b = toDate(ts); b.setHours(0, 0, 0, 0);
    return Math.round((b - a) / 86400000);
  }
  /** "Today 23:00", "Tomorrow 18:30", "Fri 23:00", "Oct 30, 23:00" */
  function fWhen(ts) {
    if (!ts) return '';
    const dd = dayDiff(ts);
    const t = fTime(ts);
    if (Math.abs(dd) <= 1) return `${cap(F.rel.format(dd, 'day'))} ${t}`;
    if (Math.abs(dd) < 7) return `${F.wd.format(toDate(ts))} ${t}`;
    return `${F.md.format(toDate(ts))}, ${t}`;
  }
  /** Episode table "Aired": "Yesterday · 11:26 PM", "Thu · 11:26 PM" within 7 days, else "Jul 2 · 11:26 PM". */
  function fAired(ts) {
    if (!ts) return '';
    const dd = dayDiff(ts);
    const t = fTime(ts);
    if (Math.abs(dd) <= 1) return `${cap(F.rel.format(dd, 'day'))} · ${t}`;
    // Weekday only for the past week. A future episode gets its date: a past "Tue" and
    // a coming "Tue" in the same table read as the same day otherwise.
    if (dd < 0 && dd > -7) return `${F.wd.format(toDate(ts))} · ${t}`;
    return `${F.md.format(toDate(ts))} · ${t}`;
  }
  function fSize(b) {
    if (b == null) return '';
    const u = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
    let i = 0; let n = b;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return `${n.toFixed(n >= 100 || i === 0 ? 0 : 1)} ${u[i]}`;
  }
  /** First day of the week from the browser locale (Intl weekInfo: 1 = Monday ... 7 = Sunday); Monday when unknown. */
  function firstDayOfWeek() {
    try {
      const loc = new Intl.Locale(navigator.language || 'en');
      const wi = typeof loc.getWeekInfo === 'function' ? loc.getWeekInfo() : loc.weekInfo;
      if (wi && wi.firstDay) return wi.firstDay % 7;
    } catch { /* old browser */ }
    return 1;
  }
  function weekStart(offset = 0) {
    const d = new Date(); d.setHours(0, 0, 0, 0);
    d.setDate(d.getDate() - ((d.getDay() - firstDayOfWeek() + 7) % 7) + offset * 7);
    return d;
  }
  function weekDays(start) {
    return Array.from({ length: 7 }, (_, i) => { const d = new Date(start); d.setDate(start.getDate() + i); return d; });
  }
  const SEASONS = ['WINTER', 'SPRING', 'SUMMER', 'FALL'];
  const seasonOf = (d = new Date()) => ({ season: SEASONS[Math.floor(d.getMonth() / 3)], year: d.getFullYear() });
  const pretty = (s) => (s ? cap(String(s).toLowerCase().replace(/_/g, ' ')) : '');
  const FORMAT_LABEL = { TV: 'TV', TV_SHORT: 'TV short', MOVIE: 'Movie', OVA: 'OVA', ONA: 'ONA', SPECIAL: 'Special' };
  const STATUS_LABEL = { RELEASING: 'Airing', FINISHED: 'Finished', NOT_YET_RELEASED: 'Not yet aired', CANCELLED: 'Cancelled', HIATUS: 'On hiatus' };

  // ------------------------------------------------------------------ app state
  const S = {
    settings: null,
    settingsDraft: null,
    state: null,
    showsById: {},
    events: [],
    eventIds: new Set(),
    lastEventId: 0,
    last: store.get('last'), // {id, banner_url, cover_url, color}: the show touched last
    quit: false, // Settings > Quit ABC was confirmed and the server answered
  };
  const FALLBACK_ACCENT = '#8b9cff';
  const OS_WIN = /win/i.test((navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || '');
  const motionOK = () => !matchMedia('(prefers-reduced-motion: reduce)').matches && !(S.settings && S.settings.ui && S.settings.ui.reduced_motion);

  // ------------------------------------------------------------------ API (SPEC section 8)
  class ApiError extends Error { constructor(msg, status, body) { super(msg); this.status = status; this.body = body; } }
  async function api(method, path, body) {
    if (S.quit) return new Promise(() => {}); // ABC has quit: no more requests, no error toasts
    const opt = { method, headers: { Accept: 'application/json' } };
    if (body !== undefined) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
    let res;
    try { res = await fetch('/api' + path, opt); } catch { throw new ApiError('Cannot reach the ABC server.', 0); }
    const text = await res.text();
    let data = null;
    if (text) { try { data = JSON.parse(text); } catch { data = text; } }
    if (!res.ok) throw new ApiError((data && data.error) || `Request failed (HTTP ${res.status})`, res.status, data);
    return data;
  }
  const q = (o) => { const p = new URLSearchParams(); for (const [k, v] of Object.entries(o)) if (v != null && v !== '') p.set(k, v); const s = p.toString(); return s ? '?' + s : ''; };
  const API = {
    state: () => api('GET', '/state'),
    settings: () => api('GET', '/settings'),
    saveSettings: (s) => api('PUT', '/settings', s),
    testQbit: () => api('GET', '/settings/test/qbit'),
    testAniList: () => api('GET', '/settings/test/anilist'),
    shows: (status) => api('GET', '/shows' + q({ status })),
    addShow: (anilistId, listStatus) => api('POST', '/shows', listStatus ? { anilist_id: anilistId, list_status: listStatus } : { anilist_id: anilistId }),
    show: (id) => api('GET', `/shows/${id}`),
    patchShow: (id, patch) => api('PATCH', `/shows/${id}`, patch),
    deleteShow: (id, deleteFiles) => api('DELETE', `/shows/${id}?delete_files=${deleteFiles ? 1 : 0}`),
    setWatched: (id, n, watched) => api('POST', `/shows/${id}/episodes/${n}/watched`, { watched }),
    refreshShow: (id) => api('POST', `/shows/${id}/refresh`),
    search: (text) => api('GET', '/search' + q({ q: text })),
    season: (season, year) => api('GET', '/season' + q({ season, year })),
    upnext: () => api('GET', '/upnext'),
    calendar: (from, to) => api('GET', '/calendar' + q({ from, to })),
    // TODO(spec): "after=0&limit=N" may return the OLDEST N if the server sorts ascending.
    // The feed asks without `after` and sorts newest-first itself; see PROGRESS-ui.md.
    events: (after, limit = 60) => api('GET', '/events' + q({ after, limit })),
    releases: (showId, episode) => api('GET', '/releases' + q({ show_id: showId, episode })),
    addRelease: (id) => api('POST', `/releases/${id}/add`),
    skipRelease: (id) => api('POST', `/releases/${id}/skip`),
    runConveyor: () => api('POST', '/conveyor/run'),
    runScan: () => api('POST', '/scan/run'),
    openFile: (id) => api('POST', `/files/${id}/open`),
    revealFile: (id) => api('POST', `/files/${id}/reveal`),
    authStart: (service) => api('GET', `/auth/${service}/start`),
    importAniList: (userName) => api('POST', '/import/anilist', { user_name: userName }),
    importMal: () => api('POST', '/import/mal'),
    accounts: () => api('GET', '/accounts'),
    deleteAccount: (service) => api('DELETE', `/accounts/${service}`),
    // v1.1 (SPEC-v1.1.md section 3)
    folders: () => api('GET', '/folders'),
    addFolder: (body) => api('POST', '/folders', body),
    patchFolder: (body) => api('PATCH', '/folders', body),
    deleteFolder: (path) => api('DELETE', '/folders' + q({ path })),
    fsList: (path) => api('GET', '/fs/list' + (path ? q({ path }) : '?path=')),
    fsSuggest: () => api('GET', '/fs/suggest'),
    openFolder: (path) => api('POST', '/folders/open', { path }),
    openShowFolder: (id) => api('POST', `/shows/${id}/open-folder`),
    testMagnet: () => api('GET', '/settings/test/magnet'),
    // SPEC-v1.1 section 9 (auto-download guard rails)
    setEpisodeAuto: (id, n, auto) => api('POST', `/shows/${id}/episodes/${n}/auto`, { auto }),
    // SPEC-v1.3 2.3: failed releases -> skipped ("cleared by user"); episode null = the whole show
    clearFailed: (id, episode) => api('POST', `/shows/${id}/releases/clear-failed`, episode != null ? { episode } : undefined),
    missingPreview: (id) => api('GET', `/shows/${id}/download-missing`),
    downloadMissing: (id) => api('POST', `/shows/${id}/download-missing`),
    // SPEC-v1.1 section 11 (bulk watched, honest Home hero)
    watchedThrough: (id, number) => api('POST', `/shows/${id}/watched-through`, { number }),
    missing: () => api('GET', '/missing'),
    quit: () => api('POST', '/quit'),
  };
  /** Absolute path test (Windows drive, UNC, or unix). The server resolves relative paths against its own
      working directory, which is never what the user meant, so the UI asks for a full path. */
  const isAbsPath = (p) => /^[a-z]:[\\/]/i.test(p) || /^\\\\[^\\]+\\[^\\]+/.test(p) || /^\//.test(p);
  const samePath = (a, b) => {
    const n = (p) => String(p || '').trim().replace(/[\\/]+$/, '').replace(/\//g, '\\');
    return OS_WIN || /^[a-z]:/i.test(a) ? n(a).toLowerCase() === n(b).toLowerCase() : n(a) === n(b);
  };
  /** Library folders as stored in settings (v1.1). Strips the read-only FolderState extras. */
  const toFolders = (list) => (list || []).map(({ path, label, download, active, added_at, scan }) => ({ path, label: label || '', download: !!download, active: !!active, added_at: added_at || 0, scan: scan || 'shows' }));
  /** The download folder from settings ("" when none). */
  const downloadDirOf = (settings) => { const f = ((settings && settings.library && settings.library.folders) || []).find((x) => x.active && x.download); return f ? f.path : ''; };
  /** Resolve to `fallback` instead of throwing; the error is kept so one failing panel does not blank the page. */
  const soft = (p, fallback) => p.then((v) => ({ v, err: null }), (err) => ({ v: fallback, err }));

  // ------------------------------------------------------------------ icons (inline SVG, 24px grid)
  const ICONS = {
    home: '<path d="M3.5 10.5 12 3.5l8.5 7"/><path d="M5.5 9v11h4.5v-6h4v6h4.5V9"/>',
    grid: '<rect x="3.5" y="3.5" width="7" height="7" rx="1.8"/><rect x="13.5" y="3.5" width="7" height="7" rx="1.8"/><rect x="3.5" y="13.5" width="7" height="7" rx="1.8"/><rect x="13.5" y="13.5" width="7" height="7" rx="1.8"/>',
    plus: '<path d="M12 5v14M5 12h14"/>',
    calendar: '<rect x="3.5" y="5" width="17" height="15.5" rx="2.5"/><path d="M3.5 10h17M8 3v4M16 3v4"/>',
    gear: '<path d="M4 7h9M17 7h3M4 17h3M11 17h9"/><circle cx="15" cy="7" r="2.2"/><circle cx="9" cy="17" r="2.2"/>',
    play: '<path d="M7.5 4.8v14.4a.6.6 0 0 0 .9.5l11.4-7.2a.6.6 0 0 0 0-1L8.4 4.3a.6.6 0 0 0-.9.5z" fill="currentColor" stroke="none"/>',
    check: '<path d="M5 12.5l4.5 4.5L19 7.5"/>',
    checkAll: '<path d="M2 12.5l4.5 4.5L16 7.5"/><path d="M11.5 16.5l.5.5 9.5-9.5"/>',
    folder: '<path d="M3.5 7.5a2 2 0 0 1 2-2h4l2 2.5h7a2 2 0 0 1 2 2v7.5a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2z"/>',
    refresh: '<path d="M19.5 11A7.5 7.5 0 0 0 6.2 6.6L4.5 8.3"/><path d="M4.5 4.5v3.8h3.8"/><path d="M4.5 13a7.5 7.5 0 0 0 13.3 4.4l1.7-1.7"/><path d="M19.5 19.5v-3.8h-3.8"/>',
    download: '<path d="M12 4v11M7 10.5l5 5 5-5"/><path d="M5 19.5h14"/>',
    magnet: '<path d="M5 4.5h4v7a3 3 0 0 0 6 0v-7h4v7a7 7 0 0 1-14 0z"/><path d="M5 8.5h4M15 8.5h4"/>',
    x: '<path d="M6 6l12 12M18 6 6 18"/>',
    power: '<path d="M12 3.5v8"/><path d="M7 6.5a7.5 7.5 0 1 0 10 0"/>',
    chevL: '<path d="M14.5 5.5 8 12l6.5 6.5"/>',
    chevR: '<path d="M9.5 5.5 16 12l-6.5 6.5"/>',
    search: '<circle cx="11" cy="11" r="6.5"/><path d="M20 20l-4.2-4.2"/>',
    bolt: '<path d="M13 3 5 13.5h6L10 21l8-10.5h-6z"/>',
    boltOff: '<path d="M13 3 5 13.5h6L10 21l8-10.5h-6z"/><path d="M4 4l16 16"/>',
    eye: '<path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12z"/><circle cx="12" cy="12" r="3"/>',
    eyeOff: '<path d="M4 4l16 16"/><path d="M9.9 5.8A9.6 9.6 0 0 1 12 5.5c6 0 9.5 6.5 9.5 6.5a16 16 0 0 1-3 3.6M6.2 7.4C3.9 9 2.5 12 2.5 12S6 18.5 12 18.5c1.3 0 2.5-.3 3.6-.8"/>',
    clock: '<circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 2"/>',
    alert: '<path d="M12 3.8 2.8 19.5h18.4z"/><path d="M12 10v4.2M12 17.2v.3"/>',
    file: '<path d="M6.5 3.5h7l4 4V20a.5.5 0 0 1-.5.5H7a.5.5 0 0 1-.5-.5z"/><path d="M13.5 3.5v4h4"/>',
    upload: '<path d="M12 20V9M7 13.5l5-5 5 5"/><path d="M5 4.5h14"/>',
    skip: '<path d="M5.5 5.5 14 12l-8.5 6.5z"/><path d="M18 5.5v13"/>',
    disk: '<rect x="3.5" y="13" width="17" height="6.5" rx="2"/><path d="M5.5 13 8 5h8l2.5 8"/><path d="M16.5 16.3h.01"/>',
    tv: '<rect x="3" y="5.5" width="18" height="12" rx="2.5"/><path d="M8.5 21h7"/>',
    external: '<path d="M14 4h6v6M20 4l-9 9"/><path d="M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5"/>',
    copy: '<rect x="8.5" y="8.5" width="11" height="11" rx="2"/><path d="M15.5 8.5v-3a1 1 0 0 0-1-1h-9a1 1 0 0 0-1 1v9a1 1 0 0 0 1 1h3"/>',
    trash: '<path d="M4.5 6.5h15M9.5 6.5v-2h5v2M6.5 6.5l1 13h9l1-13"/>',
    plug: '<path d="M9 3v5M15 3v5"/><path d="M6.5 8h11v3a5.5 5.5 0 0 1-11 0z"/><path d="M12 16.5V21"/>',
    user: '<circle cx="12" cy="8" r="4"/><path d="M4.5 20.5a7.5 7.5 0 0 1 15 0"/>',
    list: '<path d="M8.5 6.5h12M8.5 12h12M8.5 17.5h12"/><path d="M4 6.5h.01M4 12h.01M4 17.5h.01"/>',
    up: '<path d="M12 19V6M6.5 11.5 12 6l5.5 5.5"/>',
    folderPlus: '<path d="M3.5 7.5a2 2 0 0 1 2-2h4l2 2.5h7a2 2 0 0 1 2 2v7.5a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2z"/><path d="M12 11v5M9.5 13.5h5"/>',
    compass: '<circle cx="12" cy="12" r="8.5"/><path d="m15.5 8.5-2 5-5 2 2-5z"/>',
    chevD: '<path d="M5.5 9.5 12 16l6.5-6.5"/>',
    more: '<circle cx="5.5" cy="12" r="1.6" fill="currentColor" stroke="none"/><circle cx="12" cy="12" r="1.6" fill="currentColor" stroke="none"/><circle cx="18.5" cy="12" r="1.6" fill="currentColor" stroke="none"/>',
    pulse: '<path d="M2.5 12h4l2.5-6.5 4.5 13 2.5-6.5h5.5"/>',
    pin: '<path d="M9 3.5h6l-1 6 3.5 3.5h-11L10 9.5z"/><path d="M12 13v7.5"/>',
  };
  const svg = (name) => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">${ICONS[name] || ''}</svg>`;
  const ico = (name, cls = '') => raw(`<span class="ico ${cls}">${svg(name)}</span>`);

  // ------------------------------------------------------------------ accent + backdrop
  function hexToRgb(hex) {
    let x = hex.trim().replace('#', '');
    if (x.length === 3) x = x.split('').map((c) => c + c).join('');
    const n = parseInt(x, 16);
    return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
  }
  function rgbToHsl([r, g, b]) {
    r /= 255; g /= 255; b /= 255;
    const mx = Math.max(r, g, b); const mn = Math.min(r, g, b);
    let hh = 0; let s = 0; const l = (mx + mn) / 2;
    if (mx !== mn) {
      const d = mx - mn;
      s = l > 0.5 ? d / (2 - mx - mn) : d / (mx + mn);
      hh = mx === r ? (g - b) / d + (g < b ? 6 : 0) : mx === g ? (b - r) / d + 2 : (r - g) / d + 4;
      hh /= 6;
    }
    return [hh, s, l];
  }
  function hslToRgb(hh, s, l) {
    if (s === 0) return [l * 255, l * 255, l * 255];
    const f = (p, qq, t) => { if (t < 0) t += 1; if (t > 1) t -= 1; if (t < 1 / 6) return p + (qq - p) * 6 * t; if (t < 1 / 2) return qq; if (t < 2 / 3) return p + (qq - p) * (2 / 3 - t) * 6; return p; };
    const qq = l < 0.5 ? l * (1 + s) : l + s - l * s; const p = 2 * l - qq;
    return [f(p, qq, hh + 1 / 3) * 255, f(p, qq, hh) * 255, f(p, qq, hh - 1 / 3) * 255];
  }
  const toHex = (rgb) => '#' + rgb.map((v) => clamp(Math.round(v), 0, 255).toString(16).padStart(2, '0')).join('');
  function lum(rgb) {
    const f = (c) => { c /= 255; return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4; };
    return 0.2126 * f(rgb[0]) + 0.7152 * f(rgb[1]) + 0.0722 * f(rgb[2]);
  }
  const contrast = (a, b) => { const x = lum(a); const y = lum(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05); };
  /** Clamp the show colour so it reads on dark glass: HSL lightness 0.64-0.80, saturation <= 0.82.
      Ink (text on accent buttons) = whichever of near-black / white has more contrast. */
  function readableAccent(hex) {
    const [hh, s, l] = rgbToHsl(hexToRgb(hex));
    const rgb = hslToRgb(hh, Math.min(s, 0.82), clamp(l, 0.64, 0.8));
    const ink = contrast(rgb, [11, 13, 18]) >= contrast(rgb, [255, 255, 255]) ? '#0b0d12' : '#ffffff';
    return { accent: toHex(rgb), ink };
  }
  function applyAccent(color) {
    const override = S.settings && S.settings.ui && S.settings.ui.accent;
    const base = isHex(override) ? override : isHex(color) ? color : FALLBACK_ACCENT;
    const { accent, ink } = readableAccent(base);
    const st = document.documentElement.style;
    st.setProperty('--accent', accent);
    st.setProperty('--accent-ink', ink);
    st.setProperty('--tint', base);
  }
  let backdropUrl = null;
  function setBackdrop(url) {
    const el = $('#backdrop-img');
    if (!el) return;
    url = safeUrl(url);
    if (url === backdropUrl) return;
    backdropUrl = url;
    const delay = motionOK() ? 260 : 0;
    const apply = () => {
      if (backdropUrl !== url) return;
      el.style.backgroundImage = url ? cssUrl(url) : '';
      el.classList.toggle('is-empty', !url);
      requestAnimationFrame(() => el.classList.remove('is-fading'));
    };
    if (!url) { el.classList.add('is-fading'); setTimeout(apply, delay); return; }
    let done = false;
    const go = () => { if (done) return; done = true; el.classList.add('is-fading'); setTimeout(apply, delay); };
    const img = new Image();
    img.onload = go;
    img.onerror = () => { done = true; if (backdropUrl === url) { backdropUrl = null; setBackdrop(''); } };
    img.src = url;
    setTimeout(go, 3000);
  }
  /** Remember the show the user touched last: its banner becomes the blurred page backdrop, its colour the accent. */
  function touchShow(show) {
    if (!show) return;
    const t = { id: show.show_id ?? show.id, banner_url: show.banner_url || '', cover_url: show.cover_url || '', color: show.color || '' };
    S.last = t;
    store.set('last', t);
    paintFromLast();
  }
  function paintFromLast() {
    const t = S.last;
    setBackdrop(t ? t.banner_url || t.cover_url : '');
    applyAccent(t ? t.color : '');
  }
  function applyUiSettings() {
    const ui = (S.settings && S.settings.ui) || {};
    document.documentElement.classList.toggle('reduce-motion', !!ui.reduced_motion);
    document.documentElement.dataset.theme = 'glass'; // only "glass" exists in v1
    applyAccent(S.last ? S.last.color : '');
  }

  // ------------------------------------------------------------------ toasts + confirm
  function toast(msg, tone = 'info', ms) {
    const box = $('#toasts');
    const el = document.createElement('div');
    el.className = `toast glass ${tone}`;
    el.innerHTML = String(h`${ico(tone === 'ok' ? 'check' : tone === 'bad' ? 'alert' : 'bolt')}<div>${msg}</div>`);
    box.prepend(el);
    while (box.children.length > 4) box.lastElementChild.remove();
    setTimeout(() => el.remove(), ms || (tone === 'bad' ? 7000 : 4200));
  }
  /** Native <dialog> confirm. Resolves {ok, dialog} so callers can read extra inputs placed in `body`. */
  function confirmBox({ title, body, ok = 'OK', danger = true }) {
    const dlg = $('#confirm');
    if (!dlg || typeof dlg.showModal !== 'function') {
      return Promise.resolve({ ok: window.confirm(title), dialog: null });
    }
    $('#confirm-title', dlg).textContent = title;
    $('#confirm-text', dlg).innerHTML = String(body || '');
    const okBtn = $('#confirm-ok', dlg);
    okBtn.textContent = ok;
    okBtn.className = `btn ${danger ? 'btn-danger' : 'btn-primary'}`;
    dlg.returnValue = '';
    dlg.showModal();
    return new Promise((res) => dlg.addEventListener('close', () => res({ ok: dlg.returnValue === 'ok', dialog: dlg }), { once: true }));
  }
  async function copyText(text) {
    try { await navigator.clipboard.writeText(text); return true; } catch {
      const ta = document.createElement('textarea');
      ta.value = text; ta.setAttribute('readonly', ''); ta.style.position = 'fixed'; ta.style.left = '-9999px';
      document.body.appendChild(ta); ta.select();
      let ok = false;
      try { ok = document.execCommand('copy'); } catch { ok = false; }
      ta.remove();
      return ok;
    }
  }

  // ------------------------------------------------------------------ shared pieces
  function showTitle(s) { return (s && (s.title || s.show_title || s.title_romaji || s.title_english)) || 'Untitled'; }
  function mediaTitle(m) {
    const lang = S.settings && S.settings.titles && S.settings.titles.language;
    const t = m.title && typeof m.title === 'object' ? m.title : m;
    const ro = t.title_romaji || t.romaji; const en = t.title_english || t.english;
    if (typeof m.title === 'string') return m.title;
    return (lang === 'english' ? en || ro : ro || en) || 'Untitled';
  }
  function progressOf(show) {
    const p = (show && show.progress) || {};
    const watched = p.watched || 0; const onDisk = p.on_disk || 0; const aired = p.aired || 0;
    const total = (show && show.episodes_total) || 0;
    const scale = Math.max(total, aired, onDisk, watched, 1);
    return { watched, onDisk, aired, total, scale };
  }
  function progBar(show, withLegend = false) {
    const g = progressOf(show);
    const pct = (n) => `${clamp((n / g.scale) * 100, 0, 100).toFixed(2)}%`;
    const of = g.total ? ` of ${fNum(g.total)}` : '';
    const label = `${fNum(g.watched)} watched, ${fNum(g.onDisk)} on disk, ${fNum(g.aired)} aired${of}`;
    return h`<div class="prog" role="img" aria-label="${label}" title="${label}">
      <i class="p-aired" style="width:${pct(g.aired)}"></i><i class="p-disk" style="width:${pct(g.onDisk)}"></i><i class="p-watched" style="width:${pct(g.watched)}"></i>
    </div>${withLegend ? h`<div class="prog-legend">
      <span><i style="background:var(--accent)"></i>${fNum(g.watched)} watched</span>
      <span><i style="background:var(--accent-dim)"></i>${fNum(g.onDisk)} on disk</span>
      <span><i style="background:rgba(255,255,255,.2)"></i>${fNum(g.aired)} aired${of}</span></div>` : h`<div class="tiny muted">${fNum(g.watched)} watched · ${fNum(g.onDisk)} on disk · ${fNum(g.aired)} aired${of}</div>`}`;
  }
  /** Calendar / week chip state: watched, on disk, downloading, aired-not-found, upcoming. */
  function calState(it) {
    if (it.watched) return ['s-watched', 'Watched'];
    if (it.on_disk) return ['s-disk', 'On disk'];
    if (['added', 'downloading', 'chosen'].includes(it.release_status)) return ['s-dl', it.release_status === 'chosen' ? 'Magnet ready' : 'Downloading'];
    if (it.aired_at && it.aired_at > nowSec()) return ['s-future', 'Upcoming'];
    return ['s-aired', 'Aired, not found yet'];
  }
  const dubsOn = () => !!(S.settings && S.settings.dubs && S.settings.dubs.enabled && S.settings.dubs.show_in_calendar);
  /** Expand calendar items into chips: one per sub airing, plus one per dub airing when dubs are shown. */
  function calChips(items) {
    const out = [];
    for (const it of items || []) {
      if (it.aired_at) out.push({ it, ts: it.aired_at, dub: false });
      if (dubsOn() && it.dub_aired_at) out.push({ it, ts: it.dub_aired_at, dub: true });
    }
    return out.sort((a, b) => a.ts - b.ts);
  }
  function epChip(c, withTitle = true) {
    const [cls, label] = calState(c.it);
    const title = c.it.title || (S.showsById[c.it.show_id] && showTitle(S.showsById[c.it.show_id])) || 'Show';
    const tip = `${title} · episode ${c.it.episode}${c.dub ? ' (dub)' : ''} · ${fFull(c.ts)} · ${label}`;
    return h`<a class="ep-chip ${cls === 's-watched' ? 'is-watched' : ''}" href="#/show/${c.it.show_id}" title="${tip}">
      <span class="dot ${cls}" aria-hidden="true"></span>
      ${withTitle ? h`<span class="t">${title}</span><span class="m">Ep ${epNum(c.it.episode, c.it.abs_number)}${c.dub ? ' dub' : ''} · ${fTime(c.ts)}</span>` : h`<span class="t">Ep ${epNum(c.it.episode, c.it.abs_number)}${c.dub ? ' dub' : ''}</span><span class="m">${fTime(c.ts)}</span>`}
      <span class="sr-only">${label}</span>
    </a>`;
  }
  /** SPEC-v1.2 3.4 week entry: poster thumb, title, status dot + "Ep 17 · 11:26 PM" (Home strip + Calendar agenda). */
  function weekEntry(c) {
    const [cls, label] = calState(c.it);
    const show = S.showsById[c.it.show_id] || {};
    const title = c.it.title || showTitle(show);
    return h`<a class="wk-ep ${cls === 's-watched' ? 'is-watched' : ''}" href="#/show/${c.it.show_id}" title="${title} · episode ${c.it.episode}${c.dub ? ' (dub)' : ''} · ${fFull(c.ts)} · ${label}">
      <img class="wk-thumb" src="${safeUrl(show.cover_url)}" alt="" loading="lazy">
      <span class="wk-t">${title}</span>
      <span class="wk-m"><span class="dot ${cls}" aria-hidden="true"></span>Ep ${epNum(c.it.episode, c.it.abs_number)}${c.dub ? ' dub' : ''}</span>
      <span class="wk-time">${fTime(c.ts)}</span>
      <span class="sr-only">${label}</span>
    </a>`;
  }
  const stateLegend = () => h`<div class="legend" aria-label="Legend">
    <span><i class="dot s-aired"></i>Aired, not found</span><span><i class="dot s-dl"></i>Downloading</span>
    <span><i class="dot s-disk"></i>On disk</span><span><i class="dot s-watched"></i>Watched</span><span><i class="dot s-future"></i>Upcoming</span></div>`;
  const emptyBox = (icon, title, text, action) => h`<div class="empty">${ico(icon)}<h2>${title}</h2>${text ? h`<p>${text}</p>` : ''}${action || ''}</div>`;
  const skel = (hh, extra = '') => raw(`<div class="skel" style="height:${hh}px;${extra}"></div>`);
  function errorPanel(err, retry = true) {
    const offline = err && err.status === 0;
    const mockHint = location.protocol === 'file:' ? h` Opened as a file? Add <code>?mock=1</code> to the address for demo data.` : '';
    return h`<section class="surface error-box">
      <h2>${offline ? 'The ABC server is not answering' : 'Something went wrong'}</h2>
      <p class="muted">${offline ? h`Start <code>abc.exe</code> and reload. The UI talks to it at <code>${location.host || '127.0.0.1:7700'}</code>.${mockHint}` : err && err.message}</p>
      ${retry ? h`<div class="form-actions" style="justify-content:flex-start"><button class="btn btn-sm" data-act="retry">${ico('refresh')}Try again</button></div>` : ''}
    </section>`;
  }
  const softErr = (r, what) => (r && r.err ? h`<p class="note">${what} could not load: ${r.err.message}</p>` : '');
  const A = (first) => (first ? 'rise' : '');

  // ------------------------------------------------------------------ views
  const Views = {};

  // ---------- Home (home.js), Shows (shows.js), activity drawer (drawer.js) register on ABC_EXT.
  // Shared pieces they use live here: indexShows, setup card, notices.
  function indexShows(list) {
    if (!Array.isArray(list)) return;
    S.showsById = {};
    for (const s of list) S.showsById[s.id] = s;
  }

  const hasAnyShow = (st) => Object.keys(S.showsById).length > 0 || !!(st && st.counts && st.counts.watching > 0);
  /** SPEC-v1.1 6.4: the compact setup card shows while there is no download folder, or while downloads are
      manual and no show exists. v1 servers (no download_dir field) fall back to root_ok. */
  function needsSetup(D) {
    const st = D.state;
    if (!('download_dir' in st)) return st.root_ok === false;
    const mode = st.downloader && st.downloader.mode;
    return st.download_dir == null || (mode === 'manual' && !hasAnyShow(st));
  }
  /** The three setup steps as [done, label] (home.js renders them as a Needs you card). */
  function setupTodo(st) {
    const mode = st.downloader && st.downloader.mode;
    return [
      ['download_dir' in st ? st.download_dir != null : st.root_ok !== false, 'Pick where episodes go'],
      [!!mode && mode !== 'manual', 'Choose the app that downloads'],
      [hasAnyShow(st), 'Add a show'],
    ];
  }

  /** SPEC-v1.1 10: free space on the download drive is below conveyor.min_free_gib (0 = off). Servers without
      free_bytes (null / missing) show nothing; an unreachable folder has its own card. */
  const GIB = 1024 ** 3;
  function lowDisk(dd) {
    const c = (S.settings && S.settings.conveyor) || {};
    const min = Number(c.min_free_gib) || 0;
    // Only when something would actually be paused: the conveyor is on and not in manual mode.
    const auto = c.enabled !== false && !!(S.settings && S.settings.downloader) && S.settings.downloader.mode !== 'manual';
    return auto && !!dd && dd.exists !== false && typeof dd.free_bytes === 'number' && min > 0 && dd.free_bytes < min * GIB;
  }
  /** "D:" for a Windows path, "\\server\share" for UNC, else the folder path itself (unix has no drive letter). */
  function driveOf(p) {
    const s = String(p || '');
    const m = s.match(/^[A-Za-z]:/) || s.match(/^\\\\[^\\]+\\[^\\]+/);
    return m ? m[0] : s;
  }
  const LIST_TABS = [['watching', 'Watching'], ['paused', 'Paused'], ['planning', 'Planning'], ['completed', 'Completed'], ['dropped', 'Dropped']];

  // ---------- Show  #/show/{id}
  Views.show = (m) => {
    const id = Number(m[1]);
    const st = { showAll: false, detail: null, preview: null, openPicks: new Set() };
    const dynHead = () => showHead(st.detail, st.preview);
    const dynBody = () => showBody(st.detail, st);
    const reload = () => rerender(); // keeps the scroll position
    /** Repaint header + episode list from st (the settings panel and its inputs stay). */
    const paintParts = (root = $('#view')) => {
      const head = $('#show-head', root); const body = $('#show-body', root);
      const keep = focusKey([head, body]); // a background refresh must not drop keyboard focus
      if (head) { head.innerHTML = String(dynHead()); hydrate(head); }
      if (body) { body.innerHTML = String(dynBody()); hydrate(body); }
      if (keep) focusFirst(...keep);
    };
    /** SPEC-v1.1 11: POST watched-through, repaint from the returned ShowDetail, toast, then put focus back on
        the first element matching `focusSel` (the row button may be gone once its prefix is watched). */
    const markThrough = async (n, focusSel) => {
      const d = await API.watchedThrough(id, n);
      toast(n === 1 ? 'Episode 1 marked watched' : `Episodes 1-${n} marked watched`, 'ok');
      if (d && d.show && Array.isArray(d.episodes)) {
        st.detail = d;
        st.preview = (await soft(API.missingPreview(id), st.preview)).v;
        const y = window.scrollY;
        paintParts();
        window.scrollTo(0, y);
      } else await reload();
      const again = focusSel && $(focusSel);
      if (again) again.focus();
    };
    return {
      title: 'Show', nav: 'shows',
      async load() {
        const [detail, settings, prev] = await Promise.all([API.show(id), S.settings ? Promise.resolve(S.settings) : soft(API.settings(), null).then((r) => r.v), soft(API.missingPreview(id), null)]);
        if (settings) S.settings = settings;
        st.detail = detail;
        st.preview = prev.v;
        return detail;
      },
      render(d, first) {
        this.title = showTitle(d.show);
        document.title = `${this.title} · ABC`;
        return h`<div class="page">
          <div><a class="crumb" href="#/shows">${ico('chevL')}Shows</a></div>
          <div id="show-head" class="${A(first)}">${dynHead()}</div>
          ${showSettingsPanel(d.show, first)}
          <div id="show-body" class="page">${dynBody()}</div>
        </div>`;
      },
      mount(root) {
        touchShow(st.detail.show);
        bindShowSettings(root, st.detail.show, id);
        // Pick release expander (SPEC-v1.2 4): open rows survive repaints through st.openPicks.
        $('#show-body', root).addEventListener('click', (e) => {
          const b = e.target.closest('[data-pick-toggle]');
          if (!b) return;
          const n = Number(b.dataset.pickToggle);
          const row = document.getElementById(b.getAttribute('aria-controls'));
          const open = b.getAttribute('aria-expanded') !== 'true';
          if (open) st.openPicks.add(n); else st.openPicks.delete(n);
          b.setAttribute('aria-expanded', String(open));
          if (row) { row.hidden = !open; if (open) $('select', row)?.focus(); }
        });
        // Bound on #show-head (lives only as long as this view) so listeners never pile up on #view.
        $('#show-head', root).addEventListener('change', async (e) => {
          if (e.target.id !== 'list-status') return;
          const v = e.target.value;
          try { await API.patchShow(id, { list_status: v }); toast(`Moved to ${LIST_TABS.find(([k]) => k === v)?.[1] || v}`, 'ok'); } catch (err) { toast(err.message, 'bad'); }
        });
        // SPEC-v1.1 11: "Watched through episode [N] Set" (Enter submits the form).
        $('#show-head', root).addEventListener('input', (e) => {
          if (e.target.id !== 'wt-n') return;
          e.target.removeAttribute('aria-invalid');
          const er = $('#wt-err'); if (er) er.textContent = '';
        });
        $('#show-head', root).addEventListener('submit', async (e) => {
          if (e.target.id !== 'wt-form') return;
          e.preventDefault();
          const form = e.target; const inp = $('#wt-n', form); const er = $('#wt-err', form); const btn = $('button[type=submit]', form);
          if (btn.classList.contains('is-busy')) return;
          const raw = inp.value.trim(); const n = Number(raw);
          const total = st.detail.show.episodes_total; const max = total ? total + 50 : Infinity;
          const bad = (msg) => { inp.setAttribute('aria-invalid', 'true'); er.textContent = msg; inp.focus(); };
          if (!raw || !Number.isInteger(n) || n < 1) return bad('Enter a whole number, 1 or more.');
          if (n > max) return bad(`Enter a number from 1 to ${max}.`);
          btn.classList.add('is-busy'); btn.setAttribute('aria-busy', 'true');
          try { await markThrough(n, '#wt-n'); } catch (err) {
            btn.classList.remove('is-busy'); btn.removeAttribute('aria-busy');
            if (err.status === 400) bad(err.message); else toast(err.message, 'bad');
          }
        });
      },
      actions: {
        'show-all': (btn) => { st.showAll = true; $('#show-body').innerHTML = String(dynBody()); hydrate($('#show-body')); },
        'refresh-show': async () => { await API.refreshShow(id); toast('Refreshing from AniList and searching nyaa.si', 'ok'); },
        // 409 ("no episode downloaded yet…", "no download folder set") lands in the global error toast.
        'open-show-folder': async () => { await API.openShowFolder(id); toast('Opened the folder', 'ok'); },
        // SPEC-v1.1 9: per-episode auto toggle (1 = always, 0 = never). Focus returns to the same row's toggle.
        'ep-auto': async (btn, ds) => {
          const n = Number(ds.ep); const v = Number(ds.val);
          await API.setEpisodeAuto(id, n, v);
          toast(v ? `Episode ${n} will download by itself.` : `Episode ${n} will not download by itself. Press Add to get it.`, 'ok');
          await reload();
          // The toggle lives in the row's ⋯ menu, which is closed after the repaint: focus that menu's button.
          focusFirst(`#ep-${n} [data-menu]`, `#ep-${n} .btn-watch`);
        },
        // SPEC-v1.3 2.3: failed releases become skipped ("cleared by user"); auto flags are not touched.
        'clear-failed': async () => {
          const r = await API.clearFailed(id);
          const n = (r && r.cleared) || 0;
          toast(`Cleared ${plural(n, 'failed release')}`, 'ok');
          await reload();
          focusFirst('[data-menu="show-more"]', '#play-next');
        },
        'clear-failed-ep': async (btn, ds) => {
          const n = Number(ds.ep);
          await API.clearFailed(id, n);
          toast('Cleared', 'ok');
          await reload();
          focusFirst(`#ep-${n} [data-menu]`, `#ep-${n} .btn-watch`);
        },
        'download-missing': async () => {
          const r = await downloadMissingFlow(id);
          if (r.preview) st.preview = r.preview;
          if (r.changed) await reload();
        },
        // SPEC-v1.1 11: "Up to here" on an episode row.
        'watch-through': async (btn, ds) => { await markThrough(Number(ds.ep), `[data-act="watch-through"][data-ep="${Number(ds.ep)}"], [data-act="watched"][data-ep="${Number(ds.ep)}"]`); },
        'remove-show': async () => {
          const { ok, dialog } = await confirmBox({
            title: `Remove ${showTitle(st.detail.show)}?`,
            body: h`<p>ABC stops tracking it and forgets its episodes and releases.</p><label class="check"><input type="checkbox" class="switch" id="del-files"> Also delete its downloaded files (only files inside your download folder)</label>`,
            ok: 'Remove',
          });
          if (!ok) return;
          const delFiles = !!(dialog && $('#del-files', dialog) && $('#del-files', dialog).checked);
          await API.deleteShow(id, delFiles);
          toast(`Removed ${showTitle(st.detail.show)}`, 'ok');
          location.hash = '#/shows';
        },
      },
      async refresh(root) {
        const [detail, prev] = await Promise.all([API.show(id), soft(API.missingPreview(id), null)]);
        st.detail = detail;
        st.preview = prev.v;
        paintParts(root);
      },
      refreshOn: (ev) => !ev.show_id || Number(ev.show_id) === id,
    };
  };
  /** Downloader mode for wording (SPEC-v1.1 1): settings first, then /api/state. */
  const downloaderMode = () => (S.settings && S.settings.downloader && S.settings.downloader.mode) || (S.state && S.state.downloader && S.state.downloader.mode) || '';
  const fGiB = (b) => { const g = b / 1073741824; return g >= 10 ? Math.round(g).toString() : g.toFixed(1); };
  /** SPEC-v1.1 9 "Download all missing" for one show: fresh preview, confirm dialog worded per downloader mode,
      POST, toast. Shared by the show header and the Home "Nothing to play yet" hero (SPEC-v1.1 11).
      Returns {preview, changed}; changed = the caller should refresh. */
  async function downloadMissingFlow(id) {
    const p = await API.missingPreview(id);
    const n = (p && p.count) || 0;
    if (!n) { toast('Nothing to download: every aired episode has a file or a release.', 'info'); return { preview: p, changed: true }; }
    const mode = downloaderMode();
    const gib = p.bytes ? `about ${fGiB(p.bytes)} GiB` : '';
    const sz = gib ? `, ${gib},` : '';
    const title = {
      qbittorrent: `Add ${plural(n, 'release')}${sz} to qBittorrent?`,
      magnet: `Hand ${plural(n, 'magnet link')}${sz} to your torrent app?`,
      torrent_file: `Save ${plural(n, '.torrent file')}${sz} to the watch folder?`,
      manual: `Mark ${plural(n, 'release')} chosen${gib ? ` (${gib})` : ''}?`,
    }[mode] || `Add ${plural(n, 'release')}${sz} to your torrent app?`;
    const { ok } = await confirmBox({
      title,
      body: h`<p>One release for each aired episode that has no file yet, picked by this show's rules. The size limit in Settings does not apply here.</p>${mode === 'manual' ? h`<p>You add them yourself with the Magnet buttons.</p>` : ''}`,
      ok: mode === 'manual' ? 'Mark chosen' : 'Download',
      danger: false,
    });
    if (!ok) return { preview: p, changed: false };
    const r = await API.downloadMissing(id);
    const added = (r && r.added) || 0; const skipped = (r && r.skipped) || 0;
    const done = {
      qbittorrent: `Sent ${plural(added, 'release')} to qBittorrent.`,
      magnet: `Handed ${plural(added, 'magnet link')} to your torrent app.`,
      torrent_file: `Saved ${plural(added, '.torrent file')} to the watch folder.`,
      manual: `Marked ${plural(added, 'release')} chosen. Use the Magnet buttons to add them.`,
    }[mode] || `Added ${plural(added, 'release')}.`;
    toast(`${done}${skipped ? ` ${plural(skipped, 'episode')} had no release to pick.` : ''}`, added ? 'ok' : 'info', 7000);
    return { preview: p, changed: true };
  }
  /** SPEC-v1.2 4: banner-tinted glass header, poster 160, title 28, chips, progress with the three counts,
      action row Play next · Watched through · Download all missing · Open folder · AniList · ⋯ (Refresh, list, Remove). */
  function showHead(d, preview) {
    const s = d.show;
    const missing = preview && preview.count > 0 ? preview.count : 0;
    const alts = [s.title_romaji, s.title_english, s.title_native].filter((t, i, a) => t && t !== showTitle(s) && a.indexOf(t) === i);
    const seasonTxt = s.season && s.season_year ? `${pretty(s.season)} ${s.season_year}` : s.season_year || '';
    const eps = (d.episodes || []).slice().sort((a, b) => a.number - b.number);
    const nextFile = eps.find((e) => e.file && !e.watched_at);
    const off = effOffset(s); // SPEC-v1.3 1.5: "Next Ep 3 (173)"
    const failedN = (d.releases || []).filter((r) => r.status === 'failed').length; // SPEC-v1.3 2.3
    const color = isHex(s.color) ? raw(`style="--c:${s.color}"`) : '';
    return h`<section class="show-head" ${color}>
      <div class="show-banner" data-bg="${s.banner_url || ''}"></div>
      <div class="show-card glass-hero">
        <img class="show-poster" src="${safeUrl(s.cover_url)}" alt="">
        <div class="show-info">
          <h1 class="show-title">${showTitle(s)}</h1>
          ${alts.length ? h`<div class="show-alt">${alts.join(' · ')}</div>` : ''}
          <div class="show-meta">
            ${s.format ? h`<span class="chip">${FORMAT_LABEL[s.format] || s.format}</span>` : ''}
            ${s.status ? h`<span class="chip">${STATUS_LABEL[s.status] || pretty(s.status)}</span>` : ''}
            ${seasonTxt ? h`<span class="chip">${seasonTxt}</span>` : ''}
            <span class="chip">${s.episodes_total ? `${fNum(s.episodes_total)} eps` : 'Episodes: unknown'}</span>
            ${s.duration_min ? h`<span class="chip">${s.duration_min} min</span>` : ''}
            ${s.next && s.next.aired_at ? h`<span class="chip chip-accent" title="${fFull(s.next.aired_at)}">${ico('clock')}Next Ep ${epNum(s.next.episode, off > 0 ? s.next.episode + off : null)} · ${fWhen(s.next.aired_at)}</span>` : ''}
          </div>
          <div class="show-prog">${progBar(s, true)}</div>
          <div class="show-actions">
            ${nextFile ? h`<button class="btn btn-primary" data-act="play" data-file="${nextFile.file.id}" data-show="${s.id}" data-label="episode ${nextFile.number}" id="play-next">${ico('play')}Play episode ${epNum(nextFile.number, nextFile.abs_number)}</button>` : ''}
            ${watchedThroughForm(d)}
            ${missing ? h`<button class="btn" data-act="download-missing" title="${plural(missing, 'aired episode')} without a file or a release">${ico('download')}Download all missing<span class="btn-n">${fNum(missing)}</span></button>` : ''}
            <button class="btn" data-act="open-show-folder" title="Open this show's folder in your file manager">${ico('folder')}Open folder</button>
            ${s.anilist_id ? h`<a class="btn btn-ghost" href="https://anilist.co/anime/${Number(s.anilist_id)}" target="_blank" rel="noopener noreferrer">${ico('external')}AniList</a>` : ''}
            ${menu('show-more', `More actions for ${showTitle(s)}`, h`
              <button type="button" class="menu-item" data-act="refresh-show">${ico('refresh')}Refresh from AniList</button>
              ${failedN ? h`<button type="button" class="menu-item" data-act="clear-failed">${ico('x')}Clear failed releases <span class="menu-sub">${plural(failedN, 'release')}</span></button>` : ''}
              <div class="menu-field"><label for="list-status">List</label>
                <select class="select select-sm" id="list-status">${LIST_TABS.map(([k, l]) => h`<option value="${k}" ${s.list_status === k ? raw('selected') : ''}>${l}</option>`)}</select></div>
              <button type="button" class="menu-item is-danger" data-act="remove-show">${ico('trash')}Remove show</button>`, 'btn')}
          </div>
        </div>
      </div>
    </section>`;
  }
  /** ⋯ popover: a disclosure button + a panel of buttons (app-wide handler in "menus" below). */
  function menu(id, label, items, btnCls = 'btn btn-sm btn-ghost') {
    return h`<div class="menu"><button type="button" class="${btnCls} btn-icon menu-btn" data-menu="${id}" aria-haspopup="true" aria-expanded="false" aria-controls="${id}" aria-label="${label}" data-tip="More">${ico('more')}</button>
      <div class="menu-pop surface" id="${id}" hidden>${items}</div></div>`;
  }
  /** SPEC-v1.1 11 header field, prefilled with the highest watched episode number (empty when none). */
  function watchedThroughForm(d) {
    const hi = (d.episodes || []).reduce((a, e) => (e.watched_at && e.number > a ? e.number : a), 0);
    const total = d.show.episodes_total;
    return h`<form class="wt-form" id="wt-form" novalidate>
      <label for="wt-n">Watched through</label>
      <input class="input" id="wt-n" type="number" inputmode="numeric" min="1" ${total ? raw(`max="${Number(total) + 50}"`) : ''} step="1" value="${hi || ''}" aria-describedby="wt-err">
      <button class="btn" type="submit" aria-label="Set: mark episodes 1 to the number watched">Set</button>
      <span class="err" id="wt-err" role="alert"></span>
    </form>`;
  }
  /** Episodes 1..n are all watched (n = 0 when episode 1 is not). "Up to here" hides on rows up to n. */
  function watchedPrefix(eps) {
    let n = 0;
    for (const e of eps) { if (e.number !== n + 1 || !e.watched_at) break; n = e.number; }
    return n;
  }
  function epState(e, rel) {
    const chips = [];
    const now = nowSec();
    if (e.watched_at) chips.push(h`<span class="chip chip-dim" title="${fFull(e.watched_at)}${e.watched_source ? ' · ' + e.watched_source : ''}"><span class="dot s-watched"></span>Watched</span>`);
    if (e.file) chips.push(h`<span class="chip chip-ok"><span class="dot s-disk"></span>On disk</span>`);
    else if (rel) {
      const map = { added: ['chip-warn', 's-dl', 'Downloading'], downloading: ['chip-warn', 's-dl', 'Downloading'], chosen: ['chip-warn', 's-dl', 'Magnet ready'], done: ['chip-ok', 's-disk', 'Downloaded'], failed: ['chip-bad', 's-bad', 'Failed'], skipped: ['', 's-aired', 'Skipped'] };
      const x = map[rel.status];
      if (x) chips.push(h`<span class="chip ${x[0]}" title="${rel.title || ''}${rel.status_note ? ' · ' + rel.status_note : ''}"><span class="dot ${x[1]}"></span>${x[2]}</span>`);
    }
    // SPEC-v1.1 9: aired, no file, no active release, and the server says the auto rule does not apply.
    const activeRel = rel && ['chosen', 'added', 'downloading'].includes(rel.status);
    const notAuto = !e.file && !activeRel && e.aired_at && e.aired_at <= now && e.auto_reason;
    if (notAuto) {
      const days = (S.settings && S.settings.conveyor && S.settings.conveyor.lookback_days) || 14;
      chips.push(h`<span class="chip chip-dim chip-noauto" title="ABC downloads by itself only episodes that aired in the last ${days} days. Press Add to get this one.">Not auto · ${e.auto_reason}</span>`);
    }
    if (!e.file && !chips.length) {
      if (e.aired_at && e.aired_at > now) chips.push(h`<span class="chip"><span class="dot s-future"></span>Airs ${fRel(e.aired_at)}</span>`);
      else if (e.aired_at) chips.push(h`<span class="chip"><span class="dot s-aired"></span>Not found yet</span>`);
      else chips.push(h`<span class="chip"><span class="dot s-future"></span>No date</span>`);
    }
    return chips;
  }
  /** SPEC-v1.1 9 per-episode auto, now a ⋯ menu item. "On" = the conveyor would take it by itself: auto 1, or auto
      unset and no reason from the server. auto 1 below the show's auto_from floor still counts as on (the chip explains). */
  function autoItem(e) {
    const on = e.auto === 1 || (e.auto !== 0 && !e.auto_reason);
    const label = on ? 'Stop auto-downloading' : 'Auto-download this episode';
    return h`<button type="button" class="menu-item btn-auto" data-act="ep-auto" data-ep="${e.number}" data-val="${on ? 0 : 1}" aria-label="${label}: episode ${e.number}">${ico(on ? 'boltOff' : 'bolt')}${label}</button>`;
  }
  function upToHere(n) {
    const label = n === 1 ? 'Mark episode 1 watched' : `Mark episodes 1 to ${n} watched`;
    return h`<button class="btn btn-sm btn-ghost btn-icon btn-thru" data-act="watch-through" data-ep="${n}" data-tip="Up to here" aria-label="${label}">${ico('checkAll')}</button>`;
  }
  const relDesc = (r) => [r.release_group || 'Unknown group', r.resolution, r.version > 1 ? 'v' + r.version : ''].filter(Boolean).join(' ');
  /** SPEC-v1.2 4: # · Aired · State · Release · Actions. Candidates sit behind "Pick release" (an expansion row). */
  function showBody(d, st) {
    const s = d.show;
    const eps = (d.episodes || []).slice().sort((a, b) => a.number - b.number);
    const rels = d.releases || [];
    const relById = Object.fromEntries(rels.map((r) => [r.id, r]));
    const now = nowSec();
    const nextIdx = eps.findIndex((e) => !e.watched_at && (e.file || (e.aired_at && e.aired_at <= now)));
    const prefix = watchedPrefix(eps); // SPEC-v1.1 11: no "Up to here" where 1..n are already watched
    let from = 0; let to = eps.length;
    if (!st.showAll) {
      if (eps.length > 40) from = Math.max(0, (nextIdx === -1 ? eps.length - 30 : nextIdx - 3));
      const fut = eps.findIndex((e) => !e.file && !e.watched_at && e.aired_at && e.aired_at > now);
      if (fut !== -1) to = Math.min(eps.length, Math.max(fut + 2, from + 1)); // the next 2 upcoming episodes
    }
    const visible = eps.slice(from, to);
    const later = eps.length - to;
    const dubs = !!(S.settings && S.settings.dubs && S.settings.dubs.enabled);
    const COLS = 5;
    const rows = visible.map((e) => {
      const n = e.number;
      const isNext = eps[nextIdx] === e;
      const cur = e.release ? { ...(relById[e.release.id] || {}), ...e.release } : null;
      const cands = rels.filter((r) => r.episode === n && r.status === 'candidate').sort((a, b) => (b.seeders || 0) - (a.seeders || 0));
      const aired = e.aired_at && e.aired_at <= now;
      const magnet = cur && cur.status === 'chosen' ? safeMagnet(cur.magnet) : '';
      const active = !e.file && cur && ['chosen', 'added', 'downloading'].includes(cur.status);
      const f = e.file;
      const open = st.openPicks.has(n);
      const relText = f ? (f.name || baseName(f.path)) : cur && cur.title ? cur.title : '';
      const relTip = f ? [f.path || f.name, [f.release_group, f.resolution, f.version > 1 ? 'v' + f.version : '', fSize(f.size)].filter(Boolean).join(' · ')].join('\n')
        : cur && cur.title ? [cur.title, cur.seeders != null ? `${fNum(cur.seeders)} seeders` : '', cur.status_note || ''].filter(Boolean).join('\n') : '';
      const chips = [isNext ? h`<span class="chip chip-accent">Next</span>` : '', ...epState(e, cur)].filter(Boolean).slice(0, 2);
      const items = [
        !f && cands.length && !active ? h`<button type="button" class="menu-item" data-act="add-release" data-release="${cands[0].id}">${ico('download')}Add best release <span class="menu-sub">${relDesc(cands[0])} · ${fNum(cands[0].seeders ?? 0)} seeders</span></button>` : '',
        active ? h`<button type="button" class="menu-item" data-act="skip-release" data-release="${cur.id}" data-show="${s.id}" data-ep="${n}" aria-label="Skip the current release for episode ${n} and stop auto-downloading it">${ico('x')}Skip this release</button>` : '',
        // SPEC-v1.3 2.3: the red Failed chip goes away (release -> skipped); auto flags stay as they are
        cur && cur.status === 'failed' ? h`<button type="button" class="menu-item" data-act="clear-failed-ep" data-ep="${n}" aria-label="Clear the failed release for episode ${n}">${ico('x')}Clear</button>` : '',
        !f && !active ? autoItem(e) : '',
        magnet ? h`<a class="menu-item" href="${magnet}" aria-label="Open magnet link for episode ${n}">${ico('magnet')}Open magnet link</a>` : '',
        f ? h`<button type="button" class="menu-item" data-act="reveal" data-file="${f.id}" aria-label="Show episode ${n} in folder">${ico('folder')}Reveal file</button>` : '',
      ].filter(Boolean);
      const row = h`<tr class="ep-row ${isNext ? 'is-next' : ''} ${e.watched_at ? 'is-watched' : ''}" id="ep-${n}">
        <td class="c-n num">${n}${e.abs_number != null ? h`<div class="sub dim">${e.abs_number}</div>` : ''}</td>
        <td class="c-aired">${e.aired_at ? h`<span title="${fFull(e.aired_at)}">${fAired(e.aired_at)}</span>` : h`<span class="dim">Unknown</span>`}${dubs && e.dub_aired_at ? h`<div class="sub dim" title="${fFull(e.dub_aired_at)}">Dub ${fAired(e.dub_aired_at)}</div>` : ''}</td>
        <td class="c-state"><div class="chips">${chips}</div></td>
        <td class="c-rel ${relText || cands.length ? '' : 'is-empty'}"><div class="rel-line">${relText ? h`<span class="ellipsis ${f ? 'mono' : ''}" title="${relTip}">${relText}</span>` : h`<span class="dim">—</span>`}
          ${cands.length ? h`<button type="button" class="btn btn-sm btn-ghost pick-toggle" data-pick-toggle="${n}" aria-expanded="${String(open)}" aria-controls="pick-row-${n}">Pick release<span class="pick-n">${fNum(cands.length)}</span>${ico('chevD')}</button>` : ''}</div></td>
        <td class="c-act"><div class="acts">
          ${f ? h`<button class="btn btn-sm ${e.watched_at ? '' : 'btn-primary'}" data-act="play" data-file="${f.id}" data-show="${s.id}" data-label="episode ${n}" aria-label="Play episode ${n}">${ico('play')}Play</button>` : ''}
          ${magnet ? h`<button class="btn btn-sm" data-act="copy-magnet" data-magnet="${magnet}" data-tip="Copy the magnet link, then paste it into your torrent app" aria-label="Copy the magnet link for episode ${n}">${ico('copy')}Magnet</button>` : ''}
          ${aired || f || e.watched_at ? h`<span class="acts-pair">${n > prefix ? upToHere(n) : ''}<button class="btn btn-sm btn-icon btn-watch ${e.watched_at ? 'is-on' : ''}" data-act="watched" data-show="${s.id}" data-ep="${n}" data-val="${e.watched_at ? 0 : 1}" aria-pressed="${e.watched_at ? 'true' : 'false'}" data-tip="${e.watched_at ? 'Watched (click to undo)' : 'Mark watched'}" aria-label="Watched: episode ${n}">${ico('check')}</button></span>` : ''}
          ${items.length ? menu(`ep-more-${n}`, `More for episode ${n}`, items) : h`<span class="menu-gap" aria-hidden="true"></span>`}
        </div></td>
      </tr>`;
      const pick = cands.length ? h`<tr class="pick-row" id="pick-row-${n}" ${open ? '' : raw('hidden')}><td colspan="${COLS}"><div class="pick">
          <label for="pick-${n}">Release for episode ${n}</label>
          <select class="select select-sm" id="pick-${n}">${cands.map((r) => h`<option value="${r.id}">${relDesc(r)} · ${fNum(r.seeders ?? 0)} seeders${r.size_bytes ? ' · ' + fSize(r.size_bytes) : ''}</option>`)}</select>
          <button class="btn btn-sm btn-primary" data-act="add-release" data-pick="pick-${n}" title="Use this release">${ico('download')}Add</button>
          <button class="btn btn-sm btn-ghost btn-icon" data-act="skip-release" data-pick="pick-${n}" data-tip="Never pick this release" aria-label="Skip the selected release for episode ${n}">${ico('skip')}</button>
        </div></td></tr>` : '';
      return h`${row}${pick}`;
    });
    // Files that vanished (present=false) stay in the database for the replace/v2 logic but are not shown.
    const files = (d.files || []).filter((f) => f.present !== false && f.present !== 0).sort((a, b) => (b.episode || 0) - (a.episode || 0));
    return h`<section class="surface panel">
      <div class="panel-head"><h2>Episodes<span class="count">${eps.length || ''}</span></h2>${stateLegend()}</div>
      ${eps.length ? h`<table class="ep-table">
        <thead><tr><th class="c-n">#</th><th class="c-aired">Aired</th><th class="c-state">State</th><th class="c-rel">Release</th><th class="c-act"><span class="sr-only">Actions</span></th></tr></thead>
        <tbody>${from > 0 ? h`<tr class="more-row"><td colspan="${COLS}"><button class="btn btn-sm" data-act="show-all">Show ${plural(from, 'earlier episode')}</button></td></tr>` : ''}${rows}${later > 0 ? h`<tr class="more-row"><td colspan="${COLS}"><button class="btn btn-sm" data-act="show-all">Show ${plural(later, 'later episode')}</button></td></tr>` : ''}</tbody>
      </table>` : emptyBox('calendar', 'No episodes yet', 'AniList has no airing schedule for this show yet. Refresh later.', '')}
    </section>
    <div class="two-col">
      <section class="surface panel">
        <div class="panel-head"><h2>Files<span class="count">${files.length || ''}</span></h2></div>
        ${files.length ? h`<ul class="list-rows">${files.map((f) => h`<li><div class="main"><span class="ellipsis mono" title="${f.path}">${f.name || baseName(f.path)}</span><span class="sub dim">Ep ${f.episode ?? '?'} · ${[f.release_group, f.resolution, f.version > 1 ? 'v' + f.version : '', fSize(f.size)].filter(Boolean).join(' · ')}${f.present === 0 || f.present === false ? ' · missing' : ''}</span></div>
          <div class="acts"><button class="btn btn-sm btn-icon" data-act="play" data-file="${f.id}" data-show="${s.id}" data-label="episode ${f.episode}" aria-label="Play episode ${f.episode}" data-tip="Play">${ico('play')}</button><button class="btn btn-sm btn-icon" data-act="reveal" data-file="${f.id}" aria-label="Show in folder" data-tip="Show in folder">${ico('folder')}</button></div></li>`)}</ul>`
          : h`<p class="muted small">No files matched yet. Files appear when the scanner finds them in one of your library folders.</p>`}
      </section>
      <section class="surface panel">
        <div class="panel-head"><h2>Releases<span class="count">${rels.length || ''}</span></h2></div>
        ${rels.length ? h`<ul class="list-rows">${rels.slice().sort((a, b) => (b.published_at || 0) - (a.published_at || 0)).slice(0, 30).map((r) => h`<li><div class="main"><span class="ellipsis" title="${r.title}">${r.title}</span><span class="sub dim">${r.is_batch ? 'Batch' : 'Ep ' + (r.episode ?? '?')} · ${fNum(r.seeders ?? 0)} seeders${r.size_bytes ? ' · ' + fSize(r.size_bytes) : ''} · ${fRel(r.published_at)}${r.status_note ? ' · ' + r.status_note : ''}</span></div>
          <span class="chip ${r.status === 'done' ? 'chip-ok' : ['added', 'downloading', 'chosen'].includes(r.status) ? 'chip-warn' : r.status === 'failed' ? 'chip-bad' : ''}">${pretty(r.status)}</span></li>`)}</ul>`
          : h`<p class="muted small">No nyaa.si results yet. The conveyor searches every ${S.settings && S.settings.conveyor ? S.settings.conveyor.poll_interval_min : 15} minutes while the show is on your Watching list.</p>`}
      </section>
    </div>`;
  }
  function showSettingsPanel(s, first) {
    const g = (S.settings && S.settings.conveyor) || {};
    const root = downloadDirOf(S.settings) || 'download folder';
    const sep = OS_WIN ? '\\' : '/';
    const overrides = ['search_query', 'group_order', 'resolution', 'folder'].filter((k) => s[k] != null && s[k] !== '' && !(Array.isArray(s[k]) && !s[k].length)).length + (s.episode_offset ? 1 : 0) + (s.auto_download != null ? 1 : 0) + (s.auto_from != null ? 1 : 0);
    // SPEC-v1.3 1.5: the manual offset is edited as "absolute numbering starts at" (offset + 1); empty = AniList's value
    const absPh = s.abs_offset != null ? String(s.abs_offset + 1) + ' (from AniList)' : 'unknown';
    const go = Array.isArray(s.group_order) ? s.group_order.join(', ') : typeof s.group_order === 'string' ? safeJsonList(s.group_order).join(', ') : '';
    const auto = s.auto_download == null ? '' : s.auto_download === true || s.auto_download === 1 ? 'on' : 'off';
    const defQuery = String(s.title_romaji || '').replace(/[^\p{L}\p{N}\s]/gu, ' ').replace(/\s+/g, ' ').trim();
    return h`<details class="surface panel ${A(first)}" id="show-settings" data-keep>
      <summary><span class="ico chev">${raw(svg('chevR'))}</span><h2 style="flex:1">Rules for this show</h2><span class="chip ${overrides ? 'chip-accent' : ''}" id="ovr-chip">${overrides ? plural(overrides, 'override') : 'Using global rules'}</span></summary>
      <form id="show-form" novalidate>
        <div class="form-grid">
          <div class="field"><label for="ss-q">nyaa search text</label><input class="input" id="ss-q" name="search_query" value="${s.search_query || ''}" placeholder="${defQuery}" spellcheck="false" aria-describedby="ss-q-h"><p class="help" id="ss-q-h">Sent to nyaa.si. Empty = the romaji title.</p></div>
          <div class="field"><label for="ss-abs">Absolute numbering starts at</label><input class="input" id="ss-abs" name="abs_start" type="number" min="1" step="1" inputmode="numeric" value="${s.episode_offset ? s.episode_offset + 1 : ''}" placeholder="${absPh}" aria-describedby="ss-abs-h ss-abs-e"><p class="help" id="ss-abs-h">The number this show's first episode has when the whole series is counted from episode 1. Groups like SubsPlease number sequels that way. Empty = use AniList's prequel chain. ABC counts earlier seasons of the same title; a series that restarts numbering per part (JoJo) stops at the part. Example: 171 means episode 3 is released as "173".</p><p class="err" id="ss-abs-e" aria-live="polite"></p></div>
          <div class="field"><label for="ss-go">Preferred groups</label><input class="input" id="ss-go" name="group_order" value="${go}" placeholder="${(g.group_order || []).join(', ')}" spellcheck="false" aria-describedby="ss-go-h"><p class="help" id="ss-go-h">Comma separated, best first. Empty = global order.</p></div>
          <div class="field"><label for="ss-res">Resolution</label><select class="select" id="ss-res" name="resolution" aria-describedby="ss-res-h">
            <option value="">Global (${g.resolution || '1080p'})</option>${['2160p', '1080p', '720p', '480p'].map((r) => h`<option value="${r}" ${s.resolution === r ? raw('selected') : ''}>${r}</option>`)}</select><p class="help" id="ss-res-h">Releases at other resolutions are ignored.</p></div>
          <div class="field"><label for="ss-auto">Auto download</label><select class="select" id="ss-auto" name="auto_download" aria-describedby="ss-auto-h">
            <option value="" ${auto === '' ? raw('selected') : ''}>Global (${g.enabled === false ? 'off' : 'on'})</option><option value="on" ${auto === 'on' ? raw('selected') : ''}>On</option><option value="off" ${auto === 'off' ? raw('selected') : ''}>Off</option></select><p class="help" id="ss-auto-h">Off = ABC tracks the show but never adds torrents for it.</p></div>
          <div class="field"><label for="ss-from">Auto-download from episode</label><input class="input" id="ss-from" name="auto_from" type="number" min="1" step="1" inputmode="numeric" value="${s.auto_from ?? ''}" placeholder="All episodes" aria-describedby="ss-from-h ss-from-e"><p class="help" id="ss-from-h">Episodes before this number are never downloaded by themselves.</p><p class="err" id="ss-from-e" aria-live="polite"></p></div>
          <div class="field"><label for="ss-folder">Save folder</label><input class="input" id="ss-folder" name="folder" value="${s.folder || ''}" placeholder="${root}${sep}${s.title_romaji || 'Show'}" spellcheck="false" aria-describedby="ss-folder-h"><p class="help" id="ss-folder-h">Where this show's episodes are saved. Empty = the download folder${sep}&lt;Show&gt;.</p></div>
        </div>
        <div class="form-actions"><span class="small muted" id="ss-msg" aria-live="polite" style="margin-right:auto"></span><button class="btn" type="reset">Reset</button><button class="btn btn-primary" type="submit">Save rules</button></div>
      </form>
    </details>`;
  }
  function safeJsonList(s) { try { const v = JSON.parse(s); return Array.isArray(v) ? v : []; } catch { return []; } }
  function bindShowSettings(root, show, id) {
    const form = $('#show-form', root);
    if (!form) return;
    // SPEC-v1.3 1.5: "Absolute numbering starts at" = episode_offset + 1; empty = 0 (use AniList's prequel chain).
    const off = form.elements.abs_start;
    const offErr = $('#ss-abs-e', root);
    const checkOff = () => { const v = off.value.trim(); const ok = v === '' || (/^\d+$/.test(v) && Number(v) >= 1); offErr.textContent = ok ? '' : 'Whole number 1 or more, or empty for automatic.'; off.setAttribute('aria-invalid', String(!ok)); return ok; };
    off.addEventListener('input', checkOff);
    const from = form.elements.auto_from;
    const fromErr = $('#ss-from-e', root);
    const checkFrom = () => { const v = from.value.trim(); const ok = v === '' || (/^\d+$/.test(v) && Number(v) >= 1); fromErr.textContent = ok ? '' : 'Episode number (1 or more), or empty for all.'; from.setAttribute('aria-invalid', String(!ok)); return ok; };
    from.addEventListener('input', checkFrom);
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      if (!checkOff()) { off.focus(); return; }
      if (!checkFrom()) { from.focus(); return; }
      const el = form.elements;
      const groups = el.group_order.value.split(',').map((x) => x.trim()).filter(Boolean);
      const patch = {
        search_query: el.search_query.value.trim() || null,
        episode_offset: off.value.trim() === '' ? 0 : Number(off.value.trim()) - 1,
        group_order: groups.length ? groups : null,
        resolution: el.resolution.value || null,
        auto_download: el.auto_download.value === '' ? null : el.auto_download.value === 'on',
        folder: el.folder.value.trim() || null,
        auto_from: from.value.trim() === '' ? null : Number(from.value.trim()),
      };
      const msg = $('#ss-msg', root);
      const btn = form.querySelector('[type=submit]');
      btn.classList.add('is-busy');
      try {
        await API.patchShow(id, patch);
        msg.textContent = 'Saved.';
        msg.style.color = 'var(--ok)';
        const n = ['search_query', 'group_order', 'resolution', 'folder', 'auto_download', 'auto_from'].filter((k) => patch[k] != null).length + (patch.episode_offset ? 1 : 0);
        const chip = $('#ovr-chip', root);
        chip.textContent = n ? plural(n, 'override') : 'Using global rules';
        chip.classList.toggle('chip-accent', !!n);
        scheduleRefresh(0); // auto_from changes the "Not auto" chips and the Download all missing count
      } catch (err) {
        msg.textContent = err.message;
        msg.style.color = 'var(--bad)';
      } finally { btn.classList.remove('is-busy'); }
    });
    form.addEventListener('reset', () => { offErr.textContent = ''; off.removeAttribute('aria-invalid'); fromErr.textContent = ''; from.removeAttribute('aria-invalid'); $('#ss-msg', root).textContent = ''; });
  }

  // ---------- Add  #/add
  /** opts.embedded: render without the page header (setup guide step 5 reuses this view). */
  Views.add = (m, params, opts = {}) => {
    const now = seasonOf();
    const st = { season: now.season, year: now.year, q: (params && params.get('q')) || '', results: null, searching: false, seasonList: null, seasonErr: null, accounts: [] };
    const tracked = (mItem) => {
      const aid = mItem.anilist_id ?? mItem.id;
      if (mItem.tracked) return true;
      return Object.values(S.showsById).some((s) => Number(s.anilist_id) === Number(aid));
    };
    const trackedShowId = (aid) => { const s = Object.values(S.showsById).find((x) => Number(x.anilist_id) === Number(aid)); return s ? s.id : null; };
    // SPEC-v1.2 5: the 3.3 poster card with a Track button in the footer (shows.js).
    const mediaCard = (mItem) => ABC.Shows.mediaCard(mItem, { tracked: tracked(mItem), showId: trackedShowId(mItem.anilist_id ?? mItem.id) });
    const resultsHtml = () => {
      if (st.searching) return h`<div class="pgrid media-grid">${Array.from({ length: 5 }, () => skel(300))}</div>`;
      if (st.results === null) return h`<p class="muted small">Type at least 2 letters. Romaji, English and Japanese titles all work.</p>`;
      if (st.results instanceof Error) return h`<p class="note">Search failed: ${st.results.message}</p>`;
      if (!st.results.length) return emptyBox('search', `Nothing on AniList for "${st.q}"`, 'Check the spelling or try the romaji title.', '');
      return h`<div class="pgrid media-grid">${st.results.map(mediaCard)}</div>`;
    };
    const seasonHtml = () => {
      if (st.seasonErr) return h`<p class="note">Season list failed: ${st.seasonErr.message}</p>`;
      if (st.seasonList === null) return h`<div class="pgrid media-grid">${Array.from({ length: 6 }, () => skel(300))}</div>`;
      if (!st.seasonList.length) return emptyBox('calendar', 'No shows listed for this season', 'AniList has nothing for it yet.', '');
      return h`<div class="pgrid media-grid">${st.seasonList.map(mediaCard)}</div>`;
    };
    const loadSeason = async (root) => {
      st.seasonList = null; st.seasonErr = null;
      const box = $('#season-grid', root);
      if (box) box.innerHTML = String(seasonHtml());
      $('#season-label', root).textContent = `${pretty(st.season)} ${st.year}`;
      try { st.seasonList = await API.season(st.season, st.year); } catch (err) { st.seasonErr = err; }
      if ($('#season-grid', root)) { $('#season-grid', root).innerHTML = String(seasonHtml()); }
    };
    const malConnected = () => st.accounts.some((a) => a.service === 'mal');
    return {
      title: 'Add shows', nav: 'add',
      async load() {
        const [shows, accounts] = await Promise.all([soft(API.shows(), []), soft(API.accounts(), [])]);
        indexShows(shows.v);
        st.accounts = accounts.v || [];
        return st;
      },
      render(_d, first) {
        return h`<div class="page">
          ${opts.embedded ? '' : h`<header class="page-head glass ${A(first)}"><h1>Add shows</h1></header>`}
          <div class="add-top">
            <section class="surface panel ${A(first)}" aria-labelledby="search-h">
              <div class="panel-head"><h2 id="search-h">Search AniList</h2></div>
              <div class="search" style="margin-bottom:16px" data-keep><label class="sr-only" for="add-q">Search AniList</label>${ico('search')}<input class="input" id="add-q" type="search" placeholder="Frieren, One Piece, 葬送のフリーレン…" autocomplete="off" value="${st.q}"></div>
              <div id="search-results" aria-live="polite">${resultsHtml()}</div>
            </section>
            <section class="surface panel ${A(first)}" aria-labelledby="import-h">
              <div class="panel-head"><h2 id="import-h">Import a list</h2></div>
              <div class="import-box" data-keep>
                <form class="field" id="imp-al" novalidate>
                  <label for="imp-al-user">AniList username</label>
                  <div class="row"><input class="input grow" id="imp-al-user" name="user" autocomplete="off" spellcheck="false" placeholder="your AniList name" aria-describedby="imp-al-h"><button class="btn btn-primary" type="submit">${ico('download')}Import</button></div>
                  <p class="help" id="imp-al-h">Reads your public list. No login needed. Existing shows are updated, not duplicated.</p>
                </form>
                <div class="field">
                  <span class="label">MyAnimeList</span>
                  <div class="row">${malConnected() ? h`<button class="btn btn-primary" data-act="import-mal">${ico('download')}Import MAL list</button>` : h`<button class="btn" data-act="auth-start" data-service="mal">${ico('user')}Log in to MAL</button>`}</div>
                  <p class="help">${malConnected() ? 'Imports from the connected MyAnimeList account.' : h`Needs a MAL client ID first: <a href="#/settings?section=push">Settings › Accounts</a>.`}</p>
                </div>
              </div>
            </section>
          </div>
          <section class="surface panel ${A(first)}" aria-labelledby="season-h">
            <div class="panel-head">
              <h2 id="season-h">This season: <span id="season-label">${pretty(st.season)} ${st.year}</span></h2>
              <div class="season-nav">
                <button class="btn btn-sm btn-icon" data-act="season-prev" aria-label="Previous season">${ico('chevL')}</button>
                <button class="btn btn-sm" data-act="season-now">Now</button>
                <button class="btn btn-sm btn-icon" data-act="season-next" aria-label="Next season">${ico('chevR')}</button>
              </div>
            </div>
            <div id="season-grid">${seasonHtml()}</div>
          </section>
        </div>`;
      },
      mount(root) {
        if (S.last) paintFromLast();
        const qIn = $('#add-q', root);
        let seq = 0;
        const runSearch = async () => {
          st.q = qIn.value.trim();
          const my = ++seq;
          if (st.q.length < 2) { st.results = null; st.searching = false; $('#search-results', root).innerHTML = String(resultsHtml()); return; }
          st.searching = true; $('#search-results', root).innerHTML = String(resultsHtml());
          let r;
          try { r = await API.search(st.q); } catch (err) { r = err; }
          if (my !== seq) return;
          st.searching = false; st.results = r;
          $('#search-results', root).innerHTML = String(resultsHtml());
        };
        qIn.addEventListener('input', debounce(runSearch, 350));
        // Global search "Search AniList for ..." lands here as #/add?q=... (SPEC-v1.2 2).
        if (st.q.length >= 2 && st.results === null) runSearch();
        $('#imp-al', root).addEventListener('submit', async (e) => {
          e.preventDefault();
          const user = e.target.elements.user.value.trim();
          if (!user) { e.target.elements.user.focus(); toast('Type your AniList username first', 'info'); return; }
          const btn = e.target.querySelector('button');
          btn.classList.add('is-busy');
          try { const r = await API.importAniList(user); toast(`AniList import: ${r.added ?? 0} added, ${r.updated ?? 0} updated`, 'ok'); await this.refresh(root); } catch (err) { toast(err.message, 'bad'); } finally { btn.classList.remove('is-busy'); }
        });
        loadSeason(root);
        this._root = root;
      },
      actions: {
        'season-prev': () => { let i = SEASONS.indexOf(st.season) - 1; if (i < 0) { i = 3; st.year--; } st.season = SEASONS[i]; loadSeason($('#view')); },
        'season-next': () => { let i = SEASONS.indexOf(st.season) + 1; if (i > 3) { i = 0; st.year++; } st.season = SEASONS[i]; loadSeason($('#view')); },
        'season-now': () => { const n = seasonOf(); st.season = n.season; st.year = n.year; loadSeason($('#view')); },
        'import-mal': async () => { const r = await API.importMal(); toast(`MyAnimeList import: ${r.added ?? 0} added, ${r.updated ?? 0} updated`, 'ok'); },
      },
      async refresh(root) {
        const shows = await API.shows(); indexShows(shows);
        if (st.seasonList) $('#season-grid', root).innerHTML = String(seasonHtml());
        if (Array.isArray(st.results)) $('#search-results', root).innerHTML = String(resultsHtml());
      },
      refreshOn: (ev) => ev.kind === 'show.added' || ev.kind === 'show.removed',
    };
  };

  // ---------- Calendar  #/calendar
  Views.calendar = (m, params) => {
    const st = { offset: Number(params.get('week')) || 0 };
    return {
      title: 'Calendar', nav: 'calendar',
      async load() {
        const ws = weekStart(st.offset);
        const from = Math.floor(ws / 1000); const to = from + 7 * 86400;
        const [items, shows, settings] = await Promise.all([API.calendar(from, to), soft(API.shows(), []), S.settings ? Promise.resolve(S.settings) : soft(API.settings(), null).then((r) => r.v)]);
        if (settings) S.settings = settings;
        indexShows(shows.v);
        return { items: items || [], ws, from, to };
      },
      render(D, first) {
        const days = weekDays(D.ws);
        const end = days[6];
        const today = dayKey(new Date());
        const chips = calChips(D.items).filter((c) => c.ts >= D.from && c.ts < D.to);
        const rowsMap = new Map();
        for (const c of chips) { if (!rowsMap.has(c.it.show_id)) rowsMap.set(c.it.show_id, []); rowsMap.get(c.it.show_id).push(c); }
        const rows = [...rowsMap.entries()].sort((a, b) => a[1][0].ts - b[1][0].ts);
        const label = `${F.md.format(D.ws)} – ${F.mdy.format(end)}`;
        const byDay = {};
        for (const c of chips) (byDay[dayKey(toDate(c.ts))] ||= []).push(c);
        return h`<div class="page">
          <header class="page-head glass ${A(first)}">
            <h1>Calendar <span class="muted" style="font-weight:500;font-size:16px;margin-left:6px">${label}</span></h1>
            <div class="actions">
              <button class="btn btn-sm btn-icon" data-act="week-prev" aria-label="Previous week">${ico('chevL')}</button>
              <button class="btn btn-sm" data-act="week-now" ${st.offset === 0 ? raw('aria-pressed="true"') : ''}>This week</button>
              <button class="btn btn-sm btn-icon" data-act="week-next" aria-label="Next week">${ico('chevR')}</button>
            </div>
          </header>
          <section class="surface panel ${A(first)}" aria-label="Week of ${label}">
            <div class="panel-head"><h2>${rows.length ? plural(rows.length, 'show') + ' this week' : 'This week'}</h2>${stateLegend()}</div>
            ${dubsOn() ? h`<p class="small muted" style="margin:-6px 0 12px">Dub air times shown as separate "dub" entries (animeschedule.net).</p>` : ''}
            ${!rows.length ? emptyBox('calendar', 'Nothing airs this week', 'Only shows on your list appear here. Try the next week, or add shows.', h`<a class="btn" href="#/add">${ico('plus')}Add shows</a>`) : h`
            <div class="cal-wrap" role="region" aria-label="Week grid" tabindex="0">
              <div class="cal" role="table" aria-label="Episodes by show and day">
                <div role="row" style="display:contents">
                  <div class="cal-h" role="columnheader">Show</div>
                  ${days.map((d) => h`<div class="cal-h ${dayKey(d) === today ? 'is-today is-today-col' : ''}" role="columnheader">${F.wd.format(d)}<b>${F.day.format(d)}</b></div>`)}
                </div>
                ${rows.map(([sid, list]) => {
                  const s = S.showsById[sid] || {};
                  return h`<div role="row" style="display:contents">
                    <div role="rowheader"><a class="cal-show" href="#/show/${sid}"><img src="${safeUrl(s.cover_url)}" alt="" loading="lazy"><span class="clamp2">${showTitle(s.id ? s : list[0].it)}</span></a></div>
                    ${days.map((d) => { const k = dayKey(d); const cs = list.filter((c) => dayKey(toDate(c.ts)) === k); return h`<div class="cell ${k === today ? 'is-today-col' : ''}" role="cell">${cs.map((c) => epChip(c, false))}</div>`; })}
                  </div>`;
                })}
              </div>
            </div>
            <div class="agenda">
              ${days.map((d) => { const k = dayKey(d); const cs = byDay[k] || []; return h`<div class="agenda-day ${k === today ? 'is-today' : ''}"><h3>${F.wd.format(d)} <span class="muted small">${F.md.format(d)}</span></h3>${cs.length ? h`<div class="chips">${cs.map(weekEntry)}</div>` : h`<p class="small dim">Nothing airs</p>`}</div>`; })}
            </div>`}
          </section>
        </div>`;
      },
      mount() { if (S.last) paintFromLast(); },
      actions: {
        'week-prev': () => { history.replaceState(null, '', `#/calendar?week=${st.offset - 1}`); st.offset--; rerender(); },
        'week-next': () => { history.replaceState(null, '', `#/calendar?week=${st.offset + 1}`); st.offset++; rerender(); },
        'week-now': () => { history.replaceState(null, '', '#/calendar'); st.offset = 0; rerender(); },
      },
      refreshOn: (ev) => /^(airing|file|release|download|episode|show)\./.test(ev.kind),
    };
  };

  // ---------- Settings  #/settings
  const winOr = (w, o) => (OS_WIN ? w : o);
  /** One entry per field of the Settings struct (SPEC section 3). `path` = JSON path.
      TODO(spec): JSON tags are only partly given in SPEC 3; snake_case is assumed for every field.
      Change a name here and nowhere else if the Go struct uses a different tag. */
  const SETTINGS_SCHEMA = [
    // v1.1: library.root / library.extra are replaced by the folder manager (folders.js, `top: 'folders'`),
    // which talks to /api/folders directly; library.folders is kept in sync in the draft so Save never overwrites it.
    { id: 'library', title: 'Library', desc: 'Where episodes are saved and which folders are scanned.', top: 'folders', fields: [
      { path: 'library.scan_interval_min', label: 'Full rescan every (minutes)', type: 'int', min: 1, help: 'New files are also picked up live; this is the safety net.' },
      { path: 'library.video_exts', label: 'Video file types', type: 'csv', placeholder: 'mkv, mp4', help: 'Comma separated, without dots.' },
      { path: 'open.folder_on_start', label: 'Open the download folder when ABC starts', type: 'bool', span: 2, help: 'Opens it in your file manager each time abc.exe starts.' },
    ] },
    { id: 'downloads', title: 'Downloads', desc: 'Which app downloads the episodes ABC finds.', fields: [
      { path: 'downloader.mode', label: 'How releases reach your torrent app', type: 'mode', span: 2 },
      // Rendered inside the "watch folder" card by the mode field; listed here so input and Save handle it.
      { path: 'downloader.torrent_dir', label: 'Watch folder', type: 'dir', inCard: true },
    ] },
    { id: 'qbit', title: 'qBittorrent', desc: 'Used when Downloads is set to qBittorrent.', test: 'qbit', fields: [
      { path: 'qbit.url', label: 'Web UI address', type: 'url', placeholder: 'http://127.0.0.1:8080', help: 'qBittorrent › Tools › Options › Web UI. Address and port shown there.' },
      { path: 'qbit.category', label: 'Category', type: 'text', placeholder: 'abc', help: 'Torrents added by ABC get this category in qBittorrent.' },
      { path: 'qbit.username', label: 'Username', type: 'text', autocomplete: 'off', help: 'Same as in the Web UI tab. Empty when "Bypass authentication for clients on localhost" is ticked.' },
      { path: 'qbit.password', label: 'Password', type: 'password', help: 'Stored in ABC\'s local database.' },
      { path: 'qbit.save_path_style', label: 'Save into', type: 'select', options: [['show_folder', 'Download folder/<Show>/ (one folder per show)'], ['root', 'Download folder/ (all in one folder)']], help: 'Folder qBittorrent saves each episode to.' },
    ] },
    { id: 'conveyor', title: 'Conveyor', desc: 'How releases are found on nyaa.si and which one is picked.', fields: [
      { path: 'conveyor.enabled', label: 'Download new episodes automatically', type: 'bool', span: 2, help: 'Off = ABC still tracks airing and files, but adds nothing by itself.' },
      { path: 'conveyor.poll_interval_min', label: 'Check nyaa.si every (minutes)', type: 'int', min: 1, help: 'One search per show per check, 2 seconds apart.' },
      { path: 'conveyor.resolution', label: 'Resolution', type: 'select', options: [['2160p', '2160p'], ['1080p', '1080p'], ['720p', '720p'], ['480p', '480p']], help: 'Releases at other resolutions are ignored.' },
      { path: 'conveyor.group_order', label: 'Preferred release groups', type: 'list', placeholder: 'SubsPlease\nErai-raws', help: 'One group per line, best first. Groups known for small HEVC 1080p encodes: ASW, Judas, EMBER. Add them here if you want small files first.' },
      { path: 'conveyor.size_preference', label: 'When releases are otherwise equal', type: 'select', options: [['small', 'Take the smallest file'], ['any', 'Take the most seeded']], help: 'Preferred groups and resolution still come first. Releases of unknown size go last.' },
      { path: 'conveyor.fallback_hours', label: 'Wait for a preferred group (hours)', type: 'float', min: 0, step: 0.5, help: 'After this many hours since airing, the best other release is taken.' },
      { path: 'conveyor.min_seeders', label: 'Minimum seeders', type: 'int', min: 0, help: 'Releases with fewer seeders are ignored.' },
      { path: 'conveyor.lookback_days', label: 'Auto-download episodes that aired in the last (days)', type: 'int', min: 1, help: 'Older episodes wait for you: press Add on the episode, or Download all missing on the show.' },
      { path: 'conveyor.max_release_gib', label: 'Skip releases bigger than (GiB)', type: 'float', min: 0, step: 0.5, help: '0 = no limit. Protects against mislabeled batches and remuxes.' },
      { path: 'conveyor.min_free_gib', label: 'Keep this much free on the download drive (GiB)', type: 'float', min: 0, step: 0.5, help: '0 = off. Only automatic downloads wait; Add on an episode still works.' },
      { path: 'conveyor.skip_batches_while_airing', label: 'Skip batches while a show is airing', type: 'bool', help: 'Batches are whole-season packs; they usually come after the finale.' },
      { path: 'conveyor.auto_replace_v2', label: 'Replace with fixed v2 releases', type: 'bool', help: 'Same group only. The old file is deleted after the new one finishes, and only inside the download folder.' },
      { path: 'conveyor.nyaa_base', label: 'nyaa address', type: 'url', placeholder: 'https://nyaa.si', help: 'Change only to use a mirror.' },
      { path: 'conveyor.nyaa_category', label: 'nyaa category', type: 'text', placeholder: '1_2', help: '1_2 = Anime, English-translated.' },
    ] },
    { id: 'watched', title: 'Watched detection', desc: 'When an episode counts as watched.', fields: [
      { path: 'watched.mode', label: 'Mark an episode watched', type: 'select', span: 2, options: [['fraction', 'After the player was open for part of the runtime'], ['on_open', 'As soon as it opens in a player'], ['manual', 'Only when I tick it']], help: 'Detection reads player window titles; nothing is sent anywhere.' },
      { path: 'watched.fraction', label: 'Part of the runtime (%)', type: 'percent', min: 5, max: 100, enabledIf: (d) => getPath(d, 'watched.mode') === 'fraction', help: '50 = the player stayed open for half the episode.' },
      { path: 'watched.default_runtime_min', label: 'Runtime when unknown (minutes)', type: 'int', min: 1, enabledIf: (d) => getPath(d, 'watched.mode') === 'fraction', help: 'Used when AniList has no episode length.' },
      { path: 'watched.poll_sec', label: 'Check the player every (seconds)', type: 'int', min: 1, enabledIf: (d) => getPath(d, 'watched.mode') !== 'manual', help: 'Lower = more exact, slightly more CPU.' },
      { path: 'watched.player_titles', label: 'Player window titles', type: 'list', enabledIf: (d) => getPath(d, 'watched.mode') !== 'manual', help: 'A window whose title contains one of these counts as a player. One per line.' },
    ] },
    { id: 'open', title: 'Playback', desc: 'What the Play button opens.', fields: [
      { path: 'open.player_path', label: 'Player program', type: 'text', span: 2, placeholder: winOr('C:\\Program Files\\mpv\\mpv.exe', '/usr/bin/mpv'), help: 'Empty = the system\'s default app for video files.' },
    ] },
    { id: 'titles', title: 'Titles', desc: 'How show names are written.', fields: [
      { path: 'titles.language', label: 'Show titles in', type: 'select', options: [['romaji', 'Romaji (Sousou no Frieren)'], ['english', 'English (Frieren: Beyond Journey\'s End)']], help: 'Falls back to romaji when a show has no English title.' },
    ] },
    { id: 'dubs', title: 'Dubs', desc: 'English dub air times from animeschedule.net. Off by default; nothing is fetched while off.', fields: [
      { path: 'dubs.enabled', label: 'Track dub air times', type: 'bool', help: 'Needs a free animeschedule.net API token.' },
      { path: 'dubs.show_in_calendar', label: 'Show dub times in the calendar', type: 'bool', enabledIf: (d) => !!getPath(d, 'dubs.enabled'), help: 'Dubs appear as separate "dub" entries.' },
      { path: 'dubs.token', label: 'animeschedule.net token', type: 'password', span: 2, enabledIf: (d) => !!getPath(d, 'dubs.enabled'), help: 'From your animeschedule.net account page (API tokens).' },
    ] },
    { id: 'push', title: 'Accounts and sync', desc: 'Send watched progress to AniList or MyAnimeList.', accounts: true, fields: [
      { path: 'push.anilist', label: 'Push progress to AniList', type: 'bool', help: 'After an episode is marked watched. Needs a connected account.' },
      { path: 'push.mal', label: 'Push progress to MyAnimeList', type: 'bool', help: 'After an episode is marked watched. Needs a connected account.' },
      // TODO(spec): SPEC 4 says the user pastes AniList client id + secret and a MAL client id in Settings,
      // but the Settings struct in SPEC 3 has no field for them. Paths below are a guess.
      { path: 'push.anilist_client_id', label: 'AniList client ID', type: 'text', autocomplete: 'off', help: 'From anilist.co/settings/developer (steps below).' },
      { path: 'push.anilist_client_secret', label: 'AniList client secret', type: 'password', help: 'Shown next to the client ID.' },
      { path: 'push.mal_client_id', label: 'MyAnimeList client ID', type: 'text', autocomplete: 'off', span: 2, help: 'From myanimelist.net/apiconfig (steps below).' },
    ] },
    { id: 'ui', title: 'Appearance', desc: 'Theme and motion.', fields: [
      { path: 'ui.theme', label: 'Theme', type: 'theme', span: 2 },
      { path: 'ui.accent', label: 'Accent colour', type: 'color', help: 'Empty = the colour of the show you touched last.' },
      { path: 'ui.reduced_motion', label: 'Reduce motion', type: 'bool', help: 'Turns off fades, slides and the card lift. Your system setting is respected either way.' },
    ] },
    { id: 'notify', title: 'Notifications', desc: 'Planned for a later version.', fields: [
      { path: 'notify.toast', label: 'Desktop notification when an episode lands (planned)', type: 'bool', span: 2, help: 'No effect in v1.' },
    ] },
    { id: 'server', title: 'Server', desc: 'The local web server this page comes from.', top: 'server', fields: [
      { path: 'server.port', label: 'Port', type: 'int', min: 1024, max: 65535, help: 'ABC listens on 127.0.0.1 only. A new port takes effect after restarting ABC.' },
    ] },
  ];
  const FIELDS = SETTINGS_SCHEMA.flatMap((s) => s.fields);
  const fid = (f) => 'f-' + f.path.replace(/\./g, '-');
  function isUrl(s) { try { const u = new URL(s); return u.protocol === 'http:' || u.protocol === 'https:'; } catch { return false; } }
  /** Read one control into a value, or an error message. */
  function readField(el, f) {
    const s = typeof el.value === 'string' ? el.value : '';
    switch (f.type) {
      case 'bool': return { v: el.checked };
      case 'int': {
        if (s.trim() === '') return { err: 'Required.' };
        const n = Number(s);
        if (!Number.isInteger(n)) return { err: 'Whole number, please.' };
        return rangeCheck(f, n, n);
      }
      case 'float': {
        if (s.trim() === '') return { err: 'Required.' };
        const n = Number(s);
        if (!Number.isFinite(n)) return { err: 'Number, please.' };
        return rangeCheck(f, n, n);
      }
      case 'percent': {
        if (s.trim() === '') return { err: 'Required.' };
        const n = Number(s);
        if (!Number.isFinite(n)) return { err: 'Number, please.' };
        return rangeCheck(f, n, Math.round(n * 10) / 1000);
      }
      case 'list': return { v: s.split(/\r?\n/).map((x) => x.trim()).filter(Boolean) };
      case 'csv': return { v: s.split(',').map((x) => x.trim().replace(/^\./, '')).filter(Boolean) };
      case 'url': { const t = s.trim(); if (!t) return { err: 'Required.' }; return isUrl(t) ? { v: t } : { err: 'Not a web address. Example: http://127.0.0.1:8080' }; }
      case 'color': { const t = s.trim(); return !t || isHex(t) ? { v: t } : { err: 'Hex colour like #8b9cff, or empty.' }; }
      case 'theme': { const c = el.closest('fieldset').querySelector('input:checked'); return { v: c ? c.value : 'glass' }; }
      case 'mode': { const g = el.closest('[role=radiogroup]'); const c = g && g.querySelector('input[type=radio]:checked'); return c ? { v: c.value } : { err: 'Pick one.' }; }
      case 'dir': { const t = s.trim(); return !t || isAbsPath(t) ? { v: t } : { err: `Type the full path, for example ${winOr('D:\\Torrents\\watch', '/home/you/torrents')}.` }; }
      case 'text': case 'password': return { v: f.type === 'password' ? s : s.trim() };
      default: return { v: s };
    }
  }
  function rangeCheck(f, shown, v) {
    if (f.min != null && shown < f.min) return { err: `At least ${f.min}.` };
    if (f.max != null && shown > f.max) return { err: `At most ${f.max}.` };
    return { v };
  }
  function fieldHtml(f, draft) {
    const id = fid(f);
    const v = getPath(draft, f.path);
    const dis = f.enabledIf && !f.enabledIf(draft);
    const help = f.help ? h`<p class="help" id="${id}-h">${f.help}</p>` : '';
    const err = h`<p class="err" id="${id}-e" aria-live="polite"></p>`;
    const common = raw(String(h`id="${id}" data-path="${f.path}" aria-describedby="${id}-h ${id}-e"`) + (dis ? ' disabled' : ''));
    const span = f.span ? 'span2' : '';
    if (f.inCard) return '';
    switch (f.type) {
      case 'mode': // the four downloader cards live in setup.js (shared with the setup guide)
        return h`<div class="field ${span}">${ABC.Setup ? ABC.Setup.modeCards({ settings: true, draft, downloadDir: downloadDirOf(draft) }) : ''}${err}</div>`;
      case 'bool':
        return h`<div class="field field-inline ${span}"><div class="field-text"><label for="${id}">${f.label}</label>${help}${err}</div><input type="checkbox" class="switch" role="switch" ${common} ${v ? raw('checked') : ''}></div>`;
      case 'select': {
        const opts = f.options.some(([k]) => k === v) || v == null || v === '' ? f.options : [...f.options, [v, `${v} (custom)`]];
        return h`<div class="field ${span}"><label for="${id}">${f.label}</label><select class="select" ${common}>${opts.map(([k, l]) => h`<option value="${k}" ${k === v ? raw('selected') : ''}>${l}</option>`)}</select>${help}${err}</div>`;
      }
      case 'list':
        return h`<div class="field ${span}"><label for="${id}">${f.label}</label><textarea class="textarea" rows="${clamp((Array.isArray(v) ? v.length : 0) + 1, 3, 8)}" spellcheck="false" placeholder="${f.placeholder || ''}" ${common}>${(Array.isArray(v) ? v : []).join('\n')}</textarea>${help}${err}</div>`;
      case 'csv':
        return h`<div class="field ${span}"><label for="${id}">${f.label}</label><input class="input" spellcheck="false" placeholder="${f.placeholder || ''}" value="${(Array.isArray(v) ? v : []).join(', ')}" ${common}>${help}${err}</div>`;
      case 'int': case 'float':
        return h`<div class="field ${span}"><label for="${id}">${f.label}</label><input class="input" type="number" inputmode="${f.type === 'int' ? 'numeric' : 'decimal'}" step="${f.step || 1}" ${f.min != null ? raw(`min="${f.min}"`) : ''} ${f.max != null ? raw(`max="${f.max}"`) : ''} value="${v ?? ''}" ${common}>${help}${err}</div>`;
      case 'percent':
        return h`<div class="field ${span}"><label for="${id}">${f.label}</label><input class="input" type="number" inputmode="decimal" step="1" min="${f.min}" max="${f.max}" value="${v == null ? '' : Math.round(v * 1000) / 10}" ${common}>${help}${err}</div>`;
      case 'password':
        return h`<div class="field ${span}"><label for="${id}">${f.label}</label><input class="input" type="password" autocomplete="new-password" spellcheck="false" value="${v ?? ''}" ${common}>${help}${err}</div>`;
      case 'color':
        return h`<div class="field ${span}"><label for="${id}">${f.label}</label><div class="row"><input class="input grow" spellcheck="false" placeholder="from the show" value="${v ?? ''}" ${common}><input type="color" class="input" style="width:52px;padding:4px" aria-label="Pick accent colour" data-color-for="${id}" value="${isHex(v) && v.length === 7 ? v : '#8b9cff'}"></div>${help}${err}</div>`;
      case 'theme': {
        const cur = v || 'glass';
        const opt = (k, name, sw, disabled, note) => h`<label class="theme-opt ${disabled ? 'is-disabled' : ''}"><input type="radio" name="theme" value="${k}" data-theme-for="${id}" ${cur === k ? raw('checked') : ''} ${disabled ? raw('disabled') : ''}><span class="sw ${sw}"></span><span class="name">${name} ${note ? h`<small>${note}</small>` : ''}</span></label>`;
        return h`<fieldset class="field ${span}" style="border:0;padding:0;margin:0" id="${id}" data-path="${f.path}"><legend class="label" style="margin-bottom:8px;font-size:13px;font-weight:600">${f.label}</legend><div class="theme-opts">
          ${opt('glass', 'Glass', 'glass-sw', false, '')}${opt('floating', 'Floating', 'float-sw', true, '(planned)')}${opt('light', 'Light', 'light-sw', true, '(planned)')}</div>${err}</fieldset>`;
      }
      default:
        return h`<div class="field ${span}"><label for="${id}">${f.label}</label><input class="input" type="text" spellcheck="false" ${f.type === 'url' ? raw('inputmode="url"') : ''} autocomplete="${f.autocomplete || 'off'}" placeholder="${f.placeholder || ''}" value="${v ?? ''}" ${common}>${help}${err}</div>`;
    }
  }
  Views.settings = (m, params) => {
    const st = { saved: null, draft: null, accounts: [], errors: {}, tests: {}, state: null };
    const dirty = () => JSON.stringify(st.draft) !== JSON.stringify(st.saved);
    const port = () => getPath(st.draft, 'server.port') || 7700;
    const accountsHtml = () => {
      const by = Object.fromEntries((st.accounts || []).map((a) => [a.service, a]));
      const row = (svc, name, steps) => {
        const a = by[svc];
        return h`<div class="acct">${ico('user')}<div class="who"><b>${name}</b><div class="small muted">${a ? h`Connected as <b>${a.user_name || 'unknown'}</b>${a.push_enabled ? ' · push on' : ''}` : 'Not connected'}</div></div>
          ${a ? h`<button class="btn btn-sm btn-ghost" data-act="acct-delete" data-service="${svc}">Disconnect</button>` : h`<button class="btn btn-sm" data-act="auth-start" data-service="${svc}">${ico('plug')}Connect</button>`}</div>${steps}`;
      };
      const base = `http://127.0.0.1:${port()}`;
      return h`<div class="span2" style="display:grid;gap:12px">
        ${row('anilist', 'AniList', h`<details><summary class="small muted" style="cursor:pointer">How to get an AniList client ID</summary><ol class="steps" style="margin-top:8px"><li>Open <a href="https://anilist.co/settings/developer" target="_blank" rel="noopener noreferrer">anilist.co/settings/developer</a> and press <b>Create New Client</b>.</li><li>Name: <b>ABC</b>. Redirect URL: <code>${base}/api/auth/anilist/callback</code></li><li>Copy the client ID and secret into the fields above, press <b>Save</b>, then <b>Connect</b>.</li></ol></details>`)}
        ${row('mal', 'MyAnimeList', h`<details><summary class="small muted" style="cursor:pointer">How to get a MyAnimeList client ID</summary><ol class="steps" style="margin-top:8px"><li>Open <a href="https://myanimelist.net/apiconfig" target="_blank" rel="noopener noreferrer">myanimelist.net/apiconfig</a> and press <b>Create ID</b>.</li><li>App type: <b>web</b>. App redirect URL: <code>${base}/api/auth/mal/callback</code></li><li>Copy the client ID into the field above, press <b>Save</b>, then <b>Connect</b>.</li></ol></details>`)}
        <div class="row"><button class="btn btn-sm" data-act="test-anilist">${ico('plug')}Check AniList connection</button><span class="test-result" id="test-anilist-out" aria-live="polite"></span></div>
      </div>`;
    };
    const serverTop = () => {
      const s = st.state || {};
      const row = (label, path, btn) => h`<div class="kv-row"><span class="kv-k">${label}</span><span class="kv-v mono" title="${path || ''}">${path || h`<span class="dim">unknown (older server)</span>`}</span>${path ? h`<button type="button" class="btn btn-sm" data-act="open-dir" data-dir="${path}">${ico('folder')}${btn}</button>` : ''}</div>`;
      return h`<div class="kv-rows">${row('Data folder', s.data_dir, 'Open data folder')}${row('ABC\'s folder', s.exe_dir, 'Open ABC\'s folder')}</div>
        <p class="small muted" style="margin:8px 0 16px">The data folder holds settings and the database. Create a folder named <code>data</code> next to abc.exe to keep everything portable.</p>
        <div class="row quit-row"><button type="button" class="btn btn-danger" data-act="quit-abc">${ico('power')}Quit ABC</button></div>`;
    };
    const topHtml = (sec) => {
      if (sec.top === 'folders') return ABC.Folders ? ABC.Folders.managerHtml(st.folders, st.foldersErr) : '';
      if (sec.top === 'server') return serverTop();
      return '';
    };
    const sectionHtml = (sec, first) => h`<section class="set-section surface panel ${A(first)} ${sec.id === 'qbit' && getPath(st.draft, 'downloader.mode') && getPath(st.draft, 'downloader.mode') !== 'qbittorrent' ? 'is-off' : ''}" id="set-${sec.id}" aria-labelledby="set-${sec.id}-h">
      <div class="panel-head" style="align-items:flex-start"><div style="flex:1"><h2 id="set-${sec.id}-h">${sec.title}</h2><p>${sec.desc}</p></div>
        ${sec.test === 'qbit' ? h`<div class="row" style="flex-wrap:wrap;justify-content:flex-end"><span class="test-result" id="test-qbit-out" aria-live="polite"></span><button type="button" class="btn btn-sm" data-act="test-qbit" data-out="test-qbit-out">${ico('plug')}<span class="test-qbit-label">Test</span></button></div>` : ''}</div>
      ${sec.id === 'qbit' ? h`<p class="note info off-note">Pick qBittorrent under <a href="#/settings?section=downloads">Downloads</a> to use these.</p>` : ''}
      ${sec.id === 'watched' && !(st.state && st.state.os ? /win/i.test(st.state.os) : OS_WIN) ? h`<p class="note" style="margin-bottom:14px">Player detection: not available on this OS. Use manual ticks, or "As soon as it opens" via the Play button in ABC.</p>` : ''}
      ${topHtml(sec)}
      <div class="form-grid">${sec.fields.map((f) => fieldHtml(f, st.draft))}${sec.accounts ? accountsHtml() : ''}</div>
    </section>`;
    /** Dim the qBittorrent section unless it is the chosen downloader; drop a stale watch-folder error. */
    const syncMode = (root) => {
      const mode = getPath(st.draft, 'downloader.mode');
      const q = $('#set-qbit', root);
      if (q) q.classList.toggle('is-off', !!mode && mode !== 'qbittorrent');
      if (mode !== 'torrent_file') { const f = FIELDS.find((x) => x.path === 'downloader.torrent_dir'); const el = $(`#${fid(f)}`, root); if (el && f) { const r = readField(el, f); setErr(root, f, r.err); } }
    };
    /** Folder manager changed folders through /api/folders: mirror them into saved + draft so Save keeps them. */
    const onFolders = (root, list) => {
      st.folders = list;
      const fl = toFolders(list);
      for (const o of [st.saved, st.draft, S.settings]) if (o) setPath(o, 'library.folders', clone(fl));
      if (S.settingsDraft) setPath(S.settingsDraft, 'library.folders', clone(fl));
      for (const el of $$('[data-dl-path]', root)) el.textContent = downloadDirOf(st.draft) || 'your download folder';
      paintSavebar(root);
    };
    const paintSavebar = (root, msg, isErr) => {
      const bar = $('#savebar', root);
      if (!bar) return;
      const d = dirty();
      const nErr = Object.values(st.errors).filter(Boolean).length;
      bar.classList.toggle('is-dirty', d);
      const m2 = $('.msg', bar);
      m2.classList.toggle('is-error', !!isErr || nErr > 0);
      m2.textContent = msg || (nErr ? `${plural(nErr, 'field needs', 'fields need')} fixing before saving.` : d ? 'Unsaved changes.' : 'All changes saved.');
      $('#save-btn', bar).disabled = !d || nErr > 0;
      $('#discard-btn', bar).disabled = !d;
      for (const lbl of $$('.test-qbit-label', root)) lbl.textContent = d ? 'Save and test' : 'Test';
    };
    const setErr = (root, f, msg) => {
      st.errors[f.path] = msg || '';
      const el = $(`#${fid(f)}`, root);
      const e = $(`#${fid(f)}-e`, root);
      if (e) e.textContent = msg || '';
      if (el && el.tagName !== 'FIELDSET') el.setAttribute('aria-invalid', msg ? 'true' : 'false');
    };
    const syncEnabled = (root) => {
      for (const f of FIELDS) {
        if (!f.enabledIf) continue;
        const el = $(`#${fid(f)}`, root);
        if (el) el.disabled = !f.enabledIf(st.draft);
      }
    };
    const save = async (root) => {
      for (const f of FIELDS) { const el = $(`#${fid(f)}`, root); if (!el || el.disabled) continue; const r = readField(el, f); setErr(root, f, r.err); }
      const tdF = FIELDS.find((x) => x.path === 'downloader.torrent_dir');
      if (getPath(st.draft, 'downloader.mode') === 'torrent_file' && !getPath(st.draft, 'downloader.torrent_dir') && !st.errors[tdF.path]) setErr(root, tdF, 'Pick the folder your torrent app watches.');
      if (Object.values(st.errors).some(Boolean)) { paintSavebar(root); const bad = FIELDS.find((f) => st.errors[f.path]); if (bad) $(`#${fid(bad)}`, root)?.focus(); return false; }
      const btn = $('#save-btn', root);
      btn.classList.add('is-busy');
      try {
        const saved = await API.saveSettings(st.draft);
        st.saved = clone(saved || st.draft);
        st.draft = clone(st.saved);
        S.settings = clone(st.saved);
        S.settingsDraft = null;
        applyUiSettings();
        paintSavebar(root, 'Saved.');
        toast('Settings saved', 'ok');
        return true;
      } catch (err) {
        // Server validation: 400 {"error", "fields": {"group.field": "message"}} (internal/api/system.go).
        // Older servers send only "error"; then map by the field name it mentions (best effort).
        const msg = err.message || 'Save failed';
        const fields = err.body && typeof err.body === 'object' && err.body.fields && typeof err.body.fields === 'object' ? err.body.fields : null;
        let hits = [];
        if (fields) hits = Object.entries(fields).map(([k, m]) => [FIELDS.find((f) => f.path === k), m]).filter(([f]) => f);
        else {
          const low = msg.toLowerCase();
          const hit = FIELDS.find((f) => low.includes(f.path.toLowerCase()) || low.includes(f.path.split('.').pop().replace(/_/g, ' ')) || low.includes(f.path.split('.').pop()));
          if (hit) hits = [[hit, msg]];
        }
        for (const [f, m] of hits) setErr(root, f, cap(String(m)));
        // library.folders has no input of its own: show it under the folder manager (e.g. the download folder is "flat").
        const fmErr = fields && fields['library.folders'] && $('#fm-err', root);
        if (fmErr) { fmErr.textContent = cap(String(fields['library.folders'])); if (!hits.length) fmErr.closest('.fm')?.scrollIntoView({ block: 'center' }); }
        if (hits.length) $(`#${fid(hits[0][0])}`, root)?.focus();
        paintSavebar(root, `Not saved: ${msg}`, true);
        return false;
      } finally { btn.classList.remove('is-busy'); }
    };
    const onBeforeUnload = (e) => { if (st.draft && dirty()) { e.preventDefault(); e.returnValue = ''; } };
    return {
      title: 'Settings', nav: 'settings', noRefresh: true,
      async load() {
        const [settings, accounts, state, folders] = await Promise.all([API.settings(), soft(API.accounts(), []), soft(API.state(), null), soft(API.folders(), null)]);
        S.settings = settings;
        applyUiSettings();
        st.saved = clone(settings);
        st.draft = S.settingsDraft ? clone(S.settingsDraft) : clone(settings);
        st.accounts = accounts.v || [];
        st.state = state.v;
        st.folders = folders.v || [];
        st.foldersErr = folders.err;
        if (folders.v) { const fl = toFolders(folders.v); setPath(st.saved, 'library.folders', clone(fl)); setPath(st.draft, 'library.folders', clone(fl)); }
        return st;
      },
      render(_d, first) {
        return h`<div class="page">
          <header class="page-head glass ${A(first)}"><h1>Settings</h1><span class="small muted head-sub">Every rule ABC follows lives here.</span><a class="btn btn-sm" href="#/setup">${ico('compass')}Setup guide</a></header>
          <div class="settings-layout">
            <nav class="toc glass" aria-label="Settings sections">${SETTINGS_SCHEMA.map((s) => h`<a href="#/settings?section=${s.id}" data-section="${s.id}">${s.title}</a>`)}</nav>
            <form class="settings-sections" id="settings-form" novalidate>
              ${SETTINGS_SCHEMA.map((s) => sectionHtml(s, first))}
              <div class="savebar glass" id="savebar" role="region" aria-label="Save settings">
                <span class="msg" aria-live="polite"></span>
                <button class="btn" type="button" id="discard-btn">Discard</button>
                <button class="btn btn-primary" type="submit" id="save-btn">Save</button>
              </div>
            </form>
          </div>
        </div>`;
      },
      mount(root) {
        if (S.last) paintFromLast();
        const form = $('#settings-form', root);
        const onChange = (e) => {
          const t = e.target;
          if (t.dataset.colorFor) { const tx = $(`#${t.dataset.colorFor}`, root); tx.value = t.value; tx.dispatchEvent(new Event('input', { bubbles: true })); return; }
          const el = t.dataset.themeFor ? $(`#${t.dataset.themeFor}`, root) : t.closest('[data-path]');
          if (!el) return;
          const f = FIELDS.find((x) => x.path === el.dataset.path);
          if (!f) return;
          const r = readField(t.dataset.themeFor ? t : el, f);
          setErr(root, f, r.err);
          if (!r.err) setPath(st.draft, f.path, r.v);
          S.settingsDraft = dirty() ? clone(st.draft) : null;
          syncEnabled(root);
          if (f.path === 'downloader.mode') syncMode(root);
          paintSavebar(root);
        };
        const fm = $('#folder-manager', root);
        if (fm && ABC.Folders) ABC.Folders.bindManager(fm, (list) => onFolders(root, list));
        form.addEventListener('input', onChange);
        form.addEventListener('change', onChange);
        form.addEventListener('submit', (e) => { e.preventDefault(); save(root); });
        $('#discard-btn', root).addEventListener('click', () => { S.settingsDraft = null; st.draft = clone(st.saved); st.errors = {}; rerender(); });
        paintSavebar(root);
        window.addEventListener('beforeunload', onBeforeUnload);
        const sec = params.get('section');
        if (sec) { const el = $(`#set-${sec}`, root); if (el) setTimeout(() => { el.scrollIntoView({ block: 'start', behavior: motionOK() ? 'smooth' : 'auto' }); const first = $('input,select,textarea', el); if (first) first.focus({ preventScroll: true }); }, 30); }
        $('.toc', root).addEventListener('click', (e) => {
          const a = e.target.closest('[data-section]');
          if (!a) return;
          e.preventDefault();
          history.replaceState(null, '', `#/settings?section=${a.dataset.section}`);
          $(`#set-${a.dataset.section}`, root)?.scrollIntoView({ block: 'start', behavior: motionOK() ? 'smooth' : 'auto' });
        });
      },
      leave() { window.removeEventListener('beforeunload', onBeforeUnload); },
      actions: {
        'test-qbit': async (btn, ds) => {
          const out = $('#' + (ds.out || 'test-qbit-out'));
          if (dirty() && !(await save($('#view')))) return;
          out.className = 'test-result'; out.textContent = 'Testing…';
          try {
            const r = await API.testQbit();
            if (r && r.ok) { out.className = 'test-result ok'; out.textContent = `Connected${r.version ? ', qBittorrent ' + r.version : ''}`; } else { out.className = 'test-result bad'; out.textContent = (r && r.error) || 'Not reachable'; }
          } catch (err) { out.className = 'test-result bad'; out.textContent = err.message; }
        },
        'test-anilist': async () => {
          const out = $('#test-anilist-out');
          out.className = 'test-result'; out.textContent = 'Checking…';
          try {
            const r = await API.testAniList();
            if (r && r.ok) { out.className = 'test-result ok'; out.textContent = 'AniList answers.'; } else { out.className = 'test-result bad'; out.textContent = (r && r.error) || 'No answer'; }
          } catch (err) { out.className = 'test-result bad'; out.textContent = err.message; }
        },
        'acct-delete': async (btn, ds) => {
          const { ok } = await confirmBox({ title: `Disconnect ${ds.service === 'mal' ? 'MyAnimeList' : 'AniList'}?`, body: h`<p>ABC forgets the login token. Your list on the site is not touched.</p>`, ok: 'Disconnect' });
          if (!ok) return;
          await API.deleteAccount(ds.service);
          toast('Disconnected', 'ok');
          st.accounts = st.accounts.filter((a) => a.service !== ds.service);
          rerender();
        },
      },
    };
  };

  // ------------------------------------------------------------------ global actions (data-act)
  const ACTIONS = {
    async retry() { rerender(true); },
    async 'run-conveyor'() { await API.runConveyor(); toast('Conveyor run started. Results show up under Live.', 'ok'); },
    async 'run-scan'() { await API.runScan(); toast('Folder scan started', 'ok'); },
    async play(btn, ds) {
      await API.openFile(ds.file);
      toast(`Opening ${ds.label || 'the episode'} in your player`, 'ok');
      const s = S.showsById[ds.show]; if (s) touchShow(s);
    },
    async reveal(btn, ds) { await API.revealFile(ds.file); toast('Opened the folder', 'ok'); },
    async watched(btn, ds) {
      const v = ds.val === '1';
      await API.setWatched(ds.show, ds.ep, v);
      toast(v ? `Episode ${ds.ep} marked watched` : `Episode ${ds.ep} marked unwatched`, 'ok');
      const s = S.showsById[ds.show]; if (s) touchShow(s);
      scheduleRefresh(0);
    },
    async 'add-release'(btn, ds) {
      const id = ds.release || ($(`#${ds.pick}`) && $(`#${ds.pick}`).value);
      if (!id) return;
      const r = await API.addRelease(id);
      const mode = r && r.mode ? r.mode : S.state && S.state.downloader ? S.state.downloader.mode : '';
      const sent = { qbittorrent: 'Sent to qBittorrent', magnet: 'Sent to your torrent app (magnet link)', torrent_file: '.torrent saved to the watch folder' }[mode] || 'Sent to your torrent app';
      if (r && r.status === 'added') toast(sent, 'ok');
      else if (r && r.status) toast(`Release chosen. Use the Magnet button to add it.${r.status_note ? ' (' + r.status_note + ')' : ''}`, 'ok');
      else toast(S.state && S.state.qbit && S.state.qbit.connected ? 'Sent to qBittorrent' : 'Release chosen. Use the Magnet button to add it.', 'ok');
      scheduleRefresh(0);
    },
    // SPEC-v1.1 11: Home "Nothing to play yet" hero, first show of /api/missing; same dialog as the show page.
    async 'hero-download-missing'(btn, ds) {
      const r = await downloadMissingFlow(Number(ds.show));
      if (r.changed) rerender();
    },
    async 'open-dir'(btn, ds) { await API.openFolder(ds.dir); toast('Opened the folder', 'ok'); },
    // Settings > Server > Quit ABC: the only way to stop abcw.exe, which has no console window.
    async 'quit-abc'() {
      const { ok } = await confirmBox({
        title: 'Quit ABC?',
        body: h`<p>Downloads in your torrent app keep running; nothing new is picked up until you start ABC again.</p>`,
        ok: 'Quit ABC',
        danger: true,
      });
      if (!ok) return;
      await API.quit();
      quitScreen();
    },
    async 'retry-state'() {
      const st = await API.state();
      S.state = st;
      paintStatus();
      toast(st.download_dir && st.download_dir.exists ? 'The download folder is back.' : 'Still not reachable.', st.download_dir && st.download_dir.exists ? 'ok' : 'info');
      rerender();
    },
    async 'skip-release'(btn, ds) {
      const id = ds.release || ($(`#${ds.pick}`) && $(`#${ds.pick}`).value);
      if (!id) return;
      await API.skipRelease(id);
      // SPEC-v1.1 9: the X on an episode row also turns auto off, so one click means "leave this episode alone".
      if (ds.show && ds.ep) {
        await API.setEpisodeAuto(ds.show, ds.ep, 0);
        toast(`Release skipped. Episode ${ds.ep} will not download by itself.`, 'ok');
      } else toast('Release skipped', 'ok');
      scheduleRefresh(0);
    },
    async 'copy-magnet'(btn, ds) { const ok = await copyText(ds.magnet); toast(ok ? 'Magnet link copied. Paste it into your torrent app.' : 'Copy failed. Use the magnet icon instead.', ok ? 'ok' : 'bad'); },
    async track(btn, ds) {
      const card = await API.addShow(Number(ds.aid));
      if (card && card.id) S.showsById[card.id] = card;
      btn.outerHTML = String(card && card.id ? h`<a class="btn btn-sm is-on" href="#/show/${card.id}">${ico('check')}Tracked</a>` : h`<button class="btn btn-sm is-on" disabled>${ico('check')}Tracked</button>`);
      const days = (S.settings && S.settings.conveyor && S.settings.conveyor.lookback_days) || 14;
      toast(`Tracking ${ds.title}. Episodes from the last ${days} days download by themselves; older ones are on the show page.`, 'ok', 7000);
    },
    async 'auth-start'(btn, ds) {
      const r = await API.authStart(ds.service);
      if (r && r.url) location.href = r.url;
    },
    async 'import-mal'() { const r = await API.importMal(); toast(`MyAnimeList import: ${r.added ?? 0} added, ${r.updated ?? 0} updated`, 'ok'); },
  };
  document.addEventListener('click', async (e) => {
    const btn = e.target.closest('[data-act]');
    if (!btn) return;
    const act = btn.dataset.act;
    const fn = (current && current.actions && current.actions[act]) || ACTIONS[act];
    if (!fn) return;
    e.preventDefault();
    if (btn.classList.contains('is-busy')) return;
    btn.classList.add('is-busy');
    btn.setAttribute('aria-busy', 'true');
    try { await fn.call(current, btn, btn.dataset, e); } catch (err) { toast(err.message || String(err), 'bad'); } finally {
      btn.classList.remove('is-busy');
      btn.removeAttribute('aria-busy');
    }
  });
  // Broken artwork: hide the image, keep the placeholder colour.
  document.addEventListener('error', (e) => { const t = e.target; if (t && t.tagName === 'IMG') t.style.visibility = 'hidden'; }, true);

  // ------------------------------------------------------------------ router
  // Entries are a view name (resolved from Views at route time, so home.js / shows.js can register late) or a
  // factory function (setup.js pushes one). SPEC-v1.2 2: #/library stays as an alias of #/shows.
  const ROUTES = [
    [/^\/?$/, 'home'],
    [/^\/shows$/, 'shows'],
    [/^\/library$/, 'shows'],
    [/^\/add$/, 'add'],
    [/^\/calendar$/, 'calendar'],
    [/^\/settings$/, 'settings'],
    [/^\/show\/(\d+)$/, 'show'],
  ];
  const factoryOf = (f) => (typeof f === 'string' ? Views[f] : f);
  let current = null;
  let routeSeq = 0;
  function parseHash() {
    const raw0 = decodeURIComponent(location.hash.replace(/^#/, '')) || '/';
    const [path, qs] = raw0.split('?');
    return { path: path || '/', params: new URLSearchParams(qs || '') };
  }
  function hydrate(root) {
    for (const el of $$('[data-bg]', root)) {
      const u = cssUrl(el.dataset.bg);
      if (u) el.style.backgroundImage = u; else el.classList.add('is-empty');
    }
  }
  function skeleton(name) {
    if (name === 'home') return h`<div class="page home">${skel(360, 'border-radius:22px')}${skel(420, 'border-radius:22px')}</div>`;
    return h`<div class="page"><div class="page-head glass"><h1>${cap(name)}</h1></div>${skel(320, 'border-radius:18px')}</div>`;
  }
  async function route(opts = {}) {
    const { path, params } = parseHash();
    let factory = null; let match = null;
    for (const [re, f] of ROUTES) { const m = path.match(re); if (m) { factory = factoryOf(f); match = m; break; } }
    if (!factory) { location.replace('#/'); return; }
    // Same view, only ?section= changed on settings: just scroll.
    if (current && current.nav === 'settings' && factory === Views.settings && !opts.force) {
      const sec = params.get('section'); const el = sec && $(`#set-${sec}`);
      if (el) { el.scrollIntoView({ block: 'start' }); return; }
    }
    const seq = ++routeSeq;
    if (current && current.leave) current.leave();
    closeMenus();
    const view = factory(match, params);
    current = view;
    setNav(view.nav);
    document.title = `${view.title} · ABC`;
    const root = $('#view');
    root.innerHTML = String(skeleton(view.nav === 'home' ? 'home' : view.title));
    window.scrollTo(0, 0);
    await paint(view, seq, true);
    if (opts.focus) root.focus({ preventScroll: true });
  }
  async function paint(view, seq, first) {
    const root = $('#view');
    let data;
    try { data = await view.load(); } catch (err) {
      if (seq !== routeSeq) return;
      root.innerHTML = String(h`<div class="page">${errorPanel(err)}</div>`);
      return;
    }
    if (seq !== routeSeq || current !== view) return;
    view.data = data;
    root.innerHTML = String(view.render(data, first));
    hydrate(root);
    if (view.mount) view.mount(root, data);
  }
  /** Re-render the current view in place (used after actions and by SSE). */
  async function rerender(full) {
    if (!current) return;
    if (full) return route({ force: true });
    const y = window.scrollY;
    if (current.refresh && !full) {
      try { await current.refresh($('#view')); } catch (err) { toast(err.message, 'bad'); }
    } else {
      await paint(current, routeSeq, false);
    }
    window.scrollTo(0, y);
  }
  let refreshTimer = null; let refreshPending = false;
  /** Debounced refresh; postponed while the user is typing in a field of the current view or has a menu open. */
  function scheduleRefresh(ms = 600) {
    if (!current || current.noRefresh) return;
    clearTimeout(refreshTimer);
    refreshTimer = setTimeout(() => {
      const a = document.activeElement;
      // Fields marked data-keep sit outside the regions a refresh replaces, so typing there never blocks it.
      if (a && $('#view').contains(a) && a.matches('input:not([type=checkbox]):not([type=radio]), textarea, select') && !a.closest('[data-keep]')) {
        if (!refreshPending) { refreshPending = true; a.addEventListener('blur', () => { refreshPending = false; scheduleRefresh(100); }, { once: true }); }
        return;
      }
      if ($('.menu-pop:not([hidden])', $('#view'))) { scheduleRefresh(1500); return; }
      rerender();
    }, ms);
  }
  function setNav(name) {
    for (const a of $$('#main-nav a')) {
      if (a.dataset.route === name) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
    }
  }

  // ------------------------------------------------------------------ top bar: download status dot, global search
  /** SPEC-v1.2 2: the download-mode / qBittorrent status dot (same wording the old stat tile had). */
  function downloaderStatus(st) {
    const qb = (st && st.qbit) || {};
    const dl = st && st.downloader;
    const DL = {
      qbittorrent: ['qBittorrent', dl && dl.ok ? ['s-disk', 'Connected', 'Downloads go to qBittorrent'] : ['s-bad', 'Not connected', 'Magnet links only until it is back']],
      magnet: ['Torrent app', dl && dl.ok ? ['s-disk', 'Magnet links', 'Sent to your torrent app'] : ['s-bad', 'No magnet app', (dl && dl.error) || 'No app opens magnet links']],
      torrent_file: ['Watch folder', dl && dl.ok ? ['s-disk', 'Ready', '.torrent files go to the watch folder'] : ['s-bad', 'Folder missing', (dl && dl.error) || 'The watch folder is not reachable']],
      manual: ['Downloads', ['s-aired', 'By hand', 'You add magnet links yourself']],
    };
    const t = dl && DL[dl.mode];
    if (t) return { name: t[0], cls: t[1][0], state: t[1][1], sub: t[1][2], section: 'downloads' };
    const s = qb.connected ? ['s-disk', 'Connected', 'Downloads go to qBittorrent'] : qb.enabled ? ['s-bad', 'Not connected', 'Magnet links only until it is back'] : ['s-aired', 'Off', 'Magnet links only'];
    return { name: 'qBittorrent', cls: s[0], state: s[1], sub: s[2], section: 'qbit' };
  }
  function paintStatus() {
    const el = $('#dl-status');
    if (!el || !S.state) return;
    const d = downloaderStatus(S.state);
    const text = `${d.name}: ${d.state}. ${d.sub}.`;
    el.href = `#/settings?section=${d.section}`;
    el.setAttribute('aria-label', `${text} Open download settings.`);
    el.dataset.tip = `${d.name}: ${d.state}`;
    el.innerHTML = String(h`<span class="dot ${d.cls}" aria-hidden="true"></span><span class="tb-status-l">${d.name}</span>`);
  }
  function initSearch() {
    const input = $('#gsearch'); const list = $('#gsearch-list'); const bar = $('.topbar');
    if (!input || !list) return;
    let items = []; let active = -1;
    const close = () => { list.hidden = true; input.setAttribute('aria-expanded', 'false'); input.removeAttribute('aria-activedescendant'); active = -1; };
    const setActive = (i) => {
      active = i;
      $$('.gs-item', list).forEach((li, j) => li.setAttribute('aria-selected', String(j === i)));
      if (i >= 0) { input.setAttribute('aria-activedescendant', `gs-${i}`); $(`#gs-${i}`)?.scrollIntoView({ block: 'nearest' }); } else input.removeAttribute('aria-activedescendant');
    };
    const paint = () => {
      const q = input.value.trim();
      if (!q) { close(); return; }
      const low = q.toLowerCase();
      const LS = Object.fromEntries(LIST_TABS);
      const shows = Object.values(S.showsById)
        .filter((s) => [s.title, s.title_romaji, s.title_english].some((t) => t && t.toLowerCase().includes(low)))
        .sort((a, b) => (a.list_status === 'watching' ? 0 : 1) - (b.list_status === 'watching' ? 0 : 1) || showTitle(a).localeCompare(showTitle(b)))
        .slice(0, 6);
      items = [...shows.map((s) => ({ href: `#/show/${s.id}`, s })), { href: `#/add?q=${encodeURIComponent(q)}`, q }];
      list.innerHTML = String(h`${shows.length ? h`<li class="gs-head" role="presentation">Your shows</li>` : ''}${items.map((it, i) => (it.s
        ? h`<li class="gs-item" role="option" id="gs-${i}" aria-selected="false" data-i="${i}"><img src="${safeUrl(it.s.cover_url)}" alt=""><span class="gs-t">${showTitle(it.s)}</span><span class="gs-sub">${LS[it.s.list_status] || pretty(it.s.list_status)} · ${fNum(progressOf(it.s).watched)} / ${fNum(progressOf(it.s).aired)}</span></li>`
        : h`<li class="gs-item gs-anilist" role="option" id="gs-${i}" aria-selected="false" data-i="${i}">${ico('search')}<span class="gs-t">Search AniList for "${it.q}"</span><span class="gs-sub">Add page</span></li>`))}`);
      list.hidden = false;
      input.setAttribute('aria-expanded', 'true');
      setActive(0);
    };
    const go = (i) => {
      const it = items[i];
      if (!it) return;
      close();
      input.value = '';
      input.blur();
      bar.classList.remove('is-searching');
      location.hash = it.href;
    };
    input.addEventListener('input', paint);
    input.addEventListener('focus', () => { if (input.value.trim()) paint(); });
    input.addEventListener('blur', () => setTimeout(() => { if (document.activeElement !== input) { close(); if (!input.value) bar.classList.remove('is-searching'); } }, 120));
    input.addEventListener('keydown', (e) => {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        if (list.hidden) { paint(); return; }
        const n = items.length;
        setActive(((active + (e.key === 'ArrowDown' ? 1 : -1)) % n + n) % n);
      } else if (e.key === 'Enter') {
        e.preventDefault();
        if (list.hidden) paint();
        go(active >= 0 ? active : 0);
      } else if (e.key === 'Escape') {
        e.stopPropagation();
        if (!list.hidden) close(); else { input.value = ''; input.blur(); bar.classList.remove('is-searching'); }
      }
    });
    list.addEventListener('mousedown', (e) => e.preventDefault()); // keep focus in the input
    list.addEventListener('click', (e) => { const li = e.target.closest('.gs-item'); if (li) go(Number(li.dataset.i)); });
    $('#search-toggle')?.addEventListener('click', () => { bar.classList.add('is-searching'); input.focus(); });
  }
  /** `/` focuses search (SPEC-v1.2 2) unless the user is typing somewhere. */
  function focusSearch() {
    const input = $('#gsearch');
    if (!input) return;
    $('.topbar').classList.add('is-searching');
    input.focus();
    input.select();
  }

  // ------------------------------------------------------------------ menus (⋯ popovers) + global keys
  function closeMenus(except) {
    for (const b of $$('[data-menu][aria-expanded="true"]')) {
      if (b === except) continue;
      b.setAttribute('aria-expanded', 'false');
      const p = document.getElementById(b.getAttribute('aria-controls'));
      if (p) p.hidden = true;
    }
  }
  document.addEventListener('click', (e) => {
    const b = e.target.closest('[data-menu]');
    if (b) {
      const open = b.getAttribute('aria-expanded') !== 'true';
      closeMenus(b);
      b.setAttribute('aria-expanded', String(open));
      const p = document.getElementById(b.getAttribute('aria-controls'));
      if (p) {
        p.hidden = !open;
        if (open) {
          // Open upward when there is no room below.
          p.classList.remove('is-up');
          const r = p.getBoundingClientRect();
          if (r.bottom > innerHeight - 8 && r.height < b.getBoundingClientRect().top - 70) p.classList.add('is-up');
          $('.menu-item, select', p)?.focus();
        }
      }
      return;
    }
    const pop = e.target.closest('.menu-pop');
    if (!pop) { closeMenus(); return; }
    if (e.target.closest('.menu-item')) setTimeout(() => closeMenus(), 0);
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      const openMenu = $('[data-menu][aria-expanded="true"]');
      if (openMenu) { closeMenus(); openMenu.focus(); return; }
      if (document.querySelector('dialog[open]')) return; // native dialogs close themselves
      if (ABC.Drawer && ABC.Drawer.isOpen() && !ABC.Drawer.isPinned()) { ABC.Drawer.close(); return; }
      return;
    }
    if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && e.target.closest && e.target.closest('.menu-pop')) {
      const pop = e.target.closest('.menu-pop');
      const els = $$('.menu-item, select', pop);
      const i = els.indexOf(e.target);
      if (i >= 0 && e.target.tagName !== 'SELECT') { e.preventDefault(); els[(i + (e.key === 'ArrowDown' ? 1 : -1) + els.length) % els.length].focus(); }
      return;
    }
    if (e.key === '/' && !e.ctrlKey && !e.metaKey && !e.altKey) {
      const t = e.target;
      if (t && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName))) return;
      if (document.querySelector('dialog[open]')) return;
      e.preventDefault();
      focusSearch();
    }
  });

  // ------------------------------------------------------------------ live events (SSE)
  const EVENT_KINDS = ['show.added', 'show.removed', 'airing.updated', 'release.found', 'release.added', 'release.waiting', 'release.skipped', 'download.done', 'download.failed', 'file.found', 'file.missing', 'episode.watched', 'episode.unwatched', 'push.ok', 'push.failed', 'scan.done', 'error'];
  let liveState = 'connecting';
  const liveLabel = () => ({ live: 'Live', connecting: 'Connecting…', down: 'Offline' }[liveState]);
  function setLive(s) {
    liveState = s;
    if (ABC.Drawer) ABC.Drawer.setLive(s);
  }
  function mergeEvents(list) {
    if (!Array.isArray(list)) return;
    for (const ev of list) {
      if (!ev || ev.id == null || S.eventIds.has(ev.id)) continue;
      S.eventIds.add(ev.id);
      S.events.push(ev);
      if (ev.id > S.lastEventId) S.lastEventId = ev.id;
    }
    S.events.sort((a, b) => b.id - a.id);
    if (S.events.length > 200) S.events.length = 200;
  }
  function onEvent(kind, data) {
    let ev;
    try { ev = typeof data === 'string' ? JSON.parse(data) : data; } catch { return; }
    if (!ev || typeof ev !== 'object') return;
    if (!ev.kind) ev.kind = kind;
    if (!ev.ts) ev.ts = nowSec();
    if (ev.id == null) ev.id = (S.lastEventId || 0) + 0.001; // tolerate id-less events
    if (S.eventIds.has(ev.id)) return;
    mergeEvents([ev]);
    if (ABC.Drawer) ABC.Drawer.onEvent(ev);
    if (ev.kind === 'error' || ev.kind === 'download.failed' || ev.kind === 'push.failed') toast(ev.message || ev.kind, 'bad');
    if (ev.kind === 'show.added' || ev.kind === 'show.removed') refreshShowIndex();
    if (current && current.refreshOn && current.refreshOn(ev)) scheduleRefresh();
  }
  /** Keep S.showsById (global search, drawer show names) current on pages that do not load /api/shows. */
  const refreshShowIndex = debounce(async () => { const r = await soft(API.shows(), null); if (r.v) indexShows(r.v); }, 800);
  let sse = null; // the live EventSource (closed by Quit ABC)
  /** After Quit ABC: close the event stream (no reconnect) and cover the page with the note. */
  function quitScreen() {
    S.quit = true;
    if (sse) { sse.close(); sse = null; }
    document.title = 'ABC has quit';
    const el = document.createElement('div');
    el.className = 'quit-screen';
    el.setAttribute('role', 'alert');
    el.innerHTML = String(h`<div class="quit-card surface">${ico('power')}<p>ABC has quit. Close this tab.</p></div>`);
    document.body.appendChild(el);
  }
  function connectSSE() {
    if (S.quit) return;
    if (typeof EventSource === 'undefined') { setLive('down'); return; }
    let es;
    let hadError = false;
    try { es = new EventSource('/api/events/stream'); } catch { setLive('down'); return; }
    sse = es;
    es.onopen = async () => {
      setLive('live');
      if (hadError && S.lastEventId) { // catch up on what was missed while disconnected
        try { const missed = await API.events(Math.floor(S.lastEventId), 100); (missed || []).sort((a, b) => a.id - b.id).forEach((ev) => onEvent(ev.kind, ev)); } catch { /* next reconnect */ }
      }
      hadError = false;
    };
    es.onerror = () => {
      if (S.quit) { es.close(); return; } // the server went away on purpose: no reconnect
      hadError = true; setLive(es.readyState === 2 ? 'down' : 'connecting'); if (es.readyState === 2) setTimeout(connectSSE, 5000);
    };
    for (const k of EVENT_KINDS) es.addEventListener(k, (e) => onEvent(k, e.data));
    es.addEventListener('ping', () => { if (liveState !== 'live') setLive('live'); });
    es.onmessage = (e) => onEvent(null, e.data);
  }

  // ------------------------------------------------------------------ boot
  function loadScript(src) {
    return new Promise((res, rej) => { const s = document.createElement('script'); s.src = src; s.onload = res; s.onerror = () => rej(new Error('failed to load ' + src)); document.head.appendChild(s); });
  }
  async function boot() {
    if (new URLSearchParams(location.search).get('mock') === '1') {
      try { await loadScript('mock.js'); } catch (err) { console.error(err); }
    }
    for (const el of $$('[data-icon]')) el.innerHTML = svg(el.dataset.icon);
    paintFromLast();
    // Shows feed the global search and the drawer's show names; events fill the drawer before SSE starts.
    const [st0, shows0, ev0] = await Promise.all([soft(API.state(), null), soft(API.shows(), null), soft(API.events(undefined, 60), []),
      API.settings().then((s) => { S.settings = s; }, () => { /* the view shows the error */ })]);
    if (st0.v) S.state = st0.v;
    if (shows0.v) indexShows(shows0.v);
    mergeEvents(ev0.v);
    paintStatus();
    if (ABC.Drawer) ABC.Drawer.init();
    initSearch();
    // SPEC-v1.1 6.1: first boot opens the setup guide, but only from Home; deep links still work.
    if (st0.v && st0.v.setup_done === false && /^#?\/?$/.test(location.hash)) history.replaceState(null, '', '#/setup');
    applyUiSettings();
    matchMedia('(prefers-reduced-motion: reduce)').addEventListener?.('change', () => applyUiSettings());
    let navByClick = false;
    $('#main-nav').addEventListener('click', () => { navByClick = true; });
    window.addEventListener('hashchange', () => { route({ focus: navByClick }); navByClick = false; });
    await route();
    connectSSE();
    // Relative times ("5 min ago") drift; repaint the drawer timestamps every minute.
    setInterval(() => { for (const t of $$('.feed-meta time')) { const ts = Date.parse(t.getAttribute('datetime')) / 1000; t.textContent = fRel(ts); } }, 60000);
  }

  // ------------------------------------------------------------------ extensions (folders.js, setup.js, shows.js, home.js, drawer.js)
  // Those files load before app.js and push a function onto window.ABC_EXT; each gets these internals
  // and may add Views, ROUTES, ACTIONS, or set ABC.Folders / ABC.Setup / ABC.Shows / ABC.Drawer.
  const ABC = {
    $, $$, h, raw, esc, ico, svg, cap, plural, clamp, debounce, baseName, fSize, fNum, clone, getPath, setPath, store,
    api, API, ApiError, soft, toast, confirmBox, copyText, S, Views, ROUTES, ACTIONS, rerender, route, hydrate, scheduleRefresh,
    A, OS_WIN, winOr, motionOK, emptyBox, errorPanel, isAbsPath, samePath, toFolders, downloadDirOf,
    SETTINGS_SCHEMA, FIELDS, fieldHtml, readField, fid, isUrl, applyUiSettings, paintFromLast,
    // v1.2 (home.js, shows.js, drawer.js)
    showTitle, mediaTitle, progressOf, progBar, fWhen, fRel, fTime, fFull, fAired, F, safeUrl, isHex, readableAccent, nowSec,
    STATUS_LABEL, FORMAT_LABEL, pretty, LIST_TABS, indexShows, touchShow, weekStart, weekDays, dayKey, toDate, calChips, calState, weekEntry,
    needsSetup, setupTodo, lowDisk, driveOf, GIB, liveLabel, paintStatus, focusFirst, downloadMissingFlow, epNum, effOffset,
    current: () => current,
  };
  for (const ext of window.ABC_EXT || []) { try { ext(ABC); } catch (err) { console.error('[ABC] extension failed', err); } }
  boot();
})();

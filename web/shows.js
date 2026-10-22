/* AnimeBeltConveyor web UI: "Your shows" poster grid + list view (SPEC-v1.2-ui.md 3.3), shared by Home,
   the Shows tab (#/shows, alias #/library) and the Add page (poster card with a Track button, 5).
   Loaded before app.js; app.js calls the function below with its internals. Plain JS, no dependencies. */
'use strict';
(window.ABC_EXT = window.ABC_EXT || []).push((ABC) => {
  const { $, $$, h, raw, ico, store, S, Views, API, soft, A, showTitle, mediaTitle, progressOf, fNum, fFull, fWhen, safeUrl, isHex, readableAccent, nowSec, plural, debounce, STATUS_LABEL, FORMAT_LABEL, pretty, indexShows, paintFromLast } = ABC;

  // Filter chips in the spec order (3.3). "all" = every list.
  const FILTERS = [['watching', 'Watching'], ['paused', 'Paused'], ['planning', 'Planning'], ['completed', 'Completed'], ['dropped', 'Dropped'], ['all', 'All']];
  const SORTS = [['activity', 'Last activity'], ['next', 'Next airing'], ['title', 'Title'], ['progress', 'Progress']];
  const DAY = 86400;

  /** Per-show extras the list route does not carry, derived from /api/upnext, /api/missing and /api/calendar:
      play = the lowest unwatched episode with a file, missing = aired episodes without a file (watching shows),
      dl = 'dl' when a release is added/downloading, 'chosen' when a magnet is waiting (no backend change, 7). */
  function aux(upnext, missing, cal) {
    const by = {};
    const g = (id) => (by[id] ||= { play: null, unwatched: 0, missing: 0, dl: null });
    for (const e of upnext || []) {
      if (!e || e.show_id == null) continue;
      const x = g(e.show_id); x.unwatched++;
      if (e.file && (!x.play || e.number < x.play.number)) x.play = e;
    }
    for (const m of missing || []) if (m && m.count > 0) g(m.show_id).missing = m.count;
    for (const it of cal || []) {
      if (!it || it.on_disk || it.watched) continue;
      if (['added', 'downloading'].includes(it.release_status)) g(it.show_id).dl = 'dl';
      else if (it.release_status === 'chosen' && g(it.show_id).dl !== 'dl') g(it.show_id).dl = 'chosen';
    }
    return by;
  }
  /** Fetch the three sources softly (an old server keeps working without them). */
  async function loadAux() {
    const t = nowSec();
    const [up, miss, cal] = await Promise.all([soft(API.upnext(), []), soft(API.missing(), []), soft(API.calendar(t - 28 * DAY, t + DAY), [])]);
    return { upnext: up, missing: miss, cal, ax: aux(up.v, miss.v, cal.v) };
  }

  function airsIn(ts) {
    const d = ts - nowSec();
    if (d < 3600) return `Airs in ${Math.max(1, Math.round(d / 60))} min`;
    if (d < DAY) return `Airs in ${Math.round(d / 3600)} h`;
    return `Airs in ${Math.round(d / DAY)} d`;
  }
  /** One status chip, priority order from 3.3: Play, Downloading, Missing N, Airs in, Not auto, Finished. */
  function statusChip(s, x) {
    x = x || {};
    const g = progressOf(s);
    const title = showTitle(s);
    if (x.play && x.play.file) {
      return h`<button type="button" class="schip schip-play" data-act="play" data-file="${x.play.file.id}" data-show="${s.id}" data-label="episode ${x.play.number}" aria-label="Play ${title} episode ${x.play.number}">${ico('play')}Play<span class="schip-n">Ep ${x.play.number}</span></button>`;
    }
    if (x.dl === 'dl') return h`<span class="schip schip-warn">${ico('download')}Downloading</span>`;
    if (x.dl === 'chosen') return h`<span class="schip schip-warn" title="A release was chosen; add it with the Magnet button on the show page">${ico('magnet')}Magnet ready</span>`;
    if (x.missing) return h`<span class="schip schip-mute" title="${plural(x.missing, 'aired episode')} without a file">${ico('alert')}Missing ${fNum(x.missing)}</span>`;
    if (s.next && s.next.aired_at && s.next.aired_at > nowSec()) return h`<span class="schip schip-mute" title="Episode ${s.next.episode}, ${fFull(s.next.aired_at)}">${ico('clock')}${airsIn(s.next.aired_at)}</span>`;
    // The conveyor only searches shows on the Watching list, so aired episodes of other lists never arrive by themselves.
    if (!['watching', 'completed', 'dropped'].includes(s.list_status) && g.aired > g.onDisk && g.watched < g.aired) return h`<span class="schip schip-mute" title="Only shows on your Watching list download by themselves">${ico('boltOff')}Not auto</span>`;
    if (s.status === 'FINISHED') return h`<span class="schip schip-mute">${ico('check')}Finished</span>`;
    return '';
  }
  const colorVars = (c) => {
    if (!isHex(c)) return '';
    return raw(`style="--c:${c};--cc:${readableAccent(c).accent}"`);
  };
  function miniProg(s) {
    const g = progressOf(s);
    const pct = (n) => `${Math.min(100, Math.max(0, (n / g.scale) * 100)).toFixed(2)}%`;
    const label = `${fNum(g.watched)} watched, ${fNum(g.onDisk)} on disk, ${fNum(g.aired)} aired${g.total ? ' of ' + fNum(g.total) : ''}`;
    return h`<div class="pprog"><div class="prog" role="img" aria-label="${label}" title="${label}"><i class="p-aired" style="width:${pct(g.aired)}"></i><i class="p-disk" style="width:${pct(g.onDisk)}"></i><i class="p-watched" style="width:${pct(g.watched)}"></i></div>
      <span class="pprog-t">${fNum(g.watched)} / ${fNum(g.aired)} aired</span></div>`;
  }
  /** Poster card (grid). The title link is stretched over the card; the status chip sits above it. */
  function posterCard(s, x) {
    return h`<article class="pcard" ${colorVars(s.color)}>
      <div class="pcard-art"><img src="${safeUrl(s.cover_url)}" alt="" loading="lazy"></div>
      <div class="pcard-foot">
        <h3 class="pcard-title clamp2"><a class="pcard-link" href="#/show/${s.id}">${showTitle(s)}</a></h3>
        ${miniProg(s)}
        <div class="pcard-status">${statusChip(s, x)}</div>
      </div>
    </article>`;
  }
  function nextLine(s) {
    if (s.next && s.next.aired_at) return h`<span title="${fFull(s.next.aired_at)}">Ep ${s.next.episode} · ${fWhen(s.next.aired_at)}</span>`;
    return h`<span class="dim">${STATUS_LABEL[s.status] || pretty(s.status) || 'No date'}</span>`;
  }
  /** List row: thumb, title, progress, next airing, status chip, Play / Watched (only for the episode on disk). */
  function listRow(s, x) {
    x = x || {};
    const p = x.play;
    return h`<li class="srow" ${colorVars(s.color)}>
      <img class="srow-thumb" src="${safeUrl(s.cover_url)}" alt="" loading="lazy">
      <div class="srow-main"><a class="srow-title ellipsis" href="#/show/${s.id}">${showTitle(s)}</a>${miniProg(s)}</div>
      <div class="srow-next">${nextLine(s)}</div>
      <div class="srow-status">${statusChip(s, x)}</div>
      <div class="srow-acts">${p && p.file ? h`<button class="btn btn-sm" data-act="watched" data-show="${s.id}" data-ep="${p.number}" data-val="1" data-tip="Mark episode ${p.number} watched" aria-label="Mark ${showTitle(s)} episode ${p.number} watched">${ico('check')}Watched</button>` : ''}</div>
    </li>`;
  }
  /** Add page card (5): same frame, a Track button (or Tracked link) in the footer instead of the status chip. */
  function mediaCard(m, { tracked, showId }) {
    const aid = m.anilist_id ?? m.id;
    const cover = m.cover_url || (m.coverImage && (m.coverImage.extraLarge || m.coverImage.large));
    const eps = m.episodes ?? m.episodes_total;
    const meta = [FORMAT_LABEL[m.format] || m.format, eps ? plural(eps, 'ep') : '', m.season && (m.season_year || m.seasonYear) ? `${pretty(m.season)} ${m.season_year || m.seasonYear}` : ''].filter(Boolean).join(' · ');
    const title = mediaTitle(m);
    return h`<article class="pcard pcard-media" ${colorVars(m.color)}>
      <div class="pcard-art"><img src="${safeUrl(cover)}" alt="" loading="lazy"></div>
      <div class="pcard-foot">
        <h3 class="pcard-title clamp2">${tracked && showId ? h`<a class="pcard-link" href="#/show/${showId}">${title}</a>` : title}</h3>
        <div class="pcard-meta">${meta || raw('&nbsp;')}</div>
        <div class="pcard-status">${tracked ? (showId ? h`<a class="btn btn-sm is-on pcard-btn" href="#/show/${showId}">${ico('check')}Tracked</a>` : h`<button class="btn btn-sm is-on pcard-btn" disabled>${ico('check')}Tracked</button>`)
          : h`<button class="btn btn-sm pcard-btn" data-act="track" data-aid="${aid}" data-title="${title}">${ico('plus')}Track</button>`}</div>
      </div>
    </article>`;
  }

  // ---------------------------------------------------------------- state + markup shared by Home and Shows
  /** home: filter always starts at Watching. page: remembers the filter (libTab) like the old Library. */
  function newState(kind, params) {
    let filter = 'watching';
    if (kind === 'page') filter = (params && params.get('tab')) || store.get('libTab') || 'watching';
    if (!FILTERS.some(([k]) => k === filter)) filter = 'watching';
    let sort = store.get('libSort') || 'activity';
    if (!SORTS.some(([k]) => k === sort)) sort = 'activity';
    return { kind, filter, sort, view: store.get('showsView') === 'list' ? 'list' : 'grid', q: '', shows: [], ax: {} };
  }
  const countOf = (st, k) => (k === 'all' ? st.shows.length : st.shows.filter((s) => s.list_status === k).length);
  function filtered(st) {
    const needle = st.q.trim().toLowerCase();
    let list = st.shows.filter((s) => st.filter === 'all' || s.list_status === st.filter);
    if (needle) list = list.filter((s) => [s.title, s.title_romaji, s.title_english].some((t) => t && t.toLowerCase().includes(needle)));
    const ratio = (s) => { const g = progressOf(s); return g.watched / Math.max(g.total || g.aired, 1); };
    // Leading brackets and quotes do not count ("[Oshi no Ko]" sorts under O).
    const key = (x) => showTitle(x).replace(/^[^\p{L}\p{N}]+/u, '');
    const byTitle = (a, b) => key(a).localeCompare(key(b), undefined, { sensitivity: 'base', numeric: true });
    const by = {
      activity: (a, b) => (b.last_activity || 0) - (a.last_activity || 0),
      title: byTitle,
      next: (a, b) => ((a.next && a.next.aired_at) || Infinity) - ((b.next && b.next.aired_at) || Infinity) || byTitle(a, b),
      progress: (a, b) => ratio(b) - ratio(a) || byTitle(a, b),
    }[st.sort];
    return list.slice().sort(by);
  }
  function chipsHtml(st) {
    return h`${FILTERS.map(([k, label]) => h`<button type="button" class="fchip" data-filter="${k}" aria-pressed="${String(st.filter === k)}">${label}<span class="n">${fNum(countOf(st, k))}</span></button>`)}`;
  }
  function toolbarHtml(st) {
    return h`<div class="sh-tools">
      <div class="fchips" role="group" aria-label="Filter by list" id="sh-chips">${chipsHtml(st)}</div>
      <div class="sh-right">
        ${st.kind === 'page' ? h`<div class="search sh-q"><label class="sr-only" for="sh-q">Filter your shows</label>${ico('search')}<input class="input" id="sh-q" type="search" placeholder="Filter by title" value="${st.q}" autocomplete="off"></div>` : ''}
        <label class="sr-only" for="sh-sort">Sort shows</label>
        <select class="select sh-sort" id="sh-sort">${SORTS.map(([k, l]) => h`<option value="${k}" ${st.sort === k ? raw('selected') : ''}>${l}</option>`)}</select>
        <div class="seg" role="group" aria-label="Layout">
          <button type="button" class="seg-btn" data-layout="grid" aria-pressed="${String(st.view === 'grid')}" aria-label="Poster grid" data-tip="Poster grid">${ico('grid')}</button>
          <button type="button" class="seg-btn" data-layout="list" aria-pressed="${String(st.view === 'list')}" aria-label="List" data-tip="List">${ico('list')}</button>
        </div>
      </div>
    </div>`;
  }
  function bodyHtml(st) {
    if (!st.shows.length) {
      return h`<div class="one-line">${ico('tv')}<span>No shows yet. Add one and ABC fetches new episodes as they air.</span><a class="btn btn-sm btn-primary" href="#/add">${ico('plus')}Add a show</a></div>`;
    }
    const list = filtered(st);
    if (!list.length) {
      if (st.q.trim()) return h`<div class="one-line">${ico('search')}<span>No show matches "${st.q.trim()}".</span></div>`;
      const label = FILTERS.find(([k]) => k === st.filter)[1];
      return h`<div class="one-line">${ico('list')}<span>Nothing in ${label}. Change a show's list on its page to move it here.</span></div>`;
    }
    return st.view === 'list'
      ? h`<ul class="slist">${list.map((s) => listRow(s, st.ax[s.id]))}</ul>`
      : h`<div class="pgrid">${list.map((s) => posterCard(s, st.ax[s.id]))}</div>`;
  }
  const panelHtml = (st) => h`${toolbarHtml(st)}<div id="sh-body" class="sh-body">${bodyHtml(st)}</div>`;
  /** Listeners for chips, sort, layout toggle, text filter. Bound on the panel element (replaced with the view). */
  function bind(panel, st) {
    if (!panel) return;
    const paint = () => { const b = $('#sh-body', panel); if (b) b.innerHTML = String(bodyHtml(st)); const c = $('#sh-chips', panel); if (c) c.innerHTML = String(chipsHtml(st)); };
    panel.addEventListener('click', (e) => {
      const f = e.target.closest('[data-filter]');
      if (f && panel.contains(f)) {
        st.filter = f.dataset.filter;
        if (st.kind === 'page') { store.set('libTab', st.filter); history.replaceState(null, '', `#/shows${st.filter === 'watching' ? '' : '?tab=' + st.filter}`); }
        paint();
        const again = $(`[data-filter="${st.filter}"]`, panel); if (again) again.focus();
        return;
      }
      const l = e.target.closest('[data-layout]');
      if (l && panel.contains(l)) {
        st.view = l.dataset.layout; store.set('showsView', st.view);
        for (const b of $$('[data-layout]', panel)) b.setAttribute('aria-pressed', String(b.dataset.layout === st.view));
        paint();
      }
    });
    const sort = $('#sh-sort', panel);
    if (sort) sort.addEventListener('change', () => { st.sort = sort.value; store.set('libSort', st.sort); paint(); sort.blur(); });
    const qIn = $('#sh-q', panel);
    if (qIn) qIn.addEventListener('input', debounce(() => { st.q = qIn.value; paint(); }, 120));
  }

  ABC.Shows = { FILTERS, aux, loadAux, statusChip, posterCard, listRow, mediaCard, newState, panelHtml, bodyHtml, bind, airsIn };

  // ---------------------------------------------------------------- Shows tab  #/shows  (alias #/library)
  Views.shows = (m, params) => {
    const st = newState('page', params);
    return {
      title: 'Shows', nav: 'shows',
      async load() {
        const [shows, x] = await Promise.all([API.shows(), loadAux()]);
        indexShows(shows);
        st.shows = shows || [];
        st.ax = x.ax;
        return st;
      },
      render(_d, first) {
        return h`<div class="page">
          <section class="panel glass sec ${A(first)}" id="shows-panel" aria-labelledby="shows-h">
            <div class="sec-head"><h1 id="shows-h">Shows<span class="count">${fNum(st.shows.length)}</span></h1><a class="btn btn-sm" href="#/add">${ico('plus')}Add shows</a></div>
            ${panelHtml(st)}
          </section>
        </div>`;
      },
      mount(root) {
        if (S.last) paintFromLast();
        bind($('#shows-panel', root), st);
      },
      refreshOn: (ev) => /^(show|file|episode|download|airing|release)\./.test(ev.kind),
    };
  };
});

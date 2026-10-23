/* AnimeBeltConveyor web UI: Home = one scrollable page (SPEC-v1.2-ui.md 3.1-3.4):
   Play next rail, Needs you cards, Your shows (shows.js), This week strip. Activity lives in drawer.js.
   Loaded before app.js; app.js calls the function below with its internals. Plain JS, no dependencies. */
'use strict';
(window.ABC_EXT = window.ABC_EXT || []).push((ABC) => {
  const { $, $$, h, raw, ico, S, Views, API, soft, A, showTitle, progressOf, fNum, fFull, fRel, fWhen, fTime, F, safeUrl, isHex, readableAccent, nowSec, plural,
    indexShows, touchShow, paintFromLast, weekStart, weekDays, dayKey, toDate, calChips, calState, needsSetup, setupTodo, lowDisk, driveOf, GIB, motionOK, epNum } = ABC;
  const DAY = 86400;
  const MAX_NEEDS = 6;
  const colorVars = (c) => (isHex(c) ? raw(`style="--c:${c};--cc:${readableAccent(c).accent}"`) : '');
  const relLabel = (f) => (f ? [f.release_group, f.resolution, f.version > 1 ? 'v' + f.version : ''].filter(Boolean).join(' ') : '');

  // ---------------------------------------------------------------- 3.1 Play next
  function heroCard(e) {
    const show = S.showsById[e.show_id] || {};
    const f = e.file;
    const g = progressOf(show);
    const title = showTitle(show.id ? show : e);
    const rel = relLabel(f);
    return h`<article class="up-card up-hero" role="listitem" ${colorVars(e.color || show.color)}>
      <div class="up-hero-art" data-bg="${e.banner_url || show.banner_url || ''}" aria-hidden="true"></div>
      <img class="up-hero-poster" src="${safeUrl(e.cover_url || show.cover_url)}" alt="" loading="eager">
      <div class="up-hero-text">
        <div class="eyebrow"><span class="bar"></span>Up next</div>
        <h3 class="up-hero-title clamp2"><a href="#/show/${e.show_id}">${title}</a></h3>
        <p class="up-ep"><b>Episode ${epNum(e.number, e.abs_number)}</b>${rel ? ` · ${rel}` : ''}</p>
        <p class="up-meta">${e.aired_at ? h`<span title="${fFull(e.aired_at)}">Aired ${fRel(e.aired_at)}</span>` : ''}${show.id ? h`<span>${fNum(g.watched)} of ${fNum(g.aired)} aired watched</span>` : ''}</p>
        <div class="up-acts">
          ${f ? h`<button class="btn btn-primary btn-lg" data-act="play" data-file="${f.id}" data-show="${e.show_id}" data-label="episode ${e.number}">${ico('play')}Play</button>` : ''}
          <button class="btn" data-act="watched" data-show="${e.show_id}" data-ep="${e.number}" data-val="1" aria-label="Mark ${title} episode ${e.number} watched">${ico('check')}Watched</button>
          ${f ? h`<button class="btn btn-icon" data-act="reveal" data-file="${f.id}" aria-label="Show episode ${e.number} in its folder" data-tip="Show in folder">${ico('folder')}</button>` : ''}
        </div>
      </div>
    </article>`;
  }
  function upCard(e) {
    const show = S.showsById[e.show_id] || {};
    const f = e.file;
    const title = showTitle(show.id ? show : e);
    const rel = relLabel(f);
    return h`<article class="up-card" role="listitem" ${colorVars(e.color || show.color)}>
      <a class="up-poster" href="#/show/${e.show_id}" tabindex="-1" aria-hidden="true"><img src="${safeUrl(e.cover_url || show.cover_url)}" alt="" loading="lazy"></a>
      <h3 class="up-title clamp2"><a href="#/show/${e.show_id}">${title}</a></h3>
      <p class="up-ep">Episode ${epNum(e.number, e.abs_number)}${rel ? ` · ${rel}` : ''}</p>
      <div class="up-acts">
        ${f ? h`<button class="btn btn-primary up-play" data-act="play" data-file="${f.id}" data-show="${e.show_id}" data-label="episode ${e.number}" aria-label="Play ${title} episode ${e.number}">${ico('play')}Play</button>` : ''}
        <button class="btn btn-icon up-tick" data-act="watched" data-show="${e.show_id}" data-ep="${e.number}" data-val="1" aria-label="Mark ${title} episode ${e.number} watched" data-tip="Mark watched">${ico('check')}</button>
      </div>
    </article>`;
  }
  function railHtml(items, label) {
    return h`<div class="rail-wrap">
      <button type="button" class="rail-arrow is-prev" data-rail="-1" aria-label="Scroll ${label} left" tabindex="-1">${ico('chevL')}</button>
      <div class="rail-scroll" role="list" aria-label="${label}">${items}</div>
      <button type="button" class="rail-arrow is-next" data-rail="1" aria-label="Scroll ${label} right" tabindex="-1">${ico('chevR')}</button>
    </div>`;
  }

  // ---------------------------------------------------------------- 3.2 Needs you
  const span = (x) => (x.first_episode === x.last_episode ? `Episode ${x.first_episode} has no file` : `Episodes ${x.first_episode}-${x.last_episode} have no file`);
  function missingCard(x) {
    const show = S.showsById[x.show_id] || {};
    const name = x.title || showTitle(show);
    const gap = x.count < x.last_episode - x.first_episode + 1 ? ` (${fNum(x.count)} of them)` : '';
    return h`<article class="need-card" ${colorVars(x.color || show.color)}>
      <img class="need-thumb" src="${safeUrl(x.cover_url || show.cover_url)}" alt="" loading="lazy">
      <div class="need-main">
        <h3 class="need-title clamp2"><a href="#/show/${x.show_id}">${name}</a></h3>
        <p class="need-text" data-missing-line>${span(x)}${gap}</p>
        <div class="need-acts">
          <button class="btn btn-sm btn-primary" data-act="hero-download-missing" data-show="${x.show_id}" aria-label="Download all missing episodes of ${name}">${ico('download')}Download all</button>
          <a class="btn btn-sm" href="#/show/${x.show_id}" aria-label="Open ${name}">Open show</a>
        </div>
      </div>
    </article>`;
  }
  function unreachableCard(dd) {
    return h`<article class="need-card is-bad" role="alert">
      <span class="need-ico">${ico('alert')}</span>
      <div class="need-main">
        <h3 class="need-title">Download folder not reachable</h3>
        <p class="need-text">Download folder <b class="mono">${dd.path}</b> is not reachable. Plug the drive in, or pick another folder.</p>
        <div class="need-acts">
          <button class="btn btn-sm" data-act="retry-state">${ico('refresh')}Retry</button>
          <a class="btn btn-sm" href="#/settings?section=library">${ico('folder')}Pick folder</a>
        </div>
      </div>
    </article>`;
  }
  function lowDiskCard(dd) {
    const free = Math.max(0, dd.free_bytes) / GIB;
    return h`<article class="need-card is-bad" role="status" data-banner="lowdisk">
      <span class="need-ico">${ico('disk')}</span>
      <div class="need-main">
        <h3 class="need-title">Disk almost full</h3>
        <p class="need-text">Only ${free.toFixed(1)} GiB free on <b class="mono">${driveOf(dd.path)}</b>. Automatic downloads are paused until there is more room.</p>
        <div class="need-acts"><button class="btn btn-sm" data-act="open-dir" data-dir="${dd.path}" title="${dd.path}">${ico('folder')}Open folder</button></div>
      </div>
    </article>`;
  }
  function setupNeedCard(st) {
    const todo = setupTodo(st);
    const left = todo.filter(([done]) => !done).length;
    return h`<article class="need-card is-setup" aria-labelledby="setup-h">
      <span class="need-ico">${ico('compass')}</span>
      <div class="need-main">
        <h3 class="need-title" id="setup-h">Finish setting up</h3>
        <p class="need-text">${left ? `${plural(left, 'step')} left. The guide takes about two minutes.` : 'All set.'}</p>
        <ul class="setup-todo">${todo.map(([done, label]) => h`<li class="${done ? 'is-done' : ''}"><span class="step-n" aria-hidden="true">${done ? ico('check') : ''}</span>${label}<span class="sr-only">${done ? ': done' : ': to do'}</span></li>`)}</ul>
        <div class="need-acts"><a class="btn btn-sm btn-primary" href="#/setup">${ico('compass')}Open the setup guide</a></div>
      </div>
    </article>`;
  }
  /** Problem cards first (they block everything), then setup, then one card per show with missing episodes. */
  function needs(D) {
    const dd = D.state.download_dir;
    const out = [];
    if (dd && dd.exists === false) out.push(unreachableCard(dd));
    else if (lowDisk(dd)) out.push(lowDiskCard(dd));
    if (needsSetup(D)) out.push(setupNeedCard(D.state));
    const miss = (D.missing || []).filter((x) => x && x.count > 0);
    const room = Math.max(0, MAX_NEEDS - out.length);
    for (const x of miss.slice(0, room)) out.push(missingCard(x));
    return { cards: out, more: Math.max(0, miss.length - room), total: out.length + Math.max(0, miss.length - room) };
  }
  const needsGrid = (N) => h`<div class="needs-grid">${N.cards}</div>${N.more ? h`<p class="needs-more"><a href="#/shows">and ${fNum(N.more)} more ${N.more === 1 ? 'show' : 'shows'} with missing episodes</a></p>` : ''}`;

  // ---------------------------------------------------------------- view
  Views.home = () => {
    const st = { shows: ABC.Shows.newState('home'), railX: 0 };
    return {
      title: 'Home', nav: 'home',
      async load() {
        const ws = weekStart(0);
        const from = Math.floor(ws / 1000); const to = from + 7 * DAY;
        const calFrom = Math.min(from, nowSec() - 28 * DAY); // older items feed the "Downloading" chip (shows.js aux)
        const [state, upnext, shows, cal, settings, missing] = await Promise.all([
          API.state(), soft(API.upnext(), []), soft(API.shows(), []), soft(API.calendar(calFrom, to), []),
          S.settings ? Promise.resolve(S.settings) : soft(API.settings(), null).then((r) => r.v),
          soft(API.missing(), []), // SPEC-v1.1 11; an older server (404) just has no missing cards
        ]);
        if (settings) S.settings = settings;
        S.state = state;
        ABC.paintStatus();
        indexShows(shows.v);
        st.shows.shows = shows.v || [];
        st.shows.ax = ABC.Shows.aux(upnext.v, missing.v, cal.v);
        return { state, upnext, shows, cal, ws, from, to, missing: Array.isArray(missing.v) ? missing.v : [] };
      },
      render(D, isFirst) {
        const upnext = D.upnext.v || [];
        if (upnext[0]) touchShow({ ...(S.showsById[upnext[0].show_id] || {}), ...upnext[0] });
        else paintFromLast();
        const N = needs(D);
        const hasShows = st.shows.shows.length > 0;
        // 3.1: when nothing is on disk, the first panel carries the Needs you cards and reads "Nothing to play yet".
        let play;
        if (upnext.length) {
          play = h`<section class="panel glass sec sec-play ${A(isFirst)}" aria-labelledby="play-h">
            <div class="sec-head"><h2 id="play-h">Play next<span class="count">${fNum(upnext.length)}</span></h2></div>
            ${railHtml(upnext.map((e, i) => (i === 0 ? heroCard(e) : upCard(e))), 'Play next')}
            ${D.upnext.err ? h`<p class="note">Up next could not load: ${D.upnext.err.message}</p>` : ''}
          </section>`;
        } else {
          const shows = st.shows.shows;
          const next = shows.filter((s) => s.list_status === 'watching' && s.next && s.next.aired_at).sort((a, b) => a.next.aired_at - b.next.aired_at)[0];
          let line;
          if (!hasShows) line = h`<div class="one-line">${ico('tv')}<span>No shows yet. Add one and ABC fetches new episodes as they air.</span><a class="btn btn-sm btn-primary" href="#/add">${ico('plus')}Add a show</a></div>`;
          else if (N.cards.length) line = needsGrid(N);
          else if (next) line = h`<div class="one-line">${ico('clock')}<span>You are caught up. Next: <a href="#/show/${next.id}"><b>${showTitle(next)}</b></a> episode ${next.next.episode}, ${fWhen(next.next.aired_at)} (${fRel(next.next.aired_at)}).</span></div>`;
          else line = h`<div class="one-line">${ico('check')}<span>You are caught up. Nothing unwatched on disk and nothing scheduled.</span></div>`;
          play = h`<section class="panel glass sec sec-play is-empty ${A(isFirst)}" aria-labelledby="play-h">
            <div class="sec-head"><h2 id="play-h">${hasShows ? 'Nothing to play yet' : 'Play next'}${N.total && hasShows ? h`<span class="count">${fNum(N.total)}</span>` : ''}</h2></div>
            ${line}
          </section>`;
        }
        const needSec = upnext.length && N.cards.length ? h`<section class="panel glass sec sec-needs ${A(isFirst)}" aria-labelledby="needs-h">
            <div class="sec-head"><h2 id="needs-h">Needs you<span class="count">${fNum(N.total)}</span></h2></div>
            ${needsGrid(N)}
          </section>` : '';
        const dd = D.state.download_dir;
        const showsSec = h`<section class="panel glass sec sec-shows ${A(isFirst)}" id="home-shows" aria-labelledby="shows-h">
            <div class="sec-head"><h2 id="shows-h">Your shows</h2>
              ${dd && dd.exists ? h`<button class="btn btn-sm btn-ghost" data-act="open-dir" data-dir="${dd.path}" title="${dd.path}" aria-label="Open the download folder">${ico('folder')}<span class="lbl-wide">Open folder</span></button>` : ''}
              <a class="btn btn-sm btn-ghost" href="#/shows">See all shows${ico('chevR')}</a></div>
            ${ABC.Shows.panelHtml(st.shows)}
          </section>`;
        // 3.4 This week
        const days = weekDays(D.ws);
        const chips = calChips(D.cal.v).filter((c) => c.ts >= D.from && c.ts < D.to);
        const byDay = {};
        for (const c of chips) (byDay[dayKey(toDate(c.ts))] ||= []).push(c);
        const today = dayKey(new Date());
        const weekSec = h`<section class="panel glass sec sec-week ${A(isFirst)}" aria-labelledby="week-h">
            <div class="sec-head"><h2 id="week-h">This week<span class="count">${chips.length ? fNum(chips.length) : ''}</span></h2><a class="btn btn-sm btn-ghost" href="#/calendar">Open calendar${ico('chevR')}</a></div>
            ${D.cal.err ? h`<p class="note">The calendar could not load: ${D.cal.err.message}</p>` : ''}
            ${chips.length ? h`<div class="week-grid">
              ${days.map((d) => {
                const k = dayKey(d); const list = byDay[k] || [];
                return h`<div class="day ${k === today ? 'is-today' : ''} ${list.length ? '' : 'is-empty'}" ${k === today ? raw('aria-current="date"') : ''}>
                  <div class="day-head"><span>${k === today ? 'Today' : F.wd.format(d)}</span><b>${F.day.format(d)}</b></div>
                  ${list.length ? list.map(ABC.weekEntry) : h`<span class="day-empty">Nothing airs</span>`}
                </div>`;
              })}
            </div>` : h`<div class="one-line">${ico('calendar')}<span>Nothing on your list airs this week.</span></div>`}
          </section>`;
        return h`<div class="page home">${play}${needSec}${showsSec}${weekSec}</div>`;
      },
      mount(root) {
        ABC.Shows.bind($('#home-shows', root), st.shows);
        const sc = $('.rail-scroll', root);
        if (sc) {
          sc.scrollLeft = st.railX;
          bindRail(sc, (x) => { st.railX = x; });
        }
        const wg = $('.week-grid', root); const t = wg && $('.day.is-today', wg);
        if (t && wg.scrollWidth > wg.clientWidth) wg.scrollLeft = t.offsetLeft - 8;
      },
      refreshOn: () => true,
    };
  };

  /** Rail: arrows show on hover when there is more to see; mouse drag scrolls (touch and shift+wheel are native). */
  function bindRail(sc, onScroll) {
    const wrap = sc.parentElement;
    const prev = $('.rail-arrow.is-prev', wrap); const next = $('.rail-arrow.is-next', wrap);
    const sync = () => {
      const max = sc.scrollWidth - sc.clientWidth;
      wrap.classList.toggle('can-prev', sc.scrollLeft > 4);
      wrap.classList.toggle('can-next', sc.scrollLeft < max - 4);
      onScroll(sc.scrollLeft);
    };
    sc.addEventListener('scroll', sync, { passive: true });
    new ResizeObserver(sync).observe(sc);
    sync();
    for (const b of [prev, next]) {
      b.addEventListener('click', () => sc.scrollBy({ left: Number(b.dataset.rail) * Math.max(240, sc.clientWidth * 0.8), behavior: motionOK() ? 'smooth' : 'auto' }));
    }
    let drag = null;
    sc.addEventListener('pointerdown', (e) => {
      if (e.pointerType !== 'mouse' || e.button !== 0 || e.target.closest('button, a, input, select')) return;
      drag = { x: e.clientX, left: sc.scrollLeft, moved: false, id: e.pointerId };
    });
    sc.addEventListener('pointermove', (e) => {
      if (!drag || e.pointerId !== drag.id) return;
      const dx = e.clientX - drag.x;
      if (!drag.moved && Math.abs(dx) > 5) { drag.moved = true; sc.setPointerCapture(e.pointerId); sc.classList.add('is-dragging'); }
      if (drag.moved) sc.scrollLeft = drag.left - dx;
    });
    const end = () => { if (drag && drag.moved) { sc.classList.remove('is-dragging'); const stop = (ev) => { ev.stopPropagation(); ev.preventDefault(); }; sc.addEventListener('click', stop, { capture: true, once: true }); setTimeout(() => sc.removeEventListener('click', stop, { capture: true }), 0); } drag = null; };
    sc.addEventListener('pointerup', end);
    sc.addEventListener('pointercancel', end);
  }
});

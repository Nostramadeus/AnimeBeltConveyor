/* AnimeBeltConveyor web UI: activity drawer (SPEC-v1.2-ui.md 3.5). Opened by Updates in the top bar; unread
   count since the last open; consecutive events of the same kind for the same show collapse into one line
   with an expand chevron; on screens >= 1800 px it can be pinned open (remembered).
   Loaded before app.js; app.js calls the function below with its internals. Plain JS, no dependencies. */
'use strict';
(window.ABC_EXT = window.ABC_EXT || []).push((ABC) => {
  const { $, $$, h, ico, store, S, showTitle, fNum, fFull, fRel } = ABC;
  const KIND = {
    'show.added': ['plus', 'k-acc'], 'show.removed': ['x', ''], 'airing.updated': ['calendar', ''],
    'release.found': ['search', ''], 'release.added': ['download', 'k-warn'], 'release.waiting': ['clock', ''], 'release.skipped': ['skip', ''],
    'download.done': ['check', 'k-ok'], 'download.failed': ['alert', 'k-bad'], 'file.found': ['file', 'k-ok'], 'file.missing': ['alert', 'k-warn'],
    'episode.watched': ['eye', 'k-acc'], 'episode.unwatched': ['eyeOff', ''], 'push.ok': ['upload', 'k-ok'], 'push.failed': ['alert', 'k-bad'],
    'scan.done': ['folder', ''], error: ['alert', 'k-bad'],
  };
  const MAX = 120;
  const wide = matchMedia('(min-width: 1800px)');
  const D = { open: false, pinned: store.get('drawerPinned') === true, seen: Number(store.get('seenEvent')) || 0, expanded: new Set(), flash: new Set(), live: 'connecting', returnTo: null };
  const pinnedNow = () => D.pinned && wide.matches;

  /** Consecutive events (newest first) with the same kind and the same show become one group. */
  function groups(list) {
    const out = [];
    for (const ev of list) {
      const g = out[out.length - 1];
      if (g && g.kind === ev.kind && (g.show_id ?? null) === (ev.show_id ?? null)) g.items.push(ev);
      else out.push({ kind: ev.kind, show_id: ev.show_id ?? null, items: [ev], key: String(ev.id) });
    }
    return out;
  }
  /** "1-7", "3, 5, 8-9": episode numbers of a group, ascending, runs collapsed. */
  function runs(nums) {
    const n = [...new Set(nums)].sort((a, b) => a - b);
    const parts = [];
    for (let i = 0; i < n.length; i++) {
      let j = i;
      while (j + 1 < n.length && n[j + 1] === n[j] + 1) j++;
      parts.push(i === j ? `${n[i]}` : `${n[i]}-${n[j]}`);
      i = j;
    }
    return parts.join(', ');
  }
  const showName = (id) => (id != null && S.showsById[id] ? showTitle(S.showsById[id]) : '');
  /** The one line for a group of n > 1 events. Falls back to the newest message plus a count. */
  function groupText(g) {
    const n = g.items.length;
    const name = showName(g.show_id);
    const eps = g.items.map((e) => e.episode).filter((x) => Number.isFinite(x));
    const who = name ? `${name}: ` : '';
    switch (g.kind) {
      case 'episode.watched': return eps.length === n ? `${who}${eps.length === 1 ? 'episode' : 'episodes'} ${runs(eps)} marked watched` : `${who}${n} watched updates`;
      case 'episode.unwatched': return eps.length === n ? `${who}episodes ${runs(eps)} marked unwatched` : `${who}${n} unwatched updates`;
      case 'release.found': return `${who}${n} releases found`;
      case 'release.added': return `${who}${n} releases added`;
      case 'release.skipped': return `${who}${n} releases skipped`;
      case 'download.done': return `${who}${n} downloads finished`;
      case 'download.failed': return `${who}${n} downloads failed`;
      case 'file.found': return `${who}${n} new files`;
      case 'file.missing': return `${who}${n} files gone`;
      case 'scan.done': return `${n} scans finished`;
      default: return `${g.items[0].message || g.kind} (+${n - 1} more)`;
    }
  }
  const timeEl = (ts) => h`<time datetime="${new Date(ts * 1000).toISOString()}" title="${fFull(ts)}">${fRel(ts)}</time>`;
  function msgHtml(ev) {
    const show = ev.show_id != null ? S.showsById[ev.show_id] : null;
    const msg = ev.message || ev.kind;
    const low = msg.toLowerCase();
    const named = show && [show.title, show.title_romaji, show.title_english].some((t) => t && low.includes(t.toLowerCase()));
    return { named, show, html: named ? h`<a href="#/show/${show.id}">${msg}</a>` : msg };
  }
  /** The raw error text behind a plain-words error line (data.detail), folded under "Details". */
  function detailHtml(ev) {
    const d = ev.data && typeof ev.data === 'object' ? ev.data.detail : null;
    if (typeof d !== 'string' || !d || d === ev.message) return '';
    return h`<details class="feed-detail"><summary>Details</summary><code>${d}</code></details>`;
  }
  function itemHtml(ev) {
    const [icon, tone] = KIND[ev.kind] || ['bolt', ''];
    const m = msgHtml(ev);
    return h`<li class="feed-item ${D.flash.has(ev.id) ? 'is-new' : ''}" data-id="${ev.id}">
      <span class="feed-icon ${tone}" title="${ev.kind}">${ico(icon)}</span>
      <div class="feed-body"><div class="feed-msg">${m.html}</div>
      <div class="feed-meta">${m.show && !m.named ? h`<a href="#/show/${m.show.id}">${showTitle(m.show)}</a> · ` : ''}${timeEl(ev.ts)}</div>${detailHtml(ev)}</div>
    </li>`;
  }
  function groupHtml(g) {
    if (g.items.length === 1) return itemHtml(g.items[0]);
    const [icon, tone] = KIND[g.kind] || ['bolt', ''];
    const open = D.expanded.has(g.key);
    const top = g.items[0];
    const show = g.show_id != null ? S.showsById[g.show_id] : null;
    const isNew = g.items.some((e) => D.flash.has(e.id));
    return h`<li class="feed-item feed-group ${isNew ? 'is-new' : ''}" data-id="${top.id}">
      <span class="feed-icon ${tone}" title="${g.kind}">${ico(icon)}</span>
      <div class="feed-body">
        <div class="feed-msg">${show ? h`<a href="#/show/${show.id}">${groupText(g)}</a>` : groupText(g)}</div>
        <div class="feed-meta">${fNum(g.items.length)} events · ${timeEl(top.ts)}</div>
        <ul class="feed-sub" id="fg-${g.key}" ${open ? '' : ABC.raw('hidden')}>${g.items.map((ev) => h`<li><span class="feed-msg">${msgHtml(ev).html}</span> <span class="feed-meta">${timeEl(ev.ts)}</span>${detailHtml(ev)}</li>`)}</ul>
      </div>
      <button type="button" class="btn btn-sm btn-ghost btn-icon feed-exp" data-group="${g.key}" aria-expanded="${String(open)}" aria-controls="fg-${g.key}" aria-label="${open ? 'Hide' : 'Show'} the ${fNum(g.items.length)} events">${ico('chevD')}</button>
    </li>`;
  }
  function paintList() {
    const box = $('#feed-list');
    if (!box) return;
    const evs = S.events.slice(0, MAX);
    box.innerHTML = evs.length ? String(h`${groups(evs).map(groupHtml)}`)
      : String(h`<li class="feed-empty">${ico('bolt')}<span>Nothing yet. Searches, downloads, new files and watched episodes show up here as they happen.</span></li>`);
    D.flash.clear();
  }
  const maxId = () => S.events.reduce((a, e) => Math.max(a, Math.floor(e.id)), 0);
  function unread() {
    // A server that was reset hands out lower ids again; treat that as "never opened".
    if (D.seen > maxId() && S.events.length) { D.seen = 0; store.set('seenEvent', 0); }
    return S.events.filter((e) => e.id > D.seen).length;
  }
  function markSeen() { D.seen = maxId(); store.set('seenEvent', D.seen); }
  function paintBadge() {
    const n = D.open ? 0 : unread();
    const b = $('#live-unread'); const btn = $('#live-btn');
    if (b) { b.textContent = n > 99 ? '99+' : String(n); b.hidden = n === 0; }
    if (btn) btn.setAttribute('aria-label', `Updates${n ? `, ${n} unread` : ''}. Connection: ${ABC.liveLabel().toLowerCase()}`);
  }
  function paintShell() {
    const el = $('#drawer'); const btn = $('#live-btn'); const scrim = $('#drawer-scrim'); const pin = $('#drawer-pin');
    if (!el) return;
    const pinned = pinnedNow();
    el.classList.toggle('is-open', D.open);
    el.classList.toggle('is-pinned', pinned && D.open);
    document.body.classList.toggle('drawer-pinned', pinned && D.open);
    if (btn) btn.setAttribute('aria-expanded', String(D.open));
    if (scrim) scrim.hidden = !D.open || pinned;
    if (pin) { pin.setAttribute('aria-pressed', String(D.pinned)); pin.dataset.tip = D.pinned ? 'Unpin: close it like a drawer' : 'Pin open on wide screens'; }
    paintBadge();
  }
  function open(focus = true) {
    D.open = true;
    D.returnTo = document.activeElement;
    markSeen();
    paintList();
    paintShell();
    if (focus) setTimeout(() => $('#drawer-close') && $('#drawer-close').focus(), 30);
  }
  function close(refocus = true) {
    if (!D.open) return;
    D.open = false;
    if (D.pinned && wide.matches) { D.pinned = false; store.set('drawerPinned', false); }
    paintShell();
    if (refocus) $('#live-btn')?.focus();
  }
  const toggle = () => (D.open ? close() : open());

  /** Called by app.js for every live event. */
  function onEvent(ev) {
    D.flash.add(ev.id);
    if (D.open) { markSeen(); paintList(); }
    paintBadge();
  }
  function setLive(state) {
    D.live = state;
    const btn = $('#live-btn'); const t = $('#live-label'); const chip = $('#feed-live');
    if (btn) { btn.classList.toggle('is-live', state === 'live'); btn.classList.toggle('is-down', state === 'down'); }
    if (t) t.textContent = state === 'down' ? 'Offline' : 'Updates';
    if (chip) { chip.textContent = ABC.liveLabel(); chip.className = `chip ${state === 'live' ? 'chip-ok' : state === 'down' ? 'chip-bad' : ''}`; }
    paintBadge();
  }
  function init() {
    $('#live-btn')?.addEventListener('click', toggle);
    $('#drawer-close')?.addEventListener('click', () => close());
    $('#drawer-scrim')?.addEventListener('click', () => close(false));
    $('#drawer-pin')?.addEventListener('click', () => {
      D.pinned = !D.pinned; store.set('drawerPinned', D.pinned);
      if (D.pinned) D.open = true;
      paintShell();
    });
    $('#feed-list')?.addEventListener('click', (e) => {
      const b = e.target.closest('[data-group]');
      if (!b) return;
      const k = b.dataset.group;
      if (D.expanded.has(k)) D.expanded.delete(k); else D.expanded.add(k);
      const openNow = D.expanded.has(k);
      b.setAttribute('aria-expanded', String(openNow));
      b.setAttribute('aria-label', b.getAttribute('aria-label').replace(/^(Show|Hide)/, openNow ? 'Hide' : 'Show'));
      const sub = document.getElementById(b.getAttribute('aria-controls'));
      if (sub) sub.hidden = !openNow;
    });
    wide.addEventListener?.('change', paintShell);
    if (pinnedNow()) { D.open = true; markSeen(); }
    paintList();
    paintShell();
  }
  ABC.Drawer = { init, open, close, toggle, onEvent, setLive, paintList, paintBadge, groups, groupText, isOpen: () => D.open, isPinned: pinnedNow };
});

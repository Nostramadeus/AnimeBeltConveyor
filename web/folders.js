/* AnimeBeltConveyor web UI: folder picker modal and the Settings › Library folder manager.
   SPEC-v1.1.md 6.2 (folder manager) and 6.3 (picker). Loaded before app.js; app.js calls the function
   below with its internals (see "extensions" at the bottom of app.js). Plain JS, no dependencies. */
'use strict';
(window.ABC_EXT = window.ABC_EXT || []).push((ABC) => {
  const { $, $$, h, ico, API, toast, confirmBox, fSize, baseName, plural, isAbsPath, samePath, winOr, OS_WIN } = ABC;
  const EXAMPLE = winOr('D:\\Anime', '/home/you/Anime');

  // ---------------------------------------------------------------- picker modal (6.3)
  let dlg = null;
  function ensureDialog() {
    if (dlg) return dlg;
    dlg = document.createElement('dialog');
    dlg.id = 'picker';
    dlg.className = 'dialog surface picker';
    dlg.setAttribute('aria-labelledby', 'pk-title');
    document.body.appendChild(dlg);
    return dlg;
  }
  /** Split a path into breadcrumb steps: "D:\Anime\Fall" -> D:\, D:\Anime, D:\Anime\Fall. */
  function crumbsOf(path) {
    if (!path) return [];
    const win = /^[a-z]:/i.test(path) || path.includes('\\');
    const sep = win ? '\\' : '/';
    const parts = path.split(/[\\/]+/).filter(Boolean);
    const out = [];
    if (!win) { out.push(['/', '/']); let acc = ''; for (const p of parts) { acc += '/' + p; out.push([p, acc]); } return out; }
    if (/^\\\\/.test(path)) { // UNC: \\server\share\...
      let acc = '\\\\' + parts[0] + '\\' + (parts[1] || '');
      out.push([acc, acc]);
      for (const p of parts.slice(2)) { acc += sep + p; out.push([p, acc]); }
      return out;
    }
    let acc = parts[0] + sep; out.push([acc, acc]);
    for (const p of parts.slice(1)) { acc = acc.endsWith(sep) ? acc + p : acc + sep + p; out.push([p, acc]); }
    return out;
  }
  const freeTxt = (b) => (b == null ? '' : `${fSize(b)} free`);

  /**
   * Open the folder picker. Resolves {path, create} or null (Cancel / Escape).
   * context: 'download' (shows suggestion chips), 'library', 'torrent' (no "create" box: the watch folder must exist).
   */
  function pickFolder({ title = 'Choose a folder', context = 'library', initial = '', create = context === 'download' } = {}) {
    const d = ensureDialog();
    const st = { path: null, list: null, error: '', loading: true, seq: 0, suggest: null };
    const showCreate = context !== 'torrent';
    d.innerHTML = String(h`<form method="dialog" class="picker-body" novalidate>
      <div class="picker-head"><h2 id="pk-title">${title}</h2><button type="button" class="btn btn-sm btn-ghost btn-icon" data-pk="cancel" aria-label="Close">${ico('x')}</button></div>
      ${context === 'download' ? h`<div class="pk-suggest" id="pk-suggest" aria-label="Suggestions"><span class="tiny dim">Loading suggestions…</span></div>` : ''}
      <div class="pk-bar">
        <button type="button" class="btn btn-sm btn-icon" data-pk="up" aria-label="Up one folder" title="Up one folder">${ico('up')}</button>
        <nav class="pk-crumbs" id="pk-crumbs" aria-label="Current folder"></nav>
      </div>
      <div class="pk-list-wrap"><ul class="pk-list" id="pk-list" aria-label="Folders" aria-busy="true"></ul><p class="note pk-err" id="pk-err" role="status" hidden></p></div>
      <div class="field">
        <label for="pk-path">Folder path</label>
        <div class="row"><input class="input grow mono" id="pk-path" spellcheck="false" autocomplete="off" placeholder="${EXAMPLE}" value="${initial}" aria-describedby="pk-path-e"><button type="button" class="btn" data-pk="go">Go</button></div>
        <p class="err" id="pk-path-e" aria-live="polite"></p>
      </div>
      <div class="pk-foot">
        ${showCreate ? h`<label class="check"><input type="checkbox" class="switch" id="pk-create" ${create ? ABC.raw('checked') : ''}> Create it if missing</label>` : ''}
        <span class="grow"></span>
        <button type="button" class="btn" data-pk="cancel">Cancel</button>
        <button type="button" class="btn btn-primary" data-pk="choose">${ico('check')}Choose</button>
      </div>
    </form>`);
    const form = $('form', d);
    const input = $('#pk-path', d);
    const pathErr = $('#pk-path-e', d);
    let result = null;

    const paint = () => {
      const crumbs = crumbsOf(st.path);
      $('#pk-crumbs', d).innerHTML = String(h`<button type="button" class="pk-crumb" data-pk-go="" ${st.path ? '' : ABC.raw('aria-current="location"')}>${OS_WIN ? 'This PC' : 'Computer'}</button>${crumbs.map(([name, p], i) => h`<span class="pk-sep" aria-hidden="true">›</span><button type="button" class="pk-crumb" data-pk-go="${p}" ${i === crumbs.length - 1 ? ABC.raw('aria-current="location"') : ''}>${name}</button>`)}`);
      $('[data-pk="up"]', d).disabled = !st.path;
      const list = $('#pk-list', d);
      list.setAttribute('aria-busy', String(st.loading));
      if (st.loading) list.innerHTML = '<li class="pk-empty">Loading…</li>';
      else {
        const dirs = (st.list && st.list.dirs) || [];
        list.innerHTML = String(dirs.length ? h`${dirs.map((x) => h`<li><button type="button" class="pk-item" data-pk-go="${x.path}">${ico(st.path ? 'folder' : 'disk')}<span class="pk-name">${x.name}</span>${x.free_bytes != null ? h`<span class="pk-free">${freeTxt(x.free_bytes)}</span>` : ''}${ico('chevR', 'pk-chev')}</button></li>`)}`
          : h`<li class="pk-empty">${st.error ? '' : st.path ? 'No sub-folders here. Choose this folder, or type a new name at the end of the path.' : 'No drives found.'}</li>`);
      }
      const e = $('#pk-err', d);
      e.hidden = !st.error;
      e.textContent = st.error ? `${st.path || 'This folder'}: ${st.error}` : '';
    };
    const go = async (path) => {
      const my = ++st.seq;
      st.loading = true; st.error = ''; pathErr.textContent = '';
      paint();
      try {
        const r = await API.fsList(path);
        if (my !== st.seq) return;
        st.list = r; st.path = r && r.path != null ? r.path : path; st.error = (r && r.error) || '';
      } catch (err) {
        if (my !== st.seq) return;
        st.list = { path, parent: null, dirs: [] }; st.path = path; st.error = err.message;
      }
      st.loading = false;
      input.value = st.path;
      paint();
    };
    const choose = () => {
      const p = input.value.trim() || st.path || '';
      if (!p) { pathErr.textContent = 'Open a drive and a folder, or type a path.'; input.focus(); return; }
      if (!isAbsPath(p)) { pathErr.textContent = `Type the full path, for example ${EXAMPLE}.`; input.setAttribute('aria-invalid', 'true'); input.focus(); return; }
      const cb = $('#pk-create', d);
      result = { path: p, create: cb ? cb.checked : false };
      d.close();
    };
    form.addEventListener('click', (e) => {
      const t = e.target.closest('[data-pk],[data-pk-go]');
      if (!t) return;
      e.preventDefault();
      if (t.hasAttribute('data-pk-go')) { go(t.getAttribute('data-pk-go')); return; }
      const a = t.dataset.pk;
      if (a === 'cancel') d.close();
      else if (a === 'up') go(st.list && st.list.parent != null ? st.list.parent : '');
      else if (a === 'go') { const p = input.value.trim(); if (p && !isAbsPath(p)) { pathErr.textContent = `Type the full path, for example ${EXAMPLE}.`; return; } go(p); }
      else if (a === 'choose') choose();
    });
    form.addEventListener('submit', (e) => e.preventDefault());
    input.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); $('[data-pk="go"]', d).click(); } });
    input.addEventListener('input', () => { pathErr.textContent = ''; input.removeAttribute('aria-invalid'); });

    if (context === 'download') {
      API.fsSuggest().then((list) => {
        const box = $('#pk-suggest', d);
        if (!box) return;
        box.innerHTML = String((list || []).length ? h`<span class="tiny muted">Suggestions:</span>${list.map((s) => h`<button type="button" class="chip chip-btn" data-pk-pick="${s.path}" title="${s.path}${s.free_bytes != null ? ' · ' + freeTxt(s.free_bytes) : ''}">${s.label}</button>`)}` : '');
        box.addEventListener('click', (e) => { const b = e.target.closest('[data-pk-pick]'); if (!b) return; input.value = b.dataset.pkPick; pathErr.textContent = ''; input.focus(); });
      }, () => { const box = $('#pk-suggest', d); if (box) box.remove(); });
    }
    // Start where the typed path points when it is a folder that can be listed; else at the drive list.
    const start = initial && isAbsPath(initial) ? initial : '';
    go(start).then(() => { if (start && st.error) { const keep = initial; go('').then(() => { input.value = keep; }); } });
    d.showModal();
    input.focus();
    return new Promise((resolve) => d.addEventListener('close', () => { d.innerHTML = ''; resolve(result); }, { once: true }));
  }

  // ---------------------------------------------------------------- scan depth per folder (SPEC-v1.1 8)
  const SCAN_OPTS = [['flat', 'This folder only'], ['shows', 'Show folders (one level)'], ['deep', 'All subfolders']];
  const SCAN_HELP = 'One level is what qBittorrent needs. All subfolders on a big folder or a whole drive is slow and uses more memory: every file below it is checked on each rescan.';
  const scanOf = (f) => (SCAN_OPTS.some(([v]) => v === (f && f.scan)) ? f.scan : 'shows');
  let scanSeq = 0;
  /** "Scan" select for one folder row; changes PATCH /api/folders right away (see the change listener below). */
  function scanHtml(f) {
    const id = 'fm-scan-' + (++scanSeq);
    const cur = scanOf(f);
    return h`<label class="fm-scan" for="${id}"><span class="fm-scan-l" aria-hidden="true">Scan</span>
      <select class="select select-sm fm-scan-sel" id="${id}" data-fm-scan data-p="${f.path}" data-was="${cur}" aria-label="Scan depth for ${f.path}" aria-describedby="${id}-e">
        ${SCAN_OPTS.map(([v, l]) => h`<option value="${v}" ${v === cur ? ABC.raw('selected') : ''}>${l}</option>`)}
      </select></label>`;
  }
  const scanErrHtml = (f) => h`<p class="err fm-scan-e" id="fm-scan-${scanSeq}-e" aria-live="polite"></p>`;
  const scanHelpHtml = () => h`<p class="small muted fm-help">${SCAN_HELP}</p>`;
  /** Show a server error for one folder inline on its row (the select's error line). Returns false when there is no row. */
  function rowError(scope, path, msg) {
    const sel = $$('select[data-fm-scan]', scope).find((x) => x.dataset.p === path);
    const e = sel && document.getElementById(sel.getAttribute('aria-describedby'));
    if (!e) return false;
    e.textContent = msg;
    sel.setAttribute('aria-invalid', 'true');
    return true;
  }
  const folderErrMsg = (err) => {
    const fl = err && err.body && err.body.fields;
    const m = String((fl && (fl['library.folders'] || fl.scan || fl.path)) || (err && err.message) || 'Not saved');
    return m.charAt(0).toUpperCase() + m.slice(1);
  };
  // One delegated listener serves every scan select (Settings manager and setup step 2). The nearest
  // [data-fm-scope] element names who repaints: scopes[name](list, path) after a successful PATCH.
  const scopes = {};
  document.addEventListener('change', async (e) => {
    const sel = e.target.closest && e.target.closest('select[data-fm-scan]');
    if (!sel) return;
    const path = sel.dataset.p; const scan = sel.value; const was = sel.dataset.was;
    const box = sel.closest('[data-fm-scope]');
    const err = document.getElementById(sel.getAttribute('aria-describedby'));
    if (err) err.textContent = '';
    sel.removeAttribute('aria-invalid');
    sel.disabled = true;
    try {
      const list = await API.patchFolder({ path, scan });
      sel.dataset.was = scan;
      const fn = box && scopes[box.dataset.fmScope];
      if (fn) fn(list, path);
      toast(scan === 'deep' ? 'Saved. Every subfolder is scanned now.' : scan === 'flat' ? 'Saved. Only files directly in this folder are scanned.' : 'Saved. The folder and its show folders are scanned.', 'ok');
    } catch (ex) {
      sel.value = was;
      if (!err) { toast(folderErrMsg(ex), 'bad'); return; }
      err.textContent = folderErrMsg(ex);
      sel.setAttribute('aria-invalid', 'true');
    } finally {
      sel.disabled = false;
      // A repaint replaced the select: keep keyboard focus on the same row.
      const now = $$('select[data-fm-scan]', box || document).find((x) => x.dataset.p === path);
      if (now && document.activeElement !== now && (document.activeElement === document.body || !document.activeElement || document.activeElement === sel)) now.focus();
    }
  });
  /** Body for POST /api/folders: a folder new to the list starts at "shows"; a known one (e.g. a past folder) keeps its depth. */
  const addBody = (paths, body) => ((paths || []).some((p) => samePath(p, body.path)) ? body : { ...body, scan: 'shows' });
  const known = (box) => [...new Set($$('[data-p]', box).map((x) => x.dataset.p))];
  /** True when a 400 is about a folder rule (shown on the row) rather than a generic failure (toast). */
  const isRowErr = (err) => { const fl = err && err.body && err.body.fields; return !!(fl && (fl['library.folders'] || fl.scan)); };

  // ---------------------------------------------------------------- folder manager (6.2)
  const nameOf = (f) => f.label || baseName(String(f.path).replace(/[\\/]+$/, '')) || f.path;
  function rowHtml(f) {
    const past = !f.active;
    return h`<li class="fm-row ${past ? 'is-past' : ''} ${f.download ? 'is-dl' : ''}">
      <span class="fm-ico">${ico(f.download ? 'download' : 'folder')}</span>
      <div class="fm-main">
        <div class="fm-name">${nameOf(f)}</div>
        <div class="fm-path mono" title="${f.path}">${f.path}</div>
        <div class="fm-meta">
        <div class="fm-chips">
          ${f.download ? h`<span class="chip chip-accent">Downloads go here</span>` : ''}
          ${f.exists === false ? h`<span class="chip chip-bad">Not reachable</span>` : ''}
          ${past ? h`<span class="chip chip-dim">Past folder</span>` : ''}
          ${!past && f.exists !== false && f.file_count != null ? h`<span class="chip">${plural(f.file_count, 'file')}</span>` : ''}
          ${!past && f.exists !== false && f.free_bytes != null ? h`<span class="chip">${freeTxt(f.free_bytes)}</span>` : ''}
        </div>
        ${past ? '' : scanHtml(f)}
        </div>
        ${past ? '' : scanErrHtml(f)}
      </div>
      <div class="fm-acts">
        ${past ? h`
          <button type="button" class="btn btn-sm" data-fm="readd" data-p="${f.path}">${ico('plus')}Re-add</button>
          <button type="button" class="btn btn-sm btn-ghost" data-fm="forget" data-p="${f.path}">${ico('trash')}Forget</button>` : h`
          <button type="button" class="btn btn-sm" data-fm="open" data-p="${f.path}" ${f.exists === false ? ABC.raw('disabled title="Not reachable"') : ''}>${ico('folder')}Open</button>
          ${f.download ? '' : h`<button type="button" class="btn btn-sm" data-fm="make" data-p="${f.path}">${ico('download')}Make download folder</button>`}
          <button type="button" class="btn btn-sm btn-ghost" data-fm="remove" data-p="${f.path}" title="Stop scanning it. It stays listed as a past folder.">${ico('x')}Remove</button>`}
      </div>
    </li>`;
  }
  function listHtml(list) {
    const act = (list || []).filter((f) => f.active);
    const past = (list || []).filter((f) => !f.active);
    return h`${act.length ? h`<ul class="fm-list">${act.map(rowHtml)}</ul>${scanHelpHtml()}` : h`<p class="note info">No folders yet. Add the folder where new episodes should go.</p>`}
      ${act.length && !act.some((f) => f.download) ? h`<p class="note">No download folder. qBittorrent saves to its own default folder until you pick one with <b>Make download folder</b>.</p>` : ''}
      ${past.length ? h`<h3 class="fm-sub">Past folders <span class="dim small">remembered, not scanned</span></h3><ul class="fm-list">${past.map(rowHtml)}</ul>` : ''}`;
  }
  function managerHtml(list, err) {
    return h`<div class="fm span2" id="folder-manager" data-fm-scope="settings">
      <div class="fm-head"><h3>Folders</h3><p class="small muted">ABC saves new episodes into the download folder and scans every folder in the list. It never writes anywhere else.</p></div>
      ${err ? h`<p class="note">Folder list could not load: ${err.message}</p>` : ''}
      <div id="fm-body">${listHtml(list)}</div>
      <p class="err" id="fm-err" aria-live="polite"></p>
      <div class="row" style="margin-top:12px"><button type="button" class="btn btn-sm" data-fm="add">${ico('folderPlus')}Add folder</button><span class="small muted" id="fm-msg" aria-live="polite"></span></div>
    </div>`;
  }
  /** Bind the manager's buttons. onChange(list) gets the fresh FolderState list after every change. */
  function bindManager(box, onChange) {
    const paint = (list) => { $('#fm-body', box).innerHTML = String(listHtml(list)); const fe = $('#fm-err', box); if (fe) fe.textContent = ''; onChange(list); };
    scopes.settings = (list) => paint(list);
    box.addEventListener('click', async (e) => {
      const b = e.target.closest('[data-fm]');
      if (!b || b.classList.contains('is-busy')) return;
      e.preventDefault();
      const p = b.dataset.p;
      b.classList.add('is-busy');
      try {
        switch (b.dataset.fm) {
          case 'add': {
            const r = await pickFolder({ title: 'Add a folder', context: 'library', create: false });
            if (!r) break;
            paint(await API.addFolder(addBody(known(box), { path: r.path, create: r.create })));
            toast('Folder added. Scanning it now.', 'ok');
            break;
          }
          case 'open': await API.openFolder(p); toast('Opened the folder', 'ok'); break;
          case 'make': paint(await API.patchFolder({ path: p, download: true })); toast('New episodes now go to this folder.', 'ok'); break;
          case 'remove': paint(await API.patchFolder({ path: p, active: false })); toast('Moved to past folders. Its files are hidden until you re-add it.', 'ok'); break;
          case 'readd': paint(await API.patchFolder({ path: p, active: true })); toast('Folder re-added. Scanning it now.', 'ok'); break;
          case 'forget': {
            const { ok } = await confirmBox({ title: 'Forget this folder?', body: h`<p>ABC stops remembering <b class="mono">${p}</b>. Nothing on disk is deleted.</p>`, ok: 'Forget' });
            if (!ok) break;
            paint(await API.deleteFolder(p));
            toast('Folder forgotten', 'ok');
            break;
          }
          default: break;
        }
      } catch (err) {
        // A folder rule the server rejects (e.g. a "flat" folder cannot become the download folder) shows on its row.
        if (!(p && isRowErr(err) && rowError(box, p, folderErrMsg(err)))) toast(folderErrMsg(err), 'bad');
      } finally { b.classList.remove('is-busy'); }
    });
  }

  ABC.Folders = { pickFolder, managerHtml, bindManager, crumbsOf, scanHtml, scanErrHtml, scanHelpHtml, scopes, addBody, known, rowError, isRowErr, folderErrMsg, SCAN_OPTS };

  // Shared action: Browse… next to a watch-folder field (Settings › Downloads and setup step 3).
  ABC.ACTIONS['pick-torrent-dir'] = async (btn, ds) => {
    const input = $('#' + ds.target);
    const r = await pickFolder({ title: 'Folder your torrent app watches', context: 'torrent', initial: input ? input.value.trim() : '' });
    if (!r || !input) return;
    input.value = r.path;
    input.dispatchEvent(new Event('input', { bubbles: true }));
  };
});

/* AnimeBeltConveyor web UI: the setup guide (#/setup) and the four downloader cards it shares with
   Settings › Downloads. SPEC-v1.1.md 6.1 and 6.2. Loaded before app.js; app.js calls the function below
   with its internals. Plain JS, no dependencies. */
'use strict';
(window.ABC_EXT = window.ABC_EXT || []).push((ABC) => {
  const { $, $$, h, raw, ico, API, toast, clone, getPath, setPath, isAbsPath, samePath, winOr, OS_WIN, fSize, baseName, clamp, cap, S, Views, ROUTES, ACTIONS, hydrate, A, isUrl, soft } = ABC;
  const SEP = OS_WIN ? '\\' : '/';
  const EX_DL = winOr('D:\\Anime', '/home/you/Anime');
  /** Menu path as key chips: Tools › Options… › Web UI */
  const kbd = (...parts) => h`<span class="menu-path">${parts.map((p, i) => h`${i ? '›' : ''}<kbd>${p}</kbd>`)}</span>`;

  // ---------------------------------------------------------------- downloader cards (6.1 step 3, 6.2 Downloads)
  /**
   * o.settings: true inside the Settings form (radios and the watch-folder field carry data-path, the
   *   qBittorrent login lives in its own section), false in the setup guide (fields are inline, ids w-*).
   * o.draft: settings object for current values; o.mode overrides the checked card; o.downloadDir: path or "".
   */
  function modeCards(o) {
    const set = !!o.settings;
    const d = o.draft || {};
    const mode = o.mode || getPath(d, 'downloader.mode') || 'manual';
    const name = set ? 'f-dl-mode' : 'w-mode';
    const dl = o.downloadDir || '';
    const dlB = h`<b class="mono" data-dl-path>${dl || 'your download folder'}</b>`;
    const tdId = set ? 'f-downloader-torrent_dir' : 'w-torrent-dir';
    const radio = (k) => h`<input type="radio" name="${name}" value="${k}" ${mode === k ? raw('checked') : ''} ${set ? raw('data-path="downloader.mode"') : ''} aria-describedby="${name}-${k}-sub">`;
    const card = (k, title, sub, body, badge = '') => h`<div class="mode-card" data-mode="${k}">
      <label class="mode-top">${radio(k)}<span class="mode-dot" aria-hidden="true"></span><span class="mode-text"><span class="mode-title">${title}${badge}</span><span class="mode-sub" id="${name}-${k}-sub">${sub}</span></span></label>
      <div class="mode-body">${body}</div>
    </div>`;
    const testRow = (act, out, labelCls) => h`<div class="row test-row"><button type="button" class="btn btn-sm" data-act="${act}" data-out="${out}">${ico('plug')}<span class="${labelCls || ''}">Test</span></button><span class="test-result" id="${out}" aria-live="polite"></span></div>`;

    const qbLogin = set
      ? h`<p class="small muted">Address and login are in the <a href="#/settings?section=qbit">qBittorrent section</a> below.</p>`
      : h`<div class="form-grid w-qb">
          <div class="field"><label for="w-qbit-url">Web UI address</label><input class="input" id="w-qbit-url" inputmode="url" spellcheck="false" autocomplete="off" value="${getPath(d, 'qbit.url') || 'http://127.0.0.1:8080'}" aria-describedby="w-qbit-url-e"><p class="err" id="w-qbit-url-e" aria-live="polite"></p></div>
          <div class="field"><label for="w-qbit-user">Username</label><input class="input" id="w-qbit-user" spellcheck="false" autocomplete="off" placeholder="empty with Bypass" value="${getPath(d, 'qbit.username') || ''}"></div>
          <div class="field"><label for="w-qbit-pass">Password</label><input class="input" id="w-qbit-pass" type="password" autocomplete="new-password" value="${getPath(d, 'qbit.password') || ''}"></div>
        </div>`;
    const qb = h`<ol class="steps">
        <li>In qBittorrent, open ${kbd('Tools', 'Options…', 'Web UI')}.</li>
        <li>Tick <b>Web User Interface (Remote control)</b>.</li>
        <li>Keep port <b>8080</b>.</li>
        <li>Tick <b>Bypass authentication for clients on localhost</b>. Or set a username and password and type them below.</li>
        <li>Press <b>OK</b>.</li>
      </ol>
      ${qbLogin}
      ${testRow(set ? 'test-qbit' : 'w-test-qbit', name + '-qbit-out', set ? 'test-qbit-label' : '')}
      <p class="note info">qBittorrent saves each episode into <b class="mono"><span data-dl-path>${dl || 'your download folder'}</span>${SEP}&lt;Show&gt;${SEP}</b> by itself. Nothing else to set.</p>`;
    const sysWord = OS_WIN ? 'Windows' : 'your system';
    const mg = h`<p>ABC hands each magnet link to the app ${sysWord} uses for magnet links.</p>
      <p class="small muted">Deluge, Transmission, uTorrent, BitTorrent, Tixati, Free Download Manager and most other torrent apps register themselves.</p>
      <p>Set that app's download folder to ${dlB}, so ABC finds the files.</p>
      ${testRow('test-magnet', name + '-magnet-out')}
      <p class="note info">The app decides the sub-folder. ABC matches episodes by file name, so any layout works.</p>`;
    const tf = h`<p>Most torrent apps can load .torrent files from a folder by themselves. Pick that folder here, then turn the feature on in your app:</p>
      <ul class="app-paths">
        <li><b>Deluge</b>${kbd('Preferences', 'Plugins', 'AutoAdd')}</li>
        <li><b>Transmission</b>${kbd('Edit', 'Preferences', 'Downloading', 'Automatically add .torrent files from')}</li>
        <li><b>uTorrent / BitTorrent</b>${kbd('Options', 'Preferences', 'Directories', 'Automatically load .torrents from')}</li>
        <li><b>qBittorrent</b>${kbd('Tools', 'Options', 'Downloads', 'Monitored Folder')}</li>
      </ul>
      <div class="field">
        <label for="${tdId}">Watch folder</label>
        <div class="row"><input class="input grow mono" id="${tdId}" ${set ? raw('data-path="downloader.torrent_dir"') : ''} spellcheck="false" autocomplete="off" placeholder="${winOr('C:\\Users\\You\\Downloads\\watch', '/home/you/watch')}" value="${getPath(d, 'downloader.torrent_dir') || ''}" aria-describedby="${tdId}-h ${tdId}-e"><button type="button" class="btn" data-act="pick-torrent-dir" data-target="${tdId}">${ico('folder')}Browse…</button></div>
        <p class="help" id="${tdId}-h">The folder must already exist. ABC writes each .torrent there; your app picks it up.</p>
        <p class="err" id="${tdId}-e" aria-live="polite"></p>
      </div>
      <p>Set the app's download folder to ${dlB}, so ABC finds the files.</p>`;
    const mn = h`<p>ABC lists each chosen release with a magnet button and a copy button.</p><p>Nothing downloads on its own.</p>`;
    return h`<div class="mode-cards" role="radiogroup" aria-label="How releases reach your torrent app" id="${set ? 'f-downloader-mode' : 'w-modes'}">
      ${card('qbittorrent', 'qBittorrent', 'The only client ABC talks to directly: it sets the save folder per show, follows the download and knows when it is done. The other modes hand the link over and stop there.', qb, h` <span class="chip chip-accent">Full integration</span>`)}
      ${card('magnet', 'Another torrent app, via magnet links', 'Each episode opens in the app that handles magnet links.', mg)}
      ${card('torrent_file', 'Another torrent app, via a watch folder', 'ABC saves a .torrent file into a folder your app watches.', tf)}
      ${card('manual', 'I\'ll add them by hand', 'ABC shows the magnet link. You add it yourself.', mn)}
    </div>`;
  }
  ABC.Setup = { modeCards };

  /** "C:\Program Files\qBittorrent\qbittorrent.exe" "%1" -> qbittorrent.exe */
  const handlerName = (cmd) => { const m = String(cmd).match(/^\s*"([^"]+)"/) || String(cmd).match(/^\s*(\S+)/); return m ? baseName(m[1]) : String(cmd); };
  ACTIONS['test-magnet'] = async (btn, ds) => {
    const out = $('#' + ds.out);
    if (!out) return;
    out.className = 'test-result'; out.textContent = 'Checking…'; out.title = '';
    try {
      const r = await API.testMagnet();
      if (r && r.ok) {
        out.className = 'test-result ok';
        out.textContent = r.handler && r.handler !== 'unknown' ? `Magnet links open in ${handlerName(r.handler)}` : 'Your system picks the app for magnet links.';
        out.title = r.handler || '';
      } else { out.className = 'test-result bad'; out.textContent = (r && r.error) || 'No app is registered for magnet links.'; }
    } catch (err) { out.className = 'test-result bad'; out.textContent = err.message; }
  };

  // ---------------------------------------------------------------- setup guide #/setup (6.1)
  const STEPS = ['Welcome', 'Folder', 'Downloads', 'Watching', 'First shows'];
  const BELT = [['calendar', 'AniList schedule'], ['search', 'nyaa.si'], ['download', 'your torrent app'], ['folder', 'your folder'], ['play', 'your player']];
  const F4 = ['titles.language', 'watched.mode', 'watched.fraction', 'watched.player_titles'];
  const fieldOf = (p) => ABC.FIELDS.find((x) => x.path === p);

  Views.setup = (m, params) => {
    const st = { step: clamp(Number(params.get('step')) || 1, 1, 5), settings: null, state: null, folders: [], suggest: null, suggestErr: null, dlPath: null, create: true, mode: null, draft4: null, addView: null };
    const view = () => $('#view');
    const curDl = () => (st.folders || []).find((f) => f.active && f.download);
    const dlPath = () => (curDl() ? curDl().path : '');
    // Scan selects on the library rows PATCH through folders.js; it hands the fresh list back here.
    ABC.Folders.scopes.setup = (list) => { st.folders = list || st.folders; const libs = $('#w-libs'); if (libs) libs.innerHTML = String(libsHtml()); };
    const wErr = (msg) => { const e = $('#w-err'); if (e) e.textContent = msg || ''; };

    // ---------- markup
    const stepperHtml = () => STEPS.map((label, i) => {
      const n = i + 1; const done = n < st.step; const cur = n === st.step;
      return h`<li class="${done ? 'is-done' : ''} ${cur ? 'is-current' : ''}" ${cur ? raw('aria-current="step"') : ''}>
        ${done ? h`<button type="button" class="st-dot" data-act="w-goto" data-step="${n}" aria-label="Step ${n}, ${label}: done. Go back to it.">${ico('check')}</button>` : h`<span class="st-dot" aria-hidden="true">${n}</span>`}
        <span class="st-label" ${done ? raw('aria-hidden="true"') : ''}><span class="sr-only">Step ${n}: </span>${label}</span>
      </li>`;
    });
    const footHtml = () => h`<button type="button" class="btn btn-ghost w-skip" data-act="w-skip">Skip setup</button>
      <span class="grow"></span>
      ${st.step > 1 ? h`<button type="button" class="btn" data-act="w-back">${ico('chevL')}Back</button>` : ''}
      <button type="button" class="btn btn-primary" data-act="w-next">${st.step === 5 ? h`${ico('check')}Finish` : h`Next${ico('chevR')}`}</button>`;
    const titleH = (t) => h`<h2 class="w-title" id="w-title" tabindex="-1">${t}</h2>`;

    const step1 = () => h`${titleH('Welcome to AnimeBeltConveyor')}
      <p class="w-lead">New episodes of the shows you follow land in your folder by themselves.</p>
      <ol class="belt" aria-label="How an episode travels">${BELT.map(([i, l]) => h`<li class="belt-chip">${ico(i)}<span>${l}</span></li>`)}</ol>
      <ul class="w-lines">
        <li>${ico('bolt')}<span><b>By itself</b>, ABC watches the AniList schedule, finds each new episode on nyaa.si and hands it to your torrent app.</span></li>
        <li>${ico('play')}<span><b>You</b> press Play.</span></li>
        <li>${ico('eyeOff')}<span><b>Never</b>: ABC uploads nothing about you. It talks only to AniList, nyaa.si and your torrent app.</span></li>
      </ul>
      <p class="small muted">Four short questions follow. You can change every answer later in Settings.</p>`;

    const suggHtml = (val) => {
      if (st.suggestErr) return '';
      if (st.suggest == null) return raw('<div class="skel" style="height:96px"></div><div class="skel" style="height:96px"></div><div class="skel" style="height:96px"></div>');
      return h`${st.suggest.map((s) => h`<button type="button" class="sugg-card" data-act="w-suggest" data-path="${s.path}" aria-pressed="${String(!!val && samePath(s.path, val))}">
        <span class="sugg-top"><b>${s.label}</b>${s.exists ? h`<span class="chip chip-ok">exists</span>` : h`<span class="chip">new</span>`}</span>
        <span class="sugg-path mono">${s.path}</span>
        <span class="sugg-free">${s.free_bytes != null ? `${fSize(s.free_bytes)} free` : 'Free space unknown'}</span>
      </button>`)}`;
    };
    const libsHtml = () => {
      const libs = (st.folders || []).filter((f) => f.active && !f.download);
      const F = ABC.Folders;
      return libs.length ? h`<ul class="w-libs">${libs.map((f) => h`<li>${ico('folder')}<span class="mono ellipsis" title="${f.path}">${f.path}</span>${f.exists === false ? h`<span class="chip chip-bad">Not reachable</span>` : ''}${F.scanHtml(f)}<button type="button" class="btn btn-sm btn-ghost btn-icon" data-act="w-rm-lib" data-path="${f.path}" aria-label="Remove ${f.path}" title="Remove">${ico('x')}</button>${F.scanErrHtml(f)}</li>`)}</ul>${F.scanHelpHtml()}` : '';
    };
    const step2 = () => {
      const cur = dlPath();
      const val = st.dlPath != null ? st.dlPath : cur;
      return h`${titleH('Where do episodes go?')}
        <p class="w-lead">Pick one folder. Each new episode is saved there, in a folder per show.</p>
        ${cur ? h`<p class="small muted">Right now: <b class="mono">${cur}</b></p>` : ''}
        <div class="sugg" id="w-sugg" role="group" aria-label="Suggested folders">${suggHtml(val)}</div>
        <div class="field w-path">
          <label for="w-dl">Or pick any folder</label>
          <div class="row"><input class="input grow mono" id="w-dl" value="${val}" placeholder="${EX_DL}" spellcheck="false" autocomplete="off" aria-describedby="w-dl-e"><button type="button" class="btn" data-act="w-browse">${ico('folder')}Browse…</button></div>
          <p class="err" id="w-dl-e" aria-live="polite"></p>
          <label class="check"><input type="checkbox" class="switch" id="w-create" ${st.create ? raw('checked') : ''}> Create the folder if it does not exist</label>
        </div>
        <div class="w-extra" data-fm-scope="setup">
          <h3>Already have anime somewhere?</h3>
          <p class="small muted">Add those folders too. ABC scans them but never writes there.</p>
          <div id="w-libs">${libsHtml()}</div>
          <div class="row"><button type="button" class="btn btn-sm" data-act="w-add-lib">${ico('folderPlus')}Add a folder</button></div>
          <p class="err" id="w-lib-e" aria-live="polite"></p>
        </div>`;
    };
    const step3 = () => h`${titleH('Which app downloads?')}
      <p class="w-lead">ABC finds each episode. A torrent app downloads it. Pick how the two talk.</p>
      ${modeCards({ settings: false, draft: st.settings, mode: st.mode, downloadDir: dlPath() })}`;
    const step4 = () => {
      const d = st.draft4 || (st.draft4 = clone(st.settings));
      const players = getPath(d, 'watched.player_titles') || [];
      return h`${titleH('Watching')}
        <p class="w-lead">ABC ticks an episode as watched while you play it.</p>
        <div class="form-grid w-form" id="w-f4">${['titles.language', 'watched.mode', 'watched.fraction'].map((p) => ABC.fieldHtml(fieldOf(p), d))}</div>
        <p class="small muted" style="margin-top:14px">Players ABC knows: ${players.length ? players.join(', ') : 'none'}.</p>
        <details class="w-more" id="w-more"><summary>My player is not listed</summary><div class="form-grid w-form">${ABC.fieldHtml(fieldOf('watched.player_titles'), d)}</div></details>
        <p class="w-privacy small">${ico('eye')}ABC reads player window titles only on this PC.</p>`;
    };
    const step5 = () => h`${titleH('First shows')}
      <p class="w-lead">Track a few shows. ABC fetches their episodes as they air.</p>
      <div id="w-add" class="w-add">${raw('<div class="skel" style="height:280px"></div>')}</div>`;
    const stepHtml = () => [step1, step2, step3, step4, step5][st.step - 1]();

    // ---------- per-step wiring
    const repaintSugg = () => { const box = $('#w-sugg'); const inp = $('#w-dl'); if (box) box.innerHTML = String(suggHtml(inp ? inp.value.trim() : '')); };
    const mountStep = async () => {
      const root = view();
      if (st.step === 2) {
        const inp = $('#w-dl', root);
        inp.addEventListener('input', () => { st.dlPath = inp.value; $('#w-dl-e', root).textContent = ''; inp.removeAttribute('aria-invalid'); for (const b of $$('.sugg-card', root)) b.setAttribute('aria-pressed', String(!!inp.value.trim() && samePath(b.dataset.path, inp.value.trim()))); });
        $('#w-create', root).addEventListener('change', (e) => { st.create = e.target.checked; });
        if (st.suggest == null && !st.suggestErr) {
          try { st.suggest = await API.fsSuggest() || []; } catch (err) { st.suggestErr = err; }
          if (st.step === 2) repaintSugg();
        }
      } else if (st.step === 3) {
        const box = $('#w-modes', root);
        box.addEventListener('change', (e) => { if (e.target.name === 'w-mode') { st.mode = e.target.value; wErr(''); } });
        box.addEventListener('input', (e) => {
          const map = { 'w-qbit-url': 'qbit.url', 'w-qbit-user': 'qbit.username', 'w-qbit-pass': 'qbit.password', 'w-torrent-dir': 'downloader.torrent_dir' };
          if (map[e.target.id]) { setPath(st.settings, map[e.target.id], e.target.value); const er = $(`#${e.target.id}-e`, root); if (er) er.textContent = ''; e.target.removeAttribute('aria-invalid'); }
        });
      } else if (st.step === 4) {
        const onField = (e) => {
          const el = e.target.closest('[data-path]');
          if (!el) return;
          const f = fieldOf(el.dataset.path);
          if (!f) return;
          const r = ABC.readField(el, f);
          const er = $(`#${ABC.fid(f)}-e`, root); if (er) er.textContent = r.err || '';
          el.setAttribute('aria-invalid', r.err ? 'true' : 'false');
          if (!r.err) setPath(st.draft4, f.path, r.v);
          const fr = $(`#${ABC.fid(fieldOf('watched.fraction'))}`, root);
          if (fr) fr.disabled = !fieldOf('watched.fraction').enabledIf(st.draft4);
        };
        for (const id of ['#w-f4', '#w-more']) { const b = $(id, root); b.addEventListener('input', onField); b.addEventListener('change', onField); }
      } else if (st.step === 5) {
        const box = $('#w-add', root);
        const av = Views.add(null, new URLSearchParams(), { embedded: true });
        st.addView = av;
        try {
          const data = await av.load();
          if (st.step !== 5 || st.addView !== av || !document.body.contains(box)) return;
          box.innerHTML = String(av.render(data, false));
          hydrate(box);
          av.mount(box);
        } catch (err) { box.innerHTML = String(ABC.errorPanel(err, false)); }
      }
    };
    const paintAll = () => {
      const root = view();
      $('#w-stepper', root).innerHTML = String(h`${stepperHtml()}`);
      $('#w-body', root).innerHTML = String(stepHtml());
      $('#w-foot', root).innerHTML = String(footHtml());
      wErr('');
      hydrate($('#w-body', root));
    };
    const goStep = (n) => {
      st.step = clamp(n, 1, 5);
      if (st.step !== 5) st.addView = null;
      history.replaceState(null, '', `#/setup?step=${st.step}`);
      paintAll();
      mountStep();
      const t = $('#w-title'); if (t) t.focus({ preventScroll: true });
      const w = $('#wizard'); if (w && w.getBoundingClientRect().top < 0) w.scrollIntoView({ block: 'start' });
    };

    // ---------- savers (each returns true to move on)
    const fieldErr = (id, msg) => { const el = $('#' + id); const e = $(`#${id}-e`); if (e) e.textContent = msg; if (el) { el.setAttribute('aria-invalid', 'true'); el.focus(); } return false; };
    async function saveDownload() {
      const input = $('#w-dl');
      if (!input) return true;
      const p = input.value.trim(); const cur = dlPath();
      $('#w-dl-e').textContent = ''; input.removeAttribute('aria-invalid');
      if (!p) return cur ? true : fieldErr('w-dl', 'Pick a folder first: a suggestion, Browse…, or type a path.');
      if (!isAbsPath(p)) return fieldErr('w-dl', `Type the full path, for example ${EX_DL}.`);
      if (cur && samePath(cur, p)) return true;
      const create = $('#w-create').checked;
      try {
        st.folders = await API.addFolder(ABC.Folders.addBody((st.folders || []).map((f) => f.path), { path: p, download: true, create })) || st.folders;
        st.dlPath = null;
        const libs = $('#w-libs'); if (libs) libs.innerHTML = String(libsHtml());
        return true;
      } catch (err) {
        const m = cap(String((err.body && err.body.fields && err.body.fields.path) || err.message));
        return fieldErr('w-dl', /does not exist/i.test(m) && !create ? `${m}. Tick "Create the folder" below, or pick another one.` : m);
      }
    }
    async function saveSettingsFrom(mutate, fieldIds) {
      let cur;
      try { cur = await API.settings(); } catch (err) { wErr(err.message); return false; }
      mutate(cur);
      try {
        const saved = await API.saveSettings(cur);
        S.settings = saved || cur;
        st.settings = clone(S.settings);
        st.draft4 = null;
        return true;
      } catch (err) {
        const fields = (err.body && err.body.fields) || {};
        let shown = false;
        for (const [k, msg] of Object.entries(fields)) { const id = fieldIds[k]; if (id && $('#' + id)) { fieldErr(id, cap(String(msg))); shown = true; } }
        wErr(shown ? 'Fix the marked field, then press Next.' : `Not saved: ${err.message}`);
        return false;
      }
    }
    async function saveMode(forceMode) {
      const checked = $('input[name="w-mode"]:checked');
      const mode = forceMode || (checked && checked.value);
      if (!mode) { wErr('Pick one of the four.'); return false; }
      const url = ($('#w-qbit-url') || {}).value; const td = (($('#w-torrent-dir') || {}).value || '').trim();
      if (mode === 'qbittorrent' && !isUrl((url || '').trim())) return fieldErr('w-qbit-url', 'Not a web address. Example: http://127.0.0.1:8080');
      if (mode === 'torrent_file' && !td) return fieldErr('w-torrent-dir', 'Pick the folder your torrent app watches.');
      if (mode === 'torrent_file' && !isAbsPath(td)) return fieldErr('w-torrent-dir', `Type the full path, for example ${winOr('C:\\Users\\You\\Downloads\\watch', '/home/you/watch')}.`);
      return saveSettingsFrom((cur) => {
        setPath(cur, 'downloader.mode', mode);
        if (mode === 'qbittorrent') { setPath(cur, 'qbit.url', url.trim()); setPath(cur, 'qbit.username', ($('#w-qbit-user').value || '').trim()); setPath(cur, 'qbit.password', $('#w-qbit-pass').value || ''); }
        if (mode === 'torrent_file') setPath(cur, 'downloader.torrent_dir', td);
      }, { 'qbit.url': 'w-qbit-url', 'downloader.torrent_dir': 'w-torrent-dir', 'qbit.username': 'w-qbit-user', 'qbit.password': 'w-qbit-pass' });
    }
    async function saveWatching() {
      const vals = {}; const ids = {};
      let bad = null;
      for (const p of F4) {
        const f = fieldOf(p); const el = $('#' + ABC.fid(f)); ids[p] = ABC.fid(f);
        if (!el || el.disabled) continue;
        const r = ABC.readField(el, f);
        if (r.err) { const e = $(`#${ABC.fid(f)}-e`); if (e) e.textContent = r.err; el.setAttribute('aria-invalid', 'true'); bad = bad || el; } else vals[p] = r.v;
      }
      if (bad) { if (bad.closest('details')) bad.closest('details').open = true; bad.focus(); return false; }
      return saveSettingsFrom((cur) => { for (const [p, v] of Object.entries(vals)) setPath(cur, p, v); }, ids);
    }
    async function markDone() {
      return saveSettingsFrom((cur) => setPath(cur, 'ui.setup_done', true), {});
    }
    const leaveTo = (msg) => {
      if (S.state) S.state.setup_done = true;
      location.hash = '#/';
      toast(msg, 'ok', 9000);
    };
    const savers = { 1: async () => true, 2: saveDownload, 3: () => saveMode(), 4: saveWatching };
    const addAct = (name) => (btn, ds, e) => { const fn = st.addView && st.addView.actions && st.addView.actions[name]; return fn ? fn.call(st.addView, btn, ds, e) : undefined; };

    return {
      title: 'Setup guide', nav: 'setup',
      async load() {
        const [settings, state, folders] = await Promise.all([API.settings(), soft(API.state(), null), soft(API.folders(), [])]);
        S.settings = settings;
        st.settings = clone(settings);
        st.state = state.v;
        st.folders = folders.v || [];
        const saved = getPath(settings, 'downloader.mode');
        // First run: preselect the recommended qBittorrent card. A re-run keeps what was chosen.
        st.mode = saved && (saved !== 'manual' || getPath(settings, 'ui.setup_done')) ? saved : 'qbittorrent';
        return st;
      },
      render(_d, first) {
        return h`<div class="page setup-page">
          <section class="wizard glass ${A(first)}" id="wizard" aria-labelledby="w-title">
            <div class="w-top"><span class="w-kicker">${ico('compass')}Setup guide</span><ol class="stepper" id="w-stepper" aria-label="Setup steps">${stepperHtml()}</ol></div>
            <div class="w-body" id="w-body">${stepHtml()}</div>
            <p class="w-err" id="w-err" role="alert"></p>
            <div class="w-foot" id="w-foot">${footHtml()}</div>
          </section>
        </div>`;
      },
      mount(root) {
        if (S.last) ABC.paintFromLast();
        history.replaceState(null, '', `#/setup?step=${st.step}`);
        $('#wizard', root).addEventListener('keydown', (e) => {
          if (e.key !== 'Enter' || e.shiftKey || e.altKey || e.ctrlKey || e.metaKey || e.isComposing) return;
          if (e.target.closest('textarea, button, a, select, summary, input[type=search], #w-add')) return;
          e.preventDefault();
          const next = $('[data-act="w-next"]', root);
          if (next && !next.classList.contains('is-busy')) next.click();
        });
        mountStep();
      },
      actions: {
        'w-next': async () => {
          wErr('');
          if (st.step === 5) { if (await markDone()) leaveTo(`Setup done. The conveyor runs every ${getPath(S.settings, 'conveyor.poll_interval_min') || 15} minutes; press Run conveyor to start now.`); return; }
          if (await savers[st.step]()) goStep(st.step + 1);
        },
        'w-back': () => goStep(st.step - 1),
        'w-goto': (btn, ds) => goStep(Number(ds.step)),
        'w-skip': async () => { if (await markDone()) leaveTo('Setup skipped. Settings › Setup guide opens it again any time.'); },
        'w-suggest': (btn, ds) => {
          const inp = $('#w-dl'); if (!inp) return;
          inp.value = ds.path; st.dlPath = ds.path;
          inp.dispatchEvent(new Event('input', { bubbles: true }));
        },
        'w-browse': async () => {
          const inp = $('#w-dl');
          const r = await ABC.Folders.pickFolder({ title: 'Where should new episodes go?', context: 'download', initial: inp ? inp.value.trim() : '', create: st.create });
          if (!r || !inp) return;
          inp.value = r.path; st.dlPath = r.path;
          const cb = $('#w-create'); if (cb) { cb.checked = r.create; st.create = r.create; }
          inp.dispatchEvent(new Event('input', { bubbles: true }));
        },
        'w-add-lib': async () => {
          const e = $('#w-lib-e'); if (e) e.textContent = '';
          // Save the download folder first, so the server never promotes the first library folder to it.
          if (!curDl() && !(await saveDownload())) { if (e) e.textContent = 'Pick the download folder above first.'; return; }
          const r = await ABC.Folders.pickFolder({ title: 'Add a folder you already have', context: 'library', create: false });
          if (!r) return;
          try {
            st.folders = await API.addFolder(ABC.Folders.addBody((st.folders || []).map((f) => f.path), { path: r.path, download: false, create: r.create })) || st.folders;
            $('#w-libs').innerHTML = String(libsHtml());
          } catch (err) { if (e) e.textContent = cap(String((err.body && err.body.fields && err.body.fields.path) || err.message)); }
        },
        'w-rm-lib': async (btn, ds) => { st.folders = await API.patchFolder({ path: ds.path, active: false }) || st.folders; $('#w-libs').innerHTML = String(libsHtml()); },
        'w-test-qbit': async (btn, ds) => {
          const out = $('#' + ds.out);
          if (!(await saveMode('qbittorrent'))) return;
          out.className = 'test-result'; out.textContent = 'Testing…';
          try {
            const r = await API.testQbit();
            if (r && r.ok) { out.className = 'test-result ok'; out.textContent = `Connected${r.version ? ', qBittorrent ' + r.version : ''}`; } else { out.className = 'test-result bad'; out.textContent = (r && r.error) || 'Not reachable'; }
          } catch (err) { out.className = 'test-result bad'; out.textContent = err.message; }
        },
        // step 5 reuses the Add page; its own buttons dispatch here
        'season-prev': addAct('season-prev'), 'season-next': addAct('season-next'), 'season-now': addAct('season-now'), 'import-mal': addAct('import-mal'),
      },
      async refresh() { if (st.step === 5 && st.addView && $('#w-add')) await st.addView.refresh($('#w-add')); },
      refreshOn: (ev) => st.step === 5 && (ev.kind === 'show.added' || ev.kind === 'show.removed'),
    };
  };
  ROUTES.push([/^\/setup$/, Views.setup]);
});

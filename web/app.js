// vigie — interface web (JS sans framework, aucune ressource externe).
// Règle : tout texte venant de l'API est inséré avec textContent, jamais en HTML.
(function () {
  'use strict';
  const $ = (id) => document.getElementById(id);
  const state = { vs: null, self: null, datastores: [], ds: null, disk: null, job: null, es: null, presets: null, live: null };

  // ---------- utilitaires ----------
  const nf0 = new Intl.NumberFormat(window.vigieLocale || 'en-US', { maximumFractionDigits: 0 });
  const nf1 = new Intl.NumberFormat(window.vigieLocale || 'en-US', { maximumFractionDigits: 1, minimumFractionDigits: 1 });
  const nf2 = new Intl.NumberFormat(window.vigieLocale || 'en-US', { maximumFractionDigits: 2, minimumFractionDigits: 2 });
  const nf3 = new Intl.NumberFormat(window.vigieLocale || 'en-US', { maximumFractionDigits: 3, minimumFractionDigits: 3 });
  const mbps = (v) => nf0.format(v) + ' Mo/s';
  const iops = (v) => nf0.format(v) + ' IO/s';
  const ms = (v) => (v < 10 ? nf2.format(v) : nf1.format(v)) + ' ms';
  const gb = (b) => nf0.format(b / 1e9) + ' Go';
  const dt = (s) => s ? new Date(s).toLocaleString(window.vigieLocale || 'en-US') : '';
  function el(tag, attrs, ...children) {
    const e = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (k === 'class') e.className = v; else if (k === 'text') e.textContent = v;
      else if (k.startsWith('on')) e.addEventListener(k.slice(2), v); else if (v !== undefined && v !== null) e.setAttribute(k, v);
    }
    for (const c of children) if (c !== null && c !== undefined) e.append(c instanceof Node ? c : document.createTextNode(String(c)));
    return e;
  }
  const clear = (e) => { while (e.firstChild) e.removeChild(e.firstChild); return e; };
  function pill(level) { return el('span', { class: 'pill lvl-' + level, text: level }); }
  function setBadge(id, text, ok) { const b = $(id); b.textContent = text || ''; b.className = 'badge' + (ok === true ? ' ok' : ok === false ? ' err' : ''); }

  async function api(method, path, body) {
    const opt = { method, credentials: 'same-origin', headers: { 'X-Vigie': '1' } };
    if (body !== undefined) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
    const r = await fetch('/api' + path, opt);
    let data = null;
    try { data = await r.json(); } catch (_) { /* corps vide */ }
    if (r.status === 401 && path !== '/login') { showLogin(); throw new Error('session expirée'); }
    if (!r.ok) throw new Error((data && data.error) || ('HTTP ' + r.status));
    return data;
  }

  // ---------- thème ----------
  function applyTheme(t) { if (t) document.documentElement.setAttribute('data-theme', t); else document.documentElement.removeAttribute('data-theme'); }
  try { applyTheme(localStorage.getItem('vigie-theme')); } catch (_) { /* stockage indisponible */ }
  $('theme').addEventListener('click', () => {
    const dark = getComputedStyle(document.documentElement).getPropertyValue('--bg').trim().startsWith('#0');
    const t = dark ? 'light' : 'dark'; applyTheme(t);
    try { localStorage.setItem('vigie-theme', t); } catch (_) { /* rien */ }
    if (state.live) state.live.redraw();
  });

  // ---------- connexion à l'appliance ----------
  function showLogin() { $('app').classList.add('hidden'); $('login').classList.remove('hidden'); $('login-password').focus(); }
  function showApp() { $('login').classList.add('hidden'); $('app').classList.remove('hidden'); }
  $('login-form').addEventListener('submit', async (ev) => {
    ev.preventDefault(); $('login-error').textContent = '';
    try { await api('POST', '/login', { password: $('login-password').value }); $('login-password').value = ''; showApp(); boot(); }
    catch (e) { $('login-error').textContent = e.message; }
  });
  $('logout').addEventListener('click', async () => { try { await api('POST', '/logout'); } catch (_) { /* rien */ } showLogin(); });

  // ---------- onglets ----------
  document.querySelectorAll('.tab').forEach((b) => b.addEventListener('click', () => {
    document.querySelectorAll('.tab').forEach((x) => x.classList.toggle('active', x === b));
    document.querySelectorAll('.tabpane').forEach((p) => p.classList.add('hidden'));
    $('tab-' + b.dataset.tab).classList.remove('hidden');
    if (b.dataset.tab === 'history') loadHistory();
    if (b.dataset.tab === 'network') loadNetwork();
    if (b.dataset.tab === 'settings') loadSettings();
    if (b.dataset.tab === 'about') loadAbout();
  }));

  // ---------- étape 1 : vSphere ----------
  let pendingCert = null;
  $('vs-thumb').addEventListener('click', async () => {
    $('connect-error').textContent = '';
    try {
      const c = await api('GET', '/vsphere/thumbprint?url=' + encodeURIComponent($('vs-url').value.trim()));
      pendingCert = c;
      $('cert-host').textContent = c.host; $('cert-sha').textContent = c.sha256; $('cert-subject').textContent = c.subject;
      $('cert-trust').textContent = c.trusted ? 'certificat reconnu par une autorité' : (c.self_signed ? 'certificat auto-signé' : 'autorité non reconnue');
      $('vs-cert').classList.remove('hidden');
    } catch (e) { $('connect-error').textContent = e.message; }
  });
  $('vs-connect').addEventListener('click', async () => {
    $('connect-error').textContent = '';
    if (!pendingCert) return;
    $('vs-connect').disabled = true;
    try {
      await api('POST', '/vsphere/connect', { url: $('vs-url').value.trim(), username: $('vs-user').value, password: $('vs-pass').value, thumbprint: pendingCert.sha256, remember: $('vs-remember').checked });
      $('vs-pass').value = ''; pendingCert = null; $('vs-cert').classList.add('hidden');
      await refreshAll();
    } catch (e) { $('connect-error').textContent = e.message; }
    finally { $('vs-connect').disabled = false; }
  });
  async function disconnect(forget) {
    try { await api('POST', '/vsphere/disconnect' + (forget ? '?forget=1' : '')); } catch (_) { /* rien */ }
    await refreshAll();
  }
  $('vs-disconnect').addEventListener('click', () => disconnect(false));
  $('vs-forget').addEventListener('click', () => disconnect(true));
  $('clean-forget').addEventListener('click', () => disconnect(true));

  async function refreshVS() {
    const st = await api('GET', '/vsphere/status');
    state.vs = st;
    const chip = $('vs-chip');
    if (st.connected) {
      chip.textContent = 'vSphere : ' + (st.server ? st.server.name + ' ' + st.server.version : 'connecté'); chip.className = 'chip on';
      $('connect-form').classList.add('hidden'); $('connect-done').classList.remove('hidden');
      $('vs-summary').textContent = 'Connecté à ' + st.url + ' en tant que ' + st.username + ' (' + st.server.full_name + '). Empreinte ' + st.thumbprint;
      setBadge('b-connect', 'connecté', true);
    } else {
      chip.textContent = 'vSphere : déconnecté'; chip.className = 'chip';
      $('connect-form').classList.remove('hidden'); $('connect-done').classList.add('hidden');
      if (st.url && !$('vs-url').value) { $('vs-url').value = st.url; $('vs-user').value = st.username || ''; }
      setBadge('b-connect', '');
    }
  }

  // ---------- étape 2 : identité et privilèges ----------
  async function refreshSelf() {
    const t = $('self-text'); const p = clear($('priv'));
    if (!state.vs || !state.vs.connected) { t.textContent = 'Disponible après la connexion.'; t.className = 'muted'; setBadge('b-self', ''); state.self = null; return; }
    try {
      const s = await api('GET', '/self'); state.self = s;
      t.className = '';
      t.textContent = 'Je suis la VM ' + s.vm.name + ' sur l\'hôte ' + s.host.name + ' (ESXi ' + s.host.version + ')' + (s.cluster ? ', cluster ' + s.cluster.name : '') + '. Identification par ' + s.method + '.';
      setBadge('b-self', s.host.name, true);
      const pr = await api('GET', '/privileges');
      if (pr.missing && pr.missing.length) p.append(el('p', { class: 'error', text: 'Privilèges manquants : ' + pr.missing.join(', ') }));
      else p.append(el('p', { class: 'muted', text: 'Privilèges requis présents pour ' + pr.user + '.' }));
    } catch (e) { t.textContent = e.message; t.className = 'error'; setBadge('b-self', 'échec', false); }
  }

  // ---------- étape 3 : datastores ----------
  async function refreshDS() {
    const tb = clear($('ds-table').tBodies[0]);
    if (!state.self) { setBadge('b-ds', ''); return; }
    const r = await api('GET', '/datastores');
    state.datastores = r.datastores;
    const rec = state.disk && state.disk.record;
    if (rec && !state.ds) state.ds = rec.datastore_ref;
    if (!state.ds) { const nfs = r.datastores.find((d) => d.type === 'NFS' || d.type === 'NFS41'); if (nfs) state.ds = nfs.ref; }
    for (const d of r.datastores) {
      const nfs = d.type === 'NFS' || d.type === 'NFS41';
      const radio = el('input', { type: 'radio', name: 'ds', value: d.ref });
      radio.checked = d.ref === state.ds; radio.disabled = !!rec && rec.datastore_ref !== d.ref;
      radio.addEventListener('change', () => { state.ds = d.ref; refreshDS(); });
      const tr = el('tr', { class: d.ref === state.ds ? 'sel' : '' },
        el('td', {}, radio), el('td', { text: d.name }), el('td', { text: d.type }),
        el('td', { text: nfs ? (d.nfs_server + ':' + d.nfs_export) : '—' }),
        el('td', { class: 'r', text: gb(d.free_bytes) }), el('td', { class: 'r', text: String(d.host_count) }));
      tb.append(tr);
    }
    const cur = r.datastores.find((d) => d.ref === state.ds);
    setBadge('b-ds', cur ? cur.name : '', !!cur);
    updateRunButton();
    await refreshPath(cur);
    await refreshHosts(cur);
  }
  async function refreshPath(ds) {
    const box = clear($('path'));
    if (!ds || !(ds.type === 'NFS' || ds.type === 'NFS41')) { box.classList.add('hidden'); return; }
    box.classList.remove('hidden');
    try {
      const p = await api('GET', '/path?datastore=' + encodeURIComponent(ds.ref));
      box.append(el('span', { class: 'muted', text: 'Chemin NFS : ' }),
        el('span', { class: 'hop', text: p.nfs_server }), ' → ', el('span', { class: 'hop', text: p.vmk.device + ' ' + p.vmk.ip + ' (MTU ' + p.vmk.mtu + ')' }), ' → ',
        el('span', { class: 'hop', text: (p.switch_type === 'distributed' ? 'vDS ' : 'vSwitch ') + p.switch }), ' → ',
        ...p.uplinks.map((u) => el('span', { class: 'hop', text: u.device + ' ' + u.role + (u.speed_mb ? ' ' + nf0.format(u.speed_mb / 1000) + ' Gb/s' : ' lien coupé'),
          title: u.peer ? [u.peer.protocol.toUpperCase(), u.peer.switch, u.peer.port, u.peer.port_description, u.peer.mgmt_addr, u.peer.platform, u.peer.vlan ? 'VLAN ' + u.peer.vlan : '', u.peer.mtu ? 'MTU ' + u.peer.mtu : ''].filter(Boolean).join(' · ') : undefined },
          u.peer ? el('span', { class: 'muted', text: ' ↔ ' + [u.peer.switch, u.peer.port].filter(Boolean).join(' ') }) : null)));
      for (const n of (p.notes || [])) box.append(el('p', { class: 'small muted', text: n }));
    } catch (e) { box.append(el('p', { class: 'error', text: e.message })); }
  }
  async function refreshHosts(ds) {
    const tb = clear($('hosts-table').tBodies[0]);
    const self = state.self; // figé : une déconnexion pendant la requête remet state.self à null
    if (!ds || !self) return;
    tb.append(el('tr', {}, el('td', { colspan: '4', class: 'small muted', text: 'prévalidation du vMotion vers chaque hôte…' })));
    try {
      const r = await api('GET', '/migrate/check?datastore=' + encodeURIComponent(ds.ref));
      state.hostChecks = r.checks; clear(tb);
      const d = r.drs;
      $('drs-info').textContent = d && d.cluster ? 'Cluster ' + d.cluster + ' : DRS ' + (d.enabled ? 'actif (' + d.default_behavior + ')' : 'inactif') +
        (d.override ? ', override de l\'appliance : ' + d.override_behavior : '') + (d.needs_manual ? ' — DRS peut déplacer l\'appliance pendant une mesure : l\'override est recommandé.' : '') : '';
      for (const c of r.checks) {
        const cb = el('input', { type: 'checkbox', value: c.host_ref }); cb.checked = c.ok; cb.disabled = !c.ok;
        cb.addEventListener('change', updateTour);
        const status = c.current ? 'hôte courant' : c.ok ? 'possible' + (c.warnings && c.warnings.length ? ' (' + c.warnings.join(' ; ') + ')' : '') : 'impossible : ' + (c.errors || []).join(' ; ');
        tb.append(el('tr', { class: c.current ? 'sel' : '' }, el('td', {}, cb), el('td', { text: c.host }), el('td', { class: 'small', text: status }),
          el('td', {}, c.current || !c.ok ? '' : el('button', { text: 'Déplacer ici', onclick: () => { if (confirm('Déplacer l\'appliance vers ' + c.host + ' (vMotion) ?')) submitJob({ profile: 'move', target_host: c.host_ref }); } }))));
      }
      if (state.self !== self) return; // identité changée entre-temps : un autre rafraîchissement suit
      setBadge('b-hosts', self.host.name, true);
    } catch (e) { tb.append(el('tr', {}, el('td', { colspan: '4', class: 'error', text: e.message }))); }
    updateTour();
  }
  function tourHosts() { return [...document.querySelectorAll('#hosts-table input:checked')].map((x) => x.value); }
  function updateTour() {
    const hosts = tourHosts(), cycles = Number($('tour-cycles').value);
    const p = testParams(); const one = p.baseline_s + p.write_s + p.read_s + 45 + 30;
    $('tour-estimate').textContent = hosts.length >= 2 ? 'Tournée : ' + hosts.length + ' hôtes × ' + cycles + ' cycle(s) = ' + hosts.length * cycles + ' tests standard, durée estimée ' + Math.ceil(hosts.length * cycles * one / 60) + ' min (paramètres de l\'étape 6).' : '';
    const why = !state.vs || !state.vs.connected ? 'Connectez-vous à vSphere (étape 1).' : !(state.disk && state.disk.ready) ? 'Créez d\'abord le disque de test (étape 4).'
      : hosts.length < 2 ? 'Cochez au moins deux hôtes.' : state.job ? 'Un job est déjà en cours.' : '';
    $('tour-run').disabled = !!why; $('tour-why').textContent = why;
  }
  ['tour-cycles'].forEach((id) => $(id).addEventListener('change', updateTour));
  $('tour-run').addEventListener('click', () => {
    const p = testParams();
    submitJob({ profile: 'tour', hosts: tourHosts(), cycles: Number($('tour-cycles').value), drs_override: $('tour-drs').value === '1', return_home: $('tour-home').value === '1',
      baseline_s: p.baseline_s, write_s: p.write_s, read_s: p.read_s, preset: p.preset });
  });

  // ---------- étape 4 : disque de test ----------
  async function refreshDisk() {
    const st = await api('GET', '/testdisk'); state.disk = st;
    const has = !!st.record;
    $('disk-none').classList.toggle('hidden', has); $('disk-some').classList.toggle('hidden', !has);
    if (has) {
      const r = st.record;
      $('disk-text').textContent = r.file_name + ' — ' + nf0.format(r.size_bytes / 2 ** 30) + ' Gio ' + r.provisioning + ' sur ' + r.datastore_name +
        (st.signature ? ' — prérempli ' + nf0.format(st.signature.prefilled_bytes / 2 ** 30) + ' Gio' : '') + (st.error ? ' — ' + st.error : '');
      const ul = clear($('disk-checks'));
      for (const c of ((st.guest && st.guest.checks) || [])) ul.append(el('li', { class: c.ok ? '' : 'ko', text: (c.ok ? '✔ ' : '✘ ') + c.name + (c.detail ? ' — ' + c.detail : '') }));
      setBadge('b-disk', st.ready ? 'prêt' : 'non prêt', st.ready);
    } else setBadge('b-disk', '');
    $('clean-disk').disabled = !has || !state.vs || !state.vs.connected || !!state.job;
    updateRunButton();
  }
  $('disk-create').addEventListener('click', () => submitJob({ profile: 'testdisk.create', datastore: state.ds, size_gb: Number($('disk-size').value) }));
  $('clean-disk').addEventListener('click', () => { if (confirm('Supprimer le disque de test et son fichier VMDK ?')) submitJob({ profile: 'testdisk.delete' }); });

  // ---------- étape 6 : paramètres du test ----------
  const kind = () => (document.querySelector('input[name=kind]:checked') || {}).value || 'standard';
  function testParams() {
    const base = Number($('t-base').value) || 60, dur = Number($('t-dur').value) || 80;
    if (kind() === 'advanced') {
      return { profile: 'advanced', baseline_s: base, write_s: dur, read_s: dur, preset: $('t-preset').value, full: $('t-full').value === '1', ceiling_ms: Number($('t-ceiling').value) || 0 };
    }
    return { profile: 'standard', repetitions: Number($('t-reps').value), baseline_s: base, write_s: dur, read_s: dur, preset: $('t-preset').value };
  }
  function presetCeiling() { const p = state.presets && state.presets.presets[$('t-preset').value]; return p ? p.useful_iops_ceiling_ms : 5; }
  document.querySelectorAll('input[name=kind]').forEach((r) => r.addEventListener('change', () => {
    const adv = kind() === 'advanced', mon = kind() === 'monitor';
    $('adv-opts').classList.toggle('hidden', !adv); $('t-reps').disabled = adv;
    $('mon-opts').classList.toggle('hidden', !mon); $('run-opts').classList.toggle('hidden', mon); $('t-reps').closest('.grid4').classList.toggle('hidden', mon);
    if (mon) { loadSchedule().catch((e) => { $('m-error').textContent = e.message; }); return; }
    if (adv && !$('t-ceiling').value) $('t-ceiling').value = presetCeiling();
    updateEstimate();
  }));
  $('t-preset').addEventListener('change', () => { $('t-ceiling').value = presetCeiling(); });
  function updateEstimate() {
    const p = testParams();
    const prefill = state.disk && state.disk.record && !(state.disk.signature && state.disk.signature.prefilled_bytes > 0) ? Math.ceil(state.disk.record.size_bytes / 400e6) : 0;
    const one = p.baseline_s + p.write_s + p.read_s + 45;
    const matrix = p.profile === 'advanced' ? (p.full ? 36 : 12) * 45 + 60 : 0;
    const total = prefill + one * (p.repetitions || 1) + matrix;
    $('t-estimate').textContent = 'Durée estimée : ' + Math.ceil(total / 60) + ' min' + (prefill ? ' (dont préremplissage ~' + prefill + ' s)' : '') + '.';
    $('t-profile').textContent = 'Profil : écriture puis lecture séquentielles, bloc 1 Mio, QD 4, ' + p.write_s + ' s chacune sans chauffe, sur toute la zone du disque de test, O_DIRECT+O_SYNC, données aléatoires incompressibles renouvelées à chaque passe ; ligne de base au repos ' + p.baseline_s + ' s.' +
      (p.profile === 'advanced' ? ' Puis ' + (p.full ? 36 : 12) + ' paliers aléatoires uniformes alignés : ' + (p.full ? '4, 8 et 64 Kio' : '4 Kio') + ' en lecture, écriture et mixte 70/30, QD 1, 4, 16 et 32, 5 s de chauffe puis 40 s de mesure chacun, données renouvelées à chaque écriture ; IOPS utiles = palier le plus élevé dont la p99 reste sous ' + (p.ceiling_ms || presetCeiling()) + ' ms.' : '');
  }
  ['t-reps', 't-base', 't-dur', 't-preset', 't-full', 't-ceiling'].forEach((id) => $(id).addEventListener('input', updateEstimate));
  // Boutons et raisons : recalculés après chaque rafraîchissement (vSphere, identité, datastore, disque, job).
  function updateRunButton() {
    const diskWhy = !state.vs || !state.vs.connected ? 'connectez-vous à vSphere (étape 1)' : !state.self ? 'identification en cours (étape 2)'
      : !state.ds ? 'choisissez un datastore (étape 3)' : state.job ? 'un job est en cours' : '';
    $('disk-create').disabled = !!diskWhy; $('disk-why').textContent = diskWhy;
    const why = !state.vs || !state.vs.connected ? 'Connectez-vous d\'abord à vSphere (étape 1).'
      : !(state.disk && state.disk.record) ? 'Créez d\'abord le disque de test (étape 4).'
      : !state.disk.ready ? 'Le disque de test n\'est pas prêt : voir les contrôles de sécurité (étape 4).'
      : state.job ? 'Un job est déjà en cours (étape 7).' : '';
    $('t-run').disabled = !!why; $('t-why').textContent = why;
    updateEstimate();
    if ($('tour-run')) updateTour();
  }
  $('t-run').addEventListener('click', () => submitJob(testParams()));

  // ---------- jobs ----------
  async function submitJob(req) {
    $('run-error').textContent = '';
    try { const j = await api('POST', '/jobs', req); followJob(j.id); }
    catch (e) { $('run-error').textContent = e.message; $('step-run').scrollIntoView({ behavior: 'smooth' }); }
  }
  $('run-cancel').addEventListener('click', async () => { if (state.job) { try { await api('POST', '/jobs/' + state.job + '/cancel'); } catch (e) { $('run-error').textContent = e.message; } } });

  function newLiveChart() {
    const box = clear($('live-chart'));
    const cs = getComputedStyle(document.documentElement);
    const opts = { width: Math.max(300, box.clientWidth || 800), height: 200, scales: { x: { time: false } },
      axes: [{ label: 's', stroke: cs.getPropertyValue('--muted') }, { label: 'Mo/s', stroke: cs.getPropertyValue('--muted') }],
      series: [{}, { label: 'écriture (Mo/s)', stroke: cs.getPropertyValue('--s1').trim(), width: 2 }, { label: 'lecture (Mo/s)', stroke: cs.getPropertyValue('--s2').trim(), width: 2 },
        { label: 'aléatoire (IO/s)', stroke: cs.getPropertyValue('--muted').trim(), width: 1.5, scale: 'iops' }] };
    opts.scales.iops = { auto: true };
    opts.axes.push({ scale: 'iops', side: 1, label: 'IO/s', stroke: cs.getPropertyValue('--muted'), grid: { show: false } });
    state.liveData = [[], [], [], []];
    state.live = new uPlot(opts, state.liveData, box);
  }
  function followJob(id) {
    if (state.es) state.es.close();
    state.job = id; state.liveN = 0;
    clear($('run-log')); $('run-bar').style.width = '0%'; $('run-pct').textContent = '0 %'; $('run-step').textContent = '';
    $('run-cancel').classList.remove('hidden'); setBadge('b-run', 'en cours');
    newLiveChart();
    $('job-chip').classList.remove('hidden'); $('job-chip').className = 'chip busy'; $('job-chip').textContent = 'job en cours';
    $('step-run').scrollIntoView({ behavior: 'smooth' });
    refreshDisk();
    const es = new EventSource('/api/jobs/' + encodeURIComponent(id) + '/events');
    state.es = es;
    es.addEventListener('progress', (m) => { const e = JSON.parse(m.data); $('run-bar').style.width = e.progress + '%'; $('run-pct').textContent = nf0.format(e.progress) + ' %'; $('run-step').textContent = e.step; });
    es.addEventListener('log', (m) => { const e = JSON.parse(m.data); const log = $('run-log'); log.append(T(e.msg) + '\n'); log.scrollTop = log.scrollHeight; });
    es.addEventListener('second', (m) => {
      const e = JSON.parse(m.data); const d = e.data || {};
      if (d.t === undefined || d.t <= 0) return;
      state.liveN++;
      state.liveData[0].push(state.liveN);
      const rnd = String(d.kind).startsWith('rand');
      state.liveData[1].push(!rnd && String(d.kind).indexOf('write') >= 0 ? d.mbps : null);
      state.liveData[2].push(!rnd && String(d.kind).indexOf('read') >= 0 ? d.mbps : null);
      state.liveData[3].push(rnd ? d.iops : null);
      state.live.setData(state.liveData);
    });
    es.addEventListener('state', async (m) => {
      const e = JSON.parse(m.data);
      if (e.state === 'running' || e.state === 'queued') return;
      es.close(); state.es = null; state.job = null;
      $('run-cancel').classList.add('hidden'); $('job-chip').classList.add('hidden');
      setBadge('b-run', e.state === 'succeeded' ? 'terminé' : e.state + (e.msg ? ' : ' + e.msg : ''), e.state === 'succeeded');
      const j = await api('GET', '/jobs/' + encodeURIComponent(id));
      if (j.profile === 'move') { await refreshAll(); return; }
      if (j.profile === 'standard' || j.profile === 'bench' || j.profile === 'advanced' || j.profile === 'tour') { renderResult($('result'), j); setBadge('b-result', j.result && j.result.summary ? j.result.summary.level : (j.result && j.result.verdict ? j.result.verdict.level : (j.result && j.result.comparison ? j.result.comparison.level : j.state)), j.state === 'succeeded'); $('step-result').scrollIntoView({ behavior: 'smooth' }); }
      await refreshDisk(); await refreshDS();
    });
    es.onerror = () => { /* le navigateur reconnecte seul ; Last-Event-ID évite les doublons */ };
  }

  // ---------- rendu des résultats ----------
  function renderResult(root, job) {
    clear(root);
    if (!job.result) { root.append(el('p', { class: job.error ? 'error' : 'muted', text: job.error || 'Pas de résultat.' })); return; }
    if (REPORTABLE.includes(job.profile) && job.state === 'succeeded' && job.id) root.append(reportLinks(job.id));
    if (job.profile === 'standard') return renderStandard(root, job);
    if (job.profile === 'bench') return renderBench(root, job.result);
    if (job.profile === 'advanced') return renderAdvanced(root, job.result);
    if (job.profile === 'net.iperf') return renderIperf(root, job.result);
    if (job.profile === 'tour') return renderTour(root, job.result);
    root.append(el('pre', { class: 'log', text: JSON.stringify(job.result, null, 2) }));
  }
  function tile(v, l) { return el('div', { class: 'tile' }, el('div', { class: 'v', text: v }), el('div', { class: 'l', text: l })); }
  function renderStandard(root, job) {
    const res = job.result, sum = res.summary, runs = res.runs || [];
    root.append(el('div', { class: 'verdict lvl-' + sum.level }, el('span', { class: 'lvl', text: sum.level }),
      el('span', { text: 'Hôte ' + sum.host + ', datastore ' + sum.datastore + (sum.repetitions > 1 ? ', médiane de ' + sum.repetitions + ' répétitions' : '') + '. ' + (sum.triggered && sum.triggered.length ? sum.triggered.map((x) => T(x)).join(' · ') : 'Aucun indicateur déclenché.') })));
    root.append(el('div', { class: 'tiles' }, tile(mbps(sum.write_mbps_median), 'écriture séquentielle'), tile(mbps(sum.read_mbps_median), 'lecture séquentielle'),
      tile(ms(sum.write_p99_ms_median), 'latence p99 en écriture'), tile(sum.tx_ratio_median ? nf3.format(sum.tx_ratio_median) : 'n/d', 'ratio TX/écrit corrigé')));
    root.append(el('p', { class: 'profile small', text: 'Profil écriture : ' + sum.write_profile }), el('p', { class: 'profile small', text: 'Profil lecture : ' + sum.read_profile }));
    runs.forEach((r, i) => {
      if (runs.length > 1) root.append(el('h3', { text: 'Répétition ' + (i + 1) + ' — ' + r.verdict.level }));
      const t = el('table', { class: 'table' }, el('thead', {}, el('tr', {}, el('th', { text: 'Indicateur' }), el('th', { text: 'Valeur' }), el('th', { text: 'Verdict' }))));
      const tb = el('tbody'); t.append(tb);
      for (const ind of r.verdict.indicators) tb.append(el('tr', {}, el('td', { text: ind.label }), el('td', { text: ind.why }), el('td', {}, pill(ind.level))));
      root.append(el('div', { class: 'tablewrap' }, t));
      root.append(el('p', { class: 'small', text: 'Écriture ' + mbps(r.write.mbps) + ' (p50 ' + ms(r.write.latency.p50_us / 1000) + ', p99 ' + ms(r.write.latency.p99_us / 1000) + ', CPU worker ' + nf0.format(r.write.cpu_pct) + ' %) — lecture ' + mbps(r.read.mbps) + ' (p99 ' + ms(r.read.latency.p99_us / 1000) + ').' }));
      if (r.path) root.append(el('p', { class: 'small muted', text: 'Chemin : ' + r.path.nfs_ip + ' via ' + r.path.vmk.device + ' (MTU ' + r.path.vmk.mtu + ') → ' + r.path.switch + ' → ' + r.path.uplinks.map((u) => u.device + ' ' + u.role + (u.peer ? ' ↔ ' + [u.peer.switch, u.peer.port].filter(Boolean).join(' ') : '')).join(', ') }));
      if (r.per_vmnic && r.per_vmnic.length) {
        const esx = r.per_vmnic.some((v) => v.esxcli);
        const heads = ['vmnic', 'TX écriture', 'RX lecture', 'TX au repos', 'erreurs/pertes RX', 'erreurs/pertes TX'].concat(esx ? ['CRC/trame/longueur', 'manquées RX'] : []);
        const vt = el('table', { class: 'table' }, el('thead', {}, el('tr', {}, ...heads.map((h, i) => el('th', { class: i ? 'r' : '', text: h })))));
        const vb = el('tbody'); vt.append(vb);
        const esxCell = (v, keys) => el('td', { class: 'r', text: v.esxcli ? nf0.format(keys.reduce((a, k) => a + (v.esxcli[k] || 0), 0)) : '—' });
        for (const v of r.per_vmnic) vb.append(el('tr', {}, el('td', { text: v.device + ' (' + v.role + ')' }), el('td', { class: 'r', text: mbps(v.tx_write_mbps) }), el('td', { class: 'r', text: mbps(v.rx_read_mbps) }), el('td', { class: 'r', text: nf1.format(v.tx_baseline_mbps) + ' Mo/s' }), el('td', { class: 'r', text: nf0.format(v.errors_rx + v.dropped_rx) }), el('td', { class: 'r', text: nf0.format(v.errors_tx + v.dropped_tx) }),
          esx ? esxCell(v, ['rx_crc', 'rx_frame', 'rx_length']) : null, esx ? esxCell(v, ['rx_missed', 'rx_fifo', 'rx_over']) : null));
        root.append(el('div', { class: 'tablewrap' }, vt));
      }
      const box = el('div', { class: 'chart' }); root.append(box);
      requestAnimationFrame(() => seriesChart(box, [{ label: 'écriture', s: r.write.seconds }, { label: 'lecture', s: r.read.seconds }]));
      for (const w of (r.warnings || [])) root.append(el('p', { class: 'small muted', text: '⚠ ' + w }));
    });
  }
  const patLabel = { read: 'lecture', write: 'écriture', mixed: 'mixte 70/30' };
  function verdictTable(v) {
    const t = el('table', { class: 'table' }, el('thead', {}, el('tr', {}, el('th', { text: 'Indicateur' }), el('th', { text: 'Valeur' }), el('th', { text: 'Verdict' }))));
    const tb = el('tbody'); t.append(tb);
    for (const ind of v.indicators) tb.append(el('tr', {}, el('td', { text: ind.label }), el('td', { text: ind.why }), el('td', {}, pill(ind.level))));
    return el('div', { class: 'tablewrap' }, t);
  }
  function renderAdvanced(root, r) {
    const h = r.headline, v = r.verdict, std = r.standard;
    root.append(el('div', { class: 'verdict lvl-' + v.level }, el('span', { class: 'lvl', text: v.level }),
      el('span', { text: 'Hôte ' + std.host.name + ', datastore ' + std.datastore.name + ', préréglage ' + v.preset + '. ' + (v.triggered && v.triggered.length ? v.triggered.map((x) => T(x)).join(' · ') : 'Aucun indicateur déclenché.') })));
    root.append(el('div', { class: 'tiles' },
      tile(mbps(h.write_mbps) + ' / ' + mbps(h.read_mbps), 'débit séquentiel écriture / lecture (1 Mio QD4)'),
      tile(h.useful_iops ? iops(h.useful_iops) : 'aucun palier', 'IOPS utiles 4 Kio mixte 70/30, p99 < ' + nf1.format(h.ceiling_ms) + ' ms' + (h.useful_qd ? ' (QD' + h.useful_qd + ')' : '')),
      tile(ms(h.read_qd1_p50_ms) + ' / ' + ms(h.read_qd1_p99_ms), 'latence 4 Kio QD1 lecture p50 / p99'),
      tile(ms(h.write_qd1_p50_ms) + ' / ' + ms(h.write_qd1_p99_ms), 'latence 4 Kio QD1 écriture p50 / p99')));
    root.append(el('p', { class: 'profile small', text: 'Profil séquentiel : ' + std.write.profile.text }));
    root.append(el('p', { class: 'profile small', text: 'Profil aléatoire : ' + r.steps[0].profile.text.replace(/QD \d+/, 'QD 1, 4, 16, 32').replace(/bloc \d+ Kio/, (r.options.full ? 'bloc 4, 8 et 64 Kio' : 'bloc 4 Kio')).replace(/^lecture /, 'lecture, écriture et mixte 70/30, ') }));
    root.append(verdictTable(v));
    // IOPS utiles par motif
    const ut = el('table', { class: 'table' }, el('thead', {}, el('tr', {}, ...['Motif', 'Bloc', 'IOPS utiles (p99 < ' + nf1.format(r.options.ceiling_ms) + ' ms)', 'à QD', 'maximum brut', 'à QD', 'p99 au maximum'].map((x, i) => el('th', { class: i > 1 ? 'r' : '', text: x })))));
    const ub = el('tbody'); ut.append(ub);
    for (const u of r.useful) ub.append(el('tr', {}, el('td', { text: patLabel[u.pattern] }), el('td', { text: u.block_kib + ' Kio' }), el('td', { class: 'r', text: u.iops ? iops(u.iops) : 'aucun' }), el('td', { class: 'r', text: u.qd || '—' }),
      el('td', { class: 'r', text: iops(u.raw_max_iops) }), el('td', { class: 'r', text: u.raw_max_qd }), el('td', { class: 'r', text: ms(u.raw_max_p99_ms) })));
    root.append(el('h3', { text: 'IOPS utiles' }), el('div', { class: 'tablewrap' }, ut));
    // courbe IOPS / p99
    root.append(el('h3', { text: 'IOPS et latence p99 (4 Kio)' }));
    const box = el('div', { class: 'chart' }); root.append(box);
    requestAnimationFrame(() => iopsLatencyChart(box, r.points.filter((p) => p.block_kib === 4), r.options.ceiling_ms));
    // tableau des paliers
    const pt = el('table', { class: 'table' }, el('thead', {}, el('tr', {}, ...['Palier', 'IOPS', 'Mo/s', 'p50', 'p99', 'p99,9', 'CPU worker', 'IOPS vSphere', 'écart', ''].map((x, i) => el('th', { class: i > 0 && i < 9 ? 'r' : '', text: x })))));
    const pb = el('tbody'); pt.append(pb);
    r.steps.forEach((s, i) => {
      const p = r.points[i];
      pb.append(el('tr', {}, el('td', { text: s.step.name }), el('td', { class: 'r', text: nf0.format(s.iops) }), el('td', { class: 'r', text: nf1.format(s.mbps) }),
        el('td', { class: 'r', text: ms(s.latency.p50_us / 1000) }), el('td', { class: 'r', text: ms(s.latency.p99_us / 1000) }), el('td', { class: 'r', text: ms(s.latency.p999_us / 1000) }),
        el('td', { class: 'r', text: nf0.format(p.cpu_pct) + ' %' }), el('td', { class: 'r', text: p.host_iops != null ? nf0.format(p.host_iops) : '—' }),
        el('td', { class: 'r', text: p.mismatch_pct != null ? nf1.format(p.mismatch_pct) + ' %' : '—' }), el('td', { class: 'small', text: (p.flags || []).join(', ') })));
    });
    root.append(el('h3', { text: 'Paliers' }), el('div', { class: 'tablewrap' }, pt));
    // distribution QD1
    const qd1 = r.steps.filter((s) => s.step.qd === 1 && s.step.block_kib === 4 && s.step.kind !== 'randmix');
    if (qd1.length) {
      root.append(el('h3', { text: 'Distribution des latences, 4 Kio QD1' }));
      const hb = el('div', { class: 'chart' }); root.append(hb);
      requestAnimationFrame(() => histChart(hb, qd1));
    }
    for (const w of [...(std.warnings || []), ...(r.warnings || [])]) root.append(el('p', { class: 'small muted', text: '⚠ ' + w }));
  }
  function iopsLatencyChart(box, points, ceiling) {
    const cs = getComputedStyle(document.documentElement);
    const pats = ['read', 'write', 'mixed'];
    const colors = [cs.getPropertyValue('--s1').trim(), cs.getPropertyValue('--s2').trim(), cs.getPropertyValue('--warn').trim()];
    const xs = [...new Set(points.map((p) => p.iops))].sort((a, b) => a - b);
    const data = [xs, ...pats.map((pat) => xs.map((x) => { const p = points.find((q) => q.pattern === pat && q.iops === x); return p ? p.p99_ms : null; })), xs.map(() => ceiling)];
    new uPlot({ width: Math.max(300, box.clientWidth || 800), height: 260, scales: { x: { time: false }, y: { distr: 3 } },
      axes: [{ label: 'IO/s', stroke: cs.getPropertyValue('--muted') }, { label: 'latence p99 (ms, échelle log)', stroke: cs.getPropertyValue('--muted') }],
      series: [{ label: 'IO/s' }, ...pats.map((pat, i) => ({ label: patLabel[pat], stroke: colors[i], width: 2, spanGaps: true, points: { size: 7 } })),
        { label: 'plafond ' + ceiling + ' ms', stroke: cs.getPropertyValue('--alert').trim(), dash: [6, 4], width: 1, points: { show: false } }] }, data, box);
  }
  function histChart(box, steps) {
    const cs = getComputedStyle(document.documentElement);
    const edges = [...new Set(steps.flatMap((s) => (s.buckets || []).map((b) => b[0])))].sort((a, b) => a - b);
    if (!edges.length) return;
    const xs = edges.map((ns) => ns / 1000);
    const ys = steps.map((s) => { const tot = (s.buckets || []).reduce((a, b) => a + b[1], 0) || 1; const m = new Map((s.buckets || []).map((b) => [b[0], b[1] / tot * 100])); return edges.map((e) => m.get(e) || null); });
    const colors = [cs.getPropertyValue('--s1').trim(), cs.getPropertyValue('--s2').trim()];
    new uPlot({ width: Math.max(300, box.clientWidth || 800), height: 220, scales: { x: { time: false, distr: 3 } },
      axes: [{ label: 'latence (µs, échelle log)', stroke: cs.getPropertyValue('--muted') }, { label: '% des I/O', stroke: cs.getPropertyValue('--muted') }],
      series: [{ label: 'µs' }, ...steps.map((s, i) => ({ label: s.step.kind === 'randread' ? 'lecture' : 'écriture', stroke: colors[i % 2], width: 1.5, spanGaps: false, points: { size: 3 } }))] }, [xs, ...ys], box);
  }
  function renderTour(root, r) {
    const c = r.comparison;
    root.append(el('div', { class: 'verdict lvl-' + c.level }, el('span', { class: 'lvl', text: c.level }), el('span', { text: c.sentence + (c.triggered && c.triggered.length ? ' ' + c.triggered.map((x) => T(x)).join(' · ') : '') })));
    const first = r.legs[0] && r.legs[0].report;
    if (first) root.append(el('p', { class: 'profile small', text: 'Profil de chaque passage : ' + first.write.profile.text + ' ; puis ' + first.read.profile.text + ' ; ligne de base ' + first.options.baseline_s + ' s. Tournée : ' + r.options.cycles + ' cycle(s) en alternance, override DRS ' + (r.drs ? 'posé puis restauré' : 'non utilisé') + ', hôte de départ ' + r.home_host + '.' }));
    const t = el('table', { class: 'table' }, el('thead', {}, el('tr', {}, ...['Hôte', 'Passages', 'Écriture (médiane)', 'Lecture (médiane)', 'Ratio TX/écrit', 'p99 écriture', 'vs meilleur', 'Verdict'].map((x, i) => el('th', { class: i > 0 && i < 7 ? 'r' : '', text: x })))));
    const b = el('tbody'); t.append(b);
    for (const h of c.hosts) b.append(el('tr', {}, el('td', { text: h.host }), el('td', { class: 'r', text: h.runs }), el('td', { class: 'r', text: mbps(h.write_mbps_median) }), el('td', { class: 'r', text: mbps(h.read_mbps_median) }),
      el('td', { class: 'r', text: h.tx_ratio_median ? nf3.format(h.tx_ratio_median) : 'n/d' }), el('td', { class: 'r', text: ms(h.write_p99_ms_median) }), el('td', { class: 'r', text: '×' + nf2.format(h.write_vs_best) }), el('td', {}, pill(h.level))));
    root.append(el('div', { class: 'tablewrap' }, t));
    const box = el('div', { class: 'chart' }); root.append(box);
    requestAnimationFrame(() => {
      const cs = getComputedStyle(document.documentElement);
      new uPlot({ width: Math.max(300, box.clientWidth || 800), height: 220, scales: { x: { time: false, range: [-0.5, c.hosts.length - 0.5] }, y: { range: (u, mn, mx) => [0, mx * 1.1] } },
        axes: [{ stroke: cs.getPropertyValue('--muted'), values: (u, vals) => vals.map((v) => (c.hosts[v] ? c.hosts[v].host : '')), incrs: [1] }, { label: 'Mo/s (médianes)', stroke: cs.getPropertyValue('--muted') }],
        series: [{}, { label: 'écriture', fill: cs.getPropertyValue('--s1').trim() + 'aa', stroke: cs.getPropertyValue('--s1').trim(), paths: uPlot.paths.bars({ size: [0.35], align: -1 }), points: { show: false } },
          { label: 'lecture', fill: cs.getPropertyValue('--s2').trim() + 'aa', stroke: cs.getPropertyValue('--s2').trim(), paths: uPlot.paths.bars({ size: [0.35], align: 1 }), points: { show: false } }] },
        [c.hosts.map((_, i) => i), c.hosts.map((h) => h.write_mbps_median), c.hosts.map((h) => h.read_mbps_median)], box);
    });
    const lt = el('table', { class: 'table' }, el('thead', {}, el('tr', {}, ...['Cycle', 'Hôte', 'vMotion', 'Écriture', 'Lecture', 'Ratio', 'Verdict'].map((x, i) => el('th', { class: i > 1 && i < 6 ? 'r' : '', text: x })))));
    const lb = el('tbody'); lt.append(lb);
    for (const l of r.legs) lb.append(el('tr', {}, el('td', { text: l.cycle }), el('td', { text: l.host }), el('td', { class: 'r', text: l.moved ? nf0.format(l.vmotion_s) + ' s' : '—' }),
      el('td', { class: 'r', text: mbps(l.report.write.mbps) }), el('td', { class: 'r', text: mbps(l.report.read.mbps) }), el('td', { class: 'r', text: l.report.tx_ratio && l.report.tx_ratio.samples ? nf3.format(l.report.tx_ratio.value) : 'n/d' }), el('td', {}, pill(l.report.verdict.level))));
    root.append(el('h3', { text: 'Passages' }), el('div', { class: 'tablewrap' }, lt));
    for (const w of (r.warnings || [])) root.append(el('p', { class: 'small muted', text: '⚠ ' + w }));
  }
  function renderBench(root, rep) {
    const t = el('table', { class: 'table' }, el('thead', {}, el('tr', {}, ...['Palier', 'Débit', 'IOPS', 'p50', 'p99', 'p99,9', 'CPU worker', 'Profil'].map((h, i) => el('th', { class: i > 0 && i < 7 ? 'r' : '', text: h })))));
    const tb = el('tbody'); t.append(tb);
    for (const r of (rep.results || [])) tb.append(el('tr', {}, el('td', { text: r.step.name || r.step.kind }), el('td', { class: 'r', text: mbps(r.mbps) }), el('td', { class: 'r', text: iops(r.iops) }),
      el('td', { class: 'r', text: ms(r.latency.p50_us / 1000) }), el('td', { class: 'r', text: ms(r.latency.p99_us / 1000) }), el('td', { class: 'r', text: ms(r.latency.p999_us / 1000) }),
      el('td', { class: 'r', text: nf0.format(r.cpu_pct) + ' %' }), el('td', { class: 'small', text: r.profile.text })));
    root.append(el('div', { class: 'tablewrap' }, t));
    if (rep.error) root.append(el('p', { class: 'error', text: rep.error }));
  }
  function seriesChart(box, list) {
    const cs = getComputedStyle(document.documentElement);
    const xs = [], ys = list.map(() => []);
    let x = 0;
    list.forEach((it, i) => (it.s || []).filter((s) => s.t > 0).forEach((s) => { x++; xs.push(x); list.forEach((_, k) => ys[k].push(k === i ? s.bytes / 1e6 : null)); }));
    if (!xs.length) return;
    const colors = [cs.getPropertyValue('--s1').trim(), cs.getPropertyValue('--s2').trim()];
    new uPlot({ width: Math.max(300, box.clientWidth || 800), height: 220, scales: { x: { time: false } },
      axes: [{ label: 's', stroke: cs.getPropertyValue('--muted') }, { label: 'Mo/s (à la seconde)', stroke: cs.getPropertyValue('--muted') }],
      series: [{}, ...list.map((it, i) => ({ label: it.label, stroke: colors[i % 2], width: 1.5 }))] }, [xs, ...ys], box);
  }


  // ================= Network =================
  const mbit = (v) => nf0.format(v) + ' Mbit/s';
  const rtt = (v) => v < 0 ? 'perdu' : (v < 10 ? nf2.format(v) : nf1.format(v)) + ' ms';
  let netLoaded = false, nmSel = null;
  async function loadNetwork() {
    const l = await api('GET', '/net/local');
    const tb = clear($('nl-table').tBodies[0]);
    for (const i of (l.interfaces || [])) tb.append(el('tr', {}, el('td', { text: i.name }), el('td', { text: i.cidr }), el('td', { text: i.mac }), el('td', { class: 'r', text: i.mtu })));
    $('nl-extra').textContent = 'Passerelle ' + (l.gateway || '—') + ' — DNS ' + ((l.dns || []).join(', ') || '—');
    await Promise.all([loadTargets(), loadARP(), loadPeers(), loadIperfServer()]);
    if (!netLoaded) { netLoaded = true; setInterval(() => { if (!$('tab-network').classList.contains('hidden')) { loadTargets(); if (nmSel) drawSeries(); } }, 60000); }
  }
  function targetVal() { return $('nt-target').value.trim(); }
  // Ping en temps réel : chaque réponse arrive par SSE et met à jour journal, graph et statistiques.
  let pingES = null, traceES = null;
  function pingStats(r) {
    const all = r.filter((v) => v !== undefined);
    const ok = all.filter((v) => v >= 0).sort((a, b) => a - b);
    const sent = all.length, recv = ok.length;
    const pct = (p) => { if (!ok.length) return null; const pos = p / 100 * (ok.length - 1), lo = Math.floor(pos), hi = Math.ceil(pos); return ok[lo] + (ok[hi] - ok[lo]) * (pos - lo); };
    let j = 0, jn = 0, prev = null;
    for (const v of all) { if (v < 0) continue; if (prev !== null) { j += Math.abs(v - prev); jn++; } prev = v; }
    return { sent, recv, loss: sent ? 100 * (sent - recv) / sent : 0, min: ok[0], max: ok[ok.length - 1], med: pct(50), p90: pct(90), jitter: jn ? j / jn : null };
  }
  function stopPing() { if (pingES) { pingES.close(); pingES = null; } $('nt-ping').disabled = false; $('nt-stop').classList.add('hidden'); }
  $('nt-ping').addEventListener('click', () => {
    $('nt-error').textContent = ''; const out = clear($('nt-result')); clear($('nt-trace-result'));
    const t = targetVal(); if (!t) return;
    stopPing(); if (traceES) { traceES.close(); traceES = null; }
    const count = Number($('nt-count').value), interval = Number($('nt-interval').value);
    const tiles = el('div', { class: 'tiles' }); const prof = el('p', { class: 'profile small' }); const box = el('div', { class: 'chart' });
    const logEl = el('pre', { class: 'log' });
    out.append(tiles, prof, box, el('details', { open: '' }, el('summary', { text: 'Réponses' }), logEl));
    const cs = getComputedStyle(document.documentElement);
    const data = [[], []]; const rtts = [];
    const plot = new uPlot({ width: Math.max(300, box.clientWidth || 800), height: 180, scales: { x: { time: false } },
      axes: [{ label: 'n° du ping', stroke: cs.getPropertyValue('--muted'), incrs: [1, 2, 5, 10, 20, 50, 100, 200, 500, 1000] }, { label: 'RTT (ms)', stroke: cs.getPropertyValue('--muted') }],
      series: [{}, { label: 'RTT', stroke: cs.getPropertyValue('--s1').trim(), width: 1.5, points: { size: 5 } }] }, data, box);
    let info = null, ended = false;
    const render = () => {
      const st = pingStats(rtts); clear(tiles);
      tiles.append(tile(nf1.format(st.loss) + ' %', 'perte (' + st.recv + '/' + st.sent + ')'), tile(st.recv ? rtt(st.med) : '—', 'RTT médian'),
        tile(st.recv ? rtt(st.min) + ' / ' + rtt(st.max) : '—', 'RTT min / max'), tile(st.jitter != null ? rtt(st.jitter) : '—', 'gigue'), tile(st.recv ? rtt(st.p90) : '—', 'RTT p90'));
    };
    render();
    $('nt-ping').disabled = true; $('nt-stop').classList.remove('hidden'); $('nt-why').textContent = 'ping en cours…';
    const es = new EventSource('/api/net/ping/stream?target=' + encodeURIComponent(t) + '&count=' + count + '&interval_ms=' + interval);
    pingES = es;
    es.addEventListener('start', (m) => {
      info = JSON.parse(m.data);
      prof.textContent = 'Profil : ' + (count ? count + ' échos' : 'échos en continu') + ' ICMP de 64 octets vers ' + info.ip + ', un toutes les ' + interval + ' ms, délai de réponse 2 s ; cible ' + (info.on_link ? 'dans le sous-réseau de l\'appliance' : 'hors du sous-réseau (routée)') + '.';
      logEl.append('PING ' + t + ' (' + info.ip + ')\n');
    });
    es.addEventListener('reply', (m) => {
      const r = JSON.parse(m.data);
      rtts[r.seq - 1] = r.rtt_ms;
      while (data[0].length < r.seq) { data[0].push(data[0].length + 1); data[1].push(null); }
      data[1][r.seq - 1] = r.rtt_ms >= 0 ? r.rtt_ms : null;
      plot.setData(data);
      logEl.append(T(r.rtt_ms >= 0 ? 'réponse de ' + info.ip + ' : seq=' + r.seq + ' temps=' + rtt(r.rtt_ms) : 'seq=' + r.seq + ' : pas de réponse (délai dépassé)') + '\n');
      logEl.scrollTop = logEl.scrollHeight;
      render();
    });
    const finish = (stopped) => {
      if (ended) return; ended = true;
      stopPing();
      const st = pingStats(rtts);
      logEl.append(T('--- ' + st.sent + ' émis, ' + st.recv + ' reçus, ' + nf1.format(st.loss) + ' % de perte') + (st.recv ? T(', min/méd/max ' + rtt(st.min) + ' / ' + rtt(st.med) + ' / ' + rtt(st.max)) : '') + (stopped ? ' ' + T('(arrêté)') : '') + '\n');
      $('nt-why').textContent = '';
      if (info && !info.on_link && !stopped) { $('nt-why').textContent = 'cible hors du sous-réseau : traceroute automatique'; traceroute(t); }
    };
    $('nt-stop').onclick = () => { es.close(); finish(true); $('nt-why').textContent = 'arrêté'; };
    es.addEventListener('done', (m) => { es.close(); finish(JSON.parse(m.data).stopped); });
    es.addEventListener('error', (m) => {
      if (m.data) { $('nt-error').textContent = JSON.parse(m.data).error; }
      else if (!ended) { $('nt-error').textContent = 'flux interrompu'; }
      es.close(); finish(true);
    });
  });
  // Traceroute en temps réel : un saut par événement.
  function traceroute(t) {
    if (traceES) traceES.close();
    const out = clear($('nt-trace-result'));
    const head = el('h3', { text: 'Traceroute vers ' + t + '…' });
    const tbl = el('table', { class: 'table' }, el('thead', {}, el('tr', {}, el('th', { class: 'r', text: 'Saut' }), el('th', { text: 'Adresse' }), el('th', { text: 'Nom' }), el('th', { class: 'r', text: 'RTT (3 sondes)' }))));
    const b = el('tbody'); tbl.append(b);
    const wait = el('p', { class: 'small muted', text: 'saut 1 en cours…' });
    out.append(head, el('div', { class: 'tablewrap' }, tbl), wait, el('p', { class: 'small muted', text: 'Profil : échos ICMP à TTL croissant, 3 sondes par saut, délai 1,5 s, 30 sauts au maximum.' }));
    const es = new EventSource('/api/net/traceroute/stream?target=' + encodeURIComponent(t));
    traceES = es;
    es.addEventListener('start', (m) => { const d = JSON.parse(m.data); head.textContent = 'Traceroute vers ' + d.target + ' (' + d.ip + ')'; });
    es.addEventListener('hop', (m) => {
      const h = JSON.parse(m.data);
      b.append(el('tr', {}, el('td', { class: 'r', text: h.ttl }), el('td', { text: h.addr || '*' }), el('td', { text: h.name || '' }), el('td', { class: 'r', text: h.rtts_ms.map((v) => v < 0 ? '*' : rtt(v)).join('  ') })));
      wait.textContent = h.reached ? '' : 'saut ' + (h.ttl + 1) + ' en cours…';
    });
    es.addEventListener('done', (m) => { const d = JSON.parse(m.data); es.close(); traceES = null; wait.textContent = d.reached ? 'cible atteinte en ' + d.hops + ' saut(s)' : 'cible non atteinte après ' + d.hops + ' sauts'; $('nt-why').textContent = ''; });
    es.addEventListener('error', (m) => { if (m.data) wait.textContent = JSON.parse(m.data).error; es.close(); traceES = null; });
  }
  $('nt-trace').addEventListener('click', () => { const t = targetVal(); if (t) traceroute(t); });
  $('nt-watch').addEventListener('click', async () => {
    $('nt-error').textContent = '';
    try { await api('POST', '/net/targets', { host: targetVal(), label: $('nt-label').value }); $('nt-label').value = ''; await loadTargets(); }
    catch (e) { $('nt-error').textContent = e.message; }
  });

  async function loadTargets() {
    const r = await api('GET', '/net/targets');
    const tb = clear($('nm-table').tBodies[0]);
    for (const t of (r.targets || [])) {
      const last = t.last;
      const loss = last && last.s ? (100 * (last.s - last.r) / last.s) : null;
      tb.append(el('tr', { class: 'click' + (nmSel === t.id ? ' sel' : ''), onclick: () => { nmSel = t.id; $('nm-title').textContent = (t.label ? t.label + ' — ' : '') + t.host; drawSeries(); loadTargets(); } },
        el('td', { text: (t.label ? t.label + ' — ' : '') + t.host }), el('td', { text: t.last_ip || '' }),
        el('td', { class: 'r', text: loss === null ? '…' : nf0.format(loss) + ' %' }), el('td', { class: 'r', text: last && last.r ? rtt(last.med) : '—' }),
        el('td', { class: 'r', text: last && last.r ? rtt(last.p90) : '—' }), el('td', { class: 'small', text: t.last_error || (last ? 'mesurée' : 'première salve dans moins d\'une minute') }),
        el('td', {}, el('button', { class: 'danger', text: 'Retirer', onclick: async (ev) => { ev.stopPropagation(); if (confirm('Arrêter la surveillance de ' + t.host + ' et effacer son historique ?')) { await api('DELETE', '/net/targets/' + encodeURIComponent(t.id)); if (nmSel === t.id) { nmSel = null; $('nm-graph').classList.add('hidden'); } loadTargets(); } } }))));
    }
  }
  $('nm-range').addEventListener('change', () => drawSeries());
  async function drawSeries() {
    if (!nmSel) return;
    const r = await api('GET', '/net/targets/' + encodeURIComponent(nmSel) + '/series?range=' + $('nm-range').value);
    $('nm-graph').classList.remove('hidden');
    $('nm-step').textContent = 'résolution ' + (r.step_s >= 3600 ? r.step_s / 3600 + ' h' : r.step_s / 60 + ' min') + ', ' + r.points.length + ' points';
    const box = clear($('nm-chart'));
    if (!r.points.length) { box.append(el('p', { class: 'muted small', text: 'Pas encore de mesure sur cette période.' })); return; }
    const cs = getComputedStyle(document.documentElement);
    const P = r.points, nul = (p, v) => (p.r ? v : null);
    const data = [P.map((p) => p.t), P.map((p) => nul(p, p.p10)), P.map((p) => nul(p, p.p25)), P.map((p) => nul(p, p.med)), P.map((p) => nul(p, p.p75)), P.map((p) => nul(p, p.p90)), P.map((p) => (p.s ? 100 * (p.s - p.r) / p.s : null))];
    const smoke = cs.getPropertyValue('--muted').trim();
    new uPlot({ width: Math.max(300, box.clientWidth || 800), height: 260, scales: { loss: { range: [0, 100] } },
      axes: [{ stroke: cs.getPropertyValue('--muted') }, { label: 'RTT (ms)', stroke: cs.getPropertyValue('--muted') }, { scale: 'loss', side: 1, label: 'perte %', stroke: cs.getPropertyValue('--alert'), grid: { show: false } }],
      series: [{}, { label: 'p10', stroke: 'transparent', points: { show: false } }, { label: 'p25', stroke: 'transparent', points: { show: false } },
        { label: 'médiane', stroke: cs.getPropertyValue('--s1').trim(), width: 2, points: { show: false } },
        { label: 'p75', stroke: 'transparent', points: { show: false } }, { label: 'p90', stroke: 'transparent', points: { show: false } },
        { label: 'perte %', scale: 'loss', stroke: cs.getPropertyValue('--alert').trim(), fill: cs.getPropertyValue('--alert').trim() + '55', width: 0, paths: uPlot.paths.bars({ size: [0.9, 6] }), points: { show: false } }],
      bands: [{ series: [5, 1], fill: smoke + '33' }, { series: [4, 2], fill: smoke + '66' }] }, data, box);
  }

  async function loadARP() {
    const r = await api('GET', '/net/arp');
    const tb = clear($('arp-table').tBodies[0]);
    for (const e of (r.entries || [])) tb.append(el('tr', {}, el('td', { text: e.ip }), el('td', { text: e.mac }), el('td', { text: e.device }), el('td', { text: e.state }),
      el('td', {}, el('button', { class: 'danger', text: 'Supprimer', onclick: async () => { try { await api('DELETE', '/net/arp/' + encodeURIComponent(e.ip)); } catch (x) { alert(x.message); } loadARP(); } }))));
  }
  $('arp-refresh').addEventListener('click', loadARP);
  $('arp-flush').addEventListener('click', async () => { if (confirm('Vider toute la table ARP de l\'appliance ?')) { try { await api('DELETE', '/net/arp'); } catch (x) { alert(x.message); } loadARP(); } });

  async function loadIperfServer() {
    const st = await api('GET', '/net/iperf/server');
    $('ip-srv').textContent = st.running ? 'ouvert sur le port ' + st.port + ' jusqu\'à ' + new Date(st.until).toLocaleTimeString(window.vigieLocale || 'en-US') + ' (par ' + st.by + ')' : 'fermé (port ' + st.port + ')';
    $('ip-srv-stop').disabled = !st.running;
  }
  $('ip-srv-start').addEventListener('click', async () => { try { await api('POST', '/net/iperf/server', { duration_s: 600 }); } catch (e) { alert(e.message); } loadIperfServer(); });
  $('ip-srv-stop').addEventListener('click', async () => { await api('DELETE', '/net/iperf/server'); loadIperfServer(); });

  let peerPending = null;
  async function loadPeers() {
    const r = await api('GET', '/net/peers');
    const tb = clear($('peer-table').tBodies[0]);
    const sel = clear($('ip-target'));
    for (const p of (r.peers || [])) {
      tb.append(el('tr', {}, el('td', { text: p.name }), el('td', { text: p.url }), el('td', { class: 'small', text: p.fingerprint.slice(0, 23) + '…' }),
        el('td', {}, el('button', { class: 'danger', text: 'Retirer', onclick: async () => { if (confirm('Retirer le pair ' + p.name + ' ?')) { await api('DELETE', '/net/peers/' + encodeURIComponent(p.id)); loadPeers(); } } }))));
      sel.append(el('option', { value: 'peer:' + p.id, text: 'appliance ' + p.name + ' (' + p.url.replace('https://', '') + ')' }));
    }
    sel.append(el('option', { value: 'host', text: 'autre serveur iperf3 (adresse IP)…' }));
    iperfForm();
  }
  $('peer-fp').addEventListener('click', async () => {
    $('peer-error').textContent = '';
    try { const r = await api('GET', '/net/peers/fingerprint?url=' + encodeURIComponent($('peer-url').value.trim())); peerPending = r; $('peer-sha').textContent = r.sha256; $('peer-cert').classList.remove('hidden'); }
    catch (e) { $('peer-error').textContent = e.message; }
  });
  $('peer-add').addEventListener('click', async () => {
    $('peer-error').textContent = '';
    try {
      await api('POST', '/net/peers', { name: $('peer-name').value, url: peerPending.url, token: $('peer-token').value.trim(), fingerprint: peerPending.sha256 });
      $('peer-token').value = ''; $('peer-name').value = ''; $('peer-url').value = ''; $('peer-cert').classList.add('hidden'); peerPending = null; loadPeers();
    } catch (e) { $('peer-error').textContent = e.message; }
  });
  function iperfForm() {
    $('ip-host-l').classList.toggle('hidden', $('ip-target').value !== 'host');
    $('ip-bw-l').classList.toggle('hidden', $('ip-proto').value !== 'udp');
  }
  ['ip-target', 'ip-proto'].forEach((id) => $(id).addEventListener('change', iperfForm));
  $('ip-run').addEventListener('click', async () => {
    $('ip-error').textContent = '';
    const tgt = $('ip-target').value;
    const req = { profile: 'net.iperf', protocol: $('ip-proto').value, duration_s: Number($('ip-dur').value), streams: Number($('ip-streams').value), reverse: $('ip-rev').value === '1' };
    if (tgt.startsWith('peer:')) req.peer = tgt.slice(5); else req.host = $('ip-host').value.trim();
    if (req.protocol === 'udp') req.bitrate = $('ip-bw').value.trim();
    try { const j = await api('POST', '/jobs', req); followIperf(j.id); } catch (e) { $('ip-error').textContent = e.message; }
  });
  let ipLive = null;
  function followIperf(id) {
    $('ip-run').disabled = true; $('ip-prog').classList.remove('hidden'); clear($('ip-result'));
    $('job-chip').classList.remove('hidden'); $('job-chip').className = 'chip busy'; $('job-chip').textContent = 'test de débit en cours';
    const box = clear($('ip-chart')); const cs = getComputedStyle(document.documentElement);
    const d = [[], [], []];
    ipLive = new uPlot({ width: Math.max(300, box.clientWidth || 800), height: 200, scales: { x: { time: false }, rt: { auto: true } },
      axes: [{ label: 's', stroke: cs.getPropertyValue('--muted') }, { label: 'Mbit/s', stroke: cs.getPropertyValue('--muted') }, { scale: 'rt', side: 1, label: 'retransmissions', stroke: cs.getPropertyValue('--warn'), grid: { show: false } }],
      series: [{}, { label: 'débit', stroke: cs.getPropertyValue('--s1').trim(), width: 2 }, { label: 'retransmissions', scale: 'rt', stroke: cs.getPropertyValue('--warn').trim(), width: 1 }] }, d, box);
    const es = new EventSource('/api/jobs/' + encodeURIComponent(id) + '/events');
    es.addEventListener('progress', (m) => { const e = JSON.parse(m.data); $('ip-bar').style.width = e.progress + '%'; $('ip-pct').textContent = e.step; });
    es.addEventListener('iperf', (m) => { const iv = JSON.parse(m.data).data; d[0].push(iv.end); d[1].push(iv.mbps); d[2].push(iv.retransmits); ipLive.setData(d); });
    es.addEventListener('state', async (m) => {
      const e = JSON.parse(m.data); if (e.state === 'running' || e.state === 'queued') return;
      es.close(); $('ip-run').disabled = false; $('job-chip').classList.add('hidden');
      const j = await api('GET', '/jobs/' + encodeURIComponent(id));
      if (j.error) $('ip-error').textContent = j.error;
      if (j.result) renderIperf($('ip-result'), j.result);
      loadIperfServer();
    });
  }
  function renderIperf(root, r) {
    clear(root);
    if (!r) return;
    const o = r.options;
    root.append(el('div', { class: 'tiles' }, tile(mbit(r.sent_mbps), 'débit émis'), tile(mbit(r.received_mbps), 'débit reçu'),
      o.protocol === 'tcp' ? tile(nf0.format(r.retransmits), 'retransmissions TCP') : tile(nf2.format(r.jitter_ms) + ' ms / ' + nf2.format(r.lost_pct) + ' %', 'gigue / perte UDP'),
      tile(nf0.format(r.sender_cpu_pct) + ' % / ' + nf0.format(r.receiver_cpu_pct) + ' %', 'CPU émetteur / récepteur')));
    root.append(el('p', { class: 'profile small', text: 'Profil : iperf ' + (r.version || '3') + ', ' + o.protocol.toUpperCase() + ', ' + (o.reverse ? 'le serveur ' + o.host + ' émet vers cette appliance (-R)' : 'cette appliance émet vers ' + o.host) +
      ', ' + o.streams + ' flux, ' + o.duration_s + ' s' + (o.protocol === 'udp' ? ', débit cible ' + o.bitrate : '') + (r.tcp_mss ? ', MSS ' + r.tcp_mss : '') + '.' }));
    if (r.intervals && r.intervals.length && !root.closest('#ip-result')) {
      const box = el('div', { class: 'chart' }); root.append(box);
      const cs = getComputedStyle(document.documentElement);
      requestAnimationFrame(() => new uPlot({ width: Math.max(300, box.clientWidth || 800), height: 200, scales: { x: { time: false } },
        axes: [{ label: 's', stroke: cs.getPropertyValue('--muted') }, { label: 'Mbit/s', stroke: cs.getPropertyValue('--muted') }],
        series: [{}, { label: 'débit', stroke: cs.getPropertyValue('--s1').trim(), width: 2 }] }, [r.intervals.map((i) => i.end), r.intervals.map((i) => i.mbps)], box));
    }
  }

  // ---------- historique ----------
  const REPORTABLE = ['standard', 'advanced', 'tour'];
  const reportURL = (id, dl) => '/api/reports/' + encodeURIComponent(id) + (dl ? '?download=1' : '');
  function reportLinks(id) {
    return el('span', { class: 'reportbar' },
      el('a', { class: 'btn primary', href: reportURL(id), target: '_blank', rel: 'noopener', text: 'Rapport client' }),
      el('a', { class: 'btn', href: reportURL(id, true), text: 'Télécharger (HTML)' }));
  }
  const cmpSel = [];
  function cmpUpdate() {
    $('cmp-open').disabled = cmpSel.length !== 2;
    $('cmp-hint').textContent = cmpSel.length === 2 ? 'Le plus ancien est l\'« avant ».' : 'Cochez deux tests (' + cmpSel.length + '/2).';
  }
  $('cmp-open').addEventListener('click', () => {
    if (cmpSel.length === 2) window.open('/api/reports/compare?a=' + encodeURIComponent(cmpSel[0]) + '&b=' + encodeURIComponent(cmpSel[1]), '_blank', 'noopener');
  });
  async function loadHistory() {
    loadTrends().catch((e) => { $('tr-profile').textContent = e.message; });
    const r = await api('GET', '/jobs?limit=100');
    const tb = clear($('hist-table').tBodies[0]);
    cmpSel.length = 0; cmpUpdate();
    for (const j of (r.jobs || [])) {
      const ok = REPORTABLE.includes(j.profile) && j.state === 'succeeded';
      const sm = j.summary || {};
      const cb = ok ? el('input', { type: 'checkbox', title: 'sélectionner pour un rapport avant / après' }) : null;
      if (cb) {
        cb.addEventListener('click', (ev) => ev.stopPropagation());
        cb.addEventListener('change', () => {
          const i = cmpSel.indexOf(j.id);
          if (cb.checked && i < 0) { cmpSel.push(j.id); if (cmpSel.length > 2) { const old = cmpSel.shift(); const o = tb.querySelector('input[data-id="' + old + '"]'); if (o) o.checked = false; } }
          if (!cb.checked && i >= 0) cmpSel.splice(i, 1);
          cmpUpdate();
        });
        cb.setAttribute('data-id', j.id);
      }
      const links = ok ? el('span', { class: 'reportbar' }, el('a', { class: 'btn', href: reportURL(j.id), target: '_blank', rel: 'noopener', text: 'Ouvrir', onclick: (ev) => ev.stopPropagation() }),
        el('a', { class: 'btn', href: reportURL(j.id, true), text: 'HTML', onclick: (ev) => ev.stopPropagation() })) : null;
      const tr = el('tr', { class: 'click' }, el('td', {}, cb), el('td', { text: dt(j.created) }), el('td', { text: j.profile }), el('td', { text: j.state + (j.error ? ' — ' + j.error : '') }),
        el('td', { text: [sm.host, sm.datastore].filter(Boolean).join(' · ') }), el('td', {}, sm.level ? pill(sm.level) : null), el('td', { class: 'small', text: sm.text || '' }),
        el('td', {}, links), el('td', { text: j.by === 'schedule' ? 'planificateur' : (j.by || '') }));
      tr.addEventListener('click', async () => {
        const full = await api('GET', '/jobs/' + encodeURIComponent(j.id));
        $('hist-detail').classList.remove('hidden'); $('hist-title').textContent = full.profile + ' — ' + dt(full.created);
        renderResult($('hist-result'), full); $('hist-detail').scrollIntoView({ behavior: 'smooth' });
      });
      tb.append(tr);
    }
  }

  // ---------- réglages ----------
  async function loadSettings() {
    loadBranding().catch((e) => { $('br-msg').textContent = e.message; });
    const r = await api('GET', '/tokens');
    const tb = clear($('tok-table').tBodies[0]);
    for (const t of (r.tokens || [])) tb.append(el('tr', {}, el('td', { text: t.name }), el('td', { text: dt(t.created) }), el('td', { text: dt(t.last_used) || 'jamais' }),
      el('td', {}, el('button', { class: 'danger', text: 'Révoquer', onclick: async () => { if (confirm('Révoquer le jeton ' + t.name + ' ?')) { await api('DELETE', '/tokens/' + encodeURIComponent(t.id)); loadSettings(); } } }))));
    state.thresholds = await api('GET', '/thresholds');
    state.presets = { default: state.thresholds.default, presets: state.thresholds.presets };
    const th = clear($('th-table'));
    const names = Object.keys(state.presets.presets);
    const rows = [['Ratio TX/écrit ATTENTION / ALERTE', (p) => nf2.format(p.tx_ratio_warn) + ' / ' + nf2.format(p.tx_ratio_alert)],
      ['Débit d\'écriture ATTENTION / ALERTE', (p) => '< ' + p.write_mbps_warn + ' / < ' + p.write_mbps_alert + ' Mo/s'],
      ['Latence d\'écriture datastore', (p) => '> ' + p.ds_write_latency_warn_ms + ' / > ' + p.ds_write_latency_alert_ms + ' ms'],
      ['Latence 4 Kio QD1 p99 (avancé)', (p) => '> ' + p.qd1_p99_warn_ms + ' / > ' + p.qd1_p99_alert_ms + ' ms'],
      ['Plafond IOPS utiles (avancé)', (p) => p.useful_iops_ceiling_ms + ' ms'],
      ['Avertissements NFS', (p) => '> ' + p.nfs_warn_lines_warn + ' / > ' + p.nfs_warn_lines_alert],
      ['Erreurs NIC, changements de lien', () => 'ALERTE dès 1']];
    th.append(el('thead', {}, el('tr', {}, el('th', { text: 'Indicateur' }), ...names.map((n) => el('th', { text: n })))));
    const b = el('tbody'); th.append(b);
    for (const [l, f] of rows) b.append(el('tr', {}, el('td', { text: l }), ...names.map((n) => el('td', { text: f(state.presets.presets[n]) }))));
    const ps = $('th-preset'), cur = ps.value || state.presets.default;
    clear(ps);
    for (const n of names) ps.append(el('option', { value: n, text: n + ((state.thresholds.modified || []).includes(n) ? ' (modifié)' : '') }));
    ps.value = cur; editThresholds();
  }
  // seuils modifiables : [libellé, clé ATTENTION, clé ALERTE]
  const TH_EDIT = [['Ratio TX/écrit corrigé', 'tx_ratio_warn', 'tx_ratio_alert'], ['Débit d\'écriture (Mo/s, en dessous)', 'write_mbps_warn', 'write_mbps_alert'],
    ['Latence d\'écriture datastore (ms)', 'ds_write_latency_warn_ms', 'ds_write_latency_alert_ms'], ['Latence 4 Kio QD1 p99 (ms)', 'qd1_p99_warn_ms', 'qd1_p99_alert_ms'],
    ['Avertissements NFS (lignes)', 'nfs_warn_lines_warn', 'nfs_warn_lines_alert'], ['Écart entre hôtes (×)', 'host_spread_warn', 'host_spread_alert'],
    ['Erreurs NIC (au-dessus de)', null, 'nic_errors_alert'], ['Erreurs CRC, trame, longueur (au-dessus de)', null, 'nic_phys_errors_alert'],
    ['Trames manquées par la carte (au-dessus de)', 'rx_missed_warn', null], ['Changements d\'état de lien (au-dessus de)', null, 'link_flaps_alert'],
    ['Écart IOPS appliance / vSphere (%)', 'iops_mismatch_warn_pct', null], ['CPU du worker (%)', 'worker_cpu_warn_pct', null],
    ['Plafond de latence p99 des IOPS utiles (ms)', 'useful_iops_ceiling_ms', null]];
  function editThresholds() {
    const p = state.presets.presets[$('th-preset').value];
    const tb = clear($('th-edit').tBodies[0]);
    const inp = (k) => k ? el('input', { type: 'number', step: 'any', min: '0', value: String(p[k]), 'data-k': k }) : el('span', { class: 'muted', text: '—' });
    for (const [l, w, a] of TH_EDIT) tb.append(el('tr', {}, el('td', { text: l }), el('td', {}, inp(w)), el('td', {}, inp(a))));
    $('th-msg').textContent = '';
  }
  $('th-preset').addEventListener('change', editThresholds);
  $('th-save').addEventListener('click', async () => {
    const n = $('th-preset').value, t = Object.assign({}, state.presets.presets[n]);
    for (const i of $('th-edit').querySelectorAll('input[data-k]')) t[i.getAttribute('data-k')] = Number(i.value);
    try { await api('PUT', '/thresholds', { preset: n, thresholds: t }); await loadSettings(); $('th-msg').textContent = 'Enregistré.'; $('th-msg').className = 'small ok'; }
    catch (e) { $('th-msg').textContent = e.message; $('th-msg').className = 'small error'; }
  });
  $('th-reset').addEventListener('click', async () => {
    const n = $('th-preset').value;
    if (!confirm('Revenir aux valeurs fournies pour le préréglage ' + n + ' ?')) return;
    try { await api('PUT', '/thresholds', { preset: n }); await loadSettings(); $('th-msg').textContent = 'Valeurs fournies rétablies.'; $('th-msg').className = 'small ok'; }
    catch (e) { $('th-msg').textContent = e.message; $('th-msg').className = 'small error'; }
  });
  // personnalisation du rapport client
  let brandLogo = '';
  function showLogo() {
    $('br-logo').classList.toggle('hidden', !brandLogo); $('br-logo-rm').classList.toggle('hidden', !brandLogo);
    if (brandLogo) $('br-logo').src = brandLogo; else $('br-logo').removeAttribute('src');
  }
  async function loadBranding() {
    const b = await api('GET', '/branding');
    $('br-client').value = b.client || ''; $('br-site').value = b.site || ''; $('br-author').value = b.author || ''; $('br-comment').value = b.comment || '';
    brandLogo = b.logo || ''; showLogo();
  }
  $('br-logo-file').addEventListener('change', () => {
    const f = $('br-logo-file').files[0];
    $('br-msg').textContent = ''; $('br-msg').className = 'small';
    if (!f) return;
    if (f.size > 200 * 1024) { $('br-msg').textContent = 'Logo trop lourd : 200 Kio au maximum.'; $('br-msg').className = 'small error'; $('br-logo-file').value = ''; return; }
    const rd = new FileReader();
    rd.onload = () => { brandLogo = String(rd.result); showLogo(); $('br-msg').textContent = 'Logo chargé : pensez à enregistrer.'; };
    rd.readAsDataURL(f);
  });
  $('br-logo-rm').addEventListener('click', () => { brandLogo = ''; $('br-logo-file').value = ''; showLogo(); $('br-msg').textContent = 'Logo retiré : pensez à enregistrer.'; });
  $('br-save').addEventListener('click', async () => {
    try {
      await api('PUT', '/branding', { client: $('br-client').value, site: $('br-site').value, author: $('br-author').value, comment: $('br-comment').value, logo: brandLogo });
      $('br-msg').textContent = 'Enregistré.'; $('br-msg').className = 'small ok';
    } catch (e) { $('br-msg').textContent = e.message; $('br-msg').className = 'small error'; }
  });

  $('tok-create').addEventListener('click', async () => {
    try {
      const r = await api('POST', '/tokens', { name: $('tok-name').value });
      const box = clear($('tok-secret')); box.classList.remove('hidden');
      box.append(el('span', { text: 'Jeton « ' + r.name + ' » (copiez-le maintenant, il ne sera plus affiché) : ' }), el('code', { class: 'fp', text: r.token }));
      $('tok-name').value = ''; loadSettings();
    } catch (e) { alert(e.message); }
  });

  async function loadAbout() {
    const a = await api('GET', '/about');
    $('about-text').textContent = 'vigie ' + a.version + ' (commit ' + a.commit + ', build ' + a.build_date + ') sur ' + a.hostname + '. Certificat HTTPS de l\'appliance : SHA-256 ' + a.tls_fingerprint + '. Session : ' + a.user + '.';
  }

  // ---------- surveillance continue (phase 9) ----------
  function scheduleBody(enabled) {
    return { enabled, interval_min: Number($('m-interval').value), profile: $('m-profile').value, preset: $('m-preset').value,
      retention_days: Number($('m-retention').value) || 30, baseline_s: Number($('m-base').value) || 0, write_s: Number($('m-dur').value) || 30, read_s: Number($('m-dur').value) || 30 };
  }
  function monProfile() {
    const b = scheduleBody(true);
    $('m-profile-text').textContent = 'Profil : écriture puis lecture séquentielles, bloc 1 Mio, QD 4, ' + b.write_s + ' s chacune sans chauffe, sur toute la zone du disque de test, O_DIRECT+O_SYNC, données aléatoires incompressibles renouvelées à chaque passe' +
      (b.profile === 'standard' ? ' ; ligne de base au repos ' + b.baseline_s + ' s.' : ' ; sans vCenter : ni ratio TX/écrit ni compteurs de l\'hôte.');
  }
  ['m-interval', 'm-profile', 'm-base', 'm-dur'].forEach((id) => $(id).addEventListener('input', monProfile));
  function showSchedule(s) {
    $('m-interval').value = String(s.interval_min); $('m-profile').value = s.profile; $('m-retention').value = s.retention_days;
    $('m-base').value = s.baseline_s; $('m-dur').value = s.write_s; if (s.preset) $('m-preset').value = s.preset;
    $('m-save').textContent = s.enabled ? 'Enregistrer' : 'Activer la surveillance'; $('m-stop').classList.toggle('hidden', !s.enabled);
    const parts = [s.enabled ? 'Surveillance active : prochain test ' + (s.next ? dt(s.next) : '—') + '.' : 'Surveillance arrêtée.'];
    if (s.last_run) parts.push('Dernier test planifié : ' + dt(s.last_run) + '.');
    if (s.skip_reason) parts.push('Test sauté le ' + dt(s.last_skip) + ' : ' + s.skip_reason + '.');
    if (s.profile === 'standard') parts.push(s.vsphere_remembered ? 'Mot de passe vCenter mémorisé : reconnexion automatique après un redémarrage.' : 'Mot de passe vCenter non mémorisé : après un redémarrage, les tests standard planifiés seront sautés jusqu\'à la reconnexion (étape 1).');
    if (s.vsphere_reconnect) parts.push(s.vsphere_reconnect);
    const st = clear($('m-status'));
    for (const t of parts) st.append(el('span', { text: t }), ' ');
    setBadge('b-trend', s.enabled ? 'surveillance active' : '', s.enabled ? true : undefined);
    monProfile();
  }
  async function loadSchedule() { showSchedule(await api('GET', '/schedule')); }
  async function saveSchedule(enabled) {
    $('m-error').textContent = '';
    try { showSchedule(await api('PUT', '/schedule', scheduleBody(enabled))); } catch (e) { $('m-error').textContent = e.message; }
  }
  $('m-save').addEventListener('click', () => saveSchedule(true));
  $('m-stop').addEventListener('click', () => saveSchedule(false));

  // ---------- tendances (historique des mesures) ----------
  let trendKey = '';
  function trendQuery(fmt) {
    const [host, ds] = ($('tr-series').value || '\u0000').split('\u0000');
    const q = new URLSearchParams({ period: $('tr-period').value });
    if (host) q.set('host', host);
    if (ds) q.set('datastore', ds);
    if (fmt) q.set('format', fmt);
    return '/results?' + q.toString();
  }
  async function loadTrends() {
    const r = await api('GET', trendQuery());
    const sel = $('tr-series'), keep = sel.value || trendKey;
    clear(sel);
    for (const [h, d] of (r.series || [])) sel.append(el('option', { value: h + '\u0000' + d, text: (h === 'local' ? 'local (sans vCenter)' : h) + ' · ' + d }));
    if ([...sel.options].some((o) => o.value === keep)) sel.value = keep;
    trendKey = sel.value;
    $('tr-csv').href = '/api' + trendQuery('csv'); $('tr-json').href = '/api' + trendQuery('json');
    const pts = r.series && r.series.length && trendKey && !keep ? (await api('GET', trendQuery())).points : r.points;
    drawTrends(pts.filter((p) => $('tr-sched').value === '' || String(!!p.scheduled) === ($('tr-sched').value === '1' ? 'true' : 'false')));
  }
  function drawTrends(pts) {
    const box = clear($('tr-charts'));
    $('tr-profile').textContent = '';
    if (!pts.length) { box.append(el('p', { class: 'muted small', text: 'Pas encore de mesure sur cette période.' })); return; }
    const xs = pts.map((p) => new Date(p.t).getTime() / 1000);
    const col = { w: '#d9822b', r: '#2b7bd9' };
    const dot = { size: 5 };
    const mk = (title, unit, data, series, log) => {
      const c = el('div', { class: 'chart' }); box.append(el('h3', { text: title }), c);
      requestAnimationFrame(() => new uPlot({ width: Math.max(300, c.clientWidth || 800), height: 220, scales: log ? { y: { distr: 3 } } : {},
        axes: [{}, { label: unit }], series: [{}, ...series] }, [xs, ...data], c));
    };
    mk('Débit séquentiel (1 Mio QD4)', 'Mo/s', [pts.map((p) => p.write_mbps), pts.map((p) => p.read_mbps)],
      [{ label: 'écriture', stroke: col.w, width: 1.5, points: dot }, { label: 'lecture', stroke: col.r, width: 1.5, points: dot }]);
    mk('Latence p99 (1 Mio QD4)', 'ms', [pts.map((p) => p.write_p99_ms), pts.map((p) => p.read_p99_ms)],
      [{ label: 'écriture', stroke: col.w, width: 1.5, points: dot }, { label: 'lecture', stroke: col.r, width: 1.5, points: dot }], true);
    if (pts.some((p) => p.tx_ratio != null)) {
      mk('Ratio TX/écrit corrigé', '', [pts.map((p) => p.tx_ratio == null ? null : p.tx_ratio)], [{ label: 'ratio', stroke: '#7a4cc2', width: 1.5, points: dot, spanGaps: true }]);
    }
    const last = pts[pts.length - 1];
    const lv = pts.filter((p) => p.level === 'ALERTE').length, wv = pts.filter((p) => p.level === 'ATTENTION').length;
    clear($('tr-profile')).append(el('span', { text: pts.length + ' mesure(s), dont ' + wv + ' ATTENTION et ' + lv + ' ALERTE.' }), ' ',
      el('span', { text: 'Profil de la dernière mesure (' + dt(last.t) + ') :' }), ' ', el('span', { text: last.io_profile || '—' }));
  }
  ['tr-series', 'tr-period', 'tr-sched'].forEach((id) => $(id).addEventListener('change', () => { trendKey = $('tr-series').value; loadTrends().catch((e) => { $('tr-profile').textContent = e.message; }); }));

  // ---------- démarrage ----------
  async function refreshAll() {
    try {
      await refreshVS(); await refreshSelf(); await refreshDisk(); await refreshDS(); await refreshDisk();
    } catch (e) { $('connect-error').textContent = e.message; }
  }
  async function boot() {
    try {
      if (!state.presets) state.presets = await api('GET', '/presets');
      const sel = clear($('t-preset'));
      for (const n of Object.keys(state.presets.presets)) { const o = el('option', { value: n, text: n }); o.selected = n === state.presets.default; sel.append(o); }
      const ms = clear($('m-preset'));
      for (const n of Object.keys(state.presets.presets)) { const o = el('option', { value: n, text: n }); o.selected = n === state.presets.default; ms.append(o); }
      await refreshAll();
      loadNetwork().catch(() => {}); // onglet affiché par défaut
      const jobs = await api('GET', '/jobs?limit=20');
      if (jobs.current && jobs.current.profile === 'net.iperf') followIperf(jobs.current.id);
      else if (jobs.current) followJob(jobs.current.id);
      else {
        const last = (jobs.jobs || []).find((j) => ['standard', 'advanced', 'tour'].includes(j.profile) && j.state === 'succeeded');
        if (last) { const full = await api('GET', '/jobs/' + encodeURIComponent(last.id)); renderResult($('result'), full); setBadge('b-result', 'dernier test : ' + dt(full.created)); }
      }
    } catch (e) { /* 401 : écran de connexion déjà affiché */ }
  }
  (async () => {
    try { await api('GET', '/vsphere/status'); showApp(); boot(); } catch (_) { showLogin(); }
  })();
})();

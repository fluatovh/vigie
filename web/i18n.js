// vigie — traduction de l'interface web (même catalogue et même moteur que la console : i18n.tsv,
// une ligne par texte source français, colonnes fr en es it pl de ; gabarits « %d/%d reçus »).
// Le texte est traduit au fil de l'affichage (MutationObserver) : app.js reste écrit en français.
// Changer de langue recharge la page.
(function () {
  'use strict';
  const CODES = ['fr', 'en', 'es', 'it', 'pl', 'de'];
  const LOCALES = { en: 'en-US', fr: 'fr-FR', es: 'es-ES', it: 'it-IT', pl: 'pl-PL', de: 'de-DE' };
  let lang = 'en';
  try { const l = localStorage.getItem('vigie-lang'); if (LOCALES[l]) lang = l; } catch (_) { /* stockage indisponible */ }
  const col = CODES.indexOf(lang);
  window.vigieLang = lang;
  window.vigieLocale = LOCALES[lang];
  document.documentElement.lang = lang;

  const verbRe = /%(\[\d+\])?[-+# 0-9.]*[a-zA-Z]/g;
  const exact = new Map();
  const patterns = [];
  const cache = new Map();
  const missing = new Set();
  window.__i18nMissing = missing;

  function esc(s) { return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'); }
  function load(tsv) {
    for (const raw of tsv.split('\n')) {
      const line = raw.replace(/\r$/, '');
      if (!line || line[0] === '#') continue;
      const f = line.split('\t');
      if (f.length !== 6) continue;
      if (!verbRe.test(f[0].replace(/%%/g, ''))) {
        verbRe.lastIndex = 0;
        exact.set(f[0].replace(/%%/g, '%'), f.map((x) => x.replace(/%%/g, '%')));
        continue;
      }
      verbRe.lastIndex = 0;
      let re = '^', src = f[0].replace(/%%/g, '\u0001'), prefix = null, m; // « %% sur » n'est pas un verbe « % s »
      while (true) {
        verbRe.lastIndex = 0;
        m = verbRe.exec(src);
        const lit = (m ? src.slice(0, m.index) : src).replace(/\u0001/g, '%');
        if (prefix === null) prefix = lit;
        re += esc(lit);
        if (!m) break;
        re += '(.+?)';
        src = src.slice(m.index + m[0].length);
      }
      patterns.push({ re: new RegExp(re + '$'), prefix, lit: f[0].replace(/%%/g, '%').replace(verbRe, '').length, out: f.map((x) => x.replace(/%%/g, '\u0000')) });
    }
  }
  // du plus précis au plus général (texte fixe initial le plus long d'abord)
  // à préfixe égal, le gabarit au texte fixe le plus long (le plus précis) d'abord : « %s — %s Gio %s sur %s — prérempli %s Gio » avant « %s — %s »
  function sortPatterns() { patterns.sort((a, b) => b.prefix.length - a.prefix.length || b.lit - a.lit); }
  function fill(fmt, args) {
    let i = 0;
    return fmt.replace(/%(\[(\d+)\])?[-+# 0-9.]*[a-zA-Z]/g, (_, __, n) => String(n ? args[n - 1] : args[i++])).replace(/\u0000/g, '%');
  }
  function lookup(s, depth) {
    if (exact.has(s)) return exact.get(s)[col];
    if (depth >= 6) return null; // gabarits imbriqués : verdict > indicateur > valeur
    for (const p of patterns) {
      if (!s.startsWith(p.prefix)) continue;
      const m = p.re.exec(s);
      if (!m) continue;
      return fill(p.out[col], m.slice(1).map((a) => translate(a, depth + 1)));
    }
    return null;
  }
  // nombre décimal à la française produit par le serveur (« 1,09 ») : remis au format de la langue
  // (« 1,09 », « 1 258 », « 12 345,6 »)
  const frNum = /^(-?\d{1,3}(?:[\u00a0\u202f ]\d{3})+|-?\d+)(?:,(\d+))?$/;
  function number(s) {
    const m = frNum.exec(s);
    if (!m || (!m[2] && !/[\u00a0\u202f ]/.test(m[1]))) return null; // entier simple : inchangé
    const d = m[2] ? m[2].length : 0;
    const v = Number(m[1].replace(/[\u00a0\u202f ]/g, '') + (d ? '.' + m[2] : ''));
    return new Intl.NumberFormat(LOCALES[lang], { minimumFractionDigits: d, maximumFractionDigits: d }).format(v);
  }
  function translate(s, depth) {
    const num = number(s);
    if (num !== null) return num;
    let out = lookup(s, depth);
    if (out !== null) return out;
    // texte du rapport (espaces insécables avant les unités) : même gabarit qu'avec des espaces
    if (s.includes('\u00a0')) { out = lookup(s.replace(/\u00a0/g, ' '), depth); if (out !== null) return out; }
    const t = s.trim();
    if (t && t !== s) {
      out = lookup(t, depth);
      if (out !== null) { const i = s.indexOf(t); return s.slice(0, i) + out + s.slice(i + t.length); }
    }
    if (depth === 0 && /[A-Za-zÀ-ÿ]{2}/.test(s)) missing.add(s);
    return s;
  }
  // T : traduction d'un texte source (français).
  window.T = function (s) {
    if (s == null || s === '' || col === 0) return s;
    s = String(s);
    if (cache.has(s)) return cache.get(s);
    const out = translate(s, 0);
    if (cache.size > 20000) cache.clear();
    cache.set(s, out);
    return out;
  };

  // --- traduction du DOM ---
  const done = new WeakMap(); // nœud texte -> dernière valeur posée
  const ATTRS = ['placeholder', 'title', 'aria-label'];
  function textNode(n) {
    const p = n.parentNode;
    if (!p || p.nodeName === 'SCRIPT' || p.nodeName === 'STYLE' || p.closest && p.closest('[data-raw], input, textarea, code, pre.log')) return;
    if (done.get(n) === n.data) return;
    const v = window.T(n.data);
    done.set(n, v);
    if (v !== n.data) n.data = v;
  }
  function element(e) {
    for (const a of ATTRS) if (e.hasAttribute(a)) { const v = e.getAttribute(a), t = window.T(v); if (t !== v) e.setAttribute(a, t); }
  }
  function tree(root) {
    if (root.nodeType === 3) return textNode(root);
    if (root.nodeType !== 1) return;
    element(root);
    const w = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT);
    for (let n = w.nextNode(); n; n = w.nextNode()) { if (n.nodeType === 3) textNode(n); else element(n); }
  }
  function observe() {
    new MutationObserver((list) => {
      for (const m of list) {
        if (m.type === 'characterData') textNode(m.target);
        else if (m.type === 'attributes') element(m.target);
        else for (const n of m.addedNodes) tree(n);
      }
    }).observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: ATTRS });
  }

  // boîtes et graphs : traduits sans toucher à app.js
  const nativeAlert = window.alert.bind(window), nativeConfirm = window.confirm.bind(window);
  window.alert = (m) => nativeAlert(window.T(m));
  window.confirm = (m) => nativeConfirm(window.T(m));
  if (window.uPlot) {
    const U = window.uPlot;
    const W = function (opts, data, target) {
      for (const a of opts.axes || []) if (typeof a.label === 'string') a.label = window.T(a.label);
      for (const s of opts.series || []) if (typeof s.label === 'string') s.label = window.T(s.label);
      return new U(opts, data, target);
    };
    Object.assign(W, U);
    W.prototype = U.prototype;
    window.uPlot = W;
  }

  // sélecteurs de langue (bandeau et écran de connexion)
  function selectors() {
    const names = { en: 'English', fr: 'Français', es: 'Español', it: 'Italiano', pl: 'Polski', de: 'Deutsch' };
    for (const sel of document.querySelectorAll('select.lang')) {
      sel.setAttribute('data-raw', '');
      for (const c of ['en', 'fr', 'es', 'it', 'pl', 'de']) {
        const o = document.createElement('option');
        o.value = c; o.textContent = names[c]; o.selected = c === lang;
        sel.append(o);
      }
      sel.addEventListener('change', () => {
        try { localStorage.setItem('vigie-lang', sel.value); } catch (_) { /* rien */ }
        location.reload();
      });
    }
  }

  function start() {
    selectors();
    document.title = window.T(document.title);
    tree(document.body);
    observe();
    const s = document.createElement('script');
    s.src = 'app.js';
    document.body.append(s);
  }
  fetch('i18n.tsv', { credentials: 'same-origin' }).then((r) => r.text()).then((t) => { load(t); sortPatterns(); }).catch(() => { /* sans catalogue : français */ }).finally(start);
})();

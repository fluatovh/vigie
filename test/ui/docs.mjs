// Captures d'écran de la documentation (docs/img/) : parcours de l'interface en anglais sur une
// appliance connectée à un vCenter. Les identifiants de l'infrastructure (vCenter, compte, hôtes,
// datastores, adresses) sont remplacés par des valeurs fictives avant chaque capture (REDACT).
// Variables : VIGIE_URL, VIGIE_PASSWORD, VC_URL, VC_USER, VC_PASS, REDACT (JSON [[regex, remplacement]]),
// BASE_S, DUR_S, TRACE_TARGET, OUT.
import puppeteer from 'puppeteer-core';
import { mkdirSync, writeFileSync } from 'node:fs';

const URL = process.env.VIGIE_URL, OUT = process.env.OUT || '/out';
const REDACT = JSON.parse(process.env.REDACT || '[]');
mkdirSync(OUT, { recursive: true });
const log = (...a) => console.log(new Date().toISOString().slice(11, 19), ...a);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const browser = await puppeteer.launch({ executablePath: '/usr/bin/chromium-browser', headless: true, protocolTimeout: 3600000,
  userDataDir: '/tmp/vigie-docs-chrome',
  args: ['--no-sandbox', '--ignore-certificate-errors', '--disable-dev-shm-usage', '--disable-crash-reporter', '--disable-breakpad', '--lang=en-US'] });
const page = await browser.newPage();
await page.setViewport({ width: 1200, height: 900 });
await page.evaluateOnNewDocument(() => {
  try { localStorage.setItem('vigie-lang', 'en'); localStorage.removeItem('vigie-theme'); } catch (_) { /* */ }
});

// Remplace les identifiants dans le texte, les champs et les attributs title ; les valeurs des champs
// sont rétablies après la capture (elles servent encore au parcours).
const redact = (p) => p.evaluate((rules) => {
  const re = rules.map(([s, r]) => [new RegExp(s, 'g'), r]);
  const fix = (t) => re.reduce((a, [x, r]) => a.replace(x, r), t);
  const w = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  for (let n = w.nextNode(); n; n = w.nextNode()) { const v = fix(n.nodeValue); if (v !== n.nodeValue) n.nodeValue = v; }
  for (const e of document.querySelectorAll('[title]')) e.title = fix(e.title);
  const saved = [];
  for (const e of document.querySelectorAll('input, textarea')) {
    saved.push([e, e.value]);
    if (e.type === 'password') { if (e.value) e.value = 'xxxxxxxxxxxx'; } else e.value = fix(e.value);
  }
  window.__saved = saved;
}, REDACT);
const restore = (p) => p.evaluate(() => { for (const [e, v] of window.__saved || []) e.value = v; });

const shot = async (sel, name, p = page) => {
  await redact(p);
  const el = typeof sel === 'string' ? await p.$(sel) : sel;
  if (!el) throw new Error('élément absent : ' + sel);
  await el.scrollIntoView();
  await sleep(300);
  await el.screenshot({ path: `${OUT}/${name}.png` });
  await restore(p);
  log('capture', name);
};
const text = (sel) => page.$eval(sel, (e) => e.textContent.trim());
const visible = (sel) => page.$eval(sel, (e) => !e.classList.contains('hidden')).catch(() => false);
const tab = async (t) => { await page.click(`button[data-tab="${t}"]`); await sleep(1500); };
const idle = async () => { // aucun job en cours (le planificateur peut en lancer un)
  for (let i = 0; i < 480; i++) { // jusqu'à 40 min (test avancé)
    // erreur réseau passagère : le job est considéré comme en cours
    const busy = await page.evaluate(async () => { const r = await fetch('/api/jobs', { headers: { 'X-Vigie': '1' } }); const j = await r.json(); return (Array.isArray(j) ? j : j.jobs || []).some((x) => x.state === 'running' || x.state === 'queued'); }).catch(() => true);
    if (!busy) return;
    await sleep(5000);
  }
  throw new Error('un job ne se termine pas');
};
const setVal = (sel, v) => page.$eval(sel, (e, x) => { e.value = x; e.dispatchEvent(new Event('input')); e.dispatchEvent(new Event('change')); }, v);

// Connexion à l'appliance
await page.goto(URL, { waitUntil: 'networkidle0' });
await page.waitForSelector('#login-form', { visible: true });
await shot('#login-form', 'login');
await page.type('#login-password', process.env.VIGIE_PASSWORD);
await page.click('#login-form button[type=submit]');
await page.waitForSelector('#app:not(.hidden)', { timeout: 15000 });
await page.evaluate(() => document.querySelector('header.topbar').style.setProperty('position', 'static', 'important')); // CSSOM : permis par la CSP
await sleep(1500);

// 1. Connexion vSphere : déconnexion puis reconnexion par le formulaire (mot de passe mémorisé)
await tab('measure');
if (!(await visible('#connect-form'))) {
  await page.click('#vs-disconnect');
  await page.waitForSelector('#connect-form:not(.hidden)', { timeout: 15000 });
}
await setVal('#vs-url', process.env.VC_URL);
await setVal('#vs-user', process.env.VC_USER);
await setVal('#vs-pass', process.env.VC_PASS);
if (!(await page.$eval('#vs-remember', (e) => e.checked))) await page.click('#vs-remember');
await page.click('#vs-thumb');
await page.waitForSelector('#vs-cert:not(.hidden)', { timeout: 30000 });
await shot('#step-connect', 'ds-connect');
await page.click('#vs-connect');
await page.waitForSelector('#connect-done:not(.hidden)', { timeout: 60000 });
await page.waitForFunction(() => document.querySelector('#self-text').textContent.length > 10, { timeout: 60000 });
await page.waitForFunction(() => document.querySelectorAll('#ds-table tbody tr').length > 0, { timeout: 60000 });
await page.waitForSelector('#path:not(.hidden) .hop', { timeout: 60000 });
await sleep(2000);
await shot('#step-self', 'ds-identify');
await shot('#step-ds', 'ds-datastore');
if (await visible('#disk-none')) { // disque de test à créer (4 Gio)
  await page.select('#disk-size', '4');
  await shot('#step-disk', 'ds-testdisk-create');
  await idle();
  await page.waitForFunction(() => !document.getElementById('disk-create').disabled, { timeout: 60000 });
  await page.click('#disk-create');
  await sleep(3000);
  await idle();
}
await page.waitForFunction(() => !document.getElementById('disk-some').classList.contains('hidden'), { timeout: 120000 });
await sleep(1500);
await shot('#step-disk', 'ds-testdisk');
await page.waitForFunction(() => document.querySelectorAll('#hosts-table tbody tr').length > 0, { timeout: 60000 });
await shot('#step-hosts', 'ds-hosts');

// 2. Type de test
await page.click('input[name=kind][value=standard]'); await sleep(800);
await setVal('#t-base', process.env.BASE_S || '30');
await setVal('#t-dur', process.env.DUR_S || '60');
await sleep(800);
await shot('#step-test', 'ds-test-standard');
await page.click('input[name=kind][value=advanced]'); await sleep(800);
await shot('#step-test', 'ds-test-advanced');
await page.click('input[name=kind][value=monitor]');
await page.waitForFunction(() => document.querySelector('#m-status').textContent.length > 0, { timeout: 10000 });
await sleep(800);
await shot('#step-test', 'ds-test-monitor');
await page.click('input[name=kind][value=standard]'); await sleep(800);
await setVal('#t-base', process.env.BASE_S || '30');
await setVal('#t-dur', process.env.DUR_S || '60');

// 3. Test standard en direct, puis résultat
await idle();
await page.waitForFunction(() => !document.getElementById('t-run').disabled, { timeout: 60000 });
await page.click('#t-run');
await page.waitForSelector('#step-run:not(.hidden)', { timeout: 30000 });
await page.waitForFunction(() => /(\d+) ?%/.test(document.querySelector('#run-pct').textContent) && parseInt(document.querySelector('#run-pct').textContent, 10) >= 45, { timeout: 600000, polling: 1000 });
await shot('#step-run', 'ds-running');
await sleep(5000);
await idle();
await page.waitForSelector('#result .verdict', { timeout: 60000 });
await sleep(2000);
await shot('#step-result', 'ds-result');
await shot('#step-clean', 'ds-cleanup');

// 3b. Test avancé (≈ 12 min), résultat
if (process.env.ADVANCED !== '0') {
  await page.click('input[name=kind][value=advanced]'); await sleep(800);
  await setVal('#t-base', '30'); await setVal('#t-dur', '60');
  await idle();
  await page.waitForFunction(() => !document.getElementById('t-run').disabled, { timeout: 60000 });
  await page.click('#t-run');
  await sleep(10000);
  await idle();
  await page.waitForSelector('#result .verdict', { timeout: 60000 });
  await sleep(2000);
  await shot('#step-result', 'ds-result-advanced');
  await page.click('input[name=kind][value=standard]'); await sleep(500);
}

// 4. Rapport client (nouvel onglet)
const openReport = async (click, name) => {
  const tp = new Promise((res) => browser.once('targetcreated', (t) => res(t)));
  await click();
  const rp = await (await tp).page();
  await rp.setViewport({ width: 1000, height: 1300 });
  await rp.waitForFunction(() => document.documentElement.getAttribute('data-ready') === '1', { timeout: 30000 });
  await sleep(1000);
  await redact(rp);
  await rp.screenshot({ path: `${OUT}/${name}.png` });
  log('capture', name);
  await rp.close();
};

// 5. Historique et tendances
await tab('history');
await page.waitForFunction(() => document.querySelector('#tr-charts').textContent.length > 0, { timeout: 20000 });
await page.select('#tr-period', '7d').catch(() => {});
await sleep(2500);
await shot('#tab-history > article:nth-of-type(1)', 'history-trends');
// liste des jobs : les 12 plus récents seulement (CSSOM, la CSP interdit les styles en ligne)
await page.evaluate(() => [...document.querySelectorAll('#hist-table tbody tr')].slice(12).forEach((tr) => tr.style.setProperty('display', 'none')));
await shot('#tab-history > article:nth-of-type(2)', 'history-jobs');
await page.evaluate(() => [...document.querySelectorAll('#hist-table tbody tr')].forEach((tr) => tr.style.removeProperty('display')));
await openReport(() => page.click('#hist-table a.btn'), 'report');
const cbs = await page.$$('#hist-table input[type=checkbox]');
if (cbs.length >= 2) {
  await cbs[0].click(); await cbs[1].click();
  await openReport(() => page.click('#cmp-open'), 'report-compare');
  await cbs[0].click(); await cbs[1].click();
}
// 6. Réglages
await tab('settings');
await shot('#tab-settings > article:nth-of-type(1)', 'settings-tokens');
await shot('#tab-settings > article:nth-of-type(2)', 'settings-branding');
await page.select('#th-preset', 'all-flash').catch(() => {});
await sleep(800);
await shot('#tab-settings > article:nth-of-type(3)', 'settings-thresholds');

// 7. Réseau
await tab('network');
await page.waitForFunction(() => document.querySelectorAll('#nl-table tbody tr').length > 0, { timeout: 15000 });
await shot('#tab-network > article:nth-of-type(1)', 'net-interfaces');
const gw = (await text('#nl-extra')).match(/(?:Gateway|Passerelle) (\S+)/);
await page.$eval('#nt-target', (e) => { e.value = ''; });
await page.type('#nt-target', gw ? gw[1] : '192.168.0.1');
await page.select('#nt-count', '20'); await page.select('#nt-interval', '200');
await page.click('#nt-ping');
await page.waitForFunction(() => !document.querySelector('#nt-ping').disabled && /---/.test(document.querySelector('#nt-result').textContent), { timeout: 60000 });
await sleep(800);
await shot('#tab-network > article:nth-of-type(2)', 'net-ping');
if (process.env.TRACE_TARGET) {
  await page.$eval('#nt-target', (e) => { e.value = ''; });
  await page.type('#nt-target', process.env.TRACE_TARGET);
  await page.click('#nt-trace');
  await page.waitForFunction(() => !document.querySelector('#nt-trace').disabled && document.querySelector('#nt-trace-result').textContent.length > 20, { timeout: 120000 });
  await sleep(1000);
  await shot('#tab-network > article:nth-of-type(2)', 'net-traceroute');
}
if (await page.$('#nm-table tbody tr')) {
  await page.click('#nm-table tbody tr');
  await page.waitForSelector('#nm-graph:not(.hidden)', { timeout: 15000 });
  await sleep(2500);
}
await shot('#tab-network > article:nth-of-type(3)', 'net-monitor');
await page.click('#arp-refresh').catch(() => {});
await sleep(1500);
await shot('#tab-network > article:nth-of-type(4)', 'net-arp');
await shot('#tab-network > article:nth-of-type(5)', 'net-iperf');

// 8. À propos, thème sombre
await tab('about');
await shot('#tab-about > article', 'about');
await tab('measure');
await page.click('#theme'); await sleep(800);
await shot('#step-result', 'ds-result-dark');
await page.click('#theme'); await sleep(300);

// texte affiché resté en français (onglets parcourus une dernière fois) + textes absents du catalogue
const frWord = /\b(le|la|les|des|du|une?|et|ou|pour|avec|sans|dans|sur|par|est|sont|aucun|aucune)\b|[éèêàùçô]/i;
const shown = new Set();
for (const t of ['network', 'measure', 'history', 'settings', 'about']) {
  await tab(t);
  for (const line of await page.evaluate(() => document.querySelector('.tabpane:not(.hidden)').innerText.split('\n'))) if (frWord.test(line)) shown.add(line.trim());
}
const missing = [...new Set([...await page.evaluate(() => [...(window.__i18nMissing || [])]), ...shown])].sort();
writeFileSync(`${OUT}/i18n-missing.json`, JSON.stringify(missing, null, 1));
log('textes non traduits :', missing.length);
await browser.close();
log('terminé');

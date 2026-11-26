// Test d'interface pour la CI : l'appliance démarrée sous QEMU, sans vCenter. Échoue (code 1) sur
// erreur JS, violation CSP, requête hors de l'appliance ou parcours cassé.
// Variables : VIGIE_URL (https://127.0.0.1:8443/), VIGIE_PASSWORD, GATEWAY (cible de ping), OUT.
import puppeteer from 'puppeteer-core';
import { mkdirSync, writeFileSync } from 'node:fs';

const URL = process.env.VIGIE_URL, OUT = process.env.OUT || '/out', GW = process.env.GATEWAY || '10.0.2.2';
mkdirSync(OUT, { recursive: true });
const origin = new globalThis.URL(URL).origin;
const problems = [];
const log = (...a) => console.log(new Date().toISOString().slice(11, 19), ...a);
const step = async (name, f) => {
  try { await f(); log('OK  ', name); } catch (e) { problems.push(name + ' : ' + e.message.split('\n')[0]); log('ÉCHEC', name, e.message.split('\n')[0]); await shot('echec-' + name.replace(/\W+/g, '-')); }
};
const browser = await puppeteer.launch({ executablePath: '/usr/bin/chromium-browser', headless: true, protocolTimeout: 300000,
  userDataDir: '/tmp/vigie-chrome',
  args: ['--no-sandbox', '--ignore-certificate-errors', '--disable-dev-shm-usage', '--disable-crash-reporter', '--disable-breakpad', '--lang=fr-FR'] });
const page = await browser.newPage();
await page.setViewport({ width: 1280, height: 900 });
page.on('console', (m) => { if (m.type() === 'error' && !/401|409|Failed to load resource/.test(m.text())) problems.push('console : ' + m.text()); });
page.on('pageerror', (e) => problems.push('erreur JS : ' + e.message));
page.on('request', (r) => { const u = r.url(); if (!u.startsWith(origin) && !u.startsWith('data:') && !u.startsWith('blob:')) problems.push('requête externe : ' + u); });
await page.evaluateOnNewDocument(() => {
  document.addEventListener('securitypolicyviolation', (e) => console.error('CSP : ' + e.violatedDirective + ' ' + e.blockedURI));
  try { localStorage.setItem('vigie-lang', 'fr'); } catch (_) { /* */ }
});
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const shot = (n) => page.screenshot({ path: `${OUT}/${n}.png`, fullPage: true }).catch(() => {});
const text = (sel) => page.$eval(sel, (e) => e.textContent.trim());
const tab = async (t) => { await page.click(`button[data-tab="${t}"]`); await sleep(1200); };

await step('en-têtes de sécurité', async () => {
  const r = await page.goto(URL, { waitUntil: 'networkidle0' });
  const h = r.headers();
  if (!/default-src 'self'/.test(h['content-security-policy'] || '')) throw new Error('CSP absente');
  if (!h['strict-transport-security']) throw new Error('HSTS absent');
});
await step('mot de passe refusé', async () => {
  await page.waitForSelector('#login-form', { visible: true });
  await page.type('#login-password', 'mauvais-mot-de-passe');
  await page.click('#login-form button[type=submit]');
  await page.waitForFunction(() => document.querySelector('#login-error').textContent.length > 0, { timeout: 10000 });
});
await step('connexion', async () => {
  await page.$eval('#login-password', (e) => { e.value = ''; });
  await page.type('#login-password', process.env.VIGIE_PASSWORD);
  await page.click('#login-form button[type=submit]');
  await page.waitForSelector('#app:not(.hidden)', { timeout: 15000 });
  await sleep(1500); await shot('01-network');
});
await step('ping', async () => {
  await page.type('#nt-target', GW);
  await page.select('#nt-count', '10'); await page.select('#nt-interval', '200');
  await page.click('#nt-ping');
  await page.waitForFunction(() => !document.querySelector('#nt-ping').disabled && document.querySelector('#nt-result').textContent.length > 0, { timeout: 30000 });
  log('     ping :', (await text('#nt-result')).replace(/\s+/g, ' ').slice(0, 120)); // l'ICMP de QEMU user dépend de l'hôte
});
await step('onglet mesure sans vCenter', async () => {
  await tab('measure');
  if (!/vSphere/.test(await text('#t-why'))) throw new Error('raison du bouton absente : ' + await text('#t-why'));
  await page.click('input[name=kind][value=monitor]');
  await page.waitForFunction(() => document.querySelector('#m-status').textContent.length > 0, { timeout: 10000 });
  await shot('02-measure');
  await page.click('input[name=kind][value=standard]');
});
await step('historique et tendances', async () => {
  await tab('history');
  await page.waitForFunction(() => document.querySelector('#tr-charts').textContent.length > 0, { timeout: 10000 });
  await shot('03-history');
});
await step('jeton d\'API', async () => {
  await tab('settings');
  await page.type('#tok-name', 'ci');
  await page.click('#tok-create');
  await page.waitForSelector('#tok-secret:not(.hidden) code', { timeout: 10000 });
  const tok = await text('#tok-secret code');
  if (!/^vg_/.test(tok)) throw new Error('jeton inattendu');
  const st = await page.evaluate(async (t) => (await fetch('/api/health', { headers: { Authorization: 'Bearer ' + t } })).status, tok);
  if (st !== 200) throw new Error('jeton refusé : ' + st);
});
await step('seuils modifiables', async () => {
  await page.select('#th-preset', 'hybride'); await sleep(500);
  await page.$eval('#th-edit input[data-k="write_mbps_warn"]', (e) => { e.value = '77'; });
  await page.click('#th-save');
  await page.waitForFunction(() => /\S/.test(document.querySelector('#th-msg').textContent), { timeout: 10000 });
  const p = await page.evaluate(async () => (await (await fetch('/api/thresholds', { headers: { 'X-Vigie': '1' } })).json()).presets.hybride.write_mbps_warn);
  if (p !== 77) throw new Error('seuil non enregistré : ' + p);
  page.once('dialog', (d) => d.accept());
  await page.click('#th-reset'); await sleep(1500);
  await shot('04-settings');
});
await step('à propos', async () => { await tab('about'); if (!/SHA-256/.test(await text('#about-text'))) throw new Error('empreinte absente'); });
const missing = {};
for (const l of ['en', 'de', 'pl']) {
  await step('langue ' + l, async () => {
    await page.evaluate((x) => localStorage.setItem('vigie-lang', x), l);
    await page.reload({ waitUntil: 'networkidle0' }); await sleep(1500);
    for (const t of ['network', 'measure', 'history', 'settings', 'about']) await tab(t);
    missing[l] = await page.evaluate(() => [...(window.__i18nMissing || [])].sort());
    await shot('05-' + l);
  });
}
writeFileSync(`${OUT}/i18n-missing.json`, JSON.stringify(missing, null, 1));
// texte resté en français dans une autre langue : échec (même heuristique que la console)
const frWord = /\b(le|la|les|des|du|une?|et|ou|pour|avec|sans|dans|sur|par|est|sont|aucun|aucune)\b|[éèêàùçô]/i;
for (const [l, list] of Object.entries(missing)) {
  const fr = list.filter((s) => frWord.test(s));
  if (fr.length) problems.push(`langue ${l} : ${fr.length} texte(s) non traduit(s) : ` + fr.slice(0, 5).join(' | '));
}
await browser.close();
log(problems.length ? `${problems.length} problème(s) :\n  ` + problems.join('\n  ') : 'aucun problème');
process.exit(problems.length ? 1 : 0);

// Parcours de l'interface web dans une langue (LANG_UI, « de » par défaut) : relève les textes
// affichés sans traduction (window.__i18nMissing) et capture quelques écrans.
// Variables : VIGIE_URL, VIGIE_PASSWORD, VC_URL, VC_USER, VC_PASS, LANG_UI, OUT.
import puppeteer from 'puppeteer-core';
import { mkdirSync, writeFileSync } from 'node:fs';

const URL = process.env.VIGIE_URL, OUT = process.env.OUT || '/out', LANG = process.env.LANG_UI || 'de';
mkdirSync(OUT, { recursive: true });
const log = (...a) => console.log(new Date().toISOString().slice(11, 19), ...a);
const browser = await puppeteer.launch({ executablePath: '/usr/bin/chromium-browser', headless: true, protocolTimeout: 600000,
  args: ['--no-sandbox', '--ignore-certificate-errors', '--disable-dev-shm-usage'] });
const page = await browser.newPage();
await page.setViewport({ width: 1280, height: 900 });
const errors = [];
page.on('pageerror', (e) => errors.push(e.message));
await page.evaluateOnNewDocument((l) => { try { localStorage.setItem('vigie-lang', l); } catch (_) { /* */ } }, LANG);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const tryDo = async (what, f) => { try { await f(); } catch (e) { log('(ignoré)', what, e.message.split('\n')[0]); } };
const shot = (n) => page.screenshot({ path: `${OUT}/${LANG}-${n}.png`, fullPage: true });

await page.goto(URL, { waitUntil: 'networkidle0' });
await page.waitForSelector('#login-form', { visible: true });
await shot('01-login');
await page.type('#login-password', 'mauvais');
await page.click('#login-form button[type=submit]');
await sleep(1500);
await page.$eval('#login-password', (e) => { e.value = ''; });
await page.type('#login-password', process.env.VIGIE_PASSWORD);
await page.click('#login-form button[type=submit]');
await page.waitForSelector('#app:not(.hidden)', { timeout: 15000 });
await sleep(2000);
await shot('02-network');
// Network : ping, traceroute, surveillance
await tryDo('ping', async () => {
  const gw = (await page.$eval('#nl-extra', (e) => e.textContent)).match(/(\d+\.\d+\.\d+\.\d+)/);
  await page.type('#nt-target', gw ? gw[1] : '127.0.0.1');
  await page.select('#nt-count', '10'); await page.select('#nt-interval', '200');
  await page.click('#nt-ping'); await sleep(6000);
  await page.$eval('#nt-target', (e) => { e.value = '172.16.0.10'; });
  await page.click('#nt-ping'); await sleep(15000);
  await shot('03-ping');
});
await tryDo('surveillance', async () => {
  await page.click('#nt-watch'); await sleep(4000);
  const row = await page.$('#nm-table tbody tr'); if (row) { await row.click(); await sleep(3000); }
});
await tryDo('arp', async () => { await page.click('#arp-refresh'); await sleep(1500); });
// Datastore performance : connexion vSphere
await page.click('button[data-tab="measure"]'); await sleep(1500);
await tryDo('vsphere', async () => {
  if (await page.$eval('#connect-form', (e) => !e.classList.contains('hidden'))) {
    await page.$eval('#vs-url', (e) => { e.value = ''; }); await page.type('#vs-url', process.env.VC_URL);
    await page.$eval('#vs-user', (e) => { e.value = ''; }); await page.type('#vs-user', process.env.VC_USER);
    await page.type('#vs-pass', process.env.VC_PASS);
    await page.click('#vs-thumb');
    await page.waitForSelector('#vs-cert:not(.hidden)', { timeout: 30000 });
    await shot('04-empreinte');
    await page.click('#vs-connect');
  }
  await page.waitForFunction(() => document.querySelectorAll('#ds-table tbody tr').length > 0, { timeout: 60000 });
  await page.waitForSelector('#path:not(.hidden) .hop', { timeout: 60000 });
  await sleep(3000);
});
await tryDo('hôtes', async () => { await page.waitForFunction(() => document.querySelectorAll('#hosts-table tbody tr').length > 0, { timeout: 60000 }); await sleep(2000); });
await shot('05-measure');
await tryDo('surveillance continue', async () => {
  await page.click('input[name=kind][value=monitor]');
  await page.waitForFunction(() => document.querySelector('#m-status').textContent.length > 0, { timeout: 15000 });
  await page.select('#m-profile', 'local'); await sleep(500); await page.select('#m-profile', 'standard');
  const s = await page.$('#step-test'); await s.screenshot({ path: `${OUT}/${LANG}-05b-monitor.png` });
  await page.click('input[name=kind][value=standard]');
});
// Historique : chaque job
await page.click('button[data-tab="history"]'); await sleep(2500);
const n = await page.$$eval('#hist-table tbody tr', (r) => r.length);
for (let i = 0; i < Math.min(n, 12); i++) {
  await tryDo('historique ' + i, async () => {
    const rows = await page.$$('#hist-table tbody tr');
    await rows[i].click(); await sleep(2500);
  });
}
await tryDo('tendances', async () => {
  await page.waitForFunction(() => document.querySelectorAll('#tr-charts canvas').length > 0 || /\S/.test(document.querySelector('#tr-charts').textContent), { timeout: 15000 });
  await page.select('#tr-period', '24h'); await sleep(1500);
  await page.select('#tr-sched', '1'); await sleep(1500); await page.select('#tr-sched', '');  await sleep(1500);
});
await shot('06-history');
for (const t of ['settings', 'about']) {
  await page.click(`button[data-tab="${t}"]`); await sleep(2000);
  if (t === 'settings') await tryDo('seuils', async () => { await page.select('#th-preset', 'hybride'); await sleep(800); });
  await shot('07-' + t);
}
await page.click('button[data-tab="network"]'); await sleep(1500);
const missing = await page.evaluate(() => [...(window.__i18nMissing || [])].sort());
writeFileSync(`${OUT}/missing-${LANG}.txt`, missing.join('\n') + '\n');
log(`${missing.length} texte(s) sans traduction en ${LANG} ; erreurs JS : ${errors.length}`, errors.join(' | '));
await browser.close();

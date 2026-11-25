// QA de l'interface web vigie : parcours complet dans un Chromium headless.
// Variables : VIGIE_URL, VIGIE_PASSWORD, VC_URL, VC_USER, VC_PASS, FLOW (full|ui), BASE_S, DUR_S, OUT.
import puppeteer from 'puppeteer-core';
import { mkdirSync } from 'node:fs';

const URL = process.env.VIGIE_URL;
const OUT = process.env.OUT || '/out';
const FLOW = process.env.FLOW || 'full';
mkdirSync(OUT, { recursive: true });
const origin = new globalThis.URL(URL).origin;
const problems = [];
const log = (...a) => console.log(new Date().toISOString().slice(11, 19), ...a);

const browser = await puppeteer.launch({ executablePath: '/usr/bin/chromium-browser', headless: true,
  protocolTimeout: 3600000, args: ['--no-sandbox', '--ignore-certificate-errors', '--disable-dev-shm-usage', '--lang=fr-FR'] });
const page = await browser.newPage();
await page.setViewport({ width: 1280, height: 900 });
page.on('console', (m) => { if (m.type() === 'error') problems.push('console: ' + m.text()); });
page.on('pageerror', (e) => problems.push('pageerror: ' + e.message));
page.on('request', (r) => { const u = r.url(); if (!u.startsWith(origin) && !u.startsWith('data:')) problems.push('requête externe: ' + u); });
await page.evaluateOnNewDocument(() => {
  document.addEventListener('securitypolicyviolation', (e) => console.error('CSP: ' + e.violatedDirective + ' ' + e.blockedURI));
});
const shot = async (name, full = true) => { await page.screenshot({ path: `${OUT}/${name}.png`, fullPage: full }); log('capture', name); };
const text = (sel) => page.$eval(sel, (e) => e.textContent.trim());
const waitText = (sel, re, timeout = 60000) => page.waitForFunction((s, r) => { const e = document.querySelector(s); return e && new RegExp(r).test(e.textContent); }, { timeout, polling: 500 }, sel, re);

// 1. HTTP -> HTTPS
const httpResp = await fetch(URL.replace('https://', 'http://'), { redirect: 'manual' });
log('http://', httpResp.status, httpResp.headers.get('location'));
if (httpResp.status !== 301) problems.push('pas de redirection HTTP -> HTTPS');

// 2. écran de connexion
const resp = await page.goto(URL, { waitUntil: 'networkidle0' });
const csp = resp.headers()['content-security-policy'] || '';
log('CSP', csp);
await page.waitForSelector('#login-form', { visible: true });
await shot('01-login', false);
await page.type('#login-password', 'mauvais-mot-de-passe');
await page.click('#login-form button[type=submit]');
await waitText('#login-error', '.');
log('mauvais mot de passe :', await text('#login-error'));
await page.$eval('#login-password', (e) => { e.value = ''; });
await page.type('#login-password', process.env.VIGIE_PASSWORD);
await page.click('#login-form button[type=submit]');
await page.waitForSelector('#app:not(.hidden)', { timeout: 15000 });
log('connecté à l\'appliance ; onglet par défaut :', await text('.tab.active'));
await shot('01b-accueil', false);
await page.click('button[data-tab="measure"]');

// 3. connexion vSphere par l'UI
if (await page.$eval('#connect-form', (e) => !e.classList.contains('hidden'))) {
  await page.$eval('#vs-url', (e) => { e.value = ''; });
  await page.type('#vs-url', process.env.VC_URL);
  await page.$eval('#vs-user', (e) => { e.value = ''; });
  await page.type('#vs-user', process.env.VC_USER);
  await page.type('#vs-pass', process.env.VC_PASS);
  await page.click('#vs-thumb');
  await page.waitForSelector('#vs-cert:not(.hidden)', { timeout: 30000 });
  log('empreinte affichée :', await text('#cert-sha'), '—', await text('#cert-trust'));
  await shot('02-empreinte', false);
  await page.click('#vs-connect');
}
await waitText('#vs-chip', 'vCenter|ESX', 60000);
await waitText('#self-text', 'Je suis la VM', 60000);
log(await text('#self-text'));
await page.waitForFunction(() => document.querySelectorAll('#ds-table tbody tr').length > 0, { timeout: 60000 });
await page.waitForSelector('#path:not(.hidden) .hop', { timeout: 60000 });
log('chemin :', (await text('#path')).replace(/\s+/g, ' '));
await page.waitForFunction(() => document.getElementById('b-ds').textContent !== '', { timeout: 30000 });
log('raison du bouton « Lancer » :', await text('#t-why'));
await shot('03-connecte');

if (FLOW === 'full') {
  // 4. disque de test
  if (await page.$eval('#disk-some', (e) => e.classList.contains('hidden'))) {
    await page.select('#disk-size', '4');
    await page.waitForFunction(() => !document.getElementById('disk-create').disabled, { timeout: 60000 });
    await page.click('#disk-create');
    log('création du disque de test…');
    await waitText('#b-run', 'terminé|failed|cancelled', 300000);
    log('job disque :', await text('#b-run'));
  }
  await waitText('#b-disk', 'prêt', 60000);
  log('disque :', await text('#disk-text'));
  // 5. test standard depuis l'UI
  await page.$eval('#t-base', (e, v) => { e.value = v; e.dispatchEvent(new Event('input')); }, process.env.BASE_S || '60');
  await page.$eval('#t-dur', (e, v) => { e.value = v; e.dispatchEvent(new Event('input')); }, process.env.DUR_S || '80');
  log('profil affiché :', await text('#t-profile'));
  log('estimation :', await text('#t-estimate'));
  await page.waitForFunction(() => !document.getElementById('t-run').disabled, { timeout: 30000 });
  await page.click('#t-run');
  await waitText('#run-step', 'écriture continue', 600000);
  await new Promise((r) => setTimeout(r, 20000));
  await page.evaluate(() => document.getElementById('step-run').scrollIntoView());
  await shot('04-execution', false);
  log('pendant le test :', await text('#run-pct'), await text('#run-step'));
  await waitText('#b-run', 'terminé|failed|cancelled', 1200000);
  log('fin du job :', await text('#b-run'));
  await page.waitForSelector('#result .verdict', { timeout: 30000 });
  log('verdict :', (await text('#result .verdict')).replace(/\s+/g, ' '));
  log('tuiles :', (await page.$$eval('#result .tile', (t) => t.map((x) => x.textContent.trim().replace(/\s+/g, ' ')))).join(' | '));
  log('profils :', (await page.$$eval('#result .profile', (t) => t.map((x) => x.textContent.trim()))).join(' || '));
  await page.evaluate(() => document.getElementById('step-result').scrollIntoView());
  await shot('05-resultat');
}

// 6. onglets
for (const [tab, name] of [['history', '06-historique'], ['settings', '07-reglages'], ['about', '08-apropos']]) {
  await page.click(`button[data-tab="${tab}"]`);
  await new Promise((r) => setTimeout(r, 1500));
  await shot(name);
}
await page.click('button[data-tab="history"]');
await new Promise((r) => setTimeout(r, 1500));
if (await page.$('#hist-table tbody tr')) {
  await page.click('#hist-table tbody tr');
  await page.waitForSelector('#hist-detail:not(.hidden)', { timeout: 15000 });
  await new Promise((r) => setTimeout(r, 1000));
  await shot('09-historique-detail');
} else log('historique vide : détail non capturé');

// 6a. rapport client (phase 8c) : résumés de l'historique, ouverture d'un rapport, avant/après
const rows = await page.$$eval('#hist-table tbody tr', (trs) => trs.map((tr) => [...tr.cells].map((c) => c.textContent.trim().replace(/\s+/g, ' '))));
log('historique :', rows.slice(0, 4).map((r) => r.slice(1, 7).join(' | ')).join(' // '));
const openReport = async (click) => {
  const tp = new Promise((res) => browser.once('targetcreated', (t) => res(t)));
  await click();
  const rp = await (await tp).page();
  const rprob = [];
  rp.on('console', (m) => { if (m.type() === 'error') rprob.push(m.text()); });
  await rp.waitForFunction(() => document.documentElement.getAttribute('data-ready') === '1', { timeout: 20000 });
  const info = await rp.evaluate(() => ({ title: document.title, sections: document.querySelectorAll('section.page').length, canvases: document.querySelectorAll('[data-chart] canvas').length, client: document.querySelector('.client')?.textContent }));
  await rp.screenshot({ path: `${OUT}/${info.sections}-rapport-${Date.now() % 1000}.png` });
  await rp.close();
  if (rprob.length) problems.push('rapport : ' + rprob.join(' ; '));
  return info;
};
if (await page.$('#hist-table a.btn')) {
  const r1 = await openReport(() => page.click('#hist-table a.btn'));
  log('rapport ouvert depuis l\'historique :', JSON.stringify(r1));
  const cbs = await page.$$('#hist-table input[type=checkbox]');
  if (cbs.length >= 2) {
    await cbs[0].click(); await cbs[1].click();
    log('bouton avant/après :', await page.$eval('#cmp-open', (b) => (b.disabled ? 'désactivé' : 'actif')), '—', await text('#cmp-hint'));
    const r2 = await openReport(() => page.click('#cmp-open'));
    log('rapport avant/après :', JSON.stringify(r2));
  } else problems.push('moins de deux tests cochables pour l\'avant/après');
} else problems.push('aucun lien de rapport dans l\'historique');
await page.click('button[data-tab="settings"]');
await page.waitForFunction(() => document.getElementById('br-client').value !== '', { timeout: 10000 }).catch(() => problems.push('personnalisation non chargée'));
log('personnalisation :', await page.$eval('#br-client', (e) => e.value), '/', await page.$eval('#br-site', (e) => e.value), '— logo affiché :', await page.$eval('#br-logo', (e) => !e.classList.contains('hidden') && e.naturalWidth > 0));
await page.$eval('#br-site', (e) => { e.value = ''; });
await page.type('#br-site', 'PCC DC999 (UI)');
await page.click('#br-save');
await waitText('#br-msg', 'Enregistré', 10000);
log('personnalisation enregistrée depuis l\'interface :', await text('#br-msg'));
await page.evaluate(() => document.getElementById('br-client').scrollIntoView());
await shot('09b-personnalisation', false);

// 6b. onglet Network : ping de la passerelle
await page.click('button[data-tab="network"]');
await page.waitForFunction(() => document.querySelectorAll('#nl-table tbody tr').length > 0, { timeout: 15000 });
const gw = (await text('#nl-extra')).match(/Passerelle (\S+)/);
if (gw && gw[1] !== '—') {
  await page.type('#nt-target', gw[1]);
  await page.select('#nt-count', '10');
  await page.select('#nt-interval', '200');
  await page.click('#nt-ping');
  await waitText('#nt-result .log', 'seq=3', 15000);
  log('ping en temps réel (après 3 réponses) :', (await text('#nt-result .tiles')).replace(/\s+/g, ' '));
  await waitText('#nt-result .log', '---', 30000);
  log('ping passerelle terminé :', (await text('#nt-result .tiles')).replace(/\s+/g, ' '));
}
if (process.env.NET_TARGET) {
  await page.$eval('#nt-target', (e) => { e.value = ''; });
  await page.type('#nt-target', process.env.NET_TARGET);
  await page.click('#nt-ping');
  await waitText('#nt-trace-result', 'atteinte', 180000).catch(() => {});
  log('traceroute :', (await text('#nt-trace-result')).replace(/\s+/g, ' ').slice(0, 300));
}
if (await page.$('#nm-table tbody tr')) {
  await page.click('#nm-table tbody tr');
  await page.waitForSelector('#nm-graph:not(.hidden)', { timeout: 15000 });
  await new Promise((r) => setTimeout(r, 1500));
}
await shot('12-network');

// 7. thème sombre
await page.click('button[data-tab="measure"]');
await page.click('#theme');
await new Promise((r) => setTimeout(r, 800));
await shot('10-sombre');

// 8. responsive (téléphone)
await page.setViewport({ width: 390, height: 844, isMobile: true });
await new Promise((r) => setTimeout(r, 800));
const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
log('débordement horizontal à 390 px :', overflow, 'px');
if (overflow > 0) problems.push('débordement horizontal ' + overflow + ' px sur mobile');
await shot('11-mobile', false);

await browser.close();
log('PROBLÈMES :', problems.length ? '\n  ' + problems.join('\n  ') : 'aucun');
process.exit(problems.length ? 1 : 0);

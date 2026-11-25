// QA du rapport client (phase 8c) : chaque fichier HTML de /in est ouvert hors ligne (file://, toute
// requête réseau bloquée et signalée), les graphs doivent être tracés sans erreur ni violation CSP,
// puis le rapport est imprimé en PDF A4 : une section = une page, sinon un graph ou un tableau déborde.
import puppeteer from 'puppeteer-core';
import { readdirSync, readFileSync, mkdirSync } from 'node:fs';

const IN = process.env.IN || '/in', OUT = process.env.OUT || '/out';
mkdirSync(OUT, { recursive: true });
const browser = await puppeteer.launch({ executablePath: '/usr/bin/chromium-browser', headless: true,
  args: ['--no-sandbox', '--disable-dev-shm-usage', '--lang=fr-FR'] });
let failed = 0;
for (const f of readdirSync(IN).filter((x) => x.endsWith('.html')).sort()) {
  const problems = [];
  const page = await browser.newPage();
  await page.setViewport({ width: 1100, height: 900 });
  await page.setRequestInterception(true);
  page.on('request', (r) => { const u = r.url(); if (u.startsWith('file:') || u.startsWith('data:')) r.continue(); else { problems.push('requête réseau : ' + u); r.abort(); } });
  page.on('console', (m) => { if (m.type() === 'error' || m.type() === 'warning') problems.push('console : ' + m.text()); });
  page.on('pageerror', (e) => problems.push('erreur JS : ' + e.message));
  await page.evaluateOnNewDocument(() => document.addEventListener('securitypolicyviolation', (e) => console.error('CSP ' + e.violatedDirective + ' ' + e.blockedURI)));
  await page.goto('file://' + IN + '/' + f, { waitUntil: 'load' });
  await page.waitForFunction(() => document.documentElement.getAttribute('data-ready') === '1', { timeout: 10000 }).catch(() => problems.push('script du rapport non exécuté'));
  const info = await page.evaluate(() => ({
    sections: document.querySelectorAll('section.page').length,
    charts: document.querySelectorAll('[data-chart]').length,
    drawn: document.querySelectorAll('[data-chart] canvas').length,
    svgs: document.querySelectorAll('.svgchart svg').length,
    unavailable: [...document.querySelectorAll('[data-chart]')].filter((e) => /indisponible/.test(e.textContent)).length,
    overflowX: document.documentElement.scrollWidth - document.documentElement.clientWidth,
  }));
  if (info.drawn < info.charts || info.unavailable) problems.push(`graphs tracés : ${info.drawn}/${info.charts}`);
  if (info.overflowX > 0) problems.push('débordement horizontal ' + info.overflowX + ' px');
  const base = f.replace(/\.html$/, '');
  await page.screenshot({ path: `${OUT}/${base}.png`, fullPage: true });
  await page.emulateMediaType('print');
  // zone imprimable A4 : 210 × 297 mm moins les marges @page (14 / 12 mm) = 182 × 273 mm à 96 dpi
  await page.setViewport({ width: 688, height: 1032 });
  const tall = await page.evaluate(() => [...document.querySelectorAll('section.page')].map((s) => [s.id, Math.round(s.getBoundingClientRect().height)]).filter((x) => x[1] > 1031));
  for (const [id, h] of tall) problems.push(`section « ${id} » : ${h} px pour 1031 px imprimables`);
  const pdf = await page.pdf({ path: `${OUT}/${base}.pdf`, format: 'A4', preferCSSPageSize: true, printBackground: true });
  const pages = (Buffer.from(pdf).toString('latin1').match(/\/Type\s*\/Page[^s]/g) || []).length;
  if (pages !== info.sections) problems.push(`PDF de ${pages} pages pour ${info.sections} sections : une section déborde de sa page`);
  // mobile
  await page.emulateMediaType('screen');
  await page.setViewport({ width: 390, height: 800 });
  const ox = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  if (ox > 0) problems.push('débordement horizontal à 390 px : ' + ox + ' px');
  console.log(`${f} : ${info.sections} sections, ${info.drawn}/${info.charts} graphs uPlot, ${info.svgs} SVG, PDF ${pages} pages A4, ${readFileSync(IN + '/' + f).length} octets` + (problems.length ? '\n  PROBLÈMES :\n  - ' + problems.join('\n  - ') : ' — OK'));
  if (problems.length) failed++;
  await page.close();
}
await browser.close();
process.exit(failed ? 1 : 0);

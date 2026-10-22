/**
 * Screenshots for the README.
 *
 * Run against the dev forum, which carries the full rig — a bare-metal server,
 * a Docker one, backups, off-site copies, a linked identity and an install
 * template:
 *
 *   GARRISON_TOKEN=… node tools/shots.mjs
 *
 * 🚨 Signed in with a remember cookie rather than a typed password: the token
 * is minted on the server, used, and deleted there. Nothing in this file, and
 * nothing in the shell history, is a credential.
 *
 * 🚨 Every shot WAITS for the thing it is about to photograph. A screenshot
 * taken before the poll lands shows a spinner, and a README of spinners is
 * worse than a README with no pictures — it says the product is slow.
 */
import puppeteer from '../../gridiron-nation/node_modules/puppeteer-core/lib/puppeteer/puppeteer-core.js';
import { mkdir } from 'node:fs/promises';
import { dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

/*
 * 🚨 Not a hard-coded path. This is run from whichever machine is to hand, and
 * a path that exists on only one of them turns "regenerate the README shots"
 * into "first port the script" — which is how a README ends up showing a
 * version of the UI that shipped three changes ago.
 */
const CHROME =
  process.env.CHROME ||
  {
    darwin: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    win32: 'C:/Program Files/Google/Chrome/Application/chrome.exe',
  }[process.platform] ||
  '/usr/bin/google-chrome';

const BASE = process.env.GARRISON_BASE || 'https://dev.ernestdefoe.online';
const TOKEN = process.env.GARRISON_TOKEN || '';
const OUT = `${dirname(fileURLToPath(import.meta.url))}/../screenshots`;

await mkdir(OUT, { recursive: true });

const browser = await puppeteer.launch({
  executablePath: CHROME,
  headless: 'new',
  defaultViewport: { width: 1280, height: 1200, deviceScaleFactor: 2 },
  args: ['--hide-scrollbars', '--force-color-profile=srgb'],
});

const page = await browser.newPage();

/*
 * 🚨 The colour scheme is PINNED, not inherited from whichever machine is
 * running this. Headless Chrome reports the host OS setting, so the same
 * script produced dark shots on one box and light ones on another — and
 * "regenerate the screenshots after a CSS fix" silently reskinned the README.
 */
await page.emulateMediaFeatures([
  { name: 'prefers-color-scheme', value: process.env.SHOTS_THEME || 'dark' },
]);

if (TOKEN) {
  await page.setCookie({
    name: 'flarum_remember',
    value: TOKEN,
    domain: new URL(BASE).hostname,
    path: '/',
    httpOnly: true,
    secure: true,
  });
}

const settle = (ms) => new Promise((r) => setTimeout(r, ms));

/**
 * Photograph one element, cropped to itself with a little air.
 *
 * 🚨 Cropped to the ELEMENT rather than the viewport. A full-page shot of a
 * forum is ninety per cent somebody else's theme, and the reader has to hunt
 * for the part the caption is talking about.
 */
async function shoot(name, { path, selector, waitFor, before, pad = 16, maxHeight = 1600 }) {
  await page.goto(`${BASE}${path}`, { waitUntil: 'networkidle2' });
  await page.waitForSelector(waitFor || selector, { timeout: 30000 });

  if (before) await before(page);

  await settle(1200);

  const box = await page.evaluate(
    (sel, pad) => {
      const el = document.querySelector(sel);

      if (!el) return null;

      const r = el.getBoundingClientRect();

      return {
        x: Math.max(0, r.left + window.scrollX - pad),
        y: Math.max(0, r.top + window.scrollY - pad),
        width: r.width + pad * 2,
        height: r.height + pad * 2,
      };
    },
    selector,
    pad
  );

  if (!box) throw new Error(`${name}: ${selector} was never found`);

  await page.setViewport({
    width: 1280,
    height: Math.min(maxHeight, Math.max(600, Math.ceil(box.y + box.height + 40))),
    deviceScaleFactor: 2,
  });

  await settle(400);

  await page.screenshot({ path: `${OUT}/${name}.png`, clip: box });

  console.log(`  ${name}.png  ${Math.round(box.width)}×${Math.round(box.height)}`);
}

console.log('shooting against', BASE);

await shoot('status-page', {
  path: '/garrison',
  selector: '.GarrisonCards',
  waitFor: '.GarrisonCard',
});

/*
 * 🚨 The RESULTS column, not the header.
 *
 * `.GarrisonServerPage-head` is a 90px strip — the first cut of this shot was
 * a name and a status word, which says nothing about a page whose whole point
 * is how much is on it. The column is what a reader actually sees.
 */
await shoot('server-page', {
  path: '/garrison/s/1',
  selector: '.IndexPage-results',
  waitFor: '.GarrisonServerPage-facts',
  pad: 8,
  maxHeight: 2200,
});

await shoot('backups', {
  path: '/garrison/s/2',
  selector: '.GarrisonBackups',
  waitFor: '.GarrisonBackups-item',
});

await shoot('widget', {
  path: '/all',
  selector: '.GarrisonSidebar',
  waitFor: '.GarrisonServer-link',
});

await shoot('console', {
  path: '/garrison/s/2',
  selector: '.GarrisonConsole',
  waitFor: '.GarrisonConsole-line',
});

await shoot('admin', {
  path: '/admin#/extension/ernestdefoe-garrison',
  selector: '.GarrisonAdmin-section',
  waitFor: '.GarrisonAdmin-delivery',
  // The admin app boots into the dashboard and then routes; give it the extra
  // moment rather than photographing the previous page.
  before: async (p) => {
    await p.goto(`${BASE}/admin#/extension/ernestdefoe-garrison`, { waitUntil: 'networkidle2' });
    await p.waitForSelector('.GarrisonAdmin-delivery', { timeout: 30000 });
  },
});

await browser.close();
console.log('done');

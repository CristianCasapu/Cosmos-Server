// Fork addition: draws the icons of the installable panel (client/public/pwa/)
// from one drawing: the Cosmos mark (client/src/assets/images/icons/logo.svg)
// in white on the skin's accent gradient (--ct-accent-fill in
// client/public/skin/theme.css). Run it again only when the mark or the
// colours change; the PNGs are committed.
//
//   docker run --rm -v "$PWD":/src -w /src mcr.microsoft.com/playwright:v1.49.1-noble \
//     sh -c 'cd /tmp && npm i -s playwright@1.49.1 && NODE_PATH=/tmp/node_modules node /src/scripts/fork-pwa-icons.mjs'
//
//   icon-192.png, icon-512.png   "any": a rounded tile, as Chromium shows it on a desktop
//   icon-maskable-512.png        "maskable": the tile fills the square, the mark stays in
//                                the inner circle that every launcher keeps (80 %)
//   apple-touch-icon.png         180 px, square: iOS rounds the corners itself
import { createRequire } from 'node:module';
import { readFileSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const require = createRequire(import.meta.url);
const { chromium } = require('playwright');
const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const out = join(root, 'client/public/pwa');
mkdirSync(out, { recursive: true });

// the mark's shapes, white instead of black
const logo = readFileSync(join(root, 'client/src/assets/images/icons/logo.svg'), 'utf8');
const shapes = logo
  .replace(/^[\s\S]*?<\/defs>/, '')
  .replace(/<\/svg>\s*$/, '')
  .replace(/class="cls-1"/g, 'fill="none" stroke="#ffffff" stroke-width="88.62" fill-rule="evenodd"')
  .replace(/<circle /g, '<circle fill="#ffffff" ');

// mark: the share of the side the mark takes; corner: the tile's corner radius as a share
const drawing = (size, mark, corner) => {
  const markSize = size * mark;
  const offset = (size - markSize) / 2;
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 ${size} ${size}">
  <defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1">
    <stop offset="0" stop-color="#2f6fe6"/><stop offset="1" stop-color="#0b7a96"/>
  </linearGradient></defs>
  <rect width="${size}" height="${size}" rx="${size * corner}" fill="url(#g)"/>
  <svg x="${offset}" y="${offset + markSize * 0.02}" width="${markSize}" height="${markSize}" viewBox="0 0 1358 1358">${shapes}</svg>
</svg>`;
};

const icons = [
  { file: 'icon-192.png', size: 192, mark: 0.6, corner: 0.22 },
  { file: 'icon-512.png', size: 512, mark: 0.6, corner: 0.22 },
  { file: 'icon-maskable-512.png', size: 512, mark: 0.5, corner: 0 },
  { file: 'apple-touch-icon.png', size: 180, mark: 0.6, corner: 0 },
];

const browser = await chromium.launch();
const page = await browser.newPage({ deviceScaleFactor: 1 });
for (const icon of icons) {
  await page.setViewportSize({ width: icon.size, height: icon.size });
  await page.setContent(`<html><body style="margin:0;background:transparent">${drawing(icon.size, icon.mark, icon.corner)}</body></html>`);
  await page.locator('svg').first().screenshot({ path: join(out, icon.file), omitBackground: true });
  console.log('wrote', icon.file);
}
await browser.close();

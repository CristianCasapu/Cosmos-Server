// Fork addition: puts pictures of scripts/fork-ui-gallery.mjs next to each
// other in one picture, each under a caption: light beside dark, or "before"
// beside "after". A small HTML page, screenshotted by the same Playwright.
//
//   node scripts/fork-ui-gallery-sheet.mjs <spec.json>
//
// spec.json: { "sheets": [ {
//     "out": "/gallery/sheets/home.png",        .png, or .jpg for a smaller file
//     "title": "Home, 1366x768",                optional line above the pictures
//     "columns": 2, "cellWidth": 780,           the pictures are scaled to this width
//     "maxHeight": 900,                         taller pictures are cut at the bottom (in cell pixels)
//     "cells": [ { "caption": "before", "file": "/gallery/before/chromium-1366x768-dark/home.png" }, ... ]
// } ] }
import { chromium } from 'playwright';
import { readFileSync, writeFileSync, mkdirSync, existsSync, unlinkSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const spec = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const escape = (text) => String(text).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));

const browser = await chromium.launch();
const page = await browser.newPage({ deviceScaleFactor: 1 });
for (const sheet of spec.sheets) {
  const columns = sheet.columns || sheet.cells.length;
  const cellWidth = sheet.cellWidth || 780;
  const html = `<!doctype html><meta charset="utf-8"><style>
    body { margin: 0; background: #1b1f27; font: 600 15px/1.3 system-ui, sans-serif; color: #f1f5f9; }
    #sheet { display: inline-block; padding: 10px; }
    h1 { font-size: 17px; margin: 2px 2px 10px; }
    .grid { display: grid; grid-template-columns: repeat(${columns}, ${cellWidth}px); gap: 10px; }
    figure { margin: 0; background: #0b0e14; border: 1px solid #3b4352; }
    figcaption { padding: 6px 10px; background: #2a303c; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
    .crop { ${sheet.maxHeight ? `max-height: ${sheet.maxHeight}px;` : ''} overflow: hidden; }
    img { display: block; width: ${cellWidth}px; height: auto; }
    .missing { padding: 30px 10px; color: #f87171; }
  </style><div id="sheet">${sheet.title ? `<h1>${escape(sheet.title)}</h1>` : ''}<div class="grid">${sheet.cells.map((cell) => `
    <figure><figcaption>${escape(cell.caption || '')}</figcaption><div class="crop">${existsSync(cell.file)
      ? `<img src="${pathToFileURL(resolve(cell.file)).href}">` : `<div class="missing">no picture: ${escape(cell.file)}</div>`}</div></figure>`).join('')}
  </div></div>`;
  mkdirSync(dirname(resolve(sheet.out)), { recursive: true });
  const tmp = resolve(sheet.out) + '.html';
  writeFileSync(tmp, html);
  await page.goto(pathToFileURL(tmp).href);
  await page.evaluate(() => Promise.all([...document.images].map((i) => (i.complete ? true : new Promise((r) => { i.onload = i.onerror = r; })))));
  const jpeg = /\.jpe?g$/i.test(sheet.out);
  await page.locator('#sheet').screenshot({ path: sheet.out, type: jpeg ? 'jpeg' : 'png', ...(jpeg ? { quality: sheet.quality || 82 } : {}) });
  unlinkSync(tmp);
  console.log('sheet', sheet.out);
}
await browser.close();

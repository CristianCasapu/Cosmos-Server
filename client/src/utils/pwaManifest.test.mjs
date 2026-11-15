// Fork addition: the installed app opens full screen where the device can
// (Android: no status bar, no navigation bar), and as an ordinary app window
// where it cannot (desktop browsers, iOS).
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const manifest = JSON.parse(readFileSync(fileURLToPath(new URL('../../public/manifest.webmanifest', import.meta.url)), 'utf8'));

test('the installed app asks for the full screen, and falls back to its own window', () => {
  assert.equal(manifest.display, 'fullscreen');
  assert.deepEqual(manifest.display_override, ['fullscreen', 'standalone']);
  // never a browser tab or a window with the browser's address bar
  assert.ok(!manifest.display_override.includes('browser') && !manifest.display_override.includes('minimal-ui'));
});

test('the page keeps clear of the notch when nothing of the system is drawn over it', () => {
  const page = readFileSync(fileURLToPath(new URL('../../index.html', import.meta.url)), 'utf8');
  assert.match(page, /name="viewport"[^>]*viewport-fit=cover/);
  const skin = readFileSync(fileURLToPath(new URL('../../public/skin/theme.css', import.meta.url)), 'utf8');
  for (const side of ['top', 'right', 'bottom', 'left']) assert.match(skin, new RegExp(`--ct-safe-${side}: env\\(safe-area-inset-${side}`));
});

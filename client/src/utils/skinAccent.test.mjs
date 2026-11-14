// Fork addition: tests of skinAccent.js. Run: node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { parseColour, contrast, accentTokens, tokensCss, safeTokens, SURFACES, hex, mix } from './skinAccent.js';

const WHITE = { r: 255, g: 255, b: 255 };
const stops = (gradient) => (gradient.match(/#[0-9a-f]{6}/g) || []).map(parseColour);

test('colours as Configuration > Theme stores them, and nothing else', () => {
  assert.deepEqual(parseColour('rgba(64, 132, 255, 1)'), { r: 64, g: 132, b: 255 });
  assert.deepEqual(parseColour('rgb(14,150,184)'), { r: 14, g: 150, b: 184 });
  assert.deepEqual(parseColour('#48f'), { r: 68, g: 136, b: 255 });
  assert.deepEqual(parseColour('#4084FF'), { r: 64, g: 132, b: 255 });
  for (const bad of ['', null, undefined, 'blue', 'rgba(300, 0, 0, 1)', 'url(x)', 'rgba(1,2)', '#12345', 'red; background: url(x)']) {
    assert.equal(parseColour(bad), null, String(bad));
  }
  assert.equal(accentTokens('', 'rgba(1,2,3,1)'), null, 'no primary colour: the skin keeps its own');
  assert.equal(tokensCss(null), '');
});

// a grid of hues and lightnesses, the extremes included
const picks = [];
for (let h = 0; h < 360; h += 30) {
  for (const l of [15, 35, 50, 65, 85]) {
    const c = (n) => { const k = (n + h / 30) % 12; const a = (l / 100) * Math.min(1, 1 - l / 100); return Math.round(255 * (l / 100 - a * Math.max(-1, Math.min(k - 3, 9 - k, 1)))); };
    picks.push(`rgba(${c(0)}, ${c(8)}, ${c(4)}, 1)`);
  }
}
picks.push('rgba(255, 255, 255, 1)', 'rgba(0, 0, 0, 1)', 'rgba(255, 255, 0, 1)', 'rgba(128, 128, 128, 1)');

test('every pick gives readable text, fills and rings in both modes', () => {
  for (const primary of picks) {
    const t = accentTokens(primary, picks[(picks.indexOf(primary) * 7) % picks.length]);
    assert.ok(t, primary);
    for (const stop of stops(t.common['--ct-accent-fill'])) {
      assert.ok(contrast(stop, WHITE) >= 4.5, `${primary}: white on the fill ${hex(stop)} is ${contrast(stop, WHITE).toFixed(2)}`);
    }
    for (const mode of ['dark', 'light']) {
      const text = parseColour(t[mode]['--ct-accent-text']);
      for (const surface of SURFACES[mode]) {
        assert.ok(contrast(text, parseColour(surface)) >= 4.5, `${primary} ${mode}: text ${hex(text)} on ${surface} is ${contrast(text, parseColour(surface)).toFixed(2)}`);
        // and on the accent's own tints over it (a selected tab, a quiet button, the selected menu entry)
        for (const alpha of [0.09, 0.14, 0.2]) {
          const tint = mix(parseColour(primary), parseColour(surface), alpha);
          assert.ok(contrast(text, tint) >= 4.5, `${primary} ${mode}: text ${hex(text)} on the ${alpha} tint ${hex(tint)}`);
        }
      }
      const ring = parseColour(t[mode]['--ct-focus']);
      assert.ok(contrast(ring, parseColour(SURFACES[mode][0])) >= 3, `${primary} ${mode}: focus ring`);
    }
  }
});

test('a colour that is readable already is kept as it is', () => {
  // a light blue on the dark page, a dark blue on the light one
  const t = accentTokens('rgba(140, 183, 255, 1)', 'rgba(14, 150, 184, 1)');
  assert.equal(t.dark['--ct-accent-text'], '#8cb7ff');
  assert.equal(t.common['--ct-accent'], '#8cb7ff');
  const u = accentTokens('#1e3a8a', '#0e96b8');
  assert.equal(u.light['--ct-accent-text'], '#1e3a8a');
  // the skin's own blue gives about what the skin had by hand
  const own = accentTokens('rgba(64, 132, 255, 1)', 'rgba(14, 150, 184, 1)');
  assert.ok(contrast(parseColour(own.light['--ct-accent-text']), parseColour('#e2ebfa')) >= 4.5, 'light: on a selected tab');
  // the hue stays: a green stays green
  const g = parseColour(accentTokens('rgba(34, 197, 94, 1)', '').light['--ct-accent-text']);
  assert.ok(g.g > g.r && g.g > g.b, 'still green: ' + hex(g));
});

test('a missing secondary colour falls back to the skin\'s cyan', () => {
  const t = accentTokens('#4084ff', 'not a colour');
  assert.equal(t.common['--ct-accent-2'], '#0e96b8');
});

test('the style sheet holds only known names and colour values', () => {
  const css = tokensCss(accentTokens('#4084ff', '#0e96b8'));
  assert.match(css, /:root:root \{ --ct-accent: #4084ff;/);
  assert.match(css, /:root:root\[data-ct-mode="dark"\] \{ --ct-focus: #[0-9a-f]{6}; --ct-accent-text:/);
  assert.match(css, /:root:root\[data-ct-mode="light"\]/);
  assert.doesNotMatch(css, /[<>{}]\s*[<>]/);
  const cleaned = safeTokens({ '--ct-accent': '#fff', '--ct-accent-text': 'url(https://x)', '--other': '#000', '--ct-accent-2': 'red;}body{display:none', '--ct-focus': 'var(--x)' });
  assert.deepEqual(cleaned, { '--ct-accent': '#fff' });
  assert.deepEqual(safeTokens('junk'), {});
});

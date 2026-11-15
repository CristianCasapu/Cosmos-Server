// Fork addition: a page opened in a new tab must get no handle on the panel's
// tab (window.opener): every link or window.open with target "_blank" in the
// frontend carries noopener (and noreferrer). Run with:
//   node --test "client/src/utils/*.test.mjs"
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));

const sources = (dir) => readdirSync(dir).flatMap((name) => {
  const path = join(dir, name);
  if (statSync(path).isDirectory()) {
    return name === 'node_modules' ? [] : sources(path);
  }
  return /\.(jsx?|tsx?|mjs)$/.test(name) && !/\.test\.mjs$/.test(name) ? [path] : [];
});

// blankWithoutNoopener lists the places in one file's text where "_blank"
// is used without noopener: a window.open call must name it in its features,
// a link (JSX attribute or props object) within the element, which spans at
// most a few lines either side.
export const blankWithoutNoopener = (text) => {
  const lines = text.split('\n');
  const found = [];
  lines.forEach((line, index) => {
    if (!/['"]_blank['"]/.test(line)) {
      return;
    }
    const call = line.match(/window\.open\((.*)\)/);
    if (call) {
      if (!/noopener/.test(call[1])) {
        found.push(index + 1);
      }
      return;
    }
    const near = lines.slice(Math.max(0, index - 4), index + 5).join('\n');
    if (!/noopener/.test(near)) {
      found.push(index + 1);
    }
  });
  return found;
};

test('every _blank in the frontend carries noopener', () => {
  const missing = [];
  for (const file of sources(root)) {
    for (const line of blankWithoutNoopener(readFileSync(file, 'utf8'))) {
      missing.push(`${relative(root, file)}:${line}`);
    }
  }
  assert.deepEqual(missing, [], 'add rel="noopener noreferrer" (or "noopener,noreferrer" to window.open)');
});

test('the check sees a missing noopener', () => {
  assert.deepEqual(blankWithoutNoopener('<a href="x" target="_blank">x</a>'), [1]);
  assert.deepEqual(blankWithoutNoopener("window.open(url, '_blank');"), [1]);
  assert.deepEqual(blankWithoutNoopener("window.open(url, '_blank', 'noopener,noreferrer');"), []);
  assert.deepEqual(blankWithoutNoopener('<a href="x"\n  target="_blank"\n  rel="noopener noreferrer">x</a>'), []);
});

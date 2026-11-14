// Fork addition: what the panel's script policy (src/fork_csp.go: scripts
// from the panel's own files only, no eval) needs from the source. A page or a
// component that breaks one of these rules works in development and then does
// nothing in the browser, so it is caught here.
// node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

const client = new URL('../../', import.meta.url).pathname;

// the HTML pages of the build (vite.config.js: rollupOptions.input)
const pages = ['index.html', 'offline.html'];

test('the pages hold no inline script, no inline handler and no script of another origin', () => {
  for (const page of pages) {
    const html = readFileSync(join(client, page), 'utf8').replace(/<!--[\s\S]*?-->/g, '');
    for (const tag of html.match(/<script\b[^>]*>/gi) || []) {
      const src = /\ssrc\s*=\s*["']([^"']*)["']/i.exec(tag);
      assert.ok(src, `${page}: an inline <script> (${tag}): move it to a file under client/public/ or client/src/`);
      assert.match(src[1], /^\/(?!\/)/, `${page}: a script from another origin (${tag})`);
    }
    // every <script ...> has an empty body
    for (const [, body] of html.matchAll(/<script\b[^>]*>([\s\S]*?)<\/script>/gi)) {
      assert.equal(body.trim(), '', `${page}: a <script> with a body`);
    }
    assert.doesNotMatch(html, /\son[a-z]+\s*=/i, `${page}: an inline event handler (onload=, onclick=...)`);
    assert.doesNotMatch(html, /javascript:/i, `${page}: a javascript: address`);
    for (const tag of html.match(/<link\b[^>]*>/gi) || []) {
      const href = /\shref\s*=\s*["']([^"']*)["']/i.exec(tag);
      if (href) assert.match(href[1], /^\/(?!\/)/, `${page}: a file of another origin (${tag})`);
    }
  }
});

const sources = (dir, out = []) => {
  for (const name of readdirSync(dir)) {
    const full = join(dir, name);
    if (statSync(full).isDirectory()) sources(full, out);
    else if (/\.(jsx?|mjs)$/.test(name) && !/\.test\.mjs$/.test(name)) out.push(full);
  }
  return out;
};

test('the panel code evaluates no text as code', () => {
  const files = [...sources(join(client, 'src')), ...sources(join(client, 'sw')), ...sources(join(client, 'public'))];
  assert.ok(files.length > 100, 'the sources were not found');
  for (const file of files) {
    const code = readFileSync(file, 'utf8');
    const where = file.slice(client.length);
    assert.doesNotMatch(code, /(^|[^\w.$])eval\s*\(/, `${where}: eval()`);
    assert.doesNotMatch(code, /new\s+Function\s*\(/, `${where}: new Function()`);
    assert.doesNotMatch(code, /set(Timeout|Interval)\s*\(\s*['"`]/, `${where}: setTimeout/setInterval with a string`);
    assert.doesNotMatch(code, /["'`]javascript:/i, `${where}: a javascript: address`);
    // whiskers compiles its templates with new Function: utils/whiskersSafe.js reads them instead
    assert.doesNotMatch(code, /from\s+['"]whiskers['"]|require\(\s*['"]whiskers['"]\s*\)/, `${where}: imports whiskers`);
  }
});

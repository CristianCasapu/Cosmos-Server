// Fork addition: the browser checks in scripts/ run against the panel with its
// script policy enforced (src/fork_csp.go). Playwright's page.waitForFunction
// evaluates its predicate in the page through eval, which the policy refuses:
// WebKit then fails the wait at once and a check reports a failure that is
// not one. Wait with locators, or ask page.title()/page.evaluate in a loop.
// Run with: node --test "client/src/utils/*.test.mjs"
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const scripts = fileURLToPath(new URL('../../../scripts/', import.meta.url));

test('no browser check waits with page.waitForFunction', () => {
  const found = readdirSync(scripts)
    .filter((name) => name.endsWith('.mjs'))
    .filter((name) => /\.waitForFunction\(/.test(readFileSync(scripts + name, 'utf8')));
  assert.deepEqual(found, []);
});

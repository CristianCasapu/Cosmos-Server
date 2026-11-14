// Fork addition: tests of tokenForm.js. Run with: node --test "client/src/utils/*.test.mjs"
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { PERMISSIONS, READ_ONLY, FULL, presetOf, permissionsOf, toggle, fullControl, fullControlText, placeChoices, placeOf, nameProblem, PROPOSED_EXPIRY } from './tokenForm.js';

test('the presets are the server\'s own lists', () => {
  const go = readFileSync(new URL('../../../src/utils/types.go', import.meta.url), 'utf8');
  const ids = Object.fromEntries([...go.matchAll(/(PERM_\w+)\s+Permission = (\d+)/g)].map((m) => [m[1], Number(m[2])]));
  const block = go.slice(go.indexOf('var DefaultAdminTokenPermissions'), go.indexOf('}', go.indexOf('var DefaultAdminTokenPermissions')));
  const full = [...block.matchAll(/(PERM_\w+)/g)].map((m) => ids[m[1]]).sort((a, b) => a - b);
  assert.deepEqual(full, FULL);
  const api = readFileSync(new URL('../../../src/configapi/api_tokens.go', import.meta.url), 'utf8');
  const ro = api.slice(api.indexOf('} else if request.ReadOnly {'), api.indexOf('} else {', api.indexOf('} else if request.ReadOnly {')));
  assert.deepEqual([...ro.matchAll(/utils\.(PERM_\w+)/g)].map((m) => ids[m[1]]).sort((a, b) => a - b), READ_ONLY);
  // every permission a token can hold has its sentence
  assert.deepEqual(PERMISSIONS.map((p) => p.id), FULL);
});

test('a list reads back as its preset', () => {
  assert.equal(presetOf([30, 20, 10, 1]), 'read');
  assert.equal(presetOf(FULL), 'full');
  assert.equal(presetOf([1, 20]), 'custom');
  assert.deepEqual(permissionsOf('read'), READ_ONLY);
  assert.deepEqual(permissionsOf('custom', [20, 1, 20]), [1, 20]);
});

test('managing needs seeing: the pair is ticked and unticked together', () => {
  assert.deepEqual(toggle([], 21), [20, 21]);
  assert.deepEqual(toggle([20, 21], 20), []);
  assert.deepEqual(toggle([1, 2], 1), []);
  assert.deepEqual(toggle([], 2), [1, 2]);
  assert.deepEqual(toggle([20, 21], 21), [20]);
});

test('the permissions that amount to full control are named, honestly', () => {
  assert.deepEqual(fullControl(READ_ONLY), []);
  assert.deepEqual(fullControl([20, 21, 30, 31]).map((p) => p.id), [21, 31]);
  assert.equal(fullControlText(PERMISSIONS.find((p) => p.id === 31))[0], 'mgmt.config.apiTokens.fork.fullAudit');
  assert.equal(fullControlText(PERMISSIONS.find((p) => p.id === 21))[0], 'mgmt.config.apiTokens.fork.fullDocker');
  assert.equal(PROPOSED_EXPIRY, 90);
});

test('where it may be used from is proposed from the address in use', () => {
  const check = { you: { address: '192.168.1.5' }, server: { addresses: [{ ip: '192.168.1.10', network: '192.168.1.0/24' }] } };
  const choices = placeChoices(check);
  assert.deepEqual(choices.map((c) => c.value), ['network', 'device', 'anywhere', 'other']);
  assert.equal(placeOf(['192.168.1.0/24'], choices), 'network');
  assert.equal(placeOf([], choices), 'anywhere');
  assert.equal(placeOf(['10.0.0.1'], choices), 'other');
  assert.deepEqual(placeChoices(null).map((c) => c.value), ['anywhere', 'other']);
});

test('the name follows the server\'s rule', () => {
  assert.equal(nameProblem('home-assistant'), null);
  assert.equal(nameProblem('home assistant')[0], 'mgmt.config.apiTokens.fork.nameRule');
  assert.equal(nameProblem('ha', ['ha'])[0], 'mgmt.config.apiTokens.fork.nameTaken');
  assert.equal(nameProblem(''), null);
});

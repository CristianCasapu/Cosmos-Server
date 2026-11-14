// Fork addition: tests of leftovers.js. Run with: node --test "client/src/utils/*.test.mjs"
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  appOf, sortLeftovers, whatHappened, appNow, restoreHint, removeQuestion, removeBlocked,
  answerText, failureText, commandLabel, allTexts,
} from './leftovers.js';

const translations = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));

const fill = ([, text, values]) => text.replace(/\{\{(\w+)\}\}/g, (whole, name) => (values && values[name] !== undefined ? String(values[name]) : whole));

test('the app of a leftover is the name before the marker, and only for the pattern', () => {
  assert.equal(appOf('jellyfin-cosmos-old-1a2b3c4d5e6f'), 'jellyfin');
  assert.equal(appOf('a-cosmos-old-b-cosmos-old-000000000000'), 'a-cosmos-old-b');
  for (const other of ['jellyfin', 'jellyfin-cosmos-old-1A2B3C4D5E6F', 'jellyfin-cosmos-old-1a2b', '', null, undefined, 42]) {
    assert.equal(appOf(other), '', String(other));
  }
});

test('the most recent first, then by name; anything else is dropped', () => {
  const sorted = sortLeftovers([
    { name: 'b-cosmos-old-000000000000', finished: '2026-11-01T00:00:00Z' },
    { name: 'a-cosmos-old-000000000000', finished: '' },
    { name: 'c-cosmos-old-000000000000', finished: '2026-11-10T00:00:00Z' },
    { name: 'z-cosmos-old-000000000000', finished: '2026-11-01T00:00:00Z' },
    { name: 'plain' },
    null,
  ]).map((entry) => entry.name[0]);
  assert.deepEqual(sorted, ['c', 'b', 'z', 'a']);
  assert.deepEqual(sortLeftovers(undefined), []);
  assert.deepEqual(sortLeftovers({ not: 'a list' }), []);
});

test('the notice says what happened and whether the app runs now', () => {
  const entry = { app: 'jellyfin', name: 'jellyfin-cosmos-old-1a2b3c4d5e6f', running: false, appExists: true, appRunning: true };
  assert.equal(fill(whatHappened(entry, '12/11/2026, 08:30')),
    'The change of jellyfin made on 12/11/2026, 08:30 was interrupted: the previous container is kept, stopped, as jellyfin-cosmos-old-1a2b3c4d5e6f.');
  assert.equal(fill(whatHappened(entry, '')), 'A change of jellyfin was interrupted: the previous container is kept, stopped, as jellyfin-cosmos-old-1a2b3c4d5e6f.');
  assert.match(fill(whatHappened({ ...entry, running: true }, '')), /and it is running\.$/);
  assert.equal(fill(appNow(entry)), 'jellyfin runs now under its name.');
  assert.equal(fill(appNow({ ...entry, appRunning: false })), 'A container named jellyfin exists now, but it is stopped.');
  assert.equal(fill(appNow({ ...entry, appExists: false, appRunning: false })), 'No container is named jellyfin now: the app is not running.');
});

test('putting back says the current one is kept aside; removing asks and is blocked while it runs', () => {
  const entry = { app: 'jellyfin', name: 'jellyfin-cosmos-old-1a2b3c4d5e6f', running: false, appExists: true, appRunning: true };
  assert.match(fill(restoreHint(entry)), /the current jellyfin is stopped and kept aside, not deleted/);
  assert.doesNotMatch(fill(restoreHint({ ...entry, appExists: false })), /current/);
  assert.match(fill(removeQuestion(entry)), /^Remove jellyfin-cosmos-old-1a2b3c4d5e6f\? .*volumes are kept/);
  assert.equal(removeBlocked(entry), null);
  assert.ok(removeBlocked({ ...entry, running: true }));
});

test('answers and failures in words, by their code', () => {
  assert.equal(fill(answerText({ data: { result: 'restored', app: 'a', setAside: 'a-cosmos-old-000000000000' } })),
    'a is back. The container that had its name is kept, stopped, as a-cosmos-old-000000000000.');
  assert.equal(fill(answerText({ data: { result: 'restored', app: 'a', setAside: '' } })), 'a is back and started.');
  assert.equal(fill(answerText({ data: { result: 'removed', name: 'x' } })), 'x was removed.');
  const keys = new Set();
  for (const code of ['LEFT002', 'LEFT003', 'LEFT004', 'LEFT005', 'LEFT006', 'LEFT007', 'HTTP008', undefined]) {
    keys.add(failureText(code, 'why')[0]);
  }
  assert.equal(keys.size, 7);
  assert.match(fill(failureText('LEFT004', 'port is already allocated')), /everything is as it was\. port is already allocated$/);
});

test('the two commands have their own label in the activity toasts', () => {
  assert.equal(fill(commandLabel(JSON.stringify({ action: 'restore', name: 'jellyfin-cosmos-old-1a2b3c4d5e6f' }))), 'Put back the previous jellyfin');
  assert.equal(fill(commandLabel(JSON.stringify({ action: 'remove', name: 'jellyfin-cosmos-old-1a2b3c4d5e6f' }))), 'Remove the leftover jellyfin-cosmos-old-1a2b3c4d5e6f');
  for (const odd of ['', 'not json', '[]', 'null', JSON.stringify({ action: 'x' }), undefined]) {
    assert.equal(commandLabel(odd)[0], 'activity.leftovers.other');
  }
  const activity = readFileSync(new URL('./activity.js', import.meta.url), 'utf8');
  assert.match(activity, /\/\^\\\/api\\\/servapps-leftovers\$\/, \(m, method, body\) => leftoverCommand\(body\)/);
  assert.match(activity, /describe\(url, method, options && options\.body\)/);
});

test('every text is in the English translation file, word for word', () => {
  const pairs = allTexts().map(([key, text]) => [key, text]);
  const jsx = readFileSync(new URL('../components/leftovers.jsx', import.meta.url), 'utf8');
  const found = [...jsx.matchAll(/\bt\(\s*'([^']+)',\s*'((?:[^'\\]|\\.)*)'/g)];
  assert.ok(found.length >= 4);
  for (const m of found) pairs.push([m[1], m[2].replace(/\\'/g, "'")]);
  // a fork text without its English words would show its key
  assert.equal([...jsx.matchAll(/\bt\(\s*'mgmt\.servapps\.leftovers[^']*'\s*\)/g)].length, 0);
  // the notification the server writes (src/docker/fork_leftovers.go)
  const server = readFileSync(new URL('../../../src/docker/fork_leftovers.go', import.meta.url), 'utf8');
  for (const key of server.matchAll(/"(header\.notification\.(?:title|message)\.containerLeftover)"/g)) {
    assert.ok(translations[key[1]], key[1]);
    pairs.push([key[1], translations[key[1]]]);
  }
  assert.ok(pairs.length > 35, `${pairs.length}`);
  for (const [key, text] of pairs) {
    assert.equal(translations[key], text, `translation.json: ${key}`);
  }
});

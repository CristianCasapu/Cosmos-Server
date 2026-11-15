// node --test client/src/utils/edgeBlocks.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  ALL_TEXTS, EXEMPT, PERMISSION_NAME, blockOf, canUnblock, durationText, failureText, jailText, keyOf, looksLocal, problemText, proofText, rulesShown, stateText,
} from './edgeBlocks.js';

const en = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));

test('every text of the module and of the card is in the English translation file, with the same text', () => {
  for (const [key, text] of ALL_TEXTS) assert.equal(en[key], text, key);
  let found = 0;
  for (const file of ['../components/edgeBlocks.jsx', '../pages/security/addresses.jsx', '../pages/security/incidents.jsx']) {
    const src = readFileSync(new URL(file, import.meta.url), 'utf8');
    for (const m of src.matchAll(/t\('([\w.]+)', '((?:[^'\\]|\\.)*)'/g)) {
      found++;
      assert.equal(en[m[1]], m[2].replace(/\\'/g, "'"), file + ': ' + m[1]);
    }
  }
  assert.ok(found > 40, 'found the texts of the card');
  // the activity label and the notification
  for (const key of ['activity.cloudflare.edgeBlocks', 'header.notification.title.edgeBlocksProblem', 'header.notification.message.edgeBlocksPermission', 'header.notification.message.edgeBlocksToken']) {
    assert.ok(en[key], key);
  }
});

test('the server\'s notification keys are the ones translated', () => {
  const go = readFileSync(new URL('../../../src/edgeblock/events.go', import.meta.url), 'utf8');
  for (const m of go.matchAll(/"(header\.notification\.[\w.]+)"/g)) assert.ok(en[m[1]], m[1]);
});

test('the key of an address is the one the server uses', () => {
  const cases = {
    '198.51.100.7': '198.51.100.7',
    '::ffff:198.51.100.7': '198.51.100.7',
    '2001:db8:66:1::5': '2001:db8:66:1::/64',
    '2001:db8:5:6:7:8:9:a': '2001:db8:5:6::/64',
    '[2001:db8:5:6::1]': '2001:db8:5:6::/64',
    '2001:db8::1': '2001:db8::/64',
    '2001:0:0:1::9': '2001:0:0:1::/64',
    '2001:db8:0:1::': '2001:db8:0:1::/64',
    '2a02:2f04:aa:bb:cc:dd:ee:ff': '2a02:2f04:aa:bb::/64',
  };
  for (const [address, key] of Object.entries(cases)) assert.equal(keyOf(address), key, address);
  for (const bad of ['', 'example.org', '1:2:3', '1::2::3', 'g::1']) assert.equal(keyOf(bad), '', bad);
  const blocked = { '2001:db8:66:1::/64': { until: 'x' } };
  assert.equal(blockOf(blocked, '2001:db8:66:1::77').until, 'x');
  assert.equal(blockOf(blocked, '198.51.100.7'), null);
  assert.equal(blockOf(null, '198.51.100.7'), null);
});

test('the state in one sentence', () => {
  assert.equal(stateText({ zones: [] })[1][0], 'mgmt.edge.status.noZone');
  assert.equal(stateText({ zones: [{ name: 'example.org' }], enabled: false })[1][0], 'mgmt.edge.status.off');
  assert.equal(stateText({ zones: [{ name: 'example.org', problem: { reason: 'permission' } }], enabled: true })[0], 'warning');
  assert.equal(stateText({ zones: [{ name: 'example.org' }], enabled: true, live: 0 })[1][0], 'mgmt.edge.status.onNone');
  assert.equal(stateText({ zones: [{ name: 'example.org' }], enabled: true, live: 1 })[1][0], 'mgmt.edge.status.onOne');
  assert.deepEqual(stateText({ zones: [{ name: 'example.org' }], enabled: true, live: 3 })[1][2], { count: 3 });
});

test('a missing permission is named', () => {
  const text = problemText({ zone: 'example.org', reason: 'permission' });
  assert.equal(text[2].permission, PERMISSION_NAME);
  assert.equal(PERMISSION_NAME, 'Zone > Firewall Services > Edit');
  const proof = proofText({ zone: 'example.org', ok: false, step: 'list', reason: 'permission' });
  assert.equal(proof[0], 'mgmt.edge.proof.failed');
  assert.equal(proof[2].problemKey, 'mgmt.edge.problem.permission');
  assert.equal(proofText({ zone: 'example.org', ok: true, inferred: true })[0], 'mgmt.edge.proof.inferred');
  assert.equal(proofText({ zone: 'example.org', ok: true })[0], 'mgmt.edge.proof.ok');
});

test('refusals in words', () => {
  const exempt = failureText({ reason: 'exempt', exempt: 'admin' });
  assert.equal(exempt[2].why, EXEMPT.admin[1]);
  assert.equal(failureText({ reason: 'off' })[0], 'mgmt.edge.fail.off');
  assert.equal(failureText(null)[0], 'mgmt.edge.fail.noAnswer');
  assert.equal(failureText({ reason: 'something_new' })[2].reason, 'something_new');
  // every reason an exemption can have has words
  const go = readFileSync(new URL('../../../src/edgeblock/exempt.go', import.meta.url), 'utf8');
  for (const m of go.matchAll(/Exempt\w+\s+= "(\w+)"/g)) assert.ok(EXEMPT[m[1]], m[1]);
});

test('thresholds and durations in words', () => {
  assert.deepEqual(durationText(14400), ['mgmt.edge.time.hours', '{{count}} hours', { count: 4 }]);
  assert.deepEqual(durationText(86400), ['mgmt.edge.time.day', 'a day']);
  assert.deepEqual(durationText(604800), ['mgmt.edge.time.week', 'a week']);
  assert.deepEqual(durationText(600), ['mgmt.edge.time.minutes', '{{count}} minutes', { count: 10 }]);
  const line = jailText({ name: 'auth', maxRetry: 5, findTime: 3600, banTime: 14400, mode: 'managed_challenge' });
  assert.equal(line[2].count, 5);
  assert.equal(line[2].whatKey, 'mgmt.edge.reason.auth');
  assert.equal(line[2].modeKey, 'mgmt.edge.mode.challenge');
});

test('the list: newest first, unblock where it means something', () => {
  const rules = rulesShown({ rules: [{ key: 'a', since: '2026-11-15T10:00:00Z', state: 'active' }, { key: 'b', since: '2026-11-15T11:00:00Z', state: 'orphaned' }] });
  assert.deepEqual(rules.map((r) => r.key), ['b', 'a']);
  assert.ok(canUnblock({ state: 'active' }));
  assert.ok(!canUnblock({ state: 'orphaned' }));
  assert.ok(!canUnblock({ state: 'deleting' }));
  assert.ok(looksLocal('192.168.1.5') && looksLocal('fe80::1') && !looksLocal('198.51.100.7'));
});

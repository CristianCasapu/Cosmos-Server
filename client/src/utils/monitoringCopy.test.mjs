// node --test client/src/utils/monitoringCopy.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  CATCH_UP, POLL_IDLE_MS, POLL_RUNNING_MS, TO_LOCAL, TO_POSTGRES, catchUpOf, countedAgo, countingOf, fill, formatCount, formatDuration, formatSize, formatTime,
  mismatchLine, offerLabel, offerOf, pollDelay, progressOf, resultOf, tableLabel,
} from './monitoringCopy.js';

const NB = ' ';
const source = { bytes: 105 * 1024 * 1024 + 5, records: 1234567, first: Date.UTC(2026, 9, 1, 8), last: Date.UTC(2026, 10, 16, 18), lastCopy: 0, tables: [] };
const idle = {
  inUse: 'postgres', running: false, secondsLeft: -1,
  offers: [
    { direction: TO_POSTGRES, available: true, source },
    { direction: TO_LOCAL, available: true, source: { ...source, bytes: 0, records: 4200 } },
  ],
};

test('numbers and sizes in words', () => {
  assert.equal(formatCount(1234567), '1' + NB + '234' + NB + '567');
  assert.equal(formatCount(0), '0');
  assert.equal(formatCount(-3), '0');
  assert.equal(formatCount('x'), '0');
  assert.equal(formatSize(1000), 'less than 1 MB');
  assert.equal(formatSize(105 * 1024 * 1024), 'about 105 MB');
  assert.equal(formatSize(3 * 1024 * 1024 * 1024), 'about 3.0 GB');
  assert.equal(formatDuration(-1), '');
  assert.equal(formatDuration(20), 'less than a minute left');
  assert.equal(formatDuration(600), 'about 10 min left');
  assert.equal(formatDuration(3 * 3600), 'about 3.0 h left');
  assert.equal(formatTime(0), '');
  assert.match(formatTime(Date.UTC(2026, 10, 16, 18), 'en-GB'), /2026/);
  assert.equal(fill('{{a}} and {{b}}', { a: 1 }), '1 and {{b}}');
});

test('the offers: only while PostgreSQL is in use and nothing runs', () => {
  const forward = offerOf(idle, TO_POSTGRES);
  assert.equal(forward.records, 1234567);
  const label = offerLabel(forward, undefined, 'en-GB');
  assert.match(label, /^Copy the history from the local file \(1 234 567 records, about 105 MB, from .*2026.* to .*2026.*\)$/);
  assert.match(offerLabel(offerOf(idle, TO_LOCAL), undefined, 'en-GB'), /^Copy the records of PostgreSQL into the local file \(4 200 records, from /);
  assert.equal(offerLabel({ ...forward, first: 0, last: 0 }), 'Copy the history from the local file (1' + NB + '234' + NB + '567 records, about 105 MB)');

  assert.equal(offerOf({ ...idle, inUse: 'local' }, TO_POSTGRES), null);
  assert.equal(offerOf({ ...idle, running: true }, TO_POSTGRES), null);
  assert.equal(offerOf({ ...idle, offers: [{ direction: TO_POSTGRES, available: false, problem: 'empty', source }] }, TO_POSTGRES), null);
  assert.equal(offerOf({ ...idle, offers: [{ direction: TO_POSTGRES, available: true }] }, TO_POSTGRES), null);
  assert.equal(offerOf(null, TO_POSTGRES), null);
});

test('the progress of a running copy', () => {
  assert.equal(progressOf(idle), null);
  const p = progressOf({
    running: true, direction: TO_POSTGRES, phase: 'copying', secondsLeft: 120,
    tables: [
      { table: 'metrics', total: 10, done: 10, state: 'copied' },
      { table: 'metric_values', total: 1000, done: 250, state: 'copying' },
      { table: 'events', total: 0, done: 0, state: 'copied' },
    ],
  });
  assert.equal(p.total, 1010);
  assert.equal(p.done, 260);
  assert.equal(p.percent, 25);
  assert.deepEqual(p.tables.map((r) => r.percent), [100, 25, 100]);
  assert.equal(p.secondsLeft, 120);
  assert.equal(progressOf({ running: true, phase: 'verifying', tables: [{ table: 'events', total: 5, done: 1 }] }).percent, 100);
  assert.equal(progressOf({ running: true, tables: [] }).secondsLeft, -1);
});

test('how the last copy ended', () => {
  assert.equal(resultOf(idle), null);
  const upTo = Date.UTC(2026, 10, 17, 12);
  assert.match(resultOf({ last: { outcome: 'done', written: 1234567, upTo } }, undefined, 'en-GB').text,
    /^Copied 1 234 567 records; both stores now hold the same history up to .*2026.*\.$/);
  assert.match(resultOf({ last: { outcome: 'done', written: 0, upTo } }).text, /^Nothing was missing/);
  const mismatch = resultOf({ last: { outcome: 'mismatch', written: 5, tables: [
    { table: 'events', state: 'verified' },
    { table: 'metric_values', state: 'mismatch', sourceRows: 10, targetRows: 9, missing: 1 },
  ] } });
  assert.equal(mismatch.severity, 'warning');
  assert.equal(mismatch.tables.length, 1);
  assert.equal(mismatchLine(mismatch.tables[0]), 'Metric values: 10 rows in the source, 9 found in the target');
  assert.equal(resultOf({ last: { outcome: 'cancelled' } }).severity, 'info');
  assert.equal(resultOf({ last: { outcome: 'stopped' } }).severity, 'warning');
  const failed = resultOf({ last: { outcome: 'failed', message: 'Copying table events failed.' } });
  assert.equal(failed.severity, 'error');
  assert.equal(failed.text, 'The copy failed: Copying table events failed.');
});

test('polling: every 2 s while running, rarely otherwise', () => {
  assert.equal(pollDelay({ running: true }), POLL_RUNNING_MS);
  assert.equal(POLL_RUNNING_MS, 2000);
  assert.equal(pollDelay(idle), POLL_IDLE_MS);
  assert.equal(pollDelay(null), POLL_IDLE_MS);
});

test('every table of the server has a name in words', () => {
  const go = readFileSync(new URL('../../../src/monitorcopy/rules.go', import.meta.url), 'utf8');
  const tables = [...go.matchAll(/^\s*table:\s*"([a-z_]+)"/gm)].map((m) => m[1]);
  assert.ok(tables.length >= 6, 'found ' + tables.length + ' tables in rules.go');
  for (const table of tables) assert.notEqual(tableLabel(table), table, table);
});

// the English texts of this module and of the card are in translation.json, with the same text
test('the texts are in the English translations', () => {
  const en = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));
  const sources = [
    readFileSync(new URL('./monitoringCopy.js', import.meta.url), 'utf8'),
    readFileSync(new URL('../pages/config/forkMonitoringCopy.jsx', import.meta.url), 'utf8'),
  ].join('\n');
  const pairs = [...sources.matchAll(/['"](mgmt\.config\.general\.monitoringDb\.copy\.[A-Za-z.]+)['"],\s*(['"])((?:\\.|(?!\2).)*)\2/g)];
  assert.ok(pairs.length >= 25, 'found ' + pairs.length + ' texts');
  for (const [, key, quote, text] of pairs) {
    const unescaped = quote === "'" ? text.replace(/\\'/g, "'") : text.replace(/\\"/g, '"');
    assert.equal(en[key], unescaped, key);
  }
  const keys = Object.keys(en);
  const block = keys.map((k, i) => (k.startsWith('mgmt.config.general.monitoringDb.copy.') ? i : -1)).filter((i) => i >= 0);
  assert.equal(block[block.length - 1] - block[0] + 1, block.length, 'the keys are one contiguous block');
});

test('values that are not numbers are said; a count in the background is polled', () => {
  const upTo = Date.UTC(2026, 10, 17, 12);
  assert.equal(resultOf({ last: { outcome: 'done', written: 5, upTo, replaced: 2 } }).note, '2 scale values that are not numbers were stored as 1, the neutral scale.');
  assert.equal(resultOf({ last: { outcome: 'done', written: 5, upTo } }).note, undefined);
  const counting = { inUse: 'postgres', running: false, offers: [{ direction: TO_POSTGRES, available: false, counting: true }] };
  assert.equal(countingOf(counting), true);
  assert.equal(pollDelay(counting), POLL_RUNNING_MS);
  // counted before: the old numbers are offered meanwhile, no need to hurry
  const recount = { ...idle, offers: idle.offers.map((o) => ({ ...o, counting: true })) };
  assert.equal(countingOf(recount), false);
  assert.equal(pollDelay(recount), POLL_IDLE_MS);
  assert.equal(countingOf({ ...counting, inUse: 'local' }), false);
});

test('the age of a count, by the server\'s clocks', () => {
  const at = Date.UTC(2026, 10, 18, 12);
  const view = { ...idle, now: at, offers: idle.offers.map((o) => ({ ...o, countedAt: at - 3 * 60000 - 5000 })) };
  assert.equal(countedAgo(offerOf(view, TO_POSTGRES)), 'Counted 3 minutes ago.');
  assert.equal(countedAgo({ countedAt: at - 20000, now: at }), 'Counted just now.');
  assert.equal(countedAgo({ countedAt: at - 3 * 3600000, now: at }), 'Counted 3 hours ago.');
  assert.equal(countedAgo({ countedAt: at + 60000, now: at }), 'Counted just now.'); // a count stamped later than now: never negative
  assert.equal(countedAgo({ countedAt: 0, now: at }), '');
  assert.equal(countedAgo(offerOf(idle, TO_POSTGRES)), '');
});

test('the catch-up after a fallback: its line and its progress', () => {
  const from = Date.UTC(2026, 10, 18, 9);
  const to = Date.UTC(2026, 10, 18, 10);
  assert.equal(catchUpOf(idle), null);
  const done = catchUpOf({ catchUp: { outcome: 'done', counted: true, aside: 1234, present: 1234, written: 1200, from, to } }, undefined, 'en-GB');
  assert.equal(done.severity, 'success');
  assert.match(done.text, /^Caught up 1\u00a0234 records written while the database was away \(from .*2026.* to .*2026.*\): each is in the database, checked one by one \(1\u00a0200 brought over now\)\.$/);
  // review round 3: true numbers, never "caught up" when something is not known
  const partly = catchUpOf({ catchUp: { outcome: 'stopped', counted: true, aside: 2500, present: 10, missing: 0, unchecked: 2490, written: 10, from, to, message: 'It stopped.' } });
  assert.equal(partly.severity, 'warning');
  assert.match(partly.text, /^2\u00a0500 records were written while the database was away .*: 10 are in the database, 0 are not, 2\u00a0490 could not be checked\. It stopped\.$/);
  const inexact = catchUpOf({ catchUp: { outcome: 'done', counted: true, aside: 5, present: 4, inexact: 1, from, to } });
  assert.equal(inexact.severity, 'warning');
  assert.match(inexact.text, /1 hourly or daily totals could not be added up exactly\./);
  const uncounted = catchUpOf({ catchUp: { outcome: 'failed', from, to, message: 'The local file is not there any more.' } });
  assert.match(uncounted.text, /could not be counted nor brought over now: The local file/);
  assert.doesNotMatch(uncounted.text, /\b0\b/);
  assert.equal(catchUpOf({ catchUp: { outcome: 'skipped', from, to, message: 'A copy ran.' } }).severity, 'info');
  const p = progressOf({ running: true, direction: CATCH_UP, phase: 'copying', window: { from, to }, tables: [] });
  assert.equal(p.direction, CATCH_UP);
  assert.equal(p.from, from);
  assert.equal(p.to, to);
});

// node --test client/src/utils/monitoringCopy.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  POLL_IDLE_MS, POLL_RUNNING_MS, TO_LOCAL, TO_POSTGRES, fill, formatCount, formatDuration, formatSize, formatTime,
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

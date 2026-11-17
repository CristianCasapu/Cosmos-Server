// Fork addition: run with node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  PULL_MARKER, PULL_REASONS, feedPulls, formatBytes, formatSeconds, parsePullLine, pullPercent, pullReasonWords, pullWords, withoutPullLines,
} from './pullProgress.js';

const line = (v) => PULL_MARKER + JSON.stringify(v);

// What the server wrote in the live test (src/docker/fork_pull_progress_live_test.go).
const real = [
  '[PULL PROGRESS] {"image":"registry.example.org:5001/fork/bigapp:1","state":"pulling","seconds":0,"layers":0,"ready":0}',
  '{"status":"Pulling from fork/bigapp","id":"1"}',
  '{"status":"Downloading","progressDetail":{"current":1048576,"total":40009728},"id":"2c846c5a3bea"}',
  '[PULL PROGRESS] {"image":"registry.example.org:5001/fork/bigapp:1","state":"pulling","seconds":6,"layers":4,"ready":1,"current":19000000,"total":90028112}',
  '[PULL PROGRESS] {"image":"registry.example.org:5001/fork/bigapp:1","state":"done","seconds":16,"layers":4,"ready":4,"current":90028112,"total":90028112}',
  'Image registry.example.org:5001/fork/bigapp:1 pulled',
];

test('the marker lines become one step per image, the latest state', () => {
  let pulls = [];
  for (const l of real.slice(0, 4)) pulls = feedPulls(pulls, l);
  assert.equal(pulls.length, 1);
  assert.deepEqual(pulls[0], { image: 'registry.example.org:5001/fork/bigapp:1', state: 'pulling', seconds: 6, layers: 4, ready: 1, current: 19000000, total: 90028112 });
  assert.equal(pullPercent(pulls[0]), 21);
  const w = pullWords(pulls[0]);
  assert.equal(w.key, 'mgmt.servapps.create.pull.bytes');
  assert.deepEqual([w.params.current, w.params.total, w.params.ready, w.params.layers, w.params.time], ['19 MB', '90 MB', 1, 4, '6 s']);
  pulls = feedPulls(pulls, real[4]);
  assert.equal(pulls[0].state, 'done');
  assert.equal(pullWords(pulls[0]).key, 'mgmt.servapps.create.pull.done');
  // a second image of the same creation is a second step
  pulls = feedPulls(pulls, line({ image: 'busybox:1.36', state: 'pulling', seconds: 1, layers: 0, ready: 0 }));
  assert.deepEqual(pulls.map((p) => p.image), ['registry.example.org:5001/fork/bigapp:1', 'busybox:1.36']);
});

test('the log keeps every other line and drops the markers', () => {
  assert.equal(withoutPullLines(real.join('\n')), [real[1], real[2], real[5]].join('\n'));
  assert.equal(withoutPullLines(real[0]), '');
  assert.equal(withoutPullLines('Pulling image busybox:1.36'), 'Pulling image busybox:1.36');
});

test('the wait is said as a wait, then as a slow one', () => {
  const p = parsePullLine(line({ image: 'nginx:1.27', state: 'pulling', seconds: 4, layers: 0, ready: 0 }));
  assert.equal(pullWords(p).key, 'mgmt.servapps.create.pull.waiting');
  assert.equal(pullPercent(p), null);
  assert.equal(pullWords({ ...p, seconds: 75 }).key, 'mgmt.servapps.create.pull.slow');
  assert.equal(pullWords({ ...p, seconds: 75 }).params.time, '1 min 15 s');
  assert.equal(pullWords({ ...p, layers: 3, ready: 1 }).key, 'mgmt.servapps.create.pull.layers');
});

test('a failure has its reason in words and a bounded, cleaned technical line', () => {
  const p = parsePullLine(line({ image: 'registry.example.org/app:1', state: 'failed', seconds: 20, layers: 0, ready: 0, reason: 'dns',
    detail: 'dial tcp: lookup registry.example.org on 127.0.0.11:53: server misbehaving‮\u0007' + 'x'.repeat(500) }));
  assert.equal(p.reason, 'dns');
  assert.ok(p.detail.length <= 300);
  assert.ok(!/[\u0000-\u001f‮]/.test(p.detail));
  assert.equal(pullReasonWords(p.reason).key, 'mgmt.servapps.create.pull.reason.dns');
  // an unknown reason is "other", never shown as given
  assert.equal(parsePullLine(line({ image: 'a:1', state: 'failed', reason: '<b>x</b>' })).reason, 'other');
  for (const r of PULL_REASONS) assert.match(pullReasonWords(r).key, /^mgmt\.servapps\.create\.pull\.reason\./);
});

test('what the server reasons and what the panel knows are the same list', () => {
  const go = readFileSync(new URL('../../../src/docker/fork_pull_progress.go', import.meta.url), 'utf8');
  const fromGo = [...go.matchAll(/PullReason\w+\s+= "(\w+)"/g)].map((m) => m[1]).sort();
  assert.deepEqual(fromGo, [...PULL_REASONS].sort());
});

test('broken, foreign or hostile lines are ignored or bounded', () => {
  assert.equal(parsePullLine('{"status":"Downloading"}'), null);
  assert.equal(parsePullLine(PULL_MARKER + '{"image":"a:1","state":"pul'), null); // a line cut in two pieces
  assert.equal(parsePullLine(line({ image: 'a:1', state: 'exploded' })), null);
  assert.equal(parsePullLine(line({ state: 'pulling' })), null);
  const p = parsePullLine(line({ image: 'a:1', state: 'pulling', seconds: -3, layers: 2, ready: 9, current: 10, total: 5 }));
  assert.deepEqual([p.seconds, p.ready, p.current], [0, 2, 5]);
  let pulls = [];
  for (let i = 0; i < 40; i++) pulls = feedPulls(pulls, line({ image: `img${i}:1`, state: 'pulling' }));
  assert.equal(pulls.length, 16);
  assert.equal(feedPulls(pulls, 'nothing'), pulls);
});

test('sizes and times', () => {
  assert.equal(formatBytes(0), '0 B');
  assert.equal(formatBytes(999), '999 B');
  assert.equal(formatBytes(1500), '1.5 kB');
  assert.equal(formatBytes(90028112), '90 MB');
  assert.equal(formatBytes(2.5e9), '2.5 GB');
  assert.equal(formatSeconds(59), '59 s');
  assert.equal(formatSeconds(125), '2 min 05 s');
});

test('every key has its English text in the translation file', () => {
  const en = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));
  const words = [
    ...['pulling', 'done', 'failed'].map((state) => pullWords({ image: 'a', state, seconds: 0, layers: 0, ready: 0, current: 0, total: 0 })),
    pullWords({ image: 'a', state: 'pulling', seconds: 99, layers: 0, ready: 0, current: 0, total: 0 }),
    pullWords({ image: 'a', state: 'pulling', seconds: 0, layers: 2, ready: 0, current: 0, total: 0 }),
    pullWords({ image: 'a', state: 'pulling', seconds: 0, layers: 2, ready: 0, current: 1, total: 2 }),
    ...PULL_REASONS.map(pullReasonWords),
  ];
  for (const w of words) assert.equal(en[w.key], w.text, w.key);
});

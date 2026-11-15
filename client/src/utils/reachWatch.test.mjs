// Fork addition: tests of reachWatch.js. Run with: node --test "client/src/utils/*.test.mjs"
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  CAUSE_TEXT, OUTSIDE_CHECK_TEXT, allTexts, causeText, clock, duration, historyRows, homeNotice, intervalsFor, nameLine,
  outsideParams, reasonText, settingsBody, stateDot,
} from './reachWatch.js';
import { CHECK_TEXT, checkSentence, summarize } from './reachability.js';

const translations = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));

test('every text of the watch is in translation.json, with the same English', () => {
  for (const [key, text] of allTexts()) assert.equal(translations[key], text, key);
});

test('every key the watch\'s screens write out is in translation.json', () => {
  const files = ['../components/reachWatch.jsx', '../components/reachability.jsx', '../pages/notifications/details.jsx', './activity.js']
    .map((f) => readFileSync(new URL(f, import.meta.url), 'utf8'));
  for (const source of files) {
    for (const [, key, english] of source.matchAll(/\bt\('([\w.-]+)',\s*'((?:[^'\\]|\\.)*)'/g)) {
      if (!key.startsWith('reachWatch.') && !key.startsWith('mgmt.reach.')) continue;
      assert.equal(translations[key], english.replace(/\\'/g, '\''), key);
    }
    assert.ok(!/dangerouslySetInnerHTML|\beval\(|new Function/.test(source));
  }
  assert.equal(translations['activity.reachWatch.settings'], 'Save the watch from the internet');
});

test('the causes in a few words are the server\'s', () => {
  const go = readFileSync(new URL('../../../src/utils/fork_reach_watch_texts.go', import.meta.url), 'utf8');
  for (const [cause, text] of Object.entries(CAUSE_TEXT)) {
    assert.ok(go.includes('"notif.reach.cause.' + cause + '"'), cause);
    assert.equal(translations['notif.reach.cause.' + cause], text);
  }
  assert.deepEqual(causeText('nonsense'), ['reachWatch.cause.other', 'Not tested yet']);
  assert.deepEqual(causeText('__proto__'), ['reachWatch.cause.other', 'Not tested yet']);
});

const now = new Date(2026, 10, 15, 10, 0);

test('the home line: an outage, or no way out, else nothing', () => {
  assert.equal(homeNotice(null), null);
  assert.equal(homeNotice({ enabled: false, outage: { since: now, cause: 'connect' } }), null);
  const down = homeNotice({ enabled: true, outage: { since: new Date(2026, 10, 15, 9, 12).toISOString(), cause: 'connect' }, names: [] }, now);
  assert.equal(down.severity, 'error');
  assert.equal(down.params.since, '09:12');
  assert.deepEqual(down.cause, ['notif.reach.cause.connect', 'Cloudflare cannot connect (error 522)']);
  const offline = homeNotice({ enabled: true, names: [{ state: 'unknown', cause: 'offline' }, { state: 'unknown', cause: 'offline' }] }, now);
  assert.equal(offline.severity, 'unknown');
  assert.equal(homeNotice({ enabled: true, names: [{ state: 'unknown', cause: 'offline' }, { state: 'reachable' }] }, now), null);
  assert.equal(homeNotice({ enabled: true, names: [{ state: 'suspect', cause: 'connect' }] }, now), null, 'a suspicion is not told');
  assert.equal(homeNotice({ enabled: true, names: [] }, now), null);
});

test('a name: state, cause, times', () => {
  const line = nameLine({ host: 'example.org', panel: true, state: 'down', cause: 'connect', since: new Date(2026, 10, 15, 9, 12), lastTest: new Date(2026, 10, 15, 9, 58) }, now);
  assert.equal(line.dot, 'error');
  assert.deepEqual(line.cause, causeText('connect'));
  assert.deepEqual(line.parts.map((p) => p[2].time), ['09:12', '09:58']);
  assert.equal(nameLine({ host: 'example.org', state: 'reachable', seenAt: new Date(2026, 10, 14, 9, 0) }, now).parts[0][2].time, '2026-11-14 09:00');
  assert.equal(nameLine({ host: 'a', state: 'reachable' }).cause, undefined);
  assert.equal(stateDot('suspect'), 'warning');
  assert.equal(stateDot('whatever'), 'unknown');
});

test('durations and the history', () => {
  assert.deepEqual(duration(20000)[2], {});
  assert.deepEqual(duration(34 * 60000)[2], { count: 34 });
  assert.deepEqual(duration(125 * 60000)[2], { hours: 2, minutes: 5 });
  const rows = historyRows([
    { since: new Date(2026, 10, 15, 9, 12).toISOString(), end: new Date(2026, 10, 15, 9, 46).toISOString(), cause: 'connect' },
    { since: new Date(2026, 10, 15, 8, 0).toISOString(), end: new Date(2026, 10, 15, 8, 5).toISOString(), cause: 'dnsNotFound', stopped: true },
    { since: new Date(2026, 10, 15, 9, 50).toISOString(), cause: 'tlsCertificate' },
  ], now);
  assert.equal(rows[0].text[0], 'reachWatch.historyRow');
  assert.deepEqual(rows[0].duration[2], { count: 34 });
  assert.equal(rows[1].text[0], 'reachWatch.historyStopped');
  assert.equal(rows[2].text[0], 'reachWatch.historyOpen');
  assert.equal(historyRows(new Array(40).fill({ since: now, end: now, cause: 'connect' })).length, 20);
  assert.deepEqual(historyRows('junk'), []);
  assert.equal(clock('junk'), '');
});

test('why nothing is tested', () => {
  assert.equal(reasonText({ enabled: false })[0], 'reachWatch.off');
  assert.equal(reasonText({ enabled: true, reason: 'none' })[0], 'reachWatch.reason.none');
  assert.deepEqual(reasonText({ enabled: true, reason: 'port', port: 8444 })[2], { port: 8444 });
  assert.equal(reasonText({ enabled: true, reason: '' }), null);
});

test('the settings sent are bounded', () => {
  assert.deepEqual(settingsBody(true), { enabled: true });
  assert.deepEqual(settingsBody(undefined, 15), { minutes: 15 });
  assert.deepEqual(settingsBody('yes', 1), {});
  assert.deepEqual(settingsBody(undefined, 61), {});
  assert.deepEqual(settingsBody(undefined, '5; drop'), {});
  assert.deepEqual(intervalsFor({ minMinutes: 2, maxMinutes: 60, minutes: 7 }), [2, 5, 7, 10, 15, 30, 60]);
});

test('the reachability check knows what the watch saw', () => {
  for (const key of Object.keys(OUTSIDE_CHECK_TEXT)) assert.ok(CHECK_TEXT[key], key);
  const seen = checkSentence({ id: 'outside', code: 'seen', status: 'ok', params: { at: new Date(2026, 10, 15, 9, 12).toISOString() } }, 'app');
  assert.equal(seen.key, 'mgmt.reach.check.outside.seen');
  assert.match(seen.params.at, /09.12/);
  assert.equal(checkSentence({ id: 'outside', code: 'down', status: 'problem', params: {} }, 'app').fix, '/cosmos-ui/config-general/http-server');
  assert.equal(outsideParams({ at: 'junk' }).at, 'junk');
  // seen from outside beats everything a card can say, and keeps its link
  const results = [
    { route: 'a', state: 'verified', url: 'https://a.example.org:2053', checks: [] },
    { route: 'b', state: 'reachable', url: 'https://b.example.org:2053', checks: [] },
  ];
  const got = summarize({ routeNames: ['a', 'b'], results });
  assert.equal(got.state, 'reachable');
  assert.equal(got.url, 'https://b.example.org:2053');
});

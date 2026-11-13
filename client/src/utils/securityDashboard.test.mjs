// node --test client/src/utils/securityDashboard.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  ATTACK_SIGNINS_PER_HOUR, BUSY_INCIDENTS_PER_HOUR, COUNTER_GROUPS, GROUP_ORDER, GROUP_TEXT, KIND_TEXT, LINKS,
  allTexts, banOf, bucketLabel, changeText, groupLabel, groupRepeats, kindText, shorten, statusOf, timelineSeries, zoneName,
} from './securityDashboard.js';

const source = (path) => readFileSync(new URL('../../../src/' + path, import.meta.url), 'utf8');

// the kinds the server knows: the Sec… constants and their catalogue entries
const serverKinds = () => {
  const constants = Object.fromEntries([...source('utils/fork_security.go').matchAll(/\b(Sec\w+)\s*=\s*"([a-z.]+)"/g)].map((m) => [m[1], m[2]]));
  const catalogue = source('security/kinds.go');
  const of = (cls) => [...catalogue.matchAll(new RegExp('\\b' + cls + '\\(utils\\.(Sec\\w+)', 'g'))].map((m) => constants[m[1]]);
  return { constants, incidents: of('incident'), audit: of('audit') };
};

test('every incident kind the server knows has a sentence', () => {
  const { incidents, audit } = serverKinds();
  assert.ok(incidents.length > 20, 'found the incident kinds in kinds.go');
  assert.ok(audit.length > 20, 'found the audit kinds in kinds.go');
  for (const kind of incidents) {
    assert.ok(kind, 'a kind without a constant');
    const entry = KIND_TEXT[kind];
    assert.ok(entry, 'no sentence for ' + kind);
    assert.ok(entry.label && entry.what && entry.todo, 'incomplete sentence for ' + kind);
    assert.ok(['yes', 'no', 'na'].includes(entry.stopped), 'stopped of ' + kind);
    assert.ok(!entry.link || LINKS[entry.link], 'unknown link of ' + kind);
  }
  // and nothing for kinds that do not exist or are not incidents
  for (const kind of Object.keys(KIND_TEXT)) {
    assert.ok(incidents.includes(kind), kind + ' is not an incident kind of the server');
  }
});

test('every group the server defines has a label, in the order the page uses', () => {
  const { constants } = serverKinds();
  const go = source('security/groups.go');
  const ids = [...go.matchAll(/^\t\{"(\w+)", \[\]string\{/gm)].map((m) => m[1]);
  ids.push(...[...go.matchAll(/^\t\{GroupOther, /gm)].map(() => 'other'));
  assert.deepEqual([...ids].sort(), [...GROUP_ORDER].sort());
  for (const id of ids) assert.ok(GROUP_TEXT[id], 'no label for group ' + id);
  for (const id of COUNTER_GROUPS) assert.ok(GROUP_ORDER.includes(id));
  assert.ok(!COUNTER_GROUPS.includes('other'));
  assert.ok(Object.keys(constants).length > 40);
});

test('every text has its key in the English translation file, with the same text', () => {
  const en = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));
  for (const [key, text] of Object.entries(allTexts())) {
    assert.equal(en[key], text, 'translation.json: ' + key);
  }
  // the status sentences
  const statuses = [
    {}, { day: { total: 1 } }, { day: { total: 5 } },
    { recent: { total: 3, addresses: 1, groups: { ban: { total: 1 } } } },
    { recent: { total: 3, addresses: 2, groups: { ban: { total: 1 } } } },
    { recent: { groups: { login: { total: 30, addresses: 1 } } } },
    { recent: { groups: { login: { total: 30, addresses: 2 } } } },
    { day: { kinds: { 'auth.mfa.locked': 1 } } }, { day: { kinds: { 'access.grant.denied': 1 } } },
  ].map(statusOf);
  for (const s of statuses) assert.equal(en[s.key], s.text, 'translation.json: ' + s.key);
});

test('kindText: the page\'s sentence, or the server\'s words for a kind it does not know', () => {
  const known = kindText('scan.probe');
  assert.equal(known.label[0], 'security.kind.scan.probe.label');
  assert.equal(known.stopped, 'yes');
  assert.equal(kindText('shield.strike').link, '/cosmos-ui/config-url/shield');
  const unknown = kindText('new.kind', [{ kind: 'new.kind', label: 'New thing', description: 'Something new.' }]);
  assert.deepEqual(unknown.label, ['security.kind.unknown.label', 'New thing']);
  assert.equal(unknown.todo, null);
  assert.equal(kindText('other.kind').label[1], 'other.kind');
  assert.deepEqual(groupLabel('nope'), ['security.group.unknown', 'Other']);
});

// ---- status ----

const overview = ({ recent = {}, day = {} } = {}) => ({
  recent: { total: 0, addresses: 0, groups: {}, kinds: {}, ...recent },
  day: { total: 0, addresses: 0, groups: {}, kinds: {}, ...day },
});

test('status: a fresh install is quiet', () => {
  for (const o of [undefined, null, {}, overview()]) {
    const s = statusOf(o);
    assert.equal(s.level, 'quiet');
    assert.equal(s.severity, 'success');
    assert.equal(s.key, 'security.status.quietNone');
  }
});

test('status: quiet counts the day', () => {
  assert.equal(statusOf(overview({ day: { total: 3 } })).params.count, 3);
  assert.equal(statusOf(overview({ day: { total: 3 } })).key, 'security.status.quiet');
  assert.equal(statusOf(overview({ day: { total: 1 } })).key, 'security.status.quietOne');
});

test('status: under attack from the threshold of failed sign-ins in the last hour', () => {
  const below = overview({ recent: { total: ATTACK_SIGNINS_PER_HOUR - 1, addresses: 1, groups: { login: { total: ATTACK_SIGNINS_PER_HOUR - 1, addresses: 1 } } } });
  assert.equal(statusOf(below).level, 'quiet');
  const at = overview({ recent: { total: 412, addresses: 9, groups: { login: { total: 412, addresses: 9 } } } });
  const s = statusOf(at);
  assert.equal(s.level, 'attack');
  assert.equal(s.severity, 'error');
  assert.deepEqual(s.params, { count: 412, addresses: 9 });
  assert.equal(s.text, 'Under attack: {{count}} failed sign-ins from {{addresses}} addresses in the last hour.');
  const one = overview({ recent: { groups: { login: { total: ATTACK_SIGNINS_PER_HOUR, addresses: 1 } } } });
  assert.equal(statusOf(one).key, 'security.status.attackOne');
});

test('status: busy on a ban or lock-out in the last hour, or many incidents', () => {
  assert.equal(statusOf(overview({ recent: { total: 1, addresses: 1, groups: { ban: { total: 1, addresses: 1 } } } })).level, 'busy');
  assert.equal(statusOf(overview({ recent: { total: 1, addresses: 1, groups: { lockout: { total: 1, addresses: 1 } } } })).level, 'busy');
  const scans = overview({ recent: { total: BUSY_INCIDENTS_PER_HOUR, addresses: 40, groups: { scan: { total: BUSY_INCIDENTS_PER_HOUR, addresses: 40 } } } });
  const s = statusOf(scans);
  assert.equal(s.level, 'busy');
  assert.equal(s.severity, 'warning');
  assert.deepEqual(s.params, { count: BUSY_INCIDENTS_PER_HOUR, addresses: 40 });
  // scans of the IP address alone never sound like an attack
  const many = overview({ recent: { total: 100000, addresses: 3000, groups: { scan: { total: 100000, addresses: 3000 } } } });
  assert.equal(statusOf(many).level, 'busy');
  // a few scans in the hour are quiet
  assert.equal(statusOf(overview({ recent: { total: BUSY_INCIDENTS_PER_HOUR - 1, groups: { scan: { total: BUSY_INCIDENTS_PER_HOUR - 1 } } }, day: { total: 99 } })).level, 'quiet');
});

test('status: someone with a password comes first', () => {
  const attack = { total: 500, addresses: 2, groups: { login: { total: 500, addresses: 2 } } };
  const s = statusOf(overview({ recent: attack, day: { kinds: { 'auth.mfa.locked': 1 } } }));
  assert.equal(s.level, 'attention');
  assert.equal(s.key, 'security.status.attentionLocked');
  assert.equal(statusOf(overview({ day: { kinds: { 'access.grant.denied': 2 } } })).key, 'security.status.attentionGrant');
  // wrong numbers from a broken answer do not count
  assert.equal(statusOf(overview({ day: { kinds: { 'auth.mfa.locked': -1 }, total: 'x' } })).level, 'quiet');
});

// ---- periods ----

test('changeText: against the period before', () => {
  assert.deepEqual(changeText(0, 0, '24h'), { direction: 'same', key: 'security.change.same.24h', text: 'Same as the 24 hours before', params: {} });
  assert.equal(changeText(5, 0, '7d').key, 'security.change.new.7d');
  assert.deepEqual(changeText(12, 4, '30d').params, { count: 8 });
  assert.equal(changeText(12, 4, '30d').direction, 'up');
  assert.deepEqual(changeText(1, 4, '24h').params, { count: 3 });
  assert.equal(changeText(1, 4, '24h').direction, 'down');
  assert.equal(changeText(1, 4, 'bogus').key, 'security.change.fewer.24h');
});

test('bucketLabel: hours, weekday and hour, days, in the zone asked for', () => {
  const start = '2026-11-13T09:00:00Z';
  assert.equal(bucketLabel(start, 3600, { locale: 'en-GB', timeZone: 'Europe/Berlin' }), '10:00');
  assert.equal(bucketLabel(start, 3600, { locale: 'en-GB', timeZone: 'UTC' }), '09:00');
  assert.equal(bucketLabel(start, 6 * 3600, { locale: 'en-GB', timeZone: 'UTC' }), 'Fri 09:00');
  assert.equal(bucketLabel('2026-11-12T23:00:00Z', 86400, { locale: 'en-GB', timeZone: 'Europe/Berlin' }), '13 Nov');
  assert.equal(bucketLabel('not a date', 3600), '');
});

test('zoneName: the zone, or the offset', () => {
  assert.equal(zoneName('Europe/Berlin'), 'Europe/Berlin');
  assert.ok(zoneName().length > 0);
});

test('timelineSeries: one series per group that has events, in order', () => {
  const buckets = [
    { start: 'a', groups: { login: 2, scan: 0, other: 1 } },
    { start: 'b', groups: { login: 0, scan: 3, other: 0 } },
  ];
  assert.deepEqual(timelineSeries(buckets), [
    { id: 'login', data: [2, 0] },
    { id: 'scan', data: [0, 3] },
    { id: 'other', data: [1, 0] },
  ]);
  assert.deepEqual(timelineSeries([]), []);
  assert.deepEqual(timelineSeries(null), []);
});

// ---- list ----

test('groupRepeats: adjacent events of one kind, address and user make one row', () => {
  const e = (id, kind, ip, extra = {}) => ({ id, kind, ip, actor: '', time: 't' + id, severity: 'medium', count: 1, ...extra });
  const rows = groupRepeats([
    e(9, 'scan.probe', '198.51.100.20', { path: '/.env' }),
    e(8, 'scan.probe', '198.51.100.20', { path: '/wp-login.php' }),
    e(7, 'auth.login.failed', '203.0.113.7', { count: 33, details: { summary: '1' } }),
    e(6, 'auth.login.failed', '203.0.113.7'),
    e(5, 'auth.login.failed', '203.0.113.8'),
    e(4, 'scan.probe', '198.51.100.20'),
    e(3, 'access.denied', '192.0.2.11', { actor: 'bob' }),
    e(2, 'access.denied', '192.0.2.11', { actor: 'carol' }),
    null,
  ]);
  assert.deepEqual(rows.map((r) => [r.key, r.kind, r.events.length, r.count]), [
    ['9', 'scan.probe', 2, 2],
    ['7', 'auth.login.failed', 2, 34],
    ['5', 'auth.login.failed', 1, 1],
    ['4', 'scan.probe', 1, 1],
    ['3', 'access.denied', 1, 1],
    ['2', 'access.denied', 1, 1],
  ]);
  assert.equal(rows[0].last, 't9');
  assert.equal(rows[0].first, 't8');
  assert.deepEqual(groupRepeats(undefined), []);
});

test('shorten: long text is cut, the full text kept', () => {
  assert.deepEqual(shorten('short'), { text: 'short', full: 'short', cut: false });
  const long = shorten('x'.repeat(100), 10);
  assert.equal(Array.from(long.text).length, 10);
  assert.ok(long.text.endsWith('…'));
  assert.equal(long.full.length, 100);
  // characters, not UTF-16 halves
  assert.equal(shorten('😀'.repeat(20), 5).text, '😀😀😀😀…');
  assert.equal(shorten(null).text, '');
});

test('banOf: blocked now or not', () => {
  const bans = [
    { clientID: '203.0.113.7', status: 'temp', blockedUntil: '2026-11-13T12:00:00Z' },
    { clientID: '198.51.100.20', status: 'clear' },
    { clientID: '192.0.2.1', status: 'perm', blockedUntil: '2026-11-13T12:00:00Z' },
  ];
  assert.deepEqual(banOf(bans, '203.0.113.7'), { status: 'temp', until: '2026-11-13T12:00:00Z', clientID: '203.0.113.7' });
  assert.equal(banOf(bans, '198.51.100.20'), null);
  assert.equal(banOf(bans, '192.0.2.1').until, null);
  assert.equal(banOf(null, '1.2.3.4'), null);
  assert.equal(banOf(bans, ''), null);
});

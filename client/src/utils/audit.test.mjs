// node --test client/src/utils/audit.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { KIND_GROUPS, auditSentence, fill, isProblem, kindInGroup, knownKinds, templates } from './auditSentences.js';
import { DEFAULT_FILTERS, apiQuery, cleanFilters, filtersFromSearch, formatTime, searchFromFilters, userLink, zoneName } from './auditFilters.js';
import { EXPORT_MAX_ROWS, auditCsv, csvCell, exportFileName } from './auditCsv.js';

// The kinds the server records: the Sec… constants of src/utils/fork_security.go,
// which is what GET /cosmos/api/security/kinds serves (the catalogue has one
// entry per constant, a Go test makes sure of it).
function serverKinds() {
  const go = readFileSync(new URL('../../../src/utils/fork_security.go', import.meta.url), 'utf8');
  const kinds = [...go.matchAll(/^\s*Sec[A-Za-z0-9]+\s*=\s*"([a-z.]+)"/gm)].map((m) => m[1]);
  assert.ok(kinds.length >= 50, 'found only ' + kinds.length + ' kinds in fork_security.go');
  return kinds;
}

test('every kind the server records has a sentence', () => {
  const known = new Set(knownKinds());
  const missing = serverKinds().filter((kind) => !known.has(kind));
  assert.deepEqual(missing, [], 'kinds without a sentence in auditSentences.js');
  const stale = knownKinds().filter((kind) => !serverKinds().includes(kind));
  assert.deepEqual(stale, [], 'sentences for kinds the server no longer has');
});

// kinds that only strangers produce never reach the trail; every other one
// is in a filter group
const STRANGERS_ONLY = ['auth.session.forged', 'auth.session.rejected', 'auth.reset.rejected', 'token.invalid', 'token.expired',
  'token.ip.denied', 'shield.blocked', 'shield.strike', 'shield.ban.temporary', 'shield.ban.permanent', 'shield.geo.blocked',
  'shield.bot.blocked', 'shield.hostname.unknown', 'shield.ratelimited', 'scan.probe', 'recorder.dropped', 'firewall.ban'];

test('every kind of the trail is in a filter group', () => {
  const ungrouped = serverKinds().filter((kind) => !STRANGERS_ONLY.includes(kind) && !KIND_GROUPS.some((g) => kindInGroup(kind, g)));
  assert.deepEqual(ungrouped, []);
  for (const group of KIND_GROUPS) {
    assert.ok(group.kinds.length <= 20, group.id + ': the server takes at most 20 kinds');
    for (const entry of group.kinds) assert.match(entry, /^[a-z]+(\.[a-z]+)*(\.\*)?$/, entry);
  }
});

test('every template is in the English translations, with the same text', () => {
  const en = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));
  for (const [key, text] of templates()) {
    assert.equal(en[key], text, key);
  }
});

test('sentences: the examples', () => {
  const cases = [
    [{ kind: 'config.saved', actor: 'admin', details: { sections: 'HTTP' } }, 'admin changed the settings (sections: HTTP)'],
    [{ kind: 'auth.login.success', actor: 'maria', ip: '192.168.1.20', details: { mfa: 'none' } }, 'maria logged in from 192.168.1.20'],
    [{ kind: 'auth.login.success', actor: 'maria', ip: '192.168.1.20', details: { mfa: 'pending' } }, 'maria logged in from 192.168.1.20; the two-factor code was still to come'],
    [{ kind: 'auth.login.failed', target: 'maria', ip: '203.0.113.9', outcome: 'failed', details: { reason: 'wrong_password' } }, 'a wrong password for maria from 203.0.113.9'],
    [{ kind: 'auth.login.failed', ip: '203.0.113.9', outcome: 'failed', count: 33, details: { summary: '1' } }, '33 more failed logins from 203.0.113.9, summed up without the names tried'],
    [{ kind: 'auth.login.failed', outcome: 'failed' }, 'a failed login'],
    [{ kind: 'user.deleted', actor: 'paul', target: 'paul', details: { self: 'yes' } }, 'paul deleted their own account'],
    [{ kind: 'container.action', actor: 'admin', target: 'jellyfin', details: { action: 'stop' } }, 'admin stopped the container jellyfin'],
    [{ kind: 'container.action', actor: 'admin', target: 'jellyfin', details: { action: 'wobble' } }, 'admin ran "wobble" on the container jellyfin'],
    [{ kind: 'access.denied', actor: 'maria', outcome: 'denied', details: { permission: 'Configuration Read', token: 'phone' } }, 'maria was refused: no permission "Configuration Read", with the API token phone'],
    [{ kind: 'route.created', actor: 'admin', target: 'app', outcome: 'failed' }, 'admin created the URL app: it failed'],
    [{ kind: 'route.deleted', actor: 'admin', target: 'app', outcome: 'denied' }, 'admin deleted the URL app: refused'],
    [{ kind: 'user.mfa.reset', target: 'maria' }, 'someone reset the two-factor of maria'],
    [{ kind: 'token.created', actor: 'admin' }, 'admin created the API token (no name)'],
    [{ kind: 'api.change', actor: 'admin', method: 'POST', path: '/cosmos/api/cron' }, 'admin sent POST /cosmos/api/cron'],
    [{ kind: 'some.future.kind', actor: 'admin' }, 'admin: some.future.kind'],
  ];
  for (const [event, want] of cases) assert.equal(auditSentence(event), want, event.kind);
  assert.equal(auditSentence(null), '');
});

test('sentences: values are text, never templates', () => {
  const got = auditSentence({ kind: 'route.created', actor: '{{ip}}', target: '<img src=x onerror=alert(1)>', ip: '10.0.0.1' });
  assert.equal(got, '{{ip}} created the URL <img src=x onerror=alert(1)> from 10.0.0.1');
  assert.equal(fill('{{a}}{{b}}', { a: '{{b}}', b: 'x' }), '{{b}}x');
  // a detail called like a field does not replace the field
  assert.equal(auditSentence({ kind: 'auth.logout', actor: 'maria', details: { actor: 'admin' } }), 'maria logged out');
});

test('sentences: a translation is used', () => {
  const t = (key, fallback) => (key === 'securityAudit.sentence.auth.logout' ? '{{actor}} s\'est déconnecté' : fallback);
  assert.equal(auditSentence({ kind: 'auth.logout', actor: 'maria', ip: '10.0.0.1' }, t), 'maria s\'est déconnecté from 10.0.0.1');
});

test('isProblem marks failures and refusals', () => {
  assert.equal(isProblem({ outcome: 'ok' }), false);
  assert.equal(isProblem({ outcome: 'failed' }), true);
  assert.equal(isProblem({ outcome: 'denied' }), true);
});

test('filters: from the address and back', () => {
  const f = filtersFromSearch('?user=maria&period=7d&outcome=refused&group=logins&ip=203.0.113.9&q=wrong');
  assert.deepEqual(f, { user: 'maria', group: 'logins', outcome: 'refused', period: '7d', ip: '203.0.113.9', q: 'wrong' });
  assert.equal(searchFromFilters(f), '?user=maria&group=logins&outcome=refused&period=7d&ip=203.0.113.9&q=wrong');
  assert.equal(searchFromFilters(DEFAULT_FILTERS), '');
  assert.equal(userLink('maria'), '/cosmos-ui/security-audit?user=maria');
  assert.equal(userLink('a&b'), '/cosmos-ui/security-audit');
});

test('filters: what a hand-made address holds is checked', () => {
  const f = filtersFromSearch('?user=' + encodeURIComponent('<script>') + '&period=forever&outcome=x&group=__proto__&ip=' +
    encodeURIComponent('1.2.3.4\nkind=x') + '&q=' + 'a'.repeat(500));
  assert.deepEqual({ ...f, q: '' }, { ...DEFAULT_FILTERS });
  assert.equal(f.q.length, 64);
  assert.deepEqual(cleanFilters(null), DEFAULT_FILTERS);
  assert.equal(filtersFromSearch('?user=' + 'a'.repeat(100)).user, 'a'.repeat(64));
});

test('filters: the query of the API', () => {
  const now = new Date('2026-11-13T12:00:00.250Z');
  assert.equal(apiQuery({ user: 'maria', period: '24h', outcome: 'refused', group: 'tokens' }, now),
    'user=maria&kind=token.*&outcome=denied%2Cblocked&from=2026-11-12T12%3A00%3A00Z');
  assert.equal(apiQuery({ period: '7d' }, now), 'from=2026-11-06T12%3A00%3A00Z');
  assert.equal(apiQuery({ q: 'a b', ip: '::1' }, now), 'ip=%3A%3A1&q=a+b');
  assert.equal(apiQuery({}, now), '');
});

test('times: in the browser zone, the zone named', () => {
  assert.ok(zoneName().length > 0);
  assert.equal(formatTime(''), '');
  assert.equal(formatTime('0001-01-01T00:00:00Z'), '');
  assert.equal(formatTime('nonsense'), '');
  assert.ok(formatTime('2026-11-13T10:00:00Z', 'en-GB').includes('2026'));
});

test('csv: quoting', () => {
  assert.equal(csvCell('plain'), '"plain"');
  assert.equal(csvCell('a "quoted" word'), '"a ""quoted"" word"');
  assert.equal(csvCell('a,b'), '"a,b"');
  assert.equal(csvCell('line\nbreak'), '"line\nbreak"');
  assert.equal(csvCell(null), '""');
  assert.equal(csvCell(3), '"3"');
});

test('csv: a cell a spreadsheet would run is neutralised', () => {
  for (const start of ['=', '+', '-', '@', '\t', '\r', '\n', '＝', '＋', '－', '＠']) {
    const value = start + 'HYPERLINK("http://example.invalid","x")';
    const cell = csvCell(value);
    assert.ok(cell.startsWith('"\''), JSON.stringify(start) + ' → ' + cell);
  }
  assert.equal(csvCell('=1+1'), '"\'=1+1"');
  assert.equal(csvCell('a=1'), '"a=1"');
});

test('csv: the file', () => {
  const rows = [
    { id: 7, time: '2026-11-13T10:00:00Z', kind: 'route.created', actor: 'admin', target: '=cmd|"/c calc"!A1', outcome: 'ok', ip: '10.0.0.1', count: 1, details: { b: '2', a: '-1' } },
  ];
  const csv = auditCsv(rows, { sentence: (r) => auditSentence(r), formatTime: () => 'local', zone: 'Europe/Berlin' });
  assert.ok(csv.startsWith('﻿"time_utc","time_local"'));
  const lines = csv.slice(1).split('\r\n');
  assert.equal(lines.length, 3);
  assert.equal(lines[2], '');
  assert.equal(lines[1],
    '"2026-11-13T10:00:00Z","local","Europe/Berlin","admin created the URL =cmd|""/c calc""!A1 from 10.0.0.1","route.created","admin",' +
    '"\'=cmd|""/c calc""!A1","ok","10.0.0.1","","","","1","a=-1; b=2","7"');
  const many = Array.from({ length: EXPORT_MAX_ROWS + 50 }, (_, i) => ({ id: i, kind: 'auth.logout' }));
  assert.equal(auditCsv(many).split('\r\n').length, EXPORT_MAX_ROWS + 2);
  assert.equal(exportFileName(new Date('2026-11-13T10:00:00Z')), 'cosmos-audit-2026-11-13.csv');
});

// Fork addition: tests of notifications.js. Run with: node --test "client/src/utils/*.test.mjs"
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import {
  TEXT, normalizeLevel, sentence, fill, cleanVars, titleOf, messageOf, safeLink, detailsLink, cleanFilters, filtersFromSearch,
  searchFromFilters, apiParams, markBody, deleteBody, markAllBody, deleteReadBody, cleanItem, cleanItems, mergePage, ordinal, occurrence,
  timeline, tableOf, auditLink, allTexts, commandLabel, knownAction,
} from './notifications.js';

const translations = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));

test('every text of the notifications page is in translation.json, with the same English', () => {
  for (const [key, text] of allTexts()) {
    assert.equal(translations[key], text, `translation.json: ${key}`);
  }
});

test('every key the notification pages and the bell write out is in the module or in translation.json', () => {
  const dir = new URL('../pages/notifications/', import.meta.url);
  const files = readdirSync(dir).map((f) => readFileSync(new URL(f, dir), 'utf8'));
  files.push(readFileSync(new URL('../layout/MainLayout/Header/HeaderContent/Notification.jsx', import.meta.url), 'utf8'));
  for (const source of files) {
    for (const [, key] of source.matchAll(/\bt\('([\w.-]+)'/g)) {
      assert.ok(translations[key] !== undefined, `translation.json lacks ${key}`);
    }
    assert.ok(!/dangerouslySetInnerHTML|\beval\(|new Function/.test(source), 'no markup from data, no eval');
  }
});

test('levels from every writer become one of four', () => {
  assert.equal(normalizeLevel('warn'), 'warning');
  assert.equal(normalizeLevel('warning'), 'warning');
  assert.equal(normalizeLevel('ERROR'), 'error');
  assert.equal(normalizeLevel('success'), 'success');
  for (const other of ['info', 'important', '', null, undefined, 42]) assert.equal(normalizeLevel(other), 'info');
});

test('a sentence uses the translation, the English, or the key, and never markup tricks', () => {
  const t = (key, def, vars) => (key === 'known' ? 'Translated {{port}}'.replace('{{port}}', vars.port) : def);
  assert.equal(sentence({ key: 'known', text: 'Port {{port}}', vars: { port: '2053' } }, t), 'Translated 2053');
  assert.equal(sentence({ key: 'unknown', text: 'Port {{port}}', vars: { port: '2053' } }, (k, d) => fill(d, { port: '2053' })), 'Port 2053');
  assert.equal(sentence({ key: '', text: 'free text: EOF' }, t), 'free text: EOF');
  assert.equal(sentence({ key: 'only.a.key' }), 'only.a.key');
  assert.equal(sentence({ key: 'k', text: 'A {{x}}', vars: { x: '<img src=x onerror=alert(1)>' } }), 'A <img src=x onerror=alert(1)>');
  assert.equal(sentence(null), '');
  assert.deepEqual(cleanVars({ a: 'x', b: 2, c: { evil: 1 }, 'bad key!': 'y' }), { a: 'x', b: '2' });
  assert.equal(fill('{{a}} and {{missing}}', { a: '1' }), '1 and ');
  // i18next's own escaping is switched off (React escapes): passed every time
  let seen;
  sentence({ key: 'k', text: 'x' }, (k, d, o) => { seen = o; return d; });
  assert.deepEqual(seen.interpolation, { escapeValue: false });
});

test('old rows show their title key and their message with Vars; server errors their text', () => {
  const t = (key, def, vars) => ({ 'header.notification.title.upnpFailed': 'Router port forwarding failed', 'm': 'Failed: {{Vars}}' }[key] || def).replace('{{Vars}}', (vars && vars.Vars) || '');
  const old = { title: { key: 'header.notification.title.upnpFailed', text: '' }, message: { key: 'm', text: '', vars: { Vars: 'HTTPS: EOF' } } };
  assert.equal(titleOf(old, t), 'Router port forwarding failed');
  assert.equal(messageOf(old, t), 'Failed: HTTPS: EOF');
  assert.equal(messageOf({ message: { key: '', text: 'Backup: Error reading directory : permission denied' } }, t), 'Backup: Error reading directory : permission denied');
});

test('links: only the panel\'s own pages', () => {
  assert.equal(safeLink('/cosmos-ui/servapps/containers/jellyfin'), '/cosmos-ui/servapps/containers/jellyfin');
  assert.equal(safeLink('/cosmos-ui/config-general/http-server'), '/cosmos-ui/config-general/http-server');
  for (const bad of ['https://example.org/', 'javascript:alert(1)', '//example.org/cosmos-ui/', '/cosmos-ui//example.org', '/cosmos-ui/../cosmos/api/restart',
    '/cosmos-ui/a\\b', '/cosmos-ui/a\nb', '/cosmos/api/notifications', '', null, 7]) {
    assert.equal(safeLink(bad), '', String(bad));
  }
  assert.equal(detailsLink({ id: 12 }), '/cosmos-ui/notifications/12');
  assert.equal(detailsLink({ id: '12; x' }), '');
});

test('filters: checked from the address, written in a fixed order, bounded', () => {
  assert.deepEqual(filtersFromSearch('?level=error&source=upnp&read=unread&q=2053'), { level: 'error', source: 'upnp', read: 'unread', q: '2053' });
  assert.deepEqual(filtersFromSearch('?level=evil&source=nope&read=maybe'), { level: '', source: '', read: '', q: '' });
  assert.equal(cleanFilters({ q: 'x'.repeat(500) }).q.length, 100);
  assert.equal(cleanFilters({ q: 'a\nb' }).q, 'a b');
  assert.equal(searchFromFilters({ q: 'port', level: 'warning' }), '?level=warning&q=port');
  assert.equal(searchFromFilters({}), '');
  assert.deepEqual(apiParams({ level: 'error' }, '1731650000000:42', 500), { level: 'error', cursor: '1731650000000:42', limit: 100 });
  assert.deepEqual(apiParams({}, 'drop table', 0), { limit: 30 });
});

test('command bodies: numbers only, bounded', () => {
  assert.deepEqual(markBody([1, 2, 'x', -3, 4.5], true), { ids: [1, 2], read: true });
  assert.deepEqual(markBody(7, false), { ids: [7], read: false });
  assert.equal(markBody(Array.from({ length: 500 }, (_, i) => i + 1), true).ids.length, 200);
  assert.deepEqual(deleteBody([9]), { ids: [9] });
  assert.deepEqual(markAllBody(), { all: true });
  assert.deepEqual(deleteReadBody(), { read: true });
});

test('items are completed and checked: levels, times, counts, subject links', () => {
  const item = cleanItem({ id: 5, ref: 'ab12cd34ef56ab78', level: 'warn', date: '2026-11-15T08:00:00Z', count: 0, source: 'nope',
    subject: { type: 'port', name: 'HTTPS 2053/TCP', link: 'https://evil.example/' }, link: 'javascript:x' });
  assert.equal(item.level, 'warning');
  assert.equal(item.count, 1);
  assert.equal(item.source, 'other');
  assert.equal(item.subject.link, '');
  assert.equal(item.link, '');
  assert.equal(item.firstSeen, null);
  assert.equal(cleanItem({ id: 'x' }), null);
  assert.equal(cleanItem({ id: 3, ref: '"><script>' }).ref, '');
  assert.equal(cleanItems([{ id: 1 }, null, { id: 0 }]).length, 1);
  assert.deepEqual(mergePage([{ id: 1 }], [{ id: 1 }, { id: 2 }]).map((n) => n.id), [1, 2]);
});

test('occurrences: "the 4th time since …"', () => {
  assert.equal(ordinal(1), '1st');
  assert.equal(ordinal(2), '2nd');
  assert.equal(ordinal(3), '3rd');
  assert.equal(ordinal(4), '4th');
  assert.equal(ordinal(11), '11th');
  assert.equal(ordinal(12), '12th');
  assert.equal(ordinal(22), '22nd');
  assert.equal(ordinal(103), '103rd');
  assert.equal(occurrence({ count: 1 }), null);
  assert.deepEqual(occurrence({ count: 4 }, 'yesterday'), [TEXT.times[0], TEXT.times[1], { count: 4, since: 'yesterday' }]);
});

test('the timeline: occurrences and the notifications of the same thread, newest first, without doubles', () => {
  const item = { id: 1, date: '2026-11-15T09:00:00Z', level: 'error' };
  const details = {
    seen: ['2026-11-15T07:00:00Z', '2026-11-15T09:00:00Z', '2026-11-15T08:00:00Z', '2026-11-15T08:00:00Z', 'nonsense'],
    thread: [{ id: 1, date: '2026-11-15T09:00:00Z' }, { id: 2, level: 'success', date: '2026-11-15T09:30:00Z' }],
  };
  const entries = timeline(item, details);
  assert.deepEqual(entries.map((e) => e.type), ['resolved', 'occurrence', 'occurrence', 'occurrence']);
  assert.equal(entries[0].item.id, 2);
  assert.equal(timeline(item, {}).length, 1);
});

test('the state table keeps text only, as wide as its header', () => {
  const table = tableOf({ header: [{ key: 'a', text: 'Port' }, { key: 'b', text: 'To' }], rows: [{ cells: ['2053', '192.168.1.20:443', 'extra'] }, { cells: [{ x: 1 }] }, null] });
  assert.equal(table.rows[0].length, 2);
  assert.deepEqual(table.rows[2], ['', '']);
  assert.equal(tableOf({}), null);
});

test('the audit link carries the object as a search, bounded', () => {
  assert.equal(auditLink('Cosmos HTTPS'), '/cosmos-ui/security-audit?q=Cosmos+HTTPS');
  assert.equal(auditLink(''), '/cosmos-ui/security-audit');
  assert.ok(auditLink('x'.repeat(300)).length < 100);
});

test('toasts: marking all and deleting are named; marking named ones is not', () => {
  assert.deepEqual(commandLabel('/api/notifications/mark', 'POST', '{"all":true}'), ['activity.notifications.markAll', 'Mark every notification as read']);
  assert.equal(commandLabel('/api/notifications/mark', 'POST', '{"ids":[1],"read":true}'), null);
  assert.deepEqual(commandLabel('/api/notifications/delete', 'POST', '{"read":true}')[0], 'activity.notifications.deleteRead');
  assert.deepEqual(commandLabel('/api/notifications/delete', 'POST', '{"ids":[3]}')[0], 'activity.notifications.delete');
  assert.equal(commandLabel('/api/notifications/delete', 'GET', ''), null);
  assert.equal(commandLabel('/api/notifications/mark', 'POST', 'not json'), null);
  assert.ok(knownAction('upnp.check'));
  assert.ok(!knownAction('restart'));
});

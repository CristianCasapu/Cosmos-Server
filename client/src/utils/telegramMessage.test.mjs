import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  DEFAULT_DELIVERY, SEVERITIES, deliveryOf, flipKind, kindConfig, kindDefault, kindOn, kindValues, kindsOf,
  messageParts, previewLine, whyNotSent,
} from './telegramMessage.js';

test('a message is cut into its parts by UTF-16 offsets', () => {
  const text = '🔴 Critical · Disk /mnt/data nearly full\n\nError text:\nno space';
  const start = text.indexOf('/mnt/data');
  const raw = text.indexOf('no space');
  const parts = messageParts(text, [{ type: 'pre', offset: raw, length: 8 }, { type: 'code', offset: start, length: 9 }]);
  assert.deepEqual(parts.map((p) => p.type), ['plain', 'code', 'plain', 'pre']);
  assert.equal(parts[1].text, '/mnt/data');
  assert.equal(parts[3].text, 'no space');
  assert.equal(parts.map((p) => p.text).join(''), text);
});

test('entities that do not fit are ignored, never trusted', () => {
  const text = 'abc def';
  for (const entities of [
    [{ type: 'code', offset: 5, length: 10 }],
    [{ type: 'code', offset: -1, length: 2 }],
    [{ type: 'text_link', offset: 0, length: 3, url: 'https://evil.example' }],
    [{ type: 'code', offset: 0, length: 4 }, { type: 'code', offset: 2, length: 3 }],
    null,
  ]) {
    const parts = messageParts(text, entities);
    assert.equal(parts.map((p) => p.text).join(''), text);
    assert.ok(parts.every((p) => ['plain', 'code', 'pre'].includes(p.type)));
  }
});

test('the preview is the first line, cut like a phone', () => {
  assert.equal(previewLine('🟡 Warning · Failed sign-in as admin\n\nmore'), '🟡 Warning · Failed sign-in as admin');
  const long = '🟠 Error · ' + 'x'.repeat(100);
  const shown = previewLine(long, 40);
  assert.equal(Array.from(shown).length, 40);
  assert.ok(shown.endsWith('…'));
});

const failed = { kind: 'auth.login.failed', severity: 'warn', category: 'security', record: 'medium' };
const locked = { kind: 'auth.mfa.locked', severity: 'critical', category: 'security', record: 'high' };
const dying = { kind: 'container.dying', severity: 'error', category: 'apps' };
const moved = { kind: 'telegram.moved', severity: 'critical', category: 'security', own: true };
const base = { Telegram_Cat_security: true, Telegram_Cat_apps: true, Telegram_MinSeverity: 'high', Telegram_Kinds: {} };

test('a kind follows its group and the minimum severity until switched', () => {
  assert.equal(kindDefault(locked, base), true);
  assert.equal(kindDefault(failed, base), false);
  assert.equal(kindDefault(failed, { ...base, Telegram_MinSeverity: 'medium' }), true);
  assert.equal(kindDefault(dying, { ...base, Telegram_Cat_apps: false }), false);
  assert.equal(kindDefault(moved, { ...base, Telegram_Cat_security: false }), true);
});

test('flipping a switch keeps only what differs from the default', () => {
  let kinds = flipKind(failed, base);
  assert.deepEqual(kinds, { 'auth.login.failed': true });
  assert.equal(kindOn(failed, { ...base, Telegram_Kinds: kinds }), true);
  kinds = flipKind(failed, { ...base, Telegram_Kinds: kinds });
  assert.deepEqual(kinds, {});
  kinds = flipKind(dying, base);
  assert.deepEqual(kinds, { 'container.dying': false });
});

test('the deliveries: defaults, what is saved, what is read back', () => {
  const values = kindValues({});
  SEVERITIES.forEach((s) => assert.equal(deliveryOf(s, values), DEFAULT_DELIVERY[s]));
  assert.deepEqual(kindConfig(values), { Kinds: {}, Delivery: {} });

  const changed = { ...values, Telegram_Delivery_info: 'off', Telegram_Delivery_warn: 'silent', Telegram_Kinds: { b: true, a: false, junk: 'yes' } };
  const saved = kindConfig(changed);
  assert.deepEqual(saved, { Kinds: { a: false, b: true }, Delivery: { info: 'off', warn: 'silent' } });
  assert.deepEqual(Object.keys(saved.Kinds), ['a', 'b']);
  const again = kindValues(saved);
  assert.equal(again.Telegram_Delivery_info, 'off');
  assert.equal(again.Telegram_Delivery_critical, 'sound');
  assert.deepEqual(kindValues({ Delivery: { info: 'loud' }, Kinds: { x: 1 } }).Telegram_Kinds, {});
  assert.equal(kindValues({ Delivery: { info: 'loud' } }).Telegram_Delivery_info, 'silent');
});

test('the list is grouped by severity and says why a kind is not sent', () => {
  const kinds = [locked, failed, dying];
  assert.deepEqual(kindsOf(kinds, 'warn'), [failed]);
  assert.deepEqual(kindsOf(null, 'warn'), []);
  const values = kindValues({});
  const form = { ...base, ...values };
  assert.equal(whyNotSent(locked, form), null);
  assert.match(whyNotSent(failed, form)[1], /minimum severity/);
  assert.match(whyNotSent(dying, { ...form, Telegram_Cat_apps: false })[1], /group/);
  assert.match(whyNotSent(dying, { ...form, Telegram_Kinds: { 'container.dying': false } })[1], /switched off here/);
  assert.match(whyNotSent(locked, { ...form, Telegram_Delivery_critical: 'off' })[1], /severity is switched off/);
  // a closing message is delivered like information
  assert.match(whyNotSent({ ...dying, closing: true, severity: 'info' }, { ...form, Telegram_Delivery_info: 'off' })[1], /severity/);
});

// review 5: the end of a problem of a pair is sent, silently when its
// severity is off; switched off by itself, the card says what it leaves
test('the end of a problem says how it arrives', () => {
  const form = { ...base, ...kindValues({}) };
  const restored = { kind: 'reach.restored', closes: 'reach.down', closing: true, severity: 'info', category: 'system' };
  assert.equal(whyNotSent(restored, form), null);
  assert.match(whyNotSent(restored, { ...form, Telegram_Delivery_info: 'off' })[1], /silently/);
  assert.match(whyNotSent(restored, { ...form, Telegram_Kinds: { 'reach.restored': false } })[1], /last word/);
  const gone = { kind: 'smart.gone', closes: 'smart.failing', closing: true, neutral: true, severity: 'warn', category: 'storage' };
  assert.equal(whyNotSent(gone, { ...form, Telegram_Delivery_info: 'off' }), null);
  assert.match(whyNotSent(gone, { ...form, Telegram_Delivery_warn: 'off' })[1], /silently/);
});

test('a kind that is off by default stays off until switched on', () => {
  const country = { kind: 'login.newcountry', severity: 'warn', category: 'security', off: true };
  assert.equal(kindDefault(country, base), false);
  assert.match(whyNotSent(country, { ...base, ...kindValues({}) })[1], /off until switched on/);
  assert.deepEqual(flipKind(country, base), { 'login.newcountry': true });
});

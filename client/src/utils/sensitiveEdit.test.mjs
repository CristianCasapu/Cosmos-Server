// Fork addition (plan 40): node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { isSensitiveRefusal, askSensitive, answerSensitive, confirmQuery, sendWithConfirmation, subscribeSensitiveEdit, getSensitiveEdit } from './sensitiveEdit.js';

const response = (status, body) => ({ status, clone() { return this; }, json: async () => body });
const refusal = response(409, { status: 'error', code: 'DS010', message: 'x', data: { sensitive: [{ service: 'app', kind: 'device', value: '/dev/sda' }, { service: 'app', kind: 'bind', value: '/' }] } });

test('an edit refused for sensitive access is sent again with every tick, once', async () => {
  const sent = [];
  const send = async (ids) => { sent.push(ids); return ids.length ? response(200, { status: 'OK' }) : refusal; };
  const answer = await sendWithConfirmation(send, async (items) => items.map((i) => `${i.service}|${i.kind}|${i.value}`));
  assert.equal(answer.status, 200);
  assert.deepEqual(sent, [[], ['app|device|/dev/sda', 'app|bind|/']]);
});

test('cancelled, or not every item ticked: the refusal stays, nothing is sent again', async () => {
  for (const ask of [async () => null, async () => ['app|device|/dev/sda']]) {
    let calls = 0;
    const answer = await sendWithConfirmation(async () => { calls += 1; return refusal; }, ask);
    assert.equal(answer.status, 409);
    assert.equal(calls, 1);
  }
});

test('other answers pass through untouched', async () => {
  for (const r of [response(200, {}), response(409, { code: 'OTHER' }), response(500, {})]) {
    assert.equal(await sendWithConfirmation(async () => r, async () => { throw new Error('not asked'); }), r);
  }
});

test('the dialog is asked through the module, and without one nothing is confirmed', async () => {
  assert.equal(await askSensitive([{ service: 'a', kind: 'device', value: '/dev/x' }]), null);
  const off = subscribeSensitiveEdit(() => {});
  const asked = askSensitive([{ service: 'a', kind: 'device', value: '/dev/x' }]);
  assert.equal(getSensitiveEdit().items.length, 1);
  answerSensitive(['a|device|/dev/x']);
  assert.deepEqual(await asked, ['a|device|/dev/x']);
  assert.equal(getSensitiveEdit(), null);
  off();
  assert.equal(confirmQuery(['a|b|/c d']), '?confirm=a%7Cb%7C%2Fc%20d');
  assert.equal(confirmQuery([]), '');
});

test('the dialog\'s texts are in the translation file', () => {
  const translations = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));
  const source = readFileSync(new URL('../components/sensitiveEditDialog.jsx', import.meta.url), 'utf8');
  let n = 0;
  for (const m of source.matchAll(/\bt\(\s*'([^']+)'\s*,\s*'((?:[^'\\]|\\.)*)'/gs)) {
    assert.equal(translations[m[1]], m[2].replace(/\\(.)/g, '$1'), m[1]);
    n += 1;
  }
  assert.ok(n >= 4);
});

test('the refusal that becomes a question is not announced as a failure', () => {
  assert.equal(isSensitiveRefusal(409, JSON.stringify({ code: 'DS010' })), true);
  assert.equal(isSensitiveRefusal(409, JSON.stringify({ code: 'X' })), false);
  assert.equal(isSensitiveRefusal(400, JSON.stringify({ code: 'DS010' })), false);
  assert.equal(isSensitiveRefusal(409, 'not json'), false);
});

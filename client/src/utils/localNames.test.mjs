// node --test client/src/utils/localNames.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { DEVICES, avahiAdvice, blocker, isLocalName, isMultiLabel, lanAddress, nameState, summary } from './localNames.js';

const base = (names = [], extra = {}) => ({
  enabled: true, avahi: 'running', hostName: 'server.local',
  interfaces: [{ name: 'eno1', address: '172.16.0.14', auto: true }],
  problems: [], names, avahiConf: { read: true, allowInterfaces: [], denyInterfaces: [] }, ...extra,
});
const own = { interface: 'eno1', address: '172.16.0.14', from: '172.16.0.14', own: true };
const other = { interface: 'eno1', address: '172.16.0.50', from: '172.16.0.50', own: false };

test('local names and multi-label names', () => {
  assert.equal(isLocalName('radarr.local'), true);
  assert.equal(isLocalName('Radarr.LOCAL.'), true);
  assert.equal(isLocalName('radarr.example.org'), false);
  assert.equal(isLocalName('.local'), false);
  assert.equal(isMultiLabel('radarr.local'), false);
  assert.equal(isMultiLabel('a.b.local'), true);
});

test('what keeps every name from resolving', () => {
  assert.equal(blocker(base()), null);
  assert.equal(blocker(base([], { enabled: false })).level, 'warning');
  assert.equal(blocker(base([], { avahi: 'absent' })).level, 'error');
  assert.match(blocker(base([], { avahi: 'absent' })).text[1], /apt install avahi-daemon/);
  assert.equal(blocker(base([], { interfaces: [] })).text[0], 'mgmt.localNames.noInterface');
});

test('the state of one name', () => {
  assert.equal(nameState(base(), 'app.example.org'), null);
  assert.equal(nameState(null, 'radarr.local'), null);
  assert.equal(nameState(base(), 'radarr.local').text[0], 'mgmt.localNames.notYet');
  const answered = nameState(base([{ name: 'radarr.local', state: 'published', checked: true, answered: true, answers: [own] }]), 'Radarr.local');
  assert.equal(answered.level, 'success');
  assert.equal(answered.text[2].where, 'eno1 (172.16.0.14)');
  assert.equal(nameState(base([{ name: 'radarr.local', state: 'published', checked: true, answered: false, answers: [] }]), 'radarr.local').level, 'warning');
  assert.equal(nameState(base([{ name: 'radarr.local', state: 'registering', checked: false, answers: [] }]), 'radarr.local').text[0], 'mgmt.localNames.registering');
  const conflict = nameState(base([{ name: 'radarr.local', state: 'conflict', checked: true, answers: [other] }]), 'radarr.local');
  assert.equal(conflict.level, 'error');
  assert.equal(conflict.text[2].from, '172.16.0.50');
  assert.equal(conflict.extra.length, 0, 'the other device is named once');
  const both = nameState(base([{ name: 'radarr.local', state: 'published', checked: true, answered: true, answers: [own, other] }]), 'radarr.local');
  assert.equal(both.extra[0][0], 'mgmt.localNames.foreign');
  assert.equal(nameState(base([{ name: 'x.local', state: 'failed', detail: 'Invalid host name' }]), 'x.local').text[2].detail, 'Invalid host name');
  assert.equal(nameState(base([{ name: 'server.local', state: 'hostName' }]), 'server.local').level, 'success');
  assert.equal(nameState(base([], { avahi: 'absent' }), 'radarr.local').level, 'error');
  assert.equal(nameState(base(), 'a.b.local').extra[0][0], 'mgmt.localNames.multiLabel');
});

test('the summary', () => {
  assert.equal(summary(base()).text[0], 'mgmt.localNames.summaryNone');
  assert.equal(summary(base([{ name: 'a.local', state: 'published', checked: true, answered: true }])).level, 'success');
  assert.equal(summary(base([{ name: 'a.local', state: 'conflict', checked: true }, { name: 'b.local', state: 'published', checked: true, answered: true }])).text[2].names, 'a.local');
  assert.equal(summary(base([{ name: 'a.local', state: 'published', checked: true, answered: false }])).level, 'warning');
  assert.equal(summary(base([], { avahi: 'absent' })).level, 'error');
});

test('the advice about Avahi\'s own interfaces', () => {
  assert.match(avahiAdvice(base())[1], /allow-interfaces/);
  assert.equal(avahiAdvice(base())[2].names, 'eno1');
  assert.equal(avahiAdvice(base([], { avahiConf: { read: true, allowInterfaces: ['eno1'] } })), null);
  assert.equal(avahiAdvice(base([], { avahiConf: { read: false } })), null);
  assert.equal(lanAddress(base()), '172.16.0.14');
});

test('every text is in the English translation file with the same words', () => {
  const file = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url)));
  const texts = [];
  const collect = (state) => { if (state) { texts.push(state.text, ...(state.extra || [])); } };
  DEVICES.forEach((device) => texts.push(device.text));
  const statuses = [base(), base([], { enabled: false }), base([], { avahi: 'absent' }), base([], { interfaces: [] })];
  const entries = ['published', 'registering', 'hostName', 'conflict', 'tooMany', 'noInterface', 'failed'].map((state) => ({ name: 'a.b.local', state, checked: state === 'published', answered: false, answers: state === 'conflict' ? [] : [other], detail: state === 'failed' ? 'x' : '' }));
  for (const status of statuses) collect(nameState(status, 'a.b.local'));
  for (const entry of entries) {
    collect(nameState(base([entry]), 'a.b.local'));
    collect(nameState(base([{ ...entry, answers: [other] }]), 'a.b.local'));
    collect(nameState(base([{ ...entry, answered: true, answers: [own] }]), 'a.b.local'));
    collect(nameState(base([{ ...entry, checked: false, answers: [] }]), 'a.b.local'));
    collect(summary(base([entry])));
  }
  collect(nameState(base([{ name: 'a.local', state: 'failed' }]), 'a.local'));
  collect(summary(base([{ name: 'a.local', state: 'published', checked: true, answered: true }])));
  collect(summary(base()));
  texts.push(avahiAdvice(base()));
  for (const [key, english] of texts) {
    assert.equal(file[key], english, key);
  }
});

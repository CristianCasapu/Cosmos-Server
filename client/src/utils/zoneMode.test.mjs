import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { isLocalZone, proposeMode, modeHint, MODES, cleanZone } from './zoneMode.js';

const translations = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));

test('names for use at home and addresses get a self-signed certificate, public names Let\'s Encrypt', () => {
  for (const local of ['nas.local', 'home.arpa', 'srv.home.arpa', 'cosmos.lan', '192.168.1.10', 'box.internal']) {
    assert.equal(isLocalZone(local), true, local);
    if (local.includes('.') && !/^\d/.test(local)) assert.equal(proposeMode(local).mode, 'SELFSIGNED', local);
  }
  for (const pub of ['example.com', 'my-site.ro', 'localnews.com', 'home-lab.net']) {
    assert.equal(isLocalZone(pub), false, pub);
    assert.equal(proposeMode(pub).mode, 'LETSENCRYPT', pub);
  }
  assert.equal(proposeMode(''), null);
  assert.equal(proposeMode('nodot'), null);
  assert.equal(cleanZone(' https://Example.com/ '), 'example.com');
});

test('every mode has a sentence, and every text is in the translation file', () => {
  MODES.forEach((m) => assert.ok(modeHint(m), m));
  const source = readFileSync(new URL('./zoneMode.js', import.meta.url), 'utf8');
  const pairs = [...source.matchAll(/\['(mgmt\.config\.zones\.fork\.[^']+)',\s*'((?:[^'\\]|\\.)*)'/g)];
  assert.ok(pairs.length >= 6);
  for (const [, key, text] of pairs) assert.equal(translations[key], text.replace(/\\(.)/g, '$1'), key);
});

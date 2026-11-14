// node --test client/src/utils/geoDefaults.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { DEFAULT_BLOCKED, ownCountries, safeDefaultBlocked } from './geoDefaults.js';

test('an admin in Romania is not blocked by the default list', () => {
  for (const hint of [
    { zones: ['Europe/Bucharest'] },                        // the browser's or the server's clock
    { zones: ['UTC', 'Europe/Bucharest'] },
    { languages: ['en-US', 'ro'] },                         // Romanian among the browser's languages
    { languages: ['ro-RO'] },
    { serverCountry: 'ro' },                                // the setting
  ]) {
    const { list, leftOut } = safeDefaultBlocked(ownCountries(hint));
    assert.ok(!list.includes('RO'), JSON.stringify(hint));
    assert.deepEqual(leftOut, ['RO']);
    assert.equal(list.length, DEFAULT_BLOCKED.length - 1);
  }
});

test('without a hint the list is upstream\'s, and other countries do not shorten it', () => {
  assert.deepEqual(safeDefaultBlocked(ownCountries({})).list, DEFAULT_BLOCKED);
  assert.deepEqual(safeDefaultBlocked(ownCountries({ zones: ['Europe/Berlin'], languages: ['de-DE', 'en'], serverCountry: 'DE' })).list, DEFAULT_BLOCKED);
  assert.deepEqual(safeDefaultBlocked(undefined).leftOut, []);
});

test('several hints, several countries', () => {
  const own = ownCountries({ zones: ['Asia/Kolkata', 'Europe/Istanbul'], languages: ['pt-BR'], serverCountry: 'VN' });
  assert.deepEqual(own.sort(), ['BR', 'IN', 'TR', 'VN']);
  const { list, leftOut } = safeDefaultBlocked(own);
  assert.deepEqual(leftOut.sort(), ['BR', 'IN', 'TR', 'VN']);
  assert.ok(list.includes('RO') && !list.includes('IN'));
});

test('hostile or broken hints are ignored', () => {
  assert.deepEqual(ownCountries({ serverCountry: '<b>', zones: [null, 42, 'Nowhere/Land'], languages: ['', 'not a tag!', null] }), []);
});

test('the button uses the safe list and says what it left out', () => {
  const page = readFileSync(new URL('../pages/config/configHTTP.jsx', import.meta.url), 'utf8');
  assert.ok(page.includes('safeDefaultBlocked('), 'configHTTP.jsx no longer calls safeDefaultBlocked');
  assert.ok(!/setFieldValue\("GeoBlocking", \["CN"/.test(page), 'the fixed list is back in configHTTP.jsx');
  const en = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));
  assert.ok(en['mgmt.config.security.geoblock.leftOut'].includes('{{countries}}'));
});

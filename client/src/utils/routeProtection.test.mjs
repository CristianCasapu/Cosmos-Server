// Fork addition: tests of routeProtection.js. Run with: node --test "client/src/utils/*.test.mjs"
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { LEVELS, PROFILES, levelOf, applyLevel, visitorBudget, levelText, timeoutText, bandwidthText } from './routeProtection.js';

// What a new URL gets from upstream's dialogs (utils/routeGuide.js newAppRoute),
// as routeSecurity.jsx's Formik values.
const NEW_URL = {
  _SmartShield_Enabled: true, _SmartShield_PolicyStrictness: 0, _SmartShield_PerUserTimeBudget: 0,
  _SmartShield_PerUserRequestLimit: 0, _SmartShield_PerUserByteLimit: 0, _SmartShield_PerUserSimultaneous: 0,
  _SmartShield_MaxGlobalSimultaneous: 0, _SmartShield_PrivilegedGroups: 0,
  ThrottlePerMinute: 10000, BlockCommonBots: true, Timeout: 14400000,
};

test('a new URL is at the recommended level, standard', () => {
  assert.equal(levelOf(NEW_URL), 'standard');
  const source = readFileSync(new URL('./routeGuide.js', import.meta.url), 'utf8');
  assert.match(source, /ThrottlePerMinute: 10000,/);
  assert.match(source, /BlockCommonBots: true,/);
  assert.match(source, /SmartShield: \{ Enabled: true \}/);
});

test('every level reads back as itself, and changes only its own fields', () => {
  for (const level of LEVELS) {
    const next = applyLevel(NEW_URL, level.value);
    assert.equal(levelOf(next), level.value, level.value);
    assert.equal(next.Timeout, NEW_URL.Timeout); // the time limit is not part of a level
  }
});

test('a number set by hand, or a level with something changed, is custom', () => {
  assert.equal(levelOf({ ...NEW_URL, _SmartShield_PerUserRequestLimit: 500 }), 'custom');
  assert.equal(levelOf({ ...NEW_URL, ThrottlePerMinute: 300 }), 'custom');
  assert.equal(levelOf({ ...NEW_URL, _SmartShield_Enabled: false }), 'custom'); // shield off, limit still on
  assert.equal(levelOf({ ...NEW_URL, _SmartShield_PolicyStrictness: 2 }), 'standard'); // upstream's "Normal" = the free default
  // picking a level again clears the hand-set numbers
  assert.equal(levelOf(applyLevel({ ...NEW_URL, _SmartShield_PerUserByteLimit: 5 }, 'standard')), 'standard');
});

test('no level is weaker than the default without being named for it', () => {
  // strict and standard never allow more than what a new URL has
  const standard = visitorBudget('standard');
  const strict = visitorBudget('strict');
  assert.ok(strict.requestsPerHour <= standard.requestsPerHour && strict.perMinute <= standard.perMinute);
  assert.equal(standard.perMinute, NEW_URL.ThrottlePerMinute);
});

test('the numbers said to the admin are the server\'s', () => {
  // the profiles of utils/types.go
  const go = readFileSync(new URL('../../../src/utils/types.go', import.meta.url), 'utf8');
  const free = go.slice(go.lastIndexOf('return SmartShieldProfile{'));
  assert.match(free, /PolicyStrictness:\s+NORMAL/);
  assert.match(free, /PerUserRequestLimit:\s+18000,/);
  assert.match(free, /PerUserByteLimit:\s+200 \* 1024 \* 1024 \* 1024,/);
  assert.match(free, /PerUserSimultaneous:\s+100,/);
  assert.match(go, /STRICT = 1\s+NORMAL = 2\s+LENIENT = 3/);
  assert.equal(PROFILES.free.requests, 18000);
  // the strict limit per minute is upstream's own for its API
  const server = readFileSync(new URL('../../../src/httpServer.go', import.meta.url), 'utf8');
  assert.match(server, /httprate\.Limit\(500, 1\*time\.Minute/);
  assert.equal(LEVELS.find((l) => l.value === 'strict').throttle, 500);
  // budgets: limit x strictness, per hour
  assert.equal(visitorBudget('standard').requestsPerHour, 36000);
  assert.equal(visitorBudget('strict').requestsPerHour, 18000);
  assert.equal(visitorBudget('relaxed').data, '600 GB');
  assert.equal(visitorBudget('standard', true).requestsPerHour, 600000); // Pro default: lenient
  assert.equal(visitorBudget('off'), null);
});

test('each level has its sentence, and units are said in words', () => {
  for (const level of [...LEVELS.map((l) => l.value), 'custom']) {
    const [key, text, vars] = levelText(level);
    assert.ok(key.startsWith('mgmt.urls.protection.') && text.length > 20);
    for (const name of [...text.matchAll(/\{\{(\w+)\}\}/g)].map((m) => m[1])) {
      assert.ok(vars && vars[name] !== undefined, `${key}: ${name}`);
    }
  }
  assert.equal(levelText('standard')[2].perHour, '36 000');
  assert.equal(timeoutText(14400000)[2].n, 4);
  assert.equal(timeoutText(0)[0], 'mgmt.urls.protection.timeoutNone');
  assert.equal(timeoutText(30)[0], 'mgmt.urls.protection.timeoutTiny'); // seconds typed into a millisecond field
  assert.equal(bandwidthText(1250000)[2].mbit, 10);
});

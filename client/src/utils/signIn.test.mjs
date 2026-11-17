// Tests of utils/signIn.js (plan 45): node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { webcrypto } from 'node:crypto';
import {
  generatePassword, followsRule, lengthFor, marketPassword, userVariant, emailVariant, isPlaceholder,
  guessSignIn, signInLabel, proposeSignIn, PLAIN_RULE, SAFE_SYMBOLS, LABEL,
} from './signIn.js';

const random = (n) => { const a = new Uint32Array(n); webcrypto.getRandomValues(a); return a; };

const RULES = [
  { min: 8, max: 64, symbols: '-._~', require: ['lower', 'upper', 'digit', 'symbol'] },
  { min: 8, max: 64, symbols: '-._~', require: ['lower', 'upper', 'digit'] },
  { min: 6, max: 64, symbols: '', require: [] },
  { min: 8, max: 40, symbols: '-_', require: ['upper'] },
  { min: 20, max: 20, symbols: '.', require: ['digit'] },
  PLAIN_RULE,
];

test('a generated password follows its rule and holds every class the rule allows', () => {
  for (const rule of RULES) {
    for (let i = 0; i < 300; i += 1) {
      const p = generatePassword(rule, random, 24);
      assert.ok(followsRule(p, rule), `${p} for ${JSON.stringify(rule)}`);
      assert.equal(p.length, lengthFor(rule, 24));
      assert.match(p, /[a-z]/); assert.match(p, /[A-Z]/); assert.match(p, /[0-9]/);
      const symbols = [...(rule.symbols || '')];
      if (symbols.length) assert.ok([...p].some((c) => symbols.includes(c)), `no symbol in ${p}`);
      assert.ok([...p].every((c) => /[A-Za-z0-9]/.test(c) || symbols.includes(c)), `a symbol outside the rule in ${p}`);
      assert.ok(!symbols.includes(p[0]) && !symbols.includes(p[p.length - 1]), `a symbol at an end: ${p}`);
      assert.doesNotMatch(p, /[$'"#@:/?%\\ `]/);
    }
  }
});

test('the length follows the configuration inside the rule', () => {
  assert.equal(generatePassword(RULES[0], random, 32).length, 32);
  assert.equal(generatePassword(RULES[3], random, 64).length, 40);
  assert.equal(generatePassword(RULES[4], random, 24).length, 20);
  assert.equal(generatePassword({ min: 8, max: 64, symbols: '' }, random, 0).length, 24);
});

test('a rule never makes the generator use a symbol outside the four safe ones', () => {
  const p = generatePassword({ min: 30, max: 30, symbols: '$#@-' }, random, 30);
  assert.ok([...p].every((c) => /[A-Za-z0-9]/.test(c) || SAFE_SYMBOLS.includes(c)), p);
  assert.ok(!/[$#@]/.test(p));
});

test('the generator draws without bias and does not repeat', () => {
  const seen = new Set();
  const counts = {};
  for (let i = 0; i < 2000; i += 1) {
    const p = generatePassword(PLAIN_RULE, random, 24);
    seen.add(p);
    for (const c of p) counts[c] = (counts[c] || 0) + 1;
  }
  assert.equal(seen.size, 2000);
  const values = Object.values(counts);
  assert.ok(Math.max(...values) / Math.min(...values) < 1.6, JSON.stringify(counts));
});

test('the Market keeps its alphabet and length, from the cryptographic generator', () => {
  for (let i = 0; i < 100; i += 1) assert.match(marketPassword(random), /^[A-Z0-9]{24}$/);
  let calls = 0;
  marketPassword((n) => { calls += 1; return random(n); });
  assert.ok(calls > 0);
});

test('a username the app refuses gets a fitting variant, with the reason', () => {
  const pg = { pattern: '^[a-z_][a-z0-9_]*$', max: 63, reserved: ['postgres'] };
  const my = { pattern: '^[a-z_][a-z0-9_]*$', max: 32, reserved: ['root'] };
  assert.deepEqual(userVariant('owner_name', pg), { value: 'owner_name', changed: false, reason: '' });
  assert.deepEqual(userVariant('Owner', pg), { value: 'owner', changed: true, reason: 'lower' });
  assert.deepEqual(userVariant('Mihai.Pop', pg), { value: 'mihai_pop', changed: true, reason: 'chars' });
  assert.deepEqual(userVariant('Ștefan Ionescu', pg), { value: 'stefan_ionescu', changed: true, reason: 'chars' });
  assert.equal(userVariant('42owner', pg).value, 'u42owner');
  assert.equal(userVariant('root', my).value, 'root_app');
  assert.equal(userVariant('a'.repeat(40), my).value.length, 32);
  assert.equal(userVariant('postgres', pg).value, 'postgres_app');
  assert.equal(userVariant('', pg).value, '');
  assert.equal(userVariant('...', pg).reason, 'refused');
  assert.equal(userVariant('Any Name', null).value, 'Any Name');
});

test('an e-mail is never invented, and one the app refuses is not used', () => {
  assert.deepEqual(emailVariant('owner@example.org', null), { value: 'owner@example.org', ok: true, reason: '' });
  assert.equal(emailVariant('owner@server.local', { refuseSpecialUse: true }).reason, 'specialUse');
  assert.equal(emailVariant('owner@server.local', null).ok, true);
  assert.equal(emailVariant('not an address', null).ok, false);
  assert.equal(emailVariant('', null).reason, 'empty');
});

test('placeholders stand for nothing set', () => {
  for (const v of ['', 'change-me', 'password', 'cosmos', 'CHANGEME']) assert.ok(isPlaceholder(v, 'password'), v);
  assert.ok(!isPlaceholder('Xk29a-real', 'password'));
  assert.ok(isPlaceholder('admin', 'user'));
  assert.ok(isPlaceholder('user@example.com', 'email'));
  assert.ok(!isPlaceholder('owner@example.org', 'email'));
});

test('the guess follows the cases shared with the server', () => {
  const file = JSON.parse(fs.readFileSync(new URL('../../../src/appsignin/testdata/guesses.json', import.meta.url), 'utf8'));
  assert.ok(file.cases.length >= 10);
  for (const c of file.cases) {
    const got = guessSignIn(c.names);
    if (!c.want) assert.equal(got, null, JSON.stringify(c.names));
    else assert.deepEqual(got, c.want, JSON.stringify(c.names));
  }
});

test('the label holds names only and the server\'s grammar', () => {
  assert.equal(signInLabel({ where: 'web', user: 'ADMIN_USER', password: 'ADMIN_PASSWORD', from: 'guess' }), 'where=web;user=ADMIN_USER;password=ADMIN_PASSWORD;from=guess');
  assert.equal(signInLabel({ where: 'web', user: 'BAD NAME', first: 'a;b' }), 'where=web');
});

const pgLogin = {
  where: 'database',
  accounts: [{ role: 'app', user: 'POSTGRES_USER', userUnset: 'postgres', password: 'POSTGRES_PASSWORD', database: 'POSTGRES_DB', defaults: true }],
  user: { pattern: '^[a-z_][a-z0-9_]*$', max: 63, reserved: ['postgres'] },
  password: { min: 8, max: 64, symbols: '-._~', require: ['lower', 'upper', 'digit'] },
};
const plan = { signIn: { 'postgres:18': { name: 'PostgreSQL', login: pgLogin }, 'jellyfin/jellyfin:10.11': { name: 'Jellyfin', firstRun: { name: 'Jellyfin', note: 'firstRunPage' } } } };
const defaults = { user: 'Owner.Name', email: 'owner@example.org', length: 28 };

test('the review fills what is empty from the recipe, the defaults and the rule, once', () => {
  const cache = new Map();
  const definition = { services: { db: { image: 'postgres:18', container_name: 'db', environment: ['TZ=UTC'] } } };
  const first = proposeSignIn(definition, plan, defaults, cache, random);
  const env = Object.fromEntries(first.definition.services.db.environment.map((l) => [l.split('=')[0], l.slice(l.indexOf('=') + 1)]));
  assert.equal(env.POSTGRES_USER, 'owner_name');
  assert.ok(followsRule(env.POSTGRES_PASSWORD, pgLogin.password));
  assert.equal(env.POSTGRES_PASSWORD.length, 28);
  assert.equal(first.lines.length, 1);
  assert.equal(first.lines[0].user, 'owner_name');
  assert.equal(first.lines[0].userChanged, 'chars');
  assert.equal(first.lines[0].generated, true);
  assert.deepEqual(first.proposed.map((p) => p.code).sort(), ['signIn.password', 'signIn.user']);
  // a second render gives the same values
  const again = proposeSignIn(definition, plan, defaults, cache, random);
  assert.deepEqual(again.definition, first.definition);
  // the original is not changed
  assert.deepEqual(definition.services.db.environment, ['TZ=UTC']);
});

test('the review never replaces a value that was set', () => {
  const definition = { services: { db: { image: 'postgres:18', environment: ['POSTGRES_USER=app', 'POSTGRES_PASSWORD=Typed-by-me-42'] } } };
  const r = proposeSignIn(definition, plan, defaults, new Map(), random);
  assert.deepEqual(r.definition.services.db.environment, definition.services.db.environment);
  assert.equal(r.lines[0].generated, false);
  assert.equal(r.lines[0].value, undefined);
  assert.equal(r.proposed.length, 0);
});

test('a first-run app gets a proposal to copy and a label, and no password in its settings', () => {
  const definition = { services: { jellyfin: { image: 'jellyfin/jellyfin:10.11', environment: [] } } };
  const r = proposeSignIn(definition, plan, defaults, new Map(), random);
  const line = r.lines[0];
  assert.equal(line.kind, 'firstRun');
  assert.equal(line.user, 'Owner.Name');
  assert.ok(followsRule(line.value, PLAIN_RULE));
  assert.deepEqual(r.definition.services.jellyfin.environment, []);
  assert.equal(r.definition.services.jellyfin.labels[LABEL], 'where=web;first=Owner.Name;note=firstRunPage;from=review');
  assert.ok(!JSON.stringify(r.definition).includes(line.value));
});

test('an unknown image is guessed by names, said so, and labelled with names only', () => {
  const definition = { services: { forms: { image: 'registry.example.org/a/forms:1', environment: ['ADMIN_EMAIL=', 'ADMIN_PASSWORD=change-me', 'SMTP_PASSWORD='] } } };
  const r = proposeSignIn(definition, plan, defaults, new Map(), random);
  const env = Object.fromEntries(r.definition.services.forms.environment.map((l) => [l.split('=')[0], l.slice(l.indexOf('=') + 1)]));
  assert.equal(env.ADMIN_EMAIL, 'owner@example.org');
  assert.ok(followsRule(env.ADMIN_PASSWORD, PLAIN_RULE));
  assert.equal(env.SMTP_PASSWORD, '');
  assert.equal(r.lines[0].kind, 'guess');
  assert.equal(r.definition.services.forms.labels[LABEL], 'where=web;email=ADMIN_EMAIL;password=ADMIN_PASSWORD;from=guess');
  assert.ok(!r.definition.services.forms.labels[LABEL].includes(env.ADMIN_PASSWORD));
});

test('without defaults the passwords are still generated, the names left alone', () => {
  const definition = { services: { db: { image: 'postgres:18', environment: [] } } };
  const r = proposeSignIn(definition, plan, null, new Map(), random);
  const names = r.definition.services.db.environment.map((l) => l.split('=')[0]);
  assert.deepEqual(names, ['POSTGRES_PASSWORD']);
});

test('a tool that signs in with its database\'s accounts says so', () => {
  const p = { signIn: { 'phpmyadmin:5': { name: 'phpMyAdmin', login: { where: 'web', via: 'database' } } } };
  const r = proposeSignIn({ services: { pma: { image: 'phpmyadmin:5', environment: ['PMA_HOST=db'] } } }, p, defaults, new Map(), random);
  assert.equal(r.lines.length, 1);
  assert.equal(r.lines[0].kind, 'via');
  assert.deepEqual(r.definition.services.pma.environment, ['PMA_HOST=db']);
});

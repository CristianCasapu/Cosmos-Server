// node --test client/src/utils/fail2ban.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { JAILS, durationWords, entryProblem, formOf, neverBanned, parseNetwork, settingsOf, splitDuration, stateOf, timeLeft, toSeconds } from './fail2ban.js';

const BOUNDS = { minMaxRetry: 1, maxMaxRetry: 10, minFindTime: 600, maxFindTime: 604800, minBanTime: 300, maxBanTime: 2678400, maxIgnoreIP: 64 };

test('stateOf: the plain state of the server\'s answer', () => {
  assert.equal(stateOf(null), 'loading');
  assert.equal(stateOf(undefined), 'loading');
  assert.equal(stateOf({ installed: false, running: false }), 'notInstalled');
  assert.equal(stateOf({ installed: true, running: false }), 'stopped');
  assert.equal(stateOf({ installed: true, running: true, jails: [] }), 'noJails');
  assert.equal(stateOf({ installed: true, running: true }), 'noJails');
  assert.equal(stateOf({ installed: true, running: true, jails: [{ name: 'sshd', loaded: false }] }), 'noJails');
  assert.equal(stateOf({ installed: true, running: true, jails: [{ name: 'sshd', loaded: false }, { name: 'recidive', loaded: true }] }), 'active');
});

test('splitDuration and toSeconds: the largest unit that fits exactly, and back', () => {
  const cases = [
    [600, 10, 'minutes'], [3600, 1, 'hours'], [14400, 4, 'hours'], [86400, 1, 'days'], [604800, 7, 'days'],
    [5400, 90, 'minutes'], [300, 5, 'minutes'], [2678400, 31, 'days'],
  ];
  for (const [seconds, value, unit] of cases) {
    assert.deepEqual(splitDuration(seconds), { value, unit }, String(seconds));
    assert.equal(toSeconds(value, unit), seconds);
    assert.equal(toSeconds(String(value), unit), seconds);
  }
  // seconds that fit no unit are rounded up, never down
  assert.deepEqual(splitDuration(601), { value: 11, unit: 'minutes' });
  for (const odd of [0, -1, NaN, undefined, null, 'x']) {
    assert.deepEqual(splitDuration(odd), { value: 0, unit: 'minutes' }, String(odd));
  }
  for (const [value, unit] of [['', 'hours'], ['1.5', 'hours'], ['-1', 'hours'], ['1e3', 'hours'], ['4', 'weeks'], [' ', 'days'], ['4 h', 'hours'], [undefined, 'hours'], ['12345678901', 'minutes']]) {
    assert.ok(Number.isNaN(toSeconds(value, unit)), `${value} ${unit}`);
  }
  assert.equal(toSeconds(' 4 ', 'hours'), 14400);
});

test('durationWords: singular and plural', () => {
  assert.deepEqual(durationWords(3600), ['fail2ban.duration.hour', '{{n}} hour', { n: 1 }]);
  assert.deepEqual(durationWords(14400), ['fail2ban.duration.hours', '{{n}} hours', { n: 4 }]);
  assert.deepEqual(durationWords(600), ['fail2ban.duration.minutes', '{{n}} minutes', { n: 10 }]);
  assert.deepEqual(durationWords(86400), ['fail2ban.duration.day', '{{n}} day', { n: 1 }]);
  assert.deepEqual(durationWords(604800), ['fail2ban.duration.days', '{{n}} days', { n: 7 }]);
});

test('timeLeft: how long a ban still lasts', () => {
  const now = Date.UTC(2026, 10, 13, 12, 0, 0);
  const left = (seconds) => timeLeft(now + seconds * 1000, now);
  assert.deepEqual(timeLeft(null, now), ['fail2ban.left.never', 'until it is lifted', {}]);
  assert.equal(left(-5)[0], 'fail2ban.left.over');
  assert.equal(left(0)[0], 'fail2ban.left.over');
  assert.equal(left(59)[0], 'fail2ban.left.soon');
  assert.deepEqual(left(60), ['fail2ban.left.minutes', '{{minutes}} min', { minutes: 1 }]);
  assert.deepEqual(left(3599), ['fail2ban.left.minutes', '{{minutes}} min', { minutes: 59 }]);
  assert.deepEqual(left(3600 * 3 + 60 * 12 + 5), ['fail2ban.left.hours', '{{hours}} h {{minutes}} min', { hours: 3, minutes: 12 }]);
  assert.deepEqual(left(86400 * 6 + 3600 * 5), ['fail2ban.left.days', '{{days}} d {{hours}} h', { days: 6, hours: 5 }]);
  assert.equal(timeLeft('nonsense', now)[0], 'fail2ban.left.over');
});

test('parseNetwork: addresses and networks, nothing else', () => {
  const good = {
    '203.0.113.7': [4, 32], '203.0.113.0/24': [4, 24], '10.0.0.0/8': [4, 8], '0.0.0.0/0': [4, 0],
    '::1': [6, 128], '2001:db8::/32': [6, 32], 'fe80::/10': [6, 10], '2001:DB8:0:0:0:0:0:7': [6, 128],
    '::ffff:203.0.113.7': [4, 32], ' 192.168.1.20 ': [4, 32], '::': [6, 128], '2001:db8::1:2:3:4:5': [6, 128],
  };
  for (const [text, [version, bits]] of Object.entries(good)) {
    const network = parseNetwork(text);
    assert.ok(network, text);
    assert.equal(network.version, version, text);
    assert.equal(network.bits, bits, text);
  }
  const bad = ['', ' ', '/', '/24', '203.0.113', '203.0.113.256', '203.0.113.7/33', '203.0.113.7/-1', '203.0.113.7/24/24', '1.2.3.4.5', '01.2.3.4',
    'example.com', 'localhost', 'fe80::1%eth0', '[::1]', ':::', '1::2::3', '2001:db8', '1:2:3:4:5:6:7:8:9', '1:2:3:4:5:6:7::8', '::1/129', 'g::1',
    '10.0.0.0/8 192.168.0.0/16', '10.0.0.0/8\nenabled = false', '%(x)s', '<ip>', '203.0.113.7;id', undefined, null, 42];
  for (const text of bad) {
    assert.equal(parseNetwork(text), null, String(text));
  }
});

test('entryProblem: what the never-ban list accepts', () => {
  assert.equal(entryProblem('192.168.0.0/16'), null);
  assert.equal(entryProblem('2001:db8::7'), null);
  assert.equal(entryProblem('home.example.org')[0], 'fail2ban.ignore.invalid');
  assert.equal(entryProblem('')[0], 'fail2ban.ignore.invalid');
  assert.equal(entryProblem('0.0.0.0/0')[0], 'fail2ban.ignore.everything');
  assert.equal(entryProblem('::/0')[0], 'fail2ban.ignore.everything');
});

test('neverBanned: does the list cover an address', () => {
  const list = ['127.0.0.0/8', '::1', '10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16', 'fc00::/7', 'fe80::/10', '203.0.113.7', '2001:db8:5::/48', 'not an entry'];
  const cases = {
    '192.168.1.20': true, '172.16.0.14': true, '172.31.255.255': true, '172.32.0.1': false, '10.255.0.1': true, '11.0.0.1': false,
    '203.0.113.7': true, '203.0.113.8': false, '127.0.0.1': true, '::1': true, 'fd00::5': true, 'fe80::1': true,
    '2001:db8:5:1::9': true, '2001:db8:6::9': false, '::ffff:192.168.1.20': true, '': false, 'example.com': false, '192.168.0.0/16': false,
  };
  for (const [address, want] of Object.entries(cases)) {
    assert.equal(neverBanned(list, address), want, address);
  }
  assert.equal(neverBanned([], '192.168.1.20'), false);
  assert.equal(neverBanned(undefined, '192.168.1.20'), false);
  assert.equal(neverBanned(['0.0.0.0/0'], '8.8.8.8'), true);
  assert.equal(neverBanned(['0.0.0.0/0'], '2001:db8::1'), false);
});

test('formOf and settingsOf: the form holds what the server sent and gives it back', () => {
  const settings = {
    jails: {
      'cosmos-auth': { maxRetry: 5, findTime: 3600, banTime: 14400 },
      'cosmos-probe': { maxRetry: 3, findTime: 3600, banTime: 86400 },
      'cosmos-scan': { maxRetry: 10, findTime: 600, banTime: 14400 },
      'cosmos-abuse': { maxRetry: 3, findTime: 86400, banTime: 14400 },
      sshd: { maxRetry: 5, findTime: 600, banTime: 3600 },
      recidive: { maxRetry: 3, findTime: 86400, banTime: 604800 },
    },
    ignoreIP: ['127.0.0.0/8', '::1', '192.168.0.0/16'],
  };
  const form = formOf(settings);
  assert.deepEqual(form.jails['cosmos-auth'], { maxRetry: '5', findValue: '1', findUnit: 'hours', banValue: '4', banUnit: 'hours' });
  assert.deepEqual(form.jails.recidive, { maxRetry: '3', findValue: '1', findUnit: 'days', banValue: '7', banUnit: 'days' });
  assert.deepEqual(settingsOf(form, BOUNDS), { settings });

  // the form does not share the list with what it was made from
  form.ignoreIP.push('203.0.113.7', '203.0.113.7', ' 203.0.113.7 ');
  assert.equal(settings.ignoreIP.length, 3);
  assert.deepEqual(settingsOf(form, BOUNDS).settings.ignoreIP, ['127.0.0.0/8', '::1', '192.168.0.0/16', '203.0.113.7']);

  // nothing from the server: an empty form that does not pass
  const empty = formOf(undefined);
  assert.deepEqual(Object.keys(empty.jails), Object.keys(JAILS));
  assert.ok(settingsOf(empty, BOUNDS).problems);
});

test('settingsOf: says what is out of bounds, field by field', () => {
  const base = () => formOf({
    jails: Object.fromEntries(Object.keys(JAILS).map((name) => [name, { maxRetry: 5, findTime: 3600, banTime: 14400 }])),
    ignoreIP: [],
  });
  const problemsOf = (change) => {
    const form = base();
    change(form);
    return Object.keys(settingsOf(form, BOUNDS).problems || {}).sort();
  };
  assert.deepEqual(problemsOf(() => {}), []);
  assert.deepEqual(problemsOf((form) => { form.jails.sshd.maxRetry = '0'; }), ['sshd.maxRetry']);
  assert.deepEqual(problemsOf((form) => { form.jails.sshd.maxRetry = '11'; }), ['sshd.maxRetry']);
  assert.deepEqual(problemsOf((form) => { form.jails.sshd.maxRetry = '5.5'; }), ['sshd.maxRetry']);
  assert.deepEqual(problemsOf((form) => { form.jails.sshd.maxRetry = ''; }), ['sshd.maxRetry']);
  assert.deepEqual(problemsOf((form) => { form.jails['cosmos-auth'].findValue = '5'; form.jails['cosmos-auth'].findUnit = 'minutes'; }), ['cosmos-auth.findTime']);
  assert.deepEqual(problemsOf((form) => { form.jails['cosmos-auth'].findValue = '8'; form.jails['cosmos-auth'].findUnit = 'days'; }), ['cosmos-auth.findTime']);
  assert.deepEqual(problemsOf((form) => { form.jails.recidive.banValue = '32'; form.jails.recidive.banUnit = 'days'; }), ['recidive.banTime']);
  assert.deepEqual(problemsOf((form) => { form.jails.recidive.banValue = '4'; form.jails.recidive.banUnit = 'minutes'; }), ['recidive.banTime']);
  assert.deepEqual(problemsOf((form) => { form.jails.recidive.banValue = 'forever'; }), ['recidive.banTime']);
  assert.deepEqual(problemsOf((form) => { form.ignoreIP = ['192.168.0.0/16', 'home.example.org']; }), ['ignoreIP']);
  assert.deepEqual(problemsOf((form) => { form.ignoreIP = ['0.0.0.0/0']; }), ['ignoreIP']);
  assert.deepEqual(problemsOf((form) => { form.ignoreIP = Array.from({ length: 65 }, (unused, i) => '203.0.113.' + i); }), ['ignoreIP']);
  assert.deepEqual(problemsOf((form) => { form.ignoreIP = Array.from({ length: 64 }, (unused, i) => '203.0.113.' + i); }), []);
});

test('every text of the panel\'s logic has its English entry in the translation file', () => {
  const translation = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));
  const source = readFileSync(new URL('./fail2ban.js', import.meta.url), 'utf8');
  const pairs = [...source.matchAll(/\['(fail2ban\.[A-Za-z0-9.]+)', '((?:[^'\\]|\\.)*)'/g)];
  assert.ok(pairs.length > 25, `only ${pairs.length} texts found`);
  for (const [, key, text] of pairs) {
    assert.equal(translation[key], text.replace(/\\'/g, "'"), key);
  }
});

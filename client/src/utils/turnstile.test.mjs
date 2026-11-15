// node --test client/src/utils/turnstile.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  FORMS, HEADER, SCRIPT_URL, TURNSTILE_ORIGIN, canRetry, clearToken, formOfRequest, hasToken, languageFor, manualSteps,
  problemText, reasonText, refusalText, renderOptions, setToken, stateText, statusText, subscribe, themeFor, tryAddress,
  turnstileRequest, visitorSees, widgetErrorKind,
} from './turnstile.js';
import { requestVerdict } from './pwaPolicy.js';

test('only a POST of the three public forms carries a token', () => {
  assert.equal(formOfRequest('/cosmos/api/login', 'POST'), 'login');
  assert.equal(formOfRequest('/cosmos/api/login', 'post'), 'login');
  assert.equal(formOfRequest('/cosmos/api/password-reset', 'POST'), 'reset');
  assert.equal(formOfRequest('/cosmos/api/register?x=1', 'POST'), 'register');
  for (const [path, method] of [['/cosmos/api/login', 'GET'], ['/cosmos/api/mfa', 'POST'], ['/cosmos/api/sudo', 'POST'],
    ['/cosmos/api/turnstile', 'POST'], ['/cosmos/api/login/x', 'POST'], ['https://evil.example/cosmos/api/login', 'POST'], [null, undefined]]) {
    assert.equal(formOfRequest(path, method), '', String(path) + ' ' + method);
  }
});

test('a token goes into one request of its form, once, and the widget is told', async () => {
  const events = [];
  const off = subscribe((event) => events.push(event));
  setToken('login', 'tok-1');
  assert.equal(hasToken('login'), true);

  // another request does not take it
  const other = {};
  turnstileRequest('/cosmos/api/me', { method: 'GET' }, other);
  assert.deepEqual(other, {});
  assert.equal(hasToken('login'), true);

  const headers = {};
  const watch = turnstileRequest('/cosmos/api/login', { method: 'POST' }, headers);
  assert.equal(headers[HEADER], 'tok-1');
  assert.equal(hasToken('login'), false);
  assert.deepEqual(events, [{ type: 'spent', form: 'login' }]);

  // the next request has none
  const again = {};
  turnstileRequest('/cosmos/api/login', { method: 'POST' }, again);
  assert.equal(again[HEADER], undefined);

  // a refusal of the check is reported, other answers are not
  const answer = (status, body) => ({ status, clone: () => ({ json: () => Promise.resolve(body) }) });
  await watch(Promise.resolve(answer(403, { code: 'TS002' })));
  await watch(Promise.resolve(answer(401, { code: 'UL001' })));
  await watch(Promise.resolve(answer(503, { code: 'TS004' })));
  await new Promise((resolve) => setTimeout(resolve, 10));
  assert.deepEqual(events.slice(1), [{ type: 'refused', form: 'login', code: 'TS002' }, { type: 'refused', form: 'login', code: 'TS004' }]);
  off();
  clearToken('login');
  setToken('login', '');
  assert.equal(hasToken('login'), false);
});

test('the widget follows the skin and the language', () => {
  assert.equal(themeFor('dark'), 'dark');
  assert.equal(themeFor('light'), 'light');
  assert.equal(themeFor(undefined), 'light');
  assert.equal(languageFor('ro'), 'ro');
  assert.equal(languageFor('pt-br'), 'pt-BR');
  assert.equal(languageFor('zh_CN'), 'zh-CN');
  assert.equal(languageFor('cimode'), 'auto');
  assert.equal(languageFor(''), 'auto');
  const options = renderOptions({ siteKey: 'k', form: 'reset', mode: 'dark', language: 'en', appearance: 'interaction-only' });
  assert.equal(options.action, 'cosmos-reset');
  assert.equal(options.theme, 'dark');
  assert.equal(options.size, 'flexible');
  assert.equal(options.appearance, 'interaction-only');
  assert.equal(options['response-field'], false);
  assert.equal(renderOptions({ siteKey: 'k', form: 'nope' }).action, 'cosmos-login');
  for (const form of Object.keys(FORMS)) assert.match(FORMS[form].action, /^[A-Za-z0-9_-]{1,32}$/);
});

test('Cloudflare\'s error codes become plain states', () => {
  assert.equal(widgetErrorKind('110200'), 'hostname');
  assert.equal(widgetErrorKind('110100'), 'sitekey');
  assert.equal(widgetErrorKind('400070'), 'sitekey');
  assert.equal(widgetErrorKind('200500'), 'blocked');
  assert.equal(widgetErrorKind('110600'), 'timeout');
  assert.equal(widgetErrorKind('200100'), 'clock');
  assert.equal(widgetErrorKind('300030'), 'failed');
  assert.equal(canRetry('timeout'), true);
  assert.equal(canRetry('hostname'), false);
  assert.match(statusText('hostname', 'cosmos.local')[1], /\{\{host\}\}/);
  assert.equal(statusText('hostname', 'cosmos.local')[2].host, 'cosmos.local');
  assert.equal(refusalText('UL001'), null);
  assert.ok(refusalText('TS004')[1].includes('home network'));
});

test('the card says what is in force', () => {
  assert.equal(stateText(null), null);
  assert.equal(stateText({ siteKey: '' })[1][0], 'mgmt.turnstile.state.off');
  assert.equal(stateText({ siteKey: 'k', confirmed: false })[1][0], 'mgmt.turnstile.state.saved');
  assert.equal(stateText({ siteKey: 'k', confirmed: true, confirmedHost: 'cosmos.example.org' })[1][2].host, 'cosmos.example.org');
  assert.equal(stateText({ active: true, forms: { login: true, reset: false, register: true } })[1][2].count, 2);
  assert.equal(stateText({ active: true, forms: {} })[0], 'warning');
  assert.equal(stateText({ consoleOff: true, active: true, consoleFile: '/var/lib/cosmos/turnstile.off' })[1][2].file, '/var/lib/cosmos/turnstile.off');
  assert.equal(problemText({ reason: 'key_refused' })[0], 'mgmt.turnstile.problem.key');
  // away from the card (the home page) the keys are not "below"
  assert.equal(problemText({ reason: 'key_refused' }, true)[0], 'mgmt.turnstile.problem.keyElsewhere');
  assert.ok(!problemText({ reason: 'key_refused' }, true)[1].includes('below'));
  assert.equal(problemText({ reason: 'unreachable', count: 3 }, true)[2].count, 3);
  assert.equal(problemText({ reason: 'unreachable', count: 3 })[2].count, 3);
  assert.ok(reasonText('permission')[1].includes('Account > Turnstile > Edit'));
  assert.equal(reasonText('something new')[0], 'mgmt.turnstile.reason.other');
  assert.equal(visitorSees('invisible', 'always')[0], 'mgmt.turnstile.sees.invisible');
  assert.equal(visitorSees('managed', 'interaction-only')[0], 'mgmt.turnstile.sees.interaction');
  assert.ok(manualSteps('cosmos.example.org')[1][2].host === 'cosmos.example.org');
});

test('the trial is offered on the panel\'s public name only', () => {
  const status = { panelHost: 'cosmos.example.org' };
  assert.equal(tryAddress(status, { hostname: 'cosmos.example.org', port: '2053' }), '');
  assert.equal(tryAddress(status, { hostname: '192.168.1.10', port: '2053' }), 'https://cosmos.example.org:2053/cosmos-ui/config-general/https');
  assert.equal(tryAddress(status, { hostname: 'cosmos.local', port: '' }), 'https://cosmos.example.org/cosmos-ui/config-general/https');
  assert.equal(tryAddress({ panelHost: '' }, { hostname: 'x' }), '');
});

test('the service worker never answers Turnstile\'s requests', () => {
  const origin = 'https://cosmos.example.org:2053';
  const lists = { core: new Set(['/cosmos-ui/index.html']), later: new Set() };
  for (const url of [SCRIPT_URL, TURNSTILE_ORIGIN + '/cdn-cgi/challenge-platform/h/b/turnstile/if/ov2/av0/rcv/x/0x4AAA/auto/fbE/new/normal/auto/',
    'https://challenges.cloudflare.com/turnstile/v0/siteverify']) {
    for (const mode of ['no-cors', 'cors', 'navigate']) {
      assert.equal(requestVerdict({ method: 'GET', url, mode }, origin, lists), 'ignore', url + ' ' + mode);
    }
  }
  assert.equal(requestVerdict({ method: 'POST', url: origin + '/cosmos/api/login', mode: 'cors' }, origin, lists), 'ignore');
});

// every [key, text] and t('key', 'text') of the widget, the card and this module
const sources = ['./turnstile.js', '../components/turnstile.jsx', '../components/turnstileWidget.jsx'];
const pairs = () => {
  const out = [];
  const unescape = (text) => text.replace(/\\'/g, '\'').replace(/\\"/g, '"');
  for (const source of sources) {
    const text = readFileSync(new URL(source, import.meta.url), 'utf8');
    for (const m of text.matchAll(/\[\s*'([a-zA-Z0-9_.]+)',\s*'((?:[^'\\]|\\.)*)'/g)) out.push([m[1], unescape(m[2]), source]);
    for (const m of text.matchAll(/\bt\(\s*'([a-zA-Z0-9_.]+)',\s*'((?:[^'\\]|\\.)*)'/g)) out.push([m[1], unescape(m[2]), source]);
  }
  return out.filter(([key]) => key.includes('.'));
};

test('every text is in the English translation file, word for word', () => {
  const file = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));
  const found = pairs();
  assert.ok(found.length > 80, 'found ' + found.length);
  for (const [key, text, source] of found) {
    assert.equal(file[key], text, key + ' (' + source + ')');
  }
});

// node --test client/src/utils/serverAnswer.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { ERROR_PAGE_MESSAGE, NO_ANSWER_MESSAGE, looksLikeWebPage, plainFailure, serverDidNotAnswer, unansweredError } from './serverAnswer.js';

const cloudflare521 = '<!DOCTYPE html> <!--[if lt IE 7]> <html class="no-js ie6 oldie" lang="en-US"> <![endif]--> <html class="no-js" lang="en-US"> <head> <title>example.org | 521: Web server is down</title> <meta charset="UTF-8" /> </head> <body> <div id="cf-wrapper"> <h1>Web server is down</h1> ' + 'x'.repeat(6000) + '</div></body></html>';

test('a proxy answering while the server restarts is "the server did not answer", never its page', () => {
  for (const status of [502, 503, 504, 520, 521, 522, 523, 524, 525, 526, 527, 530]) {
    assert.equal(serverDidNotAnswer(status), true, String(status));
    assert.equal(plainFailure(status, cloudflare521), NO_ANSWER_MESSAGE);
    assert.equal(plainFailure(status, ''), NO_ANSWER_MESSAGE);
  }
  for (const status of [200, 400, 401, 403, 404, 409, 429, 500, 0, undefined, 'abc']) {
    assert.equal(serverDidNotAnswer(status), false, String(status));
  }
});

test('any other web page is named, not shown', () => {
  for (const page of [cloudflare521, '<html><body><h1>403 Forbidden</h1></body></html>', '\n  <!doctype html><title>x</title>', '<head><title>Blocked</title></head>', 'Error<script>alert(1)</script>']) {
    const message = plainFailure(403, page);
    assert.equal(message, ERROR_PAGE_MESSAGE + ' (HTTP 403)');
    assert.ok(!message.includes('<'));
  }
  assert.equal(looksLikeWebPage('Container not found'), false);
  assert.equal(looksLikeWebPage('value < 3 and > 1'), false);
});

test('plain text is one bounded line', () => {
  assert.equal(plainFailure(500, 'Internal server error\nmore\nlines'), 'Internal server error');
  assert.equal(plainFailure(400, '\x1b[31m  bad request  \x1b[0m'), 'bad request');
  assert.equal(plainFailure(500, ''), '');
  const long = plainFailure(500, 'y'.repeat(5000));
  assert.ok(long.length <= 301 && long.endsWith('…'));
});

test('what is tried again while waiting for the server', () => {
  assert.equal(unansweredError(undefined), true);                    // the network failed
  assert.equal(unansweredError(new TypeError('Failed to fetch')), true);
  assert.equal(unansweredError({ status: 521, unanswered: true }), true);   // a proxy answered
  assert.equal(unansweredError({ status: 400 }), false);             // the server answered: its error was shown
  assert.equal(unansweredError({ status: 401, unanswered: false }), false);
});

test('the request layer, the toasts and the reloads after a save use it', () => {
  const read = (path) => readFileSync(new URL(path, import.meta.url), 'utf8');
  const wrap = read('../api/wrap.ts');
  assert.ok(wrap.includes('message: plainFailure(response.status, rep)'), 'wrap.ts shows the raw answer again');
  assert.ok(wrap.includes('e.unanswered = Boolean(rep.unanswered)'));
  const activity = read('./activity.js');
  assert.ok(activity.includes('plainFailure(response.status, text)'), 'activity.js shows the raw answer again');
  assert.ok(activity.includes('message === NO_ANSWER_MESSAGE'));
  for (const page of ['../pages/config/index.jsx', '../pages/dashboard/AlertPage.jsx']) {
    assert.ok(read(page).includes('unansweredError(error)'), page + ' no longer waits when a proxy answers');
  }
  const en = JSON.parse(read('./locales/en/translation.json'));
  assert.ok(en['activity.noAnswer'] && en['activity.errorPage'].includes('{{status}}'));
});

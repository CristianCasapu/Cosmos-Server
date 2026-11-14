// Fork addition: tests of pwaClient.js (node --test "client/src/utils/*.test.mjs")
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  retryDelay, serverBack, installOffer, dismissedUntil, readDismissed, DISMISS_DAYS,
} from './pwaClient.js';

const UA = {
  iphone: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1',
  iphoneChrome: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/131.0.6778.73 Mobile/15E148 Safari/604.1',
  ipad: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15',
  macSafari: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15',
  macChrome: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36',
  winFirefox: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0',
  android: 'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36',
};

test('the offline page tries again soon, then every 30 seconds', () => {
  assert.deepEqual([0, 1, 2, 3, 4, 50].map(retryDelay), [5, 10, 15, 30, 30, 30]);
  for (const odd of [-1, 1.5, NaN, undefined, null, '2']) assert.equal(retryDelay(odd), 5);
});

test('which answers mean the server is back', () => {
  for (const status of [200, 204, 301, 302, 304, 401, 403, 404, 429, 500]) assert.equal(serverBack(status), true, String(status));
  for (const status of [502, 503, 504, 520, 521, 522, 523, 524, 525, 526, 527, 530, 0, undefined, null, '200', 99]) {
    assert.equal(serverBack(status), false, String(status));
  }
});

test('which way of installing is offered', () => {
  const env = { enabled: true, standalone: false, prompt: false, dismissedUntil: 0, now: 1000, touchPoints: 0 };
  const cases = [
    [{ ua: UA.android, prompt: true }, 'prompt'],
    [{ ua: UA.macChrome, prompt: true }, 'prompt'],
    [{ ua: UA.iphone, touchPoints: 5 }, 'ios'],
    [{ ua: UA.iphoneChrome, touchPoints: 5 }, 'ios'],
    [{ ua: UA.ipad, touchPoints: 5 }, 'ios'], // iPadOS says it is a Mac; its touch screen tells
    [{ ua: UA.macSafari }, 'mac-safari'],
    [{ ua: UA.macChrome }, null], // Chrome without the event: nothing to offer (not installable yet, or installed)
    [{ ua: UA.winFirefox }, null],
    [{ ua: UA.iphone, touchPoints: 5, standalone: true }, null], // already the app
    [{ ua: UA.android, prompt: true, enabled: false }, null], // switched off on the server
    [{ ua: UA.android, prompt: true, enabled: null }, null], // not known yet
    [{ ua: UA.iphone, touchPoints: 5, dismissedUntil: 2000 }, null], // "Not now"
    [{ ua: UA.iphone, touchPoints: 5, dismissedUntil: 500 }, 'ios'], // ... has run out
  ];
  for (const [change, want] of cases) {
    assert.equal(installOffer({ ...env, ...change }), want, JSON.stringify(change));
  }
  assert.equal(installOffer(null), null);
  assert.equal(installOffer({}), null);
});

test('"Not now" lasts 30 days, and anything else in its place counts as never said', () => {
  assert.equal(DISMISS_DAYS, 30);
  assert.equal(dismissedUntil(0), 30 * 86400000);
  assert.equal(readDismissed(String(dismissedUntil(5))), dismissedUntil(5));
  for (const odd of [null, '', 'abc', '-5', 'Infinity', '{}']) assert.equal(readDismissed(odd), 0, String(odd));
});

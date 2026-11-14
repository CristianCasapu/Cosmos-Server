// Fork addition: what the service worker may answer and keep, as a table.
// node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { requestVerdict, storable, serverUnreachable, staleCaches, localAddress, PWA_CACHE_PREFIX } from './pwaPolicy.js';

const ORIGIN = 'https://panel.example:2053';
const lists = {
  core: new Set(['/cosmos-ui/assets/index-1a2b3c4d.js', '/cosmos-ui/assets/index-5e6f7a8b.css', '/cosmos-ui/skin/theme.css?v=abc', '/cosmos-ui/offline.html']),
  later: new Set(['/cosmos-ui/assets/translation-99887766.js', '/cosmos-ui/assets/logo-11223344.svg']),
};
const get = (url, more = {}) => ({ method: 'GET', url, mode: 'cors', authorization: false, range: false, ...more });

test('which requests the worker answers', async (t) => {
  const table = [
    // the build's own files
    ['asset', get(ORIGIN + '/cosmos-ui/assets/index-1a2b3c4d.js')],
    ['asset', get(ORIGIN + '/cosmos-ui/assets/index-5e6f7a8b.css', { mode: 'no-cors' })],
    ['asset', get(ORIGIN + '/cosmos-ui/skin/theme.css?v=abc')],
    ['asset', get(ORIGIN + '/cosmos-ui/assets/translation-99887766.js')],
    ['asset', get('/cosmos-ui/assets/logo-11223344.svg')],
    ['asset', get(ORIGIN + '/cosmos-ui/assets/logo-11223344.svg', { mode: 'navigate' })],
    // a file of another build, or one the build does not list: the network, untouched
    ['ignore', get(ORIGIN + '/cosmos-ui/assets/index-ffffffff.js')],
    ['ignore', get(ORIGIN + '/cosmos-ui/skin/theme.css?v=other')],
    ['ignore', get(ORIGIN + '/cosmos-ui/skin/theme.css')],
    ['ignore', get(ORIGIN + '/cosmos-ui/manifest.webmanifest')],
    ['ignore', get(ORIGIN + '/cosmos-ui/sw.js')],
    ['ignore', get(ORIGIN + '/cosmos-ui/index.html')],
    // pages of the panel
    ['navigate', get(ORIGIN + '/cosmos-ui/', { mode: 'navigate' })],
    ['navigate', get(ORIGIN + '/cosmos-ui/servapps/containers/jellyfin', { mode: 'navigate' })],
    ['navigate', get(ORIGIN + '/cosmos-ui/login?redirect=%2Fcosmos-ui%2F', { mode: 'navigate' })],
    ['navigate', get(ORIGIN + '/cosmos-ui/index.html', { mode: 'navigate' })],
    // the API, logs, terminals, OAuth: never, whatever the request looks like
    ['ignore', get(ORIGIN + '/cosmos/api/status')],
    ['ignore', get(ORIGIN + '/cosmos/api/me')],
    ['ignore', get(ORIGIN + '/cosmos/api/servapps/jellyfin/logs?search=')],
    ['ignore', get(ORIGIN + '/cosmos/api/background/avif')],
    ['ignore', get(ORIGIN + '/cosmos/api/favicon?q=https://x.example/favicon.ico')],
    ['ignore', get(ORIGIN + '/cosmos/_logs')],
    ['ignore', get(ORIGIN + '/cosmos/rclone/config/dump')],
    ['ignore', get(ORIGIN + '/cosmos/oauth2/auth', { mode: 'navigate' })],
    ['ignore', get(ORIGIN + '/cosmos/api/logout', { mode: 'navigate' })],
    ['ignore', get(ORIGIN + '/cosmos', { mode: 'navigate' })],
    // outside the panel: an application on a path of the same origin, the root, well-known files
    ['ignore', get(ORIGIN + '/', { mode: 'navigate' })],
    ['ignore', get(ORIGIN + '/jellyfin/web/index.html', { mode: 'navigate' })],
    ['ignore', get(ORIGIN + '/cosmos-ui', { mode: 'navigate' })],
    ['ignore', get(ORIGIN + '/cosmos-uix/assets/index-1a2b3c4d.js')],
    ['ignore', get(ORIGIN + '/oauth2/token')],
    ['ignore', get(ORIGIN + '/.well-known/openid-configuration')],
    ['ignore', get(ORIGIN + '/logo/')],
    // other origins: an application on its own name, another port, plain http, a look-alike
    ['ignore', get('https://jellyfin.panel.example:2053/cosmos-ui/assets/index-1a2b3c4d.js')],
    ['ignore', get('https://panel.example/cosmos-ui/', { mode: 'navigate' })],
    ['ignore', get('http://panel.example:2053/cosmos-ui/assets/index-1a2b3c4d.js')],
    ['ignore', get('https://panel.example:2053.evil.example/cosmos-ui/', { mode: 'navigate' })],
    ['ignore', get('https://user:secret@panel.example:2053/cosmos-ui/', { mode: 'navigate' })],
    ['ignore', get('data:text/html,hello')],
    ['ignore', get('not an address at all ::')],
    // anything that is not a plain read
    ['ignore', { ...get(ORIGIN + '/cosmos-ui/assets/index-1a2b3c4d.js'), method: 'POST' }],
    ['ignore', { ...get(ORIGIN + '/cosmos-ui/'), method: 'HEAD', mode: 'navigate' }],
    ['ignore', get(ORIGIN + '/cosmos-ui/assets/index-1a2b3c4d.js', { authorization: true })],
    ['ignore', get(ORIGIN + '/cosmos-ui/', { mode: 'navigate', authorization: true })],
    ['ignore', get(ORIGIN + '/cosmos-ui/assets/index-1a2b3c4d.js', { range: true })],
    // addresses that could mean another path once decoded
    ['ignore', get(ORIGIN + '/cosmos-ui/%2e%2e/cosmos/api/status', { mode: 'navigate' })],
    ['ignore', get(ORIGIN + '/cosmos-ui/..%2fcosmos/api/status', { mode: 'navigate' })],
    ['ignore', get(ORIGIN + '/cosmos-ui/a%5cb', { mode: 'navigate' })],
    ['ignore', null],
    ['ignore', get(ORIGIN + '/cosmos-ui/assets/index-1a2b3c4d.js', { method: undefined })],
  ];
  for (const [verdict, request] of table) {
    await t.test(`${verdict}: ${request ? `${request.method} ${request.url} (${request.mode})` : 'no request'}`, () => {
      assert.equal(requestVerdict(request, ORIGIN, lists), verdict);
    });
  }
  await t.test('without lists nothing is a file of the build', () => {
    assert.equal(requestVerdict(get(ORIGIN + '/cosmos-ui/assets/index-1a2b3c4d.js'), ORIGIN, null), 'ignore');
    assert.equal(requestVerdict(get(ORIGIN + '/cosmos-ui/', { mode: 'navigate' }), ORIGIN, null), 'navigate');
  });
  await t.test('a path that climbs out of the panel is judged by where it ends', () => {
    // the browser resolves it before the worker sees it; the rule looks at the resolved path
    assert.equal(localAddress(ORIGIN + '/cosmos-ui/../cosmos/api/me', ORIGIN).path, '/cosmos/api/me');
    assert.equal(requestVerdict(get(ORIGIN + '/cosmos-ui/../cosmos/api/me'), ORIGIN, lists), 'ignore');
    assert.equal(requestVerdict(get(ORIGIN + '/cosmos-ui/assets/../../cosmos/api/me', { mode: 'navigate' }), ORIGIN, lists), 'ignore');
    assert.equal(requestVerdict(get(ORIGIN + '/cosmos/api/../../cosmos-ui/assets/index-1a2b3c4d.js'), ORIGIN, lists), 'asset');
  });
});

test('which answers may be kept', async (t) => {
  const ok = { status: 200, type: 'basic', redirected: false, contentType: 'text/javascript; charset=utf-8', cacheControl: '', setCookie: false, vary: '' };
  const js = '/cosmos-ui/assets/index-1a2b3c4d.js';
  const table = [
    [true, js, ok],
    [true, js, { ...ok, contentType: 'application/javascript' }],
    [true, js, { ...ok, cacheControl: 'public, max-age=31536000, immutable' }],
    [true, js, { ...ok, vary: 'Accept-Encoding' }],
    [true, '/cosmos-ui/assets/index-5e6f7a8b.css', { ...ok, contentType: 'text/css; charset=utf-8' }],
    [true, '/cosmos-ui/skin/theme.css?v=abc', { ...ok, contentType: 'text/css' }],
    [true, '/cosmos-ui/skin/inter.woff2', { ...ok, contentType: 'font/woff2' }],
    [true, '/cosmos-ui/pwa/icon-192.png', { ...ok, contentType: 'image/png' }],
    [true, '/cosmos-ui/assets/logo-11223344.svg', { ...ok, contentType: 'image/svg+xml' }],
    [true, '/cosmos-ui/offline.html', { ...ok, contentType: 'text/html; charset=utf-8' }],
    // the server answers a missing file with the panel's page and 200
    [false, js, { ...ok, contentType: 'text/html; charset=utf-8' }],
    [false, '/cosmos-ui/pwa/icon-192.png', { ...ok, contentType: 'text/html; charset=utf-8' }],
    [false, js, { ...ok, contentType: '' }],
    [false, js, { ...ok, contentType: null }],
    // anything but a complete, plain answer
    [false, js, { ...ok, status: 206 }],
    [false, js, { ...ok, status: 304 }],
    [false, js, { ...ok, status: 401 }],
    [false, js, { ...ok, status: 404 }],
    [false, js, { ...ok, status: 429 }],
    [false, js, { ...ok, status: 500 }],
    [false, js, { ...ok, status: 0, type: 'opaque' }],
    [false, js, { ...ok, type: 'cors' }],
    [false, js, { ...ok, type: 'opaqueredirect' }],
    [false, js, { ...ok, redirected: true }],
    // anything that speaks of a session or asks not to be kept
    [false, js, { ...ok, setCookie: true }],
    [false, js, { ...ok, cacheControl: 'no-store' }],
    [false, js, { ...ok, cacheControl: 'private, max-age=60' }],
    [false, js, { ...ok, vary: 'Cookie' }],
    [false, js, { ...ok, vary: 'Accept-Encoding, Authorization' }],
    [false, js, { ...ok, vary: '*' }],
    // names the worker has no business keeping
    [false, '/cosmos-ui/manifest.webmanifest', { ...ok, contentType: 'application/manifest+json' }],
    [false, '/cosmos-ui/data.json', { ...ok, contentType: 'application/json' }],
    [false, '/cosmos-ui/', { ...ok, contentType: 'text/html' }],
    [false, '/cosmos-ui/servapps', { ...ok, contentType: 'text/html' }],
    [false, js, null],
  ];
  for (const [verdict, key, response] of table) {
    const said = response ? Object.entries(response).filter(([name, value]) => ok[name] !== value).map(([name, value]) => `${name}=${value}`).join(' ') || 'plain' : 'no answer';
    await t.test(`${verdict ? 'kept' : 'not kept'}: ${key} (${said})`, () => assert.equal(storable(key, response), verdict));
  }
});

test('when the offline page stands in for an answer', () => {
  for (const status of [502, 504, 520, 521, 522, 523, 524, 525, 526, 527, 530]) assert.equal(serverUnreachable(status, false), true, String(status));
  // the server's own answers, and a proxy's challenge, always reach the visitor
  for (const status of [200, 301, 302, 307, 400, 401, 403, 404, 429, 500, 503, 519, 528]) assert.equal(serverUnreachable(status, false), false, String(status));
  assert.equal(serverUnreachable(502, true), false);
  assert.equal(serverUnreachable(521, true), false);
});

test('only the worker\'s own caches of other builds are deleted', () => {
  const current = PWA_CACHE_PREFIX + 'fork.12-abc';
  assert.deepEqual(staleCaches([current, PWA_CACHE_PREFIX + 'fork.11-def', 'cosmos-ui-', 'jellyfin-cache', 'workbox-precache-v2', 42, null], current),
    [PWA_CACHE_PREFIX + 'fork.11-def', 'cosmos-ui-']);
  assert.deepEqual(staleCaches(null, current), []);
});

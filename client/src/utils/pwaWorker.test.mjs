// Fork addition: the service worker as the build assembles it, run here with
// a stand-in for the browser (its caches, the network, the events), to see
// what it fetches, what it keeps and what it leaves alone.
// node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import { assembleWorker, buildLists, readSources, referencedBy, versionOf } from '../../sw/build.mjs';

const ORIGIN = 'https://panel.example:2053';
const INDEX = `<!DOCTYPE html><html><head>
  <link rel="icon" href="/cosmos-ui/assets/logo2-1273cf0b.png">
  <link rel="manifest" href="/cosmos-ui/manifest.webmanifest">
  <link rel="apple-touch-icon" href="/cosmos-ui/pwa/apple-touch-icon.png">
  <script id="ct-mode" src="/cosmos-ui/skin/mode.js?v=9396121cb3"></script>
  <script type="module" crossorigin src="/cosmos-ui/assets/index-1201de92.js"></script>
  <link rel="stylesheet" href="/cosmos-ui/assets/index-6b196269.css">
  <a href="https://elsewhere.example/cosmos-ui/assets/x.js">x</a><a href="/cosmos/api/status">s</a>
  </head><body><link rel="stylesheet" id="ct-theme" href="/cosmos-ui/skin/theme.css?v=9396121cb3"></body></html>`;
const FILES = [
  'index.html', 'sw.js', 'manifest.webmanifest', 'offline.html',
  'assets/index-1201de92.js', 'assets/index-6b196269.css', 'assets/logo2-1273cf0b.png',
  'assets/translation-ed6ed6ed.js', 'assets/wallpaper2_light-d620ecec.jpg', 'assets/discord-aabbccdd.svg',
  'skin/mode.js', 'skin/theme.css', 'skin/inter.woff2',
  'assets/offline-aa11bb22.js', 'assets/offline-cc33dd44.css', 'pwa/apple-touch-icon.png', 'pwa/icon-192.png', 'pwa/icon-512.png', 'pwa/screenshot-wide.png', 'pwa/screenshot-narrow.png',
  'assets/data-11223344.json', 'assets/notes.txt',
];
const OFFLINE = `<!DOCTYPE html><html><head>
  <script id="ct-mode" src="/cosmos-ui/skin/mode.js?v=9396121cb3"></script>
  <script type="module" crossorigin src="/cosmos-ui/assets/offline-aa11bb22.js"></script>
  <link rel="stylesheet" href="/cosmos-ui/assets/offline-cc33dd44.css">
  <link rel="stylesheet" href="/cosmos-ui/skin/theme.css?v=9396121cb3"></head><body></body></html>`;
const lists = buildLists([INDEX, OFFLINE], FILES);
const sources = readSources();
const VERSION = versionOf('12', lists, [sources.template, sources.policy]);
const SOURCE = assembleWorker(sources, lists, VERSION);

const TYPE = { js: 'text/javascript; charset=utf-8', css: 'text/css; charset=utf-8', png: 'image/png', jpg: 'image/jpeg', svg: 'image/svg+xml', woff2: 'font/woff2', html: 'text/html; charset=utf-8' };
const typeOf = (key) => TYPE[key.split('?')[0].split('.').pop()] || 'application/octet-stream';

// A browser, as far as the worker can tell.
const browser = (options = {}) => {
  const handlers = {};
  const stores = new Map(options.caches || []);
  const asked = [];
  const state = { claimed: 0, skipped: 0 };
  const basic = (body, init) => {
    const response = new Response(body, init);
    Object.defineProperty(response, 'type', { value: 'basic' });
    return response;
  };
  const server = options.server || ((key) => basic('content of ' + key, { status: 200, headers: { 'Content-Type': typeOf(key) } }));
  const fetchStub = async (input) => {
    const url = typeof input === 'string' ? input : input.url;
    const key = url.replace(ORIGIN, '');
    asked.push({ key, credentials: input.credentials, mode: input.mode, redirect: input.redirect, cache: input.cache });
    if (options.offline) throw new TypeError('Failed to fetch');
    return server(key, input);
  };
  const cacheOf = (name) => {
    if (!stores.has(name)) stores.set(name, new Map());
    const map = stores.get(name);
    return {
      match: async (url) => (map.has(url) ? map.get(url).clone() : undefined),
      put: async (url, response) => { map.set(url, response); },
    };
  };
  const self = {
    location: { origin: ORIGIN },
    addEventListener: (name, handler) => { handlers[name] = handler; },
    clients: { claim: async () => { state.claimed++; } },
    skipWaiting: () => { state.skipped++; },
  };
  const context = vm.createContext({
    self, URL, Request, Response, Headers, Promise, Set, JSON, Error, TypeError,
    fetch: fetchStub,
    caches: { open: async (name) => cacheOf(name), keys: async () => [...stores.keys()], delete: async (name) => stores.delete(name) },
  });
  vm.runInContext(SOURCE, context, { filename: 'sw.js' });

  const lifecycle = async (name) => {
    let work = null;
    handlers[name]({ waitUntil: (promise) => { work = promise; } });
    await work;
  };
  // returns null when the worker leaves the request to the browser
  const request = async (key, init = {}) => {
    const headers = new Headers(init.headers || {});
    let answer = null;
    handlers.fetch({
      request: { method: init.method || 'GET', url: key.startsWith('http') ? key : ORIGIN + key, mode: init.mode || 'cors', headers },
      respondWith: (promise) => { answer = promise; },
    });
    return answer;
  };
  const message = (data) => new Promise((resolve) => {
    // (the answer is an object of the worker's own world: copy it into this one)
    const port = { postMessage: (answer) => resolve({ ...answer }) };
    handlers.message({ data, ports: [port] });
    if (!data || data.type !== 'ABOUT') resolve(undefined);
  });
  const kept = () => Object.fromEntries([...stores].map(([name, map]) => [name, [...map.keys()].map((url) => url.replace(ORIGIN, '')).sort()]));
  return { install: () => lifecycle('install'), activate: () => lifecycle('activate'), request, message, kept, asked, state, stores, basic };
};

const CACHE = 'cosmos-ui-' + VERSION;

test('the lists come from the build: what index.html asks for at once, the rest when used', () => {
  assert.deepEqual(referencedBy(INDEX), [
    '/cosmos-ui/assets/logo2-1273cf0b.png', '/cosmos-ui/manifest.webmanifest', '/cosmos-ui/pwa/apple-touch-icon.png',
    '/cosmos-ui/skin/mode.js?v=9396121cb3', '/cosmos-ui/assets/index-1201de92.js', '/cosmos-ui/assets/index-6b196269.css',
    '/cosmos-ui/skin/theme.css?v=9396121cb3',
  ]);
  assert.deepEqual(lists.core, [
    '/cosmos-ui/assets/index-1201de92.js', '/cosmos-ui/assets/index-6b196269.css', '/cosmos-ui/assets/logo2-1273cf0b.png',
    '/cosmos-ui/assets/offline-aa11bb22.js', '/cosmos-ui/assets/offline-cc33dd44.css',
    '/cosmos-ui/offline.html', '/cosmos-ui/pwa/apple-touch-icon.png',
    '/cosmos-ui/skin/inter.woff2', '/cosmos-ui/skin/mode.js?v=9396121cb3', '/cosmos-ui/skin/theme.css?v=9396121cb3',
  ]);
  assert.deepEqual(lists.later, [
    '/cosmos-ui/assets/discord-aabbccdd.svg', '/cosmos-ui/assets/translation-ed6ed6ed.js', '/cosmos-ui/assets/wallpaper2_light-d620ecec.jpg',
    '/cosmos-ui/pwa/icon-192.png', '/cosmos-ui/pwa/icon-512.png',
  ]);
  // never listed: the page itself, the manifest, the worker, the install pictures, files of other kinds
  const all = [...lists.core, ...lists.later].join(' ');
  for (const name of ['index.html', 'sw.js', 'manifest.webmanifest', 'screenshot', '.json', '.txt', '/cosmos/']) assert.ok(!all.includes(name), name);
  assert.throws(() => buildLists([INDEX, OFFLINE], FILES.filter((file) => file !== 'offline.html')), /offline\.html/);
  // one page given as a string works too
  assert.ok(buildLists(INDEX, FILES).core.includes('/cosmos-ui/offline.html'));
});

test('the version names the build and changes with anything the worker is made of', () => {
  assert.match(VERSION, /^fork\.12-[0-9a-f]{12}$/);
  assert.match(versionOf('', lists, ['a']), /^[0-9a-f]{12}$/);
  assert.notEqual(versionOf('12', lists, ['a']), versionOf('12', lists, ['b']));
  assert.notEqual(versionOf('12', lists, ['a']), versionOf('12', { ...lists, later: [] }, ['a']));
  assert.equal(versionOf('12"; alert(1)//', lists, ['a']).includes('"'), false);
});

test('the worker is one plain script of its own: nothing imported, nothing from elsewhere', () => {
  assert.equal(/\bimportScripts\s*\(/.test(SOURCE), false);
  assert.equal(/^\s*(import|export)\s/m.test(SOURCE), false);
  assert.equal(/https?:\/\//.test(SOURCE.replace(/\/\/[^\n]*/g, '')), false, 'no address of another origin in the code');
  assert.equal(SOURCE.includes('__'), false, 'every mark was replaced');
  assert.ok(SOURCE.split('\n').length < 320, 'small enough to read in one sitting');
});

test('installing fetches the start files without credentials and keeps exactly those', async () => {
  const b = browser();
  await b.install();
  assert.deepEqual(b.asked.map((a) => a.key).sort(), lists.core);
  for (const a of b.asked) {
    assert.equal(a.credentials, 'omit', a.key);
    assert.equal(a.redirect, 'error', a.key);
    assert.equal(a.cache, a.key.startsWith('/cosmos-ui/assets/') ? 'default' : 'no-cache', a.key);
  }
  assert.deepEqual(b.kept(), { [CACHE]: lists.core });
});

test('installing fails as a whole when a start file is missing (the server answers with the panel page)', async () => {
  const b = browser({ server: (key) => {
    const response = new Response('<html>', { status: 200, headers: { 'Content-Type': key.includes('index-1201de92.js') ? TYPE.html : typeOf(key) } });
    Object.defineProperty(response, 'type', { value: 'basic' });
    return response;
  } });
  await assert.rejects(b.install(), /not kept: \/cosmos-ui\/assets\/index-1201de92\.js/);
  assert.equal((b.kept()[CACHE] || []).includes('/cosmos-ui/assets/index-1201de92.js'), false);
  await assert.rejects(browser({ offline: true }).install());
});

test('activating deletes the caches of earlier builds, only its own, and takes the open pages', async () => {
  const b = browser({ caches: [['cosmos-ui-fork.11-000000000000', new Map([[ORIGIN + '/cosmos-ui/assets/old.js', new Response('old')]])], ['jellyfin-web', new Map()]] });
  await b.install();
  await b.activate();
  assert.deepEqual(Object.keys(b.kept()).sort(), [CACHE, 'jellyfin-web'].sort());
  assert.equal(b.state.claimed, 1);
});

test('the API, the logs, OAuth, applications and other origins are left to the browser', async () => {
  const b = browser();
  await b.install();
  b.asked.length = 0;
  for (const [key, init] of [
    ['/cosmos/api/status'], ['/cosmos/api/me'], ['/cosmos/api/config'], ['/cosmos/api/servapps/jellyfin/logs'],
    ['/cosmos/api/login', { method: 'POST' }], ['/cosmos/api/logout', { mode: 'navigate' }], ['/cosmos/oauth2/auth', { mode: 'navigate' }],
    ['/cosmos/_logs'], ['/cosmos/rclone/config/dump', { method: 'POST' }], ['/', { mode: 'navigate' }], ['/jellyfin/', { mode: 'navigate' }],
    ['https://jellyfin.panel.example:2053/web/index.html', { mode: 'navigate' }], ['https://other.example/cosmos-ui/assets/index-1201de92.js'],
    ['/cosmos-ui/assets/index-1201de92.js', { headers: { Authorization: 'Bearer x' } }], ['/cosmos-ui/assets/index-1201de92.js', { headers: { Range: 'bytes=0-9' } }],
    ['/cosmos-ui/manifest.webmanifest'], ['/cosmos-ui/sw.js'], ['/cosmos-ui/assets/of-another-build-ffffffff.js'], ['/cosmos-ui/pwa/screenshot-wide.png'],
  ]) {
    assert.equal(await b.request(key, init), null, key);
  }
  assert.deepEqual(b.asked, [], 'the worker itself asked the network for nothing');
});

test('a start file comes from the cache without the network', async () => {
  const b = browser();
  await b.install();
  b.asked.length = 0;
  const answer = await b.request('/cosmos-ui/assets/index-1201de92.js');
  assert.equal(await answer.text(), 'content of /cosmos-ui/assets/index-1201de92.js');
  assert.equal((await b.request('/cosmos-ui/skin/theme.css?v=9396121cb3')).status, 200);
  assert.deepEqual(b.asked, []);
});

test('another file of the build is kept when first used, and only if it is what its name says', async () => {
  const b = browser();
  await b.install();
  b.asked.length = 0;
  const key = '/cosmos-ui/assets/translation-ed6ed6ed.js';
  assert.equal(await (await b.request(key)).text(), 'content of ' + key);
  assert.equal(b.asked.length, 1);
  assert.equal(b.asked[0].credentials, 'omit');
  assert.equal(await (await b.request(key)).text(), 'content of ' + key);
  assert.equal(b.asked.length, 1, 'the second time from the cache');
  assert.ok(b.kept()[CACHE].includes(key));

  const wrong = browser({ server: (k) => { const r = new Response('<html>', { status: 200, headers: { 'Content-Type': k.endsWith('.svg') ? TYPE.html : typeOf(k) } }); Object.defineProperty(r, 'type', { value: 'basic' }); return r; } });
  await wrong.install();
  const svg = '/cosmos-ui/assets/discord-aabbccdd.svg';
  assert.equal((await wrong.request(svg)).status, 200, 'the answer is passed on');
  assert.equal(wrong.kept()[CACHE].includes(svg), false, 'but not kept');
});

test('a page of the panel always comes from the network, untouched', async () => {
  for (const status of [200, 302, 401, 403, 404, 429, 500, 503]) {
    const made = new Response(status === 302 ? null : 'page', { status, headers: { 'Content-Type': TYPE.html, 'Set-Cookie': 'x=1' } });
    const b = browser({ server: (key) => (key.startsWith('/cosmos-ui/servapps') ? made : new Response('content', { status: 200, headers: { 'Content-Type': typeOf(key) } })) });
    // (the stand-in server above gives start files a type the worker would not keep, so install by hand)
    const answer = await b.request('/cosmos-ui/servapps', { mode: 'navigate' });
    assert.equal(answer, made, 'the very answer of the server for ' + status);
    assert.deepEqual(b.kept(), {}, 'and nothing of it is kept');
  }
});

test('no connection, or a proxy saying the server is gone: the offline page, as an error', async () => {
  const b = browser();
  await b.install();
  const offline = browser({ offline: true, caches: [...b.stores] });
  const answer = await offline.request('/cosmos-ui/servapps', { mode: 'navigate' });
  assert.equal(answer.status, 503);
  assert.equal(answer.headers.get('Cache-Control'), 'no-store');
  assert.equal(await answer.text(), 'content of /cosmos-ui/offline.html');
  // without a kept offline page the browser shows its own error
  await assert.rejects(browser({ offline: true }).request('/cosmos-ui/', { mode: 'navigate' }));

  for (const [status, headers, expected] of [[521, {}, 503], [502, {}, 503], [504, {}, 503], [521, { 'cf-mitigated': 'challenge' }, 521], [503, {}, 503], [403, { 'cf-mitigated': 'challenge' }, 403]]) {
    const proxy = browser({ caches: [...b.stores], server: () => new Response('from the proxy', { status, headers }) });
    const got = await proxy.request('/cosmos-ui/', { mode: 'navigate' });
    assert.equal(got.status, expected);
    const text = await got.text();
    assert.equal(text.startsWith('content of /cosmos-ui/offline.html'), status !== 503 && expected === 503, `${status} ${JSON.stringify(headers)}`);
  }
});

test('after a whole session the caches hold files of the build and nothing else', async () => {
  const b = browser();
  await b.install();
  await b.activate();
  const session = [
    ['/cosmos-ui/login', { mode: 'navigate' }], ['/cosmos/api/login', { method: 'POST' }], ['/cosmos/api/me'], ['/cosmos/api/status'],
    ['/cosmos-ui/', { mode: 'navigate' }], ['/cosmos-ui/assets/index-1201de92.js'], ['/cosmos-ui/assets/translation-ed6ed6ed.js'],
    ['/cosmos/api/servapps'], ['/cosmos/api/config'], ['/cosmos/api/users'], ['/cosmos/api/api-tokens'], ['/cosmos/api/background/avif'],
    ['/cosmos/api/favicon?q=http://jellyfin:8096'], ['/cosmos-ui/assets/wallpaper2_light-d620ecec.jpg'], ['/cosmos-ui/assets/discord-aabbccdd.svg'],
    ['/cosmos-ui/servapps/containers/jellyfin', { mode: 'navigate' }], ['/cosmos/api/servapps/jellyfin/logs?limit=100'],
    ['/cosmos-ui/config-general', { mode: 'navigate' }], ['/cosmos/api/sudo', { method: 'POST' }], ['/cosmos/api/config', { method: 'PUT' }],
    ['/cosmos-ui/logout', { mode: 'navigate' }], ['/cosmos/api/logout'], ['/cosmos-ui/login', { mode: 'navigate' }],
  ];
  for (const [key, init] of session) { const answer = await b.request(key, init); if (answer) await answer; }
  const kept = b.kept();
  assert.deepEqual(Object.keys(kept), [CACHE]);
  const allowed = new Set([...lists.core, ...lists.later]);
  for (const key of kept[CACHE]) {
    assert.ok(allowed.has(key), key + ' is a file of the build');
    assert.equal(key.startsWith('/cosmos/'), false);
    assert.equal(/\.html$/.test(key) && key !== '/cosmos-ui/offline.html', false, 'no page of the panel');
  }
});

test('the page can ask the worker to take over and which build it is', async () => {
  const b = browser();
  await b.message({ type: 'SKIP_WAITING' });
  assert.equal(b.state.skipped, 1);
  assert.deepEqual(await b.message({ type: 'ABOUT', url: ORIGIN + '/cosmos-ui/assets/index-1201de92.js' }), { version: VERSION, known: true });
  assert.deepEqual(await b.message({ type: 'ABOUT', url: ORIGIN + '/cosmos-ui/assets/index-00000000.js' }), { version: VERSION, known: false });
  assert.deepEqual(await b.message({ type: 'ABOUT', url: 'https://other.example/cosmos-ui/assets/index-1201de92.js' }), { version: VERSION, known: false });
  assert.deepEqual(await b.message({ type: 'ABOUT' }), { version: VERSION, known: false });
  await b.message({ type: 'SOMETHING_ELSE' });
  await b.message(null);
  assert.equal(b.state.skipped, 1);
});

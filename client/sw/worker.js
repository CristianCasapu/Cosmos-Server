// Fork addition: the panel's service worker. Its whole job: make the panel
// installable as an app, start it fast (the build's own files come from a
// cache), show a page in the panel's look when the server cannot be reached,
// and step aside for a newer build.
//
// It keeps ONLY files of the panel's build, each named in a list written at
// build time (client/sw/build.mjs): the files the page needs to start are
// fetched when the worker is installed, the others when they are first used.
// It never answers, keeps or changes anything under /cosmos/ (the API, logs,
// terminals, OAuth), anything of an application or anything of a session:
// the rules are in client/src/utils/pwaPolicy.js, whose text the build puts
// here in place of the next line, and are tested there.
/* __POLICY__ */

const VERSION = '__VERSION__';
const CORE = new Set(/* __CORE__ */[]);
const LATER = new Set(/* __LATER__ */[]);
const LISTS = { core: CORE, later: LATER };
const CACHE = PWA_CACHE_PREFIX + VERSION;
const ORIGIN = self.location.origin;

const describe = (response) => ({
  status: response.status,
  type: response.type,
  redirected: response.redirected,
  contentType: response.headers.get('Content-Type'),
  cacheControl: response.headers.get('Cache-Control'),
  setCookie: response.headers.has('Set-Cookie'),
  vary: response.headers.get('Vary'),
});

// A file of the build, asked without cookies or any other credential: the
// answer cannot depend on who is signed in. Files with a content hash in
// their name may come from the browser's own cache; the others are asked anew.
const fetchBuildFile = (key) => fetch(new Request(ORIGIN + key, {
  credentials: 'omit',
  redirect: 'error',
  cache: key.startsWith(PWA_SCOPE + 'assets/') ? 'default' : 'no-cache',
}));

const precache = async () => {
  const cache = await caches.open(CACHE);
  await Promise.all([...CORE].map(async (key) => {
    const response = await fetchBuildFile(key);
    if (!storable(key, describe(response))) throw new Error('not kept: ' + key + ' (' + response.status + ')');
    await cache.put(ORIGIN + key, response);
  }));
};

self.addEventListener('install', (event) => {
  // a failure leaves the worker of the previous build in charge; the browser tries again later
  event.waitUntil(precache());
});

self.addEventListener('activate', (event) => {
  event.waitUntil((async () => {
    const names = await caches.keys();
    await Promise.all(staleCaches(names, CACHE).map((name) => caches.delete(name)));
    await self.clients.claim();
  })());
});

const buildFile = async (request, key) => {
  const cache = await caches.open(CACHE);
  const kept = await cache.match(ORIGIN + key);
  if (kept) return kept;
  if (!LATER.has(key)) return fetch(request);
  let response;
  try {
    response = await fetchBuildFile(key);
  } catch (error) {
    // asked the plain way, the browser deals with whatever was in the way
    return fetch(request);
  }
  if (storable(key, describe(response))) {
    // keeping it is a convenience: a full disk must not fail the request
    cache.put(ORIGIN + key, response.clone()).catch(() => {});
  }
  return response;
};

const offlinePage = async () => {
  const cache = await caches.open(CACHE);
  const page = await cache.match(ORIGIN + PWA_OFFLINE_PAGE);
  if (!page) return null;
  // 503, so that nothing takes it for the page that was asked for
  return new Response(page.body, { status: 503, statusText: 'Server unreachable', headers: { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' } });
};

// A page of the panel: always from the network, so that a new release and
// the server's own answers (redirects, refusals) arrive untouched. Only when
// there is no answer at all, or a proxy says the server is not there, the
// offline page is shown instead of the browser's error.
const navigation = async (request) => {
  let response;
  try {
    response = await fetch(request);
  } catch (error) {
    const page = await offlinePage();
    if (page) return page;
    throw error;
  }
  if (serverUnreachable(response.status, response.headers.has('cf-mitigated'))) {
    const page = await offlinePage();
    if (page) return page;
  }
  return response;
};

self.addEventListener('fetch', (event) => {
  const request = event.request;
  const verdict = requestVerdict({
    method: request.method,
    url: request.url,
    mode: request.mode,
    authorization: request.headers.has('Authorization'),
    range: request.headers.has('Range'),
  }, ORIGIN, LISTS);
  if (verdict === 'asset') event.respondWith(buildFile(request, localAddress(request.url, ORIGIN).key));
  else if (verdict === 'navigate') event.respondWith(navigation(request));
  // 'ignore': no respondWith, the browser does what it does without a worker
});

// The page talks to the worker for two things: "take over now" (the admin
// pressed Reload on the notice of a new version) and "which build are you,
// and is this file part of it" (to tell whether the page itself is current).
self.addEventListener('message', (event) => {
  const data = event.data || {};
  if (data.type === 'SKIP_WAITING') {
    self.skipWaiting();
  } else if (data.type === 'ABOUT' && event.ports && event.ports[0]) {
    const address = typeof data.url === 'string' ? localAddress(data.url, ORIGIN) : null;
    event.ports[0].postMessage({ version: VERSION, known: !!address && (CORE.has(address.key) || LATER.has(address.key)) });
  }
});

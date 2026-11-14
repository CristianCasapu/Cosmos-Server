// Fork addition: what the panel's service worker may answer and may keep.
// Pure functions without imports: the build puts this very text into the
// worker (client/sw/build.mjs strips the word "export"), and the tests in
// pwaPolicy.test.mjs run it with a table of addresses and answers.
//
// The rule in one sentence: the worker answers a page navigation inside the
// panel (from the network, with the offline page when the server cannot be
// reached) and a file of the panel's own build; everything else passes as if
// there were no worker. Nothing under /cosmos/ (the API, logs, terminals,
// OAuth), nothing of an application, nothing with a session in it.

export const PWA_SCOPE = '/cosmos-ui/';
export const PWA_CACHE_PREFIX = 'cosmos-ui-';
export const PWA_OFFLINE_PAGE = '/cosmos-ui/offline.html';

// The path and query of an address of this origin, or null for another origin
// or something that is not an address.
export const localAddress = (url, origin) => {
  let parsed;
  try {
    parsed = new URL(String(url), origin);
  } catch (e) {
    return null;
  }
  if (parsed.origin !== origin) return null;
  if (parsed.username || parsed.password) return null;
  return { path: parsed.pathname, key: parsed.pathname + parsed.search };
};

// What to do with a request of a page the worker controls:
//   'navigate'  a page of the panel is being opened: network, offline page on failure
//   'asset'     a file of the build: from the cache, else from the network
//   'ignore'    not the worker's business: the browser handles it by itself
// request: { method, url, mode, authorization, range }
// lists:   { core, later }: the build's files as sets of "path?query"
export const requestVerdict = (request, origin, lists) => {
  if (!request || request.method !== 'GET') return 'ignore';
  const address = localAddress(request.url, origin);
  if (!address) return 'ignore';
  const path = address.path;
  // said first and on its own, so that no later rule can ever let it through
  if (path === '/cosmos' || path.startsWith('/cosmos/')) return 'ignore';
  if (!path.startsWith(PWA_SCOPE)) return 'ignore';
  // an encoded slash, backslash or dot could name another path to a server that decodes it
  if (/%2f|%5c|%2e|\\/i.test(path) || path.includes('/../') || path.includes('/./')) return 'ignore';
  if (request.authorization || request.range) return 'ignore';
  const listed = !!lists && ((lists.core && lists.core.has(address.key)) || (lists.later && lists.later.has(address.key)));
  if (request.mode === 'navigate') {
    // a file of the build opened in a tab of its own is a file, not a page of the panel
    return listed ? 'asset' : 'navigate';
  }
  return listed ? 'asset' : 'ignore';
};

const TYPES = {
  js: /^(text|application)\/javascript\b/,
  css: /^text\/css\b/,
  html: /^text\/html\b/,
  woff2: /^(font\/woff2|application\/font-woff2|application\/octet-stream)\b/,
  png: /^image\/png\b/,
  jpg: /^image\/jpeg\b/,
  svg: /^image\/svg\+xml\b/,
};

// May this answer be put in the cache under this address? Only a complete,
// plain, same-origin answer whose type is the one its name promises: the
// server answers a missing file with the panel's HTML page and status 200,
// and that must never be kept as a script or a picture.
// response: { status, type, redirected, contentType, cacheControl, setCookie, vary }
export const storable = (key, response) => {
  if (!response || response.status !== 200) return false;
  if (response.type !== 'basic' || response.redirected) return false;
  if (response.setCookie) return false;
  const cacheControl = String(response.cacheControl || '').toLowerCase();
  if (/\b(no-store|private)\b/.test(cacheControl)) return false;
  const vary = String(response.vary || '').toLowerCase();
  if (/(^|,)\s*(\*|cookie|authorization)\s*(,|$)/.test(vary)) return false;
  const path = String(key || '').split('?')[0];
  const extension = (/\.([a-z0-9]+)$/i.exec(path) || [])[1];
  const wanted = extension && TYPES[extension.toLowerCase()];
  if (!wanted) return false;
  return wanted.test(String(response.contentType || '').toLowerCase().trim());
};

// Answers to a page navigation that mean "the server is not there", as a
// proxy in front of it words them (Cloudflare's 52x, a gateway's 502/504).
// 503 is left alone: it is also what a challenge page of the proxy may use,
// and that page must reach the visitor.
export const serverUnreachable = (status, challenged) => {
  if (challenged) return false;
  return status === 502 || status === 504 || (status >= 520 && status <= 527) || status === 530;
};

// The caches of earlier builds: the worker's own, by their prefix, never
// another program's.
export const staleCaches = (names, current) => (names || []).filter((name) => typeof name === 'string'
  && name.startsWith(PWA_CACHE_PREFIX) && name !== current);

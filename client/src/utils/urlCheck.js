// Fork addition (plan 44): what the panel makes of the URL check
// (src/behind): the words of a result, the repairs in the order they are
// offered, which URLs of an app share a repair, and what a known app needs
// when a URL is made for it. Nothing here imports the UI or the API.

// "lscr.io/linuxserver/qbittorrent:latest" → "linuxserver/qbittorrent"; the
// same rule as imageKey in src/behind/apps.go.
export const imageKey = (image) => {
  let text = String(image || '').trim().toLowerCase();
  const at = text.indexOf('@');
  if (at >= 0) text = text.slice(0, at);
  const colon = text.lastIndexOf(':');
  if (colon > text.lastIndexOf('/')) text = text.slice(0, colon);
  let parts = text.split('/').filter((p, i, all) => p !== '' || all.length === 1);
  if (parts.length > 1 && (/[.:]/.test(parts[0]) || parts[0] === 'localhost')) parts = parts.slice(1);
  if (parts.length > 1 && parts[0] === 'library') parts = parts.slice(1);
  return parts.join('/');
};

// The known app of an image, from GET /api/known-apps, or null.
export const knownAppFor = (apps, image) => {
  const key = imageKey(image);
  if (!key) return null;
  return (Array.isArray(apps) ? apps : []).find((app) => (app.images || []).includes(key)) || null;
};

// The address a URL is reached at, as the panel's links show it.
export const publicFacts = (host, port, scheme = 'https') => {
  const name = String(host || '').trim().toLowerCase().replace(/:\d+$/, '');
  const p = String(port || (scheme === 'https' ? 443 : 80));
  const withPort = (scheme === 'https' && p === '443') || (scheme === 'http' && p === '80') ? name : name + ':' + p;
  return { name, port: p, host: withPort, url: scheme + '://' + withPort };
};

// A value of the data with the address filled in ({proxy} is known to the server only).
export const fillValue = (template, facts, others = []) => {
  const all = [facts, ...others].filter((f) => f && f.name);
  return String(template || '')
    .replace(/\{urls\}/g, all.map((f) => f.url).join(','))
    .replace(/\{hosts\}/g, all.map((f) => f.host).join(','))
    .replace(/\{names\}/g, all.map((f) => f.name).join(','))
    .replace(/\{url\}/g, facts.url || '')
    .replace(/\{host\}/g, facts.host || '')
    .replace(/\{name\}/g, facts.name || '')
    .replace(/\{port\}/g, facts.port || '');
};

// What a known app needs when a URL is made for it: the URL setting and the
// variables it must have (when "always"), filled for the address.
export const knownAppNeeds = (app, facts, others = []) => {
  if (!app) return null;
  return {
    appOwnHost: !!app.appOwnHost,
    env: (app.env || []).filter((e) => e.when === 'always' && facts && facts.name)
      .map((e) => ({ name: e.name, value: fillValue(e.value, facts, others), why: e.why || '' })),
  };
};

// How much a result asks for attention: "error", "warning" or null.
export const attentionOf = (result) => {
  if (!result) return null;
  if (result.verdict === 'refused' || result.verdict === 'wrongAddress') return 'error';
  if (result.verdict === 'warning') return 'warning';
  return null;
};

const REASONS = {
  hostPort: ['mgmt.urlCheck.reason.hostPort', '{{app}} refuses the address it is reached at ({{url}}): it only answers requests made to its own address and port. Cosmos can tell it its own address and pass the public one alongside.'],
  hostNotAllowed: ['mgmt.urlCheck.reason.hostNotAllowed', '{{app}} refuses {{url}}: the name is not on its list of allowed names.'],
  untrustedDomain: ['mgmt.urlCheck.reason.untrustedDomain', '{{app}} refuses {{url}}: the name is not one of its trusted domains.'],
  untrustedProxy: ['mgmt.urlCheck.reason.untrustedProxy', '{{app}} refuses requests that come through a proxy it does not know: it has to be told that Cosmos is its proxy.'],
  csrfOrigin: ['mgmt.urlCheck.reason.csrfOrigin', '{{url}} opens, but signing in to {{app}} will fail: {{app}} has to be told the address it is reached at.'],
  wrongAddress: ['mgmt.urlCheck.reason.wrongAddress', '{{app}} sends visitors to another address than {{url}}: it does not know the address it is reached at.'],
  noVisitorBreaks: ['mgmt.urlCheck.reason.noVisitorBreaks', '{{app}} needs the visitor\'s address, and this URL does not tell it: the setting "Do not tell the app the visitor\'s address" is wrong for it.'],
  httpLinks: ['mgmt.urlCheck.reason.httpLinks', '{{url}} opens, but {{app}} makes links that start with http:// instead of https://: it has to be told that it is reached over HTTPS.'],
  rootUrl: ['mgmt.urlCheck.reason.rootUrl', '{{url}} opens, but {{app}} names another address (localhost) in its links: it has to be told the address it is reached at.'],
  ownHostBreaks: ['mgmt.urlCheck.reason.ownHostBreaks', '{{app}} refuses its own address and answers the public one: the setting "Tell the app its own address" is wrong for it.'],
  refused: ['mgmt.urlCheck.reason.refused', '{{app}} answers {{url}} with an error ({{status}}). Cosmos does not recognise why: the app\'s own words are below.'],
  sameAnswer: ['mgmt.urlCheck.reason.sameAnswer', '{{app}} answers {{status}} whatever address it is told: that is its own answer (a sign-in it asks for, or a page it does not have), not a refusal of the address.'],
  noRoute: ['mgmt.urlCheck.reason.noRoute', 'This URL does not exist any more.'],
  notContainer: ['mgmt.urlCheck.reason.notContainer', 'This URL does not lead to an app of this server: Cosmos checks only apps in containers here.'],
  notWeb: ['mgmt.urlCheck.reason.notWeb', 'This URL does not lead to a web page (http or https).'],
  noName: ['mgmt.urlCheck.reason.noName', 'This URL has no name of its own, or it sets the name the app receives by hand: Cosmos does not check it.'],
  hostNetwork: ['mgmt.urlCheck.reason.hostNetwork', '{{app}} runs on the server\'s own network: Cosmos does not ask it directly.'],
  missing: ['mgmt.urlCheck.reason.missing', 'The app of this URL is not on this server.'],
  notRunning: ['mgmt.urlCheck.reason.notRunning', '{{app}} is not running.'],
  unreachable: ['mgmt.urlCheck.reason.unreachable', '{{app}} could not be reached on its port.'],
  timeout: ['mgmt.urlCheck.reason.timeout', '{{app}} did not answer in time.'],
  portRefused: ['mgmt.urlCheck.reason.portRefused', 'Nothing answers on the port this URL leads to: check the port of the URL.'],
  tooMany: ['mgmt.urlCheck.reason.tooMany', 'Too many checks in a minute: check again in a moment.'],
  works: ['mgmt.urlCheck.reason.works', '{{url}} opens: {{app}} accepts the address.'],
  answers: ['mgmt.urlCheck.reason.answers', '{{url}} reaches {{app}}.'],
};

// The words of a result: { severity, key, text, params }.
export const resultWords = (result) => {
  const r = result || {};
  const first = (r.attempts || [])[0] || {};
  const params = { app: r.appName || r.container || 'The app', url: r.url || r.route || '', status: first.status || '' };
  let id = r.reason;
  if (r.verdict === 'works') id = 'works';
  if (r.verdict === 'answers') id = 'sameAnswer';
  if (r.verdict === 'unreachable' && r.reason === 'refused') id = 'portRefused';
  if (r.verdict === 'notRunning' && (!id || id === 'notRunning')) id = 'notRunning';
  if (r.verdict === 'notRunning' && id === 'unreachable') id = 'unreachable';
  if (!REASONS[id]) id = r.verdict === 'refused' ? 'refused' : (r.verdict === 'works' ? 'works' : 'unreachable');
  const severity = { works: 'success', answers: 'info', warning: 'warning', refused: 'error', wrongAddress: 'error' }[r.verdict] || 'info';
  const [key, text] = REASONS[id];
  return { severity, key, text, params, id };
};

// The repairs, in the order they are offered: the URL setting (seen working
// first), the app's variables, its configuration lines and commands, then
// advice (variables and lines the app is better with).
export const orderedRepairs = (result) => {
  const all = (result && result.repairs) || [];
  const route = all.filter((r) => r.kind === 'route' && (r.setting === 'appOwnHost' || r.setting === 'noVisitorAddress'))
    .sort((a, b) => Number(!!b.seen) - Number(!!a.seen));
  const advice = (r) => r.when === 'advice';
  return {
    route: route[0] || null,
    env: all.filter((r) => r.kind === 'env' && !advice(r)),
    file: all.filter((r) => (r.kind === 'file' || r.kind === 'command') && !advice(r)),
    advice: all.filter((r) => (r.kind === 'file' || r.kind === 'command' || r.kind === 'env') && advice(r)),
  };
};

// Why a repair is proposed, in words (the data's `why` ids).
const WHYS = {
  visitorAddressQbt: ['mgmt.urlCheck.why.visitorAddressQbt', 'qBittorrent bans an address after 5 wrong passwords. Without these lines that address is Cosmos\' own, so a stranger\'s wrong guesses lock everybody out.'],
  visitorAddressArr: ['mgmt.urlCheck.why.visitorAddressArr', 'Without this the app sees every visitor as Cosmos, which is on the local network: "authentication disabled for local addresses" would then let the internet in.'],
  visitorAddressJellyfin: ['mgmt.urlCheck.why.visitorAddressJellyfin', 'Without this Jellyfin counts every visitor as being on the local network.'],
  hostWhitelist: ['mgmt.urlCheck.why.hostWhitelist', 'Transmission answers only the names on this list: it must list every name the app is reached at.'],
  viteAllowedHosts: ['mgmt.urlCheck.why.viteAllowedHosts', 'The Vite development server answers only the names it allows.'],
  webpackAllowedHosts: ['mgmt.urlCheck.why.webpackAllowedHosts', 'The webpack development server answers only the names it allows.'],
  trustedDomains: ['mgmt.urlCheck.why.trustedDomains', 'Nextcloud answers only its trusted domains: each name the app is reached at must be one.'],
  mainUrl: ['mgmt.urlCheck.why.mainUrl', 'The address the app writes into the links it sends.'],
  vaultwardenDomain: ['mgmt.urlCheck.why.vaultwardenDomain', 'Passkeys, e-mails and Send links need the address in DOMAIN. It takes one name only: the domain one is proposed.'],
  csrfOrigin: ['mgmt.urlCheck.why.csrfOrigin', 'Signing in checks that the page comes from this address: without it, signing in fails.'],
  httpLinks: ['mgmt.urlCheck.why.httpLinks', 'Tells the app that it is reached over HTTPS, so that its links and redirects keep https://.'],
  rootUrl: ['mgmt.urlCheck.why.rootUrl', 'The address the app names in its links and redirects (otherwise localhost).'],
};
export const whyWords = (why) => {
  const known = WHYS[why];
  return known ? { key: known[0], text: known[1] } : (why ? { key: '', text: String(why) } : null);
};

// The notes of a result, in words ({{proxy}}: the address the app sees Cosmos at).
const NOTES = {
  haTrustProxy: ['mgmt.urlCheck.note.haTrustProxy', 'To give Home Assistant the visitors\' addresses: Settings > System > Network > HTTP server: turn on X-Forwarded-For, add {{proxy}} as trusted proxy, confirm within 5 minutes; then switch off "Do not tell the app the visitor\'s address" on this URL.'],
};
export const noteWords = (result) => ((result && result.notes) || []).filter((id) => NOTES[id])
  .map((id) => ({ id, key: NOTES[id][0], text: NOTES[id][1], params: { proxy: (result && result.proxy) || 'Cosmos\' address' } }));

// The URLs of the same app that the same URL setting repairs: the one asked
// about first, then the others whose check proposes the same value.
export const sharingRouteRepair = (results, result) => {
  const mine = orderedRepairs(result).route;
  if (!mine || !result) return [];
  const out = [result.route];
  (Array.isArray(results) ? results : Object.values(results || {})).forEach((other) => {
    if (!other || other.route === result.route || other.container !== result.container) return;
    const theirs = orderedRepairs(other).route;
    if (theirs && theirs.setting === mine.setting && !!theirs.on === !!mine.on && attentionOf(other) === 'error') out.push(other.route);
  });
  return out;
};

// The variables of an env repair as fields: name, proposed value, today's.
export const envFields = (repairs) => (repairs || []).filter((r) => r.kind === 'env')
  .map((r) => ({ name: r.name, value: r.value || '', now: r.now || '', why: r.why || '' }));

// The container's variables with the repaired ones set (only those).
const VAR = /^[A-Za-z_][A-Za-z0-9_.]{0,63}$/;
export const envWith = (env, fields) => {
  const out = [...(env || [])].map(String);
  (fields || []).forEach(({ name, value }) => {
    if (!VAR.test(String(name || ''))) return;
    const line = `${name}=${value}`;
    const at = out.findIndex((l) => l.split('=')[0] === name);
    if (at >= 0) out[at] = line; else out.push(line);
  });
  return out;
};

// The routes of an app among the configuration's URLs (as utils/routes finds them).
export const routesOfContainer = (routes, container) => (routes || []).filter((route) => {
  if (!route || route.Mode !== 'SERVAPP') return false;
  const m = String(route.Target || '').trim().match(/^(?:[a-z]+:\/\/)?([^/:]+)(?::\d+)?\/?$/i);
  return !!m && m[1] === container;
});

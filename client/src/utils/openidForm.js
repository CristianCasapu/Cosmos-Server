// Fork addition: what the OpenID client form proposes and checks
// (pages/openid/forkOpenidForm.jsx). Plain functions, no React: see
// openidForm.test.mjs. Texts are ['key', 'English', variables].
//
// The redirect URIs of common apps are the ones their documentation names
// (the list and the sources are kept with the owner's plans, 27); an app the
// list does not know gets its address with a sentence asking for the path.
// The server checks the same rule on save (src/configapi/fork_openid.go).

// path: what follows the app's address. named: the path holds a name the
// admin also enters in the app (proposed as "cosmos").
export const KNOWN_APPS = [
  { key: 'immich', name: 'Immich', terms: ['immich'], paths: ['/auth/login', '/user-settings', 'app.immich:///oauth-callback'] },
  { key: 'grafana', name: 'Grafana', terms: ['grafana'], paths: ['/login/generic_oauth'] },
  { key: 'mealie', name: 'Mealie', terms: ['mealie'], paths: ['/login'] },
  { key: 'portainer', name: 'Portainer', terms: ['portainer'], paths: ['/'] },
  { key: 'synapse', name: 'Synapse (Matrix)', terms: ['synapse', 'matrix'], paths: ['/_synapse/client/oidc/callback'] },
  { key: 'audiobookshelf', name: 'Audiobookshelf', terms: ['audiobookshelf', 'abs'], paths: ['/auth/openid/callback', '/auth/openid/mobile-redirect'] },
  { key: 'nextcloud', name: 'Nextcloud', terms: ['nextcloud'], paths: ['/apps/user_oidc/code'] },
  { key: 'openwebui', name: 'Open WebUI', terms: ['open-webui', 'openwebui'], paths: ['/oauth/oidc/callback'] },
  { key: 'gitea', name: 'Gitea / Forgejo', terms: ['gitea', 'forgejo'], paths: ['/user/oauth2/cosmos/callback'], named: true },
  { key: 'paperless', name: 'Paperless-ngx', terms: ['paperless'], paths: ['/accounts/oidc/cosmos/login/callback/'], named: true },
  { key: 'jellyfin', name: 'Jellyfin (SSO plugin)', terms: ['jellyfin'], paths: ['/sso/OID/redirect/cosmos'], named: true },
];

export const SCOPES = 'openid profile email';

const words = (text) => String(text || '').toLowerCase().split(/[^a-z0-9]+/).filter(Boolean);

// The known app a URL belongs to, from its name, its address and the
// container it leads to; null when none.
export const knownAppOf = (route) => {
  if (!route) return null;
  const target = String(route.Target || '').replace(/^[a-z]+:\/\//, '').split(/[:/]/)[0];
  const seen = new Set([...words(route.Name), ...words(String(route.Host || '').split('.')[0]), ...words(target)]);
  const joined = [route.Name, route.Host, target].map((v) => String(v || '').toLowerCase()).join(' ');
  return KNOWN_APPS.find((app) => app.terms.some((term) => seen.has(term) || (term.includes('-') && joined.includes(term)))) || null;
};

// The address an app is opened with: its hostname on the panel's scheme and
// port (the apps are served by the same listener as the panel).
export const appOrigin = (host, { https = true, port = '' } = {}) => {
  const name = String(host || '').trim().toLowerCase().replace(/\/.*$/, '');
  if (!name) return '';
  const scheme = https ? 'https' : 'http';
  const standard = https ? '443' : '80';
  const p = String(port || '');
  return `${scheme}://${name}${p && p !== standard && !name.includes(':') ? ':' + p : ''}`;
};

// The server's URLs an app can be picked from: those with a name of their own.
export const appChoices = (config, where) => {
  const routes = (config && config.HTTPConfig && config.HTTPConfig.ProxyConfig && config.HTTPConfig.ProxyConfig.Routes) || [];
  const seen = new Set();
  return routes
    .filter((r) => r && r.UseHost && r.Host && !r.Disabled)
    .map((r) => ({ name: r.Name || r.Host, host: String(r.Host).toLowerCase(), url: appOrigin(r.Host, where), app: knownAppOf(r) }))
    .filter((c) => (seen.has(c.host) ? false : seen.add(c.host)));
};

// A client ID from an app's name, free among the taken ones.
export const proposeClientId = (name, taken = []) => {
  const base = String(name || '').toLowerCase().replace(/[^a-z0-9._-]+/g, '-').replace(/^[^a-z0-9]+|-+$/g, '').slice(0, 60) || 'app';
  const used = new Set(taken);
  if (!used.has(base)) return base;
  for (let i = 2; i < 100; i += 1) {
    if (!used.has(`${base}-${i}`)) return `${base}-${i}`;
  }
  return `${base}-${Date.now() % 10000}`;
};

// What the form proposes for an app picked from the server's URLs.
export const proposeForApp = (choice, taken = []) => {
  if (!choice || !choice.url) return null;
  const id = proposeClientId(choice.app ? choice.app.key : choice.name, taken);
  if (choice.app) {
    const redirects = choice.app.paths.map((p) => (/^[a-z][a-z0-9+.-]*:/.test(p) ? p : choice.url + p));
    return {
      id,
      redirects,
      complete: true,
      source: choice.app.named
        ? ['mgmt.openId.fork.sourceNamed', 'The address {{app}}\'s documentation names for its sign-in. It contains the name "cosmos": use the same name when you add Cosmos in {{app}}.', { app: choice.app.name }]
        : ['mgmt.openId.fork.sourceKnown', 'The address {{app}}\'s documentation names for its sign-in.', { app: choice.app.name }],
    };
  }
  return {
    id,
    redirects: [choice.url + '/'],
    complete: false,
    source: ['mgmt.openId.fork.sourceUnknown', 'The app\'s address. Complete it with the path its documentation names for OpenID (often something like /oauth/callback or /auth/oidc/callback).'],
  };
};

const PRIVATE_SCHEME = /^[a-z][a-z0-9+-]*(\.[a-z0-9+-]+)+$/;

// The server's rule for one redirect URI; null when it is accepted.
export const redirectProblem = (raw) => {
  const value = String(raw || '');
  if (!value.trim()) return ['mgmt.openId.fork.redirectEmpty', 'Enter the address the app is sent back to after signing in'];
  if (value.length > 512) return ['mgmt.openId.fork.redirectLong', 'At most 512 characters'];
  if (/[\s\u0000-\u001f\u007f]/.test(value)) return ['mgmt.openId.fork.redirectSpace', 'No spaces in an address'];
  if (value.includes('*')) return ['mgmt.openId.fork.redirectWildcard', 'Write the address out in full, without *: the app is sent back to exactly this address'];
  if (value.includes('#')) return ['mgmt.openId.fork.redirectFragment', 'An address the app is sent back to cannot have a # part'];
  const m = value.match(/^([a-zA-Z][a-zA-Z0-9+.-]*):(.*)$/);
  if (!m) return ['mgmt.openId.fork.redirectNotUrl', 'Write the whole address, starting with https://, for example https://app.example.com/oauth/callback'];
  const scheme = m[1].toLowerCase();
  if (scheme === 'https' || scheme === 'http') {
    let url;
    try { url = new URL(value); } catch (e) { url = null; }
    if (!url || !url.hostname || !m[2].startsWith('//')) return ['mgmt.openId.fork.redirectNotUrl', 'Write the whole address, starting with https://, for example https://app.example.com/oauth/callback'];
    if (url.username || url.password) return ['mgmt.openId.fork.redirectUser', 'An address the app is sent back to cannot carry a user name or a password'];
    if (scheme === 'http') {
      const host = url.hostname.replace(/^\[|\]$/g, '');
      if (host !== 'localhost' && !/^127\./.test(host) && host !== '::1') {
        return ['mgmt.openId.fork.redirectHttp', 'Plain http only for an app on the same device (localhost): use the app\'s https address'];
      }
    }
    return null;
  }
  if (PRIVATE_SCHEME.test(scheme)) return null;
  return ['mgmt.openId.fork.redirectScheme', 'Not an address an app can be sent back to: use https://…, or the scheme of a phone app such as app.example:/callback'];
};

export const redirectList = (text) => String(text || '').split(',').map((s) => s.trim()).filter(Boolean);

// What the whole client lacks or holds wrong, for the Create button; [] when
// it can be saved. The server checks the same.
export const clientProblems = ({ id, redirects, takenIds = [] }) => {
  const problems = [];
  const name = String(id || '').trim();
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(name)) {
    problems.push(['mgmt.openId.fork.idInvalid', 'The client ID is a name of letters, digits, dots, dashes or underscores, for example immich']);
  } else if (takenIds.includes(name)) {
    problems.push(['mgmt.openId.fork.idTaken', 'Another client already has this ID']);
  }
  const list = redirects || [];
  if (list.length === 0) problems.push(['mgmt.openId.fork.redirectEmpty', 'Enter the address the app is sent back to after signing in']);
  if (list.length > 10) problems.push(['mgmt.openId.fork.redirectMany', 'At most 10 addresses per client']);
  if (new Set(list).size !== list.length) problems.push(['mgmt.openId.fork.redirectTwice', 'An address is listed twice']);
  list.forEach((uri) => { const p = redirectProblem(uri); if (p) problems.push(p); });
  return problems;
};

// The values an app needs, as the server announces them: the panel's
// hostname with the port the panel is reached on (src/utils/fork_panel_origin.go).
export const providerValues = ({ hostname, https = true, port = '' }) => {
  const issuer = appOrigin(hostname, { https, port });
  return {
    issuer,
    discovery: issuer ? issuer + '/.well-known/openid-configuration' : '',
    authorize: issuer ? issuer + '/cosmos-ui/openid' : '',
    token: issuer ? issuer + '/oauth2/token' : '',
    userinfo: issuer ? issuer + '/oauth2/userinfo' : '',
    scopes: SCOPES,
  };
};

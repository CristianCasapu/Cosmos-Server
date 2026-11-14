// Fork addition: what the screens say about whether a container can be
// reached from the internet. The server (src/reach) checks each app route;
// this combines the routes of a container (or of a stack) with the host
// ports it publishes, and holds the plain-words sentence of every check.
// Nothing here imports the UI, so it can be exercised on its own.

// Best first: what a card says is the best its routes and ports give.
const RANK = { verified: 8, unverified: 7, problem: 6, forwarded: 5, ports: 4, limited: 3, local: 2, off: 1, none: 0 };
export const EXPOSED = ['verified', 'unverified', 'problem'];

// The host ports of a container list (Docker's Ports), one per port and
// protocol: Docker lists a port once for IPv4 and once for IPv6. "all" when
// published on every address of the server, "server" when bound to the
// loopback address only, "address" when bound to one address.
export const publishedPorts = (ports) => {
  const seen = new Map();
  (ports || []).forEach((port) => {
    if (!port || !port.PublicPort) {
      return;
    }
    const ip = String(port.IP || '');
    const scope = ip === '' || ip === '0.0.0.0' || ip === '::'
      ? 'all'
      : (ip.startsWith('127.') || ip === '::1') ? 'server' : 'address';
    const key = port.PublicPort + '/' + (port.Type || 'tcp');
    const known = seen.get(key);
    const order = { all: 2, address: 1, server: 0 };
    if (!known || order[scope] > order[known.scope]) {
      seen.set(key, { port: port.PublicPort, privatePort: port.PrivatePort, type: port.Type || 'tcp', ip, scope });
    }
  });
  return [...seen.values()].sort((a, b) => a.port - b.port);
};

// summarize gives the answer for a card or a container page.
// routeNames: the container's (or stack's) routes; results: the server's
// answer (data.routes); ports: Docker's Ports of all its containers;
// hostNetwork: a container uses the host's network.
// mappings: the app ports the router forwards for these containers (src/upnp,
// status.apps): with one that is active, "if forwarded" becomes "forwarded".
export const summarize = ({ routeNames, results, ports, hostNetwork, mappings }) => {
  const byName = new Map((results || []).map((result) => [result.route, result]));
  const routes = (routeNames || []).map((name) => byName.get(name)).filter(Boolean);
  const best = routes.reduce((top, route) => (!top || (RANK[route.state] || 0) > (RANK[top.state] || 0) ? route : top), null);
  const published = publishedPorts(ports);
  const reachablePorts = published.filter((port) => port.scope !== 'server');

  const forwarded = (mappings || []).filter((mapping) => mapping && mapping.active);

  let state = 'none';
  if (best && EXPOSED.includes(best.state)) {
    state = best.state;
  } else if (forwarded.length > 0) {
    state = 'forwarded';
  } else if (reachablePorts.length > 0 || hostNetwork) {
    state = 'ports';
  } else if (best) {
    state = best.state;
  }
  return {
    state,
    route: best && EXPOSED.includes(best.state) ? best : null,
    url: best && (best.state === 'verified' || best.state === 'unverified') ? best.url || '' : '',
    routes,
    ports: published,
    forwarded,
    hostNetwork: !!hostNetwork,
  };
};

// The first check of a route with the given status, for a one-line reason.
export const firstCheck = (route, status) => ((route && route.checks) || []).find((check) => check.status === status) || null;

// Where the admin fixes a problem.
export const FIX_LINKS = {
  route: (route) => '/cosmos-ui/config-url/' + encodeURIComponent(route),
  domains: () => '/cosmos-ui/config-general/https',
  internet: () => '/cosmos-ui/config-general/http-server',
};

// The sentence of each check the server can return: [translation key,
// English text, where it is fixed (a FIX_LINKS key) or null]. Addresses are
// not part of the sentences: admins see them beside it, others never get them.
export const CHECK_TEXT = {
  'access.disabled': ['disabled', 'This URL is switched off.', 'route'],
  'access.noHost': ['noHost', 'This URL has no address of its own, and Cosmos\' own address is a local one.', 'route'],
  'access.localGuard': ['localGuard', 'A local address: Cosmos refuses it to visitors from outside the local network.', null],
  'access.constellation': ['constellation', 'Only the devices of your Constellation may open this URL.', 'route'],
  'access.whitelist': ['whitelist', 'Only the addresses listed in the URL\'s settings may open it.', 'route'],
  'access.adminOnly': ['adminOnly', 'Open to the internet; only admins get in, after logging in.', null],
  'access.login': ['login', 'Open to the internet; visitors log in to Cosmos first.', null],
  'access.open': ['open', 'Open to everyone who reaches the address.', null],
  // the panel's own address (the server's answer has it apart from the URLs, as "panel")
  'access.panel': ['panel', 'The Cosmos panel answers at this address; everything in it needs a login.', null],
  'access.panelLocal': ['panelLocal', 'The panel\'s address is a local one: it is reached on your local network only.', null],

  'record.exists': ['exists', 'The record exists at Cloudflare.', null],
  'record.pending': ['pending', 'The record is being written at Cloudflare: this takes up to a minute.', null],
  'record.foreign': ['foreign', 'A record for this name exists at Cloudflare that Cosmos did not create: Cosmos leaves it as it is.', 'domains'],
  'record.error_invalid_token': ['errorInvalidToken', 'Cloudflare does not accept the stored API token (wrong, expired or deleted): the record cannot be written. Paste a current token.', 'domains'],
  'record.error_malformed_token': ['errorMalformedToken', 'What is stored for this domain is not an API token: the record cannot be written.', 'domains'],
  'record.error_forbidden': ['errorForbidden', 'The API token may not change this domain\'s records at Cloudflare. It needs Zone > Zone > Read and Zone > DNS > Edit.', 'domains'],
  'record.error_not_found': ['errorNotFound', 'The API token does not see this domain at Cloudflare: the record cannot be written.', 'domains'],
  'record.error_rate_limited': ['errorRateLimited', 'Cloudflare asked Cosmos to slow down: the record is written a little later.', null],
  'record.error_unreachable': ['errorUnreachable', 'Cloudflare could not be reached from this server: the record is written once it can.', null],
  'record.error_other': ['errorOther', 'The record could not be written at Cloudflare. Configuration > Domains shows the reason.', 'domains'],

  'dns.pointsHere': ['pointsHere', 'The name points to this server.', null],
  'dns.throughShield': ['throughShield', 'The name leads to Cloudflare, which passes visitors on to this server.', null],
  'dns.shieldNotYet': ['shieldNotYet', 'The name still points straight at this server although it is set to go through Cloudflare: the change is on its way, or the proxy was switched off at Cloudflare. Visitors who get this answer are refused.', 'domains'],
  'dns.behindCloudflare': ['behindCloudflare', 'The name leads to Cloudflare\'s proxy, but here it is set to be served directly: Cosmos then takes Cloudflare\'s addresses for the visitors. Set it to "through Cloudflare" under Domains.', 'domains'],
  'dns.noRecord': ['noRecord', 'The name does not exist in DNS. Create the record at your DNS provider, or let Cosmos manage the domain\'s records.', 'domains'],
  'dns.lookupFailed': ['lookupFailed', 'The name could not be looked up just now.', null],
  'dns.localAnswer': ['localAnswer', 'Here the name resolves to a local address, probably a local DNS entry (router, Pi-hole): what the internet gets cannot be seen from here.', null],
  'dns.publicIPUnknown': ['publicIPUnknown', 'The name points to a public address, but Cosmos does not know this server\'s public address, so it cannot tell whether that is this server.', null],
  'dns.pointsElsewhere': ['pointsElsewhere', 'The name points to another address than this server\'s public one. Correct the DNS record (behind a proxy such as Cloudflare this is expected).', 'domains'],
  'dns.badName': ['badName', 'This address is not a name that can be looked up.', 'route'],
  'dns.tooMany': ['tooMany', 'Not checked: too many addresses at once.', null],
  'dns.ipNotPublic': ['ipNotPublic', 'The address is not a public one.', 'route'],
  'dns.ipPublicIPUnknown': ['ipPublicIPUnknown', 'The address is a public one, but Cosmos does not know this server\'s public address, so it cannot tell whether it is this server.', null],
  'dns.ipElsewhere': ['ipElsewhere', 'The address is not this server\'s public address.', 'route'],
  'dns.ipIsServer': ['ipIsServer', 'The address is this server\'s public address.', null],

  'forward.upnp': ['upnp', 'The router forwards port {{external}} to Cosmos\' port {{port}} (automatic port forwarding).', null],
  'forward.byHand': ['byHand', 'Cosmos cannot tell whether your router forwards port {{port}} to this server. Forward it on the router, or switch on automatic forwarding.', 'internet'],
  'forward.hostPort': ['hostPort', 'The address of this URL carries port {{port}}, but from the internet Cosmos is reached on port {{external}}: a request from outside does not match it. Take the port out of the address.', 'route'],
  'forward.ownPort': ['ownPort', 'This URL uses port {{port}} of its own: Cosmos cannot tell whether the router forwards it.', null],
  'forward.ipv6Firewall': ['ipv6Firewall', 'Reached over IPv6: nothing to forward, but the router\'s IPv6 firewall must let port {{port}} in, and Cosmos cannot see it.', null],
  'forward.routerNotPublic': ['routerNotPublic', 'The router\'s own internet address is not a public one (provider NAT): nothing behind it can be reached from the internet.', 'internet'],
  'forward.upnpFailed': ['upnpFailed', 'Automatic forwarding of port {{port}} failed: {{error}}', 'internet'],

  'shield.on': ['on', 'Served through Cloudflare: Cosmos answers for this name to Cloudflare\'s network and to your local network only.', null],
  'shield.port': ['port', 'Set to go through Cloudflare, but visitors use port {{port}}, which Cloudflare does not pass on (it passes HTTPS on 443, 2053, 2083, 2087, 2096 and 8443).', 'internet'],

  'cert.valid': ['valid', 'The certificate is valid until {{until}}.', null],
  'cert.selfSigned': ['selfSigned', 'The certificate is not one browsers trust: visitors get a warning.', 'domains'],
  'cert.wrongName': ['wrongName', 'The certificate does not cover this name: visitors get a warning.', 'domains'],
  'cert.expired': ['expired', 'The certificate has expired: visitors get a warning.', 'domains'],
  'cert.none': ['none', 'No certificate is ready for this name yet.', 'domains'],
  'cert.ipAddress': ['ipAddress', 'Browsers do not trust a certificate for an IP address: give the app a name.', 'route'],
  'cert.plainHTTP': ['plainHTTP', 'Served over plain HTTP: nothing is encrypted.', null],
};

// The sentence of a check: { key, text, params, fix } (fix: a link or null).
// Unknown codes get a plain fallback, never the raw code alone.
export const checkSentence = (check, routeName) => {
  const entry = CHECK_TEXT[check.id + '.' + check.code];
  const params = { ...(check.params || {}) };
  if (params.until) {
    const date = new Date(params.until);
    params.until = isNaN(date) ? params.until : date.toLocaleDateString();
  }
  if (!entry) {
    return { key: 'mgmt.reach.check.unknown', text: 'Not checked.', params, fix: null };
  }
  // the panel is not one of the URLs: what a URL's settings fix is fixed where its address is set
  const where = entry[2] === 'route' && routeName === PANEL_ROUTE ? 'internet' : entry[2];
  return {
    key: 'mgmt.reach.check.' + check.id + '.' + entry[0],
    text: entry[1],
    params,
    fix: where ? FIX_LINKS[where](routeName) : null,
  };
};

// The panel's own address is judged like a URL (the server's answer has it
// as "panel"). It has no name among the URLs: this stands in for one.
export const PANEL_ROUTE = '';

// The panel's verdict as a list of results, the form summarize takes; empty
// when the server sent none (no address set yet).
export const panelResults = (data) => (data && data.panel ? [{ ...data.panel, route: PANEL_ROUTE }] : []);

// The addresses an admin sees beside a sentence ('' when none was sent).
export const checkAddresses = (check) => {
  const params = check.params || {};
  if (params.address && params.publicIP) {
    return params.address + ' ≠ ' + params.publicIP;
  }
  return params.address || params.router || '';
};

// The ports of a summary as text: "8080, 9000/udp".
export const portsText = (ports) => (ports || [])
  .filter((port) => port.scope !== 'server')
  .map((port) => port.port + (port.type && port.type !== 'tcp' ? '/' + port.type : ''))
  .join(', ');

// Only links the server built from a route are opened: http(s) and nothing else.
export const safeURL = (url) => (/^https?:\/\/[^\s"'<>]+$/i.test(String(url || '')) ? url : '');

// Docker's inspect form of the published ports ({"80/tcp": [{HostIp,
// HostPort}]}) in the list form publishedPorts reads.
export const portsFromInspect = (ports) => Object.entries(ports || {}).flatMap(([key, bindings]) => {
  const [privatePort, type] = String(key).split('/');
  return (bindings || [])
    .filter((binding) => binding && Number(binding.HostPort) > 0)
    .map((binding) => ({ IP: binding.HostIp || '', PublicPort: Number(binding.HostPort), PrivatePort: Number(privatePort), Type: type || 'tcp' }));
});

// A short fingerprint of the routes, to fetch the answer again when they change.
export const routesStamp = (config) => ((config && config.HTTPConfig && config.HTTPConfig.ProxyConfig && config.HTTPConfig.ProxyConfig.Routes) || [])
  .map((route) => [route.Name, route.Host, route.UseHost, route.Disabled, route.AdminOnly, route.AuthEnabled,
    route.RestrictToConstellation, (route.WhitelistInboundIPs || []).length, route.Target].join('|'))
  .join('\n');

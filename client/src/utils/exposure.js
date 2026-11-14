// Fork addition: what "expose to the internet" proposes for an app, and the
// request it sends (POST /api/expose, src/cloudflare/expose.go, which checks
// everything again). Pure: nothing here imports the UI or the API, so it is
// exercised on its own (exposure.test.mjs).
//
// For each thing an app offers:
// - its web interface goes out through Cosmos' HTTPS port under a name of a
//   configured domain: through Cloudflare's proxy when Cloudflare can carry it
//   (Cosmos is reached on a port Cloudflare proxies, the domain's records are
//   Cosmos'), else directly (DNS only), with the reason;
// - every other published port goes through the router (UPnP) and, under a
//   Cloudflare domain whose records Cosmos manages, gets a DNS-only name of
//   its own: "<app>-direct.<domain>" when the app also has a web interface
//   (its web name stays free to be proxied), "<app>.<domain>" when it has none.
// Nothing is ticked by default. Dangerous ports are only behind the door.
import { looksLikeMediaServer } from './cloudflare.js';
import { looksLikeWeb, portKey } from './routerPorts.js';
import { isLocalHost } from './appLinks.js';

// The same list as RiskyPorts in src/cloudflare/expose.go (the test compares).
export const RISKY_PORTS = [
  [21, 23], [53, 53], [111, 111], [135, 139], [161, 161], [389, 389], [445, 445], [636, 636],
  [1433, 1433], [1521, 1521], [2049, 2049], [2375, 2376], [2379, 2380], [3306, 3306], [3389, 3389],
  [4243, 4243], [5005, 5005], [5432, 5432], [5900, 5903], [5984, 5984], [6379, 6379], [6443, 6443],
  [8086, 8086], [9100, 9100], [9200, 9200], [9229, 9229], [9300, 9300], [10250, 10250],
  [11211, 11211], [15672, 15672], [27017, 27019],
];

export const isRiskyPort = (port) => RISKY_PORTS.some(([from, to]) => Number(port) >= from && Number(port) <= to);

// The app's name as a DNS label (as hostnameSuggestions.jsx makes it).
export const label = (name) => String(name || '')
  .replace(/^\//, '')
  .toLowerCase()
  .replace(/[_\s.]+/g, '-')
  .replace(/[^a-z0-9-]/g, '')
  .replace(/^-+|-+$/g, '')
  .slice(0, 63);

const lower = (text) => String(text || '').trim().toLowerCase().replace(/:\d+$/, '').replace(/\.$/, '');

const underZone = (host, zone) => host === zone || host.endsWith('.' + zone);

// A route's host, when it has one of its own.
const hostOf = (route) => (route && route.UseHost ? lower(route.Host) : '');

const isWebTarget = (target) => /^https?:\/\//i.test(String(target || '').trim());

// The routes of a container (as utils/routes.jsx finds them).
export const appRoutes = (config, containerName) => {
  const name = String(containerName || '').replace(/^\//, '');
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = new RegExp('^(([a-z]+)://)?' + escaped + '(:?[0-9]+)?/?$', 'i');
  const routes = (config && config.HTTPConfig && config.HTTPConfig.ProxyConfig && config.HTTPConfig.ProxyConfig.Routes) || [];
  return routes.filter((route) => route && route.Mode === 'SERVAPP' && match.test(String(route.Target || '').trim()));
};

// The domains an app can be exposed under, most specific first, each with
// what Cosmos can do there. `cloudflare` is GET /api/cloudflare's data.
export const exposureDomains = (config, cloudflare) => {
  const zones = (config && config.HTTPConfig && config.HTTPConfig.DNSZones) || [];
  const cf = (cloudflare && cloudflare.zones) || [];
  return zones
    .map((zone) => lower(zone.Zone))
    .filter((zone) => zone && !zone.endsWith('.local'))
    .sort((a, b) => b.length - a.length)
    .map((zone) => {
      const entry = cf.find((z) => z.name === zone) || null;
      const managed = !!(entry && entry.hasToken && entry.manageRecords);
      return {
        zone,
        cloudflare: !!entry,
        // Cosmos writes the records there, conflict rule included: a port may get a name
        managed,
        // the shield can be switched there (the server's own conditions follow)
        canShield: managed && !entry.wildcardRecord && !!(cloudflare && cloudflare.portOK),
        wildcard: !!(entry && entry.wildcardRecord),
      };
    });
};

const zoneOf = (host, domains) => (domains.find((domain) => underZone(host, domain.zone)) || null);

// Tools that control the server or come without a sign-in of their own: their
// address from the internet is proposed behind Cosmos' sign-in.
const CONTROL_TOOLS = ['portainer', 'dockge', 'yacht', 'code-server', 'codeserver', 'ttyd', 'wetty', 'dozzle', 'adminer',
  'phpmyadmin', 'pgadmin', 'pgadmin4', 'cockpit', 'webmin', 'filebrowser'];
const NO_SIGN_IN = ['glances', 'netdata', 'homepage', 'dashy', 'heimdall', 'homarr', 'it-tools', 'cyberchef', 'transmission',
  'zigbee2mqtt', 'esphome', 'node-red', 'scrutiny', 'duplicati', 'syncthing', 'jdownloader', 'jdownloader-2', 'stirling-pdf'];

const imageWords = (image) => {
  const name = String(image || '').toLowerCase().split('@')[0].replace(/:[^/]*$/, '');
  const parts = name.split('/').filter(Boolean);
  const last = parts.slice(-1)[0] || '';
  return [last, ...last.split(/[^a-z0-9]+/)];
};

// Who may open the internet address: { access, source }. The app's own URL
// keeps what it has when it has a sign-in in front already.
export const proposeAccess = (image, route) => {
  if (route && route.AuthEnabled) {
    return { access: route.AdminOnly ? 'admins' : 'users', source: 'route' };
  }
  const words = imageWords(image);
  if (CONTROL_TOOLS.some((tool) => words.includes(tool))) {
    return { access: 'admins', source: 'control' };
  }
  if (NO_SIGN_IN.some((tool) => words.includes(tool))) {
    return { access: 'users', source: 'noSignIn' };
  }
  return { access: 'anyone', source: 'own' };
};

// How a web address goes out: { through, reason }.
//   cloudflare  — Cloudflare proxies Cosmos' port and the records are Cosmos'
//   direct/port — Cosmos is reached on a port Cloudflare does not proxy
//   direct/media — a media server: Cloudflare's terms
//   direct/notCloudflare — the domain is not connected to Cloudflare by Cosmos
//   direct/wildcard — the domain publishes one wildcard record
export const proposeThrough = (domain, cloudflare, image) => {
  if (!domain || !domain.managed) {
    return { through: 'direct', reason: 'notCloudflare' };
  }
  if (domain.wildcard) {
    return { through: 'direct', reason: 'wildcard' };
  }
  if (!(cloudflare && cloudflare.portOK)) {
    return { through: 'direct', reason: 'port' };
  }
  if (looksLikeMediaServer(image)) {
    return { through: 'direct', reason: 'media' };
  }
  return { through: 'cloudflare', reason: 'cloudflare' };
};

// The route the internet addresses of the app are copies of: its local web
// URL, else its first web URL.
export const baseWebRoute = (routes) => (routes || []).find((route) => isWebTarget(route.Target) && route.UseHost && isLocalHost(hostOf(route)))
  || (routes || []).find((route) => isWebTarget(route.Target) && route.UseHost)
  || null;

// Every name used by URLs and by ports, with whose it is.
const takenNames = (config) => {
  const taken = new Map();
  const routes = (config && config.HTTPConfig && config.HTTPConfig.ProxyConfig && config.HTTPConfig.ProxyConfig.Routes) || [];
  routes.forEach((route) => { if (hostOf(route)) taken.set(hostOf(route), { route }); });
  ((config && config.HTTPConfig && config.HTTPConfig.UPnP && config.HTTPConfig.UPnP.AppPorts) || []).forEach((choice) => {
    if (choice.Hostname) taken.set(lower(choice.Hostname), { container: choice.Container });
  });
  return taken;
};

const shieldedList = (config) => ((config && config.HTTPConfig && config.HTTPConfig.Cloudflare && config.HTTPConfig.Cloudflare.Shielded) || []).map(lower);

// The web side of an app: { available, route, domains: [{ zone, host,
// exposed, through, reason, access, accessSource, canShield }] }. `exposed`
// is the app's existing address under that domain (a route copy), if any.
export const webPlan = ({ config, cloudflare, containerName, image }) => {
  const routes = appRoutes(config, containerName);
  const base = baseWebRoute(routes);
  const domains = exposureDomains(config, cloudflare);
  const shielded = shieldedList(config);
  const name = label(containerName);
  const taken = takenNames(config);
  return {
    available: !!base,
    route: base,
    domains: domains.map((domain) => {
      const exposed = routes.find((route) => hostOf(route) && underZone(hostOf(route), domain.zone) && hostOf(route) !== domain.zone) || null;
      const access = proposeAccess(image, exposed || base);
      let host = exposed ? hostOf(exposed) : name + '.' + domain.zone;
      const holder = taken.get(host);
      const free = !holder || (holder.route && appRoutes(config, containerName).includes(holder.route));
      const proposal = proposeThrough(domain, cloudflare, image);
      return {
        zone: domain.zone,
        host,
        free,
        exposed: exposed ? { host: hostOf(exposed), name: exposed.Name, own: exposed === base, through: shielded.includes(hostOf(exposed)) ? 'cloudflare' : 'direct', access: proposeAccess('', exposed).access } : null,
        through: exposed ? (shielded.includes(hostOf(exposed)) ? 'cloudflare' : 'direct') : proposal.through,
        reason: proposal.reason,
        access: access.access,
        accessSource: access.source,
        canShield: domain.canShield,
        managed: domain.managed,
      };
    }),
  };
};

// The configuration as it will be once an installation has created its
// URLs (`routes`, upstream's field names): what webPlan and portPlan read.
export const withRoutes = (config, routes) => {
  const http = (config && config.HTTPConfig) || {};
  const proxy = http.ProxyConfig || {};
  return { ...config, HTTPConfig: { ...http, ProxyConfig: { ...proxy, Routes: [...(routes || []), ...(proxy.Routes || [])] } } };
};

// The DNS-only name a raw port is proposed under, in a domain. `webDirect`
// is the app's web name there when that one is direct (DNS only): one name
// for everything then.
export const directName = ({ containerName, zone, hasWeb, webDirect, taken, own }) => {
  if (webDirect) {
    return webDirect;
  }
  const name = label(containerName);
  const first = hasWeb ? name + '-direct.' + zone : name + '.' + zone;
  const ok = (host) => {
    const holder = taken && taken.get(host);
    return !holder || (own && own(holder));
  };
  if (ok(first)) {
    return first;
  }
  for (let n = 2; n < 50; n++) {
    const next = (hasWeb ? name + '-direct-' + n : name + '-' + n) + '.' + zone;
    if (ok(next)) {
      return next;
    }
  }
  return first;
};

// The other ports of an app: [{ key, port, protocol, hostPort, web, webGuess,
// risky, reserved, door, exposed, hostname, zone }]. `ports` are the
// published ports [{ port (container), protocol, hostPort, hostIp }];
// `router` GET /api/upnp's data. The web port (the one a URL targets) is not
// among them: it goes through Cosmos. `door` ports are offered only behind
// "other ports", with a warning.
export const portPlan = ({ config, cloudflare, router, containerName, ports, webHosts }) => {
  const name = String(containerName || '').replace(/^\//, '');
  const routes = appRoutes(config, name);
  const domains = exposureDomains(config, cloudflare).filter((domain) => domain.managed);
  const zone = domains[0] || null;
  const taken = takenNames(config);
  const own = (holder) => holder.container === name || (holder.route && routes.includes(holder.route));
  const hasWeb = !!baseWebRoute(routes);
  const reserved = (router && router.reserved) || [];
  const choices = ((config && config.HTTPConfig && config.HTTPConfig.UPnP && config.HTTPConfig.UPnP.AppPorts) || []);
  const shielded = shieldedList(config);
  const seen = new Set();
  const out = [];
  (ports || []).forEach((p) => {
    const protocol = String(p.protocol || 'tcp').toLowerCase();
    const key = portKey(name, p.port, protocol);
    const ip = String(p.hostIp || '');
    if (seen.has(key) || !p.hostPort || ip.startsWith('127.') || ip === '::1') {
      return;
    }
    seen.add(key);
    const guess = looksLikeWeb({ containerName: name, containerPort: p.port, protocol, routes });
    if (guess.web && guess.route) {
      return; // the web interface: through Cosmos
    }
    const choice = choices.find((c) => portKey(c.Container, c.Port, c.Protocol) === key) || null;
    // the direct name: the web one when it is direct in that domain
    const webDirect = zone && (webHosts || []).map(lower).find((host) => underZone(host, zone.zone) && !shielded.includes(host)) || '';
    const risky = isRiskyPort(p.port) || isRiskyPort(p.hostPort);
    const isReserved = reserved.includes(Number(p.hostPort));
    out.push({
      key,
      port: Number(p.port),
      protocol,
      hostPort: Number(p.hostPort),
      webGuess: guess.web,
      risky,
      reserved: isReserved,
      door: risky || isReserved,
      exposed: choice,
      zone: zone ? zone.zone : '',
      hostname: choice ? (choice.Hostname || '') : (zone ? directName({ containerName: name, zone: zone.zone, hasWeb, webDirect, taken, own }) : ''),
    });
  });
  return out.sort((a, b) => a.port - b.port || a.protocol.localeCompare(b.protocol));
};

// The body of POST /api/expose that exposes what is chosen. `web`: [{ route,
// hostname, through, access }]; `ports`: [{ port, protocol, hostname,
// externalPort, risky }].
export const exposeRequest = (containerName, web, ports) => {
  const name = String(containerName || '').replace(/^\//, '');
  return {
    action: 'expose',
    container: name,
    confirm: name,
    web: (web || []).map((w) => ({ route: w.route, hostname: lower(w.hostname), through: w.through, access: w.access || 'anyone' })),
    ports: (ports || []).map((p) => ({
      port: Number(p.port),
      protocol: String(p.protocol || 'tcp').toLowerCase(),
      hostname: lower(p.hostname),
      externalPort: Number(p.externalPort) || 0,
      scheme: '',
      risky: !!p.risky,
    })),
  };
};

export const withdrawRequest = (containerName, hostnames, ports) => ({
  action: 'withdraw',
  container: String(containerName || '').replace(/^\//, ''),
  web: (hostnames || []).map((hostname) => ({ hostname: lower(hostname) })),
  ports: (ports || []).map((p) => ({ port: Number(p.port), protocol: String(p.protocol || 'tcp').toLowerCase() })),
});

// A typed name for a domain: lower case, no scheme or path, a label or a full name.
export const nameProblem = (host, zone) => {
  const name = lower(host);
  if (!name) return 'empty';
  if (!/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$/.test(name)) return 'invalid';
  if (zone && !underZone(name, zone)) return 'outside';
  if (zone && name === zone) return 'apex';
  return '';
};

// Sentences, [key suffix, English]; keys are "mgmt.expose.<suffix>". The test
// checks each against the translation file.
export const TEXT = {
  'through.cloudflare': 'Cosmos is reached on port {{port}}, which Cloudflare proxies: the address goes through Cloudflare\'s network, which hides your home\'s address and filters attacks.',
  'through.port': 'Cosmos is reached from the internet on port {{port}}, and Cloudflare proxies HTTPS only on ports {{ports}}: this address can only work directly (DNS only), with the domain\'s certificate.',
  'through.media': '{{name}} looks like a media server: Cloudflare\'s terms do not allow serving video through its proxy on the free plans. Directly is proposed.',
  'through.notCloudflare': 'Cosmos does not manage this domain\'s records at Cloudflare: the address works directly, and its record must point to this server.',
  'through.wildcard': 'This domain publishes one wildcard record for all names, which cannot be proxied name by name: directly.',
  'access.route': 'As the app\'s own URL has it.',
  'access.control': '{{name}} controls the server: proposed for Cosmos administrators only.',
  'access.noSignIn': '{{name}} has no sign-in of its own: proposed behind Cosmos\' sign-in.',
  'access.own': 'The app\'s own sign-in protects it.',
  'direct.cost': 'Directly means: your home\'s public address is visible to anyone who looks the name up, and Cloudflare\'s protection does not apply.',
  'port.cost': 'A port opened on the router reaches the app directly: Cosmos\' sign-in, SmartShield and the Fail2ban jails do not see that traffic, and your home\'s public address is visible in DNS. The app\'s own protection is all there is.',
  'port.why': 'Cloudflare\'s proxy carries web pages only on a few HTTPS ports, so this port goes out directly: the router forwards it, under a name of its own that is not proxied.',
  'port.noName': 'No Cloudflare domain whose records Cosmos manages: the port is reached at your public address.',
  'port.risky': 'Port {{port}} is usually a database, a remote shell or an admin interface: publishing it to the internet is how servers get broken into. Open it only if you know it is protected.',
  'port.reserved': 'Port {{port}} is on the list of ports Cosmos never asks the router for: another external port is used.',
  'port.webGuess': 'Port {{port}} looks like a web interface: a web address through Cosmos (above) is the safer way.',
  'name.empty': 'Enter a name.',
  'name.invalid': 'Letters, digits and hyphens, for example app.example.org.',
  'name.outside': 'The name must end in {{zone}}.',
  'name.apex': 'The domain\'s own name is never used for an app.',
  'name.taken': '{{host}} is already used by another app.',
};

export const text = (id, params) => ({ key: 'mgmt.expose.' + id, text: TEXT[id], params: params || {} });

// What the dialog says about the way out it proposes for a web address.
export const throughSentence = (domain, cloudflare, name) => text('through.' + domain.reason, {
  port: cloudflare && cloudflare.publicPort,
  ports: ((cloudflare && cloudflare.allowedPorts) || []).join(', '),
  name,
});

export const accessSentence = (source, name) => text('access.' + source, { name });

// The ports of a service definition, per container: installPorts' entries
// as portPlan takes them.
export const definitionPorts = (installPortsList, container) => (installPortsList || [])
  .filter((p) => p.container === container)
  .map((p) => ({ port: p.port, protocol: p.protocol, hostPort: p.hostPort, hostIp: '' }));

// ---- the choices of the dialog ------------------------------------------------
// { web: { <zone>: { on, host, through, access } }, ports: { <key>: { on,
// hostname, externalPort, risky } } }, as the plans propose them: what is
// exposed is on, the rest off with its proposal ready.
export const initialChoices = (web, ports) => ({
  web: Object.fromEntries(((web && web.available && web.domains) || []).map((d) => [d.zone, {
    on: !!d.exposed,
    host: d.exposed ? d.exposed.host : d.host,
    through: d.exposed ? d.exposed.through : (d.canShield ? d.through : 'direct'),
    access: d.exposed ? d.exposed.access : d.access,
  }])),
  ports: Object.fromEntries((ports || []).map((p) => [p.key, {
    on: !!p.exposed,
    hostname: p.hostname || '',
    externalPort: p.exposed && p.exposed.ExternalPort ? String(p.exposed.ExternalPort) : '',
    risky: false,
  }])),
});

// What has to be sent to go from `before` to `after`: { expose, withdraw },
// each a request body or null. Only what changed is sent.
export const exposureChanges = (containerName, web, ports, before, after) => {
  const exposeWeb = [];
  const withdrawWeb = [];
  ((web && web.available && web.domains) || []).forEach((d) => {
    const was = (before.web || {})[d.zone] || {};
    const now = (after.web || {})[d.zone] || {};
    if (now.on && (!was.on || was.host !== now.host || was.through !== now.through || was.access !== now.access)) {
      exposeWeb.push({ route: web.route.Name, hostname: now.host, through: now.through, access: now.access });
    }
    // a changed name: the old address goes
    if (was.on && d.exposed && !d.exposed.own && (!now.on || lower(now.host) !== lower(d.exposed.host))) {
      withdrawWeb.push(d.exposed.host);
    }
  });
  const exposePorts = [];
  const withdrawPorts = [];
  (ports || []).forEach((p) => {
    const was = (before.ports || {})[p.key] || {};
    const now = (after.ports || {})[p.key] || {};
    if (now.on && (!was.on || was.hostname !== now.hostname || String(was.externalPort || '') !== String(now.externalPort || ''))) {
      exposePorts.push({ port: p.port, protocol: p.protocol, hostname: now.hostname, externalPort: now.externalPort, risky: p.risky && !!now.risky });
    }
    if (was.on && !now.on) {
      withdrawPorts.push({ port: p.port, protocol: p.protocol });
    }
  });
  return {
    expose: exposeWeb.length + exposePorts.length > 0 ? exposeRequest(containerName, exposeWeb, exposePorts) : null,
    withdraw: withdrawWeb.length + withdrawPorts.length > 0 ? withdrawRequest(containerName, withdrawWeb, withdrawPorts) : null,
  };
};

// The problems of the choices, by field: { 'web:<zone>': code, 'port:<key>': code }.
export const choiceProblems = (web, ports, choices, config, containerName) => {
  const problems = {};
  const taken = takenNames(config);
  const name = String(containerName || '').replace(/^\//, '');
  const mine = (holder) => holder.container === name || (holder.route && appRoutes(config, name).includes(holder.route));
  ((web && web.available && web.domains) || []).forEach((d) => {
    const c = (choices.web || {})[d.zone];
    if (c && c.on) {
      const problem = nameProblem(c.host, d.zone);
      const holder = taken.get(lower(c.host));
      problems['web:' + d.zone] = problem || (holder && !mine(holder) ? 'taken' : '');
    }
  });
  (ports || []).forEach((p) => {
    const c = (choices.ports || {})[p.key];
    if (c && c.on && c.hostname) {
      const problem = nameProblem(c.hostname, p.zone);
      const holder = taken.get(lower(c.hostname));
      problems['port:' + p.key] = problem || (holder && !mine(holder) ? 'taken' : '');
    }
    if (c && c.on && String(c.externalPort || '').trim() !== '') {
      const n = Number(c.externalPort);
      if (!Number.isInteger(n) || n < 1 || n > 65535) problems['port:' + p.key + ':external'] = 'externalPort';
    }
  });
  Object.keys(problems).forEach((key) => { if (!problems[key]) delete problems[key]; });
  return problems;
};

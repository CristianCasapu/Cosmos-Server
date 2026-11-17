// Fork addition (plan 49): every interface of an app, and the one its tile
// opens for this visitor. One rule for every screen that opens an app (the
// home page, ServApps, the app's page, the chips of its URLs), so that they
// cannot disagree. Nothing here imports the UI (node --test).
//
// An app is the URLs that lead to the same container (or, for URLs that do
// not lead to a container, to the same target). Each URL is an interface with
// a role (the app's administration, its page for users, its API, other: as
// set on the URL form, else as the server proposed it, GET /api/dashboard)
// and a kind of address:
//
//   panel   no host of its own: the panel's own address with the URL's path
//   local   a .local name (multicast DNS: the home network only, and not on
//           every device)
//   home    a name for use at home (<app>.home.<domain>: public DNS, a private
//           address: the home network, or a VPN that routes it)
//   lan     a private IPv4 address (the same)
//   vpn     an address of a VPN range (that VPN only)
//   public  any other name: opens from anywhere
//
// Where the visitor is comes from the server (GET /api/status, "visitor":
// home / vpn / away, and whether it is sure); the page's own address says
// which names this device uses. From these, each interface "opens", "maybe"
// opens or is "home-only" from here, and the tile opens the best one that
// opens: the administration first for administrators (the owner's rule), the
// page for users first for everyone else, never an API while the app has a
// page; on the kind of address the visitor is using.
//
// Links are built from the configuration's own hosts only, after a check of
// their syntax; anything else is left out. Labels are text.

import { SERVICE_PORTS, serviceFamily, connectionTemplate } from './services.js';

export const ROLES = ['admin', 'user', 'api', 'other'];
export const KINDS = ['panel', 'local', 'home', 'lan', 'vpn', 'public'];

const lower = (value) => String(value === undefined || value === null ? '' : value).trim().toLowerCase();

// ---------------------------------------------------------------------------
// addresses

const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;
const NAME = /^(?=.{1,253}$)([a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?)(\.[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?)*\.?$/;
const IPV6 = /^\[[0-9a-f:.]+\]$/;

// A host as a route stores it ("app.example.org", "192.168.1.10:8080"), split
// and checked: { name, port } or null when it is not a plain host.
export const parseHost = (host) => {
  const text = lower(host);
  if (!text || text.length > 260) return null;
  let name = text;
  let port = '';
  const m = text.match(/^(.*):(\d{1,5})$/);
  if (m && !/^\[[^\]]*$/.test(m[1]) && (m[1].startsWith('[') || !m[1].includes(':'))) {
    name = m[1];
    port = m[2];
    if (Number(port) < 1 || Number(port) > 65535) return null;
  }
  if (!(IPV4.test(name) || NAME.test(name) || IPV6.test(name))) return null;
  return { name: name.replace(/\.$/, ''), port };
};

const ipv4Parts = (name) => (IPV4.test(name) ? name.split('.').map(Number) : null);

const isPrivateV4 = (p) => p && (p[0] === 10 || p[0] === 127 || (p[0] === 172 && p[1] >= 16 && p[1] <= 31) || (p[0] === 192 && p[1] === 168) || (p[0] === 169 && p[1] === 254));
const isVPNv4 = (p) => p && p[0] === 100 && p[1] >= 64 && p[1] <= 127;

// The kind of an address (see the top). homeSuffixes: the suffixes of the
// names for use at home in force ("home.example.org"), when known.
export const hostKind = (host, homeSuffixes) => {
  const parsed = parseHost(host);
  if (!parsed) return '';
  const { name } = parsed;
  if (name === 'localhost' || name.endsWith('.local')) return 'local';
  const v4 = ipv4Parts(name);
  if (v4) return isVPNv4(v4) ? 'vpn' : isPrivateV4(v4) ? 'lan' : 'public';
  if (IPV6.test(name)) {
    const inner = name.slice(1, -1);
    if (/^fd7a:115c:a1e0:/.test(inner)) return 'vpn';
    if (/^(f[cd]|fe[89ab])/.test(inner) || inner === '::1') return 'lan';
    return 'public';
  }
  if ((homeSuffixes || []).some((suffix) => suffix && name.endsWith('.' + lower(suffix)))) return 'home';
  if (!name.includes('.')) return 'local'; // a bare name: only a local resolver knows it
  return 'public';
};

// Where the visitor is: the server's answer, else a guess from the page's
// own address (an old server, or the status not loaded yet).
export const visitorPlace = (status, location) => {
  const v = status && status.visitor;
  if (v && ['home', 'vpn', 'away'].includes(v.where)) return { where: v.where, sure: v.sure === true };
  const kind = hostKind((location && location.hostname) || '');
  if (kind === 'local' || kind === 'lan') return { where: 'home', sure: false };
  if (kind === 'vpn') return { where: 'vpn', sure: false };
  return { where: 'away', sure: false };
};

// Whether an address of a kind opens from where the visitor is: 'opens',
// 'maybe' or 'home-only' ('vpn-only' for a VPN address seen from elsewhere).
export const reachFrom = (kind, place) => {
  const where = (place && place.where) || 'away';
  const sure = !place || place.sure !== false;
  if (kind === 'panel' || kind === 'public') return 'opens';
  if (kind === 'vpn') return where === 'vpn' ? 'opens' : where === 'home' ? 'maybe' : 'vpn-only';
  if (where === 'home') return sure ? 'opens' : 'maybe';
  if (where === 'vpn') return kind === 'local' ? 'home-only' : 'maybe';
  return 'home-only';
};

// ---------------------------------------------------------------------------
// links

const SAFE_PATH = /^\/[A-Za-z0-9._~%/@:+,;=!$&()*-]*$/;
const SAFE_ADMIN_PATH = /^\/[A-Za-z0-9._~%/@:+,;=!$&()*#-]*$/;

// The path of a URL, '' for none, null when it is not a plain path.
export const routePath = (route) => {
  if (!route || !route.UsePathPrefix || !route.PathPrefix) return '';
  const path = String(route.PathPrefix);
  return path.length <= 512 && SAFE_PATH.test(path) && !path.startsWith('//') ? path : null;
};

const DEFAULT_PORT = { http: '80', https: '443' };

// The port a host without one is reached on: the router's external port for a
// public name, Cosmos' own for the others, else the panel's own port.
const portFor = (kind, scheme, location, ports) => {
  if (ports) {
    const local = kind !== 'public';
    const port = scheme === 'http' ? (local ? ports.http : ports.externalHttp) : (local ? ports.https : ports.externalHttps);
    if (port) return String(port);
  }
  return String((location && location.port) || '');
};

// A checked http(s) address, or '' when it is not one.
export const safeURL = (text) => {
  try {
    const url = new URL(String(text));
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return '';
    if (url.username || url.password) return '';
    if (url.href.length > 2048) return '';
    // "https://app.example.org", as the address was written, without the slash URL adds
    return url.pathname === '/' && !url.search && !url.hash && !String(text).endsWith('/') ? url.href.slice(0, -1) : url.href;
  } catch (e) {
    return '';
  }
};

// The address a URL opens: "<scheme>://<host>[:port]<path>", the scheme and
// port of the page (or, for a host without port, the port Cosmos is reached
// on). '' when the host or the path is not plain.
export const routeURL = (route, location, ports, extraPath) => {
  const loc = location || {};
  const scheme = String(loc.protocol || 'https:').replace(/:$/, '') === 'http' ? 'http' : 'https';
  const path = routePath(route);
  if (path === null) return '';
  let tail = path;
  if (extraPath) {
    if (!SAFE_ADMIN_PATH.test(extraPath) || extraPath.startsWith('//')) return '';
    tail = path.replace(/\/$/, '') + extraPath;
  }
  if (!route || !route.UseHost || !route.Host) {
    const origin = safeURL(String(loc.origin || ''));
    return origin ? safeURL(origin.replace(/\/$/, '') + tail) : '';
  }
  const parsed = parseHost(route.Host);
  if (!parsed) return '';
  let host = parsed.name;
  let port = parsed.port;
  if (!port) {
    port = portFor(hostKind(route.Host), scheme, loc, ports);
  }
  if (port && port !== DEFAULT_PORT[scheme]) host += ':' + port;
  return safeURL(scheme + '://' + host + tail);
};

// ---------------------------------------------------------------------------
// apps

const containerOf = (route) => {
  if (!route || route.Mode !== 'SERVAPP') return '';
  const m = String(route.Target || '').match(/^[a-z][a-z0-9+.-]*:\/\/([^/:?#]+)/i);
  return m ? lower(m[1]) : '';
};

const targetPortOf = (route) => {
  const m = String((route && route.Target) || '').match(/^([a-z][a-z0-9+.-]*):\/\/[^/?#]*?(?::(\d{1,5}))?(?:[/?#]|$)/i);
  if (!m) return 0;
  if (m[2]) return Number(m[2]);
  return lower(m[1]) === 'https' ? 443 : lower(m[1]) === 'http' ? 80 : 0;
};

// What URLs of one app have in common: the container they lead to, else
// their target. A URL without a target is an app of its own.
const appKey = (route, index) => {
  const container = containerOf(route);
  if (container) return 'container ' + container;
  const target = lower(route && route.Target).replace(/\/+$/, '');
  return target ? String(route.Mode || '') + ' ' + target : 'route ' + index;
};

// Groups URLs into apps, in the order of each app's first URL. `skip(route)`
// leaves a URL out first (hidden from the home page, container stopped).
export const groupApps = (routes, skip) => {
  const apps = new Map();
  (routes || []).forEach((route, index) => {
    if (!route || (skip && skip(route))) return;
    const key = appKey(route, index);
    if (!apps.has(key)) apps.set(key, { key, container: containerOf(route), routes: [] });
    apps.get(key).routes.push(route);
  });
  return Array.from(apps.values());
};

// The app's own name: the URL named after its container, else the shortest.
const appName = (app) => {
  const own = app.container && app.routes.find((route) => lower(route.Name) === app.container);
  const named = own || app.routes.slice().sort((a, b) => String(a.Name || '').length - String(b.Name || '').length)[0];
  return String((named && named.Name) || '');
};

const roleOf = (route) => (ROLES.includes(route && route.ForkRole) ? route.ForkRole : 'user');

const cleanLabel = (label) => String(label || '').replace(/[\u0000-\u001f\u007f<>"'`\\{}\u2028\u2029]/g, '').trim().slice(0, 40);

// The domain part two hosts share, as a number of labels.
const sharedLabels = (a, b) => {
  const left = lower(a).split('.').reverse();
  const right = lower(b).split('.').reverse();
  let n = 0;
  while (n < left.length && n < right.length && left[n] && left[n] === right[n]) n++;
  return n;
};

const isWebPort = (port) => Number(port) === 443 || Number(port) === 8443 || Number(port) === 9443;

// The non-web endpoint of a published port: what it speaks, if known.
const serviceOf = (image, privatePort) => {
  const family = serviceFamily(image || '');
  if (family && Number(family.port) === Number(privatePort)) return { name: family.name, database: !!family.database };
  const known = SERVICE_PORTS[Number(privatePort)];
  return known ? { name: known.protocol, database: !!known.database } : null;
};

// The host other devices reach the server's published ports at, from here:
// the address the port is published on, else the page's own host when that is
// an address of the server at home or on the VPN, else a local name of the
// server's (any .local name of an app leads to the server).
const endpointHost = (ip, location, ownRoutes, allRoutes) => {
  if (ip && (IPV4.test(ip) || /^[0-9a-f:]+$/i.test(ip))) return ip.includes(':') ? '[' + ip + ']' : ip;
  const here = lower(location && location.hostname);
  const kind = hostKind(here);
  if (kind === 'local' || kind === 'lan' || kind === 'vpn' || kind === 'home') return parseHost(here) ? parseHost(here).name : '';
  // the app's own .local name first (it reads best), else any app's
  const local = [...(ownRoutes || []), ...(allRoutes || [])].map((r) => (r && r.UseHost ? parseHost(r.Host) : null)).find((p) => p && hostKind(p.name) === 'local');
  return local ? local.name : '';
};

// One app's interfaces: web addresses (its URLs, their names for use at home,
// an administration path of a page for users, its published web ports) and,
// for administrators, its other published ports as endpoints to copy.
export const appInterfaces = (app, ctx) => {
  const c = ctx || {};
  const place = c.place || visitorPlace(null, c.location);
  const suffixes = c.homeSuffixes || [];
  const out = [];
  const seen = new Set();
  const add = (item) => {
    const key = item.web ? item.url : item.copy;
    if (!key || seen.has(key)) return;
    seen.add(key);
    out.push(item);
  };
  app.routes.forEach((route) => {
    const role = roleOf(route);
    const label = cleanLabel(route.ForkLabel);
    const base = { route: route.Name, role, label, roleSource: route.ForkRoleSource || '', auth: route.AuthEnabled === true, keptHome: route.ForkHomeOnly === true, web: true };
    const hosts = [route.UseHost && route.Host ? route.Host : ''];
    (Array.isArray(route.ForkHomeHosts) ? route.ForkHomeHosts.slice(0, 4) : []).forEach((h) => hosts.push(h));
    hosts.forEach((host, i) => {
      const r = host ? { ...route, UseHost: true, Host: host } : { ...route, UseHost: false };
      const kind = !host ? 'panel' : i > 0 ? 'home' : hostKind(host, suffixes);
      if (!kind) return;
      const url = routeURL(r, c.location, c.ports);
      if (!url) return;
      add({ ...base, kind, host: host || '', url, reach: reachFrom(kind, place), homeName: i > 0 });
      if (role === 'user' && route.ForkAdminPath) {
        const adminURL = routeURL(r, c.location, c.ports, String(route.ForkAdminPath));
        if (adminURL) add({ ...base, role: 'admin', roleSource: 'path', label: '', derived: true, kind, host: host || '', url: adminURL, reach: reachFrom(kind, place), homeName: i > 0 });
      }
    });
  });
  if (c.isAdmin) {
    const first = app.routes.find((route) => Array.isArray(route.ForkPorts));
    const ports = (Array.isArray(app.ports) ? app.ports : first ? first.ForkPorts : []).slice(0, 32);
    const image = app.image || (first ? first.ForkImage : '');
    ports.forEach((p) => {
      // GET /api/dashboard's { private, public, protocol, ip } or Docker's { PrivatePort, PublicPort, Type, IP }
      const privatePort = Number(p && (p.private !== undefined ? p.private : p.PrivatePort));
      const publicPort = Number(p && (p.public !== undefined ? p.public : p.PublicPort));
      if (p && p.Type !== undefined && p.protocol === undefined) p = { ...p, protocol: p.Type, ip: p.IP === '0.0.0.0' || p.IP === '::' ? '' : p.IP };
      const protocol = lower(p && p.protocol) === 'udp' ? 'udp' : 'tcp';
      if (!Number.isInteger(publicPort) || publicPort < 1 || publicPort > 65535) return;
      const host = endpointHost(lower(p.ip), c.location, app.routes, c.allRoutes);
      if (!host) return;
      const kind = hostKind(host) || 'lan';
      const reach = reachFrom(kind === 'public' ? 'lan' : kind, place); // a published port is not forwarded by this
      const web = app.routes.find((route) => targetPortOf(route) === privatePort && containerOf(route) === app.container);
      if (web && protocol === 'tcp') {
        const scheme = isWebPort(privatePort) || /^https:/i.test(String(web.Target || '')) ? 'https' : 'http';
        const url = safeURL(scheme + '://' + host + ':' + publicPort);
        if (url) add({ route: web.Name, role: roleOf(web), label: cleanLabel(web.ForkLabel), roleSource: web.ForkRoleSource || '', auth: false, keptHome: web.ForkHomeOnly === true, web: true, direct: true, kind: 'lan', host, url, reach });
        return;
      }
      const service = serviceOf(image, privatePort);
      const address = host + ':' + publicPort;
      const connection = service && service.database ? connectionTemplate({ name: service.name }, host, publicPort) : '';
      add({ role: 'other', label: service ? service.name : '', web: false, protocol, port: publicPort, privatePort, kind, host, copy: address, connection, reach });
    });
  }
  return out;
};

// The order of kinds at home: the kind the page itself was opened by first
// (this device resolves it), then the device's preference.
const homeOrder = (location, prefer) => {
  const pageKind = hostKind((location && location.hostname) || '');
  const homeFirst = ['home', 'local', 'lan', 'vpn', 'panel', 'public'];
  const order = prefer === 'internet' ? ['panel', 'public', 'home', 'local', 'lan', 'vpn'] : homeFirst;
  if (pageKind && pageKind !== 'public' && order.includes(pageKind)) {
    return [pageKind, ...order.filter((k) => k !== pageKind)];
  }
  return order;
};

const kindOrder = (place, location, prefer) => {
  if (place.where === 'home') return homeOrder(location, prefer);
  if (place.where === 'vpn') return ['vpn', 'panel', 'public', 'home', 'lan', 'local'];
  return ['panel', 'public', 'home', 'lan', 'vpn', 'local'];
};

// The interface the tile opens, null when no page of the app opens from here.
export const chooseInterface = (interfaces, ctx) => {
  const c = ctx || {};
  const place = c.place || visitorPlace(null, c.location);
  const web = (interfaces || []).filter((i) => i.web);
  const pages = web.filter((i) => i.role !== 'api');
  const pool = (pages.length > 0 ? pages : web).filter((i) => i.reach === 'opens');
  if (pool.length === 0) return null;
  const roles = c.isAdmin ? ['admin', 'user', 'other', 'api'] : ['user', 'admin', 'other', 'api'];
  const kinds = kindOrder(place, c.location, c.prefer);
  const here = lower(c.location && c.location.hostname);
  return pool.slice().sort((a, b) => (roles.indexOf(a.role) - roles.indexOf(b.role))
    || ((a.direct ? 1 : 0) - (b.direct ? 1 : 0))
    || (kinds.indexOf(a.kind) - kinds.indexOf(b.kind))
    || (sharedLabels(b.host, here) - sharedLabels(a.host, here)))[0];
};

// The state of a tile: 'opens' (it opens `chosen`), 'home-only' (its pages
// open at home only: from the internet nothing does), 'vpn-only', or 'none'
// (it has no address at all).
const tileState = (interfaces, chosen) => {
  if (chosen) return 'opens';
  const web = interfaces.filter((i) => i.web);
  if (web.length === 0) return 'none';
  if (web.some((i) => i.reach === 'maybe' || i.reach === 'home-only')) return 'home-only';
  return 'vpn-only';
};

// An app as the container pages know it: the container (name, image, its
// published ports as Docker lists them) and its URLs.
export const containerApp = (name, routes, image, ports) => ({
  key: 'container ' + lower(String(name || '').replace(/^\//, '')),
  container: lower(String(name || '').replace(/^\//, '')),
  routes: (routes || []).filter(Boolean),
  image: image || '',
  ports: Array.isArray(ports) ? ports : [],
});

// A container's published ports as Docker's inspection gives them
// (NetworkSettings.Ports: { "9000/tcp": [{ HostIp, HostPort }] }), in the
// form of Docker's list.
export const inspectPorts = (info) => {
  const ports = (info && info.NetworkSettings && info.NetworkSettings.Ports) || {};
  const out = [];
  Object.keys(ports).slice(0, 64).forEach((key) => {
    const m = String(key).match(/^(\d{1,5})\/(tcp|udp|sctp)$/);
    if (!m) return;
    (Array.isArray(ports[key]) ? ports[key] : []).forEach((binding) => {
      const publicPort = Number(binding && binding.HostPort);
      if (publicPort > 0 && publicPort < 65536) out.push({ PrivatePort: Number(m[1]), PublicPort: publicPort, Type: m[2], IP: String((binding && binding.HostIp) || '') });
    });
  });
  return out;
};

// The tile of one app (see appTiles).
export const appTile = (app, ctx) => {
  const c = ctx || {};
  const interfaces = appInterfaces(app, { ...c, allRoutes: c.allRoutes || app.routes });
  const chosen = chooseInterface(interfaces, c);
  const route = (chosen && app.routes.find((r) => r.Name === chosen.route)) || app.routes[0] || null;
  return {
    key: app.key,
    container: app.container,
    name: appName(app) || app.container,
    route,
    routes: app.routes,
    interfaces,
    chosen,
    link: chosen ? chosen.url : '',
    state: tileState(interfaces, chosen),
    // plan 50: an app with a how-to guide always has the menu (it holds "How to use it")
    menu: interfaces.length > 1 || (interfaces.length === 1 && !chosen) || app.routes.some((r) => r && r.ForkGuide),
    keptHome: app.routes.some((r) => r.ForkHomeOnly === true),
  };
};

// tiles: [{ key, name, route, routes, interfaces, chosen, link, state, menu }]
// route: the URL the tile stands for (its icon); link: what it opens ('' when
// nothing opens from here); menu: whether it has more to offer than one link.
export const appTiles = (routes, ctx, skip) => groupApps(routes, skip).map((app) => appTile(app, { ...(ctx || {}), allRoutes: routes }));

// URLs as the configuration holds them, with what GET /api/dashboard adds
// (role, label, names for use at home, the container's ports): matched by name.
const DASHBOARD_FIELDS = ['ForkRole', 'ForkRoleSource', 'ForkLabel', 'ForkHomeOnly', 'ForkAdminPath', 'ForkHomeHosts', 'ForkContainer', 'ForkImage', 'ForkPorts',
  // plan 50: the guide's facts
  'ForkGuide', 'ForkGuideSource', 'ForkGuideProposed', 'ForkGuideRegion', 'ForkShielded', 'ForkOwnCAHosts', 'ForkNetworks'];
export const mergeDashboard = (routes, dashboard) => {
  const byName = new Map();
  (Array.isArray(dashboard) ? dashboard : []).forEach((d) => { if (d && d.Name && !byName.has(d.Name)) byName.set(d.Name, d); });
  return (routes || []).filter(Boolean).map((route) => {
    const d = byName.get(route.Name);
    if (!d) return route;
    const out = { ...route };
    DASHBOARD_FIELDS.forEach((field) => { if (d[field] !== undefined) out[field] = d[field]; });
    return out;
  });
};

// The tile of the app one URL belongs to (a chip of that URL opens what the
// tile would, when the URL's own address does not open from here).
export const interfaceOfRoute = (route, ctx) => {
  if (!route) return null;
  const app = { key: 'one', container: containerOf(route), routes: [route] };
  return appInterfaces(app, ctx).find((i) => i.web && !i.homeName && !i.derived) || null;
};

// The order of the "All addresses" menu: what the tile opens first, then the
// pages (in the order of the roles for this visitor), the addresses that open
// from here before the others, the published ports after the URLs, the API,
// and the endpoints to copy last.
export const menuOrder = (tile, isAdmin) => {
  const roles = isAdmin ? ['admin', 'user', 'other', 'api'] : ['user', 'admin', 'other', 'api'];
  const reach = ['opens', 'maybe', 'home-only', 'vpn-only'];
  const items = ((tile && tile.interfaces) || []).map((item, index) => ({ item, index }));
  return items.sort((a, b) => ((b.item === tile.chosen ? 1 : 0) - (a.item === tile.chosen ? 1 : 0))
    || ((a.item.web ? 0 : 1) - (b.item.web ? 0 : 1))
    || (roles.indexOf(a.item.role) - roles.indexOf(b.item.role))
    || ((a.item.direct ? 1 : 0) - (b.item.direct ? 1 : 0))
    || (reach.indexOf(a.item.reach) - reach.indexOf(b.item.reach))
    || (a.index - b.index)).map((x) => x.item);
};

// Whether a full address (a server-built link: the sign-in card's) opens
// from where the visitor is: 'opens', 'maybe', 'home-only', 'vpn-only', or
// '' when it is not an http(s) address.
export const urlReach = (url, ctx) => {
  const safe = safeURL(url);
  if (!safe) return '';
  const c = ctx || {};
  return reachFrom(hostKind(new URL(safe).host, c.homeSuffixes) || 'public', c.place || visitorPlace(null, c.location));
};

// What a menu line says about where an address works.
export const reachMark = (item) => {
  if (!item) return '';
  if (item.reach === 'vpn-only') return 'vpn';
  if (item.kind === 'public' || item.kind === 'panel') return 'internet';
  return 'home';
};

// ---------------------------------------------------------------------------
// the device's preference at home (per browser)

export const PREFER_KEY = 'cosmos-fork-links-at-home';

export const readPreference = (storage) => {
  try {
    return storage && storage.getItem(PREFER_KEY) === 'internet' ? 'internet' : 'home';
  } catch (e) {
    return 'home';
  }
};

export const writePreference = (storage, value) => {
  try {
    if (storage) storage.setItem(PREFER_KEY, value === 'internet' ? 'internet' : 'home');
  } catch (e) {
    // private mode: the default stays
  }
};

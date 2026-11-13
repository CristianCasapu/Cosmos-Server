// Fork addition: what the simple view of the URL form knows and proposes, so
// that an address for an app is a matter of confirming, not of filling in a
// dozen fields: which port of the app to point at (and why that one), how
// the app speaks, who may open the address, and whether a URL can be shown
// in the simple view at all. Also the plan behind "give every app without a
// URL its local address". Nothing here imports the UI or the API, so it can
// be exercised on its own; the list of usual ports
// (pages/servapps/defaultport.json) is handed in.
import { schemeForPort } from './portLinks.js';
import { isLocalHost } from './appLinks.js';

const WEB = /^https?:\/\//i;

// "http://radarr:7878" → { scheme: 'http', host: 'radarr', port: '7878' }.
// A target without http(s):// (a TCP/UDP forward) keeps what it has.
export const targetParts = (target) => {
  const text = String(target || '').trim();
  const m = text.match(/^(?:([a-z][a-z0-9+.-]*):\/\/)?([^/:]*)(?::(\d*))?(.*)$/i) || [];
  return { scheme: (m[1] || '').toLowerCase(), host: m[2] || '', port: m[3] || '', rest: m[4] || '' };
};

export const buildTarget = ({ scheme, host, port }) => (scheme ? scheme + '://' : '') + (host || '') + ':' + (port === undefined || port === null ? '' : port);

// The TCP ports a container listens on, as Docker lists them (declared by
// the image or published), smallest first, each once.
export const containerPorts = (container) => {
  const ports = new Set();
  ((container && container.Ports) || []).forEach((port) => {
    const number = Number(port && port.PrivatePort);
    if (port && (port.Type || 'tcp') === 'tcp' && Number.isInteger(number) && number > 0 && number <= 65535) {
      ports.add(number);
    }
  });
  return [...ports].sort((a, b) => a - b);
};

const usualPortOf = (image, defaults) => {
  const overrides = (defaults && defaults.overrides) || {};
  // the last part of the image name, without registry, owner and tag
  const name = String(image || '').split('@')[0].split('/').pop().split(':')[0];
  const key = Object.keys(overrides).find((pattern) => {
    try {
      return new RegExp(pattern, 'i').test(name) || new RegExp(pattern, 'i').test(String(image || ''));
    } catch (e) {
      return false;
    }
  });
  return key ? Number(overrides[key]) : null;
};

const matchesCommon = (port, entry) => (String(entry).endsWith('*')
  ? String(port).startsWith(String(entry).slice(0, -1))
  : String(port) === String(entry));

// proposePort picks the port of a container a URL should point at and says
// where the proposal comes from:
//   usual     the app's usual web port (known for this image), which the container has
//   only      the only port the container has
//   common    several ports: the one that is a common web port (80, 3000, 8080, 8xxx, 3xxx)
//   first     several ports, none of them a common web port: the smallest
//   usualOnly the app's usual web port, although the image declares no port at all
//   none      nothing to go by (port: null)
export const proposePort = (image, ports, defaults) => {
  const list = (ports || []).map(Number).filter((port) => Number.isInteger(port) && port > 0);
  const usual = usualPortOf(image, defaults);
  if (usual && list.includes(usual)) {
    return { port: usual, source: 'usual' };
  }
  if (list.length === 1) {
    return { port: list[0], source: 'only' };
  }
  if (list.length > 1) {
    for (const entry of (defaults && defaults.priority) || []) {
      const found = list.find((port) => matchesCommon(port, entry));
      if (found) {
        return { port: found, source: 'common' };
      }
    }
    return { port: list[0], source: 'first' };
  }
  if (usual) {
    return { port: usual, source: 'usualOnly' };
  }
  return { port: null, source: 'none' };
};

// How the app speaks on a port: https on its own HTTPS ports, else http.
export { schemeForPort };

// ---- who may open an address ------------------------------------------------
// One choice in place of two switches on two tabs.
//   anyone  whoever reaches the address; the app's own sign-in protects it
//   users   only people signed in to Cosmos
//   admins  only Cosmos administrators
export const accessOf = (route) => {
  if (route && route.AuthEnabled) {
    return route.AdminOnly ? 'admins' : 'users';
  }
  return 'anyone';
};

export const accessFields = (choice) => ({
  AuthEnabled: choice === 'users' || choice === 'admins',
  AdminOnly: choice === 'admins',
});

// ---- can the simple view show this URL? --------------------------------------
// It shows an address (a host) that leads to an app or to another web
// address. Everything else is shown with all settings, and `why` lists the
// reasons in the order they are checked.
export const guideFits = (route) => {
  const why = [];
  const mode = (route && route.Mode) || 'SERVAPP';
  if (mode !== 'SERVAPP' && mode !== 'PROXY') {
    why.push('mode');
  }
  const target = String((route && route.Target) || '');
  if ((mode === 'SERVAPP' || mode === 'PROXY') && target && !WEB.test(target)) {
    why.push('socket');
  }
  if (route && route.UsePathPrefix) {
    why.push('path');
  }
  if (route && !route.UseHost) {
    why.push('noHost');
  }
  if (route && route.Tunnel) {
    why.push('tunnel');
  }
  if (route && route.AuthEnabled === false && route.AdminOnly) {
    why.push('access');
  }
  return { fits: why.length === 0, why };
};

// ---- a new URL for an app ----------------------------------------------------
// The description says where the address works: upstream wrote "Expose <app>
// to the internet" for every address, the local ones included.
export const describeAddress = (name, host) => (isLocalHost(host)
  ? name + ', on the local network'
  : name + ', from the internet');

// The route the "New URL" dialog creates for an app, with upstream's own
// defaults for everything the admin is not asked.
export const newAppRoute = ({ name, host, port, scheme, access }) => {
  const how = scheme || schemeForPort(port);
  return {
    Name: name,
    Description: describeAddress(name, host),
    Mode: 'SERVAPP',
    Target: buildTarget({ scheme: how, host: name, port }),
    UseHost: true,
    Host: host,
    UsePathPrefix: false,
    PathPrefix: '',
    CORSOrigin: '',
    StripPathPrefix: false,
    ...accessFields(access || 'anyone'),
    // an app's own HTTPS certificate is its own: there is nothing to check it against
    AcceptInsecureHTTPSTarget: how === 'https',
    Timeout: 14400000,
    ThrottlePerMinute: 10000,
    BlockCommonBots: true,
    SmartShield: { Enabled: true },
  };
};

// A name no other URL has: the name itself, else "<name>-2", "<name>-3"…
export const freeName = (name, taken) => {
  const used = new Set(taken || []);
  if (!used.has(name)) {
    return name;
  }
  for (let n = 2; n < 1000; n++) {
    if (!used.has(name + '-' + n)) {
      return name + '-' + n;
    }
  }
  return name;
};

// An address as typed or pasted, reduced to what the field holds: no
// http(s)://, no path, no spaces around it, lower case.
export const tidyAddress = (text) => String(text || '').trim().replace(/^[a-z][a-z0-9+.-]*:\/\//i, '').replace(/\/.*$/, '').toLowerCase();

// The routes of a container, as utils/routes.jsx' getContainersRoutes finds them.
export const routesOfContainer = (routes, containerName) => {
  const name = String(containerName || '').replace(/^\//, '');
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = new RegExp('^(([a-z]+)://)?' + escaped + '(:?[0-9]+)?$', 'i');
  return (routes || []).filter((route) => route && route.Mode === 'SERVAPP' && match.test(String(route.Target || '')));
};

const hostOf = (route) => String((route && route.UseHost && route.Host) || '').trim().toLowerCase();

// planLocalAddresses lists, for the apps that have no URL at all, the local
// address each would get. `hostFor(name, routesSoFar)` proposes the address
// (the dialog's own proposal), given the routes that exist plus the ones
// planned before it. Each entry:
//   { name, image, state, host, port, source, route }
// state: 'ready'  — can be created as listed
//        'noPort' — nothing to point at: the container has no port and its usual one is not known
//        'stopped' — not running, so its ports are not known
//        'hostNetwork' — uses the server's own network: it has no address of its own to point at
//        'taken' — the address or the name is already used by another URL
export const planLocalAddresses = ({ containers, routes, hostFor, defaults, skip }) => {
  const all = [...(routes || [])];
  const plan = [];
  (containers || []).forEach((container) => {
    const name = String((container.Names && container.Names[0]) || '').replace(/^\//, '');
    if (!name || (skip || []).includes(name) || routesOfContainer(routes, name).length > 0) {
      return;
    }
    const entry = { name, image: container.Image || '', state: 'ready', host: '', port: null, source: 'none', route: null };
    plan.push(entry);
    const networks = (container.NetworkSettings && container.NetworkSettings.Networks) || {};
    if (container.State !== 'running') {
      entry.state = 'stopped';
      return;
    }
    if (networks.host) {
      entry.state = 'hostNetwork';
      return;
    }
    const proposal = proposePort(container.Image, containerPorts(container), defaults);
    entry.port = proposal.port;
    entry.source = proposal.source;
    // without a port of its own the usual one is a guess: left to the dialog, where it can be checked
    if (!proposal.port || proposal.source === 'usualOnly') {
      entry.state = 'noPort';
      return;
    }
    entry.host = String(hostFor(name, all) || '');
    const wanted = entry.host.trim().toLowerCase();
    if (!wanted || /\s/.test(wanted) || all.some((route) => route.Name === name || hostOf(route) === wanted)) {
      entry.state = 'taken';
      return;
    }
    entry.route = newAppRoute({ name, host: entry.host, port: proposal.port });
    all.push(entry.route);
  });
  return plan;
};

// Fork addition: what the screens need to know about ports on the router.
// Two things live here, both pure (nothing imports the UI, so they are
// exercised on their own, see routerPorts.test.mjs):
//
// 1. Cosmos' own addresses with the port they are really reached on. A route
//    stores a host without a port ("app.example.org"); the link to it needs
//    the port Cosmos' HTTPS port has from where the link is used: the
//    internal one on the local network, the external one (as the router
//    forwards it) from the internet. Every screen that shows or opens such an
//    address builds it here.
// 2. The published ports of apps that the router forwards (src/upnp): their
//    address, the note when the external port is not the wanted one, the
//    sentence for each state, and what the install forms offer.
import { isLocalHost } from './appLinks.js';

const DEFAULT_PORT = { https: 443, http: 80 };

const toPort = (value) => {
  const port = Number(String(value === undefined || value === null ? '' : value).trim());
  return Number.isInteger(port) && port >= 1 && port <= 65535 ? port : 0;
};

// The ports Cosmos is reached on: `https` / `http` on the server itself,
// `externalHttps` / `externalHttp` from the internet, "as mapped or
// configured": the server's answer (GET /api/upnp) when there is one, else
// what the settings say. null when neither is known: the caller then leaves
// the address as it is.
export const cosmosPorts = (config, status) => {
  const http = (config && config.HTTPConfig) || {};
  const upnp = http.UPnP || {};
  const https = toPort(status && status.httpsPort) || toPort(http.HTTPSPort);
  const plain = toPort(status && status.httpPort) || toPort(http.HTTPPort);
  if (!https && !plain) {
    return null;
  }
  const externalHttps = toPort(status && status.externalHTTPSPort)
    || (upnp.Enabled && toPort(upnp.ExternalHTTPSPort)) || https;
  const externalHttp = toPort(status && status.externalHTTPPort)
    || (upnp.Enabled && upnp.ForwardHTTP && toPort(upnp.ExternalHTTPPort)) || plain;
  return { https, http: plain, externalHttps, externalHttp };
};

// True when a host already says which port it is reached on.
export const hostHasPort = (host) => /^(\[[0-9a-f:.]+\]|[^:[\]]+):\d+$/i.test(String(host || '').trim());

// A host with the port a link needs, left out when it is the scheme's own.
export const hostWithPort = (host, port, scheme) => {
  const name = String(host || '').trim();
  const number = toPort(port);
  if (!name || hostHasPort(name) || !number || number === DEFAULT_PORT[scheme === 'http' ? 'http' : 'https']) {
    return name;
  }
  return name + ':' + number;
};

// The port an address is reached on: a local address (a .local name, a
// private IP) on Cosmos' own port, any other from the internet on the port
// the router forwards. 0 when unknown.
export const portFor = (host, ports, scheme) => {
  if (!ports) {
    return 0;
  }
  const plain = scheme === 'http';
  if (isLocalHost(String(host || '').replace(/:\d+$/, ''))) {
    return plain ? ports.http : ports.https;
  }
  return plain ? ports.externalHttp : ports.externalHttps;
};

// An address as it is shown and opened: the host of a route, a proposal or a
// preview, with its port. The value that is stored as a route's host stays
// without one (it then matches on every port).
export const shownAddress = (host, ports, scheme) => hostWithPort(host, portFor(host, ports, scheme || 'https'), scheme || 'https');

// A route as its chip shows and opens it: the same route with the port in
// its host (the chip opens "<scheme>://<host><path>" as it finds it). A
// route without a host of its own, or with a port already, is left alone.
// Only for display: what is stored never carries the port.
export const shownRoute = (route, ports, scheme) => {
  if (!route || !route.UseHost || !route.Host || !ports) {
    return route;
  }
  const host = shownAddress(String(route.Host).trim(), ports, scheme || 'https');
  return host === route.Host ? route : { ...route, Host: host };
};

// ---------------------------------------------------------------------------
// app ports on the router

export const portKey = (container, port, protocol) => String(container || '').replace(/^\//, '') + ':' + Number(port) + '/' + String(protocol || 'tcp').toLowerCase();

// What opening a port has to be confirmed with: the server compares it.
export const confirmText = portKey;

// The mapping (or pending choice) of one published port, or null.
export const findAppMapping = (status, container, containerPort, protocol) => ((status && status.apps) || []).find((m) =>
  portKey(m.container, m.containerPort, m.protocol) === portKey(container, containerPort, protocol)) || null;

// The mappings of some containers (a card shows a whole stack).
export const appMappingsOf = (status, containerNames) => {
  const names = (containerNames || []).map((name) => String(name || '').replace(/^\//, ''));
  return ((status && status.apps) || []).filter((m) => names.includes(m.container));
};

const isDomainName = (host) => /^(?=.*[a-z])([a-z0-9-]+\.)+[a-z]{2,}$/i.test(String(host || '')) && !isLocalHost(host);

// The name or address a forwarded port is reached at from the internet: its
// own DNS-only name when it has one (mapping.hostname, see utils/exposure.js),
// else the app's own external name, else the panel's name when it is a
// domain, else the public address the router reports, else ''. A name
// served through Cloudflare's proxy is never used: Cloudflare carries web
// pages on a few HTTPS ports only, never a forwarded port.
export const publicHost = ({ routes, config, status, mapping }) => {
  if (mapping && mapping.hostname) {
    return String(mapping.hostname).trim().toLowerCase();
  }
  const shielded = ((config && config.HTTPConfig && config.HTTPConfig.Cloudflare && config.HTTPConfig.Cloudflare.Shielded) || [])
    .map((name) => String(name).toLowerCase());
  const external = (routes || [])
    .map((route) => String((route && route.UseHost && route.Host) || '').trim().toLowerCase().replace(/:\d+$/, ''))
    .find((host) => isDomainName(host) && !shielded.includes(host));
  if (external) {
    return external;
  }
  const panel = String((config && config.HTTPConfig && config.HTTPConfig.Hostname) || '').trim().toLowerCase().replace(/:\d+$/, '');
  if (isDomainName(panel) && !shielded.includes(panel)) {
    return panel;
  }
  return String((status && status.externalIP) || '');
};

// The address of a forwarded port: { address, href, known }. A link only when
// an admin said the port speaks http or https: a peer port is not a web page.
export const portAddress = (mapping, host) => {
  const port = toPort(mapping && mapping.externalPort);
  if (!port) {
    return { address: '', href: '', known: false };
  }
  if (!host) {
    return { address: ':' + port, href: '', known: false };
  }
  const name = String(host).includes(':') && !String(host).startsWith('[') ? '[' + host + ']' : String(host);
  const scheme = mapping.scheme === 'http' || mapping.scheme === 'https' ? mapping.scheme : '';
  return {
    address: name + ':' + port,
    href: scheme ? scheme + '://' + hostWithPort(name, port, scheme) : '',
    known: true,
  };
};

// Sentences: [translation key suffix, English text]. The keys are
// "mgmt.router.<suffix>"; the test checks each against the translation file.
export const TEXT = {
  'note.moved': '{{external}} on the router, because {{wanted}} is taken.',
  'note.movedHolder': '{{external}} on the router, because {{wanted}} is used by {{holder}}.',
  'note.movedReserved': '{{external}} on the router, because Cosmos never asks a router for port {{wanted}}.',

  'state.active': 'The router forwards port {{external}} to port {{internal}} of this server (opened by Cosmos over UPnP). It reaches the app directly, without Cosmos\' login. Not tested from outside.',
  'state.stopped': 'Closed on the router while the container is stopped; opened again when it runs.',
  'state.gone': 'The container is gone: the port is closed on the router and the choice is forgotten after a few minutes.',
  'state.notPublished': 'The container no longer publishes this port: it is closed on the router and the choice is forgotten after a few minutes.',
  'state.localOnly': 'The port is published for this server only (127.0.0.1): there is nothing a router could forward to.',
  'state.otherAddress': 'The port is published on another address than the one the router reaches this server on: not forwarded.',
  'state.cosmosPort': 'This is one of Cosmos\' own ports: it is opened under Internet access, never as an app\'s.',
  'state.dockerUnavailable': 'Docker did not answer: Cosmos cannot tell whether the port is still published.',
  'state.router': 'The router did not answer. Cosmos does not try again by itself: press "Check now" under Internet access.',
  'state.conflict': 'Not opened: the port and the ones tried after it are in use on the router. Cosmos does not try again by itself: press "Check now" under Internet access, or choose another external port.',
  'state.conflictHolder': 'Not opened: port {{wanted}} is used by {{holder}}, and the ones tried after it are taken too. Cosmos does not try again by itself: press "Check now" under Internet access, or choose another external port.',
  'state.reserved': 'Not opened: port {{wanted}} is on the list of ports Cosmos never asks a router for.',
  'state.refused': 'The router refused: {{error}} Cosmos does not try again by itself: press "Check now" under Internet access.',
  'state.waiting': 'Not forwarded right now.',
  'state.failed': 'The router did not open the port. Cosmos does not try again by itself: press "Check now" under Internet access.',
};

const sentence = (id, params) => ({ key: 'mgmt.router.' + id, text: TEXT[id], params: params || {} });

const paramsOf = (mapping) => ({
  external: mapping.externalPort,
  internal: mapping.internalPort,
  wanted: mapping.wantedPort,
  holder: mapping.holder || '',
  error: mapping.error || '',
});

// The note under a forwarded port whose external port is not the wanted one
// ("8097 on the router, because 8096 is used by …"), or null.
export const portNote = (mapping) => {
  if (!mapping || !mapping.active || !mapping.wantedPort || mapping.externalPort === mapping.wantedPort) {
    return null;
  }
  if (mapping.code === 'movedReserved') {
    return sentence('note.movedReserved', paramsOf(mapping));
  }
  return sentence(mapping.holder ? 'note.movedHolder' : 'note.moved', paramsOf(mapping));
};

// What a port's line says about its mapping.
export const mappingSentence = (mapping) => {
  const params = paramsOf(mapping || {});
  if (!mapping) {
    return sentence('state.waiting');
  }
  if (mapping.active) {
    return sentence('state.active', params);
  }
  if (mapping.code === 'conflict') {
    return sentence(mapping.holder ? 'state.conflictHolder' : 'state.conflict', params);
  }
  if (TEXT['state.' + mapping.code]) {
    return sentence('state.' + mapping.code, params);
  }
  return sentence(mapping.state === 'failed' ? 'state.failed' : 'state.waiting', params);
};

// Ports that are usually a web interface. A hint only: it makes the dialog
// recommend the app's URL, it never decides anything.
export const WEB_PORTS = [80, 81, 443, 3000, 3001, 5000, 5001, 5055, 7878, 8000, 8008, 8080, 8081, 8090, 8096, 8123, 8181,
  8200, 8443, 8686, 8787, 8888, 8920, 8989, 9000, 9090, 9091, 9117, 9443, 9696, 32400];
const HTTPS_PORTS = [443, 8443, 8920, 9443];

// Is this published port the app's web interface? { web, scheme, route,
// guessed }: certain when one of the app's URLs targets the port (route is
// that URL, scheme its target's), a guess from the port number otherwise.
export const looksLikeWeb = ({ containerName, containerPort, protocol, routes }) => {
  if (String(protocol || 'tcp').toLowerCase() !== 'tcp') {
    return { web: false, scheme: '', route: null, guessed: false };
  }
  const name = String(containerName || '').replace(/^\//, '').toLowerCase();
  for (const route of routes || []) {
    const m = String((route && route.Target) || '').toLowerCase().match(/^(https?):\/\/([^/:]+)(?::(\d+))?\/?$/);
    if (m && m[2] === name && Number(m[3] || (m[1] === 'https' ? 443 : 80)) === Number(containerPort)) {
      return { web: true, scheme: m[1], route, guessed: false };
    }
  }
  if (WEB_PORTS.includes(Number(containerPort))) {
    return { web: true, scheme: HTTPS_PORTS.includes(Number(containerPort)) ? 'https' : 'http', route: null, guessed: true };
  }
  return { web: false, scheme: '', route: null, guessed: false };
};

// The ports a service definition (what the install forms send to
// /api/docker-service) publishes on the host, one per container port and
// protocol: [{ container, port, protocol, hostPort }]. Left out: a port
// without a fixed host port, a range, a port bound to the loopback address.
export const installPorts = (service) => {
  const found = new Map();
  const services = (service && typeof service === 'object' && service.services) || {};
  Object.entries(services).forEach(([key, definition]) => {
    if (!definition || typeof definition !== 'object') {
      return;
    }
    const container = String(definition.container_name || key);
    const ports = Array.isArray(definition.ports) ? definition.ports : [];
    ports.forEach((entry) => {
      let hostIp = '';
      let hostPort = '';
      let port = '';
      let protocol = 'tcp';
      if (entry && typeof entry === 'object') {
        hostIp = String(entry.host_ip || '');
        hostPort = String(entry.published === undefined ? '' : entry.published);
        port = String(entry.target === undefined ? '' : entry.target);
        protocol = String(entry.protocol || 'tcp');
      } else {
        const [spec, proto] = String(entry === undefined || entry === null ? '' : entry).trim().split('/');
        protocol = proto || 'tcp';
        // "[::1]:8080:80", "127.0.0.1:8080:80", "8080:80", "80"
        const m = spec.match(/^(?:(\[[^\]]*\]|[^:]*):)?([^:]*):([^:]+)$/);
        if (!m) {
          return;
        }
        hostIp = String(m[1] || '').replace(/^\[|\]$/g, '');
        hostPort = m[2];
        port = m[3];
      }
      protocol = protocol.toLowerCase();
      if (!/^\d+$/.test(hostPort) || !/^\d+$/.test(port) || !toPort(hostPort) || !toPort(port) || (protocol !== 'tcp' && protocol !== 'udp')) {
        return;
      }
      if (hostIp.startsWith('127.') || hostIp === '::1') {
        return;
      }
      const item = { container, port: Number(port), protocol, hostPort: Number(hostPort) };
      const key2 = portKey(container, item.port, protocol);
      if (!found.has(key2)) {
        found.set(key2, item);
      }
    });
  });
  return [...found.values()];
};

// The lines of the overview: Cosmos' own mappings first, then the apps'.
export const overviewRows = (status) => [
  ...((status && status.mappings) || []).map((m) => ({ ...m, kind: m.kind || 'cosmos' })),
  ...((status && status.apps) || []),
];

// "3 h", "12 min": how long a lease still runs, '' for a permanent one.
export const leaseText = (seconds) => {
  const s = Number(seconds) || 0;
  if (s <= 0) {
    return '';
  }
  if (s >= 2 * 86400) {
    return Math.round(s / 86400) + ' d';
  }
  if (s >= 2 * 3600) {
    return Math.round(s / 3600) + ' h';
  }
  return Math.max(1, Math.round(s / 60)) + ' min';
};

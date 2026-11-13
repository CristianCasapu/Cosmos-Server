// Fork addition: where the chip of a published port leads. Upstream opened
// "<the panel's protocol>://<host>:<port>": from the HTTPS panel that is
// https:// to a port that speaks plain HTTP, which the browser refuses.
// Nothing here imports the UI, so it can be exercised on its own.
import { publishedPorts } from './reachability.js';

// Container ports that speak HTTPS by convention: 443 and the ports named
// after it, and Jellyfin/Emby's HTTPS port. Everything else is taken for
// plain HTTP, which is what almost every published container port speaks.
const TLS_PORTS = [443, 8920];
export const schemeForPort = (containerPort) => {
  const port = Number(containerPort);
  return TLS_PORTS.includes(port) || (port > 1000 && port % 1000 === 443) ? 'https' : 'http';
};

// An address written as numbers. Browsers never force HTTPS on these.
export const isIPLiteral = (host) => {
  const text = String(host || '').replace(/^\[|\]$/g, '');
  return /^\d{1,3}(\.\d{1,3}){3}$/.test(text) || text.includes(':');
};

const hostForURL = (host) => (String(host).includes(':') && !String(host).startsWith('[') ? '[' + host + ']' : host);

// portLink says what one published port (an entry of publishedPorts) opens.
// location: { protocol, hostname } of the page (window.location).
//   { href, scheme, mayUpgrade }  a link; mayUpgrade: the browser may turn it
//                                 into https by itself and then fail
//   { href: null, why }           no link: 'udp', 'server' (bound to the
//                                 server itself) or 'invalid'
export const portLink = (published, location) => {
  const port = Number(published && published.port);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    return { href: null, why: 'invalid' };
  }
  if ((published.type || 'tcp') !== 'tcp') {
    return { href: null, why: 'udp' };
  }
  if (published.scope === 'server') {
    return { href: null, why: 'server' };
  }
  const pageHost = String((location && location.hostname) || '');
  const host = published.scope === 'address' && isIPLiteral(published.ip) ? published.ip : pageHost;
  if (!host) {
    return { href: null, why: 'invalid' };
  }
  const scheme = schemeForPort(published.privatePort);
  return {
    href: scheme + '://' + hostForURL(host) + ':' + port,
    scheme,
    // a name the browser has seen over HTTPS with a certificate it trusts is
    // opened over HTTPS on every port from then on (HSTS); an IP address never is
    mayUpgrade: scheme === 'http' && !!location && location.protocol === 'https:' && !isIPLiteral(host),
  };
};

// The chips of an app's published ports: one per port and protocol (Docker
// lists a port once for IPv4 and once for IPv6), each with its link.
export const portChips = (ports, location) => publishedPorts(ports).map((published) => ({
  key: published.port + '/' + published.type + '/' + published.privatePort,
  label: published.port + ':' + published.privatePort + (published.type === 'tcp' ? '' : '/' + published.type),
  published,
  ...portLink(published, location),
}));

// Fork addition: what the screens need to know about an app's links. Every
// app has a local address and, per attached domain, an external one, created
// as an extra route "<name>-<domain>" (see components/hostnameSuggestions.jsx).
// Nothing here imports the UI, so it can be exercised on its own.

// True when a route's host is a local link. Mirrors isLocalAddress in
// src/proxy/fork_local.go, which is what the server enforces: a .local name,
// or an IPv4 address in 10/8, 172.16/12, 192.168/16, 127/8 or 169.254/16,
// with or without a port.
export const isLocalHost = (host) => {
  const name = String(host || '').trim().toLowerCase().replace(/:\d+$/, '').replace(/\.$/, '');
  if (!name) {
    return false;
  }
  if (name.endsWith('.local')) {
    return true;
  }
  const parts = name.match(/^(0|[1-9]\d{0,2})\.(0|[1-9]\d{0,2})\.(0|[1-9]\d{0,2})\.(0|[1-9]\d{0,2})$/);
  if (!parts || parts.slice(1).some((part) => Number(part) > 255)) {
    return false;
  }
  const [a, b] = [Number(parts[1]), Number(parts[2])];
  return a === 10 || a === 127 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || (a === 169 && b === 254);
};

// Resolves once the server answers again after a change that made it reload
// (adding or deleting a route does).
export const serverBack = async () => {
  for (let i = 0; i < 40; i++) {
    await new Promise((resolve) => setTimeout(resolve, 750));
    try {
      await fetch('/cosmos/api/status', { credentials: 'same-origin' });
      return;
    } catch (e) {
      // still reloading
    }
  }
};

// The host a route answers on, in lower case; '' for a route without one.
export const routeHost = (route) => String((route && route.UseHost && route.Host) || '').trim().toLowerCase();

// An app's links in the order they are shown: the local ones, then the
// external ones. A group without a link is left out. ownHost is the address
// of Cosmos itself, where a route without a host of its own answers.
export const groupAppRoutes = (routes, ownHost) => {
  const local = [];
  const external = [];
  (routes || []).forEach((route) => (isLocalHost(routeHost(route) || ownHost) ? local : external).push(route));
  return [{ local: true, routes: local }, { local: false, routes: external }].filter((group) => group.routes.length > 0);
};

// The route an app's external addresses are copies of: its local one, else
// the first that is not named like an external copy, else the first.
export const baseRoute = (routes, domains) => {
  const list = routes || [];
  return list.find((route) => isLocalHost(routeHost(route)))
    || list.find((route) => !(domains || []).some((domain) => String(route.Name || '').endsWith('-' + domain)))
    || list[0]
    || null;
};

// True when the base route itself is the app's address under the domain.
export const baseIsUnderDomain = (base, domain, labels) => !!base
  && (labels || []).some((label) => label && routeHost(base) === label + '.' + domain);

// The routes, among an app's own, that are its external address under one
// domain: the copy named "<base name>-<domain>" (as long as its host is still
// under that domain), or any whose host is "<label>.<domain>". Never the base
// route, never a local link: these are the only routes detaching may delete.
export const domainRoutes = (routes, base, domain, labels) => (routes || []).filter((route) => {
  const host = routeHost(route);
  if (!base || !domain || route === base || !host || isLocalHost(host)) {
    return false;
  }
  return (route.Name === base.Name + '-' + domain && host.endsWith('.' + domain))
    || (labels || []).some((label) => label && host === label + '.' + domain);
});

// The address a route's chip opens. Upstream opened "https://<host>" for a
// route with a host of its own, without a port: wrong whenever Cosmos is not
// on the standard port, and on a server that shares its public address with
// another machine it opens that machine. A host without a port of its own
// gets the port the panel is being used on, unless that is the standard one
// of the scheme. `location` is window.location.
export const routeLink = (route, location) => {
  const loc = location || {};
  const scheme = String(loc.protocol || 'https:').replace(/:$/, '');
  const path = (route && route.UsePathPrefix && route.PathPrefix) || '';
  if (!route || !route.UseHost || !route.Host) {
    return String(loc.origin || '') + path;
  }
  let host = String(route.Host).trim();
  const standard = scheme === 'http' ? '80' : '443';
  if (!/:\d+$/.test(host) && loc.port && String(loc.port) !== standard) {
    host += ':' + loc.port;
  }
  return scheme + '://' + host + path;
};

// Fork addition: the home page shows one tile per app, not one per address.
//
// Every app has a local address and, per attached domain, an external one,
// kept as separate URLs ("radarr" and "radarr-example.org"). Upstream draws a
// tile per URL, so each app appeared twice, and a tile opened its address
// without the port the panel is used on. Here the URLs that lead to the same
// place are one app: its tile carries the app's own name and opens the
// address that fits where the panel is being used from: the local one at
// home, the external one from anywhere else.
//
// Nothing here imports the UI, so it can be exercised on its own.

import { isLocalHost, routeHost, routeLink } from './appLinks.js';

// The domain part two hosts share, as a number of labels (0 when none).
const sharedLabels = (a, b) => {
  const left = String(a || '').split('.').reverse();
  const right = String(b || '').split('.').reverse();
  let n = 0;
  while (n < left.length && n < right.length && left[n] && left[n] === right[n]) {
    n++;
  }
  return n;
};

// What two URLs of one app have in common: where they lead. A URL without a
// target is an app of its own.
const appKey = (route, index) => {
  const target = String((route && route.Target) || '').trim().toLowerCase().replace(/\/+$/, '');
  return target ? String(route.Mode || '') + ' ' + target : 'route ' + index;
};

// tiles: [{ route, name, link, routes }] in the order of the first URL of
// each app. `skip(route)` leaves a URL out before the apps are formed (hidden
// from the home page, container stopped). `location` is window.location.
export const homeTiles = (routes, location, skip) => {
  const loc = location || {};
  const here = String(loc.hostname || '').trim().toLowerCase();
  const atHome = isLocalHost(here);
  const apps = new Map();
  (routes || []).forEach((route, index) => {
    if (!route || (skip && skip(route))) {
      return;
    }
    const key = appKey(route, index);
    if (!apps.has(key)) {
      apps.set(key, []);
    }
    apps.get(key).push(route);
  });

  return Array.from(apps.values()).map((list) => {
    const local = list.filter((route) => routeHost(route) && isLocalHost(routeHost(route)));
    const external = list.filter((route) => routeHost(route) && !isLocalHost(routeHost(route)));
    // from outside: the external address closest to the name the panel is used under
    const nearest = external.slice().sort((a, b) => sharedLabels(routeHost(b), here) - sharedLabels(routeHost(a), here))[0];
    const route = (atHome ? local[0] || nearest : nearest || local[0]) || list[0];
    // the app's own name: its local URL's, else the shortest
    const named = local[0] || list.slice().sort((a, b) => String(a.Name || '').length - String(b.Name || '').length)[0];
    return { route, name: String((named && named.Name) || route.Name || ''), link: routeLink(route, loc), routes: list };
  });
};

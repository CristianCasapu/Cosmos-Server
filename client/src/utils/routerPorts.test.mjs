// node --test client/src/utils/routerPorts.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  TEXT, WEB_PORTS, appMappingsOf, confirmText, cosmosPorts, findAppMapping, hostHasPort, hostWithPort, installPorts, leaseText,
  looksLikeWeb, mappingSentence, overviewRows, portAddress, portFor, portKey, portNote, publicHost, routeAddress, shownAddress,
} from './routerPorts.js';
import { summarize } from './reachability.js';

const read = (path) => readFileSync(new URL(path, import.meta.url), 'utf8');
const fill = (s) => s.text.replace(/\{\{(\w+)\}\}/g, (_, name) => String(s.params[name]));

// HTTPS on 2053 here; the router forwards 8443 to it
const config = (upnp = {}) => ({ HTTPConfig: { HTTPSPort: '2053', HTTPPort: '2052', Hostname: '192.168.1.50', UPnP: upnp } });
const moved = { httpsPort: 2053, httpPort: 2052, externalHTTPSPort: 8443, externalHTTPPort: 2052 };
const ports = cosmosPorts(config(), moved);

test('the ports Cosmos is reached on: as mapped, else as configured, else its own', () => {
  assert.deepEqual(ports, { https: 2053, http: 2052, externalHttps: 8443, externalHttp: 2052 });
  // no answer from the server: the settings
  assert.deepEqual(cosmosPorts(config({ Enabled: true, ExternalHTTPSPort: '443' }), null), { https: 2053, http: 2052, externalHttps: 443, externalHttp: 2052 });
  // the external port of the settings counts only while forwarding is on
  assert.equal(cosmosPorts(config({ Enabled: false, ExternalHTTPSPort: '443' }), null).externalHttps, 2053);
  assert.equal(cosmosPorts(config({ Enabled: true, ExternalHTTPPort: '8080' }), null).externalHttp, 2052);
  assert.equal(cosmosPorts(config({ Enabled: true, ForwardHTTP: true, ExternalHTTPPort: '8080' }), null).externalHttp, 8080);
  // the server's answer wins over the form's idea
  assert.equal(cosmosPorts(config({ Enabled: true, ExternalHTTPSPort: '443' }), moved).externalHttps, 8443);
  // nothing known: null, and every builder then leaves the address alone
  assert.equal(cosmosPorts(null, null), null);
  assert.equal(cosmosPorts({ HTTPConfig: { HTTPSPort: 'x' } }, {}), null);
  assert.equal(shownAddress('app.example.org', null), 'app.example.org');
});

test('a host with its port', () => {
  assert.equal(hostWithPort('app.example.org', 8443, 'https'), 'app.example.org:8443');
  assert.equal(hostWithPort('app.example.org', 443, 'https'), 'app.example.org');
  assert.equal(hostWithPort('app.example.org', 80, 'http'), 'app.example.org');
  assert.equal(hostWithPort('app.example.org', 443, 'http'), 'app.example.org:443');
  assert.equal(hostWithPort('app.example.org:7200', 8443, 'https'), 'app.example.org:7200', 'a port that is there stays');
  assert.equal(hostWithPort('app.example.org', 0, 'https'), 'app.example.org');
  assert.equal(hostWithPort('app.example.org', 70000, 'https'), 'app.example.org');
  assert.equal(hostWithPort('', 8443, 'https'), '');
  assert.ok(hostHasPort('a.b:1') && hostHasPort('[::1]:443') && !hostHasPort('a.b') && !hostHasPort('::1'));
});

test('local addresses use Cosmos\' own port, the others the external one', () => {
  assert.equal(portFor('app.local', ports, 'https'), 2053);
  assert.equal(portFor('192.168.1.50', ports, 'https'), 2053);
  assert.equal(portFor('app.example.org', ports, 'https'), 8443);
  assert.equal(portFor('app.example.org', ports, 'http'), 2052);
  assert.equal(portFor('app.example.org', null, 'https'), 0);
  assert.equal(shownAddress('app.example.org', ports), 'app.example.org:8443');
  assert.equal(shownAddress('app.local', ports), 'app.local:2053');
  assert.equal(shownAddress('app.example.org', { ...ports, externalHttps: 443 }), 'app.example.org');
});

test('the link of a route', () => {
  const origin = 'https://192.168.1.50:2053';
  const route = (host, extra = {}) => ({ Name: 'app', UseHost: !!host, Host: host, ...extra });
  assert.deepEqual(routeAddress(route('app.example.org'), ports, origin), { label: 'app.example.org:8443', href: 'https://app.example.org:8443' });
  assert.deepEqual(routeAddress(route('app.local'), ports, origin), { label: 'app.local:2053', href: 'https://app.local:2053' });
  assert.deepEqual(routeAddress(route('app.example.org', { UsePathPrefix: true, PathPrefix: '/x' }), ports, origin),
    { label: 'app.example.org:8443/x', href: 'https://app.example.org:8443/x' });
  // a path that is not in use is not part of the address
  assert.equal(routeAddress(route('app.example.org', { UsePathPrefix: false, PathPrefix: '/old' }), ports, origin).href, 'https://app.example.org:8443');
  // a host with a port of its own is left as it is
  assert.equal(routeAddress(route('192.168.1.50:7200'), ports, origin).href, 'https://192.168.1.50:7200');
  // forwarded on 443: no port in the link
  assert.equal(routeAddress(route('app.example.org'), { ...ports, externalHttps: 443 }, origin).href, 'https://app.example.org');
  // the panel opened over plain HTTP
  assert.equal(routeAddress(route('app.example.org'), ports, 'http://192.168.1.50:2052').href, 'http://app.example.org:2052');
  // a route without a host is the panel's own address
  assert.deepEqual(routeAddress(route('', { UsePathPrefix: true, PathPrefix: '/x' }), ports, origin), { label: origin + '/x', href: origin + '/x' });
  // nothing known about the ports: as before
  assert.equal(routeAddress(route('app.example.org'), null, origin).href, 'https://app.example.org');
});

// Every screen that shows or opens an address of Cosmos' own builds it with
// the functions above. A screen that goes back to the bare host fails here.
test('every place that shows an address goes through this module', () => {
  const uses = {
    '../components/hostChip.jsx': ['routeAddress(route, useCosmosPorts(null), window.location.origin)', 'window.open(address.href', 'probeRoute(route, route.UseHost ? address.href'],
    '../components/hostnameSuggestions.jsx': ['shownAddress(externalAddress(name, domain), ports)', '<ExternalPortNote config={config}'],
    '../components/appLinks.jsx': ['shownAddress(host, ports)'],
    '../components/routerPorts.jsx': ['shownAddress(web.route.Host, ports', 'portAddress(mapping, host)'],
    '../pages/config/internetAccess.jsx': ['hostWithPort(hostname, publicPort'],
  };
  for (const [file, texts] of Object.entries(uses)) {
    const source = read(file);
    for (const text of texts) assert.ok(source.includes(text), file + ' no longer contains ' + text);
  }
  // what is stored as a route's host never carries the external port
  const suggestions = read('../components/hostnameSuggestions.jsx');
  assert.match(suggestions, /export const externalAddress = \(name, domain\) => hostLabel\(name\) \+ '\.' \+ domain;/);
  assert.ok(read('../components/appLinks.jsx').includes('Host: externalAddress(base.Name, row.domain),'));
  // the server builds its links with the same rule
  const reach = read('../../../src/reach/state.go');
  assert.ok(reach.includes('externalPort = mapping.ExternalPort'));
});

const mapping = (extra = {}) => ({
  name: 'jellyfin 8096/tcp', kind: 'app', container: 'jellyfin', containerPort: 8096, protocol: 'TCP',
  internalPort: 8096, externalPort: 8096, wantedPort: 8096, active: true, state: 'active', ...extra,
});

test('the address of a forwarded port', () => {
  // not a web page: text to copy, never a link
  assert.deepEqual(portAddress(mapping(), '203.0.113.7'), { address: '203.0.113.7:8096', href: '', known: true });
  assert.deepEqual(portAddress(mapping({ externalPort: 8097 }), 'jellyfin.example.org'), { address: 'jellyfin.example.org:8097', href: '', known: true });
  // a web page, as the admin said
  assert.equal(portAddress(mapping({ externalPort: 8097, scheme: 'http' }), 'jellyfin.example.org').href, 'http://jellyfin.example.org:8097');
  assert.equal(portAddress(mapping({ externalPort: 8443, scheme: 'https' }), '203.0.113.7').href, 'https://203.0.113.7:8443');
  // only http and https are ever a link
  assert.equal(portAddress(mapping({ scheme: 'javascript' }), 'x.example.org').href, '');
  // no name and no public address known
  assert.deepEqual(portAddress(mapping(), ''), { address: ':8096', href: '', known: false });
  assert.deepEqual(portAddress(null, 'x'), { address: '', href: '', known: false });
  assert.equal(portAddress(mapping(), '2001:db8::5').address, '[2001:db8::5]:8096');
});

test('the name a forwarded port is reached at', () => {
  const status = { externalIP: '203.0.113.7' };
  const routes = [{ UseHost: true, Host: 'jellyfin.local' }, { UseHost: true, Host: 'Jellyfin.Example.org:8443' }];
  assert.equal(publicHost({ routes, config: config(), status }), 'jellyfin.example.org');
  assert.equal(publicHost({ routes: [routes[0]], config: config(), status }), '203.0.113.7');
  assert.equal(publicHost({ routes: [], config: { HTTPConfig: { Hostname: 'cosmos.example.org' } }, status }), 'cosmos.example.org');
  assert.equal(publicHost({ routes: [], config: { HTTPConfig: { Hostname: 'server.local' } }, status }), '203.0.113.7');
  assert.equal(publicHost({ routes: [{ UseHost: false, Host: 'x.example.org' }], config: config(), status: {} }), '');
});

test('the note when the external port is not the wanted one', () => {
  assert.equal(portNote(mapping()), null);
  assert.equal(portNote(mapping({ active: false, externalPort: 8097 })), null);
  assert.equal(fill(portNote(mapping({ externalPort: 8097, code: 'moved' }))), '8097 on the router, because 8096 is taken.');
  assert.equal(fill(portNote(mapping({ externalPort: 8097, code: 'moved', holder: '192.168.1.99:8096 (Media box)' }))),
    '8097 on the router, because 8096 is used by 192.168.1.99:8096 (Media box).');
  assert.equal(fill(portNote(mapping({ externalPort: 81, wantedPort: 80, code: 'movedReserved' }))),
    '81 on the router, because Cosmos never asks a router for port 80.');
});

test('what a port\'s line says', () => {
  assert.match(fill(mappingSentence(mapping({ externalPort: 8097 }))), /^The router forwards port 8097 to port 8096 of this server .* Not tested from outside\.$/);
  assert.match(fill(mappingSentence(mapping({ active: false, state: 'waiting', code: 'stopped' }))), /while the container is stopped/);
  assert.match(fill(mappingSentence(mapping({ active: false, state: 'failed', code: 'conflict' }))), /in use on the router/);
  assert.match(fill(mappingSentence(mapping({ active: false, state: 'failed', code: 'conflict', holder: '192.168.1.9:80 (web)' }))), /port 8096 is used by 192\.168\.1\.9:80 \(web\)/);
  assert.match(fill(mappingSentence(mapping({ active: false, state: 'failed', code: 'refused', error: 'Refused (606).' }))), /The router refused: Refused \(606\)\./);
  assert.match(fill(mappingSentence(mapping({ active: false, state: 'failed', code: 'reserved', wantedPort: 443 }))), /port 443 is on the list/);
  // a code this page does not know still gets words, never the code itself
  assert.equal(mappingSentence(mapping({ active: false, state: 'failed', code: 'future' })).key, 'mgmt.router.state.failed');
  assert.equal(mappingSentence(mapping({ active: false, state: 'waiting', code: 'future' })).key, 'mgmt.router.state.waiting');
  assert.equal(mappingSentence(null).key, 'mgmt.router.state.waiting');
  // every code the server sets has a sentence
  const go = read('../../../src/upnp/apps.go') + read('../../../src/upnp/manager.go');
  const codes = [...go.matchAll(/\.Code(?:, [A-Za-z.]+)* = "([A-Za-z]+)"/g)].map((m) => m[1]);
  assert.ok(codes.length >= 8, 'found only ' + codes.length + ' codes in src/upnp');
  for (const code of new Set(codes)) {
    if (code === 'moved' || code === 'movedReserved') continue; // notes of an active mapping
    assert.ok(TEXT['state.' + code], 'no sentence for the code ' + code);
  }
});

test('every sentence is in the English translations, with the same text', () => {
  const en = JSON.parse(read('./locales/en/translation.json'));
  for (const [id, text] of Object.entries(TEXT)) {
    assert.equal(en['mgmt.router.' + id], text, id);
  }
});

test('finding the mappings of a container', () => {
  const status = { apps: [mapping(), mapping({ containerPort: 7359, protocol: 'UDP' }), mapping({ container: 'other' })] };
  assert.equal(portKey('/jellyfin', '8096', 'TCP'), 'jellyfin:8096/tcp');
  assert.equal(confirmText('jellyfin', 8096, 'tcp'), 'jellyfin:8096/tcp');
  assert.equal(findAppMapping(status, '/jellyfin', 8096, 'tcp'), status.apps[0]);
  assert.equal(findAppMapping(status, 'jellyfin', 7359, 'udp'), status.apps[1]);
  assert.equal(findAppMapping(status, 'jellyfin', 7359, 'tcp'), null);
  assert.equal(findAppMapping(null, 'jellyfin', 8096, 'tcp'), null);
  assert.equal(appMappingsOf(status, ['/jellyfin']).length, 2);
  assert.equal(appMappingsOf(status, ['jellyfin', 'other']).length, 3);
  assert.equal(appMappingsOf(null, ['jellyfin']).length, 0);
  assert.deepEqual(overviewRows({ mappings: [{ name: 'HTTPS' }], apps: [mapping()] }).map((m) => m.kind), ['cosmos', 'app']);
});

test('is the port a web interface', () => {
  const routes = [{ Name: 'jelly', Target: 'http://jellyfin:8096', UseHost: true, Host: 'jellyfin.local' }];
  assert.deepEqual(looksLikeWeb({ containerName: '/Jellyfin', containerPort: 8096, protocol: 'tcp', routes }), { web: true, scheme: 'http', route: routes[0], guessed: false });
  assert.equal(looksLikeWeb({ containerName: 'jellyfin', containerPort: 8920, protocol: 'tcp', routes }).guessed, true);
  assert.equal(looksLikeWeb({ containerName: 'jellyfin', containerPort: 8920, protocol: 'tcp', routes }).scheme, 'https');
  assert.equal(looksLikeWeb({ containerName: 'web', containerPort: 80, protocol: 'tcp', routes: [{ Target: 'http://web' }] }).guessed, false);
  assert.equal(looksLikeWeb({ containerName: 'torrent', containerPort: 51413, protocol: 'tcp', routes: [] }).web, false);
  assert.equal(looksLikeWeb({ containerName: 'jellyfin', containerPort: 8096, protocol: 'udp', routes }).web, false);
  // a route to another container, or to another port, says nothing
  assert.equal(looksLikeWeb({ containerName: 'jelly', containerPort: 51413, protocol: 'tcp', routes }).web, false);
  assert.ok(WEB_PORTS.includes(8096) && !WEB_PORTS.includes(51413) && !WEB_PORTS.includes(22));
});

test('the ports an installation publishes', () => {
  const service = {
    services: {
      jellyfin: { container_name: 'media', ports: ['8096:8096', '7359:7359/udp', '127.0.0.1:9000:9000', '0.0.0.0:8920:8920/tcp', '1900', '6000-6010:6000-6010', '[::1]:9001:9001'] },
      torrent: { ports: [{ published: 51413, target: 51413, protocol: 'udp' }, { published: '51413', target: 51413 }, { target: 80 }, '8096:8096'] },
      broken: { ports: 'x' },
      none: {},
      nothing: null,
    },
  };
  assert.deepEqual(installPorts(service), [
    { container: 'media', port: 8096, protocol: 'tcp', hostPort: 8096 },
    { container: 'media', port: 7359, protocol: 'udp', hostPort: 7359 },
    { container: 'media', port: 8920, protocol: 'tcp', hostPort: 8920 },
    { container: 'torrent', port: 51413, protocol: 'udp', hostPort: 51413 },
    { container: 'torrent', port: 51413, protocol: 'tcp', hostPort: 51413 },
    { container: 'torrent', port: 8096, protocol: 'tcp', hostPort: 8096 },
  ]);
  assert.deepEqual(installPorts(null), []);
  assert.deepEqual(installPorts({ services: [] }), []);
  assert.deepEqual(installPorts('x'), []);
});

test('the card says "forwarded" instead of "if forwarded" once the router forwards a port', () => {
  const dockerPorts = [{ IP: '0.0.0.0', PublicPort: 8096, PrivatePort: 8096, Type: 'tcp' }];
  assert.equal(summarize({ routeNames: [], results: [], ports: dockerPorts }).state, 'ports');
  const forwarded = summarize({ routeNames: [], results: [], ports: dockerPorts, mappings: [mapping({ externalPort: 8097 })] });
  assert.equal(forwarded.state, 'forwarded');
  assert.equal(forwarded.forwarded[0].externalPort, 8097);
  assert.equal(forwarded.url, '', 'a forwarded port is not a link of the card');
  // a choice the router did not honour changes nothing
  assert.equal(summarize({ routeNames: [], results: [], ports: dockerPorts, mappings: [mapping({ active: false, state: 'failed' })] }).state, 'ports');
  // an address through Cosmos still wins
  const results = [{ route: 'app', state: 'verified', url: 'https://app.example.org', checks: [] }];
  assert.equal(summarize({ routeNames: ['app'], results, ports: dockerPorts, mappings: [mapping()] }).state, 'verified');
  // a local-only URL does not hide the open port
  const local = [{ route: 'app', state: 'local', checks: [] }];
  assert.equal(summarize({ routeNames: ['app'], results: local, ports: dockerPorts, mappings: [mapping()] }).state, 'forwarded');
});

test('how long a lease runs', () => {
  assert.equal(leaseText(0), '');
  assert.equal(leaseText(90), '2 min');
  assert.equal(leaseText(3 * 3600), '3 h');
  assert.equal(leaseText(604800), '7 d');
});

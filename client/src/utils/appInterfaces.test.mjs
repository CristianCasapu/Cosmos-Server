import test from 'node:test';
import assert from 'node:assert/strict';
import {
  appTiles, appTile, containerApp, appInterfaces, chooseInterface, groupApps, hostKind, parseHost, reachFrom, routeURL, safeURL,
  visitorPlace, interfaceOfRoute, readPreference, writePreference, PREFER_KEY, mergeDashboard, inspectPorts, menuOrder, urlReach,
} from './appInterfaces.js';

const app = (name, host, target, more = {}) => ({ Name: name, UseHost: !!host, Host: host, Target: target, Mode: 'SERVAPP', ForkRole: 'user', ...more });

// The owner's shape (with documentation names): two-URL apps, a console with
// a local URL only, a database without a web page, an S3 app with an API URL
// and a console URL, a torrent app with a web port and a peer port.
const owner = [
  app('jellyfin', 'jellyfin.local', 'http://jellyfin:8096', { ForkAdminPath: '/web/#/dashboard', ForkHomeHosts: ['jellyfin.home.example.org'] }),
  app('jellyfin-example.org', 'jellyfin.example.org', 'http://jellyfin:8096', { ForkAdminPath: '/web/#/dashboard', AuthEnabled: true }),
  app('radarr', 'radarr.local', 'http://radarr:7878', { ForkRole: 'admin' }),
  app('radarr-example.org', 'radarr.example.org', 'http://radarr:7878', { ForkRole: 'admin' }),
  app('rustfs', 's3.example.org', 'http://rustfs:9000', { ForkRole: 'api', ForkPorts: [{ private: 9000, public: 9000, protocol: 'tcp' }, { private: 9001, public: 9001, protocol: 'tcp', ip: '192.168.1.10' }], ForkImage: 'rustfs/rustfs' }),
  app('rustfs-console', 'rustfs-console.local', 'http://rustfs:9001', { ForkRole: 'admin' }),
  app('qbittorrent', 'qbittorrent.local', 'http://qbittorrent:8080', { ForkRole: 'admin', ForkPorts: [{ private: 8080, public: 8080, protocol: 'tcp' }, { private: 6881, public: 6881, protocol: 'tcp' }, { private: 6881, public: 6881, protocol: 'udp' }], ForkImage: 'linuxserver/qbittorrent' }),
  app('qbittorrent-example.org', 'qbittorrent.example.org', 'http://qbittorrent:8080', { ForkRole: 'admin' }),
];

const away = { protocol: 'https:', hostname: 'example.org', port: '2053', origin: 'https://example.org:2053' };
const homeLocal = { protocol: 'https:', hostname: 'server.local', port: '2053', origin: 'https://server.local:2053' };
const homePublic = away;
const vpnPage = { protocol: 'https:', hostname: '100.101.102.103', port: '2053', origin: 'https://100.101.102.103:2053' };
const AWAY = { where: 'away', sure: true };
const HOME = { where: 'home', sure: true };
const HOME_GUESS = { where: 'home', sure: false };
const VPN = { where: 'vpn', sure: true };

const tile = (tiles, name) => tiles.find((t) => t.name === name);

test('the reported case: from away, the console with only a home address is "only at home", not a dead link', () => {
  const tiles = appTiles(owner, { place: AWAY, location: away, isAdmin: true });
  const rustfs = tile(tiles, 'rustfs');
  assert.ok(rustfs, 'the S3 URL and the console are one app');
  assert.equal(rustfs.routes.length, 2);
  assert.equal(rustfs.state, 'home-only');
  assert.equal(rustfs.link, '', 'the tile does not open an address that does not work from here');
  assert.ok(rustfs.menu);
  const console = rustfs.interfaces.find((i) => i.route === 'rustfs-console' && !i.direct);
  assert.equal(console.reach, 'home-only');
  assert.equal(console.url, 'https://rustfs-console.local:2053');
  const s3 = rustfs.interfaces.find((i) => i.role === 'api');
  assert.equal(s3.reach, 'opens', 'the API is listed and opens, but the tile never opens it while the app has a page');
});

test('the same console opens at home', () => {
  const tiles = appTiles(owner, { place: HOME, location: homeLocal, isAdmin: true });
  const rustfs = tile(tiles, 'rustfs');
  assert.equal(rustfs.state, 'opens');
  assert.equal(rustfs.link, 'https://rustfs-console.local:2053');
});

test('the same tile opens the local address at home and the public one from away', () => {
  assert.equal(tile(appTiles(owner, { place: HOME, location: homeLocal, isAdmin: true }), 'radarr').link, 'https://radarr.local:2053');
  assert.equal(tile(appTiles(owner, { place: AWAY, location: away, isAdmin: true }), 'radarr').link, 'https://radarr.example.org:2053');
});

test('administrators get the administration first, other users the page for users', () => {
  const admin = tile(appTiles(owner, { place: AWAY, location: away, isAdmin: true }), 'jellyfin');
  assert.equal(admin.link, 'https://jellyfin.example.org:2053/web/#/dashboard');
  assert.equal(admin.chosen.role, 'admin');
  assert.ok(admin.chosen.auth, 'the mark says Cosmos\' sign-in protects it');
  const user = tile(appTiles(owner, { place: AWAY, location: away, isAdmin: false }), 'jellyfin');
  assert.equal(user.link, 'https://jellyfin.example.org:2053');
  assert.equal(user.chosen.role, 'user');
});

test('a phone at home with the panel under the public name (probably at home): the public address opens, the home ones are offered', () => {
  const t = tile(appTiles(owner, { place: HOME_GUESS, location: homePublic, isAdmin: true }), 'radarr');
  assert.equal(t.link, 'https://radarr.example.org:2053');
  assert.equal(t.interfaces.find((i) => i.host === 'radarr.local').reach, 'maybe');
  const console = tile(appTiles(owner, { place: HOME_GUESS, location: homePublic, isAdmin: true }), 'rustfs');
  assert.equal(console.state, 'home-only', 'a console with only home addresses says so instead of guessing');
});

test('at home, surely, with the panel under the public name: home addresses first, the name for use at home before .local; the preference turns it', () => {
  const t = tile(appTiles(owner, { place: HOME, location: homePublic, isAdmin: false }), 'jellyfin');
  assert.equal(t.link, 'https://jellyfin.home.example.org:2053');
  const internet = tile(appTiles(owner, { place: HOME, location: homePublic, isAdmin: false, prefer: 'internet' }), 'jellyfin');
  assert.equal(internet.link, 'https://jellyfin.example.org:2053');
  const onLocal = tile(appTiles(owner, { place: HOME, location: homeLocal, isAdmin: false, prefer: 'internet' }), 'jellyfin');
  assert.equal(onLocal.link, 'https://jellyfin.local:2053', 'a page opened by a .local name proves the device resolves them');
});

test('through a VPN: the public address; .local names never; names for use at home only maybe', () => {
  const tiles = appTiles(owner, { place: VPN, location: vpnPage, isAdmin: true });
  assert.equal(tile(tiles, 'radarr').link, 'https://radarr.example.org:2053');
  const jf = tile(tiles, 'jellyfin');
  assert.equal(jf.interfaces.find((i) => i.host === 'jellyfin.local').reach, 'home-only');
  assert.equal(jf.interfaces.find((i) => i.host === 'jellyfin.home.example.org').reach, 'maybe');
  assert.equal(tile(tiles, 'rustfs').state, 'home-only');
});

test('a torrent app: its web port opens at home, its peer port is copy-only, for administrators only', () => {
  const admin = tile(appTiles(owner, { place: HOME, location: homeLocal, isAdmin: true }), 'qbittorrent');
  const peers = admin.interfaces.filter((i) => !i.web);
  // tcp and udp of the same port are one address to copy
  assert.deepEqual(peers.map((i) => [i.copy, i.protocol, i.label]), [['server.local:6881', 'tcp', 'BitTorrent']]);
  assert.ok(peers.every((i) => !i.url), 'an endpoint has no link');
  assert.equal(peers[0].copy, 'server.local:6881');
  assert.ok(admin.interfaces.some((i) => i.direct && i.url === 'http://server.local:8080'), 'the web port published on the server');
  assert.equal(admin.link, 'https://qbittorrent.local:2053', 'the URL before the direct port');
  const user = tile(appTiles(owner, { place: HOME, location: homeLocal, isAdmin: false }), 'qbittorrent');
  assert.ok(user.interfaces.every((i) => i.web && !i.direct), 'a plain user gets no published port');
});

test('a database without a web page: the connection string has placeholders, never a password', () => {
  const db = appTile(containerApp('/postgres', [], 'postgres:18', [{ PrivatePort: 5432, PublicPort: 5432, Type: 'tcp', IP: '192.168.1.10' }, { PrivatePort: 5432, PublicPort: 5432, Type: 'tcp', IP: '192.168.1.10' }]), { place: HOME, location: homeLocal, isAdmin: true, allRoutes: owner });
  assert.equal(db.name, 'postgres');
  assert.equal(db.state, 'none');
  assert.equal(db.link, '');
  assert.equal(db.interfaces.length, 1, 'the same port listed twice is one address');
  const end = db.interfaces[0];
  assert.equal(end.web, false);
  assert.equal(end.copy, '192.168.1.10:5432');
  assert.equal(end.connection, 'postgresql://USER:PASSWORD@192.168.1.10:5432/DATABASE');
  assert.ok(!/secret|pass[^W]/i.test(JSON.stringify(end)));
  const onAll = appTile(containerApp('pg', [], 'postgres:18', [{ PrivatePort: 5432, PublicPort: 15432, Type: 'tcp', IP: '0.0.0.0' }]), { place: AWAY, location: away, isAdmin: true, allRoutes: owner });
  assert.equal(onAll.interfaces[0].copy, 'jellyfin.local:15432', 'published on every address: a home name of the server');
  assert.equal(onAll.interfaces[0].reach, 'home-only');
  assert.equal(appTile(containerApp('pg', [], 'postgres:18', [{ PrivatePort: 5432, PublicPort: 5432, Type: 'tcp' }]), { place: HOME, location: homeLocal, isAdmin: false }).interfaces.length, 0, 'not for a plain user');
});

test('an app with exactly one address that opens keeps a plain tile', () => {
  const one = appTiles([app('wiki', 'wiki.example.org', 'http://wiki:3000')], { place: AWAY, location: away });
  assert.equal(one[0].menu, false);
  assert.equal(one[0].link, 'https://wiki.example.org:2053');
});

test('an app made only of an API opens it (it is all there is)', () => {
  const only = appTiles([app('s3', 's3.example.org', 'http://minio:9000', { ForkRole: 'api' })], { place: AWAY, location: away });
  assert.equal(only[0].link, 'https://s3.example.org:2053');
});

test('the role set by hand changes which link opens', () => {
  const routes = [
    app('svc-a', 'a.example.org', 'http://svc:1000', { ForkRole: 'user' }),
    app('svc-b', 'b.example.org', 'http://svc:2000', { ForkRole: 'user' }),
  ];
  assert.equal(appTiles(routes, { place: AWAY, location: away, isAdmin: true })[0].link, 'https://a.example.org:2053');
  routes[1] = { ...routes[1], ForkRole: 'admin', ForkRoleSource: 'set', ForkLabel: 'Back office' };
  const t = appTiles(routes, { place: AWAY, location: away, isAdmin: true })[0];
  assert.equal(t.link, 'https://b.example.org:2053');
  assert.equal(t.chosen.label, 'Back office');
});

test('without the server\'s answer, the page\'s address is the guess, and a home-only address is never opened from a public page', () => {
  assert.deepEqual(visitorPlace(null, homeLocal), { where: 'home', sure: false });
  assert.deepEqual(visitorPlace({}, away), { where: 'away', sure: false });
  assert.deepEqual(visitorPlace({ visitor: { where: 'vpn', sure: true } }, away), { where: 'vpn', sure: true });
  assert.deepEqual(visitorPlace({ visitor: { where: 'javascript:' } }, vpnPage), { where: 'vpn', sure: false });
  const t = tile(appTiles(owner, { location: away, isAdmin: true }), 'rustfs');
  assert.equal(t.link, '');
});

test('reach per kind and place', () => {
  assert.equal(reachFrom('public', AWAY), 'opens');
  assert.equal(reachFrom('panel', AWAY), 'opens');
  assert.equal(reachFrom('local', AWAY), 'home-only');
  assert.equal(reachFrom('home', AWAY), 'home-only');
  assert.equal(reachFrom('lan', VPN), 'maybe');
  assert.equal(reachFrom('local', VPN), 'home-only');
  assert.equal(reachFrom('vpn', VPN), 'opens');
  assert.equal(reachFrom('vpn', AWAY), 'vpn-only');
  assert.equal(reachFrom('local', HOME), 'opens');
  assert.equal(reachFrom('local', HOME_GUESS), 'maybe');
});

test('kinds of hosts', () => {
  assert.equal(hostKind('radarr.local'), 'local');
  assert.equal(hostKind('RADARR.LOCAL.'), 'local');
  assert.equal(hostKind('radarr'), 'local');
  assert.equal(hostKind('192.168.1.10:8080'), 'lan');
  assert.equal(hostKind('10.0.0.5'), 'lan');
  assert.equal(hostKind('100.64.1.1'), 'vpn');
  assert.equal(hostKind('100.128.1.1'), 'public');
  assert.equal(hostKind('[fd7a:115c:a1e0::1]'), 'vpn');
  assert.equal(hostKind('[fd00::1]:8080'), 'lan');
  assert.equal(hostKind('[2001:db8::1]'), 'public');
  assert.equal(hostKind('radarr.home.example.org', ['home.example.org']), 'home');
  assert.equal(hostKind('radarr.example.org', ['home.example.org']), 'public');
  assert.equal(hostKind('203.0.113.5'), 'public');
  assert.equal(hostKind('bad host'), '');
});

test('hostile hosts, paths and labels never make a link or markup', () => {
  const hostile = [
    app('a', 'javascript:alert(1)', 'http://a:1'),
    app('b', 'evil.example.org/@x', 'http://b:1'),
    app('c', 'x.example.org"><img src=x onerror=alert(1)>', 'http://c:1'),
    app('d', 'user:pass@d.example.org', 'http://d:1'),
    app('e', 'e.example.org', 'http://e:1', { UsePathPrefix: true, PathPrefix: '//evil.example.net/x' }),
    app('f', 'f.example.org', 'http://f:1', { UsePathPrefix: true, PathPrefix: '/"onmouseover=alert(1)' }),
    app('g', 'g.example.org', 'http://g:1', { UsePathPrefix: true, PathPrefix: 'javascript:alert(1)' }),
    app('h', 'h.example.org', 'http://h:1', { ForkAdminPath: 'javascript:alert(1)' }),
    app('i', 'i.example.org', 'http://i:1', { ForkAdminPath: '//evil.example.net' }),
    app('j', 'data:text/html,<script>alert(1)</script>', 'http://j:1'),
    app('k', 'k.example.org:99999', 'http://k:1'),
    app('l', 'l.example.org', 'http://l:1', { ForkRole: 'javascript:alert(1)', ForkLabel: '<b>bold</b>\u2028"x"' }),
  ];
  const tiles = appTiles(hostile, { place: AWAY, location: away, isAdmin: true });
  const links = tiles.flatMap((t) => t.interfaces.filter((i) => i.url).map((i) => i.url));
  links.forEach((url) => {
    assert.match(url, /^https:\/\/[a-z0-9.-]+(:\d+)?(\/[A-Za-z0-9._~%/@:+,;=!$&()*#-]*)?$/, url);
    assert.ok(!/javascript|data:|evil/.test(url), url);
  });
  assert.deepEqual(links.sort(), ['https://h.example.org:2053', 'https://i.example.org:2053', 'https://l.example.org:2053'].sort());
  const l = tile(tiles, 'l').interfaces[0];
  assert.equal(l.role, 'user', 'an unknown role is "a page for users"');
  assert.equal(l.label, 'bbold/bx', 'a label is text: the characters markup is made of are dropped');
  assert.equal(safeURL('javascript:alert(1)'), '');
  assert.equal(safeURL('data:text/html,x'), '');
  assert.equal(safeURL('https://u:p@x.example.org'), '');
  assert.equal(safeURL('https://x.example.org'), 'https://x.example.org');
  assert.equal(parseHost('a b'), null);
  assert.equal(routeURL({ UseHost: true, Host: 'x.example.org', UsePathPrefix: true, PathPrefix: '/a b' }, away), '');
});

test('hostile published ports and addresses are left out', () => {
  const r = app('x', 'x.local', 'http://x:80', { ForkPorts: [{ private: 1, public: 70000 }, { private: 2, public: 'a' }, { private: 5432, public: 5432, ip: '"><script>' }, { private: 5432, public: 5433, ip: '192.168.1.10' }], ForkImage: 'postgres' });
  const items = appTiles([r], { place: HOME, location: away, isAdmin: true })[0].interfaces.filter((i) => !i.web);
  assert.deepEqual(items.map((i) => i.copy), ['x.local:5432', '192.168.1.10:5433'], 'a strange address falls back to a name of the server');
});

test('a plain user gets no more than the URLs he sees', () => {
  const tiles = appTiles(owner, { place: HOME, location: homeLocal, isAdmin: false });
  tiles.forEach((t) => t.interfaces.forEach((i) => {
    assert.equal(i.web, true);
    assert.ok(!i.direct);
  }));
  assert.equal(tiles.reduce((n, t) => n + t.interfaces.length, 0), 12, 'the 8 URLs, the name for use at home and Jellyfin\'s dashboard on its three addresses');
});

test('URLs of one container are one app, named after the URL that carries its name', () => {
  const groups = groupApps(owner);
  assert.deepEqual(groups.map((g) => g.routes.length), [2, 2, 2, 2]);
  const tiles = appTiles([app('b-console', 'b.local', 'http://box:81'), app('box', 'box.example.org', 'http://box:80')], { place: AWAY, location: away });
  assert.equal(tiles.length, 1);
  assert.equal(tiles[0].name, 'box');
  const others = appTiles([
    { Name: 'files', UseHost: true, Host: 'files.local', Target: '', Mode: 'STATIC' },
    { Name: 'docs', UseHost: true, Host: 'docs.local', Target: '', Mode: 'STATIC' },
    { Name: 'r', UseHost: true, Host: 'r.local', Target: 'http://192.168.1.20:80', Mode: 'PROXY' },
    { Name: 's', UseHost: true, Host: 's.local', Target: 'http://192.168.1.20:80/', Mode: 'PROXY' },
  ], { place: HOME, location: homeLocal });
  assert.equal(others.length, 3);
  assert.deepEqual(appTiles(null, {}), []);
  assert.deepEqual(appTiles([null, undefined], {}), []);
});

test('an address without a host of its own opens the panel\'s origin with its path; ports follow the router when known', () => {
  const t = appTiles([{ Name: 'sub', UseHost: false, UsePathPrefix: true, PathPrefix: '/sub', Target: 'http://sub:1', Mode: 'PROXY' }], { place: AWAY, location: away });
  assert.equal(t[0].link, 'https://example.org:2053/sub');
  const ports = { https: 2053, http: 2052, externalHttps: 443, externalHttp: 80 };
  const r = appTiles(owner, { place: AWAY, location: away, isAdmin: true, ports });
  assert.equal(tile(r, 'radarr').link, 'https://radarr.example.org');
  const h = appTiles(owner, { place: HOME, location: homeLocal, isAdmin: true, ports });
  assert.equal(tile(h, 'radarr').link, 'https://radarr.local:2053');
  assert.equal(routeURL({ UseHost: true, Host: 'a.example.org' }, { protocol: 'https:', port: '' }), 'https://a.example.org');
});

test('a chip\'s own URL as an interface', () => {
  const i = interfaceOfRoute(owner[5], { place: AWAY, location: away });
  assert.equal(i.reach, 'home-only');
  assert.equal(interfaceOfRoute(owner[3], { place: AWAY, location: away }).reach, 'opens');
  assert.equal(interfaceOfRoute(null, {}), null);
});

test('the device preference is kept per browser, and a broken storage keeps the default', () => {
  const store = new Map();
  const storage = { getItem: (k) => store.get(k) || null, setItem: (k, v) => store.set(k, v) };
  assert.equal(readPreference(storage), 'home');
  writePreference(storage, 'internet');
  assert.equal(store.get(PREFER_KEY), 'internet');
  assert.equal(readPreference(storage), 'internet');
  writePreference(storage, '<x>');
  assert.equal(readPreference(storage), 'home');
  const broken = { getItem: () => { throw new Error('denied'); }, setItem: () => { throw new Error('denied'); } };
  assert.equal(readPreference(broken), 'home');
  writePreference(broken, 'internet');
  assert.equal(readPreference(null), 'home');
});

test('chooseInterface with nothing', () => {
  assert.equal(chooseInterface([], { place: AWAY }), null);
  assert.equal(chooseInterface(null, {}), null);
  assert.deepEqual(appInterfaces({ routes: [] }, {}), []);
});

test('URLs from the configuration get the dashboard\'s roles by name', () => {
  const merged = mergeDashboard([{ Name: 'a', Host: 'a.local', UseHost: true }, { Name: 'b' }, null], [{ Name: 'a', ForkRole: 'admin', ForkHomeHosts: ['a.home.example.org'], Host: 'evil.example.org' }, null]);
  assert.equal(merged.length, 2);
  assert.equal(merged[0].ForkRole, 'admin');
  assert.equal(merged[0].Host, 'a.local', 'only the fork\'s fields are taken');
  assert.deepEqual(merged[1], { Name: 'b' });
  assert.deepEqual(mergeDashboard(null, null), []);
});

test('published ports from a container\'s inspection', () => {
  assert.deepEqual(inspectPorts({ NetworkSettings: { Ports: { '9000/tcp': [{ HostIp: '0.0.0.0', HostPort: '9000' }, { HostIp: '::', HostPort: '9000' }], '9001/tcp': null, 'x/tcp': [{ HostPort: '1' }], '6881/udp': [{ HostIp: '', HostPort: '6881' }] } } }),
    [{ PrivatePort: 9000, PublicPort: 9000, Type: 'tcp', IP: '0.0.0.0' }, { PrivatePort: 9000, PublicPort: 9000, Type: 'tcp', IP: '::' }, { PrivatePort: 6881, PublicPort: 6881, Type: 'udp', IP: '' }]);
  assert.deepEqual(inspectPorts(null), []);
});

test('the menu: what the tile opens first, pages before the API, endpoints last', () => {
  const t = tile(appTiles(owner, { place: AWAY, location: away, isAdmin: true }), 'qbittorrent');
  const order = menuOrder(t, true).map((i) => (i.web ? i.role + ' ' + i.url : 'copy ' + i.copy));
  assert.equal(order[0], 'admin ' + t.link);
  assert.deepEqual(order.slice(-1), ['copy qbittorrent.local:6881']);
  const r = tile(appTiles(owner, { place: HOME, location: homeLocal, isAdmin: true }), 'rustfs');
  const rs = menuOrder(r, true);
  assert.equal(rs[0].url, 'https://rustfs-console.local:2053');
  assert.equal(rs[rs.length - 1].role, 'api');
  assert.deepEqual(menuOrder(null, true), []);
});

test('a server-built address: does it open from here', () => {
  assert.equal(urlReach('https://rustfs-console.local:2053', { place: AWAY }), 'home-only');
  assert.equal(urlReach('https://rustfs-console.local:2053', { place: HOME }), 'opens');
  assert.equal(urlReach('https://s3.example.org', { place: AWAY }), 'opens');
  assert.equal(urlReach('javascript:alert(1)', { place: AWAY }), '');
  assert.equal(urlReach('', { place: AWAY }), '');
});

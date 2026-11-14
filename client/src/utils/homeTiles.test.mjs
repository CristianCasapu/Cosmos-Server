import test from 'node:test';
import assert from 'node:assert/strict';
import { homeTiles } from './homeTiles.js';

const app = (name, host, target, more = {}) => ({ Name: name, UseHost: true, Host: host, Target: target, Mode: 'SERVAPP', ...more });
const routes = [
  app('radarr', 'radarr.local', 'http://radarr:7878'),
  app('radarr-example.org', 'radarr.example.org', 'http://radarr:7878'),
  app('jellyfin', 'jellyfin.local', 'http://jellyfin:8096'),
  app('jellyfin-example.org', 'jellyfin.example.org', 'http://jellyfin:8096/'),
  app('wiki', 'wiki.example.org', 'http://wiki:3000'),
];
const outside = { protocol: 'https:', hostname: 'example.org', port: '2053', origin: 'https://example.org:2053' };
const atHome = { protocol: 'https:', hostname: '192.168.1.10', port: '2053', origin: 'https://192.168.1.10:2053' };

test('one tile per app, named after the app, not one per address', () => {
  const tiles = homeTiles(routes, outside);
  assert.deepEqual(tiles.map((tile) => tile.name), ['radarr', 'jellyfin', 'wiki']);
  assert.equal(tiles[0].routes.length, 2);
});

test('from outside a tile opens the external address, with the port the panel is used on', () => {
  const tiles = homeTiles(routes, outside);
  assert.equal(tiles[0].link, 'https://radarr.example.org:2053');
  assert.equal(tiles[1].link, 'https://jellyfin.example.org:2053');
  assert.equal(tiles[0].route.Name, 'radarr-example.org');
});

test('at home a tile opens the local address', () => {
  const tiles = homeTiles(routes, atHome);
  assert.equal(tiles[0].link, 'https://radarr.local:2053');
  assert.equal(tiles[2].link, 'https://wiki.example.org:2053', 'an app without a local address opens the one it has');
  assert.equal(homeTiles(routes, { ...atHome, hostname: 'server.local' })[1].link, 'https://jellyfin.local:2053');
});

test('on the standard port no port is added', () => {
  assert.equal(homeTiles(routes, { protocol: 'https:', hostname: 'example.org', port: '', origin: 'https://example.org' })[0].link,
    'https://radarr.example.org');
});

test('from outside, the address under the name the panel is used under wins', () => {
  const two = [
    app('notes', 'notes.local', 'http://notes:80'),
    app('notes-example.net', 'notes.example.net', 'http://notes:80'),
    app('notes-example.org', 'notes.example.org', 'http://notes:80'),
  ];
  assert.equal(homeTiles(two, outside)[0].link, 'https://notes.example.org:2053');
  assert.equal(homeTiles(two, { ...outside, hostname: 'panel.example.net' })[0].link, 'https://notes.example.net:2053');
});

test('a skipped address is left out before the apps are formed', () => {
  const hidden = [app('radarr', 'radarr.local', 'http://radarr:7878', { HideFromDashboard: true }), routes[1]];
  const tiles = homeTiles(hidden, atHome, (route) => route.HideFromDashboard);
  assert.equal(tiles.length, 1);
  assert.equal(tiles[0].link, 'https://radarr.example.org:2053');
  assert.equal(tiles[0].name, 'radarr-example.org');
  assert.equal(homeTiles(routes, outside, () => true).length, 0);
});

test('addresses that lead to different places stay apart, and so do those without a target', () => {
  const mixed = [
    app('a', 'a.local', 'http://a:80'),
    app('b', 'b.local', 'http://a:81'),
    { Name: 'files', UseHost: true, Host: 'files.local', Target: '', Mode: 'STATIC' },
    { Name: 'docs', UseHost: true, Host: 'docs.local', Target: '', Mode: 'STATIC' },
    { Name: 'same target, another mode', UseHost: true, Host: 'r.local', Target: 'http://a:80', Mode: 'REDIRECT' },
  ];
  assert.equal(homeTiles(mixed, atHome).length, 5);
  assert.deepEqual(homeTiles(null, atHome), []);
  assert.deepEqual(homeTiles([null, undefined], atHome), []);
});

test('an address without a host of its own opens the panel\'s origin with its path', () => {
  const tiles = homeTiles([{ Name: 'sub', UseHost: false, UsePathPrefix: true, PathPrefix: '/sub', Target: 'http://sub:1', Mode: 'PROXY' }], outside);
  assert.equal(tiles[0].link, 'https://example.org:2053/sub');
});

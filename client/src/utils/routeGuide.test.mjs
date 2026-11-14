// Fork addition: tests of routeGuide.js. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  targetParts, buildTarget, containerPorts, proposePort, accessOf, accessFields,
  guideFits, describeAddress, newAppRoute, routesOfContainer, planLocalAddresses, freeName, tidyAddress,
} from './routeGuide.js';

// the list the form uses
const defaults = JSON.parse(readFileSync(new URL('../pages/servapps/defaultport.json', import.meta.url), 'utf8'));

// what API.docker.list() gives, cut down
const container = (name, image, ports, more = {}) => ({
  Names: ['/' + name], Image: image, State: 'running',
  Ports: ports.map((port) => (typeof port === 'number' ? { PrivatePort: port, Type: 'tcp' } : port)),
  NetworkSettings: { Networks: { bridge: {} } },
  ...more,
});

test('a target is taken apart and put together again', () => {
  assert.deepEqual(targetParts('http://radarr:7878'), { scheme: 'http', host: 'radarr', port: '7878', rest: '' });
  assert.deepEqual(targetParts('https://my-app:8443'), { scheme: 'https', host: 'my-app', port: '8443', rest: '' });
  assert.deepEqual(targetParts('http://radarr:'), { scheme: 'http', host: 'radarr', port: '', rest: '' });
  assert.deepEqual(targetParts('radarr:7878'), { scheme: '', host: 'radarr', port: '7878', rest: '' });
  assert.deepEqual(targetParts(''), { scheme: '', host: '', port: '', rest: '' });
  assert.equal(targetParts('http://192.168.1.5:8080/path').rest, '/path');
  assert.equal(buildTarget({ scheme: 'http', host: 'radarr', port: 7878 }), 'http://radarr:7878');
  assert.equal(buildTarget({ scheme: 'https', host: 'radarr', port: '' }), 'https://radarr:');
  assert.equal(buildTarget(targetParts('http://sonarr:8989')), 'http://sonarr:8989');
});

test('the ports of a container: TCP only, each once, smallest first', () => {
  const jellyfin = container('jellyfin', 'jellyfin/jellyfin', [
    { IP: '0.0.0.0', PrivatePort: 8096, PublicPort: 8096, Type: 'tcp' },
    { IP: '::', PrivatePort: 8096, PublicPort: 8096, Type: 'tcp' },
    { PrivatePort: 8920, Type: 'tcp' },
    { PrivatePort: 1900, Type: 'udp' },
    { PrivatePort: 0, Type: 'tcp' },
  ]);
  assert.deepEqual(containerPorts(jellyfin), [8096, 8920]);
  assert.deepEqual(containerPorts({}), []);
  assert.deepEqual(containerPorts(null), []);
});

test('the proposed port and where the proposal comes from', () => {
  // the app's usual port, among several
  assert.deepEqual(proposePort('linuxserver/radarr:latest', [80, 7878], defaults), { port: 7878, source: 'usual' });
  assert.deepEqual(proposePort('lscr.io/linuxserver/sonarr:4.0', [8989], defaults), { port: 8989, source: 'usual' });
  assert.deepEqual(proposePort('jellyfin/jellyfin', [8096, 8920], defaults), { port: 8096, source: 'usual' });
  // an image the list does not know: its only port
  assert.deepEqual(proposePort('ghcr.io/someone/thing:1', [5055], defaults), { port: 5055, source: 'only' });
  // several ports, none known: a common web port wins over the smallest
  assert.deepEqual(proposePort('someone/thing', [22, 3000, 9100], defaults), { port: 3000, source: 'common' });
  assert.deepEqual(proposePort('someone/thing', [22, 8123, 9100], defaults), { port: 8123, source: 'common' });
  assert.deepEqual(proposePort('someone/thing', [22, 9100], defaults), { port: 22, source: 'first' });
  // upstream's "80" also matched 1800 and 8080, and "8*" matched every port
  assert.deepEqual(proposePort('someone/thing', [1800, 9000], defaults), { port: 1800, source: 'first' });
  // the usual port is known but the container does not have it: what it has counts
  assert.deepEqual(proposePort('linuxserver/radarr', [9999], defaults), { port: 9999, source: 'only' });
  // no port at all
  assert.deepEqual(proposePort('linuxserver/radarr', [], defaults), { port: 7878, source: 'usualOnly' });
  assert.deepEqual(proposePort('busybox', [], defaults), { port: null, source: 'none' });
  assert.deepEqual(proposePort('', undefined, undefined), { port: null, source: 'none' });
});

test('an image is recognised by its name, not by its registry or owner', () => {
  // "git(lab|server)" must not match the owner "gitea" of another image, "plex" not a registry name
  assert.equal(proposePort('plexinc/pms-docker', [32400, 8324], defaults).source, 'usual');
  assert.equal(proposePort('registry.example.org/team/grafana:10', [3000, 9000], defaults).source, 'usual');
});

test('who may open an address is one choice', () => {
  assert.equal(accessOf({ AuthEnabled: false }), 'anyone');
  assert.equal(accessOf({}), 'anyone');
  assert.equal(accessOf({ AuthEnabled: true }), 'users');
  assert.equal(accessOf({ AuthEnabled: true, AdminOnly: true }), 'admins');
  assert.deepEqual(accessFields('anyone'), { AuthEnabled: false, AdminOnly: false });
  assert.deepEqual(accessFields('users'), { AuthEnabled: true, AdminOnly: false });
  assert.deepEqual(accessFields('admins'), { AuthEnabled: true, AdminOnly: true });
  for (const choice of ['anyone', 'users', 'admins']) {
    assert.equal(accessOf(accessFields(choice)), choice);
  }
});

test('the simple view shows an address that leads to an app or a web address, nothing else', () => {
  const base = { Mode: 'SERVAPP', Target: 'http://radarr:7878', UseHost: true, Host: 'radarr.local' };
  assert.deepEqual(guideFits(base), { fits: true, why: [] });
  assert.deepEqual(guideFits({ ...base, Mode: 'PROXY', Target: 'https://192.168.1.5:8443' }), { fits: true, why: [] });
  assert.deepEqual(guideFits({ ...base, Target: 'http://radarr:' }).fits, true, 'a port still to be chosen');
  assert.deepEqual(guideFits({ ...base, Mode: 'STATIC', Target: '/srv/www' }).why, ['mode']);
  assert.deepEqual(guideFits({ ...base, Mode: 'REDIRECT', Target: 'https://example.org' }).why, ['mode']);
  assert.deepEqual(guideFits({ ...base, Target: 'tcp://radarr:7878' }).why, ['socket']);
  assert.deepEqual(guideFits({ ...base, Target: 'radarr:7878' }).why, ['socket']);
  assert.deepEqual(guideFits({ ...base, UsePathPrefix: true, PathPrefix: '/radarr' }).why, ['path']);
  assert.deepEqual(guideFits({ ...base, UseHost: false, UsePathPrefix: true, PathPrefix: '/x' }).why, ['path', 'noHost']);
  assert.deepEqual(guideFits({ ...base, Tunnel: '_ANY_' }).why, ['tunnel']);
  // "administrators only" without a sign-in is a state the three choices do not have
  assert.deepEqual(guideFits({ ...base, AuthEnabled: false, AdminOnly: true }).why, ['access']);
});

test('a new URL for an app carries upstream\'s defaults and an honest description', () => {
  const route = newAppRoute({ name: 'radarr', host: 'radarr.local', port: 7878 });
  assert.equal(route.Target, 'http://radarr:7878');
  assert.equal(route.Mode, 'SERVAPP');
  assert.equal(route.UseHost, true);
  assert.equal(route.Host, 'radarr.local');
  assert.equal(route.Description, 'radarr, on the local network');
  assert.equal(route.AuthEnabled, false);
  assert.equal(route.AdminOnly, false);
  assert.equal(route.AcceptInsecureHTTPSTarget, false);
  // what the dialog has always sent for the settings it does not ask
  assert.equal(route.Timeout, 14400000);
  assert.equal(route.ThrottlePerMinute, 10000);
  assert.equal(route.BlockCommonBots, true);
  assert.deepEqual(route.SmartShield, { Enabled: true });
  assert.equal(describeAddress('radarr', 'radarr.example.org'), 'radarr, from the internet');
  assert.equal(describeAddress('radarr', '192.168.1.10:7200'), 'radarr, on the local network');
  // an app's own HTTPS port: https, and its certificate is not checked
  const secure = newAppRoute({ name: 'unifi', host: 'unifi.local', port: 8443, access: 'admins' });
  assert.equal(secure.Target, 'https://unifi:8443');
  assert.equal(secure.AcceptInsecureHTTPSTarget, true);
  assert.equal(secure.AuthEnabled, true);
  assert.equal(secure.AdminOnly, true);
});

test('the routes of a container are found whatever the scheme and port, and not those of a longer name', () => {
  const routes = [
    { Name: 'a', Mode: 'SERVAPP', Target: 'http://radarr:7878' },
    { Name: 'b', Mode: 'SERVAPP', Target: 'https://Radarr:443' },
    { Name: 'c', Mode: 'SERVAPP', Target: 'http://radarr-4k:7878' },
    { Name: 'd', Mode: 'PROXY', Target: 'http://radarr:7878' },
    { Name: 'e', Mode: 'SERVAPP', Target: 'http://my.app:80' },
  ];
  assert.deepEqual(routesOfContainer(routes, '/radarr').map((route) => route.Name), ['a', 'b']);
  assert.deepEqual(routesOfContainer(routes, 'my.app').map((route) => route.Name), ['e']);
  assert.deepEqual(routesOfContainer(routes, 'myXapp'), [], 'a dot in a name is a dot');
  assert.deepEqual(routesOfContainer(undefined, 'radarr'), []);
});

test('the plan for the apps without a URL', () => {
  const containers = [
    container('radarr', 'linuxserver/radarr', [7878]),
    container('sonarr', 'linuxserver/sonarr', [8989]),
    container('web1', 'nginx:alpine', [80]), // has a URL already
    container('db1', 'postgres:16', []), // nothing to point at
    container('old', 'someone/old', [8080], { State: 'exited' }),
    container('pihole', 'pihole/pihole', [80], { NetworkSettings: { Networks: { host: {} } } }),
    container('plain', 'linuxserver/bazarr', []), // usual port known, but the image declares none
    container('cosmos-server', 'azukaar/cosmos-server', [80, 443]),
  ];
  const routes = [{ Name: 'web1', Mode: 'SERVAPP', Target: 'http://web1:80', UseHost: true, Host: 'web1.local' }];
  const seen = [];
  const plan = planLocalAddresses({
    containers, routes, defaults, skip: ['cosmos-server'],
    hostFor: (name, soFar) => { seen.push(soFar.length); return name + '.local'; },
  });
  assert.deepEqual(plan.map((entry) => [entry.name, entry.state, entry.host, entry.port, entry.source]), [
    ['radarr', 'ready', 'radarr.local', 7878, 'usual'],
    ['sonarr', 'ready', 'sonarr.local', 8989, 'usual'],
    ['db1', 'noPort', '', null, 'none'],
    ['old', 'stopped', '', null, 'none'],
    ['pihole', 'hostNetwork', '', null, 'none'],
    ['plain', 'noPort', '', 6767, 'usualOnly'],
  ]);
  assert.deepEqual(plan[0].route, newAppRoute({ name: 'radarr', host: 'radarr.local', port: 7878 }));
  assert.equal(plan[2].route, null);
  // each proposal is made knowing the ones before it (a free port is not handed out twice)
  assert.deepEqual(seen, [1, 2]);
});

test('an address or a name that is in use is not planned twice', () => {
  const containers = [
    container('radarr', 'linuxserver/radarr', [7878]),
    container('sonarr', 'linuxserver/sonarr', [8989]),
    container('emby', 'emby/embyserver', [8096]),
  ];
  const routes = [
    { Name: 'films', Mode: 'PROXY', Target: 'http://192.168.1.9:80', UseHost: true, Host: 'Radarr.local' },
    { Name: 'emby', Mode: 'PROXY', Target: 'http://192.168.1.9:81', UseHost: true, Host: 'other.local' },
  ];
  const plan = planLocalAddresses({ containers, routes, defaults, hostFor: (name) => (name === 'sonarr' ? '192.168.1.10:7200' : name + '.local') });
  assert.deepEqual(plan.map((entry) => [entry.name, entry.state]), [['radarr', 'taken'], ['sonarr', 'ready'], ['emby', 'taken']]);
  // the fallback when there is no free address
  const none = planLocalAddresses({ containers: [containers[1]], routes: [], defaults, hostFor: () => 'NO MORE PORT AVAILABLE. PLEASE CLEAN YOUR URLS!' });
  assert.equal(none[0].state, 'taken');
  assert.equal(none[0].route, null);
});

test('a second URL of an app gets a name of its own', () => {
  assert.equal(freeName('radarr', []), 'radarr');
  assert.equal(freeName('radarr', ['radarr']), 'radarr-2');
  assert.equal(freeName('radarr', ['radarr', 'radarr-2', 'sonarr']), 'radarr-3');
  assert.equal(freeName('radarr', undefined), 'radarr');
});

test('a pasted address is reduced to the name', () => {
  assert.equal(tidyAddress('https://Radarr.Example.org/'), 'radarr.example.org');
  assert.equal(tidyAddress('  radarr.local '), 'radarr.local');
  assert.equal(tidyAddress('http://192.168.1.10:7200/login?x=1'), '192.168.1.10:7200');
  assert.equal(tidyAddress('radarr.local'), 'radarr.local');
  assert.equal(tidyAddress(undefined), '');
});

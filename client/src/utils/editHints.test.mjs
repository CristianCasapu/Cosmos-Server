// Fork addition (plan 47, I2c): tests of editHints.js. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  hostIpChoice, hostIpOf, reachOf, REACH_TEXT, containerPortOptions, proposeHostPort, proposedHostIp, newPortRow,
  dnsOptions, dnsChoice, dnsProblem, networkModeChoice, networkModeNetworks, declaredMissing, proposeDeclared,
  sourcePlaces, folderFinding, dataFoldersOwner, userOptions, userChoice, puidMismatch, withPuid, userOnLinuxserver,
  memoryOptions, cpuOptions, limitChoice, RESTART, envSuggestions, deviceOptions, deviceRow,
} from './editHints.js';

const TB = 1e12;
const GIB = 1024 ** 3;
const lan = [{ interface: 'eth1', address: '192.168.1.10', defaultRoute: false }, { interface: 'eth0', address: '192.168.1.11', defaultRoute: true }];
// a linuxserver.io app on the owner's kind of server
const hints = {
  container: 'radarr',
  image: { user: '', volumes: ['/config'], ports: ['7878/tcp', '9898/udp'], env: ['PATH'] },
  recipe: { name: 'Radarr', ports: [{ port: 7878, protocol: 'tcp', role: 'web' }], env: ['PUID', 'PGID', 'TZ', 'UMASK'], volumes: ['/config', '/movies'], memory: '256 MB to 1 GB', memoryLimit: GIB },
  knownApp: { name: 'Radarr', port: 7878 },
  linuxserver: true,
  runsAs: { uid: 1000, gid: 1000, known: true, source: 'puid', text: '1000:1000' },
  folders: [
    { source: '/storage/cosmos/radarr/config', target: '/config', state: 'ok', owner: '1000:1000', mode: '0755', writable: 'yes' },
    { source: '/archive/movies', target: '/movies', state: 'ok', owner: '1000:1000', writable: 'yes' },
    { source: '/etc/localtime', target: '/etc/localtime', state: 'outside', readOnly: true },
  ],
  anonymous: [],
  dataPath: '/storage/cosmos',
  places: [
    { path: '/storage/cosmos', kind: 'data-path', osDisk: false, disk: '/storage', free: 5.4 * TB },
    { path: '/archive', kind: 'disk', osDisk: false, disk: '/archive', free: 8.6 * TB },
    { path: '/storage', kind: 'disk', osDisk: false, disk: '/storage', free: 5.4 * TB },
    { path: '/', kind: 'system', osDisk: true, disk: '/', free: 0.8 * TB },
  ],
  server: { memory: 64 * GIB, cpus: 16 },
  lan,
  dns: { servers: ['192.168.1.1'], source: 'resolv.conf', router: '192.168.1.1' },
  devices: [
    { path: '/dev/dri/renderD128', node: '/dev/dri/renderD128', kind: 'render', name: 'Intel graphics (i915)' },
    { path: '/dev/serial/by-id/usb-dongle', node: '/dev/ttyUSB0', kind: 'serial', name: 'Silicon Labs CP2102N' },
  ],
  freePorts: { '7878/tcp': 7879, '9898/udp': 9898 },
  portsListened: true,
};

test('the host address is a choice, and says who can reach the port', () => {
  assert.equal(hostIpChoice('', lan), 'all');
  assert.equal(hostIpChoice('0.0.0.0', lan), 'all');
  assert.equal(hostIpChoice('::', lan), 'all');
  assert.equal(hostIpChoice('127.0.0.1', lan), 'local');
  assert.equal(hostIpChoice('192.168.1.10', lan), 'lan:192.168.1.10');
  assert.equal(hostIpChoice('192.0.2.7', lan), 'other');
  assert.equal(hostIpOf('lan:192.168.1.10'), '192.168.1.10');
  assert.equal(hostIpOf('all', 'x'), '');
  assert.equal(hostIpOf('local'), '127.0.0.1');
  assert.equal(hostIpOf('other', ' 192.0.2.7 '), '192.0.2.7');
  assert.equal(reachOf('', '8080', lan), 'everyone');
  assert.equal(reachOf('192.168.1.10', '8080', lan), 'home');
  assert.equal(reachOf('10.0.0.5', '8080', lan), 'home');
  assert.equal(reachOf('127.0.0.1', '8080', lan), 'server');
  assert.equal(reachOf('::1', '8080', lan), 'server');
  assert.equal(reachOf('203.0.113.4', '8080', lan), 'address');
  assert.equal(reachOf('', '', lan), 'none');
  Object.values(REACH_TEXT).forEach((text) => assert.ok(text.length > 20));
  assert.match(REACH_TEXT.everyone, /internet/);
  // the default route's address first
  assert.equal(proposedHostIp(lan), '192.168.1.11');
  assert.equal(proposedHostIp([]), '');
});

test('container ports come from the recipe, the known app and the image, each once', () => {
  const options = containerPortOptions(hints);
  assert.deepEqual(options.map((o) => `${o.port}/${o.protocol}:${o.source}`), ['7878/tcp:recipe', '9898/udp:image']);
  assert.deepEqual(containerPortOptions(null), []);
  assert.deepEqual(containerPortOptions({ image: { ports: ['80/tcp', 'x/tcp', '70000/tcp'] } }).map((o) => o.port), [80]);
});

test('the host port is the free one, moved past the form and the holders', () => {
  assert.deepEqual(proposeHostPort(hints, 9898, 'udp'), { port: 9898, reason: 'free' });
  assert.deepEqual(proposeHostPort(hints, 7878, 'tcp'), { port: 7879, reason: 'moved' });
  // another row of the form has 7879 already
  assert.deepEqual(proposeHostPort(hints, 7878, 'tcp', [{ hostPort: '7879', protocol: 'tcp' }]), { port: 7880, reason: 'moved' });
  // not tried on the server: the same number, said as such
  assert.deepEqual(proposeHostPort({}, 8096, 'tcp'), { port: 8096, reason: 'same' });
  // a holder the client knows (a stopped container's binding)
  assert.deepEqual(proposeHostPort({}, 8096, 'tcp', [], [{ port: 8096, protocol: 'tcp' }]), { port: 8097, reason: 'moved' });
  assert.equal(proposeHostPort({}, 'x', 'tcp'), null);
});

test('a new row proposes the first port not yet published, on the home network', () => {
  const row = newPortRow(hints, [{ port: '7878', protocol: 'tcp', hostPort: '7878' }]);
  assert.equal(row.port, '9898');
  assert.equal(row.protocol, 'udp');
  assert.equal(row.hostPort, '9898');
  assert.equal(row.hostIp, '192.168.1.11');
  assert.equal(row.proposed.source, 'image');
  const first = newPortRow(hints, []);
  assert.equal(first.port, '7878');
  assert.equal(first.hostPort, '7879');
  assert.equal(first.proposed.source, 'recipe');
  // everything published: empty fields, still on the home network
  const none = newPortRow(hints, [{ port: '7878', protocol: 'tcp' }, { port: '9898', protocol: 'udp' }]);
  assert.equal(none.port, '');
  assert.equal(none.hostIp, '192.168.1.11');
  assert.equal(newPortRow(null, []).hostIp, '');
});

test('DNS is a choice among Docker\'s default, the router and the resolver', () => {
  assert.deepEqual(dnsOptions(hints).map((o) => o.value), ['default', 'router', 'other']);
  const two = { dns: { servers: ['192.0.2.53', '198.51.100.53'], source: 'systemd-resolved', router: '192.168.1.1' } };
  assert.deepEqual(dnsOptions(two).map((o) => o.value), ['default', 'router', 'resolver', 'other']);
  assert.equal(dnsChoice('', two), 'default');
  assert.equal(dnsChoice('192.168.1.1', two), 'router');
  assert.equal(dnsChoice('192.0.2.53, 198.51.100.53', two), 'resolver');
  assert.equal(dnsChoice('1.1.1.1', two), 'other');
  assert.deepEqual(dnsOptions({}).map((o) => o.value), ['default', 'other']);
  assert.equal(dnsProblem('192.168.1.1, 2001:db8::1'), null);
  assert.equal(dnsProblem('dns.example.org'), 'notIp');
  assert.equal(dnsProblem('300.1.1.1'), 'notIp');
  assert.equal(dnsProblem(Array(9).fill('192.0.2.1').join(',')), 'tooMany');
});

test('network modes are choices', () => {
  const networks = [{ Name: 'bridge' }, { Name: 'host' }, { Name: 'none' }, { Name: 'cosmos-network-x' }, { Name: 'apps' }];
  assert.equal(networkModeChoice('', networks), 'default');
  assert.equal(networkModeChoice('bridge', networks), 'default');
  assert.equal(networkModeChoice('default', networks), 'default');
  assert.equal(networkModeChoice('host', networks), 'host');
  assert.equal(networkModeChoice('none', networks), 'none');
  assert.equal(networkModeChoice('container:gluetun', networks), 'container');
  assert.equal(networkModeChoice('apps', networks), 'net:apps');
  assert.equal(networkModeChoice('gone-network', networks), 'other');
  assert.deepEqual(networkModeNetworks(networks), ['apps', 'cosmos-network-x']);
});

test('a declared folder that is not mounted is proposed in the data place', () => {
  const ls = { ...hints, image: { ...hints.image, volumes: ['/config', '/cache/'] }, anonymous: ['/cache'] };
  assert.deepEqual(declaredMissing(ls, [{ Target: '/config' }, { Target: '/movies' }]), [{ target: '/cache', anonymous: true }]);
  assert.deepEqual(declaredMissing(ls, [{ Target: '/config/' }, { Target: '/movies' }, { Target: '/cache' }]), []);
  assert.deepEqual(declaredMissing(null, []), []);
  // settings: in the data path; large data: the data disk with the most room
  assert.deepEqual(proposeDeclared('/config', hints, 'radarr').source, '/storage/cosmos/radarr/config');
  assert.equal(proposeDeclared('/config', hints, 'radarr').type, 'bind');
  assert.equal(proposeDeclared('/movies', hints, 'radarr').source, '/archive/radarr/movies');
  assert.equal(proposeDeclared('/movies', hints, 'radarr').reason, 'bulk');
  assert.equal(proposeDeclared('/movies', hints, 'radarr').place.path, '/archive');
  // a fresh install: the data path on the system disk, a data disk: settings go to the disk
  const fresh = { dataPath: '/cosmos-storage', places: [
    { path: '/cosmos-storage', kind: 'data-path', osDisk: true, disk: '/', free: 0.2 * TB },
    { path: '/mnt/data', kind: 'disk', osDisk: false, disk: '/mnt/data', free: 4 * TB },
    { path: '/var/lib/docker', kind: 'docker-root', osDisk: true, disk: '/', free: 0.2 * TB },
    { path: '/', kind: 'system', osDisk: true, disk: '/', free: 0.2 * TB }] };
  assert.equal(proposeDeclared('/config', fresh, 'app').source, '/mnt/data/cosmos-storage/app/config');
  // the data path on the system disk, Docker's root on a data disk: settings still go to the data disk
  const dockerOnDisk = { ...fresh, places: fresh.places.map((p) => (p.kind === 'docker-root' ? { ...p, path: '/mnt/data/docker', osDisk: false, disk: '/mnt/data' } : p)) };
  assert.equal(proposeDeclared('/config', dockerOnDisk, 'app').source, '/mnt/data/cosmos-storage/app/config');
  // nothing known: a named volume Docker keeps
  assert.deepEqual(proposeDeclared('/data', { places: [] }, 'my app'), { type: 'volume', source: 'my-app_data', reason: 'volume', place: null });
  assert.deepEqual(sourcePlaces(hints, '/data', 'radarr').map((p) => p.source), ['/storage/cosmos/radarr/data', '/archive/radarr/data', '/storage/radarr/data']);
});

test('the folder of a bind is said in words', () => {
  const user = hints.runsAs;
  assert.equal(folderFinding({ state: 'ok', writable: 'yes', owner: '1000:1000' }, user).code, 'writable');
  assert.equal(folderFinding({ state: 'ok', writable: 'no', owner: '0:0' }, user).code, 'notWritable');
  assert.equal(folderFinding({ state: 'ok', writable: 'no', owner: '0:0' }, user).severity, 'warning');
  assert.equal(folderFinding({ state: 'ok', writable: 'unknown' }, user).code, 'unknownUser');
  assert.equal(folderFinding({ state: 'missing' }, user).code, 'missing');
  assert.equal(folderFinding({ state: 'link' }, user).code, 'link');
  assert.equal(folderFinding({ state: 'outside' }, user).code, 'outside');
  assert.equal(folderFinding(null, user), null);
});

test('the user, and the PUID/PGID of a linuxserver.io image', () => {
  assert.equal(dataFoldersOwner(hints), '1000:1000');
  const mixed = { folders: [{ state: 'ok', owner: '0:0' }, { state: 'ok', owner: '1001:1001' }, { state: 'ok', owner: '1001:1001' }, { state: 'ok', owner: '1000:1000' }] };
  assert.equal(dataFoldersOwner(mixed), '1001:1001');
  assert.equal(dataFoldersOwner({ folders: [{ state: 'ok', owner: '0:0' }] }), '0:0');
  assert.equal(dataFoldersOwner({ folders: [] }), '');
  // linuxserver: no "owner" choice for the user field
  assert.deepEqual(userOptions(hints).map((o) => o.value), ['image', 'other']);
  const plain = { image: { user: '' }, folders: [{ state: 'ok', owner: '1000:1000' }], linuxserver: false };
  assert.deepEqual(userOptions(plain).map((o) => `${o.value}=${o.user}`), ['image=', 'owner=1000:1000', 'other=null']);
  assert.equal(userChoice('', plain), 'image');
  assert.equal(userChoice('1000:1000', plain), 'owner');
  assert.equal(userChoice('nobody', plain), 'other');
  // the folders are 1000's and PUID says 1000: nothing to fix
  assert.equal(puidMismatch(hints, [{ key: 'PUID', value: '1000' }, { key: 'PGID', value: '1000' }]), null);
  // the folders are 1001's
  const theirs = { ...hints, folders: [{ state: 'ok', owner: '1001:1001' }] };
  const fix = puidMismatch(theirs, [{ key: 'PUID', value: '1000' }, { key: 'PGID', value: '1000' }]);
  assert.deepEqual(fix, { owner: '1001:1001', puid: '1001', pgid: '1001', now: '1000:1000', defaulted: false });
  // no PUID: the image's default 911
  assert.equal(puidMismatch(hints, []).now, '911:911');
  assert.equal(puidMismatch(hints, []).defaulted, true);
  // not linuxserver, root's folders, nothing known: no proposal
  assert.equal(puidMismatch({ ...hints, linuxserver: false }, []), null);
  assert.equal(puidMismatch({ ...hints, folders: [{ state: 'ok', owner: '0:0' }] }, []), null);
  const rows = withPuid([{ key: 'TZ', value: 'Etc/UTC' }, { key: 'PUID', value: '1000' }], fix);
  assert.deepEqual(rows, [{ key: 'TZ', value: 'Etc/UTC' }, { key: 'PUID', value: '1001' }, { key: 'PGID', value: '1001' }]);
  assert.equal(userOnLinuxserver(hints, '1000:1000'), true);
  assert.equal(userOnLinuxserver(hints, ''), false);
  assert.equal(userOnLinuxserver(hints, 'root'), false);
  assert.equal(userOnLinuxserver(plain, '1000'), false);
});

test('limits are choices under the server\'s totals', () => {
  const memory = memoryOptions(hints);
  assert.deepEqual(memory.map((o) => `${o.kind}:${o.value}`), ['recipe:1g', 'size:512m', 'size:2g', 'size:4g', 'size:8g', 'none:']);
  // a small server: only what fits in half of it
  assert.deepEqual(memoryOptions({ server: { memory: 2 * GIB } }).map((o) => o.value), ['512m', '1g', '']);
  // a recipe asking more than the server has is not proposed
  assert.deepEqual(memoryOptions({ server: { memory: 512 * 1024 * 1024 }, recipe: { memoryLimit: GIB } }).map((o) => o.kind), ['none']);
  assert.deepEqual(cpuOptions(hints).map((o) => `${o.kind}:${o.value}`), ['number:0.5', 'number:1', 'number:2', 'half:8', 'none:0']);
  assert.deepEqual(cpuOptions({ server: { cpus: 1 } }).map((o) => o.value), ['0.5', '1', '0']);
  assert.equal(limitChoice('', memory), 'none');
  assert.equal(limitChoice('1g', memory), 'v:1g');
  assert.equal(limitChoice('1G', memory), 'v:1g');
  assert.equal(limitChoice('768m', memory), 'other');
  assert.equal(limitChoice('0', cpuOptions(hints)), 'none');
  assert.equal(limitChoice('2', cpuOptions(hints)), 'v:2');
  assert.deepEqual(RESTART.map((r) => r.value), ['unless-stopped', 'always', 'on-failure', 'no']);
});

test('variables and devices are offered when they are not there yet', () => {
  const env = [{ key: 'PUID', value: '1000' }, { key: 'TZ', value: 'Etc/UTC' }];
  const offered = envSuggestions(hints, { env: [{ name: 'RADARR__AUTH__METHOD' }, { name: 'PUID' }, { name: 'bad name' }] }, env);
  assert.deepEqual(offered.map((o) => `${o.name}:${o.source}`), ['PGID:recipe', 'UMASK:recipe', 'RADARR__AUTH__METHOD:description']);
  assert.deepEqual(envSuggestions(null, null, []), []);
  assert.deepEqual(deviceOptions(hints, [{ key: '/dev/dri/renderD128', value: '/dev/dri/renderD128' }]).map((d) => d.kind), ['serial']);
  assert.deepEqual(deviceOptions(hints, [{ key: '/dev/ttyUSB0', value: '/dev/ttyUSB0' }]).map((d) => d.kind), ['render']);
  assert.deepEqual(deviceRow(hints.devices[1]), { key: '/dev/serial/by-id/usb-dongle', value: '/dev/ttyUSB0' });
});

import { configuredPortRows, portsToSend, insideWritableBind, vpnOf } from './editHints.js';
import { sensitiveAccess, volumeBindDevice } from './sensitiveAccess.js';
import { readFileSync } from 'node:fs';

test('the Network tab reads ports from the settings, so a stopped container keeps them all', () => {
  const stopped = {
    NetworkSettings: { Ports: null },
    Config: { ExposedPorts: { '9000/tcp': {}, '9001/udp': {}, '9002/tcp': {}, '9003/tcp': {} } },
    HostConfig: { PortBindings: { '9000/tcp': [{ HostIp: '127.0.0.1', HostPort: '18300' }], '9001/udp': [{ HostIp: '0.0.0.0', HostPort: '18301' }], '9003/tcp': [{ HostIp: '', HostPort: '' }] } },
  };
  const rows = configuredPortRows(stopped);
  assert.deepEqual(rows.map((r) => `${r.port}/${r.protocol}|${r.hostIp}|${r.hostPort}|${r.random ? 'random' : ''}`),
    ['9000/tcp|127.0.0.1|18300|', '9001/udp|0.0.0.0|18301|', '9002/tcp|||', '9003/tcp|||random']);
  const sent = portsToSend(rows);
  assert.deepEqual(sent.exposedPorts, ['9000/tcp', '9001/udp', '9002/tcp', '9003/tcp']);
  assert.deepEqual(sent.portBindings, {
    '9000/tcp': [{ HostIp: '127.0.0.1', HostPort: '18300' }], '9001/udp': [{ HostIp: '0.0.0.0', HostPort: '18301' }], '9003/tcp': [{ HostIp: '', HostPort: '' }],
  });
  // a row removed is not sent; a row without a port is not either
  assert.deepEqual(portsToSend([{ port: '', protocol: 'tcp', hostPort: '1' }]), { portBindings: {}, exposedPorts: [] });
});

test('nothing is proposed inside a folder the app can write', () => {
  const whole = { ...hints, folders: [{ source: '/storage/cosmos/radarr', target: '/config', state: 'ok', readOnly: false }] };
  assert.equal(insideWritableBind('/storage/cosmos/radarr/config', whole), true);
  assert.equal(insideWritableBind('/storage/cosmos/radarr-config', whole), false);
  // the proposal moves beside it
  assert.equal(proposeDeclared('/config', whole, 'radarr').source, '/storage/cosmos/radarr-config');
  assert.ok(sourcePlaces(whole, '/data', 'radarr').every((p) => !insideWritableBind(p.source, whole)));
  // a read-only folder is not a risk
  const ro = { ...hints, folders: [{ source: '/storage/cosmos/radarr', target: '/config', state: 'ok', readOnly: true }] };
  assert.equal(proposeDeclared('/config', ro, 'radarr').source, '/storage/cosmos/radarr/config');
  // the whole data path writable: no folder beside it either, a named volume instead
  const all = { ...hints, folders: [{ source: '/storage', target: '/all', state: 'ok' }, { source: '/archive', target: '/b', state: 'ok' }] };
  assert.equal(proposeDeclared('/config', all, 'radarr').type, 'volume');
  assert.deepEqual(sourcePlaces(all, '/data', 'radarr'), []);
});

test('links, slow disks and VPN addresses are said', () => {
  assert.equal(folderFinding({ state: 'link', linkTo: '/' }, hints.runsAs).params.target, '/');
  assert.equal(folderFinding({ state: 'ok', linkTo: '/storage/x', writable: 'yes' }, hints.runsAs).code, 'linkInside');
  assert.equal(folderFinding({ state: 'slow' }, hints.runsAs).code, 'slow');
  const vpn = { vpn: [{ interface: 'wg0', address: '10.8.0.1' }] };
  assert.equal(vpnOf('10.8.0.1', vpn).interface, 'wg0');
  assert.equal(vpnOf('192.168.1.10', vpn), null);
});

test('a volume that binds a host path, and shared propagation, follow the server\'s cases', () => {
  const cases = JSON.parse(readFileSync(new URL('../../../src/docker/testdata/sensitive_cases.json', import.meta.url), 'utf8'));
  assert.ok(cases.mounts.length > 0);
  for (const c of cases.mounts) {
    const items = sensitiveAccess({ services: { s: { volumes: [c.mount] } } }).map((i) => `${i.kind}|${i.value}`);
    assert.deepEqual(items, c.items, JSON.stringify(c.mount));
  }
  assert.equal(volumeBindDevice({ type: 'volume', volumeOptions: { driverConfig: { options: { o: 'bind', device: '/x' } } } }), '/x');
});

import { withServerItems, sensitiveBind as sensitiveBindRule, sentence as sensitiveSentence } from './sensitiveAccess.js';

test('the server\'s ticks join the panel\'s, each once (plan 47, second review)', () => {
  const local = sensitiveAccess({ services: { app: { volumes: [{ type: 'bind', source: '/etc', target: '/e' }] } } });
  const merged = withServerItems(local, [
    { service: 'app', kind: 'bind', value: '/etc' },
    { service: 'app', kind: 'nested', value: '/mnt/data/media/movies', detail: 'radarr (/mnt/data/media)' },
  ]);
  assert.deepEqual(merged.map((i) => i.id), ['app|bind|/etc', 'app|nested|/mnt/data/media/movies']);
  assert.match(sensitiveSentence(merged[1]).text, /could turn it into a link/);
  assert.equal(sensitiveSentence(merged[1]).params.detail, 'radarr (/mnt/data/media)');
});

test('the second review\'s sensitive places and options', () => {
  assert.equal(sensitiveBindRule('/home'), 'bind');
  assert.equal(sensitiveBindRule('/home/me'), 'bind');
  assert.equal(sensitiveBindRule('/home/me/music'), '');
  assert.equal(sensitiveBindRule('/var/spool/cron/crontabs'), 'bind');
  assert.equal(sensitiveBindRule('/var/lib/libvirt/images'), 'bind');
  const ids = sensitiveAccess({ services: { a: { network_mode: 'container:gluetun', security_opt: ['label=disable', 'seccomp=/tmp/x.json', 'no-new-privileges:false', 'no-new-privileges:true'] } } }).map((i) => i.id);
  assert.deepEqual(ids, ['a|network-container|gluetun', 'a|unconfined|label=disable', 'a|unconfined|seccomp=/tmp/x.json', 'a|unconfined|no-new-privileges:false']);
});

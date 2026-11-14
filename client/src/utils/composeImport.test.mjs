// Fork addition: tests of composeImport.js, one compose feature at a time. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { blockingNotes, convertCompose, convertPort, convertVolume, TEXT, text } from './composeImport.js';

const ctx = { serviceName: 'app', dataPath: '/storage/cosmos' };
const one = (service, more = {}) => convertCompose({ services: { web: { image: 'nginx', ...service } }, ...more }, ctx);
const codes = (notes) => notes.map((n) => n.kind + ':' + n.code);

test('ports: short, with address, ranges, udp', () => {
  const { doc, notes } = one({ ports: ['8080:80', '127.0.0.1:8443:443', '6881:6881/udp', '8000-8010:8000-8010', '[::1]:9000:9000'] });
  assert.deepEqual(doc.services['app-web'].ports, ['8080:80', '127.0.0.1:8443:443', '6881:6881/udp', '8000-8010:8000-8010', '[::1]:9000:9000']);
  assert.deepEqual(notes, []);
});

test('ports: a lone number (upstream made "undefined:undefined/tcp") is published on the same port, and said', () => {
  const { doc, notes } = one({ ports: [8080, '9090/udp', '127.0.0.1::5000'] });
  assert.deepEqual(doc.services['app-web'].ports, ['8080:8080', '9090:9090/udp', '127.0.0.1:5000:5000']);
  assert.deepEqual(codes(notes), ['changed:port.random', 'changed:port.random', 'changed:port.random']);
  assert.equal(notes[0].params.port, '8080');
});

test('ports: long syntax with and without published, host_ip, protocol', () => {
  const { doc, notes } = one({ ports: [
    { target: 80, published: 8080 },
    { target: 53, published: '53', protocol: 'udp', host_ip: '192.168.1.5' },
    { target: 9000 },
    { target: 443, published: 8443, host_ip: '::1' },
  ] });
  assert.deepEqual(doc.services['app-web'].ports, ['8080:80/tcp', '192.168.1.5:53:53/udp', '9000:9000/tcp', '[::1]:8443:443/tcp']);
  assert.deepEqual(codes(notes), ['changed:port.random']);
});

test('ports: what cannot be read blocks the creation', () => {
  const { notes } = one({ ports: ['web:80', { published: 80 }] });
  assert.deepEqual(codes(notes), ['blocked:port.invalid', 'blocked:port.invalid']);
  assert.equal(blockingNotes(notes).length, 2);
  assert.equal(convertPort(null).note.kind, 'blocked');
});

test('expose stays known without being published', () => {
  assert.deepEqual(one({ expose: [3000, '4000/udp'] }).doc.services['app-web'].expose, ['3000', '4000/udp']);
});

test('environment: a list, a map, values that are not text, and names without value', () => {
  let r = one({ environment: ['A=1', 'B=x=y', 'FROM_SHELL'] });
  assert.deepEqual(r.doc.services['app-web'].environment, ['A=1', 'B=x=y']);
  assert.deepEqual(codes(r.notes), ['dropped:env.unset']);
  r = one({ environment: { A: 1, B: true, C: null, D: '' } });
  // upstream wrote "C=null"
  assert.deepEqual(r.doc.services['app-web'].environment, ['A=1', 'B=true', 'D=']);
  assert.equal(r.notes[0].params.name, 'C');
});

test('env_file is said, never read', () => {
  const { doc, notes } = one({ env_file: ['.env', { path: 'other.env' }] });
  assert.equal('env_file' in doc.services['app-web'], false);
  assert.deepEqual(notes.map((n) => n.params.value), ['.env', 'other.env']);
});

test('depends_on: short and long form, keys become container names', () => {
  const r = convertCompose({ services: {
    db: { image: 'postgres' },
    cache: { image: 'redis' },
    web: { image: 'x', depends_on: { db: { condition: 'service_healthy' }, cache: { condition: 'service_completed_successfully', required: false } } },
    worker: { image: 'x', depends_on: ['db'] },
  } }, ctx);
  assert.deepEqual(r.doc.services['app-web'].depends_on, { 'app-db': { condition: 'service_healthy' }, 'app-cache': { condition: 'service_completed_successfully' } });
  assert.deepEqual(r.doc.services['app-worker'].depends_on, { 'app-db': { condition: 'service_started' } });
  assert.deepEqual(codes(r.notes).sort(), ['changed:depends.completed', 'changed:depends.optional']);
});

test('volumes: named, binds, relative, read-only, options', () => {
  const { doc, notes } = one({ volumes: ['data:/var/lib/data', '/srv/media:/media:ro', './config:/config', 'cache:/cache:nocopy', '/mnt/x:/x:rshared', '/tmp/a:/a:z'] },
    { volumes: { data: null, cache: {} } });
  assert.deepEqual(doc.services['app-web'].volumes, [
    { type: 'volume', source: 'data', target: '/var/lib/data' },
    { type: 'bind', source: '/srv/media', target: '/media', ReadOnly: true },
    // upstream put it in /cosmos-storage/config, shared by every app
    { type: 'bind', source: '/storage/cosmos/app/config', target: '/config' },
    { type: 'volume', source: 'cache', target: '/cache', VolumeOptions: { NoCopy: true } },
    { type: 'bind', source: '/mnt/x', target: '/x', BindOptions: { Propagation: 'rshared' } },
    { type: 'bind', source: '/tmp/a', target: '/a' },
  ]);
  assert.deepEqual(codes(notes), ['changed:volume.relative', 'dropped:volume.option']);
  assert.deepEqual(doc.volumes, { data: {}, cache: {} });
});

test('volumes: long syntax keeps read_only, propagation, nocopy, tmpfs size', () => {
  const { doc, notes } = one({ volumes: [
    { type: 'bind', source: '/srv/a', target: '/a', read_only: true, bind: { propagation: 'rslave', create_host_path: true } },
    { type: 'volume', source: 'v', target: '/v', volume: { nocopy: true, subpath: 'x' } },
    { type: 'tmpfs', target: '/run', tmpfs: { size: '64m' } },
  ] });
  assert.deepEqual(doc.services['app-web'].volumes, [
    { type: 'bind', source: '/srv/a', target: '/a', ReadOnly: true, BindOptions: { Propagation: 'rslave' } },
    { type: 'volume', source: 'v', target: '/v', VolumeOptions: { NoCopy: true } },
    { type: 'tmpfs', source: '', target: '/run', TmpfsOptions: { SizeBytes: 67108864 } },
  ]);
  assert.deepEqual(codes(notes), ['dropped:volume.option']);
});

test('volumes: a home folder or ".." blocks the creation', () => {
  const { doc, notes } = one({ volumes: ['~/music:/music', '../shared:/shared', '/srv/../etc:/x'] });
  assert.deepEqual(doc.services['app-web'].volumes, []);
  assert.deepEqual(codes(notes), ['blocked:volume.home', 'blocked:volume.parent', 'blocked:volume.parent']);
  assert.equal(convertVolume('./a/../../b:/b', ctx).notes[0].code, 'volume.parent');
});

test('restart, shm_size, tmpfs, init, read_only and limits are passed as the server reads them', () => {
  const { doc, notes } = one({ restart: 'on-failure:3', shm_size: '1g', tmpfs: '/run', init: true, read_only: true, mem_limit: '512m', cpus: '1.5', cpuset: '0-1', stop_grace_period: '1m30s' });
  const s = doc.services['app-web'];
  assert.equal(s.restart, 'on-failure:3');
  assert.equal(s.shm_size, '1g');
  assert.deepEqual(s.tmpfs, ['/run']);
  assert.equal(s.init, true);
  assert.equal(s.read_only, true);
  assert.equal(s.mem_limit, '512m');
  assert.equal(s.cpus, 1.5);
  assert.equal(s.cpuset_cpus, '0-1');
  assert.equal(s.stop_grace_period, 90);
  assert.deepEqual(notes, []);
});

test('healthcheck: durations stay as written for the server, a bad one blocks', () => {
  let r = one({ healthcheck: { test: 'curl -f http://localhost', interval: '1m30s', timeout: '10s', start_period: 5, retries: 3 } });
  assert.deepEqual(r.doc.services['app-web'].healthcheck, { test: 'curl -f http://localhost', interval: '1m30s', timeout: '10s', start_period: 5, retries: 3 });
  assert.deepEqual(r.notes, []);
  r = one({ healthcheck: { test: ['CMD', 'true'], interval: 'often' } });
  assert.deepEqual(codes(r.notes), ['blocked:healthcheck.interval']);
  r = one({ healthcheck: { disable: true } });
  assert.deepEqual(r.doc.services['app-web'].healthcheck, { disable: true });
});

test('command and entrypoint keep the exec form (upstream joined the list with spaces)', () => {
  const s = one({ command: ['sh', '-c', 'echo "a b"'], entrypoint: '/init --verbose' }).doc.services['app-web'];
  assert.deepEqual(s.command, ['sh', '-c', 'echo "a b"']);
  assert.equal(s.entrypoint, '/init --verbose');
});

test('network_mode: service and container become the container\'s name, with the label; an unknown one blocks', () => {
  const r = convertCompose({ services: {
    vpn: { image: 'gluetun' },
    torrent: { image: 'qbit', network_mode: 'service:vpn' },
    other: { image: 'x', network_mode: 'service:nothere' },
    host: { image: 'x', network_mode: 'host' },
  } }, ctx);
  assert.equal(r.doc.services['app-torrent'].network_mode, 'container:app-vpn');
  assert.equal(r.doc.services['app-torrent'].labels['cosmos-force-network-mode'], 'container:app-vpn');
  assert.equal('hostname' in r.doc.services['app-torrent'], false);
  assert.equal(r.doc.services['app-host'].network_mode, 'host');
  assert.deepEqual(codes(r.notes), ['blocked:network.service']);
  // services without a mode join the app's own network
  assert.equal(r.doc.services['app-vpn'].network_mode, 'cosmos-app-default');
  assert.deepEqual(r.doc.networks, { 'cosmos-app-default': {} });
});

test('a service on another one\'s network cannot publish ports: said before anything is created', () => {
  const r = convertCompose({ services: { vpn: { image: 'gluetun' }, torrent: { image: 'qbit', network_mode: 'service:vpn', ports: ['8080:8080'] } } }, ctx);
  assert.deepEqual(codes(r.notes), ['blocked:network.ports']);
});

test('labels in both forms', () => {
  assert.deepEqual(one({ labels: ['a=1', 'b=x=y', 'c'] }).doc.services['app-web'].labels, { a: '1', b: 'x=y', c: '' });
  assert.deepEqual(one({ labels: { a: 1, b: null } }).doc.services['app-web'].labels, { a: '1', b: '' });
});

test('sysctls in list form keep their values (upstream lost them)', () => {
  assert.deepEqual(one({ sysctls: ['net.core.somaxconn=1024'] }).doc.services['app-web'].sysctls, { 'net.core.somaxconn': '1024' });
});

test('keys Cosmos does not use are named, not dropped silently', () => {
  const { doc, notes } = one({ build: '.', deploy: { resources: {} }, secrets: ['s'], ulimits: { nofile: 1024 }, pid: 'host', logging: {}, profiles: ['x'], platform: 'linux/arm64', 'x-mine': 1 },
    { secrets: { s: { file: './s' } }, version: '3.8', 'x-anchor': {} });
  assert.deepEqual(codes(notes).sort(), [
    'dropped:key.build', 'dropped:key.deploy', 'dropped:key.files', 'dropped:key.platform', 'dropped:key.profiles',
    'dropped:key.unknown', 'dropped:key.unknown', 'dropped:key.unknown', 'dropped:top.unknown',
  ]);
  const s = doc.services['app-web'];
  for (const key of ['build', 'deploy', 'secrets', 'ulimits', 'pid', 'logging']) {
    assert.equal(key in s, false, key);
  }
  assert.equal(s['x-mine'], 1);
  assert.equal('secrets' in doc, false);
});

test('a file with services only, Cosmos\' own keys, stacks', () => {
  const r = convertCompose({ web: { image: 'a', 'x-routes': [{ name: 'web' }] }, db: { image: 'b' } }, ctx);
  assert.deepEqual(Object.keys(r.doc.services).sort(), ['app-db', 'app-web']);
  assert.deepEqual(r.doc.services['app-web'].routes, [{ name: 'web' }]);
  assert.equal(r.doc.services['app-web'].labels['cosmos.stack'], 'app');
  assert.equal(Object.values(r.doc.services).filter((s) => s.labels['cosmos.stack.main'] === 'true').length, 1);
});

test('every sentence is in the English translation file, word for word', () => {
  const translations = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));
  for (const code of Object.keys(TEXT)) {
    const s = text(code);
    assert.equal(translations[s.key], s.text, s.key);
  }
});

// Fork addition: tests of newContainer.js: every field of the new-container
// form reaches the request, and the edit form shows the limits again. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fieldProblems, limitsForForm, memoryBytes, memoryText, serviceFromForm, withDockerTab } from './newContainer.js';

// the form's initial state (newServiceForm.jsx)
const blank = () => ({
  Name: '', Names: [], Ports: [], Route: null,
  Config: { Env: [], Labels: {}, ExposedPorts: [], Volumes: {}, Name: '', Image: '', Tty: true, OpenStdin: true },
  HostConfig: { PortBindings: [], RestartPolicy: { Name: 'always' }, Mounts: [] },
  NetworkSettings: { Networks: {}, Ports: [] },
});

// what setup.jsx hands over (containerInfoFrom)
const dockerTab = (change) => ({
  name: 'web', image: 'nginx:1.27', restartPolicy: 'unless-stopped', user: '', envVars: ['A=1'], labels: {}, devices: [],
  interactive: 2, memLimit: '', cpus: 0, ...change,
});

const serviceOf = (info) => serviceFromForm(info).services.container_name;

test('the user reaches the request', () => {
  const info = withDockerTab(blank(), dockerTab({ user: ' 1000:1000 ' }));
  assert.equal(serviceOf(info).user, '1000:1000');
  assert.equal('user' in serviceOf(withDockerTab(blank(), dockerTab())), false);
});

test('the memory limit reaches the request as typed', () => {
  assert.equal(serviceOf(withDockerTab(blank(), dockerTab({ memLimit: '512m' }))).mem_limit, '512m');
  assert.equal('mem_limit' in serviceOf(withDockerTab(blank(), dockerTab({ memLimit: '' }))), false);
});

test('the CPU limit reaches the request', () => {
  assert.equal(serviceOf(withDockerTab(blank(), dockerTab({ cpus: 1.5 }))).cpus, 1.5);
  assert.equal('cpus' in serviceOf(withDockerTab(blank(), dockerTab({ cpus: 0 }))), false);
});

test('the interactive tick reaches the request', () => {
  assert.equal(serviceOf(withDockerTab(blank(), dockerTab({ interactive: 1 }))).tty, false);
  assert.equal(serviceOf(withDockerTab(blank(), dockerTab({ interactive: 1 }))).stdin_open, false);
  assert.equal(serviceOf(withDockerTab(blank(), dockerTab({ interactive: 2 }))).tty, true);
});

test('the network mode reaches the request', () => {
  const info = withDockerTab(blank(), dockerTab());
  info.HostConfig = { ...info.HostConfig, NetworkMode: 'host' };
  assert.equal(serviceOf(info).network_mode, 'host');
  assert.equal('network_mode' in serviceOf(withDockerTab(blank(), dockerTab())), false);
});

test('"use as VPN" (another container\'s network) reaches the request, without ports, networks or DNS', () => {
  const info = withDockerTab(blank(), dockerTab());
  info.HostConfig = { ...info.HostConfig, NetworkMode: 'container:gluetun', PortBindings: ['8080:8080/tcp'], Dns: ['1.1.1.1'] };
  info.NetworkSettings = { Networks: { bridge: {} } };
  const service = serviceOf(info);
  assert.equal(service.network_mode, 'container:gluetun');
  assert.deepEqual(service.ports, []);
  assert.deepEqual(service.networks, {});
  assert.deepEqual(service.dns, []);
});

test('the other fields are kept as before', () => {
  const info = withDockerTab(blank(), dockerTab({ devices: ['/dev/dri:/dev/dri'] }));
  info.HostConfig = { ...info.HostConfig, PortBindings: ['8080:80/tcp'] };
  const service = serviceOf(info);
  assert.equal(service.container_name, 'web');
  assert.equal(service.image, 'nginx:1.27');
  assert.equal(service.restart, 'unless-stopped');
  assert.deepEqual(service.devices, ['/dev/dri:/dev/dri']);
  assert.deepEqual(service.ports, ['8080:80/tcp']);
  assert.deepEqual(service.environment, ['A=1']);
});

test('the edit form shows the limits of a container in sizes it can send back', () => {
  assert.deepEqual(limitsForForm({ HostConfig: { Memory: 536870912, NanoCpus: 1500000000 } }), { memLimit: '512m', cpus: 1.5 });
  assert.deepEqual(limitsForForm({ HostConfig: { Memory: 2147483648 } }), { memLimit: '2g', cpus: 0 });
  assert.deepEqual(limitsForForm({ HostConfig: {} }), { memLimit: '', cpus: 0 });
  // the new form keeps what was typed, even while it is not a size yet
  assert.deepEqual(limitsForForm(withDockerTab(blank(), dockerTab({ memLimit: '5x' }))), { memLimit: '5x', cpus: 0 });
});

test('sizes both ways', () => {
  assert.equal(memoryBytes('512m'), 536870912);
  assert.equal(memoryBytes('1.5g'), 1610612736);
  assert.equal(memoryBytes('536870912'), 536870912);
  assert.equal(memoryBytes('2GiB'), 2147483648);
  assert.equal(memoryBytes(''), 0);
  assert.equal(memoryBytes('lots'), null);
  assert.equal(memoryText(1536 * 1024 * 1024), '1536m');
  assert.equal(memoryText(1000), '1000');
  assert.equal(memoryText(0), '');
  assert.equal(memoryBytes(memoryText(123456789)), 123456789);
});

test('values the server would refuse are said under their field', () => {
  assert.deepEqual(Object.keys(fieldProblems(withDockerTab(blank(), dockerTab({ user: 'a b', memLimit: '1m' })))).sort(), ['memLimit', 'user']);
  assert.deepEqual(fieldProblems(withDockerTab(blank(), dockerTab({ user: 'www-data', memLimit: '2g', cpus: 2 }))), {});
});

import { nameFromImage, quickSetup } from './newContainer.js';

test('"Review and create" proposes the name and the local URL, and keeps what was typed', () => {
  assert.equal(nameFromImage('lscr.io/linuxserver/radarr:latest'), 'radarr');
  assert.equal(nameFromImage('ghcr.io/home-assistant/home-assistant@sha256:abc'), 'home-assistant');
  const route = (name, port) => ({ Name: name, Target: `http://${name}:${port}` });
  const info = blank();
  info.Config = { ...info.Config, Image: 'jellyfin/jellyfin:10.9' };
  let r = quickSetup(info, { port: { port: 8096, source: 'usualOnly' }, route });
  assert.equal(r.info.Name, 'jellyfin');
  assert.equal(r.info.CreateRoute, true);
  assert.deepEqual(r.info.Route, { Name: 'jellyfin', Target: 'http://jellyfin:8096' });
  assert.deepEqual(r.proposed, ['name', 'route']);
  // a name typed, a URL already ticked: left alone
  r = quickSetup({ ...info, Name: 'media', CreateRoute: true, Route: { Name: 'mine' } }, { port: { port: 8096 }, route });
  assert.equal(r.info.Name, 'media');
  assert.deepEqual(r.info.Route, { Name: 'mine' });
  assert.deepEqual(r.proposed, []);
  // no port known: no URL proposed
  assert.equal(quickSetup(info, { port: { port: null }, route }).info.CreateRoute, undefined);
});

test('the request of the form is the fixture the live test creates (src/docker/fork_creation_live_test.go)', () => {
  const fixture = JSON.parse(readFileSync(new URL('../../../src/docker/testdata/new_container_form.json', import.meta.url), 'utf8'));
  delete fixture._comment;
  const info = withDockerTab(blank(), dockerTab({
    name: 'cosmos-edit-live-form', image: 'busybox:1.36', envVars: ['MARK=form'], user: '1000:1000', memLimit: '64m', cpus: 0.5, interactive: 1,
  }));
  info.HostConfig = { ...info.HostConfig, NetworkMode: 'container:cosmos-edit-live-vpn', PortBindings: ['8080:80/tcp'], Dns: ['1.1.1.1'] };
  assert.deepEqual(serviceFromForm(info), fixture);
});

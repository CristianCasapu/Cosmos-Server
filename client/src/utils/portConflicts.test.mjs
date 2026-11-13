// Fork addition: tests of portConflicts.js. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  parseHostPort, addressesOverlap, containerHolders, cosmosHolders, parseComposePort,
  findPortConflicts, hasBlockingPortConflict, composePortRows, composeContainerNames,
} from './portConflicts.js';

// what API.docker.list() returns, cut down
const containers = [
  { Names: ['/jellyfin'], State: 'running', Ports: [
    { IP: '0.0.0.0', PrivatePort: 8096, PublicPort: 8096, Type: 'tcp' },
    { IP: '::', PrivatePort: 8096, PublicPort: 8096, Type: 'tcp' },
    { PrivatePort: 1900, Type: 'udp' }, // exposed only
  ] },
  { Names: ['/pihole'], State: 'running', Ports: [
    { IP: '0.0.0.0', PrivatePort: 53, PublicPort: 53, Type: 'udp' },
    { IP: '127.0.0.1', PrivatePort: 80, PublicPort: 8081, Type: 'tcp' },
  ] },
  { Names: ['/stopped'], State: 'exited', Ports: [] },
];

test('parseHostPort reads a port, a range, nothing, and refuses the rest', () => {
  assert.deepEqual(parseHostPort('8080'), { from: 8080, to: 8080 });
  assert.deepEqual(parseHostPort(8080), { from: 8080, to: 8080 });
  assert.deepEqual(parseHostPort(' 8000-8010 '), { from: 8000, to: 8010 });
  assert.equal(parseHostPort(''), null);
  assert.equal(parseHostPort(undefined), null);
  for (const bad of ['0', '65536', '80a', 'http', '-1', '9000-8000', '80:80', '1.5', '8080 8081']) {
    assert.deepEqual(parseHostPort(bad), { invalid: true }, bad);
  }
});

test('addressesOverlap', () => {
  assert.equal(addressesOverlap('', '127.0.0.1'), 'overlap');
  assert.equal(addressesOverlap('0.0.0.0', '192.168.1.5'), 'overlap');
  assert.equal(addressesOverlap('127.0.0.1', '127.0.0.1'), 'overlap');
  assert.equal(addressesOverlap('127.0.0.1', '192.168.1.5'), 'apart');
  assert.equal(addressesOverlap('::1', '0:0:0:0:0:0:0:1'), 'overlap');
  assert.equal(addressesOverlap('[::1]', '::1'), 'overlap');
  assert.equal(addressesOverlap('::', 'fe80::1'), 'overlap');
  assert.equal(addressesOverlap('::1', 'fe80::1'), 'apart');
  assert.equal(addressesOverlap('::1', '127.0.0.1'), 'apart');
  assert.equal(addressesOverlap('::', '127.0.0.1'), 'maybe');
});

test('containerHolders lists published ports only and skips the container being edited', () => {
  const holders = containerHolders(containers);
  assert.equal(holders.length, 4);
  assert.deepEqual(holders[0], { kind: 'container', name: 'jellyfin', port: 8096, protocol: 'tcp', ip: '0.0.0.0', certain: true });
  assert.equal(holders.filter((h) => h.name === 'jellyfin').length, 2);

  assert.equal(containerHolders(containers, '/jellyfin').some((h) => h.name === 'jellyfin'), false);
  assert.equal(containerHolders(containers, ['pihole', 'jellyfin']).length, 0);
  assert.deepEqual(containerHolders(null), []);
  assert.deepEqual(containerHolders([{ Names: ['/x'] }, null, {}]), []);
});

test('cosmosHolders: certain on a host install, a hint in a container, HTTPS only when it is on', () => {
  const config = { HTTPConfig: { HTTPPort: '80', HTTPSPort: '443', HTTPSCertificateMode: 'LETSENCRYPT' } };
  assert.deepEqual(cosmosHolders(config, { containerized: false }), [
    { kind: 'cosmos', role: 'http', port: 80, protocol: 'tcp', ip: '', certain: true },
    { kind: 'cosmos', role: 'https', port: 443, protocol: 'tcp', ip: '', certain: true },
  ]);
  assert.deepEqual(cosmosHolders(config, { containerized: true }).map((h) => h.certain), [false, false]);
  assert.deepEqual(cosmosHolders(config, null).map((h) => h.certain), [false, false]);

  const plain = { HTTPConfig: { HTTPPort: '8080', HTTPSPort: '443', HTTPSCertificateMode: 'DISABLED' } };
  assert.deepEqual(cosmosHolders(plain, { containerized: false }).map((h) => [h.port, h.certain]), [[8080, true], [443, false]]);

  assert.deepEqual(cosmosHolders(null, null), []);
  assert.deepEqual(cosmosHolders({ HTTPConfig: { HTTPPort: 'abc', HTTPSPort: '' } }, { containerized: false }), []);
});

test('a port another container publishes is a certain conflict that names it', () => {
  const holders = containerHolders(containers);
  const [first, second, third] = findPortConflicts([
    { hostPort: '8096', protocol: 'tcp', port: '80' },
    { hostPort: '8097', protocol: 'tcp', port: '81' },
    { hostPort: '', protocol: 'tcp', port: '82' },
  ], holders);
  assert.equal(first.length, 1); // 0.0.0.0 and :: of one container are one finding
  assert.deepEqual(first[0], { certain: true, port: 8096, protocol: 'tcp', holder: { kind: 'container', name: 'jellyfin', role: undefined } });
  assert.deepEqual(second, []);
  assert.deepEqual(third, []);
});

test('the protocol and the address decide', () => {
  const holders = containerHolders(containers);
  const check = (row) => findPortConflicts([row], holders)[0];
  assert.deepEqual(check({ hostPort: '53', protocol: 'tcp' }), []); // held on UDP only
  assert.equal(check({ hostPort: '53', protocol: 'udp' })[0].holder.name, 'pihole');
  assert.equal(check({ hostPort: '53', protocol: 'UDP' })[0].certain, true);
  // 8081 is held on 127.0.0.1 only
  assert.equal(check({ hostPort: '8081', protocol: 'tcp' })[0].certain, true);
  assert.equal(check({ hostPort: '8081', protocol: 'tcp', hostIp: '127.0.0.1' })[0].certain, true);
  assert.deepEqual(check({ hostPort: '8081', protocol: 'tcp', hostIp: '192.168.1.10' }), []);
  assert.deepEqual(check({ hostPort: '8081', protocol: 'tcp', hostIp: '::1' }), []);
  assert.equal(check({ hostPort: '8081', protocol: 'tcp', hostIp: '::' })[0].certain, false);
});

test('the container being edited does not conflict with itself', () => {
  const rows = [{ hostPort: '8096', protocol: 'tcp' }];
  assert.equal(hasBlockingPortConflict(rows, containerHolders(containers)), true);
  assert.equal(hasBlockingPortConflict(rows, containerHolders(containers, '/jellyfin')), false);
});

test('Cosmos own ports', () => {
  const config = { HTTPConfig: { HTTPPort: '80', HTTPSPort: '443', HTTPSCertificateMode: 'SELFSIGNED' } };
  const rows = [{ hostPort: '443', protocol: 'tcp' }, { hostPort: '443', protocol: 'udp' }];

  const onHost = findPortConflicts(rows, cosmosHolders(config, { containerized: false }));
  assert.deepEqual(onHost[0], [{ certain: true, port: 443, protocol: 'tcp', holder: { kind: 'cosmos', name: undefined, role: 'https' } }]);
  assert.deepEqual(onHost[1], []);
  assert.equal(hasBlockingPortConflict(rows, cosmosHolders(config, { containerized: false })), true);

  const inContainer = findPortConflicts(rows, cosmosHolders(config, { containerized: true }));
  assert.equal(inContainer[0][0].certain, false);
  assert.equal(hasBlockingPortConflict(rows, cosmosHolders(config, { containerized: true })), false);
});

test('a host port listed twice in the form', () => {
  const found = findPortConflicts([
    { hostPort: '9000', protocol: 'tcp', port: '80' },
    { hostPort: '9000', protocol: 'tcp', port: '81' },
    { hostPort: '9000', protocol: 'udp', port: '81' },
    { hostPort: '9001', protocol: 'tcp', hostIp: '127.0.0.1' },
    { hostPort: '9001', protocol: 'tcp', hostIp: '192.168.1.2' },
  ], []);
  assert.deepEqual(found[0], [{ certain: true, port: 9000, protocol: 'tcp', holder: { kind: 'form', row: 1, name: undefined } }]);
  assert.equal(found[1][0].holder.row, 0);
  assert.deepEqual(found[2], []);
  assert.deepEqual(found[3], []);
  assert.deepEqual(found[4], []);
});

test('an invalid host port blocks, an empty one does not', () => {
  assert.deepEqual(findPortConflicts([{ hostPort: 'abc', protocol: 'tcp' }], []), [[{ invalid: true }]]);
  assert.equal(hasBlockingPortConflict([{ hostPort: '70000', protocol: 'tcp' }], []), true);
  assert.equal(hasBlockingPortConflict([{ hostPort: '', protocol: 'tcp' }, { hostPort: '8080', protocol: 'tcp' }], []), false);
  assert.equal(hasBlockingPortConflict(undefined, undefined), false);
  assert.deepEqual(findPortConflicts([null, undefined], []), [[], []]);
});

test('a range: every port of it in a compose file, one free port of it in the edit form', () => {
  const holders = containerHolders(containers);
  const rows = [{ hostPort: '8090-8099', protocol: 'tcp' }];
  const compose = findPortConflicts(rows, holders);
  assert.deepEqual([compose[0][0].certain, compose[0][0].port, compose[0][0].holder.name], [true, 8096, 'jellyfin']);
  const edit = findPortConflicts(rows, holders, { rangePicksOne: true });
  assert.equal(edit[0][0].certain, false);
  assert.equal(hasBlockingPortConflict(rows, holders, { rangePicksOne: true }), false);
  assert.deepEqual(findPortConflicts([{ hostPort: '8097-8099', protocol: 'tcp' }], holders), [[]]);
});

test('parseComposePort reads what the server reads', () => {
  assert.equal(parseComposePort('80'), null);
  assert.equal(parseComposePort('80/udp'), null);
  assert.deepEqual(parseComposePort('8080:80'), { hostPort: '8080', hostIp: '', protocol: 'tcp' });
  assert.deepEqual(parseComposePort('8080:80/UDP'), { hostPort: '8080', hostIp: '', protocol: 'udp' });
  assert.deepEqual(parseComposePort('127.0.0.1:8080:80'), { hostPort: '8080', hostIp: '127.0.0.1', protocol: 'tcp' });
  assert.deepEqual(parseComposePort('[::1]:8080:80/tcp'), { hostPort: '8080', hostIp: '::1', protocol: 'tcp' });
  assert.deepEqual(parseComposePort('8000-8010:8000-8010'), { hostPort: '8000-8010', hostIp: '', protocol: 'tcp' });
  assert.deepEqual(parseComposePort({ published: 8443, target: 443, protocol: 'tcp', host_ip: '127.0.0.1' }), { hostPort: '8443', hostIp: '127.0.0.1', protocol: 'tcp' });
  assert.equal(parseComposePort({ target: 443 }), null);
  assert.equal(parseComposePort(undefined), null);
  // the new container form writes ":80/tcp" for a row without a host port
  assert.deepEqual(findPortConflicts([parseComposePort(':80/tcp')], []), [[]]);
});

test('a compose definition: its ports against the holders, per service', () => {
  const service = { services: {
    web: { container_name: 'web', ports: ['8096:80', '127.0.0.1:9000:9000', '9000:9000', '9100:91', '9100:91'] },
    db: { ports: ['9000:5432/tcp', '53:53/udp', '${DB_PORT}:1'] },
    none: {},
  } };
  const rows = composePortRows(service);
  assert.equal(rows.length, 8);
  assert.deepEqual(rows[0], { hostPort: '8096', hostIp: '', protocol: 'tcp', service: 'web' });
  assert.equal(rows[5].service, 'db');
  assert.deepEqual(composeContainerNames(service), ['web', 'db', 'none']);

  const found = findPortConflicts(rows, containerHolders(containers));
  assert.equal(found[0][0].holder.name, 'jellyfin');
  // the same port twice in one service is dropped by the server; across services it fails
  assert.deepEqual(found[1].map((c) => [c.holder.kind, c.holder.name]), [['form', 'db']]);
  assert.deepEqual(found[3], []);
  assert.deepEqual(found[5].map((c) => c.holder.name), ['web', 'web']);
  assert.equal(found[6][0].holder.name, 'pihole');
  assert.deepEqual(found[7], [{ invalid: true }]);

  // installing again over a container of the same name
  const again = composePortRows({ services: { jellyfin: { ports: ['8096:8096'] } } });
  assert.deepEqual(findPortConflicts(again, containerHolders(containers, composeContainerNames({ services: { jellyfin: {} } }))), [[]]);
  assert.deepEqual(composePortRows(null), []);
});

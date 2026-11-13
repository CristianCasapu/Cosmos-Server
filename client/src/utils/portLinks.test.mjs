// Fork addition: tests of portLinks.js. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { schemeForPort, isIPLiteral, portLink, portChips } from './portLinks.js';

const httpsPanelOnIP = { protocol: 'https:', hostname: '192.168.1.10' };
const httpsPanelOnName = { protocol: 'https:', hostname: 'home.example.org' };
const httpPanel = { protocol: 'http:', hostname: 'server.local' };
const all = (port, privatePort, type = 'tcp') => ({ port, privatePort, type, ip: '0.0.0.0', scope: 'all' });

test('a published web port is opened over plain HTTP, also from the HTTPS panel', () => {
  // upstream built https://192.168.1.10:7878 here, which a plain-HTTP port cannot answer
  assert.equal(portLink(all(7878, 7878), httpsPanelOnIP).href, 'http://192.168.1.10:7878');
  assert.equal(portLink(all(8080, 80), httpsPanelOnIP).href, 'http://192.168.1.10:8080');
  assert.equal(portLink(all(8096, 8096), httpPanel).href, 'http://server.local:8096');
});

test('a port that speaks HTTPS inside the container is opened over HTTPS, also from an HTTP panel', () => {
  assert.equal(portLink(all(8443, 443), httpPanel).href, 'https://server.local:8443');
  assert.equal(portLink(all(9443, 9443), httpsPanelOnIP).href, 'https://192.168.1.10:9443');
  assert.equal(portLink(all(8920, 8920), httpsPanelOnIP).href, 'https://192.168.1.10:8920');
});

test('the scheme follows the container port, not the host port', () => {
  assert.equal(schemeForPort(443), 'https');
  assert.equal(schemeForPort(8443), 'https');
  assert.equal(schemeForPort(10443), 'https');
  assert.equal(schemeForPort(80), 'http');
  assert.equal(schemeForPort(7878), 'http');
  assert.equal(schemeForPort(4430), 'http');
  assert.equal(schemeForPort('8989'), 'http');
  assert.equal(schemeForPort(undefined), 'http');
  assert.equal(portLink(all(443, 80), httpPanel).scheme, 'http', 'host port 443 in front of a plain port');
});

test('a plain port under a name of the HTTPS panel is flagged: the browser may force HTTPS on it', () => {
  assert.equal(portLink(all(7878, 7878), httpsPanelOnName).mayUpgrade, true);
  assert.equal(portLink(all(7878, 7878), httpsPanelOnName).href, 'http://home.example.org:7878');
  assert.equal(portLink(all(7878, 7878), httpsPanelOnIP).mayUpgrade, false, 'never on an IP address');
  assert.equal(portLink(all(7878, 7878), httpPanel).mayUpgrade, false, 'not from an HTTP panel');
  assert.equal(portLink(all(8443, 443), httpsPanelOnName).mayUpgrade, false, 'already HTTPS');
});

test('ports that cannot be opened in a browser get no link, with the reason', () => {
  assert.deepEqual(portLink(all(5353, 53, 'udp'), httpPanel), { href: null, why: 'udp' });
  assert.deepEqual(portLink({ port: 8081, privatePort: 80, type: 'tcp', ip: '127.0.0.1', scope: 'server' }, httpPanel), { href: null, why: 'server' });
  assert.deepEqual(portLink(all(0, 80), httpPanel), { href: null, why: 'invalid' });
  assert.deepEqual(portLink(all(70000, 80), httpPanel), { href: null, why: 'invalid' });
  assert.deepEqual(portLink(all('80/../x', 80), httpPanel), { href: null, why: 'invalid' });
  assert.deepEqual(portLink(all(8080, 80), { protocol: 'https:', hostname: '' }), { href: null, why: 'invalid' });
});

test('a port published on one address of the server is opened on that address', () => {
  const one = { port: 8080, privatePort: 80, type: 'tcp', ip: '192.168.1.20', scope: 'address' };
  assert.equal(portLink(one, httpsPanelOnName).href, 'http://192.168.1.20:8080');
  assert.equal(portLink(one, httpsPanelOnName).mayUpgrade, false);
  const v6 = { port: 8080, privatePort: 80, type: 'tcp', ip: 'fd00::5', scope: 'address' };
  assert.equal(portLink(v6, httpPanel).href, 'http://[fd00::5]:8080');
});

test('isIPLiteral tells addresses from names', () => {
  ['192.168.1.10', '10.0.0.1', '::1', '[fd00::5]', 'fe80::1'].forEach((host) => assert.equal(isIPLiteral(host), true, host));
  ['server.local', 'home.example.org', 'localhost', '', undefined, '1.2.3'].forEach((host) => assert.equal(isIPLiteral(host), false, String(host)));
});

test('one chip per published port, IPv4 and IPv6 entries of Docker joined', () => {
  // what API.docker.list() gives for a container
  const ports = [
    { IP: '0.0.0.0', PrivatePort: 80, PublicPort: 8096, Type: 'tcp' },
    { IP: '::', PrivatePort: 80, PublicPort: 8096, Type: 'tcp' },
    { IP: '0.0.0.0', PrivatePort: 443, PublicPort: 8920, Type: 'tcp' },
    { IP: '::', PrivatePort: 443, PublicPort: 8920, Type: 'tcp' },
    { PrivatePort: 8096, Type: 'tcp' }, // declared by the image, not published
    { IP: '0.0.0.0', PrivatePort: 1900, PublicPort: 1900, Type: 'udp' },
  ];
  const chips = portChips(ports, httpsPanelOnIP);
  assert.deepEqual(chips.map((chip) => [chip.label, chip.href]), [
    ['1900:1900/udp', null],
    ['8096:80', 'http://192.168.1.10:8096'],
    ['8920:443', 'https://192.168.1.10:8920'],
  ]);
  assert.deepEqual(portChips(undefined, httpPanel), []);
});

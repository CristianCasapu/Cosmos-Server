// node --test client/src/utils/reachability.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { CHECK_TEXT, checkAddresses, checkSentence, portsText, publishedPorts, safeURL, summarize } from './reachability.js';

const r = (route, state, url = '') => ({ route, state, url, checks: [] });

test('summarize: the best of a container\'s routes and ports', () => {
  const results = [
    r('local', 'local'),
    r('ext', 'unverified', 'https://app.example.org:2053'),
    r('bad', 'problem'),
    r('other-app', 'verified', 'https://other.example.org'),
    r('off', 'off'),
    r('wl', 'limited'),
  ];
  const cases = [
    { name: 'no route, no port', in: { routeNames: [] }, state: 'none', url: '' },
    { name: 'local route only', in: { routeNames: ['local'] }, state: 'local', url: '' },
    { name: 'local route and a published port', in: { routeNames: ['local'], ports: [{ IP: '0.0.0.0', PublicPort: 8080, PrivatePort: 80, Type: 'tcp' }] }, state: 'ports' },
    { name: 'port on loopback only is not reachable', in: { routeNames: [], ports: [{ IP: '127.0.0.1', PublicPort: 8080, Type: 'tcp' }] }, state: 'none' },
    { name: 'host network', in: { routeNames: [], hostNetwork: true }, state: 'ports' },
    { name: 'problem only', in: { routeNames: ['local', 'bad'] }, state: 'problem', url: '' },
    { name: 'unverified beats problem', in: { routeNames: ['bad', 'ext', 'local'] }, state: 'unverified', url: 'https://app.example.org:2053' },
    { name: 'exposed route beats ports', in: { routeNames: ['ext'], ports: [{ IP: '', PublicPort: 1 }] }, state: 'unverified' },
    { name: 'restricted', in: { routeNames: ['wl', 'off'] }, state: 'limited' },
    { name: 'disabled', in: { routeNames: ['off'] }, state: 'off' },
    { name: 'a route the server did not report', in: { routeNames: ['gone'] }, state: 'none' },
  ];
  for (const c of cases) {
    const got = summarize({ results, ...c.in });
    assert.equal(got.state, c.state, c.name);
    if (c.url !== undefined) assert.equal(got.url, c.url, c.name);
  }
});

test('summarize: a stack combines its containers', () => {
  const results = [r('web', 'local'), r('api', 'verified', 'https://api.example.org')];
  const got = summarize({ routeNames: ['web', 'api'], results, ports: [] });
  assert.equal(got.state, 'verified');
  assert.equal(got.url, 'https://api.example.org');
  assert.equal(got.routes.length, 2);
});

test('publishedPorts: once per port, widest binding wins', () => {
  const got = publishedPorts([
    { IP: '0.0.0.0', PublicPort: 8080, PrivatePort: 80, Type: 'tcp' },
    { IP: '::', PublicPort: 8080, PrivatePort: 80, Type: 'tcp' },
    { IP: '127.0.0.1', PublicPort: 9000, PrivatePort: 9000, Type: 'tcp' },
    { IP: '0.0.0.0', PublicPort: 53, PrivatePort: 53, Type: 'udp' },
    { PrivatePort: 3000, Type: 'tcp' }, // exposed, not published
    null,
  ]);
  assert.deepEqual(got.map((p) => [p.port, p.type, p.scope]), [[53, 'udp', 'all'], [8080, 'tcp', 'all'], [9000, 'tcp', 'server']]);
  assert.equal(portsText(got), '53/udp, 8080');
});

test('every check the server can return has a sentence', () => {
  const go = readFileSync(new URL('../../../src/reach/state.go', import.meta.url), 'utf8');
  const codes = [...go.matchAll(/add\("(\w+)", Check\w+, (?:"(\w+)"|problem)/g)].filter((m) => m[2]).map((m) => m[1] + '.' + m[2]);
  // codes passed through: answer.Skipped and the certificate problems
  codes.push('dns.badName', 'dns.tooMany', 'cert.selfSigned', 'cert.wrongName', 'cert.expired', 'cert.none');
  assert.ok(codes.length > 25, 'found the codes in state.go');
  for (const code of codes) {
    assert.ok(CHECK_TEXT[code], 'no sentence for ' + code);
  }
});

test('checkSentence: key, fix link, parameters', () => {
  const s = checkSentence({ id: 'dns', status: 'problem', code: 'noRecord', params: { name: 'a.example.org' } }, 'app');
  assert.equal(s.key, 'mgmt.reach.check.dns.noRecord');
  assert.equal(s.fix, '/cosmos-ui/config-general/https');
  assert.equal(checkSentence({ id: 'access', code: 'disabled' }, 'my app/x').fix, '/cosmos-ui/config-url/my%20app%2Fx');
  assert.equal(checkSentence({ id: 'forward', code: 'byHand', params: { port: '2053' } }).params.port, '2053');
  assert.equal(checkSentence({ id: 'x', code: 'y' }).key, 'mgmt.reach.check.unknown');
});

test('checkAddresses: only what was sent', () => {
  assert.equal(checkAddresses({ params: { address: '1.2.3.4', publicIP: '5.6.7.8' } }), '1.2.3.4 ≠ 5.6.7.8');
  assert.equal(checkAddresses({ params: { address: '1.2.3.4' } }), '1.2.3.4');
  assert.equal(checkAddresses({}), '');
});

test('safeURL: only http(s)', () => {
  assert.equal(safeURL('https://app.example.org:2053/x'), 'https://app.example.org:2053/x');
  assert.equal(safeURL('http://a.b'), 'http://a.b');
  assert.equal(safeURL('javascript:alert(1)'), '');
  assert.equal(safeURL('https://a.b/"onmouseover'), '');
  assert.equal(safeURL(undefined), '');
});

test('portsFromInspect', async () => {
  const { portsFromInspect } = await import('./reachability.js');
  assert.deepEqual(portsFromInspect({ '80/tcp': [{ HostIp: '0.0.0.0', HostPort: '8080' }, { HostIp: '::', HostPort: '8080' }], '53/udp': null, '9/tcp': [{ HostIp: '', HostPort: '' }] }),
    [{ IP: '0.0.0.0', PublicPort: 8080, PrivatePort: 80, Type: 'tcp' }, { IP: '::', PublicPort: 8080, PrivatePort: 80, Type: 'tcp' }]);
  assert.deepEqual(portsFromInspect(undefined), []);
});

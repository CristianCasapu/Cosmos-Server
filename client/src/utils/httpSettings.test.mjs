// Fork addition: tests of httpSettings.js. Run with: node --test "client/src/utils/*.test.mjs"
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  portChoice, portsOf, portNumber, portProblems, portStateText, panelAddress, saveEffects,
  cleanList, isAddressEntry, isProxyEntry, invalidEntries, inRange, whitelistProposals, locksOut,
  hostnameProblem, hostnameProposals, ownCountriesRefused,
} from './httpSettings.js';

test('the usual pairs of ports are one choice; anything else is "other"', () => {
  assert.equal(portChoice('80', '443'), 'standard');
  assert.equal(portChoice(' 8080', '8443 '), 'alternate');
  assert.equal(portChoice('80', '8443'), 'other');
  assert.deepEqual(portsOf('alternate'), { value: 'alternate', http: '8080', https: '8443' });
  assert.equal(portsOf('other'), null);
});

test('a port is digits from 1 to 65535, and the two differ', () => {
  assert.equal(portNumber('8o80'), null); // upstream's own check lets this through
  assert.equal(portNumber('0'), null);
  assert.equal(portNumber('65536'), null);
  assert.equal(portNumber(' 443 '), 443);
  assert.deepEqual(Object.keys(portProblems('8o80', '443')), ['http']);
  assert.deepEqual(Object.keys(portProblems('8080', '8080')), ['https']);
  assert.deepEqual(portProblems('', ''), {}); // empty is not an error while typing
  assert.deepEqual(portProblems('80', '443'), {});
});

test('what the check found about a port reads as a sentence with the holder', () => {
  assert.equal(portStateText({ port: 8443, state: 'container', holder: 'nextcloud' }).severity, 'error');
  assert.equal(portStateText({ port: 8443, state: 'container', holder: 'nextcloud' }).text[2].name, 'nextcloud');
  assert.equal(portStateText({ port: 81, state: 'other' }).severity, 'error');
  assert.equal(portStateText({ port: 81, state: 'free' }).severity, 'success');
  assert.equal(portStateText({ state: 'invalid' }), null);
});

test('the panel address leaves out the usual port', () => {
  assert.equal(panelAddress({ hostname: 'cosmos.example.com', httpsPort: '443', https: true }), 'https://cosmos.example.com');
  assert.equal(panelAddress({ hostname: 'cosmos.example.com', httpsPort: '8443', https: true }), 'https://cosmos.example.com:8443');
  assert.equal(panelAddress({ hostname: '192.168.1.10', httpPort: '8080', https: false }), 'http://192.168.1.10:8080');
});

test('before saving: a new address, the restart, and a lock-out are said', () => {
  const before = { hostname: 'a.example.com', httpPort: '80', httpsPort: '443', https: true, whitelist: [], constellationOnly: false };
  const keys = (effects) => effects.map((e) => e.text[0]);
  assert.deepEqual(keys(saveEffects(before, before)), ['mgmt.config.http.fork.effectRestart']);
  const moved = saveEffects(before, { ...before, httpsPort: '8443' });
  assert.equal(moved[0].text[2].address, 'https://a.example.com:8443');
  const locked = saveEffects(before, { ...before, whitelist: ['10.0.0.0/8'] }, { you: { address: '192.168.1.5' }, whitelist: { passes: false } });
  assert.ok(keys(locked).includes('mgmt.config.http.fork.effectLockOut'));
  // an unchanged list never counts as a lock-out (the server does not check it either)
  assert.ok(!keys(saveEffects(before, before, { whitelist: { passes: false } })).includes('mgmt.config.http.fork.effectLockOut'));
});

test('address lists: entries are addresses or ranges; proxies may also be URLs', () => {
  assert.deepEqual(cleanList(' 10.0.0.1, ,192.168.1.0/24 '), ['10.0.0.1', '192.168.1.0/24']);
  assert.ok(isAddressEntry('192.168.1.0/24'));
  assert.ok(isAddressEntry('2001:db8::1'));
  assert.ok(isAddressEntry('2001:db8::/32'));
  assert.ok(!isAddressEntry('192.168.1.0/33'));
  assert.ok(!isAddressEntry('192.168.1.256'));
  assert.ok(!isAddressEntry('my router'));
  assert.ok(isProxyEntry('https://www.cloudflare.com/ips-v4'));
  assert.deepEqual(invalidEntries('10.0.0.1, nope', isAddressEntry), ['nope']);
});

test('ranges hold the addresses they should', () => {
  assert.ok(inRange('192.168.1.5', '192.168.1.0/24'));
  assert.ok(!inRange('192.168.2.5', '192.168.1.0/24'));
  assert.ok(inRange('192.168.1.5', '192.168.1.5'));
  assert.ok(inRange('10.1.2.3', '0.0.0.0/0'));
  assert.ok(!inRange('10.1.2.3', '10.1.2.4/32'));
});

test('proposals for the admin whitelist come from what the server saw', () => {
  const check = { you: { address: '192.168.1.5', throughProxy: false }, server: { addresses: [{ ip: '192.168.1.10', network: '192.168.1.0/24' }, { ip: '10.8.0.1', network: '10.8.0.0/24' }] } };
  const all = whitelistProposals(check, '');
  assert.deepEqual(all.map((p) => p.value), ['192.168.1.0/24', '192.168.1.5']);
  // already allowed: nothing more is proposed for it
  assert.deepEqual(whitelistProposals(check, '192.168.1.0/24').map((p) => p.value), []);
  // from outside the server's networks: only the address itself
  assert.deepEqual(whitelistProposals({ ...check, you: { address: '203.0.113.9' } }, '').map((p) => p.value), ['203.0.113.9']);
  assert.equal(locksOut({ whitelist: { passes: false } }), true);
  assert.equal(locksOut(null), false);
});

test('the server address: the checks of the server, in words', () => {
  assert.equal(hostnameProblem('cosmos.example.com'), null);
  assert.equal(hostnameProblem('192.168.1.10'), null);
  assert.equal(hostnameProblem('cosmos.example.com:8443'), null);
  assert.equal(hostnameProblem('https://cosmos.example.com')[0], 'mgmt.config.http.fork.hostnameScheme');
  assert.equal(hostnameProblem('cosmos.example.com/x')[0], 'mgmt.config.http.fork.hostnameChars');
  assert.equal(hostnameProblem('bad_name.example')[0], 'mgmt.config.http.fork.hostnameChars');
  assert.equal(hostnameProblem('a.example:99999')[0], 'mgmt.config.http.fork.hostnamePort');
  const proposals = hostnameProposals({ server: { addresses: [{ ip: '192.168.1.10', network: '192.168.1.0/24' }] } }, 'cosmos.home.example', '192.168.1.10');
  assert.deepEqual(proposals.map((p) => p.value), ['cosmos.home.example']);
});

test('geo-blocking: the country the admin is in, refused by the list, is named', () => {
  assert.deepEqual(ownCountriesRefused(['CN', 'RO'], false, ['RO']), ['RO']);
  assert.deepEqual(ownCountriesRefused(['FR'], true, ['RO']), ['RO']); // an allow list without it
  assert.deepEqual(ownCountriesRefused([], true, ['RO']), []); // an empty allow list allows everyone
  assert.deepEqual(ownCountriesRefused(['CN'], false, ['RO']), []);
});

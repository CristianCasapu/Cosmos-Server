import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  dockerNameProblem, subnetProblem, interfaceProblem, lanCards, freeName, marketUrlProblem, marketNameFromUrl, whitelistEntryProposals,
} from './smallForms.js';

const translations = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));

test('names: Docker\'s rule and the taken ones', () => {
  assert.equal(dockerNameProblem('media-net', ['other']), null);
  assert.equal(dockerNameProblem('', []), null);
  assert.equal(dockerNameProblem('bad name')[0], 'mgmt.servApps.fork.nameRule');
  assert.equal(dockerNameProblem('-x')[0], 'mgmt.servApps.fork.nameRule');
  assert.equal(dockerNameProblem('media', ['media'])[0], 'mgmt.servApps.fork.networkTaken');
  assert.equal(dockerNameProblem('media', ['media'], 'volume')[0], 'mgmt.servApps.fork.volumeTaken');
  assert.equal(freeName('data', ['data', 'data-2']), 'data-3');
  // Docker wants two characters at least for a volume
  assert.equal(dockerNameProblem('a', [], 'volume')[0], 'mgmt.servApps.fork.nameRule');
  assert.equal(dockerNameProblem('a', [], 'network'), null);
});

test('subnets and network cards', () => {
  assert.equal(subnetProblem(''), null);
  assert.equal(subnetProblem('172.30.0.0/24'), null);
  assert.equal(subnetProblem('172.30.0.5/24')[2].start, '172.30.0.0');
  assert.equal(subnetProblem('172.30.0.0')[0], 'mgmt.servApps.fork.subnetForm');
  assert.equal(subnetProblem('300.1.1.0/24')[0], 'mgmt.servApps.fork.subnetForm');
  assert.equal(interfaceProblem('eno1'), null);
  assert.ok(interfaceProblem(''));
  assert.ok(interfaceProblem('eth0; rm -rf'));
  assert.deepEqual(lanCards({ server: { addresses: [{ ip: '192.168.1.14', network: '192.168.1.0/24', interface: 'eno1' }, { ip: '192.168.1.15', network: '192.168.1.0/24', interface: 'eno1' }, { ip: 'fd00::1', network: 'fd00::/64', interface: 'eno1' }] } }).length, 1);
});

test('market sources', () => {
  assert.equal(marketUrlProblem('https://example.com/cosmos-servapps.json'), null);
  for (const bad of ['', 'example.com/x.json', 'file:///etc/passwd', 'https://u:p@example.com/x', 'ftp://example.com/x']) assert.ok(marketUrlProblem(bad), bad);
  assert.equal(marketNameFromUrl('https://www.selfhosted-apps.org/market.json'), 'selfhosted-apps');
  assert.equal(marketNameFromUrl('https://raw.githubusercontent.com/x/y/main/a.json'), 'githubusercontent');
  assert.equal(marketNameFromUrl('https://cosmos-cloud.io/x.json'), 'cosmos-cloud-2');
  assert.equal(marketNameFromUrl('not a url'), '');
});

test('whitelist proposals: the network you share with the server, then your address', () => {
  const check = { you: { address: '192.168.1.20', local: true }, server: { addresses: [{ ip: '192.168.1.14', network: '192.168.1.0/24' }] } };
  assert.deepEqual(whitelistEntryProposals(check, []).map((p) => p.IP), ['192.168.1.0/24', '192.168.1.20']);
  assert.deepEqual(whitelistEntryProposals(check, [{ IP: '192.168.1.0/24' }]).map((p) => p.IP), ['192.168.1.20']);
  assert.equal(whitelistEntryProposals({ you: { address: '203.0.113.9', local: false }, server: { addresses: [] } }, [])[0].source[0], 'mgmt.shield.fork.fromYou');
  assert.deepEqual(whitelistEntryProposals(null), []);
});

test('every text of the module is in the translation file', () => {
  const source = readFileSync(new URL('./smallForms.js', import.meta.url), 'utf8');
  const pairs = [...source.matchAll(/\['(mgmt\.[a-zA-Z]+\.fork\.[^']+)',\s*'((?:[^'\\]|\\.)*)'/g)];
  assert.ok(pairs.length >= 14);
  for (const [, key, text] of pairs) assert.equal(translations[key], text.replace(/\\(.)/g, '$1'), key);
});

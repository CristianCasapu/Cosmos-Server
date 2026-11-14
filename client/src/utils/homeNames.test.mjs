// node --test client/src/utils/homeNames.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  PROBLEM_CODES, activeSuffixes, allTexts, browserCheckText, certificateState, cleanLabel, exampleName,
  homeRoutesFor, isHomeName, probeURL, proposedHomeNames, rebindDomain, recordState, serverCheckText, validLabel,
} from './homeNames.js';
import { withHomeRoutes } from './appLinks.js';

const status = {
  zones: [
    { zone: 'example.org', enabled: true, active: true, label: 'home', suffix: 'home.example.org', wildcard: '*.home.example.org', panel: 'cosmos.home.example.org', record: 'ok', recordAddress: '192.168.1.20', certificateCovered: true, certificateSource: 'zone' },
    { zone: 'example.net', enabled: false, active: false, label: 'home', problem: 'records' },
  ],
  names: [
    { host: 'radarr.home.example.org', route: 'radarr', source: 'radarr.local', zone: 'example.org' },
    { host: 'sonarr.home.example.org', route: 'sonarr', source: 'sonarr.example.org', zone: 'example.org' },
    { host: 'sonarr.home.example.org', route: 'sonarr-api', source: 'sonarr.example.org', zone: 'example.org' },
  ],
};

test('labels are checked like the server checks them', () => {
  for (const ok of ['home', 'h', 'my-home', 'a'.repeat(63)]) assert.equal(validLabel(ok), true, ok);
  for (const bad of ['', 'Home', '-home', 'home-', 'ho_me', 'ho.me', 'a'.repeat(64), null]) assert.equal(validLabel(bad), false, String(bad));
  assert.equal(cleanLabel(' My Home '), 'my-home');
  assert.equal(exampleName('lan', 'example.org'), 'radarr.lan.example.org');
  assert.equal(exampleName('Bad_', 'example.org'), 'radarr.home.example.org');
});

test('an app\'s home names are copies of its routes', () => {
  const routes = [
    { Name: 'radarr', UseHost: true, Host: 'radarr.local' },
    { Name: 'radarr-example.org', UseHost: true, Host: 'radarr.example.org' },
  ];
  const home = homeRoutesFor(status, routes);
  assert.deepEqual(home.map((r) => [r.Name, r.Host, r.homeName]), [['radarr', 'radarr.home.example.org', true]]);
  // one chip per name, path prefixes or not
  const sonarr = homeRoutesFor(status, [
    { Name: 'sonarr', UseHost: true, Host: 'sonarr.example.org' },
    { Name: 'sonarr-api', UseHost: true, Host: 'sonarr.example.org', UsePathPrefix: true, PathPrefix: '/api' },
  ]);
  assert.equal(sonarr.length, 1);
  assert.deepEqual(homeRoutesFor(null, routes), []);
  // a route renamed since: no name
  assert.deepEqual(homeRoutesFor(status, [{ Name: 'radarr', UseHost: true, Host: 'other.local' }]), []);
});

test('home names lead the local group of the links', () => {
  const home = [{ Name: 'radarr', Host: 'radarr.home.example.org', homeName: true }];
  const groups = [{ local: true, routes: [{ Name: 'radarr', Host: 'radarr.local' }] }, { local: false, routes: [{ Name: 'x', Host: 'radarr.example.org' }] }];
  const merged = withHomeRoutes(groups, home);
  assert.deepEqual(merged[0].routes.map((r) => r.Host), ['radarr.home.example.org', 'radarr.local']);
  assert.equal(merged[1].routes.length, 1);
  // an app with an external address only gets a local group
  const only = withHomeRoutes([groups[1]], home);
  assert.equal(only[0].local, true);
  assert.equal(only.length, 2);
  assert.equal(withHomeRoutes(groups, []), groups);
});

test('suffixes, names and proposals', () => {
  assert.deepEqual(activeSuffixes(status), ['home.example.org']);
  assert.equal(isHomeName(status, 'Radarr.Home.Example.org:2053'), true);
  assert.equal(isHomeName(status, 'home.example.org'), false);
  assert.equal(isHomeName(status, 'radarr.example.org'), false);
  assert.deepEqual(proposedHomeNames(status, 'radarr.local'), ['radarr.home.example.org']);
  assert.deepEqual(proposedHomeNames(status, '192.168.1.5'), []);
  assert.deepEqual(proposedHomeNames(status, 'app.local:8443'), []);
  assert.deepEqual(proposedHomeNames(null, 'radarr'), []);
  assert.equal(rebindDomain(status.zones[0]), 'home.example.org');
});

test('states in words', () => {
  const row = status.zones[0];
  assert.equal(recordState(row)[0], 'success');
  assert.equal(recordState({ ...row, record: 'conflict' })[0], 'error');
  assert.equal(recordState({ ...row, record: 'pending' })[0], 'info');
  assert.equal(recordState(status.zones[1]), null);
  assert.equal(certificateState(row)[0], 'success');
  assert.equal(certificateState({ ...row, certificateSource: 'selfsigned' })[0], 'info');
  assert.equal(serverCheckText({ result: 'ok', name: 'cosmos.home.example.org' })[0], 'success');
  assert.equal(serverCheckText({ result: 'none', name: 'x' })[0], 'warning');
  assert.equal(browserCheckText('opened', 'x')[0], 'success');
  assert.equal(browserCheckText('failed', 'x')[0], 'warning');
  assert.equal(probeURL('cosmos.home.example.org', { protocol: 'https:', port: '2053' }), 'https://cosmos.home.example.org:2053/_health');
  assert.equal(probeURL('cosmos.home.example.org', { protocol: 'https:', port: '' }), 'https://cosmos.home.example.org/_health');
});

test('every problem the server can give has a sentence', () => {
  const server = readFileSync(new URL('../../../src/utils/fork_home_names.go', import.meta.url), 'utf8');
  const codes = [...server.matchAll(/HomeProblem\w+\s+=\s+"(\w+)"/g)].map((m) => m[1]);
  const route = readFileSync(new URL('../../../src/cloudflare/homenames.go', import.meta.url), 'utf8');
  codes.push(...[...route.matchAll(/reason = "(\w+)"/g)].map((m) => m[1]));
  assert.ok(codes.length >= 8);
  for (const code of codes) assert.ok(PROBLEM_CODES.includes(code), code);
});

test('every text is in the English translation file, word for word', () => {
  const file = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));
  const texts = allTexts();
  const pairsOf = (source) => [...source.matchAll(/\btr?\(\s*'([^']+)'\s*,\s*'((?:[^'\\]|\\.)*)'/gs)]
    .map((m) => [m[1], m[2].replace(/\\(.)/g, (whole, c) => (c === 'n' ? '\n' : c))]);
  for (const name of ['../components/homeNames.jsx', '../components/hostnameSuggestions.jsx', '../components/localAddressesForAll.jsx', '../components/appLinks.jsx', './activity.js']) {
    const source = readFileSync(new URL(name, import.meta.url), 'utf8');
    texts.push(...pairsOf(source).filter(([key]) => key.startsWith('mgmt.homeNames.') || key.startsWith('activity.homeNames.')));
  }
  assert.ok(texts.length > 50);
  for (const [key, english] of texts) {
    assert.equal(file[key], english, key);
  }
});

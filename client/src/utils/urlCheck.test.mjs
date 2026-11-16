// Fork addition (plan 44): tests of urlCheck.js (node --test).
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  attentionOf, envFields, envWith, fillValue, imageKey, knownAppFor, knownAppNeeds, orderedRepairs,
  noteWords, publicFacts, resultWords, routesOfContainer, sharingRouteRepair, whyWords,
} from './urlCheck.js';

test('image names as the server matches them', () => {
  assert.equal(imageKey('lscr.io/linuxserver/qbittorrent:latest'), 'linuxserver/qbittorrent');
  assert.equal(imageKey('ghcr.io/hotio/qbittorrent:release'), 'hotio/qbittorrent');
  assert.equal(imageKey('nextcloud:30'), 'nextcloud');
  assert.equal(imageKey('docker.io/library/nextcloud@sha256:abc'), 'nextcloud');
  assert.equal(imageKey('localhost:5000/me/app:1'), 'me/app');
  assert.equal(imageKey('registry.example.org:5000/app'), 'app');
  assert.equal(imageKey('qbittorrentofficial/qbittorrent-nox'), 'qbittorrentofficial/qbittorrent-nox');
  assert.equal(imageKey(''), '');
});

const apps = [
  { id: 'qbittorrent', name: 'qBittorrent', images: ['linuxserver/qbittorrent'], appOwnHost: true },
  { id: 'paperless', name: 'Paperless-ngx', images: ['paperless-ngx/paperless-ngx'], env: [
    { name: 'PAPERLESS_URL', value: '{url}', when: 'always', why: 'sign-in' },
    { name: 'PAPERLESS_CSRF_TRUSTED_ORIGINS', value: '{urls}', when: 'always' },
    { name: 'OTHER', value: '{host}', when: 'refused' },
  ] },
];

test('the known app of an image', () => {
  assert.equal(knownAppFor(apps, 'lscr.io/linuxserver/qbittorrent:5.3.0').id, 'qbittorrent');
  assert.equal(knownAppFor(apps, 'nginx'), null);
  assert.equal(knownAppFor(null, 'x'), null);
});

test('the address with its port, and the values filled in', () => {
  const home = publicFacts('Paper.server.local', 2053);
  assert.deepEqual(home, { name: 'paper.server.local', port: '2053', host: 'paper.server.local:2053', url: 'https://paper.server.local:2053' });
  assert.equal(publicFacts('a.example.org', 443).url, 'https://a.example.org');
  const domain = publicFacts('paper.example.org', 2053);
  assert.equal(fillValue('{urls}', home, [domain]), 'https://paper.server.local:2053,https://paper.example.org:2053');
  assert.equal(fillValue('{name}:{port}', home), 'paper.server.local:2053');
  const needs = knownAppNeeds(apps[1], home);
  assert.deepEqual(needs.env.map((e) => [e.name, e.value]), [['PAPERLESS_URL', 'https://paper.server.local:2053'], ['PAPERLESS_CSRF_TRUSTED_ORIGINS', 'https://paper.server.local:2053']]);
  assert.equal(needs.appOwnHost, false);
  assert.equal(knownAppNeeds(apps[0], home).appOwnHost, true);
  assert.equal(knownAppNeeds(null, home), null);
});

const qbt = {
  route: 'qbt-local', url: 'https://qbittorrent.server.local:2053', container: 'qbittorrent', appName: 'qBittorrent',
  verdict: 'refused', reason: 'hostPort', line: 'Unauthorized',
  attempts: [{ status: 401, host: 'qbittorrent.server.local:2053' }, { status: 200, appOwnHost: true }],
  repairs: [{ kind: 'file', path: '/config/qBittorrent/qBittorrent.conf', lines: ['WebUI\\ReverseProxySupportEnabled=true'], when: 'advice' }, { kind: 'route', setting: 'appOwnHost', on: true, seen: true }],
};

test('the words of a result', () => {
  const w = resultWords(qbt);
  assert.equal(w.severity, 'error');
  assert.equal(w.key, 'mgmt.urlCheck.reason.hostPort');
  assert.equal(w.params.app, 'qBittorrent');
  assert.equal(resultWords({ verdict: 'works', url: 'https://a' }).key, 'mgmt.urlCheck.reason.works');
  assert.equal(resultWords({ verdict: 'answers', reason: 'sameAnswer', attempts: [{ status: 401 }] }).params.status, 401);
  assert.equal(resultWords({ verdict: 'unreachable', reason: 'refused' }).key, 'mgmt.urlCheck.reason.portRefused');
  assert.equal(resultWords({ verdict: 'notRunning', reason: 'hostNetwork' }).key, 'mgmt.urlCheck.reason.hostNetwork');
  assert.equal(resultWords({ verdict: 'refused', reason: 'somethingNew' }).key, 'mgmt.urlCheck.reason.refused');
  assert.equal(resultWords({ verdict: 'warning', reason: 'csrfOrigin' }).severity, 'warning');
  assert.equal(resultWords(null).severity, 'info');
});

test('attention, repairs in order, shared repairs', () => {
  assert.equal(attentionOf(qbt), 'error');
  assert.equal(attentionOf({ verdict: 'warning' }), 'warning');
  assert.equal(attentionOf({ verdict: 'works' }), null);
  const r = orderedRepairs(qbt);
  assert.equal(r.route.setting, 'appOwnHost');
  assert.equal(r.route.on, true);
  assert.equal(r.advice.length, 1);
  assert.equal(r.file.length, 0);
  const more = orderedRepairs({ repairs: [
    { kind: 'route', setting: 'noVisitorAddress', on: true, seen: false },
    { kind: 'route', setting: 'appOwnHost', on: true, seen: true },
    { kind: 'route', setting: 'somethingElse', on: true, seen: true },
    { kind: 'env', name: 'A', when: 'advice' }, { kind: 'env', name: 'B', when: 'always' },
    { kind: 'command', lines: ['php occ x'], user: 'www-data', when: 'refused' },
  ] });
  assert.equal(more.route.setting, 'appOwnHost');
  assert.deepEqual(more.env.map((e) => e.name), ['B']);
  assert.deepEqual(more.advice.map((e) => e.name), ['A']);
  assert.equal(more.file[0].kind, 'command');
  const other = { ...qbt, route: 'qbt-ext', url: 'https://qbittorrent.example.org:2053' };
  const elsewhere = { ...qbt, route: 'x', container: 'other' };
  const works = { ...qbt, route: 'y', verdict: 'works', repairs: [] };
  assert.deepEqual(sharingRouteRepair({ a: qbt, b: other, c: elsewhere, d: works }, qbt), ['qbt-local', 'qbt-ext']);
  assert.deepEqual(sharingRouteRepair([], { ...qbt, repairs: [] }), []);
  const ha = { ...other, route: 'ha', repairs: [{ kind: 'route', setting: 'noVisitorAddress', on: true, seen: true }] };
  assert.deepEqual(sharingRouteRepair({ a: qbt, h: ha }, qbt), ['qbt-local'], 'another setting is not the same repair');
});

test('why ids, notes and new reasons in words', () => {
  assert.equal(whyWords('visitorAddressQbt').key, 'mgmt.urlCheck.why.visitorAddressQbt');
  assert.equal(whyWords('free text').text, 'free text');
  assert.equal(whyWords(''), null);
  const notes = noteWords({ notes: ['haTrustProxy', 'unknown'], proxy: '172.30.0.1' });
  assert.equal(notes.length, 1);
  assert.equal(notes[0].params.proxy, '172.30.0.1');
  assert.equal(resultWords({ verdict: 'refused', reason: 'noVisitorBreaks' }).key, 'mgmt.urlCheck.reason.noVisitorBreaks');
  assert.equal(resultWords({ verdict: 'warning', reason: 'httpLinks' }).key, 'mgmt.urlCheck.reason.httpLinks');
  assert.equal(resultWords({ verdict: 'unreachable', reason: 'portRefused' }).key, 'mgmt.urlCheck.reason.portRefused');
});

test('the env repair sets only the named variables', () => {
  const fields = envFields([{ kind: 'env', name: 'PAPERLESS_URL', value: 'https://p:2053', now: '' }, { kind: 'route' }]);
  assert.deepEqual(fields, [{ name: 'PAPERLESS_URL', value: 'https://p:2053', now: '', why: '' }]);
  assert.deepEqual(envWith(['A=1', 'PAPERLESS_URL=old'], fields), ['A=1', 'PAPERLESS_URL=https://p:2053']);
  assert.deepEqual(envWith(['A=1'], [{ name: 'B', value: 'x=y' }]), ['A=1', 'B=x=y']);
  assert.deepEqual(envWith(['A=1'], [{ name: 'bad name', value: 'x' }]), ['A=1']);
});

test('the URLs of a container', () => {
  const routes = [
    { Name: 'a', Mode: 'SERVAPP', Target: 'http://qbittorrent:8080' },
    { Name: 'b', Mode: 'SERVAPP', Target: 'http://qbittorrent2:8080' },
    { Name: 'c', Mode: 'PROXY', Target: 'http://qbittorrent:8080' },
  ];
  assert.deepEqual(routesOfContainer(routes, 'qbittorrent').map((r) => r.Name), ['a']);
});

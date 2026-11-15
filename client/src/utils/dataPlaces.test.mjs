// Fork addition: tests of dataPlaces.js and sensitiveAccess.js. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  applyPlaces, dataVolumes, finalPlace, proposeDefaults, proposePlace, proposePlaces, reviewSentences, sizeText, TEXT, text, volumeKind,
} from './dataPlaces.js';
import { sensitiveAccess, sensitiveBind, TEXT as SENSITIVE_TEXT, sentence } from './sensitiveAccess.js';

const TB = 1e12;
// like the owner's server: data path on a data disk, Docker's root on a data disk
const homeServer = {
  dataPath: '/storage/cosmos',
  places: [
    { path: '/storage/cosmos', kind: 'data-path', osDisk: false, disk: '/storage', free: 5.4 * TB },
    { path: '/archive', kind: 'disk', osDisk: false, disk: '/archive', free: 8.6 * TB },
    { path: '/storage', kind: 'disk', osDisk: false, disk: '/storage', free: 5.4 * TB },
    { path: '/storage/docker', kind: 'docker-root', osDisk: false, disk: '/storage', free: 5.4 * TB },
    { path: '/', kind: 'system', osDisk: true, disk: '/', free: 0.8 * TB },
  ],
};
// a fresh install: everything on the system disk, one data disk mounted
const freshInstall = {
  dataPath: '/cosmos-storage',
  places: [
    { path: '/cosmos-storage', kind: 'data-path', osDisk: true, disk: '/', free: 0.2 * TB },
    { path: '/mnt/data', kind: 'disk', osDisk: false, disk: '/mnt/data', free: 4 * TB },
    { path: '/var/lib/docker', kind: 'docker-root', osDisk: true, disk: '/', free: 0.2 * TB },
    { path: '/', kind: 'system', osDisk: true, disk: '/', free: 0.2 * TB },
  ],
};
const noDisk = { dataPath: '/cosmos-storage', places: freshInstall.places.filter((p) => p.kind !== 'disk') };

const jellyfin = {
  services: {
    jellyfin: {
      image: 'jellyfin/jellyfin',
      volumes: [
        { type: 'bind', source: '/storage/cosmos/jellyfin/config', target: '/config' },
        { type: 'volume', source: 'jellyfin-cache', target: '/cache' },
        { type: 'bind', source: '/storage/cosmos/jellyfin/media', target: '/media' },
        { type: 'bind', source: '/etc/localtime', target: '/etc/localtime', ReadOnly: true },
        { type: 'tmpfs', target: '/run' },
      ],
    },
  },
  volumes: { 'jellyfin-cache': {} },
};

test('config or bulk, from the folder in the container', () => {
  for (const target of ['/media', '/movies', '/tv', '/downloads', '/data/media', '/photos', '/music/', '/books']) {
    assert.equal(volumeKind(target), 'bulk', target);
  }
  for (const target of ['/config', '/data', '/var/lib/postgresql/data', '/app/data', '/cache', '/etc/app', '/mediaconfig']) {
    assert.equal(volumeKind(target), 'config', target);
  }
});

test('the data folders of a definition, without system files and tmpfs', () => {
  const list = dataVolumes(jellyfin, ['jellyfin-cache']);
  assert.deepEqual(list.map((v) => v.key), ['jellyfin|/config', 'jellyfin|/cache', 'jellyfin|/media']);
  assert.equal(list[1].frozen, true);
  assert.equal(list[2].kind, 'bulk');
});

test('on the owner\'s server: config stays, the cache volume stays, media in the data path stays on its data disk', () => {
  const list = dataVolumes(jellyfin);
  const proposals = proposePlaces(list, homeServer, 'jellyfin');
  assert.deepEqual(Object.values(proposals).map((p) => p.keep ? 'keep:' + p.reason : p.path), ['keep:fine', 'keep:fine', 'keep:fine']);
});

test('bulk data in a Docker volume goes to the data disk with the most free space', () => {
  const volume = { key: 'a|/movies', service: 'a', index: 0, type: 'volume', source: 'movies', target: '/movies', kind: 'bulk', frozen: false };
  assert.deepEqual(proposePlace(volume, homeServer, 'radarr'), { path: '/archive/radarr/movies', reason: 'bulk' });
});

test('a fresh install never proposes the system disk while a data disk exists', () => {
  const list = dataVolumes({ services: { app: { image: 'x', volumes: [
    { type: 'bind', source: '/cosmos-storage/app/config', target: '/config' },
    { type: 'volume', source: 'app-db', target: '/var/lib/mysql' },
    { type: 'bind', source: '/cosmos-storage/app/media', target: '/media' },
  ] } } });
  const proposals = proposePlaces(list, freshInstall, 'app');
  assert.deepEqual(Object.values(proposals).map((p) => p.path), [
    '/mnt/data/cosmos-storage/app/config', '/mnt/data/cosmos-storage/app/mysql', '/mnt/data/app/media',
  ]);
  // a folder the file names outside the data path stays, said when it is on the system disk
  assert.deepEqual(proposePlace({ key: 'a|/x', service: 'a', type: 'bind', source: '/srv/x', target: '/x', kind: 'config' }, freshInstall, 'app'), { keep: true, reason: 'systemAsked' });
  assert.deepEqual(proposePlace({ key: 'a|/m', service: 'a', type: 'bind', source: '/archive/movies', target: '/movies', kind: 'bulk' }, homeServer, 'app'), { keep: true, reason: 'fine' });
  // without a data disk nothing moves
  assert.deepEqual(Object.values(proposePlaces(list, noDisk, 'app')).map((p) => p.reason), ['noDisk', 'noDisk', 'noDisk']);
  // a frozen volume never moves
  assert.equal(proposePlace({ ...list[1], frozen: true }, freshInstall, 'app').keep, true);
});

test('the chosen folders go into the definition; a named volume nothing uses is not created', () => {
  const definition = { services: { app: { image: 'x', volumes: [
    { type: 'volume', source: 'app-db', target: '/var/lib/mysql', VolumeOptions: { NoCopy: true } },
    { type: 'bind', source: '/a', target: '/media', ReadOnly: true },
  ] } }, volumes: { 'app-db': {}, other: {} } };
  const list = dataVolumes(definition);
  const result = applyPlaces(definition, list, { 'app|/var/lib/mysql': { path: '/mnt/data/app/mysql' }, 'app|/media': { keep: true } });
  assert.deepEqual(result.services.app.volumes, [
    { type: 'bind', source: '/mnt/data/app/mysql', target: '/var/lib/mysql' },
    { type: 'bind', source: '/a', target: '/media', ReadOnly: true },
  ]);
  assert.deepEqual(result.volumes, { other: {} });
  // the original is untouched
  assert.equal(definition.services.app.volumes[0].type, 'volume');
  assert.deepEqual(finalPlace(list[0], { path: '/x' }), { type: 'bind', path: '/x' });
  assert.deepEqual(finalPlace(list[0], { keep: true }), { type: 'volume', path: 'app-db' });
});

test('defaults the server can decide: restart, time zone, the user of linuxserver images', () => {
  const { definition, proposed } = proposeDefaults({ services: {
    radarr: { image: 'lscr.io/linuxserver/radarr:latest', environment: ['TZ='] },
    db: { image: 'postgres:16', restart: 'always', environment: ['POSTGRES_PASSWORD=x'] },
    own: { image: 'linuxserver/x', restart: 'no', user: '1000', environment: ['PUID=99'] },
  } }, { dataOwner: '1001:100' });
  assert.deepEqual(definition.services.radarr.environment, ['TZ=auto', 'PUID=1001', 'PGID=100']);
  assert.equal(definition.services.radarr.restart, 'unless-stopped');
  assert.deepEqual(definition.services.db.environment, ['POSTGRES_PASSWORD=x']);
  assert.equal(definition.services.db.restart, 'always');
  assert.deepEqual(definition.services.own.environment, ['PUID=99', 'TZ=auto']);
  assert.deepEqual(proposed.map((p) => p.service + ':' + p.code), ['radarr:restart', 'radarr:tz', 'radarr:puid', 'radarr:pgid', 'own:tz']);
  assert.equal(proposeDefaults({ services: { a: { image: 'lscr.io/linuxserver/a' } } }, {}).definition.services.a.environment.includes('PUID=1000'), true);
});

test('the review in words', () => {
  const lines = reviewSentences({ services: {
    web: { container_name: 'web', image: 'nginx', ports: ['8080:80', '127.0.0.1:53:53/udp'], restart: 'unless-stopped', routes: [{ host: 'web.local' }] },
  } });
  assert.deepEqual(lines.map((l) => l.params.list || l.params.policy), ['web (nginx)', '8080 → 80 (web), 127.0.0.1:53 → 53/udp (web)', 'web.local', 'unless-stopped']);
  assert.equal(reviewSentences({ services: { a: { image: 'x' } } })[1].key, 'mgmt.servapps.creation.review.noPorts');
  assert.equal(sizeText(5.4e12), '5.4 TB');
  assert.equal(sizeText(830e9), '830 GB');
});

test('sensitive binds follow the same cases as the server', () => {
  const cases = JSON.parse(readFileSync(new URL('../../../src/docker/testdata/sensitive_cases.json', import.meta.url), 'utf8'));
  for (const [source, want] of Object.entries(cases.binds)) {
    assert.equal(sensitiveBind(source, cases.extra), want, source);
  }
});

test('sensitive access of a definition, named as the server names it', () => {
  const items = sensitiveAccess({ services: { portainer: {
    image: 'portainer',
    volumes: [{ type: 'bind', source: '/var/run/docker.sock', target: '/var/run/docker.sock' }, { type: 'bind', source: '/storage/cosmos/portainer', target: '/data' }, { type: 'volume', source: 'etc', target: '/etc' }],
    privileged: true, cap_add: ['CAP_NET_ADMIN', 'CHOWN'], labels: { 'cosmos-force-network-mode': 'host' }, security_opt: ['seccomp:unconfined'],
  } } });
  assert.deepEqual(items.map((i) => i.id), [
    'portainer|socket|/var/run/docker.sock', 'portainer|privileged|true', 'portainer|network-host|host', 'portainer|cap|NET_ADMIN', 'portainer|unconfined|seccomp:unconfined',
  ]);
});

test('a device of the server needs a tick (plan 40)', () => {
  const items = sensitiveAccess({ services: { jellyfin: { image: 'jellyfin', devices: ['/dev/dri:/dev/dri', '/dev/sda:/dev/xvda:rwm'] } } });
  assert.deepEqual(items.map((i) => i.id), ['jellyfin|device|/dev/dri', 'jellyfin|device|/dev/sda']);
});

test('every sentence is in the English translation file, word for word', () => {
  const translations = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));
  for (const code of Object.keys(TEXT)) {
    assert.equal(translations[text(code).key], TEXT[code], text(code).key);
  }
  for (const kind of Object.keys(SENSITIVE_TEXT)) {
    const s = sentence({ kind, service: 'a', value: 'b' });
    assert.equal(translations[s.key], s.text, s.key);
  }
});

import { marketArchFits } from './dataPlaces.js';

test('the Market\'s architecture list against the server\'s', () => {
  assert.equal(marketArchFits(['amd64', 'arm64'], 'amd64'), true);
  assert.equal(marketArchFits(['arm64'], 'amd64'), false);
  assert.equal(marketArchFits(['linux/arm/v7'], 'arm'), true);
  assert.equal(marketArchFits(['armv7'], 'arm'), true);
  assert.equal(marketArchFits([], 'amd64'), true);
  assert.equal(marketArchFits(undefined, 'amd64'), true);
});

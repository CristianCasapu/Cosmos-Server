// node --test client/src/utils/cloudflare.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  CANNOT_SHIELD, FAILURE, MISMATCH, PREVIEW_ACTION, RECORD_TEXTS, TOKEN_STATUS,
  cannotShieldText, failureText, hostEntry, looksLikeMediaServer, panelChoices, panelComesThroughCloudflare, previewActionText,
  previewCounts, proposePanel, recordText, replaceable, shieldMismatch, sslModeText, tokenFormat, tokenStatusText, validPanelHost,
  withPort, zoneAccessText, zoneSettings,
} from './cloudflare.js';
import { routeLink } from './appLinks.js';

test('tokenFormat: what a pasted text is (as the server decides it)', () => {
  const cases = {
    ['cfut_' + 'a1B2'.repeat(10) + 'deadbeef']: 'token',
    ['abcdefghij0123456789ABCDEFGHIJ0123456789']: 'token',
    ['cfat_' + 'a1B2'.repeat(10) + 'deadbeef']: 'token',
    ['cfk_' + '0a'.repeat(20) + '1234']: 'global_key',
    ['0123456789abcdef'.repeat(2) + '01234']: 'global_key',
    ['v1.0-' + 'ab'.repeat(40)]: 'origin_ca_key',
    ['cfoat_' + 'x'.repeat(44)]: 'oauth',
    ['']: 'malformed',
    ['short']: 'malformed',
    ['with spaces 0123456789 0123456789 0123456789']: 'malformed',
    ['a'.repeat(201)]: 'malformed',
  };
  for (const [token, want] of Object.entries(cases)) {
    assert.equal(tokenFormat(token), want, token.slice(0, 20));
  }
  assert.equal(tokenFormat('  abcdefghij0123456789ABCDEFGHIJ0123456789\n'), 'token', 'spaces around a pasted token are not part of it');
});

test('every status and reason has a sentence, unknown ones a plain fallback', () => {
  for (const status of ['ok', 'expired', 'disabled', 'invalid', 'malformed', 'global_key', 'origin_ca_key', 'oauth', 'no_zones', 'unreachable', 'rate_limited', 'error']) {
    assert.ok(tokenStatusText(status)[2].length > 20, status);
  }
  assert.equal(tokenStatusText('something new'), TOKEN_STATUS.error);
  assert.equal(tokenStatusText('global_key')[0], 'error');
  assert.match(tokenStatusText('global_key')[2], /Global API Key/);
  assert.match(tokenStatusText('no_zones')[2], /Zone > Zone > Read/);

  for (const reason of ['invalid_token', 'malformed_token', 'forbidden', 'not_found', 'rate_limited', 'unreachable', 'server', 'other']) {
    assert.ok(failureText(reason)[1].length > 20, reason);
  }
  assert.equal(failureText('<script>'), FAILURE.other);
  for (const code of ['not_served', 'not_cloudflare', 'no_token', 'wildcard_record', 'http_only', 'own_port', 'port', 'private_address']) {
    assert.ok(cannotShieldText(code)[1].length > 20, code);
  }
  assert.match(cannotShieldText('whatever')[1], /cannot be served through Cloudflare/);
});

test('no sentence suggests opening port 80 or 443', () => {
  const all = [
    ...Object.values(TOKEN_STATUS).map((entry) => entry[2]),
    ...Object.values(FAILURE).map((entry) => entry[1]),
    ...Object.values(CANNOT_SHIELD).map((entry) => entry[1]),
    ...Object.values(MISMATCH).map((entry) => entry[1]),
    ...RECORD_TEXTS().map((entry) => entry.text),
  ];
  for (const text of all) {
    assert.doesNotMatch(text, /\b(open|forward)\b[^.]*\bport (80|443)\b/i, text);
  }
});

test('zoneAccessText: what the token may do in the picked domain', () => {
  assert.equal(zoneAccessText({ canRead: 'yes', canEdit: 'yes' })[0], 'success');
  assert.equal(zoneAccessText({ canRead: 'yes', canEdit: 'no' })[0], 'error');
  assert.equal(zoneAccessText({ canRead: 'no', canEdit: 'unknown' })[0], 'error');
  assert.equal(zoneAccessText({ canRead: 'yes', canEdit: 'unknown' })[0], 'info');
  assert.equal(zoneAccessText(null)[2], '');
});

test('recordText: the state of an address at Cloudflare, in words', () => {
  const host = (record, extra = {}) => ({ host: 'radarr.example.org', managed: true, shielded: false, record, ...extra });
  assert.equal(recordText(null).level, 'none');
  assert.equal(recordText(host(null)).key, 'mgmt.cloudflare.record.byHand');
  assert.equal(recordText(host({ state: 'ok', proxied: true })).key, 'mgmt.cloudflare.record.proxied');
  assert.equal(recordText(host({ state: 'ok', proxied: false })).key, 'mgmt.cloudflare.record.direct');
  assert.equal(recordText(host({ state: 'pending' })).level, 'pending');
  assert.equal(recordText(host({ state: 'something new' })).level, 'pending');

  // the layout of the owner's server: the domain's own name, proxied, pointing here, never touched
  const apex = recordText(host({ state: 'protected', seen: true, proxied: true, pointsHere: true }));
  assert.equal(apex.level, 'ok');
  assert.match(apex.text, /does not touch it/);
  assert.equal(recordText(host({ state: 'protected', seen: true, proxied: true, pointsHere: false })).level, 'note');
  assert.equal(recordText(host({ state: 'protected', seen: true, proxied: false, pointsHere: true })).key, 'mgmt.cloudflare.record.apexHereDnsOnly');
  assert.equal(recordText(host({ state: 'protected', seen: false })).key, 'mgmt.cloudflare.record.apexNone');
  assert.match(recordText(host({ state: 'conflict', seen: true, proxied: true })).text, /Cosmos did not create it/);
  assert.equal(recordText(host({ state: 'conflict', seen: true, proxied: false })).key, 'mgmt.cloudflare.record.foreignDnsOnly');

  const failed = recordText(host({ state: 'error', reason: 'invalid_token', message: 'Invalid API Token' }));
  assert.equal(failed.level, 'problem');
  assert.equal(failed.detail, 'Invalid API Token');
  assert.equal(recordText(host({ state: 'error', reason: 'nonsense' })).text, FAILURE.other[1]);
});

test('shieldMismatch: only for a record Cosmos does not set, and only when it is known', () => {
  const host = (shielded, record, managed = false) => ({ shielded, managed, record });
  assert.equal(shieldMismatch(host(true, { state: 'protected', seen: true, proxied: true })), '');
  assert.equal(shieldMismatch(host(true, { state: 'protected', seen: true, proxied: false })), 'expectsProxy');
  assert.equal(shieldMismatch(host(false, { state: 'protected', seen: true, proxied: true })), 'expectsDirect');
  assert.equal(shieldMismatch(host(false, { state: 'conflict', seen: true, proxied: false })), '');
  assert.equal(shieldMismatch(host(false, { state: 'protected', seen: false, proxied: false })), '', 'nothing was read at Cloudflare');
  assert.equal(shieldMismatch(host(true, { state: 'ok', seen: true, proxied: false }, true)), '', 'Cosmos corrects a record it manages itself');
  assert.equal(shieldMismatch(host(true, null)), '');
  assert.equal(shieldMismatch(null), '');
});

test('looksLikeMediaServer: by the name of the image', () => {
  for (const image of ['jellyfin/jellyfin:latest', 'lscr.io/linuxserver/plex', 'emby/embyserver:4.8', 'deluan/navidrome', 'ghcr.io/hotio/plex:release',
    'docker.io/jellyfin/jellyfin@sha256:abc', 'plexinc/pms-docker', 'ghcr.io/immich-app/immich-server:release', 'linuxserver/emby', 'advplyr/audiobookshelf']) {
    assert.equal(looksLikeMediaServer(image), true, image);
  }
  for (const image of ['', null, 'linuxserver/radarr', 'nginx:alpine', 'vaultwarden/server', 'postgres:16', 'jellyseerr/jellyseerr', 'plex.example.org/tools/backup',
    'ghcr.io/home-assistant/home-assistant', 'complexity/app', 'linuxserver/sonarr:plexpass']) {
    assert.equal(looksLikeMediaServer(image), false, String(image));
  }
});

test('proposePanel: the address proposed for the panel, and why', () => {
  const apexHere = { apex: { exists: true, proxied: true, pointsHere: true } };
  // the domain itself already goes through Cloudflare to this server: no record is needed
  assert.deepEqual(proposePanel('example.org', apexHere, '192.168.1.10'), { host: 'example.org', source: 'apex' });
  // the domain's own name leads elsewhere (a website on another address): a name of its own
  assert.deepEqual(proposePanel('example.org', { apex: { exists: true, proxied: true, pointsHere: false } }, '192.168.1.10'), { host: 'cosmos.example.org', source: 'default' });
  assert.deepEqual(proposePanel('example.org', { apex: { exists: true, proxied: false, pointsHere: true } }, '192.168.1.10'), { host: 'cosmos.example.org', source: 'default' });
  assert.deepEqual(proposePanel('example.org', null, ''), { host: 'cosmos.example.org', source: 'default' });
  // what is already set is never overwritten by a proposal
  assert.deepEqual(proposePanel('example.org', apexHere, 'panel.example.org:2053'), { host: 'panel.example.org', source: 'current' });
  assert.deepEqual(proposePanel('example.org', null, 'example.org'), { host: 'example.org', source: 'current' });
  assert.deepEqual(proposePanel('example.org', apexHere, 'cosmos.notexample.org'), { host: 'example.org', source: 'apex' });
  assert.deepEqual(proposePanel('', apexHere, 'x'), { host: '', source: 'none' });

  assert.deepEqual(panelChoices('example.org', apexHere, '192.168.1.10'), ['example.org', 'cosmos.example.org']);
  assert.deepEqual(panelChoices('example.org', null, '192.168.1.10'), ['cosmos.example.org', 'example.org']);
  assert.deepEqual(panelChoices('example.org', null, 'panel.example.org'), ['panel.example.org', 'cosmos.example.org', 'example.org']);
  assert.deepEqual(panelChoices('', null, ''), []);
});

test('validPanelHost: the domain or a name under it', () => {
  for (const host of ['example.org', 'cosmos.example.org', 'a.b.example.org', 'COSMOS.Example.org ']) {
    assert.equal(validPanelHost(host, 'example.org'), true, host);
  }
  for (const host of ['', 'cosmos', 'cosmos.example.com', 'notexample.org', 'cosmos.example.org:2053', 'https://cosmos.example.org', 'co smos.example.org',
    '-a.example.org', 'a..example.org', 'cosmos.example.org/x', 'x'.repeat(64) + '.example.org']) {
    assert.equal(validPanelHost(host, 'example.org'), false, host);
  }
});

test('the preview: counts, what can be replaced, the panel through Cloudflare', () => {
  const preview = {
    rows: [
      { name: 'example.org', role: 'panel', action: 'protected', proxied: true, pointsHere: true },
      { name: 'radarr.example.org', role: 'app', action: 'create' },
      { name: 'sonarr.example.org', role: 'app', action: 'update' },
      { name: 'www.example.org', role: 'app', action: 'conflict', proxied: true },
      { name: 'old.example.org', role: 'app', action: 'remove' },
      { name: 'ok.example.org', role: 'app', action: 'keep' },
    ],
    untouched: [{}, {}, {}],
  };
  assert.deepEqual(previewCounts(preview), { written: 2, replaced: 0, removed: 1, left: 6 });
  assert.deepEqual(replaceable(preview), ['www.example.org']);
  assert.equal(panelComesThroughCloudflare(preview), true);
  assert.equal(panelComesThroughCloudflare({ rows: [{ role: 'panel', action: 'create' }] }), false);
  assert.equal(panelComesThroughCloudflare({ rows: [{ role: 'panel', action: 'protected', proxied: false }] }), false);
  assert.equal(panelComesThroughCloudflare({ rows: [{ role: 'app', action: 'conflict', proxied: true }] }), false);
  assert.deepEqual(previewCounts(null), { written: 0, replaced: 0, removed: 0, left: 0 });
  for (const action of Object.keys(PREVIEW_ACTION)) {
    assert.ok(previewActionText(action)[1].length > 5, action);
  }
  assert.equal(previewActionText('unknown')[2], 'default');
});

test('zoneSettings: the defaults of a connected domain, and what an existing one keeps', () => {
  const fresh = zoneSettings('the-token');
  assert.deepEqual(fresh, {
    HTTPSCertificateMode: 'LETSENCRYPT',
    DNSChallengeProvider: 'cloudflare',
    DNSChallengeConfig: { CF_DNS_API_TOKEN: 'the-token' },
    DNSChallengeResolvers: '',
    DNSChallengePropagationWait: 0,
    DisablePropagationChecks: false,
    UseWildcardCertificate: true,
    ManageRecords: true,
    WildcardRecord: false,
  });
  assert.equal(zoneSettings('t', { manageRecords: false, wildcardRecord: true }).WildcardRecord, false, 'no wildcard record without managed records');
  assert.equal(zoneSettings('t', { manageRecords: true, wildcardRecord: true, wildcardCertificate: false }).WildcardRecord, true);
  assert.equal(zoneSettings('t', { wildcardCertificate: false }).UseWildcardCertificate, false);

  const kept = zoneSettings('***', { manageRecords: true }, {
    DNSChallengeProvider: 'cloudflare', DNSChallengeResolvers: '1.1.1.1:53', DNSChallengePropagationWait: 30, DisablePropagationChecks: true,
    DNSChallengeConfig: { CF_DNS_API_TOKEN: '***', CF_ZONE_API_TOKEN: '***' },
  });
  assert.equal(kept.DNSChallengeResolvers, '1.1.1.1:53');
  assert.equal(kept.DNSChallengePropagationWait, 30);
  assert.equal(kept.DisablePropagationChecks, true);
  assert.deepEqual(kept.DNSChallengeConfig, { CF_DNS_API_TOKEN: '***', CF_ZONE_API_TOKEN: '***' });
  // a domain that was on another provider starts clean
  assert.deepEqual(zoneSettings('t', null, { DNSChallengeProvider: 'ovh', DNSChallengeConfig: { OVH_ENDPOINT: 'x' } }).DNSChallengeConfig, { CF_DNS_API_TOKEN: 't' });
});

test('sslModeText, hostEntry, withPort', () => {
  assert.equal(sslModeText('strict')[0], 'success');
  assert.equal(sslModeText('full')[0], 'warning');
  assert.equal(sslModeText('flexible')[0], 'warning');
  assert.equal(sslModeText('off')[0], 'error');
  assert.equal(sslModeText('')[0], 'info');
  assert.match(sslModeText('anything')[2], /Full \(strict\)/);

  const status = { hosts: [{ host: 'radarr.example.org' }, { host: 'example.org' }] };
  assert.equal(hostEntry(status, 'Radarr.Example.org:2053').host, 'radarr.example.org');
  assert.equal(hostEntry(status, 'example.org').host, 'example.org');
  assert.equal(hostEntry(status, 'radarr.local'), null);
  assert.equal(hostEntry(null, 'radarr.example.org'), null);
  assert.equal(hostEntry(status, ''), null);

  assert.equal(withPort('radarr.example.org', 2053), 'radarr.example.org:2053');
  assert.equal(withPort('radarr.example.org', 443), 'radarr.example.org');
  assert.equal(withPort('radarr.example.org:8443', 2053), 'radarr.example.org:8443');
  assert.equal(withPort('radarr.example.org', ''), 'radarr.example.org');
  assert.equal(withPort('', 2053), '');
});

test('routeLink: the link of an address carries the port Cosmos is on', () => {
  const panel = { protocol: 'https:', port: '2053', origin: 'https://192.168.1.10:2053' };
  assert.equal(routeLink({ UseHost: true, Host: 'radarr.example.org' }, panel), 'https://radarr.example.org:2053');
  assert.equal(routeLink({ UseHost: true, Host: 'radarr.local' }, panel), 'https://radarr.local:2053');
  assert.equal(routeLink({ UseHost: true, Host: 'radarr.example.org:8443' }, panel), 'https://radarr.example.org:8443', 'a URL with a port of its own keeps it');
  assert.equal(routeLink({ UseHost: true, Host: 'radarr.example.org', UsePathPrefix: true, PathPrefix: '/app' }, panel), 'https://radarr.example.org:2053/app');
  assert.equal(routeLink({ UseHost: false, UsePathPrefix: true, PathPrefix: '/app' }, panel), 'https://192.168.1.10:2053/app');
  // on the standard port nothing is added
  assert.equal(routeLink({ UseHost: true, Host: 'radarr.example.org' }, { protocol: 'https:', port: '', origin: 'https://example.org' }), 'https://radarr.example.org');
  assert.equal(routeLink({ UseHost: true, Host: 'radarr.example.org' }, { protocol: 'https:', port: '443', origin: 'https://example.org' }), 'https://radarr.example.org');
  assert.equal(routeLink({ UseHost: true, Host: 'radarr.local' }, { protocol: 'http:', port: '2052', origin: 'http://192.168.1.10:2052' }), 'http://radarr.local:2052');
});

test('every sentence of the tables is in the English translation file, word for word', () => {
  const translations = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));
  const pairs = [
    ...Object.values(TOKEN_STATUS).map((entry) => [entry[1], entry[2]]),
    ...Object.values(FAILURE),
    ...Object.values(CANNOT_SHIELD),
    ['mgmt.cloudflare.cannot.other', cannotShieldText('x')[1]],
    ...Object.values(MISMATCH),
    ...Object.values(PREVIEW_ACTION).map((entry) => [entry[0], entry[1]]),
    ...RECORD_TEXTS().map((entry) => [entry.key, entry.text]),
    ...['strict', 'full', 'flexible', 'off', ''].map((mode) => [sslModeText(mode)[1], sslModeText(mode)[2]]),
    ...[{ canEdit: 'no' }, { canRead: 'no' }, { canEdit: 'yes' }, { canEdit: 'unknown' }].map((zone) => [zoneAccessText(zone)[1], zoneAccessText(zone)[2]]),
  ];
  for (const [key, text] of pairs) {
    assert.equal(translations[key], text, key);
  }
});

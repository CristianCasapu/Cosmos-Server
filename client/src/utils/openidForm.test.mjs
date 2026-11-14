import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  knownAppOf, appOrigin, appChoices, proposeClientId, proposeForApp, redirectProblem, clientProblems, providerValues, KNOWN_APPS,
} from './openidForm.js';

const translations = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));

test('a URL is recognised by its name, its address or its container', () => {
  assert.equal(knownAppOf({ Name: 'photos', Host: 'immich.example.com' }).key, 'immich');
  assert.equal(knownAppOf({ Name: 'x', Host: 'x.example.com', Target: 'http://grafana:3000' }).key, 'grafana');
  assert.equal(knownAppOf({ Name: 'Open-WebUI', Host: 'ai.example.com' }).key, 'openwebui');
  assert.equal(knownAppOf({ Name: 'radarr', Host: 'radarr.example.com', Target: 'http://radarr:7878' }), null);
  // a word inside another one is not a match
  assert.equal(knownAppOf({ Name: 'cabsxyz', Host: 'q.example.com' }), null);
});

test('the app address carries the panel port, not the standard one', () => {
  assert.equal(appOrigin('Photos.Example.com', { https: true, port: '2053' }), 'https://photos.example.com:2053');
  assert.equal(appOrigin('photos.example.com', { https: true, port: '443' }), 'https://photos.example.com');
  assert.equal(appOrigin('photos.example.com', { https: true, port: '' }), 'https://photos.example.com');
  assert.equal(appOrigin('photos.local', { https: false, port: '80' }), 'http://photos.local');
});

test('choices: only URLs with a name of their own, once each', () => {
  const config = { HTTPConfig: { ProxyConfig: { Routes: [
    { Name: 'immich', UseHost: true, Host: 'immich.example.com' },
    { Name: 'immich-2', UseHost: true, Host: 'IMMICH.example.com' },
    { Name: 'path', UseHost: false, PathPrefix: '/x' },
    { Name: 'off', UseHost: true, Host: 'off.example.com', Disabled: true },
  ] } } };
  const choices = appChoices(config, { https: true, port: '2053' });
  assert.deepEqual(choices.map((c) => c.url), ['https://immich.example.com:2053']);
});

test('proposals: known paths, a named path, or the address to complete', () => {
  const where = { https: true, port: '2053' };
  const immich = proposeForApp({ name: 'immich', host: 'p.example.com', url: appOrigin('p.example.com', where), app: KNOWN_APPS[0] }, ['immich']);
  assert.equal(immich.id, 'immich-2');
  assert.deepEqual(immich.redirects, ['https://p.example.com:2053/auth/login', 'https://p.example.com:2053/user-settings', 'app.immich:///oauth-callback']);
  assert.ok(immich.complete);
  immich.redirects.forEach((r) => assert.equal(redirectProblem(r), null, r));
  const gitea = proposeForApp({ name: 'git', url: 'https://git.example.com', app: KNOWN_APPS.find((a) => a.key === 'gitea') });
  assert.equal(gitea.source[0], 'mgmt.openId.fork.sourceNamed');
  const other = proposeForApp({ name: 'My App', url: 'https://app.example.com' });
  assert.deepEqual(other.redirects, ['https://app.example.com/']);
  assert.equal(other.complete, false);
  assert.equal(other.id, 'my-app');
  assert.equal(proposeClientId('!!!'), 'app');
});

test('redirect URIs: the server rule', () => {
  for (const ok of ['https://a.example.com/cb', 'http://127.0.0.1:8080/cb', 'http://localhost/cb', 'http://[::1]/cb', 'app.immich:///oauth-callback']) {
    assert.equal(redirectProblem(ok), null, ok);
  }
  for (const bad of ['', 'http://a.example.com/cb', 'https://*.example.com/', 'javascript:alert(1)', 'data:text/html,x', 'file:///etc/passwd',
    'a.example.com/cb', 'https://u:p@a.example.com/', 'https://a.example.com/#x', 'https://a.example.com/a b', 'https:/a.example.com']) {
    assert.notEqual(redirectProblem(bad), null, bad);
  }
});

test('client problems', () => {
  assert.deepEqual(clientProblems({ id: 'grafana', redirects: ['https://g.example.com/login/generic_oauth'] }), []);
  assert.equal(clientProblems({ id: 'grafana', redirects: ['https://g.example.com/'], takenIds: ['grafana'] })[0][0], 'mgmt.openId.fork.idTaken');
  assert.equal(clientProblems({ id: 'bad id', redirects: ['https://g.example.com/'] })[0][0], 'mgmt.openId.fork.idInvalid');
  assert.ok(clientProblems({ id: 'a', redirects: ['https://g.example.com/', 'https://g.example.com/'] }).length > 0);
  assert.ok(clientProblems({ id: 'a', redirects: [] }).length > 0);
});

test('provider values name the port the panel is reached on', () => {
  const v = providerValues({ hostname: 'cosmos.example.com', https: true, port: '2053' });
  assert.equal(v.issuer, 'https://cosmos.example.com:2053');
  assert.equal(v.discovery, 'https://cosmos.example.com:2053/.well-known/openid-configuration');
  assert.equal(providerValues({ hostname: 'cosmos.example.com', https: true, port: '443' }).issuer, 'https://cosmos.example.com');
});

test('every text of the module is in the translation file', () => {
  const source = readFileSync(new URL('./openidForm.js', import.meta.url), 'utf8');
  const pairs = [...source.matchAll(/\['(mgmt\.openId\.fork\.[^']+)',\s*'((?:[^'\\]|\\.)*)'/g)];
  assert.ok(pairs.length > 10);
  for (const [, key, text] of pairs) {
    assert.equal(translations[key], text.replace(/\\(.)/g, '$1'), key);
  }
});

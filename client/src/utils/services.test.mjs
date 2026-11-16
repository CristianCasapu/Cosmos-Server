// Fork addition (plan 43): run with node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  classifyService, hintSettings, looksLikeDatabase, mainSecret, parseStartCheck, portIsNotWeb, probeWords, reasonWords,
  repairDefinition, repairEnv, repairReady, repairValues, serviceAddress, serviceFamily, withoutStartCheck, SERVICE_PORTS,
} from './services.js';

const recipes = JSON.parse(readFileSync(new URL('../../../src/anyimage/recipes.json', import.meta.url), 'utf8')).recipes;
const recipe = (id) => recipes.find((r) => r.id === id);
let seed = 3;
const random = (n) => Array.from({ length: n }, () => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed; });

test('the owner\'s case: bitnami/postgresql with no recipe is a PostgreSQL service on 5432, not web', () => {
  const c = classifyService({ image: 'bitnami/postgresql:latest', ports: [5432] });
  assert.equal(c.service, true);
  assert.equal(c.web, false);
  assert.equal(c.port, 5432);
  assert.equal(c.name, 'PostgreSQL');
  assert.equal(c.why, 'family');
  assert.equal(c.database, true);
  assert.equal(serviceAddress('/postgresql', 5432), 'postgresql:5432');
  assert.equal(portIsNotWeb(c, 5432), true);
});

test('families of publishers are recognised, with and without registry and tag', () => {
  for (const image of ['postgres:17', 'docker.io/library/postgres:18', 'bitnamilegacy/postgresql:17.6.0', 'pgvector/pgvector:pg18', 'postgis/postgis:17-3.5',
    'timescale/timescaledb-ha:pg17', 'cgr.dev/chainguard/postgres:latest', 'ghcr.io/example/postgres@sha256:abc']) {
    assert.equal(serviceFamily(image).name, 'PostgreSQL', image);
  }
  assert.equal(serviceFamily('linuxserver/mariadb').port, 3306);
  assert.equal(serviceFamily('percona/percona-server:8.4').port, 3306);
  assert.equal(serviceFamily('valkey/valkey:8').port, 6379);
  assert.equal(serviceFamily('redis/redis-stack:latest').web, 8001);
  assert.equal(serviceFamily('redis/redis-stack-server:latest').web, undefined);
  assert.equal(serviceFamily('bitnami/mongodb').port, 27017);
  // a name that merely contains the word is not the family
  assert.equal(serviceFamily('example/postgres-exporter'), null);
  assert.equal(serviceFamily('nginx:1'), null);
});

test('the strongest signal wins: recipe, then a real answer of the port, then the family, then the ports', () => {
  assert.deepEqual(
    (({ service, why, port }) => ({ service, why, port }))(classifyService({ image: 'postgres:17', ports: [5432], recipe: recipe('postgres') })),
    { service: true, why: 'recipe', port: 5432 });
  // a port that answered web pages is web, whatever the name says
  const probed = classifyService({ image: 'example/postgres-ui', ports: [5432, 8080], probes: { 8080: { answer: 'http' }, 5432: { answer: 'closed' } } });
  assert.equal(probed.web, true);
  assert.equal(probed.webPort, 8080);
  assert.equal(probed.why, 'probe');
  // an unknown image whose only port is a well-known non-web protocol
  const mqtt = classifyService({ image: 'example/broker', ports: [1883] });
  assert.equal(mqtt.service, true);
  assert.equal(mqtt.name, 'MQTT');
  // an unknown port that the server found not to speak web
  const odd = classifyService({ image: 'example/thing', ports: [7777], probes: { 7777: { answer: 'silent' } } });
  assert.equal(odd.service, true);
  assert.equal(odd.why, 'probe');
  // RabbitMQ: a service, and its management page is the web port
  const rabbit = classifyService({ image: 'rabbitmq:4-management', ports: [5672, 15672] });
  assert.equal(rabbit.service, true);
  assert.equal(rabbit.webPort, 15672);
  assert.equal(portIsNotWeb(rabbit, 15672), false);
});

test('a web app keeps its URL: no change for nginx or an app on 8096', () => {
  assert.equal(classifyService({ image: 'nginx:1', ports: [80] }).web, true);
  assert.equal(classifyService({ image: 'jellyfin/jellyfin', ports: [8096, 8920] }).service, false);
  assert.equal(classifyService({ image: 'nginx:1', ports: [80], recipe: recipe('nginx') }).service, false);
  assert.equal(classifyService({ image: 'example/x', ports: [] }).web, null);
});

test('the table names a protocol for every port and marks the databases', () => {
  for (const [port, entry] of Object.entries(SERVICE_PORTS)) {
    assert.ok(entry.protocol, port);
  }
  assert.equal(SERVICE_PORTS[5432].database, true);
  assert.equal(SERVICE_PORTS[22].database, undefined);
  assert.ok(Object.keys(SERVICE_PORTS).length >= 50);
});

test('the port check in words', () => {
  assert.equal(probeWords({ port: 5432, answer: 'closed' }).ok, false);
  assert.equal(probeWords({ port: 80, answer: 'http' }).ok, true);
  assert.match(probeWords({ port: 6379, answer: 'other', protocol: 'redis' }).text, /\{\{protocol\}\}/);
  assert.equal(probeWords(null), null);
});

const LINE = '[START CHECK] {"containers":[{"name":"postgresql","image":"bitnami/postgresql:latest","state":"restarting","failing":true,"reason":"missingSetting","exitCode":1,"needs":[{"name":"POSTGRESQL_PASSWORD","secret":true,"line":"The POSTGRESQL_PASSWORD environment variable is empty or not set."}],"log":["ERROR ==> The POSTGRESQL_PASSWORD environment variable is empty or not set."]}],"watched":3}';

test('the start check of a creation: read from its line, shown in words, the line itself kept out of the log', () => {
  const check = parseStartCheck('Checking...\n' + LINE + '\n');
  assert.equal(check.containers[0].needs[0].name, 'POSTGRESQL_PASSWORD');
  assert.equal(parseStartCheck('[START CHECK] {broken'), null);
  assert.equal(parseStartCheck('nothing here'), null);
  assert.equal(withoutStartCheck('a\n' + LINE + '\nb'), 'a\nb');
  const words = reasonWords(check.containers[0]);
  assert.equal(words.params.names, 'POSTGRESQL_PASSWORD');
  assert.equal(reasonWords({ name: 'x', reason: 'exited', exitCode: 2 }).params.code, 2);
});

test('the repair: a generated value for a secret, the variable added or replaced, nothing else touched', () => {
  const values = repairValues([{ name: 'POSTGRESQL_PASSWORD', secret: true }, { name: 'APP_HOST' }, { name: 'bad name' }], random);
  assert.equal(values.length, 2);
  assert.ok(values[0].value.length >= 20);
  assert.equal(values[1].value, '');
  assert.equal(repairReady(values), false);
  values[1].value = 'db';
  assert.equal(repairReady(values), true);
  assert.deepEqual(repairEnv(['TZ=UTC', 'APP_HOST='], values), ['TZ=UTC', 'APP_HOST=db', 'POSTGRESQL_PASSWORD=' + values[0].value]);
  const def = { services: { postgresql: { container_name: 'postgresql', image: 'bitnami/postgresql:latest', environment: ['TZ=UTC'] }, web: { image: 'nginx', environment: { A: '1' } } } };
  const fixed = repairDefinition(def, 'postgresql', values);
  assert.deepEqual(fixed.services.postgresql.environment, ['TZ=UTC', 'POSTGRESQL_PASSWORD=' + values[0].value, 'APP_HOST=db']);
  assert.deepEqual(fixed.services.web.environment, { A: '1' });
  assert.deepEqual(def.services.postgresql.environment, ['TZ=UTC'], 'the original is not changed');
  const map = repairDefinition({ services: { web: { environment: { A: '1' } } } }, 'web', [{ name: 'B', value: '2' }, { name: 'x=y', value: '3' }]);
  assert.deepEqual(map.services.web.environment, { A: '1', B: '2' });
});

// what the description reader answered for bitnami/postgresql (an extract, in its order)
const BITNAMI_HINTS = { source: 'description', env: [
  { name: 'POSTGRESQL_VOLUME_DIR', default: '/bitnami/postgresql', from: 'table' },
  { name: 'ALLOW_EMPTY_PASSWORD', secret: true, allowEmpty: true, from: 'table' },
  { name: 'POSTGRESQL_LDAP_BIND_PASSWORD', secret: true, from: 'table' },
  { name: 'POSTGRESQL_PASSWORD', secret: true, from: 'table' },
  { name: 'POSTGRESQL_POSTGRES_PASSWORD', secret: true, from: 'table' },
  { name: 'POSTGRESQL_REPLICATION_PASSWORD', secret: true, from: 'table' },
  { name: 'POSTGRESQL_DATABASE', default: 'postgres', from: 'table' },
  { name: 'POSTGRESQL_PASSWORD_FILE', from: 'table' },
], volumes: [], ports: [], docs: [] };

test('from the description: the image\'s own password is proposed, generated; the switch to go without is never', () => {
  assert.equal(mainSecret(BITNAMI_HINTS.env.filter((e) => !e.allowEmpty)).name, 'POSTGRESQL_PASSWORD');
  const settings = hintSettings(BITNAMI_HINTS, [], random);
  assert.equal(settings[0].name, 'POSTGRESQL_PASSWORD');
  assert.equal(settings[0].type, 'secret');
  assert.equal(settings[0].from, 'description');
  assert.ok(settings[0].value.length >= 20);
  assert.ok(!settings.some((s) => s.name === 'ALLOW_EMPTY_PASSWORD' || s.name.endsWith('_FILE')));
  // everything else is optional and empty: not set unless typed
  assert.ok(settings.slice(1).every((s) => s.type === 'optional' && s.value === ''));
  assert.equal(settings.find((s) => s.name === 'POSTGRESQL_DATABASE').hint, 'postgres');
  // a name the image declares itself is left to its declaration
  assert.ok(!hintSettings(BITNAMI_HINTS, ['POSTGRESQL_DATABASE'], random).some((s) => s.name === 'POSTGRESQL_DATABASE'));
});

test('from the description: a variable the text calls required is proposed; nothing when there is no description', () => {
  const s = hintSettings({ env: [{ name: 'POSTGRES_PASSWORD', secret: true, required: true }, { name: 'POSTGRES_USER' }, { name: 'APP_URL', required: true, example: 'https://example.org' }] }, [], random);
  assert.deepEqual(s.map((x) => [x.name, x.type]), [['POSTGRES_PASSWORD', 'secret'], ['APP_URL', 'needed'], ['POSTGRES_USER', 'optional']]);
  assert.equal(s[1].hint, 'https://example.org');
  assert.equal(s[1].value, '', 'an example is a hint, never a value');
  assert.deepEqual(hintSettings(null, [], random), []);
  // without an "allow empty" switch, a password the text does not call required is optional
  assert.equal(hintSettings({ env: [{ name: 'REDIS_PASSWORD', secret: true }] }, [], random)[0].type, 'optional');
  // bounded
  const many = { env: Array.from({ length: 100 }, (_, i) => ({ name: 'V_' + i })) };
  assert.ok(hintSettings(many, [], random).length <= 32);
});

test('an image without recipe that looks like a database is said to be one', () => {
  assert.equal(looksLikeDatabase('bitnami/postgresql', [5432]), 'PostgreSQL');
  assert.equal(looksLikeDatabase('example/store', [27017]), 'MongoDB');
  assert.equal(looksLikeDatabase('nginx', [80]), '');
  assert.equal(looksLikeDatabase('eclipse-mosquitto', [1883]), '');
});

test('the connection string has placeholders, never a password; publishing keeps the ports the app has', async () => {
  const { connectionTemplate, withPublishedPort, linkNetworkName } = await import('./services.js');
  assert.equal(connectionTemplate({ name: 'PostgreSQL' }, 'postgresql', 5432), 'postgresql://USER:PASSWORD@postgresql:5432/DATABASE');
  assert.equal(connectionTemplate({ name: 'MQTT' }, 'm', 1883), '');
  const inspected = { HostConfig: { PortBindings: { '8080/tcp': [{ HostIp: '127.0.0.1', HostPort: '8080' }] } } };
  assert.deepEqual(withPublishedPort(inspected, 5432), { '8080/tcp': [{ HostIp: '127.0.0.1', HostPort: '8080' }], '5432/tcp': [{ HostIp: '', HostPort: '5432' }] });
  assert.deepEqual(withPublishedPort({ HostConfig: { PortBindings: { '5432/tcp': [{ HostIp: '', HostPort: '5432' }] } } }, 5432)['5432/tcp'].length, 1);
  assert.deepEqual(inspected.HostConfig.PortBindings['5432/tcp'], undefined, 'the inspected container is not changed');
  assert.equal(linkNetworkName('postgresql', 'my app', 'x1'), 'link-postgresql-my-app-x1');
});

test('Bitnami\'s description of its own build is not a setting', async () => {
  const { declaredSettings } = await import('./anyImage.js');
  const names = declaredSettings({ env: ['OS_ARCH=amd64', 'OS_FLAVOUR=debian-12', 'OS_NAME=linux', 'BITNAMI_APP_NAME=postgresql', 'NSS_WRAPPER_LIB=/opt/x.so', 'IMAGE_REVISION=0', 'POSTGRESQL_PORT_NUMBER=5432'] }).map((s) => s.name);
  assert.deepEqual(names, ['POSTGRESQL_PORT_NUMBER']);
});

test('a new database is offered once per software; other publishers still recognise existing containers', async () => {
  const { databaseRecipes, existingDatabases } = await import('./anyImage.js');
  const offered = databaseRecipes(recipes, ['postgres']).map((r) => r.id);
  assert.deepEqual(offered, ['postgres']);
  const found = existingDatabases([{ Names: ['/pg'], Image: 'bitnami/postgresql:latest', State: 'running' }], recipes, ['postgres']);
  assert.equal(found.length, 1);
});

test('a recipe without a password of its own that has a web page is proposed for administrators only, behind the sign-in', async () => {
  const { serviceFor, parseReference } = await import('./anyImage.js');
  const { proposeAccess } = await import('./exposure.js');
  const withWeb = recipes.filter((r) => r.web);
  assert.ok(withWeb.some((r) => (r.notes || []).includes('noAuth')), 'at least one such recipe is checked');
  for (const r of withWeb) {
    const ref = parseReference(r.images[0]);
    const { service } = serviceFor({ ref, tag: 'latest', name: r.id, recipe: r, declared: { ports: [], volumes: [], env: [] }, settings: [], host: r.id + '.local' });
    const route = (service.routes || [])[0];
    assert.ok(route, r.id + ' gets its address');
    if ((r.notes || []).includes('noAuth')) {
      assert.equal(route.AuthEnabled, true, r.id);
      assert.equal(route.AdminOnly, true, r.id);
      for (const image of r.images) assert.equal(proposeAccess(image).access, 'admins', image + ' on the internet too');
    }
  }
});

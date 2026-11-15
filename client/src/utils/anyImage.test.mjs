// Fork addition (plan 40): run with node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  TEXT, buildDefinition, connectionOf, databaseNeed, declaredSettings, existingDatabases, facts, generateSecret, isDirectReference,
  isSecretName, isShellOnly, withCustom, marketImageName, marketMatch, nameFor, parseReference, problemText, proposedSettings, recipeVolumes, webPortOf,
} from './anyImage.js';

const recipes = JSON.parse(readFileSync(new URL('../../../src/anyimage/recipes.json', import.meta.url), 'utf8')).recipes;
const recipe = (id) => recipes.find((r) => r.id === id);
const references = JSON.parse(readFileSync(new URL('../../../src/anyimage/testdata/references.json', import.meta.url), 'utf8'));
const translations = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));
let seed = 7;
const random = (n) => Array.from({ length: n }, () => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed; });

// what the server answered for these images on 15 Nov 2026 (linux/amd64)
const PG18 = { ports: ['5432/tcp'], volumes: ['/var/lib/postgresql'], env: ['PATH=/usr/bin', 'GOSU_VERSION=1.19', 'LANG=en_US.utf8', 'PG_MAJOR=18', 'PG_VERSION=18.7-1', 'PGDATA=/var/lib/postgresql/18/docker'], healthcheck: null };
const PG17 = { ...PG18, volumes: ['/var/lib/postgresql/data'], env: ['PGDATA=/var/lib/postgresql/data'] };

test('references follow the same rules as the server (shared cases)', () => {
  for (const c of references) {
    const ref = parseReference(c.text, c.loggedIn ? [c.loggedIn] : []);
    if (c.problem) {
      assert.equal(ref.problem, c.problem, c.text);
    } else {
      assert.equal(ref.problem, undefined, c.text);
      assert.equal(`${ref.registry} ${ref.repository} ${ref.tag}`, c.want, c.text);
    }
  }
  assert.equal(parseReference('postgres').name, 'postgres');
  assert.equal(parseReference('ghcr.io/owner/app:1').name, 'ghcr.io/owner/app');
  assert.ok(isDirectReference('ghcr.io/owner/app') && isDirectReference('postgres:18') && !isDirectReference('postgres') && !isDirectReference('a b'));
});

test('the Market\'s image field in its five shapes', () => {
  assert.equal(marketImageName('https://hub.docker.com/_/wordpress'), 'wordpress');
  assert.equal(marketImageName('https://hub.docker.com/_/nextcloud/'), 'nextcloud');
  assert.equal(marketImageName('https://hub.docker.com/r/vaultwarden/server'), 'vaultwarden/server');
  assert.equal(marketImageName('https://ghcr.io/immich-app/immich-server:v3.1.0'), 'ghcr.io/immich-app/immich-server');
  assert.equal(marketImageName('https://registry-1.docker.io/dessalines/lemmy:0.19.6'), 'dessalines/lemmy');
  assert.equal(marketImageName('https://codeberg.org/forgejo/-/packages/container/forgejo'), 'codeberg.org/forgejo/forgejo');
  const apps = [{ name: 'Vaultwarden', image: 'https://hub.docker.com/r/vaultwarden/server' }, { name: 'Home Assistant', image: 'https://hub.docker.com/r/homeassistant/home-assistant' }];
  assert.equal(marketMatch(apps, 'vaultwarden/server').name, 'Vaultwarden');
  assert.equal(marketMatch(apps, 'ghcr.io/home-assistant/home-assistant', 'Home Assistant').name, 'Home Assistant');
  assert.equal(marketMatch(apps, 'postgres'), null);
});

test('secrets: strong, random, and the names that say secret', () => {
  const a = generateSecret(random);
  const b = generateSecret(random);
  assert.equal(a.length, 24);
  assert.notEqual(a, b);
  assert.match(a, /^[A-Za-z0-9]+$/);
  const strong = generateSecret(random, { strong: true });
  assert.match(strong, /[A-Z]/); assert.match(strong, /[a-z]/); assert.match(strong, /[0-9]/); assert.match(strong, /[-_.]/);
  for (const name of ['POSTGRES_PASSWORD', 'DB_PASS', 'APP_SECRET', 'API_TOKEN', 'SECRET_KEY', 'APP_KEY']) assert.ok(isSecretName(name), name);
  for (const name of ['POSTGRES_USER', 'KEYBOARD', 'PASSWORD_FILE', 'TZ']) assert.ok(!isSecretName(name), name);
});

test('PostgreSQL: the data folder follows the major, a generated password, nothing published', () => {
  const pg = recipe('postgres');
  assert.deepEqual(recipeVolumes(pg, '18', PG18).map((v) => v.target), ['/var/lib/postgresql']);
  assert.deepEqual(recipeVolumes(pg, '17-alpine', PG17).map((v) => v.target), ['/var/lib/postgresql/data']);
  assert.deepEqual(recipeVolumes(pg, 'latest', PG18).map((v) => v.target), ['/var/lib/postgresql']);
  assert.deepEqual(recipeVolumes(pg, 'alpine', PG17).map((v) => v.target), ['/var/lib/postgresql/data']);
  const settings = proposedSettings(pg, PG18, random);
  const password = settings.find((s) => s.name === 'POSTGRES_PASSWORD');
  assert.equal(password.type, 'secret');
  assert.equal(password.value.length, 24);
  const ref = parseReference('postgres');
  const { definition, web } = buildDefinition({ ref, tag: '18', name: 'postgres', recipe: pg, declared: PG18, settings, host: '' });
  const service = definition.services.postgres;
  assert.equal(web, null);
  assert.equal(service.image, 'postgres:18');
  assert.deepEqual(service.ports, []);
  assert.equal(service.routes, undefined);
  assert.deepEqual(service.volumes, [{ type: 'volume', source: 'postgres-postgresql', target: '/var/lib/postgresql' }]);
  assert.ok(service.environment.includes(`POSTGRES_PASSWORD=${password.value}`));
  assert.ok(service.environment.includes('POSTGRES_USER=app'));
  assert.equal(service.restart, 'unless-stopped');
  assert.equal(service.healthcheck.test[0], 'CMD-SHELL');
  assert.equal(service.user, '999:999', 'PostgreSQL runs as its own user, so its data folder is made its own');
  assert.equal(buildDefinition({ ref, tag: '18-alpine', name: 'pg', recipe: pg, declared: PG18, settings, host: '' }).definition.services.pg.user, '70:70');
  const lines = facts({ recipe: pg, declared: PG18, name: 'postgres', tag: '18', host: '', settings, size: 162000000 });
  assert.equal(lines[0].params.size, '162 MB');
  const reach = lines.find((l) => l.key.endsWith('fact.reach'));
  assert.deepEqual(reach.params, { host: 'postgres', port: 5432 });
  const client = lines.find((l) => l.key.endsWith('fact.reachClient'));
  assert.equal(client.params.url, 'postgres://app:<password>@postgres:5432/app');
  assert.ok(!JSON.stringify(lines).includes(password.value), 'the password is not in the sentences');
});

test('let other apps reach a database: links to them', () => {
  const { definition } = buildDefinition({ ref: parseReference('redis'), tag: '8', name: 'cache', recipe: recipe('redis'), declared: { ports: ['6379/tcp'], volumes: [], env: [] }, settings: [], host: '', reachers: ['nextcloud', 'paperless'] });
  assert.deepEqual(definition.services.cache.links, ['nextcloud', 'paperless']);
  assert.deepEqual(definition.services.cache.volumes.map((v) => v.target), ['/data']);
  assert.deepEqual(definition.services.cache.command, ['redis-server', '--appendonly', 'yes']);
});

test('a web image ends with an address in Cosmos, not a published port', () => {
  const nginx = recipe('nginx');
  const { definition, web } = buildDefinition({ ref: parseReference('nginx'), tag: '1', name: 'site', recipe: nginx, declared: { ports: ['80/tcp'], volumes: [], env: [] }, settings: [], host: 'site.server.local' });
  assert.equal(web, 80);
  assert.deepEqual(definition.services.site.ports, []);
  assert.equal(definition.services.site.routes[0].target, 'http://site:80');
  assert.equal(definition.services.site.routes[0].host, 'site.server.local');
});

test('an image without recipe: declared volumes, web port guessed, risky ports closed, secrets flagged', () => {
  const declared = { ports: ['3000/tcp', '5432/tcp'], volumes: ['/app/data'], env: ['NODE_VERSION=22', 'APP_SECRET=', 'LOG_LEVEL=info'] };
  assert.equal(webPortOf(null, declared), 3000);
  assert.equal(webPortOf(null, { ports: ['5432/tcp'] }), null);
  assert.equal(webPortOf(null, { ports: ['53/udp'] }), null);
  const settings = proposedSettings(null, declared, random);
  assert.deepEqual(settings.map((s) => [s.name, s.type]), [['APP_SECRET', 'secret'], ['LOG_LEVEL', 'declared']]);
  const { definition } = buildDefinition({ ref: parseReference('ghcr.io/owner/app:1.2'), tag: '1.2', name: 'app', recipe: null, declared, settings, host: 'app.server.local' });
  const app = definition.services.app;
  assert.equal(app.image, 'ghcr.io/owner/app:1.2');
  assert.ok(app.environment.some((l) => l.startsWith('APP_SECRET=') && l.length > 20));
  assert.ok(!app.environment.some((l) => l.startsWith('LOG_LEVEL')), 'a default left alone is not pinned');
  assert.deepEqual(app.volumes, [{ type: 'volume', source: 'app-data', target: '/app/data' }]);
  const lines = facts({ recipe: null, declared, name: 'app', tag: '1.2', host: 'app.server.local', settings });
  const closed = lines.find((l) => l.key.endsWith('fact.portKept'));
  assert.equal(closed.params.port, '5432/tcp');
  assert.ok(closed.why.key.endsWith('why.risky'));
});

test('an image that declares nothing says so, and takes a folder, a port and a command', () => {
  const nothing = { ports: [], volumes: [], env: ['PATH=/bin'], cmd: ['sh'] };
  const lines = facts({ recipe: null, declared: nothing, name: 'x', tag: 'latest', host: '', settings: [] });
  assert.ok(lines[0].key.endsWith('fact.declaresNothing'));
  assert.ok(lines[1].key.endsWith('fact.shellOnly'));
  assert.ok(isShellOnly(nothing) && !isShellOnly({ cmd: ['nginx', '-g', 'daemon off;'] }) && !isShellOnly({ entrypoint: ['/x'], cmd: ['sh'] }));
  const declared = withCustom(nothing, { volume: '/srv/data', port: '8080' });
  assert.deepEqual([declared.volumes, declared.ports], [['/srv/data'], ['8080/tcp']]);
  assert.deepEqual(withCustom(nothing, { volume: 'relative', port: '99999' }).volumes, []);
  const app = buildDefinition({ ref: parseReference('busybox'), tag: '1.36', name: 'box', recipe: null, declared, settings: [], host: 'box.server.local', command: 'httpd -f -p 8080' }).definition.services.box;
  assert.equal(app.command, 'httpd -f -p 8080');
  assert.equal(app.routes[0].target, 'http://box:8080');
  assert.deepEqual(app.volumes.map((v) => v.target), ['/srv/data']);
  assert.deepEqual(declaredSettings({ env: ['PATH=/bin', 'PHP_VERSION=8', 'HOME=/root'] }), []);
});

test('an app that needs a database: a new one with it, or one that exists', () => {
  const declared = { ports: ['8080/tcp'], volumes: [], env: ['DB_TYPE=postgresdb', 'DB_POSTGRESDB_HOST=', 'DB_POSTGRESDB_PORT=5432', 'DB_POSTGRESDB_USER=', 'DB_POSTGRESDB_PASSWORD=', 'DB_POSTGRESDB_DATABASE='] };
  const need = databaseNeed(null, declared);
  assert.deepEqual(need.kinds, ['postgres']);
  assert.equal(need.env.host, 'DB_POSTGRESDB_HOST');
  const pg = recipe('postgres');
  const dbSettings = proposedSettings(pg, null, random);
  const password = dbSettings.find((s) => s.name === 'POSTGRES_PASSWORD').value;
  const settings = proposedSettings(null, declared, random);
  const { definition } = buildDefinition({
    ref: parseReference('example/app'), tag: '1', name: 'app', recipe: null, declared, settings, host: 'app.server.local', need,
    database: { mode: 'new', ref: parseReference('postgres'), tag: '18', recipe: pg, name: 'app-postgres', settings: dbSettings },
  });
  const app = definition.services.app;
  const db = definition.services['app-postgres'];
  assert.ok(app.environment.includes('DB_POSTGRESDB_HOST=app-postgres'));
  assert.ok(app.environment.includes(`DB_POSTGRESDB_PASSWORD=${password}`));
  assert.ok(app.environment.includes('DB_POSTGRESDB_USER=app'));
  assert.ok(app.environment.includes('DB_POSTGRESDB_DATABASE=app'));
  assert.ok(app.environment.includes('DB_TYPE=postgres') || app.environment.includes('DB_TYPE=postgresdb'));
  assert.deepEqual(Object.keys(app.networks), ['app-db']);
  assert.deepEqual(Object.keys(db.networks), ['app-db']);
  assert.deepEqual(definition.networks, { 'app-db': { driver: 'bridge' } });
  assert.deepEqual(db.ports, []);
  assert.equal(app.depends_on['app-postgres'].condition, 'service_started');

  const existing = existingDatabases([{ Names: ['/maindb'], Image: 'postgres:17' }, { Names: ['/web'], Image: 'nginx' }], recipes, need.kinds);
  assert.deepEqual(existing.map((e) => e.name), ['maindb']);
  const linked = buildDefinition({
    ref: parseReference('example/app'), tag: '1', name: 'app', recipe: null, declared, settings, host: '', need,
    database: { mode: 'existing', container: 'maindb', recipe: pg, env: ['POSTGRES_USER=main', 'POSTGRES_PASSWORD=s3cret', 'POSTGRES_DB=maindb'] },
  }).definition.services.app;
  assert.deepEqual(linked.links, ['maindb']);
  assert.ok(linked.environment.includes('DB_POSTGRESDB_HOST=maindb'));
  assert.ok(linked.environment.includes('DB_POSTGRESDB_PASSWORD=s3cret'));
  assert.equal(databaseNeed(null, { env: ['LOG=1'] }), null);
  assert.deepEqual(databaseNeed(null, { env: ['DB_HOST=', 'DB_PORT=5432'] }).kinds, ['postgres'], 'the usual port says which database');
  assert.deepEqual(databaseNeed(null, { env: ['DB_HOST=', 'DB_PORT=3306'] }).kinds, ['mysql']);
  assert.deepEqual(databaseNeed(null, { env: ['DB_HOST='] }).kinds, ['postgres', 'mysql'], 'without a hint both are offered');
  assert.deepEqual(databaseNeed(recipe('phpmyadmin'), null).env, { host: 'PMA_HOST', port: 'PMA_PORT' });
  assert.equal(connectionOf(pg, 'db', { POSTGRES_USER: 'a b', POSTGRES_PASSWORD: 'p@ss', POSTGRES_DB: 'x' }).url, 'postgres://a%20b:p%40ss@db:5432/x');
});

test('a recipe that keeps its data in a Docker volume freezes it', () => {
  const { definition } = buildDefinition({ ref: parseReference('dpage/pgadmin4'), tag: '9', name: 'pgadmin', recipe: recipe('pgadmin'), declared: { ports: ['80/tcp', '443/tcp'], volumes: ['/var/lib/pgadmin'], env: [] }, settings: proposedSettings(recipe('pgadmin'), null, random), host: 'pgadmin.server.local' });
  assert.deepEqual(definition['cosmos-installer'], { 'frozen-volumes': ['pgadmin-pgadmin'] });
  const traefik = buildDefinition({ ref: parseReference('traefik'), tag: 'v3', name: 'traefik', recipe: recipe('traefik'), declared: { ports: ['80/tcp'] }, settings: [], host: 't.server.local' }).definition.services.traefik;
  assert.deepEqual(traefik.volumes, [{ type: 'bind', source: '/var/run/docker.sock', target: '/var/run/docker.sock', ReadOnly: true }]);
});

test('names and problems', () => {
  assert.equal(nameFor(parseReference('ghcr.io/owner/My_app'.toLowerCase())), 'my_app');
  assert.equal(nameFor(parseReference('postgres'), ['/postgres']), 'postgres-2');
  assert.ok(problemText({ problem: 'unauthorized', loggedIn: false, registry: 'ghcr.io' }).key.endsWith('unauthorizedLogin'));
  assert.ok(problemText({ problem: 'strange' }).key.endsWith('problem.unreachable'));
  assert.equal(problemText({ problem: '' }), null);
});

test('every text is in the English translation file, word for word', () => {
  let checked = 0;
  for (const [code, english] of Object.entries(TEXT)) {
    assert.equal(translations['mgmt.servapps.anyImage.' + code], english, code);
    checked += 1;
  }
  const component = readFileSync(new URL('../components/installAnyImage.jsx', import.meta.url), 'utf8');
  for (const m of component.matchAll(/\bt\(\s*'(mgmt\.servapps\.anyImage\.[^']+)'\s*,\s*'((?:[^'\\]|\\.)*)'/gs)) {
    assert.equal(translations[m[1]], m[2].replace(/\\(.)/g, '$1'), m[1]);
    checked += 1;
  }
  for (const r of recipes) for (const n of r.notes || []) assert.ok(TEXT['note.' + n], `note ${n} of ${r.id}`);
  for (const r of recipes) for (const p of r.ports || []) assert.ok(TEXT['why.' + p.why], `why ${p.why} of ${r.id}`);
  assert.ok(checked > 80, `only ${checked}`);
});

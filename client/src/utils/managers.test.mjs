// Tests of the web managers and of the defaults in "Install any image" and the Market (plan 45).
import test from 'node:test';
import assert from 'node:assert/strict';
import { webcrypto } from 'node:crypto';
import { buildDefinition, managerDefinition, managerEnv, managersFor, parseReference, proposedSettings } from './anyImage.js';
import { composeVarDefault, followsRule, marketFieldDefault, marketFormValue, LABEL, MANAGER_LABEL } from './signIn.js';

const random = (n) => { const a = new Uint32Array(n); webcrypto.getRandomValues(a); return a; };

const pg = {
  id: 'postgres', name: 'PostgreSQL', kind: 'database', provides: 'postgres', service: { port: 5432 },
  volumes: [{ target: '/var/lib/postgresql', from: 18 }],
  env: [{ name: 'POSTGRES_PASSWORD', type: 'secret' }, { name: 'POSTGRES_USER', type: 'name', value: 'app' }, { name: 'POSTGRES_DB', type: 'name', value: 'app' }],
  client: { url: 'postgres://{user}:{password}@{host}:5432/{database}', user: 'POSTGRES_USER', password: 'POSTGRES_PASSWORD', database: 'POSTGRES_DB' },
  login: {
    where: 'database',
    accounts: [{ role: 'app', user: 'POSTGRES_USER', userUnset: 'postgres', password: 'POSTGRES_PASSWORD', database: 'POSTGRES_DB', defaults: true }],
    user: { pattern: '^[a-z_][a-z0-9_]*$', max: 63, reserved: ['postgres'] },
    password: { min: 8, max: 64, symbols: '-._~', require: ['lower', 'upper', 'digit'] },
  },
};
const pgadmin = {
  id: 'pgadmin', name: 'pgAdmin', kind: 'tool', images: ['dpage/pgadmin4'], web: { port: 80 }, manager: { for: ['postgres'], rank: 1 },
  entrypoint: ['/bin/sh', '-c', 'wrapper'],
  volumes: [{ target: '/var/lib/pgadmin', keepVolume: true }],
  env: [{ name: 'PGADMIN_DEFAULT_EMAIL', type: 'name', value: 'admin@example.org' }, { name: 'PGADMIN_DEFAULT_PASSWORD', type: 'secret' }],
  needs: { kinds: ['postgres'], optional: true, env: {}, servers: 'PGADMIN_FORK_SERVERS', passFile: 'PGADMIN_FORK_PGPASS', passFilePath: '/var/lib/pgadmin/pgpass' },
  login: {
    where: 'web', accounts: [{ role: 'app', email: 'PGADMIN_DEFAULT_EMAIL', password: 'PGADMIN_DEFAULT_PASSWORD', defaults: true }],
    email: { refuseSpecialUse: true }, password: { min: 6, max: 64, symbols: '-._~', require: [] },
  },
};
const adminer = { id: 'adminer', name: 'Adminer', kind: 'tool', images: ['adminer'], web: { port: 8080 }, manager: { for: ['postgres', 'mysql'], rank: 2 }, needs: { kinds: ['postgres', 'mysql'], optional: true, env: { host: 'ADMINER_DEFAULT_SERVER' } } };
const defaults = { user: 'Owner.Name', email: 'owner@example.org', length: 30 };

test('the managers of a kind come by rank', () => {
  assert.deepEqual(managersFor([adminer, pgadmin, pg], 'postgres').map((r) => r.id), ['pgadmin', 'adminer']);
  assert.deepEqual(managersFor([adminer, pgadmin], 'redis'), []);
});

test('Install any image takes the defaults in the form the app takes, and the rule', () => {
  const settings = proposedSettings(pg, null, random, defaults);
  const get = (n) => settings.find((s) => s.name === n);
  assert.equal(get('POSTGRES_USER').value, 'owner_name');
  assert.equal(get('POSTGRES_USER').variant, 'chars');
  assert.equal(get('POSTGRES_DB').value, 'app');
  assert.ok(followsRule(get('POSTGRES_PASSWORD').value, pg.login.password));
  assert.equal(get('POSTGRES_PASSWORD').value.length, 30);
  const tool = proposedSettings(pgadmin, null, random, { ...defaults, email: 'owner@server.local' });
  assert.equal(tool.find((s) => s.name === 'PGADMIN_DEFAULT_EMAIL').value, 'admin@example.org', 'an address the app refuses is not used');
  assert.equal(tool.find((s) => s.name === 'PGADMIN_DEFAULT_EMAIL').refused, 'specialUse');
  // without defaults, as before
  assert.equal(proposedSettings(pg, null, random, null).find((s) => s.name === 'POSTGRES_USER').value, 'app');
});

test('pgAdmin is pointed at its database without a password in its list of servers', () => {
  const values = { POSTGRES_USER: 'owner_name', POSTGRES_DB: 'notes', POSTGRES_PASSWORD: 'Pw-1~x.y_z' };
  const env = managerEnv(pgadmin, { name: 'notes-db', recipe: pg, values });
  const servers = JSON.parse(env.PGADMIN_FORK_SERVERS);
  assert.deepEqual(servers.Servers['1'], { Name: 'notes-db', Group: 'Servers', Host: 'notes-db', Port: 5432, MaintenanceDB: 'notes', Username: 'owner_name', SSLMode: 'prefer', PassFile: '/var/lib/pgadmin/pgpass' });
  assert.ok(!env.PGADMIN_FORK_SERVERS.includes(values.POSTGRES_PASSWORD));
  assert.equal(env.PGADMIN_FORK_PGPASS, 'notes-db:5432:*:owner_name:Pw-1~x.y_z');
  // a colon or a backslash in a password is escaped as .pgpass wants
  assert.equal(managerEnv(pgadmin, { name: 'db', recipe: pg, values: { ...values, POSTGRES_PASSWORD: 'a:b\\c' } }).PGADMIN_FORK_PGPASS, 'db:5432:*:owner_name:a\\:b\\\\c');
  // an unset user is the image's own
  assert.equal(JSON.parse(managerEnv(pgadmin, { name: 'db', recipe: pg, values: { POSTGRES_PASSWORD: 'x' } }).PGADMIN_FORK_SERVERS).Servers['1'].Username, 'postgres');
  assert.deepEqual(managerEnv(adminer, { name: 'notes-db', recipe: pg, values }), { ADMINER_DEFAULT_SERVER: 'notes-db' });
});

test('a database and its manager share a network of their own; the manager is for administrators, never published', () => {
  const settings = proposedSettings(pg, null, random, defaults);
  const manager = { recipe: pgadmin, ref: parseReference('dpage/pgadmin4'), tag: '9', name: 'notes-db-pgadmin', settings: proposedSettings(pgadmin, null, random, defaults), host: 'notes-db-pgadmin.server.local' };
  const { definition } = buildDefinition({ ref: parseReference('postgres'), tag: '18', name: 'notes-db', recipe: pg, declared: null, settings, host: 'notes-db.server.local', manager });
  const db = definition.services['notes-db'];
  const m = definition.services['notes-db-pgadmin'];
  assert.deepEqual(Object.keys(definition.networks), ['notes-db-manager']);
  assert.deepEqual(Object.keys(db.networks), ['notes-db-manager']);
  assert.deepEqual(Object.keys(m.networks), ['notes-db-manager']);
  assert.deepEqual(m.ports, []);
  assert.deepEqual(db.ports, []);
  assert.equal(m.routes.length, 1);
  assert.equal(m.routes[0].AdminOnly, true);
  assert.equal(m.routes[0].AuthEnabled, true);
  assert.equal(m.routes[0].target, 'http://notes-db-pgadmin:80');
  assert.equal(m.labels[MANAGER_LABEL], 'notes-db');
  assert.deepEqual(m.entrypoint, ['/bin/sh', '-c', 'wrapper']);
  assert.ok(m.environment.some((l) => l === 'PGADMIN_DEFAULT_EMAIL=owner@example.org'));
  assert.deepEqual(definition['cosmos-installer']['frozen-volumes'], ['notes-db-pgadmin-pgadmin']);
  assert.equal(m.depends_on['notes-db'].condition, 'service_started');
  // no password on a command line or in a label
  const password = settings.find((s) => s.name === 'POSTGRES_PASSWORD').value;
  assert.ok(!JSON.stringify(m.entrypoint || []).includes(password) && !JSON.stringify(m.command || []).includes(password));
  assert.ok(!JSON.stringify(m.labels).includes(password) && !JSON.stringify(db.labels || {}).includes(password));
});

test('a manager for a database already on the server joins it through a link, the database untouched', () => {
  const def = managerDefinition({ manager: adminer, ref: parseReference('adminer'), tag: '5', name: 'old-db-adminer', settings: [], host: 'old-db-adminer.server.local', db: { name: 'old-db', recipe: pg, values: { POSTGRES_PASSWORD: 'x' } } });
  assert.deepEqual(Object.keys(def.services), ['old-db-adminer']);
  assert.deepEqual(def.services['old-db-adminer'].links, ['old-db']);
  assert.equal(def.services['old-db-adminer'].networks, undefined);
  assert.ok(def.services['old-db-adminer'].environment.includes('ADMINER_DEFAULT_SERVER=old-db'));
  assert.equal(def.services['old-db-adminer'].routes[0].AdminOnly, true);
});

test('the Market\'s form fields take the defaults over placeholders, never over a value', () => {
  const user = { name: 'MOODLE_USERNAME', type: 'text', initialValue: 'admin' };
  const mail = { name: 'MOODLE_EMAIL', type: 'text', initialValue: 'user@example.com' };
  const pass = { name: 'MOODLE_PASSWORD', type: 'password', initialValue: 'change-me' };
  const smtp = { name: 'SMTP_PASSWORD', type: 'password', initialValue: '' };
  const other = { name: 'language', type: 'select', initialValue: 'en' };
  assert.equal(marketFieldDefault(user, defaults, random).value, 'Owner.Name');
  assert.equal(marketFieldDefault(mail, defaults, random).value, 'owner@example.org');
  assert.ok(followsRule(marketFieldDefault(pass, defaults, random).value, { min: 16, max: 64, symbols: '', require: ['lower', 'upper', 'digit'] }));
  assert.equal(marketFieldDefault(smtp, defaults, random).filled, '');
  assert.equal(marketFieldDefault(other, defaults, random).value, 'en');
  assert.equal(marketFieldDefault({ name: 'ADMIN_USER', type: 'text', initialValue: 'keep-me' }, defaults, random).value, 'keep-me');
  // once, then again only while the template's placeholder is still there
  assert.equal(marketFormValue(user, undefined, null, random), undefined);
  assert.equal(marketFormValue(user, 'admin', defaults, random), 'Owner.Name');
  assert.equal(marketFormValue(user, 'Owner.Name', defaults, random), undefined);
  assert.equal(marketFormValue(user, 'typed', defaults, random), undefined);
  const first = marketFormValue(pass, undefined, null, random);
  assert.ok(first && first !== 'change-me');
  assert.equal(marketFormValue(pass, first, defaults, random), undefined);
});

test('a compose file\'s open secrets are generated, an outside service\'s left empty', () => {
  assert.ok(followsRule(composeVarDefault('DB_PASSWORD', defaults, random), { min: 16, max: 64, symbols: '', require: ['lower', 'upper', 'digit'] }));
  assert.ok(composeVarDefault('APP_SECRET_KEY', defaults, random).length >= 16);
  assert.equal(composeVarDefault('SMTP_PASSWORD', defaults, random), '');
  assert.equal(composeVarDefault('AWS_SECRET_ACCESS_KEY', defaults, random), '');
  assert.equal(composeVarDefault('ADMIN_USER', defaults, random), 'Owner.Name');
  assert.equal(composeVarDefault('ADMIN_EMAIL', defaults, random), 'owner@example.org');
  assert.equal(composeVarDefault('TZ', defaults, random), '');
  assert.equal(composeVarDefault('DB_USER', defaults, random), '');
});

test('the label of the review never carries a value', () => {
  assert.equal(LABEL, 'cosmos-fork-signin');
});

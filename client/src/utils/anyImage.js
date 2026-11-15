// Fork addition (plan 40): installing any image. From what the server read
// about an image (POST /api/images/describe: its declaration, its tags, the
// recipe Cosmos has for it) and the administrator's few choices, this builds
// the service definition the one review of every creation takes
// (components/creationReview.jsx through NewDockerService), and the sentences
// that say what was proposed. Nothing here talks to the server or keeps a
// secret anywhere but in the object it returns. Tested in anyImage.test.mjs.
import { looksLikeWeb } from './routerPorts.js';
import { isRiskyPort } from './exposure.js';
import { sizeText } from './dataPlaces.js';

const P = 'mgmt.servapps.anyImage.';

export const TEXT = {
  'problem.invalid': 'This is not an image name. A name looks like postgres, postgres:18, ghcr.io/owner/app or registry.example.org/team/app:1.2 (no https://, no spaces).',
  'problem.internal': 'This registry is on the local network (or is an address, or a name without a dot), and the server is not logged in to it. Cosmos only reads such a registry after an administrator has logged the server in to it: run "docker login {{registry}}" on the server, as the user Cosmos runs as.',
  'problem.notFound': 'The registry has no image {{name}} with the tag {{tag}}. Check the name, or pick a tag from the list.',
  'problem.unauthorized': 'The registry {{registry}} refused to show this image: it is private, or the name is wrong.',
  'problem.unauthorizedLogin': 'The server is not logged in to {{registry}}. If the image is private, log the server in with "docker login {{registry}}" (as the user Cosmos runs as), then look again.',
  'problem.unauthorizedLoggedIn': 'The server is logged in to {{registry}}, but these credentials may not read this image.',
  'problem.rateLimited': 'The registry asks to wait (too many requests). Docker Hub allows 100 image reads per 6 hours to a server that is not signed in. Try again in a while.',
  'problem.busy': 'Cosmos has looked up many images in the last minute: wait a moment, then look again.',
  'problem.unreachable': 'The registry could not be reached or did not answer in time. Check that the server can reach the internet.',
  'problem.tooLarge': 'The registry\'s answer was larger than Cosmos reads for one image: not read.',
  'problem.notImage': 'This is not a container image Cosmos can read (another kind of artifact, or an answer that does not match its checksum).',
  'problem.redirect': 'The registry sent Cosmos to an address it does not follow (not https, or too many hops).',
  'problem.platform': '{{name}} is made for {{platforms}} only: it would not run on this server.',
  'search.problem.rateLimited': 'Docker Hub asks to wait (too many searches): try again in a while.',
  'search.problem.busy': 'Too many searches just now: wait a minute and search again.',
  'search.problem.unreachable': 'Docker Hub could not be reached. Type the image\'s full name instead, for example postgres:18 or ghcr.io/owner/app.',
  'tag.pinMajor': 'The newest major version, {{tag}}. A database is pinned to its major version: it then gets the fixes of {{tag}}, and moving to the next major (which needs its data converted) stays your decision.',
  'tag.pinMajorApp': 'The newest major version, {{tag}}: it gets the updates of {{tag}}, and moving to the next major stays your decision.',
  'tag.newest': 'The newest version the registry lists.',
  'tag.prefer': 'The long-term support line ({{tag}}).',
  'tag.latest': 'The registry lists no version numbers: "latest" is what it offers.',
  'tag.typed': 'The tag you typed.',
  'fact.reach': 'Other apps reach it at {{host}}:{{port}} once they share a network with it. Nothing is published on the server.',
  'fact.reachClient': 'Their address for it: {{url}}',
  'fact.web': 'Its web interface gets the address {{url}} in Cosmos; no port is published on the server.',
  'fact.portKept': 'Port {{port}} is not published ({{why}}).',
  'fact.data': 'Its data is in {{target}}: that is what to back up.',
  'fact.dataKept': 'Its data is in {{target}} and stays in a Docker volume: the app runs as its own user, and a folder made by Cosmos would not be writable for it.',
  'fact.backup': 'To back it up while it runs: {{command}}',
  'fact.memory': 'Memory: {{memory}}.',
  'fact.size': 'The download is about {{size}} (compressed); Docker keeps it unpacked, larger, where it keeps its images.',
  'fact.healthcheck': 'Cosmos adds a health check the image does not have: {{test}}',
  'fact.declaresNothing': 'This image says nothing about itself: no port, no folder for data, no setting. Its page usually says what it needs; the fields below are open.',
  'fact.shellOnly': 'Its command is a shell ({{cmd}}): without a command of its own the container stops at once. Give it the command to run below.',
  'fact.declaredVolume': 'The image keeps data in {{target}}.',
  'fact.declaredPort': 'The image listens on port {{port}}{{web}}.',
  'fact.declaredPortWeb': ' (looks like a web interface)',
  'fact.secretsWhere': 'The generated values are shown here once. Cosmos keeps them only in the container\'s settings, which every administrator can read on its page.',
  'why.risky': 'a service other apps use, not for the house or the internet',
  'why.other': 'not needed by the app\'s users',
  'why.xProtocol': 'MySQL\'s X protocol, for MySQL Shell',
  'why.webTls': 'the same interface with its own certificate; Cosmos already gives it HTTPS',
  'why.tls': 'the same service with TLS, for clients outside',
  'why.metrics': 'measurements for a monitoring tool',
  'why.cluster': 'between the nodes of a cluster: this is one node',
  'why.cosmosProxies': 'Cosmos already answers on 80 and 443 for this server',
  'why.admin': 'its administration interface, never to be open',
  'note.majorUpgrade': 'Moving to another major version needs a dump and a restore (or pg_upgrade): a new tag of the same major is safe.',
  'note.pgdata18': 'From PostgreSQL 18 the image keeps its data under /var/lib/postgresql (each major in its own folder); up to 17 in /var/lib/postgresql/data. Cosmos mounts the right one for the tag.',
  'note.noAuth': 'It has no password: only the apps you let reach it can use it, and it is not published.',
  'note.cpuAvx': 'MongoDB 5 and newer need a processor with AVX.',
  'note.toolSignIn': 'You sign in to the database inside it, with the database\'s own user and password.',
  'note.ownedVolume': 'It runs as its own user: its data stays in a Docker volume.',
  'note.hostnameKeeps': 'It keeps its data under its host name: the container gets a fixed one.',
  'note.maxMapCount': 'The server must allow it enough memory areas: vm.max_map_count at least 262144 on the host (Cosmos does not change host settings).',
  'note.httpsSelfSigned': 'It answers HTTPS with a certificate of its own on 9200.',
  'note.nothingToKeep': 'It keeps nothing: there is nothing to back up.',
  'note.cosmosIsProxy': 'Cosmos is already this server\'s reverse proxy: this one is reached through Cosmos, not on 80 and 443.',
  'note.socket': 'It reads the Docker socket (read-only) to find the other containers: the review asks you to accept that.',
  'db.need': 'This app needs a database ({{kinds}}).',
  'db.needOptional': 'This app can use a database ({{kinds}}).',
  'db.new': 'A new {{name}} for this app',
  'db.existing': 'One that is already on this server',
  'db.none': 'None: I fill in its settings myself',
  'db.newSentence': '{{db}} is created with the app, on a network only the two share; the app\'s settings are filled in from it.',
  'db.existingSentence': 'The app joins {{db}} on a network of their own and gets its address; user, password and database are taken from {{db}}\'s own settings where it has them.',
  'db.noExisting': 'No container on this server looks like a {{kinds}} database.',
  'db.filledList': 'Filled in from the database: {{list}}.',
  'db.existingOne': '{{name}} ({{kind}}, on this server)',
  'market.has': 'The Market has {{market}}, with a form made for it.',
  'env.secret': 'Generated',
  'env.name': 'Proposed',
  'env.declared': 'The image\'s default',
};

export const text = (code, params) => ({ key: P + code, text: TEXT[code], params: params || {} });

// ---- references -------------------------------------------------------------------

const PATH = /^[a-z0-9]+(?:(?:[._]|__|[-]+)[a-z0-9]+)*$/;
const TAG = /^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$/;
const DIGEST = /^sha256:[a-f0-9]{64}$/;
const LABEL = /^(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9])$/;
const IPV4 = /^\d{1,3}(\.\d{1,3}){3}$/;

const validPort = (s) => /^\d{1,5}$/.test(s) && Number(s) >= 1 && Number(s) <= 65535;

// { internal, ok } for a registry host[:port], the server's rules (src/anyimage/reference.go).
const hostOf = (text) => {
  let host = text;
  if (host.startsWith('[')) {
    const end = host.indexOf(']');
    if (end < 0) return { ok: false };
    const rest = host.slice(end + 1);
    if (rest && !(rest.startsWith(':') && validPort(rest.slice(1)))) return { ok: false };
    return { ok: /^[0-9a-fA-F:.]+$/.test(host.slice(1, end)), internal: true };
  }
  const colon = host.lastIndexOf(':');
  if (colon >= 0) {
    if (!validPort(host.slice(colon + 1))) return { ok: false };
    host = host.slice(0, colon);
  }
  if (!host || host.length > 253) return { ok: false };
  if (IPV4.test(host)) return { ok: true, internal: true };
  const labels = host.split('.');
  if (labels.some((l) => l.length > 63 || !LABEL.test(l))) return { ok: false };
  if (labels.length < 2 || host.toLowerCase() === 'localhost' || /^\d+$/.test(labels[labels.length - 1])) return { ok: true, internal: true };
  return { ok: true, internal: false };
};

// The reference split like the server does; { problem } when it is refused.
// loggedIn: the registries the server is logged in to (none known in the
// browser: the server decides, this only gives the message early).
export const parseReference = (input, loggedIn = []) => {
  const value = String(input == null ? '' : input);
  const bad = { problem: 'invalid' };
  if (!value || value.length > 255 || /[^\x21-\x7e]/.test(value)) return bad;
  if (value.includes('://') || value.startsWith('/') || value.endsWith('/')) return bad;
  let rest = value;
  let digest = '';
  let tag = '';
  const at = rest.indexOf('@');
  if (at >= 0) {
    digest = rest.slice(at + 1);
    rest = rest.slice(0, at);
    if (!DIGEST.test(digest)) return bad;
  }
  const slash = rest.lastIndexOf('/');
  const colon = rest.lastIndexOf(':');
  if (colon > slash) {
    tag = rest.slice(colon + 1);
    rest = rest.slice(0, colon);
    if (!TAG.test(tag)) return bad;
  }
  let parts = rest.split('/');
  let registry = 'docker.io';
  if (parts.length > 1 && (/[.:]/.test(parts[0]) || parts[0] === 'localhost')) {
    const host = hostOf(parts[0]);
    if (!host.ok) return bad;
    registry = parts[0].toLowerCase();
    parts = parts.slice(1);
    if (host.internal && !loggedIn.includes(registry)) return { problem: 'internal', registry };
  }
  if (['index.docker.io', 'registry-1.docker.io', 'registry.hub.docker.com'].includes(registry)) registry = 'docker.io';
  if (!parts.length || parts.length > 8 || parts.some((p) => !PATH.test(p))) return bad;
  if (registry === 'docker.io' && parts.length === 1) parts = ['library', ...parts];
  const repository = parts.join('/');
  if (repository.length > 200) return bad;
  if (!tag && !digest) tag = 'latest';
  const name = registry === 'docker.io' ? repository.replace(/^library\//, '') : `${registry}/${repository}`;
  return { registry, repository, tag, digest, name, typedTag: colon > slash };
};

// A text that is a full reference (a registry or a tag) is described at
// once; anything else is a search of Docker Hub.
export const isDirectReference = (input) => {
  const ref = parseReference(String(input || '').trim());
  if (ref.problem === 'internal') return true;
  if (ref.problem) return false;
  return ref.registry !== 'docker.io' || ref.typedTag || !!ref.digest;
};

// ---- the Market --------------------------------------------------------------------

// The Market's "image" field, in its five shapes, as an image name.
export const marketImageName = (address) => {
  let a = String(address || '').trim().replace(/^https?:\/\//, '').replace(/\/+$/, '');
  let m;
  if ((m = a.match(/^hub\.docker\.com\/_\/([^/?#]+)/i))) return m[1].toLowerCase();
  if ((m = a.match(/^hub\.docker\.com\/r\/([^/?#]+\/[^/?#]+)/i))) return m[1].toLowerCase();
  if ((m = a.match(/^codeberg\.org\/([^/]+)\/-\/packages\/container\/([^/?#]+)/i))) return `codeberg.org/${m[1]}/${m[2]}`.toLowerCase();
  a = a.replace(/^(registry-1\.docker\.io|index\.docker\.io|docker\.io)\//i, '');
  a = a.split('@')[0];
  const lastSlash = a.lastIndexOf('/');
  const colon = a.lastIndexOf(':');
  if (colon > lastSlash) a = a.slice(0, colon);
  return a.toLowerCase().replace(/^library\//, '');
};

// The Market entry for an image name, from the Market's list or a recipe's alias.
export const marketMatch = (apps, name, alias) => {
  const list = apps || [];
  const target = String(name || '').toLowerCase();
  return list.find((app) => app && marketImageName(app.image) === target)
    || (alias ? list.find((app) => app && app.name === alias) : null)
    || null;
};

// ---- small proposals ---------------------------------------------------------------------

// A container name from the image: the last part of its path.
export const nameFor = (ref, taken = []) => {
  const last = String((ref && ref.repository) || 'app').split('/').pop();
  const base = last.replace(/[^a-z0-9_.-]+/g, '-').replace(/^[^a-z0-9]+|[^a-z0-9]+$/g, '').slice(0, 40) || 'app';
  const used = new Set((taken || []).map((n) => String(n).replace(/^\//, '')));
  if (!used.has(base)) return base;
  for (let i = 2; i < 100; i += 1) if (!used.has(`${base}-${i}`)) return `${base}-${i}`;
  return `${base}-${Date.now() % 1000}`;
};

const ALNUM = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789';

// A strong random value: 24 characters from 57 (about 140 bits); with
// `strong`, one of each kind a picky image asks for (OpenSearch).
// `random(n)` fills a Uint32Array(n) (crypto.getRandomValues in the browser).
export const generateSecret = (random, { strong = false, length = 24 } = {}) => {
  const values = random(length + 4);
  let out = '';
  for (let i = 0; i < length; i += 1) out += ALNUM[values[i] % ALNUM.length];
  if (strong) {
    out = out.slice(0, length - 4) + 'K' + 'q' + String(values[length] % 10) + '-_.'[values[length + 1] % 3];
  }
  return out;
};

export const browserRandom = (n) => {
  const values = new Uint32Array(n);
  globalThis.crypto.getRandomValues(values);
  return values;
};

const SECRET_NAME = /(PASS(WORD)?|PASSWD|SECRET|TOKEN|(^|_)KEY$|PRIVATE|CREDENTIALS?)(_|$)/i;
export const isSecretName = (name) => SECRET_NAME.test(String(name || '')) && !/_FILE$/i.test(String(name || ''));

// Variables an image keeps for itself: never copied, never shown as settings.
const INTERNAL_ENV = (key) => ['PATH', 'HOME', 'HOSTNAME', 'TERM', 'LANG', 'LANGUAGE', 'LC_ALL', 'SHELL', 'container', 'LD_LIBRARY_PATH', 'JAVA_HOME', 'PYTHONPATH'].includes(key)
  || /(^|_)(VERSION|RELEASE|MAJOR|SHA\d*|CHECKSUM|GPG_KEYS?|PGP_KEY_ID|DOWNLOAD_URL|URL|DEPS|CFLAGS|CPPFLAGS|LDFLAGS|INI_DIR|PREFIX|PACKAGE|REPO)$/.test(key)
  || /^(PHP|PYTHON|NODE|GOSU|JSYAML|ERLANG|OPENSSL|APACHE|NGINX|NJS|PKG|DYNPKG|ACME)_/.test(key);

const envKey = (line) => String(line).split('=')[0];
const envValue = (line) => { const s = String(line); const i = s.indexOf('='); return i < 0 ? '' : s.slice(i + 1); };

// The variables an image declares that are settings, with their defaults.
export const declaredSettings = (declared) => ((declared && declared.env) || [])
  .filter((line) => String(line).includes('=') && !INTERNAL_ENV(envKey(line)))
  .map((line) => ({ name: envKey(line), value: envValue(line), secret: isSecretName(envKey(line)) }));

export const majorOf = (tag) => {
  const m = String(tag || '').match(/^v?(\d+)(?:[.-]|$)/);
  return m ? Number(m[1]) : null;
};

const portNumber = (p) => Number(String(p).split('/')[0]);
const portProtocol = (p) => String(p).split('/')[1] || 'tcp';

// The volumes of a recipe for a tag: by the tag's major; for a tag without
// one ("latest", "alpine"), the one the image itself declares, else the newest.
export const recipeVolumes = (recipe, tag, declared) => {
  const list = (recipe && recipe.volumes) || [];
  const major = majorOf(tag);
  const fits = (v) => (v.from == null || (major != null && major >= v.from)) && (v.until == null || (major != null && major <= v.until));
  if (major != null) return list.filter(fits);
  const declaredTargets = (declared && declared.volumes) || [];
  const ranged = list.filter((v) => v.from != null || v.until != null);
  if (!ranged.length) return list;
  const chosen = ranged.find((v) => declaredTargets.includes(v.target)) || ranged.find((v) => v.until == null) || ranged[0];
  return list.filter((v) => (v.from == null && v.until == null) || v === chosen);
};

// A web port among the declared ones, or null: one that looks like a web
// interface (routerPorts' list), or the only TCP port when it does.
export const webPortOf = (recipe, declared) => {
  if (recipe) return recipe.web ? recipe.web.port : null;
  const ports = ((declared && declared.ports) || []).filter((p) => portProtocol(p) === 'tcp').map(portNumber);
  const web = ports.filter((port) => !isRiskyPort(port) && looksLikeWeb({ containerPort: port, protocol: 'tcp' }).web);
  return web.length ? web[0] : null;
};

// ---- databases an app needs -----------------------------------------------------------------

const ROLES = {
  host: ['DB_HOST', 'DATABASE_HOST', 'POSTGRES_HOST', 'PGHOST', 'MYSQL_HOST', 'MARIADB_HOST', 'WORDPRESS_DB_HOST', 'DB_POSTGRESDB_HOST', 'MONGO_HOST', 'MONGODB_HOST', 'DB_HOSTNAME'],
  port: ['DB_PORT', 'DATABASE_PORT', 'POSTGRES_PORT', 'PGPORT', 'MYSQL_PORT', 'DB_POSTGRESDB_PORT'],
  user: ['DB_USER', 'DB_USERNAME', 'DATABASE_USER', 'DATABASE_USERNAME', 'PGUSER', 'WORDPRESS_DB_USER', 'DB_POSTGRESDB_USER'],
  password: ['DB_PASSWORD', 'DB_PASS', 'DATABASE_PASSWORD', 'PGPASSWORD', 'WORDPRESS_DB_PASSWORD', 'DB_POSTGRESDB_PASSWORD'],
  database: ['DB_NAME', 'DB_DATABASE', 'DATABASE_NAME', 'PGDATABASE', 'WORDPRESS_DB_NAME', 'DB_POSTGRESDB_DATABASE'],
  url: ['DATABASE_URL', 'DB_URL', 'DB_URI', 'DATABASE_URI'],
};

const kindHint = (names, values) => {
  const all = (names.join(' ') + ' ' + values.join(' ')).toUpperCase();
  if (values.includes('5432')) return ['postgres'];
  if (values.includes('3306')) return ['mysql'];
  if (values.includes('27017')) return ['mongo'];
  if (/POSTGRES|PGSQL|(^|[^A-Z])PG[A-Z_]/.test(all)) return ['postgres'];
  if (/MYSQL|MARIADB|WORDPRESS/.test(all)) return ['mysql'];
  if (/MONGO/.test(all)) return ['mongo'];
  return ['postgres', 'mysql'];
};

// What an app needs of a database: { kinds, optional, env: {role: NAME} } or null.
export const databaseNeed = (recipe, declared) => {
  if (recipe) {
    if (!recipe.needs) return null;
    return { kinds: recipe.needs.kinds, optional: !!recipe.needs.optional, env: { ...(recipe.needs.env || {}) } };
  }
  const settings = declaredSettings(declared);
  const names = settings.map((s) => s.name);
  const env = {};
  Object.keys(ROLES).forEach((role) => {
    const found = ROLES[role].find((n) => names.includes(n));
    if (found) env[role] = found;
  });
  if (!env.host && !env.url) return null;
  const typeLine = settings.find((s) => /^(DB_TYPE|DB_CONNECTION|DB_ENGINE|DB_CLIENT)$/.test(s.name));
  const portLine = env.port ? settings.find((s) => s.name === env.port) : null;
  const hints = [typeLine ? typeLine.value : '', portLine ? portLine.value : ''].filter(Boolean);
  return { kinds: kindHint(names, hints), optional: false, env, typeName: typeLine ? typeLine.name : '' };
};

// The values a database gives an app: host, port, user, password, database, url.
export const connectionOf = (dbRecipe, host, values) => {
  const client = (dbRecipe && dbRecipe.client) || {};
  const port = dbRecipe && dbRecipe.service ? dbRecipe.service.port : '';
  const get = (name) => (name ? (values[name] || '') : '');
  const conn = { host, port: String(port), user: get(client.user), password: get(client.password), database: get(client.database) };
  conn.url = String(client.url || '')
    .replace('{user}', encodeURIComponent(conn.user)).replace('{password}', encodeURIComponent(conn.password))
    .replace('{host}', host).replace('{database}', encodeURIComponent(conn.database));
  return conn;
};

// The app's variables filled from a connection: { NAME: value } for each role it has.
export const fillFromConnection = (need, conn, dbKind) => {
  const out = {};
  if (!need) return out;
  Object.entries(need.env || {}).forEach(([role, name]) => {
    if (conn[role] !== undefined && conn[role] !== '') out[name] = conn[role];
  });
  if (need.typeName) out[need.typeName] = dbKind === 'mysql' ? 'mysql' : dbKind === 'mongo' ? 'mongodb' : 'postgres';
  return out;
};

// ---- the definition ---------------------------------------------------------------------------

const folderOf = (target) => (String(target).replace(/\/+$/, '').split('/').pop() || 'data').replace(/[^A-Za-z0-9_.-]+/g, '-');

// The settings proposed for a recipe or a declaration, before the
// administrator changes them: [{ name, value, type: secret|name|optional|fixed|declared }].
export const proposedSettings = (recipe, declared, random) => {
  if (recipe) {
    return (recipe.env || []).map((e) => ({
      name: e.name,
      type: e.type,
      value: e.type === 'secret' ? generateSecret(random, { strong: !!e.strong }) : (e.value || ''),
    }));
  }
  return declaredSettings(declared).map((s) => ({
    name: s.name,
    type: s.secret ? (s.value ? 'declared' : 'secret') : 'declared',
    value: s.secret && !s.value ? generateSecret(random) : s.value,
  }));
};

// The uid:gid a recipe runs the container as for a tag (so that Cosmos makes
// its data folder that user's: PostgreSQL 18 mounts the folder above its
// data, which the image's own user must be able to enter).
export const runAsFor = (recipe, tag) => {
  const entry = ((recipe && recipe.runAs) || []).find((r) => !r.tagContains || String(tag || '').includes(r.tagContains));
  return entry ? entry.user : '';
};

const healthOf = (h) => (h ? { test: h.test, interval: h.interval, timeout: h.timeout, retries: h.retries } : undefined);

// One service for an image. `settings` as proposedSettings gives them (changed
// or not); a declared default the administrator left alone is not copied
// (copying would pin it against the image's next version).
export const serviceFor = ({ ref, tag, name, recipe, declared, settings, host, extraEnv = {}, links = [], networks = [], command = '' }) => {
  const image = `${ref.name}:${tag}`;
  const env = [];
  const declaredMap = Object.fromEntries(declaredSettings(declared).map((s) => [s.name, s.value]));
  (settings || []).forEach((s) => {
    if (s.type === 'optional' && !s.value) return;
    if (s.type === 'declared' && declaredMap[s.name] === s.value) return;
    env.push(`${s.name}=${s.value}`);
  });
  Object.entries(extraEnv).forEach(([key, value]) => {
    const i = env.findIndex((line) => envKey(line) === key);
    if (i >= 0) env[i] = `${key}=${value}`; else env.push(`${key}=${value}`);
  });
  const service = {
    image,
    container_name: name,
    restart: 'unless-stopped',
    environment: env,
    labels: {},
    ports: [],
    volumes: [],
  };
  const frozen = [];
  const volumes = recipe ? recipeVolumes(recipe, tag, declared) : ((declared && declared.volumes) || []).map((target) => ({ target }));
  volumes.forEach((v) => {
    const source = `${name}-${folderOf(v.target)}`;
    service.volumes.push({ type: 'volume', source, target: v.target });
    if (v.keepVolume) frozen.push(source);
  });
  ((recipe && recipe.binds) || []).forEach((b) => service.volumes.push({ type: 'bind', source: b.source, target: b.target, ...(b.readOnly ? { ReadOnly: true } : {}) }));
  const runAs = runAsFor(recipe, tag);
  if (runAs) service.user = runAs;
  if (recipe && recipe.command) service.command = recipe.command;
  if (command && String(command).trim()) service.command = String(command).trim();
  if (recipe && recipe.hostname) service.hostname = name;
  if (recipe && recipe.healthcheck && !(declared && declared.healthcheck)) service.healthcheck = healthOf(recipe.healthcheck);
  const web = webPortOf(recipe, declared);
  if (web && host) {
    service.routes = [{
      name, description: `${name} in Cosmos`, useHost: true, host, target: `http://${name}:${web}`, mode: 'SERVAPP',
      Timeout: 14400000, ThrottlePerMinute: 10000, BlockCommonBots: true, SmartShield: { Enabled: true },
    }];
  }
  if (links.length) service.links = [...links];
  if (networks.length) service.networks = Object.fromEntries(networks.map((n) => [n, {}]));
  return { service, frozen, web };
};

// The whole definition: the image, and its new database when one is added.
// `database`: { mode: 'new', ref, tag, recipe, name, settings } |
// { mode: 'existing', container, recipe, env } | { mode: 'none' } | null.
export const buildDefinition = ({ ref, tag, name, recipe, declared, settings, host, reachers = [], database = null, need = null, command = '' }) => {
  const services = {};
  const networks = {};
  let frozen = [];
  let extraEnv = {};
  let appLinks = [];
  let appNetworks = [];
  if (database && database.mode === 'new' && database.recipe) {
    const net = `${name}-db`;
    networks[net] = { driver: 'bridge' };
    appNetworks = [net];
    const values = Object.fromEntries((database.settings || []).map((s) => [s.name, s.value]));
    const dbService = serviceFor({ ref: database.ref, tag: database.tag, name: database.name, recipe: database.recipe, declared: null, settings: database.settings, host: '', networks: [net] });
    services[database.name] = dbService.service;
    frozen = frozen.concat(dbService.frozen);
    extraEnv = fillFromConnection(need, connectionOf(database.recipe, database.name, values), database.recipe.provides);
    services[database.name].labels = { 'cosmos-stack': name, 'cosmos-stack-main': name };
  } else if (database && database.mode === 'existing' && database.container) {
    appLinks = [database.container];
    const values = Object.fromEntries(((database.env || [])).map((line) => [envKey(line), envValue(line)]));
    extraEnv = fillFromConnection(need, connectionOf(database.recipe, database.container, values), database.recipe && database.recipe.provides);
  }
  const app = serviceFor({ ref, tag, name, recipe, declared, settings, host, extraEnv, links: [...appLinks, ...reachers], networks: appNetworks, command });
  services[name] = app.service;
  if (Object.keys(services).length > 1) {
    services[name].labels = { 'cosmos-stack': name, 'cosmos-stack-main': name };
    services[name].depends_on = { [database.name]: { condition: 'service_started' } };
  }
  frozen = frozen.concat(app.frozen);
  const definition = { services: { ...services } };
  if (Object.keys(networks).length) definition.networks = networks;
  if (frozen.length) definition['cosmos-installer'] = { 'frozen-volumes': frozen };
  return { definition, web: app.web };
};

// ---- what the panel says -------------------------------------------------------------------

// The sentences about an image and the choices, as { key, text, params }
// (and `why`, a second text for the {{why}} of a closed port): what it is
// reached at, what stays closed, where its data is, notes.
export const facts = ({ recipe, declared, name, tag, host, settings, size }) => {
  const lines = [];
  if (size > 0) lines.push(text('fact.size', { size: sizeText(size) }));
  const service = recipe && recipe.service;
  const web = webPortOf(recipe, declared);
  if (service) {
    lines.push(text('fact.reach', { host: name, port: service.port }));
    if (recipe.client && recipe.client.url) {
      const values = Object.fromEntries((settings || []).map((s) => [s.name, s.value]));
      const conn = connectionOf(recipe, name, { ...values, [recipe.client.password]: 'PASSWORDPLACE' });
      lines.push(text('fact.reachClient', { url: conn.url.replace('PASSWORDPLACE', '<password>') }));
    }
  }
  if (web && host) lines.push(text('fact.web', { url: `https://${host}` }));
  if (recipe) {
    (recipe.ports || []).forEach((p) => {
      lines.push({ ...text('fact.portKept', { port: p.port }), why: text('why.' + p.why) });
    });
    recipeVolumes(recipe, tag, declared).forEach((v) => lines.push(text(v.keepVolume ? 'fact.dataKept' : 'fact.data', { target: v.target })));
    if (recipe.backup) lines.push(text('fact.backup', { command: recipe.backup }));
    if (recipe.memory) lines.push(text('fact.memory', { memory: recipe.memory }));
    if (recipe.healthcheck && !(declared && declared.healthcheck)) lines.push(text('fact.healthcheck', { test: recipe.healthcheck.test.filter((x) => !/^CMD(-SHELL)?$/.test(x)).join(' ') }));
    (recipe.notes || []).forEach((n) => lines.push(text('note.' + n)));
  } else if (declared) {
    const ports = (declared.ports || []);
    const volumes = (declared.volumes || []);
    if (!ports.length && !volumes.length && !declaredSettings(declared).length) {
      lines.push(text('fact.declaresNothing'));
    }
    if (isShellOnly(declared)) lines.push(text('fact.shellOnly', { cmd: (declared.cmd || []).join(' ') }));
    volumes.forEach((target) => lines.push(text('fact.declaredVolume', { target })));
    ports.forEach((p) => {
      const port = portNumber(p);
      if (port === web) return;
      lines.push({ ...text('fact.portKept', { port: p }), why: text(isRiskyPort(port) ? 'why.risky' : 'why.other') });
    });
  }
  if ((settings || []).some((s) => s.type === 'secret')) lines.push(text('fact.secretsWhere'));
  return lines;
};

// An image whose only command is a shell stops at once without a command of its own.
export const isShellOnly = (declared) => !!declared && !(declared.entrypoint || []).length
  && /^(\/bin\/|\/usr\/bin\/)?(sh|bash|ash|zsh)$/.test(String((declared.cmd || [])[0] || '')) && (declared.cmd || []).length <= 2;

// What the administrator adds to an image that declares nothing: a folder for
// its data, a web port, a command. The declaration the definition is built from.
export const withCustom = (declared, custom) => {
  if (!declared || !custom) return declared;
  const out = { ...declared, volumes: [...(declared.volumes || [])], ports: [...(declared.ports || [])] };
  const folder = String(custom.volume || '').trim();
  if (/^\/[^\s]*$/.test(folder) && folder !== '/' && !out.volumes.includes(folder)) out.volumes.push(folder);
  const port = Number(custom.port);
  if (Number.isInteger(port) && port > 0 && port < 65536 && !out.ports.includes(`${port}/tcp`)) out.ports.push(`${port}/tcp`);
  return out;
};

// The words for a problem of the describe answer.
export const problemText = (desc) => {
  const p = desc && desc.problem;
  if (!p) return null;
  const params = { registry: desc.registry || '', name: desc.repository ? desc.repository.replace(/^library\//, '') : desc.reference, tag: desc.tag || '' };
  if (p === 'unauthorized') return text(desc.loggedIn ? 'problem.unauthorizedLoggedIn' : 'problem.unauthorizedLogin', params);
  return TEXT['problem.' + p] ? text('problem.' + p, params) : text('problem.unreachable', params);
};

// The databases recipes, for "a new one": those providing one of the kinds.
export const databaseRecipes = (all, kinds) => (all || []).filter((r) => r && (kinds || []).includes(r.provides) && r.kind === 'database');

// Containers on the server that look like a database of these kinds: [{ name, recipe, env }].
// `containers`: the ServApps list (Names, Image, Config.Env when known).
export const existingDatabases = (containers, all, kinds) => {
  const list = [];
  (containers || []).forEach((c) => {
    const ref = parseReference(String(c.Image || '').split('@')[0]);
    if (ref.problem) return;
    const recipe = (all || []).find((r) => (r.images || []).includes(ref.name) && (kinds || []).includes(r.provides));
    if (!recipe) return;
    list.push({ name: String((c.Names && c.Names[0]) || c.Name || '').replace(/^\//, ''), recipe });
  });
  return list.filter((x) => x.name);
};

// The number of decisions the flow asked for, for the browser check: each
// click on a choice the administrator made (opening, picking, creating).
export const DECISION_STEPS = ['open', 'pick', 'create'];

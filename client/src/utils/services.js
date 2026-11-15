// Fork addition (plan 43): apps that are services, not web apps, and apps
// that do not start. Pure logic, no UI and no API, so that it can be tested
// on its own (services.test.mjs).
//
// - classifyService says whether a container or an image has a web
//   interface, from the strongest signal it has: a built-in recipe, a real
//   answer of the port (the server's port check), the image family, the
//   ports against a table of well-known protocols that are not web;
// - serviceAddress is the address other apps on this server use;
// - the start check is the line the server writes after a creation
//   ([START CHECK] {...}) and the report of GET .../start-report, turned
//   into words and into the repair (set the variables it names, a generated
//   value for a secret, and create the container again);
// - hintSettings turns what the image's description says (the describe
//   route's `hints`) into proposed settings for the review.
import { generateSecret, isSecretName } from './anyImage.js';

// Well-known ports of protocols that are not web, with the software that
// usually speaks them. `web`: the port of the same software's own web page,
// when it has one.
export const SERVICE_PORTS = {
  20: { protocol: 'FTP data' }, 21: { protocol: 'FTP' }, 22: { protocol: 'SSH' }, 23: { protocol: 'Telnet' },
  25: { protocol: 'SMTP (mail)' }, 53: { protocol: 'DNS' }, 88: { protocol: 'Kerberos' }, 110: { protocol: 'POP3 (mail)' },
  111: { protocol: 'RPC (portmap)' }, 119: { protocol: 'NNTP' }, 135: { protocol: 'Windows RPC' }, 139: { protocol: 'NetBIOS' },
  143: { protocol: 'IMAP (mail)' }, 179: { protocol: 'BGP' }, 389: { protocol: 'LDAP' }, 445: { protocol: 'SMB (file sharing)' },
  465: { protocol: 'SMTPS (mail)' }, 514: { protocol: 'Syslog' }, 587: { protocol: 'SMTP submission (mail)' }, 636: { protocol: 'LDAPS' },
  853: { protocol: 'DNS over TLS' }, 873: { protocol: 'rsync' }, 993: { protocol: 'IMAPS (mail)' }, 995: { protocol: 'POP3S (mail)' },
  1080: { protocol: 'SOCKS' }, 1433: { protocol: 'Microsoft SQL Server', database: true }, 1521: { protocol: 'Oracle Database', database: true },
  1883: { protocol: 'MQTT' }, 2049: { protocol: 'NFS' }, 2181: { protocol: 'ZooKeeper' }, 2375: { protocol: 'Docker API' }, 2376: { protocol: 'Docker API (TLS)' },
  2379: { protocol: 'etcd' }, 2380: { protocol: 'etcd peers' }, 3306: { protocol: 'MySQL / MariaDB', database: true }, 3389: { protocol: 'RDP (remote desktop)' },
  3478: { protocol: 'STUN/TURN' }, 4222: { protocol: 'NATS', web: 8222 }, 5222: { protocol: 'XMPP' }, 5269: { protocol: 'XMPP servers' },
  5432: { protocol: 'PostgreSQL', database: true }, 5672: { protocol: 'AMQP (RabbitMQ)', web: 15672 }, 5900: { protocol: 'VNC' },
  6379: { protocol: 'Redis / Valkey', database: true }, 6443: { protocol: 'Kubernetes API' }, 6881: { protocol: 'BitTorrent' },
  7000: { protocol: 'Cassandra cluster' }, 7001: { protocol: 'Cassandra cluster (TLS)' }, 7687: { protocol: 'Neo4j Bolt', database: true, web: 7474 },
  8883: { protocol: 'MQTT over TLS' }, 9042: { protocol: 'Cassandra CQL', database: true }, 9092: { protocol: 'Kafka' },
  9300: { protocol: 'Elasticsearch transport' }, 10250: { protocol: 'kubelet' }, 11211: { protocol: 'Memcached', database: true },
  25565: { protocol: 'Minecraft' }, 26257: { protocol: 'CockroachDB SQL', database: true, web: 8080 }, 27017: { protocol: 'MongoDB', database: true },
  28015: { protocol: 'RethinkDB', database: true, web: 8080 }, 50000: { protocol: 'Jenkins agents' },
};

// Image families: the software, its port, whether it is a database.
const FAMILIES = [
  [/(^|\/)(postgres(ql)?|postgis|pgvector|timescaledb(-ha)?|cimg\/postgres)(:|@|$)/, { name: 'PostgreSQL', port: 5432, database: true }],
  [/(^|\/)(mariadb|mysql|percona(-server)?|percona-server-mysql)(:|@|$)/, { name: 'MariaDB / MySQL', port: 3306, database: true }],
  [/(^|\/)(redis(-stack-server)?|valkey|keydb|dragonfly)(:|@|$)/, { name: 'Redis / Valkey', port: 6379, database: true }],
  [/(^|\/)redis-stack(:|@|$)/, { name: 'Redis Stack', port: 6379, database: true, web: 8001 }],
  [/(^|\/)(mongo(db)?|percona-server-mongodb)(:|@|$)/, { name: 'MongoDB', port: 27017, database: true }],
  [/(^|\/)memcached(:|@|$)/, { name: 'Memcached', port: 11211, database: true }],
  [/(^|\/)(mssql-server|mssql)(:|@|$)/, { name: 'Microsoft SQL Server', port: 1433, database: true }],
  [/(^|\/)(cassandra|scylla)(:|@|$)/, { name: 'Cassandra', port: 9042, database: true }],
  [/(^|\/)(eclipse-mosquitto|mosquitto|emqx)(:|@|$)/, { name: 'MQTT broker', port: 1883 }],
  [/(^|\/)(rabbitmq)(:|@|$)/, { name: 'RabbitMQ', port: 5672, web: 15672 }],
  [/(^|\/)(nats)(:|@|$)/, { name: 'NATS', port: 4222, web: 8222 }],
  [/(^|\/)(zookeeper)(:|@|$)/, { name: 'ZooKeeper', port: 2181 }],
  [/(^|\/)(kafka|cp-kafka)(:|@|$)/, { name: 'Kafka', port: 9092 }],
];

// The image's name without registry and tag: "bitnami/postgresql".
const bareName = (image) => String(image || '').toLowerCase().replace(/@.*$/, '').replace(/:[^/]*$/, '').replace(/^[^/]+\.[^/]+\//, '').replace(/^docker\.io\//, '').replace(/^library\//, '');

export const serviceFamily = (image) => {
  const name = bareName(image);
  if (!name) return null;
  // redis-stack (with its web page) before the plain Redis family
  const ordered = [...FAMILIES].sort((a, b) => (a[1].web ? -1 : 0) - (b[1].web ? -1 : 0));
  const found = ordered.find(([pattern]) => pattern.test(name + ':'));
  return found ? found[1] : null;
};

const WEBISH = (port) => [80, 443, 3000, 5000, 5001, 8000, 8001, 8008, 8080, 8081, 8088, 8090, 8443, 8888, 9000, 9001, 9090, 9443, 15672, 8222, 7474].includes(Number(port))
  || (Number(port) >= 8000 && Number(port) <= 8999);

// classifyService: { service, web, name, port, webPort, why, database }
//   service  true: no web interface on the port it is used by (webPort may still name a separate web page)
//   web      true: a web interface is known (webPort)
//   why      recipe | probe | family | port | none
// probes: { [port]: ProbeResult } as POST .../probe answers them.
export const classifyService = ({ image, ports = [], recipe = null, probes = {} }) => {
  const list = [...new Set((ports || []).map(Number).filter((p) => Number.isInteger(p) && p > 0 && p < 65536))].sort((a, b) => a - b);
  const answered = (p) => probes && probes[p] && probes[p].answer;
  const isWebAnswer = (p) => ['http', 'https'].includes(answered(p));
  const notWebAnswer = (p) => ['other', 'closed', 'silent'].includes(answered(p));
  if (recipe) {
    if (recipe.web) return { service: false, web: true, name: recipe.name, port: recipe.service ? recipe.service.port : recipe.web.port, webPort: recipe.web.port, why: 'recipe', database: recipe.kind === 'database' };
    if (recipe.service || recipe.kind === 'database') {
      return { service: true, web: false, name: recipe.name, port: recipe.service ? recipe.service.port : (list[0] || null), webPort: null, why: 'recipe', database: recipe.kind === 'database' };
    }
  }
  const webAnswered = list.find(isWebAnswer);
  if (webAnswered) {
    const other = list.find((p) => p !== webAnswered && notWebAnswer(p));
    return { service: false, web: true, name: '', port: other || webAnswered, webPort: webAnswered, why: 'probe', database: false };
  }
  const family = serviceFamily(image);
  if (family) {
    const webPort = family.web && (list.length === 0 || list.includes(family.web)) ? family.web : null;
    return { service: true, web: !!webPort, name: family.name, port: list.includes(family.port) || !list.length ? family.port : list[0], webPort, why: 'family', database: !!family.database };
  }
  const known = list.filter((p) => SERVICE_PORTS[p]);
  const allNotWeb = list.length > 0 && list.every((p) => SERVICE_PORTS[p] || notWebAnswer(p));
  if (allNotWeb) {
    const first = known[0] || list[0];
    const companion = SERVICE_PORTS[first] && SERVICE_PORTS[first].web;
    return { service: true, web: false, name: SERVICE_PORTS[first] ? SERVICE_PORTS[first].protocol : '', port: first, webPort: companion && list.includes(companion) ? companion : null, why: answered(first) ? 'probe' : 'port', database: !!(SERVICE_PORTS[first] && SERVICE_PORTS[first].database) };
  }
  const web = list.find((p) => !SERVICE_PORTS[p] && WEBISH(p));
  if (web) return { service: false, web: true, name: '', port: web, webPort: web, why: 'port', database: false };
  return { service: false, web: null, name: '', port: list[0] || null, webPort: null, why: 'none', database: false };
};

// The address other apps on this server use: "postgresql:5432".
export const serviceAddress = (container, port) => `${String(container || '').replace(/^\//, '')}${port ? ':' + port : ''}`;

// A port (as a number) is not web for this app: the URL forms warn.
export const portIsNotWeb = (classification, port) => !!classification && classification.service && Number(port) === Number(classification.port) && Number(port) !== Number(classification.webPort);

// ---- the server's port check ----------------------------------------------------------------

// What a port check said, for the warning of a URL to a port that is not web.
export const probeWords = (result) => {
  if (!result) return null;
  const p = { port: result.port, protocol: result.protocol || '' };
  switch (result.answer) {
    case 'http': return { key: 'mgmt.services.probe.http', text: 'Port {{port}} answers web pages (HTTP).', params: p, ok: true };
    case 'https': return { key: 'mgmt.services.probe.https', text: 'Port {{port}} answers web pages over HTTPS.', params: p, ok: true };
    case 'other': return result.protocol
      ? { key: 'mgmt.services.probe.protocol', text: 'Port {{port}} answers as {{protocol}}, not as a web page: a web address to it shows an error.', params: p, ok: false }
      : { key: 'mgmt.services.probe.other', text: 'Port {{port}} does not answer as a web page: a web address to it shows an error.', params: p, ok: false };
    case 'closed': return { key: 'mgmt.services.probe.closed', text: 'Port {{port}} closed the connection when asked for a web page: it speaks another protocol.', params: p, ok: false };
    case 'silent': return { key: 'mgmt.services.probe.silent', text: 'Port {{port}} did not answer a web request: it speaks another protocol, or is busy.', params: p, ok: false };
    case 'refused': return { key: 'mgmt.services.probe.refused', text: 'Nothing listens on port {{port}} of the app.', params: p, ok: false };
    case 'notRunning': return { key: 'mgmt.services.probe.notRunning', text: 'The app is not running: its port could not be checked.', params: p, ok: null };
    case 'hostNetwork': return { key: 'mgmt.services.probe.hostNetwork', text: 'The app uses the server\'s own network: its port was not checked.', params: p, ok: null };
  }
  return { key: 'mgmt.services.probe.unknown', text: 'Port {{port}} could not be checked.', params: p, ok: null };
};

// ---- the start check -----------------------------------------------------------------------

export const START_MARKER = '[START CHECK] ';

// The start check in a line of the creation's log, or null.
export const parseStartCheck = (line) => {
  const text = String(line || '');
  const at = text.indexOf(START_MARKER);
  if (at < 0) return null;
  try {
    const value = JSON.parse(text.slice(at + START_MARKER.length).split('\n')[0]);
    return value && Array.isArray(value.containers) ? value : null;
  } catch (e) {
    return null;
  }
};

// The log as shown, without the start check's line (it is shown in words).
export const withoutStartCheck = (text) => String(text || '').split('\n').filter((l) => !l.includes(START_MARKER)).join('\n');

// The reason in words.
export const reasonWords = (report) => {
  const r = report || {};
  const p = { name: r.name || '', code: r.exitCode, names: (r.needs || []).map((n) => n.name).join(', ') };
  switch (r.reason) {
    case 'missingSetting': return { key: 'mgmt.services.start.missing', text: '{{name}} does not start: it says it needs {{names}}.', params: p };
    case 'permission': return { key: 'mgmt.services.start.permission', text: '{{name}} does not start: it may not write to a folder it needs. Its data folder may belong to another user than the one the image runs as.', params: p };
    case 'processor': return { key: 'mgmt.services.start.processor', text: '{{name}} does not start: this server\'s processor cannot run this image (a newer instruction set, or another architecture).', params: p };
    case 'memory': return { key: 'mgmt.services.start.memory', text: '{{name}} does not start: it ran out of memory.', params: p };
    case 'unhealthy': return { key: 'mgmt.services.start.unhealthy', text: '{{name}} runs, but its own health check fails.', params: p };
    case 'restarting': return { key: 'mgmt.services.start.restarting', text: '{{name}} keeps stopping and Docker starts it again.', params: p };
    case 'missing': return { key: 'mgmt.services.start.gone', text: '{{name}} was not found.', params: p };
  }
  return { key: 'mgmt.services.start.exited', text: '{{name}} stopped with exit code {{code}}.', params: p };
};

const VAR = /^[A-Z_][A-Z0-9_]{0,63}$/;

// The values the repair proposes: a generated one for a secret, empty for the rest.
export const repairValues = (needs, random) => (needs || [])
  .filter((n) => n && VAR.test(String(n.name || '')))
  .slice(0, 8)
  .map((n) => ({ name: n.name, secret: !!n.secret || isSecretName(n.name), value: (n.secret || isSecretName(n.name)) ? generateSecret(random) : '' }));

// A list of "NAME=value" with the values set (replaced or added), in order.
export const repairEnv = (env, values) => {
  const out = [...(env || [])].map(String);
  (values || []).forEach(({ name, value }) => {
    if (!VAR.test(String(name || ''))) return;
    const line = `${name}=${value}`;
    const at = out.findIndex((l) => l.split('=')[0] === name);
    if (at >= 0) out[at] = line; else out.push(line);
  });
  return out;
};

// The creation's definition with the values set for one container (the
// repair of a creation: created again with the same definition).
export const repairDefinition = (definition, container, values) => {
  const copy = JSON.parse(JSON.stringify(definition || {}));
  Object.entries(copy.services || {}).forEach(([key, service]) => {
    if ((service.container_name || key) !== container) return;
    const env = service.environment;
    if (env && !Array.isArray(env) && typeof env === 'object') {
      (values || []).forEach(({ name, value }) => { if (VAR.test(name)) env[name] = value; });
    } else {
      service.environment = repairEnv(env, values);
    }
  });
  return copy;
};

// Whether the repair can be applied: every variable has a value.
export const repairReady = (values) => (values || []).length > 0 && values.every((v) => String(v.value || '').length > 0);

// Rounds of repair: the image may name a new variable after the first is set.
export const REPAIR_ROUNDS = 3;

// ---- what the image's description says (describe's `hints`) ----------------------------------

// "The image's own password": the shortest secret named …PASSWORD that is
// not about replication, LDAP, mail, a master or a second user.
const SIDE = /(REPLICATION|REPLICA|LDAP|SMTP|MAIL|MASTER|SR_CHECK|INITSCRIPTS|SENTINEL|TLS|SSL|KEYSTORE|TRUSTSTORE|METRICS|EXPORTER|ADMIN_UI|SMB|BACKUP|PASSFILE)/;
export const mainSecret = (env) => (env || [])
  .filter((e) => e && e.secret && !e.allowEmpty && /PASS(WORD)?$/.test(e.name) && !SIDE.test(e.name) && !/_FILE$/.test(e.name))
  .sort((a, b) => a.name.length - b.name.length)[0] || null;

const HINTS_SHOWN = 32;

// The settings the review proposes from the description, for an image
// without recipe: [{ name, type, value, from: 'description', hint, required }].
//   required (the text says so) and the image's own password when the text
//   documents a switch to allow an empty one (Bitnami): proposed, a secret
//   generated; every other variable: optional, empty, its example as a hint.
// Names the image declares itself are left to the declaration; switches that
// allow an empty password are never proposed.
export const hintSettings = (hints, declaredNames, random) => {
  const env = ((hints && hints.env) || []).filter((e) => e && VAR.test(String(e.name || '')) && !e.allowEmpty && !/^FILE__|_FILE$/.test(e.name) && !(declaredNames || []).includes(e.name));
  const main = (hints && hints.env || []).some((e) => e && e.allowEmpty) ? mainSecret(env) : null;
  const out = [];
  env.forEach((e) => {
    const needed = e.required || (main && main.name === e.name);
    const secret = !!e.secret || isSecretName(e.name);
    const hint = e.example || e.default || '';
    if (needed) {
      out.push({ name: e.name, type: secret ? 'secret' : 'needed', value: secret ? generateSecret(random) : '', from: 'description', hint, required: true });
    } else if (out.filter((s) => s.type === 'optional').length < HINTS_SHOWN) {
      out.push({ name: e.name, type: 'optional', value: '', from: 'description', hint, required: false });
    }
  });
  // what is needed first
  return [...out.filter((s) => s.required), ...out.filter((s) => !s.required)];
};

// For an image without recipe: does it look like a database (its name or its
// ports)? Then the review says that Cosmos does not know its required settings.
export const looksLikeDatabase = (image, ports) => {
  const family = serviceFamily(image);
  if (family) return family.database ? family.name : '';
  const found = (ports || []).map(Number).find((p) => SERVICE_PORTS[p] && SERVICE_PORTS[p].database);
  return found ? SERVICE_PORTS[found].protocol : '';
};

// The connection string other apps are given, with placeholders for what
// only the administrator knows (the password is never shown again).
export const connectionTemplate = (classification, host, port) => {
  const name = (classification && classification.name) || '';
  const at = `${host}:${port}`;
  if (/PostgreSQL/.test(name)) return `postgresql://USER:PASSWORD@${at}/DATABASE`;
  if (/MariaDB|MySQL/.test(name)) return `mysql://USER:PASSWORD@${at}/DATABASE`;
  if (/Redis|Valkey/.test(name)) return `redis://:PASSWORD@${at}/0`;
  if (/MongoDB/.test(name)) return `mongodb://USER:PASSWORD@${at}/?authSource=admin`;
  return '';
};

// The port bindings of a container with one more port published on every
// address of the server (as the Network tab sends them: what it has stays).
export const withPublishedPort = (inspected, port, protocol = 'tcp') => {
  const bindings = JSON.parse(JSON.stringify((inspected && inspected.HostConfig && inspected.HostConfig.PortBindings) || {}));
  const key = `${port}/${protocol}`;
  if (!(bindings[key] || []).some((b) => String(b.HostPort) === String(port))) {
    bindings[key] = [...(bindings[key] || []), { HostIp: '', HostPort: String(port) }];
  }
  return bindings;
};

// The network made to let one app reach another (as "Link containers" names it).
export const linkNetworkName = (service, app, suffix) => `link-${service}-${app}-${suffix}`.replace(/[^a-zA-Z0-9_.-]/g, '-').slice(0, 63);

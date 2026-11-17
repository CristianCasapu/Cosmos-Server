// Fork addition (plan 47, I2c): the proposals of a container's edit tabs
// (Docker, Network, Storage), from what the server tells about the container
// (GET /api/servapps/{id}/edit-hints, src/fork_edit_hints.go). Pure rules,
// tested in editHints.test.mjs; the tabs (pages/servapps/containers/
// forkEditTabs.jsx) only show them. As everywhere in the fork's forms, a
// proposal fills what is empty and never replaces what the administrator
// typed or what the container has: the tabs offer it beside the field.
import { proposePlace, volumeKind, placeOf, sizeText } from './dataPlaces.js';

export { sizeText };

// ---- addresses and who can reach a published port ---------------------------

const WILDCARD = ['', '0.0.0.0', '::', '[::]'];
const isLoopback = (ip) => /^127\./.test(ip) || ip === '::1' || ip === '[::1]';
const isPrivate = (ip) => /^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/.test(ip) || /^f[cd][0-9a-f]{2}:/i.test(ip);

// The choice a host address is: 'lan:<ip>' (a home-network address of the
// server), 'all' (every address), 'local' (this server only), 'other' (typed).
export const hostIpChoice = (hostIp, lan = []) => {
  const ip = String(hostIp || '').trim();
  if (WILDCARD.includes(ip)) return 'all';
  if (ip === '127.0.0.1') return 'local';
  if ((lan || []).some((a) => a.address === ip)) return 'lan:' + ip;
  return 'other';
};

// The address a choice stands for ('other' keeps what is typed).
export const hostIpOf = (choice, typed) => {
  if (choice === 'all') return '';
  if (choice === 'local') return '127.0.0.1';
  if (String(choice).startsWith('lan:')) return choice.slice(4);
  return String(typed || '').trim();
};

// Who can reach a port published on that address, as a code:
// home (the home network), everyone (every address: the internet too if the
// router forwards it), server (only programs on this server), address (one
// address of the server that is not a home-network one), none (not published).
export const reachOf = (hostIp, hostPort, lan = []) => {
  if (!String(hostPort || '').trim()) return 'none';
  const ip = String(hostIp || '').trim();
  if (WILDCARD.includes(ip)) return 'everyone';
  if (isLoopback(ip)) return 'server';
  if ((lan || []).some((a) => a.address === ip) || isPrivate(ip)) return 'home';
  return 'address';
};

export const REACH_TEXT = {
  none: 'Not published: only apps that share a network with it reach this port.',
  home: 'Any device of your home network can reach it at {{ip}}:{{port}}. Not the internet, even if the router forwards the port.',
  everyone: 'Anyone who can reach this server can open it at port {{port}}: every device of your home network, and the internet too if the router forwards that port.',
  server: 'Only programs on this server can reach it (127.0.0.1:{{port}}): no other device.',
  address: 'Reachable only at {{ip}}:{{port}}, by whoever can reach that address of the server.',
};

// ---- container ports ------------------------------------------------------------

// The container ports the tab proposes, with where each comes from:
// recipe (Cosmos' recipe of the image), known (an app Cosmos knows), image
// (the image declares it). The first source of a port wins.
export const containerPortOptions = (hints) => {
  const out = [];
  const add = (port, protocol, source, extra = {}) => {
    const number = Number(port);
    if (!(number >= 1 && number <= 65535)) return;
    const proto = protocol === 'udp' ? 'udp' : 'tcp';
    if (out.some((o) => o.port === number && o.protocol === proto)) return;
    out.push({ port: number, protocol: proto, source, ...extra });
  };
  if (!hints) return out;
  ((hints.recipe && hints.recipe.ports) || []).forEach((p) => add(p.port, p.protocol, 'recipe', { role: p.role, name: hints.recipe.name }));
  if (hints.knownApp && hints.knownApp.port) add(hints.knownApp.port, 'tcp', 'known', { name: hints.knownApp.name });
  ((hints.image && hints.image.ports) || []).forEach((text) => {
    const [port, protocol] = String(text).split('/');
    add(port, protocol, 'image');
  });
  return out;
};

const rowKey = (row) => `${Number(row.port)}/${row.protocol === 'udp' ? 'udp' : 'tcp'}`;

const heldBy = (holders, port, protocol) => (holders || []).some((h) => Number(h.port) === Number(port)
  && (h.protocol || 'tcp') === protocol && h.certain !== false);

// The host port proposed for a container port: the server's free one (it
// tried it), else the same number; then moved on past what the form's
// other rows and the known holders take. Returns { port, reason }:
// reason 'free' (the server found it free), 'same' (not tried on the
// server), 'moved' (the same number is taken: the next free one).
export const proposeHostPort = (hints, port, protocol, rows = [], holders = []) => {
  const proto = protocol === 'udp' ? 'udp' : 'tcp';
  const number = Number(port);
  if (!(number >= 1 && number <= 65535)) return null;
  const fromServer = hints && hints.freePorts && hints.freePorts[`${number}/${proto}`];
  const usedByRows = (p) => (rows || []).some((r) => Number(r.hostPort) === p && (r.protocol || 'tcp') === proto);
  let candidate = fromServer || number;
  for (let i = 0; i < 60 && (usedByRows(candidate) || heldBy(holders, candidate, proto)); i++) candidate += 1;
  if (candidate > 65535) return null;
  const listened = !!(hints && hints.portsListened && fromServer);
  return { port: candidate, reason: candidate === number ? (listened ? 'free' : 'same') : 'moved' };
};

// The address a new port is proposed on: the home-network address the
// server's default route uses, else every address.
export const proposedHostIp = (lan) => {
  const list = lan || [];
  const pick = list.find((a) => a.defaultRoute) || list[0];
  return pick ? pick.address : '';
};

// A new row of the port list: the first port the image or its recipe
// names that the container does not publish yet, on the home network, on
// a free host port. Empty fields when nothing is known.
export const newPortRow = (hints, rows = [], holders = []) => {
  const published = new Set((rows || []).filter((r) => String(r.port || '').trim()).map(rowKey));
  const next = containerPortOptions(hints).find((o) => !published.has(`${o.port}/${o.protocol}`));
  const hostIp = proposedHostIp(hints && hints.lan);
  if (!next) return { port: '', protocol: 'tcp', hostPort: '', hostIp, proposed: null };
  const host = proposeHostPort(hints, next.port, next.protocol, rows, holders);
  return {
    port: String(next.port), protocol: next.protocol, hostPort: host ? String(host.port) : '', hostIp,
    proposed: { source: next.source, name: next.name, reason: host && host.reason },
  };
};

// ---- DNS ----------------------------------------------------------------------------

const listText = (list) => (list || []).join(', ');

// The DNS choices: default (Docker's: the server's own resolvers), router
// (the home router, which usually answers DNS), resolver (the server's
// resolvers named on the container), other (typed).
export const dnsOptions = (hints) => {
  const dns = (hints && hints.dns) || {};
  const options = [{ value: 'default', servers: [], source: dns.source || '', shows: dns.servers || [] }];
  if (dns.router) options.push({ value: 'router', servers: [dns.router] });
  if ((dns.servers || []).length && listText(dns.servers) !== String(dns.router || '')) {
    options.push({ value: 'resolver', servers: dns.servers.slice(0, 3) });
  }
  options.push({ value: 'other', servers: null });
  return options;
};

export const dnsList = (text) => String(text || '').split(',').map((s) => s.trim()).filter(Boolean);

// Which choice a DNS text is.
export const dnsChoice = (text, hints) => {
  const list = listText(dnsList(text));
  if (list === '') return 'default';
  const found = dnsOptions(hints).find((o) => o.servers && o.value !== 'default' && listText(o.servers) === list);
  return found ? found.value : 'other';
};

const IPV4 = /^\d{1,3}(\.\d{1,3}){3}$/;
// The DNS text's problem, or null: each server must be an IP address, at most 8.
export const dnsProblem = (text) => {
  const list = dnsList(text);
  if (list.length > 8) return 'tooMany';
  const bad = list.find((s) => !(IPV4.test(s) && s.split('.').every((p) => Number(p) <= 255)) && !(/^[0-9a-f:.]+$/i.test(s) && s.includes(':')));
  return bad ? 'notIp' : null;
};

// ---- network mode ----------------------------------------------------------------------

// The choice a network mode is: 'default' (its own network on Docker's
// bridge), 'net:<name>' (a network of Docker), 'container' (another app's
// network), 'host', 'none', 'other' (typed).
export const networkModeChoice = (mode, networks = []) => {
  const m = String(mode || '').trim();
  if (m === '' || m === 'default' || m === 'bridge') return 'default';
  if (m === 'host') return 'host';
  if (m === 'none') return 'none';
  if (/^(container|service):/.test(m)) return 'container';
  if ((networks || []).some((n) => n.Name === m)) return 'net:' + m;
  return 'other';
};

// The networks offered as a mode: user networks, not Docker's own three.
export const networkModeNetworks = (networks = []) => (networks || [])
  .filter((n) => n && n.Name && !['bridge', 'host', 'none'].includes(n.Name))
  .map((n) => n.Name)
  .sort();

// ---- storage ---------------------------------------------------------------------------

const cleanTarget = (t) => String(t || '').trim().replace(/\/+$/, '') || '/';

// The folders the image (or its recipe) keeps data in that the container
// does not mount: [{ target, anonymous }] (anonymous: Docker keeps it in a
// volume of its own now, lost when the container is made again).
export const declaredMissing = (hints, volumes = []) => {
  if (!hints) return [];
  const mounted = new Set((volumes || []).map((v) => cleanTarget(v.Target !== undefined ? v.Target : v.target)));
  const declared = [...((hints.image && hints.image.volumes) || []), ...((hints.recipe && hints.recipe.volumes) || [])].map(cleanTarget);
  const anonymous = new Set(((hints.anonymous) || []).map(cleanTarget));
  const out = [];
  declared.forEach((target) => {
    if (target === '/' || mounted.has(target) || out.some((o) => o.target === target)) return;
    out.push({ target, anonymous: anonymous.has(target) });
  });
  return out;
};

const folderName = (target) => (String(target).replace(/\/+$/, '').split('/').pop() || 'data')
  .replace(/[^A-Za-z0-9_.-]+/g, '-').replace(/^[.-]+/, '') || 'data';
const appFolder = (app) => String(app || 'app').replace(/[^A-Za-z0-9_.-]+/g, '-').replace(/^[.-]+/, '') || 'app';

// Where a declared folder is proposed: the place the creation flow would
// give the volume Docker makes for it (utils/dataPlaces.js: large data on the
// data disk with the most room, settings moved off the system disk), else a
// folder in the data path, else a named volume (Docker keeps it when the
// container is made again). Returns { type, source, reason, place }.
export const proposeDeclared = (target, hints, app) => {
  const plan = { dataPath: hints && hints.dataPath, places: (hints && hints.places) || [] };
  const name = appFolder(app);
  const volume = `${name}_${folderName(target)}`;
  const choice = proposePlace({ service: name, type: 'volume', source: volume, target, kind: volumeKind(target) }, plan, name);
  if (choice.path) return { type: 'bind', source: choice.path, reason: choice.reason, place: placeOf(choice.path, plan.places) };
  if (plan.dataPath) {
    const source = `${plan.dataPath.replace(/\/$/, '')}/${name}/${folderName(target)}`;
    return { type: 'bind', source, reason: 'config', place: placeOf(source, plan.places) };
  }
  return { type: 'volume', source: volume, reason: 'volume', place: null };
};

// The data places a new mount's source may be chosen among: the data path
// and the data disks, each with the folder the app would get there.
export const sourcePlaces = (hints, target, app) => ((hints && hints.places) || [])
  .filter((p) => p.kind === 'data-path' || (p.kind === 'disk' && !p.osDisk))
  .map((p) => ({ ...p, source: `${p.path.replace(/\/$/, '')}/${appFolder(app)}${target ? '/' + folderName(target) : ''}` }));

// What is said about a bind's folder, as a code with its parameters:
// missing, file, outside, link, invalid, unknown, notWritable, writable.
export const folderFinding = (folder, runsAs) => {
  if (!folder) return null;
  const params = { owner: folder.owner || '', user: (runsAs && runsAs.text) || '', mode: folder.mode || '' };
  if (folder.state === 'ok') {
    if (folder.writable === 'no') return { code: 'notWritable', severity: 'warning', params };
    return { code: folder.writable === 'yes' ? 'writable' : 'unknownUser', severity: 'info', params };
  }
  if (folder.state === 'missing') return { code: 'missing', severity: 'warning', params };
  if (folder.state === 'file') return { code: 'file', severity: 'info', params };
  if (folder.state === 'link') return { code: 'link', severity: 'warning', params };
  return { code: folder.state || 'unknown', severity: 'info', params };
};

// ---- the user, PUID/PGID ------------------------------------------------------------

// The owner most of the container's data folders have ("1000:1000"), "" when
// none is known; root's folders count only when no other owner exists.
export const dataFoldersOwner = (hints) => {
  const counts = {};
  ((hints && hints.folders) || []).forEach((f) => {
    if (f.state === 'ok' && f.owner && !f.readOnly) counts[f.owner] = (counts[f.owner] || 0) + 1;
  });
  const owners = Object.keys(counts).filter((o) => o !== '0:0' || Object.keys(counts).length === 1);
  owners.sort((a, b) => counts[b] - counts[a] || a.localeCompare(b));
  return owners[0] || '';
};

// The user choices of the Docker tab: image (as the image says: its USER or
// root), owner (the owner of its data folders), other (typed). Not for a
// linuxserver.io-style image: it must start as root and takes PUID/PGID.
export const userOptions = (hints) => {
  const image = String((hints && hints.image && hints.image.user) || '');
  const owner = dataFoldersOwner(hints);
  const options = [{ value: 'image', user: image }];
  if (owner && owner !== '0:0' && owner !== image && !(hints && hints.linuxserver)) options.push({ value: 'owner', user: owner });
  options.push({ value: 'other', user: null });
  return options;
};

export const userChoice = (user, hints) => {
  const u = String(user || '').trim();
  const found = userOptions(hints).find((o) => o.user !== null && o.user === u);
  return found ? found.value : 'other';
};

const envOf = (envVars, name) => {
  const found = (envVars || []).find((e) => e.key === name);
  return found ? String(found.value || '').trim() : undefined;
};

// When a linuxserver.io-style image writes as other than the owner of its
// data folders: { owner, puid, pgid, now } to propose PUID/PGID = the owner;
// null when they agree or nothing is known. `envVars` are the form's rows.
export const puidMismatch = (hints, envVars) => {
  if (!hints || !hints.linuxserver) return null;
  const owner = dataFoldersOwner(hints);
  if (!owner || owner === '0:0') return null;
  const [uid, gid] = owner.split(':');
  const puid = envOf(envVars, 'PUID');
  const pgid = envOf(envVars, 'PGID');
  const nowUid = puid === undefined ? '911' : puid;
  const nowGid = pgid === undefined ? '911' : pgid;
  if (nowUid === uid && nowGid === gid) return null;
  return { owner, puid: uid, pgid: gid, now: `${nowUid}:${nowGid}`, defaulted: puid === undefined };
};

// The form's variables with PUID/PGID set to the fix (kept in place, or added).
export const withPuid = (envVars, fix) => {
  const rows = (envVars || []).map((e) => ({ ...e }));
  [['PUID', fix.puid], ['PGID', fix.pgid]].forEach(([name, value]) => {
    const row = rows.find((e) => e.key === name);
    if (row) row.value = value;
    else rows.push({ key: name, value });
  });
  return rows;
};

// When a user is set on a linuxserver.io-style image: it breaks its start.
export const userOnLinuxserver = (hints, user) => !!(hints && hints.linuxserver && String(user || '').trim() !== ''
  && !['0', 'root', '0:0'].includes(String(user).trim()));

// ---- limits ----------------------------------------------------------------------------

const GIB = 1024 * 1024 * 1024;
const MIB = 1024 * 1024;
const memText = (bytes) => (bytes % GIB === 0 ? `${bytes / GIB}g` : `${Math.round(bytes / MIB)}m`);

// The memory choices: recipe (the recipe's hint), sizes under half the
// server's memory, none (no limit), other (typed). value is the form's text.
export const memoryOptions = (hints) => {
  const total = (hints && hints.server && hints.server.memory) || 0;
  const options = [];
  const recipe = hints && hints.recipe && hints.recipe.memoryLimit;
  if (recipe && (!total || recipe < total)) options.push({ value: memText(recipe), kind: 'recipe', hint: hints.recipe.memory, name: hints.recipe.name });
  [512 * MIB, GIB, 2 * GIB, 4 * GIB, 8 * GIB].forEach((bytes) => {
    if (total && bytes > total / 2) return;
    if (options.some((o) => o.value === memText(bytes))) return;
    options.push({ value: memText(bytes), kind: 'size' });
  });
  options.push({ value: '', kind: 'none' });
  return options;
};

// The CPU choices: 1, 2, half of the server's, none, other.
export const cpuOptions = (hints) => {
  const cpus = (hints && hints.server && hints.server.cpus) || 0;
  const values = [0.5, 1, 2, cpus >= 4 ? Math.floor(cpus / 2) : 0].filter((v, i, all) => v > 0 && (!cpus || v <= cpus) && all.indexOf(v) === i);
  return [...values.map((v) => ({ value: String(v), kind: v === Math.floor(cpus / 2) && cpus >= 4 ? 'half' : 'number' })), { value: '0', kind: 'none' }];
};

export const limitChoice = (value, options) => {
  const v = String(value === undefined || value === null ? '' : value).trim().toLowerCase();
  if (v === '' || v === '0') return 'none';
  const found = options.find((o) => o.kind !== 'none' && String(o.value) === v);
  return found ? 'v:' + found.value : 'other';
};

// ---- restart policy --------------------------------------------------------------------

export const RESTART = [
  { value: 'unless-stopped', label: 'Unless stopped', hint: 'Comes back after a crash and after the server restarts, unless you stopped it yourself. Usual for an app.' },
  { value: 'always', label: 'Always', hint: 'Comes back after a crash and after the server restarts, even when you had stopped it.' },
  { value: 'on-failure', label: 'After a crash', hint: 'Starts again only when it stops with an error; not after the server restarts.' },
  { value: 'no', label: 'Never', hint: 'Stays stopped when it stops or the server restarts: you start it yourself.' },
];

// ---- variables and devices -------------------------------------------------------------

// The variable names offered when adding one: the recipe's, then the
// image description's, then the image's own; never one the container has.
export const envSuggestions = (hints, describeHints, envVars) => {
  const have = new Set((envVars || []).map((e) => e.key));
  const out = [];
  const add = (name, source) => {
    if (!name || have.has(name) || out.some((o) => o.name === name) || !/^[A-Za-z_][A-Za-z0-9_.-]*$/.test(name)) return;
    out.push({ name, source });
  };
  ((hints && hints.recipe && hints.recipe.env) || []).forEach((n) => add(n, 'recipe'));
  ((describeHints && describeHints.env) || []).forEach((e) => add(e && (e.name || e), 'description'));
  if (hints && hints.linuxserver) ['PUID', 'PGID', 'TZ'].forEach((n) => add(n, 'linuxserver'));
  return out.slice(0, 24);
};

// The devices offered: the server's, not already given to the container.
export const deviceOptions = (hints, devices) => {
  const have = new Set((devices || []).map((d) => d.key));
  return ((hints && hints.devices) || []).filter((d) => !have.has(d.path) && !have.has(d.node));
};

// A device row for a detected device: the stable name on the server, the
// node's name in the container.
export const deviceRow = (device) => ({ key: device.path, value: device.node || device.path });

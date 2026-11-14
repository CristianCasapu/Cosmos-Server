// Fork addition: the rules and proposals of the small forms no earlier round
// reworked: a new Docker network, a new volume, the market sources and an
// entry of the SmartShield whitelist. Plain functions: see smallForms.test.mjs.
// Texts are ['key', 'English', variables]. The server checks the same
// (src/docker/fork_network_create.go, src/configapi/fork_small_lists.go;
// Docker itself for volume names).

const NAME = /^[A-Za-z0-9][A-Za-z0-9_.-]*$/;

// A network or volume name: Docker's rule, and not one that exists.
export const dockerNameProblem = (text, taken = [], kind = 'network') => {
  const name = String(text || '').trim();
  if (!name) return null;
  if (!NAME.test(name) || name.length > 63 || (kind === 'volume' && name.length < 2)) {
    return ['mgmt.servApps.fork.nameRule', 'Letters, digits, dots, dashes and underscores, starting with a letter or a digit, for example media-net'];
  }
  if (taken.includes(name)) {
    return kind === 'volume'
      ? ['mgmt.servApps.fork.volumeTaken', 'A volume with this name exists already']
      : ['mgmt.servApps.fork.networkTaken', 'A network with this name exists already'];
  }
  return null;
};

const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;
const v4number = (ip) => ip.split('.').reduce((n, part) => n * 256 + Number(part), 0);

export const subnetProblem = (text) => {
  const value = String(text || '').trim();
  if (!value) return null;
  const [ip, bits, extra] = value.split('/');
  if (extra !== undefined || !IPV4.test(ip) || !/^\d{1,2}$/.test(bits || '') || Number(bits) > 32) {
    return ['mgmt.servApps.fork.subnetForm', 'Write it like 172.30.0.0/24, or leave it empty for Docker to pick a free one'];
  }
  const size = 2 ** (32 - Number(bits));
  if (v4number(ip) % size !== 0) {
    return ['mgmt.servApps.fork.subnetStart', 'A network starts at its first address: for /{{bits}} that would be {{start}}', {
      bits, start: [24, 16, 8, 0].map((s) => (Math.floor(v4number(ip) / size) * size >>> s) & 255).join('.') }];
  }
  return null;
};

export const interfaceProblem = (text) => {
  const value = String(text || '').trim();
  if (!value) return ['mgmt.servApps.fork.parentMissing', 'Enter the server\'s network card, for example eno1 or eth0'];
  return /^[A-Za-z0-9][A-Za-z0-9_.:@-]{0,14}$/.test(value) ? null : ['mgmt.servApps.fork.parentForm', 'A network card name is short, for example eno1, eth0 or enp3s0'];
};

// The server's network cards on a home network, from the HTTP check, for a
// macvlan network: [{interface, network, ip}].
export const lanCards = (check) => ((check && check.server && check.server.addresses) || [])
  .filter((a) => a.interface && a.network && IPV4.test(String(a.ip)))
  .filter((a, i, all) => all.findIndex((b) => b.interface === a.interface) === i);

// A name for a new network or volume, free among the taken ones.
export const freeName = (base, taken = []) => {
  if (!taken.includes(base)) return base;
  for (let i = 2; i < 100; i += 1) if (!taken.includes(`${base}-${i}`)) return `${base}-${i}`;
  return base;
};

// --- market sources -------------------------------------------------------

export const marketUrlProblem = (text) => {
  const value = String(text || '').trim();
  if (!value) return ['mgmt.market.fork.urlMissing', 'Enter the address of the market\'s list, for example https://example.com/cosmos-servapps.json'];
  let url = null;
  try { url = new URL(value); } catch (e) { url = null; }
  if (!url || !/^https?:$/.test(url.protocol) || !url.hostname || url.username || url.password || /\s/.test(value)) {
    return ['mgmt.market.fork.urlForm', 'A web address starting with https://, for example https://example.com/cosmos-servapps.json'];
  }
  if (url.protocol === 'http:') return null;
  return null;
};

// A name proposed from the address: its host without "www." and the domain end.
export const marketNameFromUrl = (text, taken = []) => {
  let host = '';
  try { host = new URL(String(text || '').trim()).hostname; } catch (e) { return ''; }
  const parts = host.replace(/^www\./, '').split('.');
  const base = (parts.length > 2 && /^(raw|cdn|market|apps|servapps)$/.test(parts[0]) ? parts[1] : parts[0]).replace(/[^A-Za-z0-9-]/g, '').toLowerCase();
  if (!base) return '';
  const lower = taken.map((n) => String(n).toLowerCase());
  return freeName(base, ['cosmos-cloud', ...lower]);
};

// --- SmartShield whitelist ------------------------------------------------

// Entries proposed from what the server saw: the address in use and the
// local network the server is on, each with a label; those listed are left out.
export const whitelistEntryProposals = (check, entries = []) => {
  const listed = entries.map((e) => String(e.IP || '').trim());
  const out = [];
  if (!check || !check.you || !check.you.address) return out;
  const you = check.you.address;
  const network = ((check.server && check.server.addresses) || []).find((a) => a.network && IPV4.test(you) && IPV4.test(String(a.ip)) && (() => {
    const [base, bits] = a.network.split('/');
    const size = 2 ** (32 - Number(bits));
    return Math.floor(v4number(you) / size) === Math.floor(v4number(base) / size);
  })());
  if (network && !listed.includes(network.network)) {
    out.push({ IP: network.network, Label: ['mgmt.shield.fork.labelHome', 'Home network'], source: ['mgmt.shield.fork.fromNetwork', 'The local network you and the server are on'] });
  }
  if (!listed.includes(you)) {
    out.push({ IP: you, Label: ['mgmt.shield.fork.labelYou', 'This device'], source: check.you.local
      ? ['mgmt.shield.fork.fromYouLocal', 'The address of the device you use now, on the local network']
      : ['mgmt.shield.fork.fromYou', 'The address the server sees you at now; at home it often changes from day to day'] });
  }
  return out;
};

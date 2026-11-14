// Fork addition: the logic of the API token form (pages/config/forkTokenForm.jsx),
// pure and tested (tokenForm.test.mjs). The server checks the fields again
// (src/configapi/fork_api_tokens.go) and never lets a token hold more than its
// creator (utils.CanGrant).

// The permissions a token can hold (src/utils/types.go), each with what it
// lets the token do, and whether it amounts to full control of the server.
// `full`: for Resources it is a property of Docker (a container can be given
// the host's disk); for Users and Configuration it is the security audit of
// 2026-11-13: their permission checks are not tight enough yet, so until that
// is fixed such a token must be treated as an administrator's.
export const PERMISSIONS = [
  { id: 1, label: ['mgmt.config.apiTokens.fork.p1', 'Read the logs'], what: ['mgmt.config.apiTokens.fork.p1What', 'The server\'s logs and events.'] },
  { id: 2, label: ['mgmt.config.apiTokens.fork.p2', 'Run the server'], what: ['mgmt.config.apiTokens.fork.p2What', 'Restart, update and stop Cosmos and the server, open a terminal on it.'], full: 'server' },
  { id: 10, label: ['mgmt.config.apiTokens.fork.p10', 'See the users'], what: ['mgmt.config.apiTokens.fork.p10What', 'The list of accounts, their roles and email addresses.'] },
  { id: 11, label: ['mgmt.config.apiTokens.fork.p11', 'Manage the users'], what: ['mgmt.config.apiTokens.fork.p11What', 'Create, change and delete accounts, invite, reset two-factor.'], full: 'audit' },
  { id: 20, label: ['mgmt.config.apiTokens.fork.p20', 'See the apps and disks'], what: ['mgmt.config.apiTokens.fork.p20What', 'Containers, storage, backups, scheduled jobs, metrics.'] },
  { id: 21, label: ['mgmt.config.apiTokens.fork.p21', 'Manage the apps and disks'], what: ['mgmt.config.apiTokens.fork.p21What', 'Create, change, start and delete containers; mount and format disks; backups and jobs.'], full: 'docker' },
  { id: 30, label: ['mgmt.config.apiTokens.fork.p30', 'Read the settings'], what: ['mgmt.config.apiTokens.fork.p30What', 'Every setting, with passwords and keys masked.'] },
  { id: 31, label: ['mgmt.config.apiTokens.fork.p31', 'Change the settings'], what: ['mgmt.config.apiTokens.fork.p31What', 'URLs, domains, roles, the admin restrictions and every other setting.'], full: 'audit' },
  { id: 40, label: ['mgmt.config.apiTokens.fork.p40', 'Read secrets'], what: ['mgmt.config.apiTokens.fork.p40What', 'Passwords and keys in the settings and the environment variables of containers, unmasked.'], secret: true },
];

// What the server gives a token with "read only" (api_tokens.go) and by default.
export const READ_ONLY = [1, 10, 20, 30];
export const FULL = [1, 2, 10, 11, 20, 21, 30, 31, 40];

const sorted = (list) => [...new Set((list || []).map(Number))].filter((p) => p < 100).sort((a, b) => a - b);
const same = (a, b) => JSON.stringify(sorted(a)) === JSON.stringify(sorted(b));

export const presetOf = (permissions) => {
  if (same(permissions, READ_ONLY)) return 'read';
  if (same(permissions, FULL)) return 'full';
  return 'custom';
};

export const permissionsOf = (preset, current) => {
  if (preset === 'read') return [...READ_ONLY];
  if (preset === 'full') return [...FULL];
  return sorted(current);
};

// Ticking "manage" also needs "see": the server checks the read permission for
// the pages a manage action is taken from.
export const toggle = (permissions, id) => {
  const set = new Set(sorted(permissions));
  if (set.has(id)) {
    set.delete(id);
    if (id % 10 === 0 && id < 40 || id === 1) set.delete(id + 1);
  } else {
    set.add(id);
    if (id % 10 === 1 && id > 1) set.add(id - 1);
    if (id === 2) set.add(1);
  }
  return sorted([...set]);
};

// The permissions in a list that amount to full control, and why.
export const fullControl = (permissions) => PERMISSIONS.filter((p) => p.full && sorted(permissions).includes(p.id));

export const fullControlText = (permission) => {
  switch (permission.full) {
    case 'server':
      return ['mgmt.config.apiTokens.fork.fullServer', '"{{name}}" is full control of the server: a terminal on it runs as the server\'s administrator.'];
    case 'docker':
      return ['mgmt.config.apiTokens.fork.fullDocker', '"{{name}}" amounts to full control: a container can be given the server\'s whole disk.'];
    default:
      return ['mgmt.config.apiTokens.fork.fullAudit', '"{{name}}" must be treated as full control for now: a security review found that its permission checks are not tight enough yet.'];
  }
};

export const EXPIRY = [7, 30, 90, 365, 0];
export const PROPOSED_EXPIRY = 90;

// Where the token may be used from, as one choice, proposed from the address
// the admin is on (what the server sees: POST /api/config/http-check).
export const placeChoices = (check) => {
  const you = check && check.you && check.you.address;
  const network = you && ((check.server && check.server.addresses) || []).find((a) => a.network && inV4Range(you, a.network));
  const choices = [];
  if (network) choices.push({ value: 'network', list: [network.network], label: ['mgmt.config.apiTokens.fork.placeNetwork', 'This network ({{range}})', { range: network.network }] });
  if (you) choices.push({ value: 'device', list: [you], label: ['mgmt.config.apiTokens.fork.placeDevice', 'This device ({{address}})', { address: you }] });
  choices.push({ value: 'anywhere', list: [], label: ['mgmt.config.apiTokens.fork.placeAnywhere', 'Anywhere'] });
  choices.push({ value: 'other', list: null, label: ['mgmt.config.apiTokens.fork.placeOther', 'Addresses I enter'] });
  return choices;
};

export const placeOf = (list, choices) => {
  const text = (list || []).join(',');
  const found = choices.find((c) => c.list && c.list.join(',') === text);
  return found ? found.value : 'other';
};

const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;
const v4 = (ip) => ip.split('.').reduce((n, part) => n * 256 + Number(part), 0);
const inV4Range = (ip, cidr) => {
  const [address, bits] = String(cidr).split('/');
  if (!IPV4.test(ip) || !IPV4.test(address)) return false;
  const size = 2 ** (32 - Number(bits));
  return Math.floor(v4(ip) / size) === Math.floor(v4(address) / size);
};

// The name of a token: what the server accepts (api_tokens.go: 1–64 letters,
// digits, dash, underscore).
export const nameProblem = (name, taken) => {
  const text = String(name || '');
  if (text === '') return null;
  if (!/^[A-Za-z0-9_-]{1,64}$/.test(text)) return ['mgmt.config.apiTokens.fork.nameRule', 'Letters, digits, - and _ only, at most 64, for example home-assistant'];
  if ((taken || []).includes(text)) return ['mgmt.config.apiTokens.fork.nameTaken', 'A token has this name already'];
  return null;
};

// Fork addition: access to the server a definition asks for that needs the
// admin's yes, item by item. The same rules as the server
// (src/docker/fork_sensitive_access.go), which refuses the creation without
// the confirmation; both are tested against
// src/docker/testdata/sensitive_cases.json.

const SENSITIVE_DIRS = [
  '/etc', '/root', '/boot', '/proc', '/sys', '/dev', '/usr', '/bin', '/sbin',
  '/lib', '/lib32', '/lib64', '/libx32', '/run', '/var/run',
  '/var/lib/docker', '/var/lib/containerd', '/var/lib/containers',
  '/opt/cosmos', '/var/lib/cosmos',
];

const SOCKETS = ['docker.sock', 'containerd.sock', 'podman.sock', 'crio.sock'];

const CAPS = ['ALL', 'SYS_ADMIN', 'NET_ADMIN', 'SYS_PTRACE', 'SYS_MODULE', 'SYS_RAWIO', 'SYS_BOOT', 'DAC_READ_SEARCH',
  'SYS_TIME', 'BPF', 'PERFMON', 'MAC_ADMIN', 'MAC_OVERRIDE'];

// path.Clean for absolute paths; "" for anything else.
export const cleanPath = (source) => {
  const value = String(source || '').trim();
  if (!value.startsWith('/')) return '';
  const parts = [];
  value.split('/').forEach((part) => {
    if (part === '' || part === '.') return;
    if (part === '..') parts.pop();
    else parts.push(part);
  });
  return '/' + parts.join('/');
};

const within = (p, dir) => p === dir || p.startsWith(dir.replace(/\/$/, '') + '/');

// "socket", "bind" or "" for a bind source.
export const sensitiveBind = (source, extra = []) => {
  const p = cleanPath(source);
  if (!p) return '';
  if (SOCKETS.includes(p.split('/').pop())) return 'socket';
  if (p === '/') return 'bind';
  if (p.split('/').some((part) => part === '.ssh' || part === '.gnupg')) return 'bind';
  for (const raw of [...SENSITIVE_DIRS, ...(extra || [])]) {
    const dir = cleanPath(raw);
    if (!dir || dir === '/') continue;
    if (within(p, dir) || within(dir, p)) return 'bind';
  }
  return '';
};

const mountType = (volume) => volume.type || volume.Type || '';
const mountSource = (volume) => volume.source !== undefined ? volume.source : volume.Source;

// The items of a definition, as the server names them ("<service>|<kind>|<value>").
export const sensitiveAccess = (definition, extra = []) => {
  const services = (definition && definition.services) || {};
  const items = [];
  Object.keys(services).sort().forEach((key) => {
    const service = services[key] || {};
    (service.volumes || []).forEach((volume) => {
      if (!volume || typeof volume !== 'object') return;
      const type = mountType(volume);
      const source = String(mountSource(volume) || '');
      if (type !== 'bind' && type !== '') return;
      if (type === '' && !source.startsWith('/')) return;
      const kind = sensitiveBind(source, extra);
      if (kind) items.push({ service: key, kind, value: cleanPath(source) });
    });
    if (service.privileged) items.push({ service: key, kind: 'privileged', value: 'true' });
    const labels = service.labels || {};
    const mode = labels['cosmos-force-network-mode'] || service.network_mode;
    if (mode === 'host') items.push({ service: key, kind: 'network-host', value: 'host' });
    (service.cap_add || []).forEach((cap) => {
      const name = String(cap).trim().toUpperCase().replace(/^CAP_/, '');
      if (CAPS.includes(name)) items.push({ service: key, kind: 'cap', value: name });
    });
    (service.security_opt || []).forEach((option) => {
      const value = String(option).trim();
      if (/[=:]unconfined$/.test(value)) items.push({ service: key, kind: 'unconfined', value });
    });
    // Fork addition (plan 40): a device of the server, used directly
    (service.devices || []).forEach((device) => {
      const host = String(device).split(':')[0].trim();
      if (host) items.push({ service: key, kind: 'device', value: cleanPath(host) });
    });
  });
  return items.map((item) => ({ ...item, id: item.service + '|' + item.kind + '|' + item.value }));
};

export const TEXT = {
  socket: '{{service}} gets the container runtime\'s socket {{value}}: it can start, stop and change every container, which means full control of the server.',
  bind: '{{service}} gets the server folder {{value}}, which holds the system, Cosmos or other apps\' data.',
  privileged: '{{service}} runs in privileged mode: it can do everything the server can.',
  'network-host': '{{service}} uses the server\'s own network: every port it opens is open on the server, without Cosmos in front.',
  cap: '{{service}} gets the capability {{value}}, which reaches outside its container.',
  unconfined: '{{service}} turns a protection off ({{value}}).',
  device: '{{service}} gets the server\'s device {{value}} (a disk, a graphics card, a USB device…) and uses it directly.',
};

export const sentence = (item) => ({ key: 'mgmt.servapps.sensitive.' + item.kind, text: TEXT[item.kind], params: { service: item.service, value: item.value } });

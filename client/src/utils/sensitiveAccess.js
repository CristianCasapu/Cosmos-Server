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
  // plan 47, second review M5: cron, other container and VM managers' state, systemd's
  '/var/spool/cron', '/var/spool/anacron', '/var/lib/kubelet', '/var/lib/lxc', '/var/lib/lxd', '/var/lib/incus',
  '/var/lib/libvirt', '/var/lib/systemd', '/var/lib/machines',
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
  // plan 47, second review M5: /home and a user's whole home
  if (p === '/home' || /^\/home\/[^/]+$/.test(p)) return 'bind';
  for (const raw of [...SENSITIVE_DIRS, ...(extra || [])]) {
    const dir = cleanPath(raw);
    if (!dir || dir === '/') continue;
    if (within(p, dir) || within(dir, p)) return 'bind';
  }
  return '';
};

const mountType = (volume) => volume.type || volume.Type || '';
const mountSource = (volume) => volume.source !== undefined ? volume.source : volume.Source;

// A key of a Docker object in any case (Go reads JSON keys without case).
const field = (object, name) => {
  if (!object || typeof object !== 'object') return undefined;
  const key = Object.keys(object).find((k) => k.toLowerCase() === name.toLowerCase());
  return key === undefined ? undefined : object[key];
};

// The host path a volume's local driver binds ({type: none, o: bind,
// device}), "" for none (plan 47 review; fork_volume_options.go).
export const volumeBindDevice = (volume) => {
  if (mountType(volume) !== 'volume') return '';
  const driver = field(field(volume, 'VolumeOptions'), 'DriverConfig');
  if (!driver) return '';
  const name = String(field(driver, 'Name') || '').trim();
  if (name !== '' && name !== 'local') return '';
  const options = field(driver, 'Options') || {};
  if (!Object.keys(options).length) return '';
  const device = String(options.device || '').trim();
  const kind = String(options.type || '').trim();
  const binds = String(options.o || '').split(',').map((w) => w.trim()).some((w) => w === 'bind' || w === 'rbind');
  if (binds || kind === 'none' || (kind === '' && device.startsWith('/'))) return device || '/';
  return '';
};

export const sharedPropagation = (volume) => {
  const propagation = field(field(volume, 'BindOptions'), 'Propagation');
  return propagation === 'shared' || propagation === 'rshared';
};

// A security option that turns a protection off or replaces Docker's profile (plan 47, second review M5).
const weakSecurityOption = (option) => {
  const o = String(option).toLowerCase().replace(/:/g, '=');
  return o === 'label=disable' || (o.startsWith('seccomp=') && o !== 'seccomp=default' && o !== 'seccomp=builtin') || o === 'no-new-privileges=false';
};

// The items of a definition, as the server names them ("<service>|<kind>|<value>").
export const sensitiveAccess = (definition, extra = []) => {
  const services = (definition && definition.services) || {};
  const items = [];
  Object.keys(services).sort().forEach((key) => {
    const service = services[key] || {};
    (service.volumes || []).forEach((volume) => {
      if (!volume || typeof volume !== 'object') return;
      // plan 47 review: a volume binding a host path is that path; shared propagation needs a tick
      const device = volumeBindDevice(volume);
      if (device) {
        const kind = sensitiveBind(device, extra);
        if (kind) items.push({ service: key, kind, value: cleanPath(device) });
      }
      if (sharedPropagation(volume)) items.push({ service: key, kind: 'propagation', value: cleanPath(String(mountSource(volume) || '')) });
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
    // plan 47, second review M5: another app's network
    if (/^(container|service):./.test(String(mode || ''))) items.push({ service: key, kind: 'network-container', value: String(mode).split(':').slice(1).join(':') });
    (service.cap_add || []).forEach((cap) => {
      const name = String(cap).trim().toUpperCase().replace(/^CAP_/, '');
      if (CAPS.includes(name)) items.push({ service: key, kind: 'cap', value: name });
    });
    (service.security_opt || []).forEach((option) => {
      const value = String(option).trim();
      if (/[=:]unconfined$/.test(value) || weakSecurityOption(value)) items.push({ service: key, kind: 'unconfined', value });
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
  propagation: '{{service}} gets the server folder {{value}} with shared propagation: what it mounts inside appears on the server.',
  'network-container': '{{service}} uses the network of the app {{value}}: it sees what that app sees, the server\'s own network when that app uses it.',
  nested: '{{service}} gets the folder {{value}}, which lies inside a folder {{detail}} can write: that app could turn it into a link to any folder of the server before the next start.',
  linkinto: '{{service}} gets the folder {{value}}, reached through a link, inside the folder of {{detail}}.',
};

export const sentence = (item) => ({ key: 'mgmt.servapps.sensitive.' + item.kind, text: TEXT[item.kind] || '{{service}}: {{value}}', params: { service: item.service, value: item.value, detail: item.detail || '' } });

// The panel's items and the server's (POST /api/install/sensitive: links
// followed, existing volumes, folders other apps can write), each once.
export const withServerItems = (local, server) => {
  const out = [...(local || [])];
  (server || []).forEach((item) => {
    const id = item.service + '|' + item.kind + '|' + item.value;
    if (!out.some((o) => o.id === id)) out.push({ ...item, id });
  });
  return out;
};

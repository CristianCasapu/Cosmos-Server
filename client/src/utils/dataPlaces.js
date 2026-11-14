// Fork addition: where an app's data goes, proposed per volume from the
// server's real disks (POST /api/install/plan, src/fork_install_plan.go).
//
// Each folder an app keeps data in is "config" (settings, databases: small,
// read often) or "bulk" (media, downloads, photos: large). The proposal moves
// only what would otherwise land on the disk the system runs from while a
// data disk exists, and bulk data kept in a Docker volume (Docker's root is
// rarely where media belongs):
//   config → <data path>/<app>/<folder>, or, when the data path itself is on
//            the system disk, <data disk>/cosmos-storage/<app>/<folder>
//   bulk   → <data disk with the most free space>/<app>/<folder>
// Volumes the template freezes are never moved; nothing is moved onto the
// system disk; every proposal can be changed (the review's door).
// Tested in dataPlaces.test.mjs.

const BULK = /^\/(media|movies|films|tv|shows|series|music|audio|photos|pictures|images|videos?|downloads?|torrents?|books|ebooks|comics|audiobooks|podcasts|library|libraries|recordings|storage|share|shares|nas|archive|backups?|files|documents|data\/(media|downloads|movies|tv|music|photos))(\/|$)/i;

// "bulk" or "config" for the folder a volume is mounted on in the container.
export const volumeKind = (target) => (BULK.test(String(target || '').trim()) ? 'bulk' : 'config');

const mountType = (v) => v.type || v.Type || '';
const mountSource = (v) => (v.source !== undefined ? v.source : v.Source) || '';
const mountTarget = (v) => (v.target !== undefined ? v.target : v.Target) || '';

const SYSTEM_FILES = /^\/(etc|usr|proc|sys|dev|run|var\/run)(\/|$)|\.sock$/;

// The folders an app keeps data in: binds and named volumes, not tmpfs, not
// sockets or system files (those are not data, and the review asks about
// them apart), not anonymous volumes.
export const dataVolumes = (definition, frozen = []) => {
  const list = [];
  const services = (definition && definition.services) || {};
  Object.keys(services).forEach((service) => {
    ((services[service] && services[service].volumes) || []).forEach((volume, index) => {
      if (!volume || typeof volume !== 'object') return;
      const type = mountType(volume) || (mountSource(volume).startsWith('/') ? 'bind' : 'volume');
      const source = mountSource(volume);
      const target = mountTarget(volume);
      if ((type !== 'bind' && type !== 'volume') || !source || !target) return;
      if (type === 'bind' && SYSTEM_FILES.test(source)) return;
      list.push({
        key: service + '|' + target,
        service, index, type, source, target,
        kind: volumeKind(target),
        frozen: frozen.includes(source),
      });
    });
  });
  return list;
};

const within = (p, dir) => !!dir && (p === dir || p.startsWith(dir.replace(/\/$/, '') + '/'));

// The place (from the plan) a host path is on: the longest that holds it.
export const placeOf = (path, places) => {
  let best = null;
  (places || []).forEach((place) => {
    if (within(path, place.path) && (!best || place.path.length > best.path.length)) best = place;
  });
  return best;
};

const folderName = (volume) => {
  const last = String(volume.target).replace(/\/+$/, '').split('/').pop() || 'data';
  return last.replace(/[^A-Za-z0-9_.-]+/g, '-').replace(/^[.-]+/, '') || 'data';
};

const appFolder = (app) => String(app || 'app').replace(/[^A-Za-z0-9_.-]+/g, '-').replace(/^[.-]+/, '') || 'app';

// The data disks, the one with the most free space first.
export const dataDisks = (plan) => ((plan && plan.places) || [])
  .filter((place) => place.kind === 'disk' && !place.osDisk)
  .sort((a, b) => (b.free || 0) - (a.free || 0));

// Where a volume lives now: { onSystem, place }.
const whereNow = (volume, plan) => {
  if (volume.type === 'volume') {
    const root = ((plan && plan.places) || []).find((place) => place.kind === 'docker-root');
    return { onSystem: !!(root && root.osDisk), place: root || null };
  }
  const place = placeOf(volume.source, plan && plan.places);
  return { onSystem: !!(place && place.osDisk), place };
};

// The proposal for one volume: { path } to put it in that host folder, or
// { keep: true } to leave it as the definition has it; with the reason code.
export const proposePlace = (volume, plan, app) => {
  if (volume.frozen) return { keep: true, reason: 'frozen' };
  const disks = dataDisks(plan);
  const now = whereNow(volume, plan);
  const dataPath = plan && plan.dataPath;
  const dataPlace = dataPath ? placeOf(dataPath, plan.places) : null;
  const dataPathOnSystem = !dataPlace || dataPlace.osDisk;
  const name = appFolder(app || volume.service);
  // a folder the file names itself, outside the data path, is the admin's or the template's choice: kept, and said
  if (volume.type === 'bind' && !(dataPath && within(volume.source, dataPath))) {
    return { keep: true, reason: now.onSystem && disks.length > 0 ? 'systemAsked' : 'fine' };
  }
  const folder = folderName(volume);

  if (volume.kind === 'bulk' && disks.length > 0 && (now.onSystem || volume.type === 'volume')) {
    return { path: `${disks[0].path.replace(/\/$/, '')}/${name}/${folder}`, reason: 'bulk' };
  }
  if (volume.kind === 'config' && now.onSystem && disks.length > 0) {
    if (dataPath && !dataPathOnSystem) {
      return { path: `${dataPath.replace(/\/$/, '')}/${name}/${folder}`, reason: 'config' };
    }
    const base = (dataPath || '/cosmos-storage').replace(/\/$/, '').split('/').pop() || 'cosmos-storage';
    return { path: `${disks[0].path.replace(/\/$/, '')}/${base}/${name}/${folder}`, reason: 'configDisk' };
  }
  return { keep: true, reason: now.onSystem ? 'noDisk' : 'fine' };
};

// Proposals for every volume, keyed like dataVolumes.
export const proposePlaces = (volumes, plan, app) => {
  const result = {};
  (volumes || []).forEach((volume) => { result[volume.key] = proposePlace(volume, plan, app); });
  return result;
};

// A choice is { path } or { keep: true }. The definition with the chosen
// folders: a moved volume becomes a bind on that folder, and a named volume
// nothing uses any more is not created.
export const applyPlaces = (definition, volumes, choices) => {
  if (!definition || !definition.services) return definition;
  const result = { ...definition, services: { ...definition.services } };
  const moved = new Set();
  (volumes || []).forEach((volume) => {
    const choice = choices && choices[volume.key];
    if (!choice || choice.keep || !choice.path || volume.frozen) return;
    const service = result.services[volume.service] = { ...result.services[volume.service] };
    service.volumes = [...service.volumes];
    const old = service.volumes[volume.index];
    const next = { ...old, type: 'bind', source: choice.path, target: volume.target };
    delete next.Type; delete next.Source; delete next.Target; delete next.VolumeOptions;
    service.volumes[volume.index] = next;
    if (volume.type === 'volume') moved.add(volume.source);
  });
  if (moved.size > 0 && result.volumes) {
    const stillUsed = new Set();
    Object.values(result.services).forEach((service) => ((service && service.volumes) || []).forEach((v) => {
      if (v && mountType(v) !== 'bind' && mountSource(v)) stillUsed.add(mountSource(v));
    }));
    result.volumes = { ...result.volumes };
    moved.forEach((name) => { if (!stillUsed.has(name)) delete result.volumes[name]; });
  }
  return result;
};

// Where a volume's data ends up with a choice, for the review's line.
export const finalPlace = (volume, choice) => (choice && !choice.keep && choice.path
  ? { type: 'bind', path: choice.path }
  : { type: volume.type, path: volume.source });

// ---- proposals the server can make itself ------------------------------------

const LINUXSERVER = /^(lscr\.io\/|ghcr\.io\/)?linuxserver\//;

// The defaults a definition gets when it says nothing: a restart policy, the
// time zone and the user of linuxserver.io images (they document TZ, PUID and
// PGID). Returns the definition and what was proposed, for the review.
export const proposeDefaults = (definition, plan) => {
  if (!definition || !definition.services) return { definition, proposed: [] };
  const proposed = [];
  const services = {};
  const [uid, gid] = String((plan && plan.dataOwner) || '1000:1000').split(':');
  Object.keys(definition.services).forEach((key) => {
    const service = { ...definition.services[key] };
    if (!service.restart) {
      service.restart = 'unless-stopped';
      proposed.push({ service: key, code: 'restart' });
    }
    const env = [...(service.environment || [])];
    const has = (name) => env.some((line) => String(line).split('=')[0] === name);
    const empty = (name) => env.findIndex((line) => String(line) === name + '=');
    const isLinuxserver = LINUXSERVER.test(String(service.image || ''));
    // TZ=auto is turned into the server's time zone by the server (src/utils/fork_timezone.go)
    if (empty('TZ') !== -1) {
      env[empty('TZ')] = 'TZ=auto';
      proposed.push({ service: key, code: 'tz' });
    } else if (isLinuxserver && !has('TZ')) {
      env.push('TZ=auto');
      proposed.push({ service: key, code: 'tz' });
    }
    if (isLinuxserver && !service.user) {
      [['PUID', uid], ['PGID', gid]].forEach(([name, value]) => {
        if (empty(name) !== -1) env[empty(name)] = name + '=' + value;
        else if (!has(name)) env.push(name + '=' + value);
        else return;
        proposed.push({ service: key, code: name.toLowerCase(), params: { value } });
      });
    }
    service.environment = env;
    services[key] = service;
  });
  return { definition: { ...definition, services }, proposed };
};

// ---- the review in words --------------------------------------------------------

export const TEXT = {
  'review.containers': 'Creates {{count}} container(s): {{list}}.',
  'review.ports': 'Publishes on the server: {{list}}.',
  'review.noPorts': 'Publishes no port on the server.',
  'review.addresses': 'Addresses in Cosmos: {{list}}.',
  'review.restart': 'Comes back by itself after a crash or a restart of the server ({{policy}}).',
  'review.noRestart': 'Does not come back by itself after a restart of the server.',
  'place.bind': '{{service}} {{target}} → the folder {{path}}',
  'place.volume': '{{service}} {{target}} → the Docker volume {{path}}',
  'place.on': 'on {{disk}}, {{free}} free',
  'place.system': 'on the disk the system runs from',
  'reason.bulk': 'Large data: proposed on the data disk with the most free space.',
  'reason.config': 'Settings and databases: proposed in the data path.',
  'reason.configDisk': 'The data path is on the disk the system runs from: proposed on a data disk instead.',
  'reason.frozen': 'The app needs this one where it is.',
  'reason.noDisk': 'No data disk was found: it stays on the system disk.',
  'reason.fine': 'As the app asks for it.',
  'reason.systemAsked': 'The file names this folder itself, and it is on the disk the system runs from: change it if it holds much data.',
  'proposed.restart': '{{service}}: restarts unless you stop it (the file says nothing).',
  'proposed.tz': '{{service}}: the server\'s time zone.',
  'proposed.puid': '{{service}}: runs as user {{value}} (PUID), the owner of the data path.',
  'proposed.pgid': '{{service}}: group {{value}} (PGID).',
};

export const text = (code, params) => ({ key: 'mgmt.servapps.creation.' + code, text: TEXT[code], params: params || {} });

const portText = (port) => {
  const [main, protocol] = String(port).split('/');
  const parts = main.split(':');
  const host = parts.length > 2 ? parts.slice(0, -2).join(':') + ':' + parts[parts.length - 2] : parts[parts.length - 2];
  return `${host} → ${parts[parts.length - 1]}${protocol && protocol !== 'tcp' ? '/' + protocol : ''}`;
};

// One sentence per fact of the definition, for the review.
export const reviewSentences = (definition) => {
  const services = (definition && definition.services) || {};
  const keys = Object.keys(services);
  const lines = [];
  lines.push(text('review.containers', {
    count: keys.length,
    list: keys.map((k) => `${services[k].container_name || k} (${services[k].image || '?'})`).join(', '),
  }));
  const ports = [];
  keys.forEach((k) => (services[k].ports || []).forEach((p) => ports.push(`${portText(p)} (${services[k].container_name || k})`)));
  lines.push(ports.length ? text('review.ports', { list: ports.join(', ') }) : text('review.noPorts'));
  const hosts = [];
  keys.forEach((k) => (services[k].routes || []).forEach((r) => { if (r && (r.host || r.Host)) hosts.push(r.host || r.Host); }));
  if (hosts.length) lines.push(text('review.addresses', { list: [...new Set(hosts)].join(', ') }));
  const policies = [...new Set(keys.map((k) => services[k].restart || 'no'))];
  lines.push(policies.every((p) => p === 'no') ? text('review.noRestart') : text('review.restart', { policy: policies.join(', ') }));
  return lines;
};

// "1.2 TB", "830 GB"
export const sizeText = (bytes) => {
  const value = Number(bytes) || 0;
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let i = 0;
  let n = value;
  while (n >= 1000 && i < units.length - 1) { n /= 1000; i += 1; }
  return (n >= 10 || i === 0 ? Math.round(n) : Math.round(n * 10) / 10) + ' ' + units[i];
};

// The Market's own list of architectures of an app (supported_architectures,
// "amd64", "arm64", "arm/v7"…) against the server's (API.CPU_ARCH): false
// when the list says the app does not run here, true when it does or says nothing.
export const marketArchFits = (list, serverArch) => {
  if (!Array.isArray(list) || list.length === 0 || !serverArch) return true;
  const names = list.map((a) => String(a).toLowerCase().replace(/^linux\//, '').split('/')[0]);
  const arch = String(serverArch).toLowerCase();
  return names.includes(arch) || (arch === 'arm' && names.some((n) => n.startsWith('arm') && n !== 'arm64'));
};

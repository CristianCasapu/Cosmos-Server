// Fork addition: a compose file turned into what POST /api/docker-service
// takes, with a note for everything that is not taken as written.
//
// Upstream's conversion (docker-compose.jsx) broke a lone port ("- 8080"
// became "undefined:undefined/tcp"), lost ":ro", read_only and the other mount
// options, turned an exec-form command into one string, read "1m30s" as one
// minute, wrote "KEY=null" for a variable without value, and dropped every
// key it did not know without a word. Here each key of a service is
// converted, converted with a stated difference, or named as not used, and
// the review shows the notes before anything is created.
//
// convertCompose(doc, { serviceName, dataPath }) → { doc, notes }
//   note: { kind, code, service, params }
//     kind "changed"  taken, with a difference the admin should know
//          "dropped"  not used (the sentence says why)
//          "blocked"  cannot be created as written: Create waits for a fix
// Tested in composeImport.test.mjs.

// Keys of a service that Cosmos' create takes (src/docker/api_blueprint.go,
// fork_compose_types.go), after the conversion below.
const TAKEN = new Set([
  'image', 'container_name', 'environment', 'labels', 'ports', 'volumes', 'networks', 'links', 'restart',
  'devices', 'expose', 'depends_on', 'tty', 'stdin_open', 'command', 'entrypoint', 'runtime', 'working_dir',
  'user', 'uid', 'gid', 'hostname', 'domainname', 'mac_address', 'privileged', 'network_mode', 'stop_signal',
  'stop_grace_period', 'healthcheck', 'dns', 'dns_search', 'extra_hosts', 'security_opt', 'storage_opt',
  'sysctls', 'isolation', 'cap_add', 'cap_drop', 'mem_limit', 'mem_reservation', 'cpus', 'cpu_shares',
  'cpuset', 'cpuset_cpus', 'post_install', 'routes', 'shm_size', 'tmpfs', 'init', 'read_only', 'env_file',
]);

// Why a key is not used, by key; the rest get "unknown".
const DROPPED_WHY = {
  build: 'build', deploy: 'deploy', secrets: 'files', configs: 'files', profiles: 'profiles',
  extends: 'extends', platform: 'platform', pull_policy: 'pull',
};

// Top-level keys: those taken, and those ignored on purpose (no note).
const TOP_TAKEN = new Set(['services', 'volumes', 'networks', 'name', 'cosmos-installer', 'x-cosmos-installer', 'minVersion']);
const TOP_SILENT = new Set(['version']);

export const TEXT = {
  'port.random': '{{service}}, ports: "{{value}}" has no port on the server, so Compose would publish it on a random one. Cosmos publishes it on the same port, {{port}}: change it in the definition if that port is taken.',
  'port.invalid': '{{service}}, ports: "{{value}}" is not a port this import understands: write it as 8080:80 or 8080:80/udp.',
  'port.mode': '{{service}}, ports: mode "{{value}}" is for Swarm and is not used.',
  'env.unset': '{{service}}, environment: {{name}} has no value. Compose would take it from the shell it runs in; Cosmos has none, so it is left out.',
  'env_file': '{{service}}, env_file: Cosmos only has this text, not the file {{value}} next to it. Paste the file\'s lines into the service\'s environment instead.',
  'volume.home': '{{service}}, volumes: "{{value}}" starts at a home folder ("~"). Write the full path of the folder on the server.',
  'volume.parent': '{{service}}, volumes: "{{value}}" goes up out of the app\'s folder (".."). Write the full path of the folder on the server.',
  'volume.relative': '{{service}}, volumes: "{{value}}" is relative to the compose file. Cosmos keeps it in {{path}}.',
  'volume.option': '{{service}}, volumes: the option "{{value}}" is not used.',
  'volume.driver': 'volumes: the volume {{name}} asks for the driver options {{value}}, which are not used: it is created as a plain Docker volume.',
  'network.option': 'networks: {{name}}: {{value}} is not used.',
  'depends.optional': '{{service}}, depends_on: {{name}} is marked "required: false"; Cosmos waits for it all the same.',
  'depends.completed': '{{service}}, depends_on: {{name}} with "service_completed_successfully": Cosmos only waits for it to start.',
  'healthcheck.interval': '{{service}}, healthcheck: {{name}} "{{value}}" is not a duration such as 30s or 1m30s.',
  'network.service': '{{service}}, network_mode: the service {{value}} is not in this file.',
  'network.ports': '{{service}} uses the network of {{value}}, so it cannot publish ports of its own: move its ports to {{value}}.',
  'extra_hosts': '{{service}}, extra_hosts: written as host:address.',
  'key.build': '{{service}}, build: Cosmos does not build images. Build it elsewhere and give the image\'s name.',
  'key.deploy': '{{service}}, deploy: these are Swarm settings. For limits, use mem_limit and cpus.',
  'key.files': '{{service}}, {{key}}: files from outside this text are not available to an import.',
  'key.profiles': '{{service}}, profiles: every service of the file is created.',
  'key.extends': '{{service}}, extends: services from other files are not available to an import.',
  'key.platform': '{{service}}, platform: the image for the server\'s own platform is used.',
  'key.pull': '{{service}}, pull_policy: Cosmos always pulls the image.',
  'key.unknown': '{{service}}, {{key}}: Cosmos does not support this setting, so it is not applied.',
  'top.unknown': '{{key}} (top level): not used.',
};

export const text = (code, params) => ({ key: 'mgmt.servapps.compose.note.' + code, text: TEXT[code], params: params || {} });

const isObject = (value) => typeof value === 'object' && value !== null && !Array.isArray(value);

const str = (value) => (value === undefined || value === null ? '' : String(value));

// ---- ports ----------------------------------------------------------------

const PORT = /^\d{1,5}(-\d{1,5})?$/;

// One entry of `ports` → "[ip:]host:container[/proto]" or a note.
export const convertPort = (entry) => {
  if (typeof entry === 'number') {
    entry = String(entry);
  }
  if (typeof entry === 'string') {
    const value = entry.trim();
    const [main, protocol] = value.split('/');
    const proto = protocol ? '/' + protocol : '';
    // "8080", "8000-8010": Compose publishes these on random ports
    if (PORT.test(main)) {
      return { port: `${main}:${main}${proto}`, note: { kind: 'changed', code: 'port.random', params: { value, port: main } } };
    }
    // ip::container (random host port on one address)
    const empty = main.match(/^(.*)::(\d{1,5}(?:-\d{1,5})?)$/);
    if (empty) {
      return { port: `${empty[1]}:${empty[2]}:${empty[2]}${proto}`, note: { kind: 'changed', code: 'port.random', params: { value, port: empty[2] } } };
    }
    const parts = main.split(':');
    if (parts.length >= 2 && PORT.test(parts[parts.length - 1]) && PORT.test(parts[parts.length - 2])) {
      return { port: value };
    }
    return { note: { kind: 'blocked', code: 'port.invalid', params: { value } } };
  }
  if (isObject(entry)) {
    const target = str(entry.target).trim();
    const published = str(entry.published).trim();
    const protocol = entry.protocol ? '/' + entry.protocol : '/tcp';
    const ip = entry.host_ip ? (String(entry.host_ip).includes(':') && !String(entry.host_ip).startsWith('[') ? `[${entry.host_ip}]` : entry.host_ip) + ':' : '';
    const notes = [];
    if (entry.mode && entry.mode !== 'host' && entry.mode !== 'ingress') {
      notes.push({ kind: 'dropped', code: 'port.mode', params: { value: entry.mode } });
    }
    if (!PORT.test(target)) {
      return { note: { kind: 'blocked', code: 'port.invalid', params: { value: JSON.stringify(entry) } } };
    }
    if (!published) {
      return { port: `${ip}${target}:${target}${protocol}`, note: { kind: 'changed', code: 'port.random', params: { value: JSON.stringify(entry), port: target } }, notes };
    }
    if (!PORT.test(published)) {
      return { note: { kind: 'blocked', code: 'port.invalid', params: { value: JSON.stringify(entry) } } };
    }
    return { port: `${ip}${published}:${target}${protocol}`, notes };
  }
  return { note: { kind: 'blocked', code: 'port.invalid', params: { value: JSON.stringify(entry) } } };
};

// ---- volumes --------------------------------------------------------------

const SHORT_OPTIONS = { ro: 'ro', rw: 'rw', z: 'z', Z: 'Z', nocopy: 'nocopy', shared: 'shared', rshared: 'rshared', slave: 'slave', rslave: 'rslave', private: 'private', rprivate: 'rprivate', cached: 'cached', delegated: 'delegated', consistent: 'consistent' };
const PROPAGATION = ['shared', 'rshared', 'slave', 'rslave', 'private', 'rprivate'];

const joinPath = (...parts) => parts.filter(Boolean).join('/').replace(/\/+/g, '/');

// A bind source as the server knows it, or a note.
const bindSource = (source, ctx) => {
  if (source === '~' || source.startsWith('~/')) {
    return { note: { kind: 'blocked', code: 'volume.home', params: { value: source } } };
  }
  if (source.startsWith('.')) {
    const clean = source.replace(/^\.\/?/, '');
    if (source.startsWith('..') || clean.split('/').includes('..')) {
      return { note: { kind: 'blocked', code: 'volume.parent', params: { value: source } } };
    }
    const path = joinPath(ctx.dataPath || '/cosmos-storage', ctx.serviceName || '', clean);
    return { source: path, note: { kind: 'changed', code: 'volume.relative', params: { value: source, path } } };
  }
  if (source.split('/').includes('..')) {
    return { note: { kind: 'blocked', code: 'volume.parent', params: { value: source } } };
  }
  return { source };
};

// One entry of a service's `volumes` → a mount as the server reads it
// ({type, source, target, ReadOnly, BindOptions, VolumeOptions, TmpfsOptions}).
export const convertVolume = (entry, ctx) => {
  const notes = [];
  let mount;
  if (typeof entry === 'string') {
    const parts = entry.split(':');
    let source = parts[0];
    let target = parts[1];
    let options = parts.slice(2).join(':');
    if (parts.length === 1) {
      // "/data": an anonymous volume
      return { mount: { type: 'volume', source: '', target: parts[0] }, notes };
    }
    if (parts.length === 2 && parts[1].split(',').every((o) => SHORT_OPTIONS[o])) {
      // "/data:ro" (anonymous volume with options) is too rare to guess; keep as written
      target = parts[1];
    }
    const isBind = source.startsWith('/') || source.startsWith('.') || source.startsWith('~');
    mount = { type: isBind ? 'bind' : 'volume', source, target };
    options.split(',').filter(Boolean).forEach((option) => {
      if (option === 'ro') mount.ReadOnly = true;
      else if (option === 'rw') { /* the default */ }
      else if (PROPAGATION.includes(option) && isBind) mount.BindOptions = { Propagation: option };
      else if (option === 'nocopy' && !isBind) mount.VolumeOptions = { NoCopy: true };
      else notes.push({ kind: 'dropped', code: 'volume.option', params: { value: option } });
    });
  } else if (isObject(entry)) {
    mount = { type: entry.type || 'volume', source: str(entry.source), target: str(entry.target) };
    if (entry.read_only) mount.ReadOnly = true;
    if (isObject(entry.bind)) {
      if (entry.bind.propagation) mount.BindOptions = { Propagation: entry.bind.propagation };
      Object.keys(entry.bind).filter((k) => !['propagation', 'create_host_path'].includes(k))
        .forEach((k) => notes.push({ kind: 'dropped', code: 'volume.option', params: { value: 'bind.' + k } }));
    }
    if (isObject(entry.volume)) {
      if (entry.volume.nocopy) mount.VolumeOptions = { NoCopy: true };
      Object.keys(entry.volume).filter((k) => k !== 'nocopy')
        .forEach((k) => notes.push({ kind: 'dropped', code: 'volume.option', params: { value: 'volume.' + k } }));
    }
    if (isObject(entry.tmpfs)) {
      const size = entry.tmpfs.size;
      if (size !== undefined) {
        const bytes = typeof size === 'number' ? size : sizeBytes(size);
        if (bytes) mount.TmpfsOptions = { SizeBytes: bytes };
        else notes.push({ kind: 'dropped', code: 'volume.option', params: { value: 'tmpfs.size ' + size } });
      }
    }
    ['consistency'].forEach((k) => {
      if (entry[k] !== undefined) notes.push({ kind: 'dropped', code: 'volume.option', params: { value: k } });
    });
  } else {
    return { notes: [{ kind: 'blocked', code: 'volume.parent', params: { value: JSON.stringify(entry) } }] };
  }
  if (mount.type === 'bind') {
    const checked = bindSource(mount.source, ctx);
    if (checked.note) notes.push(checked.note);
    if (!checked.source) return { notes };
    mount.source = checked.source;
  }
  return { mount, notes };
};

// "64m", "1g", "512k", 1024 → bytes, 0 when it is not a size.
export const sizeBytes = (value) => {
  if (typeof value === 'number') return value;
  const match = str(value).trim().toLowerCase().match(/^(\d+(?:\.\d+)?)\s*([bkmg]?)b?$/);
  if (!match) return 0;
  return Math.floor(parseFloat(match[1]) * { '': 1, b: 1, k: 1024, m: 1024 ** 2, g: 1024 ** 3 }[match[2]]);
};

// ---- durations -----------------------------------------------------------

export const isDuration = (value) => typeof value === 'number' || /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h|d))+$|^\d+(\.\d+)?$/.test(str(value).trim());

const toSeconds = (value) => {
  if (typeof value === 'number') return value;
  let total = 0;
  const re = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h|d)?/g;
  let match;
  while ((match = re.exec(str(value).trim())) !== null) {
    total += parseFloat(match[1]) * ({ ns: 1e-9, us: 1e-6, 'µs': 1e-6, ms: 1e-3, s: 1, m: 60, h: 3600, d: 86400 }[match[2] || 's']);
  }
  return Math.round(total);
};

// ---- one service ----------------------------------------------------------

const convertService = (key, service, ctx, notes) => {
  const note = (n) => notes.push({ service: key, ...n, params: { service: key, ...(n.params || {}) } });

  Object.keys(service).forEach((k) => {
    if (TAKEN.has(k) || k.startsWith('x-')) return;
    const why = DROPPED_WHY[k] || 'unknown';
    note({ kind: 'dropped', code: 'key.' + why, params: { key: k } });
    delete service[k];
  });

  if (service.volumes && Array.isArray(service.volumes)) {
    const volumes = [];
    service.volumes.forEach((entry) => {
      const result = convertVolume(entry, ctx);
      (result.notes || []).forEach(note);
      if (result.mount) volumes.push(result.mount);
    });
    service.volumes = volumes;
  }

  if (service.expose) {
    service.expose = [].concat(service.expose).map((port) => '' + port);
  }

  if (service.ports && Array.isArray(service.ports)) {
    const ports = [];
    service.ports.forEach((entry) => {
      const result = convertPort(entry);
      if (result.note) note(result.note);
      (result.notes || []).forEach(note);
      if (result.port) ports.push(result.port);
    });
    service.ports = ports;
  }

  if (service.user !== undefined && service.user !== null) {
    service.user = '' + service.user;
  }

  if (service.labels) {
    const labels = {};
    if (Array.isArray(service.labels)) {
      service.labels.forEach((label) => {
        const [name, value] = String(label).split(/=(.*)/s);
        labels['' + name] = value === undefined ? '' : '' + value;
      });
    } else if (isObject(service.labels)) {
      Object.keys(service.labels).forEach((name) => {
        const value = service.labels[name];
        labels['' + name] = value === null || value === undefined ? '' : '' + value;
      });
    }
    service.labels = labels;
  }

  if (service.environment !== undefined) {
    const environment = [];
    const add = (name, value) => {
      if (value === undefined || value === null) {
        note({ kind: 'dropped', code: 'env.unset', params: { name } });
        return;
      }
      environment.push(name + '=' + (typeof value === 'object' ? JSON.stringify(value) : value));
    };
    if (Array.isArray(service.environment)) {
      service.environment.forEach((line) => {
        const text = String(line);
        if (!text.includes('=')) add(text, null);
        else environment.push(text);
      });
    } else if (isObject(service.environment)) {
      Object.keys(service.environment).forEach((name) => add(name, service.environment[name]));
    }
    service.environment = environment;
  }

  if (service.env_file !== undefined) {
    [].concat(service.env_file).forEach((file) => {
      note({ kind: 'dropped', code: 'env_file', params: { value: isObject(file) ? file.path : file } });
    });
    delete service.env_file;
  }

  if (service.sysctls && Array.isArray(service.sysctls)) {
    const sysctls = {};
    service.sysctls.forEach((line) => {
      const [name, value] = String(line).split(/=(.*)/s);
      sysctls[name] = value === undefined ? '' : value;
    });
    service.sysctls = sysctls;
  } else if (isObject(service.sysctls)) {
    Object.keys(service.sysctls).forEach((name) => { service.sysctls[name] = str(service.sysctls[name]); });
  }

  if (service.networks && Array.isArray(service.networks)) {
    const networks = {};
    service.networks.forEach((network) => {
      if (isObject(network)) networks['' + network.name] = network;
      else networks['' + network] = {};
    });
    service.networks = networks;
  } else if (isObject(service.networks)) {
    Object.keys(service.networks).forEach((name) => { service.networks[name] = service.networks[name] || {}; });
  }

  if (service.devices && Array.isArray(service.devices)) {
    service.devices = service.devices.map((device) => {
      if (isObject(device)) return `${device.source}:${device.target || device.source}`;
      const parts = String(device).split(':');
      return parts.length === 1 ? `${parts[0]}:${parts[0]}` : `${parts[0]}:${parts[1]}`;
    });
  }

  ['dns', 'dns_search', 'tmpfs'].forEach((name) => {
    if (typeof service[name] === 'string') service[name] = [service[name]];
  });

  if (service.extra_hosts && isObject(service.extra_hosts)) {
    service.extra_hosts = Object.keys(service.extra_hosts).map((host) => `${host}:${service.extra_hosts[host]}`);
  }

  if (service.cpuset !== undefined) {
    service.cpuset_cpus = '' + service.cpuset;
    delete service.cpuset;
  }
  if (service.cpus !== undefined) service.cpus = parseFloat(service.cpus) || 0;
  ['mem_limit', 'mem_reservation'].forEach((name) => {
    if (service[name] !== undefined) service[name] = '' + service[name];
  });

  if (service.stop_grace_period !== undefined) {
    if (isDuration(service.stop_grace_period)) service.stop_grace_period = toSeconds(service.stop_grace_period);
    else {
      note({ kind: 'dropped', code: 'healthcheck.interval', params: { name: 'stop_grace_period', value: service.stop_grace_period } });
      delete service.stop_grace_period;
    }
  }

  if (service.depends_on) {
    const depends = {};
    if (Array.isArray(service.depends_on)) {
      service.depends_on.forEach((name) => { depends['' + name] = { condition: 'service_started' }; });
    } else if (isObject(service.depends_on)) {
      Object.keys(service.depends_on).forEach((name) => {
        const entry = service.depends_on[name] || {};
        depends[name] = { condition: entry.condition || 'service_started' };
        if (entry.restart !== undefined) depends[name].restart = '' + entry.restart;
        if (entry.required === false) note({ kind: 'changed', code: 'depends.optional', params: { name } });
        if (entry.condition === 'service_completed_successfully') note({ kind: 'changed', code: 'depends.completed', params: { name } });
      });
    }
    service.depends_on = depends;
  }

  if (isObject(service.healthcheck)) {
    ['interval', 'timeout', 'start_period', 'start_interval'].forEach((name) => {
      const value = service.healthcheck[name];
      if (value !== undefined && !isDuration(value)) {
        note({ kind: 'blocked', code: 'healthcheck.interval', params: { name, value } });
      }
    });
  }

  if (!service.hostname) {
    service.hostname = key;
  }
  return service;
};

// The whole file. `doc` is what js-yaml read; it is changed in place and
// returned with the notes.
export const convertCompose = (doc, ctx = {}) => {
  const notes = [];
  if (!isObject(doc)) {
    return { doc, notes };
  }
  // a file holding services only, without "services:"
  if (Object.keys(doc).length > 0 && !doc.services && !doc.networks && !doc.volumes) {
    doc = { services: Object.assign({}, doc) };
  }
  const serviceName = ctx.serviceName;

  Object.keys(doc).forEach((key) => {
    if (TOP_TAKEN.has(key) || TOP_SILENT.has(key) || key.startsWith('x-')) return;
    notes.push({ kind: 'dropped', code: 'top.unknown', params: { key } });
    delete doc[key];
  });

  if (doc.services) {
    Object.keys(doc.services).forEach((key) => {
      if (key.startsWith('x-')) return;
      if (!isObject(doc.services[key])) return;
      convertService(key, doc.services[key], ctx, notes);

      // namespace the keys with the name of the app
      let finalKey = key;
      if (serviceName) {
        finalKey = serviceName + '-' + key;
        doc.services[finalKey] = doc.services[key];
        doc.services[finalKey].old_key = key;
        delete doc.services[key];
      }
      if (!doc.services[finalKey].container_name) {
        doc.services[finalKey].container_name = finalKey;
      }
    });

    const containerOf = (oldKey) => {
      const match = Object.keys(doc.services).find((k) => doc.services[k] && (doc.services[k].old_key === oldKey || (!serviceName && k === oldKey)));
      return match ? (doc.services[match].container_name || match) : null;
    };

    Object.keys(doc.services).forEach((key) => {
      const service = doc.services[key];
      if (!isObject(service)) return;
      if (service.depends_on) {
        const depends = {};
        Object.keys(service.depends_on).forEach((name) => {
          depends[containerOf(name) || name] = service.depends_on[name];
        });
        service.depends_on = depends;
      }
      if (service.network_mode && /^(service|container):/.test(service.network_mode)) {
        const [kind, target] = service.network_mode.split(/:(.*)/s);
        const found = kind === 'service' ? containerOf(target) : (containerOf(target) || target);
        if (!found) {
          notes.push({ kind: 'blocked', code: 'network.service', service: key, params: { service: key, value: target } });
        }
        service.network_mode = 'container:' + (found || target);
        if ((service.ports || []).length > 0) {
          notes.push({ kind: 'blocked', code: 'network.ports', service: key, params: { service: key, value: found || target } });
        }
        service.labels = service.labels || {};
        service.labels['cosmos-force-network-mode'] = service.network_mode;
        delete service.hostname;
      }
    });

    Object.keys(doc.services).forEach((key) => {
      if (isObject(doc.services[key])) delete doc.services[key].old_key;
    });
  }

  if (doc.networks) {
    const networks = {};
    if (Array.isArray(doc.networks)) {
      doc.networks.forEach((network) => {
        if (isObject(network)) networks['' + network.name] = network;
        else networks['' + network] = {};
      });
    } else {
      Object.keys(doc.networks).forEach((name) => {
        const network = doc.networks[name] || {};
        ['driver_opts', 'external', 'ipam.options'].forEach((option) => {
          if (network[option] !== undefined && option !== 'external') notes.push({ kind: 'dropped', code: 'network.option', params: { name, value: option } });
        });
        networks['' + name] = network;
      });
    }
    doc.networks = networks;
  }

  if (doc.volumes) {
    const volumes = {};
    if (Array.isArray(doc.volumes)) {
      doc.volumes.forEach((volume) => {
        if (isObject(volume)) volumes['' + volume.name] = volume;
        else if (volume) volumes['' + volume] = {};
      });
    } else {
      Object.keys(doc.volumes).forEach((name) => {
        const volume = doc.volumes[name] || {};
        if (volume.driver_opts) {
          notes.push({ kind: 'dropped', code: 'volume.driver', params: { name, value: Object.keys(volume.driver_opts).join(', ') } });
          delete volume.driver_opts;
        }
        volumes['' + name] = volume;
      });
    }
    doc.volumes = volumes;
  }

  // a default network for the app, a stack label when there are several services (as upstream did)
  let hasDefaultNetwork = false;
  if (doc.services) {
    Object.keys(doc.services).forEach((key) => {
      if (isObject(doc.services[key]) && !doc.services[key].network_mode) {
        doc.services[key].network_mode = 'cosmos-' + serviceName + '-default';
        hasDefaultNetwork = true;
      }
    });
  }
  if (hasDefaultNetwork) {
    doc.networks = doc.networks || {};
    doc.networks['cosmos-' + serviceName + '-default'] = {};
  }

  const keys = doc.services ? Object.keys(doc.services).filter((k) => isObject(doc.services[k])) : [];
  if (keys.length > 1) {
    let hasMain = false;
    keys.forEach((key) => {
      const labels = doc.services[key].labels = doc.services[key].labels || {};
      if (!labels['cosmos.stack']) labels['cosmos.stack'] = serviceName;
      if (labels['cosmos.stack.main']) hasMain = true;
    });
    if (!hasMain) {
      const main = keys.find((key) => doc.services[key].labels['cosmos.stack'] === serviceName);
      if (main) doc.services[main].labels['cosmos.stack.main'] = 'true';
    }
  }

  // Cosmos' own keys
  keys.forEach((key) => {
    if (doc.services[key]['x-routes']) {
      doc.services[key].routes = doc.services[key]['x-routes'];
      delete doc.services[key]['x-routes'];
    }
  });
  if (doc.services && doc.services['x-post-install']) {
    doc['cosmos-installer'] = doc.services['x-post-install'];
    delete doc.services['x-post-install'];
  }
  if (doc['x-cosmos-installer']) {
    doc['cosmos-installer'] = doc['x-cosmos-installer'];
    delete doc['x-cosmos-installer'];
  }

  return { doc, notes };
};

// Notes that keep Create from starting.
export const blockingNotes = (notes) => (notes || []).filter((n) => n.kind === 'blocked');

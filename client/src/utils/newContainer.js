// Fork addition: what the new-container form sends, and how a container's
// limits are shown again in its edit form.
//
// Upstream's form kept User, the limits, the network mode ("use as VPN"
// writes container:<name> into the same field) and the "interactive" tick in
// its state but never put them into the request: the container was created
// without them. serviceFromForm builds the whole request from the form's
// state; tested in newContainer.test.mjs.

const KIB = 1024;
const UNITS = [['g', KIB * KIB * KIB], ['m', KIB * KIB], ['k', KIB]];

// "512m", "1.5g", "536870912" → bytes; null when it is not a size.
export const memoryBytes = (text) => {
  const value = String(text === undefined || text === null ? '' : text).trim().toLowerCase();
  if (value === '') {
    return 0;
  }
  const match = value.match(/^(\d+(?:\.\d+)?)\s*([kmgt]?)(i?b)?$/);
  if (!match) {
    return null;
  }
  const factor = { '': 1, k: KIB, m: KIB * KIB, g: KIB * KIB * KIB, t: KIB * KIB * KIB * KIB }[match[2]];
  return Math.floor(parseFloat(match[1]) * factor);
};

// bytes → the shortest exact text ("512m"), "" for none.
export const memoryText = (bytes) => {
  const value = Number(bytes) || 0;
  if (value <= 0) {
    return '';
  }
  for (const [unit, size] of UNITS) {
    if (value % size === 0) {
      return (value / size) + unit;
    }
  }
  return String(value);
};

// The limits of a container (inspect's HostConfig, or the form's own state)
// as the form shows them.
export const limitsForForm = (containerInfo) => {
  const host = (containerInfo && containerInfo.HostConfig) || {};
  const fork = (containerInfo && containerInfo.ForkLimits) || null;
  if (fork) {
    return { memLimit: fork.memLimit || '', cpus: fork.cpus || 0 };
  }
  return {
    memLimit: memoryText(host.Memory),
    cpus: host.NanoCpus ? host.NanoCpus / 1e9 : 0,
  };
};

// A network mode that shares another container's network (or the host's)
// cannot publish ports nor join networks.
export const sharesNetwork = (mode) => /^(container|service):./.test(String(mode || ''));

export const networkModeValue = (mode) => {
  const value = String(mode || '').trim();
  return value === '' ? '' : value;
};

// The Docker tab's values (setup.jsx, after containerInfoFrom) into the form's state.
export const withDockerTab = (containerInfo, values) => ({
  ...containerInfo,
  Name: values.name,
  Names: [values.name],
  ForkLimits: { memLimit: String(values.memLimit === undefined || values.memLimit === null ? '' : values.memLimit).trim(), cpus: parseFloat(values.cpus) || 0 },
  Config: {
    ...containerInfo.Config,
    Image: values.image,
    Name: values.name,
    Env: values.envVars,
    Labels: values.labels,
    User: String(values.user || '').trim(),
    Tty: values.interactive === 2 || values.interactive === true,
    OpenStdin: values.interactive === 2 || values.interactive === true,
  },
  HostConfig: {
    ...containerInfo.HostConfig,
    Devices: (values.devices || []).map((device) => ({
      PathOnHost: device.split(':')[0],
      PathInContainer: device.split(':')[1],
      CgroupPermissions: 'rwm',
    })),
    RestartPolicy: {
      Name: values.restartPolicy,
    },
  },
});

// The request for POST /api/docker-service, one service named after the container.
export const serviceFromForm = (containerInfo) => {
  const config = containerInfo.Config || {};
  const host = containerInfo.HostConfig || {};
  const mode = networkModeValue(host.NetworkMode);
  const shared = sharesNetwork(mode) || mode === 'host';
  const limits = limitsForForm(containerInfo);

  const service = {
    container_name: containerInfo.Name,
    image: config.Image,
    environment: config.Env,
    labels: config.Labels,
    dns: shared ? [] : host.Dns,
    devices: host.Devices ? host.Devices.map((device) => `${device.PathOnHost}:${device.PathInContainer}`) : [],
    expose: config.ExposedPorts,
    tty: !!config.Tty,
    stdin_open: !!config.OpenStdin,
    // a container on another one's network publishes nothing of its own (Docker refuses it)
    ports: sharesNetwork(mode) ? [] : host.PortBindings,
    restart: host.RestartPolicy && host.RestartPolicy.Name,
    volumes: host.Mounts,
    networks: shared ? {} : Object.keys((containerInfo.NetworkSettings && containerInfo.NetworkSettings.Networks) || {}).reduce((acc, cur) => {
      acc[cur] = {};
      return acc;
    }, {}),
    routes: containerInfo.CreateRoute && containerInfo.Route ? [containerInfo.Route] : [],
  };
  if (config.User) {
    service.user = config.User;
  }
  if (limits.memLimit) {
    service.mem_limit = limits.memLimit;
  }
  if (limits.cpus > 0) {
    service.cpus = limits.cpus;
  }
  if (mode) {
    service.network_mode = mode;
  }
  return { services: { container_name: service } };
};

// What the form can say about a value before the server does (the server
// checks the same: src/docker/fork_service_check.go).
export const fieldProblems = (containerInfo) => {
  const problems = {};
  const user = String((containerInfo.Config && containerInfo.Config.User) || '').trim();
  if (user && !/^[A-Za-z0-9_][A-Za-z0-9_.-]{0,31}(:[A-Za-z0-9_][A-Za-z0-9_.-]{0,31})?$/.test(user)) {
    problems.user = ['mgmt.servapps.fork.problem.user', 'Enter a user name or number, or user:group, for example 1000:1000'];
  }
  const limits = limitsForForm(containerInfo);
  const memory = memoryBytes(limits.memLimit);
  if (memory === null) {
    problems.memLimit = ['mgmt.servapps.fork.problem.memory', 'Enter a size such as 512m or 2g, or leave it empty for no limit'];
  } else if (memory > 0 && memory < 6 * KIB * KIB) {
    problems.memLimit = ['mgmt.servapps.fork.problem.memorySmall', 'Docker needs at least 6m'];
  }
  if (!(limits.cpus >= 0)) {
    problems.cpus = ['mgmt.servapps.fork.problem.cpus', 'Enter 0 for no limit, or a number such as 0.5 or 2'];
  }
  return problems;
};

// "lscr.io/linuxserver/radarr:latest" → "radarr": the name proposed for a new container.
export const nameFromImage = (image) => String(image || '').trim().split('@')[0].split('/').pop().split(':')[0]
  .toLowerCase().replace(/[^a-z0-9_.-]+/g, '-').replace(/^[^a-z0-9]+|[^a-z0-9]+$/g, '');

// "Review and create": the form's state with what can be proposed filled in
// where the admin left it empty, and what was proposed. `port` is the
// proposal of utils/routeGuide.js (proposePort), `route(name, port)` builds
// the app's local URL; nothing typed is replaced.
export const quickSetup = (containerInfo, { port, route }) => {
  const proposed = [];
  let info = containerInfo;
  if (!String(info.Name || '').trim()) {
    const name = nameFromImage(info.Config && info.Config.Image);
    if (name) {
      info = { ...info, Name: name, Names: [name], Config: { ...info.Config, Name: name } };
      proposed.push('name');
    }
  }
  if (!info.CreateRoute && port && port.port && route) {
    info = { ...info, CreateRoute: true, Route: route(info.Name, port.port) };
    proposed.push('route');
  }
  return { info, proposed };
};

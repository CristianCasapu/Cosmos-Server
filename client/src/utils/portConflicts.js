// Fork addition: finds out, before a container is created or its ports are
// changed, whether a host port it asks for is already taken: by another
// container (from the list the UI already loads), by Cosmos itself (its HTTP
// and HTTPS ports), or by another row of the same form. Plain functions, no
// React: see portConflicts.test.mjs.
//
// A conflict is "certain" when Docker is sure to refuse it: same host port,
// same protocol and addresses that overlap. It is only "possible" when that
// cannot be told from here (a port range Docker picks one port from, Cosmos'
// own ports when Cosmos runs in a container, an IPv6 wildcard next to an IPv4
// address).

export const MAX_PORT = 65535;

// "8080" -> { from: 8080, to: 8080 }, "8000-8010" -> { from: 8000, to: 8010 },
// "" -> null (not published), anything else -> { invalid: true }.
export const parseHostPort = (value) => {
  const text = String(value === undefined || value === null ? '' : value).trim();
  if (text === '') return null;

  const match = /^(\d{1,5})(?:-(\d{1,5}))?$/.exec(text);
  if (!match) return { invalid: true };

  const from = Number(match[1]);
  const to = match[2] === undefined ? from : Number(match[2]);
  if (from < 1 || to > MAX_PORT || from > to) return { invalid: true };
  return { from, to };
};

const cleanIp = (value) => String(value || '').trim().replace(/^\[|\]$/g, '').toLowerCase();

// '' (every address), 'v4' or 'v6'.
const family = (ip) => (ip === '' ? '' : ip.includes(':') ? 'v6' : 'v4');

const sameV6 = (a, b) => {
  if (a === b) return true;
  try {
    return new URL('http://[' + a + ']/').hostname === new URL('http://[' + b + ']/').hostname;
  } catch (e) {
    return false;
  }
};

const isV6Wildcard = (ip) => ip === '::' || sameV6(ip, '::');

// How two bind addresses relate: 'overlap' (they cannot both hold the port),
// 'maybe' or 'apart'.
export const addressesOverlap = (first, second) => {
  const a = cleanIp(first);
  const b = cleanIp(second);
  if (a === '' || b === '') return 'overlap';

  if (family(a) !== family(b)) {
    // an IPv6 wildcard socket usually takes the IPv4 side as well
    const v6 = family(a) === 'v6' ? a : b;
    return isV6Wildcard(v6) ? 'maybe' : 'apart';
  }
  if (family(a) === 'v4') {
    return (a === b || a === '0.0.0.0' || b === '0.0.0.0') ? 'overlap' : 'apart';
  }
  return (sameV6(a, b) || isV6Wildcard(a) || isV6Wildcard(b)) ? 'overlap' : 'apart';
};

const containerName = (container) => {
  const names = (container && container.Names) || [];
  const name = names[0] || (container && container.Name) || '';
  return String(name).replace(/^\//, '');
};

// The ports the containers of API.docker.list() publish on the host. Docker
// lists them for running containers only. `skip` names the containers that are
// being replaced (the one whose form this is).
export const containerHolders = (containers, skip) => {
  const skipped = (Array.isArray(skip) ? skip : [skip]).map((name) => String(name || '').replace(/^\//, '')).filter(Boolean);
  const holders = [];

  (Array.isArray(containers) ? containers : []).forEach((container) => {
    const name = containerName(container);
    if (!name || skipped.includes(name)) return;

    ((container && container.Ports) || []).forEach((published) => {
      const port = Number(published && published.PublicPort);
      if (!port) return; // exposed, not published
      holders.push({
        kind: 'container',
        name,
        port,
        protocol: String(published.Type || 'tcp').toLowerCase(),
        ip: cleanIp(published.IP),
        certain: true,
      });
    });
  });
  return holders;
};

// Cosmos' own ports, from its configuration. They are held for sure when
// Cosmos runs directly on the server; when it runs in a container the ports it
// really publishes are in the container list, so these are only a hint. The
// HTTPS port is not open while HTTPS is switched off. The settings are what
// the API returns: ports set through COSMOS_HTTP_PORT / COSMOS_HTTPS_PORT in
// the environment instead are not known here.
export const cosmosHolders = (config, status) => {
  const http = config && config.HTTPConfig;
  if (!http) return [];

  const onHost = !!status && status.containerized === false;
  const httpsOn = http.HTTPSCertificateMode !== 'DISABLED';
  const holders = [];
  const add = (role, value, certain) => {
    const port = Number(value);
    if (Number.isInteger(port) && port >= 1 && port <= MAX_PORT && !holders.find((h) => h.port === port)) {
      holders.push({ kind: 'cosmos', role, port, protocol: 'tcp', ip: '', certain });
    }
  };
  add('http', http.HTTPPort, onHost);
  add('https', http.HTTPSPort, onHost && httpsOn);
  return holders;
};

// A port as a compose file writes it: "80", "8080:80", "8080:80/udp",
// "127.0.0.1:8080:80", "[::1]:8080:80", "8000-8010:8000-8010", or the long
// form { published, target, protocol, host_ip }. Read the way the server reads
// it (src/docker/api_blueprint.go). null when no host port is asked for.
export const parseComposePort = (spec) => {
  if (spec && typeof spec === 'object') {
    if (spec.published === undefined || spec.published === null || spec.published === '') return null;
    return {
      hostPort: String(spec.published),
      hostIp: cleanIp(spec.host_ip),
      protocol: String(spec.protocol || 'tcp').toLowerCase(),
    };
  }

  const [ports, protocol] = String(spec === undefined || spec === null ? '' : spec).trim().split('/');
  const parts = ports.split(':');
  if (parts.length < 2) return null;
  return {
    hostPort: parts[parts.length - 2],
    hostIp: cleanIp(parts.slice(0, parts.length - 2).join(':')),
    protocol: String(protocol || 'tcp').toLowerCase(),
  };
};

const rangesTouch = (a, b) => a.from <= b.to && b.from <= a.to;

const sameHolder = (a, b) => a.kind === b.kind && a.name === b.name && a.role === b.role && a.row === b.row;

// For each row of a form ({ hostPort, protocol, hostIp }) the problems found:
// { invalid: true } for a host port that is not a port, else
// { certain, port, protocol, holder }, the certain ones first. A holder is
// { kind: 'container', name }, { kind: 'cosmos', role } or, for a port asked
// for twice, { kind: 'form', row, name } (name: the other service of a compose
// definition, whose rows carry `service`).
// `rangePicksOne` is how Docker reads "8000-8010" as a host port when a
// container is edited: any free port of the range, so a range never conflicts
// for sure. A compose file's range is every port of it.
export const findPortConflicts = (rows, holders, options) => {
  const rangePicksOne = !!(options && options.rangePicksOne);
  const list = Array.isArray(rows) ? rows : [];
  const wanted = list.map((row) => {
    const range = parseHostPort(row && row.hostPort);
    return range && {
      range,
      protocol: String((row && row.protocol) || 'tcp').toLowerCase(),
      ip: cleanIp(row && row.hostIp),
      service: row ? row.service : undefined,
    };
  });

  return wanted.map((want, index) => {
    if (!want) return [];
    if (want.range.invalid) return [{ invalid: true }];

    const single = want.range.from === want.range.to;
    const found = [];
    const add = (certain, port, holder) => {
      const known = found.find((c) => c.port === port && sameHolder(c.holder, holder));
      if (known) {
        known.certain = known.certain || certain;
      } else {
        found.push({ certain, port, protocol: want.protocol, holder });
      }
    };

    (holders || []).forEach((holder) => {
      if (holder.protocol !== want.protocol) return;
      if (holder.port < want.range.from || holder.port > want.range.to) return;
      const overlap = addressesOverlap(want.ip, holder.ip);
      if (overlap === 'apart') return;
      const certain = !!holder.certain && overlap === 'overlap' && (single || !rangePicksOne);
      add(certain, holder.port, { kind: holder.kind, name: holder.name, role: holder.role });
    });

    wanted.forEach((other, otherIndex) => {
      if (otherIndex === index || !other || other.range.invalid) return;
      // the server drops a port one service lists twice
      if (want.service !== undefined && want.service === other.service) return;
      if (other.protocol !== want.protocol || !rangesTouch(want.range, other.range)) return;
      const overlap = addressesOverlap(want.ip, other.ip);
      if (overlap === 'apart') return;
      const bothSingle = single && other.range.from === other.range.to;
      const certain = overlap === 'overlap' && (bothSingle || !rangePicksOne);
      add(certain, Math.max(want.range.from, other.range.from), { kind: 'form', row: otherIndex, name: other.service });
    });

    return found.sort((a, b) => Number(b.certain) - Number(a.certain));
  });
};

// True when a row cannot work: the form must not be sent.
export const hasBlockingPortConflict = (rows, holders, options) =>
  findPortConflicts(rows, holders, options).some((row) => row.some((problem) => problem.invalid || problem.certain));

// The published ports of the services of a compose definition, as form rows
// with the service they belong to.
export const composePortRows = (service) => {
  const services = (service && service.services) || {};
  const rows = [];
  Object.keys(services).forEach((key) => {
    const definition = services[key] || {};
    const ports = Array.isArray(definition.ports) ? definition.ports : [];
    ports.forEach((spec) => {
      const parsed = parseComposePort(spec);
      if (parsed) rows.push({ ...parsed, service: definition.container_name || key });
    });
  });
  return rows;
};

// The names of the containers a compose definition creates (and so replaces).
export const composeContainerNames = (service) => {
  const services = (service && service.services) || {};
  return Object.keys(services).map((key) => (services[key] && services[key].container_name) || key).filter(Boolean);
};

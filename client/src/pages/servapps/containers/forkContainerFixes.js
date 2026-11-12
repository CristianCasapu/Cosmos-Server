// Fork addition: what the Network and Volumes tabs of a container need to send back
// to the server everything they read, kept here so the upstream files only carry hooks.

const WILDCARD_IPS = ['0.0.0.0', '::'];

// The address a published port is bound to, for the form.
// Docker reports a port published without an address once per address family
// ("0.0.0.0" and "::"); that stays one row with an empty address. An address that was
// asked for (127.0.0.1:8080:80) is kept, so that saving the tab does not publish the
// port on every interface (upstream issue #611).
export const portHostIp = (containerInfo, contPort, binding) => {
  const ip = (binding && binding.HostIp) || '';
  const asked = containerInfo && containerInfo.HostConfig && containerInfo.HostConfig.PortBindings
    && containerInfo.HostConfig.PortBindings[contPort];
  const configured = Array.isArray(asked) ? asked.filter((b) => b && typeof b === 'object') : [];
  const samePort = (b) => !b.HostPort || b.HostPort === binding.HostPort;

  if (configured.some((b) => (b.HostIp || '') === ip && samePort(b))) return ip;
  // published without an address: Docker reports the one it used
  if (configured.some((b) => !b.HostIp && samePort(b))) return '';
  return WILDCARD_IPS.includes(ip) ? '' : ip;
};

// Empty (every address), or an IPv4 or IPv6 address as Docker takes it.
export const isHostIp = (value) => {
  const ip = value || '';
  if (ip === '') return true;

  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(ip)) {
    return ip.split('.').every((part) => Number(part) <= 255);
  }

  if (!/^[0-9a-fA-F:.]+$/.test(ip) || !ip.includes(':')) return false;
  try {
    new URL('http://[' + ip + ']/');
    return true;
  } catch (e) {
    return false;
  }
};

// "source:target:ro" and "source:target:ro,z" are read-only binds.
export const bindIsReadOnly = (mode) => (mode || '').split(',').includes('ro');

// Fork addition: the logic of the HTTP settings form (pages/config/forkHttpSettings.jsx),
// pure and tested (httpSettings.test.mjs). The server checks the same on save
// (src/configapi/fork_http_settings.go); what the form says is a courtesy.
//
// Texts are returned as [key, English, variables] and turned into sentences by
// the component with t().

// The two usual pairs of ports, as one choice (one decision per control).
export const PORT_PAIRS = [
  { value: 'standard', http: '80', https: '443' },
  { value: 'alternate', http: '8080', https: '8443' },
];

export const portChoice = (httpPort, httpsPort) => {
  const pair = PORT_PAIRS.find((p) => p.http === String(httpPort || '').trim() && p.https === String(httpsPort || '').trim());
  return pair ? pair.value : 'other';
};

export const portsOf = (choice) => PORT_PAIRS.find((p) => p.value === choice) || null;

// A port as the settings keep it (text): digits only, 1–65535.
export const portNumber = (value) => {
  const text = String(value === undefined || value === null ? '' : value).trim();
  if (!/^\d{1,5}$/.test(text)) return null;
  const port = Number(text);
  return port >= 1 && port <= 65535 ? port : null;
};

// What is wrong with the pair of ports, under the field it concerns.
export const portProblems = (httpPort, httpsPort) => {
  const problems = {};
  const a = portNumber(httpPort);
  const b = portNumber(httpsPort);
  if (String(httpPort || '').trim() !== '' && a === null) {
    problems.http = ['mgmt.config.http.fork.portInvalid', 'A port is a whole number from 1 to 65535, for example 80'];
  }
  if (String(httpsPort || '').trim() !== '' && b === null) {
    problems.https = ['mgmt.config.http.fork.portInvalid', 'A port is a whole number from 1 to 65535, for example 80'];
  }
  if (a !== null && a === b) {
    problems.https = ['mgmt.config.http.fork.portSame', 'HTTP and HTTPS need two different ports'];
  }
  return problems;
};

// What the check found about a port that is about to change.
export const portStateText = (finding) => {
  if (!finding) return null;
  switch (finding.state) {
    case 'free':
      return { severity: 'success', text: ['mgmt.config.http.fork.portFree', 'Port {{port}} is free on this server.', { port: finding.port }] };
    case 'cosmos':
      return { severity: 'success', text: ['mgmt.config.http.fork.portCosmos', 'Port {{port}} is one Cosmos uses now: it is let go and taken again on the restart.', { port: finding.port }] };
    case 'container':
      return { severity: 'error', text: ['mgmt.config.http.fork.portContainer', 'Port {{port}} is published by the container {{name}}. Saving is refused: pick another port, or change that container first.', { port: finding.port, name: finding.holder || '?' }] };
    case 'other':
      return { severity: 'error', text: ['mgmt.config.http.fork.portOther', 'Another program on this server already listens on port {{port}}. Saving is refused: pick another port.', { port: finding.port }] };
    case 'unknown':
      return { severity: 'info', text: ['mgmt.config.http.fork.portUnknown', 'Whether port {{port}} is free cannot be told from here (Cosmos runs in a container, or may not open it). Make sure nothing else uses it.', { port: finding.port }] };
    default:
      return null;
  }
};

// The panel's address with the given settings, the port left out when it is
// the usual one for the scheme.
export const panelAddress = ({ hostname, httpPort, httpsPort, https }) => {
  const host = String(hostname || '').trim();
  if (!host) return '';
  const port = https ? portNumber(httpsPort) : portNumber(httpPort);
  const usual = https ? 443 : 80;
  const shown = host.includes(':') && !host.startsWith('[') && host.split(':').length === 2 ? host.split(':')[0] : host;
  return (https ? 'https://' : 'http://') + shown + (port && port !== usual ? ':' + port : '');
};

// What a save of the HTTP tab will do, said before it is pressed.
// `before` and `after` are { hostname, httpPort, httpsPort, https, whitelist[], constellationOnly }.
export const saveEffects = (before, after, check) => {
  const effects = [];
  const moved = panelAddress(before) !== panelAddress(after);
  if (moved && panelAddress(after)) {
    effects.push({ severity: 'warning', text: ['mgmt.config.http.fork.effectAddress', 'The panel moves to {{address}}. The page you are on stops answering after the save: open the new address yourself.', { address: panelAddress(after) }] });
  }
  if (String(before.hostname || '').trim() !== String(after.hostname || '').trim()) {
    effects.push({ severity: 'info', text: ['mgmt.config.http.fork.effectHostname', 'A certificate for the new name is requested or made, and the links of the apps that use the panel\'s name change with it.'] });
  }
  if (check && check.whitelist && check.whitelist.passes === false && listChanged(before.whitelist, after.whitelist, before.constellationOnly, after.constellationOnly)) {
    effects.push({ severity: 'error', text: ['mgmt.config.http.fork.effectLockOut', 'The new admin restrictions would lock you out ({{address}} is not allowed). Saving is refused until the list allows it.', { address: (check.you && check.you.address) || '?' }] });
  }
  effects.push({ severity: 'info', text: ['mgmt.config.http.fork.effectRestart', 'Saving restarts the web server of Cosmos: the panel and the apps do not answer for a few seconds.'] });
  return effects;
};

const listChanged = (a, b, ca, cb) => Boolean(ca) !== Boolean(cb) || JSON.stringify(cleanList(a)) !== JSON.stringify(cleanList(b));

// --- address lists (admin whitelist, trusted proxies) ---------------------

export const cleanList = (list) => (Array.isArray(list) ? list : String(list || '').split(','))
  .map((entry) => String(entry).trim())
  .filter(Boolean);

export const joinList = (list) => cleanList(list).join(', ');

const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;

const isIPv6 = (text) => {
  if (!/^[0-9a-fA-F:.]+$/.test(text) || !text.includes(':')) return false;
  try {
    return new URL('http://[' + text + ']/').hostname.length > 2;
  } catch (e) {
    return false;
  }
};

export const isAddress = (text) => IPV4.test(text) || isIPv6(text);

// An address, or a range "address/bits".
export const isAddressEntry = (entry) => {
  const text = String(entry || '').trim();
  if (text.length > 256) return false;
  const [address, bits, extra] = text.split('/');
  if (extra !== undefined) return false;
  if (!isAddress(address)) return false;
  if (bits === undefined) return true;
  if (!/^\d{1,3}$/.test(bits)) return false;
  return Number(bits) <= (IPV4.test(address) ? 32 : 128);
};

// What utils.IsTrustedProxy understands: an address, a range or the URL of a list.
export const isProxyEntry = (entry) => {
  const text = String(entry || '').trim();
  return isAddressEntry(text) || (/^https?:\/\/\S+$/.test(text) && text.length <= 256);
};

export const invalidEntries = (list, valid) => cleanList(list).filter((entry) => !valid(entry));

const v4number = (ip) => ip.split('.').reduce((n, part) => n * 256 + Number(part), 0);

// Is an IPv4 address inside a range (IPv4 only; IPv6 is answered by the server).
export const inRange = (ip, entry) => {
  if (!IPV4.test(ip)) return false;
  const [address, bits] = String(entry).split('/');
  if (!IPV4.test(address)) return false;
  if (bits === undefined) return ip === address;
  const n = Number(bits);
  if (!(n >= 0 && n <= 32)) return false;
  const size = 2 ** (32 - n);
  return Math.floor(v4number(ip) / size) === Math.floor(v4number(address) / size);
};

// The entries the form proposes for the admin whitelist, from what the server
// saw: the address in use, and the server's network when the admin is on it.
// Each with its source; those already covered by the list are left out.
export const whitelistProposals = (check, list) => {
  const proposals = [];
  if (!check || !check.you || !check.you.address) return proposals;
  const current = cleanList(list);
  const you = check.you.address;
  const network = ((check.server && check.server.addresses) || []).find((a) => a.network && inRange(you, a.network));
  if (network && !current.includes(network.network)) {
    proposals.push({ value: network.network, source: ['mgmt.config.http.fork.proposeNetwork', 'The local network you and the server are on ({{ip}} is the server)', { ip: network.ip }] });
  }
  if (!current.some((entry) => entry === you || inRange(you, entry))) {
    proposals.push({ value: you, source: check.you.throughProxy
      ? ['mgmt.config.http.fork.proposeYouProxy', 'The address you are connected from, as the trusted proxy reports it']
      : ['mgmt.config.http.fork.proposeYou', 'The address you are connected from right now'] });
  }
  return proposals;
};

// Is the list known to leave the admin out? From the server's answer only
// (it knows the address it sees, proxies included).
export const locksOut = (check) => Boolean(check && check.whitelist && check.whitelist.passes === false);

// --- the server's address -------------------------------------------------

// Mirrors utils.IsValidHostnameSyntax: an IP, or DNS labels, optional ":port".
export const hostnameProblem = (value) => {
  const text = String(value || '').trim();
  if (text === '') return ['mgmt.config.http.fork.hostnameEmpty', 'Enter the name or the IP address the panel is opened with'];
  if (/^[a-z]+:\/\//i.test(text)) return ['mgmt.config.http.fork.hostnameScheme', 'Leave out http:// or https://: only the name, for example cosmos.example.com'];
  if (/[\s/?#@]/.test(text)) return ['mgmt.config.http.fork.hostnameChars', 'Only the name, without a path, a space or other signs, for example cosmos.example.com'];
  let host = text;
  const m = /^(.*):(\d+)$/.exec(text);
  if (m && !text.startsWith('[') && m[1].indexOf(':') === -1) {
    if (portNumber(m[2]) === null) return ['mgmt.config.http.fork.hostnamePort', 'The port after the name must be from 1 to 65535'];
    host = m[1];
  }
  if (isAddress(host.replace(/^\[|\]$/g, ''))) return null;
  const labels = host.toLowerCase().split('.');
  if (host.length > 253 || labels.some((label) => !/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/.test(label))) {
    return ['mgmt.config.http.fork.hostnameChars', 'Only the name, without a path, a space or other signs, for example cosmos.example.com'];
  }
  return null;
};

// Names the form can propose for the server's address: the name the page is
// open on, and the server's own addresses.
export const hostnameProposals = (check, pageHost, current) => {
  const seen = new Set([String(current || '').trim().toLowerCase()]);
  const found = [];
  const add = (value, source) => {
    const key = String(value || '').toLowerCase();
    if (!value || seen.has(key)) return;
    seen.add(key);
    found.push({ value, source });
  };
  if (pageHost && !hostnameProblem(pageHost)) add(pageHost, ['mgmt.config.http.fork.hostnameFromPage', 'The name this page is open on']);
  ((check && check.server && check.server.addresses) || []).filter((a) => IPV4.test(a.ip)).slice(0, 3)
    .forEach((a) => add(a.ip, ['mgmt.config.http.fork.hostnameFromServer', 'An address of the server on {{network}}', { network: a.network }]));
  return found;
};

// --- geo-blocking ---------------------------------------------------------

// The countries of `own` that the list being saved would refuse.
export const ownCountriesRefused = (countries, isAllowList, own) => {
  const list = new Set((countries || []).map((c) => String(c).toUpperCase()));
  return (own || []).map((c) => String(c).toUpperCase()).filter((c) => (isAllowList ? list.size > 0 && !list.has(c) : list.has(c)));
};

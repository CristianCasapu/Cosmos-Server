// Fork addition: what the panel says about a .local name, from the state the
// server reports (GET /cosmos/api/local-names, src/proxy/fork_mdns_status.go).
// Pure, so that it is tested with node --test (localNames.test.mjs). Every
// text is [translation key, English text, variables].

export const isLocalName = (host) => /^[^.]+(\.[^.]+)*\.local\.?$/i.test(String(host || '').trim());

// a.b.local: Windows and nss-mdns (most Linux machines) only resolve one word before .local
export const isMultiLabel = (host) => isLocalName(host) && String(host).trim().replace(/\.$/, '').slice(0, -'.local'.length).includes('.');

const lower = (host) => String(host || '').trim().toLowerCase().replace(/\.$/, '').split(':')[0];

export const findName = (status, host) => ((status && status.names) || []).find((entry) => entry.name === lower(host)) || null;

const where = (status) => ((status && status.interfaces) || []).map((iface) => iface.name + ' (' + iface.address + ')').join(', ');

// What keeps every name from resolving, or null.
export const blocker = (status) => {
  if (!status) return null;
  if (!status.enabled) {
    return { level: 'warning', text: ['mgmt.localNames.off', 'Cosmos does not publish .local names (Configuration > HTTP, "Publish .local domains"): such a name only works where a DNS entry points it at the server.'] };
  }
  if (status.avahi === 'absent') {
    return { level: 'error', text: ['mgmt.localNames.absent', 'Avahi is not running on the server, so no device can resolve .local names. Install and start it (Debian, Ubuntu: apt install avahi-daemon).'] };
  }
  if (!status.interfaces || status.interfaces.length === 0) {
    return { level: 'error', text: ['mgmt.localNames.noInterface', 'No network interface facing the local network was found, so the names are not published. Name the interface in Configuration > HTTP.'] };
  }
  return null;
};

// The state of one name: { level: success | info | warning | error, text, extra: [texts] }.
export const nameState = (status, host) => {
  if (!status || !isLocalName(host)) return null;
  const extra = [];
  if (isMultiLabel(host)) {
    extra.push(['mgmt.localNames.multiLabel', 'A name with a dot before .local is not resolved by Windows nor by most Linux machines: prefer a single word, such as app.local.']);
  }
  const blocked = blocker(status);
  if (blocked) return { ...blocked, extra };

  const entry = findName(status, host);
  const foreign = ((entry && entry.answers) || []).filter((answer) => !answer.own);
  if (foreign.length > 0 && (!entry || entry.state !== 'conflict')) {
    extra.push(['mgmt.localNames.foreign', 'Another device ({{from}}) also answers for this name.', { from: foreign.map((answer) => answer.from).join(', ') }]);
  }
  if (!entry) {
    return { level: 'info', text: ['mgmt.localNames.notYet', 'Published on {{where}} once the address is saved.', { where: where(status) }], extra };
  }
  switch (entry.state) {
    case 'published':
    case 'registering':
      if (entry.answered) {
        return { level: 'success', text: ['mgmt.localNames.answered', 'Published on {{where}}, and answered on the network.', { where: where(status) }], extra };
      }
      if (entry.checked) {
        return { level: 'warning', text: ['mgmt.localNames.unanswered', 'Published on {{where}}, but no answer came back on the network just now: check again in a moment.', { where: where(status) }], extra };
      }
      if (entry.state === 'registering') {
        return { level: 'info', text: ['mgmt.localNames.registering', 'Being published: Avahi first checks that no other device uses the name.'], extra };
      }
      return { level: 'success', text: ['mgmt.localNames.published', 'Published on {{where}}.', { where: where(status) }], extra };
    case 'hostName':
      return { level: 'success', text: ['mgmt.localNames.hostName', 'The server\'s own name: Avahi answers it.'], extra };
    case 'conflict':
      return {
        level: 'error',
        text: foreign.length > 0
          ? ['mgmt.localNames.conflictFrom', 'Another device ({{from}}) already uses this name, so Cosmos does not publish it. Give the app another address, or rename the other device; Cosmos tries again every ten minutes and on every save.', { from: foreign.map((answer) => answer.from).join(', ') }]
          : ['mgmt.localNames.conflict', 'Another device or program already uses this name, so Cosmos does not publish it. Give the app another address, or rename the other device; Cosmos tries again every ten minutes and on every save.'],
        extra,
      };
    case 'tooMany':
      return { level: 'error', text: ['mgmt.localNames.tooMany', 'Not published: Cosmos publishes at most 256 .local names.'], extra };
    case 'noInterface':
      return { level: 'error', text: ['mgmt.localNames.noInterface', 'No network interface facing the local network was found, so the names are not published. Name the interface in Configuration > HTTP.'], extra };
    default:
      return { level: 'error', text: entry.detail
        ? ['mgmt.localNames.failedDetail', 'Avahi refused this name: {{detail}}', { detail: entry.detail }]
        : ['mgmt.localNames.failed', 'Avahi refused this name.'], extra };
  }
};

// One line for the whole server (Configuration, the bulk dialog).
export const summary = (status) => {
  if (!status) return null;
  const blocked = blocker(status);
  if (blocked) return blocked;
  const names = status.names || [];
  const bad = names.filter((entry) => ['conflict', 'failed', 'tooMany', 'noInterface'].includes(entry.state));
  const unanswered = names.filter((entry) => entry.checked && !entry.answered && entry.state !== 'conflict');
  if (bad.length > 0) {
    return { level: 'error', text: ['mgmt.localNames.summaryBad', '{{count}} of the {{total}} .local names are not published: {{names}}.', { count: bad.length, total: names.length, names: bad.map((entry) => entry.name).join(', ') }] };
  }
  if (unanswered.length > 0) {
    return { level: 'warning', text: ['mgmt.localNames.summaryUnanswered', 'Published on {{where}}; no answer came back just now for {{names}}.', { where: where(status), names: unanswered.map((entry) => entry.name).join(', ') }] };
  }
  if (names.length === 0) {
    return { level: 'info', text: ['mgmt.localNames.summaryNone', 'Ready: .local names will be published on {{where}}.', { where: where(status) }] };
  }
  return { level: 'success', text: ['mgmt.localNames.summaryOk', '{{count}} .local names published on {{where}} and answered on the network.', { count: names.length, where: where(status) }] };
};

// Avahi itself announces the server's own name on every interface unless it
// is told which ones: say so when it is not.
export const avahiAdvice = (status) => {
  if (!status || !status.avahiConf || !status.avahiConf.read || status.avahi !== 'running') return null;
  const conf = status.avahiConf;
  if ((conf.allowInterfaces || []).length > 0 || !(status.interfaces || []).length) return null;
  return ['mgmt.localNames.avahiAllow', 'Avahi announces the server\'s own name on every interface, Docker\'s included, so the server and its containers may find it at a Docker address. Cosmos publishes the app names on {{names}} only, whatever Avahi does. To make Avahi do the same, set allow-interfaces={{names}} in the [server] section of /etc/avahi/avahi-daemon.conf and restart Avahi.', { names: status.interfaces.map((iface) => iface.name).join(',') }];
};

// The server's address on the local network, for the alternatives.
export const lanAddress = (status) => (((status && status.interfaces) || [])[0] || {}).address || '';

// Which devices resolve .local names (research of 2026-11, see the plan).
export const DEVICES = [
  { id: 'apple', works: 'yes', text: ['mgmt.localNames.device.apple', 'iPhone, iPad and Mac: yes.'] },
  { id: 'windows', works: 'yes', text: ['mgmt.localNames.device.windows', 'Windows 10 and 11: yes, without Bonjour, as long as the network is set to "Private" (on a "Public" network Windows may block the answers).'] },
  { id: 'android', works: 'mostly', text: ['mgmt.localNames.device.android', 'Android 12 and newer: usually (Android resolves .local names since late 2021). Some Chrome versions fail on some names where Firefox works; never over a VPN or mobile data.'] },
  { id: 'linux', works: 'mostly', text: ['mgmt.localNames.device.linux', 'Linux: yes with Avahi and nss-mdns (most desktop distributions). Not with systemd-resolved alone, whose mDNS is off unless switched on.'] },
  { id: 'old', works: 'no', text: ['mgmt.localNames.device.old', 'Android 11 and older, most smart TVs, consoles and many apps: no.'] },
  { id: 'remote', works: 'no', text: ['mgmt.localNames.device.remote', 'Away from home (Tailscale, VPN, mobile data): no, the names stay on the local network.'] },
];

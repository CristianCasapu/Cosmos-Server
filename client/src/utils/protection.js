// Fork addition: the plain logic of the protection cards on the security page
// (pages/security/protection.jsx): from the answer of GET /cosmos/api/protection
// (src/protection) to what each card says. Texts are [translation key, English
// text, parameters]. Nothing here imports the UI, so it can be exercised on its own.

// The cards, in the order they are shown.
export const PIECES = ['guard', 'fail2ban', 'updates'];

export const TITLES = {
  guard: ['protection.guard.title', 'Host guard'],
  fail2ban: ['protection.fail2ban.title', 'Fail2ban'],
  updates: ['protection.updates.title', 'Automatic security updates'],
};

// What each piece protects, in one sentence.
export const PROTECTS = {
  guard: ['protection.guard.protects', 'From outside the home network only Cosmos\' public port answers, for the server and for the containers\' published ports (IPv6 always, IPv4 when the server has a public IPv4 address).'],
  fail2ban: ['protection.fail2ban.protects', 'Bans addresses that keep failing to sign in or probe the server.'],
  updates: ['protection.updates.protects', 'Installs the system\'s security updates every day; never restarts the server by itself.'],
};

// The chip of each state: [colour of the MUI theme, text].
const CHIPS = {
  active: ['success', ['protection.chip.active', 'Active']],
  inactive: ['warning', ['protection.chip.inactive', 'Not active']],
  unknown: ['default', ['protection.chip.unknown', 'Unknown']],
  notInstalled: ['default', ['protection.chip.notInstalled', 'Not installed']],
};

// The sentence of a state (and its reason), per piece.
const STATES = {
  guard: {
    notInstalled: ['protection.guard.notInstalled', 'Not installed: the server\'s firewall is as the system left it.'],
    active: ['protection.guard.active', 'Active: the server and the containers\' published ports are filtered.'],
    tableMissing: ['protection.guard.tableMissing', 'Installed, but its rules are not loaded: nothing is filtered. Running its installer again loads them.'],
    notHooked: ['protection.guard.notHooked', 'Its rules are loaded but not in use: nothing is filtered. Running its installer again repairs this.'],
    noNft: ['protection.guard.noNft', 'Its state is not known: the firewall cannot be read here (the nft command is not on this system, or Cosmos runs in a container).'],
    notPermitted: ['protection.guard.notPermitted', 'Its state is not known: Cosmos is not allowed to read the firewall (it does not run as root).'],
    failed: ['protection.guard.failed', 'Its state is not known: the firewall did not answer just now.'],
  },
  fail2ban: {
    notInstalled: ['protection.fail2ban.notInstalled', 'Not installed: nothing is banned at the firewall.'],
    active: ['protection.fail2ban.active', 'Active: addresses are banned after repeated failures.'],
    stopped: ['protection.fail2ban.stopped', 'Installed, but Fail2ban is not running: nothing is banned.'],
    noJails: ['protection.fail2ban.noJails', 'Fail2ban is running without the fork\'s jails: nothing that happens in Cosmos leads to a ban.'],
    noBinary: ['protection.fail2ban.noBinary', 'Set up by the fork, but Fail2ban itself is no longer installed: nothing is banned.'],
  },
  updates: {
    notInstalled: ['protection.updates.notInstalled', 'Not set up: security updates are installed only when someone does it by hand.'],
    active: ['protection.updates.active', 'Active: security updates are installed every day.'],
    noBinary: ['protection.updates.noBinary', 'Set up, but the unattended-upgrades package is not installed: nothing is updated by itself.'],
    notEnabled: ['protection.updates.notEnabled', 'Set up, but switched off in APT\'s configuration: nothing is updated by itself.'],
    noTimer: ['protection.updates.noTimer', 'Set up, but the daily timer (apt-daily-upgrade.timer) is not enabled: nothing is updated by itself.'],
  },
};

const UNKNOWN_STATE = ['protection.state.other', 'Its state could not be told.'];

const list = (values) => (Array.isArray(values) ? values.filter((v) => Number.isInteger(v)) : []);
const portList = (values) => list(values).join(', ');

// count formats a number of packets in words a reader takes in at a glance.
export const count = (n) => {
  const value = Number(n) || 0;
  if (value < 10000) return String(value);
  if (value < 1e6) return (Math.round(value / 100) / 10) + 'k';
  if (value < 1e9) return (Math.round(value / 1e5) / 10) + 'M';
  return (Math.round(value / 1e8) / 10) + 'G';
};

// openLine: the ports the guard keeps open, from its table or (recorded) from
// what its installer wrote down.
const openLine = (tcpPorts, udpPorts, recorded) => {
  const tcp = portList(tcpPorts) || '-';
  const udp = portList(udpPorts);
  if (recorded) {
    return udp
      ? ['protection.guard.recordedUdp', 'Its installer keeps open: TCP {{tcp}}, UDP {{udp}}.', { tcp, udp }]
      : ['protection.guard.recorded', 'Its installer keeps open: TCP {{tcp}}.', { tcp }];
  }
  return udp
    ? ['protection.guard.openUdp', 'Open from outside: TCP {{tcp}}, UDP {{udp}}.', { tcp, udp }]
    : ['protection.guard.open', 'Open from outside: TCP {{tcp}}.', { tcp }];
};

// The lines under the state sentence, per piece.
const guardDetails = (piece) => {
  const lines = [];
  const table = piece.table;
  if (table && table.loaded) {
    lines.push(openLine(table.publicTcp, table.publicUdp, false));
    if (list(table.vpnUdp).length) {
      lines.push(['protection.guard.vpn', 'VPN ports open for direct connections: {{ports}}.', { ports: portList(table.vpnUdp) }]);
    }
    if (piece.active && table.droppedInput && table.droppedForward) {
      lines.push(['protection.guard.dropped', 'Refused from outside since it was loaded: {{input}} packets to the server, {{forward}} to containers.', {
        input: count(table.droppedInput.packets), forward: count(table.droppedForward.packets),
      }]);
    }
  } else if (piece.installed && list(piece.publicTcp).length) {
    lines.push(openLine(piece.publicTcp, piece.publicUdp, true));
  }
  if (piece.installed && piece.ipv4 === true) lines.push(['protection.guard.ipv4', 'IPv4 is filtered too.']);
  if (piece.installed && piece.ipv4 === false) lines.push(['protection.guard.ipv6only', 'Only IPv6 is filtered: the server has no public IPv4 address.']);
  if (piece.installed && piece.sshOpen === true) lines.push(['protection.guard.sshOpen', 'SSH answers from outside the home network.']);
  if (piece.installed && piece.sshOpen === false) lines.push(['protection.guard.sshClosed', 'SSH does not answer from outside the home network.']);
  return lines;
};

const fail2banDetails = (piece) => {
  const lines = [];
  if (!piece.installed) return lines;
  if (piece.sshd === true) lines.push(['protection.fail2ban.sshd', 'SSH logins are watched too.']);
  if (piece.sshd === false) lines.push(['protection.fail2ban.noSshd', 'SSH logins are not watched.']);
  if (piece.cosmosJails === false) lines.push(['protection.fail2ban.noCosmos', 'The jails for Cosmos were left out when it was installed.']);
  return lines;
};

const updatesDetails = (piece) => {
  const lines = [];
  if (piece.by === 'administrator') {
    lines.push(['protection.updates.byAdmin', 'Set up by this system\'s administrator, outside the fork: remove it the way it was set up.']);
  }
  if (piece.config) lines.push(['protection.updates.config', 'Switched on in {{file}}.', { file: piece.config }]);
  if (piece.incomplete) lines.push(['protection.updates.incomplete', 'APT\'s configuration was not read whole (too many or too large files): this state may be wrong.']);
  return lines;
};

const DETAILS = { guard: guardDetails, fail2ban: fail2banDetails, updates: updatesDetails };

// cardOf turns one piece of the answer into what its card shows.
//   id          guard, fail2ban or updates
//   piece       the piece's part of the answer
//   cosmosPort  the answer's cosmosPort
//   formatDate  turns an ISO time into the reader's words
export const cardOf = (id, piece, cosmosPort, formatDate = (iso) => iso) => {
  const p = piece || {};
  const state = CHIPS[p.state] ? p.state : 'unknown';
  const [chipColor, chipText] = CHIPS[state];
  const texts = STATES[id] || {};
  const sentence = texts[p.reason] || texts[state] || UNKNOWN_STATE;
  const lines = [];
  if (p.installed && p.since) {
    lines.push(['protection.since', 'Since {{date}}.', { date: formatDate(p.since) }]);
  }
  lines.push(...(DETAILS[id] || (() => []))(p));
  if (p.reason === 'stateInvalid') {
    lines.push(['protection.stateInvalid', 'Its record on the server (in /var/lib/cosmos-protection) cannot be read: running its installer again writes it anew.']);
  }

  const warnings = [];
  if (id === 'guard' && p.portMissing) {
    const kept = portList(p.keptOpen);
    warnings.push(kept
      ? ['protection.guard.portMissing', 'Cosmos now uses port {{port}}; the guard keeps {{kept}} open: run the installer again.', { port: cosmosPort, kept }]
      : ['protection.guard.portMissingNone', 'Cosmos now uses port {{port}}; the guard keeps no TCP port open: run the installer again.', { port: cosmosPort }]);
  }

  let command = null;
  if (typeof p.command === 'string' && p.command !== '' && (p.commandKind === 'install' || p.commandKind === 'uninstall')) {
    command = {
      text: p.command,
      label: p.commandKind === 'install'
        ? ['protection.command.install', 'To install it, run on the server:']
        : ['protection.command.uninstall', 'To remove it, run on the server:'],
    };
  }

  let severity = 'info';
  if (state === 'active') severity = 'success';
  if (state === 'inactive') severity = 'warning';

  return {
    id,
    title: TITLES[id],
    protects: PROTECTS[id],
    chip: { color: chipColor, text: chipText },
    severity,
    sentence,
    lines,
    warnings,
    command,
  };
};

// cardsOf: the three cards of an answer (its `data`), or [] without one.
export const cardsOf = (status, formatDate) => {
  if (!status || typeof status !== 'object') return [];
  return PIECES.map((id) => cardOf(id, status[id], status.cosmosPort, formatDate));
};

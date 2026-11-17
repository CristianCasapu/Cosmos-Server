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
  guard: ['protection.guard.protects', 'From outside the home network only the ports it keeps open answer, for the server and for the containers\' published ports.'],
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
    tableMissing: ['protection.guard.tableMissing', 'Not protecting: the guard\'s rules are not loaded (something removed them, nftables.service for example). Load them again: sudo systemctl reload cosmos-host-guard'],
    notHooked: ['protection.guard.notHooked', 'Not protecting: its rules are loaded but not hooked into the firewall, so nothing is filtered. Load them again: sudo systemctl reload cosmos-host-guard'],
    chainEmpty: ['protection.guard.chainEmpty', 'Not protecting: its table is loaded but its rules were emptied (flushed), so nothing is filtered. Load them again: sudo systemctl reload cosmos-host-guard'],
    noOutside: ['protection.guard.noOutside', 'Not filtering: no outside interface was found. The guard looks again whenever the network changes.'],
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

// recordedLine: the ports the guard is set to keep open, from its table or
// from what its installer wrote down, for a guard that does not filter now.
const recordedLine = (tcpPorts, udpPorts) => {
  const tcp = portList(tcpPorts) || '-';
  const udp = portList(udpPorts);
  return udp
    ? ['protection.guard.recordedUdp', 'Its installer keeps open: TCP {{tcp}}, UDP {{udp}}.', { tcp, udp }]
    : ['protection.guard.recorded', 'Its installer keeps open: TCP {{tcp}}.', { tcp }];
};

const names = (values) => (Array.isArray(values) ? values.filter((v) => typeof v === 'string' && v !== '') : []);

// What answers from outside, for IPv6 alone or for both families.
const ANSWERS = {
  six: {
    tcp: ['protection.guard.answers6', 'From outside over IPv6, only TCP {{tcp}} answer.'],
    tcpUdp: ['protection.guard.answers6Udp', 'From outside over IPv6, only TCP {{tcp}} and UDP {{udp}} answer.'],
    udp: ['protection.guard.answers6UdpOnly', 'From outside over IPv6, only UDP {{udp}} answer.'],
    none: ['protection.guard.answers6None', 'From outside over IPv6, nothing answers.'],
  },
  both: {
    tcp: ['protection.guard.answers', 'From outside over IPv6 and IPv4, only TCP {{tcp}} answer.'],
    tcpUdp: ['protection.guard.answersUdp', 'From outside over IPv6 and IPv4, only TCP {{tcp}} and UDP {{udp}} answer.'],
    udp: ['protection.guard.answersUdpOnly', 'From outside over IPv6 and IPv4, only UDP {{udp}} answer.'],
    none: ['protection.guard.answersNone', 'From outside over IPv6 and IPv4, nothing answers.'],
  },
};

// Why IPv4 is not filtered, from what the installer recorded.
const IPV4_OPEN = {
  'behind-router': ['protection.guard.ipv4BehindRouter', 'IPv4 is NOT filtered by the guard: the server has a private IPv4 address. That is safe only if the router forwards just the ports you chose — not when the server is the router\'s exposed host (DMZ) or a cloud server with 1:1 NAT; then run the installer again with --ipv4 yes (exposed host) or --exposed (cloud server).'],
  'asked-no': ['protection.guard.ipv4AskedNo', 'IPv4 is NOT filtered by the guard (installed with --ipv4 no).'],
};
const IPV4_OPEN_OTHER = ['protection.guard.ipv4Open', 'IPv4 is NOT filtered by the guard.'];

// ipv4Filtered: from the table when it is known (the installer's pass for
// IPv4 is a rule of its own), else from what the installer recorded; null
// when neither says.
const ipv4Filtered = (piece) => {
  const table = piece.table;
  if (table && table.loaded) return table.ipv4Passed !== true;
  if (piece.ipv4 === true || piece.ipv4 === false) return piece.ipv4;
  return null;
};

// What answers from outside, built from the table of a guard that filters.
const answerLines = (piece, table, formatDate) => {
  const lines = [];
  const filtered4 = ipv4Filtered(piece) === true;
  const tcp = portList(table.publicTcp);
  const udp = portList(table.publicUdp);
  const family = ANSWERS[filtered4 ? 'both' : 'six'];
  let answers = family.none;
  if (tcp && udp) answers = family.tcpUdp;
  else if (tcp) answers = family.tcp;
  else if (udp) answers = family.udp;
  lines.push([answers[0], answers[1], { tcp, udp }]);
  if (list(table.vpnUdp).length) {
    lines.push(['protection.guard.vpn', 'Also UDP {{ports}}, for direct VPN connections to the server.', { ports: portList(table.vpnUdp) }]);
  }

  // SSH: public, or open to the sessions that installed the guard
  const sshPorts = list(table.sshPorts);
  if (sshPorts.length) {
    const port = portList(sshPorts);
    if (sshPorts.some((p) => list(table.publicTcp).includes(p))) {
      lines.push(['protection.guard.sshPublic', 'SSH (port {{port}}) answers everyone from outside.', { port }]);
    } else {
      const allowed = Array.isArray(table.sshAllowed) ? table.sshAllowed : [];
      for (const allowance of allowed) {
        if (!allowance || typeof allowance.address !== 'string') continue;
        const four = !allowance.address.includes(':');
        if (four && !filtered4) continue; // IPv4 is not filtered: the allowance changes nothing
        if (typeof allowance.until === 'string' && allowance.until !== '') {
          lines.push(['protection.guard.sshAllowed', 'SSH (port {{port}}) also answers {{address}} until {{until}}.', { port, address: allowance.address, until: formatDate(allowance.until) }]);
        } else {
          lines.push(['protection.guard.sshAllowedAlways', 'SSH (port {{port}}) also answers {{address}}, with no end set.', { port, address: allowance.address }]);
        }
      }
    }
  }

  if (!filtered4 && ipv4Filtered(piece) === false) {
    lines.push(IPV4_OPEN[piece.ipv4Reason] || IPV4_OPEN_OTHER);
  }

  // what counts as home, per outside interface
  const lan = names(table.lanInterfaces);
  const home = names(table.homeNetworks);
  for (const ifname of names(table.wanInterfaces)) {
    if (lan.includes(ifname)) {
      lines.push(['protection.guard.homeLan', 'Home network on {{ifname}}: its own prefixes count as home.', { ifname }]);
    } else if (home.length) {
      lines.push(['protection.guard.homeNamed', 'On {{ifname}} only the networks named with --home count as home (the segment is the provider\'s).', { ifname }]);
    } else {
      lines.push(['protection.guard.homeNone', 'On {{ifname}} nothing counts as home (the segment is the provider\'s, and no network was named with --home).', { ifname }]);
    }
  }
  if (lan.some((ifname) => names(table.wanInterfaces).includes(ifname))) {
    lines.push(['protection.guard.homeForged', 'A packet from the internet that claims an address of the home network\'s own prefix cannot be told apart on the server: dropping those is the router\'s job (most routers do).']);
  }
  if (home.length) {
    lines.push(['protection.guard.homeNetworks', 'Named with --home, home on every interface: {{networks}}.', { networks: home.join(', ') }]);
  }
  if (names(table.failClosedInterfaces).length) {
    lines.push(filtered4
      ? ['protection.guard.failClosedBoth', 'No default route was found: IPv6 and IPv4 are filtered on every interface until one appears.']
      : ['protection.guard.failClosed', 'No default route was found: IPv6 is filtered on every interface until one appears.']);
  }
  return lines;
};

// The lines under the state sentence, per piece.
const guardDetails = (piece, formatDate) => {
  const lines = [];
  const table = piece.table;
  if (piece.active && table && table.loaded) {
    lines.push(...answerLines(piece, table, formatDate));
  } else if (piece.installed && table && table.loaded) {
    lines.push(recordedLine(table.publicTcp, table.publicUdp));
  } else if (piece.installed && list(piece.publicTcp).length) {
    lines.push(recordedLine(piece.publicTcp, piece.publicUdp));
  }
  const others = names(piece.otherFilters);
  if (piece.installed && others.length) {
    lines.push(['protection.guard.otherFilters', 'Other rules also filter what reaches this server: {{tables}}. Both filter; a drop in either is final. The guard never removes them.', { tables: others.join(', ') }]);
    if (others.includes('inet cosmos_guard')) {
      lines.push(['protection.guard.handMade', 'A guard made by hand (inet cosmos_guard) is also loaded: once this one works, it can be removed.']);
    }
  }
  if (piece.active && table && table.droppedInput && table.droppedForward) {
    const input = count(table.droppedInput.packets);
    const forward = count(table.droppedForward.packets);
    lines.push(table.droppedSpoofed
      ? ['protection.guard.droppedSpoofed', 'Refused from outside since it was loaded: {{input}} packets to the server, {{forward}} to containers, {{spoofed}} with a forged source.', { input, forward, spoofed: count(table.droppedSpoofed.packets) }]
      : ['protection.guard.dropped', 'Refused from outside since it was loaded: {{input}} packets to the server, {{forward}} to containers.', { input, forward }]);
  }
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
  lines.push(...(DETAILS[id] || (() => []))(p, formatDate));
  if (p.reason === 'stateInvalid' || p.stateFile === 'invalid') {
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

  // the command that loads the guard's rules again, beside the uninstaller
  let repair = null;
  if (id === 'guard' && typeof p.repairCommand === 'string' && p.repairCommand !== '') {
    repair = { text: p.repairCommand, label: ['protection.command.repair', 'To load its rules again, run on the server:'] };
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
    repair,
    command,
  };
};

// cardsOf: the three cards of an answer (its `data`), or [] without one.
export const cardsOf = (status, formatDate) => {
  if (!status || typeof status !== 'object') return [];
  return PIECES.map((id) => cardOf(id, status[id], status.cosmosPort, formatDate));
};

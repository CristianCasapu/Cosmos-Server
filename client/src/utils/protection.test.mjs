// node --test client/src/utils/protection.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { PIECES, cardOf, cardsOf, count } from './protection.js';

// Answers as src/protection gives them (src/protection/protection_test.go checks that shape).
const NOTHING = {
  time: '2026-11-17T10:00:00Z',
  cosmosPort: 2053,
  guard: { installed: false, active: false, state: 'notInstalled', stateFile: 'missing', command: 'sudo sh /opt/cosmos/fork/nftables/install.sh', commandKind: 'install', table: { loaded: false, inputHooked: false, forwardHooked: false, publicTcp: [], publicUdp: [], vpnUdp: [], wanInterfaces: [] }, publicTcp: [], publicUdp: [], portMissing: false, keptOpen: [] },
  fail2ban: { installed: false, active: false, state: 'notInstalled', stateFile: 'missing', command: 'sudo sh /opt/cosmos/fork/fail2ban/install.sh', commandKind: 'install', clientInstalled: false, running: false, configInstalled: false },
  updates: { installed: false, active: false, state: 'notInstalled', stateFile: 'missing', command: 'sudo sh /opt/cosmos/fork/unattended-upgrades/install.sh', commandKind: 'install', binaryInstalled: false, enabled: false, timerEnabled: false, incomplete: false },
};

const ALL = {
  time: '2026-11-17T10:00:00Z',
  cosmosPort: 2053,
  guard: {
    installed: true, active: true, state: 'active', since: '2026-11-17T10:00:00Z', by: 'fork', stateFile: 'ok',
    command: 'sudo sh /opt/cosmos/fork/nftables/uninstall.sh', commandKind: 'uninstall',
    table: {
      loaded: true, inputHooked: true, forwardHooked: true, inputFilters: true, forwardFilters: true, ipv4Passed: false,
      droppedInput: { packets: 123456, bytes: 7 }, droppedForward: { packets: 12, bytes: 1 },
      publicTcp: [2052, 2053], publicUdp: [], vpnUdp: [41641], wanInterfaces: ['eth0'], lanInterfaces: ['eth0'], failClosedInterfaces: [],
      sshPorts: [22], sshAllowed: [], homeNetworks: [],
    },
    publicTcp: [2052, 2053], publicUdp: [], ipv4: true, sshOpen: false, ipv4Reason: 'public-address', otherFilters: [], portMissing: false, keptOpen: [2052, 2053],
  },
  fail2ban: { installed: true, active: true, state: 'active', since: '2026-11-16T09:00:00Z', by: 'fork', stateFile: 'ok', command: 'sudo sh /opt/cosmos/fork/fail2ban/uninstall.sh', commandKind: 'uninstall', clientInstalled: true, running: true, configInstalled: true, sshd: true, cosmosJails: true },
  updates: { installed: true, active: true, state: 'active', since: '2026-11-15T08:00:00Z', by: 'fork', stateFile: 'ok', command: 'sudo sh /opt/cosmos/fork/unattended-upgrades/uninstall.sh', commandKind: 'uninstall', binaryInstalled: true, enabled: true, timerEnabled: true, config: '/etc/apt/apt.conf.d/52cosmos-security-upgrades', incomplete: false },
};

const keys = (texts) => texts.map((entry) => entry[0]);

test('nothing installed: three cards with the installers', () => {
  const cards = cardsOf(NOTHING);
  assert.deepEqual(cards.map((c) => c.id), PIECES);
  for (const card of cards) {
    assert.equal(card.chip.text[0], 'protection.chip.notInstalled');
    assert.equal(card.severity, 'info');
    assert.match(card.sentence[0], /notInstalled$/);
    assert.equal(card.command.label[0], 'protection.command.install');
    assert.match(card.command.text, /install\.sh$/);
    assert.ok(!keys(card.lines).includes('protection.since'));
    assert.deepEqual(card.warnings, []);
  }
});

test('all installed and active: since, details and the uninstallers', () => {
  const cards = cardsOf(ALL, (iso) => 'on ' + iso.slice(0, 10));
  for (const card of cards) {
    assert.equal(card.chip.color, 'success');
    assert.equal(card.severity, 'success');
    assert.equal(card.command.label[0], 'protection.command.uninstall');
    assert.match(card.lines[0][2].date, /^on 2026-11-1[567]$/);
  }
  const [guard, fail2ban, updates] = cards;
  assert.deepEqual(keys(guard.lines), ['protection.since', 'protection.guard.answers', 'protection.guard.vpn', 'protection.guard.homeLan', 'protection.guard.homeForged', 'protection.guard.dropped']);
  assert.deepEqual(guard.lines[1][2], { tcp: '2052, 2053', udp: '' });
  assert.deepEqual(guard.lines[3][2], { ifname: 'eth0' });
  assert.deepEqual(guard.lines[5][2], { input: '123.5k', forward: '12' });
  // no claim about SSH that the table does not support
  assert.ok(!guard.lines.some((line) => /SSH/.test(line[1])));
  assert.deepEqual(keys(fail2ban.lines), ['protection.since', 'protection.fail2ban.sshd']);
  assert.deepEqual(keys(updates.lines), ['protection.since', 'protection.updates.config']);
});

test('guard installed but its table not loaded', () => {
  const guard = { ...ALL.guard, active: false, state: 'inactive', reason: 'tableMissing', table: { ...NOTHING.guard.table } };
  const card = cardOf('guard', guard, 2053);
  assert.equal(card.chip.color, 'warning');
  assert.equal(card.severity, 'warning');
  assert.equal(card.sentence[0], 'protection.guard.tableMissing');
  // plainly not protected
  assert.match(card.sentence[1], /^Not protecting: /);
  // what the installer recorded stands in for the table
  assert.ok(keys(card.lines).includes('protection.guard.recorded'));
  assert.ok(!keys(card.lines).includes('protection.guard.dropped'));
  assert.ok(!keys(card.lines).some((key) => /answers/.test(key)));
  assert.equal(card.command.label[0], 'protection.command.uninstall');
  // the server's reload command beside the uninstaller
  assert.equal(card.repair, null);
  const repaired = cardOf('guard', { ...guard, repairCommand: 'sudo systemctl reload cosmos-host-guard' }, 2053);
  assert.deepEqual(repaired.repair, { text: 'sudo systemctl reload cosmos-host-guard', label: ['protection.command.repair', 'To load its rules again, run on the server:'] });
  assert.equal(repaired.command.label[0], 'protection.command.uninstall');
  assert.match(repaired.sentence[1], /sudo systemctl reload cosmos-host-guard$/);
});

// the guard as the server reads today's table (src/protection/testdata/nft-guard-v2-*.json)
const guardWith = (table, extra = {}) => ({ ...ALL.guard, ...extra, table: { ...ALL.guard.table, ...table } });
const words = (card) => card.lines.map((line) => line[1].replace(/\{\{(\w+)\}\}/g, (_, name) => String(line[2]?.[name])));

test('flushed, no outside interface: not active, said plainly', () => {
  const flushed = cardOf('guard', guardWith({ inputFilters: false }, { active: false, state: 'inactive', reason: 'chainEmpty', repairCommand: 'sudo systemctl reload cosmos-host-guard' }), 2053);
  assert.equal(flushed.chip.text[0], 'protection.chip.inactive');
  assert.equal(flushed.sentence[0], 'protection.guard.chainEmpty');
  assert.ok(flushed.repair);
  assert.deepEqual(keys(flushed.lines), ['protection.since', 'protection.guard.recorded']);
  const nowan = cardOf('guard', guardWith({ wanInterfaces: [] }, { active: false, state: 'inactive', reason: 'noOutside' }), 2053);
  assert.equal(nowan.sentence[1].split('.')[0], 'Not filtering: no outside interface was found');
  assert.equal(nowan.severity, 'warning');
});

test('what answers: IPv6 only, UDP, VPN, SSH allowances with their end', () => {
  const card = cardOf('guard', guardWith({
    ipv4Passed: true, publicTcp: [2053], publicUdp: [443], lanInterfaces: [], wanInterfaces: ['eth0'],
    sshAllowed: [{ address: '198.51.100.7', expires: 7199, until: '2026-11-17T12:00:00Z' }, { address: '2001:db8:2::20', expires: 86399, until: '2026-11-18T10:00:00Z' }, { address: '2001:db8:2::21' }],
  }, { ipv4: false, ipv4Reason: 'asked-no' }), 2053, (iso) => 'local ' + iso);
  assert.deepEqual(words(card).slice(1), [
    'From outside over IPv6, only TCP 2053 and UDP 443 answer.',
    'Also UDP 41641, for direct VPN connections to the server.',
    // the IPv4 allowance is left out: IPv4 is not filtered at all
    'SSH (port 22) also answers 2001:db8:2::20 until local 2026-11-18T10:00:00Z.',
    'SSH (port 22) also answers 2001:db8:2::21, with no end set.',
    'IPv4 is NOT filtered by the guard (installed with --ipv4 no).',
    'On eth0 nothing counts as home (the segment is the provider\'s, and no network was named with --home).',
    'Refused from outside since it was loaded: 123.5k packets to the server, 12 to containers.',
  ]);
  // IPv4 filtered: both families, and the IPv4 allowance counts
  const both = cardOf('guard', guardWith({ sshAllowed: [{ address: '198.51.100.7', until: '2026-11-17T12:00:00Z' }], publicTcp: [2053] }), 2053, (iso) => iso);
  assert.deepEqual(words(both).slice(1, 4), [
    'From outside over IPv6 and IPv4, only TCP 2053 answer.',
    'Also UDP 41641, for direct VPN connections to the server.',
    'SSH (port 22) also answers 198.51.100.7 until 2026-11-17T12:00:00Z.',
  ]);
  // SSH among the public ports: everyone, and no allowance repeated
  const open = cardOf('guard', guardWith({ publicTcp: [22, 2053], sshAllowed: [{ address: '2001:db8:2::20', until: '2026-11-18T10:00:00Z' }] }), 2053);
  assert.ok(words(open).includes('SSH (port 22) answers everyone from outside.'));
  assert.ok(!keys(open.lines).includes('protection.guard.sshAllowed'));
  // nothing kept open
  assert.equal(cardOf('guard', guardWith({ publicTcp: [], vpnUdp: [] }), 2053).lines[1][0], 'protection.guard.answersNone');
  assert.equal(cardOf('guard', guardWith({ publicTcp: [], publicUdp: [51820] }), 2053).lines[1][0], 'protection.guard.answersUdpOnly');
});

test('IPv4 not filtered: the reason the installer recorded; the table decides', () => {
  const behind = cardOf('guard', guardWith({ ipv4Passed: true }, { ipv4: false, ipv4Reason: 'behind-router' }), 2053);
  assert.ok(keys(behind.lines).includes('protection.guard.ipv4BehindRouter'));
  assert.match(behind.lines.find((l) => l[0] === 'protection.guard.ipv4BehindRouter')[1], /--ipv4 yes \(exposed host\) or --exposed \(cloud server\)/);
  assert.equal(behind.lines[1][0], 'protection.guard.answers6');
  // no reason recorded (an older installer)
  const plain = cardOf('guard', guardWith({ ipv4Passed: true }, { ipv4: false, ipv4Reason: undefined }), 2053);
  assert.ok(keys(plain.lines).includes('protection.guard.ipv4Open'));
  // the record says yes, the table passes IPv4: the table wins
  const table = cardOf('guard', guardWith({ ipv4Passed: true }, { ipv4: true, ipv4Reason: 'public-address' }), 2053);
  assert.ok(keys(table.lines).includes('protection.guard.ipv4Open'));
  // filtered: nothing to warn about
  assert.ok(!keys(cardOf('guard', guardWith({}), 2053).lines).some((key) => /^protection\.guard\.ipv4/.test(key)));
});

test('home: per outside interface, and the --home networks', () => {
  const card = cardOf('guard', guardWith({ wanInterfaces: ['eth0', 'wlan0'], lanInterfaces: ['eth0'], homeNetworks: ['192.0.2.0/24', '2001:db8:5::/48'] }), 2053);
  assert.deepEqual(words(card).filter((line) => /home/.test(line)), [
    'Home network on eth0: its own prefixes count as home.',
    'On wlan0 only the networks named with --home count as home (the segment is the provider\'s).',
    'A packet from the internet that claims an address of the home network\'s own prefix cannot be told apart on the server: dropping those is the router\'s job (most routers do).',
    'Named with --home, home on every interface: 192.0.2.0/24, 2001:db8:5::/48.',
  ]);
});

test('fail closed: active, with the note', () => {
  const six = cardOf('guard', guardWith({ ipv4Passed: true, failClosedInterfaces: ['eth0'] }, { ipv4: false }), 2053);
  assert.equal(six.chip.text[0], 'protection.chip.active');
  assert.ok(words(six).includes('No default route was found: IPv6 is filtered on every interface until one appears.'));
  const both = cardOf('guard', guardWith({ failClosedInterfaces: ['eth0'] }), 2053);
  assert.ok(keys(both.lines).includes('protection.guard.failClosedBoth'));
  assert.ok(!keys(cardOf('guard', guardWith({}), 2053).lines).some((key) => /failClosed/.test(key)));
});

test('other filters, and the guard made by hand', () => {
  const card = cardOf('guard', guardWith({ droppedSpoofed: { packets: 3, bytes: 1 } }, { otherFilters: ['inet firewalld', 'ip filter', 'inet cosmos_guard'] }), 2053);
  assert.ok(words(card).includes('Other rules also filter what reaches this server: inet firewalld, ip filter, inet cosmos_guard. Both filter; a drop in either is final. The guard never removes them.'));
  assert.ok(keys(card.lines).includes('protection.guard.handMade'));
  assert.ok(words(card).includes('Refused from outside since it was loaded: 123.5k packets to the server, 12 to containers, 3 with a forged source.'));
  const one = cardOf('guard', guardWith({}, { otherFilters: ['inet firewalld'] }), 2053);
  assert.ok(!keys(one.lines).includes('protection.guard.handMade'));
  assert.ok(!keys(cardOf('guard', guardWith({}), 2053).lines).includes('protection.guard.otherFilters'));
  // shown for a guard that does not filter, too
  assert.ok(keys(cardOf('guard', guardWith({}, { active: false, state: 'inactive', reason: 'chainEmpty', otherFilters: ['inet firewalld'] }), 2053).lines).includes('protection.guard.otherFilters'));
});

test('an unreadable record is said also when the guard does not filter', () => {
  const card = cardOf('guard', guardWith({ inputFilters: false }, { active: false, state: 'inactive', reason: 'chainEmpty', stateFile: 'invalid' }), 2053);
  assert.equal(card.sentence[0], 'protection.guard.chainEmpty');
  assert.ok(keys(card.lines).includes('protection.stateInvalid'));
});

test('unknown: the reason in words, no error', () => {
  for (const reason of ['noNft', 'notPermitted', 'failed']) {
    const card = cardOf('guard', { installed: true, active: false, state: 'unknown', reason, unknown: reason, publicTcp: [2053], publicUdp: [] }, 2053);
    assert.equal(card.chip.text[0], 'protection.chip.unknown');
    assert.equal(card.severity, 'info');
    assert.equal(card.sentence[0], 'protection.guard.' + reason);
  }
  // a state or reason this panel does not know
  const card = cardOf('fail2ban', { state: 'exploded', reason: 'why' }, 0);
  assert.equal(card.chip.text[0], 'protection.chip.unknown');
  assert.equal(card.sentence[0], 'protection.state.other');
  assert.equal(card.command, null);
});

test('the port warning names both ports', () => {
  const card = cardOf('guard', { ...ALL.guard, portMissing: true, keptOpen: [443] }, 8443);
  assert.deepEqual(card.warnings, [['protection.guard.portMissing', 'Cosmos now uses port {{port}}; the guard keeps {{kept}} open: run the installer again.', { port: 8443, kept: '443' }]]);
  const none = cardOf('guard', { ...ALL.guard, portMissing: true, keptOpen: [] }, 8443);
  assert.equal(none.warnings[0][0], 'protection.guard.portMissingNone');
});

test('updates set up by the administrator: said so, no command', () => {
  const card = cardOf('updates', { installed: true, active: true, state: 'active', by: 'administrator', since: '2025-01-01T00:00:00Z', stateFile: 'missing', config: '/etc/apt/apt.conf.d/20auto-upgrades', binaryInstalled: true, enabled: true, timerEnabled: true }, 2053);
  assert.equal(card.command, null);
  assert.ok(keys(card.lines).includes('protection.updates.byAdmin'));
});

test('a state file that cannot be read is said; inactive updates name what is missing', () => {
  assert.ok(keys(cardOf('fail2ban', { ...ALL.fail2ban, reason: 'stateInvalid', stateFile: 'invalid' }).lines).includes('protection.stateInvalid'));
  for (const reason of ['noBinary', 'notEnabled', 'noTimer']) {
    assert.equal(cardOf('updates', { installed: true, state: 'inactive', reason }).sentence[0], 'protection.updates.' + reason);
  }
});

test('a command is shown only as the server gives it, with a known kind', () => {
  assert.equal(cardOf('guard', { state: 'notInstalled', command: 'sudo sh x', commandKind: 'run' }).command, null);
  assert.equal(cardOf('guard', { state: 'notInstalled', command: 42, commandKind: 'install' }).command, null);
  assert.equal(cardOf('guard', { state: 'notInstalled', command: '', commandKind: 'install' }).command, null);
});

test('no answer, no cards', () => {
  assert.deepEqual(cardsOf(null), []);
  assert.deepEqual(cardsOf('nope'), []);
  assert.equal(cardsOf({}).length, 3);
});

test('count: packets in short words', () => {
  assert.equal(count(0), '0');
  assert.equal(count(9999), '9999');
  assert.equal(count(12345), '12.3k');
  assert.equal(count(2500000), '2.5M');
  assert.equal(count(7.25e9), '7.3G');
  assert.equal(count(undefined), '0');
});

test('every text has its key in the English translation with the same words', () => {
  const english = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));
  const source = readFileSync(new URL('./protection.js', import.meta.url), 'utf8');
  const pairs = [...source.matchAll(/\['(protection\.[^']+)', '((?:[^'\\]|\\.)*)'/g)];
  assert.ok(pairs.length > 30);
  for (const [, key, text] of pairs) {
    assert.equal(english[key], text.replace(/\\'/g, '\''), key);
  }
});

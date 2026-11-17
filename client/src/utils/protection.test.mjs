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
    table: { loaded: true, inputHooked: true, forwardHooked: true, droppedInput: { packets: 123456, bytes: 7 }, droppedForward: { packets: 12, bytes: 1 }, publicTcp: [2052, 2053], publicUdp: [], vpnUdp: [41641], wanInterfaces: ['eth0'] },
    publicTcp: [2052, 2053], publicUdp: [], ipv4: true, sshOpen: false, portMissing: false, keptOpen: [2052, 2053],
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
  assert.deepEqual(keys(guard.lines), ['protection.since', 'protection.guard.open', 'protection.guard.vpn', 'protection.guard.dropped', 'protection.guard.ipv4', 'protection.guard.sshClosed']);
  assert.deepEqual(guard.lines[1][2], { tcp: '2052, 2053' });
  const udp = cardOf('guard', { ...ALL.guard, table: { ...ALL.guard.table, publicUdp: [443] } }, 2053);
  assert.deepEqual(udp.lines[1], ['protection.guard.openUdp', 'Open from outside: TCP {{tcp}}, UDP {{udp}}.', { tcp: '2052, 2053', udp: '443' }]);
  assert.deepEqual(guard.lines[3][2], { input: '123.5k', forward: '12' });
  assert.deepEqual(keys(fail2ban.lines), ['protection.since', 'protection.fail2ban.sshd']);
  assert.deepEqual(keys(updates.lines), ['protection.since', 'protection.updates.config']);
});

test('guard installed but its table not loaded', () => {
  const guard = { ...ALL.guard, active: false, state: 'inactive', reason: 'tableMissing', table: { ...NOTHING.guard.table } };
  const card = cardOf('guard', guard, 2053);
  assert.equal(card.chip.color, 'warning');
  assert.equal(card.severity, 'warning');
  assert.equal(card.sentence[0], 'protection.guard.tableMissing');
  // what the installer recorded stands in for the table
  assert.ok(keys(card.lines).includes('protection.guard.recorded'));
  assert.ok(!keys(card.lines).includes('protection.guard.dropped'));
  assert.equal(card.command.label[0], 'protection.command.uninstall');
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

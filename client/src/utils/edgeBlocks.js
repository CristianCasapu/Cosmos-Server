// Fork addition: the words of the card "Block abusers at Cloudflare"
// (components/edgeBlocks.jsx; server side src/edgeblock). Every text is a
// [translation key, English text, values] triple, so that a test can check
// each against the English translation file.

export const PERMISSION_NAME = 'Zone > Firewall Services > Edit';
export const TOKENS_URL = 'https://dash.cloudflare.com/profile/api-tokens';

export const REASONS = {
  auth: ['mgmt.edge.reason.auth', 'Failed sign-ins'],
  probe: ['mgmt.edge.reason.probe', 'Probes for vulnerable paths'],
  scan: ['mgmt.edge.reason.scan', 'Scanning (unknown names, refused bots)'],
  abuse: ['mgmt.edge.reason.abuse', 'Abuse SmartShield stopped'],
  repeat: ['mgmt.edge.reason.repeat', 'Repeat offender'],
  manual: ['mgmt.edge.reason.manual', 'Blocked by hand'],
};

export const MODES = {
  block: ['mgmt.edge.mode.block', 'Blocked'],
  managed_challenge: ['mgmt.edge.mode.challenge', 'Challenged (a person can pass)'],
};

export const STATES = {
  pending: ['mgmt.edge.state.pending', 'Being sent to Cloudflare'],
  active: ['mgmt.edge.state.active', 'In force at Cloudflare'],
  deleting: ['mgmt.edge.state.deleting', 'Being lifted'],
  gone: ['mgmt.edge.state.gone', 'Deleted in Cloudflare\'s dashboard: not made again for this offence'],
  owner: ['mgmt.edge.state.owner', 'A rule of yours exists for this address at Cloudflare: left as it is'],
  refused: ['mgmt.edge.state.refused', 'Cloudflare refused this address'],
  orphaned: ['mgmt.edge.state.orphaned', 'Still at Cloudflare, but the domain has no token any more: delete it by hand'],
};

export const EXEMPT = {
  invalid: ['mgmt.edge.exempt.invalid', 'it is not a public address'],
  local: ['mgmt.edge.exempt.local', 'it belongs to the local network'],
  server: ['mgmt.edge.exempt.server', 'it is this server\'s own address or network'],
  cloudflare: ['mgmt.edge.exempt.cloudflare', 'it is one of Cloudflare\'s own addresses'],
  whitelist: ['mgmt.edge.exempt.whitelist', 'it is on SmartShield\'s whitelist or the admin address list'],
  fail2ban: ['mgmt.edge.exempt.fail2ban', 'it is on the Fail2ban never-ban list'],
  admin: ['mgmt.edge.exempt.admin', 'an administrator signed in from it lately (or you are using it now)'],
};

export const DURATIONS = [
  { value: '1h', label: ['mgmt.edge.duration.1h', 'One hour'] },
  { value: '1d', label: ['mgmt.edge.duration.1d', 'One day'] },
  { value: '7d', label: ['mgmt.edge.duration.7d', 'One week'] },
  { value: '30d', label: ['mgmt.edge.duration.30d', '30 days'] },
];

export const MANUAL_MODES = [
  { value: 'block', label: ['mgmt.edge.mode.block', 'Blocked'], description: ['mgmt.edge.manual.blockHelp', 'Nothing from it reaches the server through Cloudflare.'] },
  { value: 'managed_challenge', label: ['mgmt.edge.mode.challenge', 'Challenged (a person can pass)'], description: ['mgmt.edge.manual.challengeHelp', 'Cloudflare asks it to show it is a person first.'] },
];

const PROBLEMS = {
  permission: ['mgmt.edge.problem.permission', 'The token of {{zone}} may not manage IP Access Rules. In Cloudflare\'s dashboard (My Profile > API Tokens), edit the token and add the permission "{{permission}}" for this domain; then press "Try again".'],
  invalid_token: ['mgmt.edge.problem.token', 'Cloudflare refuses the token of {{zone}}. Put in a valid token for the domain.'],
  rate_limited: ['mgmt.edge.problem.rateLimited', 'Cloudflare asked to slow down: Cosmos waits until {{until}} and then goes on.'],
  unreachable: ['mgmt.edge.problem.unreachable', 'Cloudflare could not be reached for {{zone}}: Cosmos tries again by itself (next try {{until}}).'],
  server: ['mgmt.edge.problem.server', 'Cloudflare answered with an error for {{zone}}: Cosmos tries again by itself (next try {{until}}).'],
  not_found: ['mgmt.edge.problem.notFound', 'The token does not see the domain {{zone}} at Cloudflare.'],
  other: ['mgmt.edge.problem.other', 'Something went wrong talking to Cloudflare for {{zone}}: Cosmos tries again by itself (next try {{until}}).'],
};

const STEPS = {
  zone: ['mgmt.edge.step.zone', 'find the domain'],
  list: ['mgmt.edge.step.list', 'list the IP Access Rules'],
  create: ['mgmt.edge.step.create', 'make a test rule'],
  delete: ['mgmt.edge.step.delete', 'delete the test rule'],
};

const FAILURES = {
  proof_failed: ['mgmt.edge.fail.proof', 'The test did not pass for every domain: nothing was switched on.'],
  off: ['mgmt.edge.fail.off', 'Blocking at Cloudflare is off: switch it on first.'],
  not_an_address: ['mgmt.edge.fail.address', 'That is not an IP address.'],
  duration: ['mgmt.edge.fail.duration', 'Pick how long.'],
  mode: ['mgmt.edge.fail.mode', 'Pick block or challenge.'],
  no_proven_zone: ['mgmt.edge.fail.noZone', 'No domain has passed the test: switch the feature on again.'],
  no_shielded_zone: ['mgmt.edge.fail.noShielded', 'No address is served through Cloudflare\'s proxy yet: there is nothing to block at Cloudflare.'],
  full: ['mgmt.edge.fail.full', 'Cosmos already keeps the most rules it may at Cloudflare.'],
  not_blocked: ['mgmt.edge.fail.notBlocked', 'This address is not blocked at Cloudflare (any more).'],
  no_answer: ['mgmt.edge.fail.noAnswer', 'The server did not answer.'],
};

const UNKNOWN_FAILURE = ['mgmt.edge.fail.unknown', 'That did not work ({{reason}}).'];

export const PROOF_OK = ['mgmt.edge.proof.ok', '{{zone}}: the token may list, make and delete IP Access Rules.'];
export const PROOF_INFERRED = ['mgmt.edge.proof.inferred', '{{zone}}: the token may list and delete IP Access Rules; Cloudflare refused the test address itself, after checking the permission.'];
export const PROOF_FAILED = ['mgmt.edge.proof.failed', '{{zone}}: the test stopped at "{{step}}". {{problem}}'];
export const PROOF_LEFT = ['mgmt.edge.proof.left', 'The test rule for 192.0.2.1 could not be deleted yet: Cosmos deletes it as soon as it can.'];

const fmtTime = (value, format) => (value ? format(value) : '');

// problemText says what is wrong with a zone.
export const problemText = (problem, format = (v) => v) => {
  if (!problem) return null;
  const text = PROBLEMS[problem.reason] || PROBLEMS.other;
  return [text[0], text[1], { zone: problem.zone, permission: PERMISSION_NAME, until: fmtTime(problem.until, format) }];
};

// failureText turns the route's refusal into a sentence.
export const failureText = (res) => {
  const reason = (res && res.reason) || 'no_answer';
  if (reason === 'exempt') {
    const exempt = EXEMPT[res.exempt] || EXEMPT.invalid;
    return ['mgmt.edge.fail.exempt', 'This address is never blocked at Cloudflare: {{why}}.', { why: exempt[1], whyKey: exempt[0] }];
  }
  const text = FAILURES[reason];
  if (text) return text;
  return [UNKNOWN_FAILURE[0], UNKNOWN_FAILURE[1], { reason: String(reason).slice(0, 40) }];
};

// proofText says how the test of one zone went.
export const proofText = (result) => {
  if (result.ok && result.inferred) return [PROOF_INFERRED[0], PROOF_INFERRED[1], { zone: result.zone }];
  if (result.ok) return [PROOF_OK[0], PROOF_OK[1], { zone: result.zone }];
  const step = STEPS[result.step] || STEPS.list;
  const problem = PROBLEMS[result.reason] || PROBLEMS.other;
  return [PROOF_FAILED[0], PROOF_FAILED[1], {
    zone: result.zone, step: step[1], stepKey: step[0], problem: problem[1], problemKey: problem[0], permission: PERMISSION_NAME, until: '',
  }];
};

// durationText says a number of seconds in words, roughly.
export const durationText = (seconds) => {
  const s = Number(seconds) || 0;
  if (s % 86400 === 0 && s >= 86400) {
    const days = s / 86400;
    if (days % 7 === 0) return days === 7 ? ['mgmt.edge.time.week', 'a week'] : ['mgmt.edge.time.weeks', '{{count}} weeks', { count: days / 7 }];
    return days === 1 ? ['mgmt.edge.time.day', 'a day'] : ['mgmt.edge.time.days', '{{count}} days', { count: days }];
  }
  if (s % 3600 === 0 && s >= 3600) {
    return s === 3600 ? ['mgmt.edge.time.hour', 'an hour'] : ['mgmt.edge.time.hours', '{{count}} hours', { count: s / 3600 }];
  }
  const minutes = Math.max(1, Math.round(s / 60));
  return minutes === 1 ? ['mgmt.edge.time.minute', 'a minute'] : ['mgmt.edge.time.minutes', '{{count}} minutes', { count: minutes }];
};

// jailText is one line of "what counts": "Failed sign-ins: 5 within an hour,
// then challenged for 4 hours".
export const jailText = (jail) => {
  const reason = REASONS[jail.name] || REASONS.auth;
  const mode = MODES[jail.mode] || MODES.block;
  return ['mgmt.edge.jail', '{{what}}: {{count}} within {{window}}, then {{mode}} for {{length}}', {
    what: reason[1], whatKey: reason[0], count: jail.maxRetry,
    window: durationText(jail.findTime), length: durationText(jail.banTime), mode: mode[1].toLowerCase(), modeKey: mode[0],
  }];
};

// stateText is the card's first sentence: [level, text].
export const stateText = (status) => {
  if (!status) return null;
  const zones = status.zones || [];
  if (zones.length === 0) {
    return ['info', ['mgmt.edge.status.noZone', 'No address is served through Cloudflare\'s proxy yet. Once one is, abusers who come through Cloudflare can be blocked there.']];
  }
  if (!status.enabled) {
    return ['info', ['mgmt.edge.status.off', 'Off. Abusers who come through Cloudflare are only refused inside Cosmos (SmartShield), for as long as it runs.']];
  }
  const troubled = zones.filter((zone) => zone.problem);
  if (troubled.length > 0) {
    return ['warning', ['mgmt.edge.status.trouble', 'On, but Cosmos cannot reach Cloudflare as it should for {{zones}}: see below.', { zones: troubled.map((zone) => zone.name).join(', ') }]];
  }
  const live = status.live || 0;
  if (live === 0) {
    return ['success', ['mgmt.edge.status.onNone', 'On: nothing is blocked at Cloudflare right now.']];
  }
  return ['success', live === 1
    ? ['mgmt.edge.status.onOne', 'On: one rule is in force at Cloudflare.']
    : ['mgmt.edge.status.onMany', 'On: {{count}} rules are in force at Cloudflare.', { count: live }]];
};

// rulesShown are the lines of the list: what is or was at Cloudflare, newest first.
export const rulesShown = (status) => {
  const rules = (status && status.rules) || [];
  return rules.slice().sort((a, b) => String(b.since).localeCompare(String(a.since)));
};

// canUnblock says whether a line offers "Unblock".
export const canUnblock = (rule) => ['pending', 'active', 'gone', 'owner', 'refused'].includes(rule.state);

export const ruleLabel = (rule) => REASONS[rule.reason] || REASONS.manual;
export const modeLabel = (rule) => MODES[rule.mode] || MODES.block;
export const stateLabel = (rule) => STATES[rule.state] || STATES.active;

// keyOf is the unit the server blocks an address by: the address for IPv4,
// its /64 for IPv6. Used to find the rule of an address on the dashboard.
export const keyOf = (address) => {
  const text = String(address || '').trim().replace(/^\[|\]$/g, '');
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(text)) return text;
  if (/^::ffff:\d{1,3}(\.\d{1,3}){3}$/i.test(text)) return text.slice(7);
  if (!text.includes(':') || !/^[0-9a-fA-F:]+$/.test(text)) return '';
  // expand to eight groups, keep the first four
  const [head, tail] = text.split('::');
  if (text.split('::').length > 2) return '';
  const left = head ? head.split(':') : [];
  const right = tail !== undefined && tail !== '' ? tail.split(':') : [];
  const missing = tail === undefined ? 0 : 8 - left.length - right.length;
  if (missing < 0) return '';
  const groups = [...left, ...Array(missing).fill('0'), ...right];
  if (groups.length !== 8 || groups.some((g) => g === '' || g.length > 4)) return '';
  const all = [...groups.slice(0, 4).map((g) => parseInt(g, 16).toString(16)), '0', '0', '0', '0'];
  // the canonical form (RFC 5952, as Go prints it): the first longest run
  // of two or more zero groups becomes "::"
  let best = -1; let bestLen = 1;
  for (let i = 0; i < 8; i += 1) {
    let j = i;
    while (j < 8 && all[j] === '0') j += 1;
    if (j - i > bestLen) { best = i; bestLen = j - i; }
    if (j > i) i = j - 1;
  }
  const text6 = best === -1 ? all.join(':') : all.slice(0, best).join(':') + '::' + all.slice(best + bestLen).join(':');
  return text6 + '/64';
};

// blockOf finds the rule in force at Cloudflare for an address.
export const blockOf = (blocked, address) => {
  if (!blocked) return null;
  const key = keyOf(address);
  return key ? blocked[key] || null : null;
};

// looksLocal is a first check in the form; the server decides.
export const looksLocal = (address) => /^(10\.|127\.|192\.168\.|169\.254\.|172\.(1[6-9]|2\d|3[01])\.|100\.(6[4-9]|[7-9]\d|1[01]\d|12[0-7])\.)/.test(address)
  || /^(fe80:|fc|fd|::1$)/i.test(address);

// every fixed text of this module, for the test against the translation file
export const ALL_TEXTS = [
  ...Object.values(REASONS), ...Object.values(MODES), ...Object.values(STATES), ...Object.values(EXEMPT),
  ...DURATIONS.map((d) => d.label), ...MANUAL_MODES.flatMap((m) => [m.label, m.description]),
  ...Object.values(PROBLEMS), ...Object.values(STEPS), ...Object.values(FAILURES), UNKNOWN_FAILURE,
  PROOF_OK, PROOF_INFERRED, PROOF_FAILED, PROOF_LEFT,
  ['mgmt.edge.fail.exempt', 'This address is never blocked at Cloudflare: {{why}}.'],
  ['mgmt.edge.jail', '{{what}}: {{count}} within {{window}}, then {{mode}} for {{length}}'],
  ['mgmt.edge.time.week', 'a week'], ['mgmt.edge.time.weeks', '{{count}} weeks'], ['mgmt.edge.time.day', 'a day'], ['mgmt.edge.time.days', '{{count}} days'],
  ['mgmt.edge.time.hour', 'an hour'], ['mgmt.edge.time.hours', '{{count}} hours'], ['mgmt.edge.time.minute', 'a minute'], ['mgmt.edge.time.minutes', '{{count}} minutes'],
  ['mgmt.edge.status.noZone', 'No address is served through Cloudflare\'s proxy yet. Once one is, abusers who come through Cloudflare can be blocked there.'],
  ['mgmt.edge.status.off', 'Off. Abusers who come through Cloudflare are only refused inside Cosmos (SmartShield), for as long as it runs.'],
  ['mgmt.edge.status.trouble', 'On, but Cosmos cannot reach Cloudflare as it should for {{zones}}: see below.'],
  ['mgmt.edge.status.onNone', 'On: nothing is blocked at Cloudflare right now.'],
  ['mgmt.edge.status.onOne', 'On: one rule is in force at Cloudflare.'],
  ['mgmt.edge.status.onMany', 'On: {{count}} rules are in force at Cloudflare.'],
];

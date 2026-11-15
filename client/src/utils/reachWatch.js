// Fork addition: what the screens say about the watch of the server's
// reachability from the internet (src/reachwatch): the line of the home
// page, the state of each name, the history, the sentences of the
// reachability check's "outside" line. Nothing here imports the UI, so it
// can be exercised on its own (reachWatch.test.mjs).

// The cause in a few words: the same keys and English as the server's
// notifications (src/utils/fork_reach_watch_texts.go).
export const CAUSE_TEXT = {
  seen: 'Reached from the internet',
  connect: 'Cloudflare cannot connect (error 522)',
  refused: 'Connection refused before Cosmos (error 521)',
  timeout: 'No answer on the port (error 524)',
  foreign: 'Another server answers',
  unreachable: 'The record\'s address cannot be reached (error 523)',
  tlsHandshake: 'No encrypted connection (error 525)',
  tlsCertificate: 'Certificate not accepted by Cloudflare (error 526)',
  unexpected: 'An answer Cloudflare cannot use (error 520)',
  originDNS: 'The record leads nowhere (error 530)',
  originRefused: 'Cosmos refuses Cloudflare (origin pulls)',
  dnsNotFound: 'The name does not exist',
  dnsNotCloudflare: 'The name no longer goes through Cloudflare',
  edgeCert: 'Cloudflare\'s certificate is not valid',
  cloudflare: 'An error at Cloudflare',
  dnsLocal: 'A local DNS answers: cannot be tested from here',
  offline: 'This server cannot reach the internet: nothing can be concluded',
  noAnswer: 'Cloudflare did not answer in time: nothing can be concluded',
  challenged: 'Cloudflare asked the server\'s own request for a challenge: nothing can be concluded',
  edgeBlocked: 'Cloudflare blocked the server\'s own request (a rule at Cloudflare): visitors are not concerned',
};

export const causeText = (cause) => (Object.prototype.hasOwnProperty.call(CAUSE_TEXT, cause)
  ? ['notif.reach.cause.' + cause, CAUSE_TEXT[cause]]
  : ['reachWatch.cause.other', 'Not tested yet']);

export const STATE_TEXT = {
  pending: ['reachWatch.state.pending', 'Not tested yet'],
  reachable: ['reachWatch.state.reachable', 'Reachable from the internet'],
  suspect: ['reachWatch.state.suspect', 'A test failed: being confirmed'],
  down: ['reachWatch.state.down', 'Cannot be reached from the internet'],
  unknown: ['reachWatch.state.unknown', 'Could not be tested'],
};
export const stateText = (state) => STATE_TEXT[state] || STATE_TEXT.pending;

// The dot of a name's state (components/statusDot.jsx).
export const stateDot = (state) => ({ reachable: 'success', down: 'error', suspect: 'warning' })[state] || 'unknown';

// The texts of the card and the home line.
export const TEXT = {
  title: ['reachWatch.title', 'Watch from the internet'],
  intro: ['reachWatch.intro', 'Every few minutes Cosmos sends a small request to each name served through Cloudflare. It goes out to Cloudflare and comes back in through your router, like a visitor\'s: so Cosmos sees for itself when the server cannot be reached from outside, and tells you (also on Telegram, which works even then).'],
  switchLabel: ['reachWatch.switch', 'Watch whether the server can be reached from the internet'],
  interval: ['reachWatch.interval', 'Test every'],
  minutes: ['reachWatch.minutes', '{{count}} minutes'],
  checkNow: ['reachWatch.checkNow', 'Test now'],
  checkStarted: ['reachWatch.checkStarted', 'Testing through Cloudflare now: the result shows here within a minute.'],
  tooSoon: ['reachWatch.tooSoon', 'A test is running or ran moments ago: try again in a few seconds.'],
  names: ['reachWatch.names', 'Names tested from the internet'],
  lastTest: ['reachWatch.lastTest', 'last test {{time}}'],
  seenAt: ['reachWatch.seenAt', 'seen from outside at {{time}}'],
  since: ['reachWatch.since', 'since {{time}}'],
  panel: ['reachWatch.panel', 'the panel'],
  nextRound: ['reachWatch.nextRound', 'Next test about {{time}}.'],
  off: ['reachWatch.off', 'The watch is switched off: nothing is sent.'],
  direct: ['reachWatch.direct', 'Served directly, not through Cloudflare: {{names}}. Cosmos cannot test these names from inside the house (the request would only be turned around by the router). What the reachability check shows for them (the forward on the router, whether the name points at the public address, the certificate) is checked from the server, not from outside.'],
  tooMany: ['reachWatch.tooMany', 'Not watched, too many names at once (50 at most): {{names}}.'],
  reasonNone: ['reachWatch.reason.none', 'No name is served through Cloudflare\'s proxy, so nothing can be tested from the internet and nothing is sent. Names served directly cannot be tested from inside the house.'],
  reasonNoHTTPS: ['reachWatch.reason.noHTTPS', 'Cosmos does not serve HTTPS, so nothing is tested.'],
  reasonPort: ['reachWatch.reason.port', 'Visitors use port {{port}}, which Cloudflare does not pass on: nothing is tested.'],
  history: ['reachWatch.history', 'When it could not be reached'],
  historyNone: ['reachWatch.historyNone', 'No interruption seen yet.'],
  historyRow: ['reachWatch.historyRow', '{{from}}: {{duration}} — {{cause}}'],
  historyOpen: ['reachWatch.historyOpen', 'since {{from}} — {{cause}}'],
  historyStopped: ['reachWatch.historyStopped', '{{from}}: {{duration}} — {{cause}} (the watch was switched off before it was back)'],
  router: ['reachWatch.router', 'The router module, asked at {{time}}: {{answer}}.'],
  seenAs: ['reachWatch.seenAs', 'Cloudflare sees this server as {{address}}.'],
  homeDown: ['reachWatch.home.down', 'Cosmos cannot be reached from the internet since {{since}}: {{cause}}.'],
  homeOffline: ['reachWatch.home.offline', 'Could not test whether Cosmos can be reached from the internet: this server cannot reach the internet itself.'],
  homeOpen: ['reachWatch.home.open', 'Open the watch'],
  lessThanMinute: ['reachWatch.lessThanMinute', 'less than a minute'],
  duration: ['reachWatch.duration', '{{count}} min'],
  durationHours: ['reachWatch.durationHours', '{{hours}} h {{minutes}} min'],
};

// The sentences of the reachability check's "outside" line (src/reach,
// fork_watch.go), in the form of reachability.js' CHECK_TEXT.
export const OUTSIDE_CHECK_TEXT = {
  'outside.seen': ['seen', 'Seen answering from the internet at {{at}}: a test request went out through Cloudflare and came back in to this server.', null],
  'outside.down': ['down', 'Cannot be reached from the internet since {{since}} (confirmed by three tests in a row).', 'internet'],
  'outside.pending': ['pending', 'The watch from the internet has not confirmed this name yet.', null],
  'outside.unknown': ['unknown', 'The last test from the internet could not conclude.', null],
  'outside.off': ['off', 'The watch from the internet is switched off.', 'internet'],
  'outside.direct': ['direct', 'Served directly, not through Cloudflare: Cosmos cannot test this name from inside the house. What is shown here is checked from the server, not from outside.', null],
};

// The times in the outside sentences, made readable.
export const outsideParams = (params) => {
  const out = { ...(params || {}) };
  ['at', 'since'].forEach((key) => {
    if (out[key]) {
      const date = new Date(out[key]);
      out[key] = isNaN(date) ? out[key] : date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    }
  });
  return out;
};

const pad = (n) => String(n).padStart(2, '0');

// A time as the sentences give it: "09:12", or with the date when it is not
// today (now: for the tests).
export const clock = (value, now = new Date()) => {
  const d = new Date(value);
  if (!value || isNaN(d)) return '';
  const time = pad(d.getHours()) + ':' + pad(d.getMinutes());
  if (d.toDateString() === new Date(now).toDateString()) return time;
  return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) + ' ' + time;
};

// How long, as [key, English, values].
export const duration = (ms) => {
  const minutes = Math.round((Number(ms) || 0) / 60000);
  if (minutes < 1) return [...TEXT.lessThanMinute, {}];
  if (minutes < 60) return [...TEXT.duration, { count: minutes }];
  return [...TEXT.durationHours, { hours: Math.floor(minutes / 60), minutes: minutes % 60 }];
};

const isList = (value) => Array.isArray(value);

// The line of the home page, or null: an outage, or every name untestable
// because the server has no way out.
export const homeNotice = (status, now = new Date()) => {
  if (!status || !status.enabled) return null;
  if (status.outage) {
    return { id: 'reachWatch:down', severity: 'error', text: TEXT.homeDown,
      params: { since: clock(status.outage.since, now) }, cause: causeText(status.outage.cause) };
  }
  const names = isList(status.names) ? status.names : [];
  if (names.length > 0 && names.every((n) => n.state === 'unknown' && n.cause === 'offline')) {
    return { id: 'reachWatch:offline', severity: 'unknown', text: TEXT.homeOffline, params: {} };
  }
  return null;
};

// One name of the card: its state, the cause when it is not reachable, and
// when it was last tested or seen.
export const nameLine = (n, now = new Date()) => {
  const line = { host: String(n && n.host || ''), panel: !!(n && n.panel), state: stateText(n && n.state), dot: stateDot(n && n.state), parts: [] };
  if (!n) return line;
  if (n.state === 'down' || n.state === 'suspect' || n.state === 'unknown') {
    line.cause = causeText(n.cause);
  }
  if (n.state === 'down' && n.since) line.parts.push([...TEXT.since, { time: clock(n.since, now) }]);
  if (n.seenAt) line.parts.push([...TEXT.seenAt, { time: clock(n.seenAt, now) }]);
  if (n.lastTest) line.parts.push([...TEXT.lastTest, { time: clock(n.lastTest, now) }]);
  return line;
};

// The history, newest first, as rows of [key, English, values] with the cause apart.
export const historyRows = (history, now = new Date()) => (isList(history) ? history : []).slice(0, 20).map((o) => {
  const from = clock(o.since, now);
  const cause = causeText(o.cause);
  if (!o.end) return { text: TEXT.historyOpen, params: { from }, cause };
  const [dk, dt, dv] = duration(new Date(o.end) - new Date(o.since));
  return { text: o.stopped ? TEXT.historyStopped : TEXT.historyRow, params: { from }, duration: [dk, dt, dv], cause };
});

// Why nothing is tested, as [key, English, values], or null.
export const reasonText = (status) => {
  if (!status) return null;
  if (!status.enabled) return [...TEXT.off, {}];
  switch (status.reason) {
    case 'none': return [...TEXT.reasonNone, {}];
    case 'noHTTPS': return [...TEXT.reasonNoHTTPS, {}];
    case 'port': return [...TEXT.reasonPort, { port: status.port }];
    default: return null;
  }
};

// The intervals offered, within the server's bounds.
export const INTERVALS = [2, 5, 10, 15, 30, 60];
export const intervalsFor = (status) => {
  const min = (status && status.minMinutes) || 2;
  const max = (status && status.maxMinutes) || 60;
  const list = INTERVALS.filter((m) => m >= min && m <= max);
  const current = status && status.minutes;
  if (current && !list.includes(current)) list.push(current);
  return list.sort((a, b) => a - b);
};

// What the settings route is sent: nothing else.
export const settingsBody = (enabled, minutes) => {
  const body = {};
  if (typeof enabled === 'boolean') body.enabled = enabled;
  const m = Number(minutes);
  if (Number.isInteger(m) && m >= 2 && m <= 60) body.minutes = m;
  return body;
};

// Every text of the module, for the test against translation.json.
export const allTexts = () => [
  ...Object.entries(CAUSE_TEXT).map(([cause, text]) => ['notif.reach.cause.' + cause, text]),
  ['reachWatch.cause.other', 'Not tested yet'],
  ...Object.values(STATE_TEXT),
  ...Object.values(TEXT),
  ...Object.values(OUTSIDE_CHECK_TEXT).map(([code, text]) => ['mgmt.reach.check.outside.' + code, text]),
];

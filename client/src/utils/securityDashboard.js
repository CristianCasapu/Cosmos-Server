// Fork addition: the pure logic of the security dashboard
// (pages/security): the plain-word sentences for every kind of incident,
// the groups, the status line's rules, the grouping of repeated events and
// the period arithmetic. No React, no i18n: every text is returned as
// [translation key, English text, parameters] for the page to pass to t().
// Tested by securityDashboard.test.mjs (node --test).

// The groups of incidents, in the order the page shows them. The kinds of
// each group come from the server (src/security/groups.go).
export const GROUP_ORDER = ['login', 'lockout', 'ban', 'scan', 'access', 'other'];

// The groups that get a counter; "other" is mostly harmless.
export const COUNTER_GROUPS = ['login', 'lockout', 'ban', 'scan', 'access'];

export const GROUP_TEXT = {
  login: ['Failed sign-ins', 'Wrong passwords or two-factor codes, bad invitation links, unknown API tokens, forged session cookies.'],
  lockout: ['Lock-outs', 'Addresses blocked for an hour by SmartShield, two-factor checks closed after wrong codes, addresses slowed down for sending too much.'],
  ban: ['Bans', 'Addresses SmartShield banned after being blocked again and again.'],
  scan: ['Scans and probes', 'Automated scanners looking for weaknesses, unknown server names, bots and blocked countries.'],
  access: ['Refused access', 'Requests for something the sender may not have: a permission, an address restriction, a local-only URL.'],
  other: ['Other', 'Mostly harmless: expired sessions and tokens, password reset forms, requests from addresses that are already blocked.'],
};

export const groupLabel = (id) => {
  const entry = GROUP_TEXT[id];
  return entry ? ['security.group.' + id, entry[0]] : ['security.group.unknown', 'Other'];
};

export const groupDescription = (id) => {
  const entry = GROUP_TEXT[id];
  return entry ? ['security.group.' + id + '.help', entry[1]] : ['security.group.unknown.help', ''];
};

// Fixed places the advice can point to. Nothing from an event ever becomes
// a link.
export const LINKS = {
  users: { url: '/cosmos-ui/config-users', label: ['security.link.users', 'Open the users'] },
  shield: { url: '/cosmos-ui/config-url/shield', label: ['security.link.shield', 'Open SmartShield'] },
  urls: { url: '/cosmos-ui/config-url', label: ['security.link.urls', 'Open the URLs'] },
};

// stopped: 'yes' Cosmos refused or blocked it; 'no' it was let through (not
// an attack as such); 'na' it is about the record itself.
// link: one of LINKS, for the advice.
export const KIND_TEXT = {
  'auth.login.failed': {
    label: 'Failed sign-in',
    what: 'Someone tried to sign in with a wrong user name or password.',
    stopped: 'yes',
    todo: 'Nothing, if it was a user mistyping. Many attempts from one internet address are password guessing: SmartShield blocks the address after 20 in an hour, and strong passwords with two-factor sign-in keep the accounts safe.',
    link: 'users',
  },
  'auth.mfa.failed': {
    label: 'Wrong two-factor code',
    what: 'A wrong two-factor code was entered after the right password.',
    stopped: 'yes',
    todo: 'Usually a typo. If it was not the user, someone knows that account\'s password: change it.',
    link: 'users',
  },
  'auth.mfa.locked': {
    label: 'Two-factor check locked',
    what: 'After too many wrong two-factor codes Cosmos closed the code check of the account for a while.',
    stopped: 'yes',
    todo: 'Whoever tried knows the account\'s password. Unless it was the user, change that password now.',
    link: 'users',
  },
  'auth.sudo.failed': {
    label: 'Wrong password at the confirmation',
    what: 'A signed-in user typed a wrong password when Cosmos asked to confirm it before a change.',
    stopped: 'yes',
    todo: 'Usually a typo. If you do not know that session, change the user\'s password: it signs out every session.',
    link: 'users',
  },
  'auth.session.forged': {
    label: 'Forged session',
    what: 'A browser showed a session cookie that this server never issued, or that was altered.',
    stopped: 'yes',
    todo: 'Nothing to do: it was refused. Once per browser is normal after Cosmos\' sign-in key was renewed; many from one address are an attempt to break in.',
  },
  'auth.session.rejected': {
    label: 'Expired session',
    what: 'A browser showed a session that is no longer valid (it expired, or the password was changed).',
    stopped: 'yes',
    todo: 'Harmless: the user is asked to sign in again.',
  },
  'auth.reset.requested': {
    label: 'Password reset asked for',
    what: 'Someone sent the "forgot password" form with the name and email of an account: a reset link was mailed to that account\'s address.',
    stopped: 'no',
    todo: 'Harmless if the user asked for it. If not, the link only reaches the account\'s own mailbox: the user can ignore it.',
  },
  'auth.reset.rejected': {
    label: 'Password reset for an unknown account',
    what: 'The "forgot password" form was sent with a name and email that match no account.',
    stopped: 'yes',
    todo: 'Nothing to do: nothing was sent.',
  },
  'auth.register.failed': {
    label: 'Bad invitation link',
    what: 'Someone used an invitation or password reset link that is wrong or has expired.',
    stopped: 'yes',
    todo: 'If a user cannot get in, make a new link for them. Many attempts mean someone is guessing links.',
    link: 'users',
  },
  'token.invalid': {
    label: 'Unknown API token',
    what: 'A request came with an API token that does not exist.',
    stopped: 'yes',
    todo: 'A script or app may still use a deleted token: give it a new one. Otherwise someone is guessing tokens.',
    link: 'users',
  },
  'token.expired': {
    label: 'Expired API token',
    what: 'A request came with an API token that has expired.',
    stopped: 'yes',
    todo: 'If an app of yours stopped working, give it a new token.',
    link: 'users',
  },
  'token.ip.denied': {
    label: 'API token from a wrong address',
    what: 'A valid API token was used from an address it is not allowed from.',
    stopped: 'yes',
    todo: 'If you did not move that app, the token may have leaked: delete it and make a new one.',
    link: 'users',
  },
  'access.denied': {
    label: 'Permission refused',
    what: 'A signed-in user or an API token asked for something it has no permission for.',
    stopped: 'yes',
    todo: 'Usually a user opening a page they may not use. If it repeats, check that user\'s role.',
    link: 'users',
  },
  'access.grant.denied': {
    label: 'Refused to hand out a permission',
    what: 'Someone who may manage users or tokens tried to give a permission they do not hold themselves.',
    stopped: 'yes',
    todo: 'This does not happen by accident: check who it was and what their role allows.',
    link: 'users',
  },
  'access.ip.denied': {
    label: 'Address not allowed',
    what: 'A request came from an address that is not on an allowed list (the admin address restriction or a URL\'s own).',
    stopped: 'yes',
    todo: 'Nothing, if it was a stranger. If it was you, add your address to the URL\'s allowed addresses.',
    link: 'urls',
  },
  'access.local.denied': {
    label: 'Local-only URL asked from outside',
    what: 'A URL that is served on the local network only was asked for from the internet.',
    stopped: 'yes',
    todo: 'Nothing to do. To reach it from outside, change the URL\'s settings.',
    link: 'urls',
  },
  'shield.blocked': {
    label: 'Request from a blocked address',
    what: 'SmartShield turned a request away because its sender is over its limits or banned.',
    stopped: 'yes',
    todo: 'Nothing to do: the block is working.',
  },
  'shield.strike': {
    label: 'Blocked for an hour',
    what: 'An address went over a SmartShield limit (requests, failed sign-ins or data) and is blocked for an hour.',
    stopped: 'yes',
    todo: 'Nothing to do. If it is one of your own devices, lift the block in the list of addresses.',
    link: 'shield',
  },
  'shield.ban.temporary': {
    label: 'Temporary ban',
    what: 'An address that was blocked three times is banned for a longer while.',
    stopped: 'yes',
    todo: 'Nothing to do. If it is one of your own devices, lift the ban in the list of addresses.',
    link: 'shield',
  },
  'shield.ban.permanent': {
    label: 'Permanent ban',
    what: 'An address that was banned again and again stays banned until you lift the ban.',
    stopped: 'yes',
    todo: 'Nothing to do. Lift the ban only if the address is one of yours.',
    link: 'shield',
  },
  'firewall.ban': {
    label: 'Banned by Fail2ban',
    what: 'Fail2ban banned an address at the firewall: nothing from it reaches the server until the ban ends.',
    stopped: 'yes',
    todo: 'Nothing to do. If it is one of your own devices, lift the ban in the Fail2ban section of this page.',
    link: null,
  },
  'shield.geo.blocked': {
    label: 'Blocked by country',
    what: 'A request came from a country your settings block.',
    stopped: 'yes',
    todo: 'Nothing to do.',
  },
  'shield.bot.blocked': {
    label: 'Bot blocked',
    what: 'A URL\'s bot filter refused a request that did not say which browser sent it, or came from a listed bot.',
    stopped: 'yes',
    todo: 'Nothing to do. If an app of yours is refused, turn the bot filter off in that URL\'s settings.',
    link: 'urls',
  },
  'shield.hostname.unknown': {
    label: 'Unknown server name',
    what: 'A request asked for a server name this server does not serve: typically a scanner trying the bare IP address.',
    stopped: 'yes',
    todo: 'Background noise of the internet: nothing to do. From the local network it means a device opens Cosmos by its IP address.',
  },
  'shield.ratelimited': {
    label: 'Too many requests',
    what: 'An address sent more than 500 requests a minute and was slowed down.',
    stopped: 'yes',
    todo: 'Nothing to do. If it is an app of yours, it may be stuck in a loop.',
  },
  'scan.probe': {
    label: 'Probe for a known weakness',
    what: 'Someone asked for a file that attack tools look for (/.env, /wp-login.php, /.git/config and the like). It does not exist here.',
    stopped: 'yes',
    todo: 'Nothing to do: automated scanners try this on every server on the internet.',
  },
  'recorder.dropped': {
    label: 'Security events lost',
    what: 'Cosmos could not store some security events in time because the database did not answer.',
    stopped: 'na',
    todo: 'If it happens again, check the free disk space and the load of the server.',
  },
};

// The sentences of a kind as [key, English] pairs; for a kind the page does
// not know (a newer server), the server's own label and description.
export const kindText = (kind, catalogue) => {
  const entry = KIND_TEXT[kind];
  const base = 'security.kind.' + kind + '.';
  if (!entry) {
    const known = (catalogue || []).find((k) => k.kind === kind);
    return {
      label: ['security.kind.unknown.label', (known && known.label) || kind],
      what: ['security.kind.unknown.what', (known && known.description) || ''],
      todo: null,
      stopped: 'na',
      link: null,
    };
  }
  return {
    label: [base + 'label', entry.label],
    what: [base + 'what', entry.what],
    todo: [base + 'todo', entry.todo],
    stopped: entry.stopped,
    link: entry.link ? LINKS[entry.link] : null,
  };
};

export const STOPPED_TEXT = {
  yes: ['security.stopped.yes', 'Stopped'],
  no: ['security.stopped.no', 'Let through'],
};

// ---- the status line ----

// Thresholds of the status line. 20 failed sign-ins an hour is SmartShield's
// own threshold for blocking an address.
export const ATTACK_SIGNINS_PER_HOUR = 20;
export const BUSY_INCIDENTS_PER_HOUR = 100;
// Incidents that mean someone has a password or a session: always worth a
// look, however few.
export const ATTENTION_KINDS = ['auth.mfa.locked', 'access.grant.denied'];

const num = (value) => (Number.isFinite(value) && value > 0 ? value : 0);
const groupTotal = (w, id) => num(w && w.groups && w.groups[id] && w.groups[id].total);
const groupAddresses = (w, id) => num(w && w.groups && w.groups[id] && w.groups[id].addresses);

// statusOf reads the status from an overview (its "recent" hour and its
// "day"); the rules are the first that applies:
//   attention  an ATTENTION_KINDS incident in the last 24 hours
//   attack     ATTACK_SIGNINS_PER_HOUR failed sign-ins in the last hour
//   busy       a ban or lock-out in the last hour, or
//              BUSY_INCIDENTS_PER_HOUR incidents in it
//   quiet      otherwise
// Returns { level, severity (an MUI Alert severity), key, text, params }.
export const statusOf = (overview) => {
  const recent = (overview && overview.recent) || {};
  const day = (overview && overview.day) || {};

  const kinds = day.kinds || {};
  const locked = num(kinds['auth.mfa.locked']);
  const granted = num(kinds['access.grant.denied']);
  if (locked > 0) {
    return { level: 'attention', severity: 'error', key: 'security.status.attentionLocked',
      text: 'Needs your attention: the two-factor check of an account was locked after wrong codes in the last 24 hours. Whoever tried knows its password.',
      params: { count: locked } };
  }
  if (granted > 0) {
    return { level: 'attention', severity: 'error', key: 'security.status.attentionGrant',
      text: 'Needs your attention: a signed-in user tried to hand out a permission they do not have, in the last 24 hours.',
      params: { count: granted } };
  }

  const signins = groupTotal(recent, 'login');
  if (signins >= ATTACK_SIGNINS_PER_HOUR) {
    const addresses = groupAddresses(recent, 'login');
    return { level: 'attack', severity: 'error',
      key: addresses === 1 ? 'security.status.attackOne' : 'security.status.attack',
      text: addresses === 1
        ? 'Under attack: {{count}} failed sign-ins from one address in the last hour.'
        : 'Under attack: {{count}} failed sign-ins from {{addresses}} addresses in the last hour.',
      params: { count: signins, addresses } };
  }

  const recentTotal = num(recent.total);
  if (groupTotal(recent, 'ban') + groupTotal(recent, 'lockout') > 0 || recentTotal >= BUSY_INCIDENTS_PER_HOUR) {
    const addresses = num(recent.addresses);
    return { level: 'busy', severity: 'warning',
      key: addresses === 1 ? 'security.status.busyOne' : 'security.status.busy',
      text: addresses === 1
        ? 'Busy: {{count}} blocked attempts from one address in the last hour. Cosmos stopped them.'
        : 'Busy: {{count}} blocked attempts from {{addresses}} addresses in the last hour. Cosmos stopped them.',
      params: { count: recentTotal, addresses } };
  }

  const dayTotal = num(day.total);
  if (dayTotal === 0) {
    return { level: 'quiet', severity: 'success', key: 'security.status.quietNone',
      text: 'Quiet: nothing suspicious in the last 24 hours.', params: {} };
  }
  return { level: 'quiet', severity: 'success',
    key: dayTotal === 1 ? 'security.status.quietOne' : 'security.status.quiet',
    text: dayTotal === 1
      ? 'Quiet: 1 blocked attempt in the last 24 hours.'
      : 'Quiet: {{count}} blocked attempts in the last 24 hours.',
    params: { count: dayTotal } };
};

// ---- periods ----

export const PERIODS = ['24h', '7d', '30d'];

export const PERIOD_TEXT = {
  '24h': ['security.period.24h', '24 hours'],
  '7d': ['security.period.7d', '7 days'],
  '30d': ['security.period.30d', '30 days'],
};

// changeText compares a counter with the period before.
export const changeText = (now, before, period) => {
  const a = num(now);
  const b = num(before);
  const p = PERIODS.includes(period) ? period : '24h';
  if (a === b) {
    return { direction: 'same', key: 'security.change.same.' + p, text: SAME[p], params: {} };
  }
  if (b === 0) {
    return { direction: 'up', key: 'security.change.new.' + p, text: NEW[p], params: {} };
  }
  if (a > b) {
    return { direction: 'up', key: 'security.change.more.' + p, text: MORE[p], params: { count: a - b } };
  }
  return { direction: 'down', key: 'security.change.fewer.' + p, text: FEWER[p], params: { count: b - a } };
};

const SAME = { '24h': 'Same as the 24 hours before', '7d': 'Same as the 7 days before', '30d': 'Same as the 30 days before' };
const NEW = { '24h': 'None in the 24 hours before', '7d': 'None in the 7 days before', '30d': 'None in the 30 days before' };
const MORE = { '24h': '{{count}} more than the 24 hours before', '7d': '{{count}} more than the 7 days before', '30d': '{{count}} more than the 30 days before' };
const FEWER = { '24h': '{{count}} fewer than the 24 hours before', '7d': '{{count}} fewer than the 7 days before', '30d': '{{count}} fewer than the 30 days before' };

// zoneOffsetMinutes is what the server needs to start days at local
// midnight: minutes east of UTC.
export const zoneOffsetMinutes = (date = new Date()) => -date.getTimezoneOffset();

// zoneName names the browser's time zone ("Europe/Berlin"), or its offset
// when the browser does not say.
export const zoneName = (timeZone) => {
  let zone = timeZone;
  if (!zone) {
    try {
      zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
    } catch (e) {
      zone = '';
    }
  }
  if (zone) return zone;
  const offset = zoneOffsetMinutes();
  const sign = offset < 0 ? '-' : '+';
  const abs = Math.abs(offset);
  return 'UTC' + sign + String(Math.floor(abs / 60)).padStart(2, '0') + ':' + String(abs % 60).padStart(2, '0');
};

// bucketLabel is the label of a bar of the timeline: the hour for bars of
// an hour, weekday and hour for longer bars, the date for days.
export const bucketLabel = (start, bucketSeconds, { locale, timeZone } = {}) => {
  const date = new Date(start);
  if (isNaN(date)) return '';
  const zone = timeZone ? { timeZone } : {};
  let options;
  if (bucketSeconds >= 86400) {
    options = { day: 'numeric', month: 'short', ...zone };
  } else if (bucketSeconds > 3600) {
    options = { weekday: 'short', hour: '2-digit', minute: '2-digit', ...zone };
  } else {
    options = { hour: '2-digit', minute: '2-digit', ...zone };
  }
  try {
    return new Intl.DateTimeFormat(locale, options).format(date);
  } catch (e) {
    return date.toISOString();
  }
};

// timelineSeries turns the overview's bars into ApexCharts series, one per
// group in GROUP_ORDER, only groups with at least one event (an empty
// period gives an empty list).
export const timelineSeries = (buckets) => {
  const list = Array.isArray(buckets) ? buckets : [];
  return GROUP_ORDER
    .map((id) => ({ id, data: list.map((b) => num(b && b.groups && b.groups[id])) }))
    .filter((serie) => serie.data.some((n) => n > 0));
};

// ---- the incident list ----

const sameSource = (a, b) => a.kind === b.kind && (a.ip || '') === (b.ip || '') && (a.actor || '') === (b.actor || '');

// groupRepeats folds events that follow each other in the list (newest
// first) and have the same kind, address and user into one row. count adds
// up what the events stand for (a summary row counts for many).
export const groupRepeats = (events) => {
  const rows = [];
  for (const event of Array.isArray(events) ? events : []) {
    if (!event || typeof event !== 'object') continue;
    const last = rows[rows.length - 1];
    const count = Number.isFinite(event.count) && event.count > 0 ? event.count : 1;
    if (last && sameSource(last.events[0], event)) {
      last.events.push(event);
      last.count += count;
      last.first = event.time;
      continue;
    }
    rows.push({ key: String(event.id), kind: event.kind, ip: event.ip || '', actor: event.actor || '',
      severity: event.severity, events: [event], count, first: event.time, last: event.time });
  }
  return rows;
};

// shorten cuts a text for a cell; the full text goes in a tooltip.
export const shorten = (value, max = 48) => {
  const text = value === undefined || value === null ? '' : String(value);
  const chars = Array.from(text);
  if (chars.length <= max) return { text, full: text, cut: false };
  return { text: chars.slice(0, Math.max(1, max - 1)).join('') + '…', full: text, cut: true };
};

// ---- addresses ----

// banOf finds an address in SmartShield's list (GET /api/shield/bans):
// null when it is not blocked now.
export const banOf = (bans, ip) => {
  if (!Array.isArray(bans) || !ip) return null;
  const entry = bans.find((b) => b && b.clientID === ip);
  if (!entry || !['perm', 'temp', 'strike'].includes(entry.status)) return null;
  return { status: entry.status, until: entry.status === 'perm' ? null : (entry.blockedUntil || null), clientID: entry.clientID };
};

export const BAN_TEXT = {
  perm: ['security.ban.perm', 'Banned until you lift it'],
  temp: ['security.ban.temp', 'Banned until {{until}}'],
  strike: ['security.ban.strike', 'Blocked until {{until}}'],
};

// The page's English texts that are not built from the tables above, for
// the translation file (see the test).
export const OTHER_TEXT = {};

// Every [key, English] pair this module can return, for the translation
// file.
export const allTexts = () => {
  const pairs = {};
  for (const id of Object.keys(GROUP_TEXT)) {
    pairs['security.group.' + id] = GROUP_TEXT[id][0];
    pairs['security.group.' + id + '.help'] = GROUP_TEXT[id][1];
  }
  for (const kind of Object.keys(KIND_TEXT)) {
    pairs['security.kind.' + kind + '.label'] = KIND_TEXT[kind].label;
    pairs['security.kind.' + kind + '.what'] = KIND_TEXT[kind].what;
    pairs['security.kind.' + kind + '.todo'] = KIND_TEXT[kind].todo;
  }
  for (const [key, text] of Object.values(STOPPED_TEXT)) pairs[key] = text;
  for (const [key, text] of Object.values(PERIOD_TEXT)) pairs[key] = text;
  for (const [key, text] of Object.values(BAN_TEXT)) pairs[key] = text;
  for (const link of Object.values(LINKS)) pairs[link.label[0]] = link.label[1];
  for (const p of PERIODS) {
    pairs['security.change.same.' + p] = SAME[p];
    pairs['security.change.new.' + p] = NEW[p];
    pairs['security.change.more.' + p] = MORE[p];
    pairs['security.change.fewer.' + p] = FEWER[p];
  }
  return pairs;
};

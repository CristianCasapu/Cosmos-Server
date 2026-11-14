// Fork addition: "Protection" for a URL (the Security tab of the route editor,
// pages/config/routes/forkRouteProtection.jsx): one choice of a few named
// levels instead of a wall of numbers. Pure and tested (routeProtection.test.mjs).
//
// How SmartShield judges a visitor (src/proxy/shield.go, budgetViolation): over
// one hour (proxy/shield_budget.go: budgetWindow), a visitor is struck when it
// goes over a limit *times the strictness* (utils/types.go: STRICT = 1,
// NORMAL = 2, LENIENT = 3); for HTTP the limit on simultaneous requests counts
// fifteen times on top. A strike blocks for an hour, repeated strikes for 4 h,
// 24 h, 72 h (proxy/shield_bans.go). A number left at 0 is the deployment
// profile's default (utils/types.go: GetSmartShieldProfile), which upstream
// wants routes to keep at 0 ("upgrading the license re-sizes every default
// route"); so the levels below never write a number of SmartShield, only the
// strictness. Administrators are never limited (PrivilegedGroups 0 = ADMIN).
//
// The levels, and where each value comes from:
//   standard  exactly what a new URL gets from upstream's dialogs
//             (exposeModal, newServiceForm; utils/routeGuide.js newAppRoute):
//             SmartShield on with every number 0, 10 000 requests a minute,
//             the empty-User-Agent filter on.
//   strict    upstream's STRICT level (half the default budget) and the limit
//             upstream puts on its own API: 500 requests a minute per address
//             (src/httpServer.go, SecureAPI).
//   relaxed   upstream's LENIENT level (1.5 times the default budget), the
//             rest as standard.
//   off       SmartShield, the per-minute limit and the filter off.
// Nothing here changes a default: a URL keeps what it has until a level is
// picked, and "standard" is what it had when it was created.

// The deployment profiles of utils/types.go (GetSmartShieldProfile), for the
// sentences only; routeProtection.test.mjs compares them with the Go source.
export const PROFILES = {
  free: { strictness: 2, requests: 18000, bytes: 200 * 1024 ** 3, simultaneous: 100 },
  pro: { strictness: 3, requests: 200000, bytes: 1024 ** 4, simultaneous: 500 },
};

export const LEVELS = [
  { value: 'strict', shield: true, strictness: 1, throttle: 500, bots: true },
  { value: 'standard', shield: true, strictness: 0, throttle: 10000, bots: true },
  { value: 'relaxed', shield: true, strictness: 3, throttle: 10000, bots: true },
  { value: 'off', shield: false, strictness: 0, throttle: 0, bots: false },
];

export const RECOMMENDED = 'standard';

const NUMBERS = ['_SmartShield_PerUserTimeBudget', '_SmartShield_PerUserRequestLimit', '_SmartShield_PerUserByteLimit',
  '_SmartShield_PerUserSimultaneous', '_SmartShield_MaxGlobalSimultaneous', '_SmartShield_PrivilegedGroups'];

const num = (value) => {
  const n = Number(value);
  return Number.isFinite(n) ? n : NaN;
};

// The level the form's values (routeSecurity.jsx's Formik names) amount to,
// or 'custom'.
export const levelOf = (values) => {
  const v = values || {};
  const throttle = num(v.ThrottlePerMinute || 0);
  const bots = Boolean(v.BlockCommonBots);
  if (!v._SmartShield_Enabled) {
    return throttle === 0 && !bots ? 'off' : 'custom';
  }
  if (NUMBERS.some((name) => num(v[name] || 0) !== 0)) return 'custom';
  // 2 is NORMAL, the free profile's default: written by upstream's own select
  const strictness = num(v._SmartShield_PolicyStrictness || 0) === 2 ? 0 : num(v._SmartShield_PolicyStrictness || 0);
  const level = LEVELS.find((l) => l.shield && l.strictness === strictness && l.throttle === throttle && l.bots === bots);
  return level ? level.value : 'custom';
};

// The values a level writes, in one go (one setValues: see CLAUDE.md on Formik).
export const applyLevel = (values, level) => {
  const l = LEVELS.find((x) => x.value === level);
  if (!l) return values;
  const next = { ...values, _SmartShield_Enabled: l.shield, ThrottlePerMinute: l.throttle, BlockCommonBots: l.bots };
  if (l.shield) {
    next._SmartShield_PolicyStrictness = l.strictness;
    NUMBERS.forEach((name) => { next[name] = 0; });
  }
  return next;
};

const round = (n) => (n >= 1000 ? Math.round(n / 1000) * 1000 : n);

const bytesText = (bytes) => {
  if (bytes >= 1024 ** 4) return Math.round(bytes / 1024 ** 4) + ' TB';
  return Math.round(bytes / 1024 ** 3) + ' GB';
};

// What a visitor may do at a level, for the sentence under the choice.
export const visitorBudget = (level, pro) => {
  const profile = pro ? PROFILES.pro : PROFILES.free;
  const l = LEVELS.find((x) => x.value === level);
  if (!l || !l.shield) return null;
  const factor = l.strictness || profile.strictness;
  return {
    requestsPerHour: round(profile.requests * factor),
    data: bytesText(profile.bytes * factor),
    simultaneous: profile.simultaneous * factor * 15,
    perMinute: l.throttle,
  };
};

// The sentence for each level: [key, English, variables].
export const levelText = (level, pro) => {
  const b = visitorBudget(level, pro);
  const fmt = (n) => Number(n).toLocaleString('en-US').replace(/,/g, ' ');
  switch (level) {
    case 'strict':
      return ['mgmt.urls.protection.strictHint', 'For an app few people use, or one open to the internet with a sign-in page. A visitor may make up to {{perHour}} requests and fetch {{data}} an hour, and at most {{perMinute}} requests a minute; beyond that they are slowed down, then blocked for an hour (longer if it happens again). A busy page (a photo gallery, a media library) can reach this.',
        { perHour: fmt(b.requestsPerHour), data: b.data, perMinute: fmt(b.perMinute) }];
    case 'standard':
      return ['mgmt.urls.protection.standardHint', 'Recommended, and what a new URL gets. A visitor may make up to {{perHour}} requests and fetch {{data}} an hour, and at most {{perMinute}} requests a minute; beyond that they are slowed down, then blocked for an hour (longer if it happens again). Ordinary use never comes near it.',
        { perHour: fmt(b.requestsPerHour), data: b.data, perMinute: fmt(b.perMinute) }];
    case 'relaxed':
      return ['mgmt.urls.protection.relaxedHint', 'For heavy use: file sync, backups, media streaming, or many people behind one address (an office, a mobile network). A visitor may make up to {{perHour}} requests and fetch {{data}} an hour, and at most {{perMinute}} requests a minute.',
        { perHour: fmt(b.requestsPerHour), data: b.data, perMinute: fmt(b.perMinute) }];
    case 'off':
      return ['mgmt.urls.protection.offHint', 'No limit at all: anyone who can reach the address can send as many requests as they like. Only for an app reached from your own network, or one with a limit of its own.'];
    default:
      return ['mgmt.urls.protection.customHint', 'These values match none of the levels: they were set by hand. They stay as they are until you pick a level; "Adjust the values myself" shows them.'];
  }
};

// One line saying what the URL holds, for the closed door.
export const summary = (values) => {
  const level = levelOf(values);
  if (level !== 'custom') return null;
  const v = values || {};
  const parts = [];
  parts.push(v._SmartShield_Enabled
    ? ['mgmt.urls.protection.sumShieldOn', 'SmartShield on, strictness {{s}}', { s: v._SmartShield_PolicyStrictness || 0 }]
    : ['mgmt.urls.protection.sumShieldOff', 'SmartShield off']);
  parts.push(num(v.ThrottlePerMinute || 0) > 0
    ? ['mgmt.urls.protection.sumThrottle', 'at most {{n}} requests a minute', { n: v.ThrottlePerMinute }]
    : ['mgmt.urls.protection.sumNoThrottle', 'no limit per minute']);
  return parts;
};

// The request timeout, shown in words: the field is in milliseconds.
export const timeoutText = (ms) => {
  const n = num(ms || 0);
  if (!(n > 0)) return ['mgmt.urls.protection.timeoutNone', 'No time limit: a request may take as long as it needs.'];
  if (n % 3600000 === 0) return ['mgmt.urls.protection.timeoutHours', 'A request is cut off after {{n}} hours.', { n: n / 3600000 }];
  if (n % 60000 === 0) return ['mgmt.urls.protection.timeoutMinutes', 'A request is cut off after {{n}} minutes.', { n: n / 60000 }];
  if (n < 1000) return ['mgmt.urls.protection.timeoutTiny', 'A request is cut off after {{n}} milliseconds: far too short for any page. The value is in milliseconds (1000 = one second).', { n }];
  return ['mgmt.urls.protection.timeoutSeconds', 'A request is cut off after {{n}} seconds.', { n: Math.round(n / 100) / 10 }];
};

// Bandwidth in bytes per second, in words.
export const bandwidthText = (bytesPerSecond) => {
  const n = num(bytesPerSecond || 0);
  if (!(n > 0)) return ['mgmt.urls.protection.bandwidthNone', 'No limit on speed.'];
  const mbit = (n * 8) / 1e6;
  return ['mgmt.urls.protection.bandwidthLimit', 'Each answer is sent at most at {{mbit}} Mbit/s ({{n}} bytes a second).', { mbit: mbit >= 10 ? Math.round(mbit) : Math.round(mbit * 10) / 10, n }];
};

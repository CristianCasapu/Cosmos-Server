// Fork addition: the plain logic of the Fail2ban panel
// (pages/security/fail2ban.jsx): which state to tell, how to say a length of
// time, and the checks the settings form makes before it sends anything.
// The server checks everything again (src/fail2ban); nothing here is relied
// on for safety. Nothing here imports the UI, so it can be exercised on its
// own.

// The jails the fork installs, in the order the server lists them, with what
// each one watches in plain words: [translation key, English text].
export const JAILS = {
  'cosmos-auth': {
    label: ['fail2ban.jail.auth', 'Logins'],
    watches: ['fail2ban.jail.auth.watches', 'Failed logins, wrong two-factor codes, wrong password at the confirmation prompt, bad invitation or reset links, API tokens that do not exist.'],
  },
  'cosmos-probe': {
    label: ['fail2ban.jail.probe', 'Probes'],
    watches: ['fail2ban.jail.probe.watches', 'Requests for paths only scanners ask for, such as /.env, /wp-login.php or /.git/config.'],
  },
  'cosmos-scan': {
    label: ['fail2ban.jail.scan', 'Scans'],
    watches: ['fail2ban.jail.scan.watches', 'Requests for host names this server does not serve (a scan of its address) and bots refused by a URL\'s bot filter.'],
  },
  'cosmos-abuse': {
    label: ['fail2ban.jail.abuse', 'Abuse'],
    watches: ['fail2ban.jail.abuse.watches', 'Strikes and bans given by SmartShield: an address SmartShield blocks inside Cosmos is then blocked on every port.'],
  },
  sshd: {
    label: ['fail2ban.jail.sshd', 'SSH'],
    watches: ['fail2ban.jail.sshd.watches', 'Failed logins to the server itself over SSH.'],
  },
  recidive: {
    label: ['fail2ban.jail.recidive', 'Repeat offenders'],
    watches: ['fail2ban.jail.recidive.watches', 'Addresses banned again and again by the other jails: they get a much longer ban.'],
  },
};

// stateOf says, from the server's answer, which of the plain states the
// panel shows:
//   loading       no answer yet
//   notInstalled  Fail2ban is not on the server
//   stopped       installed, not running
//   noJails       running, but none of the fork's jails is loaded
//   active        running with at least one of the fork's jails
export const stateOf = (status) => {
  if (!status) {
    return 'loading';
  }
  if (!status.installed) {
    return 'notInstalled';
  }
  if (!status.running) {
    return 'stopped';
  }
  return (status.jails || []).some((jail) => jail.loaded) ? 'active' : 'noJails';
};

// The units a length of time is entered in.
export const UNITS = { minutes: 60, hours: 3600, days: 86400 };

// splitDuration turns seconds into the largest unit that holds them exactly:
// 14400 -> { value: 4, unit: 'hours' }. Seconds that fit no unit are shown in
// minutes, rounded up, so that saving never makes a time shorter than it was.
export const splitDuration = (seconds) => {
  const total = Number(seconds);
  if (!Number.isFinite(total) || total <= 0) {
    return { value: 0, unit: 'minutes' };
  }
  for (const unit of ['days', 'hours', 'minutes']) {
    if (total % UNITS[unit] === 0) {
      return { value: total / UNITS[unit], unit };
    }
  }
  return { value: Math.ceil(total / 60), unit: 'minutes' };
};

// toSeconds is the opposite; NaN when the value is not a whole number.
export const toSeconds = (value, unit) => {
  const text = String(value === undefined || value === null ? '' : value).trim();
  if (!/^\d{1,9}$/.test(text) || !UNITS[unit]) {
    return NaN;
  }
  return Number(text) * UNITS[unit];
};

// durationWords says a length of time: [translation key, English text,
// variables], for t(key, text, variables).
export const durationWords = (seconds) => {
  const { value, unit } = splitDuration(seconds);
  const words = {
    minutes: [['fail2ban.duration.minute', '{{n}} minute'], ['fail2ban.duration.minutes', '{{n}} minutes']],
    hours: [['fail2ban.duration.hour', '{{n}} hour'], ['fail2ban.duration.hours', '{{n}} hours']],
    days: [['fail2ban.duration.day', '{{n}} day'], ['fail2ban.duration.days', '{{n}} days']],
  }[unit][value === 1 ? 0 : 1];
  return [words[0], words[1], { n: value }];
};

// timeLeft says how long a ban still lasts, from its end and the present
// (both in milliseconds): [key, English text, variables].
export const timeLeft = (until, now) => {
  if (until === null || until === undefined) {
    return ['fail2ban.left.never', 'until it is lifted', {}];
  }
  const seconds = Math.floor((Number(until) - Number(now)) / 1000);
  if (!Number.isFinite(seconds) || seconds <= 0) {
    return ['fail2ban.left.over', 'ending now', {}];
  }
  if (seconds < 60) {
    return ['fail2ban.left.soon', 'less than a minute', {}];
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return ['fail2ban.left.minutes', '{{minutes}} min', { minutes }];
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 48) {
    return ['fail2ban.left.hours', '{{hours}} h {{minutes}} min', { hours, minutes: minutes % 60 }];
  }
  return ['fail2ban.left.days', '{{days}} d {{hours}} h', { days: Math.floor(hours / 24), hours: hours % 24 }];
};

// --- addresses and networks -------------------------------------------------

const parseV4 = (text) => {
  const parts = text.split('.');
  if (parts.length !== 4 || !parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255 && (part === '0' || !part.startsWith('0')))) {
    return null;
  }
  return parts.reduce((sum, part) => (sum << 8n) | BigInt(part), 0n);
};

const parseV6 = (text) => {
  if (!/^[0-9a-fA-F:.]{2,45}$/.test(text) || text.includes(':::')) {
    return null;
  }
  let head = text;
  let tail = null;
  const gap = text.indexOf('::');
  if (gap !== -1) {
    if (text.indexOf('::', gap + 1) !== -1) {
      return null;
    }
    head = text.slice(0, gap);
    tail = text.slice(gap + 2);
  }
  const groups = (part) => {
    if (part === '') {
      return [];
    }
    const list = part.split(':');
    const last = list[list.length - 1];
    if (last.includes('.')) {
      const v4 = parseV4(last);
      if (v4 === null) {
        return null;
      }
      list.splice(list.length - 1, 1, (v4 >> 16n).toString(16), (v4 & 0xffffn).toString(16));
    }
    return list.every((group) => /^[0-9a-fA-F]{1,4}$/.test(group)) ? list : null;
  };
  const first = groups(head);
  const second = tail === null ? [] : groups(tail);
  if (first === null || second === null) {
    return null;
  }
  const missing = 8 - first.length - second.length;
  if (tail === null ? missing !== 0 : missing < 1) {
    return null;
  }
  return [...first, ...Array(tail === null ? 0 : missing).fill('0'), ...second]
    .reduce((sum, group) => (sum << 16n) | BigInt(parseInt(group, 16)), 0n);
};

// parseNetwork reads "203.0.113.7", "203.0.113.0/24", "2001:db8::/32":
// { version, value, bits } or null. An IPv4 address written inside IPv6
// (::ffff:203.0.113.7) counts as IPv4, like on the server.
export const parseNetwork = (text) => {
  const entry = String(text === undefined || text === null ? '' : text).trim();
  const parts = entry.split('/');
  if (parts.length > 2 || parts[0] === '') {
    return null;
  }
  let version = 4;
  let value = parseV4(parts[0]);
  if (value === null) {
    version = 6;
    value = parseV6(parts[0]);
    if (value === null) {
      return null;
    }
    if (parts.length === 1 && (value >> 32n) === 0xffffn) {
      version = 4;
      value &= 0xffffffffn;
    }
  }
  const size = version === 4 ? 32 : 128;
  let bits = size;
  if (parts.length === 2) {
    if (!/^\d{1,3}$/.test(parts[1]) || Number(parts[1]) > size) {
      return null;
    }
    bits = Number(parts[1]);
  }
  return { version, value, bits };
};

// entryProblem checks one entry of the never-ban list the way the server
// does and returns null or [translation key, English text].
export const entryProblem = (text) => {
  const network = parseNetwork(text);
  if (network === null) {
    return ['fail2ban.ignore.invalid', 'Enter an address (203.0.113.7) or a network (203.0.113.0/24).'];
  }
  if (network.bits === 0) {
    return ['fail2ban.ignore.everything', 'This covers every address: nothing would ever be banned.'];
  }
  return null;
};

// neverBanned reports whether the list covers the address.
export const neverBanned = (list, address) => {
  const wanted = parseNetwork(address);
  if (wanted === null || wanted.bits !== (wanted.version === 4 ? 32 : 128)) {
    return false;
  }
  return (list || []).some((entry) => {
    const network = parseNetwork(entry);
    if (network === null || network.version !== wanted.version) {
      return false;
    }
    const shift = BigInt((network.version === 4 ? 32 : 128) - network.bits);
    return (network.value >> shift) === (wanted.value >> shift);
  });
};

// --- the settings form --------------------------------------------------------

// formOf turns the server's settings into what the form holds: for each jail
// the number of tries and the two times split into a number and a unit.
export const formOf = (settings) => {
  const form = { jails: {}, ignoreIP: [...((settings && settings.ignoreIP) || [])] };
  Object.keys(JAILS).forEach((name) => {
    const thresholds = ((settings && settings.jails) || {})[name] || {};
    const find = splitDuration(thresholds.findTime);
    const ban = splitDuration(thresholds.banTime);
    form.jails[name] = {
      maxRetry: String(thresholds.maxRetry === undefined ? '' : thresholds.maxRetry),
      findValue: String(find.value), findUnit: find.unit,
      banValue: String(ban.value), banUnit: ban.unit,
    };
  });
  return form;
};

// settingsOf turns the form back into what the server takes, or says what is
// wrong: { settings } or { problems: { 'jail.field': [key, text, variables] } }.
export const settingsOf = (form, bounds) => {
  const problems = {};
  const settings = { jails: {}, ignoreIP: [] };
  const limits = bounds || {};
  Object.keys(JAILS).forEach((name) => {
    const entry = (form.jails || {})[name] || {};
    const maxRetry = /^\d{1,4}$/.test(String(entry.maxRetry).trim()) ? Number(entry.maxRetry) : NaN;
    const findTime = toSeconds(entry.findValue, entry.findUnit);
    const banTime = toSeconds(entry.banValue, entry.banUnit);
    if (!(maxRetry >= limits.minMaxRetry && maxRetry <= limits.maxMaxRetry)) {
      problems[name + '.maxRetry'] = ['fail2ban.problem.tries', 'Between {{min}} and {{max}}.', { min: limits.minMaxRetry, max: limits.maxMaxRetry }];
    }
    if (!(findTime >= limits.minFindTime && findTime <= limits.maxFindTime)) {
      problems[name + '.findTime'] = ['fail2ban.problem.findTime', 'Between {{min}} minutes and {{max}} days.', { min: limits.minFindTime / 60, max: limits.maxFindTime / 86400 }];
    }
    if (!(banTime >= limits.minBanTime && banTime <= limits.maxBanTime)) {
      problems[name + '.banTime'] = ['fail2ban.problem.banTime', 'Between {{min}} minutes and {{max}} days.', { min: limits.minBanTime / 60, max: limits.maxBanTime / 86400 }];
    }
    settings.jails[name] = { maxRetry, findTime, banTime };
  });
  const seen = new Set();
  (form.ignoreIP || []).forEach((entry) => {
    const text = String(entry).trim();
    if (entryProblem(text) !== null) {
      problems.ignoreIP = entryProblem(text);
    } else if (!seen.has(text)) {
      seen.add(text);
      settings.ignoreIP.push(text);
    }
  });
  if (settings.ignoreIP.length > limits.maxIgnoreIP) {
    problems.ignoreIP = ['fail2ban.problem.tooMany', 'At most {{max}} addresses or networks.', { max: limits.maxIgnoreIP }];
  }
  return Object.keys(problems).length > 0 ? { problems } : { settings };
};

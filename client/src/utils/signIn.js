// Fork addition (plan 45): the sign-in of new apps. Generated passwords that
// follow each app's rules (its recipe's `login.password`), the username and
// e-mail of Configuration > Sign-in details for new apps made into a form the
// app takes, a guess by the variables' names for an image Cosmos knows
// nothing about (the same rules as src/appsignin/guess.go, both tested
// against src/appsignin/testdata/guesses.json), and what the one review of
// every installation fills in and says ("who signs in with what").
//
// Pure: no network, no storage. Passwords are made from `random(n)`, which
// fills a Uint32Array(n) (crypto.getRandomValues in the browser), and live
// only in the objects returned.

export const LOWER = 'abcdefghijkmnopqrstuvwxyz'; // no l (read as 1 or I)
export const UPPER = 'ABCDEFGHJKLMNPQRSTUVWXYZ'; // no I, no O
export const DIGIT = '23456789'; // no 0, no 1
// The only symbols ever generated: the URL-unreserved ones, which need no
// encoding in a connection string and mean nothing to a shell, YAML or SQL.
export const SAFE_SYMBOLS = '-._~';

// For an app Cosmos has no rules for (a guess, a first-run page): letters
// and digits only, which every app takes.
export const PLAIN_RULE = { min: 16, max: 64, symbols: '', require: ['lower', 'upper', 'digit'] };

export const DEFAULT_LENGTH = 24;

// A random suffix that makes a new network's name one that cannot collide (review minor 2).
export const networkSuffix = (random) => {
  const draw = makeDraw(random);
  const set = 'abcdefghijkmnpqrstuvwxyz23456789';
  let out = '';
  for (let i = 0; i < 6; i += 1) out += set[draw(set.length)];
  return out;
};

// a stream of uniform random numbers below n (rejection sampling: no bias)
const makeDraw = (random) => {
  let pool = [];
  return (n) => {
    const limit = Math.floor(0x100000000 / n) * n;
    for (;;) {
      if (!pool.length) pool = Array.from(random(32));
      const value = pool.pop() >>> 0;
      if (value < limit) return value % n;
    }
  };
};

const symbolsOf = (rule) => [...new Set(String((rule && rule.symbols) || '').split(''))].filter((c) => SAFE_SYMBOLS.includes(c)).join('');

// The classes a rule allows, each with its characters.
export const classesOf = (rule) => {
  const out = [['lower', LOWER], ['upper', UPPER], ['digit', DIGIT]];
  const symbols = symbolsOf(rule);
  if (symbols) out.push(['symbol', symbols]);
  return out;
};

export const lengthFor = (rule, wanted) => {
  const min = Math.max(1, Number(rule && rule.min) || 8);
  const max = Math.min(64, Number(rule && rule.max) || 64);
  const want = Number(wanted) || DEFAULT_LENGTH;
  return Math.max(Math.min(want, max), min, classesOf(rule).length);
};

// A password for a rule: one character of each class the rule allows, the
// rest from all of them, shuffled; never a symbol first or last (some tools
// read a leading "-" as an option).
// The values Cosmos generated on this page (in memory only, gone with the
// page): the review tells them from a value typed or brought by a template.
const GENERATED = new Set();
export const isGenerated = (value) => !!value && GENERATED.has(String(value));
const generated = (value) => { GENERATED.add(value); return value; };

export const generatePassword = (rule, random, wanted) => {
  const draw = makeDraw(random);
  const classes = classesOf(rule);
  const all = classes.map(([, chars]) => chars).join('');
  const length = lengthFor(rule, wanted);
  const chars = classes.map(([, set]) => set[draw(set.length)]);
  while (chars.length < length) chars.push(all[draw(all.length)]);
  for (let i = chars.length - 1; i > 0; i -= 1) {
    const j = draw(i + 1);
    [chars[i], chars[j]] = [chars[j], chars[i]];
  }
  const symbols = symbolsOf(rule);
  const isSymbol = (c) => symbols.includes(c);
  [0, chars.length - 1].forEach((edge) => {
    if (!isSymbol(chars[edge])) return;
    const k = chars.findIndex((c, i) => i > 0 && i < chars.length - 1 && !isSymbol(c));
    if (k > 0) [chars[edge], chars[k]] = [chars[k], chars[edge]];
  });
  return generated(chars.join(''));
};

// Whether a value follows a rule (the tests, and a value typed by hand).
export const followsRule = (value, rule) => {
  const v = String(value || '');
  const min = Number(rule && rule.min) || 8;
  const max = Math.min(64, Number(rule && rule.max) || 64);
  if (v.length < min || v.length > max) return false;
  const symbols = symbolsOf(rule);
  if (![...v].every((c) => /[A-Za-z0-9]/.test(c) || symbols.includes(c))) return false;
  const has = { lower: /[a-z]/.test(v), upper: /[A-Z]/.test(v), digit: /[0-9]/.test(v), symbol: [...v].some((c) => symbols.includes(c)) };
  return ((rule && rule.require) || []).every((c) => has[c]);
};

// The Market's {Passwords.n}: upstream's alphabet and length (capital letters
// and digits, 24), which every template was written for, from the browser's
// cryptographic generator instead of Math.random.
export const marketPassword = (random) => {
  const draw = makeDraw(random);
  const set = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789';
  let out = '';
  for (let i = 0; i < 24; i += 1) out += set[draw(set.length)];
  return generated(out);
};

// ---- usernames and e-mail addresses ----------------------------------------------------

const safeRegExp = (pattern) => {
  try { return new RegExp(pattern); } catch (e) { return null; }
};

// The configured username in the form the app takes: { value, changed,
// reason } (reason: lower, chars, digit, long, reserved, empty).
export const userVariant = (user, rule) => {
  const original = String(user || '').trim();
  const max = Math.min(Number(rule && rule.max) || 64, 64);
  const pattern = rule ? safeRegExp(rule.pattern || '') : null;
  const reserved = (rule && rule.reserved) || [];
  const fits = (v) => !!v && v.length <= max && (!pattern || pattern.test(v)) && !reserved.includes(v) && !(rule && /^pg_/.test(v) && reserved.includes('pg_'));
  if (!original) return { value: '', changed: false, reason: 'empty' };
  if (fits(original)) return { value: original, changed: false, reason: '' };
  if (!rule) return { value: original.slice(0, max), changed: true, reason: 'long' };
  const lower = original.toLowerCase();
  if (fits(lower)) return { value: lower, changed: true, reason: 'lower' };
  let v = lower.normalize('NFKD').replace(/[̀-ͯ]/g, '').replace(/[^a-z0-9_]/g, '_').replace(/_+/g, '_').replace(/^_+|_+$/g, '');
  if (/^[0-9]/.test(v)) v = 'u' + v;
  if (v && fits(v)) return { value: v, changed: true, reason: 'chars' };
  if (v.length > max) {
    v = v.slice(0, max).replace(/_+$/, '');
    if (fits(v)) return { value: v, changed: true, reason: 'long' };
  }
  if (v) {
    const suffixed = (v.slice(0, Math.max(1, max - 4)) + '_app');
    if (fits(suffixed)) return { value: suffixed, changed: true, reason: 'reserved' };
  }
  return { value: '', changed: true, reason: 'refused' };
};

const SPECIAL_USE = /\.(local|localhost|test|invalid|example|internal|lan|home|arpa|onion)$/i;
const EMAIL = /^[^\s@<>()",;:]+@[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)+$/;

// The configured e-mail for an app: { value, ok, reason } — never invented:
// an address the app refuses is not used (the recipe's own proposal stays).
export const emailVariant = (email, rule) => {
  const v = String(email || '').trim();
  if (!v) return { value: '', ok: false, reason: 'empty' };
  if (!EMAIL.test(v) || v.length > 254) return { value: '', ok: false, reason: 'invalid' };
  if (rule && rule.refuseSpecialUse && SPECIAL_USE.test(v.split('@')[1])) return { value: '', ok: false, reason: 'specialUse' };
  return { value: v, ok: true, reason: '' };
};

// Values that stand for "nothing set yet" in templates and images.
const PLACEHOLDER_PASSWORDS = ['', 'change-me', 'changeme', 'change_me', 'changethis', 'password', 'pass', 'secret', 'admin', 'cosmos', 'your-password', 'yourpassword', '123456', 'please-change'];
const PLACEHOLDER_USERS = ['', 'admin', 'user', 'username', 'your-username', 'changeme'];
const PLACEHOLDER_EMAILS = ['', 'user@example.com', 'admin@example.com', 'you@example.com', 'your@email.com', 'admin@admin.com', 'user@domain.com', 'admin@domain.com'];

export const isPlaceholder = (value, kind) => {
  const v = String(value == null ? '' : value).trim().toLowerCase();
  if (kind === 'user') return PLACEHOLDER_USERS.includes(v);
  if (kind === 'email') return PLACEHOLDER_EMAILS.includes(v);
  return PLACEHOLDER_PASSWORDS.includes(v);
};

// ---- the guess by names (src/appsignin/guess.go) ------------------------------------------

const ENV_NAME = /^[A-Za-z_][A-Za-z0-9_.]{0,63}$/;
const USER_WORDS = ['USER', 'USERNAME', 'LOGIN'];
const EMAIL_WORDS = ['EMAIL', 'MAIL'];
const PASSWORD_WORDS = ['PASSWORD', 'PASS', 'PASSWD', 'PWD'];
const ADMIN_WORDS = ['ADMIN', 'ROOT', 'SUPERUSER', 'INITIAL', 'OWNER', 'WEBUI', 'UI', 'WEB', 'DEFAULT'];
const FOREIGN_WORDS = ['SMTP', 'MAILER', 'IMAP', 'POP', 'POP3', 'LDAP', 'OIDC', 'OAUTH', 'SSO', 'SAML',
  'S3', 'AWS', 'B2', 'MINIO', 'PROXY', 'FTP', 'SFTP', 'SSH', 'VPN',
  'DB', 'DATABASE', 'POSTGRES', 'POSTGRESQL', 'PG', 'MYSQL', 'MARIADB', 'MONGO', 'MONGODB',
  'JDBC', 'SQL', 'REDIS', 'VALKEY', 'RABBITMQ', 'AMQP', 'KAFKA', 'NATS', 'ELASTIC',
  'REPL', 'REPLICATION', 'REGISTRY', 'DOCKER', 'GITHUB', 'GITLAB', 'ACME', 'LETSENCRYPT',
  'CERT', 'SSL', 'TLS', 'SENDER', 'FROM', 'NOTIFY', 'NOTIFICATION', 'ALERT', 'CONTACT',
  'PLEX', 'TRAKT', 'API', 'TOKEN', 'KEY', 'SECRET', 'HASH', 'FILE', 'PUID', 'PGID'];

// { user, email, password } variable names, or null.
export const guessSignIn = (names) => {
  const groups = new Map();
  [...(names || [])].map(String).sort().forEach((name) => {
    if (!ENV_NAME.test(name) || /_FILE$/i.test(name)) return;
    const words = name.toUpperCase().split('_');
    const last = words[words.length - 1];
    const prefixWords = words.slice(0, -1);
    if (prefixWords.some((w) => FOREIGN_WORDS.includes(w))) return;
    const prefix = prefixWords.join('_');
    if (!groups.has(prefix)) groups.set(prefix, { user: '', email: '', password: '' });
    const g = groups.get(prefix);
    if (USER_WORDS.includes(last) && !g.user) g.user = name;
    else if (EMAIL_WORDS.includes(last) && !g.email) g.email = name;
    else if (PASSWORD_WORDS.includes(last) && !g.password) g.password = name;
  });
  let best = null;
  let bestScore = 0;
  groups.forEach((g, prefix) => {
    if (!g.password) return;
    const words = g.password.toUpperCase().split('_').slice(0, -1);
    let score = 0;
    if (g.user || g.email) score += 2;
    if (words.some((w) => ADMIN_WORDS.includes(w)) || prefix === '') score += 1;
    if (score > bestScore) { best = g; bestScore = score; }
  });
  return best ? { ...best } : null;
};

// ---- the Market's install forms ------------------------------------------------------------

// What a field of a Market template's form (cosmos-installer.form) starts
// with: the configured username or e-mail for a field named like one, a
// generated password for a password field, when the template's own value is
// empty or a placeholder ("admin", "change-me", "user@example.com"); the
// template's value otherwise. { value, filled: 'user'|'email'|'password'|'' }.
export const marketFieldDefault = (field, defaults, random, length) => {
  const initial = field && field.initialValue;
  const name = String((field && field.name) || '');
  const words = name.toUpperCase().split(/[_-]/);
  const last = words[words.length - 1];
  const foreign = words.slice(0, -1).some((w) => FOREIGN_WORDS.includes(w));
  const keep = { value: initial, filled: '' };
  if (!field || foreign) return keep;
  if (field.type === 'password') {
    // review minor 5: a template's password, placeholder or not, is known to everyone who reads the template
    if (typeof initial === 'string' || initial === undefined) return { value: generatePassword(PLAIN_RULE, random, length), filled: 'password' };
    return keep;
  }
  if ((field.type === 'text' || field.type === 'email' || !field.type) && defaults && (typeof initial === 'string' || initial === undefined)) {
    if ((USER_WORDS.includes(last) || /USER(NAME)?$/.test(last)) && isPlaceholder(initial, 'user') && defaults.user) return { value: defaults.user, filled: 'user' };
    if ((EMAIL_WORDS.includes(last) || /EMAIL$/.test(last)) && isPlaceholder(initial, 'email') && defaults.email) return { value: defaults.email, filled: 'email' };
  }
  return keep;
};

// The compose import's `${VAR}` lines: a variable the file leaves to the
// installer gets a generated value when it is a secret (shared by the file's
// services, so an app and its database get the same), the configured name or
// e-mail when it is an administrator's; "" otherwise. A secret of another
// service the app talks to (a mail relay, a directory, a bucket) is left empty.
const OUTSIDE_WORDS = ['SMTP', 'MAILER', 'IMAP', 'POP', 'POP3', 'LDAP', 'OIDC', 'OAUTH', 'SSO', 'SAML', 'S3', 'AWS', 'B2', 'MINIO', 'PROXY', 'FTP', 'SFTP', 'SSH', 'VPN', 'REGISTRY', 'DOCKER', 'GITHUB', 'GITLAB', 'ACME', 'PLEX', 'TRAKT', 'API', 'CLOUDFLARE', 'CF', 'TELEGRAM', 'DISCORD', 'SLACK', 'OPENAI'];
export const composeVarDefault = (name, defaults, random) => {
  const words = String(name || '').toUpperCase().split('_');
  const last = words[words.length - 1];
  const before = words.slice(0, -1);
  if (!ENV_NAME.test(String(name || '')) || before.some((w) => OUTSIDE_WORDS.includes(w)) || /_FILE$/i.test(String(name))) return '';
  if (PASSWORD_WORDS.includes(last) || ['SECRET', 'TOKEN'].includes(last) || (last === 'KEY' && before.some((w) => ['SECRET', 'APP', 'ENCRYPTION', 'JWT', 'SESSION', 'SIGNING'].includes(w)))) {
    return generatePassword(PLAIN_RULE, random, defaults && defaults.length);
  }
  const admin = before.some((w) => ADMIN_WORDS.includes(w));
  if (admin && defaults && USER_WORDS.includes(last) && defaults.user) return defaults.user;
  if (admin && defaults && EMAIL_WORDS.includes(last) && defaults.email) return defaults.email;
  return '';
};

// The value a form field should take now, or undefined to leave it as it is:
// once, when the field has no value yet; never over a value, typed or not
// (the form is built only after the defaults were asked for: docker-compose.jsx).
export const marketFormValue = (field, current, defaults, random) => {
  if (current !== undefined) return undefined;
  const d = marketFieldDefault(field, defaults, random, defaults && defaults.length);
  return d.filled ? d.value : undefined;
};

// ---- the label the review sets (src/appsignin/label.go) -----------------------------------

export const LABEL = 'cosmos-fork-signin';
export const MANAGER_LABEL = 'cosmos-fork-manager-of';

export const signInLabel = ({ where, user, email, password, database, first, note, from }) => {
  const parts = [];
  if (where) parts.push(`where=${where}`);
  [['user', user], ['email', email], ['password', password], ['database', database]].forEach(([k, v]) => {
    if (v && ENV_NAME.test(v)) parts.push(`${k}=${v}`);
  });
  if (first && /^[^;=\s\x00-\x1f\x7f]{1,64}$/.test(first)) parts.push(`first=${first}`);
  if (note) parts.push(`note=${note}`);
  if (from) parts.push(`from=${from}`);
  return parts.join(';');
};

// ---- the review ---------------------------------------------------------------------------

const envKey = (line) => String(line).split('=')[0];
const envValue = (line) => { const s = String(line); const i = s.indexOf('='); return i < 0 ? '' : s.slice(i + 1); };

// environment as a list of NAME=value, whatever form the definition used
const envList = (environment) => {
  if (Array.isArray(environment)) return environment.map(String);
  if (environment && typeof environment === 'object') return Object.entries(environment).map(([k, v]) => `${k}=${v == null ? '' : v}`);
  return [];
};

const setEnv = (list, name, value) => {
  const i = list.findIndex((line) => envKey(line) === name);
  if (i >= 0) list[i] = `${name}=${value}`; else list.push(`${name}=${value}`);
};

// What the review fills in and says about the sign-in of each service.
//   definition: { services: { key: service } }
//   plan:       the install plan's answer ({ signIn: { image: { name, login, firstRun } } })
//   defaults:   { user, email, length } (GET /api/app-sign-in/defaults), or null
//   cache:      a Map kept by the caller, so that a value is generated once
// Returns { definition, lines, proposed } — lines for "Who signs in with what",
// proposed for "Filled in by Cosmos" (names only).
//   options.replace: a Set of "serviceKey/VARIABLE" the administrator asked to
//                    replace with a generated password (a value Cosmos did not make)
// The review fills only what is EMPTY (review minor 5): a value already there
// was typed by the administrator or brought by the image or the template; a
// password Cosmos did not generate is said ("preset") with the offer to replace it.
export const proposeSignIn = (definition, plan, defaults, cache, random, options = {}) => {
  const replace = options.replace || new Set();
  const services = (definition && definition.services) || {};
  const out = { ...definition, services: {} };
  const lines = [];
  const proposed = [];
  const length = (defaults && defaults.length) || DEFAULT_LENGTH;
  const remember = (key, make) => {
    if (!cache.has(key)) cache.set(key, make());
    return cache.get(key);
  };
  Object.entries(services).forEach(([key, original]) => {
    const service = { ...original };
    out.services[key] = service;
    // review F1: the sign-in label is the review's own, never one a file or a template brought
    if (service.labels && service.labels[LABEL] !== undefined) {
      service.labels = { ...service.labels };
      delete service.labels[LABEL];
    }
    const name = service.container_name || key;
    const image = String(service.image || '').trim();
    const info = (plan && plan.signIn && plan.signIn[image]) || null;
    const env = envList(service.environment);
    const values = Object.fromEntries(env.map((line) => [envKey(line), envValue(line)]));
    let changed = false;
    const fill = (variable, kind, make, force = false) => {
      if (!variable || !(force || String(values[variable] == null ? '' : values[variable]).trim() === '')) return false;
      const value = make();
      if (!value) return false;
      setEnv(env, variable, value);
      values[variable] = value;
      changed = true;
      proposed.push({ code: 'signIn.' + kind, service: name, params: { name: variable } });
      return true;
    };
    const login = info && info.login;
    if (login && login.via === 'database') {
      lines.push({ service: name, app: info.name || '', kind: 'via', source: 'recipe' });
    } else if (login && (login.accounts || []).length) {
      (login.accounts || []).slice(0, 4).forEach((account) => {
        const line = { service: name, app: info.name || '', kind: 'account', role: account.role, where: login.where, source: 'recipe', rule: login.password || PLAIN_RULE };
        if (account.defaults && defaults) {
          const variant = userVariant(defaults.user, login.user);
          if (account.user && fill(account.user, 'user', () => variant.value)) {
            line.userChanged = variant.changed ? variant.reason : '';
            line.userFrom = defaults.user;
          }
          const mail = emailVariant(defaults.email, login.email);
          if (account.email) {
            if (mail.ok) fill(account.email, 'email', () => mail.value);
            else if (defaults.email) line.emailRefused = mail.reason;
          }
        }
        if (account.password) {
          const asked = replace.has(`${key}/${account.password}`) && !isGenerated(values[account.password]);
          fill(account.password, 'password', () => remember(`${key}/${account.password}${asked ? '#replaced' : ''}`, () => generatePassword(login.password || PLAIN_RULE, random, length)), asked);
          line.generated = isGenerated(values[account.password]);
          line.preset = !line.generated && !!values[account.password];
          line.key = key;
          line.passwordVar = account.password;
          if (line.generated) line.value = values[account.password];
        }
        line.user = account.userFixed || (account.user && values[account.user]) || account.userUnset || '';
        line.email = (account.email && values[account.email]) || '';
        line.database = (account.database && values[account.database]) || '';
        if (line.user || line.email || line.passwordVar) lines.push(line);
      });
    } else if (info && info.firstRun) {
      const first = info.firstRun;
      const user = first.user || (defaults ? userVariant(defaults.user, { pattern: '^[A-Za-z0-9_.-]+$', max: 32 }).value : '');
      const password = first.note === 'logPassword' ? '' : remember(`${key}/#first`, () => generatePassword(PLAIN_RULE, random, length));
      lines.push({ service: name, app: info.name || first.name || '', kind: 'firstRun', note: first.note, user, value: password, source: 'firstRun' });
      service.labels = { ...(service.labels || {}), [LABEL]: signInLabel({ where: 'web', first: user, note: first.note, from: 'review' }) };
    } else {
      const guess = guessSignIn(Object.keys(values));
      if (guess) {
        if (defaults) {
          if (guess.user) fill(guess.user, 'user', () => userVariant(defaults.user, null).value);
          if (guess.email) { const mail = emailVariant(defaults.email, null); if (mail.ok) fill(guess.email, 'email', () => mail.value); }
        }
        const asked = replace.has(`${key}/${guess.password}`) && !isGenerated(values[guess.password]);
        fill(guess.password, 'password', () => remember(`${key}/${guess.password}${asked ? '#replaced' : ''}`, () => generatePassword(PLAIN_RULE, random, length)), asked);
        const isOurs = isGenerated(values[guess.password]);
        lines.push({ service: name, key, kind: 'guess', rule: PLAIN_RULE, user: (guess.user && values[guess.user]) || '', email: (guess.email && values[guess.email]) || '', passwordVar: guess.password, generated: isOurs, preset: !isOurs && !!values[guess.password], value: isOurs ? values[guess.password] : '', source: 'guess' });
        service.labels = { ...(service.labels || {}), [LABEL]: signInLabel({ where: 'web', user: guess.user, email: guess.email, password: guess.password, from: 'guess' }) };
      }
    }
    if (changed) service.environment = env;
  });
  return { definition: out, lines, proposed };
};

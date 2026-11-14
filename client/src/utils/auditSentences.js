// Fork addition: one sentence per row of the security record, for the user
// audit panel (pages/securityAudit). Pure: no React, no i18next; the caller
// passes its t(key, fallback). Values are put into the template here, in one
// pass, so a value that looks like a placeholder stays text; the result is
// plain text and must be rendered as text.

// Every kind the server records (src/utils/fork_security.go) has an entry;
// auditSentences.test.mjs fails when one is missing. An entry is the English
// template and, optionally:
//   ip:      append " from <address>" when the row has one
//   pick:    a function choosing a variant from the row
//   variants: the templates of the variants
const SENTENCES = {
  'auth.login.success': {
    text: '{{actor}} logged in', ip: true,
    pick: (e) => (detail(e, 'mfa') === 'pending' ? 'pending' : ''),
    variants: { pending: '{{actor}} logged in{{from}}; the two-factor code was still to come' },
  },
  'auth.login.failed': {
    text: 'a failed login for {{target}}', ip: true,
    pick: (e) => (isSummary(e) ? 'summary' : detail(e, 'reason')),
    variants: {
      wrong_password: 'a wrong password for {{target}}',
      not_registered: 'a login for {{target}}, who has not set a password yet',
      unknown_user: 'a login for an unknown name, {{target}}',
      bad_request: 'a malformed login for {{target}}',
      summary: '{{count}} more failed logins{{from}}, summed up without the names tried',
      noname: 'a failed login',
    },
  },
  'auth.logout': { text: '{{actor}} logged out', ip: true },
  'auth.mfa.success': { text: '{{actor}} gave the right two-factor code', ip: true },
  'auth.mfa.failed': {
    text: '{{actor}} gave a wrong two-factor code', ip: true,
    pick: (e) => (detail(e, 'reason') === 'locked' ? 'locked' : ''),
    variants: { locked: '{{actor}} tried a two-factor code while the check was locked' },
  },
  'auth.mfa.locked': { text: 'the two-factor check of {{target}} was locked after too many wrong codes', ip: true },
  'auth.mfa.setup': { text: '{{actor}} was given a new two-factor secret', ip: true },
  'auth.sudo.success': { text: '{{actor}} confirmed the password to make changes', ip: true },
  'auth.sudo.failed': { text: '{{actor}} gave a wrong password at the confirmation prompt', ip: true },
  'auth.session.forged': { text: 'a forged session was presented', ip: true },
  'auth.session.rejected': { text: 'a session that is no longer valid was presented', ip: true },
  'auth.reset.requested': { text: 'a password reset was asked for {{target}}', ip: true },
  'auth.reset.rejected': { text: 'a password reset was asked for an unknown account, {{target}}', ip: true },
  'auth.password.changed': { text: '{{actor}} set a new password through a reset link', ip: true },
  'auth.register.failed': {
    text: 'a wrong invitation or reset link for {{target}}', ip: true,
    pick: (e) => (detail(e, 'reason') === 'expired' ? 'expired' : ''),
    variants: { expired: 'an expired invitation or reset link for {{target}}' },
  },

  'user.created': {
    text: '{{actor}} created the account {{target}}', ip: true,
    pick: (e) => (detail(e, 'role') ? 'role' : ''),
    variants: { role: '{{actor}} created the account {{target}} (role: {{role}})' },
  },
  'user.deleted': {
    text: '{{actor}} deleted the account {{target}}', ip: true,
    pick: (e) => (detail(e, 'self') === 'yes' ? 'self' : ''),
    variants: { self: '{{actor}} deleted their own account' },
  },
  'user.edited': { text: '{{actor}} changed the email of {{target}}', ip: true },
  'user.role.changed': { text: '{{actor}} changed the role of {{target}} to {{role}}', ip: true },
  'user.invite.created': { text: '{{actor}} made an invitation link for {{target}}', ip: true },
  'user.invite.used': { text: '{{actor}} accepted the invitation and set a password', ip: true },
  'user.reset.created': { text: '{{actor}} made a password reset link for {{target}}', ip: true },
  'user.mfa.reset': { text: '{{actor}} reset the two-factor of {{target}}', ip: true },

  'token.created': { text: '{{actor}} created the API token {{target}}', ip: true },
  'token.updated': { text: '{{actor}} changed the API token {{target}}', ip: true },
  'token.deleted': { text: '{{actor}} deleted the API token {{target}}', ip: true },
  'token.invalid': { text: 'an API token that does not exist was used', ip: true },
  'token.expired': { text: 'the expired API token {{target}} was used', ip: true },
  'token.ip.denied': { text: 'the API token {{target}} was used from an address it is not allowed from', ip: true },

  'access.denied': {
    text: '{{actor}} was refused: no permission', ip: true,
    pick: (e) => (detail(e, 'permission') ? 'permission' : ''),
    variants: { permission: '{{actor}} was refused: no permission "{{permission}}"' },
  },
  'access.grant.denied': {
    text: '{{actor}} was refused to hand out a permission they do not have', ip: true,
    pick: (e) => (e.target ? 'target' : ''),
    variants: { target: '{{actor}} was refused to hand out a permission they do not have ({{what}} {{target}})' },
  },
  'access.ip.denied': { text: 'a request was refused by an address restriction', ip: true },
  'access.local.denied': { text: 'a local-only address was asked for from outside', ip: true },
  'access.origin.denied': {
    text: 'an address served through Cloudflare was asked for directly', ip: true,
    // from Cloudflare's own addresses, without the certificate that shows its proxy made the request
    pick: (e) => (['no_certificate', 'wrong_certificate', 'expired_certificate', 'not_https'].includes(detail(e, 'reason')) ? detail(e, 'reason') : ''),
    variants: {
      no_certificate: 'a request for an address served through Cloudflare came from Cloudflare\'s addresses without a certificate',
      wrong_certificate: 'a request for an address served through Cloudflare came from Cloudflare\'s addresses with another certificate than the expected one',
      expired_certificate: 'a request for an address served through Cloudflare came from Cloudflare\'s addresses with an expired certificate',
      not_https: 'a request for an address served through Cloudflare came from Cloudflare\'s addresses over plain HTTP',
    },
  },
  'access.foreign.denied': {
    text: 'a request to the panel was refused: the panel\'s own pages did not start it', ip: true,
    pick: (e) => detail(e, 'reason'),
    variants: {
      another_site: 'a request to the panel that another website started was refused',
      same_domain: 'a request to the panel that another application of the same domain started was refused',
      opened_by_hand: 'an address of the panel\'s API was opened by hand (address bar, bookmark, another program) and refused',
      another_origin: 'a request to the panel that names another site as its origin was refused',
      application_port: 'a request to the panel\'s API on the port of an application was refused',
    },
  },

  'shield.blocked': { text: 'a request was blocked by SmartShield', ip: true },
  'shield.strike': { text: 'SmartShield blocked a client for an hour', ip: true },
  'shield.ban.temporary': { text: 'SmartShield banned a client for a while', ip: true },
  'shield.ban.permanent': { text: 'SmartShield banned a client until it is unbanned', ip: true },
  'shield.unban': { text: '{{actor}} unbanned {{target}}', ip: true },
  'firewall.ban': { text: 'Fail2ban banned a client at the firewall' },
  'firewall.unban': { text: '{{actor}} lifted the Fail2ban ban of {{target}}', ip: true },
  'firewall.settings.saved': { text: '{{actor}} changed the Fail2ban settings', ip: true },
  'upnp.port.opened': { text: '{{actor}} asked the router to open port {{port}} of {{target}} to the internet', ip: true },
  'upnp.port.closed': {
    text: '{{actor}} closed port {{port}} of {{target}} on the router', ip: true,
    pick: (e) => (String(e.target || '').startsWith('Cosmos ') ? 'own' : ''),
    variants: { own: '{{actor}} closed the {{target}} port on the router' },
  },
  'upnp.mapping.created': {
    text: 'the router now forwards port {{external}} ({{protocol}}) to {{target}}',
    pick: (e) => (detail(e, 'wanted') ? 'moved' : ''),
    variants: { moved: 'the router now forwards port {{external}} ({{protocol}}) to {{target}}: port {{wanted}} was taken' },
  },
  'upnp.mapping.removed': {
    text: 'the router no longer forwards port {{external}} ({{protocol}}) to {{target}}',
    pick: (e) => detail(e, 'reason'),
    variants: {
      off: 'the router no longer forwards port {{external}} ({{protocol}}) to {{target}}: it was switched off',
      stopped: 'the router no longer forwards port {{external}} ({{protocol}}) to {{target}}: the container stopped',
      gone: 'the router no longer forwards port {{external}} ({{protocol}}) to {{target}}: the container is gone',
      notPublished: 'the router no longer forwards port {{external}} ({{protocol}}) to {{target}}: the container no longer publishes the port',
      changed: 'the router no longer forwards port {{external}} ({{protocol}}) to {{target}}: the port it led to changed',
      moved: 'the router no longer forwards port {{external}} ({{protocol}}) to {{target}}: another external port is used now',
      lost: 'port {{external}} ({{protocol}}) on the router no longer leads to {{target}}: something else has it now',
      leftover: 'a port mapping left by an earlier run was removed from the router: port {{external}} ({{protocol}}), {{target}}',
    },
  },
  'shield.geo.blocked': { text: 'a request was blocked by the country filter', ip: true },
  'shield.bot.blocked': { text: 'a bot was blocked', ip: true },
  'shield.hostname.unknown': { text: 'a request named a hostname this server does not serve', ip: true },
  'shield.ratelimited': { text: 'too many requests: a client was slowed down', ip: true },
  'scan.probe': { text: 'a probe for a vulnerable path, {{path}}', ip: true },

  'config.saved': {
    text: '{{actor}} changed the settings (sections: {{sections}})', ip: true,
    pick: (e) => (!detail(e, 'sections') || detail(e, 'sections') === '(none)' ? 'nochange' : ''),
    variants: { nochange: '{{actor}} saved the settings without changing anything' },
  },
  'route.created': { text: '{{actor}} created the URL {{target}}', ip: true },
  'route.updated': { text: '{{actor}} changed the URL {{target}}', ip: true },
  'route.deleted': { text: '{{actor}} deleted the URL {{target}}', ip: true },
  'container.action': {
    text: '{{actor}} ran "{{action}}" on the container {{target}}', ip: true,
    pick: (e) => detail(e, 'action'),
    variants: {
      start: '{{actor}} started the container {{target}}',
      stop: '{{actor}} stopped the container {{target}}',
      restart: '{{actor}} restarted the container {{target}}',
      kill: '{{actor}} killed the container {{target}}',
      pause: '{{actor}} paused the container {{target}}',
      unpause: '{{actor}} resumed the container {{target}}',
      recreate: '{{actor}} recreated the container {{target}}',
      update: '{{actor}} updated the container {{target}}',
    },
  },
  'container.deleted': { text: '{{actor}} deleted the container {{target}}', ip: true },
  'container.created': { text: '{{actor}} created containers from a compose description', ip: true },
  'container.edited': { text: '{{actor}} changed the settings of the container {{target}}', ip: true },
  'container.terminal.opened': {
    text: '{{actor}} opened a terminal in the container {{target}}', ip: true,
    pick: (e) => detail(e, 'mode'),
    variants: {
      new: '{{actor}} opened a shell in the container {{target}}',
      attach: '{{actor}} attached to the input and output of the container {{target}}',
      attach_readonly: '{{actor}} watched the output of the container {{target}}',
    },
  },
  'host.terminal.opened': { text: '{{actor}} opened a terminal on the server itself, as root', ip: true },
  'server.update.started': {
    text: '{{actor}} started a server update', ip: true,
    pick: (e) => (e.target ? 'version' : ''),
    variants: { version: '{{actor}} started a server update to {{target}}' },
  },
  'server.update.rejected': { text: 'an update was refused: the release could not be verified' },
  'server.restart': {
    text: '{{actor}} restarted Cosmos', ip: true,
    pick: (e) => (detail(e, 'scope') === 'host' ? 'host' : ''),
    variants: { host: '{{actor}} restarted the machine' },
  },
  'snapshot.restore.started': { text: '{{actor}} started restoring {{target}} from the snapshot {{snapshot}}', ip: true },
  'api.change': { text: '{{actor}} sent {{method}} {{path}}', ip: true },

  'recorder.dropped': { text: '{{count}} events could not be stored (the database did not answer)' },
};

// The parts added around a sentence.
const PARTS = {
  someone: 'someone',
  unnamed: '(no name)',
  from: ' from {{ip}}',
  token: ', with the API token {{token}}',
  times: ' ({{count}} times)',
  failed: ': it failed',
  denied: ': refused',
  blocked: ': blocked',
};

function detail(event, key) {
  const details = event && event.details;
  return details && typeof details[key] === 'string' ? details[key] : '';
}

function isSummary(event) {
  return detail(event, 'summary') === '1';
}

const identity = (key, fallback) => fallback;

// fill puts the values into a template in one pass: a value is never
// read as a template itself.
export function fill(template, values) {
  return String(template).replace(/\{\{(\w+)\}\}/g, (_, name) => {
    const value = values[name];
    return value === undefined || value === null ? '' : String(value);
  });
}

function sentenceKey(kind, variant) {
  return 'securityAudit.sentence.' + kind + (variant ? '.' + variant : '');
}

// knownKinds lists the kinds with a sentence.
export function knownKinds() {
  return Object.keys(SENTENCES);
}

// templates lists every [i18n key, English text] the sentences use, for
// translation.json.
export function templates() {
  const all = [];
  for (const [kind, entry] of Object.entries(SENTENCES)) {
    all.push([sentenceKey(kind, ''), entry.text]);
    for (const [variant, text] of Object.entries(entry.variants || {})) {
      all.push([sentenceKey(kind, variant), text]);
    }
  }
  for (const [part, text] of Object.entries(PARTS)) {
    all.push(['securityAudit.part.' + part, text]);
  }
  return all;
}

// auditSentence says in words what a row of the record means:
// "admin changed the settings (sections: HTTPConfig)".
export function auditSentence(event, t = identity) {
  if (!event || typeof event !== 'object') return '';
  const part = (name) => t('securityAudit.part.' + name, PARTS[name]);
  const entry = SENTENCES[event.kind];
  const values = {
    ...(event.details && typeof event.details === 'object' ? event.details : {}),
    actor: event.actor || part('someone'),
    target: event.target || part('unnamed'),
    ip: event.ip || '',
    path: event.path || '',
    method: event.method || '',
    count: event.count || 1,
  };

  let sentence;
  let countSaid = false;
  if (!entry) {
    // a kind this page does not know yet: say what the server calls it
    sentence = (event.actor ? event.actor + ': ' : '') + String(event.kind || '');
  } else {
    let variant = entry.pick ? entry.pick(event) : '';
    if (event.kind === 'auth.login.failed' && !variant && !event.target) variant = 'noname';
    if (variant && !(entry.variants && entry.variants[variant])) variant = '';
    const template = t(sentenceKey(event.kind, variant), variant ? entry.variants[variant] : entry.text);
    values.from = entry.ip && event.ip ? fill(part('from'), values) : '';
    sentence = fill(template, values);
    if (values.from && !template.includes('{{from}}')) sentence += values.from;
    countSaid = template.includes('{{count}}');
  }
  if (detail(event, 'token')) sentence += fill(part('token'), values);

  const kindOutcome = entry ? defaultOutcome(event.kind) : '';
  if (event.outcome && event.outcome !== 'ok' && event.outcome !== kindOutcome && PARTS[event.outcome]) {
    sentence += part(event.outcome);
  }
  if ((event.count || 1) > 1 && !countSaid) sentence += fill(part('times'), values);
  return sentence;
}

// The outcome a kind has by itself (the catalogue's): a "failed login" that
// failed needs no ": it failed".
const FAILED_KINDS = ['auth.login.failed', 'auth.mfa.failed', 'auth.sudo.failed', 'auth.reset.rejected', 'auth.register.failed', 'recorder.dropped'];
const DENIED_KINDS = ['auth.session.forged', 'auth.session.rejected', 'token.invalid', 'token.expired', 'token.ip.denied', 'access.denied', 'access.grant.denied', 'access.ip.denied', 'access.local.denied', 'access.origin.denied', 'access.foreign.denied'];

function defaultOutcome(kind) {
  if (FAILED_KINDS.includes(kind)) return 'failed';
  if (DENIED_KINDS.includes(kind)) return 'denied';
  if (kind.startsWith('shield.') && kind !== 'shield.unban') return 'blocked';
  if (kind === 'scan.probe' || kind === 'auth.mfa.locked' || kind === 'firewall.ban') return 'blocked';
  return 'ok';
}

// isProblem says whether a row is a failure or a refusal, to mark it.
export function isProblem(event) {
  return !!event && !!event.outcome && event.outcome !== 'ok';
}

// The kinds a trail row can have, grouped for the filter. Each group is a
// list of kinds or families ("auth.login.*") as the API takes them.
export const KIND_GROUPS = [
  { id: 'logins', label: 'Logins and logouts', kinds: ['auth.login.*', 'auth.logout'] },
  { id: 'credentials', label: 'Passwords and two-factor', kinds: ['auth.mfa.*', 'auth.sudo.*', 'auth.password.*', 'auth.reset.*', 'auth.register.*', 'user.reset.created', 'user.mfa.reset'] },
  { id: 'accounts', label: 'Accounts', kinds: ['user.*'] },
  { id: 'tokens', label: 'API tokens', kinds: ['token.*'] },
  { id: 'settings', label: 'Settings and URLs', kinds: ['config.*', 'route.*'] },
  { id: 'containers', label: 'Containers and terminals', kinds: ['container.*', 'host.*', 'snapshot.*'] },
  { id: 'server', label: 'Server and SmartShield', kinds: ['server.*', 'shield.unban', 'firewall.unban', 'firewall.settings.saved', 'upnp.*'] },
  { id: 'refused', label: 'Permissions refused', kinds: ['access.*'] },
  { id: 'other', label: 'Other changes', kinds: ['api.change'] },
];

// kindInGroup says whether a kind is selected by a group's entries.
export function kindInGroup(kind, group) {
  return group.kinds.some((entry) => (entry.endsWith('.*') ? kind.startsWith(entry.slice(0, -1)) : kind === entry));
}

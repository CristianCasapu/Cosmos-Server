// Fork addition: what the screens say about Cloudflare (the guided set-up in
// Configuration > Domains, the "through Cloudflare / directly" switch of an
// address). The server (src/cloudflare) answers with codes; the sentences,
// and the few decisions the forms make, are here. Nothing in this file
// imports the UI, so it can be exercised on its own.

// Each entry is [translation key, English text]. {{…}} are filled in by the caller.

// What kind of credential a pasted text is, from its form alone. Mirrors
// TokenFormat in src/cloudflare/token.go, which decides.
export const tokenFormat = (text) => {
  const token = String(text || '').trim();
  if (token.startsWith('v1.0-')) return 'origin_ca_key';
  if (token.length < 20 || token.length > 200 || !/^[A-Za-z0-9_-]+$/.test(token)) return 'malformed';
  if (token.startsWith('cfk_') || /^[0-9a-f]{37,45}$/.test(token)) return 'global_key';
  if (token.startsWith('cfoat_')) return 'oauth';
  return 'token';
};

// The answer of the token check, by its status: [severity, key, text].
export const TOKEN_STATUS = {
  ok: ['success', 'mgmt.cloudflare.token.ok', 'Cloudflare accepts this token.'],
  expired: ['error', 'mgmt.cloudflare.token.expired', 'This token has expired. Create a new one at Cloudflare (My Profile > API Tokens).'],
  disabled: ['error', 'mgmt.cloudflare.token.disabled', 'This token is switched off at Cloudflare. Switch it on there (My Profile > API Tokens), or create a new one.'],
  invalid: ['error', 'mgmt.cloudflare.token.invalid', 'Cloudflare does not know this token. Check that all of it was copied; a token is shown only once, when it is created.'],
  malformed: ['error', 'mgmt.cloudflare.token.malformed', 'This is not an API token: one is a single line of letters, digits, "_" and "-", without spaces.'],
  global_key: ['error', 'mgmt.cloudflare.token.globalKey', 'This is the Global API Key of your Cloudflare account: it can change everything in the account, and Cosmos does not accept it. Create an API token instead (My Profile > API Tokens > Create Token > "Edit zone DNS"), limited to this one domain.'],
  origin_ca_key: ['error', 'mgmt.cloudflare.token.originCaKey', 'This is an Origin CA key, which only makes certificates. Cosmos needs an API token (My Profile > API Tokens > Create Token > "Edit zone DNS").'],
  oauth: ['error', 'mgmt.cloudflare.token.oauth', 'This is a login of Cloudflare\'s command line tool, not an API token. Create one under My Profile > API Tokens.'],
  no_zones: ['error', 'mgmt.cloudflare.token.noZones', 'The token is valid but sees no domain. It needs two permissions for your domain: Zone > Zone > Read and Zone > DNS > Edit.'],
  unreachable: ['warning', 'mgmt.cloudflare.token.unreachable', 'Cloudflare could not be reached from this server just now. Try again in a moment.'],
  rate_limited: ['warning', 'mgmt.cloudflare.token.rateLimited', 'Cloudflare asks to slow down: try again in a few minutes.'],
  error: ['error', 'mgmt.cloudflare.token.error', 'Cloudflare refused the request.'],
};

export const tokenStatusText = (status) => TOKEN_STATUS[status] || TOKEN_STATUS.error;

// What a token may do in one domain: [severity, key, text]. `zone` is an
// entry of the check's answer.
export const zoneAccessText = (zone) => {
  if (!zone) return ['info', '', ''];
  if (zone.canEdit === 'no') {
    return ['error', 'mgmt.cloudflare.zone.readOnly', 'The token may read the records of this domain but not change them. Add the permission Zone > DNS > Edit.'];
  }
  if (zone.canRead === 'no') {
    return ['error', 'mgmt.cloudflare.zone.noDns', 'The token sees this domain but not its records. Add the permission Zone > DNS > Edit.'];
  }
  if (zone.canEdit === 'yes') {
    return ['success', 'mgmt.cloudflare.zone.canEdit', 'The token may change the records of this domain. Nothing was created or changed to find out.'];
  }
  return ['info', 'mgmt.cloudflare.zone.unknown', 'Cosmos could not tell whether the token may change the records of this domain. The first record it writes will show.'];
};

// A failed call to Cloudflare, by the server's reason.
export const FAILURE = {
  invalid_token: ['mgmt.cloudflare.failure.invalidToken', 'Cloudflare does not accept the token (wrong, expired or deleted). Paste a current one under Configuration > Domains.'],
  malformed_token: ['mgmt.cloudflare.failure.malformedToken', 'What is stored is not an API token. Paste one under Configuration > Domains.'],
  forbidden: ['mgmt.cloudflare.failure.forbidden', 'The token may not do this. It needs Zone > Zone > Read and Zone > DNS > Edit for this domain.'],
  not_found: ['mgmt.cloudflare.failure.notFound', 'The token does not see this domain at Cloudflare.'],
  rate_limited: ['mgmt.cloudflare.failure.rateLimited', 'Cloudflare asks to slow down. Cosmos tries again by itself.'],
  unreachable: ['mgmt.cloudflare.failure.unreachable', 'Cloudflare could not be reached from this server. Cosmos tries again by itself.'],
  server: ['mgmt.cloudflare.failure.server', 'Cloudflare has a problem of its own right now. Cosmos tries again by itself.'],
  other: ['mgmt.cloudflare.failure.other', 'Cloudflare refused the change.'],
};

export const failureText = (reason) => FAILURE[reason] || FAILURE.other;

// Why an address cannot be served through Cloudflare, by the server's code.
export const CANNOT_SHIELD = {
  not_served: ['mgmt.cloudflare.cannot.notServed', 'Cosmos does not serve this address.'],
  not_cloudflare: ['mgmt.cloudflare.cannot.notCloudflare', 'This address is not under a domain connected to Cloudflare.'],
  no_token: ['mgmt.cloudflare.cannot.noToken', 'No API token is stored for this domain. Connect it again under Configuration > Domains.'],
  wildcard_record: ['mgmt.cloudflare.cannot.wildcardRecord', 'This domain publishes one wildcard record for all its names, so this address has no record of its own to switch. Turn "wildcard record" off for the domain under Configuration > Domains.'],
  http_only: ['mgmt.cloudflare.cannot.httpOnly', 'This domain is served over plain HTTP. Choose a certificate for it under Configuration > Domains first.'],
  own_port: ['mgmt.cloudflare.cannot.ownPort', 'A URL answers for this name on a port of its own, which Cloudflare does not pass on. A name is either through Cloudflare for all its ports or for none.'],
  port: ['mgmt.cloudflare.cannot.port', 'Cloudflare passes HTTPS on only on ports {{ports}}, and Cosmos is published on port {{port}}.'],
  private_address: ['mgmt.cloudflare.cannot.privateAddress', 'The records of this server point at a local address, which Cloudflare cannot reach.'],
  direct_port: ['mgmt.cloudflare.cannot.directPort', 'A port that is not a web page is reached under this name directly: Cloudflare\'s proxy would not carry it. A name is either through Cloudflare or direct, for all its ports.'],
};

export const cannotShieldText = (code) => CANNOT_SHIELD[code] || ['mgmt.cloudflare.cannot.other', 'This address cannot be served through Cloudflare.'];

// A record Cosmos leaves as it is: the domain's own name, or a name whose
// record Cosmos did not create.
const LEFT_ALONE = {
  pending: { level: 'pending', key: 'mgmt.cloudflare.record.pending', text: 'The record is being written at Cloudflare: this takes up to a minute.' },
  apexNone: { level: 'note', key: 'mgmt.cloudflare.record.apexNone',
    text: 'This is the domain\'s own name. Cosmos never writes its record: create it at Cloudflare yourself.' },
  apexHereProxied: { level: 'ok', key: 'mgmt.cloudflare.record.apexHereProxied',
    text: 'The domain\'s own record exists at Cloudflare, goes through its proxy and points at this server. Cosmos does not touch it.' },
  apexHereDnsOnly: { level: 'ok', key: 'mgmt.cloudflare.record.apexHereDnsOnly',
    text: 'The domain\'s own record exists at Cloudflare and points straight at this server, without the proxy. Cosmos does not touch it.' },
  apexElsewhereProxied: { level: 'note', key: 'mgmt.cloudflare.record.apexElsewhereProxied',
    text: 'The domain\'s own record exists at Cloudflare and goes through its proxy. Cosmos does not touch it, and cannot tell from here whether it leads to this server.' },
  apexElsewhereDnsOnly: { level: 'note', key: 'mgmt.cloudflare.record.apexElsewhereDnsOnly',
    text: 'The domain\'s own record exists at Cloudflare and points at another address than this server\'s. Cosmos does not touch it.' },
  foreignProxied: { level: 'note', key: 'mgmt.cloudflare.record.foreignProxied',
    text: 'A record for this name already exists at Cloudflare, through its proxy, and Cosmos did not create it: it is left as it is.' },
  foreignDnsOnly: { level: 'note', key: 'mgmt.cloudflare.record.foreignDnsOnly',
    text: 'A record for this name already exists at Cloudflare, without the proxy, and Cosmos did not create it: it is left as it is.' },
};

// Every sentence recordText can give, for the translations.
export const RECORD_TEXTS = () => [
  ...Object.values(LEFT_ALONE),
  { key: 'mgmt.cloudflare.record.byHand', text: 'Cosmos does not manage the records of this domain: the record is yours to create at Cloudflare.' },
  { key: 'mgmt.cloudflare.record.proxied', text: 'The record exists at Cloudflare and goes through its proxy.' },
  { key: 'mgmt.cloudflare.record.direct', text: 'The record exists at Cloudflare and points straight at this server.' },
];

// The state of an address's record: { level, key, text }. level is ok,
// pending, note or problem. `host` is an entry of the status (GET
// /api/cloudflare, data.hosts).
export const recordText = (host) => {
  const record = host && host.record;
  if (!host) {
    return { level: 'none', key: '', text: '' };
  }
  if (!record) {
    return { level: 'note', key: 'mgmt.cloudflare.record.byHand',
      text: 'Cosmos does not manage the records of this domain: the record is yours to create at Cloudflare.' };
  }
  switch (record.state) {
    case 'ok':
      return record.proxied
        ? { level: 'ok', key: 'mgmt.cloudflare.record.proxied', text: 'The record exists at Cloudflare and goes through its proxy.' }
        : { level: 'ok', key: 'mgmt.cloudflare.record.direct', text: 'The record exists at Cloudflare and points straight at this server.' };
    case 'pending':
      return LEFT_ALONE.pending;
    case 'protected':
    case 'conflict': {
      if (!record.seen) {
        return record.state === 'protected' ? LEFT_ALONE.apexNone : LEFT_ALONE.pending;
      }
      const how = record.proxied ? 'Proxied' : 'DnsOnly';
      if (record.state === 'protected') {
        return LEFT_ALONE[(record.pointsHere ? 'apexHere' : 'apexElsewhere') + how];
      }
      return LEFT_ALONE['foreign' + how];
    }
    case 'error': {
      const failure = failureText(record.reason);
      return { level: 'problem', key: failure[0], text: failure[1], detail: record.message || '' };
    }
    default:
      return LEFT_ALONE.pending;
  }
};

// True when what Cosmos expects for an address it does not manage differs
// from how its record is set at Cloudflare: 'expectsProxy' (Cosmos refuses
// direct visitors, but the record sends them straight here) or
// 'expectsDirect' (visitors come through Cloudflare, but Cosmos takes
// Cloudflare's addresses for the visitors). '' when they agree or it is not known.
export const shieldMismatch = (host) => {
  if (!host || host.managed || !host.record || !host.record.seen) return '';
  if (host.shielded && !host.record.proxied) return 'expectsProxy';
  if (!host.shielded && host.record.proxied) return 'expectsDirect';
  return '';
};

export const MISMATCH = {
  expectsProxy: ['mgmt.cloudflare.mismatch.expectsProxy', 'At Cloudflare this record is not proxied, but Cosmos is told it is: visitors reach this server directly and are refused. Switch the proxy on at Cloudflare (orange cloud), or choose "directly" here.'],
  expectsDirect: ['mgmt.cloudflare.mismatch.expectsDirect', 'At Cloudflare this record is proxied, but Cosmos is told it is not: Cosmos sees Cloudflare\'s addresses instead of the visitors\'. Choose "through Cloudflare" here.'],
};

// Image names of applications that stream video or audio. Cloudflare's terms
// restrict serving such content through its proxy on the Free, Pro and
// Business plans, so "directly" is the usual choice for them.
const MEDIA_IMAGES = ['jellyfin', 'emby', 'plex', 'plexinc', 'navidrome', 'kodi', 'airsonic', 'subsonic', 'funkwhale', 'koel',
  'ampache', 'gonic', 'audiobookshelf', 'tvheadend', 'owncast', 'peertube', 'photoprism', 'immich', 'stash', 'dim', 'kavita', 'komga'];

// True when a container image looks like a media server. `image` is what
// Docker shows: "jellyfin/jellyfin:latest", "lscr.io/linuxserver/plex", …
export const looksLikeMediaServer = (image) => {
  const name = String(image || '').toLowerCase().split('@')[0];
  const parts = name.replace(/:[^/]*$/, '').split('/').filter(Boolean);
  // the repository and its owner, not the registry's host name
  const words = parts.slice(-2).flatMap((part) => part.split(/[^a-z0-9]+/));
  return MEDIA_IMAGES.some((media) => words.includes(media) || words.some((word) => word === media + 'server' || word === media + 'mediaserver'));
};

// The panel's address the guided set-up proposes for a domain, with where
// the proposal comes from. `preview` is the server's (data of
// /api/cloudflare/preview, may be null); `current` the panel's address now.
//
// When the domain's own name already goes through Cloudflare to this server,
// the panel is reachable under it on Cosmos' port without any new record:
// that is proposed. Otherwise a name of its own under the domain.
export const proposePanel = (zone, preview, current) => {
  const domain = String(zone || '').trim().toLowerCase();
  const now = String(current || '').trim().toLowerCase().replace(/:\d+$/, '');
  if (!domain) {
    return { host: '', source: 'none' };
  }
  if (now === domain || now.endsWith('.' + domain)) {
    return { host: now, source: 'current' };
  }
  const apex = preview && preview.apex;
  if (apex && apex.exists && apex.proxied && apex.pointsHere) {
    return { host: domain, source: 'apex' };
  }
  return { host: 'cosmos.' + domain, source: 'default' };
};

// The choices for the panel's address, the proposal first, without doubles.
export const panelChoices = (zone, preview, current) => {
  const domain = String(zone || '').trim().toLowerCase();
  if (!domain) return [];
  const proposal = proposePanel(domain, preview, current).host;
  return [...new Set([proposal, 'cosmos.' + domain, domain].filter(Boolean))];
};

// A hostname under (or equal to) a domain, in the server's syntax.
export const validPanelHost = (host, zone) => {
  const name = String(host || '').trim().toLowerCase();
  const domain = String(zone || '').trim().toLowerCase();
  if (!name || !domain || name.length > 253) return false;
  if (name !== domain && !name.endsWith('.' + domain)) return false;
  return name.split('.').every((label) => /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/.test(label));
};

// What one row of the preview says: [key, text, severity].
export const PREVIEW_ACTION = {
  create: ['mgmt.cloudflare.preview.create', 'will be created', 'success'],
  update: ['mgmt.cloudflare.preview.update', 'will get this server\'s current address', 'info'],
  replace: ['mgmt.cloudflare.preview.replace', 'will be replaced', 'warning'],
  keep: ['mgmt.cloudflare.preview.keep', 'already right', 'default'],
  conflict: ['mgmt.cloudflare.preview.conflict', 'exists already: not touched', 'default'],
  protected: ['mgmt.cloudflare.preview.protected', 'the domain itself: not touched', 'default'],
  remove: ['mgmt.cloudflare.preview.remove', 'Cosmos\' own record, no longer used: will be removed', 'warning'],
};

export const previewActionText = (action) => PREVIEW_ACTION[action] || ['mgmt.cloudflare.preview.keep', 'not touched', 'default'];

// How many records a preview writes, replaces and leaves alone.
export const previewCounts = (preview) => {
  const rows = (preview && preview.rows) || [];
  const count = (...actions) => rows.filter((row) => actions.includes(row.action)).length;
  return {
    written: count('create', 'update'),
    replaced: count('replace'),
    removed: count('remove'),
    left: count('keep', 'conflict', 'protected') + ((preview && preview.untouched) || []).length,
  };
};

// The names of a preview the admin can let Cosmos replace.
export const replaceable = (preview) => ((preview && preview.rows) || [])
  .filter((row) => row.action === 'conflict' || row.action === 'replace')
  .map((row) => row.name);

// The panel's row of a preview says the panel already comes through
// Cloudflare when its record exists, is proxied and Cosmos leaves it alone.
export const panelComesThroughCloudflare = (preview) => ((preview && preview.rows) || [])
  .some((row) => row.role === 'panel' && (row.action === 'protected' || row.action === 'conflict') && row.proxied);

// The settings a domain connected through the guided set-up gets. The token
// goes where upstream keeps a provider's credentials. `existing` is the
// domain as it is configured now, if it is: what the set-up does not ask
// about (resolvers, waiting times) is kept from it.
export const zoneSettings = (token, options, existing) => {
  const before = existing && existing.DNSChallengeProvider === 'cloudflare' ? existing : {};
  const manageRecords = !options || options.manageRecords !== false;
  return {
    HTTPSCertificateMode: 'LETSENCRYPT',
    DNSChallengeProvider: 'cloudflare',
    DNSChallengeConfig: { ...(before.DNSChallengeConfig || {}), CF_DNS_API_TOKEN: token },
    DNSChallengeResolvers: before.DNSChallengeResolvers || '',
    DNSChallengePropagationWait: Number(before.DNSChallengePropagationWait) || 0,
    DisablePropagationChecks: !!before.DisablePropagationChecks,
    UseWildcardCertificate: !options || options.wildcardCertificate !== false,
    ManageRecords: manageRecords,
    WildcardRecord: !!(options && options.wildcardRecord && manageRecords),
  };
};

// Cloudflare's setting for how it talks to this server: [severity, key, text].
export const sslModeText = (mode) => ({
  strict: ['success', 'mgmt.cloudflare.ssl.strict', 'Cloudflare\'s SSL/TLS mode for this domain is "Full (strict)": it checks this server\'s certificate.'],
  full: ['warning', 'mgmt.cloudflare.ssl.full', 'Cloudflare\'s SSL/TLS mode for this domain is "Full": the connection to this server is encrypted, but Cloudflare does not check the certificate. Set it to "Full (strict)" at Cloudflare (SSL/TLS > Overview).'],
  flexible: ['warning', 'mgmt.cloudflare.ssl.flexible', 'Cloudflare\'s SSL/TLS mode for this domain is "Flexible", which does not check this server\'s certificate. Set it to "Full (strict)" at Cloudflare (SSL/TLS > Overview).'],
  off: ['error', 'mgmt.cloudflare.ssl.off', 'Cloudflare\'s SSL/TLS mode for this domain is "Off": nothing reaches this server over HTTPS through Cloudflare. Set it to "Full (strict)" at Cloudflare (SSL/TLS > Overview).'],
  origin_pull: ['success', 'mgmt.cloudflare.ssl.strict', 'Cloudflare\'s SSL/TLS mode for this domain is "Full (strict)": it checks this server\'s certificate.'],
})[mode] || ['info', 'mgmt.cloudflare.ssl.unknown', 'For addresses served through Cloudflare, its SSL/TLS mode for the domain must be "Full (strict)" (at Cloudflare: SSL/TLS > Overview). This token may not read that setting, so Cosmos cannot check it.'];

// The status entry of one hostname ('' and null are answered with null).
export const hostEntry = (status, host) => {
  const name = String(host || '').trim().toLowerCase().replace(/:\d+$/, '');
  return (name && status && (status.hosts || []).find((entry) => entry.host === name)) || null;
};

// An address with the port visitors use: links to a name must carry it
// whenever Cosmos is not on the standard port, because the standard port of
// the same name can belong to another machine. `host` may already have one.
export const withPort = (host, port) => {
  const name = String(host || '').trim();
  const number = Number(port);
  if (!name || /:\d+$/.test(name) || !number || number === 443) {
    return name;
  }
  return name + ':' + number;
};

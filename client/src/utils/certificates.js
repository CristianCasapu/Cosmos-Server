// Fork addition: what the screens say about certificates (the "Certificates"
// card and "Add another domain" under Configuration > Domains). The server
// (src/certs) answers with codes and dates; the sentences, and the few
// decisions the forms make, are here. Nothing in this file imports the UI,
// so it can be exercised on its own (certificates.test.mjs).
//
// A sentence is [translation key, English text, values]. The English text
// is what en/translation.json holds under the key, word for word.
import { checkSentence } from './reachability.js';

const s = (key, text, values) => [key, text, values || {}];

// ---- who issued it

// The certificate authority orders go to: null for Let's Encrypt itself,
// else a warning sentence.
export const caNotice = (ca) => {
  if (!ca || ca.kind === 'production') return null;
  if (ca.kind === 'staging') {
    return s('mgmt.certs.ca.staging', 'Orders go to Let\'s Encrypt\'s staging service (the server was started with ACME_STAGING=true): its certificates are for trying things out and no browser trusts them.');
  }
  return s('mgmt.certs.ca.test', 'Orders go to a test authority, not to Let\'s Encrypt (the server was started with a test directory): its certificates are trusted by no browser.');
};

// Who issued a certificate and what that is worth, in one sentence.
export const issuerSentence = (cert) => {
  const issuer = cert.issuer || '';
  if (cert.kind === 'selfsigned') {
    return s('mgmt.certs.issuer.selfsigned', 'Signed by this server itself ({{issuer}}): browsers do not trust it and show a warning, unless this server\'s own authority was installed on the device.', { issuer });
  }
  switch (cert.trust) {
    case 'public':
      return s('mgmt.certs.issuer.public', 'Issued by {{issuer}}, an authority every browser trusts.', { issuer });
    case 'staging':
      return s('mgmt.certs.issuer.staging', 'Issued by Let\'s Encrypt\'s staging service ({{issuer}}): a trial certificate that no browser trusts.', { issuer });
    case 'test':
      return s('mgmt.certs.issuer.test', 'Issued by a test authority ({{issuer}}): no browser trusts it.', { issuer });
    default:
      return s('mgmt.certs.issuer.private', 'Issued by {{issuer}}, which browsers do not know: visitors get a warning.', { issuer });
  }
};

// ---- the state of a domain's certificate, for the chip beside its name

// { level: 'success' | 'warning' | 'error' | 'info' | 'default', label: sentence }
export const certificateState = (cert) => {
  if (cert.kind === 'none') {
    return { level: 'default', label: s('mgmt.certs.state.plain', 'Plain HTTP') };
  }
  if (cert.running) {
    return { level: 'info', label: s('mgmt.certs.state.running', 'Getting the certificate…') };
  }
  if (!cert.present) {
    if (cert.blocked) return { level: 'warning', label: s('mgmt.certs.state.blocked', 'Needs DNS access') };
    if (cert.last && !cert.last.ok) return { level: 'error', label: s('mgmt.certs.state.failed', 'No certificate: the order failed') };
    return { level: 'warning', label: s('mgmt.certs.state.none', 'No certificate yet') };
  }
  if (cert.expired) {
    return { level: 'error', label: s('mgmt.certs.state.expired', 'Expired') };
  }
  if (cert.trust !== 'public') {
    return { level: 'warning', label: s('mgmt.certs.state.untrusted', 'Not trusted by browsers') };
  }
  if (cert.kind === 'acme' && cert.warn) {
    return { level: 'warning', label: s('mgmt.certs.state.late', 'Trusted, renewal overdue') };
  }
  if ((cert.uncovered || []).length > 0) {
    return { level: 'warning', label: s('mgmt.certs.state.partly', 'Trusted, some names not covered') };
  }
  return { level: 'success', label: s('mgmt.certs.state.trusted', 'Trusted by browsers') };
};

// ---- how the domain gets its certificate

export const methodSentence = (cert) => {
  switch (cert.kind) {
    case 'none':
      return s('mgmt.certs.method.none', 'Served over plain HTTP: there is no certificate and nothing is encrypted.');
    case 'selfsigned':
      return s('mgmt.certs.method.selfsigned', 'This domain is set to use the certificate this server signs itself.');
    case 'provided':
      return s('mgmt.certs.method.provided', 'This domain uses a certificate you provided. Cosmos does not renew it: replace it before it ends.');
    default:
  }
  if (cert.method === 'dns') {
    return s('mgmt.certs.method.dns', 'Cosmos orders it from Let\'s Encrypt and proves the domain by writing a record in its DNS ({{provider}}). No port has to be open for that.', { provider: cert.provider || '' });
  }
  if (cert.method === 'delegated') {
    return s('mgmt.certs.method.delegated', 'Cosmos orders it from Let\'s Encrypt. The proof is written in the DNS of {{via}}, where a record of this domain points.', { via: cert.via || '' });
  }
  if (cert.blocked) {
    return s('mgmt.certs.method.blocked', 'Nothing can be ordered for this domain as it is set up: it has no DNS access, and the other way of proving a name does not work on this server\'s ports. Until then its addresses get the certificate this server signs itself. Use "Add another domain" below.');
  }
  return s('mgmt.certs.method.http', 'Cosmos orders it from Let\'s Encrypt when the server starts and proves each name by answering on port 80 or 443.');
};

// ---- dates

// "in 12 days", "in 20 hours", "today": how far a date is, in words.
export const howFar = (date, now) => {
  const ms = new Date(date).getTime() - new Date(now).getTime();
  const hours = ms / 3600000;
  if (hours < 0) {
    const past = -hours;
    if (past < 48) return s('mgmt.certs.ago.hours', '{{count}} hours ago', { count: Math.max(1, Math.round(past)) });
    return s('mgmt.certs.ago.days', '{{count}} days ago', { count: Math.floor(past / 24) });
  }
  if (hours < 48) return s('mgmt.certs.in.hours', 'in {{count}} hours', { count: Math.max(1, Math.round(hours)) });
  return s('mgmt.certs.in.days', 'in {{count}} days', { count: Math.floor(hours / 24) });
};

// Valid until, and when it is renewed. `format` turns a date into text.
export const validitySentences = (cert, now, format) => {
  if (!cert.present || !cert.notAfter) return [];
  const out = [];
  const until = format(cert.notAfter);
  if (cert.expired) {
    out.push(s('mgmt.certs.valid.expired', 'It ended on {{date}}: browsers refuse it.', { date: until }));
  } else {
    out.push(s('mgmt.certs.valid.until', 'Valid until {{date}}.', { date: until }));
  }
  if (cert.kind !== 'acme' || !cert.renewAt) return out;
  const due = new Date(cert.renewAt).getTime() <= new Date(now).getTime();
  if (cert.blocked) {
    out.push(s('mgmt.certs.renew.blocked', 'It cannot be renewed the way the domain is set up now.'));
  } else if (!due) {
    out.push(cert.renewSource === 'ca'
      ? s('mgmt.certs.renew.byCA', 'Cosmos renews it by itself around {{date}}, the time Let\'s Encrypt asked for.', { date: format(cert.renewAt) })
      : s('mgmt.certs.renew.byLifetime', 'Cosmos renews it by itself around {{date}}, with a third of its lifetime left.', { date: format(cert.renewAt) }));
  } else if (cert.running) {
    out.push(s('mgmt.certs.renew.running', 'It is being renewed now.'));
  } else if (cert.wait) {
    out.push(s('mgmt.certs.renew.waiting', 'It is due for renewal since {{date}}; the last order failed, see below.', { date: format(cert.renewAt) }));
  } else {
    out.push(s('mgmt.certs.renew.due', 'It is due for renewal since {{date}}: Cosmos orders it at its next check, within half an hour.', { date: format(cert.renewAt) }));
  }
  return out;
};

// ---- why an order failed, and what to do

// The codes of src/certs/failure.go. Each: [key, what happened, what to do].
export const FAILURES = {
  rateLimited: ['mgmt.certs.fail.rateLimited', 'Let\'s Encrypt asked to wait: too many certificates or too many failed attempts for this domain lately.', 'Nothing to do: Cosmos tries again by itself once the wait is over. The certificate in use stays.'],
  caUnreachable: ['mgmt.certs.fail.caUnreachable', 'Let\'s Encrypt could not be reached, or had a problem of its own.', 'Check that this server reaches the internet. Cosmos tries again by itself.'],
  caRefused: ['mgmt.certs.fail.caRefused', 'Let\'s Encrypt refused to issue for these names.', 'Check the names of the applications under this domain: each must be a public name, spelled as it is in the DNS.'],
  account: ['mgmt.certs.fail.account', 'Let\'s Encrypt did not accept this server\'s account.', 'Cosmos registers a new account at the next order. Press "Renew now".'],
  dnsCredentials: ['mgmt.certs.fail.dnsCredentials', 'The DNS provider refused the token or key stored for this domain.', 'Create a new one at the provider and store it for the domain (the pencil on its row above), then press "Renew now".'],
  dnsZone: ['mgmt.certs.fail.dnsZone', 'The DNS provider does not have this domain under the token or key that is stored.', 'Give the token access to this domain at the provider (to read the domain and to edit its DNS), then press "Renew now".'],
  dnsProvider: ['mgmt.certs.fail.dnsProvider', 'The DNS provider could not create the record that proves the domain.', 'Often a passing problem at the provider: Cosmos tries again by itself. If it lasts, check the provider\'s settings of the domain (the pencil on its row above).'],
  dnsPropagation: ['mgmt.certs.fail.dnsPropagation', 'The record was created, but did not appear on the domain\'s name servers in time.', 'Check at your registrar that the domain\'s name servers are the provider\'s. Cosmos tries again by itself.'],
  validation: ['mgmt.certs.fail.validation', 'Let\'s Encrypt looked for the record that proves the domain and did not find it.', 'Check at your registrar that the domain\'s name servers are the DNS provider\'s where Cosmos writes. Cosmos tries again by itself.'],
  caa: ['mgmt.certs.fail.caa', 'A CAA record of the domain forbids Let\'s Encrypt to issue for it.', 'At your DNS provider, add a CAA record that allows letsencrypt.org (or remove the CAA records), then press "Renew now".'],
  delegationMissing: ['mgmt.certs.fail.delegationMissing', 'The record that hands this domain\'s proof to the other domain is missing or points somewhere else.', 'Create the record shown below at this domain\'s DNS provider, then press "Renew now". A new record can take a few minutes to be visible.'],
  noProvider: ['mgmt.certs.fail.noProvider', 'Nothing is set up to prove this domain.', 'Give the domain DNS access, or hand its proof to another domain: "Add another domain" below.'],
  timeout: ['mgmt.certs.fail.timeout', 'The order took too long and was stopped.', 'Cosmos tries again by itself.'],
  other: ['mgmt.certs.fail.other', 'The order failed.', 'Cosmos tries again by itself. The detail below is what the other side said.'],
};

// The same list as FailureCodes in src/certs/failure.go.
export const FAILURE_CODES = Object.keys(FAILURES);

export const failureSentences = (code) => {
  const entry = FAILURES[code] || FAILURES.other;
  return { what: s(entry[0] + '.what', entry[1]), todo: s(entry[0] + '.todo', entry[2]) };
};

// Why no order starts by itself before a moment.
export const waitSentence = (wait, format) => {
  if (!wait) return null;
  const date = format(wait.until);
  switch (wait.reason) {
    case 'rateLimited':
      return s('mgmt.certs.wait.rateLimited', 'Let\'s Encrypt asked to wait: nothing is ordered before {{date}}.', { date });
    case 'daily':
      return s('mgmt.certs.wait.daily', 'Ten orders were made for this certificate in a day, which is the limit Cosmos sets itself: the next one can start {{date}}.', { date });
    case 'weekly':
      return s('mgmt.certs.wait.weekly', 'Thirty orders were made for this certificate in a week, which is the limit Cosmos sets itself: the next one can start {{date}}.', { date });
    case 'sameNames':
      return s('mgmt.certs.wait.sameNames', 'Three certificates were issued for exactly these names in a week (Let\'s Encrypt allows five): the next one can start {{date}}.', { date });
    default:
      return s('mgmt.certs.wait.backoff', 'After a failed order Cosmos waits before it tries again by itself: next try {{date}}. "Renew now" does not wait.', { date });
  }
};

// Why "Renew now" is not on offer, '' when it is.
export const renewBlockedSentence = (cert) => {
  if (cert.canRenew) return null;
  switch (cert.renewBlocked) {
    case 'running':
      return s('mgmt.certs.blocked.running', 'An order is running.');
    case 'notOrdered':
      return null;
    case 'noHTTPChallenge':
      return null; // said by methodSentence
    case 'needsRestart':
      return s('mgmt.certs.blocked.needsRestart', 'This certificate is ordered while the server starts: tick "force renewal" under Configuration > HTTPS and save.');
    case 'rateLimited':
      return s('mgmt.certs.blocked.rateLimited', 'Let\'s Encrypt asked to wait: "Renew now" is possible again after that.');
    case 'daily':
    case 'weekly':
      return s('mgmt.certs.blocked.limit', 'The limit of orders Cosmos sets itself is reached: "Renew now" is possible again later.');
    case 'sameNames':
      return s('mgmt.certs.blocked.sameNames', 'Three certificates were issued for these names this week: the one in use is recent, and one more would only use up Let\'s Encrypt\'s allowance.');
    default:
      return s('mgmt.certs.blocked.other', 'The certificate cannot be renewed right now.');
  }
};

// ---- the addresses

// What a visitor's browser makes of an address: { level, sentence }. The
// words for a problem are the reachability check's own (reachability.js).
export const hostState = (host, format) => {
  if (host.problem === 'plainHTTP') {
    const plain = checkSentence({ id: 'cert', code: 'plainHTTP' });
    return { level: 'info', sentence: s(plain.key, plain.text) };
  }
  if (host.trusted) {
    if (host.shielded) {
      return { level: 'ok', sentence: s('mgmt.certs.host.shieldedOk', 'Through Cloudflare: visitors see Cloudflare\'s certificate, and Cloudflare accepts this one ("Full (strict)").') };
    }
    return { level: 'ok', sentence: s('mgmt.certs.host.directOk', 'Served directly: a visitor\'s browser trusts it, until {{date}}.', { date: format(host.until) }) };
  }
  const problem = checkSentence({ id: 'cert', code: host.problem || 'none' });
  if (host.shielded) {
    return { level: 'problem', sentence: s('mgmt.certs.host.shieldedBad', 'Through Cloudflare, which refuses this certificate in "Full (strict)" mode: visitors get an error page from Cloudflare.'), detail: s(problem.key, problem.text) };
  }
  return { level: 'problem', sentence: s(problem.key, problem.text) };
};

// ---- which challenges work here

// The note on HTTP-01 / TLS-ALPN-01. `challenges` is the server's answer.
export const challengeNote = (challenges) => {
  if (!challenges) return null;
  const ports = { https: challenges.httpsPort, http: challenges.httpPort };
  if (challenges.http === 'impossible' && challenges.reason === 'admin') {
    return { level: 'info', sentence: s('mgmt.certs.challenge.no', 'You told Cosmos that certificate authorities cannot reach it on port 80 or 443: every certificate is ordered through the domain\'s DNS.') };
  }
  if (challenges.http === 'impossible') {
    return { level: 'info', sentence: s('mgmt.certs.challenge.impossible', 'On this server a domain is proven through its DNS, and in no other way. The other way (the HTTP-01 and TLS-ALPN-01 challenges) has the certificate authority connect to port 80 or 443 of the name, and from the internet Cosmos answers on ports {{http}} and {{https}}.', ports) };
  }
  if (challenges.reason === 'admin') {
    return { level: 'info', sentence: s('mgmt.certs.challenge.yes', 'You told Cosmos that it is reached on port 80 or 443 from the internet: domains without DNS access are ordered by answering there, when the server starts.') };
  }
  return null;
};

export const CHALLENGE_CHOICES = [
  { value: '', label: s('mgmt.certs.challenge.choice.detect', 'Work it out'), description: s('mgmt.certs.challenge.choice.detectHelp', 'Cosmos goes by the ports it is reached on from the internet: possible on 80 or 443, impossible on any other.') },
  { value: 'no', label: s('mgmt.certs.challenge.choice.no', 'Never reached on 80 or 443'), description: s('mgmt.certs.challenge.choice.noHelp', 'Ports 80 and 443 of your address go to another machine, or nowhere. Cosmos never tries that way.') },
  { value: 'yes', label: s('mgmt.certs.challenge.choice.yes', 'Reached on 80 or 443'), description: s('mgmt.certs.challenge.choice.yesHelp', 'Something in front of Cosmos passes port 80 or 443 on to it. For example the machine that has port 80 forwards the path /.well-known/acme-challenge/ of each name to Cosmos\' HTTP port. Without that, every such order fails.') },
];

// ---- adding a domain

// A domain name as an admin types it: trimmed, lower case, no dot at the end.
export const cleanDomain = (text) => String(text || '').trim().toLowerCase().replace(/^https?:\/\//, '').replace(/[/:].*$/, '').replace(/\.$/, '');

// '' when the text is a domain name, else why not (a sentence).
export const domainProblem = (text, zones) => {
  const name = cleanDomain(text);
  if (name === '') return null;
  if (!/^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/.test(name) || name.length > 253 || /^[0-9.]+$/.test(name)) {
    return s('mgmt.certs.add.badName', 'Enter the domain as you bought it, for example example.net: letters, digits and hyphens, with a dot.');
  }
  if ((zones || []).some((zone) => !zone.derived && String(zone.Zone).toLowerCase() === name)) {
    return s('mgmt.certs.add.exists', 'This server already has this domain: change it with the pencil on its row.');
  }
  return null;
};

// The variables to ask for: lego's credentials without its aliases (a
// second name for the same value), and what stays behind "Custom settings".
export const providerFields = (provider) => {
  const all = (provider && provider.credentials) || [];
  const main = all.filter((v) => !/^alias (to|of|for) /i.test(v.text || ''));
  return {
    main: main.length > 0 ? main : all,
    custom: ((provider && provider.optional) || []).filter((v) => !/_BASE_URL$/.test(v.name)),
  };
};

// What the dialog proposes once the server looked the domain up.
// { kind: 'cloudflare' | 'known' | 'unknown' | 'failed', source: sentence }
export const proposalFor = (info) => {
  if (!info) return null;
  const servers = (info.nameservers || []).join(', ');
  if (info.lookupFailed) {
    return { kind: 'failed', source: s('mgmt.certs.add.lookupFailed', 'The name servers of this domain could not be looked up just now. Check the spelling; you can still choose below.') };
  }
  if ((info.nameservers || []).length === 0) {
    return { kind: 'unknown', source: s('mgmt.certs.add.noServers', 'This domain has no name servers in the DNS: is it registered, and spelled right?') };
  }
  if (info.provider === 'cloudflare') {
    return { kind: 'cloudflare', source: s('mgmt.certs.add.atCloudflare', 'Its name servers are Cloudflare\'s ({{servers}}).', { servers }) };
  }
  if (info.provider) {
    return { kind: 'known', source: s('mgmt.certs.add.atProvider', 'Its name servers are at {{provider}} ({{servers}}), which Cosmos can write to with a token or key of yours.', { provider: info.providerName || info.provider, servers }) };
  }
  return { kind: 'unknown', source: s('mgmt.certs.add.atUnknown', 'Its name servers ({{servers}}) are not ones Cosmos recognises.', { servers }) };
};

// The ways on offer for a domain, in the order they are proposed.
export const waysFor = (info) => {
  const ways = [];
  const proposal = proposalFor(info);
  const canDelegate = ((info && info.canAnswer) || []).length > 0;
  if (proposal && proposal.kind === 'known') ways.push('access');
  if (canDelegate) ways.push('delegate');
  if (!ways.includes('access')) ways.push('access');
  return ways;
};

export const WAY_TEXT = {
  access: {
    label: s('mgmt.certs.add.way.access', 'Give Cosmos access to its DNS'),
    description: s('mgmt.certs.add.way.accessHelp', 'You create a token or key at the domain\'s DNS provider and store it here. Cosmos then writes the proof itself at every renewal.'),
  },
  delegate: {
    label: s('mgmt.certs.add.way.delegate', 'One record at its DNS provider'),
    description: s('mgmt.certs.add.way.delegateHelp', 'You create one record at the domain\'s DNS provider, once. It says that the proof for this domain is found in a domain Cosmos can already write to. Cosmos needs no access to this domain.'),
  },
};

// The state of the record of a delegated challenge: { ok, sentence }.
export const delegationText = (check) => {
  const values = { record: (check && check.record) || '', target: (check && check.target) || '', found: (check && check.found) || '' };
  switch (check && check.state) {
    case 'ok':
      return { ok: true, sentence: s('mgmt.certs.delegation.ok', 'The record is there: {{record}} points at {{found}}.', values) };
    case 'elsewhere':
      return { ok: false, sentence: s('mgmt.certs.delegation.elsewhere', '{{record}} exists but points at {{found}}. It has to point at {{target}}.', values) };
    case 'loop':
      return { ok: false, sentence: s('mgmt.certs.delegation.loop', '{{record}} leads through aliases that never end. It has to point at {{target}}.', values) };
    case 'failed':
      return { ok: false, sentence: s('mgmt.certs.delegation.failed', 'The record could not be looked up just now. Try again in a moment.', values) };
    default:
      return { ok: false, sentence: s('mgmt.certs.delegation.missing', 'The record is not visible yet. A new record can take a few minutes; if it stays missing, compare what you entered with the two values above.', values) };
  }
};

// What is stored for a new domain (the body of PUT /api/zones/<domain>).
export const zoneToSave = ({ way, provider, variables, via, wildcard }) => {
  const zone = {
    HTTPSCertificateMode: 'LETSENCRYPT',
    UseWildcardCertificate: wildcard !== false,
    DNSChallengeProvider: '',
    DNSChallengeConfig: {},
    DNSChallengeResolvers: '',
    DNSChallengePropagationWait: 0,
    DisablePropagationChecks: false,
    ManageRecords: false,
    WildcardRecord: false,
    ChallengeVia: '',
  };
  if (way === 'delegate') {
    zone.ChallengeVia = via || '';
    return zone;
  }
  zone.DNSChallengeProvider = provider || '';
  for (const [name, value] of Object.entries(variables || {})) {
    if (String(value).trim() !== '') zone.DNSChallengeConfig[name] = String(value).trim();
  }
  return zone;
};

// What is still missing before the domain can be added (a sentence), or null.
export const missingForSave = ({ way, provider, fields, variables, via, checked }) => {
  if (way === 'delegate') {
    if (!via) return s('mgmt.certs.add.needVia', 'Choose the domain the proof is written in.');
    if (!checked) return s('mgmt.certs.add.needCheck', 'Create the record, then press "Check the record": the domain is added once the record is found.');
    return null;
  }
  if (!provider) return s('mgmt.certs.add.needProvider', 'Choose the DNS provider of this domain.');
  const filled = (fields || []).some((field) => String((variables || {})[field.name] || '').trim() !== '');
  if (!filled) return s('mgmt.certs.add.needCredential', 'Enter the token or key the provider gave you.');
  return null;
};

// Fork addition: what the panel says about "Authenticated Origin Pulls" (the
// certificate Cloudflare's proxy presents to this server, see
// src/cloudflare/originpulls.go). The server answers with codes and counts;
// the sentences, and the few decisions the card makes, are here. Nothing in
// this file imports the UI, so it can be exercised on its own.
//
// A sentence is [translation key, English text] or, where the caller fills
// something in, [key, text, values].

// Whose certificate Cloudflare presents: the two options, the recommended
// one first. `description` says what each proves and what it does not.
export const KINDS = [
  {
    value: 'own',
    label: ['mgmt.cloudflare.pulls.kind.own', 'A certificate of your own (recommended)'],
    description: ['mgmt.cloudflare.pulls.kind.ownText',
      'Cosmos makes a certificate and you give it to Cloudflare for your domain. Only your domain at Cloudflare can present it, so it proves that a request passed your own Cloudflare settings. Free on every Cloudflare plan. It is valid for a year; Cosmos reminds you before it ends.'],
  },
  {
    value: 'shared',
    label: ['mgmt.cloudflare.pulls.kind.shared', 'Cloudflare\'s shared certificate'],
    description: ['mgmt.cloudflare.pulls.kind.sharedText',
      'One switch at Cloudflare, nothing to upload. It proves that Cloudflare\'s proxy made the connection for some domain that has this switched on, and any Cloudflare customer can switch it on for a domain of theirs. It does not prove that a request passed your domain.'],
  },
];

// Why the server refused an action, by its code.
export const REFUSAL = {
  no_kind: ['mgmt.cloudflare.pulls.refusal.noKind', 'Choose which certificate Cloudflare presents first.'],
  no_certificate: ['mgmt.cloudflare.pulls.refusal.noCertificate', 'Make a certificate and give it to Cloudflare first.'],
  shared_expired: ['mgmt.cloudflare.pulls.refusal.sharedExpired', 'Cloudflare\'s certificate built into this version of Cosmos has expired. Update Cosmos, or use a certificate of your own.'],
  not_testing: ['mgmt.cloudflare.pulls.refusal.notTesting', 'Start the test first.'],
  nothing_verified: ['mgmt.cloudflare.pulls.refusal.nothingVerified', 'No request has verified yet, so enforcing would refuse everything that comes through Cloudflare. Open an address from outside your network and wait until it is counted.'],
  failing: ['mgmt.cloudflare.pulls.refusal.failing', 'Requests without the certificate arrived within the last minute.'],
  enforcing: ['mgmt.cloudflare.pulls.refusal.enforcing', 'Stop enforcing first: this change would stop requests from verifying.'],
  no_pending: ['mgmt.cloudflare.pulls.refusal.noPending', 'Cosmos no longer holds the key of this certificate: it is kept for 15 minutes and handed over once. Make a new certificate.'],
  two_certificates: ['mgmt.cloudflare.pulls.refusal.twoCertificates', 'Two certificates are accepted already. Stop accepting the one Cloudflare no longer uses first.'],
  not_cloudflare_zone: ['mgmt.cloudflare.pulls.refusal.notCloudflareZone', 'This is not a domain connected to Cloudflare.'],
  last_certificate: ['mgmt.cloudflare.pulls.refusal.lastCertificate', 'This is the only certificate: without it nothing would verify. Switch the check off first, or make a new certificate.'],
  not_seen: ['mgmt.cloudflare.pulls.refusal.notSeen', 'No request has verified with the other certificate yet. Upload it at Cloudflare and wait until Cloudflare uses it.'],
  not_enforcing: ['mgmt.cloudflare.pulls.refusal.notEnforcing', 'Enforcing had already ended.'],
  too_late: ['mgmt.cloudflare.pulls.refusal.notEnforcing', 'Enforcing had already ended.'],
};

export const refusalText = (reason) => REFUSAL[reason] || ['mgmt.cloudflare.pulls.refusal.other', 'This is not possible right now.'];

// Why Cloudflare did not take the certificate, by the reason of the failed
// call. The stored token is the "Edit zone DNS" kind, which may not upload
// certificates: that is the expected answer, with what to do next.
export const UPLOAD_FAILURE = {
  forbidden: ['mgmt.cloudflare.pulls.upload.forbidden',
    'The token Cosmos has stored may not upload certificates: it only edits DNS records, and Cosmos does not ask for more. Either paste a token made for this one step (at Cloudflare: My Profile > API Tokens > Create Token > Custom token, permission Zone > SSL and Certificates > Edit, for this domain only); Cosmos uses it once and does not keep it. Or show the certificate and its key and upload them at Cloudflare yourself.'],
  invalid_token: ['mgmt.cloudflare.pulls.upload.invalidToken', 'Cloudflare does not accept this token (wrong, expired or deleted).'],
  malformed_token: ['mgmt.cloudflare.pulls.upload.malformedToken', 'This is not an API token: one is a single line of letters, digits, "_" and "-".'],
  not_found: ['mgmt.cloudflare.pulls.upload.notFound', 'Neither token sees this domain at Cloudflare.'],
  rate_limited: ['mgmt.cloudflare.pulls.upload.rateLimited', 'Cloudflare asks to slow down: try again in a few minutes.'],
  unreachable: ['mgmt.cloudflare.pulls.upload.unreachable', 'Cloudflare could not be reached from this server just now. Try again in a moment.'],
  server: ['mgmt.cloudflare.pulls.upload.server', 'Cloudflare has a problem of its own right now. Try again in a moment.'],
  other: ['mgmt.cloudflare.pulls.upload.other', 'Cloudflare refused the certificate.'],
};

export const uploadFailureText = (reason) => UPLOAD_FAILURE[reason] || UPLOAD_FAILURE.other;

// Why a request from Cloudflare's addresses did not count as made by
// Cloudflare's proxy: a few words, used inside a sentence.
export const PROBLEM = {
  no_certificate: ['mgmt.cloudflare.pulls.problem.noCertificate', 'no certificate'],
  wrong_certificate: ['mgmt.cloudflare.pulls.problem.wrongCertificate', 'another certificate than the expected one'],
  expired_certificate: ['mgmt.cloudflare.pulls.problem.expiredCertificate', 'an expired certificate'],
  not_https: ['mgmt.cloudflare.pulls.problem.notHttps', 'not over HTTPS'],
};

export const problemText = (reason) => PROBLEM[reason] || ['mgmt.cloudflare.pulls.problem.other', 'not verified'];

// What to do at Cloudflare, step by step, for a kind. `byHand` is for a
// certificate of your own that Cosmos did not upload itself.
export const STEPS = {
  open: ['mgmt.cloudflare.pulls.steps.open', 'At Cloudflare, open your domain, then SSL/TLS > Origin Server, and there the tab "Authenticated Origin Pulls".'],
  global: ['mgmt.cloudflare.pulls.steps.global', 'Switch "Global" on.'],
  upload: ['mgmt.cloudflare.pulls.steps.upload', 'Under "Zone-level", choose "Upload certificate" and paste the certificate and the private key Cosmos shows you.'],
  zone: ['mgmt.cloudflare.pulls.steps.zone', 'Switch "Zone-level" on.'],
  strict: ['mgmt.cloudflare.pulls.steps.strict', 'SSL/TLS > Overview must say "Full (strict)": with "Flexible" or "Off" Cloudflare presents no certificate.'],
  wait: ['mgmt.cloudflare.pulls.steps.wait', 'Come back here and start the test. A change at Cloudflare takes up to a minute to be in use.'],
};

export const stepsAtCloudflare = (kind) => (kind === 'shared'
  ? [STEPS.open, STEPS.global, STEPS.strict, STEPS.wait]
  : [STEPS.open, STEPS.upload, STEPS.zone, STEPS.strict, STEPS.wait]);

// A fingerprint the way certificate viewers show it: pairs, upper case.
export const showFingerprint = (hex) => (String(hex || '').toUpperCase().match(/.{1,2}/g) || []).join(':');

// The option in one sentence: [severity, key, text, values].
const STATE = {
  off: ['info', 'mgmt.cloudflare.pulls.state.off', 'Off: a request for an address served through Cloudflare is accepted from any of Cloudflare\'s addresses.'],
  test: ['info', 'mgmt.cloudflare.pulls.state.test', 'Testing: Cosmos asks Cloudflare\'s proxy for its certificate and counts what it gets. Nothing is refused yet.'],
  unconfirmed: ['warning', 'mgmt.cloudflare.pulls.state.unconfirmed', 'Enforcing, not confirmed yet. If this browser cannot reach Cosmos any more, Cosmos goes back to testing by itself in {{seconds}} seconds.'],
  enforce: ['success', 'mgmt.cloudflare.pulls.state.enforce', 'Enforcing: a request for an address served through Cloudflare is answered only when Cloudflare\'s proxy presented the certificate, or when it comes from your local network.'],
};

export const stateText = (pulls) => {
  if (!pulls || !pulls.mode) return STATE.off;
  if (pulls.mode === 'test') return STATE.test;
  if (pulls.unconfirmed) return [...STATE.unconfirmed, { seconds: pulls.confirmSeconds || 0 }];
  return STATE.enforce;
};

// The certificate requests are verified with, when its end is near:
// [severity, key, text, values], or null. From 60 days before.
export const EXPIRY = {
  sharedSoon: ['warning', 'mgmt.cloudflare.pulls.expiry.sharedSoon', 'Cloudflare\'s certificate built into this version of Cosmos ends in {{days}} days ({{date}}). Cloudflare will present a new one by then: update Cosmos before that date, or change to a certificate of your own.'],
  sharedOver: ['error', 'mgmt.cloudflare.pulls.expiry.sharedOver', 'Cloudflare\'s certificate built into this version of Cosmos has ended ({{date}}): nothing verifies with it any more. Update Cosmos, or change to a certificate of your own.'],
  ownSoon: ['warning', 'mgmt.cloudflare.pulls.expiry.ownSoon', 'Your certificate ends in {{days}} days ({{date}}). Make a new one below and give it to Cloudflare before then; both are accepted during the change.'],
  ownOver: ['error', 'mgmt.cloudflare.pulls.expiry.ownOver', 'Your certificate has ended ({{date}}): nothing verifies with it any more. Make a new one below and give it to Cloudflare.'],
};

const day = (time) => String(time || '').slice(0, 10);

export const expiryWarning = (pulls) => {
  if (!pulls || !pulls.kind) return null;
  if (pulls.kind === 'shared') {
    const shared = pulls.shared || {};
    if (!shared.notAfter || shared.daysLeft > 60) return null;
    const entry = shared.daysLeft <= 0 ? EXPIRY.sharedOver : EXPIRY.sharedSoon;
    return [...entry, { days: shared.daysLeft, date: day(shared.notAfter) }];
  }
  // the certificate that lasts longest among those Cloudflare can have
  const handed = (pulls.own || []).filter((cert) => cert.handedOver);
  if (handed.length === 0) return null;
  const last = handed.reduce((a, b) => (String(a.notAfter) >= String(b.notAfter) ? a : b));
  if (last.daysLeft > 60) return null;
  const entry = last.daysLeft <= 0 ? EXPIRY.ownOver : EXPIRY.ownSoon;
  return [...entry, { days: last.daysLeft, date: day(last.notAfter) }];
};

// One certificate of your own, in words: [key, text, values].
export const CERT_STATE = {
  waiting: ['mgmt.cloudflare.pulls.cert.waiting', 'Made just now, not given to Cloudflare yet. Cosmos holds its key for {{minutes}} more minutes.'],
  unused: ['mgmt.cloudflare.pulls.cert.unused', 'Made, never given to Cloudflare, and its key is gone: nobody can present it.'],
  verified: ['mgmt.cloudflare.pulls.cert.verified', 'In use: Cloudflare presented it at {{time}}.'],
  handed: ['mgmt.cloudflare.pulls.cert.handed', 'Given to Cloudflare. No request has presented it since counting began.'],
};

const clock = (time) => {
  const date = new Date(time);
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleTimeString();
};

export const certState = (cert) => {
  if (!cert) return CERT_STATE.unused;
  if (cert.waiting) return [...CERT_STATE.waiting, { minutes: Math.max(1, Math.ceil((cert.waitingSeconds || 0) / 60)) }];
  if (!cert.handedOver) return CERT_STATE.unused;
  if (cert.lastVerified) return [...CERT_STATE.verified, { time: clock(cert.lastVerified) }];
  return CERT_STATE.handed;
};

// The certificate that waits to be handed over, if there is one.
export const waitingCert = (pulls) => ((pulls && pulls.own) || []).find((cert) => cert.waiting) || null;

// What the counts say: [severity, key, text, values]. `shielded` are the
// addresses served through Cloudflare (for the hint on where to look).
export const VERDICT = {
  none: ['info', 'mgmt.cloudflare.pulls.verdict.none', 'No request has come through Cloudflare since counting began. Open one of these addresses from outside your network (a phone on mobile data, Wi-Fi off): {{hosts}}'],
  clean: ['success', 'mgmt.cloudflare.pulls.verdict.clean', '{{verified}} requests came through Cloudflare, each with a certificate that verified. Enforcing will not refuse them.'],
  nothing: ['warning', 'mgmt.cloudflare.pulls.verdict.nothing', '{{failed}} requests came from Cloudflare\'s addresses and none presented the certificate ({{reasons}}). At Cloudflare it is not in place or not switched on yet: go through the steps above, then wait a minute.'],
  settled: ['success', 'mgmt.cloudflare.pulls.verdict.settled', '{{verified}} requests verified; {{failed}} earlier ones did not ({{reasons}}), none within the last minute. Those came while the setting was still spreading at Cloudflare.'],
  failing: ['warning', 'mgmt.cloudflare.pulls.verdict.failing', '{{verified}} requests verified, {{failed}} did not ({{reasons}}), some of them within the last minute, for: {{hosts}}. If you changed something at Cloudflare just now, wait a minute. If not, these are requests that did not pass your domain at Cloudflare: exactly what enforcing refuses.'],
  enforcing: ['info', 'mgmt.cloudflare.pulls.verdict.enforcing', 'Since enforcing began: {{verified}} requests answered, {{failed}} refused.'],
  enforcingRefused: ['warning', 'mgmt.cloudflare.pulls.verdict.enforcingRefused', 'Since enforcing began: {{verified}} requests answered, {{failed}} refused ({{reasons}}). If your own visits through Cloudflare stopped working, press "Stop enforcing".'],
};

// `say` turns a [key, text] pair into words (the page's t); without it the
// English text is used.
export const verdict = (pulls, shielded, say) => {
  const words = say || ((pair) => pair[1]);
  const counts = (pulls && pulls.counts) || {};
  const verified = counts.verified || 0;
  const failed = counts.failed || 0;
  const reasons = Object.keys(counts.problems || {}).sort().map((reason) => words(problemText(reason))).join(', ');
  const failingHosts = (counts.hosts || []).filter((host) => host.failedLastMinute).map((host) => host.host).join(', ');

  if (pulls && pulls.mode === 'enforce') {
    return failed > 0
      ? [...VERDICT.enforcingRefused, { verified, failed, reasons }]
      : [...VERDICT.enforcing, { verified, failed }];
  }
  if (verified === 0 && failed === 0) {
    return [...VERDICT.none, { hosts: (shielded || []).join(', ') }];
  }
  if (failed === 0) return [...VERDICT.clean, { verified }];
  if (verified === 0) return [...VERDICT.nothing, { failed, reasons }];
  if (!counts.failedLastMinute) return [...VERDICT.settled, { verified, failed, reasons }];
  return [...VERDICT.failing, { verified, failed, reasons, hosts: failingHosts }];
};

// What the card offers next: { test, enforce, anyway, why }. `test`: the
// test can start; `enforce`: enforcing can start; `anyway`: only after an
// explicit confirmation; `why`: the sentence when the step is not possible.
export const nextStep = (pulls) => {
  const none = { test: false, enforce: false, anyway: false, why: null };
  if (!pulls) return none;
  if (!pulls.mode) {
    return pulls.cannotTest ? { ...none, why: refusalText(pulls.cannotTest) } : { ...none, test: true };
  }
  if (pulls.mode === 'enforce') return none;
  if (!pulls.cannotEnforce) return { ...none, enforce: true };
  if (pulls.cannotEnforce === 'failing') return { ...none, enforce: true, anyway: true, why: refusalText('failing') };
  return { ...none, why: refusalText(pulls.cannotEnforce) };
};

// Whether two states of the option say the same about what is switched on
// and which certificates exist (the counts aside). The card uses it to know
// that the status it reads again has caught up with what an action answered:
// right after some actions the server takes its connections down for a
// moment, and what is read then can be the state from before.
export const sameState = (a, b) => {
  if (!a || !b) return false;
  const certs = (pulls) => (pulls.own || []).map((cert) => [cert.fingerprint, !!cert.handedOver, !!cert.waiting].join(':')).join(',');
  return (a.mode || '') === (b.mode || '') && (a.kind || '') === (b.kind || '')
    && !!a.unconfirmed === !!b.unconfirmed && certs(a) === certs(b);
};

// Every sentence of this file, for the check against the translation file.
export const ALL_TEXTS = () => [
  ...KINDS.flatMap((kind) => [kind.label, kind.description]),
  ...Object.values(REFUSAL),
  refusalText('?'),
  ...Object.values(UPLOAD_FAILURE),
  ...Object.values(PROBLEM),
  problemText('?'),
  ...Object.values(STEPS),
  ...Object.values(STATE).map((entry) => entry.slice(1)),
  ...Object.values(EXPIRY).map((entry) => entry.slice(1)),
  ...Object.values(CERT_STATE),
  ...Object.values(VERDICT).map((entry) => entry.slice(1)),
];

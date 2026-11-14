// node --test client/src/utils/originPulls.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  ALL_TEXTS, KINDS, PROBLEM, REFUSAL, UPLOAD_FAILURE, certState, expiryWarning, nextStep, problemText, refusalText,
  sameState, showFingerprint, stateText, stepsAtCloudflare, uploadFailureText, verdict, waitingCert,
} from './originPulls.js';

const counts = (verified, failed, more = {}) => ({ verified, failed, problems: {}, hosts: [], failedLastMinute: 0, ...more });

test('the two kinds say what they prove, the recommended one first', () => {
  assert.deepEqual(KINDS.map((kind) => kind.value), ['own', 'shared']);
  assert.match(KINDS[0].label[1], /recommended/);
  assert.match(KINDS[0].description[1], /Only your domain/);
  assert.match(KINDS[0].description[1], /Free on every Cloudflare plan/);
  // the shared certificate is not sold as more than it is
  assert.match(KINDS[1].description[1], /does not prove that a request passed your domain/);
  assert.match(KINDS[1].description[1], /any Cloudflare customer/);
});

test('every code the server answers has a sentence, an unknown one a plain fallback', () => {
  // the reasons of refuse() in src/cloudflare/originpulls.go
  for (const reason of ['no_kind', 'no_certificate', 'shared_expired', 'not_testing', 'nothing_verified', 'failing', 'enforcing',
    'no_pending', 'two_certificates', 'not_cloudflare_zone', 'last_certificate', 'not_seen', 'not_enforcing', 'too_late']) {
    assert.ok(REFUSAL[reason], reason);
    assert.ok(refusalText(reason)[1].length > 15, reason);
  }
  assert.match(refusalText('<script>')[1], /not possible right now/);
  // the kinds of a failed call to Cloudflare (APIError.Kind)
  for (const reason of ['forbidden', 'invalid_token', 'malformed_token', 'not_found', 'rate_limited', 'unreachable', 'server', 'other']) {
    assert.ok(uploadFailureText(reason)[1].length > 20, reason);
  }
  assert.equal(uploadFailureText('new'), UPLOAD_FAILURE.other);
  // the reasons of utils/fork_origin_pulls.go
  for (const reason of ['no_certificate', 'wrong_certificate', 'expired_certificate', 'not_https']) {
    assert.ok(PROBLEM[reason], reason);
  }
  assert.equal(problemText('x')[1], 'not verified');
});

test('the refused upload with the stored token says what to do, and never to widen it', () => {
  const text = uploadFailureText('forbidden')[1];
  assert.match(text, /SSL and Certificates > Edit/);
  assert.match(text, /does not keep it/);
  assert.match(text, /upload them at Cloudflare yourself/);
  assert.doesNotMatch(text, /add (the|this) permission to the (stored )?token/i);
});

test('the steps at Cloudflare for each kind', () => {
  const shared = stepsAtCloudflare('shared').map((step) => step[1]).join(' ');
  assert.match(shared, /Switch "Global" on/);
  assert.doesNotMatch(shared, /Upload certificate/);
  const own = stepsAtCloudflare('own').map((step) => step[1]).join(' ');
  assert.match(own, /"Upload certificate"/);
  assert.match(own, /Switch "Zone-level" on/);
  assert.doesNotMatch(own, /"Global"/);
  for (const steps of [shared, own]) {
    assert.match(steps, /Full \(strict\)/);
    assert.doesNotMatch(steps, /port (80|443)\b/);
  }
});

test('a fingerprint is shown in pairs', () => {
  assert.equal(showFingerprint('9a1ac2b4'), '9A:1A:C2:B4');
  assert.equal(showFingerprint(''), '');
  assert.equal(showFingerprint(undefined), '');
});

test('the state in one sentence', () => {
  assert.equal(stateText(null)[1], 'mgmt.cloudflare.pulls.state.off');
  assert.equal(stateText({ mode: '' })[0], 'info');
  assert.equal(stateText({ mode: 'test' })[1], 'mgmt.cloudflare.pulls.state.test');
  assert.match(stateText({ mode: 'test' })[2], /Nothing is refused yet/);
  const waiting = stateText({ mode: 'enforce', unconfirmed: true, confirmSeconds: 170 });
  assert.equal(waiting[0], 'warning');
  assert.deepEqual(waiting[3], { seconds: 170 });
  assert.equal(stateText({ mode: 'enforce' })[0], 'success');
  assert.match(stateText({ mode: 'enforce' })[2], /or when it comes from your local network/);
});

test('a certificate near its end is warned about from 60 days before', () => {
  assert.equal(expiryWarning(null), null);
  assert.equal(expiryWarning({ kind: '' }), null);
  const shared = (daysLeft) => ({ kind: 'shared', shared: { notAfter: '2029-11-01T17:00:00Z', daysLeft } });
  assert.equal(expiryWarning(shared(1082)), null);
  assert.equal(expiryWarning(shared(61)), null);
  assert.equal(expiryWarning(shared(60))[0], 'warning');
  assert.deepEqual(expiryWarning(shared(60))[3], { days: 60, date: '2029-11-01' });
  assert.equal(expiryWarning(shared(0))[0], 'error');

  const own = (...certs) => ({ kind: 'own', own: certs });
  assert.equal(expiryWarning(own()), null);
  assert.equal(expiryWarning(own({ handedOver: false, notAfter: '2027-01-01T00:00:00Z', daysLeft: 5 })), null, 'a certificate nobody has does not matter');
  assert.equal(expiryWarning(own({ handedOver: true, notAfter: '2027-11-14T00:00:00Z', daysLeft: 300 })), null);
  assert.equal(expiryWarning(own({ handedOver: true, notAfter: '2027-01-01T00:00:00Z', daysLeft: 20 }))[1], 'mgmt.cloudflare.pulls.expiry.ownSoon');
  assert.equal(expiryWarning(own({ handedOver: true, notAfter: '2026-11-01T00:00:00Z', daysLeft: 0 }))[0], 'error');
  // during a change the newer one counts
  assert.equal(expiryWarning(own(
    { handedOver: true, notAfter: '2026-12-01T00:00:00Z', daysLeft: 17 },
    { handedOver: true, notAfter: '2027-11-14T00:00:00Z', daysLeft: 365 },
  )), null);
});

test('one certificate of your own, in words', () => {
  assert.equal(certState({ waiting: true, waitingSeconds: 899 })[0], 'mgmt.cloudflare.pulls.cert.waiting');
  assert.deepEqual(certState({ waiting: true, waitingSeconds: 899 })[2], { minutes: 15 });
  assert.deepEqual(certState({ waiting: true, waitingSeconds: 5 })[2], { minutes: 1 });
  assert.equal(certState({ handedOver: false })[0], 'mgmt.cloudflare.pulls.cert.unused');
  assert.equal(certState({ handedOver: true })[0], 'mgmt.cloudflare.pulls.cert.handed');
  assert.equal(certState({ handedOver: true, lastVerified: '2026-11-14T10:00:00Z' })[0], 'mgmt.cloudflare.pulls.cert.verified');
  assert.equal(waitingCert({ own: [{ fingerprint: 'a' }, { fingerprint: 'b', waiting: true }] }).fingerprint, 'b');
  assert.equal(waitingCert({ own: [{ fingerprint: 'a' }] }), null);
  assert.equal(waitingCert(null), null);
});

test('what the counts say while testing', () => {
  const key = (pulls, shielded) => verdict(pulls, shielded)[1].split('.').pop();
  const hosts = ['example.org', 'radarr.example.org'];

  const none = verdict({ mode: 'test', counts: counts(0, 0) }, hosts);
  assert.equal(none[1], 'mgmt.cloudflare.pulls.verdict.none');
  assert.equal(none[3].hosts, 'example.org, radarr.example.org');

  assert.equal(key({ mode: 'test', counts: counts(14, 0) }, hosts), 'clean');
  assert.equal(verdict({ mode: 'test', counts: counts(14, 0) }, hosts)[0], 'success');

  const nothing = verdict({ mode: 'test', counts: counts(0, 9, { problems: { no_certificate: 9 }, failedLastMinute: 4 }) }, hosts);
  assert.equal(nothing[1], 'mgmt.cloudflare.pulls.verdict.nothing');
  assert.equal(nothing[0], 'warning');
  assert.equal(nothing[3].reasons, 'no certificate');

  // earlier failures, a clean last minute: the setting was spreading
  assert.equal(key({ mode: 'test', counts: counts(20, 3, { problems: { no_certificate: 3 } }) }, hosts), 'settled');

  const failing = verdict({
    mode: 'test',
    counts: counts(20, 5, {
      problems: { wrong_certificate: 2, no_certificate: 3 }, failedLastMinute: 2,
      hosts: [{ host: 'example.org', failedLastMinute: true }, { host: 'radarr.example.org', failedLastMinute: false }],
    }),
  }, hosts);
  assert.equal(failing[1], 'mgmt.cloudflare.pulls.verdict.failing');
  assert.equal(failing[3].hosts, 'example.org');
  assert.equal(failing[3].reasons, 'no certificate, another certificate than the expected one');

  // the page's own words are used for the reasons
  const translated = verdict({ mode: 'test', counts: counts(0, 1, { problems: { no_certificate: 1 } }) }, hosts, (pair) => '[' + pair[0] + ']');
  assert.equal(translated[3].reasons, '[mgmt.cloudflare.pulls.problem.noCertificate]');
});

test('what the counts say while enforcing', () => {
  const quiet = verdict({ mode: 'enforce', counts: counts(40, 0) }, []);
  assert.equal(quiet[1], 'mgmt.cloudflare.pulls.verdict.enforcing');
  assert.deepEqual(quiet[3], { verified: 40, failed: 0 });
  const refused = verdict({ mode: 'enforce', counts: counts(40, 7, { problems: { no_certificate: 7 } }) }, []);
  assert.equal(refused[1], 'mgmt.cloudflare.pulls.verdict.enforcingRefused');
  assert.equal(refused[3].failed, 7);
  assert.match(refused[2], /Stop enforcing/);
});

test('the next step the card offers', () => {
  assert.deepEqual(nextStep(null), { test: false, enforce: false, anyway: false, why: null });
  // off
  assert.equal(nextStep({ mode: '', cannotTest: '' }).test, true);
  const noCert = nextStep({ mode: '', cannotTest: 'no_certificate' });
  assert.equal(noCert.test, false);
  assert.equal(noCert.why[0], 'mgmt.cloudflare.pulls.refusal.noCertificate');
  // testing
  assert.deepEqual(nextStep({ mode: 'test', cannotEnforce: '' }), { test: false, enforce: true, anyway: false, why: null });
  const nothing = nextStep({ mode: 'test', cannotEnforce: 'nothing_verified' });
  assert.equal(nothing.enforce, false, 'enforcing with nothing verified is never offered');
  assert.equal(nothing.anyway, false);
  assert.match(nothing.why[1], /would refuse everything/);
  const failing = nextStep({ mode: 'test', cannotEnforce: 'failing' });
  assert.equal(failing.enforce, true);
  assert.equal(failing.anyway, true, 'only after an explicit confirmation');
  // enforcing: nothing to start
  assert.deepEqual(nextStep({ mode: 'enforce', cannotEnforce: '' }), { test: false, enforce: false, anyway: false, why: null });
});

test('a status that was read again has caught up with what an action answered', () => {
  const answered = { mode: 'test', kind: 'own', own: [{ fingerprint: 'a', handedOver: true }], counts: { verified: 0 } };
  assert.equal(sameState({ ...answered, counts: { verified: 9 } }, answered), true, 'the counts aside');
  assert.equal(sameState({ ...answered, mode: '' }, answered), false, 'the state from before the test began');
  assert.equal(sameState({ ...answered, kind: 'shared' }, answered), false);
  assert.equal(sameState({ ...answered, own: [] }, answered), false);
  assert.equal(sameState({ ...answered, own: [{ fingerprint: 'a', handedOver: false, waiting: true }] }, answered), false);
  assert.equal(sameState({ mode: 'enforce', unconfirmed: true }, { mode: 'enforce' }), false);
  assert.equal(sameState({ mode: undefined, kind: undefined }, { mode: '', kind: '' }), true);
  assert.equal(sameState(null, answered), false);
  assert.equal(sameState(undefined, undefined), false);
});

test('every sentence is in the English translation file, word for word', () => {
  const translations = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));
  const texts = ALL_TEXTS();
  assert.ok(texts.length > 50);
  for (const [key, text] of texts) {
    assert.equal(translations[key], text, key);
  }
  // and what the card itself says: every t('key', 'text') of the component
  const source = readFileSync(new URL('../components/originPulls.jsx', import.meta.url), 'utf8');
  const pattern = /\bt\(\s*'([^']+)',\s*'((?:[^'\\]|\\.)*)'/g;
  let match;
  let seen = 0;
  while ((match = pattern.exec(source))) {
    seen++;
    assert.equal(translations[match[1]], match[2].replace(/\\'/g, "'"), match[1]);
  }
  assert.ok(seen > 30, 'the component\'s sentences were found');
  // the notifications raised by the server
  for (const key of ['header.notification.title.originPullExpiring', 'header.notification.message.originPullExpiring',
    'header.notification.title.originPullStopped', 'header.notification.message.originPullStopped']) {
    assert.ok(translations[key], key);
  }
});

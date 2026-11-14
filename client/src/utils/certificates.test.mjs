// node --test client/src/utils/certificates.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  CHALLENGE_CHOICES, FAILURES, FAILURE_CODES, WAY_TEXT, caNotice, certificateState, challengeNote, cleanDomain, delegationText,
  domainProblem, failureSentences, hostState, howFar, issuerSentence, methodSentence, missingForSave, proposalFor, providerFields,
  renewBlockedSentence, validitySentences, waitSentence, waysFor, zoneToSave,
} from './certificates.js';
import { CHECK_TEXT } from './reachability.js';

const read = (path) => readFileSync(new URL(path, import.meta.url), 'utf8');
const translations = JSON.parse(read('./locales/en/translation.json'));
const format = (date) => new Date(date).toISOString().slice(0, 16).replace('T', ' ');
const now = '2026-11-14T12:00:00Z';
const days = (n) => new Date(new Date(now).getTime() + n * 86400000).toISOString();

// every {{value}} of a sentence is given
const complete = (sentence, where) => {
  assert.ok(Array.isArray(sentence) && sentence.length === 3, where + ': not a sentence');
  assert.equal(translations[sentence[0]], sentence[1], 'translation.json: ' + sentence[0]);
  for (const m of sentence[1].matchAll(/\{\{(\w+)\}\}/g)) {
    assert.ok(sentence[2][m[1]] !== undefined, `${where}: {{${m[1]}}} is not passed (${sentence[0]})`);
  }
  return sentence[1].replace(/\{\{(\w+)\}\}/g, (whole, name) => String(sentence[2][name]));
};

test('every text is in the English translation file, word for word', () => {
  const unquote = (text) => text.replace(/\\(.)/g, (whole, c) => (c === 'n' ? '\n' : c));
  const string = "'((?:[^'\\\\]|\\\\.)*)'";
  let checked = 0;
  const logic = read('./certificates.js');
  for (const m of logic.matchAll(new RegExp("\\bs\\(\\s*'([^']+)'\\s*,\\s*" + string, 'gs'))) {
    assert.equal(translations[m[1]], unquote(m[2]), 'translation.json: ' + m[1]);
    checked += 1;
  }
  for (const [code, entry] of Object.entries(FAILURES)) {
    assert.equal(translations[entry[0] + '.what'], entry[1], code);
    assert.equal(translations[entry[0] + '.todo'], entry[2], code);
    checked += 2;
  }
  for (const file of ['../components/certificates.jsx', '../pages/config/configZones.jsx', '../pages/config/configHTTPS.jsx']) {
    const own = [...read(file).matchAll(new RegExp("\\bt\\(\\s*'(mgmt\\.certs\\.[^']+)'\\s*,\\s*" + string, 'gs'))];
    assert.ok(own.length > 0, file + ': no text found');
    for (const m of own) {
      assert.equal(translations[m[1]], unquote(m[2]), `translation.json: ${m[1]} (${file})`);
      checked += 1;
    }
  }
  for (const m of read('./activity.js').matchAll(new RegExp("\\['(activity\\.certificates\\.[^']+)',\\s*" + string, 'g'))) {
    assert.equal(translations[m[1]], unquote(m[2]), m[1]);
    checked += 1;
  }
  for (const key of ['title.certificateExpiring', 'message.certificateExpiring', 'title.certificateUntrusted', 'message.certificateUntrusted']) {
    assert.ok((translations['header.notification.' + key] || '').length > 10, key);
  }
  // the server's two notifications are raised under these keys (src/certs/notify.go)
  const server = read('../../../src/certs/notify.go');
  for (const key of ['header.notification.title.certificateExpiring', 'header.notification.message.certificateExpiring', 'header.notification.title.certificateUntrusted', 'header.notification.message.certificateUntrusted']) {
    assert.ok(server.includes('"' + key + '"'), key + ' is not what the server raises');
  }
  assert.match(translations['header.notification.message.certificateExpiring'], /\{\{Vars\}\}/);
  assert.match(translations['header.notification.message.certificateUntrusted'], /\{\{Vars\}\}/);
  assert.ok(checked > 150, `only ${checked} texts checked`);
});

test('every reason the server can give for a failed order has words and a next step', () => {
  const server = read('../../../src/certs/failure.go');
  const codes = [...server.matchAll(/^\tFail\w+\s+= "(\w+)"/gm)].map((m) => m[1]);
  assert.ok(codes.length >= 14, 'the codes of failure.go were not found');
  assert.deepEqual([...FAILURE_CODES].sort(), [...codes].sort());
  for (const code of codes) {
    const { what, todo } = failureSentences(code);
    assert.ok(complete(what, code).length > 15, code);
    assert.ok(complete(todo, code).length > 15, code);
    assert.notEqual(what[1], todo[1]);
    // nothing tells the admin to open or forward port 80 or 443
    assert.doesNotMatch(what[1] + todo[1], /port (80|443)|forward/i, code);
  }
  assert.deepEqual(failureSentences('something new'), failureSentences('other'));
  assert.match(failureSentences('dnsCredentials').todo[1], /Renew now/);
  assert.match(failureSentences('rateLimited').todo[1], /Nothing to do/);
  assert.match(failureSentences('caa').todo[1], /letsencrypt\.org/);
});

const acme = (more) => ({ kind: 'acme', method: 'dns', provider: 'cloudflare', present: true, trust: 'public', issuer: "Let's Encrypt (E7)",
  names: ['example.org', '*.example.org'], uncovered: [], notBefore: days(-10), notAfter: days(80), renewAt: days(50), renewSource: 'lifetime',
  expired: false, warn: false, running: false, canRenew: true, ...more });

test('the chip of a domain says the state that matters most', () => {
  const state = (cert) => { const s = certificateState(cert); complete(s.label, 'state'); return s.level + ': ' + s.label[1]; };
  assert.equal(state(acme()), 'success: Trusted by browsers');
  assert.equal(state(acme({ uncovered: ['a.b.example.org'] })), 'warning: Trusted, some names not covered');
  assert.equal(state(acme({ warn: true })), 'warning: Trusted, renewal overdue');
  assert.equal(state(acme({ expired: true, warn: true })), 'error: Expired');
  assert.equal(state(acme({ trust: 'test' })), 'warning: Not trusted by browsers');
  assert.equal(state(acme({ trust: 'staging' })), 'warning: Not trusted by browsers');
  assert.equal(state(acme({ running: true })), 'info: Getting the certificate…');
  assert.equal(state(acme({ present: false })), 'warning: No certificate yet');
  assert.equal(state(acme({ present: false, last: { ok: false, code: 'dnsCredentials' } })), 'error: No certificate: the order failed');
  assert.equal(state(acme({ present: false, method: 'http', blocked: 'noHTTPChallenge' })), 'warning: Needs DNS access');
  assert.equal(state({ kind: 'selfsigned', present: true, trust: 'private' }), 'warning: Not trusted by browsers');
  assert.equal(state({ kind: 'provided', present: true, trust: 'public', uncovered: [] }), 'success: Trusted by browsers');
  assert.equal(state({ kind: 'none' }), 'default: Plain HTTP');
});

test('who issued it, in words', () => {
  assert.match(complete(issuerSentence(acme()), 'issuer'), /^Issued by Let's Encrypt \(E7\), an authority every browser trusts\.$/);
  assert.match(complete(issuerSentence(acme({ trust: 'staging' })), 'issuer'), /staging service.*no browser trusts/);
  assert.match(complete(issuerSentence(acme({ trust: 'test' })), 'issuer'), /test authority/);
  assert.match(complete(issuerSentence(acme({ trust: 'private', issuer: 'Somebody' })), 'issuer'), /Somebody, which browsers do not know/);
  assert.match(complete(issuerSentence({ kind: 'selfsigned', issuer: 'Cosmos Root CA x', trust: 'private' }), 'issuer'), /this server itself/);
  assert.equal(caNotice({ kind: 'production' }), null);
  assert.match(complete(caNotice({ kind: 'staging' }), 'ca'), /ACME_STAGING/);
  assert.match(complete(caNotice({ kind: 'test' }), 'ca'), /test authority/);
});

test('how a domain gets its certificate', () => {
  assert.match(complete(methodSentence(acme()), 'method'), /writing a record in its DNS \(cloudflare\)\. No port has to be open/);
  assert.match(complete(methodSentence(acme({ method: 'delegated', via: 'example.org', provider: '' })), 'method'), /written in the DNS of example\.org/);
  assert.match(complete(methodSentence(acme({ method: 'http', blocked: 'noHTTPChallenge' })), 'method'), /Nothing can be ordered.*Add another domain/);
  assert.match(complete(methodSentence(acme({ method: 'http' })), 'method'), /port 80 or 443/);
  assert.match(complete(methodSentence({ kind: 'provided' }), 'method'), /you provided/);
  assert.match(complete(methodSentence({ kind: 'selfsigned' }), 'method'), /signs itself/);
  assert.match(complete(methodSentence({ kind: 'none' }), 'method'), /plain HTTP/);
});

test('until when, and when it is renewed', () => {
  const text = (cert) => validitySentences(cert, now, format).map((sentence) => complete(sentence, 'validity')).join(' ');
  assert.equal(text(acme()), 'Valid until 2027-02-02 12:00. Cosmos renews it by itself around 2027-01-03 12:00, with a third of its lifetime left.');
  assert.match(text(acme({ renewSource: 'ca' })), /the time Let's Encrypt asked for/);
  assert.match(text(acme({ renewAt: days(-1) })), /due for renewal since 2026-11-13 12:00: Cosmos orders it at its next check/);
  assert.match(text(acme({ renewAt: days(-1), wait: { reason: 'backoff', until: days(0.02) } })), /the last order failed, see below/);
  assert.match(text(acme({ renewAt: days(-1), running: true })), /being renewed now/);
  assert.match(text(acme({ expired: true, notAfter: days(-2), renewAt: days(-30) })), /^It ended on 2026-11-12 12:00: browsers refuse it\./);
  assert.match(text(acme({ method: 'http', blocked: 'noHTTPChallenge' })), /cannot be renewed the way the domain is set up/);
  // a certificate Cosmos does not order has an end and no renewal
  assert.equal(text({ kind: 'provided', present: true, notAfter: days(80) }), 'Valid until 2027-02-02 12:00.');
  assert.equal(text(acme({ present: false })), '');

  assert.equal(complete(howFar(days(12.5), now), 'far'), 'in 12 days');
  assert.equal(complete(howFar(days(0.5), now), 'far'), 'in 12 hours');
  assert.equal(complete(howFar(days(-0.01), now), 'far'), '1 hours ago');
  assert.equal(complete(howFar(days(-3.2), now), 'far'), '3 days ago');
});

test('why nothing is ordered right now, and why "Renew now" is off', () => {
  assert.equal(waitSentence(null, format), null);
  for (const reason of ['rateLimited', 'daily', 'weekly', 'sameNames', 'backoff', 'something new']) {
    assert.match(complete(waitSentence({ reason, until: days(1) }, format), reason), /2026-11-15 12:00/);
  }
  assert.match(waitSentence({ reason: 'backoff', until: days(1) }, format)[1], /"Renew now" does not wait/);
  assert.equal(renewBlockedSentence(acme()), null);
  for (const reason of ['running', 'needsRestart', 'rateLimited', 'daily', 'weekly', 'sameNames', 'whatever']) {
    assert.ok(complete(renewBlockedSentence(acme({ canRenew: false, renewBlocked: reason })), reason).length > 15, reason);
  }
  // said elsewhere on the card, not twice
  assert.equal(renewBlockedSentence({ canRenew: false, renewBlocked: 'notOrdered' }), null);
  assert.equal(renewBlockedSentence({ canRenew: false, renewBlocked: 'noHTTPChallenge' }), null);
});

test('an address: the words are the reachability check\'s own', () => {
  const direct = hostState({ host: 'app.example.org', trusted: true, shielded: false, until: days(80) }, format);
  assert.equal(direct.level, 'ok');
  assert.equal(complete(direct.sentence, 'host'), "Served directly: a visitor's browser trusts it, until 2027-02-02 12:00.");
  const shielded = hostState({ host: 'films.example.org', trusted: true, shielded: true }, format);
  assert.match(complete(shielded.sentence, 'host'), /^Through Cloudflare: visitors see Cloudflare's certificate/);

  for (const problem of ['selfSigned', 'wrongName', 'expired', 'none']) {
    const view = hostState({ host: 'a.b.example.org', trusted: false, shielded: false, problem }, format);
    assert.equal(view.level, 'problem');
    assert.equal(view.sentence[1], CHECK_TEXT['cert.' + problem][1], problem);
    assert.equal(view.sentence[0], 'mgmt.reach.check.cert.' + problem);
    assert.equal(translations[view.sentence[0]], view.sentence[1]);
    const behind = hostState({ host: 'a.b.example.org', trusted: false, shielded: true, problem }, format);
    assert.match(complete(behind.sentence, 'host'), /Cloudflare, which refuses this certificate/);
    assert.equal(behind.detail[1], CHECK_TEXT['cert.' + problem][1]);
  }
  const plain = hostState({ host: 'x.example.com', trusted: false, problem: 'plainHTTP' }, format);
  assert.equal(plain.sentence[1], CHECK_TEXT['cert.plainHTTP'][1]);
  // the old advice of the chip ("Use Let's Encrypt") is gone: the card says what to do
  assert.doesNotMatch(CHECK_TEXT['cert.selfSigned'][1], /Use Let's Encrypt/);
});

test('which challenges work is said, not offered', () => {
  assert.equal(challengeNote(null), null);
  assert.equal(challengeNote({ http: 'possible', reason: 'ports', setting: '', httpsPort: 443, httpPort: 80 }), null);
  const impossible = challengeNote({ http: 'impossible', reason: 'ports', setting: '', httpsPort: 2053, httpPort: 2052 });
  assert.match(complete(impossible.sentence, 'challenge'), /through its DNS, and in no other way.*ports 2052 and 2053/);
  assert.match(complete(challengeNote({ http: 'impossible', reason: 'admin', setting: 'no' }).sentence, 'challenge'), /You told Cosmos/);
  assert.match(complete(challengeNote({ http: 'possible', reason: 'admin', setting: 'yes' }).sentence, 'challenge'), /when the server starts/);
  assert.deepEqual(CHALLENGE_CHOICES.map((choice) => choice.value), ['', 'no', 'yes']);
  for (const choice of CHALLENGE_CHOICES) {
    complete(choice.label, 'choice');
    assert.ok(complete(choice.description, 'choice').length > 40);
  }
  // the one alternative is described where the choice is made
  assert.match(CHALLENGE_CHOICES[2].description[1], /\.well-known\/acme-challenge/);
});

test('a domain name as it is typed', () => {
  assert.equal(cleanDomain('  HTTPS://Example.NET/path '), 'example.net');
  assert.equal(cleanDomain('example.net.'), 'example.net');
  assert.equal(cleanDomain('example.net:2053'), 'example.net');
  assert.equal(domainProblem('', []), null, 'an empty field is not an error while typing');
  assert.equal(domainProblem('example.net', []), null);
  assert.equal(domainProblem('sub.example-1.co.uk', []), null);
  for (const bad of ['example', 'exa mple.net', '-a.example.net', '192.168.1.10', '*.example.net', 'a..b', 'x'.repeat(260) + '.net']) {
    assert.match(complete(domainProblem(bad, []), bad), /for example example\.net/, bad);
  }
  const zones = [{ Zone: 'example.org', derived: false }, { Zone: 'app.example.info', derived: true }];
  assert.match(complete(domainProblem('Example.org', zones), 'exists'), /already has this domain/);
  assert.equal(domainProblem('app.example.info', zones), null, 'a hostname in use is not a domain that was set up');
});

test('what is proposed for a domain, from where its DNS is', () => {
  assert.equal(proposalFor(null), null);
  const at = (info) => { const p = proposalFor(info); return p.kind + ': ' + complete(p.source, 'proposal'); };
  assert.match(at({ nameservers: ['ada.ns.cloudflare.com'], provider: 'cloudflare' }), /^cloudflare: Its name servers are Cloudflare's \(ada\.ns\.cloudflare\.com\)/);
  assert.match(at({ nameservers: ['a.ns.hetzner.com', 'b.ns.hetzner.de'], provider: 'hetzner', providerName: 'Hetzner' }), /^known: Its name servers are at Hetzner \(a\.ns\.hetzner\.com, b\.ns\.hetzner\.de\)/);
  assert.match(at({ nameservers: ['ns1.example.net'], provider: '' }), /^unknown: Its name servers \(ns1\.example\.net\) are not ones Cosmos recognises/);
  assert.match(at({ nameservers: [], provider: '' }), /^unknown: This domain has no name servers/);
  assert.match(at({ nameservers: [], lookupFailed: true }), /^failed: /);

  const offer = [{ record: '_acme-challenge.example.net', target: 'example-net._acme.example.org', via: 'example.org' }];
  assert.deepEqual(waysFor({ nameservers: ['a.ns.hetzner.com'], provider: 'hetzner', canAnswer: offer }), ['access', 'delegate'], 'a known provider: access first');
  assert.deepEqual(waysFor({ nameservers: ['ns1.example.net'], provider: '', canAnswer: offer }), ['delegate', 'access'], 'an unknown one: the single record first');
  assert.deepEqual(waysFor({ nameservers: ['ns1.example.net'], provider: '', canAnswer: [] }), ['access'], 'no domain to answer in: only access');
  for (const way of Object.values(WAY_TEXT)) {
    complete(way.label, 'way');
    complete(way.description, 'way');
  }
});

test('the settings of a provider: what is asked, what stays behind the door', () => {
  const cloudflare = {
    credentials: [
      { name: 'CF_API_EMAIL', text: 'Account email' }, { name: 'CF_DNS_API_TOKEN', text: 'API token with DNS:Edit permission' },
      { name: 'CLOUDFLARE_EMAIL', text: 'Alias to CF_API_EMAIL' }, { name: 'CLOUDFLARE_DNS_API_TOKEN', text: 'Alias to CF_DNS_API_TOKEN' },
    ],
    optional: [{ name: 'CLOUDFLARE_TTL', text: 'The TTL' }, { name: 'CLOUDFLARE_BASE_URL', text: 'API base URL' }],
  };
  const fields = providerFields(cloudflare);
  assert.deepEqual(fields.main.map((f) => f.name), ['CF_API_EMAIL', 'CF_DNS_API_TOKEN'], 'a second name for the same value is not asked');
  assert.deepEqual(fields.custom.map((f) => f.name), ['CLOUDFLARE_TTL'], 'the address of the provider\'s API is not a setting');
  assert.deepEqual(providerFields(null), { main: [], custom: [] });
  // the real catalogue: every provider asks for something
  const catalogue = JSON.parse(read('../../../src/certs/providers.json'));
  for (const [code, provider] of Object.entries(catalogue)) {
    const asked = providerFields(provider);
    if (provider.credentials.length > 0) assert.ok(asked.main.length > 0, code);
    assert.ok(asked.main.length <= provider.credentials.length, code);
  }
  assert.deepEqual(providerFields(catalogue.hetzner).main.map((f) => f.name), ['HETZNER_API_TOKEN']);
});

test('what is stored for a new domain, and what is missing before it can be', () => {
  const access = zoneToSave({ way: 'access', provider: 'hetzner', variables: { HETZNER_API_TOKEN: ' secret-token ', HETZNER_TTL: '' }, via: 'example.org' });
  assert.equal(access.HTTPSCertificateMode, 'LETSENCRYPT');
  assert.equal(access.DNSChallengeProvider, 'hetzner');
  assert.deepEqual(access.DNSChallengeConfig, { HETZNER_API_TOKEN: 'secret-token' });
  assert.equal(access.ChallengeVia, '');
  assert.equal(access.UseWildcardCertificate, true);
  assert.equal(access.ManageRecords, false, 'records are not written unless the admin asks for it');

  const delegated = zoneToSave({ way: 'delegate', provider: 'hetzner', variables: { HETZNER_API_TOKEN: 'x' }, via: 'example.org' });
  assert.equal(delegated.ChallengeVia, 'example.org');
  assert.equal(delegated.DNSChallengeProvider, '');
  assert.deepEqual(delegated.DNSChallengeConfig, {}, 'a secret typed for the other way is not stored');
  assert.equal(delegated.UseWildcardCertificate, true);

  const fields = [{ name: 'HETZNER_API_TOKEN' }];
  const missing = (state) => { const m = missingForSave(state); return m ? complete(m, 'missing') : null; };
  assert.match(missing({ way: 'access', provider: '', fields, variables: {} }), /Choose the DNS provider/);
  assert.match(missing({ way: 'access', provider: 'hetzner', fields, variables: { HETZNER_API_TOKEN: '  ' } }), /Enter the token or key/);
  assert.equal(missing({ way: 'access', provider: 'hetzner', fields, variables: { HETZNER_API_TOKEN: 'x' } }), null);
  assert.match(missing({ way: 'delegate', via: '' }), /Choose the domain/);
  assert.match(missing({ way: 'delegate', via: 'example.org', checked: false }), /Check the record/);
  assert.equal(missing({ way: 'delegate', via: 'example.org', checked: true }), null);
});

test('the record of a delegated proof: what the check found', () => {
  const record = { record: '_acme-challenge.example.net', target: 'example-net._acme.example.org' };
  const text = (check) => { const r = delegationText({ ...record, ...check }); return (r.ok ? 'ok: ' : 'no: ') + complete(r.sentence, check.state); };
  assert.equal(text({ state: 'ok', found: 'example-net._acme.example.org' }), 'ok: The record is there: _acme-challenge.example.net points at example-net._acme.example.org.');
  assert.match(text({ state: 'missing' }), /^no: The record is not visible yet\. A new record can take a few minutes/);
  assert.match(text({ state: 'elsewhere', found: 'other.example.com' }), /^no: .*points at other\.example\.com\. It has to point at example-net\._acme\.example\.org\./);
  assert.match(text({ state: 'loop', found: 'x' }), /^no: .*never end/);
  assert.match(text({ state: 'failed' }), /^no: The record could not be looked up/);
  assert.match(text({ state: 'something new' }), /^no: /);
  assert.equal(delegationText(null).ok, false);
});

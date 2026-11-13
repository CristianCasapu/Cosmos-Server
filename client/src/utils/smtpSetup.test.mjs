// Fork addition: tests of smtpSetup.js. Run with: node --test "client/src/utils/*.test.mjs"
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  splitSender, joinSender, isEmailAddress, emailDomain,
  CONNECTION_CHOICES, connectionChoice, applyConnectionChoice, securityOf, connectionInWords,
  fieldProblems, emailSettingsUnsaved, passwordNeededAgain,
  providerById, providerForHost, providerNotes, providerUnusable, serviceChoices, serviceToValues,
  candidateToValues, proposedCandidate, candidateInUse, sourceText, checkText, nothingFoundText,
  adviceFor, STEP_LABELS, allTexts,
} from './smtpSetup.js';
import { applyProposal } from './proposals.js';

const providers = JSON.parse(readFileSync(new URL('../../../src/maildetect/providers.json', import.meta.url), 'utf8')).providers;
const translations = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));

test('the sender is a name and an address', () => {
  assert.deepEqual(splitSender('cosmos@example.com'), { name: '', address: 'cosmos@example.com' });
  assert.deepEqual(splitSender('Cosmos at home <cosmos@example.com>'), { name: 'Cosmos at home', address: 'cosmos@example.com' });
  assert.deepEqual(splitSender('"Cosmos, home" <cosmos@example.com>'), { name: 'Cosmos, home', address: 'cosmos@example.com' });
  assert.deepEqual(splitSender('"A \\"quoted\\" name" <a@example.com>'), { name: 'A "quoted" name', address: 'a@example.com' });
  assert.deepEqual(splitSender(' <cosmos@example.com> '), { name: '', address: 'cosmos@example.com' });
  assert.deepEqual(splitSender(''), { name: '', address: '' });
  assert.deepEqual(splitSender(undefined), { name: '', address: '' });

  assert.equal(joinSender('', 'cosmos@example.com'), 'cosmos@example.com');
  assert.equal(joinSender('Cosmos at home', ' cosmos@example.com '), 'Cosmos at home <cosmos@example.com>');
  assert.equal(joinSender('Cosmos, home', 'cosmos@example.com'), '"Cosmos, home" <cosmos@example.com>');
  assert.equal(joinSender('A "quoted" name', 'a@example.com'), '"A \\"quoted\\" name" <a@example.com>');
  assert.equal(joinSender('Căsuța mea', 'a@example.com'), 'Căsuța mea <a@example.com>');
  // nothing typed into the name can start a new header
  assert.equal(joinSender('Evil\r\nBcc: x@example.com', 'a@example.com'), '"Evil Bcc: x@example.com" <a@example.com>');
  assert.equal(joinSender('Name', 'a@example.com>\r\n<b@example.com'), 'Name <a@example.comb@example.com>');

  for (const [name, address] of [['', 'a@example.com'], ['Cosmos', 'a@example.com'], ['Cosmos, "home" (1)', 'a@example.com'], ['a\\b', 'a@example.com']]) {
    assert.deepEqual(splitSender(joinSender(name, address)), { name, address }, `round trip of ${name}`);
  }
});

test('what counts as an address, and the domain settings are looked up for', () => {
  for (const good of ['me@example.com', 'first.last+tag@sub.example.co.uk', 'me@mailserver']) assert.equal(isEmailAddress(good), true, good);
  for (const bad of ['', 'me', 'me@', '@example.com', 'a b@example.com', 'a@b@c', 'Me <me@example.com>', 'a@example.com, b@example.com']) assert.equal(isEmailAddress(bad), false, bad);

  assert.equal(emailDomain(' Me@Example.COM '), 'example.com');
  assert.equal(emailDomain('me@bücher.example'), 'bücher.example');
  for (const none of ['me@mailserver', 'me@localhost', 'me@127.0.0.1', 'me@10.0.0.1', 'me@example..com', 'me@-a.example.com', 'nobody', '']) {
    assert.equal(emailDomain(none), '', none);
  }
});

test('port and encryption are one choice', () => {
  assert.equal(connectionChoice('465', true), '465-tls');
  assert.equal(connectionChoice(465, true), '465-tls');
  assert.equal(connectionChoice(' 587 ', true), '587-starttls');
  assert.equal(connectionChoice('25', true), '25-starttls');
  // every pair that is not a named one is "Other"
  assert.equal(connectionChoice('465', false), 'other');
  assert.equal(connectionChoice('587', false), 'other');
  assert.equal(connectionChoice('25', false), 'other');
  assert.equal(connectionChoice('2525', true), 'other');
  assert.equal(connectionChoice('', false), 'other');
  // "Other" stays open while the admin types a port that happens to be a named one
  assert.equal(connectionChoice('587', true, true), 'other');

  // choosing one sets both values, consistently
  assert.deepEqual(applyConnectionChoice('465-tls', '25', false), { Port: '465', UseTLS: true });
  assert.deepEqual(applyConnectionChoice('587-starttls', '465', true), { Port: '587', UseTLS: true });
  assert.deepEqual(applyConnectionChoice('25-starttls', '', false), { Port: '25', UseTLS: true });
  assert.deepEqual(applyConnectionChoice('other', ' 2525 ', true), { Port: '2525', UseTLS: true });
  for (const choice of CONNECTION_CHOICES.filter((c) => c.value !== 'other')) {
    const pair = applyConnectionChoice(choice.value, '1', false);
    assert.equal(connectionChoice(pair.Port, pair.UseTLS), choice.value);
  }

  assert.equal(securityOf('465', true), 'tls');
  assert.equal(securityOf('587', true), 'starttls');
  assert.equal(securityOf('2525', true), 'starttls');
  assert.equal(securityOf('465', false), 'none');
  assert.match(connectionInWords('465', true)[1], /from the start/);
  assert.match(connectionInWords('587', true)[1], /STARTTLS/);
  assert.match(connectionInWords('25', false)[1], /not encrypted/);
});

test('the form marks what the server would refuse', () => {
  const base = { Email_Enabled: true, Email_Host: 'smtp.example.com', Email_Port: '587', Email_UseTLS: true, Email_From: 'a@example.com' };
  assert.deepEqual(fieldProblems(base), {});
  assert.ok(fieldProblems({ ...base, Email_Port: 'smtp' }).port);
  assert.ok(fieldProblems({ ...base, Email_Port: '70000' }).port);
  assert.ok(fieldProblems({ ...base, Email_Port: '0587' }).port);
  assert.ok(fieldProblems({ ...base, Email_Host: 'https://smtp.example.com' }).host);
  assert.ok(fieldProblems({ ...base, Email_Host: 'smtp example.com' }).host);
  assert.ok(fieldProblems({ ...base, Email_From: 'Cosmos <not an address>' }).address);
  assert.ok(fieldProblems({ ...base, Email_Port: '465', Email_UseTLS: false }).encryption);
  // a masked host is not the admin's mistake, and nothing is marked while email is off
  assert.deepEqual(fieldProblems({ ...base, Email_Host: '***' }), {});
  assert.deepEqual(fieldProblems({ ...base, Email_Enabled: false, Email_Port: 'x' }), {});
  // empty fields are not errors while typing: the check and the save name them
  assert.deepEqual(fieldProblems({ Email_Enabled: true }), {});
});

test('saved or not, and when the password has to be typed again', () => {
  const saved = { Enabled: true, Host: 'smtp.example.com', Port: '587', Username: 'a@example.com', Password: '***', From: 'a@example.com', UseTLS: true, AllowInsecureTLS: false, NotifyLogin: false };
  const form = { Email_Enabled: true, Email_Host: 'smtp.example.com', Email_Port: 587, Email_Username: 'a@example.com', Email_Password: '***', Email_From: 'a@example.com', Email_UseTLS: true, Email_AllowInsecureTLS: false, Email_NotifyLogin: false };
  assert.equal(emailSettingsUnsaved(form, saved), false);
  assert.equal(emailSettingsUnsaved({ ...form, Email_Host: ' smtp.example.com ' }, saved), false);
  for (const change of [{ Email_Host: 'x.example.com' }, { Email_Port: '465' }, { Email_Password: 'new' }, { Email_Password: '' }, { Email_NotifyLogin: true }, { Email_From: 'Cosmos <a@example.com>' }, { Email_Enabled: false }]) {
    assert.equal(emailSettingsUnsaved({ ...form, ...change }, saved), true, JSON.stringify(change));
  }
  // settings never saved before
  assert.equal(emailSettingsUnsaved({ Email_Enabled: false }, {}), false);
  assert.equal(emailSettingsUnsaved({ Email_Enabled: true }, undefined), true);

  assert.equal(passwordNeededAgain(form, saved), false);
  for (const change of [{ Email_Host: 'x.example.com' }, { Email_Port: '465' }, { Email_Username: 'b@example.com' }, { Email_UseTLS: false }, { Email_AllowInsecureTLS: true }]) {
    assert.equal(passwordNeededAgain({ ...form, ...change }, saved), true, JSON.stringify(change));
  }
  // not when what changes is no part of the connection, when a password was typed, or when none is stored
  assert.equal(passwordNeededAgain({ ...form, Email_From: 'b@example.com', Email_NotifyLogin: true }, saved), false);
  assert.equal(passwordNeededAgain({ ...form, Email_Host: 'x.example.com', Email_Password: 'typed' }, saved), false);
  assert.equal(passwordNeededAgain({ ...form, Email_Host: 'x.example.com', Email_Password: '' }, { ...saved, Password: '' }), false);
  assert.equal(passwordNeededAgain({ ...form, Email_AllowInsecureTLS: false }, { ...saved, AllowInsecureTLS: true }), false);
});

test('the provider table has what the form needs', () => {
  assert.ok(providers.length >= 20);
  for (const provider of providers) {
    for (const [key] of providerNotes(provider)) assert.ok(key.startsWith('mgmt.config.email.note.'), provider.id);
    assert.equal(providerNotes(provider).length, (provider.notes || []).length, `${provider.id}: a note without a text`);
    for (const [, text, vars] of providerNotes(provider)) {
      // every variable of a text has a value
      for (const m of text.matchAll(/\{\{(\w+)\}\}/g)) assert.ok(vars[m[1]], `${provider.id}: {{${m[1]}}} is empty in "${text}"`);
    }
  }
});

test('hints are chosen by the provider found, or by the server typed', () => {
  const gmail = providerById(providers, 'gmail');
  assert.match(providerNotes(gmail)[0][1], /app password at \{\{url\}\}/);
  assert.equal(providerNotes(gmail)[0][2].url, 'https://myaccount.google.com/apppasswords');
  assert.equal(providerById(providers, 'nothing'), null);
  assert.deepEqual(providerNotes(null), []);

  assert.equal(providerForHost(providers, 'smtp.gmail.com').id, 'gmail');
  assert.equal(providerForHost(providers, ' SMTP.Office365.com ').id, 'microsoft365');
  assert.equal(providerForHost(providers, 'smtp.sendgrid.net').id, 'sendgrid');
  assert.equal(providerForHost(providers, 'email-smtp.eu-west-1.amazonaws.com').id, 'ses');
  assert.equal(providerForHost(providers, 'smtppro.zoho.eu').id, 'zoho-org');
  assert.equal(providerForHost(providers, 'smtp.zoho.eu').id, 'zoho');
  // only whole labels count, and an unknown or empty name has no hints
  assert.equal(providerForHost(providers, 'evilsmtp.gmail.com.example.org'), null);
  assert.equal(providerForHost(providers, 'notsmtp.sendgrid.net.attacker.example'), null);
  assert.equal(providerForHost(providers, 'mail.example.org'), null);
  assert.equal(providerForHost(providers, ''), null);

  assert.equal(providerUnusable(providerById(providers, 'outlook')), true);
  assert.equal(providerUnusable(gmail), false);
  assert.match(providerNotes(providerById(providers, 'sendgrid'))[0][1], /always the word \{\{user\}\}/);
  assert.equal(providerNotes(providerById(providers, 'sendgrid'))[0][2].user, 'apikey');
  // a provider without an address for its password gets the sentence without one
  assert.doesNotMatch(providerNotes(providerById(providers, 'gmx'))[0][1], /\{\{url\}\}/);
});

test('a candidate becomes form values', () => {
  assert.deepEqual(candidateToValues({ host: 'smtp.gmail.com', port: 465, security: 'tls', provider: 'gmail' }, 'me@gmail.com', providers),
    { Email_Host: 'smtp.gmail.com', Email_Port: '465', Email_UseTLS: true, Email_Username: 'me@gmail.com' });
  assert.deepEqual(candidateToValues({ host: 'mail.example.org', port: 587, security: 'starttls' }, ' me@example.org ', providers),
    { Email_Host: 'mail.example.org', Email_Port: '587', Email_UseTLS: true, Email_Username: 'me@example.org' });
  assert.deepEqual(candidateToValues({ host: 'mail.example.org', port: 25, security: 'none' }, '', providers),
    { Email_Host: 'mail.example.org', Email_Port: '25', Email_UseTLS: false });

  // services chosen by hand: a fixed user name is filled in, credentials of their own are left to type
  assert.deepEqual(serviceToValues(providerById(providers, 'sendgrid'), 'me@example.org'),
    { Email_Host: 'smtp.sendgrid.net', Email_Port: '587', Email_UseTLS: true, Email_Username: 'apikey' });
  assert.deepEqual(serviceToValues(providerById(providers, 'brevo'), 'me@example.org'),
    { Email_Host: 'smtp-relay.brevo.com', Email_Port: '587', Email_UseTLS: true });
  assert.equal(serviceToValues(providerById(providers, 'fastmail'), 'me@example.org').Email_Username, 'me@example.org');

  const names = serviceChoices(providers).map((p) => p.name);
  assert.ok(names.includes('Gmail') && names.includes('SendGrid') && names.includes('Amazon SES'));
  assert.ok(!names.includes('Outlook.com'), 'a service Cosmos cannot use is not offered');
  assert.equal(names.length, new Set(names).size);
  assert.deepEqual(names, [...names].sort((a, b) => a.localeCompare(b)));
});

test('the proposal fills the form without replacing what was typed', () => {
  const units = [['Email_Host', 'Email_Port', 'Email_UseTLS'], ['Email_Username']];
  const candidate = { host: 'smtp.gmail.com', port: 465, security: 'tls', provider: 'gmail' };
  const empty = { Email_Host: '', Email_Port: '', Email_UseTLS: false, Email_Username: '' };
  const filled = applyProposal(empty, candidateToValues(candidate, 'me@gmail.com', providers), {}, units);
  assert.deepEqual(filled.values, { Email_Host: 'smtp.gmail.com', Email_Port: '465', Email_UseTLS: true, Email_Username: 'me@gmail.com' });

  const typed = { Email_Host: 'smtp-relay.gmail.com', Email_Port: '587', Email_UseTLS: true, Email_Username: '' };
  const kept = applyProposal(typed, candidateToValues(candidate, 'me@gmail.com', providers), {}, units);
  assert.deepEqual(kept.values, { Email_Username: 'me@gmail.com' });
  assert.deepEqual(kept.held, ['Email_Host', 'Email_Port']);
});

test('which candidate is proposed, and how it is described', () => {
  const result = {
    domain: 'example.org', mx: 'found', candidates: [
      { host: 'here.example.org', port: 587, security: 'starttls', source: 'guess', check: 'local' },
      { host: 'odd.example.org', port: 8587, security: 'starttls', source: 'srv', check: 'not-checked' },
      { host: 'mail.example.org', port: 587, security: 'starttls', source: 'guess', check: 'ok', cert: 'ok', auth: ['PLAIN'] },
    ],
  };
  assert.equal(proposedCandidate(result).host, 'mail.example.org');
  assert.equal(proposedCandidate({ candidates: [result.candidates[0]] }), null);
  assert.equal(proposedCandidate(null), null);
  // a known provider is proposed even when it could not be reached from here
  assert.equal(proposedCandidate({ candidates: [{ host: 'smtp.gmail.com', port: 465, source: 'provider', check: 'no-answer' }] }).host, 'smtp.gmail.com');

  assert.equal(candidateInUse(result.candidates[2], { Email_Host: ' Mail.Example.org', Email_Port: 587, Email_UseTLS: true }), true);
  assert.equal(candidateInUse(result.candidates[2], { Email_Host: 'mail.example.org', Email_Port: '465', Email_UseTLS: true }), false);

  assert.equal(sourceText({ source: 'provider', provider: 'gmail' }, result, providers)[2].name, 'Gmail');
  assert.match(sourceText({ source: 'mx-provider', provider: 'google-workspace' }, result, providers)[1], /MX record/);
  assert.match(sourceText({ source: 'srv' }, result, providers)[1], /SRV record/);
  assert.match(sourceText({ source: 'guess' }, result, providers)[1], /usual name/);

  assert.equal(checkText({ check: 'ok', cert: 'ok', port: 465 }).severity, 'success');
  assert.equal(checkText({ check: 'ok', cert: 'untrusted', port: 465 }).severity, 'warning');
  assert.match(checkText({ check: 'ok', cert: 'untrusted', port: 465 }).text[1], /cannot be verified/);
  assert.match(checkText({ check: 'ok', cert: 'ok', port: 587, auth: ['XOAUTH2'] }).text[1], /no sign-in with a password/);
  assert.equal(checkText({ check: 'ok', cert: 'ok', port: 587, auth: ['LOGIN', 'XOAUTH2'] }).severity, 'success');
  assert.match(checkText({ check: 'no-answer', port: 465 }).text[1], /blocked/);
  assert.match(checkText({ check: 'local', port: 587 }).text[1], /custom settings/);

  assert.match(nothingFoundText({ domain: 'gmial.example', mx: 'none' })[1], /typing mistake/);
  assert.match(nothingFoundText({ domain: 'example.org', mx: 'found' })[1], /custom settings/);
});

test('every failure has a next step that fits it', () => {
  const gmail = providerById(providers, 'gmail');
  const fail = (code) => ({ name: 'x', ok: false, code });
  assert.equal(adviceFor({ name: 'auth', ok: true }), null);
  assert.equal(adviceFor(null), null);

  // wrong password: at a provider that wants an app password, say that
  assert.match(adviceFor(fail('auth-rejected'), { port: '465', provider: gmail })[1], /app password at \{\{url\}\}/);
  assert.equal(adviceFor(fail('auth-rejected'), { port: '465', provider: gmail })[2].url, 'https://myaccount.google.com/apppasswords');
  assert.match(adviceFor(fail('auth-rejected'), { port: '465' })[1], /user name or the password/);
  assert.match(adviceFor(fail('auth-app-password'), { port: '465' })[1], /app password/);
  assert.match(adviceFor(fail('auth-disabled'), {})[1], /Authenticated SMTP/);
  // a blocked port: name the other one
  assert.equal(adviceFor(fail('timeout'), { port: '465' })[2].other, '587');
  assert.equal(adviceFor(fail('timeout'), { port: '587' })[2].other, '465');
  assert.match(adviceFor(fail('timeout'), { port: '25' })[1], /587 or 465/);
  assert.match(adviceFor(fail('cert-untrusted'), {})[1], /Accept a certificate that cannot be verified/);
  assert.match(adviceFor(fail('tls-expected'), { port: '465' })[1], /from the start/);
  assert.match(adviceFor(fail('no-starttls'), { port: '587' })[1], /465/);

  const codes = ['host-not-found', 'timeout', 'refused', 'unreachable', 'tls-expected', 'not-tls', 'connect-failed', 'no-starttls',
    'cert-untrusted', 'cert-name', 'cert-expired', 'tls-failed', 'auth-rejected', 'auth-app-password', 'auth-disabled',
    'auth-needs-tls', 'auth-no-method', 'auth-failed', 'try-later', 'needs-tls', 'needs-sign-in', 'sender-rejected',
    'recipient-rejected', 'send-rejected'];
  // the codes of src/utils/fork_email_failure.go, each with a text of its own
  const goCodes = [...readFileSync(new URL('../../../src/utils/fork_email_failure.go', import.meta.url), 'utf8').matchAll(/EmailFail\w+\s+= "([a-z0-9-]+)"/g)].map((m) => m[1]);
  assert.deepEqual([...goCodes].sort(), [...codes].sort());
  const unknown = adviceFor(fail('something-new'), { port: '587' })[0];
  const seen = new Set();
  for (const code of codes) {
    for (const provider of [null, gmail]) {
      const [key, text, vars] = adviceFor(fail(code), { port: '587', provider });
      assert.notEqual(key, unknown, code);
      seen.add(key);
      for (const m of text.matchAll(/\{\{(\w+)\}\}/g)) assert.ok(vars[m[1]], `${code}: {{${m[1]}}} is empty`);
    }
  }
  assert.ok(seen.size >= codes.length);
  assert.deepEqual(Object.keys(STEP_LABELS), ['connect', 'tls', 'auth', 'sender', 'recipient', 'send']);
});

test('every text of the form is in the English translation file, word for word', () => {
  const pairs = [...allTexts()];
  // the texts the functions return for each input they know
  const gmail = providerById(providers, 'gmail');
  const result = { domain: 'example.org' };
  for (const source of ['provider', 'mx-provider', 'srv', 'mx-host', 'guess']) pairs.push(sourceText({ source, provider: 'gmail' }, result, providers));
  for (const check of ['ok', 'no-answer', 'not-smtp', 'no-starttls', 'tls-failed', 'local', 'not-checked']) pairs.push(checkText({ check, cert: 'ok', port: 1 }).text);
  for (const cert of ['untrusted', 'name', 'expired']) pairs.push(checkText({ check: 'ok', cert, port: 1 }).text);
  pairs.push(checkText({ check: 'ok', cert: 'ok', port: 1, auth: ['XOAUTH2'] }).text);
  pairs.push(nothingFoundText({ mx: 'none' }), nothingFoundText({}));
  for (const [p, tls] of [['465', true], ['587', true], ['25', false]]) pairs.push(connectionInWords(p, tls));
  const problems = fieldProblems({ Email_Enabled: true, Email_Host: 'a b', Email_Port: 'x', Email_From: 'x y' });
  pairs.push(problems.host, problems.port, problems.address, fieldProblems({ Email_Enabled: true, Email_Port: '465' }).encryption);
  const source = readFileSync(new URL('./smtpSetup.js', import.meta.url), 'utf8');
  for (const m of source.matchAll(/case '([a-z0-9-]+)':/g)) {
    for (const provider of [null, gmail, { name: 'X', notes: ['appPassword'] }]) {
      for (const port of ['25', '587']) pairs.push(adviceFor({ ok: false, code: m[1] }, { port, provider }));
    }
  }
  pairs.push(adviceFor({ ok: false, code: 'unheard-of' }, {}));

  // and the texts written in the components themselves
  for (const file of ['../pages/config/emailSettings.jsx', '../pages/config/emailTest.jsx', '../components/secretInput.jsx']) {
    const jsx = readFileSync(new URL(file, import.meta.url), 'utf8');
    const found = [...jsx.matchAll(/\bt\(\s*'([^']+)',\s*'((?:[^'\\]|\\.)*)'/g)];
    assert.ok(found.length > 3, file);
    for (const m of found) pairs.push([m[1], m[2].replace(/\\'/g, "'")]);
    // a text without its English words would show its key to the administrator
    assert.equal([...jsx.matchAll(/\bt\(\s*'[^']+'\s*\)/g)].length, 0, `${file}: t() without the English text`);
  }

  assert.ok(pairs.length > 150);
  for (const [key, text] of pairs) {
    assert.equal(translations[key], text, `translation.json: ${key}`);
  }
});

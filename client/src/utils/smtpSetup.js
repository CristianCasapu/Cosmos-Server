// Fork addition: the logic of the email (SMTP) settings form, kept apart from
// the components so that it can be tested with `node --test`: the sender as
// name and address, "port and encryption" as one choice, a detected
// candidate as form values, the hints of a provider, and the next step after
// a failed check. The provider table itself is src/maildetect/providers.json
// (the server embeds the same file); it is passed in, see smtpProviders.js.
//
// Texts are [translation key, English text, variables]: the components pass
// them to t().

export const MASK = '***';

// ---- sender ---------------------------------------------------------------

// "Name <address>" or "address" -> { name, address }
export const splitSender = (from) => {
  const text = String(from || '').trim();
  const m = text.match(/^(?:"((?:[^"\\]|\\.)*)"|([^<"]*?))\s*<([^<>]*)>$/);
  if (!m) {
    return { name: '', address: text };
  }
  const name = m[1] !== undefined ? m[1].replace(/\\(.)/g, '$1') : (m[2] || '');
  return { name: name.trim(), address: m[3].trim() };
};

// The reverse. The name is quoted when it holds a character that would
// otherwise end it; line breaks never get in.
export const joinSender = (name, address) => {
  const cleanAddress = String(address || '').replace(/[\r\n<>]/g, '').trim();
  const cleanName = String(name || '').replace(/[\r\n]+/g, ' ').trim();
  if (!cleanName) {
    return cleanAddress;
  }
  const shown = /[()<>[\]:;@\\,."]/.test(cleanName)
    ? '"' + cleanName.replace(/(["\\])/g, '\\$1') + '"'
    : cleanName;
  return shown + ' <' + cleanAddress + '>';
};

export const isEmailAddress = (text) => /^[^\s@<>"',;]+@[^\s@<>"',;]+$/.test(String(text || '').trim());

// The domain settings can be looked up for: at least two labels, not an IP address.
export const emailDomain = (address) => {
  const text = String(address || '').trim().toLowerCase();
  if (!isEmailAddress(text)) {
    return '';
  }
  const domain = text.slice(text.lastIndexOf('@') + 1).replace(/\.$/, '');
  if (!/^([a-z0-9\u00a1-\uffff]([a-z0-9\u00a1-\uffff-]*[a-z0-9\u00a1-\uffff])?\.)+[a-z\u00a1-\uffff][a-z0-9\u00a1-\uffff-]*$/.test(domain)) {
    return '';
  }
  return domain;
};

// ---- port and encryption: one choice ---------------------------------------

export const CONNECTION_CHOICES = [
  { value: '465-tls', port: '465', useTLS: true, label: ['mgmt.config.email.conn.465', '465 · encrypted from the start (TLS)'] },
  { value: '587-starttls', port: '587', useTLS: true, label: ['mgmt.config.email.conn.587', '587 · encrypted after connecting (STARTTLS)'] },
  { value: '25-starttls', port: '25', useTLS: true, label: ['mgmt.config.email.conn.25', '25 · STARTTLS'] },
  { value: 'other', label: ['mgmt.config.email.conn.other', 'Other'] },
];

// What the pair (port, UseTLS) means to delivery: the server encrypts from
// the start on 465, upgrades with STARTTLS on any other port, or not at all.
export const securityOf = (port, useTLS) => {
  if (!useTLS) return 'none';
  return String(port || '').trim() === '465' ? 'tls' : 'starttls';
};

// The choice that shows the pair. `otherOpen`: the administrator opened
// "Other" (the pair may then still be one of the named ones).
export const connectionChoice = (port, useTLS, otherOpen = false) => {
  if (otherOpen) return 'other';
  const found = CONNECTION_CHOICES.find((choice) => choice.port === String(port || '').trim() && choice.useTLS === !!useTLS);
  return found ? found.value : 'other';
};

// The pair a choice stands for. "Other" keeps what is there.
export const applyConnectionChoice = (choice, port, useTLS) => {
  const found = CONNECTION_CHOICES.find((item) => item.value === choice);
  if (!found || found.port === undefined) {
    return { Port: String(port || '').trim(), UseTLS: !!useTLS };
  }
  return { Port: found.port, UseTLS: found.useTLS };
};

// In "Other" the port is typed and encryption is a second, dependent choice.
// Port 465 with encryption is TLS from the start whatever is picked; without
// encryption it cannot work, which the form says.
export const ENCRYPTION_CHOICES = [
  { value: 'on', label: ['mgmt.config.email.enc.starttls', 'Encrypted (STARTTLS)'] },
  { value: 'off', label: ['mgmt.config.email.enc.none', 'Not encrypted'] },
];

export const connectionInWords = (port, useTLS) => {
  const p = String(port || '').trim();
  switch (securityOf(p, useTLS)) {
    case 'tls':
      return ['mgmt.config.email.conn.words.tls', 'port {{port}}, encrypted from the start (TLS)', { port: p }];
    case 'starttls':
      return ['mgmt.config.email.conn.words.starttls', 'port {{port}}, encrypted after connecting (STARTTLS)', { port: p }];
    default:
      return ['mgmt.config.email.conn.words.none', 'port {{port}}, not encrypted', { port: p }];
  }
};

// ---- what the form checks itself (the server checks the same on save) -------

export const fieldProblems = (values) => {
  const problems = {};
  if (!values.Email_Enabled) {
    return problems;
  }
  const { address } = splitSender(values.Email_From);
  if (address && !isEmailAddress(address)) {
    problems.address = ['mgmt.config.email.problem.address', 'Enter one email address, such as cosmos@example.com'];
  }
  const host = String(values.Email_Host || '');
  if (host && host !== MASK && !/^[A-Za-z0-9._:-]+$/.test(host.trim())) {
    problems.host = ['mgmt.config.email.problem.host', 'Enter the server\'s name only, such as smtp.example.com: no spaces, no https://'];
  }
  const port = String(values.Email_Port || '').trim();
  if (port && !(/^\d{1,5}$/.test(port) && Number(port) >= 1 && Number(port) <= 65535 && String(Number(port)) === port)) {
    problems.port = ['mgmt.config.email.problem.port', 'Enter a port between 1 and 65535, such as 587'];
  }
  if (port === '465' && !values.Email_UseTLS) {
    problems.encryption = ['mgmt.config.email.problem.465', 'Port 465 only works encrypted from the start: choose "465 · encrypted from the start (TLS)".'];
  }
  return problems;
};

// ---- saved or not -----------------------------------------------------------

const emailOf = (values) => ({
  Enabled: !!values.Email_Enabled,
  Host: String(values.Email_Host || '').trim(),
  Port: String(values.Email_Port || '').trim(),
  Username: String(values.Email_Username || '').trim(),
  Password: values.Email_Password || '',
  From: String(values.Email_From || '').trim(),
  UseTLS: !!values.Email_UseTLS,
  AllowInsecureTLS: !!values.Email_AllowInsecureTLS,
  NotifyLogin: !!values.Email_NotifyLogin,
});

const savedOf = (saved) => emailOf({
  Email_Enabled: saved && saved.Enabled,
  Email_Host: saved && saved.Host,
  Email_Port: saved && saved.Port,
  Email_Username: saved && saved.Username,
  Email_Password: saved && saved.Password,
  Email_From: saved && saved.From,
  Email_UseTLS: saved && saved.UseTLS,
  Email_AllowInsecureTLS: saved && saved.AllowInsecureTLS,
  Email_NotifyLogin: saved && saved.NotifyLogin,
});

// True when the form shows email settings that differ from the saved ones.
export const emailSettingsUnsaved = (values, saved) => {
  const a = emailOf(values);
  const b = savedOf(saved);
  return Object.keys(a).some((key) => a[key] !== b[key]);
};

// The server keeps a stored password for the connection it was saved for
// only. True when the form still relies on the stored one ("***") while it
// names another server, port, user or encryption: the password has to be
// typed again, for the check and for the save.
export const passwordNeededAgain = (values, saved) => {
  const a = emailOf(values);
  const b = savedOf(saved);
  if (a.Password !== MASK || b.Password !== MASK) {
    return false;
  }
  return a.Host !== b.Host || a.Port !== b.Port || a.Username !== b.Username || a.UseTLS !== b.UseTLS
    || (a.AllowInsecureTLS && !b.AllowInsecureTLS);
};

// ---- providers --------------------------------------------------------------

const endsWithName = (host, name) => host === name || host.endsWith('.' + name);

export const providerById = (providers, id) => (providers || []).find((provider) => provider.id === id) || null;

// The provider a server name typed by hand belongs to, for its hints.
export const providerForHost = (providers, host) => {
  const name = String(host || '').trim().toLowerCase();
  if (!name) return null;
  let best = null;
  let bestLength = 0;
  (providers || []).forEach((provider) => {
    (provider.hosts || []).forEach((suffix) => {
      if (endsWithName(name, suffix) && suffix.length > bestLength) {
        best = provider;
        bestLength = suffix.length;
      }
    });
  });
  return best;
};

// The services "fill in for a known service" offers: one entry per name.
export const serviceChoices = (providers) => {
  const seen = new Set();
  return (providers || [])
    .filter((provider) => provider.servers && provider.servers.length > 0 && !(provider.notes || []).includes('oauthOnly'))
    .filter((provider) => (seen.has(provider.name) ? false : seen.add(provider.name)))
    .sort((a, b) => a.name.localeCompare(b.name));
};

const NOTE_TEXTS = {
  appPassword: {
    withUrl: ['mgmt.config.email.note.appPassword.url', '{{name}} does not accept the normal password of the account here. Turn on two-step verification, create an app password at {{url}} and enter it as the password.'],
    plain: ['mgmt.config.email.note.appPassword', '{{name}} does not accept the normal password of the account here. Turn on two-step verification, create an app password in the account\'s security settings and enter it as the password.'],
  },
  smtpAuthOff: {
    withUrl: ['mgmt.config.email.note.smtpAuthOff.url', 'Microsoft 365 switches off sign-in with a password for sending mail ("Authenticated SMTP") unless an administrator of the organisation turns it on for this mailbox: {{url}}, then Users > Active users > the user > Mail > Manage email apps. Microsoft has announced the end of this way of signing in; a mail relay service is the lasting choice.'],
    plain: ['mgmt.config.email.note.smtpAuthOff', 'Microsoft 365 switches off sign-in with a password for sending mail ("Authenticated SMTP") unless an administrator of the organisation turns it on for this mailbox. Microsoft has announced the end of this way of signing in; a mail relay service is the lasting choice.'],
  },
  oauthOnly: {
    plain: ['mgmt.config.email.note.oauthOnly', '{{name}} no longer accepts a password from other programs for sending mail, only Microsoft\'s own sign-in window, which Cosmos cannot use. Use another mailbox or a mail relay service.'],
  },
  bridge: {
    plain: ['mgmt.config.email.note.bridge', 'Proton Mail has no ordinary SMTP access for personal plans. Either run Proton Mail Bridge on this server and enter what it shows under custom settings (server 127.0.0.1, port 1025, no encryption, its own user name and password), or, on a business plan, create an SMTP token in Proton\'s settings and enter it as the password.'],
  },
  enableAccess: {
    plain: ['mgmt.config.email.note.enableAccess', '{{name}} accepts other programs only after "POP3 and IMAP access" is switched on in the mailbox settings on its website.'],
  },
  relayCredentials: {
    withUrl: ['mgmt.config.email.note.relayCredentials.url', '{{name}} has a user name and password of its own for sending (SMTP credentials), created at {{url}}. They are not the ones you sign in to its website with.'],
    plain: ['mgmt.config.email.note.relayCredentials', '{{name}} has a user name and password of its own for sending (SMTP credentials), created in its panel. They are not the ones you sign in to its website with.'],
  },
  fixedUser: {
    withUrl: ['mgmt.config.email.note.fixedUser.url', 'At {{name}} the user name is always the word {{user}}, and the password is an API key created at {{url}}.'],
    plain: ['mgmt.config.email.note.fixedUser', 'At {{name}} the user name is always the word {{user}}, and the password is an API key created in its panel.'],
  },
  regionInHost: {
    plain: ['mgmt.config.email.note.regionInHost', 'The server name contains the region of the account (here us-east-1): replace it with yours.'],
  },
  serverVaries: {
    plain: ['mgmt.config.email.note.serverVaries', 'At {{name}} the server name can differ with the country or the plan of the account. If this one does not work, look for "SMTP" or "outgoing server" in the account\'s settings.'],
  },
  verifiedSender: {
    plain: ['mgmt.config.email.note.verifiedSender', 'The sender address must be one you verified at {{name}}, or belong to a domain verified there.'],
  },
};

// The hints of a provider, as texts.
export const providerNotes = (provider) => {
  if (!provider) return [];
  return (provider.notes || []).map((note) => {
    const text = NOTE_TEXTS[note];
    if (!text) return null;
    const chosen = provider.url && text.withUrl ? text.withUrl : text.plain;
    return [chosen[0], chosen[1], { name: provider.name, url: provider.url || '', user: provider.userValue || '' }];
  }).filter(Boolean);
};

// True for a provider Cosmos cannot send through with a password at all.
export const providerUnusable = (provider) => !!provider && (provider.notes || []).includes('oauthOnly');

// ---- a candidate as form values ---------------------------------------------

// The form fields a detected candidate stands for. The user name is part of
// it when it can be known: the address, or the provider's fixed word.
export const candidateToValues = (candidate, address, providers) => {
  const values = {
    Email_Host: candidate.host,
    Email_Port: String(candidate.port),
    Email_UseTLS: candidate.security !== 'none',
  };
  const provider = providerById(providers, candidate.provider);
  if (provider && provider.user === 'fixed') {
    values.Email_Username = provider.userValue;
  } else if (!provider || provider.user !== 'ask') {
    const clean = String(address || '').trim();
    if (clean) values.Email_Username = clean;
  }
  return values;
};

// A provider's first server as form values, for "fill in for a known service".
export const serviceToValues = (provider, address) => candidateToValues(
  { ...provider.servers[0], provider: provider.id }, provider.user === 'address' ? address : '', [provider],
);

// The candidate the form proposes: the first in the server's order that
// could be used. One that points at this server itself, or that was not
// checked, is only listed.
export const proposedCandidate = (result) => {
  const list = (result && result.candidates) || [];
  return list.find((c) => c.check !== 'local' && c.check !== 'not-checked') || null;
};

export const sameCandidate = (a, b) => !!a && !!b && a.host === b.host && a.port === b.port;

// True when the form's connection is the candidate's.
export const candidateInUse = (candidate, values) => !!candidate
  && String(values.Email_Host || '').trim().toLowerCase() === candidate.host
  && String(values.Email_Port || '').trim() === String(candidate.port)
  && !!values.Email_UseTLS === (candidate.security !== 'none');

// Where a candidate came from, in words.
export const sourceText = (candidate, result, providers) => {
  const provider = providerById(providers, candidate.provider);
  const vars = { name: provider ? provider.name : '', domain: (result && result.domain) || '', port: candidate.port };
  switch (candidate.source) {
    case 'provider':
      return ['mgmt.config.email.source.provider', 'Known provider: {{name}}', vars];
    case 'mx-provider':
      return ['mgmt.config.email.source.mxProvider', 'Mail for {{domain}} is handled by {{name}} (the domain\'s MX record says so)', vars];
    case 'srv':
      return ['mgmt.config.email.source.srv', 'Announced by {{domain}} in DNS (SRV record)', vars];
    case 'mx-host':
      return ['mgmt.config.email.source.mxHost', 'The server that receives the mail of {{domain}}', vars];
    default:
      return ['mgmt.config.email.source.guess', 'The usual name for this domain', vars];
  }
};

const passwordSignIn = (candidate) => !candidate.auth || candidate.auth.length === 0
  || candidate.auth.includes('PLAIN') || candidate.auth.includes('LOGIN');

// What the check of a candidate found, in words, with a severity for its mark.
export const checkText = (candidate) => {
  const vars = { port: candidate.port, host: candidate.host };
  switch (candidate.check) {
    case 'ok':
      if (candidate.cert === 'untrusted') {
        return { severity: 'warning', text: ['mgmt.config.email.check.certUntrusted', 'It answered on port {{port}} just now, but its certificate cannot be verified (self-signed, or from an authority this server does not know).', vars] };
      }
      if (candidate.cert === 'name') {
        return { severity: 'warning', text: ['mgmt.config.email.check.certName', 'It answered on port {{port}} just now, but its certificate was issued for another name.', vars] };
      }
      if (candidate.cert === 'expired') {
        return { severity: 'warning', text: ['mgmt.config.email.check.certExpired', 'It answered on port {{port}} just now, but its certificate is out of date.', vars] };
      }
      if (!passwordSignIn(candidate)) {
        return { severity: 'warning', text: ['mgmt.config.email.check.noPassword', 'It answered on port {{port}} just now, but offers no sign-in with a password.', vars] };
      }
      return { severity: 'success', text: ['mgmt.config.email.check.ok', 'It answered on port {{port}} just now, with a certificate that was verified.', vars] };
    case 'no-answer':
      return { severity: 'warning', text: ['mgmt.config.email.check.noAnswer', 'It did not answer on port {{port}} from this server just now: the port may be blocked on the way, or this server has no connection to the internet.', vars] };
    case 'not-smtp':
      return { severity: 'warning', text: ['mgmt.config.email.check.notSmtp', 'Something answered on port {{port}} that is not a mail server.', vars] };
    case 'no-starttls':
      return { severity: 'warning', text: ['mgmt.config.email.check.noStartTLS', 'It answered on port {{port}}, but offers no encryption there.', vars] };
    case 'tls-failed':
      return { severity: 'warning', text: ['mgmt.config.email.check.tlsFailed', 'It answered on port {{port}}, but the encryption could not be set up.', vars] };
    case 'local':
      return { severity: 'info', text: ['mgmt.config.email.check.local', 'This name points to this server itself or to an address that is never contacted automatically. If that is what you want, enter it under custom settings.', vars] };
    default:
      return { severity: 'info', text: ['mgmt.config.email.check.notChecked', 'Not checked (an unusual port, or time ran out).', vars] };
  }
};

// What to say when nothing was found.
export const nothingFoundText = (result) => {
  const vars = { domain: (result && result.domain) || '' };
  if (result && result.mx === 'none') {
    return ['mgmt.config.email.nothing.noMx', 'No mail server is known for {{domain}}: the domain does not exist or receives no mail. Check the address for a typing mistake, or enter the server under custom settings.', vars];
  }
  return ['mgmt.config.email.nothing.unknown', 'No settings were found for {{domain}}. Enter the server under custom settings: the provider\'s help pages list it as "SMTP" or "outgoing mail server".', vars];
};

// ---- the check: steps and what to do next -----------------------------------

export const STEP_LABELS = {
  connect: ['mgmt.config.email.step.connect', 'Reach the mail server'],
  tls: ['mgmt.config.email.step.tls', 'Encrypt the connection'],
  auth: ['mgmt.config.email.step.auth', 'Sign in'],
  sender: ['mgmt.config.email.step.sender', 'Sender accepted'],
  recipient: ['mgmt.config.email.step.recipient', 'Recipient accepted'],
  send: ['mgmt.config.email.step.send', 'Message handed over'],
};

const otherPort = (port) => (String(port) === '465' ? '587' : '465');

// The next step after a failed step. `context`: the form's port, and the
// provider of its server (for "this provider wants an app password").
export const adviceFor = (step, context = {}) => {
  if (!step || step.ok) return null;
  const port = String(context.port || '').trim();
  const provider = context.provider || null;
  const wantsAppPassword = !!provider && (provider.notes || []).includes('appPassword');
  const vars = { port, other: otherPort(port), name: provider ? provider.name : '', url: provider ? (provider.url || '') : '' };
  switch (step.code) {
    case 'host-not-found':
      return ['mgmt.config.email.advice.hostNotFound', 'The server name does not exist. Check its spelling under custom settings.', vars];
    case 'timeout':
      if (port === '25') {
        return ['mgmt.config.email.advice.timeout25', 'Nothing answered on port 25. Most internet providers block it: choose 587 or 465 under custom settings.', vars];
      }
      return ['mgmt.config.email.advice.timeout', 'Nothing answered on port {{port}}. Your internet provider or hosting company may block it, or the server uses the other port: try {{other}} under custom settings.', vars];
    case 'refused':
      return ['mgmt.config.email.advice.refused', 'The server is there, but nothing listens on port {{port}}. Try {{other}} under custom settings.', vars];
    case 'unreachable':
      return ['mgmt.config.email.advice.unreachable', 'This server has no way to reach the mail server. Check the server name and this server\'s network.', vars];
    case 'tls-expected':
      return ['mgmt.config.email.advice.tlsExpected', 'Port 465 expects encryption from the first moment. Choose "465 · encrypted from the start (TLS)".', vars];
    case 'not-tls':
      return ['mgmt.config.email.advice.notTls', 'This port does not start encrypted. Choose "587 · encrypted after connecting (STARTTLS)".', vars];
    case 'connect-failed':
      return ['mgmt.config.email.advice.connect', 'The connection ended before the mail server greeted. Check the server name and the port.', vars];
    case 'no-starttls':
      return ['mgmt.config.email.advice.noStartTLS', 'The server offers no encryption on this port. Try port 465, encrypted from the start. Only for a mail server on your own network: "Other", not encrypted.', vars];
    case 'cert-untrusted':
      return ['mgmt.config.email.advice.certUntrusted', 'The certificate of the mail server cannot be verified. If it is your own server with a self-signed certificate, switch on "Accept a certificate that cannot be verified" under custom settings. With a provider on the internet, stop here: something may be in between.', vars];
    case 'cert-name':
      return ['mgmt.config.email.advice.certName', 'The certificate was issued for another name than the one entered. Use the server name the provider documents; an alias of it does not do.', vars];
    case 'cert-expired':
      return ['mgmt.config.email.advice.certExpired', 'The certificate of the mail server is out of date; whoever runs it has to renew it. Also check the clock of this server.', vars];
    case 'tls-failed':
      return ['mgmt.config.email.advice.tls', 'The encryption could not be set up. Try the other port ({{other}}).', vars];
    case 'auth-app-password':
      return vars.url
        ? ['mgmt.config.email.advice.appPassword.url', 'This provider wants an app password instead of the normal password. Create one at {{url}} and enter it as the password.', vars]
        : ['mgmt.config.email.advice.appPassword', 'This provider wants an app password instead of the normal password. Create one in the account\'s security settings and enter it as the password.', vars];
    case 'auth-rejected':
      if (wantsAppPassword) {
        return vars.url
          ? ['mgmt.config.email.advice.rejectedApp.url', '{{name}} refused the sign-in. It does not take the normal password of the account: create an app password at {{url}} and enter that. Also check the user name: it is the whole email address.', vars]
          : ['mgmt.config.email.advice.rejectedApp', '{{name}} refused the sign-in. It does not take the normal password of the account: create an app password in the account\'s security settings and enter that. Also check the user name: it is the whole email address.', vars];
      }
      return ['mgmt.config.email.advice.rejected', 'The mail server refused the user name or the password. Check both: the user name is usually the whole email address, and the password must be typed exactly.', vars];
    case 'auth-disabled':
      return ['mgmt.config.email.advice.authDisabled', 'Sign-in with a password is switched off for this mailbox ("Authenticated SMTP" at Microsoft 365). An administrator of the organisation has to switch it on for the mailbox; otherwise use another mail service.', vars];
    case 'auth-needs-tls':
      return ['mgmt.config.email.advice.authNeedsTls', 'The password is only sent over an encrypted connection. Choose 465 or 587 with encryption under custom settings.', vars];
    case 'auth-no-method':
      return ['mgmt.config.email.advice.authNoMethod', 'This server takes no password here, only ways of signing in that Cosmos cannot use. Use an app password if the provider offers one, or another mail service.', vars];
    case 'auth-failed':
      return ['mgmt.config.email.advice.auth', 'Signing in failed. Check the user name and the password.', vars];
    case 'try-later':
      return ['mgmt.config.email.advice.tryLater', 'The mail server asks to try again later. Wait a few minutes; if it stays like this, the provider may be limiting the account.', vars];
    case 'needs-tls':
      return ['mgmt.config.email.advice.needsTls', 'The server wants encryption before it takes mail. Choose "587 · encrypted after connecting (STARTTLS)".', vars];
    case 'needs-sign-in':
      return ['mgmt.config.email.advice.needsSignIn', 'The server only takes mail from senders who sign in. Enter the user name and the password.', vars];
    case 'sender-rejected':
      return ['mgmt.config.email.advice.sender', 'The server refused the sender address. It usually has to be the address of the mailbox you sign in with, or one verified at the mail service.', vars];
    case 'recipient-rejected':
      return ['mgmt.config.email.advice.recipient', 'The server refused the recipient. Check the address the test is sent to.', vars];
    case 'send-rejected':
      return ['mgmt.config.email.advice.send', 'The server refused the message itself. Its reason is in the detail.', vars];
    default:
      return ['mgmt.config.email.advice.unknown', 'Check the server name, the port and the sign-in under custom settings.', vars];
  }
};

// Every text above, for the test that the translation file has them all.
export const allTexts = () => {
  const out = [];
  CONNECTION_CHOICES.forEach((choice) => out.push(choice.label));
  ENCRYPTION_CHOICES.forEach((choice) => out.push(choice.label));
  Object.values(STEP_LABELS).forEach((label) => out.push(label));
  Object.values(NOTE_TEXTS).forEach((text) => {
    if (text.withUrl) out.push(text.withUrl);
    out.push(text.plain);
  });
  return out;
};

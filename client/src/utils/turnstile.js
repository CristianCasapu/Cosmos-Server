// Fork addition: the pure logic of Cloudflare Turnstile on the panel's
// public forms (src/turnstile): which request carries which form's token,
// the token store the widget fills and the API client empties, and every
// sentence of the widget and of the settings card. No React, no i18n: texts
// are [translation key, English text, parameters] for t(). Tested by
// turnstile.test.mjs (node --test).

// The one address of the script (Cloudflare: "must be fetched from the
// exact URL", never proxied or cached; the service worker leaves another
// origin alone, see pwaPolicy.js).
export const SCRIPT_URL = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit';
export const TURNSTILE_ORIGIN = 'https://challenges.cloudflare.com';
export const HEADER = 'X-Cosmos-Turnstile';

// the forms, their routes and the action each widget is rendered with (the
// server checks the action that comes back from Cloudflare)
export const FORMS = {
  login: { path: '/cosmos/api/login', action: 'cosmos-login' },
  reset: { path: '/cosmos/api/password-reset', action: 'cosmos-reset' },
  register: { path: '/cosmos/api/register', action: 'cosmos-register' },
  test: { path: '/cosmos/api/turnstile', action: 'cosmos-test' },
};

// formOfRequest names the form whose token a request carries: a POST of one
// of the three public forms, nothing else (the settings card sends its
// token in the body).
export const formOfRequest = (path, method) => {
  if (String(method || 'GET').toUpperCase() !== 'POST') return '';
  const bare = String(path || '').split('?')[0].split('#')[0];
  for (const form of ['login', 'reset', 'register']) {
    if (FORMS[form].path === bare) return form;
  }
  return '';
};

// --- the store: one token per form, at most, until a request takes it

const store = { tokens: {}, listeners: new Set() };

const emit = (event) => {
  store.listeners.forEach((listener) => {
    try { listener(event); } catch (e) { /* a listener never breaks a request */ }
  });
};

export const setToken = (form, token) => {
  if (typeof token === 'string' && token) store.tokens[form] = token;
};
export const clearToken = (form) => { delete store.tokens[form]; };
export const hasToken = (form) => !!store.tokens[form];

// subscribe(fn) returns the function that unsubscribes. Events:
// { type: 'spent', form } and { type: 'refused', form, code }.
export const subscribe = (listener) => {
  store.listeners.add(listener);
  return () => store.listeners.delete(listener);
};

const REFUSAL_CODES = ['TS001', 'TS002', 'TS003', 'TS004'];

// turnstileRequest is the hook of the API client (api/client.ts): for a
// request of a public form it puts that form's token into the headers (the
// token is single use: it leaves the store and the widget is told to get a
// new one), and returns a wrapper for the answer that reports a refusal of
// the check. For any other request it changes nothing.
export const turnstileRequest = (path, options, headers) => {
  const form = formOfRequest(path, options && options.method);
  if (!form) return (answer) => answer;
  const token = store.tokens[form];
  if (token) {
    headers[HEADER] = token;
    delete store.tokens[form];
    emit({ type: 'spent', form });
  }
  return (answer) => Promise.resolve(answer).then((response) => {
    if (response && [403, 429, 503].includes(response.status) && typeof response.clone === 'function') {
      response.clone().json().then((body) => {
        if (body && REFUSAL_CODES.includes(body.code)) emit({ type: 'refused', form, code: body.code });
      }).catch(() => {});
    }
    return response;
  });
};

// --- the widget

// themeFor follows the panel's light or dark mode.
export const themeFor = (mode) => (mode === 'dark' ? 'dark' : 'light');

// languageFor gives the widget the panel's language when it is a code
// Cloudflare takes (xx or xx-YY), else lets it choose.
export const languageFor = (language) => {
  const text = String(language || '');
  if (/^[a-z]{2}$/.test(text)) return text;
  const m = /^([a-z]{2})[-_]([a-zA-Z]{2})$/.exec(text);
  return m ? m[1] + '-' + m[2].toUpperCase() : 'auto';
};

// renderOptions are the options of turnstile.render (callbacks added by the component).
export const renderOptions = ({ siteKey, form, mode, language, appearance }) => ({
  sitekey: siteKey,
  action: (FORMS[form] || FORMS.login).action,
  theme: themeFor(mode),
  language: languageFor(language),
  size: 'flexible',
  appearance: appearance === 'interaction-only' ? 'interaction-only' : 'always',
  'refresh-expired': 'auto',
  'response-field': false,
  'feedback-enabled': false,
});

// widgetErrorKind sorts the codes of Turnstile's error-callback
// (developers.cloudflare.com/turnstile/troubleshooting/client-side-errors/error-codes/).
export const widgetErrorKind = (code) => {
  const text = String(code || '');
  if (text === '110200') return 'hostname';
  if (/^1101[01]0$/.test(text) || /^4000(20|21|70)$/.test(text)) return 'sitekey';
  if (text === '200500') return 'blocked';
  if (/^1106[02]0$/.test(text)) return 'timeout';
  if (text === '200100') return 'clock';
  if (/^110500$/.test(text)) return 'unsupported';
  return 'failed';
};

// statusText is the line under the widget, for a state of the component:
// loading, checking, ready, expired, blocked, unsupported, or an error kind.
export const statusText = (state, host) => {
  switch (state) {
    case 'loading':
    case 'checking':
      return ['turnstile.widget.checking', 'Cloudflare is checking that this is a person and not a program…'];
    case 'ready':
      return ['turnstile.widget.ready', 'Check passed: you can send the form.'];
    case 'expired':
      return ['turnstile.widget.expired', 'The check is too old: Cloudflare makes a new one.'];
    case 'blocked':
      return ['turnstile.widget.blocked', 'The check of Cloudflare could not load: this browser, an extension or the network blocked challenges.cloudflare.com. Allow that address, or use another browser. From the home network the panel signs in without the check.'];
    case 'hostname':
      return ['turnstile.widget.hostname', 'Cloudflare does not allow its check on {{host}}. Open the panel at its public address, or sign in from the home network.', { host: host || '' }];
    case 'sitekey':
      return ['turnstile.widget.sitekey', 'Cloudflare does not know the site key of this check. Sign in from the home network and look at the sign-in check in Configuration > Domains.'];
    case 'timeout':
      return ['turnstile.widget.timeout', 'The check took too long. Try it again.'];
    case 'clock':
      return ['turnstile.widget.clock', 'The check failed because this device\'s clock is wrong. Set the date and time, then try again.'];
    case 'unsupported':
      return ['turnstile.widget.unsupported', 'This browser cannot run the check of Cloudflare. Use a current browser, or sign in from the home network.'];
    default:
      return ['turnstile.widget.failed', 'Cloudflare could not confirm that this is a person. Try it again.'];
  }
};

// canRetry says whether a "try again" button makes sense in a state.
export const canRetry = (state) => ['timeout', 'failed', 'expired', 'clock'].includes(state);

// refusalText is what the server's refusal of a form means.
export const refusalText = (code) => {
  switch (code) {
    case 'TS001':
      return ['turnstile.refused.missing', 'The form was sent before the check of Cloudflare was done. Wait until it says the check passed, then send it again.'];
    case 'TS002':
      return ['turnstile.refused.refused', 'The check of Cloudflare was not accepted. A new one is being made: send the form again when it has passed.'];
    case 'TS003':
      return ['turnstile.refused.tooMany', 'Too many attempts from this address. Wait a minute, then try again.'];
    case 'TS004':
      return ['turnstile.refused.unavailable', 'The check of Cloudflare cannot be made right now. Try again later; from the home network the panel signs in without it.'];
    default:
      return null;
  }
};

// --- the settings card

export const FORM_LABELS = {
  login: ['mgmt.turnstile.form.login', 'Sign-in'],
  reset: ['mgmt.turnstile.form.reset', 'Forgot password'],
  register: ['mgmt.turnstile.form.register', 'Invitation and password reset links'],
};

export const MODES = [
  { value: 'managed', label: ['mgmt.turnstile.mode.managed', 'Managed (recommended)'], description: ['mgmt.turnstile.mode.managed.help', 'Cloudflare decides: most visitors see a box that ticks itself, a suspicious one is asked to tick it.'] },
  { value: 'non-interactive', label: ['mgmt.turnstile.mode.nonInteractive', 'Never ask to click'], description: ['mgmt.turnstile.mode.nonInteractive.help', 'A box that ticks itself; nobody is asked to click.'] },
  { value: 'invisible', label: ['mgmt.turnstile.mode.invisible', 'Invisible'], description: ['mgmt.turnstile.mode.invisible.help', 'Nothing is shown; only a line of text says the check is running.'] },
];

export const APPEARANCES = [
  { value: 'always', label: ['mgmt.turnstile.appearance.always', 'Always show the box'], description: ['mgmt.turnstile.appearance.always.help', 'The box is on the form from the start: visitors see that something is checked and when it is done.'] },
  { value: 'interaction-only', label: ['mgmt.turnstile.appearance.interaction', 'Only when a click is needed'], description: ['mgmt.turnstile.appearance.interaction.help', 'The box appears only for a visitor Cloudflare wants to click; the others see just the line of text.'] },
];

// visitorSees is the line that says what a visitor from the internet sees.
export const visitorSees = (mode, appearance) => {
  if (mode === 'invisible') return ['mgmt.turnstile.sees.invisible', 'A visitor from the internet sees no box, only the line "Cloudflare is checking…" under the form for a moment. Nobody at home sees anything.'];
  if (appearance === 'interaction-only') return ['mgmt.turnstile.sees.interaction', 'A visitor from the internet usually sees only the line "Cloudflare is checking…"; a box to tick appears when Cloudflare is unsure. Nobody at home sees anything.'];
  if (mode === 'non-interactive') return ['mgmt.turnstile.sees.nonInteractive', 'A visitor from the internet sees a small Cloudflare box that ticks itself within a second or two. Nobody at home sees anything.'];
  return ['mgmt.turnstile.sees.managed', 'A visitor from the internet sees a small Cloudflare box that usually ticks itself; when Cloudflare is unsure it asks for a click. Nobody at home sees anything.'];
};

const formList = (forms) => ['login', 'reset', 'register'].filter((form) => forms && forms[form]);

// stateText is the card's one sentence of state: [level, text].
export const stateText = (status) => {
  if (!status) return null;
  if (status.consoleOff) return ['warning', ['mgmt.turnstile.state.console', 'Switched off from the server\'s console: the file {{file}} exists. Delete it to let the check work again.', { file: status.consoleFile }]];
  if (status.active) {
    const forms = formList(status.forms);
    if (forms.length === 0) return ['warning', ['mgmt.turnstile.state.noForms', 'On, but no form asks for the check.']];
    return ['success', ['mgmt.turnstile.state.on', 'On: visitors from the internet pass the check of Cloudflare before {{count}} of the public forms is read. The home network is never asked.', { count: forms.length }]];
  }
  if (!status.siteKey) return ['info', ['mgmt.turnstile.state.off', 'Off. Make a widget at Cloudflare (Cosmos can do it for you) and the sign-in forms ask visitors from the internet to pass its check first.']];
  if (!status.confirmed) return ['info', ['mgmt.turnstile.state.saved', 'Keys saved, not tried yet: try the check once from this browser, then switch it on.']];
  return ['info', ['mgmt.turnstile.state.ready', 'Tried on {{host}}: ready to be switched on.', { host: status.confirmedHost }]];
};

// problemText says what is wrong now.
export const problemText = (problem) => {
  if (!problem) return null;
  if (problem.reason === 'key_refused') return ['mgmt.turnstile.problem.key', 'Cloudflare refuses the secret key (it was deleted or rotated at Cloudflare). Visitors from the internet cannot sign in until a working key is saved: paste the new keys below.'];
  return ['mgmt.turnstile.problem.unreachable', 'Cloudflare\'s check could not be reached ({{count}} times since {{since}}). Visitors from the internet cannot sign in meanwhile; the home network can.', { count: problem.count, since: problem.since }];
};

// reasonText turns a refusal of the settings routes into a sentence.
export const reasonText = (reason) => {
  switch (reason) {
    case 'secret_refused':
      return ['mgmt.turnstile.reason.secretRefused', 'Cloudflare does not know this secret key. Copy it again from the widget at Cloudflare (Turnstile > the widget > Settings).'];
    case 'site_key_format':
      return ['mgmt.turnstile.reason.siteKeyFormat', 'This is not a site key: it is a line of letters, digits, "-" and "_", such as 0x4AAAAAAA…'];
    case 'secret_format':
      return ['mgmt.turnstile.reason.secretFormat', 'This is not a secret key: it is a line of letters, digits, "-" and "_", such as 0x4AAAAAAA…'];
    case 'test_keys':
      return ['mgmt.turnstile.reason.testKeys', 'These are Cloudflare\'s test keys: they let anyone through. Make a real widget.'];
    case 'unreachable':
      return ['mgmt.turnstile.reason.unreachable', 'Cloudflare could not be reached from the server. Nothing was changed; try again in a moment.'];
    case 'permission':
      return ['mgmt.turnstile.reason.permission', 'The Cloudflare token Cosmos keeps may not make Turnstile widgets. Either add the permission "Account > Turnstile > Edit" to that token at Cloudflare (My Profile > API Tokens > the token > Edit), or make the widget by hand below.'];
    case 'account':
      return ['mgmt.turnstile.reason.account', 'Cloudflare did not say which account holds the domain, so no widget could be made there. Make it by hand below.'];
    case 'no_cloudflare_token':
      return ['mgmt.turnstile.reason.noToken', 'No Cloudflare token is kept for the panel\'s domain: make the widget by hand below.'];
    case 'no_public_name':
      return ['mgmt.turnstile.reason.noPublicName', 'The panel has no public name (Configuration > HTTP, hostname): a widget needs one, an address or a .local name will not do.'];
    case 'not_public_name':
      return ['mgmt.turnstile.reason.notPublicName', 'Try it from the panel\'s public address: the check must work there, where visitors from the internet sign in.'];
    case 'no_keys':
      return ['mgmt.turnstile.reason.noKeys', 'Save the keys first.'];
    case 'not_tried':
      return ['mgmt.turnstile.reason.notTried', 'Try the check once from this browser first: the server switches it on only once a check made with these keys has passed.'];
    case 'no_token':
    case 'token_invalid':
    case 'token_malformed':
      return ['mgmt.turnstile.reason.tokenInvalid', 'Cloudflare did not accept the check made in this browser. If the site key and the secret key belong to different widgets, paste both again from the same widget.'];
    case 'token_used':
      return ['mgmt.turnstile.reason.tokenUsed', 'The check was too old or already used. Try again.'];
    case 'token_hostname':
      return ['mgmt.turnstile.reason.tokenHostname', 'The check was made for another address than this one.'];
    case 'token_action':
      return ['mgmt.turnstile.reason.tokenAction', 'The check was made for another form.'];
    case 'too_many':
      return ['mgmt.turnstile.reason.tooMany', 'Too many checks in a short time. Wait a minute.'];
    case 'rate_limited':
      return ['mgmt.turnstile.reason.rateLimited', 'Cloudflare asks to wait before the next request. Try again in a few minutes.'];
    case 'mode':
    case 'appearance':
      return ['mgmt.turnstile.reason.choice', 'That choice is not one of the options.'];
    default:
      return ['mgmt.turnstile.reason.other', 'Cloudflare refused the request or could not be reached. Nothing was changed.'];
  }
};

// tryAddress is where the check can be tried: the panel's public name with
// the port this page uses. "" when the page is already there or there is none.
export const tryAddress = (status, location) => {
  if (!status || !status.panelHost || !location) return '';
  if (String(location.hostname || '').toLowerCase() === status.panelHost) return '';
  const port = location.port ? ':' + location.port : '';
  return 'https://' + status.panelHost + port + '/cosmos-ui/config-general/https';
};

// DASHBOARD_URL is where widgets are made by hand.
export const DASHBOARD_URL = 'https://dash.cloudflare.com/?to=/:account/turnstile';

// manualSteps are the steps at Cloudflare, in words.
export const manualSteps = (host) => [
  ['mgmt.turnstile.manual.step1', 'Open Cloudflare\'s dashboard, Turnstile (in the menu on the left, under "Application security" or "Protect & Connect"), and choose "Add widget".'],
  ['mgmt.turnstile.manual.step2', 'Name it, for example "Cosmos sign-in". Under hostnames add exactly {{host}}: not the whole domain, and no other name.', { host: host || 'the panel\'s public name' }],
  ['mgmt.turnstile.manual.step3', 'Widget mode: "Managed" is recommended. No pre-clearance is needed.'],
  ['mgmt.turnstile.manual.step4', 'Create it, then copy the site key and the secret key into the two fields below.'],
];

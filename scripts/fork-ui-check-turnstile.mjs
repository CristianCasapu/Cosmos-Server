// Fork addition: checks Cloudflare Turnstile on the public forms
// (src/turnstile, components/turnstile.jsx, components/turnstileWidget.jsx)
// in headless Chromium, Firefox and WebKit at 1366x900 and 390x844, against a
// throwaway Cosmos. Never the production instance, never the owner's
// Cloudflare account.
//
// What it expects:
//   - an --internal network (172.31.231.0/24 here) holding
//       * the stand-in scripts/fork-fake-cloudflare.go with the aliases
//         api.cloudflare.com and fake (control port 8080, siteverify on 8090),
//         /shared mounted;
//       * a fresh Cosmos at SERVER_IP, run in a loop, with COSMOS_HTTP_PORT=2052,
//         COSMOS_HTTPS_PORT=2053, SSL_CERT_FILE=/shared/ca.pem,
//         COSMOS_FORK_TURNSTILE_TEST=1 (Cloudflare's test keys allowed) and
//         COSMOS_FORK_TURNSTILE_SITEVERIFY=http://fake:8090/turnstile/v0/siteverify,
//         and the aliases cosmos.example.org (the panel) and app.example.org;
//   - this script in the Playwright image on that network AND on a second,
//     ordinary network: the browsers load the real widget from
//     challenges.cloudflare.com with Cloudflare's public test site keys (the
//     server never talks to Cloudflare: its siteverify is the stand-in).
// "A visitor from the internet" is made the way a reverse proxy at home
// would: the instance trusts the test network as a proxy (TrustedProxies) and
// the browser's requests to the panel carry X-Forwarded-For: 203.0.113.50.
// Without that header the browser is on the local network.
// The script policy (src/fork_csp.go, script-src 'self', frame-src 'none', with
// the allowance of src/fork_turnstile_csp.go while a site key is saved) comes
// from the server itself (CSP=server, the default): the script checks the
// header names Cloudflare only while a site key is saved, and counts every
// violation. CSP=1 puts the policy below on the pages instead (for a server
// without its own), CSP=0 neither puts nor checks one.
//
//   SERVER_IP=172.31.231.10 NET=172.31.231 ENGINES=chromium,firefox,webkit SIZES=d,m MODES=light,dark CSP=server
// Screenshots go to ./shots.
//
// SETUP_CODE: the fresh test instance's one-time setup code, which its wizard asks for: cat <its config folder>/setup-code (src/fork_setup_code.go).
import { chromium, firefox, webkit, request } from 'playwright';
import { mkdirSync } from 'node:fs';

const ip = process.env.SERVER_IP || '172.31.231.10';
const plain = `http://${ip}:2052`;
const base = 'https://cosmos.example.org:2053';
const fake = process.env.FAKE || 'http://fake:8080';
const password = 'Test-Passw0rd!x';
const GOOD = 'cfut_dGVzdC10b2tlbi1mb3ItdGhlLWJyb3dzZXItY2hlY2s0123456789';
const engines = { chromium, firefox, webkit };
const engineNames = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const sizes = (process.env.SIZES || 'd,m').split(',');
const viewports = { d: { width: 1366, height: 900 }, m: { width: 390, height: 844 } };
const cspMode = process.env.CSP || 'server';
const withCSP = cspMode !== '0';
const injectCSP = cspMode === '1';
const modes = (process.env.MODES || 'light,dark').split(',');
const testNet = process.env.NET || '172.31.231';
const VISITOR = '203.0.113.50';
const SITE_PASS = '1x00000000000000000000AA';
const SITE_FAIL = '2x00000000000000000000AB';
const SITE_CHALLENGE = '3x00000000000000000000FF';
const SECRET_PASS = '1x0000000000000000000000000000000AA';

// the audit2 panel policy with Turnstile's origin (src/fork_turnstile_csp_test.go); form-action names the app
// an OpenID sign-in returns to, as fork_csp.go does with authorizationserver.ReturnOrigins
const POLICY = "default-src 'self'; script-src 'self' https://challenges.cloudflare.com; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; font-src 'self'; connect-src 'self' https: http: wss: ws:; media-src 'self'; worker-src 'self'; manifest-src 'self'; frame-src https://challenges.cloudflare.com; object-src 'none'; base-uri 'none'; form-action 'self' https://app.example.org:2053; frame-ancestors 'none'";

const note = (...a) => console.log(...a);
const failures = [];
const allViolations = [];
const expect = (ok, what) => { note(ok ? '  ok  ' : '  FAIL', what); if (!ok) failures.push(what); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
mkdirSync('shots', { recursive: true });

const api = await request.newContext({ ignoreHTTPSErrors: true });
const until = async (what, check, seconds = 60) => {
  for (let i = 0; i < seconds; i++) {
    try { if (await check()) return true; } catch (e) { /* restarting */ }
    await sleep(1000);
  }
  expect(false, 'timed out: ' + what);
  return false;
};
const serverUp = () => until('the server answers over HTTPS', async () => (await api.get(base + '/cosmos/api/me')).status() < 500, 90);

// --- set up once: wizard, then a trusted proxy range, the Cloudflare domain and an app behind the sign-in
{
  let fresh = false;
  await until('the server answers', async () => { await api.get(plain + '/cosmos/api/status', { timeout: 3000, maxRedirects: 0 }); return true; }, 60);
  try {
    const answer = await api.get(plain + '/cosmos/api/status', { timeout: 5000, maxRedirects: 0 });
    fresh = answer.status() === 200 && (await answer.json()).status === 'OK';
  } catch (e) { /* set up already */ }
  if (fresh) {
    const step = (data) => api.post(plain + '/cosmos/api/newInstall', { data, headers: { 'X-Cosmos-Setup-Code': process.env.SETUP_CODE || '' } }).then((r) => r.status()).catch(() => 'exit');
    note('wizard:',
      await step({ step: '2', mongodbMode: 'Create' }),
      await step({ step: '3', httpsCertificateMode: 'SELFSIGNED', hostname: 'cosmos.example.org', allowHTTPLocalIPAccess: true }),
      await step({ step: '4', nickname: 'admin', password, email: 'admin@example.org' }),
      await step({ step: '5' }));
    await sleep(5000);
  }
  await serverUp();
  const ctx = await request.newContext({ ignoreHTTPSErrors: true });
  await ctx.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  await ctx.post(base + '/cosmos/api/sudo', { data: { password } });
  const config = (await (await ctx.get(base + '/cosmos/api/config')).json()).data;
  if (!(config.HTTPConfig.TrustedProxies || []).length) {
    config.HTTPConfig.TrustedProxies = [testNet + '.0/24'];
    config.HTTPConfig.DNSZones = [{ Zone: 'example.org', HTTPSCertificateMode: 'SELFSIGNED', DNSChallengeProvider: 'cloudflare', DNSChallengeConfig: { CF_DNS_API_TOKEN: GOOD } }];
    config.HTTPConfig.ProxyConfig.Routes = [{ Name: 'app', Mode: 'PROXY', Target: 'http://fake:8080', UseHost: true, Host: 'app.example.org', AuthEnabled: true }];
    const r = await ctx.put(base + '/cosmos/api/config', { data: config });
    note('settings (trusted proxy, domain, app):', r.status());
    await sleep(6000);
    await serverUp();
  }
  await ctx.dispose();
}

// --- browsers
// every browser of the run is another visitor: the server lets one visitor ask Cloudflare 6 times, then once every 30 s
let visitors = 0;
const nextVisitor = () => '203.0.113.' + (50 + (visitors++ % 150));
// plain: the visitor's address is sent with every request by the browser itself (extraHTTPHeaders) instead of
// by changing the requests in flight: Chromium then sends the panel's cookie along to another name (app.example.org)
// when a request is changed, which the server rightly refuses ("token not valid for this domain")
const open = async (engineName, size, { visitor: asVisitor, mode = 'light', siteKeyOverride, blockWidget, tamperToken, plain = false } = {}) => {
  const visitor = asVisitor ? nextVisitor() : '';
  const browser = await engines[engineName].launch();
  // the harness makes "a visitor from the internet" by changing the panel's requests; Firefox and WebKit
  // do not let it see the requests of a page the panel's service worker controls, so the worker is kept
  // out there (Chromium keeps it: the sign-in then also runs under the worker, which leaves Cloudflare's
  // requests alone, see pwaPolicy.js)
  const ctx = await browser.newContext({ viewport: viewports[size], ignoreHTTPSErrors: true, colorScheme: mode, locale: 'en-GB',
    serviceWorkers: visitor && engineName !== 'chromium' ? 'block' : 'allow', ...(plain && visitor ? { extraHTTPHeaders: { 'X-Forwarded-For': visitor } } : {}) });
  const violations = [];
  const name = `${engineName}-${size}`;
  // the policy's own report in the page (the console wording differs per engine)
  await ctx.exposeBinding('__turnstileCheckViolation', (source, v) => { violations.push('event ' + JSON.stringify(v)); allViolations.push(`${name}-${mode} ${JSON.stringify(v)}`); });
  await ctx.addInitScript(() => document.addEventListener('securitypolicyviolation', (e) => {
    try { window.__turnstileCheckViolation({ directive: e.effectiveDirective || e.violatedDirective, blocked: e.blockedURI, source: e.sourceFile, path: location.pathname }); } catch (err) { /* unloading */ }
  }, true));
  await ctx.route('https://challenges.cloudflare.com/**', (route) => (blockWidget ? route.abort('blockedbyclient') : route.continue()));
  if (!plain) await ctx.route(/https:\/\/(cosmos|app)\.example\.org:2053\/.*/, async (route) => {
    const req = route.request();
    const headers = { ...req.headers() };
    if (visitor) headers['x-forwarded-for'] = visitor;
    if (tamperToken && headers['x-cosmos-turnstile']) headers['x-cosmos-turnstile'] = 'forged-' + Date.now();
    const url = req.url();
    if (!(siteKeyOverride && url.includes('/cosmos/api/can-send-email')) && !(injectCSP && req.resourceType() === 'document' && url.includes('/cosmos-ui/'))) {
      return route.continue({ headers });
    }
    const answer = await route.fetch({ headers, maxRedirects: 0 });
    let body = await answer.body();
    const outHeaders = { ...answer.headers() };
    if (siteKeyOverride && url.includes('/cosmos/api/can-send-email')) {
      const json = JSON.parse(body.toString());
      if (json.data && json.data.turnstile) json.data.turnstile.siteKey = siteKeyOverride;
      body = Buffer.from(JSON.stringify(json));
    }
    if (injectCSP && req.resourceType() === 'document' && url.includes('/cosmos-ui/')) outHeaders['content-security-policy'] = POLICY;
    return route.fulfill({ status: answer.status(), headers: outHeaders, body });
  });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => failures.push(`${name} PAGEERROR: ${e.message.slice(0, 300)}`));
  page.on('console', (m) => {
    const text = m.text();
    // Cloudflare's own frame has a policy of its own that Firefox reports on (unknown directives): not the panel's
    if (/challenges\.cloudflare\.com\/cdn-cgi/.test(text) && /unknown directive/i.test(text)) return;
    if (/Content Security Policy|Content-Security-Policy|violates|Refused to (load|execute|frame)/i.test(text)) { violations.push(text.slice(0, 300)); allViolations.push(`${name}-${mode} console ${text.slice(0, 300)}`); }
  });
  return { browser, ctx, page, name, violations };
};
const shot = (page, name, what) => page.screenshot({ path: `shots/${name}-${what}.png`, fullPage: true });
const noSideScroll = async (page, name, where) => {
  const over = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(over <= 1, `${name}: no sideways scroll on ${where} (${over}px over)`);
};
const adminCall = async (body) => {
  const ctx = await request.newContext({ ignoreHTTPSErrors: true });
  await ctx.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  await ctx.post(base + '/cosmos/api/sudo', { data: { password } });
  const r = await ctx.post(base + '/cosmos/api/turnstile', { data: body });
  const json = await r.json().catch(() => ({}));
  await ctx.dispose();
  return { status: r.status(), json };
};
// the panel's policy as the server sends it: Cloudflare's origin in script-src and frame-src only, and only while a site key is saved
const panelPolicy = async () => {
  const r = await api.get(base + '/cosmos-ui/login', { maxRedirects: 0 });
  return r.headers()['content-security-policy'] || r.headers()['content-security-policy-report-only'] || '';
};
const policyNamesCloudflare = (policy) => {
  const named = policy.split(';').map((d) => d.trim()).filter((d) => d.includes('challenges.cloudflare.com')).map((d) => d.split(' ')[0]);
  return named.sort().join(',');
};
const widgetState = (page) => page.locator('[data-turnstile]').getAttribute('data-turnstile-state').catch(() => null);
const statusLine = (page) => page.locator('[data-turnstile] [role=status]').innerText().catch(() => '');
const fillLogin = async (page) => {
  await page.locator('#nickname-login').fill('admin');
  await page.locator('[name=password]').fill(password);
};
const submit = (page) => page.locator('form button[type=submit]').first().click();

// ===== 1. the settings card, from the local network (Chromium, then the others for the look)
{
  const { browser, page, name, violations } = await open('chromium', 'd', {});
  await page.goto(base + '/cosmos-ui/login', { waitUntil: 'networkidle' });
  await page.waitForTimeout(2000);
  expect(await page.locator('[data-turnstile]').count() === 0, `${name}: the sign-in shows no check on the local network`);
  await fillLogin(page);
  await submit(page);
  await page.waitForURL(/cosmos-ui\/?$/, { timeout: 30000 }).catch(() => {});
  expect(!page.url().includes('/login'), `${name}: a local visitor signs in without a check`);
  await page.request.post(base + '/cosmos/api/sudo', { data: { password } });

  await page.goto(base + '/cosmos-ui/config-general/https', { waitUntil: 'domcontentloaded' });
  const card = page.locator('[data-turnstile-card]');
  await card.waitFor({ timeout: 30000 });
  await card.scrollIntoViewIfNeeded();
  expect((await card.locator('[data-turnstile-state]').getAttribute('data-turnstile-state')) === 'off', `${name}: the card says off`);
  if (cspMode === 'server') {
    const policy = await panelPolicy();
    expect(/script-src 'self'(;|$)/.test(policy) && /frame-src 'none'/.test(policy) && !/cloudflare/i.test(policy), `the server's policy names no Cloudflare without a site key (${policy})`);
  }
  await shot(page, name, 'card-off');

  // "create it for me" with the DNS token: Cloudflare refuses, the card says which permission
  await card.locator('[data-turnstile-action=create]').click();
  await card.locator('[data-turnstile-notice=error]').waitFor({ timeout: 30000 });
  const permission = await card.locator('[data-turnstile-notice=error]').innerText();
  expect(/Account > Turnstile > Edit/.test(permission), `${name}: a token without the permission is told which to add (${permission.slice(0, 80)})`);
  await shot(page, name, 'card-permission');

  // with the permission: a widget for the panel's name only
  await api.get(fake + '/token?turnstile=1');
  await card.locator('[data-turnstile-action=create]').click();
  await card.locator('[data-turnstile-keys]').waitFor({ timeout: 30000 });
  const widgets = await (await api.get(fake + '/widgets')).json();
  expect(widgets.length === 1 && JSON.stringify(widgets[0].domains) === '["cosmos.example.org"]', `${name}: the widget is made for cosmos.example.org only (${JSON.stringify(widgets.map((w) => w.domains))})`);
  expect(await card.locator('[data-turnstile-action=enable]').isDisabled(), `${name}: "Switch on" waits for a try`);
  // the server refuses to switch on without a try, whatever the page does
  const refusedOn = await adminCall({ action: 'enable' });
  expect(refusedOn.status === 409 && refusedOn.json.reason === 'not_tried', `${name}: the server refuses to switch on before a try (${refusedOn.status} ${refusedOn.json.reason})`);
  // the stand-in's site key is unknown to the real Cloudflare: the try shows it in words
  await card.locator('[data-turnstile-action=try]').click();
  await page.waitForTimeout(8000);
  const trialLine = await card.locator('[data-turnstile-trial] [role=status]').innerText().catch(() => '');
  note('  trial with an unknown site key says:', trialLine);
  expect(/site key|could not confirm|not allow/i.test(trialLine), `${name}: a site key Cloudflare does not know is said in words`);
  await shot(page, name, 'card-unknown-sitekey');
  await card.getByRole('button', { name: 'Cancel' }).click().catch(() => {});

  // wrong keys by hand: refused before they are kept
  await card.locator('[data-turnstile-action=remove]').click();
  await page.waitForTimeout(1500);
  await card.getByRole('button', { name: /Make it by hand/ }).click().catch(() => {});
  await card.locator('#turnstile-site-key').fill('0x4AAAAAAAnotAWidget');
  await card.locator('#turnstile-secret-key').fill('0x4AAAAAAAnotASecret0000000000000');
  await card.locator('[data-turnstile-action=keys]').click();
  await card.locator('[data-turnstile-notice=error]').waitFor({ timeout: 30000 });
  expect(/does not know this secret key/.test(await card.locator('[data-turnstile-notice=error]').innerText()), `${name}: a secret Cloudflare does not know is refused`);
  expect(await card.locator('[data-turnstile-action=enable]').count() === 0, `${name}: nothing to switch on after refused keys`);
  await shot(page, name, 'card-bad-keys');

  // Cloudflare's test keys (the instance runs in test mode), tried for real in this browser
  await card.locator('#turnstile-site-key').fill(SITE_PASS);
  await card.locator('#turnstile-secret-key').fill(SECRET_PASS);
  await card.locator('[data-turnstile-action=keys]').click();
  await card.locator('[data-turnstile-action=try]').waitFor({ timeout: 30000 });
  if (cspMode === 'server') {
    const policy = await panelPolicy();
    expect(policyNamesCloudflare(policy) === 'frame-src,script-src' && /script-src 'self' https:\/\/challenges\.cloudflare\.com(;|$)/.test(policy) && /frame-src https:\/\/challenges\.cloudflare\.com(;|$)/.test(policy) && !/unsafe-eval/.test(policy), `with a site key saved the server's policy allows Cloudflare in script-src and frame-src only (${policy})`);
  }
  await card.locator('[data-turnstile-action=try]').click();
  await until('the trial passes', async () => (await card.locator('[data-turnstile-state]').getAttribute('data-turnstile-state')) === 'ready', 40);
  await shot(page, name, 'card-tried');
  await card.locator('[data-turnstile-action=enable]').click();
  await until('switched on', async () => (await card.locator('[data-turnstile-state]').getAttribute('data-turnstile-state')) === 'on', 30);
  await shot(page, name, 'card-on');
  expect(violations.length === 0 || !withCSP, `${name}: no script policy violation on the card (${violations.slice(0, 3).join(' | ')})`);
  await browser.close();
}

// the look of the card in the other engines and sizes, light and dark
for (const engineName of engineNames) {
  for (const size of sizes) {
    for (const mode of modes) {
      const { browser, page, name } = await open(engineName, size, { mode });
      await page.goto(base + '/cosmos-ui/login', { waitUntil: 'domcontentloaded' });
      await page.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
      await page.goto(base + '/cosmos-ui/config-general/https', { waitUntil: 'domcontentloaded' });
      const card = page.locator('[data-turnstile-card]');
      const ok = await card.waitFor({ timeout: 30000 }).then(() => true).catch(() => false);
      expect(ok, `${name}-${mode}: the card is shown`);
      if (ok) {
        await card.getByRole('button', { name: 'If something goes wrong' }).click();
        await card.scrollIntoViewIfNeeded();
        await noSideScroll(page, name, 'the card');
        await card.screenshot({ path: `shots/${name}-${mode}-card.png` });
      }
      await browser.close();
    }
  }
}

// ===== 2. a visitor from the internet
const internetLogin = async (engineName, size, mode) => {
  const { browser, page, name, violations } = await open(engineName, size, { visitor: VISITOR, mode });
  const tag = `${name}-${mode}`;
  await page.goto(base + '/cosmos-ui/login', { waitUntil: 'domcontentloaded' });
  const shown = await page.locator('[data-turnstile=login]').waitFor({ timeout: 30000 }).then(() => true).catch(() => false);
  expect(shown, `${tag}: the sign-in shows the check to a visitor from the internet`);
  await until(`${tag}: the check passes`, async () => (await widgetState(page)) === 'ready', 40);
  expect((await statusLine(page)).includes('Check passed'), `${tag}: the line says the check passed`);
  await noSideScroll(page, tag, 'the sign-in');
  await shot(page, tag, 'login-passed');
  await fillLogin(page);
  await submit(page);
  await page.waitForURL(/cosmos-ui\/?$/, { timeout: 30000 }).catch(() => {});
  expect(!page.url().includes('/login'), `${tag}: signed in with the check`);
  if (withCSP) expect(violations.length === 0, `${tag}: no script policy violation (${violations.slice(0, 3).join(' | ')})`);
  await browser.close();
};
for (const engineName of engineNames) {
  for (const size of sizes) {
    for (const mode of modes) await internetLogin(engineName, size, mode);
  }
}

// a check the browser blocked, a failing widget, a forced challenge, a refused token, Cloudflare down
for (const engineName of engineNames) {
  const size = engineName === 'chromium' ? 'm' : 'd';
  {
    const { browser, page, name } = await open(engineName, size, { visitor: VISITOR, blockWidget: true });
    await page.goto(base + '/cosmos-ui/login', { waitUntil: 'domcontentloaded' });
    await until(`${name}: a blocked check is said`, async () => (await widgetState(page)) === 'blocked', 30);
    expect(/could not load/.test(await statusLine(page)), `${name}: the line explains the blocked check`);
    await fillLogin(page);
    const answered = page.waitForResponse((r) => r.url().includes('/cosmos/api/login'), { timeout: 20000 }).catch(() => null);
    await submit(page);
    const refusal = await answered;
    await page.waitForTimeout(1000);
    expect(!!refusal && refusal.status() === 403 && page.url().includes('/login') && /could not load/.test(await statusLine(page)), `${name}: sending anyway is refused, and the line still says why`);
    expect(await page.locator('form button[type=submit]').first().isEnabled(), `${name}: the button is not dead`);
    await shot(page, name, 'login-blocked');
    await browser.close();
  }
  {
    const { browser, page, name } = await open(engineName, size, { visitor: VISITOR, siteKeyOverride: SITE_FAIL });
    await page.goto(base + '/cosmos-ui/login', { waitUntil: 'domcontentloaded' });
    await until(`${name}: a failing check is said`, async () => ['failed', 'timeout'].includes(await widgetState(page)), 40);
    expect(await page.getByRole('button', { name: 'Check again' }).count() === 1, `${name}: a failed check offers to check again`);
    await shot(page, name, 'login-failing');
    await browser.close();
  }
  {
    const { browser, page, name } = await open(engineName, 'd', { visitor: VISITOR, siteKeyOverride: SITE_CHALLENGE });
    await page.goto(base + '/cosmos-ui/login', { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(6000);
    note(`  ${name}: forced challenge, state ${await widgetState(page)}`);
    await shot(page, name, 'login-challenge');
    // the interactive test key passes on a click in its frame
    const frame = page.frameLocator('iframe[src*="challenges.cloudflare.com"]');
    await frame.locator('input[type=checkbox], label').first().click({ timeout: 8000 }).catch(() => {});
    await page.waitForTimeout(4000);
    note(`  ${name}: after a click in the challenge, state ${await widgetState(page)}`);
    await browser.close();
  }
  {
    const { browser, page, name } = await open(engineName, size, { visitor: VISITOR, tamperToken: true });
    await page.goto(base + '/cosmos-ui/login', { waitUntil: 'domcontentloaded' });
    await until(`${name}: the check passes`, async () => (await widgetState(page)) === 'ready', 40);
    await fillLogin(page);
    await submit(page);
    await page.locator('[data-turnstile=login] [role=alert]').waitFor({ timeout: 20000 }).catch(() => {});
    const alert = await page.locator('[data-turnstile=login] [role=alert]').innerText().catch(() => '');
    expect(/was not accepted/.test(alert), `${name}: a token the server refuses is explained (${alert.slice(0, 60)})`);
    expect(page.url().includes('/login'), `${name}: not signed in with a refused token`);
    await shot(page, name, 'login-refused');
    await browser.close();
  }
  {
    await api.get(fake + '/siteverify/down?mode=500');
    const { browser, page, name } = await open(engineName, size, { visitor: VISITOR });
    await page.goto(base + '/cosmos-ui/login', { waitUntil: 'domcontentloaded' });
    await until(`${name}: the check passes`, async () => (await widgetState(page)) === 'ready', 40);
    await fillLogin(page);
    await submit(page);
    await page.locator('[data-turnstile=login] [role=alert]').waitFor({ timeout: 30000 }).catch(() => {});
    const alert = await page.locator('[data-turnstile=login] [role=alert]').innerText().catch(() => '');
    expect(/cannot be made right now/.test(alert) && /home network/.test(alert), `${name}: Cloudflare unreachable is explained (${alert.slice(0, 60)})`);
    await shot(page, name, 'login-unreachable');
    await browser.close();
    await api.get(fake + '/siteverify/down?mode=');
  }
}

// ===== 3. machines, the other forms, the two-factor step, the app gate (Chromium)
{
  // an API token is never asked; without one, a program from the internet is
  const admin = await request.newContext({ ignoreHTTPSErrors: true });
  await admin.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  await admin.post(base + '/cosmos/api/sudo', { data: { password } });
  const made = await admin.post(base + '/cosmos/api/api-tokens', { data: { name: 'ci', description: 'test', readOnly: true, expiryDays: 1 } });
  const madeJson = await made.json().catch(() => ({}));
  const token = (madeJson.data && (madeJson.data.token || madeJson.data.Token)) || '';
  note('  API token made:', made.status(), token ? 'yes' : JSON.stringify(madeJson).slice(0, 200));
  const program = await request.newContext({ ignoreHTTPSErrors: true, extraHTTPHeaders: { 'X-Forwarded-For': VISITOR } });
  const without = await program.post(base + '/cosmos/api/password-reset', { data: { nickname: 'admin', email: 'admin@example.org' } });
  expect(without.status() === 403 && (await without.json()).code === 'TS001', `a program from the internet without the check is refused (${without.status()})`);
  if (token) {
    const withToken = await program.post(base + '/cosmos/api/password-reset', { data: { nickname: 'admin', email: 'admin@example.org' }, headers: { Authorization: 'Bearer ' + token } });
    const body = await withToken.json().catch(() => ({}));
    expect(!String(body.code || '').startsWith('TS'), `an API token is not asked for the check (${withToken.status()} ${body.code})`);
  }
  const local = await request.newContext({ ignoreHTTPSErrors: true });
  const fromHome = await local.post(base + '/cosmos/api/password-reset', { data: { nickname: 'admin', email: 'admin@example.org' } });
  expect(!String((await fromHome.json().catch(() => ({}))).code || '').startsWith('TS'), `the home network is not asked (${fromHome.status()})`);

  // an invited user sets a password from the internet
  const invite = await admin.post(base + '/cosmos/api/users', { data: { nickname: 'visitor1', email: 'visitor1@example.org' } });
  const inviteJson = await invite.json().catch(() => ({}));
  const key = inviteJson.data && (inviteJson.data.registerKey || inviteJson.data.RegisterKey);
  note('  invitation:', invite.status(), key ? 'key' : JSON.stringify(inviteJson).slice(0, 200));
  if (key) {
    const { browser, page, name } = await open('chromium', 'm', { visitor: VISITOR });
    await page.goto(`${base}/cosmos-ui/register?nickname=visitor1&key=${key}`, { waitUntil: 'domcontentloaded' });
    await until(`${name}: the invitation's check passes`, async () => (await page.locator('[data-turnstile=register]').getAttribute('data-turnstile-state')) === 'ready', 40);
    await page.locator('[name=password]').fill('Visitor-Passw0rd!x');
    await page.locator('[name=confirmPassword]').fill('Visitor-Passw0rd!x').catch(() => {});
    await shot(page, name, 'register');
    await page.locator('form button[type=submit]').first().click();
    await page.waitForURL(/\/login/, { timeout: 30000 }).catch(() => {});
    expect(page.url().includes('/login'), `${name}: the invitation is used from the internet with the check`);
    await browser.close();
  }

  // forgot password from the internet: the check passes, then the server's own answer
  {
    const { browser, page, name } = await open('chromium', 'd', { visitor: VISITOR });
    await page.goto(base + '/cosmos-ui/forgot-password', { waitUntil: 'domcontentloaded' });
    await until(`${name}: the forgot-password check passes`, async () => (await page.locator('[data-turnstile=reset]').getAttribute('data-turnstile-state')) === 'ready', 40);
    await page.locator('[name=nickname]').fill('admin');
    await page.locator('[name=email]').fill('admin@example.org');
    await shot(page, name, 'forgot');
    const answer = page.waitForResponse((r) => r.url().includes('/cosmos/api/password-reset'));
    await page.locator('form button[type=submit]').first().click();
    const r = await answer;
    const body = await r.json().catch(() => ({}));
    expect(!String(body.code || '').startsWith('TS'), `${name}: the check let the reset through to the server (${r.status()} ${body.code})`);
    await page.waitForTimeout(1500);
    expect(await page.locator('form button[type=submit]').first().isEnabled(), `${name}: the reset button does not stay waiting`);
    await browser.close();
  }

  // the two-factor page has no check; the app gate's round trip goes through the protected sign-in
  {
    const { browser, page, name } = await open('chromium', 'd', { visitor: VISITOR, plain: true });
    await page.goto(base + '/cosmos-ui/loginmfa', { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(3000);
    expect(await page.locator('[data-turnstile]').count() === 0, `${name}: the two-factor page asks for no check`);
    await page.goto('https://app.example.org:2053/', { waitUntil: 'domcontentloaded' });
    await page.waitForURL(/cosmos\.example\.org:2053\/cosmos-ui\/login/, { timeout: 30000 }).catch(() => {});
    note('  app gate led to', page.url().split('?')[0]);
    const shown = await page.locator('[data-turnstile=login]').waitFor({ timeout: 30000 }).then(() => true).catch(() => false);
    expect(shown, `${name}: the app's sign-in is the panel's, with the check`);
    await until(`${name}: the check passes`, async () => (await widgetState(page)) === 'ready', 40);
    await fillLogin(page);
    // the check passes the sign-in, the authorization sends the visitor back to the app's callback,
    // which signs the visitor in on the app's name and opens the app
    let auth = null;
    page.on('response', (r) => { if (r.url().includes('/oauth2/auth') && r.request().method() === 'POST') auth = r; });
    await submit(page);
    await until(`${name}: the authorization is asked`, async () => !!auth, 40);
    const location = auth ? (auth.headers().location || '') : '';
    note('  authorization answered', auth && auth.status(), location.split('?')[0]);
    expect(!!auth && auth.status() === 303 && location.startsWith('https://app.example.org:2053/cosmos/oauth2/detect-callback?code='), `${name}: the sign-in with the check leads back to the app with a code`);
    await page.waitForURL((url) => url.hostname === 'app.example.org' && !url.pathname.startsWith('/cosmos'), { timeout: 30000 }).catch(() => {});
    const landed = new URL(page.url());
    const cookies = (await page.context().cookies('https://app.example.org:2053/')).map((c) => c.name);
    // the app behind the gate is the stand-in's control port, which has nothing at "/": its own 404 is the app answering
    const body = await page.locator('body').innerText().catch(() => '');
    expect(landed.hostname === 'app.example.org' && landed.pathname === '/' && cookies.includes('jwttoken') && /404 page not found/.test(body), `${name}: the round trip ends in the app, signed in on its name (${landed.href.split('?')[0]}, ${body.slice(0, 40)})`);
    await shot(page, name, 'app-gate');
    await browser.close();
  }

  const events = await admin.get(base + '/cosmos/api/security/events?kind=auth.turnstile.*&limit=50');
  const list = ((await events.json().catch(() => ({}))).data || {});
  const kinds = (list.events || list || []).map ? (list.events || []).map((e) => e.kind + ':' + ((e.details || {}).reason || (e.details || {}).form || '')) : [];
  note('  recorded:', JSON.stringify(kinds).slice(0, 600));
  expect(kinds.some((k) => k.startsWith('auth.turnstile.missing')) && kinds.some((k) => k.startsWith('auth.turnstile.failed')) && kinds.some((k) => k.startsWith('auth.turnstile.unavailable')), 'the three kinds are in the security record');
  const calls = await (await api.get(fake + '/siteverify/calls')).json();
  // the visitor's address as the fork determines it (the trial in the card came from the local network, the key tests carry none)
  expect(calls.some((c) => /^203\.0\.113\./.test(c.remoteip)) && calls.every((c) => !c.remoteip || /^203\.0\.113\./.test(c.remoteip) || c.remoteip.startsWith(testNet + '.')), `siteverify got the visitor's address (${[...new Set(calls.map((c) => c.remoteip))].join(',')})`);
}

if (withCSP) expect(allViolations.length === 0, `no script policy violation and no policy message in any page of the run (${allViolations.slice(0, 5).join(' | ')})`);
note(`\n${failures.length ? 'FAILED: ' + failures.length : 'all passed'}`);
for (const f of failures) note('  - ' + f);
process.exit(failures.length ? 1 : 0);

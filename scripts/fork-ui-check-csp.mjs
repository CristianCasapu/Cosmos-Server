// Fork addition: checks the panel's script policy and security headers
// (src/fork_csp.go) in real browsers against a throwaway instance. Never point
// it at the production instance.
//
// What it does, by PHASE:
//   headers  asks the server directly (no browser) and checks the headers per
//            path: the panel's page, sign-in page, offline page, a build file,
//            the worker, the manifest, an API answer, and an application behind
//            the proxy (APP_BASE), which must carry its own headers only
//   wizard   on a FRESH instance: walks the first steps of the setup wizard in
//            the browser, then finishes it over the API with HTTPS on Cosmos'
//            own certificate (SELFSIGNED) and the hostname of BASE
//   walk     runs scripts/fork-ui-gallery.mjs (every page, tab and main dialog)
//            with each browser context listening for securitypolicyviolation
//            events and for console messages about the policy
//   flows    what the gallery does not do: the OpenID consent page ending on an
//            application's address (form-action), the logs download, the CSV
//            export of the security record, a chart, the country picker, the
//            container terminal (a command typed and its output read back), the
//            Market (with images when the network reaches the internet), the
//            offline page
//   all      headers, walk, flows
//
// Every violation is a failure, whether the policy is enforced or reported
// only (COSMOS_CSP_REPORT_ONLY=1 on the server): the walk is the same, and
// report-only is how a change to the policy is tried before it is enforced.
// Results: <OUT>/csp-<phase>.json, screenshots under <OUT>/flows/ and the
// gallery's own under <OUT>/<engine>-<size>-<mode>/. The exit code is 1 when
// something failed.
//
// Run it from the Playwright image on the instance's network, with playwright
// installed next to it (npm i playwright@1.65.1):
//   docker run --rm --network <net> --ipc=host -v <scripts>:/work -w /work \
//     -e BASE=https://csp.test -e PHASE=all mcr.microsoft.com/playwright:v1.65.1-noble \
//     node fork-ui-check-csp.mjs
//
//   BASE=https://csp.test        the instance (HTTPS with Cosmos' own certificate; WebKit needs a dot)
//   APP_BASE=https://app.csp.test  an application route of the instance to a backend that sets
//                                its own Content-Security-Policy and Referrer-Policy (headers phase,
//                                and the return address of the OpenID check)
//   APP_HEADERS='{"Content-Security-Policy":"..."}'  what that backend sends (checked as is)
//   ENGINES=chromium,firefox,webkit  VIEWPORTS=1366x900,390x844  MODES=dark
//   OUT=csp-check  ADMIN_PASSWORD=...  ONLY=<regexp> (gallery views)
//   CONTAINER=jellyfin           the container whose terminal is tried (Docker in Docker, has sh)
//   INTERNET=1                   the network reaches the internet: Market images must load
//
// SETUP_CODE: the fresh test instance's one-time setup code, which its wizard asks for: cat <its config folder>/setup-code (src/fork_setup_code.go).
import { chromium, firefox, webkit } from 'playwright';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const base = process.env.BASE || 'https://csp.test';
const appBase = process.env.APP_BASE || '';
const out = process.env.OUT || 'csp-check';
const phase = process.env.PHASE || 'all';
const engineNames = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const viewports = (process.env.VIEWPORTS || '1366x900,390x844').split(',');
const modes = (process.env.MODES || 'dark').split(',');
const admin = { nickname: 'admin', password: process.env.ADMIN_PASSWORD || 'Test-Passw0rd!x' };
const containerName = process.env.CONTAINER || 'jellyfin';
const engines = { chromium, firefox, webkit };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
mkdirSync(out, { recursive: true });

let failed = 0;
const results = [];
const check = (where, what, ok, detail) => {
  results.push({ where, what, ok: !!ok, detail: detail === undefined ? null : detail });
  if (!ok) failed++;
  console.log(`${ok ? 'ok  ' : 'FAIL'} [${where}] ${what}${detail !== undefined ? ': ' + JSON.stringify(detail).slice(0, 500) : ''}`);
};

// ---------------------------------------------------------------------------
// Listening for the policy in a browser context
// ---------------------------------------------------------------------------
const violations = [];
const policyMessage = /Content.Security.Policy|Refused to|violates the following|Permissions.Policy|Feature.Policy|Cross-Origin-Opener-Policy/i;
// what the browsers say about a report-only policy as such (no address to report to,
// frame-ancestors not applied in report-only): not a violation
const reportOnlyNotice = /without a report-uri|does not specify a 'report-uri'|ignored when delivered in a report-only policy|delivered in report-only mode, but does not specify/i;
const instrument = async (context, label) => {
  await context.exposeBinding('__cspViolation', ({ page }, v) => {
    violations.push({ label, page: page ? page.url().replace(base, '') : null, ...v });
  });
  await context.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => {
      try {
        window.__cspViolation({
          kind: 'event', directive: e.effectiveDirective || e.violatedDirective, blocked: e.blockedURI,
          source: e.sourceFile, line: e.lineNumber, sample: e.sample, disposition: e.disposition,
        });
      } catch (err) { /* the binding is gone while the page unloads */ }
    }, true);
  });
  const watch = (page) => page.on('console', (m) => {
    if (policyMessage.test(m.text()) && !reportOnlyNotice.test(m.text())) violations.push({ label, page: page.url().replace(base, ''), kind: 'console', type: m.type(), text: m.text().slice(0, 400) });
  });
  context.on('page', watch);
  for (const page of context.pages()) watch(page);
};

// the gallery launches its own browsers: wrap them, so that every context it opens is listened to
const wrapEngine = (name) => {
  const engine = engines[name];
  const launch = engine.launch.bind(engine);
  engine.launch = async (...args) => {
    const browser = await launch(...args);
    const newContext = browser.newContext.bind(browser);
    browser.newContext = async (options = {}) => {
      const context = await newContext(options);
      const vp = options.viewport ? `${options.viewport.width}x${options.viewport.height}` : '';
      await instrument(context, `${name}-${vp}-${options.colorScheme || ''}`);
      return context;
    };
    return browser;
  };
};

const reportViolations = (where) => {
  const unique = new Map();
  for (const v of violations) {
    const key = [v.label, v.kind, v.directive, v.blocked, v.text, v.page].join('|');
    if (!unique.has(key)) unique.set(key, v);
  }
  const list = [...unique.values()];
  check(where, 'no policy violation and no policy message in the console', list.length === 0, list.length ? list.slice(0, 30) : undefined);
  return list;
};

// ---------------------------------------------------------------------------
// headers
// ---------------------------------------------------------------------------
const headersPhase = async () => {
  process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0';
  const ask = async (url, init = {}) => {
    const r = await fetch(url, { redirect: 'manual', ...init });
    const h = {};
    r.headers.forEach((value, name) => { h[name] = h[name] ? h[name] + ', ' + value : value; });
    return { status: r.status, h };
  };
  const panelPaths = ['/cosmos-ui/', '/cosmos-ui/login', '/cosmos-ui/offline.html', '/cosmos-ui/sw.js', '/cosmos-ui/manifest.webmanifest', '/cosmos-ui/skin/theme.css'];
  const page = await ask(base + '/cosmos-ui/');
  const asset = /src="(\/cosmos-ui\/assets\/main-[^"]+\.js)"/.exec(await (await fetch(base + '/cosmos-ui/')).text());
  if (asset) panelPaths.push(asset[1]);
  const policy = page.h['content-security-policy-report-only'] || page.h['content-security-policy'];
  check('headers', 'the panel has a script policy', !!policy, policy);
  for (const path of panelPaths) {
    const { status, h } = await ask(base + path);
    const p = h['content-security-policy-report-only'] || h['content-security-policy'];
    check('headers', `${path} (${status}): the panel's policy`, p === policy && /script-src 'self'(;|$)/.test(p) && !/unsafe-eval/.test(p) && /frame-ancestors 'none'/.test(p) && /object-src 'none'/.test(p) && /base-uri 'none'/.test(p));
    check('headers', `${path}: nosniff, Referrer-Policy, Permissions-Policy, COOP`,
      h['x-content-type-options'] === 'nosniff' && h['referrer-policy'] === 'same-origin' && /camera=\(\)/.test(h['permissions-policy'] || '') && h['cross-origin-opener-policy'] === 'same-origin',
      { nosniff: h['x-content-type-options'], referrer: h['referrer-policy'], permissions: h['permissions-policy'], coop: h['cross-origin-opener-policy'] });
    check('headers', `${path}: HSTS on a name with Cosmos' own certificate forgets, never includeSubDomains`, (h['strict-transport-security'] || 'max-age=0') === 'max-age=0', h['strict-transport-security']);
  }
  const api = await ask(base + '/cosmos/api/status', { headers: { 'Sec-Fetch-Site': 'same-origin' } });
  check('headers', `/cosmos/api/status (${api.status}): the API's policy allows nothing`, (api.h['content-security-policy'] || '').startsWith("default-src 'none'"), api.h['content-security-policy']);
  check('headers', '/cosmos/api/status: nosniff, Referrer-Policy', api.h['x-content-type-options'] === 'nosniff' && api.h['referrer-policy'] === 'same-origin');
  if (appBase) {
    const app = await ask(appBase + '/');
    const own = JSON.parse(process.env.APP_HEADERS || '{}');
    check('headers', `application ${appBase} (${app.status}) answered`, app.status === 200, app.h);
    for (const [name, value] of Object.entries(own)) {
      const got = app.h[name.toLowerCase()] || '';
      // upstream's header hardening of a route (on unless the route turns it off) puts its own
      // frame-ancestors policy in place of the application's: that is the proxy's, not the panel's
      const hardening = name.toLowerCase() === 'content-security-policy' && got === "frame-ancestors 'self'";
      check('headers', `application: its own ${name}${hardening ? " (replaced by the route's header hardening, as upstream does)" : ''}`, got.split(', ').includes(value) || got === value || hardening, got);
    }
    const cspValues = (app.h['content-security-policy'] || '');
    check('headers', 'application: not the panel\'s policy', !cspValues.includes("script-src 'self'; style-src") && !cspValues.startsWith("default-src 'none'"), cspValues);
    check('headers', 'application: no Permissions-Policy, no COOP from the panel', !app.h['permissions-policy'] && !app.h['cross-origin-opener-policy'], { pp: app.h['permissions-policy'], coop: app.h['cross-origin-opener-policy'] });
    const own_rp = own['Referrer-Policy'];
    if (own_rp) check('headers', 'application: its Referrer-Policy only', app.h['referrer-policy'] === own_rp, app.h['referrer-policy']);
  }
};

// ---------------------------------------------------------------------------
// a browser of our own (wizard, flows)
// ---------------------------------------------------------------------------
const open = async (engineName, vp, mode = 'dark') => {
  const [width, height] = vp.split('x').map(Number);
  const browser = await engines[engineName].launch();
  const context = await browser.newContext({ viewport: { width, height }, colorScheme: mode, ignoreHTTPSErrors: true, locale: 'en-GB', acceptDownloads: true });
  await context.addInitScript((m) => { try { window.localStorage.setItem('ct-mode', m); } catch (e) { /* no storage */ } }, mode);
  const label = `${engineName}-${vp}-${mode}`;
  await instrument(context, label);
  const page = await context.newPage();
  const errors = [];
  // (the panel's check whether an application answers fails on the test names: not the policy's business)
  page.on('pageerror', (e) => { if (!/__cosmos_probe/.test(e.message)) errors.push(e.message.slice(0, 300)); });
  return { browser, context, page, label, errors };
};

const shot = async (page, label, name) => {
  const dir = join(out, 'flows', label);
  mkdirSync(dir, { recursive: true });
  await page.screenshot({ path: join(dir, name + '.png') }).catch(() => {});
};

const signIn = async (page) => {
  await page.goto(base + '/cosmos-ui/login', { waitUntil: 'domcontentloaded' });
  const form = await page.locator('#nickname-login').waitFor({ timeout: 10000 }).then(() => true, () => false);
  // still signed in (the session outlives a restart of the server): the page went on by itself
  if (form && /\/login/.test(new URL(page.url()).pathname)) {
    await page.locator('#nickname-login').fill(admin.nickname);
    await page.locator('#-password-login').fill(admin.password);
    await page.locator('button[type=submit]').first().click();
    await page.waitForURL((url) => !/\/login/.test(url.pathname), { timeout: 20000 });
  }
  await page.waitForTimeout(1000);
  return page.evaluate((password) => fetch('/cosmos/api/sudo', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ password }) }).then((r) => r.status), admin.password);
};

const api = (page, method, path, body) => page.evaluate(async ([m, p, b, c]) => {
  const r = await fetch('/cosmos/api/' + p, { method: m, headers: { 'Content-Type': 'application/json', 'X-Cosmos-Setup-Code': c }, body: b ? JSON.stringify(b) : undefined });
  let json = null;
  try { json = await r.json(); } catch (e) { /* none */ }
  return { status: r.status, json };
}, [method, path, body, process.env.SETUP_CODE || '']);

// the wizard's first screen asks for the instance's setup code (SETUP_CODE)
const passSetupCode = async (page) => {
  const box = page.locator('[data-fork-setup-code] input');
  if (!(await box.waitFor({ timeout: 10000 }).then(() => true, () => false))) return;
  await box.fill(process.env.SETUP_CODE || '');
  await page.locator('[data-fork-setup-code] button[type=submit]').click();
  await page.waitForTimeout(1500);
};

// ---------------------------------------------------------------------------
// wizard
// ---------------------------------------------------------------------------
const wizardPhase = async () => {
  for (const engineName of engineNames) {
    for (const vp of viewports) {
      const { browser, page, label, errors } = await open(engineName, vp);
      await page.goto(base + '/cosmos-ui/newInstall', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(2500);
      await shot(page, label, 'wizard-code');
      await passSetupCode(page);
      await shot(page, label, 'wizard-0');
      const next = page.locator('button:has-text("Start"), button:has-text("Next"), button:has-text("Skip")').last();
      for (let step = 1; step <= 2; step++) {
        await next.click({ timeout: 8000 }).catch((e) => check(label, `wizard: step ${step}`, false, e.message.split('\n')[0]));
        await page.waitForTimeout(2500);
        await shot(page, label, 'wizard-' + step);
      }
      check(label, 'wizard: no script error', errors.length === 0, errors.length ? errors : undefined);
      await browser.close();
    }
  }
  // finish over the API from a page of the instance
  const { browser, page } = await open(engineNames[0], viewports[0]);
  await page.goto(base + '/cosmos-ui/newInstall', { waitUntil: 'domcontentloaded' });
  const host = new URL(base).hostname;
  const say = async (body) => (await api(page, 'POST', 'newInstall', body)).status;
  check('wizard', 'step 2', (await say({ step: '2' })) === 200);
  check('wizard', 'step 3 (Cosmos\' own certificate)', (await say({ step: '3', httpsCertificateMode: 'SELFSIGNED', hostname: host })) === 200);
  await sleep(1500);
  check('wizard', 'step 4', (await say({ step: '4', ...admin, email: 'admin@example.org' })) === 200);
  await say({ step: '5' }).catch(() => {});
  await browser.close();
  reportViolations('wizard');
};

// ---------------------------------------------------------------------------
// walk (the gallery)
// ---------------------------------------------------------------------------
const walkPhase = async () => {
  for (const name of engineNames) wrapEngine(name);
  process.env.OUT = out;
  process.env.ENGINES = engineNames.join(',');
  process.env.VIEWPORTS = viewports.join(',');
  process.env.MODES = modes.join(',');
  process.env.BASE = base;
  const argv = process.argv;
  process.argv = argv.slice(0, 2);
  await import('./fork-ui-gallery.mjs');
  process.argv = argv;
  reportViolations('walk');
};

// ---------------------------------------------------------------------------
// flows
// ---------------------------------------------------------------------------
const flowsPhase = async () => {
  for (const engineName of engineNames) {
    for (const vp of viewports) {
      const { browser, context, page, label, errors } = await open(engineName, vp);
      const phone = vp.startsWith('390');
      page.setDefaultNavigationTimeout(60000);
      try {
      check(label, 'signed in, password confirmed', (await signIn(page)) === 200);

      // the OpenID consent page: the form goes to /oauth2/auth, which sends the browser on to the application
      if (appBase) {
        const config = (await api(page, 'GET', 'config')).json.data;
        const redirect = appBase + '/csp-callback';
        if (!(config.OpenIDClients || []).some((c) => c.id === 'csp-check')) {
          config.OpenIDClients = [...(config.OpenIDClients || []), { id: 'csp-check', secret: '$2a$10$abcdefghijklmnopqrstuuabcdefghijklmnopqrstuvwxyz01234', redirect }];
          await api(page, 'PUT', 'config', config).catch(() => {});
          for (let i = 0; i < 30; i++) { await sleep(1000); if ((await page.request.get(base + '/cosmos/api/status', { failOnStatusCode: false }).catch(() => null))) break; }
          await sleep(4000);
          await signIn(page);
        }
        const state = 'state' + Date.now() + 'abcdefgh';
        await page.goto(`${base}/oauth2/auth?client_id=csp-check&redirect_uri=${encodeURIComponent(redirect)}&response_type=code&scope=openid&state=${state}&nonce=${state}`, { waitUntil: 'domcontentloaded' }).catch(() => {});
        await page.waitForURL((url) => url.href.startsWith(redirect), { timeout: 20000 }).catch(() => {});
        await shot(page, label, 'openid-return');
        check(label, 'OpenID: the consent form ends on the application\'s address with a code', page.url().startsWith(redirect) && /[?&]code=/.test(page.url()), page.url().replace(/code=[^&]+/, 'code=...'));
      }

      // the logs download (a link to /cosmos/_logs) and the security record's CSV export
      await page.goto(base + '/cosmos-ui/config-general/general', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(2500);
      const logs = page.locator('a[href="/cosmos/_logs"]').first();
      if (await logs.count()) {
        const [download] = await Promise.all([page.waitForEvent('download', { timeout: 15000 }).catch(() => null), logs.click()]);
        if (download) check(label, 'logs: downloaded', !!(await download.path().catch(() => null)), download.suggestedFilename());
        else {
          // shown in the tab instead of saved: it must be text, with nothing refused
          await page.waitForTimeout(1500);
          const text = await page.evaluate(() => document.body && document.body.innerText.length).catch(() => 0);
          check(label, 'logs: opened', text > 0 && /_logs/.test(page.url()), page.url().replace(base, ''));
        }
      } else check(label, 'logs: the link is there', false);

      await page.goto(base + '/cosmos-ui/security-audit', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(3000);
      const csv = page.locator('main button:has-text("CSV"), main button:has-text("Export")').first();
      if (await csv.count()) {
        const [download] = await Promise.all([page.waitForEvent('download', { timeout: 15000 }).catch(() => null), csv.click()]);
        check(label, 'security record: CSV saved', !!download && /\.csv$/.test(download.suggestedFilename()), download && download.suggestedFilename());
      } else check(label, 'security record: the export button is there', false);

      // a chart, drawn and hovered
      await page.goto(base + '/cosmos-ui/monitoring', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(4000);
      const chart = page.locator('.apexcharts-svg').first();
      check(label, 'monitoring: a chart is drawn', await chart.count() > 0);
      if (await chart.count()) { await chart.hover().catch(() => {}); await page.waitForTimeout(500); }
      await shot(page, label, 'monitoring');

      // the country picker (flag pictures from flagcdn.com)
      await page.goto(base + '/cosmos-ui/config-general/general', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(2500);
      const tabs = page.locator('main [role=tablist]').first();
      const securityTab = page.locator('main [role=tab]:has-text("Security")').first();
      if (!phone && await securityTab.count()) await securityTab.click().catch(() => {});
      const countries = page.locator('input[id*=ountr], [id*=ountr] input').first();
      if (await countries.count()) {
        await countries.click().catch(() => {});
        await page.waitForTimeout(1500);
        await shot(page, label, 'countries');
        await page.keyboard.press('Escape');
      }
      void tabs;

      // the container terminal: connect, type, read back
      await page.goto(base + `/cosmos-ui/servapps/containers/${containerName}`, { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(2500);
      try {
        if (phone) {
          await page.locator('main .MuiSelect-select').first().click();
          await page.locator('[role=listbox] li').nth(4).click();
        } else await page.locator('main [role=tab]').nth(4).click();
        await page.waitForTimeout(1500);
        // a new shell in the container (Connect attaches to its main process, which reads nothing here)
        const connect = page.locator('main button:has-text("New Shell")').first();
        await connect.click({ timeout: 8000 });
        await page.waitForTimeout(2500);
        await page.locator('.xterm-helper-textarea, .xterm').first().click().catch(() => {});
        await page.keyboard.type('echo csp-$((40+2))-ok\n', { delay: 30 });
        await page.waitForTimeout(2500);
        const screen = await page.locator('.xterm-rows').first().innerText().catch(() => '');
        check(label, 'terminal: a command ran and its output is shown', /csp-42-ok/.test(screen), screen.slice(-200));
      } catch (e) { check(label, 'terminal', false, e.message.split('\n')[0]); }
      await shot(page, label, 'terminal');

      // the Market: its list, a listing, and pictures from its hosts when there is internet
      await page.goto(base + '/cosmos-ui/market-listing', { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(8000);
      const images = await page.evaluate(() => [...document.images].filter((i) => /^https?:/.test(i.src) && !i.src.startsWith(location.origin)).map((i) => ({ src: i.src, ok: i.complete && i.naturalWidth > 0 })));
      if (process.env.INTERNET === '1') check(label, 'Market: remote pictures loaded', images.length > 0 && images.filter((i) => i.ok).length >= Math.min(3, images.length), { total: images.length, loaded: images.filter((i) => i.ok).length });
      await shot(page, label, 'market');
      if (process.env.INTERNET === '1') {
        // an app's listing with its screenshots, and its installer reading the compose file
        const first = page.locator('main a[href*="market-listing/"], main .MuiCard-root').first();
        if (await first.count()) {
          await first.click().catch(() => {});
          await page.waitForTimeout(4000);
          await shot(page, label, 'market-app');
          const install = page.locator('button:has-text("Install")').first();
          if (await install.count()) {
            await install.click().catch(() => {});
            await page.waitForTimeout(6000);
            await shot(page, label, 'market-install');
            const shown = await page.locator('[role=dialog]:visible').first().innerText().catch(() => '');
            check(label, 'Market: the installer read the compose file', shown.length > 50 && !/Error|error/.test(shown.slice(0, 200)), shown.slice(0, 160));
            await page.keyboard.press('Escape');
          }
        }
      }

      // the offline page, as a page of its own
      await page.goto(base + '/cosmos-ui/offline.html', { waitUntil: 'load' });
      await page.waitForTimeout(1200);
      check(label, 'offline page: drawn with its script', await page.locator('#ct-offline-retry').count() > 0);
      await shot(page, label, 'offline');

      } catch (e) { check(label, 'the flows ran to the end', false, String(e.message).split('\n')[0]); }
      check(label, 'no script error in the flows', errors.length === 0, errors.length ? [...new Set(errors)] : undefined);
      await context.close();
      await browser.close();
    }
  }
  reportViolations('flows');
};

if (phase === 'headers' || phase === 'all') await headersPhase();
if (phase === 'wizard') await wizardPhase();
if (phase === 'walk' || phase === 'all') { await walkPhase(); violations.length = 0; }
if (phase === 'flows' || phase === 'all') await flowsPhase();

writeFileSync(join(out, `csp-${phase}.json`), JSON.stringify({ failed, results, violations }, null, 1));
console.log(failed ? `\n${failed} check(s) FAILED` : '\nall checks passed');
process.exit(failed ? 1 : 0);

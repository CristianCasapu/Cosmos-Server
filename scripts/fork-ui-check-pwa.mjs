// Fork addition: checks the installable panel (client/sw/, src/fork_pwa.go,
// client/src/components/pwa.jsx) in real browsers against a throwaway
// instance. Never point it at the production instance.
//
// The instance needs a certificate the browsers trust (service workers are
// refused otherwise): a test authority trusted inside the browser container
// (system store for WebKit, NSS for Chromium, a policy for Firefox), and the
// instance started with that authority's certificate for its hostname
// (HTTPSCertificateMode PROVIDED). WebKit needs a hostname with a dot.
//
// Each phase keeps the browser's profile in <OUT>/profile-<engine>, so that
// the worker registered by one phase is there for the next, like on a real
// device. Run the phases in this order, doing what each header says between:
//
//   PHASE=online    sign in, worker registered and controlling, installability
//                   (Chromium), main pages visited, Cache Storage holds only the
//                   build's files, sign out: still nothing of the API, and no
//                   API answer in the HTTP cache
//   PHASE=offline   with the instance STOPPED: a page of the panel shows the
//                   offline page (503) instead of the browser's error; with
//                   WAIT_BACK=1 it then waits (up to 3 min) for the instance to
//                   be started again and checks that the page opens by itself
//   PHASE=update    after the UI was rebuilt with any change (a new sw.js): the
//                   open panel shows "a new version is ready", Reload switches
//                   to the new build and the old cache is gone
//   PHASE=off       with DisableWebApp set: the worker and its caches are gone
//                   after one visit, the manifest is "not found"
//   PHASE=fresh     (no profile kept) a first visit at phone and desktop size, as
//                   a check that nothing is broken without a worker yet
//   PHASE=all       online + offline + back in one session without a kept profile:
//                   for WebKit, whose kept profiles lose Cache Storage in Playwright.
//                   Stop the instance when it says STOP, start it when it says START
//   PHASE=install   the install button (Chromium's event stood in for) at desktop and
//                   tablet width, and the iPhone steps in the menu (WebKit, iPhone agent)
//   PHASE=switch    the switch in Configuration > Appearance, saved (SWITCH=on: back on)
//   PHASE=untrusted UNTRUSTED_BASE=https://<ip>:<port>: what the browser does with the
//                   worker behind a certificate it does not trust (reported)
//
// Firefox in Playwright ignores the enterprise policy: the test authority is
// put into each Firefox profile with certutil (the image needs libnss3-tools).
//
//   BASE=https://pwa.test:8443  ENGINES=chromium,firefox,webkit  OUT=/out
//   ADMIN_PASSWORD=...          MODES=dark,light (for the screenshots)
import { chromium, firefox, webkit } from 'playwright';
import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';

const base = process.env.BASE || 'https://pwa.test:8443';
const out = process.env.OUT || 'pwa-check';
const phase = process.env.PHASE || 'online';
const engineNames = (process.env.ENGINES || 'chromium').split(',');
const modes = (process.env.MODES || 'dark').split(',');
const admin = { nickname: process.env.ADMIN_NICKNAME || 'admin', password: process.env.ADMIN_PASSWORD || 'Test-Passw0rd!x' };
const engines = { chromium, firefox, webkit };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
mkdirSync(out, { recursive: true });

const results = [];
let failed = 0;
const check = (engine, what, ok, detail) => {
  results.push({ engine, phase, what, ok: !!ok, detail: detail === undefined ? null : detail });
  if (!ok) failed += 1;
  console.log(`${ok ? 'ok  ' : 'FAIL'} [${engine}] ${what}${detail !== undefined ? ': ' + JSON.stringify(detail).slice(0, 400) : ''}`);
};

const SIZES = { desktop: { width: 1366, height: 900 }, phone: { width: 390, height: 844 } };

const open = async (engineName, size = 'desktop', mode = 'dark', persistent = true) => {
  const options = {
    viewport: SIZES[size],
    colorScheme: mode,
    locale: 'en-GB',
    serviceWorkers: 'allow',
    ...(size === 'phone' ? { isMobile: engineName !== 'firefox', hasTouch: true, deviceScaleFactor: 2 } : {}),
  };
  // Firefox learns the test authority only through a profile: a first visit gets a new, empty one
  if (!persistent && engineName === 'firefox') persistent = 'firefox-new-' + Date.now();
  if (persistent) {
    const profile = join(out, 'profile-' + (persistent === true ? engineName : persistent));
    if (engineName === 'firefox' && !existsSync(join(profile, 'cert9.db'))) {
      // Playwright's Firefox ignores the enterprise policy: the test authority goes into the profile itself
      mkdirSync(profile, { recursive: true });
      execFileSync('certutil', ['-d', 'sql:' + profile, '-N', '--empty-password']);
      execFileSync('certutil', ['-d', 'sql:' + profile, '-A', '-t', 'C,,', '-n', 'cosmos-pwa-test', '-i', process.env.CA_FILE || '/ca/ca.crt']);
    }
    const context = await engines[engineName].launchPersistentContext(profile, options);
    const page = context.pages()[0] || await context.newPage();
    return { context, page, close: () => context.close() };
  }
  const browser = await engines[engineName].launch();
  const context = await browser.newContext(options);
  const page = await context.newPage();
  return { context, page, close: () => browser.close() };
};

const login = async (page) => {
  await page.goto(base + '/cosmos-ui/login', { waitUntil: 'networkidle' });
  // a profile kept from an earlier phase may still be signed in: the login page then leaves by itself
  if (!/\/login/.test(new URL(page.url()).pathname)) return;
  await page.locator('#nickname-login').fill(admin.nickname);
  await page.locator('#-password-login').fill(admin.password);
  await page.locator('button[type=submit]').first().click();
  await page.waitForURL((url) => !/\/login/.test(url.pathname), { timeout: 30000 });
  await page.waitForLoadState('networkidle').catch(() => {});
};

// What the page's origin keeps: the worker registrations and every cached address.
const kept = (page) => page.evaluate(async () => {
  const registrations = navigator.serviceWorker ? await navigator.serviceWorker.getRegistrations() : [];
  const caches = {};
  if (window.caches) {
    for (const name of await window.caches.keys()) {
      const cache = await window.caches.open(name);
      caches[name] = (await cache.keys()).map((request) => new URL(request.url).pathname + new URL(request.url).search);
    }
  }
  return {
    registrations: registrations.map((r) => ({ scope: r.scope, active: !!r.active, waiting: !!r.waiting, script: r.active && r.active.scriptURL })),
    controlled: !!(navigator.serviceWorker && navigator.serviceWorker.controller),
    caches,
  };
});

const onlyBuildFiles = (engine, state, when) => {
  const all = Object.values(state.caches).flat();
  const names = Object.keys(state.caches);
  check(engine, `${when}: cache names are the worker's own`, names.every((n) => n.startsWith('cosmos-ui-')), names);
  const bad = all.filter((key) => !key.startsWith('/cosmos-ui/') || key.startsWith('/cosmos/')
    || (/\.html$/.test(key.split('?')[0]) && key !== '/cosmos-ui/offline.html')
    || !/\.(js|css|woff2|png|jpg|svg|html)$/.test(key.split('?')[0]));
  check(engine, `${when}: Cache Storage holds only files of the build (${all.length} files)`, all.length > 0 && bad.length === 0, bad.length ? bad : undefined);
  check(engine, `${when}: nothing under /cosmos/ (API) in Cache Storage`, !all.some((key) => key.startsWith('/cosmos/')));
};

const shot = async (page, name) => {
  await page.screenshot({ path: join(out, name + '.png') });
};

const phases = {
  async online(engine, session) {
    const { page, context, close } = session || await open(engine);
    try {
      await login(page);
      // the worker is registered after the status is read; the first page is not controlled yet
      const ready = await page.evaluate(() => Promise.race([
        navigator.serviceWorker.ready.then((r) => !!r.active),
        new Promise((resolve) => setTimeout(() => resolve(false), 20000)),
      ])).catch((e) => String(e));
      check(engine, 'worker registered and active', ready === true, ready);
      await page.reload({ waitUntil: 'networkidle' });
      let state = await kept(page);
      check(engine, 'one registration, scope /cosmos-ui/', state.registrations.length === 1 && state.registrations[0].scope === base + '/cosmos-ui/', state.registrations);
      check(engine, 'the page is controlled by the worker', state.controlled);

      if (engine === 'chromium') {
        const cdp = await context.newCDPSession(page);
        const manifest = await cdp.send('Page.getAppManifest');
        check(engine, 'manifest parsed without errors', manifest.errors.length === 0, manifest.errors);
        const installable = await cdp.send('Page.getInstallabilityErrors');
        check(engine, 'installable (no installability errors)', installable.installabilityErrors.length === 0, installable.installabilityErrors);
        const data = JSON.parse(manifest.data || '{}');
        check(engine, 'manifest: id, start_url and scope under /cosmos-ui/, standalone', manifest.url === base + '/cosmos-ui/manifest.webmanifest'
          && data.id === '/cosmos-ui/' && data.start_url === '/cosmos-ui/' && data.scope === '/cosmos-ui/' && data.display === 'standalone', { url: manifest.url, id: data.id });
      }

      for (const path of ['/cosmos-ui/', '/cosmos-ui/servapps', '/cosmos-ui/config-url', '/cosmos-ui/monitoring', '/cosmos-ui/security', '/cosmos-ui/config-users', '/cosmos-ui/config-general', '/cosmos-ui/market-listing/']) {
        await page.goto(base + path, { waitUntil: 'networkidle' }).catch(() => {});
        await sleep(500);
      }
      state = await kept(page);
      onlyBuildFiles(engine, state, 'after a session');
      writeFileSync(join(out, `cache-${engine}-session.json`), JSON.stringify(state, null, 2));

      // API answers are no-store: the browser cannot answer from its own cache
      const header = await page.evaluate(() => fetch('/cosmos/api/status').then((r) => r.headers.get('Cache-Control'))).catch((e) => String(e));
      check(engine, 'API answers say Cache-Control: no-store', header === 'no-store', header);
      if (engine !== 'webkit') {
        // (WebKit goes to the network for only-if-cached: nothing to learn there)
        const fromCache = await page.evaluate(() => fetch('/cosmos/api/status', { cache: 'only-if-cached', mode: 'same-origin' })
          .then((r) => 'answered ' + r.status, (e) => 'refused')).catch((e) => String(e));
        check(engine, 'an API answer is not in the HTTP cache', fromCache === 'refused', fromCache);
      }

      await page.goto(base + '/cosmos-ui/logout', { waitUntil: 'networkidle' }).catch(() => {});
      await page.waitForURL(/\/login/, { timeout: 20000 }).catch(() => {});
      state = await kept(page);
      onlyBuildFiles(engine, state, 'after signing out');
      const status = await page.evaluate(() => fetch('/cosmos/api/status').then((r) => r.status)).catch((e) => String(e));
      check(engine, 'signed out: the API refuses (401)', status === 401, status);
      writeFileSync(join(out, `cache-${engine}-logout.json`), JSON.stringify(state, null, 2));
    } finally {
      if (!session) await close();
    }
  },

  async offline(engine) {
    for (const mode of modes) {
      for (const size of ['phone', 'desktop']) {
        const { page, close } = await open(engine, size, mode);
        try {
          const response = await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'load', timeout: 20000 }).catch((e) => String(e));
          const status = response && typeof response.status === 'function' ? response.status() : response;
          const title = await page.title().catch(() => '');
          check(engine, `${size} ${mode}: the offline page instead of the browser's error`, title === 'Cosmos cannot be reached', { status, title });
          check(engine, `${size} ${mode}: answered 503`, status === 503, status);
          await page.waitForSelector('#ct-offline-status:not(:empty)', { timeout: 5000 }).catch(() => {});
          const layout = await page.evaluate(() => ({
            sideways: document.documentElement.scrollWidth > window.innerWidth,
            button: (() => { const b = document.getElementById('ct-offline-retry').getBoundingClientRect(); return { w: b.width, h: b.height }; })(),
            status: document.getElementById('ct-offline-status').textContent,
            font: getComputedStyle(document.body).fontFamily,
            bg: getComputedStyle(document.querySelector('.ct-offline-card')).backgroundColor,
          }));
          check(engine, `${size} ${mode}: no sideways scroll, retry tappable (>= 44 px)`, !layout.sideways && layout.button.h >= 44, layout);
          await shot(page, `offline-${engine}-${size}-${mode}`);
          if (process.env.WAIT_BACK === '1' && size === 'desktop' && mode === modes[0]) {
            console.log(`[${engine}] waiting for the instance to come back…`);
            const back = await page.waitForFunction(() => document.title !== 'Cosmos cannot be reached', null, { timeout: 180000, polling: 1000 })
              .then(() => true, () => false);
            await page.waitForLoadState('networkidle').catch(() => {});
            check(engine, 'the panel opens by itself when the server is back', back, page.url());
            await shot(page, `offline-back-${engine}`);
          }
        } finally {
          await close();
        }
      }
    }
  },

  async update(engine) {
    const { page, close } = await open(engine);
    try {
      await login(page);
      await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'networkidle' });
      const before = await page.evaluate(() => new Promise((resolve) => {
        const channel = new MessageChannel();
        channel.port1.onmessage = (e) => resolve(e.data.version);
        navigator.serviceWorker.controller.postMessage({ type: 'ABOUT' }, [channel.port2]);
        setTimeout(() => resolve(null), 3000);
      }));
      await page.evaluate(() => navigator.serviceWorker.getRegistration('/cosmos-ui/').then((r) => r.update()));
      const notice = page.getByText('A new version of Cosmos is ready.');
      const shown = await notice.waitFor({ timeout: 30000 }).then(() => true, () => false);
      check(engine, 'the notice of a new version is shown', shown);
      await shot(page, `update-notice-${engine}`);
      if (shown) {
        await Promise.all([
          page.waitForEvent('load', { timeout: 20000 }).catch(() => {}),
          page.locator('.ct-pwa-update').getByRole('button', { name: 'Reload' }).click(),
        ]);
        await page.waitForLoadState('networkidle').catch(() => {});
        await sleep(1000);
        const after = await page.evaluate(() => new Promise((resolve) => {
          if (!navigator.serviceWorker.controller) { resolve(null); return; }
          const channel = new MessageChannel();
          channel.port1.onmessage = (e) => resolve(e.data.version);
          navigator.serviceWorker.controller.postMessage({ type: 'ABOUT' }, [channel.port2]);
          setTimeout(() => resolve(null), 3000);
        }));
        check(engine, 'the new build controls the page after Reload', !!after && after !== before, { before, after });
        const state = await kept(page);
        check(engine, 'only the new build\'s cache is left', Object.keys(state.caches).length === 1 && Object.keys(state.caches)[0] === 'cosmos-ui-' + after, Object.keys(state.caches));
        check(engine, 'the notice is gone', !(await notice.isVisible().catch(() => false)));
      }
    } finally {
      await close();
    }
  },

  // The switch in Configuration > Appearance: off, saved, read back from the
  // server. SWITCH=on puts it back. (Saving restarts the instance's listener.)
  async switch(engine) {
    const want = process.env.SWITCH !== 'on';
    const browser = await engines[engine].launch();
    const page = await (await browser.newContext({ viewport: SIZES.desktop })).newPage();
    try {
      await login(page);
      await page.evaluate((password) => fetch('/cosmos/api/sudo', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ password }) }), admin.password);
      await page.goto(base + '/cosmos-ui/config-general/appearance', { waitUntil: 'networkidle' });
      const box = page.getByLabel('Offer the panel as an installable app');
      check(engine, 'the switch is shown, on by default', want ? await box.isChecked() : !(await box.isChecked()));
      await shot(page, 'switch-card-' + engine);
      if (want) await box.uncheck(); else await box.check();
      await page.getByRole('button', { name: 'Save' }).first().click();
      await sleep(8000);
      const stored = await page.evaluate(() => fetch('/cosmos/api/config').then((r) => r.json()).then((j) => j.data.DisableWebApp === true)).catch((e) => String(e));
      check(engine, `saved: DisableWebApp is ${want}`, stored === want, stored);
    } finally {
      await browser.close();
    }
  },

  async off(engine) {
    const { page, close } = await open(engine);
    try {
      const before = await page.goto(base + '/cosmos-ui/login', { waitUntil: 'load' }).then(() => kept(page));
      // the worker's address now gives the removing worker; give the browser a moment and a second load
      await sleep(3000);
      await page.reload({ waitUntil: 'load' });
      await sleep(3000);
      const state = await kept(page);
      check(engine, 'switched off: no registration left', state.registrations.length === 0, { before: before.registrations, after: state.registrations });
      check(engine, 'switched off: no cache left', Object.keys(state.caches).length === 0, Object.keys(state.caches));
      const manifest = await page.evaluate(() => fetch('/cosmos-ui/manifest.webmanifest').then((r) => r.status));
      check(engine, 'switched off: the manifest is not found', manifest === 404, manifest);
    } finally {
      await close();
    }
  },

  // One browser session from sign-in to "server gone" and back, for WebKit:
  // Playwright's WebKit keeps no Cache Storage in a persistent profile, so the
  // worker's cache does not survive from one phase to the next there. The
  // script says when to stop the instance, and when to start it again.
  async all(engine) {
    const session = await open(engine, 'desktop', modes[0], false);
    const { page, close } = session;
    try {
      await phases.online(engine, session);
      console.log(`[${engine}] STOP THE INSTANCE NOW`);
      // (asked in a loop: a predicate that returns a promise is "true" at once for waitForFunction)
      let gone = false;
      for (let i = 0; i < 120 && !gone; i++) {
        gone = await page.evaluate(() => fetch('/cosmos-ui/manifest.webmanifest', { cache: 'no-store' }).then(() => false, () => true)).catch(() => false);
        if (!gone) await sleep(2000);
      }
      check(engine, 'the instance was stopped', gone);
      for (const size of ['phone', 'desktop']) {
        await page.setViewportSize(SIZES[size]);
        const response = await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'load', timeout: 20000 }).catch((e) => String(e));
        const status = response && typeof response.status === 'function' ? response.status() : response;
        const title = await page.title().catch(() => '');
        const how = response && typeof response.status === 'function'
          ? { fromWorker: response.fromServiceWorker(), cacheControl: response.headers()['cache-control'], url: response.url() } : null;
        check(engine, `${size}: the offline page instead of the browser's error`, title === 'Cosmos cannot be reached', { status, title, how });
        await page.waitForSelector('#ct-offline-status:not(:empty)', { timeout: 5000 }).catch(() => {});
        const sideways = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth).catch(() => null);
        check(engine, `${size}: no sideways scroll`, sideways === false, sideways);
        await shot(page, `offline-${engine}-${size}-${modes[0]}`);
      }
      console.log(`[${engine}] START THE INSTANCE AGAIN`);
      const back = await page.waitForFunction(() => document.title !== 'Cosmos cannot be reached', null, { timeout: 240000, polling: 1000 })
        .then(() => true, () => false);
      await page.waitForLoadState('networkidle').catch(() => {});
      check(engine, 'the panel opens by itself when the server is back', back, page.url());
      await shot(page, `offline-back-${engine}`);
    } finally {
      await close();
    }
  },

  // UNTRUSTED_BASE: an address whose certificate the browser does not trust
  // (the instance's IP address: the certificate names only its hostname),
  // opened with the certificate error clicked away. What does the browser do
  // with the worker there? (Reported, not judged: browsers differ.)
  async untrusted(engine) {
    const untrusted = process.env.UNTRUSTED_BASE;
    const browser = await engines[engine].launch();
    const context = await browser.newContext({ ignoreHTTPSErrors: true, serviceWorkers: 'allow', viewport: SIZES.desktop });
    const page = await context.newPage();
    try {
      await page.goto(untrusted + '/cosmos-ui/login', { waitUntil: 'networkidle' });
      const outcome = await page.evaluate(() => navigator.serviceWorker.register('/cosmos-ui/sw.js', { scope: '/cosmos-ui/' })
        .then(() => 'registered', (e) => 'refused: ' + e.name + ' ' + String(e.message).slice(0, 160))).catch((e) => String(e));
      const panel = await page.locator('#nickname-login').isVisible().catch(() => false);
      check(engine, 'untrusted certificate: the sign-in page still works', panel);
      console.log(`[${engine}] untrusted certificate, worker: ${outcome}`);
      results.push({ engine, phase, what: 'worker on an untrusted certificate', ok: true, detail: outcome });
    } finally {
      await browser.close();
    }
  },

  // The install affordances, seen: the header button at desktop and tablet
  // width when the browser offers installation (Chromium's event, stood in
  // for here: headless Chromium never fires it), and the steps for an iPhone
  // in the menu's footer (WebKit, with an iPhone's user agent).
  async install(engine) {
    const iphone = engine === 'webkit';
    const browser = await engines[engine].launch();
    const sizes = iphone ? [['phone', SIZES.phone]] : [['desktop', SIZES.desktop], ['tablet', { width: 820, height: 1180 }]];
    try {
      for (const mode of modes) {
        for (const [size, viewport] of sizes) {
          const context = await browser.newContext({
            viewport, colorScheme: mode, serviceWorkers: 'allow',
            ...(iphone ? { userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1', isMobile: true, hasTouch: true, deviceScaleFactor: 2 } : {}),
          });
          const page = await context.newPage();
          await login(page);
          await sleep(1500);
          const label = 'Install Cosmos as an app';
          if (!iphone) {
            await page.evaluate(() => {
              const event = new Event('beforeinstallprompt', { cancelable: true });
              event.prompt = async () => {};
              event.userChoice = Promise.resolve({ outcome: 'accepted', platform: 'web' });
              window.dispatchEvent(event);
            });
            const button = page.locator('header').getByRole('button', { name: label });
            check(engine, `${size} ${mode}: the header offers installation`, await button.isVisible().catch(() => false));
            await button.hover().catch(() => {});
            await sleep(600);
            await shot(page, `install-header-${engine}-${size}-${mode}`);
            await button.click();
            await sleep(500);
            check(engine, `${size} ${mode}: after installing, the button is gone`, !(await button.isVisible().catch(() => false)));
          } else {
            await page.getByRole('button', { name: /open menu|close menu|open drawer/i }).first().click();
            await sleep(600);
            const button = page.getByRole('button', { name: label });
            check(engine, `iPhone ${mode}: the menu's footer offers installation`, await button.isVisible().catch(() => false));
            await shot(page, `install-menu-${engine}-${mode}`);
            await button.click();
            const dialog = page.getByRole('dialog');
            await sleep(700); // the dialog fades in
            check(engine, `iPhone ${mode}: the steps are shown`, await dialog.getByText('Add to Home Screen').first().isVisible().catch(() => false));
            await shot(page, `install-dialog-${engine}-${mode}`);
            await dialog.getByRole('button', { name: 'Not now' }).click();
            await sleep(600);
            check(engine, `iPhone ${mode}: "Not now" hides the offer`, !(await button.isVisible().catch(() => false)));
          }
          await context.close();
        }
      }
    } finally {
      await browser.close();
    }
  },

  async fresh(engine) {
    for (const size of ['phone', 'desktop']) {
      const { page, close } = await open(engine, size, modes[0], false);
      const errors = [];
      page.on('pageerror', (e) => errors.push(String(e.message).slice(0, 200)));
      try {
        await login(page);
        await sleep(1500);
        const state = await kept(page);
        check(engine, `${size}: first visit, worker registered`, state.registrations.length === 1, state.registrations);
        check(engine, `${size}: no script error`, errors.length === 0, errors);
      } finally {
        await close();
      }
    }
  },
};

for (const engine of engineNames) {
  try {
    await phases[phase](engine);
  } catch (error) {
    check(engine, 'phase ran to the end', false, String(error && error.stack || error).slice(0, 600));
  }
}
writeFileSync(join(out, `results-${phase}.json`), JSON.stringify(results, null, 2));
console.log(`${phase}: ${results.length - failed} passed, ${failed} failed`);
process.exit(failed ? 1 : 0);

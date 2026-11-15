// Fork addition: the browser smoke of the CI. scripts/fork-ci-smoke.sh starts
// a throwaway instance of the build and runs this inside the Playwright image;
// see that file for how. Never point it at the production instance: it
// finishes the setup wizard, makes users and saves settings.
//
// It fails (exit 1) when, in Chromium at 1366x900 and 390x844:
//   - a page throws a script error or the script policy (CSP) refuses something,
//     on the pages walked here or on the gallery's core and guarded views
//     (scripts/fork-ui-gallery.mjs, SET=smoke, CHECK=1);
//   - the sign-in form does not sign in, or a settings save is not read back;
//   - a page that can break without a word shows nothing: the Storage Disks tree
//     must list the fixtures' disks (an empty tree is how @mui/lab's stubs
//     broke it), the monitoring charts must draw a series when one of their
//     metrics has no data (apexcharts threw "reading 'group'" then).
// The cron job page has no tree (newJob.jsx imports one but draws none), so
// it is only walked.
//
// Environment: BASE (default http://127.0.0.1:18080), OUT (default ./fork-ci-smoke-out),
// VIEWPORTS (default 1366x900,390x844), GALLERY=0 to skip the gallery, SEED=0 to skip the seed.
import { chromium } from 'playwright';
import { spawn } from 'node:child_process';
import { mkdirSync, writeFileSync, readFileSync, existsSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { DISKS, storageMock } from './fork-ui-gallery-fixtures.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const base = process.env.BASE || 'http://127.0.0.1:18080';
const out = process.env.OUT || 'fork-ci-smoke-out';
const viewports = (process.env.VIEWPORTS || '1366x900,390x844').split(',').map((v) => { const [width, height] = v.split('x').map(Number); return { width, height }; });
const admin = { nickname: 'admin', password: process.env.ADMIN_PASSWORD || 'Test-Passw0rd!x' };
const note = (...a) => console.log(...a);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// What a test instance shows that the owner's server does not; each one exactly, with its reason. Nothing
// else is let through.
const KNOWN = [
  // the instance has no Docker on purpose: the volumes, networks and install pages ask it anyway and the
  // panel lets the refusal through as an unhandled rejection
  /^(error: )?Internal server error: Docker Connection - Cannot ping Daemon\. Is it running\?$/,
  // the panel checks in the background whether each app's address answers (?__cosmos_probe=1); the
  // seeded apps' names lead nowhere on a test instance, and the browser logs the refused answer on
  // whatever page is open when it comes back (seen once in five runs on Storage)
  /^(console: )?Access to fetch at 'http:\/\/[a-z0-9-]+\.localhost:\d+\/\?__cosmos_probe=1' from origin 'http:\/\/(localhost|127\.0\.0\.1):\d+' has been blocked by CORS policy: No 'Access-Control-Allow-Origin' header is present on the requested resource\.$/,
];
const known = (message) => KNOWN.some((k) => k.test(message));
mkdirSync(join(out, 'shots'), { recursive: true });

const started = Date.now();
const failures = [];
const results = [];
const fail = (check, why) => { failures.push(`${check}: ${why}`); note('FAIL', check, '-', why); };
const pass = (check, detail = '') => note('ok  ', check, detail);

// a child script (the seed, a gallery run), with its output kept
const runScript = (name, file, env) => new Promise((resolve) => {
  const t0 = Date.now();
  const child = spawn(process.execPath, [join(here, file)], { cwd: dirname(here), env: { ...process.env, ...env }, stdio: ['ignore', 'pipe', 'pipe'] });
  const log = [];
  const take = (d) => { const s = d.toString(); log.push(s); process.stdout.write(s.split('\n').filter(Boolean).map((l) => `  [${name}] ${l}\n`).join('')); };
  child.stdout.on('data', take);
  child.stderr.on('data', take);
  child.on('close', (code) => { writeFileSync(join(out, name + '.log'), log.join('')); resolve({ code, seconds: Math.round((Date.now() - t0) / 1000) }); });
});

// --- the instance: the script that started it waited for its first answer; the seed finishes the
// wizard (and waits for the restart it causes), signs in and fills it
if (process.env.SEED !== '0') {
  const seed = await runScript('seed', 'fork-ui-gallery-seed.mjs', { BASE: base, LIVE_TARGET: 'http://192.0.2.2:8989' });
  results.push({ step: 'seed', ...seed });
  if (seed.code !== 0) fail('seed', `the seed ended with ${seed.code} (seed.log)`);
}

// --- what every page is watched for
const watch = (page, label) => {
  // `where`: the page being checked, named in what is reported
  const seen = { errors: [], csp: [], where: '' };
  page.on('pageerror', (e) => known(String(e.message)) || seen.errors.push(`${label} ${seen.where}: ${String(e.message).slice(0, 300)}`));
  page.on('console', (m) => {
    const text = m.text();
    if (text.startsWith('fork-ci-smoke csp:') || /Content.Security.Policy|Refused to (load|execute|apply|connect|frame|evaluate|create)/i.test(text)) seen.csp.push(`${label} ${seen.where}: ${text.slice(0, 300)}`);
  });
  return seen;
};
const newContext = async (browser, viewport, blockWorkers) => {
  const context = await browser.newContext({ viewport, reducedMotion: 'reduce', locale: 'en-GB', timezoneId: 'Europe/Bucharest', ...(blockWorkers ? { serviceWorkers: 'block' } : {}) });
  // the script policy's refusals as events (an init script is not subject to the policy)
  await context.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => console.warn('fork-ci-smoke csp: ' + e.violatedDirective + ' blocked ' + (e.blockedURI || '(inline)') + ' in ' + e.sourceFile + ':' + e.lineNumber));
  });
  return context;
};
// (not 'networkidle': the panel polls, so the network is never quiet for long)
const settle = async (page, extra = 0) => {
  await page.waitForLoadState('load', { timeout: 15000 }).catch(() => {});
  for (let i = 0; i < 40; i++) {
    if ((await page.locator('main').innerText({ timeout: 1000 }).catch(() => '')).trim().length > 20) break;
    await page.waitForTimeout(200);
  }
  await page.waitForTimeout(800 + extra);
};
const mainText = (page) => page.locator('main').innerText({ timeout: 5000 }).catch(() => '');
const shoot = (page, name) => page.screenshot({ path: join(out, 'shots', name + '.png'), fullPage: true }).catch(() => {});
const apiFetch = (page, method, path, body) => page.evaluate(([method, path, body]) => fetch('/cosmos/api/' + path, {
  method, headers: { 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body),
}).then(async (r) => ({ status: r.status, json: await r.json().catch(() => null) })), [method, path, body]);
// the panel's tab list: tabs on a desktop, a select on a phone (as in the gallery)
const openTab = async (page, phone, name) => {
  if (!phone) { await page.locator('main [role=tablist] [role=tab]', { hasText: name }).first().click({ timeout: 8000 }); return; }
  await page.locator('main .MuiSelect-select').first().click({ timeout: 8000 });
  await page.locator('[role=listbox] li', { hasText: name }).first().click({ timeout: 8000 });
};

const signIn = async (page, label) => {
  await page.locator('#nickname-login').fill(admin.nickname);
  await page.locator('#-password-login').fill(admin.password);
  await page.locator('button[type=submit]').first().click();
  const left = await page.waitForURL((url) => !/\/login/.test(url.pathname), { timeout: 20000 }).then(() => true, () => false);
  const me = await apiFetch(page, 'GET', 'me');
  if (!left || me.status !== 200) { fail(`${label} sign-in`, `the form did not sign in (still on ${new URL(page.url()).pathname}, /api/me ${me.status})`); return false; }
  pass(`${label} sign-in`);
  // most admin pages ask for the password a second time
  const sudo = await apiFetch(page, 'POST', 'sudo', { password: admin.password });
  if (sudo.status !== 200) fail(`${label} sudo`, `the password confirmation answered ${sudo.status}`);
  return true;
};

const browser = await chromium.launch();
for (const viewport of viewports) {
  const phone = viewport.width < 600;
  const label = `${viewport.width}x${viewport.height}`;

  // ---- sign-in, the main areas, one settings save: as a user has it (service worker allowed)
  {
    const context = await newContext(browser, viewport, false);
    const page = await context.newPage();
    const seen = watch(page, label);
    // Retry (reason): the first load of the panel installs its service worker, which takes control
    // and can reload the page under the first navigation. Only the navigation is retried.
    for (let attempt = 1; ; attempt++) {
      try {
        await page.goto(base + '/cosmos-ui/login', { waitUntil: 'domcontentloaded' });
        await page.locator('#nickname-login').waitFor({ timeout: 15000 });
        break;
      } catch (e) {
        if (attempt >= 3) { fail(`${label} first load`, String(e.message).split('\n')[0]); break; }
        note(`  ${label} first load, attempt ${attempt} failed (${String(e.message).split('\n')[0].slice(0, 120)}): retrying`);
        await sleep(3000);
      }
    }
    await shoot(page, `${label}-login`);
    if (await signIn(page, label)) {
      // one page of each main area: it must render something of its own, without a script error
      const areas = [
        ['home', '/cosmos-ui/'], ['servapps', '/cosmos-ui/servapps'], ['market', '/cosmos-ui/market-listing'],
        // (Storage is checked with the fixtures below: what it shows unanswered depends on the machine's lsblk)
        ['monitoring', '/cosmos-ui/monitoring'], ['urls', '/cosmos-ui/config-url'],
        ['users', '/cosmos-ui/config-users'], ['openid', '/cosmos-ui/openid-manage'], ['cron', '/cosmos-ui/cron'],
        ['config', '/cosmos-ui/config-general/general'], ['security', '/cosmos-ui/security'], ['audit', '/cosmos-ui/security-audit'],
        ['notifications', '/cosmos-ui/notifications'],
      ];
      for (const [name, path] of areas) {
        const before = seen.errors.length + seen.csp.length;
        seen.where = 'area ' + name;
        await page.goto(base + path, { waitUntil: 'domcontentloaded' });
        await settle(page, 600);
        await shoot(page, `${label}-area-${name}`);
        const text = (await mainText(page)).trim();
        const fresh = seen.errors.length + seen.csp.length - before;
        if (text.length < 20) fail(`${label} area ${name}`, `the page shows almost nothing (${text.length} characters in main)`);
        else if (!fresh) pass(`${label} area ${name}`, `${text.length} chars`);
        results.push({ step: `area ${name}`, viewport: label, chars: text.length, errors: fresh });
      }

      // a save of a harmless setting through the form, read back from the server
      if (!phone) {
        seen.where = 'settings save';
        await page.goto(base + '/cosmos-ui/config-general/general', { waitUntil: 'domcontentloaded' });
        await settle(page, 500);
        const field = page.locator('input[name=MaintenanceTime]');
        try {
          const was = await field.inputValue({ timeout: 10000 });
          const want = was === '04:15' ? '04:20' : '04:15';
          await field.scrollIntoViewIfNeeded();
          await field.fill(want);
          await field.blur();
          const saved = page.waitForResponse((r) => r.url().endsWith('/cosmos/api/config') && r.request().method() !== 'GET', { timeout: 20000 });
          await page.getByRole('button', { name: /^Save$/ }).first().click();
          const answer = await saved;
          // the server restarts its listener after a save: ask until it answers again
          let config = null;
          for (let i = 0; i < 30 && !config; i++) {
            await sleep(1000);
            const r = await apiFetch(page, 'GET', 'config').catch(() => ({ status: 0 }));
            if (r.status === 200 && r.json && r.json.data) config = r.json.data;
          }
          await shoot(page, `${label}-settings-saved`);
          if (answer.status() !== 200) fail('settings save', `the save answered ${answer.status()} (${answer.request().method()})`);
          else if (!config) fail('settings save', 'the configuration could not be read back after the save');
          else if (config.MaintenanceTime !== want) fail('settings save', `read back MaintenanceTime ${JSON.stringify(config.MaintenanceTime)}, saved ${want}`);
          else pass('settings save', `MaintenanceTime ${was} -> ${want}, ${answer.request().method()} ${answer.status()}, read back`);
        } catch (e) { fail('settings save', String(e.message).split('\n')[0].slice(0, 200)); }
      }
    }
    for (const e of seen.errors) fail('script error', e);
    for (const e of seen.csp) fail('script policy', e);
    await context.close();
  }

  // ---- content a broken page shows without an error (service worker blocked: answers come from here)
  {
    const context = await newContext(browser, viewport, true);
    const page = await context.newPage();
    const seen = watch(page, label);
    await page.goto(base + '/cosmos-ui/login', { waitUntil: 'domcontentloaded' });
    if (await signIn(page, label + ' (fixtures)')) {
      // Storage > Disks with the gallery's disks: every disk is an item of the tree
      seen.where = 'storage tree';
      for (const [part, body] of storageMock) await page.route((url) => url.pathname.startsWith(part), (route) => route.fulfill({ json: body }));
      await page.goto(base + '/cosmos-ui/storage', { waitUntil: 'domcontentloaded' });
      await settle(page, 800);
      await shoot(page, `${label}-storage-disks`);
      const items = await page.locator('main [role=treeitem]').count();
      const text = await mainText(page);
      const missing = DISKS.filter((d) => !text.includes(d.name.replace('/dev/', '')) && !text.includes(d.name));
      if (items < DISKS.length || missing.length) fail(`${label} storage tree`, `the Disks tree shows ${items} items for the fixtures' ${DISKS.length} disks${missing.length ? ', missing ' + missing.map((d) => d.name).join(' ') : ''}`);
      else pass(`${label} storage tree`, `${items} items, every disk named`);
      await page.unrouteAll({ behavior: 'ignoreErrors' });

      // the monitoring charts, as the server answers (a fresh instance: no data for most metrics),
      // then with the error series left out of every answer (an app that never failed)
      // (`series`: a chart must draw one; an app without traffic has no data at all, its charts stay empty)
      const charts = async (name, path, tab, withoutErrors, needSeries) => {
        seen.where = 'charts ' + name;
        if (withoutErrors) {
          await page.route((url) => url.pathname === '/cosmos/api/metrics', async (route) => {
            try {
              const answer = await route.fetch();
              const json = await answer.json();
              if (Array.isArray(json.data)) json.data = json.data.filter((m) => !/error/i.test(String(m && m.Key)));
              return route.fulfill({ response: answer, json });
            } catch (e) { return route.continue().catch(() => {}); }
          });
        }
        const before = seen.errors.length;
        await page.goto(base + path, { waitUntil: 'domcontentloaded' });
        await settle(page, 1500);
        if (tab) { try { await openTab(page, phone, tab); await settle(page, 1500); } catch (e) { fail(`${label} charts ${name}`, `the tab "${tab}" could not be opened: ${String(e.message).split('\n')[0].slice(0, 120)}`); } }
        await shoot(page, `${label}-charts-${name}`);
        const drawn = await page.locator('main [role=tabpanel]:not([hidden]) .apexcharts-canvas, main .apexcharts-canvas').count();
        const series = await page.locator('main .apexcharts-series').count();
        const thrown = seen.errors.length - before;
        if (thrown) fail(`${label} charts ${name}`, `${thrown} script errors while drawing (first: ${seen.errors[before]})`);
        else if (!drawn || (needSeries && !series)) fail(`${label} charts ${name}`, `no chart drawn (${drawn} charts, ${series} series)`);
        else pass(`${label} charts ${name}`, `${drawn} charts, ${series} series`);
        results.push({ step: `charts ${name}`, viewport: label, charts: drawn, series, errors: thrown });
        if (withoutErrors) await page.unrouteAll({ behavior: 'ignoreErrors' });
      };
      await charts('server', '/cosmos-ui/monitoring', null, false, true);
      await charts('proxy', '/cosmos-ui/monitoring', 'Proxy', false, true);
      await charts('proxy-no-error-data', '/cosmos-ui/monitoring', 'Proxy', true, true);
      await charts('url-no-data', '/cosmos-ui/config-url/Jellyfin', 'Monitoring', true, false);
      // the script errors were reported per chart above
      seen.errors.length = 0;
    }
    for (const e of seen.errors) fail('script error', e);
    for (const e of seen.csp) fail('script policy', e);
    await context.close();
  }
}
await browser.close();

// --- the gallery's core and guarded views (fixtures for Storage, Users, notifications, events), one
// process per screen size at the same time; the server allows 500 API calls a minute per address and
// both come from the same one, so each keeps under half of it
if (process.env.GALLERY !== '0') {
  const runs = await Promise.all(viewports.map(async (v) => {
    const size = `${v.width}x${v.height}`;
    const dir = join(out, 'gallery', size);
    const r = await runScript('gallery-' + size, 'fork-ui-gallery.mjs', {
      BASE: base, OUT: dir, ENGINES: 'chromium', VIEWPORTS: size, MODES: 'light', SET: 'smoke', CHECK: '1', TIMEOUT: '8000',
      KNOWN_MESSAGES: KNOWN.map((k) => `(?:${k.source})`).join('|'),
      API_PER_MINUTE: String(Math.floor(460 / viewports.length)),
    });
    return { size, dir, ...r };
  }));
  for (const g of runs) {
    results.push({ step: 'gallery ' + g.size, code: g.code, seconds: g.seconds });
    if (g.code !== 0) fail(`gallery ${g.size}`, `CHECK=1 failed or the gallery ended with ${g.code} (gallery-${g.size}.log, "CHECK FAILED")`);
    const summary = join(g.dir, 'summary.json');
    if (!existsSync(summary)) { fail(`gallery ${g.size}`, 'no summary.json'); continue; }
    const rows = JSON.parse(readFileSync(summary, 'utf8')).captures;
    if (rows.length < 20) fail(`gallery ${g.size}`, `only ${rows.length} captures`);
    // every capture, guarded or not: no script error, no refusal of the script policy (both in `errors`)
    let ignored = 0;
    for (const r of rows.filter((x) => x.errors)) {
      const s = JSON.parse(readFileSync(join(g.dir, r.run, r.view + '.json'), 'utf8'));
      const real = s.errors.filter((e) => !known(e));
      ignored += s.errors.length - real.length;
      if (real.length) fail(`gallery ${r.run}/${r.view}`, real.slice(0, 2).join(' | '));
    }
    pass(`gallery ${g.size}`, `${rows.length} captures, ${rows.filter((x) => x.guard).length} guarded, ${rows.filter((x) => x.problems).length} with a step that could not be done (reported, not failed), ${ignored} known messages of a test instance let through, ${g.seconds} s`);
  }
}

const seconds = Math.round((Date.now() - started) / 1000);
writeFileSync(join(out, 'smoke.json'), JSON.stringify({ base, ok: !failures.length, seconds, failures, results }, null, 1));
const lines = [failures.length ? `SMOKE FAILED (${failures.length})` : 'SMOKE OK', ...failures.map((f) => '  ' + f), `${seconds} s`];
writeFileSync(join(out, 'summary.txt'), lines.join('\n') + '\n');
note('\n' + lines.join('\n'));
if (failures.length) process.exitCode = 1;

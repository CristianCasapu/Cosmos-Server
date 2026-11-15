// Fork addition: checks in Chromium, Firefox and WebKit that the panel's
// commands go out as POST (src/fork_post_only.go) and work, and that the same
// commands sent with GET from a panel page are refused with 405.
//
// Needs a throwaway instance (CLAUDE.md, "Checking the UI in a browser") whose
// Docker is a docker:29-dind of its own holding a running container named
// cmd-app (busybox sleeping); admin / Test-Passw0rd!x. Never the production
// instance.
//
//   BASE=http://cmd.test ENGINES=chromium,firefox,webkit SIZES=d,m node fork-ui-check-commands.mjs
//
// Writes shots/commands-<engine>-<size>-*.png and prints one line per check;
// exits 1 when one failed.
import { chromium, firefox, webkit } from 'playwright';
import { mkdirSync } from 'node:fs';

const base = process.env.BASE || 'http://cmd.test';
const engines = { chromium, firefox, webkit };
const engineNames = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const sizes = { d: { width: 1366, height: 900 }, m: { width: 390, height: 844 } };
const sizeNames = (process.env.SIZES || 'd,m').split(',');
const app = process.env.APP || 'cmd-app';
const commandPath = /\/cosmos\/api\/(servapps\/[^/]+\/(manage|auto-update|secure|lazy)\/|notifications\/read|logout|restart|reset-metrics|images\/pull|mfa|snapraid\/[^/]+\/)/;
mkdirSync('shots', { recursive: true });

let failed = 0;
const check = (ok, ...what) => { if (!ok) failed++; console.log(ok ? 'ok  ' : 'FAIL', ...what); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

for (const engineName of engineNames) {
  for (const sizeName of sizeNames) {
    const tag = `${engineName}-${sizeName}`;
    const browser = await engines[engineName].launch();
    const ctx = await browser.newContext({ viewport: sizes[sizeName], ignoreHTTPSErrors: true });
    const page = await ctx.newPage();
    const errors = [];
    const sent = [];
    page.on('pageerror', (e) => errors.push('pageerror: ' + e.message.slice(0, 200)));
    page.on('console', (m) => { if (m.type() === 'error') errors.push('console: ' + m.text().slice(0, 200)); });
    page.on('response', (r) => {
      const url = new URL(r.url());
      if (commandPath.test(url.pathname)) sent.push(`${r.request().method()} ${url.pathname} ${r.status()}`);
    });

    let r = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: 'Test-Passw0rd!x' } });
    check(r.ok(), tag, 'login', r.status());
    r = await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: 'Test-Passw0rd!x' } });
    check(r.ok(), tag, 'password confirmed', r.status());

    await page.goto(base + `/cosmos-ui/servapps/containers/${app}`, { waitUntil: 'domcontentloaded' });
    await page.getByRole('button', { name: 'Stop', exact: true }).waitFor({ timeout: 30000 });
    await sleep(1500);
    await page.screenshot({ path: `shots/commands-${tag}-container.png` });

    // stop and start the container from its action bar
    await page.getByRole('button', { name: 'Stop', exact: true }).click();
    await page.getByRole('button', { name: 'Start', exact: true }).waitFor({ timeout: 30000 });
    await page.getByRole('button', { name: 'Start', exact: true }).click();
    await page.getByRole('button', { name: 'Stop', exact: true }).waitFor({ timeout: 30000 });
    await sleep(1000);
    check(sent.some((s) => s === `POST /cosmos/api/servapps/${app}/manage/stop 200`), tag, 'Stop sent as POST and done');
    check(sent.some((s) => s === `POST /cosmos/api/servapps/${app}/manage/start 200`), tag, 'Start sent as POST and done');

    // automatic updates on and off (the switch recreates the container)
    const autoUpdate = () => page.getByLabel('Auto Update Container');
    for (let round = 0; round < 2; round++) {
      const state = (await autoUpdate().isChecked()) ? 'false' : 'true';
      await autoUpdate().click();
      const deadline = Date.now() + 60000;
      while (!sent.some((s) => s.startsWith(`POST /cosmos/api/servapps/${app}/auto-update/${state} `)) && Date.now() < deadline) await sleep(500);
      check(sent.some((s) => s === `POST /cosmos/api/servapps/${app}/auto-update/${state} 200`), tag, `automatic updates ${state} sent as POST and done`);
      await page.goto(base + `/cosmos-ui/servapps/containers/${app}`, { waitUntil: 'domcontentloaded' });
      await page.getByText('Auto Update Container').waitFor({ timeout: 30000 });
      await sleep(1500);
    }
    await page.screenshot({ path: `shots/commands-${tag}-after.png` });

    // the same commands asked with GET from a panel page: refused
    const viaGet = await page.evaluate(async (a) => {
      const out = {};
      for (const path of [`/cosmos/api/servapps/${a}/manage/stop`, `/cosmos/api/servapps/${a}/auto-update/true`, `/cosmos/api/servapps/${a}/secure/true`, `/cosmos/api/servapps/${a}/lazy/true`,
        '/cosmos/api/restart', '/cosmos/api/restart-server', '/cosmos/api/reset-metrics', '/cosmos/api/notifications/read?ids=1', '/cosmos/api/images/pull?imageName=busybox:1.36', '/cosmos/api/mfa', '/cosmos/api/logout']) {
        out[path] = (await fetch(path, { method: 'GET' })).status;
      }
      out['POST notifications/read'] = (await fetch('/cosmos/api/notifications/read?ids=0', { method: 'POST' })).status;
      return out;
    }, app);
    for (const [path, status] of Object.entries(viaGet)) {
      if (path.startsWith('POST ')) check(status !== 405 && status !== 401 && status !== 403, tag, path, 'reaches the handler', status); // 404: no notification 0
      else check(status === 405, tag, 'GET', path, status);
    }
    check(!sent.some((s) => / 200$/.test(s) && s.startsWith('GET ')), tag, 'no command answered a GET');

    // logging out from the panel
    await page.goto(base + '/cosmos-ui/logout', { waitUntil: 'domcontentloaded' });
    await sleep(4000);
    check(sent.some((s) => s === 'POST /cosmos/api/logout 200'), tag, 'logout sent as POST and done');
    r = await ctx.request.get(base + '/cosmos/api/me');
    check(r.status() === 401, tag, 'session ended after logout', r.status());
    await page.screenshot({ path: `shots/commands-${tag}-logout.png` });

    // the worker is refused behind a certificate the browser does not trust (a test instance's own): not a fault
    const relevant = errors.filter((e) => !/Failed to load resource.*(405|404|401)/.test(e) && !/SSL certificate error occurred when fetching the script/.test(e));
    check(relevant.length === 0, tag, 'no script error', relevant.join(' | ').slice(0, 600));
    console.log('     sent:', sent.join(' ; '));
    await browser.close();
    await sleep(3000);
  }
}
process.exit(failed ? 1 : 0);

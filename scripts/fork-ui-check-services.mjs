// Fork addition (plan 43): apps that are services, not web apps, and apps
// that do not start. Headless Chromium, Firefox and WebKit at 1366x900 and
// 393x851, light and dark, against a throwaway Cosmos with a Docker of its
// own. Never the production instance, never the real Docker Hub.
//
// Setup, on one --internal Docker network with a documentation range
// (198.51.100.0/24, which the image reader's guard treats as the internet):
// - docker:29-dind at .5 (DIND), started with --privileged -e DOCKER_TLS_CERTDIR=,
//   whose system bundle trusts a test authority and whose /etc/hosts maps
//   registry-1.docker.io and index.docker.io to .7 and hub.docker.com to .6;
//   the test Cosmos runs INSIDE it (as on a server where Cosmos runs outside
//   Docker: the proxy's own path to containers), built with
//   -tags anyimage_testnet, COSMOS_HTTP_PORT=2052, hostname
//   cosmos-test.example.org (HTTP only), SSL_CERT_FILE the test authority,
//   COSMOS_ANYIMAGE_TEST_ALLOW=198.51.100.0/24; its log at LOG;
// - registry:3 over HTTPS at .7 holding bitnami/postgresql:latest,
//   library/nginx:1-alpine and library/busybox:1.36;
// - scripts/fork-fake-dockerhub.go at .6 with DESCRIPTIONS set to
//   src/anyimage/testdata/readmes (the repository's description);
// - in the Docker in Docker: "web1" (nginx:1-alpine, running) and
//   "postgresql-old" (bitnami/postgresql:latest created as the owner had it:
//   no variable, restart unless-stopped: it restarts forever).
// The browser container: --network <net> --add-host cosmos-test.example.org:198.51.100.5,
// the scratch run folder mounted read-only at /cosmos (for LOG).
//
// PHASE=story   once, Chromium 1366 light, the Cosmos started with
//               COSMOS_ANYIMAGE_TEST_NO_RECIPES=1: "bitnami/postgresql" typed by
//               name, the review proposes its password from the description, it
//               runs and takes that password; the Why? of postgresql-old and its
//               repair; New URL and Internet for the database; a web app's URL as
//               before; the proxy answering for an app that does not run.
// PHASE=bad     once, the Cosmos started with ..._NO_RECIPES=1 and
//               ..._NO_DESCRIPTIONS=1: the same image gets no password, the
//               creation's check names POSTGRESQL_PASSWORD, Apply, it runs.
// PHASE=look    every ENGINES x SIZES x MODES: pictures of each view.
import { chromium, firefox, webkit, request } from 'playwright';
import { readFileSync } from 'node:fs';

const base = process.env.BASE || 'http://cosmos-test.example.org:2052';
const dind = process.env.DIND || 'http://198.51.100.5:2375';
const logFile = process.env.LOG || '/cosmos/cosmos.log';
const password = process.env.PASSWORD || 'Test-Passw0rd!x';
const phase = process.env.PHASE || 'look';
const engines = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const sizes = (process.env.SIZES || 'd,m').split(',');
const modes = (process.env.MODES || 'light,dark').split(',');

const note = (...a) => console.log(...a);
const failures = [];
const expect = (ok, what) => { note(ok ? '  ok  ' : '  FAIL', what); if (!ok) failures.push(what); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const api = await request.newContext();
const until = async (what, check, seconds = 90) => {
  for (let i = 0; i < seconds; i++) {
    try { if (await check()) return true; } catch (e) { /* reloading */ }
    await sleep(1000);
  }
  expect(false, 'timed out: ' + what);
  return false;
};
const inspect = async (name) => {
  const answer = await api.get(`${dind}/containers/${name}/json`);
  return answer.status() === 200 ? answer.json() : null;
};
const run = async (name, cmd, env = []) => {
  const made = await (await api.post(`${dind}/containers/${name}/exec`, { data: { Cmd: cmd, Env: env, AttachStdout: true, AttachStderr: true } })).json();
  const out = await api.post(`${dind}/exec/${made.Id}/start`, { data: { Detach: false, Tty: true } });
  return (await out.text()).replace(/[^\x20-\x7e\n]/g, '');
};
const logLines = () => readFileSync(logFile, 'utf8').split('\n');
const psqlWorks = async (container, secret) => {
  const psql = ['sh', '-c', 'psql -h "$(hostname -i | cut -d" " -f1)" -U postgres -tAc "select 40+2" 2>&1; echo "exit=$?"'];
  const good = await run(container, psql, [`PGPASSWORD=${secret}`]);
  const bad = await run(container, psql, ['PGPASSWORD=wrong-one']);
  return { good: /42/.test(good), bad: !/exit=0/.test(bad), text: good.trim().slice(0, 100) };
};

const session = await request.newContext({ baseURL: base });
await session.post('/cosmos/api/login', { data: { nickname: 'admin', password } });
await session.post('/cosmos/api/sudo', { data: { password } });

const open = async (type, name, viewport, mode) => {
  const browser = await type.launch();
  const ctx = await browser.newContext({ viewport, colorScheme: mode, serviceWorkers: 'block' });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => {
    if (/Failed to fetch|Load failed|NetworkError/.test(e.message)) return; // the server reloads after a new URL
    failures.push(`${name} PAGEERROR: ${(e.stack || e.message).slice(0, 600)}`);
  });
  page.consoleLines = [];
  page.on('console', (m) => page.consoleLines.push(m.text()));
  const a = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  const b = await ctx.request.post(base + '/cosmos/api/sudo', { data: { password } });
  note(`\n== ${name}: login ${a.status()}/${b.status()}`);
  return { browser, ctx, page };
};
const shot = (page, name, what) => page.screenshot({ path: `shots/${name}-${what}.png`, fullPage: false });
const noSideScroll = async (page, name, where) => {
  const over = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(over <= 1, `${name}: no sideways scroll on ${where} (${over}px over)`);
};
const fits = async (page, name, selector, where) => {
  const dialog = page.locator(selector).last();
  const box = await dialog.locator('.MuiDialog-paper').boundingBox();
  expect(!!box && box.x >= -1 && box.x + box.width <= page.viewportSize().width + 1, `${name}: the dialog fits the screen (${where})`);
  const inside = await dialog.evaluate((d) => {
    const content = d.querySelector('.MuiDialogContent-root');
    if (!content) return 'no content';
    if (content.scrollWidth > content.clientWidth + 1) return `content ${content.scrollWidth} > ${content.clientWidth}`;
    return '';
  });
  expect(inside === '', `${name}: nothing sticks out of the dialog (${where}) ${inside}`);
};
const later = async (page) => {
  const button = page.getByRole('button', { name: 'Later' });
  if (await button.count() && await button.first().isVisible()) await button.first().click();
};
const card = async (page, app) => {
  await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
  const search = page.getByPlaceholder('Search...');
  await search.waitFor({ timeout: 45000 });
  await page.waitForTimeout(1500);
  await search.fill(app);
  await page.waitForTimeout(1000);
  await later(page);
};
// the tile of one app (a search for "postgresql" also finds "postgresql-old")
const tile = (page, app) => page.locator('.MuiPaper-root').filter({ has: page.locator(`.ct-app-name[title="${app}"]`) }).last();
const newUrl = async (page, app) => {
  await card(page, app);
  await tile(page, app).locator('.MuiChip-root', { has: page.locator('.MuiChip-label', { hasText: /^New$/ }) }).first().click();
  await page.locator('[data-new-url-dialog]').waitFor({ timeout: 30000 });
  await page.waitForTimeout(1500);
  return page.locator('[data-new-url-dialog]');
};
const describeInDialog = async (page, reference, containerName) => {
  await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
  await page.locator('[data-any-image-open]').first().waitFor({ timeout: 45000 });
  await later(page);
  await page.locator('[data-any-image-open]').first().click();
  await page.locator('[data-any-image-field] input').fill(reference);
  await page.locator('[data-any-image-search]').click();
  const result = page.locator(`[data-result="${reference}"]`);
  await Promise.race([result.waitFor({ timeout: 30000 }), page.locator('[data-any-image-name]').waitFor({ timeout: 30000 })]).catch(() => {});
  if (await result.count()) await result.click();
  await page.locator('[data-any-image-name] input').waitFor({ timeout: 30000 });
  if (containerName) await page.locator('[data-any-image-name] input').fill(containerName);
  await page.locator('[data-any-image-review] [data-creation-review]').waitFor({ timeout: 30000 });
  await page.waitForTimeout(1500);
};

if (phase === 'story' || phase === 'bad') {
  const name = `${phase}-chromium-1366-light`;
  const { browser, page } = await open(chromium, name, { width: 1366, height: 900 }, 'light');
  try {
    if (phase === 'story') {
      // --- 1. the owner's case, typed by name, no recipe allowed to help
      const desc = (await (await session.post('/cosmos/api/images/describe', { data: { reference: 'bitnami/postgresql' } })).json()).data;
      expect(!desc.recipe && desc.hints && desc.hints.source === 'description', `describe bitnami/postgresql: no recipe, the description read (${desc.hints && desc.hints.source})`);
      await describeInDialog(page, 'bitnami/postgresql', 'postgresql');
      const field = page.locator('[data-setting="POSTGRESQL_PASSWORD"]');
      expect(await field.count() === 1, `${name}: the review asks for POSTGRESQL_PASSWORD, from the description`);
      const secret = await field.locator('input').inputValue();
      expect(/^[A-Za-z0-9]{24}$/.test(secret), `${name}: a generated password is proposed`);
      expect(/From its description/.test(await field.innerText()), `${name}: it says where the proposal comes from`);
      expect(await page.locator('[data-setting="ALLOW_EMPTY_PASSWORD"]').count() === 0, `${name}: the switch for an empty password is not proposed`);
      expect(/looks like PostgreSQL/.test(await page.locator('[data-image-needs]').innerText()), `${name}: the review says it is a database Cosmos has no settings for`);
      await shot(page, name, '01-review-from-description');
      await page.locator('[data-any-image-review] button.shinyButton').click();
      const ok = await until('postgresql runs', async () => { const c = await inspect('postgresql'); return c && c.State.Running && !c.State.Restarting && c.RestartCount === 0 && (Date.now() - Date.parse(c.State.StartedAt)) > 15000; }, 120);
      await page.waitForTimeout(16000);
      expect(await page.locator('[data-start-problem]').count() === 0, `${name}: the start check finds nothing wrong`);
      await shot(page, name, '02-created');
      if (ok) {
        const pg = await psqlWorks('postgresql', secret);
        expect(pg.good && pg.bad, `${name}: the database takes the proposed password and refuses another (${pg.text})`);
      }
      expect(!page.consoleLines.some((l) => l.includes(secret)), `${name}: the password is not on the console`);
      await page.keyboard.press('Escape').catch(() => {});

      // --- 2. the owner's old container: ServApps says why, the repair works
      await card(page, 'postgresql-old');
      const why = page.locator('[data-start-why="postgresql-old"]');
      await why.waitFor({ timeout: 30000 });
      await shot(page, name, '03-servapps-why');
      await why.click();
      await page.locator('[data-start-dialog] [data-start-reason]').waitFor({ timeout: 30000 });
      const reason = await page.locator('[data-start-dialog] [data-start-reason]').innerText();
      expect(/POSTGRESQL_PASSWORD/.test(reason), `${name}: ServApps says why postgresql-old does not run (${reason.slice(0, 120)})`);
      await page.locator('[data-start-dialog] [data-start-log-door]').click();
      const logText = await page.locator('[data-start-dialog] [data-start-log]').innerText();
      expect(/environment variable is empty or not set/.test(logText) && !/\x1b|\[38;5/.test(logText), `${name}: its log, without colours`);
      await shot(page, name, '04-why-dialog');
      const oldSecret = await page.locator('[data-start-dialog] [data-repair-setting="POSTGRESQL_PASSWORD"] input').inputValue();
      await page.locator('[data-start-dialog] [data-start-apply]').click();
      await page.locator('[data-start-dialog] [data-start-fixed]').waitFor({ timeout: 120000 }).catch(() => {});
      expect(await page.locator('[data-start-dialog] [data-start-fixed]').count() === 1, `${name}: after the repair postgresql-old runs`);
      await shot(page, name, '05-why-fixed');
      await sleep(5000);
      const old = await psqlWorks('postgresql-old', oldSecret);
      expect(old.good, `${name}: postgresql-old takes the password set by the repair (${old.text})`);
      await page.locator('[data-start-dialog] button', { hasText: 'Close' }).click();

      // --- 3. New URL for the database: no web address, its address for other apps, the real choices
      let dialog = await newUrl(page, 'postgresql');
      expect(await dialog.locator('[data-service-info]').count() === 1 && await dialog.locator('[data-new-url-confirm]').count() === 0, `${name}: New URL proposes no web address for the database`);
      expect((await dialog.locator('[data-service-address]').innerText()).trim() === 'postgresql:5432', `${name}: the address other apps use`);
      expect(/postgresql:\/\/USER:PASSWORD@postgresql:5432\/DATABASE/.test(await dialog.innerText()), `${name}: the connection string, without a password`);
      await shot(page, name, '06-new-url-database');
      await dialog.locator('[data-service-reach-door]').click();
      await dialog.locator('[data-service-reach] .MuiChip-root', { hasText: /^web1$/ }).click();
      await dialog.locator('[data-service-link]').click();
      await dialog.locator('[data-service-linked]').waitFor({ timeout: 30000 });
      const pgc = await inspect('postgresql');
      const web = await inspect('web1');
      const shared = Object.keys(pgc.NetworkSettings.Networks).filter((n) => n.startsWith('link-postgresql-web1-') && Object.keys(web.NetworkSettings.Networks).includes(n));
      expect(shared.length === 1, `${name}: "Let these apps reach it" made one network for both (${shared.join()})`);
      const ping = await run('web1', ['sh', '-c', 'nc -z -w 3 postgresql 5432 && echo reached']);
      expect(/reached/.test(ping), `${name}: web1 reaches postgresql:5432 by name`);
      await dialog.locator('[data-service-lan-door]').click();
      expect(await dialog.locator('[data-service-publish]').isDisabled(), `${name}: publishing waits for the tick`);
      expect(/not safe for a database/.test(await dialog.locator('[data-service-lan]').innerText()), `${name}: the risk is said`);
      await shot(page, name, '07-lan-risk');
      await dialog.locator('[data-service-lan-tick]').click();
      await dialog.locator('[data-service-publish]').click();
      await dialog.locator('[data-service-published]').waitFor({ timeout: 90000 }).catch(() => {});
      const published = await until('5432 published', async () => { const c = await inspect('postgresql'); return c && ((c.HostConfig.PortBindings || {})['5432/tcp'] || []).some((b) => b.HostPort === '5432'); }, 60);
      expect(published, `${name}: port 5432 published on the server after the tick`);
      await dialog.locator('[data-service-insist]').click();
      await dialog.locator('[data-new-url-confirm]').waitFor({ timeout: 10000 });
      await page.locator('[data-port-check]').waitFor({ timeout: 15000 }).catch(() => {});
      expect(await page.locator('[data-port-check]').count() === 1, `${name}: insisting on a web address warns with the port check (${await page.locator('[data-port-check]').getAttribute('data-port-check').catch(() => '')})`);
      await shot(page, name, '08-insist-warning');
      await page.keyboard.press('Escape').catch(() => {});
      await page.locator('[data-new-url-dialog] button', { hasText: 'Cancel' }).click().catch(() => {});

      // --- 4. a web app keeps its URL as before
      dialog = await newUrl(page, 'web1');
      expect(await dialog.locator('[data-service-info]').count() === 0 && await dialog.locator('[data-new-url-confirm]').count() === 1, `${name}: web1 gets the URL form as before`);
      await shot(page, name, '09-new-url-web');
      await page.locator('[data-new-url-dialog] button', { hasText: 'Cancel' }).click().catch(() => {});

      // --- 5. the Internet dialog of the database
      await card(page, 'postgresql');
      await tile(page, 'postgresql').locator('[data-expose-open]').first().click();
      await page.locator('[data-expose-dialog] [data-service-info]').waitFor({ timeout: 30000 }).catch(() => {});
      expect(await page.locator('[data-expose-dialog] [data-service-info]').count() === 1 && await page.locator('[data-expose-save]').count() === 0, `${name}: the Internet dialog says what the database is, proposes nothing for the internet`);
      await shot(page, name, '10-internet-database');
      await page.locator('[data-expose-dialog] button', { hasText: 'Close' }).click();

      // --- 6. the proxy: one request for an app that does not run gets one answer
      const config = (await (await session.get('/cosmos/api/config')).json()).data;
      const routes = config.HTTPConfig.ProxyConfig.Routes || [];
      const add = (r) => { if (!routes.some((x) => x.Name === r.Name)) routes.push(r); };
      const route = (Name, Host, Target, Mode = 'SERVAPP') => ({ Name, Description: '', UseHost: true, Host, UsePathPrefix: false, PathPrefix: '', Timeout: 14400000, ThrottlePerMinute: 0, CORSOrigin: '', StripPathPrefix: false, AuthEnabled: false, Target, Mode, SmartShield: { Enabled: true } });
      await run('web1', ['true']);
      await api.post(`${dind}/containers/create?name=crashing`, { data: { Image: 'busybox:1.36', Cmd: ['sh', '-c', 'sleep 1; exit 1'], HostConfig: { RestartPolicy: { Name: 'always' } } } });
      await api.post(`${dind}/containers/crashing/start`);
      add(route('crashing', 'crashing.example.org', 'http://crashing:'));
      add(route('self-loop', 'self-loop.example.org', 'http://127.0.0.1:2052', 'PROXY'));
      config.HTTPConfig.ProxyConfig.Routes = routes;
      await session.put('/cosmos/api/config', { data: config });
      await sleep(6000);
      const ask = async (host) => {
        const before = logLines().length;
        const res = await api.get('http://198.51.100.5:2052/', { headers: { Host: host, Accept: 'text/html' }, timeout: 15000 }).catch((e) => null);
        await sleep(1500);
        const added = logLines().slice(before);
        return { status: res && res.status(), body: res ? await res.text() : '', loops: added.filter((l) => /REQ/.test(l) && /from 127\.0\.0\.1/.test(l)).length, notes: added.filter((l) => l.includes('[proxy] URL')).length, noIP: added.filter((l) => /no IP address found/.test(l)).length };
      };
      await until('crashing restarts', async () => { const c = await inspect('crashing'); return c && c.RestartCount > 0; }, 30);
      let a = await ask('crashing.example.org');
      for (let i = 0; i < 5 && a.status === 502; i++) a = await ask('crashing.example.org'); // it may be up for a second
      expect(a.status === 503 && /restarting|not running/.test(a.body) && a.loops === 0 && a.noIP === 0, `the proxy: one answer for an app that is restarting (${a.status}, ${a.loops} requests from loopback, ${a.noIP} address errors)`);
      const b = await ask('crashing.example.org');
      expect(b.notes === 0, `the proxy: the second request logs nothing more (${b.notes} lines)`);
      const l = await ask('self-loop.example.org');
      expect(l.status === 508 && l.loops <= 1, `the proxy: a URL that points back at Cosmos is cut at once (${l.status}, ${l.loops} request(s) back)`);
      await page.goto('http://198.51.100.5:2052/').catch(() => {});
    }

    if (phase === 'bad') {
      // --- the bad old story: no recipe, no description: the image gets no password
      await describeInDialog(page, 'bitnami/postgresql', 'pgbad');
      expect(await page.locator('[data-setting="POSTGRESQL_PASSWORD"]').count() === 0, `${name}: without recipe and description nothing proposes the password`);
      expect(/could not be read/.test(await page.locator('[data-image-needs]').innerText()), `${name}: the review says Cosmos does not know what it needs`);
      await shot(page, name, '01-review-knows-nothing');
      await page.locator('[data-any-image-review] button.shinyButton').click();
      await page.locator('[data-start-problem]').waitFor({ timeout: 120000 }).catch(() => {});
      const problem = page.locator('[data-start-problem="pgbad"]');
      expect(await problem.count() === 1, `${name}: the creation says pgbad does not start`);
      const reason = await problem.locator('[data-start-reason]').innerText().catch(() => '');
      expect(/needs POSTGRESQL_PASSWORD/.test(reason), `${name}: and why (${reason.slice(0, 100)})`);
      expect(await page.getByText(/created, but it does not start/).count() > 0, `${name}: the toast does not say "done"`);
      expect(await problem.locator('[data-repair-setting="ALLOW_EMPTY_PASSWORD"]').count() === 0, `${name}: the switch for an empty password is not the repair`);
      const notes = (await (await session.get('/cosmos/api/notifications')).json()).data || [];
      expect(notes.filter((n) => n.Title === 'notif.container.startFailed.title' && n.Vars === 'pgbad').length === 1, `${name}: one notification for the administrators`);
      await shot(page, name, '02-start-problem');
      const secret = await problem.locator('[data-repair-setting="POSTGRESQL_PASSWORD"] input').inputValue();
      expect(/^[A-Za-z0-9]{24}$/.test(secret), `${name}: the repair proposes a generated password`);
      await problem.locator('[data-start-apply]').click();
      const ok = await until('pgbad runs', async () => { const c = await inspect('pgbad'); return c && c.State.Running && !c.State.Restarting && (c.Config.Env || []).some((e) => e === 'POSTGRESQL_PASSWORD=' + secret) && (Date.now() - Date.parse(c.State.StartedAt)) > 15000; }, 150);
      await page.waitForTimeout(16000);
      expect(ok && await page.locator('[data-start-problem]').count() === 0, `${name}: after Apply it runs and the check says nothing`);
      await shot(page, name, '03-repaired');
      if (ok) {
        const pg = await psqlWorks('pgbad', secret);
        expect(pg.good && pg.bad, `${name}: the database takes the password the repair set (${pg.text})`);
      }
    }
  } catch (e) {
    failures.push(`${name}: ${e.message.split('\n')[0]}`);
    await shot(page, name, 'zz-error').catch(() => {});
  }
  await browser.close();
}

if (phase === 'look') {
  for (const engine of engines) {
    for (const size of sizes) {
      for (const mode of modes) {
        const viewport = size === 'm' ? { width: 393, height: 851 } : { width: 1366, height: 900 };
        const name = `${engine}-${size === 'm' ? 393 : 1366}-${mode}`;
        const { browser, page } = await open({ chromium, firefox, webkit }[engine], name, viewport, mode);
        try {
          await card(page, 'postgresql');
          await page.locator('[data-service-chip="postgresql"]').waitFor({ timeout: 20000 }).catch(() => {});
          expect(await page.locator('[data-service-chip="postgresql"]').count() === 1, `${name}: the tile names the database and its address`);
          await shot(page, name, '01-tile-service');
          await noSideScroll(page, name, 'ServApps');
          let dialog = await newUrl(page, 'postgresql');
          await shot(page, name, '02-new-url-database');
          await fits(page, name, '[data-new-url-dialog]', 'New URL of the database');
          await page.locator('[data-new-url-dialog] button', { hasText: 'Cancel' }).click().catch(() => {});
          await card(page, 'postgresql');
          await tile(page, 'postgresql').locator('[data-expose-open]').first().click();
          await page.locator('[data-expose-dialog] [data-service-info]').waitFor({ timeout: 30000 }).catch(() => {});
          await shot(page, name, '03-internet-database');
          await fits(page, name, '[data-expose-dialog]', 'Internet of the database');
          await page.locator('[data-expose-dialog] button', { hasText: 'Close' }).click();
          await card(page, 'crashing');
          const why = page.locator('[data-start-why="crashing"]');
          await why.waitFor({ timeout: 20000 }).catch(() => {});
          expect(await why.count() === 1, `${name}: a container that keeps stopping has its Why?`);
          await shot(page, name, '04-tile-why');
          await why.click();
          await page.locator('[data-start-dialog] [data-start-reason]').waitFor({ timeout: 30000 });
          await page.locator('[data-start-dialog] [data-start-log-door]').click().catch(() => {});
          await shot(page, name, '05-why-dialog');
          await fits(page, name, '[data-start-dialog]', 'Why?');
          await page.locator('[data-start-dialog] button', { hasText: 'Close' }).click();
          await page.goto(base + '/cosmos-ui/servapps/containers/postgresql', { waitUntil: 'domcontentloaded' });
          await page.locator('[data-overview-service] [data-service-info]').waitFor({ timeout: 30000 }).catch(() => {});
          await shot(page, name, '06-overview-database');
          await noSideScroll(page, name, 'the database\'s page');
          if (mode === 'light') {
            await sleep(9000); // eight descriptions a minute per user
            await describeInDialog(page, 'bitnami/postgresql', 'pgview');
            await shot(page, name, '07-review');
            await page.locator('[data-any-image-review]').evaluate((el) => el.scrollIntoView()).catch(() => {});
            await page.locator('[data-setting="POSTGRESQL_PASSWORD"]').scrollIntoViewIfNeeded().catch(() => {});
            await shot(page, name, '08-review-settings');
            await fits(page, name, '[data-any-image]', 'the review');
          }
        } catch (e) {
          failures.push(`${name}: ${e.message.split('\n')[0]}`);
          await shot(page, name, 'zz-error').catch(() => {});
        }
        await browser.close();
      }
    }
  }
}

note(`\n${failures.length ? 'FAILED' : 'passed'} (${failures.length})`);
failures.forEach((f) => note(' -', f));
process.exit(failures.length ? 1 : 0);

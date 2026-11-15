// Fork addition (plan 40): checks "Install any image" in headless Chromium,
// Firefox and WebKit at 1366x900 and 393x851, light and dark, against a
// throwaway Cosmos with a Docker of its own. Never the production instance,
// never the real Docker Hub.
//
// What it needs, on one Docker network made with --internal and a subnet of
// a documentation range (198.51.100.0/24: the server's address guard treats
// it as the internet, and it is routed nowhere):
// - a test authority whose certificate is in SSL_CERT_FILE of the test Cosmos
//   and in the system bundle of the Docker in Docker;
// - registry:3 over HTTPS with the network aliases registry-1.docker.io (so
//   "postgres" is read from it, by Cosmos and by the Docker in Docker) holding
//   library/postgres:18, :18.7, :17, :17.12, :latest, library/nginx:1,
//   library/busybox:1.36, library/redis:8 (multi-platform indexes, amd64 and
//   arm64), example/armonly:1 (arm64 only) and example/dbapp:1 (busybox with
//   ENV DB_HOST= DB_PORT=5432 DB_USER= DB_PASSWORD= DB_NAME= APP_SECRET=,
//   EXPOSE 8080 and busybox httpd on 8080);
// - a second registry:3 with a password (htpasswd) under private.example.org
//   holding team/app:1; the test Cosmos has no credentials for it;
// - scripts/fork-fake-dockerhub.go under hub.docker.com (search and tags);
// - docker:29-dind at DIND (2375) running "maindb" (postgres:17 with
//   POSTGRES_USER=main, POSTGRES_PASSWORD=main-test-pw, POSTGRES_DB=maindb)
//   and "web1" (nginx:1), a folder of another disk at /mnt/data in it and in
//   the test Cosmos;
// - the test Cosmos at SERVER_IP, fresh, run in a loop, COSMOS_HTTP_PORT=2052,
//   COSMOS_HTTPS_PORT=2053, DOCKER_HOST=tcp://DIND:2375.
// Playwright pre-installed (the network has no way out). ENGINES, SIZES
// (d,m), MODES (light,dark), CREATE=1 to create (once per instance: the first
// engine and size). Screenshots go to ./shots.
import { chromium, firefox, webkit, request } from 'playwright';

const ip = process.env.SERVER_IP || '198.51.100.10';
const dind = process.env.DIND || 'http://198.51.100.5:2375';
const plain = `http://${ip}:2052`;
const base = `https://${ip}:2053`;
const password = 'Test-Passw0rd!x';
const engines = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const sizes = (process.env.SIZES || 'd,m').split(',');
const modes = (process.env.MODES || 'light,dark').split(',');
let create = process.env.CREATE === '1';
let lastCreate = 0;
const reloadErrors = [];

const note = (...a) => console.log(...a);
const failures = [];
const expect = (ok, what) => { note(ok ? '  ok  ' : '  FAIL', what); if (!ok) failures.push(what); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const api = await request.newContext({ ignoreHTTPSErrors: true });
const until = async (what, check, seconds = 90) => {
  for (let i = 0; i < seconds; i++) {
    try { if (await check()) return true; } catch (e) { /* reloading */ }
    await sleep(1000);
  }
  expect(false, 'timed out: ' + what);
  return false;
};
const serverUp = () => until('the server answers over HTTPS', async () => (await api.get(base + '/cosmos/api/status')).status() < 500);
const inspect = async (name) => {
  const answer = await api.get(`${dind}/containers/${name}/json`);
  return answer.status() === 200 ? answer.json() : null;
};
// runs a command in a container of the test Docker and answers its output
const run = async (name, cmd, env = []) => {
  const made = await (await api.post(`${dind}/containers/${name}/exec`, { data: { Cmd: cmd, Env: env, AttachStdout: true, AttachStderr: true } })).json();
  const out = await api.post(`${dind}/exec/${made.Id}/start`, { data: { Detach: false, Tty: true } });
  return (await out.text()).replace(/[^\x20-\x7e\n]/g, '');
};

// --- the wizard and the settings, once
{
  let fresh = false;
  try {
    const answer = await api.get(plain + '/cosmos/api/status', { timeout: 5000, maxRedirects: 0 });
    fresh = answer.status() === 200 && (await answer.json()).status === 'OK';
  } catch (e) { /* set up already */ }
  if (fresh) {
    const step = (data) => api.post(plain + '/cosmos/api/newInstall', { data }).then((r) => r.status()).catch(() => 'exit');
    note('wizard:', await step({ step: '2', mongodbMode: 'Create' }),
      await step({ step: '3', httpsCertificateMode: 'SELFSIGNED', hostname: ip, allowHTTPLocalIPAccess: true }),
      await step({ step: '4', nickname: 'admin', password, email: 'admin@example.org' }), await step({ step: '5' }));
    await sleep(4000);
  }
  await serverUp();
  const ctx = await request.newContext({ ignoreHTTPSErrors: true });
  await ctx.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  await ctx.post(base + '/cosmos/api/sudo', { data: { password } });
  const config = (await (await ctx.get(base + '/cosmos/api/config')).json()).data;
  if (config.DockerConfig.DefaultDataPath !== '/srv/cosmos-storage') {
    config.DockerConfig.DefaultDataPath = '/srv/cosmos-storage';
    note('settings:', (await ctx.put(base + '/cosmos/api/config', { data: config })).status());
    await sleep(5000);
    await serverUp();
    await ctx.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
    await ctx.post(base + '/cosmos/api/sudo', { data: { password } });
  }

  // the routes, by themselves
  const post = async (path, data) => { const r = await ctx.post(base + path, { data }); return { status: r.status(), body: await r.json().catch(() => ({})) }; };
  let r = await post('/cosmos/api/images/search', { query: 'postgres' });
  const names = ((r.body.data && r.body.data.results) || []).map((x) => x.name);
  expect(r.status === 200 && names[0] === 'postgres' && names.indexOf('bitnami/postgresql') < names.indexOf('cimg/postgres'), `search: official first, then by downloads (${names.join(', ')})`);
  r = await post('/cosmos/api/images/describe', { reference: 'postgres' });
  const pg = r.body.data || {};
  expect(pg.tag === '18' && pg.tags.why === 'pinMajor' && pg.recipe && pg.recipe.id === 'postgres', `describe postgres: tag ${pg.tag} (${pg.tags && pg.tags.why}), recipe ${pg.recipe && pg.recipe.id}`);
  expect(pg.declared && pg.declared.volumes.join() === '/var/lib/postgresql' && pg.declared.ports.join() === '5432/tcp', `describe postgres: what it declares (${JSON.stringify(pg.declared && pg.declared.volumes)})`);
  r = await post('/cosmos/api/images/describe', { reference: 'postgres:17' });
  expect(r.body.data.declared.volumes.join() === '/var/lib/postgresql/data', 'describe postgres:17: the older data folder');
  r = await post('/cosmos/api/images/describe', { reference: 'private.example.org/team/app:1' });
  expect(r.body.data.problem === 'unauthorized' && r.body.data.loggedIn === false, `a private registry: ${r.body.data.problem}, logged in ${r.body.data.loggedIn}`);
  r = await post('/cosmos/api/images/describe', { reference: '192.168.1.2:5000/app' });
  expect(r.body.data.problem === 'internal', 'a registry on the local network without a login is refused');
  r = await post('/cosmos/api/images/describe', { reference: 'example/armonly:1' });
  expect(r.body.data.fits === 'no' && /arm64/.test(r.body.data.platforms.join()), 'an image for another processor');
  r = await post('/cosmos/api/images/describe', { reference: 'busybox:1.36' });
  expect(r.body.data.declared && !r.body.data.declared.ports.length && !r.body.data.declared.volumes.length, 'busybox declares nothing');
  r = await post('/cosmos/api/images/search', { query: 'a b' });
  expect(r.status === 400, 'search words with a space are refused');
  const get = await ctx.get(base + '/cosmos/api/images/describe');
  expect(get.status() === 405, 'describe takes POST only');
  const anonymous = await (await request.newContext({ ignoreHTTPSErrors: true })).post(base + '/cosmos/api/images/describe', { data: { reference: 'postgres' } });
  expect(anonymous.status() >= 400, 'describe needs a session');
  await ctx.dispose();
}

const open = async (type, name, viewport, mode) => {
  const browser = await type.launch();
  const ctx = await browser.newContext({ viewport, ignoreHTTPSErrors: true, colorScheme: mode, serviceWorkers: 'block' });
  const page = await ctx.newPage();
  // a creation with an address reloads the server: a request of the page in flight then fails (upstream's list
  // refresh, seen since plan 20); counted, not failed, in the half minute after a Create
  page.on('pageerror', (e) => {
    if (Date.now() - lastCreate < 30000 && /Failed to fetch|Load failed|NetworkError/.test(e.message)) { reloadErrors.push(name); return; }
    failures.push(`${name} PAGEERROR: ${(e.stack || e.message).slice(0, 600)}`);
  });
  page.consoleLines = [];
  page.on('console', (m) => page.consoleLines.push(m.text()));
  const a = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  const b = await ctx.request.post(base + '/cosmos/api/sudo', { data: { password } });
  note(`\n== ${name}: login ${a.status()}/${b.status()}`);
  return { browser, ctx, page };
};
const noSideScroll = async (page, name, where) => {
  const over = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(over <= 1, `${name}: no sideways scroll on ${where} (${over}px over)`);
};
const dialogFits = async (page, name, where) => {
  const dialog = page.locator('[data-any-image]').last();
  const box = await dialog.locator('.MuiDialog-paper').boundingBox();
  expect(!!box && box.x >= -1 && box.x + box.width <= page.viewportSize().width + 1, `${name}: the dialog fits the screen (${where})`);
  const inside = await dialog.evaluate((d) => {
    const content = d.querySelector('.MuiDialogContent-root');
    if (!content) return 'no content';
    if (content.scrollWidth > content.clientWidth + 1) return `content ${content.scrollWidth} > ${content.clientWidth}`;
    const right = content.getBoundingClientRect().right + 1;
    const out = [...content.querySelectorAll('*')].filter((el) => el.getBoundingClientRect().width > 0 && el.getBoundingClientRect().right > right
      && getComputedStyle(el).position !== 'fixed' && !el.closest('.MuiCollapse-hidden'));
    return out.length ? `${out.length} sticking out, first ${out[0].tagName}.${String(out[0].className).slice(0, 60)}` : '';
  });
  expect(inside === '', `${name}: nothing sticks out of the dialog (${where}) ${inside}`);
};
const shot = (page, name, what) => page.screenshot({ path: `shots/${name}-${what}.png`, fullPage: false });
const shotDialog = async (page, name, what) => {
  // the whole dialog, scrolled through: one picture of its full height
  const content = page.locator('[data-any-image] .MuiDialogContent-root');
  await content.evaluate((c) => { c.scrollTop = 0; });
  await shot(page, name, what);
  const tall = await content.evaluate((c) => c.scrollHeight > c.clientHeight + 10);
  if (tall) {
    await content.evaluate((c) => { c.scrollTop = c.scrollHeight; });
    await page.waitForTimeout(200);
    await shot(page, name, what + '-end');
    await content.evaluate((c) => { c.scrollTop = 0; });
  }
};

for (const engine of engines) {
  for (const size of sizes) {
    for (const mode of modes) {
      const viewport = size === 'm' ? { width: 393, height: 851 } : { width: 1366, height: 900 };
      const name = `${engine}-${size === 'm' ? 393 : 1366}-${mode}`;
      const { browser, page } = await open({ chromium, firefox, webkit }[engine], name, viewport, mode);
      let decisions = 0;
      const decide = async (locator) => { decisions += 1; await locator.click(); };
      try {
        // --- 1. "postgres" typed in the Market, to the review (and, once, to a running PostgreSQL)
        await page.goto(base + '/cosmos-ui/market-listing', { waitUntil: 'domcontentloaded' });
        await page.locator('[data-market-any-image]').waitFor({ timeout: 30000 });
        await shot(page, name, '01-market-tile');
        await noSideScroll(page, name, 'the Market with the tile');
        await page.getByPlaceholder(/search/i).fill('postgres');
        const offer = page.locator('[data-market-any-image-nomatch] button');
        await offer.waitFor({ timeout: 15000 });
        expect((await offer.innerText()).includes('postgres'), `${name}: the Market offers the words typed as an image`);
        await shot(page, name, '02-market-nomatch');
        await decide(offer);
        await page.locator('[data-result="postgres"]').waitFor({ timeout: 20000 });
        await page.waitForTimeout(300);
        await shotDialog(page, name, '03-search');
        await dialogFits(page, name, 'the search');
        const resultOrder = await page.locator('[data-result]').evaluateAll((els) => els.map((e) => e.getAttribute('data-result')));
        expect(resultOrder[0] === 'postgres', `${name}: the official image first (${resultOrder.join(', ')})`);
        await decide(page.locator('[data-result="postgres"]'));
        await page.locator('[data-any-image-review] [data-creation-review]').waitFor({ timeout: 30000 });
        await page.waitForTimeout(1500); // the plan (data places) answers
        const about = await page.locator('[data-any-image-about]').innerText();
        expect(/PostgreSQL/.test(about) && /Version \(tag\): 18/.test(about) && /pinned to its major/.test(about), `${name}: PostgreSQL 18 proposed, and why`);
        expect(/reach it at postgres(-\d+)?:5432/.test(about) && /Nothing is published/.test(about), `${name}: the address other apps use, nothing published`);
        const secret = await page.locator('[data-setting="POSTGRES_PASSWORD"] input').inputValue();
        expect(/^[A-Za-z0-9]{24}$/.test(secret), `${name}: a generated password is shown`);
        const review = await page.locator('[data-any-image-review]').innerText();
        expect(/Publishes no port on the server/.test(review), `${name}: the review says no port is published`);
        expect(/\/mnt\/data\/cosmos-storage\/postgres(-\d+)?\/postgresql/.test(review), `${name}: the data folder is proposed on the data disk`);
        expect(!review.includes(secret), `${name}: the password is not in the review's sentences`);
        expect(!page.consoleLines.some((line) => line.includes(secret)), `${name}: the password is not on the console`);
        await shotDialog(page, name, '04-postgres-review');
        await dialogFits(page, name, 'the PostgreSQL review');
        await noSideScroll(page, name, 'the PostgreSQL review');
        if (create) {
          lastCreate = Date.now();
          await decide(page.locator('[data-any-image-review] button.shinyButton'));
          await page.getByText(/created successfully|Service created|success/i).first().waitFor({ timeout: 180000 }).catch(() => {});
          expect(decisions === 3, `${name}: "postgres" in ${decisions} decisions (from the Market's search to Create)`);
          const ok = await until('postgres runs', async () => { const c = await inspect('postgres'); return c && c.State.Running && c.State.Health ? c.State.Health.Status === 'healthy' : c && c.State.Running; }, 120);
          if (ok) {
            const c = await inspect('postgres');
            const mount = (c.Mounts || []).find((m) => m.Destination === '/var/lib/postgresql');
            expect(mount && mount.Type === 'bind' && mount.Source === '/mnt/data/cosmos-storage/postgres/postgresql', `${name}: the data is on the data disk (${mount && mount.Source})`);
            expect(Object.keys(c.HostConfig.PortBindings || {}).length === 0, `${name}: no port published on the server`);
            await sleep(5000);
            // over the network (127.0.0.1 is trusted by the image's own pg_hba.conf)
            const psql = (sql) => ['sh', '-c', `psql -h "$(hostname -i | cut -d' ' -f1)" -U app -d app -tAc "${sql}" 2>&1; echo "exit=$?"`];
            const good = await run('postgres', psql('select 40+2'), [`PGPASSWORD=${secret}`]);
            expect(/42/.test(good), `${name}: the password shown works (${good.trim().slice(0, 80)})`);
            const bad = await run('postgres', psql('select 1'), ['PGPASSWORD=wrong']);
            expect(/password authentication failed|exit=2/.test(bad) && !/exit=0/.test(bad), `${name}: a wrong password is refused (${bad.trim().slice(0, 120)})`);
            const files = await run('postgres', ['ls', '/var/lib/postgresql/18/docker']);
            expect(/PG_VERSION/.test(files), `${name}: PostgreSQL 18 keeps its files in the mounted folder`);
          }
          await shot(page, name, '05-postgres-created');
          expect(!page.consoleLines.some((line) => line.includes(secret)), `${name}: the password is not on the console after the creation`);
          note(`  (the generated test password, to look for in the server's log afterwards: ${secret})`);
        }
        await page.locator('[data-any-image] button', { hasText: 'Close' }).last().click();

        // --- 2. from ServApps: an app that needs a database
        await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
        await page.locator('[data-any-image-open]').first().waitFor({ timeout: 30000 });
        await shot(page, name, '06-servapps-button');
        await noSideScroll(page, name, 'ServApps with the button');
        await page.locator('[data-any-image-open]').first().click();
        await page.locator('[data-any-image-field] input').fill('example/dbapp:1');
        await page.locator('[data-any-image-search]').click();
        await page.locator('[data-any-image-db]').waitFor({ timeout: 30000 });
        const dbText = await page.locator('[data-any-image-db]').innerText();
        expect(/needs a database \(postgres\)/.test(dbText) && /maindb \(PostgreSQL, on this server\)/.test(dbText), `${name}: the app's need for PostgreSQL is seen (its port says which), maindb offered`);
        expect(await page.locator('[data-any-image-review]').count() === 0, `${name}: no review before the database is chosen`);
        await shotDialog(page, name, '07-dbapp-needs');
        const useExisting = engine !== 'chromium' || !create;
        if (useExisting) {
          await page.locator('[data-db-existing="maindb"]').click();
          await page.locator('[data-any-image-review] [data-creation-review]').waitFor({ timeout: 30000 });
          await page.waitForTimeout(1500);
          await page.locator('[data-definition-door]').click();
          const json = await page.locator('[data-any-image-review] textarea').inputValue();
          expect(/"DB_HOST=maindb"/.test(json) && /"DB_PASSWORD=main-test-pw"/.test(json) && /"links": \[\s*"maindb"/.test(json), `${name}: an existing database: linked, its address and password filled in`);
          await page.locator('[data-definition-door]').click();
        } else {
          await page.locator('[data-db-new="postgres"]').click();
          await page.locator('[data-any-image-review] [data-creation-review]').waitFor({ timeout: 30000 });
          await page.waitForTimeout(1500);
          const review = await page.locator('[data-any-image-review]').innerText();
          expect(/Creates 2 container/.test(review), `${name}: the app and its database in one review`);
          expect(/Filled in from the database: DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME/.test(await page.locator('[data-any-image-db]').innerText())
            && await page.locator('[data-setting="DB_HOST"]').count() === 0, `${name}: the variables the database fills are not asked for`);
        }
        await shotDialog(page, name, '08-dbapp-review');
        await dialogFits(page, name, 'the app with a database');
        if (create && !useExisting) {
          lastCreate = Date.now();
          await page.locator('[data-any-image-review] button.shinyButton').click();
          const ok = await until('dbapp and its database run', async () => { const a = await inspect('dbapp'); const d = await inspect('dbapp-postgres'); return a && d && a.State.Running && d.State.Running; }, 180);
          if (ok) {
            const app = await inspect('dbapp');
            const env = app.Config.Env.join('\n');
            expect(/DB_HOST=dbapp-postgres/.test(env) && /DB_USER=app/.test(env) && /DB_NAME=app/.test(env), `${name}: the app's settings come from its database`);
            expect(Object.keys(app.NetworkSettings.Networks).includes('dbapp-db'), `${name}: the app is on the database's network`);
            const ping = await run('dbapp', ['ping', '-c', '1', '-W', '2', 'dbapp-postgres']);
            expect(/1 packets received/.test(ping), `${name}: the app reaches its database by name`);
          }
        }
        await page.locator('[data-any-image-back]').click();

        // --- 3. a web image ends with an address
        await page.locator('[data-any-image-field] input').fill('nginx');
        await page.locator('[data-any-image-search]').click();
        await page.locator('[data-result="nginx"]').click();
        await page.locator('[data-any-image-review] [data-creation-review]').waitFor({ timeout: 30000 });
        await page.waitForTimeout(1000);
        const webText = await page.locator('[data-any-image]').innerText();
        expect(/web interface gets the address https:\/\//.test(webText) && /Addresses in Cosmos/.test(webText) && /Publishes no port/.test(webText), `${name}: nginx gets an address, no published port`);
        await shotDialog(page, name, '09-nginx-review');
        await page.locator('[data-any-image-back]').click();

        // --- 4. an image that declares nothing
        await page.locator('[data-any-image-field] input').fill('busybox');
        await page.locator('[data-any-image-search]').click();
        await page.locator('[data-result="busybox"]').click();
        await page.locator('[data-any-image-facts]').waitFor({ timeout: 30000 });
        expect(/says nothing about itself/.test(await page.locator('[data-any-image-facts]').innerText()), `${name}: busybox: "this image says nothing about itself"`);
        await page.locator('[data-any-image-open-fields] input').nth(0).fill('httpd -f -p 8080 -h /etc');
        await page.locator('[data-any-image-open-fields] input').nth(2).fill('8080');
        await page.locator('[data-any-image-review] [data-creation-review]').waitFor({ timeout: 30000 });
        await page.waitForTimeout(800);
        expect(/Addresses in Cosmos: busybox/.test(await page.locator('[data-any-image-review]').innerText()), `${name}: busybox with a command and a web port typed: an address`);
        await shotDialog(page, name, '10-busybox');
        await page.locator('[data-any-image-back]').click();

        // --- 5. a private registry and another processor, refused in words
        await page.locator('[data-any-image-field] input').fill('private.example.org/team/app:1');
        await page.locator('[data-any-image-search]').click();
        await page.locator('[data-any-image-problem]').waitFor({ timeout: 30000 });
        expect(/not logged in to private\.example\.org/.test(await page.locator('[data-any-image-problem]').innerText()), `${name}: a private registry: "the server is not logged in"`);
        await shotDialog(page, name, '11-private');
        await page.locator('[data-any-image-back]').click();
        await page.locator('[data-any-image-field] input').fill('example/armonly:1');
        await page.locator('[data-any-image-search]').click();
        await page.locator('[data-any-image-platform]').waitFor({ timeout: 30000 });
        expect(await page.locator('[data-any-image-review]').count() === 0 && await page.locator('[data-any-image-name]').count() === 0, `${name}: no review and no settings for an image of another processor`);
        await shotDialog(page, name, '12-armonly');
        await dialogFits(page, name, 'the refusals');
        await noSideScroll(page, name, 'the refusals');
      } catch (e) {
        failures.push(`${name}: ${e.message.split('\n')[0]}`);
        await shot(page, name, 'zz-error').catch(() => {});
      }
      create = false;
      await browser.close();
    }
  }
}

await api.dispose();
if (reloadErrors.length) note(`\n${reloadErrors.length} page error(s) "Failed to fetch" while the server reloaded after a creation: ${reloadErrors.join(', ')}`);
note(failures.length ? `\n${failures.length} FAILURES:\n- ` + failures.join('\n- ') : '\nall checks passed');
process.exit(failures.length ? 1 : 0);

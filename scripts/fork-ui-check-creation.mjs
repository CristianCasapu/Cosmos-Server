// Fork addition: checks the creation of apps (plan 23: the new-container form
// keeps every field, the compose import says what it does not take, the
// Market installs on one screen with the data on the right disk, the image's
// processor and the access to the server said before anything is pulled) in
// headless Chromium, Firefox and WebKit at 1366x900 and 390x844, against a
// throwaway Cosmos with a Docker of its own. Never the production instance.
//
// What it needs, on one Docker network made with --internal (no way out):
// - a docker:27-dind at DIND (its API on 2375, the DOCKER_HOST of the test
//   Cosmos) holding: a registry (registry:2 on 5000) with
//   localhost:5000/jellyfin:latest and localhost:5000/whoami:latest (any
//   amd64 image, busybox will do) and localhost:5000/armonly:latest (an
//   arm64-only image: busybox pulled with --platform linux/arm64); a running
//   container "vpn" (busybox, sleep 3600); a folder of another disk than the
//   test Cosmos' own files mounted at /mnt/data in both (so that it is a
//   data disk to Cosmos and the folders Cosmos creates exist for Docker);
// - a web server at MARKET serving index.json (a market source whose "compose"
//   entries are data: URLs: the browser fetches them, and an http: one is
//   blocked as mixed content on the HTTPS panel) with the templates jellyfin
//   and panel: "Jellyfin" (supported_architectures amd64, arm64; image
//   localhost:5000/jellyfin:latest, command sleep 3600, TZ empty, a named
//   volume on /config and a bind {DefaultDataPath}/{ServiceName}/media on
//   /media, one route) and "Panel" (arm64 only; image
//   localhost:5000/armonly:latest with /var/run/docker.sock bound);
// - the test Cosmos at SERVER_IP, fresh, run in a loop, COSMOS_HTTP_PORT=2052,
//   COSMOS_HTTPS_PORT=2053, DOCKER_HOST=tcp://DIND:2375.
// It finishes the set-up wizard, sets the data path to /srv/cosmos-storage
// (on the test Cosmos' system disk, as on a fresh install) and adds the
// market source. Screenshots go to ./shots. ENGINES, SIZES as in the other checks.
import { chromium, firefox, webkit, request } from 'playwright';

const ip = process.env.SERVER_IP || '172.31.88.10';
const dind = process.env.DIND || 'http://172.31.88.5:2375';
const market = process.env.MARKET || 'http://172.31.88.20';
const plain = `http://${ip}:2052`;
const base = `https://${ip}:2053`;
const password = 'Test-Passw0rd!x';
const engines = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const sizes = (process.env.SIZES || 'd,m').split(',');

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
    config.MarketConfig = { ...(config.MarketConfig || {}), Sources: [{ Name: 'test', Url: market + '/index.json' }] };
    note('settings:', (await ctx.put(base + '/cosmos/api/config', { data: config })).status());
    await sleep(5000);
    await serverUp();
  }

  // the server refuses the Docker socket without the admin's yes, and an image for another processor
  await ctx.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  await ctx.post(base + '/cosmos/api/sudo', { data: { password } });
  const socket = { services: { sock: { container_name: 'sock', image: 'localhost:5000/whoami:latest', volumes: [{ type: 'bind', source: '/var/run/docker.sock', target: '/var/run/docker.sock' }] } } };
  let text = await (await ctx.post(base + '/cosmos/api/docker-service', { data: socket })).text();
  expect(/explicit confirmation/.test(text) && /docker\.sock/.test(text), `server: the Docker socket needs a confirmation (${text.slice(0, 120)})`);
  text = await (await ctx.post(base + '/cosmos/api/docker-service', { data: { services: { arm: { container_name: 'arm', image: 'localhost:5000/armonly:latest' } } } })).text();
  expect(/made for linux\/arm64\/v8 only/.test(text) && /linux\/amd64/.test(text), `server: an arm64 image is refused before the pull (${text.slice(0, 160)})`);
  expect(!(await inspect('sock')) && !(await inspect('arm')), 'server: nothing was created for either');
  const plan = (await (await ctx.post(base + '/cosmos/api/install/plan', { data: { images: ['localhost:5000/jellyfin:latest'] } })).json()).data;
  note('plan:', JSON.stringify(plan));
  expect(plan.server.arch === 'amd64' && plan.images[0].fits === 'yes', 'server: the plan knows the processor and the image');
  expect(plan.places.some((p) => p.path === '/mnt/data' && p.kind === 'disk' && !p.osDisk), 'server: /mnt/data is a data disk');
  expect(plan.places.some((p) => p.path === '/srv/cosmos-storage' && p.osDisk), 'server: the data path is on the system disk');
  await ctx.dispose();
}

const open = async (type, name, viewport) => {
  const browser = await type.launch();
  const ctx = await browser.newContext({ viewport, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => failures.push(`${name} PAGEERROR: ${(e.stack || e.message).slice(0, 600)}`));
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
  const box = await page.locator('[role=dialog]').last().boundingBox();
  expect(!!box && box.x >= 0 && box.x + box.width <= page.viewportSize().width + 1, `${name}: the dialog fits the screen (${where})`);
  const inside = await page.locator('[role=dialog]').last().evaluate((dialog) => {
    const content = dialog.querySelector('.MuiDialogContent-root');
    return !content || content.scrollWidth <= content.clientWidth + 1;
  });
  expect(inside, `${name}: nothing sticks out of the dialog (${where})`);
};
const shot = (page, name, what, full = false) => page.screenshot({ path: `shots/${name}-${what}.png`, fullPage: full });
let clicks = 0;
const click = async (locator) => { clicks += 1; await locator.click(); };
const nextTab = async (page) => { await page.getByRole('button', { name: 'Next' }).last().click(); await page.waitForTimeout(500); };

const fullCompose = [
  'services:',
  '  web:',
  '    image: localhost:5000/whoami:latest',
  '    build: .',
  '    command: ["sh", "-c", "echo \'a b\' && sleep 3600"]',
  '    ports:',
  '      - 8080',
  '      - target: 81',
  '        published: 8181',
  '    environment:',
  '      MODE: test',
  '      FROM_SHELL:',
  '    env_file: .env',
  '    ulimits:',
  '      nofile: 1024',
  '    volumes:',
  '      - ./config:/config',
  '      - /srv/x:/x:ro',
  '    healthcheck:',
  '      test: "true"',
  '      interval: 1m30s',
  '    shm_size: 128m',
  '    depends_on: [db]',
  '  db:',
  '    image: localhost:5000/whoami:latest',
  '  sidecar:',
  '    image: localhost:5000/whoami:latest',
  '    network_mode: service:db',
  '    ports: ["9000:9000"]',
].join('\n');

const goodCompose = [
  'services:',
  '  web:',
  '    image: localhost:5000/whoami:latest',
  '    command: ["sh", "-c", "echo \'a b\' && sleep 3600"]',
  '    ports:',
  '      - 18085',
  '    environment:',
  '      MODE: test',
  '    volumes:',
  '      - /mnt/data/composed:/data:ro',
  '    tmpfs: /scratch',
  '    healthcheck:',
  '      test: "true"',
  '      interval: 1m30s',
  '    shm_size: 128m',
  '    init: true',
  '    restart: on-failure:3',
  '    depends_on:',
  '      db:',
  '        condition: service_started',
  '  db:',
  '    image: localhost:5000/whoami:latest',
  '    command: sleep 3600',
].join('\n');

let created = false;
for (const engine of engines) {
  for (const size of sizes) {
    const viewport = size === 'm' ? { width: 390, height: 844 } : { width: 1366, height: 900 };
    const name = `${engine}-${size === 'm' ? 390 : 1366}`;
    const first = !created;
    const { browser, page } = await open({ chromium, firefox, webkit }[engine], name, viewport);
    try {
      // 1. the new-container form: every field reaches the container (once), then "Review and create"
      if (first) {
        await page.goto(base + '/cosmos-ui/servapps/new-service', { waitUntil: 'domcontentloaded' });
        await page.locator('#image').waitFor({ timeout: 45000 });
        await page.locator('#image').fill('localhost:5000/whoami:latest');
        await page.locator('#name').fill('formapp');
        await page.locator('#user').fill('1000:1000');
        await page.locator('#memLimit').fill('64m');
        await page.locator('#cpus').fill('0.5');
        await page.locator('input[name=interactive]').uncheck();
        await nextTab(page); // URL
        await nextTab(page); // Network
        await page.locator('#networkMode').fill('container:vpn');
        await page.waitForTimeout(300);
        expect(await page.locator('[data-shared-network]').count() === 1, `${name}: the Network tab says ports are not published on another container's network`);
        await nextTab(page); // Storage
        await nextTab(page); // Review
        await page.locator('[data-creation-review]').waitFor({ timeout: 20000 });
        await shot(page, name, 'form-review', true);
        await page.getByRole('button', { name: 'Create' }).click();
        await page.getByText('Service created').or(page.locator('[data-create-failed]')).first().waitFor({ timeout: 120000 }).catch(() => {});
        const c = await inspect('formapp');
        expect(!!c, `${name}: the container was created`);
        if (c) {
          expect(c.Config.User === '1000:1000', `${name}: user reached the container (${c.Config.User})`);
          expect(c.HostConfig.Memory === 64 * 1024 * 1024, `${name}: memory limit reached the container (${c.HostConfig.Memory})`);
          expect(c.HostConfig.NanoCpus === 500000000, `${name}: CPU limit reached the container (${c.HostConfig.NanoCpus})`);
          expect(/^container:/.test(c.HostConfig.NetworkMode), `${name}: "use as VPN" network mode reached the container (${c.HostConfig.NetworkMode})`);
          expect(c.Config.Tty === false, `${name}: the interactive tick reached the container`);
        }
        // the edit form shows them again (on a phone the tabs are a list; checked at desktop width)
        if (size !== 'm') {
        await page.goto(base + '/cosmos-ui/servapps/containers/formapp', { waitUntil: 'domcontentloaded' });
        await page.getByRole('tab', { name: 'Docker' }).or(page.getByText('Docker', { exact: true })).first().click({ timeout: 45000 });
        await page.locator('#user').waitFor({ timeout: 30000 });
        await page.waitForTimeout(800);
        expect(await page.locator('#user').inputValue() === '1000:1000', `${name}: the edit form shows the user`);
        expect(await page.locator('#memLimit').inputValue() === '64m', `${name}: the edit form shows the memory limit as 64m (${await page.locator('#memLimit').inputValue()})`);
        expect(await page.locator('#cpus').inputValue() === '0.5', `${name}: the edit form shows the CPU limit`);
        await shot(page, name, 'edit-docker', true);
        await page.getByRole('tab', { name: 'Network' }).or(page.getByText('Network', { exact: true })).first().click();
        await page.locator('#networkMode').waitFor({ timeout: 30000 });
        expect(/^container:/.test(await page.locator('#networkMode').inputValue()), `${name}: the edit form shows the network mode`);
        }
      }

      clicks = 0;
      await page.goto(base + '/cosmos-ui/servapps/new-service', { waitUntil: 'domcontentloaded' });
      await page.locator('#image').waitFor({ timeout: 45000 });
      await page.locator('#image').fill('localhost:5000/jellyfin:latest');
      await page.locator('#image').blur();
      await click(page.locator('[data-quick-review]'));
      await page.locator('[data-creation-review]').waitFor({ timeout: 20000 });
      await page.waitForTimeout(1500);
      const quick = await page.locator('[data-creation-review]').innerText();
      expect(/jellyfin \(localhost:5000\/jellyfin:latest\)/.test(quick), `${name}: "Review and create" proposed the name from the image`);
      expect(/jellyfin\.local/.test(quick), `${name}: and a local address on the image's usual port (${quick.replace(/\s+/g, ' ').slice(0, 200)})`);
      note(`  clicks, new container (type the image, then): ${clicks + 1} (Review and create, Create)`);
      await shot(page, name, 'quick-review', true);
      await noSideScroll(page, name, 'the new-container review');

      // 2. the compose import says what it does not take
      await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
      await page.getByRole('button', { name: 'Import Compose File' }).click({ timeout: 45000 });
      await page.getByPlaceholder(/Paste your docker-compose/).fill(fullCompose);
      await page.getByRole('button', { name: 'Next' }).click();
      await page.locator('[data-creation-review]').waitFor({ timeout: 20000 });
      await page.waitForTimeout(1000);
      const blocked = await page.locator('[data-review-blocked]').innerText().catch(() => '');
      const dropped = await page.locator('[data-review-dropped]').innerText().catch(() => '');
      const changed = await page.locator('[data-review-changed]').innerText().catch(() => '');
      expect(/cannot publish ports of its own/.test(blocked), `${name}: a service on another one's network with ports is said and blocks`);
      expect(/build/.test(dropped) && /ulimits/.test(dropped) && /env_file/.test(dropped) && /FROM_SHELL/.test(dropped), `${name}: build, ulimits, env_file and a variable without value are named`);
      expect(/random one/.test(changed) && /relative to the compose file/.test(changed), `${name}: the lone port and the relative folder are said`);
      expect(await page.getByRole('button', { name: 'Create' }).isDisabled(), `${name}: Create waits while a line cannot be created`);
      await shot(page, name, 'import-notes', true);
      await dialogFits(page, name, 'the import review');
      await page.getByRole('button', { name: 'Close' }).click();

      if (first) {
        await page.getByRole('button', { name: 'Import Compose File' }).click();
        await page.getByPlaceholder(/Paste your docker-compose/).fill(goodCompose);
        await page.getByRole('button', { name: 'Next' }).click();
        await page.locator('[data-creation-review]').waitFor({ timeout: 20000 });
        await page.waitForTimeout(1500);
        await page.getByRole('button', { name: 'Create' }).click();
        await page.getByText('Service created').or(page.locator('[data-create-failed]')).first().waitFor({ timeout: 120000 }).catch(() => {});
        const services = await (await api.get(`${dind}/containers/json?all=1`)).json();
        const web = services.find((c) => (c.Names || []).some((n) => /-web$/.test(n)));
        const c = web && await inspect(web.Id);
        expect(!!c, `${name}: the imported app was created`);
        if (c) {
          expect(JSON.stringify(c.Config.Cmd) === JSON.stringify(['sh', '-c', "echo 'a b' && sleep 3600"]), `${name}: exec-form command kept (${JSON.stringify(c.Config.Cmd)})`);
          expect(c.HostConfig.ShmSize === 128 * 1024 * 1024, `${name}: shm_size`);
          expect(c.HostConfig.Tmpfs && '/scratch' in c.HostConfig.Tmpfs, `${name}: tmpfs`);
          expect(c.HostConfig.Init === true, `${name}: init`);
          expect(c.HostConfig.RestartPolicy.Name === 'on-failure' && c.HostConfig.RestartPolicy.MaximumRetryCount === 3, `${name}: restart on-failure:3`);
          expect(c.Config.Healthcheck && c.Config.Healthcheck.Interval === 90e9, `${name}: healthcheck 1m30s`);
          expect((c.HostConfig.PortBindings['18085/tcp'] || [])[0].HostPort === '18085', `${name}: the lone port published on the same port`);
          expect(c.Mounts.some((m) => m.Destination === '/data' && m.RW === false), `${name}: the read-only mount stays read-only`);
        }
        await page.keyboard.press('Escape');
      }

      // 3. the Market: one screen, one confirmation, the data off the system disk
      clicks = 0;
      await page.goto(base + '/cosmos-ui/market-listing', { waitUntil: 'domcontentloaded' });
      const card = page.locator('a[href$="/market-listing/test/Jellyfin"]').first();
      await card.waitFor({ timeout: 60000 });
      await click(card);
      await page.getByRole('button', { name: /Install/ }).last().waitFor({ timeout: 30000 });
      // (the test market's long data: address pushes the button sideways in WebKit)
      await page.getByRole('button', { name: /Install/ }).last().scrollIntoViewIfNeeded();
      await click(page.getByRole('button', { name: /Install/ }).last());
      await page.locator('[data-installer-review] [data-creation-review]').waitFor({ timeout: 30000 });
      await page.locator('[data-place]').first().waitFor({ timeout: 30000 });
      await page.waitForTimeout(2000);
      const review = await page.locator('[data-installer-review]').innerText();
      expect(/\/mnt\/data\/cosmos-storage\/[^\s/]+\/config/.test(review), `${name}: the config volume is proposed on the data disk (the data path is on the system disk)`);
      expect(/\/mnt\/data\/[^\s/]+\/media/.test(review), `${name}: the media folder is proposed on the data disk`);
      expect(await page.getByRole('button', { name: 'Next' }).count() === 0, `${name}: no Next: the review is on the same screen`);
      await shot(page, name, 'market-review', true);
      await dialogFits(page, name, 'the Market review');
      if (first) {
        await click(page.getByRole('button', { name: 'Create' }));
        await page.getByText('Service created').or(page.locator('[data-create-failed]')).first().waitFor({ timeout: 120000 }).catch(() => {});
        note(`  clicks, Market (from the list to created): ${clicks}`);
        expect(clicks === 3, `${name}: a Market app takes three clicks from the list (${clicks})`);
        const all = await (await api.get(`${dind}/containers/json?all=1`)).json();
        const jelly = all.find((c) => (c.Image || '').includes('jellyfin'));
        const c = jelly && await inspect(jelly.Id);
        expect(!!c, `${name}: the Market app was created`);
        if (c) {
          expect(c.Mounts.every((m) => m.Type !== 'volume') && c.Mounts.some((m) => /^\/mnt\/data\//.test(m.Source)), `${name}: its data is on the data disk (${c.Mounts.map((m) => m.Source).join(', ')})`);
          expect(c.HostConfig.RestartPolicy.Name === 'unless-stopped', `${name}: restart proposed (unless-stopped)`);
          expect(c.Config.Env.some((e) => /^TZ=./.test(e) && e !== 'TZ=auto'), `${name}: the time zone filled in (${c.Config.Env.join(' ')})`);
        }
        await shot(page, name, 'market-done', true);
        await page.keyboard.press('Escape');
      }

      // 4. a Market app for another processor that asks for the Docker socket
      await page.goto(base + '/cosmos-ui/market-listing/test/Panel', { waitUntil: 'domcontentloaded' });
      await page.locator('[data-market-arch]').waitFor({ timeout: 60000 });
      expect(true, `${name}: the listing says the app is not for this processor`);
      await page.getByRole('button', { name: /Install/ }).last().scrollIntoViewIfNeeded();
      await page.getByRole('button', { name: /Install/ }).last().click();
      await page.locator('[data-review-sensitive]').waitFor({ timeout: 30000 });
      await page.locator('[data-review-arch]').waitFor({ timeout: 30000 });
      expect(/full control of the server/.test(await page.locator('[data-review-sensitive]').innerText()), `${name}: the Docker socket is said in words`);
      expect(await page.getByRole('button', { name: 'Create' }).isDisabled(), `${name}: Create waits for the ticks`);
      await shot(page, name, 'market-sensitive', true);
      for (const box of await page.locator('[data-review-sensitive] input[type=checkbox]').all()) await box.check();
      await page.locator('[data-review-arch] input[type=checkbox]').check();
      await page.waitForTimeout(300);
      expect(await page.getByRole('button', { name: 'Create' }).isEnabled(), `${name}: Create once everything is ticked`);
      await dialogFits(page, name, 'the sensitive review');
      await noSideScroll(page, name, 'the Market listing');
      created = true;
    } catch (e) {
      expect(false, `${name}: ${e.message.split('\n')[0]}`);
      await shot(page, name, 'error', true).catch(() => {});
      created = true;
    }
    await browser.close();
  }
}

note(`\n${failures.length} failure(s)`);
failures.forEach((f) => note(' -', f));
process.exit(failures.length ? 1 : 0);

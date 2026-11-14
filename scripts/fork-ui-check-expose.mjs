// Fork addition: checks "expose to the internet" (components/exposure.jsx,
// src/cloudflare/expose.go) in headless Chromium, Firefox and WebKit at
// 1366x900 and 390x844, against a throwaway Cosmos, a stand-in for
// Cloudflare's API and a stand-in router. Never the production instance,
// never the real Cloudflare or router.
//
// What it needs, on one Docker network made with --internal (no way out):
// - scripts/fork-fake-cloudflare.go with the alias api.cloudflare.com (FAKE is
//   its control port) and a folder at /shared (it writes /shared/ca.pem);
// - scripts/fork-fake-igd.go at a fixed address (IGD is its control port);
// - a docker:27-dind (DOCKER_HOST of the test Cosmos) with three containers:
//   sonarr (image lscr.io/linuxserver/sonarr, -p 8989:8989), minecraft (image
//   itzg/minecraft-server, -p 25565:25565/tcp -p 25565:25565/udp -p 8123:8123
//   -p 25575:25575), db (image postgres:16, -p 5432:5432); any image tagged so,
//   running "sleep infinity"; and a registry in it (registry:2 on 5000) holding localhost:5000/gitea:latest for the install (the pull of a compose has to work);
// - the test Cosmos at SERVER_IP, fresh, run in a loop, with --cap-add NET_ADMIN,
//   COSMOS_HTTP_PORT=2052, COSMOS_HTTPS_PORT=2053, SSL_CERT_FILE=/shared/ca.pem,
//   and its default route through the stand-in router
//   (docker run --rm --network container:<cosmos> --cap-add NET_ADMIN
//    --entrypoint ip docker:27-dind route replace default via <IGD address>).
// It finishes the set-up wizard and connects example.org itself.
// Screenshots go to ./shots.
import { chromium, firefox, webkit, request } from 'playwright';

const ip = process.env.SERVER_IP || '172.31.57.10';
const plain = `http://${ip}:2052`;
const base = `https://${ip}:2053`;
const fake = process.env.FAKE || 'http://172.31.57.3:8080';
const igd = process.env.IGD || 'http://172.31.57.2:8080';
const password = 'Test-Passw0rd!x';
const GOOD = 'cfut_dGVzdC10b2tlbi1mb3ItdGhlLWJyb3dzZXItY2hlY2s0123456789';
const engines = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const sizes = (process.env.SIZES || 'd,m').split(',');

const note = (...a) => console.log(...a);
const failures = [];
const expect = (ok, what) => { note(ok ? '  ok  ' : '  FAIL', what); if (!ok) failures.push(what); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const api = await request.newContext({ ignoreHTTPSErrors: true });
const records = async () => (await (await api.get(fake + '/records')).json()) || [];
const record = async (name) => (await records()).filter((r) => r.name === name && r.type === 'A');
const table = async () => (await (await api.get(igd + '/table')).json()) || {};
const until = async (what, check, seconds = 90) => {
  for (let i = 0; i < seconds; i++) {
    try { if (await check()) return true; } catch (e) { /* reloading */ }
    await sleep(1000);
  }
  expect(false, 'timed out: ' + what);
  return false;
};
const serverUp = () => until('the server answers over HTTPS', async () => (await api.get(base + '/cosmos/api/status')).status() < 500);

// --- the wizard and the domain, once
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
  if (!(config.HTTPConfig.ProxyConfig.Routes || []).some((r) => r.Name === 'minecraft')) {
    config.HTTPConfig.AdvertisedAddress = '203.0.113.10';
    config.HTTPConfig.ProxyConfig.Routes = [
      { Name: 'sonarr', Mode: 'SERVAPP', Target: 'http://sonarr:8989', UseHost: true, Host: 'sonarr.local' },
      { Name: 'minecraft', Mode: 'SERVAPP', Target: 'http://minecraft:8123', UseHost: true, Host: 'minecraft.local' },
    ];
    note('settings:', (await ctx.put(base + '/cosmos/api/config', { data: config })).status());
    await sleep(5000);
    await serverUp();
    await ctx.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
    await ctx.post(base + '/cosmos/api/sudo', { data: { password } });
    const zone = { HTTPSCertificateMode: 'SELFSIGNED', DNSChallengeProvider: 'cloudflare', DNSChallengeConfig: { CF_DNS_API_TOKEN: GOOD }, ManageRecords: true };
    note('domain:', (await ctx.put(base + '/cosmos/api/zones/example.org', { data: zone })).status());
    await sleep(5000);
    await serverUp();
  }
  await ctx.dispose();
}

const open = async (type, name, viewport) => {
  const browser = await type.launch();
  const ctx = await browser.newContext({ viewport, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => failures.push(`${name} PAGEERROR: ${e.message.slice(0, 300)}`));
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
const shot = (page, name, what) => page.screenshot({ path: `shots/${name}-${what}.png`, fullPage: false });
const openDialog = async (page, container) => {
  await page.goto(base + '/cosmos-ui/servapps/containers/' + container, { waitUntil: 'domcontentloaded' });
  await page.locator('[data-expose-open]').first().waitFor({ timeout: 45000 });
  await page.waitForTimeout(1000);
  await page.locator('[data-expose-open]').first().click();
  await page.locator('[data-expose-form]').waitFor({ timeout: 30000 });
  await page.waitForTimeout(800);
};

let changedOnce = false;
for (const engine of engines) {
  for (const size of sizes) {
    const viewport = size === 'm' ? { width: 390, height: 844 } : { width: 1366, height: 900 };
    const name = `${engine}-${size === 'm' ? 390 : 1366}`;
    const { browser, page } = await open({ chromium, firefox, webkit }[engine], name, viewport);
    try {
      await openDialog(page, 'minecraft');
      const dialog = page.locator('[data-expose-dialog]');
      expect(await dialog.locator('[data-expose-web="example.org"]').count() === 1, `${name}: the web interface under example.org is offered`);
      expect(/https:\/\/minecraft\.example\.org:2053/.test(await dialog.innerText()), `${name}: the address shows the port it is reached on`);
      for (const key of ['minecraft:25565/tcp', 'minecraft:25565/udp', 'minecraft:25575/tcp']) {
        expect(await dialog.locator(`[data-expose-port="${key}"]`).count() === 1, `${name}: port ${key} is offered`);
      }
      expect(await dialog.locator('[data-expose-port="minecraft:8123/tcp"]').count() === 0, `${name}: the web port is not offered as a port`);
      await shot(page, name, 'dialog');
      await dialogFits(page, name, 'the dialog');

      if (!changedOnce) {
        changedOnce = true;
        // web through Cloudflare, the game port under its own direct name
        await dialog.locator('[data-expose-web="example.org"] input[type=checkbox]').first().check();
        await page.waitForTimeout(300);
        expect(/Through Cloudflare/.test(await dialog.innerText()), `${name}: through Cloudflare is offered (Cosmos is on 2053)`);
        await dialog.locator('[data-expose-port="minecraft:25565/tcp"] input[type=checkbox]').check();
        await page.waitForTimeout(300);
        const proposed = await dialog.locator('[data-expose-port-name="minecraft:25565/tcp"]').inputValue();
        expect(proposed === 'minecraft-direct.example.org', `${name}: the port's proposed name is its own (${proposed})`);
        expect(await dialog.locator('[data-expose-cost]').count() === 1, `${name}: what direct costs is said`);
        await shot(page, name, 'dialog-ticked');
        await dialog.locator('[data-expose-save]').click();
        await page.locator('[data-expose-dialog] .MuiAlert-standardSuccess, [data-expose-dialog] .MuiAlert-standardError').first().waitFor({ timeout: 90000 });
        expect(await page.locator('[data-expose-dialog] .MuiAlert-standardError').count() === 0, `${name}: saved without an error`);
        await until('minecraft.example.org proxied at Cloudflare', async () => { const r = await record('minecraft.example.org'); return r.length === 1 && r[0].proxied; });
        await until('minecraft-direct.example.org DNS only', async () => { const r = await record('minecraft-direct.example.org'); return r.length === 1 && !r[0].proxied && r[0].content === '203.0.113.10'; });
        await until('the router forwards TCP 25565', async () => !!(await table())['TCP/25565']);
        await page.waitForTimeout(3000);
        await shot(page, name, 'dialog-saved');
        await page.keyboard.press('Escape');
        await serverUp();
        await openDialog(page, 'minecraft');
      }
      const now = page.locator('[data-expose-now]');
      await now.waitFor({ timeout: 30000 });
      await page.waitForTimeout(1500);
      const nowText = await now.innerText();
      expect(/minecraft\.example\.org/.test(nowText), `${name}: the web address is listed as exposed`);
      expect(/minecraft-direct\.example\.org:25565/.test(nowText), `${name}: the forwarded port shows under its direct name with the router's port (${nowText.replace(/\s+/g, ' ').slice(0, 160)})`);
      await shot(page, name, 'dialog-now');
      await dialogFits(page, name, 'the exposed state');
      await page.keyboard.press('Escape');

      // a database port is behind the door, and needs the risk said
      await openDialog(page, 'db');
      const door = page.locator('[data-expose-door]');
      expect(await door.count() === 1, `${name}: the database port is behind the door`);
      expect(await page.locator('[data-expose-port="db:5432/tcp"]').isVisible() === false, `${name}: and not shown until it is opened`);
      await door.click();
      await page.locator('[data-expose-port="db:5432/tcp"] input[type=checkbox]').first().check();
      await page.waitForTimeout(400);
      expect(await page.locator('[data-expose-save]').isDisabled(), `${name}: Save waits for the risk to be confirmed`);
      await shot(page, name, 'dialog-risky');
      await dialogFits(page, name, 'the risky port');
      await page.keyboard.press('Escape');

      // the card's chip
      await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
      await page.locator('[data-expose-open]').first().waitFor({ timeout: 45000 });
      await page.waitForTimeout(1500);
      await shot(page, name, 'servapps');
      await noSideScroll(page, name, 'ServApps');
    } catch (e) {
      expect(false, `${name}: ${e.message.split('\n')[0]}`);
      await shot(page, name, 'error').catch(() => {});
    }
    await browser.close();
  }
}

// --- an installation through the compose import, once, in Chromium
if (!process.env.SKIP_INSTALL) {
  const name = 'install-chromium-1366';
  const { browser, page } = await open(chromium, name, { width: 1366, height: 900 });
  try {
    await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
    await page.getByRole('button', { name: 'Import Compose File' }).click();
    const compose = [
      'services:',
      '  gitea:',
      '    image: localhost:5000/gitea:latest',
      '    container_name: gitea',
      '    command: sleep infinity',
      '    ports:',
      '      - "3000:3000"',
      '      - "2222:22"',
      '    x-routes:',
      '      - name: gitea',
      '        mode: SERVAPP',
      '        target: http://gitea:3000',
      '        useHost: true',
      '        host: gitea.local',
    ].join('\n');
    await page.getByPlaceholder(/Paste your docker-compose/).fill(compose);
    await page.getByRole('button', { name: 'Next' }).click();
    const install = page.locator('[data-expose-install]');
    await install.waitFor({ timeout: 30000 });
    await page.waitForTimeout(1500);
    const text = await install.innerText();
    expect(/gitea\.example\.org/.test(text), `${name}: the web address is proposed`);
    expect(await install.locator('[data-expose-web="example.org"] input[type=checkbox]').first().isChecked() === false, `${name}: nothing ticked by default`);
    expect(await install.locator('[data-expose-door]').count() === 1, `${name}: port 22 is behind the door`);
    await install.locator('[data-expose-web="example.org"] input[type=checkbox]').first().check();
    await shot(page, name, 'install');
    await page.getByRole('button', { name: 'Create' }).click();
    await page.locator('[data-expose-install-result]').waitFor({ timeout: 180000 });
    expect(/is set up for the internet/.test(await page.locator('[data-expose-install-result]').innerText()), `${name}: exposed once created`);
    await shot(page, name, 'install-done');
    await until('gitea.example.org at Cloudflare', async () => (await record('gitea.example.org')).length === 1);
    // creating it again under the same name is refused, not a replacement
    await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
    await page.getByRole('button', { name: 'Import Compose File' }).click();
    await page.getByPlaceholder(/Paste your docker-compose/).fill(compose);
    await page.getByRole('button', { name: 'Next' }).click();
    await page.getByRole('button', { name: 'Create' }).click();
    await page.locator('[data-create-failed]').waitFor({ timeout: 60000 });
    expect(/exists already/.test(await page.locator('[data-create-failed]').innerText()), `${name}: an existing name is refused`);
    expect(await page.getByRole('button', { name: 'Create' }).isEnabled(), `${name}: the button comes back after a failure`);
    await shot(page, name, 'install-refused');
  } catch (e) {
    expect(false, `${name}: ${e.message.split('\n')[0]}`);
    await shot(page, name, 'error').catch(() => {});
  }
  await browser.close();
}

// --- withdrawn: the records and the mapping go
if (!process.env.SKIP_WITHDRAW) {
  const name = 'withdraw-chromium-1366';
  const { browser, page } = await open(chromium, name, { width: 1366, height: 900 });
  try {
    await openDialog(page, 'minecraft');
    const dialog = page.locator('[data-expose-dialog]');
    await dialog.locator('[data-expose-web="example.org"] input[type=checkbox]').first().uncheck();
    await dialog.locator('[data-expose-port="minecraft:25565/tcp"] input[type=checkbox]').uncheck();
    await dialog.locator('[data-expose-save]').click();
    await page.locator('[data-expose-dialog] .MuiAlert-standardSuccess, [data-expose-dialog] .MuiAlert-standardError').first().waitFor({ timeout: 90000 });
    await until('the web record is gone', async () => (await record('minecraft.example.org')).length === 0);
    await until('the direct record is gone', async () => (await record('minecraft-direct.example.org')).length === 0);
    await until('the router no longer forwards 25565', async () => !(await table())['TCP/25565']);
    const kept = await record('example.org');
    expect(kept.length === 1 && kept[0].proxied, 'the domain\'s own record was never touched');
  } catch (e) {
    expect(false, `${name}: ${e.message.split('\n')[0]}`);
  }
  await browser.close();
}

note(failures.length ? `\n${failures.length} problem(s):\n- ` + failures.join('\n- ') : '\nall passed');
process.exit(failures.length ? 1 : 0);

// Fork addition: checks the "New URL" dialog (pages/servapps/exposeModal.jsx)
// with its internet choices (components/exposure.jsx' RouteExposure, the
// same form and server action as the app's Internet dialog), and the port
// proposal of the new-container form's URL tab (utils/routeGuide.js'
// containerPorts reading what its Network tab holds), in headless Chromium,
// Firefox and WebKit at 1366x900 and 390x844. Against a throwaway Cosmos, a
// stand-in for Cloudflare's API and a stand-in router; never the production
// instance, never the real Cloudflare or router.
//
// What it needs, on one Docker network made with --internal (no way out),
// the same set-up as scripts/fork-ui-check-expose.mjs without the registry:
// - scripts/fork-fake-cloudflare.go with the alias api.cloudflare.com (FAKE is
//   its control port) and a folder at /shared (it writes /shared/ca.pem);
// - scripts/fork-fake-igd.go at a fixed address (IGD is its control port);
// - a docker:29-dind (DOCKER_HOST of the test Cosmos) with twelve containers
//   without a URL: appa-1 … appa-6 (-p 810<n>:80) and appb-1 … appb-6
//   (-p 820<n>:80), any image (for example alpine tagged localtest/web:1)
//   running "sleep infinity"; and appc-1 (-p 8301:80) for the refusal;
// - the test Cosmos at SERVER_IP, fresh, run in a loop, with --cap-add NET_ADMIN,
//   COSMOS_HTTP_PORT=2052, COSMOS_HTTPS_PORT=2053, SSL_CERT_FILE=/shared/ca.pem,
//   and its default route through the stand-in router
//   (docker run --rm --network container:<cosmos> --cap-add NET_ADMIN
//    --entrypoint ip docker:29-dind route replace default via <IGD address>);
// - Playwright 1.49.1 installed next to this script (the network has no way out).
// It finishes the set-up wizard and connects example.org itself (as
// fork-ui-check-expose.mjs does). Per engine and size n: appa-n gets a local
// URL with nothing ticked (the dialog closes, as before), appb-n a local URL
// plus its web interface under example.org through Cloudflare (the dialog
// says how it went; the record is proxied at the stand-in; the app's
// Internet chip lists the address). Once (the first run), a refusal of the
// expose request (answered by the browser in the server's place) is said
// with its reason, and the URL stays. Then the new-container form: the URL
// tab, the Network tab with port 8080, the URL tab again proposes 8080.
// Screenshots go to ./shots.
import { chromium, firefox, webkit, request } from 'playwright';

const ip = process.env.SERVER_IP || '172.31.57.10';
const plain = `http://${ip}:2052`;
const base = `https://${ip}:2053`;
const fake = process.env.FAKE || 'http://172.31.57.3:8080';
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
  if (!config.HTTPConfig.AdvertisedAddress) {
    config.HTTPConfig.AdvertisedAddress = '203.0.113.10';
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
  page.on('pageerror', (e) => {
    // the server reloads after a new URL: a poll of the page in flight then fails (as the any-image and sensitive-edit checks count it)
    if (/Failed to fetch|Load failed|NetworkError/.test(e.message)) return;
    failures.push(`${name} PAGEERROR: ${e.message.slice(0, 300)} ${String(e.stack || '').split('\n').slice(1, 3).join(' ').slice(0, 300)}`);
  });
  const a = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  const b = await ctx.request.post(base + '/cosmos/api/sudo', { data: { password } });
  note(`\n== ${name}: login ${a.status()}/${b.status()}`);
  return { browser, ctx, page };
};
const routesOf = async (page, app) => {
  const config = await page.evaluate(async () => (await (await fetch('/cosmos/api/config', { credentials: 'same-origin' })).json()).data);
  return ((config && config.HTTPConfig.ProxyConfig.Routes) || []).filter((r) => new RegExp('^https?://' + app + '(:|$)').test(r.Target || ''));
};
const noSideScroll = async (page, name, where) => {
  const over = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(over <= 1, `${name}: no sideways scroll on ${where} (${over}px over)`);
};
const dialogFits = async (page, name, where) => {
  const box = await page.locator('[role=dialog]:visible').last().boundingBox();
  expect(!!box && box.x >= 0 && box.x + box.width <= page.viewportSize().width + 1, `${name}: the dialog fits the screen (${where})`);
  const inside = await page.locator('[role=dialog]:visible').last().evaluate((dialog) => {
    const content = dialog.querySelector('.MuiDialogContent-root');
    return !content || content.scrollWidth <= content.clientWidth + 1;
  });
  expect(inside, `${name}: nothing sticks out of the dialog (${where})`);
};
const shot = (page, name, what, fullPage = false) => page.screenshot({ path: `shots/${name}-${what}.png`, fullPage });
// the ServApps page, filtered to one app, and its "New" chip
const card = async (page, app) => {
  await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
  const search = page.getByPlaceholder('Search...');
  await search.waitFor({ timeout: 45000 });
  await page.waitForTimeout(1500);
  await search.fill(app);
  await page.waitForTimeout(800);
};
// a tab of the new-container form: tabs on a wide screen, a select on a phone
const gotoTab = async (page, label) => {
  const tab = page.getByRole('tab', { name: label });
  if (await tab.count() && await tab.first().isVisible()) {
    await tab.first().click();
  } else {
    await page.locator('.MuiSelect-select').first().click();
    await page.getByRole('option', { name: label }).click();
  }
  await page.waitForTimeout(1200);
};
// the installable app's "A new version of Cosmos is ready" note can sit over the buttons: "Later"
const later = async (page) => {
  const button = page.getByRole('button', { name: 'Later' });
  if (await button.count() && await button.first().isVisible()) await button.first().click();
};
const openNewUrl = async (page, app) => {
  await card(page, app);
  await later(page);
  await page.locator('.MuiChip-root', { has: page.locator('.MuiChip-label', { hasText: /^New$/ }) }).first().click();
  await page.locator('[data-new-url-dialog] [data-expose-route] [data-expose-form], [data-new-url-dialog] [data-new-url-admin-only]').first().waitFor({ timeout: 30000 });
  await page.waitForTimeout(1500);
  return page.locator('[data-new-url-dialog]');
};

let n = 0;
for (const engine of engines) {
  for (const size of sizes) {
    n += 1;
    const viewport = size === 'm' ? { width: 390, height: 844 } : { width: 1366, height: 900 };
    const name = `${engine}-${size === 'm' ? 390 : 1366}`;
    const { browser, page } = await open({ chromium, firefox, webkit }[engine], name, viewport);
    try {
      // 1. nothing ticked: the local URL, the dialog closes as before
      const a = `appa-${n}`;
      let dialog = await openNewUrl(page, a);
      const text = await dialog.innerText();
      expect(!/Attach a domain|also under/i.test(text), `${name}: the old domain picker is gone`);
      expect(await dialog.locator('[data-expose-web="example.org"]').count() === 1, `${name}: the internet choices offer example.org`);
      expect(await dialog.locator('[data-expose-web="example.org"] input[type=checkbox]').first().isChecked() === false, `${name}: nothing ticked by default`);
      expect(new RegExp(`https://${a}\\.example\\.org`).test(text), `${name}: the internet address is proposed (${a}.example.org)`);
      await shot(page, name, 'new-url', true);
      await dialogFits(page, name, 'the New URL dialog');
      await later(page);
      await dialog.locator('[data-new-url-confirm]').click();
      await until(`${a}: the dialog closes`, async () => (await page.locator('[data-new-url-dialog]').count()) === 0, 60);
      await serverUp();
      await until(`${a}: its local URL exists`, async () => (await routesOf(page, a)).some((r) => r.Host === `${a}.local` || /\.local$/.test(r.Host || '')), 60);
      const aRoutes = await routesOf(page, a);
      expect(aRoutes.length === 1, `${name}: ${a} has one URL, no copy (${aRoutes.map((r) => r.Host).join(', ')})`);
      expect((aRoutes[0] || {}).Target === `http://${a}:80`, `${name}: it leads to the port the form proposed (${(aRoutes[0] || {}).Target})`);

      // 2. the web interface under example.org through Cloudflare
      const b = `appb-${n}`;
      dialog = await openNewUrl(page, b);
      await dialog.locator('[data-expose-web="example.org"] input[type=checkbox]').first().check();
      await page.waitForTimeout(500);
      const ticked = await dialog.innerText();
      expect(/Through Cloudflare/.test(ticked), `${name}: through Cloudflare is offered`);
      const chosen = (await dialog.locator('[data-expose-web="example.org"] [aria-checked=true]').allInnerTexts()).join(' | ');
      expect(/Through Cloudflare/.test(chosen), `${name}: and proposed (${chosen})`);
      expect(/set up once the URL is created/.test(ticked), `${name}: the dialog says when the ticked part is done`);
      await dialog.locator('.MuiDialogContent-root').evaluate((el) => { el.scrollTop = el.scrollHeight; });
      await page.waitForTimeout(300);
      await shot(page, name, 'new-url-ticked', true);
      await dialogFits(page, name, 'the ticked dialog');
      await later(page);
      await dialog.locator('[data-new-url-confirm]').click();
      await page.locator('[data-new-url-outcome]').waitFor({ timeout: 120000 });
      const outcome = await page.locator('[data-new-url-outcome]').innerText();
      expect(/is created/.test(outcome), `${name}: says the URL is created`);
      expect(await page.locator('[data-new-url-exposed=ok]').count() === 1, `${name}: says it is set up for the internet (${outcome.replace(/\s+/g, ' ').slice(0, 200)})`);
      await shot(page, name, 'new-url-done');
      await dialogFits(page, name, 'the outcome');
      const bRoutes = await routesOf(page, b);
      expect(bRoutes.some((r) => /\.local$/.test(r.Host || '')) && bRoutes.some((r) => r.Host === `${b}.example.org`), `${name}: ${b} has its local URL and its internet address (${bRoutes.map((r) => r.Host).join(', ')})`);
      await until(`${b}.example.org proxied at Cloudflare`, async () => { const r = await record(`${b}.example.org`); return r.length === 1 && r[0].proxied; });
      await page.locator('[data-new-url-close]').click();
      await page.waitForTimeout(2500);
      // the card read again: the app's Internet chip shows the address
      await page.locator('[data-expose-open]').first().waitFor({ timeout: 30000 });
      await page.locator('[data-expose-open]').first().click();
      const now = page.locator('[data-expose-dialog] [data-expose-now]');
      await now.waitFor({ timeout: 30000 });
      await page.waitForTimeout(1500);
      expect(new RegExp(`${b}\\.example\\.org`).test(await now.innerText()), `${name}: the Internet chip lists ${b}.example.org`);
      await shot(page, name, 'internet-chip');
      await page.keyboard.press('Escape');
      await page.waitForTimeout(500);
      await noSideScroll(page, name, 'ServApps');

      // 2b. once: the server refuses the internet part; the dialog says why and the URL stays
      if (n === 1) {
        const c = 'appc-1';
        await page.route('**/cosmos/api/expose', (route) => route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ status: 'error', message: 'Refused for the test.' }) }));
        dialog = await openNewUrl(page, c);
        await dialog.locator('[data-expose-web="example.org"] input[type=checkbox]').first().check();
        await later(page);
      await dialog.locator('[data-new-url-confirm]').click();
        await page.locator('[data-new-url-outcome]').waitFor({ timeout: 120000 });
        const refused = await page.locator('[data-new-url-outcome]').innerText();
        expect(await page.locator('[data-new-url-exposed=failed]').count() === 1 && /Refused for the test\./.test(refused) && /The URL is kept/.test(refused),
          `${name}: a refusal is said with its reason (${refused.replace(/\s+/g, ' ').slice(0, 200)})`);
        expect((await routesOf(page, c)).length === 1, `${name}: and the URL is kept`);
        await shot(page, name, 'new-url-refused');
        await page.unroute('**/cosmos/api/expose');
        await page.locator('[data-new-url-close]').click();
        await page.waitForTimeout(1000);
      }

      // 3. the new-container form: the URL tab proposes the port its Network tab holds
      await page.goto(base + '/cosmos-ui/servapps/new-service', { waitUntil: 'domcontentloaded' });
      await page.locator('#name').waitFor({ timeout: 45000 });
      await page.waitForTimeout(2000);
      await later(page);
      await page.locator('#name').fill(`formapp-${n}`);
      await page.locator('#image').fill('localtest/none:1');
      await page.getByRole('button', { name: /Next/ }).first().click();
      await page.waitForTimeout(1000);
      await page.locator('input[type=checkbox]').first().check();
      await page.waitForTimeout(2000);
      const before = await page.locator('form').last().innerText();
      expect(/declares no port/.test(before), `${name}: without ports the URL tab asks for one`);
      await gotoTab(page, /^Network$/);
      await page.getByRole('button', { name: 'Add' }).first().click();
      await page.waitForTimeout(400);
      await page.getByLabel('Host port').last().fill('8180');
      await page.getByLabel(/Container port/i).last().fill('8080');
      await page.waitForTimeout(800);
      await shot(page, name, 'form-network', true);
      await gotoTab(page, /^URL$/);
      await page.waitForTimeout(800);
      const picked = (await page.locator('#Target .MuiChip-root[aria-checked=true]').allInnerTexts()).map((x) => x.trim()).join(',');
      const after = await page.locator('form').last().innerText();
      expect(/8080/.test(picked), `${name}: the URL tab proposes port 8080 from the Network tab (picked: ${picked || 'none'})`);
      expect(/only port this app has/.test(after), `${name}: and says why`);
      await shot(page, name, 'form-url', true);
      await noSideScroll(page, name, 'the URL tab');
    } catch (e) {
      expect(false, `${name}: ${e.message.split('\n')[0]}`);
      await shot(page, name, 'error', true).catch(() => {});
    }
    await browser.close();
  }
}

note(failures.length ? `\n${failures.length} problem(s):\n- ` + failures.join('\n- ') : '\nall passed');
process.exit(failures.length ? 1 : 0);

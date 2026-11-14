// Fork addition: tries the names for use at home (components/homeNames.jsx)
// in headless Chromium, Firefox and WebKit, at 1366x900 and 390x844.
//
// What it expects: what `KEEP=1 CHECK=home sh scripts/fork-acme-pebble.sh`
// leaves running (a test Cosmos with example.org connected to the Cloudflare
// stand-in, certificates from Pebble, the names for use at home switched off
// again), with the real pages copied into the build folder the test server
// serves (<SCRATCH>/bin/static). Run it on that --internal network, with the
// stand-in as name server (--dns <SUBNET>.2) so that the browsers resolve
// the home names like a device at home would, and with playwright@1.49.1
// installed beforehand (the network has no way out):
//
//   docker run --rm --network <PREFIX>-net --dns <SUBNET>.2 --ipc=host -v <dir>:/work -w /work \
//     mcr.microsoft.com/playwright:v1.49.1-noble node fork-ui-check-home-names.mjs
//
// SERVER=<SUBNET>.10 (default 172.31.58.10). Screenshots go to ./shots.
// ENGINES=chromium,firefox,webkit SIZES=d,m. Nothing leaves the network.
import { chromium, firefox, webkit, request } from 'playwright';

const ip = process.env.SERVER || '172.31.58.10';
const base = `https://${ip}:2053`;
const password = 'Test-Passw0rd!x';
const engines = { chromium, firefox, webkit };
const names = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const sizes = { d: { width: 1366, height: 900 }, m: { width: 390, height: 844 } };
const wanted = (process.env.SIZES || 'd,m').split(',');
const note = (...a) => console.log(...a);
const failures = [];
const expect = (ok, what, detail) => { note(ok ? '  ok  ' : '  FAIL', what, detail === undefined ? '' : '| ' + String(detail).slice(0, 300)); if (!ok) failures.push(what); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const login = async (ctx) => {
  const a = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  const b = await ctx.request.post(base + '/cosmos/api/sudo', { data: { password } });
  return a.status() + '/' + b.status();
};
const api = await request.newContext({ ignoreHTTPSErrors: true });
await login({ request: api });
const homeState = async () => (await (await api.get(base + '/cosmos/api/home-names')).json()).data;
const setHome = async (enabled) => {
  const r = await api.post(base + '/cosmos/api/home-names', { data: { zone: 'example.org', label: 'home', enabled } });
  await sleep(4000);
  return r.status();
};

const noSideScroll = async (page, name, where) => {
  const over = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(over <= 1, `${name}: no sideways scroll on ${where} (${over}px over)`);
};
const insideCard = async (page, name) => {
  const out = await page.locator('[data-home-names]').evaluate((section) => {
    const card = section.closest('.MuiCard-root') || section.parentElement;
    const limit = card.getBoundingClientRect().right + 1;
    return [...section.querySelectorAll('*')].filter((el) => el.getBoundingClientRect().width > 0 && el.getBoundingClientRect().right > limit).length;
  });
  expect(out === 0, `${name}: nothing sticks out of the card (${out} elements)`);
};
const shot = (page, name, what) => page.screenshot({ path: `shots/home-${name}-${what}.png`, fullPage: false });
const domains = async (page) => {
  await page.goto(base + '/cosmos-ui/config-general/https', { waitUntil: 'domcontentloaded' });
  await page.locator('[data-home-names]').waitFor({ timeout: 45000 });
  await page.waitForTimeout(1500);
};

let first = true;
for (const engine of names) {
  for (const size of wanted) {
    const name = `${engine}-${size === 'd' ? 1366 : 390}`;
    const browser = await engines[engine].launch();
    const ctx = await browser.newContext({ viewport: sizes[size], ignoreHTTPSErrors: true });
    const page = await ctx.newPage();
    page.on('pageerror', (e) => failures.push(`${name} PAGEERROR: ${e.message.slice(0, 300)}`));
    note(`\n== ${name}: login`, await login(ctx));

    // switched on from the card once (first run), by the API for the others
    if (!first && !(await homeState()).zones.some((z) => z.active)) {
      note('  switch on through the API:', await setHome(true));
    }
    await domains(page);
    const zone = page.locator('[data-home-zone="example.org"]');
    if (first) {
      expect(await zone.locator('[data-home-state="off"]').count() === 1, `${name}: the card says the names are off`);
      expect(/radarr\.home\.example\.org/.test(await zone.innerText()), `${name}: it shows what a name will look like`);
      await page.locator('#home-label-example\\.org').fill('Bad_');
      expect(/letters, digits and hyphens/.test(await zone.innerText()), `${name}: a label that cannot be one is said under the field`);
      await page.locator('#home-label-example\\.org').fill('home');
      await zone.locator('[data-home-state="off"]').scrollIntoViewIfNeeded();
      await shot(page, name, '1-off');
      await zone.locator('[data-home-action="on"]').click();
      await zone.locator('[data-home-state="on"]').waitFor({ timeout: 60000 });
      first = false;
    } else {
      await zone.locator('[data-home-state="on"]').waitFor({ timeout: 30000 });
    }
    const text = await zone.innerText();
    expect(/cosmos\.home\.example\.org/.test(text) && /<app>\.home\.example\.org/.test(text), `${name}: on: the names and the panel's are named`, text.slice(0, 200));

    // the record and the certificate, as the server reports them (both come within a minute or two)
    let settled = false;
    for (let i = 0; i < 24 && !settled; i++) {
      const row = (await homeState()).zones.find((z) => z.zone === 'example.org');
      settled = row && row.record === 'ok' && row.certificateCovered;
      if (!settled) await sleep(5000);
    }
    await domains(page);
    const now = await zone.innerText();
    expect(/points at 172\.31\.58\.10 \(DNS only\)/.test(now), `${name}: the record and the address it points at`, now.slice(0, 300));
    expect(/Let's Encrypt certificate: no warning/.test(now), `${name}: the certificate covers the names`);

    await zone.locator('[data-home-action="server"]').click();
    await page.waitForTimeout(2500);
    expect(/server's own resolver finds cosmos\.home\.example\.org at the server's private address/.test(await zone.innerText()), `${name}: the check from the server`);
    await zone.locator('[data-home-action="browser"]').click();
    await page.waitForTimeout(4000);
    expect(/This device opened cosmos\.home\.example\.org/.test(await zone.innerText()), `${name}: the check from the browser opens the panel's home name`, (await zone.innerText()).slice(-300));

    await zone.getByRole('button', { name: 'What it costs' }).click();
    await zone.getByRole('button', { name: 'The name does not resolve at home' }).click();
    await page.waitForTimeout(800);
    const help = await zone.innerText();
    expect(/private address .* is published in public DNS/.test(help) && /Fritz!Box/.test(help) && /rebind-domain-ok=\/home\.example\.org\//.test(help),
      `${name}: the cost and the router help, with the domain to allow`);
    await zone.locator('[data-home-action="server"]').scrollIntoViewIfNeeded();
    await shot(page, name, '2-on');
    await page.locator('[data-home-rebind]').scrollIntoViewIfNeeded();
    await shot(page, name, '3-help');
    await noSideScroll(page, name, 'the Domains tab');
    await insideCard(page, name);

    // the URL form proposes the home name and says every URL gets one
    await page.goto(base + '/cosmos-ui/config-url', { waitUntil: 'domcontentloaded' });
    await page.getByRole('button', { name: /Create/ }).first().click();
    const dialog = page.locator('[role=dialog]').last();
    await dialog.waitFor({ timeout: 20000 });
    await dialog.getByText('Another device or address').click();
    await dialog.locator('#Host').fill('nas.local');
    await page.waitForTimeout(800);
    const form = await dialog.innerText();
    expect(/At home it is also reached at nas\.home\.example\.org, by itself/.test(form), `${name}: the URL form says the home name comes by itself`, form.slice(0, 400));
    await dialog.locator('#Host').scrollIntoViewIfNeeded();
    await shot(page, name, '4-url-form');
    const box = await dialog.boundingBox();
    expect(!!box && box.x >= 0 && box.x + box.width <= page.viewportSize().width + 1, `${name}: the URL form fits the screen`);
    await page.keyboard.press('Escape');

    await browser.close();
  }
}

// switched off from the card at the end
{
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: sizes.d, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  await login(ctx);
  await domains(page);
  const zone = page.locator('[data-home-zone="example.org"]');
  await zone.locator('[data-home-action="off"]').click();
  await zone.locator('[data-home-state="off"]').waitFor({ timeout: 60000 });
  expect(!(await homeState()).zones.some((z) => z.active), 'chromium: switched off from the card');
  await browser.close();
}

note(failures.length ? `\n${failures.length} FAILED:\n  - ` + failures.join('\n  - ') : '\nALL OK');
process.exit(failures.length ? 1 : 0);

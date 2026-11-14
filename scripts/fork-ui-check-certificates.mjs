// Fork addition: looks at the certificates in headless Chromium, Firefox and
// WebKit, at 1366x900 and 390x844: the alerts on the home page, the
// "Certificates" card under Configuration > Domains and the dialog "Add
// another domain". Nothing reaches Let's Encrypt, Cloudflare or anyone's DNS.
//
// What it expects: what `KEEP=1 sh scripts/fork-acme-pebble.sh` leaves
// running (Pebble, the stand-in for Cloudflare that is also the name server,
// and a test Cosmos at SERVER_IP that has example.org through Cloudflare,
// example.com delegated to it and app.example.net without DNS access), with
// the panel's pages (./static of `npx vite build --base=/cosmos-ui/`) copied
// into the scratch folder's bin/static. Run it from the Playwright image on
// that network, with playwright installed next to it beforehand (the network
// is --internal):
//
//   docker run --rm --network <prefix>-net --ipc=host -v <dir with node_modules and this file>:/work \
//     -v <repository>:/repo:ro -w /work -e SERVER_IP=<subnet>.10 -e FAKE=http://<subnet>.2:8080 \
//     mcr.microsoft.com/playwright:v1.49.1-noble node fork-ui-check-certificates.mjs
//
// A full run makes 18 of the 30 lookups an hour the server allows for
// "Add another domain" (src/certs/http.go): restart the test server's
// process before a second run within the hour.
//
// ENGINES=chromium,firefox,webkit SIZES=d,m choose what runs. Screenshots
// go to ./shots.
import { chromium, firefox, webkit, request } from 'playwright';
import { mkdirSync } from 'node:fs';

const { homeAlerts } = await import(process.env.LOGIC || '/repo/client/src/utils/certificates.js');

const ip = process.env.SERVER_IP || '172.31.57.10';
const base = `https://${ip}:2053`;
const fake = process.env.FAKE || 'http://172.31.57.2:8080';
const password = 'Test-Passw0rd!x';
const engines = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const sizes = (process.env.SIZES || 'd,m').split(',');
const VIEWPORTS = { d: { width: 1366, height: 900 }, m: { width: 390, height: 844 } };
const TYPES = { chromium, firefox, webkit };

const note = (...a) => console.log(...a);
const failures = [];
const expect = (ok, what) => { note(ok ? '  ok  ' : '  FAIL', what); if (!ok) failures.push(what); };
mkdirSync('shots', { recursive: true });

const login = async (ctx) => {
  const a = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  const b = await ctx.request.post(base + '/cosmos/api/sudo', { data: { password } });
  return a.status() + '/' + b.status();
};
const plain = await request.newContext({ ignoreHTTPSErrors: true });
await plain.get(fake + '/dns?zone=example.info');
await plain.get(fake + '/dns?clear=_acme-challenge.example.info');

const noSideScroll = async (page, name, where) => {
  const over = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(over <= 1, `${name}: no sideways scroll on ${where} (${over}px over)`);
};
const dialogFits = async (page, name, where) => {
  const box = await page.locator('[role=dialog]').last().boundingBox();
  const width = page.viewportSize().width;
  expect(!!box && box.x >= 0 && box.x + box.width <= width + 1, `${name}: the dialog fits the screen (${where})`);
};
const shot = (page, name, what, full = true) => page.screenshot({ path: `shots/${name}-${what}.png`, fullPage: full });
const format = (date) => new Date(date).toLocaleString();

for (const engine of engines) {
  for (const size of sizes) {
    const name = `${engine}-${size}`;
    const browser = await TYPES[engine].launch();
    const ctx = await browser.newContext({ viewport: VIEWPORTS[size], ignoreHTTPSErrors: true });
    const page = await ctx.newPage();
    page.on('pageerror', (e) => failures.push(`${name} PAGEERROR: ${e.message.slice(0, 300)}`));
    note(`\n== ${name}: login`, await login(ctx));

    // --- the home page
    // compared with what the page itself was told (the test certificates
    // last four minutes: a second reading could already differ)
    const told = page.waitForResponse((r) => r.url().endsWith('/cosmos/api/certificates') && r.request().method() === 'GET', { timeout: 30000 });
    await page.goto(base + '/cosmos-ui/', { waitUntil: 'domcontentloaded' });
    const status = (await (await told).json()).data;
    const expected = homeAlerts(status, format);
    await page.waitForTimeout(1500);
    const shown = await page.locator('[data-certs-home]').count();
    expect(shown === expected.length, `${name}: the home page shows one alert per problem (${shown} shown, ${expected.length} expected: ${expected.map((a) => a.domain || 'ca').join(', ')})`);
    const home = await page.locator('body').innerText();
    expect(!/email address to use for Let's Encrypt/.test(home), `${name}: no claim that an email address is missing`);
    for (const alert of expected.filter((a) => a.domain)) {
      const text = await page.locator(`[data-certs-home="${alert.domain}"]`).first().innerText().catch(() => '');
      expect(text.includes(alert.domain) && /Open the certificates/.test(text), `${name}: the alert for ${alert.domain} names it and leads to the certificates`);
    }
    await noSideScroll(page, name, 'the home page');
    await shot(page, name, 'home', false);

    // --- the card
    const toldCard = page.waitForResponse((r) => r.url().endsWith('/cosmos/api/certificates') && r.request().method() === 'GET', { timeout: 30000 });
    await page.goto(base + '/cosmos-ui/config-general/https', { waitUntil: 'domcontentloaded' });
    const card = (await (await toldCard).json()).data;
    await page.locator('[data-certs-overview]').waitFor({ timeout: 30000 });
    await page.waitForTimeout(1500);
    const domains = await page.locator('[data-certs-domain]').count();
    expect(domains === card.certificates.length, `${name}: one entry per domain on the card (${domains} of ${card.certificates.length})`);
    for (const host of card.hosts) {
      const row = page.locator(`[data-certs-host="${host.host}"]`).first();
      const said = await row.getAttribute('data-certs-trusted').catch(() => null);
      expect(said === (host.trusted ? 'yes' : 'no'), `${name}: ${host.host} is shown ${host.trusted ? 'trusted' : 'not trusted'}, as the server says`);
    }
    const blocked = card.certificates.find((c) => c.blocked);
    if (blocked) {
      const text = await page.locator(`[data-certs-domain="${blocked.domain}"]`).innerText();
      expect(/Nothing can be ordered for this domain/.test(text), `${name}: ${blocked.domain} says why nothing can be ordered`);
    }
    await noSideScroll(page, name, 'the Domains tab');
    await page.locator('[data-certs-overview]').scrollIntoViewIfNeeded();
    await shot(page, name, 'card');

    // --- "Add another domain"
    await page.getByRole('button', { name: /Add another domain/ }).first().click();
    await page.locator('#certs-add-domain').fill('example.info');
    await page.locator('#certs-add-domain').press('Enter');
    await page.locator('[data-certs-add="way"]').waitFor({ timeout: 15000 });
    await page.waitForTimeout(500);
    const dialog = page.locator('[role=dialog]').last();
    const offered = await dialog.innerText();
    expect(/are not ones Cosmos recognises/.test(offered), `${name}: the dialog says where the DNS is`);
    expect(/One record at its DNS provider/.test(offered), `${name}: handing the proof to example.org is on offer`);
    await dialog.getByText('One record at its DNS provider').first().click();
    await page.waitForTimeout(300);
    const record = await dialog.locator('[data-certs-record]').first().getAttribute('data-certs-record').catch(() => '');
    expect(record === '_acme-challenge.example.info', `${name}: the record to create is shown (${record})`);
    await dialog.locator('[data-certs-check]').click();
    await dialog.locator('[data-certs-delegation]').waitFor({ timeout: 15000 });
    expect(await dialog.locator('[data-certs-delegation]').getAttribute('data-certs-delegation') === 'missing', `${name}: before the record exists the check says so`);
    expect(await dialog.locator('[data-certs-save]').isDisabled(), `${name}: "Add the domain" waits for the record`);
    await dialogFits(page, name, 'the delegation');
    await shot(page, name, 'add-delegate', false);

    await plain.get(fake + '/dns?name=_acme-challenge.example.info&type=CNAME&value=example-info._acme.example.org');
    await dialog.locator('[data-certs-check]').click();
    await page.waitForFunction(() => document.querySelector('[data-certs-delegation]')?.getAttribute('data-certs-delegation') === 'ok', null, { timeout: 15000 }).catch(() => {});
    expect(await dialog.locator('[data-certs-delegation]').getAttribute('data-certs-delegation') === 'ok', `${name}: once the record exists the check finds it`);
    expect(!(await dialog.locator('[data-certs-save]').isDisabled()), `${name}: then the domain can be added`);
    await plain.get(fake + '/dns?clear=_acme-challenge.example.info');

    await dialog.getByText('Give Cosmos access to its DNS').first().click();
    await page.locator('#certs-add-provider').waitFor({ timeout: 5000 }).catch(() => {});
    await dialogFits(page, name, 'DNS access');
    await shot(page, name, 'add-access', false);
    await dialog.getByRole('button', { name: /^Cancel$/ }).click();

    await browser.close();
  }
}

await plain.dispose();
note(failures.length === 0 ? '\nALL OK' : `\n${failures.length} FAILED:\n` + failures.join('\n'));
process.exit(failures.length === 0 ? 0 : 1);

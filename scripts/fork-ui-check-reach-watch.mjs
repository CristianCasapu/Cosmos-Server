// Fork addition: pictures and checks of the watch from the internet
// (src/reachwatch) in headless Chromium, Firefox and WebKit, against the
// throwaway instance of scripts/fork-reach-watch-e2e.sh (never the
// production one): the home page's notice area, the reachability of the
// panel and the watch's card (Configuration > HTTP), and the details of the
// newest notification of the watch.
//
//   BASE    the instance as the browser opens it (default https://example.org:2053;
//           run the browser container with --add-host example.org:192.168.1.10)
//   PHASE   a word for the file names (down, back, dns, cert, offline)
//   ENGINES chromium,firefox,webkit   SIZES d,m (1366x900, 393x851)
//   SHOTS   where the pictures go (default ./shots)
//
// It fails when a page has a script error or a refusal of the script
// policy, scrolls sideways, or when the server does not send the policy.
import { chromium, firefox, webkit } from 'playwright';
import { mkdirSync } from 'node:fs';

const base = process.env.BASE || 'https://example.org:2053';
const phase = process.env.PHASE || 'now';
const shots = process.env.SHOTS || 'shots';
const password = process.env.PASSWORD || 'Test-Passw0rd!x';
const engines = { chromium, firefox, webkit };
const only = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const sizes = { d: { width: 1366, height: 900 }, m: { width: 393, height: 851 } };
const wanted = (process.env.SIZES || 'd,m').split(',');
const failures = [];
const note = (...a) => console.log(...a);
const expect = (ok, what) => { note(ok ? '  ok  ' : '  FAIL', what); if (!ok) failures.push(what); };
mkdirSync(shots, { recursive: true });

const run = async (engine, size) => {
  const name = `${phase}-${engine}-${size}`;
  const browser = await engines[engine].launch();
  const ctx = await browser.newContext({ viewport: sizes[size], ignoreHTTPSErrors: true, serviceWorkers: 'block' });
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', (e) => errors.push('script error: ' + e.message.slice(0, 200)));
  page.on('console', (m) => { if (/Content.Security.Policy|Refused to (execute|load)/i.test(m.text())) errors.push('policy: ' + m.text().slice(0, 200)); });
  let r = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  expect(r.status() === 200, `${name}: login`);
  await ctx.request.post(base + '/cosmos/api/sudo', { data: { password } });

  // the home page: the notice area
  const response = await page.goto(base + '/cosmos-ui/', { waitUntil: 'domcontentloaded' });
  const policy = (response && response.headers()['content-security-policy']) || '';
  expect(/script-src 'self'/.test(policy), `${name}: the server sends the script policy`);
  await page.locator('.ct-notices').first().waitFor({ timeout: 30000 }).catch(() => {});
  await page.waitForTimeout(3000);
  const home = page.locator('[data-reach-watch-home]');
  note(name, 'home line:', (await home.count()) ? (await home.first().innerText()).replace(/\s+/g, ' ') : '(none)');
  await page.screenshot({ path: `${shots}/${name}-home.png` });
  const scroll = async (where) => {
    const w = await page.evaluate(() => [document.documentElement.scrollWidth, document.documentElement.clientWidth]);
    expect(w[0] <= w[1] + 1, `${name} ${where}: no sideways scroll (${w[0]} <= ${w[1]})`);
  };
  await scroll('home');

  // the HTTP settings: the panel's reachability and the watch's card
  await page.goto(base + '/cosmos-ui/config-general/http-server', { waitUntil: 'domcontentloaded' });
  const card = page.locator('[data-reach-watch-card]');
  await card.waitFor({ timeout: 30000 });
  await page.waitForTimeout(2500);
  const reach = page.locator('[data-reach-own-panel]');
  if (await reach.count()) {
    await reach.first().scrollIntoViewIfNeeded();
    await reach.first().screenshot({ path: `${shots}/${name}-reachability.png` });
    note(name, 'panel chip:', await page.locator('[data-reach]').first().getAttribute('data-reach'));
  }
  const cardBox = page.locator('.MuiCard-root', { has: card }).last();
  await cardBox.scrollIntoViewIfNeeded();
  await cardBox.screenshot({ path: `${shots}/${name}-card.png` });
  note(name, 'names:', JSON.stringify(await page.locator('[data-reach-watch-name]').evaluateAll((els) => els.map((e) => e.dataset.reachWatchName + '=' + e.dataset.reachWatchState))));
  await scroll('settings');

  // the newest notification of the watch
  const list = await (await ctx.request.get(base + '/cosmos/api/notifications/list?source=cloudflare&limit=10')).json();
  const item = ((list.data && list.data.items) || []).find((i) => String(i.kind).startsWith('reach.'));
  if (item) {
    await page.goto(base + '/cosmos-ui/notifications/' + item.id, { waitUntil: 'domcontentloaded' });
    await page.getByText(item.kind === 'reach.down' ? 'Cosmos cannot be reached from the internet' : 'Cosmos can be reached from the internet again').first().waitFor({ timeout: 30000 }).catch(() => {});
    await page.waitForTimeout(3000);
    await page.screenshot({ path: `${shots}/${name}-details.png`, fullPage: true });
    await scroll('details');
    note(name, 'details of', item.kind, item.id);
  } else {
    note(name, 'no notification of the watch yet');
  }
  expect(errors.length === 0, `${name}: no script error and no refusal of the policy ${errors.join(' | ')}`);
  await browser.close();
};

for (const engine of only) {
  for (const size of wanted) {
    try {
      await run(engine, size);
    } catch (e) {
      expect(false, `${phase}-${engine}-${size}: ${e.message.split('\n')[0]}`);
    }
  }
}
note(failures.length ? `${failures.length} failure(s)` : 'all passed');
process.exit(failures.length ? 1 : 0);

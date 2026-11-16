// Fork addition (plan 44): the owner's story with qBittorrent, in a browser.
// Chromium, Firefox and WebKit at 1366x900 and 393x851. A URL of the real
// qBittorrent image exists and does not open; ServApps says "Does not open";
// the dialog says why and what Cosmos tried; one confirmation tells qBittorrent
// its own address on both its URLs; the check says they open; then
// qBittorrent's own page: sign-in, a torrent added (a POST from the page),
// a reload that stays signed in; and the same on the domain name through a
// stand-in for Cloudflare in front (TLS ended there, Host kept with its port,
// X-Forwarded-For and CF-Connecting-IP added).
//
// Setup (never the owner's server): what `sh scripts/fork-behind-e2e.sh up`
// (or its inner script with APPS=qbittorrent) leaves — a docker:29-dind with
// the test Cosmos built from this tree (./static copied to /cosmos/static)
// on HTTPS 2053, hostname server.example.org, admin / PASSWORD, and the
// container "qbittorrent" (lscr.io/linuxserver/qbittorrent) with the URLs
// qbittorrent-local (qbittorrent.server.local) and qbittorrent-ext
// (qbittorrent.example.org). On an --internal network of their own: the
// Docker in Docker (COSMOS_IP), an nginx stand-in for the edge (EDGE_IP)
// listening with TLS on 2053 and proxying to COSMOS_IP:2053 with
// `proxy_set_header Host $http_host`, X-Forwarded-For and CF-Connecting-IP,
// and this browser container with --add-host server.example.org and
// qbittorrent.server.local at COSMOS_IP, qbittorrent.example.org at EDGE_IP.
// QBT_PASSWORD: the temporary password from `docker logs qbittorrent`.
//   ENGINES=chromium,firefox,webkit SIZES=d,m SHOTS=/out
import { chromium, firefox, webkit } from 'playwright';
import { mkdirSync } from 'node:fs';
import { createHash } from 'node:crypto';

const panel = process.env.BASE || 'https://server.example.org:2053';
const password = process.env.PASSWORD || 'Test-Passw0rd!x';
const qbtPassword = process.env.QBT_PASSWORD || '';
const engines = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const sizes = (process.env.SIZES || 'd,m').split(',');
const shots = process.env.SHOTS || '/out';
mkdirSync(shots, { recursive: true });
const viewports = { d: { width: 1366, height: 900 }, m: { width: 393, height: 851 } };
const types = { chromium, firefox, webkit };

const failures = [];
const expect = (ok, what) => { console.log(ok ? '  ok  ' : '  FAIL', what); if (!ok) failures.push(what); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// an API call from a panel page (the same-origin check refuses other callers' browser headers)
// (sent again when it met the moment the server reloads its URLs after a change)
const call = (page, path, body) => page.evaluate(async ([p, b]) => {
  for (let i = 0; ; i++) {
    try {
      const r = await fetch('/cosmos/api/' + p, { method: b ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json' }, body: b ? JSON.stringify(b) : undefined });
      return { status: r.status, json: await r.json().catch(() => null) };
    } catch (e) {
      if (i >= 4) throw e;
      await new Promise((r) => setTimeout(r, 1500));
    }
  }
}, [path, body]);

const noSideScroll = async (page, what) => {
  const wide = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(wide <= 1, `${what}: no sideways scroll (${wide}px)`);
};

for (const engine of engines) {
  for (const size of sizes) {
    const tag = `${engine}-${size}`;
    console.log(`== ${tag}`);
    const browser = await types[engine].launch();
    const context = await browser.newContext({ viewport: viewports[size], ignoreHTTPSErrors: true, serviceWorkers: 'block' });
    const page = await context.newPage();
    const errors = [];
    // (WebKit reports the app chips' own reachability probe, refused by the apps' CORS, as a page error: upstream's, not this page's)
    page.on('pageerror', (e) => { if (!String(e).includes('__cosmos_probe')) errors.push(String(e)); });

    // sign in to the panel, confirm the password
    await page.goto(panel + '/cosmos-ui/login');
    await page.fill('input[name=nickname]', 'admin');
    await page.fill('input[name=password]', password);
    await page.click('button[type=submit]');
    await page.waitForURL(/cosmos-ui\/(?!login)/, { timeout: 30000 });
    await page.waitForLoadState('networkidle').catch(() => {});
    await sleep(1500);
    await call(page, 'sudo', { password });

    // the story starts where the owner was: neither URL tells qBittorrent its own address
    for (const route of ['qbittorrent-local', 'qbittorrent-ext']) {
      await call(page, 'url-check/apply', { route, appOwnHost: false });
    }
    await sleep(3000);

    await page.goto(panel + '/cosmos-ui/servapps');
    const mark = page.locator('[data-url-problem]').first();
    await mark.waitFor({ timeout: 45000 }).catch(() => {});
    expect(await mark.isVisible(), 'ServApps says the URL does not open');
    expect((await mark.textContent() || '').includes('Does not open'), 'in those words');
    await mark.scrollIntoViewIfNeeded().catch(() => {});
    await noSideScroll(page, 'ServApps');
    await page.screenshot({ path: `${shots}/${tag}-1-servapps.png` });

    await mark.click();
    const dialog = page.locator('[role=dialog]:visible');
    await dialog.locator('[data-url-check-route-repair]').waitFor({ timeout: 30000 });
    const said = await dialog.textContent();
    expect(/qBittorrent/.test(said), 'the dialog names qBittorrent');
    expect(/Unauthorized/.test(said), 'and shows what qBittorrent said');
    expect(await dialog.locator('[data-url-check-seen]').isVisible(), 'and that Cosmos saw the repair work');
    await noSideScroll(page, 'the dialog');
    await page.screenshot({ path: `${shots}/${tag}-2-why.png` });

    await dialog.locator('[data-url-check-apply-route]').click();
    await dialog.locator('[data-url-check-fixed]').nth(1).waitFor({ timeout: 45000 }).catch(() => {});
    expect(await dialog.locator('[data-url-check-fixed]').count() === 2, 'one confirmation: both addresses open now');
    await page.screenshot({ path: `${shots}/${tag}-3-fixed.png` });
    await dialog.getByRole('button', { name: 'Close' }).click();

    // qBittorrent itself, at home and through the edge stand-in
    for (const host of ['qbittorrent.server.local', 'qbittorrent.example.org']) {
      const app = await context.newPage();
      app.on('pageerror', (e) => errors.push(host + ': ' + String(e)));
      const first = await app.goto(`https://${host}:2053/`);
      expect(first && first.status() === 200, `${host}: the page opens (${first && first.status()})`);
      await app.fill('#username', 'admin');
      await app.fill('#password', qbtPassword);
      await app.click('#loginButton');
      await app.waitForSelector('#loginform', { state: 'detached', timeout: 30000 }).catch(() => {});
      expect(!(await app.locator('#loginform').count()), `${host}: signed in`);
      const hash = createHash('sha1').update(`${tag}-${host}-${Date.now()}`).digest('hex');
      const added = await app.evaluate(async ([h, dn]) => {
        const body = new FormData();
        body.append('urls', `magnet:?xt=urn:btih:${h}&dn=${dn}`);
        return (await fetch('/api/v2/torrents/add', { method: 'POST', body })).status;
      }, [hash, `story-${tag}`]);
      expect(added === 200, `${host}: a torrent is added from the page (POST ${added})`);
      await app.reload();
      await sleep(3000);
      const still = await app.evaluate(async () => (await fetch('/api/v2/sync/maindata?rid=0')).text());
      expect(!(await app.locator('#loginform').count()) && still.includes(hash), `${host}: after a reload still signed in, the torrent listed`);
      await noSideScroll(app, host);
      await app.screenshot({ path: `${shots}/${tag}-4-${host.split('.')[1]}.png` });
      await app.close();
    }

    expect(errors.length === 0, 'no script errors' + (errors.length ? ': ' + errors.slice(0, 3).join(' | ') : ''));
    await browser.close();
  }
}
console.log(failures.length ? `${failures.length} failed` : 'all passed');
process.exit(failures.length ? 1 : 0);

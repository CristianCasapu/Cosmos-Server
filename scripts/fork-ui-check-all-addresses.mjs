// Fork addition: "give every app without a URL its local address" on the ServApps
// page (components/localAddressesForAll.jsx). Deletes every URL of the test instance
// first. Needs the domain and .local names of fork-ui-check-url-form.mjs (PREP=1).
// ENGINES=chromium,firefox,webkit  SIZES=d,m  TAG=<name>
// The test instance must be connected to a Docker of its own (see CLAUDE.md,
// "Checking the UI in a browser"), reachable under a name with a dot (WebKit keeps
// no session cookie otherwise), with HTTPS switched off, and in that Docker, as the
// expectations below assume (nginx:alpine tagged with the images' names is enough):
//   radarr       image linuxserver/radarr       --expose 7878 -p 7878:80
//   sonarr       image linuxserver/sonarr       --expose 8989 -p 8989:80
//   jellyfin     image jellyfin/jellyfin        --expose 8096 -p 8096:80 -p 8920:443
//   qbittorrent  image linuxserver/qbittorrent  --expose 8080 -p 8085:8080
//   web1         image nginx:alpine             -p 8081:80
//   db1          image busybox (no port)
//   media-app    image nginx:alpine --expose 3000, labels cosmos-stack=media, cosmos-stack-main=media-app
//   media-db     image busybox, label cosmos-stack=media
// BASE=http://<name>:<port> points it at the instance (default http://forms2.test:18080).
// Screenshots go to ./shots (create it). The run ends with "ALL OK" or the list of failures.
import { chromium, firefox, webkit } from 'playwright';
const base = process.env.BASE || 'http://forms2.test:18080';
const tag = process.env.TAG || 'after';
const engines = { chromium, firefox, webkit };
const names = (process.env.ENGINES || 'chromium').split(',');
const sizes = { d: { width: 1366, height: 900 }, m: { width: 390, height: 844 } };
const wanted = (process.env.SIZES || 'd').split(',');
const note = (...a) => console.log(...a);
const failures = [];
const expect = (what, ok, detail) => { note(ok ? '  ok  ' : '  FAIL', what, detail === undefined ? '' : '| ' + String(detail).slice(0, 260)); if (!ok) failures.push(what); };
const routes = async (ctx) => ((await (await ctx.request.get(base + '/cosmos/api/config')).json()).data.HTTPConfig.ProxyConfig.Routes || []);
const dropRoute = async (ctx, name) => { await ctx.request.patch(base + '/cosmos/api/config', { data: { routeName: name, operation: 'delete' } }); await new Promise((s) => setTimeout(s, 2500)); };

for (const name of names) for (const size of wanted) {
  const id = name + '-' + size;
  const browser = await engines[name].launch();
  const ctx = await browser.newContext({ viewport: sizes[size] });
  await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: 'Test-Passw0rd!x' } });
  await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: 'Test-Passw0rd!x' } });
  for (const route of await routes(ctx)) await dropRoute(ctx, route.Name);
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message.slice(0, 160)));
  note(`--- ${id}: an address for every app`);
  await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(4500);
  let text = await page.locator('body').innerText();
  expect(id + ': the page says how many apps have no address', /6 apps have no address yet: /.test(text), (text.match(/\d+ apps? ha[sv]e? no address yet[^\n]*/) || ['none'])[0]);
  expect(id + ': the notice fits', await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth) <= 0);
  await page.screenshot({ path: `shots/${tag}-all-notice-${id}.png` });
  await page.getByRole('button', { name: 'Give them a local address' }).click();
  await page.waitForTimeout(800);
  const dialog = page.locator('[role=dialog]').last();
  text = await dialog.innerText();
  for (const [app, port, why] of [['radarr', 7878, 'usual web port of radarr'], ['sonarr', 8989, 'usual web port of sonarr'], ['jellyfin', 8096, 'usual web port of jellyfin'], ['qbittorrent', 8080, 'usual web port of qbittorrent'], ['web1', 80, 'only port this app has'], ['media-app', 80, 'most likely web port']]) {
    expect(`${id}: ${app} → ${app}.local, port ${port}, with the reason`, new RegExp(`${app}\\s*→\\s*${app}\\.local`).test(text) && text.includes(`${port} is the ${why}`), '');
  }
  expect(id + ': apps without a port are left out, with the reason', /Left out:/.test(text) && /db1: has no port to point at/.test(text) && /media-db: has no port to point at/.test(text));
  expect(id + ': it says the addresses are local only', /nothing becomes reachable from the internet/.test(text));
  expect(id + ': and that some apps need the name allowed', /Invalid Hostname/.test(text));
  expect(id + ': all six are ticked', await dialog.locator('input[type=checkbox]:checked').count() === 6, await dialog.locator('input[type=checkbox]:checked').count());
  expect(id + ': the dialog is not cut off', await dialog.evaluate((el) => { const c = el.querySelector('.MuiDialogContent-root'); return c.scrollWidth - c.clientWidth; }) <= 0);
  await page.screenshot({ path: `shots/${tag}-all-dialog-${id}.png` });
  // a click beside it keeps it; two apps are left out by hand
  await page.mouse.click(3, 3);
  await page.waitForTimeout(400);
  expect(id + ': a click beside the list keeps it open', await dialog.isVisible());
  await dialog.locator('label', { hasText: 'web1' }).locator('input[type=checkbox]').uncheck();
  await dialog.locator('label', { hasText: 'media-app' }).locator('input[type=checkbox]').uncheck();
  const go = dialog.getByRole('button', { name: 'Create 4 addresses' });
  expect(id + ': the button says how many', await go.count() === 1);
  await go.click();
  await dialog.getByRole('button', { name: 'Close' }).waitFor({ timeout: 90000 }).catch(() => {});
  await page.waitForTimeout(1500);
  const made = await routes(ctx);
  const byName = Object.fromEntries(made.map((r) => [r.Name, r]));
  expect(id + ': the four ticked addresses exist, the others do not', made.length === 4 && ['radarr', 'sonarr', 'jellyfin', 'qbittorrent'].every((n) => byName[n]), made.map((r) => r.Name).join(','));
  expect(id + ': each leads to its app\'s port', byName.radarr && byName.radarr.Target === 'http://radarr:7878' && byName.sonarr.Target === 'http://sonarr:8989' && byName.jellyfin.Target === 'http://jellyfin:8096' && byName.qbittorrent.Target === 'http://qbittorrent:8080', made.map((r) => r.Target).join(','));
  expect(id + ': at its local address, as the dialog would make it', byName.radarr && byName.radarr.Host === 'radarr.local' && byName.radarr.UseHost && byName.radarr.Mode === 'SERVAPP' && byName.radarr.SmartShield.Enabled && byName.radarr.Timeout === 14400000 && byName.radarr.Description === 'radarr, on the local network' && !byName.radarr.AuthEnabled);
  text = await dialog.innerText();
  expect(id + ': the list shows what was done', await dialog.locator('[aria-label=Created]').count() === 4, await dialog.locator('[aria-label=Created]').count());
  await page.screenshot({ path: `shots/${tag}-all-done-${id}.png` });
  await dialog.getByRole('button', { name: 'Close' }).click();
  await page.waitForTimeout(4000);
  text = await page.locator('body').innerText();
  expect(id + ': the page now counts the two that are left', /2 apps have no address yet: web1, media-app\./.test(text) || /2 apps have no address yet: media-app, web1\./.test(text), (text.match(/\d+ apps? ha[sv]e? no address yet[^\n]*/) || ['none'])[0]);
  expect(id + ': the cards show the new addresses', /radarr\.local/.test(text) && /sonarr\.local/.test(text));
  expect(id + ': no script errors', errors.length === 0, [...new Set(errors)].join(' | '));
  for (const route of await routes(ctx)) await dropRoute(ctx, route.Name);
  await browser.close();
}
note(failures.length ? 'FAILURES: ' + failures.length + '\n' + failures.join('\n') : 'ALL OK');

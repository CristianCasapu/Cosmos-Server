// Fork addition: the chips of published ports on the ServApps page: where each
// leads (plain HTTP, or HTTPS for a container port that speaks it), in three engines
// and at phone width.
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
const note = (...a) => console.log(...a);
const failures = [];
const expect = (what, ok, detail) => { note(ok ? '  ok  ' : '  FAIL', what, detail === undefined ? '' : '| ' + detail); if (!ok) failures.push(what); };
for (const [name, type, viewport] of [['chromium', chromium, { width: 1366, height: 900 }], ['firefox', firefox, { width: 1366, height: 900 }], ['webkit', webkit, { width: 1366, height: 900 }], ['chromium-phone', chromium, { width: 390, height: 844 }], ['webkit-phone', webkit, { width: 390, height: 844 }]]) {
  const browser = await type.launch();
  const ctx = await browser.newContext({ viewport });
  let r = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: 'Test-Passw0rd!x' } });
  await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: 'Test-Passw0rd!x' } });
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message.slice(0, 160)));
  await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(4500);
  const chips = await page.locator('a.MuiChip-root[href*="://"][target=_blank]').evaluateAll((els) => els.map((el) => [el.innerText.trim(), el.getAttribute('href'), el.getAttribute('rel')]));
  const byLabel = Object.fromEntries(chips.map((c) => [c[0], c[1]]));
  note(name, 'chips:', JSON.stringify(byLabel));
  expect(name + ': radarr 7878 opens plain HTTP', byLabel['7878:80'] === 'http://forms2.test:7878', byLabel['7878:80']);
  expect(name + ': jellyfin 8920:443 opens HTTPS', byLabel['8920:443'] === 'https://forms2.test:8920', byLabel['8920:443']);
  expect(name + ': every chip opens without access to the panel window', chips.length >= 5 && chips.every((c) => c[2] === 'noopener noreferrer'), chips.length + ' chips');
  if (viewport.width > 500) {
    await page.locator('a.MuiChip-root', { hasText: '7878:80' }).first().hover();
    await page.waitForTimeout(900);
    const tip = await page.locator('[role=tooltip]').last().innerText().catch(() => 'NONE');
    expect(name + ': the tooltip says what the chip opens', /Opens http:\/\/forms2\.test:7878 — plain HTTP/.test(tip), tip.slice(0, 90));
  }
  const over = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(name + ': no sideways scroll', over <= 0, over);
  expect(name + ': no script errors', errors.length === 0, errors.join(' | '));
  await page.screenshot({ path: `shots/${tag}-chips-${name}.png` });
  await browser.close();
}
note(failures.length ? 'FAILURES: ' + failures.length : 'ALL OK');

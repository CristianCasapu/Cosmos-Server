// Fork addition: counts the API requests a page load sends (and how many of them
// ask GET /api/me: 46 of them before utils/sessionCheck.js, 1 after), and shows what
// a page looks like when the server throttles it (429): words, not a blank page.
// ENGINES=chromium,firefox,webkit  TAG=<name for the screenshots>
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
const tag = process.env.TAG || 'before';
const engines = { chromium, firefox, webkit };
const names = (process.env.ENGINES || 'chromium').split(',');
const note = (...a) => console.log(...a);
const pages = ['/cosmos-ui/', '/cosmos-ui/servapps', '/cosmos-ui/config-general/general', '/cosmos-ui/config-url', '/cosmos-ui/config-users', '/cosmos-ui/storage', '/cosmos-ui/cron'];

for (const name of names) {
  const browser = await engines[name].launch();
  const ctx = await browser.newContext({ viewport: { width: 1366, height: 900 } });
  await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: 'Test-Passw0rd!x' } });
  await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: 'Test-Passw0rd!x' } });
  const page = await ctx.newPage();
  let counts = {};
  page.on('request', (req) => {
    const u = new URL(req.url());
    if (!u.pathname.startsWith('/cosmos/api/')) return;
    const key = u.pathname.replace('/cosmos/api/', '').split('/')[0];
    counts[key] = (counts[key] || 0) + 1;
  });
  let totalMe = 0, totalAll = 0;
  for (const path of pages) {
    counts = {};
    await page.goto(base + path, { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(4000);
    const all = Object.values(counts).reduce((a, b) => a + b, 0);
    totalMe += counts.me || 0; totalAll += all;
    note(name, path.padEnd(36), 'api requests', String(all).padStart(3), '| me', String(counts.me || 0).padStart(2), '| others', JSON.stringify(Object.fromEntries(Object.entries(counts).filter(([k]) => k !== 'me'))));
  }
  note(name, 'TOTAL over', pages.length, 'page loads:', totalAll, 'requests,', totalMe, 'of them /api/me');

  // --- throttled: every API call answers what the server's limiter answers
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message.slice(0, 120)));
  const throttle = (route) => route.fulfill({ status: 429, contentType: 'text/plain; charset=utf-8', body: JSON.stringify({ status: 'error', message: 'Too many requests', code: 'HTTP003' }) + '\n' });
  for (const [label, pattern] of [['all', '**/cosmos/api/**'], ['all-but-status-and-me', /\/cosmos\/api\/(?!status|me)/]]) {
    await page.route(pattern, throttle);
    for (const path of ['/cosmos-ui/servapps', '/cosmos-ui/config-general/general', '/cosmos-ui/']) {
      errors.length = 0;
      await page.goto(base + path, { waitUntil: 'domcontentloaded' });
      await page.waitForTimeout(5000);
      const text = (await page.locator('body').innerText()).replace(/\s+/g, ' ').trim();
      const file = `shots/${tag}-429-${label}-${name}${path.replace(/\W+/g, '_')}.png`;
      await page.screenshot({ path: file });
      note(name, '429', label, path, '| text', text.length, 'chars:', JSON.stringify(text.slice(0, 220)), '| script errors:', JSON.stringify([...new Set(errors)]));
    }
    await page.unroute(pattern, throttle);
  }
  await browser.close();
}

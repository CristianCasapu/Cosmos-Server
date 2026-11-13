// Fork addition: half of all API calls of a page load are answered 429, three rounds
// over ten pages: no page may end in a script error or come up empty.
// ENGINE=chromium|firefox|webkit
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
const engines = { chromium, firefox, webkit };
const name = process.env.ENGINE || 'firefox';
const note = (...a) => console.log(...a);
const pages = ['/cosmos-ui/', '/cosmos-ui/servapps', '/cosmos-ui/config-general/general', '/cosmos-ui/config-url', '/cosmos-ui/config-users', '/cosmos-ui/storage', '/cosmos-ui/cron', '/cosmos-ui/servapps/containers/radarr', '/cosmos-ui/monitoring', '/cosmos-ui/market-listing/'];
const browser = await engines[name].launch();
const ctx = await browser.newContext({ viewport: { width: 1366, height: 900 } });
await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: 'Test-Passw0rd!x' } });
await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: 'Test-Passw0rd!x' } });
const page = await ctx.newPage();
const errors = [];
let refused = [];
page.on('pageerror', (e) => errors.push(e.message.slice(0, 140) + ' @ ' + String(e.stack || '').split('\n').slice(0, 2).join(' < ').slice(0, 200)));
let seed = 7;
const rnd = () => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed / 2147483648; };
await page.route(/\/cosmos\/api\//, (route) => {
  if (rnd() < 0.5) {
    refused.push(new URL(route.request().url()).pathname.replace('/cosmos/api/', ''));
    return route.fulfill({ status: 429, contentType: 'text/plain; charset=utf-8', body: JSON.stringify({ status: 'error', message: 'Too many requests', code: 'HTTP003' }) + '\n' });
  }
  return route.continue();
});
const found = {};
for (let round = 0; round < 3; round++) {
  for (const path of pages) {
    errors.length = 0; refused = [];
    await page.goto(base + path, { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(3500);
    const text = (await page.locator('body').innerText()).replace(/\s+/g, ' ').trim();
    const real = [...new Set(errors)].filter((e) => !e.startsWith('Too many requests'));
    if (real.length || text.length < 40) {
      note(name, path, 'text', text.length, '| refused:', [...new Set(refused.filter((r) => r !== 'me'))].join(',').slice(0, 120), '| errors:', JSON.stringify(real).slice(0, 600));
      real.forEach((e) => { found[path + ' :: ' + e.split(' @ ')[0]] = 1; });
    }
  }
}
note('distinct:', JSON.stringify(Object.keys(found), null, 1));
await browser.close();

// Fork addition: the URL step of the new-container wizard with the simple view of
// the URL form, for a container that does not exist yet (Chromium desktop, WebKit phone).
// Prints what the step proposes; needs nginx:alpine in the test Docker.
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
import { chromium, webkit } from 'playwright';
const base = process.env.BASE || 'http://forms2.test:18080';
const note = (...a) => console.log(...a);
for (const [name, type, viewport] of [['chromium-d', chromium, { width: 1366, height: 900 }], ['webkit-m', webkit, { width: 390, height: 844 }]]) {
  const browser = await type.launch();
  const ctx = await browser.newContext({ viewport });
  await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: 'Test-Passw0rd!x' } });
  await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: 'Test-Passw0rd!x' } });
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message.slice(0, 160)));
  await page.goto(base + '/cosmos-ui/servapps/new-service', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(3500);
  await page.locator('#name').fill('forms-check');
  await page.locator('#image').fill('nginx:alpine');
  await page.locator('#user').click();
  await page.waitForTimeout(1500);
  const offer = page.getByRole('button', { name: /Use the image's defaults/ });
  note(name, 'image defaults offered:', await offer.count());
  if (await offer.count()) { await offer.click(); await page.waitForTimeout(600); }
  await page.getByRole('button', { name: /Next/ }).first().click();
  await page.waitForTimeout(1000);
  await page.locator('input[type=checkbox]').first().check();
  await page.waitForTimeout(2000);
  const form = page.locator('form').last();
  const text = await form.innerText();
  note(name, 'address:', await page.locator('#Host').inputValue().catch(() => '?'),
    '| port picked:', (await page.locator('#Target .MuiChip-root[aria-checked=true]').allInnerTexts()).map((x) => x.trim()).join(',') || 'none',
    '| port chips:', (await page.locator('#Target .MuiChip-root').allInnerTexts()).map((x) => x.trim()).join(','),
    '| note:', (text.match(/Proposed:[^\n]*|The app declares no port[^\n]*/) || ['none'])[0],
    '| saved as:', (text.match(/Saved as "[^"]*"/) || ['?'])[0],
    '| host hint:', /receives its requests under the name/.test(text),
    '| sideways:', await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth));
  await page.screenshot({ path: `shots/final-wizard-url-${name}.png`, fullPage: true });
  // through to the review: the route is in what will be created
  for (let i = 0; i < 3; i++) { await page.getByRole('button', { name: /Next/ }).first().click(); await page.waitForTimeout(1200); }
  const review = await page.locator('body').innerText();
  note(name, 'review mentions the route target:', /http:\/\/forms-check:80/.test(review), '| and its address:', /forms-check\.local/.test(review));
  note(name, 'script errors:', JSON.stringify([...new Set(errors)]));
  await browser.close();
}

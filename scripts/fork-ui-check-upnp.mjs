// Fork addition: checks the router port forwarding for apps in headless
// Chromium, Firefox and WebKit, at 1366x900 and 390x844, against a throwaway
// Cosmos instance, never the production one (CLAUDE.md, "Checking the UI in a
// browser").
//
// What it needs, all on one Docker network of the test's own:
// - scripts/fork-fake-igd.go running at IGD (its control port, default
//   http://10.213.79.2:8080): the stand-in for the router;
// - the throwaway Cosmos (BASE, a name with a dot for WebKit), started with
//   --cap-add NET_ADMIN and "ip route replace default via <the stand-in>",
//   HTTPS disabled in the wizard, DOCKER_HOST pointing at a docker:27-dind;
// - in that Docker: "jellyfin" publishing 8096/tcp and 7359/udp, "torrent"
//   publishing 51413/tcp, 51413/udp and 127.0.0.1:9091 (busybox will do),
//   and the image IMAGE present (default busybox:1.36) for the install step.
//
// The router is given the owner's situation: 80 and 443 forwarded by hand
// (not shown over UPnP, refused), port 8096 held by another device, port
// 6881 by another program on the server. Screenshots go to ./shots.
import { chromium, firefox, webkit } from 'playwright';

const base = process.env.BASE || 'http://cosmos-upnpapps-smoke.test:18080';
const igd = process.env.IGD || 'http://10.213.79.2:8080';
const image = process.env.IMAGE || 'busybox:1.36';
const password = process.env.PASSWORD || 'Test-Passw0rd!x';
const only = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const errors = [];
const failures = [];
const note = (...a) => console.log(...a);
const expect = (ok, what) => { note(ok ? '  ok  ' : '  FAIL', what); if (!ok) failures.push(what); };
const router = async (path) => (await fetch(igd + path)).json();
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const open = async (type, name, viewport) => {
  const browser = await type.launch();
  const ctx = await browser.newContext({ viewport, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => errors.push(`${name} PAGEERROR: ${e.message.slice(0, 300)}`));
  let r = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  const login = r.status();
  r = await ctx.request.post(base + '/cosmos/api/sudo', { data: { password } });
  note(name, 'login', login, 'sudo', r.status());
  return { browser, ctx, page };
};

const api = (ctx) => ({
  get: async (path) => (await (await ctx.request.get(base + path)).json()).data,
  post: async (path, data) => { const r = await ctx.request.post(base + path, { data }); return { status: r.status(), body: await r.json().catch(() => ({})) }; },
});

// wait until the server answers again after a change that restarts its listener
const serverBack = async (ctx) => {
  for (let i = 0; i < 40; i++) {
    await sleep(750);
    try { if ((await ctx.request.get(base + '/cosmos/api/status')).status() < 500) return; } catch (e) { /* restarting */ }
  }
};

const noSidewaysScroll = async (page, name) => {
  const widths = await page.evaluate(() => ({ scroll: document.documentElement.scrollWidth, client: document.documentElement.clientWidth }));
  expect(widths.scroll <= widths.client + 1, `${name}: no sideways scroll (${widths.scroll} <= ${widths.client})`);
};

const goto = async (page, path, wait) => {
  await page.goto(base + path, { waitUntil: 'domcontentloaded' });
  await wait.waitFor({ timeout: 30000 });
  await page.waitForTimeout(2500);
};

// ---------------------------------------------------------------------------
// the situation: settings, a URL for jellyfin, the router's table
const prepare = async () => {
  const { browser, ctx } = await open(chromium, 'prepare', { width: 1366, height: 900 });
  const a = api(ctx);
  let config = await a.get('/cosmos/api/config');
  if (!(config.HTTPConfig.DNSZones || []).some((zone) => zone.Zone === 'example.org')) {
    const r = await ctx.request.put(base + '/cosmos/api/zones/example.org', { data: { Zone: 'example.org', HTTPSCertificateMode: 'DISABLED' } });
    note('domain example.org:', r.status());
    await serverBack(ctx); await sleep(3000);
  }
  for (const m of (await a.get('/cosmos/api/upnp')).apps || []) {
    await a.post('/cosmos/api/upnp/apps', { container: m.container, port: m.containerPort, protocol: m.protocol.toLowerCase(), enabled: false });
  }
  await router('/reboot');
  await router('/hide?key=TCP/80&code=718');
  await router('/hide?key=TCP/443&code=718');
  await router('/put?key=TCP/8096&client=10.213.79.99&port=8096&desc=Media%20box');
  await router('/put?key=TCP/6881&client=10.213.79.10&port=6881&desc=qBittorrent');
  await router('/put?key=TCP/8880&client=10.213.79.98&port=80&desc=' + encodeURIComponent('<b>NAS</b> web'));

  config = await a.get('/cosmos/api/config');
  if (!(config.HTTPConfig.ProxyConfig.Routes || []).some((route) => route.Name === 'jellyfin')) {
    const r = await ctx.request.patch(base + '/cosmos/api/config', { data: { routeName: '', operation: 'add', newRoute: {
      Name: 'jellyfin', Description: 'test', UseHost: true, Host: 'jellyfin.example.org', Mode: 'SERVAPP', Target: 'http://jellyfin:8096',
      Timeout: 14400000, ThrottlePerMinute: 10000, SmartShield: { Enabled: true },
    } } });
    note('URL jellyfin.example.org:', r.status());
    await serverBack(ctx); await sleep(3000);
  }
  // Cosmos' own HTTP port, asked for on 8880 (which the NAS holds): refused, with a proposal
  config = await a.get('/cosmos/api/config');
  config.HTTPConfig.UPnP = { ...config.HTTPConfig.UPnP, Enabled: true, ForwardHTTP: true, ExternalHTTPPort: '8880', ReservedPorts: '' };
  const r = await ctx.request.put(base + '/cosmos/api/config', { data: config });
  note('settings (forward the HTTP port on 8880):', r.status());
  await serverBack(ctx); await sleep(5000);
  const st = await a.get('/cosmos/api/upnp');
  note('status:', JSON.stringify({ own: st.mappings.map((m) => [m.name, m.state, m.code, m.suggested]), external: st.externalHTTPPort, foreign: st.foreign.length }));
  await browser.close();
};

// ---------------------------------------------------------------------------
// the whole story, once (Chromium, desktop)
const story = async () => {
  const name = 'chromium-desktop';
  const { browser, ctx, page } = await open(chromium, name, { width: 1366, height: 900 });
  const a = api(ctx);

  // --- Internet access: Cosmos' own port refused, never moved by itself, a proposal to take
  await goto(page, '/cosmos-ui/config-general/http-server', page.locator('[data-router-overview]'));
  let card = await page.locator('[data-router-overview]').innerText();
  expect(/Cosmos HTTP\s+not forwarded/.test(card) && /Port 8880 is used by 10\.213\.79\.98:80/.test(card), 'Internet access: the HTTP port is refused and says who holds 8880');
  expect(/another device on your network/.test(card), 'Internet access: "another device on your network"');
  const suggest = page.locator('[data-router-suggest]');
  const proposal = (await suggest.innerText().catch(() => '')).trim();
  expect(/^Use port (8080|2052|2082|2086|2095)$/.test(proposal), `Internet access: a port is proposed, never 80 or 443 ("${proposal}")`);
  expect((await page.locator('[data-router-foreign]').count()) === 3, 'Internet access: the router\'s three other mappings are listed');
  const foreign = (await page.locator('[data-router-foreign]').allInnerTexts()).join(' | ');
  expect(/opened by another program here/.test(foreign) && /qBittorrent/.test(foreign), 'Internet access: a mapping of another program on this server is told apart');
  expect(foreign.includes('<b>NAS</b> web') && (await page.locator('[data-router-foreign] b').count()) === 0, 'Internet access: a description from the router is shown as text');
  expect(/Forwards entered in the router's own pages are often not in it/.test(card), 'Internet access: says what Cosmos cannot see');
  expect(await page.locator('input[name=UPnP_ReservedPorts]').getAttribute('placeholder') === '25, 80, 110, 143, 443, 465, 587, 993, 995', 'Internet access: the reserved ports are proposed');
  await page.locator('[data-router-overview]').scrollIntoViewIfNeeded();
  await page.screenshot({ path: `shots/${name}-internet-refused.png`, fullPage: true });
  await suggest.click();
  await page.waitForTimeout(600);
  const taken = await page.locator('input[name=UPnP_ExternalHTTPPort]').inputValue();
  expect(proposal.endsWith(taken) && taken !== '', `Internet access: the proposal lands in the form (${taken})`);
  await page.getByRole('button', { name: /^Save$/ }).first().click();
  await serverBack(ctx); await sleep(6000);
  let st = await a.get('/cosmos/api/upnp');
  expect(st.mappings.some((m) => m.name === 'HTTP' && m.active && String(m.externalPort) === taken) && String(st.externalHTTPPort) === taken, `after Save the router forwards ${taken} to Cosmos' HTTP port`);

  // --- the container's page: a web port
  await goto(page, '/cosmos-ui/servapps/containers/jellyfin', page.locator('[data-router-port="jellyfin:8096/tcp"]'));
  const line = page.locator('[data-router-port="jellyfin:8096/tcp"]');
  expect(/only if your router forwards it/.test(await line.innerText()), 'container page: an unopened port says "if your router forwards it"');
  expect(!(await line.locator('input[type=checkbox]').isChecked()), 'container page: the switch is off by default');
  // the app's own URL carries the external port of Cosmos (HTTP here, since HTTPS is off on the test instance)
  const urls = (await page.locator('.MuiChip-root', { hasText: 'jellyfin.example.org' }).allInnerTexts()).join(' ');
  expect(urls.includes('jellyfin.example.org:' + taken), `container page: the app's address shows the external port (${urls.replace(/\s+/g, ' ').trim()})`);
  await line.locator('input[type=checkbox]').click();
  const dialog = page.locator('[data-router-dialog]');
  await dialog.waitFor();
  let text = await dialog.innerText();
  expect(/Open port 8096 \(TCP\) of jellyfin on the router\?/.test(text), 'dialog: names the port and the app');
  expect(/without Cosmos' login, without SmartShield, without the HTTPS Cosmos provides, and Fail2ban does not see/.test(text), 'dialog: says what opening means');
  expect(/already has an address through Cosmos: jellyfin\.example\.org/.test(text) && (await dialog.locator('[data-router-web]').count()) === 1, 'dialog: recommends the app\'s URL for a web interface');
  expect(/Proposed from the app's URL jellyfin/.test(text), 'dialog: "what is on this port" is proposed from the URL');
  expect((await dialog.locator('[data-router-confirm]').innerText()).trim() === 'Open port 8096 of jellyfin', 'dialog: the button names the port and the app');
  await page.screenshot({ path: `shots/${name}-dialog-web.png` });
  // a reserved port is refused in the form
  const external = dialog.locator('input[inputmode=numeric]');
  await external.fill('443');
  expect(/never asks the router for port 443/.test(await dialog.innerText()) && await dialog.locator('[data-router-confirm]').isDisabled(), 'dialog: port 443 cannot be asked for');
  await external.fill('');
  await dialog.locator('[data-router-confirm]').click();
  await dialog.waitFor({ state: 'detached', timeout: 20000 });
  await page.waitForTimeout(1500);
  text = await line.innerText();
  expect(/The router forwards port 8097 to port 8096 of this server/.test(text) && /Not tested from outside/.test(text), 'container page: "the router forwards port 8097", not tested from outside');
  expect(/8097 on the router, because 8096 is used by 10\.213\.79\.99:8096 \(Media box\)/.test(await line.locator('[data-router-note]').innerText()), 'container page: the note says why the port differs and who holds the wanted one');
  const href = await line.locator('[data-router-address] a').getAttribute('href');
  expect(href === 'http://jellyfin.example.org:8097', `container page: a web port is a link with the real external port (${href})`);
  expect(await line.locator('input[type=checkbox]').isChecked(), 'container page: the switch is on');
  await line.scrollIntoViewIfNeeded();
  await page.screenshot({ path: `shots/${name}-container-open.png`, fullPage: true });
  let table = await router('/table');
  expect(table['TCP/8096'].client === '10.213.79.99' && table['TCP/8097'] && table['TCP/8097'].port === '8096', 'router: the other device keeps 8096, Cosmos has 8097');

  // --- a port that is not a web page
  await goto(page, '/cosmos-ui/servapps/containers/torrent', page.locator('[data-router-port="torrent:51413/udp"]'));
  expect((await page.locator('[data-router-port="torrent:9091/tcp"]').count()) === 0, 'container page: a port published for the server only has no switch');
  const peer = page.locator('[data-router-port="torrent:51413/udp"]');
  await peer.locator('input[type=checkbox]').click();
  await dialog.waitFor();
  text = await dialog.innerText();
  expect((await dialog.locator('[data-router-web]').count()) === 0 && /peer ports, game servers, media clients/.test(text), 'dialog: a peer port gets no web hint, but what opening is for');
  await page.screenshot({ path: `shots/${name}-dialog-peer.png` });
  await dialog.locator('[data-router-confirm]').click();
  await dialog.waitFor({ state: 'detached', timeout: 20000 });
  await page.waitForTimeout(1500);
  // (the test instance's own name has a dot, so it counts as the panel's external name; with an IP address as hostname the router's public address is shown)
  const host = new URL(base).hostname;
  expect((await peer.locator('[data-router-address] a').count()) === 0 && (await peer.locator('[data-router-address]').innerText()).includes(host + ':51413'), 'container page: a peer port is an address to copy, not a link');
  expect((await peer.locator('[data-router-note]').count()) === 0, 'container page: no note when the port is the wanted one');

  // --- the cards
  await goto(page, '/cosmos-ui/servapps', page.locator('[data-router-chip]').first());
  const chips = (await page.locator('[data-router-chip]').allInnerTexts()).map((s) => s.trim());
  expect(chips.includes('jellyfin.example.org:8097') && chips.includes(host + ':51413/udp'), `cards: forwarded ports are external addresses (${chips.join(', ')})`);
  expect((await page.locator('[data-reach=forwarded]').count()) >= 1, 'cards: "Internet: port forwarded" instead of "if forwarded"');
  await page.screenshot({ path: `shots/${name}-cards.png`, fullPage: true });

  // --- installing: the same switch, off; opened once the container exists
  await goto(page, '/cosmos-ui/servapps/new-service', page.locator('#name'));
  await page.locator('#name').fill('game');
  await page.locator('#image').fill(image);
  await page.locator('#user').click();
  const next = async () => { await page.getByRole('button', { name: /Next/ }).first().click(); await page.waitForTimeout(1200); };
  await next(); await next();
  await page.getByRole('button', { name: 'Add', exact: true }).click();
  await page.waitForTimeout(400);
  await page.getByLabel('Host port').first().fill('27015'); await page.getByLabel('Container Port').first().fill('27015');
  await page.waitForTimeout(600);
  await next(); await next();
  const install = page.locator('[data-router-install]');
  await install.waitFor({ timeout: 15000 });
  expect(/Port 27015 \(TCP\) of game/.test(await install.innerText()), 'install: the published port is offered');
  expect(!(await install.locator('input[type=checkbox]').first().isChecked()), 'install: nothing is ticked by default');
  await install.locator('input[type=checkbox]').first().click();
  await dialog.waitFor();
  expect((await dialog.locator('[data-router-confirm]').innerText()).trim() === 'Open port 27015 of game', 'install: the same dialog, naming the port and the app');
  await dialog.locator('[data-router-confirm]').click();
  await dialog.waitFor({ state: 'detached' });
  expect(/What is ticked is opened on the router once the app is created/.test(await install.innerText()), 'install: says the port is opened once the app exists');
  expect(!(await router('/table'))['TCP/27015'], 'install: nothing is on the router before the app is created');
  await install.scrollIntoViewIfNeeded();
  await page.screenshot({ path: `shots/${name}-install.png`, fullPage: true });
  await page.getByRole('button', { name: /Create$/ }).click();
  const result = page.locator('[data-router-install-result]');
  await result.waitFor({ timeout: 90000 });
  expect(/Port 27015 of game: the router forwards port 27015/.test(await result.innerText()), 'install: the port is opened after the container was created');
  table = await router('/table');
  expect(table['TCP/27015'] && table['TCP/27015'].description === 'Cosmos app game', 'router: the installed app\'s port is forwarded');
  await page.screenshot({ path: `shots/${name}-install-done.png`, fullPage: true });

  // --- the overview: everything Cosmos holds, close per line
  await goto(page, '/cosmos-ui/config-general/http-server', page.locator('[data-router-overview]'));
  const rows = await page.locator('[data-router-row]').allInnerTexts();
  expect(rows.length === 4 && rows.some((r) => /Cosmos HTTP/.test(r)) && rows.some((r) => /jellyfin 8096\/tcp[\s\S]*TCP port 8097 on the router goes to port 8096 here[\s\S]*since/.test(r)), `overview: Cosmos' own port and the three app ports (${rows.length} lines)`);
  await page.locator('[data-router-overview]').scrollIntoViewIfNeeded();
  await page.screenshot({ path: `shots/${name}-overview.png`, fullPage: true });
  await page.locator('[data-router-row="game 27015/tcp"] [data-router-close]').click();
  await page.locator('[data-router-row="game 27015/tcp"]').waitFor({ state: 'detached', timeout: 20000 });
  expect(!(await router('/table'))['TCP/27015'], 'overview: "Close" withdraws the port from the router');
  table = await router('/table');
  expect(table['TCP/8096'].client === '10.213.79.99' && table['TCP/6881'].description === 'qBittorrent' && table['TCP/8880'].client === '10.213.79.98', 'router: nothing of anyone else\'s was changed');
  const calls = await router('/calls');
  expect(!calls.some((c) => /(AddPortMapping|DeletePortMapping) TCP\/(80|443|8096|6881|8880)$/.test(c)), 'router: never asked to add or delete 80, 443 or a port someone else holds');

  // --- the record
  const events = (await a.get('/cosmos/api/security/events?kind=upnp.*&limit=50')).events;
  expect(events.some((e) => e.kind === 'upnp.port.opened' && e.actor === 'admin' && e.target === 'game' && e.severity === 'high')
    && events.some((e) => e.kind === 'upnp.mapping.created' && e.target === 'jellyfin' && e.details.external === '8097' && e.details.wanted === '8096')
    && events.some((e) => e.kind === 'upnp.port.closed' && e.target === 'game')
    && events.some((e) => e.kind === 'upnp.mapping.removed' && e.target === 'game' && e.details.reason === 'off'), 'security record: opened, created (with the wanted port), closed, removed');
  await goto(page, '/cosmos-ui/security-audit', page.getByText(/asked the router to open port/).first());
  const audit = await page.locator('body').innerText();
  expect(/admin asked the router to open port 8096\/tcp of jellyfin to the internet/.test(audit) && /the router now forwards port 8097 \(tcp\) to jellyfin: port 8096 was taken/.test(audit), 'audit page: the entries read as sentences');
  await page.screenshot({ path: `shots/${name}-audit.png`, fullPage: true });
  await ctx.request.delete(base + '/cosmos/api/servapps/game').catch(() => {});
  await browser.close();
};

// ---------------------------------------------------------------------------
// the screens as they stand, in every engine and at phone width
const look = async (type, engine, viewport, size) => {
  const name = `${engine}-${size}`;
  const { browser, page } = await open(type, name, viewport);
  await goto(page, '/cosmos-ui/servapps/containers/jellyfin', page.locator('[data-router-port="jellyfin:8096/tcp"]'));
  const line = page.locator('[data-router-port="jellyfin:8096/tcp"]');
  expect(/The router forwards port 8097/.test(await line.innerText()) && (await line.locator('[data-router-address] a').getAttribute('href')) === 'http://jellyfin.example.org:8097', `${name}: container page shows the forwarded port and its link`);
  await line.scrollIntoViewIfNeeded();
  await noSidewaysScroll(page, name + ' container page');
  await page.screenshot({ path: `shots/${name}-container.png`, fullPage: true });
  // the dialog (on the UDP port, which is closed), looked at and cancelled
  const other = page.locator('[data-router-port="jellyfin:7359/udp"]');
  await other.locator('input[type=checkbox]').click();
  const dialog = page.locator('[data-router-dialog]');
  await dialog.waitFor();
  await page.waitForTimeout(500);
  const box = await dialog.locator('.MuiDialog-paper').boundingBox();
  expect(box.x >= 0 && box.x + box.width <= viewport.width + 1, `${name}: the dialog fits the screen (${Math.round(box.width)} of ${viewport.width})`);
  const button = await dialog.locator('[data-router-confirm]').boundingBox();
  expect(button.x >= box.x && button.x + button.width <= box.x + box.width + 1, `${name}: the confirmation button is inside the dialog`);
  await page.screenshot({ path: `shots/${name}-dialog.png` });
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await dialog.waitFor({ state: 'detached' });
  expect(!(await other.locator('input[type=checkbox]').isChecked()), `${name}: cancelling leaves the port closed`);
  await page.waitForTimeout(6000);

  await goto(page, '/cosmos-ui/servapps', page.locator('[data-router-chip]').first());
  expect((await page.locator('[data-router-chip]').count()) === 2 && (await page.locator('[data-reach=forwarded]').count()) >= 1, `${name}: the cards show the forwarded ports`);
  await noSidewaysScroll(page, name + ' cards');
  await page.screenshot({ path: `shots/${name}-cards.png`, fullPage: true });
  await page.waitForTimeout(6000);

  await goto(page, '/cosmos-ui/config-general/http-server', page.locator('[data-router-overview]'));
  expect((await page.locator('[data-router-row]').count()) === 3 && (await page.locator('[data-router-foreign]').count()) === 3, `${name}: the overview lists Cosmos' three ports and the three others`);
  await page.locator('[data-router-overview]').scrollIntoViewIfNeeded();
  await noSidewaysScroll(page, name + ' Internet access');
  await page.screenshot({ path: `shots/${name}-internet.png`, fullPage: true });
  await browser.close();
  await sleep(8000);
};

await prepare();
if (only.includes('chromium') && !process.env.LOOK_ONLY) await story();
const engines = { chromium, firefox, webkit };
for (const engine of only) {
  for (const [size, viewport] of [['desktop', { width: 1366, height: 900 }], ['phone', { width: 390, height: 844 }]]) {
    if (process.env.LOOK_ONLY || !(engine === 'chromium' && size === 'desktop')) await look(engines[engine], engine, viewport, size);
  }
}
note('\nscript errors:', errors.length ? errors.join('\n  ') : 'none');
note(failures.length ? `\n${failures.length} FAILED:\n  ` + failures.join('\n  ') : '\nall checks passed');
process.exit(failures.length || errors.length ? 1 : 0);

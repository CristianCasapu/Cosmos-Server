// Fork addition (plan 49): an app's links from home and from away, in three
// engines and two widths. Run by scripts/fork-ui-check-app-links.sh, which
// builds the two networks, the stand-in proxy for the public name, the test
// Cosmos and its Docker (see that file's header). Never point it at the
// production instance: PHASE=seed finishes the setup wizard and writes the
// configuration.
//
//   PHASE=seed  the wizard, the URLs (the owner's shape with documentation names), a plain user
//   PHASE=home  from 192.168.77.50: the tiles open the home addresses, the console opens, the menu lists
//               every interface with its role and Copy, the peer port and the database are copy-only,
//               ServApps and the app's page agree with the home page
//   PHASE=away  from 203.0.113.50 through the proxy: the public addresses, "Only at home" for the console
//               with its explanation, Copy and "Give it an internet address", the URL chip explains
//   PHASE=user  a plain user from away: the page for users, no published port, no more URLs than before
//   PHASE=role  the role set on the URL form changes the link the tile opens (and is set back)
// BASE, OUT, ENGINES=chromium,firefox,webkit, SIZES=d,m (1366x900, 393x851).
import { chromium, firefox, webkit, request } from 'playwright';
import { mkdirSync, writeFileSync, appendFileSync } from 'node:fs';

const base = process.env.BASE || 'http://192.168.77.10:18080';
const phase = process.env.PHASE || 'home';
const out = process.env.OUT || '/out';
const engines = (process.env.ENGINES || 'chromium,firefox,webkit').split(',').filter(Boolean);
const sizes = (process.env.SIZES || 'd,m').split(',').filter(Boolean);
const SIZE = { d: { width: 1366, height: 900 }, m: { width: 393, height: 851 } };
const admin = { nickname: 'admin', password: 'Test-Passw0rd!x' };
const user = { nickname: 'alice', password: 'Other-Passw0rd!y' };
mkdirSync(out + '/shots', { recursive: true });

const failures = [];
const note = (...a) => console.log(...a);
const check = (ok, what) => {
  note((ok ? 'ok   ' : 'FAIL ') + what);
  if (!ok) failures.push(phase + ': ' + what);
  appendFileSync(out + '/summary.txt', (ok ? 'ok   ' : 'FAIL ') + phase + ': ' + what + '\n');
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ---------------------------------------------------------------------------
if (phase === 'seed') {
  const api = await request.newContext({ baseURL: base });
  const call = async (method, path, data) => {
    try {
      const r = await api.fetch('/cosmos/api/' + path, { method, data, failOnStatusCode: false });
      let body = null;
      try { body = await r.json(); } catch (e) { /* not JSON */ }
      return { status: r.status(), body };
    } catch (e) { return { status: 0, body: null }; }
  };
  const waitUp = async () => {
    for (let i = 0; i < 90; i++) {
      const answer = (await call('GET', 'status')).status;
      if (answer > 0 && answer !== 502) return; // 502: the stand-in proxy while the server restarts
      await sleep(1000);
    }
    throw new Error('the instance did not come back');
  };
  await waitUp();
  if ((await call('POST', 'newInstall', { step: '2' })).status === 200) {
    await call('POST', 'newInstall', { step: '3', httpsCertificateMode: 'DISABLED', hostname: 'example.org' });
    await sleep(1500);
    await call('POST', 'newInstall', { step: '4', ...admin, email: 'admin@example.org' });
    await call('POST', 'newInstall', { step: '5' });
    await sleep(6000);
    await waitUp();
  }
  note('login', (await call('POST', 'login', admin)).status, 'sudo', (await call('POST', 'sudo', { password: admin.password })).status);
  const config = (await call('GET', 'config')).body.data;
  const route = (Name, Host, Target, more = {}) => ({
    Name, Description: '', UseHost: true, Host, Target, Mode: 'SERVAPP', UsePathPrefix: false, PathPrefix: '', StripPathPrefix: false,
    AuthEnabled: false, Timeout: 14400000, ThrottlePerMinute: 0, CORSOrigin: '', SmartShield: { Enabled: false }, ...more,
  });
  config.HTTPConfig.AllowHTTPLocalIPAccess = true;
  config.HTTPConfig.TrustedProxies = [process.env.PROXY_IP || '192.168.77.2'];
  config.HTTPConfig.ProxyConfig.Routes = [
    route('jellyfin', 'jellyfin.local', 'http://jellyfin:8096'),
    route('jellyfin-example.org', 'jellyfin.example.org', 'http://jellyfin:8096'),
    route('radarr', 'radarr.local', 'http://radarr:7878'),
    route('radarr-example.org', 'radarr.example.org', 'http://radarr:7878'),
    route('radarr-admin', 'radarr-admin.example.org', 'http://radarr:7878', { AdminOnly: true, AuthEnabled: true }),
    route('rustfs', 's3.example.org', 'http://rustfs:9000'),
    route('rustfs-console', 'rustfs-console.local', 'http://rustfs:9001'),
    route('qbittorrent', 'qbittorrent.local', 'http://qbittorrent:8080'),
    route('qbittorrent-example.org', 'qbittorrent.example.org', 'http://qbittorrent:8080', { AuthEnabled: true }),
  ];
  note('save the URLs', (await call('PUT', 'config', config)).status);
  await sleep(4000);
  await waitUp();
  await call('POST', 'login', admin);
  const users = ((await call('GET', 'users')).body.data || []).map((u) => u.nickname || u.Nickname);
  if (!users.includes(user.nickname)) {
    const made = await call('POST', 'users', { nickname: user.nickname, email: 'alice@example.org' });
    const key = made.body && made.body.data && (made.body.data.registerKey || made.body.data.RegisterKey);
    const visitor = await request.newContext({ baseURL: base });
    const r = await visitor.post('/cosmos/api/register', { data: { nickname: user.nickname, registerKey: key, password: user.password }, failOnStatusCode: false });
    note('plain user', made.status, r.status());
  }
  const dash = (await call('GET', 'dashboard')).body.data;
  check(dash.length === 9, 'the dashboard lists the 9 URLs for the administrator');
  const roles = Object.fromEntries(dash.map((r) => [r.Name, r.ForkRole + '/' + r.ForkRoleSource]));
  note('roles', JSON.stringify(roles));
  check(roles['rustfs-console'] === 'admin/recipe' && roles.rustfs === 'api/recipe', 'RustFS: console administration, S3 API (from the recipe)');
  check(roles.radarr === 'admin/app' && roles.jellyfin === 'user/app' && roles.qbittorrent === 'admin/app', 'the known apps\' roles');
  process.exit(failures.length ? 1 : 0);
}

// ---------------------------------------------------------------------------
const launchers = { chromium, firefox, webkit };
const who = phase === 'user' ? user : admin;
const atHome = phase === 'home';
const host = (h) => 'http://' + h + ':18080';

const tilesOf = (page) => page.locator('[data-app-tile]').evaluateAll((els) => els.map((el) => ({
  name: el.getAttribute('data-app-tile'), opens: el.getAttribute('data-opens') || '', state: el.getAttribute('data-state') || 'opens',
  href: el.getAttribute('href') || '', target: el.getAttribute('target') || '', rel: el.getAttribute('rel') || '', onlyHome: !!el.querySelector('[data-only-home]'),
})));

const noSideways = async (page, what) => {
  const [sw, iw] = await page.evaluate(() => [document.documentElement.scrollWidth, window.innerWidth]);
  check(sw <= iw + 1, what + ': no sideways scroll (' + sw + ' <= ' + iw + ')');
};

const menuItems = (page) => page.locator('[role=menu]:visible [role=menuitem], [role=menu]:visible [role=menuitemcheckbox]').evaluateAll((els) => els.map((el) => ({
  text: el.innerText.replace(/\s+/g, ' ').trim(), href: el.getAttribute('href') || '', interface: el.getAttribute('data-interface') || '',
  endpoint: el.getAttribute('data-endpoint') || '', copy: el.getAttribute('data-copy') || '', h: Math.round(el.getBoundingClientRect().height),
})));

const toastText = async (page) => (await page.locator('body').innerText()).match(/Copied: [^\n]+|did not let the page copy[^\n]*/g) || [];

for (const engine of engines) {
  for (const size of sizes) {
    const tag = phase + '-' + engine + '-' + size;
    const browser = await launchers[engine].launch();
    const context = await browser.newContext({ viewport: SIZE[size], serviceWorkers: 'block' });
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)));
    const popups = [];
    context.on('page', (p) => popups.push(p));
    const login = await context.request.post(base + '/cosmos/api/login', { data: who, failOnStatusCode: false });
    if (who === admin) await context.request.post(base + '/cosmos/api/sudo', { data: { password: admin.password }, failOnStatusCode: false });
    note('--- ' + tag + ' (login ' + login.status() + ')');

    await page.goto(base + '/cosmos-ui/', { waitUntil: 'domcontentloaded' });
    await page.locator('[data-app-tile]').first().waitFor({ timeout: 30000 });
    await sleep(1500);
    const status = await page.evaluate(async () => (await (await fetch('/cosmos/api/status', { credentials: 'same-origin' })).json()).data);
    check(status.visitor && status.visitor.where === (atHome ? 'home' : 'away'), tag + ': the server says the visitor is ' + JSON.stringify(status.visitor));
    check(!JSON.stringify(status.visitor).match(/\d+\.\d+/), tag + ': the status says nothing about addresses');
    const tiles = await tilesOf(page);
    const t = Object.fromEntries(tiles.map((x) => [x.name, x]));
    note(tag, JSON.stringify(tiles.map((x) => [x.name, x.opens || x.state])));
    await page.screenshot({ path: out + '/shots/' + tag + '-home.png', fullPage: true });
    await noSideways(page, tag + ' home');
    check(tiles.every((x) => !x.href || (x.target === '_blank' && /noopener/.test(x.rel))), tag + ': tile links open a new window with noopener');

    if (phase === 'home') {
      check(t.radarr && t.radarr.opens === host('radarr.local'), tag + ': radarr opens its home address');
      check(t.rustfs && t.rustfs.opens === host('rustfs-console.local'), tag + ': RustFS opens its console at home (not the S3 API)');
      check(t.jellyfin && t.jellyfin.opens === host('jellyfin.local') + '/web/#/dashboard', tag + ': Jellyfin opens its administration for an administrator');
      check(t.qbittorrent && t.qbittorrent.opens === host('qbittorrent.local'), tag + ': qBittorrent opens its home address');
      // the menu of RustFS: every interface with its role, Copy, the direct ports
      await page.locator('[data-app-more="rustfs"]').click();
      await page.locator('[role=menu]:visible').waitFor();
      await sleep(500); // the menu's opening transition
      const items = await menuItems(page);
      note(tag, 'rustfs menu', JSON.stringify(items.map((i) => i.text.slice(0, 70))));
      await page.screenshot({ path: out + '/shots/' + tag + '-menu.png' });
      check(items.some((i) => i.interface === 'admin' && i.href === host('rustfs-console.local')), tag + ': the menu opens the console (administration)');
      check(items.some((i) => i.interface === 'api' && i.href === host('s3.example.org')), tag + ': the menu lists the S3 API (internet)');
      check(items.some((i) => i.href === 'http://192.168.77.10:9001' && /published port/.test(i.text)), tag + ': the menu lists the console\'s published port');
      check(items.filter((i) => i.copy).length >= 3, tag + ': each address has Copy');
      check(items.some((i) => /Home only/.test(i.text)) && items.some((i) => /Internet/.test(i.text)), tag + ': marks "Home only" and "Internet"');
      if (size === 'm') check(items.every((i) => i.h >= 44), tag + ': menu lines are at least 44 px high (' + Math.min(...items.map((i) => i.h)) + ')');
      // Copy
      await page.locator('[role=menu]:visible [data-copy="' + host('rustfs-console.local') + '"]').click();
      await sleep(600);
      check((await toastText(page)).some((x) => x.includes('rustfs-console.local')), tag + ': Copy says what it copied');
      // keyboard: the menu is a real menu
      await page.locator('[data-app-more="rustfs"]').focus();
      await page.keyboard.press('Enter');
      await page.locator('[role=menu]:visible').waitFor();
      await page.keyboard.press('ArrowDown');
      const focused = await page.evaluate(() => document.activeElement && document.activeElement.getAttribute('role'));
      check(focused === 'menuitem' || focused === 'menuitemcheckbox', tag + ': arrow keys move through the menu (' + focused + ')');
      await page.keyboard.press('Escape');
      // qBittorrent: the peer port is copy-only
      await page.locator('[data-app-more="qbittorrent"]').click();
      await page.locator('[role=menu]:visible').waitFor();
      const q = await menuItems(page);
      const peer = q.find((i) => i.endpoint && i.endpoint.endsWith(':6881'));
      check(!!peer && !peer.href && /BitTorrent/.test(peer.text), tag + ': the peer port is listed, copy-only, never a link');
      await page.keyboard.press('Escape');
      // ServApps and the app's page agree with the home page
      await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
      await page.locator('[data-app-open="rustfs"]').waitFor({ timeout: 30000 });
      await sleep(1500);
      const open = (name) => page.locator('[data-app-open="' + name + '"] [data-opens]').getAttribute('data-opens').catch(() => '');
      check(await open('rustfs') === t.rustfs.opens && await open('radarr') === t.radarr.opens, tag + ': ServApps opens what the home tile opens');
      const pg = page.locator('[data-app-open="postgres"]');
      check(await pg.count() === 1 && await pg.locator('[data-opens]').count() === 0, tag + ': the database has no web link on ServApps');
      if (await pg.count()) {
        await pg.locator('button').last().click();
        await page.locator('[role=menu]:visible').waitFor({ timeout: 5000 }).catch(() => {});
        await sleep(500);
        const p = await menuItems(page);
        check(p.some((i) => i.copy === 'postgresql://USER:PASSWORD@192.168.77.10:5432/DATABASE'), tag + ': the database\'s connection string, with placeholders');
        await page.screenshot({ path: out + '/shots/' + tag + '-servapps-db.png' });
        await page.keyboard.press('Escape');
      }
      await page.screenshot({ path: out + '/shots/' + tag + '-servapps.png', fullPage: true });
      await noSideways(page, tag + ' servapps');
      await page.goto(base + '/cosmos-ui/servapps/containers/rustfs', { waitUntil: 'domcontentloaded' });
      await page.locator('[data-app-open="rustfs"]').waitFor({ timeout: 30000 });
      await sleep(1000);
      check(await open('rustfs') === t.rustfs.opens, tag + ': the app\'s page opens what the home tile opens');
      await page.screenshot({ path: out + '/shots/' + tag + '-app.png', fullPage: true });
      await noSideways(page, tag + ' app page');
    }

    if (phase === 'away') {
      check(t.radarr && t.radarr.opens === host('radarr.example.org'), tag + ': radarr opens its public address from away');
      check(t.jellyfin && t.jellyfin.opens === host('jellyfin.example.org') + '/web/#/dashboard', tag + ': Jellyfin\'s administration on its public address');
      check(t.rustfs && t.rustfs.state === 'home-only' && t.rustfs.onlyHome && !t.rustfs.href, tag + ': RustFS says "Only at home" and is no link');
      await page.locator('[data-app-tile="rustfs"]').click();
      const dialog = page.locator('[role=dialog]:visible');
      await dialog.waitFor();
      const words = await dialog.innerText();
      check(/home network only/.test(words) && /not at home/.test(words), tag + ': the dialog explains in one sentence');
      await sleep(400); // the dialog's opening transition
      check(await dialog.locator('[data-copy="' + host('rustfs-console.local') + '"]').count() === 1, tag + ': the dialog offers the home address to copy');
      check(await dialog.locator('[data-expose]').count() === 1, tag + ': an administrator is offered "Give it an internet address"');
      await page.screenshot({ path: out + '/shots/' + tag + '-only-home.png' });
      await dialog.locator('[data-copy="' + host('rustfs-console.local') + '"]').click();
      await sleep(600);
      check((await toastText(page)).length > 0, tag + ': Copy gives feedback');
      await dialog.locator('[data-expose]').click();
      await sleep(2500);
      const expose = await page.locator('[role=dialog]:visible').innerText().catch(() => '');
      check(/internet|domain|Internet/i.test(expose), tag + ': the Internet dialog of the app opens (the existing flow)');
      await page.screenshot({ path: out + '/shots/' + tag + '-expose.png' });
      await page.keyboard.press('Escape');
      await sleep(500);
      check(popups.length === 0, tag + ': nothing was opened in a new window');
      // the URL chip of the console on ServApps explains instead of opening
      await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
      await page.locator('[data-app-open="rustfs"]').waitFor({ timeout: 30000 });
      await sleep(1500);
      const chip = page.locator('[data-unreachable="true"]', { hasText: 'rustfs-console.local' }).first();
      check(await chip.count() === 1, tag + ': the console\'s chip knows it does not open from here');
      if (await chip.count()) {
        await chip.click();
        await page.locator('[role=dialog]:visible').waitFor({ timeout: 5000 }).catch(() => {});
        check(await page.locator('[role=dialog]:visible [data-unreachable-sentence]').count() === 1 && popups.length === 0, tag + ': the chip explains instead of opening a dead link');
        await page.keyboard.press('Escape');
      }
      const open = await page.locator('[data-app-open="rustfs"]').innerText();
      check(/Only at home/.test(open), tag + ': ServApps says "Only at home" as the home page does');
      await page.screenshot({ path: out + '/shots/' + tag + '-servapps.png', fullPage: true });
      await noSideways(page, tag + ' servapps');
    }

    if (phase === 'user') {
      const dash = await page.evaluate(async () => (await (await fetch('/cosmos/api/dashboard', { credentials: 'same-origin' })).json()).data);
      check(dash.length === 8 && !dash.some((r) => r.Name === 'radarr-admin'), tag + ': a plain user gets the 8 URLs that are not for administrators only');
      check(!JSON.stringify(dash).match(/ForkPorts|ForkImage|ForkContainer|"public":|6881|5432/), tag + ': no image, container or published port reaches a plain user');
      check(t.jellyfin && t.jellyfin.opens === host('jellyfin.example.org'), tag + ': Jellyfin opens its page for users for a plain user');
      check(t.rustfs && t.rustfs.state === 'home-only', tag + ': RustFS says "Only at home" for a plain user too');
      await page.locator('[data-app-tile="rustfs"]').click();
      await page.locator('[role=dialog]:visible').waitFor();
      check(await page.locator('[role=dialog]:visible [data-expose]').count() === 0, tag + ': no "Give it an internet address" for a plain user');
      await page.keyboard.press('Escape');
      await page.locator('[data-app-more="qbittorrent"]').click();
      await page.locator('[role=menu]:visible').waitFor();
      const q = await menuItems(page);
      check(q.every((i) => !i.endpoint && !/published port/.test(i.text)), tag + ': no published port in a plain user\'s menu');
      await page.keyboard.press('Escape');
      await sleep(400);
      await page.locator('[data-app-more="jellyfin"]').click();
      await page.locator('[role=menu]:visible').waitFor();
      const j = await menuItems(page);
      check(j.length > 0 && j.every((i) => !/dashboard/.test(i.href + i.copy) && i.interface !== 'admin'), tag + ': no administration link for a plain user');
      check(!JSON.stringify(dash).match(/ForkAdminPath|ForkRoleSource|ForkRoleProposed/), tag + ': no administration path or role source reaches a plain user');
      await page.keyboard.press('Escape');
    }

    if (phase === 'role' && engine === engines[0] && size === sizes[0]) {
      check(t.rustfs && t.rustfs.state === 'home-only', tag + ': before: RustFS only at home');
      // ServApps first, so that its shared dashboard answer is loaded; then the form within the app
      // (no page load): after the save ServApps must show the new role at once (forgetAppLinks)
      const go = (path) => page.evaluate((p) => { window.history.pushState({}, '', p); window.dispatchEvent(new PopStateEvent('popstate')); }, path);
      await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
      await page.locator('[data-app-open="rustfs"]').waitFor({ timeout: 30000 });
      check(/Only at home/.test(await page.locator('[data-app-open="rustfs"]').innerText()), tag + ': before: ServApps says only at home');
      await go('/cosmos-ui/config-url/rustfs');
      await page.getByRole('tab', { name: 'Setup' }).click();
      await page.locator('[id="mui-component-select-_ForkRole"], [role=combobox]#_ForkRole').first().waitFor({ timeout: 30000 });
      await page.locator('[id="mui-component-select-_ForkRole"], [role=combobox]#_ForkRole').first().click();
      await page.getByRole('option', { name: 'Page for users' }).click();
      await page.screenshot({ path: out + '/shots/' + tag + '-form.png', fullPage: true });
      await page.getByRole('button', { name: 'Save', exact: true }).click();
      await sleep(5000);
      await go('/cosmos-ui/servapps');
      await page.locator('[data-app-open="rustfs"] [data-opens]').waitFor({ timeout: 10000 }).catch(() => {});
      check(await page.locator('[data-app-open="rustfs"] [data-opens]').getAttribute('data-opens').catch(() => '') === host('s3.example.org'), tag + ': right after the save, ServApps opens what the new role says (no page load)');
      await page.goto(base + '/cosmos-ui/', { waitUntil: 'domcontentloaded' });
      await page.locator('[data-app-tile]').first().waitFor({ timeout: 30000 });
      await sleep(1500);
      const after = Object.fromEntries((await tilesOf(page)).map((x) => [x.name, x]));
      check(after.rustfs && after.rustfs.opens === host('s3.example.org'), tag + ': after the role is set by hand, the tile opens that address');
      // and back to "as proposed"
      await page.goto(base + '/cosmos-ui/config-url/rustfs', { waitUntil: 'domcontentloaded' });
      await page.getByRole('tab', { name: 'Setup' }).click();
      await page.locator('[id="mui-component-select-_ForkRole"], [role=combobox]#_ForkRole').first().waitFor({ timeout: 30000 });
      await page.locator('[id="mui-component-select-_ForkRole"], [role=combobox]#_ForkRole').first().click();
      await page.getByRole('option', { name: /^As proposed/ }).click();
      await page.getByRole('button', { name: 'Save', exact: true }).click();
      await sleep(5000);
      const dash = await page.evaluate(async () => (await (await fetch('/cosmos/api/dashboard', { credentials: 'same-origin' })).json()).data);
      check(dash.find((r) => r.Name === 'rustfs').ForkRoleSource === 'recipe', tag + ': "as proposed" removes the setting');
    }

    check(errors.length === 0, tag + ': no script errors' + (errors.length ? ' (' + errors.join(' | ') + ')' : ''));
    await browser.close();
  }
}
note(failures.length ? failures.length + ' failed' : 'all passed');
process.exit(failures.length ? 1 : 0);

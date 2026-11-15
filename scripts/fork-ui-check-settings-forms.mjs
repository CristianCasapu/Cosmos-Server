// Fork addition: the settings forms of plan 22: the HTTP settings (ports as one
// choice, the check, the admin whitelist and its lock-out refusal), the URL
// Security tab (protection levels), the alert editor (ready-made alerts, the
// read-back, the checks), the API token form, and the format dialog.
// ENGINES=chromium,firefox,webkit  SIZES=d,m  TAG=<name>  BASE=http://<name>
// The test instance: a fresh throwaway Cosmos with HTTPS off, under a name with a
// dot (WebKit), admin / Test-Passw0rd!x, no Docker needed. It changes that
// instance's settings, URLs, alerts and tokens.
// The format dialog never reaches the server: the disk list, the status and the
// format request are answered by the browser (page.route), so no disk is ever
// touched, whatever the instance runs on.
// Screenshots go to ./shots (create it). The run ends with "ALL OK" or the failures.
import { chromium, firefox, webkit } from 'playwright';

const base = process.env.BASE || 'http://forms3.test';
const tag = process.env.TAG || 'after';
const engines = { chromium, firefox, webkit };
const names = (process.env.ENGINES || 'chromium').split(',');
const sizes = { d: { width: 1366, height: 900 }, m: { width: 390, height: 844 } };
const wanted = (process.env.SIZES || 'd').split(',');
const note = (...a) => console.log(...a);
const failures = [];
const expect = (what, ok, detail) => { note(ok ? '  ok  ' : '  FAIL', what, detail === undefined ? '' : '| ' + String(detail).slice(0, 300)); if (!ok) failures.push(what); };
const wait = (ms) => new Promise((s) => setTimeout(s, ms));
const PASSWORD = 'Test-Passw0rd!x';

const getConfig = async (ctx) => (await (await ctx.request.get(base + '/cosmos/api/config')).json()).data;
const putConfig = async (ctx, config) => ctx.request.put(base + '/cosmos/api/config', { data: config });
const settle = async (ctx) => {
  for (let i = 0; i < 30; i++) {
    try { if ((await ctx.request.get(base + '/cosmos/api/status')).ok()) return; } catch (e) { /* restarting */ }
    await wait(1000);
  }
};
// On a phone the tabs are a select
const openTab = async (page, title) => {
  const tab = page.getByRole('tab', { name: title });
  if (await tab.count() && await tab.first().isVisible()) {
    await tab.first().click();
  } else {
    await page.locator('.MuiSelect-select').first().click();
    await page.waitForTimeout(500);
    await page.locator('[role=option]', { hasText: title }).first().click();
  }
  await page.waitForTimeout(1500);
};
const noSideways = async (page) => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);

const ROUTE = 'forms3-shield';
const ensureRoute = async (ctx) => {
  const config = await getConfig(ctx);
  config.HTTPConfig.ProxyConfig.Routes = (config.HTTPConfig.ProxyConfig.Routes || []).filter((r) => r.Name !== ROUTE);
  config.HTTPConfig.ProxyConfig.Routes.push({
    Name: ROUTE, Description: 'test', Mode: 'PROXY', Target: 'http://127.0.0.1:9', UseHost: true, Host: 'shield.forms3.test',
    UsePathPrefix: false, PathPrefix: '', StripPathPrefix: false, AuthEnabled: false, Timeout: 14400000, ThrottlePerMinute: 10000,
    BlockCommonBots: true, SmartShield: { Enabled: true }, CORSOrigin: '',
  });
  await putConfig(ctx, config);
  await wait(3000);
  await settle(ctx);
};

const FAKE_DISKS = [
  { name: '/dev/sdz', type: 'disk', model: 'Test USB', size: 64000000000, rota: false, children: [{ name: '/dev/sdz1', type: 'part', fstype: 'exfat', size: 64000000000, children: [] }] },
  { name: '/dev/sdy', type: 'disk', model: 'System', size: 500000000000, rota: false, children: [{ name: '/dev/sdy1', type: 'part', fstype: 'ext4', mountpoint: '/', size: 500000000000, children: [] }] },
];

for (const name of names) for (const size of wanted) {
  const id = name + '-' + size;
  const browser = await engines[name].launch();
  const ctx = await browser.newContext({ viewport: sizes[size] });
  await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: PASSWORD } });
  await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: PASSWORD } });
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message.slice(0, 160)));

  // --- the server refuses what would take the panel away -------------------
  if (name === names[0] && size === wanted[0]) {
    note(`--- ${id}: server-side checks`);
    let config = await getConfig(ctx);
    const locked = await putConfig(ctx, { ...config, AdminWhitelistIPs: ['10.0.0.0/8'] });
    const lockedBody = await locked.json();
    expect('a whitelist without the address in use is refused (UC034)', locked.status() === 400 && lockedBody.code === 'UC034', lockedBody.message);
    const badPort = await putConfig(ctx, { ...config, HTTPConfig: { ...config.HTTPConfig, HTTPPort: '8o80' } });
    expect('a port that is not a number is refused (UC031)', badPort.status() === 400 && (await badPort.json()).code === 'UC031');
    const badHost = await putConfig(ctx, { ...config, HTTPConfig: { ...config.HTTPConfig, Hostname: 'bad_name.forms3.test' } });
    expect('a hostname that is not one is refused (UC030)', badHost.status() === 400 && (await badHost.json()).code === 'UC030');
    const tooBig = await ctx.request.post(base + '/cosmos/api/config/http-check', { data: { httpPort: '80', host: '10.0.0.1' } });
    expect('the check refuses unknown fields', tooBig.status() === 400);
    config = await getConfig(ctx);
    expect('nothing of it was saved', (config.AdminWhitelistIPs || []).length === 0 && config.HTTPConfig.HTTPPort === '80');
    const tokenBad = await ctx.request.post(base + '/cosmos/api/api-tokens', { data: { name: 'bad-ip', ipWhitelist: ['my laptop'] } });
    expect('a token with an IP restriction that is not an address is refused (AT017)', tokenBad.status() === 400 && (await tokenBad.json()).code === 'AT017');
  }

  // --- HTTP settings --------------------------------------------------------
  note(`--- ${id}: HTTP settings`);
  await page.goto(base + '/cosmos-ui/config-general/http-server', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(4000);
  const ports = page.locator('#fork-http-ports');
  expect(id + ': the ports are one choice, the usual pair picked', (await ports.locator('[role=radio][aria-checked=true]').innerText()).includes('80 and 443'));
  await ports.getByText('8080 and 8443').click();
  await page.waitForTimeout(2500);
  let body = await page.locator('body').innerText();
  expect(id + ': before saving, the new address is said', body.includes('The panel moves to http://' + new URL(base).hostname + ':8080'), body.match(/The panel moves[^\n]*/));
  expect(id + ': the check says the new port is free', /Port 8080 is free on this server/.test(body));
  expect(id + ': and that saving restarts the web server', /Saving restarts the web server/.test(body));
  await ports.getByText('Other ports').click();
  await page.locator('#HTTPPort-login').fill('8o80');
  await page.locator('#HTTPPort-login').blur();
  await page.waitForTimeout(500);
  expect(id + ': a port that is not a number is marked under its field', /A port is a whole number from 1 to 65535/.test(await page.locator('body').innerText()));
  await ports.getByText('80 and 443 (usual)').click();
  await page.waitForTimeout(1500);
  body = await page.locator('body').innerText();
  const proposal = body.match(/Add (\d+\.\d+\.\d+\.\d+)\s+The address you are connected from right now/);
  expect(id + ': the address in use is proposed for the admin whitelist', /The address you are connected from right now/.test(body), body.match(/Add \d[^\n]*/g));
  expect(id + ': and the network the admin shares with the server', /The local network you and the server are on/.test(body));
  await page.locator('#fork-whitelist-add').fill('10.0.0.0/8');
  // the trusted proxies have an Add button of their own on the same tab: the whitelist's is next to its field
  await page.locator('.MuiStack-root:has(#fork-whitelist-add):has(> button)').locator('> button', { hasText: /^Add$/ }).click();
  await page.waitForTimeout(2500);
  body = await page.locator('body').innerText();
  expect(id + ': a list without the address in use says it would lock the admin out', /This list would lock you out/.test(body));
  expect(id + ': the page fits', await noSideways(page) <= 0, await noSideways(page));
  await page.screenshot({ path: `shots/${tag}-http-${id}.png`, fullPage: true });
  const savePut = page.waitForResponse((r) => r.url().endsWith('/cosmos/api/config') && r.request().method() === 'PUT', { timeout: 8000 }).catch(() => null);
  await page.locator('button[type=submit]').first().click();
  const answer = await savePut;
  expect(id + ': saving it is refused by the server', answer && answer.status() === 400, answer && answer.status());
  await page.waitForTimeout(1500);
  expect(id + ': and nothing was saved', ((await getConfig(ctx)).AdminWhitelistIPs || []).length === 0);
  await page.getByText('Add ' + (proposal ? proposal[1] : 'x'), { exact: true }).click().catch(() => {});
  await page.waitForTimeout(2500);
  body = await page.locator('body').innerText();
  expect(id + ': with the address added the list allows it', /the list allows it/.test(body) && !/This list would lock you out/.test(body), (proposal ? proposal[1] : 'no proposal') + ' ' + JSON.stringify(body.match(/Add \d[^\n]*\n[^\n]*/g)));

  // --- the URL Security tab ---------------------------------------------------
  note(`--- ${id}: URL protection`);
  await ensureRoute(ctx);
  const openSecurity = async () => {
    await page.goto(base + '/cosmos-ui/config-url/' + ROUTE, { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(3500);
    await openTab(page, 'Security');
  };
  await openSecurity();
  const level = page.locator('#fork-protection');
  expect(id + ': a new URL is at "Standard"', (await level.locator('[role=radio][aria-checked=true]').innerText()).includes('Standard'));
  body = await page.locator('body').innerText();
  expect(id + ': the level says what a visitor may do', /up to 36 000 requests and fetch 400 GB an hour/.test(body), body.match(/A visitor may[^\n]*/));
  expect(id + ': the numbers are behind a door', !(await page.locator('[name=_SmartShield_PerUserRequestLimit]').isVisible()));
  await level.getByText('Strict', { exact: true }).click();
  await page.waitForTimeout(400);
  expect(id + ': the page fits', await noSideways(page) <= 0, await noSideways(page));
  await page.screenshot({ path: `shots/${tag}-protection-${id}.png`, fullPage: true });
  await page.getByRole('button', { name: 'Save' }).last().click();
  await page.waitForTimeout(5000);
  await settle(ctx);
  let route = (await getConfig(ctx)).HTTPConfig.ProxyConfig.Routes.find((r) => r.Name === ROUTE);
  expect(id + ': "Strict" is saved as strictness 1, 500 a minute, the numbers left at the defaults',
    route.SmartShield.PolicyStrictness === 1 && route.ThrottlePerMinute === 500 && route.SmartShield.PerUserRequestLimit === 0 && route.Timeout === 14400000, JSON.stringify(route.SmartShield) + ' ' + route.ThrottlePerMinute);
  // a value set by hand shows as custom
  const config = await getConfig(ctx);
  config.HTTPConfig.ProxyConfig.Routes = config.HTTPConfig.ProxyConfig.Routes.map((r) => (r.Name === ROUTE ? { ...r, SmartShield: { ...r.SmartShield, PerUserRequestLimit: 1234 } } : r));
  await putConfig(ctx, config);
  await wait(3000);
  await settle(ctx);
  await openSecurity();
  expect(id + ': hand-set values show as "Custom values", the door open', (await level.locator('[role=radio][aria-checked=true]').innerText()).includes('Custom') && await page.locator('[name=_SmartShield_PerUserRequestLimit]').isVisible());

  // --- alerts -------------------------------------------------------------------
  note(`--- ${id}: alerts`);
  await page.goto(base + '/cosmos-ui/monitoring', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(3000);
  await openTab(page, 'Alerts');
  await page.getByRole('button', { name: 'Create' }).click();
  await page.waitForTimeout(2000);
  let dialog = page.locator('[role=dialog]').last();
  let text = await dialog.innerText();
  expect(id + ': a new alert offers ready-made ones', /Start from a ready-made alert/.test(text) && /Memory nearly full/.test(text), text.slice(0, 200));
  await dialog.getByText('Memory nearly full', { exact: true }).click();
  await page.waitForTimeout(600);
  text = await dialog.innerText();
  expect(id + ': the alert reads back in words', /When RAM is above 90 % of the maximum on average over an hour: show a notification in Cosmos/.test(text), text.match(/When [^\n]*/));
  expect(id + ': the dialog fits', await noSideways(page) <= 0);
  await page.screenshot({ path: `shots/${tag}-alert-${id}.png` });
  await dialog.getByRole('button', { name: 'Save' }).click();
  await page.waitForTimeout(6000);
  await settle(ctx);
  const alerts = (await getConfig(ctx)).MonitoringAlerts || {};
  const ram = alerts['Memory nearly full'];
  expect(id + ': the ready-made alert is saved as shown', ram && ram.TrackingMetric === 'cosmos.system.ram' && ram.Condition.Percent && ram.Condition.Value === 90 && ram.Period === 'hourly', JSON.stringify(ram));
  await page.waitForTimeout(1500);
  await page.getByRole('button', { name: 'Create' }).click();
  await page.waitForTimeout(1500);
  dialog = page.locator('[role=dialog]').last();
  await dialog.getByText('Memory nearly full', { exact: true }).click();
  await dialog.getByRole('button', { name: 'Save' }).click();
  await page.waitForTimeout(800);
  expect(id + ': a second alert of the same name is caught before saving', /Another alert has this name/.test(await dialog.innerText()) && await dialog.isVisible());
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  const cleaned = await getConfig(ctx);
  delete cleaned.MonitoringAlerts['Memory nearly full'];
  await putConfig(ctx, cleaned);
  await wait(3000);
  await settle(ctx);

  // --- API tokens -----------------------------------------------------------------
  note(`--- ${id}: API token`);
  await page.goto(base + '/cosmos-ui/config-general/api', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(3500);
  await page.getByRole('button', { name: 'New Token' }).click();
  await page.waitForTimeout(2000);
  dialog = page.locator('[role=dialog]').last();
  text = await dialog.innerText();
  expect(id + ': read only is proposed', (await dialog.locator('#fork-token-preset [role=radio][aria-checked=true]').innerText()).includes('Read only'));
  expect(id + ': the place is proposed from the address in use', (await dialog.locator('#fork-token-place [role=radio][aria-checked=true]').innerText()).includes('This network'), await dialog.locator('#fork-token-place').innerText());
  expect(id + ': an end date of 90 days is proposed', /90 days/.test(text));
  await dialog.getByText('Choose', { exact: true }).click();
  await dialog.getByText('Manage the apps and disks', { exact: false }).first().click();
  await page.waitForTimeout(300);
  expect(id + ': managing containers is named as full control', /amounts to full control: a container can be given the server's whole disk/.test(await dialog.innerText()));
  expect(id + ': the dialog fits', await noSideways(page) <= 0);
  await page.screenshot({ path: `shots/${tag}-token-${id}.png` });
  await dialog.getByText('Read only (recommended)').click();
  const tokenName = 'forms3-' + id;
  await dialog.locator('#fork-token-name').fill(tokenName);
  await dialog.getByRole('button', { name: 'Create' }).click();
  await page.waitForTimeout(3000);
  const reveal = page.locator('[role=dialog]').last();
  const shown = await reveal.locator('input').first().inputValue().catch(() => '');
  expect(id + ': the token is shown once, after Create', shown.length > 20);
  const tokens = (await (await ctx.request.get(base + '/cosmos/api/api-tokens')).json()).data || {};
  const tok = tokens[tokenName];
  expect(id + ': it is saved read only, limited to the network, ending in 90 days',
    tok && JSON.stringify(tok.permissions) === '[1,10,20,30]' && (tok.ipWhitelist || []).length === 1 && /\/\d+$/.test(tok.ipWhitelist[0]) && tok.expiresAt, JSON.stringify(tok));
  await ctx.request.delete(base + '/cosmos/api/api-tokens', { data: { name: tokenName } });
  await reveal.getByRole('button', { name: /Done/ }).click().catch(() => {});

  // --- format dialog (answered by the browser, never the server) -------------------
  note(`--- ${id}: format dialog`);
  let formatBody = null;
  await page.route('**/cosmos/api/status', async (r) => {
    const res = await r.fetch();
    const json = await res.json();
    json.data = { ...json.data, containerized: false };
    await r.fulfill({ response: res, json });
  });
  await page.route('**/cosmos/api/disks', (r) => r.fulfill({ json: { status: 'OK', data: FAKE_DISKS } }));
  await page.route('**/cosmos/api/disks/format', (r) => {
    formatBody = r.request().postDataJSON();
    return r.fulfill({ status: 200, contentType: 'text/plain', body: 'Formatting /dev/sdz1 as ext4...\n\x1b[31m[ERROR]\x1b[0m [OPERATION FAILED] Formatting /dev/sdz1 as ext4 failed (exit status 1): stand-in failure\n' });
  });
  await page.route('**/cosmos/api/smart-def', (r) => r.fulfill({ json: { status: 'OK', data: {} } }));
  await page.goto(base + '/cosmos-ui/storage', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(4000);
  await page.getByRole('button', { name: 'Format' }).first().click();
  await page.waitForTimeout(1500);
  dialog = page.locator('[role=dialog]').last();
  text = await dialog.innerText();
  expect(id + ': the dialog names the disk, its size and what is on it', /Erase and format sdz/.test(text) && /Test USB/.test(text) && /exfat/.test(text), text.slice(0, 200));
  expect(id + ': exFAT is proposed for a disk that holds it', (await dialog.locator('#fork-format-fs [role=radio][aria-checked=true]').innerText()).includes('exFAT'));
  const erase = dialog.getByRole('button', { name: 'Erase and format' });
  await dialog.locator('#fork-format-password').fill(PASSWORD);
  await dialog.locator('#fork-format-confirm').fill('sdz1');
  expect(id + ': another name does not unlock the button', await erase.isDisabled());
  await dialog.locator('#fork-format-confirm').fill('sdz');
  expect(id + ': the disk\'s name does', await erase.isEnabled());
  expect(id + ': the dialog fits', await noSideways(page) <= 0);
  await page.screenshot({ path: `shots/${tag}-format-${id}.png` });
  await erase.click();
  await page.waitForTimeout(2500);
  expect(id + ': the typed name is sent to the server', formatBody && formatBody.confirm === 'sdz' && formatBody.disk === '/dev/sdz' && formatBody.format === 'exfat', JSON.stringify(formatBody && { ...formatBody, password: '***' }));
  const shownLog = await page.locator('[role=dialog] pre').last().innerText().catch(() => '');
  const flat = shownLog.replace(/[\u00a0\s]+/g, ' ');
  expect(id + ': a failure is shown as a failure with its reason', flat.includes('OPERATION FAILED') && flat.includes('stand-in failure'), JSON.stringify(shownLog));
  await page.screenshot({ path: `shots/${tag}-format-failed-${id}.png` });
  await page.keyboard.press('Escape');
  await page.waitForTimeout(500);
  await page.locator('li[role=treeitem]', { hasText: 'sdy' }).first().getByRole('button', { name: 'Format' }).first().click().catch(() => {});
  await page.waitForTimeout(1000);
  const sys = await page.locator('[role=dialog]').last().innerText().catch(() => '');
  expect(id + ': a disk with a mounted partition is refused in the dialog', /is mounted at \/: unmount it first/.test(sys), sys.slice(0, 160));

  expect(id + ': no script error', errors.length === 0, errors.join(' / '));
  await browser.close();
}

note(failures.length ? 'FAILURES:\n' + failures.join('\n') : 'ALL OK');
process.exit(failures.length ? 1 : 0);

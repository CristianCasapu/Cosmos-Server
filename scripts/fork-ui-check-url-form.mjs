// Fork addition: the simple view of the URL form (pages/config/routes/forkRouteGuide.jsx):
// a new address for an app from the ServApps page, URLs > Create, and the Setup and
// Security tabs of a URL, which must not undo each other.
// ENGINES=chromium,firefox,webkit  SIZES=d,m  TAG=<name>  PREP=1 adds the domain
// example.org and switches on .local names first (once per instance).
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
const expect = (what, ok, detail) => { note(ok ? '  ok  ' : '  FAIL', what, detail === undefined ? '' : '| ' + String(detail).slice(0, 200)); if (!ok) failures.push(what); };
const login = async (ctx) => {
  await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: 'Test-Passw0rd!x' } });
  await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: 'Test-Passw0rd!x' } });
};
const config = async (ctx) => (await (await ctx.request.get(base + '/cosmos/api/config')).json()).data;
const routes = async (ctx) => (await config(ctx)).HTTPConfig.ProxyConfig.Routes || [];
const dropRoute = async (ctx, name) => {
  const r = await ctx.request.patch(base + '/cosmos/api/config', { data: { routeName: name, operation: 'delete' } });
  await new Promise((s) => setTimeout(s, 2500));
  return r.status();
};
const go = async (page, path) => { await page.goto(base + path, { waitUntil: 'domcontentloaded' }); await page.waitForTimeout(3500); };
const chip = (scope, text) => scope.locator('.MuiChip-root[role=radio]', { hasText: text }).first();
const picked = async (scope, group) => (await scope.locator(`#${group} .MuiChip-root[aria-checked=true]`).allInnerTexts()).map((text) => text.trim()).join(',');
const sideways = (page) => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
const cut = async (page, dialog) => dialog.evaluate((el) => { const c = el.querySelector('.MuiDialogContent-root'); return c ? c.scrollWidth - c.clientWidth : 0; });

if (process.env.PREP) {
  const browser = await chromium.launch();
  const ctx = await browser.newContext();
  await login(ctx);
  let r = await ctx.request.put(base + '/cosmos/api/zones/example.org', { data: { Zone: 'example.org', HTTPSCertificateMode: 'DISABLED' } });
  note('prep: domain', r.status()); await new Promise((s) => setTimeout(s, 8000));
  const c = await config(ctx);
  c.HTTPConfig.PublishMDNS = true;
  r = await ctx.request.put(base + '/cosmos/api/config', { data: c });
  note('prep: mDNS', r.status()); await new Promise((s) => setTimeout(s, 8000));
  await browser.close();
}

for (const name of names) for (const size of wanted) {
  const id = name + '-' + size;
  const browser = await engines[name].launch();
  const ctx = await browser.newContext({ viewport: sizes[size] });
  await login(ctx);
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message.slice(0, 160)));
  for (const leftover of ['radarr', 'radarr-example.org', 'sonarr', 'router']) {
    if ((await routes(ctx)).some((r) => r.Name === leftover)) await dropRoute(ctx, leftover);
  }

  // ---- A. a new address for radarr
  note(`--- ${id}: New URL for radarr`);
  await go(page, '/cosmos-ui/servapps');
  const card = page.locator('.MuiPaper-root', { hasText: 'linuxserver/radarr' }).last();
  await card.locator('.MuiChip-root', { hasText: /^New$/ }).click();
  await page.waitForTimeout(2500);
  const dialog = page.locator('[role=dialog]').last();
  let text = await dialog.innerText();
  expect(id + ': the dialog names the app', /An address for radarr/.test(text));
  expect(id + ': the address is proposed', await dialog.locator('#Host').inputValue() === 'radarr.local', await dialog.locator('#Host').inputValue());
  expect(id + ': the app\'s usual port is picked', await picked(dialog, 'Target') === '7878', await picked(dialog, 'Target'));
  expect(id + ': and the form says where the proposal comes from', /Proposed: 7878 is the usual web port of radarr/.test(text));
  expect(id + ': http is picked', await picked(dialog, 'guide-scheme') === 'http');
  expect(id + ': anyone may open it, as upstream', await picked(dialog, 'guide-access') === 'Anyone with the address');
  expect(id + ': it says which name the app will see', /receives its requests under the name radarr\.local\. Some apps only answer to names they know/.test(text));
  expect(id + ': the name is proposed, not asked', /Saved as "radarr"/.test(text) && await dialog.locator('#Name').count() === 0);
  expect(id + ': the domain can be attached right there', /Also reachable from the internet through/.test(text) && /radarr\.example\.org/.test(text));
  expect(id + ': upstream\'s fields are not shown', !/Result Target Preview|Use Path Prefix|Container Protocol/.test(text));
  expect(id + ': nothing is cut off sideways', await sideways(page) <= 0 && await cut(page, dialog) <= 0, `${await sideways(page)} / ${await cut(page, dialog)}`);
  await page.screenshot({ path: `shots/${tag}-guide-new-${id}.png` });

  // a click beside the dialog does not close it
  await page.mouse.click(4, 4);
  await page.waitForTimeout(500);
  expect(id + ': a click beside the form keeps it open', await dialog.isVisible());

  // another port, typed: the form warns; all settings carry the same values
  await chip(dialog, 'Another port').click();
  await dialog.locator('input[placeholder="8080"]').fill('1234');
  await page.waitForTimeout(400);
  text = await dialog.innerText();
  expect(id + ': a port the app does not declare is said to be one', /does not declare port 1234/.test(text));
  await dialog.getByRole('button', { name: 'All settings' }).click();
  await page.waitForTimeout(1500);
  text = await dialog.innerText();
  expect(id + ': all settings are upstream\'s form', /Use Path Prefix/.test(text) && /Advanced Settings/.test(text));
  expect(id + ': with the same target', await dialog.locator('input#Target').inputValue() === 'http://radarr:1234', await dialog.locator('input#Target').inputValue().catch(() => '?'));
  expect(id + ': and the host hint under the host', /receives its requests under the name radarr\.local/.test(text));
  await page.screenshot({ path: `shots/${tag}-guide-all-${id}.png` });
  await dialog.getByRole('button', { name: 'Back to the simple view' }).click();
  await page.waitForTimeout(800);
  expect(id + ': back in the simple view the typed port is kept', await dialog.locator('input[placeholder="8080"]').inputValue() === '1234');
  await chip(dialog, '7878').click();

  // who may open it: one choice
  await chip(dialog, 'People signed in to Cosmos').click();
  await page.waitForTimeout(900);
  await dialog.getByRole('button', { name: 'Confirm' }).click();
  await page.waitForTimeout(6000);
  let made = (await routes(ctx)).find((r) => r.Name === 'radarr');
  expect(id + ': the URL is created', !!made, JSON.stringify(made || {}).slice(0, 200));
  if (made) {
    expect(id + ': to the app\'s port', made.Target === 'http://radarr:7878' && made.Mode === 'SERVAPP', made.Target);
    expect(id + ': at the proposed address', made.Host === 'radarr.local' && made.UseHost === true, made.Host);
    expect(id + ': for people signed in', made.AuthEnabled === true && !made.AdminOnly);
    expect(id + ': with upstream\'s defaults', made.SmartShield && made.SmartShield.Enabled === true && made.Timeout === 14400000 && made.ThrottlePerMinute === 10000 && made.BlockCommonBots === true);
    expect(id + ': and an honest description', made.Description === 'radarr, on the local network', made.Description);
  }

  // ---- B. a second URL of the same app gets a name of its own
  await go(page, '/cosmos-ui/servapps');
  await page.locator('.MuiPaper-root', { hasText: 'linuxserver/radarr' }).last().locator('.MuiChip-root', { hasText: /^New$/ }).click();
  await page.waitForTimeout(2500);
  text = await page.locator('[role=dialog]').last().innerText();
  expect(id + ': a second URL gets a free name', /Saved as "radarr-2"/.test(text), (text.match(/Saved as "[^"]*"/) || [''])[0]);
  await page.keyboard.press('Escape');
  await page.waitForTimeout(600);
  expect(id + ': Escape closes the form', await page.locator('[role=dialog]').count() === 0);

  // ---- C. jellyfin: its own HTTPS port switches the scheme
  await page.locator('.MuiPaper-root', { hasText: 'jellyfin/jellyfin' }).last().locator('.MuiChip-root', { hasText: /^New$/ }).click();
  await page.waitForTimeout(2500);
  const jf = page.locator('[role=dialog]').last();
  expect(id + ': jellyfin: 8096 of its four ports', await picked(jf, 'Target') === '8096', await picked(jf, 'Target'));
  await chip(jf, '443').click();
  await page.waitForTimeout(500);
  expect(id + ': picking its HTTPS port proposes https', await picked(jf, 'guide-scheme') === 'https');
  await chip(jf, '8096').click();
  await page.waitForTimeout(400);
  expect(id + ': and back to http with the plain port', await picked(jf, 'guide-scheme') === 'http');
  await page.keyboard.press('Escape');
  await page.waitForTimeout(600);

  // ---- D. URLs > Create: from nothing
  note(`--- ${id}: URLs > Create`);
  await go(page, '/cosmos-ui/config-url');
  await page.getByRole('button', { name: /Create/ }).first().click();
  await page.waitForTimeout(2500);
  const fresh = page.locator('[role=dialog]').last();
  expect(id + ': it starts by asking what the address opens', await picked(fresh, 'guide-mode') === 'An app on this server');
  await fresh.locator('#guide-app').click();
  await page.getByRole('option', { name: /sonarr/ }).click();
  await page.waitForTimeout(1200);
  text = await fresh.innerText();
  expect(id + ': picking the app proposes its address', await fresh.locator('#Host').inputValue() === 'sonarr.local', await fresh.locator('#Host').inputValue());
  expect(id + ': its port', await picked(fresh, 'Target') === '8989', await picked(fresh, 'Target'));
  expect(id + ': and its name', /Saved as "sonarr"/.test(text), (text.match(/Saved as "[^"]*"/) || [''])[0]);
  await page.screenshot({ path: `shots/${tag}-guide-create-${id}.png` });
  // a pasted link becomes the name it contains
  await fresh.locator('#Host').fill('https://Sonarr.Example.org/');
  await fresh.locator('#Host').blur();
  await page.waitForTimeout(500);
  expect(id + ': a pasted link is reduced to the name', await fresh.locator('#Host').inputValue() === 'sonarr.example.org', await fresh.locator('#Host').inputValue());
  // an address without a dot: the error says what to type
  await fresh.locator('#Host').fill('sonarr');
  await fresh.locator('#Host').blur();
  await page.waitForTimeout(600);
  text = await fresh.innerText();
  expect(id + ': an address without a dot is refused in plain words', /Write the whole name with a dot/.test(text));
  // another device
  await chip(fresh, 'Another device or address').click();
  await page.waitForTimeout(500);
  await fresh.locator('#Host').fill('router.local');
  await fresh.locator('#Target').fill('https://192.168.1.1');
  await page.waitForTimeout(700);
  text = await fresh.innerText();
  expect(id + ': another device: its certificate can be accepted', /Accept its certificate without checking it/.test(text));
  expect(id + ': and the URL is named after the address', /Saved as "router"/.test(text), (text.match(/Saved as "[^"]*"/) || [''])[0]);
  await chip(fresh, 'Something else').click();
  await page.waitForTimeout(1200);
  text = await fresh.innerText();
  expect(id + ': "something else" opens all settings', /Use Path Prefix/.test(text) && /Mode/.test(text));
  await page.keyboard.press('Escape');
  await page.waitForTimeout(600);

  // ---- E. the URL's page: the same view, and the two tabs no longer undo each other
  note(`--- ${id}: the URL's page`);
  if (made) {
    const tab = async (title) => {
      const t = page.getByRole('tab', { name: title, exact: true });
      if (await t.count() && await t.first().isVisible()) await t.first().click();
      else { await page.locator('.MuiSelect-select').first().click(); await page.getByRole('option', { name: title, exact: true }).click(); }
      await page.waitForTimeout(1800);
    };
    await go(page, '/cosmos-ui/config-url/radarr');
    await tab('Setup');
    const form = page.locator('form').first();
    expect(id + ': Setup opens in the simple view', await form.locator('#Host').inputValue() === 'radarr.local' && await picked(form, 'Target') === '7878' && await picked(form, 'guide-access') === 'People signed in to Cosmos',
      `${await form.locator('#Host').inputValue().catch(() => '?')} ${await picked(form, 'Target')} ${await picked(form, 'guide-access')}`);
    expect(id + ': Setup: no sideways scroll', await sideways(page) <= 0, await sideways(page));
    await page.screenshot({ path: `shots/${tag}-guide-setup-${id}.png`, fullPage: true });
    await chip(form, 'Cosmos administrators only').click();
    await page.waitForTimeout(500);
    await page.getByRole('button', { name: /^Save$/ }).first().click();
    await page.waitForTimeout(6000);
    made = (await routes(ctx)).find((r) => r.Name === 'radarr');
    expect(id + ': "administrators only" is saved as both switches', made.AuthEnabled === true && made.AdminOnly === true, `${made.AuthEnabled} ${made.AdminOnly}`);
    // the Security tab now knows what Setup saved, and saving it keeps it
    await tab('Security');
    const admin = page.locator('input[name=AdminOnly]');
    expect(id + ': the Security tab shows what Setup saved', await admin.isChecked());
    await page.locator('input[name=BlockAPIAbuse]').check();
    await page.getByRole('button', { name: /^Save$/ }).first().click();
    await page.waitForTimeout(6000);
    made = (await routes(ctx)).find((r) => r.Name === 'radarr');
    expect(id + ': saving Security keeps it', made.AdminOnly === true && made.BlockAPIAbuse === true, `${made.AdminOnly} ${made.BlockAPIAbuse}`);
    // and Setup knows what Security saved
    await tab('Setup');
    await page.locator('form').first().locator('#Host').fill('films.local');
    await page.getByRole('button', { name: /^Save$/ }).first().click();
    await page.waitForTimeout(6000);
    made = (await routes(ctx)).find((r) => r.Name === 'radarr');
    expect(id + ': saving Setup keeps what Security saved', made.Host === 'films.local' && made.BlockAPIAbuse === true && made.AdminOnly === true, `${made.Host} ${made.BlockAPIAbuse} ${made.AdminOnly}`);
    expect(id + ': clean up', await dropRoute(ctx, 'radarr') === 200);
  }
  // (a save makes the server restart its listener; a notification poll that is under way just then
  // fails, which WebKit reports as an error of the page: not this form's)
  const own = [...new Set(errors)].filter((e) => !/\/cosmos\/api\/notifications/.test(e));
  expect(id + ': no script errors', own.length === 0, own.join(' | '));
  await browser.close();
}
note(failures.length ? 'FAILURES: ' + failures.length + '\n' + failures.join('\n') : 'ALL OK');

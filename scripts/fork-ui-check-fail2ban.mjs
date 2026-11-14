// Fork addition: checks the Fail2ban panel (client/src/pages/security/fail2ban.jsx)
// in headless Chromium, Firefox and WebKit against a throwaway Cosmos
// instance, never the production one. How to bring an instance up is in
// CLAUDE.md ("Checking the UI in a browser").
//
// The panel shows what a Fail2ban on the instance's own machine says, so
// this check is run in phases, with the machine changed in between by whoever
// runs it (a container of its own: Debian with fail2ban and nftables,
// --cap-add NET_ADMIN, set up with fork/fail2ban/install.sh):
//
//   PHASE=active        Fail2ban runs with the fork's jails and has banned
//                       BANNED (an address, in the jail JAIL). The browser's
//                       own address is not on the never-ban list. Checks the
//                       state, the jails, the ban, the warning; lifts the
//                       ban; adds the browser's address to the list; changes
//                       a threshold; tries values that must be refused.
//   PHASE=stopped       Fail2ban is installed and stopped.
//   PHASE=notinstalled  there is no fail2ban-client.
//
// BASE=http://<container>[:port]; screenshots go to ./shots.
import { chromium, firefox, webkit } from 'playwright';

const base = process.env.BASE || 'http://cosmos-smoke:18080';
const phase = process.env.PHASE || 'active';
const banned = process.env.BANNED || '';
const password = process.env.PASSWORD || 'Test-Passw0rd!x';
const url = base + '/cosmos-ui/config-url/fail2ban';
const errors = [];
const failures = [];
const note = (...a) => console.log(...a);
const expect = (ok, what) => { note(ok ? '  ok  ' : '  FAIL', what); if (!ok) failures.push(what); };

const open = async (type, name, viewport) => {
  const browser = await type.launch();
  const ctx = await browser.newContext({ viewport, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => errors.push(`${name} PAGEERROR: ${e.message.slice(0, 300)}`));
  page.on('console', (m) => { if (m.type() === 'error') errors.push(`${name} console: ${m.text().slice(0, 200)}`); });
  let r = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  const login = r.status();
  r = await ctx.request.post(base + '/cosmos/api/sudo', { data: { password } });
  note(name, 'login', login, 'sudo', r.status());
  return { browser, ctx, page };
};

const visit = async (page, name, shot) => {
  await page.goto(url, { waitUntil: 'domcontentloaded' });
  await page.getByRole('heading', { name: 'Fail2ban' }).first().waitFor({ timeout: 20000 });
  await page.waitForTimeout(2500);
  await page.screenshot({ path: `shots/${name}-${shot}.png`, fullPage: true });
  return page.locator('body').innerText();
};

// nothing may be wider than the screen
const noSidewaysScroll = async (page, name) => {
  const widths = await page.evaluate(() => ({ scroll: document.documentElement.scrollWidth, client: document.documentElement.clientWidth }));
  expect(widths.scroll <= widths.client + 1, `${name}: no sideways scroll (${widths.scroll} <= ${widths.client})`);
};

const engines = [['chromium', chromium], ['firefox', firefox], ['webkit', webkit]];

if (phase === 'stopped' || phase === 'notinstalled') {
  for (const [name, type] of engines) {
    for (const [size, viewport] of [['desktop', { width: 1366, height: 900 }], ['phone', { width: 390, height: 844 }]]) {
      const { browser, page } = await open(type, name, viewport);
      const text = await visit(page, name, `${phase}-${size}`);
      if (phase === 'stopped') {
        expect(text.includes('Fail2ban is installed but not running.'), `${name} ${size}: says it is installed but not running`);
        expect(text.includes('systemctl start fail2ban'), `${name} ${size}: says how to start it`);
        expect(!text.includes('Banned addresses'), `${name} ${size}: no list of bans while it is stopped`);
      } else {
        expect(text.includes('Fail2ban is not installed on this server.'), `${name} ${size}: says it is not installed`);
        expect(text.includes('sh fork/fail2ban/install.sh'), `${name} ${size}: says what to run`);
        expect(!text.includes('Jails and their thresholds'), `${name} ${size}: no settings without Fail2ban`);
      }
      expect(!text.includes('can be banned'), `${name} ${size}: no warning about the own address when nothing bans`);
      await noSidewaysScroll(page, `${name} ${size}`);
      await browser.close();
    }
  }
} else {
  // --- desktop Chromium: the whole walk
  {
    const { browser, ctx, page } = await open(chromium, 'chromium', { width: 1366, height: 900 });
    let text = await visit(page, 'chromium', 'active-desktop');
    expect(text.includes('Fail2ban is active.'), 'says Fail2ban is active');
    expect(/\d of 6 jails are running/.test(text), 'counts the running jails');
    expect(text.includes('1 address is banned now.'), 'counts the banned addresses');
    for (const jail of ['Logins', 'Probes', 'Scans', 'Abuse', 'SSH', 'Repeat offenders']) {
      expect(text.includes(jail), `lists the jail "${jail}"`);
    }
    expect(text.includes('Requests for paths only scanners ask for'), 'says what a jail watches');
    expect(/\d+ tries in \d+ (minutes|hours?|days?): banned for \d+ (minutes|hours?|days?)/.test(text), 'says the thresholds in words');
    const status = (await (await ctx.request.get(base + '/cosmos/api/fail2ban')).json()).data;
    note('  browser address', status.clientIP, 'never banned:', status.clientIgnored);
    expect(status.clientIgnored === false, 'the browser\'s address is not on the never-ban list for this check');
    expect(text.includes(`Your own address, ${status.clientIP}, can be banned.`), 'warns that the own address can be banned');

    // the ban, its time left, and lifting it
    expect(banned !== '' && text.includes(banned), `shows the banned address ${banned}`);
    const row = page.locator('tr', { hasText: banned }).first();
    const rowText = await row.innerText();
    expect(/\d+ (min|h|d)/.test(rowText) || rowText.includes('less than a minute'), `shows the time left (${rowText.replace(/\s+/g, ' ').slice(0, 90)})`);
    const unbanButton = row.locator('button', { hasText: 'Unban' }); // its accessible name is the tooltip's text
    await unbanButton.hover();
    await page.waitForTimeout(900);
    const tip = await page.locator('[role=tooltip]').innerText().catch(() => '');
    expect(tip.includes('Lift this ban now'), 'the Unban button has a tooltip');
    await page.screenshot({ path: 'shots/chromium-active-unban-tooltip.png' });
    await unbanButton.click();
    await page.waitForTimeout(600);
    const dialog = await page.locator('[role=dialog]').innerText().catch(() => '');
    expect(dialog.includes(`Lift the ban of ${banned}`), `asks before lifting the ban (${dialog.replace(/\s+/g, ' ').slice(0, 110)})`);
    await page.screenshot({ path: 'shots/chromium-active-unban-confirm.png' });
    await page.locator('[role=dialog]').getByRole('button', { name: 'Confirm' }).click();
    await page.waitForTimeout(3500);
    await page.screenshot({ path: 'shots/chromium-active-after-unban.png', fullPage: true });
    text = await page.locator('body').innerText();
    const after = (await (await ctx.request.get(base + '/cosmos/api/fail2ban')).json()).data;
    expect(!after.bans.some((ban) => ban.ip === banned), 'the ban is gone on the server');
    expect(await page.locator('tr', { hasText: banned }).count() === 0, 'the ban is gone from the list');
    expect(text.includes('Lift the Fail2ban ban'), 'the command and its outcome were announced');
    expect(text.includes('No address is banned at the moment.'), 'says that nothing is banned');

    // values that must be refused: nothing is sent
    const tries = page.locator('#fail2ban-cosmos-auth-maxRetry');
    const before = await tries.inputValue();
    await tries.fill('99');
    await page.locator('button', { hasText: /^Save$/ }).click();
    await page.waitForTimeout(800);
    text = await page.locator('body').innerText();
    expect(text.includes('Between 1 and 10.'), 'refuses 99 tries with the bounds');
    await page.locator('#fail2ban-new-entry').fill('home.example.org');
    await page.getByRole('button', { name: 'Add to the list' }).click();
    await page.waitForTimeout(400);
    text = await page.locator('body').innerText();
    expect(text.includes('Enter an address (203.0.113.7) or a network (203.0.113.0/24).'), 'refuses a host name in the never-ban list');
    await page.locator('#fail2ban-new-entry').fill('0.0.0.0/0');
    await page.getByRole('button', { name: 'Add to the list' }).click();
    await page.waitForTimeout(400);
    text = await page.locator('body').innerText();
    expect(text.includes('This covers every address'), 'refuses a network that covers everything');
    await page.screenshot({ path: 'shots/chromium-active-refused.png', fullPage: true });
    await page.getByRole('button', { name: 'Undo my changes' }).click();
    await page.waitForTimeout(400);
    expect(await tries.inputValue() === before, 'undoing puts the form back');

    // a threshold is changed and saved
    const wanted = before === '4' ? '5' : '4';
    await tries.fill(wanted);
    await page.locator('#fail2ban-new-entry').fill('198.51.100.0/24');
    await page.getByRole('button', { name: 'Add to the list' }).click();
    await page.waitForTimeout(300);
    text = await page.locator('body').innerText();
    expect(text.includes('is not on this list: once saved, it can be banned.'), 'warns before saving a list without the own address');
    await page.locator('button', { hasText: /^Save$/ }).click();
    await page.waitForTimeout(2500);
    await page.screenshot({ path: 'shots/chromium-active-save-toast.png' });
    text = await page.locator('body').innerText();
    expect(text.includes('Save the Fail2ban settings'), 'the save and its outcome were announced');
    await page.waitForTimeout(2500);
    text = await page.locator('body').innerText();
    expect(text.includes('Saved. Fail2ban is using the new settings'), 'says the settings were saved');
    const saved = (await (await ctx.request.get(base + '/cosmos/api/fail2ban')).json()).data;
    expect(String(saved.jails.find((jail) => jail.name === 'cosmos-auth').maxRetry) === wanted, `Fail2ban now runs with ${wanted} tries for logins`);
    expect(saved.settings.ignoreIP.includes('198.51.100.0/24'), 'the network is on Fail2ban\'s never-ban list');
    await page.screenshot({ path: 'shots/chromium-active-saved.png', fullPage: true });

    // one click protects the administrator's own address
    await page.getByRole('button', { name: 'Never ban my address' }).click();
    await page.waitForTimeout(5000);
    text = await page.locator('body').innerText();
    const protectedNow = (await (await ctx.request.get(base + '/cosmos/api/fail2ban')).json()).data;
    expect(protectedNow.clientIgnored === true, 'the browser\'s address is now never banned');
    expect(!text.includes('can be banned.'), 'the warning is gone');
    await page.screenshot({ path: 'shots/chromium-active-protected.png', fullPage: true });
    await noSidewaysScroll(page, 'chromium desktop');
    await browser.close();
  }

  // --- the other engines and the phone width: it renders, nothing is cut off
  for (const [name, type] of engines) {
    for (const [size, viewport] of [['desktop', { width: 1366, height: 900 }], ['phone', { width: 390, height: 844 }]]) {
      if (name === 'chromium' && size === 'desktop') continue;
      const { browser, page } = await open(type, name, viewport);
      const text = await visit(page, name, `active-${size}`);
      expect(text.includes('Fail2ban is active.') && text.includes('Jails and their thresholds') && text.includes('Addresses that are never banned'), `${name} ${size}: renders the state, the jails and the list`);
      await noSidewaysScroll(page, `${name} ${size}`);
      // the fields of a jail fit the screen
      const box = await page.locator('#fail2ban-recidive-banTime').boundingBox();
      expect(box && box.x >= 0 && box.x + box.width <= viewport.width, `${name} ${size}: the fields are inside the screen`);
      await browser.close();
    }
  }
}

note('script errors:', errors.length);
errors.forEach((e) => note('  ' + e));
note(failures.length === 0 ? 'ALL CHECKS PASSED' : `${failures.length} CHECK(S) FAILED`);
failures.forEach((f) => note('  FAILED: ' + f));
process.exit(failures.length === 0 ? 0 : 1);

// Fork addition: loads the UI in headless Chromium, Firefox and WebKit against
// a throwaway Cosmos instance, exercises the fork's additions and takes
// screenshots. How to bring the instance up is in CLAUDE.md ("Checking the UI
// in a browser"). Adjust the expectations below to what is being changed.
// BASE=http://<container>:<port> points it at an instance with another name.
import { chromium, firefox, webkit } from 'playwright';
const base = process.env.BASE || 'http://cosmos-smoke:18080';
const errors = [];
const note = (...a) => console.log(...a);

const open = async (type, name, viewport) => {
  const browser = await type.launch();
  const ctx = await browser.newContext({ viewport, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => errors.push(`${name} PAGEERROR: ${e.message.slice(0, 300)}`));
  page.on('console', (m) => { if (m.type() === 'error') errors.push(`${name} console: ${m.text().slice(0, 160)}`); });
  let r = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: 'Test-Passw0rd!x' } });
  const login = r.status();
  r = await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: 'Test-Passw0rd!x' } });
  note(name, 'login', login, 'sudo', r.status());
  return { browser, ctx, page };
};
const visit = async (page, name, path, shot) => {
  await page.goto(base + path, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(3500);
  await page.screenshot({ path: `shots/${name}-${shot}.png` });
  const text = await page.locator('body').innerText();
  note(name, shot, 'rendered', text.length, 'chars');
  return text;
};

// --- desktop Chromium: pages, update block, tooltips, toasts, hostname proposals
{
  const { browser, page } = await open(chromium, 'chromium', { width: 1366, height: 900 });
  await visit(page, 'chromium', '/cosmos-ui/', 'home');
  await visit(page, 'chromium', '/cosmos-ui/servapps', 'servapps');
  let text = await visit(page, 'chromium', '/cosmos-ui/config-general', 'config');
  note('update block present:', (text.match(/Running version: \S+/) || ['NO'])[0], '| maintenance time field:', await page.locator('input[name=MaintenanceTime]').count(), 'value', await page.locator('input[name=MaintenanceTime]').inputValue().catch(() => '?'));

  await page.getByRole('button', { name: 'Check for update now' }).click();
  await page.waitForTimeout(5000);
  text = await page.locator('body').innerText();
  note('after check:', (text.match(/Version [^\n]* is available\.[^\n]*/) || text.match(/You are running the latest[^\n]*/) || text.match(/Could not check[^\n]*/) || ['?'])[0]);
  note('update now enabled:', await page.getByRole('button', { name: /download Update now/ }).isEnabled());
  await page.getByRole('button', { name: 'Check for update now' }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: 'shots/chromium-update-block.png' });
  // the button is disabled while there is nothing newer to install
  if (await page.getByRole('button', { name: /download Update now/ }).isEnabled()) {
    await page.getByRole('button', { name: /download Update now/ }).click();
    await page.waitForTimeout(800);
    await page.screenshot({ path: 'shots/chromium-update-confirm.png' });
    note('confirm dialog:', (await page.locator('[role=dialog]').innerText().catch(() => 'none')).replace(/\n/g, ' | ').slice(0, 260));
    await page.keyboard.press('Escape');
    await page.waitForTimeout(500);
  }

  // tooltip on an icon-only header button
  const headerButtons = page.locator('header button');
  note('header buttons:', await headerButtons.count());
  await headerButtons.first().hover();
  await page.waitForTimeout(900);
  note('tooltip on first header button:', await page.locator('[role=tooltip]').first().innerText().catch(() => 'NONE'));
  await page.screenshot({ path: 'shots/chromium-tooltip.png' });
  for (let i = 1; i < Math.min(await headerButtons.count(), 6); i++) {
    await headerButtons.nth(i).hover(); await page.waitForTimeout(700);
    note('  header button', i, 'tooltip:', await page.locator('[role=tooltip]').last().innerText().catch(() => 'NONE'));
  }

  // a command and its outcome message: change the maintenance time and save
  await page.locator('input[name=MaintenanceTime]').fill('03:30');
  await page.getByRole('button', { name: /^Save$/ }).first().click();
  await page.waitForTimeout(1200);
  await page.screenshot({ path: 'shots/chromium-save-toast.png' });
  note('status area after save:', (await page.locator('[role=status]').innerText().catch(() => 'NONE')).replace(/\n/g, ' | ').slice(0, 200));
  await page.waitForTimeout(4000);

  // home page notice
  text = await visit(page, 'chromium', '/cosmos-ui/', 'home-notice');
  note('home notice:', (text.match(/Version [^\n]* of Cosmos is available[^\n]*/) || ['NONE'])[0]);

  // URL form with hostname proposals
  await visit(page, 'chromium', '/cosmos-ui/config-url', 'urls');
  const create = page.getByRole('button', { name: /new|create/i }).first();
  note('create button:', await create.innerText().catch(() => 'NONE'));
  await create.click().catch(() => {});
  await page.waitForTimeout(1500);
  const nameField = page.locator('input[name=Name]');
  if (await nameField.count()) {
    await nameField.fill('Jellyfin Media');
    const useHost = page.locator('input[name=UseHost]');
    if (await useHost.count() && !(await useHost.isChecked())) await useHost.check();
    await page.waitForTimeout(800);
    text = await page.locator('body').innerText();
    note('proposals:', (text.match(/Suggested addresses[^\n]*\n+[^\n]*(\n+[^\n]*)?/) || ['NONE'])[0].replace(/\n+/g, ' | '));
    const chip = page.locator('.MuiChip-root', { hasText: 'jellyfin-media' }).first();
    if (await chip.count()) { await chip.click(); note('host after click:', await page.locator('input[name=Host]').inputValue()); }
    await page.locator('input[name=Host]').scrollIntoViewIfNeeded();
    await page.screenshot({ path: 'shots/chromium-url-form.png' });
  } else note('URL form not found');
  await browser.close();
}

// --- phone width and the other engines: do the pages render without script errors?
for (const [type, name, viewport] of [[chromium, 'phone', { width: 390, height: 844 }], [firefox, 'firefox', { width: 1280, height: 800 }], [webkit, 'webkit', { width: 1280, height: 800 }]]) {
  const { browser, page } = await open(type, name, viewport);
  await visit(page, name, '/cosmos-ui/', 'home');
  const text = await visit(page, name, '/cosmos-ui/config-general', 'config');
  note(name, 'update block:', /Running version/.test(text), '| horizontal overflow:', await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1));
  await visit(page, name, '/cosmos-ui/servapps', 'servapps');
  await browser.close();
}

// --- the settings forms at desktop and phone width: the rows "folder button + field", what the
// page shows after a save (the server restarts its listener then), the Storage tabs, the alerts
for (const [name, type, viewport] of [
  ['settings-desktop', chromium, { width: 1366, height: 900 }], ['settings-phone', chromium, { width: 390, height: 844 }],
  ['settings-firefox', firefox, { width: 1366, height: 900 }], ['settings-webkit-phone', webkit, { width: 390, height: 844 }],
]) {
  const { browser, page } = await open(type, name, viewport);
  const phone = viewport.width < 900;
  // the tabs of Storage and Monitoring have no address of their own: a list of tabs, or a select on a phone
  // (`shown` is the tab the select shows now: it tells that select from the others on the page)
  const tab = async (shown, index) => {
    if (phone) {
      await page.locator('.MuiSelect-select', { hasText: new RegExp('^' + shown + '$') }).first().click();
      await page.waitForTimeout(300);
      const item = page.locator('[role=listbox] li').nth(index);
      shown = (await item.innerText()).trim();
      await item.click();
    } else await page.locator('[role=tab]').nth(index).click();
    await page.waitForTimeout(3000);
    return shown;
  };
  const overflows = () => page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);

  await visit(page, name, '/cosmos-ui/config-general', 'config');
  // button and input must start at the same height, and the input must end inside its card,
  // with the help text under the field and with a validation error under it
  const row = (id) => page.evaluate((id) => {
    const input = document.getElementById(id).closest('.MuiOutlinedInput-root');
    let parent = input, button = null;
    while (parent && !(button = parent.querySelector('button'))) parent = parent.parentElement;
    const box = input.getBoundingClientRect(), b = button.getBoundingClientRect();
    const card = input.closest('.MuiPaper-root').getBoundingClientRect();
    return `button ${Math.round(b.top - box.top)} px from the input's top, input ${box.right <= card.right ? 'inside' : 'OUTSIDE'} its card`;
  }, id);
  for (const [id, wrong] of [['BackupOutputDir', 'relative/path'], ['Snapshots_Destination', 'relative/path']]) {
    const field = page.locator('#' + id);
    const before = await field.inputValue();
    note(name, id, '| as loaded:', await row(id));
    await field.fill(wrong); await field.blur(); await page.waitForTimeout(500);
    note(name, id, '| with an error under it:', await row(id));
    await field.evaluate((el) => el.scrollIntoView({ block: 'center' }));
    await page.screenshot({ path: `shots/${name}-${id}.png` });
    await field.fill(before); await field.blur(); await page.waitForTimeout(300);
  }
  // a save: the page must end up on what was saved, not on a refused reload
  const keep = page.locator('#Snapshots_KeepRecent');
  const kept = await keep.inputValue();
  await keep.fill(kept === '9' ? '8' : '9');
  await page.getByRole('button', { name: /^Save$/ }).first().click();
  await page.waitForTimeout(9000);
  note(name, 'after a save, "settings were changed" still shown:', /The settings above were changed/.test(await page.locator('body').innerText()), '(expected: false)');
  await keep.fill(kept);
  await page.getByRole('button', { name: /^Save$/ }).first().click();
  await page.waitForTimeout(9000);
  note(name, 'general settings overflow sideways:', await overflows());

  // Storage: every tab renders something (the list of disks itself needs real disks)
  await visit(page, name, '/cosmos-ui/storage', 'storage');
  for (let i = 1, shown = 'Disks'; i < 7; i++) {
    shown = await tab(shown, i);
    const panel = page.locator('[role=tabpanel]:not([hidden])');
    note(name, 'storage tab', i, 'rendered', (await panel.innerText()).length, 'chars | overflow:', await overflows());
    await page.screenshot({ path: `shots/${name}-storage-${i}.png` });
  }

  // Monitoring > Alerts: the list, the editor of the first alert, a save
  await visit(page, name, '/cosmos-ui/monitoring', 'monitoring');
  await page.waitForTimeout(3000);
  await tab('Resources', 3);
  const rows = page.locator('[role=tabpanel]:not([hidden]) tbody tr');
  note(name, 'alerts listed:', await rows.count());
  await page.screenshot({ path: `shots/${name}-alerts.png` });
  if (await rows.count()) {
    await rows.first().locator('td').nth(1).click();
    await page.waitForTimeout(1500);
    const dialog = page.locator('[role=dialog]');
    await page.screenshot({ path: `shots/${name}-alert-editor.png` });
    note(name, 'alert editor scrolls sideways:', await dialog.locator('.MuiDialogContent-root').evaluate((el) => el.scrollWidth > el.clientWidth + 1), '(expected: false)');
    await dialog.getByRole('button', { name: /^Save$/ }).click();
    await page.waitForTimeout(9000);
    note(name, 'after saving the alert: dialog closed', (await page.locator('[role=dialog]').count()) === 0, '| list usable again:', await rows.first().locator('input[type=checkbox]').isEnabled().catch(() => false));
  }
  await browser.close();
}

const real = errors.filter((e) => !/Failed to load resource|status of 5\d\d|status of 40[134]|net::ERR|WebSocket|favicon/i.test(e));
note('\nscript errors:', real.length);
[...new Set(real)].slice(0, 25).forEach((e) => note('  ', e));
note('ignored resource/API errors:', errors.length - real.length);

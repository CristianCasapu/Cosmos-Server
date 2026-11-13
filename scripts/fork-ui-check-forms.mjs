// Fork addition: like fork-ui-check-apps.mjs, for the forms that guide and
// check: host ports that are taken (new container form, a container's Network
// tab, compose import), schedules (cron job and SnapRAID dialogs) and the
// Volumes page, and the partitions offered by the "New mount" dialog. The test instance must be connected to a Docker of its own
// (see CLAUDE.md, "Checking the UI in a browser") in which, as the
// expectations below assume:
//   - a running container "web1" publishes 8080/tcp and uses the volumes
//     "web1-data" and "photos" (a volume bound to the folder /srv/photos)
//   - a running container "dns1" publishes 5353/udp and 127.0.0.1:8081/tcp and
//     uses the volume "shared" read-only
//   - a stopped container "db1" uses "shared" too
//   - a volume "orphan" is used by nothing
// ONLY=ports,cron (ports, compose, cron, snapraid, volumes, mount) runs some of the checks.
// BASE=http://<container>:<port> points it at the instance, which must have
// HTTPS switched off and must not find itself in the Docker it is given (Cosmos
// then counts as installed on the server itself). Its HTTP port is taken from
// BASE and written into its settings with HTTPS_PORT (default: 363 above,
// 18080 -> 18443) before the checks: the form reads Cosmos' own ports from the
// settings, and the test instance gets its ports from COSMOS_HTTP_PORT.
// "Internal server error" on the Storage page is expected: a container has no disks to list.
import { chromium, firefox, webkit } from 'playwright';
const base = process.env.BASE || 'http://cosmos-smoke:18080';
const ownPort = new URL(base).port || '80';
const httpsPort = process.env.HTTPS_PORT || String(Number(ownPort) + 363);
const only = (process.env.ONLY || '').split(',').filter(Boolean);
const note = (...a) => console.log(...a);
const errors = [];
const failures = [];
const expect = (what, ok, detail) => {
  note(ok ? '  ok  ' : '  FAIL', what, detail === undefined ? '' : '| ' + detail);
  if (!ok) failures.push(what);
};

const open = async (type, name, viewport) => {
  const browser = await type.launch();
  const ctx = await browser.newContext({ viewport });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => errors.push(`${name} ${page.url().replace(base, '')}: ${e.message.slice(0, 200)}`));
  let r = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: 'Test-Passw0rd!x' } });
  const login = r.status();
  r = await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: 'Test-Passw0rd!x' } });
  note(name, 'login', login, 'sudo', r.status());
  return { browser, ctx, page };
};
const go = async (page, path) => {
  await page.goto(base + path, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(3500);
};
// a tab of a tabbed page: a tab on a wide screen, an entry of a select on a narrow one
const openTab = async (page, title) => {
  const tab = page.getByRole('tab', { name: title, exact: true });
  if (await tab.count() && await tab.first().isVisible()) {
    await tab.first().click();
  } else {
    await page.locator('.MuiSelect-select').first().click();
    await page.getByRole('option', { name: title, exact: true }).click();
  }
  await page.waitForTimeout(1500);
};
const sideways = (page) => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
const hints = async (page) => (await page.locator('form .MuiFormHelperText-root').allInnerTexts()).map((s) => s.trim()).filter(Boolean);
const shot = (page, name) => page.screenshot({ path: `shots/forms-${name}.png` });

// ---- host ports
const ports = async (page, name) => {
  note(`--- ${name}: host ports in the new container form`);
  await go(page, '/cosmos-ui/servapps/new-service');
  const next = page.getByRole('button', { name: /Next/ }).first();
  await page.locator('#name').fill('forms-check');
  await page.locator('#image').fill('busybox');
  await page.locator('#user').click();
  await page.waitForTimeout(800);
  await next.click(); await page.waitForTimeout(800);
  await next.click(); await page.waitForTimeout(1500);

  const add = page.getByRole('button', { name: 'Add', exact: true });
  const hostPort = (i) => page.getByLabel('Host port').nth(i);
  const containerPort = (i) => page.getByLabel('Container Port').nth(i);
  await add.click();
  await hostPort(0).fill('8080'); await containerPort(0).fill('80');
  await page.waitForTimeout(600);
  let said = await hints(page);
  expect('a port another app publishes is named', said.some((s) => s.includes('Port 8080 (TCP) is already used by the app "web1"')), said.join(' / '));
  expect('"Next" is held', await next.isDisabled());
  await hostPort(0).scrollIntoViewIfNeeded();
  await shot(page, `${name}-ports-taken`);
  expect('no sideways scroll', (await sideways(page)) <= 0, 'overflow ' + (await sideways(page)) + 'px');

  await hostPort(0).fill('8090'); await page.waitForTimeout(500);
  expect('a free port says nothing and frees "Next"', (await hints(page)).length === 0 && await next.isEnabled(), (await hints(page)).join(' / '));

  await hostPort(0).fill(ownPort); await page.waitForTimeout(500);
  said = await hints(page);
  expect('Cosmos\' own port is named', said.some((s) => s.includes(`Port ${ownPort} is used by Cosmos itself (its HTTP port)`)), said.join(' / '));
  expect('"Next" is held for it', await next.isDisabled());

  await hostPort(0).fill('http'); await page.waitForTimeout(500);
  said = await hints(page);
  expect('something that is not a port is refused', said.some((s) => s.startsWith('Enter a port number from 1 to 65535')) && await next.isDisabled(), said.join(' / '));

  // 8081 is held on 127.0.0.1 only, 5353 on UDP only; the same port twice
  await hostPort(0).fill('8081');
  await add.click(); await hostPort(1).fill('5353'); await containerPort(1).fill('53');
  await add.click(); await hostPort(2).fill('9000'); await containerPort(2).fill('90');
  await add.click(); await hostPort(3).fill('9000'); await containerPort(3).fill('91');
  await page.waitForTimeout(600);
  said = await hints(page);
  expect('a port held on one address conflicts with "every address"', said.some((s) => s.includes('Port 8081 (TCP) is already used by the app "dns1"')), said.join(' / '));
  expect('a port held on UDP does not conflict on TCP', !said.some((s) => s.includes('5353')));
  expect('a port asked for twice is said on both rows', said.filter((s) => s.includes('Port 9000 (TCP) is in this list twice')).length === 2);
  await hostPort(3).scrollIntoViewIfNeeded();
  await shot(page, `${name}-ports-several`);
  expect('no sideways scroll with several rows', (await sideways(page)) <= 0, 'overflow ' + (await sideways(page)) + 'px');

  // the HTTPS port of a Cosmos that has HTTPS switched off is only a hint: it does not hold "Next"
  await hostPort(0).fill('8091'); await hostPort(3).fill('9001');
  const https = httpsPort;
  await hostPort(2).fill(https); await page.waitForTimeout(600);
  said = await hints(page);
  expect('a port Cosmos may use is a hint that does not hold "Next"', said.some((s) => s.includes(`Port ${https} is the HTTPS port in Cosmos' settings`)) && await next.isEnabled(), said.join(' / '));
  await next.click(); await page.waitForTimeout(800);
  await next.click(); await page.waitForTimeout(1500);
  const warning = await page.locator('.MuiAlert-standardWarning').allInnerTexts();
  expect('the review step repeats what is left', warning.some((s) => s.includes(`Port ${https} is the HTTPS port`)), warning.join(' / ').replace(/\n/g, ' '));
  await page.evaluate(() => window.scrollTo(0, 0));
  await shot(page, `${name}-ports-review`);
  expect('no sideways scroll on the review step', (await sideways(page)) <= 0, 'overflow ' + (await sideways(page)) + 'px');

  note(`--- ${name}: host ports on a container's Network tab`);
  await go(page, '/cosmos-ui/servapps/containers/web1');
  await openTab(page, 'Network');
  const update = page.getByRole('button', { name: 'Update Ports' });
  expect('the container\'s own port is not a conflict', (await hostPort(0).inputValue()) === '8080' && (await hints(page)).length === 0 && await update.isEnabled(), (await hints(page)).join(' / '));
  await hostPort(0).fill('8081'); await page.waitForTimeout(600);
  said = await hints(page);
  expect('changing it to a taken port is named and "Update Ports" is held', said.some((s) => s.includes('already used by the app "dns1"')) && await update.isDisabled(), said.join(' / '));
  await hostPort(0).scrollIntoViewIfNeeded();
  await shot(page, `${name}-ports-network-tab`);
  expect('no sideways scroll on the Network tab', (await sideways(page)) <= 0, 'overflow ' + (await sideways(page)) + 'px');
  await page.getByLabel('Host IP (optional)').first().fill('192.168.77.7'); await page.waitForTimeout(600);
  expect('on another address the port is free', (await hints(page)).length === 0 && await update.isEnabled(), (await hints(page)).join(' / '));
  // nothing is saved: the page is left without pressing the button
};

const compose = async (page, name) => {
  note(`--- ${name}: host ports in an imported compose file`);
  await go(page, '/cosmos-ui/servapps');
  await page.getByRole('button', { name: /Import Compose File/i }).first().click();
  await page.waitForTimeout(800);
  await page.locator('[role=dialog] textarea').first().fill([
    'services:', '  alpha:', '    image: busybox', '    ports:', '      - "8080:80"', '      - "9005:90"',
    '  beta:', '    image: busybox', '    ports:', '      - "9005:91"', '      - "5353:53/udp"', '',
  ].join('\n'));
  await page.waitForTimeout(800);
  await page.locator('[role=dialog]').getByRole('button', { name: /Next/ }).click();
  await page.waitForTimeout(2500);
  const warning = (await page.locator('[role=dialog] .MuiAlert-standardWarning').allInnerTexts()).join(' / ').replace(/\n/g, ' | ');
  expect('taken ports are listed before the service is created',
    warning.includes('Port 8080 (TCP) is already used by the app "web1"') && warning.includes('Port 5353 (UDP) is already used by the app "dns1"') && warning.includes('Port 9005 (TCP) is also asked for by'), warning);
  await shot(page, `${name}-ports-compose`);
  expect('no sideways scroll in the dialog', (await sideways(page)) <= 0, 'overflow ' + (await sideways(page)) + 'px');
  await page.keyboard.press('Escape');
};

// ---- schedules
const cron = async (page, name, save) => {
  note(`--- ${name}: the schedule of a job`);
  await go(page, '/cosmos-ui/cron');
  await page.getByRole('button', { name: /New Job/ }).first().click();
  await page.waitForTimeout(1200);
  const dialog = page.locator('[role=dialog]');
  const field = dialog.locator('#Crontab');
  const said = async () => (await dialog.locator('.MuiFormHelperText-root').allInnerTexts()).map((s) => s.trim()).filter(Boolean).join(' / ');
  const chips = (await dialog.locator('.MuiChip-clickable').allInnerTexts()).map((s) => s.trim());
  expect('schedules to pick from', chips.includes('Every day at 03:00') && chips.includes('Every Sunday at 03:00') && chips.length >= 5, chips.join(', '));

  await dialog.locator('.MuiChip-clickable', { hasText: 'Every Sunday at 03:00' }).click();
  await page.waitForTimeout(400);
  expect('a click fills the field', (await field.inputValue()) === '0 0 3 * * 0', await field.inputValue());
  let text = await said();
  expect('it says what the schedule means and when it runs next', /Every Sunday at 03:00\. Next run: .*Sunday.*03:00.*\(server time\)/.test(text), text);

  await field.fill('0 3 * * *'); await page.waitForTimeout(400);
  text = await said();
  expect('five parts are refused with the way out', text.includes('This has 5 parts; Cosmos needs 6'), text);
  await dialog.locator('#Name').fill('forms-check-' + name);
  await dialog.locator('#Command').fill('echo hello');
  await dialog.getByRole('button', { name: 'Submit' }).click();
  await page.waitForTimeout(1500);
  const stored = async () => Object.keys(((await (await page.context().request.get(base + '/cosmos/api/config')).json()).data.CRON) || {});
  expect('a refused schedule is not saved', (await dialog.count()) === 1 && !(await stored()).includes('forms-check-' + name), 'jobs: ' + (await stored()).join(','));
  await shot(page, `${name}-cron-refused`);

  await field.fill('0 0 25 * * *'); await page.waitForTimeout(300);
  text = await said();
  expect('an hour that does not exist is named', text.includes('Part 3 (hour) goes from 0 to 23: "25" does not fit.'), text);
  await field.fill('0 */15 9-17 * * MON-FRI'); await page.waitForTimeout(300);
  text = await said();
  expect('a typed schedule is put in words', text.includes('Every day from Monday to Friday, every 15 minutes between 09:00 and 17:59. Next run:'), text);
  await field.fill('0 0 3 * * *'); await page.waitForTimeout(300);
  await shot(page, `${name}-cron-dialog`);
  expect('no sideways scroll', (await sideways(page)) <= 0, 'overflow ' + (await sideways(page)) + 'px');
  const box = await dialog.boundingBox();
  const wide = await page.evaluate(() => window.innerWidth);
  expect('the dialog fits the screen', box.x >= 0 && box.x + box.width <= wide, `x ${Math.round(box.x)} width ${Math.round(box.width)} of ${wide}`);

  if (!save) {
    await page.keyboard.press('Escape');
    return;
  }
  await dialog.getByRole('button', { name: 'Submit' }).click();
  await page.waitForTimeout(6000);
  expect('a valid schedule is saved', (await stored()).includes('forms-check-' + name), 'jobs: ' + (await stored()).join(','));
  await go(page, '/cosmos-ui/cron');
  const row = await page.locator('tr', { hasText: 'forms-check-' + name }).first().innerText().catch(() => 'NO ROW');
  expect('the list says it in the same words', row.includes('Every day at 03:00'), row.replace(/\s+/g, ' ').slice(0, 160));
  await shot(page, `${name}-cron-list`);
};

const snapraid = async (page, name) => {
  note(`--- ${name}: the schedules of the SnapRAID dialog`);
  await go(page, '/cosmos-ui/storage');
  await openTab(page, 'Parity');
  const create = page.getByRole('button', { name: /New Parity Disks/i }).first();
  if (!(await create.count()) || await create.isDisabled()) {
    note('  the dialog cannot be opened on this instance (button missing or disabled): not checked');
    return;
  }
  await create.click(); await page.waitForTimeout(1500);
  const dialog = page.locator('[role=dialog]');
  const text = (await dialog.locator('.MuiFormHelperText-root').allInnerTexts()).join(' / ');
  expect('both default schedules are put in words',
    text.includes('Every day at 02:00. Next run:') && text.includes('Every 2 days (the 1st, 3rd, 5th and so on of each month) at 04:00. Next run:'), text);
  await dialog.locator('#scrubCronTab').fill('0 0 4 * * 7'); await page.waitForTimeout(400);
  const refused = (await dialog.locator('.MuiFormHelperText-root.Mui-error').allInnerTexts()).join(' / ');
  expect('a weekday 7 is refused as the server refuses it', refused.includes('goes from 0 (Sunday) to 6 (Saturday)'), refused);
  await dialog.locator('#scrubCronTab').scrollIntoViewIfNeeded();
  await shot(page, `${name}-snapraid-dialog`);
  expect('no sideways scroll', (await sideways(page)) <= 0, 'overflow ' + (await sideways(page)) + 'px');
  await page.keyboard.press('Escape');
};

// ---- volumes
const volumes = async (page, name) => {
  note(`--- ${name}: the Volumes page`);
  await go(page, '/cosmos-ui/servapps');
  await openTab(page, 'Volumes');
  await page.waitForTimeout(1500);
  const row = async (volume) => (await page.locator('tr', { hasText: volume }).first().innerText().catch(() => 'NO ROW')).replace(/\s+/g, ' ');
  let text = await row('web1-data');
  expect('a volume says where it is and who uses it', /Folder on the server: \/\S+\/volumes\/web1-data\/_data/.test(text) && text.includes('web1'), text);
  text = await row('shared');
  expect('a volume used by two apps names both', text.includes('db1') && text.includes('dns1'), text);
  text = await row('orphan');
  expect('an unused volume says so', text.includes('Not used by any app'), text);
  text = await row('photos');
  expect('a volume that points to a folder shows that folder', text.includes('Folder on the server: /srv/photos'), text);
  await shot(page, `${name}-volumes`);
  expect('no sideways scroll', (await sideways(page)) <= 0, 'overflow ' + (await sideways(page)) + 'px');

  const chip = page.locator('tr', { hasText: 'shared' }).first().locator('.MuiChip-root:visible', { hasText: 'dns1' }).first();
  await chip.hover(); await page.waitForTimeout(700);
  const tip = await page.locator('[role=tooltip]').last().innerText().catch(() => 'NONE');
  expect('the app\'s chip says where the app sees the volume', tip.includes('Seen by the app as /shared (read only)'), tip);
  await chip.click(); await page.waitForTimeout(2500);
  expect('and leads to the app', page.url().endsWith('/cosmos-ui/servapps/containers/dns1'), page.url().replace(base, ''));
};

// ---- mounting a disk
// The test instance has no disks of its own to list, so the answer of the disks API is
// played to the page; nothing is mounted: the dialog is closed without pressing "Mount".
const mount = async (page, name) => {
  note(`--- ${name}: partitions to pick from in the "New mount" dialog`);
  const disks = [
    { name: '/dev/sda', type: 'disk', size: 500107862016, model: 'System disk', children: [
      { name: '/dev/sda1', type: 'part', size: 498000000000, fstype: 'ext4', mountpoint: '/' },
      { name: '/dev/sda2', type: 'part', size: 2000000000, fstype: 'swap' },
    ] },
    { name: '/dev/sdb', type: 'disk', size: 4000787030016, model: 'WDC WD40EFRX', children: [
      { name: '/dev/sdb1', type: 'part', size: 4000785964544, fstype: 'ext4' },
    ] },
    { name: '/dev/sdc', type: 'disk', size: 2000398934016, model: 'ST2000DM008', fstype: 'xfs' },
    { name: '/dev/sdd', type: 'disk', size: 2000398934016, model: 'Not formatted' },
  ];
  await page.route('**/cosmos/api/disks', (route) => route.fulfill({ json: { status: 'OK', data: disks } }));
  await go(page, '/cosmos-ui/storage');
  await openTab(page, 'Mounts');
  await page.getByRole('button', { name: /New Mount/ }).first().click();
  await page.waitForTimeout(1500);
  const dialog = page.locator('[role=dialog]');
  const chips = (await dialog.locator('.MuiChip-clickable').allInnerTexts()).map((s) => s.trim());
  expect('the partitions that are not mounted are offered, and only those',
    chips.length === 2 && chips[0] === '/dev/sdb1 · ext4 · 4 TB' && chips[1] === '/dev/sdc · xfs · 2 TB', chips.join(' , '));
  await dialog.locator('.MuiChip-clickable').first().click();
  await page.waitForTimeout(400);
  expect('a click fills the device and proposes the folder',
    (await dialog.locator('#device').inputValue()) === '/dev/sdb1' && (await dialog.locator('#path').inputValue()) === '/mnt/sdb1',
    (await dialog.locator('#device').inputValue()) + ' -> ' + (await dialog.locator('#path').inputValue()));
  await dialog.locator('.MuiChip-clickable').nth(1).click();
  await page.waitForTimeout(400);
  expect('the proposed folder follows another choice',
    (await dialog.locator('#path').inputValue()) === '/mnt/sdc', await dialog.locator('#path').inputValue());
  await dialog.locator('#path').fill('/mnt/films');
  await dialog.locator('.MuiChip-clickable').first().click();
  await page.waitForTimeout(400);
  expect('a folder typed by hand is kept', (await dialog.locator('#path').inputValue()) === '/mnt/films', await dialog.locator('#path').inputValue());
  await shot(page, `${name}-mount-dialog`);
  expect('no sideways scroll', (await sideways(page)) <= 0, 'overflow ' + (await sideways(page)) + 'px');
  const box = await dialog.boundingBox();
  const wide = await page.evaluate(() => window.innerWidth);
  expect('the dialog fits the screen', box.x >= 0 && box.x + box.width <= wide, `x ${Math.round(box.x)} width ${Math.round(box.width)} of ${wide}`);
  await page.keyboard.press('Escape');
  await page.unroute('**/cosmos/api/disks');
};

const sections = { ports, compose, cron, snapraid, volumes, mount };
const run = async (type, engine, viewport, width, list, save) => {
  const { browser, page } = await open(type, `${engine}-${width}`, viewport);
  for (const section of list) {
    if (only.length && !only.includes(section)) continue;
    try {
      await sections[section](page, `${engine}-${width}`, save);
    } catch (e) {
      expect(`${engine}-${width} ${section} ran to its end`, false, String(e.message).split('\n')[0].slice(0, 300));
      await shot(page, `${engine}-${width}-${section}-STOPPED`).catch(() => {});
    }
  }
  await browser.close();
};

const desktop = { width: 1366, height: 900 };
const phone = { width: 390, height: 844 };

// the ports the instance really listens on, into its settings (see the top of this file)
{
  const { browser, ctx, page } = await open(chromium, 'setup', desktop);
  const config = (await (await ctx.request.get(base + '/cosmos/api/config')).json()).data;
  if (config.HTTPConfig.HTTPPort !== ownPort || config.HTTPConfig.HTTPSPort !== httpsPort) {
    config.HTTPConfig.HTTPPort = ownPort;
    config.HTTPConfig.HTTPSPort = httpsPort;
    const r = await ctx.request.put(base + '/cosmos/api/config', { data: config });
    note('ports written to the settings:', ownPort, httpsPort, '->', r.status());
    await page.waitForTimeout(12000);
  }
  await browser.close();
}
await run(chromium, 'chromium', desktop, 'desktop', ['ports', 'compose', 'cron', 'snapraid', 'volumes', 'mount'], true);
await run(chromium, 'chromium', phone, 'phone', ['ports', 'compose', 'cron', 'snapraid', 'volumes', 'mount'], false);
await run(firefox, 'firefox', desktop, 'desktop', ['ports', 'cron', 'volumes', 'mount'], false);
await run(webkit, 'webkit', desktop, 'desktop', ['ports', 'cron', 'volumes', 'mount'], false);
await run(webkit, 'webkit', phone, 'phone', ['ports', 'cron', 'volumes', 'mount'], false);

note('script errors:', errors.length ? '\n  ' + errors.join('\n  ') : 'none');
note(failures.length ? `FAILED (${failures.length}): ` + failures.join(' ; ') : 'all checks passed');
process.exit(failures.length ? 1 : 0);

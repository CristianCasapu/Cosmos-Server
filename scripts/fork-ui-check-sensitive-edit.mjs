// Fork addition (plan 40, after the security review): the tick for access
// to the server on a container's edit tabs, in headless Chromium, Firefox and
// WebKit at 1366x900 and 393x851, against a throwaway Cosmos with a Docker of
// its own. Never the production instance.
//
// Every edit of a container goes through POST /api/servapps/{id}/update,
// which now refuses access to the server the container did not have
// (src/docker/fork_sensitive_edit.go); the panel asks tick by tick
// (components/sensitiveEditDialog.jsx). This checks that:
//   1. a container that already has sensitive things (the Docker socket, a
//      device) and the ordinary ones (a bind under the data disk, a published
//      port) is edited on Docker, Network and Storage without a dialog;
//   2. a new device (Docker tab), a system folder (Storage tab) and the host's
//      network (Network tab) open the dialog with the item in words; Cancel
//      changes nothing; ticked and accepted, it is saved; the next edit is not
//      asked again;
//   3. two new items at once, one added and one removed in the same edit, the
//      dialog on a phone, the keyboard (focus inside, Escape cancels), and the
//      password confirmation due at the same time;
//   4. the terminal's "Enable TTY" and the overview's icon: no dialog;
//   5. an API token without the permission gets the ordinary refusal, one with
//      it the API answer (409, DS010), never a dialog.
//
// Setup: a test Cosmos at SERVER_IP (fresh, run in a loop, COSMOS_HTTP_PORT=2052,
// COSMOS_HTTPS_PORT=2053, DOCKER_HOST=tcp://DIND:2375), a docker:29-dind at
// DIND with nginx:1 pulled and a folder of another disk at /mnt/data in both.
// The script creates its containers itself (media, jelly, quiet) before
// each engine and size. ENGINES, SIZES (d,m), MODES (light,dark: dark only
// pictures the dialog). Screenshots go to ./shots.
// It also runs against the setup of scripts/fork-ui-check-edit-tabs.sh (Cosmos inside its Docker in
// Docker, nginx:1 there a stand-in built from busybox): SERVER_IP=198.51.100.5 DIND=http://198.51.100.5:2375.
import { chromium, firefox, webkit, request } from 'playwright';

const ip = process.env.SERVER_IP || '198.51.100.10';
const dind = process.env.DIND || 'http://198.51.100.5:2375';
const plain = `http://${ip}:2052`;
const base = `https://${ip}:2053`;
const password = 'Test-Passw0rd!x';
const engines = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const sizes = (process.env.SIZES || 'd,m').split(',');
const modes = (process.env.MODES || 'light,dark').split(',');

const note = (...a) => console.log(...a);
const failures = [];
const expect = (ok, what) => { note(ok ? '  ok  ' : '  FAIL', what); if (!ok) failures.push(what); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const api = await request.newContext({ ignoreHTTPSErrors: true });
const until = async (what, check, seconds = 60) => {
  for (let i = 0; i < seconds; i++) {
    try { if (await check()) return true; } catch (e) { /* reloading */ }
    await sleep(1000);
  }
  expect(false, 'timed out: ' + what);
  return false;
};
const serverUp = () => until('the server answers', async () => (await api.get(base + '/cosmos/api/status')).status() < 500, 90);
const inspect = async (name) => {
  const answer = await api.get(`${dind}/containers/${name}/json`);
  return answer.status() === 200 ? answer.json() : null;
};
const devicesOf = async (name) => ((await inspect(name)).HostConfig.Devices || []).map((d) => d.PathOnHost).sort().join(',');
const bindsOf = async (name) => ((await inspect(name)).Mounts || []).filter((m) => m.Type === 'bind').map((m) => m.Source).sort().join(',');

// the containers, made again before each run
const SPECS = {
  media: { Image: 'nginx:1', ExposedPorts: { '80/tcp': {} }, HostConfig: {
    Binds: ['/mnt/data/media:/media', '/var/run/docker.sock:/var/run/docker.sock:ro'],
    PortBindings: { '80/tcp': [{ HostPort: '18096' }] },
    Devices: [{ PathOnHost: '/dev/null', PathInContainer: '/dev/fakenull', CgroupPermissions: 'rwm' }] } },
  jelly: { Image: 'nginx:1', ExposedPorts: { '80/tcp': {} }, HostConfig: {
    Binds: ['/mnt/data/jelly/config:/config', '/mnt/data/jelly/media:/media:ro'],
    PortBindings: { '80/tcp': [{ HostPort: '18097' }] } } },
  quiet: { Image: 'nginx:1', HostConfig: {} },
};
const remake = async () => {
  for (const [name, spec] of Object.entries(SPECS)) {
    await api.delete(`${dind}/containers/${name}?force=1&v=1`);
    const made = await api.post(`${dind}/containers/create?name=${name}`, { data: spec });
    if (made.status() !== 201) note('create', name, made.status(), await made.text());
    await api.post(`${dind}/containers/${name}/start`);
  }
  // leftovers of interrupted edits from an earlier run
  for (const c of await (await api.get(`${dind}/containers/json?all=1`)).json()) {
    if ((c.Names || []).some((n) => /cosmos-old/.test(n))) await api.delete(`${dind}/containers/${c.Id}?force=1&v=1`);
  }
};

// --- the wizard, once
{
  let fresh = false;
  try {
    const answer = await api.get(plain + '/cosmos/api/status', { timeout: 5000, maxRedirects: 0 });
    fresh = answer.status() === 200 && (await answer.json()).status === 'OK';
  } catch (e) { /* set up already */ }
  if (fresh) {
    const step = (data) => api.post(plain + '/cosmos/api/newInstall', { data }).then((r) => r.status()).catch(() => 'exit');
    note('wizard:', await step({ step: '2', mongodbMode: 'Create' }),
      await step({ step: '3', httpsCertificateMode: 'SELFSIGNED', hostname: ip, allowHTTPLocalIPAccess: true }),
      await step({ step: '4', nickname: 'admin', password, email: 'admin@example.org' }), await step({ step: '5' }));
    await sleep(4000);
  }
  await serverUp();
}

// --- 5. API tokens (once)
{
  await remake();
  const ctx = await request.newContext({ ignoreHTTPSErrors: true });
  await ctx.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  await ctx.post(base + '/cosmos/api/sudo', { data: { password } });
  const token = async (name, body) => {
    const r = await ctx.post(base + '/cosmos/api/api-tokens', { data: { name: name + '-' + Date.now().toString(36), ...body } });
    const j = await r.json().catch(() => ({}));
    if (r.status() !== 200) note('token', name, r.status(), JSON.stringify(j).slice(0, 200));
    return (j.data && (j.data.token || j.data.Token)) || j.token || '';
  };
  const reader = await token('readonly-test', { readOnly: true, description: 'test', expiryDays: 1 });
  const writer = await token('resources-test', { permissions: [21], description: 'test', expiryDays: 1 });
  await serverUp();
  const as = (tok) => api.post(base + '/cosmos/api/servapps/media/update', { headers: { Authorization: 'Bearer ' + tok }, data: { devices: ['/dev/null:/dev/fakenull', '/dev/zero:/dev/fakezero'] } });
  let r = await as(reader);
  let j = await r.json().catch(() => ({}));
  expect(reader && r.status() >= 400 && r.status() !== 409 && j.code !== 'DS010', `a token without the permission: the ordinary refusal (${r.status()} ${j.code})`);
  r = await as(writer);
  j = await r.json().catch(() => ({}));
  expect(writer && r.status() === 409 && j.code === 'DS010' && /\/dev\/zero/.test(j.message), `a token with it: the API's own answer, 409 DS010 (${r.status()} ${j.code})`);
  expect((await devicesOf('media')) === '/dev/null', 'nothing was changed by either');
  await ctx.dispose();
}

const open = async (type, name, viewport, mode, withSudo = true) => {
  const browser = await type.launch();
  const ctx = await browser.newContext({ viewport, ignoreHTTPSErrors: true, colorScheme: mode, serviceWorkers: 'block' });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => {
    if (/Failed to fetch|Load failed|NetworkError/.test(e.message)) return; // the server reloads after an edit
    failures.push(`${name} PAGEERROR: ${(e.stack || e.message).slice(0, 400)}`);
  });
  await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  if (withSudo) await ctx.request.post(base + '/cosmos/api/sudo', { data: { password } });
  return { browser, ctx, page };
};
const shot = (page, name, what) => page.screenshot({ path: `shots/${name}-${what}.png` });
const go = async (page, container, tab) => {
  await page.goto(`${base}/cosmos-ui/servapps/containers/${container}`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(2500);
  if (tab) {
    const t = page.getByRole('tab', { name: tab, exact: true });
    if (await t.count() && await t.first().isVisible()) await t.first().click();
    else { await page.locator('.MuiSelect-select').first().click(); await page.getByRole('option', { name: tab, exact: true }).click(); }
    await page.waitForTimeout(1500);
  }
};
const dialog = (page) => page.locator('[data-sensitive-edit]');
const noDialog = async (page, ms = 2500) => { await page.waitForTimeout(ms); return (await dialog(page).count()) === 0; };
const dialogFits = async (page) => page.locator('[data-sensitive-edit] .MuiDialog-paper').evaluate((p) => {
  const r = p.getBoundingClientRect();
  const boxes = [...p.querySelectorAll('input[type=checkbox], button')].map((b) => b.getBoundingClientRect());
  return r.left >= -1 && r.right <= window.innerWidth + 1 && p.scrollWidth <= p.clientWidth + 1
    && boxes.every((b) => b.width > 0 && b.left >= -1 && b.right <= window.innerWidth + 1);
});
const addDevice = async (page, host, inside) => {
  const before = await page.locator('input[name^="devices."][name$=".key"]').count();
  await page.locator('xpath=//*[normalize-space()="Devices"]/following::button[@aria-label="Add" or normalize-space()="Add"][1]').click();
  await page.locator(`input[name="devices.${before}.key"]`).fill(host);
  await page.locator(`input[name="devices.${before}.value"]`).fill(inside);
};
const updateContainer = (page) => page.getByRole('button', { name: /Update Container/ }).click();

for (const engine of engines) {
  for (const size of sizes) {
    for (const mode of modes) {
      const viewport = size === 'm' ? { width: 393, height: 851 } : { width: 1366, height: 900 };
      const name = `${engine}-${size === 'm' ? 393 : 1366}-${mode}`;
      await remake();
      await serverUp();
      const { browser, page } = await open({ chromium, firefox, webkit }[engine], name, viewport, mode);
      note(`\n== ${name}`);
      try {
        if (mode === 'dark') {
          // the dialog in dark mode: picture and fit only
          await go(page, 'media', 'Docker');
          await addDevice(page, '/dev/zero', '/dev/fakezero');
          await updateContainer(page);
          await dialog(page).waitFor({ timeout: 20000 });
          await page.waitForTimeout(400);
          expect(await dialogFits(page), `${name}: the dialog fits, its ticks and buttons on the screen`);
          await shot(page, name, 'dialog');
          await page.getByRole('button', { name: 'Do not change it' }).click();
          await browser.close();
          continue;
        }

        // 1. no dialog for what a container already has
        for (const container of ['jelly', 'media']) {
          await go(page, container, 'Docker');
          const label = page.locator('xpath=//*[normalize-space()="Labels"]/following::button[@aria-label="Add" or normalize-space()="Add"][1]');
          const labels = await page.locator('input[name^="labels."][name$=".key"]').count();
          await label.click();
          await page.locator(`input[name="labels.${labels}.key"]`).fill('test-note');
          await page.locator(`input[name="labels.${labels}.value"]`).fill(name);
          await updateContainer(page);
          expect(await noDialog(page), `${name}: ${container}, Docker tab, a label: no dialog`);
          await until(`${container} has the label`, async () => ((await inspect(container)).Config.Labels || {})['test-note'] === name);
          await serverUp();
          await go(page, container, 'Network');
          // the DNS is a choice since plan 47 (I2c): typed through its door
          await page.locator('#fork-dns [role=radio]').filter({ hasText: 'Type it myself' }).click();
          await page.locator('#fork-dns-other').fill('192.0.2.53');
          await page.getByRole('button', { name: 'Update Ports' }).click();
          expect(await noDialog(page), `${name}: ${container}, Network tab, DNS: no dialog`);
          await until(`${container} has the DNS`, async () => ((await inspect(container)).HostConfig.Dns || []).includes('192.0.2.53'));
          await serverUp();
          await go(page, container, 'Storage');
          const binds = await bindsOf(container);
          await page.getByRole('button', { name: 'Update Volumes' }).click();
          expect(await noDialog(page), `${name}: ${container}, Storage tab, saved as it is: no dialog`);
          await page.waitForTimeout(3000);
          await serverUp();
          expect((await bindsOf(container)) === binds, `${name}: ${container} keeps its folders (${binds})`);
        }
        if (size === 'd') await shot(page, name, '1-saved-without-dialog');

        // 2. a new device: Cancel changes nothing, accepted it is saved, the next edit is not asked again
        await go(page, 'media', 'Docker');
        await addDevice(page, '/dev/zero', '/dev/fakezero');
        await updateContainer(page);
        await dialog(page).waitFor({ timeout: 20000 });
        const said = await dialog(page).innerText();
        expect(/device \/dev\/zero/.test(said) && !/\/dev\/null/.test(said) && !/docker\.sock/.test(said), `${name}: the dialog names the new device only`);
        expect(await dialogFits(page), `${name}: the dialog fits, its ticks and buttons on the screen`);
        expect(!(await page.getByText(/: failed/).count()), `${name}: no "failed" message while the dialog asks`);
        await shot(page, name, '2-dialog-device');
        expect(await page.locator('[data-sensitive-edit-accept]').isDisabled(), `${name}: Accept waits for the tick`);
        await page.getByRole('button', { name: 'Do not change it' }).click();
        await page.waitForTimeout(1500);
        expect((await devicesOf('media')) === '/dev/null', `${name}: Cancel changes nothing`);
        const refusal = (await page.locator('.MuiFormHelperText-root.Mui-error, [role=alert]').allInnerTexts()).join(' ');
        expect(/Nothing was changed/.test(refusal), `${name}: the form says nothing was changed`);
        await shot(page, name, '2-cancelled');
        // like any refused save, the button waits for a change: the path is typed again
        await page.locator('input[name="devices.1.value"]').fill('/dev/fakezero2');
        await updateContainer(page);
        await dialog(page).waitFor({ timeout: 20000 });
        await dialog(page).locator('input[type=checkbox]').first().check();
        await page.locator('[data-sensitive-edit-accept]').click();
        await until('media has /dev/zero', async () => (await devicesOf('media')) === '/dev/null,/dev/zero');
        await serverUp();
        await go(page, 'media', 'Docker');
        const labels = await page.locator('input[name^="labels."][name$=".key"]').count();
        await page.locator('xpath=//*[normalize-space()="Labels"]/following::button[@aria-label="Add" or normalize-space()="Add"][1]').click();
        await page.locator(`input[name="labels.${labels}.key"]`).fill('again');
        await page.locator(`input[name="labels.${labels}.value"]`).fill('1');
        await updateContainer(page);
        expect(await noDialog(page), `${name}: the next edit is not asked again`);
        await until('media has the label', async () => ((await inspect('media')).Config.Labels || {}).again === '1');
        await serverUp();

        // 3. two new at once, one removed in the same edit
        await go(page, 'media', 'Docker');
        const nullRow = page.locator('input[name="devices.0.key"]');
        expect((await nullRow.inputValue()) === '/dev/null', `${name}: the first device row is /dev/null`);
        await nullRow.locator('xpath=ancestor::div[.//input[@name="devices.0.value"]][1]').getByRole('button', { name: 'Delete' }).click();
        await addDevice(page, '/dev/urandom', '/dev/fakeurandom');
        await addDevice(page, '/dev/full', '/dev/fakefull');
        await updateContainer(page);
        await dialog(page).waitFor({ timeout: 20000 });
        const two = await dialog(page).locator('input[type=checkbox]').count();
        const twoSaid = await dialog(page).innerText();
        expect(two === 2 && /\/dev\/urandom/.test(twoSaid) && /\/dev\/full/.test(twoSaid) && !/\/dev\/null/.test(twoSaid), `${name}: two new items, the removed one not asked (${two})`);
        await dialog(page).locator('input[type=checkbox]').first().check();
        expect(await page.locator('[data-sensitive-edit-accept]').isDisabled(), `${name}: one tick of two is not enough`);
        await dialog(page).locator('input[type=checkbox]').nth(1).check();
        await shot(page, name, '3-dialog-two');
        await page.locator('[data-sensitive-edit-accept]').click();
        await until('media has the two and not /dev/null', async () => (await devicesOf('media')) === '/dev/full,/dev/urandom,/dev/zero');
        await serverUp();

        // keyboard: focus inside, Escape cancels, ticks and Accept by keys
        await go(page, 'media', 'Docker');
        await addDevice(page, '/dev/random', '/dev/fakerandom');
        await updateContainer(page);
        await dialog(page).waitFor({ timeout: 20000 });
        await page.waitForTimeout(400);
        expect(await page.evaluate(() => !!document.activeElement && !!document.activeElement.closest('[data-sensitive-edit]')), `${name}: the focus is inside the dialog`);
        await page.keyboard.press('Escape');
        await page.waitForTimeout(1500);
        expect((await dialog(page).count()) === 0 && !(await devicesOf('media')).includes('/dev/random'), `${name}: Escape cancels`);
        const last = (await page.locator('input[name^="devices."][name$=".value"]').count()) - 1;
        await page.locator(`input[name="devices.${last}.value"]`).fill('/dev/fakerandom2');
        await updateContainer(page);
        await dialog(page).waitFor({ timeout: 20000 });
        await page.waitForTimeout(400);
        let ticked = false;
        for (let i = 0; i < 8 && !ticked; i++) {
          await page.keyboard.press('Tab');
          if (await page.evaluate(() => document.activeElement && document.activeElement.type === 'checkbox')) {
            await page.keyboard.press('Space');
            ticked = true;
          }
        }
        let accepted = false;
        for (let i = 0; i < 8 && ticked && !accepted; i++) {
          await page.keyboard.press('Tab');
          if (await page.evaluate(() => !!document.activeElement && !!document.activeElement.closest('[data-sensitive-edit-accept]'))) {
            await page.keyboard.press('Enter');
            accepted = true;
          }
        }
        expect(ticked && accepted, `${name}: ticked and accepted with the keyboard`);
        await until('media has /dev/random', async () => (await devicesOf('media')).includes('/dev/random'));
        await serverUp();

        // the Storage tab: a system folder
        await go(page, 'media', 'Storage');
        const rows = await page.locator('input[name$="].Target"]').count();
        await page.getByRole('button', { name: 'New Mount Point' }).click();
        await page.locator('[aria-label="Type of mount"]').nth(rows).click();
        await page.getByRole('option', { name: 'Bind' }).click();
        await page.locator(`input[name="volumes[${rows}].Source"]`).fill('/etc');
        await page.locator(`input[name="volumes[${rows}].Target"]`).fill('/hostetc');
        await page.getByRole('button', { name: 'Update Volumes' }).click();
        await dialog(page).waitFor({ timeout: 20000 });
        expect(/server folder \/etc/.test(await dialog(page).innerText()), `${name}: Storage tab: the dialog names /etc`);
        await dialog(page).locator('input[type=checkbox]').first().check();
        await page.locator('[data-sensitive-edit-accept]').click();
        await until('media has /etc', async () => (await bindsOf('media')).split(',').includes('/etc'));
        await serverUp();

        // the Network tab: the host's network
        await go(page, 'quiet', 'Network');
        await page.locator('#fork-network-mode [role=radio]').filter({ hasText: "server's own network" }).click(); // a choice since plan 47 (I2c)
        await page.getByRole('button', { name: 'Update Ports' }).click();
        await dialog(page).waitFor({ timeout: 20000 });
        expect(/server's own network/.test(await dialog(page).innerText()), `${name}: Network tab: the dialog names the host's network`);
        await dialog(page).locator('input[type=checkbox]').first().check();
        await page.locator('[data-sensitive-edit-accept]').click();
        await until('quiet is on the host network', async () => (await inspect('quiet')).HostConfig.NetworkMode === 'host');
        await serverUp();

        // 4. the terminal's switch and the overview's icon
        await go(page, 'jelly', 'Terminal');
        await page.getByRole('button', { name: 'Enable TTY' }).click();
        expect(await noDialog(page), `${name}: Terminal, Enable TTY: no dialog`);
        await until('jelly has a TTY', async () => (await inspect('jelly')).Config.Tty === true);
        await serverUp();
        await go(page, 'jelly', 'Overview');
        const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==', 'base64');
        await page.locator('input[type=file]').first().setInputFiles({ name: 'icon.png', mimeType: 'image/png', buffer: png });
        expect(await noDialog(page, 4000), `${name}: Overview, the icon: no dialog`);
        await until('jelly has the icon', async () => !!((await inspect('jelly')).Config.Labels || {})['cosmos-icon']);
        await serverUp();
        await browser.close();

        // 3. the password confirmation ending under the page: the page still believes it is there (as in the
        // second before its timer redraws), the server answers HTTP008 for the edit
        const late = await open({ chromium, firefox, webkit }[engine], name + '-late', viewport, mode, true);
        await go(late.page, 'media', 'Docker');
        await addDevice(late.page, '/dev/tty', '/dev/faketty');
        const other = await request.newContext({ ignoreHTTPSErrors: true });
        await other.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
        const plainJwt = (await other.storageState()).cookies.find((c) => c.name === 'jwttoken');
        await late.ctx.addCookies([{ ...plainJwt, domain: ip, path: '/' }]);
        await other.dispose();
        await updateContainer(late.page);
        const prompt = late.page.locator('input[name=password]');
        await prompt.waitFor({ timeout: 20000 });
        expect((await dialog(late.page).count()) === 0, `${name}: the password prompt comes first, no tick dialog`);
        expect(!(await devicesOf('media')).includes('/dev/tty'), `${name}: nothing saved without the password`);
        await shot(late.page, name, '3-password-first');
        await prompt.fill(password);
        await late.page.getByRole('button', { name: 'Submit' }).click();
        await late.page.waitForTimeout(2500);
        const entered = await late.page.locator('input[name^="devices."][name$=".key"]').evaluateAll((els) => els.map((e) => e.value));
        expect(entered.includes('/dev/tty'), `${name}: what was entered is still there after the password`);
        if (!(await late.page.getByRole('button', { name: /Update Container/ }).isEnabled())) {
          // like after any refused save, the button waits for a change
          const last = entered.length - 1;
          await late.page.locator(`input[name="devices.${last}.value"]`).fill('/dev/faketty2');
        }
        await updateContainer(late.page);
        await dialog(late.page).waitFor({ timeout: 20000 });
        await dialog(late.page).locator('input[type=checkbox]').first().check();
        await late.page.locator('[data-sensitive-edit-accept]').click();
        await until('media has /dev/tty', async () => (await devicesOf('media')).includes('/dev/tty'));
        expect((await devicesOf('media')).includes('/dev/tty'), `${name}: after the password and the tick the edit is saved`);
        await late.browser.close();
      } catch (e) {
        failures.push(`${name}: ${e.message.split('\n')[0]}`);
        await shot(page, name, 'zz-error').catch(() => {});
        await browser.close().catch(() => {});
      }
    }
  }
}

await api.dispose();
note(failures.length ? `\n${failures.length} FAILURES:\n- ` + failures.join('\n- ') : '\nall checks passed');
process.exit(failures.length ? 1 : 0);

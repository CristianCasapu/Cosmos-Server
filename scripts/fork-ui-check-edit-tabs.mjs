// Fork addition (plan 47, I2c): a container's edit tabs (Docker, Network, Storage) and what they
// propose, in headless Chromium, Firefox and WebKit at 1366x900 and 393x851, against a throwaway
// Cosmos that runs inside its own Docker in Docker. Never the production instance.
//
// Setup: sh scripts/fork-ui-check-edit-tabs.sh up (its header says what it makes), then
// sh scripts/fork-ui-check-edit-tabs.sh check [ENGINES=chromium,firefox,webkit SIZES=d,m MODES=light].
// The script makes its containers itself in the test Docker before each engine and size:
//   plain  example/plainapp:1 (USER 1000:1000, declares /data and port 9000), nothing mounted or published
//   lsapp  lscr.io/linuxserver/fakeapp:1, PUID/PGID 1000, /config on the data disk (owned 1000:1000),
//          port 18080 published on every address
//   lsmis  the same image, PUID/PGID 1000, its /config owned 1001:1001 (the classic mismatch)
//   odd    odd but valid settings that saved before: a user number, 127.0.0.1 and a UDP port, DNS of
//          its own, a folder outside the data disk read-only, a named volume, a tmpfs, a device,
//          on-failure:3, a network of its own
// What is checked, per engine and size: each tab opens with its proposals; "Type it myself" opens a
// field for the address, DNS, network mode, user and limits; a port proposed from the image on the
// home-network address is saved and listens there only (tried from inside the test Docker and from
// the browser's network); the declared folder's proposal is saved, the app (user 1000) writes in it
// and the data is still there after the container is made again; the PUID/PGID mismatch is said and
// its fix is saved; the server's (stand-in) render node is offered and needs its tick; harmless edits
// save; the odd container saves unchanged from each tab; no sideways scroll at phone width.
import { chromium, firefox, webkit, request } from 'playwright';

const ip = process.env.SERVER_IP || '198.51.100.5';
const lanIp = process.env.LAN_IP || '192.168.78.5';
const dind = process.env.DIND || `http://${ip}:2375`;
const plain = `http://${ip}:2052`;
const base = `https://${ip}:2053`;
const password = process.env.PASSWORD || 'Test-Passw0rd!x';
const engines = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const sizes = (process.env.SIZES || 'd,m').split(',');
const modes = (process.env.MODES || 'light').split(',');

const note = (...a) => console.log(...a);
const failures = [];
const expect = (ok, what) => { note(ok ? '  ok  ' : '  FAIL', what); if (!ok) failures.push(what); return ok; };
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
// a command in a container of the test Docker (exec), its output
const run = async (name, cmd) => {
  const made = await (await api.post(`${dind}/containers/${name}/exec`, { data: { Cmd: cmd, AttachStdout: true, AttachStderr: true } })).json();
  const out = await api.post(`${dind}/exec/${made.Id}/start`, { data: { Detach: false, Tty: true } });
  return (await out.text()).replace(/[^\x20-\x7e\n]/g, '').trim();
};
// a one-off container of the test Docker, its output
const once = async (name, spec) => {
  await api.delete(`${dind}/containers/${name}?force=1&v=1`);
  const made = await api.post(`${dind}/containers/create?name=${name}`, { data: { Image: 'busybox:1.36', ...spec } });
  if (made.status() !== 201) return 'create failed: ' + (await made.text());
  await api.post(`${dind}/containers/${name}/start`);
  await api.post(`${dind}/containers/${name}/wait`, { timeout: 60000 });
  const logs = await (await api.get(`${dind}/containers/${name}/logs?stdout=1&stderr=1`)).body();
  await api.delete(`${dind}/containers/${name}?force=1&v=1`);
  return logs.toString('latin1').replace(/[^\x20-\x7e\n]/g, '').trim();
};

const SPECS = {
  plain: { Image: 'example/plainapp:1', HostConfig: { RestartPolicy: { Name: 'unless-stopped' } } },
  lsapp: { Image: 'lscr.io/linuxserver/fakeapp:1', Env: ['PUID=1000', 'PGID=1000', 'TZ=Etc/UTC'], ExposedPorts: { '8080/tcp': {} }, HostConfig: {
    Binds: ['/mnt/data/lsapp/config:/config'], PortBindings: { '8080/tcp': [{ HostPort: '18080' }] }, RestartPolicy: { Name: 'unless-stopped' } } },
  lsmis: { Image: 'lscr.io/linuxserver/fakeapp:1', Env: ['PUID=1000', 'PGID=1000'], HostConfig: { Binds: ['/mnt/data/lsmis/config:/config'] } },
  odd: { Image: 'example/plainapp:1', User: '65534:65534', Env: ['ODD=1'], ExposedPorts: { '9000/tcp': {}, '9001/udp': {} }, HostConfig: {
    Binds: ['/srv/odd:/odd:ro', 'oddvol:/vol'], Mounts: [{ Type: 'tmpfs', Target: '/scratch' }],
    PortBindings: { '9000/tcp': [{ HostIp: '127.0.0.1', HostPort: '18200' }], '9001/udp': [{ HostPort: '18201' }] },
    Dns: ['192.0.2.53'], Devices: [{ PathOnHost: '/dev/null', PathInContainer: '/dev/fakenull', CgroupPermissions: 'rwm' }],
    RestartPolicy: { Name: 'on-failure', MaximumRetryCount: 3 }, NetworkMode: 'oddnet' } },
};
const remake = async () => {
  for (const c of await (await api.get(`${dind}/containers/json?all=1`)).json()) {
    if ((c.Names || []).some((n) => /cosmos-old/.test(n))) await api.delete(`${dind}/containers/${c.Id}?force=1&v=1`);
  }
  for (const name of Object.keys(SPECS)) await api.delete(`${dind}/containers/${name}?force=1&v=1`);
  await api.post(`${dind}/networks/create`, { data: { Name: 'oddnet' } });
  // the folders: the app's own (1000), the mismatch (1001), one outside the data disk; the proposal's folders removed
  const made = await once('edit-tabs-folders', { Cmd: ['sh', '-c', 'rm -rf /d/cosmos-storage /d/plain /d/extra* && mkdir -p /d/lsapp/config /d/lsmis/config /s/odd && chown 1000:1000 /d/lsapp/config && chown 1001:1001 /d/lsmis/config && echo made'],
    HostConfig: { Binds: ['/mnt/data:/d', '/srv:/s'] } });
  if (made !== 'made') note('folders:', made);
  for (const [name, spec] of Object.entries(SPECS)) {
    const created = await api.post(`${dind}/containers/create?name=${name}`, { data: spec });
    if (created.status() !== 201) note('create', name, created.status(), await created.text());
    await api.post(`${dind}/containers/${name}/start`);
  }
};

// what "unchanged" compares: the settings the three tabs send
const shape = (c) => {
  const h = c.HostConfig;
  const mounts = [...(h.Mounts || []).map((m) => `${m.Type}|${m.Source || ''}|${m.Target}|${!!m.ReadOnly}`),
    ...(h.Binds || []).map((b) => { const [s, t, mode] = b.split(':'); return `${s.startsWith('/') ? 'bind' : 'volume'}|${s}|${t}|${/(^|,)ro(,|$)/.test(mode || '')}`; })].sort();
  const ports = Object.entries(h.PortBindings || {}).flatMap(([p, list]) => (list || []).map((b) => `${p}|${['', '0.0.0.0', '::'].includes(b.HostIp || '') ? '*' : b.HostIp}|${b.HostPort}`)).sort();
  return JSON.stringify({
    user: c.Config.User, env: [...c.Config.Env].sort(), ports, mounts,
    devices: (h.Devices || []).map((d) => `${d.PathOnHost}:${d.PathInContainer}`).sort(),
    dns: h.Dns || [], restart: `${h.RestartPolicy.Name}:${h.RestartPolicy.MaximumRetryCount}`, mode: h.NetworkMode, memory: h.Memory, cpus: h.NanoCpus,
  });
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

const open = async (type, name, viewport, mode) => {
  const browser = await type.launch();
  const ctx = await browser.newContext({ viewport, ignoreHTTPSErrors: true, colorScheme: mode, serviceWorkers: 'block' });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => {
    // the server reloads after a save: an answer cut off reads "Server error" in Firefox and "access control checks" in WebKit
    if (/Failed to fetch|Load failed|NetworkError|due to access control checks|^Server error$/.test(e.message)) return;
    failures.push(`${name} PAGEERROR: ${(e.stack || e.message).slice(0, 400)}`);
  });
  await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  await ctx.request.post(base + '/cosmos/api/sudo', { data: { password } });
  return { browser, ctx, page };
};
const go = async (page, container, tab) => {
  await page.goto(`${base}/cosmos-ui/servapps/containers/${container}`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(2500);
  const t = page.getByRole('tab', { name: tab, exact: true });
  if (await t.count() && await t.first().isVisible()) await t.first().click();
  else { await page.locator('.MuiSelect-select').first().click(); await page.getByRole('option', { name: tab, exact: true }).click(); }
  await page.waitForTimeout(2000);
};
const radio = (page, group, name) => page.locator(`#${group} [role=radio]`).filter({ hasText: name });
const checked = async (page, group) => (await page.locator(`#${group} [role=radio][aria-checked=true]`).allInnerTexts()).join('|');
const sideways = (page) => page.evaluate(() => document.scrollingElement.scrollWidth > window.innerWidth + 1);
const dialog = (page) => page.locator('[data-sensitive-edit]');
const shot = (page, name, what) => page.screenshot({ path: `shots/edit-tabs-${name}-${what}.png`, fullPage: true });
const errorsShown = async (page) => (await page.locator('.MuiFormHelperText-root.Mui-error').allInnerTexts()).join(' | ');

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
        const oddBefore = shape(await inspect('odd'));

        // --- Network tab of plain: a port proposed from the image, on the home network
        await go(page, 'plain', 'Network');
        // the image's port is listed, not published; one click publishes it on the home network
        const row = page.locator('[data-fork-port-row="0"]');
        expect(await row.locator('[data-fork-reach=none]').count() === 1, `${name}: plain's declared port is said not published`);
        expect(/As the server/.test(await checked(page, 'fork-dns')), `${name}: DNS: Docker's default is picked`);
        expect(await radio(page, 'fork-dns', 'The home router (192.168.78.254)').count() === 1, `${name}: DNS: the home router is proposed`);
        expect(await radio(page, 'fork-dns', '192.168.78.53').count() === 1, `${name}: DNS: the server's resolver is proposed`);
        expect(/Its own network/.test(await checked(page, 'fork-network-mode')), `${name}: the network mode is a choice`);
        expect(await radio(page, 'fork-network-mode', 'sensitive').count() === 1, `${name}: host mode is named as sensitive`);
        await row.locator('[data-fork-publish]').click();
        expect(await row.locator('[data-fork-container-port="0"]').inputValue() === '9000', `${name}: the container port is the image's`);
        expect(await row.locator('[data-fork-host-port="0"]').inputValue() === '9000', `${name}: the host port is proposed free`);
        expect(/Home network \(192\.168\.78\.5\)/.test(await checked(page, 'fork-port-address-0')), `${name}: the home-network address is proposed`);
        expect(await row.locator('[data-fork-reach=home]').count() === 1, `${name}: it says the home network can reach it`);
        expect(/image declares/.test(await row.innerText()), `${name}: it says where the proposal comes from`);
        // "+" proposes nothing more: the image has no other port
        await page.locator('xpath=//*[normalize-space()="Expose Ports"]/following::button[@aria-label="Add"][1]').click();
        expect(await page.locator('[data-fork-port-row="1"] [data-fork-container-port="1"]').inputValue() === '', `${name}: a new row is empty when nothing more is declared`);
        await page.locator('[data-fork-port-row="1"]').getByRole('button', { name: 'Delete' }).click();
        if (size === 'm') expect(!(await sideways(page)), `${name}: Network tab: no sideways scroll`);
        await shot(page, name, 'network-proposed');
        // the doors: the address, the DNS, the network mode
        await radio(page, 'fork-port-address-0', 'Type it myself').click();
        await page.locator('#fork-port-ip-0').fill('127.0.0.1');
        expect(await row.locator('[data-fork-reach=server]').count() === 1, `${name}: a typed 127.0.0.1 says only this server`);
        await page.locator('#fork-port-ip-0').fill('192.168.300.1');
        expect(/IPv4 or IPv6/.test(await row.innerText()), `${name}: a typed address is checked`);
        await radio(page, 'fork-port-address-0', 'Home network').click();
        await radio(page, 'fork-dns', 'Type it myself').click();
        await page.locator('#fork-dns-other').fill('dns.example.org');
        expect(/IP addresses/.test(await errorsShown(page)), `${name}: a typed DNS name is refused in the form`);
        await page.locator('#fork-dns-other').fill('');
        await radio(page, 'fork-network-mode', 'Type it myself').click();
        expect(await page.locator('#fork-network-mode-other').inputValue() === 'bridge' || await page.locator('#fork-network-mode-other').count() === 1, `${name}: the network mode's field opens`);
        await radio(page, 'fork-network-mode', 'Its own network').click();
        if (size === 'd') await shot(page, name, 'network-doors');
        await page.getByRole('button', { name: 'Update Ports' }).click();
        await until('plain publishes 9000 on the home network', async () => {
          const b = ((await inspect('plain')).HostConfig.PortBindings || {})['9000/tcp'];
          return b && b[0].HostIp === lanIp && b[0].HostPort === '9000';
        });
        await serverUp();
        await until('plain runs', async () => (await inspect('plain')).State.Running);
        // it listens on the home-network address only: from the server itself, and from the browser's network
        const fromServer = await once('edit-tabs-probe', { Cmd: ['sh', '-c', `wget -q -T 3 -O /dev/null http://${lanIp}:9000/hostname && echo lan-yes || echo lan-no; wget -q -T 3 -O /dev/null http://${ip}:9000/hostname && echo doc-yes || echo doc-no`], HostConfig: { NetworkMode: 'host' } });
        expect(/lan-yes/.test(fromServer) && /doc-no/.test(fromServer), `${name}: listens on ${lanIp} and not on ${ip} (${fromServer.replace(/\n/g, ' ')})`);
        let outside = 'refused';
        try { await api.get(`http://${ip}:9000/hostname`, { timeout: 4000 }); outside = 'answered'; } catch (e) { /* refused */ }
        expect(outside === 'refused', `${name}: not reachable from the browser's network`);

        // --- Storage tab of plain: the declared folder, kept in the proposed place, survives a recreate
        await go(page, 'plain', 'Storage');
        const declared = page.locator('[data-fork-declared]');
        expect(/keeps data in \/data/.test(await declared.innerText()), `${name}: the declared /data is said`);
        expect(/lost when the container is made again/.test(await declared.innerText()), `${name}: it says the data is lost now`);
        const proposedPath = ((await declared.innerText()).match(/the folder (\/\S+)/) || [])[1] || '';
        // the data path (/cosmos-storage) is on the test Docker's system disk: moved to a data disk (the run folder's bind counts as one too)
        expect(/^\/(mnt\/data|cosmos)\/cosmos-storage\/plain\/data$/.test(proposedPath), `${name}: proposed on a data disk (${proposedPath})`);
        if (size === 'm') expect(!(await sideways(page)), `${name}: Storage tab: no sideways scroll`);
        await shot(page, name, 'storage-proposed');
        await declared.getByRole('button', { name: 'Keep it there' }).click();
        await page.getByRole('button', { name: 'Update Volumes' }).click();
        await until('plain keeps /data in the folder', async () => ((await inspect('plain')).Mounts || []).some((m) => m.Type === 'bind' && m.Destination === '/data' && m.Source === proposedPath));
        await serverUp();
        await until('plain runs again', async () => (await inspect('plain')).State.Running);
        const wrote = await run('plain', ['sh', '-c', 'echo proof-' + name + ' > /data/proof && cat /data/proof']);
        expect(wrote === 'proof-' + name, `${name}: the app (user 1000) writes in the new folder (${wrote})`);
        const firstId = (await inspect('plain')).Id;
        // a harmless edit makes the container again
        await go(page, 'plain', 'Docker');
        await radio(page, 'fork-restart', 'Always').click();
        await page.getByRole('button', { name: /Update Container/ }).click();
        await until('plain is made again', async () => { const c = await inspect('plain'); return c && c.Id !== firstId && c.State.Running; });
        await serverUp();
        expect((await inspect('plain')).HostConfig.RestartPolicy.Name === 'always', `${name}: the restart choice is saved`);
        expect(await run('plain', ['cat', '/data/proof']) === 'proof-' + name, `${name}: the data survived the recreate`);
        await go(page, 'plain', 'Storage');
        expect((await page.locator('[data-fork-declared]').count()) === 0, `${name}: nothing declared is left unkept`);
        expect(await page.locator('[data-fork-folder=writable]').count() >= 1, `${name}: the folder's owner and room are said`);
        // a new mount: the data places as a choice, the field as the door, read-only as a switch
        const rows = await page.locator('input[name$="].Target"]').count();
        await page.getByRole('button', { name: 'New Mount Point' }).click();
        await page.locator(`input[name="volumes[${rows}].Target"]`).fill('/extra');
        const places = page.locator('[data-fork-source-places]').last();
        expect(await places.locator('[role=button]').count() >= 1, `${name}: a new mount offers the data places`);
        await places.locator('[role=button]').last().click();
        const picked = await page.locator(`input[name="volumes[${rows}].Source"]`).inputValue();
        expect(/^\/mnt\/data\/.*\/extra$/.test(picked), `${name}: a data place fills the source (${picked})`);
        await page.locator(`input[name="volumes[${rows}].Source"]`).fill(picked + '-mine');
        await page.getByRole('switch').last().check().catch(async () => { await page.locator('input[type=checkbox]').last().check(); });
        if (size === 'd') await shot(page, name, 'storage-new-mount');
        await page.getByRole('button', { name: 'Update Volumes' }).click();
        await until('plain has /extra read-only', async () => ((await inspect('plain')).Mounts || []).some((m) => m.Destination === '/extra' && m.Source === picked + '-mine' && m.RW === false));
        await serverUp();

        // --- Docker tab of lsmis: the PUID/PGID mismatch and its fix
        await go(page, 'lsmis', 'Docker');
        const puid = page.locator('[data-fork-puid]');
        expect(/1001:1001/.test(await puid.innerText()) && /1000:1000/.test(await puid.innerText()), `${name}: the mismatch is said`);
        expect(/As the image says/.test(await checked(page, 'fork-user')), `${name}: the user is left to the image`);
        if (size === 'm') expect(!(await sideways(page)), `${name}: Docker tab: no sideways scroll`);
        await shot(page, name, 'docker-mismatch');
        await puid.getByRole('button', { name: 'Set PUID=1001 and PGID=1001' }).click();
        await page.getByRole('button', { name: /Update Container/ }).click();
        await until('lsmis runs as 1001', async () => { const env = (await inspect('lsmis')).Config.Env; return env.includes('PUID=1001') && env.includes('PGID=1001'); });
        await serverUp();
        await go(page, 'lsmis', 'Docker');
        expect((await page.locator('[data-fork-puid]').count()) === 0, `${name}: after the fix nothing is said`);

        // --- Docker tab of lsapp: the device, the limits, the user's door
        await go(page, 'lsapp', 'Docker');
        const devices = page.locator('[data-fork-devices]');
        expect(/renderD129/.test(await devices.innerText()), `${name}: the server's render node is offered`);
        await radio(page, 'fork-user', 'Type it myself').click();
        await page.locator('#fork-user-other').fill('1000:1000');
        expect(await page.locator('[data-fork-user-ls]').count() === 1, `${name}: a user on a linuxserver.io image is warned about`);
        await radio(page, 'fork-user', 'As the image says').click();
        expect(await page.locator('[data-fork-user-ls]').count() === 0, `${name}: back to the image's user`);
        await radio(page, 'fork-limit-memLimit', 'Type it myself').click();
        await page.locator('#fork-limit-memLimit-other').fill('768m');
        await radio(page, 'fork-limit-cpus', '1 CPU').click();
        await devices.locator('[role=button]').filter({ hasText: 'renderD129' }).click();
        if (size === 'd') await shot(page, name, 'docker-device');
        await page.getByRole('button', { name: /Update Container/ }).click();
        await dialog(page).waitFor({ timeout: 20000 });
        expect(/device \/dev\/dri\/renderD129/.test(await dialog(page).innerText()), `${name}: the device needs its tick`);
        await dialog(page).locator('input[type=checkbox]').first().check();
        await page.locator('[data-sensitive-edit-accept]').click();
        await until('lsapp has the device and the limits', async () => {
          const c = await inspect('lsapp');
          return (c.HostConfig.Devices || []).some((d) => d.PathOnHost === '/dev/dri/renderD129') && c.HostConfig.Memory === 768 * 1024 * 1024 && c.HostConfig.NanoCpus === 1e9;
        });
        await serverUp();

        // --- Network tab of lsapp: a port on every address says so; DNS typed by hand
        await go(page, 'lsapp', 'Network');
        expect(/Every address/.test(await checked(page, 'fork-port-address-0')), `${name}: 18080 is on every address`);
        expect(/internet/.test(await page.locator('[data-fork-reach=everyone]').innerText()), `${name}: it says the internet can reach it if forwarded`);
        await radio(page, 'fork-dns', 'Type it myself').click();
        await page.locator('#fork-dns-other').fill('192.0.2.53');
        await page.getByRole('button', { name: 'Update Ports' }).click();
        await until('lsapp has the DNS and its port', async () => {
          const c = await inspect('lsapp');
          return (c.HostConfig.Dns || []).join() === '192.0.2.53' && (c.HostConfig.PortBindings['8080/tcp'] || [])[0].HostPort === '18080';
        });
        await serverUp();

        // --- odd: saved from each tab, unchanged
        for (const [tab, button] of [['Docker', /Update Container/], ['Network', 'Update Ports'], ['Storage', 'Update Volumes']]) {
          const id = (await inspect('odd')).Id;
          await go(page, 'odd', tab);
          if (tab === 'Network') expect(/oddnet/.test(await checked(page, 'fork-network-mode')), `${name}: odd's network is picked`);
          await page.getByRole('button', { name: button }).click();
          await until(`odd saved from ${tab}`, async () => { const c = await inspect('odd'); return c && c.Id !== id && c.State.Running; });
          await serverUp();
          const shown = await errorsShown(page);
          expect(!shown, `${name}: odd, ${tab}: no error (${shown})`);
          const after = shape(await inspect('odd'));
          expect(after === oddBefore, `${name}: odd, ${tab}: unchanged` + (after === oddBefore ? '' : `\n      before ${oddBefore}\n      after  ${after}`));
        }
      } catch (e) {
        expect(false, `${name}: ${e.message.split('\n')[0]}`);
        await shot(page, name, 'failure').catch(() => {});
      }
      await browser.close();
    }
  }
}

for (const name of Object.keys(SPECS)) await api.delete(`${dind}/containers/${name}?force=1&v=1`);
note(failures.length ? `\n${failures.length} FAILED:\n  ${failures.join('\n  ')}` : '\nALL OK');
process.exit(failures.length ? 1 : 0);

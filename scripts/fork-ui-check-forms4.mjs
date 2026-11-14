// Fork addition: the forms of plan 27: OpenID clients (the app picked, the
// redirect URIs proposed, the values to copy against the server's own discovery
// document), the storage dialogs (mount, unmount with what uses it and the typed
// confirmation, merge, parity), the domain dialog filled by hand, trusted proxies
// and the geo-blocking country, and the new network, new volume, market sources
// and shield whitelist dialogs.
// ENGINES=chromium,firefox,webkit  SIZES=d,m  BASE=https://forms4.test:2053
// The test instance: a fresh throwaway Cosmos, httpsCertificateMode SELFSIGNED,
// hostname forms4.test, HTTP 2052 / HTTPS 2053, admin / Test-Passw0rd!x, no Docker.
// It changes that instance's URLs and OpenID clients.
// No storage command ever reaches a server: the disk lists, the unmount check
// and every mount, unmount, merge and parity request are answered by the
// browser (page.route), whatever the instance runs on; the same for the
// networks and volumes (no Docker), the domain lookup and the extras of the
// HTTP check. Screenshots go to ./shots. The run ends with "ALL OK" or the failures.
import { chromium, firefox, webkit } from 'playwright';

const base = process.env.BASE || 'https://forms4.test:2053';
const engines = { chromium, firefox, webkit };
const names = (process.env.ENGINES || 'chromium').split(',');
const sizes = { d: { width: 1366, height: 900 }, m: { width: 390, height: 844 } };
const wanted = (process.env.SIZES || 'd').split(',');
const note = (...a) => console.log(...a);
const failures = [];
const expect = (what, ok, detail) => { note(ok ? '  ok  ' : '  FAIL', what, detail === undefined ? '' : '| ' + String(detail).slice(0, 300)); if (!ok) failures.push(what); };
const wait = (ms) => new Promise((s) => setTimeout(s, ms));
const PASSWORD = 'Test-Passw0rd!x';
const TB = 1e12;

const settle = async (ctx) => {
  for (let i = 0; i < 40; i++) {
    try { if ((await ctx.request.get(base + '/cosmos/api/status')).ok()) return; } catch (e) { /* restarting */ }
    await wait(1000);
  }
};
const noSideways = async (page) => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
const dialogFits = async (page) => page.evaluate(() => {
  const d = document.querySelector('.MuiDialog-paper');
  if (!d) return 'no dialog';
  const r = d.getBoundingClientRect();
  const inner = [...d.querySelectorAll('*')].filter((e) => e.getBoundingClientRect().right > r.right + 1 && e.getBoundingClientRect().width > 0);
  return inner.length ? inner.slice(0, 3).map((e) => e.tagName + '.' + e.className).join(' ') : '';
});
// the password confirmation does not survive the reload a saved setting causes
const sudo = async (ctx) => { await settle(ctx); await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: PASSWORD } }); };
const shot = async (page, id, what) => page.screenshot({ path: `shots/${id}-${what}.png`, fullPage: false });
const dialog = (page) => page.locator('.MuiDialog-paper').last();
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
  await page.waitForTimeout(2000);
};

const ROUTES = [
  { Name: 'immich', Host: 'immich.forms4.test', Target: 'http://immich-server:2283' },
  { Name: 'grafana', Host: 'grafana.forms4.test', Target: 'http://grafana:3000' },
  { Name: 'notes', Host: 'notes.forms4.test', Target: 'http://notes:8080' },
];
const seedRoutes = async (ctx) => {
  const config = (await (await ctx.request.get(base + '/cosmos/api/config')).json()).data;
  const routes = (config.HTTPConfig.ProxyConfig.Routes || []).filter((r) => !ROUTES.some((x) => x.Name === r.Name));
  ROUTES.forEach((r) => routes.push({ ...r, Description: 'test', Mode: 'PROXY', UseHost: true, UsePathPrefix: false, PathPrefix: '', StripPathPrefix: false,
    AuthEnabled: false, Timeout: 14400000, ThrottlePerMinute: 10000, BlockCommonBots: true, SmartShield: { Enabled: true }, CORSOrigin: '' }));
  config.HTTPConfig.ProxyConfig.Routes = routes;
  config.OpenIDClients = [];
  const res = await ctx.request.put(base + '/cosmos/api/config', { data: config });
  await wait(3000);
  await settle(ctx);
  return res.status();
};

const CANDIDATES = {
  devices: [
    { device: '/dev/sdb1', disk: '/dev/sdb', type: 'part', fstype: 'ext4', label: 'Media', uuid: '1b2c3d4e-0000-4000-8000-000000000001', model: 'WDC WD40', size: 4 * TB, mountable: true, use: { code: '', detail: '' } },
    { device: '/dev/sdc1', disk: '/dev/sdc', type: 'part', fstype: 'ext4', label: '', size: 2 * TB, mountable: false, use: { code: 'mounted', detail: '/mnt/d1' } },
    { device: '/dev/sde1', disk: '/dev/sde', type: 'part', fstype: 'linux_raid_member', size: TB, mountable: false, use: { code: 'nofs', detail: 'linux_raid_member' } },
  ],
  mounts: [
    { path: '/', device: '/dev/nvme0n1p2', disk: '/dev/nvme0n1', fstype: 'ext4', size: TB / 2, free: TB / 4, permanent: true, kind: 'system' },
    { path: '/mnt/d1', device: '/dev/sdc1', disk: '/dev/sdc', fstype: 'ext4', size: 2 * TB, free: TB, permanent: true, kind: 'data' },
    { path: '/mnt/d2', device: '/dev/sdf1', disk: '/dev/sdf', fstype: 'xfs', size: 4 * TB, free: 3 * TB, permanent: true, kind: 'data' },
    { path: '/mnt/p1', device: '/dev/sdg1', disk: '/dev/sdg', fstype: 'ext4', size: 3 * TB, free: 3 * TB, permanent: true, kind: 'data' },
    { path: '/mnt/p2', device: '/dev/sdh1', disk: '/dev/sdh', fstype: 'ext4', size: 6 * TB, free: 6 * TB, permanent: true, kind: 'data' },
  ],
  base: '/mnt', inContainer: false,
};
const MOUNTS = [{ device: '/dev/sdc1', path: '/mnt/d1', type: 'ext4', opts: ['rw'], permenant: true }, { device: '/dev/sdf1', path: '/mnt/d2', type: 'xfs', opts: ['rw'], permenant: true }];
const ok = (data) => ({ status: 200, contentType: 'application/json', body: JSON.stringify({ status: 'OK', data }) });

// Everything storage, Docker lists and lookups answered by the browser.
const fakeServer = async (page, sent) => {
  await page.route(/\/cosmos\/api\/(storage\/candidates|mounts|disks|snapraid|mount|unmount|merge|storage\/unmount-check)(\?.*)?$/, async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname.replace('/cosmos/api/', '');
    if (req.method() !== 'GET') sent.push({ path, body: req.postDataJSON ? req.postDataJSON() : null });
    if (path === 'storage/candidates') return route.fulfill(ok(CANDIDATES));
    if (path === 'mounts') return route.fulfill(ok(MOUNTS));
    if (path === 'disks') return route.fulfill(ok([]));
    if (path === 'snapraid' && req.method() === 'GET') return route.fulfill(ok([]));
    if (path === 'storage/unmount-check') return route.fulfill(ok({ mountPoint: '/mnt/d1', device: '/dev/sdc1', permanent: true, system: false, users: [{ pid: 4242, name: 'jellyfin', how: 'files' }] }));
    return route.fulfill(ok({}));
  });
  await page.route(/\/cosmos\/api\/(networks|volumes)(\?.*)?$/, async (route) => {
    const req = route.request();
    if (req.method() !== 'GET') { sent.push({ path: new URL(req.url()).pathname.replace('/cosmos/api/', ''), body: req.postDataJSON() }); return route.fulfill(ok({})); }
    return route.fulfill(ok(req.url().includes('volumes') ? { Volumes: [{ Name: 'jellyfin-config', Driver: 'local', Mountpoint: '/var/lib/docker/volumes/x' }] } : [{ Name: 'bridge', Driver: 'bridge', Id: 'x', IPAM: { Config: [] } }, { Name: 'media-net', Driver: 'bridge', Id: 'y', IPAM: { Config: [] } }]));
  });
  await page.route(/\/cosmos\/api\/certificates\/domain$/, (route) => route.fulfill(ok({ domain: 'example.com', configured: false, nameservers: ['ada.ns.cloudflare.com', 'bob.ns.cloudflare.com'], nameserversOf: 'example.com', provider: 'cloudflare', providerName: 'Cloudflare', lookupFailed: false, canAnswer: [] })));
  await page.route(/\/cosmos\/api\/config\/http-check$/, async (route) => {
    const res = await route.fetch();
    const answer = await res.json();
    answer.data.seen = { ...answer.data.seen, country: 'RO', via: '172.30.0.7', viaTrusted: false,
      proxies: [{ name: 'npm', image: 'nginx-proxy-manager', addresses: ['172.30.0.8'] }],
      cloudflare: { connected: true, trusted: true, ranges: ['173.245.48.0/20', '103.21.244.0/22'] } };
    answer.data.you = { ...answer.data.you, address: '203.0.113.9', local: false };
    answer.data.server = { ...answer.data.server, addresses: [{ ip: '192.168.1.14', network: '192.168.1.0/24', interface: 'eno1' }] };
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(answer) });
  });
};

for (const name of names) for (const size of wanted) {
  const id = name + '-' + size;
  const phone = size === 'm';
  const browser = await engines[name].launch();
  const ctx = await browser.newContext({ viewport: sizes[size], ignoreHTTPSErrors: true });
  await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: PASSWORD } });
  await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: PASSWORD } });
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message.slice(0, 160)));
  const sent = [];
  await fakeServer(page, sent);

  if (name === names[0] && size === wanted[0]) {
    note(`--- ${id}: the server`);
    expect('routes seeded', (await seedRoutes(ctx)) === 200);
    const bad = await ctx.request.post(base + '/cosmos/api/openid', { data: { id: 'bad', redirect: 'http://notes.forms4.test/cb' } });
    expect('an http redirect URI is refused (OID013)', bad.status() === 400 && (await bad.text()).includes('OID013'));
    const wild = await ctx.request.post(base + '/cosmos/api/openid', { data: { id: 'bad2', redirect: 'https://*.forms4.test/cb' } });
    expect('a wildcard redirect URI is refused', wild.status() === 400);
    const disc = await (await ctx.request.get(base + '/.well-known/openid-configuration')).json();
    expect('the discovery document names the port in use', disc.issuer === 'https://forms4.test:2053' && disc.token_endpoint === 'https://forms4.test:2053/oauth2/token', disc.issuer);
  }

  // --- OpenID ---------------------------------------------------------------
  note(`--- ${id}: OpenID`);
  await page.goto(base + '/cosmos-ui/openid-manage', { waitUntil: 'domcontentloaded' });
  await page.getByRole('button', { name: 'Create' }).first().click();
  await page.waitForTimeout(800);
  await dialog(page).getByRole('radio', { name: /immich/i }).click();
  await page.waitForTimeout(300);
  const chips = await dialog(page).locator('.MuiChip-label').allTextContents();
  expect('Immich: the redirect URIs its documentation names', chips.includes('https://immich.forms4.test:2053/auth/login') && chips.includes('app.immich:///oauth-callback'), chips.join(' | '));
  expect('the client ID proposed', (await page.locator('#fork-openid-id').inputValue()).startsWith('immich'));
  await page.locator('#fork-openid-redirect').fill('http://immich.forms4.test/x');
  expect('an http address is refused while typing', await dialog(page).getByText(/Plain http only/).isVisible());
  await page.locator('#fork-openid-redirect').fill('');
  expect('dialog: no sideways scroll', (await noSideways(page)) <= 0);
  expect('dialog: nothing sticks out', (await dialogFits(page)) === '', await dialogFits(page));
  await shot(page, id, 'openid-new');
  const idValue = await page.locator('#fork-openid-id').inputValue();
  await dialog(page).getByRole('button', { name: 'Create' }).click();
  await page.waitForTimeout(2500);
  const values = await dialog(page).locator('input[readonly]').evaluateAll((els) => els.map((e) => e.value));
  expect('values: discovery and issuer with the port', values.includes('https://forms4.test:2053/.well-known/openid-configuration') && values.includes('https://forms4.test:2053'), values.slice(0, 2).join(' | '));
  const secret = values.find((v) => /^[A-Za-z0-9]{32}$/.test(v));
  expect('values: the secret shown once', Boolean(secret));
  expect('values: no sideways scroll', (await noSideways(page)) <= 0);
  await shot(page, id, 'openid-values');
  await dialog(page).getByRole('button', { name: 'Done' }).click();
  await page.waitForTimeout(1500);
  const cfg = (await (await ctx.request.get(base + '/cosmos/api/config')).json()).data;
  const stored = (cfg.OpenIDClients || []).find((c) => c.id === idValue);
  expect('the client is stored with its URIs, its hash not sent back', stored && stored.secret === '' && stored.redirect.includes('/auth/login'), JSON.stringify(stored));
  await page.getByRole('button', { name: 'Reset Secret' }).first().click();
  await page.waitForTimeout(500);
  expect('reset asks first', await dialog(page).getByText(/cannot sign its users in until/).isVisible());
  await dialog(page).getByRole('button', { name: 'Make a new secret' }).click();
  await page.waitForTimeout(2500);
  const values2 = await dialog(page).locator('input[readonly]').evaluateAll((els) => els.map((e) => e.value));
  const secret2 = values2.find((v) => /^[A-Za-z0-9]{32}$/.test(v));
  expect('a new secret, different', secret2 && secret2 !== secret);
  await dialog(page).getByRole('button', { name: 'Done' }).click();
  await ctx.request.delete(base + '/cosmos/api/openid/' + idValue);
  await page.waitForTimeout(1500);

  // --- storage --------------------------------------------------------------
  note(`--- ${id}: storage (answered by the browser)`);
  await sudo(ctx);
  await page.goto(base + '/cosmos-ui/storage', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(2500);
  await openTab(page, 'Mounts');
  await page.getByRole('button', { name: 'New Mount' }).first().click();
  await page.waitForTimeout(1200);
  await dialog(page).getByRole('button', { name: /sdb1/ }).click();
  await page.waitForTimeout(400);
  expect('mount: the folder proposed from the label', (await page.locator('#fork-storage-path').inputValue()) === '/mnt/Media');
  expect('mount: fstab said in words with the UUID', await dialog(page).getByText(/1b2c3d4e-0000/).isVisible());
  await dialog(page).getByRole('button', { name: /Not offered/ }).click();
  expect('mount: what is not offered, with the reason', await dialog(page).getByText(/Mounted at \/mnt\/d1 already/).isVisible());
  expect('mount: no sideways scroll', (await noSideways(page)) <= 0);
  expect('mount: nothing sticks out', (await dialogFits(page)) === '', await dialogFits(page));
  await shot(page, id, 'mount');
  await dialog(page).getByRole('button', { name: 'Mount' }).click();
  await page.waitForTimeout(1200);
  const mountReq = sent.find((s) => s.path === 'mount');
  expect('mount: sent with UUID-free device, the folder, every start, owner 1000', mountReq && mountReq.body.path === '/dev/sdb1' && mountReq.body.mountPoint === '/mnt/Media' && mountReq.body.permanent === true && mountReq.body.chown === '1000:1000', JSON.stringify(mountReq));

  // unmount: in use, typed confirmation
  await shot(page, id, 'mounts-list');
  await page.getByRole('button', { name: 'More actions' }).first().click().catch(() => {});
  await page.waitForTimeout(500);
  const unmountItem = page.getByRole('menuitem', { name: 'Unmount' });
  if (await unmountItem.count()) {
    await unmountItem.first().click();
    await page.waitForTimeout(1200);
    expect('unmount: says who uses it', await dialog(page).getByText(/jellyfin/).isVisible());
    const button = dialog(page).getByRole('button', { name: 'Unmount' });
    expect('unmount: refused until the folder is typed', await button.isDisabled());
    await page.locator('#fork-storage-confirm').fill('/mnt/d1');
    expect('unmount: allowed once typed', !(await button.isDisabled()));
    await shot(page, id, 'unmount');
    await button.click();
    await page.waitForTimeout(1000);
    const um = sent.find((s) => s.path === 'unmount');
    expect('unmount: the confirmation is sent', um && um.body.confirm === '/mnt/d1', JSON.stringify(um));
  } else {
    expect('unmount: the row menu opens', false);
  }

  // merge
  await page.keyboard.press('Escape'); // the row menu stays open behind the dialog (upstream MenuButton)
  await page.waitForTimeout(300);
  await openTab(page, 'Merge Disks');
  await page.getByRole('button', { name: 'Create Merge' }).first().click();
  await page.waitForTimeout(1200);
  await dialog(page).getByRole('checkbox', { name: '/mnt/d1' }).check();
  await dialog(page).getByRole('checkbox', { name: '/mnt/d2' }).check();
  expect('merge: the system disk is not offered', !(await dialog(page).getByRole('checkbox', { name: '/' , exact: true }).count()));
  expect('merge: the folder proposed', (await page.locator('#fork-merge-path').inputValue()) === '/mnt/storage');
  await dialog(page).getByRole('radio', { name: 'Fill one disk first' }).click();
  expect('merge: no sideways scroll', (await noSideways(page)) <= 0);
  await shot(page, id, 'merge');
  await dialog(page).getByRole('button', { name: 'Merge', exact: true }).click();
  await page.waitForTimeout(1000);
  const mg = sent.find((s) => s.path === 'merge');
  expect('merge: branches and the policy sent', mg && mg.body.branches.length === 2 && mg.body.opts === 'category.create=lfs', JSON.stringify(mg));

  // parity
  await openTab(page, 'Parity');
  await page.getByRole('button', { name: 'New Parity Disks' }).first().click();
  await page.waitForTimeout(1200);
  await dialog(page).getByRole('checkbox', { name: '/mnt/d1' }).first().check();
  await dialog(page).getByRole('checkbox', { name: '/mnt/d2' }).first().check();
  await page.waitForTimeout(500);
  expect('parity: the smallest large enough proposed (p2, 6 TB)', await dialog(page).getByText(/Proposed: \/mnt\/p2/).isVisible());
  expect('parity: a smaller disk says why', await dialog(page).getByText(/Smaller \(3\.0 TB\) than the largest data disk \(4\.0 TB\)/).isVisible());
  expect('parity: no sideways scroll', (await noSideways(page)) <= 0);
  await shot(page, id, 'parity');
  await dialog(page).getByRole('button', { name: 'Create' }).click();
  await page.waitForTimeout(1000);
  const sr = sent.find((s) => s.path === 'snapraid');
  expect('parity: data and parity sent', sr && Object.values(sr.body.data).length === 2 && sr.body.parity[0] === '/mnt/p2', JSON.stringify(sr));

  // --- domain by hand ---------------------------------------------------------
  note(`--- ${id}: domain by hand, HTTP settings`);
  await sudo(ctx);
  await page.goto(base + '/cosmos-ui/config-general/https', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(2500);
  await page.getByRole('button', { name: 'Add a domain by hand' }).click();
  await page.waitForTimeout(800);
  await page.locator('#Zone').fill('nas.local');
  await page.waitForTimeout(400);
  expect('domain: a home name proposes self-signed', (await dialog(page).getByRole('radio', { name: 'Self-signed' }).getAttribute('aria-checked')) === 'true');
  await page.locator('#Zone').fill('example.com');
  await page.locator('#Zone').blur();
  await page.waitForTimeout(1500);
  expect('domain: a public name proposes Let\'s Encrypt', (await dialog(page).getByRole('radio', { name: 'Let\'s Encrypt' }).getAttribute('aria-checked')) === 'true');
  expect('domain: the provider found from the name servers', await dialog(page).getByText(/Cloudflare's/).first().isVisible());
  expect('domain: no sideways scroll', (await noSideways(page)) <= 0);
  await shot(page, id, 'domain');
  await dialog(page).getByRole('button', { name: 'Cancel' }).click();

  // --- HTTP: trusted proxies and country ----------------------------------------
  await page.goto(base + '/cosmos-ui/config-general/http-server', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(3000);
  expect('proxies: the proxy in front proposed', await page.getByRole('button', { name: 'Add 172.30.0.7' }).isVisible());
  expect('proxies: the proxy container proposed', await page.getByRole('button', { name: 'Add 172.30.0.8' }).isVisible());
  expect('proxies: Cloudflare said', await page.getByText(/already believes Cloudflare/).isVisible());
  await page.getByRole('button', { name: 'Add 172.30.0.7' }).click();
  expect('proxies: added as a chip', await page.locator('.MuiChip-label', { hasText: '172.30.0.7' }).isVisible());
  const geo = page.locator('.MuiAutocomplete-root input').first();
  if (await geo.count()) {
    await geo.click();
    await geo.fill('Romania');
    await page.waitForTimeout(500);
    await page.getByRole('option', { name: /Romania/ }).first().click().catch(() => {});
    await page.waitForTimeout(500);
    expect('geo: the country the server sees you in is refused', await page.getByText(/the address the server sees you at \(203\.0\.113\.9\) is in RO/).isVisible());
  }
  expect('HTTP: no sideways scroll', (await noSideways(page)) <= 0);
  await page.getByText('Trusted proxies').first().scrollIntoViewIfNeeded();
  await shot(page, id, 'proxies');

  // --- the small ones -------------------------------------------------------------
  note(`--- ${id}: network, volume, market sources, shield whitelist`);
  await sudo(ctx);
  await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(2500);
  await openTab(page, 'Networks');
  await page.getByRole('button', { name: 'New Network' }).first().click();
  await page.waitForTimeout(800);
  await page.locator('#name').fill('media-net');
  await page.locator('#name').blur();
  expect('network: an existing name refused', await dialog(page).getByText('A network with this name exists already').isVisible());
  await page.locator('#name').fill('lan-apps');
  await dialog(page).getByRole('radio', { name: /home network/ }).click();
  await page.waitForTimeout(1500);
  await dialog(page).getByRole('button', { name: /eno1/ }).click().catch(() => {});
  await page.waitForTimeout(300);
  expect('network: the card and its network proposed', (await page.locator('#parentInterface').inputValue()) === 'eno1' && (await page.locator('#subnet').inputValue()) === '192.168.1.0/24');
  expect('network: no sideways scroll', (await noSideways(page)) <= 0);
  await shot(page, id, 'network');
  await dialog(page).getByRole('button', { name: 'Create' }).click();
  await page.waitForTimeout(800);
  const net = sent.find((s) => s.path === 'networks');
  expect('network: sent as macvlan on eno1', net && net.body.driver === 'macvlan' && net.body.parentInterface === 'eno1', JSON.stringify(net));

  await openTab(page, 'Volumes');
  await page.getByRole('button', { name: /New Volume/ }).first().click();
  await page.waitForTimeout(800);
  await page.locator('#name').fill('jellyfin-config');
  await page.locator('#name').blur();
  expect('volume: an existing name refused', await dialog(page).getByText('A volume with this name exists already').isVisible());
  await shot(page, id, 'volume');
  await dialog(page).getByRole('button', { name: 'Cancel' }).click();

  await page.goto(base + '/cosmos-ui/market-listing/', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(2500);
  await page.getByRole('button', { name: 'Sources' }).first().click();
  await page.waitForTimeout(800);
  await dialog(page).getByRole('button', { name: 'Add Source' }).click();
  const url = dialog(page).locator('input[name$=".Url"]').last();
  await url.fill('https://www.selfhosted-apps.org/servapps.json');
  expect('market: a name proposed from the address', (await dialog(page).locator('input[name$=".Name"]').last().inputValue()) === 'selfhosted-apps');
  expect('market: no sideways scroll', (await noSideways(page)) <= 0);
  await shot(page, id, 'market');
  await dialog(page).getByRole('button', { name: 'Cancel' }).click().catch(() => {});

  await page.goto(base + '/cosmos-ui/config-url/whitelist', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(2500);
  await page.getByRole('button', { name: 'Add IP' }).first().click();
  await page.waitForTimeout(1500);
  expect('whitelist: the address in use proposed', await dialog(page).getByRole('button', { name: 'Use 203.0.113.9' }).isVisible());
  await dialog(page).getByRole('button', { name: 'Use 203.0.113.9' }).click();
  expect('whitelist: filled with a label', (await page.locator('#IP').inputValue()) === '203.0.113.9' && (await page.locator('#Label').inputValue()) === 'This device');
  expect('whitelist: no sideways scroll', (await noSideways(page)) <= 0);
  await shot(page, id, 'whitelist');
  await dialog(page).getByRole('button', { name: 'Cancel' }).click();

  expect('no script errors', errors.length === 0, errors.join(' || '));
  await browser.close();
}

note(failures.length ? `FAILED: ${failures.length}\n - ` + failures.join('\n - ') : 'ALL OK');
process.exit(failures.length ? 1 : 0);

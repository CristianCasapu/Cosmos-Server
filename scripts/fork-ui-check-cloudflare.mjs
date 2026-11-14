// Fork addition: tries the Cloudflare integration in headless Chromium,
// Firefox and WebKit, at 1366x900 and 390x844, against a throwaway Cosmos
// and a stand-in for Cloudflare's API (scripts/fork-fake-cloudflare.go).
// Nothing is sent to Cloudflare: the test network is made with --internal and
// the name api.cloudflare.com resolves to the stand-in there.
//
// What it expects (see CLAUDE.md, "Checking the UI in a browser"):
//   - a network made with `docker network create --internal --subnet 172.31.53.0/24 <net>`;
//   - the stand-in on it with the alias api.cloudflare.com (FAKE=http://<its name>:8080
//     is its control port) and a folder mounted at /shared;
//   - a docker:27-dind with two containers named radarr (any image) and
//     jellyfin (an image named jellyfin/jellyfin), connected to that network only;
//   - a fresh Cosmos at 172.31.53.10, run in a loop, with COSMOS_HTTP_PORT=2052,
//     COSMOS_HTTPS_PORT=2053, SSL_CERT_FILE=/shared/ca.pem and DOCKER_HOST=tcp://<dind>:2375.
//   - for the part on Authenticated Origin Pulls: the test Cosmos started with
//     --cap-add NET_ADMIN, one of Cloudflare's addresses (173.245.48.5) added to the
//     loopback interface inside its network namespace, and the stand-in's second role
//     running there: `fork-fake-cloudflare edge <SERVER_IP>:2053 173.245.48.5 <FAKE>`
//     (EDGE=http://<SERVER_IP>:8081 is its control port). It stands in for Cloudflare's
//     proxy: requests that reach Cosmos from Cloudflare's address, with or without the
//     certificate Cosmos uploaded. Nothing on the host is touched.
// PART=pulls runs only that part and the panel's reachability (the domain must be connected
// by an earlier full run). The rest is for a fresh instance: it switches radarr on once.
// It finishes the set-up wizard itself: HTTPS with the self-signed
// certificate, the server's IP address as its hostname, as on the server
// this was written for. Screenshots go to ./shots.
import { chromium, firefox, webkit, request } from 'playwright';

const ip = process.env.SERVER_IP || '172.31.53.10';
const plain = `http://${ip}:2052`;
const base = `https://${ip}:2053`;
const fake = process.env.FAKE || 'http://cosmos-cloudflare-fake:8080';
const edge = process.env.EDGE || `http://${ip}:8081`;
const onlyPulls = process.env.PART === 'pulls';
const SSL = 'sslcerts0123456789abcdefghijklmnopqrstuvwxYZ';
const password = 'Test-Passw0rd!x';
const GOOD = 'cfut_dGVzdC10b2tlbi1mb3ItdGhlLWJyb3dzZXItY2hlY2s0123456789';
const READ = 'readonly0123456789abcdefghijklmnopqrstuvwxYZ';
const BROAD = 'allzones0123456789abcdefghijklmnopqrstuvwxYZ';
const GLOBAL_KEY = 'cfk_' + '0a'.repeat(20) + '1234';

const note = (...a) => console.log(...a);
const failures = [];
const expect = (ok, what) => { note(ok ? '  ok  ' : '  FAIL', what); if (!ok) failures.push(what); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const api = await request.newContext({ ignoreHTTPSErrors: true });
const records = async () => (await (await api.get(fake + '/records')).json()) || [];
const calls = async (clear) => (await (await api.get(fake + '/calls' + (clear ? '?clear=1' : ''))).json()) || [];
const record = async (name, type = 'A') => (await records()).filter((r) => r.name === name && r.type === type);
const until = async (what, check, seconds = 60) => {
  for (let i = 0; i < seconds; i++) {
    try { if (await check()) return true; } catch (e) { /* the server is reloading */ }
    await sleep(1000);
  }
  failures.push('timed out: ' + what);
  note('  FAIL timed out:', what);
  return false;
};
const serverUp = () => until('the server answers over HTTPS', async () => (await api.get(base + '/cosmos/api/status')).status() < 500, 90);

// --- the set-up wizard, once
{
  let fresh = false;
  try {
    // only a server that is not set up yet answers this to someone who is not logged in
    const answer = await api.get(plain + '/cosmos/api/status', { timeout: 5000, maxRedirects: 0 });
    fresh = answer.status() === 200 && (await answer.json()).status === 'OK';
  } catch (e) { /* already set up: the plain port only redirects */ }
  if (fresh) {
    const step = (data) => api.post(plain + '/cosmos/api/newInstall', { data }).then((r) => r.status()).catch(() => 'exit');
    note('wizard:',
      await step({ step: '2', mongodbMode: 'Create' }),
      await step({ step: '3', httpsCertificateMode: 'SELFSIGNED', hostname: ip, allowHTTPLocalIPAccess: true }),
      await step({ step: '4', nickname: 'admin', password, email: 'admin@example.org' }),
      await step({ step: '5' }));
    await sleep(4000);
  }
  await serverUp();
}

const login = async (ctx) => {
  const a = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  const b = await ctx.request.post(base + '/cosmos/api/sudo', { data: { password } });
  return a.status() + '/' + b.status();
};

// --- what the server is told before anything is connected: where its
// records point, and two applications with a local address each
{
  const ctx = await request.newContext({ ignoreHTTPSErrors: true });
  note('login', await login({ request: ctx }));
  const config = (await (await ctx.get(base + '/cosmos/api/config')).json()).data;
  if (!(config.HTTPConfig.ProxyConfig.Routes || []).some((r) => r.Name === 'radarr')) {
    config.HTTPConfig.AdvertisedAddress = '203.0.113.10';
    config.HTTPConfig.ProxyConfig.Routes = [
      { Name: 'radarr', Mode: 'SERVAPP', Target: 'http://radarr:7878', UseHost: true, Host: 'radarr.local' },
      { Name: 'jellyfin', Mode: 'SERVAPP', Target: 'http://jellyfin:8096', UseHost: true, Host: 'jellyfin.local' },
    ];
    const r = await ctx.put(base + '/cosmos/api/config', { data: config });
    note('settings (advertised address, two applications):', r.status());
    await sleep(5000);
    await serverUp();
  }
  await ctx.dispose();
}

const open = async (type, name, viewport) => {
  const browser = await type.launch();
  const ctx = await browser.newContext({ viewport, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => failures.push(`${name} PAGEERROR: ${e.message.slice(0, 300)}`));
  note(`\n== ${name}: login`, await login(ctx));
  return { browser, ctx, page };
};
const noSideScroll = async (page, name, where) => {
  const over = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(over <= 1, `${name}: no sideways scroll on ${where} (${over}px over)`);
};
const shot = (page, name, what) => page.screenshot({ path: `shots/${name}-${what}.png` });
const dialogFits = async (page, name, where) => {
  const box = await page.locator('[role=dialog]').last().boundingBox();
  const width = page.viewportSize().width;
  expect(!!box && box.x >= 0 && box.x + box.width <= width + 1, `${name}: the dialog fits the screen (${where})`);
};
const domains = async (page) => {
  await page.goto(base + '/cosmos-ui/config-general/https', { waitUntil: 'domcontentloaded' });
  await page.getByRole('button', { name: /Connect Cloudflare/ }).waitFor({ timeout: 30000 });
  await page.waitForTimeout(1500);
};
const connectDialog = (page) => page.locator('[data-cloudflare-connect]');
const stepIs = (page, step) => page.locator(`[data-cloudflare-connect="${step}"]`).waitFor({ timeout: 45000 });
const pasteToken = async (page, token) => {
  await page.locator('#cloudflare-token').fill(token);
};
const check = (page) => page.getByRole('button', { name: 'Check the token' }).click();

// what the stand-in for Cloudflare's proxy does and holds
const visit = async (host, cert, n = 1) => (await (await api.get(`${edge}/visit?host=${host}&cert=${cert}&n=${n}`)).json());
const uploadedCerts = async () => (await (await api.get(fake + '/aop')).json());
const pulls = (page) => page.locator('[data-origin-pulls]');
const pullsMode = (page, mode) => page.locator(`[data-origin-pulls="${mode}"]`).waitFor({ timeout: 45000 });
const verdictIs = (page, verdict) => page.locator(`[data-origin-pulls-verdict="${verdict}"]`).waitFor({ timeout: 30000 });
const insideCard = async (page, name, where) => {
  const out = await pulls(page).evaluate((section) => {
    const card = section.closest('.MuiCard-root') || section.parentElement;
    const limit = card.getBoundingClientRect().right + 1;
    return [...section.querySelectorAll('*')].filter((el) => el.getBoundingClientRect().width > 0 && el.getBoundingClientRect().right > limit).length;
  });
  expect(out === 0, `${name}: nothing sticks out of the card (${where}; ${out} elements)`);
};

// the panel's own address among the reachability checks (Configuration > HTTP)
const panelReach = async (page, name) => {
  await page.goto(base + '/cosmos-ui/config-general/http-server', { waitUntil: 'domcontentloaded' });
  const own = page.locator('[data-reach-own-panel]');
  await own.waitFor({ timeout: 45000 });
  await page.waitForTimeout(1500);
  const text = await own.innerText();
  // the test instance serves a certificate of its own making, so the verdict is "problem" and the address is not a link
  expect(/example\.org/.test(text) && /Internet: (checked|not verified|problem)/.test(text), `${name}: the panel's own address is checked for reachability, with the same states as an application`);
  expect(/The Cosmos panel answers at this address; everything in it needs a login/.test(text), `${name}: it is told apart from an application`);
  expect(/Served through Cloudflare: Cosmos answers for this name to Cloudflare's network and to your local network only/.test(text), `${name}: it says the panel is served through Cloudflare`);
  expect(/certificate/i.test(text), `${name}: it says what the certificate is worth`);
  await own.scrollIntoViewIfNeeded();
  await shot(page, name, 'panel-reach');
  await noSideScroll(page, name, 'Configuration > HTTP');
  const box = await own.boundingBox();
  expect(!!box && box.x >= 0 && box.x + box.width <= page.viewportSize().width + 1, `${name}: the panel's reachability fits the screen`);
};

const apexBefore = (await record('example.org'))[0];
const zoneBefore = await records();
note('the zone at the start:', zoneBefore.map((r) => `${r.type} ${r.name}${r.proxied ? ' (proxied)' : ''}`).join(', '));

// ============ the whole way once: Chromium at desktop size
if (!onlyPulls) {
  const name = 'chromium-1366';
  const { browser, ctx, page } = await open(chromium, name, { width: 1366, height: 900 });
  const already = (await (await ctx.request.get(base + '/cosmos/api/zones')).json()).data.some((z) => z.Zone === 'example.org' && !z.derived);

  if (!already) {
    await domains(page);
    await shot(page, name, '1-domains-before');
    await page.getByRole('button', { name: /Connect Cloudflare/ }).click();
    await stepIs(page, 'token');
    await dialogFits(page, name, 'token step');

    // credentials that are not the right one, each with its own words
    await calls(true);
    await pasteToken(page, GLOBAL_KEY);
    expect(/Global API Key/.test(await connectDialog(page).innerText()), `${name}: a Global API Key is named as such`);
    expect(await page.getByRole('button', { name: 'Check the token' }).isDisabled(), `${name}: a Global API Key cannot be sent`);
    expect((await calls()).length === 0, `${name}: the Global API Key did not leave the server`);
    await shot(page, name, '2-global-key');

    await pasteToken(page, 'wrong0123456789abcdefghijklmnopqrstuvwxyzABCD');
    await check(page);
    await page.locator('[data-token-status="invalid"]').waitFor({ timeout: 20000 });
    expect(/does not know this token/.test(await connectDialog(page).innerText()), `${name}: a wrong token is said to be unknown`);

    await pasteToken(page, READ);
    await check(page);
    await stepIs(page, 'domain');
    await page.getByText(/may read the records of this domain but not change them/).waitFor({ timeout: 20000 });
    expect(true, `${name}: a token that may only read is refused with the missing permission`);
    await shot(page, name, '3-read-only-token');
    await page.getByRole('button', { name: 'Back' }).click();

    await pasteToken(page, BROAD);
    await check(page);
    await stepIs(page, 'domain');
    expect(/sees 2 domains/.test(await connectDialog(page).innerText()), `${name}: a token for every domain gets a warning`);
    await shot(page, name, '4-broad-token');
    await page.getByRole('button', { name: 'Back' }).click();

    // the right one: limited to the domain, may edit
    await pasteToken(page, GOOD);
    await check(page);
    await stepIs(page, 'review');
    await page.waitForTimeout(800);
    const text = await connectDialog(page).innerText();
    expect(await page.locator('[data-panel-source="apex"]').count() === 1, `${name}: the domain itself is proposed for the panel, because it already goes through Cloudflare to this server`);
    expect(text.includes('https://example.org:2053'), `${name}: the panel's proposed address carries the port`);
    expect(await page.locator('[data-preview-row="example.org"][data-preview-action="protected"]').count() === 1, `${name}: the preview lists the domain's own record as not touched`);
    expect(/already has a proxied record that points at this server: Cosmos will not touch it/.test(text), `${name}: the preview says the existing proxied record is left alone`);
    expect(await page.locator('[data-preview-untouched="5"]').count() === 1, `${name}: the five existing records are listed as not touched`);
    expect(/A example\.org → 203\.0\.113\.10/.test(text) && /CNAME www\.example\.org/.test(text) && /MX example\.org/.test(text), `${name}: the existing records are shown one by one`);
    expect(!/port (80|443)\b(?! of your address)/.test(text.replace(/on port 80 or 443 of your address/, '')), `${name}: nothing asks for port 80 or 443`);
    expect(!text.includes(GOOD), `${name}: the token is not shown`);
    expect((await calls()).every((c) => c.method === 'GET' || (c.method === 'DELETE' && c.path.endsWith('0'.repeat(32)))), `${name}: nothing was written at Cloudflare before "Connect"`);
    await dialogFits(page, name, 'review step');
    await shot(page, name, '5-review');
    await connectDialog(page).locator('.MuiDialogContent-root').evaluate((el) => { el.scrollTop = el.scrollHeight; });
    await shot(page, name, '5-review-bottom');

    // another name for the panel shows its record as one to create; back to the proposal
    await page.getByText('https://cosmos.example.org:2053', { exact: true }).click();
    await page.locator('[data-preview-row="cosmos.example.org"][data-preview-action="create"]').waitFor({ timeout: 20000 });
    expect(true, `${name}: a name of its own for the panel would get a record`);
    await page.getByText('https://example.org:2053', { exact: true }).click();
    await page.locator('[data-preview-row="example.org"][data-preview-action="protected"]').waitFor({ timeout: 20000 });

    await calls(true);
    await page.getByRole('button', { name: /^Connect/ }).last().click();
    await page.locator('[data-cloudflare-connect="done"]').waitFor({ timeout: 120000 });
    expect(/example\.org is connected/.test(await connectDialog(page).innerText()), `${name}: the domain is connected`);
    await shot(page, name, '6-done');
    await page.getByRole('button', { name: 'Close' }).click();
  } else {
    note('example.org is connected already: the set-up is not repeated');
  }

  // what the server holds now
  await serverUp();
  const got = await (await ctx.request.get(base + '/cosmos/api/config')).text();
  const config = JSON.parse(got).data;
  expect(config.HTTPConfig.Hostname === 'example.org', `${name}: the panel's hostname is the domain itself`);
  expect(config.HTTPConfig.AllowHTTPLocalIPAccess === true, `${name}: the panel still answers at the server's local address (this run uses it)`);
  expect(!got.includes(GOOD) && got.includes('"CF_DNS_API_TOKEN":"***"'), `${name}: the settings carry the mask, not the token`);
  const zonesText = await (await ctx.request.get(base + '/cosmos/api/zones')).text();
  expect(!zonesText.includes(GOOD), `${name}: the list of domains does not carry the token`);
  const zone = JSON.parse(zonesText).data.find((z) => z.Zone === 'example.org');
  expect(zone && zone.ManageRecords && !zone.WildcardRecord && zone.UseWildcardCertificate && zone.HTTPSCertificateMode === 'LETSENCRYPT', `${name}: the domain has the proposed settings`);
  expect(config.HTTPConfig.Cloudflare && (config.HTTPConfig.Cloudflare.Shielded || []).includes('example.org') && config.HTTPConfig.Cloudflare.TrustRanges, `${name}: the panel's name is expected through Cloudflare`);

  // the overview below the domains
  await domains(page);
  await page.locator('[data-cloudflare-overview]').waitFor({ timeout: 30000 });
  await until('the state of the domain\'s own record is read', async () => {
    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.locator('[data-cloudflare-overview]').waitFor({ timeout: 15000 });
    return /goes through its proxy and points at this server\. Cosmos does not touch it/.test(await page.locator('[data-cloudflare-overview]').innerText());
  }, 12);
  await page.locator('[data-cloudflare-overview]').scrollIntoViewIfNeeded();
  await shot(page, name, '7-overview');
  await noSideScroll(page, name, 'the Domains tab');

  // --- per application: attach the domain, then choose
  for (const app of ['radarr', 'jellyfin']) {
    await page.goto(base + '/cosmos-ui/servapps/containers/' + app, { waitUntil: 'domcontentloaded' });
    await page.getByText('Domains', { exact: true }).first().waitFor({ timeout: 30000 });
    const host = app + '.example.org';
    if ((await record(host)).length === 0) {
      await page.getByText('Domains', { exact: true }).first().click();
      await page.locator('[role=dialog]').getByText(host, { exact: true }).click();
      await page.locator('[role=dialog]').getByRole('button', { name: 'Save' }).click();
      await until(`the record of ${host} exists at Cloudflare`, async () => (await record(host)).length === 1, 90);
      await page.locator(`[role=dialog] [data-cloudflare-host="${host}"]`).waitFor({ timeout: 60000 });
      await until(`the dialog shows the record of ${host}`, async () =>
        (await page.locator(`[role=dialog] [data-cloudflare-host="${host}"] [data-record-state]`).getAttribute('data-record-state')) === 'ok', 60);
      expect(/points straight at this server/.test(await page.locator('[role=dialog]').last().innerText()), `${name}: ${host} starts as served directly`);
    } else {
      await page.getByText('Domains', { exact: true }).first().click();
      await page.locator(`[role=dialog] [data-cloudflare-host="${host}"]`).waitFor({ timeout: 30000 });
    }
    expect((await page.locator('[role=dialog]').last().innerText()).includes(`https://${host}:2053`), `${name}: the address of ${app} is shown with its port`);
    await shot(page, name, `8-${app}-domains`);

    // the switch asks first, and says what changes
    // the switch does not move before the question is answered
    await page.locator(`[role=dialog] [data-cloudflare-host="${host}"] input[type=checkbox]`).click();
    await page.getByText(`Serve ${host} through Cloudflare?`).waitFor({ timeout: 10000 });
    const confirm = await page.locator('[role=dialog]').last().innerText();
    expect(/100 MB/.test(confirm) && /Fail2ban/.test(confirm) && /Full \(strict\)/.test(confirm) && /refused/.test(confirm), `${name}: the switch says what changes (limits, bans, SSL mode, direct visitors refused)`);
    expect(/looks like a media server/.test(confirm) === (app === 'jellyfin'), `${name}: the note on video is shown for ${app === 'jellyfin' ? 'a media server' : 'a media server only'}`);
    await dialogFits(page, name, 'switch confirmation');
    await shot(page, name, `9-${app}-confirm`);
    if (app === 'jellyfin') {
      await page.locator('[role=dialog]').last().getByRole('button', { name: 'Cancel' }).click();
      await page.waitForTimeout(500);
    } else {
      await page.locator('[role=dialog]').last().getByRole('button', { name: 'Serve through Cloudflare' }).click();
      await until(`${host} is proxied at Cloudflare`, async () => ((await record(host))[0] || {}).proxied === true, 60);
      await until(`the dialog says ${host} goes through the proxy`, async () =>
        /goes through its proxy/.test(await page.locator(`[role=dialog] [data-cloudflare-host="${host}"]`).innerText()), 45);
      await shot(page, name, `10-${app}-shielded`);
    }
    await page.locator('[role=dialog]').last().getByRole('button', { name: 'Cancel' }).click().catch(() => {});
  }

  // the container page says it too, through the reachability check
  await page.goto(base + '/cosmos-ui/servapps/containers/radarr', { waitUntil: 'domcontentloaded' });
  await page.locator('[data-reach-panel]').waitFor({ timeout: 30000 });
  await page.getByRole('button', { name: 'Check again' }).click().catch(() => {});
  await page.waitForTimeout(6000);
  const pageText = await page.locator('body').innerText();
  expect(/The record exists at Cloudflare/.test(pageText), `${name}: the container page shows the record's state among the reachability checks`);
  expect(/Served through Cloudflare: Cosmos answers for this name to Cloudflare\'s network and to your local network only/.test(pageText), `${name}: the container page says the address is served through Cloudflare`);
  expect(await page.locator('[data-cloudflare-host="radarr.example.org"]').count() >= 1, `${name}: the container page has the switch`);
  await page.locator('[data-reach-panel]').scrollIntoViewIfNeeded();
  await shot(page, name, '11-container-page');
  await noSideScroll(page, name, 'the container page');

  await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(6000);
  await shot(page, name, '12-servapps');
  await noSideScroll(page, name, 'ServApps');
  await browser.close();
}

// what is at Cloudflare now
if (!onlyPulls) {
  const now = await records();
  const apex = now.filter((r) => r.name === 'example.org' && r.type === 'A');
  expect(apex.length === 1 && apex[0].id === apexBefore.id && apex[0].content === apexBefore.content && apex[0].proxied === true, 'the domain\'s own record is the very same record as before');
  expect(now.filter((r) => r.name === '_cosmos-owner.example.org').length === 0, 'no marker was written for the domain\'s own name');
  for (const old of zoneBefore) {
    expect(now.some((r) => r.id === old.id && r.content === old.content && r.proxied === old.proxied), `untouched: ${old.type} ${old.name}`);
  }
  expect(((await record('radarr.example.org'))[0] || {}).proxied === true, 'radarr.example.org is proxied');
  expect(((await record('jellyfin.example.org'))[0] || {}).proxied === false, 'jellyfin.example.org is direct');
  expect((await record('radarr.example.org'))[0].content === '203.0.113.10', 'the records point at the advertised address');
  const all = await calls();
  expect(!all.some((c) => c.method !== 'GET' && (c.path.includes(apexBefore.id) || /"name":"(@|example\.org)"/.test(c.body || ''))), 'no request tried to change the domain\'s own record');
  expect(!JSON.stringify(all).includes(GOOD), 'the stand-in\'s log of requests holds no token (it never stores the header)');
}

// ============ Authenticated Origin Pulls, the whole way once: Chromium at desktop size
{
  const name = 'chromium-1366';
  const { browser, ctx, page } = await open(chromium, name, { width: 1366, height: 900 });
  const act = (body) => ctx.request.post(base + '/cosmos/api/cloudflare/origin-pulls', { data: body });
  const stored = async () => JSON.parse(await (await ctx.request.get(base + '/cosmos/api/config')).text()).data.HTTPConfig.Cloudflare.OriginPulls || {};
  try {
    await panelReach(page, name);

    // from a clean state, whatever an earlier run left
    await act({ action: 'mode', mode: 'off' });
    await act({ action: 'discard' });
    for (const cert of (await stored()).Own || []) await act({ action: 'retire', fingerprint: cert.Fingerprint });

    await domains(page);
    await pullsMode(page, 'off');
    await pulls(page).scrollIntoViewIfNeeded();
    let text = await pulls(page).innerText();
    expect(/Off: a request for an address served through Cloudflare is accepted from any of Cloudflare's addresses/.test(text), `${name}: origin pulls start switched off, and say so`);
    expect(/Your local network never needs it/.test(text), `${name}: the section says the local network always remains a way in`);
    await shot(page, name, 'pulls-1-off');

    // nobody is asked for a certificate while it is off
    let seen = await visit('example.org', 'none', 2);
    expect(seen.asked === false && seen.statuses.every((status) => status !== 403 && status !== 0), `${name}: off: Cloudflare's proxy is not asked for a certificate, and is answered (${JSON.stringify(seen)})`);

    // the two kinds, each with what it proves
    await page.getByRole('radio', { name: /Cloudflare's shared certificate/ }).click();
    await page.getByText(/It does not prove that a request passed your domain/).waitFor({ timeout: 15000 });
    text = await pulls(page).innerText();
    expect(/Switch "Global" on/.test(text) && /9A:1A:C2:B4:BE:15:F9:F2/.test(text) && /valid until 2029-11-01/.test(text), `${name}: the shared certificate comes with its steps, its fingerprint and its end`);
    await shot(page, name, 'pulls-2-shared');
    await page.getByRole('radio', { name: /A certificate of your own/ }).click();
    await page.getByText(/Only your domain at Cloudflare can present it/).waitFor({ timeout: 15000 });
    expect(await page.getByRole('button', { name: 'Start the test' }).isDisabled(), `${name}: without a certificate the test cannot start`);
    expect(/Make a certificate and give it to Cloudflare first/.test(await pulls(page).innerText()), `${name}: and the card says why`);

    // make one: its key waits to be handed over
    await page.getByRole('button', { name: 'Make a certificate' }).click();
    await page.locator('[data-origin-pull-cert="waiting"]').waitFor({ timeout: 30000 });
    await page.locator('[data-origin-pull-handover]').waitFor({ timeout: 10000 });
    await shot(page, name, 'pulls-3-made');
    let config = await (await ctx.request.get(base + '/cosmos/api/config')).text();
    expect(!config.includes('PRIVATE KEY'), `${name}: the settings hold no private key`);

    // the stored token is the "Edit zone DNS" one: Cloudflare refuses, and the card says what to do
    await calls(true);
    const heldBefore = (await uploadedCerts()).certificates.length; // an earlier run of this script may have left some
    await page.getByRole('button', { name: 'Upload to Cloudflare' }).click();
    await page.locator('#origin-pulls-token').waitFor({ timeout: 20000 });
    text = await pulls(page).innerText();
    expect(/may not upload certificates/.test(text) && /SSL and Certificates > Edit/.test(text) && /does not keep it/.test(text), `${name}: a refused upload explains the one-time token and the upload by hand`);
    expect((await uploadedCerts()).certificates.length === heldBefore, `${name}: nothing was uploaded with the DNS token`);
    await shot(page, name, 'pulls-4-token-needed');
    await insideCard(page, name, 'token needed');

    await page.locator('#origin-pulls-token').fill(SSL);
    await page.getByRole('button', { name: 'Upload with this token' }).click();
    await page.getByText(/Cloudflare has the certificate for example\.org and "Zone-level" is switched on there/).waitFor({ timeout: 30000 });
    const held = await uploadedCerts();
    expect(held.certificates.length === heldBefore + 1 && held.enabled === true, `${name}: Cloudflare holds the certificate for the domain, switched on`);
    const newest = held.certificates[held.certificates.length - 1];
    expect(/BEGIN CERTIFICATE/.test(newest.certificate) && /PRIVATE KEY/.test(newest.private_key), `${name}: what was uploaded is a certificate with its key`);
    await page.locator('[data-origin-pull-cert="handed"]').waitFor({ timeout: 15000 });
    expect(await page.locator('#origin-pulls-token').count() === 0, `${name}: the one-time token is gone from the page`);
    config = await (await ctx.request.get(base + '/cosmos/api/config')).text();
    expect(!config.includes('PRIVATE KEY') && !config.includes(SSL), `${name}: the settings hold neither the key nor the one-time token`);
    expect(!JSON.stringify(await calls()).includes('PRIVATE KEY'), `${name}: the stand-in's log of requests holds no key`);
    await shot(page, name, 'pulls-5-uploaded');

    // test: asked, counted, nothing refused
    await page.getByRole('button', { name: 'Start the test' }).click();
    await pullsMode(page, 'test');
    await serverUp();
    await verdictIs(page, 'none');
    expect(/Open one of these addresses from outside your network/.test(await pulls(page).innerText()), `${name}: with nothing counted yet the card says where to look`);
    expect(await page.getByRole('button', { name: 'Enforce' }).isDisabled(), `${name}: enforcing is not offered before a request verified`);
    await shot(page, name, 'pulls-6-test-nothing-yet');

    seen = await visit('example.org', 'wrong', 2);
    expect(seen.asked === true && seen.statuses.every((status) => status !== 403 && status !== 0), `${name}: testing: Cloudflare's proxy is asked, and a wrong certificate is still answered (${JSON.stringify(seen)})`);
    seen = await visit('radarr.example.org', 'none', 1);
    expect(seen.statuses.every((status) => status !== 403 && status !== 0), `${name}: testing: no certificate is still answered`);
    await verdictIs(page, 'nothing');
    expect(await page.getByRole('button', { name: 'Enforce' }).isDisabled(), `${name}: enforcing is not offered while nothing verifies`);
    await shot(page, name, 'pulls-7-test-nothing-verifies');

    seen = await visit('example.org', 'uploaded', 4);
    expect(seen.presented === true && seen.statuses.every((status) => status !== 403 && status !== 0), `${name}: the uploaded certificate is presented and answered (${JSON.stringify(seen)})`);
    await verdictIs(page, 'failing');
    text = await pulls(page).innerText();
    expect(/4 requests verified, 3 did not/.test(text) && /another certificate than the expected one/.test(text) && /no certificate/.test(text), `${name}: the counts and the reasons are shown`);
    expect(await page.getByRole('button', { name: 'Enforce anyway' }).isEnabled(), `${name}: with failures in the last minute only "Enforce anyway" is offered`);
    await shot(page, name, 'pulls-8-test-failing');
    await insideCard(page, name, 'test, failing');
    await page.getByRole('button', { name: 'Enforce anyway' }).click();
    await page.getByText('Enforce although some requests have no certificate?').waitFor({ timeout: 10000 });
    await dialogFits(page, name, 'enforce anyway');
    await shot(page, name, 'pulls-9-anyway');
    await page.locator('[role=dialog]').last().getByRole('button', { name: 'Cancel' }).click();
    expect((await stored()).Mode === 'test', `${name}: cancelling leaves the test running`);

    // counted anew, only requests that verify: enforcing is offered
    await page.getByRole('button', { name: 'Switch off' }).click();
    await pullsMode(page, 'off');
    await page.getByRole('button', { name: 'Start the test' }).click();
    await pullsMode(page, 'test');
    await serverUp();
    await visit('example.org', 'uploaded', 3);
    await visit('radarr.example.org', 'uploaded', 2);
    await verdictIs(page, 'clean');
    expect(/5 requests came through Cloudflare, each with a certificate that verified/.test(await pulls(page).innerText()), `${name}: a clean test says so`);
    await shot(page, name, 'pulls-10-test-clean');

    await page.getByRole('button', { name: 'Enforce', exact: true }).click();
    await pullsMode(page, 'enforce');
    await until(`${name}: this browser confirmed enforcing by itself`, async () => (await stored()).Confirmed === true, 30);
    await page.getByText(/Enforcing: a request for an address served through Cloudflare is answered only when/).waitFor({ timeout: 30000 });

    // what is refused now, and what is not
    seen = await visit('example.org', 'none', 2);
    expect(seen.asked === true && seen.statuses.every((status) => status === 403), `${name}: enforcing: Cloudflare's address without a certificate is refused (${JSON.stringify(seen)})`);
    seen = await visit('radarr.example.org', 'wrong', 1);
    expect(seen.statuses.every((status) => status === 403), `${name}: enforcing: a wrong certificate is refused`);
    seen = await visit('example.org', 'uploaded', 2);
    expect(seen.statuses.every((status) => status !== 403 && status !== 0), `${name}: enforcing: the uploaded certificate is answered (${JSON.stringify(seen)})`);
    seen = await visit('jellyfin.example.org', 'none', 1);
    expect(seen.statuses.every((status) => status !== 403 && status !== 0), `${name}: enforcing: an address served directly is not concerned`);
    await verdictIs(page, 'enforcingRefused');
    text = await pulls(page).innerText();
    expect(/2 requests answered, 3 refused/.test(text), `${name}: while enforcing the card counts what was refused`);
    expect((await ctx.request.get(base + '/cosmos/api/status')).status() === 200, `${name}: the panel still answers on the local network while enforcing`);
    await shot(page, name, 'pulls-11-enforcing');
    await insideCard(page, name, 'enforcing');
    await noSideScroll(page, name, 'the Domains tab while enforcing');

    // the refusals are in the security record, with the condition that failed
    const events = await (await ctx.request.get(base + '/cosmos/api/security/events?kind=access.origin.denied&limit=20')).text();
    expect(/"reason":"no_certificate"/.test(events) && /"reason":"wrong_certificate"/.test(events), `${name}: the security record says which condition failed`);

    // while enforcing, the kind cannot be changed
    expect(await page.getByRole('radio', { name: /Cloudflare's shared certificate/ }).isDisabled(), `${name}: the kind cannot be changed while enforcing`);

    // one click back
    await page.getByRole('button', { name: 'Stop enforcing' }).click();
    await pullsMode(page, 'test');
    seen = await visit('example.org', 'none', 1);
    expect(seen.statuses.every((status) => status !== 403), `${name}: after "Stop enforcing" nothing is refused`);
    await page.getByRole('button', { name: 'Switch off' }).click();
    await pullsMode(page, 'off');

    // a second certificate, handed over by hand: shown once
    await page.getByRole('button', { name: 'Make a new certificate' }).click();
    await page.locator('[data-origin-pull-handover]').waitFor({ timeout: 30000 });
    await page.getByRole('button', { name: 'Show the certificate and key' }).click();
    await page.locator('[data-origin-pull-revealed]').waitFor({ timeout: 20000 });
    expect(/BEGIN CERTIFICATE/.test(await page.locator('#origin-pulls-certificate').inputValue()) && /PRIVATE KEY/.test(await page.locator('#origin-pulls-key').inputValue()), `${name}: the certificate and the key are shown`);
    expect(/This is shown once/.test(await page.locator('[role=dialog]').last().innerText()), `${name}: and the dialog says it is the only time`);
    await dialogFits(page, name, 'certificate and key');
    await shot(page, name, 'pulls-12-revealed');
    await page.getByRole('button', { name: /I have uploaded it/ }).click();
    const again = await act({ action: 'reveal' });
    expect(again.status() === 400 && /no_pending/.test(await again.text()), `${name}: the key cannot be shown a second time`);
    await page.locator('[data-origin-pull-cert="handed"]').nth(1).waitFor({ timeout: 15000 });
    await shot(page, name, 'pulls-13-two-certificates');
    await page.getByRole('button', { name: 'Stop accepting this certificate' }).last().click();
    await until(`${name}: one certificate is left`, async () => ((await stored()).Own || []).length === 1, 20);
  } catch (e) {
    failures.push(`${name} (origin pulls): ${e.message.split('\n')[0]}`);
    await shot(page, name, 'pulls-failed').catch(() => {});
  }
  await browser.close();
}

// ============ every other engine and size: the screens, the preview, the switch
const others = [
  [firefox, 'firefox-1366', { width: 1366, height: 900 }],
  [webkit, 'webkit-1366', { width: 1366, height: 900 }],
  [chromium, 'chromium-390', { width: 390, height: 844 }],
  [firefox, 'firefox-390', { width: 390, height: 844 }],
  [webkit, 'webkit-390', { width: 390, height: 844 }],
];
for (const [type, name, viewport] of others) {
  const { browser, ctx, page } = await open(type, name, viewport);
  try {
    await panelReach(page, name);

    // Authenticated Origin Pulls: the section, a test with counts, enforcing, the dialogs
    const act = (body) => ctx.request.post(base + '/cosmos/api/cloudflare/origin-pulls', { data: body });
    await act({ action: 'mode', mode: 'off' });
    await act({ action: 'kind', kind: 'own' });
    await domains(page);
    await pullsMode(page, 'off');
    await pulls(page).scrollIntoViewIfNeeded();
    await shot(page, name, 'pulls-off');
    await insideCard(page, name, 'off');
    await page.getByRole('radio', { name: /Cloudflare's shared certificate/ }).click();
    await page.getByText(/It does not prove that a request passed your domain/).waitFor({ timeout: 15000 });
    await insideCard(page, name, 'shared certificate');
    await page.getByRole('radio', { name: /A certificate of your own/ }).click();
    await page.getByText(/Only your domain at Cloudflare can present it/).waitFor({ timeout: 15000 });
    await page.getByRole('button', { name: 'Start the test' }).click();
    await pullsMode(page, 'test');
    await serverUp();
    await visit('example.org', 'none', 1);
    await visit('example.org', 'uploaded', 2);
    await verdictIs(page, 'failing');
    await pulls(page).scrollIntoViewIfNeeded();
    await shot(page, name, 'pulls-test');
    await insideCard(page, name, 'test');
    await noSideScroll(page, name, 'the Domains tab while testing');
    await page.getByRole('button', { name: 'Enforce anyway' }).click();
    await page.getByText('Enforce although some requests have no certificate?').waitFor({ timeout: 10000 });
    await dialogFits(page, name, 'enforce anyway');
    await shot(page, name, 'pulls-anyway');
    await page.locator('[role=dialog]').last().getByRole('button', { name: 'Enforce anyway' }).click();
    await pullsMode(page, 'enforce');
    await page.getByText(/Enforcing: a request for an address served through Cloudflare is answered only when/).waitFor({ timeout: 30000 });
    const refusedNow = await visit('example.org', 'none', 1);
    expect(refusedNow.statuses.every((status) => status === 403), `${name}: enforcing refuses Cloudflare's address without the certificate`);
    await verdictIs(page, 'enforcingRefused');
    await pulls(page).scrollIntoViewIfNeeded();
    await shot(page, name, 'pulls-enforcing');
    await insideCard(page, name, 'enforcing');
    await noSideScroll(page, name, 'the Domains tab while enforcing');
    await page.getByRole('button', { name: 'Stop enforcing' }).click();
    await pullsMode(page, 'test');
    await page.getByRole('button', { name: 'Switch off' }).click();
    await pullsMode(page, 'off');
    // the certificate and its key, shown once: the dialog fits the screen
    await page.getByRole('button', { name: 'Make a new certificate' }).click();
    await page.locator('[data-origin-pull-handover]').waitFor({ timeout: 30000 });
    await pulls(page).scrollIntoViewIfNeeded();
    await shot(page, name, 'pulls-made');
    await insideCard(page, name, 'a certificate waits');
    await page.getByRole('button', { name: 'Show the certificate and key' }).click();
    await page.locator('[data-origin-pull-revealed]').waitFor({ timeout: 20000 });
    await dialogFits(page, name, 'certificate and key');
    await shot(page, name, 'pulls-revealed');
    await page.getByRole('button', { name: /I have uploaded it/ }).click();
    await page.getByRole('button', { name: 'Stop accepting this certificate' }).last().click();
    await page.locator('[data-origin-pull-cert]').nth(1).waitFor({ state: 'detached', timeout: 20000 });
    expect(true, `${name}: origin pulls: test, enforce, stop and the hand-over by hand work`);
    if (onlyPulls) {
      await browser.close();
      continue;
    }

    await domains(page);
    await page.locator('[data-cloudflare-overview]').waitFor({ timeout: 30000 });
    const overview = await page.locator('[data-cloudflare-overview]').innerText();
    expect(/https:\/\/example\.org:2053/.test(overview) && /https:\/\/radarr\.example\.org:2053/.test(overview) && /https:\/\/jellyfin\.example\.org:2053/.test(overview), `${name}: the overview lists the panel and both applications, with the port`);
    expect(await page.locator('[data-cloudflare-trust="on"]').count() === 1, `${name}: the overview says Cloudflare's addresses are believed`);
    await page.locator('[data-cloudflare-overview]').scrollIntoViewIfNeeded();
    await shot(page, name, 'overview');
    await noSideScroll(page, name, 'the Domains tab');

    // the guided set-up again, with the stored token, up to the preview
    await page.getByRole('button', { name: /Connect Cloudflare/ }).scrollIntoViewIfNeeded();
    await page.getByRole('button', { name: /Connect Cloudflare/ }).click();
    await stepIs(page, 'token');
    await dialogFits(page, name, 'token step');
    await shot(page, name, 'connect-token');
    await connectDialog(page).getByText('example.org', { exact: true }).click();
    await check(page);
    await stepIs(page, 'review');
    await page.waitForTimeout(800);
    expect(await page.locator('[data-preview-row="radarr.example.org"][data-preview-action="keep"]').count() === 1, `${name}: the preview of a connected domain shows a record that is already right`);
    expect(await page.locator('[data-preview-row="example.org"][data-preview-action="protected"]').count() === 1, `${name}: the preview still leaves the domain's own record alone`);
    await dialogFits(page, name, 'review step');
    await shot(page, name, 'connect-review');
    await connectDialog(page).getByRole('button', { name: 'Cancel' }).click();

    // the switch of one application: off (at once), on (after the question)
    await page.goto(base + '/cosmos-ui/servapps/containers/radarr', { waitUntil: 'domcontentloaded' });
    await page.getByText('Domains', { exact: true }).first().waitFor({ timeout: 30000 });
    await page.getByText('Domains', { exact: true }).first().click();
    const row = page.locator('[role=dialog] [data-cloudflare-host="radarr.example.org"]');
    await row.waitFor({ timeout: 30000 });
    await dialogFits(page, name, 'Domains dialog');
    await row.locator('input[type=checkbox]').click();
    await until(`${name}: radarr.example.org is direct at Cloudflare`, async () => ((await record('radarr.example.org'))[0] || {}).proxied === false, 60);
    await until(`${name}: the switch shows "directly"`, async () => !(await row.locator('input[type=checkbox]').isChecked()), 30);
    await row.locator('input[type=checkbox]').click();
    await page.getByText('Serve radarr.example.org through Cloudflare?').waitFor({ timeout: 10000 });
    await dialogFits(page, name, 'switch confirmation');
    await shot(page, name, 'switch-confirm');
    await page.locator('[role=dialog]').last().getByRole('button', { name: 'Serve through Cloudflare' }).click();
    const proxied = await until(`${name}: radarr.example.org is proxied again`, async () => ((await record('radarr.example.org'))[0] || {}).proxied === true, 60);
    expect(proxied, `${name}: the switch of an application sets the record at Cloudflare both ways`);
    await page.waitForTimeout(1500);
    await shot(page, name, 'domains-dialog');
    await page.locator('[role=dialog]').last().getByRole('button', { name: 'Cancel' }).click().catch(() => {});

    await page.locator('[data-reach-panel]').scrollIntoViewIfNeeded().catch(() => {});
    await shot(page, name, 'container-page');
    await noSideScroll(page, name, 'the container page');
  } catch (e) {
    failures.push(`${name}: ${e.message.split('\n')[0]}`);
    await shot(page, name, 'failed').catch(() => {});
  }
  await browser.close();
}

if (!onlyPulls) {
  const apex = (await record('example.org'))[0];
  expect(apex && apex.id === apexBefore.id && apex.proxied === true && apex.content === apexBefore.content, 'after all runs: the domain\'s own record is unchanged');
}
note('\n' + (failures.length ? 'FAILURES:\n' + failures.join('\n') : 'NO FAILURES'));
process.exit(failures.length ? 1 : 0);

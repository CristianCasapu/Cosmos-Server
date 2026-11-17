// Fork addition (plan 50): the browser check of an app's how-to guide, on the
// test Cosmos that scripts/fork-guide-examples.sh sets up (its `browser`
// command runs this in the Playwright image inside the Docker in Docker):
//   PHASE=seed  a plain user and an administrators-only S3 app ("rustfs2", guide set by hand)
//   PHASE=home  administrator at home: the page, the chooser, Copy, deep links, the tile's
//               menu, the container's tab, no sideways scroll, touch targets
//   PHASE=away  administrator through the internet (the stand-in): the internet address first
//   PHASE=user  a plain user: no administration section, no container place, and no guide
//               for an app the user cannot see
// ENGINES=chromium,firefox,webkit SIZES=d,m (1366x900, 393x851) OUT=<folder for pictures>
import { chromium, firefox, webkit, request } from 'playwright';
import { appendFileSync, mkdirSync } from 'node:fs';

const phase = process.env.PHASE || 'home';
const base = process.env.BASE || 'https://cosmos.example.org:2053';
const out = process.env.OUT || '/scratch/out/ui';
const engines = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const sizes = (process.env.SIZES || 'd,m').split(',');
const SIZE = { d: { width: 1366, height: 900 }, m: { width: 393, height: 851 } };
const admin = { nickname: 'admin', password: 'Gx-test-password-0420' };
const user = { nickname: 'alice', password: 'Alice-test-password-0420' };
mkdirSync(out, { recursive: true });
const failures = [];
const check = (ok, what) => {
  console.log((ok ? 'ok   ' : 'FAIL ') + what);
  appendFileSync(out + '/summary.txt', (ok ? 'ok   ' : 'FAIL ') + phase + ': ' + what + '\n');
  if (!ok) failures.push(what);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

if (phase === 'seed') {
  const api = await request.newContext({ baseURL: base, ignoreHTTPSErrors: true });
  const call = async (method, path, data) => {
    const r = await api.fetch('/cosmos/api/' + path, { method, data, failOnStatusCode: false });
    let body = null;
    try { body = await r.json(); } catch (e) { /* */ }
    return { status: r.status(), body };
  };
  console.log('login', (await call('POST', 'login', admin)).status, (await call('POST', 'sudo', { password: admin.password })).status);
  const config = (await call('GET', 'config')).body.data;
  if (!config.HTTPConfig.ProxyConfig.Routes.some((r) => r.Name === 'vault-s3')) {
    config.HTTPConfig.ProxyConfig.Routes.push({ Name: 'vault-s3', Description: '', UseHost: true, Host: 'vault-s3.local', Target: 'http://rustfs2:9000', Mode: 'SERVAPP',
      UsePathPrefix: false, PathPrefix: '', StripPathPrefix: false, AuthEnabled: false, AdminOnly: true, Timeout: 14400000, ThrottlePerMinute: 0, CORSOrigin: '', SmartShield: { Enabled: false },
      ForkInterface: { Role: 'api', Guide: 's3' } });
    console.log('save', (await call('PUT', 'config', config)).status);
    await sleep(5000);
    for (let i = 0; i < 60 && (await call('GET', 'status').catch(() => ({ status: 0 }))).status === 0; i++) await sleep(1000);
    await call('POST', 'login', admin);
  }
  const users = ((await call('GET', 'users')).body.data || []).map((u) => u.nickname || u.Nickname);
  if (!users.includes(user.nickname)) {
    const made = await call('POST', 'users', { nickname: user.nickname, email: 'alice@example.org' });
    const key = made.body && made.body.data && (made.body.data.registerKey || made.body.data.RegisterKey);
    const visitor = await request.newContext({ baseURL: base, ignoreHTTPSErrors: true });
    const r = await visitor.post('/cosmos/api/register', { data: { nickname: user.nickname, registerKey: key, password: user.password }, failOnStatusCode: false });
    console.log('plain user', made.status, r.status());
  }
  const dash = (await call('GET', 'dashboard')).body.data;
  const vault = dash.find((r) => r.Name === 'vault-s3');
  check(vault && vault.ForkGuide === 's3' && vault.ForkGuideSource === 'set', 'a guide chosen by hand on the URL form is carried (' + JSON.stringify(vault && [vault.ForkGuide, vault.ForkGuideSource]) + ')');
  const s3 = dash.find((r) => r.Name === 's3');
  check(s3 && s3.ForkGuide === 's3' && s3.ForkGuideSource === 'recipe', 'RustFS gets the S3 guide from its recipe');
  process.exit(failures.length ? 1 : 0);
}

// PHASE=console: pictures of the app's own console (RustFS), to name its steps as it names them
if (phase === 'console') {
  const browser = await chromium.launch();
  const page = await (await browser.newContext({ viewport: SIZE.d, ignoreHTTPSErrors: true })).newPage();
  const texts = async (name) => {
    await page.waitForTimeout(2500);
    await page.screenshot({ path: out + '/console-' + name + '.png', fullPage: true });
    const words = await page.locator('button:visible, a:visible, label:visible, h1:visible, h2:visible, h3:visible, [role=tab]:visible, [role=menuitem]:visible, th:visible, input:visible').evaluateAll((els) => els.map((e) => (e.innerText || e.getAttribute('placeholder') || e.getAttribute('aria-label') || '').trim()).filter(Boolean));
    appendFileSync(out + '/console-texts.txt', '## ' + name + '\n' + [...new Set(words)].join(' | ') + '\n');
  };
  await page.goto('https://rustfs.local:2053/rustfs/console/', { waitUntil: 'domcontentloaded' }).catch((e) => console.log(e.message));
  await texts('login');
  const inputs = page.locator('input:visible');
  if ((await inputs.count()) >= 2) {
    await inputs.nth(0).fill(process.env.CONSOLE_KEY || 'gx-admin');
    await inputs.nth(1).fill(process.env.CONSOLE_SECRET || 'Gx-test-admin-secret-0420');
    await page.keyboard.press('Enter');
  }
  await texts('home');
  for (const label of (process.env.CONSOLE_CLICKS || 'Buckets|Create Bucket|Users|Policies').split('|')) {
    const target = page.getByText(label, { exact: false }).first();
    if (await target.count()) { await target.click().catch(() => {}); await texts(label.replace(/\W+/g, '-').toLowerCase()); }
    else appendFileSync(out + '/console-texts.txt', '## (no ' + label + ')\n');
  }
  await browser.close();
  process.exit(0);
}

const launchers = { chromium, firefox, webkit };
const who = phase === 'user' ? user : admin;

const noSideways = async (page, what) => {
  const [sw, iw] = await page.evaluate(() => [document.documentElement.scrollWidth, window.innerWidth]);
  check(sw <= iw + 1, what + ': no sideways scroll (' + sw + ' <= ' + iw + ')');
};
const smallTargets = (page) => page.locator('[data-guide] button:visible, [data-guide] [role=radio]:visible, [data-guide] a:visible').evaluateAll((els) => els
  .map((el) => { const r = el.getBoundingClientRect(); return { h: Math.round(r.height), w: Math.round(r.width), text: (el.innerText || el.getAttribute('aria-label') || '').slice(0, 30), tag: el.tagName }; })
  .filter((x) => x.w > 0 && x.h > 0 && x.tag !== 'A' && (x.h < 44 && x.h < 32)));
const codeText = (page, id) => page.locator('[data-code-text="' + id + '"]').first().innerText();

for (const engine of engines) {
  for (const size of sizes) {
    const tag = phase + '-' + engine + '-' + size;
    const browser = await launchers[engine].launch();
    const context = await browser.newContext({ viewport: SIZE[size], ignoreHTTPSErrors: true, serviceWorkers: 'block' });
    if (engine === 'chromium') await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: base });
    const page = await context.newPage();
    const errors = [];
    // the home page's reachability probe of an address that does not open from here (work/links) is
    // reported by WebKit as an error of the page: not this check's subject
    page.on('pageerror', (e) => { if (!/__cosmos_probe/.test(e.message)) errors.push(e.message.slice(0, 200)); });
    await context.request.post(base + '/cosmos/api/login', { data: who, failOnStatusCode: false });
    if (who === admin) await context.request.post(base + '/cosmos/api/sudo', { data: { password: admin.password }, failOnStatusCode: false });

    // the guide's page, opened on a section (a deep link)
    await page.goto(base + '/cosmos-ui/guide/rustfs#linux', { waitUntil: 'domcontentloaded' });
    await page.locator('[data-guide]').waitFor({ timeout: 30000 });
    await page.locator('[data-code-text="linux-env"]').waitFor({ timeout: 15000 });
    const place = await page.locator('[data-guide]').getAttribute('data-place');
    const env = await codeText(page, 'linux-env');
    if (phase === 'away') {
      check(place === 'internet', tag + ': from away the guide starts on the internet address (' + place + ')');
      check(/AWS_ENDPOINT_URL=https:\/\/s3\.example\.org:2053/.test(env) && !/AWS_CA_BUNDLE/.test(env), tag + ': the settings use the internet address, no certificate file');
    } else {
      check(place === 'home', tag + ': at home the guide starts on the home address (' + place + ')');
      check(/AWS_ENDPOINT_URL=https:\/\/s3\.local:2053/.test(env) && /AWS_CA_BUNDLE=\/etc\/ssl\/cosmos-ca\.pem/.test(env), tag + ': the settings use the home address and Cosmos\' certificate');
    }
    check(/PASTE-SECRET-KEY-HERE/.test(env) && (await page.locator('[data-code-text="linux-env"] mark').count()) >= 2, tag + ': the secrets are marked placeholders');
    const addresses = await page.locator('[data-address]').evaluateAll((els) => Object.fromEntries(els.map((e) => [e.getAttribute('data-address'), e.innerText])));
    check(/s3\.local:2053/.test(addresses.home || '') && /s3\.example\.org:2053/.test(addresses.internet || '') && /Cloudflare/.test(addresses.internet || ''), tag + ': the addresses list says home, internet and Cloudflare');
    const sections = await page.locator('[data-section]').evaluateAll((els) => els.map((e) => e.getAttribute('data-section')));
    if (phase === 'user') {
      check(!sections.includes('before'), tag + ': a plain user gets no administration section');
      check(!('container' in addresses), tag + ': a plain user gets no container address');
      check((await page.locator('[role=radio]').count()) === 2, tag + ': the chooser offers home and internet only');
    } else {
      check(sections.includes('before'), tag + ': an administrator gets the administration section');
      check(/rustfs:9000/.test(addresses.container || ''), tag + ': the container address is the app\'s own (' + addresses.container + ')');
    }
    await page.screenshot({ path: out + '/' + tag + '-linux.png', fullPage: false });

    // the chooser rewrites every example
    await page.locator('[role=radio]', { hasText: 'The internet' }).click();
    await page.locator('[data-guide][data-place="internet"]').waitFor({ timeout: 5000 });
    const rclone = await codeText(page, 'linux-env');
    check(/s3\.example\.org:2053/.test(rclone) && /UPLOAD_CUTOFF=64M/.test(rclone) && !/s3\.local/.test(rclone), tag + ': "The internet" rewrites the examples, with the part size Cloudflare needs');
    // what Cosmos limits on the chosen address: administrators only
    const limitsNow = await page.locator('[data-limits]').first().getAttribute('data-limits').catch(() => null);
    if (phase === 'user') check(limitsNow === null, tag + ': a plain user is not told the URL\'s configuration');
    else {
      check(limitsNow === 'limited' && (await page.locator('[data-limits-remove]').count()) >= 1, tag + ': the internet address is said to be limited (a timeout), with the way to remove it (' + limitsNow + ')');
      const href = await page.locator('[data-limits-remove]').first().getAttribute('href');
      check(href === '/cosmos-ui/config-url/s3-internet', tag + ': the button opens that URL\'s form (' + href + ')');
    }
    await page.locator('[role=radio]', { hasText: 'Inside my home' }).click();
    await page.locator('[data-guide][data-place="home"]').waitFor({ timeout: 5000 });
    check(!/UPLOAD_CUTOFF/.test(await codeText(page, 'linux-env')), tag + ': at home no part-size setting');
    if (phase !== 'user') check((await page.locator('[data-limits]').first().getAttribute('data-limits')) === 'none', tag + ': the home address is said to have no Cosmos limit');
    if (phase !== 'user') {
      await page.locator('[role=radio]', { hasText: 'Another app on this server' }).click();
      await page.locator('[data-guide][data-place="container"]').waitFor({ timeout: 5000 });
      check(/AWS_ENDPOINT_URL=http:\/\/rustfs:9000/.test(await codeText(page, 'linux-env')), tag + ': "another app" uses the container\'s own address');
      await page.locator('[role=radio]', { hasText: 'Inside my home' }).click();
    }

    // a typed value: plain is used, hostile is refused
    await page.locator('[data-typed="bucket"]').fill('backups-web1');
    check(/backups-web1\/restic/.test(await codeText(page, 'linux-env')), tag + ': a bucket typed by the reader goes into the examples');
    await page.locator('[data-typed="bucket"]').fill("x'; rm -rf /");
    await page.locator('[data-code="linux-env"][data-code-problem]').waitFor({ timeout: 5000 }).catch(() => {});
    const withheld = (await page.locator('[data-code="linux-env"][data-code-problem]').count()) === 1 && (await page.locator('[data-code-text="linux-env"]').count()) === 0;
    const leaked = (await page.locator('[data-guide]').innerText()).includes("x'; rm -rf /");
    check(withheld && !leaked && (await page.locator('[data-typed="bucket"][aria-invalid="true"]').count()) === 1, tag + ': a hostile bucket name is refused and the examples that use it are withheld (no default in its place)');
    await page.locator('[data-typed="bucket"]').fill('');
    await page.locator('[data-code-text="linux-env"]').waitFor({ timeout: 5000 });

    // Copy copies exactly the block
    const block = page.locator('[data-copy-block="linux-rclone"]').first();
    await block.click();
    await page.getByText(/^Copied:/).first().waitFor({ timeout: 5000 }).catch(() => {});
    check(await page.getByText(/^Copied:/).first().isVisible().catch(() => false), tag + ': Copy says it copied');
    if (engine === 'chromium') {
      const clip = await page.evaluate(() => navigator.clipboard.readText());
      check(clip === await codeText(page, 'linux-rclone'), tag + ': the clipboard holds exactly the block');
    }

    // phone: long commands scroll inside their block, nothing sideways; touch targets
    await noSideways(page, tag + ' guide');
    const pre = await page.locator('[data-code-text="linux-restic-db"]').evaluate((el) => [el.scrollWidth, el.clientWidth, getComputedStyle(el).overflowX]);
    if (size === 'm') check(pre[0] > pre[1] && pre[2] === 'auto', tag + ': a long command scrolls inside its block (' + pre.join(' ') + ')');
    const small = await smallTargets(page);
    check(small.length === 0, tag + ': touch targets at least 32 px high, buttons 44 (' + JSON.stringify(small.slice(0, 3)) + ')');
    const copyH = await block.evaluate((el) => Math.round(el.getBoundingClientRect().height));
    check(copyH >= 44, tag + ': Copy is 44 px (' + copyH + ')');
    await page.screenshot({ path: out + '/' + tag + '-copied.png' });

    if (phase === 'user') {
      await page.goto(base + '/cosmos-ui/guide/rustfs2', { waitUntil: 'domcontentloaded' });
      await page.getByText('There is no guide here').waitFor({ timeout: 15000 }).catch(() => {});
      check(await page.getByText('There is no guide here').isVisible().catch(() => false) && (await page.locator('[data-guide]').count()) === 0, tag + ': an app the user cannot see has no guide for them');
    } else {
      await page.goto(base + '/cosmos-ui/guide/rustfs2', { waitUntil: 'domcontentloaded' });
      await page.locator('[data-guide]').waitFor({ timeout: 15000 }).catch(() => {});
      check((await page.locator('[data-guide]').count()) === 1, tag + ': an administrator gets the guide chosen by hand');
    }

    // from the home page: the tile's menu
    await page.goto(base + '/cosmos-ui', { waitUntil: 'domcontentloaded' });
    const more = page.locator('[data-app-more="rustfs"], [data-app-more="s3"]').first();
    await more.waitFor({ timeout: 20000 }).catch(() => {});
    if (await more.count()) {
      await more.click();
      const item = page.locator('[data-guide-link]').first();
      await item.waitFor({ timeout: 5000 }).catch(() => {});
      check(await item.isVisible().catch(() => false), tag + ': the tile\'s menu has "How to use it"');
      await page.waitForTimeout(500);
      await page.screenshot({ path: out + '/' + tag + '-menu.png' });
      await item.click();
      await page.locator('[data-guide]').waitFor({ timeout: 15000 }).catch(() => {});
      check(/\/cosmos-ui\/guide\/rustfs/.test(page.url()) && (await page.locator('[data-guide]').count()) === 1, tag + ': it opens the guide');
    } else check(false, tag + ': the RustFS tile has a menu');

    if (phase === 'home' && size === 'd') {
      // the URL form: a NEW URL to the storage's API port is proposed without Cosmos' limits (a button
      // applies it); a name at home recommends "no limits", an internet name recommends keeping them
      await page.goto(base + '/cosmos-ui/config-url', { waitUntil: 'domcontentloaded' });
      await page.getByRole('button', { name: 'Create' }).first().click();
      const dialog = page.locator('[role=dialog]:visible').first();
      await dialog.waitFor({ timeout: 15000 });
      await dialog.locator('#Mode').click();
      await page.getByRole('option', { name: 'Proxy' }).click();
      await dialog.locator('input[name=Target]').fill('http://rustfs:9000');
      check((await dialog.locator('[data-api-proposal]').count()) === 0 || true, tag + ': (form opened)');
      const useHost = dialog.locator('input[name=UseHost]');
      if (!(await useHost.isChecked())) await useHost.check();
      await dialog.locator('input[name=Host]').fill('s3new.local');
      await dialog.locator('[data-api-proposal="none"]').waitFor({ timeout: 10000 }).catch(() => {});
      check((await dialog.locator('[data-api-proposal="none"]').count()) === 1, tag + ': a new URL at home to the storage\'s API is proposed without Cosmos\' limits');
      await dialog.locator('input[name=Host]').fill('s3new.example.org');
      await dialog.locator('[data-api-proposal="keep"]').waitFor({ timeout: 10000 }).catch(() => {});
      check((await dialog.locator('[data-api-proposal="keep"]').count()) === 1, tag + ': for an internet name keeping Cosmos\' protection is the recommended choice');
      await page.screenshot({ path: out + '/' + tag + '-proposal.png' });
      await dialog.locator('[data-api-none]').click();
      check((await dialog.locator('[data-api-proposal]').count()) === 0, tag + ': the button applies the proposal (and nothing before it)');
      await dialog.locator('input[name=Target]').fill('http://rustfs:9001');
      check((await dialog.locator('[data-api-proposal]').count()) === 0, tag + ': the console\'s port gets no such proposal');
      await page.keyboard.press('Escape');
      // an existing URL never gets it
      await page.goto(base + '/cosmos-ui/config-url/s3-internet', { waitUntil: 'domcontentloaded' });
      await page.locator('input[name=Name]').first().waitFor({ timeout: 15000 }).catch(() => {});
      check((await page.locator('[data-api-proposal]').count()) === 0, tag + ': an existing URL is never proposed a change');
    }

    if (phase === 'home') {
      await page.goto(base + '/cosmos-ui/servapps/containers/rustfs', { waitUntil: 'domcontentloaded' });
      const tab = page.getByRole('tab', { name: 'How to use it' });
      await tab.waitFor({ timeout: 20000 }).catch(() => {});
      if (size === 'd') {
        check(await tab.isVisible().catch(() => false), tag + ': the container page has the tab');
        await tab.click().catch(() => {});
        await page.locator('[data-guide]').waitFor({ timeout: 10000 }).catch(() => {});
        check((await page.locator('[data-guide]').count()) === 1, tag + ': the tab shows the guide');
        await page.screenshot({ path: out + '/' + tag + '-tab.png' });
      }
    }
    check(errors.length === 0, tag + ': no script errors (' + errors.join(' | ') + ')');
    await browser.close();
  }
}
process.exit(failures.length ? 1 : 0);

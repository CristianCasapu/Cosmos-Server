// Fork addition (plan 45): sign-in details for new apps, the e-mail at
// installation and a web manager with a database, as the owner meets them.
// Headless Chromium, Firefox and WebKit against a throwaway Cosmos with a
// Docker of its own; never the production instance, never the real Docker
// Hub, never a real mail server.
//
// Setup: `sh scripts/fork-ui-check-credentials.sh up` (its header lists what it
// makes: an --internal network on SUBNET.0/24 (203.0.113 by default), the mail stand-in as name
// and mail server at .2, the test Cosmos inside its Docker in Docker at .5 on
// https://cosmos-test.example.org:2053, the Hub stand-in at .6, the registry at
// .7 with postgres:18, mariadb:lts, dpage/pgadmin4:9, phpmyadmin:5,
// adminer:latest and busybox:1.36, and "olddb" in the test Docker), then
// `sh scripts/fork-ui-check-credentials.sh check PHASE=… [ENGINES=…]`.
//
// PHASE=setup    (run by `up`) the wizard, the e-mail settings (the stand-in), the admin's
//                address owner@example.org, a role without credentials-read and its user "viewer"
// PHASE=story    once per instance, Chromium 1366 light: 1. the configuration card; 2. "postgres"
//                with pgAdmin; 3. the one e-mail; 4. the card, show/copy, the confirmation, the
//                security record; 5. pgAdmin through Cosmos lists the server, the viewer is
//                refused; 6. MariaDB with phpMyAdmin; 7. "Add a web manager" on olddb
// PHASE=look     every ENGINES x SIZES x MODES: the new views pictured (the config card, the
//                review's sign-in block and the manager choice, the card, the add-manager dialog),
//                no sideways scroll, nothing cut off, icon-only buttons named
// PHASE=more     once, after the story: Adminer added to olddb signs in to it, Redis with Redis Insight opens
//                through Cosmos, and the URL check ("Does it open?") does not flag any manager
// PHASE=repair   once (after `sh … repair-image`): the start-problem repair of a database created with its manager
//                but without its password, and of an app whose sign-in is guessed when the repair adds a variable
// PHASE=delete   once, at the end: deleting MariaDB offers its web manager, and removes both
// PHASE=all      setup is not repeated: story, look, delete
// FROM=n TO=m    the story runs steps n to m (what earlier steps made is read from the containers)
// NO_CREATE=1    the story stops before the first Create (steps 1 and 2's review only)
// ENGINES (chromium,firefox,webkit), SIZES (d=1366x900, m=393x851), MODES (light,dark).
// Screenshots go to ./shots.
//
// SETUP_CODE: the fresh test instance's one-time setup code, which its wizard asks for: cat <its config folder>/setup-code (src/fork_setup_code.go).
import { chromium, firefox, webkit, request } from 'playwright';

const host = process.env.HOST || 'cosmos-test.example.org';
const base = process.env.BASE || `https://${host}:2053`;
const subnet = process.env.SUBNET || '203.0.113';
const server = process.env.SERVER_IP || `${subnet}.5`;
const plain = `http://${server}:2052`;
const dind = process.env.DIND || `http://${server}:2375`;
const mail = process.env.MAIL || `http://${subnet}.2:8025`;
const password = 'Test-Passw0rd!x';
const viewerPassword = 'Viewer-Passw0rd!x';
const phase = process.env.PHASE || 'look';
const engines = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const sizes = (process.env.SIZES || 'd,m').split(',');
const modes = (process.env.MODES || 'light,dark').split(',');
const OWNER_USER = 'Owner.Name';
const OWNER_EMAIL = 'owner@example.org';

const note = (...a) => console.log(...a);
const failures = [];
const counts = {};
const expect = (ok, what) => {
  note(ok ? '  ok  ' : '  FAIL', what);
  const key = what.split(':')[0];
  counts[key] = counts[key] || { ok: 0, fail: 0 };
  counts[key][ok ? 'ok' : 'fail'] += 1;
  if (!ok) failures.push(what);
  return ok;
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const api = await request.newContext({ ignoreHTTPSErrors: true });
const until = async (what, check, seconds = 90) => {
  for (let i = 0; i < seconds; i++) {
    try { if (await check()) return true; } catch (e) { /* reloading */ }
    await sleep(1000);
  }
  expect(false, 'timed out: ' + what);
  return false;
};
const serverUp = () => until('the server answers over HTTPS', async () => (await api.get(base + '/cosmos/api/status', { timeout: 5000 })).status() < 500);
const inspect = async (name) => {
  const answer = await api.get(`${dind}/containers/${name}/json`);
  return answer.status() === 200 ? answer.json() : null;
};
const run = async (name, cmd, env = []) => {
  const made = await (await api.post(`${dind}/containers/${name}/exec`, { data: { Cmd: cmd, Env: env, AttachStdout: true, AttachStderr: true } })).json();
  const out = await api.post(`${dind}/exec/${made.Id}/start`, { data: { Detach: false, Tty: true } });
  return (await out.text()).replace(/[^\x20-\x7e\n]/g, '');
};
const mailState = async (reset) => (await (await fetch(mail + '/state', { method: reset ? 'DELETE' : 'GET' })).json());
const running = async (name) => { const c = await inspect(name); return !!(c && c.State.Running && !c.State.Restarting); };
const healthy = async (name) => { const c = await inspect(name); return !!(c && c.State.Running && (!c.State.Health || c.State.Health.Status === 'healthy')); };

// an API session of the admin (or another user), with the password confirmation
const session = async (nickname = 'admin', pw = password, sudo = true) => {
  const ctx = await request.newContext({ baseURL: base, ignoreHTTPSErrors: true });
  await ctx.post('/cosmos/api/login', { data: { nickname, password: pw } });
  if (sudo) await ctx.post('/cosmos/api/sudo', { data: { password: pw } });
  return ctx;
};

// --- the generated passwords follow the recipe's rule (src/anyimage/recipes.json)
const RULES = {
  postgres: { min: 8, max: 64, symbols: '-._~' },
  pgadmin: { min: 6, max: 64, symbols: '-._~' },
  mariadb: { min: 8, max: 64, symbols: '-._~' },
};
const followsRule = (value, rule, length = 24) => {
  const escaped = rule.symbols.replace(/[-\\^\]]/g, '\\$&');
  const only = new RegExp(`^[A-Za-z0-9${escaped}]+$`).test(value);
  const classes = /[a-z]/.test(value) && /[A-Z]/.test(value) && /[0-9]/.test(value) && (!rule.symbols || [...value].some((c) => rule.symbols.includes(c)));
  return { ok: only && classes && value.length === Math.max(rule.min, Math.min(rule.max, length)), why: `${value.length} characters, only allowed ones ${only}, every class ${classes}` };
};

// --- a small MIME reader: the parts of a message with their decoded text
const decodeQP = (text) => text.replace(/=\r?\n/g, '').replace(/=([0-9A-F]{2})/gi, (m, h) => String.fromCharCode(parseInt(h, 16)));
const mimeParts = (raw) => {
  const [head, ...rest] = raw.split(/\r?\n\r?\n/);
  const body = rest.join('\n\n');
  const type = (head.match(/^Content-Type:\s*([^;\n]+)/im) || [])[1] || 'text/plain';
  const boundary = (head.replace(/\r?\n[ \t]+/g, ' ').match(/boundary="?([^";\n]+)"?/i) || [])[1];
  if (/^multipart\//i.test(type) && boundary) {
    const pieces = body.split('--' + boundary).slice(1).filter((p) => !p.startsWith('--'));
    return { type: type.trim(), parts: pieces.map((p) => mimeParts(p.replace(/^\r?\n/, ''))).flatMap((p) => (p.parts ? p.parts : [p])) };
  }
  const encoding = ((head.match(/^Content-Transfer-Encoding:\s*(\S+)/im) || [])[1] || '').toLowerCase();
  const text = encoding === 'base64' ? Buffer.from(body.replace(/\s+/g, ''), 'base64').toString('utf8') : encoding === 'quoted-printable' ? decodeQP(body) : body;
  return { type: type.trim().toLowerCase(), text };
};

// --- browser helpers
let lastCreate = 0;
const noise = [];
const open = async (type, name, viewport, mode, { user = 'admin', pw = password, sudo = true, clipboard = false } = {}) => {
  const browser = await type.launch();
  const ctx = await browser.newContext({ viewport, ignoreHTTPSErrors: true, colorScheme: mode, serviceWorkers: 'block' });
  if (clipboard && type === chromium) await ctx.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: base });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => {
    if (Date.now() - lastCreate < 90000 && /Failed to fetch|Load failed|NetworkError/.test(e.message)) return; // the server reloads after a creation with an address
    if (!page.url().startsWith(base)) return; // a manager's own page
    // WebKit reports a cross-origin request it refused, and a request cut by a reload, as page errors even when the
    // code catches them: the app chips' reachability probe (hostChip.jsx, __cosmos_probe) and the charts' metrics
    // when a page is left mid-request. Counted and listed, not failed.
    if (type === webkit && /due to access control checks|^TypeError: Load failed/.test(e.message)) { noise.push(`${name}: ${e.message.split('\n')[0].slice(0, 160)}`); return; }
    failures.push(`${name} PAGEERROR ${page.url().replace(base, '')}: ${(e.stack || e.message).slice(0, 400)}`);
  });
  page.consoleLines = [];
  page.on('console', (m) => page.consoleLines.push(m.text()));
  const a = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: user, password: pw } });
  const b = sudo ? (await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: pw } })).status() : '-';
  note(`\n== ${name} (${user}): login ${a.status()}/${b}`);
  return { browser, ctx, page };
};
const shot = (page, name, what) => page.screenshot({ path: `shots/${name}-${what}.png`, fullPage: false });
const shotOf = async (page, name, what, locator) => {
  await locator.scrollIntoViewIfNeeded().catch(() => {});
  await page.waitForTimeout(300);
  await shot(page, name, what);
};
const noSideScroll = async (page, name, where) => {
  const over = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(over <= 1, `${name}: no sideways scroll on ${where} (${over}px over)`);
};
// nothing inside `selector` reaches past its right edge, and icon-only buttons have a name
const fitsAndNamed = async (page, name, selector, where) => {
  const el = page.locator(selector).first();
  if (!(await el.count())) { expect(false, `${name}: ${where} is there (${selector})`); return; }
  const out = await el.evaluate((root) => {
    const right = Math.min(root.getBoundingClientRect().right, document.documentElement.clientWidth) + 1;
    const cut = [...root.querySelectorAll('*')].filter((e) => {
      const r = e.getBoundingClientRect();
      if (!r.width || getComputedStyle(e).position === 'fixed' || e.closest('.MuiCollapse-hidden')) return false;
      return r.right > right || (e.scrollWidth > e.clientWidth + 1 && getComputedStyle(e).overflowX === 'hidden' && !/ellipsis/.test(getComputedStyle(e).textOverflow) && e.children.length === 0);
    }).map((e) => `${e.tagName}.${String(e.className).slice(0, 50)} "${(e.innerText || '').slice(0, 30)}"`);
    const unnamed = [...root.querySelectorAll('button, [role=button], a.MuiIconButton-root')].filter((b) => b.getBoundingClientRect().width > 0
      && !(b.innerText || '').trim() && !b.getAttribute('aria-label') && !b.getAttribute('title') && !b.getAttribute('aria-labelledby'))
      .map((b) => b.outerHTML.slice(0, 80));
    return { cut, unnamed };
  });
  expect(out.cut.length === 0, `${name}: nothing cut off in ${where}${out.cut.length ? ' (' + out.cut.slice(0, 3).join('; ') + ')' : ''}`);
  expect(out.unnamed.length === 0, `${name}: every icon-only button in ${where} has a name${out.unnamed.length ? ' (' + out.unnamed.slice(0, 2).join('; ') + ')' : ''}`);
};
const later = async (page) => {
  const button = page.getByRole('button', { name: 'Later' });
  if (await button.count() && await button.first().isVisible()) await button.first().click();
};
const dialogContent = (page) => page.locator('[data-any-image] .MuiDialogContent-root').last();
const shotDialog = async (page, name, what) => {
  const content = dialogContent(page);
  await content.evaluate((c) => { c.scrollTop = 0; });
  await shot(page, name, what);
  const steps = await content.evaluate((c) => Math.ceil(c.scrollHeight / c.clientHeight));
  for (let i = 1; i < Math.min(steps, 6); i++) {
    await content.evaluate((c, n) => { c.scrollTop = n * c.clientHeight * 0.9; }, i);
    await page.waitForTimeout(200);
    await shot(page, name, `${what}-${i + 1}`);
  }
  await content.evaluate((c) => { c.scrollTop = 0; });
};
// Install any image from the ServApps toolbar, to the review
const toReview = async (page, query, result, containerName) => {
  await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
  await page.locator('[data-any-image-open]').first().waitFor({ timeout: 45000 });
  await later(page);
  await page.locator('[data-any-image-open]').first().click();
  await page.locator('[data-any-image-field] input').fill(query);
  await page.locator('[data-any-image-search]').click();
  const pick = page.locator(`[data-result="${result}"]`);
  await Promise.race([pick.waitFor({ timeout: 30000 }), page.locator('[data-any-image-name]').waitFor({ timeout: 30000 })]).catch(() => {});
  if (await pick.count()) await pick.click();
  await page.locator('[data-any-image-name] input').waitFor({ timeout: 30000 });
  if (containerName) await page.locator('[data-any-image-name] input').fill(containerName);
  await page.locator('[data-any-image-review] [data-creation-review]').waitFor({ timeout: 60000 });
  await page.locator('[data-review-signin]').waitFor({ timeout: 30000 }).catch(() => {});
  await page.waitForTimeout(1500);
};
const value = (page, scope, setting) => page.locator(`${scope} [data-setting="${setting}"] input, ${scope} [data-manager-setting="${setting}"] input`).first().inputValue();
const pageFetch = (page, path, init) => page.evaluate(async ([p, i]) => {
  const r = await fetch(p, { credentials: 'include', ...(i || {}) });
  return { status: r.status, text: await r.text() };
}, [path, init || null]);
const card = (page) => page.locator('[data-signin-card]');
const toCard = async (page, container) => {
  await page.goto(`${base}/cosmos-ui/servapps/containers/${container}#sign-in`, { waitUntil: 'domcontentloaded' });
  await card(page).waitFor({ timeout: 45000 });
  await later(page);
  await Promise.race([
    card(page).locator('[data-signin-account]').first().waitFor({ timeout: 20000 }),
    card(page).locator('[data-signin-confirm]').waitFor({ timeout: 20000 }),
  ]).catch(() => {});
  await page.waitForTimeout(800);
};
const confirmPassword = async (page) => {
  const dialog = page.locator('[data-sudo-prompt]');
  await dialog.waitFor({ timeout: 10000 });
  await dialog.locator('input[name=password]').fill(password);
  await dialog.getByRole('button', { name: 'Submit' }).click();
  await dialog.waitFor({ state: 'detached', timeout: 10000 }).catch(() => {});
};
// the address Cosmos gives a manager, from its sign-in details
const managerUrl = async (admin, container) => {
  const d = (await (await admin.get(`/cosmos/api/servapps/${container}/sign-in`)).json()).data || {};
  return ((d.addresses || [])[0] || {}).url || '';
};
// pgAdmin, signed in: the server Cosmos registered is in the tree; opened, it connects (a password
// prompt is noted, and answered with `dbPassword`) and lists the database. Answers what it saw.
const pgadminSees = async (page, server, database, dbPassword) => {
  const seen = { listed: false, asked: false, askedHost: false, database: false };
  const entry = (re) => page.locator('.file-entry', { hasText: re }).first();
  const has = async (re, ms) => entry(re).waitFor({ timeout: ms }).then(() => true, () => false);
  await entry(/^Servers/).waitFor({ timeout: 90000 }).catch(() => {});
  if (!(await has(new RegExp(`^${server}$`), 4000))) await entry(/^Servers/).dblclick().catch(() => {});
  seen.listed = await has(new RegExp(`^${server}$`), 20000) && /\(1\)/.test(await entry(/^Servers/).innerText().catch(() => ''));
  const asked = page.locator('[role=dialog], .modal-content').filter({ hasText: /password/i });
  if (!(await has(/^Databases/, 6000))) await entry(new RegExp(`^${server}$`)).dblclick().catch(() => {});
  await Promise.race([entry(/^Databases/).waitFor({ timeout: 30000 }), asked.first().waitFor({ timeout: 30000 })]).catch(() => {});
  if (await asked.count()) {
    seen.asked = true;
    const text = await asked.first().innerText();
    seen.askedHost = /host/i.test(text) && (await asked.first().locator('input').count()) > 2;
    await asked.first().locator('input[type=password]').first().fill(dbPassword);
    await asked.first().getByRole('button', { name: /^OK$/i }).click().catch(() => {});
    await entry(/^Databases/).waitFor({ timeout: 30000 }).catch(() => {});
  }
  if (!(await has(/^Databases\s*\(\d+\)/, 3000))) await entry(/^Databases/).dblclick().catch(() => {});
  await has(/^Databases\s*\(\d+\)/, 30000);
  const names = await page.locator('.file-entry').allInnerTexts();
  const at = names.findIndex((n) => /^Databases/.test(n));
  seen.database = at >= 0 && names.slice(at + 1).some((n) => n.trim() === database);
  seen.tree = names.map((n) => n.replace(/\s+/g, ' ')).join(' | ').slice(0, 200);
  return seen;
};
const created = async (page, names, seconds = 240) => {
  lastCreate = Date.now();
  await page.locator('[data-any-image-review] button.shinyButton').click();
  const ok = await until(`${names.join(' and ')} run`, async () => (await Promise.all(names.map(running))).every(Boolean), seconds);
  await page.waitForTimeout(16000); // the start check
  return ok;
};

// ======================================================================= setup
if (phase === 'setup') {
  let fresh = false;
  for (let i = 0; i < 30 && !fresh; i++) {
    try {
      const answer = await api.get(plain + '/cosmos/api/status', { timeout: 5000, maxRedirects: 0 });
      // a new instance answers on HTTP with the placeholder name; a set-up one sends HTTP to HTTPS
      fresh = answer.status() === 200 && ((await answer.json()).data || {}).hostname === '0.0.0.0';
      break;
    } catch (e) { await sleep(2000); }
  }
  if (fresh) {
    const step = (data) => api.post(plain + '/cosmos/api/newInstall', { data, headers: { 'X-Cosmos-Setup-Code': process.env.SETUP_CODE || '' } }).then((r) => r.status()).catch(() => 'exit');
    note('wizard:', await step({ step: '-1' }), await step({ step: '2', mongodbMode: 'Create' }),
      await step({ step: '3', httpsCertificateMode: 'SELFSIGNED', hostname: host, allowHTTPLocalIPAccess: true }),
      await step({ step: '4', nickname: 'admin', password, email: OWNER_EMAIL }), await step({ step: '5' }));
    await sleep(5000);
  } else note('wizard: done already');
  await serverUp();
  const admin = await session();
  const config = (await (await admin.get('/cosmos/api/config')).json()).data;
  config.EmailConfig = {
    ...config.EmailConfig, Enabled: true, Host: 'smtp.fakemail.test', Port: '587', Username: 'cosmos@fakemail.test',
    Password: 'right-password', From: 'cosmos@fakemail.test', UseTLS: true, AllowInsecureTLS: true,
  };
  config.Roles = { ...(config.Roles || {}), 3: { name: 'Operator', permissions: [100, 101, 10, 20, 21, 30] } };
  const put = await admin.put('/cosmos/api/config', { data: config });
  note('settings:', put.status(), (await put.text()).slice(0, 200));
  await sleep(6000);
  await serverUp();
  const again = await session();
  const saved = (await (await again.get('/cosmos/api/config')).json()).data;
  expect(saved.EmailConfig.Enabled && saved.EmailConfig.Host === 'smtp.fakemail.test', 'setup: the e-mail settings point at the stand-in');
  const mine = (await (await again.get('/cosmos/api/app-sign-in/defaults')).json()).data || {};
  expect(mine.email === OWNER_EMAIL && mine.mail && mine.mail.ready, `setup: the admin's address is ${OWNER_EMAIL} and the e-mail can go (${JSON.stringify(mine)})`);
  const users = (await (await again.get('/cosmos/api/users')).json()).data || [];
  if (!users.some((u) => u.nickname === 'viewer')) {
    const made = await (await again.post('/cosmos/api/users', { data: { nickname: 'viewer', email: 'viewer@example.org', role: 3 } })).json();
    const key = made.data && made.data.registerKey;
    const reg = await api.post(base + '/cosmos/api/register', { data: { nickname: 'viewer', registerKey: key, password: viewerPassword } });
    note('viewer:', made.status, reg.status());
  }
  const viewer = await session('viewer', viewerPassword);
  const vme = (await (await viewer.get('/cosmos/api/me')).json()).data || {};
  expect(vme.role === 3 || vme.userRole === 3 || vme.Role === 3 || JSON.stringify(vme).includes('"role":3'), `setup: the viewer signs in with the role without credentials-read (${JSON.stringify(vme).slice(0, 160)})`);
  expect(await running('olddb'), 'setup: olddb runs in the test Docker');
  await mailState(true);
}

// ======================================================================= story
const secrets = {};
const from = Number(process.env.FROM || 1);
const to = Number(process.env.TO || 7);
if (phase === 'story' || phase === 'all') {
  const name = 'story-chromium-1366-light';
  const { browser, ctx, page } = await open(chromium, name, { width: 1366, height: 900 }, 'light', { clipboard: true });
  let admin = await session();
  const dlg = '[data-any-image]';
  let pgaUrl = '';
  let secretId = 'POSTGRES_PASSWORD';
  // a later start (FROM=n) reads what the earlier steps made from the containers themselves
  const envOf = async (container) => Object.fromEntries((((await inspect(container)) || { Config: {} }).Config.Env || []).map((l) => [l.split('=')[0], l.slice(l.indexOf('=') + 1)]));
  if (from > 2) {
    const pg = await envOf('postgres'); const pa = await envOf('postgres-pgadmin');
    Object.assign(secrets, { pg: pg.POSTGRES_PASSWORD, pgDb: pg.POSTGRES_DB, pga: pa.PGADMIN_DEFAULT_PASSWORD, pgaEmail: pa.PGADMIN_DEFAULT_EMAIL });
    pgaUrl = await managerUrl(admin, 'postgres-pgadmin');
  }
  try {
    if (from <= 1 && 1 <= to) {
    // --- 1. Configuration > General > "Sign-in details for new apps"
    await page.goto(base + '/cosmos-ui/config-general', { waitUntil: 'domcontentloaded' });
    const cfg = page.locator('[data-config-app-signin]');
    await cfg.waitFor({ timeout: 45000 });
    await later(page);
    expect(/Sign-in details for new apps/.test(await page.locator('.MuiPaper-root', { has: cfg }).last().innerText()), '1 config: the card is there');
    await page.locator('input[name=AppSignInUser]').fill(OWNER_USER);
    await page.locator('input[name=AppSignInEmail]').fill(OWNER_EMAIL);
    const mailBox = page.locator('input[name=AppSignInMail]');
    expect(await mailBox.isChecked(), '1 config: "Send me an e-mail" is on by default');
    await shotOf(page, name, '01-config-card', cfg);
    const [answer] = await Promise.all([
      page.waitForResponse((r) => r.url().endsWith('/cosmos/api/config') && r.request().method() !== 'GET', { timeout: 20000 }).catch(() => null),
      page.getByRole('button', { name: /^Save$/ }).first().click(),
    ]);
    expect(!!answer && answer.status() === 200, `1 config: saved (${answer && answer.status()})`);
    await sleep(5000);
    await serverUp();
    await until('the page loads again after the save', async () => { await page.goto(base + '/cosmos-ui/config-general', { waitUntil: 'domcontentloaded' }); return true; }, 60);
    await cfg.waitFor({ timeout: 45000 });
    await page.waitForTimeout(1500);
    const kept = [await page.locator('input[name=AppSignInUser]').inputValue(), await page.locator('input[name=AppSignInEmail]').inputValue(), await mailBox.isChecked()];
    expect(kept[0] === OWNER_USER && kept[1] === OWNER_EMAIL && kept[2], `1 config: kept after a reload (${kept.join(', ')})`);
    admin = await session();
    const stored = ((await (await admin.get('/cosmos/api/config')).json()).data || {}).AppSignIn || {};
    expect(stored.User === OWNER_USER && stored.Email === OWNER_EMAIL && !stored.MailOff, `1 config: stored (${JSON.stringify(stored)})`);

    }
    if (from <= 2 && 2 <= to) {
    // --- 2. "postgres" with its web manager
    await toReview(page, 'postgres', 'postgres', 'postgres');
    await page.locator('[data-manager-pick]').first().waitFor({ timeout: 30000 });
    await until('the manager image is read', async () => !(await page.locator('[data-any-image-review] button.shinyButton').isDisabled()), 60);
    const pgUser = await value(page, dlg, 'POSTGRES_USER');
    const userField = await page.locator('[data-setting="POSTGRES_USER"]').innerText();
    expect(pgUser === 'owner_name', `2 review: POSTGRES_USER = owner_name (${pgUser})`);
    expect(/Owner\.Name in the form this app takes/.test(userField), `2 review: the variant sentence by the field (${userField.replace(/\s+/g, ' ').slice(0, 140)})`);
    secrets.pg = await value(page, dlg, 'POSTGRES_PASSWORD');
    const rule = followsRule(secrets.pg, RULES.postgres);
    expect(rule.ok, `2 review: the database password follows the recipe's rule (${rule.why})`);
    secrets.pgDb = await value(page, dlg, 'POSTGRES_DB');
    expect(await page.locator('[data-manager-switch] input').isChecked(), '2 review: "Also install a web manager for it" is on');
    const picks = await page.locator('[data-manager-pick]').evaluateAll((els) => els.map((e) => `${e.getAttribute('data-manager-pick')}:${e.innerText}`));
    expect(picks[0] === 'pgadmin:pgAdmin 4 (proposed)' && picks.some((p) => p.startsWith('adminer:')), `2 review: pgAdmin proposed, Adminer the other choice (${picks.join(' | ')})`);
    secrets.pgaEmail = await value(page, dlg, 'PGADMIN_DEFAULT_EMAIL');
    secrets.pga = await value(page, dlg, 'PGADMIN_DEFAULT_PASSWORD');
    expect(secrets.pgaEmail === OWNER_EMAIL, `2 review: pgAdmin's e-mail is ${OWNER_EMAIL} (${secrets.pgaEmail})`);
    const ruleA = followsRule(secrets.pga, RULES.pgadmin);
    expect(ruleA.ok && secrets.pga !== secrets.pg, `2 review: pgAdmin's password is generated by its rule, another one (${ruleA.why})`);
    const who = (await page.locator('[data-review-signin]').innerText()).replace(/\s+/g, ' ');
    expect(await page.locator('[data-signin-line="postgres"]').count() === 1 && await page.locator('[data-signin-line="postgres-pgadmin"]').count() === 1,
      `2 review: "Who signs in with what" lists both (${who.slice(0, 300)})`);
    expect(/owner_name/.test(who) && who.includes(OWNER_EMAIL), '2 review: with the username and the e-mail');
    if (!/Your username Owner\.Name is owner_name here/.test(who)) note('  (note: the variant is said by the field, not repeated in "Who signs in with what")');
    expect(!who.includes(secrets.pg) && !who.includes(secrets.pga), '2 review: no password written out in the sentences');
    expect(/An e-mail with the app's name, address and username/.test(who), '2 review: it says the e-mail will be sent');
    await shotDialog(page, name, '02-postgres-review');
    await shotOf(page, name, '03-review-manager', page.locator('[data-any-image-manager]'));
    await shotOf(page, name, '04-review-signin', page.locator('[data-review-signin]'));
    if (process.env.NO_CREATE === '1') throw new Error('NO_CREATE');
    await mailState(true);
    const ok = await created(page, ['postgres', 'postgres-pgadmin']);
    expect(ok, '2 create: PostgreSQL and pgAdmin run');
    expect(await page.locator('[data-start-problem]').count() === 0, '2 create: the start check finds nothing wrong');
    await shot(page, name, '05-created');
    expect(!page.consoleLines.some((l) => l.includes(secrets.pg) || l.includes(secrets.pga)), '2 create: no password on the console');
    if (ok) {
      await until('postgres is healthy', () => healthy('postgres'), 90);
      const psql = ['sh', '-c', `psql -h "$(hostname -i | cut -d' ' -f1)" -U owner_name -d "${secrets.pgDb}" -tAc "select 40+2" 2>&1; echo "exit=$?"`];
      const good = await run('postgres', psql, [`PGPASSWORD=${secrets.pg}`]);
      expect(/42/.test(good), `2 create: owner_name signs in to PostgreSQL with the generated password (${good.trim().slice(0, 80)})`);
      const pga = await inspect('postgres-pgadmin');
      expect(Object.keys(pga.HostConfig.PortBindings || {}).length === 0, '2 create: the manager publishes no port');
      const dbNets = Object.keys((await inspect('postgres')).NetworkSettings.Networks);
      const shared = Object.keys(pga.NetworkSettings.Networks).filter((n) => dbNets.includes(n));
      expect(shared.length >= 1, `2 create: the manager shares a network with its database (${shared.join()})`);
      const labels = JSON.stringify(pga.Config.Labels || {});
      expect(!labels.includes(secrets.pg) && !labels.includes(secrets.pga) && !(pga.Config.Cmd || []).join(' ').includes(secrets.pg), '2 create: no password in the manager\'s labels or command line');
    }
    await page.keyboard.press('Escape').catch(() => {});

    }
    if (from <= 3 && 3 <= to) {
    // --- 3. the e-mail
    await until('the e-mail arrives', async () => ((await mailState()).messages || []).length >= 1, 60);
    await sleep(10000);
    const messages = (await mailState()).messages || [];
    expect(messages.length === 1, `3 mail: exactly one message for the creation (${messages.length})`);
    const m = messages[0] || { to: [], data: '' };
    expect(m.to.length === 1 && m.to[0] === OWNER_EMAIL, `3 mail: to ${OWNER_EMAIL} only (${m.to.join()})`);
    const mime = mimeParts(m.data || '');
    const types = (mime.parts || [mime]).map((p) => p.type);
    expect(/multipart\/alternative/i.test(mime.type) && types.includes('text/plain') && types.includes('text/html'), `3 mail: multipart, plain and HTML (${mime.type}: ${types.join(', ')})`);
    const plainText = ((mime.parts || []).find((p) => p.type === 'text/plain') || {}).text || '';
    const html = ((mime.parts || []).find((p) => p.type === 'text/html') || {}).text || '';
    pgaUrl = await managerUrl(admin, 'postgres-pgadmin');
    for (const [label, text] of [['plain', plainText], ['HTML', html]]) {
      expect(text.includes('postgres-pgadmin') && /\bpostgres\b/.test(text.replace(/postgres-pgadmin/g, '')), `3 mail ${label}: names both containers`);
      expect(text.includes('owner_name'), `3 mail ${label}: names owner_name`);
      expect(!!pgaUrl && text.includes(pgaUrl.replace(/\/$/, '')), `3 mail ${label}: the manager's address (${pgaUrl})`);
      expect(text.includes('/cosmos-ui/servapps/containers/postgres#sign-in'), `3 mail ${label}: the link to the database's sign-in details`);
    }
    const everything = m.data + plainText + html;
    expect(!everything.includes(secrets.pg) && !everything.includes(secrets.pga), '3 mail: neither generated password is in it');
    expect(!/postgres(ql)?:\/\/[^\s<>"]*:[^\s<>"@]+@/.test(everything.replace(/:<password>@|:&lt;password&gt;@/g, '@')), '3 mail: no connection string with a password');
    note('  subject:', m.subject);
    note('  plain part:\n' + plainText.split('\n').map((l) => '    | ' + l).slice(0, 40).join('\n'));
    await page.setContent(`<html><body style="margin:0">${html.replace(/<script/gi, '<x-script')}</body></html>`).catch(() => {});
    await page.waitForTimeout(500);
    await shot(page, name, '06-mail-html');

    }
    if (from <= 4 && 4 <= to) {
    // --- 4. the card on the database's page
    await toCard(page, 'postgres');
    const c = card(page);
    const cardText = (await c.innerText()).replace(/\s+/g, ' ');
    expect(/owner_name/.test(cardText) && cardText.includes(secrets.pgDb) && /postgres:5432/.test(cardText), `4 card: the user, the database and host:port (${cardText.slice(0, 220)})`);
    secretId = await c.locator('[data-signin-show]').first().getAttribute('data-signin-show');
    const hidden = await c.locator(`[data-signin-secret="${secretId}"]`).innerText();
    expect(/^•+$/.test(hidden.trim()) && !cardText.includes(secrets.pg), `4 card: the password is hidden (${hidden})`);
    const conn = c.locator('[data-signin-secret]').filter({ hasText: '<password>' }).first();
    const connId = await conn.getAttribute('data-signin-secret').catch(() => null);
    expect(!!connId && /postgres(ql)?:\/\/owner_name:<password>@postgres:5432\//.test(await conn.innerText()), `4 card: the connection string shows <password> (${connId && await conn.innerText()})`);
    await shotOf(page, name, '07-card', c);
    await c.locator(`[data-signin-show="${secretId}"]`).click();
    await until('the password is shown', async () => (await c.locator(`[data-signin-secret="${secretId}"]`).innerText()).trim() === secrets.pg, 15);
    expect((await c.locator(`[data-signin-secret="${secretId}"]`).innerText()).trim() === secrets.pg, '4 card: Show reveals exactly the generated password');
    await shotOf(page, name, '08-card-shown', c);
    await c.locator(`[data-signin-copy="${secretId}"]`).click();
    await until('Copied', async () => /Copied/.test(await c.locator(`[data-signin-copy="${secretId}"]`).innerText()), 10);
    const clip = await page.evaluate(() => navigator.clipboard.readText()).catch((e) => 'unreadable: ' + e.message);
    expect(clip === secrets.pg, `4 card: Copy puts the password on the clipboard (${clip === secrets.pg ? 'same' : clip.slice(0, 40)})`);
    if (connId) {
      expect((await conn.innerText()).includes('<password>'), '4 card: the connection string keeps <password> until its own Show');
      await c.locator(`[data-signin-show="${connId}"]`).click();
      await until('the connection string is shown', async () => (await c.locator(`[data-signin-secret="${connId}"]`).innerText()).includes(secrets.pg), 15);
      expect((await c.locator(`[data-signin-secret="${connId}"]`).innerText()).includes(`owner_name:${secrets.pg}@postgres:5432`), '4 card: its Show gives the connection string with the password');
    }
    // the password confirmation withdrawn: a new session without it replaces the cookie
    await ctx.clearCookies(); await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
    await toCard(page, 'postgres');
    expect(await c.locator('[data-signin-confirm]').count() === 1 && await c.locator('[data-signin-account]').count() === 0, '4 card: without the confirmation it offers "Confirm my password"');
    const without = await pageFetch(page, '/cosmos/api/servapps/postgres/sign-in');
    expect(without.status === 401 || without.status === 403, `4 card: the server refuses the details without the confirmation (${without.status})`);
    await shotOf(page, name, '09-card-confirm', c);
    await c.locator('[data-signin-confirm]').click();
    await confirmPassword(page);
    await c.locator('[data-signin-account]').first().waitFor({ timeout: 20000 }).catch(() => {});
    expect(await c.locator('[data-signin-account]').count() >= 1, '4 card: after the prompt the card comes back');
    const events = await pageFetch(page, '/cosmos/api/security/events?kind=credentials.revealed&limit=50');
    const list = (JSON.parse(events.text).data || {}).events || JSON.parse(events.text).data || [];
    const entries = Array.isArray(list) ? list : (list.items || []);
    const purposes = JSON.stringify(entries);
    expect(events.status === 200 && entries.length >= 3 && /show/.test(purposes) && /copy/.test(purposes), `4 security: credentials.revealed recorded for show and copy (${entries.length}: ${purposes.slice(0, 200)})`);
    expect(!purposes.includes(secrets.pg), '4 security: without the value');

    }
    if (from <= 5 && 5 <= to) {
    // --- 5. pgAdmin through Cosmos
    expect(/^https:\/\/[^/]+:2053\/?$/.test(pgaUrl), `5 pgAdmin: its address in Cosmos is on the HTTPS port 2053 (${pgaUrl})`);
    const det = (await (await admin.get('/cosmos/api/servapps/postgres-pgadmin/sign-in')).json()).data || {};
    expect((det.addresses || []).every((a) => a.adminOnly), '5 pgAdmin: its address is for administrators only');
    let tries = 0;
    await until('pgAdmin\'s sign-in page', async () => {
      tries += 1;
      const res = await page.goto(pgaUrl, { waitUntil: 'domcontentloaded', timeout: 30000 });
      if (tries === 1) note(`  first visit: ${res && res.status()} at ${page.url()}`);
      await page.locator('input[name=email], input#email').first().waitFor({ timeout: 20000 });
      return true;
    }, 240);
    note(`  pgAdmin's sign-in page after ${tries} visit(s)`);
    const at = new URL(page.url());
    expect(at.protocol === 'https:' && at.port === '2053' && at.hostname === new URL(pgaUrl).hostname, `5 pgAdmin: loads at its address, no redirect to another port or scheme (${page.url()})`);
    await shot(page, name, '10-pgadmin-login');
    await page.locator('input[name=email], input#email').first().fill(secrets.pgaEmail);
    await page.locator('input[name=password], input#password').first().fill(secrets.pga);
    await page.getByRole('button', { name: /^Login$/i }).click();
    const tree = page.locator('#tree, [role=tree], .ReactVirtualized__Grid, .file-tree').first();
    await page.getByText('Servers', { exact: false }).first().waitFor({ timeout: 90000 }).catch(() => {});
    await page.waitForTimeout(4000);
    const signedInAt = new URL(page.url());
    expect(signedInAt.port === '2053' && /browser/.test(signedInAt.pathname), `5 pgAdmin: signed in with the review's e-mail and password (${page.url()})`);
    const seen = await pgadminSees(page, 'postgres', secrets.pgDb, secrets.pg);
    expect(seen.listed, `5 pgAdmin: lists the PostgreSQL server, pre-registered (nothing typed) (${seen.tree})`);
    expect(!seen.asked, `5 pgAdmin: connecting asks for no password (it has the database's)${seen.asked ? ' (it asked' + (seen.askedHost ? ', with a host field' : ' only for the password') + ')' : ''}`);
    expect(seen.database, `5 pgAdmin: connected, it lists the database ${secrets.pgDb}`);
    await shot(page, name, '12-pgadmin-server');

    // a user without credentials-read
    const v = await open(chromium, name + '-viewer', { width: 1366, height: 900 }, 'light', { user: 'viewer', pw: viewerPassword });
    await v.page.goto(`${base}/cosmos-ui/servapps/containers/postgres`, { waitUntil: 'domcontentloaded' });
    await v.page.locator('.MuiTab-root, [role=tab]').first().waitFor({ timeout: 45000 }).catch(() => {});
    await v.page.waitForTimeout(3000);
    expect(await card(v.page).count() === 0, '5 viewer: no sign-in card on the database\'s page');
    await shot(v.page, name, '13-viewer-database');
    const vr = await pageFetch(v.page, '/cosmos/api/servapps/postgres/sign-in');
    expect(vr.status === 401 || vr.status === 403, `5 viewer: the API refuses GET …/sign-in (${vr.status})`);
    const vrr = await pageFetch(v.page, '/cosmos/api/servapps/postgres/sign-in/reveal', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ secret: secretId, purpose: 'show' }) });
    expect((vrr.status === 401 || vrr.status === 403) && !vrr.text.includes(secrets.pg), `5 viewer: and the reveal (${vrr.status})`);
    await v.page.goto(pgaUrl, { waitUntil: 'domcontentloaded', timeout: 60000 }).catch(() => {});
    await v.page.waitForTimeout(3000);
    const vText = await v.page.locator('body').innerText().catch(() => '');
    expect(!/pgAdmin/i.test(await v.page.title()) && !(await v.page.locator('input[name=email]').count()), `5 viewer: the manager's address is refused (${v.page.url()} "${vText.replace(/\s+/g, ' ').slice(0, 100)}")`);
    await shot(v.page, name, '14-viewer-manager');
    await v.browser.close();

    }
    if (from <= 6 && 6 <= to) {
    // --- 6. MariaDB with phpMyAdmin
    await mailState(true);
    await sleep(30000); // descriptions are limited per minute
    await toReview(page, 'mariadb', 'mariadb', 'mariadb');
    await page.locator('[data-manager-pick]').first().waitFor({ timeout: 30000 });
    await until('the manager image is read', async () => !(await page.locator('[data-any-image-review] button.shinyButton').isDisabled()), 60);
    const mPicks = await page.locator('[data-manager-pick]').evaluateAll((els) => els.map((e) => `${e.getAttribute('data-manager-pick')}:${e.innerText}`));
    expect(mPicks[0] === 'phpmyadmin:phpMyAdmin (proposed)' && mPicks.some((p) => p.startsWith('adminer:')), `6 review: phpMyAdmin proposed (${mPicks.join(' | ')})`);
    secrets.mUser = await value(page, dlg, 'MARIADB_USER');
    secrets.m = await value(page, dlg, 'MARIADB_PASSWORD');
    secrets.mRoot = await value(page, dlg, 'MARIADB_ROOT_PASSWORD');
    secrets.mDb = await value(page, dlg, 'MARIADB_DATABASE');
    expect(secrets.mUser === 'owner_name', `6 review: MARIADB_USER = owner_name (${secrets.mUser})`);
    const ruleM = followsRule(secrets.m, RULES.mariadb);
    expect(ruleM.ok && followsRule(secrets.mRoot, RULES.mariadb).ok, `6 review: both MariaDB passwords follow its rule (${ruleM.why})`);
    if (!(await page.locator('[data-signin-line="mariadb-phpmyadmin"]').count())) note('  (note: phpMyAdmin, signed in with the database\'s account, has no line in "Who signs in with what")');
    await shotDialog(page, name, '15-mariadb-review');
    const okM = await created(page, ['mariadb', 'mariadb-phpmyadmin']);
    expect(okM, '6 create: MariaDB and phpMyAdmin run');
    await until('mariadb is healthy', () => healthy('mariadb'), 120);
    await until('the e-mail arrives', async () => ((await mailState()).messages || []).length >= 1, 60);
    await sleep(8000);
    const mm = (await mailState()).messages || [];
    expect(mm.length === 1 && !(mm[0].data || '').includes(secrets.m) && !(mm[0].data || '').includes(secrets.mRoot), `6 mail: one message, without the passwords (${mm.length})`);
    await toCard(page, 'mariadb');
    const mc = card(page);
    const mId = await mc.locator('[data-signin-account="0"] [data-signin-show]').first().getAttribute('data-signin-show');
    await mc.locator(`[data-signin-show="${mId}"]`).click();
    await until('the MariaDB password is shown', async () => (await mc.locator(`[data-signin-secret="${mId}"]`).innerText()).trim() === secrets.m, 15);
    expect((await mc.locator(`[data-signin-secret="${mId}"]`).innerText()).trim() === secrets.m, '6 card: Show gives the MariaDB user\'s password');
    await shotOf(page, name, '16-mariadb-card', mc);
    const pmaUrl = await managerUrl(admin, 'mariadb-phpmyadmin');
    await page.goto(pmaUrl, { waitUntil: 'domcontentloaded', timeout: 60000 });
    await page.locator('#input_username').waitFor({ timeout: 60000 }).catch(() => {});
    const pAt = new URL(page.url());
    expect(pAt.protocol === 'https:' && pAt.port === '2053', `6 phpMyAdmin: loads at its address (${page.url()})`);
    expect(await page.locator('#input_servername, select[name=server]').count() === 0, '6 phpMyAdmin: no server to type');
    await shot(page, name, '17-phpmyadmin-login');
    await page.locator('#input_username').fill(secrets.mUser);
    await page.locator('#input_password').fill(secrets.m);
    await page.locator('#input_go').click();
    await page.locator('#pma_navigation').waitFor({ timeout: 60000 }).catch(() => {});
    await page.waitForTimeout(3000);
    const nav = await page.locator('#pma_navigation').innerText().catch(() => '');
    expect(nav.includes(secrets.mDb), `6 phpMyAdmin: signed in with the card's user and password, the database ${secrets.mDb} is listed`);
    expect(new URL(page.url()).port === '2053', `6 phpMyAdmin: stays on 2053 after signing in (${page.url()})`);
    await shot(page, name, '18-phpmyadmin-signed-in');

    }
    if (from <= 7 && 7 <= to) {
    // --- 7. "Add a web manager" on olddb
    await sleep(20000);
    await toCard(page, 'olddb');
    const oc = card(page);
    await oc.locator('[data-add-manager-open]').waitFor({ timeout: 20000 });
    await shotOf(page, name, '19-olddb-card', oc);
    await oc.locator('[data-add-manager-open]').click();
    const add = page.locator('[data-add-manager]');
    await add.locator('[data-manager-pick]').first().waitFor({ timeout: 30000 });
    await add.locator('[data-creation-review]').waitFor({ timeout: 60000 });
    await page.waitForTimeout(2000);
    const aPicks = await add.locator('[data-manager-pick]').evaluateAll((els) => els.map((e) => `${e.getAttribute('data-manager-pick')}:${e.innerText}`));
    expect(aPicks[0] === 'pgadmin:pgAdmin 4 (proposed)', `7 add: the dialog proposes pgAdmin for olddb (${aPicks.join(' | ')})`);
    secrets.oEmail = await add.locator('[data-manager-setting="PGADMIN_DEFAULT_EMAIL"] input').inputValue();
    secrets.o = await add.locator('[data-manager-setting="PGADMIN_DEFAULT_PASSWORD"] input').inputValue();
    expect(secrets.oEmail === OWNER_EMAIL && followsRule(secrets.o, RULES.pgadmin).ok, `7 add: its sign-in from the defaults (${secrets.oEmail})`);
    await shot(page, name, '20-add-manager');
    await add.locator('.MuiDialogContent-root').evaluate((e) => { e.scrollTop = e.scrollHeight; });
    await shot(page, name, '21-add-manager-review');
    lastCreate = Date.now();
    const again = !!(await inspect('olddb-pgadmin'));
    if (again) {
      note('  (olddb-pgadmin exists from an earlier run: not created again)');
      const env = Object.fromEntries((await inspect('olddb-pgadmin')).Config.Env.map((l) => [l.split('=')[0], l.slice(l.indexOf('=') + 1)]));
      secrets.oEmail = env.PGADMIN_DEFAULT_EMAIL; secrets.o = env.PGADMIN_DEFAULT_PASSWORD;
      await add.getByRole('button', { name: /^Close$/ }).click().catch(() => {});
    } else await add.locator('button.shinyButton').last().click();
    const okO = await until('olddb-pgadmin runs', () => running('olddb-pgadmin'), 180);
    expect(okO, '7 add: the manager is created and runs');
    await page.waitForTimeout(16000);
    await shot(page, name, '22-add-manager-created');
    const oUrl = await managerUrl(admin, 'olddb-pgadmin');
    const loginField = page.locator('input[name=email], input#email').first();
    await until('olddb-pgadmin\'s sign-in page', async () => {
      await page.goto(oUrl, { waitUntil: 'domcontentloaded', timeout: 30000 });
      await loginField.waitFor({ timeout: 20000 });
      return true;
    }, 180);
    await loginField.fill(secrets.oEmail);
    await page.locator('input[name=password], input#password').first().fill(secrets.o);
    await page.getByRole('button', { name: /^Login$/i }).click();
    const oSeen = await pgadminSees(page, 'olddb', 'olddb', 'Old-test-pw-0420');
    expect(oSeen.listed && !oSeen.asked && oSeen.database, `7 add: the manager has olddb registered, connects without a password and lists its database (${JSON.stringify(oSeen)})`);
    await shot(page, name, '23-olddb-pgadmin');
    }
  } catch (e) {
    if (e.message === 'NO_CREATE') note('  (NO_CREATE=1: stopped before Create)');
    else {
      failures.push(`${name}: ${e.message.split('\n')[0]}`);
      await shot(page, name, 'zz-error').catch(() => {});
    }
  }
  await browser.close();
}

// ======================================================================= look
if (phase === 'look' || phase === 'all') {
  let first = true;
  for (const engine of engines) {
    for (const size of sizes) {
      for (const mode of modes) {
        const viewport = size === 'm' ? { width: 393, height: 851 } : { width: 1366, height: 900 };
        const name = `${engine}-${size === 'm' ? 393 : 1366}-${mode}`;
        if (!first) await sleep(40000); // eight image descriptions a minute per user
        first = false;
        const { browser, ctx, page } = await open({ chromium, firefox, webkit }[engine], name, viewport, mode, { clipboard: true });
        try {
          // the configuration card
          await page.goto(base + '/cosmos-ui/config-general', { waitUntil: 'domcontentloaded' });
          const cfg = page.locator('[data-config-app-signin]');
          await cfg.waitFor({ timeout: 45000 });
          await later(page);
          await page.waitForTimeout(800);
          const cfgCard = page.locator('.MuiPaper-root', { has: cfg }).last();
          await shotOf(page, name, '01-config-card', cfgCard);
          await noSideScroll(page, name, 'the configuration');
          await fitsAndNamed(page, name, '[data-config-app-signin]', 'the config card');
          // the review's sign-in block and the manager choice (nothing is created)
          await toReview(page, 'postgres', 'postgres', 'pgview');
          await page.locator('[data-manager-pick]').first().waitFor({ timeout: 30000 }).catch(() => {});
          await page.waitForTimeout(2500);
          await shotOf(page, name, '02-review-manager', page.locator('[data-any-image-manager]'));
          await fitsAndNamed(page, name, '[data-any-image-manager]', 'the manager choice');
          await shotOf(page, name, '03-review-signin', page.locator('[data-review-signin]'));
          await fitsAndNamed(page, name, '[data-review-signin]', 'the review\'s sign-in block');
          await page.locator('[data-manager-pick="adminer"]').click();
          await page.waitForTimeout(2500);
          await shotOf(page, name, '04-review-adminer', page.locator('[data-any-image-manager]'));
          await fitsAndNamed(page, name, '[data-any-image] .MuiDialogContent-root', 'the review');
          await noSideScroll(page, name, 'the review');
          await page.locator('[data-any-image] button', { hasText: 'Close' }).last().click().catch(() => {});
          // the card, hidden and shown
          await toCard(page, 'postgres');
          const c = card(page);
          await shotOf(page, name, '05-card', c);
          await fitsAndNamed(page, name, '[data-signin-card]', 'the card');
          await noSideScroll(page, name, 'the database\'s page');
          const id = await c.locator('[data-signin-show]').first().getAttribute('data-signin-show');
          await c.locator(`[data-signin-show="${id}"]`).click();
          await page.waitForTimeout(1500);
          const shown = (await c.locator(`[data-signin-secret="${id}"]`).innerText()).trim();
          expect(/^[A-Za-z0-9._~-]{24}$/.test(shown), `${name}: Show reveals the password`);
          await c.locator(`[data-signin-copy="${id}"]`).click();
          await page.waitForTimeout(1500);
          expect(/Copied/.test(await c.locator(`[data-signin-copy="${id}"]`).innerText()), `${name}: Copy says Copied`);
          if (engine === 'chromium') {
            const clip = await page.evaluate(() => navigator.clipboard.readText()).catch(() => '');
            expect(clip === shown, `${name}: the clipboard holds the password`);
          }
          await shotOf(page, name, '06-card-shown', c);
          await fitsAndNamed(page, name, '[data-signin-card]', 'the card with the password shown');
          await noSideScroll(page, name, 'the card with the password shown');
          // without the confirmation
          await ctx.clearCookies(); await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
          await toCard(page, 'postgres');
          expect(await c.locator('[data-signin-confirm]').count() === 1, `${name}: without the confirmation the card offers it`);
          await shotOf(page, name, '07-card-confirm', c);
          await c.locator('[data-signin-confirm]').click();
          await page.locator('[data-sudo-prompt]').waitFor({ timeout: 10000 });
          await shot(page, name, '08-confirm-prompt');
          await confirmPassword(page);
          await c.locator('[data-signin-account]').first().waitFor({ timeout: 20000 }).catch(() => {});
          expect(await c.locator('[data-signin-account]').count() >= 1, `${name}: after the prompt the card comes back`);
          // a manager's own card, and the add-manager dialog on olddb
          await toCard(page, 'postgres-pgadmin');
          await shotOf(page, name, '09-manager-card', card(page));
          await fitsAndNamed(page, name, '[data-signin-card]', 'the manager\'s card');
          await toCard(page, 'olddb');
          await card(page).locator('[data-add-manager-open]').click();
          const add = page.locator('[data-add-manager]');
          await add.locator('[data-creation-review]').waitFor({ timeout: 60000 }).catch(() => {});
          await page.waitForTimeout(2000);
          await shot(page, name, '10-add-manager');
          await fitsAndNamed(page, name, '[data-add-manager] .MuiDialogContent-root', 'the add-manager dialog');
          const box = await add.locator('.MuiDialog-paper').boundingBox();
          expect(!!box && box.x >= -1 && box.x + box.width <= viewport.width + 1, `${name}: the add-manager dialog fits the screen`);
          await add.locator('.MuiDialogContent-root').evaluate((e) => { e.scrollTop = e.scrollHeight; });
          await page.waitForTimeout(300);
          await shot(page, name, '11-add-manager-end');
          await add.getByRole('button', { name: /^Close$/ }).click().catch(() => {});
          // the ServApps tile's key
          await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
          await page.getByPlaceholder('Search...').waitFor({ timeout: 45000 });
          // (postgres and its manager are one stack, whose tile has no key: olddb is a container of its own)
          await page.getByPlaceholder('Search...').fill('olddb');
          await page.waitForTimeout(1500);
          await later(page);
          const key = page.locator('[data-signin-tile="olddb"]');
          expect(await key.count() === 1 && !!(await key.getAttribute('aria-label')), `${name}: the tile's key to the card is named`);
          await key.hover().catch(() => {});
          await page.waitForTimeout(800);
          expect(await page.locator('[role=tooltip]').filter({ hasText: 'Sign-in details of olddb' }).count() === 1, `${name}: and has its tooltip`);
          await shot(page, name, '12-tile-key');
          await noSideScroll(page, name, 'ServApps');
        } catch (e) {
          failures.push(`${name}: ${e.message.split('\n')[0]}`);
          await shot(page, name, 'zz-error').catch(() => {});
        }
        await browser.close();
      }
    }
  }
}

// ======================================================================= delete
// ======================================================================= more managers, and the URL check
if (phase === 'more') {
  const admin = await session();
  const name = 'more-chromium-1366-light';
  const { browser, page } = await open(chromium, name, { width: 1366, height: 900 }, 'light');
  try {
    // --- Adminer added to the database made before, then signed in to it through Cosmos
    if (!(await inspect('olddb-adminer'))) {
      await toCard(page, 'olddb');
      await card(page).locator('[data-add-manager-open]').click();
      const add = page.locator('[data-add-manager]');
      await add.locator('[data-manager-pick="adminer"]').waitFor({ timeout: 30000 });
      await add.locator('[data-manager-pick="adminer"]').click();
      await add.locator('[data-creation-review]').waitFor({ timeout: 60000 });
      await page.waitForTimeout(3000);
      lastCreate = Date.now();
      await add.locator('button.shinyButton').last().click();
    }
    expect(await until('olddb-adminer runs', () => running('olddb-adminer'), 180), 'more: Adminer added to olddb runs');
    const nets = Object.keys((await inspect('olddb-adminer')).NetworkSettings.Networks);
    expect(!nets.includes('bridge'), `more: Adminer is not on the default bridge (${nets.join(', ')})`);
    await page.waitForTimeout(16000);
    const aUrl = await managerUrl(admin, 'olddb-adminer');
    expect(/^https:\/\/[^/]+:2053$/.test(aUrl), `more: Adminer's address is on 2053 (${aUrl})`);
    await until('Adminer\'s sign-in page', async () => {
      await page.goto(aUrl, { waitUntil: 'domcontentloaded', timeout: 30000 });
      await page.locator('select[name="auth[driver]"]').waitFor({ timeout: 20000 });
      return true;
    }, 120);
    expect(await page.locator('input[name="auth[server]"]').inputValue() === 'olddb', 'more: Adminer opens on olddb (no server to type)');
    await page.locator('select[name="auth[driver]"]').selectOption('pgsql');
    await page.locator('input[name="auth[username]"]').fill('olduser');
    await page.locator('input[name="auth[password]"]').fill('Old-test-pw-0420');
    await page.locator('input[name="auth[db]"]').fill('olddb');
    await page.locator('input[type=submit]').first().click();
    await page.waitForLoadState('domcontentloaded');
    await page.waitForTimeout(2000);
    const after = await page.locator('body').innerText();
    expect(/olddb/.test(after) && !/Invalid|denied|failed/i.test(after.slice(0, 400)) && page.url().startsWith(aUrl), `more: Adminer signed in to olddb and stays on its address (${page.url()})`);
    await shot(page, name, '30-adminer-signed-in');

    // --- Redis with Redis Insight from Install any image
    if (!(await inspect('redis-redisinsight'))) {
      await toReview(page, 'redis', 'redis', 'redis');
      const picks = await page.locator('[data-manager-pick]').evaluateAll((els) => els.map((e) => e.getAttribute('data-manager-pick')));
      expect(picks[0] === 'redisinsight', `more: Redis Insight proposed for Redis (${picks.join(', ')})`);
      await shotDialog(page, name, '31-redis-review');
      await created(page, ['redis', 'redis-redisinsight'], 300);
    }
    expect(await running('redis-redisinsight'), 'more: Redis and Redis Insight run');
    for (const c of ['redis', 'redis-redisinsight']) {
      const n = Object.keys((await inspect(c)).NetworkSettings.Networks);
      expect(!n.includes('bridge'), `more: ${c} is not on the default bridge (${n.join(', ')})`);
    }
    const rUrl = await managerUrl(admin, 'redis-redisinsight');
    expect(/^https:\/\/[^/]+:2053$/.test(rUrl), `more: Redis Insight's address is on 2053 (${rUrl})`);
    let rOk = false;
    await until('Redis Insight answers through Cosmos', async () => {
      await page.goto(rUrl, { waitUntil: 'domcontentloaded', timeout: 30000 });
      // through Cosmos' sign-in for the app's own name first (the OpenID round trip), then the app
      await page.waitForURL((u) => String(u).startsWith(rUrl), { timeout: 20000 }).catch(() => {});
      await page.waitForTimeout(1500);
      // its own page (the panel's sign-in page is on another name); /api/databases below proves it is Redis Insight
      rOk = page.url().startsWith(rUrl) && (await page.locator('#root').count()) > 0;
      return rOk;
    }, 120);
    expect(rOk && page.url().startsWith(rUrl), `more: Redis Insight opens through Cosmos and stays on its address (${page.url()})`);
    // Redis Insight lists the database Cosmos registered once its terms are accepted in its page (done here as
    // the page does; Cosmos never accepts them for the owner)
    const dbs = await page.evaluate(async () => {
      const spec = await (await fetch('/api/settings/agreements/spec')).json();
      const agreements = Object.fromEntries(Object.keys(spec.agreements || {}).map((k) => [k, k === 'eula' || k === 'encryption']));
      await fetch('/api/settings', { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ agreements }) });
      for (let i = 0; i < 20; i += 1) {
        const r = await fetch('/api/databases');
        const text = await r.text();
        if (/"host":"redis"/.test(text)) return { status: r.status, text: text.slice(0, 300) };
        await new Promise((ok) => setTimeout(ok, 1000));
      }
      const r = await fetch('/api/databases');
      return { status: r.status, text: (await r.text()).slice(0, 300) };
    });
    expect(dbs.status === 200 && /"host":"redis"/.test(dbs.text), `more: Redis Insight has Redis registered (${dbs.status} ${dbs.text.slice(0, 120)})`);
    await shot(page, name, '32-redisinsight');

    // --- the URL check does not flag a manager
    for (const route of ['postgres-pgadmin', 'mariadb-phpmyadmin', 'olddb-pgadmin', 'olddb-adminer', 'redis-redisinsight']) {
      if (!(await inspect(route))) { note(`  (${route} is not there: not checked)`); continue; }
      const r = await admin.post('/cosmos/api/url-check', { data: { route } });
      const data = ((await r.json().catch(() => ({}))).data) || {};
      expect(r.status() === 200 && ['works', 'answers'].includes(data.verdict), `more: "Does it open?" for ${route}: ${data.verdict} ${data.reason || ''} ${data.line || ''}`.trim());
      await sleep(1500);
    }
    // still administrators-only behind the sign-in
    const routes = ((await (await admin.get('/cosmos/api/config')).json()).data.HTTPConfig.ProxyConfig.Routes || []);
    for (const r of routes.filter((x) => /-(pgadmin|phpmyadmin|adminer|redisinsight)$/.test(x.Name))) {
      expect(r.AuthEnabled && r.AdminOnly, `more: ${r.Name} is for administrators behind the sign-in`);
    }
  } catch (e) {
    failures.push(`${name}: ${e.message.split('\n')[0]}`);
    await shot(page, name, 'zz-error').catch(() => {});
  }
  await browser.close();
}

// ======================================================================= the start-problem repair (second review R1)
if (phase === 'repair') {
  const name = 'repair-chromium-1366-light';
  const { browser, page } = await open(chromium, name, { width: 1366, height: 900 }, 'light');
  try {
    // (a) PostgreSQL with pgAdmin, its password taken out of the definition by hand: it does not start; Apply
    await toReview(page, 'postgres', 'postgres', 'repairdb');
    await page.locator('[data-any-image-manager] [data-manager-pick]').first().waitFor({ timeout: 30000 });
    await page.waitForTimeout(3000);
    await page.locator('[data-any-image-review] [data-definition-door]').click();
    const editor = page.locator('[data-any-image-review] textarea').first();
    await editor.waitFor({ timeout: 10000 });
    const text = await editor.inputValue();
    const json = JSON.parse(text);
    json.services.repairdb.environment = json.services.repairdb.environment.filter((l) => !l.startsWith('POSTGRES_PASSWORD='));
    await editor.fill(JSON.stringify(json, null, 2));
    lastCreate = Date.now();
    await page.locator('[data-any-image-review] button.shinyButton').click();
    const problem = page.locator('[data-start-problem="repairdb"]');
    await problem.waitFor({ timeout: 180000 }).catch(() => {});
    expect(await problem.count() === 1, '(a) the database without its password does not start, and the review says so');
    expect(await problem.locator('[data-repair-setting="POSTGRES_PASSWORD"]').count() === 1, '(a) the repair proposes POSTGRES_PASSWORD');
    await shot(page, name, '40-repair-database');
    lastCreate = Date.now();
    await problem.locator('[data-start-apply]').click();
    const ok = await until('repairdb and its manager run after Apply', async () => (await healthy('repairdb')) && (await running('repairdb-pgadmin')), 180);
    await page.waitForTimeout(16000);
    const refused = await page.locator('[data-create-failed]').innerText().catch(() => '');
    expect(ok && !refused, `(a) Apply creates them again, the manager's network accepted (${refused.slice(0, 160)})`);
    for (const c of ['repairdb', 'repairdb-pgadmin']) {
      const nets = Object.keys((await inspect(c)).NetworkSettings.Networks);
      expect(nets.length === 1 && /^repairdb-manager-/.test(nets[0]), `(a) ${c} on its own network only (${nets.join(', ')})`);
    }
    await shot(page, name, '41-repair-database-done');

    // (b) an app without a recipe whose sign-in is guessed; its start check names ADMIN_USER; Apply adds it
    await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
    await toReview(page, 'example/needsuser:1', 'example/needsuser', 'needsuser');
    await page.waitForTimeout(3000);
    lastCreate = Date.now();
    await page.locator('[data-any-image-review] button.shinyButton').click();
    const p2 = page.locator('[data-start-problem="needsuser"]');
    await p2.waitFor({ timeout: 180000 }).catch(() => {});
    expect(await p2.locator('[data-repair-setting="ADMIN_USER"]').count() === 1, '(b) the start check names ADMIN_USER');
    const before = (await inspect('needsuser')).Config.Labels['cosmos-fork-signin'];
    // the image's empty ADMIN_EMAIL is its own default, not sent: the guess has the password only
    expect(before === 'where=web;password=ADMIN_PASSWORD;from=guess', `(b) the guess's label (${before})`);
    await p2.locator('[data-repair-setting="ADMIN_USER"] input').fill('owner_name').catch(() => {});
    lastCreate = Date.now();
    await p2.locator('[data-start-apply]').click();
    const ok2 = await until('needsuser runs after Apply', () => running('needsuser'), 120);
    await page.waitForTimeout(16000);
    const after = ok2 ? (await inspect('needsuser')).Config.Labels['cosmos-fork-signin'] : '';
    const refused2 = await page.locator('[data-create-failed]').innerText().catch(() => '');
    expect(ok2 && !refused2 && after === 'where=web;user=ADMIN_USER;password=ADMIN_PASSWORD;from=guess', `(b) Apply creates it again, with the new guess (${after}) ${refused2.slice(0, 120)}`);
    await shot(page, name, '42-repair-guess-done');
  } catch (e) {
    failures.push(`${name}: ${e.message.split('\n')[0]}`);
    await shot(page, name, 'zz-error').catch(() => {});
  }
  await browser.close();
}

if (phase === 'delete' || phase === 'all') {
  const name = 'delete-chromium-1366-light';
  const { browser, page } = await open(chromium, name, { width: 1366, height: 900 }, 'light');
  try {
    await api.post(`${dind}/containers/mariadb/stop`);
    await until('mariadb stopped', async () => !(await running('mariadb')), 60);
    await page.goto(`${base}/cosmos-ui/servapps/containers/mariadb`, { waitUntil: 'domcontentloaded' });
    await later(page);
    const del = page.getByRole('button', { name: /^Delete$/ }).first();
    await del.waitFor({ timeout: 45000 });
    await del.click();
    const offer = page.locator('[data-delete-managers]');
    await offer.waitFor({ timeout: 20000 });
    const label = await page.locator('label', { has: offer }).innerText();
    expect(/Also delete its web manager \(mariadb-phpmyadmin\)/.test(label), `8 delete: deleting the database offers its web manager (${label})`);
    expect(!(await offer.locator('input').isChecked()), '8 delete: the offer is off until ticked');
    await shot(page, name, '01-delete-offer');
    await offer.locator('input').check();
    await page.waitForTimeout(800);
    expect(/Container \/?mariadb-phpmyadmin/.test(await page.locator('[role=dialog]:visible').innerText()), '8 delete: ticked, the manager is listed with the rest');
    await shot(page, name, '02-delete-ticked');
    await page.locator('[role=dialog]:visible').getByRole('button', { name: /^Delete$/ }).click();
    const gone = await until('mariadb and its manager are gone', async () => !(await inspect('mariadb')) && !(await inspect('mariadb-phpmyadmin')), 90);
    expect(gone, '8 delete: both are deleted');
    await shot(page, name, '03-deleted');
  } catch (e) {
    failures.push(`${name}: ${e.message.split('\n')[0]}`);
    await shot(page, name, 'zz-error').catch(() => {});
  }
  await browser.close();
}

await api.dispose();
if (noise.length) note(`\n${noise.length} WebKit page error(s) of refused or cut requests (not failed):\n- ` + [...new Set(noise)].join('\n- '));
note('\ncounts (ok/fail) per step or view:');
Object.entries(counts).forEach(([k, v]) => note(`  ${k}: ${v.ok}/${v.fail}`));
note(failures.length ? `\n${failures.length} FAILURES:\n- ` + failures.join('\n- ') : '\nall checks passed');
process.exit(failures.length ? 1 : 0);

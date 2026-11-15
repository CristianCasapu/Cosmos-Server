// Fork addition: what scripts/fork-ui-check-turnstile.mjs leaves out, run after it on the same set-up (its
// header: the --internal network with the Cloudflare stand-in, the test Cosmos in Turnstile test mode with
// Cloudflare's test keys switched on, this script in the Playwright image on that network and on an ordinary
// one for Cloudflare's real widget). Never the production instance, never the owner's Cloudflare account.
//
//   PHASE=notice NOTICE=problem   Cloudflare's siteverify failing (the stand-in's /siteverify/down): the home
//                                 page's warning is a line of its notice area, with its link and the words a
//                                 screen reader hears
//   PHASE=notice NOTICE=two       the same with two problems at once (the server's answer changed in the
//                                 browser): the area folds and unfolds around the warning
//   PHASE=notice NOTICE=console   with <config folder>/turnstile.off created on the instance beforehand
//   PHASE=interplay               the login shield and the password confirmation limit behind the check, an
//                                 API token (never asked, cut to its owner's role), logout by POST and the
//                                 check again in the same page (SKIP_SHIELD=1 leaves out the shield's ~8 min)
//   ENGINES=chromium,firefox,webkit SIZES=d,m MODES=light,dark
// "A visitor from the internet" sends X-Forwarded-For from a trusted proxy range through the browser's own
// extraHTTPHeaders (changing requests in flight makes Chromium send the panel's cookie to other names).
import { chromium, firefox, webkit, request } from 'playwright';
import { mkdirSync } from 'node:fs';

const base = 'https://cosmos.example.org:2053';
const fake = 'http://fake:8080';
const password = 'Test-Passw0rd!x';
const DUMMY = 'XXXX.DUMMY.TOKEN.XXXX';
const engines = { chromium, firefox, webkit };
const viewports = { d: { width: 1366, height: 900 }, m: { width: 390, height: 844 } };
const phase = process.env.PHASE || 'notice';
const failures = [];
let oks = 0;
const expect = (ok, what) => { console.log(ok ? '  ok  ' : '  FAIL', what); if (ok) oks++; else failures.push(what); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
mkdirSync('shots', { recursive: true });
const api = await request.newContext({ ignoreHTTPSErrors: true });

const session = async (nickname = 'admin', pass = password, headers = {}) => {
  const ctx = await request.newContext({ ignoreHTTPSErrors: true, extraHTTPHeaders: headers });
  const login = await ctx.post(base + '/cosmos/api/login', { data: { nickname, password: pass }, headers: headers['X-Forwarded-For'] ? { 'X-Cosmos-Turnstile': DUMMY } : {} });
  const sudo = await ctx.post(base + '/cosmos/api/sudo', { data: { password: pass } });
  return { ctx, login: login.status(), sudo: sudo.status() };
};

const watch = async (ctx, name) => {
  const violations = [];
  await ctx.exposeBinding('__v', (s, v) => violations.push(JSON.stringify(v)));
  await ctx.addInitScript(() => document.addEventListener('securitypolicyviolation', (e) => { try { window.__v({ d: e.effectiveDirective, b: e.blockedURI }); } catch (x) { /* gone */ } }, true));
  ctx.on('page', (p) => p.on('console', (m) => {
    // Cloudflare's own frame carries a policy of its own that Firefox reports on (unknown directives): not the panel's
    if (/challenges\.cloudflare\.com\/cdn-cgi/.test(m.text()) && /unknown directive/i.test(m.text())) return;
    if (/Content.Security.Policy|Refused to/i.test(m.text())) violations.push(m.text().slice(0, 200));
  }));
  return violations;
};

if (phase === 'notice') {
  const kind = process.env.NOTICE || 'problem';
  if (kind === 'problem') {
    // a visitor from the internet while Cloudflare answers with errors: the server notes the problem
    await api.get(fake + '/siteverify/down?mode=500');
    const visitor = await request.newContext({ ignoreHTTPSErrors: true, extraHTTPHeaders: { 'X-Forwarded-For': '203.0.113.201' } });
    const r = await visitor.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password }, headers: { 'X-Cosmos-Turnstile': DUMMY } });
    const code = (await r.json().catch(() => ({}))).code;
    expect(r.status() === 503 && code === 'TS004', `Cloudflare failing: a visitor's sign-in is refused with TS004 (${r.status()} ${code})`);
    await api.get(fake + '/siteverify/down?mode=');
  }
  const admin = await session();
  const status = (await (await admin.ctx.get(base + '/cosmos/api/turnstile')).json()).data || {};
  console.log('  card state:', JSON.stringify({ enabled: status.enabled, consoleOff: status.consoleOff, problems: status.problems }).slice(0, 300));
  const key = kind === 'console' ? 'console' : (kind === 'two' ? 'key_refused' : 'unreachable');
  for (const engineName of (process.env.ENGINES || 'chromium,firefox,webkit').split(',')) {
    for (const size of (process.env.SIZES || 'd,m').split(',')) {
      for (const mode of (process.env.MODES || 'light,dark').split(',')) {
        const name = `${engineName}-${size}-${mode}-${kind}`;
        const browser = await engines[engineName].launch();
        const ctx = await browser.newContext({ viewport: viewports[size], ignoreHTTPSErrors: true, colorScheme: mode, locale: 'en-GB', serviceWorkers: kind === 'two' ? 'block' : 'allow' });
        const violations = await watch(ctx, name);
        await ctx.addInitScript((m) => { try { localStorage.setItem('ct-mode', m); } catch (e) { /* none */ } }, mode);
        if (kind === 'two') {
          // the server's answer with both problems at once (Cloudflare refusing the key, and unreachable before),
          // so that the area has more lines than it shows and folds
          await ctx.route(/\/cosmos\/api\/turnstile(\?|$)/, async (route) => {
            const answer = await route.fetch().catch((e) => { console.log('  route.fetch failed:', e.message.split('\n')[0]); return null; });
            if (!answer) return route.continue();
            const json = await answer.json();
            const since = new Date(Date.now() - 3600000).toISOString();
            json.data.problems = [{ reason: 'key_refused', since, last: since, count: 3 }, { reason: 'unreachable', since, last: since, count: 1 }];
            await route.fulfill({ response: answer, json });
          });
          await ctx.route('**/sw.js', (route) => route.abort());
        }
        const page = await ctx.newPage();
        await page.goto(base + '/cosmos-ui/login', { waitUntil: 'domcontentloaded' });
        await page.locator('#nickname-login').fill('admin');
        await page.locator('[name=password]').fill(password);
        await page.locator('form button[type=submit]').first().click();
        await page.waitForURL(/cosmos-ui\/?$/, { timeout: 30000 }).catch(() => {});
        const area = page.locator('main [data-notices]').first();
        await area.waitFor({ timeout: 30000 }).catch(() => {});
        await page.waitForTimeout(2500);
        const line = page.locator(`[data-turnstile-alert="${key}"]`);
        let visible = await line.count() > 0;
        const toggle = area.locator('button.ct-notices-toggle');
        const lines = await area.locator('li.ct-notice').count();
        const foldable = await toggle.count() > 0;
        if (!visible && foldable) {
          expect((await toggle.getAttribute('aria-expanded')) === 'false', `${name}: the folded area says it is folded (aria-expanded=false)`);
          const said = await toggle.innerText();
          expect(/Show \d+ more \(/.test(said), `${name}: the toggle says what is folded (${said})`);
          await toggle.click();
          await page.waitForTimeout(400);
          expect((await toggle.getAttribute('aria-expanded')) === 'true', `${name}: opened, aria-expanded=true`);
          const controls = await toggle.getAttribute('aria-controls');
          expect(!!controls && (await page.locator(`[id="${controls}"]`).count()) === 1, `${name}: the toggle names the list it opens`);
          visible = await line.count() > 0;
        }
        console.log(`  ${name}: ${lines} lines shown at first, foldable ${foldable}`);
        if (foldable && (await toggle.getAttribute('aria-expanded')) === 'false') {
          // the area folds and unfolds around the warning, and says so
          const said = await toggle.innerText();
          await toggle.click();
          await page.waitForTimeout(400);
          const all = await area.locator('li.ct-notice').count();
          expect((await toggle.getAttribute('aria-expanded')) === 'true' && all > lines && /Show less/.test(await toggle.innerText()), `${name}: "${said}" opens ${all - lines} more line(s), aria-expanded=true`);
          await area.screenshot({ path: `shots/${name}-notices-open.png` }).catch(() => {});
          // folded again from the keyboard
          await toggle.focus();
          await page.keyboard.press('Enter');
          await page.waitForTimeout(400);
          expect((await toggle.getAttribute('aria-expanded')) === 'false' && (await area.locator('li.ct-notice').count()) === lines && (await line.count()) > 0, `${name}: folded again with the keyboard, the warning still shown`);
        }
        expect(visible, `${name}: the Turnstile warning is a line of the notice area`);
        if (visible) {
          expect((await area.locator(`li.ct-notice[data-turnstile-alert="${key}"]`).count()) === 1, `${name}: it is a line (li.ct-notice) of the home page's one notice area, not an alert of its own`);
          expect((await page.locator('.MuiAlert-root[data-turnstile-alert]').count()) === 0, `${name}: no separate alert for it`);
          const text = await line.innerText();
          const hidden = await line.locator('.ct-visually-hidden').first().innerText().catch(() => '');
          expect(/Sign-in check of Cloudflare:/.test(text) && (kind === 'console' ? /^Warning:/.test(hidden.trim()) : /^Problem:/.test(hidden.trim())), `${name}: its words, with the severity said for screen readers ("${hidden.trim()}" ${text.replace(/\s+/g, ' ').slice(0, 120)})`);
          const label = await area.getAttribute('aria-label');
          expect(!!label, `${name}: the area has a name (${label})`);
          const over = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
          expect(over <= 1, `${name}: no sideways scroll (${over})`);
          await line.scrollIntoViewIfNeeded();
          await area.screenshot({ path: `shots/${name}-notices.png` }).catch(() => {});
          const link = line.locator('a').first();
          expect((await link.getAttribute('href')) === '/cosmos-ui/config-general/https', `${name}: the line links to the settings`);
          await link.click();
          const card = await page.locator('[data-turnstile-card]').waitFor({ timeout: 30000 }).then(() => true).catch(() => false);
          expect(card && /config-general\/https/.test(page.url()), `${name}: the link opens the settings with the card`);
          if (card) {
            await page.locator('[data-turnstile-card]').scrollIntoViewIfNeeded();
            await page.screenshot({ path: `shots/${name}-card.png` });
          }
        } else await page.screenshot({ path: `shots/${name}-home.png`, fullPage: true });
        expect(violations.length === 0, `${name}: no policy violation (${violations.slice(0, 3).join(' | ')})`);
        await browser.close();
      }
    }
  }
}

if (phase === 'interplay') {
  const admin = await session();
  // --- the login shield after the check: a visitor from the internet with good checks and wrong passwords
  if (process.env.SKIP_SHIELD !== '1') {
    const visitor = '203.0.113.' + (210 + Math.floor(Math.random() * 40));
    const v = await request.newContext({ ignoreHTTPSErrors: true, extraHTTPHeaders: { 'X-Forwarded-For': visitor } });
    const codes = [];
    const started = Date.now();
    for (let i = 0; i < 40 && Date.now() - started < 600000; i++) {
      const r = await v.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: 'Wrong-Passw0rd!' + i }, headers: { 'X-Cosmos-Turnstile': DUMMY } });
      const body = await r.json().catch(() => ({}));
      const code = `${r.status()}${body.code ? ' ' + body.code : ''}`;
      codes.push(code);
      if (r.status() === 429 && body.code === 'TS003') { await sleep(31000); continue; }
      if (r.status() === 429 || (r.status() === 403 && !String(body.code || '').startsWith('TS'))) break;
    }
    console.log('  wrong passwords from', visitor, ':', codes.join(', '));
    // upstream answers a wrong password with 500 UL001
    const wrong = codes.filter((c) => /^(401|500)/.test(c)).length;
    const refused = codes.length - 1;
    const last = codes[codes.length - 1];
    expect(codes.some((c) => c.includes('TS003')), 'the check\'s own limit (6, then one each 30 s) comes before a password is looked at');
    expect(refused >= 15 && refused <= 24 && /^(429|403)/.test(last) && !last.includes('TS'), `after about 20 refused sign-ins (wrong passwords and the check's own refusals together) the login shield closes for that visitor (${wrong} wrong passwords + ${refused - wrong} TS003, then ${last})`);
    const before = (await (await api.get(fake + '/siteverify/calls')).json()).length;
    const after = await v.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password }, headers: { 'X-Cosmos-Turnstile': DUMMY } });
    const callsAfter = (await (await api.get(fake + '/siteverify/calls')).json()).length;
    expect(after.status() !== 200, `the right password from the struck visitor is refused too (${after.status()})`);
    expect(callsAfter === before, `the struck visitor costs no call to Cloudflare (${callsAfter - before})`);
    // the home network is not affected
    const home = await session();
    expect(home.login === 200, `the home network still signs in (${home.login})`);
  }
  // --- the sudo limit for a session that came through the check
  {
    // a second account, so the administrator's own confirmation stays open for the other checks
    const nick = 'sudotest' + Date.now().toString(36).slice(-4);
    const made = await admin.ctx.post(base + '/cosmos/api/users', { data: { nickname: nick, email: nick + '@example.org', role: 2 } });
    const key = ((await made.json().catch(() => ({}))).data || {}).registerKey;
    const reg = await api.post(base + '/cosmos/api/register', { data: { nickname: nick, registerKey: key, password: 'Sudo-Passw0rd!x' } });
    const visitor = { 'X-Forwarded-For': '203.0.113.' + (100 + Math.floor(Math.random() * 50)) };
    const s = await request.newContext({ ignoreHTTPSErrors: true, extraHTTPHeaders: visitor });
    const without = await s.post(base + '/cosmos/api/login', { data: { nickname: nick, password: 'Sudo-Passw0rd!x' } });
    expect(without.status() === 403 && (await without.json()).code === 'TS001', `${nick} (made ${made.status()}, registered ${reg.status()}): from the internet without the check: refused (${without.status()})`);
    const login = await s.post(base + '/cosmos/api/login', { data: { nickname: nick, password: 'Sudo-Passw0rd!x' }, headers: { 'X-Cosmos-Turnstile': DUMMY } });
    expect(login.status() === 200, `${nick}: signed in from the internet with the check (${login.status()})`);
    const codes = [];
    for (let i = 0; i < 5; i++) codes.push((await s.post(base + '/cosmos/api/sudo', { data: { password: 'Wrong-Passw0rd!' } })).status());
    const right = await s.post(base + '/cosmos/api/sudo', { data: { password: 'Sudo-Passw0rd!x' } });
    const body = await right.json().catch(() => ({}));
    expect(codes.every((c) => c !== 200 && c !== 429) && right.status() === 429 && body.code === 'UL010' && right.headers()['retry-after'], `5 wrong confirmations close it: the right password then gets 429 UL010 with Retry-After (${codes.join(',')} then ${right.status()} ${body.code})`);
    expect(!String(body.code || '').startsWith('TS'), 'the confirmation never asks for the check');
    // the second, right-first case: 4 wrong, then right clears
    const nick2 = nick + 'b';
    const made2 = await admin.ctx.post(base + '/cosmos/api/users', { data: { nickname: nick2, email: nick2 + '@example.org', role: 2 } });
    const key2 = ((await made2.json().catch(() => ({}))).data || {}).registerKey;
    await api.post(base + '/cosmos/api/register', { data: { nickname: nick2, registerKey: key2, password: 'Sudo-Passw0rd!x' } });
    const s2 = await request.newContext({ ignoreHTTPSErrors: true, extraHTTPHeaders: visitor });
    await s2.post(base + '/cosmos/api/login', { data: { nickname: nick2, password: 'Sudo-Passw0rd!x' }, headers: { 'X-Cosmos-Turnstile': DUMMY } });
    for (let i = 0; i < 4; i++) await s2.post(base + '/cosmos/api/sudo', { data: { password: 'Wrong-Passw0rd!' } });
    expect((await s2.post(base + '/cosmos/api/sudo', { data: { password: 'Sudo-Passw0rd!x' } })).status() === 200, `${nick2}: 4 wrong, then the right password confirms`);
    // --- an API token of that account: never asked for the check, cut to what its owner holds now
    const tokenMade = await s2.post(base + '/cosmos/api/api-tokens', { data: { name: 'full-' + nick2, description: 'test', readOnly: false, expiryDays: 1 } });
    const tj = await tokenMade.json().catch(() => ({}));
    const token = (tj.data && (tj.data.token || tj.data.Token)) || '';
    expect(!!token, `${nick2}: made a full token (${tokenMade.status()})`);
    if (token) {
      const t = await request.newContext({ ignoreHTTPSErrors: true, extraHTTPHeaders: { ...visitor, Authorization: 'Bearer ' + token } });
      const cfg = await t.get(base + '/cosmos/api/turnstile');
      const users = await t.get(base + '/cosmos/api/users');
      expect(cfg.status() === 200 && users.status() === 200, `the token reads the Turnstile settings (${cfg.status()}) and the users (${users.status()}) from the internet while its owner is an administrator`);
      const reset = await t.post(base + '/cosmos/api/password-reset', { data: { nickname: nick2, email: nick2 + '@example.org' } });
      expect(!String((await reset.json().catch(() => ({}))).code || '').startsWith('TS'), `the token is not asked for the check on a protected form (${reset.status()})`);
      const demote = await admin.ctx.patch(base + '/cosmos/api/users/' + nick2, { data: { role: 1 } });
      const role = (((await (await admin.ctx.get(base + '/cosmos/api/users/' + nick2)).json().catch(() => ({}))).data) || {}).role;
      const cfg2 = await t.get(base + '/cosmos/api/turnstile');
      const users2 = await t.get(base + '/cosmos/api/users');
      const change = await t.post(base + '/cosmos/api/turnstile', { data: { action: 'disable' } });
      expect(demote.status() === 200 && role === 1 && cfg2.status() === 403 && users2.status() === 403 && change.status() === 403, `demoted to a user (${demote.status()}, role ${role}), the same token is cut: Turnstile settings ${cfg2.status()}, users ${users2.status()}, switching the check off ${change.status()}`);
      const del = await admin.ctx.delete(base + '/cosmos/api/users/' + nick2);
      const me = await t.get(base + '/cosmos/api/me');
      expect(del.status() === 200 && me.status() >= 400, `owner deleted (${del.status()}): the token is refused (${me.status()})`);
    }
    await admin.ctx.delete(base + '/cosmos/api/users/' + nick).catch(() => {});
  }
  // --- logout by POST from a page whose sign-in had the widget: a visitor from the internet signs in through the
  // check, logs out (POST), and the same page shows the check again and signs in again
  let n = 0;
  for (const engineName of (process.env.ENGINES || 'chromium,firefox,webkit').split(',')) {
    for (const size of ['d', 'm']) {
      const name = `${engineName}-${size}`;
      const browser = await engines[engineName].launch();
      const ctx = await browser.newContext({ viewport: viewports[size], ignoreHTTPSErrors: true, locale: 'en-GB', extraHTTPHeaders: { 'X-Forwarded-For': '203.0.113.' + (160 + n++) } });
      const violations = await watch(ctx, name);
      const page = await ctx.newPage();
      const calls = [];
      page.on('request', (r) => { if (/\/cosmos\/api\/(logout|login)$/.test(r.url())) calls.push(r.method() + ' ' + r.url().split('/').pop() + (r.headers()['x-cosmos-turnstile'] ? '+check' : '')); });
      page.on('response', (r) => { if (/\/cosmos\/api\/(logout|login)$/.test(r.url())) calls.push(r.status()); });
      const signIn = async (round) => {
        await page.locator('[data-turnstile=login]').waitFor({ timeout: 30000 }).catch(() => {});
        let state = '';
        for (let i = 0; i < 40 && state !== 'ready'; i++) { state = await page.locator('[data-turnstile=login]').getAttribute('data-turnstile-state').catch(() => ''); if (state !== 'ready') await page.waitForTimeout(1000); }
        expect(state === 'ready', `${name}: round ${round}: the check passes`);
        await page.locator('#nickname-login').fill('admin');
        await page.locator('[name=password]').fill(password);
        await page.locator('form button[type=submit]').first().click();
        await page.waitForURL(/cosmos-ui\/?$/, { timeout: 30000 }).catch(() => {});
        expect(!/\/login/.test(page.url()), `${name}: round ${round}: signed in through the check (${page.url().replace(base, '')})`);
      };
      await page.goto(base + '/cosmos-ui/login', { waitUntil: 'domcontentloaded' });
      await signIn(1);
      const logout = page.locator('a[href="/cosmos-ui/logout"]:visible, [role=menuitem]:has-text("Logout"):visible').first();
      await logout.click({ timeout: 10000 }).catch((e) => console.log('  ', name, 'logout control:', e.message.split('\n')[0]));
      await page.waitForURL(/\/login/, { timeout: 30000 }).catch(() => {});
      await page.waitForTimeout(1000);
      const me = await page.request.get(base + '/cosmos/api/me');
      expect(calls.includes('POST logout') && !calls.some((c) => /^GET logout/.test(c)) && me.status() === 401 && /\/login/.test(page.url()), `${name}: logged out by POST (${calls.join(' ')}), /me ${me.status()}`);
      await signIn(2);
      await page.screenshot({ path: `shots/${name}-after-logout-and-back.png` });
      expect(violations.length === 0, `${name}: no policy violation (${violations.slice(0, 3).join(' | ')})`);
      await browser.close();
    }
  }
}

console.log(`\n${oks} ok, ${failures.length} failed`);
for (const f of failures) console.log('  - ' + f);
process.exit(failures.length ? 1 : 0);

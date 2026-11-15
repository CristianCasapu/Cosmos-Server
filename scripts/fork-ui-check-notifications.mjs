// Fork addition: checks the notifications page, the details of a notification
// and the bell in headless Chromium, Firefox and WebKit, against a throwaway
// Cosmos instance, never the production one (CLAUDE.md, "Checking the UI in a
// browser").
//
// What it needs:
// - the instance at BASE (a name with a dot for WebKit), set up, holding real
//   notifications for the administrator (some unread, at least one with steps
//   and links: the UPnP failure of the stand-in router is the worked example)
//   and none of them addressed to USER_USER;
// - an administrator ADMIN_USER / ADMIN_PASS and a user without admin rights
//   USER_USER / USER_PASS who finished the invitation (the gallery's seed makes
//   "maria" with Other-Passw0rd!y).
//
//   node scripts/fork-ui-check-notifications.mjs
//     BASE=http://notif.test         ADMIN_USER=admin ADMIN_PASS=…  USER_USER=maria USER_PASS=…
//     ENGINES=chromium,firefox,webkit SIZES=d,m,s (1366x900, 393x851, 360x800)  MODES=light,dark
//     SHOTS=shots                     where the screenshots go
//     FIXTURES=1                      a dry run of the script itself: the notifications routes are answered by
//                                     the gallery's fixtures (no state is kept: the read checks fail there), and
//                                     the part of the user without admin rights is skipped
//
// It taps an item of the bell and lands on its details, follows the links
// of the details (and presses "Check now" when the notification has it),
// checks that opening marked it read, marks it unread and read again from the
// list, filters by level, read state and text through the page's controls,
// deletes nothing of the administrator's, and checks that the user without
// admin rights sees only their own notifications: the administrator's are
// neither listed nor readable by number, the surrounding context is refused,
// marking and deleting them changes nothing, the commands refuse GET.
import { chromium, firefox, webkit } from 'playwright';
import { mkdirSync } from 'node:fs';
import { notificationPagesMock } from './fork-ui-gallery-fixtures.mjs';

const base = process.env.BASE || 'http://notif.test';
const admin = { nickname: process.env.ADMIN_USER || 'admin', password: process.env.ADMIN_PASS || 'Test-Passw0rd!x' };
const user = { nickname: process.env.USER_USER || 'maria', password: process.env.USER_PASS || 'Other-Passw0rd!y' };
const engines = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const SIZE = { d: { width: 1366, height: 900 }, m: { width: 393, height: 851 }, s: { width: 360, height: 800 } };
const sizes = (process.env.SIZES || 'd,m,s').split(',');
const modes = (process.env.MODES || 'light,dark').split(',');
const shots = process.env.SHOTS || 'shots';
const types = { chromium, firefox, webkit };
const fixtures = process.env.FIXTURES === '1';
const useFixtures = async (ctx) => {
  for (const [part, body] of notificationPagesMock) {
    await ctx.route((url) => url.pathname.startsWith(part), (route) => route.fulfill({ json: typeof body === 'function' ? body(new URL(route.request().url())) : body }));
  }
};
const failures = [];
const note = (...a) => console.log(...a);
const expect = (ok, what) => { note(ok ? '  ok  ' : '  FAIL', what); if (!ok) failures.push(what); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
mkdirSync(shots, { recursive: true });

const signIn = async (ctx, who) => {
  const l = await ctx.request.post(base + '/cosmos/api/login', { data: who, failOnStatusCode: false });
  const s = await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: who.password }, failOnStatusCode: false });
  return [l.status(), s.status()];
};
// the API from inside a panel page: the same-origin check refuses a request without the page's headers
const api = (page) => ({
  get: (path) => page.evaluate(async (p) => { const r = await fetch(p); let b = null; try { b = await r.json(); } catch (e) { /* not JSON */ } return { status: r.status, body: b }; }, '/cosmos/api/notifications/' + path),
  post: (path, data) => page.evaluate(async ([p, d]) => {
    const r = await fetch(p, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(d) });
    let b = null; try { b = await r.json(); } catch (e) { /* not JSON */ } return { status: r.status, body: b };
  }, ['/cosmos/api/notifications/' + path, data]),
  raw: (path, method) => page.evaluate(async ([p, m]) => (await fetch(p, { method: m })).status, ['/cosmos/api/notifications/' + path, method]),
});
const noSideways = async (page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1);

let adminIds = [];

for (const engine of engines) {
  for (const size of sizes) {
    for (const mode of modes) {
      const label = `${engine}-${size}-${mode}`;
      note('\n' + label);
      const browser = await types[engine].launch();
      const ctx = await browser.newContext({ viewport: SIZE[size], colorScheme: mode, ignoreHTTPSErrors: true, serviceWorkers: 'block' });
      if (fixtures) await useFixtures(ctx);
      const page = await ctx.newPage();
      const errors = [];
      page.on('pageerror', (e) => errors.push(e.message.slice(0, 200)));
      const [login, sudo] = await signIn(ctx, admin);
      expect(login === 200 && sudo === 200, `${label} admin signs in (${login}/${sudo})`);
      await page.goto(base + '/cosmos-ui/notifications', { waitUntil: 'domcontentloaded' });
      await page.locator('[data-notifications-list] [data-notification]').first().waitFor({ timeout: 15000 }).catch(() => {});
      const a = api(page);
      const list = await a.get('list?limit=100');
      const items = (list.body && list.body.data && list.body.data.items) || [];
      adminIds = items.map((n) => n.id);
      expect(list.status === 200 && items.length > 0, `${label} the list answers with the admin's notifications (${items.length})`);
      expect(await noSideways(page), `${label} the page does not scroll sideways`);
      await page.screenshot({ path: `${shots}/${label}-page.png`, fullPage: true });

      // the bell: tap an item, land on its details
      await page.locator('header button:has(.anticon-bell)').first().click();
      await page.locator('nav [data-notification]').first().waitFor({ timeout: 8000 }).catch(() => {});
      await page.screenshot({ path: `${shots}/${label}-bell.png` });
      const tapped = await page.locator('nav [data-notification]').first().getAttribute('data-notification').catch(() => null);
      await page.locator('nav [data-notification]').first().click().catch(() => {});
      await page.waitForURL(/\/cosmos-ui\/notifications\/\d+$/, { timeout: 8000 }).catch(() => {});
      expect(tapped && page.url().endsWith('/cosmos-ui/notifications/' + tapped), `${label} a bell item opens its details (${page.url().replace(base, '')})`);
      await page.locator('[data-notification-details]').waitFor({ timeout: 10000 }).catch(() => {});
      const title = (await page.locator('main h1').first().textContent().catch(() => '')) || '';
      expect(title.trim().length > 0, `${label} the details have a title ("${title.trim().slice(0, 60)}")`);
      expect(await page.getByText('What happened').count() > 0, `${label} the details say what happened`);
      await sleep(1500);
      expect(await noSideways(page), `${label} the details do not scroll sideways`);
      await page.screenshot({ path: `${shots}/${label}-details.png`, fullPage: true });
      if (tapped) {
        const after = await a.get('item?id=' + tapped);
        expect(after.status === 200 && after.body.data.read === true, `${label} opening the details marked it read`);
      }

      // the worked example: the first notification with steps; press "Check now" when it has it
      const withSteps = [];
      for (const n of items.slice(0, 30)) {
        const d = await a.get('item?id=' + n.id);
        if (d.status === 200 && d.body.data && Array.isArray(d.body.data.steps) && d.body.data.steps.length) withSteps.push(n.id);
      }
      expect(withSteps.length > 0, `${label} some notifications carry steps (${withSteps.length})`);
      if (withSteps.length) {
        await page.goto(base + '/cosmos-ui/notifications/' + withSteps[0], { waitUntil: 'domcontentloaded' });
        await page.locator('[data-notification-details]').waitFor({ timeout: 10000 }).catch(() => {});
        await sleep(1500);
        const check = page.locator('main button').filter({ hasText: /^Check now$/ });
        if (await check.count()) {
          const answer = page.waitForResponse((r) => r.url().endsWith('/cosmos/api/upnp') && r.request().method() === 'POST', { timeout: 30000 }).catch(() => null);
          await check.first().click();
          const r = await answer;
          expect(r && r.status() === 200, `${label} "Check now" asks the router (${r ? r.status() : 'no request'})`);
        }
        const hrefs = [...new Set(await page.locator('main a[href^="/cosmos-ui/"]').evaluateAll((els) => els.map((e) => e.getAttribute('href'))))]
          .filter((h) => !/^\/cosmos-ui\/notifications(\/|$)/.test(h)).slice(0, 6);
        for (const href of hrefs) {
          const before = errors.length;
          await page.goto(base + href, { waitUntil: 'domcontentloaded' });
          await sleep(1200);
          const missing = await page.getByText(/page (does not exist|not found)/i).count();
          expect(missing === 0 && errors.length === before, `${label} its link ${href} opens a page`);
        }
      }

      // the list: mark one unread and read again, filters through the controls
      await page.goto(base + '/cosmos-ui/notifications', { waitUntil: 'domcontentloaded' });
      await page.locator('[data-notifications-list] [data-notification]').first().waitFor({ timeout: 10000 }).catch(() => {});
      const card = page.locator('[data-notifications-list] [data-notification]').first();
      const cardId = await card.getAttribute('data-notification').catch(() => null);
      const readBefore = cardId ? (await a.get('item?id=' + cardId)).body.data.read : null;
      await card.locator(`button[aria-label="${readBefore ? 'Mark as unread' : 'Mark as read'}"]`).click().catch(() => {});
      await sleep(800);
      const readAfter = cardId ? (await a.get('item?id=' + cardId)).body.data.read : null;
      expect(cardId && readAfter === !readBefore, `${label} the read button changes the read state (${readBefore} → ${readAfter})`);
      await card.locator(`button[aria-label="${readAfter ? 'Mark as unread' : 'Mark as read'}"]`).click().catch(() => {});
      await sleep(800);
      expect(cardId && (await a.get('item?id=' + cardId)).body.data.read === readBefore, `${label} and back`);

      await page.getByLabel('Level').click().catch(() => {});
      await page.getByRole('option', { name: 'Error' }).click().catch(() => {});
      await page.waitForURL(/level=error/, { timeout: 5000 }).catch(() => {});
      await sleep(1000);
      const shownIds = await page.locator('[data-notifications-list] [data-notification]').evaluateAll((els) => els.map((e) => Number(e.getAttribute('data-notification'))));
      const errorIds = new Set(items.filter((n) => n.level === 'error').map((n) => n.id));
      expect(page.url().includes('level=error') && shownIds.every((id) => errorIds.has(id) || !adminIds.includes(id)), `${label} the level filter shows errors only (${shownIds.length})`);
      await page.goto(base + '/cosmos-ui/notifications?read=unread', { waitUntil: 'domcontentloaded' });
      await sleep(1500);
      const unreadShown = await page.locator('[data-notifications-list] [data-notification]').count();
      const unreadCount = (await a.get('list?read=unread&limit=100')).body.data.items.length;
      expect(unreadShown === Math.min(unreadCount, 30), `${label} the read filter (${unreadShown} of ${unreadCount})`);
      await page.goto(base + '/cosmos-ui/notifications', { waitUntil: 'domcontentloaded' });
      await page.getByLabel('Search').fill('zzzz-nothing-matches-this');
      await page.waitForURL(/q=zzzz/, { timeout: 5000 }).catch(() => {});
      await sleep(1200);
      expect(await page.locator('[data-notifications-list] [data-notification]').count() === 0 && await page.getByText('No notification matches these filters.').count() > 0,
        `${label} a search without match says so`);
      expect(errors.length === 0, `${label} no script error${errors.length ? ': ' + errors.join(' | ') : ''}`);
      await browser.close();
    }
  }

  // the user without admin rights, once per engine
  if (!fixtures) {
    const label = `${engine}-user`;
    note('\n' + label);
    const browser = await types[engine].launch();
    const ctx = await browser.newContext({ viewport: SIZE.m, ignoreHTTPSErrors: true, serviceWorkers: 'block' });
    const page = await ctx.newPage();
    const [login] = await signIn(ctx, user);
    expect(login === 200, `${label} signs in (${login})`);
    await page.goto(base + '/cosmos-ui/notifications', { waitUntil: 'domcontentloaded' });
    await sleep(2000);
    const a = api(page);
    const own = await a.get('list?limit=100');
    const ownIds = ((own.body && own.body.data && own.body.data.items) || []).map((n) => n.id);
    expect(own.status === 200 && ownIds.every((id) => !adminIds.includes(id)), `${label} lists none of the admin's notifications (${ownIds.length} own)`);
    const target = adminIds[0];
    expect(!!target, `${label} has an admin notification to try`);
    if (target) {
      const item = await a.get('item?id=' + target);
      expect(item.status === 404 || item.status === 403, `${label} cannot read the admin's notification by number (${item.status})`);
      const context = await a.get('context?id=' + target);
      expect([401, 403, 404].includes(context.status), `${label} the context is refused (${context.status})`);
      const ownContext = ownIds[0] ? await a.get('context?id=' + ownIds[0]) : { status: 403 };
      expect([401, 403].includes(ownContext.status), `${label} the context of an own notification is refused too: security record, events and log are for admins (${ownContext.status})`);
      await a.post('mark', { ids: [target], read: false });
      await a.post('delete', { ids: [target] });
      expect(await a.raw('mark', 'GET') === 405 && await a.raw('delete', 'GET') === 405, `${label} the commands refuse GET`);
      await page.goto(base + '/cosmos-ui/notifications/' + target, { waitUntil: 'domcontentloaded' });
      await sleep(2000);
      expect(await page.getByText(/not one of yours/).count() > 0, `${label} the admin's notification shows as not found`);
      await page.screenshot({ path: `${shots}/${label}-not-found.png`, fullPage: true });
    }
    await browser.close();
    // what the user tried did not touch the admin's copy
    if (target) {
      const b2 = await types[engine].launch();
      const c2 = await b2.newContext({ ignoreHTTPSErrors: true, serviceWorkers: 'block' });
      const p2 = await c2.newPage();
      await signIn(c2, admin);
      await p2.goto(base + '/cosmos-ui/notifications', { waitUntil: 'domcontentloaded' });
      const still = await api(p2).get('item?id=' + target);
      expect(still.status === 200, `${label} the admin still has notification ${target}`);
      await b2.close();
    }
  }
}

note(failures.length ? `\n${failures.length} FAILED:\n  ` + failures.join('\n  ') : '\nALL OK');
process.exit(failures.length ? 1 : 0);

// Fork addition (plan 48, review 7): checks "Mark resolved" on the details of
// a notification (the owner's way out for a problem Cosmos can no longer
// follow) in headless Chromium, Firefox and WebKit, at 1366x900 and 393x851,
// against the throwaway instance of scripts/fork-telegram-messages-e2e.sh and
// its stand-in for Telegram. Never point it at the production instance, and
// never at the real Telegram.
//
//   sh scripts/fork-telegram-messages-e2e.sh up      (and events, so that problems are open)
//   docker run --rm --network cosmos-tgmsg-net --ipc=host -v "$PWD/scripts:/work" -v <node_modules with playwright>:/work/node_modules \
//     -v <shots>:/shots -e FAKE=http://<SUBNET>.20:8080 -w /work mcr.microsoft.com/playwright:v1.65.1-noble node fork-ui-check-mark-resolved.mjs
//
// BASE (http://cosmos.example.org:2052), FAKE (the stand-in's control port),
// ENGINES (chromium,firefox,webkit), SIZES (d,m), SHOTS (/shots).
// In every engine and size: the open problem's details show the help and
// the button, nothing scrolls sideways, the route refuses GET and a body
// of two ids. In the last engine and size only, the button is pressed: the
// notification shows as resolved (and the chat then gets "No longer
// followed" for a problem of a declared pair no watch knows), or, when a
// watch still sees the problem, the panel says that it stays open.
import { chromium, firefox, webkit } from 'playwright';

const base = process.env.BASE || 'http://cosmos.example.org:2052';
const fake = process.env.FAKE || 'http://192.168.1.20:8080';
const shots = process.env.SHOTS || '/shots';
const engines = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const sizes = (process.env.SIZES || 'd,m').split(',');
const types = { chromium, firefox, webkit };
const viewports = { d: { width: 1366, height: 900 }, m: { width: 393, height: 851 } };
const problems = [];
const note = (...a) => console.log(...a);
const expect = (ok, text) => { if (ok) note('ok:', text); else { problems.push(text); console.log('PROBLEM:', text); } };
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const lastRun = `${engines[engines.length - 1]}-${sizes[sizes.length - 1]}`;

const noSideways = (page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1);

const check = async (engine, size) => {
  const label = `${engine}-${size}`;
  const browser = await types[engine].launch();
  const ctx = await browser.newContext({ viewport: viewports[size], hasTouch: size === 'm' });
  await ctx.route('**/sw.js', (route) => route.abort());
  const page = await ctx.newPage();
  page.on('pageerror', (e) => expect(false, `${label} script error: ${e.message.slice(0, 300)}`));
  let r = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: 'Test-Passw0rd!x' } });
  expect(r.ok(), `${label} login`);
  r = await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: 'Test-Passw0rd!x' } });

  // an open problem (error or warning, not resolved)
  const list = await (await ctx.request.get(base + '/cosmos/api/notifications/list?limit=100')).json();
  const items = (list.data && list.data.items) || [];
  const open = items.find((it) => !it.resolved && !it.legacy && (it.level === 'error' || it.level === 'warning'));
  note(`${label} the problem: #${open && open.id} ${open && open.kind}`);
  expect(!!open, `${label} an open problem is listed (${items.length} notifications)`);
  if (!open) { await browser.close(); return; }

  // the route: POST only, one id
  expect((await ctx.request.get(base + '/cosmos/api/notifications/resolve')).status() === 405, `${label} the route refuses GET`);
  r = await ctx.request.post(base + '/cosmos/api/notifications/resolve', { data: { ids: [open.id, open.id + 1] } });
  expect(r.status() === 400, `${label} the route refuses two ids (${r.status()})`);
  r = await ctx.request.post(base + '/cosmos/api/notifications/resolve', { data: { ids: [open.id], extra: 1 } });
  expect(r.status() === 400, `${label} the route refuses an unknown field (${r.status()})`);

  await page.goto(base + '/cosmos-ui/notifications/' + open.id, { waitUntil: 'domcontentloaded' });
  await page.locator('[data-notification-details]').waitFor({ timeout: 15000 }).catch(() => {});
  const button = page.getByRole('button', { name: 'Mark resolved' });
  expect(await button.count() === 1, `${label} the details offer "Mark resolved"`);
  expect(await page.getByText('can no longer tell').count() > 0, `${label} its help says when to use it`);
  expect(await noSideways(page), `${label} the details do not scroll sideways`);
  const box = await button.boundingBox().catch(() => null);
  expect(box && box.height >= (size === 'm' ? 43 : 30), `${label} the button is large enough to tap (${box && Math.round(box.height)} px)`);
  await page.screenshot({ path: `${shots}/${label}-mark-resolved.png`, fullPage: true });

  if (label === lastRun) {
    const before = await (await ctx.request.get(fake + '/messages')).json().catch(() => []);
    await button.click();
    await page.getByText(/^Resolved /).first().waitFor({ timeout: 10000 }).catch(() => {});
    const after = await (await ctx.request.get(base + '/cosmos/api/notifications/item?id=' + open.id)).json();
    await sleep(1500);
    const still = await page.getByText('A watch still sees this problem').count() > 0;
    expect((after.data && after.data.resolved) || still, `${label} pressed: resolved (${!!(after.data && after.data.resolved)}) or said to stay open (${still})`);
    expect(await button.count() === 0 || still, `${label} the button is gone once resolved`);
    await page.screenshot({ path: `${shots}/${label}-resolved.png`, fullPage: true });
    if (after.data && after.data.resolved) {
      // the chat: "No longer followed" within a reconcile period, unless a watch still sees it
      let told = false;
      for (let i = 0; i < 80 && !told; i++) {
        const msgs = await (await ctx.request.get(fake + '/messages')).json().catch(() => []);
        told = Array.isArray(msgs) && msgs.length > before.length && msgs.slice(before.length).some((m) => JSON.stringify(m).includes('No longer followed'));
        if (!told) await sleep(5000);
      }
      note(`${label} the chat said "No longer followed": ${told}`);
    }
  }
  await browser.close();
};

for (const engine of engines) {
  for (const size of sizes) {
    await check(engine, size).catch((e) => expect(false, `${engine}-${size} failed: ${e.message.slice(0, 300)}`));
  }
}
console.log(problems.length ? `${problems.length} PROBLEMS` : 'all checks passed');
process.exit(problems.length ? 1 : 0);

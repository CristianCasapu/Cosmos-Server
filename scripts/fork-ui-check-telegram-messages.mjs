// Fork addition (plan 48): checks the "Telegram messages" card (every kind of
// message by severity, its preview, a switch, "Send me this sample") in
// headless Chromium, Firefox and WebKit, at 1366x900 and 393x851, against
// the throwaway instance of scripts/fork-telegram-messages-e2e.sh and its
// stand-in for Telegram. Never point it at the production instance, and
// never at the real Telegram.
//
//   sh scripts/fork-telegram-messages-e2e.sh up
//   docker run --rm --network cosmos-tgmsg-net --ipc=host -v "$PWD/scripts:/work" -v <node_modules with playwright>:/work/node_modules \
//     -v <shots>:/shots -e FAKE=http://<SUBNET>.20:8080 -w /work mcr.microsoft.com/playwright:v1.65.1-noble node fork-ui-check-telegram-messages.mjs
//
// BASE (http://cosmos.example.org:2052), FAKE (the stand-in's control
// port), ENGINES (chromium,firefox,webkit), SIZES (d,m), SHOTS (/shots).
// The switch is changed and saved once, in the first engine and size, and put back.
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
const problem = (text) => { problems.push(text); console.log('PROBLEM:', text); };
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
let switched = false;

const messagesAtFake = async (ctx) => (await (await ctx.request.get(fake + '/messages')).json());

const check = async (engine, size) => {
  const name = `${engine}-${size}`;
  const browser = await types[engine].launch();
  const ctx = await browser.newContext({ viewport: viewports[size], hasTouch: size === 'm' });
  await ctx.route('**/sw.js', (route) => route.abort()); // no service worker between the page and the server
  const page = await ctx.newPage();
  page.on('pageerror', (e) => problem(`${name} script error: ${e.message.slice(0, 300)}`));

  let r = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: 'Test-Passw0rd!x' } });
  const login = r.status();
  r = await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: 'Test-Passw0rd!x' } });
  note(name, 'login', login, 'sudo', r.status());
  const kinds = (await (await ctx.request.get(base + '/cosmos/api/telegram/kinds')).json()).data.kinds;

  await page.goto(base + '/cosmos-ui/config-general', { waitUntil: 'domcontentloaded' });
  await page.getByText('Telegram messages', { exact: true }).first().waitFor({ timeout: 30000 });
  const card = page.locator('.MuiCard-root', { has: page.locator('.telegram-severity') }).last();
  await page.locator('.telegram-kind').first().waitFor({ timeout: 20000 });
  await card.scrollIntoViewIfNeeded();
  await sleep(500);

  // the severities in order, each with its kinds, each kind's preview with its mark
  const order = await card.locator('.telegram-severity').evaluateAll((els) => els.map((el) => el.dataset.severity));
  if (order.join(',') !== 'critical,error,warn,info') problem(`${name}: severities in the order ${order}`);
  const marks = { critical: '🔴 Critical · ', error: '🟠 Error · ', warn: '🟡 Warning · ', info: '🔵 Info · ' };
  for (const severity of order) {
    const rows = card.locator(`.telegram-severity[data-severity=${severity}] .telegram-kind`);
    const count = await rows.count();
    const previews = await rows.locator('.telegram-preview').allInnerTexts();
    const wrong = previews.filter((p) => !p.startsWith(marks[severity]) && !p.startsWith('🟢 OK · '));
    note(name, severity, count, 'kinds; first preview:', previews[0]);
    if (count === 0) problem(`${name}: no kind under ${severity}`);
    if (wrong.length) problem(`${name}: previews under ${severity} with another mark: ${wrong.slice(0, 3).join(' | ')}`);
  }
  const shownPreviews = await card.locator('.telegram-kind').evaluateAll((els) => els.map((el) => [el.dataset.kind, el.querySelector('.telegram-preview').getAttribute('title')]));
  for (const [kind, preview] of shownPreviews) {
    const server = kinds.find((k) => k.kind === kind);
    if (!server || server.preview !== preview) problem(`${name}: the preview of ${kind} is not the server's`);
  }
  if (shownPreviews.length !== kinds.length) problem(`${name}: ${shownPreviews.length} kinds shown, the server has ${kinds.length}`);

  // plan 48: the watches are in the card
  const watches = await card.locator('#telegram-watches').count();
  if (!watches || !(await card.innerText()).includes('Disks that report they are failing')) problem(`${name}: the watches' section is missing`);

  // the whole message, as it arrives
  const dying = card.locator('.telegram-kind[data-kind="container.dying"]');
  await dying.getByRole('button', { name: 'Show the message' }).click();
  await sleep(400);
  const shown = await dying.locator('.MuiCollapse-root').innerText();
  const server = kinds.find((k) => k.kind === 'container.dying');
  if (!shown.includes(server.text.split('\n')[2]) || !shown.includes('Open in Cosmos')) problem(`${name}: the message shown is not the server's: ${shown.slice(0, 200)}`);
  await dying.screenshot({ path: `${shots}/${name}-message.png` });

  // nothing wider than the screen; buttons and switches of 44 px at least on a phone
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  if (overflow > 1) problem(`${name}: the page scrolls sideways by ${overflow}px`);
  const cut = await card.evaluate((el) => {
    const limit = el.getBoundingClientRect().right + 1;
    return [...el.querySelectorAll('*')].filter((c) => c.getBoundingClientRect().width > 0 && c.getBoundingClientRect().right > limit).length;
  });
  if (cut) problem(`${name}: ${cut} elements reach past the card`);
  if (size === 'm') {
    const small = await card.evaluate((el) => [...el.querySelectorAll('button, .MuiFormControlLabel-root, [role=combobox]')]
      .map((b) => [b.innerText.trim().slice(0, 30) || b.getAttribute('aria-label'), Math.round(b.getBoundingClientRect().height)])
      .filter(([, h]) => h > 0 && h < 44));
    if (small.length) problem(`${name}: touch targets under 44 px: ${JSON.stringify(small.slice(0, 5))}`);
  }
  note(name, 'overflow', overflow, 'cut', cut);
  await card.screenshot({ path: `${shots}/${name}-card.png` });

  // a switch changes what is sent: once, saved, read back, put back
  if (!switched) {
    switched = true;
    const row = card.locator('.telegram-kind[data-kind="auth.login.failed"]');
    const toggle = row.locator('input[type=checkbox]');
    const before = await toggle.isChecked();
    await toggle.click();
    await sleep(300);
    const caption = await row.innerText();
    if (await toggle.isChecked() === before) problem(`${name}: the switch did not change`);
    if (before && !caption.includes('Not sent')) problem(`${name}: no word on why it is not sent: ${caption}`);
    await page.getByRole('button', { name: 'Save', exact: true }).last().click();
    await sleep(4000);
    let saved = (await (await ctx.request.get(base + '/cosmos/api/config')).json()).data.Telegram;
    note(name, 'saved Kinds after the switch:', JSON.stringify(saved.Kinds));
    if (((saved.Kinds || {})['auth.login.failed'] === true) === before) problem(`${name}: the switch was not saved: ${JSON.stringify(saved.Kinds)}`);
    // and a failed sign-in is not sent while it is off (from this browser's own context, which is not the admin's session)
    const sent = (await messagesAtFake(ctx)).length;
    await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'nobody', password: 'wrong' } });
    await sleep(3000);
    const after = await messagesAtFake(ctx);
    if (after.length !== sent && after.slice(sent).some((m) => m.preview.includes('Failed sign-in'))) problem(`${name}: a failed sign-in was sent although switched off`);
    // plan 48: the watches' settings: a number of days saved and read back
    const days = page.locator('input[name=Watch_FillDays]');
    await days.scrollIntoViewIfNeeded();
    await days.fill('5');
    await page.getByRole('button', { name: 'Save', exact: true }).last().click();
    await sleep(4000);
    const watchSaved = (await (await ctx.request.get(base + '/cosmos/api/config')).json()).data.Watch || {};
    note(name, 'saved Watch:', JSON.stringify(watchSaved));
    if (watchSaved.FillDays !== 5) problem(`${name}: the days of a disk filling fast were not saved: ${JSON.stringify(watchSaved)}`);
    // put it back
    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.locator('input[name=Watch_FillDays]').waitFor({ timeout: 30000 });
    await page.locator('input[name=Watch_FillDays]').fill('3');
    await page.locator('.telegram-kind[data-kind="auth.login.failed"]').waitFor({ timeout: 30000 });
    await page.locator('.telegram-kind[data-kind="auth.login.failed"] input[type=checkbox]').click();
    await page.getByRole('button', { name: 'Save', exact: true }).last().click();
    await sleep(4000);
    // put back means sent again: the switch may hold nothing when that is its default (the minimum severity)
    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.locator('.telegram-kind[data-kind="auth.login.failed"]').waitFor({ timeout: 30000 });
    if (await page.locator('.telegram-kind[data-kind="auth.login.failed"] input[type=checkbox]').isChecked() !== before) problem(`${name}: the switch was not put back`);
    await page.locator('.telegram-kind').first().waitFor({ timeout: 30000 });
  }

  // "Send me this sample" arrives with exactly the text shown, silently for info
  for (const kind of size === 'd' ? ['container.dying', 'cert.renewed'] : ['reach.down']) {
    const before = (await messagesAtFake(ctx)).length;
    const row = page.locator(`.telegram-kind[data-kind="${kind}"]`);
    await row.scrollIntoViewIfNeeded();
    await row.getByRole('button', { name: 'Send me this sample' }).click();
    await row.locator('.MuiAlert-root').waitFor({ timeout: 15000 });
    const alert = await row.locator('.MuiAlert-root').innerText();
    const got = (await messagesAtFake(ctx)).slice(before);
    const want = kinds.find((k) => k.kind === kind);
    note(name, kind, 'alert:', alert.split('\n')[0], '| arrived:', got.length, got[0] && got[0].preview, '| silent', got[0] && got[0].silent);
    if (got.length !== 1) { problem(`${name}: ${got.length} messages arrived for the sample of ${kind}`); continue; }
    if (got[0].text !== want.text) problem(`${name}: the sample of ${kind} differs from the preview:\n${got[0].text}\n---\n${want.text}`);
    const same = (list) => JSON.stringify((list || []).map((e) => [e.type, e.offset, e.length]));
    if (same(got[0].entities) !== same(want.entities)) problem(`${name}: the entities of ${kind} differ`);
    if (got[0].parse_mode) problem(`${name}: a parse mode was sent: ${got[0].parse_mode}`);
    if ((got[0].buttons || [])[0]?.url !== want.button?.url) problem(`${name}: the button of ${kind} differs`);
    if (got[0].silent !== (want.severity === 'info')) problem(`${name}: ${kind} arrived silent=${got[0].silent}`);
    await sleep(3200); // the server allows one every three seconds
  }
  await browser.close();
};

for (const engine of engines) {
  for (const size of sizes) {
    try {
      await check(engine, size);
    } catch (e) {
      problem(`${engine}-${size}: ${e.message.split('\n')[0]}`);
    }
  }
}
console.log(problems.length ? `${problems.length} problems` : 'no problems');
process.exit(problems.length ? 1 : 0);

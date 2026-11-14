// Fork addition: checks the notices about containers left behind by an
// interrupted change (components/leftovers.jsx in the page's notice area,
// components/servappsNotices.jsx; src/docker/fork_leftovers.go)
// in headless Chromium, Firefox and WebKit at 1366x900 and 390x844, against a
// throwaway Cosmos. Never the production instance.
//
// What it needs, on one Docker network of its own:
// - a docker:27-dind (DOCKER_HOST of the test Cosmos) holding busybox:1.36 and
//   these containers, each "sleep 36000"; a leftover is made by creating the
//   container, starting and stopping it, and renaming it
//   "<app>-cosmos-old-<first 12 hex of its ID>":
//     jellyfin (running) and a leftover of jellyfin,
//     a leftover of sonarr (no sonarr container),
//     a-very-long-application-name-for-the-phone-width-check (running) and a
//       leftover of it,
//     plainapp (running);
// - the test Cosmos built from the worktree (server binary, static/ from
//   `npx vite build --base=/cosmos-ui/`, a meta.json), fresh, run in a loop
//   (`while true; do ./cosmos; sleep 1; done`) with COSMOS_HTTP_PORT=2052 and a
//   dotted network alias (WebKit keeps no cookie for a name without a dot);
//   BASE is its address (default http://cosmos-l.test:2052).
// It finishes the set-up wizard itself. The display is checked in every
// engine and size first; then, in Chromium, the jellyfin leftover is put back
// (the running jellyfin is set aside and listed as a leftover in turn) and the
// sonarr leftover is removed through its confirmation dialog.
// Run from the Playwright image on the same network:
//   docker run --rm --network <net> --ipc=host -v <dir with this script>:/work -w /work \
//     mcr.microsoft.com/playwright:v1.49.1-noble sh -c 'npm i playwright@1.49.1 && node fork-ui-check-leftovers.mjs'
// Screenshots go to ./shots.
import { chromium, firefox, webkit, request } from 'playwright';
import { mkdirSync } from 'node:fs';

const base = process.env.BASE || 'http://cosmos-l.test:2052';
const password = 'Test-Passw0rd!x';
const engines = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const sizes = (process.env.SIZES || 'd,m').split(',');
const viewports = { d: { width: 1366, height: 900 }, m: { width: 390, height: 844 } };

const note = (...a) => console.log(...a);
const failures = [];
const expect = (ok, what) => { note(ok ? '  ok  ' : '  FAIL', what); if (!ok) failures.push(what); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
mkdirSync('shots', { recursive: true });

const api = await request.newContext();
const until = async (what, check, seconds = 90) => {
  for (let i = 0; i < seconds; i++) {
    try { if (await check()) return true; } catch (e) { /* restarting */ }
    await sleep(1000);
  }
  expect(false, 'timed out: ' + what);
  return false;
};

// --- the wizard, once
{
  let fresh = false;
  try {
    const answer = await api.get(base + '/cosmos/api/status', { timeout: 5000, maxRedirects: 0 });
    fresh = answer.status() === 200 && (await answer.json()).status === 'OK';
  } catch (e) { /* set up already */ }
  if (fresh) {
    const step = (data) => api.post(base + '/cosmos/api/newInstall', { data }).then((r) => r.status()).catch(() => 'exit');
    note('wizard:', await step({ step: '2', mongodbMode: 'Create' }),
      await step({ step: '3', httpsCertificateMode: 'DISABLED', hostname: new URL(base).hostname }),
      await step({ step: '4', nickname: 'admin', password, email: 'admin@example.org' }), await step({ step: '5' }));
    await sleep(4000);
  }
  await until('the server answers', async () => (await api.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } })).status() === 200);
}

// --- the route itself, without a browser
{
  const ctx = await request.newContext();
  await ctx.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  let answer = await ctx.get(base + '/cosmos/api/servapps-leftovers');
  const listed = (await answer.json()).data || [];
  note('listed:', JSON.stringify(listed.map((l) => [l.name, l.appExists, l.appRunning, l.finished])));
  expect(answer.status() === 200 && listed.length === 3, 'GET lists the three leftovers');
  answer = await ctx.post(base + '/cosmos/api/servapps-leftovers', { data: { action: 'remove', name: listed[0] && listed[0].name } });
  expect(answer.status() === 401, `POST without the password confirmed is refused (${answer.status()})`);
  await ctx.post(base + '/cosmos/api/sudo', { data: { password } });
  answer = await ctx.post(base + '/cosmos/api/servapps-leftovers', { data: { action: 'remove', name: 'plainapp' } });
  expect(answer.status() === 404 && (await answer.json()).code === 'LEFT002', 'a container that is not a leftover is refused');
  answer = await ctx.post(base + '/cosmos/api/servapps-leftovers', { data: { action: 'remove', name: 'x', extra: 1 } });
  expect(answer.status() === 400, 'an unknown field is refused');
  answer = await ctx.put(base + '/cosmos/api/servapps-leftovers', { data: {} });
  expect(answer.status() === 405, 'PUT is not served');
  await ctx.dispose();
}

const open = async (type, name, viewport) => {
  const browser = await type.launch();
  const ctx = await browser.newContext({ viewport });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => failures.push(`${name} PAGEERROR: ${e.message.slice(0, 300)}`));
  const a = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  const b = await ctx.request.post(base + '/cosmos/api/sudo', { data: { password } });
  note(`\n== ${name}: login ${a.status()}/${b.status()}`);
  return { browser, ctx, page };
};
const noSideScroll = async (page, name, where) => {
  const over = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(over <= 1, `${name}: no sideways scroll on ${where} (${over}px over)`);
};
const notices = (page) => page.locator('[data-leftover]');
const servApps = async (page) => {
  await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
  await page.locator('[data-leftover]').first().waitFor({ timeout: 30000 }).catch(() => {});
  await page.waitForTimeout(1500);
  // the page's notice area shows two lines and folds the rest (components/noticeArea.jsx): open it
  const more = page.locator('[data-notices] button[aria-expanded=false]');
  if (await more.count()) { await more.first().click(); await page.waitForTimeout(300); }
};

// --- what is shown, everywhere
for (const engine of engines) {
  const type = { chromium, firefox, webkit }[engine];
  for (const size of sizes) {
    const name = `${engine}-${size}`;
    const { browser, page } = await open(type, name, viewports[size]);
    await servApps(page);
    const count = await notices(page).count();
    expect(count === 3, `${name}: three notices (${count})`);
    const jellyfin = notices(page).filter({ hasText: 'jellyfin-cosmos-old-' });
    const text = await jellyfin.innerText().catch(() => '');
    expect(/The change of jellyfin made on .+ was interrupted: the previous container is kept, stopped, as jellyfin-cosmos-old-[0-9a-f]{12}\./.test(text), `${name}: says what happened`);
    expect(text.includes('jellyfin runs now under its name.'), `${name}: says the app runs now`);
    expect(text.includes('the current jellyfin is stopped and kept aside, not deleted'), `${name}: says the current one is kept aside`);
    const sonarr = await notices(page).filter({ hasText: 'sonarr-cosmos-old-' }).innerText().catch(() => '');
    expect(sonarr.includes('No container is named sonarr now') && !sonarr.includes('the current sonarr'), `${name}: an app without a container is said so`);
    expect(await jellyfin.getByRole('button', { name: 'Put the previous one back' }).isVisible(), `${name}: "Put the previous one back"`);
    expect(await jellyfin.getByRole('button', { name: 'Remove the leftover' }).isVisible(), `${name}: "Remove the leftover"`);
    // nothing of the notices outside the screen
    const width = page.viewportSize().width;
    const boxes = await notices(page).evaluateAll((all) => all.map((el) => {
      const r = el.getBoundingClientRect();
      const inner = [...el.querySelectorAll('*')].some((child) => child.getBoundingClientRect().right > r.right + 1);
      return { left: r.left, right: r.right, inner };
    }));
    expect(boxes.every((b) => b.left >= 0 && b.right <= width + 1 && !b.inner), `${name}: the notices fit the screen ${JSON.stringify(boxes)}`);
    await noSideScroll(page, name, 'ServApps');
    await notices(page).first().scrollIntoViewIfNeeded();
    await page.screenshot({ path: `shots/leftovers-${name}.png` });

    // the confirmation of a removal
    await notices(page).filter({ hasText: 'sonarr-cosmos-old-' }).getByRole('button', { name: 'Remove the leftover' }).click();
    const dialog = page.locator('[role=dialog]');
    await dialog.waitFor({ timeout: 5000 }).catch(() => {});
    const asked = await dialog.innerText().catch(() => '');
    expect(/Remove sonarr-cosmos-old-[0-9a-f]{12}\? The container is deleted for good; its volumes are kept\./.test(asked), `${name}: the removal asks first`);
    const box = await page.locator('.MuiDialog-paper').last().boundingBox();
    expect(!!box && box.x >= 0 && box.x + box.width <= width + 1, `${name}: the dialog fits the screen`);
    await page.waitForTimeout(500); // the dialog fades in
    await page.screenshot({ path: `shots/leftovers-${name}-confirm.png` });
    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await page.waitForTimeout(600);
    expect(await notices(page).count() === 3, `${name}: cancel removes nothing`);
    await browser.close();
  }
}

// --- the actions, in Chromium
{
  const name = 'chromium-actions';
  const { browser, page } = await open(chromium, name, viewports.d);
  await servApps(page);
  const jellyfin = notices(page).filter({ hasText: 'jellyfin-cosmos-old-' });
  const leftoverName = await jellyfin.getAttribute('data-leftover');
  await jellyfin.getByRole('button', { name: 'Put the previous one back' }).click();
  // busybox's sleep ignores the stop signal: setting the running jellyfin aside takes Docker's 10 s
  await page.getByText(/Put back the previous jellyfin: (done|failed)/).first().waitFor({ timeout: 40000 }).catch(() => {});
  await page.waitForTimeout(1500);
  const toasts = (await page.locator('[role=status], [role=alert]').allInnerTexts()).join(' || ').replace(/\n/g, ' ');
  note('after the restore:', toasts.slice(0, 600));
  expect(toasts.includes('Put back the previous jellyfin: done'), `${name}: the toast names the command`);
  expect(/jellyfin is back\. The container that had its name is kept, stopped, as jellyfin-cosmos-old-[0-9a-f]{12}\./.test(toasts), `${name}: the result is said`);
  const now = await notices(page).evaluateAll((all) => all.map((el) => el.getAttribute('data-leftover')));
  expect(now.length === 3 && !now.includes(leftoverName) && now.some((n) => n.startsWith('jellyfin-cosmos-old-')), `${name}: the replaced jellyfin is a leftover now (${now.join(', ')})`);
  await page.screenshot({ path: `shots/leftovers-${name}-restored.png` });

  await notices(page).filter({ hasText: 'sonarr-cosmos-old-' }).getByRole('button', { name: 'Remove the leftover' }).click();
  await page.locator('[role=dialog]').getByRole('button', { name: 'Remove it' }).click();
  await page.getByText(/Remove the leftover sonarr-cosmos-old-[0-9a-f]{12}: (done|failed)/).first().waitFor({ timeout: 20000 }).catch(() => {});
  await page.waitForTimeout(1500);
  const after = await notices(page).evaluateAll((all) => all.map((el) => el.getAttribute('data-leftover')));
  expect(after.length === 2 && !after.some((n) => n.startsWith('sonarr-')), `${name}: the sonarr leftover is removed (${after.join(', ')})`);
  const said = (await page.locator('[role=status], [role=alert]').allInnerTexts()).join(' || ').replace(/\n/g, ' ');
  expect(said.includes('Remove the leftover sonarr-cosmos-old-'), `${name}: the toast names the removal`);
  await page.screenshot({ path: `shots/leftovers-${name}-removed.png` });
  await browser.close();
}

note(failures.length ? `\n${failures.length} FAILED:\n` + failures.join('\n') : '\nall checks passed');
process.exit(failures.length ? 1 : 0);

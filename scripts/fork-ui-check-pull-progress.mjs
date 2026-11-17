// Fork addition: checks the image pull of a creation as a step of its own
// (src/docker/fork_pull_progress.go, client/src/components/forkPullProgress.jsx)
// in headless Chromium, Firefox and WebKit at 1366x900 and 393x851, against a
// throwaway Cosmos with a Docker of its own. Never the production instance.
//
// What it needs, on one Docker network made with --internal (no way out, so a
// registry name outside it does not resolve):
// - a docker:29-dind at DIND (API on 2375, the DOCKER_HOST of the test Cosmos)
//   started with --insecure-registry <slow line host:port>, running a registry
//   (registry:3 on 5000) that holds fork/bigapp:1: a few tens of MB of random
//   layers on busybox, e.g. three "RUN head -c 30000000 /dev/urandom > /x";
// - scripts/fork-fake-slow-registry.go in front of that registry at SLOW
//   (e.g. "go run scripts/fork-fake-slow-registry.go :5001 <dind>:5000 3000000"),
//   so that the pull lasts long enough to be seen;
// - the test Cosmos at SERVER_IP, fresh, run in a loop
//   (while :; do ./cosmos; sleep 1; done), COSMOS_HTTP_PORT=2052,
//   COSMOS_HTTPS_PORT=2053, DOCKER_HOST=tcp://<dind>:2375.
// It finishes the wizard (self-signed HTTPS on SERVER_IP: an address with dots,
// which WebKit needs to keep the session cookie). Run it from the Playwright
// image on the same network with playwright 1.65.1 next to it (e.g. the
// node_modules of scripts/fork-ci-smoke). Screenshots go to ./shots.
// ENGINES=chromium,firefox,webkit SIZES=d,m as in the other checks.
//
// Per engine and size: a slow pull shows its bytes and layers while it runs,
// then "downloaded"; a registry whose name does not resolve says so in words
// with Docker's technical line and "Try again", which runs the creation again;
// a tag that does not exist says so (desktop only). No sideways scroll, the
// button at least 44 px high.
import { chromium, firefox, webkit, request } from 'playwright';
import { mkdirSync } from 'node:fs';

const ip = process.env.SERVER_IP || '192.168.231.10';
const dind = process.env.DIND || 'http://cosmos-pgfix-g-dind:2375';
const slow = process.env.SLOW || 'cosmos-pgfix-g-slow:5001';
const plain = `http://${ip}:2052`;
const base = `https://${ip}:2053`;
const password = 'Test-Passw0rd!x';
const engines = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const sizes = (process.env.SIZES || 'd,m').split(',');
const goodImage = `${slow}/fork/bigapp:1`;
const missingImage = `${slow}/fork/bigapp:nope`;
const noDnsImage = 'registry.example.org/app:1';

const note = (...a) => console.log(...a);
const failures = [];
const expect = (ok, what) => { note(ok ? '  ok  ' : '  FAIL', what); if (!ok) failures.push(what); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const api = await request.newContext({ ignoreHTTPSErrors: true });
mkdirSync('shots', { recursive: true });

const until = async (what, check, seconds = 90) => {
  for (let i = 0; i < seconds; i++) {
    try { if (await check()) return true; } catch (e) { /* reloading */ }
    await sleep(1000);
  }
  expect(false, 'timed out: ' + what);
  return false;
};

// the wizard, once
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
  await until('the server answers over HTTPS', async () => (await api.get(base + '/cosmos/api/status')).status() < 500);
}

// the test Docker: the container and the image of an earlier round away, so that the pull is a real one
const forget = async (name) => {
  await api.delete(`${dind}/containers/${name}?force=1&v=1`).catch(() => {});
  await api.delete(`${dind}/images/${encodeURIComponent(goodImage)}?force=1`).catch(() => {});
};

const noSideScroll = async (page, name, where) => {
  const over = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(over <= 1, `${name}: no sideways scroll on ${where} (${over}px over)`);
};
const shot = (page, name, what) => page.screenshot({ path: `shots/${name}-${what}.png`, fullPage: true });

// the new-container form, its quick review, and Create
const createFrom = async (page, image, container) => {
  await page.goto(base + '/cosmos-ui/servapps/new-service', { waitUntil: 'domcontentloaded' });
  await page.locator('#image').waitFor({ timeout: 45000 });
  await page.locator('#image').fill(image);
  await page.locator('#name').fill(container);
  await page.locator('#image').blur();
  await page.locator('[data-quick-review]').click();
  await page.locator('[data-creation-review]').waitFor({ timeout: 30000 });
  await page.waitForTimeout(1500);
  const create = page.getByRole('button', { name: 'Create' });
  await create.waitFor({ timeout: 20000 });
  if (await create.isDisabled()) {
    const hold = await page.locator('[data-create-hold]').innerText().catch(() => '');
    expect(false, `the Create button is held: ${hold.slice(0, 200)}`);
    return false;
  }
  await create.click();
  return true;
};

for (const engine of engines) {
  for (const size of sizes) {
    const viewport = size === 'm' ? { width: 393, height: 851 } : { width: 1366, height: 900 };
    const name = `${engine}-${size === 'm' ? 393 : 1366}`;
    const browser = await { chromium, firefox, webkit }[engine].launch();
    const ctx = await browser.newContext({ viewport, ignoreHTTPSErrors: true, serviceWorkers: 'block' });
    const page = await ctx.newPage();
    page.on('pageerror', (e) => failures.push(`${name} PAGEERROR: ${(e.stack || e.message).slice(0, 600)}`));
    const a = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
    const b = await ctx.request.post(base + '/cosmos/api/sudo', { data: { password } });
    note(`\n== ${name}: login ${a.status()}/${b.status()}`);
    const container = `pull-${engine}-${size}`;
    try {
      // 1. a slow pull: the bytes and layers while it runs, then "downloaded"
      await forget(container);
      if (await createFrom(page, goodImage, container)) {
        const step = page.locator('[data-pull-state=pulling]');
        await step.waitFor({ timeout: 30000 });
        const moving = await until(`${name}: the bytes are shown`, async () => /MB of \d+(\.\d)? MB/.test(await step.innerText()), 40);
        const text = moving ? await step.innerText() : '';
        note('  pulling:', text.replace(/\s+/g, ' '));
        expect(/Downloading .*fork\/bigapp:1: .* layers ready \(\d+ s\)/.test(text), `${name}: the pull says its layers and time`);
        const bar = step.locator('[role=progressbar]');
        expect(await bar.count() === 1 && (await bar.getAttribute('aria-valuenow')) !== null, `${name}: the bar shows the share downloaded`);
        expect(!/PULL PROGRESS/.test(await page.locator('pre').first().innerText()), `${name}: the marker lines stay out of the log`);
        await shot(page, name, 'pulling');
        await noSideScroll(page, name, 'the pull');
        await page.locator('[data-pull-state=done]').waitFor({ timeout: 120000 });
        const done = await page.locator('[data-pull-state=done]').innerText();
        expect(/fork\/bigapp:1 downloaded \(\d+ (s|min)/.test(done), `${name}: the pull ends as downloaded (${done.replace(/\s+/g, ' ')})`);
        await page.getByText('Service created').or(page.locator('[data-create-failed]')).first().waitFor({ timeout: 60000 }).catch(() => {});
        expect(await page.locator('[data-create-failed]').count() === 0, `${name}: the creation went on after the pull`);
        await shot(page, name, 'pulled');
      }
      await forget(container);

      // 2. a registry whose name does not resolve: the reason in words, Docker's line, "Try again"
      if (await createFrom(page, noDnsImage, container + '-dns')) {
        const failed = page.locator('[data-pull-state=failed]');
        await failed.waitFor({ timeout: 60000 });
        const words = await failed.innerText();
        note('  failed:', words.replace(/\s+/g, ' '));
        expect(/registry\.example\.org\/app:1 could not be downloaded/.test(words), `${name}: the failure names the image`);
        expect(/could not look up the registry's name \(DNS\)/.test(words), `${name}: the failure says the name lookup failed`);
        expect(/Docker said: .*registry\.example\.org/.test(words), `${name}: Docker's technical line is shown`);
        const retry = page.locator('[data-pull-retry]');
        await retry.waitFor({ timeout: 20000 });
        const box = await retry.boundingBox();
        expect(!!box && box.height >= 44 && box.width >= 44, `${name}: "Try again" is a 44 px target (${box && Math.round(box.height)})`);
        await noSideScroll(page, name, 'the failed pull');
        await shot(page, name, 'failed-dns');
        // Try again runs the creation again: the step starts over and fails again the same way
        let sent = 0;
        page.on('request', (r) => { if (r.method() === 'POST' && r.url().includes('/cosmos/api/docker-service')) sent += 1; });
        await retry.click();
        await until(`${name}: "Try again" sends the creation again`, async () => sent === 1, 20);
        await failed.waitFor({ timeout: 60000 });
        expect(/\(DNS\)/.test(await failed.innerText()), `${name}: after "Try again" the same reason`);
      }

      // 3. a tag that does not exist (desktop only: the same component)
      if (size === 'd' && await createFrom(page, missingImage, container + '-tag')) {
        const failed = page.locator('[data-pull-state=failed]');
        await failed.waitFor({ timeout: 60000 });
        const words = await failed.innerText();
        expect(/no such image or tag/.test(words), `${name}: a missing tag is said (${words.replace(/\s+/g, ' ').slice(0, 160)})`);
        await shot(page, name, 'failed-tag');
      }
    } catch (e) {
      failures.push(`${name}: ${e.message.split('\n')[0]}`);
      await shot(page, name, 'error').catch(() => {});
    }
    await browser.close();
  }
}

await api.dispose();
note(failures.length ? `\n${failures.length} FAILURE(S):\n- ${failures.join('\n- ')}` : '\nall checks passed');
process.exit(failures.length ? 1 : 0);

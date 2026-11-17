// Fork addition (plan 47, I2d/I2e): the browser check of the forms of deliveries I2d and I2e,
// against the throwaway instance of scripts/fork-ui-check-forms-d.sh (its header lists what is in
// it). Never the production instance, never the real Telegram.
//   PHASE=setup      finishes the wizard over the API (the sh script's `setup` runs it)
//   PHASE=wizard     the first step of the wizard on the unlicensed instance: no "join a cluster"
//   PHASE=telegram   from an empty card to a received test message, by choosing the offered chat
//   PHASE=cron       a nightly dump job for maindb made by choices only, its next runs, then run
//                    once on an every-minute schedule: the dump appears in maindb's data folder
//   PHASE=alert      an alert saved, a bad one refused by the server in words
//   PHASE=database   the monitoring database page proposes maindb and tests the connection
//   PHASE=snapshots  excludes offered from /srv/appdata, the destination from the data disk
//   PHASE=all        wizard is left out (it needs a fresh instance): telegram, cron, alert, database, snapshots
// ENGINES=chromium,firefox,webkit  SIZES=d,m (1366x900, 393x851)  BASE, FAKE (the stand-in's
// control port), DIND (the test Docker's API, to look into the "server"). Pictures go to ./shots.
// The run ends with "ALL OK" or the list of failures.
import { chromium, firefox, webkit } from 'playwright';

const base = process.env.BASE || 'http://forms-d.example.org:2052';
const fake = process.env.FAKE || 'http://192.0.2.2:8080';
const dind = process.env.DIND || 'http://192.0.2.5:2375';
const PASSWORD = 'Test-Passw0rd!x';
const engines = { chromium, firefox, webkit };
const names = (process.env.ENGINES || 'chromium').split(',');
const sizes = { d: { width: 1366, height: 900 }, m: { width: 393, height: 851 } };
const wanted = (process.env.SIZES || 'd,m').split(',');
const phase = process.env.PHASE || 'all';
const note = (...a) => console.log(...a);
const failures = [];
const expect = (what, ok, detail) => {
  note(ok ? '  ok  ' : '  FAIL', what, detail === undefined ? '' : '| ' + String(detail).slice(0, 300));
  if (!ok) failures.push(what);
};
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// the "server": a command in the test Docker through its API (exec in a container is not
// possible from here, so the folder is read by a throwaway container with the folder mounted)
const onServer = async (cmd, binds) => {
  const create = await fetch(dind + '/containers/create', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ Image: 'postgres:18', Entrypoint: ['sh', '-c'], Cmd: [cmd], HostConfig: { Binds: binds || [], AutoRemove: false } }),
  }).then((r) => r.json());
  await fetch(`${dind}/containers/${create.Id}/start`, { method: 'POST' });
  await fetch(`${dind}/containers/${create.Id}/wait`, { method: 'POST' });
  const logs = await fetch(`${dind}/containers/${create.Id}/logs?stdout=1&stderr=1`).then((r) => r.arrayBuffer());
  await fetch(`${dind}/containers/${create.Id}?force=1`, { method: 'DELETE' });
  // strip the 8-byte frame headers of the multiplexed stream
  const bytes = new Uint8Array(logs);
  let out = '';
  for (let i = 0; i + 8 <= bytes.length;) {
    const size = (bytes[i + 4] << 24) | (bytes[i + 5] << 16) | (bytes[i + 6] << 8) | bytes[i + 7];
    out += new TextDecoder().decode(bytes.slice(i + 8, i + 8 + size));
    i += 8 + size;
  }
  return out;
};

const login = async (ctx) => {
  await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: PASSWORD } });
  await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: PASSWORD } });
};
const getConfig = async (ctx) => (await (await ctx.request.get(base + '/cosmos/api/config')).json()).data;
const putConfig = async (ctx, config) => {
  const r = await ctx.request.put(base + '/cosmos/api/config', { data: config });
  return { status: r.status(), body: await r.text() };
};
const waitBack = async (ctx) => {
  for (let i = 0; i < 30; i += 1) {
    await sleep(1500);
    const r = await ctx.request.get(base + '/cosmos/api/status').catch(() => null);
    if (r && r.status() < 500) return;
  }
};

// no sideways scroll, and the buttons and chips of `scope` at least 44 px tall on a phone
const layout = async (page, id, scope, size) => {
  const wide = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(id + ': no sideways scroll', wide <= 1, wide);
  if (size !== 'm' || !scope) return;
  const small = await scope.locator('button:visible, [role=radio]:visible, [role=checkbox]:visible, input[type=checkbox]:visible').evaluateAll((els) => els
    .map((el) => { const box = (el.closest('label') || el).getBoundingClientRect(); return { text: (el.innerText || el.getAttribute('aria-label') || el.name || '').trim().slice(0, 40), h: Math.round(box.height), w: Math.round(box.width) }; })
    .filter((b) => b.h > 0 && (b.h < 44 && b.w < 44 || b.h < 32)));
  expect(id + ': touch targets of at least 44 px', small.length === 0, JSON.stringify(small.slice(0, 6)));
};
const shot = (page, id, name) => page.screenshot({ path: `shots/${id}-${name}.png`, fullPage: true }).catch(() => {});

// ---------------------------------------------------------------------------------------------
const phases = {};

phases.setup = async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext();
  const step = (data) => ctx.request.post(base + '/cosmos/api/newInstall', { data }).then((r) => r.status()).catch(() => 'exit');
  note('wizard:', await step({ step: '2', mongodbMode: 'Create' }),
    await step({ step: '3', httpsCertificateMode: 'DISABLED', hostname: 'forms-d.example.org' }),
    await step({ step: '4', nickname: 'admin', password: PASSWORD }), await step({ step: '5' }));
  await browser.close();
};

phases.wizard = async (page, id, size, ctx) => {
  const status = (await (await ctx.request.get(base + '/cosmos/api/status')).json()).data || {};
  expect(id + ': the instance is unlicensed', status.Licence === false, JSON.stringify({ Licence: status.Licence, ShowPaidFeatures: status.ShowPaidFeatures }));
  await page.goto(base + '/cosmos-ui/newInstall', { waitUntil: 'domcontentloaded' });
  await page.getByText(/clean install/i).first().waitFor({ timeout: 20000 });
  await sleep(2500);
  const text = await page.locator('body').innerText();
  expect(id + ': the welcome step is shown', /clean install/i.test(text));
  const upload = await page.locator('input[type=file]').count();
  expect(id + ': no "join a cluster" upload without a licence', upload === 0 && !/constellation/i.test(text), `file inputs ${upload}`);
  await layout(page, id, page.locator('main, body').first(), size);
  await shot(page, id, 'wizard');
};

// ---------------------------------------------------------------------------------------------
const order = phase === 'all' ? ['telegram', 'cron', 'alert', 'database', 'snapshots'] : phase.split(',');
if (order.includes('setup')) {
  await phases.setup();
} else {
  for (const name of names) for (const size of wanted) {
    const id = `${name}-${size}`;
    const browser = await engines[name].launch();
    const ctx = await browser.newContext({ viewport: sizes[size] });
    // the panel's service worker would fetch past what a check looks at
    await ctx.route('**/sw.js', (route) => route.abort());
    if (!order.includes('wizard')) await login(ctx);
    const page = await ctx.newPage();
    page.on('pageerror', (e) => expect(id + ': no script error', false, e.message));
    for (const which of order) {
      note(`--- ${id}: ${which}`);
      try {
        await phases[which](page, id, size, ctx);
      } catch (e) {
        expect(`${id}: ${which} ran to its end`, false, e.message.split('\n')[0]);
        await shot(page, id, which + '-error');
      }
    }
    await browser.close();
  }
}
note(failures.length ? `FAILURES (${failures.length}):\n  ` + failures.join('\n  ') : 'ALL OK');
process.exit(failures.length ? 1 : 0);

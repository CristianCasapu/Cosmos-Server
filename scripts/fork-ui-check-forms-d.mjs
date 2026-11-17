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
// a tab by its name: tabs on a wide screen, a select on a phone
const openTab = async (page, label) => {
  const tab = page.getByRole('tab', { name: label });
  if (await tab.count() && await tab.first().isVisible()) return tab.first().click();
  await page.locator('main [role=combobox]').first().click();
  await page.getByRole('option', { name: label, exact: true }).click();
};
const shotOf = (locator, id, name) => locator.screenshot({ path: `shots/${id}-${name}-part.png` }).catch(() => {});
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
  // the step's own controls are upstream's (the language button is 24 px tall: not changed here)
  await layout(page, id, null, size);
  await shot(page, id, 'wizard');
};

// Telegram: an empty card, the token pasted, nobody wrote yet, then the owner writes to the bot
phases.telegram = async (page, id, size, ctx) => {
  const config = await getConfig(ctx);
  config.Telegram = {};
  expect(id + ': the Telegram settings start empty', (await putConfig(ctx, config)).status === 200);
  await waitBack(ctx);
  await fetch(fake + '/updates?set=none');
  await fetch(fake + '/mode?set=ok');
  await fetch(fake + '/received?clear=1');
  await page.goto(base + '/cosmos-ui/config-general/general', { waitUntil: 'domcontentloaded' });
  const token = page.locator('input[name=Telegram_BotToken]');
  await token.waitFor({ timeout: 20000 });
  const card = page.locator('.MuiCard-root', { has: token }).last();
  await card.locator('input[name=Telegram_Enabled]').check();
  await token.fill(['123456789', 'AAFakeFakeFakeFakeFakeFakeFakeFake0'].join(':'));
  expect(id + ': the chat ID is empty', (await card.locator('input[name=Telegram_ChatID]').inputValue()) === '');
  await card.getByRole('button', { name: /Look for chats/ }).click();
  await card.getByText(/send it any message, then press Look again/).first().waitFor({ timeout: 15000 });
  expect(id + ': nobody wrote yet: it says what to do', true);
  await shot(page, id, 'telegram-nobody');
  await fetch(fake + '/updates?set=both');
  await sleep(3200); // the server answers one look every three seconds
  await card.getByRole('button', { name: /Look again/ }).click();
  const choice = card.getByRole('radio', { name: /private chat with Ann Example/ });
  await choice.waitFor({ timeout: 15000 });
  expect(id + ': the group is offered too', await card.getByRole('radio', { name: /group Home server/ }).count() === 1);
  expect(id + ': and the door to type it', await card.getByRole('radio', { name: /Type the chat ID myself/ }).count() === 1);
  await choice.click();
  expect(id + ': the chosen chat fills the chat ID', (await card.locator('input[name=Telegram_ChatID]').inputValue()) === '123456789');
  await layout(page, id, card, size);
  await shot(page, id, 'telegram-chosen');
  await shotOf(card.locator('#Telegram_ChatChoice').locator('xpath=ancestor::div[contains(@class,"MuiGrid")][1]'), id, 'telegram-chosen');
  await card.getByRole('button', { name: /Send a test message/ }).click();
  await card.getByText(/Telegram accepted the test message/).first().waitFor({ timeout: 20000 });
  const got = await (await fetch(fake + '/received')).json();
  const sent = got.filter((r) => /sendMessage$/.test(r.path));
  expect(id + ': the stand-in received the test message for the chosen chat', sent.length === 1 && String(sent[0].body.chat_id) === '123456789', JSON.stringify(sent.map((r) => r.body.chat_id)));
  const raw = await (await ctx.request.get(base + '/cosmos/api/config')).text();
  expect(id + ': the token is not in the settings read', !raw.includes('AAFakeFake'));
};

// A nightly dump of maindb by choices only; then every minute, and the dump appears
const dumps = async () => (await onServer('ls /d/cosmos-dumps 2>/dev/null | grep -c "^dump-" || true', ['/mnt/data/maindb:/d:ro'])).trim();
const dropJobs = async (ctx) => {
  const config = await getConfig(ctx);
  if (config.CRON && Object.keys(config.CRON).length) { config.CRON = {}; await putConfig(ctx, config); await waitBack(ctx); }
};
phases.cron = async (page, id, size, ctx) => {
  await dropJobs(ctx);
  await page.goto(base + '/cosmos-ui/cron', { waitUntil: 'domcontentloaded' });
  await sleep(3000);
  await page.locator('button:has(.anticon-plus)').first().click();
  const dialog = page.locator('[role=dialog]:visible').last();
  await dialog.locator('#job-where').waitFor({ timeout: 15000 });
  await dialog.getByRole('radio', { name: 'In a container of this server' }).click();
  await dialog.locator('#Container').click();
  await page.getByRole('option', { name: /^maindb/ }).click();
  await dialog.getByText('A nightly dump of this database (PostgreSQL)').waitFor({ timeout: 15000 });
  await dialog.getByRole('button', { name: 'Use this job' }).click();
  expect(id + ': the job is named from the proposal', /maindb/.test(await dialog.locator('#Name').inputValue()), await dialog.locator('#Name').inputValue());
  expect(id + ': the command is the dump of the proposal', /pg_dumpall/.test(await dialog.locator('#Command').inputValue()));
  await dialog.getByRole('radio', { name: 'Every night' }).click();
  const runs = await dialog.locator('[data-testid=job-next-runs] li').count();
  expect(id + ': the next three runs are shown', runs === 3, runs);
  expect(id + ': the expression is shown', /0 0 3 \* \* \*/.test(await dialog.locator('[data-testid=job-expression]').innerText()));
  expect(id + ': it says where the dumps go on the server', /\/mnt\/data\/maindb\/cosmos-dumps/.test(await dialog.innerText()));
  await layout(page, id, dialog, size);
  await shot(page, id, 'cron-job');
  await shotOf(dialog, id, 'cron-job');
  await dialog.getByRole('button', { name: 'Submit' }).click();
  await sleep(2000);
  await waitBack(ctx);
  const jobs = (await getConfig(ctx)).CRON || {};
  const job = Object.values(jobs)[0];
  expect(id + ': the job is saved for maindb, nightly', job && job.Container === 'maindb' && job.Crontab === '0 0 3 * * *', JSON.stringify(job && { c: job.Container, s: job.Crontab }));
  if (!job) return;
  // every minute, as an edit through the server's check
  const before = Number(await dumps()) || 0;
  const config = await getConfig(ctx);
  config.CRON[job.Name] = { ...job, Crontab: '0 * * * * *' };
  expect(id + ': the every-minute schedule is accepted', (await putConfig(ctx, config)).status === 200);
  await waitBack(ctx);
  let after = before;
  for (let i = 0; i < 20 && after <= before; i += 1) { await sleep(5000); after = Number(await dumps()) || 0; }
  expect(id + ': the job ran and the dump is in the data folder', after > before, `${before} -> ${after}`);
  await page.goto(base + '/cosmos-ui/cron', { waitUntil: 'domcontentloaded' });
  await sleep(3000);
  await shot(page, id, 'cron-list');
  // a job the form could not make, sent by a client of its own: refused in words
  const bad = await getConfig(ctx);
  bad.CRON['Broken'] = { Enabled: true, Name: 'Broken', Crontab: '0 0 25 * * *', Command: 'true', Container: 'no-such-container' };
  const refused = await putConfig(ctx, bad);
  expect(id + ': a bad job is refused in words', refused.status === 400 && /schedule|container/i.test(refused.body), refused.body);
  await dropJobs(ctx);
};

// An alert from a ready-made one; a bad one changed on its way refused in words by the server
phases.alert = async (page, id, size, ctx) => {
  const config = await getConfig(ctx);
  for (const name of Object.keys(config.MonitoringAlerts || {})) if (/^(Memory nearly full|Bad memory)/.test(name)) delete config.MonitoringAlerts[name];
  await putConfig(ctx, config);
  await waitBack(ctx);
  await page.goto(base + '/cosmos-ui/monitoring', { waitUntil: 'domcontentloaded' });
  await sleep(2500);
  await openTab(page, 'Alerts');
  await sleep(1500);
  const openEditor = async () => {
    await page.getByRole('button', { name: /Create$/ }).click();
    const d = page.locator('[role=dialog]:visible').last();
    await d.getByText('Memory nearly full').first().click();
    return d;
  };
  let dialog = await openEditor();
  await layout(page, id, null, size);
  await shot(page, id, 'alert-editor');
  await dialog.getByRole('button', { name: /^Save$/ }).click();
  await sleep(2500);
  await waitBack(ctx);
  const saved = (await getConfig(ctx)).MonitoringAlerts || {};
  expect(id + ': the alert is saved', Object.keys(saved).some((k) => /^Memory nearly full/.test(k)), Object.keys(saved).join(', '));
  // the same editor, but the request is changed to a percentage of 150 before it leaves
  await page.route('**/cosmos/api/config', async (route) => {
    if (route.request().method() !== 'PUT') return route.continue();
    const body = JSON.parse(route.request().postData());
    for (const a of Object.values(body.MonitoringAlerts || {})) if (/^Bad memory/.test(a.Name)) a.Condition = { ...a.Condition, Percent: true, Value: 150 };
    return route.continue({ postData: JSON.stringify(body) });
  });
  await page.goto(base + '/cosmos-ui/monitoring', { waitUntil: 'domcontentloaded' });
  await sleep(2500);
  await openTab(page, 'Alerts');
  await sleep(1500);
  dialog = await openEditor();
  await dialog.locator('input[name=name]').fill('Bad memory alert');
  await dialog.getByRole('button', { name: /^Save$/ }).click();
  const shown = await page.getByText(/a percentage is from 0 to 100/).first().waitFor({ timeout: 15000 }).then(() => true, () => false);
  expect(id + ': the server refuses it and the panel shows why', shown);
  await shot(page, id, 'alert-refused');
  await page.unroute('**/cosmos/api/config');
  const after = (await getConfig(ctx)).MonitoringAlerts || {};
  expect(id + ': the bad alert is not saved', !Object.keys(after).some((k) => /^Bad memory/.test(k)));
};

// The monitoring database: maindb proposed, its password on request, the connection tested (not saved)
phases.database = async (page, id, size, ctx) => {
  await page.goto(base + '/cosmos-ui/config-general/general', { waitUntil: 'domcontentloaded' });
  await sleep(1500);
  await openTab(page, 'External');
  const found = page.locator('[data-fork-postgres-found]');
  await found.waitFor({ timeout: 20000 });
  expect(id + ': the running PostgreSQL is named', /maindb/.test(await found.innerText()), await found.innerText());
  await page.getByText('Postgres (optional)').first().click();
  const box = page.locator('[data-fork-postgres-proposal=maindb]');
  await box.waitFor({ timeout: 10000 });
  await sleep(800); // the section unfolds
  const text = await box.innerText();
  expect(id + ': host, database and user are proposed from its settings', /127\.0\.0\.1:5432/.test(text) && /maindb/.test(text) && /main\b/.test(text), text);
  expect(id + ': the password is not in the proposal', !/Main-test-pw/.test(await page.locator('body').innerText()));
  expect(id + ': nothing is filled in by itself', (await page.locator('input[name=PostgresHost]').inputValue()) === '');
  const alert = page.locator('.MuiAlert-root', { has: box });
  await alert.getByRole('button', { name: 'Use these' }).click();
  expect(id + ': "Use these" fills the host', (await page.locator('input[name=PostgresHost]').inputValue()) === '127.0.0.1:5432');
  expect(id + ': and the database and user', (await page.locator('input[name=PostgresDatabase]').inputValue()) === 'maindb' && (await page.locator('input[name=PostgresUsername]').inputValue()) === 'main');
  expect(id + ': the password stays empty until asked for', (await page.locator('input[name=PostgresPassword]').inputValue()) === '');
  await alert.getByRole('button', { name: 'Use its password' }).click();
  await sleep(2500);
  expect(id + ': "Use its password" fills the password', (await page.locator('input[name=PostgresPassword]').inputValue()) === 'Main-test-pw-0420');
  await page.locator('[data-fork-postgres-test]').click();
  const result = page.locator('[data-fork-postgres-result]');
  await result.waitFor({ timeout: 20000 });
  expect(id + ': the connection is tested', /these settings work/.test(await result.innerText()), await result.innerText());
  await layout(page, id, page.locator('.MuiCard-root', { has: box }).last(), size);
  await shot(page, id, 'database');
  await shotOf(page.locator('.MuiCard-root', { has: box }).last(), id, 'database');
  // a wrong password is said in words
  await sleep(3200);
  await page.locator('input[name=PostgresPassword]').fill('wrong-password');
  await page.locator('[data-fork-postgres-test]').click();
  await sleep(4000);
  expect(id + ': a wrong password is said in words', /password/i.test(await result.innerText()), await result.innerText());
};

// Snapshots: the destination proposed on the data disk, excludes offered from what /srv/appdata holds
phases.snapshots = async (page, id, size, ctx) => {
  const config = await getConfig(ctx);
  config.Snapshots = { Enabled: false, Destination: '', Sources: ['/srv/appdata'], Excludes: [] };
  expect(id + ': the snapshot settings are reset', (await putConfig(ctx, config)).status === 200);
  await waitBack(ctx);
  await page.goto(base + '/cosmos-ui/config-general/general', { waitUntil: 'domcontentloaded' });
  const destination = page.locator('input[name=Snapshots_Destination]');
  await destination.waitFor({ timeout: 20000 });
  const note = page.locator('[data-fork-snapshot-destination-note]');
  await note.waitFor({ timeout: 15000 });
  // the test instance has two data disks: the loop file at /mnt/data and its own folder's disk at /cosmos
  const chips = await page.locator('[data-fork-snapshot-destination]').evaluateAll((els) => els.map((el) => el.getAttribute('data-fork-snapshot-destination')));
  expect(id + ': the data disk is offered', chips.includes('/mnt/data/snapshots'), chips.join(', '));
  expect(id + ': the empty destination takes the first offer', (await destination.inputValue()) === chips[0], await destination.inputValue());
  expect(id + ': with a word on where it came from', /Proposed/.test(await note.innerText()), await note.innerText());
  expect(id + ': the free space is shown', /free/.test(await page.locator('[data-fork-snapshot-destination]').first().innerText()));
  const excludes = page.locator('textarea[name=Snapshots_Excludes]').first();
  await excludes.fill('keep-this');
  const look = page.locator('[data-fork-snapshot-look="/srv/appdata"]');
  await look.getByRole('button', { name: /Look in/ }).click();
  const cache = page.locator('input[data-fork-exclude="cache"]');
  await cache.waitFor({ state: 'attached', timeout: 15000 });
  for (const pattern of ['.thumbnails', 'tmp', '*.part', 'transcodes']) {
    expect(`${id}: "${pattern}" is offered`, await page.locator(`input[data-fork-exclude="${pattern}"]`).count() === 1);
  }
  const offered = await look.innerText();
  expect(id + ': the link to / is not followed', !/\bproc\b|\bsys\b|\betc\b/.test(offered), offered.slice(0, 200));
  await cache.check();
  expect(id + ': a tick adds its pattern', (await excludes.inputValue()).split('\n').includes('cache'), await excludes.inputValue());
  await cache.uncheck();
  const left = (await excludes.inputValue()).split('\n');
  expect(id + ': unticking removes only its own line', !left.includes('cache') && left.includes('keep-this'), left.join('|'));
  await layout(page, id, page.locator('.MuiCard-root', { has: destination }).last(), size);
  await shot(page, id, 'snapshots');
  await shotOf(look, id, 'snapshots');
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

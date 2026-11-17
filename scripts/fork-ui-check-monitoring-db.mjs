// Fork addition (plan 51): the browser replay of the owner's attempts at a PostgreSQL monitoring
// database, against the throwaway instance of scripts/fork-ui-check-monitoring-db.sh (its header
// lists what is in it: Cosmos outside its Docker, the bitnami "postgresql" with its port published
// on the server's home-network address LAN only). Never the production instance.
//   PHASE=setup     finishes the wizard over the API (the sh script's `up` runs it)
//   PHASE=story     the three attempts and the save: the masked-password hint before saving,
//                   localhost:5432 refused with the right server named, a .local guess refused with
//                   the right server named, the proposal's chip filling the right host, the history
//                   sentence, the save passing, the tables in PostgreSQL. Ends with PostgreSQL in use.
//   PHASE=down      the database stopped: the pages say so, records continue in the local file;
//                   started again: healed without a restart
//   PHASE=start     a Cosmos start with the database down (the sh script restarts Cosmos between
//                   PART=before and PART=after): the local file, said, then healed by itself
//   PHASE=copy      after story: the history copy offered, run with progress, the old records in PostgreSQL
//   PHASE=reset     back to the local file (the settings only, over the API)
//   PHASE=proposals prints what the server proposes (to record what a version proposed)
// ENGINES=chromium,firefox,webkit  SIZES=d,m (1366x900, 393x851)  BASE, DIND (the test Docker's API),
// LAN (the server's home-network address). Pictures go to ./shots. Ends with "ALL OK" or the failures.
import { chromium, firefox, webkit } from 'playwright';

const base = process.env.BASE || 'http://monitoring-db.example.org:2052';
const dind = process.env.DIND || 'http://192.0.2.5:2375';
const lan = process.env.LAN || '192.0.2.5';
const PASSWORD = 'Test-Passw0rd!x';
const DB_PASSWORD = 'Cosmos-test-pw-51';
const engines = { chromium, firefox, webkit };
const names = (process.env.ENGINES || 'chromium').split(',');
const sizes = { d: { width: 1366, height: 900 }, m: { width: 393, height: 851 } };
const wanted = (process.env.SIZES || 'd,m').split(',');
const phase = process.env.PHASE || 'story';
const part = process.env.PART || '';
const note = (...a) => console.log(...a);
const failures = [];
const expect = (what, ok, detail) => {
  note(ok ? '  ok  ' : '  FAIL', what, detail === undefined ? '' : '| ' + String(detail).slice(0, 400));
  if (!ok) failures.push(what);
};
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// the test Docker's API: a container started or stopped, a command in a container
const docker = (path, method = 'POST') => fetch(dind + path, { method });
const execIn = async (container, cmd) => {
  const create = await fetch(`${dind}/containers/${container}/exec`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ Cmd: ['sh', '-c', cmd], AttachStdout: true, AttachStderr: true }),
  }).then((r) => r.json());
  const raw = await fetch(`${dind}/exec/${create.Id}/start`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ Detach: false, Tty: false }),
  }).then((r) => r.arrayBuffer());
  const bytes = new Uint8Array(raw);
  let out = '';
  for (let i = 0; i + 8 <= bytes.length;) {
    const size = (bytes[i + 4] << 24) | (bytes[i + 5] << 16) | (bytes[i + 6] << 8) | bytes[i + 7];
    out += new TextDecoder().decode(bytes.slice(i + 8, i + 8 + size));
    i += 8 + size;
  }
  return out;
};
const tables = async () => (await execIn('postgresql',
  `PGPASSWORD=${DB_PASSWORD} psql -h 127.0.0.1 -U cosmos -d cosmos_monitoring -At -c "select string_agg(tablename, ',' order by tablename) from pg_tables where schemaname='public'"`)).trim();
const pgCount = async (table) => Number((await execIn('postgresql',
  `PGPASSWORD=${DB_PASSWORD} psql -h 127.0.0.1 -U cosmos -d cosmos_monitoring -At -c "select count(*) from ${table}"`)).trim());

const login = async (ctx, sudo = true) => {
  await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: PASSWORD } });
  if (sudo) await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: PASSWORD } });
};
const getConfig = async (ctx) => (await (await ctx.request.get(base + '/cosmos/api/config')).json()).data;
const putConfig = async (ctx, config, query = '') => {
  const r = await ctx.request.put(base + '/cosmos/api/config' + query, { data: config });
  return { status: r.status(), body: await r.text() };
};
const storeState = async (ctx) => (await (await ctx.request.get(base + '/cosmos/api/monitoring-store')).json()).data || {};
const waitBack = async (ctx) => {
  for (let i = 0; i < 30; i += 1) {
    await sleep(1500);
    const r = await ctx.request.get(base + '/cosmos/api/status').catch(() => null);
    if (r && r.status() < 500) return;
  }
};
const waitState = async (ctx, ok, seconds) => {
  let s = {};
  for (let i = 0; i < seconds; i += 1) {
    s = await storeState(ctx).catch(() => ({}));
    if (ok(s)) return s;
    await sleep(1000);
  }
  return s;
};

// no sideways scroll, and the buttons and chips of `scope` at least 44 px tall on a phone
const layout = async (page, id, scope, size) => {
  const wide = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(id + ': no sideways scroll', wide <= 1, wide);
  if (size !== 'm' || !scope) return;
  const small = await scope.locator('button:visible, [role=radio]:visible, [role=checkbox]:visible').evaluateAll((els) => els
    .map((el) => { const box = (el.closest('label') || el).getBoundingClientRect(); return { text: (el.innerText || el.getAttribute('aria-label') || el.name || '').trim().slice(0, 40), h: Math.round(box.height), w: Math.round(box.width) }; })
    .filter((b) => b.h > 0 && (b.h < 44 && b.w < 44 || b.h < 32)));
  expect(id + ': touch targets of at least 44 px', small.length === 0, JSON.stringify(small.slice(0, 6)));
};
const openTab = async (page, label) => {
  const tab = page.getByRole('tab', { name: label });
  if (await tab.count() && await tab.first().isVisible()) return tab.first().click();
  await page.locator('main [role=combobox]').first().click();
  await page.getByRole('option', { name: label, exact: true }).click();
};
const shotOf = (locator, id, name) => locator.screenshot({ path: `shots/${id}-${name}-part.png` }).catch(() => {});
const shot = (page, id, name) => page.screenshot({ path: `shots/${id}-${name}.png`, fullPage: true }).catch(() => {});

const openExternal = async (page) => {
  await page.goto(base + '/cosmos-ui/config-general/general', { waitUntil: 'domcontentloaded' });
  await sleep(1500);
  // the page asks again what needed the confirmation, which may ask once more
  for (let i = 0; i < 3 && await answerPrompt(page); i += 1) await sleep(1500);
  await openTab(page, 'External');
  await page.locator('input[name=PostgresHost]').waitFor({ state: 'attached', timeout: 20000 });
  const fold = page.getByText('Postgres (optional)').first();
  if (!(await page.locator('input[name=PostgresHost]').isVisible())) await fold.click();
  await page.locator('input[name=PostgresHost]').waitFor({ timeout: 10000 });
  await sleep(800);
};
// the password prompt the panel opens for an action that needs it: answered (what was typed stays)
const answerPrompt = async (page) => {
  const prompt = page.locator('[data-sudo-prompt]');
  if (!(await prompt.waitFor({ timeout: 8000 }).then(() => true, () => false))) return false;
  const input = prompt.locator('input').first();
  await input.fill(PASSWORD);
  await input.press('Enter'); // the prompt is a form: Enter sends it (a click raced its re-render in WebKit)
  await prompt.waitFor({ state: 'hidden', timeout: 10000 }).catch(() => {});
  return true;
};
const field = (page, name) => page.locator(`input[name=${name}]`);
const save = async (page) => {
  await page.getByRole('button', { name: /^Save$/ }).last().click();
};
// the history sentence, if asked: confirmed
const confirmMove = async (page, id, expected) => {
  const dialog = page.locator('[data-fork-store-move]');
  const asked = await dialog.waitFor({ timeout: 4000 }).then(() => true, () => false);
  if (expected !== undefined) expect(id + ': the history sentence is ' + (expected ? 'asked' : 'not asked again'), asked === expected);
  if (!asked) return '';
  const text = await dialog.innerText();
  await dialog.locator('[data-fork-store-move-save]').click();
  return text;
};
const refusal = async (page) => {
  const alert = page.locator('[data-fork-postgres-refused]');
  await alert.waitFor({ timeout: 25000 });
  return alert.innerText();
};

// ---------------------------------------------------------------------------------------------
const phases = {};

phases.setup = async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext();
  const step = (data) => ctx.request.post(base + '/cosmos/api/newInstall', { data }).then((r) => r.status()).catch(() => 'exit');
  note('wizard:', await step({ step: '2', mongodbMode: 'Create' }),
    await step({ step: '3', httpsCertificateMode: 'DISABLED', hostname: 'monitoring-db.example.org' }),
    await step({ step: '4', nickname: 'admin', password: PASSWORD }), await step({ step: '5' }));
  await browser.close();
};

// Back to the local file (the settings only)
phases.reset = async (page, id, size, ctx) => {
  const config = await getConfig(ctx);
  config.Database = { ...config.Database, PostgresHost: '', PostgresDatabase: '', PostgresUsername: '', PostgresPassword: '' };
  const r = await putConfig(ctx, config);
  expect(id + ': back to the local file', r.status === 200, r.body);
  await waitBack(ctx);
  await waitState(ctx, (s) => s.inUse === 'sqlite' && !s.fallback, 20);
};

// The owner's day: a host saved earlier (here: an odd one saved with "Save anyway" over the API, so
// that the password is stored and masked), then the three attempts and the right value.
phases.story = async (adminPage, id, size, ctx) => {
  await phases.reset(adminPage, id, size, ctx);
  const config = await getConfig(ctx);
  config.Database = { ...config.Database, PostgresHost: `${lan}:6543`, PostgresDatabase: 'postgres', PostgresUsername: 'cosmos', PostgresPassword: DB_PASSWORD };
  const first = await putConfig(ctx, config);
  expect(id + ': a server where nothing answers is refused over the API too', first.status === 400 && /UC047/.test(first.body), first.body);
  const anyway = await putConfig(ctx, config, '?database=unchecked');
  expect(id + ': and saved with "Save anyway" by an administrator', anyway.status === 200, anyway.body);
  await waitBack(ctx);
  const fell = await waitState(ctx, (s) => s.fallback, 30);
  expect(id + ': the store is the local file meanwhile, said', fell.fallback && fell.inUse === 'sqlite' && fell.host === `${lan}:6543`, JSON.stringify(fell));

  // the owner's page: opened before the password was confirmed, so the saved password is the mask
  const uiCtx = await adminPage.context().browser().newContext({ viewport: sizes[size] });
  await uiCtx.route('**/sw.js', (route) => route.abort());
  await login(uiCtx, false);
  const page = await uiCtx.newPage();
  page.on('pageerror', (e) => expect(id + ': no script error', false, e.message));
  await openExternal(page).catch(async (e) => { await shot(page, id, 'story-open-error'); throw e; });
  const line = page.locator('[data-fork-monitoring-fallback]').first();
  await line.waitFor({ timeout: 20000 });
  expect(id + ': External says the database does not answer and where records go', /does not answer since/.test(await line.innerText()) && /local file/.test(await line.innerText()), await line.innerText());
  await shot(page, id, 'fallback-external');

  // the proposals: the owner's container, its host as Cosmos reaches it, checked
  const box = page.locator('[data-fork-postgres-proposal=postgresql]');
  await box.waitFor({ timeout: 20000 });
  const boxText = await page.locator('.MuiAlert-root', { has: box }).innerText();
  expect(id + ': the bitnami container is proposed at the address it is published on', boxText.includes(`${lan}:5432`) && /cosmos_monitoring/.test(boxText) && /user: cosmos/.test(boxText), boxText);
  expect(id + ': and checked from Cosmos', /Answers from Cosmos/.test(boxText), boxText);
  const official = page.locator('.MuiAlert-root', { has: page.locator('[data-fork-postgres-proposal=pgofficial]') });
  expect(id + ': the unpublished one is proposed at its own address with the warning', /changes when the container is made again/.test(await official.innerText()) && /appdb/.test(await official.innerText()), await official.innerText());

  // attempt 1: host and database changed, the masked password still there: said before the save
  await field(page, 'PostgresHost').fill('localhost:5432');
  await field(page, 'PostgresDatabase').fill('cosmos_monitoring');
  expect(id + ': the password field holds the mask', (await field(page, 'PostgresPassword').inputValue()) === '***');
  const hint = page.locator('[data-fork-postgres-masked]');
  await hint.waitFor({ timeout: 5000 });
  expect(id + ': the masked-password hint is shown before saving', /type the password again/.test(await hint.innerText()), await hint.innerText());
  await shotOf(page.locator('.MuiCard-root', { has: hint }).last(), id, 'masked-hint');

  // the owner confirms the password (the prompt a save asks for), then
  // attempt 2: localhost:5432 with the password typed: refused, the right server named
  await uiCtx.request.post(base + '/cosmos/api/sudo', { data: { password: PASSWORD } });
  await field(page, 'PostgresPassword').fill(DB_PASSWORD);
  expect(id + ': the hint goes once the password is typed', await hint.count() === 0);
  await save(page);
  const moved = await confirmMove(page, id, true);
  expect(id + ': the history sentence: the records of the database saved before stay there', /stay there and are not copied/.test(moved), moved);
  const r2 = await refusal(page);
  expect(id + ': localhost:5432 is refused', /Not saved/.test(r2) && /Nothing answers at localhost:5432/.test(r2), r2);
  expect(id + ': with the right server named', r2.includes(`published on ${lan} only`) && r2.includes(`use ${lan}:5432`), r2);
  expect(id + ': "Save anyway" is offered to the administrator', await page.locator('[data-fork-postgres-anyway]').count() === 1);
  await layout(page, id, page.locator('[data-fork-postgres-refused]'), size);
  await shot(page, id, 'refused-localhost');
  await shotOf(page.locator('[data-fork-postgres-refused]'), id, 'refused-localhost');
  expect(id + ': nothing was saved', (await getConfig(ctx)).Database.PostgresHost === `${lan}:6543`);

  // attempt 3: a .local guess: refused, the right server named
  await field(page, 'PostgresHost').fill('postgresql.local:5432');
  await save(page);
  await confirmMove(page, id, false);
  await sleep(1000);
  const r3 = await refusal(page);
  expect(id + ': a name that does not resolve is refused', /could not be looked up/.test(r3), r3);
  expect(id + ': with the right server named', /\.local exist only for apps that have a URL/.test(r3) && r3.includes(`use ${lan}:5432`), r3);
  await shotOf(page.locator('[data-fork-postgres-refused]'), id, 'refused-local-name');

  // the proposal's chip fills the right host
  const chip = page.locator(`[data-fork-postgres-choice="${lan}:5432"]`);
  expect(id + ': the server field offers the proposals', await chip.count() === 1 && await page.locator('[data-fork-postgres-choice=custom]').count() === 1);
  expect(id + ': the chip says it answers', /answers/.test(await chip.innerText()), await chip.innerText());
  await chip.click();
  expect(id + ': the chip fills the right host', (await field(page, 'PostgresHost').inputValue()) === `${lan}:5432`);
  await layout(page, id, page.locator('[data-fork-postgres-choices]'), size);
  await shotOf(page.locator('[data-fork-postgres-choices]'), id, 'choices');
  await save(page);
  await confirmMove(page, id, false);
  // saved: the refusal goes, the store switches, the tables appear
  const healed = await waitState(ctx, (s) => s.inUse === 'pgx' && !s.fallback, 40);
  expect(id + ': the save passes and PostgreSQL is the store', healed.inUse === 'pgx' && !healed.fallback && healed.host === `${lan}:5432`, JSON.stringify(healed));
  const list = await tables();
  expect(id + ': the tables appear in PostgreSQL', ['events', 'metric_values', 'metrics', 'monitoring_kv', 'notifications'].every((tb) => list.split(',').includes(tb)), list);
  await waitBack(ctx);
  await page.reload({ waitUntil: 'domcontentloaded' });
  await sleep(2000);
  expect(id + ': no line about the local file any more', await page.locator('[data-fork-monitoring-fallback]').count() === 0);
  await uiCtx.close();
};

// The first save from the local file to PostgreSQL asks the history sentence (a phase of its own:
// it needs the local file as the store first)
phases.move = async (page, id, size, ctx) => {
  await phases.reset(page, id, size, ctx);
  await openExternal(page);
  await page.locator(`[data-fork-postgres-choice="${lan}:5432"]`).click();
  expect(id + ': the chip fills database and user when empty', (await field(page, 'PostgresDatabase').inputValue()) === 'cosmos_monitoring' && (await field(page, 'PostgresUsername').inputValue()) === 'cosmos');
  await field(page, 'PostgresPassword').fill(DB_PASSWORD);
  await save(page);
  const dialog = page.locator('[data-fork-store-move]');
  await dialog.waitFor({ timeout: 8000 });
  const text = await dialog.innerText();
  expect(id + ': the history sentence before moving to PostgreSQL', /stay in the local file and are not copied now/.test(text) && /charts start again from now/.test(text) && /offers to copy the history/.test(text), text);
  await layout(page, id, dialog, size);
  await shot(page, id, 'move-sentence');
  await dialog.locator('[data-fork-store-move-save]').click();
  const s = await waitState(ctx, (st) => st.inUse === 'pgx', 40);
  expect(id + ': saved, PostgreSQL in use', s.inUse === 'pgx', JSON.stringify(s));
  await waitBack(ctx);
};

// The database stopped: the pages say so, records continue in the local file; started: healed
phases.down = async (page, id, size, ctx) => {
  let s = await storeState(ctx);
  expect(id + ': PostgreSQL is the store before', s.inUse === 'pgx', JSON.stringify(s));
  await docker('/containers/postgresql/stop?t=2');
  s = await waitState(ctx, (st) => st.fallback && st.inUse === 'sqlite', 90);
  expect(id + ': the database stopped: the local file, said', s.fallback && s.inUse === 'sqlite', JSON.stringify(s));
  await page.goto(base + '/cosmos-ui/monitoring', { waitUntil: 'domcontentloaded' });
  const line = page.locator('[data-fork-monitoring-fallback]').first();
  await line.waitFor({ timeout: 20000 }).catch(() => {});
  expect(id + ': the monitoring page says it', /does not answer since/.test(await line.innerText().catch(() => '')), await line.innerText().catch(() => ''));
  await layout(page, id, null, size);
  await shot(page, id, 'down-monitoring');
  // records continue: the notice of the fallback is written into the local file, and read from it
  await sleep(3000);
  const notes = await (await ctx.request.get(base + '/cosmos/api/notifications')).text();
  expect(id + ': records go on in the local file (the notice is there)', /monitoring\.store\.down|monitoring database does not answer/i.test(notes), notes.slice(0, 300));
  await openExternal(page);
  await page.locator('[data-fork-monitoring-fallback]').first().waitFor({ timeout: 20000 });
  await shot(page, id, 'down-external');
  await docker('/containers/postgresql/start');
  s = await waitState(ctx, (st) => st.inUse === 'pgx' && !st.fallback, 120);
  expect(id + ': started again: healed without a restart', s.inUse === 'pgx' && !s.fallback, JSON.stringify(s));
  expect(id + ': the period in the local file is known', !!s.localFrom && !!s.localTo, JSON.stringify(s));
  await sleep(2000);
  expect(id + ': the notice that it answers again is in PostgreSQL', (await pgCount("notifications where fork_kind='monitoring.store.restored'")) >= 1);
};

// A Cosmos start with the database down: PART=before stops the database (the sh script then
// restarts Cosmos), PART=after looks at the fallback and starts the database: healed by itself
phases.start = async (page, id, size, ctx) => {
  if (part === 'before') {
    await docker('/containers/postgresql/stop?t=2');
    expect(id + ': the database is stopped', true);
    return;
  }
  let s = await waitState(ctx, (st) => st.fallback, 30);
  expect(id + ': started with the database down: the local file, said', s.fallback && s.inUse === 'sqlite' && !!s.reason, JSON.stringify(s));
  await page.goto(base + '/cosmos-ui/monitoring', { waitUntil: 'domcontentloaded' });
  await page.locator('[data-fork-monitoring-fallback]').first().waitFor({ timeout: 20000 }).catch(() => {});
  expect(id + ': the monitoring page says it', await page.locator('[data-fork-monitoring-fallback]').count() > 0);
  await docker('/containers/postgresql/start');
  s = await waitState(ctx, (st) => st.inUse === 'pgx' && !st.fallback, 120);
  expect(id + ': healed by itself', s.inUse === 'pgx' && !s.fallback, JSON.stringify(s));
};

// After the switch: the copy of the local file's history is offered, runs with progress, and the
// old records are then in PostgreSQL (the charts read them from there)
phases.copy = async (page, id, size, ctx) => {
  const s = await storeState(ctx);
  expect(id + ': PostgreSQL is the store', s.inUse === 'pgx', JSON.stringify(s));
  const before = { values: await pgCount('metric_values'), events: await pgCount('events') };
  const oldest = Number((await execIn('postgresql', `PGPASSWORD=${DB_PASSWORD} psql -h 127.0.0.1 -U cosmos -d cosmos_monitoring -At -c "select coalesce(min(date),0) from events"`)).trim());
  await openExternal(page);
  const offer = page.locator('[data-fork-copy-offer=to-postgres]');
  await offer.waitFor({ timeout: 30000 });
  const text = await offer.innerText();
  expect(id + ': the copy of the history is offered with its size and dates', /Copy the history from the local file/.test(text) && /records/.test(text) && /MB/.test(text), text);
  await layout(page, id, offer, size);
  await shotOf(offer, id, 'copy-offer');
  await page.locator('[data-fork-copy-start=to-postgres]').click();
  await answerPrompt(page);
  const progress = page.locator('[data-fork-copy-progress]');
  const sawProgress = await progress.first().waitFor({ timeout: 15000 }).then(() => true, () => false);
  if (sawProgress) await shotOf(progress.first(), id, 'copy-progress');
  const result = page.locator('[data-fork-copy-result]');
  await result.first().waitFor({ timeout: 240000 });
  const outcome = await result.first().getAttribute('data-fork-copy-result');
  const said = await result.first().innerText();
  expect(id + ': the copy ends verified', (outcome === 'done') && /both stores (now|already) hold the same history/.test(said), outcome + ' | ' + said);
  await shotOf(result.first(), id, 'copy-result');
  const after = { values: await pgCount('metric_values'), events: await pgCount('events') };
  const oldestAfter = Number((await execIn('postgresql', `PGPASSWORD=${DB_PASSWORD} psql -h 127.0.0.1 -U cosmos -d cosmos_monitoring -At -c "select coalesce(min(date),0) from events"`)).trim());
  expect(id + ': the old records are in PostgreSQL now', after.values >= before.values && after.events >= before.events && (oldest === 0 || oldestAfter <= oldest),
    JSON.stringify({ before, after, oldest, oldestAfter }));
  await page.goto(base + '/cosmos-ui/monitoring', { waitUntil: 'domcontentloaded' });
  await sleep(4000);
  await layout(page, id, null, size);
  await shot(page, id, 'copy-monitoring');
};

// What the server proposes, as it answers (for the record of what a version proposed)
phases.proposals = async (page, id, size, ctx) => {
  const r = await ctx.request.get(base + '/cosmos/api/config/postgres-proposals');
  note(id + ': proposals', r.status(), await r.text());
};

// ---------------------------------------------------------------------------------------------
const order = phase.split(',');
if (order.includes('setup')) {
  await phases.setup();
} else {
  for (const name of names) for (const size of wanted) {
    const id = `${name}-${size}`;
    const browser = await engines[name].launch();
    const ctx = await browser.newContext({ viewport: sizes[size] });
    await ctx.route('**/sw.js', (route) => route.abort()); // the panel's service worker would fetch past what a check looks at
    await login(ctx);
    const page = await ctx.newPage();
    page.on('pageerror', (e) => expect(id + ': no script error', false, e.message));
    for (const which of order) {
      note(`--- ${id}: ${which}`);
      try {
        await phases[which](page, id, size, ctx);
      } catch (e) {
        expect(`${id}: ${which} ran to its end`, false, e.message.split('\n').slice(0, 3).join(' '));
        await shot(page, id, which + '-error');
      }
    }
    await browser.close();
  }
}
note(failures.length ? `FAILURES (${failures.length}):\n  ` + failures.join('\n  ') : 'ALL OK');
process.exit(failures.length ? 1 : 0);

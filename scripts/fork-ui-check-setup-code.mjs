// Fork addition (plan 47): the setup wizard of a fresh instance in a real
// browser, along its DEFAULT path: the one-time setup code
// (src/fork_setup_code.go, client/src/pages/newInstall/setupCodeGate.jsx),
// the welcome step with "clean install" ticked as it is by default and
// "Next", Docker, HTTPS as proposed, the administrator, "Apply and restart",
// and the server set up at the end. Along the way: the code's errors in words
// and no sideways scroll.
//
// One run sets one instance up: scripts/fork-ui-check-setup-code.sh makes a
// fresh instance for each engine and size and runs this. By hand: a fresh
// test Cosmos (never set up, restarted in a loop) at BASE on a Docker network
// of the test's own; SETUP_CODE is its code (cat <its config
// folder>/setup-code); ENGINE (chromium, firefox or webkit), SIZE (d: 1366x900,
// m: 390x844), OUT for the screenshots. Run from the Playwright image on that
// network, with playwright@1.65.1 installed.
import { chromium, firefox, webkit } from 'playwright';
import { mkdirSync } from 'node:fs';

const base = process.env.BASE || 'http://cosmos-setup.test:2052';
const code = process.env.SETUP_CODE || '';
const out = process.env.OUT || 'out';
const engine = process.env.ENGINE || 'chromium';
const size = process.env.SIZE || 'd';
const viewports = { d: { width: 1366, height: 900 }, m: { width: 390, height: 844 } };
const kinds = { chromium, firefox, webkit };
mkdirSync(out, { recursive: true });

let failed = 0;
const label = `${engine}/${size}`;
const check = (what, ok, detail) => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}: ${what}${detail ? ' (' + detail + ')' : ''}`);
  if (!ok) failed++;
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
if (!/^[A-Z0-9]{4}(-[A-Z0-9]{4}){4}$/.test(code)) {
  console.log('SETUP_CODE is missing or not a code');
  process.exit(2);
}

const browser = await kinds[engine].launch();
const ctx = await browser.newContext({ viewport: viewports[size], serviceWorkers: 'block', ignoreHTTPSErrors: true });
const page = await ctx.newPage();
const errors = [];
page.on('pageerror', (e) => errors.push(e.message));
const shot = (name) => page.screenshot({ path: `${out}/setup-${engine}-${size}-${name}.png`, fullPage: true }).catch(() => {});
const overflow = () => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);

// --- the code
// the code file is written before the server listens: try for a while
for (let i = 0; ; i++) {
  try { await page.goto(base + '/cosmos-ui/newInstall', { waitUntil: 'domcontentloaded' }); break; } catch (e) { if (i >= 60) throw e; await sleep(1000); }
}
const gate = page.locator('[data-fork-setup-code]');
const shown = await gate.waitFor({ timeout: 30000 }).then(() => true, () => false);
check('the wizard opens on the setup code', shown);
if (!shown) { await browser.close(); process.exit(1); }
await sleep(800);
const text = await page.locator('body').innerText();
check('it says where the code is', /install\.sh/.test(text) && /setup-code/.test(text) && /journalctl/.test(text));
check('nothing of the wizard behind it', !/clean install/i.test(text));
const box = gate.locator('input');
const submit = gate.locator('button[type=submit]');
const alert = () => page.locator('#setup-code-problem').innerText().catch(() => '');
await submit.click();
await sleep(300);
check('an empty code is said', /Enter the setup code/i.test(await alert()));
await box.fill('ABCD-EFGH-JKMN-PQRS-TVW0');
await submit.click();
await sleep(300);
check('a look-alike sign is said', /never holds 0, 1, I, L, O or U/.test(await alert()));
await box.fill('AAAA-AAAA-AAAA-AAAA-AAAA');
await submit.click();
await sleep(1500);
check('a wrong code is refused by the server', /not the setup code/i.test(await alert()));
check('no sideways scroll on the code screen', (await overflow()) <= 0);
await shot('code');
await box.fill(code.toLowerCase().replace(/-/g, ' '));
await submit.click();

// --- the default path
const welcome = await page.getByText(/clean install/i).first().waitFor({ timeout: 15000 }).then(() => true, () => false);
check('the right code, in lower case with spaces, opens the wizard', welcome);
const tick = page.locator('input[type=checkbox]').first();
check('"clean install" is ticked by default', await tick.isChecked().catch(() => false));
await shot('welcome');
const next = () => page.locator('button').filter({ has: page.locator('.anticon-right') }).last();
await next().click(); // Start: the clean install (step -1) runs with the code
await sleep(2500);
check('after the clean install the wizard goes on, not back to the code', (await gate.count()) === 0, await alert());
await next().click(); // Docker: next, or skip without Docker
await sleep(1500);
await shot('https');
const hostname = page.locator('input[name=Hostname]');
check('the HTTPS step is shown', await hostname.waitFor({ timeout: 15000 }).then(() => true, () => false));
await page.locator('form button[type=submit]').first().click(); // HTTPS as proposed
const admin = page.locator('input[name=nickname]');
check('the HTTPS step is accepted with the same code', await admin.waitFor({ timeout: 20000 }).then(() => true, () => false), await alert());
await admin.fill('owner'); // ('admin' and 'root' are refused by the form)
await page.locator('input[name=email]').fill('admin@example.org');
await page.locator('input[name=password]').fill('E2e-Test-Passw0rd!x');
await page.locator('input[name=confirmPassword]').fill('E2e-Test-Passw0rd!x');
check('no sideways scroll on the administrator step', (await overflow()) <= 0);
await shot('admin');
await page.locator('form button[type=submit]').first().click();
const finish = await page.getByText(/apply|restart/i).first().waitFor({ timeout: 20000 }).then(() => true, () => false);
check('the administrator is created', finish, await alert());
await shot('finish');
const errorsBefore = errors.length; // the restart that follows breaks the page's own requests: expected
await next().click(); // Apply and restart: step 5, the server restarts set up

// --- set up: the wizard is closed, code or not
let closed = false;
for (let i = 0; i < 60 && !closed; i++) {
  await sleep(1000);
  // set up with HTTPS, the server sends plain HTTP to its HTTPS port
  for (const url of [base, base.replace(/^http:/, 'https:').replace(/:2052$/, ':2053')]) {
    const answer = await ctx.request.post(url + '/cosmos/api/newInstall', { data: { step: '2' }, headers: { 'X-Cosmos-Setup-Code': code }, failOnStatusCode: false, maxRedirects: 0 }).catch(() => null);
    if (answer && answer.status() === 403 && /NI001/.test(await answer.text())) closed = true;
  }
}
check('the server is set up and its wizard closed', closed);
check('no script error before the restart', errorsBefore === 0, errors.slice(0, errorsBefore).join(' | '));
await browser.close();
console.log(failed ? `${failed} check(s) failed` : 'all checks passed');
process.exit(failed ? 1 : 0);

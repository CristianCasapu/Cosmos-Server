// Fork addition (plan 47): the first screen of the setup wizard, which asks
// for the one-time setup code (src/fork_setup_code.go,
// client/src/pages/newInstall/setupCodeGate.jsx), in real browsers.
//
// Setup: a fresh test Cosmos (never set up) at BASE, its HTTP port on a
// Docker network of the test's own, no Docker needed; SETUP_CODE is its code:
// cat <its config folder>/setup-code. Run from the Playwright image on the
// same network:
//   docker run --rm --network <net> --ipc=host -v <dir>:/work -w /work \
//     -e BASE=http://cosmos-setup.test:2052 -e SETUP_CODE=... -e OUT=/work/out \
//     mcr.microsoft.com/playwright:v1.65.1-noble sh -c 'npm i playwright@1.65.1 && node fork-ui-check-setup-code.mjs'
// ENGINES (default chromium,firefox,webkit), SIZES d,m (1366x900, 390x844).
// It makes one wrong try only (in the first engine): the server pauses an
// address after five.
import { chromium, firefox, webkit } from 'playwright';
import { mkdirSync } from 'node:fs';

const base = process.env.BASE || 'http://cosmos-setup.test:2052';
const code = process.env.SETUP_CODE || '';
const out = process.env.OUT || 'out';
const engines = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const sizes = (process.env.SIZES || 'd,m').split(',');
const viewports = { d: { width: 1366, height: 900 }, m: { width: 390, height: 844 } };
const kinds = { chromium, firefox, webkit };
mkdirSync(out, { recursive: true });

let failed = 0;
const check = (label, what, ok, detail) => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${label}: ${what}${detail ? ' (' + detail + ')' : ''}`);
  if (!ok) failed++;
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

if (!/^[A-Z0-9]{4}(-[A-Z0-9]{4}){4}$/.test(code)) {
  console.log('SETUP_CODE is missing or not a code');
  process.exit(2);
}
// typed as a person might: lower case, spaces for dashes
const typed = code.toLowerCase().replace(/-/g, ' ');

let first = true;
for (const engine of engines) {
  for (const size of sizes) {
    const label = `${engine}/${size}`;
    const browser = await kinds[engine].launch();
    const ctx = await browser.newContext({ viewport: viewports[size], serviceWorkers: 'block' });
    const page = await ctx.newPage();
    const errors = [];
    page.on('pageerror', (e) => errors.push(e.message));
    await page.goto(base + '/cosmos-ui/newInstall', { waitUntil: 'domcontentloaded' });
    const gate = page.locator('[data-fork-setup-code]');
    const shown = await gate.waitFor({ timeout: 20000 }).then(() => true, () => false);
    check(label, 'the wizard opens on the setup code', shown);
    if (!shown) { await browser.close(); continue; }
    await sleep(800);
    const text = await page.locator('body').innerText();
    check(label, 'it says where the code is', /install\.sh/.test(text) && /setup-code/.test(text) && /journalctl/.test(text));
    check(label, 'nothing of the wizard behind it', !/clean install/i.test(text));
    const box = gate.locator('input');
    const submit = gate.locator('button[type=submit]');
    const alert = () => page.locator('#setup-code-problem').innerText().catch(() => '');

    await submit.click();
    await sleep(300);
    check(label, 'an empty code is said', /Enter the setup code/i.test(await alert()));
    await box.fill('ABCD-EFGH-JKMN-PQRS-TVW0');
    await submit.click();
    await sleep(300);
    check(label, 'a look-alike sign is said', /never holds 0, 1, I, L, O or U/.test(await alert()));
    if (first) {
      await box.fill('AAAA-AAAA-AAAA-AAAA-AAAA');
      await submit.click();
      await sleep(1500);
      check(label, 'a wrong code is refused by the server', /not the setup code/i.test(await alert()));
      first = false;
    }
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    check(label, 'no sideways scroll', overflow <= 0, String(overflow));
    await page.screenshot({ path: `${out}/setup-code-${engine}-${size}.png`, fullPage: true });

    await box.fill(typed);
    await submit.click();
    const welcome = await page.getByText(/clean install/i).first().waitFor({ timeout: 15000 }).then(() => true, () => false);
    check(label, 'the right code, typed in lower case with spaces, opens the wizard', welcome);
    await page.screenshot({ path: `${out}/setup-code-${engine}-${size}-accepted.png`, fullPage: true });
    check(label, 'no script error', errors.length === 0, errors.join(' | '));
    await browser.close();
  }
}
console.log(failed ? `${failed} check(s) failed` : 'all checks passed');
process.exit(failed ? 1 : 0);

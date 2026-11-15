// Fork addition: the card "Block abusers at Cloudflare" and the dashboard's
// "Block at Cloudflare" in headless Chromium, Firefox and WebKit, at
// 1366x900 and 393x851, light and dark, against a throwaway Cosmos and the
// stand-in for Cloudflare's API (scripts/fork-fake-cloudflare.go). Nothing is
// sent to Cloudflare: the test network is made with --internal.
//
// What it expects (set up as in scripts/fork-ui-check-cloudflare.mjs):
//   - a network made with `docker network create --internal`, the stand-in on it
//     with the alias api.cloudflare.com (FAKE=http://<it>:8080, its control port);
//   - a Cosmos set up (admin / PASSWORD) at SERVER_IP with HTTPS on 2053, the domain
//     example.org with the stand-in's GoodToken, its panel name shielded, the feature
//     off, one custom rule of the owner's own at the stand-in (description "the
//     owner's rule", /custom/add), and a few incidents through the stand-in's edge
//     role from 198.51.100.77 (below every threshold) and an administrator's sign-in
//     from 198.51.100.150 (its header says how), so that the dashboard lists both;
//   - this script run from the Playwright image on that network, from an address of
//     the local network (it signs in by the server's IP address).
// Each run plays the first owner's token (the account's IP Access Rules and
// the zone's custom rules readable only: the test says what to add and offers
// the account's rules), then the same token with "Zone > Zone WAF > Edit"
// (switched on, custom rules), a block from the dashboard into Cosmos' custom
// rule, "Unblock" from the list, and switching off.
// The server sends its own script policy: any violation is a failure.
// Screenshots go to ./shots. ENGINES=chromium,firefox,webkit SIZES=d,m MODES=light,dark.
import { chromium, firefox, webkit, request } from 'playwright';

const ip = process.env.SERVER_IP || '172.31.72.10';
const base = `https://${ip}:2053`;
const fake = process.env.FAKE || 'http://172.31.72.5:8080';
const password = process.env.PASSWORD || 'Test-Passw0rd!x';
const engines = { chromium, firefox, webkit };
const viewports = { d: { width: 1366, height: 900 }, m: { width: 393, height: 851 } };
const pick = (name, all) => (process.env[name] ? process.env[name].split(',') : all);

const failures = [];
const note = (...a) => console.log(...a);
const expect = (ok, what) => { note(ok ? '  ok  ' : '  FAIL', what); if (!ok) failures.push(what); };
const api = await request.newContext({ ignoreHTTPSErrors: true });
const token = (query) => api.get(fake + '/token?' + query);
const custom = async () => (await (await api.get(fake + '/custom')).json()) || { rules: [] };
const cosmosRule = async (word) => ((await custom()).rules || []).find((r) => /cosmos-edge:/.test(r.description) && r.description.endsWith('] ' + word));
const headers = { Origin: base, 'Sec-Fetch-Site': 'same-origin' };

const signIn = async (page) => {
  await page.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password }, headers });
  await page.request.post(base + '/cosmos/api/sudo', { data: { password }, headers });
};
const noSideScroll = async (page, name, where) => {
  const over = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(over <= 1, `${name}: no sideways scroll on ${where} (${over}px over)`);
};
const smallTargets = async (page, scope) => page.evaluate((selector) => {
  const out = [];
  document.querySelectorAll(selector + ' button, ' + selector + ' [role=radio]').forEach((el) => {
    const box = el.getBoundingClientRect();
    if (box.width > 0 && box.height > 0 && box.height < 43.5) out.push((el.innerText || el.getAttribute('aria-label') || '?').slice(0, 40) + ' ' + Math.round(box.height));
  });
  return out;
}, scope);
const waitFor = async (check, seconds = 30) => {
  for (let i = 0; i < seconds; i++) {
    if (await check()) return true;
    await new Promise((r) => setTimeout(r, 1000));
  }
  return false;
};

for (const engineName of pick('ENGINES', ['chromium', 'firefox', 'webkit'])) {
  for (const size of pick('SIZES', ['d', 'm'])) {
    for (const mode of pick('MODES', ['light', 'dark'])) {
      const name = `${engineName}-${size}-${mode}`;
      const browser = await engines[engineName].launch();
      const ctx = await browser.newContext({ viewport: viewports[size], ignoreHTTPSErrors: true, colorScheme: mode, locale: 'en-GB' });
      const page = await ctx.newPage();
      const violations = [];
      page.on('pageerror', (e) => failures.push(`${name} PAGEERROR: ${e.message.slice(0, 300)}`));
      page.on('console', (m) => {
        if (/Content Security Policy|Content-Security-Policy|violates|Refused to (load|execute|frame)/i.test(m.text())) violations.push(m.text().slice(0, 300));
      });
      await signIn(page);

      // --- the first owner's token: the test says what is missing, in words
      await token('account=1&wafread=1');
      await page.goto(base + '/cosmos-ui/config-general/https', { waitUntil: 'domcontentloaded' });
      const card = page.locator('[data-edge-card]');
      await card.waitFor({ timeout: 30000 });
      await page.waitForTimeout(1200);
      await card.scrollIntoViewIfNeeded();
      expect(await card.locator('[data-edge-state-line=off]').count() === 1, `${name}: the card says it is off`);
      await card.locator('[data-edge-action=enable]').click();
      const proof = card.locator('[data-edge-proof]').first();
      await proof.waitFor({ timeout: 30000 }).catch(() => {});
      const said = await proof.innerText().catch(() => '');
      expect(await proof.getAttribute('data-edge-proof').catch(() => '') === 'waf_read_only', `${name}: the owner's token: custom rules readable only`);
      expect(/Zone > Zone WAF > Edit/.test(said) && /whole account/.test(said), `${name}: the sentence names the permission to add and the account's rules (${said.slice(0, 90)})`);
      const notice = await card.locator('[data-edge-notice=error]').innerText().catch(() => '');
      expect(/Not switched on/.test(notice) && !/did not answer/.test(notice), `${name}: the refusal is a sentence, not "no answer" (${notice.slice(0, 60)})`);
      expect(await card.locator('[data-edge-account-choice]').count() === 1, `${name}: the account's rules are offered, with what they mean`);
      expect(/every domain of your Cloudflare account/.test(await card.innerText()), `${name}: the account choice says it blocks on every domain`);
      await noSideScroll(page, name, 'the card, refused');
      await card.screenshot({ path: `shots/${name}-before.png` });

      // --- with Zone WAF Edit: switched on, custom rules
      await token('account=1&waf=1');
      await card.locator('[data-edge-action=enable]').click();
      await card.locator('[data-edge-state-line=on]').waitFor({ timeout: 30000 }).catch(() => {});
      expect(await card.locator('[data-edge-state-line=on]').count() === 1, `${name}: switched on with Zone WAF Edit`);
      const proven = await card.locator('[data-edge-zone]').first().innerText().catch(() => '');
      expect(/custom rules of the domain \(WAF\)/.test(proven), `${name}: the card says blocks are custom rules (${proven.slice(0, 90)})`);
      expect(!((await custom()).rules || []).some((r) => /test/.test(r.description)), `${name}: the test rule is gone`);
      await card.screenshot({ path: `shots/${name}-after.png` });

      // --- the dashboard: block an address into Cosmos' custom rule
      await page.goto(base + '/cosmos-ui/security', { waitUntil: 'domcontentloaded' });
      const button = page.locator('[data-edge-block="198.51.100.77"]');
      await button.waitFor({ timeout: 30000 }).catch(() => {});
      expect(await button.count() === 1, `${name}: "Block at Cloudflare" on the busiest addresses`);
      await noSideScroll(page, name, 'the security dashboard');
      if (await button.count() === 1) {
        await button.click();
        const dialog = page.getByRole('dialog');
        await dialog.waitFor();
        await dialog.getByRole('radio', { name: 'One hour' }).click();
        if (size === 'm') {
          const small = await smallTargets(page, '[role=dialog]');
          expect(small.length === 0, `${name}: the dialog's targets are 44 px (${small.join('; ')})`);
        }
        await dialog.screenshot({ path: `shots/${name}-block-dialog.png` });
        await dialog.locator('[data-edge-block-confirm]').click();
        await dialog.waitFor({ state: 'detached', timeout: 15000 }).catch(() => {});
        const made = await waitFor(async () => {
          const rule = await cosmosRule('block');
          return rule && rule.expression.includes('198.51.100.77');
        });
        expect(made, `${name}: the address is in Cosmos' custom rule at the stand-in`);
        await page.goto(base + '/cosmos-ui/security', { waitUntil: 'domcontentloaded' }).catch(() => {});
        const chip = page.locator('[data-edge-chip="198.51.100.77"]').first();
        await chip.waitFor({ timeout: 20000 }).catch(() => {});
        expect(await chip.count() === 1, `${name}: the dashboard shows the block`);
        await chip.scrollIntoViewIfNeeded().catch(() => {});
        await page.screenshot({ path: `shots/${name}-dashboard.png` });
      }

      // --- the list: "Unblock", then switching off
      await page.goto(base + '/cosmos-ui/config-general/https', { waitUntil: 'domcontentloaded' });
      await card.waitFor({ timeout: 30000 });
      await page.waitForTimeout(1200);
      const unblock = card.locator('[data-edge-unblock="198.51.100.77"]');
      await unblock.scrollIntoViewIfNeeded().catch(() => {});
      if (size === 'm') {
        const small = await smallTargets(page, '[data-edge-card] .MuiTable-root');
        expect(small.length === 0, `${name}: the list's buttons are 44 px high on a phone (${small.join('; ')})`);
      }
      expect(/custom rules of the domain/.test(await card.innerText()), `${name}: the list says how the block is made`);
      await card.screenshot({ path: `shots/${name}-list.png` });
      await unblock.click();
      await page.getByRole('dialog').getByRole('button', { name: /confirm/i }).click();
      expect(await waitFor(async () => !(await cosmosRule('block'))), `${name}: "Unblock" removes Cosmos' custom rule (it held one address)`);
      await card.locator('[data-edge-action=disable]').click();
      await card.locator('[data-edge-state-line=off]').waitFor({ timeout: 30000 }).catch(() => {});
      const rest = (await custom()).rules || [];
      expect(rest.every((r) => !/cosmos-edge:/.test(r.description)) && rest.some((r) => r.description === "the owner's rule"), `${name}: switched off; the owner's custom rule untouched`);
      await noSideScroll(page, name, 'the Domains tab');
      expect(violations.length === 0, `${name}: no script policy violation (${violations.slice(0, 2).join(' | ')})`);
      await browser.close();
    }
  }
}

note(failures.length ? `\n${failures.length} FAILED:\n` + failures.join('\n') : '\nall passed');
process.exit(failures.length ? 1 : 0);

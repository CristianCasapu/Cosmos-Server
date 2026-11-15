// Fork addition: the card "Block abusers at Cloudflare" and the dashboard's
// "Block at Cloudflare" in headless Chromium, Firefox and WebKit, at
// 1366x900 and 390x844, light and dark, against a throwaway Cosmos and the
// stand-in for Cloudflare's API (scripts/fork-fake-cloudflare.go). Nothing is
// sent to Cloudflare: the test network is made with --internal.
//
// What it expects (set up as in scripts/fork-ui-check-cloudflare.mjs):
//   - a network made with `docker network create --internal`, the stand-in on it
//     with the alias api.cloudflare.com (FAKE=http://<it>:8080, its control port);
//   - a Cosmos set up (admin / PASSWORD) at SERVER_IP with HTTPS on 2053, the domain
//     example.org with the stand-in's GoodToken, its panel name shielded, the token
//     allowed to manage firewall rules (GET <FAKE>/token?firewall=1), and the feature
//     switched on with a few blocks made through the stand-in's edge role (its
//     header says how), so that the list and the dashboard have something to show;
//   - this script run from the Playwright image on that network, from an address of
//     the local network (it signs in by the server's IP address).
// The server sends its own script policy: any violation is a failure.
// Screenshots go to ./shots. ENGINES=chromium,firefox,webkit SIZES=d,m MODES=light,dark.
import { chromium, firefox, webkit, request } from 'playwright';

const ip = process.env.SERVER_IP || '172.31.71.10';
const base = `https://${ip}:2053`;
const fake = process.env.FAKE || 'http://172.31.71.5:8080';
const password = process.env.PASSWORD || 'Test-Passw0rd!x';
const engines = { chromium, firefox, webkit };
const viewports = { d: { width: 1366, height: 900 }, m: { width: 390, height: 844 } };
const pick = (name, all) => (process.env[name] ? process.env[name].split(',') : all);

const failures = [];
const note = (...a) => console.log(...a);
const expect = (ok, what) => { note(ok ? '  ok  ' : '  FAIL', what); if (!ok) failures.push(what); };
const api = await request.newContext({ ignoreHTTPSErrors: true });
const rulesAt = async () => (await (await api.get(fake + '/rules')).json()) || [];

const signIn = async (page) => {
  await page.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password }, headers: { Origin: base, 'Sec-Fetch-Site': 'same-origin' } });
  await page.request.post(base + '/cosmos/api/sudo', { data: { password }, headers: { Origin: base, 'Sec-Fetch-Site': 'same-origin' } });
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

let acted = false;
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
        const text = m.text();
        if (/Content Security Policy|Content-Security-Policy|violates|Refused to (load|execute|frame)/i.test(text)) violations.push(text.slice(0, 300));
      });
      await signIn(page);

      // --- the card
      await page.goto(base + '/cosmos-ui/config-general/https', { waitUntil: 'domcontentloaded' });
      const card = page.locator('[data-edge-card]');
      await card.waitFor({ timeout: 30000 });
      await page.waitForTimeout(1500);
      await card.scrollIntoViewIfNeeded();
      expect(await card.locator('[data-edge-state-line=on]').count() === 1, `${name}: the card says it is on`);
      expect(await card.locator('[data-edge-rule]').count() > 0, `${name}: the list shows what is blocked`);
      await card.getByRole('button', { name: 'What leads to a block, and what never is blocked' }).click();
      await page.waitForTimeout(400);
      const text = await card.innerText();
      expect(/Failed sign-ins: 5 within an hour/.test(text), `${name}: the thresholds in words`);
      expect(/administrator signed in from in the last 7 days/.test(text), `${name}: the exemptions in words`);
      await noSideScroll(page, name, 'the Domains tab');
      if (size === 'm') {
        const small = await smallTargets(page, '[data-edge-card] .MuiTable-root');
        expect(small.length === 0, `${name}: the list's buttons are 44 px high on a phone (${small.join('; ')})`);
      }
      await card.screenshot({ path: `shots/${name}-card.png` });

      // once: unblock one line from the list (with the confirmation)
      if (!acted && engineName === 'chromium' && size === 'd' && mode === 'light') {
        const before = await rulesAt();
        const target = await card.locator('[data-edge-unblock]').first().getAttribute('data-edge-unblock');
        await card.locator(`[data-edge-unblock="${target}"]`).click();
        await page.getByRole('dialog').getByRole('button', { name: /confirm/i }).click();
        let gone = false;
        for (let i = 0; i < 30 && !gone; i++) {
          await page.waitForTimeout(1000);
          gone = !(await rulesAt()).some((r) => r.configuration.value === target);
        }
        expect(before.some((r) => r.configuration.value === target) && gone, `${name}: "Unblock" deletes ${target} at Cloudflare`);
      }

      // --- the dashboard
      await page.goto(base + '/cosmos-ui/security', { waitUntil: 'domcontentloaded' });
      const blockButton = page.locator('[data-edge-block]').first();
      await blockButton.waitFor({ timeout: 30000 }).catch(() => {});
      expect(await page.locator('[data-edge-block]').count() > 0, `${name}: the busiest addresses offer "Block at Cloudflare"`);
      await page.waitForTimeout(800);
      await noSideScroll(page, name, 'the security dashboard');
      await page.screenshot({ path: `shots/${name}-dashboard.png`, fullPage: true });
      if (await page.locator('[data-edge-chip]').count() > 0) {
        await page.locator('[data-edge-chip]').first().scrollIntoViewIfNeeded();
        await page.screenshot({ path: `shots/${name}-dashboard-chip.png` });
      }
      // the administrator's own (recent) address is refused, with the reason
      const adminButton = page.locator('[data-edge-block="198.51.100.150"]');
      if (await adminButton.count() > 0) {
        await adminButton.click();
        const dialog = page.getByRole('dialog');
        await dialog.waitFor();
        await noSideScroll(page, name, 'the block dialog');
        await dialog.screenshot({ path: `shots/${name}-block-dialog.png` });
        await dialog.locator('[data-edge-block-confirm]').click();
        await dialog.locator('[data-edge-block-error]').waitFor({ timeout: 15000 }).catch(() => {});
        const refusal = await dialog.locator('[data-edge-block-error]').innerText().catch(() => '');
        expect(/an administrator signed in from it lately/.test(refusal), `${name}: an administrator's address is refused with the reason (${refusal.slice(0, 80)})`);
        await dialog.screenshot({ path: `shots/${name}-block-refused.png` });
        await dialog.getByRole('button', { name: /cancel/i }).click();
      } else {
        expect(false, `${name}: the administrator's address is in the busiest list`);
      }
      expect(violations.length === 0, `${name}: no script policy violation (${violations.slice(0, 2).join(' | ')})`);
      acted = acted || (engineName === 'chromium' && size === 'd' && mode === 'light');
      await browser.close();
    }
  }
}

note(failures.length ? `\n${failures.length} FAILED:\n` + failures.join('\n') : '\nall passed');
process.exit(failures.length ? 1 : 0);

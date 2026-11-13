// Fork addition: like fork-ui-check.mjs, for the pages that need containers
// (ServApps action bar, "New URL" dialog, progress line) and for the backup
// settings. The test instance must be connected to a Docker of its own (see
// CLAUDE.md, "Checking the UI in a browser").
// BASE=http://<container>:<port> points it at an instance with another name;
// IMAGE names an image that is present in the test daemon (for the new container form).
import { chromium } from 'playwright';
const base = process.env.BASE || 'http://cosmos-smoke:18080';
const image = process.env.IMAGE || 'nginx:alpine';
const note = (...a) => console.log(...a);
const browser = await chromium.launch();
const ctx = await browser.newContext({ viewport: { width: 1366, height: 900 } });
const page = await ctx.newPage();
const errors = [];
page.on('pageerror', (e) => errors.push(page.url().replace(base, '') + ': ' + e.message.slice(0, 200)));
const J = { headers: { 'Content-Type': 'application/json' } };
await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: 'Test-Passw0rd!x' } });
await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: 'Test-Passw0rd!x' } });

// a domain and mDNS publishing, so both kinds of address can be offered
let r = await ctx.request.put(base + '/cosmos/api/zones/example.org', { data: { Zone: 'example.org', HTTPSCertificateMode: 'DISABLED' } });
note("add domain:", r.status(), (await r.text()).slice(0, 120)); await page.waitForTimeout(9000);
let config = (await (await ctx.request.get(base + '/cosmos/api/config')).json()).data;
config.HTTPConfig.PublishMDNS = true;
r = await ctx.request.put(base + '/cosmos/api/config', { data: config });
note("enable mDNS publishing:", r.status()); await page.waitForTimeout(6000);
await page.waitForTimeout(4000);

// --- ServApps: tooltips on the action bar
await page.goto(base + '/cosmos-ui/servapps', { waitUntil: 'domcontentloaded' });
await page.waitForTimeout(5000);
await page.screenshot({ path: 'shots/servapps.png' });
const card = page.locator('div', { hasText: /^jellyfin/ }).first();
const buttons = page.locator('button:has(svg), button:has(.anticon)').filter({ hasNotText: /\w/ });
const n = await buttons.count();
const tips = [];
for (let i = 0; i < Math.min(n, 24); i++) {
  const b = buttons.nth(i);
  if (!(await b.isVisible())) continue;
  await b.hover({ timeout: 2000 }).catch(() => {});
  await page.waitForTimeout(450);
  const tip = await page.locator('[role=tooltip]').last().innerText({ timeout: 500 }).catch(() => 'NONE');
  tips.push(tip);
  await page.mouse.move(5, 300);
  await page.waitForTimeout(150);
}
note('icon buttons on the page:', n, '| tooltips:', tips.join(' / '));

// --- a slow command: restart a container (busybox ignores the stop signal for 10 s)
await page.mouse.move(5, 300);
const restart = buttons.nth(6);
await restart.hover(); await page.waitForTimeout(600);
note('restart button tooltip:', await page.locator('[role=tooltip]').last().innerText().catch(() => 'NONE'));
await restart.click();
await page.waitForTimeout(3500);
const running = await page.locator('[role=status]').allInnerTexts();
note('while it runs:', running.map((s) => s.replace(/\n/g, ' | ')).join(' || ') || 'NOTHING SHOWN');
await page.screenshot({ path: 'shots/servapps-running.png' });
await page.waitForTimeout(11000);
note('when it ends:', (await page.locator('[role=status]').allInnerTexts()).map((s) => s.replace(/\n/g, ' | ')).join(' || ') || 'NOTHING SHOWN');
await page.screenshot({ path: 'shots/servapps-done.png' });

// --- "New URL" for a container: local address by default, a domain attached on request
await page.waitForTimeout(4500);
const newButtons = page.locator('.MuiChip-root', { hasText: /^New/ });
note('"New" buttons:', await newButtons.count());
await newButtons.last().click();
await page.waitForTimeout(2500);
note('default host:', await page.locator('input[name=Host]').inputValue().catch(() => '?'), '| target:', await page.locator('input[name=Target], input[name=Target-preview]').first().inputValue().catch(() => '?'));
const chip = page.locator('.MuiChip-root', { hasText: '.example.org' }).last();
note('domain chip:', await chip.innerText().catch(() => 'NONE'));
await chip.click().catch(() => {});
await page.waitForTimeout(400);
await chip.scrollIntoViewIfNeeded().catch(() => {});
await page.screenshot({ path: 'shots/new-url.png' });
await page.getByRole('button', { name: 'Confirm' }).click();
await page.waitForTimeout(8000);
note('errors in dialog:', await page.locator('[role=dialog] .MuiAlert-standardError').innerText().catch(() => 'none'));
config = (await (await ctx.request.get(base + '/cosmos/api/config')).json()).data;
note('routes now:', JSON.stringify((config.HTTPConfig.ProxyConfig.Routes || []).map((x) => [x.Name, x.Host, x.Target])));

// --- settings: backup schedule and directory
await page.goto(base + '/cosmos-ui/config-general', { waitUntil: 'domcontentloaded' });
await page.waitForTimeout(3500);
const dir = page.locator('input[name=BackupOutputDir]');
await dir.fill('relative/path'); await dir.blur();
await page.getByRole('button', { name: /^Save$/ }).first().click();
await page.waitForTimeout(1200);
note('form says:', (await page.locator('body').innerText()).match(/Enter an absolute path[^\n]*/)?.[0] || 'NO VALIDATION MESSAGE');
await dir.fill('/proc/cosmos-test/not-possible'); await dir.blur();
await page.getByRole('button', { name: /^Save$/ }).first().click();
await page.waitForTimeout(2500);
note('server refuses:', (await page.locator('[role=status]').allInnerTexts()).join(' || ').replace(/\n/g, ' | ').slice(0, 260) || 'NOTHING SHOWN');
await page.waitForTimeout(9500);
await dir.fill('/tmp/cosmos-test-backups//nested/dir/'); await dir.blur();
await page.locator('#BackupFrequency, [name=BackupFrequency]').first().scrollIntoViewIfNeeded().catch(() => {});
await page.screenshot({ path: 'shots/backup-settings.png' });
await page.getByRole('button', { name: /^Save$/ }).first().click();
await page.waitForTimeout(2500);
note('server accepts:', (await page.locator('[role=status]').allInnerTexts()).join(' || ').replace(/\n/g, ' | ').slice(0, 200) || 'NOTHING SHOWN');
await page.waitForTimeout(5000);
config = (await (await ctx.request.get(base + '/cosmos/api/config')).json()).data;
note('stored: dir', config.BackupOutputDir, '| frequency', config.BackupFrequency, '| time', config.BackupTime);

// --- ServApps > Start ServApp: the new container form, step by step, at desktop and phone width
for (const [name, viewport] of [['desktop', { width: 1366, height: 900 }], ['phone', { width: 390, height: 844 }]]) {
  await page.setViewportSize(viewport);
  await page.goto(base + '/cosmos-ui/servapps/new-service', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(3500);
  const next = async () => { await page.getByRole('button', { name: /Next/ }).first().click(); await page.waitForTimeout(1200); await page.evaluate(() => window.scrollTo(0, 0)); };
  const shot = (step) => page.screenshot({ path: `shots/new-container-${name}-${step}.png` });
  // the mandatory fields carry an asterisk and say so when left empty
  note(name, 'labels:', (await page.locator('label').allInnerTexts()).slice(0, 2).map((l) => l.replace(/\s+/g, ' ')).join(' | '));
  await page.locator('#name').click(); await page.locator('#image').click(); await page.locator('#user').click();
  await page.waitForTimeout(400);
  note(name, 'left empty:', (await page.locator('.MuiFormHelperText-root.Mui-error').allInnerTexts()).join(' | ') || 'NO MESSAGE', '| Next enabled:', await page.getByRole('button', { name: /Next/ }).first().isEnabled());
  await page.locator('#name').fill('check-' + name);
  await page.locator('#image').fill(image); await page.locator('#user').click();
  await page.waitForTimeout(1500);
  // an image that is on the server: its defaults are offered, and adding them changes nothing typed before
  const offer = page.getByRole('button', { name: /Use the image's defaults/ });
  note(name, 'defaults offered:', (await page.locator('.MuiAlert-root', { has: offer }).innerText().catch(() => 'NO OFFER')).replace(/\n+/g, ' / ').slice(0, 300));
  await shot('docker');
  if (await offer.count()) {
    await offer.click(); await page.waitForTimeout(600);
    note(name, 'after adding them: name', await page.locator('#name').inputValue(), '| image', await page.locator('#image').inputValue(), '| variables', await page.locator('input[name^="envVars."]').count() / 2);
  }
  await next();
  await page.locator('input[type=checkbox]').first().check();
  await page.waitForTimeout(1500);
  const proposals = page.locator('form .MuiChip-clickable');
  note(name, 'URL step: host', await page.locator('input[name=Host]').inputValue().catch(() => '?'), '| target', await page.locator('input[name=Target]').inputValue().catch(() => '?'), '| proposals:', (await proposals.allInnerTexts()).join(' , ') || 'NONE');
  await page.locator('input[name=Host]').evaluate((el) => el.scrollIntoView({ block: 'center' }));
  await shot('url');
  await page.locator('input[name=Host]').fill(''); await page.locator('input[name=Host]').blur(); await page.waitForTimeout(500);
  note(name, 'URL step, host emptied:', (await page.locator('.MuiFormHelperText-root.Mui-error').allInnerTexts()).join(' | ') || 'NO MESSAGE');
  await proposals.first().click().catch(() => {}); await page.waitForTimeout(700);
  await next(); await shot('network');
  await next(); await shot('storage');
  await next(); await page.waitForTimeout(800); await shot('review');
  note(name, 'review reached:', await page.getByRole('button', { name: /Create/ }).count() > 0, '| overflow sideways:', await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1));
}
note('page errors:', [...new Set(errors)].join(' ; ') || 'none');
await browser.close();

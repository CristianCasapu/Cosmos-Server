// Fork addition: checks, in real browsers, that the end of the password
// confirmation ("sudo") in the middle of work loses nothing that was typed.
//
// Needs a throwaway instance (CLAUDE.md, "Checking the UI in a browser")
// whose server was built with a SHORT confirmation: in a scratch copy of the
// sources set SudoDuration in src/user/fork_session_life.go to the seconds
// given here as SUDO_SECONDS (default 50), so the check does not take two
// hours. Never point this at the production instance.
//
//   BASE=http://<dotted name of the instance>:<port> SUDO_SECONDS=50 node fork-ui-check-sudo.mjs
import { chromium, firefox, webkit } from 'playwright';

const base = process.env.BASE || 'http://cosmos-sessions.smoke:18080';
const sudoSeconds = parseInt(process.env.SUDO_SECONDS || '50', 10);
const password = 'Test-Passw0rd!x';
const problems = [];
const note = (...a) => console.log(...a);
const expect = (name, what, ok) => { note(ok ? 'ok   ' : 'FAIL ', name + ':', what); if (!ok) problems.push(`${name}: ${what}`); };

const typed = 'typed-before-the-end-0420';

const run = async (type, name, viewport) => {
  const browser = await type.launch();
  const ctx = await browser.newContext({ viewport, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const scriptErrors = [];
  let loads = 0;
  page.on('pageerror', (e) => scriptErrors.push(e.message.slice(0, 200)));
  page.on('load', () => { loads++; });

  let r = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password } });
  expect(name, 'login ' + r.status(), r.status() === 200);
  r = await ctx.request.post(base + '/cosmos/api/sudo', { data: { password } });
  const confirmedAt = Date.now();
  expect(name, 'password confirmed ' + r.status(), r.status() === 200);

  await page.goto(base + '/cosmos-ui/config-general', { waitUntil: 'domcontentloaded' });
  const field = page.locator('input[name=MaintenanceTime]');
  await field.waitFor({ timeout: 20000 });
  await page.waitForTimeout(1500);
  const save = page.getByRole('button', { name: /^Save$/ }).first();
  const chip = page.locator('header').getByText('Admin', { exact: true });
  const guard = page.locator('span[style*="cursor: pointer"]').filter({ has: page.getByRole('button', { name: /^Save$/ }) }).first();
  expect(name, 'while confirmed: Save is usable, no "Admin" chip', await save.isEnabled() && await guard.count() === 0 && await chip.count() === 0);

  // the admin is in the middle of a form
  const notes = page.locator('input[name=BackupOutputDir]');
  const target = (await notes.count()) ? notes : field;
  const before = await target.inputValue();
  await target.fill((await notes.count()) ? '/tmp/' + typed + '-' + name : '04:20'); // differs from what is stored, so a reload would show
  const entered = await target.inputValue();
  const loadsBefore = loads;

  // --- 1. the confirmation ends while the page is open: the page shows it by itself
  const wait = Math.max(0, confirmedAt + sudoSeconds * 1000 - Date.now()) + 2500;
  note(name, `waiting ${Math.round(wait / 1000)} s for the confirmation to end...`);
  await page.waitForTimeout(wait);
  expect(name, 'at the end the "Admin" chip appears and Save greys out, without anything being clicked', await chip.count() === 1 && await guard.count() === 1);
  expect(name, 'what was typed is still in the field', await target.inputValue() === entered);
  await page.screenshot({ path: `shots/${name}-1-ended.png` });

  // --- 2. a click on the greyed Save asks for the password right there
  await guard.scrollIntoViewIfNeeded();
  await guard.click({ position: { x: 12, y: 12 } });
  const dialog = page.locator('[data-sudo-prompt]');
  await dialog.waitFor({ timeout: 5000 }).catch(() => {});
  expect(name, 'the password prompt opens from the greyed Save button', await dialog.count() === 1);
  const dialogText = (await dialog.innerText().catch(() => '')).replace(/\n/g, ' | ');
  note(name, 'prompt:', dialogText.slice(0, 400));
  expect(name, 'the prompt says that what was entered stays', /stays as it is/.test(dialogText));
  await page.screenshot({ path: `shots/${name}-2-prompt.png` });
  const box = await dialog.boundingBox();
  expect(name, `the prompt fits the screen (${box ? Math.round(box.x) + '..' + Math.round(box.x + box.width) : 'none'} of ${viewport.width})`, !!box && box.x >= 0 && box.x + box.width <= viewport.width);
  const sideways = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(name, 'no sideways page scroll (' + sideways + ')', sideways <= 1);

  // a wrong password keeps everything too
  await dialog.locator('input[name=password]').fill('Wrong-Passw0rd!x');
  await dialog.getByRole('button', { name: 'Submit' }).click();
  await page.waitForTimeout(3500);
  expect(name, 'a wrong password: the prompt stays and says so', await dialog.count() === 1 && /Invalid password/.test(await dialog.innerText()));

  await dialog.locator('input[name=password]').fill(password);
  await dialog.getByRole('button', { name: 'Submit' }).click();
  await dialog.waitFor({ state: 'detached', timeout: 8000 }).catch(() => {});
  await page.waitForTimeout(1200);
  const confirmedAgainAt = Date.now();
  expect(name, 'after the password the prompt closes', await dialog.count() === 0);
  expect(name, 'the page was not loaded again', loads === loadsBefore);
  expect(name, 'what was typed is still in the field after the password', await target.inputValue() === entered);
  expect(name, 'Save is usable again and the chip is gone', await save.isEnabled() && await chip.count() === 0 && await guard.count() === 0);
  await page.screenshot({ path: `shots/${name}-3-confirmed-again.png` });

  // --- 3. the page believes it is confirmed, the server knows better: the refused command asks
  // (a browser whose clock is behind, or a tab that was asleep: here the readable cookie is set ahead)
  const cookies = await ctx.cookies(base);
  const infos = cookies.find((c) => c.name === 'client-infos');
  const parts = decodeURIComponent(infos.value).split(',');
  parts[2] = String(Math.floor(Date.now() / 1000) + 7200);
  await ctx.addCookies([{ ...infos, value: parts.join(',') }]);
  const wait2 = Math.max(0, confirmedAgainAt + sudoSeconds * 1000 - Date.now()) + 2500;
  note(name, `waiting ${Math.round(wait2 / 1000)} s for the second confirmation to end on the server...`);
  await page.waitForTimeout(wait2);
  expect(name, 'the page still shows Save as usable (it cannot know)', await guard.count() === 0 && await chip.count() === 0);
  const loadsBeforeSave = loads;
  await save.click();
  await dialog.waitFor({ timeout: 8000 }).catch(() => {});
  expect(name, 'the refused Save opens the password prompt', await dialog.count() === 1);
  const refusedText = (await dialog.innerText().catch(() => '')).replace(/\n/g, ' | ');
  expect(name, 'the prompt says the action was not carried out and to repeat it', /did not carry out your last action/.test(refusedText) && /repeat the action/.test(refusedText));
  await page.screenshot({ path: `shots/${name}-4-refused.png` });
  await dialog.locator('input[name=password]').fill(password);
  await dialog.getByRole('button', { name: 'Submit' }).click();
  await dialog.waitFor({ state: 'detached', timeout: 8000 }).catch(() => {});
  await page.waitForTimeout(1200);
  expect(name, 'after the password: no reload, the typed value is still there', loads === loadsBeforeSave && await target.inputValue() === entered);

  // the action, repeated, is carried out
  const [answer] = await Promise.all([
    page.waitForResponse((res) => res.url().endsWith('/cosmos/api/config') && res.request().method() !== 'GET', { timeout: 15000 }).catch(() => null),
    save.click(),
  ]);
  expect(name, 'the repeated Save is accepted by the server (' + (answer ? answer.status() : 'no answer') + ')', !!answer && answer.status() === 200);
  await page.waitForTimeout(1500);
  await page.screenshot({ path: `shots/${name}-5-saved.png` });

  // put the value back (the instance restarts its HTTP server on a save; give it a moment)
  await page.waitForTimeout(4000);
  r = await ctx.request.get(base + '/cosmos/api/config');
  note(name, 'config readable after the save:', r.status(), '| field before the test:', JSON.stringify(before));

  expect(name, 'no script errors' + (scriptErrors.length ? ': ' + scriptErrors.join(' / ') : ''), scriptErrors.length === 0);
  await browser.close();
};

const only = process.env.ONLY || '';
const runs = [
  [chromium, 'chromium-desktop', { width: 1366, height: 900 }],
  [chromium, 'chromium-phone', { width: 390, height: 844 }],
  [firefox, 'firefox-desktop', { width: 1366, height: 900 }],
  [webkit, 'webkit-phone', { width: 390, height: 844 }],
];
for (const [type, name, viewport] of runs) {
  if (only && !name.includes(only)) continue;
  try {
    await run(type, name, viewport);
  } catch (e) {
    problems.push(`${name}: ${String(e).slice(0, 300)}`);
    note('FAIL ', name, String(e).slice(0, 300));
  }
}
note(problems.length ? `\n${problems.length} problem(s):\n- ${problems.join('\n- ')}` : '\nAll checks passed.');
process.exit(problems.length ? 1 : 0);

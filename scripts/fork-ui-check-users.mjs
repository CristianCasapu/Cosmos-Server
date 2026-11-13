// Fork addition: the dialogs of the Users page (pages/config/users/forkUserDialogs.jsx).
// ENGINES=chromium,firefox,webkit  SIZES=d,m  TAG=<name>
// The test instance must be connected to a Docker of its own (see CLAUDE.md,
// "Checking the UI in a browser"), reachable under a name with a dot (WebKit keeps
// no session cookie otherwise), with HTTPS switched off, and in that Docker, as the
// expectations below assume (nginx:alpine tagged with the images' names is enough):
//   radarr       image linuxserver/radarr       --expose 7878 -p 7878:80
//   sonarr       image linuxserver/sonarr       --expose 8989 -p 8989:80
//   jellyfin     image jellyfin/jellyfin        --expose 8096 -p 8096:80 -p 8920:443
//   qbittorrent  image linuxserver/qbittorrent  --expose 8080 -p 8085:8080
//   web1         image nginx:alpine             -p 8081:80
//   db1          image busybox (no port)
//   media-app    image nginx:alpine --expose 3000, labels cosmos-stack=media, cosmos-stack-main=media-app
//   media-db     image busybox, label cosmos-stack=media
// BASE=http://<name>:<port> points it at the instance (default http://forms2.test:18080).
// Screenshots go to ./shots (create it). The run ends with "ALL OK" or the list of failures.
import { chromium, firefox, webkit } from 'playwright';
const base = process.env.BASE || 'http://forms2.test:18080';
const tag = process.env.TAG || 'after';
const engines = { chromium, firefox, webkit };
const names = (process.env.ENGINES || 'chromium').split(',');
const sizes = { d: { width: 1366, height: 900 }, m: { width: 390, height: 844 } };
const wanted = (process.env.SIZES || 'd').split(',');
const note = (...a) => console.log(...a);
const failures = [];
const expect = (what, ok, detail) => { note(ok ? '  ok  ' : '  FAIL', what, detail === undefined ? '' : '| ' + String(detail).slice(0, 260)); if (!ok) failures.push(what); };
const users = async (ctx) => ((await (await ctx.request.get(base + '/cosmos/api/users')).json()).data || []);

for (const name of names) for (const size of wanted) {
  const id = name + '-' + size;
  const nick = ('t' + name + size).replace(/[^a-z0-9]/g, '');
  const browser = await engines[name].launch();
  const ctx = await browser.newContext({ viewport: sizes[size] });
  await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: 'Test-Passw0rd!x' } });
  await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: 'Test-Passw0rd!x' } });
  await ctx.request.delete(base + '/cosmos/api/users/' + nick);
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message.slice(0, 160)));
  note(`--- ${id}: users`);
  await page.goto(base + '/cosmos-ui/config-users', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(3500);
  await page.getByRole('button', { name: /Create/ }).first().click();
  await page.waitForTimeout(800);
  const dialog = page.locator('[role=dialog]').last();
  let text = await dialog.innerText();
  expect(id + ': the roles are User and Admin, each explained; no Guest the server does not know', /User/.test(text) && /Admin/.test(text) && !/Guest/.test(text) && /Cannot change anything in Cosmos/.test(text));
  expect(id + ': it says what happens next', /you get a link to pass on to the person/.test(text));
  // a nickname the server would refuse is refused under the field, in plain words
  await dialog.locator('#nickname').fill('jean.marc');
  await dialog.locator('#email').fill('jean@');
  await dialog.getByRole('button', { name: 'Create' }).click();
  await page.waitForTimeout(600);
  text = await dialog.innerText();
  expect(id + ': a nickname with a dot is refused in words', /Letters and digits only/.test(text));
  expect(id + ': and so is half an email address', /This is not an email address/.test(text));
  expect(id + ': nothing was sent', !(await users(ctx)).some((u) => u.nickname === 'jeanmarc' || u.nickname === 'jean.marc'));
  await dialog.locator('#nickname').fill('admin');
  await dialog.locator('#nickname').blur();
  await page.waitForTimeout(400);
  expect(id + ': a taken nickname is said to be taken', /This nickname is taken/.test(await dialog.innerText()));
  // a click beside the dialog keeps it and what was typed
  await page.mouse.click(3, 3);
  await page.waitForTimeout(400);
  expect(id + ': a click beside the dialog keeps what was typed', await dialog.isVisible() && await dialog.locator('#email').inputValue() === 'jean@');
  expect(id + ': the dialog fits', await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth) <= 0 && await dialog.evaluate((el) => { const c = el.querySelector('.MuiDialogContent-root'); return c.scrollWidth - c.clientWidth; }) <= 0);
  await page.screenshot({ path: `shots/${tag}-user-create-${id}.png` });
  await dialog.locator('#nickname').fill(nick);
  await dialog.locator('#email').fill(nick + '@example.org');
  await page.waitForTimeout(300);
  expect(id + ': with an address but no mail set up, it says no email is sent', /No email is sent: sending mail is not set up/.test(await dialog.innerText()));
  await dialog.locator('#email').fill('');
  await dialog.getByRole('button', { name: 'Create' }).click();
  await page.waitForTimeout(4000);
  const made = (await users(ctx)).find((u) => u.nickname === nick);
  expect(id + ': the user is created as User', !!made && made.role === 1, JSON.stringify(made || {}).slice(0, 120));
  // the link to pass on
  const link = page.locator('[role=dialog]').last();
  text = await link.innerText();
  const value = await link.locator('input[readonly]').inputValue().catch(() => '');
  expect(id + ': the link is shown, to pass on', new RegExp('Invitation for ' + nick).test(text) && /No email was sent\. Pass this link on/.test(text) && value.includes('/cosmos-ui/register?t=2&nickname=' + nick + '&key='), value.slice(0, 80));
  expect(id + ': the link dialog fits', await link.evaluate((el) => { const c = el.querySelector('.MuiDialogContent-root'); return c.scrollWidth - c.clientWidth; }) <= 0 && await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth) <= 0);
  await page.screenshot({ path: `shots/${tag}-user-link-${id}.png` });
  await link.getByRole('button', { name: 'Close' }).click();
  await page.waitForTimeout(2500);

  // edit: the role as a choice; the own account's role cannot be changed
  if (sizes[size].width > 600) {
    await page.locator('tbody tr', { hasText: nick }).locator('td').first().click();
    await page.waitForTimeout(800);
    const edit = page.locator('[role=dialog]').last();
    expect(id + ': edit names the user', new RegExp('Edit ' + nick).test(await edit.innerText()));
    await edit.locator('.MuiChip-root[role=radio]', { hasText: 'Admin' }).click();
    await edit.locator('#email').fill(nick + '@example.org');
    await edit.getByRole('button', { name: 'Save' }).click();
    await page.waitForTimeout(3000);
    const edited = (await users(ctx)).find((u) => u.nickname === nick);
    expect(id + ': role and address are saved', !!edited && edited.role === 2 && edited.email === nick + '@example.org', JSON.stringify(edited || {}).slice(0, 120));
    await page.locator('tbody tr', { hasText: 'admin' }).first().locator('td').first().click();
    await page.waitForTimeout(800);
    const own = page.locator('[role=dialog]').last();
    expect(id + ': the own account\'s role is not a choice, with the reason', /another administrator has to change its role/.test(await own.innerText()) && await own.locator('.MuiChip-root[role=radio][aria-disabled=true], .MuiChip-root.Mui-disabled').count() >= 2);
    await own.getByRole('button', { name: 'Cancel' }).click();
    await page.waitForTimeout(500);
    // the two-factor reset asks first
    await page.locator('tbody tr', { hasText: nick }).getByRole('button', { name: /Reset 2FA/i }).click();
    await page.waitForTimeout(600);
    const ask = page.locator('[role=dialog]').last();
    expect(id + ': the two-factor reset asks first', new RegExp('Reset the two-factor sign-in of ' + nick).test(await ask.innerText()));
    await ask.getByRole('button', { name: 'Cancel' }).click();
    await page.waitForTimeout(400);
    // cancelling a delete gives the row its buttons back
    await page.locator('tbody tr', { hasText: nick }).getByRole('button', { name: 'Delete' }).click();
    await page.waitForTimeout(500);
    await page.locator('[role=dialog]').last().getByRole('button', { name: 'Cancel' }).click();
    await page.waitForTimeout(600);
    expect(id + ': cancelling a delete gives the row its buttons back', await page.locator('tbody tr', { hasText: nick }).getByRole('button', { name: 'Delete' }).count() === 1);
  }
  const gone = await ctx.request.delete(base + '/cosmos/api/users/' + nick);
  expect(id + ': clean up', gone.status() === 200, gone.status());
  expect(id + ': no script errors', errors.length === 0, [...new Set(errors)].join(' | '));
  await browser.close();
}
note(failures.length ? 'FAILURES: ' + failures.length + '\n' + failures.join('\n') : 'ALL OK');

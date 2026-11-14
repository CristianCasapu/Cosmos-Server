// Fork addition: the scheduled-job dialog (pages/cron/newJob.jsx): a job needs a name
// and a command, cannot take the name of another job, and is saved once. Deletes
// the scheduled jobs of the test instance.
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
const jobs = async (ctx) => ((await (await ctx.request.get(base + '/cosmos/api/config')).json()).data.CRON || {});
const dropJobs = async (ctx) => {
  const config = (await (await ctx.request.get(base + '/cosmos/api/config')).json()).data;
  if (config.CRON && Object.keys(config.CRON).length) { config.CRON = {}; await ctx.request.put(base + '/cosmos/api/config', { data: config }); await new Promise((s) => setTimeout(s, 6000)); }
};

for (const name of names) for (const size of wanted) {
  const id = name + '-' + size;
  const browser = await engines[name].launch();
  const ctx = await browser.newContext({ viewport: sizes[size] });
  await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: 'Test-Passw0rd!x' } });
  await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: 'Test-Passw0rd!x' } });
  await dropJobs(ctx);
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message.slice(0, 160)));
  if (process.env.SHOW_FAILED) page.on('requestfailed', (r) => note('   request failed:', r.method(), r.url().replace(base, ''), r.failure() && r.failure().errorText));
  let saves = 0;
  page.on('request', (r) => { if (r.method() === 'PUT' && r.url().endsWith('/cosmos/api/config')) saves += 1; });
  note(`--- ${id}: scheduled job`);
  const open = async () => {
    await page.goto(base + '/cosmos-ui/cron', { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(3500);
    await page.locator('button:has(.anticon-plus)').first().click();
    await page.waitForTimeout(1500);
    return page.locator('[role=dialog]').last();
  };
  let dialog = await open();
  expect(id + ': a new job is called a new job', /New Job/.test(await dialog.locator('h2').first().innerText()), await dialog.locator('h2').first().innerText());
  expect(id + ': it says where the command runs', /Runs on the server itself, with the rights of Cosmos/.test(await dialog.innerText()));
  await dialog.locator('#Crontab').fill('0 0 3 * * *');
  await dialog.getByRole('button', { name: 'Submit' }).click();
  await page.waitForTimeout(1200);
  let text = await dialog.innerText();
  expect(id + ': a job without a name is not saved, and the field says so', /Give the job a name/.test(text) && saves === 0, saves);
  expect(id + ': nor one with nothing to run', /Enter the command to run/.test(text));
  await page.mouse.click(3, 3);
  await page.waitForTimeout(400);
  expect(id + ': a click beside the dialog keeps what was typed', await dialog.isVisible() && await dialog.locator('#Crontab').inputValue() === '0 0 3 * * *');
  expect(id + ': the dialog fits', await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth) <= 0);
  await page.screenshot({ path: `shots/${tag}-cronjob-${id}.png` });
  await dialog.locator('#Name').fill('Night list');
  await dialog.locator('#Command').fill('ls -la /tmp');
  await dialog.getByRole('button', { name: 'Submit' }).click();
  await page.waitForTimeout(7000);
  let all = await jobs(ctx);
  expect(id + ': the list shows the new job without a reload', /Night list/.test(await page.locator('body').innerText()));
  expect(id + ': the job is saved, once', !!all['Night list'] && all['Night list'].Command === 'ls -la /tmp' && saves === 1, `${Object.keys(all).join(',')} saves ${saves}`);
  // a second job under the same name would replace the first
  dialog = await open();
  await dialog.locator('#Name').fill('Night list');
  await dialog.locator('#Crontab').fill('0 0 5 * * *');
  await dialog.locator('#Command').fill('echo other');
  await dialog.getByRole('button', { name: 'Submit' }).click();
  await page.waitForTimeout(2500);
  text = await dialog.innerText().catch(() => '');
  all = await jobs(ctx);
  expect(id + ': a second job cannot take the name of the first', /Another job is called "Night list"/.test(text) && all['Night list'].Command === 'ls -la /tmp' && saves === 1, `${text.replace(/\s+/g, " ").slice(0, 80)} | ${all['Night list'] && all['Night list'].Command} | saves ${saves}`);
  expect(id + ': and what was typed is still there', await dialog.locator('#Command').inputValue().catch(() => '') === 'echo other');
  await page.keyboard.press('Escape');
  await dropJobs(ctx);
  expect(id + ': no script errors', errors.length === 0, [...new Set(errors)].join(' | '));
  await browser.close();
}
note(failures.length ? 'FAILURES: ' + failures.length + '\n' + failures.join('\n') : 'ALL OK');

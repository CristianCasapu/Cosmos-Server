// Fork addition: the email (SMTP) settings form in a headless browser, against
// a throwaway instance and a mail provider that is made up
// (scripts/fork-fake-mail: its header says which domains it knows). Nothing
// here reaches a real mail server or a real name server: the instance's only
// name server is the fake one, on a Docker network without a way out.
//
//   docker network create --internal <net>
//   docker run -d --name <mail> --network <net> -v <dir with fake-mail>:/mail:ro debian:13 /mail/fake-mail
//   start the instance on <net> with --dns <address of <mail>> and a dotted
//   alias (WebKit keeps no cookie for a name without a dot), finish the
//   wizard (CLAUDE.md, "Checking the UI in a browser"), then run this from
//   the Playwright image on <net>:
//   BASE=http://<alias>:18080 MAIL=http://<mail>:8025 node fork-ui-check-email.mjs
//
// It types an address, reads the proposal, changes a field by hand, checks
// with a wrong and with the right password, saves, reloads and reads the
// saved state; then looks at the card in Firefox and WebKit and at phone
// width. Screenshots go to ./shots. The instance must be freshly set up
// (email not configured). ENGINES=chromium limits the engines.
import { chromium, firefox, webkit } from 'playwright';
import { mkdirSync } from 'node:fs';

const base = process.env.BASE || 'http://smtp-panel.test:18080';
const mail = process.env.MAIL || 'http://cosmos-smtp-mail:8025';
const engines = (process.env.ENGINES || 'chromium,firefox,webkit').split(',');
const ADDRESS = 'cosmos@fakemail.test';
const RIGHT = 'right-password';
mkdirSync('shots', { recursive: true });

const note = (...a) => console.log(...a);
const errors = [];
const failures = [];
const expect = (what, ok, detail) => {
  note(ok ? '  ok  ' : '  FAIL', what, detail === undefined ? '' : '| ' + String(detail).replace(/\s+/g, ' ').slice(0, 260));
  if (!ok) failures.push(what);
};
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const mailState = async (reset) => {
  const state = await (await fetch(mail + '/state', { method: reset ? 'DELETE' : 'GET' })).json();
  return { questions: state.questions || [], commands: state.commands || [], signIns: state.signIns || [], messages: state.messages || [] };
};

const open = async (type, name, viewport) => {
  const browser = await type.launch();
  const ctx = await browser.newContext({ viewport });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => errors.push(`${name} ${page.url().replace(base, '')}: ${e.message.slice(0, 200)}`));
  let r = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: 'Test-Passw0rd!x' } });
  const login = r.status();
  r = await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: 'Test-Passw0rd!x' } });
  note(name, 'login', login, 'sudo', r.status());
  return { browser, ctx, page };
};

// the card, and helpers that read and drive it
const card = (page) => page.locator('.MuiPaper-root', { has: page.locator('#Email_Address, input[name=Email_Enabled]') }).last();
const cardText = async (page) => (await card(page).innerText()).replace(/\s+/g, ' ');
const sideways = (page) => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
const shot = async (page, name) => {
  await card(page).scrollIntoViewIfNeeded();
  await card(page).screenshot({ path: `shots/${name}.png` });
};
const openSettings = async (page) => {
  await page.goto(base + '/cosmos-ui/config-general', { waitUntil: 'domcontentloaded' });
  await page.locator('input[name=Email_Enabled]').waitFor({ timeout: 20000 });
  await page.waitForTimeout(1500);
};
// the lookups are limited to twelve a minute for the whole server: keep a distance
let lastLookup = 0;
const typeAddress = async (page, address) => {
  const wait = lastLookup + 6500 - Date.now();
  if (wait > 0) await sleep(wait);
  lastLookup = Date.now();
  await page.locator('#Email_Address').fill(address);
  await page.locator('#Email_Address').blur();
  await page.getByText(/Looking for the mail servers/).waitFor({ state: 'hidden', timeout: 20000 }).catch(() => {});
  await page.waitForTimeout(600);
};
const lookAgain = async (page) => {
  const wait = lastLookup + 6500 - Date.now();
  if (wait > 0) await sleep(wait);
  lastLookup = Date.now();
  await page.getByRole('button', { name: 'Look again' }).click();
  await page.waitForTimeout(300);
  await page.getByText(/Looking for the mail servers/).waitFor({ state: 'hidden', timeout: 20000 }).catch(() => {});
  await page.waitForTimeout(600);
};
const openCustom = async (page) => {
  if (!(await page.locator('#Email_Host').count())) {
    await page.getByRole('button', { name: 'Custom settings' }).click();
    await page.locator('#Email_Host').waitFor();
    await page.waitForTimeout(400);
  }
};
const check = async (page) => {
  await page.getByRole('button', { name: 'Check and send a test email' }).click();
  await card(page).locator('.MuiAlert-standardSuccess, .MuiAlert-standardError').filter({ hasText: /It works|The check stopped|could not be sent/ })
    .waitFor({ timeout: 45000 });
  await page.waitForTimeout(300);
  return cardText(page);
};
const savedConfig = async (page) => page.evaluate(async () => (await (await fetch('/cosmos/api/config')).json()).data.EmailConfig);

// ---- the whole flow, in desktop Chromium --------------------------------------
if (engines.includes('chromium')) {
  const { browser, page } = await open(chromium, 'chromium', { width: 1366, height: 900 });
  await mailState(true);
  await openSettings(page);

  note('--- the card before email is set up');
  let text = await cardText(page);
  expect('says what Cosmos sends email for', /Cosmos sends email for:/.test(text) && /invitation link/.test(text) && /Forgot your password/.test(text) && /alerts whose action is "email"/.test(text), text);
  expect('asks for nothing while email is off', await page.locator('#Email_Address').count() === 0);
  await shot(page, 'chromium-1-off');
  await page.locator('input[name=Email_Enabled]').check();
  await page.locator('#Email_Address').waitFor();
  text = await cardText(page);
  expect('asks for the address, the password and a name', await page.locator('#Email_Address, #Email_Password, #Email_FromName').count() === 3);
  expect('server fields stay behind "Custom settings"', await page.locator('#Email_Host, #Email_Username, #Email_Port').count() === 0 && /No mail server yet/.test(text));
  expect('the check waits for an address and a server', await page.getByRole('button', { name: 'Check and send a test email' }).isDisabled());

  note('--- a known provider: the table answers, nothing answers from here (no way out)');
  await typeAddress(page, 'someone@gmail.com');
  text = await cardText(page);
  expect('Gmail is proposed from the table', /smtp\.gmail\.com, port 465, encrypted from the start \(TLS\)/.test(text) && /Known provider: Gmail/.test(text), text);
  expect('it says the server did not answer from here', /did not answer on port 465 from this server/.test(text));
  expect('the app password hint is shown with its address as text', /Gmail: what to know/.test(text) && /app password at https:\/\/myaccount\.google\.com\/apppasswords/.test(text));
  expect('no link is made of the address', await card(page).locator('a[href*="google"]').count() === 0);
  expect('the form took the proposal', /Sends through: smtp\.gmail\.com, port 465/.test(text) && /Signs in as: someone@gmail\.com/.test(text));
  await shot(page, 'chromium-2-gmail');

  note('--- another address: the untouched proposal is replaced');
  let before = await mailState();
  await typeAddress(page, ADDRESS);
  text = await cardText(page);
  expect('the fake provider is proposed from its DNS records', /smtp\.fakemail\.test, port 465, encrypted from the start \(TLS\)/.test(text) && /Announced by fakemail\.test in DNS \(SRV record\)/.test(text), text);
  expect('it answered, with a certificate that cannot be verified', /answered on port 465 just now, but its certificate cannot be verified/.test(text));
  expect('the form follows: server and user name', /Sends through: smtp\.fakemail\.test, port 465/.test(text) && new RegExp('Signs in as: ' + ADDRESS).test(text));
  await page.getByRole('button', { name: /Other possibilities \(3\)/ }).click();
  await page.waitForTimeout(500);
  text = await cardText(page);
  expect('other candidates are listed on request, each with its source', /smtp\.fakemail\.test, port 587, encrypted after connecting \(STARTTLS\)/.test(text) && /mx\.fakemail\.test, port 465/.test(text)
    && /The server that receives the mail of fakemail\.test · answered, certificate cannot be verified/.test(text), text);
  expect('the Gmail hint is gone', !/Gmail: what to know/.test(text));
  expect('it says nothing is saved', /not saved yet/.test(text));
  let state = await mailState();
  const commands = state.commands.slice(before.commands.length);
  expect('detection only greeted the mail server', commands.length >= 4 && commands.every((c) => /^(EHLO localhost|STARTTLS|QUIT)$/i.test(c)), commands.join(' / '));
  expect('detection signed in nowhere', state.signIns.length === 0 && state.messages.length === 0);
  expect('only the domain was looked up', state.questions.some((q) => /^MX fakemail\.test\.$/.test(q)) && !state.questions.some((q) => /cosmos/.test(q)), state.questions.slice(-12).join(' / '));
  await shot(page, 'chromium-3-proposal');

  note('--- check: the certificate is not trusted');
  await page.locator('#Email_Password').fill('wrong-password');
  text = await check(page);
  expect('the check stops at the first step and says why', /The check stopped at: Reach the mail server/.test(text) && /certificate of the mail server cannot be verified/.test(text) && /Accept a certificate that cannot be verified/.test(text), text.slice(text.indexOf('The check stopped')));
  expect('the mail server\'s own words are shown as a detail', /The mail server said:/.test(text));
  await shot(page, 'chromium-4-cert');
  state = await mailState();
  expect('no password was sent to a server whose certificate failed', state.signIns.length === 0);

  note('--- a field changed by hand is kept');
  await openCustom(page);
  expect('custom settings show every field', await page.locator('#Email_Host, #Email_Username, #Email_Service').count() === 3 && await page.getByRole('radio').count() >= 4);
  expect('port and encryption show as one choice', await page.getByRole('radio', { name: /^465/ }).getAttribute('aria-checked') === 'true');
  await page.locator('input[name=Email_AllowInsecureTLS]').check();
  await page.getByRole('radio', { name: /^587/ }).click();
  text = await cardText(page);
  expect('choosing 587 sets port and encryption together', /Sends through: smtp\.fakemail\.test, port 587, encrypted after connecting \(STARTTLS\)/.test(text));
  await lookAgain(page);
  text = await cardText(page);
  expect('looking again does not replace what was chosen by hand', /Sends through: smtp\.fakemail\.test, port 587/.test(text) && /The form holds other settings/.test(text)
    && await page.getByRole('button', { name: 'Use these settings' }).count() === 1, text);
  await shot(page, 'chromium-5-custom');

  note('--- check with a wrong password, then with the right one');
  text = await check(page);
  expect('a wrong password stops at "Sign in" with what to check', /The check stopped at: Sign in/.test(text) && /refused the user name or the password/.test(text), text.slice(text.indexOf('The check stopped')));
  state = await mailState();
  expect('the server saw one refused sign-in, over an encrypted connection', state.signIns.length === 1 && !state.signIns[0].ok && state.signIns[0].tls && state.messages.length === 0, JSON.stringify(state.signIns));
  await shot(page, 'chromium-6-wrong-password');
  await page.locator('#Email_Password').fill(RIGHT);
  await page.locator('#Email_FromName').fill('Cosmos, test');
  text = await check(page);
  expect('the right password sends the test email', new RegExp('It works: the mail server took the test email for ' + ADDRESS).test(text) && /These settings are not saved yet: press Save below/.test(text), text.slice(text.indexOf('It works')));
  state = await mailState();
  expect('the fake server holds the message, from the address with its name', state.messages.length === 1 && state.messages[0].from === ADDRESS
    && state.messages[0].to[0] === ADDRESS && /"Cosmos, test" <cosmos@fakemail\.test>/.test(state.messages[0].header), JSON.stringify(state.messages));
  await shot(page, 'chromium-7-works');

  note('--- save, reload, read the saved state');
  expect('nothing was saved by the check', (await savedConfig(page)).Host === '');
  await page.getByRole('button', { name: /^Save$/ }).last().click();
  await page.waitForTimeout(6000);
  let saved = await savedConfig(page);
  expect('saved: server, port, encryption, user, sender', saved.Enabled && saved.Host === 'smtp.fakemail.test' && saved.Port === '587' && saved.UseTLS && saved.AllowInsecureTLS
    && saved.Username === ADDRESS && saved.From === '"Cosmos, test" <cosmos@fakemail.test>', JSON.stringify(saved));
  expect('the stored password does not come back to the browser', saved.Password === '***');
  text = await cardText(page);
  expect('after the save the form no longer says "not saved"', !/not saved yet/.test(text), text.slice(-400));
  await openSettings(page);
  text = await cardText(page);
  expect('after a reload: address, name and server are there', await page.locator('#Email_Address').inputValue() === ADDRESS && await page.locator('#Email_FromName').inputValue() === 'Cosmos, test'
    && /Sends through: smtp\.fakemail\.test, port 587/.test(text) && !/not saved yet/.test(text));
  expect('the password field is empty and says one is saved', await page.locator('#Email_Password').inputValue() === ''
    && /Saved\. Type here to replace it/.test(await page.locator('#Email_Password').getAttribute('placeholder')) && /One is saved on the server/.test(text));
  expect('nothing is looked up when the page opens', !/Looking for|Known provider|Announced by/.test(text));
  await shot(page, 'chromium-8-saved');
  text = await check(page);
  expect('the check works with the stored password', /It works/.test(text) && !/not saved yet/.test(text));
  state = await mailState();
  expect('the stored password reached the saved server', state.signIns.length === 3 && state.signIns[2].ok && state.messages.length === 2, JSON.stringify(state.signIns));

  note('--- the stored password is for the saved connection only');
  await openCustom(page);
  await page.getByRole('radio', { name: /^465/ }).click();
  text = await cardText(page);
  expect('another port asks for the password again', /Type the password again/.test(text) && await page.getByRole('button', { name: 'Check and send a test email' }).isDisabled());
  await page.getByRole('button', { name: /^Save$/ }).last().click();
  await page.waitForTimeout(3000);
  expect('the server refuses the save too, and says why', /enter the mail password again/.test(await page.locator('body').innerText()) && (await savedConfig(page)).Port === '587');
  await shot(page, 'chromium-9-password-again');
  await page.getByRole('radio', { name: /^587/ }).click();
  await page.waitForTimeout(300);
  expect('back on the saved port nothing is asked', !/Type the password again/.test(await cardText(page)));

  note('--- other and custom ports');
  await page.getByRole('radio', { name: 'Other' }).click();
  await page.locator('#Email_Port').fill('99999');
  text = await cardText(page);
  expect('a port that cannot be is marked', /Enter a port between 1 and 65535/.test(text));
  await page.locator('#Email_Port').fill('465');
  await page.getByRole('radio', { name: 'Not encrypted' }).click();
  text = await cardText(page);
  expect('465 without encryption is marked as not working', /Port 465 only works encrypted from the start/.test(text));
  await page.getByRole('radio', { name: /^587/ }).click();

  note('--- what other addresses give (the form holds saved settings: nothing is replaced)');
  await typeAddress(page, 'someone@plainmail.test');
  text = await cardText(page);
  expect('a domain without records: the usual name, checked for real', /smtp\.plainmail\.test, port 465/.test(text) && /The usual name for this domain/.test(text), text);
  expect('saved settings are not replaced by a proposal', await page.locator('#Email_Host').inputValue() === 'smtp.fakemail.test' && /The form holds other settings/.test(text));
  await typeAddress(page, 'someone@hosted.test');
  text = await cardText(page);
  expect('a domain hosted at Google is recognised by its MX record', /smtp\.gmail\.com/.test(text) && /Mail for hosted\.test is handled by Google Workspace/.test(text), text);
  await shot(page, 'chromium-10-hosted');
  await typeAddress(page, 'someone@outlook.com');
  text = await cardText(page);
  expect('Outlook.com: says it cannot be used instead of proposing it', /Outlook\.com cannot be used for sending from Cosmos/.test(text) && !/Use these settings/.test(text), text);
  await typeAddress(page, 'someone@nowhere-at-all.test');
  text = await cardText(page);
  expect('an unknown domain: says so and points to the address', /No mail server is known for nowhere-at-all\.test/.test(text) && /typing mistake/.test(text), text);
  await shot(page, 'chromium-11-unknown');
  await page.locator('#Email_Service').click();
  await page.getByRole('option', { name: 'SendGrid' }).click();
  await page.waitForTimeout(400);
  text = await cardText(page);
  expect('a known service fills server, port and its fixed user name', await page.locator('#Email_Host').inputValue() === 'smtp.sendgrid.net'
    && await page.locator('#Email_Username').inputValue() === 'apikey' && /user name is always the word apikey/.test(text), text);
  await shot(page, 'chromium-12-service');
  expect('no sideways scroll', (await sideways(page)) <= 1, await sideways(page));

  // nothing of the above was saved: the next engines find the saved state
  await browser.close();

  note('--- without the confirmed password the settings stay hidden');
  {
    const plain = await chromium.launch();
    const ctx = await plain.newContext({ viewport: { width: 1366, height: 900 } });
    const view = await ctx.newPage();
    view.on('pageerror', (e) => errors.push(`unconfirmed: ${e.message.slice(0, 200)}`));
    await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: 'Test-Passw0rd!x' } });
    await openSettings(view);
    const hiddenText = await cardText(view);
    expect('no address, server or password field is shown', /shown and changed after you confirm your password/.test(hiddenText)
      && await view.locator('#Email_Address, #Email_Password, #Email_Host').count() === 0 && !/fakemail/.test(hiddenText), hiddenText);
    await shot(view, 'chromium-13-unconfirmed');
    await plain.close();
  }
}

// ---- the saved card in the other engines and on a phone ------------------------
const others = [
  ['chromium', chromium, 'phone-chromium', { width: 390, height: 844 }],
  ['firefox', firefox, 'firefox', { width: 1366, height: 900 }],
  ['firefox', firefox, 'phone-firefox', { width: 390, height: 844 }],
  ['webkit', webkit, 'webkit', { width: 1366, height: 900 }],
  ['webkit', webkit, 'phone-webkit', { width: 390, height: 844 }],
];
for (const [engine, type, name, viewport] of others) {
  if (!engines.includes(engine)) continue;
  note('--- ' + name);
  const { browser, page } = await open(type, name, viewport);
  await openSettings(page);
  let text = await cardText(page);
  expect(name + ': the saved settings are shown', await page.locator('#Email_Address').inputValue() === ADDRESS && /Sends through: smtp\.fakemail\.test, port 587/.test(text), text.slice(0, 200));
  await shot(page, name + '-1-saved');
  await typeAddress(page, 'someone@plainmail.test');
  await openCustom(page);
  text = await cardText(page);
  expect(name + ': a proposal is shown and held', /smtp\.plainmail\.test, port 465/.test(text) && /The form holds other settings/.test(text), text.slice(0, 300));
  await page.getByRole('button', { name: 'Use these settings' }).click();
  await page.waitForTimeout(400);
  expect(name + ': "Use these settings" fills the form', await page.locator('#Email_Host').inputValue() === 'smtp.plainmail.test'
    && await page.getByRole('radio', { name: /^465/ }).getAttribute('aria-checked') === 'true');
  await page.locator('#Email_Password').fill('wrong-password');
  await page.locator('input[name=Email_AllowInsecureTLS]').check().catch(() => {});
  text = await check(page);
  expect(name + ': the check says where it stopped and what to do', /The check stopped at: Sign in/.test(text) && /refused the user name or the password/.test(text), text.slice(text.indexOf('The check stopped')));
  await shot(page, name + '-2-check');
  expect(name + ': no sideways scroll', (await sideways(page)) <= 1, await sideways(page));
  // every part of the card stays inside it
  const outside = await card(page).evaluate((el) => {
    const box = el.getBoundingClientRect();
    return [...el.querySelectorAll('input, button, .MuiChip-root, .MuiAlert-root, p, span')]
      .filter((child) => child.getBoundingClientRect().width > 0 && child.getBoundingClientRect().right > box.right + 1).length;
  });
  expect(name + ': nothing sticks out of the card', outside === 0, outside);
  await browser.close();
}

note('\nscript errors:', errors.length ? errors : 'none');
note(failures.length ? 'FAILED: ' + failures.join(' ; ') : 'ALL CHECKS PASSED');
process.exit(failures.length || errors.length ? 1 : 0);

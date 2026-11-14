// Fork addition: checks the "Telegram notifications" card of the settings in
// headless Chromium, Firefox and WebKit, at desktop and phone width, against
// a throwaway Cosmos instance and a stand-in for Telegram. Never point it at
// the production instance, and never at the real Telegram.
//
// It expects (see CLAUDE.md, "Checking the UI in a browser"):
//   BASE  the instance, set up with admin / Test-Passw0rd!x, with Telegram
//         settings saved (a token and a chat) and switched on
//   FAKE  the control port of the stand-in: GET /mode?set=ok|chat_not_found
//         chooses how it answers sendMessage
// The instance must resolve api.telegram.org to the stand-in and trust its
// certificate: the server's code path is the real one.
import { chromium, firefox, webkit } from 'playwright';

const base = process.env.BASE || 'http://cosmos-smoke:18080';
const fake = process.env.FAKE || 'http://cosmos-fake:8080';
const problems = [];
const note = (...a) => console.log(...a);
const problem = (text) => { problems.push(text); console.log('PROBLEM:', text); };
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const sizes = { desktop: { width: 1366, height: 900 }, phone: { width: 390, height: 844 } };

const check = async (type, engine, size) => {
  const name = `${engine}-${size}`;
  const browser = await type.launch();
  const ctx = await browser.newContext({ viewport: sizes[size], ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => problem(`${name} script error: ${e.message.slice(0, 300)}`));

  let r = await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: 'Test-Passw0rd!x' } });
  const login = r.status();
  r = await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: 'Test-Passw0rd!x' } });
  note(name, 'login', login, 'sudo', r.status());

  await page.goto(base + '/cosmos-ui/config-general', { waitUntil: 'domcontentloaded' });
  const heading = page.getByText('Telegram notifications', { exact: true }).first();
  await heading.waitFor({ timeout: 20000 });
  await page.waitForTimeout(1500);
  const card = page.locator('.MuiCard-root', { has: page.locator('input[name=Telegram_BotToken]') }).last();
  await card.scrollIntoViewIfNeeded();
  await card.screenshot({ path: `shots/${name}-card.png` });

  // the token field: a password field that holds nothing, though a token is saved
  const token = page.locator('input[name=Telegram_BotToken]');
  const tokenType = await token.getAttribute('type');
  const tokenValue = await token.inputValue();
  const cardText = await card.innerText();
  note(name, 'token field: type', tokenType, 'value', JSON.stringify(tokenValue), '| says a token is saved:', cardText.includes('A token is saved'));
  if (tokenType !== 'password' || tokenValue !== '') problem(`${name}: the token field is ${tokenType} with value "${tokenValue}"`);
  if (!cardText.includes('A token is saved')) problem(`${name}: the card does not say that a token is saved`);
  if ((await page.content()).includes('AAFake')) problem(`${name}: the page contains the token`);

  // the page as a whole: nothing wider than the screen
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  note(name, 'sideways overflow of the page:', overflow, 'px');
  if (overflow > 1) problem(`${name}: the page scrolls sideways by ${overflow}px`);
  const cut = await card.evaluate((el) => {
    const limit = el.getBoundingClientRect().right + 1;
    return [...el.querySelectorAll('*')].filter((child) => child.getBoundingClientRect().width > 0 && child.getBoundingClientRect().right > limit)
      .slice(0, 3).map((child) => child.tagName + ' ' + (child.textContent || '').slice(0, 40));
  });
  if (cut.length) problem(`${name}: reaches out of the card: ${JSON.stringify(cut)}`);

  // the six kinds of messages, each with its line
  for (const label of ['Security events', 'Logins from a new address', 'System', 'Storage', 'Apps', 'Everything else']) {
    if (!cardText.includes(label)) problem(`${name}: the kind "${label}" is not shown`);
  }

  // a check of the form: a chat that is none
  const chat = page.locator('input[name=Telegram_ChatID]');
  const savedChat = await chat.inputValue();
  await chat.fill('my chat');
  await chat.blur();
  await page.waitForTimeout(400);
  const complained = (await card.innerText()).includes('A chat ID is a number');
  const testButton = card.getByRole('button', { name: 'Send a test message' });
  note(name, 'bad chat ID: complaint shown', complained, '| test button disabled', await testButton.isDisabled());
  if (!complained) problem(`${name}: a chat ID that is none is not complained about`);
  if (!(await testButton.isDisabled())) problem(`${name}: a test can be sent with a chat ID that is none`);
  await chat.fill(savedChat);
  await chat.blur();

  // quiet hours open their two times
  await card.getByText('Quiet hours: deliver without sound').click();
  await page.waitForTimeout(400);
  note(name, 'quiet hours fields:', await page.locator('input[name=Telegram_QuietFrom]').count(), await page.locator('input[name=Telegram_QuietTo]').count());
  if ((await page.locator('input[name=Telegram_QuietFrom]').count()) !== 1) problem(`${name}: the quiet hours did not open`);

  // a test message that Telegram refuses, in words
  await fetch(fake + '/mode?set=chat_not_found');
  await testButton.scrollIntoViewIfNeeded();
  await testButton.click();
  await card.getByText('The test message was not sent.').waitFor({ timeout: 30000 }).catch(() => problem(`${name}: no answer to a refused test message`));
  await page.waitForTimeout(300);
  const refused = await card.getByRole('alert').filter({ hasText: 'The test message was not sent.' }).innerText().catch(() => '');
  note(name, 'refused test:', refused.replace(/\n/g, ' ').slice(0, 200));
  if (!refused.includes('Telegram cannot find this chat')) problem(`${name}: the refusal is not explained: ${refused}`);
  await card.getByRole('alert').filter({ hasText: 'The test message was not sent.' }).scrollIntoViewIfNeeded().catch(() => {});
  await page.screenshot({ path: `shots/${name}-test-failed.png` });

  // and one it accepts (the server leaves three seconds between two tests)
  await fetch(fake + '/mode?set=ok');
  await sleep(3300);
  await testButton.click();
  await card.getByText('Telegram accepted the test message').waitFor({ timeout: 30000 }).catch(() => problem(`${name}: no answer to an accepted test message`));
  await page.waitForTimeout(600);
  const delivered = (await card.innerText()).match(/Last message sent: [^\n]*/);
  note(name, 'accepted test shown:', (await card.innerText()).includes('Telegram accepted the test message'), '|', delivered ? delivered[0] : 'no delivery line');
  await page.screenshot({ path: `shots/${name}-test-ok.png` });

  // removing the saved token is announced, and can be taken back
  await card.getByRole('button', { name: 'Remove the saved token' }).click();
  await page.waitForTimeout(300);
  const removing = (await card.innerText()).includes('The saved token is removed when you save');
  await card.getByRole('button', { name: 'Keep the saved token' }).click();
  note(name, 'remove the token: announced', removing);
  if (!removing) problem(`${name}: removing the token is not announced`);

  await card.scrollIntoViewIfNeeded();
  await page.screenshot({ path: `shots/${name}-page.png`, fullPage: false });
  await sleep(3300);
  await browser.close();
};

// one engine saves: the name is kept, and so is the token nobody typed
const saveRoundTrip = async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: sizes.desktop });
  const page = await ctx.newPage();
  await ctx.request.post(base + '/cosmos/api/login', { data: { nickname: 'admin', password: 'Test-Passw0rd!x' } });
  await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: 'Test-Passw0rd!x' } });
  await page.goto(base + '/cosmos-ui/config-general', { waitUntil: 'domcontentloaded' });
  await page.locator('input[name=Telegram_ServerName]').waitFor({ timeout: 20000 });
  await page.waitForTimeout(1500);
  const stamp = 'Saved from the browser ' + Date.now().toString().slice(-5);
  await page.locator('input[name=Telegram_ServerName]').fill(stamp);
  await page.getByRole('button', { name: /^Save$/ }).first().click();
  await page.waitForTimeout(6000);
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.locator('input[name=Telegram_ServerName]').waitFor({ timeout: 20000 });
  await page.waitForTimeout(1500);
  const kept = await page.locator('input[name=Telegram_ServerName]').inputValue();
  const card = page.locator('.MuiCard-root', { has: page.locator('input[name=Telegram_BotToken]') }).last();
  const text = await card.innerText();
  note('save: the name after a reload:', JSON.stringify(kept), '| a token is still saved:', text.includes('A token is saved'));
  if (kept !== stamp) problem('the saved name did not come back after a reload');
  if (!text.includes('A token is saved')) problem('saving the form lost the token');

  // the bell says when messages are not delivered
  await fetch(fake + '/mode?set=wrong_token');
  await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: 'not the password' } });
  await ctx.request.post(base + '/cosmos/api/sudo', { data: { password: 'Test-Passw0rd!x' } });
  await sleep(4000);
  const bell = await ctx.request.get(base + '/cosmos/api/notifications');
  note('bell after a refused token:', (await bell.text()).slice(0, 300));
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.locator('input[name=Telegram_ServerName]').waitFor({ timeout: 20000 });
  await page.waitForTimeout(2000);
  const problemText = (await card.innerText()).match(/Last problem[^\n]*/);
  note('card after a refused token:', problemText ? problemText[0].slice(0, 260) : 'NO PROBLEM LINE');
  if (!problemText) problem('the card does not show the last problem');
  await card.getByText('Deliveries', { exact: true }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: 'shots/chromium-desktop-delivery-problem.png' });
  await fetch(fake + '/mode?set=ok');
  await browser.close();
};

for (const [type, engine] of [[chromium, 'chromium'], [firefox, 'firefox'], [webkit, 'webkit']]) {
  for (const size of ['desktop', 'phone']) {
    await check(type, engine, size).catch((e) => problem(`${engine}-${size}: ${e.message.slice(0, 300)}`));
  }
}
await saveRoundTrip().catch((e) => problem(`save: ${e.message.slice(0, 300)}`));

console.log(problems.length ? `\n${problems.length} problem(s)` : '\nno problem found');
process.exit(problems.length ? 1 : 0);

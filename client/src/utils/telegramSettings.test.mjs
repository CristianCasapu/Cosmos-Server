// Fork addition: tests of telegramSettings.js. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  MASKED, TELEGRAM_CATEGORIES, telegramInitialValues, telegramToConfig, tokenIsSaved, tokenToSave,
  tokenProblem, chatProblem, topicProblem, serverNameProblem, quietProblem, maxPerMinuteProblem,
  canSendTest, telegramUnsaved,
} from './telegramSettings.js';

const TOKEN = '123456789:AAFakeFakeFakeFakeFakeFakeFakeFake0';

// what GET /api/config sends when Telegram is set up: the token masked
const saved = {
  Telegram: {
    Enabled: true, BotToken: MASKED, ChatID: '-1001234567890', TopicID: 7, ServerName: 'Cellar',
    Categories: { security: true, logins: true, apps: false }, MinSeverity: 'medium', HideDetails: true,
    QuietFrom: '22:30', QuietTo: '06:00', QuietSilenceCritical: true, MaxPerMinute: 3,
  },
};

test('a server that never had Telegram set up starts from the defaults', () => {
  for (const config of [undefined, null, {}, { Telegram: {} }]) {
    const values = telegramInitialValues(config);
    assert.equal(values.Telegram_Enabled, false);
    assert.equal(values.Telegram_BotToken, '');
    assert.equal(values.Telegram_ChatID, '');
    assert.equal(values.Telegram_TopicID, '');
    assert.equal(values.Telegram_MinSeverity, 'high');
    assert.equal(values.Telegram_IncludeDetails, true);
    assert.equal(values.Telegram_QuietEnabled, false);
    assert.equal(values.Telegram_QuietCriticalRings, true);
    assert.equal(values.Telegram_MaxPerMinute, 6);
    assert.deepEqual(
      TELEGRAM_CATEGORIES.map(({ name }) => [name, values['Telegram_Cat_' + name]]),
      [['security', true], ['logins', false], ['system', true], ['storage', true], ['apps', true], ['other', true]],
    );
    assert.equal(tokenIsSaved(config), false);
  }
});

test('the form shows what is saved, except the token', () => {
  const values = telegramInitialValues(saved);
  assert.equal(values.Telegram_BotToken, '', 'the token field never receives anything, not even the mask');
  assert.equal(values.Telegram_Enabled, true);
  assert.equal(values.Telegram_ChatID, '-1001234567890');
  assert.equal(values.Telegram_TopicID, '7');
  assert.equal(values.Telegram_ServerName, 'Cellar');
  assert.equal(values.Telegram_MinSeverity, 'medium');
  assert.equal(values.Telegram_IncludeDetails, false);
  assert.equal(values.Telegram_QuietEnabled, true);
  assert.equal(values.Telegram_QuietFrom, '22:30');
  assert.equal(values.Telegram_QuietCriticalRings, false);
  assert.equal(values.Telegram_MaxPerMinute, 3);
  // a category the settings name has their value, the others their default
  assert.equal(values.Telegram_Cat_logins, true);
  assert.equal(values.Telegram_Cat_apps, false);
  assert.equal(values.Telegram_Cat_system, true);
  assert.equal(tokenIsSaved(saved), true);
});

test('saving the form as it was loaded changes nothing and keeps the stored token', () => {
  const values = telegramInitialValues(saved);
  const out = telegramToConfig(values, saved);
  assert.equal(out.BotToken, MASKED, 'the mask goes back: the server keeps what it has');
  assert.deepEqual({ ...out, Categories: undefined }, { ...saved.Telegram, Categories: undefined });
  assert.deepEqual(out.Categories, { security: true, logins: true, system: true, storage: true, apps: false, other: true });
  assert.equal(telegramUnsaved(values, saved), false);
  assert.equal(telegramUnsaved({ ...values, Telegram_ChatID: '5' }, saved), true);
});

test('the token that is saved', () => {
  const values = telegramInitialValues(saved);
  // typed: replaces, spaces from the paste removed
  assert.equal(tokenToSave({ ...values, Telegram_BotToken: '  ' + TOKEN + '\n' }, saved), TOKEN);
  // removed on purpose
  assert.equal(tokenToSave({ ...values, Telegram_ForgetToken: true }, saved), '');
  // typed wins over "remove"
  assert.equal(tokenToSave({ ...values, Telegram_ForgetToken: true, Telegram_BotToken: TOKEN }, saved), TOKEN);
  // nothing stored, nothing typed: nothing, never the mask
  assert.equal(tokenToSave(telegramInitialValues({}), {}), '');
  assert.equal(telegramToConfig(telegramInitialValues({}), {}).BotToken, '');
});

test('numbers are saved as numbers, quiet hours only when switched on', () => {
  const values = { ...telegramInitialValues({}), Telegram_TopicID: ' 42 ', Telegram_MaxPerMinute: '10', Telegram_QuietFrom: '23:00', Telegram_QuietTo: '07:00' };
  const out = telegramToConfig(values, {});
  assert.equal(out.TopicID, 42);
  assert.equal(out.MaxPerMinute, 10);
  assert.equal(out.QuietFrom, '');
  assert.equal(out.QuietTo, '');
  const quiet = telegramToConfig({ ...values, Telegram_QuietEnabled: true, Telegram_QuietCriticalRings: false, Telegram_IncludeDetails: false }, {});
  assert.equal(quiet.QuietFrom, '23:00');
  assert.equal(quiet.QuietTo, '07:00');
  assert.equal(quiet.QuietSilenceCritical, true);
  assert.equal(quiet.HideDetails, true);
});

test('the token is checked', () => {
  const fresh = telegramInitialValues({});
  // off and empty: fine
  assert.equal(tokenProblem(fresh, {}), '');
  // on without a token anywhere
  assert.equal(tokenProblem({ ...fresh, Telegram_Enabled: true }, {}), 'required');
  // on with a stored one
  assert.equal(tokenProblem(telegramInitialValues(saved), saved), '');
  // on, the stored one being removed
  assert.equal(tokenProblem({ ...telegramInitialValues(saved), Telegram_ForgetToken: true }, saved), 'required');
  assert.equal(tokenProblem({ ...fresh, Telegram_BotToken: TOKEN }, {}), '');
  for (const bad of ['nonsense', '123:abc', TOKEN + '/getMe', TOKEN + ' x', MASKED, 'bot' + TOKEN, '123456789:AAFake?FakeFakeFakeFakeFakeFakeFake0']) {
    assert.equal(tokenProblem({ ...fresh, Telegram_BotToken: bad }, {}), 'shape', bad);
  }
});

test('the chat, the topic, the name, the hours and the cap are checked', () => {
  const on = { ...telegramInitialValues({}), Telegram_Enabled: true };
  assert.equal(chatProblem(on), 'required');
  assert.equal(chatProblem(telegramInitialValues({})), '');
  for (const good of ['123456789', '-1001234567890', '@my_channel', MASKED, ' 42 ']) {
    assert.equal(chatProblem({ ...on, Telegram_ChatID: good }), '', good);
  }
  for (const bad of ['my chat', 'https://t.me/x', '@ab', '12.5', '--1', '@1abc']) {
    assert.equal(chatProblem({ ...on, Telegram_ChatID: bad }), 'shape', bad);
  }

  for (const good of ['', '1', '42', '2147483647']) assert.equal(topicProblem({ Telegram_TopicID: good }), '', good);
  for (const bad of ['0', '-1', '1.5', 'abc', '2147483648', '99999999999']) assert.equal(topicProblem({ Telegram_TopicID: bad }), 'shape', bad);

  assert.equal(serverNameProblem({ Telegram_ServerName: 'Home server (cellar) ăîșț' }), '');
  assert.equal(serverNameProblem({ Telegram_ServerName: 'n'.repeat(64) }), '');
  assert.equal(serverNameProblem({ Telegram_ServerName: 'n'.repeat(65) }), 'long');
  assert.equal(serverNameProblem({ Telegram_ServerName: 'two\nlines' }), 'shape');
  assert.equal(serverNameProblem({ Telegram_ServerName: 'hidden' + String.fromCharCode(0x202e) + 'override' }), 'shape');

  assert.equal(quietProblem({ Telegram_QuietEnabled: false, Telegram_QuietFrom: 'x', Telegram_QuietTo: 'y' }), '');
  assert.equal(quietProblem({ Telegram_QuietEnabled: true, Telegram_QuietFrom: '23:00', Telegram_QuietTo: '07:00' }), '');
  assert.equal(quietProblem({ Telegram_QuietEnabled: true, Telegram_QuietFrom: '24:00', Telegram_QuietTo: '07:00' }), 'shape');
  assert.equal(quietProblem({ Telegram_QuietEnabled: true, Telegram_QuietFrom: '', Telegram_QuietTo: '07:00' }), 'shape');
  assert.equal(quietProblem({ Telegram_QuietEnabled: true, Telegram_QuietFrom: '08:00', Telegram_QuietTo: '08:00' }), 'same');

  for (const good of [1, 6, 15, '10']) assert.equal(maxPerMinuteProblem({ Telegram_MaxPerMinute: good }), '', String(good));
  for (const bad of [0, 16, -1, '', 'x', 1.5, 100]) assert.equal(maxPerMinuteProblem({ Telegram_MaxPerMinute: bad }), 'range', String(bad));
});

test('a test message needs a token and a chat, saved or typed, and nothing wrong with them', () => {
  const fresh = telegramInitialValues({});
  assert.equal(canSendTest(fresh, {}), false);
  assert.equal(canSendTest({ ...fresh, Telegram_BotToken: TOKEN }, {}), false, 'no chat');
  assert.equal(canSendTest({ ...fresh, Telegram_BotToken: TOKEN, Telegram_ChatID: '42' }, {}), true, 'typed, not saved, not even switched on');
  assert.equal(canSendTest({ ...fresh, Telegram_BotToken: 'nonsense', Telegram_ChatID: '42' }, {}), false);
  assert.equal(canSendTest({ ...fresh, Telegram_BotToken: TOKEN, Telegram_ChatID: 'somewhere' }, {}), false);
  assert.equal(canSendTest(telegramInitialValues(saved), saved), true, 'the saved token');
  assert.equal(canSendTest({ ...telegramInitialValues(saved), Telegram_ForgetToken: true }, saved), false, 'the saved token is being removed');
  assert.equal(canSendTest({ ...telegramInitialValues(saved), Telegram_TopicID: 'x' }, saved), false);
});

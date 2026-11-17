// Fork addition (plan 47): tests of telegramChats.js. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  MANUAL, chatLabel, chatChoices, pickedChoice, proposeFromLook, takeChoice, nobodyWrote,
} from './telegramChats.js';

const ann = { id: '123456789', kind: 'private', name: 'Ann Example', username: 'ann_example' };
const group = { id: '-1001234567890', kind: 'supergroup', name: 'Home server', topics: [{ id: 7, name: 'Alerts' }] };
const channel = { id: '-1009876543210', kind: 'channel', name: 'Server news' };

const empty = { Telegram_ChatID: '', Telegram_TopicID: '' };

test('each chat is named in words', () => {
  assert.equal(chatLabel(ann)[1], 'The private chat with {{name}} (@{{username}})');
  assert.deepEqual(chatLabel(ann)[2], { name: 'Ann Example', username: 'ann_example' });
  assert.equal(chatLabel({ ...ann, username: '' })[1], 'The private chat with {{name}}');
  assert.equal(chatLabel(group)[1], 'The group {{name}}');
  assert.equal(chatLabel({ ...group, kind: 'group' })[1], 'The group {{name}}');
  assert.equal(chatLabel(channel)[1], 'The channel {{name}}');
  assert.deepEqual(chatLabel(group, group.topics[0])[2], { topic: 'Alerts', name: 'Home server' });
  // a chat without a name is named by its number
  assert.equal(chatLabel({ id: '42', kind: 'group', name: '' })[2].name, '42');
});

test('the choices are the chats, their topics and the door, last', () => {
  const choices = chatChoices([ann, group, channel, { id: 'not a number', kind: 'group' }, null]);
  assert.deepEqual(choices.map((c) => c.value), ['123456789', '-1001234567890', '-1001234567890#7', '-1009876543210', MANUAL]);
  assert.equal(choices[2].topicId, '7');
  assert.equal(choices[choices.length - 1].label[1], 'Type the chat ID myself');
  assert.deepEqual(chatChoices(undefined).map((c) => c.value), [MANUAL]);
});

test('the picked choice follows the form', () => {
  const choices = chatChoices([ann, group]);
  assert.equal(pickedChoice(empty, choices, false), '');
  assert.equal(pickedChoice({ Telegram_ChatID: '123456789', Telegram_TopicID: '' }, choices, false), '123456789');
  assert.equal(pickedChoice({ Telegram_ChatID: '-1001234567890', Telegram_TopicID: '7' }, choices, false), '-1001234567890#7');
  assert.equal(pickedChoice({ Telegram_ChatID: '555', Telegram_TopicID: '' }, choices, false), MANUAL);
  assert.equal(pickedChoice({ Telegram_ChatID: '123456789', Telegram_TopicID: '' }, choices, true), MANUAL);
});

test('one chat found fills an empty form, never a typed chat', () => {
  let out = proposeFromLook(empty, [ann], {});
  assert.deepEqual(out.values, { Telegram_ChatID: '123456789' });
  assert.deepEqual(out.held, []);
  // looked again: the field still holds the proposal, which may be replaced
  out = proposeFromLook({ Telegram_ChatID: '123456789', Telegram_TopicID: '' }, [channel], out.marks);
  assert.deepEqual(out.values, { Telegram_ChatID: '-1009876543210' });

  // a chat typed (or saved) is not replaced: the proposal waits
  out = proposeFromLook({ Telegram_ChatID: '555', Telegram_TopicID: '' }, [ann], {});
  assert.deepEqual(out.values, {});
  assert.deepEqual(out.held, ['Telegram_ChatID']);
  assert.equal(out.choice.value, '123456789');

  // several: nothing is filled, the administrator picks
  out = proposeFromLook(empty, [ann, channel], {});
  assert.deepEqual(out.values, {});
  assert.equal(out.proposal, null);
});

test('picking a choice sets the chat and its topic together', () => {
  const choices = chatChoices([group]);
  let out = takeChoice({ Telegram_ChatID: '555', Telegram_TopicID: '3' }, choices[1]);
  assert.deepEqual(out.values, { Telegram_ChatID: '-1001234567890', Telegram_TopicID: '7' });
  out = takeChoice({ Telegram_ChatID: '555', Telegram_TopicID: '3' }, choices[0]);
  assert.deepEqual(out.values, { Telegram_ChatID: '-1001234567890', Telegram_TopicID: '' });
  // the door changes nothing
  assert.deepEqual(takeChoice({ Telegram_ChatID: '555' }, choices[2]).values, {});
});

test('nobody wrote yet: the exact thing to do', () => {
  assert.deepEqual(nobodyWrote('cosmos_test_bot'),
    ['mgmt.config.telegram.chats.none', 'Open the bot in Telegram (@{{bot}}) and send it any message, then press Look again.', { bot: 'cosmos_test_bot' }]);
  assert.equal(nobodyWrote('')[1], 'Open your bot in Telegram and send it any message, then press Look again.');
});

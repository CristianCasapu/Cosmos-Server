// Fork addition (plan 47, I2d): the chats Telegram reported for the bot
// (POST /api/telegram/chats, src/telegram/fork_chats.go) as choices of the
// Telegram card (pages/config/telegram.jsx), and how a choice fills the form.
// Plain functions, no React: see telegramChats.test.mjs.
//
// The names come from other people's Telegram accounts: they are text, put
// into a sentence as a value, never markup.

import { applyProposal, acceptProposal } from './proposals.js';

export const MANUAL = 'manual';

// The two fields a chat fills; they go together (a topic belongs to its group).
export const CHAT_UNIT = ['Telegram_ChatID', 'Telegram_TopicID'];

const text = (value) => String(value === undefined || value === null ? '' : value).trim();

// A choice's value: the chat, and the topic after "#" when there is one.
export const choiceValue = (chatId, topicId) => (topicId ? `${chatId}#${topicId}` : String(chatId));

// [key, English, params] of a chat in words.
export const chatLabel = (chat, topic) => {
  const name = text(chat && chat.name) || text(chat && chat.id);
  const username = text(chat && chat.username);
  if (topic) {
    const topicName = text(topic.name) || '#' + topic.id;
    return ['mgmt.config.telegram.chats.topic', 'The topic {{topic}} of the group {{name}}', { topic: topicName, name }];
  }
  switch (chat && chat.kind) {
    case 'private':
      return username
        ? ['mgmt.config.telegram.chats.privateUser', 'The private chat with {{name}} (@{{username}})', { name, username }]
        : ['mgmt.config.telegram.chats.private', 'The private chat with {{name}}', { name }];
    case 'channel':
      return username
        ? ['mgmt.config.telegram.chats.channelUser', 'The channel {{name}} (@{{username}})', { name, username }]
        : ['mgmt.config.telegram.chats.channel', 'The channel {{name}}', { name }];
    default:
      return ['mgmt.config.telegram.chats.group', 'The group {{name}}', { name }];
  }
};

// The choices for a list of chats: each chat, each topic of a group with
// topics, then the door ("type it myself"). The labels are [key, English, params].
export const chatChoices = (chats) => {
  const choices = [];
  (Array.isArray(chats) ? chats : []).forEach((chat) => {
    if (!chat || !/^-?[0-9]{1,20}$/.test(text(chat.id))) return;
    choices.push({ value: choiceValue(chat.id), chatId: text(chat.id), topicId: '', label: chatLabel(chat) });
    (Array.isArray(chat.topics) ? chat.topics : []).forEach((topic) => {
      if (!topic || !Number.isInteger(topic.id) || topic.id < 1) return;
      choices.push({ value: choiceValue(chat.id, topic.id), chatId: text(chat.id), topicId: String(topic.id), label: chatLabel(chat, topic) });
    });
  });
  choices.push({ value: MANUAL, chatId: '', topicId: '', label: ['mgmt.config.telegram.chats.manual', 'Type the chat ID myself'] });
  return choices;
};

// What a choice puts into the form.
export const proposalOf = (choice) => (choice && choice.value !== MANUAL
  ? { Telegram_ChatID: choice.chatId, Telegram_TopicID: choice.topicId }
  : null);

// The choice that matches the form, MANUAL when the form holds another chat,
// '' when it holds none. `manual` is the administrator having picked the door.
export const pickedChoice = (values, choices, manual) => {
  if (manual) return MANUAL;
  const chat = text(values && values.Telegram_ChatID);
  if (!chat) return '';
  const found = (choices || []).find((choice) => choice.value !== MANUAL
    && choice.chatId === chat && choice.topicId === text(values.Telegram_TopicID));
  return found ? found.value : MANUAL;
};

// What the answer of a look proposes by itself: the chat when exactly one was
// found (with several, the administrator picks one). The rule of
// utils/proposals.js: empty fields, or fields that still hold an earlier
// proposal, are filled; anything else is held for a "Use" button.
export const proposeFromLook = (values, chats, marks) => {
  const choices = chatChoices(chats).filter((choice) => choice.value !== MANUAL && !choice.topicId);
  if (choices.length !== 1) return { values: {}, marks: marks || {}, held: [], proposal: null };
  const proposal = proposalOf(choices[0]);
  return { ...applyProposal(values, proposal, marks || {}, [CHAT_UNIT]), proposal, choice: choices[0] };
};

// The administrator picking a choice (or pressing "Use"): set, and marked.
export const takeChoice = (values, choice) => {
  const proposal = proposalOf(choice);
  return proposal ? acceptProposal(values, proposal) : { values: {}, marks: {} };
};

// The sentence when nobody wrote to the bot yet.
export const nobodyWrote = (bot) => (text(bot)
  ? ['mgmt.config.telegram.chats.none', 'Open the bot in Telegram (@{{bot}}) and send it any message, then press Look again.', { bot: text(bot) }]
  : ['mgmt.config.telegram.chats.noneNoName', 'Open your bot in Telegram and send it any message, then press Look again.']);

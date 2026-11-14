// Fork addition: the logic of the "Telegram notifications" card of the
// settings (pages/config/telegram.jsx): what the form starts from, what it
// saves, and the checks of its fields. The server checks again when the
// settings are saved (src/telegram/config.go). Plain functions, no React:
// see telegramSettings.test.mjs.
//
// The bot token is a secret. The server never sends it: it sends "***" when
// one is stored and "" when none is. The form's token field therefore always
// starts empty, and saving sends back what the server sent unless a new
// token was typed.

// What the server sends in place of a stored secret, and takes back as "keep it".
export const MASKED = '***';

// The kinds of messages, in the order they are shown, with their default.
export const TELEGRAM_CATEGORIES = [
  { name: 'security', byDefault: true },
  { name: 'logins', byDefault: false },
  { name: 'system', byDefault: true },
  { name: 'storage', byDefault: true },
  { name: 'apps', byDefault: true },
  { name: 'other', byDefault: true },
];

export const DEFAULT_MAX_PER_MINUTE = 6;
export const MAX_PER_MINUTE = 15;
export const MAX_SERVER_NAME = 64;
const MAX_TOPIC = 2147483647;

const TOKEN = /^[0-9]{5,15}:[A-Za-z0-9_-]{20,80}$/;
const CHAT_NUMBER = /^-?[0-9]{1,20}$/;
const CHAT_NAME = /^@[A-Za-z][A-Za-z0-9_]{3,31}$/;
const TIME = /^([01][0-9]|2[0-3]):[0-5][0-9]$/;

const text = (value) => String(value === undefined || value === null ? '' : value).trim();

// Whether the server holds a token (it never says which).
export const tokenIsSaved = (config) => Boolean(config && config.Telegram && config.Telegram.BotToken === MASKED);

// What the form starts from. The token field is always empty.
export const telegramInitialValues = (config) => {
  const saved = (config && config.Telegram) || {};
  const categories = saved.Categories || {};
  const values = {
    Telegram_Enabled: Boolean(saved.Enabled),
    Telegram_BotToken: '',
    Telegram_ForgetToken: false,
    Telegram_ChatID: saved.ChatID || '',
    Telegram_TopicID: saved.TopicID ? String(saved.TopicID) : '',
    Telegram_ServerName: saved.ServerName || '',
    Telegram_MinSeverity: ['medium', 'low'].includes(saved.MinSeverity) ? saved.MinSeverity : 'high',
    Telegram_IncludeDetails: !saved.HideDetails,
    Telegram_QuietEnabled: Boolean(saved.QuietFrom && saved.QuietTo),
    Telegram_QuietFrom: saved.QuietFrom || '23:00',
    Telegram_QuietTo: saved.QuietTo || '07:00',
    Telegram_QuietCriticalRings: !saved.QuietSilenceCritical,
    Telegram_MaxPerMinute: saved.MaxPerMinute || DEFAULT_MAX_PER_MINUTE,
  };
  TELEGRAM_CATEGORIES.forEach(({ name, byDefault }) => {
    values['Telegram_Cat_' + name] = typeof categories[name] === 'boolean' ? categories[name] : byDefault;
  });
  return values;
};

// The token that is saved: the one typed, else what the server sent ("***"
// keeps the stored one), else none when the stored one is to be removed.
export const tokenToSave = (values, config) => {
  const typed = text(values.Telegram_BotToken);
  if (typed !== '') return typed;
  if (values.Telegram_ForgetToken) return '';
  return tokenIsSaved(config) ? MASKED : '';
};

// What is saved as config.Telegram (and sent with a test message).
export const telegramToConfig = (values, config) => {
  const categories = {};
  TELEGRAM_CATEGORIES.forEach(({ name }) => {
    categories[name] = Boolean(values['Telegram_Cat_' + name]);
  });
  const quiet = Boolean(values.Telegram_QuietEnabled);
  return {
    Enabled: Boolean(values.Telegram_Enabled),
    BotToken: tokenToSave(values, config),
    ChatID: text(values.Telegram_ChatID),
    TopicID: Number(text(values.Telegram_TopicID)) || 0,
    ServerName: text(values.Telegram_ServerName),
    Categories: categories,
    MinSeverity: ['medium', 'low'].includes(values.Telegram_MinSeverity) ? values.Telegram_MinSeverity : 'high',
    HideDetails: !values.Telegram_IncludeDetails,
    QuietFrom: quiet ? text(values.Telegram_QuietFrom) : '',
    QuietTo: quiet ? text(values.Telegram_QuietTo) : '',
    QuietSilenceCritical: !values.Telegram_QuietCriticalRings,
    MaxPerMinute: Number(values.Telegram_MaxPerMinute) || 0,
  };
};

// Whether there will be a token to send with: one typed, or one stored that
// is not being removed.
export const willHaveToken = (values, config) => tokenToSave(values, config) !== '';

// The checks. Each returns '' when the value is fine, else the name of what
// is wrong (the card turns it into a sentence).
export const tokenProblem = (values, config) => {
  const typed = text(values.Telegram_BotToken);
  if (typed !== '') return TOKEN.test(typed) ? '' : 'shape';
  return values.Telegram_Enabled && !willHaveToken(values, config) ? 'required' : '';
};

export const chatProblem = (values) => {
  const chat = text(values.Telegram_ChatID);
  if (chat === '') return values.Telegram_Enabled ? 'required' : '';
  return chat === MASKED || CHAT_NUMBER.test(chat) || CHAT_NAME.test(chat) ? '' : 'shape';
};

export const topicProblem = (values) => {
  const topic = text(values.Telegram_TopicID);
  if (topic === '') return '';
  return /^[0-9]{1,10}$/.test(topic) && Number(topic) >= 1 && Number(topic) <= MAX_TOPIC ? '' : 'shape';
};

export const serverNameProblem = (values) => {
  const name = text(values.Telegram_ServerName);
  if ([...name].length > MAX_SERVER_NAME) return 'long';
  // line breaks and other characters that cannot be seen
  return /[\u0000-\u001f\u007f-\u009f\u200b-\u200f\u2028-\u202e\u2060-\u206f\ufeff]/.test(name) ? 'shape' : '';
};

export const quietProblem = (values) => {
  if (!values.Telegram_QuietEnabled) return '';
  const from = text(values.Telegram_QuietFrom);
  const to = text(values.Telegram_QuietTo);
  if (!TIME.test(from) || !TIME.test(to)) return 'shape';
  return from === to ? 'same' : '';
};

export const maxPerMinuteProblem = (values) => {
  const value = text(values.Telegram_MaxPerMinute);
  return /^[0-9]{1,2}$/.test(value) && Number(value) >= 1 && Number(value) <= MAX_PER_MINUTE ? '' : 'range';
};

// Whether a test message can be sent with what is in the form.
export const canSendTest = (values, config) =>
  willHaveToken(values, config) && text(values.Telegram_ChatID) !== ''
  && !tokenProblem({ ...values, Telegram_Enabled: true }, config) && !chatProblem(values) && !topicProblem(values) && !serverNameProblem(values);

// Whether the form differs from what is saved.
export const telegramUnsaved = (values, config) =>
  JSON.stringify(telegramToConfig(values, config)) !== JSON.stringify(telegramToConfig(telegramInitialValues(config), config));

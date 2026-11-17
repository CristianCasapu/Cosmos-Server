// Fork addition (plan 48): the logic of the list of Telegram messages by
// severity (pages/config/telegramKinds.jsx). The server renders every kind
// of message with sample values (GET /api/telegram/kinds: the text, its
// entities in UTF-16 code units, the button); this module cuts the text into
// parts to show, says what a phone's preview shows, and keeps the switches
// per kind and the delivery per severity in the settings form. Plain
// functions, no React: see telegramMessage.test.mjs.

// From the most to the least serious, as the list shows them.
export const SEVERITIES = ['critical', 'error', 'warn', 'info'];

// How a severity arrives, and what the server does when the settings say nothing.
export const DELIVERY_MODES = ['sound', 'silent', 'off'];
export const DEFAULT_DELIVERY = { info: 'silent', warn: 'sound', error: 'sound', critical: 'sound' };

const RECORD_RANK = { low: 1, medium: 2, high: 3 };

// The parts of a message: plain text, and the values Telegram shows as code
// (one line) or pre (a block). Offsets and lengths are UTF-16 code units,
// which is what JavaScript strings count: they apply directly. Entities that
// overlap or reach outside the text are ignored, never trusted.
export const messageParts = (text, entities) => {
  const source = String(text || '');
  const list = (Array.isArray(entities) ? entities : [])
    .filter((e) => e && (e.type === 'code' || e.type === 'pre')
      && Number.isInteger(e.offset) && Number.isInteger(e.length)
      && e.offset >= 0 && e.length > 0 && e.offset + e.length <= source.length)
    .sort((a, b) => a.offset - b.offset);
  const parts = [];
  let at = 0;
  list.forEach((e) => {
    if (e.offset < at) return; // overlaps the previous one
    if (e.offset > at) parts.push({ type: 'plain', text: source.slice(at, e.offset) });
    parts.push({ type: e.type, text: source.slice(e.offset, e.offset + e.length) });
    at = e.offset + e.length;
  });
  if (at < source.length) parts.push({ type: 'plain', text: source.slice(at) });
  return parts;
};

// What a phone shows before the message is opened: the first line, cut to
// about one line of a phone (limit characters) with "…".
export const previewLine = (text, limit = 64) => {
  const first = String(text || '').split('\n')[0];
  const chars = Array.from(first);
  return chars.length > limit ? chars.slice(0, limit - 1).join('').trimEnd() + '…' : first;
};

// The kinds of one severity, in the order the server gave them.
export const kindsOf = (kinds, severity) => (Array.isArray(kinds) ? kinds : []).filter((k) => k && k.severity === severity);

// Whether a kind is sent when its switch was not touched: its category is
// on, and for an event of the security record, it is at least of the
// minimum severity. A message of the notifications themselves always is.
export const kindDefault = (kind, values) => {
  if (!kind) return false;
  if (kind.own) return true;
  if (kind.off) return false; // plan 48: off until switched on by itself
  if (!values['Telegram_Cat_' + kind.category]) return false;
  if (kind.record) {
    const minimum = RECORD_RANK[values.Telegram_MinSeverity] || RECORD_RANK.high;
    return (RECORD_RANK[kind.record] || 0) >= minimum;
  }
  return true;
};

// Whether a kind is sent with the form as it is.
export const kindOn = (kind, values) => {
  const explicit = (values.Telegram_Kinds || {})[kind.kind];
  return typeof explicit === 'boolean' ? explicit : kindDefault(kind, values);
};

// The switches after one was flipped: a switch that ends where its default
// is holds nothing, so that the category and the minimum severity decide.
export const flipKind = (kind, values) => {
  const next = { ...(values.Telegram_Kinds || {}) };
  const on = !kindOn(kind, values);
  if (on === kindDefault(kind, values)) {
    delete next[kind.kind];
  } else {
    next[kind.kind] = on;
  }
  return next;
};

// How a severity arrives with the form as it is.
export const deliveryOf = (severity, values) => {
  const mode = values['Telegram_Delivery_' + severity];
  return DELIVERY_MODES.includes(mode) ? mode : DEFAULT_DELIVERY[severity];
};

// The form's values from the saved settings.
export const kindValues = (saved) => {
  const values = { Telegram_Kinds: {} };
  const kinds = (saved && saved.Kinds) || {};
  Object.keys(kinds).forEach((name) => {
    if (typeof kinds[name] === 'boolean') values.Telegram_Kinds[name] = kinds[name];
  });
  const delivery = (saved && saved.Delivery) || {};
  SEVERITIES.forEach((severity) => {
    values['Telegram_Delivery_' + severity] = DELIVERY_MODES.includes(delivery[severity]) ? delivery[severity] : DEFAULT_DELIVERY[severity];
  });
  return values;
};

// What is saved: the switches that were set (sorted, so that comparing two
// forms compares their content), and the deliveries that are not the default.
export const kindConfig = (values) => {
  const kinds = {};
  Object.keys(values.Telegram_Kinds || {}).sort().forEach((name) => {
    if (typeof values.Telegram_Kinds[name] === 'boolean') kinds[name] = values.Telegram_Kinds[name];
  });
  const delivery = {};
  SEVERITIES.slice().reverse().forEach((severity) => {
    const mode = deliveryOf(severity, values);
    if (mode !== DEFAULT_DELIVERY[severity]) delivery[severity] = mode;
  });
  return { Kinds: kinds, Delivery: delivery };
};

// Why a kind is not sent, as [key, English text] for t(), or null when it is.
export const whyNotSent = (kind, values) => {
  if (deliveryOf(kind.closing ? 'info' : kind.severity, values) === 'off') {
    return ['mgmt.config.telegram.kinds.whySeverityOff', 'Not sent: this severity is switched off above.'];
  }
  if (kindOn(kind, values)) return null;
  const explicit = (values.Telegram_Kinds || {})[kind.kind];
  if (explicit === false) return ['mgmt.config.telegram.kinds.whyOff', 'Not sent: switched off here.'];
  if (kind.off) return ['mgmt.config.telegram.kinds.whyDefaultOff', 'Not sent: off until switched on here.'];
  if (!values['Telegram_Cat_' + kind.category]) return ['mgmt.config.telegram.kinds.whyCategory', 'Not sent: its group is switched off under "What is sent".'];
  return ['mgmt.config.telegram.kinds.whyMinimum', 'Not sent: below the minimum severity of security events. Switch it on to receive it anyway.'];
};

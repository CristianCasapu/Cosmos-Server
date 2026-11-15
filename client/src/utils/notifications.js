// Fork addition: the pure logic of the notifications page, its details view
// and the bell (pages/notifications, layout/.../Notification.jsx). The
// server's answers are described in src/fork_notifications.go. Every text a
// notification holds is data: these functions return strings that the pages
// render as text, never as markup, and links only to the panel's own pages.

// ---- the texts of the pages: [key, English] (the test checks translation.json) ----

export const TEXT = {
  pageTitle: ['notifications.page.title', 'Notifications'],
  menu: ['menu-items.navigation.notifications', 'Notifications'],
  intro: ['notifications.page.intro', 'Everything Cosmos told you, newest first. Open one to see what happened, what it means, what Cosmos did about it and what you can do.'],
  zone: ['notifications.page.zone', 'Times are in {{zone}}.'],
  filterLevel: ['notifications.filter.level', 'Level'],
  filterSource: ['notifications.filter.source', 'About'],
  filterRead: ['notifications.filter.read', 'Read or not'],
  filterText: ['notifications.filter.text', 'Search'],
  filterTextPlaceholder: ['notifications.filter.textPlaceholder', 'a port, a domain, an app...'],
  filterAny: ['notifications.filter.any', 'All'],
  filterUnread: ['notifications.filter.unread', 'Unread only'],
  filterReadOnly: ['notifications.filter.readOnly', 'Read only'],
  filterClear: ['notifications.filter.clear', 'Clear the filters'],
  refresh: ['notifications.page.refresh', 'Refresh'],
  markAllRead: ['notifications.page.markAllRead', 'Mark all as read'],
  deleteRead: ['notifications.page.deleteRead', 'Delete the read ones'],
  deleteReadTitle: ['notifications.page.deleteReadTitle', 'Delete every notification you have read?'],
  deleteReadText: ['notifications.page.deleteReadText', 'Only your own copies are deleted: other administrators keep theirs, and the security record and the events are not touched. Unread notifications stay.'],
  deleteOneTitle: ['notifications.page.deleteOneTitle', 'Delete this notification?'],
  deleteOneText: ['notifications.page.deleteOneText', 'Only your own copy is deleted. If the same thing happens again, a new notification is written.'],
  delete: ['notifications.action.delete', 'Delete'],
  cancel: ['notifications.action.cancel', 'Cancel'],
  markRead: ['notifications.action.markRead', 'Mark as read'],
  markUnread: ['notifications.action.markUnread', 'Mark as unread'],
  open: ['notifications.action.open', 'Details'],
  loadMore: ['notifications.page.loadMore', 'Show older ones'],
  empty: ['notifications.page.empty', 'Nothing to report. Updates, alerts and problems Cosmos finds are listed here.'],
  emptyFiltered: ['notifications.page.emptyFiltered', 'No notification matches these filters.'],
  loadFailed: ['notifications.page.loadFailed', 'The notifications could not be loaded: {{error}}'],
  unreadCount: ['notifications.page.unreadCount', '{{count}} unread'],
  unread: ['notifications.state.unread', 'Unread'],
  resolved: ['notifications.state.resolved', 'Resolved'],
  resolvedAt: ['notifications.state.resolvedAt', 'Resolved {{when}}'],
  open_: ['notifications.state.open', 'Still going on'],
  times: ['notifications.occurrence.times', '{{count}} times since {{since}}'],
  lastSeen: ['notifications.occurrence.lastSeen', 'last {{when}}'],
  // the bell
  bellTitle: ['notifications.bell.title', 'Notifications'],
  seeAll: ['notifications.bell.seeAll', 'See all notifications'],
  bellEmpty: ['header.notification.empty', 'Nothing to report. Updates, alerts and jobs that failed are listed here.'],
  // the details
  back: ['notifications.details.back', 'All notifications'],
  notFound: ['notifications.details.notFound', 'This notification does not exist, or it is not one of yours. It may have been deleted, or removed after 30 days.'],
  whatHappened: ['notifications.details.whatHappened', 'What happened'],
  whatItMeans: ['notifications.details.whatItMeans', 'What it means'],
  whatCosmosDid: ['notifications.details.whatCosmosDid', 'What Cosmos did'],
  whatYouCanDo: ['notifications.details.whatYouCanDo', 'What you can do'],
  technical: ['notifications.details.technical', 'Technical text'],
  copy: ['notifications.details.copy', 'Copy'],
  copied: ['notifications.details.copied', 'Copied.'],
  facts: ['notifications.details.facts', 'Details'],
  stateNow: ['notifications.details.stateNow', 'Right now'],
  stateLoading: ['notifications.details.stateLoading', 'Looking at the state right now...'],
  stateOk: ['notifications.details.stateOk', 'Fine now'],
  stateBad: ['notifications.details.stateBad', 'Still a problem'],
  stateUnknown: ['notifications.details.stateUnknown', 'Not known'],
  stateNone: ['notifications.details.stateNone', 'There is nothing to look at right now for this kind of notification.'],
  readAt: ['notifications.details.readAt', 'Read at {{when}}'],
  history: ['notifications.details.history', 'Earlier occurrences'],
  historyOnce: ['notifications.details.historyOnce', 'This happened once.'],
  historyMany: ['notifications.details.historyMany', 'This is the {{ordinal}} time since {{since}}.'],
  historyOccurrence: ['notifications.details.historyOccurrence', 'Happened'],
  historyRelated: ['notifications.details.historyRelated', 'Related notification'],
  security: ['notifications.details.security', 'In the security record around that time'],
  securityNone: ['notifications.details.securityNone', 'Nothing in the security record belongs to it.'],
  securityOpen: ['notifications.details.securityOpen', 'Open the audit'],
  events: ['notifications.details.events', 'Monitoring events around that time'],
  eventsNone: ['notifications.details.eventsNone', 'No Monitoring event belongs to it.'],
  eventsOpen: ['notifications.details.eventsOpen', 'Open Monitoring'],
  log: ['notifications.details.log', 'Server log around that moment'],
  logNote: ['notifications.details.logNote', 'The lines of Cosmos\' log about this, around each time it happened. Tokens, keys, passwords and cookies are hidden.'],
  logNone: ['notifications.details.logNone', 'No line of the log belongs to it.'],
  logTruncated: ['notifications.details.logTruncated', 'More lines were found than are shown here.'],
  contextFailed: ['notifications.details.contextFailed', 'What surrounds this notification could not be loaded: {{error}}'],
  legacy: ['notifications.details.legacy', 'This notification was written by an earlier version of Cosmos: it holds only its text.'],
  openLink: ['notifications.details.openLink', 'Open the page it is about'],
  first: ['notifications.details.first', 'First seen'],
  last: ['notifications.details.last', 'Last seen'],
  level: ['notifications.details.level', 'Level'],
  source: ['notifications.details.source', 'About'],
  subject: ['notifications.details.subject', 'Object'],
  kind: ['notifications.details.kind', 'Kind'],
  checkNow: ['notifications.step.checkNow', 'Check now'],
  checkDone: ['notifications.action.checkDone', 'Checked: the router forwards the port.'],
  checkFailed: ['notifications.action.checkFailed', 'Checked: port forwarding still fails: {{error}}'],
  actionFailed: ['notifications.action.failed', 'That did not work: {{error}}'],
};

export const LEVELS = ['error', 'warning', 'info', 'success'];

export const LEVEL_TEXT = {
  error: ['notifications.level.error', 'Error'],
  warning: ['notifications.level.warning', 'Warning'],
  info: ['notifications.level.info', 'Information'],
  success: ['notifications.level.success', 'Resolved or done'],
};

export const SOURCES = [
  ['upnp', 'Router port forwarding'],
  ['certificates', 'Certificates'],
  ['containers', 'Apps and containers'],
  ['snapshots', 'Incremental backup'],
  ['monitoring', 'Monitoring alerts'],
  ['fail2ban', 'Fail2ban'],
  ['telegram', 'Telegram'],
  ['updates', 'Server updates'],
  ['cloudflare', 'Cloudflare'],
  ['backup', 'Configuration backup'],
  ['jobs', 'Scheduled jobs'],
  ['storage', 'Storage'],
  ['server', 'The server'],
  ['security', 'Security'],
  ['other', 'Other'],
];
export const SOURCE_TEXT = Object.fromEntries(SOURCES.map(([id, text]) => [id, ['notifications.source.' + id, text]]));

export const SUBJECT_TEXT = {
  port: ['notifications.subject.port', 'Port'],
  router: ['notifications.subject.router', 'Router'],
  domain: ['notifications.subject.domain', 'Domain'],
  container: ['notifications.subject.container', 'App'],
  disk: ['notifications.subject.disk', 'Disk'],
  job: ['notifications.subject.job', 'Job'],
  alert: ['notifications.subject.alert', 'Alert'],
  release: ['notifications.subject.release', 'Release'],
  chat: ['notifications.subject.chat', 'Chat'],
  folder: ['notifications.subject.folder', 'Folder'],
  setting: ['notifications.subject.setting', 'Setting'],
  other: ['notifications.subject.other', 'About'],
};

// ---- levels ----

// normalizeLevel: what upstream and the fork write ("warn", "warning",
// "important", an alert's severity) as one of LEVELS.
export function normalizeLevel(level) {
  const l = String(level || '').toLowerCase();
  if (l === 'warn' || l === 'warning') return 'warning';
  if (l === 'error' || l === 'critical' || l === 'fatal') return 'error';
  if (l === 'success' || l === 'ok') return 'success';
  return 'info';
}

// the MUI colour of a level (Alert severity, palette name)
export const levelColor = (level) => normalizeLevel(level);

// ---- sentences ----

const textOf = (value) => (typeof value === 'string' ? value : value === undefined || value === null ? '' : String(value));

// fill puts the vars into an English text, the way i18next does.
export function fill(text, vars) {
  return textOf(text).replace(/\{\{\s*(\w+)\s*\}\}/g, (whole, name) => (vars && vars[name] !== undefined && vars[name] !== null ? textOf(vars[name]) : ''));
}

// cleanVars keeps string and number values only (a sentence's vars come from the server).
export function cleanVars(vars) {
  const out = {};
  if (!vars || typeof vars !== 'object' || Array.isArray(vars)) return out;
  for (const [key, value] of Object.entries(vars)) {
    if (/^\w{1,40}$/.test(key) && (typeof value === 'string' || typeof value === 'number')) out[key] = textOf(value);
  }
  return out;
}

// sentence renders {key, text, vars} with t (i18next's), or without (the
// English, for the tests). A missing translation and a missing text fall
// back to the other; a key alone that is not translated is shown as it is
// rather than nothing.
export function sentence(s, t) {
  if (!s || typeof s !== 'object') return '';
  const key = textOf(s.key);
  const text = textOf(s.text);
  const vars = cleanVars(s.vars);
  if (!key) return fill(text, vars);
  if (!t) return text ? fill(text, vars) : key;
  const result = t(key, text || key, { ...vars, interpolation: { escapeValue: false } });
  if (typeof result !== 'string' || result === '') return text ? fill(text, vars) : key;
  return result;
}

// titleOf / messageOf: what a list row shows. Old rows carry the upstream
// title key and a message key with {Vars}; "Server Error" rows carry their
// text in the message without a key.
export const titleOf = (item, t) => sentence(item && item.title, t) || sentence({ text: item && item.kind }, t);
export const messageOf = (item, t) => sentence(item && item.message, t);

// ---- links ----

// safeLink returns a link of the panel's own pages, or '' for anything else
// (another site, a script address, a path that climbs out).
export function safeLink(link) {
  const l = textOf(link);
  if (l.length > 2000 || !l.startsWith('/cosmos-ui/')) return '';
  if (/[\u0000-\u001f\u007f\\]/.test(l) || l.includes('//') || /(^|\/)\.\.?(\/|$|\?|#)/.test(l)) return '';
  return l;
}

export const detailsLink = (item) => (item && Number.isSafeInteger(item.id) && item.id > 0 ? '/cosmos-ui/notifications/' + item.id : '');

// ---- step actions: the commands the panel knows ----

export const ACTIONS = { 'upnp.check': true, 'reach.check': true /* Fork addition: the watch from the internet */ };
export const knownAction = (action) => ACTIONS[action] === true;

// ---- filters (in the page's address: ?level=error&source=upnp&read=unread&q=...) ----

export const MAX_Q = 100;
export const PAGE_SIZE = 30;
export const BELL_SIZE = 8;
export const DEFAULT_FILTERS = { level: '', source: '', read: '', q: '' };
const SOURCE_IDS = new Set(SOURCES.map(([id]) => id));

export function cleanFilters(input) {
  const f = { ...DEFAULT_FILTERS };
  if (!input || typeof input !== 'object') return f;
  if (LEVELS.includes(input.level)) f.level = input.level;
  if (SOURCE_IDS.has(input.source)) f.source = input.source;
  if (input.read === 'read' || input.read === 'unread') f.read = input.read;
  f.q = Array.from(textOf(input.q).replace(/[\u0000-\u001f\u007f]/g, ' ')).slice(0, MAX_Q).join('').trim();
  return f;
}

export function filtersFromSearch(search) {
  let params;
  try {
    params = new URLSearchParams(textOf(search));
  } catch (e) {
    return { ...DEFAULT_FILTERS };
  }
  return cleanFilters({ level: params.get('level'), source: params.get('source'), read: params.get('read'), q: params.get('q') });
}

export function searchFromFilters(filters) {
  const f = cleanFilters(filters);
  const params = new URLSearchParams();
  for (const key of Object.keys(DEFAULT_FILTERS)) if (f[key]) params.set(key, f[key]);
  const text = params.toString();
  return text ? '?' + text : '';
}

export const hasFilters = (filters) => searchFromFilters(filters) !== '';

// apiParams: the query of GET /cosmos/api/notifications/list.
export function apiParams(filters, cursor, limit = PAGE_SIZE) {
  const f = cleanFilters(filters);
  const params = {};
  if (f.level) params.level = f.level;
  if (f.source) params.source = f.source;
  if (f.read) params.read = f.read;
  if (f.q) params.q = f.q;
  if (typeof cursor === 'string' && /^\d{1,16}:\d{1,19}$/.test(cursor)) params.cursor = cursor;
  params.limit = Math.max(1, Math.min(100, Number(limit) || PAGE_SIZE));
  return params;
}

// ---- bodies of the commands ----

const ids = (list) => (Array.isArray(list) ? list : [list]).filter((id) => Number.isSafeInteger(id) && id > 0).slice(0, 200);
export const markBody = (list, read) => ({ ids: ids(list), read: !!read });
export const markAllBody = () => ({ all: true });
export const deleteBody = (list) => ({ ids: ids(list) });
export const deleteReadBody = () => ({ read: true });

// ---- items as the server sends them: checked and completed ----

const time = (value) => {
  const d = new Date(value);
  return value && !Number.isNaN(d.getTime()) && d.getTime() > 0 ? d.toISOString() : null;
};

export function cleanItem(raw) {
  if (!raw || typeof raw !== 'object') return null;
  const id = Number(raw.id);
  if (!Number.isSafeInteger(id) || id <= 0) return null;
  const subject = raw.subject && typeof raw.subject === 'object' && textOf(raw.subject.name)
    ? { type: SUBJECT_TEXT[raw.subject.type] ? raw.subject.type : 'other', name: textOf(raw.subject.name), link: safeLink(raw.subject.link) }
    : null;
  return {
    ...raw,
    id,
    ref: /^[0-9a-f]{8,64}$/.test(textOf(raw.ref)) ? raw.ref : '',
    level: normalizeLevel(raw.level),
    read: !!raw.read,
    date: time(raw.date),
    firstSeen: time(raw.firstSeen),
    resolved: time(raw.resolved),
    count: Math.max(1, Math.floor(Number(raw.count) || 1)),
    source: SOURCE_IDS.has(raw.source) ? raw.source : 'other',
    subject,
    link: safeLink(raw.link),
    legacy: !!raw.legacy,
  };
}

export const cleanItems = (list) => (Array.isArray(list) ? list.map(cleanItem).filter(Boolean) : []);

// mergePage appends an older page to what is shown, without doubles.
export function mergePage(shown, page) {
  const seen = new Set((shown || []).map((n) => n.id));
  return [...(shown || []), ...cleanItems(page).filter((n) => !seen.has(n.id))];
}

// ---- occurrences ----

// ordinal: "1st", "2nd", "3rd", "4th", "11th", "22nd" (English; the sentence is translatable).
export function ordinal(n) {
  const v = Math.floor(Number(n) || 0);
  const tens = v % 100;
  if (tens >= 11 && tens <= 13) return v + 'th';
  return v + ({ 1: 'st', 2: 'nd', 3: 'rd' }[v % 10] || 'th');
}

// occurrence: the words under a row: [key, text, vars] or null for once.
export function occurrence(item, since) {
  if (!item || item.count <= 1) return null;
  return [TEXT.times[0], TEXT.times[1], { count: item.count, since: textOf(since) }];
}

// timeline: what happened, newest first: each occurrence (the server's
// "seen" times), the notifications of the same thread (a follow-up that
// says it is resolved, earlier ones), bounded.
export function timeline(item, details, max = 40) {
  const entries = [];
  const seen = details && Array.isArray(details.seen) ? details.seen : [];
  for (const value of seen) {
    const at = time(value);
    if (at) entries.push({ type: 'occurrence', at });
  }
  if (entries.length === 0 && item && item.date) entries.push({ type: 'occurrence', at: item.date });
  for (const other of cleanItems(details && details.thread)) {
    if (item && other.id === item.id) continue;
    entries.push({ type: other.level === 'success' || other.resolved ? 'resolved' : 'related', at: other.date, item: other });
  }
  const unique = [];
  const keys = new Set();
  for (const e of entries.sort((a, b) => (a.at < b.at ? 1 : a.at > b.at ? -1 : 0))) {
    const key = e.type + e.at + (e.item ? e.item.id : '');
    if (!keys.has(key)) {
      keys.add(key);
      unique.push(e);
    }
  }
  return unique.slice(0, max);
}

// ---- the context's table ----

// tableOf checks a {header, rows} of the context: every cell text, every
// row as long as the header.
export function tableOf(state) {
  if (!state || !Array.isArray(state.header) || !Array.isArray(state.rows) || state.header.length === 0) return null;
  const header = state.header.slice(0, 8);
  const rows = state.rows.slice(0, 200).map((row) => {
    const cells = Array.isArray(row && row.cells) ? row.cells : [];
    return header.map((_, i) => textOf(cells[i]));
  });
  return { header, rows };
}

// auditLink: the audit page filtered on what the notification is about.
export function auditLink(target) {
  const q = Array.from(textOf(target)).slice(0, 64).join('').trim();
  return '/cosmos-ui/security-audit' + (q ? '?' + new URLSearchParams({ q }).toString() : '');
}

// allTexts: every [key, English] of this file, for the translation test.
export function allTexts() {
  return [
    ...Object.values(TEXT),
    ...Object.values(LEVEL_TEXT),
    ...Object.values(SOURCE_TEXT),
    ...Object.values(SUBJECT_TEXT),
    ...Object.values(COMMAND_TEXT),
  ];
}

// ---- the commands' names in the activity toasts (utils/activity.js) ----

export const COMMAND_TEXT = {
  markAll: ['activity.notifications.markAll', 'Mark every notification as read'],
  deleteSome: ['activity.notifications.delete', 'Delete the notification'],
  deleteRead: ['activity.notifications.deleteRead', 'Delete the read notifications'],
};

// commandLabel: the toast's name of a request to the notifications routes;
// null for marking named notifications read or unread (the list shows it
// at once, and opening a notification marks it read).
export function commandLabel(path, method, body) {
  if (method !== 'POST') return null;
  let parsed = {};
  try {
    parsed = typeof body === 'string' ? JSON.parse(body) : body || {};
  } catch (e) {
    parsed = {};
  }
  if (path === '/api/notifications/mark') return parsed.all ? COMMAND_TEXT.markAll : null;
  if (path === '/api/notifications/delete') return parsed.read ? COMMAND_TEXT.deleteRead : COMMAND_TEXT.deleteSome;
  return null;
}

// Fork addition: the filters of the user audit panel. The state lives in
// the page's address (?user=maria&period=7d), so a link to one user's
// activity works; whatever the address holds is checked here, so a hand-made
// link gives a filter the server accepts or none.

import { KIND_GROUPS } from './auditSentences.js';

export const PERIODS = [
  { id: 'all', label: 'Everything kept', hours: 0 },
  { id: '24h', label: 'Last 24 hours', hours: 24 },
  { id: '7d', label: 'Last 7 days', hours: 7 * 24 },
  { id: '30d', label: 'Last 30 days', hours: 30 * 24 },
];

export const OUTCOMES = [
  { id: 'all', label: 'Any outcome', values: [] },
  { id: 'ok', label: 'Done', values: ['ok'] },
  { id: 'failed', label: 'Failed', values: ['failed'] },
  { id: 'refused', label: 'Refused or blocked', values: ['denied', 'blocked'] },
];

export const DEFAULT_FILTERS = { user: '', group: 'all', outcome: 'all', period: 'all', ip: '', q: '' };

// Bounds, as the server applies them.
export const MAX_USER = 64;
export const MAX_TEXT = 64;
export const MAX_IP = 45;

const NICKNAME = /^[\p{L}\p{N}_.@-]{1,64}$/u;
const ADDRESS = /^[0-9A-Fa-f:.]{2,45}$/;

function clip(value, max) {
  return Array.from(String(value || '')).slice(0, max).join('').trim();
}

// cleanFilters keeps what is valid and puts the default in place of the rest.
export function cleanFilters(input) {
  const f = { ...DEFAULT_FILTERS };
  if (!input || typeof input !== 'object') return f;
  const user = clip(input.user, MAX_USER);
  if (NICKNAME.test(user)) f.user = user;
  if (KIND_GROUPS.some((g) => g.id === input.group)) f.group = input.group;
  if (OUTCOMES.some((o) => o.id === input.outcome)) f.outcome = input.outcome;
  if (PERIODS.some((p) => p.id === input.period)) f.period = input.period;
  const ip = clip(input.ip, MAX_IP);
  if (ADDRESS.test(ip)) f.ip = ip;
  f.q = clip(input.q, MAX_TEXT);
  return f;
}

// filtersFromSearch reads the filters from location.search.
export function filtersFromSearch(search) {
  const params = new URLSearchParams(search || '');
  const input = {};
  for (const key of Object.keys(DEFAULT_FILTERS)) {
    if (params.has(key)) input[key] = params.get(key);
  }
  return cleanFilters(input);
}

// searchFromFilters writes the filters that differ from the default, in a
// fixed order, as a search string ("?user=maria&period=7d", or "").
export function searchFromFilters(filters) {
  const f = cleanFilters(filters);
  const params = new URLSearchParams();
  for (const key of Object.keys(DEFAULT_FILTERS)) {
    if (f[key] !== DEFAULT_FILTERS[key]) params.set(key, f[key]);
  }
  const text = params.toString();
  return text ? '?' + text : '';
}

// userLink is the address of one user's activity on the audit page.
export function userLink(nickname) {
  return '/cosmos-ui/security-audit' + searchFromFilters({ user: nickname });
}

// apiQuery is the query of GET /cosmos/api/security/audit for the filters,
// without cursor and limit. now is a Date (passed in for the tests).
export function apiQuery(filters, now = new Date()) {
  const f = cleanFilters(filters);
  const params = new URLSearchParams();
  if (f.user) params.set('user', f.user);
  const group = KIND_GROUPS.find((g) => g.id === f.group);
  if (group) params.set('kind', group.kinds.join(','));
  const outcome = OUTCOMES.find((o) => o.id === f.outcome);
  if (outcome && outcome.values.length) params.set('outcome', outcome.values.join(','));
  const period = PERIODS.find((p) => p.id === f.period);
  if (period && period.hours) {
    // whole seconds: the server reads RFC 3339
    const from = new Date(Math.floor(now.getTime() / 1000) * 1000 - period.hours * 3600 * 1000);
    params.set('from', from.toISOString().replace('.000Z', 'Z'));
  }
  if (f.ip) params.set('ip', f.ip);
  if (f.q) params.set('q', f.q);
  return params.toString();
}

// hasFilters says whether anything narrows the trail.
export function hasFilters(filters) {
  return searchFromFilters(filters) !== '';
}

// ---- times: in the browser's zone, and the zone said ----

export function zoneName() {
  try {
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
    if (zone) return zone;
  } catch (e) { /* no Intl zone support */ }
  const minutes = -new Date().getTimezoneOffset();
  const sign = minutes >= 0 ? '+' : '-';
  const abs = Math.abs(minutes);
  return 'UTC' + sign + String(Math.floor(abs / 60)).padStart(2, '0') + ':' + String(abs % 60).padStart(2, '0');
}

// formatTime gives a time of the record in the browser's zone, with seconds.
// Empty for a missing or impossible time.
export function formatTime(value, locale) {
  const date = value instanceof Date ? value : new Date(value);
  if (!value || Number.isNaN(date.getTime()) || date.getTime() <= 0) return '';
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'medium' }).format(date);
  } catch (e) {
    return date.toString();
  }
}

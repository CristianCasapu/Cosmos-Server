// Fork addition (plan 51): the copy of the monitoring history between the
// local file and PostgreSQL (src/monitorcopy), as the Monitoring database card
// shows it (pages/config/forkMonitoringCopy.jsx). Pure: no React, no i18next;
// the callers pass t(key, fallback, vars) where text is made.

export const POLL_RUNNING_MS = 2000; // while a copy runs
export const POLL_IDLE_MS = 30000; // otherwise: the store in use can change (a save, the fallback)

export const TO_POSTGRES = 'to-postgres';
export const TO_LOCAL = 'to-local';
export const CATCH_UP = 'catch-up'; // the automatic catch-up after a fallback

const identity = (key, fallback, vars) => fill(fallback, vars);

// fill puts {{name}} values into a template, as i18next does.
export function fill(template, vars) {
  return String(template).replace(/\{\{(\w+)\}\}/g, (whole, name) => (vars && vars[name] !== undefined ? String(vars[name]) : whole));
}

// formatCount: 1234567 -> "1 234 567" (no-break spaces, the brief's style
// and readable in every language).
export function formatCount(n) {
  const value = Math.max(0, Math.round(Number(n) || 0));
  return String(value).replace(/\B(?=(\d{3})+(?!\d))/g, ' ');
}

// formatSize: bytes in words, rounded the way an administrator reads them.
export function formatSize(bytes, t = identity) {
  const n = Number(bytes) || 0;
  const mb = n / (1024 * 1024);
  if (mb < 1) return t('mgmt.config.general.monitoringDb.copy.sizeSmall', 'less than 1 MB');
  if (mb < 1024) return t('mgmt.config.general.monitoringDb.copy.sizeMB', 'about {{n}} MB', { n: formatCount(mb) });
  return t('mgmt.config.general.monitoringDb.copy.sizeGB', 'about {{n}} GB', { n: (mb / 1024).toFixed(1) });
}

// formatDuration: seconds left in words.
export function formatDuration(seconds, t = identity) {
  const s = Number(seconds);
  if (!(s >= 0)) return '';
  if (s < 60) return t('mgmt.config.general.monitoringDb.copy.leftSeconds', 'less than a minute left');
  const minutes = Math.round(s / 60);
  if (minutes < 90) return t('mgmt.config.general.monitoringDb.copy.leftMinutes', 'about {{n}} min left', { n: minutes });
  return t('mgmt.config.general.monitoringDb.copy.leftHours', 'about {{n}} h left', { n: (s / 3600).toFixed(1) });
}

// formatTime: unix ms in the browser's language and time zone; '' for none.
export function formatTime(ms, locale) {
  const n = Number(ms) || 0;
  if (n <= 0) return '';
  try {
    return new Intl.DateTimeFormat(locale || undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(n));
  } catch (e) {
    return new Date(n).toISOString();
  }
}

const TABLES = {
  monitoring_kv: ['mgmt.config.general.monitoringDb.copy.table.kv', 'Monitoring settings'],
  metrics: ['mgmt.config.general.monitoringDb.copy.table.metrics', 'List of metrics'],
  metric_values: ['mgmt.config.general.monitoringDb.copy.table.values', 'Metric values'],
  events: ['mgmt.config.general.monitoringDb.copy.table.events', 'Events'],
  notifications: ['mgmt.config.general.monitoringDb.copy.table.notifications', 'Notifications'],
  fork_security_events: ['mgmt.config.general.monitoringDb.copy.table.security', 'Security record'],
};

// tableLabel names a table in words; an unknown one by its name.
export function tableLabel(table, t = identity) {
  const known = TABLES[table];
  return known ? t(known[0], known[1]) : String(table || '');
}

// offerOf: what the card offers for a direction, or null when nothing.
// view is the data of GET /api/monitoring-copy.
export function offerOf(view, direction) {
  if (!view || view.inUse !== 'postgres' || view.running) return null;
  const offer = (view.offers || []).find((o) => o && o.direction === direction);
  if (!offer || !offer.available || !offer.source) return null;
  const source = offer.source;
  return {
    direction,
    records: Number(source.records) || 0,
    bytes: Number(source.bytes) || 0,
    first: Number(source.first) || 0,
    last: Number(source.last) || 0,
    lastCopy: Number(source.lastCopy) || 0,
    countedAt: Number(offer.countedAt) || 0,
    now: Number(view.now) || 0,
  };
}

// countedAgo: how old the numbers of an offer are, by the server's clocks
// (countedAt and now both come from the server, so the browser's clock does
// not matter); '' when not known.
export function countedAgo(offer, t = identity) {
  if (!offer || !(offer.countedAt > 0) || !(offer.now > 0)) return '';
  const minutes = Math.floor(Math.max(0, offer.now - offer.countedAt) / 60000);
  if (minutes < 1) return t('mgmt.config.general.monitoringDb.copy.countedNow', 'Counted just now.');
  if (minutes < 120) return t('mgmt.config.general.monitoringDb.copy.countedMinutes', 'Counted {{n}} minutes ago.', { n: minutes });
  return t('mgmt.config.general.monitoringDb.copy.countedHours', 'Counted {{n}} hours ago.', { n: Math.floor(minutes / 60) });
}

// catchUpOf: how the last automatic catch-up after a fallback ended, for a
// line of the card, or null.
export function catchUpOf(view, t = identity, locale) {
  const c = view && view.catchUp;
  if (!c || !c.outcome) return null;
  const vars = {
    n: formatCount(c.written), missing: formatCount(c.missing),
    from: formatTime(c.from, locale), to: formatTime(c.to, locale), message: c.message || '',
  };
  switch (c.outcome) {
    case 'done':
      return { outcome: 'done', severity: 'success', text: t('mgmt.config.general.monitoringDb.copy.caughtUp', 'Caught up {{n}} records written while the database was away (from {{from}} to {{to}}).', vars) };
    case 'skipped':
      return { outcome: 'skipped', severity: 'info', text: t('mgmt.config.general.monitoringDb.copy.catchUpSkipped', 'The records written while the database was away (from {{from}} to {{to}}) were not caught up: {{message}}', vars) };
    default:
      return { outcome: c.outcome, severity: 'warning', text: t('mgmt.config.general.monitoringDb.copy.catchUpPartly', 'Caught up {{n}} records written while the database was away (from {{from}} to {{to}}); {{missing}} were not: {{message}}', vars) };
  }
}

// offerLabel is the button of an offer, with its numbers.
export function offerLabel(offer, t = identity, locale) {
  if (!offer) return '';
  const vars = {
    records: formatCount(offer.records),
    size: formatSize(offer.bytes, t),
    first: formatTime(offer.first, locale),
    last: formatTime(offer.last, locale),
  };
  if (offer.direction === TO_POSTGRES) {
    if (vars.first && vars.last) {
      return t('mgmt.config.general.monitoringDb.copy.offerLocal', 'Copy the history from the local file ({{records}} records, {{size}}, from {{first}} to {{last}})', vars);
    }
    return t('mgmt.config.general.monitoringDb.copy.offerLocalNoDates', 'Copy the history from the local file ({{records}} records, {{size}})', vars);
  }
  if (vars.first && vars.last) {
    return t('mgmt.config.general.monitoringDb.copy.offerPostgres', 'Copy the records of PostgreSQL into the local file ({{records}} records, from {{first}} to {{last}})', vars);
  }
  return t('mgmt.config.general.monitoringDb.copy.offerPostgresNoDates', 'Copy the records of PostgreSQL into the local file ({{records}} records)', vars);
}

const percent = (done, total) => (total > 0 ? Math.min(100, Math.max(0, Math.floor((done * 100) / total))) : 100);

// progressOf: the running copy's progress, or null when none runs.
export function progressOf(view) {
  if (!view || !view.running) return null;
  const tables = (view.tables || []).map((p) => ({
    table: p.table,
    done: Number(p.done) || 0,
    total: Number(p.total) || 0,
    state: p.state || 'waiting',
    percent: percent(Number(p.done) || 0, Number(p.total) || 0),
  }));
  const total = tables.reduce((n, p) => n + p.total, 0);
  const done = tables.reduce((n, p) => n + Math.min(p.done, p.total), 0);
  const window = view.window || {};
  return {
    direction: view.direction,
    from: Number(window.from) || 0,
    to: Number(window.to) || 0,
    phase: view.phase || 'preparing',
    cancelling: !!view.cancelling,
    tables,
    done,
    total,
    percent: view.phase === 'verifying' || view.phase === 'done' ? 100 : percent(done, total),
    secondsLeft: typeof view.secondsLeft === 'number' && view.secondsLeft >= 0 ? view.secondsLeft : -1,
  };
}

// resultOf: how the last copy ended, for an alert, or null.
// severity is MUI's; text is the sentence; tables lists the disagreements.
export function resultOf(view, t = identity, locale) {
  const result = resultSentence(view, t, locale);
  const last = view && view.last;
  if (result && last && Number(last.replaced) > 0) {
    // values that are not numbers (NaN, infinities), which the local file cannot hold
    result.note = t('mgmt.config.general.monitoringDb.copy.replaced', '{{n}} scale values that are not numbers were stored as 1, the neutral scale.', { n: formatCount(last.replaced) });
  }
  return result;
}

function resultSentence(view, t, locale) {
  const last = view && view.last;
  if (!last || !last.outcome) return null;
  const vars = { n: formatCount(last.written), time: formatTime(last.upTo, locale), message: last.message || '' };
  const tables = (last.tables || []).filter((p) => p.state === 'mismatch').map((p) => ({
    table: p.table,
    label: tableLabel(p.table, t),
    sourceRows: Number(p.sourceRows) || 0,
    targetRows: Number(p.targetRows) || 0,
    missing: Number(p.missing) || 0,
  }));
  switch (last.outcome) {
    case 'done':
      if (!(Number(last.written) > 0)) {
        return { outcome: 'done', severity: 'success', tables, text: t('mgmt.config.general.monitoringDb.copy.doneNothing', 'Nothing was missing: both stores already hold the same history up to {{time}}.', vars) };
      }
      return { outcome: 'done', severity: 'success', tables, text: t('mgmt.config.general.monitoringDb.copy.done', 'Copied {{n}} records; both stores now hold the same history up to {{time}}.', vars) };
    case 'mismatch':
      return { outcome: 'mismatch', severity: 'warning', tables, text: t('mgmt.config.general.monitoringDb.copy.mismatch', 'Copied {{n}} records, but the two stores do not agree on the tables below. Start the copy again: it adds what is missing.', vars) };
    case 'cancelled':
      return { outcome: 'cancelled', severity: 'info', tables, text: t('mgmt.config.general.monitoringDb.copy.cancelled', 'The copy was cancelled after a complete batch; what was copied stays. Start it again to resume where it stopped.', vars) };
    case 'stopped':
      return { outcome: 'stopped', severity: 'warning', tables, text: t('mgmt.config.general.monitoringDb.copy.stopped', 'The copy stopped: the monitoring store in use changed (PostgreSQL stopped answering, or the settings changed). What was copied stays; it can be resumed once PostgreSQL is in use again.', vars) };
    default:
      return { outcome: 'failed', severity: 'error', tables, text: t('mgmt.config.general.monitoringDb.copy.failed', 'The copy failed: {{message}}', vars) };
  }
}

// mismatchLine says one disagreement in words.
export function mismatchLine(row, t = identity) {
  return t('mgmt.config.general.monitoringDb.copy.mismatchLine', '{{table}}: {{source}} rows in the source, {{target}} found in the target', {
    table: row.label, source: formatCount(row.sourceRows), target: formatCount(row.targetRows),
  });
}

// pollDelay: when to ask again, by what the last answer said.
export function pollDelay(view) {
  return view && (view.running || countingOf(view)) ? POLL_RUNNING_MS : POLL_IDLE_MS;
}

// countingOf: the server is counting a store's records and has nothing to
// offer yet (the offer comes with the count).
export function countingOf(view) {
  if (!view || view.inUse !== 'postgres' || view.running) return false;
  return (view.offers || []).some((o) => o && o.counting && !o.available);
}

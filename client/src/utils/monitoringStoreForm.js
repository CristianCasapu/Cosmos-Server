// Fork addition (plan 51): the logic of the monitoring database form around a
// save (pages/config/configExternal.jsx, forkPostgresProposals.jsx,
// pages/config/index.jsx): which store a save moves to and the sentence said
// before it, the hint next to a masked password that would be refused, the
// host choices made from the proposals, the server's refusal of a database
// that does not answer and "Save anyway". Pure and tested
// (monitoringStoreForm.test.mjs).

const trim = (value) => String(value || '').trim();

// The store some settings name: null for the local file, else server and database.
export const storeOf = (db) => {
  const host = trim(db && (db.PostgresHost !== undefined ? db.PostgresHost : db.host));
  if (!host) return null;
  const database = trim(db.PostgresDatabase !== undefined ? db.PostgresDatabase : db.database) || 'cosmos';
  return { host, database };
};

// What a save does to the store: null when it stays where it is.
// { from: 'local'|'postgres', to: 'local'|'postgres', fromName, toName }
export const storeMove = (saved, next) => {
  const a = storeOf(saved);
  const b = storeOf(next);
  if (!a && !b) return null;
  if (a && b && a.host === b.host && a.database === b.database) return null;
  return {
    from: a ? 'postgres' : 'local',
    to: b ? 'postgres' : 'local',
    fromName: a ? a.database + ' at ' + a.host : '',
    toName: b ? b.database + ' at ' + b.host : '',
  };
};

// The key of a move, so that it is asked once: a refusal, "Save anyway" and a
// corrected server (the same kind of move from the same store) do not ask again.
export const moveKey = (move) => (move ? [move.from, move.fromName, move.to].join('|') : '');

// The one sentence said before the save: [key, English, params].
export const moveSentence = (move) => {
  if (!move) return null;
  if (move.from === 'local') {
    return ['mgmt.config.general.monitoringDb.fork.move.toPostgres',
      'Monitoring will record into the database {{to}} from now on. The records so far stay in the local file and are not copied now: the charts start again from now. After the save, Cosmos offers to copy the history into the database; switching back to the local file shows it again.',
      { to: move.toName }];
  }
  if (move.to === 'local') {
    return ['mgmt.config.general.monitoringDb.fork.move.toLocal',
      'Monitoring will record into the local file from now on. The records in the database {{from}} stay there and are not copied: the charts show the local file\'s history again (up to when you switched to the database) and go on from now. To keep them, copy them into the local file first (Copy the history, above), then save.',
      { from: move.fromName }];
  }
  return ['mgmt.config.general.monitoringDb.fork.move.toOther',
    'Monitoring will record into the database {{to}} from now on. The records in {{from}} stay there and are not copied: the charts start again from now; switching back shows them again.',
    { from: move.fromName, to: move.toName }];
};

// The password field still holds the saved (masked) password while the
// server, port, database or user changed: the server refuses that save (UC046).
export const maskedPasswordStale = (values, saved) => {
  const v = values || {};
  const s = saved || {};
  if (String(v.PostgresPassword || '') !== '***' || !trim(v.PostgresHost)) return false;
  return ['PostgresHost', 'PostgresDatabase', 'PostgresUsername'].some((name) => trim(v[name]) !== trim(s[name]));
};

// The host choices: one per proposal with an address, then "Type it myself".
// [{ value, host, container, reach, source }]
export const hostChoices = (proposals) => {
  const seen = new Set();
  const out = [];
  for (const p of (proposals || [])) {
    const host = trim(p && p.host);
    if (!host || seen.has(host)) continue;
    seen.add(host);
    out.push({ value: host, host, container: String(p.container || ''), reach: p.reach || '', source: p.hostSource || '' });
  }
  return out;
};

// Which choice the host field holds: a proposal's host, or 'custom'.
export const pickedChoice = (host, choices) => {
  const h = trim(host);
  return (choices || []).some((c) => c.value === h) ? h : 'custom';
};

// What a host chip puts into the form: the host, and the database and the
// user of its proposal only where those fields are empty.
export const choiceValues = (values, proposal) => {
  const v = values || {};
  const out = { PostgresHost: trim(proposal && proposal.host) };
  if (!trim(v.PostgresDatabase) && proposal && proposal.database) out.PostgresDatabase = String(proposal.database);
  if (!trim(v.PostgresUsername) && proposal && proposal.user) out.PostgresUsername = String(proposal.user);
  return out;
};

// The server refused a save because the database did not pass the check
// (UC047): its sentence, and whether "Save anyway" is offered.
export const databaseRefusal = (error) => {
  if (!error || error.code !== 'UC047') return null;
  return { message: String(error.message || '').replace(/^Monitoring database: /, '') };
};

// "Save anyway": asked for by the button, taken once by the next save.
let saveAnyway = false;
export const requestSaveAnyway = () => { saveAnyway = true; };
export const takeSaveAnyway = () => { const was = saveAnyway; saveAnyway = false; return was; };

// The plain line about the store in use (GET /api/monitoring-store): null
// when nothing is wrong. [key, English, params]
export const storeStateLine = (state, formatDate = (d) => d) => {
  if (!state || !state.fallback) return null;
  const since = state.since ? formatDate(state.since) : '';
  if (state.host) {
    return ['mgmt.config.general.monitoringDb.fork.state.fallback',
      'The monitoring database at {{host}} does not answer since {{since}}: {{reason}} Records are kept in the local file (database.db) meanwhile; Cosmos tries again by itself and switches back as soon as it answers.',
      { host: state.host, since, reason: [state.message, state.advice].filter(Boolean).join(' ') }];
  }
  return ['mgmt.config.general.monitoringDb.fork.state.fallbackNoHost',
    'The monitoring database does not answer since {{since}}. Records are kept in the local file (database.db) meanwhile; Cosmos tries again by itself.',
    { since }];
};

// The server field holds a container's own address on Docker's network (the
// owner once saved 172.17.0.x:5432): it works now and changes when the
// container is made again. { container, address, prefer } (prefer: the
// address that stays, '' when the container publishes none), or null.
export const containerAddressWarning = (host, proposals) => {
  const h = trim(host);
  if (!h) return null;
  const bracket = h.match(/^\[([^\]]+)\]/);
  const address = bracket ? bracket[1] : ((h.match(/:/g) || []).length === 1 ? h.split(':')[0] : h);
  for (const p of (proposals || [])) {
    if (!p || !(p.addresses || []).includes(address)) continue;
    const prefer = p.host && p.hostSource !== 'address' && p.hostSource !== 'none' && p.host !== h ? String(p.host) : '';
    return { container: String(p.container || ''), address, prefer };
  }
  return null;
};

// Its sentence: [key, English, params].
export const containerAddressSentence = (warning) => {
  if (!warning) return null;
  if (warning.prefer) {
    return ['mgmt.config.general.monitoringDb.fork.bridgePrefer',
      '{{address}} is the address of the container {{container}} on Docker\'s network: it works now, but it changes when the container is made again (an update, an edit) or the containers start in another order. Prefer {{prefer}}, the address it is published on.',
      warning];
  }
  return ['mgmt.config.general.monitoringDb.fork.bridgePublish',
    '{{address}} is the address of the container {{container}} on Docker\'s network: it works now, but it changes when the container is made again (an update, an edit) or the containers start in another order. Better: publish its port 5432 on 127.0.0.1 (in the container\'s Network tab), then use 127.0.0.1:5432.',
    warning];
};

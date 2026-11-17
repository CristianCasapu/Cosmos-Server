// Fork addition (plan 47): the logic of the monitoring database form
// (pages/config/configExternal.jsx): the shapes of its fields (the same rules
// as the server's utils.PostgresFieldsProblem), what a proposal of
// GET /api/config/postgres-proposals puts into the form, and what the
// connection test sends. A proposal never carries a password: the form asks
// for it separately, on a click (the recorded reveal of the sign-in details).
// Pure and tested (postgresProposal.test.mjs).
import { applyProposal, acceptProposal } from './proposals.js';

export const POSTGRES_FIELDS = ['PostgresHost', 'PostgresDatabase', 'PostgresUsername'];

const HOST_NAME = /^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$/;
const IDENT = /^[A-Za-z0-9_][A-Za-z0-9_.$-]{0,62}$/;
const IPV4 = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/;
const looksLikeIPv6 = (text) => /^[0-9A-Fa-f:.]+$/.test(text) && text.includes(':');

// Why the database server cannot be used, or null: 'shape' | 'port'.
export const postgresHostProblem = (value) => {
  const host = String(value || '').trim();
  if (!host) return null; // the built-in database
  let name = host;
  let port = '';
  const bracketed = host.match(/^\[([^\]]+)\](?::(.*))?$/);
  if (bracketed) {
    name = bracketed[1];
    if (!looksLikeIPv6(name) || bracketed[2] === undefined) return 'shape';
    port = bracketed[2];
  } else if ((host.match(/:/g) || []).length === 1) {
    [name, port] = host.split(':');
  } else if (host.includes(':')) {
    return looksLikeIPv6(host) ? null : 'shape';
  }
  if (port !== '' || host.endsWith(':')) {
    if (!/^\d{1,5}$/.test(port) || Number(port) < 1 || Number(port) > 65535 || String(Number(port)) !== port) return 'port';
  }
  if (!bracketed && !IPV4.test(name) && !HOST_NAME.test(name)) return 'shape';
  return null;
};

// Why a database or user name cannot be used, or null.
export const postgresNameProblem = (value) => {
  const text = String(value || '').trim();
  return !text || IDENT.test(text) ? null : 'shape';
};

// The fields a proposal fills: never the password.
export const proposalValues = (proposal) => {
  const p = proposal || {};
  return {
    PostgresHost: String(p.host || ''),
    PostgresDatabase: String(p.database || ''),
    PostgresUsername: String(p.user || ''),
  };
};

// "Use these": the three fields go together, and only where the form holds
// nothing of the administrator's own (empty, or what an earlier proposal put
// there). `held` lists the fields left alone.
export const takeProposal = (values, proposal, marks = {}) => applyProposal(values, proposalValues(proposal), marks, [POSTGRES_FIELDS]);

// "Replace what is typed": the administrator asked for it in so many words.
export const replaceWithProposal = (values, proposal) => acceptProposal(values, proposalValues(proposal));

// Whether the form holds this proposal now.
export const holdsProposal = (values, proposal) => {
  const wanted = proposalValues(proposal);
  return POSTGRES_FIELDS.every((name) => String((values || {})[name] || '').trim() === wanted[name]);
};

// What the connection test sends. An untouched masked password stays the
// mask: the server then uses the saved one, for the saved settings only.
export const testPayload = (values) => {
  const v = values || {};
  return {
    host: String(v.PostgresHost || '').trim(),
    database: String(v.PostgresDatabase || '').trim(),
    username: String(v.PostgresUsername || '').trim(),
    password: String(v.PostgresPassword || ''),
  };
};

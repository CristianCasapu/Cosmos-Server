// Fork addition: tests of proposals.js. Run with: node --test "client/src/utils/*.test.mjs"
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { applyProposal, acceptProposal, stillProposed, isEmptyValue } from './proposals.js';

const SERVER = [['host', 'port', 'tls'], ['user']];

test('a proposal fills empty fields and remembers what it put there', () => {
  const out = applyProposal({ host: '', port: '', tls: false, user: '' },
    { host: 'smtp.example.com', port: '465', tls: true, user: 'me@example.com' }, {}, SERVER);
  assert.deepEqual(out.values, { host: 'smtp.example.com', port: '465', tls: true, user: 'me@example.com' });
  assert.deepEqual(out.marks, out.values);
  assert.deepEqual(out.held, []);
});

test('a typed value is never replaced; the proposal is held beside it', () => {
  const out = applyProposal({ host: 'mail.mine.example', port: '587', tls: true, user: '' },
    { host: 'smtp.example.com', port: '465', tls: true, user: 'me@example.com' }, {}, SERVER);
  assert.deepEqual(out.values, { user: 'me@example.com' });
  assert.deepEqual(out.held, ['host', 'port']);
  assert.equal(out.marks.host, undefined);
  assert.equal(out.marks.user, 'me@example.com');
});

test('a value loaded from the saved settings counts as typed', () => {
  // nothing was ever proposed here: no marks
  const out = applyProposal({ user: 'saved-user' }, { user: 'me@example.com' });
  assert.deepEqual(out.values, {});
  assert.deepEqual(out.held, ['user']);
});

test('an earlier proposal that was not touched is replaced by the next one', () => {
  const first = applyProposal({ host: '', port: '', tls: false, user: '' },
    { host: 'smtp.gmail.com', port: '465', tls: true, user: 'me@gmail.com' }, {}, SERVER);
  const form = { host: 'smtp.gmail.com', port: '465', tls: true, user: 'me@gmail.com' };
  const second = applyProposal(form, { host: 'smtp.mail.me.com', port: '587', tls: true, user: 'me@icloud.com' }, first.marks, SERVER);
  assert.deepEqual(second.values, { host: 'smtp.mail.me.com', port: '587', user: 'me@icloud.com' });
  assert.deepEqual(second.held, []);
});

test('typing in a proposed field makes it the administrator\'s own', () => {
  const first = applyProposal({ host: '', port: '', tls: false, user: '' },
    { host: 'smtp.example.com', port: '465', tls: true, user: 'me@example.com' }, {}, SERVER);
  // the admin changes the user name; the server stays as proposed
  const form = { host: 'smtp.example.com', port: '465', tls: true, user: 'postmaster' };
  assert.equal(stillProposed(form, first.marks, 'user'), false);
  assert.equal(stillProposed(form, first.marks, 'host'), true);
  const second = applyProposal(form, { host: 'mail.example.org', port: '587', tls: true, user: 'me@example.org' }, first.marks, SERVER);
  assert.deepEqual(second.values, { host: 'mail.example.org', port: '587' });
  assert.deepEqual(second.held, ['user']);
});

test('fields that go together are filled together or not at all', () => {
  // the admin typed the port only: the proposed server is not put next to it
  const out = applyProposal({ host: '', port: '2525', tls: false },
    { host: 'smtp.example.com', port: '465', tls: true }, {}, SERVER);
  assert.deepEqual(out.values, {});
  assert.deepEqual(out.held, ['host', 'port', 'tls']);

  // one touched field of a proposed unit makes the whole unit the admin's
  const first = applyProposal({ host: '', port: '', tls: false }, { host: 'a.example.com', port: '465', tls: true }, {}, SERVER);
  const touched = { host: 'a.example.com', port: '587', tls: true };
  const second = applyProposal(touched, { host: 'b.example.com', port: '465', tls: true }, first.marks, SERVER);
  assert.deepEqual(second.values, {});
  assert.deepEqual(second.held, ['host', 'port']);
  assert.deepEqual(second.marks, {});
});

test('a switch follows the fields of its unit and is never empty by itself', () => {
  // a switch the admin turned off next to proposed text fields is the admin's own
  const first = applyProposal({ host: '', port: '', tls: false }, { host: 'a.example.com', port: '587', tls: true }, {}, SERVER);
  const off = { host: 'a.example.com', port: '587', tls: false };
  const second = applyProposal(off, { host: 'b.example.com', port: '465', tls: true }, first.marks, SERVER);
  assert.deepEqual(second.values, {});
  // a switch alone is only replaced while it still holds a proposal
  assert.deepEqual(applyProposal({ on: false }, { on: true }).values, {});
  assert.deepEqual(applyProposal({ on: false }, { on: true }, { on: false }).values, { on: true });
});

test('a proposal only speaks for the fields it has', () => {
  const out = applyProposal({ host: '', port: '', tls: false, user: 'typed' },
    { host: 'smtp.example.com', port: '587', tls: true, user: undefined }, {}, SERVER);
  assert.deepEqual(out.values, { host: 'smtp.example.com', port: '587', tls: true });
  assert.deepEqual(out.held, []);
});

test('"use these" sets everything, and it can be replaced again later', () => {
  const out = acceptProposal({ host: 'mine.example', port: '587', tls: true, user: 'me' },
    { host: 'smtp.example.com', port: '465', tls: true, user: 'me@example.com' });
  assert.deepEqual(out.values, { host: 'smtp.example.com', port: '465', user: 'me@example.com' });
  assert.deepEqual(out.marks, { host: 'smtp.example.com', port: '465', tls: true, user: 'me@example.com' });
});

test('numbers and text compare as the form shows them', () => {
  assert.equal(stillProposed({ port: 465 }, { port: '465' }, 'port'), true);
  assert.equal(isEmptyValue('   '), true);
  assert.equal(isEmptyValue(0), false);
  assert.equal(isEmptyValue(false), false);
});

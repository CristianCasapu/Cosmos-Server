// Fork addition: tests of userForm.js. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { nicknameProblem, emailProblem, inviteWay, roleChoices } from './userForm.js';

test('a nickname is checked with the server\'s rule before the click', () => {
  assert.equal(nicknameProblem('', []), 'required');
  assert.equal(nicknameProblem('   ', []), 'required');
  assert.equal(nicknameProblem('ab', []), 'short');
  assert.equal(nicknameProblem('abc', []), null);
  assert.equal(nicknameProblem('a'.repeat(32), []), null);
  assert.equal(nicknameProblem('a'.repeat(33), []), 'long');
  // what the server answered with "Key: 'CreateRequestJSON.Nickname' Error:Field validation ... 'alphanum'"
  assert.equal(nicknameProblem('jean.marc', []), 'chars');
  assert.equal(nicknameProblem('jean marc', []), 'chars');
  assert.equal(nicknameProblem('ana-maria', []), 'chars');
  assert.equal(nicknameProblem('ștefan', []), 'chars');
  assert.equal(nicknameProblem('Maria2', []), null);
});

test('a nickname that exists is refused whatever its case', () => {
  assert.equal(nicknameProblem('Admin', ['admin', 'maria']), 'taken');
  assert.equal(nicknameProblem('maria', ['admin', 'Maria']), 'taken');
  assert.equal(nicknameProblem('mario', ['admin', 'maria']), null);
  assert.equal(nicknameProblem('mario', undefined), null);
});

test('an email address is optional, and checked when given', () => {
  assert.equal(emailProblem(''), null);
  assert.equal(emailProblem(undefined), null);
  assert.equal(emailProblem('maria@example.org'), null);
  assert.equal(emailProblem(' maria@example.org '), null);
  for (const bad of ['maria', 'maria@', '@example.org', 'maria@example', 'maria @example.org', 'maria@example.', 'a@b.c' + 'd'.repeat(260)]) {
    assert.equal(emailProblem(bad), 'invalid', bad);
  }
});

test('the dialog knows whether a mail goes out or a link is to be passed on', () => {
  assert.equal(inviteWay({ email: 'maria@example.org', canSendEmail: true }), 'mail');
  assert.equal(inviteWay({ email: 'maria@example.org', canSendEmail: false }), 'linkNoMail');
  assert.equal(inviteWay({ email: '', canSendEmail: true }), 'linkNoAddress');
  assert.equal(inviteWay({ email: '  ', canSendEmail: true }), 'linkNoAddress');
  assert.equal(inviteWay({}), 'linkNoAddress');
});

test('the roles as choices: User first, the server\'s own roles after the built-in ones', () => {
  assert.deepEqual(roleChoices({ 0: 'Guest', 1: 'User', 2: 'Admin' }).map((role) => [role.value, role.kind]), [[1, 'user'], [0, 'guest'], [2, 'admin']]);
  const withGroup = roleChoices({ 0: 'Guest', 1: 'User', 2: 'Admin', 10: 'Family' });
  assert.deepEqual(withGroup.map((role) => role.name), ['User', 'Guest', 'Admin', 'Family']);
  assert.equal(withGroup[3].kind, 'other');
  assert.deepEqual(roleChoices(undefined), []);
});

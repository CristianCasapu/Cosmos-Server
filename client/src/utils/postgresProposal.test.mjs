// Fork addition: tests of postgresProposal.js. Run with: node --test "client/src/utils/*.test.mjs"
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { postgresHostProblem, postgresNameProblem, proposalValues, takeProposal, replaceWithProposal, holdsProposal, testPayload } from './postgresProposal.js';

const PROPOSAL = { container: 'notes-db', host: '127.0.0.1:15432', hostSource: 'published', database: 'notes', user: 'owner_name', passwordSecret: 'password.0', password: 'must-not-travel' };
const EMPTY = { PostgresHost: '', PostgresDatabase: '', PostgresUsername: '', PostgresPassword: '' };

test('the host has the shapes the server accepts', () => {
  for (const good of ['', '127.0.0.1', '127.0.0.1:5432', 'db.example.org', 'notes-db:5432', '[2001:db8::1]:5432', '2001:db8::1']) {
    assert.equal(postgresHostProblem(good), null, good);
  }
  for (const [bad, why] of [['http://db:5432', 'shape'], ['db:0', 'port'], ['db:99999', 'port'], ['db:', 'port'], ['db/x', 'shape'], ['db:5432/cosmos', 'port'], ['a b', 'shape'], ['[nothost]:5432', 'shape'], ['[2001:db8::1]', 'shape']]) {
    assert.equal(postgresHostProblem(bad), why, bad);
  }
});

test('database and user names are plain', () => {
  assert.equal(postgresNameProblem(''), null);
  assert.equal(postgresNameProblem('owner_name'), null);
  assert.equal(postgresNameProblem('a;drop'), 'shape');
  assert.equal(postgresNameProblem('d'.repeat(64)), 'shape');
});

test('a proposal never carries a password, whatever the answer holds', () => {
  const values = proposalValues(PROPOSAL);
  assert.deepEqual(Object.keys(values).sort(), ['PostgresDatabase', 'PostgresHost', 'PostgresUsername']);
  assert.ok(!JSON.stringify(values).includes('must-not-travel'));
  const used = takeProposal(EMPTY, PROPOSAL);
  assert.ok(!('PostgresPassword' in used.values));
  assert.ok(!JSON.stringify(used).includes('must-not-travel'));
  assert.ok(!('PostgresPassword' in replaceWithProposal(EMPTY, PROPOSAL).values));
});

test('"Use these" fills empty fields, never what was typed', () => {
  const used = takeProposal(EMPTY, PROPOSAL);
  assert.deepEqual(used.values, { PostgresHost: '127.0.0.1:15432', PostgresDatabase: 'notes', PostgresUsername: 'owner_name' });
  assert.deepEqual(used.held, []);
  const typed = { ...EMPTY, PostgresHost: 'mine.example.org' };
  const kept = takeProposal(typed, PROPOSAL);
  assert.deepEqual(kept.values, {});
  assert.ok(kept.held.includes('PostgresHost'));
  // a second proposal replaces the first one as long as nothing was typed over it
  const after = { ...EMPTY, ...used.values };
  const other = takeProposal(after, { host: '172.18.0.4:5432', database: 'app', user: 'app' }, used.marks);
  assert.deepEqual(other.values, { PostgresHost: '172.18.0.4:5432', PostgresDatabase: 'app', PostgresUsername: 'app' });
  // "Replace what is typed" is the administrator's word
  assert.deepEqual(replaceWithProposal(typed, PROPOSAL).values.PostgresHost, '127.0.0.1:15432');
  assert.ok(holdsProposal(after, PROPOSAL));
  assert.ok(!holdsProposal(typed, PROPOSAL));
});

test('the test sends the mask when the password was not touched', () => {
  assert.deepEqual(testPayload({ PostgresHost: ' db:5432 ', PostgresDatabase: 'cosmos', PostgresUsername: 'cosmos', PostgresPassword: '***' }),
    { host: 'db:5432', database: 'cosmos', username: 'cosmos', password: '***' });
});

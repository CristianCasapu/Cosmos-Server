// Fork addition: tests of cronJobForm.js. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { jobNameProblem, jobCommandProblem, jobRunsIn } from './cronJobForm.js';

test('a job needs a name', () => {
  // upstream saved it under the name ""
  assert.equal(jobNameProblem('', []), 'required');
  assert.equal(jobNameProblem('   ', ['a']), 'required');
  assert.equal(jobNameProblem(undefined, undefined), 'required');
  assert.equal(jobNameProblem('Backup photos', []), null);
});

test('a new job cannot take the name of another: it would replace it', () => {
  assert.equal(jobNameProblem('Backup photos', ['Backup photos', 'Clean up']), 'taken');
  assert.equal(jobNameProblem(' Backup photos ', ['Backup photos']), 'taken');
  assert.equal(jobNameProblem('Backup films', ['Backup photos', 'Clean up']), null);
});

test('an edited job keeps its name, and cannot be renamed onto another job', () => {
  assert.equal(jobNameProblem('Backup photos', ['Backup photos', 'Clean up'], 'Backup photos'), null);
  assert.equal(jobNameProblem('Clean up', ['Backup photos', 'Clean up'], 'Backup photos'), 'taken');
  assert.equal(jobNameProblem('Backup all', ['Backup photos', 'Clean up'], 'Backup photos'), null);
});

test('a job needs something to run', () => {
  assert.equal(jobCommandProblem(''), 'required');
  assert.equal(jobCommandProblem('  '), 'required');
  assert.equal(jobCommandProblem(undefined), 'required');
  assert.equal(jobCommandProblem('ls /storage'), null);
});

test('the command runs on the server, or in the container that is picked', () => {
  assert.equal(jobRunsIn(''), 'server');
  assert.equal(jobRunsIn(undefined), 'server');
  assert.equal(jobRunsIn('jellyfin'), 'container');
});

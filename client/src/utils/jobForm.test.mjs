// Fork addition: tests of jobForm.js. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  scheduleExpression, readSchedule, nextRuns, placeOf, containerChoices, proposalFields, DEFAULT_SCHEDULE,
} from './jobForm.js';
import { isValidCron } from './cronExpression.js';

test('each choice writes an expression the server takes, and reads back as itself', () => {
  const choices = [
    { kind: 'hourly' },
    { kind: 'nightly', time: '03:00' },
    { kind: 'nightly', time: '23:45' },
    { kind: 'weekly', time: '02:30', weekday: 1 },
    { kind: 'weekly', time: '00:05', weekday: 0 },
    { kind: 'monthly', time: '04:15' },
  ];
  for (const choice of choices) {
    const expression = scheduleExpression(choice);
    assert.ok(isValidCron(expression), expression);
    const back = readSchedule(expression);
    assert.equal(back.kind, choice.kind, expression);
    if (choice.time) assert.equal(back.time, choice.time);
    if (choice.kind === 'weekly') assert.equal(back.weekday, choice.weekday);
  }
  assert.equal(scheduleExpression({ kind: 'nightly', time: '03:00' }), '0 0 3 * * *');
  assert.equal(scheduleExpression({ kind: 'weekly', time: '02:30', weekday: 1 }), '0 30 2 * * 1');
  assert.equal(scheduleExpression({ kind: 'monthly', time: '04:15' }), '0 15 4 1 * *');
  assert.equal(scheduleExpression(DEFAULT_SCHEDULE), '0 0 3 * * *');
});

test('a choice without a usable time writes nothing, and custom is never written', () => {
  assert.equal(scheduleExpression({ kind: 'nightly', time: '25:00' }), null);
  assert.equal(scheduleExpression({ kind: 'nightly', time: '3:00' }), null);
  assert.equal(scheduleExpression({ kind: 'weekly', time: '03:00', weekday: 7 }), null);
  assert.equal(scheduleExpression({ kind: 'custom' }), null);
  assert.equal(scheduleExpression(null), null);
});

test('a saved expression no choice writes opens as typed', () => {
  for (const expression of ['0 */15 * * * *', '0 0 3 * * mon', '30 0 3 * * *', '0 0 3 2 * *', '0 0 3 * * 1-5', '@daily', '0 3 * * *', '', 'nonsense', '0 03 3 * * *']) {
    assert.equal(readSchedule(expression).kind, 'custom', expression);
  }
});

test('the next runs follow each other', () => {
  const from = { year: 2026, month: 11, day: 17, hour: 10, minute: 0, second: 0 };
  const runs = nextRuns('0 0 3 * * *', from, 3);
  assert.deepEqual(runs.map((r) => [r.day, r.hour, r.minute]), [[18, 3, 0], [19, 3, 0], [20, 3, 0]]);
  const weekly = nextRuns('0 30 2 * * 1', from, 3); // 2026-11-17 is a Tuesday
  assert.deepEqual(weekly.map((r) => [r.month, r.day]), [[11, 23], [11, 30], [12, 7]]);
  const minute = nextRuns('0 * * * * *', { ...from, minute: 59 }, 2);
  assert.deepEqual(minute.map((r) => [r.hour, r.minute]), [[11, 0], [11, 1]]);
  assert.deepEqual(nextRuns('nonsense', from), []);
  assert.deepEqual(nextRuns('0 0 3 31 2 *', from), []);
});

test('where the job runs, and the containers to choose from', () => {
  assert.equal(placeOf(''), 'server');
  assert.equal(placeOf('  '), 'server');
  assert.equal(placeOf('db'), 'container');
  assert.deepEqual(containerChoices([
    { Names: ['/web'], State: 'exited' },
    { Names: ['/db'], State: 'running' },
    { Names: [] },
    null,
  ]), [{ name: 'db', state: 'running', running: true }, { name: 'web', state: 'exited', running: false }]);
  assert.deepEqual(containerChoices(undefined), []);
});

test('a proposed job fills the name, the command and the schedule', () => {
  assert.deepEqual(proposalFields({ name: 'Nightly dump of db', command: 'pg_dumpall', schedule: '0 0 3 * * *' }),
    { Name: 'Nightly dump of db', Command: 'pg_dumpall', Crontab: '0 0 3 * * *' });
});

test('the next runs of a five-field schedule are shown too', () => {
  const from = { year: 2026, month: 11, day: 17, hour: 12, minute: 0, second: 0 };
  assert.deepEqual(nextRuns('0 3 * * *', from), nextRuns('0 0 3 * * *', from));
  assert.equal(nextRuns('0 3 * * *', from).length, 3);
});

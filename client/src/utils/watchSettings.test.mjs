import { test } from 'node:test';
import assert from 'node:assert/strict';
import { watchInitialValues, watchProblem, watchToConfig } from './watchSettings.js';

test('nothing saved: everything watched, 3 days, the schedule decides; saved back as nothing', () => {
  const values = watchInitialValues({});
  assert.equal(values.Watch_SMART, true);
  assert.equal(values.Watch_FillDays, '3');
  assert.equal(values.Watch_SnapshotLateHours, '');
  assert.deepEqual(watchToConfig(values), { SMARTOff: false, FillOff: false, FillDays: 0, DockerOff: false, SnapshotsOff: false, SnapshotLateHours: 0, CountryOff: false });
  assert.equal(watchProblem(values), '');
});

test('what was changed is kept and read back', () => {
  const values = { ...watchInitialValues({}), Watch_Country: false, Watch_FillDays: '7', Watch_SnapshotLateHours: '48' };
  const saved = watchToConfig(values);
  assert.equal(saved.CountryOff, true);
  assert.equal(saved.FillDays, 7);
  assert.equal(saved.SnapshotLateHours, 48);
  const again = watchInitialValues({ Watch: saved });
  assert.equal(again.Watch_FillDays, '7');
  assert.equal(again.Watch_Country, false);
});

test('out of range values are refused', () => {
  const base = watchInitialValues({});
  for (const bad of [{ Watch_FillDays: '0' }, { Watch_FillDays: '31' }, { Watch_FillDays: 'x' }]) assert.equal(watchProblem({ ...base, ...bad }), 'days');
  for (const bad of [{ Watch_SnapshotLateHours: '0' }, { Watch_SnapshotLateHours: '9999' }, { Watch_SnapshotLateHours: '1.5' }]) assert.equal(watchProblem({ ...base, ...bad }), 'hours');
});

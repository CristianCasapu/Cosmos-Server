// Fork addition: tests of metricsState.js. Run: node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { metricsByKey, percentOf, hasMeasurements } from './metricsState.js';

const cpu = { Key: 'cosmos.system.cpu.0', Max: 100, Values: [{ Value: 3 }, { Value: 12.5 }] };
const ram = { Key: 'cosmos.system.ram', Max: 8000, Values: [{ Value: 2000 }] };

test('metricsByKey: an answer with nothing is an empty object, never undefined', () => {
  assert.deepEqual(metricsByKey(null), {});
  assert.deepEqual(metricsByKey(undefined), {});
  assert.deepEqual(metricsByKey([]), {});
  assert.deepEqual(metricsByKey('nonsense'), {});
  assert.deepEqual(metricsByKey([cpu, null, { no: 'key' }, ram]), { [cpu.Key]: cpu, [ram.Key]: ram });
});

test('metricsByKey: keeps what was known and replaces what came again', () => {
  const newer = { ...cpu, Values: [{ Value: 50 }] };
  const merged = metricsByKey([newer], { [cpu.Key]: cpu, [ram.Key]: ram });
  assert.equal(merged[cpu.Key], newer);
  assert.equal(merged[ram.Key], ram);
  // nothing new: what was known stays, in a new object
  const previous = { [ram.Key]: ram };
  const same = metricsByKey(null, previous);
  assert.deepEqual(same, previous);
  assert.notEqual(same, previous);
});

test('percentOf: a whole percentage, or null instead of NaN', () => {
  assert.equal(percentOf(2000, 8000), 25);
  assert.equal(percentOf(0, 8000), 0);
  assert.equal(percentOf(9000, 8000), 100);
  assert.equal(percentOf(-5, 8000), 0);
  // what gave "NaN%": nothing measured, or a maximum of zero
  assert.equal(percentOf(undefined, 0), null);
  assert.equal(percentOf(null, 8000), null);
  assert.equal(percentOf(2000, 0), null);
  assert.equal(percentOf(2000, undefined), null);
  assert.equal(percentOf(NaN, 8000), null);
});

test('hasMeasurements: at least one metric with a value', () => {
  assert.equal(hasMeasurements(null), false);
  assert.equal(hasMeasurements({}), false);
  assert.equal(hasMeasurements({ a: { Values: [] }, b: null, c: {} }), false);
  assert.equal(hasMeasurements({ a: { Values: [] }, b: ram }), true);
});

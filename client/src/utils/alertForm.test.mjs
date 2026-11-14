// Fork addition: tests of alertForm.js. Run with: node --test "client/src/utils/*.test.mjs"
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { kindOf, unitOf, percentMakesSense, actionsFor, templates, readBack, alertProblems } from './alertForm.js';

const METRICS = {
  'cosmos.system.disk./': 'Disk /', 'cosmos.system.disk./storage': 'Disk /storage', 'cosmos.system.ram': 'RAM',
  'cosmos.system.cpu.0': 'CPU 0', 'cosmos.system.temp.all': 'Temperature - All', 'cosmos.system.docker.cpu.radarr': 'radarr CPU',
  'cosmos.system.disk-health.temperature.sda': 'sda temperature',
};

test('the metric names are the server\'s', () => {
  const go = readFileSync(new URL('../../../src/metrics/system.go', import.meta.url), 'utf8');
  for (const name of ['"system.disk."', '"system.ram"', '"system.cpu."', '"system.temp.all"', '"system.docker.cpu."', '"system.disk-health.temperature."']) {
    assert.ok(go.includes(name), name);
  }
  const alerts = readFileSync(new URL('../../../src/metrics/alerts.go', import.meta.url), 'utf8');
  assert.match(alerts, /float64\(Value\) \/ float64\(metric\.Max\) \* 100/); // percent of the maximum
  assert.match(alerts, /time\.Hour \* 24/); // throttled: once a day
});

test('each metric has its unit, and percent only where there is a maximum', () => {
  assert.equal(kindOf('cosmos.system.disk-health.temperature.sda').kind, 'temp');
  assert.equal(unitOf('cosmos.system.disk./', true)[0], 'mgmt.monitoring.alerts.fork.unitDiskPercent');
  assert.equal(unitOf('cosmos.system.temp.all', false)[1], '°C');
  assert.ok(percentMakesSense('cosmos.system.ram'));
  assert.ok(!percentMakesSense('cosmos.system.cpu.0'));
  assert.deepEqual(actionsFor('cosmos.system.ram'), ['notification', 'email']);
  assert.ok(actionsFor('cosmos.system.docker.cpu.*').includes('stop'));
});

test('ready-made alerts come from the metrics the server has', () => {
  const ids = templates(METRICS).map((x) => x.id);
  assert.deepEqual(ids, ['disk:cosmos.system.disk./', 'disk:cosmos.system.disk./storage', 'ram', 'cpu', 'temp']);
  assert.deepEqual(templates({ 'cosmos.system.ram': 'RAM' }).map((x) => x.id), ['ram']);
  for (const tpl of templates(METRICS)) {
    assert.deepEqual(alertProblems(tpl.values, { metrics: METRICS, names: [] }), {}, tpl.id);
  }
  // the name of a disk alert has no "/": it is part of the URL of /api/alerts/{name}
  assert.ok(!templates(METRICS)[1].values.name.includes('/'));
});

test('the alert reads back in words', () => {
  const r = readBack(templates(METRICS)[0].values, METRICS);
  assert.equal(r.when[2].what, 'Disk /');
  assert.equal(r.op[1], 'above');
  assert.equal(r.period[1], 'at any measurement');
  assert.equal(r.actions[0][1], 'show a notification in Cosmos');
  assert.equal(r.throttled[0], 'mgmt.monitoring.alerts.fork.readOnce');
});

test('mistakes are caught before saving', () => {
  const ok = templates(METRICS)[2].values;
  assert.deepEqual(Object.keys(alertProblems({ ...ok, name: '' }, { metrics: METRICS })), ['name']);
  assert.deepEqual(Object.keys(alertProblems({ ...ok, name: 'a/b' }, { metrics: METRICS })), ['name']);
  assert.deepEqual(Object.keys(alertProblems(ok, { metrics: METRICS, names: ['Memory nearly full'] })), ['name']);
  assert.deepEqual(alertProblems(ok, { metrics: METRICS, names: ['Memory nearly full'], original: 'Memory nearly full' }), {});
  assert.deepEqual(Object.keys(alertProblems({ ...ok, trackingMetric: 'cosmos.system.rma' }, { metrics: METRICS })), ['trackingMetric']);
  assert.deepEqual(alertProblems({ ...ok, trackingMetric: 'cosmos.system.docker.cpu.*', conditionPercent: false }, { metrics: METRICS }), {});
  assert.deepEqual(Object.keys(alertProblems({ ...ok, conditionValue: '90%' }, { metrics: METRICS })), ['conditionValue']);
  assert.deepEqual(Object.keys(alertProblems({ ...ok, conditionValue: 150 }, { metrics: METRICS })), ['conditionValue']);
  assert.deepEqual(Object.keys(alertProblems({ ...ok, actions: [{ Type: 'stop' }] }, { metrics: METRICS })), ['actions']);
  assert.deepEqual(Object.keys(alertProblems({ ...ok, trackingMetric: 'cosmos.system.cpu.0', conditionPercent: true, conditionValue: 50 }, { metrics: METRICS })), ['conditionPercent']);
});

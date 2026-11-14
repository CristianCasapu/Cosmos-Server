// Fork addition: tests of health.js. Run: node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { failedJobs, brokenContainers, exitCode, fullDisks, diskKeys, snapshotProblems, healthLines, calmLine, oomCandidates, oomKilled, isThrottled, healthState, unreadLine } from './health.js';

const NEVER = '0001-01-01T00:00:00Z';

test('a scheduled job counts as failed only after a run that failed', () => {
  const jobs = {
    Custom: {
      backup: { Name: 'backup', LastRun: '2026-11-14T02:00:00Z', LastRunSuccess: false },
      ok: { Name: 'ok', LastRun: '2026-11-14T02:00:00Z', LastRunSuccess: true },
      never: { Name: 'never', LastRun: NEVER, LastRunSuccess: false },
      off: { Name: 'off', Disabled: true, LastRun: '2026-11-14T02:00:00Z', LastRunSuccess: false },
      running: { Name: 'running', Running: true, LastRun: '2026-11-14T02:00:00Z', LastRunSuccess: false },
    },
    SnapRAID: { sync: { Name: 'sync', LastRun: '2026-11-13T02:00:00Z', LastRunSuccess: false } },
  };
  assert.deepEqual(failedJobs(jobs), [{ scheduler: 'Custom', name: 'backup' }, { scheduler: 'SnapRAID', name: 'sync' }]);
  assert.deepEqual(failedJobs(null), []);
  assert.deepEqual(failedJobs({ Custom: null }), []);
});

test('an app stopped by an error or in a restart loop, not one stopped on purpose', () => {
  assert.equal(exitCode('Exited (3) 2 minutes ago'), 3);
  assert.equal(exitCode('Up 3 hours'), null);
  const list = [
    { Names: ['/jellyfin'], State: 'running', Status: 'Up 2 hours' },
    { Names: ['/pihole'], State: 'exited', Status: 'Exited (137) 3 hours ago' },
    { Names: ['/vault'], State: 'exited', Status: 'Exited (0) 1 day ago' },
    { Names: ['/term'], State: 'exited', Status: 'Exited (143) 1 day ago' },
    { Names: ['/crashed'], State: 'exited', Status: 'Exited (1) 5 minutes ago' },
    { Names: ['/loop'], State: 'restarting', Status: 'Restarting (2) 3 seconds ago' },
    { Names: ['/gone'], State: 'dead', Status: 'Dead' },
    { Names: ['/new'], State: 'created', Status: 'Created' },
  ];
  assert.deepEqual(brokenContainers(list), [
    { name: 'crashed', why: 'exited', code: 1 },
    { name: 'loop', why: 'restarting' },
    { name: 'gone', why: 'dead' },
  ]);
  assert.deepEqual(brokenContainers(undefined), []);
});

test('disks from 90 % on, the fullest first', () => {
  const disk = (key, used, max) => ({ Key: key, Max: max, Values: [{ Value: 1 }, { Value: used }] });
  const metrics = [
    disk('cosmos.system.disk./', 50, 100),
    disk('cosmos.system.disk./storage', 91, 100),
    disk('cosmos.system.disk./archive', 97, 100),
    disk('cosmos.system.disk-health.temperature.sda', 99, 100),
    { Key: 'cosmos.system.disk./empty', Max: 0, Values: [{ Value: 5 }] },
    { Key: 'cosmos.system.disk./novalues', Max: 100, Values: [] },
  ];
  assert.deepEqual(fullDisks(metrics), [{ path: '/archive', percent: 97 }, { path: '/storage', percent: 91 }]);
  assert.deepEqual(diskKeys({ 'cosmos.system.disk./': 'Disk /', 'cosmos.system.disk-health.temperature.sda': 'T', 'cosmos.system.ram': 'RAM' }), ['cosmos.system.disk./']);
});

test('snapshots: only when switched on', () => {
  assert.deepEqual(snapshotProblems({ enabled: false, error: 'x' }), []);
  assert.deepEqual(snapshotProblems({ enabled: true, error: 'The destination does not exist', sources: [{ path: '/srv', lastError: 'disk not mounted' }, { path: '/ok', lastError: '' }] }),
    [{ path: '', error: 'The destination does not exist' }, { path: '/srv', error: 'disk not mounted' }]);
});

test('the lines: errors first, each with a link, plain text only', () => {
  const lines = healthLines({
    jobs: { Custom: { backup: { Name: 'backup', LastRun: '2026-11-14T02:00:00Z', LastRunSuccess: false } } },
    containers: [{ Names: ['/crashed'], State: 'exited', Status: 'Exited (1) now' }],
    disks: [{ Key: 'cosmos.system.disk./storage', Max: 100, Values: [{ Value: 92 }] }],
    snapshots: { enabled: true, sources: [{ path: '/srv', lastError: 'disk not mounted' }] },
  });
  assert.deepEqual(lines.map((l) => [l.id, l.severity]), [
    ['containers', 'error'], ['jobs', 'warning'], ['disk:/storage', 'warning'], ['snapshots', 'warning'],
  ]);
  assert.equal(lines[0].link, '/cosmos-ui/servapps/containers/crashed');
  assert.deepEqual(lines[0].text[2], { name: 'crashed', code: 1 });
  for (const line of lines) assert.ok(line.link.startsWith('/cosmos-ui/'), line.id);
  // several apps: one line that names them
  const many = healthLines({ containers: ['a', 'b', 'c', 'd'].map((n) => ({ Names: ['/' + n], State: 'restarting' })) });
  assert.equal(many.length, 1);
  assert.equal(many[0].link, '/cosmos-ui/servapps');
  assert.equal(many[0].text[2].names, 'a, b, c …');
  // a full disk is an error
  assert.equal(healthLines({ disks: [{ Key: 'cosmos.system.disk./', Max: 10, Values: [{ Value: 10 }] }] })[0].severity, 'error');
});

test('nothing wrong: no line, and the calm sentence counts the running apps', () => {
  const sources = { jobs: {}, containers: [{ State: 'running' }, { State: 'running' }, { State: 'exited', Status: 'Exited (0) x' }], disks: [], snapshots: { enabled: false } };
  assert.deepEqual(healthLines(sources), []);
  assert.equal(calmLine(sources)[2].running, 2);
  assert.deepEqual(healthLines(undefined), []);
});

test('apps that exited with 137 are asked about, at most five; those killed for memory make an error line', () => {
  const list = ['a', 'b', 'c', 'd', 'e', 'f'].map((n) => ({ Names: ['/' + n], State: 'exited', Status: 'Exited (137) 1 hour ago' }));
  list.push({ Names: ['/g'], State: 'exited', Status: 'Exited (0) 1 hour ago' }, { Names: ['/h'], State: 'running', Status: 'Up' });
  assert.deepEqual(oomCandidates(list), ['a', 'b', 'c', 'd', 'e']);
  assert.deepEqual(oomCandidates(null), []);
  assert.deepEqual(oomKilled({ a: { State: { OOMKilled: true } }, b: { State: { OOMKilled: false } }, c: undefined }), ['a']);
  const one = healthLines({ oom: ['a'] });
  assert.equal(one[0].id, 'oom');
  assert.equal(one[0].severity, 'error');
  assert.equal(one[0].link, '/cosmos-ui/servapps/containers/a');
  assert.equal(healthLines({ oom: ['a', 'b'] })[0].link, '/cosmos-ui/servapps');
  assert.deepEqual(healthLines({ oom: [] }), []);
});

test('three states: all well only when every source was read', () => {
  assert.equal(healthState([], []), 'ok');
  assert.equal(healthState([], ['scheduled jobs']), 'unknown');
  assert.equal(healthState([{ id: 'jobs' }], ['disks']), 'attention');
  assert.equal(healthState([{ id: 'jobs' }], []), 'attention');
});

test('a refusal by the request limit is named as such', () => {
  assert.equal(isThrottled({ code: 429 }), true);
  assert.equal(isThrottled({ code: 'HTTP003', status: 'error' }), true);
  assert.equal(isThrottled({ message: 'Too many requests' }), true);
  assert.equal(isThrottled({ code: 'HTTP001', message: 'Internal server error' }), false);
  assert.equal(isThrottled(undefined), false);
  assert.match(unreadLine(['scheduled jobs', 'disks'], true)[1], /limiting requests/);
  assert.equal(unreadLine(['scheduled jobs', 'disks'], true)[2].what, 'scheduled jobs, disks');
  assert.match(unreadLine(['apps'], false)[1], /did not answer/);
});

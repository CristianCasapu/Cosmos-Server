// Fork addition: tests of sharedRequest.js. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { createSharedRequest } from './sharedRequest.js';

// a `load` whose requests are answered by hand
const manual = () => {
  const calls = [];
  const load = () => new Promise((resolve, reject) => calls.push({ resolve, reject }));
  return { calls, load };
};
const tick = () => new Promise((resolve) => setImmediate(resolve));

test('callers at the same moment share one request', async () => {
  const { calls, load } = manual();
  const shared = createSharedRequest(load);
  const asked = Array.from({ length: 46 }, () => shared.get());
  await tick();
  assert.equal(calls.length, 1);
  calls[0].resolve({ status: 'OK' });
  const answers = await Promise.all(asked);
  assert.equal(answers.length, 46);
  answers.forEach((answer) => assert.deepEqual(answer, { status: 'OK' }));
});

test('the answer is kept for the time given, then asked again', async () => {
  let clock = 1000;
  let sent = 0;
  const shared = createSharedRequest(async () => ({ n: ++sent }), { ttl: 5000, now: () => clock });
  assert.deepEqual(await shared.get(), { n: 1 });
  clock += 4999;
  assert.deepEqual(await shared.get(), { n: 1 });
  assert.equal(sent, 1);
  clock += 1;
  assert.deepEqual(await shared.get(), { n: 2 });
  assert.equal(sent, 2);
});

test('an answer that may not be kept is given to its callers but not to later ones', async () => {
  let sent = 0;
  const answers = [{ status: 'error', code: 'HTTP004' }, { status: 'OK' }];
  const shared = createSharedRequest(async () => answers[sent++], { keep: (answer) => answer.status === 'OK' });
  assert.equal((await shared.get()).code, 'HTTP004'); // not logged in: must not stick
  assert.equal((await shared.get()).status, 'OK');
  assert.equal((await shared.get()).status, 'OK');
  assert.equal(sent, 2);
});

test('a failed request is not kept and its callers all see the failure', async () => {
  const { calls, load } = manual();
  const shared = createSharedRequest(load);
  const first = shared.get();
  const second = shared.get();
  await tick();
  calls[0].reject(new Error('offline'));
  await assert.rejects(first, /offline/);
  await assert.rejects(second, /offline/);
  const third = shared.get();
  await tick();
  assert.equal(calls.length, 2);
  calls[1].resolve('fine');
  assert.equal(await third, 'fine');
});

test('invalidate forgets the kept answer', async () => {
  let sent = 0;
  const shared = createSharedRequest(async () => ++sent);
  assert.equal(await shared.get(), 1);
  assert.equal(await shared.get(), 1);
  shared.invalidate();
  assert.equal(await shared.get(), 2);
});

test('an answer that arrives after invalidate is not kept', async () => {
  const { calls, load } = manual();
  const shared = createSharedRequest(load);
  const before = shared.get(); // asked while user A was signed in
  await tick();
  shared.invalidate(); // login as user B
  const after = shared.get();
  await tick();
  assert.equal(calls.length, 2, 'a new request is sent, the old one is not reused');
  calls[1].resolve('B');
  calls[0].resolve('A');
  assert.equal(await before, 'A');
  assert.equal(await after, 'B');
  assert.equal(await shared.get(), 'B', 'what is kept is the answer asked after the change');
});

test('a keep that throws keeps nothing and breaks nothing', async () => {
  let sent = 0;
  const shared = createSharedRequest(async () => ++sent, { keep: () => { throw new Error('bad'); } });
  assert.equal(await shared.get(), 1);
  assert.equal(await shared.get(), 2);
});

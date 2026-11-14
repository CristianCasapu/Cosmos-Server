// Fork addition: tests of sessionCheck.js. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  shareSessionCheck, forgetSessionCheck, noteAnswer, watchAnswers,
  throttledRecently, throttleSecondsLeft, resetSessionCheck, THROTTLE_MEMORY,
} from './sessionCheck.js';

// an auth API that counts its calls, like api/authentication.tsx's
const fakeAuth = (answer = { status: 'OK', data: { nickname: 'admin' } }) => {
  const sent = { me: 0, login: 0, logout: 0, sudo: 0 };
  return {
    sent,
    answer,
    api: {
      me: async () => { sent.me += 1; return fake.answer; },
      login: async (values) => { sent.login += 1; return { status: 'OK', values }; },
      logout: async () => { sent.logout += 1; return { status: 'OK' }; },
      sudo: async () => { sent.sudo += 1; return { status: 'OK' }; },
    },
  };
};
let fake;
const fresh = (answer) => { resetSessionCheck(); fake = fakeAuth(answer); return shareSessionCheck(fake.api); };

test('46 checks while a page loads send one request', async () => {
  const auth = fresh();
  const answers = await Promise.all(Array.from({ length: 46 }, () => auth.me()));
  assert.equal(fake.sent.me, 1);
  assert.equal(answers[45].data.nickname, 'admin');
  await auth.me(); // a component that asks a moment later
  assert.equal(fake.sent.me, 1);
});

test('"not signed in" is never kept', async () => {
  const auth = fresh({ status: 'error', code: 'HTTP004' });
  assert.equal((await auth.me()).code, 'HTTP004');
  fake.answer = { status: 'OK' };
  assert.equal((await auth.me()).status, 'OK');
  assert.equal(fake.sent.me, 2);
});

test('a throttled answer is never kept', async () => {
  const auth = fresh({ status: 'error', code: 'HTTP003', message: 'Too many requests' });
  await auth.me();
  await auth.me();
  assert.equal(fake.sent.me, 2);
});

for (const change of ['login', 'logout', 'sudo']) {
  test(`${change} drops the kept answer and passes its own result on`, async () => {
    const auth = fresh();
    await auth.me();
    const result = await auth[change]({ nickname: 'other' });
    assert.equal(result.status, 'OK');
    assert.equal(fake.sent[change], 1);
    await auth.me();
    assert.equal(fake.sent.me, 2);
  });
}

test('a check sent before a sign-in does not answer for the session after it', async () => {
  resetSessionCheck();
  const waiting = [];
  let session = 'A';
  const auth = shareSessionCheck({
    me: () => new Promise((resolve) => waiting.push(() => resolve({ status: 'OK', who: session }))),
    login: async () => { session = 'B'; return { status: 'OK' }; },
  });
  const before = auth.me();
  await new Promise((resolve) => setImmediate(resolve));
  await auth.login({});
  const after = auth.me();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(waiting.length, 2);
  waiting[1]();
  waiting[0]();
  assert.equal((await after).who, 'B');
  await before;
  assert.equal((await auth.me()).who, 'B');
});

test('an answer 401 or 403 to any call drops the kept answer; other answers do not', async () => {
  const auth = fresh();
  await auth.me();
  noteAnswer(200);
  noteAnswer(404);
  noteAnswer(500);
  await auth.me();
  assert.equal(fake.sent.me, 1);
  noteAnswer(401);
  await auth.me();
  assert.equal(fake.sent.me, 2);
  noteAnswer(403);
  await auth.me();
  assert.equal(fake.sent.me, 3);
});

test('forgetSessionCheck asks again', async () => {
  const auth = fresh();
  await auth.me();
  forgetSessionCheck();
  await auth.me();
  assert.equal(fake.sent.me, 2);
});

test('a 429 is remembered for a minute', () => {
  resetSessionCheck();
  assert.equal(throttledRecently(1000), false);
  assert.equal(throttleSecondsLeft(1000), 0);
  noteAnswer(429, 100000);
  assert.equal(throttledRecently(100000), true);
  assert.equal(throttleSecondsLeft(100000), 60);
  assert.equal(throttleSecondsLeft(100000 + 45500), 15);
  assert.equal(throttledRecently(100000 + THROTTLE_MEMORY - 1), true);
  assert.equal(throttledRecently(100000 + THROTTLE_MEMORY), false);
  assert.equal(throttleSecondsLeft(100000 + THROTTLE_MEMORY), 0);
});

test('watching a request returns the same promise and notes its status', async () => {
  const auth = fresh();
  await auth.me();
  const request = Promise.resolve({ status: 403 });
  assert.equal(watchAnswers(request), request);
  await request;
  await new Promise((resolve) => setImmediate(resolve));
  await auth.me();
  assert.equal(fake.sent.me, 2);
  // a request that fails is left alone: its own caller handles the failure
  const failing = Promise.reject(new Error('offline'));
  assert.equal(watchAnswers(failing), failing);
  await assert.rejects(failing);
});

// Fork addition: tests of sudoPrompt.js (node --test "client/src/utils/*.test.mjs").
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  askForPassword, closeSudoRequest, confirmationDone, confirmedAt, getSudoState, isSudoRefusal, noteInput, noteSession,
  reloadAfterConfirmation, resetSudoPromptForTests, subscribeSudo, sudoUntilOf, watchForRefusal,
} from './sudoPrompt.js';

// a clock and timers the test moves
const fakeTimers = (startMs) => {
  const clock = { now: startMs, pending: [], nextId: 1 };
  return {
    clock,
    timers: {
      now: () => clock.now,
      set: (fn, ms) => { const id = clock.nextId++; clock.pending.push({ id, at: clock.now + ms, fn }); return id; },
      clear: (id) => { clock.pending = clock.pending.filter((timer) => timer.id !== id); },
    },
    advance: (ms) => {
      clock.now += ms;
      const due = clock.pending.filter((timer) => timer.at <= clock.now);
      clock.pending = clock.pending.filter((timer) => timer.at > clock.now);
      due.forEach((timer) => timer.fn());
    },
  };
};

const cookie = (sudoUntil) => `admin,1-2-100,${sudoUntil}`;
const answer = (status, body) => ({ status, clone: () => ({ json: async () => body }) });
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

test('the end of the confirmation is read from the cookie', () => {
  assert.equal(sudoUntilOf(cookie(1800000000)), 1800000000);
  assert.equal(sudoUntilOf(cookie(-1)), 0);
  assert.equal(sudoUntilOf('admin,1-2'), 0);
  assert.equal(sudoUntilOf('admin,1-2,abc'), 0);
  assert.equal(sudoUntilOf(undefined), 0);
  assert.equal(sudoUntilOf(''), 0);
  assert.equal(confirmedAt(1800000000, 1799999999000), true);
  assert.equal(confirmedAt(1800000000, 1800000000000), false);
  assert.equal(confirmedAt(0, 5), false);
});

test('everything is drawn again once, at the moment the confirmation ends', () => {
  const fake = fakeTimers(1_000_000_000_000);
  resetSudoPromptForTests(fake.timers);
  let draws = 0;
  subscribeSudo(() => { draws++; });
  const until = 1_000_000_000 + 7200; // two hours from now

  // every guarded button on the page reports the same cookie
  for (let i = 0; i < 50; i++) noteSession(cookie(until));
  assert.equal(fake.clock.pending.length, 1, 'one timer, however many readers');

  fake.advance(7199_000);
  assert.equal(draws, 0, 'nothing happens while the confirmation lasts');
  fake.advance(2_000);
  assert.equal(draws, 1, 'one redraw when it ends');
  assert.equal(getSudoState().tick, 1);
  assert.equal(fake.clock.pending.length, 0);

  // the cookie still names the past end: no new timer, no loop
  noteSession(cookie(until));
  assert.equal(fake.clock.pending.length, 0);

  // confirmed again: a new end, a new timer
  noteSession(cookie(until + 9000));
  assert.equal(fake.clock.pending.length, 1);
  // a cookie without a confirmation changes nothing
  noteSession(cookie(-1));
  assert.equal(fake.clock.pending.length, 1);
});

test('a refused command opens the prompt, a refused read does not', async () => {
  resetSudoPromptForTests(fakeTimers(0).timers);
  const refused = answer(401, { status: 'error', message: 'Sudo required for: Configuration', code: 'HTTP008' });

  watchForRefusal({ method: 'GET' }, Promise.resolve(refused));
  watchForRefusal(undefined, Promise.resolve(refused));
  await settle();
  assert.equal(getSudoState().request, null, 'a read never opens the prompt');

  watchForRefusal({ method: 'POST' }, Promise.resolve(answer(200, { status: 'OK' })));
  watchForRefusal({ method: 'POST' }, Promise.resolve(answer(401, { code: 'HTTP004' })));
  watchForRefusal({ method: 'POST' }, Promise.resolve(answer(401, { code: 'HTTP005', message: 'Missing permission' })));
  watchForRefusal({ method: 'POST' }, Promise.resolve(answer(403, { code: 'HTTP008' })));
  watchForRefusal({ method: 'POST' }, Promise.reject(new Error('offline')));
  watchForRefusal({ method: 'POST' }, Promise.resolve({ status: 401, clone: () => ({ json: async () => { throw new Error('not JSON'); } }) }));
  watchForRefusal({ method: 'POST' }, null);
  await settle();
  assert.equal(getSudoState().request, null, 'only "password not confirmed" opens the prompt');

  watchForRefusal({ method: 'post' }, Promise.resolve(refused));
  await settle();
  assert.equal(getSudoState().request.reason, 'refused');
  const first = getSudoState().request.id;

  // several commands refused at once: one prompt
  watchForRefusal({ method: 'DELETE' }, Promise.resolve(refused));
  await settle();
  assert.equal(getSudoState().request.id, first);

  closeSudoRequest();
  assert.equal(getSudoState().request, null);
  assert.equal(isSudoRefusal(401, { code: 'HTTP008' }), true);
  assert.equal(isSudoRefusal(401, null), false);
});

test('the page is loaded again after the password only when nothing can be lost', () => {
  // a page loaded without the confirmation, nothing typed, the chip clicked: as upstream
  resetSudoPromptForTests(fakeTimers(1_000_000_000_000).timers);
  assert.equal(reloadAfterConfirmation(null), true);

  // the server refused a command: the admin was in the middle of something
  askForPassword('refused');
  assert.equal(reloadAfterConfirmation(getSudoState().request), false);

  // a guarded button was clicked
  resetSudoPromptForTests();
  askForPassword('guard');
  assert.equal(reloadAfterConfirmation(getSudoState().request), false);

  // the confirmation was there on this page and has ended since
  resetSudoPromptForTests();
  noteSession(cookie(1_000_000_000 + 60));
  assert.equal(reloadAfterConfirmation(null), false);

  // something was typed
  resetSudoPromptForTests();
  noteInput();
  assert.equal(reloadAfterConfirmation(null), false);
});

test('after the password, without a reload: the prompt closes and the page redraws', () => {
  resetSudoPromptForTests(fakeTimers(0).timers);
  let draws = 0;
  subscribeSudo(() => { draws++; });
  askForPassword('refused');
  assert.equal(draws, 1);
  confirmationDone();
  assert.equal(getSudoState().request, null);
  assert.equal(getSudoState().tick, 1);
  assert.equal(draws, 2);
});

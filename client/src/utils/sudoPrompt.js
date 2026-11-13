// Fork addition: what the pages know about the password confirmation
// ("sudo") and when to ask for it again.
//
// The confirmation lasts two hours from the moment the password was given
// and nothing renews it (src/user/fork_session_life.go). So it can end in the
// middle of work. This module makes that moment harmless:
//
//   - at the moment it ends, everything that depends on it is drawn again
//     (the "Admin" chip appears, guarded buttons grey out) instead of each
//     part noticing at its next redraw;
//   - a command the server refuses because the confirmation is missing opens
//     the password prompt by itself, and so does a click on a guarded button;
//   - after the password the page is NOT loaded again when that could lose
//     something: when the prompt was asked for in the middle of work, when
//     the confirmation had been there earlier on this page, or when anything
//     was typed since the page loaded. Then the page only redraws, and what
//     was entered is still there.
//
// No React in here: components/…/sudoModal.jsx and utils/hooks.js use it
// through utils/sudoPromptHooks.js, and sudoPrompt.test.mjs tests it.

const SUDO_REFUSED = 'HTTP008';

let timers = {
  set: (fn, ms) => setTimeout(fn, ms),
  clear: (id) => clearTimeout(id),
  now: () => Date.now(),
};

let state = { request: null, tick: 0 };
let hadConfirmation = false;
let sawInput = false;
let scheduledFor = 0;
let timerId = null;
let nextRequest = 1;
const listeners = new Set();

const emit = (change) => {
  state = { ...state, ...change };
  listeners.forEach((listener) => listener());
};

export const subscribeSudo = (listener) => {
  listeners.add(listener);
  return () => listeners.delete(listener);
};

export const getSudoState = () => state;

// sudoUntilOf reads the end of the confirmation (seconds) from the
// client-infos cookie "nickname,permissions,sudoUntil"; 0 when there is none.
export const sudoUntilOf = (cookie) => {
  if (typeof cookie !== 'string') return 0;
  const parts = cookie.split(',');
  if (parts.length < 3) return 0;
  const until = parseInt(parts[2], 10);
  return Number.isFinite(until) && until > 0 ? until : 0;
};

export const confirmedAt = (sudoUntil, nowMs) => sudoUntil > 0 && sudoUntil * 1000 > nowMs;

// noteSession is told the cookie's value whenever a part of the page reads
// it. It remembers that a confirmation was there, and sets one timer for the
// moment it ends.
export const noteSession = (cookie) => {
  const until = sudoUntilOf(cookie);
  const now = timers.now();
  if (!confirmedAt(until, now)) return;
  hadConfirmation = true;
  if (scheduledFor === until) return;
  if (timerId !== null) timers.clear(timerId);
  scheduledFor = until;
  // a moment after the end, so that every reader then sees it as ended
  timerId = timers.set(() => {
    timerId = null;
    scheduledFor = 0;
    emit({ tick: state.tick + 1 });
  }, Math.max(0, until * 1000 - now) + 250);
};

// askForPassword opens the prompt. reason: 'refused' (the server refused a
// command) or 'guard' (a guarded control was clicked).
export const askForPassword = (reason) => {
  if (state.request) return;
  emit({ request: { id: nextRequest++, reason, hadConfirmation } });
};

export const closeSudoRequest = () => {
  if (state.request) emit({ request: null });
};

// noteInput: something was typed or chosen on the page.
export const noteInput = () => { sawInput = true; };

// reloadAfterConfirmation: whether the page may be loaded again once the
// password was confirmed (upstream always did, to fetch what a page without
// the confirmation had left out). Not when anything could be lost.
export const reloadAfterConfirmation = (request) => !(request || hadConfirmation || sawInput);

// confirmationDone is called once the server accepted the password and the
// page stays: the prompt closes and everything redraws with the new cookie.
export const confirmationDone = () => {
  emit({ request: null, tick: state.tick + 1 });
};

const isRead = (method) => method === 'GET' || method === 'HEAD' || method === 'OPTIONS';

// isSudoRefusal: the answer of a command that was refused only because the
// password is not confirmed (any more).
export const isSudoRefusal = (status, body) => status === 401 && !!body && body.code === SUDO_REFUSED;

// watchForRefusal is given every request the API client sends (by
// utils/activity.js). It never changes or delays the request. Reads are left
// alone: a page that asks for something it may not see must not open a
// password prompt on its own.
export const watchForRefusal = (options, promise) => {
  try {
    const method = String((options && options.method) || 'GET').toUpperCase();
    if (isRead(method) || !promise || typeof promise.then !== 'function') return;
    promise.then((response) => {
      if (!response || response.status !== 401 || typeof response.clone !== 'function') return;
      response.clone().json().then((body) => {
        if (isSudoRefusal(response.status, body)) askForPassword('refused');
      }).catch(() => {});
    }).catch(() => {});
  } catch (e) {
    // watching a request must never break it
  }
};

// For tests: a clock and timers of their own, and a clean slate.
export const resetSudoPromptForTests = (replacement) => {
  if (timerId !== null) timers.clear(timerId);
  timers = replacement || timers;
  state = { request: null, tick: 0 };
  hadConfirmation = false;
  sawInput = false;
  scheduledFor = 0;
  timerId = null;
  listeners.clear();
};

// Fork addition: "who am I?" (GET /api/me) asked once per page load instead of
// once per entry of the route table (46 times), and what the panel remembers
// about the server holding it back (429). The API limits an address to 500
// requests a minute; with 46 of them spent on each page, ten pages in a
// minute were enough to be throttled.
//
// The answer is kept for five seconds, in this tab's memory only, and only
// when it says "signed in": an error or "not signed in" is never kept. It is
// dropped when the session changes (sign-in, sign-out, password
// confirmation) and whenever any call is answered 401 or 403. The server
// still checks every other request on its own: this only saves questions.
import { createSharedRequest } from './sharedRequest.js';

export const SESSION_CHECK_TTL = 5000;
// how long after a 429 the panel still says it is being held back
export const THROTTLE_MEMORY = 60000;

const checks = new Set();
let throttledAt = 0;

// forgetSessionCheck drops the kept answer: the next check asks the server.
export const forgetSessionCheck = () => checks.forEach((check) => check.invalidate());

const forgetAround = (call) => (...args) => {
  forgetSessionCheck();
  let result;
  try {
    result = call(...args);
  } catch (e) {
    forgetSessionCheck();
    throw e;
  }
  if (result && typeof result.then === 'function') {
    // forget again once it is over, whatever the outcome; the caller gets the call's own promise
    result.then(forgetSessionCheck, forgetSessionCheck);
  }
  return result;
};

// shareSessionCheck returns the auth API with `me` shared and with the calls
// that change the session dropping the kept answer.
export const shareSessionCheck = (auth, options = {}) => {
  const check = createSharedRequest(() => auth.me(), {
    ttl: options.ttl === undefined ? SESSION_CHECK_TTL : options.ttl,
    now: options.now,
    keep: (answer) => !!answer && answer.status === 'OK',
  });
  checks.add(check);
  const shared = { ...auth, me: () => check.get() };
  ['login', 'logout', 'sudo'].forEach((name) => {
    if (typeof auth[name] === 'function') {
      shared[name] = forgetAround(auth[name]);
    }
  });
  return shared;
};

// noteAnswer is told the status of every answer the API client receives.
export const noteAnswer = (status, now = Date.now()) => {
  if (status === 401 || status === 403) {
    forgetSessionCheck();
  } else if (status === 429) {
    throttledAt = now;
  }
};

// watchAnswers looks at a request's answer without touching the request: it
// returns the promise it was given.
export const watchAnswers = (promise) => {
  try {
    promise.then((response) => noteAnswer(response && response.status), () => {});
  } catch (e) {
    // watching a request must never break it
  }
  return promise;
};

// True when the server refused a request for "too many requests" a moment ago.
export const throttledRecently = (now = Date.now()) => throttledAt > 0 && now - throttledAt < THROTTLE_MEMORY;

// Seconds until the server's one-minute window has passed since the last refusal.
export const throttleSecondsLeft = (now = Date.now()) => (throttledRecently(now)
  ? Math.max(1, Math.ceil((THROTTLE_MEMORY - (now - throttledAt)) / 1000))
  : 0);

// for tests
export const resetSessionCheck = () => {
  checks.clear();
  throttledAt = 0;
};

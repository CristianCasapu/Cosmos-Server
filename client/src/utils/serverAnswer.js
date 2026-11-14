// Fork addition: what to say when the server's answer is not the server's.
//
// Behind a proxy (Cloudflare, or any reverse proxy in front of Cosmos) a
// request made while Cosmos restarts does not fail on the network: the proxy
// answers itself, with a status such as 521 and a whole HTML page. The panel
// took any answer that was not JSON as the error's message and showed it, so
// saving a setting (which restarts Cosmos' listener) covered the screen with
// the source of Cloudflare's error page. Such an answer is now told apart:
//   - a gateway status means "the server did not answer": said in one calm
//     sentence, and treated like a network failure by the code that waits for
//     the server to come back;
//   - any other answer that is a web page is named as one, never shown;
//   - plain text is cut to one bounded line.

// Statuses a proxy answers with when the server behind it does not: the
// gateway errors, and Cloudflare's own 520 to 527 and 530.
const NO_ANSWER = [502, 503, 504, 520, 521, 522, 523, 524, 525, 526, 527, 530];

export const serverDidNotAnswer = (status) => NO_ANSWER.includes(Number(status));

// Markers, translated where they are shown (activity.js, explain).
export const NO_ANSWER_MESSAGE = 'The server did not answer';
export const ERROR_PAGE_MESSAGE = 'The server answered with an error page';

const MAX_MESSAGE = 300;

export const looksLikeWebPage = (text) => {
  const start = String(text || '').slice(0, 2000);
  return /^\s*(<!doctype\b|<html\b|<\?xml\b|<head\b|<body\b)/i.test(start) || /<(html|head|body|title|script|style)\b[^>]*>/i.test(start);
};

// plainFailure turns an answer that is not JSON into a message fit to show.
export const plainFailure = (status, text) => {
  if (serverDidNotAnswer(status)) {
    return NO_ANSWER_MESSAGE;
  }
  const body = String(text || '');
  if (looksLikeWebPage(body)) {
    return ERROR_PAGE_MESSAGE + ' (HTTP ' + (Number(status) || '?') + ')';
  }
  const line = body.replace(/\x1b\[[0-9;]*m/g, '').split(/[\r\n]+/).map((part) => part.trim()).filter(Boolean)[0] || '';
  return line.length > MAX_MESSAGE ? line.slice(0, MAX_MESSAGE) + '…' : line;
};

// unansweredError says whether a failed call should be tried again by code
// that waits for the server to come back: nothing answered at all, or a
// proxy answered in the server's place.
export const unansweredError = (error) => !(error && error.status) || Boolean(error && error.unanswered);

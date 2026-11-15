// Fork addition (plan 40, security review F3): an edit of a container that
// gives it access to the server it did not have (a device, a system folder,
// the host's network …) is refused by the server until each item is ticked
// (POST /api/servapps/{id}/update answers 409, code DS010, with the items).
// This module asks the administrator, through the dialog mounted once
// (components/sensitiveEditDialog.jsx), and sends the edit again with the
// ticks. No React in here; tested in sensitiveEdit.test.mjs.

const listeners = new Set();
let pending = null; // { items, resolve }

export const subscribeSensitiveEdit = (listener) => {
  listeners.add(listener);
  return () => listeners.delete(listener);
};

export const getSensitiveEdit = () => pending;

const emit = () => listeners.forEach((listener) => listener());

// Asks for the items; answers the ids ticked, or null when cancelled.
// Without a dialog mounted (no listener) nothing can be asked: null.
export const askSensitive = (items) => new Promise((resolve) => {
  if (listeners.size === 0) {
    resolve(null);
    return;
  }
  if (pending) pending.resolve(null);
  pending = {
    items,
    resolve: (answer) => {
      pending = null;
      emit();
      resolve(answer);
    },
  };
  emit();
});

export const answerSensitive = (ids) => { if (pending) pending.resolve(ids); };

const idOf = (item) => `${item.service}|${item.kind}|${item.value}`;

// send(confirmIds) makes the request and answers its Response. On a refusal
// for sensitive access the administrator is asked; ticked, the request is
// sent again with the ticks, else the refusal is answered as it was.
export const sendWithConfirmation = async (send, ask = askSensitive) => {
  const first = await send([]);
  if (first.status !== 409) return first;
  let body = null;
  try { body = await first.clone().json(); } catch (e) { return first; }
  const items = body && body.code === 'DS010' && body.data && Array.isArray(body.data.sensitive) ? body.data.sensitive : null;
  if (!items || !items.length) return first;
  const ids = await ask(items);
  const all = items.map(idOf);
  if (!ids || !all.every((id) => ids.includes(id))) return first;
  return send(all);
};

export const confirmQuery = (ids) => (ids && ids.length ? '?' + ids.map((id) => 'confirm=' + encodeURIComponent(id)).join('&') : '');

// A refusal the dialog turns into a question (409, DS010): not a failure to
// announce (utils/activity.js); the edit sent again after the ticks is.
export const isSensitiveRefusal = (status, text) => {
  if (status !== 409) return false;
  try { return JSON.parse(text).code === 'DS010'; } catch (e) { return false; }
};

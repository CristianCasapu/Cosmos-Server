// Fork addition: the words of the banner about containers left behind by an
// interrupted change (components/leftovers.jsx, route
// /cosmos/api/servapps-leftovers, src/docker/fork_leftovers.go). Pure: each
// text is [key, English, values] for t(); tested by leftovers.test.mjs.

const MARKER = '-cosmos-old-';
const PATTERN = /^([a-zA-Z0-9][a-zA-Z0-9_.-]*)-cosmos-old-([0-9a-f]{12})$/;

// appOf gives the app's name of a leftover's name, '' for any other name.
export const appOf = (name) => {
  const m = PATTERN.exec(String(name || ''));
  return m ? m[1] : '';
};

// The most recent first (when it stopped, roughly when the change was made), then by name.
export const sortLeftovers = (list) => [...(Array.isArray(list) ? list : [])]
  .filter((entry) => entry && typeof entry.name === 'string' && entry.name.includes(MARKER))
  .sort((a, b) => {
    const fa = a.finished || '';
    const fb = b.finished || '';
    if (fa !== fb) return fa < fb ? 1 : -1;
    return a.name < b.name ? -1 : a.name > b.name ? 1 : 0;
  });

// What happened, in one sentence. `date` is the stop time already written for the reader, '' when unknown.
export const whatHappened = (entry, date) => {
  const values = { app: entry.app, name: entry.name, date };
  if (entry.running) {
    return date
      ? ['mgmt.servapps.leftovers.whatRunningDated', 'The change of {{app}} made on {{date}} was interrupted: the previous container is kept as {{name}}, and it is running.', values]
      : ['mgmt.servapps.leftovers.whatRunning', 'A change of {{app}} was interrupted: the previous container is kept as {{name}}, and it is running.', values];
  }
  return date
    ? ['mgmt.servapps.leftovers.whatDated', 'The change of {{app}} made on {{date}} was interrupted: the previous container is kept, stopped, as {{name}}.', values]
    : ['mgmt.servapps.leftovers.what', 'A change of {{app}} was interrupted: the previous container is kept, stopped, as {{name}}.', values];
};

// Whether the app runs now under its name.
export const appNow = (entry) => {
  const values = { app: entry.app };
  if (entry.appExists && entry.appRunning) {
    return ['mgmt.servapps.leftovers.nowRunning', '{{app}} runs now under its name.', values];
  }
  if (entry.appExists) {
    return ['mgmt.servapps.leftovers.nowStopped', 'A container named {{app}} exists now, but it is stopped.', values];
  }
  return ['mgmt.servapps.leftovers.nowNone', 'No container is named {{app}} now: the app is not running.', values];
};

// What "Put the previous one back" does.
export const restoreHint = (entry) => (entry.appExists
  ? ['mgmt.servapps.leftovers.restoreHint', 'Put the previous one back: it gets the name {{app}} again and is started; the current {{app}} is stopped and kept aside, not deleted.', { app: entry.app }]
  : ['mgmt.servapps.leftovers.restoreHintAlone', 'Put the previous one back: it gets the name {{app}} again and is started.', { app: entry.app }]);

// The question before removing.
export const removeQuestion = (entry) => ['mgmt.servapps.leftovers.removeConfirm',
  'Remove {{name}}? The container is deleted for good; its volumes are kept. {{app}} itself is not touched.', { app: entry.app, name: entry.name }];

// Why the remove button cannot be used.
export const removeBlocked = (entry) => (entry.running
  ? ['mgmt.servapps.leftovers.removeRunning', 'It is running: stop it first (its own page, under Stop).', {}]
  : null);

// What the server answered, in words.
export const answerText = (answer) => {
  const data = (answer && answer.data) || {};
  if (data.result === 'restored') {
    return data.setAside
      ? ['mgmt.servapps.leftovers.restoredAside', '{{app}} is back. The container that had its name is kept, stopped, as {{aside}}.', { app: data.app, aside: data.setAside }]
      : ['mgmt.servapps.leftovers.restored', '{{app}} is back and started.', { app: data.app }];
  }
  if (data.result === 'removed') {
    return ['mgmt.servapps.leftovers.removed', '{{name}} was removed.', { name: data.name }];
  }
  return ['mgmt.servapps.leftovers.done', 'Done.', {}];
};

// A failure, by the code of the answer; `detail` is the server's own sentence.
export const failureText = (code, detail) => {
  const values = { detail: detail || '' };
  switch (code) {
    case 'LEFT002':
      return ['mgmt.servapps.leftovers.fail.gone', 'This container is no longer a leftover (it was changed or removed meanwhile). The list is refreshed.', values];
    case 'LEFT003':
      return ['mgmt.servapps.leftovers.fail.running', 'The leftover is running: stop it before removing it.', values];
    case 'LEFT004':
      return ['mgmt.servapps.leftovers.fail.undone', 'The previous container was not put back; everything is as it was. {{detail}}', values];
    case 'LEFT005':
      return ['mgmt.servapps.leftovers.fail.notUndone', 'The previous container was not put back, and not everything could be undone: check the containers below. {{detail}}', values];
    case 'LEFT006':
      return ['mgmt.servapps.leftovers.fail.remove', 'The leftover could not be removed. {{detail}}', values];
    case 'LEFT007':
      return ['mgmt.servapps.leftovers.fail.docker', 'Docker does not answer.', values];
    default:
      return ['mgmt.servapps.leftovers.fail.other', 'That did not work. {{detail}}', values];
  }
};

// The label of the command in the activity toasts (utils/activity.js), from the request's body.
export const commandLabel = (body) => {
  let request = null;
  try {
    request = typeof body === 'string' ? JSON.parse(body) : null;
  } catch (e) {
    request = null;
  }
  const name = request && typeof request.name === 'string' ? request.name : '';
  const app = appOf(name) || name;
  if (request && request.action === 'restore') {
    return ['activity.leftovers.restore', 'Put back the previous {{name}}', { name: app }];
  }
  if (request && request.action === 'remove') {
    return ['activity.leftovers.remove', 'Remove the leftover {{name}}', { name }];
  }
  return ['activity.leftovers.other', 'Change a leftover container', {}];
};

// Every text of this module, for the translation test.
export const allTexts = () => {
  const entries = [
    { app: 'a', name: 'a-cosmos-old-000000000000', running: false, appExists: false },
    { app: 'a', name: 'a-cosmos-old-000000000000', running: true, appExists: true, appRunning: true },
    { app: 'a', name: 'a-cosmos-old-000000000000', running: false, appExists: true, appRunning: false },
  ];
  const out = [];
  for (const entry of entries) {
    out.push(whatHappened(entry, ''), whatHappened(entry, 'today'), appNow(entry), restoreHint(entry), removeQuestion(entry));
    const blocked = removeBlocked(entry);
    if (blocked) out.push(blocked);
  }
  out.push(answerText({ data: { result: 'restored', app: 'a', setAside: 'x' } }), answerText({ data: { result: 'restored', app: 'a' } }),
    answerText({ data: { result: 'removed', name: 'x' } }), answerText({}));
  for (const code of ['LEFT002', 'LEFT003', 'LEFT004', 'LEFT005', 'LEFT006', 'LEFT007', 'other']) out.push(failureText(code, ''));
  out.push(commandLabel('{"action":"restore","name":"a-cosmos-old-000000000000"}'), commandLabel('{"action":"remove","name":"x"}'), commandLabel('nope'));
  return out;
};

// Fork addition: follows the commands the UI sends to the server, so that one
// place can tell the admin what is running and how it ended (see
// components/activityIndicator.jsx). The API client reports every request
// here; only requests that change something are followed, and the request
// itself is never altered or delayed.
import i18n from 'i18next';

// Translated when the translations are loaded, the English text otherwise.
const tr = (key, text, vars) => {
  let result;
  try {
    result = i18n.isInitialized ? i18n.t(key, { defaultValue: text, ...(vars || {}) }) : undefined;
  } catch (e) {
    result = undefined;
  }
  if (typeof result === 'string' && result !== '' && result !== key) {
    return result;
  }
  return text.replace(/\{\{(\w+)\}\}/g, (whole, name) => (vars && vars[name] !== undefined ? String(vars[name]) : whole));
};

const decode = (part) => {
  try {
    return decodeURIComponent(part).replace(/^\//, '');
  } catch (e) {
    return part;
  }
};

const CONTAINER_ACTIONS = {
  start: ['activity.container.start', 'Start {{name}}'],
  stop: ['activity.container.stop', 'Stop {{name}}'],
  restart: ['activity.container.restart', 'Restart {{name}}'],
  kill: ['activity.container.kill', 'Kill {{name}}'],
  remove: ['activity.container.remove', 'Delete {{name}}'],
  pause: ['activity.container.pause', 'Pause {{name}}'],
  unpause: ['activity.container.unpause', 'Resume {{name}}'],
  recreate: ['activity.container.recreate', 'Recreate {{name}}'],
  update: ['activity.container.update', 'Update {{name}}'],
};

const isRead = (method) => method === 'GET' || method === 'HEAD' || method === 'OPTIONS';

// For paths that are read with GET and changed with the other methods.
const byMethod = (labels) => (m, method) => (isRead(method) ? null : labels[method] || labels.POST);
const whenChanging = (label) => (m, method) => (isRead(method) ? null : label(m));

// Requests that are not commands, whatever their method: sign-in, reads sent
// as POST, and actions whose result the page shows by itself.
const IGNORED = [
  /^\/api\/(login|logout|sudo|mfa|register|password-reset|me|status|newInstall|setup-join)\b/,
  /^\/api\/(metrics|events|notifications|list-dir|dns-check|_logs|_memory)\b/,
  /^\/api\/jobs\/(get|running)\b/,
  /^\/api\/(config\/test-email|upnp|server-update|force-server-update)\b/,
  /^\/api\/telegram\b/, // the Telegram card shows what a test message gave, in words, where it was asked for
  /^\/api\/servapps\/[^/]+\/(logs|terminal|export|check-update)\b/,
  /^\/api\/constellation\/(ping|devices\/[^/]+\/ping)\b/,
  /^\/rclone\/(config\/(dump|listremotes)|operations\/about|vfs\/stats|core\/stats)\b/,
];

// [method (null: any), path, label]. The first match wins.
const COMMANDS = [
  [null, /^\/api\/servapps\/([^/]+)\/manage\/(\w+)$/, (m) => {
    const action = CONTAINER_ACTIONS[m[2]];
    return action ? [action[0], action[1], { name: decode(m[1]) }] : ['activity.container.other', 'Change {{name}}', { name: decode(m[1]) }];
  }],
  [null, /^\/api\/servapps\/([^/]+)\/auto-update\/(true|false)$/, (m) => m[2] === 'true'
    ? ['activity.container.autoUpdateOn', 'Turn on automatic updates for {{name}}', { name: decode(m[1]) }]
    : ['activity.container.autoUpdateOff', 'Turn off automatic updates for {{name}}', { name: decode(m[1]) }]],
  [null, /^\/api\/servapps\/([^/]+)\/secure\/(true|false)$/, (m) => ['activity.container.isolation', 'Change the network isolation of {{name}}', { name: decode(m[1]) }]],
  [null, /^\/api\/servapps\/([^/]+)\/lazy\//, (m) => ['activity.container.lazy', 'Change start-on-demand for {{name}}', { name: decode(m[1]) }]],
  ['POST', /^\/api\/servapps\/([^/]+)\/update$/, (m) => ['activity.container.settings', 'Save the settings of {{name}}', { name: decode(m[1]) }]],
  [null, /^\/api\/servapps\/([^/]+)\/network/, whenChanging((m) => ['activity.container.networks', 'Change the networks of {{name}}', { name: decode(m[1]) }])],
  ['POST', /^\/api\/docker-service$/, () => ['activity.install', 'Install the application']],
  ['POST', /^\/api\/servapps\/?$/, () => ['activity.container.create', 'Create the container']],
  ['DELETE', /^\/api\/servapps\//, () => ['activity.container.delete', 'Delete the application']],
  [null, /^\/api\/images\/pull/, whenChanging(() => ['activity.image.pull', 'Download the image'])],
  ['DELETE', /^\/api\/volume\/(.+)$/, (m) => ['activity.volume.delete', 'Delete volume {{name}}', { name: decode(m[1]) }]],
  ['POST', /^\/api\/volumes$/, () => ['activity.volume.create', 'Create the volume']],
  ['DELETE', /^\/api\/network\/(.+)$/, (m) => ['activity.network.delete', 'Delete network {{name}}', { name: decode(m[1]) }]],
  ['POST', /^\/api\/networks$/, () => ['activity.network.create', 'Create the network']],

  [null, /^\/api\/restart$/, () => ['activity.restartCosmos', 'Restart Cosmos']],
  [null, /^\/api\/restart-server$/, () => ['activity.restartServer', 'Restart the server']],
  [null, /^\/api\/config\/dns$/, whenChanging(() => ['activity.config.dns', 'Save the DNS settings'])],
  [null, /^\/api\/config$/, byMethod({ POST: ['activity.config.save', 'Save the settings'] })],
  ['POST', /^\/api\/routes$/, () => ['activity.route.create', 'Create the URL']],
  ['PUT', /^\/api\/routes\/(.+)$/, (m) => ['activity.route.update', 'Save URL {{name}}', { name: decode(m[1]) }]],
  ['DELETE', /^\/api\/routes\/(.+)$/, (m) => ['activity.route.delete', 'Delete URL {{name}}', { name: decode(m[1]) }]],
  ['PUT', /^\/api\/zones\/(.+)$/, (m) => ['activity.zone.save', 'Save domain {{name}}', { name: decode(m[1]) }]],
  ['DELETE', /^\/api\/zones\/(.+)$/, (m) => ['activity.zone.delete', 'Delete domain {{name}}', { name: decode(m[1]) }]],

  ['POST', /^\/api\/users$/, () => ['activity.user.create', 'Create the user']],
  ['POST', /^\/api\/invite$/, () => ['activity.user.invite', 'Create the invitation']],
  ['PATCH', /^\/api\/users\/(.+)$/, (m) => ['activity.user.update', 'Save user {{name}}', { name: decode(m[1]) }]],
  ['DELETE', /^\/api\/users\/(.+)$/, (m) => ['activity.user.delete', 'Delete user {{name}}', { name: decode(m[1]) }]],
  [null, /^\/api\/groups/, byMethod({ POST: ['activity.group.create', 'Create the group'], PUT: ['activity.group.update', 'Save the group'], DELETE: ['activity.group.delete', 'Delete the group'] })],
  [null, /^\/api\/api-tokens/, byMethod({ POST: ['activity.token.create', 'Create the API token'], PUT: ['activity.token.update', 'Save the API token'], DELETE: ['activity.token.delete', 'Delete the API token'] })],
  [null, /^\/api\/openid/, byMethod({ POST: ['activity.openid.create', 'Create the OpenID client'], PUT: ['activity.openid.update', 'Save the OpenID client'], DELETE: ['activity.openid.delete', 'Delete the OpenID client'] })],
  [null, /^\/api\/alerts/, byMethod({ POST: ['activity.alert.save', 'Save the alert'], PUT: ['activity.alert.save', 'Save the alert'], DELETE: ['activity.alert.delete', 'Delete the alert'] })],
  ['POST', /^\/api\/shield\/unban$/, () => ['activity.shield.unban', 'Lift the ban']],

  ['POST', /^\/api\/jobs\/run$/, () => ['activity.job.run', 'Start the job']],
  ['POST', /^\/api\/jobs\/stop$/, () => ['activity.job.stop', 'Stop the job']],
  ['POST', /^\/api\/jobs\/delete$/, () => ['activity.job.delete', 'Delete the job']],
  [null, /^\/api\/cron/, byMethod({ POST: ['activity.cron.create', 'Create the scheduled job'], PUT: ['activity.cron.update', 'Save the scheduled job'], DELETE: ['activity.cron.delete', 'Delete the scheduled job'] })],

  // internet reachability (src/reach): GET is a read and never followed
  ['POST', /^\/api\/reachability$/, () => ['activity.reachability.check', 'Check internet reachability']],

  // incremental snapshots (src/snapshots); their status is read with GET, which is never followed
  ['POST', /^\/api\/snapshots\/run$/, () => ['activity.snapshots.run', 'Start the incremental backup']],
  ['POST', /^\/api\/snapshots\/restore$/, () => ['activity.snapshots.restore', 'Restore the snapshot']],
  ['POST', /^\/api\/snapshots\/cleanup$/, () => ['activity.snapshots.cleanup', 'Delete the folder kept by a restore']],

  ['POST', /^\/api\/backups\/[^/]+\/restore$/, () => ['activity.backup.restore', 'Start the restore']],
  ['POST', /^\/api\/backups\/[^/]+\/unlock$/, () => ['activity.backup.unlock', 'Unlock the backup repository']],
  ['DELETE', /^\/api\/backups\/[^/]+\/[^/]+\/forget$/, () => ['activity.backup.forget', 'Delete the snapshot']],
  [null, /^\/api\/backups/, byMethod({ POST: ['activity.backup.save', 'Save the backup'], DELETE: ['activity.backup.delete', 'Delete the backup'] })],

  ['POST', /^\/api\/mount$/, () => ['activity.storage.mount', 'Mount the disk']],
  ['POST', /^\/api\/unmount$/, () => ['activity.storage.unmount', 'Unmount the disk']],
  ['POST', /^\/api\/merge$/, () => ['activity.storage.merge', 'Create the merged folder']],
  ['POST', /^\/api\/disks\/format$/, () => ['activity.storage.format', 'Format the disk']],
  ['POST', /^\/api\/new-dir$/, () => ['activity.storage.newDir', 'Create the folder']],
  ['POST', /^\/api\/upload\//, () => ['activity.upload', 'Upload the file']],
  [null, /^\/api\/snapraid\/[^/]+\/sync$/, () => ['activity.snapraid.sync', 'Start the parity sync']],
  [null, /^\/api\/snapraid\/[^/]+\/scrub$/, () => ['activity.snapraid.scrub', 'Start the parity check']],
  [null, /^\/api\/snapraid/, byMethod({ POST: ['activity.snapraid.save', 'Save the parity setup'], DELETE: ['activity.snapraid.delete', 'Delete the parity setup'] })],
  [null, /^\/api\/storage\/raid/, byMethod({ POST: ['activity.raid.change', 'Change the RAID array'], DELETE: ['activity.raid.delete', 'Delete the RAID array'] })],
  [null, /^\/api\/rclone-restart$/, () => ['activity.rclone.restart', 'Restart the remote storage service']],
  ['POST', /^\/rclone\/config\/delete$/, () => ['activity.rclone.delete', 'Delete the remote storage']],
  ['POST', /^\/rclone\/config\//, () => ['activity.rclone.save', 'Save the remote storage']],
];

// describe returns the plain-words name of a command, or null for a request
// that is not followed.
export const describe = (url, method) => {
  const path = String(url || '').replace(/^https?:\/\/[^/]+/, '').split('?')[0];
  const m = path.match(/^\/cosmos(\/(?:api|rclone)\/.*)$/);
  if (!m) {
    return null;
  }
  const rest = m[1];
  if (IGNORED.some((re) => re.test(rest))) {
    return null;
  }
  for (const [commandMethod, re, label] of COMMANDS) {
    if (commandMethod && commandMethod !== method) continue;
    const match = rest.match(re);
    if (match) {
      const found = label(match, method);
      return found ? tr(found[0], found[1], found[2]) : null;
    }
  }
  if (isRead(method)) {
    return null;
  }
  return tr('activity.generic', 'Apply the change');
};

// ---- state -----------------------------------------------------------------

const SUCCESS_FOR = 4000;
const FAILURE_FOR = 9000;
const MAX_TOASTS = 4;

let nextId = 1;
let state = { calls: [], toasts: [], total: 0, done: 0 };
const listeners = new Set();

const set = (change) => {
  state = { ...state, ...change };
  listeners.forEach((listener) => listener());
};

export const getActivity = () => state;

export const subscribeActivity = (listener) => {
  listeners.add(listener);
  return () => listeners.delete(listener);
};

export const dismissToast = (id) => set({ toasts: state.toasts.filter((toast) => toast.id !== id) });

// pruneActivity drops expired toasts and, once nothing is running, starts the
// "done / total" count afresh.
export const pruneActivity = (now) => {
  const toasts = state.toasts.filter((toast) => toast.until > now);
  const idle = state.calls.length === 0 && state.total > 0 && now - state.idleSince > 1500;
  if (toasts.length !== state.toasts.length || idle) {
    set({ toasts, ...(idle ? { total: 0, done: 0 } : {}) });
  }
};

const addToast = (severity, text, raw) => {
  const now = Date.now();
  const toast = { id: nextId++, severity, text, raw: raw || '', at: now, until: now + (severity === 'error' ? FAILURE_FOR : SUCCESS_FOR) };
  set({ toasts: [...state.toasts, toast].slice(-MAX_TOASTS) });
};

// notify shows a message that did not come from a followed command (the API
// layer's own error reports). The same error is not shown twice.
export const notify = (message, severity = 'error') => {
  if (!message) return;
  const now = Date.now();
  if (state.toasts.some((toast) => toast.raw === message && now - toast.at < 3000)) {
    return;
  }
  // a page's own "saved" message right after the command's "done": one is enough
  if (severity === 'success' && state.toasts.some((toast) => toast.severity === 'success' && !toast.raw && now - toast.at < 2000)) {
    return;
  }
  addToast(severity, String(message), String(message));
};

const finish = (call, ok, message) => {
  const calls = state.calls.filter((running) => running.id !== call.id);
  set({ calls, done: state.done + 1, idleSince: Date.now() });

  if (ok) {
    const now = Date.now();
    // the page already said so itself a moment ago
    if (!state.toasts.some((toast) => toast.severity === 'success' && toast.raw && now - toast.at < 2000)) {
      addToast('success', tr('activity.done', '{{action}}: done', { action: call.label }));
    }
    return;
  }

  const text = tr('activity.failed', '{{action}}: failed', { action: call.label }) + (message ? ' — ' + message : '');
  const now = Date.now();
  const already = message && state.toasts.find((toast) => toast.raw === message && now - toast.at < 3000);
  if (already) {
    // the API layer reported the same error a moment ago: name the command on that message
    set({ toasts: state.toasts.map((toast) => (toast.id === already.id ? { ...toast, text } : toast)) });
  } else {
    addToast('error', text, message);
  }
};

const lastLine = (text) => {
  const lines = text.replace(/\x1b\[[0-9;]*m/g, '').split(/[\r\n]+/).map((line) => line.trim()).filter(Boolean);
  return lines.length ? lines[lines.length - 1].slice(0, 160) : '';
};

// follow reads a copy of the answer to learn how the command ended; for a
// command that reports progress line by line, the latest line is kept as the
// step in progress.
const follow = async (call, response) => {
  let text = '';
  const isJSON = (response.headers.get('content-type') || '').includes('json');
  try {
    if (response.body && response.body.getReader) {
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let shown = 0;
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        text = (text + decoder.decode(value, { stream: true })).slice(-8192);
        const now = Date.now();
        if (!isJSON && now - shown > 300) {
          shown = now;
          const detail = lastLine(text);
          if (detail && state.calls.some((running) => running.id === call.id)) {
            set({ calls: state.calls.map((running) => (running.id === call.id ? { ...running, detail } : running)) });
          }
        }
      }
    } else {
      text = await response.text();
    }
  } catch (e) {
    finish(call, false, tr('activity.interrupted', 'the connection was interrupted'));
    return;
  }

  if (response.status >= 400) {
    let message = '';
    try {
      message = JSON.parse(text).message || '';
    } catch (e) {
      message = isJSON ? '' : lastLine(text);
    }
    finish(call, false, message);
  } else if (text.includes('[OPERATION FAILED]')) {
    finish(call, false, '');
  } else {
    finish(call, true);
  }
};

// trackFetch is called by the API client with every request it sends. It
// returns the same promise it was given.
export const trackFetch = (url, options, promise) => {
  try {
    const method = String((options && options.method) || 'GET').toUpperCase();
    const label = describe(url, method);
    if (!label) {
      return promise;
    }
    const call = { id: nextId++, label, startedAt: Date.now(), detail: '' };
    set({ calls: [...state.calls, call], total: state.total + 1 });
    promise.then(
      (response) => {
        let copy;
        try {
          copy = response.clone();
        } catch (e) {
          finish(call, response.status < 400, '');
          return;
        }
        follow(call, copy);
      },
      () => finish(call, false, tr('activity.unreachable', 'the server did not answer')),
    );
  } catch (e) {
    // following a request must never break it
  }
  return promise;
};

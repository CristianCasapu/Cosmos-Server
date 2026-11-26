// Fork addition: the image pull of a creation as a step of its own. The server adds lines
//   [PULL PROGRESS] {"image":"…","state":"pulling|done|failed","seconds":4,"layers":3,"ready":1,"current":…,"total":…,"reason":"…","detail":"…"}
// to the creation's answer (src/docker/fork_pull_progress.go); they are read here and kept out of the log.

export const PULL_MARKER = '[PULL PROGRESS] ';
export const PULL_REASONS = ['dns', 'unreachable', 'timeout', 'notFound', 'rateLimit', 'diskFull', 'damaged', 'other'];
const STATES = ['pulling', 'done', 'failed'];
const PULLS_MAX = 16;
const TEXT_MAX = 300;

const count = (n) => (Number.isFinite(n) && n >= 0 ? Math.floor(n) : 0);
const text = (s) => String(s || '').replace(/[\u0000-\u001f\u007f-\u009f​-‏‪-‮⁦-⁩﻿]/g, ' ').slice(0, TEXT_MAX);

// One marker line, checked and bounded, or null.
export const parsePullLine = (line) => {
  const s = String(line || '');
  const at = s.indexOf(PULL_MARKER);
  if (at < 0) return null;
  let v;
  try {
    v = JSON.parse(s.slice(at + PULL_MARKER.length).split('\n')[0]);
  } catch (e) {
    return null;
  }
  if (!v || typeof v !== 'object' || !STATES.includes(v.state) || typeof v.image !== 'string' || !v.image) return null;
  const p = {
    image: text(v.image), state: v.state, seconds: count(v.seconds),
    layers: count(v.layers), ready: count(v.ready), current: count(v.current), total: count(v.total),
  };
  if (p.ready > p.layers) p.ready = p.layers;
  if (p.current > p.total) p.current = p.total;
  if (p.state === 'failed') {
    p.reason = PULL_REASONS.includes(v.reason) ? v.reason : 'other';
    p.detail = text(v.detail);
  }
  return p;
};

// The log as shown, without the marker lines.
export const withoutPullLines = (log) => String(log || '').split('\n').filter((l) => !l.includes(PULL_MARKER)).join('\n');

// The pulls so far, one per image in the order they started, after the lines of one piece of the answer.
export const feedPulls = (pulls, piece) => {
  let next = pulls || [];
  for (const line of String(piece || '').split('\n')) {
    const p = parsePullLine(line);
    if (!p) continue;
    const i = next.findIndex((x) => x.image === p.image);
    if (i >= 0) next = next.map((x, j) => (j === i ? p : x));
    else if (next.length < PULLS_MAX) next = [...next, p];
  }
  return next;
};

// 1 kB is 1000 bytes, as Docker counts.
export const formatBytes = (n) => {
  const v = count(n);
  if (v < 1000) return `${v} B`;
  const units = ['kB', 'MB', 'GB', 'TB'];
  let x = v;
  let u = -1;
  while (x >= 1000 && u < units.length - 1) { x /= 1000; u++; }
  return `${x < 10 ? x.toFixed(1) : Math.round(x)} ${units[u]}`;
};

export const formatSeconds = (s) => {
  const v = count(s);
  if (v < 60) return `${v} s`;
  return `${Math.floor(v / 60)} min ${String(v % 60).padStart(2, '0')} s`;
};

// The share downloaded, 0 to 100, when the registry said the sizes; otherwise null.
export const pullPercent = (p) => (p && p.total > 0 ? Math.min(100, Math.round((p.current / p.total) * 100)) : null);

// After this long without a byte, the wait is said as a wait.
export const PULL_SLOW_AFTER = 15;

// The step in words: { key, text, params } for t().
export const pullWords = (p) => {
  const params = {
    image: p.image, time: formatSeconds(p.seconds), ready: p.ready, layers: p.layers,
    current: formatBytes(p.current), total: formatBytes(p.total),
  };
  if (p.state === 'done') return { key: 'mgmt.servapps.create.pull.done', text: '{{image}} downloaded ({{time}}).', params };
  if (p.state === 'failed') return { key: 'mgmt.servapps.create.pull.failed', text: '{{image}} could not be downloaded.', params };
  if (p.total > 0) return { key: 'mgmt.servapps.create.pull.bytes', text: 'Downloading {{image}}: {{current}} of {{total}}, {{ready}} of {{layers}} layers ready ({{time}}).', params };
  if (p.layers > 0) return { key: 'mgmt.servapps.create.pull.layers', text: 'Downloading {{image}}: {{ready}} of {{layers}} layers ready ({{time}}).', params };
  if (p.seconds >= PULL_SLOW_AFTER) return { key: 'mgmt.servapps.create.pull.slow', text: 'Downloading {{image}}: still waiting for the registry after {{time}}. A slow name lookup (DNS) or a slow registry can take a minute or more.', params };
  return { key: 'mgmt.servapps.create.pull.waiting', text: 'Downloading {{image}}: waiting for the registry ({{time}}).', params };
};

// Why it failed, in words: { key, text } for t().
export const pullReasonWords = (reason) => {
  switch (reason) {
    case 'dns': return { key: 'mgmt.servapps.create.pull.reason.dns', text: 'This server could not look up the registry\'s name (DNS). Check the server\'s name servers and its internet connection.' };
    case 'unreachable': return { key: 'mgmt.servapps.create.pull.reason.unreachable', text: 'The registry could not be reached. Check the server\'s internet connection, a firewall or proxy, and the registry\'s address.' };
    case 'timeout': return { key: 'mgmt.servapps.create.pull.reason.timeout', text: 'The registry did not answer in time. The server\'s connection or the registry may be slow or down.' };
    case 'notFound': return { key: 'mgmt.servapps.create.pull.reason.notFound', text: 'The registry has no such image or tag, or it wants a sign-in for it (docker login). Check the image\'s name and tag.' };
    case 'rateLimit': return { key: 'mgmt.servapps.create.pull.reason.rateLimit', text: 'The registry refuses more downloads for now (rate limit). Wait a while, or sign in to the registry for a higher limit.' };
    case 'diskFull': return { key: 'mgmt.servapps.create.pull.reason.diskFull', text: 'The disk that holds Docker\'s data is full. Free some space, then try again.' };
    case 'damaged': return { key: 'mgmt.servapps.create.pull.reason.damaged', text: 'The download was interrupted or arrived damaged. The connection to the registry may be unreliable.' };
    default: return { key: 'mgmt.servapps.create.pull.reason.other', text: 'Docker could not download the image. The technical reason is below.' };
  }
};

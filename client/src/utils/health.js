// Fork addition: the home page's health card (components/healthCard.jsx).
// What needs the admin's attention, from answers the API already gives:
//   failed scheduled jobs        GET /api/jobs
//   apps stopped by an error     GET /api/servapps (a crash or a restart loop)
//   disks nearly full            GET /api/list-metrics + /api/metrics (cosmos.system.disk.*)
//   snapshots that went wrong    GET /api/snapshots
//   apps killed for lack of memory  GET /api/servapps/{name}/ for the apps that exited with 137 (at most 5)
// Each line is { id, severity, text: [key, English, params], link }. The card
// has three states (healthState): every check made and nothing found, something
// needs attention, or not everything could be checked (never "all is well"
// then). Pure functions, tested with node --test.

export const DISK_WARN = 90; // per cent used
export const DISK_FULL = 95;

const s = (key, text, params) => [key, text, params];
const NEVER = '0001-01-01T00:00:00Z';

// jobs: { scheduler: { name: job } } -> the jobs whose last run failed
export const failedJobs = (jobs) => {
  const out = [];
  if (!jobs || typeof jobs !== 'object') return out;
  for (const [scheduler, list] of Object.entries(jobs)) {
    for (const job of Object.values(list || {})) {
      if (!job || job.Disabled || job.Running) continue;
      if (!job.LastRun || job.LastRun === NEVER) continue;
      if (job.LastRunSuccess === false) out.push({ scheduler, name: String(job.Name || '') });
    }
  }
  return out;
};

// A stop by the admin or by Docker (SIGTERM 143, SIGKILL 137, a clean 0) is not a
// problem; another exit code is the app failing, and a restart loop or a dead
// container is too.
const STOPPED_ON_PURPOSE = new Set([0, 137, 143]);
export const exitCode = (status) => {
  const m = /^Exited \((-?\d+)\)/.exec(String(status || ''));
  return m ? Number(m[1]) : null;
};
export const brokenContainers = (containers) => {
  const out = [];
  for (const c of Array.isArray(containers) ? containers : []) {
    if (!c) continue;
    const name = String((c.Names && c.Names[0]) || '').replace(/^\//, '');
    const state = String(c.State || '').toLowerCase();
    if (state === 'restarting' || state === 'dead') { out.push({ name, why: state }); continue; }
    if (state === 'exited') {
      const code = exitCode(c.Status);
      if (code !== null && !STOPPED_ON_PURPOSE.has(code)) out.push({ name, why: 'exited', code });
    }
  }
  return out;
};

// The memory killer ends a container with 137, the code of a stop by the admin
// too; only Docker's inspect says which (State.OOMKilled). These are the apps
// worth asking about, at most `max`: one request each, once per visit.
export const OOM_CHECKS = 5;
export const oomCandidates = (containers, max = OOM_CHECKS) => (Array.isArray(containers) ? containers : [])
  .filter((c) => c && String(c.State || '').toLowerCase() === 'exited' && exitCode(c.Status) === 137)
  .map((c) => String((c.Names && c.Names[0]) || '').replace(/^\//, ''))
  .filter(Boolean)
  .slice(0, max);
// inspected: { name: the inspect answer, or undefined when it could not be read } -> the names killed for memory
export const oomKilled = (inspected) => Object.entries(inspected || {})
  .filter(([, info]) => info && info.State && info.State.OOMKilled === true)
  .map(([name]) => name);

// metrics: the answer of /api/metrics for the disk keys -> the disks at DISK_WARN % or more
export const fullDisks = (metrics) => {
  const out = [];
  for (const m of Array.isArray(metrics) ? metrics : []) {
    if (!m || !/^cosmos\.system\.disk\./.test(m.Key || '') || !(m.Max > 0) || !Array.isArray(m.Values) || !m.Values.length) continue;
    const used = m.Values[m.Values.length - 1].Value;
    if (typeof used !== 'number') continue;
    const percent = Math.round((used / m.Max) * 100);
    if (percent >= DISK_WARN) out.push({ path: String(m.Key).slice('cosmos.system.disk.'.length) || '/', percent });
  }
  return out.sort((a, b) => b.percent - a.percent);
};
export const diskKeys = (names) => Object.keys(names || {}).filter((k) => /^cosmos\.system\.disk\./.test(k)).slice(0, 50);

// snapshots: the answer of /api/snapshots -> what went wrong
export const snapshotProblems = (status) => {
  if (!status || !status.enabled) return [];
  const out = [];
  if (status.error) out.push({ path: '', error: String(status.error) });
  for (const source of status.sources || []) if (source && source.lastError) out.push({ path: String(source.path || ''), error: String(source.lastError) });
  return out;
};

const LINKS = {
  jobs: '/cosmos-ui/cron',
  container: (name) => '/cosmos-ui/servapps/containers/' + encodeURIComponent(name),
  apps: '/cosmos-ui/servapps',
  storage: '/cosmos-ui/storage',
  snapshots: '/cosmos-ui/config-general/general',
};

const names = (list, max = 3) => list.slice(0, max).join(', ') + (list.length > max ? ' …' : '');

// sources: { jobs, containers, disks, snapshots, oom } as read (the certificates have lines of their own
// in the home page's notice area, components/certificates.jsx)
// (undefined: not read); failed: the names of the sources that could not be read
export const healthLines = (sources) => {
  const lines = [];
  const { jobs, containers, disks, snapshots, oom } = sources || {};

  const broken = brokenContainers(containers);
  if (broken.length === 1) {
    const c = broken[0];
    lines.push({ id: 'containers', severity: 'error', link: LINKS.container(c.name),
      text: c.why === 'exited'
        ? s('health.containerExited', '{{name}} stopped with an error (exit code {{code}}).', { name: c.name, code: c.code })
        : s('health.containerLoop', '{{name}} keeps restarting.', { name: c.name }) });
  } else if (broken.length > 1) {
    lines.push({ id: 'containers', severity: 'error', link: LINKS.apps,
      text: s('health.containers', '{{count}} apps stopped with an error or keep restarting: {{names}}.', { count: broken.length, names: names(broken.map((c) => c.name)) }) });
  }
  const killed = Array.isArray(oom) ? oom : [];
  if (killed.length) {
    lines.push({ id: 'oom', severity: 'error', link: killed.length === 1 ? LINKS.container(killed[0]) : LINKS.apps,
      text: killed.length === 1
        ? s('health.oomOne', '{{name}} was stopped because the server ran out of memory.', { name: killed[0] })
        : s('health.oom', '{{count}} apps were stopped because the server ran out of memory: {{names}}.', { count: killed.length, names: names(killed) }) });
  }
  const failed = failedJobs(jobs);
  if (failed.length) {
    lines.push({ id: 'jobs', severity: 'warning', link: LINKS.jobs,
      text: failed.length === 1
        ? s('health.job', 'The scheduled job "{{name}}" failed at its last run.', { name: failed[0].name })
        : s('health.jobs', '{{count}} scheduled jobs failed at their last run: {{names}}.', { count: failed.length, names: names(failed.map((j) => j.name)) }) });
  }
  for (const disk of fullDisks(disks)) {
    lines.push({ id: 'disk:' + disk.path, severity: disk.percent >= DISK_FULL ? 'error' : 'warning', link: LINKS.storage,
      text: s('health.disk', 'The disk {{path}} is {{percent}} % full.', { path: disk.path, percent: disk.percent }) });
  }
  const snaps = snapshotProblems(snapshots);
  if (snaps.length) {
    lines.push({ id: 'snapshots', severity: 'warning', link: LINKS.snapshots,
      text: snaps[0].path
        ? s('health.snapshot', 'The last snapshot of {{path}} did not go cleanly: {{error}}', { path: snaps[0].path, error: snaps[0].error.slice(0, 200) })
        : s('health.snapshotSettings', 'Snapshots cannot run: {{error}}', { error: snaps[0].error.slice(0, 200) }) });
  }
  const order = { error: 0, warning: 1 };
  return lines.sort((a, b) => order[a.severity] - order[b.severity]);
};

// the one calm line, when nothing needs attention
export const calmLine = (sources) => {
  const running = (Array.isArray(sources && sources.containers) ? sources.containers : []).filter((c) => c && c.State === 'running').length;
  return s('health.calm', 'All is well: {{running}} apps running, no failed job, no disk above {{warn}} %, no problem with certificates or snapshots.', { running, warn: DISK_WARN });
};

// A source the server refused to answer because of its request limit, not because something is wrong.
export const isThrottled = (error) => {
  if (!error) return false;
  const code = String(error.code === undefined ? '' : error.code);
  return code === '429' || code === 'HTTP003' || Number(error.status) === 429 || /too many requests/i.test(String(error.message || ''));
};

// The card's state: 'ok' only when every source was read and nothing was found.
//   lines: healthLines(...); unread: the names of the sources that could not be read
export const healthState = (lines, unread) => {
  if (Array.isArray(lines) && lines.length) return 'attention';
  if (Array.isArray(unread) && unread.length) return 'unknown';
  return 'ok';
};

// The line for what could not be checked. `throttled`: at least one of them was refused by the request limit.
export const unreadLine = (unread, throttled) => {
  const what = (Array.isArray(unread) ? unread : []).join(', ');
  return throttled
    ? s('health.unreadThrottled', 'Could not check {{what}}: the server is limiting requests right now. Check again in a minute.', { what })
    : s('health.unreadFailed', 'Could not check {{what}}: the server did not answer. Check again, or see the server\'s logs if it keeps failing.', { what });
};

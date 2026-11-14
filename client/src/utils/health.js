// Fork addition: the home page's health card (components/healthCard.jsx).
// What needs the admin's attention, from answers the API already gives:
//   failed scheduled jobs        GET /api/jobs
//   apps stopped by an error     GET /api/servapps (a crash or a restart loop)
//   disks nearly full            GET /api/list-metrics + /api/metrics (cosmos.system.disk.*)
//   snapshots that went wrong    GET /api/snapshots
//   certificates                 counted from the home page's certificate alerts
// Each line is { id, severity, text: [key, English, params], link }. Pure
// functions, tested with node --test.

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
  certificates: '/cosmos-ui/config-general/https',
};

const names = (list, max = 3) => list.slice(0, max).join(', ') + (list.length > max ? ' …' : '');

// sources: { jobs, containers, disks, snapshots, certificateAlerts } as read
// (undefined: not read); failed: the names of the sources that could not be read
export const healthLines = (sources) => {
  const lines = [];
  const { jobs, containers, disks, snapshots, certificateAlerts } = sources || {};

  if (certificateAlerts && certificateAlerts.length) {
    const errors = certificateAlerts.filter((a) => a.severity === 'error').length;
    lines.push({ id: 'certificates', severity: errors ? 'error' : 'warning', link: LINKS.certificates,
      text: s('health.certificates', '{{count}} certificate problem(s): see the notices above.', { count: certificateAlerts.length }) });
  }
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

// Fork addition: the logic of the alert editor (Monitoring > Alerts,
// pages/dashboard/forkAlertGuide.jsx): ready-made alerts, the unit of a
// metric, a sentence that reads the alert back, and the checks before saving.
// Pure and tested (alertForm.test.mjs).
//
// How the server judges an alert (src/metrics/alerts.go, CheckAlerts): the
// metric's value for the period ("latest": every sample; "hourly", "daily":
// the hour's or the day's aggregate) is compared with the value; with
// "percent" the value is first taken as a percentage of the metric's maximum
// (a disk's size, the RAM). "Throttled": at most once a day.

// The metrics the server collects (src/metrics/system.go), by key prefix.
const KINDS = [
  { prefix: 'cosmos.system.disk.', kind: 'disk', unit: 'bytes', percent: true },
  { prefix: 'cosmos.system.ram', kind: 'ram', unit: 'bytes', percent: true },
  { prefix: 'cosmos.system.cpu.', kind: 'cpu', unit: '%', percent: false },
  { prefix: 'cosmos.system.temp.', kind: 'temp', unit: '°C', percent: false },
  { prefix: 'cosmos.system.disk-health.temperature.', kind: 'temp', unit: '°C', percent: false },
  { prefix: 'cosmos.system.docker.cpu.', kind: 'container-cpu', unit: '%', percent: false },
  { prefix: 'cosmos.system.docker.ram.', kind: 'container-ram', unit: 'bytes', percent: false },
  { prefix: 'cosmos.system.docker.net', kind: 'container-net', unit: 'bytes', percent: false },
  { prefix: 'cosmos.system.net', kind: 'net', unit: 'bytes', percent: false },
  { prefix: 'cosmos.proxy.', kind: 'proxy', unit: 'count', percent: false },
];

export const kindOf = (metric) => {
  const key = String(metric || '');
  // the longest matching prefix wins (disk-health before disk)
  return KINDS.filter((k) => key.startsWith(k.prefix)).sort((a, b) => b.prefix.length - a.prefix.length)[0] || null;
};

// The unit the value is compared in, as words.
export const unitOf = (metric, percent) => {
  const kind = kindOf(metric);
  if (percent) {
    return kind && kind.kind === 'disk'
      ? ['mgmt.monitoring.alerts.fork.unitDiskPercent', '% of the disk\'s size']
      : ['mgmt.monitoring.alerts.fork.unitPercent', '% of the maximum'];
  }
  if (!kind) return ['mgmt.monitoring.alerts.fork.unitRaw', 'in the metric\'s own unit'];
  if (kind.unit === '%') return ['mgmt.monitoring.alerts.fork.unitCpu', '% of the processor'];
  if (kind.unit === '°C') return ['mgmt.monitoring.alerts.fork.unitTemp', '°C'];
  if (kind.unit === 'bytes') return ['mgmt.monitoring.alerts.fork.unitBytes', 'bytes (1 GB = 1000000000)'];
  return ['mgmt.monitoring.alerts.fork.unitCount', 'requests'];
};

// "Percent" only means something where the server knows a maximum.
export const percentMakesSense = (metric) => {
  const kind = kindOf(metric);
  return !kind || kind.percent;
};

// Stop and restart act on a container: only for a container's metric.
export const actionsFor = (metric) => {
  const kind = kindOf(metric);
  const container = Boolean(kind && kind.kind.startsWith('container'));
  return container ? ['notification', 'email', 'stop', 'restart'] : ['notification', 'email'];
};

// Ready-made alerts, filled from the metrics the server has. `metrics` is
// API.metrics.list()'s answer ({key: label}). Thresholds: the usual ones of
// host monitoring (disk 90 %, RAM 90 % over an hour, CPU 90 % over an hour,
// temperature 80 °C).
export const templates = (metrics) => {
  const keys = Object.keys(metrics || {});
  const found = [];
  keys.filter((k) => k.startsWith('cosmos.system.disk.')).sort().forEach((key) => {
    const mount = key.slice('cosmos.system.disk.'.length).replace(/_/g, '.');
    found.push({
      id: 'disk:' + key,
      label: ['mgmt.monitoring.alerts.fork.tplDisk', 'Disk {{mount}} nearly full', { mount }],
      values: { name: 'Disk nearly full ' + mount.replace(/\//g, ' ').trim(), trackingMetric: key, conditionOperator: 'gt', conditionValue: 90, conditionPercent: true, period: 'latest', severity: 'warn', throttled: true, actions: [{ Type: 'notification' }] },
    });
  });
  if (keys.includes('cosmos.system.ram')) {
    found.push({ id: 'ram', label: ['mgmt.monitoring.alerts.fork.tplRam', 'Memory nearly full'],
      values: { name: 'Memory nearly full', trackingMetric: 'cosmos.system.ram', conditionOperator: 'gt', conditionValue: 90, conditionPercent: true, period: 'hourly', severity: 'warn', throttled: true, actions: [{ Type: 'notification' }] } });
  }
  if (keys.includes('cosmos.system.cpu.0')) {
    found.push({ id: 'cpu', label: ['mgmt.monitoring.alerts.fork.tplCpu', 'Processor busy for an hour'],
      values: { name: 'Processor busy', trackingMetric: 'cosmos.system.cpu.0', conditionOperator: 'gt', conditionValue: 90, conditionPercent: false, period: 'hourly', severity: 'warn', throttled: true, actions: [{ Type: 'notification' }] } });
  }
  if (keys.includes('cosmos.system.temp.all')) {
    found.push({ id: 'temp', label: ['mgmt.monitoring.alerts.fork.tplTemp', 'Server too hot'],
      values: { name: 'Server too hot', trackingMetric: 'cosmos.system.temp.all', conditionOperator: 'gt', conditionValue: 80, conditionPercent: false, period: 'latest', severity: 'error', throttled: true, actions: [{ Type: 'notification' }] } });
  }
  return found;
};

// What cannot be an alert here, and where the admin hears about it instead.
export const notAvailable = [
  ['mgmt.monitoring.alerts.fork.naRestart', 'A container that keeps restarting is not a metric: Cosmos reports it through the Telegram notifications (Configuration > General).'],
  ['mgmt.monitoring.alerts.fork.naCert', 'A certificate near its end is not a metric: Cosmos warns on the home page and in the notifications when one could not be renewed.'],
];

const OPS = { gt: ['mgmt.monitoring.alerts.fork.opGt', 'above'], lt: ['mgmt.monitoring.alerts.fork.opLt', 'below'], eq: ['mgmt.monitoring.alerts.fork.opEq', 'exactly'] };
const PERIODS = {
  latest: ['mgmt.monitoring.alerts.fork.periodLatest', 'at any measurement'],
  hourly: ['mgmt.monitoring.alerts.fork.periodHourly', 'on average over an hour'],
  daily: ['mgmt.monitoring.alerts.fork.periodDaily', 'on average over a day'],
};
const ACTIONS = {
  notification: ['mgmt.monitoring.alerts.fork.actNotify', 'show a notification in Cosmos'],
  email: ['mgmt.monitoring.alerts.fork.actEmail', 'send an email to the administrators'],
  stop: ['mgmt.monitoring.alerts.fork.actStop', 'stop the container'],
  restart: ['mgmt.monitoring.alerts.fork.actRestart', 'restart the container'],
};

// The alert read back in parts the component joins: [{text}] with t() triples.
export const readBack = (values, metrics) => {
  const v = values || {};
  const what = (metrics && metrics[v.trackingMetric]) || v.trackingMetric || '?';
  const unit = unitOf(v.trackingMetric, v.conditionPercent);
  const actions = (v.actions || []).filter((a) => !a.removed).map((a) => ACTIONS[a.Type]).filter(Boolean);
  return {
    when: ['mgmt.monitoring.alerts.fork.readWhen', 'When {{what}} is', { what }],
    op: OPS[v.conditionOperator] || OPS.gt,
    value: String(v.conditionValue),
    unit,
    period: PERIODS[v.period] || PERIODS.latest,
    actions,
    throttled: v.throttled
      ? ['mgmt.monitoring.alerts.fork.readOnce', 'At most once a day.']
      : ['mgmt.monitoring.alerts.fork.readEvery', 'Every time it is measured so, which can be every 30 seconds.'],
  };
};

// The checks before saving: { field: [key, English] }.
export const alertProblems = (values, { names, original, metrics } = {}) => {
  const v = values || {};
  const problems = {};
  const name = String(v.name || '').trim();
  if (!name) problems.name = ['mgmt.monitoring.alerts.fork.nameEmpty', 'Give the alert a name'];
  else if (name.length > 64) problems.name = ['mgmt.monitoring.alerts.fork.nameLong', 'At most 64 characters'];
  else if (/[/\\?#%]/.test(name)) problems.name = ['mgmt.monitoring.alerts.fork.nameChars', 'A name cannot hold / \\ ? # or %'];
  else if ((names || []).includes(name) && name !== original) problems.name = ['mgmt.monitoring.alerts.fork.nameTaken', 'Another alert has this name: saving would replace it'];

  const metric = String(v.trackingMetric || '').trim();
  const known = Object.keys(metrics || {});
  const wildcard = metric.endsWith('.*') && known.some((k) => k.startsWith(metric.slice(0, -1)));
  if (!metric) problems.trackingMetric = ['mgmt.monitoring.alerts.fork.metricEmpty', 'Pick what to watch from the list'];
  else if (known.length > 0 && !known.includes(metric) && !wildcard) problems.trackingMetric = ['mgmt.monitoring.alerts.fork.metricUnknown', 'The server has no such metric: pick one from the list'];

  const value = String(v.conditionValue === undefined || v.conditionValue === null ? '' : v.conditionValue).trim();
  if (!/^-?\d+$/.test(value)) problems.conditionValue = ['mgmt.monitoring.alerts.fork.valueNumber', 'A whole number, for example 90'];
  else if (v.conditionPercent && (Number(value) < 0 || Number(value) > 100)) problems.conditionValue = ['mgmt.monitoring.alerts.fork.valuePercent', 'A percentage from 0 to 100'];

  if (v.conditionPercent && metric && !percentMakesSense(metric)) problems.conditionPercent = ['mgmt.monitoring.alerts.fork.percentNo', 'This metric has no maximum to take a percentage of: untick it'];
  const allowed = actionsFor(metric);
  if ((v.actions || []).some((a) => !a.removed && !allowed.includes(a.Type))) problems.actions = ['mgmt.monitoring.alerts.fork.actionNo', 'Stop and restart act on a container: they do nothing for this metric'];
  return problems;
};

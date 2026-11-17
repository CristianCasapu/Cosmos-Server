// Fork addition: the logic of the scheduled-job form's choices
// (pages/cron/forkJobForm.jsx): where the job runs, the schedule as a choice
// in words read back from and written to the expression the server stores,
// and the next runs. Pure and tested (jobForm.test.mjs). The expression is
// the server's own format (six fields, seconds first: utils/cronExpression.js).
import { parseCron, nextCronRun } from './cronExpression.js';

// The schedules offered as a choice; 'custom' is "type it myself".
export const SCHEDULE_KINDS = ['hourly', 'nightly', 'weekly', 'monthly', 'custom'];

// What a new job starts with: every night at 03:00, when a home server is
// usually quiet.
export const DEFAULT_SCHEDULE = { kind: 'nightly', time: '03:00', weekday: 1 };

const TIME = /^([01]\d|2[0-3]):([0-5]\d)$/;
const pad = (n) => String(n).padStart(2, '0');

// The expression of a choice; null for 'custom' or a time that is not HH:MM.
export const scheduleExpression = (choice) => {
  const c = choice || {};
  if (c.kind === 'hourly') return '0 0 * * * *';
  if (!['nightly', 'weekly', 'monthly'].includes(c.kind)) return null;
  const m = TIME.exec(String(c.time || ''));
  if (!m) return null;
  const at = `0 ${Number(m[2])} ${Number(m[1])}`;
  if (c.kind === 'nightly') return `${at} * * *`;
  if (c.kind === 'monthly') return `${at} 1 * *`;
  const day = Number(c.weekday);
  if (!Number.isInteger(day) || day < 0 || day > 6) return null;
  return `${at} * * ${day}`;
};

// The choice an expression stands for: one of the kinds when it is exactly
// what that choice writes, otherwise 'custom' (shown as typed).
export const readSchedule = (expression) => {
  const text = String(expression || '').trim();
  if (text === '0 0 * * * *') return { kind: 'hourly', time: DEFAULT_SCHEDULE.time, weekday: DEFAULT_SCHEDULE.weekday };
  const m = /^0 (\d{1,2}) (\d{1,2}) (\*|1) \* (\*|[0-6])$/.exec(text);
  if (m) {
    const minute = Number(m[1]);
    const hour = Number(m[2]);
    if (minute <= 59 && hour <= 23 && String(minute) === m[1] && String(hour) === m[2]) {
      const time = `${pad(hour)}:${pad(minute)}`;
      if (m[3] === '*' && m[4] === '*') return { kind: 'nightly', time, weekday: DEFAULT_SCHEDULE.weekday };
      if (m[3] === '1' && m[4] === '*') return { kind: 'monthly', time, weekday: DEFAULT_SCHEDULE.weekday };
      if (m[3] === '*' && m[4] !== '*') return { kind: 'weekly', time, weekday: Number(m[4]) };
    }
  }
  return { kind: 'custom', time: DEFAULT_SCHEDULE.time, weekday: DEFAULT_SCHEDULE.weekday };
};

// The next `count` runs after `from` (a clock reading, see clockReading), as
// clock readings; fewer when the schedule has fewer, none when it cannot be
// read or is not made of fields ("@every").
export const nextRuns = (expression, from, count = 3) => {
  const schedule = parseCron(expression, { fiveFields: true }); // the jobs' scheduler takes five fields too
  const runs = [];
  if (!schedule.ok || !from) return runs;
  let at = from;
  for (let i = 0; i < count; i++) {
    const run = nextCronRun(schedule, at);
    if (!run) break;
    runs.push(run);
    at = run;
  }
  return runs;
};

// Where the job runs: 'container' when one is named, else 'server'.
export const placeOf = (container) => (String(container || '').trim() ? 'container' : 'server');

// The containers to choose from, from the Docker list (API.docker.list()):
// { name, running, state }, sorted by name.
export const containerChoices = (list) => (Array.isArray(list) ? list : [])
  .map((c) => ({
    name: String(((c && c.Names && c.Names[0]) || '')).replace(/^\//, ''),
    state: String((c && c.State) || ''),
  }))
  .filter((c) => c.name)
  .map((c) => ({ ...c, running: c.state === 'running' }))
  .sort((a, b) => a.name.localeCompare(b.name));

// The fields of the form a proposed job fills.
export const proposalFields = (proposal) => ({
  Name: proposal.name,
  Command: proposal.command,
  Crontab: proposal.schedule,
});

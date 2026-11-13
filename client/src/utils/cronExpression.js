// Fork addition: reads a schedule the way the server does, so a form can
// refuse what the server would silently not schedule, say in words what the
// schedule means and tell when it runs next. Plain functions, no React: see
// cronExpression.test.mjs.
//
// The server hands every schedule to gocron.CronJob(crontab, true)
// (src/cron/index.go), which is robfig/cron's parser with a mandatory seconds
// field: exactly six fields (second minute hour day-of-month month
// day-of-week), or a descriptor (@hourly, @daily, @midnight, @weekly,
// @monthly, @yearly, @annually, @every <duration>), optionally after
// "TZ=<zone> " or "CRON_TZ=<zone> ". The rules below follow that parser
// (parser.go, spec.go of robfig/cron v3).

const MONTH_NAMES = ['jan', 'feb', 'mar', 'apr', 'may', 'jun', 'jul', 'aug', 'sep', 'oct', 'nov', 'dec'];
const DAY_NAMES = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'];

export const CRON_FIELDS = [
  { name: 'second', min: 0, max: 59 },
  { name: 'minute', min: 0, max: 59 },
  { name: 'hour', min: 0, max: 23 },
  { name: 'dayOfMonth', min: 1, max: 31 },
  { name: 'month', min: 1, max: 12, names: MONTH_NAMES, nameOffset: 1 },
  { name: 'dayOfWeek', min: 0, max: 6, names: DAY_NAMES, nameOffset: 0 },
];

const DESCRIPTORS = {
  '@yearly': '0 0 0 1 1 *',
  '@annually': '0 0 0 1 1 *',
  '@monthly': '0 0 0 1 * *',
  '@weekly': '0 0 0 * * 0',
  '@daily': '0 0 0 * * *',
  '@midnight': '0 0 0 * * *',
  '@hourly': '0 0 * * * *',
};

const fail = (code, details) => ({ ok: false, error: { code, ...(details || {}) } });

// A number or, for months and weekdays, a name. Go's strconv.Atoi: digits with an optional sign.
const numberOrName = (text, field) => {
  if (field.names) {
    const index = field.names.indexOf(text.toLowerCase());
    if (index !== -1) return index + field.nameOffset;
  }
  if (!/^\+?\d{1,9}$/.test(text)) return null;
  return Number(text);
};

const parseField = (text, field) => {
  const values = new Set();
  let star = false;
  const bad = (code) => ({ error: { code, field: field.name, text, min: field.min, max: field.max } });

  const pieces = text.split(',').filter((piece) => piece !== '');
  for (const piece of pieces) {
    const rangeAndStep = piece.split('/');
    const lowAndHigh = rangeAndStep[0].split('-');
    if (rangeAndStep.length > 2) return bad('field');

    let start;
    let end;
    let pieceStar = false;
    if (lowAndHigh[0] === '*' || lowAndHigh[0] === '?') {
      start = field.min;
      end = field.max;
      pieceStar = true;
    } else {
      if (lowAndHigh.length > 2) return bad('field');
      start = numberOrName(lowAndHigh[0], field);
      end = lowAndHigh.length === 2 ? numberOrName(lowAndHigh[1], field) : start;
      if (start === null || end === null) return bad('field');
    }

    let step = 1;
    if (rangeAndStep.length === 2) {
      if (!/^\+?\d{1,9}$/.test(rangeAndStep[1])) return bad('step');
      step = Number(rangeAndStep[1]);
      if (lowAndHigh.length === 1) end = field.max; // "N/step" is "N-max/step"
      if (step > 1) pieceStar = false;
    }

    if (start < field.min || end > field.max) return bad('range');
    if (start > end) return bad('order');
    if (step === 0) return bad('step');

    for (let value = start; value <= end; value += step) values.add(value);
    star = star || pieceStar;
  }

  if (values.size === 0) return bad('field');
  return { values: [...values].sort((a, b) => a - b), star };
};

// Go's time.ParseDuration, as far as "@every" needs it: the length in seconds, or null.
const parseDuration = (text) => {
  const UNITS = { ns: 1e-9, us: 1e-6, 'µs': 1e-6, 'μs': 1e-6, ms: 1e-3, s: 1, m: 60, h: 3600 };
  let rest = text.replace(/^[+-]/, '');
  const negative = text.startsWith('-');
  if (rest === '0') return 0;
  if (rest === '') return null;

  let seconds = 0;
  const part = /^(\d+\.?\d*|\.\d+)(ns|us|µs|μs|ms|s|m|h)/;
  while (rest !== '') {
    const match = part.exec(rest);
    if (!match) return null;
    seconds += Number(match[1]) * UNITS[match[2]];
    rest = rest.slice(match[0].length);
  }
  return negative ? -seconds : seconds;
};

const zoneIsKnown = (zone) => {
  if (zone === '' || zone === 'UTC' || zone === 'Local') return true;
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: zone });
    return true;
  } catch (e) {
    return false;
  }
};

// parseCron reads a schedule. The result is { ok: false, error: { code, ... } } or
// { ok: true, zone, kind: 'fields', second, minute, hour, dayOfMonth, month,
//   dayOfWeek: { values, star } } or { ok: true, zone, kind: 'every', seconds }.
// `zone` is the time zone written in the schedule itself ("" for the server's).
export const parseCron = (expression) => {
  let spec = typeof expression === 'string' ? expression : '';
  if (spec.trim() === '') return fail('empty');

  let zone = '';
  if (spec.startsWith('TZ=') || spec.startsWith('CRON_TZ=')) {
    const space = spec.indexOf(' ');
    if (space === -1) return fail('zoneAlone');
    zone = spec.slice(spec.indexOf('=') + 1, space);
    if (!zoneIsKnown(zone)) return fail('zone', { zone });
    spec = spec.slice(space);
  }
  spec = spec.trim();
  if (spec === '') return fail('zoneAlone');

  if (spec.startsWith('@')) {
    if (DESCRIPTORS[spec]) {
      spec = DESCRIPTORS[spec];
    } else if (spec.startsWith('@every ')) {
      const seconds = parseDuration(spec.slice('@every '.length));
      if (seconds === null) return fail('every', { text: spec });
      // the server runs nothing more often than once a second, and counts whole seconds
      return { ok: true, zone, kind: 'every', seconds: Math.max(1, Math.floor(seconds)) };
    } else {
      return fail('descriptor', { text: spec });
    }
  }

  const parts = spec.split(/\s+/);
  if (parts.length !== CRON_FIELDS.length) return fail('count', { count: parts.length });

  const result = { ok: true, zone, kind: 'fields' };
  for (let i = 0; i < CRON_FIELDS.length; i++) {
    const parsed = parseField(parts[i], CRON_FIELDS[i]);
    if (parsed.error) return { ok: false, error: parsed.error };
    result[CRON_FIELDS[i].name] = parsed;
  }
  if (nextCronRun(result, { year: 2000, month: 1, day: 1, hour: 0, minute: 0, second: 0 }, 9) === null) {
    return fail('never');
  }
  return result;
};

export const isValidCron = (expression) => parseCron(expression).ok;

const dayMatches = (schedule, date) => {
  const inMonth = schedule.dayOfMonth.values.includes(date.getUTCDate());
  const inWeek = schedule.dayOfWeek.values.includes(date.getUTCDay());
  // a day of the month and a day of the week together mean "either", unless one of them is "*"
  return (schedule.dayOfMonth.star || schedule.dayOfWeek.star) ? (inMonth && inWeek) : (inMonth || inWeek);
};

const firstTimeFrom = (schedule, from) => {
  for (const hour of schedule.hour.values) {
    if (hour * 3600 + 3599 < from) continue;
    for (const minute of schedule.minute.values) {
      if (hour * 3600 + minute * 60 + 59 < from) continue;
      for (const second of schedule.second.values) {
        if (hour * 3600 + minute * 60 + second >= from) return { hour, minute, second };
      }
    }
  }
  return null;
};

// nextCronRun: the first run after `from`, both as clock readings
// { year, month (1-12), day, hour, minute, second } in the zone the schedule
// runs in; null when there is none within `years` (the server looks five
// years ahead) or the schedule is not made of fields ("@every").
export const nextCronRun = (schedule, from, years) => {
  if (!schedule || !schedule.ok || schedule.kind !== 'fields' || !from) return null;

  const firstDay = Date.UTC(from.year, from.month - 1, from.day);
  const days = Math.ceil(366 * (years || 5)) + 1;
  for (let i = 0; i < days; i++) {
    const date = new Date(firstDay + i * 86400000);
    if (!schedule.month.values.includes(date.getUTCMonth() + 1) || !dayMatches(schedule, date)) continue;
    const startAt = i === 0 ? from.hour * 3600 + from.minute * 60 + from.second + 1 : 0;
    const time = firstTimeFrom(schedule, startAt);
    if (time) {
      return { year: date.getUTCFullYear(), month: date.getUTCMonth() + 1, day: date.getUTCDate(), ...time };
    }
  }
  return null;
};

// The clock reading of `date` in a time zone ("" or unknown: the device's own).
export const clockReading = (date, timeZone) => {
  if (timeZone) {
    try {
      const parts = new Intl.DateTimeFormat('en-US', {
        timeZone, hourCycle: 'h23', year: 'numeric', month: 'numeric', day: 'numeric',
        hour: 'numeric', minute: 'numeric', second: 'numeric',
      }).formatToParts(date);
      const read = (type) => Number(parts.find((part) => part.type === type).value);
      return { year: read('year'), month: read('month'), day: read('day'), hour: read('hour') % 24, minute: read('minute'), second: read('second') };
    } catch (e) {
      // an unknown zone: fall through to the device's clock
    }
  }
  return {
    year: date.getFullYear(), month: date.getMonth() + 1, day: date.getDate(),
    hour: date.getHours(), minute: date.getMinutes(), second: date.getSeconds(),
  };
};

// ---- in words ----

const plain = (key, text, vars) => text.replace(/\{\{(\w+)\}\}/g, (whole, name) => (vars && vars[name] !== undefined ? String(vars[name]) : whole));

const two = (n) => String(n).padStart(2, '0');
const clock = (hour, minute, second) => two(hour) + ':' + two(minute) + (second ? ':' + two(second) : '');

const ordinal = (n) => {
  const tens = n % 100;
  if (tens >= 11 && tens <= 13) return n + 'th';
  return n + (['th', 'st', 'nd', 'rd'][n % 10] || 'th');
};

const WEEKDAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
const MONTHS = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];

// How a field's values are spread: every value, every n-th from the lowest, one, a run, or a list.
// `round` asks that the steps also fit the turn of the clock: 0, 25 and 50 are not "every 25 minutes".
const shape = (field, bounds, round) => {
  const values = field.values;
  if (values.length === bounds.max - bounds.min + 1) return { kind: 'all' };
  if (values.length === 1) return { kind: 'one', value: values[0] };

  const stride = values[1] - values[0];
  const even = values.every((value, i) => i === 0 || value - values[i - 1] === stride);
  if (even && stride === 1) return { kind: 'run', from: values[0], to: values[values.length - 1] };
  const whole = !round || (bounds.max - bounds.min + 1) % stride === 0;
  if (even && whole && values[0] === bounds.min && values[values.length - 1] + stride > bounds.max) return { kind: 'step', step: stride };
  return { kind: 'list' };
};

// describeCron says what a parsed schedule means. `tr(key, englishText, vars)`
// translates a sentence; without it the English text is used.
export const describeCron = (schedule, tr) => {
  const t = tr || plain;
  if (!schedule || !schedule.ok) return '';

  const list = (items) => {
    if (items.length <= 1) return String(items[0] === undefined ? '' : items[0]);
    return t('mgmt.cron.words.list', '{{items}} and {{last}}', { items: items.slice(0, -1).join(', '), last: items[items.length - 1] });
  };

  if (schedule.kind === 'every') {
    const total = schedule.seconds;
    const parts = [];
    const hours = Math.floor(total / 3600);
    const minutes = Math.floor((total % 3600) / 60);
    const seconds = total % 60;
    if (hours) parts.push(hours === 1 ? t('mgmt.cron.words.hour', '1 hour') : t('mgmt.cron.words.hours', '{{n}} hours', { n: hours }));
    if (minutes) parts.push(minutes === 1 ? t('mgmt.cron.words.minute', '1 minute') : t('mgmt.cron.words.minutes', '{{n}} minutes', { n: minutes }));
    if (seconds) parts.push(seconds === 1 ? t('mgmt.cron.words.second', '1 second') : t('mgmt.cron.words.seconds', '{{n}} seconds', { n: seconds }));
    return t('mgmt.cron.words.everyDuration', 'Every {{duration}}, counted from the moment Cosmos starts', { duration: list(parts) });
  }

  const [SECOND, MINUTE, HOUR, DAY, MONTH, WEEKDAY] = CRON_FIELDS;
  const second = shape(schedule.second, SECOND, true);
  const minute = shape(schedule.minute, MINUTE, true);
  const hour = shape(schedule.hour, HOUR, true);
  const weekdayName = (n) => t('mgmt.cron.weekday.' + n, WEEKDAYS[n]);
  const monthName = (n) => t('mgmt.cron.month.' + n, MONTHS[n - 1]);

  // -- which days
  const month = shape(schedule.month, MONTH);
  let dayOfMonth = shape(schedule.dayOfMonth, DAY);
  let weekday = shape(schedule.dayOfWeek, WEEKDAY);
  const either = !schedule.dayOfMonth.star && !schedule.dayOfWeek.star;
  if (either && (dayOfMonth.kind === 'all' || weekday.kind === 'all')) {
    // "either" with one side matching every day is every day
    dayOfMonth = { kind: 'all' };
    weekday = { kind: 'all' };
  }

  const weekdaysText = () => (weekday.kind === 'run' && schedule.dayOfWeek.values.length > 2
    ? t('mgmt.cron.words.weekdayRun', 'every day from {{from}} to {{to}}', { from: weekdayName(weekday.from), to: weekdayName(weekday.to) })
    : t('mgmt.cron.words.weekdays', 'every {{days}}', { days: list(schedule.dayOfWeek.values.map(weekdayName)) }));
  const monthsText = () => (month.kind === 'run' && schedule.month.values.length > 2
    ? t('mgmt.cron.words.monthRun', '{{from}} to {{to}}', { from: monthName(month.from), to: monthName(month.to) })
    : list(schedule.month.values.map(monthName)));
  const datesText = () => list(schedule.dayOfMonth.values.map(ordinal));

  let days;
  const everyDay = dayOfMonth.kind === 'all' && weekday.kind === 'all';
  if (everyDay) {
    days = month.kind === 'all'
      ? t('mgmt.cron.words.everyDay', 'every day')
      : t('mgmt.cron.words.everyDayIn', 'every day in {{months}}', { months: monthsText() });
  } else {
    const parts = [];
    if (dayOfMonth.kind !== 'all') {
      if (dayOfMonth.kind === 'step' && schedule.dayOfMonth.values.length > 4) {
        const dates = schedule.dayOfMonth.values;
        const vars = { n: dayOfMonth.step, first: ordinal(dates[0]), second: ordinal(dates[1]), third: ordinal(dates[2]), months: monthsText() };
        parts.push(month.kind === 'all'
          ? t('mgmt.cron.words.everyNDays', 'every {{n}} days (the {{first}}, {{second}}, {{third}} and so on of each month)', vars)
          : t('mgmt.cron.words.everyNDaysIn', 'every {{n}} days (the {{first}}, {{second}}, {{third}} and so on) in {{months}}', vars));
      } else {
        parts.push(month.kind === 'all'
          ? t('mgmt.cron.words.dates', 'every month on the {{dates}}', { dates: datesText() })
          : t('mgmt.cron.words.datesIn', 'every year on the {{dates}} of {{months}}', { dates: datesText(), months: monthsText() }));
      }
    }
    if (weekday.kind !== 'all') {
      parts.push(month.kind === 'all'
        ? weekdaysText()
        : t('mgmt.cron.words.weekdaysIn', '{{days}} in {{months}}', { days: weekdaysText(), months: monthsText() }));
    }
    days = parts.length === 2 ? t('mgmt.cron.words.either', '{{first}}, and also {{second}}', { first: parts[0], second: parts[1] }) : parts[0];
  }

  // -- at which times
  const hours = schedule.hour.values;
  const compact = (values) => {
    const runs = [];
    values.forEach((value) => {
      const last = runs[runs.length - 1];
      if (last && value === last[1] + 1) last[1] = value; else runs.push([value, value]);
    });
    return list(runs.map(([from, to]) => (from === to ? String(from) : to === from + 1 ? from + ', ' + to : from + '-' + to)));
  };
  const duringHours = () => {
    if (hour.kind === 'all') return '';
    if (hour.kind === 'one' || hour.kind === 'run') {
      return ' ' + t('mgmt.cron.words.between', 'between {{from}} and {{to}}', { from: clock(hours[0], 0, 0), to: clock(hours[hours.length - 1], 59, 0) });
    }
    return ' ' + t('mgmt.cron.words.duringHours', 'during the hours {{hours}}', { hours: compact(hours) });
  };

  let times = '';
  let repeat = '';
  if (second.kind === 'one' && minute.kind === 'one') {
    const at = (h) => clock(h, minute.value, second.value);
    if (hour.kind === 'all') {
      repeat = (minute.value === 0 && second.value === 0)
        ? t('mgmt.cron.words.everyHour', 'every hour, on the hour')
        : t('mgmt.cron.words.everyHourAt', 'every hour at minute {{minute}}', { minute: minute.value });
      if (second.value !== 0) repeat += t('mgmt.cron.words.atSecond', ', at second {{second}} of the minute', { second: second.value });
    } else if (hour.kind === 'step' && hours.length > 6) {
      repeat = t('mgmt.cron.words.everyNHours', 'every {{n}} hours ({{first}}, {{second}}, {{third}} and so on)', { n: hour.step, first: at(hours[0]), second: at(hours[1]), third: at(hours[2]) });
    } else if (hour.kind === 'run' && hours.length > 2) {
      repeat = t('mgmt.cron.words.everyHourFromTo', 'every hour from {{from}} to {{to}}', { from: at(hours[0]), to: at(hours[hours.length - 1]) });
    } else {
      times = list(hours.map(at));
    }
  } else if (second.kind === 'one') {
    if (minute.kind === 'run' && hour.kind === 'one') {
      repeat = t('mgmt.cron.words.everyMinuteFromTo', 'every minute from {{from}} to {{to}}', { from: clock(hour.value, minute.from, 0), to: clock(hour.value, minute.to, 0) });
    } else {
      if (minute.kind === 'all') repeat = t('mgmt.cron.words.everyMinute', 'every minute');
      else if (minute.kind === 'step') repeat = t('mgmt.cron.words.everyNMinutes', 'every {{n}} minutes', { n: minute.step });
      else if (hour.kind === 'all') repeat = t('mgmt.cron.words.atMinutes', 'at the minutes {{minutes}} of every hour', { minutes: compact(schedule.minute.values) });
      else repeat = t('mgmt.cron.words.atMinutesPast', 'at the minutes {{minutes}} past the hour', { minutes: compact(schedule.minute.values) });
      repeat += duringHours();
    }
    if (second.value !== 0) repeat += t('mgmt.cron.words.atSecond', ', at second {{second}} of the minute', { second: second.value });
  } else {
    if (second.kind === 'all') repeat = t('mgmt.cron.words.everySecond', 'every second');
    else if (second.kind === 'step') repeat = t('mgmt.cron.words.everyNSeconds', 'every {{n}} seconds', { n: second.step });
    else repeat = t('mgmt.cron.words.atSeconds', 'at the seconds {{seconds}} of every minute', { seconds: compact(schedule.second.values) });
    if (minute.kind === 'one') repeat += t('mgmt.cron.words.duringMinute', ', during minute {{minute}} of the hour', { minute: minute.value });
    else if (minute.kind !== 'all') repeat += t('mgmt.cron.words.duringMinutes', ', during the minutes {{minutes}} of the hour', { minutes: compact(schedule.minute.values) });
    repeat += duringHours();
  }

  let sentence;
  if (times) sentence = t('mgmt.cron.words.daysAt', '{{days}} at {{times}}', { days, times });
  else if (everyDay && month.kind === 'all') sentence = repeat;
  else sentence = t('mgmt.cron.words.daysRepeat', '{{days}}, {{repeat}}', { days, repeat });
  return sentence.charAt(0).toUpperCase() + sentence.slice(1);
};

// Why a schedule is refused, as a sentence for the admin.
export const cronErrorText = (error, tr) => {
  const t = tr || plain;
  const FIELD_NAMES = {
    second: t('mgmt.cron.field.second', 'Part 1 (second)'),
    minute: t('mgmt.cron.field.minute', 'Part 2 (minute)'),
    hour: t('mgmt.cron.field.hour', 'Part 3 (hour)'),
    dayOfMonth: t('mgmt.cron.field.dayOfMonth', 'Part 4 (day of the month)'),
    month: t('mgmt.cron.field.month', 'Part 5 (month)'),
    dayOfWeek: t('mgmt.cron.field.dayOfWeek', 'Part 6 (day of the week)'),
  };
  const vars = { ...error, field: FIELD_NAMES[error.field] };

  switch (error.code) {
    case 'empty':
      return t('mgmt.cron.error.empty', 'Enter a schedule, or pick one below.');
    case 'count':
      return error.count === 5
        ? t('mgmt.cron.error.five', 'This has 5 parts; Cosmos needs 6, the first one being the seconds. Put "0 " in front of it.')
        : t('mgmt.cron.error.count', 'A schedule has 6 parts separated by spaces (second, minute, hour, day of the month, month, day of the week). This one has {{count}}.', vars);
    case 'range':
      return error.field === 'dayOfWeek'
        ? t('mgmt.cron.error.weekday', '{{field}} goes from 0 (Sunday) to 6 (Saturday), or SUN to SAT: "{{text}}" does not fit.', vars)
        : t('mgmt.cron.error.range', '{{field}} goes from {{min}} to {{max}}: "{{text}}" does not fit.', vars);
    case 'order':
      return t('mgmt.cron.error.order', '{{field}}: in "{{text}}" the range starts after it ends.', vars);
    case 'step':
      return t('mgmt.cron.error.step', '{{field}}: in "{{text}}" the number after "/" must be 1 or more.', vars);
    case 'field':
      return t('mgmt.cron.error.field', '{{field}}: "{{text}}" is not understood. Use a number, *, a list (1,15), a range (1-5) or a step (*/2).', vars);
    case 'descriptor':
      return t('mgmt.cron.error.descriptor', '"{{text}}" is not known. The words that work are @hourly, @daily, @weekly, @monthly, @yearly and @every followed by a duration such as 1h30m.', vars);
    case 'every':
      return t('mgmt.cron.error.every', 'After @every comes a duration such as 30s, 15m or 1h30m.', vars);
    case 'zone':
      return t('mgmt.cron.error.zone', 'The time zone "{{zone}}" is not known. Write it like Europe/Paris.', vars);
    case 'zoneAlone':
      return t('mgmt.cron.error.zoneAlone', 'After the time zone comes a space and then the schedule.', vars);
    case 'never':
      return t('mgmt.cron.error.never', 'This schedule never comes up (such a date does not exist).', vars);
    default:
      return t('mgmt.cron.invalidCron', 'Invalid CRONTAB format (use 6 parts)');
  }
};

// Schedules to pick from. The label is the English text of the key.
export const CRON_PRESETS = [
  { key: 'mgmt.cron.preset.15min', label: 'Every 15 minutes', expression: '0 */15 * * * *' },
  { key: 'mgmt.cron.preset.hourly', label: 'Every hour', expression: '0 0 * * * *' },
  { key: 'mgmt.cron.preset.6hours', label: 'Every 6 hours', expression: '0 0 */6 * * *' },
  { key: 'mgmt.cron.preset.daily', label: 'Every day at 03:00', expression: '0 0 3 * * *' },
  { key: 'mgmt.cron.preset.weekly', label: 'Every Sunday at 03:00', expression: '0 0 3 * * 0' },
  { key: 'mgmt.cron.preset.monthly', label: 'Every month on the 1st at 03:00', expression: '0 0 3 1 * *' },
];

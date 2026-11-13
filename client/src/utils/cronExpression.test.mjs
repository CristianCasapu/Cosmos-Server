// Fork addition: tests of cronExpression.js. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  parseCron, isValidCron, describeCron, nextCronRun, clockReading, cronErrorText, CRON_PRESETS,
} from './cronExpression.js';

const words = (expression) => describeCron(parseCron(expression));
const errorCode = (expression) => {
  const parsed = parseCron(expression);
  return parsed.ok ? 'ok' : parsed.error.code;
};
const at = (year, month, day, hour = 0, minute = 0, second = 0) => ({ year, month, day, hour, minute, second });
const next = (expression, from) => nextCronRun(parseCron(expression), from);

test('six fields are required, seconds first', () => {
  assert.equal(errorCode('0 0 3 * * *'), 'ok');
  assert.equal(errorCode('  0  0 3 *   * * '), 'ok');
  assert.equal(errorCode('0 3 * * *'), 'count');
  assert.equal(parseCron('0 3 * * *').error.count, 5);
  assert.equal(errorCode('0 0 3 * * * *'), 'count');
  assert.equal(errorCode('* * *'), 'count');
  assert.equal(errorCode(''), 'empty');
  assert.equal(errorCode('   '), 'empty');
  assert.equal(errorCode(undefined), 'empty');
});

test('each field has its limits', () => {
  assert.equal(errorCode('59 59 23 31 12 6'), 'ok');
  assert.equal(errorCode('0 0 0 1 1 0'), 'ok');
  assert.deepEqual([errorCode('60 * * * * *'), parseCron('60 * * * * *').error.field], ['range', 'second']);
  assert.deepEqual([errorCode('0 60 * * * *'), parseCron('0 60 * * * *').error.field], ['range', 'minute']);
  assert.deepEqual([errorCode('0 0 24 * * *'), parseCron('0 0 24 * * *').error.field], ['range', 'hour']);
  assert.deepEqual([errorCode('0 0 0 0 * *'), parseCron('0 0 0 0 * *').error.field], ['range', 'dayOfMonth']);
  assert.deepEqual([errorCode('0 0 0 32 * *'), parseCron('0 0 0 32 * *').error.field], ['range', 'dayOfMonth']);
  assert.deepEqual([errorCode('0 0 0 * 13 *'), parseCron('0 0 0 * 13 *').error.field], ['range', 'month']);
  assert.deepEqual([errorCode('0 0 0 * 0 *'), parseCron('0 0 0 * 0 *').error.field], ['range', 'month']);
  // the server's parser has no 7 for Sunday
  assert.deepEqual([errorCode('0 0 0 * * 7'), parseCron('0 0 0 * * 7').error.field], ['range', 'dayOfWeek']);
});

test('lists, ranges, steps and names', () => {
  assert.deepEqual(parseCron('0 0,15,30 * * * *').minute.values, [0, 15, 30]);
  assert.deepEqual(parseCron('0 0 9-12 * * *').hour.values, [9, 10, 11, 12]);
  assert.deepEqual(parseCron('0 */20 * * * *').minute.values, [0, 20, 40]);
  assert.deepEqual(parseCron('0 5/20 * * * *').minute.values, [5, 25, 45]);
  assert.deepEqual(parseCron('0 10-30/10 * * * *').minute.values, [10, 20, 30]);
  assert.deepEqual(parseCron('0 0 0 * JAN,jul *').month.values, [1, 7]);
  assert.deepEqual(parseCron('0 0 0 * * MON-FRI').dayOfWeek.values, [1, 2, 3, 4, 5]);
  assert.deepEqual(parseCron('0 0 0 ? * sun').dayOfWeek.values, [0]);
  assert.deepEqual(parseCron('0 1,,2 0 * * *').minute.values, [1, 2]);

  assert.equal(parseCron('0 0 0 * * *').dayOfMonth.star, true);
  assert.equal(parseCron('0 0 0 ? * *').dayOfMonth.star, true);
  assert.equal(parseCron('0 0 0 */1 * *').dayOfMonth.star, true);
  assert.equal(parseCron('0 0 0 */2 * *').dayOfMonth.star, false);
  assert.equal(parseCron('0 0 0 1-31 * *').dayOfMonth.star, false);

  assert.equal(errorCode('0 0 5-1 * * *'), 'order');
  assert.equal(errorCode('0 */0 * * * *'), 'step');
  assert.equal(errorCode('0 */x * * * *'), 'step');
  assert.equal(errorCode('0 1/2/3 * * * *'), 'field');
  assert.equal(errorCode('0 1-2-3 * * * *'), 'field');
  assert.equal(errorCode('0 abc * * * *'), 'field');
  assert.equal(errorCode('0 , * * * *'), 'field');
  assert.equal(errorCode('0 -5 * * * *'), 'field');
  assert.equal(errorCode('0 1.5 * * * *'), 'field');
  assert.equal(errorCode('0 0 0 * mon *'), 'field'); // a weekday name is not a month
});

test('descriptors', () => {
  for (const word of ['@yearly', '@annually', '@monthly', '@weekly', '@daily', '@midnight', '@hourly']) {
    assert.equal(errorCode(word), 'ok', word);
  }
  assert.equal(errorCode(' @daily '), 'ok');
  assert.equal(errorCode('@reboot'), 'descriptor');
  assert.equal(errorCode('@Daily'), 'descriptor');
  assert.equal(errorCode('@every'), 'descriptor');
  assert.deepEqual(parseCron('@every 1h30m'), { ok: true, zone: '', kind: 'every', seconds: 5400 });
  assert.equal(parseCron('@every 90s').seconds, 90);
  assert.equal(parseCron('@every 1.5h').seconds, 5400);
  assert.equal(parseCron('@every 500ms').seconds, 1);
  assert.equal(errorCode('@every 5'), 'every');
  assert.equal(errorCode('@every 5 minutes'), 'every');
  assert.equal(errorCode('@every x'), 'every');
  assert.equal(errorCode('@every '), 'descriptor'); // the server trims it to "@every"
});

test('a time zone in front', () => {
  assert.equal(parseCron('TZ=Europe/Paris 0 0 3 * * *').zone, 'Europe/Paris');
  assert.equal(parseCron('CRON_TZ=UTC @daily').zone, 'UTC');
  assert.equal(errorCode('TZ=Nowhere/Atlantis 0 0 3 * * *'), 'zone');
  // without a space the server's parser does not survive it
  assert.equal(errorCode('TZ=UTC'), 'zoneAlone');
  assert.equal(errorCode('TZ=UTC '), 'zoneAlone');
});

test('a date that does not exist is refused', () => {
  assert.equal(errorCode('0 0 0 31 2 *'), 'never');
  assert.equal(errorCode('0 0 0 30 2 *'), 'never');
  assert.equal(errorCode('0 0 0 31 4,6,9,11 *'), 'never');
  assert.equal(errorCode('0 0 0 29 2 *'), 'ok');
  assert.equal(errorCode('0 0 0 31 2 0'), 'ok'); // "either": every Sunday of February
  assert.equal(isValidCron('0 0 0 31 2 *'), false);
  assert.equal(isValidCron('0 0 3 * * *'), true);
});

test('in words: the usual schedules', () => {
  assert.equal(words('0 0 3 * * *'), 'Every day at 03:00');
  assert.equal(words('0 0 3 * * 0'), 'Every Sunday at 03:00');
  assert.equal(words('0 0 3 1 * *'), 'Every month on the 1st at 03:00');
  assert.equal(words('0 0 * * * *'), 'Every hour, on the hour');
  assert.equal(words('0 */15 * * * *'), 'Every 15 minutes');
  assert.equal(words('0 0 */6 * * *'), 'Every day at 00:00, 06:00, 12:00 and 18:00');
  assert.equal(words('0 0 */2 * * *'), 'Every 2 hours (00:00, 02:00, 04:00 and so on)');
  assert.equal(words('0 * * * * *'), 'Every minute');
  assert.equal(words('* * * * * *'), 'Every second');
  assert.equal(words('*/10 * * * * *'), 'Every 10 seconds');
  assert.equal(words('0 30 * * * *'), 'Every hour at minute 30');
  assert.equal(words('30 0 3 * * *'), 'Every day at 03:00:30');
  assert.equal(words('0 30 9 * * 1-5'), 'Every day from Monday to Friday at 09:30');
  assert.equal(words('0 0 20 * * 1,3,5'), 'Every Monday, Wednesday and Friday at 20:00');
  assert.equal(words('0 0 0 1,15 * *'), 'Every month on the 1st and 15th at 00:00');
  assert.equal(words('0 0 8-18 * * *'), 'Every hour from 08:00 to 18:00');
  assert.equal(words('0 */5 9-17 * * 1-5'), 'Every day from Monday to Friday, every 5 minutes between 09:00 and 17:59');
  assert.equal(words('0 0,30 * * * *'), 'Every 30 minutes');
  assert.equal(words('0 0,20 * * * *'), 'At the minutes 0 and 20 of every hour');
  // 50 to 0 is not 25 minutes
  assert.equal(words('0 */25 * * * *'), 'At the minutes 0, 25 and 50 of every hour');
  assert.equal(words('0 0 */5 * * *'), 'Every day at 00:00, 05:00, 10:00, 15:00 and 20:00');
  assert.equal(words('0 10-20 3 * * *'), 'Every minute from 03:10 to 03:20');
  assert.equal(words('0 0,20 8-17 * * *'), 'At the minutes 0 and 20 past the hour between 08:00 and 17:59');
  assert.equal(words('0 * 3 * * 0'), 'Every Sunday, every minute between 03:00 and 03:59');
  assert.equal(words('15 */5 * * * *'), 'Every 5 minutes, at second 15 of the minute');
  assert.equal(words('*/30 5 2,4 * * *'), 'Every 30 seconds, during minute 5 of the hour during the hours 2 and 4');
  assert.equal(words('0 0 0 * 6-8 *'), 'Every day in June to August at 00:00');
  assert.equal(words('0 0 6 2,22,23 * *'), 'Every month on the 2nd, 22nd and 23rd at 06:00');
  assert.equal(words('0 0 6 11,12,13 * *'), 'Every month on the 11th, 12th and 13th at 06:00');
});

test('in words: the defaults of the SnapRAID form', () => {
  assert.equal(words('0 0 2 * * *'), 'Every day at 02:00');
  assert.equal(words('0 0 4 */2 * *'), 'Every 2 days (the 1st, 3rd, 5th and so on of each month) at 04:00');
});

test('in words: descriptors and durations', () => {
  assert.equal(words('@daily'), 'Every day at 00:00');
  assert.equal(words('@hourly'), 'Every hour, on the hour');
  assert.equal(words('@weekly'), 'Every Sunday at 00:00');
  assert.equal(words('@monthly'), 'Every month on the 1st at 00:00');
  assert.equal(words('@yearly'), 'Every year on the 1st of January at 00:00');
  assert.equal(words('@every 1h30m'), 'Every 1 hour and 30 minutes, counted from the moment Cosmos starts');
  assert.equal(words('@every 45s'), 'Every 45 seconds, counted from the moment Cosmos starts');
});

test('in words: a day of the month and a day of the week mean either one', () => {
  assert.equal(words('0 0 12 1 * 0'), 'Every month on the 1st, and also every Sunday at 12:00');
  // ... unless one of them covers every day
  assert.equal(words('0 0 3 1-31 * 0'), 'Every day at 03:00');
  assert.equal(words('0 0 3 * * 0-6'), 'Every day at 03:00');
  assert.equal(words('0 0 3 ? * MON'), 'Every Monday at 03:00');
});

test('in words: every preset says what its label says', () => {
  assert.deepEqual(CRON_PRESETS.map((preset) => [preset.label, words(preset.expression)]), [
    ['Every 15 minutes', 'Every 15 minutes'],
    ['Every hour', 'Every hour, on the hour'],
    ['Every 6 hours', 'Every day at 00:00, 06:00, 12:00 and 18:00'],
    ['Every day at 03:00', 'Every day at 03:00'],
    ['Every Sunday at 03:00', 'Every Sunday at 03:00'],
    ['Every month on the 1st at 03:00', 'Every month on the 1st at 03:00'],
  ]);
});

test('in words: nothing for a schedule that is refused, and a translator is used when given', () => {
  assert.equal(words('0 3 * * *'), '');
  const seen = [];
  const text = describeCron(parseCron('0 0 3 * * 0'), (key, english, vars) => {
    seen.push(key);
    return english.replace(/\{\{(\w+)\}\}/g, (whole, name) => vars[name]).toUpperCase();
  });
  assert.equal(text, 'EVERY SUNDAY AT 03:00');
  assert.deepEqual(seen, ['mgmt.cron.weekday.0', 'mgmt.cron.words.weekdays', 'mgmt.cron.words.daysAt']);
});

test('the reason a schedule is refused is a full sentence', () => {
  const reason = (expression) => cronErrorText(parseCron(expression).error);
  assert.match(reason('0 3 * * *'), /5 parts.*Put "0 " in front/);
  assert.match(reason('0 0 3 * * * *'), /has 7\.$/);
  assert.equal(reason('0 0 25 * * *'), 'Part 3 (hour) goes from 0 to 23: "25" does not fit.');
  assert.match(reason('0 0 0 * * 7'), /0 \(Sunday\) to 6 \(Saturday\)/);
  assert.match(reason('@reboot'), /"@reboot" is not known/);
  assert.match(reason('TZ=Nowhere/Atlantis 0 0 3 * * *'), /"Nowhere\/Atlantis" is not known/);
  for (const bad of ['', '0 */0 * * * *', '0 5-1 * * * *', '0 x * * * *', '@every x', 'TZ=UTC', '0 0 0 31 2 *']) {
    const text = reason(bad);
    assert.ok(text.length > 20 && !text.includes('{{'), bad + ' -> ' + text);
  }
});

test('next run', () => {
  assert.deepEqual(next('0 0 3 * * *', at(2026, 11, 13, 10)), at(2026, 11, 14, 3));
  assert.deepEqual(next('0 0 3 * * *', at(2026, 11, 13, 2, 59, 59)), at(2026, 11, 13, 3));
  assert.deepEqual(next('0 0 3 * * *', at(2026, 11, 13, 3)), at(2026, 11, 14, 3)); // strictly after
  assert.deepEqual(next('*/10 * * * * *', at(2026, 11, 13, 10, 0, 5)), at(2026, 11, 13, 10, 0, 10));
  assert.deepEqual(next('0 */15 * * * *', at(2026, 11, 13, 23, 50)), at(2026, 11, 14, 0, 0));
  assert.deepEqual(next('0 0 3 1 * *', at(2026, 11, 13, 10)), at(2026, 12, 1, 3));
  assert.deepEqual(next('@yearly', at(2026, 11, 13, 10)), at(2027, 1, 1));
  assert.deepEqual(next('0 0 0 29 2 *', at(2026, 11, 13, 10)), at(2028, 2, 29));
  assert.deepEqual(next('0 0 0 31 * *', at(2026, 11, 13, 10)), at(2026, 12, 31));

  // 2026-11-13 is a Friday
  assert.equal(new Date(Date.UTC(2026, 10, 13)).getUTCDay(), 5);
  assert.deepEqual(next('0 0 3 * * 0', at(2026, 11, 13, 10)), at(2026, 11, 15, 3));
  assert.deepEqual(next('0 30 9 * * MON-FRI', at(2026, 11, 13, 10)), at(2026, 11, 16, 9, 30));
  // the 1st or a Sunday, whichever comes first
  assert.deepEqual(next('0 0 12 1 * 0', at(2026, 11, 13, 10)), at(2026, 11, 15, 12));
  assert.deepEqual(next('0 0 12 1 * 0', at(2026, 11, 29, 13)), at(2026, 12, 1, 12));

  assert.equal(next('@every 5m', at(2026, 11, 13)), null);
  assert.equal(next('not a schedule', at(2026, 11, 13)), null);
  assert.equal(nextCronRun(parseCron('0 0 3 * * *'), null), null);
});

test('clockReading gives the wall clock of a zone', () => {
  const instant = new Date(Date.UTC(2026, 10, 13, 23, 30, 15));
  assert.deepEqual(clockReading(instant, 'UTC'), at(2026, 11, 13, 23, 30, 15));
  assert.deepEqual(clockReading(instant, 'Europe/Paris'), at(2026, 11, 14, 0, 30, 15));
  assert.deepEqual(clockReading(instant, 'America/New_York'), at(2026, 11, 13, 18, 30, 15));
  // an unknown zone: the device's clock
  assert.deepEqual(clockReading(instant, 'Nowhere/Atlantis'), clockReading(instant, ''));
  assert.equal(clockReading(instant, '').minute, 30);
});

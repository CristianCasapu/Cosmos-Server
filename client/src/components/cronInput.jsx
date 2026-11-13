// Fork addition: help for a field that holds a schedule (crontab). Under the
// field it says in words what the schedule means and when it runs next, or
// why the server would not take it, and offers common schedules to click
// instead of typing one. The rules are the server's own (six fields, seconds
// first): see utils/cronExpression.js.
import * as React from 'react';
import { Chip, FormHelperText, Stack, Typography } from '@mui/material';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import { getNestedValue } from '../pages/config/users/formShortcuts';
import {
  parseCron, isValidCron, describeCron, cronErrorText, nextCronRun, clockReading, CRON_PRESETS,
} from '../utils/cronExpression';

// For a Formik `validate`: marks the schedule fields the server would not
// schedule. The reason is shown by <CronHelp> under the field, so the mark
// carries no text of its own.
export const cronFieldErrors = (values, names) => {
  const errors = {};
  names.forEach((name) => {
    if (!isValidCron(values[name])) errors[name] = true;
  });
  return errors;
};

// A schedule in words, for a list; upstream's "invalid" text when it is not one.
export const cronInWords = (crontab, t) => {
  const schedule = parseCron(crontab);
  return schedule.ok ? describeCron(schedule, t) : t('mgmt.cron.invalidCron', 'Invalid CRONTAB format (use 6 parts)');
};

// The server's time zone, asked once. Only trusted when Cosmos runs directly
// on the server: in a container its clock may be set differently.
let serverZone = null;
const getServerZone = () => {
  if (!serverZone) {
    serverZone = API.getStatus()
      .then((res) => ((res && res.data && res.data.containerized === false && res.data.hostTimezone) || ''))
      .catch(() => '');
  }
  return serverZone;
};

const formatRun = (run, language) => {
  const options = {
    weekday: 'long', day: 'numeric', month: 'long', year: 'numeric',
    hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZone: 'UTC',
  };
  if (run.second) options.second = '2-digit';
  const date = new Date(Date.UTC(run.year, run.month - 1, run.day, run.hour, run.minute, run.second));
  try {
    return new Intl.DateTimeFormat(language || 'en', options).format(date);
  } catch (e) {
    return new Intl.DateTimeFormat('en', options).format(date);
  }
};

const CronHelp = ({ formik, name, disabled }) => {
  const { t, i18n } = useTranslation();
  const [zone, setZone] = React.useState('');
  const value = getNestedValue(formik.values, name) || '';
  const schedule = parseCron(value);
  const asked = !!(formik.touched[name] || formik.submitCount > 0);

  React.useEffect(() => {
    let live = true;
    getServerZone().then((found) => {
      if (live) setZone(found);
    });
    return () => { live = false; };
  }, []);

  // the form validates on blur and on submit only: do not leave the field marked once it is right
  React.useEffect(() => {
    if (schedule.ok && formik.errors[name]) formik.setFieldError(name, undefined);
  }, [schedule.ok, formik.errors[name]]);

  let line = null;
  if (!schedule.ok) {
    if (schedule.error.code !== 'empty' || asked) {
      line = <FormHelperText error sx={{ mt: 0 }}>{cronErrorText(schedule.error, t)}</FormHelperText>;
    }
  } else {
    const inZone = schedule.zone || zone;
    const run = nextCronRun(schedule, clockReading(new Date(), inZone));
    let next = '';
    if (run) {
      const when = formatRun(run, i18n.resolvedLanguage);
      if (schedule.zone) next = t('mgmt.cron.next.zone', 'Next run: {{when}} ({{zone}} time).', { when, zone: schedule.zone });
      else if (zone) next = t('mgmt.cron.next.server', 'Next run: {{when}} (server time).', { when });
      else next = t('mgmt.cron.next.device', 'Next run: {{when}}, if the server\'s clock shows the same time as this device.', { when });
    }
    line = <FormHelperText sx={{ mt: 0 }}>{describeCron(schedule, t)}. {next}</FormHelperText>;
  }

  return <Stack spacing={0.75}>
    {line}
    {!disabled && <>
      <Typography variant="caption" color="textSecondary">
        {t('mgmt.cron.presets.title', 'Common schedules (click to use one):')}
      </Typography>
      <Stack direction="row" useFlexGap flexWrap="wrap" spacing={1}>
        {CRON_PRESETS.map((preset) => {
          const picked = value.trim() === preset.expression;
          return <Chip
            key={preset.key}
            size="small"
            clickable
            color={picked ? 'primary' : 'default'}
            variant={picked ? 'filled' : 'outlined'}
            label={t(preset.key, preset.label)}
            title={preset.expression}
            onClick={() => formik.setFieldValue(name, preset.expression)}
            sx={{ maxWidth: '100%' }}
          />;
        })}
      </Stack>
    </>}
  </Stack>;
};

export default CronHelp;

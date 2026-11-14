// Fork addition: guidance in the alert editor (Monitoring > Alerts, upstream's
// EditAlertModal in AlertPage.jsx): ready-made alerts for a new one, the unit
// of the value, a sentence that reads the alert back, and the checks before
// saving (as Formik's `validate`). The logic is utils/alertForm.js.
import * as React from 'react';
import { Alert, Chip, FormHelperText, Stack, Typography } from '@mui/material';
import { useTranslation } from 'react-i18next';
import { templates, notAvailable, unitOf, readBack, alertProblems } from '../../utils/alertForm';

const tr = (t, text) => (text ? t(text[0], text[1], text[2] || {}) : '');

// Formik's `validate` for the editor: messages under the fields.
export const forkAlertValidate = (t, context) => (values) => {
  const problems = alertProblems(values, context);
  return Object.fromEntries(Object.entries(problems).map(([field, text]) => [field, tr(t, text)]));
};

export const ForkAlertTemplates = ({ formik, metrics }) => {
  const { t } = useTranslation();
  const list = templates(metrics);
  return <Stack spacing={1}>
    <Typography variant="subtitle1" component="div">{t('mgmt.monitoring.alerts.fork.start', 'Start from a ready-made alert')}</Typography>
    {list.length === 0
      ? <FormHelperText>{t('mgmt.monitoring.alerts.fork.noMetrics', 'The server has not collected any metric yet: ready-made alerts appear once it has (a few minutes after it starts, with monitoring on).')}</FormHelperText>
      : <Stack direction="row" useFlexGap flexWrap="wrap" spacing={1}>
        {list.map((tpl) => <Chip
          key={tpl.id}
          label={tr(t, tpl.label)}
          variant="outlined"
          clickable
          onClick={() => formik.setValues({ ...formik.values, ...tpl.values })}
          sx={{ maxWidth: '100%', height: 'auto', minHeight: 32, '& .MuiChip-label': { whiteSpace: 'normal', py: 0.5 } }}
        />)}
      </Stack>}
    {notAvailable.map((text) => <FormHelperText key={text[0]}>{tr(t, text)}</FormHelperText>)}
  </Stack>;
};

export const ForkAlertUnit = ({ formik }) => {
  const { t } = useTranslation();
  const { trackingMetric, conditionPercent } = formik.values;
  return <>
    {trackingMetric && <FormHelperText>{t('mgmt.monitoring.alerts.fork.unit', 'The value is in {{unit}}.', { unit: tr(t, unitOf(trackingMetric, conditionPercent)) })}</FormHelperText>}
    {formik.errors.conditionPercent && <FormHelperText error>{formik.errors.conditionPercent}</FormHelperText>}
  </>;
};

export const ForkAlertReadBack = ({ formik, metrics }) => {
  const { t } = useTranslation();
  const v = formik.values;
  if (!v.trackingMetric) return null;
  const r = readBack(v, metrics);
  const actions = r.actions.map((a) => tr(t, a));
  return <Stack spacing={1}>
    <Alert severity="info" sx={{ '& .MuiAlert-message': { minWidth: 0, overflowWrap: 'anywhere' } }}>
      {t('mgmt.monitoring.alerts.fork.sentence', '{{when}} {{op}} {{value}} {{unit}} {{period}}: {{actions}}. {{throttled}}', {
        when: tr(t, r.when),
        op: tr(t, r.op),
        value: r.value,
        unit: tr(t, r.unit),
        period: tr(t, r.period),
        actions: actions.length ? actions.join(', ') : t('mgmt.monitoring.alerts.fork.noAction', 'nothing happens (add an action below)'),
        throttled: tr(t, r.throttled),
        interpolation: { escapeValue: false },
      })}
    </Alert>
    {formik.errors.actions && <FormHelperText error>{formik.errors.actions}</FormHelperText>}
  </Stack>;
};

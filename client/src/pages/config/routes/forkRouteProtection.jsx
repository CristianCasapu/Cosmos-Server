// Fork addition: "Protection" on the Security tab of a URL: one choice of a
// few named levels (utils/routeProtection.js says what each writes and where
// its values come from), with upstream's numbers behind "Adjust the values
// myself" (they are this component's children, untouched). Values that match
// no level show as "Custom values" and stay as they are until a level is
// picked. Socket (TCP/UDP) URLs keep upstream's fields only: the per-minute
// limit and the filter do not apply to them.
import * as React from 'react';
import { Button, Collapse, FormHelperText, Grid, Stack } from '@mui/material';
import { DownOutlined, RightOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { CosmosFormDivider } from '../users/formShortcuts';
import { ChoiceRow } from '../../../components/formHelpers';
import { useIsPro } from '../../../utils/pro';
import { levelOf, applyLevel, levelText, summary, timeoutText, bandwidthText, RECOMMENDED } from '../../../utils/routeProtection';

const tr = (t, text) => (text ? t(text[0], text[1], text[2] || {}) : '');

const ForkRouteProtection = ({ formik, socket, children }) => {
  const { t } = useTranslation();
  const pro = useIsPro();
  const level = levelOf(formik.values);
  const [open, setOpen] = React.useState(level === 'custom');

  if (socket) {
    return children;
  }

  const options = [
    { value: 'strict', label: t('mgmt.urls.protection.strict', 'Strict') },
    { value: 'standard', label: t('mgmt.urls.protection.standard', 'Standard (recommended)') },
    { value: 'relaxed', label: t('mgmt.urls.protection.relaxed', 'Relaxed') },
    { value: 'off', label: t('mgmt.urls.protection.off', 'Off') },
  ];
  if (level === 'custom') {
    options.push({ value: 'custom', label: t('mgmt.urls.protection.custom', 'Custom values') });
  }
  const parts = summary(formik.values);

  return <>
    <CosmosFormDivider title={t('mgmt.urls.protection.title', 'Protection against abuse')} />
    <ChoiceRow
      name="fork-protection"
      label={t('mgmt.urls.protection.label', 'How much one visitor may do')}
      value={level}
      onChange={(value) => {
        if (value === 'custom') {
          setOpen(true);
          return;
        }
        formik.setValues(applyLevel(formik.values, value));
      }}
      options={options.map((option) => ({ ...option, hint: tr(t, levelText(option.value, pro)) }))}
      note={level !== RECOMMENDED && level !== 'custom'
        ? t('mgmt.urls.protection.notRecommended', 'Administrators signed in to Cosmos are never limited. "Standard" is what a new URL gets.')
        : t('mgmt.urls.protection.admins', 'Administrators signed in to Cosmos are never limited.')}
    >
      {parts && <FormHelperText sx={{ overflowWrap: 'anywhere' }}>
        {t('mgmt.urls.protection.holds', 'This URL holds: {{what}}.', { what: parts.map((p) => tr(t, p)).join(', ') })}
      </FormHelperText>}
      <FormHelperText>{tr(t, timeoutText(formik.values.Timeout))}</FormHelperText>
    </ChoiceRow>
    <Grid item xs={12}>
      <Button
        size="small"
        onClick={() => setOpen(!open)}
        startIcon={open ? <DownOutlined /> : <RightOutlined />}
        aria-expanded={open}
      >
        {t('mgmt.urls.protection.door', 'Adjust the values myself')}
      </Button>
    </Grid>
    <Grid item xs={12} sx={{ pt: open ? undefined : '0 !important' }}>
      <Collapse in={open} unmountOnExit={false}>
        <Stack spacing={1} sx={{ mb: 2 }}>
          <FormHelperText>
            {t('mgmt.urls.protection.units', 'A number left at 0 uses the server\'s default. Over one hour per visitor: the time budget is in milliseconds of request time, the byte limit in bytes. A visitor is blocked when over a limit times the strictness (strict 1, normal 2, lenient 3). The time limit is in milliseconds, the bandwidth in bytes per second, the request limit per minute and address.')}
          </FormHelperText>
          <FormHelperText>{tr(t, bandwidthText(formik.values.MaxBandwith))}</FormHelperText>
        </Stack>
        <Grid container spacing={2}>
          {children}
        </Grid>
      </Collapse>
    </Grid>
  </>;
};

export default ForkRouteProtection;

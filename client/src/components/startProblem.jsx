// Fork addition (plan 43): an app that does not start, in words, with the
// end of its log and, when Cosmos recognises the reason, the repair.
//
// - StartProblem: the report (from the creation's start check or from
//   GET /api/servapps/{id}/start-report), the variables it names as fields
//   (a generated value for a secret, shown once, with Copy) and "Apply and
//   start again", which the caller does: the creation is made again with
//   the same definition, an existing container is edited (POST .../update,
//   with the checks of every edit);
// - StartProblemButton: on a ServApps tile of an app that keeps stopping,
//   "Why?" opens the same in a dialog and applies the repair to the container.
//
// The log is text from the image: shown as text, never as HTML.
import * as React from 'react';
import { Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, Stack, TextField, Typography } from '@mui/material';
import { CopyOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';

import * as API from '../api';
import { browserRandom } from '../utils/anyImage';
import { REPAIR_ROUNDS, reasonWords, repairEnv, repairReady, repairValues } from '../utils/services';

const wrap = { overflowWrap: 'anywhere', minWidth: 0, '& .MuiAlert-message': { minWidth: 0 } };

const CopyButton = ({ value }) => {
  const { t } = useTranslation();
  const [copied, setCopied] = React.useState(false);
  return <Button size="small" variant="outlined" sx={{ flexShrink: 0 }} startIcon={<CopyOutlined />} data-copy
    onClick={() => {
      try {
        navigator.clipboard.writeText(value).then(() => setCopied(true), () => setCopied(false));
      } catch (e) { /* no clipboard: the value is on screen */ }
    }}>
    {copied ? t('mgmt.servapps.anyImage.ui.copied', 'Copied') : t('mgmt.servapps.anyImage.ui.copy', 'Copy')}
  </Button>;
};
export { CopyButton };

export const StartLog = ({ report }) => {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const lines = (report && report.log) || [];
  if (!lines.length) return null;
  return <Stack spacing={1} sx={{ minWidth: 0 }}>
    <Button size="small" sx={{ alignSelf: 'flex-start' }} onClick={() => setOpen(!open)} data-start-log-door>
      {open ? t('mgmt.services.start.hideLog', 'Hide its log') : t('mgmt.services.start.showLog', 'Its log (the last {{count}} lines)', { count: lines.length })}
    </Button>
    {open && <Box component="pre" data-start-log sx={{ m: 0, p: 1.5, maxHeight: 260, overflow: 'auto', fontSize: 12, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', bgcolor: 'action.hover', borderRadius: 1 }}>
      {lines.join('\n')}
    </Box>}
  </Stack>;
};

export const StartProblem = ({ report, onApply, busy, rounds = 0, applyLabel }) => {
  const { t } = useTranslation();
  const [values, setValues] = React.useState(() => repairValues(report && report.needs, browserRandom));
  React.useEffect(() => { setValues(repairValues(report && report.needs, browserRandom)); }, [report]);
  if (!report) return null;
  const words = reasonWords(report);
  const canRepair = values.length > 0 && rounds < REPAIR_ROUNDS && !!onApply;
  return <Stack spacing={1.5} sx={{ minWidth: 0 }} data-start-problem={report.name}>
    <Alert severity="error" sx={wrap} data-start-reason={report.reason}>
      {t(words.key, words.text, { ...words.params, interpolation: { escapeValue: false } })}
    </Alert>
    {canRepair && <Stack spacing={1.5} sx={{ minWidth: 0 }} data-start-repair>
      <Typography variant="body2">
        {t('mgmt.services.start.repairIntro', 'This image says it needs these settings. Cosmos proposes them; it then creates the container again with them.')}
      </Typography>
      {values.map((v) => <Stack key={v.name} direction="row" spacing={1} sx={{ alignItems: 'flex-start', minWidth: 0 }} data-repair-setting={v.name}>
        <TextField size="small" fullWidth label={v.name} value={v.value} disabled={busy}
          onChange={(e) => setValues((old) => old.map((o) => (o.name === v.name ? { ...o, value: e.target.value.slice(0, 4096) } : o)))}
          helperText={v.secret
            ? t('mgmt.servapps.anyImage.ui.secretHelp', 'Generated for you. Copy it now: it is not shown again after the creation.')
            : t('mgmt.services.start.valueHelp', 'Its value: the image\'s documentation says what it expects.')} />
        {v.secret && <CopyButton value={v.value} />}
      </Stack>)}
      <Button variant="contained" sx={{ alignSelf: 'flex-start' }} disabled={busy || !repairReady(values)} onClick={() => onApply(values)} data-start-apply>
        {applyLabel || t('mgmt.services.start.apply', 'Apply and start again')}
      </Button>
    </Stack>}
    {values.length > 0 && rounds >= REPAIR_ROUNDS && <Alert severity="warning" sx={wrap}>
      {t('mgmt.services.start.tooMany', 'The image keeps asking for more settings. Read its log and its documentation; the settings can be changed on the app\'s page.')}
    </Alert>}
    <StartLog report={report} />
  </Stack>;
};

// The tile's button and its dialog, for a container that exists.
export const StartProblemButton = ({ containerName, refresh }) => {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const [report, setReport] = React.useState(null);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState('');
  const [rounds, setRounds] = React.useState(0);
  const [fixed, setFixed] = React.useState(false);
  const [changed, setChanged] = React.useState(false);

  const read = () => API.docker.startReport(containerName).then((res) => setReport((res && res.data) || null)).catch((e) => setError(e.message || String(e)));
  React.useEffect(() => { if (open) { setReport(null); setError(''); setFixed(false); read(); } }, [open]);

  const apply = async (values) => {
    setBusy(true);
    setError('');
    try {
      const inspected = await API.docker.get(containerName);
      const env = (inspected && inspected.data && inspected.data.Config && inspected.data.Config.Env) || [];
      await API.docker.updateContainer(containerName, { envVars: repairEnv(env, values) });
      setRounds(rounds + 1);
      // the container is created again: look at it once it had time to start
      await new Promise((r) => setTimeout(r, 12000));
      const res = await API.docker.startReport(containerName);
      const next = (res && res.data) || null;
      setReport(next);
      setFixed(!!next && !next.failing);
      // the page is read again when the dialog closes: a tile that runs again has no "Why?", and its dialog would go with it
      setChanged(true);
    } catch (e) {
      setError(e.message || String(e));
    }
    setBusy(false);
  };

  const close = () => {
    setOpen(false);
    if (changed) { setChanged(false); refresh && refresh(); }
  };

  return <>
    <Button size="small" variant="outlined" color="error" onClick={() => setOpen(true)} data-start-why={containerName}>
      {t('mgmt.services.start.why', 'Why?')}
    </Button>
    {open && <Dialog open onClose={(e, reason) => { if (!busy && reason !== 'backdropClick') close(); }} fullWidth maxWidth="sm" data-start-dialog>
      <DialogTitle sx={{ overflowWrap: 'anywhere' }}>{t('mgmt.services.start.title', 'Why {{name}} does not run', { name: containerName })}</DialogTitle>
      <DialogContent>
        <Stack spacing={2} sx={{ minWidth: 0, pt: 1 }}>
          {!report && !error && <Typography variant="body2">{t('mgmt.services.start.reading', 'Reading its state and its log…')}</Typography>}
          {busy && <Alert severity="info" sx={wrap}>{t('mgmt.services.start.applying', 'Creating the container again with these settings, then looking whether it starts…')}</Alert>}
          {fixed && <Alert severity="success" sx={wrap} data-start-fixed>{t('mgmt.services.start.fixed', '{{name}} runs now.', { name: containerName })}</Alert>}
          {report && !fixed && (report.failing
            ? <StartProblem report={report} onApply={apply} busy={busy} rounds={rounds} />
            : <Alert severity="success" sx={wrap}>{t('mgmt.services.start.notFailing', '{{name}} does not seem to fail now.', { name: containerName })}</Alert>)}
          {error && <Alert severity="error" sx={wrap}>{error}</Alert>}
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={close} disabled={busy}>{t('global.close', 'Close')}</Button>
      </DialogActions>
    </Dialog>}
  </>;
};

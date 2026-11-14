// Fork addition: the format dialog, in place of upstream's FormatModal.jsx
// (same props, plus the disk). It names the disk and what is on it, says that
// everything is erased and what the steps are, proposes the file system, and
// asks for the disk's name to be typed and the password. The server checks the
// typed name, refuses a disk in use and reports a failed step as failed
// (src/storage/fork_format.go); utils/formatDisk.js holds the logic.
import * as React from 'react';
import { Alert, Button, Dialog, DialogActions, DialogContent, DialogTitle, Stack, Typography } from '@mui/material';
import { LoadingButton } from '@mui/lab';
import { useTranslation } from 'react-i18next';
import ChoiceChips from '../../components/choiceChips';
import { FormField, FormSelect } from '../../components/formField';
import { ProposalNote, keepOnBackdrop } from '../../components/formHelpers';
import { simplifyNumber } from '../dashboard/components/utils';
import { FILESYSTEMS, filesystem, diskSummary, blockedReason, proposedFilesystem, confirmMatches, plannedSteps } from '../../utils/formatDisk';

const tr = (t, text) => (text ? t(text[0], text[1], text[2] || {}) : '');

const ForkFormatDialog = ({ disk, cb, OnClose }) => {
  const { t } = useTranslation();
  const summary = React.useMemo(() => diskSummary(disk), [disk]);
  const proposal = React.useMemo(() => proposedFilesystem(summary), [summary]);
  const blocked = blockedReason(summary);
  const [fs, setFs] = React.useState(proposal.value);
  const [typed, setTyped] = React.useState('');
  const [password, setPassword] = React.useState('');
  const [error, setError] = React.useState('');
  const [busy, setBusy] = React.useState(false);
  const main = filesystem(fs) && filesystem(fs).main;
  const ready = !blocked && confirmMatches(typed, summary) && password !== '';

  const submit = () => {
    if (!ready || busy) return;
    setBusy(true);
    Promise.resolve(cb({ format: fs, password, confirm: typed.trim() })).then(() => {
      setBusy(false);
      OnClose();
    }).catch((err) => {
      setBusy(false);
      setError(err && err.message ? err.message : String(err));
    });
  };

  const what = summary.parts.length > 0
    ? summary.parts.map((p) => [p.name, p.fstype || t('mgmt.storage.fork.noFs', 'no file system'), p.size ? simplifyNumber(p.size, 'b') : ''].filter(Boolean).join(' · ')).join('; ')
    : (summary.fstype || t('mgmt.storage.fork.noFs', 'no file system'));

  return <Dialog open={true} onClose={keepOnBackdrop(() => OnClose())} maxWidth="sm" fullWidth>
    <DialogTitle>{t('mgmt.storage.fork.formatTitle', 'Erase and format {{name}}', { name: summary.name })}</DialogTitle>
    <DialogContent>
      <Stack spacing={2} sx={{ mt: 1, overflowWrap: 'anywhere' }}>
        <Stack spacing={0.5}>
          <Typography variant="subtitle1" component="div" sx={{ fontWeight: 600 }}>
            {[summary.path, summary.model, summary.size ? simplifyNumber(summary.size, 'b') : ''].filter(Boolean).join(' · ')}
          </Typography>
          <Typography variant="body2" component="div">
            {summary.whole
              ? t('mgmt.storage.fork.holdsDisk', 'A whole disk. On it now: {{what}}.', { what })
              : t('mgmt.storage.fork.holdsPart', 'A partition. On it now: {{what}}.', { what })}
          </Typography>
        </Stack>

        {blocked
          ? <Alert severity="error" sx={{ '& .MuiAlert-message': { minWidth: 0 } }}>{tr(t, blocked)}</Alert>
          : <Alert severity="warning" sx={{ '& .MuiAlert-message': { minWidth: 0 } }}>
            <div>{t('mgmt.storage.fork.erased', 'Everything on {{name}} is erased, for good. Copy what you need first.', { name: summary.name })}</div>
            <ul style={{ margin: '4px 0 0', paddingLeft: '20px' }}>
              {plannedSteps(summary, fs).map((step) => <li key={step[0]}>{tr(t, step)}</li>)}
            </ul>
          </Alert>}

        {!blocked && <>
          <Stack spacing={1}>
            <ChoiceChips
              id="fork-format-fs"
              label={t('mgmt.storage.fork.fsLabel', 'File system')}
              value={main ? fs : 'other'}
              options={[
                ...FILESYSTEMS.filter((f) => f.main).map((f) => ({ value: f.value, label: f.label, description: tr(t, f.hint) })),
                { value: 'other', label: t('mgmt.storage.fork.fsOther', 'Other'), description: tr(t, (filesystem(fs) || {}).hint) },
              ]}
              onChange={(value) => setFs(value === 'other' ? 'fat32' : value)}
            />
            {!main && <FormSelect
              id="fork-format-fs-other"
              value={fs}
              onChange={setFs}
              options={FILESYSTEMS.filter((f) => !f.main).map((f) => [f.value, f.label])}
            />}
            {fs === proposal.value && <ProposalNote>{tr(t, proposal.source)}</ProposalNote>}
          </Stack>

          <FormField
            id="fork-format-confirm"
            label={t('mgmt.storage.fork.confirmLabel', 'Type {{name}} to confirm', { name: summary.name })}
            value={typed}
            onChange={setTyped}
            autoComplete="off"
            inputProps={{ autoCapitalize: 'none', spellCheck: false }}
            error={typed.trim() && !confirmMatches(typed, summary) ? t('mgmt.storage.fork.confirmWrong', 'That is not {{name}}: check which disk you are about to erase.', { name: summary.name }) : ''}
            helperText={t('mgmt.storage.fork.confirmHelp', 'The name of the disk as shown above, so that the right one is erased.')}
          />
          <FormField
            id="fork-format-password"
            type="password"
            label={t('mgmt.storage.confirmPwd.confirmPwdLabel')}
            value={password}
            onChange={setPassword}
            onEnter={submit}
            autoComplete="current-password"
          />
        </>}
        {error && <Alert severity="error">{error}</Alert>}
      </Stack>
    </DialogContent>
    <DialogActions>
      <Button onClick={() => OnClose()}>{t('global.cancelAction')}</Button>
      {!blocked && <LoadingButton color="error" variant="contained" disabled={!ready} loading={busy} onClick={submit}>
        {t('mgmt.storage.fork.formatAction', 'Erase and format')}
      </LoadingButton>}
    </DialogActions>
  </Dialog>;
};

export default ForkFormatDialog;

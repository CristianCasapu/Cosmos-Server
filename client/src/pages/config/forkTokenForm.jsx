// Fork addition: the "New token" dialog, in place of upstream's CreateTokenDialog
// in configAPITokens.jsx (same props, plus the names taken). What the token may
// do as one choice (read only, the recommended one / full control / choose),
// each permission said in plain words, the ones that amount to full control
// named as such; an expiry proposed (90 days); where it may be used from,
// proposed from the address the admin is on. The logic is utils/tokenForm.js;
// the server checks the fields again (src/configapi/fork_api_tokens.go).
import * as React from 'react';
import { Alert, Button, Checkbox, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, FormHelperText, Stack, Typography } from '@mui/material';
import { Button as LoadingButton } from '@mui/material'; // Fork addition: MUI 9 has no LoadingButton (Button has the loading state since 6.4)
import * as API from '../../api';
import ChoiceChips from '../../components/choiceChips';
import { FormField, FormSelect } from '../../components/formField';
import { keepOnBackdrop } from '../../components/formHelpers';
import { PaidOnly } from '../../components/paidVisibility';
import { cleanList, invalidEntries, isAddressEntry } from '../../utils/httpSettings';
import { PERMISSIONS, presetOf, permissionsOf, toggle, fullControl, fullControlText, EXPIRY, PROPOSED_EXPIRY, placeChoices, placeOf, nameProblem } from '../../utils/tokenForm';

const tr = (t, text) => (text ? t(text[0], text[1], text[2] || {}) : '');

const ForkCreateTokenDialog = ({ open, onClose, onCreated, t, taken }) => {
  const empty = { name: '', description: '', permissions: permissionsOf('read'), preset: 'read', expiryDays: PROPOSED_EXPIRY, ips: '', place: null, restrictToConstellation: false };
  const [values, setValues] = React.useState(empty);
  const [check, setCheck] = React.useState(null);
  const [error, setError] = React.useState('');
  const [busy, setBusy] = React.useState(false);

  // the address the admin is on, as the server sees it: nothing probed, nothing saved
  React.useEffect(() => {
    if (!open) return;
    setValues(empty);
    setError('');
    let gone = false;
    API.config.httpCheck({ httpPort: '', httpsPort: '', adminWhitelist: [], constellationOnly: false })
      .then((res) => { if (!gone) setCheck(res.data); }).catch(() => {});
    return () => { gone = true; };
  }, [open]);

  const choices = placeChoices(check);
  // proposed until the admin picks: the local network, else this device
  const place = values.place || (choices[0] && choices[0].value) || 'anywhere';
  const placeChoice = choices.find((c) => c.value === place) || choices[choices.length - 1];
  const list = place === 'other' ? cleanList(values.ips) : placeChoice.list;
  const badIps = place === 'other' ? invalidEntries(values.ips, isAddressEntry) : [];
  const nameIssue = nameProblem(values.name, taken);
  const full = fullControl(values.permissions);
  const ready = values.name !== '' && !nameIssue && badIps.length === 0 && values.permissions.length > 0;
  const set = (patch) => setValues((old) => ({ ...old, ...patch }));

  const submit = async () => {
    if (!ready || busy) return;
    setBusy(true);
    setError('');
    try {
      const res = await API.apiTokens.create({
        name: values.name,
        description: values.description.trim(),
        permissions: values.permissions,
        ipWhitelist: list,
        restrictToConstellation: values.restrictToConstellation,
        expiryDays: Number(values.expiryDays) || 0,
      });
      setBusy(false);
      onCreated(res.data);
    } catch (err) {
      setBusy(false);
      setError(err && err.message ? err.message : String(err));
    }
  };

  return <Dialog open={open} onClose={keepOnBackdrop(onClose)} maxWidth="sm" fullWidth>
    <DialogTitle>{t('mgmt.config.apiTokens.createTitle')}</DialogTitle>
    <DialogContent>
      <Stack spacing={2.5} sx={{ mt: 1, overflowWrap: 'anywhere' }}>
        <FormField
          id="fork-token-name"
          label={t('mgmt.config.apiTokens.tokenName')}
          required
          value={values.name}
          onChange={(name) => set({ name })}
          autoComplete="off"
          error={tr(t, nameIssue)}
          helperText={t('mgmt.config.apiTokens.fork.nameHelp', 'What will use it, for example home-assistant. Letters, digits, - and _.')}
        />
        <FormField
          id="fork-token-description"
          label={t('global.description')}
          value={values.description}
          onChange={(description) => set({ description: description.slice(0, 256) })}
          helperText={t('mgmt.config.apiTokens.fork.descriptionHelp', 'Optional: a note for later, for example "dashboard on the kitchen tablet".')}
        />

        <Stack spacing={1}>
          <ChoiceChips
            id="fork-token-preset"
            label={t('mgmt.config.apiTokens.fork.mayLabel', 'What the token may do')}
            value={values.preset}
            options={[
              { value: 'read', label: t('mgmt.config.apiTokens.fork.presetRead', 'Read only (recommended)'), description: t('mgmt.config.apiTokens.fork.presetReadHint', 'It can look at everything (logs, users, apps, settings with secrets masked) and change nothing. Enough for a dashboard or a monitor.') },
              { value: 'full', label: t('mgmt.config.apiTokens.fork.presetFull', 'Full control'), description: t('mgmt.config.apiTokens.fork.presetFullHint', 'Everything an administrator can do, secrets included. Anyone who gets hold of it owns the server.') },
              { value: 'custom', label: t('mgmt.config.apiTokens.fork.presetCustom', 'Choose'), description: t('mgmt.config.apiTokens.fork.presetCustomHint', 'Tick what it needs, nothing more.') },
            ]}
            onChange={(preset) => set({ preset, permissions: permissionsOf(preset, values.permissions) })}
          />
          {values.preset === 'custom' && <Stack spacing={0.5}>
            {PERMISSIONS.map((p) => <Stack key={p.id}>
              <FormControlLabel
                control={<Checkbox checked={values.permissions.includes(p.id)} onChange={() => set({ permissions: toggle(values.permissions, p.id) })} />}
                label={<span>{tr(t, p.label)}{p.full ? ' — ' + t('mgmt.config.apiTokens.fork.fullMark', 'full control') : ''}</span>}
              />
              <FormHelperText sx={{ mt: -1, ml: 4 }}>{tr(t, p.what)}</FormHelperText>
            </Stack>)}
          </Stack>}
          {full.length > 0 && values.preset !== 'full' && <Alert severity="warning" sx={{ '& .MuiAlert-message': { minWidth: 0 } }}>
            {full.map((p) => <div key={p.id}>{t(fullControlText(p)[0], fullControlText(p)[1], { name: tr(t, p.label) })}</div>)}
          </Alert>}
          {values.preset === 'custom' && values.permissions.length === 0 && <FormHelperText error>{t('mgmt.config.apiTokens.fork.noneTicked', 'Tick at least one thing the token may do.')}</FormHelperText>}
          {values.preset === 'custom' && presetOf(values.permissions) === 'read' && <FormHelperText>{t('mgmt.config.apiTokens.fork.isRead', 'This is the same as "Read only".')}</FormHelperText>}
        </Stack>

        <Stack spacing={1}>
          <ChoiceChips
            id="fork-token-place"
            label={t('mgmt.config.apiTokens.fork.placeLabel', 'Where it may be used from')}
            value={place}
            options={choices.map((c) => ({ value: c.value, label: tr(t, c.label) }))}
            onChange={(value) => set({ place: value, ips: value === 'other' && !values.ips ? list.join(', ') : values.ips })}
            helperText={place === 'anywhere'
              ? t('mgmt.config.apiTokens.fork.placeAnywhereHint', 'From any address that can reach the server. Limit it when you know where the program that uses it runs.')
              : t('mgmt.config.apiTokens.fork.placeHint', 'A request with the token from any other address is refused.')}
          />
          {!values.place && choices[0] && choices[0].value !== 'anywhere' && <FormHelperText>{t('mgmt.config.apiTokens.fork.placeProposed', 'Proposed from the address you are connected from.')}</FormHelperText>}
          {place === 'other' && <FormField
            id="fork-token-ips"
            value={values.ips}
            onChange={(ips) => set({ ips })}
            placeholder="192.168.1.0/24, 10.0.0.5"
            error={badIps.length ? t('mgmt.config.apiTokens.fork.ipsInvalid', 'Not an address or a range: {{entries}}', { entries: badIps.join(', ') }) : ''}
            helperText={t('mgmt.config.apiTokens.fork.ipsHelp', 'Addresses or ranges, separated by commas, for example 192.168.1.0/24, 10.0.0.5. Empty: anywhere.')}
          />}
        </Stack>

        <PaidOnly keep={false}>{/* needs Constellation, hence a licence */}
          <FormControlLabel
            control={<Checkbox checked={values.restrictToConstellation} onChange={(e) => set({ restrictToConstellation: e.target.checked })} />}
            label={t('mgmt.config.apiTokens.restrictToConstellation')}
          />
        </PaidOnly>

        <FormSelect
          id="fork-token-expiry"
          label={t('mgmt.config.apiTokens.expiry')}
          value={String(values.expiryDays)}
          onChange={(value) => set({ expiryDays: Number(value) })}
          options={EXPIRY.map((days) => [String(days), days === 0
            ? t('mgmt.config.apiTokens.fork.expiryNever', 'Never (not recommended)')
            : t('mgmt.config.apiTokens.fork.expiryDays', '{{n}} days', { n: days })])}
          helperText={Number(values.expiryDays) === 0
            ? t('mgmt.config.apiTokens.fork.expiryNeverHint', 'A token that never ends works for whoever finds it, years later. Prefer an end date and make a new token then.')
            : t('mgmt.config.apiTokens.fork.expiryHint', 'After that it stops working; make a new one then. 90 days is proposed.')}
        />

        <Typography variant="body2" component="div">{t('mgmt.config.apiTokens.fork.usage', 'The token is shown once, after Create. A program sends it in the header "Authorization: Bearer <token>".')}</Typography>
        {error && <Alert severity="error">{error}</Alert>}
      </Stack>
    </DialogContent>
    <DialogActions>
      <Button onClick={onClose}>{t('global.cancelAction')}</Button>
      <LoadingButton color="primary" variant="contained" disabled={!ready} loading={busy} onClick={submit}>
        {t('global.createAction')}
      </LoadingButton>
    </DialogActions>
  </Dialog>;
};

export default ForkCreateTokenDialog;

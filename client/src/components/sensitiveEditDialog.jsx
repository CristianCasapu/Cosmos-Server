// Fork addition (plan 40, security review F3): asks, tick by tick, for the
// access to the server an edit gives a container (utils/sensitiveEdit.js).
// Mounted once, beside the password prompt.
import * as React from 'react';
import { Button, Checkbox, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, Stack, Typography } from '@mui/material';
import { useTranslation } from 'react-i18next';
import { answerSensitive, getSensitiveEdit, subscribeSensitiveEdit } from '../utils/sensitiveEdit';
import { sentence } from '../utils/sensitiveAccess';

const idOf = (item) => `${item.service}|${item.kind}|${item.value}`;

const SensitiveEditDialog = () => {
  const { t } = useTranslation();
  const request = React.useSyncExternalStore(subscribeSensitiveEdit, getSensitiveEdit);
  const [ticked, setTicked] = React.useState([]);
  React.useEffect(() => { setTicked([]); }, [request]);
  if (!request) return null;
  const all = request.items.map(idOf);
  const say = (s) => t(s.key, s.text, { ...s.params, interpolation: { escapeValue: false } });
  return <Dialog open onClose={(e, reason) => { if (reason !== 'backdropClick') answerSensitive(null); }} maxWidth="sm" fullWidth data-sensitive-edit>
    <DialogTitle>{t('mgmt.servapps.sensitiveEdit.title', 'This change needs your yes')}</DialogTitle>
    <DialogContent>
      <Stack spacing={1}>
        <Typography variant="body2">{t('mgmt.servapps.sensitiveEdit.intro', 'The change gives the container access to the server that it did not have and that Cosmos never gives without asking. Tick each one you accept; without every tick nothing is changed.')}</Typography>
        {request.items.map((item) => <FormControlLabel key={idOf(item)} sx={{ alignItems: 'flex-start' }}
          control={<Checkbox size="small" checked={ticked.includes(idOf(item))}
            onChange={(e) => setTicked(e.target.checked ? [...ticked, idOf(item)] : ticked.filter((id) => id !== idOf(item)))} />}
          label={<Typography variant="body2" sx={{ pt: 1, overflowWrap: 'anywhere' }}>{say(sentence(item))}</Typography>} />)}
      </Stack>
    </DialogContent>
    <DialogActions>
      <Button onClick={() => answerSensitive(null)}>{t('mgmt.servapps.sensitiveEdit.cancel', 'Do not change it')}</Button>
      <Button variant="contained" disabled={!all.every((id) => ticked.includes(id))} onClick={() => answerSensitive(ticked)} data-sensitive-edit-accept>
        {t('mgmt.servapps.sensitiveEdit.accept', 'Accept and save')}
      </Button>
    </DialogActions>
  </Dialog>;
};

export default SensitiveEditDialog;

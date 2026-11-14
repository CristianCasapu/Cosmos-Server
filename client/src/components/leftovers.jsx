// Fork addition: a notice at the top of the ServApps list for each container
// left behind by an interrupted change (src/docker/fork_leftovers.go): an
// edit sets the old container aside, stopped and renamed
// "<app>-cosmos-old-<id>", until its replacement runs; when Cosmos stopped in
// between, it stays. Each notice offers to put it back or to remove it.
// Nothing is done without a click, and removing asks first. The words are in
// utils/leftovers.js.
import * as React from 'react';
import {
  Alert,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  Stack,
  Tooltip,
  Typography,
} from '@mui/material';
import { LoadingButton } from '@mui/lab';
import { DeleteOutlined, RollbackOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import PermissionGuard from './permissionGuard';
import { PERM_RESOURCES } from '../utils/permissions';
import {
  answerText, appNow, failureText, removeBlocked, removeQuestion, restoreHint, sortLeftovers, whatHappened,
} from '../utils/leftovers';

const when = (value) => {
  if (!value) return '';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleString();
};

const wrapStyle = { overflowWrap: 'anywhere', wordBreak: 'break-word' };

// `servApps` is the page's list: the leftovers are asked for again whenever it
// is refreshed. `refresh` refreshes the page after an action; `wrap` puts each
// notice into the page's grid.
const Leftovers = ({ servApps, refresh, wrap }) => {
  const { t } = useTranslation();
  const [list, setList] = React.useState([]);
  const [busy, setBusy] = React.useState(''); // the name being acted on
  const [results, setResults] = React.useState([]); // [{ id, name, severity, text }], the latest first
  const [asking, setAsking] = React.useState(null); // the leftover whose removal is asked

  const tx = (descriptor) => (descriptor ? t(descriptor[0], descriptor[1], descriptor[2]).trim() : '');

  const load = React.useCallback(() => {
    if (!API.docker.leftovers) return;
    API.docker.leftovers().then((answer) => {
      setList(sortLeftovers(answer && answer.data));
    }).catch(() => {
      // the page says itself when Docker does not answer
    });
  }, []);

  React.useEffect(() => { load(); }, [load, servApps]);

  const act = (action, entry) => {
    setBusy(entry.name);
    const said = (severity, text) => setResults((previous) => [
      { id: Date.now() + Math.random(), name: entry.name, severity, text },
      ...previous.filter((result) => result.name !== entry.name),
    ].slice(0, 5));
    API.docker.leftoverAction(action, entry.name).then((answer) => {
      said('success', tx(answerText(answer)));
    }).catch((error) => {
      said('error', tx(failureText(error && error.code, error && error.message)));
    }).finally(() => {
      setBusy('');
      load();
      refresh && refresh();
    });
  };

  const notices = list.map((entry) => {
    const said = results.find((result) => result.name === entry.name && result.severity === 'error');
    const blocked = removeBlocked(entry);
    const removeButton = <Button
      color="inherit"
      size="small"
      variant="outlined"
      startIcon={<DeleteOutlined />}
      disabled={Boolean(busy) || Boolean(blocked)}
      onClick={() => setAsking(entry)}
    >{t('mgmt.servapps.leftovers.remove', 'Remove the leftover')}</Button>;

    return <Alert key={entry.name} severity="warning" data-leftover={entry.name}
      sx={{ textAlign: 'left', '& .MuiAlert-message': { ...wrapStyle, minWidth: 0, width: '100%' } }}>
      <Stack spacing={1}>
        <Typography variant="body2" sx={wrapStyle}>{tx(whatHappened(entry, when(entry.finished)))}</Typography>
        <Typography variant="body2" sx={wrapStyle}>{tx(appNow(entry))}</Typography>
        <PermissionGuard permission={PERM_RESOURCES}>
          <Typography variant="caption" sx={wrapStyle}>{tx(restoreHint(entry))}</Typography>
          <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} alignItems={{ xs: 'stretch', sm: 'center' }}>
            <LoadingButton
              color="inherit"
              size="small"
              variant="outlined"
              startIcon={<RollbackOutlined />}
              loading={busy === entry.name}
              loadingPosition="start"
              disabled={Boolean(busy) && busy !== entry.name}
              onClick={() => act('restore', entry)}
            >{t('mgmt.servapps.leftovers.restore', 'Put the previous one back')}</LoadingButton>
            {blocked
              ? <Tooltip title={tx(blocked)}><span style={{ display: 'flex' }}>{React.cloneElement(removeButton, { sx: { flexGrow: 1 } })}</span></Tooltip>
              : removeButton}
          </Stack>
        </PermissionGuard>
        {said && <Typography variant="body2" color={said.severity === 'error' ? 'error' : undefined} sx={wrapStyle}>{said.text}</Typography>}
      </Stack>
    </Alert>;
  });

  // what an action gave, on its own once the leftover is no longer listed (a failure on a listed one is on its notice)
  const listed = new Set(list.map((entry) => entry.name));
  const finished = results.filter((result) => result.severity === 'success' || !listed.has(result.name))
    .map((result) => <Alert key={'result-' + result.id} severity={result.severity}
      onClose={() => setResults((previous) => previous.filter((other) => other.id !== result.id))}
      sx={{ textAlign: 'left', '& .MuiAlert-message': { ...wrapStyle, minWidth: 0 } }}>{result.text}</Alert>);

  if (notices.length === 0 && finished.length === 0 && !asking) {
    return null;
  }

  return <>
    {[...finished, ...notices].map((notice) => <React.Fragment key={notice.key}>{wrap ? wrap(notice) : notice}</React.Fragment>)}
    <Dialog open={Boolean(asking)} onClose={() => setAsking(null)}>
      <DialogTitle>{t('mgmt.servapps.leftovers.removeTitle', 'Remove the leftover?')}</DialogTitle>
      <DialogContent>
        <DialogContentText sx={wrapStyle}>{asking ? tx(removeQuestion(asking)) : ''}</DialogContentText>
      </DialogContent>
      <DialogActions>
        <Button onClick={() => setAsking(null)}>{t('global.cancelAction')}</Button>
        <Button color="error" variant="contained" startIcon={<DeleteOutlined />} onClick={() => {
          const entry = asking;
          setAsking(null);
          act('remove', entry);
        }}>{t('mgmt.servapps.leftovers.removeYes', 'Remove it')}</Button>
      </DialogActions>
    </Dialog>
  </>;
};

export default Leftovers;

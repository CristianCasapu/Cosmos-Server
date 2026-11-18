// Fork addition (plan 51): the copy of the monitoring history between the
// local file and PostgreSQL, inside the Monitoring database card
// (configExternal.jsx). Nothing is copied without a click; the progress is
// asked every 2 s while a copy runs (it survives a reload: the server keeps
// it), and stops being asked when the card goes away. Logic and texts:
// utils/monitoringCopy.js; server: src/monitorcopy.
import * as React from 'react';
import { Alert, Box, Button, LinearProgress, Stack, Typography } from '@mui/material';
import { useTranslation } from 'react-i18next';
import * as API from '../../api';
import {
  CATCH_UP, TO_LOCAL, TO_POSTGRES, catchUpOf, countedAgo, countingOf, formatCount, formatDuration, formatTime, mismatchLine, offerLabel, offerOf, pollDelay, progressOf, resultOf, tableLabel,
} from '../../utils/monitoringCopy';

const touch = { minHeight: 44, textAlign: 'left', whiteSpace: 'normal', overflowWrap: 'anywhere' };

// saves: the form's submit count; after a save the store in use may change
// a moment later (the switch runs in the background): the card asks again.
const MonitoringCopy = ({ saves }) => {
  const { t, i18n } = useTranslation();
  const locale = i18n && i18n.language;
  const [view, setView] = React.useState(null);
  const [busy, setBusy] = React.useState('');
  const [problem, setProblem] = React.useState('');
  const timer = React.useRef(null);
  const live = React.useRef(true);
  const available = typeof (API.config && API.config.monitoringCopyStatus) === 'function';

  const load = React.useCallback(() => {
    if (!available) return Promise.resolve();
    return API.config.monitoringCopyStatus()
      .then((res) => {
        if (!live.current) return;
        const data = (res && res.data) || null;
        setView(data);
        clearTimeout(timer.current);
        timer.current = setTimeout(load, pollDelay(data));
      })
      .catch(() => {
        if (!live.current) return;
        clearTimeout(timer.current);
        timer.current = setTimeout(load, pollDelay(null));
      });
  }, [available]);

  React.useEffect(() => {
    live.current = true;
    load();
    return () => {
      live.current = false;
      clearTimeout(timer.current);
    };
  }, [load]);

  // after a save of the database settings: now, and when the switch is done
  React.useEffect(() => {
    const later = [setTimeout(load, 2000), setTimeout(load, 6000)];
    return () => later.forEach(clearTimeout);
  }, [saves, load]);

  const start = (direction) => {
    setBusy(direction);
    setProblem('');
    API.config.monitoringCopyStart(direction)
      .then((res) => { if (live.current && res && res.data) setView(res.data); })
      .catch((e) => { if (live.current) setProblem((e && e.message) || String(e)); })
      .finally(() => { if (live.current) { setBusy(''); load(); } });
  };
  const cancel = () => {
    setBusy('cancel');
    API.config.monitoringCopyCancel()
      .then((res) => { if (live.current && res && res.data) setView(res.data); })
      .catch((e) => { if (live.current) setProblem((e && e.message) || String(e)); })
      .finally(() => { if (live.current) { setBusy(''); load(); } });
  };

  if (!view) return null;
  const progress = progressOf(view);
  const forward = offerOf(view, TO_POSTGRES);
  const reverse = offerOf(view, TO_LOCAL);
  const result = resultOf(view, t, locale);
  const counting = countingOf(view);
  const caught = catchUpOf(view, t, locale);
  if (!progress && !forward && !reverse && !result && !problem && !counting && !caught) return null;

  return <Stack spacing={1.5} data-fork-copy>
    {progress && <Box data-fork-copy-progress={progress.phase}>
      <Stack spacing={1}>
        <Typography variant="body2">
          {progress.phase === 'verifying'
            ? t('mgmt.config.general.monitoringDb.copy.verifying', 'Comparing the two stores…')
            : progress.direction === CATCH_UP
              ? t('mgmt.config.general.monitoringDb.copy.runningCatchUp', 'Catching up the records written into the local file while the database was away (from {{from}} to {{to}})… Monitoring keeps recording meanwhile.', { from: formatTime(progress.from, locale), to: formatTime(progress.to, locale) })
            : progress.direction === TO_LOCAL
              ? t('mgmt.config.general.monitoringDb.copy.runningToLocal', 'Copying the records of PostgreSQL into the local file… Monitoring keeps recording meanwhile.')
              : t('mgmt.config.general.monitoringDb.copy.runningToPostgres', 'Copying the history from the local file into PostgreSQL… Monitoring keeps recording meanwhile; the local file is only read.')}
        </Typography>
        <LinearProgress variant="determinate" value={progress.percent} aria-label={t('mgmt.config.general.monitoringDb.copy.progressLabel', 'Progress of the copy')} />
        <Typography variant="caption" sx={{ color: 'text.secondary' }}>
          {t('mgmt.config.general.monitoringDb.copy.overall', '{{done}} of {{total}} records', { done: formatCount(progress.done), total: formatCount(progress.total) })}
          {progress.secondsLeft >= 0 && progress.phase === 'copying' ? ' · ' + formatDuration(progress.secondsLeft, t) : ''}
        </Typography>
        <Stack spacing={0.5}>
          {progress.tables.map((row) => <Box key={row.table} data-fork-copy-table={row.table} data-fork-copy-state={row.state}>
            <Stack direction="row" spacing={1} useFlexGap sx={{ justifyContent: 'space-between', flexWrap: 'wrap' }}>
              <Typography variant="caption">{tableLabel(row.table, t)}</Typography>
              <Typography variant="caption" sx={{ color: 'text.secondary' }}>
                {t('mgmt.config.general.monitoringDb.copy.tableDone', '{{done}} of {{total}}', { done: formatCount(row.done), total: formatCount(row.total) })}
              </Typography>
            </Stack>
            <LinearProgress variant="determinate" value={row.percent} sx={{ height: 3 }} />
          </Box>)}
        </Stack>
        <Box>
          <Button variant="outlined" color="warning" sx={touch} loading={busy === 'cancel'} disabled={progress.cancelling} onClick={cancel} data-fork-copy-cancel>
            {progress.cancelling
              ? t('mgmt.config.general.monitoringDb.copy.cancelling', 'Stopping after the current batch…')
              : t('mgmt.config.general.monitoringDb.copy.cancel', 'Cancel the copy')}
          </Button>
        </Box>
      </Stack>
    </Box>}

    {result && !progress && <Alert severity={result.severity} sx={{ overflowWrap: 'anywhere' }} data-fork-copy-result={result.outcome}>
      {result.text}
      {result.note && <Box sx={{ mt: 0.5 }} data-fork-copy-replaced>{result.note}</Box>}
      {result.tables.length > 0 && <Box component="ul" sx={{ m: 0, mt: 0.5, pl: 2.5 }}>
        {result.tables.map((row) => <li key={row.table} data-fork-copy-mismatch={row.table}>{mismatchLine(row, t)}</li>)}
      </Box>}
    </Alert>}

    {counting && !progress && <Typography variant="caption" sx={{ color: 'text.secondary' }} data-fork-copy-counting>
      {t('mgmt.config.general.monitoringDb.copy.counting', 'Counting the records of the two stores…')}
    </Typography>}

    {caught && <Alert severity={caught.severity} sx={{ overflowWrap: 'anywhere' }} data-fork-copy-catchup={caught.outcome}>
      {caught.text}
    </Alert>}

    {problem && <Alert severity="error" onClose={() => setProblem('')} sx={{ overflowWrap: 'anywhere' }} data-fork-copy-problem>
      {t('mgmt.config.general.monitoringDb.copy.startFailed', 'The copy could not start:')} {problem}
    </Alert>}

    {forward && <Alert severity="info" icon={false} sx={{ overflowWrap: 'anywhere' }} data-fork-copy-offer={TO_POSTGRES}>
      <Stack spacing={1}>
        <Typography variant="body2">
          {forward.lastCopy > 0
            ? t('mgmt.config.general.monitoringDb.copy.offerLocalAgain', 'The history of the local file was copied into PostgreSQL on {{time}}. Copying again only adds what is missing.', { time: formatTime(forward.lastCopy, locale) })
            : t('mgmt.config.general.monitoringDb.copy.offerLocalText', 'PostgreSQL is in use; the records made before the switch are still in the local file (database.db). Copy them so that charts and lists show the whole history.')}
        </Typography>
        <Box>
          <Button variant={forward.lastCopy > 0 ? 'outlined' : 'contained'} sx={touch} loading={busy === TO_POSTGRES} disabled={!!busy} onClick={() => start(TO_POSTGRES)} data-fork-copy-start={TO_POSTGRES}>
            {offerLabel(forward, t, locale)}
          </Button>
        </Box>
        {countedAgo(forward, t) && <Typography variant="caption" sx={{ color: 'text.secondary' }} data-fork-copy-counted>{countedAgo(forward, t)}</Typography>}
        <Typography variant="caption" sx={{ color: 'text.secondary' }}>
          {t('mgmt.config.general.monitoringDb.copy.safety', 'The local file is only read: its records are not changed and it is not deleted (SQLite may leave its two small companion files, database.db-wal and database.db-shm, next to it). Monitoring keeps recording during the copy; it can be cancelled and resumed.')}
        </Typography>
      </Stack>
    </Alert>}

    {reverse && <Stack spacing={0.5} data-fork-copy-offer={TO_LOCAL}>
      <Box>
        <Button variant="outlined" sx={touch} loading={busy === TO_LOCAL} disabled={!!busy} onClick={() => start(TO_LOCAL)} data-fork-copy-start={TO_LOCAL}>
          {offerLabel(reverse, t, locale)}
        </Button>
      </Box>
      <Typography variant="caption" sx={{ color: 'text.secondary' }}>
        {reverse.lastCopy > 0
          ? t('mgmt.config.general.monitoringDb.copy.reverseAgain', 'Last copied into the local file on {{time}}. So that switching back to the local file later shows these records too. Possible only while PostgreSQL is in use.', { time: formatTime(reverse.lastCopy, locale) })
          : t('mgmt.config.general.monitoringDb.copy.reverseText', 'So that switching back to the local file later shows these records too. Possible only while PostgreSQL is in use.')}
      </Typography>
    </Stack>}
  </Stack>;
};

export default MonitoringCopy;

// Fork addition: the image pull of a creation as a step of its own, read from the server's
// "[PULL PROGRESS]" lines (utils/pullProgress.js): the bytes and layers when the registry says them,
// otherwise the time it has been waiting; on failure the reason in words and "Try again".
import * as React from 'react';
import { Alert, Button, LinearProgress, Stack, Typography } from '@mui/material';
import { ReloadOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { PULL_MARKER, feedPulls, pullPercent, pullReasonWords, pullWords, withoutPullLines } from '../utils/pullProgress';

// The pulls of the running creation: take() reads a line of the answer and gives back what the log shows.
export const usePullProgress = () => {
  const [pulls, setPulls] = React.useState([]);
  const take = React.useCallback((line) => {
    if (!String(line || '').includes(PULL_MARKER)) return line;
    setPulls((old) => feedPulls(old, line));
    return withoutPullLines(line);
  }, []);
  const reset = React.useCallback(() => setPulls([]), []);
  return { pulls, take, reset };
};

const severity = { pulling: 'info', done: 'success', failed: 'error' };

const ForkPullProgress = ({ pulls, onRetry }) => {
  const { t } = useTranslation();
  if (!pulls || !pulls.length) return null;
  return <Stack spacing={1} data-pull-progress>
    {pulls.map((p) => {
      const words = pullWords(p);
      const percent = pullPercent(p);
      const reason = p.state === 'failed' ? pullReasonWords(p.reason) : null;
      return <Alert key={p.image} severity={severity[p.state]} data-pull-state={p.state} data-pull-reason={p.reason || undefined}
        sx={{ overflowWrap: 'anywhere', '& .MuiAlert-message': { width: '100%', minWidth: 0 } }}
        aria-live={p.state === 'pulling' ? 'off' : 'polite'}>
        <Stack spacing={1}>
          <span>{t(words.key, words.text, { ...words.params, interpolation: { escapeValue: false } })}</span>
          {p.state === 'pulling' && <LinearProgress variant={percent === null ? 'indeterminate' : 'determinate'} value={percent === null ? undefined : percent}
            aria-label={t('mgmt.servapps.create.pull.progress', 'Download of the image')} />}
          {reason && <span data-pull-reason-words>{t(reason.key, reason.text)}</span>}
          {reason && p.detail && <Typography variant="caption" component="div" sx={{ fontFamily: 'monospace', opacity: 0.85 }} data-pull-detail>
            {t('mgmt.servapps.create.pull.detail', 'Docker said: {{detail}}', { detail: p.detail, interpolation: { escapeValue: false } })}
          </Typography>}
          {reason && onRetry && <Button variant="outlined" color="error" startIcon={<ReloadOutlined />} onClick={onRetry}
            sx={{ alignSelf: 'flex-start', minHeight: 44 }} data-pull-retry>
            {t('mgmt.servapps.create.pull.retry', 'Try again')}
          </Button>}
        </Stack>
      </Alert>;
    })}
  </Stack>;
};

export default ForkPullProgress;

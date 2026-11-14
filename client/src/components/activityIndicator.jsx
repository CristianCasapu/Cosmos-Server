// Fork addition: feedback for commands. A message at the top of the page with
// the outcome of every command sent to the server, and, for one that takes
// longer than a second, a floating line at the bottom saying what is running,
// for how long, how many calls are done out of how many, and the step in
// progress.
// The data comes from utils/activity.js.
import * as React from 'react';
import { Alert, Box, CircularProgress, Paper, Stack, Typography } from '@mui/material';
import { useTranslation } from 'react-i18next';
import { dismissToast, getActivity, pruneActivity, subscribeActivity } from '../utils/activity';

const SHOW_AFTER = 1000;

const elapsed = (ms, t) => {
  const seconds = Math.max(0, Math.floor(ms / 1000));
  if (seconds < 60) {
    return t('activity.seconds', '{{seconds}} s', { seconds });
  }
  return t('activity.minutes', '{{minutes}} min {{seconds}} s', { minutes: Math.floor(seconds / 60), seconds: seconds % 60 });
};

const ActivityIndicator = () => {
  const { t } = useTranslation();
  const [, refresh] = React.useReducer((n) => n + 1, 0);

  React.useEffect(() => subscribeActivity(refresh), []);

  const activity = getActivity();
  const active = activity.calls.length > 0 || activity.toasts.length > 0 || activity.total > 0;

  // While something is shown, tick: the elapsed time moves and messages expire.
  React.useEffect(() => {
    if (!active) return undefined;
    const timer = setInterval(() => {
      pruneActivity(Date.now());
      refresh();
    }, 500);
    return () => clearInterval(timer);
  }, [active]);

  const now = Date.now();
  const oldest = activity.calls[0];
  const current = activity.calls[activity.calls.length - 1];
  const showRunning = oldest && now - oldest.startedAt >= SHOW_AFTER;

  if (!showRunning && activity.toasts.length === 0) {
    return null;
  }

  const layer = {
    position: 'fixed',
    left: 0,
    right: 0,
    px: 2,
    display: 'flex',
    justifyContent: 'center',
    pointerEvents: 'none',
    zIndex: (theme) => theme.zIndex.snackbar,
  };
  const column = { width: '100%', maxWidth: 560, pointerEvents: 'auto' };

  return <>
    {/* outcomes at the top, where they do not cover the buttons of a dialog */}
    {/* clear of a phone's notch in the installed app (the skin's --ct-safe-top, 0 elsewhere) */}
    {activity.toasts.length > 0 && <Box sx={{ ...layer, top: { xs: 'calc(12px + var(--ct-safe-top, 0px))', sm: 'calc(20px + var(--ct-safe-top, 0px))' } }}>
      <Stack spacing={1} sx={column} role="status" aria-live="polite">
        {activity.toasts.map((toast) => (
          // the Paper gives the tinted Alert an opaque surface to sit on
          <Paper key={toast.id} elevation={6} sx={{ borderRadius: 2, overflow: 'hidden' }}>
            <Alert severity={toast.severity} onClose={() => dismissToast(toast.id)}
              sx={{ alignItems: 'center', overflowWrap: 'anywhere' }}>
              {toast.text}
            </Alert>
          </Paper>
        ))}
      </Stack>
    </Box>}

    {/* what is running, at the bottom; on phones clear of the light/dark switch */}
    {showRunning && <Box sx={{ ...layer, bottom: { xs: 'calc(68px + var(--ct-safe-bottom, 0px))', sm: 'calc(20px + var(--ct-safe-bottom, 0px))' } }}>
      <Paper elevation={6} role="status" aria-live="polite"
        sx={{ ...column, px: 2, py: 1.25, display: 'flex', alignItems: 'center', gap: 1.5, borderRadius: 2 }}>
        <CircularProgress size={20} thickness={5} sx={{ flexShrink: 0 }} />
        <Box sx={{ minWidth: 0, flexGrow: 1 }}>
          <Typography variant="body2" sx={{ fontWeight: 600, overflowWrap: 'anywhere' }}>
            {current.label}
            {activity.total > 1 ? ' · ' + t('activity.count', '{{done}}/{{total}}', { done: activity.done, total: activity.total }) : ''}
          </Typography>
          <Typography variant="caption" color="textSecondary" noWrap component="div">
            {elapsed(now - oldest.startedAt, t)}
            {current.detail ? ' · ' + current.detail : ''}
          </Typography>
        </Box>
      </Paper>
    </Box>}
  </>;
};

export default ActivityIndicator;

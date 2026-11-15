// Fork addition: "Watch from the internet" (Configuration > HTTP, after the
// internet access card; server side src/reachwatch, words in
// utils/reachWatch.js), and the line of the home page while the server
// cannot be reached from outside.
//
// The answer is fetched once for the page and shared; while a test runs it
// is asked again every few seconds until the round is over.
import * as React from 'react';
import { Alert, FormControlLabel, Link, Stack, Switch, Typography } from '@mui/material';
import { LoadingButton } from '@mui/lab';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import MainCard from './MainCard';
import ChoiceChips from './choiceChips';
import PermissionGuard from './permissionGuard';
import StatusDot from './statusDot';
import { useClientInfos } from '../utils/hooks';
import { PERM_ADMIN, PERM_ADMIN_READ, PERM_CONFIGURATION } from '../utils/permissions';
import {
  TEXT, causeText, clock, historyRows, homeNotice, intervalsFor, nameLine, reasonText, settingsBody,
} from '../utils/reachWatch';

// --- one answer for the page
let store = { data: null, at: 0, loading: false };
const listeners = new Set();
const emit = () => listeners.forEach((listener) => listener({ ...store }));
const load = (force) => {
  if (store.loading || (!force && Date.now() - store.at < 20000)) return;
  store = { ...store, loading: true };
  API.reachWatch.status()
    .then((res) => { store = { ...store, data: res && res.data, at: Date.now(), loading: false }; })
    .catch(() => { store = { ...store, at: Date.now(), loading: false }; })
    .finally(emit);
};
const put = (data) => { store = { ...store, data, at: Date.now() }; emit(); };

const useReachWatch = (enabled = true) => {
  const [state, setState] = React.useState({ ...store });
  React.useEffect(() => {
    if (!enabled) return undefined;
    listeners.add(setState);
    load(false);
    setState({ ...store });
    return () => { listeners.delete(setState); };
  }, [enabled]);
  // while a round runs, ask again until it is over
  React.useEffect(() => {
    if (!enabled || !state.data || !state.data.running) return undefined;
    const timer = setTimeout(() => load(true), 4000);
    return () => clearTimeout(timer);
  }, [enabled, state.data, state.at]);
  return state;
};

const say = (t, triple) => (triple ? t(triple[0], triple[1], { ...(triple[2] || {}), interpolation: { escapeValue: false } }) : '');
const wrap = { overflowWrap: 'anywhere', minWidth: 0 };

// The line of the home page (components/homeNotices.jsx), for admins.
export const useReachWatchNotices = () => {
  const { t } = useTranslation();
  const { hasPermission } = useClientInfos();
  const admin = hasPermission(PERM_ADMIN_READ);
  const { data } = useReachWatch(admin);
  const notice = admin ? homeNotice(data) : null;
  if (!notice) return { items: [] };
  const cause = notice.cause ? t(notice.cause[0], notice.cause[1]) : '';
  return {
    items: [{
      id: notice.id,
      severity: notice.severity,
      attrs: { 'data-reach-watch-home': notice.severity },
      text: <span style={wrap}>{t(notice.text[0], notice.text[1], { ...notice.params, cause, interpolation: { escapeValue: false } })}</span>,
      actions: <Link href="/cosmos-ui/config-general/http-server" className="ct-notice-link">{t(...TEXT.homeOpen)}</Link>,
    }],
  };
};

const NameRow = ({ n, t }) => {
  const line = nameLine(n);
  return <Stack direction="row" spacing={1} alignItems="flex-start" sx={{ minWidth: 0 }} data-reach-watch-name={line.host} data-reach-watch-state={n.state}>
    <StatusDot status={line.dot} size={8} style={{ marginTop: 7, flexShrink: 0 }} />
    <Typography variant="body2" component="div" sx={wrap}>
      <strong>{line.host}</strong>{line.panel ? ' (' + t(...TEXT.panel) + ')' : ''}{': '}
      {t(line.state[0], line.state[1])}
      {line.cause && <> — {t(line.cause[0], line.cause[1])}</>}
      {line.parts.length > 0 && <Typography component="span" variant="caption" color="textSecondary">
        {' · '}{line.parts.map((part) => say(t, part)).join(' · ')}
      </Typography>}
    </Typography>
  </Stack>;
};

// The card of the settings page.
export const ReachWatchCard = () => {
  const { t } = useTranslation();
  const { hasPermission } = useClientInfos();
  const admin = hasPermission(PERM_ADMIN_READ);
  const { data, loading } = useReachWatch(admin);
  const [busy, setBusy] = React.useState('');
  const [notice, setNotice] = React.useState(null);
  if (!admin || !data) return null;

  const save = (body) => {
    setBusy('settings');
    setNotice(null);
    API.reachWatch.settings(body)
      .then((res) => { if (res && res.data) put(res.data); })
      .catch(() => {})
      .finally(() => setBusy(''));
  };
  const check = () => {
    setBusy('check');
    setNotice(null);
    API.reachWatch.check()
      .then((res) => {
        const answer = (res && res.data) || {};
        if (answer.status) put(answer.status);
        setNotice(answer.started ? { severity: 'info', text: t(...TEXT.checkStarted) } : { severity: 'info', text: t(...TEXT.tooSoon) });
      })
      .catch(() => {})
      .finally(() => setBusy(''));
  };

  const reason = reasonText(data);
  const names = data.names || [];
  const history = historyRows(data.history);
  const testable = data.enabled && names.length > 0;

  return <MainCard title={t(...TEXT.title)}>
    <Stack spacing={2} data-reach-watch-card>
      <Typography variant="body2" color="textSecondary" sx={wrap}>{t(...TEXT.intro)}</Typography>

      <PermissionGuard permission={PERM_CONFIGURATION}>
        <Stack spacing={1.5}>
          <FormControlLabel
            sx={{ mr: 0, alignItems: 'flex-start', '& .MuiFormControlLabel-label': { pt: '7px', ...wrap } }}
            control={<Switch checked={!!data.enabled} disabled={!!busy} onChange={(event) => save(settingsBody(event.target.checked))}
              inputProps={{ 'data-reach-watch-switch': data.enabled ? 'on' : 'off' }} />}
            label={t(...TEXT.switchLabel)}
          />
          {data.enabled && <ChoiceChips
            id="reach-watch-interval"
            label={t(...TEXT.interval)}
            value={data.minutes}
            disabled={!!busy}
            onChange={(minutes) => save(settingsBody(undefined, minutes))}
            options={intervalsFor(data).map((m) => ({ value: m, label: t(TEXT.minutes[0], TEXT.minutes[1], { count: m }) }))}
          />}
        </Stack>
      </PermissionGuard>

      {reason && <Alert severity="info" sx={wrap} data-reach-watch-reason={data.enabled ? data.reason : 'off'}>{say(t, reason)}</Alert>}

      {data.outage && <Alert severity="error" sx={wrap} data-reach-watch-outage={data.outage.cause}>
        {t(TEXT.homeDown[0], TEXT.homeDown[1], { since: clock(data.outage.since), cause: t(...causeText(data.outage.cause)), interpolation: { escapeValue: false } })}
      </Alert>}

      {testable && <Stack spacing={0.75}>
        <Typography variant="subtitle2">{t(...TEXT.names)}</Typography>
        {names.map((n) => <NameRow key={n.host} n={n} t={t} />)}
        {data.nextRound && <Typography variant="caption" color="textSecondary">{say(t, [...TEXT.nextRound, { time: clock(data.nextRound) }])}</Typography>}
      </Stack>}

      {data.router && data.routerAt && <Typography variant="body2" sx={wrap} data-reach-watch-router>
        {say(t, [...TEXT.router, { time: clock(data.routerAt), answer: data.router }])}
      </Typography>}
      {data.seenAs && <Typography variant="caption" color="textSecondary" sx={wrap}>{say(t, [...TEXT.seenAs, { address: data.seenAs }])}</Typography>}

      {(data.direct || []).length > 0 && <Typography variant="body2" color="textSecondary" sx={wrap} data-reach-watch-direct>
        {say(t, [...TEXT.direct, { names: data.direct.join(', ') }])}
      </Typography>}
      {(data.tooMany || []).length > 0 && <Typography variant="body2" color="textSecondary" sx={wrap}>
        {say(t, [...TEXT.tooMany, { names: data.tooMany.slice(0, 10).join(', ') + (data.tooMany.length > 10 ? ', …' : '') }])}
      </Typography>}

      {notice && <Alert severity={notice.severity} sx={wrap}>{notice.text}</Alert>}
      {testable && <PermissionGuard permission={PERM_ADMIN}>
        <div>
          <LoadingButton size="small" variant="outlined" loading={busy === 'check' || data.running || loading} disabled={!!busy} onClick={check} data-reach-watch-check>
            {t(...TEXT.checkNow)}
          </LoadingButton>
        </div>
      </PermissionGuard>}

      <Stack spacing={0.5} data-reach-watch-history>
        <Typography variant="subtitle2">{t(...TEXT.history)}</Typography>
        {history.length === 0 && <Typography variant="body2" color="textSecondary">{t(...TEXT.historyNone)}</Typography>}
        {history.map((row, i) => <Typography key={i} variant="body2" sx={wrap}>
          {t(row.text[0], row.text[1], {
            ...row.params,
            duration: row.duration ? say(t, row.duration) : '',
            cause: t(row.cause[0], row.cause[1]),
            interpolation: { escapeValue: false },
          })}
        </Typography>)}
      </Stack>
    </Stack>
  </MainCard>;
};

export default ReachWatchCard;

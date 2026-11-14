// Fork addition: one action that gives every app without a URL its local
// address. Apps installed before the fork's link model have no address at
// all; this lists what would be created (app, address, the port it leads to
// and why that one), with a tick per line, and creates the ticked ones the
// way the "New URL" dialog does: the same route (utils/routeGuide.js'
// newAppRoute), the same check, the same call, one after the other. Nothing
// is created without the tick, and nothing becomes reachable from the
// internet: these are local addresses.
import * as React from 'react';
import {
  Alert,
  Button,
  Checkbox,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  Stack,
  Typography,
} from '@mui/material';
import { LoadingButton } from '@mui/lab';
import { CheckCircleOutlined, HomeOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import PermissionGuard from './permissionGuard';
import { PERM_RESOURCES } from '../utils/permissions';
import { ValidateRoute } from '../utils/routes';
import { serverBack } from '../utils/appLinks';
import { planLocalAddresses } from '../utils/routeGuide';
import { localAddress } from './hostnameSuggestions';
import { LocalNameState } from './localNames'; // Fork addition
import { isLocalName } from '../utils/localNames';
import { keepOnBackdrop } from './formHelpers';
import { portSourceText } from '../pages/config/routes/forkRouteGuide';
import defaultport from '../pages/servapps/defaultport.json';

// The plan for the apps of the page, each address proposed like the dialog
// proposes it, knowing the addresses planned before it.
const planFor = (config, containers, selfName) => {
  if (!config || !config.HTTPConfig || !containers) {
    return [];
  }
  const proxy = config.HTTPConfig.ProxyConfig || {};
  return planLocalAddresses({
    containers,
    routes: proxy.Routes || [],
    defaults: defaultport,
    skip: selfName ? [selfName] : [],
    hostFor: (name, soFar) => {
      try {
        return localAddress(name, { ...config, HTTPConfig: { ...config.HTTPConfig, ProxyConfig: { ...proxy, Routes: soFar } } });
      } catch (e) {
        return '';
      }
    },
  });
};

// `wrap` puts the notice into the page's grid; nothing is rendered when no app can be helped.
const LocalAddressesForAll = ({ config, containers, selfName, refresh, wrap }) => {
  const { t } = useTranslation();
  const [plan, setPlan] = React.useState(null); // the list while the dialog is open
  const [ticked, setTicked] = React.useState({});
  const [outcome, setOutcome] = React.useState({}); // name → 'creating' | 'done' | the reason it failed
  const [running, setRunning] = React.useState(false);

  const current = React.useMemo(() => planFor(config, containers, selfName), [config, containers, selfName]);
  const ready = current.filter((entry) => entry.state === 'ready');

  if (ready.length === 0 && !plan) {
    return null;
  }

  const open = () => {
    setPlan(current);
    setTicked(Object.fromEntries(ready.map((entry) => [entry.name, true])));
    setOutcome({});
  };
  const close = () => {
    if (!running) {
      setPlan(null);
    }
  };

  const listed = plan || [];
  const offered = listed.filter((entry) => entry.state === 'ready');
  const leftOut = listed.filter((entry) => entry.state !== 'ready');
  const chosen = offered.filter((entry) => ticked[entry.name] && outcome[entry.name] !== 'done');
  const finished = offered.length > 0 && offered.every((entry) => !ticked[entry.name] || outcome[entry.name] === 'done')
    && offered.some((entry) => outcome[entry.name] === 'done');

  const create = async () => {
    setRunning(true);
    const results = { ...outcome };
    for (const entry of chosen) {
      results[entry.name] = 'creating';
      setOutcome({ ...results });
      try {
        // the same check the dialog makes before it sends a URL
        const problems = ValidateRoute(entry.route, config) || [];
        if (problems.length > 0) {
          throw new Error(problems.join(' '));
        }
        await API.config.addRoute(entry.route);
        results[entry.name] = 'done';
        setOutcome({ ...results });
        // the server reloads after each new URL: wait until it answers again
        await serverBack();
      } catch (e) {
        results[entry.name] = (e && e.message) || String(e);
        setOutcome({ ...results });
        break; // what stopped this one would stop the next ones too
      }
    }
    setRunning(false);
    refresh && refresh();
  };

  const whyLeftOut = (entry) => ({
    stopped: t('mgmt.urls.all.why.stopped', 'not running, so its ports are not known: start it first'),
    hostNetwork: t('mgmt.urls.all.why.hostNetwork', 'uses the server\'s own network: it is reached at the server\'s address and its own port'),
    taken: entry.host
      ? t('mgmt.urls.all.why.taken', 'the address {{host}} or the name is already used by another URL', { host: entry.host })
      : t('mgmt.urls.all.why.noAddress', 'no free address could be proposed'),
    noPort: entry.port
      ? t('mgmt.urls.all.why.guess', 'declares no port; {{port}} would be a guess: use New on its card', { port: entry.port })
      : t('mgmt.urls.all.why.noPort', 'has no port to point at (not a web app, or its port is not declared): use New on its card if it has a web page'),
  }[entry.state] || entry.state);

  const names = ready.map((entry) => entry.name);
  const notice = <Alert
    severity="info"
    sx={{ textAlign: 'left', alignItems: 'center', '& .MuiAlert-message': { overflowWrap: 'anywhere', minWidth: 0 }, '& .MuiAlert-action': { alignItems: 'center', pt: 0 } }}
    action={<PermissionGuard permission={PERM_RESOURCES}>
      <Button color="inherit" size="small" variant="outlined" startIcon={<HomeOutlined />} onClick={open} sx={{ whiteSpace: 'nowrap' }}>
        {ready.length === 1
          ? t('mgmt.urls.all.buttonOne', 'Give it a local address')
          : t('mgmt.urls.all.button', 'Give them a local address')}
      </Button>
    </PermissionGuard>}
  >
    {ready.length === 1
      ? t('mgmt.urls.all.noticeOne', '{{name}} has no address yet.', { name: names[0] })
      : t('mgmt.urls.all.notice', '{{number}} apps have no address yet: {{names}}.', {
        number: ready.length,
        names: names.slice(0, 6).join(', ') + (names.length > 6 ? '…' : ''),
      })}
  </Alert>;

  return <>
    {ready.length > 0 && (wrap ? wrap(notice) : notice)}

    {plan && <Dialog open onClose={keepOnBackdrop(close)} fullWidth maxWidth="sm">
      <DialogTitle>{t('mgmt.urls.all.title', 'A local address for the apps that have none')}</DialogTitle>
      <DialogContent>
        <Stack spacing={1.5}>
          <Typography variant="body2" color="textSecondary">
            {t('mgmt.urls.all.text',
              'Each ticked app gets the address shown. It works on the local network only: nothing becomes reachable from the internet. Every address can be changed or deleted afterwards under URLs.')}
          </Typography>

          {/* Fork addition: will the .local names resolve, and on which devices, see localNames.jsx */}
          <LocalNameState host={(offered.find((entry) => isLocalName(entry.host)) || {}).host} help />

          {offered.map((entry) => {
            const state = outcome[entry.name];
            const failed = state && state !== 'done' && state !== 'creating';
            return <Stack key={entry.name} spacing={0.25}>
              <Stack direction="row" alignItems="flex-start" spacing={1}>
                <FormControlLabel
                  sx={{ flexGrow: 1, minWidth: 0, alignItems: 'flex-start', mr: 0 }}
                  control={<Checkbox
                    sx={{ pt: 0.5 }}
                    checked={Boolean(ticked[entry.name])}
                    disabled={running || state === 'done'}
                    onChange={(event) => setTicked({ ...ticked, [entry.name]: event.target.checked })}
                  />}
                  label={<Stack sx={{ minWidth: 0 }}>
                    <Typography variant="body1" sx={{ overflowWrap: 'anywhere' }}>
                      <strong>{entry.name}</strong> → <HomeOutlined /> {entry.host}
                    </Typography>
                    <Typography variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
                      {portSourceText(t, { port: entry.port, source: entry.source }, entry.image)}
                    </Typography>
                  </Stack>}
                />
                {state === 'creating' && <CircularProgress size={18} sx={{ mt: 0.75, flexShrink: 0 }} aria-label={t('mgmt.urls.all.creating', 'Creating')} />}
                {state === 'done' && <Typography color="success.main" sx={{ mt: 0.5, flexShrink: 0 }} aria-label={t('mgmt.urls.all.done', 'Created')}><CheckCircleOutlined /></Typography>}
              </Stack>
              {failed && <Alert severity="error" sx={{ overflowWrap: 'anywhere' }}>
                {t('mgmt.urls.all.failed', 'Not created: {{reason}}', { reason: state })}
              </Alert>}
            </Stack>;
          })}

          {offered.length === 0 && <Alert severity="info">
            {t('mgmt.urls.all.nothing', 'No app is left that an address can be proposed for.')}
          </Alert>}

          {leftOut.length > 0 && <Stack spacing={0.5}>
            <Typography variant="subtitle2">{t('mgmt.urls.all.leftOut', 'Left out:')}</Typography>
            {leftOut.map((entry) => <Typography key={entry.name} variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
              <strong>{entry.name}</strong>: {whyLeftOut(entry)}
            </Typography>)}
          </Stack>}

          <Typography variant="caption" color="textSecondary">
            {t('mgmt.urls.all.hostHint',
              'Each app receives its requests under its new name. Apps that only answer to names they know (Radarr and Sonarr say "Invalid Hostname") need that name added in their own settings.')}
          </Typography>
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={close} disabled={running}>{finished ? t('global.close') : t('global.cancelAction')}</Button>
        {!finished && <LoadingButton loading={running} disabled={chosen.length === 0} onClick={create}>
          {chosen.length === 1
            ? t('mgmt.urls.all.createOne', 'Create 1 address')
            : t('mgmt.urls.all.create', 'Create {{number}} addresses', { number: chosen.length })}
        </LoadingButton>}
      </DialogActions>
    </Dialog>}
  </>;
};

export default LocalAddressesForAll;

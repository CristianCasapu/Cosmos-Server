// Fork addition: whether a container can be reached from the internet, and
// the link when it can. The server checks each app route (src/reach); the
// card gets one small chip, the container page the checks one by one with
// what to do about each problem. The answer is fetched once for the whole
// page and shared by every card.
import * as React from 'react';
import { Alert, Chip, Link, Stack, Tooltip, Typography } from '@mui/material';
import { useTheme } from '@mui/material/styles';
import { LoadingButton } from '@mui/lab';
import {
  CheckCircleOutlined,
  CloseCircleOutlined,
  GlobalOutlined,
  InfoCircleOutlined,
  QuestionCircleOutlined,
} from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { LocalNameState } from './localNames'; // Fork addition
import * as API from '../api';
import StatusDot from './statusDot';
import PermissionGuard from './permissionGuard';
import { PERM_ADMIN_READ } from '../utils/permissions';
import {
  EXPOSED, PANEL_ROUTE, checkAddresses, checkSentence, firstCheck, panelResults, portsText, routesStamp, safeURL, summarize,
} from '../utils/reachability';
import { RouterPortLine, useRouterStatus } from './routerPorts'; // ports the router forwards (src/upnp)
import { appMappingsOf } from '../utils/routerPorts';

// --- one answer for the whole page
let store = { data: null, at: 0, loading: false, failed: false, stamp: null };
const listeners = new Set();
const emit = () => listeners.forEach((listener) => listener({ ...store }));

const load = (stamp) => {
  if (store.loading || (store.stamp === stamp && Date.now() - store.at < 30000)) {
    return;
  }
  store = { ...store, loading: true, stamp };
  API.config.reachability()
    .then((res) => { store = { ...store, data: res.data, at: Date.now(), loading: false, failed: false }; })
    .catch(() => { store = { ...store, at: Date.now(), loading: false, failed: true }; })
    .finally(emit);
};

// checkAgain asks the server to look everything up again now.
const checkAgain = (askPublicIP) => {
  store = { ...store, loading: true };
  emit();
  return API.config.reachabilityCheck(askPublicIP)
    .then((res) => { store = { ...store, data: res.data, at: Date.now(), loading: false, failed: false }; return res.data; })
    .catch(() => { store = { ...store, loading: false }; return null; })
    .finally(emit);
};

const useReach = (config) => {
  const [state, setState] = React.useState({ ...store });
  const stamp = React.useMemo(() => routesStamp(config), [config]);
  React.useEffect(() => {
    listeners.add(setState);
    if (config) {
      load(stamp);
    }
    setState({ ...store });
    return () => { listeners.delete(setState); };
  }, [stamp]);
  return state;
};

const DOT = { verified: 'success', unverified: 'warning', problem: 'error', forwarded: 'warning', ports: 'orange' };

const useTexts = () => {
  const { t } = useTranslation();
  const sentence = (check, routeName) => {
    const s = checkSentence(check, routeName);
    return t(s.key, s.text, s.params);
  };
  const label = (state) => ({
    verified: t('mgmt.reach.label.verified', 'Internet: checked'),
    unverified: t('mgmt.reach.label.unverified', 'Internet: not verified'),
    problem: t('mgmt.reach.label.problem', 'Internet: problem'),
    forwarded: t('mgmt.reach.label.forwarded', 'Internet: port forwarded'),
    ports: t('mgmt.reach.label.ports', 'Internet: if forwarded'),
    limited: t('mgmt.reach.label.limited', 'Internet: restricted'),
  })[state] || t('mgmt.reach.label.no', 'Internet: no');

  const explain = (summary) => {
    const route = summary.route;
    switch (summary.state) {
      case 'verified':
        return t('mgmt.reach.tip.verified',
          'Checked from the server: the name points here, the router forwards the port and the certificate is valid. Nobody tested it from outside. Click to open {{url}}.',
          { url: summary.url });
      case 'unverified': {
        const check = firstCheck(route, 'unknown');
        return t('mgmt.reach.tip.unverified', 'Open to the internet at {{host}}, but not everything could be checked: {{reason}} Click to open it.',
          { host: route.host, reason: check ? sentence(check, route.route) : '' });
      }
      case 'problem': {
        const check = firstCheck(route, 'problem');
        return t('mgmt.reach.tip.problem', 'Has the internet address {{host}}, but: {{reason}}',
          { host: route.host, reason: check ? sentence(check, route.route) : '' });
      }
      case 'forwarded':
        return t('mgmt.reach.tip.forwarded',
          'No internet address through Cosmos, but your router forwards port {{ports}} to this app (opened by Cosmos over UPnP). It reaches the app directly, without Cosmos\' login. Nobody tested it from outside.',
          { ports: summary.forwarded.map((mapping) => mapping.externalPort + (mapping.protocol === 'UDP' ? '/udp' : '')).join(', ') });
      case 'ports':
        return summary.hostNetwork && !portsText(summary.ports)
          ? t('mgmt.reach.tip.hostNetwork',
            'No internet address through Cosmos. It uses the server\'s own network, so its ports are the server\'s: reachable from the local network, from the internet only if your router forwards them. Cosmos cannot tell.')
          : t('mgmt.reach.tip.ports',
            'No internet address through Cosmos. Published on the server: port {{ports}}. Reachable from the local network; from the internet only if your router forwards it, which Cosmos cannot tell.',
            { ports: portsText(summary.ports) });
      case 'limited':
        return t('mgmt.reach.tip.limited', 'Its URL may be opened only from the addresses listed in its settings, or from the Constellation.');
      case 'local':
        return t('mgmt.reach.tip.local', 'Only local addresses: not reachable from the internet.');
      case 'off':
        return t('mgmt.reach.tip.off', 'Its URL is switched off.');
      default:
        return t('mgmt.reach.tip.none', 'No URL and no published port: not reachable from outside.');
    }
  };
  return { t, sentence, label, explain };
};

// panel: the verdict on the panel's own address instead of on URLs
const summaryOf = (state, routeNames, ports, hostNetwork, mappings, panel) => (state.data
  ? summarize({ routeNames, results: panel ? panelResults(state.data) : state.data.routes, ports, hostNetwork, mappings })
  : null);

// The chip of a card. Shows nothing until the server answered, or when the
// visitor may not read the answer.
// containerNames: the containers the card stands for, to find the ports the
// router forwards for them.
export const ReachChip = ({ config, routeNames, ports, hostNetwork, style, containerNames, panel }) => {
  const state = useReach(config);
  const { label, explain } = useTexts();
  const router = useRouterStatus();
  const summary = summaryOf(state, routeNames, ports, hostNetwork, appMappingsOf(router, containerNames), panel);
  if (!summary) {
    return null;
  }
  const url = safeURL(summary.url);
  const dot = DOT[summary.state];
  const link = url ? { component: 'a', href: url, target: '_blank', rel: 'noopener noreferrer', clickable: true } : { tabIndex: 0 };

  return <Tooltip title={explain(summary)}>
    <Chip
      size="small"
      variant="outlined"
      color={summary.state === 'verified' ? 'success' : summary.state === 'problem' ? 'error' : 'default'}
      icon={<GlobalOutlined />}
      label={<>{dot ? <StatusDot status={dot} size={8} style={{ marginRight: 6 }} /> : <StatusDot status="unknown" hollow size={8} style={{ marginRight: 6 }} />}{label(summary.state)}</>}
      style={{ maxWidth: '100%', ...style }}
      data-reach={summary.state}
      {...link}
    />
  </Tooltip>;
};

const CheckIcon = ({ status }) => {
  const theme = useTheme();
  const props = { style: { marginTop: 3, flexShrink: 0 } };
  if (status === 'ok') return <CheckCircleOutlined {...props} style={{ ...props.style, color: theme.palette.success.main }} />;
  if (status === 'problem') return <CloseCircleOutlined {...props} style={{ ...props.style, color: theme.palette.error.main }} />;
  if (status === 'unknown') return <QuestionCircleOutlined {...props} />;
  return <InfoCircleOutlined {...props} />;
};

// The "Internet" part of a container's page.
export const ReachPanel = ({ config, routeNames, ports, hostNetwork, containerName, panel }) => {
  const state = useReach(config);
  const { t, sentence, label, explain } = useTexts();
  const [notice, setNotice] = React.useState(null);
  const router = useRouterStatus();
  const containerNames = containerName ? [containerName] : [];
  const summary = summaryOf(state, routeNames, ports, hostNetwork, appMappingsOf(router, containerNames), panel);

  if (!summary) {
    return state.failed ? null : <Typography variant="caption" color="textSecondary" component="div" style={{ margin: '5px' }}>
      {t('mgmt.reach.loading', 'Checking…')}
    </Typography>;
  }

  const fixLabel = (fix) => (fix.includes('config-url')
    ? t('mgmt.reach.fix.route', 'URL settings')
    : fix.endsWith('/https') ? t('mgmt.reach.fix.domains', 'Domains') : t('mgmt.reach.fix.internet', 'Internet access'));

  const again = (askPublicIP) => {
    setNotice(null);
    checkAgain(askPublicIP).then((data) => {
      if (data && data.tooSoon) setNotice(t('mgmt.reach.tooSoon', 'Checked moments ago: try again in a few seconds.'));
      else if (data && data.lookupFailed) setNotice(t('mgmt.reach.lookupFailed', 'The public address could not be looked up.'));
    });
  };

  const routes = [...summary.routes].sort((a, b) => (EXPOSED.includes(b.state) ? 1 : 0) - (EXPOSED.includes(a.state) ? 1 : 0));
  const data = state.data;
  const unknownIP = summary.routes.some((route) => route.checks.some((check) => check.code === 'publicIPUnknown' || check.code === 'ipPublicIPUnknown'));
  const source = {
    upnp: t('mgmt.reach.source.upnp', 'from the router'),
    dyndns: t('mgmt.reach.source.dyndns', 'from DynDNS'),
    lookup: t('mgmt.reach.source.lookup', 'looked up'),
  }[data.publicIPSource];

  return <Stack spacing={1.5} style={{ margin: '5px', maxWidth: '100%' }} data-reach-panel>
    <div><ReachChip config={config} routeNames={routeNames} ports={ports} hostNetwork={hostNetwork} containerNames={containerNames} panel={panel} /></div>
    {/* the routes below say it check by check; the sentence is for the rest */}
    {!summary.route && <Typography variant="body2">{explain(summary)}</Typography>}

    {routes.map((route) => {
      const url = safeURL(route.url);
      return <Stack key={route.route} spacing={0.5} style={{ minWidth: 0 }}>
        <Typography variant="subtitle2" style={{ overflowWrap: 'anywhere' }}>
          {url
            ? <Link href={url} target="_blank" rel="noopener noreferrer">{url}</Link>
            : route.host || route.route}
          {' · '}
          <Typography component="span" variant="caption" color="textSecondary">{label(route.state)}</Typography>
        </Typography>
        {route.checks.map((check, index) => {
          const s = checkSentence(check, route.route);
          const addresses = checkAddresses(check);
          return <Stack key={index} direction="row" spacing={1} alignItems="flex-start" style={{ minWidth: 0 }}>
            <CheckIcon status={check.status} />
            <Typography variant="body2" style={{ overflowWrap: 'anywhere', minWidth: 0 }}>
              {sentence(check, route.route)}
              {addresses && <Typography component="span" variant="caption" color="textSecondary"> ({addresses})</Typography>}
              {s.fix && check.status !== 'ok' && <> <Link href={s.fix}>{fixLabel(s.fix)}</Link></>}
            </Typography>
          </Stack>;
        })}
        <LocalNameState host={route.host} help />{/* Fork addition: is the .local name published and answered, see localNames.jsx */}
      </Stack>;
    })}

    {summary.ports.map((port) => {
      const words = port.scope === 'server'
        ? t('mgmt.reach.port.server', 'Port {{port}} is published for this server only ({{ip}}): not reachable from other devices.', { port: port.port, ip: port.ip })
        : port.scope === 'address'
          ? t('mgmt.reach.port.address', 'Port {{port}} is published on the address {{ip}} only. From the internet only if your router forwards it; Cosmos cannot tell.', { port: port.port, ip: port.ip })
          : t('mgmt.reach.port.all', 'Port {{port}} ({{type}}) is published on the server: reachable from the local network; from the internet only if your router forwards it, which Cosmos cannot tell.', { port: port.port, type: port.type.toUpperCase() });
      // a port the router could forward gets the switch that asks it to (routerPorts.jsx)
      if (containerName && port.scope !== 'server') {
        return <RouterPortLine key={port.port + port.type} config={config} containerName={containerName} port={port} fallback={words} />;
      }
      return <Stack key={port.port + port.type} direction="row" spacing={1} alignItems="flex-start">
        <QuestionCircleOutlined style={{ marginTop: 3, flexShrink: 0 }} />
        <Typography variant="body2" style={{ overflowWrap: 'anywhere', minWidth: 0 }}>{words}</Typography>
      </Stack>;
    })}
    {summary.hostNetwork && <Stack direction="row" spacing={1} alignItems="flex-start">
      <QuestionCircleOutlined style={{ marginTop: 3, flexShrink: 0 }} />
      <Typography variant="body2">
        {t('mgmt.reach.port.host', 'Uses the server\'s own network: every port it listens on is a port of the server. From the internet only if your router forwards it.')}
      </Typography>
    </Stack>}

    <Typography variant="caption" color="textSecondary" component="div">
      {data.publicIP
        ? t('mgmt.reach.publicIP', 'This server\'s public address: {{ip}} ({{source}}).', { ip: data.publicIP, source })
        : data.publicIPSource
          ? t('mgmt.reach.publicIPKnown', 'Cosmos knows this server\'s public address ({{source}}).', { source })
          : t('mgmt.reach.publicIPUnknown', 'Cosmos does not know this server\'s public address: it learns it from automatic port forwarding or from DynDNS.')}
      {' '}
      {t('mgmt.reach.checkedAt', 'Checked {{time}} from the server. Nothing is tested from outside your network: only a device outside it (a phone on mobile data) can confirm.',
        { time: new Date(data.checkedAt).toLocaleString() })}
    </Typography>
    {notice && <Alert severity="info">{notice}</Alert>}
    <Stack direction="row" useFlexGap flexWrap="wrap" spacing={1}>
      <LoadingButton size="small" variant="outlined" loading={state.loading} onClick={() => again(false)}>
        {t('mgmt.reach.checkAgain', 'Check again')}
      </LoadingButton>
      {unknownIP && !data.publicIPSource && <PermissionGuard permission={PERM_ADMIN_READ}>
        <Tooltip title={t('mgmt.reach.askPublicIPTip',
          'Asks api.ipify.org (then icanhazip.com, checkip.amazonaws.com: the services DynDNS uses) which address this server shows up with. Only on this click; the answer is kept for six hours.')}>
          <span>
            <LoadingButton size="small" variant="outlined" loading={state.loading} onClick={() => again(true)}>
              {t('mgmt.reach.askPublicIP', 'Look up my public address')}
            </LoadingButton>
          </span>
        </Tooltip>
      </PermissionGuard>}
    </Stack>
  </Stack>;
};

// Whether the panel itself can be reached from the internet at its own
// address: does the name lead to this server, is the port forwarded, does the
// certificate fit. The same checks, states and words as for an application.
// Shows nothing while the panel has no address, or to who may not read it.
const noRoutes = {};
export const PanelReach = () => {
  const state = useReach(noRoutes);
  const { t } = useTranslation();
  if (!state.data || panelResults(state.data).length === 0) {
    return null;
  }
  return <Stack spacing={0.5} data-reach-own-panel>
    <Typography variant="subtitle2">{t('mgmt.reach.panel.title', 'Can the panel be reached from the internet at this address?')}</Typography>
    <ReachPanel config={noRoutes} routeNames={[PANEL_ROUTE]} ports={[]} panel />
  </Stack>;
};

export default ReachChip;

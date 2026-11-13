// Fork addition: the published ports of apps on the router (src/upnp on the
// server, utils/routerPorts.js for everything that can be computed without
// the UI). The router's status is fetched once for the whole page and shared:
// - useCosmosPorts: the ports Cosmos' own addresses are reached on, for every
//   link and preview (hostChip, the address proposals, the Domains dialog);
// - RouterPortLine: the line of one published port on the container's page,
//   with the switch that opens or closes it on the router;
// - RouterPortLinks: the forwarded ports of an app as external addresses;
// - InstallRouterPorts: the same switch, off, when an app is installed;
// - MappingsOverview: everything open on the router, in "Internet access".
import * as React from 'react';
import {
  Alert,
  Button,
  Checkbox,
  Chip,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  Link,
  MenuItem,
  Stack,
  Switch,
  TextField,
  Tooltip,
  Typography,
} from '@mui/material';
import { LoadingButton } from '@mui/lab';
import { CheckCircleOutlined, CloseCircleOutlined, CopyOutlined, GlobalOutlined, QuestionCircleOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import PermissionGuard from './permissionGuard';
import { PERM_ADMIN, PERM_CONFIGURATION_READ } from '../utils/permissions';
import { useClientInfos } from '../utils/hooks';
import { notify } from '../utils/activity';
import { getContainersRoutes } from '../utils/routes';
import {
  appMappingsOf, confirmText, cosmosPorts, findAppMapping, installPorts, leaseText, looksLikeWeb, mappingSentence,
  overviewRows, portAddress, portKey, portNote, publicHost, shownAddress,
} from '../utils/routerPorts';

// --- one answer for the whole page
let store = { status: null, at: 0, loading: false };
const listeners = new Set();
const emit = () => listeners.forEach((listener) => listener({ ...store }));

const load = (force) => {
  if (store.loading || (!force && store.at && Date.now() - store.at < 30000)) {
    return;
  }
  store = { ...store, loading: true };
  // quiet: a reader without the permission simply gets no answer
  API.config.upnpStatus(true)
    .then((res) => { store = { status: (res && res.data) || null, at: Date.now(), loading: false }; })
    .catch(() => { store = { ...store, at: Date.now(), loading: false }; })
    .finally(emit);
};

// The answer of a command is the new status: every screen shows it at once.
export const setRouterStatus = (status) => {
  if (status && typeof status === 'object') {
    store = { status, at: Date.now(), loading: false };
    emit();
  }
};

export const useRouterStatus = () => {
  const [state, setState] = React.useState({ ...store });
  // only who may read the settings is answered: nobody else asks (a refused
  // request would be noted as an incident each time)
  const allowed = useClientInfos().hasPermission(PERM_CONFIGURATION_READ);
  React.useEffect(() => {
    if (!allowed) {
      return undefined;
    }
    listeners.add(setState);
    load(false);
    setState({ ...store });
    return () => { listeners.delete(setState); };
  }, [allowed]);
  return allowed ? state.status : null;
};

// The ports Cosmos is reached on, as mapped or configured (null: unknown).
export const useCosmosPorts = (config) => cosmosPorts(config, useRouterStatus());

// One line under address proposals: the port they are reached on from the
// internet, when it is not 443 (the proposals themselves are stored without).
export const ExternalPortNote = ({ config, example }) => {
  const { t } = useTranslation();
  const ports = useCosmosPorts(config);
  if (!ports || !ports.externalHttps || ports.externalHttps === 443) {
    return null;
  }
  return <Typography variant="caption" color="textSecondary" data-external-port-note sx={{ overflowWrap: 'anywhere' }}>
    {t('mgmt.router.externalPortNote',
      'From the internet, addresses under your domains are reached on port {{port}}: https://{{example}}',
      { port: ports.externalHttps, example: shownAddress(example || 'app.example.org', ports, 'https') })}
  </Typography>;
};

const useSentence = () => {
  const { t } = useTranslation();
  return (s) => (s ? t(s.key, s.text, s.params) : '');
};

const WhatOpeningMeans = () => {
  const { t } = useTranslation();
  return <Alert severity="warning" sx={{ overflowWrap: 'anywhere' }}>
    {t('mgmt.router.meaning',
      'A port opened this way reaches the app directly from the internet: without Cosmos\' login, without SmartShield, without the HTTPS Cosmos provides, and Fail2ban does not see the app\'s own sign-ins. The app\'s own protection is all there is.')}
  </Alert>;
};

// The dialog that opens a port. It says what opening means, recommends the
// app's URL for a web interface, proposes what is on the port, and its
// button names the port and the app. With onChoose nothing is sent: the
// install forms keep the choice until the container exists.
export const OpenPortDialog = ({ containerName, port, routes, config, onClose, onChoose }) => {
  const { t } = useTranslation();
  const status = useRouterStatus();
  const web = looksLikeWeb({ containerName, containerPort: port.port, protocol: port.protocol, routes });
  const [scheme, setScheme] = React.useState(web.web ? web.scheme : '');
  const [external, setExternal] = React.useState('');
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState('');
  const ports = cosmosPorts(config, status);
  const reserved = (status && status.reserved) || [];
  const proto = String(port.protocol || 'tcp').toUpperCase();

  const externalNumber = external.trim() === '' ? 0 : Number(external.trim());
  let externalError = '';
  if (external.trim() !== '' && !(Number.isInteger(externalNumber) && externalNumber >= 1 && externalNumber <= 65535)) {
    externalError = t('mgmt.router.dialog.externalInvalid', 'Enter a port between 1 and 65535, or leave it empty');
  } else if (reserved.includes(externalNumber)) {
    externalError = t('mgmt.router.dialog.externalReserved', 'Cosmos never asks the router for port {{port}} (Internet access, reserved ports)', { port: externalNumber });
  }

  const confirm = () => {
    const body = {
      container: containerName, port: Number(port.port), protocol: String(port.protocol || 'tcp').toLowerCase(),
      enabled: true, externalPort: externalNumber, scheme, confirm: confirmText(containerName, port.port, port.protocol),
    };
    if (onChoose) {
      onChoose(body);
      return;
    }
    setBusy(true);
    setError('');
    API.config.upnpAppPort(body)
      .then((res) => { setRouterStatus(res.data); onClose(); })
      .catch((e) => { setError(e.message || String(e)); setBusy(false); });
  };

  return <Dialog open onClose={() => { if (!busy) onClose(); }} fullWidth maxWidth="sm" data-router-dialog>
    <DialogTitle sx={{ overflowWrap: 'anywhere' }}>
      {t('mgmt.router.dialog.title', 'Open port {{port}} ({{protocol}}) of {{name}} on the router?',
        { port: port.port, protocol: proto, name: containerName })}
    </DialogTitle>
    <DialogContent>
      <Stack spacing={2}>
        <WhatOpeningMeans />
        {web.web && web.route && <Alert severity="info" data-router-web sx={{ overflowWrap: 'anywhere' }}>
          {t('mgmt.router.dialog.webRoute',
            'This port is the app\'s web interface, and the app already has an address through Cosmos: {{address}}. That address is the safer way and needs no extra port. Open the port only if something has to reach the app directly.',
            { address: web.route.UseHost && web.route.Host ? shownAddress(web.route.Host, ports, 'https') : web.route.Name })}
        </Alert>}
        {web.web && !web.route && <Alert severity="info" data-router-web sx={{ overflowWrap: 'anywhere' }}>
          {t('mgmt.router.dialog.webGuess',
            'Port {{port}} looks like a web interface. For a web page, give the app a URL instead ("New" under URLs): it goes through Cosmos\' one HTTPS port, with login and SmartShield, and needs no extra port.',
            { port: port.port })}
        </Alert>}
        {!web.web && <Typography variant="body2">
          {t('mgmt.router.dialog.purpose',
            'Opening a port is for what is not a web page or has to be reached directly: peer ports, game servers, media clients. For a web interface the app\'s URL through Cosmos is the safer way.')}
        </Typography>}

        <TextField
          select
          size="small"
          label={t('mgmt.router.dialog.schemeLabel', 'What is on this port?')}
          value={scheme}
          onChange={(event) => setScheme(event.target.value)}
          helperText={web.web
            ? (web.route
              ? t('mgmt.router.dialog.schemeFromRoute', 'Proposed from the app\'s URL {{name}}.', { name: web.route.Name })
              : t('mgmt.router.dialog.schemeFromPort', 'Proposed from the port number: change it if it is wrong.'))
            : t('mgmt.router.dialog.schemeHelp', 'A web page gets a link; anything else is shown as an address to copy.')}
        >
          <MenuItem value="">{t('mgmt.router.dialog.schemeNone', 'Not a web page')}</MenuItem>
          <MenuItem value="http">{t('mgmt.router.dialog.schemeHttp', 'A web page (http)')}</MenuItem>
          <MenuItem value="https">{t('mgmt.router.dialog.schemeHttps', 'A web page (https)')}</MenuItem>
        </TextField>

        <TextField
          size="small"
          label={t('mgmt.router.dialog.externalLabel', 'Port on the router (optional)')}
          value={external}
          placeholder={String(port.hostPort || '')}
          inputProps={{ inputMode: 'numeric', 'aria-label': t('mgmt.router.dialog.externalLabel', 'Port on the router (optional)') }}
          error={Boolean(externalError)}
          onChange={(event) => setExternal(event.target.value)}
          helperText={externalError || t('mgmt.router.dialog.externalHelp',
            'Empty: {{port}}, the port the container publishes. If the router has it taken, Cosmos uses the next free port above it and says so; it never takes a port from another device.',
            { port: port.hostPort || port.port })}
        />

        <Typography variant="caption" color="textSecondary">
          {t('mgmt.router.dialog.limits',
            'Cosmos asks the router over UPnP. It cannot see forwards entered in the router\'s own pages: such a port may be refused, or accepted without effect. Only IPv4 is forwarded; over IPv6 nothing is opened.')}
        </Typography>
        {error && <Alert severity="error" sx={{ overflowWrap: 'anywhere' }}>{error}</Alert>}
      </Stack>
    </DialogContent>
    <DialogActions sx={{ flexWrap: 'wrap' }}>
      <Button onClick={onClose} disabled={busy}>{t('global.cancelAction')}</Button>
      <LoadingButton loading={busy} disabled={Boolean(externalError)} color="warning" variant="contained" onClick={confirm} data-router-confirm>
        {t('mgmt.router.dialog.confirm', 'Open port {{port}} of {{name}}', { port: port.port, name: containerName })}
      </LoadingButton>
    </DialogActions>
  </Dialog>;
};

// The address of a forwarded port: a link when an admin said it is a web
// page, otherwise text with a button that copies it.
const PortAddress = ({ mapping, host }) => {
  const { t } = useTranslation();
  const where = portAddress(mapping, host);
  if (!where.address) {
    return null;
  }
  const copy = () => {
    const done = () => notify(t('mgmt.router.copied', 'Copied: {{address}}', { address: where.address }), 'success');
    try {
      navigator.clipboard.writeText(where.address).then(done).catch(() => {});
    } catch (e) {
      // no clipboard in this browser or page: the address is on the screen
    }
  };
  return <Typography variant="body2" component="div" sx={{ overflowWrap: 'anywhere' }} data-router-address>
    <GlobalOutlined />{' '}
    {where.href
      ? <Link href={where.href} target="_blank" rel="noopener noreferrer">{where.href}</Link>
      : <>
        {where.known ? where.address : t('mgmt.router.addressUnknown', 'port {{port}} of your public address', { port: mapping.externalPort })}
        {where.known && <Tooltip title={t('mgmt.router.copy', 'Copy the address')}>
          <Button size="small" onClick={copy} sx={{ minWidth: 0, ml: 0.5 }} aria-label={t('mgmt.router.copy', 'Copy the address')}><CopyOutlined /></Button>
        </Tooltip>}
      </>}
  </Typography>;
};

// The line of one published port on the container's page. `port` is an entry
// of publishedPorts (utils/reachability.js); `fallback` what the page says
// about a port Cosmos was not asked to open.
export const RouterPortLine = ({ config, containerName, port, fallback }) => {
  const { t } = useTranslation();
  const say = useSentence();
  const status = useRouterStatus();
  const [dialog, setDialog] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const routes = getContainersRoutes(config, containerName);
  const mapping = findAppMapping(status, containerName, port.privatePort, port.type);
  const note = portNote(mapping);
  const label = t('mgmt.router.switch', 'Open port {{port}} on the router (UPnP)', { port: port.port });

  const close = () => {
    setBusy(true);
    API.config.upnpAppPort({ container: containerName, port: Number(port.privatePort), protocol: port.type, enabled: false })
      .then((res) => setRouterStatus(res.data))
      .catch(() => {})
      .finally(() => setBusy(false));
  };

  const Icon = !mapping ? QuestionCircleOutlined : mapping.active ? CheckCircleOutlined : mapping.state === 'failed' ? CloseCircleOutlined : QuestionCircleOutlined;
  return <Stack spacing={0.5} style={{ minWidth: 0 }} data-router-port={portKey(containerName, port.privatePort, port.type)}>
    <Stack direction="row" spacing={1} alignItems="flex-start" style={{ minWidth: 0 }}>
      <Icon style={{ marginTop: 3, flexShrink: 0 }} />
      <Typography variant="body2" style={{ overflowWrap: 'anywhere', minWidth: 0 }}>
        {mapping
          ? <>{t('mgmt.router.portLead', 'Port {{port}} ({{type}}):', { port: port.port, type: String(port.type).toUpperCase() })} {say(mappingSentence(mapping))}</>
          : fallback}
      </Typography>
    </Stack>
    {mapping && mapping.active && <div style={{ paddingLeft: 22 }}>
      <PortAddress mapping={mapping} host={publicHost({ routes, config, status })} />
      {note && <Typography variant="caption" color="textSecondary" component="div" data-router-note>{say(note)}</Typography>}
    </div>}
    {status && <div style={{ paddingLeft: 22 }}>
      <PermissionGuard permission={PERM_ADMIN}>
        <FormControlLabel
          sx={{ mr: 0 }}
          control={<Switch
            size="small"
            checked={Boolean(mapping)}
            disabled={busy}
            inputProps={{ 'aria-label': label }}
            onChange={(event) => (event.target.checked ? setDialog(true) : close())}
          />}
          label={<Typography variant="body2">{label}</Typography>}
        />
      </PermissionGuard>
    </div>}
    {dialog && <OpenPortDialog
      containerName={containerName}
      port={{ port: port.privatePort, protocol: port.type, hostPort: port.port }}
      routes={routes}
      config={config}
      onClose={() => setDialog(false)}
    />}
  </Stack>;
};

// The forwarded ports of an app (or of the containers of a stack) as chips:
// external addresses beside the app's URLs. With `rows` they get a line of
// their own, headed like the groups of AppLinks.
export const RouterPortLinks = ({ config, containerNames, chipStyle, rows }) => {
  const { t } = useTranslation();
  const say = useSentence();
  const status = useRouterStatus();
  const mappings = appMappingsOf(status, containerNames).filter((mapping) => mapping.active);
  if (mappings.length === 0) {
    return null;
  }
  const label = t('mgmt.router.links.group', 'Internet, by port (opened on the router)');
  const chips = mappings.map((mapping) => {
    const host = publicHost({ routes: getContainersRoutes(config, mapping.container), config, status });
    const where = portAddress(mapping, host);
    const note = portNote(mapping);
    const tip = say(mappingSentence(mapping)) + (note ? ' ' + say(note) : '');
    const link = where.href ? { component: 'a', href: where.href, target: '_blank', rel: 'noopener noreferrer', clickable: true } : { tabIndex: 0 };
    return <Tooltip key={portKey(mapping.container, mapping.containerPort, mapping.protocol)} title={tip}>
      <Chip
        icon={<GlobalOutlined />}
        label={(where.known ? where.address : ':' + mapping.externalPort) + (mapping.protocol === 'UDP' ? '/udp' : '')}
        color="primary"
        variant="outlined"
        style={{ maxWidth: '100%', ...chipStyle }}
        data-router-chip
        {...link}
      />
    </Tooltip>;
  });
  if (rows) {
    return <div>
      <Typography variant="caption" color="textSecondary" component="div" style={chipStyle}>
        <GlobalOutlined /> {label}
      </Typography>
      {chips}
    </div>;
  }
  return <>{chips}</>;
};

const parseService = (text, fallback) => {
  try {
    const parsed = JSON.parse(text);
    return parsed && typeof parsed === 'object' ? parsed : fallback;
  } catch (e) {
    return fallback;
  }
};

// The optional step of an installation: the ports the definition publishes,
// each with the switch, off. What is chosen is opened once `done` says the
// containers exist. `definition` is the text that is about to be sent.
export const InstallRouterPorts = ({ service, definition, done }) => {
  const { t } = useTranslation();
  const status = useRouterStatus();
  const [chosen, setChosen] = React.useState({});
  const [dialog, setDialog] = React.useState(null);
  const [results, setResults] = React.useState(null);
  const sent = React.useRef(false);
  const ports = installPorts(parseService(definition, service));

  React.useEffect(() => {
    if (!done || sent.current) {
      return;
    }
    sent.current = true;
    const wanted = ports.map((port) => chosen[portKey(port.container, port.port, port.protocol)]).filter(Boolean);
    if (wanted.length === 0) {
      return;
    }
    (async () => {
      const outcome = [];
      for (const body of wanted) {
        try {
          const res = await API.config.upnpAppPort(body);
          setRouterStatus(res.data);
          outcome.push({ body, mapping: findAppMapping(res.data, body.container, body.port, body.protocol) });
        } catch (e) {
          outcome.push({ body, error: e.message || String(e) });
        }
        setResults([...outcome]);
      }
    })();
  }, [done]);

  if (ports.length === 0 || !status) {
    return null;
  }
  const count = Object.values(chosen).filter(Boolean).length;

  return <PermissionGuard permission={PERM_ADMIN}>
    <Stack spacing={1} data-router-install sx={{ overflowWrap: 'anywhere' }}>
      <Typography variant="subtitle2">
        {t('mgmt.router.install.title', 'Open a port on the router as well? (optional)')}
      </Typography>
      <Typography variant="caption" color="textSecondary">
        {t('mgmt.router.install.text',
          'The app publishes these ports on the server. Each can also be opened on your router (UPnP), so it is reached from the internet directly. Nothing is opened unless you tick it. A web interface needs none: the app\'s URL goes through Cosmos.')}
      </Typography>
      {ports.map((port) => {
        const key = portKey(port.container, port.port, port.protocol);
        return <FormControlLabel
          key={key}
          sx={{ mr: 0 }}
          control={<Checkbox
            size="small"
            checked={Boolean(chosen[key])}
            disabled={done}
            onChange={(event) => (event.target.checked ? setDialog(port) : setChosen({ ...chosen, [key]: null }))}
          />}
          label={<Typography variant="body2">
            {t('mgmt.router.install.port', 'Port {{port}} ({{protocol}}) of {{name}}', { port: port.hostPort, protocol: port.protocol.toUpperCase(), name: port.container })}
          </Typography>}
        />;
      })}
      {count > 0 && !done && <Alert severity="warning">
        {t('mgmt.router.install.pending', 'What is ticked is opened on the router once the app is created, and is then reachable from the internet directly, without Cosmos\' login.')}
      </Alert>}
      {results && results.map((result) => {
        const key = portKey(result.body.container, result.body.port, result.body.protocol);
        const ok = result.mapping && result.mapping.active;
        return <Alert key={key} severity={ok ? 'success' : 'error'} data-router-install-result>
          {ok
            ? t('mgmt.router.install.opened', 'Port {{port}} of {{name}}: the router forwards port {{external}}.',
              { port: result.body.port, name: result.body.container, external: result.mapping.externalPort })
            : t('mgmt.router.install.failed', 'Port {{port}} of {{name}} was not opened: {{error}}',
              { port: result.body.port, name: result.body.container, error: result.error || (result.mapping && result.mapping.error) || '' })}
        </Alert>;
      })}
      {dialog && <OpenPortDialog
        containerName={dialog.container}
        port={{ port: dialog.port, protocol: dialog.protocol, hostPort: dialog.hostPort }}
        routes={[]}
        onClose={() => setDialog(null)}
        onChoose={(body) => {
          setChosen({ ...chosen, [portKey(dialog.container, dialog.port, dialog.protocol)]: body });
          setDialog(null);
        }}
      />}
    </Stack>
  </PermissionGuard>;
};

const sinceText = (since) => {
  const date = since ? new Date(since) : null;
  return date && !isNaN(date) ? date.toLocaleString() : '';
};

// Everything open on the router, in the "Internet access" card: what Cosmos
// holds (its own ports and the apps'), each with "Close", and what else the
// router lists, read only. `status` is the card's own copy; `onStatus`
// receives the answer of a command; `formik` is the settings form, whose
// fields follow when one of Cosmos' own ports is closed here.
export const MappingsOverview = ({ status, onStatus, formik }) => {
  const { t } = useTranslation();
  const say = useSentence();
  const [busy, setBusy] = React.useState('');
  const [confirmOwn, setConfirmOwn] = React.useState(null);
  if (!status) {
    return null;
  }
  const rows = overviewRows(status);
  const foreign = status.foreign || [];
  const done = (res) => { onStatus(res.data); setRouterStatus(res.data); };

  const closeApp = (mapping) => {
    setBusy(mapping.name);
    API.config.upnpAppPort({ container: mapping.container, port: mapping.containerPort, protocol: mapping.protocol.toLowerCase(), enabled: false })
      .then(done).catch(() => {}).finally(() => setBusy(''));
  };
  const closeOwn = (mapping) => {
    setConfirmOwn(null);
    setBusy(mapping.name);
    API.config.upnpClose(mapping.name)
      .then((res) => {
        done(res);
        // the form on this page still has the switch on: a later save must not open the port again
        formik && formik.setFieldValue(mapping.name === 'HTTP' ? 'UPnP_ForwardHTTP' : 'UPnP_Enabled', false);
      })
      .catch(() => {}).finally(() => setBusy(''));
  };

  return <Stack spacing={1} data-router-overview>
    <Typography variant="subtitle1">{t('mgmt.router.overview.title', 'Ports open on the router')}</Typography>
    {rows.length === 0 && <Typography variant="body2" color="text.secondary">
      {t('mgmt.router.overview.none', 'Cosmos holds no port open on the router.')}
    </Typography>}
    {rows.map((m) => {
      const own = m.kind !== 'app';
      const note = portNote(m);
      return <Stack key={m.kind + m.name} spacing={0.5} data-router-row={m.name}>
        <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap" useFlexGap>
          <Chip
            size="small"
            color={m.active ? 'success' : m.state === 'failed' ? 'error' : 'default'}
            icon={m.active ? <CheckCircleOutlined /> : m.state === 'failed' ? <CloseCircleOutlined /> : <QuestionCircleOutlined />}
            label={own ? 'Cosmos ' + m.name : m.name}
            sx={{ maxWidth: '100%' }}
          />
          <Typography variant="body2" sx={{ overflowWrap: 'anywhere', minWidth: 0 }}>
            {m.active
              ? t('mgmt.router.overview.forwarded', '{{protocol}} port {{external}} on the router goes to port {{internal}} here', { protocol: m.protocol, external: m.externalPort, internal: m.internalPort })
              : own
                ? ((!m.holder && m.error) || t('mgmt.config.internet.status.notForwarded', 'not forwarded'))
                : say(mappingSentence(m))}
            {m.active && m.since ? ' · ' + t('mgmt.router.overview.since', 'since {{time}}', { time: sinceText(m.since) }) : ''}
          </Typography>
          <PermissionGuard permission={PERM_ADMIN}>
            <LoadingButton size="small" variant="outlined" loading={busy === m.name} onClick={() => (own ? setConfirmOwn(m) : closeApp(m))} data-router-close>
              {m.active ? t('mgmt.router.overview.close', 'Close') : t('mgmt.router.overview.remove', 'Remove')}
            </LoadingButton>
          </PermissionGuard>
        </Stack>
        {note && <Typography variant="caption" color="textSecondary" data-router-note>{say(note)}</Typography>}
        {m.paused && <Typography variant="caption" color="textSecondary">
          {t('mgmt.router.overview.paused', 'Cosmos does not try this one again by itself: press "Check now". The other ports are renewed as usual.')}
        </Typography>}
        {own && !m.active && m.holder && <Typography variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
          {t('mgmt.router.overview.holder', 'Port {{port}} is used by {{holder}}: another device on your network. Cosmos does not take it.', { port: m.wantedPort, holder: m.holder })}
        </Typography>}
        {own && !m.active && m.suggested > 0 && <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap" useFlexGap>
          <Typography variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere', minWidth: 0 }}>
            {t('mgmt.router.overview.suggest',
              'Port {{port}} looks free on the router. Cosmos does not move its own port by itself: another external port changes every external address of your apps, and behind Cloudflare only 443, 2053, 2083, 2087, 2096 and 8443 work.',
              { port: m.suggested })}
          </Typography>
          {formik && <Button size="small" variant="outlined" data-router-suggest onClick={() => {
            formik.setFieldValue(m.name === 'HTTP' ? 'UPnP_ExternalHTTPPort' : 'UPnP_ExternalHTTPSPort', String(m.suggested));
            notify(t('mgmt.router.overview.suggestTaken', 'Port {{port}} is in the form: press Save to use it.', { port: m.suggested }), 'info');
          }}>
            {t('mgmt.router.overview.useSuggested', 'Use port {{port}}', { port: m.suggested })}
          </Button>}
        </Stack>}
      </Stack>;
    })}

    <Typography variant="subtitle1" sx={{ pt: 1 }}>{t('mgmt.router.foreign.title', 'Other ports open on the router (not Cosmos\')')}</Typography>
    {!status.gatewayIP && <Typography variant="body2" color="text.secondary">
      {t('mgmt.router.foreign.notAsked', 'Cosmos has not talked to the router: it does so when a port is to be opened, or on "Check now" while something is switched on.')}
    </Typography>}
    {status.gatewayIP && !status.foreignListed && <Typography variant="body2" color="text.secondary">
      {t('mgmt.router.foreign.notListed', 'This router does not let its port mappings be listed.')}
    </Typography>}
    {status.gatewayIP && status.foreignListed && foreign.length === 0 && <Typography variant="body2" color="text.secondary">
      {t('mgmt.router.foreign.none', 'The router lists no other port mapping.')}
    </Typography>}
    {foreign.map((f) => <Typography key={f.protocol + f.externalPort} variant="body2" sx={{ overflowWrap: 'anywhere' }} data-router-foreign>
      {f.thisServer
        ? t('mgmt.router.foreign.rowHere', '{{protocol}} port {{external}} goes to port {{internal}} of this server, opened by another program here', { protocol: f.protocol, external: f.externalPort, internal: f.internalPort })
        : t('mgmt.router.foreign.row', '{{protocol}} port {{external}} goes to {{client}}, port {{internal}}: another device on your network', { protocol: f.protocol, external: f.externalPort, client: f.internalClient, internal: f.internalPort })}
      {f.description ? ' · "' + f.description + '"' : ''}
      {leaseText(f.leaseSeconds) ? ' · ' + t('mgmt.router.foreign.lease', 'for another {{time}}', { time: leaseText(f.leaseSeconds) }) : ''}
    </Typography>)}
    {status.foreignTruncated && <Typography variant="caption" color="textSecondary">
      {t('mgmt.router.foreign.truncated', 'The list is longer than what Cosmos reads (128 entries).')}
    </Typography>}
    <Typography variant="caption" color="text.secondary">
      {t('mgmt.router.foreign.limits',
        'This is what the router shows over UPnP. Forwards entered in the router\'s own pages are often not in it, and Cosmos cannot see them: asked for such a port, a router may refuse, or accept without any effect. Cosmos never changes or removes a mapping it did not create. Only IPv4 is forwarded this way.')}
    </Typography>

    {confirmOwn && <Dialog open onClose={() => setConfirmOwn(null)} fullWidth maxWidth="xs">
      <DialogTitle>{t('mgmt.router.overview.closeOwnTitle', 'Close Cosmos\' {{name}} port on the router?', { name: confirmOwn.name })}</DialogTitle>
      <DialogContent>
        <Typography variant="body2">
          {confirmOwn.name === 'HTTP'
            ? t('mgmt.router.overview.closeOwnHttp', 'The router stops forwarding the HTTP port and "Also forward the HTTP port" is switched off in the settings.')
            : t('mgmt.router.overview.closeOwnHttps', 'Automatic port forwarding for Cosmos is switched off in the settings: Cosmos and every app address under your domains can no longer be reached from the internet, unless the router forwards the port by other means. Ports of apps stay as they are.')}
        </Typography>
      </DialogContent>
      <DialogActions>
        <Button onClick={() => setConfirmOwn(null)}>{t('global.cancelAction')}</Button>
        <Button color="warning" variant="contained" onClick={() => closeOwn(confirmOwn)} data-router-close-own>
          {t('mgmt.router.overview.closeOwnConfirm', 'Close the {{name}} port', { name: confirmOwn.name })}
        </Button>
      </DialogActions>
    </Dialog>}
  </Stack>;
};

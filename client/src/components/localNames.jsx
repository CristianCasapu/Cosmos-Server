// Fork addition: the truth about .local addresses, wherever the panel shows
// or proposes one (src/proxy/fork_mdns*.go on the server).
//
//   - useLocalNames: the server's state, read once for the page and kept 15 s
//     (admins only: it names interfaces and addresses).
//   - LocalNameState: one line on a name: published, answered, taken by
//     another device, Avahi missing.
//   - LocalNamesHelp: "Which devices can open it?" and the dialog that says
//     which devices resolve .local names, what to use on the others and how
//     to make the certificate warning go away.
//   - LocalNamesSettings: the block of Configuration > HTTP.
//
// The sentences are in utils/localNames.js, tested on their own.
import * as React from 'react';
import {
  Alert,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Link,
  Stack,
  Typography,
} from '@mui/material';
import { LoadingButton } from '@mui/lab';
import {
  CheckCircleOutlined,
  CloseCircleOutlined,
  ExclamationCircleOutlined,
  InfoCircleOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import { useTheme } from '@mui/material/styles';
import { useTranslation } from 'react-i18next';
import { Link as RouterLink } from 'react-router-dom';
import * as API from '../api';
import { useClientInfos } from '../utils/hooks';
import { PERM_ADMIN_READ } from '../utils/permissions';
import { CosmosInputText } from '../pages/config/users/formShortcuts';
import { DEVICES, avahiAdvice, isLocalName, lanAddress, nameState, summary } from '../utils/localNames';

// --- one state for the whole page
let store = { data: null, at: 0, loading: false };
const listeners = new Set();
const emit = () => listeners.forEach((listener) => listener({ ...store }));

export const loadLocalNames = (force) => {
  if (store.loading || (!force && Date.now() - store.at < 15000)) {
    return Promise.resolve(store.data);
  }
  store = { ...store, loading: true };
  const call = force ? API.config.localNamesCheck() : API.config.localNames();
  return call.then((res) => {
    store = { data: (res && res.data) || store.data, at: Date.now(), loading: false };
    emit();
    return store.data;
  }).catch(() => {
    store = { ...store, at: Date.now(), loading: false };
    emit();
    return store.data;
  });
};

export const useLocalNames = () => {
  const { hasPermission } = useClientInfos();
  const allowed = hasPermission(PERM_ADMIN_READ);
  const [state, setState] = React.useState({ ...store });
  React.useEffect(() => {
    if (!allowed) return undefined;
    listeners.add(setState);
    loadLocalNames(false);
    setState({ ...store });
    return () => { listeners.delete(setState); };
  }, [allowed]);
  return allowed ? state.data : null;
};

const LevelIcon = ({ level }) => {
  const theme = useTheme();
  const style = { marginTop: 3, flexShrink: 0 };
  if (level === 'success') return <CheckCircleOutlined style={{ ...style, color: theme.palette.success.main }} />;
  if (level === 'error') return <CloseCircleOutlined style={{ ...style, color: theme.palette.error.main }} />;
  if (level === 'warning') return <ExclamationCircleOutlined style={{ ...style, color: theme.palette.warning.main }} />;
  return <InfoCircleOutlined style={style} />;
};

const say = (t, text) => (text ? t(text[0], text[1], text[2] || {}) : '');

// One state as a short line with its icon.
const StateLine = ({ state, children }) => {
  const { t } = useTranslation();
  if (!state) return null;
  return <Stack direction="row" spacing={1} alignItems="flex-start">
    <LevelIcon level={state.level} />
    <Typography variant="caption" color="textSecondary" component="div" sx={{ overflowWrap: 'anywhere', minWidth: 0 }}>
      {say(t, state.text)}
      {(state.extra || []).map((text) => <React.Fragment key={text[0]}> {say(t, text)}</React.Fragment>)}
      {children}
    </Typography>
  </Stack>;
};

// The state of one .local name; nothing for another kind of address or for
// someone who may not read it. `help` adds the "which devices" link.
export const LocalNameState = ({ host, help }) => {
  const status = useLocalNames();
  if (!status || !isLocalName(host)) return null;
  return <StateLine state={nameState(status, host)}>
    {help && <> <LocalNamesHelp /></>}
  </StateLine>;
};

// The states of some names as one plain sentence (a tooltip), '' when there
// is nothing to say.
export const useLocalNamesTitle = (hosts) => {
  const { t } = useTranslation();
  const status = useLocalNames();
  if (!status) return '';
  return (hosts || []).filter(isLocalName).map((host) => {
    const state = nameState(status, host);
    return host + ': ' + say(t, state.text) + (state.extra || []).map((text) => ' ' + say(t, text)).join('');
  }).join(' · ');
};

// The state of every name at once (the bulk dialog).
export const LocalNamesSummary = ({ help }) => {
  const status = useLocalNames();
  if (!status) return null;
  return <StateLine state={summary(status)}>
    {help && <> <LocalNamesHelp /></>}
  </StateLine>;
};

// Which devices can use the names, the alternatives, the certificate.
export const LocalNamesNote = ({ status }) => {
  const { t } = useTranslation();
  const ip = lanAddress(status) || t('mgmt.localNames.note.theIP', 'the server\'s IP address');
  const port = (status && status.httpsPort) || '';
  return <Stack spacing={1.5}>
    <Typography variant="body2">
      {t('mgmt.localNames.note.intro', 'A .local address is not looked up in DNS: the device asks the local network (mDNS), and Cosmos, through Avahi, answers with the server\'s address. So it works on the home network only, and only on devices that ask the network:')}
    </Typography>
    <Stack component="ul" spacing={0.5} sx={{ m: 0, pl: 2.5 }}>
      {DEVICES.map((device) => <Typography component="li" variant="body2" key={device.id}>{say(t, device.text)}</Typography>)}
    </Stack>
    <Typography variant="body2">
      {t('mgmt.localNames.note.otherwise', 'Where it does not work:')}
    </Typography>
    <Stack component="ul" spacing={0.5} sx={{ m: 0, pl: 2.5 }}>
      <Typography component="li" variant="body2">
        {port
          ? t('mgmt.localNames.note.ip', 'The server\'s address, {{ip}}: https://{{ip}}:{{port}} opens the panel, and an app can have an address of its own made of the server\'s address and a port (the last proposal of its URL form).', { ip, port })
          : t('mgmt.localNames.note.ipNoPort', 'The server\'s address, {{ip}}: an app can have an address of its own made of the server\'s address and a port (the last proposal of its URL form).', { ip })}
      </Typography>
      <Typography component="li" variant="body2">
        {t('mgmt.localNames.note.domain', 'Its internet address, when a domain is attached to the app (Configuration > Domains): it works everywhere and has a certificate every device trusts.')}
      </Typography>
      <Typography component="li" variant="body2">
        {t('mgmt.localNames.note.router', 'A DNS entry on your router or Pi-hole that points a name at {{ip}}: every device on the network then resolves it. Pick a name that does not end in .local (for example app.home.arpa) and add it to the app\'s URL as well: devices send .local names to mDNS only.', { ip })}
      </Typography>
    </Stack>
    <Typography variant="body2">
      {t('mgmt.localNames.note.certificate', 'The browser warns about the certificate the first time: no public authority certifies a .local name, so Cosmos signs these addresses with its own authority. Every .local address of an app is on that certificate. Install the authority once on each device and the warning is gone for good: ')}
      <Link component={RouterLink} to="/cosmos-ui/trust">{t('mgmt.localNames.note.trustLink', 'install Cosmos\' certificate authority')}</Link>
      {t('mgmt.localNames.note.certificateApps', '. Apps on Android, smart TVs and many apps on phones ignore an authority added by hand; use the internet address there.')}
    </Typography>
  </Stack>;
};

// The link and the dialog with the note.
export const LocalNamesHelp = () => {
  const { t } = useTranslation();
  const status = useLocalNames();
  const [open, setOpen] = React.useState(false);
  return <>
    <Link component="button" type="button" variant="caption" onClick={(event) => { event.preventDefault(); event.stopPropagation(); setOpen(true); }} sx={{ verticalAlign: 'baseline' }}>
      {t('mgmt.localNames.help', 'Which devices can open it?')}
    </Link>
    <Dialog open={open} onClose={() => setOpen(false)} maxWidth="sm" fullWidth>
      <DialogTitle>{t('mgmt.localNames.note.title', '.local addresses')}</DialogTitle>
      <DialogContent>
        <LocalNamesNote status={status} />
      </DialogContent>
      <DialogActions>
        <Button onClick={() => setOpen(false)}>{t('global.close', 'Close')}</Button>
      </DialogActions>
    </Dialog>
  </>;
};

const problemText = (t, problem) => ({
  notFound: t('mgmt.localNames.problem.notFound', '{{name}}: there is no such interface on the server.', { name: problem.name }),
  down: t('mgmt.localNames.problem.down', '{{name}}: the interface is down.', { name: problem.name }),
  noIPv4: t('mgmt.localNames.problem.noIPv4', '{{name}}: the interface has no IPv4 address.', { name: problem.name }),
}[problem.code] || null);

// Configuration > HTTP: the state, the interfaces, the note. `formik` holds
// MDNSInterfaces as text (names separated by commas).
export const LocalNamesSettings = ({ formik }) => {
  const { t } = useTranslation();
  const status = useLocalNames();
  const [checking, setChecking] = React.useState(false);
  if (!formik.values.PublishMDNS) return null;

  const check = () => {
    setChecking(true);
    loadLocalNames(true).finally(() => setChecking(false));
  };
  const advice = avahiAdvice(status);
  const problems = ((status && status.problems) || []).map((problem) => problemText(t, problem)).filter(Boolean);
  const state = summary(status);

  return <Stack spacing={1.5} sx={{ mt: 1.5 }}>
    {status && <Alert severity={state ? state.level : 'info'} sx={{ overflowWrap: 'anywhere', '& .MuiAlert-message': { minWidth: 0 } }}>
      <Stack spacing={0.5} alignItems="flex-start">
        <span>{say(t, state && state.text)}</span>
        {status.hostName && <span>{t('mgmt.localNames.hostNameIs', 'Avahi\'s name for this server: {{name}}.', { name: status.hostName })}</span>}
        {problems.map((text) => <span key={text}>{text}</span>)}
        {status.checkedAt && !String(status.checkedAt).startsWith('0001') && <span>{t('mgmt.localNames.checkedAt', 'Asked on the network at {{time}}.', { time: new Date(status.checkedAt).toLocaleTimeString() })}</span>}
        {status.enabled && status.avahi === 'running' && <LoadingButton color="inherit" size="small" variant="outlined" loading={checking} startIcon={<ReloadOutlined />} onClick={check} sx={{ mt: 0.5 }}>
          {t('mgmt.localNames.checkAgain', 'Check again')}
        </LoadingButton>}
      </Stack>
    </Alert>}
    {advice && <Alert severity="info" sx={{ overflowWrap: 'anywhere', '& .MuiAlert-message': { minWidth: 0 } }}>{say(t, advice)}</Alert>}
    <CosmosInputText
      label={t('mgmt.localNames.interfaces', 'Network interfaces for the .local names')}
      name="MDNSInterfaces"
      formik={formik}
    />
    <Typography variant="caption" color="textSecondary">
      {t('mgmt.localNames.interfacesHelp', 'Leave empty to use the interfaces facing the local network, chosen automatically (Docker\'s networks, VPNs and Tailscale never are). Otherwise names as the server spells them, separated by commas (for example eno1, wlan0).')}
    </Typography>
    <LocalNamesNote status={status} />
  </Stack>;
};

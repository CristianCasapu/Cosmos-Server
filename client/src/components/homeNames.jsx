// Fork addition: names for use at home under the owner's domain
// (<app>.<label>.<domain> at the server's private address, see
// utils/homeNames.js for the sentences and src/cloudflare/homenames.go).
//
//   - useHomeNames: the server's state, read once for the page and kept 15 s
//     (whoever may read the configuration).
//   - HomeNamesCard: the switch per domain on the Domains tab, the cost, the
//     state, the checks and the help for routers that drop the answers.
//   - useHomeRoutes: an app's home names as routes for its links.
import * as React from 'react';
import {
  Alert,
  Button,
  Collapse,
  Divider,
  Stack,
  Typography,
} from '@mui/material';
import { LoadingButton } from '@mui/lab';
import {
  CheckCircleOutlined,
  CloseCircleOutlined,
  DownOutlined,
  ExclamationCircleOutlined,
  HomeOutlined,
  InfoCircleOutlined,
  UpOutlined,
} from '@ant-design/icons';
import { useTheme } from '@mui/material/styles';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import MainCard from './MainCard';
import PermissionGuard from './permissionGuard';
import { FormField } from './formField';
import { useClientInfos } from '../utils/hooks';
import { PERM_CONFIGURATION, PERM_CONFIGURATION_READ } from '../utils/permissions';
import { serverBack } from '../utils/appLinks';
import {
  DEFAULT_LABEL,
  REBIND_HELP,
  browserCheckText,
  certificateState,
  cleanLabel,
  exampleName,
  homeRoutesFor,
  probeURL,
  problemText,
  rebindDomain,
  recordState,
  serverCheckText,
  validLabel,
} from '../utils/homeNames';

// --- one state for the whole page
let store = { data: null, at: 0, loading: false };
const listeners = new Set();
const emit = () => listeners.forEach((listener) => listener({ ...store }));

export const loadHomeNames = (force) => {
  if (store.loading || (!force && Date.now() - store.at < 15000)) {
    return Promise.resolve(store.data);
  }
  store = { ...store, loading: true };
  return API.config.homeNames().then((res) => {
    store = { data: (res && res.ok && res.data) || store.data, at: Date.now(), loading: false };
    emit();
    return store.data;
  }).catch(() => {
    store = { ...store, at: Date.now(), loading: false };
    emit();
    return store.data;
  });
};

export const useHomeNames = () => {
  const { hasPermission } = useClientInfos();
  const allowed = hasPermission(PERM_CONFIGURATION_READ);
  const [state, setState] = React.useState({ ...store });
  React.useEffect(() => {
    if (!allowed) return undefined;
    listeners.add(setState);
    loadHomeNames(false);
    setState({ ...store });
    return () => { listeners.delete(setState); };
  }, [allowed]);
  return allowed ? state.data : null;
};

// An app's home names as routes (the home name as the host), for its links.
export const useHomeRoutes = (routes) => {
  const status = useHomeNames();
  return React.useMemo(() => homeRoutesFor(status, routes), [status, routes]);
};

const say = (t, text) => (text ? t(text[0], text[1], text[2] || {}) : '');

const LevelIcon = ({ level }) => {
  const theme = useTheme();
  const style = { marginTop: 3, flexShrink: 0 };
  if (level === 'success') return <CheckCircleOutlined style={{ ...style, color: theme.palette.success.main }} />;
  if (level === 'error') return <CloseCircleOutlined style={{ ...style, color: theme.palette.error.main }} />;
  if (level === 'warning') return <ExclamationCircleOutlined style={{ ...style, color: theme.palette.warning.main }} />;
  return <InfoCircleOutlined style={style} />;
};

const StateLine = ({ state }) => {
  const { t } = useTranslation();
  if (!state) return null;
  return <Stack direction="row" spacing={1} alignItems="flex-start">
    <LevelIcon level={state[0]} />
    <Typography variant="body2" color="textSecondary" component="div" sx={{ overflowWrap: 'anywhere', minWidth: 0 }}>
      {say(t, state[1])}
    </Typography>
  </Stack>;
};

// A small text button that opens a section under it.
const Door = ({ open, onClick, children }) => <Button
  size="small"
  onClick={onClick}
  endIcon={open ? <UpOutlined /> : <DownOutlined />}
  sx={{ alignSelf: 'flex-start', textAlign: 'left' }}
  aria-expanded={open}
>
  {children}
</Button>;

// What the names cost and how to make a router let them through.
const RebindHelp = ({ row }) => {
  const { t } = useTranslation();
  const domain = rebindDomain(row) || DEFAULT_LABEL + '.' + row.zone;
  return <Stack spacing={1} data-home-rebind>
    <Typography variant="body2" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
      {t('mgmt.homeNames.rebind.recognise',
        'How to recognise it: the name works on a phone on mobile data (or with "nslookup {{name}} 1.1.1.1" on a computer) but not on the home network. Then the router or the DNS resolver at home drops answers that carry a private address ("DNS rebinding protection"). Let {{domain}} through:',
        { name: row.panel || exampleName(row.label, row.zone, 'cosmos'), domain })}
    </Typography>
    {REBIND_HELP.map(([id, name, text]) => <Typography key={id} variant="body2" color="textSecondary" component="div" sx={{ overflowWrap: 'anywhere' }}>
      {name && <strong>{name}: </strong>}{say(t, [text[0], text[1], { domain }])}
    </Typography>)}
  </Stack>;
};

const Cost = () => {
  const { t } = useTranslation();
  return <Stack spacing={0.5}>
    <Typography variant="body2" color="textSecondary">
      {t('mgmt.homeNames.cost.dns', 'The server\'s private address (for example 192.168.1.20) is published in public DNS. It cannot be reached from outside, but anyone can see which address range your home network uses.')}
    </Typography>
    <Typography variant="body2" color="textSecondary">
      {t('mgmt.homeNames.cost.home', 'The names answer devices on the home network only (and over Tailscale or VPNs the server counts as local). From the internet Cosmos refuses them and notes it in the security record: they are no second way in.')}
    </Typography>
    <Typography variant="body2" color="textSecondary">
      {t('mgmt.homeNames.cost.rebind', 'Some routers drop DNS answers with private addresses (rebinding protection): they need an exception, see below. Without internet the names do not resolve.')}
    </Typography>
  </Stack>;
};

const HomeZone = ({ row, address }) => {
  const { t } = useTranslation();
  const [label, setLabel] = React.useState(row.label || DEFAULT_LABEL);
  const [busy, setBusy] = React.useState('');
  const [notice, setNotice] = React.useState(null);
  const [serverCheck, setServerCheck] = React.useState(null);
  const [browserCheck, setBrowserCheck] = React.useState(null);
  const [costOpen, setCostOpen] = React.useState(false);
  const [helpOpen, setHelpOpen] = React.useState(false);

  React.useEffect(() => { setLabel(row.label || DEFAULT_LABEL); }, [row.label]);

  const cleaned = cleanLabel(label);
  const labelError = cleaned && !validLabel(cleaned) ? say(t, problemText('label')) : '';
  const changed = row.enabled && cleaned !== row.label;

  const set = async (enabled) => {
    setBusy(enabled ? 'on' : 'off');
    setNotice(null);
    const res = await API.config.homeNamesSet(row.zone, cleaned || DEFAULT_LABEL, enabled);
    if (!res.ok) {
      setBusy('');
      setNotice({ severity: 'error', text: res.reason ? say(t, problemText(res.reason)) : (res.message || say(t, problemText(''))) });
      return;
    }
    // the server reloads its routes and certificate names
    await serverBack();
    setBusy('');
    setServerCheck(null);
    setBrowserCheck(null);
    loadHomeNames(true);
  };

  const checkServer = async () => {
    setBusy('server');
    const res = await API.config.homeNamesCheck(row.zone);
    setBusy('');
    if (!res.ok) {
      setServerCheck(['error', res.reason === 'rate_limited'
        ? ['mgmt.homeNames.check.server.limited', 'Too many checks this hour: try again later.']
        : ['mgmt.homeNames.check.server.error', 'The server could not ask its resolver.']]);
      return;
    }
    setServerCheck(serverCheckText(res.data, address));
  };

  const checkBrowser = async () => {
    setBusy('browser');
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 6000);
    let result = 'failed';
    try {
      // an opaque answer is all a page may have from another address; that
      // it came at all means the name resolved and the certificate passed
      await fetch(probeURL(row.panel, window.location), { mode: 'no-cors', cache: 'no-store', credentials: 'omit', signal: controller.signal });
      result = 'opened';
    } catch (e) {
      result = 'failed';
    }
    clearTimeout(timer);
    setBusy('');
    setBrowserCheck(browserCheckText(result, row.panel));
  };

  const problem = row.problem ? problemText(row.problem) : null;

  return <Stack spacing={1} data-home-zone={row.zone}>
    <Typography variant="subtitle1" sx={{ overflowWrap: 'anywhere' }}><HomeOutlined /> {row.zone}</Typography>

    {row.active
      ? <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }} data-home-state="on">
        {t('mgmt.homeNames.on', 'On: every app is also at <app>.{{suffix}}, for example {{example}}, and the panel at {{panel}}. Use these addresses at home, on every device.',
          { suffix: row.suffix, example: exampleName(row.label, row.zone), panel: row.panel })}
      </Typography>
      : <Typography variant="body2" color="textSecondary" sx={{ overflowWrap: 'anywhere' }} data-home-state={row.enabled ? 'inactive' : 'off'}>
        {row.enabled
          ? t('mgmt.homeNames.inactive', 'Switched on, but not in force:')
          : t('mgmt.homeNames.off', 'Off. Switched on, every app gets a name like {{example}} that works at home on every device, with a certificate every browser trusts.', { example: exampleName(cleaned, row.zone) })}
      </Typography>}

    {problem && <Alert severity={row.enabled ? 'warning' : 'info'} sx={{ overflowWrap: 'anywhere', '& .MuiAlert-message': { minWidth: 0 } }}>{say(t, problem)}</Alert>}

    {row.active && <Stack spacing={0.5}>
      <StateLine state={recordState(row)} />
      <StateLine state={certificateState(row)} />
    </Stack>}

    <PermissionGuard permission={PERM_CONFIGURATION}>
      {(!row.problem || row.enabled) && <Stack spacing={1}>
        {(!row.problem) && <FormField
          id={'home-label-' + row.zone}
          label={t('mgmt.homeNames.label', 'Label')}
          value={label}
          onChange={setLabel}
          error={labelError}
          helperText={t('mgmt.homeNames.labelHelp', 'The part between the app and the domain: {{example}}. Letters, digits and hyphens.', { example: exampleName(cleaned, row.zone) })}
          inputProps={{ maxLength: 63, autoCapitalize: 'none', spellCheck: false }}
        />}
        <Stack direction="row" spacing={1} useFlexGap flexWrap="wrap">
          {!row.enabled && <LoadingButton variant="contained" size="small" loading={busy === 'on'} disabled={!!busy || !!labelError || !!row.problem} onClick={() => set(true)} data-home-action="on">
            {t('mgmt.homeNames.switchOn', 'Switch on')}
          </LoadingButton>}
          {row.enabled && changed && <LoadingButton variant="contained" size="small" loading={busy === 'on'} disabled={!!busy || !!labelError} onClick={() => set(true)}>
            {t('mgmt.homeNames.changeLabel', 'Use this label')}
          </LoadingButton>}
          {row.enabled && <LoadingButton variant="outlined" size="small" loading={busy === 'off'} disabled={!!busy} onClick={() => set(false)} data-home-action="off">
            {t('mgmt.homeNames.switchOff', 'Switch off')}
          </LoadingButton>}
          {row.active && <LoadingButton variant="outlined" size="small" loading={busy === 'server'} disabled={!!busy} onClick={checkServer} data-home-action="server">
            {t('mgmt.homeNames.checkServer', 'Check from the server')}
          </LoadingButton>}
          {row.active && <LoadingButton variant="outlined" size="small" loading={busy === 'browser'} disabled={!!busy} onClick={checkBrowser} data-home-action="browser">
            {t('mgmt.homeNames.checkBrowser', 'Try from this device')}
          </LoadingButton>}
        </Stack>
        {row.enabled && changed && <Typography variant="caption" color="textSecondary">
          {t('mgmt.homeNames.changeNote', 'The old names stop working and their record goes; the certificate gets the new name (one order).')}
        </Typography>}
      </Stack>}
    </PermissionGuard>

    {notice && <Alert severity={notice.severity} sx={{ overflowWrap: 'anywhere', '& .MuiAlert-message': { minWidth: 0 } }}>{notice.text}</Alert>}
    <StateLine state={serverCheck} />
    <StateLine state={browserCheck} />
    {row.active && browserCheck && <Typography variant="caption" color="textSecondary">
      {t('mgmt.homeNames.check.browser.note', 'The browser cannot look a name up: it can only try to open the address. Chrome may first ask whether this page may reach devices on the local network.')}
    </Typography>}

    {(!row.problem || row.enabled) && <>
      <Door open={costOpen} onClick={() => setCostOpen(!costOpen)}>{t('mgmt.homeNames.costTitle', 'What it costs')}</Door>
      <Collapse in={costOpen} unmountOnExit><Cost /></Collapse>
      <Door open={helpOpen} onClick={() => setHelpOpen(!helpOpen)}>{t('mgmt.homeNames.rebindTitle', 'The name does not resolve at home')}</Door>
      <Collapse in={helpOpen} unmountOnExit><RebindHelp row={row} /></Collapse>
    </>}
  </Stack>;
};

// The card on the Domains tab. Only domains at Cloudflare are listed (the
// record is written there); nothing when there is none.
export const HomeNamesCard = () => {
  const { t } = useTranslation();
  const status = useHomeNames();
  if (!status) return null;
  const rows = (status.zones || []).filter((row) => row.enabled || row.problem !== 'not_cloudflare');
  if (rows.length === 0) return null;

  return <MainCard title={t('mgmt.homeNames.title', 'Names for use at home')}>
    <Stack spacing={2} data-home-names>
      <Typography variant="body2" color="textSecondary">
        {t('mgmt.homeNames.intro', '.local names do not work on older Android phones, most TVs and some Linux systems, and browsers warn about their certificate. Names under your own domain that point at the server\'s private address work on every device at home, with the domain\'s Let\'s Encrypt certificate. Cosmos writes one record at Cloudflare for all of them, and one name on the certificate: no app\'s name is published.')}
      </Typography>
      {status.addressKnown
        ? (status.address && <Typography variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
          {status.addressFresh
            ? t('mgmt.homeNames.address', 'The server\'s private address: {{address}} (the network interface the .local names use, see Configuration > HTTP).', { address: status.address })
            : t('mgmt.homeNames.addressOld', 'The server\'s private address: {{address}}, as last seen (no interface has one right now).', { address: status.address })}
        </Typography>)
        : <Alert severity="warning">{t('mgmt.homeNames.noAddress', 'Cosmos finds no private IPv4 address on the server\'s home network interface: the names cannot point anywhere yet.')}</Alert>}
      <Stack spacing={2} divider={<Divider flexItem />}>
        {rows.map((row) => <HomeZone key={row.zone} row={row} address={status.address} />)}
      </Stack>
    </Stack>
  </MainCard>;
};

export default HomeNamesCard;

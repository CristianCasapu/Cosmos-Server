// Fork addition: Cloudflare in the panel (see src/cloudflare on the server).
//
//   - CloudflareConnect: the guided set-up of Configuration > Domains. Paste a
//     token, pick the domain, see exactly which records will be written and
//     which are left alone, connect.
//   - CloudflareOverview: what is connected, every address under it with the
//     state of its record and its switch, and Cloudflare's address ranges.
//   - AddressShield: one address with its record's state and the "through
//     Cloudflare / directly" switch; used in an app's "Domains" dialog, on
//     its page and in the overview.
//
// The sentences are in utils/cloudflare.js, which can be tested on its own.
import * as React from 'react';
import {
  Alert,
  Button,
  Checkbox,
  Chip,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Divider,
  FormControlLabel,
  InputLabel,
  Link,
  OutlinedInput,
  Stack,
  Switch,
  Tooltip,
  Typography,
} from '@mui/material';
import { LoadingButton } from '@mui/lab';
import {
  CheckCircleOutlined,
  ClockCircleOutlined,
  CloseCircleOutlined,
  CloudOutlined,
  InfoCircleOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import { useTheme } from '@mui/material/styles';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import MainCard from './MainCard';
import PermissionGuard from './permissionGuard';
import OriginPulls from './originPulls';
import { PERM_CONFIGURATION } from '../utils/permissions';
import { serverBack } from '../utils/appLinks';
import {
  MISMATCH, cannotShieldText, failureText, hostEntry, looksLikeMediaServer, panelChoices, panelComesThroughCloudflare,
  previewActionText, previewCounts, proposePanel, recordText, shieldMismatch, sslModeText, tokenFormat, tokenStatusText,
  validPanelHost, zoneAccessText, zoneSettings,
} from '../utils/cloudflare';

// --- one status for the whole page, shared by every switch on it
let store = { data: null, at: 0, loading: false };
const listeners = new Set();
const emit = () => listeners.forEach((listener) => listener({ ...store }));
let followUp = null;

const hasPending = (data) => ((data && data.hosts) || []).some((host) => host.record && host.record.state === 'pending');

export const loadCloudflare = (force) => {
  if (store.loading || (!force && Date.now() - store.at < 15000)) {
    return Promise.resolve(store.data);
  }
  store = { ...store, loading: true };
  return API.config.cloudflareStatus().then((res) => {
    store = { data: res.ok ? res.data : store.data, at: Date.now(), loading: false };
    emit();
    return store.data;
  });
};

// After a change the records follow within a minute: look again a few times.
const followChanges = (times = 8) => {
  clearTimeout(followUp);
  if (times <= 0) return;
  followUp = setTimeout(() => {
    loadCloudflare(true).then((data) => { if (hasPending(data) || times > 6) followChanges(times - 1); });
  }, 4000);
};

export const useCloudflare = () => {
  const [state, setState] = React.useState({ ...store });
  React.useEffect(() => {
    listeners.add(setState);
    loadCloudflare(false);
    setState({ ...store });
    return () => { listeners.delete(setState); };
  }, []);
  return state;
};

const LevelIcon = ({ level }) => {
  const theme = useTheme();
  const style = { marginTop: 3, flexShrink: 0 };
  if (level === 'ok') return <CheckCircleOutlined style={{ ...style, color: theme.palette.success.main }} />;
  if (level === 'problem') return <CloseCircleOutlined style={{ ...style, color: theme.palette.error.main }} />;
  if (level === 'pending') return <ClockCircleOutlined style={style} />;
  return <InfoCircleOutlined style={style} />;
};

// What changes for an address that goes through Cloudflare, said before the
// switch is flipped. `image` is the application's, when it is known.
const ShieldNotes = ({ image, name }) => {
  const { t } = useTranslation();
  return <Stack spacing={1}>
    {looksLikeMediaServer(image) && <Alert severity="warning" sx={{ overflowWrap: 'anywhere' }}>
      {t('mgmt.cloudflare.notes.media',
        '{{name}} looks like a media server. Cloudflare\'s terms do not allow serving video or large amounts of big files through its proxy on the Free, Pro and Business plans, and it may limit or switch off the proxy for a domain used that way. "Directly" is the usual choice for such an application.',
        { name: name || image })}
    </Alert>}
    <Typography variant="body2">
      {t('mgmt.cloudflare.notes.guard',
        'Cosmos will answer for this address only to connections from Cloudflare\'s network and from your local network. Whoever asks this server for it directly is refused, and the attempt is recorded.')}
    </Typography>
    <Typography variant="body2">
      {t('mgmt.cloudflare.notes.limits',
        'Limits of Cloudflare\'s proxy on the free plan: at most 100 MB per upload, an answer must begin within about two minutes, and long-lived connections (live updates, terminals) are cut from time to time.')}
    </Typography>
    <Typography variant="body2">
      {t('mgmt.cloudflare.notes.bans',
        'A firewall ban on this server (Fail2ban) cannot cut off a visitor who comes through Cloudflare: the connection is Cloudflare\'s. Cosmos\' own SmartShield still goes by the visitor\'s real address.')}
    </Typography>
    <Typography variant="body2">
      {t('mgmt.cloudflare.notes.ssl',
        'At Cloudflare, the domain\'s SSL/TLS mode must be "Full (strict)" (SSL/TLS > Overview).')}
    </Typography>
  </Stack>;
};

// One address: where it leads, the state of its record and the switch.
// `host` is an entry of the status; `image` and `name` describe the
// application, when there is one.
export const AddressShield = ({ host, status, image, name, dense }) => {
  const { t } = useTranslation();
  const [ask, setAsk] = React.useState(null); // 'on' | 'last'
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState('');

  if (!host) {
    return null;
  }
  const record = recordText(host);
  const mismatch = shieldMismatch(host);
  const blocked = !host.shielded && host.cannotShield;

  const change = async (shielded) => {
    setBusy(true);
    setError('');
    const res = await API.config.cloudflareShield(host.host, shielded);
    setBusy(false);
    setAsk(null);
    if (!res.ok) {
      const why = cannotShieldText(res.reason);
      setError(res.reason ? t(why[0], why[1], { ports: ((status && status.allowedPorts) || []).join(', '), port: status && status.publicPort }) : (res.message || t('mgmt.cloudflare.switch.failed', 'The change could not be saved.')));
      return;
    }
    await loadCloudflare(true);
    followChanges();
    if (res.data && res.data.lastShield) {
      setAsk('last');
    }
  };

  const stopTrusting = async () => {
    setBusy(true);
    await API.config.cloudflareRanges('distrust');
    setBusy(false);
    setAsk(null);
    loadCloudflare(true);
  };

  const cannot = blocked ? cannotShieldText(host.cannotShield) : null;
  const label = host.shielded
    ? t('mgmt.cloudflare.switch.through', 'Through Cloudflare')
    : t('mgmt.cloudflare.switch.directly', 'Directly');

  return <Stack spacing={0.5} sx={{ minWidth: 0 }} data-cloudflare-host={host.host}>
    <Stack direction="row" spacing={1} alignItems="center" justifyContent="space-between" useFlexGap flexWrap="wrap" sx={{ minWidth: 0 }}>
      <Typography variant={dense ? 'body2' : 'subtitle2'} sx={{ overflowWrap: 'anywhere', minWidth: 0 }}>
        <Link href={host.url} target="_blank" rel="noopener noreferrer">{host.url}</Link>
        {host.panel && <> <Chip size="small" label={t('mgmt.cloudflare.panelChip', 'Cosmos panel')} /></>}
      </Typography>
      <PermissionGuard permission={PERM_CONFIGURATION}>
        <Tooltip title={cannot
          ? t(cannot[0], cannot[1], { ports: ((status && status.allowedPorts) || []).join(', '), port: status && status.publicPort })
          : host.managed
            ? t('mgmt.cloudflare.switch.tipManaged', 'Cosmos sets this at Cloudflare for you. A change made by hand at Cloudflare is put back within ten minutes.')
            : t('mgmt.cloudflare.switch.tipByHand', 'Cosmos does not manage this record: this only tells Cosmos how the record is set at Cloudflare, so that it believes and refuses the right visitors.')}>
          <FormControlLabel
            sx={{ marginRight: 0 }}
            control={<Switch
              size="small"
              checked={!!host.shielded}
              disabled={busy || !!blocked}
              onChange={(event) => (event.target.checked ? setAsk('on') : change(false))}
              inputProps={{ 'aria-label': t('mgmt.cloudflare.switch.aria', 'Serve {{host}} through Cloudflare', { host: host.host }) }}
            />}
            label={<Typography variant="body2">{label}</Typography>}
          />
        </Tooltip>
      </PermissionGuard>
    </Stack>

    <Stack direction="row" spacing={1} alignItems="flex-start" sx={{ minWidth: 0 }}>
      <LevelIcon level={record.level} />
      <Typography variant="body2" color="textSecondary" sx={{ overflowWrap: 'anywhere', minWidth: 0 }} data-record-state={host.record ? host.record.state : 'none'}>
        {t(record.key, record.text)}
        {record.detail && <Typography component="span" variant="caption" color="textSecondary"> ({record.detail})</Typography>}
      </Typography>
    </Stack>
    {mismatch && <Alert severity="warning" sx={{ overflowWrap: 'anywhere' }}>{t(MISMATCH[mismatch][0], MISMATCH[mismatch][1])}</Alert>}
    {cannot && <Typography variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
      {t(cannot[0], cannot[1], { ports: ((status && status.allowedPorts) || []).join(', '), port: status && status.publicPort })}
    </Typography>}
    {error && <Alert severity="error" sx={{ overflowWrap: 'anywhere' }}>{error}</Alert>}

    {ask === 'on' && <Dialog open onClose={() => !busy && setAsk(null)} fullWidth maxWidth="xs">
      <DialogTitle sx={{ overflowWrap: 'anywhere' }}>
        {t('mgmt.cloudflare.confirm.title', 'Serve {{host}} through Cloudflare?', { host: host.host })}
      </DialogTitle>
      <DialogContent>
        <Stack spacing={1.5}>
          {!host.managed && <Alert severity="info" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.cloudflare.confirm.byHand',
              'Cosmos does not manage this record, so nothing is changed at Cloudflare: the proxy (orange cloud) must be on for it there.')}
          </Alert>}
          <ShieldNotes image={image} name={name} />
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={() => setAsk(null)} disabled={busy}>{t('global.cancelAction')}</Button>
        <LoadingButton loading={busy} onClick={() => change(true)}>
          {t('mgmt.cloudflare.confirm.yes', 'Serve through Cloudflare')}
        </LoadingButton>
      </DialogActions>
    </Dialog>}

    {ask === 'last' && <Dialog open onClose={() => !busy && setAsk(null)} fullWidth maxWidth="xs">
      <DialogTitle>{t('mgmt.cloudflare.last.title', 'Nothing is served through Cloudflare any more')}</DialogTitle>
      <DialogContent>
        <Typography variant="body2">
          {t('mgmt.cloudflare.last.text',
            'Cosmos still believes Cloudflare\'s addresses when they say who the visitor is. With no address served through Cloudflare there is no reason to: stop believing them?')}
        </Typography>
      </DialogContent>
      <DialogActions>
        <Button onClick={() => setAsk(null)} disabled={busy}>{t('mgmt.cloudflare.last.keep', 'Keep it')}</Button>
        <LoadingButton loading={busy} onClick={stopTrusting}>{t('mgmt.cloudflare.last.stop', 'Stop believing them')}</LoadingButton>
      </DialogActions>
    </Dialog>}
  </Stack>;
};

// The addresses of one application (by their hostnames) that are under a
// Cloudflare domain, each with its switch. Shows nothing when there is none.
export const AppShields = ({ hosts, image, name, title, dense }) => {
  const { t } = useTranslation();
  const state = useCloudflare();
  const entries = (hosts || []).map((host) => hostEntry(state.data, host)).filter(Boolean);
  if (entries.length === 0) {
    return null;
  }
  return <Stack spacing={1.5} sx={{ minWidth: 0 }}>
    {title && <Typography variant="caption" color="textSecondary" component="div">
      <CloudOutlined /> {t('mgmt.cloudflare.appTitle', 'Cloudflare')}
    </Typography>}
    {entries.map((entry) => <AddressShield key={entry.host} host={entry} status={state.data} image={image} name={name} dense={dense} />)}
  </Stack>;
};

// What is connected, in Configuration > Domains.
export const CloudflareOverview = () => {
  const { t } = useTranslation();
  const state = useCloudflare();
  const [busy, setBusy] = React.useState('');
  const [notice, setNotice] = React.useState(null);
  const data = state.data;

  if (!data || (data.zones || []).length === 0) {
    return null;
  }

  const ranges = async (action) => {
    setBusy(action);
    setNotice(null);
    const res = await API.config.cloudflareRanges(action);
    setBusy('');
    if (!res.ok) {
      const failure = failureText(res.reason);
      setNotice({ severity: 'error', text: res.reason ? t(failure[0], failure[1]) + (res.detail ? ' (' + res.detail + ')' : '') : res.message });
    } else if (action === 'refresh') {
      setNotice({ severity: 'success', text: t('mgmt.cloudflare.ranges.refreshed', 'Cloudflare\'s current list is in use: {{count}} ranges.', { count: res.data.count }) });
    }
    loadCloudflare(true);
  };

  const shielded = data.hosts.filter((host) => host.shielded).length;
  const direct = data.hosts.length - shielded;

  return <MainCard title={t('mgmt.cloudflare.title', 'Cloudflare')}>
    <Stack spacing={2} data-cloudflare-overview>
      {!data.portOK && <Alert severity="warning">
        {t('mgmt.cloudflare.port.bad',
          'Visitors reach Cosmos on port {{port}}, which Cloudflare does not pass on (it passes HTTPS on ports {{ports}}). Addresses can only be served directly until that changes.',
          { port: data.publicPort, ports: data.allowedPorts.join(', ') })}
      </Alert>}

      {data.zones.map((zone) => {
        const ssl = sslModeText(zone.sslMode === 'unreadable' ? '' : zone.sslMode);
        return <Stack key={zone.name} spacing={0.5}>
          <Typography variant="subtitle1" sx={{ overflowWrap: 'anywhere' }}><CloudOutlined /> {zone.name}</Typography>
          {!zone.hasToken && <Alert severity="warning">
            {t('mgmt.cloudflare.zone.noToken', 'No API token is stored for this domain: Cosmos cannot write its records. Connect it again.')}
          </Alert>}
          {zone.hasToken && !zone.manageRecords && <Typography variant="body2" color="textSecondary">
            {t('mgmt.cloudflare.zone.byHand', 'Cosmos gets this domain\'s certificate through Cloudflare, but does not manage its records ("manage records" is off): you create each address at Cloudflare yourself.')}
          </Typography>}
          {shielded > 0 && zone.sslMode && <Alert severity={ssl[0]} sx={{ overflowWrap: 'anywhere' }}>{t(ssl[1], ssl[2])}</Alert>}
        </Stack>;
      })}

      {data.hosts.length === 0
        ? <Typography variant="body2" color="textSecondary">
          {t('mgmt.cloudflare.noHosts', 'No address under this domain yet. Give an application one with "Domains" on its card (ServApps).')}
        </Typography>
        : <Stack spacing={1.5} divider={<Divider flexItem />}>
          {data.hosts.map((host) => <AddressShield key={host.host} host={host} status={data} />)}
        </Stack>}

      {shielded > 0 && direct > 0 && <Typography variant="caption" color="textSecondary">
        {t('mgmt.cloudflare.mixed',
          'Some addresses are served directly: their records show this server\'s public address to anyone who looks them up. Cloudflare filters the traffic of the addresses that go through it; it does not hide the server.')}
      </Typography>}

      <Stack spacing={1}>
        <Typography variant="subtitle1">{t('mgmt.cloudflare.ranges.title', 'Cloudflare\'s addresses')}</Typography>
        <Typography variant="body2" color="textSecondary" sx={{ overflowWrap: 'anywhere' }} data-cloudflare-trust={data.ranges.trusted ? 'on' : 'off'}>
          {data.ranges.trusted
            ? t('mgmt.cloudflare.ranges.trusted',
              'For an address served through Cloudflare, Cosmos takes the visitor\'s address from what Cloudflare reports, when the connection comes from one of Cloudflare\'s own addresses. For every other address, and from everyone else, such a report is ignored.')
            : t('mgmt.cloudflare.ranges.untrusted',
              'Nothing is served through Cloudflare: Cosmos takes every visitor\'s address from the connection itself.')}
          {' '}
          {data.ranges.builtin
            ? t('mgmt.cloudflare.ranges.builtin', 'List in use: {{count}} ranges, the one built into Cosmos (Cloudflare\'s list of {{date}}).', { count: data.ranges.count, date: data.ranges.date })
            : t('mgmt.cloudflare.ranges.fetched', 'List in use: {{count}} ranges, fetched from Cloudflare on {{date}}.', { count: data.ranges.count, date: data.ranges.date })}
        </Typography>
        {data.ranges.trusted && <Typography variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
          {t('mgmt.cloudflare.ranges.limit',
            'What this does and does not do: it keeps out everyone who does not come through Cloudflare\'s network. That network is shared by all of Cloudflare\'s customers, so it is not proof that a request passed your own Cloudflare settings. Logins, two-factor codes and SmartShield apply to every request as before.')}
        </Typography>}
        {notice && <Alert severity={notice.severity} sx={{ overflowWrap: 'anywhere' }}>{notice.text}</Alert>}
        <PermissionGuard permission={PERM_CONFIGURATION}>
          <Stack direction="row" spacing={1} useFlexGap flexWrap="wrap">
            <Tooltip title={t('mgmt.cloudflare.ranges.refreshTip', 'Asks api.cloudflare.com for its published address ranges and uses them if they are a list of public networks. While an address is served through Cloudflare, Cosmos also does this once a week.')}>
              <span>
                <LoadingButton size="small" variant="outlined" loading={busy === 'refresh'} startIcon={<ReloadOutlined />} onClick={() => ranges('refresh')}>
                  {t('mgmt.cloudflare.ranges.refresh', 'Fetch the current list')}
                </LoadingButton>
              </span>
            </Tooltip>
            {data.ranges.trusted && shielded === 0 && <LoadingButton size="small" variant="outlined" loading={busy === 'distrust'} onClick={() => ranges('distrust')}>
              {t('mgmt.cloudflare.ranges.distrust', 'Stop believing Cloudflare\'s addresses')}
            </LoadingButton>}
          </Stack>
        </PermissionGuard>
      </Stack>

      <OriginPulls status={data} reload={() => loadCloudflare(true)} />
    </Stack>
  </MainCard>;
};

const STEPS = ['token', 'domain', 'review', 'done'];

// The guided set-up. `zones` are the domains already configured (the answer
// of /api/zones), `hostname` the panel's address now, `onClose(changed)`.
export const CloudflareConnect = ({ zones, hostname, httpsPort, onClose, onManual }) => {
  const { t } = useTranslation();
  const connected = (zones || []).filter((zone) => !zone.derived && zone.DNSChallengeProvider === 'cloudflare').map((zone) => zone.Zone);

  const [step, setStep] = React.useState('token');
  const [token, setToken] = React.useState('');
  const [stored, setStored] = React.useState(''); // use the token stored for this domain
  const [checking, setChecking] = React.useState(false);
  const [report, setReport] = React.useState(null);
  const [failure, setFailure] = React.useState(null);
  const [zone, setZone] = React.useState('');
  const [typedZone, setTypedZone] = React.useState('');
  const [preview, setPreview] = React.useState(null);
  const [previewing, setPreviewing] = React.useState(false);
  const [panel, setPanel] = React.useState('');
  const [panelSource, setPanelSource] = React.useState('');
  const [keepPanel, setKeepPanel] = React.useState(false);
  const [replace, setReplace] = React.useState([]);
  const [options, setOptions] = React.useState({ wildcardCertificate: true, manageRecords: true, wildcardRecord: false });
  const [showOptions, setShowOptions] = React.useState(false);
  const [panelShield, setPanelShield] = React.useState(true);
  const [working, setWorking] = React.useState('');
  const [done, setDone] = React.useState(null);

  const format = token ? tokenFormat(token) : '';
  const usable = stored ? true : format === 'token';
  const usedToken = stored ? '' : token.trim();
  const currentPanel = String(hostname || '').replace(/:\d+$/, '').toLowerCase();
  const port = Number(httpsPort) || 443;
  const address = (name) => 'https://' + name + (port === 443 ? '' : ':' + port);

  const failed = (res) => {
    const text = failureText(res.reason);
    setFailure(res.reason && res.reason !== 'no_answer'
      ? t(text[0], text[1]) + (res.detail ? ' (' + res.detail + ')' : '')
      : (res.message || t('mgmt.cloudflare.noAnswer', 'The server did not answer. Confirm your password again if it asks, then retry.')));
  };

  const check = async () => {
    setChecking(true);
    setFailure(null);
    setReport(null);
    const res = await API.config.cloudflareCheck(usedToken, stored);
    setChecking(false);
    if (!res.ok) {
      failed(res);
      return;
    }
    setReport(res.data);
    if (res.data.status === 'ok') {
      setStep('domain');
      const names = res.data.zones.map((entry) => entry.name);
      if (stored && names.includes(stored)) {
        pickZone(stored, res.data);
      } else if (names.length === 1) {
        pickZone(names[0], res.data);
      }
    }
  };

  const runPreview = async (name, panelHost, ticks) => {
    setPreviewing(true);
    setFailure(null);
    const res = await API.config.cloudflarePreview(usedToken, name, panelHost, ticks);
    setPreviewing(false);
    if (!res.ok) {
      setPreview(null);
      failed(res);
      return null;
    }
    setPreview(res.data);
    return res.data;
  };

  const pickZone = async (name, checked) => {
    setZone(name);
    setReplace([]);
    setPreview(null);
    const existing = (zones || []).find((entry) => !entry.derived && entry.Zone === name && entry.DNSChallengeProvider === 'cloudflare');
    if (existing) {
      // already connected: start from how it is set up now
      setOptions({ wildcardCertificate: !!existing.UseWildcardCertificate, manageRecords: !!existing.ManageRecords, wildcardRecord: !!existing.WildcardRecord });
    }
    const access = ((checked || report || {}).zones || []).find((entry) => entry.name === name);
    if (access && (access.canEdit === 'no' || access.canRead === 'no')) {
      return;
    }
    // first what the domain holds today, to propose the panel's address from it
    const first = await runPreview(name, '', []);
    if (!first) {
      return;
    }
    const proposal = proposePanel(name, first, currentPanel);
    setPanel(proposal.host);
    setPanelSource(proposal.source);
    setKeepPanel(false);
    await runPreview(name, proposal.host, []);
    setStep('review');
  };

  const panelHost = keepPanel ? '' : panel.trim().toLowerCase();
  const panelValid = keepPanel || validPanelHost(panelHost, zone);
  const refresh = (nextPanel, ticks) => runPreview(zone, nextPanel, ticks);

  const choosePanel = (host) => {
    setKeepPanel(false);
    setPanel(host);
    setPanelSource('picked');
    refresh(host, replace);
  };

  const toggleReplace = (name) => {
    const next = replace.includes(name) ? replace.filter((entry) => entry !== name) : [...replace, name];
    setReplace(next);
    refresh(panelHost, next);
  };

  const access = report && report.zones.find((entry) => entry.name === zone);
  const accessText = zoneAccessText(access);
  const counts = previewCounts(preview);
  const viaCloudflare = !keepPanel && panelComesThroughCloudflare(preview);
  const panelChanges = !keepPanel && panelHost && panelHost !== currentPanel;

  const connect = async () => {
    setFailure(null);
    try {
      if (replace.length > 0) {
        setWorking(t('mgmt.cloudflare.work.takeover', 'Noting which records Cosmos may replace…'));
        const res = await API.config.cloudflareTakeOver(replace, zone);
        if (!res.ok) { setWorking(''); failed(res); return; }
      }
      setWorking(t('mgmt.cloudflare.work.zone', 'Saving the domain…'));
      // a domain that is already configured keeps what the guided set-up does not ask about
      const existing = (zones || []).find((entry) => !entry.derived && entry.Zone === zone);
      const settings = zoneSettings(stored ? '***' : usedToken, options, existing);
      await API.zones.set(zone, settings);
      await serverBack();
      if (panelChanges) {
        setWorking(t('mgmt.cloudflare.work.panel', 'Setting the panel\'s address…'));
        const res = await API.config.cloudflarePanel(panelHost);
        if (!res.ok) { setWorking(''); setFailure(res.message || t('mgmt.cloudflare.panelFailed', 'The panel\'s address could not be set.')); return; }
        await serverBack();
      }
      let shieldProblem = '';
      if (viaCloudflare && panelShield && panelHost) {
        setWorking(t('mgmt.cloudflare.work.shield', 'Telling Cosmos that the panel comes through Cloudflare…'));
        const res = await API.config.cloudflareShield(panelHost, true);
        if (!res.ok) {
          const why = cannotShieldText(res.reason);
          shieldProblem = t(why[0], why[1], { ports: '443, 2053, 2083, 2087, 2096, 8443', port });
        }
      }
      setWorking('');
      setDone({ panel: panelChanges ? panelHost : '', shieldProblem, through: viaCloudflare && panelShield && !shieldProblem });
      setStep('done');
      loadCloudflare(true);
      followChanges();
    } catch (error) {
      setWorking('');
      setFailure(error.message || String(error));
    }
  };

  const stepNumber = STEPS.indexOf(step) + 1;
  const status = report ? tokenStatusText(report.status) : null;
  const formatProblem = token && !stored && format !== 'token' ? tokenStatusText(format) : null;
  const busy = checking || previewing || !!working;

  return <Dialog open onClose={() => !busy && onClose(step === 'done')} fullWidth maxWidth="sm" data-cloudflare-connect={step}>
    <DialogTitle>
      {t('mgmt.cloudflare.connect.title', 'Connect Cloudflare')}
      <Typography variant="caption" color="textSecondary" component="div">
        {t('mgmt.cloudflare.connect.step', 'Step {{n}} of 4', { n: stepNumber })}
      </Typography>
    </DialogTitle>
    <DialogContent>
      <Stack spacing={2}>
        {/* 1. the token */}
        {step === 'token' && <>
          <Typography variant="body2">
            {t('mgmt.cloudflare.connect.intro',
              'With an API token of your Cloudflare account, Cosmos gives each application an address under your domain, keeps those addresses pointed at this server when its public address changes, and gets real certificates without opening any other port. Records Cosmos did not create are never changed.')}
          </Typography>
          <Typography variant="body2" color="textSecondary">
            {t('mgmt.cloudflare.connect.howToken',
              'Create the token at Cloudflare: My Profile > API Tokens > Create Token > "Edit zone DNS". Permissions: Zone > DNS > Edit and Zone > Zone > Read. Zone Resources: Include > Specific zone > your domain. A token limited to that one domain is all Cosmos needs.')}
          </Typography>
          {connected.length > 0 && <Stack direction="row" spacing={1} useFlexGap flexWrap="wrap" alignItems="center">
            <Typography variant="caption" color="textSecondary">{t('mgmt.cloudflare.connect.useStored', 'Or use the token already stored for:')}</Typography>
            {connected.map((name) => <Chip
              key={name}
              size="small"
              clickable
              color={stored === name ? 'primary' : 'default'}
              variant={stored === name ? 'filled' : 'outlined'}
              label={name}
              onClick={() => { setStored(stored === name ? '' : name); setReport(null); }}
            />)}
          </Stack>}
          {!stored && <Stack spacing={1}>
            <InputLabel htmlFor="cloudflare-token">{t('mgmt.cloudflare.connect.tokenLabel', 'API token')}</InputLabel>
            <OutlinedInput
              id="cloudflare-token"
              type="password"
              fullWidth
              autoComplete="off"
              value={token}
              inputProps={{ maxLength: 220, spellCheck: false, 'data-lpignore': 'true' }}
              placeholder={t('mgmt.cloudflare.connect.tokenPlaceholder', 'Paste the token here')}
              onChange={(event) => { setToken(event.target.value); setReport(null); setFailure(null); }}
              onKeyDown={(event) => {
                // inside the settings form: Enter must not save that form
                if (event.key === 'Enter') {
                  event.preventDefault();
                  if (usable && !checking) check();
                }
              }}
            />
            <Typography variant="caption" color="textSecondary">
              {t('mgmt.cloudflare.connect.tokenHelp',
                'It is stored on this server with the domain, is never shown again and is sent to api.cloudflare.com only.')}
            </Typography>
          </Stack>}
          {formatProblem && <Alert severity={formatProblem[0]} sx={{ overflowWrap: 'anywhere' }}>{t(formatProblem[1], formatProblem[2])}</Alert>}
          {status && report.status !== 'ok' && <Alert severity={status[0]} sx={{ overflowWrap: 'anywhere' }} data-token-status={report.status}>
            {t(status[1], status[2])}{report.message ? ' (' + report.message + ')' : ''}
          </Alert>}
        </>}

        {/* 2. the domain */}
        {step === 'domain' && report && <>
          <Alert severity="success" data-token-status="ok">{t(status[1], status[2])}</Alert>
          {report.broad && <Alert severity="warning" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.cloudflare.connect.broad',
              'This token sees {{count}} domains of your account. Cosmos only needs the one you connect: a token limited to it (Zone Resources > Specific zone) is safer, and you can replace this one later.',
              { count: report.zones.length + (report.moreZones ? '+' : '') })}
          </Alert>}
          <Typography variant="subtitle2">{t('mgmt.cloudflare.connect.pick', 'Which domain should Cosmos use?')}</Typography>
          <Stack direction="row" spacing={1} useFlexGap flexWrap="wrap">
            {report.zones.map((entry) => <Chip
              key={entry.name}
              clickable
              icon={<CloudOutlined />}
              color={zone === entry.name ? 'primary' : 'default'}
              variant={zone === entry.name ? 'filled' : 'outlined'}
              label={entry.name + (entry.connected ? ' · ' + t('mgmt.cloudflare.connect.connected', 'connected') : '')}
              onClick={() => pickZone(entry.name)}
              disabled={previewing}
              sx={{ maxWidth: '100%' }}
            />)}
          </Stack>
          {report.moreZones && <Stack spacing={1}>
            <InputLabel htmlFor="cloudflare-zone">{t('mgmt.cloudflare.connect.typeZone', 'The token sees more domains than are listed. Type yours:')}</InputLabel>
            <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1}>
              <OutlinedInput
                id="cloudflare-zone"
                fullWidth
                value={typedZone}
                placeholder="example.org"
                onChange={(event) => setTypedZone(event.target.value)}
                onKeyDown={(event) => { if (event.key === 'Enter') { event.preventDefault(); if (typedZone.trim()) pickZone(typedZone.trim().toLowerCase()); } }}
              />
              <Button variant="outlined" disabled={!typedZone.trim() || previewing} onClick={() => pickZone(typedZone.trim().toLowerCase())}>
                {t('mgmt.cloudflare.connect.useZone', 'Use it')}
              </Button>
            </Stack>
          </Stack>}
          {zone && access && accessText[1] && <Alert severity={accessText[0]} sx={{ overflowWrap: 'anywhere' }}>{t(accessText[1], accessText[2])}</Alert>}
          {previewing && <Stack direction="row" spacing={1} alignItems="center">
            <CircularProgress size={16} color="inherit" />
            <Typography variant="body2">{t('mgmt.cloudflare.connect.reading', 'Reading the records of {{zone}} at Cloudflare…', { zone })}</Typography>
          </Stack>}
        </>}

        {/* 3. what will happen */}
        {step === 'review' && preview && <>
          <Typography variant="subtitle2" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.cloudflare.review.panelTitle', 'The address of the Cosmos panel')}
          </Typography>
          <Stack direction="row" spacing={1} useFlexGap flexWrap="wrap">
            {panelChoices(zone, preview, currentPanel).map((host) => <Chip
              key={host}
              clickable
              color={!keepPanel && panelHost === host ? 'primary' : 'default'}
              variant={!keepPanel && panelHost === host ? 'filled' : 'outlined'}
              label={address(host)}
              onClick={() => choosePanel(host)}
              disabled={busy}
              sx={{ maxWidth: '100%' }}
            />)}
            {!(currentPanel === zone || currentPanel.endsWith('.' + zone)) && <Chip
              clickable
              color={keepPanel ? 'primary' : 'default'}
              variant={keepPanel ? 'filled' : 'outlined'}
              label={t('mgmt.cloudflare.review.keepPanel', 'Keep {{host}}', { host: currentPanel })}
              onClick={() => { setKeepPanel(true); refresh('', replace); }}
              disabled={busy}
              sx={{ maxWidth: '100%' }}
            />}
          </Stack>
          {!keepPanel && <Stack spacing={1}>
            <InputLabel htmlFor="cloudflare-panel">{t('mgmt.cloudflare.review.panelLabel', 'Or another name under {{zone}}', { zone })}</InputLabel>
            <OutlinedInput
              id="cloudflare-panel"
              fullWidth
              value={panel}
              error={!panelValid}
              placeholder={'cosmos.' + zone}
              onChange={(event) => { setPanel(event.target.value); setPanelSource('typed'); }}
              onBlur={() => panelValid && refresh(panelHost, replace)}
              onKeyDown={(event) => { if (event.key === 'Enter') { event.preventDefault(); if (panelValid) refresh(panelHost, replace); } }}
            />
            {!panelValid && <Typography variant="caption" color="error">
              {t('mgmt.cloudflare.review.panelInvalid', 'Expected {{zone}} itself or a name under it, such as cosmos.{{zone}}.', { zone })}
            </Typography>}
          </Stack>}
          <Typography variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }} data-panel-source={keepPanel ? 'keep' : panelSource}>
            {keepPanel
              ? t('mgmt.cloudflare.review.source.keep', 'The panel keeps the address it has now.')
              : panelSource === 'apex'
                ? t('mgmt.cloudflare.review.source.apex', 'Proposed because {{zone}} already goes through Cloudflare to this server: on port {{port}} it reaches Cosmos, without any new record.', { zone, port })
                : panelSource === 'current'
                  ? t('mgmt.cloudflare.review.source.current', 'This is the panel\'s address today.')
                  : panelSource === 'default'
                    ? t('mgmt.cloudflare.review.source.default', 'Proposed: a name of its own for the panel. Cosmos creates its record.')
                    : ''}
          </Typography>
          {panelChanges && <Alert severity="info" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.cloudflare.review.panelChange',
              'What changes for you: the panel keeps answering at the address you use now ({{current}}) and you stay logged in there. At {{address}} you log in once more, because a login belongs to one address. That address works from outside once your router forwards port {{port}} to this server; until its certificate has arrived (a few minutes) the browser shows a warning there.',
              { current: window.location.host, address: address(panelHost), port })}
          </Alert>}
          {!keepPanel && panelHost === zone && <Alert severity="warning" sx={{ overflowWrap: 'anywhere' }} data-panel-apex-note>
            {t('mgmt.cloudflare.review.apexCookie',
              'Good to know before choosing the domain itself: a browser sends a login to every port of a name. Whatever answers https://{{zone}} without a port (another machine, if your router sends that port elsewhere) receives the panel\'s login cookie with each visit, and so does every name under {{zone}}. If those are not all yours alone, give the panel a name of its own instead ({{own}}).',
              { zone, own: 'cosmos.' + zone })}
          </Alert>}
          {viaCloudflare && <FormControlLabel
            control={<Checkbox checked={panelShield} disabled={busy} onChange={(event) => setPanelShield(event.target.checked)} />}
            label={<Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
              {t('mgmt.cloudflare.review.panelShield',
                '{{host}} goes through Cloudflare\'s proxy: tell Cosmos so. It then takes visitors\' addresses from Cloudflare for this name, and refuses whoever asks this server for it directly (your local network always gets in).',
                { host: panelHost })}
            </Typography>}
          />}

          <Divider />
          <Typography variant="subtitle2">{t('mgmt.cloudflare.review.recordsTitle', 'What Cosmos will do at Cloudflare')}</Typography>
          {preview.canEdit === 'no' && <Alert severity="error">{t(zoneAccessText({ canEdit: 'no' })[1], zoneAccessText({ canEdit: 'no' })[2])}</Alert>}
          {!options.manageRecords && <Alert severity="info">
            {t('mgmt.cloudflare.review.notManaged', '"Manage records" is off: Cosmos writes no record. You create each address at Cloudflare yourself.')}
          </Alert>}
          <Stack spacing={1} data-preview-rows>
            {preview.rows.length === 0 && <Typography variant="body2" color="textSecondary">
              {t('mgmt.cloudflare.review.noRows', 'Nothing yet: no application has an address under this domain. Each address you attach later gets its record then.')}
            </Typography>}
            {preview.rows.map((row) => {
              const action = previewActionText(options.manageRecords || row.action === 'protected' || row.action === 'conflict' ? row.action : 'keep');
              const canReplace = options.manageRecords && (row.action === 'conflict' || row.action === 'replace');
              const here = row.action === 'protected' && row.proxied && row.pointsHere;
              return <Stack key={row.name} spacing={0.25} sx={{ minWidth: 0 }} data-preview-row={row.name} data-preview-action={row.action}>
                <Stack direction="row" spacing={1} alignItems="center" useFlexGap flexWrap="wrap">
                  <Typography variant="body2" sx={{ overflowWrap: 'anywhere', fontWeight: 600 }}>{row.name}</Typography>
                  {row.role === 'panel' && <Chip size="small" label={t('mgmt.cloudflare.panelChip', 'Cosmos panel')} />}
                  <Chip size="small" variant="outlined" color={action[2]} label={t(action[0], action[1])} />
                </Stack>
                <Typography variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
                  {row.current
                    ? t('mgmt.cloudflare.review.now', 'Now: {{current}}', { current: row.current }) + (row.proxied ? ' · ' + t('mgmt.cloudflare.review.proxied', 'through Cloudflare\'s proxy') : '')
                    : t('mgmt.cloudflare.review.nowNothing', 'Now: no record')}
                  {row.new && options.manageRecords ? ' → ' + (row.new === 'A'
                    ? t('mgmt.cloudflare.review.newUnknown', 'A record to this server\'s public address')
                    : row.new) : ''}
                </Typography>
                {here && <Typography variant="caption" sx={{ overflowWrap: 'anywhere' }}>
                  {t('mgmt.cloudflare.review.alreadyHere', 'This name already has a proxied record that points at this server: Cosmos will not touch it.')}
                </Typography>}
                {canReplace && <FormControlLabel
                  control={<Checkbox size="small" checked={replace.includes(row.name)} disabled={busy} onChange={() => toggleReplace(row.name)} />}
                  label={<Typography variant="caption" sx={{ overflowWrap: 'anywhere' }}>
                    {t('mgmt.cloudflare.review.replace', 'Let Cosmos replace this record with one that points at this server')}
                  </Typography>}
                />}
              </Stack>;
            })}
          </Stack>

          <Typography variant="body2" data-preview-untouched={preview.untouched.length}>
            {t('mgmt.cloudflare.review.untouchedTitle', 'Not touched: the {{count}} other records of {{zone}}', { count: preview.untouched.length + (preview.untouchedMore ? '+' : ''), zone })}
          </Typography>
          <Stack spacing={0.25} sx={{ maxHeight: 180, overflowY: 'auto', paddingRight: 1 }}>
            {preview.untouched.map((record, index) => <Typography key={index} variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
              {record.type} {record.name} → {record.content}{record.proxied ? ' · ' + t('mgmt.cloudflare.review.proxied', 'through Cloudflare\'s proxy') : ''}
              {record.apex ? ' · ' + t('mgmt.cloudflare.review.apex', 'the domain itself') : ''}
            </Typography>)}
          </Stack>

          {preview.truncated && <Alert severity="warning">{t('mgmt.cloudflare.review.truncated', 'This domain has more records than Cosmos reads (5000): the list above is not complete.')}</Alert>}
          {preview.warnings.includes('caa') && <Alert severity="warning" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.cloudflare.review.caa', 'The domain has CAA records that do not name letsencrypt.org: Let\'s Encrypt will refuse to issue certificates for it until one is added at Cloudflare.')}
          </Alert>}
          {preview.warnings.includes('wildcard') && <Alert severity="info" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.cloudflare.review.wildcard', 'The domain already has a wildcard record (*): names without a record of their own resolve to wherever it points.')}
          </Alert>}
          {preview.warnings.includes('private_address') && <Alert severity="warning" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.cloudflare.review.privateAddress', 'The records would point at a local address ({{address}}): they work on your network only.', { address: preview.address })}
          </Alert>}
          {!preview.address && options.manageRecords && <Typography variant="caption" color="textSecondary">
            {t('mgmt.cloudflare.review.addressLater', 'Cosmos does not know this server\'s public address yet: it looks it up when it writes the records.')}
          </Typography>}
          {preview.sslMode && preview.sslMode !== 'strict' && preview.sslMode !== 'origin_pull' && <Alert severity={sslModeText(preview.sslMode)[0]} sx={{ overflowWrap: 'anywhere' }}>
            {t(sslModeText(preview.sslMode)[1], sslModeText(preview.sslMode)[2])}
          </Alert>}

          <Divider />
          <Typography variant="subtitle2">{t('mgmt.cloudflare.review.settingsTitle', 'How the domain is set up')}</Typography>
          <Typography variant="body2" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.cloudflare.review.settings',
              'Certificates from Let\'s Encrypt, proven through Cloudflare\'s DNS: nothing has to be reachable on port 80 or 443 of your address. One certificate for {{zone}} and every name under it. Cosmos creates a record for each address you attach to an application and removes it when you detach it; there is no catch-all (wildcard) record.',
              { zone })}
          </Typography>
          <Link component="button" type="button" variant="body2" onClick={() => setShowOptions(!showOptions)} sx={{ alignSelf: 'flex-start' }}>
            {showOptions ? t('mgmt.cloudflare.review.hideOptions', 'Hide these settings') : t('mgmt.cloudflare.review.showOptions', 'Change these settings (advanced)')}
          </Link>
          {showOptions && <Stack spacing={0.5}>
            <FormControlLabel
              control={<Checkbox checked={options.wildcardCertificate} onChange={(event) => setOptions({ ...options, wildcardCertificate: event.target.checked })} />}
              label={<Typography variant="body2">{t('mgmt.cloudflare.options.wildcardCertificate', 'One certificate for the domain and all names under it (recommended: a new address works at once, and the names do not appear in public certificate logs)')}</Typography>}
            />
            <FormControlLabel
              control={<Checkbox checked={options.manageRecords} onChange={(event) => setOptions({ ...options, manageRecords: event.target.checked, wildcardRecord: event.target.checked && options.wildcardRecord })} />}
              label={<Typography variant="body2">{t('mgmt.cloudflare.options.manageRecords', 'Cosmos creates and updates the records of the addresses it serves (recommended). Off: you keep every record by hand.')}</Typography>}
            />
            <FormControlLabel
              control={<Checkbox checked={options.wildcardRecord} disabled={!options.manageRecords} onChange={(event) => setOptions({ ...options, wildcardRecord: event.target.checked })} />}
              label={<Typography variant="body2">{t('mgmt.cloudflare.options.wildcardRecord', 'One catch-all record (*) instead of a record per address. Not recommended: every name under the domain then leads to this server, and no address can be switched to Cloudflare\'s proxy on its own.')}</Typography>}
            />
            <Typography variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
              {t('mgmt.cloudflare.options.provided',
                'A certificate you provide (for example a Cloudflare Origin CA certificate) is possible too, with "Add a domain by hand". Browsers only accept an Origin CA certificate through Cloudflare\'s proxy: an address served directly, or opened on your local network, would show a warning.')}
            </Typography>
            <Link component="button" type="button" variant="body2" onClick={onManual} sx={{ alignSelf: 'flex-start' }}>
              {t('mgmt.cloudflare.options.manual', 'Add a domain by hand instead')}
            </Link>
          </Stack>}
        </>}

        {/* 4. done */}
        {step === 'done' && done && <>
          <Alert severity="success" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.cloudflare.done.title', '{{zone}} is connected.', { zone })}
          </Alert>
          {done.panel && <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.cloudflare.done.panel', 'The panel now also answers at {{address}}. Log in there once; the address you are using keeps working.', { address: address(done.panel) })}
          </Typography>}
          {done.through && <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.cloudflare.done.through', 'Cosmos expects visitors of the panel\'s name through Cloudflare, and refuses that name to anyone else outside your local network.')}
          </Typography>}
          {done.shieldProblem && <Alert severity="warning" sx={{ overflowWrap: 'anywhere' }}>{done.shieldProblem}</Alert>}
          <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.cloudflare.done.next',
              'Next: on the card of an application (ServApps), "Domains" gives it an address under {{zone}}, and there you choose for each address whether it goes through Cloudflare or directly. The certificate arrives within a few minutes; the list above shows it.',
              { zone })}
          </Typography>
          <Typography variant="body2" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.cloudflare.done.router',
              'From outside, every address is opened with port {{port}}. Your router must forward that port to this server (Configuration > HTTP > Internet access can ask it to).',
              { port })}
          </Typography>
        </>}

        {working && <Stack direction="row" spacing={1} alignItems="center">
          <CircularProgress size={16} color="inherit" />
          <Typography variant="body2">{working}</Typography>
        </Stack>}
        {failure && <Alert severity="error" sx={{ overflowWrap: 'anywhere' }} data-cloudflare-failure>{failure}</Alert>}
      </Stack>
    </DialogContent>
    <DialogActions>
      {step === 'token' && <>
        <Button onClick={() => onClose(false)} disabled={busy}>{t('global.cancelAction')}</Button>
        <LoadingButton variant="contained" loading={checking} disabled={!usable} onClick={check}>
          {t('mgmt.cloudflare.connect.check', 'Check the token')}
        </LoadingButton>
      </>}
      {step === 'domain' && <>
        <Button onClick={() => { setStep('token'); setReport(null); setZone(''); }} disabled={busy}>{t('mgmt.cloudflare.connect.back', 'Back')}</Button>
        <Button onClick={() => onClose(false)} disabled={busy}>{t('global.cancelAction')}</Button>
      </>}
      {step === 'review' && <>
        <Button onClick={() => setStep('domain')} disabled={busy}>{t('mgmt.cloudflare.connect.back', 'Back')}</Button>
        <Button onClick={() => onClose(false)} disabled={busy}>{t('global.cancelAction')}</Button>
        <LoadingButton
          variant="contained"
          loading={!!working}
          disabled={previewing || !panelValid || !preview || preview.canEdit === 'no'}
          onClick={connect}
        >
          {counts.written + counts.replaced + counts.removed > 0 && options.manageRecords
            ? t('mgmt.cloudflare.connect.go', 'Connect and write {{count}} records', { count: counts.written + counts.replaced + counts.removed })
            : t('mgmt.cloudflare.connect.goNothing', 'Connect')}
        </LoadingButton>
      </>}
      {step === 'done' && <Button variant="contained" onClick={() => onClose(true)}>{t('mgmt.cloudflare.connect.close', 'Close')}</Button>}
    </DialogActions>
  </Dialog>;
};

export default CloudflareOverview;

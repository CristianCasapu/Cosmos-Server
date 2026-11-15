// Fork addition: "Block abusers at Cloudflare" (Configuration > Domains,
// after the sign-in check's card; server side src/edgeblock, words in
// utils/edgeBlocks.js), and the "Block at Cloudflare" action of the security
// dashboard.
//
// Off until switched on; switching on is a test with each domain's token
// (list, make and delete a rule) that says which permission is missing. The
// list shows what Cosmos holds at Cloudflare now, with "Unblock" per line
// and for everything.
import * as React from 'react';
import dayjs from 'dayjs';
import {
  Alert, Box, Button, Chip, Collapse, Dialog, DialogActions, DialogContent, DialogTitle, Divider, Link, Stack, Tooltip, Typography, useMediaQuery,
} from '@mui/material';
import { LoadingButton } from '@mui/lab';
import {
  CheckCircleOutlined, CloseCircleOutlined, DownOutlined, ExclamationCircleOutlined, InfoCircleOutlined, StopOutlined, UpOutlined,
} from '@ant-design/icons';
import { useTheme } from '@mui/material/styles';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import MainCard from './MainCard';
import ChoiceChips from './choiceChips';
import PermissionGuard from './permissionGuard';
import PrettyTableView from './tableView/prettyTableView';
import { ConfirmModalDirect } from './confirmModal';
import { useClientInfos } from '../utils/hooks';
import { PERM_ADMIN_READ, PERM_CONFIGURATION, PERM_CONFIGURATION_READ } from '../utils/permissions';
import {
  DURATIONS, MANUAL_MODES, PERMISSION_NAME, PROOF_LEFT, TOKENS_URL, blockOf, canUnblock, failureText, jailText, looksLocal,
  modeLabel, problemText, proofText, ruleLabel, rulesShown, stateLabel, stateText,
} from '../utils/edgeBlocks';

const when = (value) => (value ? dayjs(value).format('L LT') : '');

// a text triple, with the parts it names translated too
const say = (t, text) => {
  if (!text) return '';
  const values = { ...(text[2] || {}) };
  if (values.whyKey) values.why = t(values.whyKey, values.why);
  if (values.stepKey) values.step = t(values.stepKey, values.step);
  if (values.problemKey) values.problem = t(values.problemKey, values.problem, values);
  if (values.whatKey) values.what = t(values.whatKey, values.what);
  if (values.modeKey) values.mode = t(values.modeKey, values.mode).toLowerCase();
  ['window', 'length'].forEach((name) => {
    if (Array.isArray(values[name])) values[name] = t(values[name][0], values[name][1], values[name][2] || {});
  });
  return t(text[0], text[1], values);
};

const wrap = { overflowWrap: 'anywhere', '& .MuiAlert-message': { minWidth: 0 } };

// --- one state for the page (the card and the dashboard's buttons)
let store = { data: null, at: 0, loading: false };
const listeners = new Set();
const emit = () => listeners.forEach((listener) => listener({ ...store }));

const load = (force) => {
  if (store.loading || (!force && Date.now() - store.at < 15000)) return Promise.resolve(store.data);
  store = { ...store, loading: true };
  return API.config.edgeBlocks().then((res) => {
    store = { data: (res && res.ok && res.data) || store.data, at: Date.now(), loading: false };
    emit();
    return store.data;
  }).catch(() => {
    store = { ...store, at: Date.now(), loading: false };
    emit();
    return store.data;
  });
};

const setStatus = (data) => {
  if (!data) return;
  store = { data, at: Date.now(), loading: false };
  emit();
};

export const useEdgeBlocks = () => {
  const { hasPermission } = useClientInfos();
  const allowed = hasPermission(PERM_CONFIGURATION_READ);
  const [state, setState] = React.useState({ ...store });
  React.useEffect(() => {
    if (!allowed) return undefined;
    listeners.add(setState);
    load(false);
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

const Door = ({ open, onClick, children, id }) => <Button
  size="small" onClick={onClick} endIcon={open ? <UpOutlined /> : <DownOutlined />}
  sx={{ alignSelf: 'flex-start', textAlign: 'left' }} aria-expanded={open} aria-controls={id}
>{children}</Button>;

const PermissionSteps = () => {
  const { t } = useTranslation();
  return <Stack component="ol" spacing={0.5} sx={{ pl: 2.5, m: 0 }}>
    <Typography component="li" variant="body2" sx={{ overflowWrap: 'anywhere' }}>
      {t('mgmt.edge.steps.open', 'Open Cloudflare\'s dashboard at My Profile > API Tokens:')}{' '}
      <Link href={TOKENS_URL} target="_blank" rel="noopener noreferrer">{TOKENS_URL.replace('https://', '')}</Link>
    </Typography>
    <Typography component="li" variant="body2" sx={{ overflowWrap: 'anywhere' }}>
      {t('mgmt.edge.steps.edit', 'Edit the token Cosmos uses for the domain (the one with "Zone > DNS > Edit").')}
    </Typography>
    <Typography component="li" variant="body2" sx={{ overflowWrap: 'anywhere' }}>
      {t('mgmt.edge.steps.add', 'Add the permission "{{permission}}", keep "Zone Resources" on this domain only, and save. The token itself stays the same.', { permission: PERMISSION_NAME })}
    </Typography>
    <Typography component="li" variant="body2" sx={{ overflowWrap: 'anywhere' }}>
      {t('mgmt.edge.steps.test', 'Come back here and press "Test and switch on".')}
    </Typography>
  </Stack>;
};

const RulesList = ({ status, busy, onAction }) => {
  const { t } = useTranslation();
  const narrow = useMediaQuery((theme) => theme.breakpoints.down('sm'));
  const [confirm, setConfirm] = React.useState(null);
  const rules = rulesShown(status);
  return <Stack spacing={1}>
    {confirm && <ConfirmModalDirect
      content={confirm === '*'
        ? t('mgmt.edge.unblockAll.confirm', 'Lift every block Cosmos made at Cloudflare? The addresses can reach the server through Cloudflare again (SmartShield still applies).')
        : t('mgmt.edge.unblock.confirm', 'Lift the block of {{key}} at Cloudflare?', { key: confirm })}
      callback={() => onAction(confirm === '*' ? { action: 'unblockAll' } : { action: 'unblock', key: confirm })}
      onClose={() => setConfirm(null)}
    />}
    <Stack direction="row" spacing={1} useFlexGap flexWrap="wrap" alignItems="center" justifyContent="space-between">
      <Typography variant="subtitle1">{t('mgmt.edge.list.title', 'Blocked at Cloudflare now')}</Typography>
      {rules.some(canUnblock) && <PermissionGuard permission={PERM_CONFIGURATION}>
        <Button size={narrow ? 'medium' : 'small'} variant="outlined" color="error" disabled={!!busy} onClick={() => setConfirm('*')} data-edge-action="unblockAll">
          {t('mgmt.edge.unblockAll', 'Unblock everything Cosmos created')}
        </Button>
      </PermissionGuard>}
    </Stack>
    <PrettyTableView
      data={rules}
      getKey={(rule) => rule.key + ' ' + rule.zone}
      empty={{ title: t('mgmt.edge.list.emptyTitle', 'Nothing is blocked at Cloudflare'), text: t('mgmt.edge.list.emptyText', 'Addresses that come through Cloudflare and keep offending are listed here while Cloudflare blocks or challenges them.') }}
      columns={[
        {
          title: t('mgmt.edge.list.address', 'Address'),
          search: (rule) => rule.key + ' ' + (rule.address || ''),
          field: (rule) => <Stack spacing={0.25} data-edge-rule={rule.key}>
            <strong style={{ overflowWrap: 'anywhere' }}>{rule.key}</strong>
            {rule.address && rule.address !== rule.key && <Typography variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
              {t('mgmt.edge.list.seenAs', 'seen as {{address}}', { address: rule.address })}
            </Typography>}
          </Stack>,
        },
        {
          title: t('mgmt.edge.list.why', 'Why'),
          search: (rule) => t(...ruleLabel(rule)),
          field: (rule) => <Stack spacing={0.5} alignItems="flex-start">
            <span style={{ overflowWrap: 'anywhere' }}>{t(...ruleLabel(rule))}{rule.actor ? ' (' + rule.actor + ')' : ''}</span>
            <Chip size="small" variant="outlined" color={rule.mode === 'block' ? 'error' : 'warning'} label={t(...modeLabel(rule))} />
          </Stack>,
        },
        {
          title: t('mgmt.edge.list.when', 'Since – until'),
          field: (rule) => <span style={{ overflowWrap: 'anywhere' }}>{when(rule.since)} – {when(rule.until)}</span>,
        },
        {
          title: t('mgmt.edge.list.state', 'State'),
          screenMin: 'md',
          field: (rule) => <span style={{ overflowWrap: 'anywhere' }} data-edge-state={rule.state}>{t(...stateLabel(rule))}</span>,
        },
        {
          title: '',
          clickable: true,
          style: { textAlign: 'right' },
          field: (rule) => (canUnblock(rule) ? <PermissionGuard permission={PERM_CONFIGURATION}>
            <Button size={narrow ? 'medium' : 'small'} variant="outlined" disabled={!!busy} onClick={() => setConfirm(rule.key)} data-edge-unblock={rule.key}>
              {t('mgmt.edge.unblock', 'Unblock')}
            </Button>
          </PermissionGuard> : (rule.state === 'orphaned' ? <PermissionGuard permission={PERM_CONFIGURATION}>
            <Button size={narrow ? 'medium' : 'small'} disabled={!!busy} onClick={() => onAction({ action: 'forget', key: rule.key })}>
              {t('mgmt.edge.forget', 'Forget')}
            </Button>
          </PermissionGuard> : null)),
        },
      ]}
    />
  </Stack>;
};

export const EdgeBlocksCard = () => {
  const { t } = useTranslation();
  const status = useEdgeBlocks();
  const { hasPermission } = useClientInfos();
  const [busy, setBusy] = React.useState('');
  const [notice, setNotice] = React.useState(null);
  const [proofs, setProofs] = React.useState(null);
  const [rulesOpen, setRulesOpen] = React.useState(false);
  const [stepsOpen, setStepsOpen] = React.useState(false);

  if (!status) return null;

  const act = async (body) => {
    setBusy(body.action);
    const res = await API.config.edgeBlocksChange(body);
    setBusy('');
    if (res && res.ok) {
      const data = res.data && res.data.status ? res.data.status : res.data;
      setStatus(data);
      setProofs(res.data && res.data.results ? res.data.results : null);
      setNotice(body.action === 'enable' ? { severity: 'success', text: t('mgmt.edge.enabled', 'Switched on: the token passed the test.') } : null);
    } else {
      setNotice({ severity: 'error', text: say(t, failureText(res && res.raw ? { ...res.raw, reason: res.reason } : res)) });
      setProofs(res && res.raw && res.raw.results ? res.raw.results : null);
      load(true);
    }
  };

  const state = stateText(status);
  const zones = status.zones || [];
  const needsPermission = zones.some((zone) => !zone.proven) || (proofs || []).some((p) => !p.ok && p.reason === 'permission');

  return <MainCard title={t('mgmt.edge.title', 'Block abusers at Cloudflare')}>
    <Stack spacing={2} data-edge-card>
      <Typography variant="body2" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
        {t('mgmt.edge.intro', 'Visitors who come through Cloudflare\'s proxy cannot be banned by Fail2ban: their connections are Cloudflare\'s. Switched on, an address that keeps offending through Cloudflare (failed sign-ins, probes, scans, abuse SmartShield stopped) is challenged or blocked by Cloudflare itself, for a limited time, before anything reaches your home connection. Direct visitors stay with Fail2ban.')}
      </Typography>

      {state && <Stack direction="row" spacing={1} alignItems="flex-start" data-edge-state-line={status.enabled ? 'on' : 'off'}>
        <LevelIcon level={state[0]} />
        <Typography variant="body2" sx={{ overflowWrap: 'anywhere', minWidth: 0 }}>{say(t, state[1])}</Typography>
      </Stack>}

      {zones.length > 0 && <Stack spacing={0.75}>
        {zones.map((zone) => <Stack key={zone.name} spacing={0.5} data-edge-zone={zone.name}>
          <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
            {zone.proven
              ? t('mgmt.edge.zone.proven', '{{zone}} ({{count}} addresses through Cloudflare): the token passed the test on {{at}}.', { zone: zone.name, count: zone.shielded, at: when(zone.provenAt) })
              : (zone.tokenChanged
                ? t('mgmt.edge.zone.changed', '{{zone}}: the token changed since its test; test it again.', { zone: zone.name })
                : t('mgmt.edge.zone.untested', '{{zone}} ({{count}} addresses through Cloudflare): not tested yet.', { zone: zone.name, count: zone.shielded }))}
          </Typography>
          {zone.problem && <Alert severity="warning" sx={wrap} data-edge-problem={zone.problem.reason}>{say(t, problemText(zone.problem, when))}</Alert>}
        </Stack>)}
      </Stack>}

      {!status.originPulls && zones.length > 0 && <Alert severity="info" sx={wrap} data-edge-pulls>
        {t('mgmt.edge.pulls', 'The visitor\'s address comes from a header Cloudflare sets. Someone with an account at Cloudflare could, with paid features, send requests for your names with an address of their choosing and get that address challenged at your domain (never elsewhere). Enforcing "Proof that a request came through Cloudflare" with a certificate of your own (in the Cloudflare card) closes that. Until then, administrators\' addresses are never blocked and first offences are challenges a person passes.')}
      </Alert>}

      {proofs && proofs.map((result) => <Alert key={result.zone} severity={result.ok ? 'success' : 'error'} sx={wrap} data-edge-proof={result.ok ? 'ok' : result.reason}>
        {say(t, proofText(result))}{result.leftTestRule ? ' ' + t(PROOF_LEFT[0], PROOF_LEFT[1]) : ''}
      </Alert>)}
      {notice && <Alert severity={notice.severity} sx={wrap} data-edge-notice={notice.severity}>{notice.text}</Alert>}
      {status.dropped > 0 && <Alert severity="warning" sx={wrap} data-edge-dropped>
        {t('mgmt.edge.dropped', '{{count}} blocks were not made because of the limits (at most {{perHour}} new blocks an hour and {{max}} rules at once); the last on {{at}}. SmartShield still refused those addresses.', { count: status.dropped, perHour: status.maxPerHour, max: status.maxRules, at: when(status.droppedAt) })}
      </Alert>}

      <Door id="edge-rules" open={rulesOpen} onClick={() => setRulesOpen(!rulesOpen)}>{t('mgmt.edge.rules.door', 'What leads to a block, and what never is blocked')}</Door>
      <Collapse id="edge-rules" in={rulesOpen} unmountOnExit>
        <Stack spacing={0.75}>
          <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.edge.rules.count', 'Only requests that came through Cloudflare for an address served through it count, with the thresholds of the Fail2ban jails (never a single request):')}
          </Typography>
          <Stack component="ul" spacing={0.25} sx={{ pl: 2.5, m: 0 }}>
            {(status.jails || []).map((jail) => <Typography key={jail.name} component="li" variant="body2" sx={{ overflowWrap: 'anywhere' }}>{say(t, jailText(jail))}</Typography>)}
          </Stack>
          <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.edge.rules.v6', 'An IPv6 address is blocked with its whole /64, so that changing addresses inside it does not help. Rules end by themselves: Cosmos deletes them when the time is up.')}
          </Typography>
          <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.edge.rules.never', 'Never blocked: the local network, this server\'s own addresses, Cloudflare\'s addresses, SmartShield\'s whitelist, the Fail2ban never-ban list, and any address an administrator signed in from in the last {{days}} days. A block of an address an administrator then signs in from is lifted at once.', { days: status.adminDays })}
          </Typography>
          <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.edge.rules.limits', 'At most {{max}} rules at once and {{perHour}} new blocks an hour; rules you delete in Cloudflare\'s dashboard are not made again for the same offence, and rules of your own are never touched.', { max: status.maxRules, perHour: status.maxPerHour })}
          </Typography>
        </Stack>
      </Collapse>

      <PermissionGuard permission={PERM_CONFIGURATION}>
        <Stack spacing={1.5}>
          {needsPermission && zones.length > 0 && !status.enabled && <>
            <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }} data-edge-permission>
              {t('mgmt.edge.permission', 'The token Cosmos keeps for a domain may edit its DNS records only. Blocking at Cloudflare needs one more permission: "{{permission}}".', { permission: PERMISSION_NAME })}
            </Typography>
            <Door id="edge-steps" open={stepsOpen} onClick={() => setStepsOpen(!stepsOpen)}>{t('mgmt.edge.steps.door', 'How to add it')}</Door>
            <Collapse id="edge-steps" in={stepsOpen} unmountOnExit><PermissionSteps /></Collapse>
          </>}
          <Stack direction="row" spacing={1} useFlexGap flexWrap="wrap">
            {!status.enabled && zones.length > 0 && <LoadingButton variant="contained" size="small" loading={busy === 'enable'} disabled={!!busy} onClick={() => act({ action: 'enable' })} data-edge-action="enable">
              {t('mgmt.edge.enable', 'Test and switch on')}
            </LoadingButton>}
            {status.enabled && <LoadingButton variant="outlined" size="small" loading={busy === 'disable'} disabled={!!busy} onClick={() => act({ action: 'disable' })} data-edge-action="disable">
              {t('mgmt.edge.disable', 'Switch off and lift every block')}
            </LoadingButton>}
            {zones.some((zone) => zone.problem) && <LoadingButton variant="outlined" size="small" loading={busy === 'retry'} disabled={!!busy} onClick={() => act({ action: 'retry' })} data-edge-action="retry">
              {t('mgmt.edge.retry', 'Try again now')}
            </LoadingButton>}
          </Stack>
          {!status.enabled && zones.length > 0 && <Typography variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.edge.enable.help', 'The test makes a challenge rule for the documentation address 192.0.2.1 at Cloudflare and deletes it at once.')}
          </Typography>}
        </Stack>
      </PermissionGuard>

      {hasPermission(PERM_ADMIN_READ) && status.withAddresses && <>
        <Divider flexItem />
        <RulesList status={status} busy={busy} onAction={act} />
        {(status.leftovers || []).length > 0 && <Alert severity="info" sx={wrap} data-edge-leftovers>
          {t('mgmt.edge.leftovers', 'Cloudflare holds {{count}} rules with Cosmos\' mark that this server has no record of (from before its record was lost): Cosmos does not touch them. Delete them in Cloudflare\'s dashboard (Security > WAF > Tools) if you no longer want them: {{list}}.', { count: status.leftovers.length, list: status.leftovers.slice(0, 10).map((l) => l.value).join(', ') })}
        </Alert>}
        {rulesShown(status).some((rule) => rule.state === 'orphaned') && <Alert severity="warning" sx={wrap}>
          {t('mgmt.edge.orphaned', 'Some rules stayed at Cloudflare because their domain has no token any more. Delete them in Cloudflare\'s dashboard (Security > WAF > Tools), then press "Forget" on their line.')}
        </Alert>}
      </>}
    </Stack>
  </MainCard>;
};

// The "Block at Cloudflare" action of the security dashboard, for one address.
export const EdgeBlockButton = ({ address, narrow, onDone }) => {
  const { t } = useTranslation();
  const status = useEdgeBlocks();
  const [open, setOpen] = React.useState(false);
  const [duration, setDuration] = React.useState('1d');
  const [mode, setMode] = React.useState('block');
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState('');
  if (!status || !status.enabled || !address || looksLocal(address)) return null;
  const label = t('mgmt.edge.block.button', 'Block at Cloudflare');
  const send = async () => {
    setBusy(true);
    setError('');
    const res = await API.config.edgeBlocksChange({ action: 'block', address, duration, mode });
    setBusy(false);
    if (res && res.ok) {
      setStatus(res.data);
      setOpen(false);
      if (onDone) onDone();
    } else {
      setError(say(t, failureText(res && res.raw ? { ...res.raw, reason: res.reason } : res)));
    }
  };
  const compact = narrow ? { minWidth: 0, px: 1, '& .MuiButton-startIcon': { m: 0 } } : {};
  return <PermissionGuard permission={PERM_CONFIGURATION}>
    <Tooltip title={label}>
      <Button size="small" variant="outlined" color="error" sx={compact} startIcon={<StopOutlined />} onClick={() => setOpen(true)} aria-label={narrow ? label : undefined} data-edge-block={address}>
        {!narrow && label}
      </Button>
    </Tooltip>
    {open && <Dialog open onClose={() => setOpen(false)} fullWidth maxWidth="sm">
      <DialogTitle>{t('mgmt.edge.block.title', 'Block {{address}} at Cloudflare', { address })}</DialogTitle>
      <DialogContent dividers>
        <Stack spacing={2}>
          <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.edge.block.what', 'Cloudflare stops this address before it reaches the server, for every address of your domains served through Cloudflare. Direct connections are not concerned (Fail2ban\'s). An IPv6 address is blocked with its /64.')}
          </Typography>
          <ChoiceChips id="edge-block-duration" label={t('mgmt.edge.block.duration', 'For how long')} value={duration} onChange={setDuration}
            options={DURATIONS.map((d) => ({ value: d.value, label: t(...d.label) }))} />
          <ChoiceChips id="edge-block-mode" label={t('mgmt.edge.block.mode', 'What Cloudflare does')} value={mode} onChange={setMode}
            options={MANUAL_MODES.map((m) => ({ value: m.value, label: t(...m.label), description: t(...m.description) }))} />
          {error && <Alert severity="error" sx={wrap} data-edge-block-error>{error}</Alert>}
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={() => setOpen(false)}>{t('global.cancelAction')}</Button>
        <LoadingButton variant="contained" color="error" loading={busy} onClick={send} data-edge-block-confirm>{t('mgmt.edge.block.confirm', 'Block')}</LoadingButton>
      </DialogActions>
    </Dialog>}
  </PermissionGuard>;
};

// The chip "Blocked at Cloudflare until …" for an address of the dashboard.
export const EdgeBlockedChip = ({ address }) => {
  const { t } = useTranslation();
  const status = useEdgeBlocks();
  if (!status || !status.rules) return null;
  const blocked = {};
  status.rules.forEach((rule) => {
    if (rule.state === 'active' || rule.state === 'pending') blocked[rule.key] = rule;
  });
  const rule = blockOf(blocked, address);
  if (!rule) return null;
  return <Box><Chip size="small" color={rule.mode === 'block' ? 'error' : 'warning'} variant="outlined" data-edge-chip={rule.key}
    label={t('mgmt.edge.chip', 'At Cloudflare until {{until}}', { until: when(rule.until) })} /></Box>;
};

export const reloadEdgeBlocks = () => load(true);

export default EdgeBlocksCard;

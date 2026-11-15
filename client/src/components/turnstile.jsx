// Fork addition: the settings card of Cloudflare Turnstile on the public
// forms (Configuration > Domains, after the Cloudflare card; server side
// src/turnstile, sentences in utils/turnstile.js), and the warning on the
// home page when it does not work.
//
// The order is the one the server enforces: keys (made by Cosmos through
// Cloudflare's API when the token may, else pasted by hand, the secret
// tested before it is kept), a check made in this browser with those keys,
// then "Switch on". Saying what a visitor sees, which forms ask, and how the
// owner gets in when something goes wrong.
import * as React from 'react';
import dayjs from 'dayjs';
import {
  Alert,
  Button,
  Checkbox,
  Collapse,
  Divider,
  FormControlLabel,
  Link,
  Stack,
  Typography,
} from '@mui/material';
import { LoadingButton } from '@mui/lab';
import { CheckCircleOutlined, CloseCircleOutlined, DownOutlined, ExclamationCircleOutlined, InfoCircleOutlined, UpOutlined } from '@ant-design/icons';
import { useTheme } from '@mui/material/styles';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import MainCard from './MainCard';
import ChoiceChips from './choiceChips';
import { FormField } from './formField';
import SecretInput, { SECRET_MASK } from './secretInput';
import PermissionGuard from './permissionGuard';
import { TurnstileBox } from './turnstileWidget';
import { useClientInfos } from '../utils/hooks';
import { PERM_CONFIGURATION, PERM_CONFIGURATION_READ } from '../utils/permissions';
import {
  APPEARANCES, DASHBOARD_URL, FORM_LABELS, MODES, TRY_AFTER_RELOAD, manualSteps, problemText, reasonText, stateText, statusText, tryAddress, visitorSees,
} from '../utils/turnstile';

const say = (t, text) => (text ? t(text[0], text[1], text[2] || {}) : '');
const wrap = { overflowWrap: 'anywhere', '& .MuiAlert-message': { minWidth: 0 } };

// --- one state for the page (the card and the home page's warning)
let store = { data: null, at: 0, loading: false };
const listeners = new Set();
const emit = () => listeners.forEach((listener) => listener({ ...store }));

export const loadTurnstileStatus = (force) => {
  if (store.loading || (!force && Date.now() - store.at < 15000)) return Promise.resolve(store.data);
  store = { ...store, loading: true };
  return API.config.turnstileStatus().then((res) => {
    store = { data: (res && res.ok && res.data) || store.data, at: Date.now(), loading: false };
    emit();
    return store.data;
  }).catch(() => {
    store = { ...store, at: Date.now(), loading: false };
    emit();
    return store.data;
  });
};

const useTurnstileStatus = () => {
  const { hasPermission } = useClientInfos();
  const allowed = hasPermission(PERM_CONFIGURATION_READ);
  const [state, setState] = React.useState({ ...store });
  React.useEffect(() => {
    if (!allowed) return undefined;
    listeners.add(setState);
    loadTurnstileStatus(false);
    setState({ ...store });
    return () => { listeners.delete(setState); };
  }, [allowed]);
  return allowed ? state.data : null;
};

// The server's script policy allows Cloudflare's check only while a site key is
// saved, and this page keeps the policy it was opened with: once keys are saved
// here where there were none, the page is opened again before the first try.
let policyStale = false;
const keysSaved = (before, res) => { if (res && res.ok && !before.siteKey) policyStale = true; };
const storage = () => { try { return window.sessionStorage; } catch (e) { return null; } };

const setStatus = (data) => {
  if (!data) return;
  store = { data, at: Date.now(), loading: false };
  emit();
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
  size="small"
  onClick={onClick}
  endIcon={open ? <UpOutlined /> : <DownOutlined />}
  sx={{ alignSelf: 'flex-start', textAlign: 'left' }}
  aria-expanded={open}
  aria-controls={id}
>
  {children}
</Button>;

const problemValues = (problem) => (problem ? { ...problem, since: problem.since ? dayjs(problem.since).format('YYYY-MM-DD HH:mm') : '' } : problem);

// The home page's warning for administrators (Cloudflare refuses the key, cannot
// be reached, or the check was switched off from the console), as lines of
// its notice area (components/homeNotices.jsx).
export const useTurnstileNotices = () => {
  const { t } = useTranslation();
  const status = useTurnstileStatus();
  if (!status || !status.enabled) return { items: [] };
  const found = status.consoleOff
    ? [['warning', 'console', say(t, stateText(status)[1])]]
    : (status.problems || []).map((problem) => ['error', problem.reason, say(t, problemText(problemValues(problem), true))]);
  return {
    items: found.map(([severity, key, text]) => ({
      id: 'turnstile:' + key,
      severity,
      attrs: { 'data-turnstile-alert': key },
      text: <span style={{ overflowWrap: 'anywhere' }}>{t('mgmt.turnstile.alert.title', 'Sign-in check of Cloudflare:')} {text}</span>,
      actions: <Link href="/cosmos-ui/config-general/https" className="ct-notice-link">{t('mgmt.turnstile.alert.open', 'Open the settings')}</Link>,
    })),
  };
};

// --- the card

const KeysByHand = ({ status, onAnswer, busy, setBusy }) => {
  const { t } = useTranslation();
  const [siteKey, setSiteKey] = React.useState(status.siteKey || '');
  const [secret, setSecret] = React.useState(status.secretKey ? SECRET_MASK : '');
  React.useEffect(() => { setSiteKey(status.siteKey || ''); setSecret(status.secretKey ? SECRET_MASK : ''); }, [status.siteKey, status.secretKey]);
  const save = async () => {
    setBusy('keys');
    const res = await API.config.turnstileChange({ action: 'keys', siteKey: siteKey.trim(), secretKey: secret.trim() });
    setBusy('');
    keysSaved(status, res);
    onAnswer(res);
  };
  const ready = siteKey.trim() !== '' && secret.trim() !== '';
  return <Stack spacing={1.5}>
    <Stack component="ol" spacing={0.5} sx={{ pl: 2.5, m: 0 }}>
      {manualSteps(status.panelHost).map((step) => <Typography key={step[0]} component="li" variant="body2" sx={{ overflowWrap: 'anywhere' }}>{say(t, step)}</Typography>)}
    </Stack>
    <Link href={DASHBOARD_URL} target="_blank" rel="noopener noreferrer" variant="body2">
      {t('mgmt.turnstile.manual.open', 'Open Turnstile in Cloudflare\'s dashboard')}
    </Link>
    <FormField
      id="turnstile-site-key"
      label={t('mgmt.turnstile.siteKey', 'Site key')}
      value={siteKey}
      onChange={setSiteKey}
      onEnter={() => ready && save()}
      helperText={t('mgmt.turnstile.siteKey.help', 'Public: it is part of every sign-in page. Starts with 0x.')}
      inputProps={{ maxLength: 100, autoCapitalize: 'none', spellCheck: false, autoComplete: 'off' }}
    />
    <SecretInput
      id="turnstile-secret-key"
      label={t('mgmt.turnstile.secretKey', 'Secret key')}
      value={secret}
      stored={!!status.secretKey}
      onChange={setSecret}
      helperText={t('mgmt.turnstile.secretKey.help', 'Kept on the server only, never shown again. Cosmos asks Cloudflare whether it knows it before keeping it.')}
    />
    <LoadingButton variant="contained" size="small" sx={{ alignSelf: 'flex-start' }} loading={busy === 'keys'} disabled={!!busy || !ready} onClick={save} data-turnstile-action="keys">
      {t('mgmt.turnstile.saveKeys', 'Test and save the keys')}
    </LoadingButton>
  </Stack>;
};

const CreateWidget = ({ status, onAnswer, busy, setBusy }) => {
  const { t } = useTranslation();
  const [mode, setMode] = React.useState(status.mode || 'managed');
  const create = async () => {
    setBusy('create');
    const res = await API.config.turnstileChange({ action: 'create', mode });
    setBusy('');
    keysSaved(status, res);
    onAnswer(res);
  };
  return <Stack spacing={1.5}>
    <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
      {t('mgmt.turnstile.create.what', 'Cosmos asks Cloudflare, with the token it keeps for {{zone}}, to make a widget named "Cosmos sign-in" for exactly {{host}} (not the whole domain, no other name). It needs the token permission "Account > Turnstile > Edit", which the usual DNS token does not have: Cosmos then says so.', { zone: status.zone, host: status.panelHost })}
    </Typography>
    <ChoiceChips
      id="turnstile-mode"
      label={t('mgmt.turnstile.mode', 'Widget mode')}
      value={mode}
      onChange={setMode}
      options={MODES.map((m) => ({ value: m.value, label: say(t, m.label), description: say(t, m.description) }))}
    />
    <LoadingButton variant="contained" size="small" sx={{ alignSelf: 'flex-start' }} loading={busy === 'create'} disabled={!!busy} onClick={create} data-turnstile-action="create">
      {t('mgmt.turnstile.create.button', 'Create the widget for me')}
    </LoadingButton>
  </Stack>;
};

const TryIt = ({ status, onAnswer, busy, setBusy }) => {
  const { t } = useTranslation();
  // opened again for the try (see policyStale): the try starts by itself
  const [afterReload] = React.useState(() => {
    const store = storage();
    const asked = !!store && store.getItem(TRY_AFTER_RELOAD) === '1';
    if (asked) store.removeItem(TRY_AFTER_RELOAD);
    return asked;
  });
  const [open, setOpen] = React.useState(afterReload);
  const [widgetState, setWidgetState] = React.useState('loading');
  const [resetKey, setResetKey] = React.useState(afterReload ? 1 : 0);
  const trial = React.useRef(null);
  React.useEffect(() => {
    if (afterReload && trial.current && trial.current.scrollIntoView) trial.current.scrollIntoView({ block: 'center' });
  }, [afterReload]);
  const start = () => {
    const store = storage();
    if (policyStale && store) {
      store.setItem(TRY_AFTER_RELOAD, '1');
      window.location.reload();
      return;
    }
    setOpen(true);
    setResetKey((k) => k + 1);
  };
  const elsewhere = tryAddress(status, typeof window !== 'undefined' ? window.location : null);
  if (elsewhere) {
    return <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }} data-turnstile-try="elsewhere">
      {t('mgmt.turnstile.try.elsewhere', 'The check can only be tried where visitors from the internet sign in: open this page at the panel\'s public address,')}{' '}
      <Link href={elsewhere}>{elsewhere}</Link>
    </Typography>;
  }
  const send = async (token) => {
    if (!token) return;
    setBusy('try');
    const res = await API.config.turnstileChange({ action: 'try', token });
    setBusy('');
    setOpen(false);
    onAnswer(res);
  };
  return <Stack spacing={1}>
    {!open && <Button variant="outlined" size="small" sx={{ alignSelf: 'flex-start' }} disabled={!!busy} onClick={start} data-turnstile-action="try">
      {t('mgmt.turnstile.try.button', 'Try the check now')}
    </Button>}
    {open && <Stack spacing={1} data-turnstile-trial ref={trial}>
      <TurnstileBox siteKey={status.siteKey} form="test" appearance="always" resetKey={resetKey} onToken={send} onState={setWidgetState} />
      <Typography variant="caption" color={['loading', 'checking', 'ready'].includes(widgetState) ? 'textSecondary' : 'error'} role="status" aria-live="polite" sx={{ overflowWrap: 'anywhere' }}>
        {say(t, statusText(widgetState, typeof window !== 'undefined' ? window.location.hostname : ''))}
      </Typography>
      {widgetState === 'policy' && <Button size="small" variant="outlined" sx={{ alignSelf: 'flex-start' }} onClick={() => window.location.reload()}>
        {t('turnstile.widget.reloadPage', 'Reload the page')}
      </Button>}
      <Button size="small" sx={{ alignSelf: 'flex-start' }} onClick={() => setOpen(false)}>{t('mgmt.turnstile.try.cancel', 'Cancel')}</Button>
    </Stack>}
  </Stack>;
};

const Forms = ({ status, onAnswer, busy, setBusy }) => {
  const { t } = useTranslation();
  const [forms, setForms] = React.useState(status.forms);
  const [appearance, setAppearance] = React.useState(status.appearance);
  React.useEffect(() => { setForms(status.forms); setAppearance(status.appearance); }, [status.forms, status.appearance]);
  const changed = JSON.stringify(forms) !== JSON.stringify(status.forms) || appearance !== status.appearance;
  const save = async () => {
    setBusy('settings');
    const res = await API.config.turnstileChange({ action: 'settings', forms, appearance });
    setBusy('');
    onAnswer(res);
  };
  return <Stack spacing={1}>
    <Typography variant="subtitle1">{t('mgmt.turnstile.forms', 'Which forms ask')}</Typography>
    <Stack>
      {['login', 'reset', 'register'].map((form) => <FormControlLabel
        key={form}
        control={<Checkbox checked={!!forms[form]} onChange={(event) => setForms({ ...forms, [form]: event.target.checked })} />}
        label={say(t, FORM_LABELS[form])}
      />)}
    </Stack>
    <Typography variant="caption" color="textSecondary">
      {t('mgmt.turnstile.forms.note', 'Not asked: the two-factor code (it follows a right password), the password confirmation of a logged-in session, API tokens, and anyone on the home network.')}
    </Typography>
    <ChoiceChips
      id="turnstile-appearance"
      label={t('mgmt.turnstile.appearance', 'The box on the form')}
      value={appearance}
      onChange={setAppearance}
      options={APPEARANCES.map((a) => ({ value: a.value, label: say(t, a.label), description: say(t, a.description) }))}
    />
    <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }} data-turnstile-sees>{say(t, visitorSees(status.mode, appearance))}</Typography>
    {changed && <LoadingButton variant="contained" size="small" sx={{ alignSelf: 'flex-start' }} loading={busy === 'settings'} disabled={!!busy} onClick={save} data-turnstile-action="settings">
      {t('mgmt.turnstile.forms.save', 'Save these choices')}
    </LoadingButton>}
  </Stack>;
};

const LockOutHelp = ({ status }) => {
  const { t } = useTranslation();
  return <Stack spacing={0.5}>
    <Typography variant="body2" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
      {t('mgmt.turnstile.lockout.home', 'The home network is never asked (and neither are Tailscale or a VPN into it): at home you always sign in, with or without the internet.')}
    </Typography>
    <Typography variant="body2" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
      {t('mgmt.turnstile.lockout.closed', 'When Cloudflare cannot be reached, visitors from the internet cannot sign in until it can (letting them through then would let anyone through who can make the check fail). You are told on the home page, in the notifications and on Telegram.')}
    </Typography>
    <Typography variant="body2" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
      {t('mgmt.turnstile.lockout.console', 'Away from home and locked out? On the server (over SSH), create the file {{file}}: the check is off within seconds, without a restart. Delete it to switch the check back on.', { file: status.consoleFile })}
    </Typography>
  </Stack>;
};

export const TurnstileCard = () => {
  const { t } = useTranslation();
  const status = useTurnstileStatus();
  const [busy, setBusy] = React.useState('');
  const [notice, setNotice] = React.useState(null);
  const [byHandOpen, setByHandOpen] = React.useState(false);
  const [helpOpen, setHelpOpen] = React.useState(false);

  if (!status) return null;

  const onAnswer = (res, success) => {
    if (res && res.ok) {
      setStatus(res.data);
      setNotice(success ? { severity: 'success', text: success } : null);
    } else {
      setNotice({ severity: 'error', text: say(t, reasonText(res && res.reason)) });
    }
  };
  const act = async (action) => {
    setBusy(action);
    const res = await API.config.turnstileChange({ action });
    setBusy('');
    onAnswer(res);
  };

  const state = stateText(status);
  const canCreate = !!status.panelHost && status.cloudflareToken;

  return <MainCard title={t('mgmt.turnstile.title', 'Sign-in check (Cloudflare Turnstile)')}>
    <Stack spacing={2} data-turnstile-card>
      <Typography variant="body2" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
        {t('mgmt.turnstile.intro', 'Cloudflare\'s Turnstile tells people from programs. Switched on, the sign-in, "forgot password" and invitation pages ask a visitor from the internet to pass it before the server reads the form: a program guessing passwords is turned away before any password is looked at. The visitor\'s browser then loads a script from Cloudflare.')}
      </Typography>

      {state && <Stack direction="row" spacing={1} alignItems="flex-start" data-turnstile-state={status.active ? 'on' : (status.confirmed ? 'ready' : (status.siteKey ? 'saved' : 'off'))}>
        <LevelIcon level={state[0]} />
        <Typography variant="body2" sx={{ overflowWrap: 'anywhere', minWidth: 0 }}>{say(t, state[1])}</Typography>
      </Stack>}

      {(status.problems || []).map((problem) => <Alert key={problem.reason} severity="error" sx={wrap} data-turnstile-problem={problem.reason}>{say(t, problemText(problemValues(problem)))}</Alert>)}
      {status.testMode && <Alert severity="info" sx={wrap}>{t('mgmt.turnstile.testMode', 'Test mode: this server accepts Cloudflare\'s test keys (started with COSMOS_FORK_TURNSTILE_TEST=1). Never on a real server.')}</Alert>}

      {!status.panelHost && <Alert severity="info" sx={wrap}>{say(t, reasonText('no_public_name'))}</Alert>}

      {status.siteKey && <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }} data-turnstile-keys>
        {status.source === 'api'
          ? t('mgmt.turnstile.keys.api', 'Widget made by Cosmos for {{hosts}}, site key {{key}}.', { hosts: (status.hostnames || []).join(', '), key: status.siteKey })
          : t('mgmt.turnstile.keys.manual', 'Keys pasted by hand, site key {{key}}.', { key: status.siteKey })}
      </Typography>}

      <PermissionGuard permission={PERM_CONFIGURATION}>
        <Stack spacing={2}>
          {notice && <Alert severity={notice.severity} sx={wrap} data-turnstile-notice={notice.severity}>{notice.text}</Alert>}

          {!!status.panelHost && !status.active && <Stack spacing={1.5}>
            <Typography variant="subtitle1">{status.siteKey ? t('mgmt.turnstile.widget.replace', 'Another widget') : t('mgmt.turnstile.widget.make', 'The widget at Cloudflare')}</Typography>
            {canCreate && <CreateWidget status={status} onAnswer={onAnswer} busy={busy} setBusy={setBusy} />}
            {!canCreate && <Typography variant="body2" color="textSecondary">{say(t, reasonText('no_cloudflare_token'))}</Typography>}
            <Door id="turnstile-by-hand" open={byHandOpen || !canCreate} onClick={() => setByHandOpen(!byHandOpen)}>
              {t('mgmt.turnstile.manual.door', 'Make it by hand and paste the keys')}
            </Door>
            <Collapse id="turnstile-by-hand" in={byHandOpen || !canCreate} unmountOnExit>
              <KeysByHand status={status} onAnswer={onAnswer} busy={busy} setBusy={setBusy} />
            </Collapse>
          </Stack>}

          {status.siteKey && !status.active && <>
            <Divider flexItem />
            <Stack spacing={1}>
              <Typography variant="subtitle1">{t('mgmt.turnstile.try.title', 'Try it, then switch it on')}</Typography>
              {!status.confirmed && <TryIt status={status} onAnswer={(res) => onAnswer(res, t('mgmt.turnstile.try.passed', 'The check passed with these keys: it can be switched on.'))} busy={busy} setBusy={setBusy} />}
              <Stack direction="row" spacing={1} useFlexGap flexWrap="wrap" alignItems="center">
                <LoadingButton variant="contained" size="small" loading={busy === 'enable'} disabled={!!busy || !status.confirmed} onClick={() => act('enable')} data-turnstile-action="enable">
                  {t('mgmt.turnstile.enable', 'Switch on')}
                </LoadingButton>
                <LoadingButton variant="outlined" size="small" color="error" loading={busy === 'remove'} disabled={!!busy} onClick={() => act('remove')} data-turnstile-action="remove">
                  {t('mgmt.turnstile.remove', 'Remove the keys')}
                </LoadingButton>
              </Stack>
              {!status.confirmed && <Typography variant="caption" color="textSecondary">{say(t, reasonText('not_tried'))}</Typography>}
            </Stack>
          </>}

          {status.active && <Stack direction="row" spacing={1} useFlexGap flexWrap="wrap">
            <LoadingButton variant="outlined" size="small" loading={busy === 'disable'} disabled={!!busy} onClick={() => act('disable')} data-turnstile-action="disable">
              {t('mgmt.turnstile.disable', 'Switch off')}
            </LoadingButton>
          </Stack>}
          {status.enabled && status.consoleOff && <Alert severity="warning" sx={wrap}>{say(t, state[1])}</Alert>}

          {status.siteKey && <><Divider flexItem /><Forms status={status} onAnswer={onAnswer} busy={busy} setBusy={setBusy} /></>}
        </Stack>
      </PermissionGuard>

      <Door id="turnstile-help" open={helpOpen} onClick={() => setHelpOpen(!helpOpen)}>{t('mgmt.turnstile.lockout.title', 'If something goes wrong')}</Door>
      <Collapse id="turnstile-help" in={helpOpen} unmountOnExit><LockOutHelp status={status} /></Collapse>
    </Stack>
  </MainCard>;
};

export default TurnstileCard;

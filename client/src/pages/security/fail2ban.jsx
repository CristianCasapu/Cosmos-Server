// Fork addition: the Fail2ban panel. Fail2ban is a program on the server
// itself that bans addresses at the firewall; the fork's installer
// (fork/fail2ban/install.sh) sets it up, Cosmos only shows it and changes a
// few settings (src/fail2ban on the server). This panel tells in plain words
// whether it is there and running, lists the jails with what each watches,
// the banned addresses with the time left and a button to lift a ban, and
// holds the settings: thresholds, ban times and the addresses that are never
// banned. It warns when the administrator's own address could be banned.
//
// Self-contained: mount it anywhere with <Fail2banPanel />. Its logic is in
// utils/fail2ban.js; it talks to /cosmos/api/fail2ban.
import * as React from 'react';
import dayjs from 'dayjs';
import {
  Alert,
  Button,
  Chip,
  CircularProgress,
  Divider,
  FormHelperText,
  Grid,
  InputLabel,
  MenuItem,
  OutlinedInput,
  Select,
  Stack,
  Tooltip,
  Typography,
} from '@mui/material';
import { LoadingButton } from '@mui/lab';
import { PlusOutlined, SaveOutlined, SyncOutlined, UndoOutlined, UnlockOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';

import * as API from '../../api';
import MainCard from '../../components/MainCard';
import PrettyTableView from '../../components/tableView/prettyTableView';
import { ConfirmModalDirect } from '../../components/confirmModal';
import PermissionGuard from '../../components/permissionGuard';
import { PERM_ADMIN } from '../../utils/permissions';
import { JAILS, durationWords, entryProblem, formOf, neverBanned, settingsOf, stateOf, timeLeft } from '../../utils/fail2ban';

const POLL_EVERY = 10000;
const INSTALL_COMMAND = 'sh fork/fail2ban/install.sh';

// A command to type on the server: shown as text, in the theme's own font
// for code, wrapping on narrow screens.
const Command = ({ children }) => (
  <Typography component="code" variant="body2" style={{ display: 'block', margin: '6px 0', fontFamily: 'monospace', overflowWrap: 'anywhere' }}>
    {children}
  </Typography>
);

const Fail2banPanel = () => {
  const { t } = useTranslation();
  const tr = (entry) => t(entry[0], entry[1], entry[2] || {});

  const [status, setStatus] = React.useState(null);
  const [loadError, setLoadError] = React.useState('');
  const [form, setForm] = React.useState(null);
  const [dirty, setDirty] = React.useState(false);
  const [problems, setProblems] = React.useState({});
  const [saving, setSaving] = React.useState(false);
  const [saveError, setSaveError] = React.useState('');
  const [saved, setSaved] = React.useState(false);
  const [newEntry, setNewEntry] = React.useState('');
  const [entryError, setEntryError] = React.useState('');
  const [confirmUnban, setConfirmUnban] = React.useState(null);
  const [actionError, setActionError] = React.useState('');
  const [now, setNow] = React.useState(Date.now());
  // the server's clock minus this browser's, so that "time left" does not
  // depend on the clock of the device the panel is read on
  const clockOffset = React.useRef(0);
  const dirtyRef = React.useRef(false);
  dirtyRef.current = dirty;

  const take = (data) => {
    setStatus(data);
    setLoadError('');
    if (data && data.time) {
      clockOffset.current = new Date(data.time).getTime() - Date.now();
    }
    // the form follows the server until the administrator starts editing
    if (data && !dirtyRef.current) {
      setForm(formOf(data.settings));
    }
  };

  const refresh = () => {
    if (!API.config.fail2banStatus) {
      setLoadError(t('fail2ban.unavailable', 'Fail2ban cannot be shown here.'));
      return Promise.resolve();
    }
    return API.config.fail2banStatus()
      .then((res) => take(res.data))
      .catch((err) => setLoadError((err && err.message) || t('fail2ban.loadFailed', 'The state of Fail2ban could not be read.')));
  };

  React.useEffect(() => {
    refresh();
    const poll = setInterval(refresh, POLL_EVERY);
    const tick = setInterval(() => setNow(Date.now()), 30000);
    return () => {
      clearInterval(poll);
      clearInterval(tick);
    };
  }, []);

  const state = stateOf(status);
  const serverNow = now + clockOffset.current;
  const jails = (status && status.jails) || [];
  const bans = (status && status.bans) || [];
  const loadedJails = jails.filter((jail) => jail.loaded).length;
  // an address banned by two jails is one banned address
  const bannedAddresses = new Set(bans.map((ban) => ban.ip)).size;
  const canSave = Boolean(status && status.installed && status.configInstalled);

  const jailLabel = (name) => (JAILS[name] ? tr(JAILS[name].label) : name);

  const changeJail = (name, field, value) => {
    setForm({ ...form, jails: { ...form.jails, [name]: { ...form.jails[name], [field]: value } } });
    setDirty(true);
    setSaved(false);
  };

  const send = (settings) => {
    setSaving(true);
    setSaveError('');
    setSaved(false);
    return API.config.fail2banSettings(settings)
      .then((res) => {
        dirtyRef.current = false;
        setDirty(false);
        setProblems({});
        setSaved(true);
        take(res.data);
      })
      .catch((err) => setSaveError((err && err.message) || t('fail2ban.saveFailed', 'The settings could not be saved.')))
      .finally(() => setSaving(false));
  };

  const save = () => {
    const result = settingsOf(form, status.bounds);
    if (result.problems) {
      setProblems(result.problems);
      return;
    }
    setProblems({});
    send(result.settings);
  };

  const reset = () => {
    setForm(formOf(status.settings));
    setDirty(false);
    setProblems({});
    setSaveError('');
    setEntryError('');
    setNewEntry('');
  };

  const addEntry = () => {
    const text = newEntry.trim();
    const problem = entryProblem(text);
    if (problem) {
      setEntryError(tr(problem));
      return;
    }
    if (!form.ignoreIP.includes(text)) {
      setForm({ ...form, ignoreIP: [...form.ignoreIP, text] });
      setDirty(true);
      setSaved(false);
    }
    setNewEntry('');
    setEntryError('');
  };

  const removeEntry = (entry) => {
    setForm({ ...form, ignoreIP: form.ignoreIP.filter((other) => other !== entry) });
    setDirty(true);
    setSaved(false);
  };

  // one click: the address this browser comes from is added to the list as
  // it runs now, and saved
  const protectMe = () => {
    const result = settingsOf(formOf(status.settings), status.bounds);
    if (result.problems) {
      setSaveError(t('fail2ban.saveFailed', 'The settings could not be saved.'));
      return;
    }
    send({ ...result.settings, ignoreIP: [...result.settings.ignoreIP, status.clientIP] });
  };

  const unban = (ban) => {
    setActionError('');
    API.config.fail2banUnban(ban.ip, ban.jail)
      .then((res) => take(res.data))
      .catch((err) => setActionError((err && err.message) || t('fail2ban.unbanFailed', 'The ban could not be lifted.')));
  };

  const thresholdText = (jail) => t('fail2ban.threshold', '{{tries}} in {{within}}: banned for {{ban}}', {
    tries: jail.maxRetry === 1 ? t('fail2ban.try', '1 try') : t('fail2ban.tries', '{{n}} tries', { n: jail.maxRetry }),
    within: tr(durationWords(jail.findTime)),
    ban: tr(durationWords(jail.banTime)),
  });

  const unitOptions = [
    ['minutes', t('fail2ban.unit.minutes', 'minutes')],
    ['hours', t('fail2ban.unit.hours', 'hours')],
    ['days', t('fail2ban.unit.days', 'days')],
  ];

  // a number with, for the two times, its unit next to it
  const field = (name, key, label, valueField, unitField) => {
    const id = 'fail2ban-' + name + '-' + key;
    const problem = problems[name + '.' + key];
    return <Grid item xs={12} sm={4}>
      <Stack spacing={1}>
        <InputLabel htmlFor={id}>{label}</InputLabel>
        <Stack direction="row" spacing={1}>
          <OutlinedInput
            id={id}
            type="number"
            value={form.jails[name][valueField]}
            onChange={(event) => changeJail(name, valueField, event.target.value)}
            inputProps={{ min: 1, step: 1, inputMode: 'numeric' }}
            error={Boolean(problem)}
            disabled={!canSave}
            style={{ flex: '1 1 0', minWidth: 0 }}
          />
          {unitField && <Select
            value={form.jails[name][unitField]}
            onChange={(event) => changeJail(name, unitField, event.target.value)}
            disabled={!canSave}
            inputProps={{ 'aria-label': t('fail2ban.unit', 'Unit of {{label}}', { label }) }}
            style={{ flex: '1 1 0', minWidth: 0 }}
          >
            {unitOptions.map((option) => <MenuItem key={option[0]} value={option[0]}>{option[1]}</MenuItem>)}
          </Select>}
        </Stack>
        {problem && <FormHelperText error>{tr(problem)}</FormHelperText>}
      </Stack>
    </Grid>;
  };

  const formCovers = Boolean(form && status && status.clientIP) && neverBanned(form.ignoreIP, status.clientIP);

  return <div style={{ maxWidth: '1200px', margin: 'auto' }}>
    {confirmUnban && <ConfirmModalDirect
      callback={() => unban(confirmUnban)}
      content={t('fail2ban.unbanConfirm', 'Lift the ban of {{ip}} in "{{jail}}"? It can reach the server again at once.', { ip: confirmUnban.ip, jail: jailLabel(confirmUnban.jail) })}
      onClose={() => setConfirmUnban(null)}
    />}

    <Stack spacing={2}>
      <div>
        <Typography variant="h5">{t('fail2ban.title', 'Fail2ban')}</Typography>
        <Typography variant="body2" color="text.secondary">
          {t('fail2ban.intro', 'Fail2ban runs on the server itself. It reads the security log of Cosmos and the server\'s SSH log, and bans an address at the firewall after repeated failed logins, scans or abuse: a banned address reaches nothing on the server, neither Cosmos nor the apps nor SSH, until the ban ends.')}
        </Typography>
      </div>

      {state === 'loading' && !loadError && <Stack direction="row" spacing={1} alignItems="center">
        <CircularProgress size={18} />
        <Typography variant="body2">{t('fail2ban.loading', 'Asking the server about Fail2ban...')}</Typography>
      </Stack>}
      {loadError && <Alert severity="error">{loadError}</Alert>}

      {state === 'notInstalled' && <Alert severity="info">
        <strong>{t('fail2ban.state.notInstalled', 'Fail2ban is not installed on this server.')}</strong>
        <div>{t('fail2ban.state.notInstalled.how', 'Nothing is banned at the firewall. To install it, log in to the server as root and run this from a copy of the fork\'s repository. It shows what it will change and asks before changing anything:')}</div>
        <Command>{INSTALL_COMMAND}</Command>
        <div>{t('fail2ban.state.notInstalled.why', 'Cosmos does not install it for you: adding a program that decides who may reach the server is done on the server, by you.')}</div>
      </Alert>}

      {state === 'stopped' && <Alert severity="warning">
        <strong>{t('fail2ban.state.stopped', 'Fail2ban is installed but not running.')}</strong>
        <div>{t('fail2ban.state.stopped.how', 'Nothing is banned at the firewall. Start it on the server:')}</div>
        <Command>systemctl start fail2ban</Command>
        {!status.configInstalled && <div>{t('fail2ban.state.noFiles', 'The jails of this fork are not installed either. On the server, as root, from a copy of the fork\'s repository:')}<Command>{INSTALL_COMMAND}</Command></div>}
      </Alert>}

      {state === 'noJails' && <Alert severity="warning">
        <strong>{t('fail2ban.state.noJails', 'Fail2ban is running, but without the jails of this fork.')}</strong>
        <div>{status.configInstalled
          ? t('fail2ban.state.noJails.reload', 'Their files are on the server but Fail2ban has not loaded them. On the server, as root:')
          : t('fail2ban.state.noJails.how', 'Nothing that happens in Cosmos leads to a ban. To install them, on the server, as root, from a copy of the fork\'s repository:')}</div>
        <Command>{status.configInstalled ? 'systemctl restart fail2ban' : INSTALL_COMMAND}</Command>
        {status.otherJails > 0 && <div>{t('fail2ban.state.otherJails', 'Jails that are not the fork\'s: {{n}}. They are not shown here.', { n: status.otherJails })}</div>}
      </Alert>}

      {state === 'active' && <Alert severity="success">
        <strong>{t('fail2ban.state.active', 'Fail2ban is active.')}</strong>{' '}
        {t('fail2ban.state.active.jails', '{{loaded}} of {{all}} jails are running.', { loaded: loadedJails, all: jails.length })}{' '}
        {bannedAddresses === 1
          ? t('fail2ban.state.active.banned.one', '1 address is banned now.')
          : t('fail2ban.state.active.banned', '{{banned}} addresses are banned now.', { banned: bannedAddresses })}
        {status.otherJails > 0 && <> {t('fail2ban.state.otherJails', 'Jails that are not the fork\'s: {{n}}. They are not shown here.', { n: status.otherJails })}</>}
      </Alert>}

      {status && status.incomplete && <Alert severity="warning">
        {t('fail2ban.incomplete', 'Fail2ban did not answer every question in time: jails or bans may be missing below. This page asks again every few seconds.')}
      </Alert>}

      {state === 'active' && status.trustedProxies > 0 && <Alert severity="warning">
        <strong>{t('fail2ban.warn.proxy', 'Cosmos is set up behind a reverse proxy.')}</strong>{' '}
        {t('fail2ban.warn.proxy.text', 'Visitors who come through the proxy cannot be blocked by this server\'s firewall: their packets arrive from the proxy. And a proxy that passes on the X-Forwarded-For header a visitor sent lets that visitor choose which address gets banned. Ban at the proxy instead, or remove the trusted proxies (Configuration, HTTP) if Cosmos is reached directly.')}
      </Alert>}

      {state === 'active' && status.clientIP && !status.clientIgnored && <Alert severity="warning" action={
        canSave && <PermissionGuard permission={PERM_ADMIN}>
          <LoadingButton color="inherit" size="small" loading={saving} onClick={protectMe}>{t('fail2ban.warn.self.add', 'Never ban my address')}</LoadingButton>
        </PermissionGuard>
      }>
        <strong>{t('fail2ban.warn.self', 'Your own address, {{ip}}, can be banned.', { ip: status.clientIP })}</strong>{' '}
        {t('fail2ban.warn.self.text', 'It is not among the addresses that are never banned. A few mistyped passwords from it would cut it off from the whole server, this panel and SSH included, until the ban ends.')}
      </Alert>}

      {state === 'active' && !status.privateIgnored && <Alert severity="warning">
        <strong>{t('fail2ban.warn.private', 'The private networks are not all on the never-ban list.')}</strong>{' '}
        {t('fail2ban.warn.private.text', 'Devices at home and the containers on this server use private addresses (10.x, 172.16 to 172.31, 192.168.x). Without them on the list a device or an app that misbehaves towards Cosmos is banned like a stranger, and a banned container loses its network.')}
      </Alert>}

      {actionError && <Alert severity="error" onClose={() => setActionError('')}>{actionError}</Alert>}

      {state === 'active' && <div>
        <Typography variant="h5" style={{ marginBottom: '10px' }}>{t('fail2ban.bans.title', 'Banned addresses')}</Typography>
        {bans.length === 0 && <Stack direction="row" spacing={2} alignItems="center" flexWrap="wrap" useFlexGap>
          <Typography variant="body2" color="text.secondary">{t('fail2ban.bans.none', 'No address is banned at the moment.')}</Typography>
          <Button variant="outlined" startIcon={<SyncOutlined />} onClick={refresh}>{t('global.refresh')}</Button>
        </Stack>}
        {bans.length > 0 && <PrettyTableView
          isLoading={false}
          data={bans}
          getKey={(ban) => ban.jail + ' ' + ban.ip}
          buttons={[
            <Button key="refresh" variant="outlined" startIcon={<SyncOutlined />} onClick={refresh}>{t('global.refresh')}</Button>,
          ]}
          columns={[
            {
              title: t('fail2ban.bans.address', 'Address'),
              search: (ban) => ban.ip + ' ' + jailLabel(ban.jail),
              field: (ban) => <strong style={{ overflowWrap: 'anywhere' }}>{ban.ip}</strong>,
            },
            {
              title: t('fail2ban.bans.jail', 'Banned by'),
              field: (ban) => <Tooltip title={JAILS[ban.jail] ? tr(JAILS[ban.jail].watches) : ''}><Chip size="small" variant="outlined" label={jailLabel(ban.jail)} /></Tooltip>,
            },
            {
              title: t('fail2ban.bans.since', 'Since'),
              screenMin: 'md',
              field: (ban) => (ban.since ? dayjs(ban.since).format('L LT') : ''),
            },
            {
              title: t('fail2ban.bans.left', 'Time left'),
              field: (ban) => <Tooltip title={ban.until ? t('fail2ban.bans.until', 'Ends {{date}}', { date: dayjs(ban.until).format('L LT') }) : ''}>
                <span>{tr(timeLeft(ban.until ? new Date(ban.until).getTime() : null, serverNow))}</span>
              </Tooltip>,
            },
            {
              title: '',
              field: (ban) => <PermissionGuard permission={PERM_ADMIN}>
                <Tooltip title={t('fail2ban.bans.unban.tip', 'Lift this ban now: the address can reach the server again at once.')}>
                  <Button size="small" variant="outlined" color="error" startIcon={<UnlockOutlined />} onClick={() => setConfirmUnban(ban)}>
                    {t('fail2ban.bans.unban', 'Unban')}
                  </Button>
                </Tooltip>
              </PermissionGuard>,
              style: { textAlign: 'right' },
            },
          ]}
        />}
      </div>}

      {status && status.installed && form && <MainCard title={t('fail2ban.jails.title', 'Jails and their thresholds')}>
        <Stack spacing={2}>
          <Typography variant="body2" color="text.secondary">
            {t('fail2ban.jails.intro', 'A jail watches one kind of misbehaviour. An address that reaches the number of tries within the time they are counted over is banned for the length of the ban.')}
          </Typography>
          {!status.configInstalled && <Alert severity="info">
            {t('fail2ban.jails.notInstalled', 'The jails of this fork are not installed on the server yet, so there is nothing to set here. What is shown is what the installer would set up.')}
          </Alert>}

          {jails.map((jail, position) => <React.Fragment key={jail.name}>
            {position > 0 && <Divider />}
            {/* the wrapper keeps the Stack from resetting the margins a Grid container needs */}
            <div><Grid container spacing={2}>
              <Grid item xs={12} md={5}>
                <Stack spacing={0.5}>
                  <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap" useFlexGap>
                    <Typography variant="subtitle1"><strong>{jailLabel(jail.name)}</strong></Typography>
                    {status.running && <Chip size="small" color={jail.loaded ? 'success' : 'default'} label={jail.loaded ? t('fail2ban.jail.running', 'running') : t('fail2ban.jail.off', 'off')} />}
                  </Stack>
                  <Typography variant="body2" color="text.secondary">{JAILS[jail.name] ? tr(JAILS[jail.name].watches) : ''}</Typography>
                  <Typography variant="body2">{thresholdText(jail)}</Typography>
                  {jail.loaded && <Typography variant="caption" color="text.secondary">
                    {t('fail2ban.jail.counts', 'Banned now: {{now}}. Banned since Fail2ban started: {{total}}. Tries being counted: {{failed}}.', { now: jail.currentlyBanned, total: jail.totalBanned, failed: jail.currentlyFailed })}
                  </Typography>}
                </Stack>
              </Grid>
              <Grid item xs={12} md={7}>
                <Grid container spacing={2}>
                  {field(jail.name, 'maxRetry', t('fail2ban.field.tries', 'Tries'), 'maxRetry')}
                  {field(jail.name, 'findTime', t('fail2ban.field.findTime', 'Counted over'), 'findValue', 'findUnit')}
                  {field(jail.name, 'banTime', t('fail2ban.field.banTime', 'Banned for'), 'banValue', 'banUnit')}
                </Grid>
              </Grid>
            </Grid></div>
          </React.Fragment>)}

          <Divider />
          <Stack spacing={1}>
            <Typography variant="subtitle1"><strong>{t('fail2ban.ignore.title', 'Addresses that are never banned')}</strong></Typography>
            <Typography variant="body2" color="text.secondary">
              {t('fail2ban.ignore.intro', 'Whatever comes from these addresses and networks is never banned by the jails above. The server itself is always on the list. Keep your home network here, and add the address of any other place you manage the server from.')}
            </Typography>
            <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
              {form.ignoreIP.map((entry) => <Chip
                key={entry}
                label={entry}
                variant="outlined"
                color={status.clientIP && neverBanned([entry], status.clientIP) ? 'success' : 'default'}
                onDelete={canSave ? () => removeEntry(entry) : undefined}
              />)}
              {form.ignoreIP.length === 0 && <Typography variant="body2" color="text.secondary">{t('fail2ban.ignore.none', 'Only the server itself.')}</Typography>}
            </Stack>
            {status.otherIgnored > 0 && <FormHelperText>
              {t('fail2ban.ignore.others', 'Fail2ban\'s list also holds {{n}} entries that are not addresses (host names). They are not shown, and saving here removes them.', { n: status.otherIgnored })}
            </FormHelperText>}
            <InputLabel htmlFor="fail2ban-new-entry">{t('fail2ban.ignore.add.label', 'Address or network')}</InputLabel>
            <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} alignItems="stretch">
              <OutlinedInput
                id="fail2ban-new-entry"
                value={newEntry}
                placeholder="203.0.113.7, 192.168.0.0/16"
                disabled={!canSave}
                error={Boolean(entryError)}
                onChange={(event) => { setNewEntry(event.target.value); setEntryError(''); }}
                onKeyDown={(event) => { if (event.key === 'Enter') { event.preventDefault(); addEntry(); } }}
                inputProps={{ maxLength: 64, autoComplete: 'off', spellCheck: false }}
                sx={{ flex: '1 1 auto', maxWidth: { sm: '420px' } }}
              />
              <Button variant="outlined" startIcon={<PlusOutlined />} disabled={!canSave || newEntry.trim() === ''} onClick={addEntry}>
                {t('fail2ban.ignore.add', 'Add to the list')}
              </Button>
            </Stack>
            {entryError && <FormHelperText error>{entryError}</FormHelperText>}
            {problems.ignoreIP && <FormHelperText error>{tr(problems.ignoreIP)}</FormHelperText>}
            {canSave && dirty && status.clientIP && !formCovers && <Alert severity="warning">
              {t('fail2ban.ignore.selfMissing', 'Your own address, {{ip}}, is not on this list: once saved, it can be banned.', { ip: status.clientIP })}
            </Alert>}
          </Stack>

          {saveError && <Alert severity="error">{saveError}</Alert>}
          {saved && !dirty && <Alert severity="success">{t('fail2ban.saved', 'Saved. Fail2ban is using the new settings; addresses that are banned stay banned until their ban ends.')}</Alert>}
          {canSave && <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
            <PermissionGuard permission={PERM_ADMIN} alwaysShow>
              <LoadingButton variant="contained" startIcon={<SaveOutlined />} loading={saving} disabled={!dirty} onClick={save}>
                {t('global.saveAction')}
              </LoadingButton>
            </PermissionGuard>
            <Button variant="outlined" startIcon={<UndoOutlined />} disabled={!dirty || saving} onClick={reset}>
              {t('fail2ban.reset', 'Undo my changes')}
            </Button>
          </Stack>}
        </Stack>
      </MainCard>}
    </Stack>
  </div>;
};

export { Fail2banPanel };
export default Fail2banPanel;

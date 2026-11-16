// Fork addition (plan 45): "Sign-in details" on an app's page. What the
// container's own settings say about how one signs in to it (the server's
// GET /api/servapps/{id}/sign-in, no secret in it), each password hidden with
// Show and Copy (POST …/sign-in/reveal, one value at a time, recorded in the
// security record), for a database its address and connection string, and
// "Add a web manager". Only for accounts whose role may read credentials; the
// server asks for the password confirmation, the page offers it. A shown value
// is held in this component only, and hidden again after a minute.
import * as React from 'react';
import { Alert, Button, Chip, IconButton, Stack, Tooltip, Typography } from '@mui/material';
import { CopyOutlined, EyeInvisibleOutlined, EyeOutlined, KeyOutlined, LinkOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import * as API from '../api';
import { useClientInfos } from '../utils/hooks';
import { askForPassword, getSudoState, subscribeSudo } from '../utils/sudoPrompt';
import { PERM_CREDENTIALS_READ, PERM_RESOURCES } from '../utils/permissions';
import PermissionGuard from './permissionGuard';
import { AddManagerDialog } from './managerChoice';
import { MANAGER_LABEL } from '../utils/signIn';

const SHOWN_FOR = 60000;
const mono = { fontFamily: 'monospace', overflowWrap: 'anywhere', wordBreak: 'break-all', minWidth: 0 };
const wrap = { overflowWrap: 'anywhere', minWidth: 0, '& .MuiAlert-message': { minWidth: 0 } };
const touch = { '&&&': { minHeight: { xs: 44, sm: 0 } }, flexShrink: 0 };

// Copy a value that is still being fetched: Safari only lets the page write
// to the clipboard within the click, which a ClipboardItem holding a promise
// keeps; elsewhere writeText after the answer.
const copyFrom = (promise) => {
  try {
    if (typeof window !== 'undefined' && window.ClipboardItem && navigator.clipboard && navigator.clipboard.write) {
      const blob = promise.then((text) => new Blob([text], { type: 'text/plain' }));
      return navigator.clipboard.write([new window.ClipboardItem({ 'text/plain': blob })]).catch(() => promise.then((text) => navigator.clipboard.writeText(text)));
    }
  } catch (e) { /* fall through */ }
  return promise.then((text) => navigator.clipboard.writeText(text));
};

const Row = ({ label, children }) => <Stack direction={{ xs: 'column', sm: 'row' }} spacing={{ xs: 0.25, sm: 1 }} sx={{ alignItems: { sm: 'center' }, minWidth: 0 }}>
  <Typography variant="body2" sx={{ color: 'text.secondary', minWidth: { sm: 140 }, flexShrink: 0 }}>{label}</Typography>
  <Stack direction="row" spacing={1} useFlexGap sx={{ alignItems: 'center', flexWrap: 'wrap', minWidth: 0 }}>{children}</Stack>
</Stack>;

const PlainValue = ({ value, copyLabel }) => {
  const { t } = useTranslation();
  const [copied, setCopied] = React.useState(false);
  return <>
    <Typography variant="body2" sx={mono} className="ct-readonly">{value}</Typography>
    <Button size="small" variant="outlined" startIcon={<CopyOutlined />} sx={touch} aria-label={copyLabel}
      onClick={() => { try { navigator.clipboard.writeText(value).then(() => setCopied(true), () => {}); } catch (e) { /* on screen */ } }}>
      {copied ? t('mgmt.servapps.signIn.card.copied', 'Copied') : t('mgmt.servapps.signIn.card.copy', 'Copy')}
    </Button>
  </>;
};

// A secret: hidden, Show (for a minute) and Copy; each asks the server.
const SecretValue = ({ container, secret, placeholder, copyLabel, showLabel }) => {
  const { t } = useTranslation();
  const [value, setValue] = React.useState('');
  const [state, setState] = React.useState('');
  const timer = React.useRef(null);
  React.useEffect(() => () => { if (timer.current) clearTimeout(timer.current); }, []);
  const ask = (purpose) => API.signIn.reveal(container, secret, purpose).then((res) => String((res && res.data && res.data.value) || ''));
  const hide = () => { setValue(''); if (timer.current) clearTimeout(timer.current); timer.current = null; };
  // the password prompt opens by itself for a POST refused for want of the confirmation (utils/sudoPrompt.js)
  const fail = (e) => setState(e && e.code === 'SI429' ? 'busy' : e && e.code === 'HTTP008' ? '' : 'failed');
  return <>
    <Typography variant="body2" sx={mono} className="ct-readonly" data-signin-secret={secret}>{value || placeholder || '••••••••••••'}</Typography>
    {value
      ? <Button size="small" variant="outlined" startIcon={<EyeInvisibleOutlined />} sx={touch} onClick={hide}>{t('mgmt.servapps.signIn.card.hide', 'Hide')}</Button>
      : <Button size="small" variant="outlined" startIcon={<EyeOutlined />} sx={touch} aria-label={showLabel} data-signin-show={secret}
        onClick={() => {
          setState('');
          ask('show').then((v) => {
            setValue(v);
            if (timer.current) clearTimeout(timer.current);
            timer.current = setTimeout(hide, SHOWN_FOR);
          }).catch(fail);
        }}>{t('mgmt.servapps.signIn.card.show', 'Show')}</Button>}
    <Button size="small" variant="outlined" startIcon={<CopyOutlined />} sx={touch} aria-label={copyLabel} data-signin-copy={secret}
      onClick={() => { setState(''); copyFrom(ask('copy')).then(() => setState('copied')).catch(fail); }}>
      {state === 'copied' ? t('mgmt.servapps.signIn.card.copied', 'Copied') : t('mgmt.servapps.signIn.card.copy', 'Copy')}
    </Button>
    {state === 'busy' && <Typography variant="caption" color="error">{t('mgmt.servapps.signIn.card.busy', 'Asked too often: wait a minute.')}</Typography>}
    {state === 'failed' && <Typography variant="caption" color="error">{t('mgmt.servapps.signIn.card.failed', 'Cosmos could not read it just now.')}</Typography>}
  </>;
};

const WHERE = {
  database: ['mgmt.servapps.signIn.card.where.database', 'With a database client, or a web manager (below).'],
  web: ['mgmt.servapps.signIn.card.where.web', 'In its web page.'],
  s3: ['mgmt.servapps.signIn.card.where.s3', 'With an S3 client, and in its console.'],
  amqp: ['mgmt.servapps.signIn.card.where.amqp', 'From apps, and in its management page.'],
  http: ['mgmt.servapps.signIn.card.where.http', 'Through its API.'],
};

const Account = ({ container, account, index }) => {
  const { t } = useTranslation();
  const role = account.role === 'root' || account.role === 'admin'
    ? t('mgmt.servapps.signIn.card.role.' + account.role, account.role === 'root' ? 'Its root account' : 'Its administrator account') : '';
  return <Stack spacing={0.75} sx={{ minWidth: 0 }} data-signin-account={index}>
    {role && <Typography variant="subtitle2">{role}</Typography>}
    {account.user && <Row label={t('mgmt.servapps.signIn.card.user', 'Username')}><PlainValue value={account.user} copyLabel={t('mgmt.servapps.signIn.card.copyUser', 'Copy the username')} /></Row>}
    {account.email && <Row label={t('mgmt.servapps.signIn.card.email', 'E-mail')}><PlainValue value={account.email} copyLabel={t('mgmt.servapps.signIn.card.copyEmail', 'Copy the e-mail')} /></Row>}
    {account.database && <Row label={t('mgmt.servapps.signIn.card.database', 'Database')}><PlainValue value={account.database} copyLabel={t('mgmt.servapps.signIn.card.copyDatabase', 'Copy the database name')} /></Row>}
    {account.password && <Row label={t('mgmt.servapps.signIn.card.password', 'Password')}>
      <SecretValue container={container} secret={account.password} copyLabel={t('mgmt.servapps.signIn.card.copyPassword', 'Copy the password')} showLabel={t('mgmt.servapps.signIn.card.showPassword', 'Show the password')} />
    </Row>}
    {!account.password && account.passwordVar && <Row label={t('mgmt.servapps.signIn.card.password', 'Password')}>
      <Typography variant="body2" sx={{ color: 'text.secondary' }}>{t('mgmt.servapps.signIn.card.noPassword', 'None is set in its settings ({{name}}).', { name: account.passwordVar })}</Typography>
    </Row>}
    {account.connection && <Row label={t('mgmt.servapps.signIn.card.connection', 'Connection string')}>
      <SecretValue container={container} secret={account.connectionSecret} placeholder={account.connection}
        copyLabel={t('mgmt.servapps.signIn.card.copyConnection', 'Copy the connection string with its password')} showLabel={t('mgmt.servapps.signIn.card.showConnection', 'Show the connection string with its password')} />
    </Row>}
  </Stack>;
};

const managerOf = (containers, name) => (containers || []).filter((c) => c && c.Labels && c.Labels[MANAGER_LABEL] === name)
  .map((c) => String((c.Names && c.Names[0]) || '').replace(/^\//, '')).filter(Boolean);

export const SignInDetails = ({ containerInfo, refresh }) => {
  const { t } = useTranslation();
  const { hasPermission, hasRolePermission } = useClientInfos();
  const name = String((containerInfo && containerInfo.Name) || '').replace(/^\//, '');
  const allowed = hasPermission(PERM_CREDENTIALS_READ);
  const [details, setDetails] = React.useState(null);
  const [problem, setProblem] = React.useState('');
  const [managers, setManagers] = React.useState([]);
  const [adding, setAdding] = React.useState(false);
  const anchor = React.useRef(null);
  const sudoTick = React.useSyncExternalStore(subscribeSudo, () => getSudoState().tick); // read again after a password confirmation

  React.useEffect(() => {
    if (!allowed || !name || !API.signIn) return undefined;
    let live = true;
    setProblem('');
    API.signIn.details(name).then((res) => { if (live) setDetails((res && res.data) || null); })
      .catch((e) => { if (live) setProblem(e && e.code === 'HTTP008' ? 'confirm' : 'failed'); });
    API.docker.list().then((res) => { if (live) setManagers(managerOf(res && res.data, name)); }).catch(() => {});
    return () => { live = false; };
  }, [allowed, name, containerInfo && containerInfo.Id, sudoTick]);

  React.useEffect(() => {
    if (typeof window !== 'undefined' && window.location.hash === '#sign-in' && anchor.current && details) {
      try { anchor.current.scrollIntoView({ block: 'start' }); } catch (e) { /* old browser */ }
    }
  }, [details]);

  if (!hasRolePermission(PERM_CREDENTIALS_READ)) return null;
  const nothing = details && !details.accounts.length && !details.firstRun && !details.via && details.where !== 'database';
  const sourceText = details && {
    recipe: t('mgmt.servapps.signIn.card.source.recipe', 'As Cosmos knows {{name}}.', { name: details.name || details.image }),
    label: t('mgmt.servapps.signIn.card.source.label', 'As its installation recorded it.'),
    guess: t('mgmt.servapps.signIn.card.source.guess', 'Guessed from the names of its settings: check them in the app\'s documentation.'),
    firstRun: '',
    none: '',
  }[details.source];

  return <div id="sign-in" ref={anchor} data-signin-card style={{ minWidth: 0 }}>
    <strong><KeyOutlined /> {t('mgmt.servapps.signIn.card.title', 'Sign-in details')}</strong>
    <Stack spacing={1.25} sx={{ mt: 1, minWidth: 0 }}>
      {(!allowed || problem === 'confirm') && <Stack spacing={1} sx={{ alignItems: 'flex-start' }}>
        <Typography variant="body2">{t('mgmt.servapps.signIn.card.confirm', 'The username, password and address of this app are shown after you confirm your password.')}</Typography>
        <Button size="small" variant="contained" sx={touch} onClick={() => askForPassword('guard')} data-signin-confirm>{t('mgmt.servapps.signIn.card.confirmButton', 'Confirm my password')}</Button>
      </Stack>}
      {allowed && problem === 'failed' && <Typography variant="body2" color="error">{t('mgmt.servapps.signIn.card.readFailed', 'Cosmos could not read this app\'s settings just now.')}</Typography>}
      {allowed && !details && !problem && <Typography variant="body2" sx={{ color: 'text.secondary' }}>{t('mgmt.servapps.signIn.card.reading', 'Reading its settings…')}</Typography>}
      {allowed && details && <>
        {nothing && <Typography variant="body2" sx={{ color: 'text.secondary' }}>{t('mgmt.servapps.signIn.card.nothing', 'Cosmos does not know how one signs in to this app: its settings name no username or password it recognises. Its documentation says how.')}</Typography>}
        {details.addresses.length > 0 && <Row label={t('mgmt.servapps.signIn.card.open', 'Open it at')}>
          {details.addresses.map((a) => <Chip key={a.url} component="a" href={a.url} target="_blank" rel="noopener noreferrer" clickable icon={<LinkOutlined />}
            label={a.url + (a.adminOnly ? ' · ' + t('mgmt.servapps.signIn.card.adminsOnly', 'administrators only') : '')} sx={{ maxWidth: '100%', '& .MuiChip-label': { overflow: 'hidden', textOverflow: 'ellipsis' } }} />)}
        </Row>}
        {details.where && WHERE[details.where] && !nothing && <Typography variant="body2">{t(WHERE[details.where][0], WHERE[details.where][1])}</Typography>}
        {details.host && details.port > 0 && <Row label={t('mgmt.servapps.signIn.card.reach', 'Other apps reach it at')}>
          <PlainValue value={`${details.host}:${details.port}`} copyLabel={t('mgmt.servapps.signIn.card.copyAddress', 'Copy the address')} />
        </Row>}
        {details.via === 'database' && <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }} data-signin-via>
          {details.viaContainer
            ? <>{t('mgmt.servapps.signIn.card.via', 'You sign in to it with the user and password of the database {{name}}:', { name: details.viaContainer })} <Link to={`/cosmos-ui/servapps/containers/${encodeURIComponent(details.viaContainer)}#sign-in`}>{t('mgmt.servapps.signIn.card.viaLink', 'its sign-in details')}</Link>.</>
            : t('mgmt.servapps.signIn.card.viaAny', 'You sign in to it with the user and password of the database it manages.')}
        </Typography>}
        {details.firstRun && <Alert severity="info" sx={wrap} data-signin-firstrun>
          {details.firstRun.note === 'logPassword'
            ? t('mgmt.servapps.signIn.card.logPassword', 'It prints a temporary password in its log the first time it starts (Logs tab), for the username {{user}}. Cosmos did not set it and does not keep it.', { user: details.firstRun.user || 'admin' })
            : details.firstRun.user
              ? t('mgmt.servapps.signIn.card.firstRunUser', 'It makes its first account in its own page, the first time it is opened: Cosmos did not set it. The username proposed at its installation: {{user}}. The password proposed then is not kept by Cosmos.', { user: details.firstRun.user })
              : t('mgmt.servapps.signIn.card.firstRun', 'It makes its first account in its own page, the first time it is opened: Cosmos did not set it and does not know it.')}
        </Alert>}
        {details.accounts.map((account, i) => <Account key={i} index={i} container={name} account={account} />)}
        {sourceText && !nothing && <Typography variant="caption" sx={{ color: 'text.secondary' }}>{sourceText}</Typography>}
        {details.accounts.some((a) => a.password) && <Typography variant="caption" sx={{ color: 'text.secondary', overflowWrap: 'anywhere' }}>
          {t('mgmt.servapps.signIn.card.note', 'Shown as the app was installed: a password changed inside the app since then is not known to Cosmos. The values live in the container\'s settings (and so in Cosmos\' backup of container definitions, readable by the server\'s administrator only). Each Show and Copy is written to the security record, without the value.')}
        </Typography>}
        {details.where === 'database' && <Stack direction="row" spacing={1} useFlexGap sx={{ flexWrap: 'wrap', alignItems: 'center' }} data-signin-managers>
          {managers.map((m) => <Chip key={m} component={Link} to={`/cosmos-ui/servapps/containers/${encodeURIComponent(m)}`} clickable label={t('mgmt.servapps.signIn.card.manager', 'Its web manager: {{name}}', { name: m })} />)}
          <PermissionGuard permission={PERM_RESOURCES}>
            <Button size="small" variant="outlined" sx={touch} onClick={() => setAdding(true)} data-add-manager-open>{t('mgmt.servapps.signIn.card.addManager', 'Add a web manager')}</Button>
          </PermissionGuard>
        </Stack>}
      </>}
    </Stack>
    {adding && <AddManagerDialog open={adding} onClose={() => setAdding(false)} containerName={name} refresh={refresh} />}
  </div>;
};

export default SignInDetails;

// The ServApps tile's way to the card: a key, for those whose role may read credentials.
export const SignInLink = ({ containerName }) => {
  const { t } = useTranslation();
  const { hasRolePermission } = useClientInfos();
  if (!hasRolePermission(PERM_CREDENTIALS_READ) || !containerName) return null;
  const label = t('mgmt.servapps.signIn.card.tileLink', 'Sign-in details of {{name}}', { name: containerName });
  return <Tooltip title={label}>
    <IconButton component={Link} to={`/cosmos-ui/servapps/containers/${encodeURIComponent(containerName)}#sign-in`} size="small" aria-label={label}
      sx={{ '&&&': { minWidth: { xs: 44, sm: 0 }, minHeight: { xs: 44, sm: 0 } } }} data-signin-tile={containerName}>
      <KeyOutlined />
    </IconButton>
  </Tooltip>;
};

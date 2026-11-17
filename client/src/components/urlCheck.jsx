// Fork addition (plan 44): does an app accept the address it is reached at?
//
// - UrlCheckDialog: checks an app's URL(s) (POST /api/url-check), says in
//   words what the app objects to, shows the app's own words as text, what
//   Cosmos tried (folded), and the repairs: the URL setting "tell the app its
//   own address" (one confirmation for every listed URL of the app that fails
//   the same way, each changed by its own call, then checked again), the
//   app's variables (the container is edited and created again, as the
//   start-problem repair does), configuration lines to copy, advice (folded).
// - UrlCheckButton: "Check this URL" (an icon variant with a tooltip).
// - useUrlChecks: the last checks of some URLs, for administrators, asked in
//   one batch per page (POST /api/url-checks, which checks a few stale ones).
// - UrlProblemMark: "Does not open" next to an app's links when its check says so.
// - UrlCheckNotice: after a URL was made or changed: checks it and, unless it
//   opens, says so with "Why?".
// Everything the app said is shown as text, never as HTML.
import * as React from 'react';
import {
  Alert, Box, Button, Collapse, Dialog, DialogActions, DialogContent, DialogTitle, IconButton, Stack, TextField, Tooltip, Typography,
} from '@mui/material';
import { CheckCircleOutlined, ExclamationCircleOutlined, FileSearchOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';

import * as API from '../api';
import { useClientInfos } from '../utils/hooks';
import { PERM_ADMIN } from '../utils/permissions';
import { CopyButton } from './startProblem';
import { attentionOf, envFields, envWith, knownAppFor, knownAppNeeds, noteWords, orderedRepairs, publicFacts, resultWords, sharingRouteRepair, whyWords } from '../utils/urlCheck';

const wrap = { overflowWrap: 'anywhere', minWidth: 0, '& .MuiAlert-message': { minWidth: 0 } };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ---- the results kept for the page, asked in one batch -----------------------------------

const store = { results: {}, asked: new Set(), pending: new Set(), listeners: new Set(), timer: null };
const notify = () => store.listeners.forEach((fn) => fn({ ...store.results }));
export const rememberResult = (result) => {
  if (result && result.route) {
    store.results[result.route] = result;
    notify();
  }
};
// in batches of 64 (the server's bound); the server checks a few stale ones per call, so the
// names it did not answer yet are asked again, until a call brings nothing new
const flush = async () => {
  store.timer = null;
  let queue = [...store.pending];
  store.pending.clear();
  while (queue.length) {
    const batch = queue.slice(0, 64);
    let res;
    try {
      res = await API.urlCheck.checks(batch);
    } catch (e) {
      return; // not known (or too many checks for now): nothing is marked
    }
    const answered = Object.values((res && res.data) || {}).filter((r) => r && r.route);
    answered.forEach((r) => { store.results[r.route] = r; });
    notify();
    const done = new Set(answered.map((r) => r.route));
    const left = [...batch.filter((n) => !done.has(n)), ...queue.slice(64)];
    if (left.length === queue.length) return; // nothing new: the rest is not checked now (not running, no web address…)
    queue = left;
    await sleep(1000);
  }
};

// The last checks of these URLs (administrators only), { name: result }.
export const useUrlChecks = (routeNames) => {
  const { hasPermission } = useClientInfos();
  const admin = hasPermission(PERM_ADMIN);
  const key = (routeNames || []).filter(Boolean).join('\n');
  const [results, setResults] = React.useState(() => ({ ...store.results }));
  React.useEffect(() => {
    store.listeners.add(setResults);
    return () => { store.listeners.delete(setResults); };
  }, []);
  React.useEffect(() => {
    if (!admin || !key) return;
    key.split('\n').forEach((name) => {
      if (!store.asked.has(name)) {
        store.asked.add(name);
        store.pending.add(name);
      }
    });
    if (store.pending.size && !store.timer) store.timer = setTimeout(flush, 300);
  }, [admin, key]);
  return admin ? results : {};
};

// ---- one URL's result -------------------------------------------------------------------

const Attempts = ({ result }) => {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const attempts = (result && result.attempts) || [];
  if (!attempts.length) return null;
  return <Stack spacing={1} sx={{ minWidth: 0 }}>
    <Button size="small" sx={{ alignSelf: 'flex-start' }} onClick={() => setOpen(!open)} data-url-check-tried>
      {open ? t('mgmt.urlCheck.hideTried', 'Hide what Cosmos tried') : t('mgmt.urlCheck.showTried', 'What Cosmos tried')}
    </Button>
    <Collapse in={open} unmountOnExit>
      <Stack spacing={1} sx={{ minWidth: 0 }}>
        {attempts.map((a, i) => <Box key={i} sx={{ p: 1, bgcolor: 'action.hover', borderRadius: 1, overflowWrap: 'anywhere', minWidth: 0 }}>
          <Typography variant="body2" component="div">
            {a.appOwnHost
              ? t('mgmt.urlCheck.triedOwn', 'Telling the app its own address: Host {{host}}, the public address {{public}} alongside.', { host: a.host, public: a.forwardedHost, interpolation: { escapeValue: false } })
              : t('mgmt.urlCheck.triedPublic', 'As a visitor arrives: Host {{host}}.', { host: a.host, interpolation: { escapeValue: false } })}
            {a.noVisitorAddress ? ' ' + t('mgmt.urlCheck.triedNoVisitor', 'Without the visitor\'s address.') : ''}
          </Typography>
          <Typography variant="caption" color="textSecondary" component="div">
            {a.error
              ? t('mgmt.urlCheck.triedError', 'No answer ({{error}}).', { error: a.error })
              : t('mgmt.urlCheck.triedStatus', 'Answer {{status}}', { status: a.status })}
            {a.location ? ' — ' + t('mgmt.urlCheck.triedLocation', 'sends to {{location}}', { location: a.location, interpolation: { escapeValue: false } }) : ''}
            {a.first ? ' — “' + a.first + '”' : ''}
          </Typography>
        </Box>)}
      </Stack>
    </Collapse>
  </Stack>;
};

// The words of a repair's `why` id.
const WhyText = ({ why }) => {
  const { t } = useTranslation();
  const w = whyWords(why);
  if (!w) return null;
  return <Typography variant="caption" color="textSecondary" component="div">{w.key ? t(w.key, w.text) : w.text}</Typography>;
};

// Lines of a configuration file, or commands for the app's terminal, to copy.
// An env repair given as advice is shown the same way, as NAME=value.
const FileLines = ({ repair, advice }) => {
  const { t } = useTranslation();
  const lines = repair.kind === 'env' ? [repair.name + '=' + (repair.value || '')] : (repair.lines || []);
  const text = lines.join('\n');
  const p = { path: repair.path, section: repair.section, user: repair.user || 'root', interpolation: { escapeValue: false } };
  let intro;
  if (repair.kind === 'command') {
    intro = t('mgmt.urlCheck.commandIntro', 'Run in the app\'s terminal (its page > Terminal), as {{user}}:', p);
  } else if (repair.kind === 'env') {
    intro = t('mgmt.urlCheck.envAdvice', 'Recommended setting of the app (its page, Settings), then it is created again:');
  } else if (repair.section) {
    intro = advice
      ? t('mgmt.urlCheck.adviceIntroSection', 'Recommended in {{path}}, in the section {{section}}, then restart the app:', p)
      : t('mgmt.urlCheck.fileIntroSection', 'Add these lines to {{path}}, in the section {{section}}, then restart the app:', p);
  } else {
    intro = advice
      ? t('mgmt.urlCheck.adviceIntro', 'Recommended in {{path}} (inside the container), then restart the app:', p)
      : t('mgmt.urlCheck.fileIntro', 'Add these lines to {{path}} (inside the container), then restart the app:', p);
  }
  return <Stack spacing={1} sx={{ minWidth: 0 }} data-url-check-file={repair.path || repair.name || repair.kind}>
    <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>{intro}</Typography>
    <WhyText why={repair.why} />
    <Stack direction="row" spacing={1} sx={{ alignItems: 'flex-start', minWidth: 0 }}>
      <Box component="pre" sx={{ m: 0, p: 1.5, flex: 1, minWidth: 0, overflow: 'auto', fontSize: 12, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', bgcolor: 'action.hover', borderRadius: 1 }}>{text}</Box>
      <CopyButton value={text} />
    </Stack>
  </Stack>;
};

const Advice = ({ list }) => {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  if (!list.length) return null;
  return <Stack spacing={1} sx={{ minWidth: 0 }}>
    <Button size="small" sx={{ alignSelf: 'flex-start' }} onClick={() => setOpen(!open)} data-url-check-advice>
      {open ? t('mgmt.urlCheck.hideAdvice', 'Hide the advice') : t('mgmt.urlCheck.showAdvice', 'Advice for this app')}
    </Button>
    <Collapse in={open} unmountOnExit>
      <Stack spacing={2}>{list.map((r, i) => <FileLines key={i} repair={r} advice />)}</Stack>
    </Collapse>
  </Stack>;
};

// The words of a result, with the app's own words.
export const UrlCheckResult = ({ result }) => {
  const { t } = useTranslation();
  if (!result) return null;
  const words = resultWords(result);
  return <Stack spacing={1} sx={{ minWidth: 0 }} data-url-check-result={result.route} data-url-check-verdict={result.verdict}>
    <Alert severity={words.severity} sx={wrap}>
      {t(words.key, words.text, { ...words.params, interpolation: { escapeValue: false } })}
    </Alert>
    {result.line && attentionOf(result) && <Typography variant="body2" component="div" sx={{ overflowWrap: 'anywhere' }} data-url-check-line>
      {t('mgmt.urlCheck.itSays', '{{app}} says:', { app: words.params.app, interpolation: { escapeValue: false } })} <Box component="span" sx={{ fontFamily: 'monospace' }}>{result.line}</Box>
    </Typography>}
    <Attempts result={result} />
  </Stack>;
};

// ---- the dialog --------------------------------------------------------------------------

// The title and explanation of a URL setting repair.
const routeRepairWords = (t, repair, app) => {
  const p = { app, interpolation: { escapeValue: false } };
  if (repair.setting === 'noVisitorAddress') {
    return repair.on
      ? { title: t('mgmt.urlCheck.noVisitorTitle', 'Open it without telling it the visitor\'s address', p),
        help: t('mgmt.urlCheck.noVisitorHelp', 'A temporary step, to reach {{app}}\'s own settings: Cosmos then sends it no visitor\'s address, so {{app}} sees every visitor at Cosmos\' private address. Anything in {{app}} that trusts local addresses then admits everyone, the whole internet included (Home Assistant\'s trusted networks, Jellyfin\'s local network, Sonarr\'s and Radarr\'s "disabled for local addresses"), and one stranger\'s failed sign-ins make {{app}} ban Cosmos\' address, which locks everyone out. Do the step shown here at once, then switch this off again.', p) }
      : { title: t('mgmt.urlCheck.visitorTitle', 'Tell {{app}} the visitor\'s address again', p),
        help: t('mgmt.urlCheck.visitorHelp', 'Cosmos then sends {{app}} the visitor\'s address in X-Forwarded-For, as it does for most apps.', p) };
  }
  return repair.on
    ? { title: t('mgmt.urlCheck.ownTitle', 'Tell {{app}} its own address', p),
      help: t('mgmt.urlCheck.ownHelp', 'Cosmos then sends {{app}} its own address as the name it is asked for, and the address the visitor typed in X-Forwarded-Host, as the app\'s guide for reverse proxies asks. Nothing of the app\'s own protection is switched off.', p) }
    : { title: t('mgmt.urlCheck.publicTitle', 'Tell {{app}} the public address', p),
      help: t('mgmt.urlCheck.publicHelp', 'Cosmos then sends {{app}} the address the visitor typed, as it does for most apps.', p) };
};

export const UrlCheckDialog = ({ routeNames, initial, recheck = true, onClose, refresh }) => {
  const { t } = useTranslation();
  const names = (routeNames || []).filter(Boolean).slice(0, 8);
  const [results, setResults] = React.useState(() => ({ ...(initial || {}) }));
  const [busy, setBusy] = React.useState('');
  const [error, setError] = React.useState('');
  const [fixed, setFixed] = React.useState([]);
  const [changed, setChanged] = React.useState(false);
  const [env, setEnv] = React.useState({});

  const checkOne = async (name) => {
    const res = await API.urlCheck.check(name);
    const r = res && res.data;
    if (r) {
      rememberResult(r);
      setResults((old) => ({ ...old, [name]: r }));
    }
    return r;
  };
  const checkAll = async (onlyMissing) => {
    setBusy('check');
    setError('');
    try {
      for (const name of names) if (!onlyMissing || !(initial && initial[name])) await checkOne(name);
    } catch (e) {
      setError(e.message || String(e));
    }
    setBusy('');
  };
  React.useEffect(() => { checkAll(!recheck); }, [names.join('\n')]);

  // the first URL that fails, and what it shares with the others
  const failing = names.map((n) => results[n]).filter((r) => attentionOf(r));
  const lead = failing[0] || null;
  const repairs = orderedRepairs(lead);
  const sharing = lead ? sharingRouteRepair(results, lead) : [];
  React.useEffect(() => {
    if (lead) setEnv((old) => (old[lead.route] ? old : { ...old, [lead.route]: envFields(repairs.env) }));
  }, [lead && lead.route, lead && lead.checkedAt]);

  const afterChange = (list, outcomes) => {
    setFixed(list.filter((n, i) => outcomes[i] && !attentionOf(outcomes[i]) && outcomes[i].verdict !== 'unreachable'));
    setChanged(true);
  };

  const applyRoute = async () => {
    setBusy('route');
    setError('');
    const outcomes = [];
    try {
      for (let i = 0; i < sharing.length; i++) {
        const name = sharing[i];
        // every call but the last says that another follows: the server reloads its URLs once, after the last
        const res = await API.urlCheck.apply(name, repairs.route.setting, !!repairs.route.on, i < sharing.length - 1);
        const r = res && res.data;
        if (r && r.verdict === 'notChecked' && r.reason === 'tooMany') {
          outcomes.push(null);
          continue;
        }
        if (r) {
          rememberResult(r);
          setResults((old) => ({ ...old, [name]: r }));
        }
        outcomes.push(r);
      }
      // the server reloads its URLs 2 s after the last change: "opens now" is said once it did
      await sleep(3500);
      afterChange(sharing, outcomes);
    } catch (e) {
      setError(e.message || String(e));
    }
    setBusy('');
  };

  const applyEnv = async () => {
    const fields = env[lead.route] || [];
    setBusy('env');
    setError('');
    try {
      const inspected = await API.docker.get(lead.container);
      const current = (inspected && inspected.data && inspected.data.Config && inspected.data.Config.Env) || [];
      await API.docker.updateContainer(lead.container, { envVars: envWith(current, fields) });
      // the container is created again: asked once it had time to start
      await sleep(15000);
      const outcomes = [];
      for (const name of names) outcomes.push(await checkOne(name));
      afterChange(names, outcomes);
    } catch (e) {
      setError(e.message || String(e));
    }
    setBusy('');
  };

  const close = () => {
    onClose && onClose();
    if (changed && refresh) refresh();
  };

  const appName = (lead && (lead.appName || lead.container)) || '';
  return <Dialog open onClose={(e, reason) => { if (!busy && reason !== 'backdropClick') close(); }} fullWidth maxWidth="sm" data-url-check-dialog>
    <DialogTitle sx={{ overflowWrap: 'anywhere' }}>
      {names.length === 1
        ? t('mgmt.urlCheck.titleOne', 'Does {{url}} open?', { url: (results[names[0]] && results[names[0]].url) || names[0], interpolation: { escapeValue: false } })
        : t('mgmt.urlCheck.titleMany', 'Do the addresses of this app open?')}
    </DialogTitle>
    <DialogContent>
      <Stack spacing={2} sx={{ minWidth: 0, pt: 1 }}>
        {busy === 'check' && <Alert severity="info" sx={wrap}>{t('mgmt.urlCheck.checking', 'Asking the app the way a visitor arrives…')}</Alert>}
        {fixed.map((name) => <Alert key={name} severity="success" sx={wrap} data-url-check-fixed={name}>
          {t('mgmt.urlCheck.opensNow', '{{url}} opens now.', { url: (results[name] && results[name].url) || name, interpolation: { escapeValue: false } })}
        </Alert>)}
        {names.map((name) => results[name] && !fixed.includes(name) && <UrlCheckResult key={name} result={results[name]} />)}
        {lead && noteWords(lead).map((n) => <Alert key={n.id} severity="info" sx={wrap} data-url-check-note={n.id}>{t(n.key, n.text, { ...n.params, interpolation: { escapeValue: false } })}</Alert>)}

        {lead && repairs.route && <Stack spacing={1} sx={{ minWidth: 0 }} data-url-check-route-repair>
          <Typography variant="subtitle1" sx={{ fontWeight: 600 }}>
            {routeRepairWords(t, repairs.route, appName).title}
          </Typography>
          <Typography variant="body2">
            {routeRepairWords(t, repairs.route, appName).help}
          </Typography>
          {repairs.route.seen && <Typography variant="body2" sx={{ color: 'success.main' }} data-url-check-seen>
            <CheckCircleOutlined /> {t('mgmt.urlCheck.seen', 'Cosmos tried this and {{app}} then answered.', { app: appName, interpolation: { escapeValue: false } })}
          </Typography>}
          {sharing.length > 1 && <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }} data-url-check-apply-names>
            {t('mgmt.urlCheck.applyNames', 'The setting is changed for: {{names}}.', { names: sharing.join(', '), interpolation: { escapeValue: false } })}
          </Typography>}
          <Button variant="contained" sx={{ alignSelf: 'flex-start' }} disabled={!!busy} loading={busy === 'route'} onClick={applyRoute} data-url-check-apply-route>
            {sharing.length > 1
              ? t('mgmt.urlCheck.applyMany', 'Apply to its {{count}} addresses', { count: sharing.length })
              : t('mgmt.urlCheck.applyOne', 'Apply')}
          </Button>
        </Stack>}

        {lead && repairs.env.length > 0 && <Stack spacing={1.5} sx={{ minWidth: 0 }} data-url-check-env-repair>
          <Typography variant="body2">
            {t('mgmt.urlCheck.envIntro', '{{app}} has to be told its address in its settings. Cosmos proposes these values; it then creates the container again with them.', { app: appName, interpolation: { escapeValue: false } })}
          </Typography>
          {(env[lead.route] || []).map((v) => <TextField key={v.name} size="small" fullWidth label={v.name} value={v.value} disabled={!!busy} data-url-check-env={v.name}
            onChange={(e) => setEnv((old) => ({ ...old, [lead.route]: old[lead.route].map((o) => (o.name === v.name ? { ...o, value: e.target.value.slice(0, 2048) } : o)) }))}
            helperText={v.now
              ? t('mgmt.urlCheck.envNow', 'Today: {{value}}', { value: v.now, interpolation: { escapeValue: false } })
              : t('mgmt.urlCheck.envUnset', 'Not set today.')} />)}
          {(env[lead.route] || []).filter((v) => v.why).map((v) => <WhyText key={'why-' + v.name} why={v.why} />)}
          <Button variant="contained" sx={{ alignSelf: 'flex-start' }} disabled={!!busy || !(env[lead.route] || []).every((v) => v.value)} loading={busy === 'env'} onClick={applyEnv} data-url-check-apply-env>
            {t('mgmt.urlCheck.applyEnv', 'Apply and create the container again')}
          </Button>
          {busy === 'env' && <Alert severity="info" sx={wrap}>{t('mgmt.urlCheck.applyingEnv', 'Creating the container again with these settings, then checking the address again…')}</Alert>}
        </Stack>}

        {lead && repairs.file.map((r, i) => <FileLines key={i} repair={r} />)}
        {lead && <Advice list={repairs.advice} />}
        {lead && !repairs.route && !repairs.env.length && !repairs.file.length && <Typography variant="body2" color="textSecondary">
          {t('mgmt.urlCheck.noRepair', 'Cosmos knows no repair for this. The app\'s documentation for reverse proxies says what it needs to be told; the app\'s own words above say what it objects to.')}
        </Typography>}
        {error && <Alert severity="error" sx={wrap}>{error}</Alert>}
      </Stack>
    </DialogContent>
    <DialogActions>
      <Button onClick={() => checkAll(false)} disabled={!!busy} data-url-check-again>{t('mgmt.urlCheck.again', 'Check again')}</Button>
      <Button onClick={close} disabled={!!busy}>{t('global.close', 'Close')}</Button>
    </DialogActions>
  </Dialog>;
};

// "Check this URL" (icon: a tooltip says it).
export const UrlCheckButton = ({ routeNames, icon, refresh, size = 'small', sx }) => {
  const { t } = useTranslation();
  const { hasRolePermission } = useClientInfos();
  const [open, setOpen] = React.useState(false);
  if (!hasRolePermission(PERM_ADMIN) || !(routeNames || []).length) return null;
  const label = (routeNames || []).length > 1 ? t('mgmt.urlCheck.buttonMany', 'Check its addresses') : t('mgmt.urlCheck.button', 'Check this URL');
  return <>
    {icon
      ? <Tooltip title={label}><IconButton size={size} sx={sx} aria-label={label} onClick={() => setOpen(true)} data-url-check-button><FileSearchOutlined /></IconButton></Tooltip>
      : <Button size={size} sx={sx} variant="outlined" startIcon={<FileSearchOutlined />} onClick={() => setOpen(true)} data-url-check-button>{label}</Button>}
    {open && <UrlCheckDialog routeNames={routeNames} onClose={() => setOpen(false)} refresh={refresh} />}
  </>;
};

// "Does not open" next to an app's links, when its last check says so.
export const UrlProblemMark = ({ routeNames, refresh }) => {
  const { t } = useTranslation();
  const results = useUrlChecks(routeNames);
  const [open, setOpen] = React.useState(false);
  // the URLs the dialog was opened for: it stays while open, though a repair that worked takes the mark away under it
  const [shown, setShown] = React.useState([]);
  const bad = (routeNames || []).map((n) => results[n]).filter((r) => attentionOf(r));
  const dialog = open && <UrlCheckDialog routeNames={shown} initial={results} recheck={false} onClose={() => setOpen(false)} refresh={refresh} />;
  const error = bad.some((r) => attentionOf(r) === 'error');
  const names = [...bad.map((r) => r.route), ...(routeNames || []).filter((n) => !bad.some((r) => r.route === n))];
  // one shape whether the mark is shown or not, so the open dialog keeps what it did
  return <>
    {bad.length > 0 && <Tooltip title={t('mgmt.urlCheck.markTip', 'Cosmos asked the app as a visitor arrives: see why and how to repair it')}>
      <Button size="small" variant="outlined" color={error ? 'error' : 'warning'} startIcon={<ExclamationCircleOutlined />}
        onClick={(e) => { e.stopPropagation(); setShown(names); setOpen(true); }} data-url-problem={bad.map((r) => r.route).join(',')}
        sx={{ py: 0, minHeight: 0, lineHeight: 1.6, whiteSpace: 'nowrap', flexShrink: 0, minWidth: 'max-content', mr: '5px' }}>
        {error ? t('mgmt.urlCheck.mark', 'Does not open') : t('mgmt.urlCheck.markWarning', 'Needs a setting')}
      </Button>
    </Tooltip>}
    {dialog}
  </>;
};

// After a URL was made or changed: checked, and said when it does not open.
export const UrlCheckNotice = ({ routeNames, refresh, delay = 1500 }) => {
  const { t } = useTranslation();
  const { hasPermission } = useClientInfos();
  const admin = hasPermission(PERM_ADMIN);
  const key = (routeNames || []).filter(Boolean).join('\n');
  const [results, setResults] = React.useState(null);
  const [open, setOpen] = React.useState(false);
  React.useEffect(() => {
    if (!admin || !key) return undefined;
    let live = true;
    setResults(null);
    (async () => {
      await sleep(delay); // the routes are reloaded after a save
      const out = {};
      for (const name of key.split('\n').slice(0, 4)) {
        try {
          const res = await API.urlCheck.check(name);
          if (res && res.data) { out[name] = res.data; rememberResult(res.data); }
        } catch (e) { /* not checked: nothing said */ }
      }
      if (live) setResults(out);
    })();
    return () => { live = false; };
  }, [admin, key]);
  if (!results) return null;
  const bad = Object.values(results).filter((r) => attentionOf(r));
  if (!bad.length) return null;
  const words = resultWords(bad[0]);
  return <>
    <Alert severity={words.severity} sx={wrap} data-url-check-notice
      action={<Button color="inherit" size="small" onClick={() => setOpen(true)} data-url-check-why>{t('mgmt.urlCheck.why', 'Why?')}</Button>}>
      {t('mgmt.urlCheck.noticeOpen', '{{url}} does not open yet:', { url: bad[0].url || bad[0].route, interpolation: { escapeValue: false } })}{' '}
      {t(words.key, words.text, { ...words.params, interpolation: { escapeValue: false } })}
    </Alert>
    {open && <UrlCheckDialog routeNames={key.split('\n')} initial={results} recheck={false} onClose={() => setOpen(false)} refresh={refresh} />}
  </>;
};

// ---- known apps (GET /api/known-apps), read once per page load ---------------------------

let knownAppsOnce = null;
export const useKnownApps = () => {
  const [apps, setApps] = React.useState(null);
  React.useEffect(() => {
    let live = true;
    if (!knownAppsOnce) {
      knownAppsOnce = API.urlCheck.knownApps().then((res) => (res && Array.isArray(res.data) ? res.data : [])).catch(() => []);
    }
    knownAppsOnce.then((list) => live && setApps(list));
    return () => { live = false; };
  }, []);
  return apps;
};

// What a known app needs behind Cosmos, while a URL is made or edited: the
// URL setting (proposed for a new URL, undoable; offered for an existing
// one), and the variables of the app that must name the address.
export const KnownAppHint = ({ image, host, port, appOwnHost, isNew, onSet }) => {
  const { t } = useTranslation();
  const apps = useKnownApps();
  const app = knownAppFor(apps, image);
  const proposed = React.useRef(false);
  const [undone, setUndone] = React.useState(false);
  const facts = host ? publicFacts(host, port) : null;
  const needs = app ? knownAppNeeds(app, facts || publicFacts('', port)) : null;
  React.useEffect(() => {
    if (isNew && app && app.appOwnHost && !appOwnHost && !proposed.current && !undone) {
      proposed.current = true;
      onSet && onSet(true);
    }
  }, [isNew, app && app.id]);
  if (!app) return null;
  const env = (needs && facts) ? needs.env : [];
  return <Stack spacing={1} sx={{ minWidth: 0 }} data-known-app={app.id}>
    {app.appOwnHost && appOwnHost && <Alert severity="info" sx={wrap} data-known-app-own-host
      action={<Button color="inherit" size="small" onClick={() => { setUndone(true); onSet && onSet(false); }}>{t('mgmt.urlCheck.known.undo', 'Undo')}</Button>}>
      {t('mgmt.urlCheck.known.ownSet', '{{app}} only answers its own address: this URL tells it that address, and the one visitors use alongside ("Tell the app its own address").', { app: app.name, interpolation: { escapeValue: false } })}
    </Alert>}
    {app.appOwnHost && !appOwnHost && <Alert severity="warning" sx={wrap} data-known-app-own-host-off
      action={<Button color="inherit" size="small" onClick={() => { setUndone(false); onSet && onSet(true); }}>{t('mgmt.urlCheck.known.use', 'Use it')}</Button>}>
      {t('mgmt.urlCheck.known.ownNeeded', '{{app}} only answers its own address: without "Tell the app its own address" it refuses this URL.', { app: app.name, interpolation: { escapeValue: false } })}
    </Alert>}
    {app.noOwnHost && appOwnHost && <Alert severity="warning" sx={wrap} data-known-app-no-own-host
      action={<Button color="inherit" size="small" onClick={() => onSet && onSet(false)}>{t('mgmt.urlCheck.known.turnOff', 'Turn it off')}</Button>}>
      {t('mgmt.urlCheck.known.noOwn', '{{app}} refuses requests made to its own address: "Tell the app its own address" must be off for it.', { app: app.name, interpolation: { escapeValue: false } })}
    </Alert>}
    {env.length > 0 && <Alert severity="info" sx={wrap} data-known-app-env>
      <Typography variant="body2" component="div">
        {t('mgmt.urlCheck.known.env', '{{app}} has to be told this address in its settings, or signing in fails. Set these variables on the app (its page, Settings):', { app: app.name, interpolation: { escapeValue: false } })}
      </Typography>
      {env.map((e) => <Typography key={e.name} variant="body2" component="div" sx={{ fontFamily: 'monospace', overflowWrap: 'anywhere' }}>{e.name}={e.value}</Typography>)}
    </Alert>}
    {app.works && !app.appOwnHost && env.length === 0 && <Typography variant="caption" color="textSecondary" component="div" data-known-app-works>
      {t('mgmt.urlCheck.known.works', '{{app}} works behind Cosmos as it is: nothing to set.', { app: app.name, interpolation: { escapeValue: false } })}
    </Typography>}
  </Stack>;
};

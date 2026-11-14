// Fork addition: "expose to the internet" (utils/exposure.js decides and
// proposes, src/cloudflare/expose.go checks and does it).
// - ExposureForm: the choices for one app — its web interface per domain
//   (through Cloudflare or directly, who may open it) and its other ports
//   (forwarded by the router under a DNS-only name) — with what each costs.
// - ExposeDialog: the form for an app that exists, with what is exposed now
//   and its state; "Save" sends only what changed (and withdraws what was
//   unticked).
// - InstallExposure: the same choices while an app is being created, sent
//   once its containers exist.
import * as React from 'react';
import {
  Alert, Button, Checkbox, Chip, Collapse, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, Stack,
  TextField, Typography,
} from '@mui/material';
import { LoadingButton } from '@mui/lab';
import { DownOutlined, GlobalOutlined, RightOutlined, WarningOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import ChoiceChips from './choiceChips';
import { keepOnBackdrop } from './formHelpers';
import { AppShields, loadCloudflare, useCloudflare } from './cloudflare';
import { RouterPortLinks, setRouterStatus, useCosmosPorts, useRouterStatus } from './routerPorts';
import { installPorts, shownAddress } from '../utils/routerPorts';
import { serverBack } from '../utils/appLinks';
import {
  accessSentence, choiceProblems, definitionPorts, exposureChanges, initialChoices, portPlan, text, throughSentence, webPlan,
  withRoutes,
} from '../utils/exposure';

const useSay = () => {
  const { t } = useTranslation();
  return (s) => (s ? t(s.key, s.text, s.params) : '');
};

const NAME_TEXT = (t, code, params) => {
  const s = text('name.' + code, params);
  return t(s.key, s.text, s.params);
};

// The choices for one app. `web` and `ports` are the plans
// (utils/exposure.js), `value` the choices, `onChange(next)` takes all of
// them. `busy` freezes the form.
export const ExposureForm = ({ name, image, web, ports, value, onChange, cloudflare, problems, busy, cosmos }) => {
  const { t } = useTranslation();
  const say = useSay();
  const [door, setDoor] = React.useState(false);
  const [renaming, setRenaming] = React.useState({});
  const setWeb = (zone, change) => onChange({ ...value, web: { ...value.web, [zone]: { ...value.web[zone], ...change } } });
  const setPort = (key, change) => onChange({ ...value, ports: { ...value.ports, [key]: { ...value.ports[key], ...change } } });

  const usual = (ports || []).filter((p) => !p.door);
  const others = (ports || []).filter((p) => p.door);
  const anyDirectWeb = web.available && web.domains.some((d) => (value.web[d.zone] || {}).on && value.web[d.zone].through === 'direct');
  const anyPort = (ports || []).some((p) => (value.ports[p.key] || {}).on);
  const domains = web.available ? web.domains : [];
  // the port web addresses are reached on from outside: as the router maps it, else as Cosmos runs
  const outside = (cosmos && cosmos.externalHttps && cosmos.externalHttps !== 443) || !(cloudflare && cloudflare.publicPort)
    ? cosmos
    : { ...(cosmos || {}), externalHttps: cloudflare.publicPort };

  const portRow = (p) => {
    const choice = value.ports[p.key] || {};
    const problem = problems && problems['port:' + p.key];
    const external = problems && problems['port:' + p.key + ':external'];
    return <Stack key={p.key} spacing={1} sx={{ minWidth: 0 }} data-expose-port={p.key}>
      <FormControlLabel
        sx={{ mr: 0, alignItems: 'flex-start' }}
        control={<Checkbox size="small" checked={!!choice.on} disabled={busy} onChange={(event) => setPort(p.key, { on: event.target.checked })} />}
        label={<Typography variant="body2" sx={{ pt: '9px', overflowWrap: 'anywhere' }}>
          {t('mgmt.expose.port.label', 'Port {{port}} ({{protocol}}), published on the server as {{hostPort}}',
            { port: p.port, protocol: p.protocol.toUpperCase(), hostPort: p.hostPort })}
        </Typography>}
      />
      {p.risky && choice.on && <Alert severity="error" icon={<WarningOutlined />} sx={{ overflowWrap: 'anywhere' }}>
        {say(text('port.risky', { port: p.port }))}
        <FormControlLabel
          sx={{ display: 'flex', mr: 0, mt: 1 }}
          control={<Checkbox size="small" checked={!!choice.risky} disabled={busy} onChange={(event) => setPort(p.key, { risky: event.target.checked })} />}
          label={<Typography variant="body2">{t('mgmt.expose.port.riskyConfirm', 'I know what is on this port and that it is protected')}</Typography>}
        />
      </Alert>}
      {p.reserved && choice.on && <Typography variant="caption" color="textSecondary">{say(text('port.reserved', { port: p.hostPort }))}</Typography>}
      {p.webGuess && choice.on && <Typography variant="caption" color="textSecondary">{say(text('port.webGuess', { port: p.port }))}</Typography>}
      {choice.on && <Stack spacing={1} sx={{ pl: { xs: 0, sm: 4 }, minWidth: 0 }}>
        {p.zone
          ? <TextField
            size="small"
            fullWidth
            label={t('mgmt.expose.port.name', 'Reached at (DNS only, not through Cloudflare)')}
            value={choice.hostname}
            disabled={busy}
            error={!!problem}
            onChange={(event) => setPort(p.key, { hostname: event.target.value.trim().toLowerCase() })}
            helperText={problem ? NAME_TEXT(t, problem, { zone: p.zone, host: choice.hostname }) : say(text('port.why'))}
            inputProps={{ 'data-expose-port-name': p.key }}
          />
          : <Typography variant="caption" color="textSecondary">{say(text('port.noName'))}</Typography>}
        <TextField
          size="small"
          label={t('mgmt.expose.port.external', 'Port on the router (optional)')}
          value={choice.externalPort}
          placeholder={String(p.hostPort)}
          disabled={busy}
          error={!!external}
          inputProps={{ inputMode: 'numeric' }}
          onChange={(event) => setPort(p.key, { externalPort: event.target.value.trim() })}
          helperText={external
            ? t('mgmt.router.dialog.externalInvalid', 'Enter a port between 1 and 65535, or leave it empty')
            : t('mgmt.expose.port.externalHelp', 'Empty: {{port}}. When the router has it taken, Cosmos takes the next free port and the address says so.', { port: p.hostPort })}
        />
      </Stack>}
    </Stack>;
  };

  return <Stack spacing={2} sx={{ minWidth: 0 }} data-expose-form>
    {/* the web interface, through Cosmos' HTTPS port */}
    <Stack spacing={1}>
      <Typography variant="subtitle2"><GlobalOutlined /> {t('mgmt.expose.web.title', 'Web interface')}</Typography>
      {!web.available && <Typography variant="body2" color="textSecondary">
        {t('mgmt.expose.web.none', '{{name}} has no URL to a web interface yet: give it one under URLs ("New"), then it can be reached from the internet as well.', { name })}
      </Typography>}
      {web.available && domains.length === 0 && <Typography variant="body2" color="textSecondary">
        {t('mgmt.expose.web.noDomain', 'No domain is configured: add one under Configuration > Domains to reach the app from the internet by name.')}
      </Typography>}
      {domains.map((d) => {
        const choice = value.web[d.zone] || {};
        const problem = problems && problems['web:' + d.zone];
        const throughOptions = [
          ...(d.canShield ? [{ value: 'cloudflare', label: t('mgmt.expose.through.cloudflareLabel', 'Through Cloudflare'), description: say(throughSentence({ reason: 'cloudflare' }, cloudflare, name)) }] : []),
          { value: 'direct', label: t('mgmt.expose.through.directLabel', 'Directly (DNS only)'), description: say(text('direct.cost')) },
        ];
        return <Stack key={d.zone} spacing={1} sx={{ minWidth: 0 }} data-expose-web={d.zone}>
          <FormControlLabel
            sx={{ mr: 0, alignItems: 'flex-start' }}
            control={<Checkbox size="small" checked={!!choice.on} disabled={busy || (d.exposed && d.exposed.own)} onChange={(event) => setWeb(d.zone, { on: event.target.checked })} />}
            label={<Typography variant="body2" sx={{ pt: '9px', overflowWrap: 'anywhere' }}>
              {t('mgmt.expose.web.label', 'Reach {{name}} at {{address}}', { name, address: 'https://' + shownAddress(choice.host, outside, 'https') })}
            </Typography>}
          />
          {d.exposed && d.exposed.own && <Typography variant="caption" color="textSecondary">
            {t('mgmt.expose.web.own', 'This is the address of the app\'s own URL: change or delete it under URLs.')}
          </Typography>}
          {choice.on && <Stack spacing={1.5} sx={{ pl: { xs: 0, sm: 4 }, minWidth: 0 }}>
            {renaming[d.zone] || problem
              ? <TextField
                size="small"
                fullWidth
                label={t('mgmt.expose.web.name', 'Address')}
                value={choice.host}
                disabled={busy}
                error={!!problem}
                helperText={problem ? NAME_TEXT(t, problem, { zone: d.zone, host: choice.host }) : t('mgmt.expose.web.nameHelp', 'A name under {{zone}}, for example {{example}}', { zone: d.zone, example: d.host })}
                onChange={(event) => setWeb(d.zone, { host: event.target.value.trim().toLowerCase() })}
              />
              : <Button size="small" sx={{ alignSelf: 'flex-start' }} onClick={() => setRenaming({ ...renaming, [d.zone]: true })} disabled={busy}>
                {t('mgmt.expose.web.rename', 'Another name')}
              </Button>}
            <ChoiceChips
              id={'expose-through-' + d.zone}
              label={t('mgmt.expose.through.label', 'How it goes out')}
              value={choice.through}
              options={throughOptions}
              disabled={busy}
              onChange={(through) => setWeb(d.zone, { through })}
            />
            {!d.canShield && <Typography variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
              {say(throughSentence(d, cloudflare, name))}
            </Typography>}
            {d.canShield && d.reason === 'media' && choice.through === 'direct' && <Typography variant="caption" color="textSecondary">
              {say(throughSentence(d, cloudflare, name))}
            </Typography>}
            <ChoiceChips
              id={'expose-access-' + d.zone}
              label={t('mgmt.urls.guide.access.label', 'Who may open it')}
              value={choice.access}
              disabled={busy}
              onChange={(access) => setWeb(d.zone, { access })}
              options={[
                { value: 'anyone', label: t('mgmt.urls.guide.access.anyone', 'Anyone with the address'), description: t('mgmt.urls.guide.access.anyoneHint', 'Cosmos asks nothing: the app\'s own sign-in is what protects it. Fine for apps with their own accounts.') },
                { value: 'users', label: t('mgmt.urls.guide.access.users', 'People signed in to Cosmos'), description: t('mgmt.urls.guide.access.usersHint', 'Cosmos asks for its sign-in first. For apps without a sign-in of their own.') },
                { value: 'admins', label: t('mgmt.urls.guide.access.admins', 'Cosmos administrators only'), description: t('mgmt.urls.guide.access.adminsHint', 'Cosmos asks for its sign-in and lets administrators through only.') },
              ]}
            />
            {!d.exposed && d.accessSource !== 'own' && <Typography variant="caption" color="textSecondary">{say(accessSentence(d.accessSource, name))}</Typography>}
          </Stack>}
        </Stack>;
      })}
    </Stack>

    {/* everything else the app publishes, through the router */}
    {(ports || []).length > 0 && <Stack spacing={1}>
      <Typography variant="subtitle2">{t('mgmt.expose.ports.title', 'Other ports (not web pages)')}</Typography>
      <Typography variant="caption" color="textSecondary">
        {t('mgmt.expose.ports.text', 'Game servers, peer ports, MQTT and the like cannot pass Cloudflare\'s proxy: the router forwards them (UPnP). Nothing is opened unless you tick it.')}
      </Typography>
      {usual.map(portRow)}
      {others.length > 0 && <>
        <Button size="small" sx={{ alignSelf: 'flex-start' }} startIcon={door ? <DownOutlined /> : <RightOutlined />} onClick={() => setDoor(!door)} data-expose-door>
          {t('mgmt.expose.ports.door', 'Ports that are dangerous to publish ({{count}})', { count: others.length })}
        </Button>
        <Collapse in={door || others.some((p) => (value.ports[p.key] || {}).on)}>
          <Stack spacing={1}>{others.map(portRow)}</Stack>
        </Collapse>
      </>}
    </Stack>}

    {(anyDirectWeb || anyPort) && <Alert severity="warning" sx={{ overflowWrap: 'anywhere', '& .MuiAlert-message': { minWidth: 0 } }} data-expose-cost>
      {anyPort ? say(text('port.cost')) : say(text('direct.cost'))}
    </Alert>}
  </Stack>;
};

const containerPortsOf = (container) => ((container && container.Ports) || [])
  .filter((p) => p.PublicPort)
  .map((p) => ({ port: p.PrivatePort, protocol: p.Type || 'tcp', hostPort: p.PublicPort, hostIp: p.IP || '' }));

// The answer of a refused change, in words.
const refusalText = (t, res) => (res && res.message) || t('mgmt.expose.failed', 'The change could not be made.');

// After a change: the server may reload (new URLs), then everything is read again.
const afterChange = async (reloaded, refresh) => {
  if (reloaded) {
    await serverBack();
  }
  loadCloudflare(true);
  API.config.upnpStatus(true).then((res) => setRouterStatus(res && res.data)).catch(() => {});
  if (refresh) await refresh();
};

// The dialog for an app that exists. `containerName` without "/".
export const ExposeDialog = ({ config, containerName, image, onClose, refresh }) => {
  const { t } = useTranslation();
  const cloudflareState = useCloudflare();
  const cloudflare = cloudflareState.data;
  const router = useRouterStatus();
  const cosmos = useCosmosPorts(config); // the port web addresses are reached on from outside
  const [container, setContainer] = React.useState(null);
  const [value, setValue] = React.useState(null);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState('');
  const [done, setDone] = React.useState(false);

  React.useEffect(() => {
    API.docker.list().then((res) => {
      setContainer(((res && res.data) || []).find((c) => (c.Names || []).some((n) => n.replace(/^\//, '') === containerName)) || { Ports: [] });
    }).catch(() => setContainer({ Ports: [] }));
  }, [containerName]);

  const web = webPlan({ config, cloudflare, containerName, image });
  const ports = container ? portPlan({ config, cloudflare, router, containerName, ports: containerPortsOf(container), webHosts: web.domains.filter((d) => d.exposed).map((d) => d.exposed.host) }) : [];
  const before = initialChoices(web, ports);
  // the proposals need the domains' state: wait for its first answer
  const ready = !!container && cloudflareState.at > 0;
  React.useEffect(() => {
    if (ready && value === null) setValue(before);
  }, [ready]);

  const current = value || before;
  const problems = choiceProblems(web, ports, current, config, containerName);
  const changes = exposureChanges(containerName, web, ports, before, current);
  const nothing = !changes.expose && !changes.withdraw;

  const save = async () => {
    setBusy(true);
    setError('');
    let reloaded = false;
    for (const body of [changes.withdraw, changes.expose].filter(Boolean)) {
      const res = await API.config.expose(body);
      if (!res.ok) {
        setError(refusalText(t, res));
        setBusy(false);
        await afterChange(reloaded, refresh);
        return;
      }
      reloaded = reloaded || !!(res.data && res.data.reloaded);
    }
    await afterChange(reloaded, refresh);
    setBusy(false);
    setDone(true);
    setValue(null);
  };

  const exposedHosts = web.domains.filter((d) => d.exposed).map((d) => d.exposed.host);
  const close = () => { if (!busy) onClose(); };

  return <Dialog open onClose={keepOnBackdrop(close)} fullWidth maxWidth="sm" data-expose-dialog>
    <DialogTitle sx={{ overflowWrap: 'anywhere' }}>{t('mgmt.expose.title', '{{name}} on the internet', { name: containerName })}</DialogTitle>
    <DialogContent>
      <Stack spacing={2} sx={{ minWidth: 0 }}>
        {!ready && <Typography variant="body2">{t('mgmt.expose.loading', 'Looking at the app\'s ports and the domains…')}</Typography>}
        {ready && <ExposureForm
          name={containerName}
          image={image}
          web={web}
          ports={ports}
          value={current}
          onChange={(next) => { setValue(next); setDone(false); }}
          cloudflare={cloudflare}
          problems={problems}
          busy={busy}
          cosmos={cosmos}
        />}
        {(exposedHosts.length > 0 || (router && (router.apps || []).some((m) => m.container === containerName))) && <Stack spacing={1} data-expose-now>
          <Typography variant="subtitle2">{t('mgmt.expose.now', 'Reached from the internet now')}</Typography>
          <AppShields hosts={exposedHosts} image={image} name={containerName} dense />
          <div><RouterPortLinks config={config} containerNames={[containerName]} chipStyle={{ margin: '0 5px 5px 0' }} /></div>
        </Stack>}
        {done && <Alert severity="success">{t('mgmt.expose.saved', 'Done. Records at Cloudflare and the router follow within a minute.')}</Alert>}
        {error && <Alert severity="error" sx={{ overflowWrap: 'anywhere' }}>{error}</Alert>}
      </Stack>
    </DialogContent>
    <DialogActions sx={{ flexWrap: 'wrap' }}>
      <Button onClick={close} disabled={busy}>{t('global.close', 'Close')}</Button>
      <LoadingButton loading={busy} variant="contained" disabled={nothing || Object.keys(problems).length > 0 || ports.some((p) => p.risky && current.ports[p.key] && current.ports[p.key].on && !(p.exposed) && !current.ports[p.key].risky)} onClick={save} data-expose-save>
        {t('global.saveAction', 'Save')}
      </LoadingButton>
    </DialogActions>
  </Dialog>;
};

const parseService = (text, fallback) => {
  try {
    const parsed = JSON.parse(text);
    return parsed && typeof parsed === 'object' ? parsed : fallback;
  } catch (e) {
    return fallback;
  }
};

// The routes of a service definition (what the installers send), as routes.
const definitionRoutes = (service) => Object.values((service && service.services) || {}).flatMap((s) => (s && Array.isArray(s.routes) ? s.routes : [])
  .map((r) => ({ Name: r.name || r.Name, Mode: r.mode || r.Mode || 'SERVAPP', UseHost: r.useHost !== undefined ? r.useHost : r.UseHost, Host: r.host || r.Host, Target: r.target || r.Target, AuthEnabled: r.authEnabled || r.AuthEnabled })));

// The exposure step of an installation: the containers of the definition,
// each with its choices, nothing ticked. Sent once `done` says they exist.
export const InstallExposure = ({ service, definition, done }) => {
  const { t } = useTranslation();
  const [config, setConfig] = React.useState(null);
  const cosmos = useCosmosPorts(config);
  React.useEffect(() => {
    API.config.get().then((res) => setConfig(res && res.data)).catch(() => {});
  }, []);
  const cloudflare = useCloudflare().data;
  const router = useRouterStatus();
  const [values, setValues] = React.useState({});
  const [results, setResults] = React.useState(null);
  const sent = React.useRef(false);

  const parsed = parseService(definition, service);
  const planned = config ? withRoutes(config, definitionRoutes(parsed)) : null;
  const all = installPorts(parsed);
  const containers = Object.entries((parsed && parsed.services) || {}).map(([key, s]) => ({ name: String((s && s.container_name) || key), image: (s && s.image) || '' }));
  const plans = planned ? containers.map((c) => {
    const web = webPlan({ config: planned, cloudflare, containerName: c.name, image: c.image });
    const ports = portPlan({ config: planned, cloudflare, router, containerName: c.name, ports: definitionPorts(all, c.name) });
    return { ...c, web, ports, before: initialChoices(web, ports) };
  }).filter((p) => (p.web.available && p.web.domains.length > 0) || p.ports.length > 0) : [];

  React.useEffect(() => {
    if (!done || sent.current) return;
    sent.current = true;
    const wanted = plans.map((p) => ({ p, changes: exposureChanges(p.name, p.web, p.ports, p.before, values[p.name] || p.before) })).filter((x) => x.changes.expose);
    if (wanted.length === 0) return;
    (async () => {
      // the containers' URLs were just created: the server reloads first
      await serverBack();
      const outcome = [];
      for (const { p, changes } of wanted) {
        const res = await API.config.expose(changes.expose);
        outcome.push({ name: p.name, ok: res.ok, message: res.message });
        setResults([...outcome]);
        if (res.ok && res.data && res.data.reloaded) await serverBack();
      }
      loadCloudflare(true);
      API.config.upnpStatus(true).then((res) => setRouterStatus(res && res.data)).catch(() => {});
    })();
  }, [done]);

  if (plans.length === 0) return null;
  const chosen = plans.some((p) => exposureChanges(p.name, p.web, p.ports, p.before, values[p.name] || p.before).expose);

  return <Stack spacing={1.5} data-expose-install sx={{ minWidth: 0 }}>
    <Typography variant="subtitle1"><GlobalOutlined /> {t('mgmt.expose.install.title', 'Reach it from the internet as well? (optional)')}</Typography>
    <Typography variant="caption" color="textSecondary">
      {t('mgmt.expose.install.text', 'The app gets its local address in any case. Tick what should also be reached from the internet: everything is proposed, nothing is opened without a tick.')}
    </Typography>
    {plans.map((p) => {
      const value = values[p.name] || p.before;
      return <Stack key={p.name} spacing={1} sx={{ minWidth: 0 }}>
        {plans.length > 1 && <Typography variant="subtitle2">{p.name}</Typography>}
        <ExposureForm
          name={p.name}
          image={p.image}
          web={p.web}
          ports={p.ports}
          value={value}
          onChange={(next) => setValues({ ...values, [p.name]: next })}
          cloudflare={cloudflare}
          problems={choiceProblems(p.web, p.ports, value, planned, p.name)}
          busy={done}
          cosmos={cosmos}
        />
      </Stack>;
    })}
    {chosen && !done && <Alert severity="info">{t('mgmt.expose.install.pending', 'What is ticked is set up once the app is created.')}</Alert>}
    {results && results.map((r) => <Alert key={r.name} severity={r.ok ? 'success' : 'error'} sx={{ overflowWrap: 'anywhere' }} data-expose-install-result>
      {r.ok
        ? t('mgmt.expose.install.done', '{{name}} is set up for the internet. Records and the router follow within a minute.', { name: r.name })
        : t('mgmt.expose.install.failed', '{{name}} could not be set up for the internet: {{error}}', { name: r.name, error: r.message || '' })}
    </Alert>)}
  </Stack>;
};

// The chip that opens the dialog, beside an app's URLs.
export const ExposeChip = ({ config, containerName, image, refresh, size, style }) => {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  return <span style={{ display: 'inline-flex', ...style }}>
    <Chip
      label={t('mgmt.expose.button', 'Internet')}
      color="primary"
      variant="outlined"
      size={size}
      icon={<GlobalOutlined />}
      onClick={() => setOpen(true)}
      data-expose-open
    />
    {open && <ExposeDialog config={config} containerName={containerName} image={image} refresh={refresh} onClose={() => setOpen(false)} />}
  </span>;
};

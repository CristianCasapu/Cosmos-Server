// Fork addition (plan 43): an app that is a service, not a web app (a
// database, a message broker...). Where the panel would propose a web
// address for it (New URL, the Internet dialog, the app's page), it says
// what the app is instead, gives the address other apps on this server use,
// and offers the real choices:
//   - let these apps reach it: a network of its own with each app picked,
//     as "Link containers" on the Network tab makes it;
//   - reach it from my home network: its port published on this server,
//     after the risk is said and ticked; never forwarded by the router;
//   - a web address anyway, for an administrator who knows better (the
//     form then warns with the port check's answer).
// Nothing here sends a request before the administrator clicks.
import * as React from 'react';
import { Alert, Box, Button, Checkbox, Chip, FormControlLabel, Stack, Tooltip, Typography } from '@mui/material';
import { useTranslation } from 'react-i18next';

import * as API from '../api';
import { CopyButton, StartProblemButton } from './startProblem';
import { classifyService as classifyServiceOf0, connectionTemplate, linkNetworkName, probeWords, serviceAddress, withPublishedPort } from '../utils/services';
import { containerPorts } from '../utils/routeGuide';

const classifyServiceOf = (container) => classifyServiceOf0({ image: container.Image, ports: containerPorts(container) });
import { randomString } from '../utils/indexs';

const wrap = { overflowWrap: 'anywhere', minWidth: 0, '& .MuiAlert-message': { minWidth: 0 } };
const say = (t, w) => (w ? t(w.key, w.text, { ...w.params, interpolation: { escapeValue: false } }) : '');

// The port check's answer for the port a URL would point at, said once it is known.
export const PortCheck = ({ containerName, port }) => {
  const { t } = useTranslation();
  const [result, setResult] = React.useState(null);
  React.useEffect(() => {
    setResult(null);
    const n = Number(port);
    if (!containerName || !Number.isInteger(n) || n < 1 || n > 65535) return undefined;
    let gone = false;
    const timer = setTimeout(() => {
      API.docker.probePort(containerName, n).then((res) => { if (!gone) setResult((res && res.data) || null); }).catch(() => {});
    }, 600);
    return () => { gone = true; clearTimeout(timer); };
  }, [containerName, port]);
  const words = probeWords(result);
  if (!words || words.ok === true) return null;
  return <Alert severity={words.ok === false ? 'warning' : 'info'} sx={wrap} data-port-check={result.answer}>
    {say(t, words)}
    {words.ok === false && <><br />{t('mgmt.services.insist.still', 'You can still create the address: Cosmos then answers its visitors with the app\'s error.')}</>}
  </Alert>;
};

export const ServiceInfo = ({ containerName, classification, onInsist, insistLabel, refresh, compact }) => {
  const { t } = useTranslation();
  const name = String(containerName || '').replace(/^\//, '');
  const c = classification || {};
  const address = serviceAddress(name, c.port);
  const connection = connectionTemplate(c, name, c.port);
  const [others, setOthers] = React.useState(null);
  const [picked, setPicked] = React.useState([]);
  const [linking, setLinking] = React.useState(false);
  const [linked, setLinked] = React.useState('');
  const [lan, setLan] = React.useState(false);
  const [understood, setUnderstood] = React.useState(false);
  const [publishing, setPublishing] = React.useState(false);
  const [published, setPublished] = React.useState(false);
  const [error, setError] = React.useState('');
  const [reachOpen, setReachOpen] = React.useState(false);

  const openReach = () => {
    setReachOpen(!reachOpen);
    if (others === null) {
      API.docker.list().then((res) => setOthers(((res && res.data) || []).map((x) => String((x.Names || [''])[0]).replace(/^\//, '')).filter((n) => n && n !== name).sort()))
        .catch(() => setOthers([]));
    }
  };

  const link = async () => {
    setLinking(true); setError(''); setLinked('');
    try {
      for (const app of picked) {
        const network = linkNetworkName(name, app, randomString(3));
        await API.docker.createNetwork({ name: network, driver: 'bridge', attachCosmos: false });
        await API.docker.attachNetwork(name, network);
        await API.docker.attachNetwork(app, network);
      }
      setLinked(picked.join(', '));
      setPicked([]);
      refresh && refresh();
    } catch (e) {
      setError(e.message || String(e));
    }
    setLinking(false);
  };

  const publish = async () => {
    setPublishing(true); setError('');
    try {
      const inspected = await API.docker.get(name);
      const info = (inspected && inspected.data) || {};
      await API.docker.updateContainer(name, {
        portBindings: withPublishedPort(info, c.port),
        networkMode: (info.HostConfig && info.HostConfig.NetworkMode) || '',
      });
      setPublished(true);
      refresh && refresh();
    } catch (e) {
      setError(e.message || String(e));
    }
    setPublishing(false);
  };

  return <Stack spacing={1.5} sx={{ minWidth: 0 }} data-service-info={name}>
    <Alert severity="info" sx={wrap} data-service-kind={c.why}>
      {c.name
        ? t('mgmt.services.what', '{{software}} speaks its own protocol on port {{port}}, not web: it has no page to open in a browser.', { software: c.name, port: c.port, interpolation: { escapeValue: false } })
        : t('mgmt.services.whatUnknown', 'Port {{port}} of {{name}} is not web: it has no page to open in a browser.', { name, port: c.port })}
      {c.webPort ? <><br />{t('mgmt.services.webToo', 'Its own web page is on port {{port}}: a web address can point there.', { port: c.webPort })}</> : null}
    </Alert>
    <Stack spacing={0.5} sx={{ minWidth: 0 }}>
      <Typography variant="body2">{t('mgmt.services.address', 'Other apps on this server reach it at:')}</Typography>
      <Stack direction="row" spacing={1} sx={{ alignItems: 'center', minWidth: 0 }}>
        <Box component="code" data-service-address sx={{ overflowWrap: 'anywhere', minWidth: 0, fontSize: 14 }}>{address}</Box>
        <CopyButton value={address} />
      </Stack>
      {connection && <Stack direction="row" spacing={1} sx={{ alignItems: 'center', minWidth: 0 }}>
        <Box component="code" data-service-connection sx={{ overflowWrap: 'anywhere', minWidth: 0, fontSize: 12 }}>{connection}</Box>
        <CopyButton value={connection} />
      </Stack>}
      <Typography variant="caption" sx={{ color: 'text.secondary' }}>
        {t('mgmt.services.addressHint', 'This name works for the apps that share a network with it. The user and password are the ones set when it was created: Cosmos does not show a password again.')}
      </Typography>
    </Stack>

    {!compact && <>
      <Button size="small" variant="outlined" sx={{ alignSelf: 'flex-start' }} onClick={openReach} data-service-reach-door>
        {t('mgmt.servapps.anyImage.ui.reachTitle', 'Let these apps reach it')}
      </Button>
      {reachOpen && <Stack spacing={1} sx={{ minWidth: 0 }} data-service-reach>
        <Typography variant="caption" sx={{ color: 'text.secondary' }}>{t('mgmt.services.reachHint', 'Each app you pick gets a network of its own with {{name}}, and reaches it at {{address}}.', { name, address })}</Typography>
        {others === null && <Typography variant="body2">…</Typography>}
        {others && others.length === 0 && <Typography variant="body2">{t('mgmt.services.noOthers', 'There is no other app on this server yet.')}</Typography>}
        {others && others.length > 0 && <Stack direction="row" useFlexGap sx={{ flexWrap: 'wrap' }} spacing={1}>
          {others.map((n) => {
            const on = picked.includes(n);
            return <Chip key={n} label={n} clickable disabled={linking} color={on ? 'primary' : 'default'} variant={on ? 'filled' : 'outlined'}
              onClick={() => setPicked(on ? picked.filter((x) => x !== n) : [...picked, n])} sx={{ maxWidth: '100%' }} />;
          })}
        </Stack>}
        <Button size="small" variant="contained" sx={{ alignSelf: 'flex-start' }} disabled={linking || !picked.length} onClick={link} data-service-link>
          {t('mgmt.services.link', 'Link them')}
        </Button>
        {linked && <Alert severity="success" sx={wrap} data-service-linked>{t('mgmt.services.linked', '{{apps}} can now reach it at {{address}}.', { apps: linked, address })}</Alert>}
      </Stack>}

      <Button size="small" variant="outlined" sx={{ alignSelf: 'flex-start' }} onClick={() => setLan(!lan)} data-service-lan-door>
        {t('mgmt.services.lan', 'Reach it from my home network')}
      </Button>
      {lan && <Stack spacing={1} sx={{ minWidth: 0 }} data-service-lan>
        <Alert severity="warning" sx={wrap}>
          {t('mgmt.services.lanRisk', 'Port {{port}} of this server then leads to {{software}} for every device on your home network, and only its password protects it. Cosmos does not open it on your router; forwarding this port on the router would put it on the internet, which is not safe for a database.', { port: c.port, software: c.name || name, interpolation: { escapeValue: false } })}
        </Alert>
        <FormControlLabel control={<Checkbox checked={understood} disabled={publishing || published} onChange={(e) => setUnderstood(e.target.checked)} data-service-lan-tick />}
          label={t('mgmt.services.lanTick', 'I understand: publish port {{port}} on this server', { port: c.port })} />
        <Button size="small" variant="contained" sx={{ alignSelf: 'flex-start' }} disabled={!understood || publishing || published} onClick={publish} data-service-publish>
          {t('mgmt.services.publish', 'Publish port {{port}}', { port: c.port })}
        </Button>
        {published && <Alert severity="success" sx={wrap} data-service-published>{t('mgmt.services.published', 'Done: devices on your home network reach it at this server\'s address, port {{port}}. It was created again for this.', { port: c.port })}</Alert>}
        <Typography variant="caption" sx={{ color: 'text.secondary' }}>
          {t('mgmt.services.tcpRoute', 'Cosmos can also forward a port itself (a URL whose address carries a port and whose target has no http://), with its shield in front. For a database on the home network, publishing the port is the simpler way.')}
        </Typography>
      </Stack>}
    </>}

    {error && <Alert severity="error" sx={wrap}>{error}</Alert>}
    {onInsist && <Button size="small" sx={{ alignSelf: 'flex-start' }} onClick={onInsist} data-service-insist>
      {insistLabel || (c.webPort
        ? t('mgmt.services.insistWeb', 'A web address for its web page (port {{port}})', { port: c.webPort })
        : t('mgmt.services.insist', 'Make a web address anyway'))}
    </Button>}
  </Stack>;
};

// The service classification of a container as Docker lists or inspects it.
export { classifyService } from '../utils/services';

// On a ServApps tile: what the app is and the address other apps use, in
// place of a web address it does not have.
export const ServiceChip = ({ container }) => {
  const { t } = useTranslation();
  if (!container) return null;
  const c = classifyServiceOf(container);
  if (!c || !c.service || !c.port) return null;
  const name = String((container.Names || [''])[0]).replace(/^\//, '');
  const address = serviceAddress(name, c.port);
  return <Tooltip title={t('mgmt.services.chipHint', 'No web page: other apps on this server reach it at {{address}}', { address })}>
    <Chip size="small" variant="outlined" label={(c.name ? c.name + ' · ' : '') + address} data-service-chip={name}
      sx={{ maxWidth: '100%', '& .MuiChip-label': { overflow: 'hidden', textOverflow: 'ellipsis' } }} />
  </Tooltip>;
};

// On the app's page (overview): why it does not run, and what it is when it
// is a service without a web page and has no URL.
export const OverviewServiceNotes = ({ containerInfo, hasRoutes, refresh }) => {
  if (!containerInfo || !containerInfo.Config) return null;
  const name = String(containerInfo.Name || '').replace(/^\//, '');
  const state = containerInfo.State || {};
  // a container that restarts is "running" for a moment each time: a restart in the last two minutes counts too
  const recentRestart = (containerInfo.RestartCount || 0) > 0 && Date.now() - Date.parse(state.StartedAt || 0) < 120000;
  const failing = state.Restarting || recentRestart || (!state.Running && state.Status === 'exited' && state.ExitCode !== 0);
  const c = classifyServiceOf0({ image: containerInfo.Config.Image, ports: containerPorts(containerInfo) });
  return <Stack spacing={1} sx={{ minWidth: 0, my: 1 }} data-overview-service>
    {failing && <Box><StartProblemButton containerName={name} refresh={refresh} /></Box>}
    {!hasRoutes && c.service && c.port && <ServiceInfo containerName={name} classification={c} refresh={refresh} />}
  </Stack>;
};

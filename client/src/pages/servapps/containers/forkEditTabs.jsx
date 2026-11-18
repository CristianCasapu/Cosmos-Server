// Fork addition (plan 47, I2c): the proposals of a container's edit tabs
// (Docker, Network, Storage). The rules are in utils/editHints.js, the facts
// come from GET /api/servapps/{id}/edit-hints (src/fork_edit_hints.go); the
// upstream tabs only hook these pieces in, for an existing container (the
// new-container form keeps its own review). Every proposal is offered beside
// the field and never replaces what is there; every choice has "Type it
// myself"; the server checks the same again (src/docker/fork_edit_check.go).
import * as React from 'react';
import {
  Alert, Autocomplete, Button, Chip, FormControlLabel, FormHelperText, Grid, IconButton, Stack, Switch, TextField, Tooltip, Typography,
} from '@mui/material';
import { DeleteOutlined, PlusCircleOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import * as API from '../../../api';
import ChoiceChips from '../../../components/choiceChips';
import { ChoiceRow, ProposalNote } from '../../../components/formHelpers';
import ProposalBox from '../../../components/proposalBox';
import { FormField } from '../../../components/formField';
import ResponsiveButton from '../../../components/responseiveButton';
import { isHostIp } from './forkContainerFixes';
import {
  hostIpChoice, hostIpOf, reachOf, REACH_TEXT, containerPortOptions, proposeHostPort, proposedHostIp, newPortRow,
  dnsOptions, dnsChoice, dnsProblem, networkModeChoice, networkModeNetworks, declaredMissing, proposeDeclared,
  sourcePlaces, folderFinding, vpnOf, userOptions, userChoice, puidMismatch, withPuid, userOnLinuxserver,
  memoryOptions, cpuOptions, limitChoice, RESTART, envSuggestions, deviceOptions, deviceRow, sizeText,
} from '../../../utils/editHints';

const K = 'mgmt.servapps.editTabs.';
const wrapText = { overflowWrap: 'anywhere', minWidth: 0 };

// ---- the facts ------------------------------------------------------------------

// What the server tells about the container, read again when it changes.
// null while loading, false when it could not be read (the tabs then work as before).
export const useEditHints = (containerInfo, enabled = true) => {
  const [hints, setHints] = React.useState(null);
  const name = containerInfo && containerInfo.Name ? containerInfo.Name.replace('/', '') : '';
  const version = containerInfo ? `${containerInfo.Id}|${containerInfo.State && containerInfo.State.StartedAt}|${(containerInfo.Mounts || []).length}` : '';
  React.useEffect(() => {
    if (!enabled || !name) return undefined;
    let live = true;
    API.docker.editHints(name).then((res) => { if (live) setHints(res.data || false); }).catch(() => { if (live) setHints(false); });
    return () => { live = false; };
  }, [name, version, enabled]);
  return hints;
};

// ---- Network: one published port ---------------------------------------------------

const PROTOCOLS = [{ value: 'tcp', label: 'TCP' }, { value: 'udp', label: 'UDP' }];

const sourceText = (t, proposed) => {
  if (!proposed) return '';
  const from = {
    recipe: t(K + 'port.fromRecipe', 'Cosmos\' recipe of {{name}} names this port', { name: proposed.name || '' }),
    known: t(K + 'port.fromKnown', '{{name}} serves on this port', { name: proposed.name || '' }),
    image: t(K + 'port.fromImage', 'the image declares this port'),
  }[proposed.source] || '';
  const host = {
    free: t(K + 'port.hostFreeSockets', 'the same number, free on the server (no program listens on it; a port another program forwards with a firewall rule alone is not seen)'),
    same: t(K + 'port.hostSame', 'the same number on the server (no other app has it)'),
    moved: t(K + 'port.hostMoved', 'the same number is taken on the server: the next free one'),
  }[proposed.reason] || '';
  return t(K + 'port.proposed', 'Proposed: {{from}}; host port: {{host}}.', { from, host });
};

export const ForkPortRow = ({ port, idx, ports, onChange, onDelete, hints, holders, padding, children }) => {
  const { t } = useTranslation();
  const lan = (hints && hints.lan) || [];
  const set = (fields) => onChange(idx, { ...port, ...fields });
  const choice = port.hostIpOther ? 'other' : hostIpChoice(port.hostIp, lan);
  const ipOk = isHostIp(port.hostIp);
  const reach = reachOf(port.hostIp, port.hostPort || (port.random ? '*' : ''), lan); // a stored binding without a host port: Docker picks one
  const others = containerPortOptions(hints).filter((o) => String(o.port) !== String(port.port) || o.protocol !== port.protocol);
  const addressOptions = [
    ...lan.map((a) => ({ value: 'lan:' + a.address, label: t(K + 'address.lan', 'Home network ({{ip}})', { ip: a.address }), description: t(K + 'address.lanHint', 'Only this address of the server ({{name}}), which devices of your home network use.', { name: a.interface }) })),
    { value: 'all', label: t(K + 'address.all', 'Every address of the server'), description: t(K + 'address.allHint', 'Every address the server has: the home network, a VPN, and its public address when it has one. Docker opens the port past the server\'s own firewall.') },
    { value: 'local', label: t(K + 'address.local', 'This server only (127.0.0.1)'), description: t(K + 'address.localHint', 'Only programs on the server itself: for a port Cosmos\' URLs or another program on the server use.') },
    { value: 'other', label: t(K + 'address.other', 'Type it myself'), description: t(K + 'address.otherHint', 'One address of the server, IPv4 or IPv6.') },
  ];
  if (choice.startsWith('lan:') && !lan.some((a) => 'lan:' + a.address === choice)) addressOptions.unshift({ value: choice, label: choice.slice(4) });

  return <Grid container data-fork-port-row={idx} sx={{ borderTop: idx > 0 ? 1 : 0, borderColor: 'divider', pt: idx > 0 ? 1 : 0 }}>
    <Grid size={{ xs: 6, md: 3 }} style={{ padding }}>
      <TextField
        fullWidth
        label={t('mgmt.servApps.networks.containerPortInput.containerPortLabel')}
        value={port.port}
        slotProps={{ htmlInput: { inputMode: 'numeric', 'data-fork-container-port': idx } }}
        onChange={(e) => set({ port: e.target.value.trim(), proposed: null })}
      />
    </Grid>
    <Grid size={{ xs: 6, md: 3 }} style={{ padding }}>
      <TextField
        fullWidth
        label={t(K + 'hostPort', 'Host port')}
        value={'' + (port.hostPort || '')}
        slotProps={{ htmlInput: { inputMode: 'numeric', 'data-fork-host-port': idx } }}
        placeholder={port.random ? t(K + 'randomPort', 'Docker picks one') : undefined}
        onChange={(e) => set({ hostPort: e.target.value.trim(), proposed: null, random: false })}
      />
    </Grid>
    <Grid size={{ xs: 9, md: 4 }} style={{ padding }}>
      <ChoiceChips
        id={`fork-port-protocol-${idx}`}
        label={t(K + 'protocol', 'Protocol')}
        value={port.protocol === 'udp' ? 'udp' : 'tcp'}
        options={PROTOCOLS}
        onChange={(value) => set({ protocol: value })}
      />
    </Grid>
    <Grid size={{ xs: 3, md: 2 }} style={{ padding, display: 'flex', alignItems: 'center', justifyContent: 'flex-end' }}>
      <Tooltip title={t('global.delete', 'Delete')}><IconButton color="error" size="large" aria-label={t('global.delete', 'Delete')} onClick={() => onDelete(idx)}>
        <DeleteOutlined />
      </IconButton></Tooltip>
    </Grid>
    {(port.proposed || (others.length > 0 && !String(port.port || '').trim())) && <Grid size={{ xs: 12 }} style={{ padding, paddingTop: 0 }}>
      <ProposalNote>{sourceText(t, port.proposed)}</ProposalNote>
      {!String(port.port || '').trim() && <Stack direction="row" useFlexGap spacing={1} sx={{ flexWrap: 'wrap', mt: 0.5 }}>
        {others.map((o) => <Chip key={`${o.port}/${o.protocol}`} variant="outlined" clickable sx={{ minHeight: 44 }}
          label={`${o.port}${o.protocol === 'udp' ? '/udp' : ''}`}
          onClick={() => {
            const host = proposeHostPort(hints, o.port, o.protocol, ports.filter((_, i) => i !== idx), holders);
            set({ port: String(o.port), protocol: o.protocol, hostPort: port.hostPort || (host ? String(host.port) : ''), proposed: { source: o.source, name: o.name, reason: host && host.reason } });
          }} />)}
      </Stack>}
    </Grid>}
    <Grid size={{ xs: 12 }} style={{ padding, paddingTop: 0 }}>
      <ChoiceChips
        id={`fork-port-address-${idx}`}
        label={t(K + 'address.label', 'Who can reach it: the address it is published on')}
        value={choice}
        options={addressOptions}
        onChange={(value) => set(value === 'other' ? { hostIpOther: true } : { hostIp: hostIpOf(value), hostIpOther: false })}
      />
      {choice === 'other' && <Stack sx={{ mt: 1 }}>
        <FormField
          id={`fork-port-ip-${idx}`}
          label={t('mgmt.servApps.networks.hostIpInput.hostIpLabel', 'Host IP (optional)')}
          value={port.hostIp || ''}
          placeholder="192.168.1.10"
          error={!ipOk && t('mgmt.servApps.networks.hostIpInput.hostIpValidation', 'Enter an IPv4 or IPv6 address, or leave it empty for every address')}
          onChange={(text) => set({ hostIp: text.trim(), hostIpOther: true })}
        />
      </Stack>}
      {ipOk && <FormHelperText data-fork-reach={reach} sx={wrapText}>
        {t(K + 'reach.' + reach, REACH_TEXT[reach], { ip: port.hostIp, port: port.hostPort })}
      </FormHelperText>}
      {ipOk && vpnOf(port.hostIp, hints) && <FormHelperText data-fork-vpn sx={wrapText}>
        {t(K + 'reach.vpn', 'This address belongs to the VPN interface {{name}}: the port works only while the VPN is up (and the app does not start while it is down).', { name: vpnOf(port.hostIp, hints).interface })}
      </FormHelperText>}
      {reach === 'none' && String(port.port || '').trim() !== '' && (() => {
        // a port the container declares but does not publish: proposed, on the home network and a free host port
        const host = proposeHostPort(hints, port.port, port.protocol, ports.filter((_, i) => i !== idx), holders);
        if (!host) return null;
        const at = proposedHostIp(lan);
        return <Button size="small" variant="outlined" sx={{ mt: 1, minHeight: 44 }} data-fork-publish
          onClick={() => set({ hostPort: String(host.port), hostIp: at, hostIpOther: false, proposed: { source: 'image', reason: host.reason } })}>
          {at ? t(K + 'publishHome', 'Publish it for the home network, on port {{port}}', { port: host.port }) : t(K + 'publishAll', 'Publish it on port {{port}}', { port: host.port })}
        </Button>;
      })()}
    </Grid>
    {children}
  </Grid>;
};

// The row the "+" button adds on the edit tab: proposed from the image, on the home network.
export const forkNewPortRow = (hints, ports, holders) => {
  const row = newPortRow(hints || null, ports, holders);
  return { ...row, proposed: row.proposed && row.proposed.source ? row.proposed : null };
};

// ---- Network: DNS ---------------------------------------------------------------------

export const ForkDnsChoice = ({ formik, hints }) => {
  const { t } = useTranslation();
  const options = dnsOptions(hints || {});
  const [other, setOther] = React.useState(false); // the door, opened by a click (a value no choice matches shows it too)
  const choice = other ? 'other' : dnsChoice(formik.values.dns, hints || {});
  const dns = (hints && hints.dns) || {};
  const problem = dnsProblem(formik.values.dns);
  const label = (o) => ({
    default: t(K + 'dns.default', 'As the server (Docker\'s default)'),
    router: t(K + 'dns.router', 'The home router ({{ip}})', { ip: (o.servers || [])[0] }),
    resolver: t(K + 'dns.resolver', 'The server\'s DNS ({{list}})', { list: (o.servers || []).join(', ') }),
    other: t(K + 'dns.other', 'Type it myself'),
  }[o.value]);
  const hint = (o) => ({
    default: dns.servers && dns.servers.length
      ? t(K + 'dns.defaultHint', 'Docker gives the app the DNS servers the server uses: {{list}} (from {{source}}). Usual.', { list: dns.servers.join(', '), source: dns.source })
      : t(K + 'dns.defaultNone', 'The server only names a DNS of its own (127.0.0.53), which apps cannot use: Docker then gives them public servers (8.8.8.8).'),
    router: t(K + 'dns.routerHint', 'The server\'s default gateway, your router: it usually answers DNS, and knows the names of your home network.'),
    resolver: t(K + 'dns.resolverHint', 'The DNS servers the server uses, named on the app: they stay even if the server\'s change.'),
    other: t(K + 'dns.otherHint', 'One or more IP addresses, separated by commas, for example a DNS app of your own (Pi-hole) at 192.168.1.5.'),
  }[o.value]);
  return <ChoiceRow
    name="fork-dns"
    label={t('mgmt.servApps.networks.dnsLabel')}
    value={choice}
    options={options.map((o) => ({ value: o.value, label: label(o), hint: hint(o) }))}
    onChange={(value) => {
      setOther(value === 'other');
      if (value === 'other') return;
      const found = options.find((o) => o.value === value);
      formik.setFieldValue('dns', (found.servers || []).join(', '));
    }}
  >
    {choice === 'other' && <FormField id="fork-dns-other" value={formik.values.dns} placeholder="192.168.1.5, 1.1.1.1"
      onChange={(text) => formik.setFieldValue('dns', text)}
      error={problem && t(K + 'dns.problem.' + problem, problem === 'tooMany' ? 'At most 8 DNS servers.' : 'Enter IP addresses separated by commas, for example 192.168.1.1, 1.1.1.1')} />}
  </ChoiceRow>;
};

// ---- Network: the network mode ---------------------------------------------------------

export const ForkNetworkMode = ({ formik, networks, picker }) => {
  const { t } = useTranslation();
  const mode = formik.values.networkMode || '';
  const [other, setOther] = React.useState(false);
  const [vpn, setVpn] = React.useState(false);
  const found = networkModeChoice(mode, networks);
  const choice = other ? 'other' : (vpn && found !== 'host' && found !== 'none' && !found.startsWith('net:') ? 'container' : found);
  const names = networkModeNetworks(networks);
  const options = [
    { value: 'default', label: t(K + 'mode.default', 'Its own network (default)'), hint: t(K + 'mode.defaultHint', 'Docker\'s usual network: the app has its own address, publishes only the ports listed below, and reaches the apps it shares a network with (see Networks below).') },
    ...names.map((n) => ({ value: 'net:' + n, label: n, hint: t(K + 'mode.netHint', 'The app joins the network {{name}} as its main network: the apps on it reach it by its name.', { name: n }) })),
    { value: 'container', label: t(K + 'mode.container', 'Another app\'s network'), hint: t(K + 'mode.containerHint', 'Uses the network of another app (a VPN client such as Gluetun): all its traffic goes through that app, and ports are published on that app instead.') },
    { value: 'host', label: t(K + 'mode.host', 'The server\'s own network (sensitive)'), hint: t(K + 'mode.hostHint', 'No network of its own: every port the app opens is open on the server, and it sees the server\'s network. Saving asks you to confirm it, tick by tick.') },
    { value: 'none', label: t(K + 'mode.none', 'No network'), hint: t(K + 'mode.noneHint', 'The app cannot reach anything, and nothing reaches it.') },
    { value: 'other', label: t(K + 'mode.other', 'Type it myself'), hint: t(K + 'mode.otherHint', 'bridge, host, none, a network\'s name, or container:<name>.') },
  ];
  return <ChoiceRow
    name="fork-network-mode"
    label={t('mgmt.servApps.networks.modeInput.modeLabel')}
    value={choice}
    options={options}
    onChange={(value) => {
      setOther(value === 'other');
      setVpn(value === 'container');
      if (value === 'other' || value === 'container') return;
      const next = value === 'default' ? 'bridge' : value.startsWith('net:') ? value.slice(4) : value;
      formik.setValues({ ...formik.values, networkMode: next, Container: '' });
    }}
  >
    {choice === 'container' && picker}
    {choice === 'other' && <FormField id="fork-network-mode-other" value={mode} placeholder="bridge"
      onChange={(text) => formik.setValues({ ...formik.values, networkMode: text.trim(), Container: '' })} />}
  </ChoiceRow>;
};

export const ForkNetworksHint = () => {
  const { t } = useTranslation();
  return <Typography variant="body2" sx={{ color: 'text.secondary', ...wrapText }}>
    {t(K + 'networksHint', '{{title}}: connect it to a network another app is on (or use "Link containers"), and they reach each other by name. Apps on no common network cannot reach each other.', { title: t('mgmt.servapps.anyImage.ui.reachTitle', 'Let these apps reach it') })}
  </Typography>;
};

// ---- Storage ---------------------------------------------------------------------------

const placeText = (t, place) => (place
  ? t(K + 'place.on', 'on {{disk}}, {{free}} free', { disk: place.disk || place.path, free: sizeText(place.free) })
  : '');

const reasonText = (t, reason) => ({
  bulk: t(K + 'reason.bulk', 'Large data: on the data disk with the most free space.'),
  config: t(K + 'reason.config', 'Settings and databases: in the data path (Configuration > Docker).'),
  configDisk: t(K + 'reason.configDisk', 'The data path is on the disk the system runs from: on a data disk instead.'),
  volume: t(K + 'reason.volume', 'No data path or data disk is known: a Docker volume, which Docker keeps when the container is made again.'),
}[reason] || '');

// For each folder the image keeps data in and the container does not mount: a proposal.
export const ForkDeclaredVolumes = ({ hints, volumes, app, onAdd }) => {
  const { t } = useTranslation();
  const missing = declaredMissing(hints, volumes);
  if (!missing.length) return null;
  return <Stack spacing={1} data-fork-declared>
    {missing.map((m) => {
      const proposal = proposeDeclared(m.target, hints, app);
      return <ProposalBox
        key={m.target}
        severity="warning"
        title={t(K + 'declared.title', 'This app keeps data in {{target}}: it is not kept anywhere now', { target: m.target })}
        status={m.anonymous
          ? t(K + 'declared.anonymous', 'Docker keeps it in a volume of its own, without a name: it is lost when the container is made again (every edit and every update of the image does that).')
          : t(K + 'declared.lost', 'It is lost when the container is made again (every edit and every update of the image does that).')}
        source={proposal.type === 'bind'
          ? t(K + 'declared.proposalBind', 'Proposed: the folder {{source}} {{place}}. {{reason}}', { source: proposal.source, place: placeText(t, proposal.place), reason: reasonText(t, proposal.reason) })
          : t(K + 'declared.proposalVolume', 'Proposed: the Docker volume {{source}}. {{reason}}', { source: proposal.source, reason: reasonText(t, proposal.reason) })}
        heldText={m.anonymous ? t(K + 'declared.notCopied', 'What it holds now is not copied: copy it first if it matters.') : ''}
        onUse={() => onAdd({ Type: proposal.type, Source: proposal.source, Target: m.target, ReadOnly: false, forkNew: true })}
        useLabel={t(K + 'declared.use', 'Keep it there')}
      />;
    })}
  </Stack>;
};

const findingText = (t, finding) => {
  const p = finding.params;
  return {
    writable: t(K + 'folder.writable', 'Owned by {{owner}} ({{mode}}): the app ({{user}}) can write in it.', p),
    unknownUser: t(K + 'folder.unknownUser', 'Owned by {{owner}} ({{mode}}).', p),
    notWritable: t(K + 'folder.notWritable', 'Owned by {{owner}} ({{mode}}): the app runs as {{user}} and probably cannot write in it.', p),
    missing: t(K + 'folder.missing', 'This folder does not exist yet: it is made when you save.'),
    file: t(K + 'folder.file', 'This is a file, not a folder.'),
    link: p.target
      ? t(K + 'folder.linkTo', 'This path is a link to {{target}}, out of the data disk: not looked at. A new folder given this way is refused when you save.', p)
      : t(K + 'folder.link', 'A link in this path leads out of the data disk: not looked at.'),
    linkInside: t(K + 'folder.linkInside', 'This path is a link to {{target}}: Docker mounts that folder.', p),
    slow: t(K + 'folder.slow', 'The disk did not answer in time: nothing is known about this folder.'),
    outside: '',
    invalid: t(K + 'folder.invalid', 'Give an absolute path, such as /mnt/data/app, without "..".'),
    unknown: '',
  }[finding.code] || '';
};

// Under a bind's source: its disk, free space, owner and whether the app can write in it.
export const ForkFolderNote = ({ row, hints }) => {
  const { t } = useTranslation();
  const [asked, setAsked] = React.useState(null);
  const known = hints && row && (hints.folders || []).some((f) => f.source === row.Source && f.target === row.Target);
  const source = row && row.Type === 'bind' ? String(row.Source || '') : '';
  React.useEffect(() => {
    if (!hints || known || !source.startsWith('/') || !hints.container) return undefined;
    let live = true;
    const timer = setTimeout(() => {
      API.docker.editHints(hints.container, [source])
        .then((res) => { if (live) setAsked({ source, folder: res.data && res.data.folders && res.data.folders[0] }); })
        .catch(() => {});
    }, 600);
    return () => { live = false; clearTimeout(timer); };
  }, [source, known, hints && hints.container]);
  if (!hints || !row || row.Type !== 'bind' || !row.Source) return null;
  const folder = (hints.folders || []).find((f) => f.source === row.Source && f.target === row.Target);
  if (folder) {
    const finding = folderFinding(folder, hints.runsAs);
    const text = finding && findingText(t, finding);
    const place = folder.total ? t(K + 'folder.space', '{{free}} free of {{total}} on {{place}}.', { free: sizeText(folder.free), total: sizeText(folder.total), place: folder.place }) : '';
    if (!text && !place) return null;
    return <FormHelperText data-fork-folder={finding && finding.code} sx={{ ...wrapText, whiteSpace: 'normal', color: finding && finding.severity === 'warning' ? 'text.primary' : undefined, fontWeight: finding && finding.severity === 'warning' ? 600 : undefined }}>
      {[text, place].filter(Boolean).join(' ')}
    </FormHelperText>;
  }
  // a row the form added: what the server knows of that path (asked after typing stops), and where it would go
  const askedFolder = asked && asked.source === row.Source ? asked.folder : null;
  if (askedFolder && (askedFolder.state === 'link' || askedFolder.state === 'slow' || askedFolder.linkTo)) {
    const finding = folderFinding(askedFolder, hints.runsAs);
    return <FormHelperText data-fork-folder={finding.code} sx={{ ...wrapText, whiteSpace: 'normal', color: 'text.primary', fontWeight: 600 }}>{findingText(t, finding)}</FormHelperText>;
  }
  const place = ((hints.places || []).filter((p) => p.kind !== 'system').sort((a, b) => b.path.length - a.path.length))
    .find((p) => row.Source === p.path || row.Source.startsWith(p.path.replace(/\/$/, '') + '/'));
  if (!place) return null;
  return <FormHelperText data-fork-folder="new" sx={{ ...wrapText, whiteSpace: 'normal' }}>
    {hints.usernsUnknown
      ? t(K + 'folder.newUnknown', 'On {{disk}}, {{free}} free. A folder that does not exist is made when you save, owned by root: Docker did not say whether it runs apps with users of their own, so it is not given to the app\'s user.', { disk: place.disk || place.path, free: sizeText(place.free) })
      : hints.usernsRemap
      ? t(K + 'folder.newRemap', 'On {{disk}}, {{free}} free. A folder that does not exist is made when you save, owned by root: Docker runs apps with users of their own on this server (userns-remap), so it cannot be given to the app\'s user.', { disk: place.disk || place.path, free: sizeText(place.free) })
      : t(K + 'folder.new', 'On {{disk}}, {{free}} free. A folder that does not exist is made when you save, owned by the app\'s user ({{user}}).', { disk: place.disk || place.path, free: sizeText(place.free), user: (hints.runsAs && hints.runsAs.text) || 'root' })}
  </FormHelperText>;
};

// Under a new bind's source: the data places as a choice (the field stays the door).
export const ForkSourcePlaces = ({ row, hints, app, onPick }) => {
  const { t } = useTranslation();
  const places = sourcePlaces(hints, row.Target, app);
  if (!places.length) return null;
  return <Stack direction="row" useFlexGap spacing={1} sx={{ flexWrap: 'wrap', mt: 0.5 }} data-fork-source-places>
    {places.map((p) => <Chip key={p.path} variant={row.Source === p.source ? 'filled' : 'outlined'} color={row.Source === p.source ? 'primary' : 'default'}
      clickable sx={{ minHeight: 44, maxWidth: '100%', '& .MuiChip-label': { whiteSpace: 'normal' } }}
      label={(p.kind === 'data-path' ? t(K + 'source.dataPath', 'Data path') : p.path) + ' · ' + sizeText(p.free)}
      onClick={() => onPick(p.source)} />)}
  </Stack>;
};

// The source of a named volume: the volumes Docker has, or a new name typed.
export const ForkVolumeSource = ({ value, volumes, onChange, disabled }) => {
  const { t } = useTranslation();
  const names = (volumes || []).map((v) => v.Name).filter(Boolean);
  return <Autocomplete
    freeSolo
    disabled={disabled}
    options={names}
    value={value || ''}
    onChange={(_, v) => onChange(v || '')}
    onInputChange={(_, v, reason) => { if (reason === 'input') onChange(v); }}
    sx={{ minWidth: 200 }}
    renderInput={(params) => <TextField {...params} className="px-2 my-2" label={t('global.source')} helperText={t(K + 'volumeSourceHint', 'An existing volume, or a new name.')} />}
  />;
};

export const ForkReadOnly = ({ checked, onChange, disabled }) => {
  const { t } = useTranslation();
  return <FormControlLabel
    sx={{ minHeight: 44, mx: 0 }}
    control={<Switch checked={!!checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />}
    label={t(K + 'readOnly', 'Read-only')}
  />;
};

// ---- Docker tab ----------------------------------------------------------------------------

export const ForkRestartChoice = ({ formik }) => {
  const { t } = useTranslation();
  const value = formik.values.restartPolicy || 'no';
  return <ChoiceRow
    name="fork-restart"
    label={t('mgmt.servapps.newContainer.restartPolicyInput.restartPolicyLabel')}
    value={value}
    options={RESTART.map((r) => ({ value: r.value, label: t(K + 'restart.' + r.value, r.label), hint: t(K + 'restart.' + r.value + 'Hint', r.hint) }))}
    onChange={(v) => formik.setFieldValue('restartPolicy', v)}
  />;
};

export const ForkUserChoice = ({ formik, hints, problem }) => {
  const { t } = useTranslation();
  const user = formik.values.user || '';
  const options = userOptions(hints || {});
  const [other, setOther] = React.useState(false);
  const choice = other ? 'other' : userChoice(user, hints || {});
  const imageUser = (hints && hints.image && hints.image.user) || '';
  const label = (o) => ({
    image: imageUser ? t(K + 'user.image', 'As the image says ({{user}})', { user: imageUser }) : t(K + 'user.imageRoot', 'As the image says (root)'),
    owner: t(K + 'user.owner', 'The owner of its data folders ({{user}})', { user: o.user }),
    other: t(K + 'user.other', 'Type it myself'),
  }[o.value]);
  const hint = (o) => ({
    image: hints && hints.linuxserver
      ? t(K + 'user.imageLs', 'This image starts as root and runs the app as PUID/PGID (its variables): leave the user to the image.')
      : t(K + 'user.imageHint', 'The user the image is made to run as.'),
    owner: t(K + 'user.ownerHint', 'Runs the app as the user that owns its data folders, so that it can write in them.'),
    other: t(K + 'user.otherHint', 'A user name or number, or user:group, for example 1000:1000.'),
  }[o.value]);
  return <ChoiceRow
    name="fork-user"
    label={t('global.user')}
    value={choice}
    options={options.map((o) => ({ value: o.value, label: label(o), hint: hint(o) }))}
    onChange={(value) => {
      setOther(value === 'other');
      if (value === 'other') return;
      formik.setFieldValue('user', options.find((o) => o.value === value).user || '');
    }}
  >
    {choice === 'other' && <FormField id="fork-user-other" value={user} placeholder="1000:1000" onChange={(text) => formik.setFieldValue('user', text)} error={problem} />}
    {userOnLinuxserver(hints, user) && <Alert severity="warning" sx={wrapText} data-fork-user-ls>
      {t(K + 'user.lsWarning', 'This is a linuxserver.io-style image: with a user set it cannot do its start-up work and often does not start. Leave the user to the image and set PUID and PGID instead.')}
    </Alert>}
  </ChoiceRow>;
};

// A linuxserver.io-style image writing as another user than the owner of its data folders.
export const ForkPuidFix = ({ formik, hints }) => {
  const { t } = useTranslation();
  const fix = puidMismatch(hints, formik.values.envVars);
  if (!fix) return null;
  return <Grid size={{ xs: 12 }} data-fork-puid>
    <ProposalBox
      severity="warning"
      title={t(K + 'puid.title', 'The app writes as {{now}}, its data folders belong to {{owner}}', fix)}
      source={fix.defaulted
        ? t(K + 'puid.defaulted', 'This linuxserver.io-style image runs the app as PUID and PGID, 911 when they are not set; its data folders on the server belong to {{owner}}. It may not be able to write in them.', fix)
        : t(K + 'puid.source', 'This linuxserver.io-style image runs the app as PUID and PGID; its data folders on the server belong to {{owner}}. It may not be able to write in them.', fix)}
      onUse={() => formik.setFieldValue('envVars', withPuid(formik.values.envVars, fix))}
      useLabel={t(K + 'puid.use', 'Set PUID={{puid}} and PGID={{pgid}}', fix)}
    />
  </Grid>;
};

const deviceKind = (t, d) => ({
  render: t(K + 'device.render', 'graphics, for video transcoding'),
  serial: t(K + 'device.serial', 'USB serial dongle (Zigbee, Z-Wave, …)'),
  video: t(K + 'device.video', 'camera or capture card'),
}[d.kind] || d.kind);

export const ForkDeviceChoices = ({ formik, hints }) => {
  const { t } = useTranslation();
  const offered = deviceOptions(hints, formik.values.devices);
  if (!hints || !(hints.devices || []).length) return null;
  return <Stack spacing={1} sx={{ mb: 1 }} data-fork-devices>
    <Typography variant="body2" sx={wrapText}>
      {offered.length
        ? t(K + 'device.offered', 'Devices of this server an app can use. Adding one gives the app direct access to it: saving asks you to confirm it.')
        : t(K + 'device.allGiven', 'The app has every device of this server that apps usually need.')}
    </Typography>
    {offered.length > 0 && <Stack direction="row" useFlexGap spacing={1} sx={{ flexWrap: 'wrap' }}>
      {offered.map((d) => <Chip key={d.path} variant="outlined" clickable={!d.problem} disabled={!!d.problem} icon={<PlusCircleOutlined />}
        sx={{ minHeight: 44, maxWidth: '100%', height: 'auto', '& .MuiChip-label': { whiteSpace: 'normal', overflowWrap: 'anywhere', py: 0.5 } }}
        label={[d.name || d.node, deviceKind(t, d), d.name || d.path !== d.node ? d.path : '', d.problem ? t(K + 'device.badName', 'its name cannot be given to Docker (it holds ":" or ",")') : ''].filter(Boolean).join(' · ')}
        onClick={() => { if (!d.problem) formik.setFieldValue('devices', [...formik.values.devices, deviceRow(d)]); }} />)}
    </Stack>}
  </Stack>;
};

// A limit as chips (choices under the server's totals) with the field as the door.
export const ForkLimitChoice = ({ formik, name, label, options, unitLabel, error, placeholder }) => {
  const { t } = useTranslation();
  const value = formik.values[name];
  const [other, setOther] = React.useState(false);
  const choice = other ? 'other' : limitChoice(value, options);
  const chips = [
    ...options.filter((o) => o.kind !== 'none').map((o) => ({ value: 'v:' + o.value, label: unitLabel(o), description: o.kind === 'recipe' ? t(K + 'limit.recipe', 'What Cosmos\' recipe of {{name}} says it needs: {{hint}}.', { name: o.name, hint: o.hint }) : '' })),
    { value: 'none', label: t(K + 'limit.none', 'No limit'), description: t(K + 'limit.noneHint', 'The app may use what the server has.') },
    { value: 'other', label: t(K + 'limit.other', 'Type it myself'), description: placeholder },
  ];
  return <ChoiceRow name={'fork-limit-' + name} label={label} value={choice} options={chips.map((c) => ({ value: c.value, label: c.label, hint: c.description }))}
    error={choice !== 'other' ? error : undefined}
    onChange={(v) => {
      setOther(v === 'other');
      if (v === 'other') return;
      const none = options.find((o) => o.kind === 'none');
      formik.setFieldValue(name, v === 'none' ? (none ? none.value : '') : v.slice(2));
    }}
  >
    {choice === 'other' && <FormField id={'fork-limit-' + name + '-other'} value={value} placeholder={placeholder} onChange={(text) => formik.setFieldValue(name, text)} error={error} />}
  </ChoiceRow>;
};

export const ForkLimits = ({ formik, hints, errors }) => {
  const { t } = useTranslation();
  const server = (hints && hints.server) || {};
  return <>
    {server.memory > 0 && <Grid size={{ xs: 12 }}><FormHelperText sx={wrapText}>
      {t(K + 'limit.server', 'This server has {{memory}} of memory and {{cpus}} CPUs.', { memory: sizeText(server.memory), cpus: server.cpus })}
    </FormHelperText></Grid>}
    <ForkLimitChoice formik={formik} name="memLimit" label={t('mgmt.servapps.newContainer.memoryLimit')} options={memoryOptions(hints)}
      unitLabel={(o) => (o.kind === 'recipe' ? t(K + 'limit.recipeLabel', '{{value}} (recipe)', { value: o.value.toUpperCase() }) : o.value.toUpperCase())}
      placeholder={t(K + 'limit.memoryPlaceholder', 'A size such as 768m or 3g')} error={errors.memLimit} />
    <ForkLimitChoice formik={formik} name="cpus" label={t('mgmt.servapps.newContainer.cpuLimit')} options={cpuOptions(hints)}
      unitLabel={(o) => (o.kind === 'half' ? t(K + 'limit.half', '{{value}} (half of the server)', { value: o.value }) : t(K + 'limit.cpus', '{{value}} CPU', { value: o.value }))}
      placeholder={t(K + 'limit.cpuPlaceholder', 'A number such as 0.5 or 3')} error={errors.cpus} />
  </>;
};

// "Add" for the variables: the names the recipe and the image's description give, then an empty row.
export const ForkEnvAdd = ({ formik, hints, image }) => {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const [described, setDescribed] = React.useState(null);
  const suggestions = envSuggestions(hints, described, formik.values.envVars);
  const add = (key) => formik.setFieldValue('envVars', [...formik.values.envVars, { key, value: '' }]);
  return <Stack spacing={1} sx={{ alignItems: 'flex-start' }}>
    <ResponsiveButton variant="outlined" color="primary" size="large" startIcon={<PlusCircleOutlined />} onClick={() => {
      add('');
      if (!open) {
        setOpen(true);
        if (image && described === null) {
          setDescribed(false);
          API.docker.imageDescribe(image).then((res) => setDescribed((res && res.data && res.data.hints) || false)).catch(() => setDescribed(false));
        }
      }
    }}>
      {t('global.addAction')}
    </ResponsiveButton>
    {open && suggestions.length > 0 && <Stack spacing={0.5} data-fork-env-suggestions sx={{ minWidth: 0, maxWidth: '100%' }}>
      <FormHelperText sx={wrapText}>{t(K + 'env.offered', 'Names this image uses (from Cosmos\' recipe and the image\'s description): one click adds it.')}</FormHelperText>
      <Stack direction="row" useFlexGap spacing={1} sx={{ flexWrap: 'wrap' }}>
        {suggestions.map((s) => <Chip key={s.name} label={s.name} variant="outlined" clickable sx={{ minHeight: 44, maxWidth: '100%' }}
          onClick={() => {
            // fills the empty row just added, else adds one
            const rows = [...formik.values.envVars];
            const empty = rows.findIndex((r) => !r.key && !r.value);
            if (empty !== -1) rows[empty] = { key: s.name, value: '' };
            else rows.push({ key: s.name, value: '' });
            formik.setFieldValue('envVars', rows);
          }} />)}
      </Stack>
    </Stack>}
  </Stack>;
};


// Fork addition (plan 47, I2c, third review m-2): what an edit had to change by itself (a fixed
// address that no longer fits its network), said under the tab's button after a save.
export const ForkEditNotes = ({ notes }) => {
  const { t } = useTranslation();
  if (!notes || !notes.length) return null;
  return <Alert severity="info" sx={wrapText} data-fork-edit-notes>
    {t(K + 'notes', 'Saved. Cosmos had to change this by itself:')} {notes.join('; ')}.
  </Alert>;
};

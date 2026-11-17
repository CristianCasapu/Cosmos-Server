// Fork addition (plan 45): a web manager with a database. In "Install any
// image", a database gets "Also install a web manager for it" (on, the
// proposal first, a choice when several fit); on the page of a database that
// is already on the server, "Add a web manager". Either way the manager is
// created through the one review: on a network only it and its database
// share, already pointed at the database, its own sign-in from the
// configuration's defaults, an address in Cosmos for administrators only
// behind Cosmos' sign-in, never a published port. The logic is in
// utils/anyImage.js (managersFor, managerService, managerDefinition).
import * as React from 'react';
import {
  Alert, Button, Chip, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, Stack, Switch, TextField, Typography, useMediaQuery,
} from '@mui/material';
import { CopyOutlined, DatabaseOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import NewDockerService from '../pages/servapps/containers/newService';
import { localAddress } from './hostnameSuggestions';
import { browserRandom, managerDefinition, managersFor, parseReference, proposedSettings } from '../utils/anyImage';
import { networkSuffix } from '../utils/signIn';

const wrap = { overflowWrap: 'anywhere', minWidth: 0, '& .MuiAlert-message': { minWidth: 0 } };
const touchChip = { '&&&': { height: { xs: 'auto', sm: 32 }, minHeight: { xs: 44, sm: 0 } }, '& .MuiChip-label': { whiteSpace: 'normal' } };

const NOTES = {
  pgadmin: ['mgmt.servapps.manager.note.pgadmin', 'The database is already registered in it: no host to type. Sign in to pgAdmin with the e-mail and password below; the first time you open the database it may ask for the database\'s password once (its Sign-in details show it).'],
  phpmyadmin: ['mgmt.servapps.manager.note.phpmyadmin', 'It opens on this database: sign in with the database\'s own user and password (its Sign-in details show them).'],
  adminer: ['mgmt.servapps.manager.note.adminer', 'It opens on this database: sign in with the database\'s own user and password (its Sign-in details show them).'],
  redisinsight: ['mgmt.servapps.manager.note.redisinsight', 'The database is already registered in it; the first time you open it, it asks you to accept Redis\' terms, then lists it. It has no sign-in of its own: only administrators reach it, behind Cosmos\' sign-in.'],
  'mongo-express': ['mgmt.servapps.manager.note.mongoexpress', 'It opens on this database. Sign in with the username and password below.'],
};

// The manager's own sign-in (pgAdmin's e-mail and password), editable, a generated password with Copy.
const ManagerSettings = ({ settings, onChange, busy }) => {
  const { t } = useTranslation();
  return <Stack spacing={1} sx={{ minWidth: 0 }}>
    {settings.filter((s) => s.type === 'secret' || s.type === 'name').map((s) => <Stack key={s.name} direction="row" spacing={1} sx={{ alignItems: 'flex-start', minWidth: 0 }} data-manager-setting={s.name}>
      <TextField size="small" fullWidth label={s.name} value={s.value} disabled={busy}
        helperText={s.type === 'secret' ? t('mgmt.servapps.manager.secretHelp', 'Generated for you. Copy it now; it is also on the manager\'s Sign-in details.') : ''}
        onChange={(e) => onChange(settings.map((o) => (o.name === s.name ? { ...o, value: e.target.value } : o)))} />
      {s.type === 'secret' && <Button size="small" variant="outlined" startIcon={<CopyOutlined />} sx={{ flexShrink: 0, mt: 0.25 }}
        onClick={() => { try { navigator.clipboard.writeText(s.value); } catch (e) { /* on screen */ } }}>
        {t('mgmt.servapps.anyImage.ui.copy', 'Copy')}
      </Button>}
    </Stack>)}
  </Stack>;
};

// State of a manager choice: { on, recipe, desc, settings } → the manager of buildDefinition, or null.
export const useManager = ({ kind, managers, dbName, defaults, config }) => {
  const options = React.useMemo(() => managersFor(managers, kind), [managers, kind]);
  const [on, setOn] = React.useState(true);
  const [picked, setPicked] = React.useState(null);
  const [desc, setDesc] = React.useState(null);
  const [settings, setSettings] = React.useState([]);
  const pick = React.useCallback((recipe) => {
    setPicked(recipe);
    setDesc(null);
    setSettings(proposedSettings(recipe, null, browserRandom, defaults));
    API.docker.imageDescribe(recipe.images[0]).then((res) => setDesc(res.data || { problem: 'unreachable' })).catch(() => setDesc({ problem: 'unreachable' }));
  }, [defaults]);
  React.useEffect(() => {
    if (options.length && (!picked || !options.some((o) => o.id === picked.id))) pick(options[0]);
  }, [options.map((o) => o.id).join(' ')]);
  const name = picked ? `${dbName || 'db'}-${picked.id}` : '';
  const manager = on && picked && desc && desc.tag && !desc.problem
    ? { recipe: picked, ref: parseReference(desc.reference), tag: desc.tag, name, settings, host: config ? localAddress(name, config) : '' }
    : null;
  const waiting = on && !!picked && !(desc && (desc.tag || desc.problem));
  return { options, on, setOn, picked, pick, desc, settings, setSettings, manager, waiting, name };
};

// The part of "Install any image" for a database.
export const ManagerChoice = ({ state, busy, dbLabel }) => {
  const { t } = useTranslation();
  if (!state.options.length) return null;
  const note = state.picked && NOTES[state.picked.id];
  return <Stack spacing={1} sx={{ minWidth: 0 }} data-any-image-manager>
    <FormControlLabel control={<Switch checked={state.on} disabled={busy} onChange={(e) => state.setOn(e.target.checked)} data-manager-switch />}
      label={t('mgmt.servapps.manager.also', 'Also install a web manager for it')} />
    {state.on && <>
      <Stack direction="row" useFlexGap sx={{ flexWrap: 'wrap' }} spacing={1}>
        {state.options.map((r, i) => {
          const chosen = state.picked && state.picked.id === r.id;
          return <Chip key={r.id} clickable disabled={busy} color={chosen ? 'primary' : 'default'} variant={chosen ? 'filled' : 'outlined'} sx={{ maxWidth: '100%', ...touchChip }}
            label={i === 0 ? t('mgmt.servapps.manager.proposed', '{{name}} (proposed)', { name: r.name }) : r.name} onClick={() => state.pick(r)} data-manager-pick={r.id} />;
        })}
      </Stack>
      <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
        {state.existing
          ? t('mgmt.servapps.manager.sentenceExisting', '{{manager}} is created on a network of its own and joins {{db}} on a network only the two share; {{db}} stays on the networks it has. Its address in Cosmos is for administrators only, behind Cosmos\' sign-in. No port is published.', { manager: state.name, db: dbLabel, interpolation: { escapeValue: false } })
          : t('mgmt.servapps.manager.sentence', '{{manager}} is created with {{db}}, on a network only the two share, with an address in Cosmos for administrators only, behind Cosmos\' sign-in. No port is published.', { manager: state.name, db: dbLabel, interpolation: { escapeValue: false } })}
      </Typography>
      {note && <Typography variant="caption" sx={{ color: 'text.secondary', overflowWrap: 'anywhere' }}>{t(note[0], note[1])}</Typography>}
      {state.desc && state.desc.problem && <Alert severity="warning" sx={wrap}>{t('mgmt.servapps.manager.unreadable', 'Cosmos could not read the manager\'s image just now: the database is created without it. You can add one later from its page.')}</Alert>}
      <ManagerSettings settings={state.settings} onChange={state.setSettings} busy={busy} />
    </>}
  </Stack>;
};

// The web managers of the containers about to be deleted (containers of the
// ServApps list whose label names one of them), not already among them.
export const useManagersOf = (open, containers) => {
  const [managers, setManagers] = React.useState([]);
  const names = (containers || []).map((c) => String(c.Name || (c.Names && c.Names[0]) || '').replace(/^\//, '')).filter(Boolean);
  const key = names.join(' ');
  React.useEffect(() => {
    if (!open || !names.length || !API.signIn) { setManagers([]); return; }
    let live = true;
    // the server names the managers by their label as the administrator set it (never an image's own);
    // an account that may not read credentials gets no offer
    Promise.all(names.slice(0, 8).map((n) => API.signIn.details(n).then((res) => (res && res.data && res.data.managers) || []).catch(() => [])))
      .then((lists) => {
        const wanted = [...new Set(lists.flat())].filter((m) => !names.includes(m));
        if (!wanted.length) { if (live) setManagers([]); return null; }
        return API.docker.list().then((res) => {
          if (!live) return;
          setManagers(((res && res.data) || []).filter((c) => wanted.includes(String((c.Names && c.Names[0]) || '').replace(/^\//, '')))
            .map((c) => ({ ...c, Name: c.Names[0] })));
        });
      }).catch(() => {});
    return () => { live = false; };
  }, [open, key]);
  return managers;
};

export const ManagersOffer = ({ managers, on, onChange, busy }) => {
  const { t } = useTranslation();
  if (!(managers || []).length) return null;
  const list = managers.map((m) => String(m.Name).replace(/^\//, '')).join(', ');
  return <FormControlLabel control={<Switch checked={on} disabled={busy} onChange={(e) => onChange(e.target.checked)} data-delete-managers />}
    label={t('mgmt.servapps.manager.alsoDelete', 'Also delete its web manager ({{list}})', { list })} />;
};

const envOf = (container) => Object.fromEntries(((container && container.Config && container.Config.Env) || []).map((line) => [String(line).split('=')[0], String(line).slice(String(line).indexOf('=') + 1)]));

// "Add a web manager" on the page of a database already on the server.
export const AddManagerDialog = ({ open, onClose, containerName, refresh }) => {
  const { t } = useTranslation();
  const phone = useMediaQuery((theme) => theme.breakpoints.down('sm'));
  const [container, setContainer] = React.useState(null);
  const [desc, setDesc] = React.useState(null);
  const [config, setConfig] = React.useState(null);
  const [defaults, setDefaults] = React.useState(null);
  const [suffix] = React.useState(() => networkSuffix(browserRandom));
  React.useEffect(() => {
    if (!open) return;
    API.docker.get(containerName).then((res) => {
      const data = res && res.data;
      setContainer(data || null);
      const image = data && data.Config && data.Config.Image;
      if (image) API.docker.imageDescribe(String(image).split('@')[0]).then((r) => setDesc(r.data || null)).catch(() => setDesc({ problem: 'unreachable' }));
    }).catch(() => setContainer(null));
    API.config.get().then((res) => setConfig((res && res.data) || null)).catch(() => {});
    if (API.signIn) API.signIn.defaults().then((res) => setDefaults((res && res.data) || null)).catch(() => {});
  }, [open, containerName]);
  const recipe = desc && desc.recipe;
  const values = envOf(container);
  const masked = Object.values(values).some((v) => /^\*{3,}$/.test(v));
  const state = useManager({ kind: recipe && recipe.provides, managers: (desc && desc.managers) || [], dbName: containerName, defaults, config });
  const definition = state.manager && recipe && !masked
    ? managerDefinition({ manager: state.manager.recipe, ref: state.manager.ref, tag: state.manager.tag, name: state.manager.name, settings: state.manager.settings, host: state.manager.host, db: { name: containerName, recipe, values }, suffix })
    : null;
  return <Dialog open={open} onClose={(e, reason) => { if (reason !== 'backdropClick') onClose(); }} fullWidth maxWidth="md" fullScreen={phone} data-add-manager>
    <DialogTitle>{t('mgmt.servapps.manager.addTitle', 'A web manager for {{name}}', { name: containerName })}</DialogTitle>
    <DialogContent>
      <Stack spacing={2} sx={{ pt: 1, minWidth: 0 }}>
        {!desc && <Typography variant="body2">{t('mgmt.servapps.manager.reading', 'Reading what this database is…')}</Typography>}
        {desc && !recipe && <Alert severity="info" sx={wrap}>{t('mgmt.servapps.manager.unknown', 'Cosmos does not know which kind of database this image is, so it cannot propose a manager for it.')}</Alert>}
        {recipe && !state.options.length && <Alert severity="info" sx={wrap}>{t('mgmt.servapps.manager.none', 'Cosmos has no web manager for {{name}}.', { name: recipe.name })}</Alert>}
        {masked && <Alert severity="warning" sx={wrap} data-manager-masked>{t('mgmt.servapps.manager.masked', 'The manager needs the database\'s settings, which your account cannot read now: confirm your password (or ask an administrator who may read credentials).')}</Alert>}
        {recipe && state.options.length > 0 && <>
          <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}><DatabaseOutlined /> {recipe.name}: {containerName}</Typography>
          <ManagerChoice state={{ ...state, on: true, setOn: () => {}, existing: true }} busy={false} dbLabel={containerName} />
        </>}
        {definition && <NewDockerService service={definition} refresh={refresh} />}
      </Stack>
    </DialogContent>
    <DialogActions><Button onClick={onClose}>{t('global.close')}</Button></DialogActions>
  </Dialog>;
};

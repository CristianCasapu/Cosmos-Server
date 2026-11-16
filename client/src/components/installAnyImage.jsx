// Fork addition (plan 40): "Install any image". The administrator types a
// name (postgres, redis, ghcr.io/owner/app, registry.example.org/team/app:1.2),
// picks an image from Docker Hub's answer or names one directly, and gets the
// same one review every installation has (NewDockerService with
// components/creationReview.jsx), filled in from what the server read about
// the image (POST /api/images/describe: its declaration, its tags, the recipe
// Cosmos has for it). The logic is in utils/anyImage.js. Nothing is pulled or
// created before Create; generated secrets live only in this component's
// state and in the definition sent with Create.
import * as React from 'react';
import {
  Alert, Box, Button, Chip, Dialog, DialogActions, DialogContent, DialogTitle, Stack, TextField, Typography, useMediaQuery,
} from '@mui/material';
import { AppstoreAddOutlined, CopyOutlined, DatabaseOutlined, DownOutlined, RightOutlined, SearchOutlined, StarOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import NewDockerService from '../pages/servapps/containers/newService';
import ResponsiveButton from './responseiveButton';
import PermissionGuard from './permissionGuard';
import { PERM_RESOURCES } from '../utils/permissions';
import { localAddress } from './hostnameSuggestions';
import {
  browserRandom, buildDefinition, databaseNeed, declaredSettings, databaseRecipes, existingDatabases, facts, isDirectReference, marketMatch,
  connectionOf, filledNames as namesFilled, isMasked, isShellOnly, nameFor, parseReference, problemText, proposedSettings, text as aiText, withCustom,
} from '../utils/anyImage';

import { HandSettings, ImageNeeds, hintHelp } from './imageNeeds'; // Fork addition (plan 43): what an image needs, and where that is known from
import { hintSettings } from '../utils/services'; // Fork addition (plan 43)
import { ManagerChoice, useManager } from './managerChoice'; // Fork addition (plan 45): a web manager with a database

// "NAME=value" lines typed by the administrator, as settings.
const linesToSettings = (text) => String(text || '').split('\n').map((l) => l.trim())
  .filter((l) => /^[A-Za-z_][A-Za-z0-9_.]*=/.test(l)).slice(0, 64)
  .map((l) => ({ name: l.slice(0, l.indexOf('=')), value: l.slice(l.indexOf('=') + 1), type: 'fixed' }));

const wrap = { overflowWrap: 'anywhere', minWidth: 0, '& .MuiAlert-message': { minWidth: 0 } };
// a finger's height on a phone (the skin gives it to buttons in a page's main area, not in dialogs);
// '&&&' outweighs the skin's doubled classes
const touch = { '&&&': { minHeight: { xs: 44, sm: 0 } } };
const touchChip = { '&&&': { height: { xs: 'auto', sm: 32 }, minHeight: { xs: 44, sm: 0 }, minWidth: { xs: 44, sm: 0 } }, '& .MuiChip-label': { whiteSpace: 'normal' } };
const touchLink = { display: { xs: 'inline-flex', sm: 'inline' }, alignItems: 'center', minHeight: { xs: 44, sm: 0 } };

const useSay = () => {
  const { t } = useTranslation();
  const say = (line) => {
    if (!line) return '';
    const params = { ...(line.params || {}), interpolation: { escapeValue: false } };
    if (line.why) params.why = say(line.why);
    return t(line.key, line.text, params);
  };
  return say;
};

const count = (n) => {
  const value = Number(n) || 0;
  if (value >= 1e9) return Math.round(value / 1e8) / 10 + 'B';
  if (value >= 1e6) return Math.round(value / 1e5) / 10 + 'M';
  if (value >= 1e3) return Math.round(value / 100) / 10 + 'k';
  return String(value);
};

const allApps = (answer) => Object.entries((answer && answer.all) || {}).flatMap(([store, list]) => (list || []).map((app) => ({ ...app, appstore: store })));

// One variable: a secret is shown in clear once, with a copy button.
const SettingField = ({ setting, onChange, busy }) => {
  const { t } = useTranslation();
  const [copied, setCopied] = React.useState(false);
  const helper = hintHelp(t, setting) /* Fork addition (plan 43) */ || (setting.fromDefaults /* Fork addition (plan 45) */ ? (setting.variant
    ? t('mgmt.servapps.signIn.fromDefaultsVariant', 'From Configuration > Sign-in details for new apps: {{typed}} in the form this app takes.', { typed: setting.typed, interpolation: { escapeValue: false } })
    : t('mgmt.servapps.signIn.fromDefaults', 'From Configuration > Sign-in details for new apps.')) : setting.type === 'secret' ? t('mgmt.servapps.anyImage.ui.secretHelp', 'Generated for you. After the creation it is on the app\'s page, under Sign-in details (or in its settings).')
    : setting.type === 'name' ? t('mgmt.servapps.anyImage.ui.nameHelp', 'Proposed: change it if you like.')
      : setting.type === 'declared' ? t('mgmt.servapps.anyImage.ui.declaredHelp', 'The image\'s default.')
        : setting.type === 'fixed' ? t('mgmt.servapps.anyImage.ui.fixedHelp', 'Needed by this image as it is set here.') : t('mgmt.servapps.anyImage.ui.optionalHelp', 'Optional.'));
  return <Stack direction="row" spacing={1} sx={{ alignItems: 'flex-start', minWidth: 0 }} data-setting={setting.name}>
    <TextField size="small" fullWidth label={setting.name} value={setting.value} disabled={busy}
      onChange={(e) => onChange(e.target.value)} helperText={helper} error={setting.type === 'needed' && !setting.value /* Fork addition (plan 43) */} />
    {setting.type === 'secret' && <Button size="small" variant="outlined" sx={{ flexShrink: 0, mt: 0.25, ...touch }} startIcon={<CopyOutlined />} data-copy-secret
      onClick={() => {
        try {
          navigator.clipboard.writeText(setting.value).then(() => setCopied(true), () => setCopied(false));
        } catch (e) { /* no clipboard: the value is on screen */ }
      }}>
      {copied ? t('mgmt.servapps.anyImage.ui.copied', 'Copied') : t('mgmt.servapps.anyImage.ui.copy', 'Copy')}
    </Button>}
  </Stack>;
};

// Chips that pick names from a list (the apps that may reach a service).
const PickChips = ({ options, picked, onChange, busy }) => <Stack direction="row" useFlexGap sx={{ flexWrap: 'wrap' }} spacing={1}>
  {options.map((name) => {
    const on = picked.includes(name);
    return <Chip key={name} label={name} clickable disabled={busy} color={on ? 'primary' : 'default'} variant={on ? 'filled' : 'outlined'}
      onClick={() => onChange(on ? picked.filter((n) => n !== name) : [...picked, name])} sx={{ maxWidth: '100%', ...touchChip }} />;
  })}
</Stack>;

const InstallAnyImage = ({ open, onClose, initial, refresh }) => {
  const { t } = useTranslation();
  const say = useSay();
  const phone = useMediaQuery((theme) => theme.breakpoints.down('sm'));
  const [query, setQuery] = React.useState(initial || ''); // the words are there from the first paint
  const [results, setResults] = React.useState(null);
  const [searchProblem, setSearchProblem] = React.useState('');
  const [busy, setBusy] = React.useState(false);
  const [desc, setDesc] = React.useState(null);
  const [name, setName] = React.useState('');
  const [settings, setSettings] = React.useState([]);
  const [reachers, setReachers] = React.useState([]);
  const [containers, setContainers] = React.useState([]);
  const [config, setConfig] = React.useState(null);
  const [market, setMarket] = React.useState([]);
  const [anyway, setAnyway] = React.useState(false);
  const [allTags, setAllTags] = React.useState(false);
  const [showOptional, setShowOptional] = React.useState(false);
  const [db, setDb] = React.useState({ mode: '' });
  const [dbDesc, setDbDesc] = React.useState(null);
  const [dbSettings, setDbSettings] = React.useState([]);
  const [dbEnv, setDbEnv] = React.useState([]);
  const [custom, setCustom] = React.useState({ volume: '', port: '', command: '', env: '' });
  const [signInDefaults, setSignInDefaults] = React.useState(null); // Fork addition (plan 45)
  const asked = React.useRef(0);

  const reset = () => {
    setResults(null); setSearchProblem(''); setDesc(null); setName(''); setSettings([]); setReachers([]); setAnyway(false);
    setAllTags(false); setShowOptional(false); setDb({ mode: '' }); setDbDesc(null); setDbSettings([]); setDbEnv([]);
    setCustom({ volume: '', port: '', command: '', env: '' });
  };

  const describe = (reference, keepName) => {
    const ticket = ++asked.current;
    setBusy(true);
    setDesc((old) => (old ? { ...old, loading: true } : { reference, loading: true }));
    API.docker.imageDescribe(reference).then((res) => {
      if (ticket !== asked.current) return;
      const answer = res.data || {};
      setDesc(answer);
      const ref = parseReference(answer.reference || reference);
      if (!keepName) setName(nameFor(ref, containers.map((c) => (c.Names || [''])[0])));
      // Fork addition (plan 43): without a recipe, what the publisher's description names is proposed too (marked as such)
      setSettings([...proposedSettings(answer.recipe || null, answer.declared || null, browserRandom, signInDefaults /* Fork addition (plan 45) */),
        ...(answer.recipe ? [] : hintSettings(answer.hints, declaredSettings(answer.declared).map((s) => s.name), browserRandom))]);
      setDb({ mode: '' }); setDbDesc(null); setDbSettings([]); setDbEnv([]);
    }).catch((e) => {
      if (ticket === asked.current) setDesc({ reference, problem: 'unreachable' });
    }).finally(() => { if (ticket === asked.current) setBusy(false); });
  };

  const runSearch = (text) => {
    const value = String(text || '').trim();
    if (!value) return;
    reset();
    if (isDirectReference(value)) {
      describe(value);
      return;
    }
    if (parseReference(value).problem && !/^[a-z0-9][a-z0-9._/-]*$/i.test(value)) {
      setDesc({ reference: value, problem: 'invalid' });
      return;
    }
    setBusy(true);
    API.docker.imageSearch(value.toLowerCase()).then((res) => {
      const data = res.data || {};
      setResults(data.results || []);
      setSearchProblem(data.problem || '');
    }).catch(() => { setResults([]); setSearchProblem('unreachable'); }).finally(() => setBusy(false));
  };

  React.useEffect(() => {
    if (!open) return;
    setQuery(initial || '');
    reset();
    API.docker.list().then((res) => setContainers((res && res.data) || [])).catch(() => {});
    API.config.get().then((res) => setConfig((res && res.data) || null)).catch(() => {});
    if (API.signIn) API.signIn.defaults().then((res) => setSignInDefaults((res && res.data) || null)).catch(() => {}); // Fork addition (plan 45)
    if (API.market && API.market.list) API.market.list().then((res) => setMarket(allApps(res && res.data))).catch(() => {});
    if (initial) runSearch(initial);
  }, [open]);

  const ref = desc && desc.reference && !desc.problem ? parseReference(desc.reference) : null;
  const recipe = (desc && desc.recipe) || null;
  const rawDeclared = (desc && desc.declared) || null;
  // an image without recipe that says little gets the open fields: a folder, a port, a command, settings
  const openFields = !!rawDeclared && !(desc && desc.recipe) && ((!(rawDeclared.ports || []).length && !(rawDeclared.volumes || []).length) || isShellOnly(rawDeclared));
  const declared = openFields ? withCustom(rawDeclared, custom) : rawDeclared;
  const extraSettings = linesToSettings(custom.env); // Fork addition (plan 43): settings by hand for every image (was: only for one that declares nothing)
  const marketApp = ref ? marketMatch(market, ref.name, desc.market) : null;
  const need = desc && !desc.problem ? databaseNeed(recipe, declared) : null;
  const dbRecipes = need ? databaseRecipes(desc.databases, need.kinds) : [];
  const existing = need ? existingDatabases(containers, desc.databases, need.kinds) : [];
  const host = ref && config ? localAddress(name || 'app', config) : '';
  const others = containers.map((c) => String((c.Names || [''])[0]).replace(/^\//, '')).filter((n) => n && n !== name);
  const isService = !!(recipe && recipe.service);
  // Fork addition (plan 45): a database gets its web manager; defaults that arrive after the image was read still apply
  const mgr = useManager({ kind: recipe && ['database', 'cache'].includes(recipe.kind) ? recipe.provides : null, managers: (desc && desc.managers) || [], dbName: name, defaults: signInDefaults, config });
  React.useEffect(() => {
    if (!signInDefaults || !recipe) return;
    const fresh = proposedSettings(recipe, null, browserRandom, signInDefaults);
    setSettings((old) => old.map((o) => {
      const f = fresh.find((x) => x.name === o.name);
      const own = (recipe.env || []).find((e) => e.name === o.name);
      return f && f.fromDefaults && own && o.value === (own.value || '') ? f : o;
    }));
  }, [signInDefaults, recipe && recipe.id]);

  const pickDbRecipe = (r) => {
    setDb({ mode: 'new', recipe: r });
    setDbSettings(proposedSettings(r, null, browserRandom));
    setDbDesc(null);
    API.docker.imageDescribe(r.images[0]).then((res) => setDbDesc(res.data || null)).catch(() => setDbDesc({ problem: 'unreachable' }));
  };
  const pickExisting = (container) => {
    setDb({ mode: 'existing', container: container.name, recipe: container.recipe });
    setDbEnv([]);
    API.docker.get(container.name).then((res) => {
      const env = (res && res.data && res.data.Config && res.data.Config.Env) || [];
      setDbEnv(env);
      // values this account may not read come as ***: the app's matching settings are left empty for the administrator
      if (env.some((line) => isMasked(String(line).slice(String(line).indexOf('=') + 1))) && need) {
        const roles = Object.entries(need.env || {}).filter(([role]) => role !== 'host' && role !== 'port').map(([, name]) => name);
        setSettings((old) => old.map((o) => (roles.includes(o.name) ? { ...o, value: '', type: 'name' } : o)));
      }
    }).catch(() => {});
  };

  const ready = ref && declared && desc.fits !== 'no' && !desc.loading && name && (!marketApp || anyway)
    && !(need && !need.optional && !db.mode) && !(db.mode === 'new' && !(dbDesc && dbDesc.tag)) && !mgr.waiting /* Fork addition (plan 45) */;
  const definition = React.useMemo(() => {
    if (!ready) return null;
    let database = null;
    if (db.mode === 'new') {
      database = { mode: 'new', ref: parseReference(dbDesc.reference), tag: dbDesc.tag, recipe: db.recipe, name: `${name}-${db.recipe.id}`, settings: dbSettings };
    } else if (db.mode === 'existing') {
      database = { mode: 'existing', container: db.container, recipe: db.recipe, env: dbEnv };
    }
    return buildDefinition({ ref, tag: desc.tag, name, recipe, declared, settings: [...settings.filter((s) => !extraSettings.some((e) => e.name === s.name)), ...extraSettings] /* Fork addition (plan 43): a name typed by hand replaces the proposal */, host, reachers, database, need, command: openFields ? custom.command : '', manager: mgr.manager /* Fork addition (plan 45) */ }).definition;
  }, [ready, desc, name, settings, host, reachers, db, dbDesc, dbSettings, dbEnv, custom, mgr.manager && JSON.stringify(mgr.manager)]);

  const lines = desc && rawDeclared ? facts({ recipe, declared: rawDeclared, name: name || 'app', tag: desc.tag, host: definition && definition.services[name] && definition.services[name].routes ? host : '', settings, size: desc.size }) : [];
  const problem = desc && desc.problem ? problemText(desc) : null;
  // the app's variables a chosen database fills in are not asked for
  const dbValues = Object.fromEntries((dbEnv || []).map((line) => [String(line).split('=')[0], String(line).slice(String(line).indexOf('=') + 1)]));
  const dbMasked = db.mode === 'existing' && Object.values(dbValues).some(isMasked);
  const filledNames = !need ? []
    : db.mode === 'new' ? [...Object.values(need.env || {}), need.typeName].filter(Boolean)
      : db.mode === 'existing' ? namesFilled(need, connectionOf(db.recipe, db.container, dbValues), db.recipe && db.recipe.provides) : [];
  const visible = settings.filter((s) => (s.type !== 'optional' || showOptional) && !filledNames.includes(s.name));
  const optionalCount = settings.filter((s) => s.type === 'optional').length;
  const tags = (desc && desc.tags && desc.tags.list) || [];
  const shownTags = allTags ? tags : tags.slice(0, 8);
  const why = desc && desc.tags && desc.tags.why === 'pinMajor' && !(recipe && recipe.kind === 'database') ? 'pinMajorApp' : desc && desc.tags && desc.tags.why;
  const tagWhy = desc && desc.tags && (desc.tagTyped ? aiText('tag.typed') : why ? aiText('tag.' + why, { tag: desc.tags.suggested }) : null);

  return <Dialog open={open} onClose={(e, reason) => { if (reason !== 'backdropClick') onClose(); }} fullWidth maxWidth="md" fullScreen={phone} data-any-image>
    <DialogTitle>{t('mgmt.servapps.anyImage.ui.title', 'Install any image')}</DialogTitle>
    <DialogContent>
      <Stack spacing={2} sx={{ minWidth: 0, pt: 1 }}>
        <Typography variant="body2" sx={{ color: 'text.secondary' }}>
          {t('mgmt.servapps.anyImage.ui.intro', 'Search Docker Hub, or type an image\'s full name from any registry (for example ghcr.io/owner/app or registry.example.org/team/app:1.2). Cosmos reads what the image declares without downloading it, proposes the rest, and shows everything before anything is created.')}
        </Typography>
        <Stack direction="row" spacing={1} sx={{ minWidth: 0 }} component="form" onSubmit={(e) => { e.preventDefault(); runSearch(query); }}>
          <TextField size="small" fullWidth value={query} onChange={(e) => setQuery(e.target.value)} disabled={busy}
            label={t('mgmt.servapps.anyImage.ui.field', 'Image or search words')} placeholder="postgres" data-any-image-field />
          <Button type="submit" variant="contained" disabled={busy || !query.trim()} startIcon={<SearchOutlined />} sx={{ flexShrink: 0, ...touch }} data-any-image-search>
            {t('mgmt.servapps.anyImage.ui.search', 'Look')}
          </Button>
        </Stack>

        {searchProblem && <Alert severity="warning" sx={wrap}>{say(aiText('search.problem.' + (['rateLimited', 'busy'].includes(searchProblem) ? searchProblem : 'unreachable')))}</Alert>}
        {results && !desc && <Stack spacing={1} sx={{ minWidth: 0 }} data-any-image-results>
          {results.length === 0 && !searchProblem && <Typography variant="body2">{t('mgmt.servapps.anyImage.ui.noResult', 'Docker Hub has no image for these words. If the image is on another registry, type its full name.')}</Typography>}
          {results.map((r) => <Button key={r.name} variant="outlined" color="inherit" onClick={() => describe(r.name)} disabled={busy} data-result={r.name}
            sx={{ justifyContent: 'flex-start', textAlign: 'left', textTransform: 'none', minWidth: 0, display: 'block', py: 1, '&&&': { minHeight: 44 } }}>
            <Stack direction="row" spacing={1} useFlexGap sx={{ alignItems: 'center', flexWrap: 'wrap', minWidth: 0 }}>
              <Typography variant="subtitle1" component="span" sx={{ overflowWrap: 'anywhere' }}>{r.name}</Typography>
              {r.official && <Chip size="small" color="primary" label={t('mgmt.servapps.anyImage.ui.official', 'Docker official image')} />}
              {r.recipe && <Chip size="small" variant="outlined" label={t('mgmt.servapps.anyImage.ui.hasRecipe', 'Settings known to Cosmos')} />}
              <Typography variant="caption" component="span" sx={{ color: 'text.secondary' }}>
                <StarOutlined /> {count(r.stars)} · {t('mgmt.servapps.anyImage.ui.pulls', '{{count}} downloads', { count: count(r.pulls) })}
              </Typography>
            </Stack>
            {r.description && <Typography variant="body2" component="div" sx={{ color: 'text.secondary', overflowWrap: 'anywhere' }}>{r.description}</Typography>}
          </Button>)}
        </Stack>}

        {desc && desc.loading && !desc.declared && <Typography variant="body2" sx={{ color: 'text.secondary' }}>{t('mgmt.servapps.anyImage.ui.reading', 'Reading what the image declares…')}</Typography>}
        {problem && <Alert severity="error" sx={wrap} data-any-image-problem={desc.problem}>
          {say(problem)}
          {desc.page && <> <Box component="a" sx={touchLink} href={desc.page} target="_blank" rel="noopener noreferrer">{t('mgmt.servapps.anyImage.ui.page', 'The image\'s page')}</Box></>}
        </Alert>}

        {desc && !desc.problem && desc.repository && <Stack spacing={1.5} sx={{ minWidth: 0 }} data-any-image-about>
          <Stack direction="row" spacing={1} sx={{ alignItems: 'center', flexWrap: 'wrap' }} useFlexGap>
            <Typography variant="h5" sx={{ overflowWrap: 'anywhere' }}>{recipe ? recipe.name : ref && ref.name}</Typography>
            {recipe && <Typography variant="body2" sx={{ color: 'text.secondary', overflowWrap: 'anywhere' }}>{ref && ref.name}</Typography>}
            {desc.official && <Chip size="small" color="primary" label={t('mgmt.servapps.anyImage.ui.official', 'Docker official image')} />}
            {desc.page && <Box component="a" sx={touchLink} href={desc.page} target="_blank" rel="noopener noreferrer">{t('mgmt.servapps.anyImage.ui.page', 'The image\'s page')}</Box>}
            {recipe && recipe.source && recipe.source !== desc.page && <Box component="a" sx={touchLink} href={recipe.source} target="_blank" rel="noopener noreferrer">{t('mgmt.servapps.anyImage.ui.docs', 'Its documentation')}</Box>}
          </Stack>

          {marketApp && <Alert severity="info" sx={wrap} data-any-image-market>
            <Stack spacing={1}>
              <div>{say(aiText('market.has', { market: marketApp.name }))}</div>
              <Stack direction="row" spacing={1} useFlexGap sx={{ flexWrap: 'wrap' }}>
                <Button size="small" variant="contained" href={`/cosmos-ui/market-listing/${encodeURIComponent(marketApp.appstore)}/${encodeURIComponent(marketApp.name)}`}>{t('mgmt.servapps.anyImage.ui.openMarket', 'Open it in the Market')}</Button>
                {!anyway && <Button size="small" onClick={() => setAnyway(true)}>{t('mgmt.servapps.anyImage.ui.anyway', 'Install the image anyway')}</Button>}
              </Stack>
            </Stack>
          </Alert>}

          <Stack spacing={0.5} sx={{ minWidth: 0 }} data-any-image-tags>
            <Typography variant="subtitle1">{t('mgmt.servapps.anyImage.ui.version', 'Version (tag): {{tag}}', { tag: desc.tag })}</Typography>
            {tagWhy && <Typography variant="caption" sx={{ color: 'text.secondary', overflowWrap: 'anywhere' }}>{say(tagWhy)}</Typography>}
            {tags.length > 0 && <Stack direction="row" useFlexGap sx={{ flexWrap: 'wrap' }} spacing={1}>
              {shownTags.map((tag) => <Chip key={tag} size="small" label={tag} clickable disabled={busy}
                color={tag === desc.tag ? 'primary' : 'default'} variant={tag === desc.tag ? 'filled' : 'outlined'}
                onClick={() => { if (tag !== desc.tag) describe(`${ref.name}:${tag}`, true); }} sx={touchChip} />)}
              {tags.length > 8 && <Button size="small" sx={touch} onClick={() => setAllTags(!allTags)} endIcon={allTags ? <DownOutlined /> : <RightOutlined />}>
                {allTags ? t('mgmt.servapps.anyImage.ui.fewerTags', 'Fewer') : t('mgmt.servapps.anyImage.ui.moreTags', 'All {{count}} tags', { count: tags.length })}
              </Button>}
            </Stack>}
          </Stack>

          {desc.fits === 'no' && <Alert severity="error" sx={wrap} data-any-image-platform>
            {say(aiText('problem.platform', { name: ref && ref.name, platforms: (desc.platforms || []).join(', ') }))}
          </Alert>}

          {declared && desc.fits !== 'no' && <>
            <TextField size="small" label={t('mgmt.servapps.anyImage.ui.name', 'Name of the container')} value={name} disabled={busy}
              onChange={(e) => setName(e.target.value.toLowerCase().replace(/[^a-z0-9_.-]/g, '-').slice(0, 60))}
              helperText={t('mgmt.servapps.anyImage.ui.nameHint', 'Other apps reach it by this name.')} data-any-image-name />

            <Stack spacing={0.5} sx={{ minWidth: 0 }} data-any-image-facts>
              {lines.map((line, i) => <Typography key={i} variant="body2" sx={{ overflowWrap: 'anywhere' }}>{say(line)}</Typography>)}
            </Stack>
            <ImageNeeds desc={desc} recipe={recipe} declared={rawDeclared} reference={ref && ref.name} />{/* Fork addition (plan 43) */}

            {openFields && <Stack spacing={1.5} sx={{ minWidth: 0 }} data-any-image-open-fields>
              <TextField size="small" label={t('mgmt.servapps.anyImage.ui.command', 'Command to run')} value={custom.command} disabled={busy}
                onChange={(e) => setCustom({ ...custom, command: e.target.value.slice(0, 1000) })}
                helperText={t('mgmt.servapps.anyImage.ui.commandHint', 'Leave empty for the image\'s own. For example: httpd -f -p 8080')} />
              <TextField size="small" label={t('mgmt.servapps.anyImage.ui.folder', 'Folder for its data, in the container')} value={custom.volume} disabled={busy}
                onChange={(e) => setCustom({ ...custom, volume: e.target.value.slice(0, 200) })}
                helperText={t('mgmt.servapps.anyImage.ui.folderHint', 'For example /data. The review proposes where it goes on the server.')} />
              <TextField size="small" label={t('mgmt.servapps.anyImage.ui.port', 'Its web port, if it has a web interface')} value={custom.port} disabled={busy}
                onChange={(e) => setCustom({ ...custom, port: e.target.value.replace(/[^0-9]/g, '').slice(0, 5) })}
                helperText={t('mgmt.servapps.anyImage.ui.portHint', 'For example 8080. It gets an address in Cosmos; nothing is published on the server.')} />
              <TextField size="small" multiline minRows={2} label={t('mgmt.servapps.anyImage.ui.env', 'Settings (variables)')} value={custom.env} disabled={busy}
                onChange={(e) => setCustom({ ...custom, env: e.target.value.slice(0, 8000) })}
                helperText={t('mgmt.servapps.anyImage.ui.envHint', 'One per line, NAME=value, as its page lists them.')} />
            </Stack>}

            {visible.length > 0 && <Stack spacing={1.5} sx={{ minWidth: 0 }} data-any-image-settings>
              <Typography variant="subtitle1">{t('mgmt.servapps.anyImage.ui.settings', 'Its settings')}</Typography>
              {visible.map((s) => <SettingField key={s.name} setting={s} busy={busy}
                onChange={(value) => setSettings((old) => old.map((o) => (o.name === s.name ? { ...o, value } : o)))} />)}
            </Stack>}
            {optionalCount > 0 && <Button size="small" sx={{ alignSelf: 'flex-start', ...touch }} onClick={() => setShowOptional(!showOptional)} endIcon={showOptional ? <DownOutlined /> : <RightOutlined />}>
              {t('mgmt.servapps.anyImage.ui.optional', 'Optional settings ({{count}})', { count: optionalCount })}
            </Button>}
            {!openFields && <HandSettings value={custom.env} busy={busy} onChange={(env) => setCustom({ ...custom, env })} />}{/* Fork addition (plan 43) */}

            <ManagerChoice state={mgr} busy={busy} dbLabel={name} />{/* Fork addition (plan 45) */}

            {isService && others.length > 0 && <Stack spacing={1} sx={{ minWidth: 0 }} data-any-image-reach>
              <Typography variant="subtitle1">{t('mgmt.servapps.anyImage.ui.reachTitle', 'Let these apps reach it')}</Typography>
              <Typography variant="caption" sx={{ color: 'text.secondary' }}>{t('mgmt.servapps.anyImage.ui.reachHint', 'Each one you pick gets a network of its own with {{name}} (Cosmos keeps apps apart otherwise). It can be done later too, with "Link containers" on the app\'s Network tab.', { name: name || 'app' })}</Typography>
              <PickChips options={others} picked={reachers} onChange={setReachers} busy={busy} />
            </Stack>}

            {need && <Stack spacing={1} sx={{ minWidth: 0 }} data-any-image-db>
              <Typography variant="subtitle1"><DatabaseOutlined /> {say(aiText(need.optional ? 'db.needOptional' : 'db.need', { kinds: need.kinds.join(', ') }))}</Typography>
              <Stack direction="row" useFlexGap sx={{ flexWrap: 'wrap' }} spacing={1}>
                {dbRecipes.map((r) => <Chip key={r.id} clickable disabled={busy} label={say(aiText('db.new', { name: r.name }))}
                  color={db.mode === 'new' && db.recipe && db.recipe.id === r.id ? 'primary' : 'default'} variant={db.mode === 'new' && db.recipe && db.recipe.id === r.id ? 'filled' : 'outlined'}
                  onClick={() => pickDbRecipe(r)} sx={{ maxWidth: '100%', ...touchChip }} data-db-new={r.id} />)}
                {existing.map((c) => <Chip key={c.name} clickable disabled={busy} label={say(aiText('db.existingOne', { name: c.name, kind: c.recipe.name }))}
                  color={db.mode === 'existing' && db.container === c.name ? 'primary' : 'default'} variant={db.mode === 'existing' && db.container === c.name ? 'filled' : 'outlined'}
                  onClick={() => pickExisting(c)} sx={{ maxWidth: '100%', ...touchChip }} data-db-existing={c.name} />)}
                <Chip clickable disabled={busy} label={say(aiText('db.none'))} color={db.mode === 'none' ? 'primary' : 'default'} variant={db.mode === 'none' ? 'filled' : 'outlined'}
                  onClick={() => setDb({ mode: 'none' })} sx={{ maxWidth: '100%', ...touchChip }} data-db-none />
              </Stack>
              {existing.length === 0 && <Typography variant="caption" sx={{ color: 'text.secondary' }}>{say(aiText('db.noExisting', { kinds: need.kinds.join(', ') }))}</Typography>}
              {db.mode === 'new' && <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>{say(aiText('db.newSentence', { db: `${name}-${db.recipe.id}` + (dbDesc && dbDesc.tag ? ` (${db.recipe.images[0]}:${dbDesc.tag})` : '') }))}</Typography>}
              {db.mode === 'new' && dbDesc && dbDesc.problem && <Alert severity="error" sx={wrap}>{say(problemText(dbDesc))}</Alert>}
              {db.mode === 'new' && dbSettings.map((s) => <SettingField key={s.name} setting={s} busy={busy}
                onChange={(value) => setDbSettings((old) => old.map((o) => (o.name === s.name ? { ...o, value } : o)))} />)}
              {db.mode === 'existing' && <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>{say(aiText('db.existingSentence', { db: db.container }))}</Typography>}
              {dbMasked && <Alert severity="warning" sx={wrap} data-db-masked>{say(aiText('db.masked', { db: db.container }))}</Alert>}
              {filledNames.length > 0 && <Typography variant="caption" sx={{ color: 'text.secondary', overflowWrap: 'anywhere' }} data-db-filled>{say(aiText('db.filledList', { list: filledNames.join(', ') }))}</Typography>}
            </Stack>}
          </>}
        </Stack>}

        {definition && <Box sx={{ minWidth: 0 }} data-any-image-review>
          <NewDockerService service={definition} refresh={refresh} />
        </Box>}
      </Stack>
    </DialogContent>
    <DialogActions>
      {desc && <Button onClick={() => { asked.current += 1; setBusy(false); setDesc(null); if (!results) setQuery(''); }} data-any-image-back>{t('mgmt.servapps.anyImage.ui.back', 'Another image')}</Button>}
      <Button onClick={onClose}>{t('global.close')}</Button>
    </DialogActions>
  </Dialog>;
};

export default InstallAnyImage;

// The button, for a toolbar (ServApps) or the Market's top.
// `plain`: a button that keeps its words on a phone (the toolbars' buttons show only their icon there).
export const InstallAnyImageButton = ({ initial, refresh, variant = 'outlined', label, plain }) => {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const words = label || t('mgmt.servapps.anyImage.ui.button', 'Install any image');
  return <PermissionGuard permission={PERM_RESOURCES}>
    {plain
      ? <Button variant={variant} startIcon={<AppstoreAddOutlined />} onClick={() => setOpen(true)} sx={{ minHeight: 44, maxWidth: '100%', overflowWrap: 'anywhere' }} data-any-image-open>{words}</Button>
      : <ResponsiveButton variant={variant} startIcon={<AppstoreAddOutlined />} onClick={() => setOpen(true)} data-any-image-open>{words}</ResponsiveButton>}
    {open && <InstallAnyImage open={open} onClose={() => setOpen(false)} initial={initial} refresh={refresh} />}
  </PermissionGuard>;
};

// The Market's entry: a line at the top of the list, and, when a search finds
// no app, the offer to install the image named by the words typed.
export const MarketAnyImage = ({ search, noMatch }) => {
  const { t } = useTranslation();
  const words = String(search || '').trim().slice(0, 80);
  if (noMatch) {
    return <Stack spacing={1} sx={{ maxWidth: 720 }} data-market-any-image-nomatch>
      <InstallAnyImageButton initial={words} variant="contained" plain
        label={t('mgmt.servapps.anyImage.ui.marketNoMatch', 'Install the image “{{words}}”', { words, interpolation: { escapeValue: false } })} />
      <Typography variant="caption" sx={{ color: 'text.secondary' }}>{t('mgmt.servapps.anyImage.ui.marketNoMatchHint', 'Not in the Market: Cosmos looks for it on Docker Hub, reads what it needs and proposes the settings.')}</Typography>
    </Stack>;
  }
  return <Alert severity="info" icon={<AppstoreAddOutlined />} sx={{ ...wrap, maxWidth: 960 }} data-market-any-image
    >
    <Stack spacing={1} sx={{ alignItems: 'flex-start' }}>
      <div>{t('mgmt.servapps.anyImage.ui.marketTile', 'Not in the list? Any image from Docker Hub or another registry, databases included, installs with the same review.')}</div>
      <InstallAnyImageButton initial={words.length > 2 ? words : ''} variant="contained" plain />
    </Stack>
  </Alert>;
};

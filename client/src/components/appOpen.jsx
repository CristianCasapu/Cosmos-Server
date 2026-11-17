// Fork addition (plan 49): opening an app from where the visitor is.
//
// One rule for every screen (utils/appInterfaces.js): the tile or button
// opens the app's best address from here (the administration first for
// administrators); "More addresses" lists every interface with its role, its
// address, where it works and Copy; an app whose pages open only at home says
// "Only at home" and, pressed, explains and offers what makes sense (copy the
// address for later; for administrators the app's Internet dialog, unless the
// address is kept at home on purpose). Links to apps open in a new window of
// the system browser (target _blank, noopener): in the installed app on a
// phone that is the browser with its own sessions, and the panel stays as it
// was.
import * as React from 'react';
import {
  Box, Button, ButtonGroup, Dialog, DialogActions, DialogContent, DialogTitle, Divider, IconButton,
  ListItemIcon, ListItemText, ListSubheader, Menu, MenuItem, Stack, Tooltip, Typography,
} from '@mui/material';
import {
  CheckOutlined, CopyOutlined, DownOutlined, GlobalOutlined, HomeOutlined, LockOutlined, MoreOutlined, ExportOutlined,
} from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import { useClientInfos } from '../utils/hooks';
import { PERM_ADMIN, PERM_ADMIN_READ } from '../utils/permissions';
import { notify } from '../utils/activity';
import {
  appTile, containerApp, interfaceOfRoute, menuOrder, mergeDashboard, readPreference, reachMark, visitorPlace, writePreference,
} from '../utils/appInterfaces';
import { proposeAccess } from '../utils/exposure';
import { useCosmosPorts } from './routerPorts';
import { ExposeDialog } from './exposure';
import { Link as RouterLink } from 'react-router';
import { ReadOutlined } from '@ant-design/icons';
import { appGuide, guidePath } from '../utils/guides'; // Fork addition (plan 50)

// ---------------------------------------------------------------------------
// what every opener needs: where the visitor is, the URLs' roles, the ports

let statusStore = { at: 0, data: null, promise: null };
let dashboardStore = { at: 0, data: null, promise: null };
const FRESH = 15000;

const cached = (store, load, set) => {
  if (store.promise || (store.data && Date.now() - store.at < FRESH)) return store.promise || Promise.resolve(store.data);
  const promise = load().then((res) => {
    set({ at: Date.now(), data: (res && res.data) || null, promise: null });
    return res && res.data;
  }).catch(() => {
    set({ at: Date.now(), data: null, promise: null });
    return null;
  });
  set({ ...store, promise });
  return promise;
};

// The server's status (for "visitor"), shared by every opener of a page.
// The home page passes its own and nothing is asked again.
export const useVisitorStatus = (given) => {
  const own = given === undefined; // null: the page loads it itself (the home page), nothing is asked here
  const [data, setData] = React.useState(statusStore.data);
  React.useEffect(() => {
    if (!own) return undefined;
    let live = true;
    cached(statusStore, () => API.getStatus(), (s) => { statusStore = s; }).then((d) => { if (live) setData(d); });
    return () => { live = false; };
  }, [own]);
  return own ? data : given;
};

// The URLs' roles, labels and names for use at home (GET /api/dashboard), for
// the pages that list URLs from the configuration.
export const useDashboardRoutes = (given) => {
  const [data, setData] = React.useState(given || dashboardStore.data);
  React.useEffect(() => {
    if (given) return undefined;
    let live = true;
    cached(dashboardStore, () => API.config.getDashboard(), (s) => { dashboardStore = s; }).then((d) => { if (live) setData(d); });
    return () => { live = false; };
  }, [given]);
  return given || data;
};

// Forget what was loaded (after a URL was saved).
export const forgetAppLinks = () => {
  dashboardStore = { at: 0, data: null, promise: null };
};

const readStorage = () => {
  try { return window.localStorage; } catch (e) { return null; }
};

// status: the page's own GET /api/status answer (null while it loads), or
// leave it out and the status is asked once for all the openers of the page.
export const useOpenContext = ({ status } = {}) => {
  const { hasPermission } = useClientInfos();
  const isAdmin = hasPermission(PERM_ADMIN_READ);
  const visitorStatus = useVisitorStatus(status);
  // the ports as the server reports them (administrators); otherwise the page's own port is used, as
  // the URL chips do (the configuration's ports may not be the ones the server listens on)
  const ports = useCosmosPorts(null);
  const [prefer, setPrefer] = React.useState(() => readPreference(readStorage()));
  return React.useMemo(() => ({
    place: visitorPlace(visitorStatus, window.location),
    location: window.location,
    isAdmin,
    canExpose: hasPermission(PERM_ADMIN),
    ports,
    prefer,
    setPrefer: (value) => { writePreference(readStorage(), value); setPrefer(value); },
  }), [visitorStatus, isAdmin, ports, prefer]);
};

// ---------------------------------------------------------------------------
// copying

export const copyToClipboard = (text) => {
  try {
    if (navigator.clipboard && window.isSecureContext) return navigator.clipboard.writeText(text);
  } catch (e) { /* the fallback below */ }
  return new Promise((resolve, reject) => {
    // no clipboard API (a page on plain http): a selected field and the copy command
    const field = document.createElement('textarea');
    field.value = text;
    field.setAttribute('readonly', '');
    field.style.position = 'fixed';
    field.style.opacity = '0';
    document.body.appendChild(field);
    field.select();
    let ok = false;
    try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
    document.body.removeChild(field);
    if (ok) resolve(); else reject(new Error('copy'));
  });
};

const useCopy = () => {
  const { t } = useTranslation();
  return (text) => copyToClipboard(text)
    .then(() => notify(t('mgmt.appLinks.copied', 'Copied: {{text}}', { text }), 'success'))
    .catch(() => notify(t('mgmt.appLinks.copyFailed', 'This browser did not let the page copy. The address: {{text}}', { text }), 'warning'));
};

// ---------------------------------------------------------------------------
// words

const ROLE_TEXT = {
  admin: ['mgmt.appLinks.role.admin', 'Administration'],
  user: ['mgmt.appLinks.role.user', 'Page for users'],
  api: ['mgmt.appLinks.role.api', 'API'],
  other: ['mgmt.appLinks.role.other', 'Other page'],
};

export const useRoleText = () => {
  const { t } = useTranslation();
  return (role) => t(...(ROLE_TEXT[role] || ROLE_TEXT.other));
};

const MarkLine = ({ item }) => {
  const { t } = useTranslation();
  const mark = reachMark(item);
  const parts = [];
  if (mark === 'internet') parts.push(t('mgmt.appLinks.mark.internet', 'Internet'));
  else if (mark === 'vpn') parts.push(t('mgmt.appLinks.mark.vpn', 'VPN only'));
  else parts.push(t('mgmt.appLinks.mark.home', 'Home only'));
  if (item.reach === 'maybe') parts.push(t('mgmt.appLinks.mark.maybe', 'may not open from here'));
  if (item.auth) parts.push(t('mgmt.appLinks.mark.auth', 'behind Cosmos\' sign-in'));
  if (item.direct) parts.push(t('mgmt.appLinks.mark.direct', 'published port, without Cosmos'));
  if (!item.web) parts.push(item.protocol === 'udp' ? t('mgmt.appLinks.mark.udp', 'UDP, not a web page') : t('mgmt.appLinks.mark.notWeb', 'not a web page'));
  const Icon = mark === 'internet' ? GlobalOutlined : HomeOutlined;
  return <Box component="span" sx={{ display: 'inline-flex', gap: 0.5, alignItems: 'center', flexWrap: 'wrap' }}>
    <Icon aria-hidden />{parts.join(' · ')}{item.auth && <LockOutlined aria-hidden />}
  </Box>;
};

const itemTitle = (item, roleText, t) => {
  if (!item.web) return item.label ? item.label : t('mgmt.appLinks.port', 'Port {{port}}', { port: item.port });
  const role = roleText(item.role);
  return item.label ? item.label + ' (' + role + ')' : role;
};

const address = (item) => (item.web ? item.url : item.copy);

// ---------------------------------------------------------------------------
// the menu

const touchItem = { minHeight: 44, whiteSpace: 'normal', alignItems: 'flex-start' };

// Every interface of an app: Open (web addresses that may open from here) and
// Copy; endpoints that are not web pages Copy only.
export const AppInterfacesMenu = ({ tile, ctx, anchorEl, open, onClose, onUnreachable }) => {
  const { t } = useTranslation();
  const roleText = useRoleText();
  const copy = useCopy();
  if (!tile) return null;
  const items = [];
  items.push(<ListSubheader key="head" sx={{ lineHeight: 1.5, py: 1, whiteSpace: 'normal' }}>{t('mgmt.appLinks.menuTitle', 'Addresses of {{name}}', { name: tile.name })}</ListSubheader>);
  menuOrder(tile, ctx && ctx.isAdmin).forEach((item, index) => {
    const opens = item.web && (item.reach === 'opens' || item.reach === 'maybe');
    const title = itemTitle(item, roleText, t);
    if (opens) {
      items.push(<MenuItem key={'open' + index} component="a" href={item.url} target="_blank" rel="noopener noreferrer" onClick={onClose} sx={touchItem} data-interface={item.role} data-open={item.url}>
        <ListItemIcon sx={{ mt: '3px' }}>{tile.chosen === item ? <CheckOutlined aria-label={t('mgmt.appLinks.chosen', 'What the tile opens')} /> : <ExportOutlined aria-hidden />}</ListItemIcon>
        <ListItemText primary={t('mgmt.appLinks.open', 'Open {{what}}', { what: title })} secondary={<><Box component="span" sx={{ display: 'block', overflowWrap: 'anywhere' }}>{item.url}</Box><MarkLine item={item} /></>} />
      </MenuItem>);
    } else if (item.web) {
      items.push(<MenuItem key={'away' + index} onClick={() => { onClose(); if (onUnreachable) onUnreachable(); }} sx={touchItem} data-interface={item.role} data-unreachable={item.url}>
        <ListItemIcon sx={{ mt: '3px' }}><HomeOutlined aria-hidden /></ListItemIcon>
        <ListItemText primary={title} secondary={<><Box component="span" sx={{ display: 'block', overflowWrap: 'anywhere' }}>{item.url}</Box><MarkLine item={item} /></>} />
      </MenuItem>);
    } else {
      items.push(<MenuItem key={'end' + index} onClick={() => { onClose(); copy(item.copy); }} sx={touchItem} data-endpoint={item.copy}>
        <ListItemIcon sx={{ mt: '3px' }}><CopyOutlined aria-hidden /></ListItemIcon>
        <ListItemText primary={t('mgmt.appLinks.copyWhat', 'Copy {{what}}', { what: title })} secondary={<><Box component="span" sx={{ display: 'block', overflowWrap: 'anywhere' }}>{item.copy}</Box><MarkLine item={item} /></>} />
      </MenuItem>);
    }
    if (item.web) {
      items.push(<MenuItem key={'copy' + index} onClick={() => { onClose(); copy(item.url); }} sx={{ minHeight: 44, pl: 7 }} data-copy={item.url}>
        <ListItemIcon><CopyOutlined aria-hidden /></ListItemIcon>
        <ListItemText primary={t('mgmt.appLinks.copyAddress', 'Copy the address')} />
      </MenuItem>);
    }
    if (item.connection) {
      items.push(<MenuItem key={'conn' + index} onClick={() => { onClose(); copy(item.connection); }} sx={{ minHeight: 44, pl: 7, whiteSpace: 'normal' }} data-copy={item.connection}>
        <ListItemIcon><CopyOutlined aria-hidden /></ListItemIcon>
        <ListItemText primary={t('mgmt.appLinks.copyConnection', 'Copy the connection string')} secondary={t('mgmt.appLinks.connectionHint', 'With USER and PASSWORD to replace: the password stays behind "Sign-in details".')} />
      </MenuItem>);
    }
  });
  // Fork addition (plan 50): the app's how-to guide
  if (appGuide(tile)) {
    items.push(<Divider key="guide-div" />);
    items.push(<MenuItem key="guide" component={RouterLink} to={guidePath(tile)} onClick={onClose} sx={{ ...touchItem, alignItems: 'center' }} data-guide-link={tile.container || tile.name}>
      <ListItemIcon><ReadOutlined aria-hidden /></ListItemIcon>
      <ListItemText primary={t('mgmt.guide.menu', 'How to use it')} secondary={t('mgmt.guide.menuHint', 'Examples with this app\'s addresses')} />
    </MenuItem>);
  }
  if (ctx && ctx.place && ctx.place.where === 'home' && tile.interfaces.some((i) => i.web && i.kind === 'public') && tile.interfaces.some((i) => i.web && i.kind !== 'public' && i.kind !== 'panel')) {
    items.push(<Divider key="div" />);
    items.push(<MenuItem key="prefer" role="menuitemcheckbox" aria-checked={ctx.prefer === 'internet'} onClick={() => { ctx.setPrefer(ctx.prefer === 'internet' ? 'home' : 'internet'); onClose(); }} sx={{ ...touchItem, alignItems: 'center' }} data-prefer>
      <ListItemIcon>{ctx.prefer === 'internet' ? <CheckOutlined aria-hidden /> : <span />}</ListItemIcon>
      <ListItemText primary={t('mgmt.appLinks.preferInternet', 'At home, open the internet addresses first on this device')} />
    </MenuItem>);
  }
  return <Menu anchorEl={anchorEl} open={!!open && !!anchorEl} onClose={onClose}
    slotProps={{ paper: { sx: { maxWidth: 'min(440px, calc(100vw - 32px))', maxHeight: '70vh' } }, list: { 'aria-label': t('mgmt.appLinks.menuTitle', 'Addresses of {{name}}', { name: tile.name }) } }}>
    {items}
  </Menu>;
};

// ---------------------------------------------------------------------------
// when nothing opens from here

export const UnreachableDialog = ({ tile, ctx, config, onClose, refresh }) => {
  const { t } = useTranslation();
  const roleText = useRoleText();
  const copy = useCopy();
  const [expose, setExpose] = React.useState(false);
  if (!tile) return null;
  const where = (ctx && ctx.place && ctx.place.where) || 'away';
  const pages = tile.interfaces.filter((i) => i.web && i.role !== 'api');
  const shown = (pages.length ? pages : tile.interfaces.filter((i) => i.web));
  const endpoints = tile.interfaces.filter((i) => !i.web);
  const image = (tile.routes.find((r) => r.ForkImage) || {}).ForkImage || tile.image || '';
  const control = proposeAccess(image, null).source === 'control';
  const canExpose = ctx && ctx.canExpose && tile.container && !tile.keptHome && config;
  let sentence;
  if (tile.state === 'none') {
    sentence = t('mgmt.appLinks.none', '{{name}} has no web address.', { name: tile.name });
  } else if (tile.state === 'vpn-only') {
    sentence = t('mgmt.appLinks.vpnOnly', '{{name}} opens only through your VPN.', { name: tile.name });
  } else if (where === 'vpn') {
    sentence = t('mgmt.appLinks.homeOnlyVpn', 'The pages of {{name}} open on your home network only: through the VPN its .local names do not resolve, and its other home addresses open only if the VPN routes your home network.', { name: tile.name });
  } else if (where === 'home') {
    sentence = t('mgmt.appLinks.homeOnlyGuess', 'The pages of {{name}} open on your home network only, and this device seems to come in through the internet address. If you are at home, try them; otherwise copy one for later.', { name: tile.name });
  } else {
    sentence = t('mgmt.appLinks.homeOnly', 'The pages of {{name}} open on your home network only, and you are not at home now: copy an address for later.', { name: tile.name });
  }
  return <>
    <Dialog open={!expose} onClose={onClose} fullWidth maxWidth="sm" aria-labelledby="app-unreachable-title">
      <DialogTitle id="app-unreachable-title">{tile.name}</DialogTitle>
      <DialogContent>
        <Stack spacing={2}>
          <Typography data-unreachable-sentence>{sentence}</Typography>
          {tile.keptHome && <Typography color="textSecondary">{t('mgmt.appLinks.keptHome', 'It is kept at home on purpose (set on its URL).')}</Typography>}
          {!tile.keptHome && canExpose && control && <Typography color="textSecondary">{t('mgmt.appLinks.controlNote', 'It controls the server: an internet address for it is proposed for Cosmos administrators only.')}</Typography>}
          {shown.map((item, index) => <Stack key={index} direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ alignItems: { sm: 'center' }, justifyContent: 'space-between' }}>
            <Box sx={{ minWidth: 0 }}>
              <Typography variant="subtitle2">{itemTitle(item, roleText, t)}</Typography>
              <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>{item.url}</Typography>
              <Typography variant="caption" color="textSecondary"><MarkLine item={item} /></Typography>
            </Box>
            <Stack direction="row" spacing={1} sx={{ flexShrink: 0 }}>
              {item.reach === 'maybe' && <Button size="small" variant="outlined" href={item.url} target="_blank" rel="noopener noreferrer" sx={{ minHeight: 44 }}>{t('mgmt.appLinks.tryIt', 'Try it')}</Button>}
              <Button size="small" variant="outlined" startIcon={<CopyOutlined />} onClick={() => copy(item.url)} sx={{ minHeight: 44 }} data-copy={item.url}>{t('mgmt.appLinks.copy', 'Copy')}</Button>
            </Stack>
          </Stack>)}
          {endpoints.map((item, index) => <Stack key={'e' + index} direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ alignItems: { sm: 'center' }, justifyContent: 'space-between' }}>
            <Box sx={{ minWidth: 0 }}>
              <Typography variant="subtitle2">{itemTitle(item, roleText, t)}</Typography>
              <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>{item.copy}</Typography>
              <Typography variant="caption" color="textSecondary"><MarkLine item={item} /></Typography>
            </Box>
            <Button size="small" variant="outlined" startIcon={<CopyOutlined />} onClick={() => copy(item.connection || item.copy)} sx={{ minHeight: 44, flexShrink: 0 }} data-copy={item.connection || item.copy}>{item.connection ? t('mgmt.appLinks.copyConnectionShort', 'Copy connection string') : t('mgmt.appLinks.copy', 'Copy')}</Button>
          </Stack>)}
        </Stack>
      </DialogContent>
      <DialogActions sx={{ flexWrap: 'wrap', gap: 1 }}>
        {canExpose && tile.state !== 'none' && <Button variant="contained" startIcon={<GlobalOutlined />} onClick={() => setExpose(true)} sx={{ minHeight: 44 }} data-expose>{t('mgmt.appLinks.expose', 'Give it an internet address')}</Button>}
        <Button onClick={onClose} sx={{ minHeight: 44 }}>{t('global.close', 'Close')}</Button>
      </DialogActions>
    </Dialog>
    {expose && <ExposeDialog config={config} containerName={tile.container} image={image} refresh={() => { forgetAppLinks(); if (refresh) refresh(); }} onClose={() => { setExpose(false); onClose(); }} />}
  </>;
};

// ---------------------------------------------------------------------------
// the home page's tile

// Wraps a tile's content: a link to what it opens (exactly as before when the
// app has one address that opens), "More addresses" in its corner when it has
// more, "Only at home" when nothing opens from here.
export const AppTileLink = ({ tile, ctx, config, refresh, linkStyle, children }) => {
  const { t } = useTranslation();
  const [anchor, setAnchor] = React.useState(null);
  const [explain, setExplain] = React.useState(false);
  const more = tile.menu
    ? <Tooltip title={t('mgmt.appLinks.more', 'All addresses of {{name}}', { name: tile.name })}>
      <IconButton aria-label={t('mgmt.appLinks.more', 'All addresses of {{name}}', { name: tile.name })} aria-haspopup="menu" aria-expanded={!!anchor}
        onClick={(e) => { e.preventDefault(); e.stopPropagation(); setAnchor(e.currentTarget); }}
        sx={{ position: 'absolute', top: 2, right: 2, width: 44, height: 44, color: 'inherit', zIndex: 1 }} data-app-more={tile.name}>
        <MoreOutlined />
      </IconButton>
    </Tooltip>
    : null;
  const body = tile.link
    ? <a href={tile.link} target="_blank" rel="noopener noreferrer" style={{ textDecoration: 'none', display: 'block', ...linkStyle }} data-app-tile={tile.name} data-opens={tile.link}>{children}</a>
    : <Box component="button" type="button" onClick={() => setExplain(true)} data-app-tile={tile.name} data-state={tile.state}
      sx={{ all: 'unset', display: 'block', width: '100%', cursor: 'pointer', boxSizing: 'border-box', '&:focus-visible': { outline: '2px solid', outlineOffset: 2 } }} style={linkStyle}
      aria-label={tile.state === 'home-only' ? t('mgmt.appLinks.homeOnlyLabel', '{{name}}: only at home', { name: tile.name }) : tile.name}>
      {children}
      {tile.state !== 'opens' && <Typography variant="caption" component="div" sx={{ mt: 0.5, textAlign: 'center', fontWeight: 600 }} data-only-home>
        {tile.state === 'home-only'
          ? <><HomeOutlined aria-hidden /> {t('mgmt.appLinks.onlyAtHome', 'Only at home')}</>
          : tile.state === 'vpn-only' ? t('mgmt.appLinks.onlyVpn', 'Only through the VPN') : t('mgmt.appLinks.noWebAddress', 'No web address')}
      </Typography>}
    </Box>;
  return <>
    {body}
    {more}
    <AppInterfacesMenu tile={tile} ctx={ctx} anchorEl={anchor} open={!!anchor} onClose={() => setAnchor(null)} onUnreachable={() => setExplain(true)} />
    {explain && <UnreachableDialog tile={tile} ctx={ctx} config={config} refresh={refresh} onClose={() => setExplain(false)} />}
  </>;
};

// ---------------------------------------------------------------------------
// ServApps and the app's page: an "Open" split button

export const AppOpenButton = ({ name, routes, image, ports, config, refresh, size = 'small' }) => {
  const { t } = useTranslation();
  const ctx = useOpenContext({ config });
  const dashboard = useDashboardRoutes();
  const [anchor, setAnchor] = React.useState(null);
  const [explain, setExplain] = React.useState(false);
  const tile = React.useMemo(() => appTile(containerApp(name, mergeDashboard(routes, dashboard), image, ports), { ...ctx, allRoutes: dashboard || routes }),
    [name, routes, image, ports, dashboard, ctx]);
  if (!tile.interfaces.length) return null;
  const label = tile.link ? t('mgmt.appLinks.openShort', 'Open') : tile.state === 'home-only' ? t('mgmt.appLinks.onlyAtHome', 'Only at home') : t('mgmt.appLinks.addresses', 'Addresses');
  return <>
    <ButtonGroup size={size} variant="outlined" sx={{ flexShrink: 0, '& .MuiButton-root': { minHeight: 36 } }} data-app-open={tile.container}>
      {tile.link
        ? <Button href={tile.link} target="_blank" rel="noopener noreferrer" startIcon={<ExportOutlined />} data-opens={tile.link}
          title={tile.chosen ? itemTitle(tile.chosen, (r) => t(...(ROLE_TEXT[r] || ROLE_TEXT.other)), t) + ' — ' + tile.link : undefined}>{label}</Button>
        : <Button startIcon={<HomeOutlined />} onClick={(e) => (tile.interfaces.some((i) => i.web) ? setExplain(true) : setAnchor(e.currentTarget))} data-state={tile.state}>{label}</Button>}
      {tile.menu && <Button aria-label={t('mgmt.appLinks.more', 'All addresses of {{name}}', { name: tile.name })} aria-haspopup="menu" aria-expanded={!!anchor}
        onClick={(e) => setAnchor(e.currentTarget)} data-app-more={tile.container}><DownOutlined /></Button>}
    </ButtonGroup>
    <AppInterfacesMenu tile={tile} ctx={ctx} anchorEl={anchor} open={!!anchor} onClose={() => setAnchor(null)} onUnreachable={() => setExplain(true)} />
    {explain && <UnreachableDialog tile={{ ...tile, image }} ctx={ctx} config={config} refresh={refresh} onClose={() => setExplain(false)} />}
  </>;
};

// ---------------------------------------------------------------------------
// a chip of one URL (HostChip): does its own address open from here?

export const useRouteReach = (route) => {
  const ctx = useOpenContext();
  return React.useMemo(() => {
    const item = interfaceOfRoute(route, ctx);
    return { item, ctx };
  }, [route, ctx]);
};

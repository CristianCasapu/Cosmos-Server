// Fork addition: every notification of the signed-in user, in full, newest
// first, with filters (in the address, so a filtered list can be linked),
// and the commands to mark read or unread and to delete one's own copies.
import * as React from 'react';
import { Link as RouterLink, useLocation, useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import {
  Alert, Box, Button, Chip, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, IconButton, Link,
  MenuItem, Stack, TextField, Tooltip, Typography, useMediaQuery,
} from '@mui/material';
import { CheckOutlined, DeleteOutlined, EyeInvisibleOutlined, SyncOutlined } from '@ant-design/icons';
import * as API from '../../api';
import MainCard from '../../components/MainCard';
import { zoneName, formatTime } from '../../utils/auditFilters';
import {
  LEVELS, LEVEL_TEXT, MAX_Q, PAGE_SIZE, SOURCES, SOURCE_TEXT, TEXT, apiParams, cleanFilters, cleanItems, deleteBody, deleteReadBody,
  detailsLink, filtersFromSearch, hasFilters, markAllBody, markBody, mergePage, messageOf, occurrence, searchFromFilters, titleOf,
} from '../../utils/notifications';
import { LevelIcon, SourceChip, SubjectChip, When, wrapAnywhere } from './parts';

function useDebounced(value, ms) {
  const [debounced, setDebounced] = React.useState(value);
  React.useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), ms);
    return () => clearTimeout(timer);
  }, [value, ms]);
  return debounced;
}

const touch = { minWidth: { xs: 44, sm: 36 }, minHeight: { xs: 44, sm: 36 } };

export const NotificationCard = ({ item, onRead, onDelete }) => {
  const { t } = useTranslation();
  const title = titleOf(item, t);
  const message = messageOf(item, t);
  const repeat = occurrence(item, item.firstSeen ? formatTime(item.firstSeen) : '');
  const link = detailsLink(item);
  return <Box data-notification={item.id} sx={{
    border: '1px solid var(--ct-border)', borderRadius: 'var(--ct-radius-l)', bgcolor: 'var(--ct-surface-2)', p: { xs: 1.5, sm: 2 }, minWidth: 0,
    borderLeft: item.read ? '1px solid var(--ct-border)' : '4px solid', borderLeftColor: item.read ? 'var(--ct-border)' : item.level + '.main',
  }}>
    <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ alignItems: { xs: 'stretch', sm: 'center' }, minWidth: 0 }}>
    <Stack direction="row" spacing={1.5} sx={{ alignItems: 'flex-start', minWidth: 0, flex: 1 }}>
      <LevelIcon level={item.level} />
      <Box sx={{ flex: 1, minWidth: 0 }}>
        <Stack direction="row" spacing={1} useFlexGap sx={{ flexWrap: 'wrap', alignItems: 'center' }}>
          <Link component={RouterLink} to={link} underline="hover" color="inherit" variant={item.read ? 'subtitle1' : 'h5'} sx={{ ...wrapAnywhere, fontWeight: item.read ? 500 : 700, minHeight: { xs: 44, sm: 0 }, display: 'inline-flex', alignItems: 'center' }}>
            {title}
          </Link>
          {!item.read && <Chip size="small" color="primary" label={t(...TEXT.unread)} />}
          {item.resolved && <Chip size="small" color="success" variant="outlined" label={t(...TEXT.resolved)} />}
          {item.count > 1 && <Chip size="small" variant="outlined" label={'×' + item.count} />}
        </Stack>
        {message && <Typography variant="body2" sx={{ mt: 0.5, whiteSpace: 'pre-wrap', ...wrapAnywhere }}>{message}</Typography>}
        <Stack direction="row" spacing={1} useFlexGap sx={{ flexWrap: 'wrap', alignItems: 'center', mt: 1 }}>
          <SourceChip source={item.source} />
          <SubjectChip subject={item.subject} />
          <When value={item.date} />
        </Stack>
        {repeat && <Typography variant="caption" color="textSecondary" component="div" sx={{ mt: 0.5 }}>
          {t(repeat[0], repeat[1], repeat[2])}
        </Typography>}
      </Box>
    </Stack>
    <Stack direction="row" spacing={1} useFlexGap sx={{ flexWrap: 'nowrap', justifyContent: 'flex-end', flexShrink: 0 }}>
      <Button component={RouterLink} to={link} size="small" variant="outlined" sx={touch}>{t(...TEXT.open)}</Button>
      <Tooltip title={t(...(item.read ? TEXT.markUnread : TEXT.markRead))}>
        <IconButton size="small" aria-label={t(...(item.read ? TEXT.markUnread : TEXT.markRead))} onClick={() => onRead(item, !item.read)} sx={touch}>
          {item.read ? <EyeInvisibleOutlined /> : <CheckOutlined />}
        </IconButton>
      </Tooltip>
      <Tooltip title={t(...TEXT.delete)}>
        <IconButton size="small" aria-label={t(...TEXT.delete)} onClick={() => onDelete(item)} sx={touch}><DeleteOutlined /></IconButton>
      </Tooltip>
    </Stack>
    </Stack>
  </Box>;
};

const Confirm = ({ open, title, text, onCancel, onConfirm }) => {
  const { t } = useTranslation();
  return <Dialog open={open} onClose={onCancel} fullWidth maxWidth="xs">
    <DialogTitle>{title}</DialogTitle>
    <DialogContent><Typography variant="body2">{text}</Typography></DialogContent>
    <DialogActions>
      <Button onClick={onCancel}>{t(...TEXT.cancel)}</Button>
      <Button color="error" variant="contained" onClick={onConfirm}>{t(...TEXT.delete)}</Button>
    </DialogActions>
  </Dialog>;
};

const NotificationsPage = () => {
  const { t } = useTranslation();
  const narrow = useMediaQuery((theme) => theme.breakpoints.down('sm'));
  const size = narrow ? 'medium' : 'small';
  const location = useLocation();
  const navigate = useNavigate();
  const filters = React.useMemo(() => filtersFromSearch(location.search), [location.search]);
  const setFilters = (changes) => navigate({ pathname: location.pathname, search: searchFromFilters(cleanFilters({ ...filters, ...changes })) }, { replace: true });

  const [qText, setQText] = React.useState(filters.q);
  const qDebounced = useDebounced(qText, 500);
  React.useEffect(() => { if (cleanFilters({ q: qText }).q !== filters.q) setQText(filters.q); }, [filters.q]);
  React.useEffect(() => { const q = cleanFilters({ q: qDebounced }).q; if (q !== filters.q) setFilters({ q }); }, [qDebounced]);

  const [items, setItems] = React.useState(null);
  const [next, setNext] = React.useState('');
  const [unread, setUnread] = React.useState(0);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState('');
  const [confirm, setConfirm] = React.useState(null);
  const request = React.useRef(0);
  const store = React.useRef(''); // Fork addition (plan 51): the monitoring store the ids were listed from

  const load = (cursor) => {
    const id = ++request.current;
    setLoading(true);
    return API.notifications.list(apiParams(filters, cursor, PAGE_SIZE), true).then((res) => {
      if (id !== request.current) return;
      const data = res.data || {};
      if (!cursor) store.current = typeof res.store === 'string' ? res.store : '';
      setItems((shown) => (cursor ? mergePage(shown, data.items) : cleanItems(data.items)));
      setNext(typeof data.next === 'string' ? data.next : '');
      setUnread(Number(data.unread) || 0);
      setError('');
    }).catch((e) => {
      if (id === request.current) setError(e.message || String(e));
    }).finally(() => {
      if (id === request.current) setLoading(false);
    });
  };

  React.useEffect(() => { load(''); }, [filters.level, filters.source, filters.read, filters.q]);

  const markOne = (item, read) => {
    API.notifications.mark(markBody([item.id], read, store.current)).then(() => {
      setItems((shown) => (shown || []).map((n) => (n.id === item.id ? { ...n, read } : n)));
      setUnread((u) => Math.max(0, u + (read ? -1 : 1)));
    }).catch((e) => { if (e && e.code === 'NOTIF009') load(''); }); // the store changed: the list again
  };
  const markAll = () => API.notifications.mark(markAllBody()).then(() => load('')).catch(() => {});
  const doDelete = () => {
    const what = confirm;
    setConfirm(null);
    const body = what === 'read' ? deleteReadBody() : deleteBody([what.id], store.current);
    API.notifications.remove(body).then(() => {
      if (what === 'read') load('');
      else setItems((shown) => (shown || []).filter((n) => n.id !== what.id));
    }).catch((e) => { if (e && e.code === 'NOTIF009') load(''); }); // Fork addition (plan 51): the store changed: the list again
  };

  return (
    <Stack spacing={2} sx={{ maxWidth: '1200px', margin: 'auto', minWidth: 0 }}>
    <MainCard title={t(...TEXT.pageTitle)}>
      <Stack spacing={2}>
        <Typography variant="body2">{t(...TEXT.intro)}</Typography>
        <Typography variant="caption" color="textSecondary">{t(TEXT.zone[0], TEXT.zone[1], { zone: zoneName() })}</Typography>
        <Box sx={{ display: 'grid', gap: 2, gridTemplateColumns: { xs: 'minmax(0, 1fr)', sm: 'repeat(2, minmax(0, 1fr))', md: 'repeat(4, minmax(0, 1fr))' } }}>
              <TextField
                select
                fullWidth
                size={size}
                label={t(...TEXT.filterLevel)}
                value={filters.level}
                onChange={(e) => setFilters({ level: e.target.value })}
                slotProps={{
                  select: { displayEmpty: true },
                  inputLabel: { shrink: true }
                }}>
              <MenuItem value="">{t(...TEXT.filterAny)}</MenuItem>
              {LEVELS.map((l) => <MenuItem key={l} value={l}>{t(...LEVEL_TEXT[l])}</MenuItem>)}
            </TextField>
              <TextField
                select
                fullWidth
                size={size}
                label={t(...TEXT.filterSource)}
                value={filters.source}
                onChange={(e) => setFilters({ source: e.target.value })}
                slotProps={{
                  select: { displayEmpty: true },
                  inputLabel: { shrink: true }
                }}>
              <MenuItem value="">{t(...TEXT.filterAny)}</MenuItem>
              {SOURCES.map(([id]) => <MenuItem key={id} value={id}>{t(...SOURCE_TEXT[id])}</MenuItem>)}
            </TextField>
              <TextField
                select
                fullWidth
                size={size}
                label={t(...TEXT.filterRead)}
                value={filters.read}
                onChange={(e) => setFilters({ read: e.target.value })}
                slotProps={{
                  select: { displayEmpty: true },
                  inputLabel: { shrink: true }
                }}>
              <MenuItem value="">{t(...TEXT.filterAny)}</MenuItem>
              <MenuItem value="unread">{t(...TEXT.filterUnread)}</MenuItem>
              <MenuItem value="read">{t(...TEXT.filterReadOnly)}</MenuItem>
            </TextField>
            <TextField fullWidth size={size} label={t(...TEXT.filterText)} value={qText} onChange={(e) => setQText(e.target.value)}
                placeholder={t(...TEXT.filterTextPlaceholder)} slotProps={{
                htmlInput: { maxLength: MAX_Q }
              }} />
        </Box>
        <Stack direction="row" spacing={1} useFlexGap sx={{ flexWrap: 'wrap', alignItems: 'center' }}>
          <Button variant="outlined" startIcon={<SyncOutlined />} onClick={() => load('')}>{t(...TEXT.refresh)}</Button>
          <Button variant="outlined" startIcon={<CheckOutlined />} disabled={unread === 0} onClick={markAll}>{t(...TEXT.markAllRead)}</Button>
          <Button variant="outlined" color="error" startIcon={<DeleteOutlined />} onClick={() => setConfirm('read')}>{t(...TEXT.deleteRead)}</Button>
          {hasFilters(filters) && <Button onClick={() => { setQText(''); navigate({ pathname: location.pathname, search: '' }, { replace: true }); }}>{t(...TEXT.filterClear)}</Button>}
          {unread > 0 && <Typography variant="body2" color="textSecondary">{t(TEXT.unreadCount[0], TEXT.unreadCount[1], { count: unread })}</Typography>}
        </Stack>
      </Stack>
    </MainCard>

    {error && <Alert severity="error" sx={wrapAnywhere}>{t(TEXT.loadFailed[0], TEXT.loadFailed[1], { error, interpolation: { escapeValue: false } })}</Alert>}

    <Stack spacing={1.5} sx={{ minWidth: 0 }} data-notifications-list>
      {items === null && loading && <Box sx={{ textAlign: 'center', py: 4 }}><CircularProgress /></Box>}
      {items && items.length === 0 && <MainCard><Typography variant="body1" color="textSecondary">
        {t(...(hasFilters(filters) ? TEXT.emptyFiltered : TEXT.empty))}
      </Typography></MainCard>}
      {(items || []).map((item) => <NotificationCard key={item.id} item={item} onRead={markOne} onDelete={(n) => setConfirm(n)} />)}
      {next && <Box sx={{ textAlign: 'center' }}>
        <Button variant="outlined" disabled={loading} onClick={() => load(next)} startIcon={loading ? <CircularProgress size={14} /> : null}>{t(...TEXT.loadMore)}</Button>
      </Box>}
    </Stack>

    <Confirm open={confirm === 'read'} title={t(...TEXT.deleteReadTitle)} text={t(...TEXT.deleteReadText)} onCancel={() => setConfirm(null)} onConfirm={doDelete} />
    <Confirm open={!!confirm && confirm !== 'read'} title={t(...TEXT.deleteOneTitle)} text={t(...TEXT.deleteOneText)} onCancel={() => setConfirm(null)} onConfirm={doDelete} />
    </Stack>
  );
};

export default NotificationsPage;

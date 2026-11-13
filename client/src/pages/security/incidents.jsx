// Fork addition: the security dashboard's incident list. Newest first,
// filtered on the server (group, severity, address, text), a page at a time;
// repeated events are one row; a row opens everything the record holds.
// Every value is data from a request: it is rendered as text, never as
// markup and never as a link.
import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import dayjs from 'dayjs';
import {
  Alert, Box, Button, Chip, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle,
  MenuItem, Stack, Table, TableBody, TableCell, TableRow, TextField, Tooltip, Typography, useMediaQuery,
} from '@mui/material';

import * as API from '../../api';
import MainCard from '../../components/MainCard';
import { GROUP_ORDER, STOPPED_TEXT, groupLabel, groupRepeats, kindText, shorten } from '../../utils/securityDashboard';

const PAGE_SIZE = 50;
// shown in one dialog; the rest is counted
const DIALOG_EVENTS = 50;

const SEVERITY_ALERT = { high: 'error', medium: 'warning', low: 'info', info: 'info' };

const looksLikeAddress = (value) => /^[0-9a-fA-F:.]{2,45}$/.test(value);

const Cut = ({ value, max = 60 }) => {
  const s = shorten(value, max);
  if (!s.cut) return s.text;
  return <Tooltip title={<span style={{ overflowWrap: 'anywhere' }}>{s.full}</span>}><span>{s.text}</span></Tooltip>;
};

const Field = ({ label, value }) => {
  if (value === undefined || value === null || value === '') return null;
  return <TableRow>
    <TableCell component="th" sx={{ fontWeight: 600, verticalAlign: 'top', width: '35%', pl: 0 }}>{label}</TableCell>
    <TableCell sx={{ overflowWrap: 'anywhere', wordBreak: 'break-word', whiteSpace: 'pre-wrap', pr: 0 }}>{String(value)}</TableCell>
  </TableRow>;
};

const EventFields = ({ event, t }) => {
  const details = event.details && typeof event.details === 'object' ? Object.entries(event.details) : [];
  return <Table size="small" sx={{ tableLayout: 'fixed', width: '100%' }}>
    <TableBody>
      <Field label={t('security.field.time', 'Time')} value={dayjs(event.time).format('L LTS')} />
      <Field label={t('security.field.kind', 'Kind')} value={event.kind} />
      <Field label={t('security.field.severity', 'Severity')} value={event.severity} />
      <Field label={t('security.field.outcome', 'Outcome')} value={event.outcome} />
      <Field label={t('security.field.ip', 'Address')} value={event.ip} />
      <Field label={t('security.field.actor', 'Signed-in user')} value={event.actor} />
      <Field label={t('security.field.target', 'About (as sent)')} value={event.target} />
      <Field label={t('security.field.host', 'Server name asked for')} value={event.host} />
      <Field label={t('security.field.method', 'Method')} value={event.method} />
      <Field label={t('security.field.path', 'Path asked for')} value={event.path} />
      <Field label={t('security.field.count', 'Events')} value={event.count > 1 ? t('security.field.countSummary', '{{count}} (a summary of events that were not stored one by one)', { count: event.count }) : event.count} />
      {details.map(([key, value]) => <Field key={key} label={key} value={value} />)}
      <Field label={t('security.field.id', 'Record number')} value={event.id} />
    </TableBody>
  </Table>;
};

const Details = ({ row, catalogue, onClose }) => {
  const { t } = useTranslation();
  const text = kindText(row.kind, catalogue);
  const shown = row.events.slice(0, DIALOG_EVENTS);
  const stopped = STOPPED_TEXT[text.stopped];
  return <Dialog open onClose={onClose} fullWidth maxWidth="md" scroll="paper">
    <DialogTitle>{t(...text.label)}{row.count > 1 ? ' ×' + row.count : ''}</DialogTitle>
    <DialogContent dividers>
      <Stack spacing={2}>
        <Alert severity={SEVERITY_ALERT[row.severity] || 'info'}>
          <Stack spacing={1}>
            <span>{t(...text.what)}</span>
            {stopped && <span><strong>{t(...stopped)}.</strong></span>}
            {text.todo && <span>{t(...text.todo)}</span>}
            {text.link && <span><Button component={Link} to={text.link} size="small" variant="outlined">
              {t('security.details.open', 'Open the settings')}
            </Button></span>}
          </Stack>
        </Alert>
        {shown.map((event, i) => <Box key={event.id}>
          {shown.length > 1 && <Typography variant="h6" sx={{ mb: 0.5 }}>
            {t('security.details.event', 'Event {{n}} of {{total}}', { n: i + 1, total: row.events.length })}
          </Typography>}
          <EventFields event={event} t={t} />
        </Box>)}
        {row.events.length > shown.length && <Typography color="textSecondary">
          {t('security.details.more', '{{count}} more like these are in the list.', { count: row.events.length - shown.length })}
        </Typography>}
      </Stack>
    </DialogContent>
    <DialogActions>
      <Button onClick={onClose}>{t('global.close')}</Button>
    </DialogActions>
  </Dialog>;
};

const RowCard = ({ row, catalogue, onOpen }) => {
  const { t } = useTranslation();
  const text = kindText(row.kind, catalogue);
  const first = row.events[0];
  const where = [first.ip, first.actor && t('security.list.user', 'user {{name}}', { name: first.actor })].filter(Boolean);
  const about = first.target || first.path || first.host;
  const stopped = STOPPED_TEXT[text.stopped];
  return <Box
    role="button"
    tabIndex={0}
    onClick={onOpen}
    onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onOpen(); } }}
    sx={{ cursor: 'pointer', borderRadius: 1, '&:focus-visible': { outline: 2, outlineColor: 'primary.main' } }}
  >
    <Alert severity={SEVERITY_ALERT[row.severity] || 'info'} sx={{ '& .MuiAlert-message': { minWidth: 0, flex: 1 } }}>
      <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 1 }}>
        <span style={{ fontWeight: 'bold' }}>{t(...text.label)}</span>
        {row.count > 1 && <Chip size="small" variant="outlined" label={'×' + row.count} />}
        {stopped && <Chip size="small" variant="outlined" color={text.stopped === 'yes' ? 'success' : 'warning'} label={t(...stopped)} />}
      </Box>
      <div>
        {row.events.length > 1 && row.first !== row.last
          ? dayjs(row.first).format('L LTS') + ' – ' + dayjs(row.last).format('LTS')
          : dayjs(row.last).format('L LTS')}
        {' · '}{dayjs(row.last).fromNow()}
      </div>
      {(where.length > 0 || about) && <div style={{ overflowWrap: 'anywhere' }}>
        {where.join(' · ')}{where.length > 0 && about ? ' · ' : ''}{about && <Cut value={about} />}
      </div>}
    </Alert>
  </Box>;
};

const Incidents = forwardRef(({ catalogue, from }, ref) => {
  const { t } = useTranslation();
  const narrow = useMediaQuery((theme) => theme.breakpoints.down('sm'));
  const [group, setGroup] = useState('');
  const [severity, setSeverity] = useState('');
  const [ip, setIP] = useState('');
  const [text, setText] = useState('');
  const [debounced, setDebounced] = useState({ ip: '', text: '' });
  const [events, setEvents] = useState(null);
  const [cursor, setCursor] = useState(0);
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);
  const [open, setOpen] = useState(null);
  const request = useRef(0);
  const box = useRef(null);
  const pages = useRef(1);
  // the start of the period, as the server computed it; refreshed with the page
  const since = useRef(from);

  const ipValid = debounced.ip === '' || looksLikeAddress(debounced.ip);

  const load = (fromCursor) => {
    if (!ipValid) return Promise.resolve();
    const id = ++request.current;
    setLoading(true);
    return API.security.incidents({
      group, severity, ip: debounced.ip, q: debounced.text, from: since.current || undefined, cursor: fromCursor || undefined, limit: PAGE_SIZE,
    }).then((res) => {
      if (id !== request.current) return;
      const page = res.data || {};
      setEvents((previous) => (fromCursor ? [...(previous || []), ...(page.events || [])] : (page.events || [])));
      setCursor(page.nextCursor || 0);
      pages.current = fromCursor ? pages.current + 1 : 1;
      setFailed(false);
    }).catch(() => {
      if (id === request.current) setFailed(true);
    }).finally(() => {
      if (id === request.current) setLoading(false);
    });
  };

  useImperativeHandle(ref, () => ({
    // called by the page's refresh: only while the first page is shown and
    // nothing is open, so that reading is not disturbed
    refresh: (start) => {
      if (pages.current !== 1 || open) return;
      since.current = start || since.current;
      load(0);
    },
    showAddress: (address) => {
      setIP(address);
      setDebounced((d) => ({ ...d, ip: address }));
      if (box.current && box.current.scrollIntoView) box.current.scrollIntoView({ behavior: 'smooth', block: 'start' });
    },
  }));

  useEffect(() => {
    const handler = setTimeout(() => {
      const q = text.trim();
      setDebounced({ ip: ip.trim(), text: q.length === 1 ? '' : q });
    }, 500);
    return () => clearTimeout(handler);
  }, [ip, text]);

  useEffect(() => {
    since.current = from;
    if (from) load(0);
  }, [group, severity, debounced.ip, debounced.text, from]);

  const rows = groupRepeats(events || []);
  const filtered = group || severity || debounced.ip || debounced.text;

  return <div ref={box}>
    {open && <Details row={open} catalogue={catalogue} onClose={() => setOpen(null)} />}
    <Typography variant="h5">{t('security.list.title', 'Incidents')}</Typography>
    <Typography variant="body2" color="textSecondary" sx={{ mb: 1.5 }}>
      {t('security.list.help', 'Newest first, in the period chosen above. Repeats from the same address are folded into one row; click a row for everything that was recorded.')}
    </Typography>
    <Stack direction={narrow ? 'column' : 'row'} spacing={2} sx={{ mb: 2 }} useFlexGap flexWrap="wrap">
      <TextField select size="small" label={t('security.list.group', 'What')} value={group} onChange={(e) => setGroup(e.target.value)} sx={{ minWidth: 180 }}>
        <MenuItem value="">{t('security.list.allGroups', 'Everything')}</MenuItem>
        {GROUP_ORDER.map((id) => <MenuItem key={id} value={id}>{t(...groupLabel(id))}</MenuItem>)}
      </TextField>
      <TextField select size="small" label={t('security.list.severity', 'Severity')} value={severity} onChange={(e) => setSeverity(e.target.value)} sx={{ minWidth: 160 }}>
        <MenuItem value="">{t('security.list.anySeverity', 'Any')}</MenuItem>
        <MenuItem value="medium">{t('security.list.mediumUp', 'Medium and high')}</MenuItem>
        <MenuItem value="high">{t('security.list.highOnly', 'High only')}</MenuItem>
      </TextField>
      <TextField size="small" label={t('security.list.address', 'Address')} value={ip} onChange={(e) => setIP(e.target.value)}
        error={!ipValid} helperText={!ipValid ? t('security.list.badAddress', 'Not an IP address') : undefined}
        inputProps={{ maxLength: 45, spellCheck: false }} sx={{ minWidth: 180 }} />
      <TextField size="small" label={t('security.list.text', 'Text')} value={text} onChange={(e) => setText(e.target.value)}
        placeholder={t('security.list.textPlaceholder', 'user name, path, server name…')}
        inputProps={{ maxLength: 100, spellCheck: false }} sx={{ flex: 1, minWidth: 180 }} />
      {filtered && <Button onClick={() => { setGroup(''); setSeverity(''); setIP(''); setText(''); setDebounced({ ip: '', text: '' }); }}>
        {t('security.list.clear', 'Clear the filters')}
      </Button>}
    </Stack>

    {failed && <Alert severity="warning" sx={{ mb: 2 }}>{t('security.list.failed', 'The incidents could not be read just now. The page tries again at the next refresh.')}</Alert>}

    {events === null && loading && <Box sx={{ textAlign: 'center', p: 3 }}><CircularProgress /></Box>}

    {events !== null && rows.length === 0 && <MainCard>
      <Typography color="textSecondary" sx={{ textAlign: 'center' }}>
        {filtered
          ? t('security.list.noneMatch', 'No incident matches these filters.')
          : t('security.list.none', 'No incidents recorded yet. Failed sign-ins, scans, blocks and bans appear here as they happen.')}
      </Typography>
    </MainCard>}

    {rows.length > 0 && <Stack spacing={1}>
      {rows.map((row) => <RowCard key={row.key} row={row} catalogue={catalogue} onOpen={() => setOpen(row)} />)}
      {cursor > 0 && <MainCard>
        <Button variant="contained" fullWidth disabled={loading} onClick={() => load(cursor)}>
          {loading ? <CircularProgress size={20} /> : t('security.list.more', 'Show older incidents')}
        </Button>
      </MainCard>}
    </Stack>}
  </div>;
});

export default Incidents;

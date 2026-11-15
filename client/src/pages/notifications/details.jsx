// Fork addition: one notification with everything that helps to understand
// it: what happened (then the technical text in full), what it means, what
// Cosmos did, what you can do, the state of the thing right now, the earlier
// occurrences, and for administrators the security record, the Monitoring
// events and the server's log lines around that moment. Its own address
// (/cosmos-ui/notifications/<id>, or /r/<ref> from a Telegram message).
import * as React from 'react';
import { Link as RouterLink, useParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import {
  Alert, Box, Button, Chip, CircularProgress, Divider, Link, Stack, Table, TableBody, TableCell, TableHead, TableRow, Typography, useMediaQuery,
} from '@mui/material';
import { ArrowLeftOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import * as API from '../../api';
import MainCard from '../../components/MainCard';
import { useClientInfos } from '../../utils/hooks';
import { PERM_ADMIN_READ } from '../../utils/permissions';
import { formatTime, zoneName } from '../../utils/auditFilters';
import { auditSentence } from '../../utils/auditSentences';
import { notify } from '../../utils/activity';
import EventDetails from '../securityAudit/eventDetails';
import {
  LEVEL_TEXT, TEXT, auditLink, cleanItem, knownAction, markBody, messageOf, ordinal, safeLink, sentence, tableOf,
  timeline, titleOf, detailsLink,
} from '../../utils/notifications';
import { Field, LevelIcon, RawText, SourceChip, SubjectChip, When, wrapAnywhere } from './parts';

const touch = { minHeight: { xs: 44, sm: 36 } };

const Section = ({ title, children, ...rest }) => <MainCard title={title} sx={{ minWidth: 0 }} {...rest}>{children}</MainCard>;

const Step = ({ step, t }) => {
  const [busy, setBusy] = React.useState(false);
  const text = sentence(step, t);
  const link = safeLink(step.link);
  const label = step.label ? sentence(step.label, t) : '';
  const run = () => {
    if (step.action === 'upnp.check') {
      setBusy(true);
      API.config.upnpRetry().then((res) => {
        const st = (res && res.data) || {};
        const failing = st.error || ((st.mappings || []).find((m) => m.state === 'failed' && m.error) || {}).error;
        if (failing || st.paused) notify(t(TEXT.checkFailed[0], TEXT.checkFailed[1], { error: failing || '', interpolation: { escapeValue: false } }), 'error');
        else notify(t(...TEXT.checkDone), 'success');
      }).catch(() => {}).finally(() => setBusy(false));
    }
    if (step.action === 'reach.check') { // Fork addition: the watch from the internet tests now (src/reachwatch)
      setBusy(true);
      API.reachWatch.check().then((res) => {
        notify(res && res.data && res.data.started ? t('reachWatch.checkStarted', 'Testing through Cloudflare now: the result shows here within a minute.') : t('reachWatch.tooSoon', 'A test is running or ran moments ago: try again in a few seconds.'), 'info');
      }).catch(() => {}).finally(() => setBusy(false));
    }
  };
  return <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ alignItems: { xs: 'stretch', sm: 'center' }, justifyContent: 'space-between', py: 0.75 }}>
    <Typography variant="body2" sx={{ ...wrapAnywhere, flex: 1 }}>{text}</Typography>
    {knownAction(step.action) && <Button variant="contained" onClick={run} disabled={busy} sx={{ ...touch, flexShrink: 0 }}
      startIcon={busy ? <CircularProgress size={14} /> : null}>{label || t(...TEXT.checkNow)}</Button>}
    {!knownAction(step.action) && link && <Button component={RouterLink} to={link} variant="outlined" sx={{ ...touch, flexShrink: 0, ...wrapAnywhere }}>
      {label || t(...TEXT.openLink)}
    </Button>}
  </Stack>;
};

const Facts = ({ facts, t }) => {
  if (!Array.isArray(facts) || facts.length === 0) return null;
  return <Box>{facts.map((f, i) => <Field key={i} label={sentence({ key: f.key, text: f.text }, t)} value={f.value} />)}</Box>;
};

const StateTable = ({ state, t }) => {
  const narrow = useMediaQuery((theme) => theme.breakpoints.down('sm'));
  const table = tableOf(state);
  if (!table || table.rows.length === 0) return null;
  const header = table.header.map((h) => sentence(h, t));
  if (narrow) {
    return <Stack spacing={1}>{table.rows.map((row, i) => <Box key={i} sx={{ border: '1px solid var(--ct-border)', borderRadius: 'var(--ct-radius-s)', p: 1 }}>
      {row.map((cell, j) => <Field key={j} label={header[j]} value={cell} />)}
    </Box>)}</Stack>;
  }
  return <Box sx={{ overflowX: 'auto' }}><Table size="small" sx={{ tableLayout: 'fixed', width: '100%' }}>
    <TableHead><TableRow>{header.map((h, i) => <TableCell key={i}>{h}</TableCell>)}</TableRow></TableHead>
    <TableBody>{table.rows.map((row, i) => <TableRow key={i}>{row.map((cell, j) => <TableCell key={j} sx={wrapAnywhere}>{cell}</TableCell>)}</TableRow>)}</TableBody>
  </Table></Box>;
};

const StateNow = ({ context, t }) => {
  if (!context) return <Typography variant="body2" color="textSecondary"><CircularProgress size={14} sx={{ mr: 1 }} />{t(...TEXT.stateLoading)}</Typography>;
  const state = context.state;
  if (!state || (!state.sentence && !(state.facts || []).length && !(state.rows || []).length)) {
    return <Typography variant="body2" color="textSecondary">{t(...TEXT.stateNone)}</Typography>;
  }
  const severity = state.ok === true ? 'success' : state.ok === false ? 'error' : 'info';
  const verdict = state.ok === true ? TEXT.stateOk : state.ok === false ? TEXT.stateBad : TEXT.stateUnknown;
  const link = safeLink(state.link);
  return <Stack spacing={1.5}>
    <Alert severity={severity} sx={{ '& .MuiAlert-message': wrapAnywhere }}>
      <strong>{t(...verdict)}.</strong>{state.sentence ? ' ' + sentence(state.sentence, t) : ''}
    </Alert>
    <Facts facts={state.facts} t={t} />
    <StateTable state={state} t={t} />
    {link && <Box><Button component={RouterLink} to={link} variant="outlined" sx={touch}>{t(...TEXT.openLink)}</Button></Box>}
  </Stack>;
};

const History = ({ item, details, t }) => {
  const entries = timeline(item, details);
  const since = item.firstSeen || item.date;
  return <Stack spacing={1}>
    <Typography variant="body2">
      {item.count > 1
        ? t(TEXT.historyMany[0], TEXT.historyMany[1], { ordinal: ordinal(item.count), since: formatTime(since) + ' (' + dayjs(since).fromNow() + ')' })
        : t(...TEXT.historyOnce)}
    </Typography>
    <Box component="ol" sx={{ listStyle: 'none', m: 0, p: 0, borderLeft: '2px solid var(--ct-border-strong)', ml: 1 }}>
      {entries.map((e, i) => <Box component="li" key={i} sx={{ position: 'relative', pl: 2, py: 0.5,
        '&::before': { content: '""', position: 'absolute', left: -6, top: 12, width: 10, height: 10, borderRadius: '50%',
          bgcolor: e.type === 'resolved' ? 'success.main' : e.type === 'related' ? 'info.main' : item.level + '.main' } }}>
        <Typography variant="body2" component="div" sx={wrapAnywhere}>
          <strong>{formatTime(e.at)}</strong>{' · '}{dayjs(e.at).fromNow()}
        </Typography>
        <Typography variant="body2" color="textSecondary" component="div" sx={wrapAnywhere}>
          {e.item
            ? <Link component={RouterLink} to={detailsLink(e.item)}>{titleOf(e.item, t)}</Link>
            : t(...TEXT.historyOccurrence)}
        </Typography>
      </Box>)}
    </Box>
  </Stack>;
};

const SecurityList = ({ part, subject, t }) => {
  const [open, setOpen] = React.useState(null);
  if (!part || part.available === false) return null;
  const events = Array.isArray(part.events) ? part.events : [];
  return <Stack spacing={1}>
    {events.length === 0 && <Typography variant="body2" color="textSecondary">{t(...TEXT.securityNone)}</Typography>}
    {events.map((e, i) => <Box key={e.id || i} role="button" tabIndex={0} onClick={() => setOpen(e)}
      onKeyDown={(k) => { if (k.key === 'Enter' || k.key === ' ') { k.preventDefault(); setOpen(e); } }}
      sx={{ cursor: 'pointer', py: 0.75, px: 1, minHeight: 44, borderRadius: 'var(--ct-radius-s)', '&:hover': { bgcolor: 'var(--ct-hover)' }, '&:focus-visible': { outline: 2, outlineColor: 'primary.main' } }}>
      <Typography variant="body2" sx={wrapAnywhere}>{auditSentence(e, t)}</Typography>
      <Typography variant="caption" color="textSecondary">{formatTime(e.time)}{e.outcome ? ' · ' + e.outcome : ''}</Typography>
    </Box>)}
    <Box><Button component={RouterLink} to={auditLink(subject && subject.name)} variant="outlined" sx={touch}>{t(...TEXT.securityOpen)}</Button></Box>
    <EventDetails event={open} onClose={() => setOpen(null)} />
  </Stack>;
};

const EventsList = ({ part, t }) => {
  if (!part || part.available === false) return null;
  const events = Array.isArray(part.events) ? part.events : [];
  return <Stack spacing={1}>
    {events.length === 0 && <Typography variant="body2" color="textSecondary">{t(...TEXT.eventsNone)}</Typography>}
    {events.map((e, i) => <Box key={e.id || i} sx={{ py: 0.5 }}>
      <Typography variant="body2" sx={wrapAnywhere}>{String(e.label || e.eventId || '')}</Typography>
      <Typography variant="caption" color="textSecondary" sx={wrapAnywhere}>{formatTime(e.date)}{e.level ? ' · ' + e.level : ''}{e.object ? ' · ' + e.object : ''}</Typography>
    </Box>)}
    <Box><Button component={RouterLink} to="/cosmos-ui/monitoring" variant="outlined" sx={touch}>{t(...TEXT.eventsOpen)}</Button></Box>
  </Stack>;
};

const LogLines = ({ part, t }) => {
  if (!part) return null;
  const lines = Array.isArray(part.lines) ? part.lines.map(String) : [];
  if (part.available === false || lines.length === 0) {
    return <Typography variant="body2" color="textSecondary" sx={wrapAnywhere}>{part.reason ? sentence(part.reason, t) : t(...TEXT.logNone)}</Typography>;
  }
  return <Stack spacing={1}>
    <Typography variant="body2" color="textSecondary">{t(...TEXT.logNote)}</Typography>
    <RawText text={lines.join('\n')} label={t(...TEXT.log)} />
    {part.truncated && <Typography variant="caption" color="textSecondary">{t(...TEXT.logTruncated)}</Typography>}
  </Stack>;
};

const NotificationDetails = () => {
  const { t } = useTranslation();
  const { id, ref } = useParams();
  const { hasPermission } = useClientInfos();
  const isAdmin = hasPermission(PERM_ADMIN_READ);
  const [details, setDetails] = React.useState(null);
  const [missing, setMissing] = React.useState(false);
  const [context, setContext] = React.useState(null);
  const [contextError, setContextError] = React.useState('');

  React.useEffect(() => {
    setDetails(null);
    setMissing(false);
    setContext(null);
    setContextError('');
    const params = ref ? { ref: /^[0-9a-f]{8,64}$/.test(ref) ? ref : '' } : { id: /^\d{1,18}$/.test(id || '') ? Number(id) : 0 };
    if (!params.ref && !params.id) { setMissing(true); return; }
    let gone = false;
    API.notifications.item(params).then((res) => {
      if (gone) return;
      const item = cleanItem(res.data);
      if (!item) { setMissing(true); return; }
      setDetails({ ...res.data, ...item });
      if (!item.read) API.notifications.mark(markBody([item.id], true)).catch(() => {});
      if (isAdmin) {
        API.notifications.context(item.id).then((c) => { if (!gone) setContext(c.data || {}); })
          .catch((e) => { if (!gone) { setContext({}); if (e && !e.refused) setContextError(e.message || String(e)); } });
      }
    }).catch(() => { if (!gone) setMissing(true); });
    return () => { gone = true; };
  }, [id, ref, isAdmin]);

  const back = <Box><Button component={RouterLink} to="/cosmos-ui/notifications" startIcon={<ArrowLeftOutlined />} sx={touch}>{t(...TEXT.back)}</Button></Box>;

  if (missing) return <Stack spacing={2} sx={{ maxWidth: '1000px', margin: 'auto' }}>{back}<Alert severity="info">{t(...TEXT.notFound)}</Alert></Stack>;
  if (!details) return <Box sx={{ textAlign: 'center', py: 6 }}><CircularProgress /></Box>;

  const item = details;
  const title = titleOf(item, t);
  const what = sentence(item.what, t) || messageOf(item, t);
  const raw = typeof item.raw === 'string' ? item.raw : '';
  const means = sentence(item.means, t);
  const doing = sentence(item.doing, t);
  const steps = Array.isArray(item.steps) ? item.steps : [];
  const link = safeLink(item.link);
  const subjectLink = item.subject && item.subject.link;

  return <Stack spacing={2} sx={{ maxWidth: '1000px', margin: 'auto', minWidth: 0 }} data-notification-details={item.id}>
    {back}
    <MainCard sx={{ minWidth: 0 }}>
      <Stack spacing={1.5}>
        <Stack direction="row" spacing={1.5} sx={{ alignItems: 'flex-start' }}>
          <LevelIcon level={item.level} size={40} />
          <Box sx={{ minWidth: 0, flex: 1 }}>
            <Typography variant="h3" component="h1" sx={wrapAnywhere}>{title}</Typography>
            <Stack direction="row" spacing={1} useFlexGap sx={{ flexWrap: 'wrap', alignItems: 'center', mt: 1 }}>
              <Chip size="small" color={item.level} variant="outlined" label={t(...LEVEL_TEXT[item.level])} />
              {item.resolved
                ? <Chip size="small" color="success" label={t(TEXT.resolvedAt[0], TEXT.resolvedAt[1], { when: formatTime(item.resolved) })} />
                : (item.level === 'error' || item.level === 'warning') && !item.legacy && <Chip size="small" variant="outlined" label={t(...TEXT.open_)} />}
              {item.count > 1 && <Chip size="small" variant="outlined" label={'×' + item.count} />}
              <SourceChip source={item.source} />
              <SubjectChip subject={item.subject} />
            </Stack>
          </Box>
        </Stack>
        <Divider />
        <Field label={t(...TEXT.last)}><When value={item.date} variant="body2" /></Field>
        {item.firstSeen && item.count > 1 && <Field label={t(...TEXT.first)}><When value={item.firstSeen} variant="body2" /></Field>}
        <Typography variant="caption" color="textSecondary">{t(TEXT.zone[0], TEXT.zone[1], { zone: zoneName() })}</Typography>
      </Stack>
    </MainCard>

    <Section title={t(...TEXT.whatHappened)}>
      <Stack spacing={1.5}>
        {what && <Typography variant="body1" sx={{ whiteSpace: 'pre-wrap', ...wrapAnywhere }}>{what}</Typography>}
        {item.legacy && <Typography variant="body2" color="textSecondary">{t(...TEXT.legacy)}</Typography>}
        <RawText text={raw} />
        <Facts facts={item.facts} t={t} />
        {(link || subjectLink) && <Box><Button component={RouterLink} to={subjectLink || link} variant="outlined" sx={touch}>{t(...TEXT.openLink)}</Button></Box>}
      </Stack>
    </Section>

    {means && <Section title={t(...TEXT.whatItMeans)}><Typography variant="body1" sx={wrapAnywhere}>{means}</Typography></Section>}

    {(doing || item.resolved) && <Section title={t(...TEXT.whatCosmosDid)}>
      <Stack spacing={1}>
        {doing && <Typography variant="body1" sx={wrapAnywhere}>{doing}</Typography>}
        {item.resolved && <Alert severity="success">{t(TEXT.resolvedAt[0], TEXT.resolvedAt[1], { when: formatTime(item.resolved) + ' (' + dayjs(item.resolved).fromNow() + ')' })}</Alert>}
      </Stack>
    </Section>}

    {steps.length > 0 && <Section title={t(...TEXT.whatYouCanDo)}>
      <Stack divider={<Divider flexItem />}>{steps.slice(0, 8).map((s, i) => <Step key={i} step={s} t={t} />)}</Stack>
    </Section>}

    {isAdmin && <Section title={t(...TEXT.stateNow)}><StateNow context={context} t={t} /></Section>}

    <Section title={t(...TEXT.history)}><History item={item} details={details} t={t} /></Section>

    {contextError && <Alert severity="warning" sx={wrapAnywhere}>{t(TEXT.contextFailed[0], TEXT.contextFailed[1], { error: contextError, interpolation: { escapeValue: false } })}</Alert>}
    {isAdmin && context && context.security && context.security.available !== false && <Section title={t(...TEXT.security)}><SecurityList part={context.security} subject={item.subject} t={t} /></Section>}
    {isAdmin && context && context.events && context.events.available !== false && <Section title={t(...TEXT.events)}><EventsList part={context.events} t={t} /></Section>}
    {isAdmin && context && context.log && <Section title={t(...TEXT.log)}><LogLines part={context.log} t={t} /></Section>}
  </Stack>;
};

export default NotificationDetails;

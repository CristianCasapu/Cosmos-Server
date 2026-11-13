// Fork addition: the audit page. What each user did and what happened to each
// account, from the security record (src/security). Read only: nothing on
// this page changes or deletes an entry.
import * as React from 'react';
import { Alert, Box, Button, CircularProgress, Grid, MenuItem, Stack, TextField, Tooltip, Typography } from '@mui/material';
import { DownloadOutlined, SyncOutlined } from '@ant-design/icons';
import { useLocation, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import * as API from '../../api';
import MainCard from '../../components/MainCard';
import { useClientInfos } from '../../utils/hooks';
import { PERM_ADMIN_READ, PERM_USERS_READ } from '../../utils/permissions';
import { KIND_GROUPS, auditSentence } from '../../utils/auditSentences';
import { MAX_TEXT, OUTCOMES, PERIODS, apiQuery, cleanFilters, filtersFromSearch, formatTime, hasFilters, searchFromFilters, zoneName } from '../../utils/auditFilters';
import { EXPORT_MAX_ROWS, EXPORT_PAGE_SIZE, auditCsv, exportFileName } from '../../utils/auditCsv';
import { notify } from '../../utils/activity';
import EventDetails from './eventDetails';
import TrailList from './trailList';
import UserSummary from './userSummary';

const PAGE_SIZE = 50;

// the text fields are applied after a pause in typing
function useDebounced(value, ms) {
    const [debounced, setDebounced] = React.useState(value);
    React.useEffect(() => {
        const timer = setTimeout(() => setDebounced(value), ms);
        return () => clearTimeout(timer);
    }, [value, ms]);
    return debounced;
}

const SecurityAuditPage = () => {
    const { t } = useTranslation();
    const { hasPermission } = useClientInfos();
    const canRead = hasPermission(PERM_ADMIN_READ);
    const canReadUsers = hasPermission(PERM_USERS_READ);
    const location = useLocation();
    const navigate = useNavigate();

    const filters = React.useMemo(() => filtersFromSearch(location.search), [location.search]);
    const setFilters = (changes) => {
        const next = cleanFilters({ ...filters, ...changes });
        navigate({ pathname: location.pathname, search: searchFromFilters(next) }, { replace: true });
    };

    // the text fields keep what is typed, the address what is applied
    const [ipText, setIpText] = React.useState(filters.ip);
    const [qText, setQText] = React.useState(filters.q);
    const ipDebounced = useDebounced(ipText, 600);
    const qDebounced = useDebounced(qText, 600);
    const cleanIp = (text) => cleanFilters({ ip: text }).ip;
    const cleanQ = (text) => cleanFilters({ q: text }).q;
    const ipInvalid = ipText.trim() !== '' && cleanIp(ipText) === '';
    // the address changed by other means (a link, back, "clear"): show it
    React.useEffect(() => { if (cleanIp(ipText) !== filters.ip) setIpText(filters.ip); }, [filters.ip]);
    React.useEffect(() => { if (cleanQ(qText) !== filters.q) setQText(filters.q); }, [filters.q]);
    // what is typed: applied after a pause, an invalid address not at all
    React.useEffect(() => {
        const ipOK = ipDebounced.trim() === '' || cleanIp(ipDebounced) !== '';
        const changes = {};
        if (ipOK && cleanIp(ipDebounced) !== filters.ip) changes.ip = ipDebounced;
        if (cleanQ(qDebounced) !== filters.q) changes.q = qDebounced;
        if (Object.keys(changes).length) setFilters(changes);
    }, [ipDebounced, qDebounced]);

    const [users, setUsers] = React.useState([]);
    React.useEffect(() => {
        if (!canRead || !canReadUsers) return;
        API.users.list().then((res) => {
            setUsers((res.data || []).map((u) => u.nickname).filter(Boolean).sort((a, b) => a.localeCompare(b)));
        }).catch(() => setUsers([]));
    }, [canRead, canReadUsers]);

    const [events, setEvents] = React.useState(null);
    const [cursor, setCursor] = React.useState(0);
    const [loading, setLoading] = React.useState(false);
    const [error, setError] = React.useState('');
    const [selected, setSelected] = React.useState(null);
    const [exporting, setExporting] = React.useState(false);
    const request = React.useRef(0);

    const load = (more) => {
        const id = ++request.current;
        setLoading(true);
        setError('');
        API.config.securityAudit(apiQuery(filters), more ? cursor : 0, PAGE_SIZE, true).then((res) => {
            if (id !== request.current) return;
            const page = res.data || {};
            setEvents((previous) => (more ? [...(previous || []), ...(page.events || [])] : (page.events || [])));
            setCursor(page.nextCursor || 0);
            setLoading(false);
        }).catch((err) => {
            if (id !== request.current) return;
            setError(err && err.message ? err.message : String(err));
            setLoading(false);
            if (!more) setEvents([]);
        });
    };

    React.useEffect(() => {
        if (canRead) load(false);
    }, [location.search, canRead]);

    const exportCSV = async () => {
        setExporting(true);
        try {
            const rows = [];
            let next = 0;
            const query = apiQuery(filters);
            do {
                const res = await API.config.securityAudit(query, next, EXPORT_PAGE_SIZE, true);
                rows.push(...((res.data && res.data.events) || []));
                next = (res.data && res.data.nextCursor) || 0;
            } while (next && rows.length < EXPORT_MAX_ROWS);
            const kept = rows.slice(0, EXPORT_MAX_ROWS);
            const csv = auditCsv(kept, { sentence: (row) => auditSentence(row, t), formatTime, zone: zoneName() });
            const url = URL.createObjectURL(new Blob([csv], { type: 'text/csv;charset=utf-8' }));
            const link = document.createElement('a');
            link.href = url;
            link.download = exportFileName();
            document.body.appendChild(link);
            link.click();
            link.remove();
            setTimeout(() => URL.revokeObjectURL(url), 10000);
            notify(next
                ? t('securityAudit.export.capped', 'Exported the newest {{count}} lines: the export stops there. Narrow the filters for older lines.', { count: kept.length })
                : t('securityAudit.export.done', 'Exported {{count}} lines.', { count: kept.length }), next ? 'warning' : 'success');
        } catch (err) {
            notify(t('securityAudit.export.failed', 'The export failed: {{reason}}', { reason: err && err.message ? err.message : String(err) }), 'error');
        }
        setExporting(false);
    };

    if (!canRead) {
        return <Box sx={{ maxWidth: '1200px', margin: 'auto' }}>
            <Alert severity="info">{t('securityAudit.noPermission', 'The audit is for administrators who may read the server’s logs.')}</Alert>
        </Box>;
    }

    const userOptions = filters.user && !users.includes(filters.user) ? [filters.user, ...users] : users;

    return <Stack spacing={2} sx={{ maxWidth: '1200px', margin: 'auto', minWidth: 0 }}>
        <MainCard title={t('securityAudit.title', 'Audit')}>
            <Stack spacing={2}>
                <Typography variant="body2">
                    {t('securityAudit.intro', 'What each user did and what happened to each account: logins and failed attempts, password and two-factor changes, changes to settings, URLs, containers, accounts and API tokens, terminals opened, permissions refused. Newest first. Click a line for everything it holds.')}
                </Typography>
                <Typography variant="caption" color="textSecondary">
                    {t('securityAudit.zone', 'Times are in {{zone}}.', { zone: zoneName() })}
                </Typography>

                <Grid container spacing={2}>
                    <Grid item xs={12} sm={6} md={4}>
                        {canReadUsers
                            ? <TextField select fullWidth size="small" label={t('securityAudit.filter.user', 'User')} value={filters.user}
                                onChange={(e) => setFilters({ user: e.target.value })}>
                                <MenuItem value="">{t('securityAudit.filter.allUsers', 'All users')}</MenuItem>
                                {userOptions.map((u) => <MenuItem key={u} value={u} sx={{ overflowWrap: 'anywhere', whiteSpace: 'normal' }}>{u}</MenuItem>)}
                            </TextField>
                            : <TextField fullWidth size="small" label={t('securityAudit.filter.user', 'User')} value={filters.user}
                                onChange={(e) => setFilters({ user: e.target.value })} inputProps={{ maxLength: 64 }} />}
                    </Grid>
                    <Grid item xs={12} sm={6} md={4}>
                        <TextField select fullWidth size="small" label={t('securityAudit.filter.group', 'What')} value={filters.group}
                            onChange={(e) => setFilters({ group: e.target.value })}>
                            <MenuItem value="all">{t('securityAudit.group.all', 'Everything')}</MenuItem>
                            {KIND_GROUPS.map((g) => <MenuItem key={g.id} value={g.id}>{t('securityAudit.group.' + g.id, g.label)}</MenuItem>)}
                        </TextField>
                    </Grid>
                    <Grid item xs={12} sm={6} md={4}>
                        <TextField select fullWidth size="small" label={t('securityAudit.filter.outcome', 'Outcome')} value={filters.outcome}
                            onChange={(e) => setFilters({ outcome: e.target.value })}>
                            {OUTCOMES.map((o) => <MenuItem key={o.id} value={o.id}>{t('securityAudit.outcomeFilter.' + o.id, o.label)}</MenuItem>)}
                        </TextField>
                    </Grid>
                    <Grid item xs={12} sm={6} md={4}>
                        <TextField select fullWidth size="small" label={t('securityAudit.filter.period', 'Period')} value={filters.period}
                            onChange={(e) => setFilters({ period: e.target.value })}>
                            {PERIODS.map((p) => <MenuItem key={p.id} value={p.id}>{t('securityAudit.period.' + p.id, p.label)}</MenuItem>)}
                        </TextField>
                    </Grid>
                    <Grid item xs={12} sm={6} md={4}>
                        <TextField fullWidth size="small" label={t('securityAudit.filter.ip', 'Address')} value={ipText}
                            onChange={(e) => setIpText(e.target.value)} inputProps={{ maxLength: 45 }}
                            error={ipInvalid} helperText={ipInvalid ? t('securityAudit.filter.ipInvalid', 'An IP address, like 192.168.1.20') : ''} />
                    </Grid>
                    <Grid item xs={12} sm={6} md={4}>
                        <TextField fullWidth size="small" label={t('securityAudit.filter.text', 'Text')} value={qText}
                            onChange={(e) => setQText(e.target.value)} inputProps={{ maxLength: MAX_TEXT }}
                            placeholder={t('securityAudit.filter.textPlaceholder', 'a name, a path, a container...')} />
                    </Grid>
                </Grid>

                <Stack direction="row" spacing={1} useFlexGap flexWrap="wrap">
                    <Button variant="outlined" startIcon={<SyncOutlined />} onClick={() => load(false)}>{t('global.refresh', 'Refresh')}</Button>
                    <Tooltip title={t('securityAudit.export.tooltip', 'Download the lines these filters select as a CSV file, at most {{max}} (the newest).', { max: EXPORT_MAX_ROWS })}>
                        <span>
                            <Button variant="outlined" startIcon={exporting ? <CircularProgress size={14} /> : <DownloadOutlined />}
                                disabled={exporting || !events || events.length === 0} onClick={exportCSV}>
                                {t('securityAudit.export.button', 'Export CSV')}
                            </Button>
                        </span>
                    </Tooltip>
                    {hasFilters(filters) && <Button onClick={() => { setIpText(''); setQText(''); navigate({ pathname: location.pathname, search: '' }, { replace: true }); }}>
                        {t('securityAudit.filter.clear', 'Clear the filters')}
                    </Button>}
                </Stack>
            </Stack>
        </MainCard>

        {filters.user && <UserSummary user={filters.user} onOpen={setSelected} />}

        <MainCard title={filters.user ? t('securityAudit.trailOf', 'Activity of {{user}}', { user: filters.user }) : t('securityAudit.trail', 'Activity')} sx={{ minWidth: 0 }}>
            {error && <Alert severity="error" sx={{ mb: 2, overflowWrap: 'anywhere' }}>{t('securityAudit.loadFailed', 'The record could not be read: {{reason}}', { reason: error })}</Alert>}
            {events === null && <Box sx={{ textAlign: 'center', py: 3 }}><CircularProgress /></Box>}
            {events !== null && events.length === 0 && !loading && !error && <Typography variant="body2" color="textSecondary" sx={{ py: 2 }}>
                {hasFilters(filters)
                    ? t('securityAudit.emptyFiltered', 'Nothing in the record matches these filters.')
                    : t('securityAudit.empty', 'Nothing has been recorded yet. Logins, failed attempts and changes made by users will appear here.')}
            </Typography>}
            {events !== null && events.length > 0 && <TrailList events={events} onOpen={setSelected} />}
            {loading && events !== null && <Box sx={{ textAlign: 'center', py: 2 }}><CircularProgress size={24} /></Box>}
            {!loading && cursor > 0 && <Box sx={{ pt: 2 }}>
                <Button fullWidth variant="outlined" onClick={() => load(true)}>{t('securityAudit.loadMore', 'Show older lines')}</Button>
            </Box>}
            <Typography variant="caption" color="textSecondary" component="div" sx={{ pt: 2 }}>
                {t('securityAudit.limits', 'What the record cannot know: anything before it was installed or older than 180 days; a few container changes that upstream sends as plain page loads (secure mode, automatic updates, lazy loading) are not recorded; for container updates and creations from a compose file, "done" means the request was accepted, not that it finished well. Attacks by strangers that name no account are on the security dashboard, not here.')}
            </Typography>
        </MainCard>

        <EventDetails event={selected} onClose={() => setSelected(null)} />
    </Stack>;
};

export default SecurityAuditPage;

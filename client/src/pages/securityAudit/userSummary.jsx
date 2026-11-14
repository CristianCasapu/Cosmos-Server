// Fork addition: what the record (and, for user readers, the account) says
// about one user.
import * as React from 'react';
import { Alert, Box, CircularProgress, Grid, Stack, Typography } from '@mui/material';
import { useTranslation } from 'react-i18next';
import * as API from '../../api';
import MainCard from '../../components/MainCard';
import { formatTime } from '../../utils/auditFilters';
import { wrapAnywhere } from './eventDetails';
import TrailList from './trailList';

const Fact = ({ title, children }) => <Grid item xs={12} sm={6} md={4}>
    <Stack spacing={0.5} sx={{ minWidth: 0 }}>
        <Typography variant="h6">{title}</Typography>
        <Box sx={wrapAnywhere}>{children}</Box>
    </Stack>
</Grid>;

const Line = ({ children, secondary }) => <Typography variant="body2" color={secondary ? 'textSecondary' : undefined} sx={wrapAnywhere}>{children}</Typography>;

const UserSummary = ({ user, onOpen }) => {
    const { t } = useTranslation();
    const [summary, setSummary] = React.useState(null);
    const [recent, setRecent] = React.useState(null);
    const [failed, setFailed] = React.useState(false);

    React.useEffect(() => {
        let current = true;
        setSummary(null);
        setRecent(null);
        setFailed(false);
        API.config.securityUserSummary(user).then((res) => {
            if (current) setSummary(res.data);
        }).catch(() => current && setFailed(true));
        API.config.securityAudit(new URLSearchParams({ user }).toString(), 0, 5, true).then((res) => {
            if (current) setRecent(res.data.events || []);
        }).catch(() => current && setRecent([]));
        return () => { current = false; };
    }, [user]);

    const failures = (f) => {
        const parts = [];
        if (f.password) parts.push(t('securityAudit.summary.failPassword', '{{count}} wrong password(s)', { count: f.password }));
        if (f.twoFactor) parts.push(t('securityAudit.summary.failTwoFactor', '{{count}} wrong two-factor code(s)', { count: f.twoFactor }));
        if (f.locked) parts.push(t('securityAudit.summary.failLocked', 'two-factor check locked {{count}} time(s)', { count: f.locked }));
        if (f.confirm) parts.push(t('securityAudit.summary.failConfirm', '{{count}} wrong password(s) at the confirmation prompt', { count: f.confirm }));
        return parts.length ? parts.join(', ') : t('securityAudit.summary.none', 'none');
    };

    const account = summary && summary.account;

    return <MainCard title={t('securityAudit.summary.title', 'Account of {{user}}', { user })} sx={wrapAnywhere}>
        {failed && <Alert severity="error">{t('securityAudit.summary.failed', 'The summary could not be read.')}</Alert>}
        {!failed && !summary && <Box sx={{ textAlign: 'center', py: 2 }}><CircularProgress /></Box>}
        {summary && <Grid container spacing={2}>
            {account && !account.exists && <Grid item xs={12}>
                <Alert severity="info">{t('securityAudit.summary.noAccount', 'There is no account of this name now (it was deleted, or never existed). What the record kept about the name is shown.')}</Alert>
            </Grid>}

            <Fact title={t('securityAudit.summary.lastLogin', 'Last login')}>
                {summary.lastLogin
                    ? <>
                        <Line>{formatTime(summary.lastLogin.time)}</Line>
                        {summary.lastLogin.ip && <Line secondary>{t('securityAudit.summary.fromAddress', 'from {{ip}}', { ip: summary.lastLogin.ip })}</Line>}
                    </>
                    : account && account.lastLogin
                        ? <Line>{formatTime(account.lastLogin)} <Typography component="span" variant="caption" color="textSecondary">{t('securityAudit.summary.fromAccount', '(from the account, address unknown)')}</Typography></Line>
                        : <Line secondary>{t('securityAudit.summary.noLogin', 'None in the record')}</Line>}
            </Fact>

            <Fact title={t('securityAudit.summary.failures', 'Failed attempts')}>
                <Line>{t('securityAudit.summary.last24h', 'Last 24 hours: {{text}}', { text: failures(summary.failures24h) })}</Line>
                <Line>{t('securityAudit.summary.last7d', 'Last 7 days: {{text}}', { text: failures(summary.failures7d) })}</Line>
            </Fact>

            <Fact title={t('securityAudit.summary.twoFactor', 'Two-factor')}>
                {account
                    ? (account.exists ? <Line>{account.twoFactor ? t('securityAudit.summary.twoFactorOn', 'On') : t('securityAudit.summary.twoFactorOff', 'Off')}</Line> : <Line secondary>-</Line>)
                    : <Line secondary>{t('securityAudit.summary.needsUsersRead', 'Unknown: needs the permission to read users')}</Line>}
            </Fact>

            <Fact title={t('securityAudit.summary.password', 'Password last set')}>
                {account && account.passwordChangedAt
                    ? <Line>{formatTime(account.passwordChangedAt)}</Line>
                    : summary.lastPasswordChange
                        ? <Line>{formatTime(summary.lastPasswordChange.time)}</Line>
                        : <Line secondary>{account && account.exists && !account.registered
                            ? t('securityAudit.summary.notRegistered', 'Never: the invitation was not used yet')
                            : t('securityAudit.summary.unknown', 'Not known')}</Line>}
                {summary.lastPasswordChange && summary.lastPasswordChange.ip &&
                    <Line secondary>{t('securityAudit.summary.fromAddress', 'from {{ip}}', { ip: summary.lastPasswordChange.ip })}</Line>}
            </Fact>

            <Fact title={t('securityAudit.summary.addresses', 'Used from (last 30 days)')}>
                {summary.addresses && summary.addresses.length
                    ? summary.addresses.map((a) => <Line key={a.ip}>{a.ip} <Typography component="span" variant="caption" color="textSecondary">
                        {t('securityAudit.summary.addressUse', '{{count}}x, last {{time}}', { count: a.count, time: formatTime(a.last) })}
                    </Typography></Line>)
                    : <Line secondary>{t('securityAudit.summary.noAddresses', 'No address in the record')}</Line>}
            </Fact>

            <Fact title={t('securityAudit.summary.since', 'Known since')}>
                <Line>{summary.recordSince ? formatTime(summary.recordSince) : t('securityAudit.summary.emptyRecord', 'The record is empty')}</Line>
                <Line secondary>{t('securityAudit.summary.sinceNote', 'The record keeps 180 days; nothing before its first entry is known.')}</Line>
            </Fact>

            <Grid item xs={12}>
                <Typography variant="h6" sx={{ mb: 0.5 }}>{t('securityAudit.summary.lastActions', 'Last actions')}</Typography>
                {recent === null ? <CircularProgress size={20} /> : recent.length
                    ? <TrailList events={recent} onOpen={onOpen} />
                    : <Line secondary>{t('securityAudit.summary.noActions', 'Nothing in the record')}</Line>}
            </Grid>

            <Grid item xs={12}>
                <Typography variant="caption" color="textSecondary">
                    {t('securityAudit.summary.floodNote', 'Failed attempts that come too fast from one address are summed up without the name tried, so these counts can be lower than the real number. Times are those of this browser.')}
                </Typography>
            </Grid>
        </Grid>}
    </MainCard>;
};

export default UserSummary;

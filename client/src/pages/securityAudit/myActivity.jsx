// Fork addition: "Recent activity on your account", for every logged-in user.
// The server gives only rows about the own account, and on rows done by
// someone else (an administrator's change, a failed login against the
// account) neither who nor from where (src/security/useraudit_api.go).
import * as React from 'react';
import { Accordion, AccordionDetails, AccordionSummary, Box, Button, CircularProgress, Stack, Typography } from '@mui/material';
import { DownOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import * as API from '../../api';
import { zoneName } from '../../utils/auditFilters';
import TrailList from './trailList';

const PAGE = 20;

const MyActivity = () => {
    const { t } = useTranslation();
    const [open, setOpen] = React.useState(false);
    const [events, setEvents] = React.useState(null);
    const [cursor, setCursor] = React.useState(0);
    const [loading, setLoading] = React.useState(false);
    const [failed, setFailed] = React.useState(false);

    const load = (more) => {
        setLoading(true);
        setFailed(false);
        API.config.securityMyActivity(more ? cursor : 0, PAGE).then((res) => {
            const page = res.data || {};
            setEvents((previous) => (more ? [...(previous || []), ...(page.events || [])] : (page.events || [])));
            setCursor(page.nextCursor || 0);
            setLoading(false);
        }).catch(() => {
            setFailed(true);
            setLoading(false);
            if (!more) setEvents([]);
        });
    };

    // read when opened, not with every visit of the home page
    React.useEffect(() => {
        if (open && events === null) load(false);
    }, [open]);

    return <Accordion expanded={open} onChange={(e, expanded) => setOpen(expanded)} sx={{ minWidth: 0 }}>
        <AccordionSummary expandIcon={<DownOutlined />} aria-controls="my-activity-content" id="my-activity-header">
            <Typography variant="h6">{t('securityAudit.mine.title', 'Recent activity on your account')}</Typography>
        </AccordionSummary>
        <AccordionDetails>
            <Stack spacing={1}>
                <Typography variant="caption" color="textSecondary">
                    {t('securityAudit.mine.intro', 'Your logins and what was done to your account. If you see a login or a change you did not make, change your password and tell your administrator. Times are in {{zone}}.', { zone: zoneName() })}
                </Typography>
                {events === null && <Box sx={{ textAlign: 'center' }}><CircularProgress size={24} /></Box>}
                {failed && <Typography variant="body2" color="error">{t('securityAudit.mine.failed', 'Your activity could not be read.')}</Typography>}
                {events !== null && !failed && events.length === 0 && <Typography variant="body2" color="textSecondary">
                    {t('securityAudit.mine.empty', 'Nothing recorded yet.')}
                </Typography>}
                {events !== null && events.length > 0 && <TrailList events={events} />}
                {loading && events !== null && <Box sx={{ textAlign: 'center' }}><CircularProgress size={20} /></Box>}
                {!loading && cursor > 0 && <Button variant="outlined" onClick={() => load(true)}>{t('securityAudit.loadMore', 'Show older lines')}</Button>}
            </Stack>
        </AccordionDetails>
    </Accordion>;
};

export default MyActivity;

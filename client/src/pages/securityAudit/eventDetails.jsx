// Fork addition: everything a row of the security record holds, as text.
import * as React from 'react';
import { Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, Stack, Typography } from '@mui/material';
import { useTranslation } from 'react-i18next';
import { auditSentence } from '../../utils/auditSentences';
import { formatTime, zoneName } from '../../utils/auditFilters';

// wrap long values anywhere: a path or a name typed into a form can be one
// long word
export const wrapAnywhere = { overflowWrap: 'anywhere', wordBreak: 'break-word', minWidth: 0 };

const Field = ({ label, value }) => {
    if (value === undefined || value === null || value === '') return null;
    return <Stack direction={{ xs: 'column', sm: 'row' }} spacing={{ xs: 0, sm: 2 }} sx={{ py: 0.5 }}>
        <Typography variant="body2" color="textSecondary" sx={{ minWidth: { sm: 140 }, flexShrink: 0 }}>{label}</Typography>
        <Typography variant="body2" sx={wrapAnywhere}>{String(value)}</Typography>
    </Stack>;
};

const EventDetails = ({ event, onClose }) => {
    const { t } = useTranslation();
    const open = !!event;
    const e = event || {};
    const details = e.details && typeof e.details === 'object' ? Object.keys(e.details).sort() : [];

    return <Dialog open={open} onClose={onClose} fullWidth maxWidth="sm">
        <DialogTitle sx={wrapAnywhere}>{open ? auditSentence(e, t) : ''}</DialogTitle>
        <DialogContent>
            <Field label={t('securityAudit.field.time', 'Time')} value={formatTime(e.time) + ' (' + zoneName() + ')'} />
            <Field label={t('securityAudit.field.timeUTC', 'Time (UTC)')} value={e.time} />
            <Field label={t('securityAudit.field.kind', 'Kind')} value={e.kind} />
            <Field label={t('securityAudit.field.class', 'Class')} value={e.class === 'audit'
                ? t('securityAudit.class.audit', 'audit (an action of a logged-in user)')
                : t('securityAudit.class.incident', 'incident (refused, failed or suspicious)')} />
            <Field label={t('securityAudit.field.severity', 'Severity')} value={e.severity} />
            <Field label={t('securityAudit.field.outcome', 'Outcome')} value={e.outcome} />
            <Field label={t('securityAudit.field.actor', 'Done by')} value={e.actor} />
            <Field label={t('securityAudit.field.target', 'About')} value={e.target} />
            <Field label={t('securityAudit.field.ip', 'Address')} value={e.ip} />
            <Field label={t('securityAudit.field.method', 'Method')} value={e.method} />
            <Field label={t('securityAudit.field.path', 'Path')} value={e.path} />
            <Field label={t('securityAudit.field.host', 'Hostname asked for')} value={e.host} />
            <Field label={t('securityAudit.field.count', 'Times')} value={e.count > 1 ? e.count : ''} />
            {details.map((key) => <Field key={key} label={key} value={e.details[key]} />)}
            <Field label={t('securityAudit.field.id', 'Row')} value={e.id} />
            {e.count > 1 && <Box sx={{ pt: 1 }}>
                <Typography variant="caption" color="textSecondary">
                    {t('securityAudit.details.summaryNote', 'This line stands for several events of the same kind that came too fast to be stored one by one.')}
                </Typography>
            </Box>}
        </DialogContent>
        <DialogActions>
            <Button onClick={onClose}>{t('global.close', 'Close')}</Button>
        </DialogActions>
    </Dialog>;
};

export default EventDetails;

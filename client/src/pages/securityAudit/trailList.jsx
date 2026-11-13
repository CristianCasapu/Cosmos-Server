// Fork addition: rows of the security record as sentences, newest first. A
// row opens its details when onOpen is given.
import * as React from 'react';
import { Box, ButtonBase, Chip, Stack, Typography } from '@mui/material';
import { useTheme } from '@mui/material/styles';
import { useTranslation } from 'react-i18next';
import { auditSentence, isProblem } from '../../utils/auditSentences';
import { formatTime } from '../../utils/auditFilters';
import { wrapAnywhere } from './eventDetails';

const OutcomeChip = ({ outcome }) => {
    const { t } = useTranslation();
    if (outcome === 'failed') return <Chip size="small" color="error" variant="outlined" label={t('securityAudit.outcome.failed', 'failed')} />;
    if (outcome === 'denied') return <Chip size="small" color="warning" variant="outlined" label={t('securityAudit.outcome.denied', 'refused')} />;
    if (outcome === 'blocked') return <Chip size="small" color="warning" variant="outlined" label={t('securityAudit.outcome.blocked', 'blocked')} />;
    return null;
};

const TrailRow = ({ event, onOpen }) => {
    const { t } = useTranslation();
    const theme = useTheme();
    const content = <Stack direction="row" spacing={1.5} alignItems="flex-start" sx={{ width: '100%', textAlign: 'left', py: 1, px: 1.5 }}>
        <Box sx={{ flex: 1, minWidth: 0 }}>
            <Typography variant="body1" sx={wrapAnywhere}>{auditSentence(event, t)}</Typography>
            <Typography variant="caption" color="textSecondary">{formatTime(event.time)}</Typography>
        </Box>
        {isProblem(event) && <Box sx={{ flexShrink: 0, pt: 0.25 }}><OutcomeChip outcome={event.outcome} /></Box>}
    </Stack>;

    const sx = {
        width: '100%',
        borderRadius: 1,
        borderLeft: '3px solid',
        borderLeftColor: isProblem(event) ? theme.palette.warning.main : 'transparent',
    };
    if (!onOpen) return <Box sx={sx}>{content}</Box>;
    return <ButtonBase component="div" role="button" sx={{ ...sx, display: 'block', '&:hover': { background: theme.palette.action.hover } }}
        onClick={() => onOpen(event)}>
        {content}
    </ButtonBase>;
};

const TrailList = ({ events, onOpen }) => {
    return <Stack spacing={0.5}>
        {(events || []).map((event) => <TrailRow key={event.id} event={event} onOpen={onOpen} />)}
    </Stack>;
};

export default TrailList;

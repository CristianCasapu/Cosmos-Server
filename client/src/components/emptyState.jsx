// Fork addition: one look for "nothing here (yet)" and "this cannot be shown":
// a quiet block with a title, a sentence that says what to do next, and
// optionally buttons. NoMeasurements is the sentence for a server that has
// measured nothing yet.
import * as React from 'react';
import { Alert, Box, Typography } from '@mui/material';
import { useTranslation } from 'react-i18next';

const EmptyState = ({ icon, title, children, action, compact }) => (
  <Box className="ct-empty" role="status" sx={{ textAlign: 'center', py: compact ? 3 : 8, px: 2, maxWidth: 560, mx: 'auto' }}>
    {icon && <Box className="ct-empty-icon" aria-hidden="true" sx={{ fontSize: compact ? 28 : 44, lineHeight: 1, mb: 1.5, color: 'text.secondary' }}>{icon}</Box>}
    {title && <Typography variant="h4" component="h2" sx={{ mb: 1, overflowWrap: 'anywhere' }}>{title}</Typography>}
    {children && <Typography variant="body1" color="textSecondary" component="div" sx={{ overflowWrap: 'anywhere' }}>{children}</Typography>}
    {action && <Box sx={{ mt: 2.5, display: 'flex', gap: 1, justifyContent: 'center', flexWrap: 'wrap' }}>{action}</Box>}
  </Box>
);

export const NoMeasurements = ({ sx }) => {
  const { t } = useTranslation();
  return <Alert severity="info" className="ct-no-measurements" sx={sx}>
    {t('monitoring.noMeasurements', 'No measurements yet. The first ones arrive within a minute of the server\'s start; this page fills in by itself.')}
  </Alert>;
};

export default EmptyState;

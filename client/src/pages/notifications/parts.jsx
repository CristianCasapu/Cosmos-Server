// Fork addition: what the notifications page, its details and the bell share.
// Every text of a notification is data: shown as text, never as markup.
import * as React from 'react';
import { Avatar, Box, Button, Chip, Stack, Typography } from '@mui/material';
import { CheckCircleOutlined, CopyOutlined, ExclamationCircleOutlined, InfoCircleOutlined, WarningOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import dayjs from 'dayjs';
import { formatTime } from '../../utils/auditFilters';
import { notify } from '../../utils/activity';
import { LEVEL_TEXT, SOURCE_TEXT, SUBJECT_TEXT, TEXT, normalizeLevel } from '../../utils/notifications';

export const wrapAnywhere = { overflowWrap: 'anywhere', wordBreak: 'break-word', minWidth: 0 };

const ICONS = { error: ExclamationCircleOutlined, warning: WarningOutlined, success: CheckCircleOutlined, info: InfoCircleOutlined };

export const LevelIcon = ({ level, size = 36 }) => {
  const { t } = useTranslation();
  const l = normalizeLevel(level);
  const Icon = ICONS[l];
  return <Avatar aria-label={t(...LEVEL_TEXT[l])} sx={{ color: l + '.main', bgcolor: l + '.lighter', width: size, height: size, fontSize: '1rem', flexShrink: 0 }}>
    <Icon />
  </Avatar>;
};

// When: "5 minutes ago" with the exact time beside it (both, as the brief asks).
export const When = ({ value, variant = 'caption' }) => {
  if (!value) return null;
  return <Typography variant={variant} color="textSecondary" component="span" sx={wrapAnywhere}>
    {dayjs(value).fromNow()}{' · '}<time dateTime={value}>{formatTime(value)}</time>
  </Typography>;
};

export const SubjectChip = ({ subject }) => {
  const { t } = useTranslation();
  if (!subject) return null;
  return <Chip size="small" variant="outlined" sx={{ maxWidth: '100%', height: 'auto', '& .MuiChip-label': { whiteSpace: 'normal', ...wrapAnywhere, py: '2px' } }}
    label={t(...(SUBJECT_TEXT[subject.type] || SUBJECT_TEXT.other)) + ': ' + subject.name} />;
};

export const SourceChip = ({ source }) => {
  const { t } = useTranslation();
  return <Chip size="small" label={t(...(SOURCE_TEXT[source] || SOURCE_TEXT.other))} />;
};

export const copyText = (text, t) => {
  const done = () => notify(t(...TEXT.copied), 'success');
  try {
    navigator.clipboard.writeText(text).then(done).catch(() => {});
  } catch (e) {
    // no clipboard in this page: the text is on the screen and can be selected
  }
};

// RawText: a technical text in full, selectable, wrapped, with a copy button.
export const RawText = ({ text, label }) => {
  const { t } = useTranslation();
  if (!text) return null;
  return <Box sx={{ border: '1px solid var(--ct-border)', borderRadius: 'var(--ct-radius-s)', bgcolor: 'var(--ct-surface-2)', p: 1.5, minWidth: 0 }}>
    <Stack direction="row" spacing={1} sx={{ alignItems: 'center', justifyContent: 'space-between', mb: 0.5 }}>
      <Typography variant="caption" color="textSecondary">{label || t(...TEXT.technical)}</Typography>
      <Button size="small" startIcon={<CopyOutlined />} onClick={() => copyText(text, t)} sx={{ minHeight: { xs: 44, sm: 'auto' } }}>
        {t(...TEXT.copy)}
      </Button>
    </Stack>
    <Typography component="pre" variant="body2" sx={{
      m: 0, fontFamily: 'monospace', whiteSpace: 'pre-wrap', ...wrapAnywhere, userSelect: 'text', maxHeight: 420, overflow: 'auto',
    }}>{text}</Typography>
  </Box>;
};

// Field: a label and its value, side by side, stacked on a phone.
export const Field = ({ label, value, children }) => {
  if ((value === undefined || value === null || value === '') && !children) return null;
  return <Stack direction={{ xs: 'column', sm: 'row' }} spacing={{ xs: 0, sm: 2 }} sx={{ py: 0.5 }}>
    <Typography variant="body2" color="textSecondary" sx={{ minWidth: { sm: 160 }, maxWidth: { sm: 220 }, flexShrink: 0 }}>{label}</Typography>
    {children || <Typography variant="body2" sx={wrapAnywhere}>{String(value)}</Typography>}
  </Stack>;
};

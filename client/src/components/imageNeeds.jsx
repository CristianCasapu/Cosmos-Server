// Fork addition (plan 43): in "Install any image", what Cosmos could find
// out about the settings an image needs, and where from: a built-in recipe,
// what the image declares, its publisher's description (the describe
// route's `hints`), its documentation links (the image's labels, https only,
// opened by the administrator, never by Cosmos). For an image without recipe
// it says plainly that Cosmos does not know its required settings, and that
// the first start is watched: if the image says it needs something, Cosmos
// proposes it then.
import * as React from 'react';
import { Alert, Box, Stack, TextField, Typography } from '@mui/material';
import { useTranslation } from 'react-i18next';

import { looksLikeDatabase } from '../utils/services';

const wrap = { overflowWrap: 'anywhere', minWidth: 0, '& .MuiAlert-message': { minWidth: 0 } };
// a finger's height on a phone, as the dialog's other links (installAnyImage.jsx)
const touchLink = { display: { xs: 'inline-flex', sm: 'inline' }, alignItems: 'center', minHeight: { xs: 44, sm: 0 }, overflowWrap: 'anywhere' };
const httpsOnly = (u) => /^https:\/\/[^\s"'<>]+$/i.test(String(u || ''));

export const ImageNeeds = ({ desc, recipe, declared, reference }) => {
  const { t } = useTranslation();
  if (!desc || recipe || !declared) return null;
  const hints = desc.hints || null;
  const fromText = !!(hints && hints.source === 'description' && (hints.env || []).length);
  const docs = ((hints && hints.docs) || []).filter(httpsOnly).slice(0, 3);
  const ports = ((declared && declared.ports) || []).map((p) => Number(String(p).split('/')[0]));
  const database = looksLikeDatabase(reference, ports);
  return <Stack spacing={1} sx={{ minWidth: 0 }} data-image-needs={fromText ? 'description' : 'unknown'}>
    <Alert severity={database ? 'warning' : 'info'} sx={wrap} data-image-needs-database={database || undefined}>
      {database
        ? t('mgmt.services.needs.database', 'This looks like {{software}}, but Cosmos has no built-in settings for this image: it does not know which settings it requires. Databases usually need a password, often a user and a database name too.', { software: database, interpolation: { escapeValue: false } })
        : t('mgmt.services.needs.unknown', 'Cosmos has no built-in settings for this image: it does not know which settings it requires.')}
      {' '}
      {fromText
        ? t('mgmt.services.needs.fromText', 'The settings below marked "from its description" are what its publisher\'s description names: check them.')
        : t('mgmt.services.needs.noText', 'Its publisher\'s description could not be read for settings: its page says what it needs.')}
      {' '}
      {t('mgmt.services.needs.watch', 'After the creation Cosmos watches its first start: if the image says it is missing a setting, Cosmos proposes it then.')}
    </Alert>
    {(docs.length > 0 || desc.page) && <Stack direction="row" spacing={2} useFlexGap sx={{ flexWrap: 'wrap', minWidth: 0 }}>
      {desc.page && <Box component="a" href={desc.page} target="_blank" rel="noopener noreferrer" sx={touchLink} data-image-needs-page>
        {t('mgmt.services.needs.page', 'What its publisher says it needs')}
      </Box>}
      {docs.filter((d) => d !== desc.page).map((d) => <Box key={d} component="a" href={d} target="_blank" rel="noopener noreferrer" sx={touchLink} data-image-needs-doc>
        {d.replace(/^https:\/\//, '')}
      </Box>)}
    </Stack>}
  </Stack>;
};

// Settings typed by hand, for every image: "NAME=value", one per line.
export const HandSettings = ({ value, onChange, busy }) => {
  const { t } = useTranslation();
  return <Stack spacing={0.5} sx={{ minWidth: 0 }} data-hand-settings>
    <TextField size="small" multiline minRows={2} label={t('mgmt.services.hand.label', 'More settings (variables), by hand')} value={value} disabled={busy}
      onChange={(e) => onChange(e.target.value.slice(0, 8000))}
      helperText={t('mgmt.services.hand.hint', 'One per line, NAME=value. A name above is replaced. Volumes and ports can be changed in the review\'s definition (advanced).')} />
  </Stack>;
};

// The helper text of a setting proposed from the description.
export const hintHelp = (t, setting) => {
  if (!setting || setting.from !== 'description') return null;
  const base = setting.type === 'secret'
    ? t('mgmt.services.hint.secret', 'From its description. Generated for you; after the creation it is on the app\'s page, under Sign-in details (or in its settings).')
    : setting.type === 'needed'
      ? t('mgmt.services.hint.needed', 'From its description, which says it is required: enter its value.')
      : t('mgmt.services.hint.optional', 'From its description. Left empty, it is not set.');
  return setting.hint ? base + ' ' + t('mgmt.services.hint.example', 'Its example: {{hint}}', { hint: setting.hint, interpolation: { escapeValue: false } }) : base;
};

export const HintCount = ({ settings }) => {
  const { t } = useTranslation();
  const n = (settings || []).filter((s) => s.from === 'description').length;
  if (!n) return null;
  return <Typography variant="caption" sx={{ color: 'text.secondary' }}>{t('mgmt.services.hint.count', '{{count}} of these settings come from its description.', { count: n })}</Typography>;
};

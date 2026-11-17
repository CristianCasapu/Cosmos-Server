// Fork addition: the protection cards of the security dashboard. One card
// each for the host guard (fork/nftables), Fail2ban (fork/fail2ban) and the
// automatic security updates (fork/unattended-upgrades): installed or not,
// active or not, what it protects, since when, and the command that installs
// or removes it, to copy and run on the server. Read only: Cosmos never
// installs or changes these pieces. The logic is in utils/protection.js; it
// talks to GET /cosmos/api/protection (src/protection).
import * as React from 'react';
import dayjs from 'dayjs';
import { useTranslation } from 'react-i18next';
import { Alert, Box, Chip, Grid, IconButton, Stack, Tooltip, Typography } from '@mui/material';
import { CopyOutlined, SyncOutlined } from '@ant-design/icons';

import * as API from '../../api';
import MainCard from '../../components/MainCard';
import { useClientInfos } from '../../utils/hooks';
import { PERM_ADMIN_READ } from '../../utils/permissions';
import { cardsOf } from '../../utils/protection';

// at least 44 px to touch on a phone
const touch = { minWidth: { xs: 44, sm: 32 }, minHeight: { xs: 44, sm: 32 } };

// The command, in a box of its own that wraps long paths, with a copy button.
const Command = ({ label, text }) => {
  const { t } = useTranslation();
  const [copied, setCopied] = React.useState(false);
  const box = React.useRef(null);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
    } catch (e) {
      // no clipboard here (an old browser, a page without HTTPS): select it for copying by hand
      const selection = window.getSelection && window.getSelection();
      if (selection && box.current) {
        const range = document.createRange();
        range.selectNodeContents(box.current);
        selection.removeAllRanges();
        selection.addRange(range);
      }
    }
  };
  const tip = copied ? t('protection.copied', 'Copied') : t('protection.copy', 'Copy the command');
  return <Stack spacing={0.5} sx={{ minWidth: 0 }}>
    <Typography variant="caption" color="textSecondary">{label}</Typography>
    <Stack direction="row" spacing={0.5} sx={{ alignItems: 'center', minWidth: 0 }}>
      <Box ref={box} component="code" sx={{
        flex: '1 1 auto', minWidth: 0, display: 'block', px: 1, py: 0.75, borderRadius: 1,
        border: '1px solid', borderColor: 'divider', bgcolor: 'action.hover',
        fontFamily: 'monospace', fontSize: '0.8125rem', overflowWrap: 'anywhere', userSelect: 'all',
      }} data-protection-command>{text}</Box>
      <Tooltip title={tip}>
        <IconButton onClick={copy} aria-label={tip} sx={touch} onMouseLeave={() => setCopied(false)}><CopyOutlined /></IconButton>
      </Tooltip>
    </Stack>
  </Stack>;
};

const PieceCard = ({ card }) => {
  const { t } = useTranslation();
  const say = (entry) => t(entry[0], entry[1], entry[2] || {});
  return <MainCard sx={{ height: '100%' }} contentSX={{ p: 2.25 }} data-protection-card={card.id}>
    <Stack spacing={1.25}>
      <Stack direction="row" spacing={1} sx={{ alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap' }} useFlexGap>
        <Typography variant="h5">{say(card.title)}</Typography>
        <Chip size="small" color={card.chip.color} variant={card.chip.color === 'default' ? 'outlined' : 'filled'} label={say(card.chip.text)} />
      </Stack>
      <Typography variant="body2" sx={{ color: 'text.secondary' }}>{say(card.protects)}</Typography>
      <Alert severity={card.severity} sx={{ overflowWrap: 'anywhere', '& .MuiAlert-message': { minWidth: 0 } }}>{say(card.sentence)}</Alert>
      {card.warnings.map((warning) => <Alert key={warning[0]} severity="warning" data-protection-warning={warning[0]}
        sx={{ overflowWrap: 'anywhere', '& .MuiAlert-message': { minWidth: 0 } }}>
        {say(warning)}
      </Alert>)}
      {card.lines.length > 0 && <Stack spacing={0.5}>
        {card.lines.map((line) => <Typography key={line[0]} variant="body2" sx={{ overflowWrap: 'anywhere' }}>{say(line)}</Typography>)}
      </Stack>}
      {card.command && <Command label={say(card.command.label)} text={card.command.text} />}
    </Stack>
  </MainCard>;
};

const ProtectionCards = () => {
  const { t } = useTranslation();
  const { hasPermission } = useClientInfos();
  const mayRead = hasPermission(PERM_ADMIN_READ);
  const [status, setStatus] = React.useState(null);
  const [failed, setFailed] = React.useState(false);

  const refresh = React.useCallback(() => {
    if (!API.config.protectionStatus) return;
    API.config.protectionStatus()
      .then((res) => { setStatus(res.data); setFailed(false); })
      .catch(() => setFailed(true));
  }, []);

  React.useEffect(() => {
    if (mayRead) refresh();
  }, [mayRead, refresh]);

  if (!mayRead) return null;
  const cards = cardsOf(status, (iso) => dayjs(iso).format('L LT'));

  return <Stack spacing={1.5} data-protection>
    <Stack direction="row" spacing={1} sx={{ alignItems: 'center', justifyContent: 'space-between' }}>
      <Typography variant="h5">{t('protection.title', 'Protection on the server')}</Typography>
      <Tooltip title={t('global.refresh')}>
        <IconButton onClick={refresh} aria-label={t('global.refresh')} sx={touch}><SyncOutlined /></IconButton>
      </Tooltip>
    </Stack>
    <Typography variant="body2" sx={{ color: 'text.secondary' }}>
      {t('protection.intro', 'Three pieces the fork can install on the server itself. Cosmos only shows their state: they are installed and removed on the server, with the commands below, by you.')}
    </Typography>
    {failed && <Alert severity="warning">{t('protection.failed', 'The state of the protection could not be read just now.')}</Alert>}
    {cards.length > 0 && <Grid container spacing={2}>
      {cards.map((card) => <Grid key={card.id} size={{ xs: 12, md: 4 }}>
        <PieceCard card={card} />
      </Grid>)}
    </Grid>}
  </Stack>;
};

export default ProtectionCards;

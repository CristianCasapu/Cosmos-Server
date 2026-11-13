// Fork addition: the security dashboard's busiest addresses: what they did,
// when, whether SmartShield blocks them now, and the actions on them.
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import dayjs from 'dayjs';
import { Box, Button, Chip, Stack, Tooltip, Typography } from '@mui/material';
import { DeleteOutlined, FilterOutlined } from '@ant-design/icons';

import * as API from '../../api';
import MainCard from '../../components/MainCard';
import PrettyTableView from '../../components/tableView/prettyTableView';
import { ConfirmModalDirect } from '../../components/confirmModal';
import PermissionGuard from '../../components/permissionGuard';
import { PERM_CONFIGURATION } from '../../utils/permissions';
import { BAN_TEXT, banOf, kindText, shorten } from '../../utils/securityDashboard';

const Text = ({ value, max }) => {
  const s = shorten(value, max);
  if (!s.cut) return <span>{s.text}</span>;
  return <Tooltip title={s.full}><span>{s.text}</span></Tooltip>;
};

const Addresses = ({ overview, bans, bansKnown, catalogue, onShowAddress, onUnbanned }) => {
  const { t } = useTranslation();
  const [confirmUnban, setConfirmUnban] = useState(null);
  const top = (overview && overview.top) || [];

  const where = (a) => {
    if (a.local) return t('security.addresses.local', 'Local network');
    return a.country
      ? t('security.addresses.internetCountry', 'Internet ({{country}})', { country: a.country })
      : t('security.addresses.internet', 'Internet');
  };

  const did = (a) => (a.kinds || []).slice(0, 3).map((k) => {
    const label = t(...kindText(k.kind, catalogue).label);
    return k.total > 1 ? label + ' ×' + k.total : label;
  }).join(', ') + ((a.kinds || []).length > 3 ? ', …' : '');

  const blocked = (a) => {
    if (!bansKnown) return null;
    const ban = banOf(bans, a.ip);
    if (!ban) return <Chip size="small" variant="outlined" label={t('security.addresses.notBlocked', 'Not blocked')} />;
    const [key, text] = BAN_TEXT[ban.status];
    return <Chip size="small" color={ban.status === 'strike' ? 'warning' : 'error'}
      label={t(key, text, { until: ban.until ? dayjs(ban.until).format('L LT') : '' })} />;
  };

  return <>
    {confirmUnban && <ConfirmModalDirect
      callback={() => {
        API.shield.unban(confirmUnban).then(() => onUnbanned && onUnbanned());
      }}
      content={t('mgmt.shield.bans.unbanConfirm', { client: confirmUnban })}
      onClose={() => setConfirmUnban(null)}
    />}
    <Typography variant="h5">{t('security.addresses.title', 'Who: the busiest addresses')}</Typography>
    <Typography variant="body2" color="textSecondary" sx={{ mb: 1.5 }}>
      {t('security.addresses.help', 'The addresses behind the most incidents in this period. Cosmos looks up no address on the internet; a country is shown only when Cosmos\' own country file knows it.')}
    </Typography>
    {top.length === 0 ? (
      <MainCard>
        <Typography color="textSecondary" sx={{ textAlign: 'center' }}>
          {t('security.addresses.empty', 'No address caused an incident in this period.')}
        </Typography>
      </MainCard>
    ) : (
      <PrettyTableView
        data={top}
        getKey={(a) => a.ip}
        columns={[
          {
            title: t('security.addresses.address', 'Address'),
            search: (a) => a.ip,
            field: (a) => <Stack spacing={0.25}>
              <strong style={{ overflowWrap: 'anywhere' }}><Text value={a.ip} max={40} /></strong>
              <Typography variant="caption" color="textSecondary">{where(a)}</Typography>
            </Stack>,
          },
          {
            title: t('security.addresses.did', 'What it did'),
            search: (a) => did(a),
            field: (a) => <Stack spacing={0.25}>
              <span style={{ overflowWrap: 'anywhere' }}>{did(a)}</span>
              <Typography variant="caption" color="textSecondary">
                {t('security.addresses.total', '{{count}} in all', { count: a.total })}
              </Typography>
            </Stack>,
          },
          {
            title: t('security.addresses.lastSeen', 'Last seen'),
            screenMin: 'md',
            field: (a) => <Tooltip title={dayjs(a.last).format('L LTS')}><span>{dayjs(a.last).fromNow()}</span></Tooltip>,
          },
          bansKnown ? {
            title: t('security.addresses.now', 'Now'),
            screenMin: 'sm',
            field: (a) => blocked(a),
          } : null,
          {
            title: '',
            clickable: true,
            style: { textAlign: 'right' },
            field: (a) => <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 1, justifyContent: 'flex-end' }}>
              <Tooltip title={t('security.addresses.showIncidents', 'Show the incidents of this address')}>
                <Button size="small" variant="outlined" startIcon={<FilterOutlined />} onClick={() => onShowAddress(a.ip)}>
                  {t('security.addresses.show', 'Incidents')}
                </Button>
              </Tooltip>
              {bansKnown && banOf(bans, a.ip) && <PermissionGuard permission={PERM_CONFIGURATION}>
                <Button size="small" variant="outlined" color="error" startIcon={<DeleteOutlined />} onClick={() => setConfirmUnban(a.ip)}>
                  {banOf(bans, a.ip).status === 'strike' ? t('mgmt.shield.bans.unstrike') : t('mgmt.shield.bans.unban')}
                </Button>
              </PermissionGuard>}
            </Box>,
          },
        ]}
      />
    )}
  </>;
};

export default Addresses;

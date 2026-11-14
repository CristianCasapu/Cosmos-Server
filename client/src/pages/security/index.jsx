// Fork addition: the security dashboard. One page for administrators who
// may read the logs: is something attacking the server now, what happened
// in the period, from which addresses, was it stopped, what can be done.
// Built on the security record (src/security, GET /cosmos/api/security/*);
// the logic is in utils/securityDashboard.js.
import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Box, Button, Grid, Stack, Tooltip, Typography } from '@mui/material';
import { RiseOutlined, FallOutlined, SyncOutlined } from '@ant-design/icons';

import * as API from '../../api';
import MainCard from '../../components/MainCard';
import { useClientInfos } from '../../utils/hooks';
import { PERM_ADMIN_READ, PERM_CONFIGURATION_READ } from '../../utils/permissions';
import {
  COUNTER_GROUPS, PERIODS, PERIOD_TEXT, changeText, groupDescription, groupLabel, statusOf, zoneName, zoneOffsetMinutes,
} from '../../utils/securityDashboard';
import Timeline from './timeline';
import Addresses from './addresses';
import Incidents from './incidents';
import Fail2banSection from './fail2banSection';

// The page refreshes this often while the tab is visible, never faster
// than every 30 seconds.
const REFRESH_MS = 60 * 1000;

const Counter = ({ id, overview, period }) => {
  const { t } = useTranslation();
  const now = (overview.current.groups[id] || {}).total || 0;
  const before = (overview.previous.groups[id] || {}).total || 0;
  const addresses = (overview.current.groups[id] || {}).addresses || 0;
  const change = changeText(now, before, period);
  return <MainCard contentSX={{ p: 2.25 }} sx={{ height: '100%' }}>
    <Stack spacing={0.5}>
      <Tooltip title={t(...groupDescription(id))}>
        <Typography variant="h6" color="textSecondary" sx={{ width: 'fit-content' }}>{t(...groupLabel(id))}</Typography>
      </Tooltip>
      <Typography variant="h4">{now}</Typography>
      <Typography variant="caption" color="textSecondary" sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
        {change.direction === 'up' && <RiseOutlined />}
        {change.direction === 'down' && <FallOutlined />}
        {t(change.key, change.text, change.params)}
      </Typography>
      {addresses > 0 && <Typography variant="caption" color="textSecondary">
        {addresses === 1
          ? t('security.counter.fromOne', 'from one address')
          : t('security.counter.from', 'from {{count}} addresses', { count: addresses })}
      </Typography>}
    </Stack>
  </MainCard>;
};

const SecurityDashboard = () => {
  const { t } = useTranslation();
  const { hasPermission } = useClientInfos();
  const mayRead = hasPermission(PERM_ADMIN_READ);
  const mayReadBans = hasPermission(PERM_CONFIGURATION_READ);
  const [period, setPeriod] = useState('24h');
  const [overview, setOverview] = useState(null);
  const [failed, setFailed] = useState(false);
  const [catalogue, setCatalogue] = useState([]);
  const [bans, setBans] = useState(null);
  const [listFrom, setListFrom] = useState(null);
  const list = useRef(null);
  const periodRef = useRef(period);
  const request = useRef(0);

  const refreshBans = useCallback(() => {
    if (!mayReadBans) return;
    API.shield.bans().then((res) => setBans(res.data || [])).catch(() => setBans(null));
  }, [mayReadBans]);

  const refresh = useCallback((withList) => {
    const id = ++request.current;
    const asked = periodRef.current;
    API.security.overview(asked, zoneOffsetMinutes()).then((res) => {
      if (id !== request.current) return;
      setOverview(res.data);
      setFailed(false);
      const start = res.data && res.data.current && res.data.current.from;
      if (withList === 'period') {
        setListFrom(start);
      } else if (withList && list.current) {
        list.current.refresh(start);
      }
    }).catch(() => {
      if (id === request.current) setFailed(true);
    });
    refreshBans();
  }, [refreshBans]);

  // the catalogue gives the server's own words for a kind the page does not know
  useEffect(() => {
    if (!mayRead) return;
    API.security.kinds().then((res) => setCatalogue(res.data || [])).catch(() => {});
  }, [mayRead]);

  // a new period: everything again
  useEffect(() => {
    if (!mayRead) return;
    periodRef.current = period;
    refresh('period');
  }, [period, mayRead]);

  // every REFRESH_MS while the tab is visible; at once when it comes back
  useEffect(() => {
    if (!mayRead) return undefined;
    let timer = null;
    const start = () => {
      if (timer === null) timer = setInterval(() => refresh(true), REFRESH_MS);
    };
    const stop = () => {
      if (timer !== null) clearInterval(timer);
      timer = null;
    };
    const onVisibility = () => {
      if (document.hidden) {
        stop();
      } else {
        refresh(true);
        start();
      }
    };
    if (!document.hidden) start();
    document.addEventListener('visibilitychange', onVisibility);
    return () => {
      stop();
      document.removeEventListener('visibilitychange', onVisibility);
    };
  }, [mayRead, refresh]);

  if (!mayRead) {
    return <Alert severity="error">{t('global.adminOnly')}</Alert>;
  }

  const status = overview ? statusOf(overview) : null;

  return <div style={{ maxWidth: '1200px', margin: 'auto' }}>
    <Grid container rowSpacing={4.5} columnSpacing={2.75}>
      <Grid item xs={12}>
        <Stack spacing={1.5}>
          <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', justifyContent: 'space-between', gap: 1 }}>
            <Stack direction="row" alignItems="center" spacing={0} sx={{ flexWrap: 'wrap' }}>
              {PERIODS.map((p) => <Button
                key={p}
                size="small"
                onClick={() => setPeriod(p)}
                color={period === p ? 'primary' : 'secondary'}
                variant={period === p ? 'outlined' : 'text'}
              >
                {t(...PERIOD_TEXT[p])}
              </Button>)}
            </Stack>
            <Stack direction="row" alignItems="center" spacing={1}>
              <Typography variant="caption" color="textSecondary">
                {t('security.zone', 'Times in {{zone}}', { zone: zoneName() })}
              </Typography>
              <Tooltip title={t('global.refresh')}>
                <Button size="small" variant="outlined" onClick={() => refresh(true)} aria-label={t('global.refresh')} sx={{ minWidth: 0, px: 1 }}>
                  <SyncOutlined />
                </Button>
              </Tooltip>
            </Stack>
          </Box>
          {failed && <Alert severity="warning">
            {t('security.failed', 'The security record cannot be read just now. On a new installation it starts after the first restart of Cosmos. The page tries again every minute.')}
          </Alert>}
          {status && <Alert severity={status.severity} sx={{ '& .MuiAlert-message': { fontSize: '1rem' } }}>
            <strong>{t(status.key, status.text, status.params)}</strong>
          </Alert>}
        </Stack>
      </Grid>

      {overview && <>
        <Grid item xs={12}>
          <Grid container spacing={2}>
            {COUNTER_GROUPS.map((id) => <Grid item key={id} xs={6} sm={4} md>
              <Counter id={id} overview={overview} period={period} />
            </Grid>)}
          </Grid>
        </Grid>

        <Grid item xs={12}>
          <Timeline overview={overview} />
        </Grid>

        <Grid item xs={12}>
          <Addresses
            overview={overview}
            bans={bans}
            bansKnown={mayReadBans && bans !== null}
            catalogue={catalogue}
            onShowAddress={(ip) => list.current && list.current.showAddress(ip)}
            onUnbanned={refreshBans}
          />
        </Grid>
      </>}

      {/* Fork addition: the Fail2ban status goes here (pages/security/fail2banSection.jsx) */}
      <Grid item xs={12} sx={{ '&:empty': { display: 'none' } }}>
        <Fail2banSection period={period} />
      </Grid>

      <Grid item xs={12}>
        <Incidents ref={list} catalogue={catalogue} from={listFrom} />
      </Grid>
    </Grid>
  </div>;
};

export default SecurityDashboard;

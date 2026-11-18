// Fork addition (plan 50): the page of an app's guide, /cosmos-ui/guide/<app>#<section>.
// Behind the sign-in like every page of the panel; the app is looked up in the
// reader's own GET /api/dashboard, which lists only the URLs this reader may
// see, so someone who cannot see the app gets "not found" and nothing of it.
import * as React from 'react';
import { Button, Stack, Typography } from '@mui/material';
import { Link, useParams } from 'react-router';
import { useTranslation } from 'react-i18next';

import * as API from '../../api';
import MainCard from '../../components/MainCard';
import Back from '../../components/back';
import EmptyState from '../../components/emptyState';
import { AppGuide, hasGuide } from '../../components/appGuide';
import { useOpenContext } from '../../components/appOpen';
import { groupApps } from '../../utils/appInterfaces';
import { findGuideApp } from '../../utils/guides';

const GuidePage = () => {
  const { t } = useTranslation();
  const { app: key } = useParams();
  const [routes, setRoutes] = React.useState(null);
  const [status, setStatus] = React.useState(null);
  React.useEffect(() => {
    API.config.getDashboard().then((res) => setRoutes((res && res.data) || [])).catch(() => setRoutes([]));
    API.getStatus().then((res) => setStatus((res && res.data) || {})).catch(() => setStatus({}));
  }, []);
  const ctx = useOpenContext({ status });
  const app = React.useMemo(() => (routes ? findGuideApp(groupApps(routes), key) : null), [routes, key]);
  if (routes === null || status === null) return null;
  if (!app || !hasGuide(app.routes)) {
    return <EmptyState title={t('mgmt.guide.missing.title', 'There is no guide here')}
      action={<Button component={Link} to="/cosmos-ui" variant="contained">{t('mgmt.guide.missing.home', 'Back to the home page')}</Button>}>
      {t('mgmt.guide.missing.text', 'The app does not exist, has no guide, or is not one you may see.')}
    </EmptyState>;
  }
  const name = app.container || (app.routes[0] && app.routes[0].Name) || key;
  return <Stack spacing={1} sx={{ minWidth: 0 }}>
    <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }} className="guide-no-print">
      <Back />
      <Typography>{name}</Typography>
    </Stack>
    <MainCard title={t('mgmt.guide.title', 'How to use {{name}}', { name })} sx={{ minWidth: 0 }}>
      <AppGuide app={app} ctx={ctx} status={status} name={name} />
    </MainCard>
  </Stack>;
};

export default GuidePage;

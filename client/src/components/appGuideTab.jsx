// Fork addition (plan 50): the "How to use it" tab of a container's page,
// when the app has a guide (the same guide as /cosmos-ui/guide/<app>).
import * as React from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Stack } from '@mui/material';
import { Link } from 'react-router';
import { LinkOutlined } from '@ant-design/icons';

import { AppGuide, hasGuide } from './appGuide';
import { useDashboardRoutes, useOpenContext, useVisitorStatus } from './appOpen';
import { groupApps } from '../utils/appInterfaces';
import { findGuideApp, guidePath } from '../utils/guides';

const GuideTab = ({ app, name }) => {
  const { t } = useTranslation();
  const status = useVisitorStatus();
  const ctx = useOpenContext({ status });
  return <Stack spacing={2} sx={{ minWidth: 0 }}>
    <Stack direction="row"><Button component={Link} to={guidePath(app)} startIcon={<LinkOutlined />} sx={{ minHeight: 44 }}>{t('mgmt.guide.ownPage', 'Open on its own page')}</Button></Stack>
    <AppGuide app={app} ctx={ctx} status={status} name={name} />
  </Stack>;
};

export const useGuideTabs = (containerName) => {
  const { t } = useTranslation();
  const dashboard = useDashboardRoutes();
  const name = String(containerName || '').replace(/^\//, '').toLowerCase();
  const app = React.useMemo(() => (dashboard ? findGuideApp(groupApps(dashboard), name) : null), [dashboard, name]);
  if (!app || app.container !== name || !hasGuide(app.routes)) return [];
  return [{ title: t('mgmt.guide.tab', 'How to use it'), children: <GuideTab app={app} name={name} /> }];
};

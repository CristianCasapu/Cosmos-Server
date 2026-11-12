import React, { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import { Stack, CircularProgress, Card, Typography, useMediaQuery, Chip, Button } from '@mui/material';
import { CloudServerOutlined, CalendarOutlined, FolderOutlined, ReloadOutlined, InfoCircleOutlined, CloudOutlined } from '@ant-design/icons';
import * as API from '../../api';
import { crontabToText } from '../../utils/indexs';
import ResponsiveButton from '../../components/responseiveButton';
import MainCard from '../../components/MainCard';
import { useTranslation } from 'react-i18next';
import BackupOverview from './overview';
import PrettyTabbedView from '../../components/tabbedView/tabbedView';
import Back from '../../components/back';
import BackupRestore from './restore';
import EventExplorerStandalone from '../dashboard/eventsExplorerStandalone';
import { Backups } from './backups';
import { Repositories } from './repositories';
import PremiumSalesPage from '../../utils/free';
import VMWarning from '../storage/vmWarning';
import proFeatures from '../../pro';

export default function AllBackupsIndex() {
  const { t } = useTranslation();
  const [coStatus, setCoStatus] = React.useState(null);

  const refreshStatus = () => {
    API.getStatus().then((res) => {
      setCoStatus(res.data);
    });
  }

  React.useEffect(() => {
    refreshStatus();
  }, []);
  
  let containerized = coStatus && coStatus.containerized;
  let free = coStatus && !coStatus.Licence;
  
  return free ? <>
  {containerized && <VMWarning />}
  <PremiumSalesPage salesKey="backup" />
  </>
  : <div>
    <Stack spacing={1}>
      <PrettyTabbedView
      tabs={[
        {
          title: t('mgmt.backup.backups'),
          children: <Backups />
        },
        {
          title: t('mgmt.backup.repositories'),
          children: <Repositories />
        },
        // Pro cluster state: the records of every Pro feature, and the import
        // that works when the per-feature pages are empty.
        ...(proFeatures.ClusterStateTab && coStatus && coStatus.ConstellationName ? [{
          title: t('mgmt.state.tab'),
          children: <proFeatures.ClusterStateTab />
        }] : []),
      ]} />
    </Stack>
  </div>;
}
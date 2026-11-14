import * as React from 'react';
import MainCard from '../../../components/MainCard';
import RestartModal from '../../config/users/restart';
import { Alert, Chip, Divider, Stack, useMediaQuery } from '@mui/material';
import HostChip from '../../../components/hostChip';
import { RouteMode, RouteSecurity } from '../../../components/routeComponents';
import { getFaviconURL } from '../../../utils/routes';
import * as API from '../../../api';
import { CheckOutlined, ClockCircleOutlined, DashboardOutlined, DeleteOutlined, DownOutlined, LockOutlined, UpOutlined } from "@ant-design/icons";
import PrettyTabbedView from '../../../components/tabbedView/tabbedView';
import Back from '../../../components/back';
import { useParams } from 'react-router';
import ContainerOverview from './overview';
import Logs from './logs';
import DockerContainerSetup from './setup';
import NetworkContainerSetup from './network';
import VolumeContainerSetup from './volumes';
import DockerTerminal from './terminal';
import ContainerMetrics from '../../dashboard/containerMetrics';
import EventExplorerStandalone from '../../dashboard/eventsExplorerStandalone';
import ContainerComposeEdit from './compose-editor';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Button } from '@mui/material';
import EmptyState from '../../../components/emptyState'; // Fork addition: a container that cannot be shown

const ContainerIndex = () => {
  const { t } = useTranslation();
  const { containerName } = useParams();
  const [container, setContainer] = React.useState(null);
  const [config, setConfig] = React.useState(null);
  const [selfName, setSelfName] = React.useState("");
  const [updatesAvailable, setUpdatesAvailable] = React.useState(null);
  const [missing, setMissing] = React.useState(false); // Fork addition
  
  const refreshContainer = () => {
    return Promise.all([API.docker.get(containerName).then((res) => {
      setContainer(res.data);
      setMissing(false);
    }).catch((error) => {
      // Fork addition: the page span forever when the container was gone (the error itself is shown by the API layer)
      setMissing(true);
    }),
    API.config.get().then((res) => {
      setConfig(res.data);
      setUpdatesAvailable(res.updates);
      setSelfName(res.hostname);
    })]);
  };

  React.useEffect(() => {
    refreshContainer();
  }, []);

  return <div>
    <Stack spacing={1}>
      <Stack direction="row" spacing={1} alignItems="center">
        <Back />
        <div>{containerName}</div>
      </Stack>

      {missing && !container && <EmptyState
        title={t('mgmt.servapps.container.missing.title', 'There is no container named {{name}}', { name: containerName })}
        action={<Button component={Link} to="/cosmos-ui/servapps" variant="contained">{t('mgmt.servapps.container.missing.back', 'Back to the ServApps')}</Button>}
      >
        {t('mgmt.servapps.container.missing.text', 'It may have been renamed or deleted, or the Docker service did not answer. The list of ServApps shows what exists now.')}
      </EmptyState>}

      {!(missing && !container) && <PrettyTabbedView 
      isLoading={!container || !config}
      tabs={[
        {
          title: t('mgmt.servapps.overview'),
          children: <ContainerOverview updatesAvailable={updatesAvailable} selfName={selfName} refresh={refreshContainer} containerInfo={container} config={config}/>
        },
        {
          title: t('mgmt.scheduler.list.action.logs'),
          children: <Logs containerInfo={container} config={config}/>
        },
        {
          title: t('menu-items.navigation.monitoringTitle'),
          children: <ContainerMetrics containerName={containerName}/>
        },
        {
          title: t('navigation.monitoring.eventsTitle'),
          children: <EventExplorerStandalone initSearch={`{"object":"container@${containerName}"}`}/>
        },
        {
          title: t('mgmt.servapps.terminal'),
          children: <DockerTerminal refresh={refreshContainer} containerInfo={container} config={config}/>
        },
        {
          title: 'Compose',
          children: <ContainerComposeEdit updatesAvailable={updatesAvailable} selfName={selfName} refresh={refreshContainer} containerInfo={container} config={config}/>
        },
        {
          title: 'Docker',
          children: <DockerContainerSetup refresh={refreshContainer} containerInfo={container} config={config}/>
        },
        {
          title: t('global.network'),
          children: <NetworkContainerSetup refresh={refreshContainer} containerInfo={container} config={config}/>
        },
        {
          title: t('menu-items.management.storage'),
          children: <VolumeContainerSetup refresh={refreshContainer} containerInfo={container} config={config}/>
        },
      ]} />}
    </Stack>
  </div>;
}

export default ContainerIndex;
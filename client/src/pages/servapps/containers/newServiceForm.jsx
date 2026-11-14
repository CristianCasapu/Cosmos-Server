import * as React from 'react';
import MainCard from '../../../components/MainCard';
import RestartModal from '../../config/users/restart';
import { Alert, Button, Checkbox, Chip, Divider, Stack, useMediaQuery } from '@mui/material';
import HostChip from '../../../components/hostChip';
import { RouteMode, RouteSecurity } from '../../../components/routeComponents';
import { getFaviconURL, getHostnameFromName } from '../../../utils/routes';
import * as API from '../../../api';
import { ArrowLeftOutlined, ArrowRightOutlined, CheckOutlined, ClockCircleOutlined, DashboardOutlined, DeleteOutlined, DownOutlined, LockOutlined, UpOutlined } from "@ant-design/icons";
import PrettyTabbedView from '../../../components/tabbedView/tabbedView';
import Back from '../../../components/back';
import { useParams } from 'react-router';
import ContainerOverview from './overview';
import Logs from './logs';
import DockerContainerSetup from './setup';
import NetworkContainerSetup from './network';
import VolumeContainerSetup from './volumes';
import DockerTerminal from './terminal';
import NewDockerService from './newService';
import RouteManagement from '../../config/routes/routeman';
import { localAddress } from '../../../components/hostnameSuggestions'; // Fork addition
import ImageDefaults from './imageDefaults'; // Fork addition
import { usePortSources, ComposePortConflicts } from '../../../components/portConflicts'; // Fork addition: host ports that are already taken
import { useTranslation } from 'react-i18next';
import { networkModeValue, quickSetup, serviceFromForm, withDockerTab } from '../../../utils/newContainer'; // Fork addition
import { containerPorts, newAppRoute, proposePort, schemeForPort } from '../../../utils/routeGuide'; // Fork addition
import defaultport from '../defaultport.json'; // Fork addition

const NewDockerServiceForm = () => {
  const { t } = useTranslation();
  const [currentTab, setCurrentTab] = React.useState(0);
  const [maxTab, setMaxTab] = React.useState(0);
  const [config, setConfig] = React.useState(null);
  // Fork addition: the image name once its field has been left; its defaults are then offered
  const [imageToInspect, setImageToInspect] = React.useState('');
  // Fork addition: who holds which host port, and whether a row of the Network step cannot work
  const portSources = usePortSources();
  const [portsBlocked, setPortsBlocked] = React.useState(false);
  // Fork addition: the URL "Review and create" proposed, shown by the URL tab if it is opened afterwards
  const [quickRoute, setQuickRoute] = React.useState(null);
  const [containerInfo, setContainerInfo] = React.useState({
    Name: '',
    Names: [],
    Ports: [], // fake for contianerPicker
    Route: null,
    Config: {
      Env: [],
      Labels: {},
      ExposedPorts: [],
      Volumes: {},
      Name: '',
      Image: '',
      Tty: true,
      OpenStdin: true,
    },
    HostConfig: {
      PortBindings: [],
      RestartPolicy: {
        Name: 'always',
      },
      Mounts: [],
    },
    NetworkSettings: {
      Networks: {},
      Ports: [],
    },
  });
  
  const refreshConfig = () => {
    API.config.get().then((res) => {
      setConfig(res.data);
    });
  };

  React.useEffect(() => {
    refreshConfig();
  }, []);

  // Fork addition: every field of the form reaches the request (utils/newContainer.js); upstream left out
  // the user, the limits, the network mode and the interactive tick
  let service = serviceFromForm(containerInfo);

  // Fork addition: one confirmation by default. "Review and create" fills in what can be proposed (the name from
  // the image, a local URL on the image's usual or declared port) and goes to the review, which proposes the rest
  // (restart policy, time zone, where the data goes) and says in words what will be created.
  const quick = () => <Stack style={{ maxWidth: '1000px', width: '100%', margin: 'auto' }} spacing={1}>
    <Button
      variant="contained"
      color="primary"
      fullWidth
      data-quick-review
      disabled={!String(containerInfo.Config.Image || '').trim() || !config /* the addresses need the settings */}
      onClick={() => {
        const image = String(containerInfo.Config.Image || '').trim();
        const port = proposePort(image, containerPorts(containerInfo), defaultport);
        const { info } = quickSetup(containerInfo, {
          port,
          route: (name, number) => newAppRoute({ name, host: localAddress(name, config), port: number, scheme: schemeForPort(number) }),
        });
        setContainerInfo(info);
        if (info.Route && info.Route !== containerInfo.Route) setQuickRoute(info.Route);
        setMaxTab(4);
        setCurrentTab(4);
      }}
    >
      {t('mgmt.servapps.creation.quick', 'Review and create')}
    </Button>
    <div style={{ fontSize: '0.8em', opacity: 0.8 }}>
      {t('mgmt.servapps.creation.quickHint', 'Cosmos proposes the rest (a local address, the restart policy, where the data goes) and shows everything on one page before anything is created. The other tabs stay open for changes.')}
    </div>
  </Stack>;

  const nav = () => <Stack
    direction="row"
    spacing={1}
    style={{
      maxWidth: '1000px',
      width: '100%',
      margin: 'auto',
      marginTop: '20px',
    }}
    >
    <Button
      variant="contained"
      startIcon={<ArrowLeftOutlined />}
      disabled={currentTab === 0}  
      fullWidth
      onClick={() => {
        setCurrentTab(currentTab - 1);
      }}
    >
        {t('newInstall.previousButton')}
    </Button>
    <Button
      variant="contained"
      fullWidth
      endIcon={<ArrowRightOutlined />}
      disabled={(currentTab === 4) || containerInfo.Name === '' || containerInfo.Config.Image === '' || (currentTab === 2 && portsBlocked) /* Fork addition */}
      onClick={() => {
        setCurrentTab(currentTab + 1);
        setMaxTab(Math.max(currentTab + 1, maxTab));
      }}
    >
        {t('global.next')}
    </Button>
    </Stack>

  return <div>
    <Stack spacing={1}>
      <Stack direction="row" spacing={1} alignItems="center">
        <Back />
        <div>{t('mgmt.servApp.newServAppButton')}</div>
      </Stack>

      {<PrettyTabbedView
      currentTab={currentTab}
      setCurrentTab={setCurrentTab}
      tabs={[
        {
          title: 'Docker',
          disabled: false,
          children: <Stack spacing={2} onBlur={(e) => {
            // Fork addition
            if (e.target && e.target.name === 'image') setImageToInspect((e.target.value || '').trim());
          }}><DockerContainerSetup newContainer containerInfo={containerInfo}
            afterImage={<ImageDefaults image={imageToInspect} containerInfo={containerInfo} onApply={setContainerInfo} /> /* Fork addition */}
            OnChange={(values) => {
            setContainerInfo(withDockerTab(containerInfo, values)); // Fork addition: user, limits and the interactive tick are kept
          }} 
          OnForceSecure={(value) => {
            const newValues = {
              ...containerInfo,
              Config: {
                ...containerInfo.Config,
                Labels: {
                  ...containerInfo.Config.Labels,
                  'cosmos-force-network-secured': ''+value,
                },
              },
            }
            setContainerInfo(newValues);
          }}/>{quick()}{nav()}</Stack>
        },
        {
          title: 'URL',
          disabled: maxTab < 1,
          children:  <Stack spacing={2}>
            <MainCard  style={{ maxWidth: '1000px', width: '100%', margin: 'auto', position: 'relative' }}>
              <Checkbox
                checked={containerInfo.CreateRoute}
                onChange={(e) => {
                  const newValues = {
                    ...containerInfo,
                    CreateRoute: e.target.checked,
                  }
                  setContainerInfo(newValues);
                }}
              />{t('mgmt.servApp.url')}
            </MainCard>
            {containerInfo.CreateRoute && <RouteManagement TargetContainer={containerInfo} 
              routeConfig={quickRoute /* Fork addition */ || {
                Target: "http://"+containerInfo.Name.replace('/', '') + ":",
                Mode: "SERVAPP",
                Name: containerInfo.Name.replace('/', ''),
                Description: t('mgmt.servApp.exposeDesc').replace('containerName',containerInfo.Name.replace('/', '')) + containerInfo.Name.replace('/', ''),
                UseHost: true,
                // Fork addition: every app gets a local address; a domain can be picked from the suggestions
                Host: localAddress(containerInfo.Name, config),
                UsePathPrefix: false,
                PathPrefix: '',
                CORSOrigin: '',
                StripPathPrefix: false,
                AuthEnabled: false,
                Timeout: 14400000,
                ThrottlePerMinute: 10000,
                BlockCommonBots: true,
                SmartShield: {
                  Enabled: true,
                }
              }} 
              routeNames={[]}
              config={config /* Fork addition: for the address suggestions */}
              setRouteConfig={(newRoute) => {
                const newValues = {
                  ...containerInfo,
                  Route: newRoute,
                }
                setContainerInfo(newValues);
              }}
              up={() => {}}
              down={() => {}}
              deleteRoute={() => {}}
              noControls
              lockTarget
          />}{nav()}</Stack>
        },
        {
          title: t('global.network'),
          disabled: maxTab < 1,
          children: <Stack spacing={2}><NetworkContainerSetup newContainer containerInfo={containerInfo} portSources={portSources} onPortsBlocked={setPortsBlocked} /* Fork addition: both */ OnChange={(values) => {
            const newValues = {
              ...containerInfo,
              Config: {
                ...containerInfo.Config,
                ExposedPorts:
                  values.ports.map((port) => {
                    return `${port.port}/${port.protocol}`;
                  }),
              },
              HostConfig: {
                ...containerInfo.HostConfig,
                Dns: (values.networkMode || '').startsWith('container:') ? [] : (values.dns || '').split(',').map((s) => s.trim()).filter(Boolean),
                NetworkMode: networkModeValue(values.networkMode), // Fork addition: the network mode and "use as VPN" were never sent
                // Fork addition: a port without a host port is not published (the form says so); "":80 published a random one
                PortBindings: values.ports.filter((port) => String(port.hostPort === undefined || port.hostPort === null ? '' : port.hostPort).trim() !== '').map((port) => {
                  return `${port.hostPort}:${port.port}/${port.protocol}`;
                }),
              },
              NetworkSettings: {
                ...containerInfo.NetworkSettings,
                Ports: values.ports && values.ports.reduce((acc, port) => {
                  acc[`${port.port}/${port.protocol}`] = [{
                    HostIp: '',
                    HostPort: `${port.hostPort}`,
                  }];
                  return acc;
                }, {}),
              },
            }
            setContainerInfo(newValues);
          }} OnConnect={(net) => {
            const newValues = {
              ...containerInfo,
              NetworkSettings: {
                ...containerInfo.NetworkSettings,
                Networks: {
                  ...containerInfo.NetworkSettings.Networks,
                  [net]: {
                    Name: net,
                  },
                },
              },
            }
            setContainerInfo(newValues);
          }} OnDisconnect={(net) => {
            let newContainerInfo = {
              ...containerInfo,
            }
            delete newContainerInfo.NetworkSettings.Networks[net];
            setContainerInfo(newContainerInfo);
          }}/>{nav()}</Stack>
        },
        {
          title: t('menu-items.management.storage'),
          disabled: maxTab < 1 || portsBlocked, // Fork addition: not past a host port that cannot work
          children: <Stack spacing={2}><VolumeContainerSetup newContainer containerInfo={containerInfo} OnChange={(values) => {
            const newValues = {
              ...containerInfo,
              HostConfig: {
                ...containerInfo.HostConfig,
                Mounts: values.volumes.map((volume) => {
                  return {
                    Type: volume.Type,
                    Source: volume.Source,
                    Target: volume.Target,
                  };
                }),
              },
            }
            setContainerInfo(newValues);
          }} />{nav()}</Stack>
        },
        {
          title: t('mgmt.servApp.newContainer.reviewStartButton'),
          disabled: maxTab < 1 || portsBlocked, // Fork addition
          children: <Stack style={{ width: '100%', margin: 'auto', maxWidth: '1000px' }} spacing={2}><ComposePortConflicts service={service} sources={portSources} />{/* Fork addition */}<NewDockerService service={service} />{nav()}</Stack>
        }
      ]} />}

    </Stack>
  </div>;
}

export default NewDockerServiceForm;
import React from 'react';
import { Formik } from 'formik';
import { Button, Stack, Grid, MenuItem, TextField, IconButton, FormHelperText, CircularProgress, useTheme, Alert, useMediaQuery, Tooltip } from '@mui/material'; // Fork addition: Tooltip, for the icon-only buttons
import MainCard from '../../../components/MainCard';
import { CosmosCheckbox, CosmosFormDivider, CosmosInputText, CosmosSelect }
  from '../../config/users/formShortcuts';
import { ApiOutlined, CheckCircleOutlined, CloseCircleOutlined, DeleteOutlined, PlusCircleOutlined } from '@ant-design/icons';
import * as API from '../../../api';
import { LoadingButton } from '@mui/lab';
import PrettyTableView from '../../../components/tableView/prettyTableView';
import { NetworksColumns } from '../networks';
import NewNetworkButton from '../createNetwork';
import LinkContainersButton from '../linkContainersButton';
import { useTranslation } from 'react-i18next';
import { CosmosContainerPicker } from '../../config/users/containerPicker';
import PermissionGuard from '../../../components/permissionGuard';
import { PERM_RESOURCES } from '../../../utils/permissions';
import { portHostIp, isHostIp } from './forkContainerFixes'; // Fork addition: the host address of a published port
import { usePortHolders, hasBlockingPortConflict, PortConflictHint, PortConflictReport, EDIT_FORM } from '../../../components/portConflicts'; // Fork addition: host ports that are already taken

const NetworkContainerSetup = ({ config, containerInfo, refresh, newContainer, OnChange, OnConnect, OnDisconnect, portSources, onPortsBlocked }) => {
  const { t } = useTranslation();
  // Fork addition: who holds which host port (this container's own ports do not count)
  const portHolders = usePortHolders(newContainer ? '' : containerInfo.Name, portSources);
  const portRules = newContainer ? undefined : EDIT_FORM;
  const [networks, setNetworks] = React.useState([]);
  const theme = useTheme();
  const isDark = theme.palette.mode === 'dark';
  const isMobile = useMediaQuery(theme.breakpoints.down('sm'));
  const padding = isMobile ? '6px 4px' : '12px 10px';

  const isForceSecure = containerInfo.Config.Labels.hasOwnProperty('cosmos-force-network-secured') &&
  containerInfo.Config.Labels['cosmos-force-network-secured'] === 'true';

  React.useEffect(() => {
    API.docker.networkList().then((res) => {
      setNetworks(res.data);
    });
  }, []);

  const refreshAll = () => {
    if(refresh)
      refresh().then(() => {
        API.docker.networkList().then((res) => {
          setNetworks(res.data);
        });
      });
    else 
      API.docker.networkList().then((res) => {
        setNetworks(res.data);
      });
  };

  const connect = (network) => {
    if(!OnConnect) {
      return API.docker.attachNetwork(containerInfo.Name.replace('/', ''), network).then(() => {
        refreshAll();
      });
    } else {
      OnConnect(network);
      refreshAll();
    }
  }

  const disconnect = (network) => {
    if(!OnDisconnect) {
      return API.docker.detachNetwork(containerInfo.Name.replace('/', ''), network).then(() => {
        refreshAll();
      });
    } else {
      OnDisconnect(network);
      refreshAll();
    }
  }

  const getPortBindings = () => {
    let portsBound = [];
    let portsConfig = containerInfo.NetworkSettings.Ports;

    return Object.keys(portsConfig).map((contPort) => {
      return portsConfig[contPort] ? portsConfig[contPort].map((hostPort) => {
        const hostIp = portHostIp(containerInfo, contPort, hostPort); // Fork addition
        let key = contPort + ":" + hostIp + ":" + hostPort.HostPort + "/" + hostPort.protocol;
        if (portsBound.includes(key)) return null;
        portsBound.push(key);
        return {
          port: contPort.split('/')[0],
          protocol: contPort.split('/')[1],
          hostPort: hostPort.HostPort,
          hostIp, // Fork addition
        };
      }) : {
        port: contPort.split('/')[0],
        protocol: contPort.split('/')[1],
        hostPort: '',
      }
    }).flat().filter((p) => p);
  }

  return (<Stack spacing={2}>
    <div style={{ maxWidth: '1000px', width: '100%', margin: 'auto', position: 'relative' }}>
      <Formik
        initialValues={{
          networkMode: containerInfo.HostConfig.NetworkMode,
          ports: getPortBindings(),
          dns: (containerInfo.HostConfig.Dns || []).join(', '),
          Container: (() => {
            if(!containerInfo || !containerInfo.HostConfig || !containerInfo.HostConfig.NetworkMode) return "";
            if(!containerInfo.HostConfig.NetworkMode.startsWith("container:")) return "";
            return containerInfo.HostConfig.NetworkMode.replace("container:", "");
          })()
        }}
        validate={(values) => {
          const errors = {};
          // Fork addition: a mistyped address must not reach Docker
          if (!newContainer && values.ports.some((port) => !isHostIp(port.hostIp))) {
            errors.ports = t('mgmt.servApps.networks.hostIpInput.hostIpValidation', 'Enter an IPv4 or IPv6 address, or leave it empty for every address');
          }
          // Fork addition: a host port that is taken is said under its row; nothing is sent meanwhile
          if (!newContainer && hasBlockingPortConflict(values.ports, portHolders, portRules)) {
            errors.ports = t('mgmt.servApps.ports.blocked', 'A host port in the list cannot be used. See the note under it.');
          }
          OnChange && OnChange(values);
          return errors;
        }}
        onSubmit={async (values, { setErrors, setStatus, setSubmitting }) => {
          if(newContainer) return false;
          setSubmitting(true);
          const realvalues = {
            portBindings: {},
            networkMode: values.networkMode,
            // a container that shares the network of another one takes its DNS too
            dns: (values.networkMode || '').startsWith('container:') ? [] : values.dns.split(',').map((s) => s.trim()).filter(Boolean),
          };
          values.ports.forEach((port) => {
            let key = `${port.port}/${port.protocol}`;
            
            if (port.hostPort) {
              if(!realvalues.portBindings[key]) realvalues.portBindings[key] = [];
              
              realvalues.portBindings[`${port.port}/${port.protocol}`].push({
                HostIp: port.hostIp || '', // Fork addition: without it a port bound to 127.0.0.1 became public
                HostPort: port.hostPort,
              })
            }
          });
          return API.docker.updateContainer(containerInfo.Name.replace('/', ''), realvalues)
            .then((res) => {
              setStatus({ success: true });
              setSubmitting(false);
              refresh && refresh();
            }
            ).catch((err) => {
              setStatus({ success: false });
              setErrors({ submit: err.message });
              setSubmitting(false);
              refresh && refresh();
            });
        }}
      >
        {(formik) => (
          <form noValidate onSubmit={formik.handleSubmit}>
            <Stack spacing={2}>

              <MainCard title={t('mgmt.servApps.newContainer.networkSettingsTitle')}>
                <Stack spacing={4}>
                  {/* {isForceSecure && (
                    <Alert severity="warning" style={{ marginBottom: '0px' }}>
                      {t('mgmt.servApps.networks.forcedSecurityWarning')}          
                    </Alert>
                  )} */}
                  <CosmosInputText
                    label={t('mgmt.servApps.networks.modeInput.modeLabel')}
                    name="networkMode"
                    placeholder={'default'}
                    formik={formik}
                    onChange={(e) => {
                      formik.setFieldValue('Container', '');
                    }}
                  />
                  <CosmosContainerPicker
                    formik={formik}
                    onTargetChange={(_, name) => {
                      if(!name) return;
                      formik.setFieldValue('networkMode', `container:${name}`);
                    }}
                    name='Container'
                    label={t('mgmt.servApps.networks.useAsVPN')}
                    nameOnly
                  />
                  {!(formik.values.networkMode || '').startsWith('container:') && <CosmosInputText
                    label={t('mgmt.servApps.networks.dnsLabel')}
                    name="dns"
                    placeholder={'1.1.1.1, 8.8.8.8'}
                    formik={formik}
                  />}
                  <CosmosFormDivider title={t('mgmt.servApps.networks.exposePortsTitle')} />
                  <div>
                    {formik.values.ports.map((port, idx) => (
                      <Grid container key={idx}>
                        {/* Fork addition: the address the port is bound to (not sent by the new container form) */}
                        {!newContainer && <Grid item xs={12} md={3} style={{ padding }}>
                          <TextField
                            fullWidth
                            label={t('mgmt.servApps.networks.hostIpInput.hostIpLabel', 'Host IP (optional)')}
                            value={port.hostIp || ''}
                            error={!isHostIp(port.hostIp)}
                            helperText={!isHostIp(port.hostIp) && t('mgmt.servApps.networks.hostIpInput.hostIpValidation', 'Enter an IPv4 or IPv6 address, or leave it empty for every address')}
                            onChange={(e) => {
                              const newports = [...formik.values.ports];
                              newports[idx].hostIp = e.target.value.trim();
                              formik.setFieldValue('ports', newports);
                            }}
                          />
                        </Grid>}
                        <Grid item xs={4} md={newContainer ? 4 : 3} style={{ padding }}>
                          <TextField
                            fullWidth
                            label="Host port"
                            value={'' + port.hostPort}
                            onChange={(e) => {
                              const newports = [...formik.values.ports];
                              newports[idx].hostPort = e.target.value;
                              formik.setFieldValue('ports', newports);
                            }}
                          />
                        </Grid>
                        <Grid item xs={4} md={newContainer ? 4 : 3} style={{ padding }}>
                          <TextField
                            label={t('mgmt.servApps.networks.containerPortInput.containerPortLabel')}
                            fullWidth
                            value={port.port}
                            onChange={(e) => {
                              const newports = [...formik.values.ports];
                              newports[idx].port = e.target.value;
                              formik.setFieldValue('ports', newports);
                            }}
                          />
                        </Grid>
                        <Grid item xs={3} md={newContainer ? 3 : 2} style={{ padding }}>
                          <TextField
                            className="px-2 my-2"
                            variant="outlined"
                            name='protocol'
                            id='protocol'
                            select
                            value={port.protocol}
                            onChange={(e) => {
                              const newports = [...formik.values.ports];
                              newports[idx].protocol = e.target.value;
                              formik.setFieldValue('ports', newports);
                            }}
                          >
                            <MenuItem value="tcp">TCP</MenuItem>
                            <MenuItem value="udp">UDP</MenuItem>
                          </TextField>
                        </Grid>
                        <Grid item xs={1} style={{ padding }}>
                          <Tooltip title={t('global.delete', 'Delete')}><IconButton
                            fullWidth
                            variant="outlined"
                            color="error"
                            onClick={() => {
                              const newports = [...formik.values.ports];
                              newports.splice(idx, 1);
                              formik.setFieldValue('ports', newports);
                            }}
                          >
                            <DeleteOutlined />
                          </IconButton></Tooltip>
                        </Grid>
                        {/* Fork addition: says when the host port is already taken, and by whom */}
                        <PortConflictHint ports={formik.values.ports} index={idx} holders={portHolders} options={portRules} style={{ padding, paddingTop: 0 }} />
                      </Grid>
                    ))}
                    {/* Fork addition: lets the new container form hold its "Next" while a host port cannot work */}
                    <PortConflictReport ports={formik.values.ports} holders={portHolders} options={portRules} onBlocked={onPortsBlocked} />
                    <PermissionGuard permission={PERM_RESOURCES}><Tooltip title={t('global.addAction', 'Add')}><IconButton
                      fullWidth
                      variant="outlined"
                      color="primary"
                      size='large'
                      onClick={() => {
                        const newports = [...formik.values.ports];
                        newports.push({
                          port: '',
                          protocol: 'tcp',
                          hostPort: '',
                        });
                        formik.setFieldValue('ports', newports);
                      }}
                    >
                      <PlusCircleOutlined />
                    </IconButton></Tooltip></PermissionGuard>
                  </div>
                  <div>
                    <Stack direction="column" spacing={2}>
                      {formik.errors.submit && (
                        <Grid item xs={12}>
                          <FormHelperText error>{formik.errors.submit}</FormHelperText>
                        </Grid>
                      )}
                      {!newContainer && <PermissionGuard permission={PERM_RESOURCES}><LoadingButton
                        fullWidth
                        disableElevation
                        disabled={formik.errors.submit || hasBlockingPortConflict(formik.values.ports, portHolders, portRules) /* Fork addition */}
                        loading={formik.isSubmitting}
                        size="large"
                        type="submit"
                        variant="contained"
                        color="primary"
                      >
                        {t('mgmt.servApps.networks.updatePortsButton')}
                      </LoadingButton></PermissionGuard>}
                    </Stack>
                  </div>
                </Stack>
              </MainCard>
              <MainCard title={t('global.networks')}>
                <Stack spacing={2}>

                {networks && <Stack spacing={2}>
                  {Object.keys(containerInfo.NetworkSettings.Networks).map((networkName) => {
                    const network = networks.find((n) => n.Name === networkName);
                    if (!network) {
                      return <Alert severity="error">
                        {t('mgmt.servApps.networks.removedNetConnectedError')} <strong>{networkName}</strong>. 
                        {t('mgmt.servApps.networks.removedNetConnectedEitherRecreate')}
                        <Button
                          style={{ marginLeft: '10px' }}
                          variant="outlined"
                          color="primary"
                          onClick={() => {
                            disconnect(networkName);
                          }}
                        >
                          {t('mgmt.servApps.networks.removedNetConnectedDisconnect')}
                        </Button>
                      </Alert>
                    }
                  })}
                </Stack>}

                {networks && <PrettyTableView
                  data={networks}
                  sort={(a, b) => a.Name > b.Name}
                  buttons={[
                    <PermissionGuard permission={PERM_RESOURCES}><NewNetworkButton refresh={refreshAll} /></PermissionGuard>,
                    <PermissionGuard permission={PERM_RESOURCES}><LinkContainersButton
                      refresh={refreshAll}
                      originContainer={containerInfo.Name.replace('/', '')}
                      newContainer={newContainer}
                      OnConnect={OnConnect}
                    /></PermissionGuard>,
                  ]}
                  onRowClick={() => { }}
                  getKey={(r) => r.Id}
                  columns={[
                    {
                      title: '',
                      field: (r) => {
                        const isConnected = containerInfo.NetworkSettings.Networks[r.Name];
                        // icon 
                        return isConnected ?
                          <ApiOutlined style={{ color: 'green' }} /> :
                          <ApiOutlined style={{ color: 'red' }} />
                      }
                    },
                    ...NetworksColumns(theme, isDark, t),
                    {
                      title: '',
                      field: (r) => {
                        const isConnected = containerInfo.NetworkSettings.Networks[r.Name];
                        return (<PermissionGuard permission={PERM_RESOURCES}><Button
                          variant="outlined"
                          color="primary"
                          onClick={() => {
                            isConnected ? disconnect(r.Name) : connect(r.Name);
                          }}>
                          {isConnected ? t('mgmt.servapps.containers.terminal.disconnectButton') : t('mgmt.servapps.containers.terminal.connectButton')}
                        </Button></PermissionGuard>)
                      }
                    }
                  ]}
                />}
                {!networks && (<div style={{ height: '550px' }}>
                  <center>
                    <br />
                    <CircularProgress />
                  </center>
                </div>
                )}
                </Stack>
              </MainCard>
            </Stack>
          </form>
        )}
      </Formik>
    </div>
  </Stack>);
};

export default NetworkContainerSetup;
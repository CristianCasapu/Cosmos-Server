// material-ui
import * as React from 'react';
import { Alert, Button, Checkbox, FormControlLabel, FormLabel, Stack, Typography } from '@mui/material';
import { WarningOutlined, PlusCircleOutlined, CopyOutlined, ExclamationCircleOutlined, SyncOutlined, UserOutlined, KeyOutlined, ArrowUpOutlined, FileZipOutlined, ArrowDownOutlined, ConsoleSqlOutlined } from '@ant-design/icons';
import Table from '@mui/material/Table';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableContainer from '@mui/material/TableContainer';
import TableHead from '@mui/material/TableHead';
import TableRow from '@mui/material/TableRow';
import Paper from '@mui/material/Paper';
import Dialog from '@mui/material/Dialog';
import DialogActions from '@mui/material/DialogActions';
import DialogContent from '@mui/material/DialogContent';
import DialogContentText from '@mui/material/DialogContentText';
import DialogTitle from '@mui/material/DialogTitle';
import TextField from '@mui/material/TextField';
import CircularProgress from '@mui/material/CircularProgress';
import Chip from '@mui/material/Chip';
import IconButton from '@mui/material/IconButton';
import * as API from '../../../api';
import MainCard from '../../../components/MainCard';
import { useEffect, useState } from 'react';
import ResponsiveButton from '../../../components/responseiveButton';
import UploadButtons from '../../../components/fileUpload';
import NewDockerService from './newService';
import { loadCompose } from '../../../utils/composeYaml.mjs'; // Fork addition: js-yaml 5 (merge keys added back)
import { CosmosCollapse, CosmosFormDivider, CosmosInputPassword, CosmosInputText, CosmosSelect } from '../../config/users/formShortcuts';
import VolumeContainerSetup from './volumes';
import DockerContainerSetup from './setup';
import whiskers from '../../../utils/whiskersSafe'; // Fork addition: the same templates, read without being turned into code (the panel's script policy), see utils/whiskersSafe.js
import {version} from '../../../../../package.json';
import cmp from 'semver-compare';
import { HostnameChecker, getHostnameFromName } from '../../../utils/routes';
import { CosmosContainerPicker } from '../../config/users/containerPicker';
import { randomString } from '../../../utils/indexs';
import { useTranslation } from 'react-i18next';
import { FilePickerButton } from '../../../components/filePicker';
import PermissionGuard from '../../../components/permissionGuard';
import { PERM_RESOURCES } from '../../../utils/permissions';
import { ComposePortConflicts } from '../../../components/portConflicts'; // Fork addition
import { convertCompose } from '../../../utils/composeImport'; // Fork addition
// Fork addition: local address by default, external ones per attached domain
import { keepOnBackdrop } from '../../../components/formHelpers'; // Fork addition
import HostnameSuggestions, { addressName, installerRouteHasAddress, localAddress, withExternalRoutes } from '../../../components/hostnameSuggestions';

function checkIsOnline() {
  API.isOnline().then((res) => {
    window.location.reload();
  }).catch((err) => {
    setTimeout(() => {
      checkIsOnline();
    }, 1000);
  });
}

const preStyle = {
  backgroundColor: '#000',
  color: '#fff',
  padding: '10px',
  borderRadius: '5px',
  overflow: 'auto',
  maxHeight: '520px',
  maxWidth: '100%',
  width: '100%',
  margin: '0',
  position: 'relative',
  fontSize: '12px',
  fontFamily: 'monospace',
  whiteSpace: 'pre-wrap',
  wordWrap: 'break-word',
  wordBreak: 'break-all',
  lineHeight: '1.5',
  boxShadow: '0 0 10px rgba(0,0,0,0.5)',
  border: '1px solid rgba(255,255,255,0.1)',
  boxSizing: 'border-box',
  marginBottom: '10px',
  marginTop: '10px',
  marginLeft: '0',
  marginRight: '0',
}

const isNewerVersion = (minver) => {
  return cmp(version, minver) === -1;
}

const cleanUpStore = (service) => {
  let newService = Object.assign({}, service);
  delete newService['cosmos-installer'];
  delete newService['x-cosmos-installer'];
  return newService;
}

// Fork addition: the conversion is utils/composeImport.js, which takes every compose form it knows and names
// what it does not take (upstream's conversion broke a lone port and dropped unknown keys without a word).
const convertDockerCompose = (config, serviceName, dockerCompose, setYmlError, setNotes) => {
  try {
    const result = convertCompose(loadCompose(dockerCompose), {
      serviceName,
      dataPath: (config && config.DockerConfig && config.DockerConfig.DefaultDataPath) || '/cosmos-storage',
    });
    setNotes && setNotes(result.notes);
    return result.doc;
  } catch (e) {
    setYmlError(e.message);
    return;
  }
}

const DockerComposeImport = ({ refresh, dockerComposeInit, installerInit, defaultName }) => {
  const { t, i18n } = useTranslation();
  const cleanDefaultName = defaultName && defaultName.replace(/\s/g, '-').replace(/[^a-zA-Z0-9-]/g, '');
  const [step, setStep] = useState(0);
  const [isLoading, setIsLoading] = useState(false);
  const [openModal, setOpenModal] = useState(false);
  const [dockerCompose, setDockerCompose] = useState('');
  const [service, setService] = useState({});
  const [ymlError, setYmlError] = useState('');
  const [serviceName, setServiceName] = useState(null);
  const [hostnames, setHostnames] = useState({});
  const [overrides, setOverrides] = useState({});
  const [context, setContext] = useState({});
  const [installer, setInstaller] = useState(installerInit);
  const [config, setConfig] = useState({});
  const [rootProtocol, setRootProtocol] = useState('https');
  const [envContent, setEnvContent] = useState('');
  const [detectedEnvVars, setDetectedEnvVars] = useState([]);
  // Fork addition: domains attached to the app; each adds an external address per route
  const [domains, setDomains] = useState([]);
  // Fork addition: what the imported file has that is not taken as written
  const [notes, setNotes] = useState([]);

  // Extract ${VAR} patterns from docker compose
  const extractEnvVars = (compose) => {
    const regex = /\$\{([^}:]+)(?::-[^}]*)?\}/g;
    const vars = new Set();
    let match;
    while ((match = regex.exec(compose)) !== null) {
      vars.add(match[1]);
    }
    return Array.from(vars).sort();
  };

  // Parse .env content into key-value object
  const parseEnvContent = (content) => {
    const envMap = {};
    content.split('\n').forEach(line => {
      const trimmed = line.trim();
      if (trimmed && !trimmed.startsWith('#')) {
        const eqIndex = trimmed.indexOf('=');
        if (eqIndex > 0) {
          const key = trimmed.substring(0, eqIndex).trim();
          const value = trimmed.substring(eqIndex + 1).trim();
          envMap[key] = value;
        }
      }
    });
    return envMap;
  };

  // Apply env substitution to compose
  const applyEnvSubstitution = (compose, envMap) => {
    return compose.replace(/\$\{([^}:]+)(:-([^}]*))?\}/g, (match, varName, defaultPart, defaultValue) => {
      if (envMap[varName] !== undefined && envMap[varName] !== '') {
        return envMap[varName];
      }
      if (defaultValue !== undefined) {
        return defaultValue;
      }
      return match;
    });
  };

  // Update detected env vars when compose changes
  useEffect(() => {
    if (dockerCompose) {
      const vars = extractEnvVars(dockerCompose);
      if (JSON.stringify(vars) !== JSON.stringify(detectedEnvVars)) {
        setDetectedEnvVars(vars);
        // Generate placeholder .env content for new vars
        const currentEnv = parseEnvContent(envContent);
        const newEnvLines = vars.map(v => {
          if (currentEnv[v] !== undefined) {
            return `${v}=${currentEnv[v]}`;
          }
          return `${v}=`;
        });
        setEnvContent(newEnvLines.join('\n'));
      }
    } else {
      setDetectedEnvVars([]);
      setEnvContent('');
    }
  }, [dockerCompose]);

  let hostnameErrors = () => {
    let broken = false;
    Object.values(hostnames).forEach((service) => {
      Object.values(service).forEach((route) => {
        if(!route.host || route.host.match(/\s/g)) {
          broken = true;
        }
      });
    });
    return broken;
  }

  const [passwords, setPasswords] = useState([
    randomString(24),
    randomString(24),
    randomString(24),
    randomString(24)
  ]);

  const resetPassword = () => {
    setPasswords([
      randomString(24),
      randomString(24),
      randomString(24),
      randomString(24)
    ]);
  }


  function refreshConfig() {
    API.config.get().then((res) => {
      setRootProtocol(res.isHTTPS === false ? 'http' : 'https');
      setConfig(res.data);
    });
  }

  React.useEffect(() => {
    refreshConfig();
  }, []);

  useEffect(() => {
    if (!openModal) {
      return;
    }
    if(dockerComposeInit)
      fetch(dockerComposeInit)
        .then((res) => res.text())
        .then((text) => {
          setDockerCompose(text);
      });
  }, [openModal, dockerComposeInit]);

  useEffect(() => {
    setOverrides({});
  }, [serviceName, hostnames, context]);

  useEffect(() => {
    if (!openModal || dockerCompose === '') {
      return;
    }

      setYmlError('');
      if (dockerCompose === '') {
        return;
      }
    
    try {
      // Apply env substitution
      const envMap = parseEnvContent(envContent);
      const envSubstitutedCompose = applyEnvSubstitution(dockerCompose, envMap);
      // Fork addition: not printed to the console: it holds the generated passwords

      let isJson = envSubstitutedCompose && envSubstitutedCompose.trim().startsWith('{') && envSubstitutedCompose.trim().endsWith('}');

      const rendered = whiskers.render(envSubstitutedCompose.replace(/{StaticServiceName}/ig, serviceName), {
        ServiceName: serviceName,
        Hostnames: hostnames,
        Context: context,
        Passwords: passwords,
        CPU_ARCH: API.CPU_ARCH,
        CPU_AVX: API.CPU_AVX,
        DefaultDataPath: (config && config.DockerConfig && config.DockerConfig.DefaultDataPath) || "/cosmos-storage",
        RootHostname: (config && config.HTTPConfig && config.HTTPConfig.Hostname) || window.location.hostname,
        RootProtocol: rootProtocol,
      });
      // Fork addition: not printed to the console: it holds the generated passwords

      let jsoned;
      if(isJson) {
        jsoned = JSON.parse(rendered);
        setNotes([]); // Fork addition
      } else {
        jsoned = convertDockerCompose(config, serviceName, rendered, setYmlError, (found) => { // Fork addition: the notes
          if (JSON.stringify(found) !== JSON.stringify(notes)) setNotes(found);
        });
      // Fork addition: not printed to the console: it holds the generated passwords
      }
      
      if(!serviceName && !Object.keys(service).length) {
        if(jsoned['name'] && jsoned['name'].trim() !== '') {
          setServiceName(jsoned['name']);
        } else if (jsoned['services'] && Object.keys(jsoned['services']).length > 0 && Object.keys(jsoned['services'])[0].trim() !== '') {
          setServiceName(Object.keys(jsoned['services'])[0]);
        } else {
          setServiceName(cleanDefaultName || 'default-service');
        }
      }

      if (typeof jsoned['cosmos-installer'] === 'object' || typeof jsoned['x-cosmos-installer'] === 'object') {
        setInstaller(true);

        if (jsoned.services) {
          // GENERATE HOSTNAMES FORM
          let newHostnames = {};
          Object.keys(jsoned.services).forEach((key) => {
            if (jsoned.services[key].routes) {
              let routeId = 0;
              jsoned.services[key].routes.forEach((route) => {
                if (route.useHost) {
                  let newRoute = Object.assign({}, route);
                  if (route.useHost === true) {
                    newRoute.host = getHostnameFromName(key + (routeId > 0 ? '-' + routeId : ''), newRoute, config);
                    // Fork addition: a route reached by name gets the local address by default
                    if (installerRouteHasAddress(newRoute)) {
                      newRoute.host = localAddress(key + (routeId > 0 ? '-' + routeId : ''), config, newRoute);
                    }
                  }
                  
                  if(!newHostnames[key]) newHostnames[key] = {};


                  if(!newHostnames[key][route.name]) {
                    newHostnames[key][route.name] = newRoute;
                    if(hostnames[key] && hostnames[key][route.name]) {
                      newHostnames[key][route.name].host = hostnames[key][route.name].host;
                    }
                  }
                }
              });
            }
          });

          if(JSON.stringify(newHostnames) !== JSON.stringify(hostnames))
            setHostnames({...newHostnames});

          let newVolumes = [];

          // SET DEFAULT CONTEXT
          let newContext = {};
          if (jsoned['cosmos-installer'] && jsoned['cosmos-installer'].form) {
            jsoned['cosmos-installer'].form.forEach((field) => {
              [field.name, field['name-container']].forEach((fieldName) => {
                if(typeof context[fieldName] === "undefined" && typeof field.initialValue !== "undefined") {
                  newContext[fieldName] = field.initialValue;
                } else if (typeof context[fieldName] !== "undefined") {
                  newContext[fieldName] = context[fieldName];
                }
              });
            });
          }
          
          if(JSON.stringify(Object.keys(newContext)) !== JSON.stringify(Object.keys(context)))
            setContext({...newContext});

          Object.keys(jsoned.services).forEach((key) => {
            // APPLY OVERRIDE
            if (overrides[key]) {
              // prevent customizing static volumes
              if (jsoned.services[key].volumes && jsoned['cosmos-installer'] && jsoned['cosmos-installer']['frozen-volumes']) {
                jsoned['cosmos-installer']['frozen-volumes'].forEach((volumeName) => {
                  const keyVolume = overrides[key].volumes.findIndex((v) => {
                    return v.source === volumeName;
                  });
                  delete overrides[key].volumes[keyVolume];
                });
              }

              jsoned.services[key] = {
                ...jsoned.services[key],
                ...overrides[key],
              };
            }

            // APPLY HOSTNAMES
            if (hostnames[key]) {
              if (jsoned.services[key].routes) {
                jsoned.services[key].routes.forEach((route) => {
                  if (hostnames[key][route.name]) {
                    route.host = hostnames[key][route.name].host;
                  }
                });
                // Fork addition: one more route per attached domain, created by the same request
                jsoned.services[key].routes = withExternalRoutes(jsoned.services[key].routes, key, domains);
              }
            }

            // CREATE NEW VOLUMES
            if (jsoned.services[key].volumes) {
              jsoned.services[key].volumes.forEach((volume) => {
                // Fork addition: a tmpfs mount has no source (volume.source && ...)
                if (typeof volume === 'object' && volume.source && !volume.source.startsWith('/') && !volume.existing) {
                  newVolumes.push(volume);
                } else if (typeof volume === 'object' && volume.existing) {
                  delete volume.existing;
                }
              });
            }
          });

          if (newVolumes.length > 0) {
            jsoned.volumes = jsoned.volumes || {};
            newVolumes.forEach((volume) => {
              jsoned.volumes[volume.source] = {
              };
            });
          }

          // CREATE DEFAULT NETWORK
          if(!jsoned['cosmos-installer'] || !jsoned['cosmos-installer']['skip-default-network']) {
            let hasDefaultNetwork = false;
            if (jsoned.services) {
              Object.keys(jsoned.services).forEach((key) => {
                if(!jsoned.services[key].network_mode) {
                  jsoned.services[key].network_mode = 'cosmos-' + serviceName + '-default';
                  hasDefaultNetwork = true;
                }
              });
            }

            if(hasDefaultNetwork) {
              if(!jsoned.networks) {
                jsoned.networks = {}
              }
              
              jsoned.networks['cosmos-' + serviceName + '-default'] = {
                Labels: {
                  'cosmos.stack': serviceName,
                }
              }
            }
          }
        }

        setService(jsoned);
      } else {
        setService(jsoned);
      }
    } catch (e) {
      setYmlError(e.message);
      return;
    }
  }, [openModal, dockerCompose, serviceName, hostnames, overrides, installer, config, rootProtocol, envContent, domains]);

  const openModalFunc = () => {
    setOpenModal(true);
    setStep(0);
    setService({});
    setYmlError(null);
    setOverrides({});
    setHostnames({});
    setDomains([]); // Fork addition
    setContext({});
    setDockerCompose('');
    setInstaller(installerInit);
    setServiceName(null);
    resetPassword();
  }

  return (
    <>
    <Dialog open={openModal} onClose={keepOnBackdrop(() => setOpenModal(false)) /* Fork addition: a click beside it no longer throws the form away */} fullWidth maxWidth={'sm'}>
      <DialogTitle>{installer ? t('mgmt.servapps.compose.installTitle') : t('mgmt.servapps.importComposeFileButton')}</DialogTitle>
      <DialogContent style={{ width: '100%' }}>
        <DialogContentText>
          {step === 0 && !installer && <><Stack spacing={2}>


            <Stack direction="row" spacing={2}>
              <UploadButtons
                accept='.yml,.yaml,.json'
                label={t('mgmt.servapps.compose.uploadCompose')}
                OnChange={(e) => {
                  const file = e.target.files[0];
                  const reader = new FileReader();
                  reader.onload = (e) => {
                    setDockerCompose(e.target.result);
                  };
                  reader.readAsText(file);
                }}
              />

              {detectedEnvVars.length > 0 && <UploadButtons
                label={t('mgmt.servapps.compose.uploadEnv')}
                OnChange={(e) => {
                  const file = e.target.files[0];
                  const reader = new FileReader();
                  reader.onload = (e) => {
                    setEnvContent(e.target.result);
                  };
                  reader.readAsText(file);
                }}
              />}
            </Stack>

            <div style={{ color: 'red' }}>
              {ymlError}
            </div>

            {detectedEnvVars.length > 0 && (<>
              <div>{t('mgmt.servapps.compose.envVarsDetected')}</div>
              <TextField
                multiline
                placeholder="VAR=value"
                fullWidth
                value={envContent}
                onChange={(e) => setEnvContent(e.target.value)}
                sx={{...preStyle, maxHeight: '200px'}}
                  minRows={Math.min(detectedEnvVars.length, 5)}
                  slotProps={{
                    input: {
                  sx: {
                    color: '#EEE',
                  }
                    }
                  }}></TextField>
            </>)}

            <TextField
              multiline
              placeholder={t('mgmt.servapps.pasteComposeButton.pasteComposePlaceholder')}
              fullWidth
              value={dockerCompose}
              onChange={(e) => setDockerCompose(e.target.value)}
              sx={{...preStyle, maxHeight: detectedEnvVars.length > 0 ? '400px' : '520px'}}
                minRows={detectedEnvVars.length > 0 ? 15 : 20}
                slotProps={{
                  input: {
                sx: {
                  color: '#EEE',
                }
                  }
                }}></TextField>
          </Stack></>}

          {step === 0 && installer && <><Stack spacing={2}>
            <div style={{ color: 'red' }}>
              {ymlError}
            </div>

            {!ymlError && (<><FormLabel>{t('mgmt.servApps.newContainer.serviceNameInput')}</FormLabel>

              <TextField label="" value={serviceName} onChange={(e) => setServiceName(e.target.value)}
                  slotProps={{
                    htmlInput: { 'aria-label': t('mgmt.servApps.newContainer.serviceNameInput') }
                  }} />

              {service['cosmos-installer'] && service['cosmos-installer'].form && service['cosmos-installer'].form.map((formElement) => {
                return formElement.type === 'checkbox' ?
                <FormControlLabel
                  control={<Checkbox checked={context[formElement.name]} onChange={(e) => {
                    setContext({ ...context, [formElement.name]: e.target.checked });
                  }
                  } />}
                  label={ service['cosmos-installer']?.translation?.[i18n?.resolvedLanguage]?.['form.'+formElement.name+'.label'] || service['cosmos-installer']?.translation?.[i18n?.resolvedLanguage.substr?.(0,2)]?.['form.'+formElement.name+'.label'] || formElement.label }
                /> : (formElement.type === 'password' || formElement.type === 'email') ?
                  <TextField
                  label={ service['cosmos-installer']?.translation?.[i18n?.resolvedLanguage]?.['form.'+formElement.name+'.label'] || service['cosmos-installer']?.translation?.[i18n?.resolvedLanguage.substr?.(0,2)]?.['form.'+formElement.name+'.label'] || formElement.label }
                  value={context[formElement.name]}
                  type={formElement.type}
                  onChange={(e) => {
                    setContext({ ...context, [formElement.name]: e.target.value });
                  }
                  } />
                :  (formElement.type === 'select') ?
                    <CosmosSelect
                    name={formElement.name} 
                    label={ service['cosmos-installer']?.translation?.[i18n?.resolvedLanguage]?.['form.'+formElement.name+'.label'] || service['cosmos-installer']?.translation?.[i18n?.resolvedLanguage.substr?.(0,2)]?.['form.'+formElement.name+'.label'] || formElement.label }
                    formik={{
                      values: {
                        [formElement.name] : context[formElement.name]
                      },
                      touched: {},
                      errors: {},
                      setFieldValue: () => {},
                      handleChange: () => {}
                    }}
                    onChange={(e) => {
                      setContext({ ...context, [formElement.name]: e.target.value });
                    }}
                    options={ service['cosmos-installer']?.translation?.[i18n?.resolvedLanguage]?.['form.'+formElement.name+'.options'] || service['cosmos-installer']?.translation?.[i18n?.resolvedLanguage.substr?.(0,2)]?.['form.'+formElement.name+'.options'] || formElement.options }
                  />
                : formElement.type === 'hostname' ? 
                  <>
                    <TextField
                      label={ service['cosmos-installer']?.translation?.[i18n?.resolvedLanguage]?.['form.'+formElement.name+'.label'] || service['cosmos-installer']?.translation?.[i18n?.resolvedLanguage.substr?.(0,2)]?.['form.'+formElement.name+'.label'] || formElement.label }
                      value={context[formElement.name]}
                      onChange={(e) => {
                        setContext({ ...context, [formElement.name]: e.target.value });
                      }
                      } />
                    <HostnameChecker hostname={context[formElement.name]} />
                  </>
                : formElement.type === 'container' || formElement.type === 'container-full' ? 
                  <CosmosContainerPicker
                      name={formElement.name} 
                      formik={{
                        values: {
                          [formElement.name] : context[formElement.name]
                        },
                        errors: {},
                        setFieldValue: (name, value) => {
                          setContext({ ...context, [formElement.name]: value });
                        },
                      }}
                      nameOnly={formElement.type === 'container'}
                      label={ service['cosmos-installer']?.translation?.[i18n?.resolvedLanguage]?.['form.'+formElement.name+'.label'] || service['cosmos-installer']?.translation?.[i18n?.resolvedLanguage.substr?.(0,2)]?.['form.'+formElement.name+'.label'] || formElement.label }
                      onTargetChange={(_, name) => {
                        setContext({ ...context, [formElement['name-container']]: name });
                      }}
                  />
                  : formElement.type === 'error' || formElement.type === 'info' || formElement.type === 'warning' ?
                  <Alert severity={formElement.type}>
                    { service['cosmos-installer']?.translation?.[i18n?.resolvedLanguage]?.['form.'+formElement.name+'.label'] || service['cosmos-installer']?.translation?.[i18n?.resolvedLanguage.substr?.(0,2)]?.['form.'+formElement.name+'.label'] || formElement.label }
                  </Alert>
                  : formElement.type === 'path' ?
                    <Stack direction={"row"} spacing={2}>
                      <FilePickerButton onPick={(path) => {
                        if(path)
                          setContext({ ...context, [formElement.name]: path });
                      }} size="150%" select="folder" value={context[formElement.name] /* Fork addition: open there */} />
                      <TextField
                      style={{ width: '100%' }}
                      label={ service['cosmos-installer']?.translation?.[i18n?.resolvedLanguage]?.['form.'+formElement.name+'.label'] || service['cosmos-installer']?.translation?.[i18n?.resolvedLanguage.substr?.(0,2)]?.['form.'+formElement.name+'.label'] || formElement.label }
                      value={context[formElement.name]}
                      onChange={(e) => {
                        setContext({ ...context, [formElement.name]: e.target.value });
                      }} />   
                    </Stack>
                  : <TextField
                    label={ service['cosmos-installer']?.translation?.[i18n?.resolvedLanguage]?.['form.'+formElement.name+'.label'] || service['cosmos-installer']?.translation?.[i18n?.resolvedLanguage.substr?.(0,2)]?.['form.'+formElement.name+'.label'] || formElement.label }
                    value={context[formElement.name]}
                    onChange={(e) => {
                      setContext({ ...context, [formElement.name]: e.target.value });
                    }} />
              })}

              {Object.keys(hostnames).map((serviceIndex) => {
                const service = hostnames[serviceIndex];
                return Object.keys(service).map((hostIndex) => {
                  const hostname = service[hostIndex];
                  return <>
                    <FormLabel>{t('mgmt.servApps.newContainer.chooseUrl')} {hostname.name}</FormLabel>
                    <div style={{ fontSize: 'var(--ct-text-xs, 0.75rem)', color: 'var(--ct-muted)' }} /* Fork addition: the skin's smallest size (it was 11.2 px) */
                    >{hostname.description}</div>
                    <TextField key={serviceIndex + hostIndex} label="Hostname" value={hostname.host} onChange={(e) => {
                      hostnames[serviceIndex][hostname.name].host = e.target.value;
                      setHostnames({...hostnames});
                    }} />
                    {/* Fork addition: addresses to pick from */}
                    {installerRouteHasAddress(hostname) && <HostnameSuggestions
                      inline
                      name={addressName(serviceIndex, hostname)}
                      value={hostname.host}
                      config={config}
                      onPick={(host) => {
                        hostnames[serviceIndex][hostname.name].host = host;
                        setHostnames({...hostnames});
                      }}
                    />}
                    <HostnameChecker hostname={hostname.host} />
                  </>
                })
              })}


              {service && service.services && Object.values(service.services).map((value) => {
                return <CosmosCollapse title={t('mgmt.servApps.newContainer.customize', {container_name: value.container_name})}>
                  <Stack spacing={2}>
                    <DockerContainerSetup
                      newContainer
                      containerInfo={{
                        Name: '',
                        Image: '',
                        Config: {
                          Env: value.environment || [],
                          Labels: value.labels || {},
                          User: value.user || '',
                        },
                        HostConfig: {
                          RestartPolicy: {},
                          Devices: value.devices || [],
                        }
                      }}
                      OnChange={(containerInfo) => {
                        setOverrides({
                          ...overrides,
                          [value.container_name]: {
                            ...overrides[value.container_name],
                            environment: containerInfo.envVars,
                            labels: containerInfo.labels,
                            devices: containerInfo.devices,
                          }
                        })
                      }}
                      noCard
                      installer
                    />
                    <CosmosFormDivider title={t('mgmt.servapps.networks.volumes')} />
                    <VolumeContainerSetup
                      newContainer
                      frozenVolumes={service['cosmos-installer'] && service['cosmos-installer']['frozen-volumes'] || []}
                      containerInfo={{
                        HostConfig: {
                          Binds: [],
                          Mounts: value.volumes && Object.keys(value.volumes).map(k => {
                            return {
                              Type: value.volumes[k].type || (k.startsWith('/') ? t('mgmt.servapps.newContainer.volumes.bindInput') : t('global.volume')),
                              Source: value.volumes[k].source || "",
                              Target: value.volumes[k].target || "",
                            }
                          }) || [],
                        }
                      }}
                      OnChange={(containerInfo, volumes) => {
                        setOverrides({
                          ...overrides,
                          [value.container_name]: {
                            ...overrides[value.container_name],
                            volumes: containerInfo.volumes.map((v, k) => {
                              return {
                                type: v.Type,
                                source: v.Source,
                                target: v.Target,
                                existing: v.Type == 'volume' && volumes.find(v2 => v2.Source === v.Name),
                              }
                            })
                          }
                        })
                      }}
                      noCard
                    />
                  </Stack>
                </CosmosCollapse>
              })}

              {/* Fork addition: one screen, one confirmation: the review, the internet and Create follow the app's own fields */}
              {service && service.services && <Stack spacing={2} data-installer-review>
                <ComposePortConflicts service={service} />
                <NewDockerService service={service} refresh={refresh} notes={notes}
                  holdReason={hostnameErrors() ? t('mgmt.servapps.creation.holdAddress', 'Every address above needs a name, without spaces.') : ''} />
              </Stack>}
            </>)}
          </Stack></>}
          
          {step === 0 && dockerComposeInit && dockerCompose == '' && <Stack spacing={2} sx={{ alignItems: 'center' }} style={{paddingTop: '20px'}}>
            <CircularProgress />
          </Stack>}

          {step === 1 && <Stack spacing={2}>
            {/* Fork addition: host ports of the definition that are already taken */}
            <ComposePortConflicts service={service} />
            <NewDockerService service={service} refresh={refresh} notes={notes} />
          </Stack>}
        </DialogContentText>
      </DialogContent>
      {(installerInit && service.minVersion && isNewerVersion(service.minVersion)) ?
      <Alert severity="error" icon={<WarningOutlined />}>
        {t('mgmt.servApps.newContainer.cosmosOutdatedError')}
      </Alert>
      : 
      (!isLoading && <DialogActions>
        <Button onClick={() => {
          setOpenModal(false);
          setStep(0);
          setDockerCompose('');
          setYmlError('');
          setInstaller(false);
          setServiceName(null);
          setContext({});
          setHostnames({});
          setOverrides({});
        }}>{t('global.close')}</Button>
        {!(installer && step === 0) /* Fork addition: the installer's review is on the same screen */ && <Button disabled={!dockerCompose || ymlError || hostnameErrors()} onClick={() => {
          if (step === 0) {
            setStep(1);
          } else {
            setStep(0);
          }
        }}>
          {step === 0 && t('global.next')}
          {step === 1 && t('global.backAction')}
        </Button>}
      </DialogActions>)}
    </Dialog>

    <PermissionGuard permission={PERM_RESOURCES}>
      <ResponsiveButton
        color="primary"
        onClick={() => {
          openModalFunc();
        }}
        variant={(installerInit ? "contained" : "outlined")}
        startIcon={(installerInit ? <ArrowDownOutlined /> : <ArrowUpOutlined />)}
      >
        {installerInit ? t('mgmt.servapps.compose.installButton') : t('mgmt.servapps.importComposeFileButton')}
      </ResponsiveButton>
    </PermissionGuard>
    
    </>
  );
};

export default DockerComposeImport;

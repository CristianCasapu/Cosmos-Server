import React, { useState } from 'react';
import { Button, Dialog, DialogActions, DialogContent, DialogContentText, DialogTitle, Stack } from '@mui/material';
import { Alert } from '@mui/material';
import RouteManagement from '../config/routes/routeman';
import { ValidateRoute, getFaviconURL, sanitizeRoute, getContainersRoutes, getHostnameFromName } from '../../utils/routes';
import * as API from '../../api';
import { DomainPicker, externalAddress, localAddress } from '../../components/hostnameSuggestions';
import { useTranslation } from 'react-i18next';

// Fork addition: resolves once the server answers again after a change that made it reload.
const serverBack = async () => {
  for (let i = 0; i < 40; i++) {
    await new Promise((resolve) => setTimeout(resolve, 750));
    try {
      await fetch('/cosmos/api/status', { credentials: 'same-origin' });
      return;
    } catch (e) {
      // still reloading
    }
  }
};

const ExposeModal = ({ openModal, setOpenModal, config, updateRoutes, container }) => {
  const { t } = useTranslation();
  const [submitErrors, setSubmitErrors] = useState([]);
  const [newRoute, setNewRoute] = useState(null);
  // Fork addition: domains attached to the app; each adds an external address
  const [domains, setDomains] = useState([]);
  const [creating, setCreating] = useState(false);

  let containerName = openModal && (openModal.Names[0]);

  const hasCosmosNetwork = () => {
    return container && container.Labels["cosmos-force-network-secured"] === "true";
  }
  
  return <Dialog open={openModal} onClose={() => setOpenModal(false)}>
    <DialogTitle>{t('mgmt.servApp.container.urls.exposeTitle')}</DialogTitle>
        {openModal && <>
        <DialogContent>
            <DialogContentText>
              <Stack spacing={2}>
                <div>
                  {t('mgmt.servApp.container.urls.exposeText')}
                </div>
                <div>
                    <RouteManagement TargetContainer={openModal} 
                      routeConfig={{
                        Target: "http://"+containerName.replace('/', '') + ":",
                        Mode: "SERVAPP",
                        Name: containerName.replace('/', ''),
                        Description: "Expose " + containerName.replace('/', '') + " to the internet",
                        UseHost: true,
                        // Fork addition: every app gets a local address; domains are attached below
                        Host: localAddress(containerName, config),
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
                      config={config}
                      routeNames={config.HTTPConfig.ProxyConfig.Routes.map((r) => r.Name)}
                      setRouteConfig={(_newRoute) => {
                        setNewRoute(sanitizeRoute(_newRoute));
                      }}
                      up={() => {}}
                      down={() => {}}
                      deleteRoute={() => {}}
                      noControls
                      lockTarget
                      showErrors={submitErrors.length > 0 && submitErrors /* Fork addition */}
                    />
                </div>
                {/* Fork addition */}
                <DomainPicker
                  name={(newRoute && newRoute.Name) || containerName}
                  config={config}
                  selected={domains}
                  onChange={setDomains}
                />
              </Stack>
            </DialogContentText>
        </DialogContent>
        <DialogActions>
            {submitErrors && submitErrors.length > 0 && <Stack spacing={2} direction={"column"}>
              <Alert severity="error">{submitErrors.map((err) => {
                  return <div>{err}</div>
                })}</Alert>
            </Stack>}
            <Button onClick={() => setOpenModal(false)}>{t('global.cancelAction')}</Button>
            <Button disabled={creating} onClick={async () => {
              // Fork addition: one more route per attached domain, same target and settings
              const extras = domains.map((domain) => ({
                ...newRoute,
                Name: newRoute.Name + '-' + domain,
                Host: externalAddress(newRoute.Name, domain),
              }));

              let errors = [newRoute, ...extras].flatMap((route) => ValidateRoute(route, config, t) || []);
              if (errors && errors.length > 0) {
                errors = errors.map((err) => {
                  return `${err}`;
                });
                setSubmitErrors([...new Set(errors)]);
                return true;
              } else {
                setSubmitErrors([]);
                setCreating(true);
                try {
                  for (const extra of extras) {
                    await API.config.addRoute(extra);
                    // the server reloads after each new route: wait until it answers again
                    await serverBack();
                  }
                  setDomains([]);
                  updateRoutes(newRoute);
                } catch (e) {
                  setSubmitErrors([e.message || String(e)]);
                } finally {
                  setCreating(false);
                }
              }
              
            }}>{t('global.confirmAction')}</Button>
        </DialogActions>
    </>}
  </Dialog>
};

export default ExposeModal;
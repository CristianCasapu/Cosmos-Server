import React, { useState } from 'react';
import { Button, Dialog, DialogActions, DialogContent, DialogContentText, DialogTitle, Stack } from '@mui/material';
import { Alert } from '@mui/material';
import RouteManagement from '../config/routes/routeman';
import { ValidateRoute, getFaviconURL, sanitizeRoute, getContainersRoutes, getHostnameFromName } from '../../utils/routes';
import * as API from '../../api';
import { DomainPicker, externalAddress, localAddress } from '../../components/hostnameSuggestions';
import { useTranslation } from 'react-i18next';
import { serverBack } from '../../utils/appLinks'; // Fork addition
import { keepOnBackdrop } from '../../components/formHelpers'; // Fork addition
import { newAppRoute } from '../../utils/routeGuide'; // Fork addition

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
  
  // Fork addition: a click beside the form does not close it; fullWidth, so it does not change size with its content
  return <Dialog open={openModal} onClose={keepOnBackdrop(() => setOpenModal(false))} fullWidth maxWidth="sm">
    {/* Fork addition: the title names the app; upstream's "Expose ServApp ... to the internet" was wrong for a local address */}
    <DialogTitle sx={{ overflowWrap: 'anywhere' }}>{openModal
      ? t('mgmt.urls.guide.exposeTitle', 'An address for {{name}}', { name: containerName.replace('/', '') })
      : t('mgmt.servApp.container.urls.exposeTitle')}</DialogTitle>
        {openModal && <>
        <DialogContent>
            <DialogContentText>
              <Stack spacing={2}>
                <div>
                    <RouteManagement TargetContainer={openModal} 
                      routeConfig={/* Fork addition: built where the "address for every app" action builds its URLs
                        (utils/routeGuide.js), so that both create the same thing; the port is proposed by the form */
                        newAppRoute({ name: containerName.replace('/', ''), host: localAddress(containerName, config), port: '' })}
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
                      addressExtra={/* Fork addition: the domains, right under the address */ <DomainPicker
                        name={(newRoute && newRoute.Name) || containerName}
                        config={config}
                        selected={domains}
                        onChange={setDomains}
                      />}
                    />
                </div>
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
import React, { useState } from 'react';
import { Button, Dialog, DialogActions, DialogContent, DialogContentText, DialogTitle, Stack, Typography } from '@mui/material';
import { Alert } from '@mui/material';
import RouteManagement from '../config/routes/routeman';
import { ValidateRoute, getFaviconURL, sanitizeRoute, getContainersRoutes, getHostnameFromName } from '../../utils/routes';
import * as API from '../../api';
import { localAddress } from '../../components/hostnameSuggestions';
import { useTranslation } from 'react-i18next';
import { serverBack } from '../../utils/appLinks'; // Fork addition
import { keepOnBackdrop } from '../../components/formHelpers'; // Fork addition
import { newAppRoute } from '../../utils/routeGuide'; // Fork addition
import { RouteExposure, sendExposure } from '../../components/exposure'; // Fork addition
import { useClientInfos } from '../../utils/hooks'; // Fork addition
import { PERM_ADMIN } from '../../utils/permissions'; // Fork addition

// Fork addition: the internet choices are the Internet dialog's own
// (components/exposure.jsx, one server action: POST /api/expose); the domain
// picker that was here made plain copies of the URL, without the Cloudflare
// choice or the sign-in.
const ExposeModal = ({ openModal, setOpenModal, config, updateRoutes, container, refresh }) => {
  const { t } = useTranslation();
  const [submitErrors, setSubmitErrors] = useState([]);
  const [newRoute, setNewRoute] = useState(null);
  const [creating, setCreating] = useState(false);
  // Fork addition: the internet choices, what they would send, and how it went
  const [exposure, setExposure] = useState(null);
  const [exposurePlan, setExposurePlan] = useState({ changes: {}, chosen: false, blocked: false });
  const [outcome, setOutcome] = useState(null);
  const { hasRolePermission } = useClientInfos();
  const mayExpose = hasRolePermission(PERM_ADMIN);

  let containerName = openModal && (openModal.Names[0]);
  const appName = containerName ? containerName.replace('/', '') : '';
  // Fork addition: built where the "address for every app" action builds its URLs
  // (utils/routeGuide.js), so that both create the same thing; the port is proposed by the form
  const startRoute = openModal && config ? newAppRoute({ name: appName, host: localAddress(containerName, config), port: '' }) : null;

  const hasCosmosNetwork = () => {
    return container && container.Labels["cosmos-force-network-secured"] === "true";
  }

  // Fork addition: the next app starts afresh
  const reset = () => {
    setNewRoute(null);
    setExposure(null);
    setOutcome(null);
    setSubmitErrors([]);
  };
  const close = () => {
    if (creating) return;
    const done = !!outcome;
    setOpenModal(false);
    reset();
    if (done && refresh) refresh();
  };

  // Fork addition: the server reloads right after saving a new URL and can
  // drop the connection before its answer arrives (WebKit says "Load failed"):
  // without an answer, the URL is looked for once the server is back.
  const createRoute = async (route) => {
    try {
      await API.config.addRoute(route);
    } catch (e) {
      if (e && e.status && !e.unanswered) throw e;
      await serverBack();
      const saved = await API.config.get().catch(() => null);
      const routes = (saved && saved.data && saved.data.HTTPConfig.ProxyConfig.Routes) || [];
      if (!routes.some((r) => r.Name === route.Name)) throw e; // the form proposes a name no URL has
    }
  };

  // Fork addition: the URL first, then what was ticked for the internet, for that URL
  const createAndExpose = async (route) => {
    setCreating(true);
    try {
      await createRoute(route);
    } catch (e) {
      setSubmitErrors([e.message || String(e)]);
      setCreating(false);
      return;
    }
    let result;
    try {
      // the server reloads after a new URL: wait until it answers again
      await serverBack();
      result = await sendExposure(exposurePlan.changes);
    } catch (e) {
      result = { ok: false, message: e.message || String(e) };
    }
    setOutcome({ name: route.Name, ...result });
    setCreating(false);
  };

  // Fork addition: a click beside the form does not close it; fullWidth, so it does not change size with its content
  return <Dialog open={openModal} onClose={keepOnBackdrop(close)} fullWidth maxWidth="sm" data-new-url-dialog>
    {/* Fork addition: the title names the app; upstream's "Expose ServApp ... to the internet" was wrong for a local address */}
    <DialogTitle sx={{ overflowWrap: 'anywhere' }}>{openModal
      ? t('mgmt.urls.guide.exposeTitle', 'An address for {{name}}', { name: appName })
      : t('mgmt.servApp.container.urls.exposeTitle')}</DialogTitle>
        {openModal && <>
        <DialogContent>
            {/* Fork addition: once the URL is made, what happened, in words */}
            {outcome && <Stack spacing={2} data-new-url-outcome>
              <Alert severity="success" sx={{ overflowWrap: 'anywhere' }}>
                {t('mgmt.expose.newUrl.created', 'The URL {{name}} is created.', { name: outcome.name })}
              </Alert>
              <Alert severity={outcome.ok ? 'success' : 'error'} sx={{ overflowWrap: 'anywhere' }} data-new-url-exposed={outcome.ok ? 'ok' : 'failed'}>
                {outcome.ok
                  ? t('mgmt.expose.install.done', '{{name}} is set up for the internet. Records and the router follow within a minute.', { name: appName })
                  : <>
                    {t('mgmt.expose.install.failed', '{{name}} could not be set up for the internet: {{error}}', { name: appName, error: outcome.message || t('mgmt.expose.failed', 'The change could not be made.') })}
                    <br />
                    {t('mgmt.expose.newUrl.retry', 'The URL is kept. The app\'s Internet chip opens the same choices to try again.')}
                  </>}
              </Alert>
            </Stack>}
            {!outcome && <DialogContentText>
              <Stack spacing={2}>
                <div>
                    <RouteManagement TargetContainer={openModal}
                      routeConfig={startRoute}
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
                {/* Fork addition: the internet, as the app's Internet dialog offers it (administrators only, as there) */}
                {mayExpose
                  ? <RouteExposure
                    config={config}
                    containerName={appName}
                    image={openModal.Image}
                    route={newRoute || (startRoute && sanitizeRoute(startRoute))}
                    container={openModal /* as Docker lists it: its published ports */}
                    value={exposure}
                    onChange={setExposure}
                    onPlan={setExposurePlan}
                    busy={creating}
                  />
                  : <Typography variant="caption" color="textSecondary" data-new-url-admin-only>
                    {t('mgmt.expose.newUrl.adminOnly', 'An administrator can also make the app reachable from the internet, from the app\'s Internet chip.')}
                  </Typography>}
              </Stack>
            </DialogContentText>}
        </DialogContent>
        <DialogActions sx={{ flexWrap: 'wrap' }}>
            {submitErrors && submitErrors.length > 0 && <Stack spacing={2} direction={"column"}>
              <Alert severity="error">{submitErrors.map((err) => {
                  return <div>{err}</div>
                })}</Alert>
            </Stack>}
            {outcome
              ? <Button onClick={close} data-new-url-close>{t('global.close', 'Close')}</Button>
              : <>
            <Button onClick={close} disabled={creating}>{t('global.cancelAction')}</Button>
            <Button disabled={creating || (mayExpose && exposurePlan.chosen && exposurePlan.blocked)} data-new-url-confirm onClick={async () => {
              const route = newRoute || sanitizeRoute(startRoute);
              let errors = ValidateRoute(route, config, t) || [];
              if (errors && errors.length > 0) {
                errors = errors.map((err) => {
                  return `${err}`;
                });
                setSubmitErrors([...new Set(errors)]);
                return true;
              } else {
                setSubmitErrors([]);
                // Fork addition: something ticked for the internet: the dialog stays to say how it went
                if (mayExpose && exposurePlan.chosen) {
                  await createAndExpose(route);
                  return;
                }
                // nothing for the internet: the URL, then the dialog closes and the page reads it again
                // (Fork addition: in place of the page's updateRoutes, which had no answer to a lost connection)
                setCreating(true);
                try {
                  await createRoute(route);
                } catch (e) {
                  setSubmitErrors([e.message || String(e)]);
                  setCreating(false);
                  return;
                }
                setCreating(false);
                setOpenModal(false);
                reset();
                // the server reloads after a new URL: read again once it answers
                if (refresh) serverBack().then(() => refresh()).catch(() => {});
              }

            }}>{t('global.confirmAction')}</Button>
            </>}
        </DialogActions>
    </>}
  </Dialog>
};

export default ExposeModal;

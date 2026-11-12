// Fork addition: tells the admin whether a newer release of the server exists
// and lets them install it. Used in the general settings (ServerUpdate) and on
// the home page (ServerUpdateAlert). Checking never installs anything;
// "Update now" asks for confirmation first.
import * as React from 'react';
import * as API from '../api';
import { Alert, Button, Grid, Stack, Typography } from '@mui/material';
import { LoadingButton } from '@mui/lab';
import { CloudDownloadOutlined, ReloadOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { ConfirmModalDirect } from './confirmModal';
import PermissionGuard from './permissionGuard';
import { PERM_ADMIN, PERM_CONFIGURATION_READ } from '../utils/permissions';
import { useClientInfos } from '../utils/hooks';

const shown = (version) => (version || '').replace(/^v/, '');

const useServerUpdate = () => {
  const { hasPermission } = useClientInfos();
  // reading the status needs no password confirmation; the buttons do
  const isAdmin = hasPermission(PERM_CONFIGURATION_READ);
  const [status, setStatus] = React.useState(null);
  const [checking, setChecking] = React.useState(false);
  const [failed, setFailed] = React.useState(false);
  const [installing, setInstalling] = React.useState(null);
  const [confirm, setConfirm] = React.useState(false);

  const load = (call) => {
    setChecking(true);
    setFailed(false);
    return call()
      .then((res) => setStatus(res.data))
      .catch(() => setFailed(true))
      .finally(() => setChecking(false));
  };

  React.useEffect(() => {
    if (isAdmin) {
      load(API.config.serverUpdateStatus);
    }
  }, [isAdmin]);

  const check = () => load(API.config.checkServerUpdate);

  const install = () => {
    setChecking(true);
    setFailed(false);
    // The server checks once more and, if there is an update, downloads it and restarts.
    return API.forceAutoUpdate()
      .then((res) => {
        if (res.data && res.data.updateAvailable) {
          setInstalling(res.data.latestVersion);
        } else {
          return load(API.config.serverUpdateStatus);
        }
      })
      .catch(() => setFailed(true))
      .finally(() => setChecking(false));
  };

  return { isAdmin, status, checking, failed, installing, confirm, setConfirm, check, install };
};

const UpdateConfirm = ({ update }) => {
  const { t } = useTranslation();
  if (!update.confirm) {
    return null;
  }
  return <ConfirmModalDirect
    callback={update.install}
    onClose={() => update.setConfirm(false)}
    content={t('mgmt.config.update.confirm',
      'Cosmos will download version {{version}} and restart to install it. The interface is unavailable for a minute or two; running containers are not stopped.',
      { version: shown(update.status && update.status.latestVersion) })}
  />;
};

const ServerUpdate = () => {
  const { t } = useTranslation();
  const update = useServerUpdate();
  const { status } = update;

  if (!update.isAdmin) {
    return null;
  }

  const checkedAt = status && status.checkedAt ? new Date(status.checkedAt).toLocaleString() : null;

  return <Grid item xs={12}>
    <Stack spacing={1.5}>
      <Typography variant="body2" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
        {t('mgmt.config.update.running', 'Running version: {{version}}', { version: shown(status && status.currentVersion) || '…' })}
        {status && status.latestVersion ? ' · ' + t('mgmt.config.update.latest', 'Latest release: {{version}}', { version: shown(status.latestVersion) }) : ''}
        {checkedAt ? ' · ' + t('mgmt.config.update.checkedAt', 'Checked: {{time}}', { time: checkedAt }) : ''}
      </Typography>

      {update.installing ? (
        <Alert severity="info">{t('mgmt.config.general.updateCheck.updateFound', { version: shown(update.installing) })}</Alert>
      ) : update.failed || (status && status.error) ? (
        <Alert severity="error">{t('mgmt.config.update.failed', 'Could not check for updates. Check that the server can reach the internet and try again.')}</Alert>
      ) : status && status.containerized ? (
        <Alert severity="info">{t('mgmt.config.general.updateCheck.containerized')}</Alert>
      ) : status && status.updateAvailable ? (
        <Alert severity="warning">
          {t('mgmt.config.update.available', 'Version {{version}} is available.', { version: shown(status.latestVersion) })}
          {' '}
          {status.autoUpdate
            ? t('mgmt.config.update.autoAt', 'It will be installed automatically at {{time}}.', { time: status.maintenanceTime })
            : t('mgmt.config.update.manualOnly', 'Automatic updates are off, so it is installed only when you choose to.')}
        </Alert>
      ) : status && status.checkedAt ? (
        <Alert severity="success">{t('mgmt.config.general.updateCheck.upToDate', { version: shown(status.currentVersion) })}</Alert>
      ) : null}

      <Stack direction="row" spacing={1} useFlexGap flexWrap="wrap">
        <PermissionGuard permission={PERM_ADMIN}>
          <LoadingButton loading={update.checking} variant="outlined" startIcon={<ReloadOutlined />}
            disabled={Boolean(update.installing)} onClick={update.check}>
            {t('mgmt.config.general.forceAutoUpdateButton')}
          </LoadingButton>
        </PermissionGuard>
        <PermissionGuard permission={PERM_ADMIN}>
          <Button variant="contained" startIcon={<CloudDownloadOutlined />}
            disabled={!status || !status.updateAvailable || update.checking || Boolean(update.installing)}
            onClick={() => update.setConfirm(true)}>
            {t('mgmt.config.update.installNow', 'Update now')}
          </Button>
        </PermissionGuard>
      </Stack>
      <UpdateConfirm update={update} />
    </Stack>
  </Grid>;
};

const ServerUpdateAlert = () => {
  const { t } = useTranslation();
  const update = useServerUpdate();
  const { status } = update;

  if (update.installing) {
    return <Alert severity="info">{t('mgmt.config.general.updateCheck.updateFound', { version: shown(update.installing) })}</Alert>;
  }
  if (!status || !status.updateAvailable) {
    return null;
  }

  return <>
    <Alert severity="warning" action={
      <PermissionGuard permission={PERM_ADMIN}>
        <Button color="inherit" size="small" disabled={update.checking} onClick={() => update.setConfirm(true)}>
          {t('mgmt.config.update.installNow', 'Update now')}
        </Button>
      </PermissionGuard>
    }>
      {t('mgmt.config.update.availableRunning', 'Version {{version}} of Cosmos is available (this server runs {{current}}).',
        { version: shown(status.latestVersion), current: shown(status.currentVersion) })}
      {' '}
      {status.autoUpdate
        ? t('mgmt.config.update.autoAt', 'It will be installed automatically at {{time}}.', { time: status.maintenanceTime })
        : t('mgmt.config.update.manualOnly', 'Automatic updates are off, so it is installed only when you choose to.')}
    </Alert>
    <UpdateConfirm update={update} />
  </>;
};

export default ServerUpdate;
export { ServerUpdateAlert };

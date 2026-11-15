// Fork addition: everything on the home page that needs the admin's attention,
// in one notice area (components/noticeArea.jsx): the server's own state
// (database, Docker, restart needed, host name, install mode, backups), the
// certificates, a newer release, and the health lines (healthNotices.jsx).
// When every check was made and nothing was found, one calm line; when a check
// could not be made it is said, with a retry, and the calm line is not shown.
import * as React from 'react';
import { Button } from '@mui/material';
import { SyncOutlined } from '@ant-design/icons';
import { Trans, useTranslation } from 'react-i18next';
import NoticeArea from './noticeArea';
import { useCertificateNotices } from './certificates';
import { useServerUpdateNotice } from './serverUpdate';
import { useHealthNotices } from './healthNotices';
import { useTurnstileNotices } from './turnstile'; // the sign-in check of Cloudflare does not work
import { useReachWatchNotices } from './reachWatch'; // Fork addition: the server cannot be reached from the internet
import PermissionGuard from './permissionGuard';
import { PERM_ADMIN } from '../utils/permissions';
import Migrate014 from '../pages/home/migrate014';
import RestartModal from '../pages/config/users/restart';

const HomeNotices = ({ coStatus, config }) => {
  const { t } = useTranslation();
  const [restart, setRestart] = React.useState(false);
  const certificates = useCertificateNotices();
  const update = useServerUpdateNotice();
  const turnstile = useTurnstileNotices();
  const reachWatch = useReachWatchNotices(); // Fork addition: the server cannot be reached from the internet
  const health = useHealthNotices({ docker: Boolean(coStatus && coStatus.docker), enabled: Boolean(coStatus) });
  if (!coStatus) return null;

  const own = [];
  if (!coStatus.database) own.push({ id: 'database', severity: 'error', text: t('navigation.home.dbCantConnectError') });
  if (!coStatus.docker) own.push({ id: 'docker', severity: 'error', text: <Trans i18nKey="newInstall.dockerNotConnected" /> });
  if (coStatus.backup_status && coStatus.backup_status !== '') own.push({ id: 'backup', severity: 'error', text: String(coStatus.backup_status) });
  if (coStatus.domain) own.push({ id: 'hostname', severity: 'error', text: t('navigation.home.localhostnotRecommendedError') });
  if (coStatus.needsRestart) {
    own.push({
      id: 'restart', severity: 'warning', text: t('navigation.home.configChangeRequiresRestartError'),
      actions: <PermissionGuard permission={PERM_ADMIN}>
        <Button size="small" variant="outlined" color="inherit" startIcon={<SyncOutlined />} onClick={() => setRestart(true)}>
          {t('global.restartCosmos')}
        </Button>
      </PermissionGuard>,
    });
  }
  if (!coStatus.hostmode && config) {
    own.push({ id: 'hostmode', severity: 'warning', text: t('navigation.home.cosmosNotDockerHostError'), detail: <Migrate014 config={config} /> });
  }

  const items = [...own, ...reachWatch.items, ...turnstile.items, ...certificates.items, ...update.items, ...health.items];
  // the certificates are a source too: when their status could not be read, that is said
  if (certificates.failed) {
    items.push({ id: 'certificates:unread', severity: 'unknown', text: t('health.certificatesUnread', 'Could not check the certificates: the server did not answer. Open the certificates to try again.') });
  }
  if (items.length === 0 && health.calm && certificates.loaded) {
    items.push({ id: 'calm', severity: 'success', attrs: { 'data-health': 'calm' }, text: health.calm });
  }

  return <>
    <NoticeArea items={items} label={t('health.title', 'Server health')} after={3} />
    {update.dialog}
    <RestartModal openModal={restart} setOpenModal={setRestart} />
  </>;
};

export default HomeNotices;

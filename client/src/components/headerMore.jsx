// Fork addition: the phone header's "More" menu. Below 600 px the header keeps
// the menu, the jobs, the notifications and Logout one tap away, each a
// finger's width with a gap; the actions used least (the server terminal and
// the two restarts) move here. From 600 px they are in the header as before.
import * as React from 'react';
import { IconButton, ListItemIcon, ListItemText, Menu, MenuItem, Tooltip } from '@mui/material';
import { BorderOuterOutlined, MoreOutlined, PoweroffOutlined, SyncOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import PermissionGuard from './permissionGuard';
import { PERM_ADMIN } from '../utils/permissions';
import RestartModal from '../pages/config/users/restart';

const HeaderMore = ({ onTerminal, sx }) => {
  const { t } = useTranslation();
  const [anchor, setAnchor] = React.useState(null);
  const [restart, setRestart] = React.useState(false);
  const [restartServer, setRestartServer] = React.useState(false);
  const [containerized, setContainerized] = React.useState(false);
  const menuId = React.useId();

  React.useEffect(() => {
    API.getStatus().then((res) => setContainerized(Boolean(res && res.data && res.data.containerized))).catch(() => {});
  }, []);

  const choose = (action) => () => { setAnchor(null); action(); };

  return <PermissionGuard permission={PERM_ADMIN}>
    <>
      <Tooltip title={t('header.more', 'More: terminal, restart')}>
        <IconButton
          sx={{ color: 'text.primary', ...(sx || {}) }}
          aria-label={t('header.more', 'More: terminal, restart')}
          aria-haspopup="true"
          aria-controls={anchor ? menuId : undefined}
          aria-expanded={anchor ? 'true' : undefined}
          onClick={(event) => setAnchor(event.currentTarget)}
        >
          <MoreOutlined />
        </IconButton>
      </Tooltip>
      <Menu id={menuId} anchorEl={anchor} open={Boolean(anchor)} onClose={() => setAnchor(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }} transformOrigin={{ vertical: 'top', horizontal: 'right' }}>
        <MenuItem onClick={choose(onTerminal)}>
          <ListItemIcon><BorderOuterOutlined /></ListItemIcon>
          <ListItemText>{t('mgmt.servapps.containers.terminal.enabled')}</ListItemText>
        </MenuItem>
        <MenuItem onClick={choose(() => setRestart(true))}>
          <ListItemIcon><SyncOutlined /></ListItemIcon>
          <ListItemText>{t('global.restartCosmos')}</ListItemText>
        </MenuItem>
        <MenuItem disabled={containerized} onClick={choose(() => setRestartServer(true))}>
          <ListItemIcon><PoweroffOutlined /></ListItemIcon>
          <ListItemText>{t('global.restartServer')}</ListItemText>
        </MenuItem>
      </Menu>
      <RestartModal openModal={restart} setOpenModal={setRestart} />
      <RestartModal openModal={restartServer} setOpenModal={setRestartServer} isHostMachine />
    </>
  </PermissionGuard>;
};

export default HeaderMore;

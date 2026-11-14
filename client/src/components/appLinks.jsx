// Fork addition: an app's links, grouped (the local ones under a house, the
// external ones under a globe), and the "Internet" action (exposure.jsx).
import * as React from 'react';
import { Tooltip, Typography } from '@mui/material';
import { GlobalOutlined, HomeOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import HostChip from './hostChip';
import { useCosmosPorts } from './routerPorts'; // the port the addresses are reached on (src/upnp)
import { shownRoute } from '../utils/routerPorts';
import PermissionGuard from './permissionGuard';
import { PERM_ADMIN } from '../utils/permissions';
import { ExposeChip } from './exposure';
import { groupAppRoutes, routeHost } from '../utils/appLinks';
import { useLocalNamesTitle } from './localNames'; // Fork addition

// The chips of an app's routes, local links first. In a row of chips each
// group starts with its icon; with `rows` each group gets a line of its own,
// headed by the icon and what it stands for.
const AppLinks = ({ routes, chipStyle, rows }) => {
  const { t } = useTranslation();
  // a host is stored without a port; its chip shows and opens it with the port it is reached on
  const ports = useCosmosPorts(null);
  const scheme = window.location.protocol === 'http:' ? 'http' : 'https';
  // Fork addition: whether the .local names are published and answered, see localNames.jsx
  const localTitle = useLocalNamesTitle((routes || []).map(routeHost));

  return groupAppRoutes(routes, window.location.host).map((group) => {
    const Icon = group.local ? HomeOutlined : GlobalOutlined;
    const label = group.local
      ? t('mgmt.urls.links.local', 'Local network')
      : t('mgmt.urls.links.external', 'Internet');
    const chips = group.routes.map((route) => <HostChip key={route.Name} route={shownRoute(route, ports, scheme)} settings style={chipStyle} />);

    if (rows) {
      return <div key={label}>
        <Typography variant="caption" color="textSecondary" component="div" style={chipStyle}>
          <Icon /> {label}
        </Typography>
        {chips}
      </div>;
    }
    return <React.Fragment key={label}>
      <Tooltip title={group.local && localTitle ? label + ' — ' + localTitle : label}>
        <Icon aria-label={label} style={{ flexShrink: 0 }} />
      </Tooltip>
      {chips}
    </React.Fragment>;
  });
};

// The "Internet" chip of an app: its addresses under the configured domains
// (through Cloudflare or directly) and its other ports on the router, in one
// dialog (components/exposure.jsx). It replaces the "Domains" dialog, which
// attached domains only.
export const AppDomains = ({ config, containerName, refresh, size, style, image }) => (
  <PermissionGuard permission={PERM_ADMIN}>
    <ExposeChip config={config} containerName={containerName} image={image} refresh={refresh} size={size} style={style} />
  </PermissionGuard>
);

export default AppLinks;

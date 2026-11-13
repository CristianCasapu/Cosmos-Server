// Fork addition: the chips of an app's published ports. Each opens the port
// the way it speaks (plain HTTP for nearly all, HTTPS for the few that do),
// says so in its tooltip, and a port a browser cannot open says why instead
// of being a dead link. The logic is in utils/portLinks.js.
import * as React from 'react';
import { Chip, Tooltip } from '@mui/material';
import { useTranslation } from 'react-i18next';
import { portChips } from '../utils/portLinks';
import { notify } from '../utils/activity';

const PortChips = ({ ports, style }) => {
  const { t } = useTranslation();

  return portChips(ports, window.location).map((chip) => {
    let tip;
    if (chip.href && chip.scheme === 'https') {
      tip = t('mgmt.servApps.port.opensHttps', 'Opens {{url}} — the app\'s own HTTPS port, directly, not through Cosmos.', { url: chip.href });
    } else if (chip.href && chip.mayUpgrade) {
      tip = t('mgmt.servApps.port.opensHttpName',
        'Opens {{url}} — plain HTTP, directly, not through Cosmos. If your browser turns it into https and shows an error, open it with the server\'s IP address instead of its name, or give the app a URL (New).',
        { url: chip.href });
    } else if (chip.href) {
      tip = t('mgmt.servApps.port.opensHttp', 'Opens {{url}} — plain HTTP, directly, not through Cosmos.', { url: chip.href });
    } else if (chip.why === 'udp') {
      tip = t('mgmt.servApps.port.udp', 'Port {{port}} is a UDP port: not something a browser can open.', { port: chip.published.port });
    } else if (chip.why === 'server') {
      tip = t('mgmt.servApps.port.server', 'Port {{port}} is published for the server itself only ({{ip}}): it cannot be opened from another device.', { port: chip.published.port, ip: chip.published.ip });
    } else {
      tip = t('mgmt.servApps.port.invalid', 'This port cannot be opened in a browser.');
    }

    const link = chip.href
      ? { component: 'a', href: chip.href, target: '_blank', rel: 'noopener noreferrer', clickable: true }
      // no link: a tap says why (a tooltip needs a mouse)
      : { onClick: () => notify(tip, 'info') };

    return <Tooltip key={chip.key} title={tip}>
      <Chip
        style={{ fontSize: '80%', cursor: chip.href ? 'pointer' : 'help', ...style }}
        label={chip.label}
        color="warning"
        variant="outlined"
        {...link}
      />
    </Tooltip>;
  });
};

export default PortChips;

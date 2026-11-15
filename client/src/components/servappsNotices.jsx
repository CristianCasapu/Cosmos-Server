// Fork addition: what needs attention on the ServApps page, in one notice area
// (components/noticeArea.jsx): the leftovers of interrupted edits, the apps
// with a newer image, the apps without an address. Each was an alert in a
// card of its own before, and they stacked.
import * as React from 'react';
import { Link } from '@mui/material';
import { Link as RouterLink } from 'react-router';
import { useTranslation } from 'react-i18next';
import NoticeArea from './noticeArea';
import useLeftoverNotices from './leftovers';
import useLocalAddressNotice from './localAddressesForAll';

// updates: the map of container names ("/jellyfin") with a newer image, as /api/config gives it
const ServAppsNotices = ({ config, servApps, selfName, refresh, updates, stack }) => {
  const { t } = useTranslation();
  const leftovers = useLeftoverNotices({ servApps, refresh });
  const local = useLocalAddressNotice({ config, containers: servApps, selfName, refresh });

  const names = Object.keys(updates || {}).map((name) => name.replace(/^\//, '')).filter(Boolean);
  const updateItems = names.length ? [{
    id: 'updates',
    severity: 'info',
    text: <>
      {t('mgmt.servapps.updatesAvailableFor')}{' '}
      {names.map((name, index) => <React.Fragment key={name}>
        {index > 0 && ', '}
        <Link component={RouterLink} to={'/cosmos-ui/servapps/containers/' + encodeURIComponent(name)} className="ct-notice-link">{name}</Link>
      </React.Fragment>)}
    </>,
    detail: t('mgmt.servapps.updatesHow', 'Each of them has an update button on its card and on its page.'),
  }] : [];

  // a stack's page shows its own apps only: the leftovers and the addresses are said on the main list
  const items = stack ? updateItems : [...leftovers.items, ...updateItems, ...local.items];

  return <>
    <NoticeArea items={items} label={t('mgmt.servapps.notices', 'ServApps that need attention')} />
    {!stack && leftovers.dialog}
    {!stack && local.dialog}
  </>;
};

export default ServAppsNotices;

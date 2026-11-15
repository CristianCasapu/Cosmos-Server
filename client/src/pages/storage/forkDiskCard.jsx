// Fork addition: one disk, partition or volume of the Storage page's tree, drawn within the skin
// (client/public/skin/theme.css, "the disks' tree"). Upstream's card (disks.jsx, `Disk`) was a row
// that never wrapped: on a phone its details ran out of the card over the name, the name broke a
// word per line and the buttons lay beside the card, off the screen. Here the card is three
// blocks that wrap (who it is; its identifiers; its commands): side by side on a wide screen,
// stacked on a phone with the commands as the card's footer. What it shows and offers comes from
// utils/diskCard.js.
import React from 'react';
import { Button, LinearProgress, Tooltip } from '@mui/material';
import { CompassOutlined } from '@ant-design/icons';
import { TreeItem } from '@mui/lab';
import { useTranslation } from 'react-i18next';
import MountDiskDialog from './mountDiskDialog';
import { diskChip, diskColor } from './smart';
import { simplifyNumber } from '../dashboard/components/utils';
import { diskActions, diskDetails, diskFacts, diskName, diskPercent } from '../../utils/diskCard';

import diskIcon from '../../assets/images/icons/disk.svg';
import partIcon from '../../assets/images/icons/part.svg';
import lockIcon from '../../assets/images/icons/lock.svg';
import raidIcon from '../../assets/images/icons/database.svg';

const icons = { disk: diskIcon, part: partIcon, lvm: partIcon, crypt: lockIcon, raid: raidIcon, raid0: raidIcon, raid1: raidIcon, raid5: raidIcon, raid6: raidIcon, raid10: raidIcon };

// the health stripe at the card's left, in the skin's state colours
const stripe = { green: 'var(--ct-ok-text)', yellow: 'var(--ct-warn-text)', orange: 'var(--ct-warn-text)', red: 'var(--ct-danger-text)' };

const ForkDiskCard = ({ disk, refresh, SetSMARTDialogOpened, containerized, FormatButton }) => {
  const { t } = useTranslation();
  const actions = diskActions(disk);
  const percent = diskPercent(disk);
  const details = diskDetails(disk);
  const temperature = disk.smart && disk.smart.Temperature;
  const health = disk.smart ? diskColor(disk) : 'gray';
  const label = {
    serial: t('mgmt.storage.disk.serial', 'Serial'),
    wwn: t('mgmt.storage.disk.wwn', 'WWN'),
    vendor: t('mgmt.storage.disk.vendor', 'Vendor'),
    rev: t('mgmt.storage.disk.rev', 'Firmware'),
    partuuid: t('mgmt.storage.disk.partuuid', 'Partition ID'),
  };

  return <TreeItem className="ct-disk-node" nodeId={disk.name} label={
    <div className="ct-disk-card" style={{ '--ct-disk-health': stripe[health] || 'var(--ct-border-strong)' }}>
      <div className="ct-disk-main">
        <div className="ct-disk-icon">
          <Tooltip title={disk.type}>
            <img width="48" height="48" alt={disk.type} src={icons[disk.type] || (disk.type === 'disk' ? diskIcon : partIcon)} />
          </Tooltip>
          {temperature ? <span className="ct-disk-temp">{diskChip(disk)} {temperature}°C</span> : null}
        </div>
        <div className="ct-disk-text">
          <div className="ct-disk-name">{diskName(disk)}</div>
          <div className="ct-disk-facts">{diskFacts(disk).map((fact, i) => <span key={i}>{fact}</span>)}</div>
          {disk.usage ? <div className="ct-disk-usage">
            <LinearProgress variant="determinate" color={percent > 95 ? 'error' : (percent > 75 ? 'warning' : 'info')} value={percent}
              aria-label={t('mgmt.storage.disk.used', 'Space used')} />
            <span>{simplifyNumber(disk.usage, 'b')} / {simplifyNumber(disk.size, 'b')} ({percent}%)</span>
          </div> : <div className="ct-disk-size">{simplifyNumber(disk.size, 'b')}</div>}
        </div>
      </div>

      {details.length > 0 && <dl className="ct-disk-details">
        {details.map(([key, value]) => <React.Fragment key={key}>
          <dt>{label[key] || key}</dt>
          <dd>{value}</dd>
        </React.Fragment>)}
      </dl>}

      {(actions.format || actions.mount || actions.unmount || actions.smart) && <div className="ct-disk-actions"
        /* a tap on a command (or in the dialog it opens) does not open or close the tree as well */
        onClick={(e) => e.stopPropagation()}>
        {actions.format ? <FormatButton disabled={containerized} disk={disk} refresh={refresh} /> : null}
        {actions.unmount ? <MountDiskDialog disabled={containerized} disk={disk} unmount={true} refresh={refresh} /> : null}
        {actions.mount ? <MountDiskDialog disk={disk} refresh={refresh} /> : null}
        {actions.smart ? <Button onClick={() => SetSMARTDialogOpened(disk)} variant="outlined" size="small" startIcon={<CompassOutlined />}>S.M.A.R.T.</Button> : null}
      </div>}
    </div>
  }>
    {disk.children && disk.children.map((child) => <ForkDiskCard key={child.name} containerized={containerized} disk={child} refresh={refresh}
      SetSMARTDialogOpened={SetSMARTDialogOpened} FormatButton={FormatButton} />)}
  </TreeItem>;
};

export default ForkDiskCard;

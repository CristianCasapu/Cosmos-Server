// Fork addition: in the "New mount" dialog, the partitions that are waiting
// to be mounted, to click instead of typing a device name: what the Disks tab
// lists with a file system and no mount point (utils/mountDevices.js).
// Clicking one fills the device and, while it is empty, the folder to mount to.
import * as React from 'react';
import { Chip, Stack, Typography } from '@mui/material';
import { HddOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import { simplifyNumber } from '../pages/dashboard/components/utils';
import { mountablePartitions, defaultMountPath } from '../utils/mountDevices';

const MountDeviceSuggestions = ({ formik, deviceField = 'device', pathField = 'path' }) => {
  const { t } = useTranslation();
  // null: not known (still loading, or the disks could not be listed)
  const [partitions, setPartitions] = React.useState(null);

  React.useEffect(() => {
    let live = true;
    API.storage.disks.list()
      .then((res) => { if (live) setPartitions(mountablePartitions(res && res.data)); })
      .catch(() => {});
    return () => { live = false; };
  }, []);

  if (partitions === null) {
    return null;
  }
  if (partitions.length === 0) {
    return <Typography variant="caption" color="textSecondary">
      {t('mgmt.storage.mount.noneWaiting', 'No partition is waiting to be mounted: every partition with a file system is in use already. A new disk has to be formatted first (Storage > Disks).')}
    </Typography>;
  }

  const current = formik.values[deviceField];
  return <Stack spacing={0.75}>
    <Typography variant="caption" color="textSecondary">
      {t('mgmt.storage.mount.waiting', 'Partitions that are not mounted (click to use one):')}
    </Typography>
    <Stack direction="row" useFlexGap flexWrap="wrap" spacing={1}>
      {partitions.map((partition) => (
        <Chip
          key={partition.device}
          size="small"
          clickable
          color={current === partition.device ? 'primary' : 'default'}
          variant={current === partition.device ? 'filled' : 'outlined'}
          icon={<HddOutlined />}
          label={[partition.device, partition.fstype, partition.size ? simplifyNumber(partition.size, 'b') : ''].filter(Boolean).join(' · ')}
          title={partition.model || undefined}
          onClick={() => {
            const proposed = defaultMountPath(current);
            formik.setFieldValue(deviceField, partition.device);
            // the folder follows the device as long as it was not typed by hand
            if (!formik.values[pathField] || (current && formik.values[pathField] === proposed)) {
              formik.setFieldValue(pathField, defaultMountPath(partition.device));
            }
          }}
          sx={{ maxWidth: '100%' }}
        />
      ))}
    </Stack>
  </Stack>;
};

export default MountDeviceSuggestions;

// Fork addition: the partitions that can be mounted, out of what the disks API
// returns (lsblk's tree), so the "New mount" dialog can offer them instead of
// having the device typed. The rule is the one the Disks tab uses for its own
// "Mount" button: a partition, or a disk without partitions, that carries a
// file system (not swap, not a member of a RAID) and is not mounted. Plain
// functions, no React: see mountDevices.test.mjs.

const UNMOUNTABLE = ['swap', 'linux_raid_member', 'LVM2_member', 'crypto_LUKS', 'zfs_member'];

// [{ device: '/dev/sdb1', fstype: 'ext4', size: 500107862016, model: 'WDC WD5000' }]
// in the order of the tree; `model` is the disk's.
export const mountablePartitions = (disks) => {
  const found = [];
  const walk = (devices, model) => {
    (Array.isArray(devices) ? devices : []).forEach((device) => {
      if (!device || typeof device.name !== 'string') return;
      const children = Array.isArray(device.children) ? device.children : [];
      const ownModel = device.model || model || '';
      const isLeafDisk = device.type === 'disk' && children.length === 0;
      if ((device.type === 'part' || isLeafDisk) && device.fstype && !UNMOUNTABLE.includes(device.fstype)
        && !device.mountpoint && device.name.startsWith('/dev/') && !found.find((f) => f.device === device.name)) {
        found.push({ device: device.name, fstype: device.fstype, size: Number(device.size) || 0, model: String(ownModel).trim() });
      }
      walk(children, ownModel);
    });
  };
  walk(disks, '');
  return found;
};

// The folder proposed for a device: /mnt/<its name>, as the Disks tab does.
export const defaultMountPath = (device) => '/mnt/' + String(device || '').replace(/^\/dev\//, '').replace(/[^A-Za-z0-9._-]+/g, '-');

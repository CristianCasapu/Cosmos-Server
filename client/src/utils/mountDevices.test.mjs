// Fork addition: tests of mountDevices.js. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { mountablePartitions, defaultMountPath } from './mountDevices.js';

// what API.storage.disks.list() returns, cut down
const disks = [
  { name: '/dev/nvme0n1', type: 'disk', size: 500107862016, model: 'Samsung SSD 980 ', children: [
    { name: '/dev/nvme0n1p1', type: 'part', size: 536870912, fstype: 'vfat', mountpoint: '/boot/efi' },
    { name: '/dev/nvme0n1p2', type: 'part', size: 498000000000, fstype: 'ext4', mountpoint: '/' },
    { name: '/dev/nvme0n1p3', type: 'part', size: 1000000000, fstype: 'swap' },
  ] },
  { name: '/dev/sda', type: 'disk', size: 4000787030016, model: 'WDC WD40EFRX', children: [
    { name: '/dev/sda1', type: 'part', size: 4000785964544, fstype: 'ext4' },
  ] },
  { name: '/dev/sdb', type: 'disk', size: 2000398934016, model: 'ST2000', fstype: 'xfs' }, // a file system on the whole disk
  { name: '/dev/sdc', type: 'disk', size: 2000398934016, model: 'ST2000' }, // empty: nothing to mount
  { name: '/dev/sdd', type: 'disk', size: 1000, children: [
    { name: '/dev/sdd1', type: 'part', size: 900, fstype: 'linux_raid_member', children: [
      { name: '/dev/md0', type: 'raid1', size: 900, fstype: 'ext4' },
    ] },
    { name: '/dev/sdd2', type: 'part', size: 50 }, // no file system
    { name: '/dev/sdd3', type: 'part', size: 50, fstype: 'crypto_LUKS' },
  ] },
  { name: '/dev/sr0', type: 'rom', size: 1073741312, fstype: 'iso9660' },
];

test('mountablePartitions: what has a file system and is not mounted', () => {
  assert.deepEqual(mountablePartitions(disks), [
    { device: '/dev/sda1', fstype: 'ext4', size: 4000785964544, model: 'WDC WD40EFRX' },
    { device: '/dev/sdb', fstype: 'xfs', size: 2000398934016, model: 'ST2000' },
  ]);
});

test('mountablePartitions: nothing to offer, and odd input', () => {
  assert.deepEqual(mountablePartitions([disks[0]]), []);
  assert.deepEqual(mountablePartitions([]), []);
  assert.deepEqual(mountablePartitions(null), []);
  assert.deepEqual(mountablePartitions([null, {}, { name: 'sdz1', type: 'part', fstype: 'ext4' }]), []);
  // the same partition listed twice is offered once
  assert.equal(mountablePartitions([disks[1], disks[1]]).length, 1);
});

test('defaultMountPath', () => {
  assert.equal(defaultMountPath('/dev/sda1'), '/mnt/sda1');
  assert.equal(defaultMountPath('/dev/mapper/vg-data'), '/mnt/mapper-vg-data');
  assert.equal(defaultMountPath('/dev/nvme0n1p3'), '/mnt/nvme0n1p3');
});

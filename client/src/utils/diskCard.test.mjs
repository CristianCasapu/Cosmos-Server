// Fork addition: tests of diskCard.js. Run: node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { diskDetails, diskName, diskFacts, diskPercent, diskActions } from './diskCard.js';

const raidDisk = {
  name: '/dev/sda', type: 'disk', size: 6e12, rota: true, serial: '0a1b2c3d4e5f60718293a4b5c6d7e8f9',
  wwn: '0x6f0e1d2c3b4a59687766554433221100', vendor: 'ACME    ', model: 'RAIDCTL V700', rev: '4.27', usage: 0,
  smart: { Temperature: 0 }, children: [{ name: '/dev/sda1', type: 'part' }],
};

test('the details list: the identifiers in the server\'s order, padding and empty values dropped', () => {
  assert.deepEqual(diskDetails(raidDisk), [
    ['serial', '0a1b2c3d4e5f60718293a4b5c6d7e8f9'],
    ['wwn', '0x6f0e1d2c3b4a59687766554433221100'],
    ['vendor', 'ACME'],
    ['rev', '4.27'],
  ]);
  assert.deepEqual(diskDetails({ name: '/dev/sdb1', type: 'part', partuuid: 'x-1', serial: '', wwn: null }), [['partuuid', 'x-1']]);
  assert.deepEqual(diskDetails(null), []);
});

test('names without /dev/ and /dev/mapper/', () => {
  assert.equal(diskName(raidDisk), 'sda');
  assert.equal(diskName({ name: '/dev/mapper/server--vg-root' }), 'server--vg-root');
  assert.equal(diskName({}), '');
});

test('the line under the name, swap said in words', () => {
  assert.deepEqual(diskFacts(raidDisk), ['HDD', 'RAIDCTL V700']);
  assert.deepEqual(diskFacts({ rota: false, fstype: 'ext4', mountpoint: '/' }), ['SSD', 'ext4', '/']);
  assert.deepEqual(diskFacts({ rota: false, fstype: 'swap', mountpoint: '[SWAP]' }), ['SSD', 'swap', 'swap space']);
});

test('usage in percent, bounded, 0 when unknown', () => {
  assert.equal(diskPercent({ size: 2e12, usage: 1.42e12 }), 71);
  assert.equal(diskPercent({ size: 0, usage: 5 }), 0);
  assert.equal(diskPercent({ size: 10, usage: 11 }), 100);
  assert.equal(diskPercent(undefined), 0);
});

test('commands: format and S.M.A.R.T. on disks, mount on a mountable file system only', () => {
  assert.deepEqual(diskActions(raidDisk), { format: true, mount: false, unmount: false, smart: true });
  // an empty new disk: nothing to mount
  assert.deepEqual(diskActions({ type: 'disk', children: [] }), { format: true, mount: false, unmount: false, smart: true });
  assert.equal(diskActions({ type: 'part', fstype: 'vfat' }).mount, true);
  assert.equal(diskActions({ type: 'part', fstype: 'ext4', mountpoint: '/mnt/media' }).mount, false);
  assert.equal(diskActions({ type: 'part', fstype: 'ext4', mountpoint: '/mnt/media' }).unmount, true);
  for (const fstype of ['LVM2_member', 'crypto_LUKS', 'linux_raid_member', 'swap']) assert.equal(diskActions({ type: 'part', fstype }).mount, false, fstype);
  // a logical volume: neither formatted nor given S.M.A.R.T. here; swap in use is not "unmounted"
  assert.deepEqual(diskActions({ type: 'lvm', fstype: 'swap', mountpoint: '[SWAP]' }), { format: false, mount: false, unmount: false, smart: false });
  assert.equal(diskActions({ type: 'lvm', fstype: 'ext4', mountpoint: '/' }).unmount, true);
});

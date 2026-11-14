// Fork addition: tests of formatDisk.js. Run with: node --test "client/src/utils/*.test.mjs"
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { FILESYSTEMS, diskSummary, blockedReason, proposedFilesystem, confirmMatches, plannedSteps, failureReason } from './formatDisk.js';

const usb = { name: '/dev/sdc', type: 'disk', model: 'USB Stick', size: 64e9, children: [{ name: '/dev/sdc1', type: 'part', fstype: 'exfat', size: 64e9 }] };
const system = { name: '/dev/sda', type: 'disk', size: 500e9, children: [{ name: '/dev/sda1', type: 'part', fstype: 'ext4', mountpoint: '/' }] };

test('the summary names the disk, its parts and what holds them', () => {
  const s = diskSummary(usb);
  assert.equal(s.name, 'sdc');
  assert.equal(s.whole, true);
  assert.deepEqual(s.parts.map((p) => p.name), ['sdc1']);
  assert.deepEqual(diskSummary(system).mounted, [{ name: 'sda1', mountpoint: '/' }]);
});

test('a disk with a mounted partition, or one in a RAID, is not offered for formatting', () => {
  assert.equal(blockedReason(diskSummary(system))[0], 'mgmt.storage.fork.blockedMounted');
  assert.equal(blockedReason(diskSummary({ name: '/dev/sdd', type: 'disk', fstype: 'linux_raid_member' }))[0], 'mgmt.storage.fork.blockedRaid');
  assert.equal(blockedReason(diskSummary(usb)), null);
});

test('the proposed file system keeps one other computers read, else ext4', () => {
  assert.equal(proposedFilesystem(diskSummary(usb)).value, 'exfat');
  assert.equal(proposedFilesystem(diskSummary({ name: '/dev/sdb', type: 'disk' })).value, 'ext4');
  assert.equal(proposedFilesystem(diskSummary({ name: '/dev/sdb1', type: 'part', fstype: 'vfat' })).value, 'fat32');
});

test('the typed name must be the device, as the server requires', () => {
  const s = diskSummary(usb);
  assert.ok(confirmMatches('sdc', s));
  assert.ok(confirmMatches(' /dev/sdc ', s));
  assert.ok(!confirmMatches('sdc1', s));
  assert.ok(!confirmMatches('', s));
  // the server's own rule (src/storage/fork_format.go)
  const go = readFileSync(new URL('../../../src/storage/fork_format.go', import.meta.url), 'utf8');
  assert.match(go, /typed := strings.TrimSpace\(confirm\)/);
});

test('the steps say what happens to a whole disk', () => {
  assert.equal(plannedSteps(diskSummary(usb), 'ext4').length, 3);
  assert.equal(plannedSteps(diskSummary({ name: '/dev/sdc1', type: 'part' }), 'xfs').length, 1);
});

test('every file system offered is one the server formats', () => {
  const go = readFileSync(new URL('../../../src/storage/fork_format.go', import.meta.url), 'utf8');
  for (const fs of FILESYSTEMS) {
    assert.match(go, new RegExp('"' + fs.value + '"'), fs.value);
  }
});

test('the reason of a failure is the server\'s last line without markers', () => {
  assert.equal(failureReason('\x1b[31m[ERROR]\x1b[0m [OPERATION FAILED] /dev/sdb1 is mounted at /mnt/data: unmount it first'), '/dev/sdb1 is mounted at /mnt/data: unmount it first');
});

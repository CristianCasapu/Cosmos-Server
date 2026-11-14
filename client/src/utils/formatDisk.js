// Fork addition: the logic of the format dialog (pages/storage/forkFormatDialog.jsx),
// pure and tested (formatDisk.test.mjs). The server checks the same and more
// (src/storage/fork_format.go): the typed name, a disk or partition in use
// (mounted, swap, part of a RAID or LVM volume), the file system.

// The file systems the server formats, with a line each. The first four are
// offered as a choice, the others behind "Other".
export const FILESYSTEMS = [
  { value: 'ext4', label: 'ext4', main: true, hint: ['mgmt.storage.fork.fsExt4', 'Recommended for a disk this server uses: Linux permissions, reliable, every Linux reads it.'] },
  { value: 'exfat', label: 'exFAT', main: true, hint: ['mgmt.storage.fork.fsExfat', 'To plug the disk into Windows and macOS computers too. No Linux permissions: every app sees every file.'] },
  { value: 'xfs', label: 'XFS', main: true, hint: ['mgmt.storage.fork.fsXfs', 'For large disks with large files (media). It can grow but never shrink.'] },
  { value: 'btrfs', label: 'Btrfs', main: true, hint: ['mgmt.storage.fork.fsBtrfs', 'Snapshots and checksums of the data, for those who know it.'] },
  { value: 'ext3', label: 'ext3', hint: ['mgmt.storage.fork.fsExt3', 'The older ext: only for an old system that needs it.'] },
  { value: 'ext2', label: 'ext2', hint: ['mgmt.storage.fork.fsExt2', 'No journal: only for very small or read-mostly devices.'] },
  { value: 'fat32', label: 'FAT32', hint: ['mgmt.storage.fork.fsFat32', 'For old devices (cameras, TVs, car radios). A file can be at most 4 GB.'] },
  { value: 'ntfs', label: 'NTFS', hint: ['mgmt.storage.fork.fsNtfs', 'Windows\' own. Linux reads and writes it, more slowly.'] },
];

export const filesystem = (value) => FILESYSTEMS.find((fs) => fs.value === value) || null;

// lsblk's names for what a device holds, as the server's choices.
const FAMILY = { vfat: 'fat32', exfat: 'exfat', ntfs: 'ntfs', ext4: 'ext4', ext3: 'ext3', ext2: 'ext2', xfs: 'xfs', btrfs: 'btrfs' };

export const baseName = (name) => String(name || '').replace(/^\/dev\//, '');

const flatten = (disk) => [disk, ...((disk && disk.children) || []).flatMap(flatten)];

// What is on a device, and what holds it, for the dialog.
export const diskSummary = (disk) => {
  const all = flatten(disk || {});
  const parts = ((disk && disk.children) || []).map((child) => ({
    name: baseName(child.name), fstype: child.fstype || '', size: child.size, mountpoint: child.mountpoint || '', label: child.label || '',
  }));
  const mounted = all.filter((d) => d && d.mountpoint).map((d) => ({ name: baseName(d.name), mountpoint: d.mountpoint }));
  const raid = all.filter((d) => d && (d.fstype === 'linux_raid_member' || d.fstype === 'LVM2_member' || d.fstype === 'crypto_LUKS'));
  return {
    name: baseName(disk && disk.name),
    path: (disk && disk.name) || '',
    whole: Boolean(disk && disk.type === 'disk'),
    model: (disk && disk.model) || '',
    size: disk && disk.size,
    fstype: (disk && disk.fstype) || '',
    parts,
    mounted,
    member: raid.map((d) => ({ name: baseName(d.name), fstype: d.fstype })),
  };
};

// Why the dialog will not format it (the server says the same), or null.
export const blockedReason = (summary) => {
  if (summary.mounted.length > 0) {
    const m = summary.mounted[0];
    return ['mgmt.storage.fork.blockedMounted', '{{name}} is mounted at {{path}}: unmount it first (the Unmount button on the disk). The disk that holds the system is never formatted.', { name: m.name, path: m.mountpoint }];
  }
  if (summary.member.length > 0) {
    const m = summary.member[0];
    return m.fstype === 'linux_raid_member'
      ? ['mgmt.storage.fork.blockedRaid', '{{name}} is part of a RAID array: remove it from the array first.', { name: m.name }]
      : ['mgmt.storage.fork.blockedVolume', '{{name}} holds an LVM or encrypted volume: close that volume first.', { name: m.name }];
  }
  return null;
};

// The file system proposed, and where that came from.
export const proposedFilesystem = (summary) => {
  const held = [summary.fstype, ...summary.parts.map((p) => p.fstype)].map((fs) => FAMILY[fs]).filter(Boolean);
  const shared = held.find((fs) => fs === 'exfat' || fs === 'fat32' || fs === 'ntfs');
  if (shared) {
    return { value: shared, source: ['mgmt.storage.fork.proposeKeep', 'It holds {{fs}} now, which other computers read: proposed so that it goes on working with them.', { fs: filesystem(shared).label }] };
  }
  return { value: 'ext4', source: ['mgmt.storage.fork.proposeExt4', 'Proposed for a disk this server uses.'] };
};

// Does the typed text name the device? The server accepts the same.
export const confirmMatches = (typed, summary) => {
  const text = String(typed || '').trim();
  return text !== '' && (text === summary.name || text === summary.path);
};

// What the format does, step by step, for the dialog.
export const plannedSteps = (summary, fs) => {
  const label = (filesystem(fs) || { label: fs }).label;
  if (summary.whole) {
    return [
      ['mgmt.storage.fork.stepTable', 'A new partition table replaces everything on {{name}}.', { name: summary.name }],
      ['mgmt.storage.fork.stepPartition', 'One partition is made over the whole disk.'],
      ['mgmt.storage.fork.stepFormat', 'It is formatted as {{fs}}; then it can be mounted.', { fs: label }],
    ];
  }
  return [['mgmt.storage.fork.stepFormatPart', '{{name}} is formatted as {{fs}}; then it can be mounted.', { name: summary.name, fs: label }]];
};

// The reason the server gave, from its last line, without the markers.
export const failureReason = (line) => String(line || '').replace(/\x1b\[[0-9;]*m/g, '').replace(/^.*\[OPERATION FAILED\]\s*/, '').trim();

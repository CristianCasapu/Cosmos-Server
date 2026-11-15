// Fork addition: realistic answers for the pages a throwaway instance shows
// empty (it has no disks, no mounts of its own, no parity, a handful of users),
// for scripts/fork-ui-gallery.mjs. The shapes are those the server's handlers
// return (src/storage/disks.go: lsblk's columns plus usage and SMART;
// src/storage/mounts.go; src/storage/fork_candidates.go; utils.SnapRAIDConfig
// plus Status; utils.User). Every value is made up: serials, identifiers, names
// and addresses are invented or documentation values.
const TB = 1e12;
const GB = 1e9;

// what the server sends for a device without SMART (a partition, a RAID virtual disk behind a controller)
const noSmart = () => ({ Temperature: 0, Read: 0, Written: 0, PowerOnHours: 0, PowerCycles: 0, AdditionalData: null, Thresholds: null });
const u128 = (n) => ({ Val: [n, 0] });
const part = (name, size, fields, children = []) => ({ name, type: 'part', size, rota: false, ...fields, children, usage: fields.usage || 0, smart: noSmart() });

// an ATA disk's SMART page: id -> attribute (the names and types the library computes)
const ataAttr = (Id, Current, Worst, Name, ValueRaw, Type = 3) => ({ Id, Flags: 50, Current, Worst, VendorBytes: [0, 0, 0, 0, 0, 0], Name, Type, ValueRaw });
const ataSmart = (temperature, hours) => ({
  Temperature: temperature, Read: 412345678901, Written: 98765432101, PowerOnHours: hours, PowerCycles: 41,
  AdditionalData: { Version: 16, Attrs: {
    1: ataAttr(1, 100, 100, 'Raw_Read_Error_Rate', 0), 3: ataAttr(3, 172, 168, 'Spin_Up_Time', 7344, 10), 4: ataAttr(4, 100, 100, 'Start_Stop_Count', 52),
    5: ataAttr(5, 100, 100, 'Reallocated_Sector_Ct', 0), 7: ataAttr(7, 100, 253, 'Seek_Error_Rate', 0), 9: ataAttr(9, 61, 61, 'Power_On_Hours', hours, 1),
    10: ataAttr(10, 100, 253, 'Spin_Retry_Count', 0), 12: ataAttr(12, 100, 100, 'Power_Cycle_Count', 41), 192: ataAttr(192, 200, 200, 'Power-Off_Retract_Count', 18),
    193: ataAttr(193, 199, 199, 'Load_Cycle_Count', 4211), 194: ataAttr(194, 116, 102, 'Temperature_Celsius', temperature, 6), 197: ataAttr(197, 200, 200, 'Current_Pending_Sector', 0),
    198: ataAttr(198, 100, 253, 'Offline_Uncorrectable', 0), 199: ataAttr(199, 200, 200, 'UDMA_CRC_Error_Count', 0),
  } },
  Thresholds: { Revnumber: 1, Thresholds: { 1: 51, 3: 21, 4: 0, 5: 10, 7: 0, 9: 0, 10: 0, 12: 0, 192: 0, 193: 0, 194: 0, 197: 0, 198: 0, 199: 0 } },
});

export const DISKS = [
  // two virtual disks of a hardware RAID controller: no SMART through the controller
  { name: '/dev/sda', type: 'disk', size: 5.99 * TB, rota: true, serial: '0a1b2c3d4e5f60718293a4b5c6d7e8f9', wwn: '0x6f0e1d2c3b4a59687766554433221100', vendor: 'ACME    ', model: 'RAIDCTL V700', rev: '4.27', usage: 0, smart: noSmart(), children: [
    part('/dev/sda1', 0.5 * GB, { fstype: 'vfat', mountpoint: '/boot/efi', partuuid: '1f2e3d4c-0001-4a5b-8c9d-0e1f2a3b4c5d', usage: 0.03 * GB, rota: true }),
    part('/dev/sda2', 2 * TB, { fstype: 'ext4', mountpoint: '/mnt/media', partuuid: '1f2e3d4c-0002-4a5b-8c9d-0e1f2a3b4c5d', usage: 1.42 * TB, rota: true }),
    part('/dev/sda3', 3.99 * TB, { fstype: 'ext4', mountpoint: '/mnt/backup-of-the-family-photos', partuuid: '1f2e3d4c-0003-4a5b-8c9d-0e1f2a3b4c5d', usage: 3.88 * TB, rota: true }),
  ] },
  { name: '/dev/sdb', type: 'disk', size: 31.99 * TB, rota: true, serial: 'f9e8d7c6b5a4938271605f4e3d2c1b0a', wwn: '0x6f0e1d2c3b4a5968ffeeddccbbaa9988', vendor: 'ACME    ', model: 'RAIDCTL V700', rev: '4.27', usage: 0, smart: noSmart(), children: [
    part('/dev/sdb1', 31.99 * TB, { fstype: 'xfs', mountpoint: '/mnt/archive', partuuid: '2a3b4c5d-0001-4e6f-8a7b-9c0d1e2f3a4b', usage: 12.6 * TB, rota: true }),
  ] },
  // an NVMe SSD with the system on LVM
  { name: '/dev/nvme0n1', type: 'disk', size: 1.024 * TB, rota: false, serial: 'S9XAMPLE0R123456', wwn: 'eui.0025380000000000a1b2c3d4e5f60718', model: 'EXAMPLE PM9A1 NVMe SSD MZXL1T0HBLR-00000 1TB', rev: 'XPLE7Q01', usage: 0,
    smart: { Temperature: 44, Read: 51234567, Written: 81234567, PowerOnHours: 9123, PowerCycles: 211, AdditionalData: {
      CritWarning: 0, Temperature: 317, AvailSpare: 100, SpareThresh: 10, PercentUsed: 3, EnduranceCritWarning: 0,
      DataUnitsRead: u128(51234567), DataUnitsWritten: u128(81234567), HostReads: u128(912345678), HostWrites: u128(1712345678),
      CtrlBusyTime: u128(4211), PowerCycles: u128(211), PowerOnHours: u128(9123), UnsafeShutdowns: u128(17), MediaErrors: u128(0), NumErrLogEntries: u128(42),
      WarningTempTime: 0, CritCompTime: 0, TempSensor: [317, 321, 0, 0, 0, 0, 0, 0], ThermalTransitionCount: [0, 0], ThermalManagementTime: [0, 0],
    }, Thresholds: { VendorID: 5197, Ssvid: 5197 } },
    children: [
      part('/dev/nvme0n1p1', 1.07 * GB, { fstype: 'vfat', partuuid: '3c4d5e6f-0001-4a1b-9c2d-3e4f5a6b7c8d' }),
      part('/dev/nvme0n1p2', 2.15 * GB, { fstype: 'ext4', mountpoint: '/boot', partuuid: '3c4d5e6f-0002-4a1b-9c2d-3e4f5a6b7c8d', usage: 0.31 * GB }),
      part('/dev/nvme0n1p3', 1.02 * TB, { fstype: 'LVM2_member', partuuid: '3c4d5e6f-0003-4a1b-9c2d-3e4f5a6b7c8d' }, [
        { name: '/dev/mapper/server--vg-root', type: 'lvm', size: 0.95 * TB, rota: false, fstype: 'ext4', mountpoint: '/', children: [], usage: 0.21 * TB, smart: noSmart() },
        { name: '/dev/mapper/server--vg-swap_1', type: 'lvm', size: 68 * GB, rota: false, fstype: 'swap', mountpoint: '[SWAP]', children: [], usage: 0, smart: noSmart() },
      ]),
    ] },
  // two plain SATA disks with SMART, one of them the parity disk
  { name: '/dev/sdc', type: 'disk', size: 8.0 * TB, rota: true, serial: 'ZX1EXAMPLE', wwn: '0x5000c500a1b2c3d4', vendor: 'ATA     ', model: 'EXAMPLEDISK HD8000-ABC', rev: 'SC60', usage: 0, smart: ataSmart(36, 21877), children: [
    part('/dev/sdc1', 8.0 * TB, { fstype: 'xfs', mountpoint: '/mnt/disk1', partuuid: '4d5e6f70-0001-4b2c-8d3e-4f5a6b7c8d9e', usage: 5.1 * TB, rota: true }),
  ] },
  { name: '/dev/sdd', type: 'disk', size: 8.0 * TB, rota: true, serial: 'ZX1EXAMPLF', wwn: '0x5000c500a1b2c3d5', vendor: 'ATA     ', model: 'EXAMPLEDISK HD8000-ABC', rev: 'SC60', usage: 0, smart: ataSmart(52, 21901), children: [
    part('/dev/sdd1', 8.0 * TB, { fstype: 'ext4', mountpoint: '/mnt/parity1', partuuid: '4d5e6f70-0002-4b2c-8d3e-4f5a6b7c8d9e', usage: 6.9 * TB, rota: true }),
  ] },
  // a new disk, nothing on it yet: Format and S.M.A.R.T. only
  { name: '/dev/sde', type: 'disk', size: 4.0 * TB, rota: true, serial: 'WX0EXAMPLE1234567', wwn: '0x50014ee2b1c2d3e4', vendor: 'ATA     ', model: 'EXAMPLE RED PLUS 4TB', rev: '80.00A80', usage: 0, smart: ataSmart(31, 12), children: [] },
];

export const MOUNTS = [
  { path: '/', permenant: true, device: '/dev/mapper/server--vg-root', type: 'ext4', opts: ['rw', 'relatime', 'errors=remount-ro'] },
  { path: '/boot', permenant: true, device: '/dev/nvme0n1p2', type: 'ext4', opts: ['rw', 'relatime'] },
  { path: '/boot/efi', permenant: true, device: '/dev/sda1', type: 'vfat', opts: ['rw', 'relatime', 'fmask=0077', 'dmask=0077', 'codepage=437', 'iocharset=ascii', 'shortname=mixed', 'utf8', 'errors=remount-ro'] },
  { path: '/mnt/media', permenant: true, device: '/dev/sda2', type: 'ext4', opts: ['rw', 'relatime'] },
  { path: '/mnt/backup-of-the-family-photos', permenant: true, device: '/dev/sda3', type: 'ext4', opts: ['rw', 'relatime'] },
  { path: '/mnt/archive', permenant: true, device: '/dev/sdb1', type: 'xfs', opts: ['rw', 'relatime', 'attr2', 'inode64', 'logbufs=8', 'logbsize=32k', 'noquota'] },
  { path: '/mnt/disk1', permenant: true, device: '/dev/sdc1', type: 'xfs', opts: ['rw', 'relatime', 'attr2', 'inode64'] },
  { path: '/mnt/parity1', permenant: true, device: '/dev/sdd1', type: 'ext4', opts: ['rw', 'relatime'] },
  { path: '/mnt/pool', permenant: true, device: '/mnt/media:/mnt/archive:/mnt/disk1', type: 'fuse.mergerfs', opts: ['rw', 'relatime', 'user_id=0', 'group_id=0', 'default_permissions', 'allow_other'] },
  { path: '/var/lib/docker/overlay2/3f0c9a8b7e6d5c4b3a2918f7e6d5c4b3a2918f7e6d5c4b3a2918f7e6d5c4b3a/merged', permenant: false, device: 'overlay', type: 'overlay', opts: ['rw', 'relatime', 'lowerdir=/var/lib/docker/overlay2/l/EXAMPLE'] },
];

export const CANDIDATES = {
  devices: [
    { device: '/dev/sde', disk: '/dev/sde', type: 'disk', fstype: '', label: '', model: 'EXAMPLE RED PLUS 4TB', size: 4 * TB, mountable: false, use: { code: 'nofs', detail: '' } },
    { device: '/dev/nvme0n1p1', disk: '/dev/nvme0n1', type: 'part', fstype: 'vfat', label: '', model: 'EXAMPLE PM9A1 NVMe SSD MZXL1T0HBLR-00000 1TB', size: 1.07 * GB, mountable: true, use: { code: '', detail: '' } },
    { device: '/dev/sda2', disk: '/dev/sda', type: 'part', fstype: 'ext4', label: 'media', model: 'RAIDCTL V700', size: 2 * TB, mountable: false, use: { code: 'mounted', detail: '/mnt/media' } },
    { device: '/dev/nvme0n1p3', disk: '/dev/nvme0n1', type: 'part', fstype: 'LVM2_member', label: '', model: 'EXAMPLE PM9A1 NVMe SSD MZXL1T0HBLR-00000 1TB', size: 1.02 * TB, mountable: false, use: { code: 'nofs', detail: 'LVM2_member' } },
  ],
  mounts: [
    { path: '/', device: '/dev/mapper/server--vg-root', disk: '/dev/nvme0n1', fstype: 'ext4', size: 0.95 * TB, free: 0.74 * TB, permanent: true, kind: 'system' },
    { path: '/mnt/media', device: '/dev/sda2', disk: '/dev/sda', fstype: 'ext4', size: 2 * TB, free: 0.58 * TB, permanent: true, kind: 'data' },
    { path: '/mnt/backup-of-the-family-photos', device: '/dev/sda3', disk: '/dev/sda', fstype: 'ext4', size: 3.99 * TB, free: 0.11 * TB, permanent: true, kind: 'data' },
    { path: '/mnt/archive', device: '/dev/sdb1', disk: '/dev/sdb', fstype: 'xfs', size: 31.99 * TB, free: 19.39 * TB, permanent: true, kind: 'data' },
    { path: '/mnt/disk1', device: '/dev/sdc1', disk: '/dev/sdc', fstype: 'xfs', size: 8 * TB, free: 2.9 * TB, permanent: true, kind: 'data' },
    { path: '/mnt/parity1', device: '/dev/sdd1', disk: '/dev/sdd', fstype: 'ext4', size: 8 * TB, free: 1.1 * TB, permanent: true, kind: 'data' },
    { path: '/mnt/pool', device: '/mnt/media:/mnt/archive:/mnt/disk1', disk: '', fstype: 'fuse.mergerfs', size: 41.99 * TB, free: 22.87 * TB, permanent: true, kind: 'merge' },
  ],
  base: '/mnt', inContainer: false,
};

export const SNAPRAID = [{
  Name: 'Media parity', Enabled: true, Data: { disk1: '/mnt/disk1', media: '/mnt/media' }, Parity: ['/mnt/parity1'],
  SyncCrontab: '0 0 2 * * *', ScrubCrontab: '0 0 4 */2 * *', CheckOnFix: false,
  Status: 'Self test...\nLoading state from /mnt/disk1/snapraid.content...\nUsing 1843 MiB of memory for the file-system.\nSnapRAID status report:\n\n   Files Fragmented Excess  Wasted  Used    Free  Use Name\n            Files  Fragments  GB      GB      GB\n  412873     211     902       -    5100    2900  63% disk1\n   98112      14      77       -    1420     580  71% media\n --------------------------------------------------------------------------\n  510985     225     979     0.0    6520    3480  65%\n\nThe oldest block was scrubbed 9 days ago, the median 4, the newest 0.\nNo sync is in progress.\nThe 21% of the array is not scrubbed.\nNo error detected.\n',
}];

export const UNMOUNT_CHECK = { mountPoint: '/mnt/media', device: '/dev/sda2', permanent: true, system: false, users: [{ pid: 4242, name: 'jellyfin', how: 'files' }, { pid: 4317, name: 'smbd', how: 'working directory' }] };

const ok = (data) => ({ status: 'OK', data });
// [path, answer] for the gallery's `mock` option (paths match by prefix: the longer first)
export const storageMock = [
  ['/cosmos/api/storage/candidates', ok(CANDIDATES)],
  ['/cosmos/api/storage/unmount-check', ok(UNMOUNT_CHECK)],
  ['/cosmos/api/disks', ok(DISKS)],
  ['/cosmos/api/mounts', ok(MOUNTS)],
  ['/cosmos/api/snapraid', ok(SNAPRAID)],
];

// the users list: the seeded accounts (admin, maria, an invitation not yet taken, a long name and address)
// plus a second administrator who signed in two hours ago, which makes five: the most a server without a
// licence takes. The pending invitation is shown as run out. In the shape of utils.User.
const day = 24 * 3600 * 1000;
const iso = (ms) => new Date(ms).toISOString();
export const extraUsers = (now = Date.now()) => [
  { nickname: 'ops', role: 2, email: 'ops@example.org', registeredAt: iso(now - 30 * day), registerKeyExp: iso(now - 29 * day), createdAt: iso(now - 30 * day), lastLogin: iso(now - 2 * 3600 * 1000), lastPasswordChangedAt: iso(now - 30 * day), link: '' },
];
const expire = (u, now) => (new Date(u.registeredAt).getTime() > 0 ? u : { ...u, registerKeyExp: iso(now - 3 * day) });
export const usersPatch = [['/cosmos/api/users', (j) => (Array.isArray(j.data) ? { ...j, data: [...j.data.map((u) => expire(u, Date.now())), ...extraUsers()] } : j)]];

// the bell's list: a month of a busy server (utils.Notification has no JSON tags: the Go field names)
const minute = 60 * 1000;
const notes = [
  ['info', 'Container updated', 'jellyfin was updated to the latest image and started again.', '/cosmos-ui/servapps/containers/jellyfin'],
  ['warn', 'Disk almost full', 'The disk mounted on /mnt/backup-of-the-family-photos is 97% full (3.9 TB of 4 TB). Free some space or move the snapshots elsewhere.', '/cosmos-ui/storage'],
  ['error', 'Scheduled job failed', 'nightly-cleanup ended with exit code 1 after 0.4 s: rm: cannot remove \'/mnt/media/cache/transcodes/0f1e2d3c4b5a69788796a5b4c3d2e1f0.ts\': Device or resource busy', '/cosmos-ui/cron'],
  ['warn', 'Address blocked', '192.0.2.77 was blocked for 30 minutes after 6 wrong passwords for "admin".', '/cosmos-ui/security'],
  ['info', 'Certificate renewed', 'The certificate for *.example.org and example.org was renewed; it is valid until 12 February 2027.', ''],
  ['error', 'Container stopped', 'nextcloud-aio-mastercontainer-with-a-rather-long-name exited with code 137 (out of memory).', '/cosmos-ui/servapps/containers/nextcloud-aio-mastercontainer-with-a-rather-long-name'],
];
export const NOTIFICATIONS = Array.from({ length: 30 }, (_, i) => {
  const [Level, Title, Message, Link] = notes[i % notes.length];
  return { ID: 1000 - i, Title, Message, Vars: '', Icon: '', Link, Date: new Date(Date.now() - (i * 97 + 3) * minute).toISOString(), Level, Read: i > 4, Recipient: 'admin', Actions: null };
});
export const notificationsMock = [['/cosmos/api/notifications', ok(NOTIFICATIONS)]];

// the events list: many rows, long objects and labels (src/metrics/events.go answers status, total, data)
const eventKinds = [
  ['info', 'cosmos.docker.container.start', 'Container started', 'container@jellyfin'],
  ['warning', 'cosmos.proxy.shield.block', 'Request blocked by Smart Shield: too many requests from 198.51.100.23 to /api/items/0f1e2d3c4b5a69788796a5b4c3d2e1f0/PlaybackInfo', 'route@Jellyfin'],
  ['error', 'cosmos.docker.container.die', 'Container exited with code 137 (out of memory)', 'container@nextcloud-aio-mastercontainer-with-a-rather-long-name'],
  ['info', 'cosmos.user.login', 'User logged in', 'user@alexandruconstantinescu2026home'],
  ['info', 'cosmos.storage.mount', 'Mounted /dev/sda3 on /mnt/backup-of-the-family-photos', 'disk@/dev/sda3'],
];
export const EVENTS = Array.from({ length: 60 }, (_, i) => {
  const [level, eventId, label, object] = eventKinds[i % eventKinds.length];
  return { id: 5000 - i, label, application: 'Cosmos', eventId, date: new Date(Date.now() - (i * 13 + 1) * minute).toISOString(), level, data: { source: '192.0.2.' + (10 + (i % 40)), details: 'made-up event for the gallery' }, object };
});
export const eventsMock = [['/cosmos/api/events', { status: 'OK', total: 1240, data: EVENTS }]];

// blocks at Cloudflare's edge (src/edgeblock/api.go answers the card and the dashboard with one status):
// switched on for one domain, six rules of every reason, an IPv6 /64, one deleted in Cloudflare's dashboard
const hour = 3600 * 1000;
const rule = (key, reason, mode, sinceHours, forHours, extra = {}) => ({
  key, address: key, zone: 'example.org', mode, reason, state: 'active',
  since: iso(Date.now() - sinceHours * hour), until: iso(Date.now() + forHours * hour), ...extra,
});
export const EDGE_RULES = [
  rule('198.51.100.89', 'auth', 'managed_challenge', 0.2, 3.8),
  rule('198.51.100.88', 'auth', 'managed_challenge', 0.5, 3.5),
  rule('203.0.113.42', 'probe', 'block', 1, 23),
  rule('203.0.113.41', 'repeat', 'block', 2, 166),
  rule('2001:db8:1:2::/64', 'manual', 'managed_challenge', 3, 21, { address: '2001:db8:1:2::5', actor: 'admin' }),
  rule('198.51.100.77', 'scan', 'managed_challenge', 5, -1, { state: 'gone' }),
];
const JAILS = [
  { name: 'auth', maxRetry: 5, findTime: 3600, banTime: 14400, mode: 'managed_challenge' },
  { name: 'probe', maxRetry: 3, findTime: 3600, banTime: 86400, mode: 'block' },
  { name: 'scan', maxRetry: 10, findTime: 600, banTime: 14400, mode: 'managed_challenge' },
  { name: 'abuse', maxRetry: 3, findTime: 86400, banTime: 14400, mode: 'managed_challenge' },
  { name: 'repeat', maxRetry: 3, findTime: 604800, banTime: 604800, mode: 'block' },
];
export const EDGE_STATUS = {
  enabled: true, adminDays: 7, counts: { active: 5, gone: 1 }, dropped: 0, droppedAt: null, durations: ['1h', '1d', '7d', '30d'],
  jails: JAILS, leftovers: [], live: 5, maxPerHour: 60, maxRules: 500, originPulls: false, pullsKind: '', withAddresses: true,
  zones: [{ name: 'example.org', shielded: 2, proven: true, provenAt: iso(Date.now() - 2 * day), tokenChanged: false }],
  rules: EDGE_RULES,
};
export const edgeMock = [['/cosmos/api/cloudflare/edge-blocks', ok(EDGE_STATUS)]];
// the dashboard's busiest addresses as they come through Cloudflare (a test instance only sees local ones,
// for which "Block at Cloudflare" is not offered): the first rows are given the addresses of the rules
const EDGE_TOP = ['198.51.100.89', '198.51.100.88', '203.0.113.42', '198.51.100.150'];
export const edgeOverviewPatch = ['/cosmos/api/security/overview', (j) => {
  if (!j || !j.data) return j;
  const top = (j.data.top || []).slice();
  EDGE_TOP.forEach((ip, i) => {
    const row = top[i] || { total: 6 - i, first: iso(Date.now() - hour), last: iso(Date.now() - 600000), country: '', kinds: [{ kind: 'auth.login.failed', total: 6 - i }] };
    top[i] = { ...row, ip, local: false, kinds: i < 3 ? [...(row.kinds || []), { kind: 'edge.blocked', total: 1 }] : row.kinds };
  });
  return { ...j, data: { ...j.data, top } };
}];

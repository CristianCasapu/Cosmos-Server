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
export const notificationsMock = [['/cosmos/api/notifications', ok(NOTIFICATIONS)],
  // the bell asks the notifications page's list since 36-notifications: the same rows in that shape
  ['/cosmos/api/notifications/list', ok({ items: NOTIFICATIONS.map((n) => ({ id: n.ID, ref: '', kind: '', source: 'other', level: n.Level === 'warn' ? 'warning' : n.Level, read: n.Read, date: n.Date,
    firstSeen: null, count: 1, resolved: null, subject: null, link: n.Link, legacy: true, title: { key: '', text: n.Title, vars: {} }, message: { key: '', text: n.Message, vars: {} } })), next: '', unread: 5, sources: ['other'] })]];

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

// ---- the notifications page, the details of each kind and the bell (src/fork_notifications.go answers;
// the shapes are those of the plan 36-notifications.md, 3.3). Made-up values, documentation addresses.
const hour = 60 * minute;
const at = (msAgo) => new Date(Date.now() - msAgo).toISOString();
const s = (key, text, vars) => ({ key, text, vars: vars || {} });
const fact = (key, text, value) => ({ key, text, value });
const step = (key, text, vars, link, action, label) => ({ key, text, vars: vars || {}, link: link || '', action: action || '', ...(label ? { label } : {}) });
const item = (id, fields) => ({
  id, ref: (id.toString(16) + 'a1b2c3d4e5f60718').slice(0, 16), kind: '', source: 'other', level: 'info', read: false,
  date: at(id * 7 * minute), firstSeen: null, count: 1, resolved: null, subject: null, link: '', legacy: false,
  title: s('', ''), message: s('', ''), ...fields,
});
const ROUTER_ANSWER = 'HTTPS: Post "http://192.168.1.1:2828/upnp/control/9f2c41d0-5b7a-4c1e-8d3f-2a6b0c9e7f13/wanipconn-8": EOF';
export const NOTIFICATION_ITEMS = [
  item(101, {
    kind: 'upnp.forward.failed', source: 'upnp', level: 'error', read: false, date: at(38 * minute), firstSeen: at(3 * hour + 12 * minute), count: 4, resolved: at(9 * minute),
    subject: { type: 'port', name: 'HTTPS 2053/TCP', link: '/cosmos-ui/config-general/http-server' }, link: '/cosmos-ui/config-general/http-server',
    title: s('notif.upnp.forward.failed.title', 'The router no longer forwards port {{port}}', { port: '2053' }),
    message: s('notif.upnp.forward.failed.what.absent', 'The router no longer forwards port {{port}} to this server: Cosmos cannot be reached from the internet.', { port: '2053' }),
  }),
  item(102, {
    kind: 'upnp.forward.restored', source: 'upnp', level: 'success', read: false, date: at(9 * minute),
    subject: { type: 'port', name: 'HTTPS 2053/TCP', link: '/cosmos-ui/config-general/http-server' }, link: '/cosmos-ui/config-general/http-server',
    title: s('notif.upnp.forward.restored.title', 'Port {{port}} is forwarded again', { port: '2053' }),
    message: s('notif.upnp.forward.restored.what', 'The router forwards port {{port}} to this server again since {{at}}: Cosmos can be reached from the internet. It was not forwarded for {{minutes}} minutes.', { port: '2053', at: '08:51', minutes: '183' }),
  }),
  item(103, {
    kind: 'certificate.failed', source: 'certificates', level: 'error', count: 2, firstSeen: at(26 * hour), date: at(2 * hour),
    subject: { type: 'domain', name: 'photos.example.org', link: '/cosmos-ui/config-general/https' }, link: '/cosmos-ui/config-general/https',
    title: s('notif.certificate.failed.title', 'The certificate of {{domain}} could not be renewed', { domain: 'photos.example.org' }),
    message: s('notif.certificate.failed.what', 'Let\'s Encrypt refused the order for {{domain}}: the DNS provider did not accept the record Cosmos tried to write.', { domain: 'photos.example.org' }),
  }),
  item(104, {
    kind: 'certificate.expiring', source: 'certificates', level: 'warning', read: true, date: at(5 * hour),
    subject: { type: 'domain', name: 'example.org', link: '/cosmos-ui/config-general/https' }, link: '/cosmos-ui/config-general/https',
    title: s('notif.certificate.expiring.title', 'The certificate of {{domain}} ends in {{days}} days', { domain: 'example.org', days: '12' }),
    message: s('notif.certificate.expiring.what', 'The certificate of {{domain}} ends on {{date}} and could not be renewed yet.', { domain: 'example.org', date: '27 November 2026' }),
  }),
  item(105, {
    kind: 'certificate.renewed', source: 'certificates', level: 'info', read: true, date: at(30 * hour),
    subject: { type: 'domain', name: '*.example.org, example.org', link: '/cosmos-ui/config-general/https' }, link: '/cosmos-ui/config-general/https',
    title: s('notif.certificate.renewed.title', 'Certificate renewed'),
    message: s('notif.certificate.renewed.what', 'The certificate for {{domains}} was renewed; it is valid until {{until}}.', { domains: '*.example.org, example.org', until: '13 February 2027' }),
  }),
  item(106, {
    kind: 'container.updated', source: 'containers', level: 'info', read: true, date: at(11 * hour),
    subject: { type: 'container', name: 'jellyfin', link: '/cosmos-ui/servapps/containers/jellyfin' }, link: '/cosmos-ui/servapps/containers/jellyfin',
    title: s('notif.container.updated.title', '{{name}} was updated', { name: 'jellyfin' }),
    message: s('notif.container.updated.what', 'The app {{name}} was updated to a new image ({{image}}) and restarted.', { name: 'jellyfin', image: 'jellyfin/jellyfin:latest' }),
  }),
  item(107, {
    kind: 'container.update.failed', source: 'containers', level: 'error', date: at(10 * hour + 58 * minute),
    subject: { type: 'container', name: 'nextcloud-aio-mastercontainer-with-a-rather-long-name', link: '/cosmos-ui/servapps/containers/nextcloud-aio-mastercontainer-with-a-rather-long-name' },
    link: '/cosmos-ui/servapps/containers/nextcloud-aio-mastercontainer-with-a-rather-long-name',
    title: s('notif.container.updateFailed.title', '{{name}} could not be updated', { name: 'nextcloud-aio-mastercontainer-with-a-rather-long-name' }),
    message: s('notif.container.updateFailed.what', 'The new image of {{name}} was downloaded but the app could not be recreated with it: it runs the previous one.', { name: 'nextcloud-aio-mastercontainer-with-a-rather-long-name' }),
  }),
  item(108, {
    kind: 'container.dying', source: 'containers', level: 'error', count: 3, firstSeen: at(70 * minute), date: at(52 * minute),
    subject: { type: 'container', name: 'radarr', link: '/cosmos-ui/servapps/containers/radarr' }, link: '/cosmos-ui/servapps/containers/radarr',
    title: s('notif.container.dying.title', '{{name}} keeps stopping', { name: 'radarr' }),
    message: s('notif.container.dying.what', 'The app {{name}} stopped with an error {{exits}} times within ten minutes (last exit code {{code}}).', { name: 'radarr', exits: '3', code: '137' }),
  }),
  item(109, {
    kind: 'snapshots.failed', source: 'snapshots', level: 'error', read: true, date: at(20 * hour),
    subject: { type: 'folder', name: '/mnt/data/photos', link: '/cosmos-ui/config-general/general' }, link: '/cosmos-ui/config-general/general',
    title: s('notif.snapshots.failed.title', 'The incremental backup did not go cleanly'),
    message: s('notif.snapshots.failed.what', 'The snapshot of {{source}} could not be written: {{reason}}.', { source: '/mnt/data/photos', reason: 'no space left on /mnt/backup' }),
  }),
  item(110, {
    kind: 'fail2ban.down', source: 'fail2ban', level: 'warning', date: at(4 * hour),
    subject: { type: 'setting', name: 'Fail2ban', link: '/cosmos-ui/security' }, link: '/cosmos-ui/security',
    title: s('notif.fail2ban.down.title', 'Fail2ban is not running'),
    message: s('notif.fail2ban.down.what', 'Fail2ban is installed and set up for Cosmos but its server does not answer: addresses that attack Cosmos are not banned at the firewall.'),
  }),
  item(111, {
    kind: 'telegram.failed', source: 'telegram', level: 'error', read: true, date: at(15 * hour),
    subject: { type: 'chat', name: 'Telegram', link: '/cosmos-ui/config-general/general' }, link: '/cosmos-ui/config-general/general',
    title: s('notif.telegram.failed.title', 'Telegram notifications are not delivered'),
    message: s('notif.telegram.failed.what', 'Telegram refused the messages: the bot was removed from the chat. Messages are counted and summed up once Telegram accepts them again.'),
  }),
  item(112, {
    kind: 'container.leftover', source: 'containers', level: 'warning', read: true, date: at(40 * hour),
    subject: { type: 'container', name: 'sonarr', link: '/cosmos-ui/servapps' }, link: '/cosmos-ui/servapps',
    title: s('notif.container.leftover.title', 'A change of {{name}} was interrupted', { name: 'sonarr' }),
    message: s('notif.container.leftover.what', 'A change of {{name}} was interrupted when Cosmos stopped: the previous container is kept, stopped, as {{kept}}. Nothing was deleted.', { name: 'sonarr', kept: 'sonarr-cosmos-old-ba9876543210' }),
  }),
  item(113, {
    kind: 'alert.disk', source: 'monitoring', level: 'warning', count: 2, firstSeen: at(28 * hour), date: at(3 * hour),
    subject: { type: 'disk', name: '/mnt/backup-of-the-family-photos', link: '/cosmos-ui/storage' }, link: '/cosmos-ui/monitoring',
    title: s('notif.alert.disk.title', 'The disk {{disk}} is nearly full', { disk: '/mnt/backup-of-the-family-photos' }),
    message: s('notif.alert.disk.what', 'The alert "{{alert}}" was triggered: the disk mounted on {{disk}} was {{percent}}% full.', { alert: 'Disk Full Notification (/mnt/backup-of-the-family-photos)', disk: '/mnt/backup-of-the-family-photos', percent: '96' }),
  }),
  // what was written before this version: the owner's morning, as the old rows hold it
  item(114, {
    kind: '', source: 'upnp', level: 'error', read: true, date: at(3 * hour + 12 * minute), legacy: true,
    title: s('header.notification.title.upnpFailed', ''), message: s('header.notification.message.upnpFailed', '', { Vars: ROUTER_ANSWER }),
  }),
  item(115, {
    kind: '', source: 'server', level: 'error', read: true, date: at(50 * hour), legacy: true,
    title: s('header.notification.title.serverError', ''), message: s('', 'Backup: Error reading directory : open /var/lib/cosmos/backups: permission denied'),
  }),
].sort((a, b) => (a.date < b.date ? 1 : -1));

const THREADS = { 101: [102], 102: [101] };
const seenOf = (n) => (n.count > 1 ? Array.from({ length: n.count }, (_, i) => new Date(new Date(n.firstSeen).getTime() + (new Date(n.date) - new Date(n.firstSeen)) * (i / (n.count - 1))).toISOString()) : [n.date]);
const byId = (id) => NOTIFICATION_ITEMS.find((n) => n.id === id);

const EXTRA = {
  101: {
    what: s('notif.upnp.forward.failed.what.absent', 'The router no longer forwards port {{port}} to this server: Cosmos cannot be reached from the internet.', { port: '2053' }),
    means: s('notif.upnp.forward.failed.means.router', 'Visitors from the internet, and you when you are away, cannot open Cosmos or its apps through port {{port}} until the router forwards it again. The router closed the connection instead of answering: this happens while it restarts its UPnP service or when it is overloaded.', { port: '2053' }),
    doing: s('notif.upnp.forward.failed.doing.retry', 'Cosmos tries again by itself at {{at}} (attempt {{n}}), then every {{every}} minutes, and looks every five minutes whether the forward is back.', { at: '08:51', n: '5', every: '15' }),
    steps: [
      step('notif.step.upnp.check', 'Ask the router again now.', {}, '', 'upnp.check', s('notifications.step.checkNow', 'Check now')),
      step('notif.step.upnp.settings', 'See the forwards and the router\'s answers under Configuration > HTTP > Internet access.', {}, '/cosmos-ui/config-general/http-server', '', s('notif.step.upnp.settingsButton', 'Open internet access')),
      step('notif.step.upnp.router', 'Open the router\'s page ({{router}}) and check that UPnP is on and that port {{port}} is forwarded to {{local}}:{{internal}}.', { router: 'http://192.168.1.1', port: '2053', local: '192.168.1.20', internal: '2053' }),
      step('notif.step.upnp.wait', 'If the router was restarted, wait a few minutes: Cosmos asks again by itself.'),
    ],
    raw: ROUTER_ANSWER,
    facts: [
      fact('notif.fact.port', 'Port on the router', '2053/TCP → 192.168.1.20:2053'), fact('notif.fact.router', 'Router', 'FRITZ!Box 7590 (192.168.1.1)'),
      fact('notif.fact.server', 'This server', '192.168.1.20'), fact('notif.fact.attempts', 'Attempts', '4'), fact('notif.fact.nextTry', 'Next try', '08:51'),
    ],
  },
  102: {
    what: s('notif.upnp.forward.restored.what', 'The router forwards port {{port}} to this server again since {{at}}: Cosmos can be reached from the internet. It was not forwarded for {{minutes}} minutes.', { port: '2053', at: '08:51', minutes: '183' }),
    doing: s('notif.upnp.forward.restored.doing', 'Cosmos renews the forward every 20 minutes and looks every five minutes whether it is still there.'),
    facts: [fact('notif.fact.port', 'Port on the router', '2053/TCP → 192.168.1.20:2053'), fact('notif.fact.outage', 'Not forwarded for', '183 minutes')],
  },
  103: {
    what: s('notif.certificate.failed.what', 'Let\'s Encrypt refused the order for {{domain}}: the DNS provider did not accept the record Cosmos tried to write.', { domain: 'photos.example.org' }),
    means: s('notif.certificate.failed.means', 'The current certificate keeps working until {{until}}. After that, browsers warn visitors of {{domain}}.', { until: '27 November 2026', domain: 'photos.example.org' }),
    doing: s('notif.certificate.failed.doing', 'Cosmos tries again by itself in {{hours}} hours.', { hours: '6' }),
    steps: [step('notif.step.certificate.card', 'See the certificate and renew it now under Configuration > Domains.', {}, '/cosmos-ui/config-general/https'),
      step('notif.step.certificate.dns', 'Check at the DNS provider that the API token may still edit the zone of {{domain}}.', { domain: 'example.org' })],
    raw: 'acme: error: 400 :: POST :: https://acme-v02.api.letsencrypt.org/acme/finalize/123456/789 :: urn:ietf:params:acme:error:dns :: DNS problem: NXDOMAIN looking up TXT for _acme-challenge.photos.example.org',
  },
  108: {
    means: s('notif.container.dying.means', 'The app {{name}} is not available while it restarts, and it may lose work each time.', { name: 'radarr' }),
    doing: s('notif.container.dying.doing', 'Docker restarts it by itself (its restart policy). Cosmos does nothing more.'),
    steps: [step('notif.step.container.logs', 'Read the app\'s logs on its page: the last lines before each stop say why.', {}, '/cosmos-ui/servapps/containers/radarr'),
      step('notif.step.container.oom', 'Exit code 137 often means it ran out of memory: give it more or look for a leak.')],
    facts: [fact('notif.fact.exitCode', 'Last exit code', '137'), fact('notif.fact.exits', 'Stops within ten minutes', '3')],
  },
};

const CONTEXT = {
  101: {
    state: { ok: true, sentence: s('notif.state.upnp.forwarded', 'The router forwards port {{port}} to this server right now (since {{since}}).', { port: '2053', since: '08:51' }),
      facts: [fact('notif.fact.router', 'Router', 'FRITZ!Box 7590 (192.168.1.1)'), fact('notif.fact.externalIP', 'Address on the internet', '203.0.113.45')],
      header: [s('notif.state.upnp.col.port', 'Port'), s('notif.state.upnp.col.to', 'Forwarded to'), s('notif.state.upnp.col.what', 'What')],
      rows: [{ cells: ['2053/TCP', '192.168.1.20:2053', 'Cosmos HTTPS'] }, { cells: ['51413/TCP', '192.168.1.20:51413', 'Cosmos app torrent'] }, { cells: ['3074/UDP', '192.168.1.37:3074', 'Xbox (another device)'] }],
      link: '/cosmos-ui/config-general/http-server' },
    security: { available: true, events: [
      { id: 9001, time: at(9 * minute), kind: 'upnp.mapping.created', class: 'audit', severity: 'medium', actor: '', ip: '', target: 'Cosmos HTTPS', outcome: 'ok', count: 1, details: { external: '2053', internal: '2053', protocol: 'tcp' } },
      { id: 8990, time: at(3 * hour + 12 * minute), kind: 'upnp.mapping.removed', class: 'audit', severity: 'medium', actor: '', ip: '', target: 'Cosmos HTTPS', outcome: 'ok', count: 1, details: { external: '2053', protocol: 'tcp', reason: 'lost' } },
    ] },
    events: { available: true, events: [{ id: 7001, label: 'Critical Error', application: 'Cosmos', eventId: 'cosmos.error', date: at(3 * hour + 12 * minute), level: 'error', data: {}, object: '' }] },
    log: { available: true, reason: null, truncated: false, lines: [
      '2026/11/15 05:39:02 [ERROR] UPnP: could not forward port 2053 (HTTPS) : ' + ROUTER_ANSWER,
      '2026/11/15 05:39:02 [WARN]  UPnP: the router could not be set up; trying again by itself at 05:40',
      '2026/11/15 08:51:07 [INFO]  UPnP: port 2053 (HTTPS) is forwarded again',
      '2026/11/15 08:51:07 [INFO]  UPnP: router forwards port 2053 (TCP) to 192.168.1.20:2053 (HTTPS)',
    ] },
  },
};
const defaultContext = (n) => ({
  state: n.source === 'containers'
    ? { ok: n.kind !== 'container.dying', sentence: s('notif.state.container', 'The app {{name}} is {{state}}.', { name: n.subject ? n.subject.name : '', state: n.kind === 'container.dying' ? 'restarting (restarted 7 times)' : 'running' }), facts: [fact('notif.fact.image', 'Image', 'linuxserver/radarr:latest')], link: n.link }
    : n.legacy ? null : { ok: null, sentence: null, facts: [] },
  security: { available: true, events: [] },
  events: { available: true, events: [] },
  log: { available: false, reason: s('notif.log.none', 'No line of the log belongs to it.'), lines: [], truncated: false },
});

const filterList = (url) => {
  const p = url.searchParams;
  let list = NOTIFICATION_ITEMS;
  if (p.get('level')) list = list.filter((n) => p.get('level').split(',').includes(n.level));
  if (p.get('source')) list = list.filter((n) => n.source === p.get('source'));
  if (p.get('read') === 'unread') list = list.filter((n) => !n.read);
  if (p.get('read') === 'read') list = list.filter((n) => n.read);
  if (p.get('q')) list = list.filter((n) => JSON.stringify(n).toLowerCase().includes(p.get('q').toLowerCase()));
  const limit = Number(p.get('limit') || 30);
  return ok({ items: list.slice(0, limit), next: list.length > limit ? '1:1' : '', unread: NOTIFICATION_ITEMS.filter((n) => !n.read).length, sources: [...new Set(NOTIFICATION_ITEMS.map((n) => n.source))] });
};
const details = (url) => {
  const id = Number(url.searchParams.get('id'));
  const n = byId(id) || NOTIFICATION_ITEMS.find((x) => x.ref === url.searchParams.get('ref'));
  if (!n) return { status: 'error', message: 'Not found', code: 'NOT001' };
  return ok({ ...n, what: null, means: null, doing: null, steps: [], raw: '', facts: [], ...(EXTRA[n.id] || {}), seen: seenOf(n), thread: [n, ...(THREADS[n.id] || []).map(byId)] });
};
const context = (url) => {
  const n = byId(Number(url.searchParams.get('id')));
  return n ? ok(CONTEXT[n.id] || defaultContext(n)) : { status: 'error', message: 'Not found' };
};
// the order matters: a later route is asked first
export const notificationPagesMock = [
  ['/cosmos/api/notifications/mark', ok({})],
  ['/cosmos/api/notifications/delete', ok({})],
  ['/cosmos/api/notifications/list', filterList],
  ['/cosmos/api/notifications/item', details],
  ['/cosmos/api/notifications/context', context],
];

// blocks at Cloudflare's edge (src/edgeblock/api.go answers the card and the dashboard with one status):
// switched on for one domain, six rules of every reason, an IPv6 /64, one deleted in Cloudflare's dashboard
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

// Fork addition (plan 40): "Install any image", as the server answers it for
// a server that reaches Docker Hub (src/anyimage). The recipe is a copy of
// recipes.json's PostgreSQL, trimmed; made-up counts.
const PG_RECIPE = {
  id: 'postgres', name: 'PostgreSQL', images: ['postgres'], source: 'https://hub.docker.com/_/postgres', kind: 'database', provides: 'postgres', pin: 'major',
  service: { port: 5432 }, runAs: [{ tagContains: 'alpine', user: '70:70' }, { user: '999:999' }],
  volumes: [{ target: '/var/lib/postgresql', from: 18, evidence: 'declared', backup: true }, { target: '/var/lib/postgresql/data', until: 17, evidence: 'declared', backup: true }],
  env: [{ name: 'POSTGRES_PASSWORD', type: 'secret' }, { name: 'POSTGRES_USER', type: 'name', value: 'app' }, { name: 'POSTGRES_DB', type: 'name', value: 'app' }],
  healthcheck: { test: ['CMD-SHELL', 'pg_isready -U "$POSTGRES_USER" -d "$POSTGRES_DB"'], interval: '10s', timeout: '5s', retries: 5 },
  memory: '256 MB to 1 GB', backup: 'pg_dumpall -U app', notes: ['majorUpgrade', 'pgdata18'],
  client: { url: 'postgres://{user}:{password}@{host}:5432/{database}', user: 'POSTGRES_USER', password: 'POSTGRES_PASSWORD', database: 'POSTGRES_DB' },
};
const describe = (fields) => ({
  registry: 'docker.io', official: false, platforms: ['linux/amd64', 'linux/arm64/v8'], platform: 'linux/amd64', fits: 'yes', tagTyped: false,
  loggedIn: false, databases: [PG_RECIPE], tags: { list: [], suggested: '', why: '', more: false }, ...fields,
});
export const ANY_IMAGE = {
  search: { results: [
    { name: 'postgres', description: 'The PostgreSQL object-relational database system provides reliability and data integrity.', official: true, stars: 15056, pulls: 12455318364, recipe: 'postgres' },
    { name: 'bitnami/postgresql', description: 'Bitnami container image for PostgreSQL', official: false, stars: 2400, pulls: 1000000000 },
    { name: 'cimg/postgres', description: '', official: false, stars: 9, pulls: 869601096 },
  ] },
  postgres: describe({
    reference: 'postgres:18', repository: 'library/postgres', tag: '18', official: true, page: 'https://hub.docker.com/_/postgres', recipe: PG_RECIPE,
    declared: { ports: ['5432/tcp'], volumes: ['/var/lib/postgresql'], env: ['PATH=/usr/bin', 'PG_MAJOR=18', 'PGDATA=/var/lib/postgresql/18/docker'], user: '', entrypoint: ['docker-entrypoint.sh'], cmd: ['postgres'], labels: {}, workingDir: '', stopSignal: 'SIGINT' },
    tags: { list: ['18.7', '18', '17.12', '17', '16', 'latest', 'alpine'], suggested: '18', why: 'pinMajor', more: false },
  }),
  dbapp: describe({
    reference: 'example/dbapp:1', repository: 'example/dbapp', tag: '1', tagTyped: true, page: 'https://hub.docker.com/r/example/dbapp',
    declared: { ports: ['8080/tcp'], volumes: [], env: ['DB_HOST=', 'DB_PORT=5432', 'DB_USER=', 'DB_PASSWORD=', 'DB_NAME=', 'APP_SECRET='], user: '', entrypoint: [], cmd: ['httpd'], labels: {}, workingDir: '/', stopSignal: '' },
    tags: { list: ['1'], suggested: '1', why: 'newest', more: false },
  }),
  nothing: describe({
    reference: 'busybox:1.36', repository: 'library/busybox', tag: '1.36', official: true, page: 'https://hub.docker.com/_/busybox',
    declared: { ports: [], volumes: [], env: ['PATH=/bin'], user: '', entrypoint: [], cmd: ['sh'], labels: {}, workingDir: '', stopSignal: '' },
    tags: { list: ['1.37', '1.36', 'latest'], suggested: '1.37', why: 'newest', more: false },
  }),
  private: { reference: 'private.example.org/team/app:1', registry: 'private.example.org', repository: 'team/app', tag: '1', page: 'https://private.example.org/team/app', problem: 'unauthorized', loggedIn: false, platforms: [], tags: { list: [] } },
};
// the describe answer for the reference the page asks about
export const anyImageMock = [
  ['/cosmos/api/images/search', ok(ANY_IMAGE.search)],
  ['/cosmos/api/images/describe', (url, request) => {
    let reference = '';
    try { reference = (request && request.postDataJSON().reference) || ''; } catch (e) { /* no body */ }
    if (/dbapp/.test(reference)) return ok(ANY_IMAGE.dbapp);
    if (/busybox/.test(reference)) return ok(ANY_IMAGE.nothing);
    if (/private/.test(reference)) return ok(ANY_IMAGE.private);
    return ok(ANY_IMAGE.postgres);
  }],
];

// The protection cards of the security page (GET /cosmos/api/protection, src/protection): nothing installed,
// all installed and active (IPv4 filtered, a home network behind a router), the guard's table not loaded, a
// Cosmos that may not read the firewall, and the guard's other states: fail-closed (no default route), no
// outside interface, a flushed chain, IPv4 not filtered behind a router, SSH allowances with their end, other
// tables that filter (one of them a guard made by hand). The shape is the one src/protection/protection_test.go
// checks; the tables follow src/protection/testdata/nft-guard-v2-*.json.
const protectionPiece = (piece, installed, extra = {}) => ({
  installed, active: false, state: installed ? 'active' : 'notInstalled', stateFile: installed ? 'ok' : 'missing',
  command: `sudo sh /opt/cosmos/fork/${piece}/${installed ? 'uninstall' : 'install'}.sh`, commandKind: installed ? 'uninstall' : 'install',
  ...extra,
});
const emptySets = { publicTcp: [], publicUdp: [], vpnUdp: [], wanInterfaces: [], lanInterfaces: [], failClosedInterfaces: [], sshPorts: [], sshAllowed: [], homeNetworks: [] };
const noTable = { loaded: false, inputHooked: false, forwardHooked: false, inputFilters: false, forwardFilters: false, ipv4Passed: false, ...emptySets };
const liveTable = {
  ...emptySets, loaded: true, inputHooked: true, forwardHooked: true, inputFilters: true, forwardFilters: true, ipv4Passed: false,
  droppedInput: { packets: 48213, bytes: 2892780 }, droppedForward: { packets: 1290, bytes: 77400 }, droppedSpoofed: { packets: 37, bytes: 2220 },
  publicTcp: [2052, 2053], vpnUdp: [41641], wanInterfaces: ['eth0'], lanInterfaces: ['eth0'], sshPorts: [22],
};
const reload = 'sudo systemctl reload cosmos-host-guard';
const PROTECTION = {
  none: {
    time: '2026-11-17T10:00:00Z', cosmosPort: 2053,
    guard: protectionPiece('nftables', false, { table: noTable, publicTcp: [], publicUdp: [], otherFilters: [], portMissing: false, keptOpen: [] }),
    fail2ban: protectionPiece('fail2ban', false, { clientInstalled: false, running: false, configInstalled: false }),
    updates: protectionPiece('unattended-upgrades', false, { binaryInstalled: true, enabled: false, timerEnabled: true, incomplete: false }),
  },
  all: {
    time: '2026-11-17T10:00:00Z', cosmosPort: 2053,
    guard: protectionPiece('nftables', true, {
      active: true, since: '2026-11-17T10:00:00Z', by: 'fork', table: liveTable,
      publicTcp: [2052, 2053], publicUdp: [], ipv4: true, sshOpen: false, ipv4Reason: 'asked-yes', exposed: false, otherFilters: [], portMissing: false, keptOpen: [2052, 2053],
    }),
    fail2ban: protectionPiece('fail2ban', true, { active: true, since: '2026-11-16T09:00:00Z', by: 'fork', clientInstalled: true, running: true, configInstalled: true, sshd: true, cosmosJails: true }),
    updates: protectionPiece('unattended-upgrades', true, { active: true, since: '2026-11-15T08:00:00Z', by: 'fork', binaryInstalled: true, enabled: true, timerEnabled: true, config: '/etc/apt/apt.conf.d/52cosmos-security-upgrades', incomplete: false }),
  },
};
const guardOf = (guard) => ({ ...PROTECTION.all, guard: { ...PROTECTION.all.guard, ...guard } });
const inactive = (reason, table, extra = {}) => guardOf({ active: false, state: 'inactive', reason, table, ...extra });
PROTECTION.unloaded = { ...inactive('tableMissing', noTable, { repairCommand: reload, portMissing: true, keptOpen: [2052, 2053] }), cosmosPort: 8443 };
PROTECTION.unknown = {
  ...PROTECTION.all,
  guard: { ...PROTECTION.all.guard, active: false, state: 'unknown', reason: 'notPermitted', unknown: 'notPermitted', table: undefined },
  updates: { ...PROTECTION.all.updates, by: 'administrator', stateFile: 'missing', since: '2025-03-02T07:41:00Z', config: '/etc/apt/apt.conf.d/20auto-upgrades', command: undefined, commandKind: undefined },
};
PROTECTION.failclosed = guardOf({ ipv4: false, ipv4Reason: 'behind-router', table: { ...liveTable, ipv4Passed: true, wanInterfaces: ['eth0', 'eth1'], lanInterfaces: [], failClosedInterfaces: ['eth0', 'eth1'] } });
PROTECTION.nooutside = inactive('noOutside', { ...liveTable, wanInterfaces: [], lanInterfaces: [] });
PROTECTION.flushed = inactive('chainEmpty', { ...liveTable, inputFilters: false, ipv4Passed: false }, { repairCommand: reload });
PROTECTION.behindrouter = guardOf({
  ipv4: false, ipv4Reason: 'behind-router', publicUdp: [443],
  table: { ...liveTable, ipv4Passed: true, publicTcp: [2053], publicUdp: [443], wanInterfaces: ['eth0', 'wlan0'], lanInterfaces: ['eth0'], homeNetworks: ['192.0.2.0/24', '2001:db8:5::/48'] },
});
PROTECTION.sshallowed = guardOf({
  table: {
    ...liveTable, publicTcp: [2053], lanInterfaces: [],
    sshAllowed: [{ address: '198.51.100.7', expires: 7199, until: '2026-11-17T12:00:00Z' }, { address: '2001:db8:2::20', expires: 86399, until: '2026-11-18T10:00:00Z' }],
    homeNetworks: ['203.0.113.0/24'],
  },
  ipv4Reason: 'public-address',
});
PROTECTION.others = guardOf({ otherFilters: ['inet firewalld', 'ip filter', 'inet cosmos_guard'] });
// the guard's third table (src/protection/testdata/nft-guard-v3-*.json): IPv4 passed per interface, trusted
// uplinks, pending prefixes, the reverse-path counter; what the installer found; its watcher's liveness
const v3Table = { ...liveTable, ipv4PassInterfaces: [], rpfInterfaces: ['eth0'], trustedInterfaces: [], pendingPrefixes: [], droppedRpf: { packets: 12, bytes: 720 } };
const fresh = { watcherAt: '2026-11-17T09:59:55Z', refreshAt: '2026-11-17T09:59:20Z', watcherStale: false, refreshStale: false };
const v3 = (table, extra = {}) => guardOf({ liveness: fresh, vpnFound: [], udpNotOpen: [], trustedInterfaces: [], table: { ...v3Table, ...table }, ...extra });
PROTECTION.trusted = v3({ wanInterfaces: ['eth0', 'tun0'], trustedInterfaces: ['tun0'] }, { active: false, state: 'inactive', reason: 'trustedUplink', trustedInterfaces: ['tun0'] });
PROTECTION.pass4 = v3({ wanInterfaces: ['eth0', 'wlan0'], lanInterfaces: ['eth0'], ipv4PassInterfaces: ['eth0'], rpfInterfaces: ['wlan0'], publicTcp: [2053] }, { ipv4: false, ipv4Reason: 'behind-router' });
PROTECTION.pending = v3({ pendingPrefixes: ['2001:db8:5::/64'] });
PROTECTION.cloud = v3({ lanInterfaces: [], publicTcp: [2053] }, { cloud: 'Amazon EC2', exposed: true, ipv4Reason: 'exposed', sshMode: 'console' });
PROTECTION.vpnunknown = v3({}, { vpnFound: [{ kind: 'tailscale', port: 41641 }, { kind: 'wireguard', interface: 'wg0' }], udpNotOpen: [5353, 51820] });
PROTECTION.stale = v3({}, { liveness: { watcherAt: '2026-11-17T09:20:00Z', refreshAt: '2026-11-17T09:21:00Z', watcherStale: true, refreshStale: true } });
export const protectionMock = (state) => [['/cosmos/api/protection', ok(PROTECTION[state])]];

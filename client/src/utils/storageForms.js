// Fork addition: what the storage dialogs (mount, unmount, merge, parity)
// propose and explain, from GET /api/storage/candidates and POST
// /api/storage/unmount-check (src/storage/fork_candidates.go,
// fork_unmount.go). Plain functions: see storageForms.test.mjs. Texts are
// ['key', 'English', variables]. The server refuses the same things.

const GB = 1000 * 1000 * 1000;

export const sizeText = (bytes) => {
  const n = Number(bytes) || 0;
  if (n >= 1000 * GB) return (n / (1000 * GB)).toFixed(n >= 10000 * GB ? 0 : 1) + ' TB';
  if (n >= GB) return (n / GB).toFixed(n >= 10 * GB ? 0 : 1) + ' GB';
  if (n >= 1000 * 1000) return Math.round(n / (1000 * 1000)) + ' MB';
  return n + ' B';
};

const USE_TEXTS = {
  mounted: ['mgmt.storage.fork.useMounted', 'Mounted at {{detail}} already'],
  swap: ['mgmt.storage.fork.useSwap', 'In use as swap (the system\'s spare memory)'],
  raid: ['mgmt.storage.fork.useRaid', 'Part of the RAID array {{detail}}'],
  lvm: ['mgmt.storage.fork.useLvm', 'Used by the volume {{detail}} (LVM or encryption)'],
  holder: ['mgmt.storage.fork.useHolder', 'Used by {{detail}}'],
  nofs: ['mgmt.storage.fork.useNofs', 'No file system that can be mounted ({{detail}}): format it first (Disks tab)'],
  'has-partitions': ['mgmt.storage.fork.useParts', 'A whole disk with partitions: mount one of its partitions'],
};

export const useText = (use) => {
  if (!use || !use.code) return null;
  const text = USE_TEXTS[use.code] || ['mgmt.storage.fork.useOther', 'Not available ({{detail}})'];
  return [text[0], text[1], { detail: use.detail || use.code }];
};

// The devices that can be mounted, and the others with the reason.
export const mountOffers = (data) => {
  const devices = (data && data.devices) || [];
  const offered = [];
  const notOffered = [];
  devices.forEach((d) => {
    if (d.mountable) offered.push(d);
    else if (d.type !== 'disk' || (d.use && d.use.code !== 'has-partitions')) notOffered.push({ ...d, reason: useText(d.use) || ['mgmt.storage.fork.useOther', 'Not available ({{detail}})', { detail: '?' }] });
  });
  return { offered, notOffered };
};

const slug = (text) => String(text || '').trim().replace(/[^A-Za-z0-9._-]+/g, '-').replace(/^[-.]+|[-.]+$/g, '').slice(0, 48);

// The folder proposed for a device: /mnt/<label>, else /mnt/<device name>,
// free among the folders in use.
export const proposeMountPath = (device, taken = [], base = '/mnt') => {
  if (!device) return '';
  const name = slug(device.label) || slug(String(device.device || '').replace(/^\/dev\//, '').replace(/\//g, '-')) || 'disk';
  const used = new Set(taken);
  let path = `${base}/${name}`;
  for (let i = 2; used.has(path) && i < 100; i += 1) path = `${base}/${name}-${i}`;
  return path;
};

export const mountPathProblem = (path) => {
  const value = String(path || '');
  if (!value) return ['mgmt.storage.fork.pathMissing', 'Enter the folder to mount on, for example /mnt/data'];
  if (!/^\/(mnt|var\/mnt)\/[^\s#\\]+$/.test(value) || value.includes('/../') || value.endsWith('/..') || /\/\/|\/$/.test(value)) {
    return ['mgmt.storage.fork.pathRule', 'A folder below /mnt without spaces, for example /mnt/data'];
  }
  return null;
};

// The owner of the mounted folder, as one choice.
export const OWNER_APPS = '1000:1000';
export const ownerChoice = (value) => (value === OWNER_APPS ? 'apps' : (value === '' ? 'root' : 'other'));
export const ownerProblem = (value) => (value === '' || /^[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}(:[A-Za-z0-9_][A-Za-z0-9_.-]{0,63})?$/.test(value)
  ? null : ['mgmt.storage.fork.ownerRule', 'A user name or number, optionally :group, for example 1000:1000']);

export const fstabSentence = (permanent, device) => (permanent
  ? (device && device.uuid
    ? ['mgmt.storage.fork.fstabUuid', 'Written to /etc/fstab by its file system\'s UUID ({{uuid}}) with "nofail": it is mounted at every start, and if the disk is missing then, the server starts without it.', { uuid: device.uuid }]
    : ['mgmt.storage.fork.fstabNoUuid', 'Written to /etc/fstab with "nofail": it is mounted at every start, and if the disk is missing then, the server starts without it.'])
  : ['mgmt.storage.fork.fstabNot', 'Mounted until the next restart of the server only.']);

// --- unmount ---------------------------------------------------------------

export const usersText = (check) => {
  const users = (check && check.users) || [];
  if (users.length === 0) return null;
  const names = [...new Set(users.map((u) => u.name).filter(Boolean))].slice(0, 6).join(', ');
  return ['mgmt.storage.fork.inUse', 'In use right now by {{names}}: unmounting it takes the folder away from them, and apps that keep writing there write to the system disk instead.', { names: names || '?' }];
};

// The server asks the folder to be typed when the mount is in use or the
// unmount also removes it from /etc/fstab.
export const unmountNeedsConfirm = (check, permanent) => Boolean(permanent || (check && check.users && check.users.length > 0));

// --- merge (MergerFS) --------------------------------------------------------

export const POLICIES = [
  { value: 'epmfs', label: ['mgmt.storage.fork.policyEpmfs', 'Keep folders together'], hint: ['mgmt.storage.fork.policyEpmfsHint', 'A new file goes to a disk that already holds its folder, the one with most free space: a series or an album stays on one disk.'] },
  { value: 'mfs', label: ['mgmt.storage.fork.policyMfs', 'Most free space'], hint: ['mgmt.storage.fork.policyMfsHint', 'Each new file goes to the disk with the most free space: the disks fill evenly, a folder\'s files may be spread.'] },
  { value: 'lfs', label: ['mgmt.storage.fork.policyLfs', 'Fill one disk first'], hint: ['mgmt.storage.fork.policyLfsHint', 'New files go to the fullest disk that still has room: one disk fills before the next is used, the others can sleep.'] },
  { value: 'ff', label: ['mgmt.storage.fork.policyFf', 'In the order listed'], hint: ['mgmt.storage.fork.policyFfHint', 'New files go to the first disk in the list that has room.'] },
];

// The policy held in the options text, and the options with another policy.
export const policyOf = (opts) => {
  const m = String(opts || '').match(/(?:^|,)category\.create=([a-z]+)/);
  return m ? m[1] : '';
};
export const withPolicy = (opts, policy) => {
  const rest = String(opts || '').split(',').map((o) => o.trim()).filter((o) => o && !o.startsWith('category.create='));
  return [policy ? `category.create=${policy}` : '', ...rest].filter(Boolean).join(',');
};

// The folders a merge can be made of: data disks, not the system disk, not a
// merge, not a network share.
export const mergeBranches = (data) => {
  const mounts = (data && data.mounts) || [];
  const offered = [];
  const left = [];
  mounts.forEach((m) => {
    if (m.kind === 'data') offered.push(m);
    else left.push({ ...m, reason: {
      system: ['mgmt.storage.fork.notSystem', 'The system disk'],
      merge: ['mgmt.storage.fork.notMerge', 'A merged folder itself'],
      network: ['mgmt.storage.fork.notNetwork', 'A network share: a merge of network shares stalls when one is unreachable'],
    }[m.kind] || ['mgmt.storage.fork.notOther', 'Not a data disk'] });
  });
  return { offered, left };
};

export const freePath = (base, taken = []) => {
  const used = new Set(taken);
  let path = base;
  for (let i = 2; used.has(path) && i < 100; i += 1) path = `${base}-${i}`;
  return path;
};

// --- parity (SnapRAID) -------------------------------------------------------

// For each data-disk mount: whether it can be the parity of the data chosen,
// and why not. SnapRAID: a parity disk must be at least as large as the
// largest data disk, and on a disk of its own.
export const parityCandidates = (data, dataPaths = [], parityPaths = []) => {
  const mounts = ((data && data.mounts) || []).filter((m) => m.kind === 'data');
  const chosen = mounts.filter((m) => dataPaths.includes(m.path));
  const largest = chosen.reduce((max, m) => Math.max(max, Number(m.size) || 0), 0);
  const dataDisks = new Set(chosen.map((m) => m.disk || m.device));
  return mounts.map((m) => {
    let reason = null;
    if (dataPaths.includes(m.path)) reason = ['mgmt.storage.fork.parityIsData', 'Chosen as a data disk'];
    else if (dataDisks.has(m.disk || m.device)) reason = ['mgmt.storage.fork.paritySameDisk', 'On the same physical disk as a data disk: a failure of that disk would take both'];
    else if (largest && (Number(m.size) || 0) < largest) reason = ['mgmt.storage.fork.paritySmaller', 'Smaller ({{size}}) than the largest data disk ({{largest}})', { size: sizeText(m.size), largest: sizeText(largest) }];
    else if (parityPaths.some((p) => p !== m.path && mounts.find((o) => o.path === p && (o.disk || o.device) === (m.disk || m.device)))) reason = ['mgmt.storage.fork.paritySameParity', 'On the same disk as another parity disk'];
    return { ...m, ok: !reason, reason };
  });
};

// The parity disk proposed: the smallest one that is large enough (the
// larger disks stay free for data); null when none fits.
export const proposeParity = (data, dataPaths = []) => {
  const fit = parityCandidates(data, dataPaths, []).filter((m) => m.ok);
  if (!dataPaths.length || fit.length === 0) return null;
  return fit.sort((a, b) => (Number(a.size) || 0) - (Number(b.size) || 0))[0];
};

// Data disks offered: the data mounts, each on a disk of its own.
export const dataCandidates = (data, dataPaths = [], parityPaths = []) => {
  const mounts = ((data && data.mounts) || []).filter((m) => m.kind === 'data');
  return mounts.map((m) => {
    const sameAs = mounts.find((o) => o.path !== m.path && dataPaths.includes(o.path) && (o.disk || o.device) === (m.disk || m.device));
    let reason = null;
    if (parityPaths.includes(m.path)) reason = ['mgmt.storage.fork.dataIsParity', 'Chosen as the parity disk'];
    else if (sameAs && !dataPaths.includes(m.path)) reason = ['mgmt.storage.fork.dataSameDisk', 'On the same physical disk as {{path}}', { path: sameAs.path }];
    return { ...m, ok: !reason, reason };
  });
};

export const parityNameProblem = (name) => (/^[A-Za-z0-9][A-Za-z0-9 _-]{2,31}$/.test(String(name || ''))
  ? null : ['mgmt.storage.fork.parityName', 'A name of 3 to 32 letters, digits, spaces, dashes or underscores, for example Storage Parity']);

// Fork addition: what a disk's card on the Storage page shows (pages/storage/disks.jsx), as plain
// functions over one entry of /api/disks (lsblk's columns plus usage and SMART). See
// diskCard.test.mjs.

// the columns the card shows elsewhere (title, secondary line, size and bar), or never
const SHOWN_ELSEWHERE = new Set(['name', 'children', 'smart', 'fstype', 'mountpoint', 'model', 'usage', 'rota', 'type', 'size']);

// file systems that are not mounted as such: they hold something else (LVM, encryption, RAID) or are swap
const NOT_MOUNTABLE = new Set(['swap', 'linux_raid_member', 'LVM2_member', 'crypto_LUKS', 'zfs_member', 'bcache', 'ddf_raid_member', 'isw_raid_member']);

const text = (value) => {
  if (value === null || value === undefined) return '';
  if (typeof value === 'object') return JSON.stringify(value);
  return String(value).trim();
};

// The lines of the details list: [key, value], in the server's order, empty values left out
// (lsblk pads the vendor with spaces: "ATA     ").
export function diskDetails(disk) {
  if (!disk || typeof disk !== 'object') return [];
  return Object.keys(disk)
    .filter((key) => !SHOWN_ELSEWHERE.has(key))
    .map((key) => [key, text(disk[key])])
    .filter(([, value]) => value !== '');
}

// The name without /dev/ (and without /dev/mapper/ for a logical volume or an opened LUKS device).
export function diskName(disk) {
  return String((disk && disk.name) || '').replace(/^\/dev\/(mapper\/)?/, '');
}

// The parts of the line under the name: kind, file system, model, where it is mounted.
export function diskFacts(disk) {
  if (!disk) return [];
  const facts = [disk.rota ? 'HDD' : 'SSD'];
  if (disk.fstype) facts.push(disk.fstype);
  if (disk.model && text(disk.model)) facts.push(text(disk.model));
  if (disk.mountpoint) facts.push(disk.mountpoint === '[SWAP]' ? 'swap space' : disk.mountpoint);
  return facts;
}

// Used space in percent (0 when unknown).
export function diskPercent(disk) {
  if (!disk || !disk.usage || !disk.size) return 0;
  return Math.max(0, Math.min(100, Math.round((disk.usage / disk.size) * 100)));
}

// Which commands the card offers, as upstream decided them, except that a partition that holds
// LVM, an encrypted volume or another RAID member is not offered "Mount" (upstream: swap and
// linux_raid_member only).
export function diskActions(disk) {
  if (!disk) return { format: false, mount: false, unmount: false, smart: false };
  const isDisk = disk.type === 'disk';
  const empty = !disk.children || !disk.children.length;
  const mountedHere = !!disk.mountpoint && disk.mountpoint !== '[SWAP]';
  return {
    format: isDisk || disk.type === 'part',
    unmount: mountedHere,
    mount: (disk.type === 'part' || (isDisk && empty)) && !!disk.fstype && !NOT_MOUNTABLE.has(disk.fstype) && !disk.mountpoint,
    smart: isDisk,
  };
}

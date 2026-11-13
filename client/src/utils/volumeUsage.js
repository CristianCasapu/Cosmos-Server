// Fork addition: what the Volumes page says about a Docker volume beyond its
// name: which apps use it and where its data really is. Built from what the
// UI already loads (the volume list and the container list); Docker gives a
// volume's size only through a slow call, so there is none here. Plain
// functions, no React: see volumeUsage.test.mjs.

const nameOf = (container) => {
  const names = (container && container.Names) || [];
  return String(names[0] || (container && container.Name) || '').replace(/^\//, '');
};

// The containers that mount the volume, running or not:
// [{ name, target, readOnly, running }], sorted by name. null when the
// container list is not known (still loading, or refused).
export const volumeUsers = (volume, containers) => {
  if (!Array.isArray(containers)) return null;
  const wanted = volume && volume.Name;
  const users = [];

  containers.forEach((container) => {
    const name = nameOf(container);
    if (!name || !wanted) return;
    ((container && container.Mounts) || []).forEach((mount) => {
      if (!mount || mount.Type !== 'volume' || mount.Name !== wanted) return;
      users.push({
        name,
        target: mount.Destination || '',
        readOnly: mount.RW === false,
        running: container.State === 'running' || !!(container.State && container.State.Running),
      });
    });
  });
  return users.sort((a, b) => a.name.localeCompare(b.name) || a.target.localeCompare(b.target));
};

const NETWORK_TYPES = ['nfs', 'nfs4', 'cifs', 'smb3', 'smbfs', 'glusterfs', 'ceph'];

const option = (options, key) => {
  const found = Object.keys(options).find((name) => name.toLowerCase() === key);
  return found ? String(options[found] || '') : '';
};

// Where the data of a volume is:
//   { kind: 'docker', path }  in Docker's own folder (the usual case)
//   { kind: 'folder', path }  a folder of the server the volume points to
//   { kind: 'share', path, type }  a network share (nfs, cifs...)
//   { kind: 'device', path, type }  a disk or partition mounted for it
//   { kind: 'memory' }        in memory only
//   { kind: 'other', path, driver }  kept by another volume driver
export const volumeLocation = (volume) => {
  const mountpoint = (volume && volume.Mountpoint) || '';
  const driver = (volume && volume.Driver) || 'local';
  if (driver !== 'local') return { kind: 'other', path: mountpoint, driver };

  const options = (volume && volume.Options) || {};
  const type = option(options, 'type').toLowerCase();
  const device = option(options, 'device');
  const flags = option(options, 'o').split(',').map((flag) => flag.trim());

  if (type === 'tmpfs') return { kind: 'memory' };
  if (device && device.startsWith('/') && !device.startsWith('//') && (type === '' || type === 'none' || flags.includes('bind'))) {
    return { kind: 'folder', path: device };
  }
  if (device && type && type !== 'none') {
    // NFS names its server in the options ("addr=192.168.1.5") and the export in the device (":/export")
    const address = flags.map((flag) => /^addr=(.+)$/.exec(flag)).filter(Boolean).map((match) => match[1])[0];
    const path = (address && device.startsWith(':')) ? address + device : device;
    return { kind: NETWORK_TYPES.includes(type) ? 'share' : 'device', path, type };
  }
  return { kind: 'docker', path: mountpoint };
};

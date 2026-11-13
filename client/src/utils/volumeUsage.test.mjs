// Fork addition: tests of volumeUsage.js. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { volumeUsers, volumeLocation } from './volumeUsage.js';

// what API.docker.list() returns, cut down
const containers = [
  { Names: ['/jellyfin'], State: 'running', Mounts: [
    { Type: 'volume', Name: 'jellyfin-config', Source: '/var/lib/docker/volumes/jellyfin-config/_data', Destination: '/config', RW: true },
    { Type: 'volume', Name: 'media', Source: '/var/lib/docker/volumes/media/_data', Destination: '/media', RW: false },
    { Type: 'bind', Source: '/storage/films', Destination: '/films', RW: true },
  ] },
  { Names: ['/backup'], State: 'exited', Mounts: [
    { Type: 'volume', Name: 'media', Destination: '/data', RW: true },
  ] },
  { Names: ['/plain'], State: 'running', Mounts: [] },
  { Names: ['/nomounts'], State: 'running' },
];

test('volumeUsers names the apps that mount the volume, stopped ones too', () => {
  assert.deepEqual(volumeUsers({ Name: 'jellyfin-config' }, containers), [
    { name: 'jellyfin', target: '/config', readOnly: false, running: true },
  ]);
  assert.deepEqual(volumeUsers({ Name: 'media' }, containers), [
    { name: 'backup', target: '/data', readOnly: false, running: false },
    { name: 'jellyfin', target: '/media', readOnly: true, running: true },
  ]);
});

test('volumeUsers: an unused volume has no users, an unknown container list is not "unused"', () => {
  assert.deepEqual(volumeUsers({ Name: 'orphan' }, containers), []);
  assert.deepEqual(volumeUsers({ Name: 'orphan' }, []), []);
  assert.equal(volumeUsers({ Name: 'orphan' }, null), null);
  assert.equal(volumeUsers({ Name: 'orphan' }, undefined), null);
  // a bind mount of a folder with the volume's name is not the volume
  assert.deepEqual(volumeUsers({ Name: '/storage/films' }, containers), []);
  assert.deepEqual(volumeUsers(null, containers), []);
  assert.deepEqual(volumeUsers({ Name: 'x' }, [null, {}, { Names: ['/a'], Mounts: [null] }]), []);
});

test('volumeUsers reads the state of an inspected container too', () => {
  const inspected = [{ Name: '/db', State: { Running: true }, Mounts: [{ Type: 'volume', Name: 'data', Destination: '/var/lib/db' }] }];
  assert.deepEqual(volumeUsers({ Name: 'data' }, inspected), [{ name: 'db', target: '/var/lib/db', readOnly: false, running: true }]);
});

test('volumeLocation: a plain volume lies where Docker says', () => {
  assert.deepEqual(
    volumeLocation({ Name: 'a', Driver: 'local', Mountpoint: '/storage/docker/volumes/a/_data', Options: null }),
    { kind: 'docker', path: '/storage/docker/volumes/a/_data' });
  assert.deepEqual(volumeLocation({ Name: 'a', Mountpoint: '/var/lib/docker/volumes/a/_data', Options: {} }).kind, 'docker');
  assert.deepEqual(volumeLocation(null), { kind: 'docker', path: '' });
});

test('volumeLocation: a volume that points to a folder, a share, or memory', () => {
  const local = (Options) => volumeLocation({ Name: 'a', Driver: 'local', Mountpoint: '/var/lib/docker/volumes/a/_data', Options });
  assert.deepEqual(local({ type: 'none', o: 'bind', device: '/storage/photos' }), { kind: 'folder', path: '/storage/photos' });
  assert.deepEqual(local({ o: 'rw,bind', device: '/archive/x', type: 'ext4' }), { kind: 'folder', path: '/archive/x' });
  assert.deepEqual(local({ type: 'nfs', o: 'addr=192.168.1.5,rw,nfsvers=4', device: ':/export/media' }), { kind: 'share', path: '192.168.1.5:/export/media', type: 'nfs' });
  assert.deepEqual(local({ type: 'cifs', o: 'username=x,password=y', device: '//nas/share' }), { kind: 'share', path: '//nas/share', type: 'cifs' });
  assert.deepEqual(local({ type: 'ext4', device: '/dev/sdb1' }), { kind: 'device', path: '/dev/sdb1', type: 'ext4' });
  assert.deepEqual(local({ type: 'tmpfs', device: 'tmpfs', o: 'size=100m' }), { kind: 'memory' });
  assert.deepEqual(local({ Type: 'NFS', O: 'addr=nas', Device: ':/x' }), { kind: 'share', path: 'nas:/x', type: 'nfs' });
});

test('volumeLocation: another driver keeps the data itself', () => {
  assert.deepEqual(volumeLocation({ Name: 'a', Driver: 'rclone', Mountpoint: '' }), { kind: 'other', path: '', driver: 'rclone' });
});

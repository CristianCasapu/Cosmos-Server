import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  sizeText, useText, mountOffers, proposeMountPath, mountPathProblem, ownerChoice, ownerProblem, fstabSentence,
  usersText, unmountNeedsConfirm, policyOf, withPolicy, mergeBranches, freePath, parityCandidates, proposeParity,
  dataCandidates, parityNameProblem, POLICIES,
} from './storageForms.js';

const translations = JSON.parse(readFileSync(new URL('./locales/en/translation.json', import.meta.url), 'utf8'));
const TB = 1e12;

const data = {
  devices: [
    { device: '/dev/sdb1', disk: '/dev/sdb', type: 'part', fstype: 'ext4', label: 'Media Disk', uuid: 'aaaa-1', size: 4 * TB, mountable: true, use: { code: '' } },
    { device: '/dev/sdc1', disk: '/dev/sdc', type: 'part', fstype: 'ext4', label: '', size: 2 * TB, mountable: false, use: { code: 'mounted', detail: '/mnt/sdc1' } },
    { device: '/dev/sdd', disk: '/dev/sdd', type: 'disk', size: 8 * TB, mountable: false, use: { code: 'has-partitions' } },
    { device: '/dev/sde1', disk: '/dev/sde', type: 'part', fstype: 'linux_raid_member', size: TB, mountable: false, use: { code: 'nofs', detail: 'linux_raid_member' } },
  ],
  mounts: [
    { path: '/', device: '/dev/nvme0n1p2', disk: '/dev/nvme0n1', size: TB / 2, free: TB / 4, kind: 'system' },
    { path: '/mnt/d1', device: '/dev/sdf1', disk: '/dev/sdf', size: 4 * TB, free: 2 * TB, kind: 'data' },
    { path: '/mnt/d2', device: '/dev/sdg1', disk: '/dev/sdg', size: 6 * TB, free: 5 * TB, kind: 'data' },
    { path: '/mnt/d2b', device: '/dev/sdg2', disk: '/dev/sdg', size: 1 * TB, free: TB, kind: 'data' },
    { path: '/mnt/p1', device: '/dev/sdh1', disk: '/dev/sdh', size: 8 * TB, free: 8 * TB, kind: 'data' },
    { path: '/mnt/p2', device: '/dev/sdi1', disk: '/dev/sdi', size: 6 * TB, free: 6 * TB, kind: 'data' },
    { path: '/mnt/storage', device: 'd1:d2', size: 10 * TB, kind: 'merge' },
    { path: '/mnt/nas', device: 'nas:/x', kind: 'network' },
  ],
  base: '/mnt',
};

test('sizes in words', () => {
  assert.equal(sizeText(4 * TB), '4.0 TB');
  assert.equal(sizeText(500e9), '500 GB');
  assert.equal(sizeText(64e9), '64 GB');
});

test('mount: what is offered, the others with the reason, the folder from the label', () => {
  const { offered, notOffered } = mountOffers(data);
  assert.deepEqual(offered.map((d) => d.device), ['/dev/sdb1']);
  // a whole disk with partitions is not listed: its partitions are
  assert.deepEqual(notOffered.map((d) => d.device), ['/dev/sdc1', '/dev/sde1']);
  assert.equal(notOffered[0].reason[2].detail, '/mnt/sdc1');
  assert.equal(proposeMountPath(offered[0], []), '/mnt/Media-Disk');
  assert.equal(proposeMountPath(offered[0], ['/mnt/Media-Disk']), '/mnt/Media-Disk-2');
  assert.equal(proposeMountPath({ device: '/dev/nvme1n1p1' }, []), '/mnt/nvme1n1p1');
  assert.equal(mountPathProblem('/mnt/data'), null);
  for (const bad of ['', '/data', '/mnt/', '/mnt/a b', '/mnt/../etc', '/mnt//x']) assert.ok(mountPathProblem(bad), bad);
  assert.equal(useText({ code: '' }), null);
});

test('owner and fstab in words', () => {
  assert.equal(ownerChoice('1000:1000'), 'apps');
  assert.equal(ownerChoice(''), 'root');
  assert.equal(ownerChoice('33:33'), 'other');
  assert.equal(ownerProblem('www-data:www-data'), null);
  assert.ok(ownerProblem('a b'));
  assert.equal(fstabSentence(true, { uuid: 'aaaa' })[2].uuid, 'aaaa');
  assert.equal(fstabSentence(false)[0], 'mgmt.storage.fork.fstabNot');
});

test('unmount: who uses it, and when the folder must be typed', () => {
  assert.equal(usersText({ users: [] }), null);
  assert.equal(usersText({ users: [{ name: 'jellyfin' }, { name: 'jellyfin' }, { name: 'smbd' }] })[2].names, 'jellyfin, smbd');
  assert.equal(unmountNeedsConfirm({ users: [] }, false), false);
  assert.equal(unmountNeedsConfirm({ users: [{ name: 'x' }] }, false), true);
  assert.equal(unmountNeedsConfirm({ users: [] }, true), true);
});

test('merge: data disks only, a free folder, the policy in the options', () => {
  const { offered, left } = mergeBranches(data);
  assert.deepEqual(offered.map((m) => m.path), ['/mnt/d1', '/mnt/d2', '/mnt/d2b', '/mnt/p1', '/mnt/p2']);
  assert.deepEqual(left.map((m) => m.path), ['/', '/mnt/storage', '/mnt/nas']);
  assert.equal(freePath('/mnt/storage', ['/mnt/storage']), '/mnt/storage-2');
  assert.equal(policyOf('minfreespace=20G,category.create=lfs'), 'lfs');
  assert.equal(withPolicy('minfreespace=20G,category.create=lfs', 'epmfs'), 'category.create=epmfs,minfreespace=20G');
  assert.equal(withPolicy('', 'mfs'), 'category.create=mfs');
  assert.equal(POLICIES.length, 4);
});

test('parity: at least as large as the largest data disk, on a disk of its own', () => {
  const c = parityCandidates(data, ['/mnt/d1', '/mnt/d2']);
  const by = Object.fromEntries(c.map((m) => [m.path, m]));
  assert.equal(by['/mnt/p1'].ok, true);
  assert.equal(by['/mnt/p2'].ok, true);
  assert.equal(by['/mnt/d1'].reason[0], 'mgmt.storage.fork.parityIsData');
  assert.equal(by['/mnt/d2b'].reason[0], 'mgmt.storage.fork.paritySameDisk');
  // the smallest that is large enough is proposed
  assert.equal(proposeParity(data, ['/mnt/d1', '/mnt/d2']).path, '/mnt/p2');
  assert.equal(proposeParity(data, ['/mnt/p1', '/mnt/d1']), null);
  assert.equal(parityCandidates(data, ['/mnt/p1']).find((m) => m.path === '/mnt/p2').reason[0], 'mgmt.storage.fork.paritySmaller');
  const d = Object.fromEntries(dataCandidates(data, ['/mnt/d2'], ['/mnt/p1']).map((m) => [m.path, m]));
  assert.equal(d['/mnt/d2b'].reason[0], 'mgmt.storage.fork.dataSameDisk');
  assert.equal(d['/mnt/p1'].reason[0], 'mgmt.storage.fork.dataIsParity');
  assert.equal(parityNameProblem('Storage Parity'), null);
  assert.ok(parityNameProblem('ab'));
  assert.ok(parityNameProblem('a/b/c'));
});

test('every text of the module is in the translation file', () => {
  const source = readFileSync(new URL('./storageForms.js', import.meta.url), 'utf8');
  const pairs = [...source.matchAll(/\['(mgmt\.storage\.fork\.[^']+)',\s*'((?:[^'\\]|\\.)*)'/g)];
  assert.ok(pairs.length > 30);
  for (const [, key, text] of pairs) assert.equal(translations[key], text.replace(/\\(.)/g, '$1'), key);
});

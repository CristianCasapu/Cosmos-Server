// Fork addition: tests of snapshotExcludes.js. Run with: node --test "client/src/utils/*.test.mjs"
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { hasPattern, addPattern, removePattern, togglePattern, bigEntries, destinationProposals } from './snapshotExcludes.js';

test('a tick adds its own line and removes that line only', () => {
  const typed = '*.iso\ndownloads/incomplete';
  const ticked = addPattern(typed, 'cache');
  assert.equal(ticked, '*.iso\ndownloads/incomplete\ncache');
  assert.equal(addPattern(ticked, 'cache'), ticked, 'once only');
  assert.equal(addPattern('', 'docker/*'), 'docker/*');
  assert.equal(removePattern(ticked, 'cache'), typed);
  assert.equal(removePattern('cache/\n*.iso', 'cache'), '*.iso', 'the plain form counts');
  assert.equal(togglePattern(typed, '*.iso', false), 'downloads/incomplete');
  assert.equal(togglePattern(typed, '*.tmp', true), typed + '\n*.tmp');
});

test('a tick shows ticked when the pattern was typed', () => {
  assert.ok(hasPattern('  cache  \n*.iso', 'cache'));
  assert.ok(hasPattern('thumbnails/', 'thumbnails'));
  assert.ok(!hasPattern('caches', 'cache'));
  assert.ok(!hasPattern('', 'cache'));
});

test('big entries: folders with a pattern that no offer covers', () => {
  const answer = {
    offers: [{ pattern: 'docker/*' }],
    top: [
      { name: 'docker', dir: true, size: 900, pattern: 'docker/*' },
      { name: 'media', dir: true, size: 800, pattern: 'media/*' },
      { name: 'big.iso', dir: false, size: 700, pattern: 'big.iso' },
      { name: 'link', dir: false, link: true, size: 0 },
      { name: 'empty', dir: true, size: 0, pattern: 'empty/*' },
    ],
  };
  assert.deepEqual(bigEntries(answer).map((e) => e.name), ['media']);
  assert.deepEqual(bigEntries(null), []);
});

test('the destination is proposed on a data disk, not the system disk, not inside a source', () => {
  const places = [
    { path: '/storage/apps', kind: 'data-path', free: 5e11, total: 1e12, osDisk: false, disk: '/storage' },
    { path: '/storage', kind: 'disk', free: 5e11, total: 1e12, osDisk: false, disk: '/storage' },
    { path: '/mnt/backup', kind: 'disk', free: 2e11, total: 4e11, osDisk: false, disk: '/mnt/backup' },
    { path: '/var/lib/docker', kind: 'docker-root', free: 9e11, total: 1e12, osDisk: true, disk: '/' },
    { path: '/', kind: 'system', free: 9e11, total: 1e12, osDisk: true, disk: '/' },
  ];
  const out = destinationProposals(places, ['/storage/apps']);
  assert.deepEqual(out.map((p) => p.path), ['/mnt/backup/snapshots', '/storage/snapshots'], 'another disk than the sources first');
  assert.equal(out[0].free, 2e11);
  // a folder backed up that holds the disk: nothing inside it is proposed
  assert.deepEqual(destinationProposals(places, ['/mnt/backup']).map((p) => p.path), ['/storage/snapshots']);
  const onlySystem = destinationProposals([{ path: '/srv/apps', kind: 'data-path', free: 1, osDisk: true, disk: '/' }], []);
  assert.deepEqual(onlySystem.map((p) => [p.path, p.osDisk]), [['/snapshots', true]]);
  assert.deepEqual(destinationProposals(undefined, undefined), []);
});

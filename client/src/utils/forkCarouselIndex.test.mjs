// Fork addition: tests of components/forkCarouselIndex.mjs (the Market's slide shows).
// node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { nextIndex } from '../components/forkCarouselIndex.mjs';

test('moves forward and wraps to the first slide', () => {
  assert.equal(nextIndex(0, 1, 3), 1);
  assert.equal(nextIndex(2, 1, 3), 0);
});

test('moves back and wraps to the last slide', () => {
  assert.equal(nextIndex(0, -1, 3), 2);
  assert.equal(nextIndex(1, -1, 3), 0);
});

test('a single slide stays where it is; no slide gives 0', () => {
  assert.equal(nextIndex(0, 1, 1), 0);
  assert.equal(nextIndex(0, -1, 1), 0);
  assert.equal(nextIndex(4, 1, 0), 0);
});

test('an index left over from a longer list still lands inside', () => {
  assert.equal(nextIndex(7, 1, 3), 2);
});

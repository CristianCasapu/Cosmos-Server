// Fork addition: tests of notices.js. Run: node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { sortNotices, worstSeverity, foldNotices, hiddenSummary } from './notices.js';

const line = (id, severity) => ({ id, severity });

test('the most urgent first, the given order kept within a severity', () => {
  const items = [line('a', 'info'), line('b', 'warning'), line('c', 'error'), line('d', 'warning'), line('e', 'unknown'), line('f', 'success')];
  assert.deepEqual(sortNotices(items).map((i) => i.id), ['c', 'b', 'd', 'e', 'a', 'f']);
});

test('an unknown severity counts as information; lines without an id are dropped', () => {
  assert.deepEqual(sortNotices([line('x', 'odd'), line('y', 'warning'), null, { severity: 'error' }]).map((i) => i.id), ['y', 'x']);
  assert.deepEqual(sortNotices(null), []);
});

test('the worst severity tints the area', () => {
  assert.equal(worstSeverity([line('a', 'info'), line('b', 'warning')]), 'warning');
  assert.equal(worstSeverity([line('a', 'odd')]), 'info');
  assert.equal(worstSeverity([]), '');
});

test('folded after two lines, but never to hide a single line', () => {
  const three = [line('a', 'info'), line('b', 'error'), line('c', 'warning')];
  assert.deepEqual(foldNotices(three, false), { shown: [three[1], three[2], three[0]], hidden: [], foldable: false });
  const four = [...three, line('d', 'error')];
  const folded = foldNotices(four, false);
  assert.deepEqual(folded.shown.map((i) => i.id), ['b', 'd']);
  assert.deepEqual(folded.hidden.map((i) => i.id), ['c', 'a']);
  assert.equal(folded.foldable, true);
  const open = foldNotices(four, true);
  assert.equal(open.shown.length, 4);
  assert.equal(open.hidden.length, 0);
  assert.equal(open.foldable, true);
  assert.equal(foldNotices(four, false, 3).hidden.length, 0);
  assert.equal(foldNotices(four, false, 0).shown.length, 1, 'at least one line is shown');
});

test('the hidden lines are counted per severity', () => {
  assert.deepEqual(hiddenSummary([line('a', 'info'), line('b', 'warning'), line('c', 'info')]), [{ severity: 'warning', count: 1 }, { severity: 'info', count: 2 }]);
  assert.deepEqual(hiddenSummary([]), []);
});

test('a pinned line comes first and is never folded away', () => {
  const items = [line('a', 'warning'), line('b', 'warning'), line('c', 'warning'), line('d', 'info'), { id: 'done', severity: 'success', pinned: true }];
  const folded = foldNotices(items, false);
  assert.deepEqual(folded.shown.map((i) => i.id), ['done', 'a', 'b']);
  assert.deepEqual(folded.hidden.map((i) => i.id), ['c', 'd']);
  assert.deepEqual(foldNotices(items, true).shown.map((i) => i.id), ['done', 'a', 'b', 'c', 'd']);
});

// Fork addition: tests of tableColumns.js. Run: node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { tableColumns } from './tableColumns.js';

const up = (from) => { const order = ['xs', 'sm', 'md', 'lg', 'xl', 'xxl']; const i = order.indexOf(from); return Object.fromEntries(order.map((b, j) => [b, j <= i])); };
const down = (width) => { const order = ['xs', 'sm', 'md', 'lg', 'xl', 'xxl']; const i = order.indexOf(width); return Object.fromEntries(order.map((b, j) => [b, j > i])); };
// a phone is "xs": up(xs) only, below every breakpoint from sm
const phone = { screenMin: up('xs'), screenMax: down('xs') };
const tablet = { screenMin: up('sm'), screenMax: down('sm') };
const desktop = { screenMin: up('lg'), screenMax: down('lg') };

// the users table of the panel
const users = [
  { title: 'User' }, { title: 'Status', screenMin: 'sm' }, { title: 'Role', screenMin: 'sm' }, { title: 'Email', screenMin: 'md' },
  { title: 'Created', screenMin: 'lg' }, { title: 'Last login', screenMin: 'lg' }, { title: '' },
];

test('on a phone the columns the table hides come back into the cards', () => {
  const { shown, cards } = tableColumns(users, phone);
  assert.equal(cards, true);
  assert.deepEqual(users.filter(shown).map((c) => c.title), ['User', 'Status', 'Role', 'Email', 'Created', 'Last login', '']);
});

test('a tablet and a desktop keep hiding what does not fit a row', () => {
  const t = tableColumns(users, tablet);
  assert.equal(t.cards, false);
  assert.deepEqual(users.filter(t.shown).map((c) => c.title), ['User', 'Status', 'Role', '']);
  const d = tableColumns(users, desktop);
  assert.deepEqual(users.filter(d.shown).map((c) => c.title), ['User', 'Status', 'Role', 'Email', 'Created', 'Last login', '']);
});

test('phone: false stays out; a phone-only column replaces it', () => {
  const parity = [{ title: 'Name' }, { title: 'Disks' }, { title: 'Status', screenMax: 'md' }, { title: '', screenMin: 'md', phone: false }, { title: 'Schedule', screenMin: 'sm' }];
  const { shown, cards } = tableColumns(parity, phone);
  assert.equal(cards, true);
  assert.deepEqual(parity.filter(shown).map((c) => c.title), ['Name', 'Disks', 'Status', 'Schedule']);
  // on the desktop the full text, not the phone's icon
  assert.deepEqual(parity.filter(tableColumns(parity, desktop).shown).map((c) => c.title), ['Name', 'Disks', '', 'Schedule']);
});

test('two columns stay a table row; no cards when the table asks for none; before the size is known, no cards', () => {
  const two = [{ title: 'Name' }, { title: '' }];
  assert.equal(tableColumns(two, phone).cards, false);
  assert.equal(tableColumns(users, { ...phone, phoneCards: false }).cards, false);
  assert.deepEqual(users.filter(tableColumns(users, { ...phone, phoneCards: false }).shown).map((c) => c.title), ['User', '']);
  // the first render: no media query has answered yet
  assert.equal(tableColumns(users, { screenMin: {}, screenMax: {} }).cards, false);
  assert.equal(tableColumns(null, phone).cards, false);
});

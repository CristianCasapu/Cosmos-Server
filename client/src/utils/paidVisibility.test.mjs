// Fork addition: tests of paidVisibility.js. Run with:
//   node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  UNKNOWN, stateFromStatus, hidePaid, showPurchase, offerMode, showPaidPart,
  filterMenu, filterTabs, PURCHASE_URL, PAID_MENU_URLS, PAID_ROUTE_PREFIXES,
} from './paidVisibility.js';

const here = dirname(fileURLToPath(import.meta.url));
const src = join(here, '..');

// licence: valid / absent / unknown; switch: on (hide) / off / not in the answer
const licensedOn = stateFromStatus({ Licence: true });
const licensedOff = stateFromStatus({ Licence: true, ShowPaidFeatures: true });
const freeOn = stateFromStatus({ Licence: false });
const freeOff = stateFromStatus({ Licence: false, ShowPaidFeatures: true });

test('truth table', () => {
  const rows = [
    // state,       hidePaid, purchase, offer
    [licensedOn,  false, false, 'show'],
    [licensedOff, false, false, 'show'],
    [freeOn,      true,  true,  'note'],
    [freeOff,     false, true,  'show'],
    [UNKNOWN,     false, false, 'none'],
    [stateFromStatus(null), false, false, 'none'],
    [stateFromStatus(undefined), false, false, 'none'],
  ];
  for (const [state, hide, purchase, offer] of rows) {
    assert.equal(hidePaid(state), hide, JSON.stringify(state));
    assert.equal(showPurchase(state), purchase, JSON.stringify(state));
    assert.equal(offerMode(state), offer, JSON.stringify(state));
  }
});

test('the switch is on when the setting is absent (fork default)', () => {
  assert.equal(freeOn.hideSwitch, true);
  assert.equal(stateFromStatus({ Licence: false, ShowPaidFeatures: false }).hideSwitch, true);
  assert.equal(freeOff.hideSwitch, false);
});

test('a licence that is not exactly true is no licence', () => {
  assert.equal(hidePaid(stateFromStatus({ Licence: 'yes' })), true);
  assert.equal(hidePaid(stateFromStatus({})), true);
});

test('paid fields: hidden only when hidePaid, kept when on', () => {
  assert.equal(showPaidPart(freeOn, false), false);
  assert.equal(showPaidPart(freeOn, true), true);
  assert.equal(showPaidPart(freeOff, false), true);
  assert.equal(showPaidPart(licensedOn, false), true);
  assert.equal(showPaidPart(UNKNOWN, false), true);
});

const menu = [
  { id: 'a', type: 'group', children: [{ id: 'home' }, { id: 'backups', paid: true }] },
  { id: 'b', type: 'group', children: [{ id: 'deploy', paid: true }] },
  { id: 'support', type: 'group', children: [{ id: 'doc' }, { id: 'buy', forkPurchase: true }] },
];
const ids = (groups) => groups.map((g) => `${g.id}:${g.children.map((c) => c.id).join(',')}`);

test('menu: hidden, purchase entry, empty groups dropped', () => {
  assert.deepEqual(ids(filterMenu(menu, freeOn)), ['a:home', 'support:doc,buy']);
  assert.deepEqual(ids(filterMenu(menu, freeOff)), ['a:home,backups', 'b:deploy', 'support:doc,buy']);
  assert.deepEqual(ids(filterMenu(menu, licensedOn)), ['a:home,backups', 'b:deploy', 'support:doc']);
  assert.deepEqual(ids(filterMenu(menu, UNKNOWN)), ['a:home,backups', 'b:deploy', 'support:doc']);
});

test('menu: groups without a change are the same objects', () => {
  const out = filterMenu(menu, licensedOn);
  assert.equal(out[0], menu[0]);
});

test('tabs', () => {
  const tabs = [{ title: 'Disks' }, { title: 'External', paid: true }, { title: 'Parity' }];
  assert.deepEqual(filterTabs(tabs, freeOn).map((t) => t.title), ['Disks', 'Parity']);
  assert.equal(filterTabs(tabs, freeOff), tabs);
  assert.equal(filterTabs(tabs, licensedOn), tabs);
  assert.equal(filterTabs(tabs, UNKNOWN), tabs);
});

test('purchase URL is one of the original project checkouts offered in the app', () => {
  const free = readFileSync(join(src, 'utils', 'free.jsx'), 'utf8');
  assert.ok(free.includes(`"${PURCHASE_URL}"`));
});

// --- the definitions themselves: every paid entry goes through the helper ---

// Object literals `{ ... }` that contain `url:`, at the depth of a menu item.
function menuItems(text) {
  const items = [];
  const stack = [];
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '{') stack.push(i);
    else if (text[i] === '}') {
      const start = stack.pop();
      const body = text.slice(start, i + 1);
      const url = body.match(/\burl:\s*'([^']+)'/);
      if (url && /\bid:\s*'/.test(body) && !items.some((it) => body.includes(it.body))) {
        items.push({ url: url[1], body });
      }
    }
  }
  return items;
}

test('every menu item that leads to a paid page is marked paid', () => {
  const dir = join(src, 'menu-items');
  const found = new Set();
  for (const file of readdirSync(dir).filter((f) => f.endsWith('.jsx'))) {
    for (const item of menuItems(readFileSync(join(dir, file), 'utf8'))) {
      if (PAID_MENU_URLS.includes(item.url)) {
        found.add(item.url);
        assert.match(item.body, /\bpaid:\s*true\b/, `${file}: ${item.url} must carry paid: true`);
      } else {
        assert.doesNotMatch(item.body, /\bpaid:\s*true\b/, `${file}: ${item.url} is marked paid but not in PAID_MENU_URLS`);
      }
    }
  }
  assert.deepEqual([...found].sort(), [...PAID_MENU_URLS].sort());
});

test('the navigation filters the menu through the helper', () => {
  const nav = readFileSync(join(src, 'layout/MainLayout/Drawer/DrawerContent/Navigation/index.jsx'), 'utf8');
  assert.match(nav, /useForkMenu\(menuItem\.items\)/);
  const support = readFileSync(join(src, 'menu-items/support.jsx'), 'utf8');
  assert.match(support, /forkPurchase:\s*true/);
});

test('every paid route is rendered through ProRoute or PaidRoute', () => {
  const routes = readFileSync(join(src, 'routes/MainRoutes.jsx'), 'utf8');
  const re = /path:\s*'([^']+)',\s*(?:\/\/[^\n]*\s*)*element:\s*(<\w+)/g;
  let m;
  let paid = 0;
  while ((m = re.exec(routes))) {
    const [, path, element] = m;
    if (PAID_ROUTE_PREFIXES.some((p) => path === p || path.startsWith(p + '/'))) {
      paid++;
      assert.ok(['<ProRoute', '<PaidRoute'].includes(element), `${path} renders ${element}`);
    }
  }
  assert.ok(paid >= PAID_ROUTE_PREFIXES.length, `found ${paid} paid routes`);
});

test('the sales page is only exported through the offer gate', () => {
  const free = readFileSync(join(src, 'utils', 'free.jsx'), 'utf8');
  assert.match(free, /export default withPaidOfferGate\(PremiumSalesPage\)/);
});

// Fork addition: tests of wayfinding.js. Run: node --test "client/src/utils/*.test.mjs"
import test from 'node:test';
import assert from 'node:assert/strict';
import { cleanPath, menuEntries, menuEntryFor, pageSubject, crumbsFor, tabTitleFor } from './wayfinding.js';

// the shape of client/src/menu-items
const menu = {
  items: [
    { id: 'group-dashboard', type: 'group', children: [
      { id: 'home', type: 'item', url: '/cosmos-ui/', title: 'menu-items.navigation.home', breadcrumbs: false },
      { id: 'dashboard', type: 'item', url: '/cosmos-ui/monitoring', title: 'menu-items.navigation.monitoringTitle', breadcrumbs: false },
      { id: 'market', type: 'item', url: '/cosmos-ui/market-listing', title: 'menu-items.navigation.marketTitle', breadcrumbs: false },
    ] },
    { id: 'management', type: 'group', children: [
      { id: 'servapps', type: 'item', url: '/cosmos-ui/servapps', title: 'menu-items.management.servApps' },
      { id: 'url', type: 'item', url: '/cosmos-ui/config-url', title: 'menu-items.management.urls' },
      { id: 'users', type: 'item', url: '/cosmos-ui/config-users', title: 'menu-items.management.usersTitle' },
      { id: 'config', type: 'item', url: '/cosmos-ui/config-general', title: 'menu-items.management.configurationTitle' },
    ] },
    { id: 'group-security', type: 'group', children: [
      { id: 'security', type: 'item', url: '/cosmos-ui/security', title: 'menu-items.security.dashboard', breadcrumbs: false },
      { id: 'security-audit', type: 'item', url: '/cosmos-ui/security-audit', title: 'menu-items.management.securityAudit' },
    ] },
    { id: 'support', type: 'group', children: [
      { id: 'docs', type: 'item', url: 'https://example.org/doc', title: 'Documentation', external: true, target: true },
      { id: 'broken' },
    ] },
  ],
};
const entries = menuEntries(menu);
const words = {
  'menu-items.navigation.home': 'Home', 'menu-items.navigation.monitoringTitle': 'Monitoring',
  'menu-items.management.servApps': 'ServApps', 'menu-items.management.urls': 'URLs',
  'menu-items.management.configurationTitle': 'Configuration', 'menu-items.management.securityAudit': 'Audit',
  'menu-items.security.dashboard': 'Security', 'navigation.market.startServAppButton': 'Start ServApp',
  'auth.login': 'Login', 'auth.pwdReset.pwdResetTitle': 'Password Reset',
};
const t = (key, text) => words[key] || text;

test('cleanPath: one leading slash, none at the end', () => {
  assert.equal(cleanPath('/cosmos-ui/'), '/cosmos-ui');
  assert.equal(cleanPath('cosmos-ui//servapps/'), '/cosmos-ui/servapps');
  assert.equal(cleanPath(''), '/');
  assert.equal(cleanPath(undefined), '/');
});

test('menuEntries: only the entries that lead to a page of the panel', () => {
  assert.deepEqual(entries.map((e) => e.id), ['home', 'dashboard', 'market', 'servapps', 'url', 'users', 'config', 'security', 'security-audit']);
  assert.equal(entries[0].url, '/cosmos-ui');
  assert.equal(entries[0].crumbs, false);
  assert.equal(entries[3].crumbs, true);
  assert.deepEqual(menuEntries(null), []);
});

test('menuEntryFor: the page itself, or the longest entry above it', () => {
  const id = (path) => (menuEntryFor(path, entries) || {}).id;
  assert.equal(id('/cosmos-ui/'), 'home');
  assert.equal(id('/cosmos-ui'), 'home');
  assert.equal(id('/'), 'home');
  // the ids of these entries are not parts of their addresses: upstream's check by id never found them
  assert.equal(id('/cosmos-ui/monitoring'), 'dashboard');
  assert.equal(id('/cosmos-ui/config-general/http-server'), 'config');
  assert.equal(id('/cosmos-ui/config-url/Jellyfin'), 'url');
  assert.equal(id('/cosmos-ui/market-listing/cosmos-cloud/Jellyfin'), 'market');
  assert.equal(id('/cosmos-ui/servapps/containers/jellyfin'), 'servapps');
  // a longer name is another page, not a page below
  assert.equal(id('/cosmos-ui/security'), 'security');
  assert.equal(id('/cosmos-ui/security-audit'), 'security-audit');
  assert.equal(id('/cosmos-ui/config-users/'), 'users');
  // the home page is above nothing
  assert.equal(id('/cosmos-ui/nothing-here'), undefined);
  assert.equal(id('/cosmos-ui/login'), undefined);
  assert.equal(menuEntryFor('/cosmos-ui/servapps', null), null);
});

test('pageSubject: the container, URL or app the page is about; never a tab', () => {
  const subject = (path) => pageSubject(path, menuEntryFor(path, entries));
  assert.deepEqual(subject('/cosmos-ui/servapps/containers/jellyfin'), { text: 'jellyfin' });
  assert.deepEqual(subject('/cosmos-ui/servapps/stack/immich'), { text: 'immich' });
  assert.deepEqual(subject('/cosmos-ui/config-url/My%20App'), { text: 'My App' });
  assert.deepEqual(subject('/cosmos-ui/market-listing/cosmos-cloud/Jellyfin'), { text: 'Jellyfin' });
  assert.deepEqual(subject('/cosmos-ui/servapps/new-service'), { key: 'navigation.market.startServAppButton', text: 'Start ServApp' });
  assert.equal(subject('/cosmos-ui/servapps'), null);
  assert.equal(subject('/cosmos-ui/config-general/http-server'), null);
  assert.equal(subject('/cosmos-ui/config-url/shield'), null);
  assert.equal(subject('/cosmos-ui/nothing-here'), null);
  // a name that is not valid percent-encoding is shown as it is
  assert.deepEqual(subject('/cosmos-ui/config-url/100%'), { text: '100%' });
});

test('crumbsFor: Home, the entry (a link when the page is below it), the subject', () => {
  assert.deepEqual(crumbsFor('/cosmos-ui/servapps', entries), [
    { key: 'menu-items.navigation.home', text: 'Home', to: '/cosmos-ui/' },
    { key: 'menu-items.management.servApps', text: 'menu-items.management.servApps', to: null },
  ]);
  const below = crumbsFor('/cosmos-ui/servapps/containers/jellyfin', entries);
  assert.equal(below.length, 3);
  assert.equal(below[1].to, '/cosmos-ui/servapps');
  assert.deepEqual(below[2], { text: 'jellyfin', to: null });
  // pages whose menu entry asks for no crumbs, and pages outside the menu
  assert.deepEqual(crumbsFor('/cosmos-ui/', entries), []);
  assert.deepEqual(crumbsFor('/cosmos-ui/monitoring', entries), []);
  assert.deepEqual(crumbsFor('/cosmos-ui/nothing-here', entries), []);
});

test('tabTitleFor: subject, menu entry, product', () => {
  assert.equal(tabTitleFor('/cosmos-ui/', entries, t), 'Home · Cosmos');
  assert.equal(tabTitleFor('/cosmos-ui/servapps', entries, t), 'ServApps · Cosmos');
  assert.equal(tabTitleFor('/cosmos-ui/servapps/containers/jellyfin', entries, t), 'jellyfin · ServApps · Cosmos');
  assert.equal(tabTitleFor('/cosmos-ui/servapps/new-service', entries, t), 'Start ServApp · ServApps · Cosmos');
  assert.equal(tabTitleFor('/cosmos-ui/config-general/https', entries, t), 'Configuration · Cosmos');
  assert.equal(tabTitleFor('/cosmos-ui/security-audit', entries, t), 'Audit · Cosmos');
  assert.equal(tabTitleFor('/cosmos-ui/login', entries, t), 'Login · Cosmos');
  assert.equal(tabTitleFor('/cosmos-ui/forgot-password', entries, t), 'Password Reset · Cosmos');
  assert.equal(tabTitleFor('/cosmos-ui/nothing-here', entries, t), 'Cosmos');
  // without a translator the English texts are used
  assert.equal(tabTitleFor('/cosmos-ui/login', entries), 'Login · Cosmos');
});

test('tabTitleFor: a name from the address stays one short line', () => {
  const long = '/cosmos-ui/config-url/' + encodeURIComponent('a'.repeat(200) + '\n second line');
  const title = tabTitleFor(long, entries, t);
  assert.ok(!title.includes('\n'));
  assert.ok(title.length <= 80 + ' · URLs · Cosmos'.length);
});

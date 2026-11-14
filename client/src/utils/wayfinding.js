// Fork addition: "where am I". From the address of the page: which entry of
// the side menu is the current one, the crumbs above the page, and what the
// browser tab says. Pure functions, so they can be tested without a browser:
// the menu comes in as a plain list of { id, url, title } (see menuEntries).

const HOME = '/cosmos-ui';

// one leading slash, none at the end, no doubled ones
export const cleanPath = (path) => {
  const cleaned = ('/' + String(path || '')).replace(/\/+/g, '/').replace(/\/$/, '');
  return cleaned || '/';
};

const decode = (part) => {
  try {
    return decodeURIComponent(part);
  } catch (e) {
    return part;
  }
};

// The entries of the side menu that lead to a page of the panel, flattened.
export const menuEntries = (menu) => {
  const entries = [];
  for (const group of (menu && menu.items) || []) {
    for (const item of group.children || []) {
      if (item && item.type === 'item' && typeof item.url === 'string' && item.url.startsWith('/') && !item.external) {
        entries.push({ id: item.id, url: cleanPath(item.url), title: item.title, crumbs: item.breadcrumbs !== false });
      }
    }
  }
  return entries;
};

// The menu entry a page belongs to: the one whose address is the page's own
// or the longest one above it. "/cosmos-ui/security-audit" is not below
// "/cosmos-ui/security", and the home page is above nothing.
export const menuEntryFor = (pathname, entries) => {
  const path = cleanPath(pathname);
  let best = null;
  for (const entry of entries || []) {
    const url = cleanPath(entry.url);
    const matches = url === HOME ? (path === HOME || path === '/') : (path === url || path.startsWith(url + '/'));
    if (matches && (!best || url.length > cleanPath(best.url).length)) best = entry;
  }
  return best;
};

// Parts of an address that name a tab of the page or are plumbing, not the
// thing the page is about. Tabs change the address without telling the
// router, so a crumb made from one would go stale.
const NOT_A_NAME = new Set([
  'containers', 'stack', 'view', 'repo',
  'general', 'http-server', 'https', 'appearance', 'external', 'api',
  'shield', 'whitelist', 'users', 'groups',
  'disks', 'mounts', 'shares', 'mergerfs', 'parity', 'raid',
  'restore', 'snapshots', 'events', 'dns',
]);

// Pages below a menu entry that have a name of their own in the translations.
const NAMED_PAGES = {
  'new-service': ['navigation.market.startServAppButton', 'Start ServApp'],
};

// What the page below a menu entry is about: a container, a URL, an app of
// the market. null on the entry's own page and on its tabs.
export const pageSubject = (pathname, entry) => {
  if (!entry) return null;
  const rest = cleanPath(pathname).slice(cleanPath(entry.url).length).split('/').filter(Boolean);
  for (let i = rest.length - 1; i >= 0; i--) {
    const part = decode(rest[i]);
    if (NAMED_PAGES[part]) return { key: NAMED_PAGES[part][0], text: NAMED_PAGES[part][1] };
    if (!NOT_A_NAME.has(part)) return { text: part };
  }
  return null;
};

// The crumbs above the page: Home, the menu entry (a link when the page is
// below it), then what the page is about. Empty where the entry asks for none.
export const crumbsFor = (pathname, entries) => {
  const entry = menuEntryFor(pathname, entries);
  if (!entry || entry.crumbs === false) return [];
  const subject = pageSubject(pathname, entry);
  const crumbs = [
    { key: 'menu-items.navigation.home', text: 'Home', to: HOME + '/' },
    { key: entry.title, text: entry.title, to: subject ? entry.url : null },
  ];
  if (subject) crumbs.push({ ...subject, to: null });
  return crumbs;
};

// The pages outside the menu: signing in and what goes with it.
const OTHER_PAGES = {
  '/cosmos-ui/login': ['auth.login', 'Login'],
  '/cosmos-ui/loginmfa': ['auth.login', 'Login'],
  '/cosmos-ui/newmfa': ['auth.login', 'Login'],
  '/cosmos-ui/logout': ['global.logout', 'Logout'],
  '/cosmos-ui/register': ['auth.invite.submitButton.register', 'Register'],
  '/cosmos-ui/forgot-password': ['auth.pwdReset.pwdResetTitle', 'Password Reset'],
};

// The text of the browser tab: what the page is about, the menu entry, the
// product. `t` translates a key (with the English text as its default).
export const tabTitleFor = (pathname, entries, t, brand = 'Cosmos') => {
  const translate = typeof t === 'function' ? t : (key, text) => text;
  const parts = [];
  const other = OTHER_PAGES[cleanPath(pathname)];
  if (other) parts.push(translate(other[0], other[1]));
  else {
    const entry = menuEntryFor(pathname, entries);
    const subject = pageSubject(pathname, entry);
    if (subject) parts.push(subject.key ? translate(subject.key, subject.text) : subject.text);
    if (entry) parts.push(translate(entry.title, entry.title));
  }
  parts.push(brand);
  // a name is free text: keep the tab's title one short line
  return parts.map((part) => String(part).replace(/\s+/g, ' ').trim().slice(0, 80)).filter(Boolean).join(' · ');
};

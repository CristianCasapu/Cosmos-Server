// Fork addition: which features that need a licence the panel shows.
//
// Presentation only. The server decides what works (src/pro, src/constellation,
// src/backups, src/storage/rclone.go, src/utils/cloud.go are not touched); this
// module only decides what the menu, the pages and the forms show, from the
// status answer: `Licence` (valid licence or not) and `ShowPaidFeatures` (the
// fork's setting, stored inverted so that a configuration without it hides).
//
// While the status is not known yet everything behaves as if licensed, so a
// licensed user never loses a menu entry, a tab or a field on load; offers are
// the exception: they render nothing until the answer is known, so neither an
// offer nor the neutral note flashes. Plain functions, no React: see
// paidVisibility.test.mjs.

// The checkout the original project's own offer page preselects (yearly plan,
// client/src/utils/free.jsx). The one pointer to buying left in the menu.
export const PURCHASE_URL = 'https://buy.stripe.com/cN2bMN9KLbkV59e9AE';

// Menu entries that lead to a page that needs a licence (the inventory in the
// plan). Every menu item with one of these URLs must carry `paid: true`.
export const PAID_MENU_URLS = [
  '/cosmos-ui/constellation',
  '/cosmos-ui/backups',
  '/cosmos-ui/deployments',
  '/cosmos-ui/databases',
  '/cosmos-ui/object-storage',
  '/cosmos-ui/registries',
  '/cosmos-ui/functions',
  '/cosmos-ui/ci',
];

// Routes that are entirely paid. Each must be rendered through ProRoute (whose
// offer is gated) or PaidRoute. Constellation is not here: its page keeps the
// free "join with a constellation file" action.
export const PAID_ROUTE_PREFIXES = [
  '/cosmos-ui/backups',
  '/cosmos-ui/deployments',
  '/cosmos-ui/databases',
  '/cosmos-ui/object-storage',
  '/cosmos-ui/registries',
  '/cosmos-ui/functions',
  '/cosmos-ui/ci',
];

export const UNKNOWN = Object.freeze({ licence: null, hideSwitch: null });

// The state from a status answer's data (or nothing: unknown).
export function stateFromStatus(data) {
  if (!data || typeof data !== 'object') return UNKNOWN;
  return {
    licence: data.Licence === true,
    hideSwitch: data.ShowPaidFeatures !== true,
  };
}

const known = (state) => !!state && state.licence !== null && state.licence !== undefined;

// True when paid entries, tabs, fields and pages are taken away.
export function hidePaid(state) {
  return known(state) && state.licence === false && state.hideSwitch === true;
}

// The "Purchase premium" menu entry: only once it is known there is no licence,
// whatever the switch says.
export function showPurchase(state) {
  return known(state) && state.licence === false;
}

// How an offer (the sales page) renders: 'show' as upstream, 'note' a short
// neutral note, 'none' nothing (state not known yet).
export function offerMode(state) {
  if (!known(state)) return 'none';
  return hidePaid(state) ? 'note' : 'show';
}

// A field or section marked paid: hidden when hidePaid, unless `keep` (a value
// that is on stays visible so it can be turned off).
export function showPaidPart(state, keep) {
  return !!keep || !hidePaid(state);
}

// The menu groups with paid items and the purchase entry resolved.
export function filterMenu(groups, state) {
  const hide = hidePaid(state);
  const purchase = showPurchase(state);
  return (groups || [])
    .map((group) => {
      const children = (group.children || []).filter((item) => {
        if (item.forkPurchase) return purchase;
        if (item.paid) return !hide;
        return true;
      });
      if (children.length === (group.children || []).length) return group;
      // a group left without entries goes as a whole
      return children.length ? { ...group, children } : null;
    })
    .filter(Boolean);
}

// Tabs marked paid removed when hidePaid; the same array otherwise.
export function filterTabs(tabs, state) {
  if (!Array.isArray(tabs) || !hidePaid(state) || !tabs.some((tab) => tab && tab.paid)) return tabs;
  return tabs.filter((tab) => !(tab && tab.paid));
}

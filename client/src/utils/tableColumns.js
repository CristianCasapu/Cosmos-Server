// Fork addition: which columns PrettyTableView (components/tableView/prettyTableView.jsx) draws, and
// whether its rows become cards (the skin's ct-table-cards, phones only). See tableColumns.test.mjs.
//
// A column may say `screenMin: 'md'` (only from that width up) or `screenMax: 'md'` (only below it).
// In a table row on a phone there is no room for the first kind; in a card there is. So on a phone a
// column hidden by `screenMin` comes back into the card, unless it says `phone: false` (a wide chart, or
// a full text that a phone-only `screenMax` column already sums up). Rows become cards when that leaves
// more than two columns: one or two fit a phone's row as they are.
//
// `screenMin` / `screenMax`: { xs, sm, md, lg, xl, xxl } -> whether the window is at least / below that
// breakpoint (MUI's up() / down()). The phone is "below sm".
export function tableColumns(columns, { screenMin = {}, screenMax = {}, phoneCards = true } = {}) {
  const list = (columns || []).filter(Boolean);
  const inTable = (c) => (!c.screenMin || !!screenMin[c.screenMin]) && (!c.screenMax || !!screenMax[c.screenMax]);
  const phone = !!screenMax.sm;
  const inCard = (c) => inTable(c) || (!!c.screenMin && c.phone !== false && (!c.screenMax || !!screenMax[c.screenMax]));
  const cards = !!phoneCards && phone && list.filter(inCard).length > 2;
  const shown = (c) => !!c && (cards ? inCard(c) : inTable(c));
  return { shown, cards };
}

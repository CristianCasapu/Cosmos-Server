// Fork addition: the order and the folding of the panel's notice areas
// (components/noticeArea.jsx): the things that need the admin's attention on a
// page, one line each, in one surface. Pure, tested by notices.test.mjs.

// The most urgent first. "unknown" is a check that could not be made: less
// than a warning (nothing is known to be wrong), more than information.
export const SEVERITIES = ['error', 'warning', 'unknown', 'info', 'success'];
const rank = (severity) => {
  const index = SEVERITIES.indexOf(severity);
  return index < 0 ? SEVERITIES.indexOf('info') : index;
};

// A stable sort by severity: lines of the same severity keep the order they were given in.
export const sortNotices = (items) => (Array.isArray(items) ? items : [])
  .filter((item) => item && item.id !== undefined && item.id !== null)
  .map((item, index) => ({ item, index }))
  .sort((a, b) => rank(a.item.severity) - rank(b.item.severity) || a.index - b.index)
  .map(({ item }) => item);

// The most urgent severity among the lines ('' for none): it tints the area's edge.
export const worstSeverity = (items) => {
  const sorted = sortNotices(items);
  return sorted.length ? (SEVERITIES.includes(sorted[0].severity) ? sorted[0].severity : 'info') : '';
};

// Folded, the area shows the first `after` lines and says how many more there
// are; it folds only when that hides at least two lines (a "Show 1 more"
// button takes the room of the line it hides).
export const DEFAULT_AFTER = 2;
export const foldNotices = (items, open, after = DEFAULT_AFTER) => {
  const sorted = sortNotices(items);
  const limit = Math.max(1, Math.floor(Number.isFinite(Number(after)) ? Number(after) : DEFAULT_AFTER));
  if (open || sorted.length <= limit + 1) return { shown: sorted, hidden: [], foldable: sorted.length > limit + 1 };
  return { shown: sorted.slice(0, limit), hidden: sorted.slice(limit), foldable: true };
};

// What the hidden lines are, for the "Show N more" button: the count per severity, the most urgent first.
export const hiddenSummary = (hidden) => {
  const counts = {};
  for (const item of sortNotices(hidden)) counts[item.severity] = (counts[item.severity] || 0) + 1;
  return SEVERITIES.filter((severity) => counts[severity]).map((severity) => ({ severity, count: counts[severity] }));
};

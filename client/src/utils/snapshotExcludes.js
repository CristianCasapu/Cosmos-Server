// Fork addition (plan 47): the logic behind the ticks and the destination
// proposal of the incremental backup card (pages/config/snapshots.jsx).
// The exclusions stay one text field, one pattern per line (the door): a tick
// adds its own line or removes that line only, and shows ticked whenever the
// pattern is in the text, typed or ticked. Pure and tested
// (snapshotExcludes.test.mjs).

// A line in its plain form, as the server stores it (no trailing slash).
const plain = (line) => String(line || '').trim().replace(/\/+$/, '');

export const hasPattern = (text, pattern) => String(text || '').split(/\r?\n/).some((line) => plain(line) === plain(pattern));

export const addPattern = (text, pattern) => {
  if (!plain(pattern) || hasPattern(text, pattern)) return String(text || '');
  const body = String(text || '').replace(/\s+$/, '');
  return body ? body + '\n' + plain(pattern) : plain(pattern);
};

export const removePattern = (text, pattern) => String(text || '').split(/\r?\n/)
  .filter((line) => plain(line) !== plain(pattern) || !plain(pattern))
  .join('\n');

export const togglePattern = (text, pattern, on) => (on ? addPattern(text, pattern) : removePattern(text, pattern));

// The top entries worth a tick: folders with a pattern, biggest first, not
// those an offer already covers.
export const bigEntries = (answer, max = 6) => {
  const offered = new Set(((answer && answer.offers) || []).map((o) => plain(o.pattern)));
  return ((answer && answer.top) || [])
    .filter((entry) => entry.dir && entry.pattern && entry.size > 0 && !offered.has(plain(entry.pattern)))
    .slice(0, max);
};

const inside = (path, parent) => path === parent || path.startsWith(parent.replace(/\/+$/, '') + '/');

// Places for the snapshots from the install plan's places
// (POST /api/install/plan: path, kind, free, total, osDisk, disk, exists):
// a folder "snapshots" on each data disk, best first: not the system's disk,
// not a disk a backed-up folder lies on, the most free space. None inside a
// folder that is backed up (the server refuses that).
export const destinationProposals = (places, sources) => {
  const folders = (sources || []).filter(Boolean);
  const seen = new Set();
  const out = [];
  (places || []).forEach((place) => {
    if (!place || !place.path || !['disk', 'data-path'].includes(place.kind)) return;
    const base = place.kind === 'disk' ? place.path : (place.disk || place.path);
    const path = base.replace(/\/+$/, '') + '/snapshots';
    if (seen.has(path) || folders.some((source) => inside(path, source))) return;
    seen.add(path);
    out.push({
      path,
      free: Number(place.free) || 0,
      total: Number(place.total) || 0,
      osDisk: Boolean(place.osDisk),
      sharesDisk: folders.some((source) => place.disk && inside(source, place.disk) && place.disk !== '/'),
      disk: place.disk || base,
    });
  });
  return out.sort((a, b) => (a.osDisk - b.osDisk) || (a.sharesDisk - b.sharesDisk) || (b.free - a.free));
};

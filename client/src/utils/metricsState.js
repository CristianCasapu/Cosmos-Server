// Fork addition: what the pages may draw from the answer of /api/metrics.
// A server that has measured nothing yet (a fresh install, the first minute
// after a start) answers with nothing, an empty list, or metrics without
// values: no chart may be built from that. Pure functions, tested without a
// browser.

// The metrics of an answer by their key. Always an object: nothing in the
// answer means "asked, and there is nothing yet", which is not "still loading".
export const metricsByKey = (data, previous) => {
  const result = previous ? { ...previous } : {};
  if (Array.isArray(data)) {
    for (const metric of data) {
      if (metric && typeof metric.Key === 'string') result[metric.Key] = metric;
    }
  }
  return result;
};

// value as a whole percentage of max, between 0 and 100; null when either is
// missing or max is not above zero (0/0 gave the dial its "NaN%").
export const percentOf = (value, max) => {
  const v = Number(value);
  const m = Number(max);
  if (value === null || value === undefined || max === null || max === undefined) return null;
  if (!Number.isFinite(v) || !Number.isFinite(m) || m <= 0) return null;
  return Math.max(0, Math.min(100, Math.round((v / m) * 100)));
};

// Is there anything to draw: at least one metric with at least one value.
export const hasMeasurements = (metrics) => {
  if (!metrics) return false;
  return Object.values(metrics).some((metric) => metric && Array.isArray(metric.Values) && metric.Values.length > 0);
};

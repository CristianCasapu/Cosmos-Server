package metrics

// Fork addition: which alerts a measured metric has to be checked against.
//
// Upstream looked for a "*" in the key of the metric that was just measured (which
// never holds one) and otherwise compared for equality, stopping at the first hit.
// So an alert on "cosmos.system.docker.cpu.*" never fired, and of two alerts on
// the same metric only one (a random one, the alerts being a map) was checked.

import (
	"regexp"
	"sort"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

// alertTracksMetric reports whether an alert's tracking metric covers the key of a
// metric. A "*" in the tracking metric stands for any run of characters; everything
// else is compared literally, over the whole key.
func alertTracksMetric(trackingMetric string, key string) bool {
	if !strings.Contains(trackingMetric, "*") {
		return trackingMetric == key
	}

	parts := strings.Split(trackingMetric, "*")
	for i, part := range parts {
		parts[i] = regexp.QuoteMeta(part)
	}
	pattern, err := regexp.Compile("^" + strings.Join(parts, ".*") + "$")
	if err != nil {
		return false
	}
	return pattern.MatchString(key)
}

// alertsForMetric returns every alert of the given period that tracks the metric,
// ordered by name so that the order of their actions does not change between runs.
func alertsForMetric(alerts map[string]utils.Alert, key string, period string) []utils.Alert {
	matching := []utils.Alert{}
	for _, alert := range alerts {
		if alert.Period == period && alertTracksMetric(alert.TrackingMetric, key) {
			matching = append(matching, alert)
		}
	}
	sort.Slice(matching, func(i, j int) bool { return matching[i].Name < matching[j].Name })
	return matching
}

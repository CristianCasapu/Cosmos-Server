package watch

// No snapshot for too long: for each folder of the incremental backup
// (src/snapshots) with a schedule, the last finished snapshot is compared
// with what the schedule allows plus a margin (half a period, at least six
// hours; the settings can give a number of hours instead). A folder that
// never had a snapshot counts from the first time it was watched. Late: a
// warning (snapshots.late); a new snapshot: a closing reply.

import (
	"sort"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	snapshotsEvery = time.Hour
	maxFolders     = 64
)

// period is how often the schedule makes a snapshot.
func period(frequency string) time.Duration {
	switch frequency {
	case "6h":
		return 6 * time.Hour
	case "12h":
		return 12 * time.Hour
	case "weekly":
		return 7 * 24 * time.Hour
	}
	return 24 * time.Hour
}

// lateAfter is when a folder's last snapshot is late.
func lateAfter(frequency string, override time.Duration) time.Duration {
	if override > 0 {
		return override
	}
	p := period(frequency)
	margin := p / 2
	if margin < 6*time.Hour {
		margin = 6 * time.Hour
	}
	return p + margin
}

func periodWords(frequency string) string {
	switch frequency {
	case "6h":
		return "6 hours"
	case "12h":
		return "12 hours"
	case "weekly":
		return "week"
	}
	return "day"
}

// checkSnapshots compares the last snapshot of each folder (last: zero when
// it has none) with its schedule.
func checkSnapshots(st *store, cfg utils.SnapshotsConfig, last map[string]time.Time, override time.Duration, now time.Time) {
	enabled := cfg.Enabled && strings.TrimSpace(cfg.Destination) != ""
	sources := []string{}
	if enabled {
		for _, source := range cfg.Sources {
			if source != "" && len(sources) < maxFolders {
				sources = append(sources, source)
			}
		}
	}
	sort.Strings(sources)
	limit := lateAfter(cfg.Frequency, override)
	st.with(func(data *stateData) bool {
		watched := map[string]bool{}
		for _, source := range sources {
			watched[source] = true
			if _, known := data.SnapFirst[source]; !known {
				data.SnapFirst[source] = now
			}
			since := last[source]
			lastWords := since.Local().Format("2 Jan 15:04")
			if since.IsZero() {
				since = data.SnapFirst[source]
				lastWords = "never"
			}
			age := now.Sub(since)
			thread := "snapshots.late:" + source
			switch {
			case age > limit && !data.SnapLate[source]:
				data.SnapLate[source] = true
				notify("warning", &utils.NotificationInfo{
					Kind: "snapshots.late", Source: "snapshots", Thread: thread,
					Title:   utils.NText("notif.snapshots.late.title", "folder", source, "age", durationWords(age)),
					What:    utils.NText("notif.snapshots.late.what", "folder", source, "age", durationWords(age), "last", lastWords, "period", periodWords(cfg.Frequency)),
					Means:   utils.NText("notif.snapshots.late.means"),
					Subject: &utils.NotificationSubject{Type: "folder", Name: source, Link: "/cosmos-ui/config-general/general"},
				})
			case age <= limit && data.SnapLate[source]:
				delete(data.SnapLate, source)
				resolveThread(thread)
				notify("success", &utils.NotificationInfo{
					Kind: "snapshots.ontime", Source: "snapshots", Thread: thread, Resolves: thread,
					Title:   utils.NText("notif.snapshots.ontime.title", "folder", source),
					What:    utils.NText("notif.snapshots.ontime.what", "folder", source, "last", lastWords),
					Subject: &utils.NotificationSubject{Type: "folder", Name: source, Link: "/cosmos-ui/config-general/general"},
				})
			}
		}
		// folders no longer backed up: forgotten, an open warning ended quietly
		for source := range data.SnapFirst {
			if !watched[source] {
				if data.SnapLate[source] {
					resolveThread("snapshots.late:" + source)
				}
				delete(data.SnapFirst, source)
				delete(data.SnapLate, source)
			}
		}
		return true
	})
}

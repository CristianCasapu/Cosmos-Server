package snapshots

import (
	"fmt"
	"sort"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	defaultKeepDaily   = 7
	defaultKeepWeekly  = 4
	defaultKeepMonthly = 6
)

// keepCounts returns the retention of the settings, with the defaults where
// nothing is set.
func keepCounts(cfg utils.SnapshotsConfig) (daily int, weekly int, monthly int) {
	daily, weekly, monthly = cfg.KeepDaily, cfg.KeepWeekly, cfg.KeepMonthly
	if daily <= 0 {
		daily = defaultKeepDaily
	}
	if weekly <= 0 {
		weekly = defaultKeepWeekly
	}
	if monthly <= 0 {
		monthly = defaultKeepMonthly
	}
	return daily, weekly, monthly
}

// retention sorts snapshot names into those to keep and those to remove. Kept
// are the newest snapshot of each of the last `daily` days, of each of the
// last `weekly` ISO weeks and of each of the last `monthly` months in which
// there is a snapshot, and always the newest one of all. A name that is not a
// snapshot's is in neither list: it is never removed.
func retention(names []string, daily int, weekly int, monthly int) (keep []string, remove []string) {
	ids := []string{}
	for _, name := range names {
		if validID(name) {
			ids = append(ids, name)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids))) // newest first

	kept := map[string]bool{}
	if len(ids) > 0 {
		kept[ids[0]] = true
	}

	rules := []struct {
		count  int
		period func(time.Time) string
	}{
		{daily, func(t time.Time) string { return t.Format("2006-01-02") }},
		{weekly, func(t time.Time) string {
			year, week := t.ISOWeek()
			return fmt.Sprintf("%04d-W%02d", year, week)
		}},
		{monthly, func(t time.Time) string { return t.Format("2006-01") }},
	}
	for _, rule := range rules {
		last, used := "", 0
		for _, id := range ids {
			at, _ := time.ParseInLocation(idLayout, id, time.Local)
			period := rule.period(at)
			if period == last {
				continue // an older snapshot of a period that already has its newest
			}
			if used >= rule.count {
				break
			}
			kept[id] = true
			last = period
			used++
		}
	}

	for _, id := range ids {
		if kept[id] {
			keep = append(keep, id)
		} else {
			remove = append(remove, id)
		}
	}
	return keep, remove
}

// prune applies the retention to the snapshots of source and returns the
// names it removed. It goes on after a snapshot that cannot be removed and
// reports the first such problem.
func prune(destination string, source string, daily int, weekly int, monthly int) (removed []string, err error) {
	dir, err := slugDir(destination, source)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, snapshot := range listSnapshots(dir) {
		names = append(names, snapshot.ID)
	}
	_, remove := retention(names, daily, weekly, monthly)
	for _, id := range remove {
		if errRemove := removeSnapshotDir(destination, source, id); errRemove != nil {
			if err == nil {
				err = fmt.Errorf("the old snapshot %s could not be removed (%s)", id, reason(errRemove))
			}
			continue
		}
		removed = append(removed, id)
	}
	return removed, err
}

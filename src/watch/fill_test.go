package watch

import (
	"testing"
	"time"
)

func usage(mount string, total uint64, free uint64) Usage {
	return Usage{Mount: mount, Total: total, Free: free}
}

func TestADiskFillingFastIsToldOnceAndEnds(t *testing.T) {
	r := record(t)
	st := newStore("")
	start := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	const tb = 1000 * 1000 * 1000 * 1000
	free := uint64(400 * 1000 * 1000 * 1000)
	// 10 GB every half hour: 480 GB a day, 400 GB free: full in less than a day
	for i := 0; i <= 14; i++ {
		checkFill(st, []Usage{usage("/mnt/data", 4*tb, free), usage("/boot", 512<<20, 1<<20), {Mount: "/mnt/ro", Total: 4 * tb, Free: 1, ReadOnly: true}}, 3, start.Add(time.Duration(i)*fillEvery))
		free -= 10 * 1000 * 1000 * 1000
	}
	if r.kinds() != "disk.filling" {
		t.Fatalf("%s", r.kinds())
	}
	what := r.written[0].Fork.What.Vars
	if r.written[0].Fork.Thread != "fill:/mnt/data" || what["left"] == "" || r.written[0].Level != "warning" {
		t.Errorf("%+v", what)
	}
	// it stops growing: once the day's trend is flat the warning ends
	for i := 15; i <= 70; i++ {
		checkFill(st, []Usage{usage("/mnt/data", 4*tb, free)}, 3, start.Add(time.Duration(i)*fillEvery))
	}
	if r.kinds() != "disk.filling,disk.filling.over" || r.written[1].Fork.Resolves != "fill:/mnt/data" {
		t.Fatalf("%s", r.kinds())
	}
}

func TestOneBigCopyOrASlowGrowthIsNoAlarm(t *testing.T) {
	r := record(t)
	st := newStore("")
	start := time.Now()
	const tb = 1000 * 1000 * 1000 * 1000
	free := uint64(3 * tb)
	for i := 0; i <= 48; i++ {
		if i == 20 {
			free -= 200 * 1000 * 1000 * 1000 // one large download, then nothing
		}
		free -= 100 * 1000 * 1000 // 100 MB every half hour: years to fill
		checkFill(st, []Usage{usage("/mnt/data", 4*tb, free)}, 3, start.Add(time.Duration(i)*fillEvery))
	}
	if len(r.written) != 0 {
		t.Errorf("%s", r.kinds())
	}
	// too few samples after a restart: nothing either
	if _, _, ok := trend([]fillSample{{At: start, Used: 1}, {At: start.Add(time.Hour), Used: 5}}); ok {
		t.Error("a trend from two samples")
	}
}

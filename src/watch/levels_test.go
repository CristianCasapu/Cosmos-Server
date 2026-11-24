package watch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// levelEnv gives a store whose levels are registered, with this run's
// memory empty.
func levelEnv(t *testing.T, path string) *store {
	t.Helper()
	lookedMu.Lock()
	savedLooked := looked
	looked = map[string]bool{}
	lookedMu.Unlock()
	savedRun, savedGone, savedNow, savedSnap := smartRun, fillGone, levelNow, snapshotLimits
	smartRun, fillGone = map[string]smartRunMemo{}, map[string]bool{}
	t.Cleanup(func() {
		lookedMu.Lock()
		looked = savedLooked
		lookedMu.Unlock()
		smartRun, fillGone, levelNow, snapshotLimits = savedRun, savedGone, savedNow, savedSnap
	})
	st := newStore(path)
	registerLevels(st)
	return st
}

// stateFile writes a state file the store reads as loaded.
func stateFile(t *testing.T, change func(d *stateData)) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "watch.state.json")
	st := newStore(path)
	st.with(func(d *stateData) bool { change(d); return true })
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	return path
}

// reviews 5 and 6: with the state file read, the watches say their level
// from it: open or over; a thing without a memo is unknown.
func TestTheWatchesSayTheirLevel(t *testing.T) {
	now := time.Now()
	path := stateFile(t, func(d *stateData) {
		d.SMART["serial:S1"] = smartMemo{Open: true}
		d.SMART["serial:S2"] = smartMemo{Unreadable: true}
		d.SMART["serial:S3"] = smartMemo{Read: true}
		d.Fill["/mnt/data"] = &fillRing{Open: true}
		d.Fill["/mnt/calm"] = &fillRing{}
		d.Docker.Open = true
		d.SnapLate["/srv"] = true
		d.SnapLast["/home"] = now.Add(-time.Hour)
	})
	levelEnv(t, path)
	snapshotLimits = func() (map[string]bool, time.Duration) {
		return map[string]bool{"/srv": true, "/home": true, "/new": true}, 36 * time.Hour
	}
	for _, c := range []struct {
		kind, thread string
		open, known  bool
	}{
		{"smart.failing", "smart:serial:S1", true, true}, {"smart.failing", "smart:serial:S3", false, true},
		{"smart.unreadable", "smart:serial:S2:unreadable", true, true}, {"smart.unreadable", "smart:serial:S3:unreadable", false, true},
		{"smart.failing", "smart:serial:never", false, false},
		{"disk.filling", "fill:/mnt/data", true, true}, {"disk.filling", "fill:/mnt/calm", false, true},
		{"disk.filling", "fill:/mnt/other", false, false},
		{"docker.down", "docker.down", true, true},
		{"snapshots.late", "snapshots.late:/srv", true, true}, {"snapshots.late", "snapshots.late:/home", false, true},
		{"snapshots.late", "snapshots.late:/new", false, false}, {"snapshots.late", "snapshots.late:/dropped", false, true},
	} {
		if open, known := utils.SourceThreadLevel(c.kind, c.thread); known != c.known || open != c.open {
			t.Errorf("%s %s: open %v known %v, want %v %v", c.kind, c.thread, open, known, c.open, c.known)
		}
	}
	opens := utils.SourceOpenThreads()
	if len(opens["smart.failing"]) != 1 || len(opens["disk.filling"]) != 1 || len(opens["docker.down"]) != 1 || len(opens["snapshots.late"]) != 1 || len(opens["smart.unreadable"]) != 1 {
		t.Errorf("open lists %v", opens)
	}
}

// review 6, M1: without the state file, a look of this run is evidence only
// once it concludes by the watch's own rule.
func TestWithoutTheStateFileALookMustConclude(t *testing.T) {
	record(t)
	st := levelEnv(t, filepath.Join(t.TempDir(), "missing.json"))
	start := time.Now()
	levelNow = func() time.Time { return start }
	snapshotLimits = func() (map[string]bool, time.Duration) { return map[string]bool{"/srv": true}, 36 * time.Hour }
	disk := Disk{Device: "/dev/sdb", Serial: "S9", Reallocated: 8}
	// S.M.A.R.T.: read well once, no history: not over
	checkSMART(st, []Disk{disk}, start)
	if _, known := utils.SourceThreadLevel("smart.failing", "smart:serial:S9"); known {
		t.Error("one read without history is evidence of health")
	}
	for i := 1; i < readWindow; i++ {
		checkSMART(st, []Disk{disk}, start.Add(time.Duration(i)*time.Hour))
	}
	if open, known := utils.SourceThreadLevel("smart.unreadable", "smart:serial:S9:unreadable"); open || !known {
		t.Errorf("readable for a window of reads: open %v known %v", open, known)
	}
	levelNow = func() time.Time { return start.Add(calmAfter) }
	if open, known := utils.SourceThreadLevel("smart.failing", "smart:serial:S9"); open || !known {
		t.Errorf("calm for calmAfter: open %v known %v", open, known)
	}
	// an unreadable disk never read in this run is not stored: unknown
	checkSMART(st, []Disk{{Device: "/dev/sdc", Serial: "S8", Unreadable: true}}, start)
	if _, known := utils.SourceThreadLevel("smart.unreadable", "smart:serial:S8:unreadable"); known {
		t.Error("a disk never read is known readable")
	}
	// Docker: a check inside the grace concludes nothing
	dw := newDockerWatch(st, start)
	dw.step(errors.New("refused"), start.Add(time.Minute))
	if dw.wasConcluded() {
		t.Error("a failing check inside the grace concluded")
	}
	if _, known := utils.SourceThreadLevel("docker.down", dockerThread); known {
		t.Error("Docker known without a concluding check")
	}
	dw.step(nil, start.Add(2*time.Minute))
	if !dw.wasConcluded() {
		t.Error("Docker answered: not concluded")
	}
	levelNow = func() time.Time { return start }
	// snapshots: the first look starts the count again: not evidence
	checkSnapshots(st, utils.SnapshotsConfig{Enabled: true, Destination: "/backup", Sources: []string{"/srv"}, Frequency: "daily"}, nil, nil, 0, start)
	if _, known := utils.SourceThreadLevel("snapshots.late", "snapshots.late:/srv"); known {
		t.Error("a folder without a known snapshot is known on time")
	}
	checkSnapshots(st, utils.SnapshotsConfig{Enabled: true, Destination: "/backup", Sources: []string{"/srv"}, Frequency: "daily"}, map[string]time.Time{"/srv": start}, nil, 0, start)
	if open, known := utils.SourceThreadLevel("snapshots.late", "snapshots.late:/srv"); open || !known {
		t.Errorf("a snapshot just made: open %v known %v", open, known)
	}
}

// review 6, F1: a look whose statfs failed or timed out concludes nothing:
// the warning stays open and the samples are kept; only a listing that
// worked and no longer holds the mount forgets it.
func TestAFailedFillLookIsNoEvidence(t *testing.T) {
	record(t)
	st := levelEnv(t, "")
	start := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	const tb = 1000 * 1000 * 1000 * 1000
	free := uint64(400 * 1000 * 1000 * 1000)
	for i := 0; i <= 14; i++ {
		checkFill(st, []Usage{usage("/mnt/data", 4*tb, free)}, true, 3, start.Add(time.Duration(i)*fillEvery))
		free -= 10 * 1000 * 1000 * 1000
	}
	if open, known := utils.SourceThreadLevel("disk.filling", "fill:/mnt/data"); !open || !known {
		t.Fatalf("filling: open %v known %v", open, known)
	}
	samples := 0
	st.with(func(d *stateData) bool { samples = len(d.Fill["/mnt/data"].Samples); return false })
	checkFill(st, []Usage{{Mount: "/mnt/data", Failed: true}}, true, 3, start.Add(15*fillEvery))
	checkFill(st, nil, false, 3, start.Add(16*fillEvery)) // the listing failed
	if _, known := utils.SourceThreadLevel("disk.filling", "fill:/mnt/data"); known {
		t.Errorf("after a failed look: known")
	}
	// review 7, N2: remounted read-only (a failing disk): not read, kept
	ro := usage("/mnt/data", 4*tb, free)
	ro.ReadOnly = true
	checkFill(st, []Usage{ro}, true, 3, start.Add(17*fillEvery))
	if _, known := utils.SourceThreadLevel("disk.filling", "fill:/mnt/data"); known {
		t.Errorf("read-only: known")
	}
	st.with(func(d *stateData) bool {
		if r := d.Fill["/mnt/data"]; r == nil || len(r.Samples) != samples {
			t.Errorf("the samples were not kept")
		}
		return false
	})
	// read again: open
	checkFill(st, []Usage{usage("/mnt/data", 4*tb, free)}, true, 3, start.Add(18*fillEvery))
	if open, known := utils.SourceThreadLevel("disk.filling", "fill:/mnt/data"); !open || !known {
		t.Errorf("read again: open %v known %v", open, known)
	}
	// absent from listings that worked: unknown, for a day; then over
	gone := start.Add(19 * fillEvery)
	checkFill(st, nil, true, 3, gone)
	checkFill(st, nil, true, 3, gone.Add(fillGoneAfter-time.Minute))
	if _, known := utils.SourceThreadLevel("disk.filling", "fill:/mnt/data"); known {
		t.Errorf("absent for less than a day: known")
	}
	checkFill(st, nil, true, 3, gone.Add(fillGoneAfter))
	if open, known := utils.SourceThreadLevel("disk.filling", "fill:/mnt/data"); open || !known {
		t.Errorf("absent for a day: open %v known %v", open, known)
	}
}

// review 6: a name the notifications cleaned (cut, trimmed) is still found
// by its source.
func TestALongNameIsFoundAsTheStoreKeepsIt(t *testing.T) {
	long := "/mnt/" + strings.Repeat("данные", 40)
	path := stateFile(t, func(d *stateData) { d.Fill[long] = &fillRing{Open: true} })
	levelEnv(t, path)
	stored := utils.ThreadName(fillThread(long))
	if stored == fillThread(long) {
		t.Fatal("the name is not cut by the store: the test proves nothing")
	}
	if open, known := utils.SourceThreadLevel("disk.filling", stored); !open || !known {
		t.Errorf("open %v known %v", open, known)
	}
	if list := utils.SourceOpenThreads()["disk.filling"]; len(list) != 1 || list[0] != stored {
		t.Errorf("open list %v", list)
	}
}

// review 6: a watch stuck and told is open; a look still in progress is
// unknown; a free slot (its look finished in this run) is over.
func TestTheLevelOfAStuckWatch(t *testing.T) {
	s := &slot{what: "failing disks"}
	registerStuck(map[string]*slot{"smart": s})
	thread := "watch.stuck:failing disks"
	if _, known := utils.SourceThreadLevel("watch.stuck", thread); known {
		t.Error("no look of this run yet: known")
	}
	s.ran = true
	s.busy, s.told = true, true
	if open, known := utils.SourceThreadLevel("watch.stuck", thread); !open || !known {
		t.Errorf("told: open %v known %v", open, known)
	}
	s.told = false
	if _, known := utils.SourceThreadLevel("watch.stuck", thread); known {
		t.Error("a look in progress concluded")
	}
	s.busy = false
	if open, known := utils.SourceThreadLevel("watch.stuck", thread); open || !known {
		t.Errorf("free: open %v known %v", open, known)
	}
}

// review 6, M1: a Docker check inside the start-up grace is no look for the
// level; one that Docker answered is.
func TestADockerCheckInsideTheGraceIsNoLook(t *testing.T) {
	record(t)
	st := levelEnv(t, filepath.Join(t.TempDir(), "missing.json"))
	dw := newDockerWatch(st, time.Now())
	lookDocker(dw, func(time.Duration) error { return errors.New("refused") })
	if _, known := utils.SourceThreadLevel("docker.down", dockerThread); known {
		t.Error("a failing check inside the grace counts")
	}
	lookDocker(dw, func(time.Duration) error { return nil })
	if open, known := utils.SourceThreadLevel("docker.down", dockerThread); open || !known {
		t.Errorf("Docker answered: open %v known %v", open, known)
	}
}

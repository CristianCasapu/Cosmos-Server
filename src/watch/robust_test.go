package watch

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Review R4: a disk that was read and then cannot be read is "cannot be
// read" after three tries, never "healthy again"; a disk never read is not
// told; a disk whose power state cannot be asked is said once, not read.
func TestADiskThatCannotBeRead(t *testing.T) {
	r := record(t)
	st := newStore("")
	now := time.Now()
	d := disk("S9")
	d.Reallocated = 13
	checkSMART(st, []Disk{d}, now)
	d.Reallocated = 20
	checkSMART(st, []Disk{d}, now.Add(time.Hour)) // failing
	unreadable := Disk{Device: d.Device, Serial: "S9", Unreadable: true}
	for i := 0; i < 6; i++ {
		checkSMART(st, []Disk{unreadable}, now.Add(time.Duration(2+i)*time.Hour))
	}
	if r.kinds() != "smart.failing,smart.unreadable" {
		t.Fatalf("%s", r.kinds())
	}
	never := Disk{Device: "/dev/sdz", Serial: "V1", Unreadable: true}
	for i := 0; i < 5; i++ {
		checkSMART(st, []Disk{unreadable, never}, now.Add(time.Duration(10+i)*time.Hour))
	}
	usb := Disk{Device: "/dev/sdu", Serial: "U1", Unwatchable: true}
	checkSMART(st, []Disk{unreadable, usb}, now.Add(20*time.Hour))
	checkSMART(st, []Disk{unreadable, usb}, now.Add(21*time.Hour))
	if r.kinds() != "smart.failing,smart.unreadable,smart.unwatchable" {
		t.Errorf("%s", r.kinds())
	}
}

// Review R5: one watch that hangs stops only itself, is said once after an
// hour, and the others go on.
func TestAHungWatchStopsOnlyItself(t *testing.T) {
	r := record(t)
	previous := stuckAfter
	stuckAfter = 50 * time.Millisecond
	defer func() { stuckAfter = previous }()

	release := make(chan struct{})
	hung := &slot{what: "disks filling fast"}
	other := &slot{what: "Docker"}
	runs := 0
	var mu sync.Mutex
	hung.start(time.Now(), func() { <-release })
	for i := 0; i < 5; i++ {
		time.Sleep(20 * time.Millisecond)
		hung.start(time.Now(), func() { t.Error("a second run of a hung watch") })
		done := make(chan struct{})
		other.start(time.Now(), func() { mu.Lock(); runs++; mu.Unlock(); close(done) })
		<-done
	}
	r.mu.Lock()
	first := r.written[0]
	r.mu.Unlock()
	close(release)
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline) && r.kinds() != "watch.stuck,watch.recovered"; {
		time.Sleep(10 * time.Millisecond)
	}
	if runs != 5 || r.kinds() != "watch.stuck,watch.recovered" || !strings.Contains(first.Fork.What.Vars["what"], "filling") {
		t.Errorf("%d runs of the other watch, %s", runs, r.kinds())
	}
	// a panic in a watch frees its slot
	p := &slot{what: "x"}
	finished := make(chan struct{})
	p.start(time.Now(), func() { defer close(finished); panic("boom") })
	<-finished
	time.Sleep(10 * time.Millisecond)
	ran := make(chan struct{})
	p.start(time.Now(), func() { close(ran) })
	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Error("a watch that panicked never runs again")
	}
}

// A panic in the Docker step or in writing a notification never stops the
// watches (review: two surviving mutations).
func TestPanicsAreContained(t *testing.T) {
	prevWrite, prevResolve := write, resolveThread
	defer func() { write, resolveThread = prevWrite, prevResolve }()
	write = func(utils.Notification) { panic("database gone") }
	notify("error", &utils.NotificationInfo{Kind: "docker.down"}) // must return

	resolveThread = func(string) { panic("database gone") }
	w := &dockerWatch{started: time.Now().Add(-time.Hour), open: true, failingSince: time.Now().Add(-time.Minute)}
	w.step(nil, time.Now()) // must return
	w.step(errors.New("refused"), time.Now())

	// a round whose reader panics returns, and the loop goes on
	slots := map[string]*slot{"docker": {what: "Docker"}, "smart": {what: "s"}, "fill": {what: "f"}, "snapshots": {what: "n"}, "accounts": {what: "a"}}
	func() {
		defer func() {
			if recover() != nil {
				t.Error("a round let a panic out")
			}
		}()
		round(newStore(""), Readers{Disks: func() ([]Disk, error) { panic("reader") }}, w, slots, func(string, time.Duration) bool { return true })
	}()
}

// Docker's open problem survives a restart; the log says down and back once each.
func TestDockerOutageAcrossARestart(t *testing.T) {
	r := record(t)
	lines := []string{}
	previous := logLine
	logLine = func(line string) { lines = append(lines, line) }
	defer func() { logLine = previous }()

	path := t.TempDir() + "/" + stateFileName
	st := newStore(path)
	start := time.Now().Add(-time.Hour)
	w := newDockerWatch(st, start)
	down := errors.New("connection refused")
	for s := 0; s <= 300; s += 30 {
		w.step(down, start.Add(10*time.Minute+time.Duration(s)*time.Second))
	}
	// Cosmos restarts while Docker is down
	w2 := newDockerWatch(newStore(path), time.Now())
	w2.step(nil, time.Now())
	if r.kinds() != "docker.down,docker.restored" || len(lines) != 2 {
		t.Errorf("%s, log %q", r.kinds(), lines)
	}
}

func TestTheCountriesForgetDeletedAccountsAndStayBounded(t *testing.T) {
	record(t)
	st := newStore("")
	lookup := func(string) (string, error) { return "RO", nil }
	checkSignIn(st, "alice", "203.0.113.7", lookup)
	checkSignIn(st, "bob", "203.0.113.7", lookup)
	checkAccounts(st, []string{"bob"})
	if _, has := st.data.Countries["alice"]; has {
		t.Error("a deleted account's countries are kept")
	}
	checkAccounts(st, nil) // the list could not be read: nothing forgotten
	if _, has := st.data.Countries["bob"]; !has {
		t.Error("forgotten on an unreadable list")
	}
	for i := 0; i < maxUsers+5; i++ {
		checkSignIn(st, "user"+string(rune('a'+i%26))+strings.Repeat("x", i/26), "203.0.113.7", lookup)
	}
	if len(st.data.Countries) > maxUsers {
		t.Errorf("%d accounts kept", len(st.data.Countries))
	}
	if _, has := st.data.Countries["bob"]; has {
		t.Error("the oldest account did not make room")
	}
}

// The backup disk is read only when a folder looks late by what is known
// in memory, and not again while it is known late.
func TestTheBackupDiskIsReadOnlyWhenNeeded(t *testing.T) {
	record(t)
	st := newStore("")
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	cfg := utils.SnapshotsConfig{Enabled: true, Destination: "/mnt/backup", Sources: []string{"/data"}, Frequency: "daily"}
	reads := 0
	disk := func(string) time.Time { reads++; return time.Time{} }
	memory := map[string]time.Time{"/data": now.Add(-2 * time.Hour)}
	for h := 0; h < 30; h++ {
		checkSnapshots(st, cfg, memory, disk, 0, now.Add(time.Duration(h)*time.Hour))
	}
	if reads != 0 {
		t.Errorf("%d reads while the memory says it is on time", reads)
	}
	for h := 30; h < 60; h++ {
		checkSnapshots(st, cfg, memory, disk, 0, now.Add(time.Duration(h)*time.Hour))
	}
	if reads != 1 {
		t.Errorf("%d reads of the backup disk for a folder that went late, want 1", reads)
	}
}

func TestTheStateFileIsWrittenWhole(t *testing.T) {
	path := t.TempDir() + "/" + stateFileName
	writeFileSynced(path, []byte(`{"x":1}`))
	st := newStore(path)
	st.with(func(d *stateData) bool { d.Countries["a"] = []string{"RO"}; return true })
	again := newStore(path)
	again.with(func(d *stateData) bool {
		if len(d.Countries["a"]) != 1 {
			t.Error("not read back")
		}
		return false
	})
}

// Review T2: a disk that was read and then fails even the power check is
// "cannot be read", never "not watched"; two failures in three reads reach
// it too; reading well for the whole window closes it.
func TestADiskThatStopsAnsweringIsNeverJustUnwatched(t *testing.T) {
	r := record(t)
	st := newStore("")
	now := time.Now()
	d := disk("S7")
	checkSMART(st, []Disk{d}, now)
	silent := Disk{Device: d.Device, Serial: "S7", Unwatchable: true}
	for i := 1; i <= 3; i++ {
		checkSMART(st, []Disk{silent}, now.Add(time.Duration(i)*time.Hour))
	}
	if r.kinds() != "smart.unreadable" {
		t.Fatalf("%s", r.kinds())
	}
	// two failures in every three reads
	r2 := record(t)
	st2 := newStore("")
	checkSMART(st2, []Disk{disk("S8")}, now)
	bad := Disk{Device: d.Device, Serial: "S8", Unreadable: true}
	for i := 1; i <= 9; i++ {
		if i%3 == 0 {
			checkSMART(st2, []Disk{disk("S8")}, now.Add(time.Duration(i)*time.Hour))
		} else {
			checkSMART(st2, []Disk{bad}, now.Add(time.Duration(i)*time.Hour))
		}
	}
	if !strings.Contains(r2.kinds(), "smart.unreadable") {
		t.Fatalf("two failures in three: %s", r2.kinds())
	}
	for i := 10; i <= 16; i++ {
		checkSMART(st2, []Disk{disk("S8")}, now.Add(time.Duration(i)*time.Hour))
	}
	if !strings.HasSuffix(r2.kinds(), "smart.readable") || r2.written[len(r2.written)-1].Fork.Resolves != "smart:serial:S8:unreadable" {
		t.Errorf("%s", r2.kinds())
	}
}

// The disk's key does not change with the path it was seen on.
func TestTheDiskKeyIsTheListedOne(t *testing.T) {
	if (Disk{Serial: "A", WWN: "0x5"}).key() != "serial:A" || (Disk{WWN: "0x5", Device: "/dev/sda"}).key() != "wwn:0x5" {
		t.Error("key")
	}
}

// A stuck watch's end is told; an open Docker problem ends when its watch is switched off.
func TestStuckAndUnwatchedHaveTheirClosings(t *testing.T) {
	r := record(t)
	previous := stuckAfter
	stuckAfter = 30 * time.Millisecond
	defer func() { stuckAfter = previous }()
	release := make(chan struct{})
	s := &slot{what: "late snapshots"}
	s.start(time.Now(), func() { <-release })
	time.Sleep(50 * time.Millisecond)
	s.checkStuck(time.Now())
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(r.kinds(), "watch.recovered") {
		time.Sleep(10 * time.Millisecond)
	}
	if r.kinds() != "watch.stuck,watch.recovered" {
		t.Errorf("%s", r.kinds())
	}
	w := &dockerWatch{open: true, failingSince: time.Now().Add(-time.Hour)}
	w.unwatched()
	w.unwatched()
	if !strings.HasSuffix(r.kinds(), "docker.unwatched") || strings.Count(r.kinds(), "docker.unwatched") != 1 {
		t.Errorf("%s", r.kinds())
	}
}

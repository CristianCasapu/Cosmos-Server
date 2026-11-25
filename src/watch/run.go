package watch

import (
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Readers are what the watches read the world with. The server gives the
// real ones (src/fork_watch.go); a nil reader switches its watch off.
type Readers struct {
	Disks      func() ([]Disk, error)
	Usages     func() ([]Usage, bool)
	DockerPing func(timeout time.Duration) error
	// Snapshots gives the settings of the incremental backup and the last
	// success of each folder the snapshot module knows in memory;
	// SnapshotOnDisk reads a folder's last snapshot on the backup disk.
	Snapshots      func() (utils.SnapshotsConfig, map[string]time.Time)
	SnapshotOnDisk func(source string) time.Time
	// Country is the local country lookup (GeoLite file); Users lists the
	// accounts (nil when it cannot).
	Country func(ip string) (string, error)
	Users   func() []string
}

var startOnce sync.Once

// ready reports whether the watches may work: the setup is finished.
func ready() bool { return !utils.GetMainConfig().NewInstall }

func settingsNow() Settings { return SettingsOf(utils.GetMainConfig().Watch) }

// Start starts the watches; with the setup not finished they wait. It
// returns the handler of successful sign-ins (user, address), for the
// server to give the security record.
func Start(r Readers) func(user string, ip string) {
	st := newStore(statePath())
	startOnce.Do(func() {
		registerLevels(st)
		go loop(st, r, time.Now())
	})
	return func(user string, ip string) {
		if r.Country == nil || !ready() || !settingsNow().Country {
			return
		}
		defer func() { recover() }()
		checkSignIn(st, user, ip, r.Country)
	}
}

// stuckAfter is how long a watch may run before it is said to hang.
var stuckAfter = time.Hour

// slot runs one watch at a time in a goroutine of its own: a watch that
// hangs (a dead network share) stops only itself, is not started again
// while it hangs, and is said once (watch.stuck).
type slot struct {
	what  string
	mu    sync.Mutex
	busy  bool
	since time.Time
	told  bool
	ran   bool // a look of this run started (review 7: before, unknown)
}

func (s *slot) start(now time.Time, work func()) {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		s.checkStuck(now)
		return
	}
	s.busy, s.since, s.ran = true, now, true
	s.mu.Unlock()
	go func() {
		defer func() {
			recover()
			s.mu.Lock()
			told, since := s.told, s.since
			s.busy, s.told = false, false
			s.mu.Unlock()
			if told {
				// review T2: the stuck check has its closing
				thread := "watch.stuck:" + s.what
				resolveThread(thread)
				notify("success", &utils.NotificationInfo{
					Kind: "watch.recovered", Source: "server", Thread: thread, Resolves: thread,
					Title: utils.NText("notif.watch.recovered.title", "what", s.what),
					What:  utils.NText("notif.watch.recovered.what", "what", s.what, "age", durationWords(time.Since(since))),
				})
			}
		}()
		work()
	}()
}

// checkStuck says once that a watch has run for stuckAfter; asked every
// round, so the hour is kept whatever the watch's own rhythm.
func (s *slot) checkStuck(now time.Time) {
	s.mu.Lock()
	tell := s.busy && !s.told && now.Sub(s.since) >= stuckAfter
	if tell {
		s.told = true
	}
	since := s.since
	s.mu.Unlock()
	if tell {
		notify("warning", &utils.NotificationInfo{
			Kind: "watch.stuck", Source: "server", Thread: "watch.stuck:" + s.what,
			Title: utils.NText("notif.watch.stuck.title", "what", s.what),
			What:  utils.NText("notif.watch.stuck.what", "what", s.what, "age", durationWords(now.Sub(since))),
		})
	}
}

// loop starts the watches when they are due, each in its slot. A panic in
// one round starts the next round all the same.
func loop(st *store, r Readers, started time.Time) {
	for !ready() {
		time.Sleep(30 * time.Second)
	}
	docker := newDockerWatch(st, started)
	slots := map[string]*slot{
		"docker": {what: "Docker"}, "smart": {what: "failing disks"}, "fill": {what: "disks filling fast"},
		"snapshots": {what: "late snapshots"}, "accounts": {what: "accounts"},
	}
	registerStuck(slots)
	next := map[string]time.Time{
		"smart":     time.Now().Add(5 * time.Minute),
		"fill":      time.Now().Add(time.Minute),
		"snapshots": time.Now().Add(10 * time.Minute),
		"accounts":  time.Now().Add(15 * time.Minute),
	}
	due := func(name string, every time.Duration) bool {
		if time.Now().Before(next[name]) {
			return false
		}
		next[name] = time.Now().Add(every)
		return true
	}
	for {
		func() {
			defer func() { recover() }()
			round(st, r, docker, slots, due)
		}()
		time.Sleep(dockerEvery)
	}
}

// round starts what is due.
func round(st *store, r Readers, docker *dockerWatch, slots map[string]*slot, due func(string, time.Duration) bool) {
	s := settingsNow()
	now := time.Now()
	for _, sl := range slots {
		sl.checkStuck(now)
	}
	if s.Docker && r.DockerPing != nil {
		slots["docker"].start(now, func() { lookDocker(docker, r.DockerPing) })
	} else {
		docker.unwatched()
		markLooked("docker")
	}
	if due("smart", smartEvery) && s.SMART && r.Disks != nil {
		slots["smart"].start(now, func() {
			if disks, err := r.Disks(); err == nil {
				checkSMART(st, disks, time.Now())
				markLooked("smart")
			}
		})
	}
	if due("fill", fillEvery) && s.Fill && r.Usages != nil {
		slots["fill"].start(now, func() {
			usages, complete := r.Usages()
			checkFill(st, usages, complete, s.FillDays, time.Now())
			markLooked("fill")
		})
	}
	if due("snapshots", snapshotsEvery) && s.Snapshots && r.Snapshots != nil {
		slots["snapshots"].start(now, func() {
			cfg, last := r.Snapshots()
			checkSnapshots(st, cfg, last, r.SnapshotOnDisk, s.SnapshotLate, time.Now())
			markLooked("snapshots")
		})
	}
	if due("accounts", time.Hour) && r.Users != nil {
		slots["accounts"].start(now, func() { checkAccounts(st, r.Users()) })
	}
}

func contains(text string, parts ...string) bool {
	text = strings.ToLower(text)
	for _, part := range parts {
		if strings.Contains(text, part) {
			return true
		}
	}
	return false
}

// lookDocker asks Docker once; the look counts for the level only when it
// concluded (plan 48, review 6: a check inside the grace concludes nothing).
func lookDocker(docker *dockerWatch, ping func(time.Duration) error) {
	docker.check(ping, time.Now)
	if docker.wasConcluded() {
		markLooked("docker")
	}
}

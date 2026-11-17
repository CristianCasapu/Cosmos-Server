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
	Disks  func() ([]Disk, error)
	Usages func() []Usage
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
	startOnce.Do(func() { go loop(st, r, time.Now()) })
	return func(string, string) {}
}

func loop(st *store, r Readers, _ time.Time) {
	defer func() {
		if p := recover(); p != nil {
			utils.Error("Watches: stopped after an unexpected error", nil)
		}
	}()
	for !ready() {
		time.Sleep(30 * time.Second)
	}
	next := map[string]time.Time{
		"smart": time.Now().Add(5 * time.Minute),
		"fill":  time.Now().Add(time.Minute),
	}
	run := func(name string, every time.Duration, work func()) {
		if time.Now().Before(next[name]) {
			return
		}
		next[name] = time.Now().Add(every)
		func() {
			defer func() { recover() }()
			work()
		}()
	}
	for {
		s := settingsNow()
		run("smart", smartEvery, func() {
			if s.SMART && r.Disks != nil {
				if disks, err := r.Disks(); err == nil {
					checkSMART(st, disks, time.Now())
				}
			}
		})
		run("fill", fillEvery, func() {
			if s.Fill && r.Usages != nil {
				checkFill(st, r.Usages(), s.FillDays, time.Now())
			}
		})
		time.Sleep(30 * time.Second)
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

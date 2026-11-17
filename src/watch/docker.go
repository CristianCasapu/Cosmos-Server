package watch

// Docker not answering: Docker is asked every dockerEvery through the
// server's own connection (Readers.DockerPing); one failed answer is a
// hiccup, a failure that lasts dockerDownAfter is an error (docker.down),
// never in the first dockerGrace after Cosmos started (Docker may start
// after it at boot). When it answers again, a closing reply with how long it
// lasted. A ping that does not come back in time counts as a failure, and
// no second one is sent while it hangs.

import (
	"strconv"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	dockerEvery     = 30 * time.Second
	dockerTimeout   = 15 * time.Second
	dockerDownAfter = 2 * time.Minute
	dockerGrace     = 3 * time.Minute
	dockerThread    = "docker.down"
)

type dockerWatch struct {
	mu           sync.Mutex
	st           *store // its open problem survives a restart; nil: in memory only
	started      time.Time
	failingSince time.Time
	reason       string
	open         bool
	pending      bool
}

// step records one answer (err nil: Docker answered) and writes what
// started or ended.
func (w *dockerWatch) step(err error, now time.Time) {
	defer func() { recover() }() // review: a panic here never stops the watches
	w.mu.Lock()
	defer w.mu.Unlock()
	if err == nil {
		if w.open {
			lasted := now.Sub(w.failingSince)
			w.open = false
			w.remember()
			logLine("Watches: Docker answers again")
			resolveThread(dockerThread)
			notify("success", &utils.NotificationInfo{
				Kind: "docker.restored", Source: "containers", Thread: dockerThread, Resolves: dockerThread,
				Title:   utils.NText("notif.docker.restored.title"),
				What:    utils.NText("notif.docker.restored.what", "duration", utils.NotificationDuration(lasted)),
				Subject: &utils.NotificationSubject{Type: "service", Name: "Docker", Link: "/cosmos-ui/servapps"},
			})
		}
		w.failingSince = time.Time{}
		return
	}
	if w.failingSince.IsZero() {
		w.failingSince = now
	}
	w.reason = err.Error()
	if w.open || now.Sub(w.failingSince) < dockerDownAfter || now.Sub(w.started) < dockerGrace {
		return
	}
	w.open = true
	w.remember()
	logLine("Watches: Docker does not answer: " + reasonWords(w.reason))
	minutes := int(now.Sub(w.failingSince).Minutes() + 0.5)
	notify("error", &utils.NotificationInfo{
		Kind: "docker.down", Source: "containers", Thread: dockerThread,
		Title:   utils.NText("notif.docker.down.title"),
		What:    utils.NText("notif.docker.down.what", "minutes", strconv.Itoa(minutes), "reason", reasonWords(w.reason)),
		Means:   utils.NText("notif.docker.down.means"),
		Subject: &utils.NotificationSubject{Type: "service", Name: "Docker", Link: "/cosmos-ui/servapps"},
		Raw:     w.reason,
	})
}

// newDockerWatch starts the Docker watch with what a previous run left: an
// open problem stays open, so that its end answers it.
func newDockerWatch(st *store, started time.Time) *dockerWatch {
	w := &dockerWatch{st: st, started: started}
	if st != nil {
		st.with(func(data *stateData) bool {
			if data.Docker.Open {
				w.open, w.failingSince = true, data.Docker.Since
			}
			return false
		})
	}
	return w
}

// unwatched ends an open problem when the Docker watch is switched off:
// whether Docker answers is not known any more (review T2).
func (w *dockerWatch) unwatched() {
	defer func() { recover() }()
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.open {
		return
	}
	w.open, w.failingSince = false, time.Time{}
	w.remember()
	resolveThread(dockerThread)
	notify("info", &utils.NotificationInfo{
		Kind: "docker.unwatched", Source: "containers", Thread: dockerThread, Resolves: dockerThread,
		Title:   utils.NText("notif.docker.unwatched.title"),
		What:    utils.NText("notif.docker.unwatched.what"),
		Subject: &utils.NotificationSubject{Type: "service", Name: "Docker", Link: "/cosmos-ui/servapps"},
	})
}

// remember keeps the open problem in the state file.
func (w *dockerWatch) remember() {
	if w.st == nil {
		return
	}
	memo := dockerMemo{Open: w.open, Since: w.failingSince}
	w.st.with(func(data *stateData) bool {
		data.Docker = memo
		return true
	})
}

// logLine writes one line to the server's log. A variable for the tests.
var logLine = func(line string) { utils.Warn(line) }

// reasonWords says the usual failures in words; anything else is "no answer"
// (the error text itself is the notification's technical text).
func reasonWords(err string) string {
	switch {
	case contains(err, "connection refused"):
		return "the connection was refused"
	case contains(err, "no such file"):
		return "its socket is missing"
	case contains(err, "deadline", "timeout", "did not answer"):
		return "it did not answer in time"
	case contains(err, "permission denied"):
		return "access to it was refused"
	}
	return "it did not answer"
}

// check asks Docker once, in the background, and records the answer.
func (w *dockerWatch) check(ping func(time.Duration) error, now func() time.Time) {
	w.mu.Lock()
	if w.pending {
		w.mu.Unlock()
		w.step(errHanging, now())
		return
	}
	w.pending = true
	w.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		defer func() {
			if recover() != nil {
				done <- errHanging
			}
		}()
		done <- ping(dockerTimeout)
	}()
	var err error
	select {
	case err = <-done:
		w.mu.Lock()
		w.pending = false
		w.mu.Unlock()
	case <-time.After(dockerTimeout + 5*time.Second):
		err = errHanging
		go func() { // the ping is left to finish; until then no other is sent
			<-done
			w.mu.Lock()
			w.pending = false
			w.mu.Unlock()
		}()
	}
	w.step(err, now())
}

type hangingError struct{}

func (hangingError) Error() string { return "Docker did not answer in time" }

var errHanging error = hangingError{}

package reachwatch

// The history (the outages, bounded) and an outage still open are kept in
// reach-watch.json in the configuration folder, so that "back after N
// minutes" is right across a restart. Written beside the file and renamed,
// readable by the server only, and only when something changed.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const maxFile = 256 << 10

type stored struct {
	Outage  *Outage              `json:"outage,omitempty"`
	History []Outage             `json:"history"`
	Seen    map[string]time.Time `json:"seen,omitempty"`
	// the names that were down, with cause and since, to take up again
	Down []NameState `json:"down,omitempty"`
}

var storePath = func() string { return filepath.Join(utils.CONFIGFOLDER, "reach-watch.json") }

// save writes the file when something changed.
func (w *watcher) save() {
	w.mu.Lock()
	if !w.dirty {
		w.mu.Unlock()
		return
	}
	w.dirty = false
	s := stored{Outage: w.outage, History: w.history, Seen: map[string]time.Time{}}
	for host, st := range w.names {
		if !st.SeenAt.IsZero() {
			s.Seen[host] = st.SeenAt
		}
		if st.State == StateDown {
			s.Down = append(s.Down, NameState{Host: st.Host, Panel: st.Panel, State: StateDown, Cause: st.Cause, Code: st.Code, Since: st.Since, Fails: st.Fails})
		}
	}
	raw, err := json.Marshal(s)
	w.mu.Unlock()
	if err != nil {
		return
	}
	path := storePath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		utils.Warn("Reachability watch: the history could not be written")
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		utils.Warn("Reachability watch: the history could not be written")
	}
}

// load reads the file at the start.
func (w *watcher) load() {
	f, err := os.Open(storePath())
	if err != nil {
		return
	}
	defer f.Close()
	var s stored
	if err := json.NewDecoder(&limited{f, maxFile}).Decode(&s); err != nil {
		utils.Warn("Reachability watch: the history could not be read, starting afresh")
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(s.History) > maxHistory {
		s.History = s.History[len(s.History)-maxHistory:]
	}
	w.history = s.History
	if s.Outage != nil && s.Outage.End == nil {
		w.outage = s.Outage
	}
	for host, at := range s.Seen {
		if len(w.names) >= maxWatched {
			break
		}
		w.names[host] = &NameState{Host: host, State: StatePending, SeenAt: at}
	}
	if w.outage != nil {
		for _, d := range s.Down {
			if len(w.names) >= maxWatched && w.names[d.Host] == nil {
				break
			}
			st := d
			w.names[d.Host] = &st
		}
	}
}

type limited struct {
	f    *os.File
	left int
}

func (l *limited) Read(p []byte) (int, error) {
	if l.left <= 0 {
		return 0, os.ErrInvalid
	}
	if len(p) > l.left {
		p = p[:l.left]
	}
	n, err := l.f.Read(p)
	l.left -= n
	return n, err
}

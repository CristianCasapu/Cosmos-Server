package edgeblock

// The state that survives a restart: one JSON file in the configuration
// folder, mode 0600, written to a temporary name and renamed, at most every
// five seconds while something changed, and at stop. Bounded: rule entries,
// offenders and administrators' addresses each have a ceiling, and what is
// older than its use is dropped when the file is read.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// FileName is the file in the configuration folder.
const FileName = "cloudflare-edge.json"

const maxFile = 4 << 20

type state struct {
	// Marker goes into the note of every rule: "cosmos-edge:<Marker>"
	Marker    string               `json:"marker"`
	Rules     []*Rule              `json:"rules"`
	Offenders map[string]*offender `json:"offenders"`
	Admins    map[string]time.Time `json:"admins"`
	// blocks not issued because of the caps
	Dropped   int       `json:"dropped,omitempty"`
	DroppedAt time.Time `json:"droppedAt,omitzero"`
}

func emptyState() state {
	return state{Marker: newMarker(), Rules: []*Rule{}, Offenders: map[string]*offender{}, Admins: map[string]time.Time{}}
}

func newMarker() string {
	raw := make([]byte, 6)
	rand.Read(raw)
	return hex.EncodeToString(raw)
}

func statePath() string { return filepath.Join(utils.CONFIGFOLDER, FileName) }

// markerOf is the text every note of this server carries.
func (s state) markerText() string { return "cosmos-edge:" + s.Marker }

var validStates = map[string]bool{StatePending: true, StateActive: true, StateDeleting: true, StateGone: true, StateOwner: true, StateRefused: true, StateOrphaned: true}

// load reads the file; a missing or unreadable file gives an empty state
// (with a new marker: rules of an old one are then leftovers).
func (e *Engine) load() {
	e.mu.Lock()
	defer e.mu.Unlock()
	file, err := os.Open(e.path())
	if err != nil {
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxFile+1))
	if err != nil || len(raw) > maxFile {
		utils.Warn("Cloudflare edge blocks: the state file could not be read, starting afresh")
		return
	}
	var read state
	if json.Unmarshal(raw, &read) != nil || len(read.Marker) != 12 {
		utils.Warn("Cloudflare edge blocks: the state file could not be read, starting afresh")
		return
	}
	now := e.now()
	st := emptyState()
	st.Marker = read.Marker
	st.Dropped, st.DroppedAt = read.Dropped, read.DroppedAt
	for _, r := range read.Rules {
		if r == nil || !validStates[r.State] || len(st.Rules) >= maxEntries {
			continue
		}
		if _, ok := prefixOfKey(r.Key); !ok {
			continue
		}
		// what ended long ago and is not at Cloudflare any more is forgotten
		if r.State != StateActive && r.State != StateDeleting && r.State != StateOrphaned && now.Sub(r.Until) > 0 {
			continue
		}
		st.Rules = append(st.Rules, r)
	}
	for key, o := range read.Offenders {
		if o == nil || len(st.Offenders) >= maxOffenders || now.Sub(o.Last) > historyFor {
			continue
		}
		if _, ok := prefixOfKey(key); ok {
			st.Offenders[key] = o
		}
	}
	for key, at := range read.Admins {
		if len(st.Admins) < maxAdmins && now.Sub(at) < AdminFor {
			if _, ok := prefixOfKey(key); ok {
				st.Admins[key] = at
			}
		}
	}
	e.st = st
}

// save writes the file when something changed (force: now, else at most
// every five seconds).
func (e *Engine) save(force bool) {
	e.mu.Lock()
	if !e.dirty || (!force && e.now().Sub(e.savedAt) < 5*time.Second) {
		e.mu.Unlock()
		return
	}
	raw, err := json.Marshal(e.st)
	e.dirty = false
	e.savedAt = e.now()
	e.mu.Unlock()
	if err != nil {
		return
	}
	if err := writeFile(e.path(), raw); err != nil {
		utils.Warn("Cloudflare edge blocks: the state could not be saved: " + err.Error())
		e.mu.Lock()
		e.dirty = true
		e.mu.Unlock()
	}
}

func writeFile(path string, raw []byte) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".cloudflare-edge-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(raw); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

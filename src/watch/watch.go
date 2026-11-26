// Package watch holds the fork's own watches (plan 48): a disk whose
// S.M.A.R.T. data says it is failing, a disk filling fast, Docker not
// answering, a snapshot folder without a recent snapshot, and a sign-in from
// a country the account never signed in from. Each turns what it sees into
// a notification for the administrators (and with it a Telegram message)
// when something starts, and a closing one when it is over.
//
// Fork addition: nothing here exists upstream. The server starts it with
// Start once the setup is finished, and gives it the readers of the real
// world (Readers); the tests give it fakes. Every run is bounded (disks,
// mounts, folders, users), nothing is fetched from outside the server, and
// the little that is remembered is kept in watch.state.json (0600) in the
// configuration folder.
package watch

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	stateFileName = "watch.state.json"
	maxStateBytes = 512 << 10

	defaultFillDays = 3
	maxFillDays     = 30
	maxLateHours    = 24 * 90
)

// Settings are the watches' settings as they are used.
type Settings struct {
	SMART, Fill, Docker, Snapshots, Country bool
	FillDays                                int
	// SnapshotLate replaces the delay derived from the schedule; 0: derived.
	SnapshotLate time.Duration
}

// SettingsOf reads the configuration; values out of range take the default.
func SettingsOf(c utils.WatchConfig) Settings {
	s := Settings{SMART: !c.SMARTOff, Fill: !c.FillOff, Docker: !c.DockerOff, Snapshots: !c.SnapshotsOff, Country: !c.CountryOff, FillDays: c.FillDays}
	if s.FillDays < 1 || s.FillDays > maxFillDays {
		s.FillDays = defaultFillDays
	}
	if c.SnapshotLateHours > 0 && c.SnapshotLateHours <= maxLateHours {
		s.SnapshotLate = time.Duration(c.SnapshotLateHours) * time.Hour
	}
	return s
}

// Validate checks the settings when they are saved. The error is a
// sentence for the administrator.
func Validate(c *utils.WatchConfig) error {
	if c.FillDays != 0 && (c.FillDays < 1 || c.FillDays > maxFillDays) {
		return errors.New("Watches: warn about a disk that fills within 1 to " + strconv.Itoa(maxFillDays) + " days")
	}
	if c.SnapshotLateHours != 0 && (c.SnapshotLateHours < 1 || c.SnapshotLateHours > maxLateHours) {
		return errors.New("Watches: a snapshot is late after 1 to " + strconv.Itoa(maxLateHours) + " hours, or leave it empty to follow the schedule")
	}
	return nil
}

// write is how a notification is written. A variable for the tests.
var write = utils.WriteNotification

// notify writes one notification for the administrators, never panicking
// out of a watch.
func notify(level string, info *utils.NotificationInfo) {
	defer func() { recover() }()
	write(utils.Notification{Recipient: "admin", Level: level, Fork: info})
}

// What the S.M.A.R.T. watch remembers of a disk.
type smartMemo struct {
	Device        string    `json:"device"`
	Model         string    `json:"model,omitempty"`
	Serial        string    `json:"serial,omitempty"`
	Reallocated   int64     `json:"reallocated"`
	Pending       int64     `json:"pending"`
	Uncorrectable int64     `json:"uncorrectable"`
	MediaErrors   int64     `json:"mediaErrors"`
	Open          bool      `json:"open,omitempty"`
	LastBad       time.Time `json:"lastBad,omitempty"`
	Missing       int       `json:"missing,omitempty"`
	Seen          time.Time `json:"seen"`
	// Read: it was read once; History: the last reads;
	// Unwatchable: said once that it cannot be read without waking it
	Read        bool   `json:"read,omitempty"`
	History     []bool `json:"history,omitempty"` // the last reads: ok or not
	Unreadable  bool   `json:"unreadable,omitempty"`
	Unwatchable bool   `json:"unwatchable,omitempty"`
}

// What the fill watch keeps of a mount.
type fillSample struct {
	At   time.Time `json:"at"`
	Used float64   `json:"used"`
}

type fillRing struct {
	Samples []fillSample `json:"samples"`
	Open    bool         `json:"open,omitempty"`
	// plan 48, review 7 (N2): the last look could not read it (statfs
	// failed, or it is mounted read-only: the classic sign of a failing
	// disk); since when listings that worked no longer hold it
	Unread  bool      `json:"unread,omitempty"`
	Missing time.Time `json:"missing,omitempty"`
}

// stateData is what is remembered between runs and restarts.
type stateData struct {
	SMART     map[string]smartMemo `json:"smart,omitempty"`
	Fill      map[string]*fillRing `json:"fill,omitempty"`
	SnapFirst map[string]time.Time `json:"snapFirst,omitempty"`
	SnapLate  map[string]bool      `json:"snapLate,omitempty"`
	Countries map[string][]string  `json:"countries,omitempty"`
	// review (second round): when an account's countries last changed (for
	// the bound), the last snapshot seen per folder, Docker's open problem
	CountriesAt map[string]time.Time `json:"countriesAt,omitempty"`
	SnapLast    map[string]time.Time `json:"snapLast,omitempty"`
	Docker      dockerMemo           `json:"docker,omitempty"`
}

type dockerMemo struct {
	Open  bool      `json:"open,omitempty"`
	Since time.Time `json:"since,omitempty"`
}

// store keeps stateData in a file; path "" keeps it in memory only.
type store struct {
	mu   sync.Mutex
	path string
	data stateData
	read bool
	// loaded: the state was read from the file (review 5: it is then the
	// level of the watches' problems)
	loaded bool
}

func newStore(path string) *store { return &store{path: path} }

// with runs change on the state, loaded once, and saves it after.
func (s *store) with(change func(d *stateData) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.read {
		s.read = true
		if s.path != "" {
			if info, err := os.Stat(s.path); err == nil && info.Size() <= maxStateBytes {
				if raw, err := os.ReadFile(s.path); err == nil {
					s.loaded = json.Unmarshal(raw, &s.data) == nil
				}
			}
		}
	}
	if s.data.SMART == nil {
		s.data.SMART = map[string]smartMemo{}
	}
	if s.data.Fill == nil {
		s.data.Fill = map[string]*fillRing{}
	}
	if s.data.SnapFirst == nil {
		s.data.SnapFirst = map[string]time.Time{}
	}
	if s.data.SnapLate == nil {
		s.data.SnapLate = map[string]bool{}
	}
	if s.data.Countries == nil {
		s.data.Countries = map[string][]string{}
	}
	if s.data.CountriesAt == nil {
		s.data.CountriesAt = map[string]time.Time{}
	}
	if s.data.SnapLast == nil {
		s.data.SnapLast = map[string]time.Time{}
	}
	if change(&s.data) && s.path != "" {
		if raw, err := json.Marshal(s.data); err == nil && len(raw) <= maxStateBytes {
			writeFileSynced(s.path, raw)
		}
	}
}

// writeFileSynced writes a file beside the target, flushes it to the disk
// and renames it over the target, so that a power loss leaves the old file
// or the new one, never an empty one.
func writeFileSynced(path string, raw []byte) {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return
	}
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	if f.Close() != nil || err != nil || os.Rename(tmp, path) != nil {
		os.Remove(tmp)
	}
}

func statePath() string { return filepath.Join(utils.CONFIGFOLDER, stateFileName) }

// sizeWords says a number of bytes the way people read it.
func sizeWords(n float64) string {
	units := []string{"bytes", "KB", "MB", "GB", "TB", "PB"}
	i := 0
	for n >= 1000 && i < len(units)-1 {
		n /= 1000
		i++
	}
	if i == 0 || n >= 100 {
		return strconv.Itoa(int(n+0.5)) + " " + units[i]
	}
	return strconv.FormatFloat(n, 'f', 1, 64) + " " + units[i]
}

// durationWords says a span in days or hours.
func durationWords(d time.Duration) string {
	hours := int(d.Hours() + 0.5)
	switch {
	case hours < 1:
		return "less than an hour"
	case hours == 1:
		return "1 hour"
	case hours < 48:
		return strconv.Itoa(hours) + " hours"
	}
	days := (hours + 12) / 24
	return strconv.Itoa(days) + " days"
}

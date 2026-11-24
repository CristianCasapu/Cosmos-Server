package telegram

// Fork addition (plan 48): what the chats hold of the threads (a problem of
// a declared pair, see threadorder.go): the message that opened each one,
// whether it is pinned, the unpins Telegram refused (tried again), and the
// state of the threads not settled yet (review 4, B3), so that a restart
// loses nothing. Kept in a small file beside the state file, per chat
// (message numbers are per chat; a few chats are kept, so that after a
// change of chat the old one can still be unpinned).
//
// A thread is known by a short hash of its name (review 4, P3: names of any
// length and script). The name is kept beside it, bounded, to ask the
// notifications whether the problem is still open (reconcile).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	threadsFileName = "telegram.threads.json"
	maxThreads      = 256
	maxPinned       = 3
	maxThreadName   = 300 // runes
	maxThreadsBytes = 1 << 20
	maxThreadChats  = 4
	maxPinTries     = 3
)

// threadID is the short hash a thread is known by.
func threadID(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:12])
}

func boundName(name string) string {
	if utf8.RuneCountInString(name) > maxThreadName {
		return string([]rune(name)[:maxThreadName])
	}
	return name
}

// openThread is an opening the chat holds.
type openThread struct {
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	MessageID int       `json:"id"`
	Pinned    bool      `json:"pinned,omitempty"`
	WantPin   bool      `json:"wantPin,omitempty"`
	PinTries  int       `json:"pinTries,omitempty"`
	At        time.Time `json:"at"`
	// Uncertain: a closing of it timed out in flight in this chat (review
	// 5, S2): the chat may show it over
	Uncertain bool `json:"uncertain,omitempty"`
	// GaveWay: its pin gave way to a newer one (at most maxPinned): it is
	// pinned again when a pinned problem ends (review 6)
	GaveWay bool `json:"gaveWay,omitempty"`
}

// pendingUnpin is a pin Telegram did not take away yet.
type pendingUnpin struct {
	MessageID int `json:"id"`
	Tries     int `json:"tries"`
}

type chatThreads struct {
	Threads map[string]openThread `json:"threads"`
	Unpin   []pendingUnpin        `json:"unpin,omitempty"`
	Used    time.Time             `json:"used"`
}

// savedNotice is what a notice of a thread needs to be told after a
// restart (no error text: the panel has it).
type savedNotice struct {
	Kind     string            `json:"kind"`
	Category string            `json:"category"`
	Severity int               `json:"severity"`
	Closing  bool              `json:"closing,omitempty"`
	At       time.Time         `json:"at"`
	Vars     map[string]string `json:"vars,omitempty"`
	What     string            `json:"what,omitempty"`
	Means    string            `json:"means,omitempty"`
	Ref      string            `json:"ref,omitempty"`
	Thread   string            `json:"thread,omitempty"`
	Resolves string            `json:"resolves,omitempty"`
	Count    int               `json:"count,omitempty"`
	First    time.Time         `json:"first,omitempty"`
}

func saveNotice(nt *notice) *savedNotice {
	if nt == nil {
		return nil
	}
	vars := map[string]string{}
	for k, v := range nt.vars {
		if len(vars) < 12 {
			vars[k] = cleanValue(v, 300)
		}
	}
	return &savedNotice{Kind: nt.kind, Category: nt.category, Severity: int(nt.severity), Closing: nt.closing, At: nt.at,
		Vars: vars, What: nt.what, Means: nt.means, Ref: nt.ref, Thread: boundName(nt.thread), Resolves: boundName(nt.resolves),
		Count: nt.count, First: nt.first}
}

func (s *savedNotice) notice() *notice {
	if s == nil || !knownKind(s.Kind) {
		return nil
	}
	sev := severity(s.Severity)
	if sev < sevInfo || sev > sevCritical {
		sev = texts.Kinds[s.Kind].severity
	}
	vars := s.Vars
	if vars == nil {
		vars = map[string]string{}
	}
	return &notice{kind: s.Kind, category: s.Category, severity: sev, closing: s.Closing, at: s.At, vars: vars,
		what: s.What, means: s.Means, ref: s.Ref, thread: s.Thread, resolves: s.Resolves, count: s.Count, first: s.First,
		key: "n:" + s.Kind}
}

// savedEntry is a thread's state that is not settled yet.
type savedEntry struct {
	Name    string       `json:"name"`
	Kind    string       `json:"kind,omitempty"`
	Open    bool         `json:"open"`
	Since   time.Time    `json:"since"`
	// SeenOpen: review 6, T1 (added field: older files have none)
	SeenOpen time.Time   `json:"seenOpen,omitempty"`
	Fresh    bool        `json:"fresh,omitempty"`
	Opening *savedNotice `json:"opening,omitempty"`
	Closing *savedNotice `json:"closing,omitempty"`
}

type threadFile struct {
	Chats   map[string]*chatThreads `json:"chats"`
	Entries map[string]savedEntry   `json:"entries,omitempty"`
	// Plain: the threads sent as messages of their own (the table was
	// full): their other events, their end included, go the same way
	Plain map[string]time.Time `json:"plain,omitempty"`
	// Farewell: a warning to a chat losing the notifications was waiting
	// when the server stopped (review 4, T4).
	Farewell *farewellMark `json:"farewell,omitempty"`
}

type farewellMark struct {
	What string    `json:"what"`
	By   string    `json:"by"`
	At   time.Time `json:"at"`
}

// threadMemory is safe for use by several goroutines.
type threadMemory struct {
	mu   sync.Mutex
	path string // "" keeps it in memory only
	now  func() time.Time
	data threadFile
	read bool
	// found: the file was there when it was read
	found bool
	// logf says once that the file cannot be written for its size
	logf   func(string)
	tooBig bool
}

func newThreadMemory(path string) *threadMemory {
	return &threadMemory{path: path, now: time.Now}
}

// chatKey ties the memory to a chat: message numbers are per chat.
func chatKey(st settings) string {
	sum := sha256.Sum256([]byte(st.chatID + "\x00" + strconv.Itoa(st.topicID)))
	return hex.EncodeToString(sum[:8])
}

func (m *threadMemory) readLocked() {
	if m.read {
		return
	}
	m.read = true
	if m.path != "" {
		if info, err := os.Stat(m.path); err == nil && info.Size() <= maxThreadsBytes {
			if raw, err := os.ReadFile(m.path); err == nil {
				var data threadFile
				if json.Unmarshal(raw, &data) == nil {
					m.data, m.found = data, true
				}
			}
		}
	}
	if m.data.Chats == nil {
		m.data.Chats = map[string]*chatThreads{}
	}
	if m.data.Entries == nil {
		m.data.Entries = map[string]savedEntry{}
	}
}

// chatLocked gives the memory of a chat.
func (m *threadMemory) chatLocked(key string) *chatThreads {
	m.readLocked()
	c := m.data.Chats[key]
	if c == nil {
		for len(m.data.Chats) >= maxThreadChats {
			oldest := ""
			for k, other := range m.data.Chats {
				if oldest == "" || other.Used.Before(m.data.Chats[oldest].Used) {
					oldest = k
				}
			}
			delete(m.data.Chats, oldest)
		}
		c = &chatThreads{}
		m.data.Chats[key] = c
	}
	if c.Threads == nil {
		c.Threads = map[string]openThread{}
	}
	// review 5, S4: an opening the chat holds is never forgotten by age:
	// only its end (close) or the bound of the table (open, which unpins
	// first) take it away
	c.Used = m.now()
	return c
}

func (m *threadMemory) saveLocked() {
	if m.path == "" {
		return
	}
	raw, err := json.Marshal(m.data)
	if err == nil && len(raw) > maxThreadsBytes {
		// review 5: bounded by its content, never left unwritten: the
		// notices of the entries go first (the level gives them back)
		for id, e := range m.data.Entries {
			e.Opening, e.Closing = nil, nil
			m.data.Entries[id] = e
		}
		raw, err = json.Marshal(m.data)
	}
	if err != nil || len(raw) > maxThreadsBytes {
		if !m.tooBig {
			m.tooBig = true
			if m.logf != nil {
				m.logf("Telegram: the threads file would be over " + strconv.Itoa(maxThreadsBytes>>10) + " KB and is not written")
			}
		}
		return
	}
	m.tooBig = false
	tmp := m.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return
	}
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	if f.Close() != nil || err != nil || os.Rename(tmp, m.path) != nil {
		os.Remove(tmp)
	}
}

// existed reports whether the file was there (and readable) at start.
func (m *threadMemory) existed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readLocked()
	return m.found
}

// get returns what the chat holds of a thread.
func (m *threadMemory) get(st settings, id string) (openThread, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.chatLocked(chatKey(st)).Threads[id]
	return t, ok
}

// all returns the threads the chat holds.
func (m *threadMemory) all(st settings) map[string]openThread {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]openThread{}
	for id, t := range m.chatLocked(chatKey(st)).Threads {
		out[id] = t
	}
	return out
}

// open remembers the message that opened a thread. It returns the pinned
// messages to unpin: an older opening of the same thread, and the oldest
// pins beyond maxPinned.
func (m *threadMemory) open(st settings, id string, name string, kind string, messageID int, wantPin bool) []int {
	if messageID <= 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.chatLocked(chatKey(st))
	unpin := []int{}
	if previous, had := c.Threads[id]; had && previous.Pinned && previous.MessageID != messageID {
		unpin = append(unpin, previous.MessageID)
	}
	c.Threads[id] = openThread{Name: boundName(name), Kind: kind, MessageID: messageID, WantPin: wantPin, At: m.now()}
	ids := make([]string, 0, len(c.Threads))
	for key := range c.Threads {
		ids = append(ids, key)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := c.Threads[ids[i]], c.Threads[ids[j]]
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		return ids[i] < ids[j]
	})
	pins := 0
	for _, key := range ids {
		if c.Threads[key].WantPin {
			pins++
		}
	}
	for _, key := range ids {
		if pins <= maxPinned {
			break
		}
		t := c.Threads[key]
		if t.WantPin && key != id {
			if t.Pinned {
				unpin = append(unpin, t.MessageID)
			}
			t.WantPin, t.Pinned, t.GaveWay = false, false, true
			c.Threads[key] = t
			pins--
		}
	}
	for len(c.Threads) > maxThreads && len(ids) > 0 {
		oldest := ids[0]
		ids = ids[1:]
		if oldest == id {
			continue
		}
		if t := c.Threads[oldest]; t.Pinned {
			unpin = append(unpin, t.MessageID)
		}
		delete(c.Threads, oldest)
	}
	for _, messageID := range unpin {
		m.unpinLaterLocked(c, messageID)
	}
	m.saveLocked()
	return unpin
}

// pinned records whether a thread's pin was taken; a refused one is
// counted, and given up after maxPinTries (gaveUp).
func (m *threadMemory) pinned(st settings, id string, messageID int, ok bool) (gaveUp bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.chatLocked(chatKey(st))
	t, had := c.Threads[id]
	if !had || t.MessageID != messageID {
		return false
	}
	if ok {
		t.Pinned = true
	} else {
		t.PinTries++
		if t.PinTries >= maxPinTries {
			t.WantPin, gaveUp = false, true
		}
	}
	c.Threads[id] = t
	m.saveLocked()
	return gaveUp
}

// toPin lists the openings whose pin is still to be taken.
func (m *threadMemory) toPin(st settings) map[string]openThread {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]openThread{}
	for id, t := range m.chatLocked(chatKey(st)).Threads {
		if t.WantPin && !t.Pinned {
			out[id] = t
		}
	}
	return out
}

// close forgets a thread and returns what it was; its pin is to be taken
// away (it is kept among the unpins until that is done).
func (m *threadMemory) close(st settings, id string) (openThread, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.chatLocked(chatKey(st))
	t, ok := c.Threads[id]
	if ok {
		delete(c.Threads, id)
		if t.Pinned || t.WantPin {
			m.unpinLaterLocked(c, t.MessageID)
			repinLocked(c)
		}
		m.saveLocked()
	}
	return t, ok
}

// repinLocked: a pin is free; the newest problems whose pin gave way are
// pinned again (review 6).
func repinLocked(c *chatThreads) {
	pins := 0
	for _, t := range c.Threads {
		if t.WantPin {
			pins++
		}
	}
	for pins < maxPinned {
		newest := ""
		for key, t := range c.Threads {
			if t.GaveWay && (newest == "" || t.At.After(c.Threads[newest].At) || (t.At.Equal(c.Threads[newest].At) && key > newest)) {
				newest = key
			}
		}
		if newest == "" {
			return
		}
		t := c.Threads[newest]
		t.WantPin, t.GaveWay, t.PinTries = true, false, 0
		c.Threads[newest] = t
		pins++
	}
}

func (m *threadMemory) unpinLaterLocked(c *chatThreads, messageID int) {
	for _, p := range c.Unpin {
		if p.MessageID == messageID {
			return
		}
	}
	if len(c.Unpin) < maxThreads {
		c.Unpin = append(c.Unpin, pendingUnpin{MessageID: messageID})
	}
}

// unpinned records an unpin: done, or refused (counted, given up after
// maxPinTries: gaveUp).
func (m *threadMemory) unpinned(st settings, messageID int, ok bool) (gaveUp bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.unpinnedLocked(m.chatLocked(chatKey(st)), messageID, ok)
}

func (m *threadMemory) unpinnedLocked(c *chatThreads, messageID int, ok bool) (gaveUp bool) {
	kept := c.Unpin[:0]
	for _, p := range c.Unpin {
		if p.MessageID == messageID {
			if !ok {
				p.Tries++
				if p.Tries < maxPinTries {
					kept = append(kept, p)
				} else {
					gaveUp = true
				}
			}
			continue
		}
		kept = append(kept, p)
	}
	c.Unpin = kept
	m.saveLocked()
	return gaveUp
}

// unpins lists the pins to take away.
func (m *threadMemory) unpins(st settings) []pendingUnpin {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]pendingUnpin(nil), m.chatLocked(chatKey(st)).Unpin...)
}

// pinnedIn lists the messages pinned in a chat, and drops them from it
// (the chat is left: review 4, a change of chat).
func (m *threadMemory) leave(st settings) []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readLocked()
	c := m.data.Chats[chatKey(st)]
	if c == nil {
		return nil
	}
	out := []int{}
	for _, t := range c.Threads {
		if t.Pinned || t.WantPin {
			out = append(out, t.MessageID)
		}
	}
	for _, p := range c.Unpin {
		out = append(out, p.MessageID)
	}
	sort.Ints(out)
	delete(m.data.Chats, chatKey(st))
	m.saveLocked()
	return out
}

// saveEntries keeps the threads that are not settled; nil entries are
// forgotten.
func (m *threadMemory) saveEntries(entries map[string]*savedEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readLocked()
	changed := false
	for id, e := range entries {
		if e == nil {
			if _, had := m.data.Entries[id]; had {
				delete(m.data.Entries, id)
				changed = true
			}
			continue
		}
		if len(m.data.Entries) >= maxThreads {
			if _, had := m.data.Entries[id]; !had {
				continue
			}
		}
		if old, had := m.data.Entries[id]; had {
			// written only on a change (review 5); the moment it was last
			// seen open alone is no change (review 6: it goes with the next)
			a, b := old, *e
			a.SeenOpen, b.SeenOpen = time.Time{}, time.Time{}
			ja, _ := json.Marshal(a)
			jb, _ := json.Marshal(b)
			if string(ja) == string(jb) {
				continue
			}
		}
		m.data.Entries[id] = *e
		changed = true
	}
	if changed {
		m.saveLocked()
	}
}

// entries returns the threads that were not settled.
func (m *threadMemory) entries() map[string]savedEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readLocked()
	out := map[string]savedEntry{}
	for id, e := range m.data.Entries {
		out[id] = e
	}
	return out
}

// farewell records (mark non-nil) or forgets a warning waiting to be sent.
func (m *threadMemory) farewell(mark *farewellMark) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readLocked()
	m.data.Farewell = mark
	m.saveLocked()
}

// takeFarewell returns the warning that was waiting when the server
// stopped, and forgets it.
func (m *threadMemory) takeFarewell() *farewellMark {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readLocked()
	mark := m.data.Farewell
	if mark != nil {
		m.data.Farewell = nil
		m.saveLocked()
	}
	return mark
}

func threadsPath() string {
	return filepath.Join(utils.CONFIGFOLDER, threadsFileName)
}

// isPlain reports whether a thread is sent as messages of its own.
func (m *threadMemory) isPlain(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readLocked()
	_, plain := m.data.Plain[id]
	return plain
}

// markPlain records (on) or forgets that a thread goes as messages of its
// own; bounded.
func (m *threadMemory) markPlain(id string, on bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readLocked()
	if m.data.Plain == nil {
		m.data.Plain = map[string]time.Time{}
	}
	if !on {
		if _, had := m.data.Plain[id]; had {
			delete(m.data.Plain, id)
			m.saveLocked()
		}
		return true
	}
	if _, had := m.data.Plain[id]; had {
		return true
	}
	if len(m.data.Plain) >= 4*maxThreads {
		return false
	}
	m.data.Plain[id] = m.now()
	m.saveLocked()
	return true
}

// uncertain records that a closing of a thread timed out in flight in
// this chat.
func (m *threadMemory) uncertain(st settings, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.chatLocked(chatKey(st))
	if t, ok := c.Threads[id]; ok && !t.Uncertain {
		t.Uncertain = true
		c.Threads[id] = t
		m.saveLocked()
	}
}

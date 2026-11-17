package telegram

// Fork addition (plan 48): the messages that opened something that lasts
// (an outage, a router that does not answer, a certificate that ends), so
// that the message saying it is over is sent as a reply to them, and so
// that a critical one pinned while it lasted is unpinned. Bounded: at most
// maxThreads, each for threadAge; kept in a small file beside the state
// file so that a restart in between does not lose them. The file holds
// message numbers and the thread names Cosmos gives (a kind and the name
// of a port, a domain, a container), tied to a hash of the chat.

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

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	threadsFileName = "telegram.threads.json"
	maxThreads      = 256
	threadAge       = 14 * 24 * time.Hour
	maxPinned       = 3
	maxThreadName   = 200
	maxThreadsBytes = 128 << 10
)

type openThread struct {
	MessageID int       `json:"id"`
	Pinned    bool      `json:"pinned,omitempty"`
	At        time.Time `json:"at"`
}

type threadFile struct {
	Chat    string                `json:"chat"`
	Threads map[string]openThread `json:"threads"`
}

// threadMemory is safe for use by several goroutines.
type threadMemory struct {
	mu   sync.Mutex
	path string // "" keeps it in memory only
	now  func() time.Time
	data threadFile
	read bool
}

func newThreadMemory(path string) *threadMemory {
	return &threadMemory{path: path, now: time.Now}
}

// chatKey ties the memory to a chat: message numbers are per chat.
func chatKey(st settings) string {
	sum := sha256.Sum256([]byte(st.chatID + "\x00" + strconv.Itoa(st.topicID)))
	return hex.EncodeToString(sum[:8])
}

// loadLocked reads the file once, and starts over for another chat.
func (m *threadMemory) loadLocked(chat string) {
	if !m.read {
		m.read = true
		if m.path != "" {
			if info, err := os.Stat(m.path); err == nil && info.Size() <= maxThreadsBytes {
				if raw, err := os.ReadFile(m.path); err == nil {
					json.Unmarshal(raw, &m.data)
				}
			}
		}
	}
	if m.data.Threads == nil || m.data.Chat != chat {
		m.data = threadFile{Chat: chat, Threads: map[string]openThread{}}
	}
	now := m.now()
	for name, t := range m.data.Threads {
		if now.Sub(t.At) > threadAge || len(name) > maxThreadName {
			delete(m.data.Threads, name)
		}
	}
}

func (m *threadMemory) saveLocked() {
	if m.path == "" {
		return
	}
	raw, err := json.Marshal(m.data)
	if err != nil {
		return
	}
	tmp := m.path + ".tmp"
	if os.WriteFile(tmp, raw, 0600) != nil {
		os.Remove(tmp)
		return
	}
	if os.Rename(tmp, m.path) != nil {
		os.Remove(tmp)
	}
}

// get returns the message that opened a thread.
func (m *threadMemory) get(st settings, thread string) (openThread, bool) {
	if thread == "" {
		return openThread{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked(chatKey(st))
	t, ok := m.data.Threads[thread]
	return t, ok
}

// open remembers the message that opened a thread. It returns the pinned
// messages that are to be unpinned to stay within maxPinned.
func (m *threadMemory) open(st settings, thread string, messageID int, pinned bool) []int {
	if thread == "" || messageID <= 0 || len(thread) > maxThreadName {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked(chatKey(st))
	unpin := []int{}
	if previous, had := m.data.Threads[thread]; had && previous.Pinned && previous.MessageID != messageID {
		// the thread was opened again: its new message is the one to answer
		// and to keep pinned
		unpin = append(unpin, previous.MessageID)
	}
	m.data.Threads[thread] = openThread{MessageID: messageID, Pinned: pinned, At: m.now()}

	if len(m.data.Threads) > maxThreads || pinned {
		names := make([]string, 0, len(m.data.Threads))
		for name := range m.data.Threads {
			names = append(names, name)
		}
		sort.Slice(names, func(i, j int) bool { return m.data.Threads[names[i]].At.Before(m.data.Threads[names[j]].At) })
		count := 0
		for _, name := range names {
			if m.data.Threads[name].Pinned {
				count++
			}
		}
		for _, name := range names {
			t := m.data.Threads[name]
			if count > maxPinned && t.Pinned && name != thread {
				t.Pinned = false
				m.data.Threads[name] = t
				unpin = append(unpin, t.MessageID)
				count--
			}
		}
		for len(m.data.Threads) > maxThreads {
			oldest := names[0]
			names = names[1:]
			if oldest == thread {
				continue
			}
			if t := m.data.Threads[oldest]; t.Pinned {
				unpin = append(unpin, t.MessageID)
			}
			delete(m.data.Threads, oldest)
		}
	}
	m.saveLocked()
	return unpin
}

// close forgets a thread and returns what it was.
func (m *threadMemory) close(st settings, thread string) (openThread, bool) {
	if thread == "" {
		return openThread{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked(chatKey(st))
	t, ok := m.data.Threads[thread]
	if ok {
		delete(m.data.Threads, thread)
		m.saveLocked()
	}
	return t, ok
}

func threadsPath() string {
	return filepath.Join(utils.CONFIGFOLDER, threadsFileName)
}

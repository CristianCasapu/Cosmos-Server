package telegram

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// orderFake is a stand-in whose answers a test scripts per send: accept,
// refuse (502), or hold the request until released and then refuse (a
// failure in flight). Accepted messages are numbered like Telegram's.
type orderFake struct {
	*fakeTelegram
	mu      sync.Mutex
	script  []string // per sendMessage, in order: "ok", "refuse", "hold"
	sends   int
	release chan struct{}
	pinned  map[int]bool
}

func newOrderFake(t *testing.T, script ...string) *orderFake {
	f := &orderFake{fakeTelegram: newFakeTelegram(t), script: script, release: make(chan struct{}), pinned: map[int]bool{}}
	f.answerWith(func(n int, req fakeRequest) fakeAnswer {
		id, _ := req.body["message_id"].(float64)
		switch {
		case strings.HasSuffix(req.path, "/pinChatMessage"):
			f.mu.Lock()
			f.pinned[int(id)] = true
			f.mu.Unlock()
			return fakeAnswer{status: 200, body: `{"ok":true,"result":true}`}
		case strings.HasSuffix(req.path, "/unpinChatMessage"):
			f.mu.Lock()
			delete(f.pinned, int(id))
			f.mu.Unlock()
			return fakeAnswer{status: 200, body: `{"ok":true,"result":true}`}
		}
		f.mu.Lock()
		step := "ok"
		if f.sends < len(f.script) {
			step = f.script[f.sends]
		}
		f.sends++
		f.mu.Unlock()
		switch step {
		case "refuse":
			return fakeAnswer{status: 502, body: "bad gateway"}
		case "hold":
			<-f.release
			return fakeAnswer{status: 502, body: "bad gateway"}
		}
		return fakeAnswer{status: 200, body: `{"ok":true,"result":{"message_id":` + strconv.Itoa(500+n) + `}}`}
	})
	return f
}

// accepted are the texts Telegram took, in order.
func (f *orderFake) accepted() []string {
	texts := []string{}
	f.mu.Lock()
	script := append([]string(nil), f.script...)
	f.mu.Unlock()
	i := 0
	for _, req := range f.received() {
		if !strings.HasSuffix(req.path, "/sendMessage") {
			continue
		}
		step := "ok"
		if i < len(script) {
			step = script[i]
		}
		i++
		if step == "ok" {
			text, _ := req.body["text"].(string)
			texts = append(texts, text)
		}
	}
	return texts
}

func (f *orderFake) pins() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pinned)
}

// quiet waits until the notifier has nothing of the thread left to do.
func quiet(t *testing.T, h *harness) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		h.notifier.mu.Lock()
		busy := false
		for _, e := range h.notifier.entries {
			busy = busy || e.queued || e.inFlight
		}
		h.notifier.mu.Unlock()
		if !busy {
			time.Sleep(150 * time.Millisecond) // a pin or unpin after the last send
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the thread never settled")
}

func orderHarness(t *testing.T, f *orderFake, tune ...func(*utils.Config)) *harness {
	h := newHarness(t, f.fakeTelegram, false, func(h *harness) {
		h.threads = newThreadMemory("")
		h.limits.backoff = []time.Duration{150 * time.Millisecond, 300 * time.Millisecond}
		h.limits.criticalAge, h.limits.normalAge = time.Hour, time.Hour
	})
	for _, change := range tune {
		h.setConfig(change)
	}
	return h
}

func down(at time.Time) notice {
	nt := sampleNotice("reach.down")
	nt.sample, nt.thread, nt.at = false, "reach:t", at
	return nt
}

func up(at time.Time) notice {
	nt := sampleNotice("reach.restored")
	nt.sample, nt.thread, nt.resolves, nt.at = false, "reach:t", "reach:t", at
	return nt
}

// checkTruth: the last message of the thread matches its truth, and the
// thread is pinned if and only if it is open.
func checkTruth(t *testing.T, name string, f *orderFake, open bool) {
	t.Helper()
	texts := f.accepted()
	last := ""
	if len(texts) > 0 {
		last = texts[len(texts)-1]
	}
	switch {
	case open && !strings.HasPrefix(last, "🔴 "):
		t.Errorf("%s: the thread is open, the last message is %q (all: %q)", name, PreviewLine(last), texts)
	case !open && last != "" && !strings.HasPrefix(last, "🟢 "):
		t.Errorf("%s: the thread is over, the last message is %q (all: %q)", name, PreviewLine(last), texts)
	}
	if pinned := f.pins(); (pinned == 1) != open || pinned > 1 {
		t.Errorf("%s: %d pinned, open %v (all: %q)", name, pinned, open, texts)
	}
}

func TestOrderPerThread(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	cases := []struct {
		name   string
		script []string
		run    func(h *harness, f *orderFake)
		open   bool
		merged bool // the last message says it happened and is over
	}{
		{"delivered, then over", nil, func(h *harness, f *orderFake) {
			h.Notify(down(t0))
			quiet(t, h)
			h.Notify(up(t0.Add(time.Minute)))
		}, false, false},
		{"refused twice, over while waiting", []string{"refuse", "refuse"}, func(h *harness, f *orderFake) {
			h.Notify(down(t0))
			f.waitFor(t, 1)
			h.Notify(up(t0.Add(12 * time.Minute)))
		}, false, true},
		{"over while the opening is on the wire and then fails", []string{"hold"}, func(h *harness, f *orderFake) {
			h.Notify(down(t0))
			f.waitFor(t, 1)
			h.Notify(up(t0.Add(5 * time.Minute)))
			close(f.release)
		}, false, true},
		{"down again while the merged message waits", []string{"refuse", "refuse", "refuse"}, func(h *harness, f *orderFake) {
			h.Notify(down(t0))
			f.waitFor(t, 1)
			h.Notify(up(t0.Add(time.Minute)))
			f.waitFor(t, 2)
			h.Notify(down(t0.Add(2 * time.Minute)))
		}, true, false},
		{"flapping five times, ending over", []string{"refuse", "refuse", "refuse", "refuse"}, func(h *harness, f *orderFake) {
			for i := 0; i < 5; i++ {
				h.Notify(down(t0.Add(time.Duration(2*i) * time.Minute)))
				time.Sleep(40 * time.Millisecond)
				h.Notify(up(t0.Add(time.Duration(2*i+1) * time.Minute)))
				time.Sleep(40 * time.Millisecond)
			}
		}, false, true},
		{"flapping five times, ending open", []string{"refuse", "refuse", "refuse"}, func(h *harness, f *orderFake) {
			for i := 0; i < 5; i++ {
				h.Notify(down(t0.Add(time.Duration(2*i) * time.Minute)))
				time.Sleep(40 * time.Millisecond)
				h.Notify(up(t0.Add(time.Duration(2*i+1) * time.Minute)))
				time.Sleep(40 * time.Millisecond)
			}
			h.Notify(down(t0.Add(20 * time.Minute)))
		}, true, false},
		{"delivered, flapping after, ending over", []string{"ok", "refuse", "refuse"}, func(h *harness, f *orderFake) {
			h.Notify(down(t0))
			quiet(t, h)
			for i := 0; i < 5; i++ {
				h.Notify(up(t0.Add(time.Duration(2*i+1) * time.Minute)))
				time.Sleep(40 * time.Millisecond)
				h.Notify(down(t0.Add(time.Duration(2*i+2) * time.Minute)))
				time.Sleep(40 * time.Millisecond)
			}
			h.Notify(up(t0.Add(30 * time.Minute)))
		}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newOrderFake(t, tc.script...)
			h := orderHarness(t, f)
			tc.run(h, f)
			quiet(t, h)
			checkTruth(t, tc.name, f, tc.open)
			texts := f.accepted()
			if tc.merged {
				if len(texts) != 1 || !strings.Contains(PreviewLine(texts[0]), "(was: Cosmos unreachable") {
					t.Errorf("want one merged message, got %q", texts)
				}
				for _, req := range f.received() {
					if req.body["reply_parameters"] != nil {
						t.Errorf("the merged message replies to a thread: %s", req.raw)
					}
				}
			}
			for _, text := range texts {
				if strings.HasPrefix(text, "🔴 ") && !tc.open && strings.Contains(strings.Join(texts, "|"), "🟢") && strings.Index(strings.Join(texts, "|"), text) > strings.LastIndex(strings.Join(texts, "|"), "🟢") {
					t.Errorf("a stale opening after the end: %q", texts)
				}
			}
		})
	}
}

// A closing whose message is not wanted still ends the thread: unpinned.
func TestAClosingNotWantedStillUnpins(t *testing.T) {
	for name, change := range map[string]func(*utils.Config){
		"kind off":   func(c *utils.Config) { c.Telegram.Kinds = map[string]bool{"reach.restored": false} },
		"info off":   func(c *utils.Config) { c.Telegram.Delivery = map[string]string{"info": "off"} },
		"cap of one": func(c *utils.Config) { c.Telegram.MaxPerMinute = 1 },
	} {
		f := newOrderFake(t)
		h := orderHarness(t, f)
		h.Notify(down(time.Now()))
		quiet(t, h)
		h.setConfig(change)
		h.Notify(up(time.Now()))
		quiet(t, h)
		if f.pins() != 0 {
			t.Errorf("%s: still pinned", name)
		}
		if name == "cap of one" && !strings.HasPrefix(f.accepted()[len(f.accepted())-1], "🟢 ") {
			t.Errorf("%s: the closing was held back by the cap", name)
		}
	}
}

// The merged message rings as the opening would have.
func TestTheMergedMessageRingsLikeItsOpening(t *testing.T) {
	f := newOrderFake(t, "refuse")
	h := orderHarness(t, f)
	h.Notify(down(time.Now().Add(-10 * time.Minute)))
	f.waitFor(t, 1)
	h.Notify(up(time.Now()))
	quiet(t, h)
	last := f.received()[len(f.received())-1]
	if last.body["disable_notification"] == true {
		t.Errorf("the merged message of a critical outage was silent: %s", last.raw)
	}
}

// One thread stuck in its back-off does not hold another thread.
func TestOneThreadDoesNotHoldAnother(t *testing.T) {
	f := newOrderFake(t)
	f.answerWith(func(n int, req fakeRequest) fakeAnswer {
		text, _ := req.body["text"].(string)
		if strings.Contains(text, "Unreachable") {
			return fakeAnswer{status: 502, body: "bad gateway"}
		}
		return fakeAnswer{status: 200, body: `{"ok":true,"result":{"message_id":` + strconv.Itoa(700+n) + `}}`}
	})
	h := orderHarness(t, f)
	h.Notify(down(time.Now()))
	other := sampleNotice("fail2ban.down")
	other.sample, other.thread = false, "fail2ban.down"
	h.Notify(other)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(strings.Join(f.texts(), "|"), "Fail2ban not running") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("the other thread was held: %q", f.texts())
}

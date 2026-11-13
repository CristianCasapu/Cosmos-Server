package security

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestRecorderStoresEventsInTheOrderTheyHappened(t *testing.T) {
	folder := openDatabase(t)
	clock := newTestClock()
	recorder := startRecorder(t, folder, clock.now)

	for i := 0; i < 40; i++ {
		clock.advance(time.Second)
		recorder.Record(Event{Kind: utils.SecContainerAction, Actor: "alice", IP: "192.0.2.10", Target: fmt.Sprintf("app-%02d", i), Details: map[string]string{"action": "start"}})
	}
	recorder.Flush()

	events := all(t)
	if len(events) != 40 {
		t.Fatalf("%d events stored, want 40", len(events))
	}
	for i, event := range events {
		if event.Target != fmt.Sprintf("app-%02d", i) {
			t.Fatalf("event %d is %q: the order was not kept", i, event.Target)
		}
		if i > 0 && (event.ID <= events[i-1].ID || event.Time.Before(events[i-1].Time)) {
			t.Fatalf("event %d: id %d after %d, time %s after %s", i, event.ID, events[i-1].ID, event.Time, events[i-1].Time)
		}
	}
	first := events[0]
	if first.Class != ClassAudit || first.Severity != "low" || first.Outcome != OutcomeOK || first.Count != 1 ||
		first.Actor != "alice" || first.IP != "192.0.2.10" || first.Details["action"] != "start" {
		t.Fatalf("stored event = %+v", first)
	}
}

func TestRecorderRefusesKindsThatAreNotInTheCatalogue(t *testing.T) {
	folder := openDatabase(t)
	recorder := startRecorder(t, folder, time.Now)

	recorder.Record(Event{Kind: "made.up", IP: "192.0.2.10"})
	recorder.Record(Event{Kind: "", IP: "192.0.2.10"})
	recorder.Record(Event{Kind: utils.SecLoginFailed + "\nx", IP: "192.0.2.10"})
	recorder.Flush()

	if events := all(t); len(events) != 0 {
		t.Fatalf("%d events stored for unknown kinds: %+v", len(events), events)
	}
	if recorder.refused.Load() != 3 {
		t.Fatalf("%d refused, want 3", recorder.refused.Load())
	}
	if log := readLog(t, folder); log != "" {
		t.Fatalf("the log file has lines for unknown kinds: %q", log)
	}
}

// A request must never wait for the record: with nobody taking events from
// the queue, Record still returns at once, and what does not fit is counted.
func TestRecorderNeverBlocksAndCountsWhatItDrops(t *testing.T) {
	folder := openDatabase(t)
	clock := newTestClock()
	recorder := newRecorderSized(func() string { return filepath.Join(folder, LogFileName) }, clock.now, 8)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			recorder.Record(Event{Kind: utils.SecContainerAction, Actor: "alice", Target: fmt.Sprintf("app-%d", i)})
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Record blocked on a full queue")
	}
	if dropped := recorder.dropped.Load(); dropped != 42 {
		t.Fatalf("%d events counted as dropped, want 42 (50 recorded, 8 fit)", dropped)
	}

	// once the writer runs, the loss is on the record itself
	go recorder.run()
	recorder.Stop()

	events := all(t)
	if len(events) != 9 {
		t.Fatalf("%d rows, want the 8 that fit and one about the loss: %+v", len(events), events)
	}
	last := events[len(events)-1]
	if last.Kind != utils.SecRecorderDropped || last.Count != 42 {
		t.Fatalf("last row = %+v, want recorder.dropped with count 42", last)
	}
}

// Nothing vanishes: under parallel load, with limits hit, every event is
// either a row of its own or counted in a summary row. Run with -race.
func TestRecorderAccountsForEveryEventUnderParallelLoad(t *testing.T) {
	folder := openDatabase(t)
	recorder := startRecorder(t, folder, time.Now)

	// a subscriber is there too, for the race detector
	var delivered atomic.Int64
	defer recorder.subs.subscribe("counter", func(event Event) { delivered.Add(int64(event.Count)) })()

	const workers, each = 6, 150
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				switch i % 3 {
				case 0:
					recorder.Record(Event{Kind: utils.SecLoginFailed, IP: fmt.Sprintf("203.0.113.%d", w+1), Target: "admin", Details: map[string]string{"reason": "wrong_password"}})
				case 1:
					recorder.Record(Event{Kind: utils.SecScanProbe, IP: fmt.Sprintf("198.51.100.%d", i%200+1), Path: "/.env"})
				default:
					recorder.Record(Event{Kind: utils.SecContainerAction, Actor: fmt.Sprintf("user%d", w), Target: "app"})
				}
				if i%50 == 0 {
					// readers and flushes in between
					if _, err := List(Filter{Class: ClassIncident}, 0, 10); err != nil {
						t.Error("List:", err)
					}
					recorder.Flush()
				}
			}
		}(w)
	}
	wg.Wait()
	recorder.Stop() // writes the queue and every pending summary

	total := 0
	perWorker := map[string]int{}
	for _, event := range all(t) {
		total += event.Count
		if event.Kind == utils.SecContainerAction && event.Details["summary"] == "" {
			perWorker[event.Actor]++
		}
	}
	if total != workers*each {
		t.Fatalf("the rows stand for %d events, %d were recorded", total, workers*each)
	}
	if dropped := recorder.dropped.Load(); dropped != 0 {
		t.Fatalf("%d events dropped although the writer was running", dropped)
	}
	for w := 0; w < workers; w++ {
		if got := perWorker[fmt.Sprintf("user%d", w)]; got != each/3 {
			t.Fatalf("user%d has %d audit rows, want %d: audit entries below their limit must all be there", w, got, each/3)
		}
	}
}

func TestRecorderKeepsEventsUntilTheDatabaseOpens(t *testing.T) {
	folder := useFolder(t)
	utils.CloseMetricsDatabase()
	clock := newTestClock()
	recorder := startRecorder(t, folder, clock.now)

	seen := make(chan Event, 10)
	defer recorder.subs.subscribe("early", func(event Event) { seen <- event })()

	for i := 0; i < 3; i++ {
		clock.advance(time.Second)
		recorder.Record(Event{Kind: utils.SecLoginFailed, IP: "203.0.113.7", Target: fmt.Sprintf("try%d", i)})
	}
	recorder.Flush()

	// no database: the Fail2ban file and the subscribers have them already
	if lines := strings.Count(readLog(t, folder), "\n"); lines != 3 {
		t.Fatalf("%d lines in the log file without a database, want 3", lines)
	}
	for i := 0; i < 3; i++ {
		select {
		case event := <-seen:
			if event.ID != 0 {
				t.Fatalf("an event that is not stored has the id %d", event.ID)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the subscriber did not get the events while the database was closed")
		}
	}

	if err := utils.InitMetricsDatabase(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(utils.CloseMetricsDatabase)
	clock.advance(time.Second)
	recorder.Record(Event{Kind: utils.SecLoginFailed, IP: "203.0.113.7", Target: "try3"})
	recorder.Flush()

	events := all(t)
	if len(events) != 4 {
		t.Fatalf("%d events stored once the database opened, want 4", len(events))
	}
	for i, event := range events {
		if event.Target != fmt.Sprintf("try%d", i) {
			t.Fatalf("event %d is %q: the earlier ones must come first", i, event.Target)
		}
	}
	// stored once, delivered once
	if lines := strings.Count(readLog(t, folder), "\n"); lines != 4 {
		t.Fatalf("%d lines in the log file, want 4", lines)
	}
}

func TestRecorderBoundsWhatWaitsForTheDatabase(t *testing.T) {
	folder := useFolder(t)
	utils.CloseMetricsDatabase()
	recorder := newRecorder(func() string { return filepath.Join(folder, LogFileName) }, time.Now)

	batch := make([]Event, 0, 300)
	for i := 0; i < 300; i++ {
		batch = append(batch, clean(Event{Kind: utils.SecContainerAction, Actor: "alice"}, catalogue[utils.SecContainerAction], time.Now()))
	}
	for round := 0; round < 10; round++ {
		recorder.write(append([]Event(nil), batch...))
		if len(recorder.unsaved) > maxUnsaved {
			t.Fatalf("%d events wait for the database, more than the bound of %d", len(recorder.unsaved), maxUnsaved)
		}
	}
	if kept, dropped := len(recorder.unsaved), int(recorder.dropped.Load()); kept+dropped != 3000 || kept != maxUnsaved {
		t.Fatalf("%d kept and %d counted as dropped, want %d kept and 3000 in all", kept, dropped, maxUnsaved)
	}
}

func TestRecorderFollowsADatabaseThatIsOpenedAgain(t *testing.T) {
	first := openDatabase(t)
	recorder := startRecorder(t, first, time.Now)

	recorder.Record(Event{Kind: utils.SecLoginSuccess, Actor: "alice", IP: "192.0.2.10"})
	recorder.Flush()
	if events := all(t); len(events) != 1 {
		t.Fatalf("%d events in the first database, want 1", len(events))
	}

	// another folder, as after a change of the configuration folder: the
	// table does not exist there yet
	utils.CloseMetricsDatabase()
	useFolder(t)
	if err := utils.InitMetricsDatabase(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(utils.CloseMetricsDatabase)

	recorder.Record(Event{Kind: utils.SecLoginSuccess, Actor: "bob", IP: "192.0.2.11"})
	recorder.Flush()
	events := all(t)
	if len(events) != 1 || events[0].Actor != "bob" {
		t.Fatalf("events in the second database = %+v, want the one of bob", events)
	}
}

var logLinePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[+-]\d{4} cosmos-security kind=[a-z.]+ ip=[0-9a-f.:]+ sev=(info|low|medium|high) n=\d+$`)

func TestLogFileHasOneFixedLinePerIncidentWithAnAddress(t *testing.T) {
	folder := openDatabase(t)
	clock := newTestClock()
	recorder := startRecorder(t, folder, clock.now)

	hostile := "admin\n2026-11-13T10:00:00+0000 cosmos-security kind=auth.login.failed ip=198.51.100.1 sev=medium n=1"
	recorder.Record(Event{Kind: utils.SecLoginFailed, IP: "203.0.113.7", Target: hostile, Host: hostile, Path: "/" + hostile,
		Actor: hostile, Method: "POST", Details: map[string]string{"reason": hostile}})
	recorder.Record(Event{Kind: utils.SecScanProbe, IP: "2001:db8::7", Path: "/.env"})
	recorder.Record(Event{Kind: utils.SecLoginFailed, IP: "::ffff:198.51.100.20"})
	// none of these belongs in the file
	recorder.Record(Event{Kind: utils.SecLoginFailed, IP: "127.0.0.1"})                    // loopback
	recorder.Record(Event{Kind: utils.SecLoginFailed, IP: "::1"})                          // loopback
	recorder.Record(Event{Kind: utils.SecLoginFailed, IP: "0.0.0.0"})                      // no address
	recorder.Record(Event{Kind: utils.SecLoginFailed, IP: "fe80::1"})                      // link-local
	recorder.Record(Event{Kind: utils.SecLoginFailed, IP: "169.254.1.1"})                  // link-local
	recorder.Record(Event{Kind: utils.SecLoginFailed, IP: ""})                             // none
	recorder.Record(Event{Kind: utils.SecLoginFailed, IP: "203.0.113.9 ip=198.51.100.1"})  // not an address
	recorder.Record(Event{Kind: utils.SecShieldStrike, Actor: "alice"})                    // a user, not an address
	recorder.Record(Event{Kind: utils.SecLoginSuccess, Actor: "alice", IP: "203.0.113.8"}) // audit
	recorder.Record(Event{Kind: utils.SecHostTerminal, Actor: "alice", IP: "203.0.113.8"}) // audit
	recorder.Flush()

	stamp := clock.now().Format(logTimeFormat)
	want := stamp + " cosmos-security kind=auth.login.failed ip=203.0.113.7 sev=medium n=1\n" +
		stamp + " cosmos-security kind=scan.probe ip=2001:db8::7 sev=medium n=1\n" +
		stamp + " cosmos-security kind=auth.login.failed ip=198.51.100.20 sev=medium n=1\n"
	got := readLog(t, folder)
	if got != want {
		t.Fatalf("log file:\n%s\nwant:\n%s", got, want)
	}
	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		if !logLinePattern.MatchString(line) {
			t.Errorf("line does not have the documented format: %q", line)
		}
	}

	info, err := os.Stat(filepath.Join(folder, LogFileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("the log file has mode %o, want 600", info.Mode().Perm())
	}

	// the database has all of them, the text cleaned
	events := all(t)
	if len(events) != 13 {
		t.Fatalf("%d events stored, want 13", len(events))
	}
	for _, field := range []string{events[0].Target, events[0].Host, events[0].Path, events[0].Actor, events[0].Details["reason"]} {
		if strings.ContainsAny(field, "\r\n") {
			t.Errorf("a stored field still has a line break: %q", field)
		}
	}
}

func TestLogFileSummaryLineCarriesTheCount(t *testing.T) {
	folder := openDatabase(t)
	clock := newTestClock()
	recorder := newRecorder(func() string { return filepath.Join(folder, LogFileName) }, clock.now)
	go recorder.run()

	for i := 0; i < 47; i++ {
		recorder.Record(Event{Kind: utils.SecLoginFailed, IP: "203.0.113.7", Target: "admin"})
	}
	recorder.Stop() // the pending summary is written on the way out

	lines := strings.Split(strings.TrimSuffix(readLog(t, folder), "\n"), "\n")
	if len(lines) != 11 {
		t.Fatalf("%d lines for a flood of 47 from one address, want 10 and a summary:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.HasSuffix(lines[10], " kind=auth.login.failed ip=203.0.113.7 sev=medium n=37") {
		t.Fatalf("summary line = %q, want n=37", lines[10])
	}
	events := all(t)
	if summary := events[len(events)-1]; summary.Count != 37 || summary.Details["summary"] != "1" || summary.IP != "203.0.113.7" {
		t.Fatalf("summary row = %+v", summary)
	}
}

func TestLogFileRotatesBySizeAndStaysPrivate(t *testing.T) {
	folder := t.TempDir()
	path := filepath.Join(folder, LogFileName)
	log := newLogFile(func() string { return path }, 2000)

	event := clean(Event{Kind: utils.SecLoginFailed, IP: "203.0.113.7"}, catalogue[utils.SecLoginFailed], time.Now())
	line := logLine(event)
	written := 0
	for i := 0; i < 100; i++ {
		if err := log.writeEvents([]Event{event, event}); err != nil {
			t.Fatal(err)
		}
		written += 2
		for _, name := range []string{path, path + ".1"} {
			info, err := os.Stat(name)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() > 2000 {
				t.Fatalf("%s is %d bytes, more than the limit of 2000", filepath.Base(name), info.Size())
			}
			if info.Mode().Perm() != 0600 {
				t.Fatalf("%s has mode %o, want 600", filepath.Base(name), info.Mode().Perm())
			}
		}
	}

	entries, err := os.ReadDir(folder)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("%d files after rotating many times, want the file and one old one", len(entries))
	}
	// whole lines only, in both files
	for _, name := range []string{path, path + ".1"} {
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if len(content) == 0 || len(content)%len(line) != 0 || strings.Count(string(content), line)*len(line) != len(content) {
			t.Fatalf("%s does not consist of whole lines", filepath.Base(name))
		}
	}
}

func TestLogFileIsStartedAgainWhenItWasMovedAway(t *testing.T) {
	folder := t.TempDir()
	path := filepath.Join(folder, LogFileName)
	log := newLogFile(func() string { return path }, logMaxBytes)
	event := clean(Event{Kind: utils.SecLoginFailed, IP: "203.0.113.7"}, catalogue[utils.SecLoginFailed], time.Now())

	if err := log.writeEvents([]Event{event}); err != nil {
		t.Fatal(err)
	}
	// what logrotate or an administrator may do
	if err := os.Rename(path, path+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := log.writeEvents([]Event{event}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := log.writeEvents([]Event{event}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != logLine(event) {
		t.Fatalf("the new file has %q, want the one line written after it was removed", content)
	}

	// an existing file with a wider mode is closed down
	other := filepath.Join(folder, "other.log")
	if err := os.WriteFile(other, nil, 0644); err != nil {
		t.Fatal(err)
	}
	wide := newLogFile(func() string { return other }, logMaxBytes)
	if err := wide.writeEvents([]Event{event}); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(other); info.Mode().Perm() != 0600 {
		t.Fatalf("an existing file kept the mode %o", info.Mode().Perm())
	}
}

func TestSubscribersCannotHoldUpOrBreakTheRecorder(t *testing.T) {
	folder := openDatabase(t)
	recorder := startRecorder(t, folder, time.Now)

	var mu sync.Mutex
	good := []Event{}
	release := make(chan struct{})
	var panics atomic.Int64

	defer recorder.subs.subscribe("blocked", func(Event) { <-release })()
	defer recorder.subs.subscribe("panicking", func(event Event) {
		panics.Add(1)
		panic("subscriber failure")
	})()
	defer recorder.subs.subscribe("tampering", func(event Event) {
		if event.Details != nil {
			event.Details["action"] = "tampered"
		}
	})()
	defer recorder.subs.subscribe("good", func(event Event) {
		mu.Lock()
		good = append(good, event)
		mu.Unlock()
	})()
	defer close(release)

	const events = 300
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < events; i++ {
			recorder.Record(Event{Kind: utils.SecContainerAction, Actor: fmt.Sprintf("user%d", i%20), Target: fmt.Sprintf("app-%03d", i), Details: map[string]string{"action": "start"}})
			if i%10 == 9 {
				recorder.Flush() // keeps the good subscriber's queue from overflowing
				time.Sleep(time.Millisecond)
			}
		}
		recorder.Flush()
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("a subscriber that never returns held up the recorder")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(good)
		mu.Unlock()
		if n == events && panics.Load() == events {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(good) != events {
		t.Fatalf("the well-behaved subscriber got %d of %d events", len(good), events)
	}
	if panics.Load() != events {
		t.Fatalf("the panicking subscriber was called %d times, want %d: a panic must not end its subscription", panics.Load(), events)
	}
	for i, event := range good {
		if event.Target != fmt.Sprintf("app-%03d", i) || event.ID == 0 {
			t.Fatalf("event %d delivered as %q with id %d: out of order or not stored", i, event.Target, event.ID)
		}
		if event.Details["action"] != "start" {
			t.Fatalf("event %d: one subscriber saw what another changed (%q)", i, event.Details["action"])
		}
	}
	if missed := recorder.subs.missed()["blocked"]; missed == 0 {
		t.Fatal("the blocked subscriber is not reported as having missed events")
	}
	if stored := all(t); len(stored) != events || stored[0].Details["action"] != "start" {
		t.Fatalf("%d events stored, want %d untouched", len(stored), events)
	}
}

func TestUnsubscribeEndsDelivery(t *testing.T) {
	folder := openDatabase(t)
	recorder := startRecorder(t, folder, time.Now)

	var got atomic.Int64
	unsubscribe := recorder.subs.subscribe("once", func(Event) { got.Add(1) })

	recorder.Record(Event{Kind: utils.SecLoginSuccess, Actor: "alice"})
	recorder.Flush()
	deadline := time.Now().Add(5 * time.Second)
	for got.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	unsubscribe()
	unsubscribe() // twice is harmless

	recorder.Record(Event{Kind: utils.SecLoginSuccess, Actor: "alice"})
	recorder.Flush()
	time.Sleep(50 * time.Millisecond)
	if got.Load() != 1 {
		t.Fatalf("%d events delivered, want only the one before unsubscribing", got.Load())
	}
}

func TestStartConnectsTheHooksOfTheRestOfTheServer(t *testing.T) {
	folder := openDatabase(t)
	startServerRecorder(t)
	Start() // twice is harmless

	utils.RecordSecurityEvent(Event{Kind: utils.SecLoginFailed, IP: "203.0.113.77", Target: "admin"})
	Flush()

	events := all(t)
	if len(events) != 1 || events[0].Kind != utils.SecLoginFailed || events[0].IP != "203.0.113.77" {
		t.Fatalf("events = %+v, want the one recorded through utils", events)
	}
	if !strings.Contains(readLog(t, folder), "kind=auth.login.failed ip=203.0.113.77 ") {
		t.Fatalf("the server's recorder does not write to %s in the configuration folder", LogFileName)
	}
}

package proxy

// Fork addition: tests of fork_event_sample.go.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

type writtenRow struct {
	eventId, object string
	data            map[string]interface{}
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func testSampler(maxKeys int) (*eventSampler, *fakeClock, func() []writtenRow) {
	var mu sync.Mutex
	rows := []writtenRow{}
	clock := &fakeClock{t: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	s := newEventSampler(time.Minute, maxKeys, func(eventId, label, level, object string, data map[string]interface{}) {
		mu.Lock()
		rows = append(rows, writtenRow{eventId, object, data})
		mu.Unlock()
	})
	s.now = clock.now
	return s, clock, func() []writtenRow {
		mu.Lock()
		defer mu.Unlock()
		return append([]writtenRow(nil), rows...)
	}
}

func observeN(s *eventSampler, eventId string, n int) {
	s.observe(eventId, "label", "info", "route@app", map[string]interface{}{"n": n})
}

func TestSampledEventsWriteOneRowPerKindAndMinute(t *testing.T) {
	s, clock, rows := testSampler(100)

	for i := 1; i <= 100; i++ {
		observeN(s, "cosmos.proxy.response.app.success", i)
		clock.advance(500 * time.Millisecond)
	}
	// another kind is written at once, whatever the first one does
	observeN(s, "cosmos.proxy.response.app.error", 1)

	got := rows()
	if len(got) != 2 || got[0].data["n"] != 1 || got[0].data["requests"] != 1 || got[1].eventId != "cosmos.proxy.response.app.error" {
		t.Fatalf("rows within the first minute = %+v, want the first request of each kind", got)
	}

	s.flush(false)
	if len(rows()) != 2 {
		t.Fatalf("a flush before the minute is over wrote %+v", rows()[2:])
	}

	clock.advance(10 * time.Second) // 60 s after the first row
	s.flush(false)
	got = rows()
	if len(got) != 3 {
		t.Fatalf("rows after the minute = %d, want the counted requests in one row", len(got))
	}
	last := got[2]
	if last.eventId != "cosmos.proxy.response.app.success" || last.data["requests"] != 99 || last.data["n"] != 100 || last.data["since"] != "2026-11-14T12:00:00Z" {
		t.Fatalf("summary row = %+v, want 99 requests since the second one with the details of the latest", last)
	}
}

func TestSampledEventsNextRequestCarriesTheCount(t *testing.T) {
	s, clock, rows := testSampler(100)
	for i := 1; i <= 5; i++ {
		observeN(s, "cosmos.api.token.used", i)
	}
	clock.advance(time.Minute)
	observeN(s, "cosmos.api.token.used", 6)

	got := rows()
	if len(got) != 2 || got[1].data["requests"] != 5 || got[1].data["n"] != 6 {
		t.Fatalf("rows = %+v, want the sixth request written with the four counted before it", got)
	}
	// nothing is counted twice
	clock.advance(time.Minute)
	s.flush(true)
	if len(rows()) != 2 {
		t.Fatalf("a flush after the count was written wrote %+v", rows()[2:])
	}
}

func TestSampledEventsForgetQuietKinds(t *testing.T) {
	s, clock, _ := testSampler(100)
	for i := 0; i < 50; i++ {
		observeN(s, fmt.Sprintf("cosmos.proxy.response.app%d.success", i), 1)
	}
	if s.size() != 50 {
		t.Fatalf("held %d kinds, want 50", s.size())
	}
	clock.advance(time.Minute)
	s.flush(false)
	if s.size() != 0 {
		t.Fatalf("held %d kinds after a quiet minute, want none", s.size())
	}
}

func TestSampledEventsHoldABoundedNumberOfKinds(t *testing.T) {
	s, _, rows := testSampler(2)
	for i := 0; i < 10; i++ {
		observeN(s, fmt.Sprintf("kind%d", i%5), i)
	}
	if s.size() != 2 {
		t.Fatalf("held %d kinds, want the bound of 2", s.size())
	}
	// past the bound a request is written as its own row: nothing is lost
	got := rows()
	if len(got) != 2+6 {
		t.Fatalf("wrote %d rows, want one per held kind and one per request past the bound", len(got))
	}
}

func TestSampledEventsCountEveryRequestUnderLoad(t *testing.T) {
	s, clock, rows := testSampler(100)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				observeN(s, fmt.Sprintf("kind%d", (g+i)%3), i)
				if i%100 == 0 {
					clock.advance(15 * time.Second)
					s.flush(false)
				}
			}
		}(g)
	}
	wg.Wait()
	s.flush(true)

	total := 0
	for _, row := range rows() {
		total += row.data["requests"].(int)
	}
	if total != 16*500 {
		t.Fatalf("rows stand for %d requests, want %d", total, 16*500)
	}
}

// Through the SmartShield and into the events table: a burst of answers on
// one URL leaves one row per outcome, and their count once the minute is over.
func TestProxiedRequestsLeaveOneEventRowPerMinute(t *testing.T) {
	resetShieldState(t)
	prevFolder := utils.CONFIGFOLDER
	utils.CONFIGFOLDER = t.TempDir() + "/"
	if err := utils.InitMetricsDatabase(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// the metrics of the requests are pushed from goroutines
		time.Sleep(100 * time.Millisecond)
		utils.CloseMetricsDatabase()
		utils.CONFIGFOLDER = prevFolder
	})

	route := utils.ProxyRouteConfig{Name: "fork-sampled-events"}
	h := SmartShieldMiddleware("fork-sampled-events", route)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	for i := 0; i < 200; i++ {
		path := "/"
		if i%10 == 0 {
			path = "/missing"
		}
		r := httptest.NewRequest("GET", "http://app.example"+path, nil)
		r.RemoteAddr = "192.168.1.30:40000"
		h.ServeHTTP(httptest.NewRecorder(), r)
	}

	count := func() map[string][]int {
		events, _, err := utils.QueryEvents(utils.EventQuery{
			From:   time.Now().Add(-time.Hour),
			To:     time.Now().Add(time.Hour),
			Filter: map[string]interface{}{"object": "route@fork-sampled-events"},
			Limit:  100,
		})
		if err != nil {
			t.Fatal(err)
		}
		byId := map[string][]int{}
		for _, event := range events {
			n, _ := event.Data["requests"].(float64)
			byId[event.EventId] = append(byId[event.EventId], int(n))
		}
		return byId
	}

	got := count()
	if len(got["cosmos.proxy.response.fork-sampled-events.success"]) != 1 || len(got["cosmos.proxy.response.fork-sampled-events.error"]) != 1 {
		t.Fatalf("rows after 200 requests = %v, want one per outcome", got)
	}

	requestEvents.flush(true)
	got = count()
	sum := func(ns []int) (total int) {
		for _, n := range ns {
			total += n
		}
		return
	}
	if sum(got["cosmos.proxy.response.fork-sampled-events.success"]) != 180 || sum(got["cosmos.proxy.response.fork-sampled-events.error"]) != 20 {
		t.Fatalf("rows stand for %v, want 180 answers and 20 errors", got)
	}
}

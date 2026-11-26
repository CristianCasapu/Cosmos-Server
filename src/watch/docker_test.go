package watch

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDockerNotAnswering(t *testing.T) {
	r := record(t)
	start := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)
	w := &dockerWatch{started: start}
	down := errors.New("dial unix /var/run/docker.sock: connect: connection refused")

	// at Cosmos' own start Docker may come up later: nothing in the grace
	for s := 0; s < 170; s += 30 {
		w.step(down, start.Add(time.Duration(s)*time.Second))
	}
	if len(r.written) != 0 {
		t.Fatalf("told during the start: %s", r.kinds())
	}
	w.step(nil, start.Add(4*time.Minute))
	// a hiccup later: nothing
	at := start.Add(10 * time.Minute)
	w.step(down, at)
	w.step(nil, at.Add(30*time.Second))
	if len(r.written) != 0 {
		t.Fatalf("a hiccup is told: %s", r.kinds())
	}
	// a real outage: told once after two minutes, closed when it answers
	for s := 0; s <= 300; s += 30 {
		w.step(down, at.Add(time.Minute+time.Duration(s)*time.Second))
	}
	w.step(nil, at.Add(9*time.Minute))
	if r.kinds() != "docker.down,docker.restored" || r.written[0].Fork.What.Vars["reason"] != "the connection was refused" || r.written[1].Fork.Resolves != dockerThread {
		t.Fatalf("%s %+v", r.kinds(), r.written)
	}
}

func TestADockerThatHangsCountsAsDown(t *testing.T) {
	r := record(t)
	w := &dockerWatch{started: time.Now().Add(-time.Hour)}
	release := make(chan struct{})
	pings := 0
	ping := func(time.Duration) error { pings++; <-release; return nil }
	clock := time.Now()
	now := func() time.Time { return clock }
	first := make(chan struct{})
	go func() { w.check(ping, now); close(first) }() // hangs
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 6; i++ {
		clock = clock.Add(30 * time.Second)
		w.check(ping, now) // no second ping while one hangs: a failure each time
	}
	close(release)
	<-first // its late answer closes the outage before the test ends
	if pings != 1 || !strings.HasPrefix(r.kinds(), "docker.down") {
		t.Errorf("%d pings, %s", pings, r.kinds())
	}
}

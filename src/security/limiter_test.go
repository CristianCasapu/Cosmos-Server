package security

import (
	"fmt"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func loginFailure(ip string) Event {
	return Event{Kind: utils.SecLoginFailed, IP: ip, Count: 1}
}

func TestLimiterLetsABurstThroughThenCounts(t *testing.T) {
	clock := newTestClock()
	l := newLimiter()
	kind := catalogue[utils.SecLoginFailed]

	allowed := 0
	for i := 0; i < 100; i++ {
		if l.allow(loginFailure("203.0.113.7"), kind, clock.now()) {
			allowed++
		}
	}
	if allowed != int(incidentLimits.sourceBurst) {
		t.Fatalf("%d of 100 events from one address were recorded, want %d", allowed, int(incidentLimits.sourceBurst))
	}

	// another address is not held back by the first one
	if !l.allow(loginFailure("198.51.100.9"), kind, clock.now()) {
		t.Fatal("an event from another address was held back by the flood of the first")
	}
	// and another kind from the flooding address is not either
	if !l.allow(Event{Kind: utils.SecScanProbe, IP: "203.0.113.7", Count: 1}, catalogue[utils.SecScanProbe], clock.now()) {
		t.Fatal("an event of another kind was held back")
	}

	// nothing is due while the flood goes on and is younger than summaryEvery
	if rows := l.summaries(clock.now(), false); len(rows) != 0 {
		t.Fatalf("%d summaries right away, want none yet", len(rows))
	}

	// once the address is quiet, one row says how many more there were
	clock.advance(summaryQuiet)
	rows := l.summaries(clock.now(), false)
	if len(rows) != 1 {
		t.Fatalf("%d summaries, want 1: %+v", len(rows), rows)
	}
	summary := rows[0]
	if summary.Kind != utils.SecLoginFailed || summary.IP != "203.0.113.7" || summary.Count != 90 || summary.Details["summary"] != "1" {
		t.Fatalf("summary = %+v, want 90 more failed logins from 203.0.113.7", summary)
	}
	// and it is said once
	if rows := l.summaries(clock.now(), false); len(rows) != 0 {
		t.Fatalf("the summary was repeated: %+v", rows)
	}
}

func TestLimiterRefillsOverTime(t *testing.T) {
	clock := newTestClock()
	l := newLimiter()
	kind := catalogue[utils.SecLoginFailed]

	for i := 0; i < 50; i++ {
		l.allow(loginFailure("203.0.113.7"), kind, clock.now())
	}
	// one a minute afterwards: a slow attack is recorded attempt by attempt
	for minute := 0; minute < 5; minute++ {
		clock.advance(time.Minute)
		if !l.allow(loginFailure("203.0.113.7"), kind, clock.now()) {
			t.Fatalf("minute %d: the one event a minute was not recorded", minute)
		}
		if l.allow(loginFailure("203.0.113.7"), kind, clock.now()) {
			t.Fatalf("minute %d: a second event in the same minute was recorded", minute)
		}
	}
	// after a long pause the whole burst is there again, and no more
	clock.advance(time.Hour)
	allowed := 0
	for i := 0; i < 50; i++ {
		if l.allow(loginFailure("203.0.113.7"), kind, clock.now()) {
			allowed++
		}
	}
	if allowed != int(incidentLimits.sourceBurst) {
		t.Fatalf("%d events after an hour of quiet, want the burst of %d", allowed, int(incidentLimits.sourceBurst))
	}
}

func TestLimiterSummarisesAnOngoingFloodEveryFiveMinutes(t *testing.T) {
	clock := newTestClock()
	l := newLimiter()
	kind := catalogue[utils.SecLoginFailed]

	summaries, summarised := 0, 0
	for second := 0; second < 20*60; second++ {
		clock.advance(time.Second)
		for i := 0; i < 3; i++ {
			l.allow(loginFailure("203.0.113.7"), kind, clock.now())
		}
		if second%15 == 0 {
			for _, row := range l.summaries(clock.now(), false) {
				summaries++
				summarised += row.Count
			}
		}
	}
	if summaries < 3 || summaries > 5 {
		t.Fatalf("%d summaries in 20 minutes of flood, want one about every five minutes", summaries)
	}
	if summarised < 2000 {
		t.Fatalf("the summaries stand for %d events, far fewer than the 3600 sent", summarised)
	}
}

func TestLimiterBoundsAKindFloodedFromManyAddresses(t *testing.T) {
	clock := newTestClock()
	l := newLimiter()
	kind := catalogue[utils.SecScanProbe]

	allowed := 0
	for i := 0; i < 5000; i++ {
		ip := fmt.Sprintf("198.18.%d.%d", i/250, i%250+1)
		if l.allow(Event{Kind: utils.SecScanProbe, IP: ip, Count: 1}, kind, clock.now()) {
			allowed++
		}
	}
	if allowed != int(incidentLimits.kindBurst) {
		t.Fatalf("%d events of one kind recorded at once, want the kind's burst of %d", allowed, int(incidentLimits.kindBurst))
	}

	clock.advance(summaryQuiet)
	rows := l.summaries(clock.now(), false)
	if len(rows) != 1 {
		t.Fatalf("%d summaries, want one for the kind: %+v", len(rows), rows)
	}
	if rows[0].IP != "" || rows[0].Count != 5000-allowed || rows[0].Details["sources"] != "1000+" {
		t.Fatalf("summary = %+v, want %d events from 1000+ sources and no address", rows[0], 5000-allowed)
	}

	// another kind has its own budget
	if !l.allow(loginFailure("198.18.0.1"), catalogue[utils.SecLoginFailed], clock.now()) {
		t.Fatal("a flood of one kind used up the budget of another")
	}
}

func TestLimiterCountsEverything(t *testing.T) {
	clock := newTestClock()
	l := newLimiter()
	kind := catalogue[utils.SecShieldBlocked]

	sent, allowed, summarised := 0, 0, 0
	for round := 0; round < 400; round++ {
		clock.advance(700 * time.Millisecond)
		for i := 0; i < 40; i++ {
			ip := fmt.Sprintf("198.18.%d.%d", (round*7+i)%200, i%13+1)
			sent++
			if l.allow(Event{Kind: utils.SecShieldBlocked, IP: ip, Count: 1}, kind, clock.now()) {
				allowed++
			}
		}
		if round%20 == 0 {
			for _, row := range l.summaries(clock.now(), false) {
				summarised += row.Count
			}
		}
	}
	for _, row := range l.summaries(clock.now(), true) {
		summarised += row.Count
	}
	if allowed+summarised != sent {
		t.Fatalf("%d recorded + %d summarised = %d, but %d events were sent: some vanished", allowed, summarised, allowed+summarised, sent)
	}
}

func TestLimiterMemoryIsBounded(t *testing.T) {
	clock := newTestClock()
	l := newLimiter()
	l.max = 500
	kind := catalogue[utils.SecScanProbe]

	sent, allowed := 0, 0
	for i := 0; i < 20000; i++ {
		ip := fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255)
		for j := 0; j < 12; j++ { // enough for each address to owe a summary
			sent++
			if l.allow(Event{Kind: utils.SecScanProbe, IP: ip, Count: 1}, kind, clock.now()) {
				allowed++
			}
		}
		if size := l.size(); size > l.max {
			t.Fatalf("the limiter remembers %d sources, more than its bound of %d", size, l.max)
		}
	}
	summarised := 0
	for _, row := range l.summaries(clock.now(), true) {
		summarised += row.Count
	}
	if allowed+summarised != sent {
		t.Fatalf("%d recorded + %d summarised, but %d sent: making room lost events", allowed, summarised, sent)
	}
}

func TestLimiterTreatsAnIPv6PrefixAsOneSource(t *testing.T) {
	clock := newTestClock()
	l := newLimiter()
	kind := catalogue[utils.SecLoginFailed]

	allowed := 0
	for i := 0; i < 200; i++ {
		if l.allow(loginFailure(fmt.Sprintf("2001:db8:1:2::%x", i+1)), kind, clock.now()) {
			allowed++
		}
	}
	if allowed != int(incidentLimits.sourceBurst) {
		t.Fatalf("%d events from one /64 recorded, want %d: every address of the prefix got its own allowance", allowed, int(incidentLimits.sourceBurst))
	}
	if !l.allow(loginFailure("2001:db8:1:3::1"), kind, clock.now()) {
		t.Fatal("another /64 was held back")
	}
}

func TestLimiterLimitsEventsWithoutAnAddressByActor(t *testing.T) {
	clock := newTestClock()
	l := newLimiter()
	kind := catalogue[utils.SecContainerAction]

	allowed := 0
	for i := 0; i < 500; i++ {
		if l.allow(Event{Kind: utils.SecContainerAction, Actor: "alice", Count: 1}, kind, clock.now()) {
			allowed++
		}
	}
	if allowed != int(auditLimits.sourceBurst) {
		t.Fatalf("%d audit entries of one user recorded at once, want %d", allowed, int(auditLimits.sourceBurst))
	}
	if !l.allow(Event{Kind: utils.SecContainerAction, Actor: "bob", Count: 1}, kind, clock.now()) {
		t.Fatal("another user was held back")
	}

	clock.advance(summaryQuiet)
	rows := l.summaries(clock.now(), false)
	if len(rows) != 1 || rows[0].Actor != "alice" || rows[0].Count != 500-allowed {
		t.Fatalf("summaries = %+v, want one for alice", rows)
	}
}

package security

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/azukaar/cosmos-server/src/utils"
)

// subscriberQueue is how many events wait for one subscriber. A subscriber
// that falls further behind loses events: the recorder never waits for it.
const subscriberQueue = 256

type subscriber struct {
	name    string
	events  chan Event
	done    chan struct{}
	missed  atomic.Int64
	stopped sync.Once
}

type subscribers struct {
	mu   sync.RWMutex
	list []*subscriber
}

// subscribe registers a function that is called for every event the
// recorder writes, in order, from a goroutine of its own.
func (s *subscribers) subscribe(name string, handle func(Event)) func() {
	sub := &subscriber{name: name, events: make(chan Event, subscriberQueue), done: make(chan struct{})}

	go func() {
		for {
			select {
			case event := <-sub.events:
				sub.deliver(handle, event)
			case <-sub.done:
				return
			}
		}
	}()

	s.mu.Lock()
	s.list = append(s.list, sub)
	s.mu.Unlock()

	return func() {
		sub.stopped.Do(func() {
			s.mu.Lock()
			for i, other := range s.list {
				if other == sub {
					s.list = append(s.list[:i:i], s.list[i+1:]...)
					break
				}
			}
			s.mu.Unlock()
			close(sub.done)
		})
	}
}

// deliver calls the subscriber's function; a panic in it ends there.
func (sub *subscriber) deliver(handle func(Event), event Event) {
	defer func() {
		if failure := recover(); failure != nil {
			utils.Error("Security events: the subscriber "+sub.name+" failed", fmt.Errorf("%v", failure))
		}
	}()
	handle(event)
}

// publish hands the events to every subscriber without waiting for any.
func (s *subscribers) publish(events []Event) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sub := range s.list {
		for _, event := range events {
			select {
			case sub.events <- copyOf(event):
			default:
				sub.missed.Add(1)
			}
		}
	}
}

// missed reports how many events each subscriber lost because it was too
// slow, by name.
func (s *subscribers) missed() map[string]int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lost := map[string]int64{}
	for _, sub := range s.list {
		lost[sub.name] += sub.missed.Load()
	}
	return lost
}

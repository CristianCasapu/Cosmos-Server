package utils

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// overlapConn counts the calls that start while another one is under way,
// which is where gorilla/websocket panics.
type overlapConn struct {
	busy     atomic.Int32
	overlaps atomic.Int32
	writes   atomic.Int32
}

func (c *overlapConn) enter() {
	if c.busy.Add(1) > 1 {
		c.overlaps.Add(1)
	}
	time.Sleep(200 * time.Microsecond)
	c.busy.Add(-1)
}

func (c *overlapConn) WriteMessage(messageType int, data []byte) error {
	c.enter()
	c.writes.Add(1)
	return nil
}

func (c *overlapConn) SetWriteDeadline(t time.Time) error {
	c.enter()
	return nil
}

// hammer writes from several goroutines at once, as the terminal's output and
// its keep-alive answers do.
func hammer(conn WebsocketWriter) {
	var wait sync.WaitGroup
	for writer := 0; writer < 8; writer++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for i := 0; i < 50; i++ {
				conn.SetWriteDeadline(time.Now().Add(time.Minute))
				conn.WriteMessage(websocket.TextMessage, []byte("_PONG_"))
			}
		}()
	}
	wait.Wait()
}

func TestSerialWebsocketLetsOneWriterInAtATime(t *testing.T) {
	// the same load without the lock does overlap: the check below means something
	bare := &overlapConn{}
	hammer(bare)
	if bare.overlaps.Load() == 0 {
		t.Fatal("the test load must be able to overlap, or it proves nothing")
	}

	conn := &overlapConn{}
	hammer(NewSerialWebsocket(conn))
	if got := conn.overlaps.Load(); got != 0 {
		t.Errorf("%d writes started while another one was under way", got)
	}
	if got := conn.writes.Load(); got != 8*50 {
		t.Errorf("every message must be written, got %d of %d", got, 8*50)
	}
}

// The same against a real connection: gorilla/websocket panics on concurrent
// writers, which would end this test run.
func TestSerialWebsocketOnARealConnection(t *testing.T) {
	upgrader := websocket.Upgrader{}
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer ws.Close()
		hammer(NewSerialWebsocket(ws))
		close(done)
	}))
	defer server.Close()

	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	received := 0
	for received < 8*50 {
		client.SetReadDeadline(time.Now().Add(10 * time.Second))
		_, message, err := client.ReadMessage()
		if err != nil {
			t.Fatalf("after %d messages: %v", received, err)
		}
		if string(message) != "_PONG_" {
			t.Fatalf("a message arrived mixed up: %q", message)
		}
		received++
	}
	<-done
}

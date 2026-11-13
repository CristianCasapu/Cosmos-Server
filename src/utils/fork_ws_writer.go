package utils

// Fork addition: one writer at a time on a websocket.
//
// gorilla/websocket allows one goroutine to write to a connection and panics
// ("concurrent write to websocket connection") when two do. The host terminal
// writes from two: the one that forwards what the shell prints, and the one
// that answers the browser's keep-alive. The panic is raised in a goroutine of
// the handler's own, where nothing recovers it, so it ends the whole server.
// The write deadline is a plain field of the connection as well, set by both.
//
// SerialWebsocket puts the writes and the write deadline of a connection
// behind one lock. Reads are left alone: there is one reader.

import (
	"sync"
	"time"
)

// WebsocketWriter is the writing side of a *websocket.Conn.
type WebsocketWriter interface {
	WriteMessage(messageType int, data []byte) error
	SetWriteDeadline(t time.Time) error
}

// SerialWebsocket writes to a websocket for several goroutines, one at a time.
type SerialWebsocket struct {
	lock sync.Mutex
	conn WebsocketWriter
}

// NewSerialWebsocket wraps the writing side of a connection. Every write to
// that connection must go through the value returned.
func NewSerialWebsocket(conn WebsocketWriter) *SerialWebsocket {
	return &SerialWebsocket{conn: conn}
}

// WriteMessage writes one message, after the writes already under way.
func (s *SerialWebsocket) WriteMessage(messageType int, data []byte) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.conn.WriteMessage(messageType, data)
}

// SetWriteDeadline sets the deadline of the writes that follow.
func (s *SerialWebsocket) SetWriteDeadline(t time.Time) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.conn.SetWriteDeadline(t)
}

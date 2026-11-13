package utils

// Fork addition: the request logger wraps every response with chi's
// WrapResponseWriter (moved from chi v4 to chi/v5). These tests pin what the
// logger relies on: the status and byte count it records, and that the
// wrapper still lets websocket upgrades hijack the connection.

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type forkCapturedEntry struct {
	status, bytes int
	written       bool
}

func (e *forkCapturedEntry) Write(status, bytes int, _ http.Header, _ time.Duration, _ interface{}) {
	e.status, e.bytes, e.written = status, bytes, true
}
func (e *forkCapturedEntry) Panic(interface{}, []byte) {}

type forkCaptureFormatter struct{ entry *forkCapturedEntry }

func (f forkCaptureFormatter) NewLogEntry(*http.Request) LogEntry { return f.entry }

func TestForkRequestLoggerRecordsStatusAndBytes(t *testing.T) {
	entry := &forkCapturedEntry{}
	h := RequestLogger(forkCaptureFormatter{entry})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if GetLogEntry(r) != entry {
			t.Error("the handler does not see the request's log entry")
		}
		w.WriteHeader(http.StatusTeapot)
		w.Write([]byte("hello"))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))

	if !entry.written || entry.status != http.StatusTeapot || entry.bytes != 5 {
		t.Fatalf("logged status=%d bytes=%d written=%v, want 418, 5, true", entry.status, entry.bytes, entry.written)
	}
	if rec.Code != http.StatusTeapot || rec.Body.String() != "hello" {
		t.Fatalf("response is %d %q, want 418 \"hello\"", rec.Code, rec.Body.String())
	}
}

func TestForkRequestLoggerImplicitStatus(t *testing.T) {
	entry := &forkCapturedEntry{}
	h := RequestLogger(forkCaptureFormatter{entry})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if entry.status != http.StatusOK || entry.bytes != 2 {
		t.Fatalf("logged status=%d bytes=%d, want 200, 2", entry.status, entry.bytes)
	}
}

// A real HTTP/1.1 connection through the logger: the handler takes over the
// connection the way a websocket upgrade does and answers on the raw socket.
func TestForkRequestLoggerKeepsHijacker(t *testing.T) {
	entry := &forkCapturedEntry{}
	srv := httptest.NewServer(RequestLogger(forkCaptureFormatter{entry})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "not hijackable", http.StatusInternalServerError)
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: test\r\nConnection: Upgrade\r\n\r\n")
		buf.Flush()
	})))
	defer srv.Close()

	conn, err := net.DialTimeout("tcp", srv.Listener.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	conn.Write([]byte("GET /ws HTTP/1.1\r\nHost: x\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n"))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("got %d, want 101: the logged response writer could not be hijacked", resp.StatusCode)
	}
}

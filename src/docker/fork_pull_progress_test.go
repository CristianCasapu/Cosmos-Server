package docker

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// Realistic texts: what Docker 29 (containerd store and classic store) and Docker Hub answer.
func TestPullReasonFromDockerTexts(t *testing.T) {
	cases := map[string]string{
		`Error response from daemon: failed to resolve reference "no-such.example.org/app:1": failed to do request: Head "https://no-such.example.org/v2/app/manifests/1": dial tcp: lookup no-such.example.org on 127.0.0.11:53: no such host`: PullReasonDNS,
		`Error response from daemon: Get "https://registry-1.docker.io/v2/": dial tcp: lookup registry-1.docker.io on 192.168.1.1:53: read udp 192.168.1.10:41234->192.168.1.1:53: i/o timeout`:                                                 PullReasonDNS,
		`Error response from daemon: Get "https://registry-1.docker.io/v2/": dial tcp: lookup registry-1.docker.io: Temporary failure in name resolution`:                                                                                       PullReasonDNS,
		`Error response from daemon: Get "https://registry-1.docker.io/v2/": dial tcp: lookup registry-1.docker.io on 127.0.0.53:53: server misbehaving`:                                                                                        PullReasonDNS,
		`Error response from daemon: Get "https://registry-1.docker.io/v2/": net/http: request canceled while waiting for connection (Client.Timeout exceeded while awaiting headers)`:                                                          PullReasonTimeout,
		`Error response from daemon: Get "https://192.0.2.10/v2/": dial tcp 192.0.2.10:443: i/o timeout`:                                                                                                                                        PullReasonTimeout,
		`Error response from daemon: Get "https://registry.example.org/v2/": net/http: TLS handshake timeout`:                                                                                                                                   PullReasonTimeout,
		`Error response from daemon: failed to resolve reference "localhost:5999/app:1": failed to do request: Head "https://localhost:5999/v2/app/manifests/1": dial tcp [::1]:5999: connect: connection refused`:                              PullReasonUnreachable,
		`Error response from daemon: Get "https://192.0.2.10/v2/": dial tcp 192.0.2.10:443: connect: no route to host`:                                                                                                                          PullReasonUnreachable,
		`Error response from daemon: Get "https://192.0.2.10:5000/v2/": http: server gave HTTP response to HTTPS client`:                                                                                                                        PullReasonUnreachable,
		`Error response from daemon: failed to resolve reference "localhost:5000/fork/bigapp:nope": localhost:5000/fork/bigapp:nope: not found`:                                                                                                 PullReasonNotFound,
		`Error response from daemon: manifest for nginx:nope not found: manifest unknown: manifest unknown`:                                                                                                                                     PullReasonNotFound,
		`Error response from daemon: pull access denied for nosuchuser/nosuchimage, repository does not exist or may require 'docker login': denied: requested access to the resource is denied`:                                                PullReasonNotFound,
		`Error response from daemon: no matching manifest for linux/amd64 in the manifest list entries`:                                                                                                                                         PullReasonNotFound,
		`toomanyrequests: You have reached your unauthenticated pull rate limit. https://www.docker.com/increase-rate-limit`:                                                                                                                    PullReasonRateLimit,
		`Error response from daemon: toomanyrequests: Too Many Requests (HAP429).`:                                                                                                                                                              PullReasonRateLimit,
		`failed to register layer: write /var/lib/docker/overlay2/abc/diff/usr/lib/x: no space left on device`:                                                                                                                                  PullReasonDiskFull,
		`failed to extract layer sha256:0123: write /var/lib/containerd/x: no space left on device: unknown`:                                                                                                                                    PullReasonDiskFull,
		`Error response from daemon: Get "https://registry.example.org/v2/": EOF`:                                                                                                                                                               PullReasonUnreachable,
		`failed commit on ref "layer-sha256:345b": commit failed: unexpected commit digest sha256:38f6, expected sha256:345b: failed precondition`:                                                                                              PullReasonDamaged,
		`filesystem layer verification failed for digest sha256:0123`:                                                                                                                                                                           PullReasonDamaged,
		`read tcp 192.168.1.10:41234->203.0.113.5:443: unexpected EOF`:                                                                                                                                                                          PullReasonDamaged,
		`Error response from daemon: something nobody has seen before`:                                                                                                                                                                          PullReasonOther,
	}
	for text, want := range cases {
		if got := PullReason(text); got != want {
			t.Errorf("%q: got %s, want %s", text, got, want)
		}
	}
}

// The classic store's stream: bytes per layer, ready layers.
func TestPullTrackerSumsLayersAndBytes(t *testing.T) {
	p := newPullTracker()
	for _, l := range []string{
		`{"status":"Pulling from library/nginx","id":"1.27"}`,
		`{"status":"Pulling fs layer","progressDetail":{},"id":"aaaaaaaaaaaa"}`,
		`{"status":"Pulling fs layer","progressDetail":{},"id":"bbbbbbbbbbbb"}`,
		`{"status":"Already exists","progressDetail":{},"id":"cccccccccccc"}`,
		`{"status":"Waiting","progressDetail":{},"id":"bbbbbbbbbbbb"}`,
		`{"status":"Downloading","progressDetail":{"current":1000,"total":4000},"progress":"[==>  ] 1kB/4kB","id":"aaaaaaaaaaaa"}`,
		`{"status":"Downloading","progressDetail":{"current":500,"total":2000},"id":"bbbbbbbbbbbb"}`,
		`{"status":"Downloading","progressDetail":{"current":3000,"total":4000},"id":"aaaaaaaaaaaa"}`,
	} {
		if msg := p.add(l); msg != "" {
			t.Fatalf("no error in %s, got %q", l, msg)
		}
	}
	layers, ready, current, total := p.summary()
	if layers != 3 || ready != 1 || current != 3500 || total != 6000 {
		t.Fatalf("got %d layers, %d ready, %d/%d bytes", layers, ready, current, total)
	}
	p.add(`{"status":"Download complete","progressDetail":{},"id":"aaaaaaaaaaaa"}`)
	p.add(`{"status":"Extracting","progressDetail":{"current":2000,"total":4000},"id":"aaaaaaaaaaaa"}`)
	p.add(`{"status":"Pull complete","progressDetail":{},"id":"aaaaaaaaaaaa"}`)
	p.add(`{"status":"Retrying in 5 seconds","progressDetail":{},"id":"bbbbbbbbbbbb"}`)
	p.add(`{"status":"Digest: sha256:0123"}`)
	p.add(`{"status":"Status: Downloaded newer image for nginx:1.27"}`)
	layers, ready, current, total = p.summary()
	if layers != 3 || ready != 2 || current != 4500 || total != 6000 {
		t.Fatalf("after: got %d layers, %d ready, %d/%d bytes", layers, ready, current, total)
	}
}

// The containerd store (Docker 29's default), as it really answered: "Extracting" counts seconds,
// and the config and manifest come as "Download complete" without being layers.
func TestPullTrackerContainerdStore(t *testing.T) {
	p := newPullTracker()
	for _, l := range []string{
		`{"status":"Pulling from fork/bigapp","id":"1"}`,
		`{"status":"Pulling fs layer","progressDetail":{},"id":"2c846c5a3bea"}`,
		`{"status":"Pulling fs layer","progressDetail":{},"id":"345b9629ed20"}`,
		`{"status":"Downloading","progressDetail":{"current":1048576,"total":40009728},"id":"2c846c5a3bea"}`,
		`{"status":"Download complete","progressDetail":{"hidecounts":true},"id":"da4413d4dbbd"}`,
		`{"status":"Download complete","progressDetail":{"hidecounts":true},"id":"44136fa355b3"}`,
		`{"status":"Extracting","progressDetail":{"current":2,"units":"s"},"id":"345b9629ed20"}`,
		`{"status":"Pull complete","progressDetail":{"hidecounts":true},"id":"345b9629ed20"}`,
	} {
		p.add(l)
	}
	layers, ready, current, total := p.summary()
	if layers != 2 || ready != 1 || current != 1048576 || total != 40009728 {
		t.Fatalf("got %d layers, %d ready, %d/%d bytes", layers, ready, current, total)
	}
}

func TestPullTrackerIsBounded(t *testing.T) {
	p := newPullTracker()
	for i := 0; i < pullLayersMax+50; i++ {
		p.add(`{"status":"Pulling fs layer","id":"layer-` + itoa(i) + `"}`)
	}
	if layers, _, _, _ := p.summary(); layers != pullLayersMax {
		t.Fatalf("%d layers tracked", layers)
	}
	// nonsense progress is not taken
	q := newPullTracker()
	q.add(`{"status":"Downloading","progressDetail":{"current":9,"total":3},"id":"aaaaaaaaaaaa"}`)
	q.add(`{"status":"Downloading","progressDetail":{"current":-1,"total":3},"id":"aaaaaaaaaaaa"}`)
	if _, _, current, total := q.summary(); current != 0 || total != 0 {
		t.Fatalf("got %d/%d", current, total)
	}
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func TestPullTrackerSeesTheErrorLine(t *testing.T) {
	p := newPullTracker()
	if msg := p.add(`{"errorDetail":{"message":"failed to register layer: no space left on device"},"error":"failed to register layer: no space left on device"}`); msg != "failed to register layer: no space left on device" {
		t.Fatalf("got %q", msg)
	}
	if msg := p.add(`{"error":"toomanyrequests: slow down"}`); msg != "toomanyrequests: slow down" {
		t.Fatalf("got %q", msg)
	}
	if msg := p.add(`{"status":"Downloading","error":""}`); msg != "" {
		t.Fatalf("got %q", msg)
	}
	if msg := p.add(`not json`); msg != "" {
		t.Fatalf("got %q", msg)
	}
}

func TestPullDetailIsCleanAndBounded(t *testing.T) {
	got := pullDetail("Error response from daemon: bad\x1b[31m red\x1b[0m\nline\r‮evil " + strings.Repeat("é", 400))
	if strings.ContainsAny(got, "\n\r\x1b‮") || strings.HasPrefix(got, "Error response") {
		t.Fatalf("not clean: %q", got)
	}
	if len(got) > pullDetailMax+len("…") || !strings.HasSuffix(got, "…") || !strings.HasPrefix(got, "bad red line evil é") {
		t.Fatalf("not bounded: %d %q", len(got), got)
	}
}

// fakePull answers for Docker with a stream, an error, or nothing until released.
type pullRecorder struct {
	lines []string // deliberately not locked: -race finds two writers at once
}

func (r *pullRecorder) onLog(s string) { r.lines = append(r.lines, s) }

func (r *pullRecorder) markers(t *testing.T) []PullProgress {
	t.Helper()
	out := []PullProgress{}
	for _, l := range r.lines {
		if strings.HasPrefix(l, PullProgressMarker) {
			if !strings.HasSuffix(l, "\n") || strings.Count(l, "\n") != 1 {
				t.Fatalf("a marker is not one line: %q", l)
			}
			var p PullProgress
			if err := json.Unmarshal([]byte(strings.TrimPrefix(l, PullProgressMarker)), &p); err != nil {
				t.Fatalf("%q: %v", l, err)
			}
			out = append(out, p)
		}
	}
	return out
}

func (r *pullRecorder) docker() []string {
	out := []string{}
	for _, l := range r.lines {
		if !strings.HasPrefix(l, PullProgressMarker) {
			out = append(out, l)
		}
	}
	return out
}

func withPull(t *testing.T, beat time.Duration, fake func(string) (io.ReadCloser, error)) {
	t.Helper()
	oldBeat, oldPull := pullBeatEvery, pullImageFor
	pullBeatEvery, pullImageFor = beat, fake
	t.Cleanup(func() { pullBeatEvery, pullImageFor = oldBeat, oldPull })
}

const goodStream = `{"status":"Pulling from library/busybox","id":"1.36"}
{"status":"Pulling fs layer","progressDetail":{},"id":"aaaaaaaaaaaa"}
{"status":"Downloading","progressDetail":{"current":100,"total":200},"id":"aaaaaaaaaaaa"}
{"status":"Pull complete","progressDetail":{},"id":"aaaaaaaaaaaa"}
{"status":"Status: Downloaded newer image for busybox:1.36"}
`

// Docker's lines reach the client exactly as upstream forwarded them; the markers are added around them.
func TestPullForwardsDockerLinesUnchanged(t *testing.T) {
	withPull(t, time.Hour, func(string) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(goodStream)), nil })
	r := &pullRecorder{}
	out, err := forkPullImage("busybox:1.36", r.onLog)
	if err != nil {
		t.Fatal(err)
	}
	if rest, _ := io.ReadAll(out); len(rest) != 0 {
		t.Fatalf("the reader handed back is not empty: %q", rest)
	}
	want := []string{}
	for _, l := range strings.Split(strings.TrimSuffix(goodStream, "\n"), "\n") {
		want = append(want, l+"\n")
	}
	if got := r.docker(); strings.Join(got, "") != strings.Join(want, "") {
		t.Fatalf("Docker's lines changed:\n%q\nwant\n%q", got, want)
	}
	m := r.markers(t)
	if len(m) != 2 || m[0].State != "pulling" || m[1].State != "done" || m[1].Layers != 1 || m[1].Ready != 1 || m[1].Current != 200 || m[1].Total != 200 || m[1].Image != "busybox:1.36" {
		t.Fatalf("markers: %+v", m)
	}
	if !strings.HasPrefix(r.lines[0], PullProgressMarker) || !strings.HasPrefix(r.lines[len(r.lines)-1], PullProgressMarker) {
		t.Fatal("the first and last lines are not the markers")
	}
}

// A pull that fails inside the stream is a failure, with its reason, and not "pulled".
func TestPullErrorInsideTheStreamFails(t *testing.T) {
	stream := `{"status":"Pulling from library/big","id":"1"}
{"status":"Downloading","progressDetail":{"current":100,"total":200},"id":"aaaaaaaaaaaa"}
{"errorDetail":{"message":"failed to register layer: write /var/lib/docker/x: no space left on device"},"error":"failed to register layer: write /var/lib/docker/x: no space left on device"}
`
	withPull(t, time.Hour, func(string) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(stream)), nil })
	r := &pullRecorder{}
	out, err := forkPullImage("big:1", r.onLog)
	if err == nil || out != nil {
		t.Fatalf("a failing stream was taken for a pull: %v", err)
	}
	if !strings.Contains(err.Error(), "no space left on device") {
		t.Fatalf("the reason is lost: %v", err)
	}
	m := r.markers(t)
	last := m[len(m)-1]
	if last.State != "failed" || last.Reason != PullReasonDiskFull || !strings.Contains(last.Detail, "no space left") {
		t.Fatalf("last marker: %+v", last)
	}
	if len(r.docker()) != 3 {
		t.Fatalf("Docker's lines: %q", r.docker())
	}

	// the pull routes: no markers, the same failure
	r2 := &pullRecorder{}
	if _, err := forkPull("big:1", r2.onLog, false); err == nil {
		t.Fatal("the route took the failing stream for a pull")
	}
	if len(r2.markers(t)) != 0 || len(r2.lines) != 3 {
		t.Fatalf("the route's lines: %q", r2.lines)
	}
}

// Docker that does not answer: the beat says the time, and stops when it answers; no goroutine is left.
func TestPullBeatsWhileDockerHasNotAnsweredAndStops(t *testing.T) {
	release := make(chan struct{})
	withPull(t, 5*time.Millisecond, func(string) (io.ReadCloser, error) {
		<-release
		return nil, errors.New(`Error response from daemon: Get "https://registry-1.docker.io/v2/": dial tcp: lookup registry-1.docker.io on 192.168.1.1:53: read udp 192.168.1.10:41234->192.168.1.1:53: i/o timeout`)
	})
	var mu sync.Mutex
	beats := 0
	r := &pullRecorder{}
	onLog := func(s string) {
		r.onLog(s)
		mu.Lock()
		if strings.Contains(s, `"state":"pulling"`) {
			beats++
		}
		mu.Unlock()
	}
	done := make(chan error)
	go func() { _, err := forkPullImage("busybox:1.36", onLog); done <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := beats
		mu.Unlock()
		if n >= 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no beat while Docker had not answered (%d)", n)
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("the error was lost")
	}
	count := len(r.lines)
	time.Sleep(50 * time.Millisecond)
	if len(r.lines) != count {
		t.Fatalf("the beat goes on after the pull returned: %d lines, then %d", count, len(r.lines))
	}
	m := r.markers(t)
	if last := m[len(m)-1]; last.State != "failed" || last.Reason != PullReasonDNS {
		t.Fatalf("last marker: %+v", last)
	}
	for _, p := range m[:len(m)-1] {
		if p.State != "pulling" {
			t.Fatalf("a marker after the end: %+v", m)
		}
	}
}

// The beat stops on the stream's paths too (success, error line, unreadable line), under -race.
func TestPullBeatStopsOnEveryPath(t *testing.T) {
	long := `{"status":"` + strings.Repeat("x", pullLineMax+10) + `"}` + "\n"
	for name, stream := range map[string]string{
		"success": goodStream,
		"error":   `{"error":"toomanyrequests: You have reached your pull rate limit."}` + "\n",
		"toolong": long,
	} {
		t.Run(name, func(t *testing.T) {
			slow := &slowReader{r: strings.NewReader(stream), wait: 2 * time.Millisecond}
			withPull(t, time.Millisecond, func(string) (io.ReadCloser, error) { return io.NopCloser(slow), nil })
			r := &pullRecorder{}
			_, err := forkPullImage("x:1", r.onLog)
			if (name == "success") != (err == nil) {
				t.Fatalf("err %v", err)
			}
			count := len(r.lines)
			time.Sleep(20 * time.Millisecond)
			if len(r.lines) != count {
				t.Fatal("the beat goes on after the pull returned")
			}
			m := r.markers(t)
			last := m[len(m)-1]
			switch name {
			case "error":
				if last.Reason != PullReasonRateLimit {
					t.Fatalf("%+v", last)
				}
			case "toolong":
				if last.State != "failed" || !strings.Contains(last.Detail, "could not be read") {
					t.Fatalf("%+v", last)
				}
			}
			for _, l := range r.lines {
				if strings.HasPrefix(l, PullProgressMarker) && len(l) > 2048 {
					t.Fatalf("a marker line of %d bytes", len(l))
				}
			}
		})
	}
}

type slowReader struct {
	r    io.Reader
	wait time.Duration
}

func (s *slowReader) Read(p []byte) (int, error) {
	time.Sleep(s.wait)
	if len(p) > 4096 {
		p = p[:4096]
	}
	return s.r.Read(p)
}

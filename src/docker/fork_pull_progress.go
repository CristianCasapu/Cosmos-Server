package docker

// Fork addition: the image pull of a creation as a step of its own. While
// DockerPullImage has not answered (a name lookup that times out, a registry
// that does not answer) and while the layers come in, the creation's answer
// gets, every pullBeatEvery, one line
//
//	[PULL PROGRESS] {"image":"…","state":"pulling","seconds":4,"layers":3,"ready":1,"current":1048576,"total":4194304}
//
// and a last one with "state":"done" or "state":"failed" (with "reason", one
// of pullReasons, and "detail", a bounded and cleaned technical line). The
// panel shows it in words (client/src/utils/pullProgress.js). Docker's own
// lines are forwarded unchanged and in order, as upstream did: existing
// clients see only added lines. A pull whose stream ends with Docker's
// {"error": …} line now fails the creation (upstream went on with "Image …
// pulled" and failed later, at the container, with a confusing reason).
//
// Bounded: one marker line per beat (plus the first and the last), at most
// pullLayersMax layers tracked, pullDetailMax bytes of detail, pullLineMax
// bytes per line read from Docker. The beat's goroutine is stopped and waited
// for on every path before the function returns.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	PullProgressMarker = "[PULL PROGRESS] "
	pullLayersMax      = 256
	pullDetailMax      = 300
	pullLineMax        = 1 << 20
)

// The reasons a pull fails, as the panel knows them.
const (
	PullReasonDNS         = "dns"
	PullReasonUnreachable = "unreachable"
	PullReasonTimeout     = "timeout"
	PullReasonNotFound    = "notFound"
	PullReasonRateLimit   = "rateLimit"
	PullReasonDiskFull    = "diskFull"
	PullReasonDamaged     = "damaged"
	PullReasonOther       = "other"
)

var (
	// pullBeatEvery and pullImageFor are variables so that tests run fast and answer for Docker.
	pullBeatEvery = 2 * time.Second
	pullImageFor  = DockerPullImage
)

// PullProgress is one marker line.
type PullProgress struct {
	Image   string `json:"image"`
	State   string `json:"state"` // pulling, done, failed
	Seconds int    `json:"seconds"`
	Layers  int    `json:"layers"`
	Ready   int    `json:"ready"`
	Current int64  `json:"current,omitempty"`
	Total   int64  `json:"total,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// pullLine is what a line of Docker's pull stream may hold.
type pullLine struct {
	Status         string `json:"status"`
	ID             string `json:"id"`
	ProgressDetail struct {
		Current int64  `json:"current"`
		Total   int64  `json:"total"`
		Units   string `json:"units"`
	} `json:"progressDetail"`
	Error       string `json:"error"`
	ErrorDetail struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
}

type pullLayer struct {
	current, total int64
	ready          bool
}

// pullTracker sums the layers of one pull.
type pullTracker struct {
	layers map[string]*pullLayer
}

func newPullTracker() *pullTracker { return &pullTracker{layers: map[string]*pullLayer{}} }

// layerStatuses are the statuses that name a layer (not the tag of "Pulling from", nor the
// config or manifest the containerd store reports as "Download complete" only).
var layerStatuses = map[string]bool{
	"Pulling fs layer": true, "Waiting": true, "Downloading": true, "Verifying Checksum": true,
	"Extracting": true, "Pull complete": true, "Already exists": true, "Retrying": true,
}

// add reads one line of the stream; it answers Docker's error message when the line is one.
func (p *pullTracker) add(raw string) string {
	var line pullLine
	if json.Unmarshal([]byte(raw), &line) != nil {
		return ""
	}
	if line.Error != "" || line.ErrorDetail.Message != "" {
		if line.ErrorDetail.Message != "" {
			return line.ErrorDetail.Message
		}
		return line.Error
	}
	if line.ID == "" {
		return ""
	}
	status := line.Status
	if strings.HasPrefix(status, "Retrying") {
		status = "Retrying"
	}
	layer, known := p.layers[line.ID]
	if !known {
		if !layerStatuses[status] || len(p.layers) >= pullLayersMax {
			return ""
		}
		layer = &pullLayer{}
		p.layers[line.ID] = layer
	}
	switch status {
	case "Downloading":
		// bytes only: the containerd store counts "Extracting" in seconds
		if d := line.ProgressDetail; d.Total > 0 && d.Units == "" && d.Current >= 0 && d.Current <= d.Total {
			layer.current, layer.total = d.Current, d.Total
		}
	case "Download complete", "Verifying Checksum":
		if layer.total > 0 {
			layer.current = layer.total
		}
	case "Pull complete", "Already exists":
		if layer.total > 0 {
			layer.current = layer.total
		}
		layer.ready = true
	}
	return ""
}

func (p *pullTracker) summary() (layers, ready int, current, total int64) {
	for _, l := range p.layers {
		layers++
		if l.ready {
			ready++
		}
		current += l.current
		total += l.total
	}
	return
}

// PullReason says why a pull failed, from Docker's error text.
func PullReason(text string) string {
	t := strings.ToLower(text)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(t, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("no space left on device", "disk quota exceeded"):
		return PullReasonDiskFull
	case has("toomanyrequests", "too many requests", "rate limit"):
		return PullReasonRateLimit
	case has("manifest unknown", "not found", "pull access denied", "repository does not exist",
		"name unknown", "requested access to the resource is denied", "no matching manifest", "unauthorized", "authentication required"):
		return PullReasonNotFound
	case has("unexpected commit digest", "digest mismatch", "digest did not match", "verification failed", "unexpected eof"):
		return PullReasonDamaged
	case has("no such host", "server misbehaving", "temporary failure in name resolution", "name resolution", "dial tcp: lookup ", "lookup "):
		return PullReasonDNS
	case has("timeout", "timed out", "deadline exceeded"):
		return PullReasonTimeout
	case has("connection refused", "no route to host", "network is unreachable", "connection reset",
		"dial tcp", "server gave http response to https client", "tls:", "x509:", "eof"):
		return PullReasonUnreachable
	}
	return PullReasonOther
}

// pullDetail is Docker's text, cleaned and bounded, for the technical line.
func pullDetail(text string) string {
	text = strings.TrimPrefix(strings.TrimSpace(text), "Error response from daemon: ")
	text = strings.NewReplacer("\n", " ", "\r", " ").Replace(text)
	text = strings.Join(strings.Fields(cleanLogLine(text)), " ")
	if len(text) > pullDetailMax {
		cut := pullDetailMax
		for cut > 0 && !utf8Start(text[cut]) {
			cut--
		}
		text = text[:cut] + "…"
	}
	return text
}

func pullMarkerLine(p PullProgress) string {
	b, _ := json.Marshal(p)
	return PullProgressMarker + string(b) + "\n"
}

// forkPullImage pulls an image for a creation: Docker's lines forwarded, the marker lines added,
// a failure inside the stream returned as an error. The reader it answers is empty (the stream is read).
func forkPullImage(image string, OnLog func(string)) (io.ReadCloser, error) {
	return forkPull(image, OnLog, true)
}

// forkPullImageTo is the same for the pull routes, whose panel shows the lines as they are: no markers.
func forkPullImageTo(image string, w io.Writer, flush func()) (io.ReadCloser, error) {
	return forkPull(image, func(s string) {
		utils.Log(strings.TrimSuffix(s, "\n"))
		fmt.Fprintf(w, "%s", s)
		flush()
	}, false)
}

func forkPull(image string, OnLog func(string), markers bool) (io.ReadCloser, error) {
	var mu sync.Mutex // OnLog writes to the answer: one writer at a time
	tracker := newPullTracker()
	begin := time.Now()
	progress := func(state, reason, detail string) PullProgress {
		layers, ready, current, total := tracker.summary()
		return PullProgress{Image: pullDetail(image), State: state, Seconds: int(time.Since(begin) / time.Second),
			Layers: layers, Ready: ready, Current: current, Total: total, Reason: reason, Detail: detail}
	}
	say := func(state, reason, detail string) {
		if markers {
			mu.Lock()
			OnLog(pullMarkerLine(progress(state, reason, detail)))
			mu.Unlock()
		}
	}

	stopBeat := func() {}
	if markers {
		say("pulling", "", "")
		stop, stopped := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(stopped)
			tick := time.NewTicker(pullBeatEvery)
			defer tick.Stop()
			for {
				select {
				case <-stop:
					return
				case <-tick.C:
					say("pulling", "", "")
				}
			}
		}()
		var once sync.Once
		stopBeat = func() { once.Do(func() { close(stop); <-stopped }) }
	}
	defer stopBeat()

	out, err := pullImageFor(image)
	if err != nil {
		stopBeat()
		say("failed", PullReason(err.Error()), pullDetail(err.Error()))
		return nil, err
	}

	failure := ""
	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 0, 64*1024), pullLineMax)
	for scanner.Scan() {
		text := scanner.Text()
		mu.Lock()
		OnLog(fmt.Sprintf("%s\n", text))
		failure = tracker.add(text)
		mu.Unlock()
		if failure != "" {
			break
		}
	}
	if failure == "" && scanner.Err() != nil {
		failure = "the pull's answer could not be read: " + scanner.Err().Error()
	}
	out.Close()
	stopBeat()
	if failure != "" {
		detail := pullDetail(failure)
		say("failed", PullReason(failure), detail)
		return nil, errors.New(detail)
	}
	say("done", "", "")
	return io.NopCloser(strings.NewReader("")), nil
}

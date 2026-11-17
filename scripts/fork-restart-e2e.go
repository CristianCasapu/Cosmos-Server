//go:build ignore

// Fork addition (plan 46): restarts of the web server, hammered end to end.
// scripts/fork-restart-e2e.sh builds this file and runs it inside the Docker
// in Docker that holds the test Cosmos (bare-metal style, HTTPS 2053 with a
// self-signed certificate, HTTP 2052, hostname server.example.org). Two modes:
//
//	app      the proxied app on 127.0.0.1:8081: / and /version answer
//	         "app ok", /ws echoes a WebSocket, /big?mb=N&kbps=R streams N MB
//	         at R KB/s, /slow?ms=N answers after N ms
//	hammer   sets up ~15 URLs to the app, then restarts the web server
//	         through the API routes that do it (PUT /api/routes/{name},
//	         PATCH /api/config as the URL form does, PUT /api/config as the
//	         settings page does) at 0 ms, 100 ms, 1 s and 3 s spacing, one
//	         client after the other and several at once, while probes ask
//	         the app over HTTPS, the plain-HTTP listener and the panel, a
//	         WebSocket stays open and a long download runs. After each
//	         pattern every listener must answer again, the last saved URL
//	         must be live and the process must be the same one. Then it
//	         measures single restarts: how long until a change is live and
//	         how long the listeners refuse or drop connections.
//
// Exit status 1 when anything failed; the summary is the last lines printed.
// Options (environment): PASSWORD, SCALE (multiplies the counts, default 1),
// ONLY (comma separated pattern names to run, e.g. "routes-0ms-seq"),
// GAPS (number of single restarts measured, default 20).
// Never against the owner's server: it talks to 127.0.0.1 inside the test's
// own Docker in Docker only.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: fork-restart-e2e app|hammer")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "app":
		runApp()
	case "hammer":
		os.Exit(runHammer())
	}
}

// ---------------------------------------------------------------- the app

func runApp() {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "app ok "+r.Host) })
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			t, m, err := c.ReadMessage()
			if err != nil {
				return
			}
			if c.WriteMessage(t, m) != nil {
				return
			}
		}
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) {
		mb, _ := strconv.Atoi(r.URL.Query().Get("mb"))
		kbps, _ := strconv.Atoi(r.URL.Query().Get("kbps"))
		if kbps <= 0 {
			kbps = 1024
		}
		w.Header().Set("Content-Length", strconv.Itoa(mb<<20))
		chunk := bytes.Repeat([]byte("x"), 16<<10)
		per := time.Duration(float64(time.Second) * 16 / float64(kbps))
		for sent := 0; sent < mb<<20; sent += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(per)
		}
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		ms, _ := strconv.Atoi(r.URL.Query().Get("ms"))
		time.Sleep(time.Duration(ms) * time.Millisecond)
		io.WriteString(w, "slow ok")
	})
	fmt.Println(http.ListenAndServe("127.0.0.1:8081", mux))
}

// ---------------------------------------------------------------- clients

const (
	panelHost = "server.example.org"
	httpsPort = "2053"
	httpPort  = "2052"
)

var sourceIP atomic.Uint32

// client returns an HTTP client that sends every host to 127.0.0.1, from one
// of many loopback source addresses (the API limits requests per address),
// without keep-alive so that every request is a new connection.
func client(timeout time.Duration, block byte, jar http.CookieJar) *http.Client {
	tr := &http.Transport{
		DisableKeepAlives: true,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, // the test's own self-signed certificate
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, _ := net.SplitHostPort(addr)
			n := sourceIP.Add(1)
			d := net.Dialer{Timeout: 3 * time.Second, LocalAddr: &net.TCPAddr{IP: net.IPv4(127, 0, block, byte(1+n%200))}}
			return d.DialContext(ctx, "tcp", "127.0.0.1:"+port)
		},
		ForceAttemptHTTP2: true,
	}
	return &http.Client{Timeout: timeout, Transport: tr, Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// kind names a failure the way the report counts it
func kind(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "reset"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), strings.Contains(err.Error(), "EOF"):
		return "closed"
	case strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline"):
		return "timeout"
	case strings.Contains(err.Error(), "tls"):
		return "tls"
	}
	return "other:" + err.Error()
}

type api struct {
	c        *http.Client
	password string
}

func (a *api) do(method, path string, body interface{}) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, "https://"+panelHost+":"+httpsPort+"/cosmos/api/"+path, rd)
	req.Header.Set("Content-Type", "application/json")
	res, err := a.c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, raw, nil
}

func (a *api) login() error {
	for try := 0; try < 60; try++ {
		st, body, err := a.do("POST", "login", map[string]string{"nickname": "admin", "password": a.password})
		if err == nil && (st == 200 || strings.Contains(string(body), "already logged in")) {
			st, _, err = a.do("POST", "sudo", map[string]string{"password": a.password})
			if err == nil && st == 200 {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return errors.New("cannot sign in")
}

// ---------------------------------------------------------------- probes

type sample struct {
	at   time.Time
	ok   bool
	kind string
}

type probe struct {
	name  string
	mu    sync.Mutex
	all   []sample
	check func() (bool, string)
}

func (p *probe) run(stop <-chan struct{}, every time.Duration) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		ok, k := p.check()
		p.mu.Lock()
		p.all = append(p.all, sample{time.Now(), ok, k})
		p.mu.Unlock()
		time.Sleep(every)
	}
}

func (p *probe) since(t time.Time) []sample {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []sample{}
	for _, s := range p.all {
		if !s.at.Before(t) {
			out = append(out, s)
		}
	}
	return out
}

func get(c *http.Client, url string, host string) (int, string, error) {
	req, _ := http.NewRequest("GET", url, nil)
	req.Host = host
	res, err := c.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4096))
	if err != nil {
		return res.StatusCode, "", err
	}
	return res.StatusCode, string(raw), nil
}

func appCheck(c *http.Client, host string) (bool, string) {
	st, body, err := get(c, "https://"+host+":"+httpsPort+"/version", host+":"+httpsPort)
	if err != nil {
		return false, kind(err)
	}
	if st != 200 || !strings.HasPrefix(body, "app ok") {
		return false, "status-" + strconv.Itoa(st)
	}
	return true, ""
}

func plainCheck(c *http.Client) (bool, string) {
	st, _, err := get(c, "http://app01.example.org:"+httpPort+"/", "app01.example.org:"+httpPort)
	if err != nil {
		return false, kind(err)
	}
	if st < 300 || st > 399 {
		return false, "status-" + strconv.Itoa(st)
	}
	return true, ""
}

func panelCheck(c *http.Client) (bool, string) {
	st, _, err := get(c, "https://"+panelHost+":"+httpsPort+"/cosmos-ui/", panelHost+":"+httpsPort)
	if err != nil {
		return false, kind(err)
	}
	if st != 200 {
		return false, "status-" + strconv.Itoa(st)
	}
	return true, ""
}

// ---------------------------------------------------------------- the process

func cosmosPID() int {
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		comm, _ := os.ReadFile("/proc/" + e.Name() + "/comm")
		if strings.TrimSpace(string(comm)) == "cosmos" {
			return pid
		}
	}
	return 0
}

// ---------------------------------------------------------------- hammer

type hammer struct {
	a        *api
	probe    *http.Client
	probes   []*probe
	seq      atomic.Int64
	failures []string
	mu       sync.Mutex
	requests []time.Time // when each restart was asked for
	lost     int         // saves whose answer did not arrive
	refused  int         // saves that did not reach the server (sent again)
	pid      int
}

func (h *hammer) fail(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println("  FAIL " + msg)
	h.mu.Lock()
	h.failures = append(h.failures, msg)
	h.mu.Unlock()
}

func liveRoute(host string) map[string]interface{} {
	return map[string]interface{}{"Name": "live", "UseHost": true, "Host": host, "Target": "http://127.0.0.1:8081",
		"Mode": "PROXY", "SmartShield": map[string]interface{}{"Enabled": false}}
}

// change saves a new address for the URL "live" with one of the three
// routes; it returns the host it saved.
func (h *hammer) change(method string, c *api) string {
	n := h.seq.Add(1)
	host := fmt.Sprintf("live-%d.example.org", n)
	h.mu.Lock()
	h.requests = append(h.requests, time.Now())
	h.mu.Unlock()
	var st int
	var err error
	var body []byte
	// a save sent while the listeners are closed is refused before it
	// reaches the server (nothing saved): sent again, as a person would
	for try := 0; try < 200; try++ {
		st, body, err = h.save(method, c, host)
		// refused, or an HTTP/2 connection ended before its first request
		// (GOAWAY): the save did not reach the server, browsers send it again
		if k := kind(err); k != "refused" && !strings.Contains(k, "client conn could not be established") {
			break
		}
		h.mu.Lock()
		h.refused++
		h.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || st != 200 {
		h.mu.Lock()
		h.lost++
		h.mu.Unlock()
		why := kind(err)
		if err == nil {
			why = "status " + strconv.Itoa(st) + " " + strings.TrimSpace(string(body))
			if len(why) > 200 {
				why = why[:200]
			}
		}
		fmt.Println("  save " + method + " " + host + ": no answer: " + why)
	}
	return host
}

func (h *hammer) save(method string, c *api, host string) (st int, body []byte, err error) {
	switch method {
	case "routes":
		st, body, err = c.do("PUT", "routes/live", liveRoute(host))
	case "patch":
		st, body, err = c.do("PATCH", "config", map[string]interface{}{"routeName": "live", "operation": "replace", "newRoute": liveRoute(host)})
	case "config":
		var cfg map[string]interface{}
		st, body, err = c.do("GET", "config", nil)
		if err == nil && st == 200 {
			var got struct{ Data map[string]interface{} }
			json.Unmarshal(body, &got)
			cfg = got.Data
			routes := cfg["HTTPConfig"].(map[string]interface{})["ProxyConfig"].(map[string]interface{})["Routes"].([]interface{})
			for i, r := range routes {
				if r.(map[string]interface{})["Name"] == "live" {
					r.(map[string]interface{})["Host"] = host
					routes[i] = r
				}
			}
			st, body, err = c.do("PUT", "config", cfg)
		}
	}
	return
}

// configuredLive reads back which address "live" has now.
func (h *hammer) configuredLive() string {
	for try := 0; try < 30; try++ {
		st, body, err := h.a.do("GET", "routes/live", nil)
		if err == nil && st == 200 {
			var got struct{ Data struct{ Host string } }
			json.Unmarshal(body, &got)
			return got.Data.Host
		}
		time.Sleep(500 * time.Millisecond)
	}
	return ""
}

// settle waits until every probe and the configured address answer.
func (h *hammer) settle(name string, within time.Duration) {
	deadline := time.Now().Add(within)
	want := h.configuredLive()
	var last string
	for time.Now().Before(deadline) {
		problems := []string{}
		if ok, k := appCheck(h.probe, "app01.example.org"); !ok {
			problems = append(problems, "HTTPS listener ("+k+")")
		}
		if ok, k := plainCheck(h.probe); !ok {
			problems = append(problems, "plain-HTTP listener ("+k+")")
		}
		if ok, k := panelCheck(h.probe); !ok {
			problems = append(problems, "panel ("+k+")")
		}
		if want == "" {
			problems = append(problems, "the configuration could not be read")
		} else if ok, k := appCheck(h.probe, strings.TrimSuffix(want, ".example.org")+".example.org"); !ok {
			problems = append(problems, "the last saved address "+want+" is not live ("+k+")")
		}
		if len(problems) == 0 {
			break
		}
		last = strings.Join(problems, ", ")
		time.Sleep(200 * time.Millisecond)
	}
	if time.Now().After(deadline) {
		h.fail("%s: %s still failing %s after the last save", name, last, within)
	}
	if pid := cosmosPID(); pid != h.pid {
		h.fail("%s: the Cosmos process ended (pid %d, now %d)", name, h.pid, pid)
		h.pid = pid
	}
}

// recover brings a dead listener back for the next pattern: a restart, and
// when the HTTPS listener cannot be asked any more, the process is ended
// (the loop of the shell script starts it again).
func (h *hammer) recover() {
	if ok, _ := appCheck(h.probe, "app01.example.org"); ok {
		if okp, _ := plainCheck(h.probe); okp {
			return
		}
		h.change("routes", h.a)
		time.Sleep(5 * time.Second)
		if okp, _ := plainCheck(h.probe); okp {
			return
		}
	}
	fmt.Println("  recovering: ending the Cosmos process")
	if pid := cosmosPID(); pid > 0 {
		syscall.Kill(pid, syscall.SIGKILL)
	}
	for try := 0; try < 60; try++ {
		time.Sleep(time.Second)
		if ok, _ := appCheck(h.probe, "app01.example.org"); ok {
			break
		}
	}
	h.a.login()
	h.pid = cosmosPID()
}

func (h *hammer) pattern(method string, spacing time.Duration, clients int, count int) {
	name := fmt.Sprintf("%s-%s-%s", method, spacing, map[bool]string{true: "seq", false: "conc"}[clients == 1])
	if only := os.Getenv("ONLY"); only != "" && !strings.Contains(","+only+",", ","+name+",") {
		return
	}
	failsBefore := len(h.failures)
	start := time.Now()
	var wg sync.WaitGroup
	for c := 0; c < clients; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < count; i++ {
				h.change(method, h.a)
				time.Sleep(spacing)
			}
		}()
	}
	wg.Wait()
	h.settle(name, 30*time.Second)
	bad := map[string]int{}
	for _, p := range h.probes {
		for _, s := range p.since(start) {
			if !s.ok {
				bad[p.name+":"+s.kind]++
			}
		}
	}
	fmt.Printf("  %-26s %4d saves in %6.1fs, failed probes %v\n", name, clients*count, time.Since(start).Seconds(), bad)
	if len(h.failures) > failsBefore {
		h.recover()
	}
}

func percentile(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[int(float64(len(d)-1)*p)]
}

// gaps measures single restarts: time from the save until the new address
// answers, and the window in which new connections fail.
func (h *hammer) gaps(n int) {
	fast := client(3*time.Second, 9, nil)
	slowClient := client(20*time.Second, 10, nil)
	var live, gap, waited []time.Duration
	failedKinds := map[string]int{}
	slowOK, slowFail := 0, 0
	for i := 0; i < n; i++ {
		time.Sleep(3 * time.Second)
		// an ordinary request in flight across the restart
		slow := make(chan error, 1)
		go func() {
			st, body, err := get(slowClient, "https://app02.example.org:"+httpsPort+"/slow?ms=2500", "app02.example.org:"+httpsPort)
			if err == nil && (st != 200 || body != "slow ok") {
				err = fmt.Errorf("status %d", st)
			}
			slow <- err
		}()
		time.Sleep(200 * time.Millisecond)
		stop := make(chan struct{})
		var mu sync.Mutex
		var first, last time.Time
		var slowest time.Duration
		var wg sync.WaitGroup
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					t := time.Now()
					ok, k := appCheck(fast, "app01.example.org")
					if took := time.Since(t); ok {
						mu.Lock()
						if took > slowest {
							slowest = took
						}
						mu.Unlock()
					}
					if !ok {
						mu.Lock()
						if first.IsZero() {
							first = t
						}
						last = time.Now()
						failedKinds[k]++
						mu.Unlock()
					}
					time.Sleep(2 * time.Millisecond)
				}
			}()
		}
		saved := time.Now()
		host := h.change("routes", h.a)
		for time.Since(saved) < 20*time.Second {
			if ok, _ := appCheck(fast, host); ok {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		live = append(live, time.Since(saved))
		time.Sleep(1500 * time.Millisecond)
		close(stop)
		wg.Wait()
		if !first.IsZero() {
			gap = append(gap, last.Sub(first))
		} else {
			gap = append(gap, 0)
		}
		waited = append(waited, slowest)
		if err := <-slow; err != nil {
			slowFail++
			fmt.Println("  an ordinary request in flight across the restart failed: " + err.Error())
		} else {
			slowOK++
		}
	}
	fmt.Printf("  single restarts: %d; save until the change is live: median %s, p95 %s, max %s\n", n,
		percentile(live, .5).Round(time.Millisecond), percentile(live, .95).Round(time.Millisecond), percentile(live, 1).Round(time.Millisecond))
	fmt.Printf("  connections failing during a restart: median %s, p95 %s, max %s, kinds %v\n",
		percentile(gap, .5).Round(time.Millisecond), percentile(gap, .95).Round(time.Millisecond), percentile(gap, 1).Round(time.Millisecond), failedKinds)
	fmt.Printf("  the slowest answered request on a new connection during a restart: median %s, p95 %s, max %s\n",
		percentile(waited, .5).Round(time.Millisecond), percentile(waited, .95).Round(time.Millisecond), percentile(waited, 1).Round(time.Millisecond))
	fmt.Printf("  ordinary 2.5 s requests in flight across a restart: %d finished, %d cut\n", slowOK, slowFail)
	if slowFail > 0 {
		h.fail("%d ordinary requests in flight were cut by a restart", slowFail)
	}
}

func runHammer() int {
	password := os.Getenv("PASSWORD")
	if password == "" {
		password = "Test-Passw0rd!x"
	}
	scale := 1.0
	if s, err := strconv.ParseFloat(os.Getenv("SCALE"), 64); err == nil && s > 0 {
		scale = s
	}
	gapCount := 20
	if g, err := strconv.Atoi(os.Getenv("GAPS")); err == nil {
		gapCount = g
	}
	jar, _ := cookiejar.New(nil)
	h := &hammer{a: &api{c: client(60*time.Second, 2, jar), password: password}, probe: client(5*time.Second, 1, nil)}
	if err := h.a.login(); err != nil {
		fmt.Println("FAIL " + err.Error())
		return 1
	}
	h.pid = cosmosPID()

	// the URLs: app01..app15 and "live", saved at once
	st, body, err := h.a.do("GET", "config", nil)
	if err != nil || st != 200 {
		fmt.Println("FAIL reading the configuration")
		return 1
	}
	var got struct{ Data map[string]interface{} }
	json.Unmarshal(body, &got)
	cfg := got.Data
	routes := []interface{}{liveRoute("live-0.example.org")}
	for i := 1; i <= 15; i++ {
		r := liveRoute(fmt.Sprintf("app%02d.example.org", i))
		r["Name"] = fmt.Sprintf("app%02d", i)
		routes = append(routes, r)
	}
	cfg["HTTPConfig"].(map[string]interface{})["ProxyConfig"].(map[string]interface{})["Routes"] = routes
	if st, body, err := h.a.do("PUT", "config", cfg); err != nil || st != 200 {
		fmt.Printf("FAIL saving the URLs: %v %d %s\n", err, st, body)
		return 1
	}
	for try := 0; try < 60; try++ {
		time.Sleep(time.Second)
		if ok, _ := appCheck(h.probe, "app15.example.org"); ok {
			break
		}
	}
	h.a.login()
	h.pid = cosmosPID()
	fmt.Printf("16 URLs live, Cosmos pid %d\n", h.pid)

	// probes, a WebSocket and a long download for the whole run
	stop := make(chan struct{})
	pc := client(5*time.Second, 3, nil)
	h.probes = []*probe{
		{name: "https", check: func() (bool, string) { return appCheck(pc, "app01.example.org") }},
		{name: "http", check: func() (bool, string) { return plainCheck(pc) }},
		{name: "panel", check: func() (bool, string) { return panelCheck(pc) }},
	}
	for _, p := range h.probes {
		go p.run(stop, 20*time.Millisecond)
	}
	wsBreaks := []string{}
	var wsMu sync.Mutex
	go func() {
		d := websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, HandshakeTimeout: 5 * time.Second,
			NetDial: func(network, addr string) (net.Conn, error) { return net.Dial("tcp", "127.0.0.1:"+httpsPort) }}
		for {
			c, _, err := d.Dial("wss://app03.example.org:"+httpsPort+"/ws", http.Header{"Host": {"app03.example.org:" + httpsPort}})
			if err != nil {
				time.Sleep(200 * time.Millisecond)
				continue
			}
			opened := time.Now()
			for {
				select {
				case <-stop:
					c.Close()
					wsMu.Lock()
					wsBreaks = append(wsBreaks, fmt.Sprintf("open at the end (%s)", time.Since(opened).Round(time.Second)))
					wsMu.Unlock()
					return
				default:
				}
				c.SetWriteDeadline(time.Now().Add(5 * time.Second))
				c.SetReadDeadline(time.Now().Add(5 * time.Second))
				if err := c.WriteMessage(websocket.TextMessage, []byte("ping")); err == nil {
					_, _, err = c.ReadMessage()
				}
				if err != nil {
					wsMu.Lock()
					wsBreaks = append(wsBreaks, fmt.Sprintf("broke after %s: %s", time.Since(opened).Round(time.Second), kind(err)))
					wsMu.Unlock()
					c.Close()
					break
				}
				time.Sleep(250 * time.Millisecond)
			}
		}
	}()
	download := make(chan string, 1)
	go func() {
		dc := client(30*time.Minute, 4, nil)
		started := time.Now()
		req, _ := http.NewRequest("GET", "https://app04.example.org:"+httpsPort+"/big?mb=120&kbps=512", nil)
		req.Host = "app04.example.org:" + httpsPort
		res, err := dc.Do(req)
		if err != nil {
			download <- "did not start: " + kind(err)
			return
		}
		n, err := io.Copy(io.Discard, res.Body)
		res.Body.Close()
		download <- fmt.Sprintf("%d of %d MB in %s, %v", n>>20, 120, time.Since(started).Round(time.Second), map[bool]string{true: "complete", false: "cut: " + kind(err)}[err == nil && n == 120<<20])
	}()

	counts := func(n int) int {
		c := int(float64(n) * scale)
		if c < 1 {
			c = 1
		}
		return c
	}
	fmt.Println("patterns (method-spacing-clients):")
	for _, method := range []string{"routes", "patch", "config"} {
		h.pattern(method, 0, 1, counts(150))
		h.pattern(method, 0, 8, counts(40))
		h.pattern(method, 100*time.Millisecond, 1, counts(60))
		h.pattern(method, 100*time.Millisecond, 8, counts(15))
		h.pattern(method, time.Second, 1, counts(10))
		h.pattern(method, time.Second, 8, counts(4))
		h.pattern(method, 3*time.Second, 1, counts(5))
		h.pattern(method, 3*time.Second, 8, counts(2))
	}
	if gapCount > 0 {
		fmt.Println("single restarts, measured:")
		h.gaps(gapCount)
	}

	// failures of the probes outside the 15 s after any save
	h.mu.Lock()
	reqs := append([]time.Time{}, h.requests...)
	h.mu.Unlock()
	sort.Slice(reqs, func(i, j int) bool { return reqs[i].Before(reqs[j]) })
	outside := 0
	for _, p := range h.probes {
		for _, s := range p.since(time.Time{}) {
			if s.ok {
				continue
			}
			i := sort.Search(len(reqs), func(i int) bool { return reqs[i].After(s.at) })
			if i == 0 || s.at.Sub(reqs[i-1]) > 15*time.Second {
				outside++
			}
		}
	}
	close(stop)
	total := 0
	for _, p := range h.probes {
		total += len(p.since(time.Time{}))
	}
	fmt.Println("summary:")
	fmt.Printf("  saves: %d; not reaching the server (refused, or HTTP/2 ended before the request) and sent again: %d; without an answer: %d\n", len(reqs), h.refused, h.lost)
	fmt.Printf("  probe requests: %d; failed outside the 15 s after a save: %d\n", total, outside)
	select {
	case d := <-download:
		fmt.Println("  long download through the proxy: " + d)
	case <-time.After(10 * time.Minute):
		fmt.Println("  long download through the proxy: still running")
	}
	time.Sleep(500 * time.Millisecond)
	wsMu.Lock()
	fmt.Printf("  WebSocket through the proxy: %v\n", wsBreaks)
	wsMu.Unlock()
	if h.lost > 0 {
		h.fail("%d saves got no answer", h.lost)
	}
	if outside > 0 {
		h.fail("%d probe requests failed away from any restart", outside)
	}
	if len(h.failures) > 0 {
		fmt.Printf("FAILED: %d problems\n", len(h.failures))
		for _, f := range h.failures {
			fmt.Println("  - " + f)
		}
		return 1
	}
	fmt.Println("PASSED")
	return 0
}

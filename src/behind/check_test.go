package behind

// Fork addition (plan 44): tests of the URL check. The apps are stand-ins
// that answer as the real ones did in the research (answers kept in
// testdata/answers, read from the images on the day apps.json says).

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/proxy"
	"github.com/azukaar/cosmos-server/src/utils"
)

const appIP = "192.0.2.50"

type stand struct {
	mu       sync.Mutex
	requests []*http.Request
	server   *httptest.Server
}

func (s *stand) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

// withApp sets up a server like the owner's (HTTPS on 2053), the URLs given,
// a container "app" of image at appIP whose port is answered by handler.
func withApp(t *testing.T, image string, env map[string]string, log []string, routes []utils.ProxyRouteConfig, handler http.HandlerFunc) *stand {
	t.Helper()
	s := &stand{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.Clone(context.Background()))
		s.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(s.server.Close)
	listener := strings.TrimPrefix(s.server.URL, "http://")

	config := utils.Config{}
	config.HTTPConfig.Hostname = "server.example.org"
	config.HTTPConfig.HTTPPort = "2052"
	config.HTTPConfig.HTTPSPort = "2053"
	config.HTTPConfig.HTTPSCertificateMode = utils.HTTPSCertModeList["SELFSIGNED"]
	config.HTTPConfig.ProxyConfig.Routes = routes
	utils.LoadBaseMainConfig(config)
	listening := utils.HTTPSListening
	utils.HTTPSListening = true

	oldFacts, oldDial, oldLog := factsOf, dialApp, logSince
	factsOf = func(ctx context.Context, name string) docker.URLCheckFacts {
		if name != "app" {
			return docker.URLCheckFacts{Why: "missing"}
		}
		return docker.URLCheckFacts{ID: "c0ffee", IP: appIP, Image: image, Env: env}
	}
	dialApp = func(ctx context.Context, address string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(address)
		if host != appIP {
			t.Errorf("the check connected to %s, not the container's address", address)
			return nil, &net.OpError{Op: "dial", Err: os.ErrPermission}
		}
		var d net.Dialer
		return d.DialContext(ctx, "tcp", listener)
	}
	logSince = func(context.Context, string, bool, time.Time) []string { return log }
	t.Cleanup(func() {
		factsOf, dialApp, logSince = oldFacts, oldDial, oldLog
		utils.HTTPSListening = listening
		utils.LoadBaseMainConfig(utils.Config{})
		cacheMu.Lock()
		cache = map[string]Result{}
		cacheMu.Unlock()
	})
	return s
}

func urlTo(name, host string, own bool) utils.ProxyRouteConfig {
	return utils.ProxyRouteConfig{Name: name, Mode: "SERVAPP", UseHost: true, Host: host, Target: "http://app:8080", ForkAppOwnHost: own}
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("testdata/answers/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// qbittorrent answers as qBittorrent 5.3 does: a Host whose port is not its own is refused.
func qbittorrent(t *testing.T) http.HandlerFunc {
	refusal := fixture(t, "qbittorrent-host-port.txt")
	return func(w http.ResponseWriter, r *http.Request) {
		if _, port, err := net.SplitHostPort(r.Host); err == nil && port != "8080" {
			w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(refusal))
			return
		}
		w.Write([]byte("<!DOCTYPE html><html><head><title>qBittorrent WebUI</title></head></html>"))
	}
}

func TestQBittorrentIsRecognisedAndTheRepairSeenWorking(t *testing.T) {
	s := withApp(t, "lscr.io/linuxserver/qbittorrent:latest", nil, nil,
		[]utils.ProxyRouteConfig{urlTo("qbt", "qbittorrent.server.local", false), urlTo("qbt-ext", "qbittorrent.example.org", false)}, qbittorrent(t))
	r := Check(context.Background(), "qbt")
	if r.Verdict != "refused" || r.Reason != "hostPort" || r.App != "qbittorrent" {
		t.Fatalf("verdict %q reason %q app %q, want refused hostPort qbittorrent: %+v", r.Verdict, r.Reason, r.App, r)
	}
	if r.URL != "https://qbittorrent.server.local:2053" {
		t.Errorf("URL %q", r.URL)
	}
	if len(r.Attempts) != 2 || r.Attempts[0].Status != 401 || r.Attempts[1].Status != 200 {
		t.Fatalf("attempts %+v", r.Attempts)
	}
	if r.Attempts[0].Host != "qbittorrent.server.local:2053" || r.Attempts[1].Host != appIP+":8080" || r.Attempts[1].ForwardedHost != "qbittorrent.server.local:2053" {
		t.Errorf("hosts sent: %+v", r.Attempts)
	}
	if len(r.Repairs) == 0 || r.Repairs[0].Kind != "route" || r.Repairs[0].Setting != "appOwnHost" || !r.Repairs[0].On || !r.Repairs[0].Seen {
		t.Errorf("the first repair must be the URL's setting, seen working: %+v", r.Repairs)
	}
	if s.count() != 2 {
		t.Errorf("the app was asked %d times, want 2", s.count())
	}

	// with the setting on, it works and nothing more is asked
	utils.LoadBaseMainConfig(func() utils.Config {
		c := utils.GetMainConfig()
		c.HTTPConfig.ProxyConfig.Routes[0].ForkAppOwnHost = true
		return c
	}())
	r = Check(context.Background(), "qbt")
	if r.Verdict != "works" || s.count() != 3 {
		t.Errorf("with the setting: verdict %q after %d requests, want works after 3", r.Verdict, s.count())
	}
}

func TestTheRequestCarriesNothingButTheURLsAddress(t *testing.T) {
	s := withApp(t, "example/app:1", nil, nil, []utils.ProxyRouteConfig{urlTo("app", "app.example.org", false)}, func(w http.ResponseWriter, r *http.Request) {})
	Check(context.Background(), "app")
	if s.count() != 1 {
		t.Fatalf("asked %d times", s.count())
	}
	r := s.requests[0]
	if r.Method != "GET" || r.URL.Path != "/" || r.ContentLength > 0 {
		t.Errorf("request %s %s (%d bytes)", r.Method, r.URL.Path, r.ContentLength)
	}
	for _, h := range []string{"Cookie", "Authorization", "Origin", "Referer"} {
		if r.Header.Get(h) != "" {
			t.Errorf("the check sent %s: %q", h, r.Header.Get(h))
		}
	}
	for h, want := range map[string]string{"X-Forwarded-Host": "app.example.org:2053", "X-Forwarded-Port": "2053", "X-Forwarded-Proto": "https", "Sec-Fetch-Site": "none"} {
		if r.Header.Get(h) != want {
			t.Errorf("%s = %q, want %q", h, r.Header.Get(h), want)
		}
	}
	if r.Host != "app.example.org:2053" {
		t.Errorf("Host %q", r.Host)
	}
}

func TestRedirectsAreReadNeverFollowed(t *testing.T) {
	s := withApp(t, "example/app:1", nil, nil, []utils.ProxyRouteConfig{urlTo("app", "app.example.org", false)}, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://app.example.org/login", http.StatusFound)
	})
	r := Check(context.Background(), "app")
	if s.count() != 1 {
		t.Errorf("a redirect was followed: %d requests", s.count())
	}
	if r.Verdict != "wrongAddress" || r.Line != "http://app.example.org/login" {
		t.Errorf("verdict %q line %q, want wrongAddress", r.Verdict, r.Line)
	}

	// a relative redirect, or one to the URL's own address, is fine
	for _, location := range []string{"/login", "https://app.example.org:2053/login"} {
		if wrongLocation(location, publicOf(t, "app.example.org"), "https") {
			t.Errorf("%s judged wrong", location)
		}
	}
	for _, location := range []string{"https://app.example.org/login", "http://app.example.org:2053/", "https://app:8080/", "https://192.0.2.50:8080/"} {
		if !wrongLocation(location, publicOf(t, "app.example.org"), "https") {
			t.Errorf("%s judged right", location)
		}
	}
}

func publicOf(t *testing.T, host string) proxy.PublicAddress {
	return proxy.PublicAddress{Name: host, Port: "2053", Host: host + ":2053"}
}

func TestAnAnswerIsBoundedAndShownAsText(t *testing.T) {
	withApp(t, "example/app:1", nil, nil, []utils.ProxyRouteConfig{urlTo("app", "app.example.org", false)}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("<script>alert(1)</script>\x1b[31m" + strings.Repeat("x", 1<<20)))
	})
	r := Check(context.Background(), "app")
	if len(r.Attempts[0].First) > firstMax+4 || strings.ContainsAny(r.Attempts[0].First, "<>\x1b") {
		t.Errorf("first words not bounded or not cleaned: %q", r.Attempts[0].First[:40])
	}
}

func TestOnlyContainersOfThisServerAreAsked(t *testing.T) {
	s := withApp(t, "example/app:1", nil, nil, []utils.ProxyRouteConfig{
		{Name: "proxy", Mode: "PROXY", UseHost: true, Host: "a.example.org", Target: "http://192.0.2.60:80"},
		{Name: "ip", Mode: "SERVAPP", UseHost: true, Host: "b.example.org", Target: "http://192.0.2.60:80"},
		{Name: "dotted", Mode: "SERVAPP", UseHost: true, Host: "c.example.org", Target: "http://app.lan:80"},
		{Name: "local", Mode: "SERVAPP", UseHost: true, Host: "d.example.org", Target: "http://localhost:80"},
		{Name: "gone", Mode: "SERVAPP", UseHost: true, Host: "e.example.org", Target: "http://gone:80"},
		{Name: "spoof", Mode: "SERVAPP", UseHost: true, Host: "f.example.org", Target: "http://app:8080", SpoofHostname: true},
		{Name: "static", Mode: "STATIC", UseHost: true, Host: "g.example.org", Target: "/srv"},
	}, func(w http.ResponseWriter, r *http.Request) {})
	for name, want := range map[string]string{"proxy": "notChecked", "ip": "notChecked", "dotted": "notChecked", "local": "notChecked", "gone": "notRunning", "spoof": "notChecked", "static": "notChecked", "nosuch": "notChecked"} {
		if r := Check(context.Background(), name); r.Verdict != want {
			t.Errorf("%s: verdict %q reason %q, want %s", name, r.Verdict, r.Reason, want)
		}
	}
	if s.count() != 0 {
		t.Errorf("the app was asked %d times", s.count())
	}
}

func TestNeverTheServerItself(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1", "0.0.0.0", "169.254.169.254", "fe80::1", "224.0.0.1", "not-an-ip", ""} {
		if usableAddress(ip) {
			t.Errorf("%q may be asked", ip)
		}
	}
	old := ownAddresses
	ownAddresses = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("192.0.2.10"), Mask: net.CIDRMask(24, 32)}}, nil
	}
	defer func() { ownAddresses = old }()
	if usableAddress("192.0.2.10") {
		t.Error("one of the server's own addresses may be asked")
	}
	if !usableAddress("172.30.0.5") {
		t.Error("a container's address is refused")
	}
}

func TestOwnHostThatBreaksTheAppIsSaid(t *testing.T) {
	// as Portainer and Uptime Kuma: the origin check compares with Host only
	withApp(t, "example/app:1", nil, nil, []utils.ProxyRouteConfig{urlTo("app", "app.example.org", true)}, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Host, appIP) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
	})
	r := Check(context.Background(), "app")
	if r.Verdict != "refused" || r.Reason != "ownHostBreaks" || len(r.Repairs) == 0 || r.Repairs[0].Setting != "appOwnHost" || r.Repairs[0].On || !r.Repairs[0].Seen {
		t.Errorf("verdict %q reason %q repairs %+v", r.Verdict, r.Reason, r.Repairs)
	}
}

func TestSameAnswerUnderEveryName(t *testing.T) {
	withApp(t, "example/app:1", nil, nil, []utils.ProxyRouteConfig{urlTo("app", "app.example.org", false)}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	if r := Check(context.Background(), "app"); r.Verdict != "answers" {
		t.Errorf("verdict %q, want answers", r.Verdict)
	}
}

func TestApplyChangesOneSettingOfOneURL(t *testing.T) {
	withApp(t, "lscr.io/linuxserver/qbittorrent:5.3.0", nil, nil,
		[]utils.ProxyRouteConfig{urlTo("qbt", "qbittorrent.server.local", false), urlTo("other", "other.example.org", false)}, qbittorrent(t))
	var saved utils.Config
	oldUpdate, oldReload := updateRoute, reloadRoutes
	updateRoute = func(name string, change func(*utils.ProxyRouteConfig) error) error {
		c := utils.GetMainConfig()
		for i := range c.HTTPConfig.ProxyConfig.Routes {
			if c.HTTPConfig.ProxyConfig.Routes[i].Name == name {
				if err := change(&c.HTTPConfig.ProxyConfig.Routes[i]); err != nil {
					return err
				}
				saved = c
				utils.LoadBaseMainConfig(c)
				return nil
			}
		}
		return errNoRoute
	}
	reloadRoutes = func(bool) {}
	defer func() { updateRoute, reloadRoutes = oldUpdate, oldReload }()

	post := func(body string, perms ...utils.Permission) (int, map[string]interface{}) {
		req := httptest.NewRequest("POST", "/cosmos/api/url-check/apply", bytes.NewReader([]byte(body)))
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{Nickname: "admin", Permissions: perms, IsSudoed: true, UserRole: utils.ADMIN}))
		rec := httptest.NewRecorder()
		ApplyRoute(rec, req)
		var out map[string]interface{}
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	admin := []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN}
	if code, _ := post(`{"route":"qbt","appOwnHost":true}`, utils.PERM_LOGIN, utils.PERM_RESOURCES_READ); code == 200 {
		t.Error("a user without the admin permission changed a URL")
	}
	if code, _ := post(`{"route":"qbt"}`, admin...); code != 400 {
		t.Errorf("no setting named: %d", code)
	}
	code, out := post(`{"route":"qbt","appOwnHost":true}`, admin...)
	if code != 200 {
		t.Fatalf("apply: %d %v", code, out)
	}
	data := out["data"].(map[string]interface{})
	if data["verdict"] != "works" {
		t.Errorf("after the repair the check says %v", data["verdict"])
	}
	for _, r := range saved.HTTPConfig.ProxyConfig.Routes {
		if r.Name == "other" && r.ForkAppOwnHost {
			t.Error("the repair of one URL changed another")
		}
		if r.Name == "qbt" && !r.ForkAppOwnHost {
			t.Error("the repair was not saved")
		}
	}
	if code, _ := post(`{"route":"nosuch","appOwnHost":true}`, admin...); code != 404 {
		t.Errorf("unknown URL: %d", code)
	}
	req := httptest.NewRequest("GET", "/cosmos/api/url-check/apply", nil)
	req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{Nickname: "admin", Permissions: admin, IsSudoed: true, UserRole: utils.ADMIN}))
	rec := httptest.NewRecorder()
	ApplyRoute(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", rec.Code)
	}
}

// Second review F2: a repair applied to several URLs reloads them once, after
// the last: every call but the last says that another follows, and the reload
// then waits for it instead of firing between two of them; a caller that
// never sends the rest still gets its reload.
func TestARepairOfSeveralURLsReloadsOnce(t *testing.T) {
	oldRestart, oldAfter, oldAbandoned := utils.RestartHTTPServer, reloadAfter, reloadIfAbandoned
	var reloads int32
	utils.RestartHTTPServer = func() { atomic.AddInt32(&reloads, 1) }
	reloadAfter, reloadIfAbandoned = 30*time.Millisecond, 400*time.Millisecond
	defer func() {
		reloadMu.Lock()
		if reloadTimer != nil {
			reloadTimer.Stop()
		}
		reloadMu.Unlock()
		utils.RestartHTTPServer, reloadAfter, reloadIfAbandoned = oldRestart, oldAfter, oldAbandoned
	}()
	// three URLs, each apply slower than the short delay (its check takes time)
	reloadRoutes(true)
	time.Sleep(80 * time.Millisecond)
	reloadRoutes(true)
	time.Sleep(80 * time.Millisecond)
	if n := atomic.LoadInt32(&reloads); n != 0 {
		t.Fatalf("reloaded %d time(s) between the URLs of one repair", n)
	}
	reloadRoutes(false)
	time.Sleep(120 * time.Millisecond)
	if n := atomic.LoadInt32(&reloads); n != 1 {
		t.Fatalf("reloaded %d time(s) after the last URL, want once", n)
	}
	// the rest never comes: the reload still happens
	reloadRoutes(true)
	time.Sleep(600 * time.Millisecond)
	if n := atomic.LoadInt32(&reloads); n != 2 {
		t.Fatalf("an abandoned repair was reloaded %d time(s) in all, want 2", n)
	}
}

func TestChecksAreBounded(t *testing.T) {
	now := time.Now()
	for i := 0; i < checksPerMin; i++ {
		if !allowed("bounded", 1, now) {
			t.Fatalf("check %d refused", i)
		}
	}
	if allowed("bounded", 1, now) {
		t.Error("more checks than allowed a minute")
	}
	if !allowed("bounded", 1, now.Add(61*time.Second)) {
		t.Error("still refused a minute later")
	}
}

func TestAppThatRefusesTheVisitorsAddressIsOpenedWithout(t *testing.T) {
	// as Home Assistant: an empty 400 to any X-Forwarded-For from a peer it does not trust, said in its log
	s := withApp(t, "ghcr.io/home-assistant/home-assistant:stable", nil,
		[]string{"2026-11-16 08:00:00.000 ERROR (MainThread) [homeassistant.components.http.forwarded] A request from a reverse proxy was received from 172.30.0.1, but your HTTP integration is not set-up for reverse proxies"},
		[]utils.ProxyRouteConfig{urlTo("ha", "homeassistant.server.local", false)}, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Forwarded-For") != "" {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte("400: Bad Request"))
				return
			}
			http.Redirect(w, r, "/onboarding.html", http.StatusFound)
		})
	r := Check(context.Background(), "ha")
	if r.Verdict != "refused" || r.Reason != "untrustedProxy" || !strings.Contains(r.Line, "reverse proxy") {
		t.Fatalf("verdict %q reason %q line %q", r.Verdict, r.Reason, r.Line)
	}
	if r.Repairs[0].Setting != "noVisitorAddress" || !r.Repairs[0].On || !r.Repairs[0].Seen {
		t.Errorf("repairs %+v", r.Repairs)
	}
	if s.count() != 3 {
		t.Errorf("asked %d times, want 3 (at most)", s.count())
	}
	last := s.requests[2]
	if last.Header.Get("X-Forwarded-For") != "" || last.Header.Get("X-Real-Ip") != "" {
		t.Error("the third request still told the visitor's address")
	}
}

func TestASignInThatWillFailIsSaidWithTheVariable(t *testing.T) {
	// Paperless opens, but Django refuses the sign-in's Origin until it is told the addresses
	handler := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/accounts/login/?next=/", http.StatusFound)
	}
	routes := []utils.ProxyRouteConfig{urlTo("paperless", "paperless.server.local", false), urlTo("paperless-ext", "paperless.example.org", false)}
	withApp(t, "ghcr.io/paperless-ngx/paperless-ngx:latest", nil, nil, routes, handler)
	r := Check(context.Background(), "paperless")
	if r.Verdict != "warning" || r.Reason != "csrfOrigin" || len(r.Repairs) == 0 {
		t.Fatalf("verdict %q reason %q repairs %+v", r.Verdict, r.Reason, r.Repairs)
	}
	e := r.Repairs[0]
	if e.Kind != "env" || e.Name != "PAPERLESS_CSRF_TRUSTED_ORIGINS" || e.Value != "https://paperless.server.local:2053,https://paperless.example.org:2053" {
		t.Errorf("repair %+v", e)
	}
	// told already, or told the scheme instead: nothing to say
	for _, env := range []map[string]string{
		{"PAPERLESS_CSRF_TRUSTED_ORIGINS": "https://paperless.server.local:2053,https://paperless.example.org:2053"},
		{"PAPERLESS_PROXY_SSL_HEADER": `["HTTP_X_FORWARDED_PROTO","https"]`},
	} {
		withApp(t, "ghcr.io/paperless-ngx/paperless-ngx:latest", env, nil, routes, handler)
		if r := Check(context.Background(), "paperless"); r.Verdict != "works" {
			t.Errorf("with %v: %q %+v", env, r.Verdict, r.Repairs)
		}
	}
}

func TestCommandsAreWrittenForEachName(t *testing.T) {
	withApp(t, "nextcloud:35", map[string]string{"OVERWRITEPROTOCOL": "https"}, nil,
		[]utils.ProxyRouteConfig{urlTo("nc", "nextcloud.server.local", false), urlTo("nc-ext", "nextcloud.example.org", false)},
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("<html><head><style>" + strings.Repeat("body{margin:0}", 40) + "</style><script>var x = 1;</script></head><body><h2>Access through untrusted domain</h2>Please contact your administrator.</body></html>"))
		})
	r := Check(context.Background(), "nc")
	if r.Verdict != "refused" || r.Reason != "untrustedDomain" {
		t.Fatalf("%q %q", r.Verdict, r.Reason)
	}
	var cmd *Repair
	for i := range r.Repairs {
		if r.Repairs[i].Kind == "command" {
			cmd = &r.Repairs[i]
		}
		if r.Repairs[i].Kind == "route" {
			t.Errorf("a URL setting was proposed for Nextcloud: %+v", r.Repairs[i])
		}
	}
	if cmd == nil || cmd.User != "www-data" || len(cmd.Lines) != 2 ||
		cmd.Lines[0] != "php occ config:system:set trusted_domains 10 --value='nextcloud.server.local'" ||
		cmd.Lines[1] != "php occ config:system:set trusted_domains 11 --value='nextcloud.example.org'" {
		t.Errorf("command %+v", cmd)
	}
}

func TestTheCheckWaitsItsTurnOnlyAsLongAsItsRequest(t *testing.T) {
	withApp(t, "example/app:1", nil, nil, []utils.ProxyRouteConfig{urlTo("app", "app.example.org", false)}, func(w http.ResponseWriter, r *http.Request) {})
	checkSlot <- struct{}{} // another check is asking an app
	defer func() { <-checkSlot }()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	r := Check(ctx, "app")
	if r.Verdict != "notChecked" || r.Reason != "busy" || time.Since(start) > 2*time.Second {
		t.Errorf("%q %q after %s", r.Verdict, r.Reason, time.Since(start))
	}
}

func TestKeptResultsFollowTheContainer(t *testing.T) {
	withApp(t, "example/app:1", nil, nil, []utils.ProxyRouteConfig{urlTo("app", "app.example.org", false)}, func(w http.ResponseWriter, r *http.Request) {})
	remember(Check(context.Background(), "app"))
	route := utils.GetMainConfig().HTTPConfig.ProxyConfig.Routes[0]
	if _, ok := remembered(route, "c0ffee"); !ok {
		t.Error("the result of the same container was not kept")
	}
	if _, ok := remembered(route, "re-created"); ok {
		t.Error("a re-created container kept the old result")
	}
}

func TestHidingTheVisitorIsSaidToBeTemporary(t *testing.T) {
	route := urlTo("ha", "homeassistant.server.local", false)
	route.ForkNoVisitorAddress = true
	withApp(t, "ghcr.io/home-assistant/home-assistant:stable", nil, nil, []utils.ProxyRouteConfig{route}, func(w http.ResponseWriter, r *http.Request) {})
	r := Check(context.Background(), "ha")
	if r.Verdict != "warning" || r.Reason != "noVisitorLocalTrust" || len(r.Repairs) == 0 || r.Repairs[0].Setting != "noVisitorAddress" || r.Repairs[0].On {
		t.Errorf("%q %q %+v", r.Verdict, r.Reason, r.Repairs)
	}
	withApp(t, "example/app:1", nil, nil, []utils.ProxyRouteConfig{route}, func(w http.ResponseWriter, r *http.Request) {})
	if r := Check(context.Background(), "ha"); r.Reason != "noVisitorOn" {
		t.Errorf("an unknown app: %q", r.Reason)
	}
}

func TestTunnelledURLsAreNeitherCheckedNorChanged(t *testing.T) {
	tunnelled := urlTo("t", "app.example.org", false)
	tunnelled.Tunnel = "node-b"
	withApp(t, "example/app:1", nil, nil, []utils.ProxyRouteConfig{tunnelled}, func(w http.ResponseWriter, r *http.Request) {})
	if r := Check(context.Background(), "t"); r.Reason != "tunnel" {
		t.Errorf("checked: %q", r.Reason)
	}
	oldUpdate := updateRoute
	updateRoute = func(name string, change func(*utils.ProxyRouteConfig) error) error {
		r := utils.GetMainConfig().HTTPConfig.ProxyConfig.Routes[0]
		return change(&r)
	}
	defer func() { updateRoute = oldUpdate }()
	req := httptest.NewRequest("POST", "/cosmos/api/url-check/apply", strings.NewReader(`{"route":"t","appOwnHost":true}`))
	req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{Nickname: "admin", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN}, IsSudoed: true, UserRole: utils.ADMIN}))
	rec := httptest.NewRecorder()
	ApplyRoute(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "tunnel") {
		t.Errorf("apply on a tunnelled URL: %d %s", rec.Code, rec.Body.String())
	}
}

func TestOwnAddressIsWhatTheProxyDials(t *testing.T) {
	inside, host := utils.IsInsideContainer, utils.IsHostNetwork
	defer func() { utils.IsInsideContainer, utils.IsHostNetwork = inside, host }()
	utils.IsInsideContainer, utils.IsHostNetwork = true, false
	if got := dialledAddress("qbittorrent", "172.30.0.2", "8080"); got != "qbittorrent:8080" {
		t.Errorf("Cosmos in a container: %s", got)
	}
	utils.IsInsideContainer = false
	if got := dialledAddress("qbittorrent", "172.30.0.2", "8080"); got != "172.30.0.2:8080" {
		t.Errorf("Cosmos outside Docker: %s", got)
	}
}

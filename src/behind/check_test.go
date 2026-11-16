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
	if len(r.Repairs) == 0 || r.Repairs[0].Kind != "route" || !r.Repairs[0].AppOwnHost || !r.Repairs[0].Seen {
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
	if r.Verdict != "refused" || r.Reason != "ownHostBreaks" || len(r.Repairs) == 0 || r.Repairs[0].AppOwnHost || !r.Repairs[0].Seen {
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
	reloadRoutes = func() {}
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

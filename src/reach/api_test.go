package reach

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/upnp"
	"github.com/azukaar/cosmos-server/src/utils"
)

// fakes replaces everything that could reach the network and resets the
// cache. resolve answers for the names it is given.
func fakes(t *testing.T, routes []utils.ProxyRouteConfig, resolve func(ctx context.Context, host string) ([]net.IP, error)) *int32 {
	t.Helper()
	var asked int32
	saved := []interface{}{resolveHost, detectPublicIP, dynDNSAddress, upnpStatus, ownAddresses, clock, cacheTTL, lookupTimeout, runTimeout, parallel, maxHosts, forceGap}
	resolveHost = func(ctx context.Context, host string) ([]net.IP, error) {
		atomic.AddInt32(&asked, 1)
		return resolve(ctx, host)
	}
	detectPublicIP = func() (string, error) { return "", errors.New("not in tests") }
	dynDNSAddress = func() string { return "" }
	upnpStatus = func() upnp.Status { return upnp.Status{} }
	ownAddresses = func() []net.IP { return nil }
	cache, lastForced, lookedUp.ip = map[string]dnsEntry{}, time.Time{}, ""
	utils.LoadBaseMainConfig(utils.Config{HTTPConfig: utils.HTTPConfig{
		HTTPSPort: "2053", HTTPPort: "2052", HTTPSCertificateMode: "SELFSIGNED",
		ProxyConfig: utils.ProxyConfig{Routes: routes},
	}})
	t.Cleanup(func() {
		resolveHost = saved[0].(func(context.Context, string) ([]net.IP, error))
		detectPublicIP = saved[1].(func() (string, error))
		dynDNSAddress = saved[2].(func() string)
		upnpStatus = saved[3].(func() upnp.Status)
		ownAddresses = saved[4].(func() []net.IP)
		clock = saved[5].(func() time.Time)
		cacheTTL, lookupTimeout, runTimeout = saved[6].(time.Duration), saved[7].(time.Duration), saved[8].(time.Duration)
		parallel, maxHosts, forceGap = saved[9].(int), saved[10].(int), saved[11].(time.Duration)
		cache, lastForced, lookedUp.ip = map[string]dnsEntry{}, time.Time{}, ""
		utils.LoadBaseMainConfig(utils.Config{})
	})
	return &asked
}

func ours(ctx context.Context, host string) ([]net.IP, error) {
	return []net.IP{net.ParseIP("203.0.113.7")}, nil
}

func request(method, who, body string) *http.Request {
	req := httptest.NewRequest(method, "/cosmos/api/reachability", strings.NewReader(body))
	if who != "none" {
		auth := &utils.AuthContext{Nickname: "someone"}
		switch who {
		case "user":
			auth.Permissions = []utils.Permission{utils.PERM_LOGIN}
		case "reader":
			auth.Permissions = []utils.Permission{utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ}
		case "admin":
			auth.Permissions = []utils.Permission{utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ, utils.PERM_ADMIN_READ}
		}
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
	}
	return req
}

func serve(req *http.Request) (*httptest.ResponseRecorder, Response) {
	rec := httptest.NewRecorder()
	StatusRoute(rec, req)
	var body struct {
		Data Response `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body.Data
}

func TestEndpointPermissionsAndMethods(t *testing.T) {
	asked := fakes(t, []utils.ProxyRouteConfig{route("app.example.org")}, ours)
	for _, c := range []struct {
		method, who, body string
		code              int
	}{
		{"GET", "none", "", 401},
		{"GET", "user", "", 401},
		{"POST", "none", "", 401},
		{"POST", "user", "{}", 401},
		{"POST", "reader", `{"askPublicIP": true}`, 401},
		{"DELETE", "admin", "", 405},
		{"PUT", "admin", "", 405},
		{"POST", "admin", strings.Repeat(" ", 2000), 400},
		{"POST", "admin", "{not json", 400},
		{"GET", "reader", "", 200},
		{"GET", "admin", "", 200},
	} {
		rec, _ := serve(request(c.method, c.who, c.body))
		if rec.Code != c.code {
			t.Errorf("%s as %s: %d, want %d", c.method, c.who, rec.Code, c.code)
		}
	}
	if *asked != 1 {
		t.Errorf("%d lookups, want 1 (refused requests look nothing up, the second GET uses the cache)", *asked)
	}
}

func TestAddressesOnlyForAdmins(t *testing.T) {
	fakes(t, []utils.ProxyRouteConfig{route("app.example.org"), route("other.example.org")}, func(ctx context.Context, host string) ([]net.IP, error) {
		if host == "other.example.org" {
			return []net.IP{net.ParseIP("198.51.100.9")}, nil
		}
		return ours(ctx, host)
	})
	dynDNSAddress = func() string { return "203.0.113.7" }

	rec, admin := serve(request("GET", "admin", ""))
	if admin.PublicIP != "203.0.113.7" || admin.PublicIPSource != "dyndns" || !strings.Contains(rec.Body.String(), "198.51.100.9") {
		t.Fatalf("admin does not get the addresses: %s", rec.Body.String())
	}
	rec, reader := serve(request("GET", "reader", ""))
	if strings.Contains(rec.Body.String(), "203.0.113.7") || strings.Contains(rec.Body.String(), "198.51.100.9") {
		t.Fatalf("addresses given to a non-admin: %s", rec.Body.String())
	}
	if len(reader.Routes) != 2 || reader.Routes[1].State != StateProblem || reader.PublicIPSource != "dyndns" {
		t.Fatalf("the states should still be there: %+v", reader)
	}
}

func TestOnlyConfiguredAppRoutesAreLookedUp(t *testing.T) {
	var mu sync.Mutex
	seen := []string{}
	proxy := route("proxied.example.org")
	proxy.Mode = "PROXY"
	local := route("app.local")
	disabled := route("off.example.org")
	disabled.Disabled = true
	bad := route("bad_name.example.org")
	fakes(t, []utils.ProxyRouteConfig{route("app.example.org"), route("APP.example.org:2053"), proxy, local, disabled, bad, route("203.0.113.7")},
		func(ctx context.Context, host string) ([]net.IP, error) {
			mu.Lock()
			seen = append(seen, host)
			mu.Unlock()
			return ours(ctx, host)
		})
	serve(request("GET", "admin", "?host=evil.example.com"))
	req := request("POST", "admin", `{"host": "evil.example.com"}`)
	serve(req)
	if strings.Join(seen, ",") != "app.example.org,app.example.org" {
		t.Fatalf("looked up %v, want app.example.org once per run", seen)
	}
}

func TestCacheAndCheckAgain(t *testing.T) {
	asked := fakes(t, []utils.ProxyRouteConfig{route("app.example.org")}, ours)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	clock = func() time.Time { return now }

	serve(request("GET", "reader", ""))
	serve(request("GET", "reader", ""))
	if *asked != 1 {
		t.Fatalf("%d lookups within the cache time", *asked)
	}
	now = now.Add(11 * time.Minute)
	serve(request("GET", "reader", ""))
	if *asked != 2 {
		t.Fatalf("%d lookups after the cache time", *asked)
	}
	_, first := serve(request("POST", "reader", ""))
	_, second := serve(request("POST", "reader", ""))
	if *asked != 3 || first.TooSoon || !second.TooSoon {
		t.Fatalf("check again: %d lookups, tooSoon %v/%v", *asked, first.TooSoon, second.TooSoon)
	}
	now = now.Add(16 * time.Second)
	serve(request("POST", "reader", ""))
	if *asked != 4 {
		t.Fatalf("check again after the pause: %d lookups", *asked)
	}
}

func TestAskPublicIP(t *testing.T) {
	fakes(t, []utils.ProxyRouteConfig{route("app.example.org")}, ours)
	calls := 0
	detectPublicIP = func() (string, error) { calls++; return "203.0.113.7", nil }

	_, before := serve(request("GET", "admin", ""))
	if calls != 0 || before.PublicIPSource != "" || codeOf(before.Routes[0], "dns") != "unknown:publicIPUnknown" {
		t.Fatalf("a GET asked outside or knew the address: %d %+v", calls, before)
	}
	_, after := serve(request("POST", "admin", `{"askPublicIP": true}`))
	if calls != 1 || after.PublicIPSource != "lookup" || codeOf(after.Routes[0], "dns") != "ok:pointsHere" {
		t.Fatalf("look up: %d %+v", calls, after)
	}
	_, again := serve(request("POST", "admin", `{"askPublicIP": true}`))
	if calls != 1 || !again.TooSoon {
		t.Fatalf("second look-up right away was not refused: %d", calls)
	}
}

func TestRunIsBounded(t *testing.T) {
	routes := []utils.ProxyRouteConfig{}
	for i := 0; i < 12; i++ {
		routes = append(routes, route("app"+strconv.Itoa(i)+".example.org"))
	}
	var running, most int32
	fakes(t, routes, func(ctx context.Context, host string) ([]net.IP, error) {
		n := atomic.AddInt32(&running, 1)
		for {
			m := atomic.LoadInt32(&most)
			if n <= m || atomic.CompareAndSwapInt32(&most, m, n) {
				break
			}
		}
		defer atomic.AddInt32(&running, -1)
		<-ctx.Done() // a resolver that never answers
		return nil, ctx.Err()
	})
	parallel, maxHosts = 3, 9
	lookupTimeout, runTimeout = 50*time.Millisecond, 120*time.Millisecond

	start := time.Now()
	_, resp := serve(request("GET", "admin", ""))
	if took := time.Since(start); took > time.Second {
		t.Fatalf("the run took %v", took)
	}
	if most > 3 {
		t.Fatalf("%d lookups at once", most)
	}
	codes := map[string]int{}
	for _, r := range resp.Routes {
		codes[codeOf(r, "dns")]++
	}
	if codes["unknown:tooMany"] != 3 || codes["unknown:lookupFailed"] != 9 {
		t.Fatalf("codes %v", codes)
	}
}

func TestConcurrentRequestsShareOneRun(t *testing.T) {
	asked := fakes(t, []utils.ProxyRouteConfig{route("app.example.org")}, func(ctx context.Context, host string) ([]net.IP, error) {
		time.Sleep(30 * time.Millisecond)
		return ours(ctx, host)
	})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); serve(request("GET", "reader", "")) }()
	}
	wg.Wait()
	if *asked != 1 {
		t.Fatalf("%d lookups for ten requests at once", *asked)
	}
}

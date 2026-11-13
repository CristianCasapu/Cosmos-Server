package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: tests of fork_forwarded.go.

// throughProxy sends a request through the proxy of a route and returns the
// headers the application received.
func throughProxy(t *testing.T, route utils.ProxyRouteConfig, peer string, headers map[string][]string) http.Header {
	t.Helper()
	seen := make(chan http.Header, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
	}))
	t.Cleanup(backend.Close)

	route.Mode = "PROXY"
	route.Target = backend.URL
	proxy, err := NewProxy(backend.URL, false, false, route)
	if err != nil {
		t.Fatal("NewProxy:", err)
	}

	r := httptest.NewRequest("GET", "http://app.example/", nil)
	r.RemoteAddr = peer
	// what the route's own middleware sets before the proxy (routerGen.go)
	r.Header.Set("x-cosmos-user", "alice")
	r.Header.Set("x-cosmos-role", "1")
	for name, values := range headers {
		for _, value := range values {
			r.Header.Add(name, value)
		}
	}
	proxy.ServeHTTP(httptest.NewRecorder(), r)
	return <-seen
}

func TestVisitorCannotRemoveTheHeadersTheProxySets(t *testing.T) {
	utils.LoadBaseMainConfig(utils.Config{})
	route := utils.ProxyRouteConfig{Name: "app", UseHost: true, Host: "app.example", ExtraHeaders: map[string]string{"Remote-User": "$user"}}

	h := throughProxy(t, route, "86.120.1.1:40000", map[string][]string{
		"Connection": {"close, X-Cosmos-User, X-Cosmos-Role, X-Forwarded-For, X-Forwarded-Proto, X-Forwarded-Host, Remote-User, X-Real-IP"},
	})
	for name, want := range map[string]string{
		"X-Cosmos-User":     "alice",
		"X-Cosmos-Role":     "1",
		"Remote-User":       "alice",
		"X-Forwarded-For":   "86.120.1.1",
		"X-Real-Ip":         "86.120.1.1",
		"X-Forwarded-Proto": "http",
	} {
		if got := h.Get(name); got != want {
			t.Errorf("the application received %s = %q, want %q", name, got, want)
		}
	}
	if got := h.Get("X-Forwarded-Host"); got != "app.example" {
		t.Errorf("the application received X-Forwarded-Host = %q, want %q", got, "app.example")
	}
}

func TestVisitorCannotSpellAGuardedHeaderWithUnderscores(t *testing.T) {
	utils.LoadBaseMainConfig(utils.Config{})
	route := utils.ProxyRouteConfig{Name: "app", ExtraHeaders: map[string]string{"Remote-User": "$user"}, DisableLegacyHTTPHeaders: true}

	h := throughProxy(t, route, "86.120.1.1:40000", map[string][]string{
		"X_Cosmos_User":      {"admin"},
		"X_COSMOS_ROLE":      {"2"},
		"x_cosmos_user_role": {"2"},
		"X_Forwarded_For":    {"192.168.1.5"},
		"X_Real_IP":          {"192.168.1.5"},
		"Remote_User":        {"admin"},
		"HTTP_X_COSMOS_USER": {"admin"},
		"HTTP_REMOTE_USER":   {"admin"},
		"X_Unrelated_Thing":  {"kept"},
	})
	for name := range h {
		for _, value := range h.Values(name) {
			if value == "admin" || value == "2" || value == "192.168.1.5" {
				t.Errorf("the application received the visitor's %s: %q", name, value)
			}
		}
	}
	if h.Get("X_unrelated_thing") != "kept" {
		t.Errorf("a header that has nothing to do with the guarded ones was dropped: %v", h)
	}
	if h.Get("X-Cosmos-User") != "alice" || h.Get("Remote-User") != "alice" {
		t.Errorf("the proxy's own headers are wrong: %q, %q", h.Get("X-Cosmos-User"), h.Get("Remote-User"))
	}
}

func TestApplicationIsToldTheAddressThisServerWorksWith(t *testing.T) {
	// no proxy is trusted: whatever the visitor says about its address is dropped
	utils.LoadBaseMainConfig(utils.Config{})
	route := utils.ProxyRouteConfig{Name: "app"}
	h := throughProxy(t, route, "86.120.1.1:40000", map[string][]string{
		"X-Forwarded-For": {"127.0.0.1, 192.168.1.5"},
		"X-Real-IP":       {"192.168.1.5"},
		"Forwarded":       {"for=192.168.1.5;proto=https"},
	})
	if got := h.Values("X-Forwarded-For"); len(got) != 1 || got[0] != "86.120.1.1" {
		t.Errorf("X-Forwarded-For is %q, want the peer's address alone", got)
	}
	if got := h.Get("X-Real-Ip"); got != "86.120.1.1" {
		t.Errorf("X-Real-IP is %q, want the peer's address", got)
	}
	if got := h.Get("Forwarded"); got != "" {
		t.Errorf("the visitor's Forwarded header was passed on: %q", got)
	}

	// behind a trusted proxy: the visitor that proxy saw, then the proxy
	config := utils.Config{}
	config.HTTPConfig.TrustedProxies = []string{"10.0.0.2"}
	utils.LoadBaseMainConfig(config)
	t.Cleanup(func() { utils.LoadBaseMainConfig(utils.Config{}) })
	h = throughProxy(t, route, "10.0.0.2:5000", map[string][]string{
		"X-Forwarded-For": {"192.168.1.5, 86.120.1.1"},
		"X-Real-IP":       {"192.168.1.5"},
	})
	if got := h.Get("X-Forwarded-For"); got != "86.120.1.1, 10.0.0.2" {
		t.Errorf("behind a trusted proxy X-Forwarded-For is %q, want %q", got, "86.120.1.1, 10.0.0.2")
	}
	if got := h.Get("X-Real-Ip"); got != "86.120.1.1" {
		t.Errorf("behind a trusted proxy X-Real-IP is %q, want the visitor the proxy saw", got)
	}
}

func TestWebsocketUpgradeStillPassesTheProxy(t *testing.T) {
	header := http.Header{}
	header.Add("Connection", "keep-alive, Upgrade, X-Cosmos-User")
	keepConnectionTokens(header)
	if got := header.Get("Connection"); got != "keep-alive, Upgrade" {
		t.Errorf("Connection is %q, want %q", got, "keep-alive, Upgrade")
	}
	empty := http.Header{}
	keepConnectionTokens(empty)
	if _, present := empty["Connection"]; present {
		t.Error("a Connection header appeared where there was none")
	}
}

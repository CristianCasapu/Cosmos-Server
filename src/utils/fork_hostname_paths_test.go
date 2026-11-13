package utils

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Fork addition: tests of fork_hostname_paths.go.

func hostnamePathsConfig(t *testing.T) {
	t.Helper()
	prev := GetBaseMainConfig()
	config := Config{}
	config.HTTPConfig.Hostname = "cosmos.example.com"
	config.HTTPConfig.ProxyConfig.Routes = []ProxyRouteConfig{
		{Name: "app", UseHost: true, Host: "app.example.com", Target: "http://127.0.0.1:8096", Mode: "PROXY"},
	}
	LoadBaseMainConfig(config)
	prevMetrics := PushShieldMetrics
	PushShieldMetrics = func(string) {}
	t.Cleanup(func() {
		LoadBaseMainConfig(prev)
		PushShieldMetrics = prevMetrics
	})
}

func askAPIOn(host string, method string, path string) (int, bool) {
	reached := false
	handler := EnsureHostnameCosmosAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	req := httptest.NewRequest(method, "https://"+host+path, nil)
	req.Host = host
	req.RemoteAddr = "192.168.1.20:50000"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code, reached
}

func TestAPIOnTheHostnameOfAnApplication(t *testing.T) {
	hostnamePathsConfig(t)

	answered := []struct{ method, path string }{
		{"POST", "/cosmos/api/login"},
		{"GET", "/cosmos/api/logout"},
		{"GET", "/cosmos/api/status"},
		{"POST", "/cosmos/api/password-reset"},
		{"GET", "/cosmos/api/can-send-email"},
		{"GET", "/cosmos/api/me"},
		{"POST", "/cosmos/api/mfa"},
		{"GET", "/cosmos/oauth2/detect-callback"},
	}
	for _, c := range answered {
		if code, reached := askAPIOn("app.example.com", c.method, c.path); !reached {
			t.Errorf("%s %s should be answered on the hostname of an application, got %d", c.method, c.path, code)
		}
	}

	refused := []struct{ method, path string }{
		// a second factor is set up and removed on the server's own hostname
		{"GET", "/cosmos/api/mfa"},
		{"DELETE", "/cosmos/api/mfa"},
		// paths that merely start like an allowed one
		{"GET", "/cosmos/api/metrics"},
		{"POST", "/cosmos/api/merge"},
		{"GET", "/cosmos/api/mfa/x"},
		{"GET", "/cosmos/api/status-of-something"},
		{"GET", "/cosmos/api/loginx"},
		// outside /cosmos/api
		{"GET", "/cosmos/_logs"},
		{"GET", "/cosmos/rclone/config/listremotes"},
		{"POST", "/cosmos/rclone/config/create"},
		// the rest of the API, as before
		{"GET", "/cosmos/api/config"},
		{"GET", "/cosmos/api/users"},
	}
	for _, c := range refused {
		if code, reached := askAPIOn("app.example.com", c.method, c.path); reached || code != http.StatusBadRequest {
			t.Errorf("%s %s must be refused on the hostname of an application, got %d (reached %v)", c.method, c.path, code, reached)
		}
	}

	// on the server's own hostname everything passes this check
	for _, c := range append(answered, refused...) {
		if code, reached := askAPIOn("cosmos.example.com:2053", c.method, c.path); !reached {
			t.Errorf("%s %s should pass on the server's own hostname, got %d", c.method, c.path, code)
		}
	}
}

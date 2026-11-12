package proxy

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsLocalAddress(t *testing.T) {
	local := []string{"jellyfin.local", "Jellyfin.LOCAL", "jellyfin.local:2053", "172.16.0.14:7200", "192.168.1.10", "10.0.0.5:80", "127.0.0.1", "[fe80::1]:443", "app.local."}
	for _, host := range local {
		if !isLocalAddress(host) {
			t.Errorf("%q should be a local address", host)
		}
	}
	external := []string{"", "jellyfin.example.com", "jellyfin.example.com:2053", "local", "notlocal.localdomain.com", "8.8.8.8", "8.8.8.8:443", "[2a02:2f04::1]:443", ":8080", "my.local.example.com"}
	for _, host := range external {
		if isLocalAddress(host) {
			t.Errorf("%q should not be a local address", host)
		}
	}
}

func TestIsLocalVisitor(t *testing.T) {
	_, lan6, _ := net.ParseCIDR("2a02:2f04:a:1084::/64")
	networks := []*net.IPNet{lan6}

	for _, address := range []string{"172.16.0.20", "192.168.0.9", "10.1.2.3", "127.0.0.1", "::1", "fe80::1234", "100.101.102.103", "2a02:2f04:a:1084::beef", "fd00::1"} {
		if !isLocalVisitor(address, networks) {
			t.Errorf("%s should count as local", address)
		}
	}
	for _, address := range []string{"8.8.8.8", "86.120.1.1", "2a02:2f04:a:1085::1", "2001:4860:4860::8888", "", "not-an-ip", "172.32.0.1", "100.128.0.1"} {
		if isLocalVisitor(address, networks) {
			t.Errorf("%s should not count as local", address)
		}
	}
}

func TestLocalNetworkOnly(t *testing.T) {
	reached := 0
	handler := localNetworkOnly(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { reached++ }))

	request := func(remote string) int {
		req := httptest.NewRequest("GET", "https://jellyfin.local/", nil)
		req.RemoteAddr = remote
		// a forged header from an untrusted peer must not turn an outsider into a local visitor
		req.Header.Set("X-Forwarded-For", "192.168.1.5")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := request("86.120.1.1:40000"); code != http.StatusForbidden || reached != 0 {
		t.Errorf("an outside visitor got %d (handler reached %d times)", code, reached)
	}
	if code := request("192.168.1.77:40000"); code != http.StatusOK || reached != 1 {
		t.Errorf("a local visitor got %d (handler reached %d times)", code, reached)
	}
}

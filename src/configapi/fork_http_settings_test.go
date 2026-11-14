package configapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// fakeHTTPHost replaces what the checks read from the machine: who listens
// where, and what containers publish.
func fakeHTTPHost(t *testing.T, busy map[int]error, containers map[int]string) {
	t.Helper()
	oldProbe, oldPublished, oldSee := listenProbe, publishedPorts, cannotSeeHostPorts
	listenProbe = func(port int) error { return busy[port] }
	publishedPorts = func() (map[int]string, error) { return containers, nil }
	cannotSeeHostPorts = func() bool { return false }
	t.Cleanup(func() { listenProbe, publishedPorts, cannotSeeHostPorts = oldProbe, oldPublished, oldSee })
}

func requestFrom(client string) *http.Request {
	req := httptest.NewRequest("PUT", "/cosmos/api/config", nil)
	req.RemoteAddr = client + ":51000"
	return req.WithContext(context.WithValue(req.Context(), "ClientID", client))
}

func storedHTTP() utils.Config {
	var c utils.Config
	c.HTTPConfig.Hostname = "cosmos.example.com"
	c.HTTPConfig.HTTPPort = "80"
	c.HTTPConfig.HTTPSPort = "443"
	return c
}

func TestHTTPSettingsUnchangedNeverRefused(t *testing.T) {
	// an odd value saved earlier must not block saving something else
	fakeHTTPHost(t, map[int]error{80: syscall.EADDRINUSE, 443: syscall.EADDRINUSE}, nil)
	stored := storedHTTP()
	stored.HTTPConfig.Hostname = "odd name"
	stored.AdminWhitelistIPs = []string{"not-an-ip"}
	request := stored
	if p := checkHTTPSettings(requestFrom("192.168.1.5"), &request, stored); p != nil {
		t.Fatalf("unchanged settings refused: %+v", p)
	}
}

func TestHTTPSettingsHostname(t *testing.T) {
	fakeHTTPHost(t, nil, nil)
	for name, ok := range map[string]bool{
		"cosmos.example.com":         true,
		"192.168.1.10":               true,
		"https://cosmos.example.com": false,
		"cosmos.example.com/panel":   false,
		"two words":                  false,
	} {
		stored := storedHTTP()
		request := stored
		request.HTTPConfig.Hostname = name
		p := checkHTTPSettings(requestFrom("192.168.1.5"), &request, stored)
		if ok != (p == nil) || (p != nil && p.Code != "UC030") {
			t.Errorf("%q: got %+v", name, p)
		}
	}
}

func TestHTTPSettingsPorts(t *testing.T) {
	fakeHTTPHost(t, map[int]error{8081: syscall.EADDRINUSE, 1000: syscall.EACCES}, map[int]string{8443: "nextcloud"})
	cases := []struct {
		http, https, code string
	}{
		{"8080", "8444", ""},
		{"443", "80", ""},        // a swap: both are Cosmos' own today
		{"8o80", "443", "UC031"}, // passes upstream's validate tag
		{"0", "443", "UC031"},
		{"99999", "443", "UC031"},
		{"8080", "8080", "UC031"},
		{"8080", "8443", "UC032"}, // a container publishes it
		{"8081", "443", "UC032"},  // another program listens on it
		{"1000", "443", ""},       // cannot be told (no right to bind it here): not refused
	}
	for _, c := range cases {
		stored := storedHTTP()
		request := stored
		request.HTTPConfig.HTTPPort, request.HTTPConfig.HTTPSPort = c.http, c.https
		p := checkHTTPSettings(requestFrom("192.168.1.5"), &request, stored)
		got := ""
		if p != nil {
			got = p.Code
		}
		if got != c.code {
			t.Errorf("%s/%s: got %q (%+v), want %q", c.http, c.https, got, p, c.code)
		}
		if c.code == "UC032" && c.https == "8443" && !strings.Contains(p.Message, "nextcloud") {
			t.Errorf("the container is not named: %s", p.Message)
		}
	}
}

func TestHTTPSettingsAdminWhitelistLockOut(t *testing.T) {
	// the real restriction check, as the admin routes apply it
	fakeHTTPHost(t, nil, nil)
	cases := []struct {
		list []string
		code string
	}{
		{[]string{"10.0.0.0/8"}, "UC034"},
		{[]string{"192.168.1.0/24"}, ""},
		{[]string{" 192.168.1.5 ", ""}, ""},
		{[]string{"192.168.1.5", "garbage"}, "UC033"},
		{[]string{}, ""}, // no list: everyone, as before
	}
	for _, c := range cases {
		stored := storedHTTP()
		stored.AdminWhitelistIPs = []string{"192.168.0.0/16", "10.0.0.1"}
		request := stored
		request.AdminWhitelistIPs = c.list
		p := checkHTTPSettings(requestFrom("192.168.1.5"), &request, stored)
		got := ""
		if p != nil {
			got = p.Code
		}
		if got != c.code {
			t.Errorf("%v: got %q (%+v), want %q", c.list, got, p, c.code)
		}
		if c.code == "UC034" && !strings.Contains(p.Message, "192.168.1.5") {
			t.Errorf("the message does not say the address in use: %s", p.Message)
		}
	}
}

func TestHTTPSettingsConstellationOnlyLockOut(t *testing.T) {
	fakeHTTPHost(t, nil, nil)
	stored := storedHTTP()
	request := stored
	request.AdminConstellationOnly = true
	if p := checkHTTPSettings(requestFrom("203.0.113.9"), &request, stored); p == nil || p.Code != "UC034" {
		t.Fatalf("switching to Constellation only from outside it was not refused: %+v", p)
	}
}

func TestHTTPSettingsTrustedProxies(t *testing.T) {
	fakeHTTPHost(t, nil, nil)
	stored := storedHTTP()
	request := stored
	request.HTTPConfig.TrustedProxies = []string{"172.17.0.1", "10.0.0.0/8", "https://www.cloudflare.com/ips-v4"}
	if p := checkHTTPSettings(requestFrom("192.168.1.5"), &request, stored); p != nil {
		t.Fatalf("valid proxies refused: %+v", p)
	}
	request.HTTPConfig.TrustedProxies = []string{"my router"}
	if p := checkHTTPSettings(requestFrom("192.168.1.5"), &request, stored); p == nil || p.Code != "UC035" {
		t.Fatalf("an invalid proxy was accepted: %+v", p)
	}
}

func TestHTTPCheckAnswer(t *testing.T) {
	fakeHTTPHost(t, map[int]error{8081: syscall.EADDRINUSE}, map[int]string{9000: "portainer"})
	oldAddrs := serverAddresses
	serverAddresses = func() []serverAddress { return []serverAddress{{IP: "192.168.1.10", Network: "192.168.1.0/24"}} }
	t.Cleanup(func() { serverAddresses = oldAddrs })

	cfg := storedHTTP()
	cases := map[string]string{"80": portCosmos, "8081": portOther, "9000": portContainer, "8080": portFree, "x": portInvalid}
	for value, want := range cases {
		answer := httpCheck(requestFrom("192.168.1.5"), httpCheckRequest{HTTPPort: value, HTTPSPort: "443"}, cfg)
		if answer.Ports[0].State != want {
			t.Errorf("port %s: %s, want %s", value, answer.Ports[0].State, want)
		}
	}
	answer := httpCheck(requestFrom("192.168.1.5"), httpCheckRequest{HTTPPort: "80", HTTPSPort: "443", AdminWhitelist: []string{"10.0.0.0/8", "nope"}}, cfg)
	if answer.Whitelist.Passes || len(answer.Whitelist.Entries) != 2 || answer.Whitelist.Entries[1].Valid {
		t.Errorf("whitelist: %+v", answer.Whitelist)
	}
	if answer.You.Address != "192.168.1.5" || !answer.You.Local || answer.You.ThroughProxy {
		t.Errorf("you: %+v", answer.You)
	}
	if len(answer.Ports) != 2 {
		t.Errorf("only the two ports asked for are probed: %+v", answer.Ports)
	}
}

func TestHTTPCheckRouteBounds(t *testing.T) {
	// unknown fields and large bodies are refused before anything is probed
	probed := false
	fakeHTTPHost(t, nil, nil)
	listenProbe = func(int) error { probed = true; return nil }
	for _, body := range []string{
		`{"httpPort":"80","host":"10.0.0.1"}`,
		`{"httpPort":"` + strings.Repeat("1", 2000) + `"}`,
	} {
		req := httptest.NewRequest("POST", "/cosmos/api/config/http-check", strings.NewReader(body))
		var parsed httpCheckRequest
		if decodeHTTPCheck(req, &parsed) == nil {
			t.Errorf("accepted: %s", body[:20])
		}
	}
	if probed {
		t.Error("a refused request probed a port")
	}
	for i := 0; i < httpChecksATminute; i++ {
		if !httpCheckLimit.allow(timeNow()) {
			t.Fatal("refused under the limit")
		}
	}
	if httpCheckLimit.allow(timeNow()) {
		t.Fatal("the limit is not enforced")
	}
}

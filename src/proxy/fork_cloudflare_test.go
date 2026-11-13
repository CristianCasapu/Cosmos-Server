package proxy

// Fork addition: tests of fork_cloudflare.go, the guard of the hostnames
// served through Cloudflare.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestShieldedHostnamesAnswerCloudflareAndTheLocalNetworkOnly(t *testing.T) {
	previous := utils.GetBaseMainConfig()
	config := utils.Config{}
	// the panel on the domain itself and one application through Cloudflare, one direct
	config.HTTPConfig.Hostname = "example.org"
	config.HTTPConfig.Cloudflare.Shielded = []string{"example.org", "radarr.example.org"}
	config.HTTPConfig.Cloudflare.TrustRanges = true
	utils.LoadBaseMainConfig(config)
	t.Cleanup(func() { utils.LoadBaseMainConfig(previous) })

	events := captureSecurity(t)
	served := 0
	guarded := CloudflareOriginGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		w.WriteHeader(http.StatusNoContent)
	}))

	const cloudflare, cloudflareV6, stranger, local = "173.245.48.9:40000", "[2606:4700::9]:40000", "198.51.100.66:50000", "192.168.1.23:51000"
	cases := []struct {
		name   string
		host   string
		peer   string
		header map[string]string
		want   int
	}{
		{"through Cloudflare", "radarr.example.org:2053", cloudflare, nil, 204},
		{"through Cloudflare over IPv6", "radarr.example.org:2053", cloudflareV6, nil, 204},
		{"the panel through Cloudflare", "example.org:2053", cloudflare, nil, 204},
		{"around Cloudflare", "radarr.example.org:2053", stranger, nil, 403},
		{"the panel around Cloudflare", "example.org:2053", stranger, nil, 403},
		{"around Cloudflare, written differently", "RADARR.example.org.:2053", stranger, nil, 403},
		{"around Cloudflare, claiming to be Cloudflare", "radarr.example.org", stranger,
			map[string]string{"X-Forwarded-For": "173.245.48.9", "CF-Connecting-IP": "173.245.48.9", "X-Real-IP": "173.245.48.9", "Forwarded": "for=173.245.48.9"}, 403},
		{"around Cloudflare, claiming to be local", "radarr.example.org", stranger,
			map[string]string{"X-Forwarded-For": "192.168.1.23", "CF-Connecting-IP": "127.0.0.1", "X-Real-IP": "192.168.1.23"}, 403},

		// the owner at home: the shielded names by the local network, and the
		// ways in that are not names of Cloudflare's at all
		{"a device at home asks for the shielded name", "radarr.example.org:2053", local, nil, 204},
		{"a device at home asks for the panel's name", "example.org:2053", local, nil, 204},
		{"the server itself", "example.org:2053", "127.0.0.1:40000", nil, 204},
		{"the panel by the server's local address", "192.168.1.10:2053", local, nil, 204},
		{"an application by its local name", "radarr.local", local, nil, 204},
		// a name that is not shielded is not this guard's business, whoever asks
		{"a direct application, from anywhere", "jellyfin.example.org:2053", stranger, nil, 204},
		{"the server's address as the name, from anywhere", "192.168.1.10:2053", stranger, nil, 204},
		{"a local name, from anywhere (the local guard's business)", "radarr.local", stranger, nil, 204},
	}
	refused := 0
	for _, c := range cases {
		req := httptest.NewRequest("GET", "https://"+c.host+"/", nil)
		req.Host = c.host
		req.RemoteAddr = c.peer
		for name, value := range c.header {
			req.Header.Set(name, value)
		}
		recorder := httptest.NewRecorder()
		guarded.ServeHTTP(recorder, req)
		if recorder.Code != c.want {
			t.Errorf("%s: status %d, want %d", c.name, recorder.Code, c.want)
		}
		if c.want == 403 {
			refused++
		}
	}
	if served != len(cases)-refused {
		t.Errorf("%d requests reached the application, want %d", served, len(cases)-refused)
	}

	recorded := 0
	for _, event := range events() {
		if event.Kind == utils.SecAccessOriginDenied {
			recorded++
			if event.IP != "198.51.100.66" {
				t.Errorf("the refusal names %q, not the peer", event.IP)
			}
		}
	}
	if recorded != refused {
		t.Errorf("%d refusals were recorded, want %d", recorded, refused)
	}

	// nothing shielded: the guard does nothing, even without any ranges
	config.HTTPConfig.Cloudflare = utils.ForkCloudflareConfig{}
	utils.LoadBaseMainConfig(config)
	req := httptest.NewRequest("GET", "https://radarr.example.org/", nil)
	req.RemoteAddr = stranger
	recorder := httptest.NewRecorder()
	guarded.ServeHTTP(recorder, req)
	if recorder.Code != 204 {
		t.Errorf("with nothing shielded: status %d", recorder.Code)
	}

	// a range list in the configuration that is no list of Cloudflare's does
	// not open the guard to the world: the built-in one is used
	config.HTTPConfig.Cloudflare = utils.ForkCloudflareConfig{Shielded: []string{"radarr.example.org"}, RangesV4: []string{"0.0.0.0/0"}, RangesV6: []string{"::/0"}}
	utils.LoadBaseMainConfig(config)
	req = httptest.NewRequest("GET", "https://radarr.example.org/", nil)
	req.RemoteAddr = stranger
	recorder = httptest.NewRecorder()
	guarded.ServeHTTP(recorder, req)
	if recorder.Code != 403 {
		t.Errorf("with a broken range list: status %d", recorder.Code)
	}
}

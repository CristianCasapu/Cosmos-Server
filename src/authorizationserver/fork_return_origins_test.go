package authorizationserver

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ory/fosite"
)

// Fork addition: tests of fork_return_origins.go.

func TestReturnOriginSource(t *testing.T) {
	for uri, want := range map[string]string{
		"https://app.example.com/oauth/callback?x=1": "https://app.example.com",
		"https://APP.example.com:8443/cb":            "https://app.example.com:8443",
		"http://jellyfin.server.local:2052/cb":          "http://jellyfin.server.local:2052",
		"http://127.0.0.1/callback":                  "http://127.0.0.1:*",
		"http://127.0.0.1:5000/callback":             "http://127.0.0.1:*",
		"http://localhost:3000/cb":                   "http://localhost:*",
		"com.example.app:/oauth2redirect":            "com.example.app:",
		"  https://padded.example.com/cb  ":          "https://padded.example.com",
	} {
		got, ok := returnOriginSource(uri)
		if !ok || got != want {
			t.Errorf("%q: %q %v, want %q", uri, got, ok, want)
		}
	}
	// nothing that could end the directive or the header, nothing that is not an address
	for _, uri := range []string{
		"",
		"https://evil.example.com;script-src *",
		"https://a b.example.com/cb",
		"https://*.example.com/cb",
		"https://app.example.com:80x/cb",
		"https:///nohost",
		"/relative/path",
		"https://app.example.com\r\nX-Injected: 1",
		"https://" + strings.Repeat("a", 2100) + ".example.com/cb",
		"1http://bad.scheme/",
		// browsers refuse IPv6 addresses in a policy
		"http://[::1]:8080/cb",
		"https://[2001:db8::1]:9000/cb",
	} {
		if got, ok := returnOriginSource(uri); ok {
			t.Errorf("%q gave %q, want nothing", uri, got)
		}
	}
}

func TestRememberReturnOrigins(t *testing.T) {
	t.Cleanup(func() { returnOrigins.Store([]string(nil)) })
	rememberReturnOrigins(map[string]fosite.Client{
		"a": &fosite.DefaultClient{ID: "a", RedirectURIs: []string{"https://app.example.com/cb", "https://app.example.com/other", "https://bad;x/"}},
		"b": &fosite.DefaultClient{ID: "b", RedirectURIs: []string{"https://cosmos.example.com/cosmos/api/oauth2/callback", "com.example.app:/cb"}},
	})
	want := []string{"com.example.app:", "https://app.example.com", "https://cosmos.example.com"}
	if got := ReturnOrigins(); !reflect.DeepEqual(got, want) {
		t.Fatalf("%q, want %q", got, want)
	}

	many := map[string]fosite.Client{}
	for i := 0; i < maxReturnOrigins+50; i++ {
		id := strings.Repeat("x", i%7) + string(rune('a'+i%26)) + strings.Repeat("y", i/26)
		many[id] = &fosite.DefaultClient{ID: id, RedirectURIs: []string{"https://" + id + ".example.com/cb"}}
	}
	rememberReturnOrigins(many)
	if got := len(ReturnOrigins()); got != maxReturnOrigins {
		t.Errorf("%d origins kept, want at most %d", got, maxReturnOrigins)
	}
}

// Init hands the clients over once all of them are registered
func TestInitRemembersReturnOrigins(t *testing.T) {
	source, err := os.ReadFile("oauth2.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	home := strings.Index(text, "addHomeNameRedirects(store.Clients)")
	remember := strings.Index(text, "rememberReturnOrigins(store.Clients)")
	if home < 0 || remember < home {
		t.Error("Init must call rememberReturnOrigins after the last client is added")
	}
}

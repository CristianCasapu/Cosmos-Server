package utils

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSetCORSHeaders(t *testing.T) {
	for name, tc := range map[string]struct {
		allowed    string
		origin     string
		wantOrigin string
		wantCreds  bool
		wantVary   bool
	}{
		"nothing configured": {
			allowed: "", origin: "https://app.example.com",
		},
		"public: no credentials with a wildcard": {
			allowed: "*", origin: "https://app.example.com",
			wantOrigin: "*",
		},
		"wildcard inside a list still wins": {
			allowed: "https://a.example.com, *", origin: "https://app.example.com",
			wantOrigin: "*",
		},
		"full origin echoed with credentials": {
			allowed: "https://app.example.com", origin: "https://app.example.com",
			wantOrigin: "https://app.example.com", wantCreds: true, wantVary: true,
		},
		"bare host matches any scheme": {
			allowed: "app.example.com", origin: "http://app.example.com",
			wantOrigin: "http://app.example.com", wantCreds: true, wantVary: true,
		},
		"bare host with port": {
			allowed: "nas.local:8443", origin: "https://nas.local:8443",
			wantOrigin: "https://nas.local:8443", wantCreds: true, wantVary: true,
		},
		"list, second entry matches": {
			allowed: "https://a.example.com,https://b.example.com", origin: "https://b.example.com",
			wantOrigin: "https://b.example.com", wantCreds: true, wantVary: true,
		},
		"case does not matter": {
			allowed: "https://App.Example.com", origin: "https://app.example.com",
			wantOrigin: "https://app.example.com", wantCreds: true, wantVary: true,
		},
		"other origin gets nothing": {
			allowed: "https://app.example.com", origin: "https://evil.example.com",
			wantVary: true,
		},
		"port must match": {
			allowed: "nas.local:8443", origin: "https://nas.local",
			wantVary: true,
		},
		"no Origin header (same-origin or non-browser)": {
			allowed: "https://app.example.com", origin: "",
			wantVary: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/", nil)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			SetCORSHeaders(w, r, tc.allowed)

			h := w.Header()
			if got := h.Get("Access-Control-Allow-Origin"); got != tc.wantOrigin {
				t.Errorf("Allow-Origin = %q, want %q", got, tc.wantOrigin)
			}
			if got := h.Get("Access-Control-Allow-Credentials") == "true"; got != tc.wantCreds {
				t.Errorf("Allow-Credentials = %v, want %v", got, tc.wantCreds)
			}
			if got := h.Get("Vary") == "Origin"; got != tc.wantVary {
				t.Errorf("Vary = %q, want Origin: %v", h.Get("Vary"), tc.wantVary)
			}
		})
	}
}

// a wildcard never carries credentials, even if a previous middleware set them
func TestPublicCORSDropsCredentials(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "https://app.example.com")

	PublicCORS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(w, r)

	if w.Header().Get("Access-Control-Allow-Origin") != "*" || w.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("got %v", w.Header())
	}
}

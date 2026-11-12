package utils

import "testing"

func TestIsValidHostnameSyntax(t *testing.T) {
	for h, want := range map[string]bool{
		"server.example.com":         true,
		"Server.Example.com":         true,
		"node2.example.com:8443":     true,
		"cosmos.local":               true,
		"myserver":                   true,
		"192.168.1.10":               true,
		"192.168.1.10:8080":          true,
		"[::1]:443":                  true,
		"server.example.com/":        false,
		"https://server.example.com": false,
		"server.example.com/path":    false,
		"server..example.com":        false,
		"-bad.example.com":           false,
		"server.example.com:0":       false,
		"server.example.com:99999":   false,
		"server example.com":         false,
		"":                           false,
	} {
		if got := IsValidHostnameSyntax(h); got != want {
			t.Errorf("IsValidHostnameSyntax(%q) = %v, want %v", h, got, want)
		}
	}
}

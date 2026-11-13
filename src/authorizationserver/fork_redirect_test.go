package authorizationserver

import "testing"

func TestLocalPath(t *testing.T) {
	kept := []string{"/", "/web/index.html", "/a/b/c", "/search?q=1", "/with space", "/ünïcode", "/a//b"}
	for _, path := range kept {
		if got := localPath(path); got != path {
			t.Errorf("localPath(%q) = %q, want it unchanged", path, got)
		}
	}
	replaced := []string{"", "web", "//evil.example", "//evil.example/x", `/\evil.example`, `/\/evil.example`, `\\evil.example`, `/a\b`, "https://evil.example/", "/x\r\nSet-Cookie: a=b", "/tab\tin"}
	for _, path := range replaced {
		if got := localPath(path); got != "/" {
			t.Errorf("localPath(%q) = %q, want /", path, got)
		}
	}
}

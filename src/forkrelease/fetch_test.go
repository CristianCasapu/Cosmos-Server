package forkrelease

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Fork addition: tests of the updater's HTTP rules, against a local server
// that stands in for every host: nothing here talks to GitHub.

// fakeGitHub is a TLS server that every connection of the returned fetcher
// lands on, whatever host the address names. connections counts them.
func fakeGitHub(t *testing.T, handler http.Handler) (f fetcher, connections *atomic.Int64) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	connections = &atomic.Int64{}
	address := server.Listener.Addr().String()
	return fetcher{
		dial: func(ctx context.Context, network string, _ string) (net.Conn, error) {
			connections.Add(1)
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, address)
		},
		tlsConfig: &tls.Config{InsecureSkipVerify: true}, // the stand-in has no certificate for github.com
	}, connections
}

func TestOnlyGitHubHosts(t *testing.T) {
	for _, host := range []string{"github.com", "api.github.com", "GitHub.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"} {
		if !githubHost(host) {
			t.Errorf("%s is refused", host)
		}
	}
	for _, host := range []string{"", "example.com", "github.com.evil.example", "evilgithub.com", "gist.github.com", "raw.github.com",
		"githubusercontent.com", ".githubusercontent.com", "githubusercontent.com.evil.example", "evilgithubusercontent.com",
		"api.github.com.", "127.0.0.1", "localhost", "github.io", "x.github.io"} {
		if githubHost(host) {
			t.Errorf("%s is accepted", host)
		}
	}
}

func TestFetchRefusesOtherAddressesWithoutConnecting(t *testing.T) {
	f, connections := fakeGitHub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	for _, address := range []string{
		"http://github.com/o/r/releases/download/v1/x.zip",
		"https://example.com/o/r/releases/download/v1/x.zip",
		"https://github.com.evil.example/x",
		"https://github.com:8443/x",
		"https://user:pass@github.com/x",
		"https://127.0.0.1/x",
		"file:///etc/passwd",
		"ftp://github.com/x",
		"//github.com/x",
		"github.com/x",
		"https://github.com\\@evil.example/x",
		"",
	} {
		if _, err := f.get(address, 100, 5*time.Second); !errors.Is(err, ErrAddress) {
			t.Errorf("%q: %v, want it refused as an address", address, err)
		}
		if _, err := f.download(filepath.Join(t.TempDir(), "f"), address, 100, 5*time.Second); !errors.Is(err, ErrAddress) {
			t.Errorf("%q (download): %v, want it refused as an address", address, err)
		}
	}
	if n := connections.Load(); n != 0 {
		t.Errorf("%d connection(s) were opened for refused addresses", n)
	}
	if body, err := f.get("https://github.com/o/r/releases/download/v1/x.sig", 100, 5*time.Second); err != nil || string(body) != "ok" {
		t.Errorf("github.com: %q %v", body, err)
	}
}

func TestFetchFollowsRedirectsOnlyToGitHub(t *testing.T) {
	var reachedEvil atomic.Bool
	handler := http.NewServeMux()
	handler.HandleFunc("/to-assets", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://release-assets.githubusercontent.com/file", http.StatusFound)
	})
	handler.HandleFunc("/file", func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "release-assets.githubusercontent.com" {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		w.Write([]byte("the file"))
	})
	handler.HandleFunc("/to-evil", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example/payload", http.StatusFound)
	})
	handler.HandleFunc("/to-http", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://github.com/payload", http.StatusFound)
	})
	handler.HandleFunc("/to-port", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://github.com:8443/payload", http.StatusFound)
	})
	handler.HandleFunc("/to-local", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://127.0.0.1/payload", http.StatusFound)
	})
	handler.HandleFunc("/payload", func(w http.ResponseWriter, r *http.Request) {
		reachedEvil.Store(true)
		w.Write([]byte("evil"))
	})
	handler.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://github.com/loop", http.StatusFound)
	})
	f, _ := fakeGitHub(t, handler)

	if body, err := f.get("https://github.com/to-assets", 100, 5*time.Second); err != nil || string(body) != "the file" {
		t.Errorf("a redirect to GitHub's asset host: %q %v", body, err)
	}
	for _, path := range []string{"/to-evil", "/to-http", "/to-port", "/to-local"} {
		if body, err := f.get("https://github.com"+path, 100, 5*time.Second); !errors.Is(err, ErrAddress) {
			t.Errorf("%s: %q %v, want the redirect refused", path, body, err)
		}
		target := filepath.Join(t.TempDir(), "f")
		if _, err := f.download(target, "https://github.com"+path, 100, 5*time.Second); !errors.Is(err, ErrAddress) {
			t.Errorf("%s (download): %v, want the redirect refused", path, err)
		}
		if _, err := os.Stat(target); err == nil {
			t.Errorf("%s: a file was written", path)
		}
	}
	if reachedEvil.Load() {
		t.Error("a refused redirect was followed")
	}
	if _, err := f.get("https://github.com/loop", 100, 5*time.Second); err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Errorf("an endless redirect: %v", err)
	}
}

func TestFetchBounds(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/exact", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat("a", 100))) })
	handler.HandleFunc("/large", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat("a", 101))) })
	handler.HandleFunc("/large-unannounced", func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		for i := 0; i < 5; i++ {
			w.Write([]byte(strings.Repeat("a", 50)))
			flusher.Flush()
		}
	})
	handler.HandleFunc("/missing", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	handler.HandleFunc("/error", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("the body of an error page"))
	})
	handler.HandleFunc("/partial", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte("part"))
	})
	handler.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	})
	handler.HandleFunc("/slow-body", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("start"))
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	})
	handler.HandleFunc("/cut", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.Write([]byte("only this"))
		if hijacker, ok := w.(http.Hijacker); ok {
			conn, _, _ := hijacker.Hijack()
			conn.Close()
		}
	})
	f, _ := fakeGitHub(t, handler)
	get := func(path string, total time.Duration) ([]byte, error) {
		return f.get("https://github.com"+path, 100, total)
	}

	if body, err := get("/exact", 5*time.Second); err != nil || len(body) != 100 {
		t.Errorf("an answer of exactly the limit: %d bytes, %v", len(body), err)
	}
	for _, path := range []string{"/large", "/large-unannounced"} {
		if body, err := get(path, 5*time.Second); !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: %d bytes, %v, want it refused as too large", path, len(body), err)
		}
	}
	for path, status := range map[string]string{"/missing": "404", "/error": "500", "/partial": "206"} {
		if body, err := get(path, 5*time.Second); err == nil || !strings.Contains(err.Error(), status) {
			t.Errorf("%s: %q %v, want the status %s refused", path, body, err, status)
		}
	}
	for _, path := range []string{"/slow", "/slow-body"} {
		started := time.Now()
		if body, err := get(path, 700*time.Millisecond); err == nil {
			t.Errorf("%s: answered %q", path, body)
		}
		if took := time.Since(started); took > 5*time.Second {
			t.Errorf("%s: gave up after %s, the limit was 700ms", path, took)
		}
	}

	// downloads: the same bounds, and no file is left behind
	dir := t.TempDir()
	for _, path := range []string{"/large", "/large-unannounced", "/missing", "/error", "/slow-body", "/cut"} {
		target := filepath.Join(dir, "download")
		if sum, err := f.download(target, "https://github.com"+path, 100, 700*time.Millisecond); err == nil {
			t.Errorf("download of %s: accepted with checksum %s", path, sum)
		}
		if _, err := os.Lstat(target); err == nil {
			t.Errorf("download of %s: the file was left behind", path)
			os.Remove(target)
		}
	}
}

func TestDownloadWritesAPrivateNewFile(t *testing.T) {
	content := strings.Repeat("zip content ", 1000)
	f, _ := fakeGitHub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(content)) }))
	dir := t.TempDir()
	target := filepath.Join(dir, "cosmos-update.zip.part")

	// something lies at the name already: a link to a file elsewhere
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("untouched"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, target); err != nil {
		t.Fatal(err)
	}

	sum, err := f.download(target, "https://github.com/o/r/releases/download/v1/x.zip", int64(len(content)), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if sum != sha256Hex([]byte(content)) {
		t.Errorf("checksum %s, want %s", sum, sha256Hex([]byte(content)))
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Errorf("the download is %v (%v), want a regular file readable by its owner only", info.Mode(), err)
	}
	if written, _ := os.ReadFile(target); string(written) != content {
		t.Error("the file does not hold what was sent")
	}
	if kept, _ := os.ReadFile(victim); string(kept) != "untouched" {
		t.Error("the download was written through the link")
	}
}

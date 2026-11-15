package anyimage

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func fakeHub(t *testing.T, handler http.HandlerFunc) {
	server := httptest.NewTLSServer(handler)
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	testRoots = pool
	resetClient()
	checkAddr = func(a netip.Addr, inside bool, own []netip.Addr) bool { return !a.IsLoopback() }
	old := hubAPI
	hubAPI = server.URL
	searchMu.Lock()
	searchCache = map[string]struct {
		at      time.Time
		results []Result
	}{}
	searchMu.Unlock()
	searchWindow = &window{perMin: 20, atOnce: 2}
	t.Cleanup(func() {
		server.Close()
		hubAPI = old
		testRoots = nil
		resetClient()
		checkAddr = refusedAddr
	})
}

func TestSearchOfficialFirstThenPulls(t *testing.T) {
	asked := 0
	fakeHub(t, func(w http.ResponseWriter, r *http.Request) {
		asked++
		if r.URL.Path != "/v2/search/repositories/" || r.URL.Query().Get("query") != "postgres" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected request %s %v", r.URL, r.Header)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"results": []map[string]interface{}{
			{"repo_name": "cimg/postgres", "short_description": "CI", "pull_count": 900, "star_count": 9},
			{"repo_name": "bitnami/postgresql", "short_description": "B\x07itnami", "pull_count": 5000},
			{"repo_name": "postgres", "short_description": "The PostgreSQL database", "pull_count": 100, "is_official": true, "star_count": 15000},
			{"repo_name": "Bad Name", "pull_count": 99999},
		}})
	})
	results, err := search(context.Background(), "postgres")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, r := range results {
		names = append(names, r.Name)
	}
	if strings.Join(names, ",") != "postgres,bitnami/postgresql,cimg/postgres" {
		t.Fatal(names)
	}
	if !results[0].Official || results[0].Recipe != "postgres" || strings.ContainsRune(results[1].Description, 7) {
		t.Fatalf("%+v", results)
	}
	search(context.Background(), "postgres")
	if asked != 1 {
		t.Fatal("not cached")
	}
}

func TestSearchIsRateLimited(t *testing.T) {
	fakeHub(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"results":[]}`)) })
	searchWindow = &window{perMin: 3, atOnce: 2}
	var last error
	for i := 0; i < 5; i++ {
		_, last = search(context.Background(), "q"+string(rune('a'+i)))
	}
	if last != errBusy || codeOf(last) != "busy" {
		t.Fatal(last)
	}
}

func TestSearchAnswers(t *testing.T) {
	fakeHub(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) })
	if _, err := search(context.Background(), "redis"); err != errRateLimited {
		t.Fatal(err)
	}
	fakeHub(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat("x", 600<<10))) })
	if _, err := search(context.Background(), "redis"); err != errTooLarge {
		t.Fatal(err)
	}
}

func TestNormalQuery(t *testing.T) {
	for text, want := range map[string]string{
		"Postgres": "postgres", " redis ": "redis", "ghcr.io/a": "ghcr.io/a",
		"a b": "", "": "", "-x": "", "<script>": "", "https://x": "", strings.Repeat("a", 65): "",
	} {
		if got := NormalQuery(text); got != want {
			t.Errorf("%q: %q", text, got)
		}
	}
}

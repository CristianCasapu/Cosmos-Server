package anyimage

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRegistry is a registry with token authentication, an index for two
// platforms, and its blobs redirected to a storage server, like Docker Hub.
type fakeRegistry struct {
	t        *testing.T
	main     *httptest.Server
	storage  *httptest.Server
	mu       sync.Mutex
	seen     []string
	authSeen map[string]string // path -> Authorization
	// knobs
	private        bool   // the token never opens the repository
	basicOnly      bool   // Basic challenge instead of a token
	realm          string // overrides the realm
	blobRedirect   string // overrides where blobs are sent
	hugeConfig     bool
	slow           time.Duration
	badDigest      bool
	tagPages       bool
	realmUser      string
	configPayload  []byte
	claimDigest    string // what HEAD announces for the tag, instead of the truth
	listen         string // the address the registry listens on (default loopback)
	indexDigest    string
	served         []string // manifests answered with a body (what Docker Hub would count)
	manifestDigest string
	ratelimit      string // the ratelimit-remaining header sent with manifests
}

const fakeToken = "tok" + "en-for-tests"

func (f *fakeRegistry) host() string      { return strings.TrimPrefix(f.main.URL, "https://") }
func (f *fakeRegistry) storeHost() string { return strings.TrimPrefix(f.storage.URL, "https://") }

func newFakeRegistry(t *testing.T, setup func(*fakeRegistry)) *fakeRegistry {
	f := &fakeRegistry{t: t, authSeen: map[string]string{}}
	if setup != nil {
		setup(f)
	}
	config := f.configPayload
	if config == nil {
		config, _ = json.Marshal(map[string]interface{}{
			"os": "linux", "architecture": "amd64",
			"config": map[string]interface{}{
				"ExposedPorts": map[string]interface{}{"5432/tcp": map[string]interface{}{}},
				"Volumes":      map[string]interface{}{"/var/lib/postgresql": map[string]interface{}{}},
				"Env":          []string{"PATH=/usr/bin", "PGDATA=/var/lib/postgresql/18/docker", "BAD=a\x01b"},
				"Entrypoint":   []string{"docker-entrypoint.sh"},
				"Cmd":          []string{"postgres"},
				"Labels":       map[string]string{"cosmos-network-name": "host", "x": "<script>"},
				"StopSignal":   "SIGINT",
			},
		})
	}
	if f.hugeConfig {
		config = []byte(`{"config":{"Env":["A=` + strings.Repeat("x", 2<<20) + `"]}}`)
	}
	configDigest := digestOf(config)
	manifest, _ := json.Marshal(map[string]interface{}{
		"mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config":    map[string]interface{}{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": configDigest, "size": len(config)},
		"layers":    []map[string]interface{}{{"digest": "sha256:" + strings.Repeat("3", 64), "size": 1000}, {"digest": "sha256:" + strings.Repeat("4", 64), "size": 2345}},
	})
	manifestDigest := digestOf(manifest)
	index, _ := json.Marshal(map[string]interface{}{
		"mediaType": "application/vnd.oci.image.index.v1+json",
		"manifests": []map[string]interface{}{
			{"digest": "sha256:" + strings.Repeat("1", 64), "platform": map[string]string{"os": "linux", "architecture": "arm64", "variant": "v8"}},
			{"digest": manifestDigest, "platform": map[string]string{"os": "linux", "architecture": "amd64"}},
			{"digest": "sha256:" + strings.Repeat("2", 64), "platform": map[string]string{"os": "unknown", "architecture": "unknown"}},
		},
	})
	f.storage = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.authSeen["storage"] = r.Header.Get("Authorization")
		f.mu.Unlock()
		if f.badDigest {
			w.Write(append(config, ' '))
			return
		}
		w.Write(config)
	}))
	f.main = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.seen = append(f.seen, r.Method+" "+r.URL.Path)
		f.authSeen[r.URL.Path] = r.Header.Get("Authorization")
		f.mu.Unlock()
		if f.slow > 0 {
			time.Sleep(f.slow)
		}
		if strings.HasPrefix(r.URL.Path, "/v2/namespaces/team/repositories/app/tags/") {
			// Docker Hub's API: a tag's platforms and their manifests' digests
			if !strings.HasSuffix(r.URL.Path, "/18") && !strings.HasSuffix(r.URL.Path, "/18.0") {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"digest": digestOf(index), "images": []map[string]interface{}{
				{"architecture": "arm64", "os": "linux", "variant": "v8", "digest": "sha256:" + strings.Repeat("1", 64), "size": 99},
				{"architecture": "amd64", "os": "linux", "variant": nil, "digest": manifestDigest, "size": 4321},
			}})
			return
		}
		if r.URL.Path == "/token" {
			user, pass, ok := r.BasicAuth()
			f.mu.Lock()
			if ok {
				f.realmUser = user + ":" + pass
			}
			f.mu.Unlock()
			if r.URL.Query().Get("scope") != "repository:team/app:pull" {
				http.Error(w, "scope", http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"token": fakeToken})
			return
		}
		auth := r.Header.Get("Authorization")
		if f.basicOnly {
			if !strings.HasPrefix(auth, "Basic ") {
				w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		} else if auth != "Bearer "+fakeToken || f.private {
			realm := f.realm
			if realm == "" {
				realm = f.main.URL + "/token"
			}
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s",service="fake",scope="repository:team/app:pull"`, realm))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if f.ratelimit != "" && strings.Contains(r.URL.Path, "/manifests/") {
			w.Header().Set("ratelimit-remaining", f.ratelimit)
		}
		switch {
		case r.URL.Path == "/v2/team/app/manifests/18" || r.URL.Path == "/v2/team/app/manifests/18.0":
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			if f.claimDigest != "" {
				w.Header().Set("Docker-Content-Digest", f.claimDigest)
			} else {
				w.Header().Set("Docker-Content-Digest", digestOf(index))
			}
			if r.Method == http.MethodHead {
				return
			}
			w.Write(index)
			f.served = append(f.served, r.URL.Path)
		case r.URL.Path == "/v2/team/app/manifests/"+manifestDigest:
			w.Write(manifest)
			f.served = append(f.served, r.URL.Path)
		case r.URL.Path == "/v2/team/app/blobs/"+configDigest && f.blobRedirect == "none":
			w.Write(config)
		case r.URL.Path == "/v2/team/app/blobs/"+configDigest:
			target := f.blobRedirect
			if target == "" {
				target = f.storage.URL + "/blob"
			}
			http.Redirect(w, r, target, http.StatusTemporaryRedirect)
		case r.URL.Path == "/v2/team/app/tags/list":
			if f.tagPages && r.URL.Query().Get("last") == "" {
				w.Header().Set("Link", `</v2/team/app/tags/list?n=1000&last=17>; rel="next"`)
				json.NewEncoder(w).Encode(map[string]interface{}{"tags": []string{"latest", "16", "17"}})
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"tags": []string{"18", "18.1", "18.0", "19-rc1", "bad tag"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	if f.listen != "" {
		listener, err := net.Listen("tcp", f.listen)
		if err != nil {
			t.Skip("cannot listen on " + f.listen)
		}
		f.main.Listener.Close()
		f.main.Listener = listener
	}
	f.main.StartTLS()
	f.indexDigest, f.manifestDigest = digestOf(index), manifestDigest
	pool := x509.NewCertPool()
	pool.AddCert(f.main.Certificate())
	pool.AddCert(f.storage.Certificate())
	testRoots = pool
	resetClient()
	checkAddr = func(a netip.Addr, inside bool, own []netip.Prefix) bool {
		if a.IsLoopback() {
			return false // the test servers
		}
		return refusedAddr(a, inside, own)
	}
	ownAddrsF = func() []netip.Prefix { return nil }
	credentialsFor = func(string) (string, string) { return "", "" }
	loggedIn = func(string) bool { return false }
	descriptions.clear()
	blobs.clear()
	platformsOf.clear()
	hubBudget = &hubAllowance{remaining: -1}
	t.Cleanup(func() {
		f.main.Close()
		f.storage.Close()
		testRoots = nil
		resetClient()
		checkAddr = refusedAddr
		lookupIP = net.DefaultResolver.LookupNetIP
	})
	return f
}

func (f *fakeRegistry) ref() Reference {
	return Reference{Registry: f.host(), Repository: "team/app", Tag: "18"}
}

var amd64 = Platform{OS: "linux", Arch: "amd64"}

func TestDescribeReadsTheDeclarationWithoutAPull(t *testing.T) {
	f := newFakeRegistry(t, nil)
	desc := describeImage(context.Background(), f.ref(), amd64, "user:test")
	if desc.Problem != "" || desc.Declared == nil {
		t.Fatalf("%+v", desc)
	}
	d := desc.Declared
	if strings.Join(d.Ports, ",") != "5432/tcp" || strings.Join(d.Volumes, ",") != "/var/lib/postgresql" || d.StopSignal != "SIGINT" {
		t.Fatalf("%+v", d)
	}
	if desc.Size != 3345 {
		t.Fatalf("download size %d", desc.Size)
	}
	if strings.Join(desc.Platforms, ",") != "linux/amd64,linux/arm64/v8" || desc.Platform != "linux/amd64" || desc.Fits != "yes" {
		t.Fatalf("platforms %v %s %s", desc.Platforms, desc.Platform, desc.Fits)
	}
	for _, e := range d.Env {
		if strings.ContainsRune(e, 1) {
			t.Fatal("control character kept:", e)
		}
	}
	if d.Labels["cosmos-network-name"] != "host" {
		t.Fatal("labels are data, returned as they are")
	}
	// the blob came from the storage server without the registry's token
	if f.authSeen["storage"] != "" {
		t.Fatal("the token followed the redirect to another host")
	}
	for _, s := range f.seen {
		if strings.Contains(s, "/blobs/") && !strings.HasPrefix(s, "GET") || strings.Contains(s, "pull") {
			t.Fatal(s)
		}
	}
	// a second look is answered from the cache
	before := len(f.seen)
	describeImage(context.Background(), f.ref(), amd64, "user:test")
	if len(f.seen) != before {
		t.Fatal("not cached")
	}
}

func TestDescribeOtherProcessorOnly(t *testing.T) {
	f := newFakeRegistry(t, nil)
	desc := describeImage(context.Background(), f.ref(), Platform{OS: "linux", Arch: "riscv64"}, "user:test")
	if desc.Fits != "no" || desc.Declared != nil || len(desc.Platforms) != 2 {
		t.Fatalf("%+v", desc)
	}
}

func TestDescribeFailures(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeRegistry)
		ref   func(f *fakeRegistry) Reference
		want  string
	}{
		{"private image", func(f *fakeRegistry) { f.private = true }, nil, "unauthorized"},
		{"basic without credentials", func(f *fakeRegistry) { f.basicOnly = true }, nil, "unauthorized"},
		{"realm over http", func(f *fakeRegistry) { f.realm = "http://example.com/token" }, nil, "unauthorized"},
		{"realm on the local network", func(f *fakeRegistry) { f.realm = "https://192.168.1.2/token" }, nil, "internal"},
		{"blob sent to http", func(f *fakeRegistry) { f.blobRedirect = "http://example.com/blob" }, nil, "redirect"},
		{"blob sent inside", func(f *fakeRegistry) { f.blobRedirect = "https://10.1.2.3/blob" }, nil, "internal"},
		{"blob sent to metadata", func(f *fakeRegistry) { f.blobRedirect = "https://169.254.169.254/latest" }, nil, "internal"},
		{"config too large", func(f *fakeRegistry) { f.hugeConfig = true }, nil, "tooLarge"},
		{"config does not match its digest", func(f *fakeRegistry) { f.badDigest = true }, nil, "notImage"},
		{"unknown tag", nil, func(f *fakeRegistry) Reference { r := f.ref(); r.Tag = "nope"; return r }, "notFound"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeRegistry(t, c.setup)
			ref := f.ref()
			if c.ref != nil {
				ref = c.ref(f)
			}
			desc := describeImage(context.Background(), ref, amd64, "user:test")
			if desc.Problem != c.want {
				t.Fatalf("want %s, got %q (%+v)", c.want, desc.Problem, desc)
			}
		})
	}
}

func TestDescribeRedirectChainIsBounded(t *testing.T) {
	hops := 0
	var loop *httptest.Server
	loop = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops++
		http.Redirect(w, r, loop.URL+fmt.Sprintf("/hop%d", hops), http.StatusFound)
	}))
	defer loop.Close()
	f := newFakeRegistry(t, func(f *fakeRegistry) {})
	f.blobRedirect = loop.URL + "/start"
	pool := x509.NewCertPool()
	pool.AddCert(f.main.Certificate())
	pool.AddCert(loop.Certificate())
	testRoots = pool
	resetClient()
	desc := describeImage(context.Background(), f.ref(), amd64, "user:test")
	if desc.Problem != "redirect" || hops > maxRedirects {
		t.Fatalf("%q after %d hops", desc.Problem, hops)
	}
}

func TestDescribeSlowRegistryTimesOut(t *testing.T) {
	f := newFakeRegistry(t, func(f *fakeRegistry) { f.slow = 1500 * time.Millisecond })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	desc := describeImage(ctx, f.ref(), amd64, "user:test")
	if desc.Problem != "unreachable" || time.Since(start) > 3*time.Second {
		t.Fatalf("%q in %s", desc.Problem, time.Since(start))
	}
}

func TestCredentialsGoOnlyToTheRegistryAndItsOwnRealm(t *testing.T) {
	f := newFakeRegistry(t, nil)
	credentialsFor = func(registry string) (string, string) {
		if registry == f.host() {
			return "robot", "pw-" + "for-tests"
		}
		return "", ""
	}
	if desc := describeImage(context.Background(), f.ref(), amd64, "user:test"); desc.Problem != "" {
		t.Fatal(desc.Problem)
	}
	if f.realmUser != "robot:pw-for-tests" {
		t.Fatalf("the registry's own realm did not get the credentials: %q", f.realmUser)
	}

	// a realm on another host gets no credentials
	g := newFakeRegistry(t, nil)
	g.realm = "https://" + strings.Replace(g.host(), "127.0.0.1", "example.com", 1) + "/token"
	lookupIP = func(ctx context.Context, network, host string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	credentialsFor = func(registry string) (string, string) { return "robot", "pw" }
	if desc := describeImage(context.Background(), g.ref(), amd64, "user:test"); desc.Problem != "" {
		t.Fatalf("the anonymous token from the other host's realm: %q", desc.Problem)
	}
	if g.realmUser != "" {
		t.Fatalf("a realm on another host got the credentials: %q", g.realmUser)
	}
}

func TestTagsAndSuggestion(t *testing.T) {
	f := newFakeRegistry(t, func(f *fakeRegistry) { f.tagPages = true })
	tags, more := listTags(context.Background(), f.ref())
	if strings.Join(tags, ",") != "latest,16,17,18,18.1,18.0,19-rc1" || more {
		t.Fatalf("%v %v", tags, more)
	}
	if tag, why := suggestTag(tags, true, ""); tag != "18" || why != "pinMajor" {
		t.Fatal(tag, why)
	}
	if tag, why := suggestTag(tags, false, ""); tag != "18.1" || why != "newest" {
		t.Fatal(tag, why)
	}
	if tag, why := suggestTag([]string{"latest", "edge"}, true, ""); tag != "latest" || why != "latest" {
		t.Fatal(tag, why)
	}
	if tag, _ := suggestTag([]string{"4-management", "4.4.0-management", "4.4.0", "4"}, true, "-management"); tag != "4-management" {
		t.Fatal(tag)
	}
	if tag, _ := suggestTag([]string{"v3", "v3.7.19", "v2.11.63", "v3.8.0-rc.3"}, true, ""); tag != "v3" {
		t.Fatal(tag)
	}
	if got := strings.Join(sortTags([]string{"latest", "9.4.8", "9.5.5", "8.19.25"}), ","); got != "9.5.5,9.4.8,8.19.25,latest" {
		t.Fatal(got)
	}
}

func TestGuardRefusesInsideAddresses(t *testing.T) {
	own := []netip.Prefix{netip.MustParsePrefix("203.0.113.9/32")}
	refused := []string{"127.0.0.1", "::1", "169.254.169.254", "0.0.0.0", "224.0.0.1", "fe80::1", "::ffff:127.0.0.1", "64:ff9b::7f00:1", "203.0.113.9", "2002::1"}
	for _, a := range refused {
		if !refusedAddr(netip.MustParseAddr(a), true, own) {
			t.Errorf("%s allowed", a)
		}
	}
	inside := []string{"10.0.0.5", "172.20.0.2", "192.168.1.10", "100.64.1.1", "fd00::5"}
	for _, a := range inside {
		if !refusedAddr(netip.MustParseAddr(a), false, own) {
			t.Errorf("%s allowed without a login", a)
		}
		if refusedAddr(netip.MustParseAddr(a), true, own) {
			t.Errorf("%s refused for a registry the server is logged in to", a)
		}
	}
	for _, a := range []string{"198.51.100.7", "8.8.8.8", "2606:4700::1"} {
		if refusedAddr(netip.MustParseAddr(a), false, own) {
			t.Errorf("%s refused", a)
		}
	}
	// the dialer itself, with the real rules: loopback is never reached
	checkAddr = refusedAddr
	if _, err := guardedDial(context.Background(), "tcp", "127.0.0.1:443"); err != errRefusedAddress {
		t.Fatal(err)
	}
	// Docker Hub's names never get the inside allowance, even when logged in
	loggedIn = func(string) bool { return true }
	lookupIP = func(ctx context.Context, network, host string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("192.168.1.5")}, nil
	}
	defer func() { loggedIn = func(string) bool { return false }; lookupIP = net.DefaultResolver.LookupNetIP }()
	if _, err := guardedDial(context.Background(), "tcp", "registry-1.docker.io:443"); err != errRefusedAddress {
		t.Fatal(err)
	}
}

func TestChallenge(t *testing.T) {
	scheme, p := challenge(`Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:library/postgres:pull"`)
	if scheme != "bearer" || p["realm"] != "https://auth.docker.io/token" || p["service"] != "registry.docker.io" {
		t.Fatal(scheme, p)
	}
	if s, _ := challenge(`Basic realm="x"`); s != "basic" {
		t.Fatal(s)
	}
	challenge(`Bearer realm="unterminated`)
}

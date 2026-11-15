package anyimage

// The security review of plan 40 (40-any-image-security-review.md): one
// test per finding and per test it listed as missing.

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

// F1: a registry that announces another image's digest cannot get its
// answer served for that image.
func TestF1ARegistryCannotPoisonAnotherImage(t *testing.T) {
	good := newFakeRegistry(t, nil)
	goodRef := good.ref()
	evil := newFakeRegistry(t, func(f *fakeRegistry) {
		f.configPayload = []byte(`{"os":"linux","architecture":"amd64","config":{"ExposedPorts":{"6666/tcp":{}},"Volumes":{"/evil":{}}}}`)
	})
	evil.claimDigest = good.indexDigest
	// both fakes trust the other's certificate (the last one made set testRoots): make both known
	trustBoth(t, good, evil)
	evilDesc := describeImage(context.Background(), evil.ref(), amd64, "user:a")
	if evilDesc.Problem != "notImage" {
		t.Fatalf("a body that is not what the registry announced must be refused, got %q", evilDesc.Problem)
	}
	desc := describeImage(context.Background(), goodRef, amd64, "user:b")
	if desc.Problem != "" || desc.Registry != goodRef.Registry || desc.Page != goodRef.Page() || strings.Join(desc.Declared.Ports, ",") != "5432/tcp" {
		t.Fatalf("the good image's description came from elsewhere: %+v", desc)
	}
	// and the other way round: describe the good one first, then a registry claiming its digest
	descriptions.clear()
	describeImage(context.Background(), goodRef, amd64, "user:b")
	evilDesc = describeImage(context.Background(), evil.ref(), amd64, "user:a")
	if evilDesc.Declared != nil && strings.Join(evilDesc.Declared.Ports, ",") == "5432/tcp" {
		t.Fatal("the cache served the good image's manifest under another registry")
	}
	if evilDesc.Problem != "notImage" {
		t.Fatalf("%q", evilDesc.Problem)
	}
}

func trustBoth(t *testing.T, list ...*fakeRegistry) {
	pool := testRoots
	for _, f := range list {
		pool.AddCert(f.main.Certificate())
		pool.AddCert(f.storage.Certificate())
	}
	resetClient()
}

// A description from the cache takes its identity from the reference asked for.
func TestCacheHitKeepsTheIdentityOfTheReference(t *testing.T) {
	ref := Reference{Registry: "registry.example.org", Repository: "team/app", Tag: "1"}
	fake := identity(Description{Reference: "elsewhere.example.org/x:1", Registry: "elsewhere.example.org", Repository: "x", Page: "https://elsewhere.example.org/x"}, ref)
	if fake.Registry != ref.Registry || fake.Page != ref.Page() || fake.Repository != ref.Repository || fake.Reference != ref.String() {
		t.Fatalf("%+v", fake)
	}
}

// F2: Docker Hub is read through its API (not a pull) and one counted
// manifest read; cached manifests cost nothing; the allowance stops reads
// before the last pulls.
func TestF2DockerHubPullsAreCountedAndKeptForRealPulls(t *testing.T) {
	f := newFakeRegistry(t, nil)
	hubRegistryHost, hubAPI = f.host(), f.main.URL
	defer func() { hubRegistryHost, hubAPI = "registry-1.docker.io", "https://hub.docker.com" }()
	ref := Reference{Registry: hubRegistry, Repository: "team/app", Tag: "18"}
	manifestGets := func() int { return len(f.served) }
	desc := describeImage(context.Background(), ref, amd64, "user:a")
	if desc.Problem != "" || desc.Size != 4321 || desc.Platform != "linux/amd64" || len(desc.Platforms) != 2 {
		t.Fatalf("%+v", desc)
	}
	if n := manifestGets(); n != 1 {
		t.Fatalf("one counted read (the platform's manifest), not the index: %d", n)
	}
	// another tag with the same platform manifest: from the cache, no read
	descriptions.clear()
	other := ref
	other.Tag = "18.0"
	if d := describeImage(context.Background(), other, amd64, "user:a"); d.Problem != "" || manifestGets() != 1 {
		t.Fatalf("%q, %d reads", d.Problem, manifestGets())
	}
	// Docker Hub says 25 are left: no more reads, a sentence instead
	descriptions.clear()
	blobs.clear()
	f.ratelimit = "25;w=21600"
	describeImage(context.Background(), ref, amd64, "user:a") // this read learns the number
	descriptions.clear()
	blobs.clear()
	before := manifestGets()
	d := describeImage(context.Background(), ref, amd64, "user:a")
	if d.Problem != "hubReserve" || d.HubLeft != 25 || manifestGets() != before {
		t.Fatalf("%q left %d reads %d->%d", d.Problem, d.HubLeft, before, manifestGets())
	}
}

func TestHubAllowance(t *testing.T) {
	now := time.Now()
	h := &hubAllowance{remaining: -1}
	for i := 0; i < hubPerUserHour; i++ {
		if err := h.take("user:a", now); err != nil {
			t.Fatal(i, err)
		}
	}
	if err := h.take("user:a", now); err != errHubBudget {
		t.Fatal("one user's hour budget", err)
	}
	for i := 0; i < hubPerHour-hubPerUserHour; i++ {
		if err := h.take("user:b"+string(rune('a'+i%3)), now); err != nil && err != errHubBudget {
			t.Fatal(err)
		}
	}
	h = &hubAllowance{remaining: -1}
	h.observe(http.Header{"Ratelimit-Remaining": []string{"26;w=21600"}}, now)
	if err := h.take("user:a", now); err != nil {
		t.Fatal(err)
	}
	h.observe(http.Header{"Ratelimit-Remaining": []string{"25;w=21600"}}, now)
	if err := h.take("user:a", now); err != errHubReserve {
		t.Fatal(err)
	}
	if err := h.take("user:a", now.Add(7*time.Hour)); err != nil {
		t.Fatal("an old number does not hold for ever", err)
	}
	h.observe(http.Header{"Ratelimit-Remaining": []string{"nonsense"}}, now)
}

func TestPerUserWindows(t *testing.T) {
	users := &perUser{perMin: 3}
	now := time.Now()
	for i := 0; i < 3; i++ {
		if !users.start("user:a", now) {
			t.Fatal(i)
		}
		users.done("user:a")
	}
	if users.start("user:a", now) {
		t.Fatal("a fourth in a minute")
	}
	if !users.start("user:b", now) {
		t.Fatal("another user is not held up by the first")
	}
}

func TestCachesAreBoundedByBytes(t *testing.T) {
	c := newByteCache(1000)
	for i := 0; i < 20; i++ {
		c.put(string(rune('a'+i)), cacheItem{data: make([]byte, 200), size: 200})
	}
	if c.used > 1000 || len(c.items) != 5 {
		t.Fatalf("%d bytes, %d items", c.used, len(c.items))
	}
	if _, ok := c.get("a", time.Hour); ok {
		t.Fatal("the oldest left first")
	}
	c.put("big", cacheItem{size: 300})
	if _, ok := c.get("big", time.Hour); ok {
		t.Fatal("an entry over a quarter is not kept")
	}
}

// F4: neighbours on the server's own networks, IPv4-compatible and site-local addresses.
func TestF4OnLinkAndOldForms(t *testing.T) {
	own := []netip.Prefix{netip.MustParsePrefix("2001:db8:1:2::5/64"), netip.MustParsePrefix("203.0.113.9/24"), netip.MustParsePrefix("::ffff:198.51.100.4/120")}
	for _, a := range []string{"2001:db8:1:2::77", "203.0.113.50", "198.51.100.200", "::7f00:1", "::a00:1", "fec0::1", "feff::1"} {
		if !refusedAddr(netip.MustParseAddr(a), false, own) {
			t.Errorf("%s allowed", a)
		}
	}
	for _, a := range []string{"2001:db8:9::1", "8.8.8.8"} {
		if refusedAddr(netip.MustParseAddr(a), false, own) {
			t.Errorf("%s refused", a)
		}
	}
	if refusedAddr(netip.MustParseAddr("203.0.113.50"), true, own) {
		t.Error("a registry on the same network the server is logged in to")
	}
	if !refusedAddr(netip.MustParseAddr("203.0.113.9"), true, own) {
		t.Error("the server itself")
	}
}

// Names: one that resolves inside, one with several addresses, one the server is logged in to.
func TestGuardWithNames(t *testing.T) {
	checkAddr, ownAddrsF = refusedAddr, func() []netip.Prefix { return nil }
	defer func() { lookupIP = net.DefaultResolver.LookupNetIP; loggedIn = func(string) bool { return false } }()
	lookupIP = func(ctx context.Context, network, host string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.1.2.3"), netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("169.254.169.254")}, nil
	}
	loggedIn = func(string) bool { return false }
	if _, err := guardedDial(context.Background(), "tcp", "inside.example.org:443"); err != errRefusedAddress {
		t.Fatal("every address of the name is inside", err)
	}
	// logged in: the local network is allowed, still never loopback or metadata
	loggedIn = func(host string) bool { return host == "inside.example.org" }
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err = guardedDial(ctx, "tcp", "inside.example.org:"+strings.Split(listener.Addr().String(), ":")[1])
	if err == errRefusedAddress {
		t.Fatal("the logged-in registry's local address is allowed (the dial itself may time out)")
	}
}

// One fake registry reached through the production guard (checkAddr and the
// server's real interfaces), allowed only by testAllowedPrefixes, which no
// production build can fill (guard.go, guard_testnet.go).
func TestProductionGuardWithATestRegistry(t *testing.T) {
	var ip netip.Addr
	if list, err := net.InterfaceAddrs(); err == nil {
		for _, a := range list {
			if p, err := netip.ParsePrefix(a.String()); err == nil && p.Addr().Is4() && !p.Addr().IsLoopback() {
				ip = p.Addr()
				break
			}
		}
	}
	if !ip.IsValid() {
		t.Skip("no interface address to listen on")
	}
	f := newFakeRegistry(t, func(f *fakeRegistry) { f.listen = ip.String() + ":0"; f.blobRedirect = "none" })
	checkAddr, ownAddrsF = refusedAddr, ownAddrs // the production guard
	lookupIP = func(ctx context.Context, network, host string) ([]netip.Addr, error) {
		if host == "example.com" {
			return []netip.Addr{ip}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host}
	}
	port := strings.Split(f.host(), ":")[1]
	f.realm = "https://example.com:" + port + "/token" // the certificate names example.com
	ref := Reference{Registry: "example.com:" + port, Repository: "team/app", Tag: "18"}
	if d := describeImage(context.Background(), ref, amd64, "user:a"); d.Problem != "internal" {
		t.Fatalf("the server's own address is refused by the production guard: %q", d.Problem)
	}
	descriptions.clear()
	testAllowedPrefixes = []netip.Prefix{netip.PrefixFrom(ip, 32)}
	defer func() { testAllowedPrefixes = nil }()
	if d := describeImage(context.Background(), ref, amd64, "user:a"); d.Problem != "" || d.Declared == nil {
		t.Fatalf("with the test allowance: %+v", d)
	}
}

// Basic credentials never follow a redirect to another host.
func TestBasicCredentialsDoNotFollowARedirect(t *testing.T) {
	f := newFakeRegistry(t, func(f *fakeRegistry) { f.basicOnly = true })
	credentialsFor = func(registry string) (string, string) {
		if registry == f.host() {
			return "robot", "pw-" + "for-tests"
		}
		return "", ""
	}
	if d := describeImage(context.Background(), f.ref(), amd64, "user:a"); d.Problem != "" {
		t.Fatal(d.Problem)
	}
	if f.authSeen["storage"] != "" {
		t.Fatal("Basic credentials followed the blob's redirect to the storage host")
	}
}

func TestDockerHubCredentialKeys(t *testing.T) {
	for _, r := range []string{"docker.io", "registry-1.docker.io", "index.docker.io"} {
		if authKey(r) != hubAuthKey {
			t.Error(r)
		}
	}
	if authKey("ghcr.io") != "ghcr.io" {
		t.Error("ghcr")
	}
	hub := Reference{Registry: hubRegistry, Repository: "library/postgres"}
	mustURL := func(s string) *urlT { u, _ := parseURL(s); return u }
	if !realmMayGetCredentials(hub, mustURL("https://auth.docker.io/token")) || realmMayGetCredentials(hub, mustURL("https://auth.example.org/token")) {
		t.Error("Docker Hub's realm")
	}
	ghcr := Reference{Registry: "ghcr.io", Repository: "a/b"}
	if !realmMayGetCredentials(ghcr, mustURL("https://ghcr.io/token")) || realmMayGetCredentials(ghcr, mustURL("https://auth.docker.io/token")) {
		t.Error("GHCR's realm")
	}
}

// Tag pages: only a path on the same registry is followed; Docker Hub's next page only on Docker Hub's API.
func TestTagPagesAreNotFollowedElsewhere(t *testing.T) {
	f := newFakeRegistry(t, func(f *fakeRegistry) { f.tagPages = true })
	tags, _ := listTags(context.Background(), f.ref())
	if len(tags) != 7 {
		t.Fatal(tags)
	}
	if m := linkNext.FindStringSubmatch(`<https://elsewhere.example.org/v2/x/tags/list?last=1>; rel="next"`); m == nil {
		t.Fatal("pattern")
	} else if u, err := parseURL(m[1]); err != nil || u.Host == "" {
		t.Fatal("an absolute address has a host, which listTags does not follow")
	}
}

type urlT = url.URL

var parseURL = url.Parse

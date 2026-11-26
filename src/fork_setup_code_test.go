package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestSetupCodeShape(t *testing.T) {
	shape := regexp.MustCompile(`^[ABCDEFGHJKMNPQRSTVWXYZ2-9]{4}(-[ABCDEFGHJKMNPQRSTVWXYZ2-9]{4}){4}$`)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code, err := newSetupCode(rand.Reader)
		if err != nil || !shape.MatchString(code) {
			t.Fatalf("code %q, %v", code, err)
		}
		if seen[code] {
			t.Fatal("the same code twice")
		}
		seen[code] = true
		if strings.ContainsAny(code, "01ILOU") {
			t.Fatalf("a look-alike in %q", code)
		}
	}
}

func TestSetupCodeNormalized(t *testing.T) {
	want := "ABCDEFGHJKMNPQRSTVWX"
	for _, typed := range []string{"ABCD-EFGH-JKMN-PQRS-TVWX", "abcd efgh jkmn pqrs tvwx", "ABCDEFGHJKMNPQRSTVWX", " abcd_efgh-JKMN pqrs-tvwx\n"} {
		if got := normalizeSetupCode(typed); got != want {
			t.Errorf("%q -> %q", typed, got)
		}
	}
	for _, bad := range []string{"", "ABCD", "ABCD-EFGH-JKMN-PQRS-TVWXY", "ABCD-EFGH-JKMN-PQRS-TVW0", "ABCD-EFGH-JKMN-PQRS-TVWÄ", strings.Repeat("A", 500)} {
		if got := normalizeSetupCode(bad); got != "" {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

func TestSetupCodeFileMadeOnceAndRemovedWhenSetUp(t *testing.T) {
	file := filepath.Join(t.TempDir(), "setup-code")
	var out bytes.Buffer
	setupCodeOut = &out
	defer func() { setupCodeOut = os.Stderr }()

	ensureSetupCode(true, file, rand.Reader)
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file %v, mode %v", err, info)
	}
	code, err := readSetupCode(file)
	if err != nil || !strings.Contains(out.String(), "FIRST-RUN SETUP CODE") || !strings.Contains(strings.ReplaceAll(out.String(), "-", ""), code) {
		t.Fatalf("the code was not told once: %v %q", err, out.String())
	}

	// a later start keeps the code and says nothing
	out.Reset()
	ensureSetupCode(true, file, rand.Reader)
	again, _ := readSetupCode(file)
	if again != code || out.Len() != 0 {
		t.Errorf("a second start changed or told the code again")
	}

	// set up: the file goes
	ensureSetupCode(false, file, rand.Reader)
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("the code file stayed after setup")
	}
	// set up and no file: nothing is made
	ensureSetupCode(false, file, rand.Reader)
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("a code file was made for an installation that is set up")
	}
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func testGuard(t *testing.T, newInstall bool) (*setupGuard, string, string, *fakeClock, *int) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "setup-code")
	setupCodeOut = &bytes.Buffer{}
	t.Cleanup(func() { setupCodeOut = os.Stderr })
	ensureSetupCode(true, file, rand.Reader)
	code, err := readSetupCode(file)
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	g := &setupGuard{newInstall: func() bool { return newInstall }, file: func() string { return file }, limiter: newSetupLimiter(clock.now)}
	reached := 0
	return g, file, code, clock, &reached
}

func setupCall(g *setupGuard, reached *int, method, target, code, from string) *httptest.ResponseRecorder {
	h := g.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reached++
		if r.Header.Get(setupCodeHeader) != "" {
			panic("the code reached the handler")
		}
		w.Write([]byte(`{"status":"OK"}`))
	}))
	r := httptest.NewRequest(method, target, strings.NewReader(`{"step":"4","nickname":"x","password":"y"}`))
	r.RemoteAddr = net.JoinHostPort(from, "40000")
	if code != "" {
		r.Header.Set(setupCodeHeader, code)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct{ Code string }
	json.Unmarshal(w.Body.Bytes(), &body)
	return body.Code
}

// Every route the new-install state opens without a session, in every
// spelling the router would still take, with no code, a wrong code, the
// right one, and with the file missing.
func TestEverySetupRouteNeedsTheCode(t *testing.T) {
	paths := []string{
		"/cosmos/api/newInstall", "/cosmos/api/setup", "/cosmos/api/setup-join", "/cosmos/api/dns?url=example.org",
		"/cosmos/api//newInstall", "/cosmos/api/./setup", "/COSMOS/API/SETUP-JOIN", "/cosmos/api/x/../newInstall",
	}
	for _, p := range paths {
		g, file, code, _, reached := testGuard(t, true)
		method := "POST"
		if strings.Contains(p, "dns") {
			method = "GET"
		}
		if w := setupCall(g, reached, method, p, "", "198.51.100.1"); w.Code != 403 || errorCode(t, w) != "SC001" {
			t.Errorf("%s without a code: %d %s", p, w.Code, w.Body)
		}
		if w := setupCall(g, reached, method, p, "AAAA-AAAA-AAAA-AAAA-AAAA", "198.51.100.1"); w.Code != 403 || errorCode(t, w) != "SC002" {
			t.Errorf("%s with a wrong code: %d %s", p, w.Code, w.Body)
		}
		if w := setupCall(g, reached, method, p, "not a code", "198.51.100.1"); w.Code != 403 || errorCode(t, w) != "SC002" {
			t.Errorf("%s with something else: %d %s", p, w.Code, w.Body)
		}
		if *reached != 0 {
			t.Fatalf("%s: the handler was reached without the code", p)
		}
		if w := setupCall(g, reached, method, p, strings.ToLower(strings.ReplaceAll(code, "-", " ")), "198.51.100.2"); w.Code != 200 || *reached != 1 {
			t.Errorf("%s with the right code: %d %s", p, w.Code, w.Body)
		}
		if strings.Contains(setupCall(g, reached, method, p, "", "198.51.100.3").Body.String(), code) {
			t.Errorf("%s: an answer holds the code", p)
		}
		os.Remove(file)
		if w := setupCall(g, reached, method, p, code, "198.51.100.2"); w.Code != 503 || errorCode(t, w) != "SC004" || *reached != 1 {
			t.Errorf("%s with the file missing: %d %s", p, w.Code, w.Body)
		}
		os.Mkdir(file, 0o700) // unreadable as a code
		if w := setupCall(g, reached, method, p, code, "198.51.100.2"); w.Code != 503 || *reached != 1 {
			t.Errorf("%s with an unreadable file: %d %s", p, w.Code, w.Body)
		}
	}
}

func TestOtherRoutesAndSetUpInstallationsAreUntouched(t *testing.T) {
	g, _, _, _, reached := testGuard(t, true)
	for _, p := range []string{"/cosmos/api/status", "/cosmos-ui/newInstall", "/cosmos/api/me", "/"} {
		if w := setupCall(g, reached, "GET", p, "", "198.51.100.1"); w.Code != 200 {
			t.Errorf("%s: %d (the guard only takes the setup routes; the others answer NEW_INSTALL as before)", p, w.Code)
		}
	}
	g, file, _, _, reached := testGuard(t, false)
	if w := setupCall(g, reached, "POST", "/cosmos/api/newInstall", "", "198.51.100.1"); w.Code != 200 {
		t.Errorf("set up: %d (the route itself refuses)", w.Code)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("the code file stayed once the installation was set up")
	}
}

func TestSetupCodeGuessingIsBounded(t *testing.T) {
	g, _, code, clock, reached := testGuard(t, true)
	wrong := "AAAA-AAAA-AAAA-AAAA-AAAA"
	for i := 0; i < setupFreeTries; i++ {
		if w := setupCall(g, reached, "POST", "/cosmos/api/setup", wrong, "203.0.113.9"); w.Code != 403 || errorCode(t, w) != "SC002" {
			t.Fatalf("try %d: %d", i+1, w.Code)
		}
	}
	// the sixth wrong try earns a pause, which then doubles
	setupCall(g, reached, "POST", "/cosmos/api/setup", wrong, "203.0.113.9")
	w := setupCall(g, reached, "POST", "/cosmos/api/setup", wrong, "203.0.113.9")
	if w.Code != 429 || errorCode(t, w) != "SC003" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("a wrong code during the pause: %d %s", w.Code, w.Body)
	}
	clock.t = clock.t.Add(2 * time.Second)
	setupCall(g, reached, "POST", "/cosmos/api/setup", wrong, "203.0.113.9") // the 7th: a pause of 2 s
	clock.t = clock.t.Add(1500 * time.Millisecond)
	if w := setupCall(g, reached, "POST", "/cosmos/api/setup", wrong, "203.0.113.9"); w.Code != 429 {
		t.Fatalf("the pause did not grow: %d", w.Code)
	}
	// the right code is never answered "too many tries", even from the paused address
	if w := setupCall(g, reached, "POST", "/cosmos/api/setup", code, "203.0.113.9"); w.Code != 200 || *reached != 1 {
		t.Fatalf("the right code during a pause: %d %s", w.Code, w.Body)
	}
	// and it wipes the address's slate
	if w := setupCall(g, reached, "POST", "/cosmos/api/setup", wrong, "203.0.113.9"); w.Code != 403 {
		t.Fatalf("after the right code, a wrong one: %d", w.Code)
	}
	// the longest pause is 15 minutes
	for i := 0; i < 40; i++ {
		clock.t = clock.t.Add(setupMaxPause + time.Second)
		setupCall(g, reached, "POST", "/cosmos/api/setup", wrong, "203.0.113.9")
	}
	if paused, wait := g.limiter.paused("203.0.113.9"); !paused || wait > setupMaxPause {
		t.Fatalf("paused %v for %v", paused, wait)
	}
}

// A flood of wrong guesses from many addresses (IPv4 and IPv6 alike) never
// keeps the owner out: the right code is accepted every time, and the table
// of addresses stays bounded.
func TestAFloodOfGuessesNeverKeepsTheOwnerOut(t *testing.T) {
	g, _, code, clock, reached := testGuard(t, true)
	accepted := 0
	for second := 0; second < 60; second++ {
		for i := 0; i < 200; i++ {
			var from string
			if i%2 == 0 {
				from = fmt.Sprintf("2001:db8:%x:%x::%x", second, i, i)
			} else {
				from = fmt.Sprintf("198.51.%d.%d", second%250, i%250)
			}
			setupCall(g, reached, "POST", "/cosmos/api/setup", "AAAA-AAAA-AAAA-AAAA-AAAA", from)
			clock.t = clock.t.Add(5 * time.Millisecond)
		}
		for _, owner := range []string{"127.0.0.1", "192.168.1.20", "2001:db8:ffff::1"} {
			if w := setupCall(g, reached, "POST", "/cosmos/api/setup", code, owner); w.Code == 200 {
				accepted++
			}
		}
	}
	if accepted != 180 {
		t.Fatalf("the owner's right code was accepted %d times of 180 under the flood", accepted)
	}
	if n := g.limiter.size(); n > setupMaxAddresses {
		t.Fatalf("the table holds %d addresses", n)
	}
}

// An IPv6 network counts as one address (one machine holds a whole /64);
// headers never choose the address a guess counts against.
func TestSetupGuessesCountByConnection(t *testing.T) {
	g, _, _, _, reached := testGuard(t, true)
	wrong := "AAAA-AAAA-AAAA-AAAA-AAAA"
	for i := 0; i < setupFreeTries+1; i++ {
		setupCall(g, reached, "POST", "/cosmos/api/setup", wrong, fmt.Sprintf("2001:db8:1:2::%x", i+1))
	}
	if w := setupCall(g, reached, "POST", "/cosmos/api/setup", wrong, "2001:db8:1:2:aaaa::1"); w.Code != 429 {
		t.Fatalf("fresh addresses of one /64 escaped the pause: %d", w.Code)
	}
	if w := setupCall(g, reached, "POST", "/cosmos/api/setup", wrong, "2001:db8:1:3::1"); w.Code != 403 {
		t.Fatalf("another /64 was held up: %d", w.Code)
	}
	// X-Forwarded-For and X-Real-IP are not read: a paused address stays paused
	for i := 0; i < setupFreeTries+1; i++ {
		setupCall(g, reached, "POST", "/cosmos/api/setup", wrong, "203.0.113.50")
	}
	h := g.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	r := httptest.NewRequest("POST", "/cosmos/api/setup", nil)
	r.RemoteAddr = "203.0.113.50:4000"
	r.Header.Set(setupCodeHeader, wrong)
	r.Header.Set("X-Forwarded-For", "198.51.100.200")
	r.Header.Set("X-Real-IP", "198.51.100.201")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 429 {
		t.Fatalf("a forwarded-for header escaped the pause: %d", w.Code)
	}
	if got := setupClient(r); got != "203.0.113.50" {
		t.Fatalf("client %q", got)
	}
}

// A setup-code file this server did not make (a link, another mode, another
// owner, a hard link) is never trusted: it is replaced, or setup stays closed.
func TestAPlantedSetupCodeIsNotTrusted(t *testing.T) {
	setupCodeOut = &bytes.Buffer{}
	defer func() { setupCodeOut = os.Stderr }()
	dir := t.TempDir()
	file := filepath.Join(dir, "setup-code")
	target := filepath.Join(dir, "elsewhere")
	os.WriteFile(target, []byte("ABCD-EFGH-JKMN-PQRS-TVWX\n"), 0o600)

	os.Symlink(target, file)
	if _, err := readSetupCode(file); err == nil {
		t.Fatal("a code read through a link")
	}
	ensureSetupCode(true, file, rand.Reader)
	if info, err := os.Lstat(file); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("the link was not replaced by a file: %v", err)
	}
	if code, err := readSetupCode(file); err != nil || code == "ABCDEFGHJKMNPQRSTVWX" {
		t.Fatalf("the planted code was kept: %q %v", code, err)
	}
	if data, _ := os.ReadFile(target); string(data) != "ABCD-EFGH-JKMN-PQRS-TVWX\n" {
		t.Fatal("the link's target was touched")
	}

	os.Remove(file)
	os.WriteFile(file, []byte("ABCD-EFGH-JKMN-PQRS-TVWX\n"), 0o644)
	if _, err := readSetupCode(file); err == nil {
		t.Fatal("a 0644 file was trusted")
	}
	ensureSetupCode(true, file, rand.Reader)
	if code, err := readSetupCode(file); err != nil || code == "ABCDEFGHJKMNPQRSTVWX" {
		t.Fatalf("a 0644 planted code was kept: %q %v", code, err)
	}

	os.Remove(file)
	os.WriteFile(target, []byte("ABCD-EFGH-JKMN-PQRS-TVWX\n"), 0o600)
	os.Link(target, file)
	if _, err := readSetupCode(file); err == nil {
		t.Fatal("a hard link was trusted")
	}
}

func TestTheWizardListensOnLoopbackWhenAsked(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "setup-loopback")
	if got := setupListenAddrFor("0.0.0.0:80", true, marker); got != "0.0.0.0:80" {
		t.Errorf("without the marker: %s", got)
	}
	os.WriteFile(marker, nil, 0o600)
	if got := setupListenAddrFor("0.0.0.0:80", true, marker); got != "127.0.0.1:80" {
		t.Errorf("with the marker: %s", got)
	}
	if got := setupListenAddrFor("0.0.0.0:8443", false, marker); got != "0.0.0.0:8443" {
		t.Errorf("set up: %s", got)
	}
	os.Chmod(marker, 0o644)
	if got := setupListenAddrFor("0.0.0.0:80", true, marker); got != "0.0.0.0:80" {
		t.Errorf("a marker anyone could have left counted: %s", got)
	}
	// the real flag: the configuration's NewInstall
	prevFolder, prevConfig := utils.CONFIGFOLDER, utils.GetBaseMainConfig()
	defer func() { utils.CONFIGFOLDER = prevFolder; utils.LoadBaseMainConfig(prevConfig) }()
	utils.CONFIGFOLDER = dir + "/"
	os.Chmod(marker, 0o600)
	config := utils.DefaultConfig
	config.NewInstall = true
	utils.LoadBaseMainConfig(config)
	if got := setupListenAddr("0.0.0.0:80"); got != "127.0.0.1:80" {
		t.Errorf("new installation, marker: %s", got)
	}
	config.NewInstall = false
	utils.LoadBaseMainConfig(config)
	if got := setupListenAddr("0.0.0.0:80"); got != "0.0.0.0:80" {
		t.Errorf("set up, marker still there: %s", got)
	}
	// and the start says where it listens, also when the marker is not trusted
	if s := setupWhereSaid(marker); !strings.Contains(s, "127.0.0.1 only") {
		t.Errorf("said %q", s)
	}
	os.Chmod(marker, 0o644)
	if s := setupWhereSaid(marker); !strings.Contains(s, "WARNING") || !strings.Contains(s, "EVERY address") {
		t.Errorf("an untrusted marker said %q", s)
	}
	os.Remove(marker)
	if s := setupWhereSaid(marker); !strings.Contains(s, "every address") {
		t.Errorf("no marker said %q", s)
	}
	lifecycle, _ := os.ReadFile("fork_server_lifecycle.go")
	if !strings.Contains(string(lifecycle), `net.Listen("tcp", setupListenAddr(addr))`) {
		t.Error("the listeners do not go through setupListenAddr")
	}
}

func TestSetupCodeIsHookedIn(t *testing.T) {
	server, _ := os.ReadFile("httpServer.go")
	index, _ := os.ReadFile("index.go")
	if !strings.Contains(string(server), "router.Use(forkSetupCodeGuard)") {
		t.Error("the guard is not on the router")
	}
	if !strings.Contains(string(index), "forkSetupCodeAtStart()") {
		t.Error("the code is not made at start")
	}
	token, _ := os.ReadFile("user/token.go")
	open := regexp.MustCompile(`req\.URL\.Path != "(/cosmos/api/[a-zA-Z-]+)"`).FindAllStringSubmatch(string(token), -1)
	if len(open) == 0 {
		t.Fatal("the new-install exemptions were not found in user/token.go")
	}
	for _, m := range open {
		if m[1] != "/cosmos/api/status" && !setupPathGuarded(m[1]) {
			t.Errorf("%s is open during setup and not guarded by the code", m[1])
		}
	}
}

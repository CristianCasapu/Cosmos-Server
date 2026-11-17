package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
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
	r.RemoteAddr = from + ":40000"
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
		if w := setupCall(g, reached, "POST", "/cosmos/api/setup", wrong, "203.0.113.9"); w.Code != 403 {
			t.Fatalf("try %d: %d", i+1, w.Code)
		}
	}
	// the sixth wrong try earns a pause, which then doubles
	setupCall(g, reached, "POST", "/cosmos/api/setup", wrong, "203.0.113.9")
	w := setupCall(g, reached, "POST", "/cosmos/api/setup", code, "203.0.113.9")
	if w.Code != 429 || errorCode(t, w) != "SC003" || w.Header().Get("Retry-After") == "" || *reached != 0 {
		t.Fatalf("during the pause even the right code waits: %d %s", w.Code, w.Body)
	}
	// another address is not held up by this one
	if w := setupCall(g, reached, "POST", "/cosmos/api/setup", code, "198.51.100.7"); w.Code != 200 {
		t.Fatalf("another address: %d", w.Code)
	}
	clock.t = clock.t.Add(2 * time.Second)
	setupCall(g, reached, "POST", "/cosmos/api/setup", wrong, "203.0.113.9") // the 7th: a pause of 2 s
	clock.t = clock.t.Add(1500 * time.Millisecond)
	if w := setupCall(g, reached, "POST", "/cosmos/api/setup", code, "203.0.113.9"); w.Code != 429 {
		t.Fatalf("the pause did not grow: %d", w.Code)
	}
	// never for good: at most 15 minutes, then the owner gets through
	for i := 0; i < 40; i++ {
		clock.t = clock.t.Add(setupMaxPause)
		setupCall(g, reached, "POST", "/cosmos/api/setup", wrong, "203.0.113.9")
	}
	clock.t = clock.t.Add(setupMaxPause + time.Second)
	if w := setupCall(g, reached, "POST", "/cosmos/api/setup", code, "203.0.113.9"); w.Code != 200 {
		t.Fatalf("after the longest pause the right code is refused: %d %s", w.Code, w.Body)
	}
}

func TestSetupCodeChecksAreBoundedOverall(t *testing.T) {
	g, _, code, clock, reached := testGuard(t, true)
	refused := 0
	for i := 0; i < setupBucketSize+10; i++ {
		w := setupCall(g, reached, "POST", "/cosmos/api/setup", "AAAA-AAAA-AAAA-AAAA-AAAA", "198.51.100."+string(rune('0'+i%10))+string(rune('0'+i/10)))
		if w.Code == 429 {
			refused++
		}
	}
	if refused != 10 {
		t.Fatalf("%d checks refused over the bucket, want 10", refused)
	}
	// it refills: a moment later the owner's check goes through
	clock.t = clock.t.Add(time.Second)
	if w := setupCall(g, reached, "POST", "/cosmos/api/setup", code, "192.0.2.50"); w.Code != 200 {
		t.Fatalf("after a second: %d", w.Code)
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

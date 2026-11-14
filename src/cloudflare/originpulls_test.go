package cloudflare

// Fork addition: tests of originpulls.go: the certificate made on the
// server, its hand-over, the guarded sequence that switches the option on,
// the fall-back and the reminders.

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/cloudflare/cffake"
	"github.com/azukaar/cosmos-server/src/utils"
)

// a token as the administrator would make for the upload: "SSL and
// Certificates: Edit" for the domain, nothing else
const sslToken = "sslcerts0123456789abcdefghijklmnopqrstuvwxYZ"

type pullsEnv struct {
	*testEnv
	recycled int
	notices  []string
}

func pullsSetup(t *testing.T) *pullsEnv {
	t.Helper()
	env := &pullsEnv{}
	env.testEnv = setup(t, func(config *utils.Config) {
		config.HTTPConfig.Cloudflare.Shielded = []string{"radarr.example.org"}
		config.HTTPConfig.Cloudflare.TrustRanges = true
	})
	env.fake.AddToken(sslToken, cffake.Token{Zones: []string{"example.org"}, NoZoneRead: true, EditSSL: true})

	previousRecycle, previousNotify := recycleConnections, notifyAdmins
	recycleConnections = func() { env.recycled++ }
	notifyAdmins = func(title string, message string, vars string, level string) {
		env.notices = append(env.notices, title+"|"+vars)
	}
	setPending(nil)
	utils.ResetOriginPullCounts()
	t.Cleanup(func() {
		recycleConnections, notifyAdmins = previousRecycle, previousNotify
		setPending(nil)
		utils.ResetOriginPullCounts()
	})
	return env
}

func pulls() utils.ForkOriginPullsConfig {
	return utils.ReadConfigFromFile().HTTPConfig.Cloudflare.OriginPulls
}

// act calls the route and returns the status, the reason of a refusal and
// the data of the answer.
func act(t *testing.T, body map[string]interface{}) (int, string, map[string]interface{}) {
	t.Helper()
	code, answer := post(t, OriginPullsRoute, body)
	reason, _ := answer["reason"].(string)
	return code, reason, data(answer)
}

func mustAct(t *testing.T, body map[string]interface{}) map[string]interface{} {
	t.Helper()
	code, reason, result := act(t, body)
	if code != http.StatusOK {
		t.Fatalf("%v: status %d (%s)", body, code, reason)
	}
	return result
}

func refused(t *testing.T, body map[string]interface{}, want string) {
	t.Helper()
	before := pulls()
	code, reason, _ := act(t, body)
	if code != http.StatusBadRequest || reason != want {
		t.Errorf("%v: status %d reason %q, want a refusal because %q", body, code, reason, want)
	}
	after := pulls()
	if after.Mode != before.Mode || after.Kind != before.Kind || len(after.Own) != len(before.Own) || after.Confirmed != before.Confirmed {
		t.Errorf("%v: a refused action changed the settings: %+v -> %+v", body, before, after)
	}
}

// noSecrets fails when a private key or the one-time token is where it must
// never be: the configuration on disk.
func noSecrets(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile(utils.CONFIGFOLDER + "cosmos.config.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "PRIVATE KEY") || strings.Contains(string(raw), sslToken) {
		t.Error("the configuration file holds a private key or the one-time token")
	}
}

func TestACertificateOfTheServersOwn(t *testing.T) {
	now := time.Now()
	stored, bundle, err := makeOwnCertificate(now)
	if err != nil {
		t.Fatal(err)
	}

	authority, err := utils.ParseOriginPullCA(stored.CA)
	if err != nil {
		t.Fatalf("what the configuration keeps is not an authority: %v", err)
	}
	if strings.Contains(stored.CA, "PRIVATE KEY") || strings.Count(stored.CA, "BEGIN CERTIFICATE") != 1 {
		t.Error("the configuration keeps more than the authority's certificate")
	}
	pair, err := tls.X509KeyPair([]byte(bundle.certificate), []byte(bundle.privateKey))
	if err != nil {
		t.Fatalf("the certificate and the key do not belong together: %v", err)
	}
	leaf, _ := x509.ParseCertificate(pair.Certificate[0])
	if leaf.IsCA || len(pair.Certificate) != 1 {
		t.Error("what goes to Cloudflare is not a single leaf certificate")
	}
	if len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Errorf("usages %v, want client authentication alone", leaf.ExtKeyUsage)
	}
	if utils.CertificateFingerprint(leaf.Raw) != stored.Fingerprint || bundle.fingerprint != stored.Fingerprint {
		t.Error("the fingerprint kept is not the leaf's")
	}
	if valid := leaf.NotAfter.Sub(now); valid < 364*24*time.Hour || valid > 366*24*time.Hour || !stored.NotAfter.Equal(leaf.NotAfter) {
		t.Errorf("valid for %v (kept: %v)", valid, stored.NotAfter)
	}
	if authority.NotAfter.Before(leaf.NotAfter) {
		t.Error("the authority ends before its certificate")
	}
	if until := bundle.until.Sub(now); until != pendingLifetime {
		t.Errorf("the key waits for %v", until)
	}

	// it verifies as the server's own, and only there
	cf := utils.ForkCloudflareConfig{OriginPulls: utils.ForkOriginPullsConfig{Mode: utils.OriginPullEnforce, Kind: utils.OriginPullOwn, Own: []utils.ForkOriginPullCert{stored}}}
	state := &tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}
	if problem, issuer := utils.OriginPullCheck(cf, state); problem != "" || issuer != issuerFingerprint(stored) {
		t.Errorf("the certificate does not verify under its own authority: %q", problem)
	}
	another, _, _ := makeOwnCertificate(now)
	cf.OriginPulls.Own = []utils.ForkOriginPullCert{another}
	if problem, _ := utils.OriginPullCheck(cf, state); problem != utils.OriginPullWrongCert {
		t.Errorf("it verifies under another authority made here: %q", problem)
	}
	if another.Fingerprint == stored.Fingerprint || another.CA == stored.CA {
		t.Error("two certificates made here are the same")
	}
}

// The whole way: choose the kind, make a certificate, hand it to Cloudflare,
// test, enforce, confirm, stop.
func TestOriginPullsAreSwitchedOnInGuardedSteps(t *testing.T) {
	env := pullsSetup(t)
	const host = "radarr.example.org"

	// nothing is on, and nothing can be until there is something to verify with
	code, answer := do(t, StatusRoute, admin(httptest.NewRequest("GET", "/x", nil)))
	state, _ := data(answer)["originPulls"].(map[string]interface{})
	if code != 200 || state["mode"] != "" || state["kind"] != "" || state["cannotTest"] != "no_kind" {
		t.Fatalf("at the start: %v", state)
	}
	refused(t, map[string]interface{}{"action": "mode", "mode": "test"}, "no_kind")
	refused(t, map[string]interface{}{"action": "mode", "mode": "enforce"}, "not_testing")
	refused(t, map[string]interface{}{"action": "kind", "kind": "any"}, "no_kind")
	refused(t, map[string]interface{}{"action": "mode", "mode": "always"}, "unknown_mode")
	refused(t, map[string]interface{}{"action": "upload", "zone": "example.org"}, "no_pending")
	refused(t, map[string]interface{}{"action": "reveal"}, "no_pending")
	refused(t, map[string]interface{}{"action": "confirm"}, "not_enforcing")

	mustAct(t, map[string]interface{}{"action": "kind", "kind": "own"})
	refused(t, map[string]interface{}{"action": "mode", "mode": "test"}, "no_certificate")

	// a certificate is made: its key waits in memory, the configuration has the authority only
	result := mustAct(t, map[string]interface{}{"action": "generate"})
	own := result["originPulls"].(map[string]interface{})["own"].([]interface{})
	if len(own) != 1 || own[0].(map[string]interface{})["waiting"] != true || own[0].(map[string]interface{})["handedOver"] != false {
		t.Fatalf("after making a certificate: %v", own)
	}
	noSecrets(t)
	// made, not handed over: nobody can present it, so there is nothing to test with
	refused(t, map[string]interface{}{"action": "mode", "mode": "test"}, "no_certificate")

	// the stored token is the "Edit zone DNS" one: Cloudflare refuses, and the key keeps waiting
	env.fake.Calls(true)
	code, reason, _ := act(t, map[string]interface{}{"action": "upload", "zone": "example.org"})
	if code != http.StatusBadGateway || reason != "forbidden" {
		t.Errorf("upload with the stored token: status %d reason %q", code, reason)
	}
	if certs, _ := env.fake.OriginPull("example.org"); len(certs) != 0 || pendingBundle() == nil || len(handedOver(pulls())) != 0 {
		t.Fatal("a refused upload handed the certificate over")
	}
	// a domain that is not one of the configuration's is not uploaded to, nor a token that is none
	refused(t, map[string]interface{}{"action": "upload", "zone": "example.net", "token": sslToken}, "not_cloudflare_zone")
	if code, reason, _ := act(t, map[string]interface{}{"action": "upload", "zone": "example.org", "token": "cfk_" + strings.Repeat("0a", 20)}); code != http.StatusBadGateway || reason != "malformed_token" {
		t.Errorf("upload with a Global API Key: status %d reason %q", code, reason)
	}

	// a one-time token with the permission: uploaded, switched on, the key gone
	result = mustAct(t, map[string]interface{}{"action": "upload", "zone": "example.org", "token": sslToken})
	if result["enabled"] != true {
		t.Errorf("the upload did not switch the feature on: %v", result["enabled"])
	}
	certs, enabled := env.fake.OriginPull("example.org")
	if len(certs) != 1 || !enabled {
		t.Fatalf("at Cloudflare: %d certificates, enabled %v", len(certs), enabled)
	}
	atCloudflare, err := tls.X509KeyPair([]byte(certs[0].Certificate), []byte(certs[0].PrivateKey))
	if err != nil {
		t.Fatalf("what Cloudflare got is not a certificate with its key: %v", err)
	}
	if pendingBundle() != nil || len(handedOver(pulls())) != 1 {
		t.Error("after the upload the key is still held, or the certificate does not count as handed over")
	}
	refused(t, map[string]interface{}{"action": "reveal"}, "no_pending")
	noSecrets(t)
	for _, call := range env.fake.Calls(false) {
		if strings.Contains(call.Path, "origin_tls_client_auth") && !strings.HasPrefix(call.Path, "/client/v4/zones/") {
			t.Errorf("unexpected call %s %s", call.Method, call.Path)
		}
	}

	// test: from now on Cloudflare's connections are asked, so the open ones are closed once
	mustAct(t, map[string]interface{}{"action": "mode", "mode": "test"})
	if got := pulls(); got.Mode != utils.OriginPullTest || env.recycled != 1 {
		t.Fatalf("after switching the test on: mode %q, connections recycled %d times", got.Mode, env.recycled)
	}

	// nothing verified yet: enforcing would cut everything off
	refused(t, map[string]interface{}{"action": "mode", "mode": "enforce"}, "nothing_verified")
	refused(t, map[string]interface{}{"action": "mode", "mode": "enforce", "anyway": true}, "nothing_verified")

	// what Cloudflare presents verifies against what the server kept
	cf := utils.ReadConfigFromFile().HTTPConfig.Cloudflare
	leaf, _ := x509.ParseCertificate(atCloudflare.Certificate[0])
	problem, issuer := utils.OriginPullCheck(cf, &tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}})
	if problem != "" {
		t.Fatalf("the uploaded certificate does not verify: %q", problem)
	}
	utils.CountOriginPull(host, problem, issuer)

	// a request without a certificate within the last minute: only with "anyway"
	utils.CountOriginPull(host, utils.OriginPullNoCert, "")
	refused(t, map[string]interface{}{"action": "mode", "mode": "enforce"}, "failing")
	result = mustAct(t, map[string]interface{}{"action": "mode", "mode": "enforce", "anyway": true})
	state = result["originPulls"].(map[string]interface{})
	if state["mode"] != "enforce" || state["unconfirmed"] != true || env.recycled != 1 {
		t.Fatalf("after enforcing: %v (recycled %d)", state, env.recycled)
	}
	if got := pulls(); got.Confirmed || time.Until(got.ConfirmBy) > utils.OriginPullConfirmWindow || time.Until(got.ConfirmBy) < utils.OriginPullConfirmWindow-time.Minute {
		t.Errorf("the confirmation window: %+v", got)
	}
	if counts := utils.OriginPullSnapshot(); counts.Verified != 0 || counts.Failed != 0 {
		t.Errorf("the counters were not started anew with enforcement: %+v", counts)
	}

	// while enforcing nothing that would stop requests from verifying is possible
	refused(t, map[string]interface{}{"action": "kind", "kind": "shared"}, "enforcing")
	refused(t, map[string]interface{}{"action": "retire", "fingerprint": pulls().Own[0].Fingerprint}, "last_certificate")

	mustAct(t, map[string]interface{}{"action": "confirm"})
	if got := pulls(); !got.Confirmed || utils.OriginPullMode(utils.GetMainConfig().HTTPConfig.Cloudflare) != utils.OriginPullEnforce {
		t.Errorf("after the confirmation: %+v", got)
	}
	// confirmed enforcement does not lapse
	watchOriginPulls(time.Now().Add(time.Hour))
	if got := pulls(); got.Mode != utils.OriginPullEnforce || len(env.notices) != 0 {
		t.Errorf("confirmed enforcement was ended by the watch: %+v %v", got, env.notices)
	}

	// one click back, and off
	mustAct(t, map[string]interface{}{"action": "mode", "mode": "test"})
	if got := pulls(); got.Mode != utils.OriginPullTest {
		t.Errorf("after stopping: %+v", got)
	}
	mustAct(t, map[string]interface{}{"action": "mode", "mode": "off"})
	if got := pulls(); got.Mode != utils.OriginPullOff || got.Kind != utils.OriginPullOwn || len(got.Own) != 1 {
		t.Errorf("after switching off: %+v", got)
	}
	noSecrets(t)
}

// Uploading by hand: the certificate and its key are shown once.
func TestTheKeyIsShownOnce(t *testing.T) {
	pullsSetup(t)
	mustAct(t, map[string]interface{}{"action": "kind", "kind": "own"})
	mustAct(t, map[string]interface{}{"action": "generate"})

	raw := `{"action":"reveal"}`
	recorder := httptest.NewRecorder()
	OriginPullsRoute(recorder, admin(httptest.NewRequest("POST", "/x", strings.NewReader(raw))))
	if recorder.Code != 200 || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("reveal: status %d, Cache-Control %q", recorder.Code, recorder.Header().Get("Cache-Control"))
	}
	code, answer := post(t, OriginPullsRoute, map[string]interface{}{"action": "reveal"})
	if code != http.StatusBadRequest || answer["reason"] != "no_pending" {
		t.Errorf("a second reveal: status %d %v", code, answer["reason"])
	}
	body := recorder.Body.String()
	if strings.Count(body, "PRIVATE KEY") != 2 || strings.Count(body, "BEGIN CERTIFICATE") != 1 {
		t.Errorf("the answer does not hold one certificate and one key")
	}
	if pendingBundle() != nil || len(handedOver(pulls())) != 1 {
		t.Error("after it was shown the key is still held, or the certificate does not count as handed over")
	}
	noSecrets(t)

	// the status never shows a key, to anyone
	_, status := do(t, StatusRoute, admin(httptest.NewRequest("GET", "/x", nil)))
	if text := toJSON(status); strings.Contains(text, "PRIVATE KEY") || strings.Contains(text, "BEGIN CERTIFICATE") {
		t.Error("the status holds a certificate or a key")
	}
	// with a certificate handed over, the test can begin
	mustAct(t, map[string]interface{}{"action": "mode", "mode": "test"})
}

// A key that was never handed over is forgotten after a quarter of an hour,
// or when the administrator says so, and its certificate with it.
func TestAKeyThatWasNotHandedOverIsForgotten(t *testing.T) {
	pullsSetup(t)
	mustAct(t, map[string]interface{}{"action": "generate"})
	if len(pulls().Own) != 1 || pendingBundle() == nil {
		t.Fatal("nothing was made")
	}
	// making another replaces the one that was never handed over
	first := pulls().Own[0].Fingerprint
	mustAct(t, map[string]interface{}{"action": "generate"})
	if got := pulls().Own; len(got) != 1 || got[0].Fingerprint == first {
		t.Errorf("after making a second one: %+v", got)
	}

	pendingMu.Lock()
	pending.until = time.Now().Add(-time.Second)
	pendingMu.Unlock()
	if pendingBundle() != nil {
		t.Error("the key is still held after its time")
	}
	watchOriginPulls(time.Now())
	if got := pulls().Own; len(got) != 0 {
		t.Errorf("the certificate nobody can present is still accepted: %+v", got)
	}

	mustAct(t, map[string]interface{}{"action": "generate"})
	mustAct(t, map[string]interface{}{"action": "discard"})
	if pendingBundle() != nil || len(pulls().Own) != 0 {
		t.Error("discarding left the key or the certificate")
	}
}

// Enforcement the administrator's browser did not confirm (it was cut off)
// ends by itself, once, and says so.
func TestUnconfirmedEnforcementFallsBack(t *testing.T) {
	env := pullsSetup(t)
	mustAct(t, map[string]interface{}{"action": "kind", "kind": "shared"})
	mustAct(t, map[string]interface{}{"action": "mode", "mode": "test"})
	utils.CountOriginPull("radarr.example.org", "", "cloudflare")
	mustAct(t, map[string]interface{}{"action": "mode", "mode": "enforce"})

	// within the window: enforced
	watchOriginPulls(time.Now().Add(utils.OriginPullConfirmWindow - 10*time.Second))
	if got := pulls(); got.Mode != utils.OriginPullEnforce || len(env.notices) != 0 {
		t.Fatalf("within the window: %+v %v", got, env.notices)
	}

	// the window passes without a confirmation
	watchOriginPulls(time.Now().Add(utils.OriginPullConfirmWindow + time.Second))
	if got := pulls(); got.Mode != utils.OriginPullTest || got.Confirmed {
		t.Errorf("after the window: %+v", got)
	}
	if len(env.notices) != 1 || !strings.HasPrefix(env.notices[0], TitleOriginPullStopped) {
		t.Errorf("notifications: %v", env.notices)
	}
	// a confirmation that arrives later does not bring enforcement back
	refused(t, map[string]interface{}{"action": "confirm"}, "not_enforcing")
	watchOriginPulls(time.Now().Add(time.Hour))
	if len(env.notices) != 1 {
		t.Errorf("told more than once: %v", env.notices)
	}
}

// Changing the certificate: both are accepted, and the old one can only go
// once a request has verified with the new one.
func TestChangingTheCertificateWhileEnforcing(t *testing.T) {
	pullsSetup(t)
	mustAct(t, map[string]interface{}{"action": "kind", "kind": "own"})
	mustAct(t, map[string]interface{}{"action": "generate"})
	mustAct(t, map[string]interface{}{"action": "reveal"})
	mustAct(t, map[string]interface{}{"action": "mode", "mode": "test"})
	old := pulls().Own[0]
	utils.CountOriginPull("radarr.example.org", "", issuerFingerprint(old))
	mustAct(t, map[string]interface{}{"action": "mode", "mode": "enforce"})
	mustAct(t, map[string]interface{}{"action": "confirm"})

	mustAct(t, map[string]interface{}{"action": "generate"})
	mustAct(t, map[string]interface{}{"action": "reveal"})
	both := pulls().Own
	if len(both) != 2 || both[0].Fingerprint != old.Fingerprint {
		t.Fatalf("during the change: %+v", both)
	}
	refused(t, map[string]interface{}{"action": "generate"}, "two_certificates")

	// nothing verified with the new one yet (Cloudflare still presents the old one)
	utils.CountOriginPull("radarr.example.org", "", issuerFingerprint(old))
	refused(t, map[string]interface{}{"action": "retire", "fingerprint": old.Fingerprint}, "not_seen")
	for _, bad := range []string{"", "abc", strings.Repeat("g", 64), strings.Repeat("A", 64)} {
		if code, _, _ := act(t, map[string]interface{}{"action": "retire", "fingerprint": bad}); code != http.StatusBadRequest {
			t.Errorf("retire %q: status %d", bad, code)
		}
	}
	refused(t, map[string]interface{}{"action": "retire", "fingerprint": strings.Repeat("0", 64)}, "no_certificate")

	utils.CountOriginPull("radarr.example.org", "", issuerFingerprint(both[1]))
	mustAct(t, map[string]interface{}{"action": "retire", "fingerprint": old.Fingerprint})
	if got := pulls(); len(got.Own) != 1 || got.Own[0].Fingerprint != both[1].Fingerprint || got.Mode != utils.OriginPullEnforce {
		t.Errorf("after the change: %+v", got)
	}
}

func TestRemindersBeforeACertificateExpires(t *testing.T) {
	for _, c := range []struct {
		days, reminded, want int
	}{
		{200, 0, 0}, {31, 0, 0}, {30, 0, 30}, {20, 0, 30}, {20, 30, 0}, {14, 30, 14}, {10, 0, 14},
		{10, 14, 0}, {3, 14, 3}, {0, 14, 3}, {0, 3, 0}, {2, 0, 3},
	} {
		if got := dueReminder(ownReminders, c.days, c.reminded); got != c.want {
			t.Errorf("%d days left, last reminder at %d: %d, want %d", c.days, c.reminded, got, c.want)
		}
	}

	env := pullsSetup(t)
	mustAct(t, map[string]interface{}{"action": "kind", "kind": "own"})
	mustAct(t, map[string]interface{}{"action": "generate"})
	mustAct(t, map[string]interface{}{"action": "reveal"})
	expiry := pulls().Own[0].NotAfter

	// off: nobody depends on the certificate, nobody is reminded
	remindOriginPulls(expiry.Add(-5 * 24 * time.Hour))
	if len(env.notices) != 0 {
		t.Fatalf("reminded while the option is off: %v", env.notices)
	}
	mustAct(t, map[string]interface{}{"action": "mode", "mode": "test"})

	remindOriginPulls(expiry.Add(-100 * 24 * time.Hour))
	if len(env.notices) != 0 {
		t.Fatalf("reminded 100 days before: %v", env.notices)
	}
	for i := 0; i < 3; i++ {
		remindOriginPulls(expiry.Add(-20*24*time.Hour - time.Hour))
	}
	if len(env.notices) != 1 || !strings.HasPrefix(env.notices[0], TitleOriginPullExpiring+"|20 days (") {
		t.Fatalf("20 days before: %v", env.notices)
	}
	remindOriginPulls(expiry.Add(-10*24*time.Hour - time.Hour))
	remindOriginPulls(expiry.Add(-9*24*time.Hour - time.Hour))
	if len(env.notices) != 2 || pulls().Own[0].Reminded != 14 {
		t.Errorf("10 days before: %v (kept: %d)", env.notices, pulls().Own[0].Reminded)
	}

	// Cloudflare's own certificate: reminded from 60 days before its end
	mustAct(t, map[string]interface{}{"action": "kind", "kind": "shared"})
	builtin, _ := utils.CloudflareOriginPullCA()
	env.notices = nil
	remindOriginPulls(builtin.NotAfter.Add(-61 * 24 * time.Hour))
	remindOriginPulls(builtin.NotAfter.Add(-59 * 24 * time.Hour))
	remindOriginPulls(builtin.NotAfter.Add(-58 * 24 * time.Hour))
	if len(env.notices) != 1 || pulls().SharedReminded != 60 {
		t.Errorf("Cloudflare's certificate: %v (kept: %d)", env.notices, pulls().SharedReminded)
	}
}

// The upload sends a leaf and its key to Cloudflare's API host and nowhere
// else; a failure to switch the feature on is said, not hidden.
func TestUploadWithTheStoredTokenWhenItMay(t *testing.T) {
	env := pullsSetup(t)
	// the administrator widened the stored token at Cloudflare themselves
	env.fake.AddToken(goodToken, cffake.Token{Zones: []string{"example.org"}, ReadDNS: true, EditDNS: true, EditSSL: true})
	mustAct(t, map[string]interface{}{"action": "generate"})
	result := mustAct(t, map[string]interface{}{"action": "upload", "zone": "EXAMPLE.org"})
	certs, enabled := env.fake.OriginPull("example.org")
	if result["enabled"] != true || len(certs) != 1 || !enabled {
		t.Fatalf("upload with the stored token: %v, %d certificates, enabled %v", result["enabled"], len(certs), enabled)
	}
	block, _ := pem.Decode([]byte(certs[0].Certificate))
	if cert, err := x509.ParseCertificate(block.Bytes); err != nil || cert.IsCA {
		t.Error("what was uploaded is not a leaf certificate")
	}
	if text := toJSON(result); strings.Contains(text, "PRIVATE KEY") || strings.Contains(text, goodToken) {
		t.Error("the answer of an upload holds the key or the token")
	}
}

func toJSON(value interface{}) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

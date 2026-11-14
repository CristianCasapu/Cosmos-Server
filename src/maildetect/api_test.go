package maildetect

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// call sends a request as: "none" (no session), "user", "admin" (an
// administrator who has not confirmed the password), "sudo" (one who has),
// "token" (an API token without the admin permission).
func call(method, who, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/cosmos/api/config/email-detect", strings.NewReader(body))
	var auth *utils.AuthContext
	admin := []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN_READ, utils.PERM_ADMIN, utils.PERM_CONFIGURATION, utils.PERM_CREDENTIALS_READ}
	switch who {
	case "user":
		auth = &utils.AuthContext{Nickname: "someone", Permissions: []utils.Permission{utils.PERM_LOGIN}}
	case "reader":
		auth = &utils.AuthContext{Nickname: "reader", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN_READ, utils.PERM_CONFIGURATION_READ}}
	case "admin":
		auth = &utils.AuthContext{Nickname: "admin", Permissions: admin}
	case "sudo":
		auth = &utils.AuthContext{Nickname: "admin", Permissions: admin, IsSudoed: true}
	case "token":
		auth = &utils.AuthContext{APIToken: &utils.APITokenContext{Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_CONFIGURATION_READ}}}
	}
	if auth != nil {
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
	}
	rec := httptest.NewRecorder()
	Route(rec, req)
	return rec
}

// offline makes the route use a detector without any network.
func offline(t *testing.T) (*fakeResolver, *dialLog) {
	t.Helper()
	resolver := &fakeResolver{}
	d, log := testDetector(resolver, nil, nil)
	routeDetector, routeLimiter = d, &limiter{}
	t.Cleanup(func() { routeDetector, routeLimiter = NewDetector(), &limiter{} })
	return resolver, log
}

func TestRouteIsForAdministratorsWhoConfirmedThePassword(t *testing.T) {
	resolver, log := offline(t)
	body := `{"address":"me@example.org"}`
	for who, want := range map[string]int{
		"none": http.StatusUnauthorized, "user": http.StatusUnauthorized, "reader": http.StatusUnauthorized,
		"admin": http.StatusUnauthorized, "token": http.StatusForbidden,
	} {
		if rec := call("POST", who, body); rec.Code != want {
			t.Errorf("%s: %d, want %d", who, rec.Code, want)
		}
	}
	if len(resolver.asked) != 0 || len(log.all()) != 0 {
		t.Fatalf("a refused caller made the server look something up: %v %v", resolver.asked, log.all())
	}
	if rec := call("POST", "sudo", body); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"domain":"example.org"`) {
		t.Errorf("administrator: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRouteTakesPOSTOnly(t *testing.T) {
	resolver, _ := offline(t)
	for _, method := range []string{"GET", "PUT", "DELETE", "PATCH", "HEAD"} {
		if rec := call(method, "sudo", `{"address":"me@example.org"}`); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", method, rec.Code)
		}
	}
	if len(resolver.asked) != 0 {
		t.Errorf("questions: %v", resolver.asked)
	}
}

func TestRouteBoundsItsInput(t *testing.T) {
	resolver, _ := offline(t)
	for name, body := range map[string]string{
		"too large":      `{"address":"me@example.org","pad":"` + strings.Repeat("x", 2000) + `"}`,
		"padded":         `{"address":"me@example.org"}` + strings.Repeat(" ", 2000),
		"large address":  `{"address":"` + strings.Repeat("a", 990) + `@example.org"}`,
		"not JSON":       `address=me@example.org`,
		"empty":          ``,
		"a password":     `{"address":"me@example.org","password":"hunter2"}`,
		"a whole config": `{"address":"me@example.org","EmailConfig":{"Password":"hunter2"}}`,
		"no address":     `{}`,
		"not an address": `{"address":"nobody"}`,
		"an IP address":  `{"address":"me@127.0.0.1"}`,
		"localhost":      `{"address":"me@localhost"}`,
		"a URL":          `{"address":"http://169.254.169.254/latest/meta-data"}`,
		"a port":         `{"address":"me@example.org:6379"}`,
		"a number":       `{"address":42}`,
		"control chars":  `{"address":"me@example.org\r\nQUIT"}`,
	} {
		rec := call("POST", "sudo", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "hunter2") {
			t.Errorf("%s: the answer repeats the request", name)
		}
	}
	if len(resolver.asked) != 0 {
		t.Errorf("a refused request made the server look something up: %v", resolver.asked)
	}
}

func TestRouteIsRateLimited(t *testing.T) {
	offline(t)
	body := `{"address":"me@example.org"}`
	for i := 0; i < perMinute; i++ {
		if rec := call("POST", "sudo", body); rec.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i+1, rec.Code)
		}
	}
	if rec := call("POST", "sudo", body); rec.Code != http.StatusTooManyRequests {
		t.Errorf("request %d: %d, want 429", perMinute+1, rec.Code)
	}

	// one at a time, and the count forgets after a minute
	l := &limiter{}
	now := time.Now()
	if !l.start(now) || l.start(now) {
		t.Error("two detections ran at once")
	}
	l.done()
	for i := 1; i < perMinute; i++ {
		if !l.start(now) {
			t.Fatalf("start %d refused", i+1)
		}
		l.done()
	}
	if l.start(now.Add(30 * time.Second)) {
		t.Error("more than the limit within a minute")
	}
	if !l.start(now.Add(61 * time.Second)) {
		t.Error("still limited after a minute")
	}
}

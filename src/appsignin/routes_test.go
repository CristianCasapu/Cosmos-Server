package appsignin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/utils"
)

func withFakes(t *testing.T, containers map[string]containerInfo) *[]utils.SecurityEvent {
	t.Helper()
	oldInspect, oldRoutes, oldURL, oldUser, oldConfig := inspect, routesNow, serverURL, userRecord, configNow
	inspect = func(ctx context.Context, name string) (containerInfo, error) {
		c, ok := containers[name]
		if !ok {
			return containerInfo{}, errors.New("no such container")
		}
		return c, nil
	}
	routesNow = noRoutes
	oldManagers := managersOf
	managersOf = func(context.Context, string) []string { return []string{} }
	t.Cleanup(func() { managersOf = oldManagers })
	serverURL = testServerURL
	userRecord = func(nickname string) (string, error) { return nickname + "@example.org", nil }
	configNow = func() utils.Config { return utils.Config{} }
	revealPerCaller = newWindow(20, time.Minute)
	revealAll = newWindow(120, time.Minute)
	recorded := &[]utils.SecurityEvent{}
	utils.SetSecurityRecorder(func(e utils.SecurityEvent) { *recorded = append(*recorded, e) }, func() {})
	t.Cleanup(func() {
		inspect, routesNow, serverURL, userRecord, configNow = oldInspect, oldRoutes, oldURL, oldUser, oldConfig
		utils.SetSecurityRecorder(func(utils.SecurityEvent) {}, func() {})
	})
	return recorded
}

var (
	sudoed       = &utils.AuthContext{Nickname: "owner", IsSudoed: true, Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_RESOURCES_READ, utils.PERM_RESOURCES, utils.PERM_CREDENTIALS_READ}}
	notSudo      = &utils.AuthContext{Nickname: "owner", IsSudoed: false, Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_RESOURCES_READ, utils.PERM_RESOURCES, utils.PERM_CREDENTIALS_READ}}
	noCreds      = &utils.AuthContext{Nickname: "helper", IsSudoed: true, Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_RESOURCES_READ, utils.PERM_RESOURCES}}
	configReader = &utils.AuthContext{Nickname: "owner", IsSudoed: true, Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_RESOURCES, utils.PERM_CONFIGURATION_READ, utils.PERM_CREDENTIALS_READ}}
	tokenNo      = &utils.AuthContext{APIToken: &utils.APITokenContext{Name: "deploy", Permissions: []utils.Permission{utils.PERM_RESOURCES_READ, utils.PERM_RESOURCES}}}
	tokenYes     = &utils.AuthContext{APIToken: &utils.APITokenContext{Name: "vault", Permissions: []utils.Permission{utils.PERM_RESOURCES_READ, utils.PERM_CREDENTIALS_READ}}}
)

func call(handler http.HandlerFunc, method string, path string, vars map[string]string, body string, auth *utils.AuthContext) (int, map[string]interface{}, string) {
	req := httptest.NewRequest(method, "http://cosmos.example.org"+path, strings.NewReader(body))
	if auth != nil {
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
	}
	req = mux.SetURLVars(req, vars)
	w := httptest.NewRecorder()
	handler(w, req)
	answer := map[string]interface{}{}
	json.Unmarshal(w.Body.Bytes(), &answer)
	return w.Code, answer, w.Body.String()
}

func TestTheCardNeedsCredentialsReadAndTheConfirmation(t *testing.T) {
	withFakes(t, map[string]containerInfo{"notes-db": postgresContainer()})
	vars := map[string]string{"containerId": "notes-db"}
	path := "/cosmos/api/servapps/notes-db/sign-in"
	for _, c := range []struct {
		auth *utils.AuthContext
		code int
		key  string
	}{
		{nil, http.StatusUnauthorized, "HTTP004"},
		{notSudo, http.StatusUnauthorized, "HTTP008"},
		{noCreds, http.StatusUnauthorized, "HTTP005"},
		{tokenNo, http.StatusForbidden, "AT003"},
	} {
		code, answer, body := call(SignInRoute, "GET", path, vars, "", c.auth)
		if code != c.code || answer["code"] != c.key {
			t.Errorf("%+v: %d %s", c.auth, code, body)
		}
		mustHaveNoSecret(t, "a refusal", body, testPassword)
		code, _, body = call(RevealRoute, "POST", path+"/reveal", vars, `{"secret":"password.0","purpose":"show"}`, c.auth)
		if code != c.code {
			t.Errorf("reveal %+v: %d %s", c.auth, code, body)
		}
		mustHaveNoSecret(t, "a refusal", body, testPassword)
	}
	for _, auth := range []*utils.AuthContext{sudoed, tokenYes} {
		code, _, body := call(SignInRoute, "GET", path, vars, "", auth)
		if code != http.StatusOK || !strings.Contains(body, `"user":"owner_name"`) {
			t.Fatalf("allowed: %d %s", code, body)
		}
		mustHaveNoSecret(t, "the card's answer", body, testPassword, testOther)
	}
}

func TestRevealIsAPostOfOneValueAndIsRecordedWithoutIt(t *testing.T) {
	recorded := withFakes(t, map[string]containerInfo{"notes-db": postgresContainer()})
	vars := map[string]string{"containerId": "notes-db"}
	path := "/cosmos/api/servapps/notes-db/sign-in/reveal"
	if code, _, _ := call(RevealRoute, "GET", path, vars, "", sudoed); code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", code)
	}
	for _, body := range []string{`{}`, `{"secret":"OTHER_TOKEN","purpose":"show"}`, `{"secret":"password.0","purpose":"print"}`, `{"secret":"password.0","purpose":"show","extra":1}`, strings.Repeat("x", 2000)} {
		if code, _, _ := call(RevealRoute, "POST", path, vars, body, sudoed); code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, code)
		}
	}
	code, answer, body := call(RevealRoute, "POST", path, vars, `{"secret":"password.0","purpose":"copy"}`, sudoed)
	if code != http.StatusOK || answer["data"].(map[string]interface{})["value"] != testPassword {
		t.Fatalf("reveal: %d %s", code, body)
	}
	if len(*recorded) != 1 {
		t.Fatalf("recorded: %+v", *recorded)
	}
	e := (*recorded)[0]
	if e.Kind != utils.SecCredentialsRevealed || e.Target != "notes-db" || e.Details["secret"] != "password.0" || e.Details["purpose"] != "copy" || e.Actor != "owner" {
		t.Fatalf("event: %+v", e)
	}
	raw, _ := json.Marshal(e)
	mustHaveNoSecret(t, "the security record", string(raw), testPassword)
	// a container that has no such secret
	if code, _, _ := call(RevealRoute, "POST", path, vars, `{"secret":"password.2","purpose":"show"}`, sudoed); code != http.StatusNotFound {
		t.Errorf("missing secret: %d", code)
	}
	if code, _, _ := call(RevealRoute, "POST", "/x", map[string]string{"containerId": "../etc"}, `{"secret":"password.0","purpose":"show"}`, sudoed); code != http.StatusBadRequest {
		t.Errorf("bad name: %d", code)
	}
}

func TestRevealIsLimited(t *testing.T) {
	withFakes(t, map[string]containerInfo{"notes-db": postgresContainer()})
	vars := map[string]string{"containerId": "notes-db"}
	ok := 0
	for i := 0; i < 25; i++ {
		code, _, _ := call(RevealRoute, "POST", "/r", vars, `{"secret":"password.0","purpose":"show"}`, sudoed)
		if code == http.StatusOK {
			ok++
		} else if code != http.StatusTooManyRequests {
			t.Fatalf("code %d", code)
		}
	}
	if ok != 20 {
		t.Fatalf("allowed %d a minute", ok)
	}
	// another caller still may
	if code, _, _ := call(RevealRoute, "POST", "/r", vars, `{"secret":"password.0","purpose":"show"}`, tokenYes); code != http.StatusOK {
		t.Fatalf("other caller: %d", code)
	}
}

func TestTheDefaultsAreTheCallersOwn(t *testing.T) {
	withFakes(t, nil)
	configNow = func() utils.Config {
		c := utils.Config{}
		c.EmailConfig.Enabled = true
		return c
	}
	code, answer, body := call(DefaultsRoute, "GET", "/d", nil, "", sudoed)
	data, _ := answer["data"].(map[string]interface{})
	if code != http.StatusOK || data["user"] != "owner" || data["email"] != "owner@example.org" || data["userFrom"] != "account" || data["length"] != float64(24) {
		t.Fatalf("defaults: %d %s", code, body)
	}
	if mail := data["mail"].(map[string]interface{}); mail["on"] != true || mail["ready"] != true {
		t.Fatalf("mail: %s", body)
	}
	configNow = func() utils.Config {
		c := utils.Config{}
		c.AppSignIn = utils.ForkAppSignInConfig{User: "house", Email: "house@example.org", MailOff: true, Length: 32}
		return c
	}
	// one who may not see the configured values unmasked gets their own (review minor 4)
	_, answer, body = call(DefaultsRoute, "GET", "/d", nil, "", sudoed)
	data, _ = answer["data"].(map[string]interface{})
	if data["user"] != "owner" || data["email"] != "owner@example.org" || data["userFrom"] != "account" {
		t.Fatalf("resources only: %s", body)
	}
	_, answer, body = call(DefaultsRoute, "GET", "/d", nil, "", configReader)
	data, _ = answer["data"].(map[string]interface{})
	if data["user"] != "house" || data["email"] != "house@example.org" || data["userFrom"] != "config" || data["length"] != float64(32) || data["mail"].(map[string]interface{})["why"] != "off" {
		t.Fatalf("configured: %s", body)
	}
	if code, _, _ := call(DefaultsRoute, "GET", "/d", nil, "", notSudo); code != http.StatusUnauthorized {
		t.Fatalf("not confirmed: %d", code)
	}
	if code, _, _ := call(DefaultsRoute, "POST", "/d", nil, "", sudoed); code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", code)
	}
}

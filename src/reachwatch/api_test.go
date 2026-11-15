package reachwatch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

func as(method, path, who, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if who == "none" {
		return req
	}
	auth := &utils.AuthContext{Nickname: "someone", Permissions: []utils.Permission{utils.PERM_LOGIN}}
	switch who {
	case "reader":
		auth.Permissions = append(auth.Permissions, utils.PERM_CONFIGURATION_READ)
	case "adminRead":
		auth.Permissions = append(auth.Permissions, utils.PERM_CONFIGURATION_READ, utils.PERM_ADMIN_READ)
	case "admin":
		auth.Permissions = append(auth.Permissions, utils.PERM_CONFIGURATION_READ, utils.PERM_ADMIN_READ, utils.PERM_ADMIN, utils.PERM_CONFIGURATION)
		auth.IsSudoed = true
	case "adminNoSudo":
		auth.Permissions = append(auth.Permissions, utils.PERM_CONFIGURATION_READ, utils.PERM_ADMIN_READ, utils.PERM_ADMIN, utils.PERM_CONFIGURATION)
	}
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
}

func call(route http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	route(rec, req)
	return rec
}

func TestTheRoutesCheckWhoAsks(t *testing.T) {
	newRig(t, "example.org")
	cases := []struct {
		route  http.HandlerFunc
		method string
		path   string
		who    string
		body   string
		want   int
	}{
		{StatusRoute, "GET", "/cosmos/api/reach-watch", "none", "", 401},
		{StatusRoute, "GET", "/cosmos/api/reach-watch", "reader", "", 401},
		{StatusRoute, "GET", "/cosmos/api/reach-watch", "adminRead", "", 200},
		{StatusRoute, "POST", "/cosmos/api/reach-watch", "admin", "", 405},
		{CheckRoute, "GET", "/cosmos/api/reach-watch/check", "admin", "", 405},
		{CheckRoute, "POST", "/cosmos/api/reach-watch/check", "none", "", 401},
		{CheckRoute, "POST", "/cosmos/api/reach-watch/check", "adminRead", "", 401},
		{CheckRoute, "POST", "/cosmos/api/reach-watch/check", "adminNoSudo", "", 401},
		{SettingsRoute, "GET", "/cosmos/api/reach-watch/settings", "admin", "", 405},
		{SettingsRoute, "POST", "/cosmos/api/reach-watch/settings", "none", `{"enabled":false}`, 401},
		{SettingsRoute, "POST", "/cosmos/api/reach-watch/settings", "adminRead", `{"enabled":false}`, 401},
		{SettingsRoute, "POST", "/cosmos/api/reach-watch/settings", "adminNoSudo", `{"enabled":false}`, 401},
		{SettingsRoute, "POST", "/cosmos/api/reach-watch/settings", "admin", `{"enabled":false,"url":"https://203.0.113.9/"}`, 400},
		{SettingsRoute, "POST", "/cosmos/api/reach-watch/settings", "admin", `{}`, 400},
		{SettingsRoute, "POST", "/cosmos/api/reach-watch/settings", "admin", `{"minutes":1}`, 400},
		{SettingsRoute, "POST", "/cosmos/api/reach-watch/settings", "admin", `{"minutes":61}`, 400},
		{SettingsRoute, "POST", "/cosmos/api/reach-watch/settings", "admin", `{"minutes":5` + strings.Repeat(" ", 2000) + `}`, 413},
	}
	for _, c := range cases {
		if got := call(c.route, as(c.method, c.path, c.who, c.body)).Code; got != c.want {
			t.Errorf("%s %s as %s %q: %d, want %d", c.method, c.path, c.who, c.body[:min(len(c.body), 40)], got, c.want)
		}
	}
}

func TestCheckNowStartsARound(t *testing.T) {
	r := newRig(t, "example.org")
	rec := call(CheckRoute, as("POST", "/cosmos/api/reach-watch/check", "admin", ""))
	var body struct {
		Data struct {
			Started bool   `json:"started"`
			Status  Status `json:"status"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 200 || !body.Data.Started {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if got := r.waitTested(1); len(got) != 1 {
		t.Fatalf("tested %v", got)
	}
	rec = call(CheckRoute, as("POST", "/cosmos/api/reach-watch/check", "admin", ""))
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Data.Started {
		t.Fatal("a second round within 15 s")
	}
}

func TestSettingsAreSavedAndRecorded(t *testing.T) {
	newRig(t, "example.org")
	previousFolder := utils.CONFIGFOLDER
	previousConfig := utils.GetBaseMainConfig()
	utils.CONFIGFOLDER = t.TempDir() + "/"
	utils.SetBaseMainConfig(utils.Config{})
	events := []utils.SecurityEvent{}
	utils.SetSecurityRecorder(func(e utils.SecurityEvent) { events = append(events, e) }, func() {})
	t.Cleanup(func() {
		utils.CONFIGFOLDER = previousFolder
		utils.LoadBaseMainConfig(previousConfig)
		utils.SetSecurityRecorder(func(utils.SecurityEvent) {}, func() {})
	})

	rec := call(SettingsRoute, as("POST", "/cosmos/api/reach-watch/settings", "admin", `{"enabled":false,"minutes":15}`))
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	watch := utils.ReadConfigFromFile().HTTPConfig.Cloudflare.Watch
	if !watch.Off || watch.Minutes != 15 || utils.ReachWatchInterval(watch) != 15*time.Minute {
		t.Fatalf("%+v", watch)
	}
	found := false
	for _, e := range events {
		found = found || e.Kind == utils.SecConfigSaved
	}
	if !found {
		t.Error("not in the security record")
	}
	if strings.Contains(rec.Body.String(), "token") {
		t.Error("an answer mentions a token")
	}
}

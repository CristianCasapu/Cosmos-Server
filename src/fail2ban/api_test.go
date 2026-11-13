package fail2ban

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// apiRequest builds a request from nobody ("none"), from a logged-in user
// without the permissions ("user"), from someone who may read the logs
// ("reader"), from an administrator who has not confirmed the password
// ("admin") or from one who has ("sudo").
func apiRequest(method string, path string, who string, body string) *http.Request {
	req := httptest.NewRequest(method, "/cosmos"+path, strings.NewReader(body))
	req.RemoteAddr = "203.0.113.50:40000"
	if who == "none" {
		return req
	}
	auth := &utils.AuthContext{Nickname: "someone", Permissions: []utils.Permission{utils.PERM_LOGIN}, IsSudoed: who == "sudo"}
	switch who {
	case "reader":
		auth.Permissions = append(auth.Permissions, utils.PERM_ADMIN_READ)
	case "admin", "sudo":
		auth.Permissions = append(auth.Permissions, utils.PERM_ADMIN_READ, utils.PERM_ADMIN)
	}
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
}

func call(handler http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func statusOf(t *testing.T, rec *httptest.ResponseRecorder) Status {
	t.Helper()
	var response struct {
		Status string
		Data   Status
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.Status != "OK" {
		t.Fatalf("answer %d %s (%v)", rec.Code, rec.Body.String(), err)
	}
	return response.Data
}

func settingsBody(t *testing.T, settings Settings) string {
	t.Helper()
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestRoutesNeedASessionThePermissionAndTheMethod(t *testing.T) {
	useJailDir(t, true)
	useConfig(t, utils.Config{})
	client := useFake(t, true, realAnswers(t))
	saved := captureSaved(t)
	recorded := captureSecurity(t)

	unbanBody := `{"ip":"203.0.113.7","jail":"cosmos-auth"}`
	routes := []struct {
		path    string
		handler http.HandlerFunc
		method  string
		body    string
	}{
		{"/api/fail2ban", StatusRoute, "GET", ""},
		{"/api/fail2ban/unban", UnbanRoute, "POST", unbanBody},
		{"/api/fail2ban/settings", SettingsRoute, "POST", settingsBody(t, defaultSettings())},
	}

	for _, route := range routes {
		// nobody: refused whatever the method, before anything is read or run
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
			if rec := call(route.handler, apiRequest(method, route.path, "none", route.body)); rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s without a session: status %d, want 401", method, route.path, rec.Code)
			}
		}
		// a logged-in user without the permission
		if rec := call(route.handler, apiRequest(route.method, route.path, "user", route.body)); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s as a user without the permission: status %d, want 401", route.method, route.path, rec.Code)
		}
		// the other methods are not served, even to an administrator
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"} {
			if method == route.method {
				continue
			}
			if rec := call(route.handler, apiRequest(method, route.path, "sudo", route.body)); rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: status %d, want 405", method, route.path, rec.Code)
			}
		}
	}
	// changing something takes an administrator who confirmed the password:
	// neither a reader of the logs nor an administrator who did not
	for _, route := range routes[1:] {
		for _, who := range []string{"reader", "admin"} {
			if rec := call(route.handler, apiRequest("POST", route.path, who, route.body)); rec.Code != http.StatusUnauthorized {
				t.Errorf("POST %s as %s: status %d, want 401", route.path, who, rec.Code)
			}
		}
	}

	if calls := client.called(); len(calls) != 0 {
		t.Errorf("fail2ban-client was run for refused requests: %v", calls)
	}
	if _, err := os.Stat(filepath.Join(jailDir, panelFile)); !os.IsNotExist(err) {
		t.Error("a settings file was written for a refused request")
	}
	if len(*saved) != 0 {
		t.Errorf("settings were saved for a refused request: %+v", *saved)
	}
	for _, event := range recorded() {
		if event.Kind == utils.SecFail2banUnban || event.Kind == utils.SecFail2banSettings {
			t.Errorf("a refused request was recorded as done: %+v", event)
		}
	}

	// reading takes the permission of the logs page, no password confirmation
	for _, who := range []string{"reader", "admin", "sudo"} {
		rec := call(StatusRoute, apiRequest("GET", "/api/fail2ban", who, ""))
		if rec.Code != http.StatusOK {
			t.Errorf("GET /api/fail2ban as %s: status %d, want 200", who, rec.Code)
			continue
		}
		if status := statusOf(t, rec); !status.Running || status.ClientIP != "203.0.113.50" || status.ClientIgnored {
			t.Errorf("status as %s = %+v", who, status)
		}
	}
}

func TestUnbanRoute(t *testing.T) {
	useJailDir(t, true)
	useConfig(t, utils.Config{})
	client := useFake(t, true, realAnswers(t))
	recorded := captureSecurity(t)

	// one jail
	rec := call(UnbanRoute, apiRequest("POST", "/api/fail2ban/unban", "sudo", `{"ip":"203.0.113.7","jail":"cosmos-auth"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	statusOf(t, rec)
	if client.count("set") != 1 || client.count("set", "cosmos-auth", "unbanip", "203.0.113.7") != 1 {
		t.Errorf("calls = %v, want one unbanip in cosmos-auth", client.called())
	}

	// every jail of the fork that is loaded; the address in its canonical form
	rec = call(UnbanRoute, apiRequest("POST", "/api/fail2ban/unban", "sudo", `{"ip":"2001:DB8:0:0::7"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	for _, known := range jails {
		if client.count("set", known.name, "unbanip", "2001:db8::7") != 1 {
			t.Errorf("no unbanip of 2001:db8::7 in %s", known.name)
		}
	}

	events := recorded()
	if len(events) != 2 {
		t.Fatalf("%d events, want 2: %+v", len(events), events)
	}
	if event := events[0]; event.Kind != utils.SecFail2banUnban || event.Target != "203.0.113.7" || event.Details["jail"] != "cosmos-auth" || event.Actor != "someone" {
		t.Errorf("event = %+v", event)
	}
	if event := events[1]; event.Target != "2001:db8::7" || event.Details["jail"] != "all" {
		t.Errorf("event = %+v", event)
	}
}

// What a request sends never reaches the command line unless it is an
// address, and the jail only selects one of the fork's.
func TestUnbanRouteRefusesWhatIsNotAnAddressOrAJail(t *testing.T) {
	useJailDir(t, true)
	useConfig(t, utils.Config{})
	client := useFake(t, true, realAnswers(t))
	recorded := captureSecurity(t)

	bodies := []string{
		``,
		`{`,
		`[]`,
		`"203.0.113.7"`,
		`{"ip":""}`,
		`{"ip":"--all"}`,
		`{"ip":"-h"}`,
		`{"ip":"203.0.113.7; reboot"}`,
		`{"ip":"203.0.113.7 203.0.113.8"}`,
		`{"ip":"203.0.113.0/24"}`,
		`{"ip":"example.com"}`,
		`{"ip":"fe80::1%eth0"}`,
		`{"ip":"$(reboot)"}`,
		`{"ip":["203.0.113.7"]}`,
		`{"ip":2130706433}`,
		`{"ip":"203.0.113.7","jail":"apache-auth"}`,
		`{"ip":"203.0.113.7","jail":"--all"}`,
		`{"ip":"203.0.113.7","jail":"sshd; reboot"}`,
		`{"ip":"203.0.113.7","jail":"cosmos-auth cosmos-scan"}`,
		`{"ip":"203.0.113.7","jail":"SSHD"}`,
		`{"ip":"203.0.113.7","jail":["sshd"]}`,
		`{"ip":"203.0.113.7","jail":"` + strings.Repeat("a", maxBody) + `"}`,
	}
	for _, body := range bodies {
		rec := call(UnbanRoute, apiRequest("POST", "/api/fail2ban/unban", "sudo", body))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %.60q: status %d, want 400", body, rec.Code)
		}
		// the answer does not repeat what was sent
		for _, sent := range []string{"reboot", "apache-auth", "example.com", "--all"} {
			if strings.Contains(body, sent) && strings.Contains(rec.Body.String(), sent) {
				t.Errorf("body %.60q: the answer repeats %q: %s", body, sent, rec.Body.String())
			}
		}
	}
	if calls := client.called(); len(calls) != 0 {
		t.Errorf("fail2ban-client was run: %v", calls)
	}
	if events := recorded(); len(events) != 0 {
		t.Errorf("events for refused requests: %+v", events)
	}
}

func TestUnbanRouteSaysWhenFail2banIsNotThere(t *testing.T) {
	useJailDir(t, true)
	useConfig(t, utils.Config{})
	body := `{"ip":"203.0.113.7","jail":"cosmos-auth"}`

	client := useFake(t, false, realAnswers(t))
	if rec := call(UnbanRoute, apiRequest("POST", "/api/fail2ban/unban", "sudo", body)); rec.Code != http.StatusConflict {
		t.Errorf("not installed: status %d, want 409", rec.Code)
	}
	if len(client.called()) != 0 {
		t.Errorf("the client was run although it is not installed: %v", client.called())
	}

	useFake(t, true, func(args []string) (string, error) { return "", errClient })
	if rec := call(UnbanRoute, apiRequest("POST", "/api/fail2ban/unban", "sudo", body)); rec.Code != http.StatusConflict {
		t.Errorf("not running: status %d, want 409", rec.Code)
	}

	// running, but the unban itself fails
	answers := realAnswers(t)
	useFake(t, true, func(args []string) (string, error) {
		if args[0] == "set" {
			return "", errClient
		}
		return answers(args)
	})
	if rec := call(UnbanRoute, apiRequest("POST", "/api/fail2ban/unban", "sudo", body)); rec.Code != http.StatusConflict {
		t.Errorf("unban failed: status %d, want 409", rec.Code)
	}
}

func TestSettingsRoute(t *testing.T) {
	folder := useJailDir(t, true)
	useConfig(t, utils.Config{})
	client := useFake(t, true, realAnswers(t))
	saved := captureSaved(t)
	recorded := captureSecurity(t)

	settings := defaultSettings()
	settings.Jails["sshd"] = Thresholds{MaxRetry: 4, FindTime: 900, BanTime: 7200}
	settings.IgnoreIP = []string{"192.168.0.0/16", "203.0.113.50"}
	rec := call(SettingsRoute, apiRequest("POST", "/api/fail2ban/settings", "sudo", settingsBody(t, settings)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	statusOf(t, rec)
	content, err := os.ReadFile(filepath.Join(folder, panelFile))
	if err != nil {
		t.Fatal(err)
	}
	assertOnlySafeLines(t, string(content))
	if !strings.Contains(string(content), "[sshd]\nmaxretry = 4\nfindtime = 900\nbantime = 7200\nignoreip = 127.0.0.0/8 ::1 192.168.0.0/16 203.0.113.50\n") {
		t.Errorf("the file does not hold the settings:\n%s", content)
	}
	if client.count("reload") != 1 || len(*saved) != 1 {
		t.Errorf("reloads %d, saves %d; want one each", client.count("reload"), len(*saved))
	}
	if events := recorded(); len(events) != 1 || events[0].Kind != utils.SecFail2banSettings || events[0].Actor != "someone" {
		t.Errorf("events = %+v", events)
	}

	// refused: the file stays as it is, the answer says what is wrong
	// without repeating the value
	hostile := []string{
		``,
		`{`,
		`{"jails":{},"ignoreIP":[]}`,
		`{"jails":null}`,
		strings.Replace(settingsBody(t, settings), `"192.168.0.0/16"`, `"192.168.0.0/16\nenabled = false"`, 1),
		strings.Replace(settingsBody(t, settings), `"192.168.0.0/16"`, `"%(known/x)s"`, 1),
		strings.Replace(settingsBody(t, settings), `"192.168.0.0/16"`, `"0.0.0.0/0"`, 1),
		strings.Replace(settingsBody(t, settings), `"maxRetry":4`, `"maxRetry":400`, 1),
		strings.Replace(settingsBody(t, settings), `"maxRetry":4`, `"maxRetry":"4"`, 1),
		strings.Replace(settingsBody(t, settings), `"maxRetry":4`, `"maxRetry":4.5`, 1),
		strings.Replace(settingsBody(t, settings), `"banTime":7200`, `"banTime":-1`, 1),
		strings.Replace(settingsBody(t, settings), `"sshd"`, `"sshd]\nenabled = false\n[x"`, 1),
		strings.Replace(settingsBody(t, settings), `"ignoreIP"`, `"enabled":false,"ignoreIP"`, 1), // a field that does not exist
		`{"jails":{},"ignoreIP":["` + strings.Repeat("1", maxBody) + `"]}`,
	}
	for _, body := range hostile {
		rec := call(SettingsRoute, apiRequest("POST", "/api/fail2ban/settings", "sudo", body))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %.80q: status %d, want 400", body, rec.Code)
		}
		for _, sent := range []string{"enabled", "known", "0.0.0.0/0"} {
			if strings.Contains(body, sent) && strings.Contains(rec.Body.String(), sent) {
				t.Errorf("body %.80q: the answer repeats %q: %s", body, sent, rec.Body.String())
			}
		}
	}
	if after, _ := os.ReadFile(filepath.Join(folder, panelFile)); string(after) != string(content) {
		t.Errorf("the file changed for a refused request:\n%s", after)
	}
	if client.count("reload") != 1 || len(*saved) != 1 {
		t.Errorf("reloads %d, saves %d after refused requests; want still one each", client.count("reload"), len(*saved))
	}
}

func TestSettingsRouteSaysWhenTheForksFilesAreNotInstalled(t *testing.T) {
	useJailDir(t, false)
	useConfig(t, utils.Config{})
	useFake(t, true, realAnswers(t))
	captureSaved(t)
	rec := call(SettingsRoute, apiRequest("POST", "/api/fail2ban/settings", "sudo", settingsBody(t, defaultSettings())))
	if rec.Code != http.StatusConflict {
		t.Errorf("status %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

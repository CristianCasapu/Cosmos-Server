package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/utils"
)

func notificationRouter(t *testing.T) *mux.Router {
	t.Helper()
	prev := utils.CONFIGFOLDER
	utils.CONFIGFOLDER = t.TempDir() + "/"
	if err := utils.InitMetricsDatabase(); err != nil {
		t.Fatal(err)
	}
	restore := utils.SetNotificationRecipients(func(group string) []string { return []string{"alice", "carol"} })
	t.Cleanup(func() {
		restore()
		utils.CloseMetricsDatabase()
		utils.CONFIGFOLDER = prev
	})
	router := mux.NewRouter()
	registerNotificationRoutes(router.PathPrefix("/cosmos").Subrouter())
	return router
}

var (
	adminReader = &utils.AuthContext{Nickname: "alice", Role: utils.ADMIN, Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN_READ}}
	plainUser   = &utils.AuthContext{Nickname: "carol", Role: utils.USER, Permissions: []utils.Permission{utils.PERM_LOGIN}}
)

func call(router *mux.Router, auth *utils.AuthContext, method string, path string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://cosmos.example"+path, strings.NewReader(body))
	if auth != nil {
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func items(t *testing.T, w *httptest.ResponseRecorder) []utils.NotificationView {
	t.Helper()
	var answer struct {
		Data struct {
			Items  []utils.NotificationView `json:"items"`
			Unread int                      `json:"unread"`
		} `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &answer) != nil {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	return answer.Data.Items
}

func TestNotificationRoutesFollowThePermissions(t *testing.T) {
	router := notificationRouter(t)
	// written for the admins while carol was one, and one of carol's own
	utils.WriteNotification(utils.Notification{Recipient: "admin", Level: "error", Fork: &utils.NotificationInfo{
		Kind: "upnp.forward.failed", What: utils.NText("notif.upnp.forward.failed.https.absent", "port", "2053"),
		Subject: &utils.NotificationSubject{Type: "port", Name: "HTTPS 2053/TCP"}}})
	utils.WriteNotification(utils.Notification{Recipient: "carol", Level: "info", Fork: &utils.NotificationInfo{
		Kind: "alert.triggered", What: utils.NText("notif.alert.triggered.what", "name", "mine")}})

	paths := []string{"/cosmos/api/notifications/list", "/cosmos/api/notifications/item?id=1", "/cosmos/api/notifications/context?id=1"}
	for _, path := range paths {
		if w := call(router, nil, "GET", path, ""); w.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a session: %d", path, w.Code)
		}
		if w := call(router, adminReader, "POST", path, "{}"); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/cosmos/api/notifications/mark", "/cosmos/api/notifications/delete"} {
		if w := call(router, nil, "POST", path, `{"all":true}`); w.Code != http.StatusUnauthorized {
			t.Errorf("POST %s without a session: %d", path, w.Code)
		}
		if w := call(router, adminReader, "GET", path+"?all=true", ""); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s changes something: %d", path, w.Code)
		}
	}

	mine := items(t, call(router, plainUser, "GET", "/cosmos/api/notifications/list", ""))
	if len(mine) != 1 || mine[0].Kind != "alert.triggered" {
		t.Fatalf("carol sees %+v", mine)
	}
	admins := items(t, call(router, adminReader, "GET", "/cosmos/api/notifications/list", ""))
	if len(admins) != 1 || admins[0].Kind != "upnp.forward.failed" {
		t.Fatalf("alice sees %+v", admins)
	}
	// carol's own copy of the admins' notification is not hers while she is not an admin
	for id := 1; id <= 4; id++ {
		w := call(router, plainUser, "GET", "/cosmos/api/notifications/item?id="+strconv.Itoa(id), "")
		if w.Code == http.StatusOK && strings.Contains(w.Body.String(), "upnp.forward.failed") {
			t.Fatalf("carol read an admins' notification: %s", w.Body.String())
		}
	}
	if w := call(router, plainUser, "GET", "/cosmos/api/notifications/item?id="+strconv.FormatInt(admins[0].ID, 10), ""); w.Code != http.StatusNotFound {
		t.Errorf("alice's notification by id for carol: %d", w.Code)
	}
	if w := call(router, plainUser, "GET", "/cosmos/api/notifications/context?id="+strconv.FormatInt(mine[0].ID, 10), ""); w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Errorf("context for a non-admin: %d", w.Code)
	}
	w := call(router, adminReader, "GET", "/cosmos/api/notifications/context?id="+strconv.FormatInt(admins[0].ID, 10), "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"log"`) {
		t.Errorf("context for the admin: %d %s", w.Code, w.Body.String())
	}
	if w := call(router, adminReader, "GET", "/cosmos/api/notifications/item?ref="+admins[0].Ref, ""); w.Code != http.StatusOK {
		t.Errorf("by ref: %d", w.Code)
	}

	// changes: own rows only, bounded bodies
	id := strconv.FormatInt(admins[0].ID, 10)
	if w := call(router, plainUser, "POST", "/cosmos/api/notifications/delete", `{"ids":[`+id+`]}`); !strings.Contains(w.Body.String(), `"deleted":0`) {
		t.Errorf("carol deleting alice's: %s", w.Body.String())
	}
	for _, bad := range []string{`{"ids":[1],"read":true,"admin":true}`, `{"ids":[-1],"read":true}`, `{"ids":[1]}`, `{}`, `not json`,
		`{"ids":[` + strings.Repeat("1,", 200) + `1],"read":true}`, `{"ids":[1],"read":true,"x":"` + strings.Repeat("a", 17<<10) + `"}`} {
		if w := call(router, adminReader, "POST", "/cosmos/api/notifications/mark", bad); w.Code != http.StatusBadRequest {
			t.Errorf("mark %.40q: %d", bad, w.Code)
		}
	}
	if w := call(router, adminReader, "POST", "/cosmos/api/notifications/mark", `{"ids":[`+id+`],"read":true}`); !strings.Contains(w.Body.String(), `"changed":1`) {
		t.Errorf("mark: %s", w.Body.String())
	}
	if w := call(router, adminReader, "POST", "/cosmos/api/notifications/delete", `{"all":true}`); w.Code != http.StatusBadRequest {
		t.Errorf("deleting unread ones in bulk: %d", w.Code)
	}
	if w := call(router, adminReader, "POST", "/cosmos/api/notifications/delete", `{"read":true}`); !strings.Contains(w.Body.String(), `"deleted":1`) {
		t.Errorf("delete read: %s", w.Body.String())
	}
	for _, bad := range []string{"?level=loud", "?limit=1000", "?q=" + strings.Repeat("a", 101), "?cursor=x", "?source=../../etc"} {
		if w := call(router, adminReader, "GET", "/cosmos/api/notifications/list"+bad, ""); w.Code != http.StatusBadRequest {
			t.Errorf("list %s: %d", bad, w.Code)
		}
	}
	if w := call(router, adminReader, "GET", "/cosmos/api/notifications/item?ref=../x", ""); w.Code != http.StatusBadRequest {
		t.Errorf("a bad ref: %d", w.Code)
	}
}

func TestTheLogLinesOfTheMoment(t *testing.T) {
	at := time.Date(2026, 11, 15, 8, 30, 0, 0, time.Local)
	stamp := func(d time.Duration) string { return at.Add(d).Format("2006/01/02 15:04:05") }
	token := "1234567:" + strings.Repeat("Zx9", 12)
	log := strings.Join([]string{
		stamp(-10*time.Minute) + " [ERROR] UPnP: too early",
		stamp(-time.Minute) + " [WARN]  UPnP: the router could not be set up; trying again by itself at 08:31",
		stamp(-time.Minute) + " [INFO]  Something else entirely",
		"  a continuation line without a time",
		stamp(0) + " [ERROR] \x1b[31mUPnP: could not forward port 2053 (HTTPS) : Post \"http://192.168.1.1:2828/ctl\": EOF token=" + token + "\x1b[0m",
		stamp(time.Minute) + " [INFO]  UPnP: " + strings.Repeat("x", 900),
		stamp(10*time.Minute) + " [INFO]  UPnP: too late",
	}, "\n")
	path := t.TempDir() + "/cosmos.plain.log"
	os.WriteFile(path, []byte(log), 0600)

	d := utils.NotificationDetails{NotificationView: utils.NotificationView{Source: "upnp", Date: at}, Seen: []time.Time{at}}
	out := logAround(d, path)
	if !out.Available || len(out.Lines) != 3 {
		t.Fatalf("%+v", out.Lines)
	}
	if strings.Contains(out.Lines[1], "\x1b") || strings.Contains(out.Lines[1], token) || !strings.Contains(out.Lines[1], "EOF token=[hidden]") {
		t.Errorf("colours and secrets: %q", out.Lines[1])
	}
	if len(out.Lines[2]) > contextLogLineLen+4 {
		t.Errorf("a long line is cut: %d", len(out.Lines[2]))
	}
	if missing := logAround(d, path+".none"); missing.Available || missing.Reason == nil {
		t.Errorf("a missing log says so: %+v", missing)
	}
	// a source without markers gets the warnings and errors
	d.Source = "server"
	if lines := logAround(d, path).Lines; len(lines) != 2 {
		t.Errorf("server: %q", lines)
	}
}

func TestNotificationRoutesAreRegistered(t *testing.T) {
	source, _ := os.ReadFile("httpServer.go")
	if !strings.Contains(string(source), "registerNotificationRoutes(srapiAdmin)") {
		t.Error("the notification routes are not registered")
	}
}

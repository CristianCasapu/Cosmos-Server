package main

// Fork addition: tests of the server's side of the security record
// (fork_security_hooks.go), and of the hooks in upstream's files: a merge
// that drops one fails here.

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/proxy"
	"github.com/azukaar/cosmos-server/src/security"
	"github.com/azukaar/cosmos-server/src/utils"
)

func captureSecurity(t *testing.T) func() []utils.SecurityEvent {
	t.Helper()
	var mu sync.Mutex
	events := []utils.SecurityEvent{}
	utils.SetSecurityRecorder(func(event utils.SecurityEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}, func() {})
	t.Cleanup(func() { utils.SetSecurityRecorder(nil, nil) })
	return func() []utils.SecurityEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]utils.SecurityEvent(nil), events...)
	}
}

// securityHooks are the hooks in files that are not the fork's own: where,
// what the line contains, and how many times at least. When upstream moves
// the code around a hook, re-apply the hook and adjust the entry; do not
// delete the entry.
var securityHooks = []struct {
	file  string
	text  string
	times int
}{
	{"index.go", "startSecurityRecord()", 1},
	{"httpServer.go", "registerSecurityRoutes(srapiAdmin)", 1},
	{"httpServer.go", "userRouter.Use(securityAudit)", 1},
	{"httpServer.go", "recordNotFound(r)", 2},
	{"httpServer.go", "utils.RecordSecurity(r, utils.SecTokenInvalid", 1},
	{"httpServer.go", "utils.RecordSecurity(r, utils.SecTokenExpired", 1},
	{"httpServer.go", "utils.RecordSecurity(r, utils.SecTokenIPDenied", 1},
	{"httpServer.go", "utils.RecordSecurity(r, utils.SecShieldRateLimited", 1},
	{"httpServer.go", "utils.RecordSecuritySessionError(r, err)", 1},
	{"system.go", "utils.RecordSecurityNow(req, utils.SecServerUpdate", 1},
	{"system.go", "utils.RecordSecurityNow(req, utils.SecServerRestart", 1},
	{"terminal.go", "utils.RecordSecurity(r, utils.SecHostTerminal", 1},

	{"utils/loggedIn.go", "RecordSecurity(req, SecAccessDenied", 2},
	{"utils/middleware.go", "RecordSecurity(r, SecShieldGeo", 2},
	{"utils/middleware.go", "RecordSecurity(r, SecShieldHostname", 2},
	{"utils/middleware.go", "RecordSecurity(r, SecAccessIPDenied", 1},

	{"user/login.go", "recordLoginFailure(req, ", 4},
	{"user/login.go", "recordLogin(req, user)", 1},
	{"user/sudo.go", "utils.RecordSecurity(req, utils.SecSudoFailed", 1},
	{"user/sudo.go", "utils.RecordSecurity(req, utils.SecSudoSuccess", 1},
	{"user/2fa_check.go", "utils.RecordSecurity(req, utils.SecMFAFailed", 1},
	{"user/2fa_check.go", "recordMFAFailure(req, nickname)", 1},
	{"user/2fa_check.go", "utils.RecordSecurity(req, utils.SecMFASuccess", 1},
	{"user/2fa_new.go", "utils.RecordSecurity(req, utils.SecMFASetup", 1},
	{"user/2fa_reset.go", "utils.AuditSecurity(req, utils.SecUserMFAReset", 1},
	{"user/logout.go", "recordLogout(req)", 1},
	{"user/register.go", "utils.RecordSecurity(req, utils.SecRegisterFailed", 2},
	{"user/register.go", "recordRegistration(req, ", 1},
	{"user/password_reset.go", "recordPasswordResetRequest(req, ", 1},
	{"user/resend.go", "auditLink(req, ", 1},
	{"user/create.go", "utils.AuditSecurity(req, utils.SecUserCreated", 1},
	{"user/create.go", "utils.RecordSecurity(req, utils.SecAccessGrantDenied", 1},
	{"user/delete.go", "auditUserDeletion(req, ", 1},
	{"user/edit.go", "auditUserEdit(req, ", 1},
	{"user/edit.go", "utils.RecordSecurity(req, utils.SecAccessGrantDenied", 1},

	{"configapi/set.go", "auditSettings(req, ", 1},
	{"configapi/set.go", "utils.RecordSecurity(req, utils.SecAccessGrantDenied", 1},
	{"configapi/patch.go", "auditRoutePatch(req, ", 1},
	{"configapi/routes.go", "utils.AuditSecurity(req, utils.SecRouteCreated", 1},
	{"configapi/routes.go", "utils.AuditSecurity(req, utils.SecRouteUpdated", 1},
	{"configapi/routes.go", "utils.AuditSecurity(req, utils.SecRouteDeleted", 1},
	{"configapi/api_tokens.go", "utils.AuditSecurity(req, utils.SecTokenCreated", 1},
	{"configapi/api_tokens.go", "utils.AuditSecurity(req, utils.SecTokenUpdated", 1},
	{"configapi/api_tokens.go", "utils.AuditSecurity(req, utils.SecTokenDeleted", 1},
	{"configapi/api_tokens.go", "utils.RecordSecurity(req, utils.SecAccessGrantDenied", 2},
	{"configapi/restart.go", "utils.RecordSecurityNow(req, utils.SecServerRestart", 1},

	{"docker/api_managecont.go", "auditContainerAction(req, ", 1},
	{"docker/api_updateContainer.go", "utils.AuditSecurity(req, utils.SecContainerEdited", 1},
	{"docker/api_blueprint.go", "utils.AuditSecurity(req, utils.SecContainerCreated", 1},
	{"docker/api_terminal.go", "recordTerminal(r, ", 1},

	{"proxy/shield.go", "utils.RecordSecurity(r, utils.SecShieldBlocked", 1},
	{"proxy/shield_bans.go", "recordShieldBan(ban)", 1},
	{"proxy/api_shield.go", "utils.AuditSecurity(req, utils.SecShieldUnban", 1},
	{"proxy/botblock.go", "recordBotBlock(r)", 2},
	{"proxy/routerGen.go", "utils.RecordSecuritySessionError(r, err)", 1},
	{"proxy/fork_local.go", "utils.RecordSecurity(req, utils.SecAccessLocalDenied", 1},
	{"proxy/fork_cloudflare.go", "utils.RecordSecurity(req, utils.SecAccessOriginDenied", 1},
	{"snapshots/api.go", "utils.AuditSecurity(req, utils.SecSnapshotRestore", 1},
}

func TestSecurityHooksAreInPlace(t *testing.T) {
	sources := map[string]string{}
	for _, hook := range securityHooks {
		source, read := sources[hook.file]
		if !read {
			content, err := os.ReadFile(hook.file)
			if err != nil {
				t.Errorf("%s: %v", hook.file, err)
				continue
			}
			source = string(content)
			sources[hook.file] = source
		}
		if found := strings.Count(source, hook.text); found < hook.times {
			t.Errorf("%s: %q is there %d time(s), want %d: a hook of the security record is gone", hook.file, hook.text, found, hook.times)
		}
	}
}

// Every kind has a place that records it: a kind that nothing records any
// more is a hook that went missing (or a kind to remove, on purpose).
func TestSecurityEveryKindIsRecordedSomewhere(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "utils/fork_security.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	ast.Inspect(file, func(node ast.Node) bool {
		if spec, ok := node.(*ast.ValueSpec); ok {
			for _, name := range spec.Names {
				if strings.HasPrefix(name.Name, "Sec") && len(spec.Values) > 0 {
					if _, isString := spec.Values[0].(*ast.BasicLit); isString {
						names = append(names, name.Name)
					}
				}
			}
		}
		return true
	})
	if len(names) < 40 {
		t.Fatalf("only %d kinds found: the test no longer reads utils/fork_security.go right", len(names))
	}

	uses := map[string]int{}
	err = filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || path == filepath.Join("security", "kinds.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, name := range names {
			pattern := regexp.MustCompile(`\b` + name + `\b`)
			uses[name] += len(pattern.FindAll(content, -1))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		// one use is the declaration itself
		if uses[name] < 2 {
			t.Errorf("utils.%s is declared and in the catalogue, but nothing records it", name)
		}
	}
}

func TestSecurityNotFoundRecordsProbesAndUnknownHostnames(t *testing.T) {
	plainHTTPTestConfig(t, nil)
	recorded := captureSecurity(t)

	request := func(host string, path string) {
		req := httptest.NewRequest("GET", "https://"+host+path, nil)
		req.RemoteAddr = "203.0.113.7:40000"
		// not believed: the peer is no trusted proxy
		req.Header.Set("X-Forwarded-For", "198.51.100.99")
		recordNotFound(req)
	}

	request("172.16.0.14:2053", "/.env")
	request("172.16.0.14:2053", "/wp-login.php?redirect=1&token=cosmos_secret")
	request("scanner.example", "/phpmyadmin/")         // a probe first, whatever the hostname
	request("scanner.example", "/favicon.ico")         // a hostname that is not served
	request("172.16.0.14:2053", "/favicon.ico")        // an ordinary 404
	request("media.home.lan", "/apple-touch-icon.png") // an ordinary 404 on a route's hostname
	request("172.16.0.14:2053", "/.well-known/security.txt")

	events := recorded()
	got := []string{}
	for _, event := range events {
		got = append(got, event.Kind+" "+event.Host+" "+event.Path+" "+event.Details["probe"])
		if event.IP != "203.0.113.7" {
			t.Errorf("event = %+v, want the peer's address", event)
		}
	}
	want := []string{
		utils.SecScanProbe + " 172.16.0.14:2053 /.env dotfile",
		utils.SecScanProbe + " 172.16.0.14:2053 /wp-login.php wordpress",
		utils.SecScanProbe + " scanner.example /phpmyadmin/ phpmyadmin",
		utils.SecShieldHostname + " scanner.example /favicon.ico ",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("recorded:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The plain-HTTP port answers what it does not redirect with a 404: a probe
// there is recorded, and still answered 404.
func TestSecurityPlainHTTPPortRecordsProbes(t *testing.T) {
	plainHTTPTestConfig(t, nil)
	recorded := captureSecurity(t)
	handler := plainHTTPHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the router was reached")
	}))

	req := httptest.NewRequest("GET", "http://203.0.113.200:2052/.git/config", nil)
	req.RemoteAddr = "203.0.113.7:40000"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("answer %d, want the 404 as before", w.Code)
	}
	events := recorded()
	if len(events) != 1 || events[0].Kind != utils.SecScanProbe || events[0].IP != "203.0.113.7" {
		t.Fatalf("events = %+v", events)
	}
}

// The outcome of an audited request is read from the SmartShield's response
// wrapper, the writer the handlers of the API are given.
func TestSecurityAuditReadsTheStatusFromTheShieldWrapper(t *testing.T) {
	recorded := captureSecurity(t)

	serve := func(method string, answer int) {
		handler := securityAudit(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			utils.AuditSecurity(req, utils.SecContainerAction, "jellyfin", "action", "stop")
			if answer != 0 {
				w.WriteHeader(answer)
			}
			w.Write([]byte("{}"))
		}))
		req := httptest.NewRequest(method, "http://cosmos.example/cosmos/api/servapps/jellyfin/manage/stop", nil)
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, &utils.AuthContext{Nickname: "alice"}))
		handler.ServeHTTP(&proxy.SmartResponseWriterWrapper{ResponseWriter: httptest.NewRecorder(), Method: method}, req)
	}
	serve("GET", 0)
	serve("GET", http.StatusInternalServerError)
	serve("GET", http.StatusUnauthorized)

	events := recorded()
	if len(events) != 3 || events[0].Outcome != "ok" || events[1].Outcome != "failed" || events[2].Outcome != "denied" {
		t.Fatalf("outcomes = %+v", events)
	}
}

// The read routes: registered, refusing whoever is not logged in, and
// answering an administrator.
func TestSecurityRoutesAreRegisteredAndProtected(t *testing.T) {
	router := mux.NewRouter()
	api := router.PathPrefix("/cosmos").Subrouter()
	registerSecurityRoutes(api)

	for _, path := range []string{"/cosmos/api/security/events", "/cosmos/api/security/counts", "/cosmos/api/security/top-ips", "/cosmos/api/security/kinds"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "http://cosmos.example"+path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a session: %d, want 401", path, w.Code)
		}
	}

	admin := &utils.AuthContext{Nickname: "alice", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_ADMIN_READ}}
	req := httptest.NewRequest("GET", "http://cosmos.example/cosmos/api/security/kinds", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, admin)))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), utils.SecLoginFailed) {
		t.Fatalf("GET kinds as an admin reader: %d %s", w.Code, w.Body.String())
	}
	if len(security.Kinds()) < 40 {
		t.Fatalf("the catalogue has %d kinds", len(security.Kinds()))
	}

	// in the server they sit on the admin router, behind its restrictions
	source, err := os.ReadFile("httpServer.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "registerSecurityRoutes(srapiAdmin)") {
		t.Fatal("the routes are not registered on the admin router")
	}
}

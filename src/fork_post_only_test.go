package main

// Fork addition: tests of fork_post_only.go.

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
	"testing"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/utils"
)

// registeredHandlers reads httpServer.go: path template (with /cosmos) ->
// handler expression, for the routes registered on the three API routers.
func registeredHandlers(t *testing.T) map[string]string {
	t.Helper()
	source, err := os.ReadFile("httpServer.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(srapi|srapiStrict|srapiAdmin)\.HandleFunc\("([^"]+)",\s*([A-Za-z0-9_.]+)\)`)
	out := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(string(source), -1) {
		out["/cosmos"+m[2]] = m[3]
	}
	return out
}

// TestCommandRoutesStillExist fails when upstream renames or removes a listed
// route: the list must follow, or the command would take GET again.
func TestCommandRoutesStillExist(t *testing.T) {
	handlers := registeredHandlers(t)
	for _, command := range commandRoutes {
		if _, ok := handlers[command.template]; !ok {
			t.Errorf("%s is not registered in httpServer.go any more: update commandRoutes", command.template)
		}
	}
}

// TestCommandHandlersStillExpectTheirMethod fails when upstream changes one of
// the handlers so that it no longer acts on the method the wrapper hands it
// (for example when upstream itself moves it to POST): the entry must then
// say so, or the command stops working.
func TestCommandHandlersStillExpectTheirMethod(t *testing.T) {
	handlers := registeredHandlers(t)
	packages := map[string]map[string]*ast.FuncDecl{}
	sources := map[string][]byte{}
	fset := token.NewFileSet()
	find := func(expr string) (*ast.FuncDecl, []byte) {
		dir, name := ".", expr
		if dot := strings.Index(expr, "."); dot >= 0 {
			dir, name = expr[:dot], expr[dot+1:]
		}
		if packages[dir] == nil {
			packages[dir] = map[string]*ast.FuncDecl{}
			files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
			for _, file := range files {
				if strings.HasSuffix(file, "_test.go") {
					continue
				}
				data, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				parsed, err := parser.ParseFile(fset, file, data, 0)
				if err != nil {
					t.Fatal(err)
				}
				for _, decl := range parsed.Decls {
					if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
						packages[dir][fn.Name.Name] = fn
						sources[dir+"."+fn.Name.Name] = data
					}
				}
			}
		}
		return packages[dir][name], sources[dir+"."+name]
	}
	for _, command := range commandRoutes {
		expr := handlers[command.template]
		if expr == "" {
			continue // reported by TestCommandRoutesStillExist
		}
		fn, data := find(expr)
		if fn == nil {
			t.Errorf("%s: handler %s not found", command.template, expr)
			continue
		}
		body := string(data[fset.Position(fn.Body.Pos()).Offset:fset.Position(fn.Body.End()).Offset])
		for accepted, presented := range command.methods {
			if presented == "GET" && !strings.Contains(body, `"GET"`) && !strings.Contains(body, "resetMethodAllowed(") {
				t.Errorf("%s: %s is handed to %s as GET, but the handler no longer looks for GET", command.template, accepted, expr)
			}
		}
	}
}

// commandTestRouter registers every listed route on a /cosmos subrouter with
// a handler that tells which method it was given.
func commandTestRouter(t *testing.T) (*mux.Router, map[string]bool) {
	router := mux.NewRouter()
	api := router.PathPrefix("/cosmos").Subrouter()
	for _, command := range commandRoutes {
		api.HandleFunc(strings.TrimPrefix(command.template, "/cosmos"), func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Handled-As", r.Method)
			w.Header().Set("X-Vars", mux.Vars(r)["containerId"]+"|"+mux.Vars(r)["status"])
		})
	}
	api.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Handled-As", r.Method)
	})
	return router, commandsOnlyByPost(api)
}

func concrete(template string) string {
	return regexp.MustCompile(`\{([a-zA-Z]+)\}`).ReplaceAllStringFunc(template, func(v string) string {
		switch v {
		case "{status}":
			return "true"
		case "{action}":
			return "stop"
		}
		return "app"
	})
}

func TestCommandsRefuseGetAndTakePost(t *testing.T) {
	router, found := commandTestRouter(t)
	for _, command := range commandRoutes {
		if !found[command.template] {
			t.Errorf("%s was not wrapped", command.template)
		}
		path := concrete(command.template)
		for _, method := range []string{"GET", "HEAD", "PATCH"} {
			if _, accepted := command.methods[method]; accepted {
				continue
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
			if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("X-Handled-As") != "" {
				t.Errorf("%s %s: got %d, handled as %q; want 405 and the handler not called", method, path, rec.Code, rec.Header().Get("X-Handled-As"))
			}
			if rec.Header().Get("Allow") == "" {
				t.Errorf("%s %s: no Allow header", method, path)
			}
		}
		for accepted, presented := range command.methods {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(accepted, path, nil))
			if got := rec.Header().Get("X-Handled-As"); got != presented {
				t.Errorf("%s %s: handler given %q, want %q", accepted, path, got, presented)
			}
		}
	}
	// the values of the path reach the handler
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("POST", "/cosmos/api/servapps/jellyfin/lazy/true", nil))
	if got := rec.Header().Get("X-Vars"); got != "jellyfin|true" {
		t.Errorf("path values: %q", got)
	}
	// a route that is not listed keeps its methods
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/cosmos/api/status", nil))
	if rec.Header().Get("X-Handled-As") != "GET" {
		t.Errorf("an unlisted route was changed: %d", rec.Code)
	}
}

// The two-factor set-up: GET no longer issues a secret, PUT does; the code
// check (POST) and the reset (DELETE) are unchanged.
func TestNewTwoFactorSecretTakesPut(t *testing.T) {
	router, _ := commandTestRouter(t)
	for method, want := range map[string]string{"PUT": "GET", "POST": "POST", "DELETE": "DELETE", "GET": ""} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, "/cosmos/api/mfa", nil))
		if got := rec.Header().Get("X-Handled-As"); got != want {
			t.Errorf("%s /cosmos/api/mfa: handled as %q, want %q (status %d)", method, got, want, rec.Code)
		}
	}
}

// The three container switches leave an audit entry; a caller who may not
// make them leaves none here (the permission check records the refusal).
func TestContainerSwitchesAreAudited(t *testing.T) {
	router, _ := commandTestRouter(t)
	events := captureSecurity(t)
	admin := &utils.AuthContext{Nickname: "admin", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_RESOURCES}, IsSudoed: true}
	plain := &utils.AuthContext{Nickname: "someone", Permissions: []utils.Permission{utils.PERM_LOGIN}}
	cases := []struct {
		path   string
		auth   *utils.AuthContext
		action string
	}{
		{"/cosmos/api/servapps/jellyfin/secure/true", admin, "isolate_on"},
		{"/cosmos/api/servapps/jellyfin/auto-update/false", admin, "auto_update_off"},
		{"/cosmos/api/servapps/jellyfin/lazy/true", admin, "lazy_on"},
		{"/cosmos/api/servapps/jellyfin/lazy/true", plain, ""},
	}
	for _, c := range cases {
		before := len(events())
		req := httptest.NewRequest("POST", c.path, nil)
		req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, c.auth))
		router.ServeHTTP(httptest.NewRecorder(), req)
		got := events()[before:]
		if c.action == "" {
			if len(got) != 0 {
				t.Errorf("%s by a caller without the permission: %d entries", c.path, len(got))
			}
			continue
		}
		if len(got) != 1 || got[0].Kind != utils.SecContainerAction || got[0].Target != "jellyfin" || got[0].Details["action"] != c.action {
			t.Errorf("%s: entries %+v, want one container.action %s", c.path, got, c.action)
		}
	}
}

// The hook is in InitServer.
func TestCommandsOnlyByPostIsHooked(t *testing.T) {
	source, err := os.ReadFile("httpServer.go")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^\s*commandsOnlyByPost\(srapi, srapiStrict, srapiAdmin\)`).Match(source) {
		t.Fatal("the hook of fork_post_only.go is missing from InitServer")
	}
}

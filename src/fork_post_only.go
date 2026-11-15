package main

// Fork addition: the API's commands are not reachable with GET.
//
// A few of upstream's routes act when they are read: restart the server or the
// host, stop, remove or recreate a container, download an image, switch a
// container's isolation, automatic updates or start-on-demand, reset the
// monitoring data, mark notifications read, run a parity job, log out. A GET is
// what a browser sends for a link, a bookmark, a prefetch or an image, and what
// tools repeat without asking; the same-origin check (fork_same_origin.go)
// stops such requests from other sites in current browsers, but not in a
// browser that sends neither Fetch Metadata nor Origin. So these routes take
// POST (the new-secret step of the two-factor set-up takes PUT, POST being the
// code check) and answer 405 to anything else.
//
// Upstream's handlers are left as they are: the accepted method is handed to
// them as the method they were written for. The wrapper sits innermost, inside
// every check of the routers (same origin, token, permissions, audit), which
// therefore see the real method: the command leaves an "api.change" entry in
// the security record when its handler records nothing itself, and the three
// container switches announce a "container.action" entry of their own.

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/utils"
)

// commandRoute is a route whose methods are fixed here: for each accepted
// method, the method upstream's handler is given.
type commandRoute struct {
	template string
	methods  map[string]string
	// announce, when set, writes the audit entry of the command.
	announce func(req *http.Request)
}

var postAsGet = map[string]string{"POST": "GET"}
var postAsIs = map[string]string{"POST": "POST"}

// commandRoutes is every route of the API (outside paid code) that changes
// something on GET, or takes any method for a change.
var commandRoutes = []commandRoute{
	{template: "/cosmos/api/logout", methods: postAsGet},
	{template: "/cosmos/api/restart-server", methods: postAsGet},
	{template: "/cosmos/api/restart", methods: postAsGet},
	{template: "/cosmos/api/images/pull", methods: postAsGet},
	{template: "/cosmos/api/images/pull-if-missing", methods: postAsGet},
	{template: "/cosmos/api/servapps/{containerId}/manage/{action}", methods: postAsGet},
	{template: "/cosmos/api/servapps/{containerId}/secure/{status}", methods: postAsGet, announce: announceSwitch("isolate")},
	{template: "/cosmos/api/servapps/{containerId}/auto-update/{status}", methods: postAsGet, announce: announceSwitch("auto_update")},
	{template: "/cosmos/api/servapps/{containerId}/lazy/{status}", methods: postAsGet, announce: announceSwitch("lazy")},
	{template: "/cosmos/api/reset-metrics", methods: postAsGet},
	{template: "/cosmos/api/notifications/read", methods: postAsGet},
	{template: "/cosmos/api/snapraid/{name}/{action}", methods: postAsGet},
	// GET issues a new two-factor secret; it becomes PUT
	{template: "/cosmos/api/mfa", methods: map[string]string{"PUT": "GET", "POST": "POST", "DELETE": "DELETE"}},
	// these took any method; the panel sends POST
	{template: "/cosmos/rclone/config/create", methods: postAsIs},
	{template: "/cosmos/rclone/config/update", methods: postAsIs},
	{template: "/cosmos/rclone/config/delete", methods: postAsIs},
	{template: "/cosmos/rclone/config/save", methods: postAsIs},
}

// announceSwitch writes the audit entry of a container switch
// ("<name>_on" / "<name>_off"), for a caller who may make it: a refused one
// is recorded by the permission check.
func announceSwitch(name string) func(req *http.Request) {
	return func(req *http.Request) {
		if !utils.HasPermission(req, utils.PERM_RESOURCES) {
			return
		}
		vars := mux.Vars(req)
		state := "off"
		if vars["status"] == "true" {
			state = "on"
		}
		utils.AuditSecurity(req, utils.SecContainerAction, utils.SanitizeSafe(vars["containerId"]), "action", name+"_"+state)
	}
}

// commandsOnlyByPost is the hook of InitServer: it fixes the methods of the
// listed routes on the given routers, once every route is registered. It
// returns the templates it found, for the test that keeps the list honest.
func commandsOnlyByPost(routers ...*mux.Router) map[string]bool {
	byTemplate := map[string]commandRoute{}
	for _, command := range commandRoutes {
		byTemplate[command.template] = command
	}
	found := map[string]bool{}
	for _, router := range routers {
		router.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
			template, err := route.GetPathTemplate()
			if err != nil {
				return nil
			}
			command, listed := byTemplate[template]
			if !listed || route.GetHandler() == nil || found[template] {
				return nil
			}
			found[template] = true
			route.Handler(onlyMethods(command, route.GetHandler()))
			return nil
		})
	}
	for _, command := range commandRoutes {
		if !found[command.template] {
			utils.Warn("Fork: the route " + command.template + " was not found; its methods are not fixed")
		}
	}
	return found
}

func onlyMethods(command commandRoute, next http.Handler) http.Handler {
	allowed := make([]string, 0, len(command.methods))
	for method := range command.methods {
		allowed = append(allowed, method)
	}
	sort.Strings(allowed)
	allow := strings.Join(allowed, ", ")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented, ok := command.methods[r.Method]
		if !ok {
			w.Header().Set("Allow", allow)
			utils.HTTPError(w, "This command is sent with "+allow+" only.", http.StatusMethodNotAllowed, "HTTP001")
			return
		}
		if command.announce != nil {
			command.announce(r)
		}
		if presented != r.Method {
			r = r.WithContext(r.Context())
			r.Method = presented
		}
		next.ServeHTTP(w, r)
	})
}

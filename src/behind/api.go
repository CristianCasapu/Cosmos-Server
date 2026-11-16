package behind

// The routes of the URL check (administrators; commands are POST):
//
//   POST /cosmos/api/url-check        {"route": "<name>"}                  check one URL now
//   POST /cosmos/api/url-checks       {"routes": ["<name>", ...]}          the last checks, and up to 6 stale ones checked
//   POST /cosmos/api/url-check/apply  {"route": "<name>", "appOwnHost": b} or {"route": "<name>", "noVisitorAddress": b}
//                                     change that one setting of that one URL, then check it
//   GET  /cosmos/api/known-apps                                            what Cosmos knows of apps behind a proxy (resources-read)

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	bodyMax          = 4 << 10
	checksPerMin     = 30 // per user
	serverChecksMin  = 60 // for the whole server
	listMax          = 64
	staleChecksAtMax = 6
)

var routeNamePattern = regexp.MustCompile(`^[^\x00-\x1f\x7f]{1,128}$`)

var (
	limitMu sync.Mutex
	limits  = map[string][]time.Time{}
)

// allowed bounds the checks per user and for the server, n at a time.
func allowed(user string, n int, now time.Time) bool {
	limitMu.Lock()
	defer limitMu.Unlock()
	if len(limits) > 256 {
		limits = map[string][]time.Time{}
	}
	take := func(key string, limit int) bool {
		recent := []time.Time{}
		for _, at := range limits[key] {
			if now.Sub(at) < time.Minute {
				recent = append(recent, at)
			}
		}
		limits[key] = recent
		return len(recent)+n <= limit
	}
	if !take("user:"+user, checksPerMin) || !take("\x00server", serverChecksMin) {
		return false
	}
	for i := 0; i < n; i++ {
		limits["user:"+user] = append(limits["user:"+user], now)
		limits["\x00server"] = append(limits["\x00server"], now)
	}
	return true
}

func userOf(req *http.Request) string {
	ctx := utils.GetAuthContext(req)
	if ctx.APIToken != nil {
		return "token:" + ctx.APIToken.Name
	}
	return ctx.Nickname
}

func answer(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": data})
}

// containerIDOf is the id of the container a URL leads to now ("" when none), for the kept results.
var containerIDOf = func(ctx context.Context, route utils.ProxyRouteConfig) string {
	name, _, _, _ := containerOf(route)
	if name == "" {
		return ""
	}
	return docker.URLCheckFactsOf(ctx, name, nil).ID
}

func routeOf(name string) (utils.ProxyRouteConfig, bool) {
	for _, r := range utils.GetMainConfig().HTTPConfig.ProxyConfig.Routes {
		if r.Name == name {
			return r, true
		}
	}
	return utils.ProxyRouteConfig{}, false
}

func checkNow(req *http.Request, name string) Result {
	ctx, cancel := context.WithTimeout(req.Context(), 15*time.Second)
	defer cancel()
	r := Check(ctx, name)
	remember(r)
	return r
}

// CheckRoute answers POST /cosmos/api/url-check.
func CheckRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}
	if req.Method != http.MethodPost {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	var body struct {
		Route string `json:"route"`
	}
	if err := json.NewDecoder(io.LimitReader(req.Body, bodyMax)).Decode(&body); err != nil || !routeNamePattern.MatchString(body.Route) {
		utils.HTTPError(w, "Name the URL to check", http.StatusBadRequest, "URC001")
		return
	}
	if _, ok := routeOf(body.Route); !ok {
		utils.HTTPError(w, "There is no such URL", http.StatusNotFound, "URC002")
		return
	}
	if !allowed(userOf(req), 1, time.Now()) {
		utils.HTTPError(w, "Too many URL checks: wait a minute", http.StatusTooManyRequests, "URC003")
		return
	}
	answer(w, checkNow(req, body.Route))
}

// ChecksRoute answers POST /cosmos/api/url-checks: what is known of the URLs
// named, the stale ones among them checked (a few at a time, bounded).
func ChecksRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}
	if req.Method != http.MethodPost {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	var body struct {
		Routes []string `json:"routes"`
	}
	if err := json.NewDecoder(io.LimitReader(req.Body, 16<<10)).Decode(&body); err != nil || len(body.Routes) > listMax {
		utils.HTTPError(w, "Name at most 64 URLs", http.StatusBadRequest, "URC001")
		return
	}
	out := map[string]Result{}
	stale := []string{}
	for _, name := range body.Routes {
		if !routeNamePattern.MatchString(name) {
			continue
		}
		route, ok := routeOf(name)
		if !ok {
			continue
		}
		if r, ok := remembered(route, containerIDOf(req.Context(), route)); ok {
			out[name] = r
		} else if route.Mode == "SERVAPP" && !route.Disabled && len(stale) < staleChecksAtMax {
			stale = append(stale, name)
		}
	}
	if len(stale) > 0 && allowed(userOf(req), len(stale), time.Now()) {
		for _, name := range stale {
			out[name] = checkNow(req, name)
		}
	}
	answer(w, out)
}

// updateRoute changes one URL in the configuration file (the copy in memory
// carries values from the environment).
var updateRoute = func(name string, change func(*utils.ProxyRouteConfig) error) error {
	utils.ConfigLock.Lock()
	defer utils.ConfigLock.Unlock()
	config := utils.ReadConfigFromFile()
	for i := range config.HTTPConfig.ProxyConfig.Routes {
		if config.HTTPConfig.ProxyConfig.Routes[i].Name == name {
			if err := change(&config.HTTPConfig.ProxyConfig.Routes[i]); err != nil {
				return err
			}
			utils.SetBaseMainConfig(config)
			return nil
		}
	}
	return errNoRoute
}

type refusal string

func (r refusal) Error() string { return string(r) }

const (
	errNoRoute  = refusal("there is no such URL")
	errManaged  = refusal("this URL is managed elsewhere and can only be changed from there")
	errNotProxy = refusal("this URL does not lead to an app")
	errTunnel   = refusal("this URL goes through a tunnel: the address it tells the app is the tunnel's, and the visitor's address is what the other node's shield works with, so these settings are never used for it")
)

// reloadRoutes reloads the URLs once for changes made close together (the
// panel applies a repair to each of an app's URLs in a row): restarts of the
// HTTP server that overlap can leave its HTTPS listener refusing every
// connection until Cosmos is started again (upstream's RestartHTTPServer
// shuts the server down from a goroutine while the next one starts).
var reloadRoutes = func() {
	reloadMu.Lock()
	defer reloadMu.Unlock()
	if reloadTimer != nil {
		reloadTimer.Stop()
	}
	reloadTimer = time.AfterFunc(reloadAfter, utils.RestartHTTPServer)
}

var (
	reloadMu    sync.Mutex
	reloadTimer *time.Timer
	reloadAfter = 3 * time.Second
)

// ApplyRoute answers POST /cosmos/api/url-check/apply: the one setting of
// the one URL named, nothing else, then the URL is checked again.
func ApplyRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}
	if req.Method != http.MethodPost {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	var body struct {
		Route      string `json:"route"`
		AppOwnHost       *bool  `json:"appOwnHost"`
		NoVisitorAddress *bool  `json:"noVisitorAddress"`
	}
	if err := json.NewDecoder(io.LimitReader(req.Body, bodyMax)).Decode(&body); err != nil || !routeNamePattern.MatchString(body.Route) || (body.AppOwnHost == nil) == (body.NoVisitorAddress == nil) {
		utils.HTTPError(w, "Name the URL and the setting", http.StatusBadRequest, "URC001")
		return
	}
	err := updateRoute(body.Route, func(r *utils.ProxyRouteConfig) error {
		if r.IsManaged() {
			return errManaged
		}
		if r.Mode != "SERVAPP" && r.Mode != "PROXY" {
			return errNotProxy
		}
		if r.Tunnel != "" {
			return errTunnel
		}
		if body.AppOwnHost != nil {
			r.ForkAppOwnHost = *body.AppOwnHost
		} else {
			r.ForkNoVisitorAddress = *body.NoVisitorAddress
		}
		return nil
	})
	if err != nil {
		status := http.StatusBadRequest
		if err == errNoRoute {
			status = http.StatusNotFound
		}
		utils.HTTPError(w, "The URL was not changed: "+err.Error(), status, "URC004")
		return
	}
	setting, value := "appOwnHost", body.AppOwnHost
	if value == nil {
		setting, value = "noVisitorAddress", body.NoVisitorAddress
	}
	on := "off"
	if *value {
		on = "on"
	}
	utils.AuditSecurity(req, utils.SecRouteUpdated, body.Route, setting, on)
	utils.Log("Route updated: " + body.Route + " (" + setting + " " + on + ")")
	utils.TriggerEvent("cosmos.routes", "Route updated: "+body.Route, "success", "", map[string]interface{}{}) // as a save of the URL form
	// the check reads the saved setting (it composes the request itself); the
	// routes are reloaded after the answer, since reloading ends the connections
	// saved; the check waits its turn only a few seconds (another check may be asking an app): then the panel asks again
	result := Result{Route: body.Route, Verdict: "notChecked", Reason: "tooMany", Attempts: []Attempt{}, Repairs: []Repair{}}
	if allowed(userOf(req), 1, time.Now()) {
		ctx, cancel := context.WithTimeout(req.Context(), 12*time.Second)
		result = Check(ctx, body.Route)
		cancel()
		if result.Reason != "busy" {
			remember(result)
		}
	}
	answer(w, result)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	reloadRoutes()
}

// KnownAppsRoute answers GET /cosmos/api/known-apps.
func KnownAppsRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_RESOURCES_READ) != nil {
		return
	}
	if req.Method != http.MethodGet {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	answer(w, Apps())
}

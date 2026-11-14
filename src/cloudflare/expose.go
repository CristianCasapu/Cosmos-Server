package cloudflare

// Fork addition: "expose to the internet", one action for everything an
// application offers outside the local network.
//
//   - Its web interface goes out through Cosmos' HTTPS port under a name of a
//     configured domain: a copy of the app's own URL under that name (what
//     the Domains dialog did), served through Cloudflare's proxy or directly
//     (DNS only), with Cosmos' sign-in in front or not.
//   - A port that is not a web page (a game server, a torrent listener, MQTT)
//     cannot pass Cloudflare's proxy: the router forwards it (src/upnp) and it
//     may get a DNS-only name of its own, which Cosmos publishes at the
//     public address while the choice exists (UPnPAppPort.Hostname). A name
//     carrying such a port can never be shielded, and a shielded name never
//     carries one: a record is proxied or not, for all its ports.
//
// Everything is checked here, whatever the form checked, and written in one
// go. Withdrawing removes what exposing made: the URL copy (its record goes
// with the next DNS pass) and the router mapping with its name.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/dnsrecords"
	"github.com/azukaar/cosmos-server/src/upnp"
	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	maxExposeBody  = 8 << 10
	maxExposeWeb   = 4
	maxExposePorts = 16
)

// replaced by the tests: the router is never asked from a test, and the
// HTTP server is not reloaded there
var (
	routerChanged = func(keys []string) { go upnp.ChoicesChanged(keys) }
	reloadRoutes  = func() { go utils.RestartHTTPServer() }
	listContainers = upnp.ListContainersNow
	// the address the DNS pass publishes names at
	publishedAddress = dnsrecords.CurrentAddress
)

// RiskyPorts are ports that are dangerous to publish to the internet:
// databases, the Docker API, remote desktops and shells, file sharing,
// cluster and debug ports. They are never proposed; opening one needs an
// explicit "I know" (risky). client/src/utils/exposure.js holds the same
// list (a test compares them).
var RiskyPorts = [][2]int{
	{21, 23}, {53, 53}, {111, 111}, {135, 139}, {161, 161}, {389, 389}, {445, 445}, {636, 636},
	{1433, 1433}, {1521, 1521}, {2049, 2049}, {2375, 2376}, {2379, 2380}, {3306, 3306}, {3389, 3389},
	{4243, 4243}, {5005, 5005}, {5432, 5432}, {5900, 5903}, {5984, 5984}, {6379, 6379}, {6443, 6443},
	{8086, 8086}, {9100, 9100}, {9200, 9200}, {9229, 9229}, {9300, 9300}, {10250, 10250},
	{11211, 11211}, {15672, 15672}, {27017, 27019},
}

// IsRiskyPort reports whether a port is on the list above.
func IsRiskyPort(port int) bool {
	for _, r := range RiskyPorts {
		if port >= r[0] && port <= r[1] {
			return true
		}
	}
	return false
}

func init() {
	dnsrecords.ForkMoreHosts = func(hosts map[string][]string) map[string][]string {
		address := publishedAddress()
		if address == "" {
			return hosts
		}
		for host := range portHostnames(utils.GetMainConfig()) {
			if _, known := hosts[host]; !known {
				hosts[host] = []string{address}
			}
		}
		return hosts
	}
}

// portHostnames lists the DNS-only names of exposed ports, with their ports
// ("25565/tcp").
func portHostnames(config utils.Config) map[string][]string {
	out := map[string][]string{}
	for _, choice := range config.HTTPConfig.UPnP.AppPorts {
		host := utils.CloudflareHostname(choice.Hostname)
		if host == "" {
			continue
		}
		out[host] = append(out[host], strconv.Itoa(choice.Port)+"/"+strings.ToLower(choice.Protocol))
	}
	for host := range out {
		sort.Strings(out[host])
	}
	return out
}

// portHostnameOwners tells which container each port name belongs to.
func portHostnameOwners(config utils.Config) map[string]string {
	out := map[string]string{}
	for _, choice := range config.HTTPConfig.UPnP.AppPorts {
		if host := utils.CloudflareHostname(choice.Hostname); host != "" {
			out[host] = choice.Container
		}
	}
	return out
}

// routeHostnames is the set of hostnames URLs answer for.
func routeHostnames(config utils.Config) map[string]bool {
	out := map[string]bool{}
	for _, route := range config.HTTPConfig.ProxyConfig.Routes {
		if route.UseHost {
			if host := utils.CloudflareHostname(route.Host); host != "" {
				out[host] = true
			}
		}
	}
	return out
}

// targetsContainer reports whether a route leads to a container, the way
// the panel finds an app's URLs (utils/routes.jsx).
func targetsContainer(route utils.ProxyRouteConfig, container string) bool {
	if route.Mode != "SERVAPP" {
		return false
	}
	match := regexp.MustCompile(`(?i)^(([a-z]+)://)?` + regexp.QuoteMeta(container) + `(:?[0-9]+)?/?$`)
	return match.MatchString(strings.TrimSpace(route.Target))
}

func isWebTarget(target string) bool {
	lower := strings.ToLower(strings.TrimSpace(target))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

type exposeWeb struct {
	Route    string `json:"route"`
	Hostname string `json:"hostname"`
	Through  string `json:"through"` // cloudflare | direct
	Access   string `json:"access"`  // anyone | users | admins
}

type exposePort struct {
	Port         int    `json:"port"`
	Protocol     string `json:"protocol"`
	Hostname     string `json:"hostname"`
	ExternalPort int    `json:"externalPort"`
	Scheme       string `json:"scheme"`
	Risky        bool   `json:"risky"`
}

type exposeRequest struct {
	Action    string       `json:"action"` // expose | withdraw
	Container string       `json:"container"`
	Confirm   string       `json:"confirm"`
	Web       []exposeWeb  `json:"web"`
	Ports     []exposePort `json:"ports"`
}

// exposeRefusal is a refusal with the code the UI turns into a sentence.
type exposeRefusal struct {
	status int
	code   string
	reason string
	text   string
}

func (r *exposeRefusal) Error() string { return r.text }

func refuseExpose(reason string, text string) *exposeRefusal {
	return &exposeRefusal{http.StatusBadRequest, "EXP003", reason, text}
}

// nameProblem checks a name an app is to be reached at from the internet.
// kind is "web" or "port". It returns a reason code and a sentence, or "".
func nameProblem(config utils.Config, host string, container string, kind string) (string, string) {
	zones := config.HTTPConfig.DNSZones
	index := utils.FindZone(zones, host)
	switch {
	case index == -1:
		return "not_under_domain", host + " is not under one of the domains of Configuration > Domains"
	case strings.ToLower(strings.TrimSuffix(zones[index].Zone, ".")) == host:
		return "apex", "the domain's own name is never used for an app"
	case utils.CloudflareHostname(config.HTTPConfig.Hostname) == host:
		return "panel", host + " is the address of the Cosmos panel"
	}
	zone := zones[index]
	for _, route := range config.HTTPConfig.ProxyConfig.Routes {
		if route.UseHost && utils.CloudflareHostname(route.Host) == host && !targetsContainer(route, container) {
			return "taken", host + " is already the address of the URL " + route.Name
		}
	}
	if owner, used := portHostnameOwners(config)[host]; used && owner != container {
		return "taken", host + " already carries a port of " + owner
	}
	if kind == "web" {
		if utils.HostIsHTTPOnly(config, host) {
			return "http_only", "the domain is served over plain HTTP: Cosmos never offers plain HTTP to the internet"
		}
		return "", ""
	}
	// a port's name is a record Cosmos writes: only where it manages the
	// records, with a token, and where a record of someone else's is never replaced
	if !isCloudflareZone(zone) || !zone.ManageRecords || storedToken(zone) == "" {
		return "not_managed", "Cosmos can give a port a name only under a Cloudflare domain whose records it manages"
	}
	if utils.ShieldedIn(config.HTTPConfig.Cloudflare.Shielded, host) {
		return "shielded", host + " is served through Cloudflare's proxy, which carries no other port"
	}
	syncMu.Lock()
	skip := ""
	if state, ok := synced[zone.Zone]; ok {
		skip = state.skipped[host]
	}
	syncMu.Unlock()
	if skip == SkipConflict && !nameIn(config.HTTPConfig.Cloudflare.TakeOver, host) {
		return "conflict", host + " has a record at Cloudflare that Cosmos did not create"
	}
	return "", ""
}

func nameIn(list []string, name string) bool {
	for _, known := range list {
		if utils.CloudflareHostname(known) == name {
			return true
		}
	}
	return false
}

func accessFields(access string) (auth bool, adminOnly bool, ok bool) {
	switch access {
	case "anyone", "":
		return false, false, true
	case "users":
		return true, false, true
	case "admins":
		return true, true, true
	}
	return false, false, false
}

// freeRouteName is a name no other URL has.
func freeRouteName(routes []utils.ProxyRouteConfig, name string) string {
	taken := map[string]bool{}
	for _, route := range routes {
		taken[route.Name] = true
	}
	if !taken[name] {
		return name
	}
	for n := 2; n < 1000; n++ {
		candidate := name + "-" + strconv.Itoa(n)
		if !taken[candidate] {
			return candidate
		}
	}
	return name
}

// exposeOutcome is what an action changed.
type exposeOutcome struct {
	Routes   []string `json:"routes"`   // URLs created, changed or deleted
	Web      []string `json:"web"`      // names, with how they are served
	Ports    []string `json:"ports"`    // ports, with their names
	Reloaded bool     `json:"reloaded"` // the HTTP server reloads (routes changed)
	keys     []string
	shield   bool
}

// applyExpose changes the configuration for an "expose" request. It changes
// nothing and returns a refusal when any part is not allowed.
func applyExpose(config *utils.Config, request exposeRequest, containers []upnp.Container, address string) (*exposeOutcome, error) {
	out := &exposeOutcome{Routes: []string{}, Web: []string{}, Ports: []string{}}
	routes := append([]utils.ProxyRouteConfig{}, config.HTTPConfig.ProxyConfig.Routes...)
	cf := &config.HTTPConfig.Cloudflare
	shielded := prune(cf.Shielded, servedHostnames(*config))
	wantShield := map[string]bool{}

	for _, web := range request.Web {
		host, ok := domainName(web.Hostname)
		if !ok {
			return nil, refuseExpose("invalid_name", "the address is not a valid name, for example app.example.org")
		}
		if reason, text := nameProblem(*config, host, request.Container, "web"); reason != "" {
			return nil, refuseExpose(reason, text)
		}
		if web.Through != "cloudflare" && web.Through != "direct" {
			return nil, refuseExpose("invalid", "how the address is served must be cloudflare or direct")
		}
		auth, adminOnly, ok := accessFields(web.Access)
		if !ok {
			return nil, refuseExpose("invalid", "who may open it must be anyone, users or admins")
		}
		if web.Through == "cloudflare" && len(portHostnames(*config)[host]) > 0 {
			return nil, refuseExpose("direct_port", host+" carries a port exposed directly: it cannot go through Cloudflare's proxy")
		}

		baseIndex, existing := -1, -1
		for i, route := range routes {
			if route.Name == web.Route {
				baseIndex = i
			}
			if route.UseHost && utils.CloudflareHostname(route.Host) == host {
				existing = i
			}
		}
		if baseIndex == -1 || !targetsContainer(routes[baseIndex], request.Container) || !isWebTarget(routes[baseIndex].Target) || !routes[baseIndex].UseHost {
			return nil, refuseExpose("no_route", "the app has no URL "+web.Route+" to a web interface to start from")
		}
		if routes[baseIndex].IsManaged() || (existing != -1 && routes[existing].IsManaged()) {
			return nil, refuseExpose("managed", "this URL is managed by "+routes[baseIndex].ManagedBy()+" and can only be changed from there")
		}

		if existing == -1 {
			copyRoute := routes[baseIndex]
			zone := strings.ToLower(config.HTTPConfig.DNSZones[utils.FindZone(config.HTTPConfig.DNSZones, host)].Zone)
			copyRoute.Name = freeRouteName(routes, routes[baseIndex].Name+"-"+zone)
			copyRoute.Host = host
			copyRoute.UseHost = true
			copyRoute.Description = request.Container + ", from the internet"
			copyRoute.AuthEnabled, copyRoute.AdminOnly = auth, adminOnly
			routes = append([]utils.ProxyRouteConfig{copyRoute}, routes...)
			out.Routes = append(out.Routes, copyRoute.Name)
		} else if routes[existing].AuthEnabled != auth || routes[existing].AdminOnly != adminOnly {
			routes[existing].AuthEnabled, routes[existing].AdminOnly = auth, adminOnly
			out.Routes = append(out.Routes, routes[existing].Name)
		}
		wantShield[host] = web.Through == "cloudflare"
		out.Web = append(out.Web, host+" ("+web.Through+")")
	}

	// the shield is judged on the configuration with the new URLs in it
	effective := *config
	effective.HTTPConfig.ProxyConfig.Routes = routes
	running := utils.GetMainConfig().HTTPConfig
	if running.HTTPSPort != "" {
		effective.HTTPConfig.HTTPPort, effective.HTTPConfig.HTTPSPort = running.HTTPPort, running.HTTPSPort
	}
	for host, through := range wantShield {
		if !through {
			shielded = removeName(shielded, host)
			continue
		}
		if !utils.ShieldedIn(shielded, host) {
			if problem := shieldProblem(effective, host, address); problem != "" {
				return nil, refuseExpose(problem, host+" cannot be served through Cloudflare ("+problem+")")
			}
			shielded = addName(shielded, host)
			out.shield = true
		}
	}

	choices := config.HTTPConfig.UPnP.AppPorts
	for _, port := range request.Ports {
		protocol := strings.ToLower(strings.TrimSpace(port.Protocol))
		if port.Port < 1 || port.Port > 65535 || (protocol != "tcp" && protocol != "udp") {
			return nil, refuseExpose("invalid", "name the container's port (1 to 65535) and the protocol (tcp or udp)")
		}
		if port.Scheme != "" && port.Scheme != "https" {
			return nil, refuseExpose("http", "plain HTTP is never opened to the internet")
		}
		cfg := config.HTTPConfig
		cfg.UPnP.AppPorts = choices
		status, code, message, ok := upnp.CheckOpen(request.Container, port.Port, protocol, port.ExternalPort, port.Scheme, cfg, containers)
		if !ok {
			return nil, &exposeRefusal{status, code, "router", message}
		}
		if !port.Risky {
			hostPort := 0
			for _, c := range containers {
				if c.Name == request.Container {
					for _, p := range c.Ports {
						if p.ContainerPort == port.Port && strings.EqualFold(p.Protocol, protocol) {
							hostPort = p.HostPort
						}
					}
				}
			}
			for _, number := range []int{port.Port, hostPort, port.ExternalPort} {
				if number != 0 && IsRiskyPort(number) {
					return nil, refuseExpose("risky", fmt.Sprintf("port %d is dangerous to publish to the internet: confirm that you know what it is", number))
				}
			}
		}
		host := ""
		if strings.TrimSpace(port.Hostname) != "" {
			var valid bool
			if host, valid = domainName(port.Hostname); !valid {
				return nil, refuseExpose("invalid_name", "the name of the port is not a valid name, for example game.example.org")
			}
			if reason, text := nameProblem(*config, host, request.Container, "port"); reason != "" {
				return nil, refuseExpose(reason, text)
			}
			if wantShield[host] || utils.ShieldedIn(shielded, host) {
				return nil, refuseExpose("shielded", host+" is served through Cloudflare's proxy, which carries no other port")
			}
		}
		choices = upnp.WithChoice(choices, utils.UPnPAppPort{
			Container: request.Container, Port: port.Port, Protocol: protocol,
			ExternalPort: port.ExternalPort, Scheme: port.Scheme, Since: time.Now(), Hostname: host,
		})
		text := strconv.Itoa(port.Port) + "/" + protocol
		if host != "" {
			text += " " + host
		}
		out.Ports = append(out.Ports, text)
		out.keys = append(out.keys, upnp.ChoiceKey(request.Container, port.Port, protocol))
	}
	if len(choices) > upnp.MaxAppPorts {
		return nil, &exposeRefusal{http.StatusConflict, "UPNP016", "router", fmt.Sprintf("Cosmos keeps at most %d app ports open on the router", upnp.MaxAppPorts)}
	}
	if len(shielded) > utils.CloudflareMaxNames {
		return nil, refuseExpose("too_many", "too many names")
	}

	sort.Strings(shielded)
	cf.Shielded = shielded
	if out.shield {
		cf.TrustRanges = true
	}
	config.HTTPConfig.ProxyConfig.Routes = routes
	config.HTTPConfig.UPnP.AppPorts = choices
	out.Reloaded = len(out.Routes) > 0
	return out, nil
}

// applyWithdraw changes the configuration for a "withdraw" request.
func applyWithdraw(config *utils.Config, request exposeRequest) (*exposeOutcome, error) {
	out := &exposeOutcome{Routes: []string{}, Web: []string{}, Ports: []string{}}
	routes := config.HTTPConfig.ProxyConfig.Routes
	cf := &config.HTTPConfig.Cloudflare

	for _, web := range request.Web {
		host, ok := domainName(web.Hostname)
		if !ok {
			return nil, refuseExpose("invalid_name", "the address is not a valid name")
		}
		index, own := -1, 0
		for i, route := range routes {
			if !targetsContainer(route, request.Container) {
				continue
			}
			own++
			if route.UseHost && utils.CloudflareHostname(route.Host) == host {
				index = i
			}
		}
		if index == -1 || utils.FindZone(config.HTTPConfig.DNSZones, host) == -1 {
			return nil, refuseExpose("not_exposed", host+" is not an internet address of this app")
		}
		if routes[index].IsManaged() {
			return nil, refuseExpose("managed", "this URL is managed by "+routes[index].ManagedBy()+" and can only be changed from there")
		}
		if own <= 1 {
			return nil, refuseExpose("last_route", host+" is the app's only URL: change or delete it under URLs")
		}
		out.Routes = append(out.Routes, routes[index].Name)
		routes = append(append([]utils.ProxyRouteConfig{}, routes[:index]...), routes[index+1:]...)
		out.Web = append(out.Web, host)
	}

	choices := config.HTTPConfig.UPnP.AppPorts
	for _, port := range request.Ports {
		protocol := strings.ToLower(strings.TrimSpace(port.Protocol))
		if port.Port < 1 || port.Port > 65535 || (protocol != "tcp" && protocol != "udp") {
			return nil, refuseExpose("invalid", "name the container's port (1 to 65535) and the protocol (tcp or udp)")
		}
		choices = upnp.WithoutChoice(choices, request.Container, port.Port, protocol)
		out.Ports = append(out.Ports, strconv.Itoa(port.Port)+"/"+protocol)
		out.keys = append(out.keys, upnp.ChoiceKey(request.Container, port.Port, protocol))
	}
	config.HTTPConfig.ProxyConfig.Routes = routes
	config.HTTPConfig.UPnP.AppPorts = choices
	// the names nothing serves any more lose their choices: shield and take-over
	served := servedHostnames(*config)
	cf.Shielded, cf.TakeOver = prune(cf.Shielded, served), prune(cf.TakeOver, served)
	out.Reloaded = len(out.Routes) > 0
	return out, nil
}

// ExposeRoute godoc
// @Summary Expose an app to the internet, or withdraw that
// @Description action "expose": the app's web interface under a name of a configured domain (a copy of one of its URLs), served through Cloudflare's proxy or directly, with or without Cosmos' sign-in; and published ports forwarded by the router, each optionally with a DNS-only name under a Cloudflare domain whose records Cosmos manages. Needs confirm = the container's name; a port on the list of dangerous ports needs risky; plain HTTP is never opened. action "withdraw": removes the URL copies and the router ports named, with their names.
// @Tags config
// @Router /api/expose [post]
func ExposeRoute(w http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}
	var request exposeRequest
	if !readBody(w, req, maxExposeBody, &request) {
		return
	}
	if !upnp.ContainerNameValid(request.Container) || len(request.Web) > maxExposeWeb || len(request.Ports) > maxExposePorts ||
		(request.Action != "expose" && request.Action != "withdraw") || len(request.Web)+len(request.Ports) == 0 {
		utils.HTTPError(w, "Name the action, the container and what to expose (at most 4 addresses and 16 ports)", http.StatusBadRequest, "EXP001")
		return
	}
	if request.Action == "expose" && request.Confirm != request.Container {
		utils.HTTPError(w, "Exposing has to be confirmed with the name of the app", http.StatusBadRequest, "EXP002")
		return
	}

	var containers []upnp.Container
	if request.Action == "expose" && len(request.Ports) > 0 {
		var err error
		if containers, err = listContainers(); err != nil {
			utils.HTTPError(w, "Docker did not answer: the container's ports could not be checked", http.StatusConflict, "UPNP009")
			return
		}
	}

	var outcome *exposeOutcome
	err := updateConfig(func(config *utils.Config) error {
		var err error
		if request.Action == "expose" {
			outcome, err = applyExpose(config, request, containers, knownAddress())
		} else {
			outcome, err = applyWithdraw(config, request)
		}
		return err
	})
	if err != nil {
		var refusal *exposeRefusal
		if errors.As(err, &refusal) {
			w.WriteHeader(refusal.status)
			json.NewEncoder(w).Encode(map[string]interface{}{"status": "error", "code": refusal.code, "message": refusal.text, "reason": refusal.reason})
			return
		}
		utils.HTTPError(w, err.Error(), http.StatusBadRequest, "EXP003")
		return
	}

	kind := utils.SecExposeOpened
	if request.Action == "withdraw" {
		kind = utils.SecExposeClosed
	}
	utils.AuditSecurity(req, kind, request.Container, "web", strings.Join(outcome.Web, ", "), "ports", strings.Join(outcome.Ports, ", "))
	utils.Log("Expose: " + utils.GetAuthContext(req).Nickname + " " + map[string]string{"expose": "exposed", "withdraw": "withdrew"}[request.Action] +
		" " + request.Container + ": " + strings.Join(append(append([]string{}, outcome.Web...), outcome.Ports...), ", "))

	forgetSync()
	triggerReconcile()
	AfterChange()
	if len(outcome.keys) > 0 {
		routerChanged(outcome.keys)
	}
	if outcome.Reloaded {
		reloadRoutes()
	}
	answer(w, outcome)
}

// pruneStale drops from the shield and take-over lists the names Cosmos no
// longer serves (a deleted URL or a forgotten port): otherwise a later URL of
// the same name would be shielded without being asked, and Fail2ban would
// keep Cloudflare on its never-ban list for nothing.
func pruneStale() {
	changed := false
	updateConfig(func(config *utils.Config) error {
		served := servedHostnames(*config)
		cf := &config.HTTPConfig.Cloudflare
		shielded, takeOver := prune(cf.Shielded, served), prune(cf.TakeOver, served)
		if len(shielded) == len(cf.Shielded) && len(takeOver) == len(cf.TakeOver) {
			return errors.New("unchanged")
		}
		cf.Shielded, cf.TakeOver = shielded, takeOver
		changed = true
		return nil
	})
	if changed {
		utils.Log("[Cloudflare] forgot the choices of names that are no longer served")
		forgetSync()
		AfterChange()
	}
}

func startPruning() {
	go func() {
		for {
			time.Sleep(time.Minute)
			pruneStale()
		}
	}()
}

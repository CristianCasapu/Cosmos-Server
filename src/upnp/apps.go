package upnp

// The published ports of containers that the router forwards as well: the
// choices an administrator made (kept in the configuration, HTTPConfig.UPnP.
// AppPorts), what they turn into given the containers Docker has right now,
// and the routes that change them.
//
// A choice is keyed by the container's name, the container's port and the
// protocol. It follows the container through a re-creation (same name) and a
// change of its host port; a stopped container keeps its choice but not its
// mapping; a container or port that is gone for longer than forgetAfter
// loses the choice, so a later container of the same name does not inherit
// an open port.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	// maxAppPorts bounds how many app ports Cosmos keeps open on the router.
	maxAppPorts = 32
	// forgetAfter: how long a choice outlives its container or its published
	// port. Long enough for a re-creation done outside Cosmos.
	forgetAfter = 15 * time.Minute
	maxAPIBody  = 4 << 10
)

// defaultReserved are the external ports Cosmos never asks a router for
// unless the list is changed in the settings: web and mail. They are the
// ports most often forwarded by hand to another machine, which a router
// does not always show to UPnP clients.
var defaultReserved = []int{25, 80, 110, 143, 443, 465, 587, 993, 995}

// reservedPorts reads the setting: empty is the default list, "none" no
// port at all, otherwise numbers separated by commas or spaces. What is not
// a port is ignored.
func reservedPorts(setting string) (map[int]bool, []int) {
	list := defaultReserved
	if text := strings.ToLower(strings.TrimSpace(setting)); text == "none" {
		list = nil
	} else if text != "" {
		list = nil
		for _, field := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' }) {
			if port, err := parsePort(field); err == nil && len(list) < 64 {
				list = append(list, port)
			}
		}
	}
	set := map[int]bool{}
	sorted := []int{}
	for _, port := range list {
		if !set[port] {
			set[port] = true
			sorted = append(sorted, port)
		}
	}
	sort.Ints(sorted)
	return set, sorted
}

// Container is what the reconciler needs to know about a container.
type Container struct {
	Name     string
	Running  bool
	IsCosmos bool // the container Cosmos itself runs in
	Ports    []Published
}

// Published is one port of a container published on the host.
type Published struct {
	ContainerPort int
	Protocol      string // "tcp" or "udp"
	HostPort      int
	HostIP        string // "" (or 0.0.0.0, ::) for every address of the host
}

// ListContainers lists the containers and what they publish. Set by the
// server at start (src/fork_upnp_apps.go): this package does not import the
// Docker client.
var ListContainers func() ([]Container, error)

func listContainers() ([]Container, error) {
	if ListContainers == nil {
		return nil, errors.New("the list of containers is not available")
	}
	return ListContainers()
}

// updateUPnPConfig changes the UPnP part of the stored configuration: under
// the configuration lock, from the file, written only when something
// changed. The change must not alter a slice in place. A variable so the
// tests keep the configuration in memory.
var updateUPnPConfig = func(change func(*utils.UPnPConfig)) {
	utils.ConfigLock.Lock()
	defer utils.ConfigLock.Unlock()
	config := utils.ReadConfigFromFile()
	before := config.HTTPConfig.UPnP
	change(&config.HTTPConfig.UPnP)
	if reflect.DeepEqual(before, config.HTTPConfig.UPnP) {
		return
	}
	utils.SetBaseMainConfig(config)
}

func loadChoices() []utils.UPnPAppPort {
	choices := utils.GetMainConfig().HTTPConfig.UPnP.AppPorts
	if len(choices) > maxAppPorts {
		choices = choices[:maxAppPorts]
	}
	return choices
}

func appKey(container string, port int, protocol string) string {
	return "app:" + container + ":" + strconv.Itoa(port) + "/" + strings.ToLower(protocol)
}

func choiceKey(choice utils.UPnPAppPort) string {
	return appKey(choice.Container, choice.Port, choice.Protocol)
}

// forgetChoices drops the choices with these keys.
func forgetChoices(keys []string) {
	drop := map[string]bool{}
	for _, key := range keys {
		drop[key] = true
	}
	updateUPnPConfig(func(config *utils.UPnPConfig) {
		kept := []utils.UPnPAppPort{}
		for _, choice := range config.AppPorts {
			if !drop[choiceKey(choice)] {
				kept = append(kept, choice)
			}
		}
		config.AppPorts = kept
	})
	for _, key := range keys {
		utils.Log("UPnP: forgot the choice for " + strings.TrimPrefix(key, "app:") + ": its container or published port is gone")
	}
}

// rememberAssigned stores the external port each of these choices got.
func rememberAssigned(assigned map[string]int) {
	updateUPnPConfig(func(config *utils.UPnPConfig) {
		list := make([]utils.UPnPAppPort, len(config.AppPorts))
		copy(list, config.AppPorts)
		for i := range list {
			if port, ok := assigned[choiceKey(list[i])]; ok {
				list[i].AssignedPort = port
			}
		}
		config.AppPorts = list
	})
}

func isEveryAddress(ip string) bool {
	return ip == "" || ip == "0.0.0.0" || ip == "::"
}

func isLoopback(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}

// publishedBy finds how a container publishes one of its ports: on every
// address when it does, else on one address that is not the loopback, else
// on the loopback. ok is false when the port is not published at all.
func publishedBy(container Container, port int, protocol string) (Published, bool) {
	var found *Published
	rank := func(p Published) int {
		switch {
		case isEveryAddress(p.HostIP):
			return 3
		case !isLoopback(p.HostIP):
			return 2
		}
		return 1
	}
	for i := range container.Ports {
		p := container.Ports[i]
		if p.ContainerPort != port || !strings.EqualFold(p.Protocol, protocol) || p.HostPort < 1 || p.HostPort > 65535 {
			continue
		}
		if found == nil || rank(p) > rank(*found) {
			found = &container.Ports[i]
		}
	}
	if found == nil {
		return Published{}, false
	}
	return *found, true
}

// appDescription is what the mapping of an app port is called on the
// router: short, plain ASCII, starting with Cosmos' mark.
func appDescription(container string) string {
	name := []rune{}
	for _, r := range container {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			name = append(name, r)
		}
	}
	if len(name) > 24 {
		name = name[:24]
	}
	return descriptionPrefix + "app " + string(name)
}

// appWants turns the choices into what to ask the router for, given the
// containers there are now, and names the choices to forget.
func appWants(choices []utils.UPnPAppPort, cfg utils.HTTPConfig, now time.Time) ([]want, []string) {
	if len(choices) == 0 {
		missing = map[string]time.Time{}
		return nil, nil
	}
	containers, dockerErr := listContainers()
	byName := map[string]Container{}
	for _, container := range containers {
		byName[container.Name] = container
	}
	cosmosPorts := map[int]bool{}
	for _, value := range []string{cfg.HTTPPort, cfg.HTTPSPort} {
		if port, err := parsePort(value); err == nil {
			cosmosPorts[port] = true
		}
	}

	wants := []want{}
	forget := []string{}
	seen := map[string]bool{}
	for i, choice := range choices {
		key := choiceKey(choice)
		if seen[key] {
			continue
		}
		seen[key] = true
		protocol := strings.ToUpper(choice.Protocol)
		w := want{
			key: key, auto: true, assigned: choice.AssignedPort, choice: i,
			description: appDescription(choice.Container),
			mapping: Mapping{
				Name: fmt.Sprintf("%s %d/%s", choice.Container, choice.Port, strings.ToLower(choice.Protocol)),
				Kind: "app", Container: choice.Container, ContainerPort: choice.Port, Protocol: protocol,
				WantedPort: choice.ExternalPort, ExternalPort: choice.ExternalPort, Scheme: choice.Scheme, State: StateWaiting,
				Hostname: choice.Hostname,
			},
		}
		gone := false
		container, exists := byName[choice.Container]
		published, isPublished := publishedBy(container, choice.Port, choice.Protocol)
		switch {
		case dockerErr != nil:
			// Docker does not answer: what is held is kept as it is, nothing is forgotten
			w.mapping.Code = "dockerUnavailable"
			if h, ok := held[key]; ok {
				w.eligible = true
				w.mapping.InternalPort = h.InternalPort
				if w.mapping.WantedPort == 0 {
					w.mapping.WantedPort = h.InternalPort
				}
				w.mapping.Code = ""
			}
		case !exists:
			w.mapping.Code, gone = "gone", true
		case container.IsCosmos:
			w.mapping.Code, gone = "cosmosPort", true
		case !container.Running:
			w.mapping.Code = "stopped"
		case !isPublished:
			w.mapping.Code, gone = "notPublished", true
		case isLoopback(published.HostIP):
			w.mapping.Code = "localOnly"
		case cosmosPorts[published.HostPort]:
			w.mapping.Code = "cosmosPort"
		default:
			w.eligible = true
			w.mapping.InternalPort = published.HostPort
			if w.mapping.WantedPort == 0 {
				w.mapping.WantedPort = published.HostPort
			}
			if !isEveryAddress(published.HostIP) {
				w.bindIP = published.HostIP
			}
		}
		if w.mapping.ExternalPort == 0 {
			w.mapping.ExternalPort = w.mapping.WantedPort
		}

		if gone {
			since, known := missing[key]
			if !known {
				missing[key] = now
			} else if now.Sub(since) >= forgetAfter {
				forget = append(forget, key)
			}
		} else {
			delete(missing, key)
		}
		wants = append(wants, w)
	}
	for key := range missing {
		if !seen[key] {
			delete(missing, key)
		}
	}
	return wants, forget
}

// --- the routes

var containerNameRule = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

// appPortRequest switches the forwarding of one published port on or off.
type appPortRequest struct {
	Container string `json:"container"`
	Port      int    `json:"port"`     // the container's port
	Protocol  string `json:"protocol"` // tcp or udp
	Enabled   bool   `json:"enabled"`
	// ExternalPort: the external port to ask for; 0 for the host port.
	ExternalPort int `json:"externalPort"`
	// Scheme: what is on the port: "http", "https", or "" for anything else.
	Scheme string `json:"scheme"`
	// Confirm must name the port and the app, "<container>:<port>/<protocol>",
	// to switch the forwarding on.
	Confirm string `json:"confirm"`
}

// refusal is why a port cannot be opened, as a sentence for the admin.
type refusal struct {
	status  int
	code    string
	message string
}

// checkAppPort decides whether a published port may be opened on the router.
func checkAppPort(request appPortRequest, cfg utils.HTTPConfig, containers []Container, choices []utils.UPnPAppPort) *refusal {
	bad := func(code string, message string) *refusal {
		return &refusal{http.StatusBadRequest, code, message}
	}
	conflict := func(code string, message string) *refusal {
		return &refusal{http.StatusConflict, code, message}
	}
	if request.Confirm != fmt.Sprintf("%s:%d/%s", request.Container, request.Port, request.Protocol) {
		return bad("UPNP004", "Opening a port has to be confirmed with the name of the app and the port")
	}
	// never plain HTTP from the internet: a web page goes through Cosmos' HTTPS port
	if request.Scheme != "" && request.Scheme != "https" {
		return bad("UPNP005", "What is on the port must be https or nothing: plain HTTP is never opened to the internet")
	}
	reserved, _ := reservedPorts(cfg.UPnP.ReservedPorts)
	cosmosPorts := map[int]bool{}
	for _, value := range []string{cfg.HTTPPort, cfg.HTTPSPort, cfg.UPnP.ExternalHTTPSPort, cfg.UPnP.ExternalHTTPPort} {
		if port, err := parsePort(value); err == nil {
			cosmosPorts[port] = true
		}
	}
	if request.ExternalPort != 0 {
		switch {
		case request.ExternalPort < 1 || request.ExternalPort > 65535:
			return bad("UPNP006", "The external port must be between 1 and 65535")
		case reserved[request.ExternalPort]:
			return bad("UPNP007", fmt.Sprintf("External port %d is on the list of ports Cosmos never asks the router for", request.ExternalPort))
		case cosmosPorts[request.ExternalPort] && request.Protocol == "tcp":
			return bad("UPNP008", fmt.Sprintf("External port %d is one of Cosmos' own", request.ExternalPort))
		}
	}

	var container *Container
	for i := range containers {
		if containers[i].Name == request.Container {
			container = &containers[i]
		}
	}
	switch {
	case container == nil:
		return conflict("UPNP010", "There is no container of that name")
	case container.IsCosmos:
		return conflict("UPNP011", "This is the container Cosmos itself runs in: its ports are opened under Internet access, not as an app's")
	case !container.Running:
		return conflict("UPNP012", "The container is not running: start it first")
	}
	published, ok := publishedBy(*container, request.Port, request.Protocol)
	switch {
	case !ok:
		return conflict("UPNP013", "The container does not publish that port on the server")
	case isLoopback(published.HostIP):
		return conflict("UPNP014", "The port is published for this server only (127.0.0.1): nothing outside the server can reach it")
	case request.Protocol == "tcp" && (published.HostPort == portOr(cfg.HTTPPort) || published.HostPort == portOr(cfg.HTTPSPort)):
		return conflict("UPNP015", "That host port is one of Cosmos' own: it is opened under Internet access, not as an app's")
	}

	key := appKey(request.Container, request.Port, request.Protocol)
	others := 0
	for _, choice := range choices {
		if choiceKey(choice) != key {
			others++
		}
	}
	if others >= maxAppPorts {
		return conflict("UPNP016", fmt.Sprintf("Cosmos keeps at most %d app ports open on the router: close one first", maxAppPorts))
	}
	return nil
}

func portOr(value string) int {
	port, _ := parsePort(value)
	return port
}

func readRequest(w http.ResponseWriter, req *http.Request, into any) bool {
	raw, err := io.ReadAll(io.LimitReader(req.Body, maxAPIBody+1))
	if err != nil || len(raw) > maxAPIBody {
		utils.HTTPError(w, "Request too large", http.StatusBadRequest, "UPNP001")
		return false
	}
	if err := json.Unmarshal(raw, into); err != nil {
		utils.HTTPError(w, "The request could not be read", http.StatusBadRequest, "UPNP002")
		return false
	}
	return true
}

// forReader takes out what only administrators may see: the other devices'
// mappings carry addresses of the local network.
func forReader(st Status, req *http.Request) Status {
	if utils.HasPermission(req, utils.PERM_ADMIN_READ) {
		return st
	}
	st.Foreign = []Foreign{}
	// who holds a port Cosmos wanted is an address of the local network too
	hide := func(list []Mapping) []Mapping {
		out := make([]Mapping, len(list))
		for i, m := range list {
			if m.Holder != "" || m.Code == "conflict" {
				m.Holder = ""
				if m.Error != "" {
					m.Error = fmt.Sprintf("external port %d is already in use on the router", m.WantedPort)
				}
			}
			out[i] = m
		}
		return out
	}
	st.Mappings, st.Apps = hide(st.Mappings), hide(st.Apps)
	return st
}

func answer(w http.ResponseWriter, st Status) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
		"data":   st,
	})
}

// AppPortRoute godoc
// @Summary Open or close a published port of a container on the router
// @Description Asks the router, over UPnP, to forward a port a container publishes on the server (enabled true), or withdraws that (enabled false). Opening needs confirm = "<container>:<port>/<protocol>". The container must run and publish the port; Cosmos' own ports and the ports on the reserved list are refused. Returns the new status.
// @Tags config
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body appPortRequest true "The container, its port and protocol, and whether to forward it"
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Failure 409 {object} utils.HTTPErrorResult
// @Router /api/upnp/apps [post]
func AppPortRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	var request appPortRequest
	if !readRequest(w, req, &request) {
		return
	}
	request.Protocol = strings.ToLower(strings.TrimSpace(request.Protocol))
	if !containerNameRule.MatchString(request.Container) || request.Port < 1 || request.Port > 65535 || (request.Protocol != "tcp" && request.Protocol != "udp") {
		utils.HTTPError(w, "Name the container, its port (1 to 65535) and the protocol (tcp or udp)", http.StatusBadRequest, "UPNP003")
		return
	}
	key := appKey(request.Container, request.Port, request.Protocol)
	portText := strconv.Itoa(request.Port) + "/" + request.Protocol
	cfg := utils.GetMainConfig().HTTPConfig

	if request.Enabled {
		containers, err := listContainers()
		if err != nil {
			utils.HTTPError(w, "Docker did not answer: the container's ports could not be checked", http.StatusConflict, "UPNP009")
			return
		}
		if refused := checkAppPort(request, cfg, containers, cfg.UPnP.AppPorts); refused != nil {
			utils.HTTPError(w, refused.message, refused.status, refused.code)
			return
		}
		// security record: what was asked for and allowed (a refused request
		// is in the record as a failed change through the API)
		utils.AuditSecurity(req, utils.SecUPnPPortOpened, request.Container, "port", portText)
		now := clock()
		updateUPnPConfig(func(config *utils.UPnPConfig) {
			choice := utils.UPnPAppPort{
				Container: request.Container, Port: request.Port, Protocol: request.Protocol,
				ExternalPort: request.ExternalPort, Scheme: request.Scheme, Since: now,
			}
			list := []utils.UPnPAppPort{}
			for _, old := range config.AppPorts {
				if choiceKey(old) != key {
					list = append(list, old)
					continue
				}
				// the same port again: what the router gave is kept unless another port is asked for
				choice.Since = old.Since
				if old.ExternalPort == request.ExternalPort {
					choice.AssignedPort = old.AssignedPort
				}
			}
			config.AppPorts = append(list, choice)
		})
		utils.Log("UPnP: " + utils.GetAuthContext(req).Nickname + " asked to open port " + portText + " of " + request.Container + " on the router")
	} else {
		utils.AuditSecurity(req, utils.SecUPnPPortClosed, request.Container, "port", portText) // security record
		forgetChoices([]string{key})
	}

	runMu.Lock()
	delete(failed, key)
	delete(missing, key)
	st := run(makePlan(utils.GetMainConfig().HTTPConfig, clock()), false)
	runMu.Unlock()
	answer(w, forReader(st, req))
}

// closeRequest names one of Cosmos' own mappings.
type closeRequest struct {
	Name string `json:"name"` // "HTTPS" or "HTTP"
}

// CloseRoute godoc
// @Summary Close one of Cosmos' own ports on the router
// @Description Switches the automatic forwarding of Cosmos' own port off in the settings and withdraws the mapping: "HTTP" the plain HTTP port alone, "HTTPS" the automatic forwarding of Cosmos' ports altogether. App ports are closed with /api/upnp/apps. Returns the new status.
// @Tags config
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body closeRequest true "HTTPS or HTTP"
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/upnp/close [post]
func CloseRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	var request closeRequest
	if !readRequest(w, req, &request) {
		return
	}
	if request.Name != "HTTPS" && request.Name != "HTTP" {
		utils.HTTPError(w, "Name the mapping to close: HTTPS or HTTP", http.StatusBadRequest, "UPNP003")
		return
	}
	utils.AuditSecurity(req, utils.SecUPnPPortClosed, "Cosmos "+request.Name) // security record
	updateUPnPConfig(func(config *utils.UPnPConfig) {
		if request.Name == "HTTP" {
			config.ForwardHTTP = false
		} else {
			config.Enabled = false
		}
	})
	utils.Log("UPnP: " + utils.GetAuthContext(req).Nickname + " closed Cosmos' " + request.Name + " port on the router")
	answer(w, forReader(reconcile(false), req))
}

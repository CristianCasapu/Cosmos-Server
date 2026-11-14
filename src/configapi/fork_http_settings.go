package configapi

// Fork addition: the HTTP settings are checked before they are saved, on the
// server, because a wrong value there takes the panel away with the save:
//
//   - a hostname that is not one (a scheme, a path, a space) breaks the
//     certificates and the links;
//   - a port that is not a number between 1 and 65535, the same port twice, or
//     a port another program or a container already holds: the web server
//     cannot listen after the restart that follows the save;
//   - an admin whitelist that does not contain the address the admin is on,
//     or an entry that is not an address: every admin call is refused right
//     after the save, and only the config file on the server brings it back;
//   - trusted proxies that are neither an address, a range nor a URL.
//
// Only what changed is checked, so that an old odd value never blocks saving
// something else. The form shows the same findings before saving, from the
// detection route below (HTTPCheckRoute); the form's check is a courtesy,
// this one is the guard.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	maxListEntries  = 64
	maxListEntryLen = 256
)

// The state of a port, as codes; the form turns them into sentences.
const (
	portFree      = "free"      // nothing listens on it here
	portCosmos    = "cosmos"    // Cosmos itself, today (it lets it go on restart)
	portContainer = "container" // a running container publishes it
	portOther     = "other"     // another program on this server listens on it
	portUnknown   = "unknown"   // cannot be told from here
	portInvalid   = "invalid"   // not a port number
)

// httpPort reads a port as the settings store it (text): digits only, 1–65535.
func httpPort(value string) (int, bool) {
	text := strings.TrimSpace(value)
	if text == "" || len(text) > 5 {
		return 0, false
	}
	for _, c := range text {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	port, err := strconv.Atoi(text)
	if err != nil || port < 1 || port > 65535 {
		return 0, false
	}
	return port, true
}

// listenProbe tries to take a TCP port on every address of this server for an
// instant. A variable so that the tests need no network.
var listenProbe = func(port int) error {
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return err
	}
	return listener.Close()
}

// publishedPorts lists the host ports running containers publish (TCP), with
// the name of the container. A variable for the tests.
var publishedPorts = func() (map[int]string, error) {
	containers, err := docker.ListContainers()
	if err != nil {
		return nil, err
	}
	found := map[int]string{}
	for _, c := range containers {
		if c.State != "running" {
			continue
		}
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		for _, p := range c.Ports {
			if p.PublicPort != 0 && (p.Type == "" || p.Type == "tcp") {
				if _, taken := found[int(p.PublicPort)]; !taken {
					found[int(p.PublicPort)] = name
				}
			}
		}
	}
	return found, nil
}

// cannotSeeHostPorts: inside a container of its own network, a free port in
// Cosmos' namespace says nothing about the host's.
var cannotSeeHostPorts = func() bool {
	return utils.IsInsideContainer && !utils.IsHostNetwork
}

type portFinding struct {
	Port   int    `json:"port"`
	State  string `json:"state"`
	Holder string `json:"holder,omitempty"`
}

// portState says who holds a port. `current` are the ports Cosmos listens on
// now; `containers` may be nil when Docker could not be asked.
func portState(port int, current []int, containers map[int]string) portFinding {
	finding := portFinding{Port: port}
	for _, c := range current {
		if c == port {
			finding.State = portCosmos
			return finding
		}
	}
	if name, ok := containers[port]; ok {
		finding.State = portContainer
		finding.Holder = plainText(name)
		return finding
	}
	if cannotSeeHostPorts() {
		finding.State = portUnknown
		return finding
	}
	err := listenProbe(port)
	switch {
	case err == nil:
		finding.State = portFree
	case errors.Is(err, syscall.EADDRINUSE):
		finding.State = portOther
	default:
		// not allowed (a port below 1024 without the right), or anything else
		finding.State = portUnknown
	}
	return finding
}

// runningHTTPConfig: the settings Cosmos runs with, COSMOS_HTTP_PORT and
// COSMOS_HTTPS_PORT applied (the stored settings do not have them).
var runningHTTPConfig = func() utils.HTTPConfig {
	return utils.GetMainConfig().HTTPConfig
}

// currentPorts: the ports Cosmos listens on now, those of the stored settings
// and those it runs with when the environment overrides them.
func currentPorts(config utils.Config) []int {
	ports := []int{}
	running := runningHTTPConfig()
	for _, value := range []string{config.HTTPConfig.HTTPPort, config.HTTPConfig.HTTPSPort, running.HTTPPort, running.HTTPSPort} {
		if port, ok := httpPort(value); ok {
			ports = append(ports, port)
		}
	}
	return ports
}

// cleanList trims the entries of a list the form sends and drops empty ones.
func cleanList(list []string) []string {
	cleaned := []string{}
	for _, entry := range list {
		if entry = strings.TrimSpace(entry); entry != "" {
			cleaned = append(cleaned, entry)
		}
	}
	return cleaned
}

// validAddressEntry: an IP address or a range in CIDR notation, the two forms
// utils.checkIPAccess understands.
func validAddressEntry(entry string) bool {
	if len(entry) > maxListEntryLen {
		return false
	}
	if net.ParseIP(entry) != nil {
		return true
	}
	_, _, err := net.ParseCIDR(entry)
	return err == nil
}

// validProxyEntry: what utils.IsTrustedProxy understands: an address, a range,
// or the URL of a list of ranges.
func validProxyEntry(entry string) bool {
	if validAddressEntry(entry) {
		return true
	}
	if len(entry) > maxListEntryLen || strings.ContainsAny(entry, " \t\r\n") {
		return false
	}
	return strings.HasPrefix(entry, "https://") || strings.HasPrefix(entry, "http://")
}

func sameList(a, b []string) bool {
	a, b = cleanList(a), cleanList(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// requestAddresses: the client's address as the restrictions see it (behind a
// trusted proxy, the visitor's), and the address the connection came from.
func requestAddresses(req *http.Request) (string, string) {
	client, _ := req.Context().Value("ClientID").(string)
	if client == "" {
		client = utils.GetClientIP(req)
	}
	remote, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		remote = req.RemoteAddr
	}
	return client, remote
}

// wouldLetThrough: would the admin restrictions of the new settings let this
// very request through? The function the restrictions themselves use.
var wouldLetThrough = func(client, remote string, constellationOnly bool, whitelist []string) bool {
	return utils.CheckRouteIPAccess(client, remote, constellationOnly, whitelist)
}

type httpSettingsProblem struct {
	Code    string
	Message string
}

// checkHTTPSettings checks what changed in the HTTP settings of a save.
// It also stores the lists trimmed, as the form shows them.
func checkHTTPSettings(req *http.Request, request *utils.Config, stored utils.Config) *httpSettingsProblem {
	newHTTP, oldHTTP := &request.HTTPConfig, stored.HTTPConfig

	if strings.TrimSpace(newHTTP.Hostname) != oldHTTP.Hostname {
		newHTTP.Hostname = strings.TrimSpace(newHTTP.Hostname)
		if !utils.IsValidHostnameSyntax(newHTTP.Hostname) {
			return &httpSettingsProblem{"UC030", "The address of the server must be a name such as cosmos.example.com or an IP address, without http://, a path or a space"}
		}
	}

	portsChanged := strings.TrimSpace(newHTTP.HTTPPort) != oldHTTP.HTTPPort || strings.TrimSpace(newHTTP.HTTPSPort) != oldHTTP.HTTPSPort
	if portsChanged {
		newHTTP.HTTPPort, newHTTP.HTTPSPort = strings.TrimSpace(newHTTP.HTTPPort), strings.TrimSpace(newHTTP.HTTPSPort)
		httpP, okHTTP := httpPort(newHTTP.HTTPPort)
		httpsP, okHTTPS := httpPort(newHTTP.HTTPSPort)
		if !okHTTP || !okHTTPS {
			return &httpSettingsProblem{"UC031", "A port is a whole number from 1 to 65535, for example 80 for HTTP and 443 for HTTPS"}
		}
		if httpP == httpsP {
			return &httpSettingsProblem{"UC031", "HTTP and HTTPS need two different ports, for example 80 and 443"}
		}
		containers, _ := publishedPorts()
		current := currentPorts(stored)
		for _, port := range []int{httpP, httpsP} {
			if port == mustPort(oldHTTP.HTTPPort) || port == mustPort(oldHTTP.HTTPSPort) {
				continue
			}
			switch found := portState(port, current, containers); found.State {
			case portContainer:
				return &httpSettingsProblem{"UC032", fmt.Sprintf("Port %d is already published by the container %s: pick another port, or change that container first", port, found.Holder)}
			case portOther:
				return &httpSettingsProblem{"UC032", fmt.Sprintf("Port %d is already in use by another program on this server: pick another port", port)}
			}
		}
	}

	whitelist := cleanList(request.AdminWhitelistIPs)
	if !sameList(whitelist, stored.AdminWhitelistIPs) || request.AdminConstellationOnly != stored.AdminConstellationOnly {
		if len(whitelist) > maxListEntries {
			return &httpSettingsProblem{"UC033", fmt.Sprintf("The admin whitelist holds at most %d entries", maxListEntries)}
		}
		for _, entry := range whitelist {
			if !validAddressEntry(entry) {
				return &httpSettingsProblem{"UC033", "Not an address or a range: " + plainText(entry) + ". Enter addresses such as 192.168.1.20 or ranges such as 192.168.1.0/24, separated by commas"}
			}
		}
		client, remote := requestAddresses(req)
		if !wouldLetThrough(client, remote, request.AdminConstellationOnly, whitelist) {
			return &httpSettingsProblem{"UC034", "This would lock you out: you are connected from " + plainText(client) + ", which the new admin restrictions do not allow. Add this address or its network to the list, or save from an address the list allows"}
		}
		request.AdminWhitelistIPs = whitelist
	}

	proxies := cleanList(newHTTP.TrustedProxies)
	if !sameList(proxies, oldHTTP.TrustedProxies) {
		if len(proxies) > maxListEntries {
			return &httpSettingsProblem{"UC035", fmt.Sprintf("At most %d trusted proxies", maxListEntries)}
		}
		for _, entry := range proxies {
			if !validProxyEntry(entry) {
				return &httpSettingsProblem{"UC035", "Not an address, a range or a URL: " + plainText(entry) + ". Enter for example 172.17.0.1, 10.0.0.0/8 or https://www.cloudflare.com/ips-v4"}
			}
		}
		newHTTP.TrustedProxies = proxies
	}
	return nil
}

func mustPort(value string) int {
	port, _ := httpPort(value)
	return port
}

// --- the detection route ---------------------------------------------------

const (
	httpCheckBody      = 1024
	httpChecksATminute = 30
)

type httpCheckLimiter struct {
	mu     sync.Mutex
	recent []time.Time
}

func (l *httpCheckLimiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.recent[:0]
	for _, at := range l.recent {
		if now.Sub(at) < time.Minute {
			kept = append(kept, at)
		}
	}
	l.recent = kept
	if len(l.recent) >= httpChecksATminute {
		return false
	}
	l.recent = append(l.recent, now)
	return true
}

var httpCheckLimit = &httpCheckLimiter{}

type serverAddress struct {
	IP      string `json:"ip"`
	Network string `json:"network"`
}

// serverAddresses lists the addresses of this server's own network
// interfaces, without loopback, link-local and the bridges of Docker. A
// variable so that the tests need no network.
var serverAddresses = func() []serverAddress {
	found := []serverAddress{}
	interfaces, err := net.Interfaces()
	if err != nil {
		return found
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		name := iface.Name
		if strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "veth") {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.IsLoopback() || ipNet.IP.IsLinkLocalUnicast() {
				continue
			}
			network := &net.IPNet{IP: ipNet.IP.Mask(ipNet.Mask), Mask: ipNet.Mask}
			found = append(found, serverAddress{IP: ipNet.IP.String(), Network: network.String()})
			if len(found) >= 16 {
				return found
			}
		}
	}
	return found
}

type httpCheckRequest struct {
	HTTPPort          string   `json:"httpPort"`
	HTTPSPort         string   `json:"httpsPort"`
	AdminWhitelist    []string `json:"adminWhitelist"`
	ConstellationOnly bool     `json:"constellationOnly"`
}

type httpCheckEntry struct {
	Value string `json:"value"`
	Valid bool   `json:"valid"`
}

type httpCheckAnswer struct {
	You struct {
		Address      string `json:"address"`
		Local        bool   `json:"local"`
		ThroughProxy bool   `json:"throughProxy"`
	} `json:"you"`
	Server struct {
		Addresses []serverAddress `json:"addresses"`
		InDocker  bool            `json:"inDocker"`
	} `json:"server"`
	Ports     []portFinding `json:"ports"`
	Whitelist struct {
		Passes  bool             `json:"passes"`
		Entries []httpCheckEntry `json:"entries"`
	} `json:"whitelist"`
}

func httpCheck(req *http.Request, body httpCheckRequest, config utils.Config) httpCheckAnswer {
	var answer httpCheckAnswer
	client, remote := requestAddresses(req)
	answer.You.Address = client
	answer.You.Local = net.ParseIP(client) != nil && utils.IsLocalIP(client)
	answer.You.ThroughProxy = client != remote
	answer.Server.Addresses = serverAddresses()
	answer.Server.InDocker = cannotSeeHostPorts()

	// the two ports of the form and nothing else: this is not a port scanner
	containers, _ := publishedPorts()
	current := currentPorts(config)
	answer.Ports = []portFinding{}
	for _, value := range []string{body.HTTPPort, body.HTTPSPort} {
		port, ok := httpPort(value)
		if !ok {
			answer.Ports = append(answer.Ports, portFinding{State: portInvalid})
			continue
		}
		answer.Ports = append(answer.Ports, portState(port, current, containers))
	}

	whitelist := cleanList(body.AdminWhitelist)
	answer.Whitelist.Entries = []httpCheckEntry{}
	for _, entry := range whitelist {
		answer.Whitelist.Entries = append(answer.Whitelist.Entries, httpCheckEntry{Value: entry, Valid: validAddressEntry(entry)})
	}
	answer.Whitelist.Passes = wouldLetThrough(client, remote, body.ConstellationOnly, whitelist)
	return answer
}

var timeNow = time.Now

// decodeHTTPCheck reads a bounded body with the known fields only.
func decodeHTTPCheck(req *http.Request, body *httpCheckRequest) error {
	raw, err := io.ReadAll(io.LimitReader(req.Body, httpCheckBody+1))
	if err != nil {
		return err
	}
	if len(raw) > httpCheckBody {
		return errors.New("request too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		return err
	}
	if len(body.AdminWhitelist) > maxListEntries {
		return errors.New("too many entries")
	}
	return nil
}

// HTTPCheckRoute godoc
// @Summary Check HTTP settings before saving them
// @Description Says, for the HTTP settings being edited, whether each of the two ports is free on this server, held by Cosmos, by a container or by another program (by binding it for an instant on this server; nothing is contacted), whether the admin whitelist would let the current request through, the address the request comes from and the addresses of the server's own interfaces. Nothing is saved.
// @Tags config
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Failure 429 {object} utils.HTTPErrorResult
// @Router /api/config/http-check [post]
func HTTPCheckRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	var body httpCheckRequest
	if err := decodeHTTPCheck(req, &body); err != nil {
		utils.HTTPError(w, "Invalid request", http.StatusBadRequest, "HC002")
		return
	}
	if !httpCheckLimit.allow(timeNow()) {
		utils.HTTPError(w, "Too many checks: wait a moment and try again", http.StatusTooManyRequests, "HC003")
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
		"data":   httpCheck(req, body, utils.GetMainConfig()),
	})
}

// plainText keeps a value from the request or from Docker fit for a message:
// no control characters, at most 80 characters.
func plainText(value string) string {
	var b strings.Builder
	for _, c := range value {
		if c < 0x20 || c == 0x7f {
			continue
		}
		if b.Len() >= 80 {
			b.WriteString("…")
			break
		}
		b.WriteRune(c)
	}
	return b.String()
}

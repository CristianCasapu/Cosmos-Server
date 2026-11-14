// Package reach is a fork addition: it tells, per route of an app, whether it
// can be reached from the internet, as far as that can be checked from inside
// the network. It never connects anywhere: the hostname is resolved through
// the system resolver, the certificate is the one Cosmos would serve, and the
// router's port forwarding is known only from the fork's UPnP status. A real
// test from outside is not possible without a third party and is not done.
package reach

import (
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/upnp"
	"github.com/azukaar/cosmos-server/src/utils"
)

// The verdict on a route.
const (
	StateOff        = "off"        // the URL is disabled
	StateLocal      = "local"      // a local address: refused to visitors from outside
	StateLimited    = "limited"    // only listed addresses, or Constellation only
	StateProblem    = "problem"    // exposed, but a check failed
	StateUnverified = "unverified" // exposed, nothing failed, something could not be checked
	StateVerified   = "verified"   // exposed, everything that can be checked from here is right
)

// The outcome of one check.
const (
	CheckOK      = "ok"
	CheckProblem = "problem"
	CheckUnknown = "unknown"
	CheckNote    = "note" // worth saying, does not change the verdict
)

// Check is one thing that was (or could not be) checked. Code says what, in a
// form the UI turns into a sentence; Params fill it in.
type Check struct {
	ID     string            `json:"id"` // access, dns, forward, cert
	Status string            `json:"status"`
	Code   string            `json:"code"`
	Params map[string]string `json:"params,omitempty"`
}

// RouteResult is the answer for one route.
type RouteResult struct {
	Route  string  `json:"route"`
	Host   string  `json:"host"`
	State  string  `json:"state"`
	URL    string  `json:"url,omitempty"`
	Checks []Check `json:"checks"`
}

// DNSAnswer is what the system resolver said about a hostname.
type DNSAnswer struct {
	Addresses []net.IP
	NotFound  bool   // the name does not exist / has no address
	Failed    bool   // no answer in time, or the resolver failed
	Skipped   string // not looked up: "badName" or "tooMany"
}

// CertResult is what the certificate served for a hostname is worth.
type CertResult struct {
	Valid    bool
	Problem  string // selfSigned, wrongName, expired, none
	NotAfter time.Time
}

// Env is everything a verdict depends on besides the route itself.
type Env struct {
	HTTP     utils.HTTPConfig
	PublicIP string   // the server's public IPv4, "" when unknown
	OwnIPs   []net.IP // the server's own global addresses (IPv6 at home)
	UPnP     upnp.Status
	HTTPOnly func(host string) bool // the host is served over plain HTTP
	DNS      func(host string) DNSAnswer
	Cert     func(host string) CertResult
	// Fork addition (Cloudflare): what is known about a hostname's record at
	// Cloudflare and whether the admin serves it through Cloudflare's proxy,
	// and whether an address is one of Cloudflare's. Both may be nil.
	Cloudflare   func(host string) CloudflareInfo
	IsCloudflare func(ip net.IP) bool
}

// CloudflareInfo is what the Cloudflare integration knows about a hostname.
type CloudflareInfo struct {
	// Shielded: the admin serves the hostname through Cloudflare's proxy
	Shielded bool
	// Managed: Cosmos manages the hostname's record at Cloudflare
	Managed bool
	// RecordState: ok, pending, conflict, protected or error; RecordReason
	// the kind of an error
	RecordState  string
	RecordReason string
}

// Filled in by the main package (fork_cloudflare.go): reach does not import
// the Cloudflare integration.
var (
	Cloudflare          func(host string) CloudflareInfo
	IsCloudflareAddress func(ip net.IP) bool
)

// splitHost separates "name:port"; port is 0 without one.
func splitHost(host string) (string, int) {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, p, err := net.SplitHostPort(host); err == nil {
		port, _ := strconv.Atoi(p)
		return strings.TrimSuffix(strings.Trim(h, "[]"), "."), port
	}
	return strings.TrimSuffix(strings.Trim(host, "[]"), "."), 0
}

// isLocalAddress mirrors isLocalAddress in src/proxy/fork_local.go: the hosts
// the local guard refuses to visitors from outside.
func isLocalAddress(name string) bool {
	if name == "" {
		return false
	}
	if strings.HasSuffix(name, ".local") {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast())
}

// IsPublicIP reports whether an address can be reached from the internet at
// all: not private, loopback, link-local, carrier NAT or unspecified.
func IsPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	return !carrierNAT.Contains(ip)
}

var carrierNAT = func() *net.IPNet {
	_, network, _ := net.ParseCIDR("100.64.0.0/10")
	return network
}()

// ValidHostname reports whether a name can be looked up in DNS: letters,
// digits and hyphens in labels of up to 63 characters, 253 in all, at least
// one dot. IP addresses are not names.
func ValidHostname(name string) bool {
	if len(name) == 0 || len(name) > 253 || !strings.Contains(name, ".") || net.ParseIP(name) != nil {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// RouteHost is the address a route answers on: its own host, or Cosmos' own
// hostname for a route without one.
func RouteHost(route utils.ProxyRouteConfig, http utils.HTTPConfig) string {
	if route.UseHost && strings.TrimSpace(route.Host) != "" {
		return strings.ToLower(strings.TrimSpace(route.Host))
	}
	return strings.ToLower(strings.TrimSpace(http.Hostname))
}

// NeedsDNS reports which hostname, if any, the verdict on a route looks up.
func NeedsDNS(route utils.ProxyRouteConfig, http utils.HTTPConfig) string {
	name, _ := splitHost(RouteHost(route, http))
	if route.Disabled || isLocalAddress(name) || route.RestrictToConstellation || len(route.WhitelistInboundIPs) > 0 {
		return ""
	}
	if net.ParseIP(name) != nil || name == "" {
		return ""
	}
	return name
}

func portOf(value string, fallback int) int {
	if port, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && port > 0 && port < 65536 {
		return port
	}
	return fallback
}

func ipIn(ip net.IP, list []net.IP) bool {
	for _, other := range list {
		if other.Equal(ip) {
			return true
		}
	}
	return false
}

// Evaluate gives the verdict on one route.
func Evaluate(route utils.ProxyRouteConfig, env Env) RouteResult {
	host := RouteHost(route, env.HTTP)
	name, routePort := splitHost(host)
	result := RouteResult{Route: route.Name, Host: host, Checks: []Check{}}
	add := func(id, status, code string, params map[string]string) {
		result.Checks = append(result.Checks, Check{ID: id, Status: status, Code: code, Params: params})
	}

	switch {
	case route.Disabled:
		result.State = StateOff
		add("access", CheckNote, "disabled", nil)
		return result
	case name == "":
		result.State = StateLocal
		add("access", CheckNote, "noHost", nil)
		return result
	case isLocalAddress(name):
		result.State = StateLocal
		add("access", CheckNote, "localGuard", nil)
		return result
	case route.RestrictToConstellation:
		result.State = StateLimited
		add("access", CheckNote, "constellation", nil)
		return result
	case len(route.WhitelistInboundIPs) > 0:
		result.State = StateLimited
		add("access", CheckNote, "whitelist", map[string]string{"count": strconv.Itoa(len(route.WhitelistInboundIPs))})
		return result
	}

	switch {
	case route.AdminOnly:
		add("access", CheckNote, "adminOnly", nil)
	case route.AuthEnabled:
		add("access", CheckNote, "login", nil)
	default:
		add("access", CheckOK, "open", nil)
	}

	// --- the record at Cloudflare, when Cosmos manages it
	cf := CloudflareInfo{}
	if env.Cloudflare != nil && net.ParseIP(name) == nil {
		cf = env.Cloudflare(name)
	}
	if cf.Managed {
		switch cf.RecordState {
		case "ok":
			add("record", CheckOK, "exists", nil)
		case "conflict", "protected":
			// a record Cosmos did not create and leaves alone: whether it
			// points here is what the next check says
			add("record", CheckNote, "foreign", nil)
		case "error":
			reason := cf.RecordReason
			switch reason {
			case "invalid_token", "malformed_token", "forbidden", "not_found", "rate_limited", "unreachable":
			default:
				reason = "other"
			}
			code := "error_" + reason
			add("record", CheckProblem, code, nil)
		default:
			add("record", CheckUnknown, "pending", nil)
		}
	}

	// --- does the name point at this server?
	onlyIPv6 := false
	if ip := net.ParseIP(name); ip != nil {
		switch {
		case !IsPublicIP(ip):
			add("dns", CheckProblem, "ipNotPublic", map[string]string{"address": ip.String()})
		case ip.String() == env.PublicIP || ipIn(ip, env.OwnIPs):
			add("dns", CheckOK, "ipIsServer", map[string]string{"address": ip.String()})
			onlyIPv6 = ip.To4() == nil
		case env.PublicIP == "":
			add("dns", CheckUnknown, "ipPublicIPUnknown", map[string]string{"address": ip.String()})
		default:
			add("dns", CheckProblem, "ipElsewhere", map[string]string{"address": ip.String(), "publicIP": env.PublicIP})
		}
	} else {
		answer := env.DNS(name)
		public, private := []net.IP{}, []net.IP{}
		for _, ip := range answer.Addresses {
			if IsPublicIP(ip) {
				public = append(public, ip)
			} else {
				private = append(private, ip)
			}
		}
		var matchV4, matchV6 net.IP
		for _, ip := range public {
			if ip.To4() != nil && ip.String() == env.PublicIP && matchV4 == nil {
				matchV4 = ip
			} else if ip.To4() == nil && ipIn(ip, env.OwnIPs) && matchV6 == nil {
				matchV6 = ip
			}
		}
		// answered by Cloudflare's proxy: every public address is one of its own
		viaCloudflare := len(public) > 0 && env.IsCloudflare != nil
		for _, ip := range public {
			viaCloudflare = viaCloudflare && env.IsCloudflare(ip)
		}
		switch {
		case answer.Skipped != "":
			add("dns", CheckUnknown, answer.Skipped, nil)
		case answer.Failed:
			add("dns", CheckUnknown, "lookupFailed", nil)
		case answer.NotFound || len(answer.Addresses) == 0:
			add("dns", CheckProblem, "noRecord", map[string]string{"name": name})
		case cf.Shielded && viaCloudflare:
			add("dns", CheckOK, "throughShield", nil)
		case cf.Shielded && (matchV4 != nil || matchV6 != nil):
			// the switch was just flipped, or the record was changed at
			// Cloudflare: visitors who get this answer are refused
			add("dns", CheckUnknown, "shieldNotYet", nil)
		case viaCloudflare:
			add("dns", CheckUnknown, "behindCloudflare", nil)
		case matchV4 != nil:
			add("dns", CheckOK, "pointsHere", map[string]string{"address": matchV4.String()})
		case matchV6 != nil:
			add("dns", CheckOK, "pointsHere", map[string]string{"address": matchV6.String()})
			onlyIPv6 = true
		case len(public) == 0:
			// split DNS: the local resolver answers with the LAN address, so
			// what the internet gets cannot be seen from here
			add("dns", CheckUnknown, "localAnswer", map[string]string{"address": private[0].String()})
		case env.PublicIP == "":
			add("dns", CheckUnknown, "publicIPUnknown", map[string]string{"address": public[0].String()})
		default:
			add("dns", CheckProblem, "pointsElsewhere", map[string]string{"address": public[0].String(), "publicIP": env.PublicIP})
		}
	}

	// --- does the router forward the port?
	httpOnly := env.HTTPOnly != nil && env.HTTPOnly(name)
	httpsPort, httpPort := portOf(env.HTTP.HTTPSPort, 443), portOf(env.HTTP.HTTPPort, 80)
	cosmosPort := httpsPort
	if httpOnly {
		cosmosPort = httpPort
	}
	port := cosmosPort
	ownPort := routePort != 0 && routePort != httpsPort && routePort != httpPort
	if routePort != 0 {
		port = routePort
	}
	// Fork addition (router port forwarding): a host that carries a port only
	// answers requests for that port. With Cosmos' port forwarded on another
	// external port, the host must carry the external one (or none).
	var cosmosMapping *upnp.Mapping
	for i := range env.UPnP.Mappings {
		if env.UPnP.Mappings[i].InternalPort == cosmosPort && env.UPnP.Mappings[i].Active {
			cosmosMapping = &env.UPnP.Mappings[i]
		}
	}
	moved := cosmosMapping != nil && cosmosMapping.ExternalPort != cosmosPort
	hostPortMismatch := moved && routePort == cosmosPort
	if moved && routePort == cosmosMapping.ExternalPort {
		ownPort, port = false, cosmosPort
	}
	externalPort := port
	ports := map[string]string{"port": strconv.Itoa(port)}
	switch {
	case hostPortMismatch:
		add("forward", CheckProblem, "hostPort", map[string]string{"port": strconv.Itoa(cosmosPort), "external": strconv.Itoa(cosmosMapping.ExternalPort)})
	case ownPort:
		add("forward", CheckUnknown, "ownPort", ports)
	case onlyIPv6:
		add("forward", CheckUnknown, "ipv6Firewall", ports)
	case !env.UPnP.Enabled:
		add("forward", CheckUnknown, "byHand", ports)
	case env.UPnP.Warning != "":
		add("forward", CheckProblem, "routerNotPublic", map[string]string{"router": env.UPnP.ExternalIP})
	default:
		var mapping *upnp.Mapping
		for i := range env.UPnP.Mappings {
			if env.UPnP.Mappings[i].InternalPort == port {
				mapping = &env.UPnP.Mappings[i]
			}
		}
		switch {
		case mapping == nil:
			add("forward", CheckUnknown, "byHand", ports)
		case mapping.Active:
			externalPort = mapping.ExternalPort
			add("forward", CheckOK, "upnp", map[string]string{"port": strconv.Itoa(port), "external": strconv.Itoa(mapping.ExternalPort)})
		default:
			reason := mapping.Error
			if reason == "" {
				reason = env.UPnP.Error
			}
			add("forward", CheckProblem, "upnpFailed", map[string]string{"port": strconv.Itoa(port), "error": reason})
		}
	}

	// --- through Cloudflare's proxy: only on a port it proxies
	if cf.Shielded {
		if utils.IsCloudflareHTTPSPort(externalPort) && !ownPort {
			add("shield", CheckNote, "on", nil)
		} else {
			add("shield", CheckProblem, "port", map[string]string{"port": strconv.Itoa(externalPort)})
		}
	}

	// --- the certificate
	scheme := "https"
	if httpOnly {
		scheme = "http"
		add("cert", CheckNote, "plainHTTP", nil)
	} else if net.ParseIP(name) != nil {
		add("cert", CheckProblem, "ipAddress", nil)
	} else {
		cert := env.Cert(name)
		if cert.Valid {
			add("cert", CheckOK, "valid", map[string]string{"until": cert.NotAfter.UTC().Format(time.RFC3339)})
		} else {
			problem := cert.Problem
			if problem == "" {
				problem = "none"
			}
			add("cert", CheckProblem, problem, nil)
		}
	}

	result.State = StateVerified
	for _, check := range result.Checks {
		if check.Status == CheckProblem {
			result.State = StateProblem
			break
		}
		if check.Status == CheckUnknown {
			result.State = StateUnverified
		}
	}

	if result.State == StateVerified || result.State == StateUnverified {
		address := name
		if strings.Contains(name, ":") {
			address = "[" + name + "]"
		}
		if !(scheme == "https" && externalPort == 443) && !(scheme == "http" && externalPort == 80) {
			address += ":" + strconv.Itoa(externalPort)
		}
		path := ""
		if route.UsePathPrefix && strings.HasPrefix(route.PathPrefix, "/") {
			path = route.PathPrefix
		}
		result.URL = scheme + "://" + address + path
	}
	return result
}

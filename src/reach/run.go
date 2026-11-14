package reach

import (
	"context"
	"errors"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/dnsrecords"
	"github.com/azukaar/cosmos-server/src/upnp"
	"github.com/azukaar/cosmos-server/src/utils"
)

// Bounds of a run. Variables so the tests can shorten them.
var (
	cacheTTL      = 10 * time.Minute // a hostname is looked up again after this
	lookupTimeout = 3 * time.Second  // one lookup
	runTimeout    = 10 * time.Second // all the lookups of a run
	parallel      = 4                // lookups at the same time
	maxHosts      = 200              // hostnames looked up per run, the rest is "tooMany"
	forceGap      = 15 * time.Second // "check again" at most this often
	lookedUpTTL   = 6 * time.Hour    // how long a looked-up public address is used
)

// Replaced by the tests: nothing here may reach a real resolver or service.
var (
	resolveHost = func(ctx context.Context, host string) ([]net.IP, error) {
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		ips := make([]net.IP, 0, len(addrs))
		for _, addr := range addrs {
			ips = append(ips, addr.IP)
		}
		return ips, err
	}
	detectPublicIP = dnsrecords.DetectPublicIP
	dynDNSAddress  = dnsrecords.CurrentAddress
	upnpStatus     = upnp.GetStatus
	ownAddresses   = func() []net.IP {
		ips := []net.IP{}
		if addrs, err := net.InterfaceAddrs(); err == nil {
			for _, addr := range addrs {
				if network, ok := addr.(*net.IPNet); ok && IsPublicIP(network.IP) {
					ips = append(ips, network.IP)
				}
			}
		}
		return ips
	}
	clock = time.Now
)

type dnsEntry struct {
	answer DNSAnswer
	at     time.Time
}

var (
	runMu      sync.Mutex // one run at a time; the others wait and share the cache
	cache      = map[string]dnsEntry{}
	lastForced time.Time
	lookedUp   struct {
		ip string
		at time.Time
	}
	lookups int // lookups done, for the tests
)

// Response is what the endpoint returns.
type Response struct {
	Routes         []RouteResult `json:"routes"`
	PublicIP       string        `json:"publicIP,omitempty"`
	PublicIPSource string        `json:"publicIPSource"` // upnp, dyndns, lookup, or "" when unknown
	CheckedAt      time.Time     `json:"checkedAt"`
	TooSoon        bool          `json:"tooSoon,omitempty"`
	LookupFailed   bool          `json:"lookupFailed,omitempty"`
}

// publicAddress is the server's public IPv4 as far as Cosmos knows it without
// asking anyone new: the router's (UPnP), the one DynDNS advertises, or one
// an admin had looked up.
func publicAddress(st upnp.Status) (string, string) {
	if st.Enabled || len(st.Apps) > 0 {
		if ip := net.ParseIP(st.ExternalIP); ip != nil && ip.To4() != nil && IsPublicIP(ip) {
			return ip.To4().String(), "upnp"
		}
	}
	if ip := net.ParseIP(dynDNSAddress()); ip != nil && ip.To4() != nil && IsPublicIP(ip) {
		return ip.To4().String(), "dyndns"
	}
	if lookedUp.ip != "" && clock().Sub(lookedUp.at) < lookedUpTTL {
		return lookedUp.ip, "lookup"
	}
	return "", ""
}

func lookup(ctx context.Context, host string) DNSAnswer {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	ips, err := resolveHost(ctx, host)
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return DNSAnswer{NotFound: true}
		}
		return DNSAnswer{Failed: true}
	}
	return DNSAnswer{Addresses: ips}
}

// Options of a run.
type Options struct {
	Force       bool // "check again": look every hostname up again
	AskPublicIP bool // ask the outside services DynDNS uses for the public address
}

// Run checks every app route, from the cache where it is fresh.
func Run(opts Options) Response {
	runMu.Lock()
	defer runMu.Unlock()

	now := clock()
	resp := Response{Routes: []RouteResult{}}
	if opts.Force || opts.AskPublicIP {
		if now.Sub(lastForced) < forceGap {
			resp.TooSoon = true
			opts = Options{}
		} else {
			lastForced = now
		}
	}
	if opts.AskPublicIP {
		ip, err := detectPublicIP()
		if parsed := net.ParseIP(ip); err == nil && parsed != nil && IsPublicIP(parsed) {
			lookedUp.ip, lookedUp.at = parsed.String(), now
		} else {
			resp.LookupFailed = true
		}
	}

	config := utils.GetMainConfig()
	routes := []utils.ProxyRouteConfig{}
	for _, route := range config.HTTPConfig.ProxyConfig.Routes {
		if route.Mode == "SERVAPP" {
			routes = append(routes, route)
		}
	}

	// the hostnames to look up, from the configuration only
	wanted := map[string]DNSAnswer{}
	names := []string{}
	for _, route := range routes {
		name := NeedsDNS(route, config.HTTPConfig)
		if name == "" {
			continue
		}
		if _, seen := wanted[name]; seen {
			continue
		}
		switch {
		case !ValidHostname(name):
			wanted[name] = DNSAnswer{Skipped: "badName"}
		case len(names) >= maxHosts:
			wanted[name] = DNSAnswer{Skipped: "tooMany"}
		default:
			wanted[name] = DNSAnswer{}
			names = append(names, name)
		}
	}

	stale := []string{}
	fresh := map[string]dnsEntry{}
	for _, name := range names {
		if entry, ok := cache[name]; ok && !opts.Force && now.Sub(entry.at) < cacheTTL {
			fresh[name] = entry
		} else {
			stale = append(stale, name)
		}
	}

	if len(stale) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
		var mu sync.Mutex
		var wg sync.WaitGroup
		slots := make(chan struct{}, parallel)
		for _, name := range stale {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				answer := DNSAnswer{Failed: true}
				select {
				case slots <- struct{}{}:
					answer = lookup(ctx, name)
					<-slots
				case <-ctx.Done():
				}
				mu.Lock()
				lookups++
				fresh[name] = dnsEntry{answer: answer, at: clock()}
				mu.Unlock()
			}(name)
		}
		wg.Wait()
		cancel()
	}
	// only the hostnames of the current routes stay cached
	cache = fresh

	resp.CheckedAt = now
	for _, entry := range fresh {
		if entry.at.Before(resp.CheckedAt) {
			resp.CheckedAt = entry.at
		}
	}

	st := upnpStatus()
	resp.PublicIP, resp.PublicIPSource = publicAddress(st)
	env := Env{
		HTTP:         config.HTTPConfig,
		PublicIP:     resp.PublicIP,
		OwnIPs:       ownAddresses(),
		UPnP:         st,
		HTTPOnly:     func(host string) bool { return utils.HostIsHTTPOnly(config, host) },
		Cloudflare:   Cloudflare,          // Fork addition (Cloudflare): the record and the shield of a hostname
		IsCloudflare: IsCloudflareAddress, // nil when the integration is not wired in
		DNS: func(host string) DNSAnswer {
			if entry, ok := fresh[host]; ok {
				return entry.answer
			}
			if answer, ok := wanted[host]; ok && answer.Skipped != "" {
				return answer
			}
			return DNSAnswer{Failed: true}
		},
		Cert: func(host string) CertResult {
			return CheckCertificate(CertificateFor(host), host, clock(), nil)
		},
	}
	for _, route := range routes {
		resp.Routes = append(resp.Routes, Evaluate(route, env))
	}
	sort.SliceStable(resp.Routes, func(i, j int) bool { return resp.Routes[i].Route < resp.Routes[j].Route })
	return resp
}

// hideAddresses takes out the server's public address and what the names
// resolve to, for callers who may read the routes but are not admins.
func hideAddresses(resp Response) Response {
	resp.PublicIP = ""
	routes := make([]RouteResult, len(resp.Routes))
	for i, route := range resp.Routes {
		checks := make([]Check, len(route.Checks))
		for j, check := range route.Checks {
			if check.Params != nil {
				params := map[string]string{}
				for key, value := range check.Params {
					if key != "address" && key != "publicIP" && key != "router" {
						params[key] = value
					}
				}
				check.Params = params
			}
			checks[j] = check
		}
		route.Checks = checks
		routes[i] = route
	}
	resp.Routes = routes
	return resp
}

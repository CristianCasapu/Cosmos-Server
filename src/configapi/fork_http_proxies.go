package configapi

// Fork addition: what the HTTP settings form proposes for "trusted proxies"
// and for the geo-blocking warning, answered by the HTTP check
// (POST /api/config/http-check). Nothing is contacted: the request itself,
// the local GeoLite database and the server's own Docker are all it reads.
//
//   - the connection this very request came through, when it carries a
//     forwarding header and the peer is on this machine or the local network:
//     a reverse proxy in front of Cosmos that is not trusted yet (its
//     visitors all look like the proxy, so blocking one blocks everyone);
//   - running containers of the usual reverse-proxy images, with their
//     addresses on Docker's networks;
//   - whether a domain is at Cloudflare and whether Cosmos already believes
//     Cloudflare's addresses for the apps served through it;
//   - the country of the address the server sees (a public one only: on the
//     local network geo-blocking never applies).

import (
	"net"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

type proxyCandidate struct {
	Name      string   `json:"name"`
	Image     string   `json:"image"`
	Addresses []string `json:"addresses"`
}

type httpCheckExtras struct {
	Country    string           `json:"country"`
	Via        string           `json:"via"`
	ViaTrusted bool             `json:"viaTrusted"`
	Proxies    []proxyCandidate `json:"proxies"`
	Cloudflare struct {
		Connected bool `json:"connected"`
		Trusted   bool `json:"trusted"`
		Ranges    []string `json:"ranges"`
	} `json:"cloudflare"`
}

// the image names (without registry, owner and tag) of common reverse proxies
var proxyImages = map[string]bool{
	"cloudflared": true, "nginx-proxy-manager": true, "traefik": true, "caddy": true,
	"haproxy": true, "swag": true, "nginx": true, "nginx-proxy": true, "npm": true,
	"tailscale": true, "zoraxy": true, "pangolin": true, "newt": true,
}

func proxyImageName(image string) string {
	name := image
	if at := strings.Index(name, "@"); at >= 0 {
		name = name[:at]
	}
	base := path.Base(name)
	if colon := strings.Index(base, ":"); colon >= 0 {
		base = base[:colon]
	}
	return strings.ToLower(base)
}

// proxyContainers is a variable so that the tests need no Docker.
var proxyContainers = func() []proxyCandidate {
	containers, err := docker.ListContainers()
	if err != nil {
		return nil
	}
	found := []proxyCandidate{}
	for _, c := range containers {
		if c.State != "running" || !proxyImages[proxyImageName(c.Image)] {
			continue
		}
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		candidate := proxyCandidate{Name: plainText(name), Image: plainText(proxyImageName(c.Image)), Addresses: []string{}}
		if c.NetworkSettings != nil {
			for _, network := range c.NetworkSettings.Networks {
				if network != nil && net.ParseIP(network.IPAddress) != nil {
					candidate.Addresses = append(candidate.Addresses, network.IPAddress)
				}
			}
		}
		sort.Strings(candidate.Addresses)
		if len(candidate.Addresses) > 8 {
			candidate.Addresses = candidate.Addresses[:8]
		}
		found = append(found, candidate)
		if len(found) >= 8 {
			break
		}
	}
	return found
}

// countryOf is a variable so that the tests need no database.
var countryOf = func(ip string) string {
	code, err := utils.GetIPLocation(ip)
	if err != nil || len(code) != 2 {
		return ""
	}
	return strings.ToUpper(code)
}

func forwarded(req *http.Request) bool {
	return req.Header.Get("X-Forwarded-For") != "" || req.Header.Get("Forwarded") != "" || req.Header.Get("X-Real-Ip") != ""
}

func httpCheckExtrasFor(req *http.Request, config utils.Config) httpCheckExtras {
	var extras httpCheckExtras
	client, remote := requestAddresses(req)
	if ip := net.ParseIP(client); ip != nil && !utils.IsLocalIP(client) {
		extras.Country = countryOf(client)
	}
	if client != remote {
		extras.Via, extras.ViaTrusted = remote, true
	} else if forwarded(req) && net.ParseIP(remote) != nil && utils.IsLocalIP(remote) {
		// a proxy on this machine or the local network that is not trusted yet;
		// the address proposed is the connection's, never a header's
		extras.Via = remote
	}
	extras.Proxies = proxyContainers()
	if extras.Proxies == nil {
		extras.Proxies = []proxyCandidate{}
	}
	for _, zone := range config.HTTPConfig.DNSZones {
		if strings.EqualFold(zone.DNSChallengeProvider, "cloudflare") {
			extras.Cloudflare.Connected = true
		}
	}
	cf := config.HTTPConfig.Cloudflare
	extras.Cloudflare.Trusted = cf.TrustRanges
	v4, v6, _ := utils.CloudflareRangeLists(cf)
	extras.Cloudflare.Ranges = append(append([]string{}, v4...), v6...)
	return extras
}

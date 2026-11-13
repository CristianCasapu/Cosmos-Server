package upnp

import (
	"github.com/azukaar/cosmos-server/src/utils"
)

// ExternalHTTPSPort answers: on which port is Cosmos' HTTPS port reached
// from the internet? As mapped (the external port of the active mapping),
// else as configured (the external port asked for while automatic
// forwarding is on), else the HTTPS port itself (forwarded by hand, or not
// at all). 0 only when the HTTPS port in the settings is not a port.
//
// This is what every external link is built with, and what the Cloudflare
// integration checks against the ports Cloudflare passes.
func ExternalHTTPSPort() int {
	https, _ := externalPorts(utils.GetMainConfig().HTTPConfig, GetStatus().Mappings)
	return https
}

// ExternalHTTPPort is the same for the plain HTTP port.
func ExternalHTTPPort() int {
	_, http := externalPorts(utils.GetMainConfig().HTTPConfig, GetStatus().Mappings)
	return http
}

func externalPorts(cfg utils.HTTPConfig, mappings []Mapping) (https int, http int) {
	one := func(name string, internal string, external string, asked bool) int {
		for _, m := range mappings {
			if m.Name == name && m.Kind != "app" && m.Active && m.ExternalPort != 0 {
				return m.ExternalPort
			}
		}
		if asked {
			if port, err := parsePort(external); err == nil {
				return port
			}
		}
		port, _ := parsePort(internal)
		return port
	}
	https = one("HTTPS", cfg.HTTPSPort, cfg.UPnP.ExternalHTTPSPort, cfg.UPnP.Enabled)
	http = one("HTTP", cfg.HTTPPort, cfg.UPnP.ExternalHTTPPort, cfg.UPnP.Enabled && cfg.UPnP.ForwardHTTP)
	return https, http
}

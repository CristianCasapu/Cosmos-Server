package docker

// Fork addition (plan 44): a new URL of an install gets what its known app needs.

import (
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestNewURLOfAKnownAppIsToldItsOwnAddress(t *testing.T) {
	old := KnownRouteSetting
	KnownRouteSetting = func(image string) (bool, string) {
		if strings.Contains(image, "qbittorrent") {
			return true, "qBittorrent"
		}
		return false, ""
	}
	defer func() { KnownRouteSetting = old }()
	spec := DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{
		"qbittorrent": {Image: "lscr.io/linuxserver/qbittorrent:latest", Routes: []utils.ProxyRouteConfig{{Name: "qbittorrent", Mode: "SERVAPP", UseHost: true, Host: "qbittorrent.server.local", Target: "http://qbittorrent:8080"}}},
		"radarr":      {Image: "lscr.io/linuxserver/radarr:latest", Routes: []utils.ProxyRouteConfig{{Name: "radarr", Mode: "SERVAPP", UseHost: true, Host: "radarr.server.local", Target: "http://radarr:7878"}}},
	}}
	logged := ""
	existing := []utils.ProxyRouteConfig{{Name: "radarr", Mode: "SERVAPP", Host: "radarr.server.local", Target: "http://radarr:7878"}}
	routes, _ := MergeComposeRoutes(existing, spec, func(line string) { logged += line })
	for _, r := range routes {
		if r.Name == "qbittorrent" && !r.ForkAppOwnHost {
			t.Error("qBittorrent's new URL was not told its own address")
		}
		if r.Name == "radarr" && r.ForkAppOwnHost {
			t.Error("Radarr was told its own address")
		}
	}
	if !strings.Contains(logged, "tells qBittorrent its own address") {
		t.Errorf("the install's log does not say it: %q", logged)
	}
}

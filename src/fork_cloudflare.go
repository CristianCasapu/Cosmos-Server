package main

// Fork addition: wires the guided Cloudflare integration (src/cloudflare)
// into the server: its routes, and what the reachability check learns from
// it.

import (
	"net"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/cloudflare"
	"github.com/azukaar/cosmos-server/src/reach"
	"github.com/azukaar/cosmos-server/src/utils"
)

func init() {
	reach.Cloudflare = func(host string) reach.CloudflareInfo {
		info := reach.CloudflareInfo{Shielded: utils.IsShieldedHost(host)}
		if record, ok := cloudflare.RecordOf(host); ok {
			info.Managed = true
			info.RecordState = record.State
			info.RecordReason = record.Reason
		}
		return info
	}
	reach.IsCloudflareAddress = func(ip net.IP) bool { return utils.IsCloudflareIP(ip.String()) }
}

// registerCloudflareRoutes adds the routes to the router of the admin API
// (same-origin check, token and session checks as its neighbours).
func registerCloudflareRoutes(router *mux.Router) {
	router.HandleFunc("/api/cloudflare", cloudflare.StatusRoute)
	router.HandleFunc("/api/cloudflare/check", cloudflare.CheckRoute)
	router.HandleFunc("/api/cloudflare/preview", cloudflare.PreviewRoute)
	router.HandleFunc("/api/cloudflare/takeover", cloudflare.TakeOverRoute)
	router.HandleFunc("/api/cloudflare/shield", cloudflare.ShieldRoute)
	router.HandleFunc("/api/cloudflare/panel", cloudflare.PanelRoute)
	router.HandleFunc("/api/cloudflare/ranges", cloudflare.RangesRoute)
	router.HandleFunc("/api/cloudflare/origin-pulls", cloudflare.OriginPullsRoute)
	router.HandleFunc("/api/expose", cloudflare.ExposeRoute)
	cloudflare.Start()
}

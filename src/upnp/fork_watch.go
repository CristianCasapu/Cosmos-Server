package upnp

// Fork addition: the reachability watch (src/reachwatch) asks the router
// module to look at once when Cloudflare cannot connect to the server: the
// look that otherwise runs every five minutes (retry.go) happens now, and a
// forward the router lost is made again before the watch confirms the
// failure. The same pass as "Check now" in the panel.

// CheckNow lifts the pauses and brings the router's forwards in line with
// the settings now. It returns the status after the pass.
func CheckNow() Status {
	return reconcile(true)
}

// HoldsCosmosForward reports whether the router module is set to keep a
// forward of Cosmos' own HTTPS port, and that forward as it stands.
func HoldsCosmosForward(st Status) (Mapping, bool) {
	if !st.Enabled {
		return Mapping{}, false
	}
	for _, m := range st.Mappings {
		if m.Name == "HTTPS" {
			return m, true
		}
	}
	return Mapping{}, false
}

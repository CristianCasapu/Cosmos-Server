package proxy

// Fork addition: keeps the published .local names alive.
//
// Upstream published the .local names once, when Cosmos started or the list
// of names changed. Avahi forgets them when its daemon restarts (a package
// upgrade, a network reconfiguration); the server's address may change
// (DHCP); Avahi may not be running yet when Cosmos starts. The watch below
// reconciles once a minute (fork_mdns.go), which covers all of these.

import (
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const mdnsWatchInterval = time.Minute

var mdnsWatchOnce sync.Once

// startMDNSWatch starts the watch; later calls do nothing.
func startMDNSWatch() {
	mdnsWatchOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(mdnsWatchInterval)
			defer ticker.Stop()
			for range ticker.C {
				checkMDNS()
			}
		}()
	})
}

// avahiRunning reports whether an Avahi daemon is on the system bus, without
// starting one.
func avahiRunning() bool {
	conn, err := dbus.SystemBus()
	if err != nil {
		return false
	}
	var owned bool
	err = conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, "org.freedesktop.Avahi").Store(&owned)
	return err == nil && owned
}

// checkMDNS publishes what changed: names Avahi lost, a new address, a name
// whose collision is due for another try. With publishing switched off it
// withdraws the names.
func checkMDNS() {
	localNames.reconcile(false)
}

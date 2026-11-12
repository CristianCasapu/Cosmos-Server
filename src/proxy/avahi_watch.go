package proxy

// Fork addition: keeps the published .local names alive.
//
// Cosmos publishes each .local hostname as an alias of the machine's own
// Avahi name, once, when it starts or when the list of names changes. Avahi
// forgets those aliases when its daemon restarts (a package upgrade, a
// network reconfiguration), and they point at the wrong name when Avahi
// renames the machine after a name conflict ("host-2.local"). Either way
// every .local URL stops resolving until Cosmos itself is restarted. The
// same happens when Avahi was not running yet when Cosmos started.
//
// The watch below looks at the published aliases once a minute and publishes
// them again when they are gone or stale.

import (
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/holoplot/go-avahi"
	"github.com/miekg/dns"

	"github.com/azukaar/cosmos-server/src/utils"
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

// lost says why the published names are no longer valid, or "" if they are.
func (p *Publisher) lost() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	fqdn, err := p.avahiServer.GetHostNameFqdn()
	if err != nil {
		return "Avahi does not answer"
	}
	if dns.Fqdn(fqdn) != p.fqdn {
		return "Avahi renamed this machine from " + p.fqdn + " to " + dns.Fqdn(fqdn)
	}

	for _, group := range p.entryGroups {
		state, err := group.GetState()
		if err != nil {
			// The daemon restarted: it no longer knows the entries it was given.
			return "Avahi no longer has the published names"
		}
		if state == avahi.EntryGroupCollision || state == avahi.EntryGroupFailure {
			// Publishing again would fail the same way; say so once instead.
			if !p.warnedRejected {
				p.warnedRejected = true
				utils.Warn("[mDNS] Avahi rejected some .local names, most likely because another machine on the network already uses them")
			}
		}
	}
	return ""
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

// checkMDNS publishes the .local names again if Avahi lost them, or if Avahi
// was not available when they were first published.
func checkMDNS() {
	if !utils.GetMainConfig().HTTPConfig.PublishMDNS {
		return
	}

	publisherMu.Lock()
	p := publisher
	publisherMu.Unlock()

	if p == nil {
		if avahiRunning() {
			utils.Log("[mDNS] Avahi is available now, publishing the .local names")
			PublishAllMDNSFromConfig()
		}
		return
	}

	reason := p.lost()
	if reason == "" {
		return
	}
	utils.Warn("[mDNS] " + reason + ", publishing the .local names again")

	publisherMu.Lock()
	if publisher == p {
		p.Close()
		publisher = nil
	}
	publisherMu.Unlock()

	if avahiRunning() {
		PublishAllMDNSFromConfig()
	}
}

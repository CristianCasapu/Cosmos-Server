package proxy

// Fork addition: the .local names of the routes, published so that the other
// machines of the local network can resolve them.
//
// Upstream published each name as a CNAME pointing at the machine's own Avahi
// name, on every interface. Only Apple's resolver follows such an alias over
// mDNS reliably; Windows, Android, systemd-resolved and most other resolvers
// ask for an address and get nothing usable. So each name now gets an address
// record (A) of its own, with the server's address on each interface that
// faces the local network (never a Docker bridge, a veth pair or a VPN):
// every mDNS resolver answers that.
//
// Each name has an entry group of its own, published unique: Avahi probes the
// network first and reports a collision when another machine already answers
// for the name. The name is then left unpublished (no silent "name-2") and
// the panel says so; it is tried again on the next settings save and every
// ten minutes. The set is reconciled on every start and save and once a
// minute: a changed address (DHCP), a new or vanished interface, an Avahi
// restart or Avahi appearing later are all picked up.
//
// Everything goes through Avahi's D-Bus API, on a connection of its own.

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/holoplot/go-avahi"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	mdnsMaxNames              = 256              // names published at most (Avahi limits objects per client)
	mdnsCollisionRetry        = 10 * time.Minute // a taken name is tried again after this
	mdnsAvahiPublishNoReverse = 16               // AVAHI_PUBLISH_NO_REVERSE: the reverse lookup stays the host's own name
)

// What the publisher needs from Avahi; the tests replace it.
type mdnsAvahi interface {
	HostFqdn() (string, error)
	NewGroup() (mdnsGroup, error)
	Close()
}

type mdnsGroup interface {
	AddAddress(iface int32, name, address string) error
	AddService(iface int32, name string, port uint16) error
	Commit() error
	Reset() error
	State() (int32, error)
	Free()
}

var (
	// mdnsConnect opens a connection to Avahi; mdnsAvahiOnBus tells whether
	// an Avahi daemon is on the system bus at all.
	mdnsConnect    = connectAvahi
	mdnsAvahiOnBus = avahiRunning
	mdnsNow        = time.Now
)

// The state of one published name.
const (
	nameRegistering = "registering" // Avahi is probing the network
	namePublished   = "published"
	nameConflict    = "conflict" // another machine answers for it
	nameFailed      = "failed"
	nameTooMany     = "tooMany"
	nameNoInterface = "noInterface" // no interface to publish it on
	nameHostName    = "hostName"    // it is the machine's own Avahi name: Avahi answers it already
)

type mdnsEntry struct {
	group      mdnsGroup
	spec       string // the interfaces and addresses it was published with
	state      string
	detail     string // Avahi's words when it refused the name
	collidedAt time.Time
}

type localNamesPublisher struct {
	mu       sync.Mutex
	avahi    mdnsAvahi
	hostFqdn string // the machine's Avahi name, without the final dot
	avahiErr string // "", "off", "absent"
	entries  map[string]*mdnsEntry
	order    []string // the wanted names, in the order of the status
	ifaces   []lanInterface
	problems []mdnsProblem
	at       time.Time
}

var localNames = &localNamesPublisher{entries: map[string]*mdnsEntry{}}

// wantedLocalNames returns the .local names Cosmos serves, sorted, unique,
// lower case: the setup name during the wizard.
func wantedLocalNames(config utils.Config) []string {
	if config.NewInstall {
		return []string{"setup-cosmos.local"}
	}
	seen := map[string]bool{}
	names := []string{}
	for _, host := range utils.GetAllHostnames(true) {
		host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
		if !strings.HasSuffix(host, ".local") || len(host) <= len(".local") || seen[host] {
			continue
		}
		seen[host] = true
		names = append(names, host)
	}
	sort.Strings(names)
	return names
}

// specOf says what a name is published with, to see when it has to change.
func specOf(ifaces []lanInterface) string {
	parts := make([]string, 0, len(ifaces))
	for _, iface := range ifaces {
		parts = append(parts, strconv.Itoa(iface.Index)+"="+iface.Address)
	}
	return strings.Join(parts, ",")
}

// reconcile brings what Avahi publishes in line with the configuration.
// `retry` also tries again the names another machine took (a settings save).
func (p *localNamesPublisher) reconcile(retry bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reconcileLocked(retry)
}

func (p *localNamesPublisher) reconcileLocked(retry bool) {
	config := utils.GetMainConfig()
	p.at = mdnsNow()

	if !config.HTTPConfig.PublishMDNS {
		p.withdrawAll()
		p.avahiErr, p.order, p.ifaces, p.problems = "off", nil, nil, nil
		return
	}

	wanted := wantedLocalNames(config)
	p.order = wanted
	p.ifaces, p.problems = chooseLANInterfaces(config.HTTPConfig.MDNSInterfaces)

	if !p.connect() {
		return
	}

	// Avahi forgets everything when its daemon restarts: start afresh then.
	for _, entry := range p.entries {
		if entry.group == nil {
			continue
		}
		if _, err := entry.group.State(); err != nil {
			utils.Warn("[mDNS] Avahi no longer has the published names (restarted?), publishing them again")
			p.drop()
			if !p.connect() {
				return
			}
			break
		}
	}

	keep := map[string]bool{}
	for i, name := range wanted {
		keep[name] = true
		entry := p.entries[name]
		if entry == nil {
			entry = &mdnsEntry{}
			p.entries[name] = entry
		}
		switch {
		case i >= mdnsMaxNames:
			p.withdraw(entry, nameTooMany)
		case name == p.hostFqdn:
			p.withdraw(entry, nameHostName)
		case len(p.ifaces) == 0:
			p.withdraw(entry, nameNoInterface)
		default:
			p.publish(name, entry, retry)
		}
	}
	for name, entry := range p.entries {
		if !keep[name] {
			p.withdraw(entry, "")
			delete(p.entries, name)
		}
	}
}

// connect makes sure there is a connection to Avahi, and says whether there is.
func (p *localNamesPublisher) connect() bool {
	if p.avahi != nil {
		fqdn, err := p.avahi.HostFqdn()
		if err == nil {
			p.hostFqdn = strings.TrimSuffix(strings.ToLower(fqdn), ".")
			p.avahiErr = ""
			return true
		}
		p.drop()
	}
	if !mdnsAvahiOnBus() {
		if p.avahiErr != "absent" {
			utils.Warn("[mDNS] Avahi is not running: the .local names cannot be published (install the avahi-daemon package)")
		}
		p.avahiErr = "absent"
		p.markAll(nameFailed)
		return false
	}
	conn, err := mdnsConnect()
	if err != nil {
		utils.Error("[mDNS] cannot connect to Avahi", err)
		p.avahiErr = "absent"
		p.markAll(nameFailed)
		return false
	}
	fqdn, err := conn.HostFqdn()
	if err != nil {
		conn.Close()
		utils.Error("[mDNS] Avahi does not answer", err)
		p.avahiErr = "absent"
		p.markAll(nameFailed)
		return false
	}
	p.avahi = conn
	p.hostFqdn = strings.TrimSuffix(strings.ToLower(fqdn), ".")
	p.avahiErr = ""
	return true
}

// drop forgets the connection and the groups (Avahi has lost them).
func (p *localNamesPublisher) drop() {
	if p.avahi != nil {
		p.avahi.Close()
		p.avahi = nil
	}
	for _, entry := range p.entries {
		entry.group = nil
		entry.spec = ""
		entry.state = ""
	}
}

func (p *localNamesPublisher) markAll(state string) {
	for _, entry := range p.entries {
		entry.state = state
	}
}

// withdrawAll takes every name off the network and closes the connection.
func (p *localNamesPublisher) withdrawAll() {
	for name, entry := range p.entries {
		p.withdraw(entry, "")
		delete(p.entries, name)
	}
	if p.avahi != nil {
		p.avahi.Close()
		p.avahi = nil
	}
}

func (p *localNamesPublisher) withdraw(entry *mdnsEntry, state string) {
	if entry.group != nil {
		entry.group.Free()
		entry.group = nil
	}
	entry.spec, entry.state, entry.detail = "", state, ""
}

// publish (re)publishes one name when what it should be published with
// changed, when Avahi failed it, or when a collision is due for a retry.
func (p *localNamesPublisher) publish(name string, entry *mdnsEntry, retry bool) {
	spec := specOf(p.ifaces)
	if entry.group != nil && entry.spec == spec {
		state, _ := entry.group.State()
		switch state {
		case avahi.EntryGroupEstablished:
			entry.state, entry.detail = namePublished, ""
			return
		case avahi.EntryGroupRegistering, avahi.EntryGroupUncommited:
			entry.state = nameRegistering
			return
		case avahi.EntryGroupCollision:
			if entry.state != nameConflict {
				entry.collidedAt = mdnsNow()
				utils.Warn("[mDNS] another machine on the network already answers for " + name + ": it is not published (rename the app's address, or the other machine)")
			}
			entry.state = nameConflict
			if !retry && mdnsNow().Sub(entry.collidedAt) < mdnsCollisionRetry {
				return
			}
		}
		// failure, or a collision due for another try: publish it again below
	}

	var err error
	if entry.group != nil {
		err = entry.group.Reset()
		if err != nil {
			entry.group.Free()
			entry.group = nil
		}
	}
	if entry.group == nil {
		entry.group, err = p.avahi.NewGroup()
		if err != nil {
			utils.Error("[mDNS] cannot create an entry group for "+name, err)
			entry.state, entry.spec, entry.detail = nameFailed, "", avahiWords(err)
			return
		}
	}

	port, _ := strconv.Atoi(utils.GetServerPort())
	for _, iface := range p.ifaces {
		if err = entry.group.AddAddress(int32(iface.Index), name, iface.Address); err != nil {
			break
		}
		// the DNS-SD entry upstream published with each name, now on the same interfaces
		if err = entry.group.AddService(int32(iface.Index), name, uint16(port)); err != nil {
			break
		}
	}
	if err == nil {
		err = entry.group.Commit()
	}
	if err != nil {
		entry.group.Reset()
		entry.spec, entry.detail = "", avahiWords(err)
		if isLocalCollision(err) {
			// another program on this machine publishes the name
			if entry.state != nameConflict {
				entry.collidedAt = mdnsNow()
			}
			entry.state = nameConflict
			utils.Warn("[mDNS] " + name + " is already published by another program on this machine")
			return
		}
		entry.state = nameFailed
		utils.Error("[mDNS] Avahi refused "+name, err)
		return
	}
	entry.spec, entry.state, entry.detail = spec, nameRegistering, ""
	utils.Log("[mDNS] publishing " + name + " at " + spec)
}

// isLocalCollision reports Avahi's "Local name collision".
func isLocalCollision(err error) bool {
	var dbusErr dbus.Error
	if errors.As(err, &dbusErr) && dbusErr.Name == "org.freedesktop.Avahi.CollisionError" {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "collision")
}

// avahiWords keeps Avahi's message short and printable for the panel.
func avahiWords(err error) string {
	if err == nil {
		return ""
	}
	return printable(err.Error(), 160)
}

// printable keeps the printable characters of s, at most max of them.
func printable(s string, max int) string {
	out := []rune{}
	for _, r := range s {
		if len(out) >= max {
			break
		}
		if r >= 0x20 && r != 0x7f && !(r >= 0x80 && r < 0xa0) {
			out = append(out, r)
		}
	}
	return string(out)
}

// ForkPublishLocalNames publishes the .local names now; called where upstream
// published its CNAMEs (start and every settings save) and by the watch.
func ForkPublishLocalNames() {
	startMDNSWatch()
	localNames.reconcile(true)
	forgetLocalNamesCheck()
}

// --- the real Avahi -------------------------------------------------------

type realAvahi struct {
	conn   *dbus.Conn
	server *avahi.Server
	mu     sync.Mutex
	groups []*realGroup
}

type realGroup struct {
	server *avahi.Server
	group  *avahi.EntryGroup
	done   chan struct{}
	once   sync.Once
}

// connectAvahi opens a D-Bus connection of its own: closing it never closes
// the shared system bus connection other parts of Cosmos use.
func connectAvahi() (mdnsAvahi, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, err
	}
	server, err := avahi.ServerNew(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &realAvahi{conn: conn, server: server}, nil
}

func (a *realAvahi) HostFqdn() (string, error) {
	return a.server.GetHostNameFqdn()
}

func (a *realAvahi) NewGroup() (mdnsGroup, error) {
	group, err := a.server.EntryGroupNew()
	if err != nil {
		return nil, err
	}
	g := &realGroup{server: a.server, group: group, done: make(chan struct{})}
	a.mu.Lock()
	live := a.groups[:0]
	for _, old := range a.groups {
		select {
		case <-old.done: // freed
		default:
			live = append(live, old)
		}
	}
	a.groups = append(live, g)
	a.mu.Unlock()
	// go-avahi hands every state change to a channel of 10 and blocks its
	// signal loop when nobody reads it: read and drop them (the state is
	// asked for when needed).
	go func() {
		for {
			select {
			case <-group.StateChangeChannel:
			case <-g.done:
				return
			}
		}
	}()
	return g, nil
}

// Close frees the groups (Avahi withdraws their names) and the connection.
func (a *realAvahi) Close() {
	a.server.Close()
	a.conn.Close()
	a.mu.Lock()
	for _, g := range a.groups {
		g.once.Do(func() { close(g.done) })
	}
	a.groups = nil
	a.mu.Unlock()
}

func (g *realGroup) AddAddress(iface int32, name, address string) error {
	return g.group.AddAddress(iface, avahi.ProtoInet, mdnsAvahiPublishNoReverse, name, address)
}

func (g *realGroup) AddService(iface int32, name string, port uint16) error {
	return g.group.AddService(iface, avahi.ProtoInet, 0, name, "_http._tcp", "", "", port, nil)
}

func (g *realGroup) Commit() error { return g.group.Commit() }
func (g *realGroup) Reset() error  { return g.group.Reset() }
func (g *realGroup) State() (int32, error) {
	return g.group.GetState()
}

// Free releases the group; the reading of its states stops after it is gone
// from the library's list, so a state change in flight never blocks.
func (g *realGroup) Free() {
	g.once.Do(func() {
		g.server.EntryGroupFree(g.group)
		close(g.done)
	})
}

// forkLocalNames hands upstream's PublishAllMDNSFromConfig over to the fork.
const forkLocalNames = true

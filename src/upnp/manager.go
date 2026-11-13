package upnp

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	// Mappings are leased, not permanent: if Cosmos stops, the router closes
	// the ports by itself within the hour.
	leaseSeconds  = 3600
	renewInterval = 20 * time.Minute
	// How often what is wanted (the settings, the containers and their
	// published ports) is compared with what is held. The router is only
	// asked when the two differ or a renewal is due.
	checkInterval = time.Minute
)

// The state of a mapping.
const (
	StateActive  = "active"  // the router forwards the port
	StateFailed  = "failed"  // the router was asked and did not do it
	StateWaiting = "waiting" // nothing to forward to right now (see Code)
)

// Mapping is one port forwarded (or to be forwarded) on the router: one of
// Cosmos' own ports (Kind "cosmos", Name "HTTPS" or "HTTP") or a published
// port of a container (Kind "app").
type Mapping struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Container     string `json:"container,omitempty"`
	ContainerPort int    `json:"containerPort,omitempty"`
	Protocol      string `json:"protocol"`
	InternalPort  int    `json:"internalPort"`
	// ExternalPort is the port on the router; WantedPort the one that was
	// asked for. They differ when the wanted one was taken (apps only).
	ExternalPort int    `json:"externalPort"`
	WantedPort   int    `json:"wantedPort"`
	Active       bool   `json:"active"`
	State        string `json:"state"`
	// Code says why, for the UI's sentences: conflict, reserved, refused,
	// moved, stopped, gone, notPublished, localOnly, otherAddress,
	// cosmosPort, dockerUnavailable, router.
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
	// Paused: the router refused this mapping and is not asked again for it
	// until an admin says so. The other mappings are renewed as usual.
	Paused bool `json:"paused,omitempty"`
	// Holder is who has the wanted external port, when the router tells.
	Holder string `json:"holder,omitempty"`
	// Suggested is an external port that looked free, for Cosmos' own ports
	// (they are never moved without being asked).
	Suggested int        `json:"suggested,omitempty"`
	Scheme    string     `json:"scheme,omitempty"`
	Since     *time.Time `json:"since,omitempty"`
}

// Foreign is a forward on the router that Cosmos did not create. Read only:
// Cosmos never changes or deletes one.
type Foreign struct {
	Protocol       string `json:"protocol"`
	ExternalPort   int    `json:"externalPort"`
	InternalClient string `json:"internalClient"`
	InternalPort   int    `json:"internalPort"`
	Description    string `json:"description"`
	LeaseSeconds   int    `json:"leaseSeconds"`
	ThisServer     bool   `json:"thisServer"` // made by another program on this server
}

// Status is the outcome of the last attempt to bring the router in line with
// the configuration.
type Status struct {
	Enabled    bool      `json:"enabled"`
	Gateway    string    `json:"gateway"`
	GatewayIP  string    `json:"gatewayIP"`
	LocalIP    string    `json:"localIP"`
	ExternalIP string    `json:"externalIP"`
	Mappings   []Mapping `json:"mappings"` // Cosmos' own ports
	Apps       []Mapping `json:"apps"`     // the chosen ports of containers
	// Foreign lists what else the router forwards, when it lets its table be
	// read (ForeignListed). Forwards entered in the router's own pages are
	// often not in that table.
	Foreign          []Foreign `json:"foreign"`
	ForeignListed    bool      `json:"foreignListed"`
	ForeignTruncated bool      `json:"foreignTruncated,omitempty"`
	// The port Cosmos' HTTPS (HTTP) port is reached on from outside, as
	// mapped or configured; see ExternalHTTPSPort.
	HTTPSPort         int    `json:"httpsPort"`
	HTTPPort          int    `json:"httpPort"`
	ExternalHTTPSPort int    `json:"externalHTTPSPort"`
	ExternalHTTPPort  int    `json:"externalHTTPPort"`
	Reserved          []int  `json:"reserved"`
	MaxAppPorts       int    `json:"maxAppPorts"`
	Warning           string `json:"warning,omitempty"`
	Error             string `json:"error,omitempty"`
	// Paused is set when the router could not be reached: Cosmos does not
	// try again by itself, only when asked to (or when the server or its
	// settings change). A single mapping the router refused is paused on its
	// own (Mapping.Paused).
	Paused      bool      `json:"paused,omitempty"`
	LastRefresh time.Time `json:"lastRefresh"`
}

// heldMapping is a forward this process created or took over on the router.
type heldMapping struct {
	Target       string // what it is for, for the record: "Cosmos HTTPS" or the container
	Protocol     string
	ExternalPort int
	InternalPort int
	Since        time.Time
}

var (
	runMu  sync.Mutex                 // one reconciliation at a time
	held   = map[string]heldMapping{} // by key: what Cosmos holds on the router
	paused bool                       // the router did not answer: no automatic retry
	// failed: mappings the router refused, by key, with the request that was
	// refused. Not asked again by itself while the request stays the same.
	failed  = map[string]string{}
	missing = map[string]time.Time{} // app choices whose container or port is gone, since when
	// what the last pass wanted and when the router was last asked
	lastPlan       string
	lastRouterTalk time.Time
	lastConfigErr  string

	statusMu sync.Mutex
	status   = Status{Mappings: []Mapping{}, Apps: []Mapping{}, Foreign: []Foreign{}}

	renewOnce sync.Once
	clock     = time.Now
)

// Apply brings the router's port forwards in line with the configuration. It
// returns at once; the work happens in the background. Call it whenever the
// server (re)starts, which is also when a changed configuration takes effect.
func Apply() {
	go reconcile(true)
	renewOnce.Do(func() {
		go func() {
			for range time.Tick(checkInterval) {
				tick()
			}
		}()
	})
}

// tick is the periodic check: it asks the router only when what is wanted
// changed (a container stopped, a published port moved) or a renewal is due.
func tick() {
	runMu.Lock()
	defer runMu.Unlock()
	if paused {
		// A router that did not answer is not asked again until an admin
		// says so; what was forwarded expires by itself.
		return
	}
	cfg := utils.GetMainConfig().HTTPConfig
	if len(held) == 0 && !cfg.UPnP.Enabled && len(loadChoices()) == 0 {
		return
	}
	p := makePlan(cfg, clock())
	if p.signature == lastPlan && len(p.forget) == 0 && clock().Sub(lastRouterTalk) < renewInterval {
		return
	}
	run(p, false)
}

// GetStatus returns the outcome of the last reconciliation.
func GetStatus() Status {
	statusMu.Lock()
	defer statusMu.Unlock()
	return status
}

func parsePort(value string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", value)
	}
	return port, nil
}

// desiredMappings lists what the configuration asks the router to forward of
// Cosmos' own. Only Cosmos' own listening ports, and the plain HTTP port
// only when asked for explicitly.
func desiredMappings(cfg utils.HTTPConfig) ([]Mapping, error) {
	if !cfg.UPnP.Enabled {
		return nil, nil
	}

	build := func(name, internal, external string) (Mapping, error) {
		in, err := parsePort(internal)
		if err != nil {
			return Mapping{}, fmt.Errorf("%s port: %w", name, err)
		}
		out := in
		if strings.TrimSpace(external) != "" {
			if out, err = parsePort(external); err != nil {
				return Mapping{}, fmt.Errorf("external %s port: %w", name, err)
			}
		}
		return Mapping{Name: name, Kind: "cosmos", Protocol: "TCP", InternalPort: in, ExternalPort: out, WantedPort: out}, nil
	}

	wanted := []Mapping{}
	if cfg.HTTPSCertificateMode != utils.HTTPSCertModeList["DISABLED"] {
		m, err := build("HTTPS", cfg.HTTPSPort, cfg.UPnP.ExternalHTTPSPort)
		if err != nil {
			return nil, err
		}
		wanted = append(wanted, m)
	}
	if cfg.UPnP.ForwardHTTP {
		m, err := build("HTTP", cfg.HTTPPort, cfg.UPnP.ExternalHTTPPort)
		if err != nil {
			return nil, err
		}
		wanted = append(wanted, m)
	}

	if len(wanted) == 0 {
		return nil, errors.New("HTTPS is disabled, so there is nothing to forward: enable HTTPS, or choose to forward the HTTP port")
	}
	if len(wanted) == 2 && wanted[0].ExternalPort == wanted[1].ExternalPort {
		return nil, errors.New("HTTP and HTTPS cannot use the same external port")
	}
	return wanted, nil
}

// privateAddressNote explains why a forward on a router whose own WAN address
// is not public will not make the server reachable.
func privateAddressNote(externalIP string) string {
	ip := net.ParseIP(externalIP)
	if ip == nil {
		return ""
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || cgnat.Contains(ip) {
		return "The router's own internet address (" + externalIP + ") is not a public one, so it sits behind another router or the provider's NAT. Forwarding ports here will not make Cosmos reachable from the internet."
	}
	return ""
}

// discoverGateway finds the router. A variable so tests never touch a real one.
var discoverGateway = discover

// notifyFailure tells the admins, once per failure, that a forwarding is not
// in place. A variable so tests can replace it.
var notifyFailure = func(reason string) {
	utils.WriteNotification(utils.Notification{
		Recipient: "admin",
		Title:     "header.notification.title.upnpFailed",
		Message:   "header.notification.message.upnpFailed",
		Vars:      reason,
		Level:     "error",
	})
}

// recordMapping writes a change made on the router to the security record.
// These entries have no actor: they are what Cosmos did, after an
// administrator's choice (which has an entry of its own) or by itself (a
// container stopped, a port was taken).
func recordMapping(kind string, target string, details ...string) {
	event := utils.SecurityEvent{Kind: kind, Target: target, Details: map[string]string{}}
	for i := 0; i+1 < len(details); i += 2 {
		if details[i] == "outcome" {
			event.Outcome = details[i+1]
		} else if details[i+1] != "" {
			event.Details[details[i]] = details[i+1]
		}
	}
	utils.RecordSecurityEvent(event)
}

// want is one mapping the configuration asks for.
type want struct {
	key         string
	mapping     Mapping
	description string
	// eligible: there is something to forward to. Otherwise mapping.State
	// and Code say why not.
	eligible bool
	// bindIP is the one address of this server the port is published on, ""
	// for every address.
	bindIP string
	// auto: may be given another external port when the wanted one is taken
	// (app ports; never Cosmos' own).
	auto     bool
	assigned int // the external port it got last time
	choice   int // index of the app choice, -1 for Cosmos' own
}

// plan is everything a pass needs to know before the router is asked.
type plan struct {
	cfg       utils.HTTPConfig
	cfgErr    error
	wants     []want
	forget    []string // keys of app choices to drop: their container or port is gone for good
	reserved  map[int]bool
	signature string
}

func makePlan(cfg utils.HTTPConfig, now time.Time) plan {
	p := plan{cfg: cfg}
	own, err := desiredMappings(cfg)
	p.cfgErr = err
	p.reserved, _ = reservedPorts(cfg.UPnP.ReservedPorts)
	for _, m := range own {
		p.wants = append(p.wants, want{key: "cosmos:" + m.Name, mapping: m, description: descriptionPrefix + m.Name, eligible: true, choice: -1})
	}
	apps, forget := appWants(loadChoices(), cfg, now)
	p.wants = append(p.wants, apps...)
	p.forget = forget

	parts := []string{}
	for _, w := range p.wants {
		parts = append(parts, fmt.Sprintf("%s|%s|%d|%d|%v|%s|%s", w.key, w.mapping.Protocol, w.mapping.InternalPort, w.mapping.WantedPort, w.eligible, w.mapping.Code, w.bindIP))
	}
	if p.cfgErr != nil {
		parts = append(parts, "error:"+p.cfgErr.Error())
	}
	parts = append(parts, "reserved:"+cfg.UPnP.ReservedPorts)
	p.signature = strings.Join(parts, "\n")
	return p
}

// requestOf is what is asked of the router for a mapping: when the router
// refused exactly this, it is not asked again by itself.
func requestOf(w want) string {
	return fmt.Sprintf("%s|%d|%d|%s", w.mapping.Protocol, w.mapping.InternalPort, w.mapping.WantedPort, w.bindIP)
}

func portKey(protocol string, port int) string {
	return protocol + "/" + strconv.Itoa(port)
}

// reconcile removes forwards that are no longer wanted and creates or renews
// the wanted ones. manual is a pass someone asked for ("Check now", a
// start, a change of the settings): everything that was paused is tried again.
func reconcile(manual bool) Status {
	runMu.Lock()
	defer runMu.Unlock()
	return run(makePlan(utils.GetMainConfig().HTTPConfig, clock()), manual)
}

// run does one pass. The caller holds runMu.
func run(p plan, manual bool) Status {
	now := clock()
	cfg := p.cfg
	_, reservedList := reservedPorts(cfg.UPnP.ReservedPorts)
	st := Status{
		Enabled: cfg.UPnP.Enabled, Mappings: []Mapping{}, Apps: []Mapping{}, Foreign: []Foreign{},
		Reserved: reservedList, MaxAppPorts: maxAppPorts, LastRefresh: now,
	}
	st.HTTPSPort, _ = parsePort(cfg.HTTPSPort)
	st.HTTPPort, _ = parsePort(cfg.HTTPPort)
	if p.cfgErr != nil {
		st.Error = p.cfgErr.Error()
	}
	previous := map[string]Mapping{}
	for _, m := range append(append([]Mapping{}, GetStatus().Mappings...), GetStatus().Apps...) {
		previous[keyOf(m)] = m
	}
	if manual {
		failed = map[string]string{}
	}
	wasPaused := paused
	routerFailed := false
	notices := []string{}

	put := func(m Mapping) {
		if m.State == "" {
			m.State = StateWaiting
		}
		if m.Kind == "app" {
			st.Apps = append(st.Apps, m)
		} else {
			st.Mappings = append(st.Mappings, m)
		}
	}
	publish := func() Status {
		wanted := false
		for _, w := range p.wants {
			wanted = wanted || w.eligible
		}
		paused = routerFailed && wanted
		st.Paused = paused
		if paused && !wasPaused {
			utils.Warn("UPnP: the router could not be set up; not trying again until asked to")
			notifyFailure(st.Error)
		}
		if p.cfgErr != nil && p.cfgErr.Error() != lastConfigErr {
			notifyFailure(p.cfgErr.Error())
		}
		lastConfigErr = ""
		if p.cfgErr != nil {
			lastConfigErr = p.cfgErr.Error()
		}
		for _, notice := range notices {
			notifyFailure(notice)
		}
		st.ExternalHTTPSPort, st.ExternalHTTPPort = externalPorts(cfg, st.Mappings)
		lastPlan = p.signature
		statusMu.Lock()
		status = st
		statusMu.Unlock()
		return st
	}

	// app choices whose container or port has been gone for good are dropped
	if len(p.forget) > 0 {
		forgetChoices(p.forget)
		for _, key := range p.forget {
			delete(missing, key)
			delete(failed, key)
		}
	}
	forgotten := map[string]bool{}
	for _, key := range p.forget {
		forgotten[key] = true
	}

	needRouter := len(held) > 0
	for _, w := range p.wants {
		needRouter = needRouter || w.eligible
	}
	if !needRouter {
		for _, w := range p.wants {
			if !forgotten[w.key] {
				put(w.mapping)
			}
		}
		return publish() // nothing to create and nothing of ours to remove
	}

	g, err := discoverGateway()
	lastRouterTalk = now
	if err != nil {
		routerFailed = true
		st.Error = err.Error()
		for _, w := range p.wants {
			if forgotten[w.key] {
				continue
			}
			m := w.mapping
			if w.eligible {
				m.State, m.Code = StateFailed, "router"
			}
			put(m)
		}
		utils.Error("UPnP: cannot reach the router", err)
		return publish()
	}
	st.Gateway = cleanText(g.name, maxRouterText)
	st.GatewayIP = g.ip.String()
	st.LocalIP = g.localIP.String()
	if ip, err := g.externalIP(); err == nil {
		st.ExternalIP = ip
		st.Warning = privateAddressNote(ip)
	}
	local := g.localIP.String()

	entries, listed, truncated := g.list()

	// --- withdraw what is held and no longer wanted as it is
	wantByKey := map[string]want{}
	for _, w := range p.wants {
		wantByKey[w.key] = w
	}
	// what this pass took off the router: the table read above still has it
	gone := map[string]bool{}
	withdraw := func(key string, reason string) {
		h := held[key]
		_, err := g.release(h.Protocol, h.ExternalPort, h.InternalPort, true)
		delete(held, key)
		gone[portKey(h.Protocol, h.ExternalPort)] = err == nil
		outcome := "ok"
		if err != nil {
			outcome = "failed"
			utils.Error("UPnP: could not remove the forward of port "+strconv.Itoa(h.ExternalPort), err)
		} else {
			utils.Log("UPnP: removed the forward of port " + strconv.Itoa(h.ExternalPort) + " (" + reason + ")")
		}
		recordMapping(utils.SecUPnPMappingRemoved, h.Target, "external", strconv.Itoa(h.ExternalPort), "protocol", strings.ToLower(h.Protocol), "reason", reason, "outcome", outcome)
	}
	heldKeys := make([]string, 0, len(held))
	for key := range held {
		heldKeys = append(heldKeys, key)
	}
	sort.Strings(heldKeys)
	for _, key := range heldKeys {
		h := held[key]
		w, wanted := wantByKey[key]
		switch {
		case !wanted || forgotten[key]:
			reason := "off"
			if forgotten[key] {
				reason = "gone"
			}
			withdraw(key, reason)
		case !w.eligible:
			withdraw(key, w.mapping.Code)
		case w.mapping.Protocol != h.Protocol || w.mapping.InternalPort != h.InternalPort:
			withdraw(key, "changed")
		}
	}

	// --- external ports that are spoken for before any app is given one:
	// Cosmos' own (as wanted, and its listening ports under the same number)
	used := map[string]bool{}
	for _, w := range p.wants {
		if w.choice == -1 {
			used[portKey(w.mapping.Protocol, w.mapping.WantedPort)] = true
		}
	}
	for _, port := range []int{st.HTTPSPort, st.HTTPPort} {
		if port != 0 {
			used[portKey("TCP", port)] = true
		}
	}
	for _, w := range p.wants {
		// what an app holds stays its own while it is renewed
		if h, ok := held[w.key]; ok && w.choice != -1 {
			if used[portKey(h.Protocol, h.ExternalPort)] {
				// Cosmos' own port is now wanted there: the app moves
				withdraw(w.key, "changed")
				continue
			}
			used[portKey(h.Protocol, h.ExternalPort)] = true
		}
	}
	assigned := map[string]int{}

	// --- create or renew
	for _, w := range p.wants {
		if forgotten[w.key] {
			continue
		}
		m := w.mapping
		if !w.eligible {
			put(m)
			continue
		}
		if w.bindIP != "" && w.bindIP != local {
			m.State, m.Code = StateWaiting, "otherAddress"
			if _, ok := held[w.key]; ok {
				withdraw(w.key, "changed")
			}
			put(m)
			continue
		}
		request := requestOf(w)
		if refused, isFailed := failed[w.key]; isFailed && refused == request {
			if old, ok := previous[w.key]; ok && old.State == StateFailed {
				m = old
			} else {
				m.State = StateFailed
			}
			m.Paused = true
			put(m)
			continue
		}

		h, isHeld := held[w.key]
		candidates := candidatePorts(w, p.reserved, used, h, isHeld)
		var conflicts []*conflictError
		var refusal error
		got, renewed := 0, false
		for _, port := range candidates {
			known := isHeld && h.ExternalPort == port
			was, err := g.claimOrRenew(m.Protocol, port, m.InternalPort, w.description, leaseSeconds, known)
			if err == nil {
				got, renewed = port, was
				break
			}
			var conflict *conflictError
			if !errors.As(err, &conflict) {
				refusal = err // not a matter of the port: the next one would be refused too
				break
			}
			conflicts = append(conflicts, conflict)
		}
		holderOf := func(port int) string {
			for _, conflict := range conflicts {
				if conflict.Port == port && conflict.Holder != nil {
					return describeHolder(conflict.Holder)
				}
			}
			// a renewal does not ask for the wanted port again: who has it is
			// in the router's table
			for i := range entries {
				if entries[i].Protocol == m.Protocol && entries[i].ExternalPort == port && !(entries[i].InternalClient == local && entries[i].InternalPort == m.InternalPort) {
					return describeHolder(&entries[i])
				}
			}
			return ""
		}

		if got == 0 {
			m.State, m.Paused, m.ExternalPort = StateFailed, true, m.WantedPort
			switch {
			case refusal != nil:
				m.Code, m.Error = "refused", refusal.Error()
			case len(candidates) == 0 && p.reserved[m.WantedPort]:
				m.Code = "reserved"
				m.Error = fmt.Sprintf("external port %d is on the list of ports Cosmos never asks the router for", m.WantedPort)
			case len(candidates) == 0:
				m.Code, m.Error = "conflict", fmt.Sprintf("external port %d is already used by another mapping of Cosmos", m.WantedPort)
			case w.auto:
				m.Code, m.Holder = "conflict", holderOf(m.WantedPort)
				m.Error = fmt.Sprintf("external port %d and the %d ports tried after it are all in use on the router", m.WantedPort, len(candidates)-1)
			default:
				m.Code, m.Holder, m.Error = "conflict", holderOf(m.WantedPort), conflicts[0].Error()
			}
			if !w.auto && m.Code != "refused" {
				m.Suggested = suggestPort(g, m, p.reserved, used)
			}
			if isHeld {
				// it was ours and is not any more (the router lost it and
				// something else took the port)
				delete(held, w.key)
				recordMapping(utils.SecUPnPMappingRemoved, h.Target, "external", strconv.Itoa(h.ExternalPort), "protocol", strings.ToLower(h.Protocol), "reason", "lost")
			}
			if failed[w.key] != request {
				notices = append(notices, m.Name+": "+m.Error)
			}
			failed[w.key] = request
			utils.Error("UPnP: could not forward port "+strconv.Itoa(m.WantedPort)+" ("+m.Name+")", errors.New(m.Error))
			put(m)
			continue
		}

		delete(failed, w.key)
		m.Active, m.State, m.ExternalPort = true, StateActive, got
		if got != m.WantedPort {
			m.Code, m.Holder = "moved", holderOf(m.WantedPort)
			if p.reserved[m.WantedPort] {
				m.Code = "movedReserved"
			}
		}
		used[portKey(m.Protocol, got)] = true
		since := now
		if isHeld && h.ExternalPort == got {
			since = h.Since
		} else {
			if isHeld {
				// moved to another external port: the old one is given back
				_, err := g.release(h.Protocol, h.ExternalPort, h.InternalPort, true)
				gone[portKey(h.Protocol, h.ExternalPort)] = err == nil
				recordMapping(utils.SecUPnPMappingRemoved, h.Target, "external", strconv.Itoa(h.ExternalPort), "protocol", strings.ToLower(h.Protocol), "reason", "moved")
			}
			target := "Cosmos " + m.Name
			if m.Kind == "app" {
				target = m.Container
			}
			wantedNote := ""
			if got != m.WantedPort {
				wantedNote = strconv.Itoa(m.WantedPort)
			}
			if !renewed {
				// (found on the router as it should be, after a restart of
				// Cosmos, it is not a port that was opened just now)
				recordMapping(utils.SecUPnPMappingCreated, target, "external", strconv.Itoa(got), "internal", strconv.Itoa(m.InternalPort), "protocol", strings.ToLower(m.Protocol), "wanted", wantedNote)
			}
			utils.Log(fmt.Sprintf("UPnP: router forwards port %d (%s) to %s:%d (%s)", got, m.Protocol, local, m.InternalPort, m.Name))
			h = heldMapping{Target: target}
		}
		h.Protocol, h.ExternalPort, h.InternalPort, h.Since = m.Protocol, got, m.InternalPort, since
		held[w.key] = h
		m.Since = &since
		if w.choice != -1 && w.assigned != got {
			assigned[w.key] = got
		}
		put(m)
	}
	if len(assigned) > 0 {
		rememberAssigned(assigned)
	}

	// --- what else is on the router: Cosmos' own leftovers are removed,
	// everything else is listed and left alone
	heldPorts := map[string]bool{}
	for _, h := range held {
		heldPorts[portKey(h.Protocol, h.ExternalPort)] = true
	}
	for _, e := range entries {
		if heldPorts[portKey(e.Protocol, e.ExternalPort)] || gone[portKey(e.Protocol, e.ExternalPort)] {
			continue
		}
		if e.InternalClient == local && isOurDescription(e.Description) {
			// left by an earlier run of Cosmos and wanted by nothing now
			if deleted, err := g.release(e.Protocol, e.ExternalPort, e.InternalPort, false); err == nil && deleted {
				recordMapping(utils.SecUPnPMappingRemoved, e.Description, "external", strconv.Itoa(e.ExternalPort), "protocol", strings.ToLower(e.Protocol), "reason", "leftover")
				utils.Log("UPnP: removed a leftover forward of port " + strconv.Itoa(e.ExternalPort))
				continue
			}
		}
		st.Foreign = append(st.Foreign, Foreign{
			Protocol: e.Protocol, ExternalPort: e.ExternalPort, InternalClient: e.InternalClient,
			InternalPort: e.InternalPort, Description: e.Description, LeaseSeconds: e.Lease,
			ThisServer: e.InternalClient == local,
		})
	}
	sort.SliceStable(st.Foreign, func(i, j int) bool { return st.Foreign[i].ExternalPort < st.Foreign[j].ExternalPort })
	st.ForeignListed, st.ForeignTruncated = listed, truncated

	return publish()
}

func keyOf(m Mapping) string {
	if m.Kind == "app" {
		return appKey(m.Container, m.ContainerPort, strings.ToLower(m.Protocol))
	}
	return "cosmos:" + m.Name
}

// Further external ports tried for an app port when the wanted one is taken:
// the next ones above it.
const alternativePorts = 9

// candidatePorts lists the external ports to ask for, in order. For Cosmos'
// own ports: the wanted one, nothing else. For an app port: the one it has
// or had, the wanted one, then the next alternativePorts above it; never a
// reserved port and never one that is spoken for.
func candidatePorts(w want, reserved map[int]bool, used map[string]bool, h heldMapping, isHeld bool) []int {
	wanted := w.mapping.WantedPort
	list := []int{}
	add := func(port int, own bool) {
		if port < 1 || port > 65535 || reserved[port] {
			return
		}
		if used[portKey(w.mapping.Protocol, port)] && !own {
			return
		}
		for _, have := range list {
			if have == port {
				return
			}
		}
		list = append(list, port)
	}
	if !w.auto {
		add(wanted, true)
		return list
	}
	if isHeld {
		add(h.ExternalPort, true)
	}
	if w.assigned != 0 && w.assigned >= wanted && w.assigned <= wanted+alternativePorts {
		add(w.assigned, false)
	}
	for port := wanted; port <= wanted+alternativePorts; port++ {
		add(port, false)
	}
	return list
}

// Ports proposed for Cosmos' own HTTPS and HTTP port when the wanted one is
// taken: the ones Cloudflare's proxy passes (so a proxied domain keeps
// working), without 80 and 443.
var (
	suggestHTTPS = []int{2053, 2083, 2087, 2096, 8443}
	suggestHTTP  = []int{8080, 8880, 2052, 2082, 2086, 2095}
)

// suggestPort looks for an external port that the router shows as free. 0
// when the router cannot be asked or nothing in the short list is free. The
// port is only proposed: an administrator has to take it.
func suggestPort(g *gateway, m Mapping, reserved map[int]bool, used map[string]bool) int {
	list := suggestHTTPS
	if m.Name == "HTTP" {
		list = suggestHTTP
	}
	list = append(append([]int{}, list...), m.WantedPort+1, m.WantedPort+2, m.WantedPort+3)
	for _, port := range list {
		if port == m.WantedPort || port < 1 || port > 65535 || port == 80 || port == 443 || reserved[port] {
			continue
		}
		if used[portKey(m.Protocol, port)] && port != m.InternalPort {
			continue
		}
		existing, err := g.lookup(m.Protocol, port)
		if err != nil {
			return 0
		}
		if existing == nil {
			return port
		}
	}
	return 0
}

// StatusRoute godoc
// @Summary UPnP port forwarding status
// @Description GET returns the outcome of the last attempt to forward Cosmos' ports and the chosen app ports on the router (the router's other mappings only to administrators); POST retries now and returns the new outcome
// @Tags config
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/upnp [get]
// @Router /api/upnp [post]
func StatusRoute(w http.ResponseWriter, req *http.Request) {
	// Reading the status is for anyone who may read the configuration (the
	// settings page loads it); re-applying the forwarding is an admin action.
	permission := utils.PERM_CONFIGURATION_READ
	if req.Method != "GET" {
		permission = utils.PERM_ADMIN
	}
	if utils.CheckPermissions(w, req, permission) != nil {
		return
	}

	var st Status
	switch req.Method {
	case "GET":
		st = GetStatus()
	case "POST":
		// Takes no input: it only re-applies the saved configuration, and
		// tries again what was paused.
		st = reconcile(true)
	default:
		utils.Error("UPnP: Method not allowed "+req.Method, nil)
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}

	answer(w, forReader(st, req))
}

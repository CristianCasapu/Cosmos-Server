package maildetect

import (
	"context"
	"crypto/x509"
	"net"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// Where a candidate came from.
const (
	SourceProvider   = "provider"    // the table, by the mailbox domain
	SourceMXProvider = "mx-provider" // the table, by the domain's MX host
	SourceSRV        = "srv"         // the domain's SRV records (RFC 6186, RFC 8314)
	SourceMXHost     = "mx-host"     // the domain's own MX host, tried as a submission server
	SourceGuess      = "guess"       // smtp.<domain>, mail.<domain>
)

// Candidate is one proposal for the form. It carries host names, ports and
// fixed words only: nothing a contacted server said, and no address.
type Candidate struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Security string   `json:"security"`
	Source   string   `json:"source"`
	Provider string   `json:"provider,omitempty"`
	Check    string   `json:"check"`
	Cert     string   `json:"cert,omitempty"`
	Auth     []string `json:"auth,omitempty"`
}

// Result is the answer of a detection.
type Result struct {
	Domain      string      `json:"domain"`
	Provider    string      `json:"provider,omitempty"`
	ProviderVia string      `json:"providerVia,omitempty"` // "domain" or "mx"
	MX          string      `json:"mx"`                    // "found", "none", "failed", or "" when not asked
	Candidates  []Candidate `json:"candidates"`
	Complete    bool        `json:"complete"` // false when time ran out before everything was checked
}

// Resolver is the part of net.Resolver detection uses.
type Resolver interface {
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error)
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Detector holds the bounds of a detection and what it reaches the network
// through; the tests replace the latter.
type Detector struct {
	Resolver Resolver
	Dial     func(ctx context.Context, network, address string) (net.Conn, error)
	OwnAddrs func() []netip.Addr
	Roots    *x509.CertPool // nil: the system's
	Now      func() time.Time

	Budget        time.Duration // the whole detection
	LookupTimeout time.Duration // one DNS question
	ProbeTimeout  time.Duration // one connection, from connect to QUIT
	MaxLookups    int
	MaxProbes     int
	Parallel      int
	MaxCandidates int
}

// NewDetector returns a detector that uses the system resolver and real
// connections, with the bounds the route promises.
func NewDetector() *Detector {
	d := &Detector{
		Resolver:      net.DefaultResolver,
		OwnAddrs:      interfaceAddrs,
		Budget:        8 * time.Second,
		LookupTimeout: 2 * time.Second,
		ProbeTimeout:  3 * time.Second,
		MaxLookups:    10,
		MaxProbes:     10,
		Parallel:      4,
		MaxCandidates: 8,
	}
	dialer := &net.Dialer{Control: socketGuard(d.OwnAddrs)}
	d.Dial = dialer.DialContext
	return d
}

func (d *Detector) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// run is the state of one detection.
type run struct {
	d       *Detector
	ctx     context.Context
	lookups int
	probes  int
	targets map[string]target // host -> where to connect
}

// target is what a host name resolved to: the one address that will be
// dialled, or the reason nothing is dialled.
type target struct {
	addr  netip.Addr
	state string // "", CheckNoAnswer, CheckLocal or CheckNotChecked
}

// absolute is the name as it is asked: with the final dot, so that the
// resolver does not try it below the server's own search domains.
func absolute(name string) string {
	return name + "."
}

func (r *run) mayLookup() bool {
	if r.ctx.Err() != nil || r.lookups >= r.d.MaxLookups {
		return false
	}
	r.lookups++
	return true
}

func (r *run) lookupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.ctx, r.d.LookupTimeout)
}

// resolve finds the address to dial for host, once. Every address the name
// has must be one detection may contact, otherwise none is: a name that
// answers with a public and a local address is not given the benefit of the
// doubt. The address chosen here is the one dialled; the name is never
// resolved again, so a second, different answer changes nothing.
func (r *run) resolve(host string) target {
	if found, ok := r.targets[host]; ok {
		return found
	}
	result := target{state: CheckNotChecked}
	if r.mayLookup() {
		ctx, cancel := r.lookupContext()
		addrs, err := r.d.Resolver.LookupNetIP(ctx, "ip", absolute(host))
		cancel()
		switch {
		case err != nil || len(addrs) == 0:
			result.state = CheckNoAnswer
		default:
			own := r.d.OwnAddrs()
			result.state = ""
			for _, addr := range addrs {
				if refusedAddr(addr, own) {
					result.state = CheckLocal
				}
			}
			if result.state == "" {
				result.addr = plainAddr(addrs[0])
				for _, addr := range addrs {
					// IPv4 first: many servers at home have no IPv6 route
					if plainAddr(addr).Is4() {
						result.addr = plainAddr(addr)
						break
					}
				}
			}
		}
	}
	r.targets[host] = result
	return result
}

// Detect proposes SMTP settings for a mail domain (as returned by DomainOf).
func (d *Detector) Detect(parent context.Context, domain string) Result {
	ctx, cancel := context.WithTimeout(parent, d.Budget)
	defer cancel()
	r := &run{d: d, ctx: ctx, targets: map[string]target{}}
	result := Result{Domain: domain, Candidates: []Candidate{}}

	var candidates []Candidate
	add := func(c Candidate) {
		for _, have := range candidates {
			if have.Host == c.Host && have.Port == c.Port {
				return
			}
		}
		candidates = append(candidates, c)
	}
	fromProvider := func(p *Provider, source string) {
		for _, server := range p.Servers {
			add(Candidate{Host: server.Host, Port: server.Port, Security: server.Security, Source: source, Provider: p.ID})
		}
	}

	var guessHosts []string
	if p := providerByDomain(domain); p != nil {
		// a mailbox at a known provider: the table is the answer
		result.Provider, result.ProviderVia = p.ID, "domain"
		fromProvider(p, SourceProvider)
	} else {
		mxHost := r.mx(&result)
		if p := providerByMX(mxHost); p != nil {
			result.Provider, result.ProviderVia = p.ID, "mx"
			fromProvider(p, SourceMXProvider)
		}
		for _, c := range r.srv(domain) {
			add(c)
		}
		if result.Provider == "" {
			guessHosts = []string{"smtp." + domain, "mail." + domain}
			for _, host := range guessHosts {
				add(Candidate{Host: host, Port: 465, Security: "tls", Source: SourceGuess})
				add(Candidate{Host: host, Port: 587, Security: "starttls", Source: SourceGuess})
			}
			if mxHost != "" && mxHost != guessHosts[0] && mxHost != guessHosts[1] {
				guessHosts = append(guessHosts, mxHost)
				add(Candidate{Host: mxHost, Port: 465, Security: "tls", Source: SourceMXHost})
				add(Candidate{Host: mxHost, Port: 587, Security: "starttls", Source: SourceMXHost})
			}
		}
	}

	r.check(candidates)

	// Port 25 is where mail servers talk to each other; a server on the
	// local network sometimes takes mail from its own machines there and
	// nowhere else. Tried only when nothing else answered.
	if len(guessHosts) > 0 && !anyAnswered(candidates) {
		var plain []Candidate
		for _, host := range guessHosts {
			if r.resolve(host).state == "" {
				source := SourceGuess
				if host != "smtp."+domain && host != "mail."+domain {
					source = SourceMXHost
				}
				plain = append(plain, Candidate{Host: host, Port: 25, Security: "starttls", Source: source})
			}
		}
		r.check(plain)
		candidates = append(candidates, plain...)
	}

	result.Candidates = rank(candidates, d.MaxCandidates)
	result.Complete = ctx.Err() == nil
	return result
}

// mx asks for the domain's MX records and returns the first usable host.
func (r *run) mx(result *Result) string {
	if !r.mayLookup() {
		return ""
	}
	ctx, cancel := r.lookupContext()
	records, err := r.d.Resolver.LookupMX(ctx, absolute(result.Domain))
	cancel()
	if err != nil && len(records) == 0 {
		result.MX = "failed"
		if dnsErr, ok := err.(*net.DNSError); ok && dnsErr.IsNotFound {
			result.MX = "none"
		}
		return ""
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].Pref < records[j].Pref })
	for _, record := range records {
		if host := cleanHostName(record.Host); host != "" {
			result.MX = "found"
			return host
		}
	}
	// only a null MX ("."), or names that are not host names
	result.MX = "none"
	return ""
}

// srv asks for the submission services the domain announces: with TLS from
// the start first, as RFC 8314 prefers.
func (r *run) srv(domain string) []Candidate {
	var found []Candidate
	for _, service := range []struct{ name, security string }{{"submissions", "tls"}, {"submission", "starttls"}} {
		if !r.mayLookup() {
			break
		}
		ctx, cancel := r.lookupContext()
		_, records, _ := r.d.Resolver.LookupSRV(ctx, service.name, "tcp", absolute(domain))
		cancel()
		sort.SliceStable(records, func(i, j int) bool { return records[i].Priority < records[j].Priority })
		for _, record := range records {
			// a target of "." says the service is not offered
			host := cleanHostName(record.Target)
			if host == "" || record.Port == 0 {
				continue
			}
			found = append(found, Candidate{Host: host, Port: int(record.Port), Security: service.security, Source: SourceSRV})
			break
		}
	}
	return found
}

// check fills in the outcome of every candidate, a few at a time.
func (r *run) check(candidates []Candidate) {
	var wg sync.WaitGroup
	slots := make(chan struct{}, r.d.Parallel)
	for i := range candidates {
		c := &candidates[i]
		if !allowedPorts[c.Port] {
			c.Check = CheckNotChecked
			continue
		}
		// names are resolved here, one after the other: few, and counted
		where := r.resolve(c.Host)
		if where.state != "" {
			c.Check = where.state
			continue
		}
		if r.ctx.Err() != nil || r.probes >= r.d.MaxProbes {
			c.Check = CheckNotChecked
			continue
		}
		r.probes++
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-r.ctx.Done():
				c.Check = CheckNotChecked
				return
			}
			outcome := r.d.probe(r.ctx, c.Host, where.addr, c.Port, c.Security)
			c.Check, c.Cert, c.Auth = outcome.check, outcome.cert, outcome.auth
		}()
	}
	wg.Wait()
}

func anyAnswered(candidates []Candidate) bool {
	for _, c := range candidates {
		if c.Check == CheckOK {
			return true
		}
	}
	return false
}

// rank orders the candidates by how much each can be trusted to be right,
// drops the guesses that did not answer, and keeps the first `limit`.
func rank(candidates []Candidate, limit int) []Candidate {
	guess := func(c Candidate) bool { return c.Source == SourceGuess || c.Source == SourceMXHost }
	tier := func(c Candidate) int {
		answered := c.Check == CheckOK
		switch {
		case c.Check == CheckLocal:
			return 6
		case c.Source == SourceProvider:
			return 0
		case c.Source == SourceMXProvider:
			return 1
		case c.Source == SourceSRV && answered:
			return 2
		case guess(c) && (answered || c.Check == CheckNoStartTLS):
			return 3
		case c.Source == SourceSRV:
			return 4
		}
		return 5
	}
	portOrder := func(port int) int {
		switch port {
		case 465:
			return 0
		case 587:
			return 1
		}
		return 2
	}
	kept := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		if guess(c) && tier(c) == 5 {
			continue
		}
		kept = append(kept, c)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		a, b := kept[i], kept[j]
		if tier(a) != tier(b) {
			return tier(a) < tier(b)
		}
		if (a.Check == CheckOK) != (b.Check == CheckOK) {
			return a.Check == CheckOK
		}
		// the table's own order stands; elsewhere TLS from the start comes first
		if a.Provider == "" && a.Host == b.Host && portOrder(a.Port) != portOrder(b.Port) {
			return portOrder(a.Port) < portOrder(b.Port)
		}
		return false
	})
	if len(kept) > limit {
		kept = kept[:limit]
	}
	return kept
}

package certs

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"sort"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/reach"
	"github.com/azukaar/cosmos-server/src/utils"
)

// What the panel is told about the certificates: per domain the
// certificate it has (who issued it, whether browsers trust it, what it
// covers and what it does not, until when, when it is renewed, how the last
// order went), and per served address whether a visitor's browser will
// accept it. Codes and dates only: the sentences are the panel's.

// Attempt is how the last order for a certificate went.
type Attempt struct {
	At     time.Time `json:"at"`
	OK     bool      `json:"ok"`
	Code   string    `json:"code,omitempty"`
	Detail string    `json:"detail,omitempty"`
	CA     string    `json:"ca,omitempty"`
}

// Wait says that no order starts by itself before a moment, and why
// ("rateLimited", "daily", "weekly", "sameNames", "backoff").
type Wait struct {
	Reason string    `json:"reason"`
	Until  time.Time `json:"until"`
}

// Certificate is one domain and the certificate its names are served with.
type Certificate struct {
	Domain string `json:"domain"`
	// Derived: no domain was set up for it, it is a hostname in use.
	Derived bool `json:"derived"`
	// Kind is how the domain gets its certificate: "acme" (ordered by this
	// server), "provided", "selfsigned", "none" (plain HTTP).
	Kind string `json:"kind"`
	// Method is how an order proves the domain: "dns", "delegated", "http".
	Method   string `json:"method,omitempty"`
	Provider string `json:"provider,omitempty"`
	Via      string `json:"via,omitempty"`
	// Blocked: the method cannot work here ("noHTTPChallenge").
	Blocked string `json:"blocked,omitempty"`

	// Present: there is a certificate. The fields up to Due describe it.
	Present bool   `json:"present"`
	Issuer  string `json:"issuer,omitempty"`
	// Trust is "public" (a CA browsers trust), "staging", "test", or
	// "private" (signed by this server or by a CA browsers do not know).
	Trust       string    `json:"trust,omitempty"`
	Names       []string  `json:"names"`
	NotBefore   time.Time `json:"notBefore,omitzero"`
	NotAfter    time.Time `json:"notAfter,omitzero"`
	Expired     bool      `json:"expired"`
	RenewAt     time.Time `json:"renewAt,omitzero"`
	RenewSource string    `json:"renewSource,omitempty"` // "ca" or "lifetime"
	Warn        bool      `json:"warn"`

	// Uncovered are served names of the domain the certificate does not
	// cover (all of them when there is none).
	Uncovered []string `json:"uncovered"`
	// Records are, for a delegated challenge, the records the names need.
	Records []Delegation `json:"records,omitempty"`

	Last    *Attempt `json:"last,omitempty"`
	Wait    *Wait    `json:"wait,omitempty"`
	Running bool     `json:"running"`
	// CanRenew: "Renew now" would start an order. RenewBlocked says why
	// not: "running", "notOrdered", "noHTTPChallenge", "needsRestart" or a
	// Wait reason.
	CanRenew     bool   `json:"canRenew"`
	RenewBlocked string `json:"renewBlocked,omitempty"`
}

// Host is one served address.
type Host struct {
	Host   string `json:"host"`
	Domain string `json:"domain"`
	// Shielded: served through Cloudflare, visitors see Cloudflare's
	// certificate and Cloudflare sees this one.
	Shielded bool `json:"shielded"`
	// Trusted: a browser that reaches this server directly accepts the
	// certificate. Problem says why not, in the words of the reachability
	// check: selfSigned, wrongName, expired, none, plainHTTP.
	Trusted bool      `json:"trusted"`
	Problem string    `json:"problem,omitempty"`
	Until   time.Time `json:"until,omitzero"`
}

// Status is the answer of GET /api/certificates.
type Status struct {
	CA struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"ca"`
	Challenges   Challenges    `json:"challenges"`
	Certificates []Certificate `json:"certificates"`
	Hosts        []Host        `json:"hosts"`
	// CanAnswer are the domains that can answer the challenge of another.
	CanAnswer []string `json:"canAnswer"`
}

// certificateFor is what this server answers a handshake for a hostname
// with. A variable for the tests.
var certificateFor = func(host string) *tls.Certificate { return reach.CertificateFor(host) }

// systemRoots is the trust store certificates are judged against; nil is
// the system's. A variable for the tests.
var systemRoots *x509.CertPool

// parseChain reads a PEM text into its first certificate and the rest.
func parseChain(text string) (*x509.Certificate, *x509.CertPool) {
	var leaf *x509.Certificate
	rest := x509.NewCertPool()
	data := []byte(text)
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		if leaf == nil {
			leaf = cert
		} else {
			rest.AddCert(cert)
		}
	}
	return leaf, rest
}

// chainsToRoots reports whether a certificate chains to a root browsers
// (the system) trust. Judged at a moment inside its validity: whether it
// has expired is said apart.
func chainsToRoots(leaf *x509.Certificate, intermediates *x509.CertPool, now time.Time) bool {
	at := now
	if at.After(leaf.NotAfter) || at.Before(leaf.NotBefore) {
		at = leaf.NotBefore.Add(leaf.NotAfter.Sub(leaf.NotBefore) / 2)
	}
	_, err := leaf.Verify(x509.VerifyOptions{Intermediates: intermediates, Roots: systemRoots, CurrentTime: at,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	return err == nil
}

func issuerName(leaf *x509.Certificate) string {
	name := leaf.Issuer.CommonName
	if len(leaf.Issuer.Organization) > 0 && leaf.Issuer.Organization[0] != "" && !strings.Contains(name, leaf.Issuer.Organization[0]) {
		if name == "" {
			name = leaf.Issuer.Organization[0]
		} else {
			name = leaf.Issuer.Organization[0] + " (" + name + ")"
		}
	}
	return clean(name, 120)
}

// describe fills in what a certificate text says.
func describe(view *Certificate, certPEM string, known certState, now time.Time) *x509.Certificate {
	leaf, intermediates := parseChain(certPEM)
	if leaf == nil {
		return nil
	}
	view.Present = true
	view.Issuer = issuerName(leaf)
	view.Names = append([]string{}, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		view.Names = append(view.Names, ip.String())
	}
	view.NotBefore = leaf.NotBefore
	view.NotAfter = leaf.NotAfter
	view.Expired = now.After(leaf.NotAfter)
	view.Trust = "private"
	if chainsToRoots(leaf, intermediates, now) {
		view.Trust = "public"
	}
	// a certificate of the staging or the test authority is said to be
	// one, whatever the trust store of this machine holds
	if known.IssuedSerial == leaf.SerialNumber.String() {
		switch {
		case known.IssuedDirectory == letsEncryptStaging:
			view.Trust = "staging"
		case known.IssuedDirectory != letsEncryptProduction && known.IssuedDirectory != "":
			view.Trust = "test"
		}
	}
	return leaf
}

func covered(names []string, host string) bool {
	return utils.CertCovers(names, strings.ToLower(host))
}

// GetStatus builds the answer for the panel from the configuration, what
// is remembered about the orders, and the certificates being served.
func GetStatus() Status {
	now := clock()
	config := utils.GetMainConfig()
	status := Status{Certificates: []Certificate{}, Hosts: []Host{}, CanAnswer: []string{}}
	ca := Current()
	status.CA.Kind, status.CA.Name = ca.Kind, ca.Name
	status.Challenges = httpChallenge(config)

	for _, zone := range config.HTTPConfig.DNSZones {
		if canAnswerFor(zone) {
			status.CanAnswer = append(status.CanAnswer, zone.Zone)
		}
	}
	sort.Strings(status.CanAnswer)

	hosts := append(utils.GetAllHostnames(true), utils.ClusterHostnames()...)
	httpsOff := utils.ServerHTTPSDisabled(config.HTTPConfig)

	for _, zone := range utils.ResolveZones(config, hosts) {
		view := Certificate{Domain: zone.Zone, Derived: zone.Derived, Names: []string{}, Uncovered: []string{}}
		served := append([]string{}, zone.Hosts...)
		sort.Strings(served)

		mode := zone.HTTPSCertificateMode
		if mode == "" {
			mode = utils.HTTPSCertModeList["LETSENCRYPT"]
		}
		if httpsOff {
			mode = utils.HTTPSCertModeList["DISABLED"]
		}

		key := ""
		switch mode {
		case utils.HTTPSCertModeList["DISABLED"]:
			view.Kind = "none"
		case utils.HTTPSCertModeList["SELFSIGNED"]:
			view.Kind = "selfsigned"
			describe(&view, config.HTTPConfig.SelfTLSCert, certState{}, now)
		case utils.HTTPSCertModeList["PROVIDED"]:
			view.Kind = "provided"
			text := zone.TLSCert
			if zone.Derived {
				text = config.HTTPConfig.TLSCert
			}
			describe(&view, text, certState{}, now)
		default:
			view.Kind = "acme"
			certPEM := ""
			if !zone.Derived && utils.IssuesZoneCert(zone.DNSZoneConfig) {
				key = utils.ForkZoneCertKey(zone.Zone)
				view.Method = "dns"
				view.Provider = zone.DNSChallengeProvider
				if zone.ChallengeVia != "" {
					view.Method = "delegated"
					view.Via = zone.ChallengeVia
					view.Provider = ""
					seen := map[string]bool{}
					for _, name := range utils.ZoneCertHostnames(zone.DNSZoneConfig, zone.Hosts) {
						if record := challengeName(name); !seen[record] {
							seen[record] = true
							view.Records = append(view.Records, Delegation{Name: name, Record: record, Target: DelegationTarget(name, zone.ChallengeVia)})
						}
					}
				}
				if cert, ok := config.HTTPConfig.ZoneCerts[zone.Zone]; ok {
					certPEM = cert.TLSCert
				}
			} else {
				key = utils.ForkLocalCertKey(utils.LocalCertZone(config, zone.Zone))
				view.Method = "http"
				if status.Challenges.HTTP == "impossible" {
					view.Blocked = "noHTTPChallenge"
				}
				if cert, ok := config.HTTPConfig.LocalCerts[utils.LocalCertZone(config, zone.Zone)]; ok {
					certPEM = cert.TLSCert
				}
			}
			known := store.get(key)
			leaf := describe(&view, certPEM, known, now)
			if leaf != nil {
				view.RenewAt, view.RenewSource = renewMoment(leaf, known.ARI)
				view.Warn = !now.Before(warnAfter(leaf.NotBefore, leaf.NotAfter))
			}
			if known.LastAt != 0 {
				view.Last = &Attempt{At: time.Unix(known.LastAt, 0), OK: known.LastOK, Code: known.LastCode, Detail: known.LastDetail, CA: known.LastCA}
			}
			_, view.Running = running.Load(key)
			wanted := served
			if view.Method != "http" {
				wanted = utils.ZoneCertHostnames(zone.DNSZoneConfig, zone.Hosts)
			}
			allowed, why, until := store.mayOrder(key, wanted, false, now)
			if !allowed {
				view.Wait = &Wait{Reason: why, Until: until}
			}
			forcedAllowed, forcedWhy, _ := store.mayOrder(key, wanted, true, now)
			switch {
			case view.Running:
				view.RenewBlocked = "running"
			case view.Blocked != "":
				view.RenewBlocked = view.Blocked
			case !forcedAllowed:
				view.RenewBlocked = forcedWhy
			case view.Method == "http":
				// an order over HTTP-01 needs the HTTP port while the
				// server does not listen: it happens at a restart
				view.RenewBlocked = "needsRestart"
			default:
				view.CanRenew = true
			}
		}
		if view.Kind != "acme" {
			view.RenewBlocked = "notOrdered"
		}

		for _, host := range served {
			if view.Kind == "none" {
				continue
			}
			if !view.Present || !covered(view.Names, host) {
				view.Uncovered = append(view.Uncovered, host)
			}
		}
		status.Certificates = append(status.Certificates, view)

		for _, host := range served {
			entry := Host{Host: host, Domain: zone.Zone, Shielded: utils.IsShieldedHost(host)}
			if view.Kind == "none" {
				entry.Problem = "plainHTTP"
			} else {
				result := reach.CheckCertificate(certificateFor(host), host, now, systemRoots)
				entry.Trusted, entry.Problem, entry.Until = result.Valid, result.Problem, result.NotAfter
			}
			status.Hosts = append(status.Hosts, entry)
		}
	}
	sort.SliceStable(status.Hosts, func(i, j int) bool { return status.Hosts[i].Host < status.Hosts[j].Host })
	return status
}

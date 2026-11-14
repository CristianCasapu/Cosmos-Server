package utils

// Fork addition: proof that a connection from Cloudflare's addresses was
// made by Cloudflare's proxy ("Authenticated Origin Pulls").
//
// Cloudflare's network is shared by all its customers. A request for a
// hostname served through Cloudflare (fork_cloudflare.go) that comes from one
// of Cloudflare's addresses has not necessarily passed this server's own
// zone there. Cloudflare's answer is a client certificate: its proxy presents
// one to the origin when the origin asks for it and the zone has the feature
// on. Two kinds, the administrator's choice:
//
//   - "shared": Cloudflare's own certificate (fork_cloudflare_origin_ca.go),
//     the same for every account. Proves that Cloudflare's proxy made the
//     connection for a zone that has the feature on. Not that it was this
//     server's zone.
//   - "own": a certificate made on this server (src/cloudflare) and uploaded
//     to the zone. Only that zone holds its private key, so it proves the
//     connection was made for it.
//
// The option is off until the administrator switches it on, in two steps:
// "test" (certificates are asked for and counted, nothing is refused) and
// "enforce". What is here:
//
//   - OriginPullAsk: for the TLS listener (src/fork_origin_pulls.go), whether
//     the peer of a new connection is asked for a certificate, and which
//     issuers it is told are expected. Only peers inside Cloudflare's ranges
//     are ever asked, and only while the option is on;
//   - OriginPullCheck: whether the certificate a connection presented
//     verifies against exactly those issuers. The handshake itself verifies
//     nothing (a certificate that does not fit must end in a recorded
//     refusal, not in a broken handshake), but it always proves that the
//     peer holds the private key of what it presented;
//   - the counters the panel shows.
//
// The guard (proxy/fork_cloudflare.go) refuses on it and ForwardedClient
// believes Cloudflare's report of the visitor's address on it.

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The modes of the option, and the kinds of certificate.
const (
	OriginPullOff     = ""
	OriginPullTest    = "test"
	OriginPullEnforce = "enforce"

	OriginPullShared = "shared"
	OriginPullOwn    = "own"
)

// Why a connection does not count as made by Cloudflare's proxy. "" is "it
// does". The texts are fixed: they are recorded and shown.
const (
	OriginPullNotHTTPS  = "not_https"
	OriginPullNoCert    = "no_certificate"
	OriginPullWrongCert = "wrong_certificate"
	OriginPullExpired   = "expired_certificate"
)

// OriginPullMaxOwn is how many certificates of the server's own are accepted
// at once: the one in use and, during a change, the next.
const OriginPullMaxOwn = 2

// OriginPullConfirmWindow is how long enforcement waits for the
// administrator's confirmation before it goes back to "test" by itself.
const OriginPullConfirmWindow = 3 * time.Minute

// ForkOriginPullsConfig is HTTPConfig.Cloudflare.OriginPulls. Like the rest
// of that block it is changed only through the routes of src/cloudflare. It
// never holds a private key.
type ForkOriginPullsConfig struct {
	Mode string `json:"Mode,omitempty"`
	Kind string `json:"Kind,omitempty"`
	// Own are the issuers of the certificates made on this server, oldest
	// first.
	Own []ForkOriginPullCert `json:"Own,omitempty"`
	// Since is when Mode last changed.
	Since time.Time `json:"Since,omitzero"`
	// Confirmed: the administrator's browser reached the server after
	// enforcement began. Until then, and until ConfirmBy, enforcement is on
	// probation: when ConfirmBy passes without it, the mode in force is
	// "test" again.
	Confirmed bool      `json:"Confirmed,omitempty"`
	ConfirmBy time.Time `json:"ConfirmBy,omitzero"`
	// SharedReminded is the last reminder raised about the expiry of
	// Cloudflare's own certificate (days before expiry).
	SharedReminded int `json:"SharedReminded,omitempty"`
}

// ForkOriginPullCert is one certificate made on this server: the certificate
// of its issuer (public), which is what connections are verified against.
// The issuer's key was destroyed when the certificate was made, so exactly
// one certificate exists under it.
type ForkOriginPullCert struct {
	// CA is the issuer's certificate, PEM.
	CA string `json:"CA"`
	// Fingerprint is the SHA-256 of the certificate Cloudflare presents (the
	// leaf), lower-case hexadecimal: what its dashboard shows after the
	// upload.
	Fingerprint string    `json:"Fingerprint"`
	NotAfter    time.Time `json:"NotAfter"`
	Created     time.Time `json:"Created"`
	// HandedOver: the certificate and its key were given to Cloudflare, or
	// shown to an administrator. Until then nobody but this server has the
	// key, and nobody can present the certificate.
	HandedOver bool `json:"HandedOver,omitempty"`
	// Reminded is the last expiry reminder raised (days before expiry).
	Reminded int `json:"Reminded,omitempty"`
}

// OriginPullMode is the mode in force: what is configured, except that
// enforcement nobody confirmed in time counts as "test".
func OriginPullMode(cf ForkCloudflareConfig) string {
	pulls := cf.OriginPulls
	switch pulls.Mode {
	case OriginPullTest:
		return OriginPullTest
	case OriginPullEnforce:
		if !pulls.Confirmed && !pulls.ConfirmBy.IsZero() && time.Now().After(pulls.ConfirmBy) {
			return OriginPullTest
		}
		return OriginPullEnforce
	}
	return OriginPullOff
}

// CertificateFingerprint is the SHA-256 of a certificate in DER form,
// lower-case hexadecimal.
func CertificateFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// CloudflareOriginPullCA returns Cloudflare's own origin-pull certificate,
// the one built into this server.
func CloudflareOriginPullCA() (*x509.Certificate, error) {
	return parseOneCertificate(cloudflareOriginPullCA)
}

// parseOneCertificate reads a PEM text that holds exactly one certificate.
func parseOneCertificate(text string) (*x509.Certificate, error) {
	block, rest := pem.Decode([]byte(text))
	if block == nil || block.Type != "CERTIFICATE" || strings.TrimSpace(string(rest)) != "" {
		return nil, errors.New("not one certificate in PEM form")
	}
	return x509.ParseCertificate(block.Bytes)
}

// ParseOriginPullCA reads the issuer of a certificate made on this server
// out of the configuration. Whatever the file holds is checked before it is
// trusted: one certificate, an authority, nothing else.
func ParseOriginPullCA(text string) (*x509.Certificate, error) {
	cert, err := parseOneCertificate(text)
	if err != nil {
		return nil, err
	}
	if !cert.IsCA || !cert.BasicConstraintsValid {
		return nil, errors.New("not a certificate authority")
	}
	return cert, nil
}

// originPullIssuers is the set of issuers in force, with the pool made of
// them. A pool is never built from the system's roots.
type originPullIssuers struct {
	key   string
	pool  *x509.CertPool
	count int
}

var originPullIssuersNow atomic.Pointer[originPullIssuers]

// originPullPool returns the issuers a certificate must chain to for the
// configured kind, nil when there is none (an unknown kind, no certificate
// of the server's own yet).
func originPullPool(pulls ForkOriginPullsConfig) *x509.CertPool {
	parts := []string{pulls.Kind}
	if pulls.Kind == OriginPullOwn {
		for _, own := range pulls.Own {
			parts = append(parts, own.CA)
		}
	}
	key := strings.Join(parts, "\x00")
	if current := originPullIssuersNow.Load(); current != nil && current.key == key {
		if current.count == 0 {
			return nil
		}
		return current.pool
	}

	built := &originPullIssuers{key: key, pool: x509.NewCertPool()}
	switch pulls.Kind {
	case OriginPullShared:
		if cert, err := CloudflareOriginPullCA(); err == nil {
			built.pool.AddCert(cert)
			built.count++
		}
	case OriginPullOwn:
		for i, own := range pulls.Own {
			if i >= OriginPullMaxOwn {
				break
			}
			if cert, err := ParseOriginPullCA(own.CA); err == nil {
				built.pool.AddCert(cert)
				built.count++
			}
		}
	}
	originPullIssuersNow.Store(built)
	if built.count == 0 {
		return nil
	}
	return built.pool
}

// OriginPullAsk tells the TLS listener whether the peer of a new connection
// is asked for a client certificate: the issuers to name when it is, nil
// when it is not. Only a peer inside Cloudflare's ranges is asked, and only
// while the option is on: nobody else's handshake changes.
func OriginPullAsk(remoteAddr string) *x509.CertPool {
	cf := GetMainConfig().HTTPConfig.Cloudflare
	if OriginPullMode(cf) == OriginPullOff || !IsCloudflareAddress(cf, remoteAddr) {
		return nil
	}
	return originPullPool(cf.OriginPulls)
}

type originPullVerdict struct {
	problem string
	issuer  string
	until   time.Time
}

var (
	originPullVerdictsMu sync.Mutex
	originPullVerdicts   = map[string]originPullVerdict{}
)

// how long the verdict on one certificate is reused, and how many are kept
const (
	originPullVerdictTTL = time.Minute
	originPullVerdictMax = 256
)

// OriginPullCheck reports whether the certificate a connection presented
// verifies against the issuers in force: problem is "" when it does, else
// one of the OriginPull... reasons. issuer is the fingerprint of the issuer
// it verified against. state is the request's TLS state (nil over plain
// HTTP).
//
// The verification is the one a TLS handshake does for a client: a chain to
// one of the issuers, through the certificates the peer sent along, valid
// now, usable for client authentication. That the peer holds the private
// key was proven by the handshake.
func OriginPullCheck(cf ForkCloudflareConfig, state *tls.ConnectionState) (problem string, issuer string) {
	if state == nil {
		return OriginPullNotHTTPS, ""
	}
	if len(state.PeerCertificates) == 0 {
		return OriginPullNoCert, ""
	}
	pool := originPullPool(cf.OriginPulls)
	if pool == nil {
		return OriginPullWrongCert, ""
	}
	leaf := state.PeerCertificates[0]

	// the verdict depends on the certificate and on the issuers in force
	current := originPullIssuersNow.Load()
	key := CertificateFingerprint(leaf.Raw)
	if current != nil {
		key += "|" + CertificateFingerprint([]byte(current.key))
	}
	now := time.Now()
	originPullVerdictsMu.Lock()
	verdict, known := originPullVerdicts[key]
	originPullVerdictsMu.Unlock()
	if known && now.Before(verdict.until) {
		return verdict.problem, verdict.issuer
	}

	intermediates := x509.NewCertPool()
	for _, cert := range state.PeerCertificates[1:] {
		intermediates.AddCert(cert)
	}
	chains, err := leaf.Verify(x509.VerifyOptions{
		Roots:         pool,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	verdict = originPullVerdict{until: now.Add(originPullVerdictTTL)}
	switch {
	case leaf.IsCA:
		// an authority's own certificate is not a client's, whoever holds its key
		verdict.problem = OriginPullWrongCert
	case err == nil && len(chains) > 0 && len(chains[0]) > 0:
		chain := chains[0]
		verdict.issuer = CertificateFingerprint(chain[len(chain)-1].Raw)
		// never reuse a verdict past the end of the certificate
		for _, cert := range chain {
			if cert.NotAfter.Before(verdict.until) {
				verdict.until = cert.NotAfter
			}
		}
	case isExpiredCertificate(err):
		verdict.problem = OriginPullExpired
	default:
		verdict.problem = OriginPullWrongCert
	}

	originPullVerdictsMu.Lock()
	if len(originPullVerdicts) >= originPullVerdictMax {
		originPullVerdicts = map[string]originPullVerdict{}
	}
	originPullVerdicts[key] = verdict
	originPullVerdictsMu.Unlock()
	return verdict.problem, verdict.issuer
}

func isExpiredCertificate(err error) bool {
	var invalid x509.CertificateInvalidError
	return errors.As(err, &invalid) && invalid.Reason == x509.Expired
}

// OriginPullVerified reports whether a request arrived on a connection that
// presented a certificate of the expected kind.
func OriginPullVerified(cf ForkCloudflareConfig, state *tls.ConnectionState) bool {
	problem, _ := OriginPullCheck(cf, state)
	return problem == ""
}

// --- the counters the panel shows

// OriginPullHostCount is what was seen for one hostname served through
// Cloudflare.
type OriginPullHostCount struct {
	Host     string `json:"host"`
	Verified int64  `json:"verified"`
	Failed   int64  `json:"failed"`
	// the reason of the last request that did not verify
	LastProblem  string     `json:"lastProblem,omitempty"`
	LastVerified *time.Time `json:"lastVerified,omitempty"`
	LastFailed   *time.Time `json:"lastFailed,omitempty"`
	// FailedLastMinute: a request did not verify within the last minute
	FailedLastMinute bool `json:"failedLastMinute"`
}

// OriginPullCounts is what was seen since the counters were last reset (a
// change of the option, a restart of the server): requests for hostnames
// served through Cloudflare that came from Cloudflare's addresses.
type OriginPullCounts struct {
	Since    time.Time `json:"since"`
	Verified int64     `json:"verified"`
	Failed   int64     `json:"failed"`
	// Problems counts the requests that did not verify, by reason.
	Problems map[string]int64 `json:"problems"`
	// the last sixty seconds
	VerifiedLastMinute int64                 `json:"verifiedLastMinute"`
	FailedLastMinute   int64                 `json:"failedLastMinute"`
	Hosts              []OriginPullHostCount `json:"hosts"`
	// Issuers: when a request last verified against each issuer, by its
	// fingerprint.
	Issuers map[string]time.Time `json:"issuers"`
}

type originPullSlot struct {
	second   int64
	verified int64
	failed   int64
}

type originPullHost struct {
	verified, failed         int64
	lastProblem              string
	lastVerified, lastFailed time.Time
}

const originPullSlotSeconds = 5

var originPullCounter = struct {
	sync.Mutex
	since    time.Time
	verified int64
	failed   int64
	problems map[string]int64
	hosts    map[string]*originPullHost
	issuers  map[string]time.Time
	slots    [60 / originPullSlotSeconds]originPullSlot
}{since: time.Now(), problems: map[string]int64{}, hosts: map[string]*originPullHost{}, issuers: map[string]time.Time{}}

// ResetOriginPullCounts starts the counting anew.
func ResetOriginPullCounts() {
	c := &originPullCounter
	c.Lock()
	defer c.Unlock()
	c.since = time.Now()
	c.verified, c.failed = 0, 0
	c.problems = map[string]int64{}
	c.hosts = map[string]*originPullHost{}
	c.issuers = map[string]time.Time{}
	c.slots = [60 / originPullSlotSeconds]originPullSlot{}
}

// CountOriginPull counts one request for a hostname served through
// Cloudflare that came from Cloudflare's addresses. host is a name of the
// list of shielded hostnames (the caller checked), which bounds what is kept.
func CountOriginPull(host string, problem string, issuer string) {
	now := time.Now()
	c := &originPullCounter
	c.Lock()
	defer c.Unlock()

	entry := c.hosts[host]
	if entry == nil {
		if len(c.hosts) >= CloudflareMaxNames {
			return
		}
		entry = &originPullHost{}
		c.hosts[host] = entry
	}
	slot := &c.slots[(now.Unix()/originPullSlotSeconds)%int64(len(c.slots))]
	if slot.second != now.Unix()/originPullSlotSeconds {
		*slot = originPullSlot{second: now.Unix() / originPullSlotSeconds}
	}
	if problem == "" {
		c.verified++
		entry.verified++
		entry.lastVerified = now
		slot.verified++
		if issuer != "" && (len(c.issuers) < 2*OriginPullMaxOwn || !c.issuers[issuer].IsZero()) {
			c.issuers[issuer] = now
		}
		return
	}
	c.failed++
	c.problems[problem]++
	entry.failed++
	entry.lastFailed = now
	entry.lastProblem = problem
	slot.failed++
}

// OriginPullSnapshot returns the counters.
func OriginPullSnapshot() OriginPullCounts {
	now := time.Now()
	c := &originPullCounter
	c.Lock()
	defer c.Unlock()

	counts := OriginPullCounts{
		Since: c.since, Verified: c.verified, Failed: c.failed,
		Problems: map[string]int64{}, Hosts: []OriginPullHostCount{}, Issuers: map[string]time.Time{},
	}
	for problem, count := range c.problems {
		counts.Problems[problem] = count
	}
	for issuer, at := range c.issuers {
		counts.Issuers[issuer] = at
	}
	oldest := now.Unix()/originPullSlotSeconds - int64(len(c.slots)) + 1
	for _, slot := range c.slots {
		if slot.second >= oldest {
			counts.VerifiedLastMinute += slot.verified
			counts.FailedLastMinute += slot.failed
		}
	}
	for host, entry := range c.hosts {
		shown := OriginPullHostCount{Host: host, Verified: entry.verified, Failed: entry.failed, LastProblem: entry.lastProblem}
		if !entry.lastVerified.IsZero() {
			at := entry.lastVerified
			shown.LastVerified = &at
		}
		if !entry.lastFailed.IsZero() {
			at := entry.lastFailed
			shown.LastFailed = &at
			shown.FailedLastMinute = now.Sub(at) < time.Minute
		}
		counts.Hosts = append(counts.Hosts, shown)
	}
	sort.Slice(counts.Hosts, func(i, j int) bool { return counts.Hosts[i].Host < counts.Hosts[j].Host })
	return counts
}

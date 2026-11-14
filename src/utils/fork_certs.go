package utils

// Fork addition: where upstream's certificate code asks the fork's package
// (src/certs) for its decisions. certs imports utils, so the questions are
// function variables here, filled in by certs.Start; while they are nil
// (the tests of other packages, a build without the package) every answer
// is upstream's own behaviour.
//
//   - ForkObtain orders a certificate (DoLetsEncrypt hands over to it);
//   - ForkCertDue says whether a certificate has to be renewed (upstream:
//     45 days before the end, whatever the lifetime);
//   - ForkMayOrder says whether an order for these names may be started now
//     (back-off after a failure, bounds per day and week);
//   - ForkNoHTTPChallenge says that the HTTP-01 and TLS-ALPN-01 challenges
//     cannot reach this server, so that no such order is started.

import (
	"crypto/x509"
	"encoding/pem"
	"time"
)

// ForkCertificatesConfig is HTTPConfig.Certificates. It is changed only
// through the routes of src/certs: a full settings save puts the stored
// value back (configapi, keepCertificatesState).
type ForkCertificatesConfig struct {
	// HTTPChallenge: "" lets Cosmos work out from its ports whether a
	// certificate authority can reach it on port 80 or 443, "no" says it
	// cannot, "yes" says it can.
	HTTPChallenge string `json:"HTTPChallenge,omitempty"`
}

var (
	ForkObtain          func(domains []string, zone *DNSZoneConfig) (string, string)
	ForkCertDue         func(key string, cert ZoneCert, now time.Time) bool
	ForkMayOrder        func(key string, names []string, force bool, now time.Time) bool
	ForkNoHTTPChallenge func(config Config) bool
)

// ForkZoneCertKey and ForkLocalCertKey name a certificate for the questions
// above and for what is remembered about its orders.
func ForkZoneCertKey(zone string) string  { return "zone:" + zoneHost(zone) }
func ForkLocalCertKey(zone string) string { return "local:" + zoneHost(zone) }

// forkCertDue: has the certificate to be renewed. Without the fork's rule,
// upstream's: 45 days before the end.
func forkCertDue(key string, cert ZoneCert) bool {
	if ForkCertDue != nil {
		return ForkCertDue(key, cert, time.Now())
	}
	return !time.Now().Add(45 * 24 * time.Hour).Before(cert.ValidUntil)
}

// forkMayOrder: may an order for the certificate, with these names, be
// started now.
func forkMayOrder(key string, names []string, force bool) bool {
	if ForkMayOrder != nil {
		return ForkMayOrder(key, names, force, time.Now())
	}
	return true
}

// forkNoHTTPChallenge: no order over HTTP-01/TLS-ALPN-01 can succeed here.
func forkNoHTTPChallenge(config Config) bool {
	return ForkNoHTTPChallenge != nil && ForkNoHTTPChallenge(config)
}

// forkLocalCertServes replaces upstream's certServes for a certificate this
// server ordered for a domain: it covers every wanted hostname and is not
// due for renewal.
func forkLocalCertServes(key string, cert ZoneCert, wanted []string) bool {
	for _, host := range wanted {
		if !CertCovers(cert.Hosts, host) {
			return false
		}
	}
	return !forkCertDue(key, cert)
}

// ForkDelegated reports that the DNS challenge of a domain is answered in
// another domain of this server (DNSZoneConfig.ChallengeVia).
func ForkDelegated(zone DNSZoneConfig) bool {
	return zone.ChallengeVia != ""
}

// ForkCertExpiry is when a certificate just received ends, read from the
// certificate itself (upstream assumed 90 days from now, whatever was
// issued). A text that cannot be read gets upstream's guess.
func ForkCertExpiry(certPEM string) time.Time {
	if leaf := ForkLeaf(certPEM); leaf != nil {
		return leaf.NotAfter
	}
	return time.Now().AddDate(0, 0, 90)
}

// ForkLeaf parses the first certificate of a PEM text, nil when there is none.
func ForkLeaf(certPEM string) *x509.Certificate {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}
	return leaf
}

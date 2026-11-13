package reach

import (
	"crypto/tls"
	"crypto/x509"
	"time"
)

// CertificateFor returns the certificate Cosmos serves for a hostname. Set by
// the server (src/fork_reachability.go): the choice lives in package main.
var CertificateFor = func(host string) *tls.Certificate { return nil }

// CheckCertificate says whether a browser would accept cert for host: it
// covers the name, has not expired, and chains to a root the system trusts
// (roots nil = the system's). Nothing is fetched.
func CheckCertificate(cert *tls.Certificate, host string, now time.Time, roots *x509.CertPool) CertResult {
	if cert == nil || len(cert.Certificate) == 0 {
		return CertResult{Problem: "none"}
	}
	leaf := cert.Leaf
	if leaf == nil {
		parsed, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return CertResult{Problem: "none"}
		}
		leaf = parsed
	}
	result := CertResult{NotAfter: leaf.NotAfter}
	if leaf.VerifyHostname(host) != nil {
		result.Problem = "wrongName"
		return result
	}
	if now.After(leaf.NotAfter) {
		result.Problem = "expired"
		return result
	}
	intermediates := x509.NewCertPool()
	for _, der := range cert.Certificate[1:] {
		if parsed, err := x509.ParseCertificate(der); err == nil {
			intermediates.AddCert(parsed)
		}
	}
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Intermediates: intermediates, Roots: roots, CurrentTime: now}); err != nil {
		result.Problem = "selfSigned"
		return result
	}
	result.Valid = true
	return result
}

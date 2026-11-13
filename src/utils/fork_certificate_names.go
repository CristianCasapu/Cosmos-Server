package utils

// Fork addition: the self-signed certificate names IP addresses as addresses.
//
// A certificate lists what it is valid for as DNS names and, separately, as
// IP addresses. Upstream put every hostname into the DNS names, an address
// like 172.16.0.14 included, and browsers do not accept an address there: a
// server reached by its address showed a certificate warning for good, even
// on a device that trusts Cosmos' own certificate authority. The addresses
// are now also listed as addresses (the DNS names stay as they were), so
// that such a device gets a verified connection.

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"strings"
)

// CertificateIPs returns the IP addresses among the names of a certificate
// to be made (a name may carry a port, which is not part of it).
func CertificateIPs(names []string) []net.IP {
	ips := []net.IP{}
	for _, name := range names {
		host := strings.TrimSpace(name)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
			ips = append(ips, ip)
		}
	}
	return ips
}

// CertificateLacksIP reports whether a certificate (PEM) does not list, as an
// address, one of the IP addresses among names: such a certificate was made
// before this rule and has to be made again.
func CertificateLacksIP(certPEM string, names []string) bool {
	wanted := CertificateIPs(names)
	if len(wanted) == 0 {
		return false
	}
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return false // nothing to compare with: the other rules decide
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	for _, ip := range wanted {
		found := false
		for _, have := range cert.IPAddresses {
			if have.Equal(ip) {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}
	return false
}

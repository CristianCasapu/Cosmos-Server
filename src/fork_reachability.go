package main

// Fork addition: lets src/reach see the certificate this server serves for a
// hostname (the choice is made by GetCertificate, in this package).

import (
	"crypto/tls"

	"github.com/azukaar/cosmos-server/src/reach"
)

func init() {
	reach.CertificateFor = func(host string) *tls.Certificate {
		cert, err := GetCertificate(&tls.ClientHelloInfo{ServerName: host})
		if err != nil {
			return nil
		}
		return cert
	}
}

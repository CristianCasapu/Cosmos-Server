package main

// Fork addition (plan 50): whether the certificate this server presents for a
// name is signed by Cosmos' own authority, for the guides of the apps (a
// client of such an address must be given the authority's certificate,
// which GET /api/status already carries as CACert). See
// src/configapi/fork_app_guides.go.

import (
	"crypto/tls"
	"crypto/x509"

	"github.com/azukaar/cosmos-server/src/configapi"
	"github.com/azukaar/cosmos-server/src/utils"
)

func init() {
	configapi.ForkHostOwnCA = forkHostOwnCA
}

func forkHostOwnCA(host string) bool {
	if host == "" {
		return false
	}
	ca, _ := utils.ForkCosmosCA()
	if ca == nil {
		return false
	}
	cert, err := GetCertificate(&tls.ClientHelloInfo{ServerName: host})
	if err != nil || cert == nil || len(cert.Certificate) == 0 {
		return false
	}
	leaf := cert.Leaf
	if leaf == nil {
		leaf, err = x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return false
		}
	}
	return leaf.CheckSignatureFrom(ca) == nil
}

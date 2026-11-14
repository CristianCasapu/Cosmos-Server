package configapi

import (
	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: a settings save keeps the CA and the self-signed certificate.
//
// The settings page saves the whole configuration it read. What it read had
// the private keys taken out (get.go), and upstream's save puts back the ones
// of the main certificate only. The key of the CA and the key of the
// self-signed certificate were therefore written back empty, and the restart
// that follows every save found a certificate without its key: it made a new
// CA and a new certificate each time. Browsers had to be told to accept the
// server again after every save, and a CA installed on a device (the "trust"
// page offers it for download) stopped being the one in use.
//
// These fields are the server's own: it creates them at start-up
// (InitServer, utils.GenerateRSAWebCertificates) and nothing in the interface
// edits them. So the save takes them from the stored configuration, whatever
// the request carries. The start-up code is unchanged: it still makes a new
// certificate when a hostname is not covered, when the certificate nears its
// end or when a renewal is forced, and signs it with the CA it kept.
func keepServerCertificates(request *utils.Config, stored utils.Config) {
	request.HTTPConfig.CACert = stored.HTTPConfig.CACert
	request.HTTPConfig.CAPrivateKey = stored.HTTPConfig.CAPrivateKey
	request.HTTPConfig.SelfTLSCert = stored.HTTPConfig.SelfTLSCert
	request.HTTPConfig.SelfTLSKey = stored.HTTPConfig.SelfTLSKey
	request.HTTPConfig.SelfTLSKeyHostsCached = stored.HTTPConfig.SelfTLSKeyHostsCached
	request.HTTPConfig.SelfTLSValidUntil = stored.HTTPConfig.SelfTLSValidUntil
	// What the certificate orders may use is changed through the routes of
	// src/certs only (POST /api/certificates/settings), which check it.
	request.HTTPConfig.Certificates = stored.HTTPConfig.Certificates
}

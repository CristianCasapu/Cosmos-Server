package main

// Fork addition: wires the fork's certificate package (src/certs) into the
// server: its routes, the three places where upstream's issuance asks it
// something, and "Renew now".

import (
	"time"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/certs"
	"github.com/azukaar/cosmos-server/src/upnp"
	"github.com/azukaar/cosmos-server/src/utils"
)

// registerCertificateRoutes adds the routes to the router of the admin API
// (same-origin check, token and session checks as its neighbours) and
// connects the package. InitServer calls it at every (re)start; connecting
// happens once.
func registerCertificateRoutes(router *mux.Router) {
	router.HandleFunc("/api/certificates", certs.StatusRoute)
	router.HandleFunc("/api/certificates/providers", certs.ProvidersRoute)
	router.HandleFunc("/api/certificates/renew", certs.RenewRoute)
	router.HandleFunc("/api/certificates/domain", certs.DomainRoute)
	router.HandleFunc("/api/certificates/delegation", certs.DelegationRoute)
	router.HandleFunc("/api/certificates/settings", certs.SettingsRoute)
	connectCertificates()
}

// connectCertificates hands the package what only this one knows.
func connectCertificates() {
	certs.ExternalPorts = func() (int, int) { return upnp.ExternalHTTPSPort(), upnp.ExternalHTTPPort() }
	certs.RenewZone = forkRenewZone
	certs.Start()
}

// forkIssueZoneCerts replaces the call InitServer made to issueZoneCerts.
// Upstream ordered before the server listened: every start with a
// certificate due (and every settings save is a start) kept the panel and
// every application unreachable for as long as the order took, two minutes
// and more when the DNS provider did not answer. The server now starts
// with what it has (the previous certificate, the self-signed one for a
// first order) and the order runs behind it; the certificate is swapped in
// when it arrives, the way upstream already does for a cluster
// (refreshZoneCerts).
//
// The package is connected here too: InitServer orders before it sets up
// the routes.
func forkIssueZoneCerts(force bool) {
	connectCertificates()
	if len(utils.ZoneCertsToIssue(utils.GetMainConfig(), force)) == 0 {
		return
	}
	go func() {
		// let this start finish writing the configuration and listen first
		time.Sleep(forkZoneOrderDelay)
		issueZoneCerts(force)
		loadZoneCertStore()
	}()
}

// forkZoneOrderDelay is how long after a start the order begins.
var forkZoneOrderDelay = 3 * time.Second

// forkCertificateFailed replaces upstream's notice of a failed order
// (utils.MajorError): same text, with the reason, and held back when the
// same reason was told for this certificate within a day.
func forkCertificateFailed(key string, message string, reason error) {
	certs.NoticeFailure(key, message, reason)
}

// forkRenewZone is "Renew now" for a domain whose certificate comes
// through DNS: the order runs without a restart. certs marked the
// certificate as asked for, which makes it due and lifts the wait after a
// failure; upstream's own one-hour memory of a failure is lifted here.
func forkRenewZone(zone string) {
	zoneIssueLock.Lock()
	delete(lastZoneIssueFailure, zone)
	zoneIssueLock.Unlock()
	refreshZoneCerts()
}

// forkLetsEncryptEmailNeeded is what the status tells the home page under
// "letsencrypt", which makes it show "You need to provide the configuration
// with an email address to use for Let's Encrypt". Upstream said so whenever
// the server orders certificates (utils.NeedsLetsEncrypt, true as soon as a
// domain has a DNS provider) and no address was set. That is not true:
// an account is registered and certificates are issued without a contact
// address (seen on the server this fork is for), Let's Encrypt stopped
// sending expiry emails on 2025-06-04 and deleted the addresses it had
// ("Let's Encrypt will not store the address"). No order ever waits on an
// address, so the home page is never told one is missing; the field itself
// says that it is optional (configHTTPS.jsx).
func forkLetsEncryptEmailNeeded() bool {
	return false
}

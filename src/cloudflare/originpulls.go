package cloudflare

// Fork addition: "Authenticated Origin Pulls" from the panel. What the
// request path does with it is in utils/fork_origin_pulls.go (who is asked
// for a certificate, what verifies), proxy/fork_cloudflare.go (the guard) and
// src/fork_origin_pulls.go (the TLS listener). This file is the
// administrator's side:
//
//   - making a certificate of the server's own. An authority and one
//     certificate under it are made here; the authority's key is dropped
//     before the function returns, so nothing else can ever be issued under
//     it, and its certificate (public) is what the configuration keeps. The
//     certificate and its private key, which Cloudflare needs in order to
//     present it, are held in memory only, for a quarter of an hour at most,
//     until they are handed over once: uploaded to Cloudflare through its
//     API, or shown to the administrator. They are never written to the
//     configuration, a log, an event or an error;
//   - the guarded sequence that switches the option on: test, enforce,
//     confirm, with the fall-back when enforcement is not confirmed;
//   - the state the panel shows, and the reminders before a certificate
//     expires.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	// how long a certificate of the server's own is valid
	ownCertValidity = 365 * 24 * time.Hour
	// how long the private key waits in memory to be handed over
	pendingLifetime = 15 * time.Minute
	maxPullsBody    = 8 << 10
)

// days before expiry at which a reminder is raised
var (
	ownReminders    = []int{30, 14, 3}
	sharedReminders = []int{60, 30, 7}
)

// what the tests replace
var (
	// recycleConnections makes every client connect anew, so that the
	// connections Cloudflare holds open are asked for a certificate from
	// then on. It is what saving a URL does.
	recycleConnections = func() {
		if utils.RestartHTTPServer != nil {
			go utils.RestartHTTPServer()
		}
	}
	notifyAdmins = func(title string, message string, vars string, level string) {
		utils.WriteNotification(utils.Notification{Recipient: "admin", Title: title, Message: message, Vars: vars, Level: level})
	}
	rsaBits = 2048
)

// The notifications this file raises.
const (
	TitleOriginPullExpiring   = "header.notification.title.originPullExpiring"
	MessageOriginPullExpiring = "header.notification.message.originPullExpiring"
	TitleOriginPullStopped    = "header.notification.title.originPullStopped"
	MessageOriginPullStopped  = "header.notification.message.originPullStopped"
)

// ownBundle is a certificate made here that has not been handed over yet.
type ownBundle struct {
	certificate string // PEM, the leaf: what Cloudflare presents
	privateKey  string // PEM
	fingerprint string // of the leaf
	until       time.Time
}

var (
	pendingMu sync.Mutex
	pending   *ownBundle
)

// pendingBundle returns the bundle waiting to be handed over, nil when there
// is none (any more).
func pendingBundle() *ownBundle {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	if pending != nil && time.Now().After(pending.until) {
		pending = nil
	}
	return pending
}

func setPending(bundle *ownBundle) {
	pendingMu.Lock()
	pending = bundle
	pendingMu.Unlock()
}

func randomSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}

// makeOwnCertificate makes an authority and one client certificate under it.
// It returns what the configuration keeps (the authority's certificate) and
// the bundle for Cloudflare. The authority's key does not leave this
// function.
func makeOwnCertificate(now time.Time) (utils.ForkOriginPullCert, *ownBundle, error) {
	none := utils.ForkOriginPullCert{}
	tag := make([]byte, 4)
	if _, err := rand.Read(tag); err != nil {
		return none, nil, err
	}
	name := hex.EncodeToString(tag)

	caKey, err := rsa.GenerateKey(rand.Reader, rsaBits)
	if err != nil {
		return none, nil, err
	}
	caSerial, err := randomSerial()
	if err != nil {
		return none, nil, err
	}
	notBefore := now.Add(-5 * time.Minute).UTC().Truncate(time.Second)
	notAfter := now.Add(ownCertValidity).UTC().Truncate(time.Second)
	caTemplate := &x509.Certificate{
		SerialNumber:          caSerial,
		Subject:               pkix.Name{CommonName: "Cosmos origin pull authority " + name},
		NotBefore:             notBefore,
		NotAfter:              notAfter.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return none, nil, err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return none, nil, err
	}

	leafKey, err := rsa.GenerateKey(rand.Reader, rsaBits)
	if err != nil {
		return none, nil, err
	}
	leafSerial, err := randomSerial()
	if err != nil {
		return none, nil, err
	}
	leafTemplate := &x509.Certificate{
		SerialNumber:          leafSerial,
		Subject:               pkix.Name{CommonName: "Cosmos origin pull " + name},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return none, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		return none, nil, err
	}

	fingerprint := utils.CertificateFingerprint(leafDER)
	stored := utils.ForkOriginPullCert{
		CA:          string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})),
		Fingerprint: fingerprint,
		NotAfter:    notAfter,
		Created:     now.UTC().Truncate(time.Second),
	}
	bundle := &ownBundle{
		certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})),
		privateKey:  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})),
		fingerprint: fingerprint,
		until:       now.Add(pendingLifetime),
	}
	return stored, bundle, nil
}

// issuerFingerprint is the fingerprint of the authority of a certificate of
// the server's own: what the counters know it by.
func issuerFingerprint(own utils.ForkOriginPullCert) string {
	cert, err := utils.ParseOriginPullCA(own.CA)
	if err != nil {
		return ""
	}
	return utils.CertificateFingerprint(cert.Raw)
}

func daysLeft(until time.Time, now time.Time) int {
	if !until.After(now) {
		return 0
	}
	return int(until.Sub(now).Hours() / 24)
}

// handedOver lists the certificates of the server's own that Cloudflare can
// have: the ones that were uploaded or shown.
func handedOver(pulls utils.ForkOriginPullsConfig) []utils.ForkOriginPullCert {
	out := []utils.ForkOriginPullCert{}
	for _, own := range pulls.Own {
		if own.HandedOver {
			out = append(out, own)
		}
	}
	return out
}

// cannotTest says why certificates cannot be asked for yet, "" when they can.
func cannotTest(pulls utils.ForkOriginPullsConfig, now time.Time) string {
	switch pulls.Kind {
	case utils.OriginPullShared:
		cert, err := utils.CloudflareOriginPullCA()
		if err != nil || !now.Before(cert.NotAfter) {
			return "shared_expired"
		}
		return ""
	case utils.OriginPullOwn:
		for _, own := range handedOver(pulls) {
			if now.Before(own.NotAfter) {
				return ""
			}
		}
		return "no_certificate"
	}
	return "no_kind"
}

// cannotEnforce says why enforcement cannot begin, "" when it can. "failing"
// can be overruled by the administrator; the others cannot.
func cannotEnforce(cf utils.ForkCloudflareConfig, counts utils.OriginPullCounts, now time.Time) string {
	if cf.OriginPulls.Mode == utils.OriginPullOff {
		return "not_testing"
	}
	if problem := cannotTest(cf.OriginPulls, now); problem != "" {
		return problem
	}
	if counts.Verified == 0 {
		return "nothing_verified"
	}
	if counts.FailedLastMinute > 0 {
		return "failing"
	}
	return ""
}

// OwnCertState is one certificate of the server's own, for the panel.
type OwnCertState struct {
	Fingerprint string    `json:"fingerprint"`
	NotAfter    time.Time `json:"notAfter"`
	DaysLeft    int       `json:"daysLeft"`
	Created     time.Time `json:"created"`
	// HandedOver: it was uploaded to Cloudflare or shown to an administrator.
	// Waiting: its private key is still held here, for WaitingSeconds more.
	HandedOver     bool `json:"handedOver"`
	Waiting        bool `json:"waiting"`
	WaitingSeconds int  `json:"waitingSeconds,omitempty"`
	// LastVerified: when a request last verified with it (since the
	// counters were reset).
	LastVerified *time.Time `json:"lastVerified,omitempty"`
}

// OriginPullsState is the part of the status about origin pulls.
type OriginPullsState struct {
	// Mode in force: "", "test" or "enforce"
	Mode string `json:"mode"`
	Kind string `json:"kind"`
	// Unconfirmed: enforcement began and waits for the confirmation, for
	// ConfirmSeconds more
	Unconfirmed    bool `json:"unconfirmed"`
	ConfirmSeconds int  `json:"confirmSeconds,omitempty"`
	// Shared is Cloudflare's own certificate, as built into this server
	Shared struct {
		Fingerprint string    `json:"fingerprint"`
		NotAfter    time.Time `json:"notAfter"`
		DaysLeft    int       `json:"daysLeft"`
	} `json:"shared"`
	Own []OwnCertState `json:"own"`
	// CannotTest and CannotEnforce: why the next step is not possible, ""
	// when it is
	CannotTest    string                 `json:"cannotTest"`
	CannotEnforce string                 `json:"cannotEnforce"`
	Counts        utils.OriginPullCounts `json:"counts"`
	// Zones: the Cloudflare domains of the configuration, where a
	// certificate can be uploaded
	Zones []string `json:"zones"`
}

func originPullsStatus(config utils.Config) OriginPullsState {
	cf := config.HTTPConfig.Cloudflare
	pulls := cf.OriginPulls
	now := time.Now()
	counts := utils.OriginPullSnapshot()

	state := OriginPullsState{
		Mode: utils.OriginPullMode(cf), Kind: pulls.Kind, Own: []OwnCertState{}, Counts: counts, Zones: []string{},
	}
	if pulls.Mode == utils.OriginPullEnforce && !pulls.Confirmed && now.Before(pulls.ConfirmBy) {
		state.Unconfirmed = true
		state.ConfirmSeconds = int(pulls.ConfirmBy.Sub(now).Seconds()) + 1
	}
	if cert, err := utils.CloudflareOriginPullCA(); err == nil {
		state.Shared.Fingerprint = utils.CertificateFingerprint(cert.Raw)
		state.Shared.NotAfter = cert.NotAfter
		state.Shared.DaysLeft = daysLeft(cert.NotAfter, now)
	}
	waiting := pendingBundle()
	for _, own := range pulls.Own {
		shown := OwnCertState{
			Fingerprint: own.Fingerprint, NotAfter: own.NotAfter, DaysLeft: daysLeft(own.NotAfter, now),
			Created: own.Created, HandedOver: own.HandedOver,
		}
		if waiting != nil && waiting.fingerprint == own.Fingerprint {
			shown.Waiting = true
			shown.WaitingSeconds = int(waiting.until.Sub(now).Seconds()) + 1
		}
		if at, ok := counts.Issuers[issuerFingerprint(own)]; ok {
			seen := at
			shown.LastVerified = &seen
		}
		state.Own = append(state.Own, shown)
	}
	state.CannotTest = cannotTest(pulls, now)
	state.CannotEnforce = cannotEnforce(cf, counts, now)
	for _, zone := range config.HTTPConfig.DNSZones {
		if isCloudflareZone(zone) {
			state.Zones = append(state.Zones, strings.ToLower(zone.Zone))
		}
	}
	return state
}

// refusal is an action that cannot be done now: reason is a fixed code the
// panel turns into a sentence.
type refusal struct{ reason string }

func (r *refusal) Error() string { return "not possible now: " + r.reason }

func refuse(reason string) error { return &refusal{reason: reason} }

// dropUnused removes the certificates that were never handed over and whose
// key is no longer held: nobody can present them.
func dropUnused(pulls *utils.ForkOriginPullsConfig, waiting *ownBundle) {
	kept := []utils.ForkOriginPullCert{}
	for _, own := range pulls.Own {
		if own.HandedOver || (waiting != nil && waiting.fingerprint == own.Fingerprint) {
			kept = append(kept, own)
		}
	}
	pulls.Own = kept
}

// generateOwn makes a certificate and keeps its bundle for the hand-over.
func generateOwn() error {
	now := time.Now()
	stored, bundle, err := makeOwnCertificate(now)
	if err != nil {
		return errors.New("the certificate could not be made")
	}
	err = updateConfig(func(config *utils.Config) error {
		pulls := &config.HTTPConfig.Cloudflare.OriginPulls
		// one that was made before and never handed over is replaced
		dropUnused(pulls, nil)
		if len(pulls.Own) >= utils.OriginPullMaxOwn {
			return refuse("two_certificates")
		}
		pulls.Own = append(pulls.Own, stored)
		return nil
	})
	if err != nil {
		return err
	}
	setPending(bundle)
	return nil
}

// markHandedOver records that the bundle left this server, and forgets it.
func markHandedOver(fingerprint string) error {
	err := updateConfig(func(config *utils.Config) error {
		pulls := &config.HTTPConfig.Cloudflare.OriginPulls
		for i := range pulls.Own {
			if pulls.Own[i].Fingerprint == fingerprint {
				pulls.Own[i].HandedOver = true
				return nil
			}
		}
		return refuse("no_pending")
	})
	setPending(nil)
	return err
}

// UploadOriginPullCertificate uploads a client certificate for a zone's
// "zone-level" Authenticated Origin Pulls. Needs the permission "SSL and
// Certificates: Edit".
func (c *Client) UploadOriginPullCertificate(ctx context.Context, zoneID string, certificate string, privateKey string) error {
	if !validID(zoneID) {
		return &APIError{Kind: "other", Message: "not a zone id"}
	}
	body := map[string]string{"certificate": certificate, "private_key": privateKey}
	_, err := c.call(ctx, "POST", "/zones/"+zoneID+"/origin_tls_client_auth", nil, body, nil)
	return err
}

// EnableOriginPulls switches a zone's "zone-level" Authenticated Origin
// Pulls on. Same permission.
func (c *Client) EnableOriginPulls(ctx context.Context, zoneID string) error {
	if !validID(zoneID) {
		return &APIError{Kind: "other", Message: "not a zone id"}
	}
	_, err := c.call(ctx, "PUT", "/zones/"+zoneID+"/origin_tls_client_auth/settings", nil, map[string]bool{"enabled": true}, nil)
	return err
}

// uploadOwn hands the waiting bundle to Cloudflare for one domain of the
// configuration: with oneTime when given (a token used for this and not
// kept), else with the token stored for the domain. enabled tells whether
// the feature could also be switched on there.
func uploadOwn(ctx context.Context, zoneName string, oneTime string) (enabled bool, err error) {
	bundle := pendingBundle()
	if bundle == nil {
		return false, refuse("no_pending")
	}
	config := utils.GetMainConfig()
	var zone *utils.DNSZoneConfig
	for i := range config.HTTPConfig.DNSZones {
		if strings.EqualFold(config.HTTPConfig.DNSZones[i].Zone, zoneName) && isCloudflareZone(config.HTTPConfig.DNSZones[i]) {
			zone = &config.HTTPConfig.DNSZones[i]
		}
	}
	if zone == nil {
		return false, refuse("not_cloudflare_zone")
	}
	stored := storedToken(*zone)
	token := stored
	if oneTime != "" {
		if TokenFormat(oneTime) != "token" {
			return false, &APIError{Kind: "malformed_token"}
		}
		token = strings.TrimSpace(oneTime)
	}
	if token == "" {
		return false, &APIError{Kind: "invalid_token", Message: "no API token is stored for this domain"}
	}

	// the domain's id: the token for the upload may see nothing but
	// certificates, the stored one sees the domain
	zoneID, err := lookupZoneID(ctx, NewClient(token), strings.ToLower(zone.Zone))
	if err != nil && stored != "" && stored != token {
		zoneID, err = lookupZoneID(ctx, NewClient(stored), strings.ToLower(zone.Zone))
	}
	if err != nil {
		return false, err
	}

	client := NewClient(token)
	if err := client.UploadOriginPullCertificate(ctx, zoneID, bundle.certificate, bundle.privateKey); err != nil {
		return false, err
	}
	// Cloudflare has it: the key is dropped here whatever follows
	if err := markHandedOver(bundle.fingerprint); err != nil {
		return false, err
	}
	if err := client.EnableOriginPulls(ctx, zoneID); err != nil {
		utils.Warn("[Cloudflare] the certificate was uploaded for " + zone.Zone + ", but Authenticated Origin Pulls could not be switched on there: " + err.Error())
		return false, nil
	}
	return true, nil
}

// setMode is the guarded change of the option.
func setMode(mode string, anyway bool) error {
	now := time.Now()
	counts := utils.OriginPullSnapshot()
	before := utils.OriginPullOff
	err := updateConfig(func(config *utils.Config) error {
		cf := &config.HTTPConfig.Cloudflare
		pulls := &cf.OriginPulls
		before = utils.OriginPullMode(*cf)
		switch mode {
		case utils.OriginPullOff:
		case utils.OriginPullTest:
			if problem := cannotTest(*pulls, now); problem != "" {
				return refuse(problem)
			}
		case utils.OriginPullEnforce:
			if before == utils.OriginPullEnforce {
				return nil
			}
			problem := cannotEnforce(*cf, counts, now)
			if problem != "" && !(problem == "failing" && anyway) {
				return refuse(problem)
			}
		default:
			return refuse("unknown_mode")
		}
		pulls.Mode = mode
		pulls.Since = now.UTC().Truncate(time.Second)
		pulls.Confirmed = false
		pulls.ConfirmBy = time.Time{}
		if mode == utils.OriginPullEnforce {
			pulls.ConfirmBy = now.Add(utils.OriginPullConfirmWindow).UTC()
		}
		return nil
	})
	if err != nil {
		return err
	}
	after := utils.OriginPullMode(utils.GetMainConfig().HTTPConfig.Cloudflare)
	if after != before {
		utils.ResetOriginPullCounts()
	}
	if before == utils.OriginPullOff && after != utils.OriginPullOff {
		// the connections Cloudflare holds open were never asked for a certificate
		recycleConnections()
	}
	return nil
}

// setKind chooses whose certificate is expected. Not while enforcing: the
// requests that verify now would stop verifying.
func setKind(kind string) error {
	if kind != utils.OriginPullShared && kind != utils.OriginPullOwn {
		return refuse("no_kind")
	}
	changed, asking := false, false
	err := updateConfig(func(config *utils.Config) error {
		cf := &config.HTTPConfig.Cloudflare
		if cf.OriginPulls.Kind == kind {
			return nil
		}
		if cf.OriginPulls.Mode == utils.OriginPullEnforce {
			return refuse("enforcing")
		}
		cf.OriginPulls.Kind = kind
		changed = true
		if cf.OriginPulls.Mode == utils.OriginPullTest {
			if cannotTest(cf.OriginPulls, time.Now()) != "" {
				// nothing to test with yet: the test starts again once there is
				cf.OriginPulls.Mode = utils.OriginPullOff
			} else {
				asking = true
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if changed {
		utils.ResetOriginPullCounts()
	}
	if asking {
		recycleConnections()
	}
	return nil
}

// retireOwn stops accepting one certificate of the server's own. While
// certificates are relied on, the last one cannot go, and one cannot go
// before a request has verified with the other.
func retireOwn(fingerprint string) error {
	counts := utils.OriginPullSnapshot()
	waiting := pendingBundle()
	err := updateConfig(func(config *utils.Config) error {
		cf := &config.HTTPConfig.Cloudflare
		pulls := &cf.OriginPulls
		kept := []utils.ForkOriginPullCert{}
		found := false
		for _, own := range pulls.Own {
			if own.Fingerprint == fingerprint {
				found = true
				continue
			}
			kept = append(kept, own)
		}
		if !found {
			return refuse("no_certificate")
		}
		if pulls.Kind == utils.OriginPullOwn && pulls.Mode != utils.OriginPullOff {
			if len(handedOver(utils.ForkOriginPullsConfig{Own: kept})) == 0 {
				return refuse("last_certificate")
			}
			if pulls.Mode == utils.OriginPullEnforce {
				verifiedWithAnother := false
				for _, own := range kept {
					if _, ok := counts.Issuers[issuerFingerprint(own)]; ok {
						verifiedWithAnother = true
					}
				}
				if !verifiedWithAnother {
					return refuse("not_seen")
				}
			}
		}
		pulls.Own = kept
		return nil
	})
	if err == nil && waiting != nil && waiting.fingerprint == fingerprint {
		setPending(nil)
	}
	return err
}

// confirmEnforcement is what the administrator's browser sends after
// enforcement began: the request arriving is the proof that the way in works.
func confirmEnforcement() error {
	return updateConfig(func(config *utils.Config) error {
		pulls := &config.HTTPConfig.Cloudflare.OriginPulls
		if pulls.Mode != utils.OriginPullEnforce {
			return refuse("not_enforcing")
		}
		if !pulls.Confirmed && time.Now().After(pulls.ConfirmBy) {
			return refuse("too_late")
		}
		pulls.Confirmed = true
		return nil
	})
}

// OriginPullsRoute godoc
// @Summary Authenticated Origin Pulls: proof that a request came through Cloudflare's proxy
// @Description action "kind" chooses whose certificate Cloudflare's proxy must present (kind: "shared" for Cloudflare's own, "own" for one made on this server); "generate" makes a certificate of the server's own; "upload" hands it to Cloudflare for a domain (zone), with the stored token or a one-time token that is not kept; "reveal" returns the certificate and its private key once, for an upload by hand; "discard" drops a certificate that was not handed over; "mode" switches the option ("test": certificates are asked for and counted, "enforce": requests without one are refused, "": off); "confirm" confirms enforcement from the administrator's browser; "retire" stops accepting one certificate (fingerprint).
// @Tags cloudflare
// @Router /api/cloudflare/origin-pulls [post]
func OriginPullsRoute(w http.ResponseWriter, req *http.Request) {
	if !postOnly(w, req) {
		return
	}
	var body struct {
		Action      string `json:"action"`
		Kind        string `json:"kind"`
		Mode        string `json:"mode"`
		Anyway      bool   `json:"anyway"`
		Zone        string `json:"zone"`
		Token       string `json:"token"`
		Fingerprint string `json:"fingerprint"`
	}
	if !readBody(w, req, maxPullsBody, &body) {
		return
	}

	result := map[string]interface{}{}
	audit := []string{"sections", "cloudflare", "originPulls", body.Action}
	var err error
	switch body.Action {
	case "kind":
		audit = append(audit, "kind", body.Kind)
		err = setKind(body.Kind)
	case "generate":
		err = generateOwn()
	case "upload":
		zone, ok := domainName(body.Zone)
		if !ok {
			utils.HTTPError(w, "Invalid domain", http.StatusBadRequest, "CF004")
			return
		}
		audit = append(audit, "zone", zone)
		var enabled bool
		enabled, err = uploadOwn(req.Context(), zone, body.Token)
		result["enabled"] = enabled
	case "reveal":
		bundle := pendingBundle()
		if bundle == nil {
			err = refuse("no_pending")
			break
		}
		if err = markHandedOver(bundle.fingerprint); err == nil {
			// shown once, to this session; never stored by a browser or a proxy
			w.Header().Set("Cache-Control", "no-store")
			result["certificate"] = bundle.certificate
			result["privateKey"] = bundle.privateKey
			result["fingerprint"] = bundle.fingerprint
		}
	case "discard":
		setPending(nil)
		err = updateConfig(func(config *utils.Config) error {
			dropUnused(&config.HTTPConfig.Cloudflare.OriginPulls, nil)
			return nil
		})
	case "mode":
		audit = append(audit, "mode", body.Mode)
		if body.Mode == "off" {
			body.Mode = utils.OriginPullOff
		}
		err = setMode(body.Mode, body.Anyway)
	case "confirm":
		err = confirmEnforcement()
	case "retire":
		if len(body.Fingerprint) != 64 || strings.Trim(body.Fingerprint, "0123456789abcdef") != "" {
			utils.HTTPError(w, "Invalid fingerprint", http.StatusBadRequest, "CF021")
			return
		}
		err = retireOwn(body.Fingerprint)
	default:
		utils.HTTPError(w, "Unknown action", http.StatusBadRequest, "CF013")
		return
	}

	var refused *refusal
	var apiErr *APIError
	switch {
	case errors.As(err, &refused):
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "error", "code": "CF020", "message": refused.Error(), "reason": refused.reason,
		})
		return
	case errors.As(err, &apiErr):
		apiFailure(w, err)
		return
	case err != nil:
		utils.HTTPError(w, err.Error(), http.StatusInternalServerError, "CF022")
		return
	}

	utils.AuditSecurity(req, utils.SecConfigSaved, "", audit...)
	result["originPulls"] = originPullsStatus(utils.GetMainConfig())
	answer(w, result)
}

// watchOriginPulls is run now and then: it ends an enforcement nobody
// confirmed, forgets a key that waited too long, and raises the reminders.
func watchOriginPulls(now time.Time) {
	cf := utils.GetMainConfig().HTTPConfig.Cloudflare
	pulls := cf.OriginPulls
	waiting := pendingBundle()

	lapsed := pulls.Mode == utils.OriginPullEnforce && !pulls.Confirmed && now.After(pulls.ConfirmBy)
	unused := false
	for _, own := range pulls.Own {
		if !own.HandedOver && (waiting == nil || waiting.fingerprint != own.Fingerprint) {
			unused = true
		}
	}
	if lapsed || unused {
		stopped := false
		err := updateConfig(func(config *utils.Config) error {
			stored := &config.HTTPConfig.Cloudflare.OriginPulls
			if stored.Mode == utils.OriginPullEnforce && !stored.Confirmed && now.After(stored.ConfirmBy) {
				stored.Mode = utils.OriginPullTest
				stored.Since = now.UTC().Truncate(time.Second)
				stored.ConfirmBy = time.Time{}
				stopped = true
			}
			dropUnused(stored, pendingBundle())
			return nil
		})
		if err == nil && stopped {
			utils.ResetOriginPullCounts()
			utils.Warn("[Cloudflare] enforcing Authenticated Origin Pulls was not confirmed within " + strconv.Itoa(int(utils.OriginPullConfirmWindow.Minutes())) + " minutes: back to testing, nothing is refused")
			notifyAdmins(TitleOriginPullStopped, MessageOriginPullStopped, "", "warning")
		}
	}

	remindOriginPulls(now)
}

// dueReminder returns the reminder to raise for a certificate with that many
// days left, given the last one raised (0 for none): the smallest step that
// was reached and not raised yet, 0 when there is none.
func dueReminder(steps []int, days int, reminded int) int {
	due := 0
	for _, step := range steps {
		if days <= step && (reminded == 0 || step < reminded) {
			due = step
		}
	}
	return due
}

// remindOriginPulls raises a notification when the certificate requests are
// verified with comes close to its end. Each step is raised once.
func remindOriginPulls(now time.Time) {
	cf := utils.GetMainConfig().HTTPConfig.Cloudflare
	pulls := cf.OriginPulls
	if pulls.Mode == utils.OriginPullOff {
		return
	}
	switch pulls.Kind {
	case utils.OriginPullShared:
		cert, err := utils.CloudflareOriginPullCA()
		if err != nil {
			return
		}
		days := daysLeft(cert.NotAfter, now)
		step := dueReminder(sharedReminders, days, pulls.SharedReminded)
		if step == 0 {
			return
		}
		if updateConfig(func(config *utils.Config) error {
			config.HTTPConfig.Cloudflare.OriginPulls.SharedReminded = step
			return nil
		}) == nil {
			notifyAdmins(TitleOriginPullExpiring, MessageOriginPullExpiring, expiryVars(days, cert.NotAfter), "warning")
		}

	case utils.OriginPullOwn:
		counts := utils.OriginPullSnapshot()
		accepted := handedOver(pulls)
		for _, own := range accepted {
			// one that a later certificate has taken over from needs no reminder
			replaced := false
			for _, other := range accepted {
				if _, verified := counts.Issuers[issuerFingerprint(other)]; verified && other.NotAfter.After(own.NotAfter) {
					replaced = true
				}
			}
			if replaced {
				continue
			}
			days := daysLeft(own.NotAfter, now)
			step := dueReminder(ownReminders, days, own.Reminded)
			if step == 0 {
				continue
			}
			fingerprint := own.Fingerprint
			if updateConfig(func(config *utils.Config) error {
				stored := config.HTTPConfig.Cloudflare.OriginPulls.Own
				for i := range stored {
					if stored[i].Fingerprint == fingerprint {
						stored[i].Reminded = step
					}
				}
				return nil
			}) == nil {
				notifyAdmins(TitleOriginPullExpiring, MessageOriginPullExpiring, expiryVars(days, own.NotAfter), "warning")
			}
		}
	}
}

func expiryVars(days int, until time.Time) string {
	return strconv.Itoa(days) + " days (" + until.UTC().Format("2006-01-02") + ")"
}

// startOriginPullWatch runs watchOriginPulls every 20 seconds.
func startOriginPullWatch() {
	go func() {
		for {
			time.Sleep(20 * time.Second)
			func() {
				defer func() { recover() }()
				watchOriginPulls(time.Now())
			}()
		}
	}()
}

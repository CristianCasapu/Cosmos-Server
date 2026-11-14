package main

// Fork addition: the security headers of the panel (its pages and files under
// /cosmos-ui/) and of its API (/cosmos/...). Applications reached through the
// proxy are not touched by anything here: their answers go through the
// proxy's own routes (proxy/routerGen.go), never through these routers.
//
// The panel's script policy (Content-Security-Policy):
//   - scripts only from the panel's own files: no inline script, no eval, no
//     other origin;
//   - styles from its own files and inline: MUI (emotion) writes its styles
//     into <style> elements at run time, and the charts and the terminal set
//     style attributes. The panel is a static build served as files
//     (utils.SPAHandler): no answer is templated, so there is no nonce to give
//     emotion, and style attributes cannot carry a nonce at all;
//   - pictures from anywhere over HTTPS, data: and blob: (the Market's icons
//     and screenshots come from the Market's own hosts; charts export blobs);
//   - connections to the own origin and to other addresses: the panel checks
//     whether each application answers on its own address, the Market's
//     installer reads compose files from the Market's hosts;
//   - fonts, the worker and the manifest from the own origin; no plugin, no
//     frame, no <base>; forms only to the own origin and to the addresses a
//     sign-in may end on (authorizationserver.ReturnOrigins); and no other
//     site may frame the panel.
// It is enforced. COSMOS_CSP_REPORT_ONLY=1 in the server's environment sends
// it as Content-Security-Policy-Report-Only instead (the browser then only
// reports what it would have refused, in its console): a way back without a
// new build if a page ever turns out to need something it forbids. There is
// no address the browsers report to: that would be an endpoint anyone can
// write to.
//
// The API's answers are data, never pages: they get a policy that allows
// nothing, so that an answer opened in a tab can neither run nor load anything.
//
// Strict-Transport-Security, on the panel and the API: only on a name whose
// certificate the public authorities vouch for, for a year, without
// includeSubDomains; "max-age=0" on a name with Cosmos' own certificate.
// The reasons:
//   - a browser that has the header for a name refuses that name for the
//     whole max-age as soon as its certificate is not trusted, with no way
//     to continue anyway. Cosmos' own certificate authority is trusted only
//     where it was installed, and changes when Cosmos makes a new one: a
//     device that trusted the old one would be locked out of the name;
//   - includeSubDomains would extend that to every name under the panel's
//     (an application on an HTTP-only zone, a device on the network with a
//     name under the same domain), and an address with a port other than 443
//     is upgraded on the same port, where nothing speaks TLS;
//   - "max-age=0" makes the browsers forget what upstream told them before
//     (a year, with includeSubDomains, on every HTTPS name); it only arrives
//     over a connection the browser trusts, which is where it was recorded;
//   - IP addresses never get the header (browsers ignore it there): the panel
//     always stays reachable by its address, whatever a name does.

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/authorizationserver"
	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/gorilla/mux"
)

const (
	forkCSPReportOnlyEnv = "COSMOS_CSP_REPORT_ONLY"

	forkAPIPolicy = "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

	forkReferrerPolicy = "same-origin"

	// the powerful features the panel never uses; the clipboard (copy
	// buttons, pasting a one-time code) and full screen stay allowed. Only
	// names every Chromium knows: an unknown one is an error in its console
	forkPermissionsPolicy = "accelerometer=(), autoplay=(), camera=(), display-capture=(), geolocation=(), gyroscope=(), hid=(), magnetometer=(), microphone=(), midi=(), payment=(), serial=(), usb=(), xr-spatial-tracking=()"

	forkHSTSPublic = "max-age=31536000"
	forkHSTSForget = "max-age=0"
)

// forkCSPEnforced is read once at start
var forkCSPEnforced = os.Getenv(forkCSPReportOnlyEnv) != "1"

// forkReturnOrigins is authorizationserver.ReturnOrigins, replaceable in tests
var forkReturnOrigins = authorizationserver.ReturnOrigins

// forkPanelPolicy is the panel's script policy, with the origins a sign-in may end on.
func forkPanelPolicy(returnOrigins []string) string {
	formAction := "'self'"
	for _, origin := range returnOrigins {
		formAction += " " + origin
	}
	return strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob: https:",
		"font-src 'self'",
		"connect-src 'self' https: http: wss: ws:",
		"media-src 'self'",
		"worker-src 'self'",
		"manifest-src 'self'",
		"frame-src 'none'",
		"object-src 'none'",
		"base-uri 'none'",
		"form-action " + formAction,
		"frame-ancestors 'none'",
	}, "; ")
}

// forkPanelHeaders is the hook of the panel's router (uirouter), after
// upstream's utils.SetSecurityHeaders, whose policy it replaces.
func forkPanelHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		policy := forkPanelPolicy(forkReturnOrigins())
		if forkCSPEnforced {
			h.Set("Content-Security-Policy", policy)
			h.Del("Content-Security-Policy-Report-Only")
		} else {
			// upstream's frame-ancestors stays enforced meanwhile
			h.Set("Content-Security-Policy-Report-Only", policy)
		}
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", forkReferrerPolicy)
		h.Set("Permissions-Policy", forkPermissionsPolicy)
		// browsers ignore it on plain HTTP, with an error in the console
		if r.TLS != nil {
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
		}
		forkHSTS(h, r)
		next.ServeHTTP(w, r)
	})
}

// forkAPIHeaders is the hook of the API's routers, after upstream's
// utils.SetSecurityHeaders.
func forkAPIHeaders(routers ...*mux.Router) {
	for _, router := range routers {
		router.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				h := w.Header()
				h.Set("Content-Security-Policy", forkAPIPolicy)
				h.Set("X-Content-Type-Options", "nosniff")
				h.Set("Referrer-Policy", forkReferrerPolicy)
				forkHSTS(h, r)
				next.ServeHTTP(w, r)
			})
		})
	}
}

// forkHSTS replaces upstream's Strict-Transport-Security (see the top of the file).
func forkHSTS(h http.Header, r *http.Request) {
	h.Del("Strict-Transport-Security")
	host := strings.ToLower(strings.TrimSuffix(utils.HostWithoutPort(r.Host), "."))
	if host == "" || net.ParseIP(strings.Trim(host, "[]")) != nil {
		return
	}
	// upstream's condition: the name is served over HTTPS here
	if !utils.HostServedOverHTTPS(r.Host) {
		return
	}
	if forkPubliclyTrusted(host) {
		h.Set("Strict-Transport-Security", forkHSTSPublic)
	} else {
		h.Set("Strict-Transport-Security", forkHSTSForget)
	}
}

// forkHSTSRoots are the authorities a certificate must chain to; nil is the
// system's (tests put their own)
var forkHSTSRoots *x509.CertPool

type forkTrustEntry struct {
	leaf    *x509.Certificate
	trusted bool
	checked time.Time
}

var (
	forkTrustMu    sync.Mutex
	forkTrustCache = map[[32]byte]forkTrustEntry{}
)

const (
	forkTrustRecheck  = time.Hour
	forkTrustMaxCache = 64
)

// forkPubliclyTrusted says whether the certificate this server serves for
// host chains to a public authority and names host. The chain is checked
// once an hour per certificate (not per name: names come from requests).
func forkPubliclyTrusted(host string) bool {
	cert := utils.GetHostCertificate(utils.GetMainConfig(), host)
	if cert.Source == "selfsigned" || cert.Source == "none" || !cert.Covered || cert.PEM == "" {
		return false
	}
	key := sha256.Sum256([]byte(cert.PEM))
	now := time.Now()

	forkTrustMu.Lock()
	entry, ok := forkTrustCache[key]
	forkTrustMu.Unlock()
	if !ok || now.Sub(entry.checked) > forkTrustRecheck {
		entry = forkCheckChain(cert.PEM, now)
		forkTrustMu.Lock()
		if len(forkTrustCache) >= forkTrustMaxCache {
			forkTrustCache = map[[32]byte]forkTrustEntry{}
		}
		forkTrustCache[key] = entry
		forkTrustMu.Unlock()
	}
	if !entry.trusted || entry.leaf == nil || now.After(entry.leaf.NotAfter) {
		return false
	}
	return entry.leaf.VerifyHostname(host) == nil
}

func forkCheckChain(certPEM string, now time.Time) forkTrustEntry {
	entry := forkTrustEntry{checked: now}
	intermediates := x509.NewCertPool()
	rest := []byte(certPEM)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		parsed, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return entry
		}
		if entry.leaf == nil {
			entry.leaf = parsed
		} else {
			intermediates.AddCert(parsed)
		}
	}
	if entry.leaf == nil {
		return entry
	}
	_, err := entry.leaf.Verify(x509.VerifyOptions{
		Roots:         forkHSTSRoots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	entry.trusted = err == nil
	return entry
}

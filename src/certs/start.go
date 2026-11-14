package certs

import (
	"context"
	"crypto/x509"
	"errors"
	"math/rand/v2"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/go-acme/lego/v5/acme/api"

	"github.com/azukaar/cosmos-server/src/utils"
)

// tickEvery is how often the certificates are looked at between restarts.
const tickEvery = 30 * time.Minute

// How often the CA is asked for its renewal window (RFC 9773; Let's
// Encrypt: at least twice a day), and how long to wait after it could not
// be asked.
const (
	ariEvery      = 12 * time.Hour
	ariAfterError = 6 * time.Hour
	ariShortest   = time.Minute
	ariLongest    = 24 * time.Hour
)

var startOnce sync.Once

// Start connects this package to upstream's certificate code (the function
// variables of utils/fork_certs.go) and starts the periodic look. Called
// when the server sets up its routes; calling it again does nothing.
func Start() {
	startOnce.Do(func() {
		utils.ForkObtain = obtainHook
		utils.ForkCertDue = certDue
		utils.ForkMayOrder = func(key string, names []string, force bool, now time.Time) bool {
			allowed, _, _ := store.mayOrder(key, names, force, now)
			return allowed
		}
		utils.ForkNoHTTPChallenge = noHTTPChallenge

		ca := Current()
		if ca.Kind != "production" {
			utils.Warn("Certificates: orders go to " + ca.Name + " (" + ca.Directory + "): its certificates are not trusted by browsers")
		}

		every := tickEvery
		if testCA != nil {
			if seconds, err := strconv.Atoi(os.Getenv(envTestTick)); err == nil && seconds > 0 {
				every = time.Duration(seconds) * time.Second
			}
		}
		go func() {
			for {
				time.Sleep(every)
				Tick()
			}
		}()
	})
}

// certDue is utils.ForkCertDue: has the certificate to be renewed. An
// admin asked for it; or it came from another CA than the one in use (a
// test or staging certificate once the server orders for real); or its
// renewal moment has come.
func certDue(key string, cert utils.ZoneCert, now time.Time) bool {
	if store.isForced(key) {
		return true
	}
	leaf := utils.ForkLeaf(cert.TLSCert)
	if leaf == nil {
		// not readable: upstream's rule on the date it stored
		return !now.Add(45 * 24 * time.Hour).Before(cert.ValidUntil)
	}
	known := store.get(key)
	if known.IssuedSerial == leaf.SerialNumber.String() && known.IssuedDirectory != "" && known.IssuedDirectory != Current().Directory {
		return true
	}
	moment, _ := renewMoment(leaf, known.ARI)
	return !now.Before(moment)
}

var tickLock sync.Mutex

// Tick is the periodic look: ask the CA for its renewal windows, order
// what is due (certificates through DNS need no restart), raise the
// notices. Certificates over HTTP-01 are left to upstream's nightly check,
// which restarts the server: their order needs the HTTP port.
func Tick() {
	if !tickLock.TryLock() {
		return
	}
	defer tickLock.Unlock()
	defer func() {
		if r := recover(); r != nil {
			utils.Error("Certificates: the periodic check stopped", errors.New("panic"))
		}
	}()

	config := utils.GetMainConfig()
	now := clock()
	refreshRenewalInfo(config, now)

	if utils.IsZoneIssuer() && !utils.ServerHTTPSDisabled(config.HTTPConfig) && len(utils.ZoneCertsToIssue(config, false)) > 0 {
		utils.Log("Certificates: a certificate is due, ordering")
		utils.RefreshZoneCerts()
	}

	raiseNotices(GetStatus(), clock())
}

// renewalInfo asks the CA for the renewal window of a certificate. A
// variable for the tests.
var renewalInfo = func(ctx context.Context, ca CA, email string, leaf *x509.Certificate) (start time.Time, end time.Time, again time.Duration, err error) {
	client, failure := acmeClient(ctx, ca, email)
	if failure != nil {
		return time.Time{}, time.Time{}, 0, failure
	}
	info, err := client.Certificate.GetRenewalInfo(ctx, leaf)
	if err != nil {
		return time.Time{}, time.Time{}, 0, err
	}
	return info.SuggestedWindow.Start, info.SuggestedWindow.End, info.RetryAfter, nil
}

// refreshRenewalInfo keeps the CA's renewal window of each certificate this
// server ordered from the CA in use. Nothing is asked when no account
// exists yet: asking must never be what registers one.
func refreshRenewalInfo(config utils.Config, now time.Time) {
	ca := Current()
	if loadAccount(ca) == nil {
		return
	}
	ask := func(key string, cert utils.ZoneCert) {
		leaf := utils.ForkLeaf(cert.TLSCert)
		if leaf == nil || now.After(leaf.NotAfter) {
			return
		}
		serial := leaf.SerialNumber.String()
		known := store.get(key)
		if known.IssuedSerial != serial || known.IssuedDirectory != ca.Directory {
			return
		}
		if known.ARI != nil && known.ARI.Serial == serial && known.ARI.Again > now.Unix() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		start, end, again, err := renewalInfo(ctx, ca, config.HTTPConfig.SSLEmail, leaf)
		cancel()
		store.update(key, func(state *certState) {
			if state.ARI == nil || state.ARI.Serial != serial {
				state.ARI = &ariState{Serial: serial}
			}
			state.ARI.Checked = now.Unix()
			switch {
			case errors.Is(err, api.ErrNoARI):
				state.ARI.Again = now.Add(ariLongest).Unix()
			case err != nil:
				state.ARI.Again = now.Add(ariAfterError).Unix()
			default:
				if again <= 0 {
					again = ariEvery
				}
				if again < ariShortest {
					again = ariShortest
				}
				if again > ariLongest {
					again = ariLongest
				}
				state.ARI.Again = now.Add(again).Unix()
				// the moment is drawn once per window: drawing again at
				// every look would drift towards the window's start
				if state.ARI.Start != start.Unix() || state.ARI.End != end.Unix() || state.ARI.Pick == 0 {
					state.ARI.Start, state.ARI.End = start.Unix(), end.Unix()
					state.ARI.Pick = pickInWindow(start, end, rand.Float64()).Unix()
				}
			}
		})
	}
	for zone, cert := range config.HTTPConfig.ZoneCerts {
		ask(utils.ForkZoneCertKey(zone), cert)
	}
	for zone, cert := range config.HTTPConfig.LocalCerts {
		ask(utils.ForkLocalCertKey(zone), cert)
	}
}

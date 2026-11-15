package upnp

// Fork addition: a forward the router lost, or would not make, is asked for
// again by itself.
//
// Before, one failed request (a router that closed the connection while it
// restarted its UPnP service is enough) stopped Cosmos from asking until an
// administrator pressed "Check now" in the panel, and a forward that the
// router dropped was only noticed at the next renewal, up to twenty minutes
// later. On a server reached from outside through that very forward, the
// administrator who is away cannot open the panel to press anything: the
// server stayed unreachable until someone at home did.
//
// Now a failure is tried again after a pause that grows: a router that did
// not answer or broke the connection after 1, 2, 5 and 10 minutes, then
// every 15; a request the router answered with a refusal (a port taken by
// someone else, not allowed) after 15 and 30 minutes, then every hour, since
// only a change on the router can make it succeed. In between, every five
// minutes, the router is asked whether the forwards of Cosmos' own ports are
// still there; when one is gone, it is made again at once.
// "Check now", a restart and a change of the settings try everything
// immediately, as before. Administrators are told once when a failure
// starts, not at every retry.

import (
	"errors"
	"strings"
	"time"
)

var (
	unreachablePauses = []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute}
	refusedPauses     = []time.Duration{15 * time.Minute, 30 * time.Minute, time.Hour}
	// how often the router is asked whether Cosmos' own forwards are still there
	probeInterval = 5 * time.Minute
)

// retry is when something that failed is tried again.
type retry struct {
	attempts int
	next     time.Time
}

var (
	routerRetry  retry                // the router could not be reached
	mappingRetry = map[string]retry{} // by key: a forward that could not be made
	lastProbe    time.Time
	lastGateway  *gateway // the router as it was last reached, for the probe
)

// pauseAfter returns how long to wait after the n-th failure in a row.
func pauseAfter(pauses []time.Duration, attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > len(pauses) {
		attempts = len(pauses)
	}
	return pauses[attempts-1]
}

// again notes one more failure and returns when to try next.
func (r retry) again(now time.Time, pauses []time.Duration) retry {
	r.attempts++
	r.next = now.Add(pauseAfter(pauses, r.attempts))
	return r
}

// retryDue reports whether a forward that failed is to be tried again now.
func retryDue(key string, now time.Time) bool {
	r, waiting := mappingRetry[key]
	return waiting && !now.Before(r.next)
}

// anyRetryDue reports whether a pass is needed because a failed forward's
// pause is over.
func anyRetryDue(now time.Time) bool {
	for key := range mappingRetry {
		if _, stillFailed := failed[key]; stillFailed && retryDue(key, now) {
			return true
		}
	}
	return false
}

// resetRetries forgets every pause: someone asked for a pass.
func resetRetries() {
	routerRetry = retry{}
	mappingRetry = map[string]retry{}
}

// ownForwardLost asks the router, at most every probeInterval, whether the
// forwards of Cosmos' own ports are still what Cosmos made. True when one is
// gone or leads elsewhere, or when the router does not answer (the pass that
// follows finds out which, and starts the pauses if it is the router).
func ownForwardLost(now time.Time) bool {
	if now.Sub(lastProbe) < probeInterval {
		return false
	}
	lastProbe = now
	g := lastGateway
	if g == nil {
		return false
	}
	local := g.localIP.String()
	for key, h := range held {
		if !strings.HasPrefix(key, "cosmos:") {
			continue // an app's port: renewed with the others
		}
		found, err := g.lookup(h.Protocol, h.ExternalPort)
		if err != nil {
			if routerAnswered(err) {
				continue // a router without this question: the renewal stays the only check
			}
			return true
		}
		if found == nil || found.InternalClient != local || found.InternalPort != h.InternalPort {
			return true
		}
	}
	return false
}

// routerAnswered reports whether an error is the router's own answer (a
// refusal with a UPnP error code) rather than a router that could not be
// reached or broke the connection.
func routerAnswered(err error) bool {
	var answer *soapError
	return errors.As(err, &answer)
}

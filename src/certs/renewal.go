package certs

import (
	"crypto/x509"
	"time"
)

// When a certificate is renewed, from its own lifetime. Let's Encrypt is
// moving from 90 days to 64 (February 2027) and 45 (February 2028), and
// also issues six-day certificates: a fixed number of days before the end
// is wrong for at least one of them. Its advice, followed here: with a
// third of the lifetime left, at half for lifetimes under ten days.

const shortLifetime = 10 * 24 * time.Hour

// renewAfter is the moment from which a certificate valid between the two
// dates is renewed, when the CA said nothing else.
func renewAfter(notBefore time.Time, notAfter time.Time) time.Time {
	life := notAfter.Sub(notBefore)
	if life <= 0 {
		return notAfter
	}
	if life < shortLifetime {
		return notBefore.Add(life / 2)
	}
	return notAfter.Add(-life / 3)
}

// warnAfter is the moment from which a certificate that is still not
// renewed is worth a notice: half of the renewal margin is gone (15 days
// before the end for 90 days, 7.5 for 45, 20 hours for 160 hours).
func warnAfter(notBefore time.Time, notAfter time.Time) time.Time {
	margin := notAfter.Sub(renewAfter(notBefore, notAfter))
	return notAfter.Add(-margin / 2)
}

// renewMoment is when a certificate is due, and where that moment comes
// from: "ca" when the CA gave a renewal window for this very certificate
// (ARI; the moment was drawn from the window once and kept), "lifetime"
// otherwise. The CA's moment is followed when it is earlier (it may be
// revoking), and when later only up to the start of the warning window.
func renewMoment(leaf *x509.Certificate, ari *ariState) (time.Time, string) {
	byLifetime := renewAfter(leaf.NotBefore, leaf.NotAfter)
	if ari == nil || ari.Pick == 0 || ari.Serial != leaf.SerialNumber.String() {
		return byLifetime, "lifetime"
	}
	pick := time.Unix(ari.Pick, 0)
	if latest := warnAfter(leaf.NotBefore, leaf.NotAfter); pick.After(latest) {
		pick = latest
	}
	return pick, "ca"
}

// pickInWindow draws the moment of renewal from the CA's window. random is
// a number in [0, 1).
func pickInWindow(start time.Time, end time.Time, random float64) time.Time {
	if !end.After(start) {
		return start
	}
	return start.Add(time.Duration(float64(end.Sub(start)) * random))
}

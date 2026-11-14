package certs

import (
	"crypto/x509"
	"math/big"
	"testing"
	"time"
)

const day = 24 * time.Hour

func TestRenewalFollowsTheLifetime(t *testing.T) {
	cases := []struct {
		name  string
		life  time.Duration
		renew time.Duration // after the start
		warn  time.Duration // after the start
	}{
		{"90 days: with 30 left", 90 * day, 60 * day, 75 * day},
		{"64 days (Let's Encrypt from February 2027)", 64 * day, 64 * day * 2 / 3, 64*day - 64*day/6},
		{"45 days (from February 2028): with 15 left", 45 * day, 30 * day, 45*day - 180*time.Hour},
		{"160 hours (short-lived): at half", 160 * time.Hour, 80 * time.Hour, 120 * time.Hour},
		{"200 days: with a third left", 200 * day, 200 * day * 2 / 3, 200*day - 200*day/6},
	}
	for _, c := range cases {
		from := t0
		to := t0.Add(c.life)
		if got := renewAfter(from, to).Sub(from); got/time.Minute != c.renew/time.Minute {
			t.Errorf("%s: renewed after %v, want %v", c.name, got, c.renew)
		}
		if got := warnAfter(from, to).Sub(from); got/time.Minute != c.warn/time.Minute {
			t.Errorf("%s: warned after %v, want %v", c.name, got, c.warn)
		}
		if !renewAfter(from, to).Before(warnAfter(from, to)) || !warnAfter(from, to).Before(to) {
			t.Errorf("%s: renewal, warning and end are out of order", c.name)
		}
	}
	// upstream's rule, for the record: 45 days before the end is "always"
	// for a 45-day certificate
	if renewAfter(t0, t0.Add(45*day)).Sub(t0) < 29*day {
		t.Error("a 45-day certificate must not be due from its first day")
	}
	if got := renewAfter(t0, t0); !got.Equal(t0) {
		t.Errorf("a certificate without a lifetime is due at once, got %v", got)
	}
}

func TestTheCAsRenewalWindowIsFollowedWithinLimits(t *testing.T) {
	leaf := &x509.Certificate{SerialNumber: big.NewInt(42), NotBefore: t0, NotAfter: t0.Add(90 * day)}
	byLifetime := t0.Add(60 * day)

	if moment, source := renewMoment(leaf, nil); !moment.Equal(byLifetime) || source != "lifetime" {
		t.Errorf("without a window: %v by %s", moment, source)
	}
	// a window for another certificate is not this one's
	other := &ariState{Serial: "7", Pick: t0.Add(10 * day).Unix()}
	if moment, source := renewMoment(leaf, other); !moment.Equal(byLifetime) || source != "lifetime" {
		t.Errorf("another certificate's window was used: %v by %s", moment, source)
	}
	// earlier than the lifetime rule (the CA is about to revoke): followed
	early := &ariState{Serial: "42", Pick: t0.Add(3 * day).Unix()}
	if moment, source := renewMoment(leaf, early); !moment.Equal(t0.Add(3*day)) || source != "ca" {
		t.Errorf("an early window: %v by %s", moment, source)
	}
	// later: followed up to the start of the warning window, not beyond
	late := &ariState{Serial: "42", Pick: t0.Add(70 * day).Unix()}
	if moment, _ := renewMoment(leaf, late); !moment.Equal(t0.Add(70 * day)) {
		t.Errorf("a later window: %v", moment)
	}
	tooLate := &ariState{Serial: "42", Pick: t0.Add(89 * day).Unix()}
	if moment, _ := renewMoment(leaf, tooLate); !moment.Equal(t0.Add(75 * day)) {
		t.Errorf("a window at the very end must be cut at the warning window, got %v", moment)
	}
}

func TestTheMomentIsDrawnInsideTheWindow(t *testing.T) {
	start, end := t0, t0.Add(48*time.Hour)
	for _, random := range []float64{0, 0.25, 0.999999} {
		pick := pickInWindow(start, end, random)
		if pick.Before(start) || !pick.Before(end) {
			t.Errorf("random %v gave %v, outside the window", random, pick)
		}
	}
	if pick := pickInWindow(start, end, 0.5); !pick.Equal(t0.Add(24 * time.Hour)) {
		t.Errorf("the middle of the window is %v", pick)
	}
	if pick := pickInWindow(end, start, 0.5); !pick.Equal(end) {
		t.Errorf("a window that ends before it starts gives its start, got %v", pick)
	}
}

func TestDueFromTheCertificateItself(t *testing.T) {
	env := setup(t, nil)
	key := "zone:example.org"
	// the stored end says 90 days from now, as upstream wrote it; the
	// certificate itself lasts 45 days and was issued 31 days ago
	cert := env.zoneCert(t, env.ca, "example.org", []string{"example.org", "*.example.org"}, t0.Add(-31*day), t0.Add(14*day))
	cert.ValidUntil = t0.Add(90 * day)
	if !certDue(key, cert, env.now) {
		t.Error("a 45-day certificate with 14 days left is due")
	}
	fresh := env.zoneCert(t, env.ca, "example.org", []string{"example.org"}, t0.Add(-10*day), t0.Add(35*day))
	if certDue(key, fresh, env.now) {
		t.Error("a 45-day certificate with 35 days left is not due (upstream's 45-day rule would say it is)")
	}
	// an admin asked
	store.force(key)
	if !certDue(key, fresh, env.now) {
		t.Error("\"Renew now\" makes the certificate due")
	}
	store.unforce(key)

	// it came from the staging authority and orders now go to production
	leaf := parseLeaf(t, fresh.TLSCert)
	store.update(key, func(state *certState) {
		state.IssuedSerial = leaf.SerialNumber.String()
		state.IssuedDirectory = letsEncryptStaging
	})
	if !certDue(key, fresh, env.now) {
		t.Error("a certificate of another authority than the one in use is due")
	}
	store.update(key, func(state *certState) { state.IssuedDirectory = letsEncryptProduction })
	if certDue(key, fresh, env.now) {
		t.Error("a certificate of the authority in use is not due before its time")
	}
	// the CA's window says now
	store.update(key, func(state *certState) {
		state.ARI = &ariState{Serial: leaf.SerialNumber.String(), Pick: env.now.Add(-time.Hour).Unix()}
	})
	if !certDue(key, fresh, env.now) {
		t.Error("a renewal moment of the CA that has passed makes the certificate due")
	}
	// a text that is no certificate falls back to the stored date
	if certDue(key, cert0(t0.Add(60*day)), env.now) || !certDue(key, cert0(t0.Add(40*day)), env.now) {
		t.Error("an unreadable certificate follows the stored end, 45 days before it")
	}
}

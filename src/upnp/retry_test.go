package upnp

import (
	"errors"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// What happened on a real server: the router restarted its UPnP service,
// lost every forward and closed the connection on the request that asked
// for Cosmos' port again. Cosmos must notice that the forward is gone and
// get it back by itself, without anyone pressing "Check now".
func TestALostForwardComesBackByItself(t *testing.T) {
	w := newWorld(t)
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = true })
	if st := reconcile(true); !own(st, "HTTPS").Active {
		t.Fatalf("at the start: %+v", own(st, "HTTPS"))
	}

	w.router.reboot()
	w.router.breakAdd = true
	w.minutes(6) // the forward is looked for every five minutes
	st := GetStatus()
	if own(st, "HTTPS").Active || own(st, "HTTPS").State != StateFailed {
		t.Fatalf("with the forward gone and the router breaking the connection: %+v", own(st, "HTTPS"))
	}
	if _, there := w.router.get("TCP/2053"); there {
		t.Fatal("the fake router made the forward although it breaks the connection")
	}
	asked := w.router.count("AddPortMapping", "TCP/2053")

	w.router.breakAdd = false
	w.minutes(3)
	st = GetStatus()
	if !own(st, "HTTPS").Active || own(st, "HTTPS").Paused {
		t.Fatalf("after the router works again: %+v (asked %d times before, %d now)", own(st, "HTTPS"), asked, w.router.count("AddPortMapping", "TCP/2053"))
	}
	if forward, there := w.router.get("TCP/2053"); !there || forward.port != "2053" {
		t.Errorf("the router's table: %+v %v", forward, there)
	}
	if len(w.notices) != 1 {
		t.Errorf("the admins were told %d times, want once: %v", len(w.notices), w.notices)
	}
}

// A forward that the router dropped while it otherwise works is made again
// within the five minutes of the next look, not at the next renewal.
func TestADroppedForwardIsNoticedWithinFiveMinutes(t *testing.T) {
	w := newWorld(t)
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = true })
	reconcile(true)
	w.minutes(2)
	w.router.expire("TCP/2053")
	w.minutes(5)
	if _, there := w.router.get("TCP/2053"); !there {
		t.Fatal("five minutes after the router dropped the forward it is still gone")
	}
	if !own(GetStatus(), "HTTPS").Active || len(w.notices) != 0 {
		t.Errorf("status %+v, notices %v", own(GetStatus(), "HTTPS"), w.notices)
	}
}

// The look costs the router one question per forward of Cosmos' own every
// five minutes, no more, and nothing at all for apps' ports.
func TestTheLookIsRare(t *testing.T) {
	w := newWorld(t)
	w.containers = []Container{container("game", true, udp(27015, 27015))}
	w.config(func(c *utils.Config) {
		c.HTTPConfig.UPnP.Enabled = true
		c.HTTPConfig.UPnP.AppPorts = []utils.UPnPAppPort{{Container: "game", Port: 27015, Protocol: "udp"}}
	})
	reconcile(true)
	before := w.router.count("GetSpecificPortMappingEntry", "TCP/2053")
	beforeApp := w.router.count("GetSpecificPortMappingEntry", "UDP/27015")
	w.minutes(4)
	if got := w.router.count("GetSpecificPortMappingEntry", "TCP/2053"); got != before {
		t.Errorf("asked %d times within four minutes", got-before)
	}
	w.minutes(1)
	if got := w.router.count("GetSpecificPortMappingEntry", "TCP/2053"); got != before+1 {
		t.Errorf("asked %d times within five minutes, want once", got-before)
	}
	if got := w.router.count("GetSpecificPortMappingEntry", "UDP/27015"); got != beforeApp {
		t.Errorf("an app's port was looked for %d times", got-beforeApp)
	}
}

// A request the router answers with a refusal is asked again rarely: only a
// change on the router can make it succeed.
func TestARefusedForwardIsAskedAgainRarely(t *testing.T) {
	w := newWorld(t)
	w.router.refuse["TCP/2053"] = 606
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = true })
	st := reconcile(true)
	if own(st, "HTTPS").Active || !own(st, "HTTPS").Paused {
		t.Fatalf("refused: %+v", own(st, "HTTPS"))
	}
	first := w.router.count("AddPortMapping", "TCP/2053")
	w.minutes(14)
	if got := w.router.count("AddPortMapping", "TCP/2053"); got != first {
		t.Errorf("asked again %d times within 14 minutes", got-first)
	}
	w.minutes(2)
	if got := w.router.count("AddPortMapping", "TCP/2053"); got != first+1 {
		t.Errorf("asked again %d times within 16 minutes, want once", got-first)
	}
	delete(w.router.refuse, "TCP/2053")
	w.minutes(31) // the second pause is 30 minutes
	if !own(GetStatus(), "HTTPS").Active {
		t.Errorf("after the router allows it: %+v", own(GetStatus(), "HTTPS"))
	}
	if len(w.notices) != 1 {
		t.Errorf("the admins were told %d times, want once: %v", len(w.notices), w.notices)
	}
}

// A router that comes back is used again without anyone asking.
func TestAnUnreachableRouterIsFoundAgain(t *testing.T) {
	w := newWorld(t)
	w.routerErr = errors.New("no UPnP router answered")
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = true })
	if st := reconcile(true); !st.Paused {
		t.Fatalf("with no router: %+v", st)
	}
	w.minutes(7)
	w.routerErr = nil
	w.minutes(2) // the next try is at minute 8
	st := GetStatus()
	if st.Paused || !own(st, "HTTPS").Active || len(w.notices) != 1 {
		t.Errorf("with the router back: paused=%v https=%+v notices=%d", st.Paused, own(st, "HTTPS"), len(w.notices))
	}
}

func TestPausesGrowAndStop(t *testing.T) {
	var r retry
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	want := []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	for i, pause := range want {
		r = r.again(now, unreachablePauses)
		if r.next.Sub(now) != pause {
			t.Errorf("failure %d: tried again after %v, want %v", i+1, r.next.Sub(now), pause)
		}
	}
	if pauseAfter(refusedPauses, 0) != 15*time.Minute || pauseAfter(refusedPauses, 99) != time.Hour {
		t.Error("the pauses after a refusal")
	}
}

// A forward the router shows as it should be is not written over: on a real
// router that dropped the forward and closed the connection. The router is
// only asked whether it is still there.
func TestAForwardThatIsThereIsLeftAlone(t *testing.T) {
	for name, left := range map[string]string{"a router that gives no time": "0", "a router with plenty of time left": "3000"} {
		w := newWorld(t)
		w.router.leaseLeft = left
		if left == "0" {
			w.router.onlyPermanent = true // forwards without an end are all it knows
		}
		w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = true })
		if st := reconcile(true); !own(st, "HTTPS").Active {
			t.Fatalf("%s, at the start: %+v", name, own(st, "HTTPS"))
		}
		asked := w.router.count("AddPortMapping", "TCP/2053")
		w.minutes(125)
		if got := w.router.count("AddPortMapping", "TCP/2053"); got != asked {
			t.Errorf("%s: the forward was written over %d times in two hours", name, got-asked)
		}
		if !own(GetStatus(), "HTTPS").Active || len(w.notices) != 0 {
			t.Errorf("%s: status %+v, notices %v", name, own(GetStatus(), "HTTPS"), w.notices)
		}
		// and when it goes away it is made again
		w.router.expire("TCP/2053")
		w.minutes(6)
		if _, there := w.router.get("TCP/2053"); !there {
			t.Errorf("%s: a forward that went away was not made again", name)
		}
	}
}

// A forward whose time on the router is running out is asked for again.
func TestAForwardAboutToEndIsRenewed(t *testing.T) {
	w := newWorld(t)
	w.router.leaseLeft = "900"
	w.config(func(c *utils.Config) { c.HTTPConfig.UPnP.Enabled = true })
	reconcile(true)
	asked := w.router.count("AddPortMapping", "TCP/2053")
	w.minutes(41)
	if got := w.router.count("AddPortMapping", "TCP/2053"); got != asked+2 {
		t.Errorf("renewed %d times in 41 minutes, want 2", got-asked)
	}
}

func TestLeftAlone(t *testing.T) {
	for left, want := range map[int]bool{0: true, 3600: true, 2401: true, 2400: false, 1200: false, 1: false} {
		if leftAlone(left) != want {
			t.Errorf("a forward with %d seconds left: left alone = %v, want %v", left, !want, want)
		}
	}
}

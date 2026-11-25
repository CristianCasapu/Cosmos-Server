package watch

import (
	"errors"
	"testing"
)

func TestASignInFromANewCountry(t *testing.T) {
	r := record(t)
	st := newStore("")
	asked := []string{}
	countries := map[string]string{"203.0.113.7": "RO", "198.51.100.9": "PT", "2001:db8::1": "DE"}
	lookup := func(ip string) (string, error) {
		asked = append(asked, ip)
		return countries[ip], nil
	}
	// the first sign-in of an account only records
	checkSignIn(st, "alice", "203.0.113.7", lookup)
	checkSignIn(st, "alice", "203.0.113.7", lookup)
	// home, VPN, carrier's shared and loopback addresses are never a country
	for _, ip := range []string{"192.168.1.20", "10.8.0.2", "100.64.3.4", "127.0.0.1", "fd00::1", "fe80::1", "not an address"} {
		checkSignIn(st, "alice", ip, lookup)
	}
	if len(r.written) != 0 {
		t.Fatalf("%s", r.kinds())
	}
	for _, ip := range asked {
		if !(ip == "203.0.113.7") {
			t.Errorf("a private address was looked up: %s", ip)
		}
	}
	checkSignIn(st, "alice", "198.51.100.9", lookup)
	if r.kinds() != "login.newcountry" {
		t.Fatalf("%s", r.kinds())
	}
	what := r.written[0].Fork.What.Vars
	if what["country"] != "PT" || what["known"] != "RO" || what["user"] != "alice" || r.written[0].Level != "warning" {
		t.Errorf("%+v", what)
	}
	checkSignIn(st, "alice", "198.51.100.9", lookup)
	if len(r.written) != 1 {
		t.Error("the same new country told twice")
	}
	// what is kept: codes only, never an address
	for _, list := range st.data.Countries {
		for _, code := range list {
			if len(code) != 2 {
				t.Errorf("kept %q", code)
			}
		}
	}
	// an unknown or odd answer of the lookup is no country
	checkSignIn(st, "bob", "2001:db8::2", func(string) (string, error) { return "<b>", nil })
	checkSignIn(st, "bob", "2001:db8::2", func(string) (string, error) { return "", errors.New("no file") })
	if _, has := st.data.Countries["bob"]; has {
		t.Error("an odd answer was recorded")
	}
}

func TestTheStateIsKeptAcrossARestart(t *testing.T) {
	path := t.TempDir() + "/" + stateFileName
	lookup := func(string) (string, error) { return "RO", nil }
	checkSignIn(newStore(path), "alice", "203.0.113.7", lookup)
	st := newStore(path)
	st.with(func(d *stateData) bool {
		if len(d.Countries["alice"]) != 1 {
			t.Errorf("after a restart: %v", d.Countries)
		}
		return false
	})
}

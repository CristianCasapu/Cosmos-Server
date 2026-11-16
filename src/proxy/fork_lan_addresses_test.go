package proxy

import "testing"

// Only private IPv4 addresses of the chosen interfaces, each once, bounded.
func TestLANAddressesArePrivateIPv4Only(t *testing.T) {
	prev := chooseLANForAddresses
	defer func() { chooseLANForAddresses = prev }()
	chooseLANForAddresses = func() []lanInterface {
		return []lanInterface{
			{Name: "eno1", Address: "192.168.1.10", Default: true},
			{Name: "eno2", Address: "198.51.100.7"}, // a public address: never
			{Name: "eno3", Address: "10.0.0.5"},
			{Name: "eno4", Address: "192.168.1.10"},
			{Name: "eno5", Address: "fd00::5"},
		}
	}
	got := LANAddresses()
	if len(got) != 2 || got[0].Address != "192.168.1.10" || !got[0].Default || got[1].Address != "10.0.0.5" {
		t.Fatalf("%+v", got)
	}
}

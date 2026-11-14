package configapi

import (
	"strings"
	"testing"
)

func TestValidateMDNSInterfaces(t *testing.T) {
	for _, ok := range [][]string{nil, {}, {"eno1"}, {"eth0.100", "enp3s0", "wlan0", "br0", "bond0:1", "veth@if2"}} {
		if msg := validateMDNSInterfaces(ok); msg != "" {
			t.Fatalf("%v refused: %s", ok, msg)
		}
	}
	for _, bad := range [][]string{{""}, {"eth 0"}, {"-eth"}, {"eth0;rm"}, {"abcdefghijklmnop"}, {"eno1", "eno1"}, strings.Split("a,b,c,d,e,f,g,h,i", ",")} {
		if validateMDNSInterfaces(bad) == "" {
			t.Fatalf("%v accepted", bad)
		}
	}
}

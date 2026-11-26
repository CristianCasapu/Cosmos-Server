package protection

import (
	"os"
	"testing"
)

// The tables the installer really loads (fork/nftables/install.sh on a
// Debian 13 test machine of scripts/fork-guard-e2e.sh, `nft -j list table`):
// a home behind a router with an SSH allowance from outside, and a server
// with a public IPv4 address. The card must read them as they are.
func TestTablesTheInstallerLoads(t *testing.T) {
	read := func(name string) Table {
		t.Helper()
		out, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		table, err := parseTable(out)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return table
	}

	nat := read("nft-guard-real-nat.json")
	if !nat.Loaded || !nat.InputHooked || !nat.ForwardHooked || !nat.InputFilters || !nat.ForwardFilters {
		t.Errorf("home behind a router: not read as filtering: %+v", nat)
	}
	if !nat.IPv4Passed {
		t.Error("home behind a router: IPv4 is passed by the installer's rule, read as filtered")
	}
	if len(nat.LANInterfaces) != 1 || nat.LANInterfaces[0] != "eth0" || len(nat.WANInterfaces) != 1 {
		t.Errorf("home behind a router: interfaces %v / lan %v", nat.WANInterfaces, nat.LANInterfaces)
	}
	if len(nat.SSHAllowed) != 1 || nat.SSHAllowed[0].Address != "2001:db8:2::20" || nat.SSHAllowed[0].Expires == nil || *nat.SSHAllowed[0].Expires < 86000 {
		t.Errorf("home behind a router: the SSH allowance with its expiry was not read: %+v", nat.SSHAllowed)
	}
	if len(nat.SSHPorts) == 0 || nat.SSHPorts[0] != 22 || len(nat.PublicTCP) != 2 {
		t.Errorf("home behind a router: ports %v ssh %v", nat.PublicTCP, nat.SSHPorts)
	}

	pub := read("nft-guard-real-pub.json")
	if !pub.InputFilters || pub.IPv4Passed {
		t.Errorf("public IPv4: should filter IPv4 too: %+v", pub)
	}
	if len(pub.LANInterfaces) != 0 || len(pub.WANInterfaces) != 1 || len(pub.FailClosedInterfaces) != 0 {
		t.Errorf("public IPv4: the provider's segment must not be home: wan %v lan %v", pub.WANInterfaces, pub.LANInterfaces)
	}
	if pub.DroppedSpoofed == nil {
		t.Error("public IPv4: the forged-source counter was not read")
	}
}

package constellation

// The resolver of the server itself, pointed at the Constellation DNS while it
// runs, the way the desktop client does it (beyond-cloud-app desktop/linux.go).

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// 0. Using systemd-resolved directly
//
// resolvedActive reports whether systemd-resolved is running. /run/systemd/resolve
// only exists while the daemon runs, so it distinguishes an active resolved from a
// merely installed resolvectl binary.
func resolvedActive() bool {
	if exec.Command("which", "resolvectl").Run() != nil {
		return false
	}
	info, err := os.Stat("/run/systemd/resolve")
	return err == nil && info.IsDir()
}

// setDNSWithResolvectl publishes the tunnel resolvers straight to systemd-resolved.
//
// Going through NetworkManager is not enough on resolved systems: NM accepts
// `device modify ipv4.dns ...` and updates its own DnsManager state, but has been
// observed never forwarding the per-link DNS to resolved for the tun device, so
// queries keep flowing to the LAN resolver. resolved also needs a routing domain
// before it will steer queries at a non-default-route link, which NM does not
// derive from ipv4.dns-priority. Talking to resolved directly fixes both, and the
// settings are runtime-only: they die with the tun device, nothing is persisted.
func setDNSWithResolvectl(adapter, dns string) error {
	args := append([]string{"dns", adapter}, strings.Fields(dns)...)
	if err := exec.Command("resolvectl", args...).Run(); err != nil {
		return fmt.Errorf("failed to set DNS with resolvectl for adapter %s: %w", adapter, err)
	}

	// Route every lookup through the tunnel resolvers, mirroring the exclusive
	// intent of ipv4.dns-priority -1 (and of the macOS/Windows behaviour, which
	// point the whole system at these resolvers while connected).
	if err := exec.Command("resolvectl", "domain", adapter, "~.").Run(); err != nil {
		return fmt.Errorf("failed to set DNS routing domain with resolvectl for adapter %s: %w", adapter, err)
	}
	if err := exec.Command("resolvectl", "default-route", adapter, "true").Run(); err != nil {
		return fmt.Errorf("failed to set DNS default route with resolvectl for adapter %s: %w", adapter, err)
	}
	// answers cached from the previous resolver, or from a Cosmos DNS that did not
	// know the cluster yet, must not outlive the switch
	_ = exec.Command("resolvectl", "flush-caches").Run()

	return nil
}

func resetDNSWithResolvectl(adapter string) error {
	// Per-link resolved state dies with the tun device, so a failure here (device
	// already gone) is harmless.
	return exec.Command("resolvectl", "revert", adapter).Run()
}

// 1. Using nmcli
func setDNSWithNmcli(adapter, dns string) error {
	// `device modify` applies the settings to the device's active connection at
	// runtime only - nothing is written to disk - and reapplies them in the same
	// step (no down/up cycle, which would race nebula's tun read loop and crash
	// the process with "read /dev/net/tun: bad address").
	cmd := exec.Command("nmcli", "device", "modify", adapter,
		"ipv4.dns", dns,
		"ipv4.dns-priority", "-1")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to set DNS with nmcli for adapter %s: %w", adapter, err)
	}

	return nil
}

func resetDNSWithNmcli(adapter string) error {
	// Runtime-only for the same reason as setDNSWithNmcli. Since nothing is
	// persisted, the DNS settings also die with the tunnel device, so a failure
	// here (e.g. device already gone) is harmless.
	return exec.Command("nmcli", "device", "modify", adapter,
		"ipv4.dns", "",
		"ipv4.dns-priority", "0").Run()
}

// 2. Using netplan
func setDNSWithNetplan(adapter, dns string) error {
	configFile := fmt.Sprintf("/etc/netplan/99-%s.yaml", adapter)

	// the file is only ever ours: one left behind by a crash is overwritten

	yamlContent := fmt.Sprintf(`
network:
  version: 2
  ethernets:
    %s:
      dhcp4: true
      nameservers:
        addresses: [%s]
`, adapter, dns)

	if err := os.WriteFile(configFile, []byte(yamlContent), 0644); err != nil {
		return fmt.Errorf("failed to write configuration file: %v", err)
	}

	return exec.Command("netplan", "apply").Run()
}

func resetDNSWithNetplan(adapter string) error {
	configFile := fmt.Sprintf("/etc/netplan/99-%s.yaml", adapter)
	if err := os.Remove(configFile); err != nil {
		return fmt.Errorf("failed to delete configuration file: %v", err)
	}
	return exec.Command("netplan", "apply").Run()
}

// 3. Using /etc/network/interfaces
func setDNSWithInterfaces(adapter, dns string) error {
	return fmt.Errorf("Cosmos Cloud is not able to set the DNS for /etc/network/interfaces, please set it manually to %s", dns)
}

func resetDNSWithInterfaces(adapter string) error {
	return nil
}

// 4. Using systemd-networkd
func setDNSWithSystemdNetworkd(adapter, dns string) error {
	return fmt.Errorf("Cosmos Cloud is not able to set the DNS for systemd-networkd, please set it manually to %s", dns)
}

func resetDNSWithSystemdNetworkd(adapter string) error {
	return nil
}

// setHostDNS chooses the most desirable method
func setHostDNS(adapter, dns string) error {
	if resolvedActive() {
		utils.Debug("Host DNS: chose resolvectl")
		return setDNSWithResolvectl(adapter, dns)
	}
	if exec.Command("which", "nmcli").Run() == nil {
		utils.Debug("Host DNS: chose nmcli")
		return setDNSWithNmcli(adapter, dns)
	} else if _, err := os.Stat("/etc/netplan"); err == nil {
		utils.Debug("Host DNS: chose netplan")
		return setDNSWithNetplan(adapter, dns)
	} else if _, err := os.Stat("/etc/network/interfaces"); err == nil {
		utils.Debug("Host DNS: chose interfaces")
		return setDNSWithInterfaces(adapter, dns)
	} else if _, err := os.Stat(fmt.Sprintf("/etc/systemd/network/%s.network", adapter)); err == nil {
		utils.Debug("Host DNS: chose systemd-networkd")
		return setDNSWithSystemdNetworkd(adapter, dns)
	}
	return fmt.Errorf("no known method available to set DNS for adapter: %s", adapter)
}

// resetHostDNS undoes setHostDNS with the same method
func resetHostDNS(adapter string) error {
	if resolvedActive() {
		return resetDNSWithResolvectl(adapter)
	}
	if exec.Command("which", "nmcli").Run() == nil {
		return resetDNSWithNmcli(adapter)
	} else if _, err := os.Stat("/etc/netplan"); err == nil {
		return resetDNSWithNetplan(adapter)
	} else if _, err := os.Stat("/etc/network/interfaces"); err == nil {
		return resetDNSWithInterfaces(adapter)
	} else if _, err := os.Stat(fmt.Sprintf("/etc/systemd/network/%s.network", adapter)); err == nil {
		return resetDNSWithSystemdNetworkd(adapter)
	}
	return fmt.Errorf("no known method available to reset DNS for adapter: %s", adapter)
}

// the tun device takes a moment to exist after nebula starts
func waitForAdapter(adapter string) bool {
	for i := 0; i < 50; i++ {
		if _, err := net.InterfaceByName(adapter); err == nil {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

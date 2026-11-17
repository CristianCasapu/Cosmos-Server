package protection

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const guardState = "piece=nftables\nformat=1\ninstalled_at=2026-11-17T10:00:00Z\npublic_tcp=2053,2052\npublic_udp=\nipv4=yes\nssh_open=no\n"

func TestStateFileOfEachPiece(t *testing.T) {
	newWorld(t)
	at := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

	writeState(t, PieceGuard, guardState+"some_future_key=whatever\n")
	state, file := readState(PieceGuard)
	if file != fileOK || !state.InstalledAt.Equal(at) {
		t.Fatalf("guard: %v %+v", file, state)
	}
	if !reflect.DeepEqual(state.PublicTCP, []int{2052, 2053}) || !reflect.DeepEqual(state.PublicUDP, []int{}) {
		t.Errorf("ports %v %v, want [2052 2053] and an empty list", state.PublicTCP, state.PublicUDP)
	}
	if state.IPv4 == nil || !*state.IPv4 || state.SSHOpen == nil || *state.SSHOpen {
		t.Errorf("ipv4 %v ssh_open %v, want yes and no", state.IPv4, state.SSHOpen)
	}

	writeState(t, PieceFail2ban, "piece=fail2ban\r\nformat=1\r\ninstalled_at=2026-11-17T10:00:00Z\r\nsshd=off\r\ncosmos_jails=on\r\n")
	state, file = readState(PieceFail2ban)
	if file != fileOK || state.SSHD == nil || *state.SSHD || state.CosmosJails == nil || !*state.CosmosJails {
		t.Errorf("fail2ban: %v %+v", file, state)
	}

	writeState(t, PieceUpdates, "# written by the installer\npiece=unattended-upgrades\nformat=1\ninstalled_at=2026-11-17T10:00:00Z\nconfig=/etc/apt/apt.conf.d/52cosmos-security-upgrades\n")
	state, file = readState(PieceUpdates)
	if file != fileOK || state.Config != "/etc/apt/apt.conf.d/52cosmos-security-upgrades" {
		t.Errorf("updates: %v %+v", file, state)
	}
}

func TestStateFileThatIsNotThere(t *testing.T) {
	newWorld(t)
	if _, file := readState(PieceGuard); file != fileMissing {
		t.Errorf("no file: %v, want missing", file)
	}
	StateDir = filepath.Join(t.TempDir(), "never-created")
	if _, file := readState(PieceGuard); file != fileMissing {
		t.Errorf("no folder: %v, want missing", file)
	}
}

func TestStateFileThatCannotBeTaken(t *testing.T) {
	newWorld(t)
	for name, content := range map[string]string{
		"another piece":            strings.Replace(guardState, "piece=nftables", "piece=fail2ban", 1),
		"no piece":                 strings.Replace(guardState, "piece=nftables\n", "", 1),
		"another format":           strings.Replace(guardState, "format=1", "format=2", 1),
		"no format":                strings.Replace(guardState, "format=1\n", "", 1),
		"no installation time":     strings.Replace(guardState, "installed_at=2026-11-17T10:00:00Z\n", "", 1),
		"a local time":             strings.Replace(guardState, "2026-11-17T10:00:00Z", "2026-11-17T11:00:00+01:00", 1),
		"a date only":              strings.Replace(guardState, "2026-11-17T10:00:00Z", "2026-11-17", 1),
		"no time at all":           strings.Replace(guardState, "2026-11-17T10:00:00Z", "yesterday", 1),
		"a key twice":              guardState + "piece=nftables\n",
		"a control character":      guardState + "note=\x1b[31m\n",
		"text that is not ASCII":   guardState + "note=caf\xc3\xa9\n",
		"one byte more than 4 KiB": guardState + "pad=" + strings.Repeat("x", maxStateFile-len(guardState)-5+1) + "\n",
		"empty":                    "",
	} {
		writeState(t, PieceGuard, content)
		if state, file := readState(PieceGuard); file != fileInvalid {
			t.Errorf("%s: %v %+v, want invalid", name, file, state)
		}
	}
	// exactly 4 KiB is still read
	content := guardState + "pad=" + strings.Repeat("x", maxStateFile-len(guardState)-5) + "\n"
	if len(content) != maxStateFile {
		t.Fatalf("the padded file has %d bytes", len(content))
	}
	writeState(t, PieceGuard, content)
	if _, file := readState(PieceGuard); file != fileOK {
		t.Errorf("a file of exactly 4 KiB: %v, want ok", file)
	}
}

func TestStateFileIsNotReadThroughALinkOrFromSomethingElse(t *testing.T) {
	newWorld(t)
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.state")
	if err := os.WriteFile(elsewhere, []byte(guardState), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(StateDir, PieceGuard+".state")); err != nil {
		t.Fatal(err)
	}
	if _, file := readState(PieceGuard); file != fileInvalid {
		t.Errorf("a link: %v, want invalid", file)
	}
	if err := os.Mkdir(filepath.Join(StateDir, PieceFail2ban+".state"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, file := readState(PieceFail2ban); file != fileInvalid {
		t.Errorf("a folder: %v, want invalid", file)
	}
}

func TestStateFileValuesThatDoNotPassAreLeftOut(t *testing.T) {
	newWorld(t)
	for _, bad := range []string{"0", "65536", "2053,0", "abc", "2053;reboot", "2053 2052", "-1", "+443", "0x50", "2053,,2052", "123456", strings.Repeat("1,", maxPorts) + "1"} {
		writeState(t, PieceGuard, strings.Replace(guardState, "public_tcp=2053,2052", "public_tcp="+bad, 1))
		state, file := readState(PieceGuard)
		if file != fileOK || state.PublicTCP != nil {
			t.Errorf("public_tcp=%q: %v %v, want the file taken and the list left out", bad, file, state.PublicTCP)
		}
	}
	writeState(t, PieceGuard, strings.Replace(guardState, "public_tcp=2053,2052", "public_tcp= 443 , 443,65535", 1))
	if state, _ := readState(PieceGuard); !reflect.DeepEqual(state.PublicTCP, []int{443, 65535}) {
		t.Errorf("spaces and a duplicate: %v, want [443 65535]", state.PublicTCP)
	}
	writeState(t, PieceGuard, strings.NewReplacer("ipv4=yes", "ipv4=true", "ssh_open=no", "ssh_open=").Replace(guardState))
	if state, _ := readState(PieceGuard); state.IPv4 != nil || state.SSHOpen != nil {
		t.Errorf("ipv4=true and ssh_open=: %v %v, want both left out", state.IPv4, state.SSHOpen)
	}
	for _, bad := range []string{"/etc/apt/apt.conf.d/../../shadow", "/etc/passwd", "relative", "/etc/apt/apt.conf.d/a b", "/etc/apt/apt.conf.d/", "/etc/apt/apt.conf.d/x/y"} {
		writeState(t, PieceUpdates, "piece=unattended-upgrades\nformat=1\ninstalled_at=2026-11-17T10:00:00Z\nconfig="+bad+"\n")
		if state, file := readState(PieceUpdates); file != fileOK || state.Config != "" {
			t.Errorf("config=%q: %v %q, want the file taken and the path left out", bad, file, state.Config)
		}
	}
}

func writeState(t *testing.T, piece string, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(StateDir, piece+".state"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestStateFileOfTodaysGuard(t *testing.T) {
	newWorld(t)
	writeState(t, PieceGuard, guardState+"ipv4_reason=public-address\nexposed=yes\nother_filters= inet firewalld ; ip filter;inet firewalld\n")
	state, file := readState(PieceGuard)
	if file != fileOK || state.IPv4Reason != "public-address" || state.Exposed == nil || !*state.Exposed || !reflect.DeepEqual(state.OtherFilters, []string{"inet firewalld", "ip filter"}) {
		t.Errorf("%v %+v", file, state)
	}
	for _, reason := range []string{"public-address", "cgnat-address", "asked-yes", "exposed", "behind-router", "asked-no"} {
		writeState(t, PieceGuard, guardState+"ipv4_reason="+reason+"\n")
		if state, _ := readState(PieceGuard); state.IPv4Reason != reason {
			t.Errorf("ipv4_reason=%s: %q", reason, state.IPv4Reason)
		}
	}
	writeState(t, PieceGuard, guardState+"other_filters=\n")
	if state, _ := readState(PieceGuard); state.OtherFilters == nil || len(state.OtherFilters) != 0 {
		t.Errorf("other_filters empty: %v, want an empty list", state.OtherFilters)
	}
	// left out when absent
	writeState(t, PieceGuard, guardState)
	if state, _ := readState(PieceGuard); state.IPv4Reason != "" || state.Exposed != nil || len(state.OtherFilters) != 0 {
		t.Errorf("an older file: %+v", state)
	}
}

func TestStateFileOfTodaysGuardDropsWhatDoesNotPass(t *testing.T) {
	newWorld(t)
	tooMany := strings.TrimSuffix(strings.Repeat("inet t;", maxOtherFilters+1), ";")
	for _, bad := range []string{
		"ipv4_reason=public", "ipv4_reason=Behind-Router", "ipv4_reason=behind-router;x",
		"exposed=true", "exposed=1",
		"other_filters=inet", "other_filters=inet firewalld;", "other_filters=inet fire walld", "other_filters=ip4 filter",
		"other_filters=inet $(reboot)", "other_filters=inet " + strings.Repeat("a", 65), "other_filters=inet firewalld,ip filter",
		"other_filters=" + tooMany,
	} {
		writeState(t, PieceGuard, guardState+bad+"\n")
		state, file := readState(PieceGuard)
		dropped := len(state.OtherFilters) == 0
		if strings.HasPrefix(bad, "other_filters=") {
			dropped = state.OtherFilters == nil
		}
		if file != fileOK || state.IPv4Reason != "" || state.Exposed != nil || !dropped {
			t.Errorf("%q: %v %+v, want the file taken and the value left out", bad, file, state)
		}
		if !reflect.DeepEqual(state.PublicTCP, []int{2052, 2053}) {
			t.Errorf("%q: the rest of the file was lost", bad)
		}
	}
	exactly := strings.TrimSuffix(strings.Repeat("inet t;", maxOtherFilters), ";")
	writeState(t, PieceGuard, guardState+"other_filters="+exactly+"\n")
	if state, _ := readState(PieceGuard); len(state.OtherFilters) != 1 {
		t.Errorf("16 entries (one table): %v", state.OtherFilters)
	}
}

func TestStateFileOfTheGuardWithWhatItFound(t *testing.T) {
	newWorld(t)
	writeState(t, PieceGuard, guardState+"cloud=Amazon EC2\nvirt=kvm\nvpn_found=tailscale:41641;wireguard wg0:unknown;nebula:4242;tailscale:41641\nudp_not_open=51820,5353\nssh_mode=console\ntrusted_if=tun0, wg1,tun0\n")
	state, file := readState(PieceGuard)
	port := func(p int) *int { return &p }
	wantVPNs := []VPN{{Kind: "tailscale", Port: port(41641)}, {Kind: "wireguard", Interface: "wg0"}, {Kind: "nebula", Port: port(4242)}}
	if file != fileOK || state.Cloud != "Amazon EC2" || state.Virt != "kvm" || state.SSHMode != "console" ||
		!reflect.DeepEqual(state.VPNs, wantVPNs) || !reflect.DeepEqual(state.UDPNotOpen, []int{5353, 51820}) || !reflect.DeepEqual(state.TrustedInterfaces, []string{"tun0", "wg1"}) {
		t.Errorf("%v %+v", file, state)
	}
	for _, mode := range []string{"allowance", "open", "from", "console"} {
		writeState(t, PieceGuard, guardState+"ssh_mode="+mode+"\n")
		if state, _ := readState(PieceGuard); state.SSHMode != mode {
			t.Errorf("ssh_mode=%s: %q", mode, state.SSHMode)
		}
	}
	writeState(t, PieceGuard, guardState+"cloud=\nvpn_found=\nudp_not_open=\ntrusted_if=\n")
	if state, _ := readState(PieceGuard); state.Cloud != "" || state.VPNs == nil || len(state.VPNs) != 0 || state.UDPNotOpen == nil || state.TrustedInterfaces == nil || len(state.TrustedInterfaces) != 0 {
		t.Errorf("empty values: %+v, want empty lists", state)
	}
	writeState(t, PieceGuard, guardState+"cloud=Google Compute Engine (n2) 1/2._x-y\n")
	if state, _ := readState(PieceGuard); state.Cloud != "Google Compute Engine (n2) 1/2._x-y" {
		t.Errorf("cloud %q", state.Cloud)
	}
}

func TestStateFileOfTheGuardDropsWhatItFoundWhenItDoesNotPass(t *testing.T) {
	newWorld(t)
	tooManyVPNs := strings.TrimSuffix(strings.Repeat("nebula:1;", maxVPNs+1), ";")
	tooManyIfs := strings.TrimSuffix(strings.Repeat("eth0,", maxInterfaces+1), ",")
	for _, bad := range []string{
		"cloud=Amazon EC2;reboot", "cloud=" + strings.Repeat("a", 65), "cloud=<b>x</b>", "cloud=a\tb",
		"virt=KVM", "virt=kvm lxc", "virt=" + strings.Repeat("a", 33),
		"vpn_found=tailscale", "vpn_found=tailscale:0", "vpn_found=tailscale:65536", "vpn_found=tailscale:123456",
		"vpn_found=openvpn:1194", "vpn_found=wireguard:51820", "vpn_found=wireguard wg0 x:51820",
		"vpn_found=wireguard this-name-is-far-too-long:1", "vpn_found=wireguard $(x):1", "vpn_found=tailscale:41641;", "vpn_found=tailscale:41641,nebula:4242",
		"vpn_found=Tailscale:41641", "vpn_found=" + tooManyVPNs,
		"udp_not_open=0", "udp_not_open=5353;51820", "udp_not_open=" + strings.Repeat("1,", maxPorts) + "1",
		"ssh_mode=closed", "ssh_mode=Console", "ssh_mode=console;x",
		"trusted_if=bad name", "trusted_if=tun0,", "trusted_if=this-name-is-far-too-long", "trusted_if=$(x)", "trusted_if=tun0;wg1", "trusted_if=" + tooManyIfs,
	} {
		writeState(t, PieceGuard, guardState+bad+"\n")
		state, file := readState(PieceGuard)
		key, _, _ := strings.Cut(bad, "=")
		var kept bool
		switch key {
		case "cloud":
			kept = state.Cloud != ""
		case "virt":
			kept = state.Virt != ""
		case "vpn_found":
			kept = state.VPNs != nil
		case "udp_not_open":
			kept = state.UDPNotOpen != nil
		case "ssh_mode":
			kept = state.SSHMode != ""
		case "trusted_if":
			kept = state.TrustedInterfaces != nil
		}
		if file != fileOK || kept {
			t.Errorf("%q: %v %+v, want the file taken and the value left out", bad, file, state)
		}
		if !reflect.DeepEqual(state.PublicTCP, []int{2052, 2053}) {
			t.Errorf("%q: the rest of the file was lost", bad)
		}
	}
	exactly := strings.TrimSuffix(strings.Repeat("nebula:1;", maxVPNs), ";")
	writeState(t, PieceGuard, guardState+"vpn_found="+exactly+"\n")
	if state, _ := readState(PieceGuard); len(state.VPNs) != 1 {
		t.Errorf("16 entries (one VPN): %v", state.VPNs)
	}
}

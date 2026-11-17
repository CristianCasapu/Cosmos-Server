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

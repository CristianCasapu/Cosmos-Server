package protection

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/azukaar/cosmos-server/src/fail2ban"
)

// world is a server of the test's own: every path the package reads points
// into a temporary folder, nft and Fail2ban are stand-ins.
type world struct {
	t        *testing.T
	root     string
	nftCalls atomic.Int32
	// what the stand-in nft answers
	nftOut []byte
	nftErr string
	nftRun error
	// what the stand-in Fail2ban says
	fail2ban fail2ban.Summary
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, root: t.TempDir()}
	for _, dir := range []string{"state", "apt.conf.d", "bin", "wants", "sbin", "cosmos"} {
		if err := os.MkdirAll(filepath.Join(w.root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	previous := struct {
		stateDir, aptDir, binary, timer string
		nft                             []string
		run                             func(string) ([]byte, string, error)
		summary                         func() fail2ban.Summary
		exe                             func() (string, error)
	}{StateDir, aptConfigDir, upgradeBinary, upgradeTimer, nftPaths, runNft, fail2banSummary, executable}
	StateDir = filepath.Join(w.root, "state")
	aptConfigDir = filepath.Join(w.root, "apt.conf.d")
	upgradeBinary = filepath.Join(w.root, "bin", "unattended-upgrade")
	upgradeTimer = filepath.Join(w.root, "wants", "apt-daily-upgrade.timer")
	nftPaths = []string{filepath.Join(w.root, "sbin", "nft")}
	runNft = func(string) ([]byte, string, error) {
		w.nftCalls.Add(1)
		return w.nftOut, w.nftErr, w.nftRun
	}
	fail2banSummary = func() fail2ban.Summary { return w.fail2ban }
	executable = func() (string, error) { return filepath.Join(w.root, "cosmos", "cosmos"), nil }
	forget()
	t.Cleanup(func() {
		StateDir, aptConfigDir, upgradeBinary, upgradeTimer = previous.stateDir, previous.aptDir, previous.binary, previous.timer
		nftPaths, runNft, fail2banSummary, executable = previous.nft, previous.run, previous.summary, previous.exe
		forget()
	})
	return w
}

func (w *world) write(path string, content string, mode os.FileMode) {
	w.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) state(piece string, content string) {
	w.write(filepath.Join(StateDir, piece+".state"), content, 0644)
}

// nft puts an executable "nft" in place (the stand-in answers anyway).
func (w *world) nft() {
	w.write(nftPaths[0], "#!/bin/sh\nexit 0\n", 0755)
}

// table makes the stand-in nft answer with a fixture.
func (w *world) table(fixture string) {
	w.t.Helper()
	w.nft()
	content, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		w.t.Fatal(err)
	}
	w.nftOut, w.nftErr, w.nftRun = content, "", nil
}

// noTable makes it answer as nft does for a table that is not there.
func (w *world) noTable() {
	w.nft()
	w.nftOut = nil
	w.nftErr = "Error: No such file or directory\nlist table inet cosmos_host_guard\n                ^^^^^^^^^^^^^^^^^\n"
	w.nftRun = exitError(w.t)
}

// exitError is a real *exec.ExitError (status 1).
func exitError(t *testing.T) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fail")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	_, _, err := realRunNft(path)
	if err == nil {
		t.Fatal("the failing stand-in did not fail")
	}
	return err
}

// realRunNft is the package's runner as it is outside the tests.
var realRunNft = runNft

// updatesOn sets up the automatic updates as Debian's dpkg-reconfigure does.
func (w *world) updatesOn() {
	w.write(upgradeBinary, "#!/bin/sh\n", 0755)
	w.write(filepath.Join(aptConfigDir, "20auto-upgrades"), "APT::Periodic::Update-Package-Lists \"1\";\nAPT::Periodic::Unattended-Upgrade \"1\";\n", 0644)
	if err := os.Symlink("/lib/systemd/system/apt-daily-upgrade.timer", upgradeTimer); err != nil {
		w.t.Fatal(err)
	}
}

var errStandIn = errors.New("stand-in failure")

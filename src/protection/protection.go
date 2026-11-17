package protection

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/fail2ban"
	"github.com/azukaar/cosmos-server/src/utils"
)

// The answer of GET /cosmos/api/protection.

// States of a piece.
const (
	StateNotInstalled = "notInstalled"
	StateActive       = "active"
	StateInactive     = "inactive"
	StateUnknown      = "unknown"
)

// Who set a piece up.
const (
	ByFork          = "fork"
	ByAdministrator = "administrator"
)

// Kinds of command offered.
const (
	CommandInstall   = "install"
	CommandUninstall = "uninstall"
)

// Reasons (codes the panel puts in words) for a state that is not plain.
const (
	reasonStateInvalid = "stateInvalid"  // the state file is there but cannot be read
	reasonTableMissing = "tableMissing"  // guard: installed, its table is not in the kernel
	reasonNotHooked    = "notHooked"     // guard: the table is there, a base chain is not hooked
	reasonChainEmpty   = "chainEmpty"    // guard: the chains are hooked but do not hold their rules (flushed)
	reasonNoOutside    = "noOutside"     // guard: no outside interface in wan_if, nothing is filtered
	reasonTrusted      = "trustedUplink" // guard: an outside interface is trusted (--trust-interface): nothing through it is filtered
	reasonStopped      = "stopped"       // fail2ban: installed, not running
	reasonNoJails      = "noJails"       // fail2ban: running, the fork's jail file is not there
	reasonNoBinary     = "noBinary"      // fail2ban, updates: the program itself is not installed
	reasonNotEnabled   = "notEnabled"    // updates: APT::Periodic::Unattended-Upgrade is not "1"
	reasonNoTimer      = "noTimer"       // updates: the apt-daily-upgrade timer is not enabled
)

// Piece is what every card shows.
type Piece struct {
	// Installed: the piece is on the server (by the fork's installer, or
	// for Fail2ban and the updates also set up by hand).
	Installed bool `json:"installed"`
	// Active: it protects now.
	Active bool `json:"active"`
	// State is one of notInstalled, active, inactive, unknown.
	State string `json:"state"`
	// Reason is a code for an inactive or unknown state (or a remark on an
	// active one, such as stateInvalid).
	Reason string `json:"reason,omitempty"`
	// Since is when it was installed (the state file's installed_at, or the
	// time of the file that set it up).
	Since *time.Time `json:"since,omitempty"`
	// By is fork or administrator, for an installed piece.
	By string `json:"by,omitempty"`
	// StateFile is missing, ok or invalid.
	StateFile string `json:"stateFile"`
	// Command is the command to type on the server, CommandKind install or
	// uninstall; both empty when none is offered.
	Command     string `json:"command,omitempty"`
	CommandKind string `json:"commandKind,omitempty"`
}

// Guard is the card of the host guard (fork/nftables).
type Guard struct {
	Piece
	// Unknown is why the live state could not be read (noNft,
	// notPermitted, failed), empty when it was.
	Unknown string `json:"unknown,omitempty"`
	// Table is the live state, nil when unknown.
	Table *Table `json:"table,omitempty"`
	// What the installer recorded.
	PublicTCP []int `json:"publicTcp"`
	PublicUDP []int `json:"publicUdp"`
	IPv4      *bool `json:"ipv4,omitempty"`
	SSHOpen   *bool `json:"sshOpen,omitempty"`
	// IPv4Reason is why the installer filters IPv4 or not: public-address,
	// cgnat-address, asked-yes, exposed, behind-router, asked-no.
	IPv4Reason string `json:"ipv4Reason,omitempty"`
	// Exposed: installed with --exposed.
	Exposed *bool `json:"exposed,omitempty"`
	// OtherFilters are other nftables tables that filter what reaches the
	// server ("inet firewalld"), as the installer found them.
	OtherFilters []string `json:"otherFilters"`
	// RepairCommand loads the guard's rules again, offered when they are
	// installed but not in force.
	RepairCommand string `json:"repairCommand,omitempty"`
	// PortMissing: Cosmos' public port is not among the ports the guard
	// keeps open (the table's when known, else the state file's).
	PortMissing bool `json:"portMissing"`
	// KeptOpen are the TCP ports that comparison was made against.
	KeptOpen []int `json:"keptOpen"`
	// Cloud is the cloud the installer detected ("Amazon EC2"), empty when
	// none was; Virt the virtualisation it found (kvm, lxc, none).
	Cloud string `json:"cloud,omitempty"`
	Virt  string `json:"virt,omitempty"`
	// VPNFound are the VPNs the installer found, with their UDP port when
	// known.
	VPNFound []VPN `json:"vpnFound"`
	// UDPNotOpen are UDP ports listening on the server that do not answer
	// from outside, as the installer found them.
	UDPNotOpen []int `json:"udpNotOpen"`
	// SSHMode is how the installer left SSH from outside: allowance, open,
	// from, console.
	SSHMode string `json:"sshMode,omitempty"`
	// TrustedInterfaces are the interfaces trusted with --trust-interface,
	// as the installer recorded them (the table's set says which are in
	// force).
	TrustedInterfaces []string `json:"trustedInterfaces"`
	// Liveness: whether its watcher and its timer still run, for a guard
	// whose table is loaded.
	Liveness *Liveness `json:"liveness,omitempty"`
}

// Fail2ban is the card of Fail2ban (fork/fail2ban).
type Fail2ban struct {
	Piece
	// ClientInstalled: Fail2ban itself is on the server.
	ClientInstalled bool `json:"clientInstalled"`
	Running         bool `json:"running"`
	// ConfigInstalled: the fork's jail file is in its configuration.
	ConfigInstalled bool  `json:"configInstalled"`
	SSHD            *bool `json:"sshd,omitempty"`
	CosmosJails     *bool `json:"cosmosJails,omitempty"`
}

// Updates is the card of the automatic security updates
// (fork/unattended-upgrades).
type Updates struct {
	Piece
	BinaryInstalled bool `json:"binaryInstalled"`
	Enabled         bool `json:"enabled"`
	TimerEnabled    bool `json:"timerEnabled"`
	// Config is the file that switched them on (the last assignment).
	Config string `json:"config,omitempty"`
	// Incomplete: APT's configuration was not read whole.
	Incomplete bool `json:"incomplete"`
}

// Status is the answer.
type Status struct {
	Time time.Time `json:"time"`
	// CosmosPort is the port Cosmos serves the panel on: the HTTPS port, or
	// the HTTP port when HTTPS is switched off.
	CosmosPort int      `json:"cosmosPort"`
	Guard      Guard    `json:"guard"`
	Fail2ban   Fail2ban `json:"fail2ban"`
	Updates    Updates  `json:"updates"`
}

// cacheFor is how long a reading is reused: the route cannot run nft, ping
// Fail2ban or scan APT's folder more often than this.
const cacheFor = 10 * time.Second

var (
	cacheLock sync.Mutex
	cached    *Status
	cachedAt  time.Time
)

// forget drops the cached reading (tests).
func forget() {
	cacheLock.Lock()
	cached = nil
	cacheLock.Unlock()
}

// GetStatus returns the state of the three pieces. Concurrent callers wait
// for one reading.
func GetStatus() Status {
	cacheLock.Lock()
	if cached == nil || time.Since(cachedAt) > cacheFor {
		reading := read()
		cached, cachedAt = &reading, time.Now()
	}
	status := *cached
	cacheLock.Unlock()

	// what depends on Cosmos' own settings is worked out each time: they
	// can change between two readings
	status.CosmosPort = cosmosPort()
	status.Guard = withPortCheck(status.Guard, status.CosmosPort)
	return status
}

func read() Status {
	now := time.Now().UTC()
	return Status{
		Time:     now,
		Guard:    readGuard(now),
		Fail2ban: readFail2ban(),
		Updates:  readUpdates(),
	}
}

func timePointer(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	return &at
}

// withUntil gives each allowance its end, from the time of the reading.
func withUntil(allowed []Allowance, now time.Time) []Allowance {
	out := make([]Allowance, 0, len(allowed))
	for _, allowance := range allowed {
		if allowance.Expires != nil {
			until := now.Add(time.Duration(*allowance.Expires) * time.Second).UTC().Truncate(time.Second)
			allowance.Until = &until
		}
		out = append(out, allowance)
	}
	return out
}

// repairCommand loads the guard's rules again (its unit's reload runs
// `cosmos-host-guard load`, which replaces the table in one transaction).
const repairCommand = "sudo systemctl reload cosmos-host-guard"

func readGuard(now time.Time) Guard {
	state, file := readState(PieceGuard)
	guard := Guard{
		Piece: Piece{StateFile: file}, PublicTCP: []int{}, PublicUDP: []int{}, KeptOpen: []int{}, OtherFilters: []string{},
		VPNFound: []VPN{}, UDPNotOpen: []int{}, TrustedInterfaces: []string{},
	}
	if file == fileOK {
		guard.Since = timePointer(state.InstalledAt)
		if state.PublicTCP != nil {
			guard.PublicTCP = state.PublicTCP
		}
		if state.PublicUDP != nil {
			guard.PublicUDP = state.PublicUDP
		}
		guard.IPv4, guard.SSHOpen = state.IPv4, state.SSHOpen
		guard.IPv4Reason, guard.Exposed = state.IPv4Reason, state.Exposed
		if state.OtherFilters != nil {
			guard.OtherFilters = state.OtherFilters
		}
		guard.Cloud, guard.Virt, guard.SSHMode = state.Cloud, state.Virt, state.SSHMode
		if state.VPNs != nil {
			guard.VPNFound = state.VPNs
		}
		if state.UDPNotOpen != nil {
			guard.UDPNotOpen = state.UDPNotOpen
		}
		if state.TrustedInterfaces != nil {
			guard.TrustedInterfaces = state.TrustedInterfaces
		}
	}

	live := readTable()
	if live.unknown == "" {
		table := live.table
		table.SSHAllowed = withUntil(table.SSHAllowed, now)
		guard.Table = &table
	} else {
		guard.Unknown = live.unknown
	}
	loaded := guard.Table != nil && guard.Table.Loaded

	// installed: the installer's record, or its table in the kernel
	guard.Installed = file != fileMissing || loaded
	if guard.Installed {
		guard.By = ByFork
	}
	switch {
	case live.unknown != "":
		guard.State = StateUnknown
		guard.Reason = live.unknown
	case !guard.Installed:
		guard.State = StateNotInstalled
	case !loaded:
		guard.State, guard.Reason = StateInactive, reasonTableMissing
	case !guard.Table.InputHooked || !guard.Table.ForwardHooked:
		guard.State, guard.Reason = StateInactive, reasonNotHooked
	case !guard.Table.InputFilters || !guard.Table.ForwardFilters:
		guard.State, guard.Reason = StateInactive, reasonChainEmpty
	case len(guard.Table.WANInterfaces) == 0:
		guard.State, guard.Reason = StateInactive, reasonNoOutside
	case len(guard.Table.TrustedInterfaces) > 0:
		// an uplink nothing is filtered on: whatever reaches the server
		// through it reaches every port, so the guard is not Active
		guard.State, guard.Reason = StateInactive, reasonTrusted
	default:
		guard.State, guard.Active = StateActive, true
		if file == fileInvalid {
			guard.Reason = reasonStateInvalid
		}
	}
	switch guard.Reason {
	case reasonTableMissing, reasonNotHooked, reasonChainEmpty:
		guard.RepairCommand = repairCommand
	}
	if loaded {
		live := readLiveness(now)
		guard.Liveness = &live
	}
	// an inactive guard keeps the reason it does not filter; stateFile
	// "invalid" tells that its record cannot be read as well
	guard.Command, guard.CommandKind = command(PieceGuard, guard.Installed)
	return guard
}

// withPortCheck compares Cosmos' public port with what the guard keeps open.
func withPortCheck(guard Guard, port int) Guard {
	guard.PortMissing = false
	guard.KeptOpen = []int{}
	if !guard.Installed || port == 0 {
		return guard
	}
	kept := guard.PublicTCP
	if guard.Table != nil && guard.Table.Loaded {
		kept = guard.Table.PublicTCP
	} else if guard.StateFile != fileOK {
		return guard
	}
	guard.KeptOpen = kept
	guard.PortMissing = true
	for _, open := range kept {
		if open == port {
			guard.PortMissing = false
		}
	}
	return guard
}

// fail2banSummary is fail2ban.GetSummary, replaced in tests.
var fail2banSummary = fail2ban.GetSummary

func readFail2ban() Fail2ban {
	state, file := readState(PieceFail2ban)
	summary := fail2banSummary()
	card := Fail2ban{
		Piece:           Piece{StateFile: file},
		ClientInstalled: summary.Installed,
		Running:         summary.Running,
		ConfigInstalled: summary.ConfigInstalled,
	}
	if file == fileOK {
		card.SSHD, card.CosmosJails = state.SSHD, state.CosmosJails
		card.Since = timePointer(state.InstalledAt)
	} else if summary.ConfigInstalled {
		card.Since = timePointer(summary.ConfigSince)
	}
	// installed: the installer's record, or Fail2ban with the fork's jails
	card.Installed = file != fileMissing || (summary.Installed && summary.ConfigInstalled)
	switch {
	case !card.Installed:
		card.State = StateNotInstalled
	case !summary.Installed:
		card.State, card.Reason = StateInactive, reasonNoBinary
	case !summary.Running:
		card.State, card.Reason = StateInactive, reasonStopped
	case !summary.ConfigInstalled:
		card.State, card.Reason = StateInactive, reasonNoJails
	default:
		card.State, card.Active = StateActive, true
		if file == fileInvalid {
			card.Reason = reasonStateInvalid
		}
	}
	if card.Installed {
		card.By = ByFork
	}
	card.Command, card.CommandKind = command(PieceFail2ban, card.Installed)
	return card
}

func readUpdates() Updates {
	state, file := readState(PieceUpdates)
	setting := readAptSetting()
	card := Updates{
		Piece:           Piece{StateFile: file},
		BinaryInstalled: isExecutable(upgradeBinary),
		Enabled:         setting.Value == "1",
		TimerEnabled:    exists(upgradeTimer),
		Incomplete:      setting.Incomplete,
	}
	card.Active = card.BinaryInstalled && card.Enabled && card.TimerEnabled
	if card.Enabled {
		card.Config = setting.File
	}
	fork := file != fileMissing
	card.Installed = fork || card.Active
	switch {
	case fork:
		card.By = ByFork
		if file == fileOK {
			card.Since = timePointer(state.InstalledAt)
		}
	case card.Active:
		card.By = ByAdministrator
		card.Since = timePointer(setting.Changed)
	}
	switch {
	case !card.Installed:
		card.State = StateNotInstalled
	case !card.BinaryInstalled:
		card.State, card.Reason = StateInactive, reasonNoBinary
	case !card.Enabled:
		card.State, card.Reason = StateInactive, reasonNotEnabled
	case !card.TimerEnabled:
		card.State, card.Reason = StateInactive, reasonNoTimer
	default:
		card.State = StateActive
		if file == fileInvalid {
			card.Reason = reasonStateInvalid
		}
	}
	// set up outside the fork: the fork's uninstaller is not offered
	if card.By == ByAdministrator {
		return card
	}
	card.Command, card.CommandKind = command(PieceUpdates, card.Installed)
	return card
}

// cosmosPort is the port the panel is served on.
func cosmosPort() int {
	config := utils.GetMainConfig().HTTPConfig
	value := config.HTTPSPort
	if config.HTTPSCertificateMode == utils.HTTPSCertModeList["DISABLED"] {
		value = config.HTTPPort
	}
	port, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(value), ":"))
	if err != nil || port < 1 || port > 65535 {
		return 0
	}
	return port
}

// executable is os.Executable, replaced in tests.
var executable = os.Executable

// defaultInstallDir is where the fork's files are on a server installed as
// the README says.
const defaultInstallDir = "/opt/cosmos"

// installDir is the folder of the running executable when the piece's
// installer is next to it, else defaultInstallDir.
func installDir(piece string) string {
	if path, err := executable(); err == nil {
		dir := filepath.Dir(path)
		if info, err := os.Stat(filepath.Join(dir, "fork", piece, "install.sh")); err == nil && info.Mode().IsRegular() {
			return dir
		}
	}
	return defaultInstallDir
}

var plainPath = regexp.MustCompile(`^[A-Za-z0-9_./+-]+$`)

// shellQuote quotes a path for sh when it holds anything but plain
// characters.
func shellQuote(path string) string {
	if plainPath.MatchString(path) {
		return path
	}
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// command is the command that installs the piece, or removes it when it is
// installed. The piece is one of the fixed names.
func command(piece string, installed bool) (string, string) {
	script, kind := "install.sh", CommandInstall
	if installed {
		script, kind = "uninstall.sh", CommandUninstall
	}
	return "sudo sh " + shellQuote(filepath.Join(installDir(piece), "fork", piece, script)), kind
}

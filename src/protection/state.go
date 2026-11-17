// Package protection is a fork addition: the state of the three protection
// pieces the fork can install on the server (fork/nftables, fork/fail2ban,
// fork/unattended-upgrades), for the cards of the security page. It only
// reads: Cosmos never installs, starts or changes these pieces; their
// installers do, run by the administrator.
//
// Nothing from a request reaches a path or a command here: the route takes
// no input. What is read is fixed and bounded: three state files of at most
// 4 KiB, one run of `nft -j list table inet cosmos_host_guard` (3 s, 1 MiB,
// at most once per cacheFor), the files of APT's configuration folder (at
// most 100 files of 64 KiB) and the presence of a few files.
package protection

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The pieces, by the name of their folder under fork/ and of their state
// file.
const (
	PieceGuard    = "nftables"
	PieceFail2ban = "fail2ban"
	PieceUpdates  = "unattended-upgrades"
)

var pieces = []string{PieceGuard, PieceFail2ban, PieceUpdates}

// StateDir is where each piece's installer writes <piece>.state and its
// uninstaller removes it. A variable so that tests point it elsewhere.
var StateDir = "/var/lib/cosmos-protection"

const (
	// maxStateFile bounds what is read of a state file: a few lines.
	maxStateFile = 4 << 10
	// maxPorts bounds a list of ports in a state file.
	maxPorts = 64
	// maxOtherFilters bounds the list of other tables in a state file.
	maxOtherFilters = 16
)

// ipv4Reasons are the values ipv4_reason may take: why the installer
// filters IPv4 (a public or carrier-grade address, asked with --ipv4 yes,
// --exposed) or does not (a private address behind a router, --ipv4 no).
var ipv4Reasons = map[string]bool{
	"public-address": true, "cgnat-address": true, "asked-yes": true, "exposed": true,
	"behind-router": true, "asked-no": true,
}

// otherFilterPattern is one entry of other_filters: a family and a table.
var otherFilterPattern = regexp.MustCompile(`^(ip|ip6|inet|arp|bridge|netdev) [A-Za-z0-9_.-]{1,64}$`)

// State is what a piece's state file says, after validation. A value that
// does not pass its check is left out (nil or empty), the rest of the file
// still counts.
type State struct {
	InstalledAt time.Time
	// nftables
	PublicTCP []int
	PublicUDP []int
	IPv4      *bool
	SSHOpen   *bool
	// IPv4Reason is why IPv4 is filtered or not (one of ipv4Reasons).
	IPv4Reason string
	// Exposed: installed with --exposed (a cloud server with 1:1 NAT).
	Exposed *bool
	// OtherFilters are the other nftables tables that filter input, as
	// "<family> <name>"; nil when the value did not pass.
	OtherFilters []string
	// fail2ban
	SSHD        *bool
	CosmosJails *bool
	// unattended-upgrades
	Config string
}

// How a state file was found.
const (
	fileMissing = "missing"
	fileOK      = "ok"
	fileInvalid = "invalid"
)

var (
	errTooLarge  = errors.New("larger than a state file can be")
	errNotText   = errors.New("not ASCII text")
	errNotPiece  = errors.New("not the state of this piece")
	errFormat    = errors.New("a format this Cosmos does not know")
	errNoInstall = errors.New("no valid installed_at")
	errTwice     = errors.New("a key given twice")
)

// aptConfigPattern is the only form the unattended-upgrades piece's config
// value may take: a file directly in APT's configuration folder.
var aptConfigPattern = regexp.MustCompile(`^/etc/apt/apt\.conf\.d/[A-Za-z0-9_.-]{1,64}$`)

// readState reads and checks the state file of one of the fixed pieces. It
// returns fileMissing when there is none, fileInvalid when it is there but
// cannot be taken (too large, not text, another piece, another format, no
// valid installation time), fileOK otherwise.
func readState(piece string) (State, string) {
	path := filepath.Join(StateDir, piece+".state")
	// never through a link, never a device or a fifo
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, fileMissing
	}
	if err != nil || !info.Mode().IsRegular() {
		return State{}, fileInvalid
	}
	file, err := os.Open(path)
	if err != nil {
		return State{}, fileInvalid
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxStateFile+1))
	if err != nil {
		return State{}, fileInvalid
	}
	state, err := parseState(piece, content)
	if err != nil {
		return State{}, fileInvalid
	}
	return state, fileOK
}

// parseState checks the lines of a state file: ASCII key=value lines,
// unknown keys ignored, each known value checked.
func parseState(piece string, content []byte) (State, error) {
	if len(content) > maxStateFile {
		return State{}, errTooLarge
	}
	for _, c := range content {
		if c != '\n' && c != '\r' && c != '\t' && (c < 0x20 || c > 0x7e) {
			return State{}, errNotText
		}
	}
	values := map[string]string{}
	for _, line := range bytes.Split(content, []byte("\n")) {
		text := strings.TrimSpace(string(line))
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, value, found := strings.Cut(text, "=")
		if !found {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if _, twice := values[key]; twice {
			return State{}, errTwice
		}
		values[key] = value
	}
	if values["piece"] != piece {
		return State{}, errNotPiece
	}
	if values["format"] != "1" {
		return State{}, errFormat
	}
	state := State{}
	at, ok := parseTime(values["installed_at"])
	if !ok {
		return State{}, errNoInstall
	}
	state.InstalledAt = at

	switch piece {
	case PieceGuard:
		state.PublicTCP, _ = parsePorts(values["public_tcp"])
		state.PublicUDP, _ = parsePorts(values["public_udp"])
		state.IPv4 = parseYesNo(values["ipv4"], "yes", "no")
		state.SSHOpen = parseYesNo(values["ssh_open"], "yes", "no")
		if ipv4Reasons[values["ipv4_reason"]] {
			state.IPv4Reason = values["ipv4_reason"]
		}
		state.Exposed = parseYesNo(values["exposed"], "yes", "no")
		state.OtherFilters, _ = parseOtherFilters(values["other_filters"])
	case PieceFail2ban:
		state.SSHD = parseYesNo(values["sshd"], "on", "off")
		state.CosmosJails = parseYesNo(values["cosmos_jails"], "on", "off")
	case PieceUpdates:
		if aptConfigPattern.MatchString(values["config"]) && !strings.Contains(values["config"], "..") {
			state.Config = values["config"]
		}
	}
	return state, nil
}

// parseTime takes an RFC 3339 time in UTC ("2026-11-17T10:00:00Z") only.
func parseTime(value string) (time.Time, bool) {
	if !strings.HasSuffix(value, "Z") || len(value) > 40 {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil || at.Year() < 2000 || at.Year() > 9999 {
		return time.Time{}, false
	}
	return at.UTC(), true
}

// parsePorts takes "2053,2052": numbers 1 to 65535, comma-separated, at
// most maxPorts, sorted and without duplicates. Empty is an empty list. A
// list with one bad entry is refused whole (nil, false).
func parsePorts(value string) ([]int, bool) {
	ports := []int{}
	if value == "" {
		return ports, true
	}
	parts := strings.Split(value, ",")
	if len(parts) > maxPorts {
		return nil, false
	}
	seen := map[int]bool{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || len(part) > 5 || strings.TrimLeft(part, "0123456789") != "" {
			return nil, false
		}
		port, err := strconv.Atoi(part)
		if err != nil || port < 1 || port > 65535 {
			return nil, false
		}
		if !seen[port] {
			seen[port] = true
			ports = append(ports, port)
		}
	}
	sort.Ints(ports)
	return ports, true
}

func parseYesNo(value string, yes string, no string) *bool {
	switch value {
	case yes:
		v := true
		return &v
	case no:
		v := false
		return &v
	}
	return nil
}

// parseOtherFilters takes "inet firewalld;ip filter": at most
// maxOtherFilters entries of a family and a table name, separated by ";",
// in the order given, without duplicates. Empty is an empty list. A list
// with one bad entry is refused whole (nil, false).
func parseOtherFilters(value string) ([]string, bool) {
	filters := []string{}
	if value == "" {
		return filters, true
	}
	parts := strings.Split(value, ";")
	if len(parts) > maxOtherFilters {
		return nil, false
	}
	seen := map[string]bool{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if !otherFilterPattern.MatchString(part) {
			return nil, false
		}
		if !seen[part] {
			seen[part] = true
			filters = append(filters, part)
		}
	}
	return filters, true
}

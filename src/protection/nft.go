package protection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// This file is the only place where a command is run: nft, with a fixed
// argument list, no shell, no input from a request.

const (
	guardFamily = "inet"
	guardTable  = "cosmos_host_guard"
	// nftTimeout bounds one run; listing one table takes milliseconds.
	nftTimeout = 3 * time.Second
	// maxNftOutput bounds what is read of its output: the table with full
	// sets is a few tens of kilobytes.
	maxNftOutput = 1 << 20
	// maxNftErrors bounds what is read of its error output, which only
	// tells why it failed.
	maxNftErrors = 4 << 10
	// maxElements bounds what is kept of one set.
	maxElements = 256
)

// nftPaths are where Debian and others put nft. Absolute, so that PATH
// plays no part in what is run. A variable for the tests.
var nftPaths = []string{"/usr/sbin/nft", "/sbin/nft", "/usr/bin/nft"}

var nftArgs = []string{"-j", "list", "table", guardFamily, guardTable}

// Why the guard's live state is unknown (codes the panel puts in words).
const (
	unknownNoNft        = "noNft"        // nft is not on this server (or Cosmos runs in a container)
	unknownNotPermitted = "notPermitted" // Cosmos may not read the firewall (not root, no CAP_NET_ADMIN)
	unknownFailed       = "failed"       // nft failed for another reason, or its answer could not be read
)

// Counter is one of the guard's named counters.
type Counter struct {
	Packets uint64 `json:"packets"`
	Bytes   uint64 `json:"bytes"`
}

// Table is what the kernel holds of the guard's table.
type Table struct {
	// Loaded: the table is in the kernel.
	Loaded bool `json:"loaded"`
	// InputHooked, ForwardHooked: its base chains are hooked where they
	// should be.
	InputHooked   bool `json:"inputHooked"`
	ForwardHooked bool `json:"forwardHooked"`
	// InputFilters, ForwardFilters: the chain holds its last rule, the
	// counted drop of what came from outside (a flushed chain has none and
	// lets everything through).
	InputFilters   bool     `json:"inputFilters"`
	ForwardFilters bool     `json:"forwardFilters"`
	DroppedInput   *Counter `json:"droppedInput,omitempty"`
	DroppedForward *Counter `json:"droppedForward,omitempty"`
	// DroppedSpoofed: packets from outside with a forged source.
	DroppedSpoofed *Counter `json:"droppedSpoofed,omitempty"`
	// IPv4Passed: the input chain lets every IPv4 packet through (the guard
	// was installed without IPv4 filtering).
	IPv4Passed bool  `json:"ipv4Passed"`
	PublicTCP  []int `json:"publicTcp"`
	PublicUDP  []int `json:"publicUdp"`
	VPNUDP     []int `json:"vpnUdp"`
	// WANInterfaces are the interfaces the guard filters (those with a
	// default route, or every one when none has: FailClosedInterfaces).
	WANInterfaces []string `json:"wanInterfaces"`
	// LANInterfaces are the outside interfaces judged a home network behind
	// a router: their on-link prefixes and link-local sources count as home.
	LANInterfaces []string `json:"lanInterfaces"`
	// FailClosedInterfaces is not empty when no default route was found and
	// the guard filters every interface that is not loopback, Docker's or a
	// VPN.
	FailClosedInterfaces []string `json:"failClosedInterfaces"`
	// SSHPorts are the ports the SSH allowances open.
	SSHPorts []int `json:"sshPorts"`
	// SSHAllowed are the addresses that may reach the SSH ports from
	// outside, IPv4 first.
	SSHAllowed []Allowance `json:"sshAllowed"`
	// HomeNetworks are the networks named with --home (addresses,
	// prefixes or ranges as text), IPv4 first.
	HomeNetworks []string `json:"homeNetworks"`
}

// Allowance is an address of ssh_allow4 or ssh_allow6.
type Allowance struct {
	Address string `json:"address"`
	// Expires is how many seconds were left when the table was read, nil
	// for an element without a timeout.
	Expires *int64 `json:"expires,omitempty"`
	// Until is when it ends (the reading's time plus Expires).
	Until *time.Time `json:"until,omitempty"`
}

// liveTable is a reading of the table: Unknown is set (a code) when it
// could not be read.
type liveTable struct {
	table   Table
	unknown string
}

// capped keeps the first max bytes written to it and notes that there was
// more.
type capped struct {
	buffer    bytes.Buffer
	max       int
	truncated bool
}

func (c *capped) Write(data []byte) (int, error) {
	if room := c.max - c.buffer.Len(); room < len(data) {
		c.truncated = true
		if room > 0 {
			c.buffer.Write(data[:room])
		}
	} else {
		c.buffer.Write(data)
	}
	return len(data), nil
}

var errNftOutput = errors.New("nft wrote more than is read")

// findNft returns the first of nftPaths that is an executable file.
func findNft() string {
	for _, path := range nftPaths {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return path
		}
	}
	return ""
}

// runNft runs nft once and returns its output and error output. Tests
// replace it.
var runNft = func(path string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), nftTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, nftArgs...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "LANG=C"}
	cmd.Dir = "/"
	out := &capped{max: maxNftOutput}
	errOut := &capped{max: maxNftErrors}
	cmd.Stdout = out
	cmd.Stderr = errOut
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if err == nil && out.truncated {
		err = errNftOutput
	}
	return out.buffer.Bytes(), errOut.buffer.String(), err
}

// readTable runs nft (once) and reads its answer.
func readTable() liveTable {
	path := findNft()
	if path == "" {
		return liveTable{unknown: unknownNoNft}
	}
	out, errOut, err := runNft(path)
	if err != nil {
		var exit *exec.ExitError
		switch {
		case errors.As(err, &exit) && strings.Contains(errOut, "No such file or directory"):
			// nft's answer for a table that is not there
			return liveTable{table: emptyTable()}
		case strings.Contains(errOut, "Operation not permitted"), strings.Contains(errOut, "Permission denied"):
			return liveTable{unknown: unknownNotPermitted}
		case errors.Is(err, exec.ErrNotFound), errors.Is(err, os.ErrNotExist):
			return liveTable{unknown: unknownNoNft}
		}
		return liveTable{unknown: unknownFailed}
	}
	table, err := parseTable(out)
	if err != nil {
		return liveTable{unknown: unknownFailed}
	}
	return liveTable{table: table}
}

func emptyTable() Table {
	return Table{
		PublicTCP: []int{}, PublicUDP: []int{}, VPNUDP: []int{}, WANInterfaces: []string{},
		LANInterfaces: []string{}, FailClosedInterfaces: []string{}, SSHPorts: []int{},
		SSHAllowed: []Allowance{}, HomeNetworks: []string{},
	}
}

// The parts of nft's JSON that are read; everything else is ignored.
type nftObject struct {
	Table *struct {
		Family string `json:"family"`
		Name   string `json:"name"`
	} `json:"table"`
	Chain *struct {
		Family string `json:"family"`
		Table  string `json:"table"`
		Name   string `json:"name"`
		Type   string `json:"type"`
		Hook   string `json:"hook"`
	} `json:"chain"`
	Counter *struct {
		Family  string `json:"family"`
		Table   string `json:"table"`
		Name    string `json:"name"`
		Packets uint64 `json:"packets"`
		Bytes   uint64 `json:"bytes"`
	} `json:"counter"`
	Set *struct {
		Family string            `json:"family"`
		Table  string            `json:"table"`
		Name   string            `json:"name"`
		Elem   []json.RawMessage `json:"elem"`
	} `json:"set"`
	Rule *struct {
		Family string            `json:"family"`
		Table  string            `json:"table"`
		Chain  string            `json:"chain"`
		Expr   []json.RawMessage `json:"expr"`
	} `json:"rule"`
}

var ifnamePattern = regexp.MustCompile(`^[A-Za-z0-9_.@:-]{1,15}$`)

var errNotNft = errors.New("not an answer of nft -j")

// parseTable reads the answer of `nft -j list table inet cosmos_host_guard`.
func parseTable(out []byte) (Table, error) {
	var answer struct {
		Nftables []nftObject `json:"nftables"`
	}
	if err := json.Unmarshal(out, &answer); err != nil || answer.Nftables == nil {
		return Table{}, errNotNft
	}
	table := emptyTable()
	var allow4, allow6 []Allowance
	var home4, home6 []string
	ours := func(family, name string) bool { return family == guardFamily && name == guardTable }
	for _, object := range answer.Nftables {
		switch {
		case object.Table != nil:
			if ours(object.Table.Family, object.Table.Name) {
				table.Loaded = true
			}
		case object.Chain != nil:
			c := object.Chain
			if !ours(c.Family, c.Table) || c.Type != "filter" {
				continue
			}
			if c.Name == "input" && c.Hook == "input" {
				table.InputHooked = true
			}
			if c.Name == "forward" && c.Hook == "forward" {
				table.ForwardHooked = true
			}
		case object.Counter != nil:
			c := object.Counter
			if !ours(c.Family, c.Table) {
				continue
			}
			counter := &Counter{Packets: c.Packets, Bytes: c.Bytes}
			switch c.Name {
			case "dropped_input":
				table.DroppedInput = counter
			case "dropped_forward":
				table.DroppedForward = counter
			case "dropped_spoofed":
				table.DroppedSpoofed = counter
			}
		case object.Rule != nil:
			r := object.Rule
			if !ours(r.Family, r.Table) {
				continue
			}
			switch {
			case r.Chain == "input" && isCountedDrop(r.Expr, "dropped_input"):
				table.InputFilters = true
			case r.Chain == "forward" && isCountedDrop(r.Expr, "dropped_forward"):
				table.ForwardFilters = true
			case r.Chain == "input" && isIPv4Pass(r.Expr):
				table.IPv4Passed = true
			}
		case object.Set != nil:
			s := object.Set
			if !ours(s.Family, s.Table) {
				continue
			}
			switch s.Name {
			case "public_tcp":
				table.PublicTCP = portElements(s.Elem)
			case "public_udp":
				table.PublicUDP = portElements(s.Elem)
			case "vpn_udp":
				table.VPNUDP = portElements(s.Elem)
			case "wan_if":
				table.WANInterfaces = nameElements(s.Elem)
			case "lan_if":
				table.LANInterfaces = nameElements(s.Elem)
			case "failclosed_if":
				table.FailClosedInterfaces = nameElements(s.Elem)
			case "ssh_port":
				table.SSHPorts = portElements(s.Elem)
			case "ssh_allow4":
				allow4 = allowanceElements(s.Elem, true)
			case "ssh_allow6":
				allow6 = allowanceElements(s.Elem, false)
			case "home4":
				home4 = networkElements(s.Elem, true)
			case "home6":
				home6 = networkElements(s.Elem, false)
			}
		}
	}
	if !table.Loaded {
		// an answer without the table is no answer about it
		return Table{}, errNotNft
	}
	table.SSHAllowed = append(append(table.SSHAllowed, allow4...), allow6...)
	table.HomeNetworks = append(append(table.HomeNetworks, home4...), home6...)
	return table, nil
}

// elementValue unwraps an element given as {"elem": {"val": ...}} (a set
// with counters or timeouts) to its value.
func elementValue(raw json.RawMessage) json.RawMessage {
	var wrapped struct {
		Elem *struct {
			Val json.RawMessage `json:"val"`
		} `json:"elem"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && wrapped.Elem != nil && wrapped.Elem.Val != nil {
		return wrapped.Elem.Val
	}
	return raw
}

// portElements keeps the elements that are port numbers (1 to 65535),
// sorted, at most maxElements.
func portElements(elements []json.RawMessage) []int {
	ports := []int{}
	seen := map[int]bool{}
	for _, raw := range elements {
		if len(ports) >= maxElements {
			break
		}
		var port int
		if json.Unmarshal(elementValue(raw), &port) != nil || port < 1 || port > 65535 || seen[port] {
			continue
		}
		seen[port] = true
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}

// nameElements keeps the elements that are interface names, sorted, at
// most maxElements.
func nameElements(elements []json.RawMessage) []string {
	names := []string{}
	seen := map[string]bool{}
	for _, raw := range elements {
		if len(names) >= maxElements {
			break
		}
		var name string
		if json.Unmarshal(elementValue(raw), &name) != nil || !ifnamePattern.MatchString(name) || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// isCountedDrop: the rule is exactly "counter name <counter> drop", the last
// rule of each of the guard's chains.
func isCountedDrop(expr []json.RawMessage, counter string) bool {
	if len(expr) != 2 {
		return false
	}
	var first struct {
		Counter *string `json:"counter"`
	}
	var second map[string]json.RawMessage
	if json.Unmarshal(expr[0], &first) != nil || first.Counter == nil || *first.Counter != counter {
		return false
	}
	if json.Unmarshal(expr[1], &second) != nil || len(second) != 1 {
		return false
	}
	_, drop := second["drop"]
	return drop
}

// isIPv4Pass: the rule is exactly "meta nfproto ipv4 accept", which the
// installer puts in when IPv4 is not to be filtered.
func isIPv4Pass(expr []json.RawMessage) bool {
	if len(expr) != 2 {
		return false
	}
	var first struct {
		Match *struct {
			Op   string `json:"op"`
			Left struct {
				Meta *struct {
					Key string `json:"key"`
				} `json:"meta"`
			} `json:"left"`
			Right json.RawMessage `json:"right"`
		} `json:"match"`
	}
	var second map[string]json.RawMessage
	if json.Unmarshal(expr[0], &first) != nil || first.Match == nil || first.Match.Left.Meta == nil {
		return false
	}
	m := first.Match
	if (m.Op != "==" && m.Op != "in") || m.Left.Meta.Key != "nfproto" || string(m.Right) != `"ipv4"` {
		return false
	}
	if json.Unmarshal(expr[1], &second) != nil || len(second) != 1 {
		return false
	}
	_, accept := second["accept"]
	return accept
}

// maxExpires bounds the seconds an allowance may have left (ten years):
// anything beyond is not an answer of nft and is left out.
const maxExpires = 10 * 365 * 24 * 3600

// addressOf parses an address of the given family (no zone, no IPv4 in
// IPv6 clothing).
func addressOf(text string, four bool) (netip.Addr, bool) {
	if len(text) > 64 {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(text)
	if err != nil || addr.Zone() != "" || addr.Is4() != four || addr.Is4In6() {
		return netip.Addr{}, false
	}
	return addr, true
}

// allowanceElements keeps the elements of ssh_allow4/6 that are addresses
// of the set's family, with the seconds they have left, at most
// maxElements.
func allowanceElements(elements []json.RawMessage, four bool) []Allowance {
	allowed := []Allowance{}
	seen := map[netip.Addr]bool{}
	for _, raw := range elements {
		if len(allowed) >= maxElements {
			break
		}
		var text string
		var expires *int64
		var wrapped struct {
			Elem *struct {
				Val     json.RawMessage `json:"val"`
				Expires *int64          `json:"expires"`
			} `json:"elem"`
		}
		if json.Unmarshal(raw, &text) != nil {
			if json.Unmarshal(raw, &wrapped) != nil || wrapped.Elem == nil || json.Unmarshal(wrapped.Elem.Val, &text) != nil {
				continue
			}
			if e := wrapped.Elem.Expires; e != nil && *e >= 0 && *e <= maxExpires {
				expires = e
			}
		}
		addr, ok := addressOf(text, four)
		if !ok || seen[addr] {
			continue
		}
		seen[addr] = true
		allowed = append(allowed, Allowance{Address: addr.String(), Expires: expires})
	}
	return allowed
}

// networkElements keeps the elements of home4/6 (an address, a prefix or a
// range of the set's family) as text, at most maxElements.
func networkElements(elements []json.RawMessage, four bool) []string {
	networks := []string{}
	seen := map[string]bool{}
	for _, raw := range elements {
		if len(networks) >= maxElements {
			break
		}
		var text string
		var shaped struct {
			Prefix *struct {
				Addr string `json:"addr"`
				Len  int    `json:"len"`
			} `json:"prefix"`
			Range []string `json:"range"`
		}
		value := elementValue(raw)
		isText := json.Unmarshal(value, &text) == nil
		isShaped := !isText && json.Unmarshal(value, &shaped) == nil
		switch {
		case isText:
			addr, ok := addressOf(text, four)
			if !ok {
				continue
			}
			text = addr.String()
		case isShaped && shaped.Prefix != nil:
			addr, ok := addressOf(shaped.Prefix.Addr, four)
			if !ok || shaped.Prefix.Len < 0 || shaped.Prefix.Len > addr.BitLen() {
				continue
			}
			prefix := netip.PrefixFrom(addr, shaped.Prefix.Len)
			if !prefix.IsValid() {
				continue
			}
			text = prefix.Masked().String()
		case isShaped && len(shaped.Range) == 2:
			from, ok1 := addressOf(shaped.Range[0], four)
			to, ok2 := addressOf(shaped.Range[1], four)
			if !ok1 || !ok2 || to.Less(from) {
				continue
			}
			text = from.String() + "-" + to.String()
		default:
			continue
		}
		if seen[text] {
			continue
		}
		seen[text] = true
		networks = append(networks, text)
	}
	return networks
}

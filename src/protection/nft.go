package protection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	InputHooked    bool     `json:"inputHooked"`
	ForwardHooked  bool     `json:"forwardHooked"`
	DroppedInput   *Counter `json:"droppedInput,omitempty"`
	DroppedForward *Counter `json:"droppedForward,omitempty"`
	PublicTCP      []int    `json:"publicTcp"`
	PublicUDP      []int    `json:"publicUdp"`
	VPNUDP         []int    `json:"vpnUdp"`
	// WANInterfaces are the interfaces the guard filters (those with a
	// default route).
	WANInterfaces []string `json:"wanInterfaces"`
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
	return Table{PublicTCP: []int{}, PublicUDP: []int{}, VPNUDP: []int{}, WANInterfaces: []string{}}
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
			}
		}
	}
	if !table.Loaded {
		// an answer without the table is no answer about it
		return Table{}, errNotNft
	}
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

package protection

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The fixtures are real answers of nft 1.1.3 (Debian 13) to
// `nft -j list table inet cosmos_host_guard`, with the table of
// fork/nftables/cosmos-host-guard.nft.in loaded in a throwaway container:
// nft-guard-loaded.json with public_tcp 2052,2053, vpn_udp 41641, wan_if
// eth0 and lo, after two refused connections on lo (4 packets, 240 bytes);
// nft-guard-udp.json with public_tcp 443 and public_udp 443,51820. Those two
// hold the guard's first table; the nft-guard-v2-*.json ones the table of
// today (lan_if, failclosed_if, ssh_port, ssh_allow4/6, home4/6 as --home
// only, dropped_spoofed), each loaded with `#@IPV4@` replaced by nothing
// (IPv4 filtered) or by `meta nfproto ipv4 accept`:
//   - v2-ipv4: IPv4 filtered; wan_if eth0, public_tcp 2053, vpn_udp 41641,
//     ssh_port 22, ssh_allow6 2001:db8:2::20 (timeout 24h), ssh_allow4
//     198.51.100.7 (timeout 2h), home4 192.0.2.0/24 and 203.0.113.5, home6
//     2001:db8:5::/48
//   - v2-lan: IPv4 passed; wan_if eth0 and wlan0, lan_if eth0, public_tcp
//     22,2053, public_udp 443, ssh_port 22
//   - v2-failclosed: IPv4 passed; wan_if and failclosed_if eth0 and eth1
//   - v2-flushed: v2-failclosed after `nft flush chain inet cosmos_host_guard input`
//   - v2-nowan: IPv4 passed; wan_if empty, public_tcp 2053
//
// The nft-guard-v3-*.json ones hold the table with IPv4 passed per interface
// (pass4_if), rpf_if, trusted_if, pending6 and dropped_rpf, each with
// public_tcp 2053 and ssh_port 22:
//   - v3-pass4: wan_if eth0 and wlan0, lan_if and pass4_if eth0, rpf_if
//     wlan0, vpn_udp 41641, pending6 2001:db8:5::/64, ssh_allow4
//     198.51.100.7 (no timeout), ssh_allow6 2001:db8:2::20 (timeout 24h)
//   - v3-filtered: wan_if and rpf_if eth0, public_udp 443
//   - v3-trusted: wan_if eth0 and tun0, trusted_if tun0, rpf_if eth0
func fixtureTable(t *testing.T, name string) Table {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	table, err := parseTable(content)
	if err != nil {
		t.Fatal(err)
	}
	return table
}

func TestTableOfARealNft(t *testing.T) {
	table := fixtureTable(t, "nft-guard-loaded.json")
	want := Table{
		Loaded: true, InputHooked: true, ForwardHooked: true,
		DroppedInput: &Counter{Packets: 4, Bytes: 240}, DroppedForward: &Counter{},
		InputFilters: true, ForwardFilters: true,
		PublicTCP: []int{2052, 2053}, PublicUDP: []int{}, VPNUDP: []int{41641},
		WANInterfaces: []string{"eth0", "lo"}, LANInterfaces: []string{}, FailClosedInterfaces: []string{},
		SSHPorts: []int{}, SSHAllowed: []Allowance{}, IPv4PassInterfaces: []string{}, RPFInterfaces: []string{},
		TrustedInterfaces: []string{}, VPNHomeInterfaces: []string{}, PendingPrefixes: []string{},
		HomeNetworks: []string{"10.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7", "fe80::/10"},
	}
	if !reflect.DeepEqual(table, want) {
		got, _ := json.Marshal(table)
		t.Errorf("table %s", got)
	}
	table = fixtureTable(t, "nft-guard-udp.json")
	if !reflect.DeepEqual(table.PublicTCP, []int{443}) || !reflect.DeepEqual(table.PublicUDP, []int{443, 51820}) || len(table.WANInterfaces) != 0 || len(table.VPNUDP) != 0 {
		t.Errorf("table %+v", table)
	}
}

func TestTableWithAChainThatIsNotHooked(t *testing.T) {
	content, _ := os.ReadFile(filepath.Join("testdata", "nft-guard-loaded.json"))
	// the same table with the input chain a regular one (no type, no hook)
	edited := strings.Replace(string(content), `"name": "input", "handle": 1, "type": "filter", "hook": "input", "prio": -5, "policy": "accept"`, `"name": "input", "handle": 1`, 1)
	if edited == string(content) {
		t.Fatal("the fixture does not have the chain as expected")
	}
	table, err := parseTable([]byte(edited))
	if err != nil || table.InputHooked || !table.ForwardHooked {
		t.Errorf("table %+v %v, want the input chain not hooked", table, err)
	}
}

func TestTableTakesOnlyItsOwnAndOnlyValidElements(t *testing.T) {
	answer := `{"nftables": [
		{"metainfo": {"version": "1.1.3", "json_schema_version": 1}},
		{"table": {"family": "inet", "name": "cosmos_host_guard", "handle": 1}},
		{"chain": {"family": "ip", "table": "filter", "name": "input", "type": "filter", "hook": "input"}},
		{"chain": {"family": "inet", "table": "cosmos_host_guard", "name": "input", "type": "filter", "hook": "output"}},
		{"chain": {"family": "inet", "table": "cosmos_host_guard", "name": "forward", "type": "nat", "hook": "forward"}},
		{"counter": {"family": "inet", "table": "other", "name": "dropped_input", "packets": 9, "bytes": 9}},
		{"set": {"family": "inet", "table": "cosmos_host_guard", "name": "public_tcp", "elem": [443, 0, 70000, "x", {"elem": {"val": 8443, "counter": {"packets": 1}}}, 443, {"range": [1000, 2000]}]}},
		{"set": {"family": "inet", "table": "cosmos_host_guard", "name": "wan_if", "elem": ["eth0", "this-name-is-far-too-long", "bad name", "$(x)", "wlan0"]}},
		{"set": {"family": "inet", "table": "other", "name": "vpn_udp", "elem": [51820]}}
	]}`
	table, err := parseTable([]byte(answer))
	if err != nil {
		t.Fatal(err)
	}
	if table.InputHooked || table.ForwardHooked || table.DroppedInput != nil || len(table.VPNUDP) != 0 {
		t.Errorf("took another table's objects: %+v", table)
	}
	if !reflect.DeepEqual(table.PublicTCP, []int{443, 8443}) || !reflect.DeepEqual(table.WANInterfaces, []string{"eth0", "wlan0"}) {
		t.Errorf("elements %v %v, want [443 8443] [eth0 wlan0]", table.PublicTCP, table.WANInterfaces)
	}

	many := make([]int, maxElements+50)
	for i := range many {
		many[i] = i + 1
	}
	elements, _ := json.Marshal(many)
	table, err = parseTable([]byte(`{"nftables": [{"table": {"family": "inet", "name": "cosmos_host_guard"}}, {"set": {"family": "inet", "table": "cosmos_host_guard", "name": "public_tcp", "elem": ` + string(elements) + `}}]}`))
	if err != nil || len(table.PublicTCP) != maxElements {
		t.Errorf("a long set: %d elements kept, want %d", len(table.PublicTCP), maxElements)
	}
}

func TestTableAnswersThatAreNotOne(t *testing.T) {
	for name, answer := range map[string]string{
		"empty":            "",
		"not JSON":         "table inet cosmos_host_guard {",
		"no nftables":      `{"other": []}`,
		"another table":    `{"nftables": [{"table": {"family": "ip", "name": "cosmos_host_guard"}}]}`,
		"only the version": `{"nftables": [{"metainfo": {"version": "1.1.3"}}]}`,
	} {
		if table, err := parseTable([]byte(answer)); err == nil {
			t.Errorf("%s: taken as %+v", name, table)
		}
	}
}

func TestLiveStateFromWhatNftSays(t *testing.T) {
	w := newWorld(t)
	if live := readTable(); live.unknown != unknownNoNft || w.nftCalls.Load() != 0 {
		t.Errorf("without nft: %+v after %d runs, want noNft and no run", live, w.nftCalls.Load())
	}

	w.table("nft-guard-loaded.json")
	if live := readTable(); live.unknown != "" || !live.table.Loaded {
		t.Errorf("the table: %+v", live)
	}

	w.noTable()
	if live := readTable(); live.unknown != "" || live.table.Loaded || live.table.PublicTCP == nil {
		t.Errorf("no table: %+v, want known and not loaded", live)
	}

	// what nft 1.1.3 says to a user who is not root
	w.nftErr = "Operation not permitted (you must be root)\nnetlink: Error: cache initialization failed: Operation not permitted\n"
	if live := readTable(); live.unknown != unknownNotPermitted {
		t.Errorf("not root: %+v, want notPermitted", live)
	}

	w.nftErr, w.nftRun = "", errStandIn
	if live := readTable(); live.unknown != unknownFailed {
		t.Errorf("another failure: %+v, want failed", live)
	}

	w.nftOut, w.nftRun = []byte("not json"), nil
	if live := readTable(); live.unknown != unknownFailed {
		t.Errorf("an answer that cannot be read: %+v, want failed", live)
	}
}

// The runner itself, with a script standing in for nft.
func TestNftRunsWithFixedArgumentsAndBounds(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := filepath.Join(dir, "nft")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+argsFile+"\nenv > "+argsFile+".env\nprintf '{\"nftables\": []}'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	out, _, err := realRunNft(script)
	if err != nil || string(out) != `{"nftables": []}` {
		t.Fatalf("%q %v", out, err)
	}
	args, _ := os.ReadFile(argsFile)
	if string(args) != "-j\nlist\ntable\ninet\ncosmos_host_guard\n" {
		t.Errorf("arguments %q", args)
	}
	env, _ := os.ReadFile(argsFile + ".env")
	if strings.Contains(string(env), "HOME=") || !strings.Contains(string(env), "LC_ALL=C") {
		t.Errorf("environment %q, want only the fixed one", env)
	}

	// more than 1 MiB: refused
	if err := os.WriteFile(script, []byte("#!/bin/sh\nhead -c 1100000 /dev/zero\n"), 0755); err != nil {
		t.Fatal(err)
	}
	out, _, err = realRunNft(script)
	if err == nil || len(out) > maxNftOutput {
		t.Errorf("a large answer: %d bytes, %v; want refused and at most %d bytes kept", len(out), err, maxNftOutput)
	}

	// an nft that hangs is stopped after the timeout
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, _, err = realRunNft(script); err == nil {
		t.Error("a hanging nft did not fail")
	}
	if took := time.Since(start); took > nftTimeout+2*time.Second {
		t.Errorf("a hanging nft held the request for %v", took)
	}
}

func int64p(v int64) *int64 { return &v }

func TestTableOfTodaysGuard(t *testing.T) {
	table := fixtureTable(t, "nft-guard-v2-ipv4.json")
	want := Table{
		Loaded: true, InputHooked: true, ForwardHooked: true, InputFilters: true, ForwardFilters: true,
		DroppedInput: &Counter{}, DroppedForward: &Counter{}, DroppedSpoofed: &Counter{},
		PublicTCP: []int{2053}, PublicUDP: []int{}, VPNUDP: []int{41641},
		WANInterfaces: []string{"eth0"}, LANInterfaces: []string{}, FailClosedInterfaces: []string{},
		SSHPorts: []int{22},
		SSHAllowed: []Allowance{
			{Address: "198.51.100.7", Expires: int64p(7199)},
			{Address: "2001:db8:2::20", Expires: int64p(86399)},
		},
		HomeNetworks:       []string{"192.0.2.0/24", "203.0.113.5", "2001:db8:5::/48"},
		IPv4PassInterfaces: []string{}, RPFInterfaces: []string{}, TrustedInterfaces: []string{}, VPNHomeInterfaces: []string{}, PendingPrefixes: []string{},
	}
	if !reflect.DeepEqual(table, want) {
		got, _ := json.Marshal(table)
		t.Errorf("table %s", got)
	}

	table = fixtureTable(t, "nft-guard-v2-lan.json")
	if !table.IPv4Passed || !table.InputFilters || !reflect.DeepEqual(table.WANInterfaces, []string{"eth0", "wlan0"}) || !reflect.DeepEqual(table.LANInterfaces, []string{"eth0"}) ||
		!reflect.DeepEqual(table.PublicTCP, []int{22, 2053}) || !reflect.DeepEqual(table.PublicUDP, []int{443}) || len(table.HomeNetworks) != 0 || len(table.SSHAllowed) != 0 {
		t.Errorf("behind a router: %+v", table)
	}

	table = fixtureTable(t, "nft-guard-v2-failclosed.json")
	if !table.InputFilters || !reflect.DeepEqual(table.FailClosedInterfaces, []string{"eth0", "eth1"}) || !reflect.DeepEqual(table.WANInterfaces, []string{"eth0", "eth1"}) {
		t.Errorf("fail closed: %+v", table)
	}

	// flushed: hooked, but the input chain holds nothing; the forward chain
	// still passes IPv4 to the containers
	table = fixtureTable(t, "nft-guard-v2-flushed.json")
	if !table.InputHooked || !table.ForwardHooked || table.InputFilters || !table.ForwardFilters || !table.IPv4Passed {
		t.Errorf("flushed: %+v", table)
	}

	table = fixtureTable(t, "nft-guard-v2-nowan.json")
	if !table.InputFilters || len(table.WANInterfaces) != 0 {
		t.Errorf("no outside interface: %+v", table)
	}
}

// Only the exact catch-all drop counts as the chain holding its rules, and
// only the exact IPv4 pass as IPv4 passed.
func TestTableRulesThatCount(t *testing.T) {
	rule := func(chain, expr string) string {
		return `{"rule": {"family": "inet", "table": "cosmos_host_guard", "chain": "` + chain + `", "expr": ` + expr + `}}`
	}
	answer := func(rules ...string) Table {
		t.Helper()
		table, err := parseTable([]byte(`{"nftables": [{"table": {"family": "inet", "name": "cosmos_host_guard"}}, ` + strings.Join(append(rules, `{"metainfo": {}}`), ", ") + `]}`))
		if err != nil {
			t.Fatal(err)
		}
		return table
	}
	drop := `[{"counter": "dropped_input"}, {"drop": null}]`
	if table := answer(rule("input", drop)); !table.InputFilters || table.ForwardFilters {
		t.Errorf("the input drop: %+v", table)
	}
	for name, table := range map[string]Table{
		"in another chain":     answer(rule("forward", drop)),
		"another counter":      answer(rule("input", `[{"counter": "dropped_spoofed"}, {"drop": null}]`)),
		"an accept":            answer(rule("input", `[{"counter": "dropped_input"}, {"accept": null}]`)),
		"with a match first":   answer(rule("input", `[{"match": {"op": "==", "left": {"ct": {"key": "state"}}, "right": "invalid"}}, {"counter": "dropped_input"}, {"drop": null}]`)),
		"of another table":     answer(`{"rule": {"family": "inet", "table": "other", "chain": "input", "expr": ` + drop + `}}`),
		"an anonymous counter": answer(rule("input", `[{"counter": {"packets": 0, "bytes": 0}}, {"drop": null}]`)),
	} {
		if table.InputFilters {
			t.Errorf("%s: taken as the input chain's drop", name)
		}
	}
	pass := `[{"match": {"op": "==", "left": {"meta": {"key": "nfproto"}}, "right": "ipv4"}}, {"accept": null}]`
	if table := answer(rule("input", pass)); !table.IPv4Passed {
		t.Error("the IPv4 pass was not seen")
	}
	for name, expr := range map[string]string{
		"ipv6":      strings.Replace(pass, "ipv4", "ipv6", 1),
		"a drop":    strings.Replace(pass, "accept", "drop", 1),
		"not ==":    strings.Replace(pass, `"=="`, `"!="`, 1),
		"meta mark": strings.Replace(pass, "nfproto", "mark", 1),
	} {
		if table := answer(rule("input", expr)); table.IPv4Passed {
			t.Errorf("%s: taken as the IPv4 pass", name)
		}
	}
	// in forward it opens IPv4 to the containers: passed as well
	if table := answer(rule("forward", pass)); !table.IPv4Passed {
		t.Error("an IPv4 pass in forward was not seen")
	}
	if table := answer(rule("output", pass)); table.IPv4Passed {
		t.Error("an IPv4 pass in another chain taken as one of the guard's")
	}
}

func TestTableOfTheGuardWithIPv4PerInterface(t *testing.T) {
	table := fixtureTable(t, "nft-guard-v3-pass4.json")
	want := Table{
		Loaded: true, InputHooked: true, ForwardHooked: true, InputFilters: true, ForwardFilters: true,
		DroppedInput: &Counter{}, DroppedForward: &Counter{}, DroppedSpoofed: &Counter{}, DroppedRPF: &Counter{},
		IPv4PassInterfaces: []string{"eth0"}, RPFInterfaces: []string{"wlan0"}, TrustedInterfaces: []string{}, VPNHomeInterfaces: []string{},
		PendingPrefixes: []string{"2001:db8:5::/64"},
		PublicTCP:       []int{2053}, PublicUDP: []int{}, VPNUDP: []int{41641},
		WANInterfaces: []string{"eth0", "wlan0"}, LANInterfaces: []string{"eth0"}, FailClosedInterfaces: []string{},
		SSHPorts: []int{22},
		SSHAllowed: []Allowance{
			{Address: "198.51.100.7"},
			{Address: "2001:db8:2::20", Expires: int64p(86399)},
		},
		HomeNetworks: []string{},
	}
	if !reflect.DeepEqual(table, want) {
		got, _ := json.Marshal(table)
		t.Errorf("table %s", got)
	}

	table = fixtureTable(t, "nft-guard-v3-filtered.json")
	if table.IPv4Passed || len(table.IPv4PassInterfaces) != 0 || table.IPv4PassInterfaces == nil || !reflect.DeepEqual(table.RPFInterfaces, []string{"eth0"}) || len(table.PendingPrefixes) != 0 || len(table.TrustedInterfaces) != 0 {
		t.Errorf("IPv4 filtered everywhere: %+v", table)
	}

	table = fixtureTable(t, "nft-guard-v3-trusted.json")
	if !reflect.DeepEqual(table.TrustedInterfaces, []string{"tun0"}) || !reflect.DeepEqual(table.WANInterfaces, []string{"eth0", "tun0"}) {
		t.Errorf("a trusted uplink: %+v", table)
	}

	// the set's elements count only while a chain holds the rule that passes them
	content, _ := os.ReadFile(filepath.Join("testdata", "nft-guard-v3-pass4.json"))
	rule := `{"match": {"op": "==", "left": {"meta": {"key": "iifname"}}, "right": "@pass4_if"}}, {"match": {"op": "==", "left": {"meta": {"key": "nfproto"}}, "right": "ipv4"}}, {"accept": null}`
	if strings.Count(string(content), rule) != 2 {
		t.Fatalf("the fixture does not hold the pass rule in both chains")
	}
	for name, replacement := range map[string]string{
		"no rule":           `{"match": {"op": "==", "left": {"meta": {"key": "iifname"}}, "right": "@pass4_if"}}, {"match": {"op": "==", "left": {"meta": {"key": "nfproto"}}, "right": "ipv4"}}, {"drop": null}`,
		"another set":       strings.Replace(rule, "@pass4_if", "@lan_if", 1),
		"IPv6":              strings.Replace(rule, `"ipv4"`, `"ipv6"`, 1),
		"not the interface": strings.Replace(rule, `"op": "=="`, `"op": "!="`, 1),
		"one match more":    `{"match": {"op": "==", "left": {"meta": {"key": "iifname"}}, "right": "@pass4_if"}}, {"match": {"op": "==", "left": {"meta": {"key": "nfproto"}}, "right": "ipv4"}}, {"match": {"op": "==", "left": {"meta": {"key": "mark"}}, "right": 1}}, {"accept": null}`,
	} {
		table, err := parseTable([]byte(strings.ReplaceAll(string(content), rule, replacement)))
		if err != nil {
			t.Fatal(err)
		}
		if len(table.IPv4PassInterfaces) != 0 || table.IPv4Passed {
			t.Errorf("%s: IPv4 taken as passed on %v (%v)", name, table.IPv4PassInterfaces, table.IPv4Passed)
		}
	}
	// in one chain alone it still opens IPv4 there
	first := strings.Index(string(content), rule)
	inForward := string(content[:first]) + strings.Replace(string(content[first:]), rule, `{"drop": null}`, 1)
	if table, _ := parseTable([]byte(inForward)); !reflect.DeepEqual(table.IPv4PassInterfaces, []string{"eth0"}) {
		t.Errorf("the pass in one chain only: %v", table.IPv4PassInterfaces)
	}
}

func TestTablePendingPrefixesAndNewSetsAreValidated(t *testing.T) {
	answer := `{"nftables": [
		{"table": {"family": "inet", "name": "cosmos_host_guard"}},
		{"set": {"family": "inet", "table": "cosmos_host_guard", "name": "pending6", "elem": [
			{"prefix": {"addr": "2001:db8:5::", "len": 64}}, {"prefix": {"addr": "2001:db8:5::1", "len": 64}}, "192.0.2.1",
			{"prefix": {"addr": "192.0.2.0", "len": 24}}, {"prefix": {"addr": "2001:db8::", "len": 129}}, "fe80::1%eth0", "x",
			{"range": ["2001:db8:7::", "2001:db8:7::ff"]}]}},
		{"set": {"family": "inet", "table": "cosmos_host_guard", "name": "trusted_if", "elem": ["tun0", "bad name", "$(x)", "this-name-is-far-too-long"]}},
		{"set": {"family": "inet", "table": "cosmos_host_guard", "name": "rpf_if", "elem": ["eth0", "eth0", 7]}},
		{"set": {"family": "inet", "table": "cosmos_host_guard", "name": "vpnhome_if", "elem": ["tailscale0", "bad name", 3]}},
		{"set": {"family": "inet", "table": "cosmos_host_guard", "name": "pending4", "elem": [{"prefix": {"addr": "192.168.77.0", "len": 24}}, "2001:db8::1", "x"]}},
		{"set": {"family": "inet", "table": "other", "name": "trusted_if", "elem": ["wg9"]}},
		{"counter": {"family": "inet", "table": "cosmos_host_guard", "name": "dropped_rpf", "packets": 5, "bytes": 300}},
		{"counter": {"family": "inet", "table": "other", "name": "dropped_rpf", "packets": 9, "bytes": 9}}
	]}`
	table, err := parseTable([]byte(answer))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"2001:db8:5::/64", "2001:db8:7::-2001:db8:7::ff", "192.168.77.0/24"}; !reflect.DeepEqual(table.PendingPrefixes, want) {
		t.Errorf("pending %v, want %v", table.PendingPrefixes, want)
	}
	if !reflect.DeepEqual(table.TrustedInterfaces, []string{"tun0"}) || !reflect.DeepEqual(table.RPFInterfaces, []string{"eth0"}) || !reflect.DeepEqual(table.VPNHomeInterfaces, []string{"tailscale0"}) {
		t.Errorf("interfaces %v %v %v", table.TrustedInterfaces, table.RPFInterfaces, table.VPNHomeInterfaces)
	}
	if table.DroppedRPF == nil || table.DroppedRPF.Packets != 5 {
		t.Errorf("dropped_rpf %+v", table.DroppedRPF)
	}
	many := []string{}
	for i := 0; i < maxElements+40; i++ {
		many = append(many, fmt.Sprintf(`{"prefix": {"addr": "2001:db8:%x::", "len": 64}}`, i+1))
	}
	table, err = parseTable([]byte(`{"nftables": [{"table": {"family": "inet", "name": "cosmos_host_guard"}}, {"set": {"family": "inet", "table": "cosmos_host_guard", "name": "pending6", "elem": [` + strings.Join(many, ",") + `]}}]}`))
	if err != nil || len(table.PendingPrefixes) != maxElements {
		t.Errorf("a long set: %d prefixes kept, want %d", len(table.PendingPrefixes), maxElements)
	}
}

func TestTableAllowancesAndHomeAreValidated(t *testing.T) {
	answer := `{"nftables": [
		{"table": {"family": "inet", "name": "cosmos_host_guard"}},
		{"set": {"family": "inet", "table": "cosmos_host_guard", "name": "ssh_allow4", "elem": [
			{"elem": {"val": "192.0.2.1", "timeout": 60, "expires": 30}},
			{"elem": {"val": "192.0.2.1", "timeout": 60, "expires": 20}},
			"192.0.2.2",
			{"elem": {"val": "192.0.2.3", "expires": -5}},
			{"elem": {"val": "192.0.2.4", "expires": 999999999999}},
			"2001:db8::1", "::ffff:192.0.2.9", "192.0.2.300", "x", 7, {"elem": {"val": 7}}]}},
		{"set": {"family": "inet", "table": "cosmos_host_guard", "name": "ssh_allow6", "elem": [
			{"elem": {"val": "2001:DB8::2", "expires": 10}}, "fe80::1%eth0", "192.0.2.5", "2001:db8::3"]}},
		{"set": {"family": "inet", "table": "cosmos_host_guard", "name": "home4", "elem": [
			{"prefix": {"addr": "192.0.2.0", "len": 24}}, {"prefix": {"addr": "192.0.2.7", "len": 24}},
			{"prefix": {"addr": "192.0.2.0", "len": 33}}, {"prefix": {"addr": "2001:db8::", "len": 32}},
			{"range": ["198.51.100.10", "198.51.100.20"]}, {"range": ["198.51.100.20", "198.51.100.10"]},
			{"range": ["198.51.100.10", "2001:db8::1"]}, "203.0.113.5", "not an address", {"other": 1}]}},
		{"set": {"family": "inet", "table": "cosmos_host_guard", "name": "home6", "elem": [
			{"prefix": {"addr": "2001:db8:5::", "len": 48}}, "192.0.2.1", {"prefix": {"addr": "2001:db8::", "len": 129}}]}},
		{"set": {"family": "inet", "table": "cosmos_host_guard", "name": "lan_if", "elem": ["eth0", "bad name"]}},
		{"set": {"family": "inet", "table": "cosmos_host_guard", "name": "failclosed_if", "elem": ["eth1", "$(x)"]}}
	]}`
	table, err := parseTable([]byte(answer))
	if err != nil {
		t.Fatal(err)
	}
	want := []Allowance{
		{Address: "192.0.2.1", Expires: int64p(30)}, {Address: "192.0.2.2"}, {Address: "192.0.2.3"}, {Address: "192.0.2.4"},
		{Address: "2001:db8::2", Expires: int64p(10)}, {Address: "2001:db8::3"},
	}
	if !reflect.DeepEqual(table.SSHAllowed, want) {
		got, _ := json.Marshal(table.SSHAllowed)
		t.Errorf("allowances %s", got)
	}
	if wantHome := []string{"192.0.2.0/24", "198.51.100.10-198.51.100.20", "203.0.113.5", "2001:db8:5::/48"}; !reflect.DeepEqual(table.HomeNetworks, wantHome) {
		t.Errorf("home %v, want %v", table.HomeNetworks, wantHome)
	}
	if !reflect.DeepEqual(table.LANInterfaces, []string{"eth0"}) || !reflect.DeepEqual(table.FailClosedInterfaces, []string{"eth1"}) {
		t.Errorf("interfaces %v %v", table.LANInterfaces, table.FailClosedInterfaces)
	}

	// bounded like the other sets
	many := []string{}
	for i := 0; i < maxElements+40; i++ {
		many = append(many, fmt.Sprintf(`{"elem": {"val": "2001:db8::%x", "expires": 5}}`, i+1))
	}
	table, err = parseTable([]byte(`{"nftables": [{"table": {"family": "inet", "name": "cosmos_host_guard"}}, {"set": {"family": "inet", "table": "cosmos_host_guard", "name": "ssh_allow6", "elem": [` + strings.Join(many, ",") + `]}}, {"set": {"family": "inet", "table": "cosmos_host_guard", "name": "home6", "elem": [` + strings.Join(many, ",") + `]}}]}`))
	if err != nil || len(table.SSHAllowed) != maxElements || len(table.HomeNetworks) != maxElements {
		t.Errorf("a long set: %d allowances and %d networks kept, want %d", len(table.SSHAllowed), len(table.HomeNetworks), maxElements)
	}
}

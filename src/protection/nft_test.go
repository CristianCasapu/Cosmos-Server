package protection

import (
	"encoding/json"
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
// nft-guard-udp.json with public_tcp 443 and public_udp 443,51820.
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
		PublicTCP: []int{2052, 2053}, PublicUDP: []int{}, VPNUDP: []int{41641},
		WANInterfaces: []string{"eth0", "lo"},
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

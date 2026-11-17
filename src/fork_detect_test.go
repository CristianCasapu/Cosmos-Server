package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/upnp"
)

func fakeDetect(router upnp.RouterProbe, outside func() (string, error)) (detectDeps, *int) {
	asked := 0
	return detectDeps{
		network: func() detectNetwork {
			return detectNetwork{DefaultInterface: "eth0", Gateway: "192.168.1.1", Address: "192.168.1.10", LAN: "192.168.1.0/24"}
		},
		ipv6: func() detectIPv6 {
			return detectIPv6{Global: true, DefaultRoute: true, Addresses: []string{"2001:db8::10"}}
		},
		router: func() upnp.RouterProbe { return router },
		outside: func() (string, error) {
			asked++
			return outside()
		},
	}, &asked
}

func TestDetectPrefersTheRouterAndDoesNotAskOutside(t *testing.T) {
	deps, asked := fakeDetect(upnp.RouterProbe{Answers: true, Name: "Test Router", ExternalIP: "203.0.113.7"},
		func() (string, error) { return "198.51.100.9", nil })
	r := runDetect(deps, false, 2*time.Second)
	if *asked != 0 || r.Outside.Asked {
		t.Fatal("the outside service was asked although the router gave its address (Q4)")
	}
	if r.PublicIP != "203.0.113.7" || r.PublicIPSource != "router" {
		t.Errorf("public address %q from %q", r.PublicIP, r.PublicIPSource)
	}
	if r.SharedAddress == nil || *r.SharedAddress {
		t.Errorf("sharedAddress = %v", r.SharedAddress)
	}
	if r.Network.LAN != "192.168.1.0/24" || !r.IPv6.Global || r.TimedOut {
		t.Errorf("report = %+v", r)
	}
}

func TestDetectSaysASharedAddress(t *testing.T) {
	deps, _ := fakeDetect(upnp.RouterProbe{Answers: true, ExternalIP: "100.64.3.4", NotPublic: true},
		func() (string, error) { return "198.51.100.9", nil })
	r := runDetect(deps, false, 2*time.Second)
	if r.SharedAddress == nil || !*r.SharedAddress {
		t.Errorf("sharedAddress = %v, want true", r.SharedAddress)
	}
}

func TestDetectAsksOutsideOnlyWhenTheRouterIsSilentAndAllowed(t *testing.T) {
	silent := upnp.RouterProbe{Error: "the router did not answer"}
	deps, asked := fakeDetect(silent, func() (string, error) { return "198.51.100.9", nil })
	r := runDetect(deps, false, 2*time.Second)
	if *asked != 1 || !r.Outside.Asked || r.PublicIP != "198.51.100.9" || r.PublicIPSource != "outside" {
		t.Errorf("report = %+v", r)
	}
	if r.SharedAddress != nil {
		t.Error("without the router's address a shared address cannot be told")
	}

	deps, asked = fakeDetect(silent, func() (string, error) { return "198.51.100.9", nil })
	r = runDetect(deps, true, 2*time.Second)
	if *asked != 0 || r.Outside.Asked || r.PublicIP != "" {
		t.Errorf("--no-outside: asked %d times, report %+v", *asked, r)
	}

	deps, _ = fakeDetect(silent, func() (string, error) { return "10.0.0.1", nil })
	if r = runDetect(deps, false, 2*time.Second); r.PublicIP != "" || r.Outside.Error == "" {
		t.Errorf("a private answer was taken: %+v", r)
	}
	deps, _ = fakeDetect(silent, func() (string, error) { return "", errors.New("down\nsecond line") })
	if r = runDetect(deps, false, 2*time.Second); strings.Contains(r.Outside.Error, "\n") || r.Outside.Error == "" {
		t.Errorf("error = %q", r.Outside.Error)
	}
}

func TestDetectEndsInTimeWhateverTheNetworkDoes(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	deps := detectDeps{
		network: func() detectNetwork { return detectNetwork{DefaultInterface: "eth0"} },
		ipv6:    func() detectIPv6 { return detectIPv6{} },
		router:  func() upnp.RouterProbe { <-block; return upnp.RouterProbe{} },
		outside: func() (string, error) { <-block; return "", nil },
	}
	start := time.Now()
	r := runDetect(deps, false, time.Second)
	if took := time.Since(start); took > 1500*time.Millisecond {
		t.Fatalf("took %v", took)
	}
	if !r.TimedOut || r.Network.DefaultInterface != "eth0" || r.IPv6.Addresses == nil {
		t.Errorf("report = %+v", r)
	}
}

func TestDetectJSONShape(t *testing.T) {
	deps, _ := fakeDetect(upnp.RouterProbe{Answers: true, Name: "R", Model: "M", ExternalIP: "203.0.113.7"}, nil)
	var b strings.Builder
	writeDetect(&b, runDetect(deps, true, time.Second))
	var got map[string]any
	if err := json.Unmarshal([]byte(b.String()), &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema", "elapsedMs", "timedOut", "network", "router", "outside", "publicIP", "publicIPSource", "sharedAddress", "ipv6"} {
		if _, ok := got[key]; !ok {
			t.Errorf("key %q missing", key)
		}
	}
	for _, key := range []string{"answers", "name", "model", "externalIP", "notPublic"} {
		if _, ok := got["router"].(map[string]any)[key]; !ok {
			t.Errorf("router.%s missing", key)
		}
	}
	for _, key := range []string{"defaultInterface", "gateway", "address", "lan"} {
		if _, ok := got["network"].(map[string]any)[key]; !ok {
			t.Errorf("network.%s missing", key)
		}
	}
	for _, key := range []string{"global", "defaultRoute", "addresses"} {
		if _, ok := got["ipv6"].(map[string]any)[key]; !ok {
			t.Errorf("ipv6.%s missing", key)
		}
	}
	// the shape is documented in the file's header
	source, err := os.ReadFile("fork_detect.go")
	if err != nil {
		t.Fatal(err)
	}
	for key := range got {
		if !strings.Contains(string(source), `"`+key+`":`) {
			t.Errorf("key %q is not documented in fork_detect.go", key)
		}
	}
}

func TestDetectArguments(t *testing.T) {
	if no, timeout, err := parseDetectArgs(nil); err != nil || no || timeout != 10*time.Second {
		t.Errorf("defaults: %v %v %v", no, timeout, err)
	}
	if no, timeout, err := parseDetectArgs([]string{"--no-outside", "--timeout", "3"}); err != nil || !no || timeout != 3*time.Second {
		t.Errorf("%v %v %v", no, timeout, err)
	}
	for _, bad := range [][]string{{"--timeout"}, {"--timeout", "0"}, {"--timeout", "11"}, {"--timeout", "x"}, {"--outside"}} {
		if _, _, err := parseDetectArgs(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestParseRoutes(t *testing.T) {
	routes := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"eth0\t0001A8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n" +
		"eth0\t00000000\t0101A8C0\t0003\t0\t0\t0\t00000000\t0\t0\t0\n"
	n := parseDefaultRoute(strings.NewReader(routes))
	if n.DefaultInterface != "eth0" || n.Gateway != "192.168.1.1" {
		t.Errorf("%+v", n)
	}
	v6 := "00000000000000000000000000000000 00 00000000000000000000000000000000 00 fe800000000000000000000000000001 00000400 00000001 00000000 00000003 eth0\n"
	if !hasIPv6DefaultRoute(strings.NewReader(v6)) {
		t.Error("IPv6 default route not found")
	}
	lo := "00000000000000000000000000000000 00 00000000000000000000000000000000 00 00000000000000000000000000000000 ffffffff 00000001 00000000 00200200 lo\n"
	if hasIPv6DefaultRoute(strings.NewReader(lo)) {
		t.Error("the loopback rejection route counted as a default route")
	}
}

// The command itself, as the scripts run it: only JSON on standard output,
// no configuration folder created, no database opened, in time.
func TestDetectCommandWritesOnlyJSONAndCreatesNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the server")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "cosmos")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	config := filepath.Join(dir, "config")
	work := filepath.Join(dir, "work")
	os.Mkdir(work, 0o755)
	cmd := exec.Command(bin, "fork-detect", "--no-outside", "--timeout", "5")
	cmd.Dir = work
	// RCLONE_CONFIG as the scripts set it: rclone, compiled in, otherwise makes
	// an empty ~/.config/rclone when the binary starts, before main runs.
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + work, "COSMOS_CONFIG_FOLDER=" + config + "/", "RCLONE_CONFIG=" + filepath.Join(dir, "none.conf")}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	start := time.Now()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v: %s", err, stderr.String())
	}
	if took := time.Since(start); took > 6*time.Second {
		t.Errorf("took %v", took)
	}
	var report detectReport
	if err := json.Unmarshal(out, &report); err != nil || report.Schema != 1 {
		t.Fatalf("standard output is not the JSON: %v\n%s", err, out)
	}
	if report.Outside.Asked {
		t.Error("--no-outside asked outside")
	}
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Error("the configuration folder was created")
	}
	if entries, _ := os.ReadDir(work); len(entries) != 0 {
		t.Errorf("files were written in the working folder: %v", entries)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Errorf("files were written next to the binary: %v", entries)
	}

	bad := exec.Command(bin, "fork-detect", "--nonsense")
	bad.Dir = work
	if out, err := bad.Output(); err == nil || len(out) != 0 {
		t.Errorf("a bad argument: %v, stdout %q", err, out)
	}
}

package main

// Fork addition (plan 47, C0): `cosmos fork-detect`, what the install
// scripts cannot find out from the shell, printed as one JSON object.
//
//	cosmos fork-detect [--no-outside] [--timeout <seconds, 1-10>]
//
// It changes nothing: no configuration is read or created, no database is
// opened, nothing is written anywhere, the router is only read (discovery,
// its description, GetExternalIPAddress). It runs before Cosmos is
// installed, from the unpacked and verified release. It ends within the
// timeout (default 10 s) whatever the network does: what is not known by
// then is left empty and "timedOut" is true. Standard output carries the
// JSON and nothing else (anything else the code prints goes to standard
// error); the exit status is 0 whenever the JSON was printed, 2 for a bad
// argument.
//
// One thing happens before main and so before this command: rclone, which
// is compiled into the binary, makes an empty ~/.config/rclone when it
// starts unless RCLONE_CONFIG is set. The scripts run the command with
// RCLONE_CONFIG set to a file that does not exist (it is never read here).
//
// The JSON (schema 1):
//
//	{
//	  "schema": 1,
//	  "elapsedMs": 3012,
//	  "timedOut": false,
//	  "network": {
//	    "defaultInterface": "eth0",       // interface of the IPv4 default route, "" when none
//	    "gateway": "192.168.1.1",         // the default gateway (the router UPnP talks to)
//	    "address": "192.168.1.10",        // this machine's IPv4 address on that interface
//	    "lan": "192.168.1.0/24"           // the home network: that address's network
//	  },
//	  "router": {                         // the default gateway over UPnP
//	    "answers": true,                  // it answered with a port-forwarding service
//	    "name": "…", "model": "…",        // from its description, cleaned, at most 64 characters
//	    "externalIP": "203.0.113.7",      // its own outside address ("" when it did not say)
//	    "notPublic": false,               // that address is private or in 100.64.0.0/10
//	    "error": "…"                      // why it did not answer (omitted on success)
//	  },
//	  "outside": {                        // the address the internet sees, asked of an outside service
//	    "asked": false,                   // only when the router gave no address and --no-outside is absent (decision Q4)
//	    "why": "the router answered",     // why it was or was not asked
//	    "address": "", "error": ""        // the answer, or why there is none
//	  },
//	  "publicIP": "203.0.113.7",          // the best outside IPv4 known: the router's, else the outside service's
//	  "publicIPSource": "router",         // "router", "outside" or ""
//	  "sharedAddress": false,             // true: the router's own address is not public (a carrier's shared
//	                                      // address or a second router); null: cannot be told (no router answer)
//	  "ipv6": {
//	    "global": true,                   // a global IPv6 address is present (fc00::/7 and fe80::/10 do not count)
//	    "defaultRoute": true,             // an IPv6 default route exists
//	    "addresses": ["2001:db8::10"]     // the global addresses, at most 8
//	  }
//	}

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/azukaar/cosmos-server/src/dnsrecords"
	"github.com/azukaar/cosmos-server/src/upnp"
)

const (
	detectDefaultTimeout = 10 * time.Second
	detectMaxIPv6        = 8
)

type detectNetwork struct {
	DefaultInterface string `json:"defaultInterface"`
	Gateway          string `json:"gateway"`
	Address          string `json:"address"`
	LAN              string `json:"lan"`
}

type detectOutside struct {
	Asked   bool   `json:"asked"`
	Why     string `json:"why"`
	Address string `json:"address"`
	Error   string `json:"error"`
}

type detectIPv6 struct {
	Global       bool     `json:"global"`
	DefaultRoute bool     `json:"defaultRoute"`
	Addresses    []string `json:"addresses"`
}

type detectReport struct {
	Schema         int              `json:"schema"`
	ElapsedMs      int64            `json:"elapsedMs"`
	TimedOut       bool             `json:"timedOut"`
	Network        detectNetwork    `json:"network"`
	Router         upnp.RouterProbe `json:"router"`
	Outside        detectOutside    `json:"outside"`
	PublicIP       string           `json:"publicIP"`
	PublicIPSource string           `json:"publicIPSource"`
	SharedAddress  *bool            `json:"sharedAddress"`
	IPv6           detectIPv6       `json:"ipv6"`
}

// detectDeps are the parts that touch the network; the tests replace them.
type detectDeps struct {
	network func() detectNetwork
	ipv6    func() detectIPv6
	router  func() upnp.RouterProbe
	outside func() (string, error)
}

var realDetectDeps = detectDeps{
	network: localNetwork,
	ipv6:    localIPv6,
	router:  upnp.Probe,
	outside: dnsrecords.DetectPublicIP,
}

// forkDetectCommand runs `cosmos fork-detect` when that is the command, and
// reports whether it was. Called first thing in main, before anything that
// could touch Docker or the configuration.
func forkDetectCommand() bool {
	if len(os.Args) < 2 || os.Args[1] != "fork-detect" {
		return false
	}
	noOutside, timeout, err := parseDetectArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "fork-detect:", err)
		fmt.Fprintln(os.Stderr, "usage: cosmos fork-detect [--no-outside] [--timeout <seconds, 1-10>]")
		os.Exit(2)
	}
	out := os.Stdout
	// Whatever else is printed while detecting goes to standard error: the
	// scripts read standard output as JSON.
	if saved, err := syscall.Dup(1); err == nil {
		if syscall.Dup3(2, 1, 0) == nil {
			out = os.NewFile(uintptr(saved), "json")
			os.Stdout = os.Stderr
		}
	}
	report := runDetect(realDetectDeps, noOutside, timeout)
	writeDetect(out, report)
	os.Exit(0)
	return true
}

func parseDetectArgs(args []string) (noOutside bool, timeout time.Duration, err error) {
	timeout = detectDefaultTimeout
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--no-outside":
			noOutside = true
		case "--timeout":
			if i+1 >= len(args) {
				return false, 0, fmt.Errorf("--timeout needs a number of seconds")
			}
			i++
			n, convErr := strconv.Atoi(args[i])
			if convErr != nil || n < 1 || n > 10 {
				return false, 0, fmt.Errorf("--timeout takes 1 to 10 seconds")
			}
			timeout = time.Duration(n) * time.Second
		default:
			return false, 0, fmt.Errorf("unknown argument %q", args[i])
		}
	}
	return noOutside, timeout, nil
}

func writeDetect(w io.Writer, report detectReport) {
	data, _ := json.MarshalIndent(report, "", "  ")
	w.Write(append(data, '\n'))
}

// runDetect fills a report within timeout. The parts run in the background;
// at the deadline what is known is returned.
func runDetect(deps detectDeps, noOutside bool, timeout time.Duration) detectReport {
	start := time.Now()
	var mu sync.Mutex
	report := detectReport{Schema: 1, IPv6: detectIPv6{Addresses: []string{}}}
	report.Outside.Why = "not asked yet"
	if noOutside {
		report.Outside.Why = "forbidden by --no-outside"
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			n := deps.network()
			v6 := deps.ipv6()
			if v6.Addresses == nil {
				v6.Addresses = []string{}
			}
			mu.Lock()
			report.Network, report.IPv6 = n, v6
			mu.Unlock()
		}()
		go func() {
			defer wg.Done()
			r := deps.router()
			mu.Lock()
			report.Router = r
			if r.Answers && r.ExternalIP != "" {
				report.PublicIP, report.PublicIPSource = r.ExternalIP, "router"
				shared := r.NotPublic
				report.SharedAddress = &shared
				report.Outside.Why = "the router gave its address"
			}
			ask := !noOutside && report.PublicIP == ""
			if ask {
				report.Outside.Asked = true
				report.Outside.Why = "the router did not give its address"
			}
			mu.Unlock()
			if !ask {
				return
			}
			address, err := deps.outside()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				report.Outside.Error = cleanDetectText(err.Error())
				return
			}
			if upnp.IsPublicIPv4(address) {
				report.Outside.Address = address
				report.PublicIP, report.PublicIPSource = address, "outside"
			} else {
				report.Outside.Error = "the outside service answered an address that is not public"
			}
		}()
		wg.Wait()
	}()

	timer := time.NewTimer(timeout - 100*time.Millisecond)
	defer timer.Stop()
	timedOut := false
	select {
	case <-done:
	case <-timer.C:
		timedOut = true
	}

	mu.Lock()
	defer mu.Unlock()
	result := report
	result.IPv6.Addresses = append([]string{}, report.IPv6.Addresses...)
	if report.SharedAddress != nil {
		shared := *report.SharedAddress
		result.SharedAddress = &shared
	}
	result.TimedOut = timedOut
	result.ElapsedMs = time.Since(start).Milliseconds()
	return result
}

// cleanDetectText keeps an error text printable and short.
func cleanDetectText(text string) string {
	var b strings.Builder
	for _, r := range text {
		if b.Len() >= 200 {
			break
		}
		if r < 0x20 || r == 0x7f {
			r = ' '
		}
		b.WriteRune(r)
	}
	return b.String()
}

// localNetwork reads the IPv4 default route and the address on its interface.
func localNetwork() detectNetwork {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return detectNetwork{}
	}
	defer f.Close()
	n := parseDefaultRoute(f)
	if n.DefaultInterface == "" {
		return n
	}
	iface, err := net.InterfaceByName(n.DefaultInterface)
	if err != nil {
		return n
	}
	addrs, _ := iface.Addrs()
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil && !ipnet.IP.IsLoopback() {
			n.Address = ipnet.IP.To4().String()
			n.LAN = (&net.IPNet{IP: ipnet.IP.Mask(ipnet.Mask), Mask: ipnet.Mask}).String()
			break
		}
	}
	return n
}

// parseDefaultRoute finds the IPv4 default route in /proc/net/route.
func parseDefaultRoute(r io.Reader) detectNetwork {
	scanner := bufio.NewScanner(r)
	first := true
	for scanner.Scan() {
		if first {
			first = false
			continue
		}
		f := strings.Fields(scanner.Text())
		if len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(f[3], 16, 32)
		if err != nil || flags&0x1 == 0 { // RTF_UP
			continue
		}
		n := detectNetwork{DefaultInterface: f[0]}
		if raw, err := strconv.ParseUint(f[2], 16, 32); err == nil && flags&0x2 != 0 {
			n.Gateway = net.IPv4(byte(raw), byte(raw>>8), byte(raw>>16), byte(raw>>24)).String()
		}
		return n
	}
	return detectNetwork{}
}

// localIPv6 reports the global IPv6 addresses and whether a default route exists.
func localIPv6() detectIPv6 {
	v6 := detectIPv6{Addresses: []string{}}
	if f, err := os.Open("/proc/net/ipv6_route"); err == nil {
		v6.DefaultRoute = hasIPv6DefaultRoute(f)
		f.Close()
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return v6
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || virtualInterface(iface.Name) {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || !globalIPv6(ipnet.IP) {
				continue
			}
			v6.Global = true
			if len(v6.Addresses) < detectMaxIPv6 {
				v6.Addresses = append(v6.Addresses, ipnet.IP.String())
			}
		}
	}
	return v6
}

func globalIPv6(ip net.IP) bool {
	return ip.To4() == nil && ip.To16() != nil && ip.IsGlobalUnicast() && !ip.IsPrivate()
}

// virtualInterface: Docker's and other bridges' interfaces are not the
// machine's own network.
func virtualInterface(name string) bool {
	for _, prefix := range []string{"docker", "br-", "veth", "virbr", "cni", "flannel", "nebula"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// hasIPv6DefaultRoute reads /proc/net/ipv6_route: destination ::/0, not on
// the loopback interface, not a rejection.
func hasIPv6DefaultRoute(r io.Reader) bool {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		f := strings.Fields(scanner.Text())
		if len(f) < 10 || f[0] != strings.Repeat("0", 32) || f[1] != "00" || f[9] == "lo" {
			continue
		}
		flags, err := strconv.ParseUint(f[8], 16, 32)
		if err != nil || flags&0x1 == 0 || flags&0x200 != 0 { // RTF_UP, not RTF_REJECT
			continue
		}
		return true
	}
	return false
}

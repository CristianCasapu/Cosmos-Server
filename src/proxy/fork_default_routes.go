package proxy

// Fork addition (plan 49, second review): which interfaces carry a default
// route, for "is this tunnel the server's way out" (fork_visitor_place.go).
// Besides the IPv4 0.0.0.0/0, OpenVPN's redirect-gateway def1 sets the two
// halves 0.0.0.0/1 and 128.0.0.0/1 on the tunnel, and IPv6 has its own table
// (::/0, or ::/1 with 8000::/1). Both files are read bounded: 1 MiB, 4096 lines.

import (
	"bufio"
	"io"
	"os"
	"strings"
)

func scanLines(table io.Reader, each func(fields []string)) {
	if table == nil {
		return
	}
	scanner := bufio.NewScanner(io.LimitReader(table, 1<<20))
	for lines := 0; scanner.Scan() && lines < 4096; lines++ {
		each(strings.Fields(scanner.Text()))
	}
}

// parseDefaultRoutes lists the interfaces that carry a default route, from
// /proc/net/route (v4) and /proc/net/ipv6_route (v6).
func parseDefaultRoutes(v4, v6 io.Reader) map[string]bool {
	out := map[string]bool{}
	halves := map[string]int{} // interface -> which /1 halves it holds (bit 1: low, bit 2: high)
	scanLines(v4, func(f []string) {
		// Iface Destination Gateway Flags RefCnt Use Metric Mask ...; addresses in little-endian hex
		if len(f) < 8 || f[0] == "Iface" {
			return
		}
		switch {
		case f[1] == "00000000" && f[7] == "00000000":
			out[f[0]] = true
		case f[1] == "00000000" && f[7] == "00000080":
			halves[f[0]] |= 1
		case f[1] == "00000080" && f[7] == "00000080":
			halves[f[0]] |= 2
		}
	})
	halves6 := map[string]int{}
	scanLines(v6, func(f []string) {
		// destination prefix-length source source-length next-hop metric refcount use flags iface
		if len(f) < 10 || len(f[0]) != 32 {
			return
		}
		iface := f[9]
		zero := strings.Repeat("0", 32)
		switch {
		case f[0] == zero && f[1] == "00":
			out[iface] = true
		case f[0] == zero && f[1] == "01":
			halves6[iface] |= 1
		case f[0] == "8"+strings.Repeat("0", 31) && f[1] == "01":
			halves6[iface] |= 2
		}
	})
	for iface, h := range halves {
		if h == 3 {
			out[iface] = true
		}
	}
	for iface, h := range halves6 {
		if h == 3 {
			out[iface] = true
		}
	}
	delete(out, "lo")
	return out
}

// defaultRouteInterfaces reads both tables of this machine.
func defaultRouteInterfaces() map[string]bool {
	var v4, v6 io.Reader
	if file, err := os.Open("/proc/net/route"); err == nil {
		defer file.Close()
		v4 = file
	}
	if file, err := os.Open("/proc/net/ipv6_route"); err == nil {
		defer file.Close()
		v6 = file
	}
	return parseDefaultRoutes(v4, v6)
}

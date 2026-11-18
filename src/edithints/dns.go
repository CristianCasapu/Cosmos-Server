package edithints

import (
	"bufio"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

const maxResolvers = 8

// ParseResolvConf reads the name servers of a resolv.conf, at most 8.
func ParseResolvConf(r io.Reader) []string {
	out := []string{}
	scanner := bufio.NewScanner(io.LimitReader(r, 64<<10))
	for lines := 0; scanner.Scan() && lines < 1024; lines++ {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		ip := net.ParseIP(strings.SplitN(fields[1], "%", 2)[0])
		if ip == nil {
			continue
		}
		out = append(out, ip.String())
		if len(out) >= maxResolvers {
			break
		}
	}
	return out
}

// Resolvers are the DNS servers Docker gives a container that names none:
// those of the server's resolv.conf, or, when it only names loopback
// addresses (systemd-resolved's 127.0.0.53), those of systemd-resolved's
// upstream list. Source: "resolv.conf", "systemd-resolved" or "" (none
// usable: Docker then falls back to public servers).
func Resolvers(etcResolv, resolvedResolv string) (servers []string, source string) {
	read := func(path string) []string {
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()
		return ParseResolvConf(file)
	}
	usable := func(list []string) []string {
		out := []string{}
		for _, s := range list {
			if ip := net.ParseIP(s); ip != nil && !ip.IsLoopback() {
				out = append(out, s)
			}
		}
		return out
	}
	if list := usable(read(etcResolv)); len(list) > 0 {
		return list, "resolv.conf"
	}
	if list := usable(read(resolvedResolv)); len(list) > 0 {
		return list, "systemd-resolved"
	}
	return []string{}, ""
}

// ParseGateway reads the gateway of the lowest-metric IPv4 default route of
// /proc/net/route ("" when there is none).
func ParseGateway(table io.Reader) string {
	best, bestMetric := "", -1
	scanner := bufio.NewScanner(io.LimitReader(table, 1<<20))
	for lines := 0; scanner.Scan() && lines < 4096; lines++ {
		// Iface Destination Gateway Flags RefCnt Use Metric Mask ...
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 || fields[1] != "00000000" || fields[7] != "00000000" {
			continue
		}
		metric, err := strconv.Atoi(fields[6])
		if err != nil || metric < 0 {
			continue
		}
		raw, err := strconv.ParseUint(fields[2], 16, 32)
		if err != nil || raw == 0 || len(fields[2]) != 8 {
			continue
		}
		// the kernel writes the address in the host's byte order: little-endian on the servers Cosmos runs on
		ip := net.IPv4(byte(raw), byte(raw>>8), byte(raw>>16), byte(raw>>24))
		if bestMetric == -1 || metric < bestMetric {
			best, bestMetric = ip.String(), metric
		}
	}
	return best
}

// Gateway reads the default gateway from a route table file.
func Gateway(routeFile string) string {
	file, err := os.Open(routeFile)
	if err != nil {
		return ""
	}
	defer file.Close()
	return ParseGateway(file)
}

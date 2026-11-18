package edithints

import (
	"bufio"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Taken says whether a host port of a protocol is in use.
type Taken func(port int, protocol string) bool

const scanWidth = 50

// ProposeHostPort is the host port proposed for a container port: the same
// number when it is free, else the next free one after it (for a port under
// 1024, from 8000 + the port: 80 → 8080), 0 when none of the scanned ones is.
// At most 2+scanWidth ports are tried.
func ProposeHostPort(wanted int, protocol string, taken Taken) int {
	if wanted < 1 || wanted > 65535 {
		return 0
	}
	if !taken(wanted, protocol) {
		return wanted
	}
	start := wanted + 1
	if wanted < 1024 {
		start = 8000 + wanted
	}
	for port := start; port < start+scanWidth && port <= 65535; port++ {
		if !taken(port, protocol) {
			return port
		}
	}
	return 0
}

// Listening reads what holds a port on the server from the kernel's socket
// tables (proc/net/tcp, tcp6: sockets in LISTEN; udp, udp6: every bound
// socket), without binding anything. procNet is the folder of the tables
// (/proc/net normally). Each table is read up to 8 MB. What the tables do not
// show: a port forwarded by a firewall rule alone (Docker without its proxy,
// nftables/iptables DNAT of another program) and ports of other network
// namespaces; Docker's published ports and Cosmos' own are added by the caller.
func Listening(procNet string) map[string]bool {
	held := map[string]bool{}
	read := func(file, protocol string, listenOnly bool) {
		f, err := os.Open(procNet + "/" + file)
		if err != nil {
			return
		}
		defer f.Close()
		scanner := bufio.NewScanner(io.LimitReader(f, 8<<20))
		for lines := 0; scanner.Scan() && lines < 200000; lines++ {
			// sl local_address rem_address st ...
			fields := strings.Fields(scanner.Text())
			if len(fields) < 4 || !strings.Contains(fields[1], ":") {
				continue
			}
			if listenOnly && fields[3] != "0A" {
				continue
			}
			hexPort := fields[1][strings.LastIndex(fields[1], ":")+1:]
			port, err := strconv.ParseUint(hexPort, 16, 16)
			if err != nil || port == 0 {
				continue
			}
			held[strconv.Itoa(int(port))+"/"+protocol] = true
		}
	}
	read("tcp", "tcp", true)
	read("tcp6", "tcp", true)
	read("udp", "udp", false)
	read("udp6", "udp", false)
	return held
}

var (
	listeningMu    sync.Mutex
	listeningAt    time.Time
	listeningCache map[string]bool
)

// ListeningCached is Listening of /proc/net, read at most every 5 seconds.
func ListeningCached() map[string]bool {
	listeningMu.Lock()
	defer listeningMu.Unlock()
	if listeningCache == nil || time.Since(listeningAt) > 5*time.Second {
		listeningCache, listeningAt = Listening("/proc/net"), time.Now()
	}
	return listeningCache
}

var sizePart = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(kb|mb|gb|tb|k|m|g|t)\b`)

// MemoryFromHint reads a recipe's memory hint ("256 MB to 1 GB", "under
// 64 MB", "512 MB and more") as a limit in bytes: the upper bound of a
// range, the bound of "under", twice the floor of "and more", the size
// itself otherwise; 0 when it names no size.
func MemoryFromHint(hint string) int64 {
	matches := sizePart.FindAllStringSubmatch(hint, 4)
	if len(matches) == 0 {
		return 0
	}
	sizes := []int64{}
	for _, m := range matches {
		value, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		unit := map[string]float64{"k": 1 << 10, "m": 1 << 20, "g": 1 << 30, "t": 1 << 40}[strings.ToLower(m[2][:1])]
		size := value * unit
		if size <= 0 || size > math.MaxInt64/4 {
			continue
		}
		sizes = append(sizes, int64(size))
	}
	if len(sizes) == 0 {
		return 0
	}
	lower := strings.ToLower(hint)
	largest := sizes[0]
	for _, s := range sizes {
		if s > largest {
			largest = s
		}
	}
	if len(sizes) == 1 && strings.Contains(lower, "and more") {
		return 2 * sizes[0]
	}
	return largest
}

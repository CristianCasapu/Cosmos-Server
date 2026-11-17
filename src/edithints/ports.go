package edithints

import (
	"math"
	"net"
	"regexp"
	"strconv"
	"strings"
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

// ListenTaken is the test the server makes: a port that cannot be listened
// on (every address) is taken.
func ListenTaken(port int, protocol string) bool {
	address := ":" + strconv.Itoa(port)
	if protocol == "udp" {
		conn, err := net.ListenPacket("udp", address)
		if err != nil {
			return true
		}
		conn.Close()
		return false
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return true
	}
	listener.Close()
	return false
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

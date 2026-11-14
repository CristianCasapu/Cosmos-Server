package fail2ban

import (
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The parsers of what fail2ban-client prints (version 1.1.0, Debian 13).
// They take what they expect and drop everything else: a line that does not
// have the expected shape is skipped, an address that does not parse is
// skipped, a number that is not a plain bounded integer is not a number.
// Nothing of the output is ever passed on as text, so an unexpected answer
// can make the panel show less, never something else.

const (
	// maxBans bounds the bans read of one jail, maxJails the jail names read.
	maxBans  = 2000
	maxJails = 200
	// maxCount bounds a counter or a time read from the output.
	maxCount = 1 << 40
)

// counts are the counters of one jail.
type counts struct {
	CurrentlyFailed int
	TotalFailed     int
	CurrentlyBanned int
	TotalBanned     int
}

// banTime is one ban of a jail.
type banTime struct {
	Address netip.Addr
	Since   time.Time
	// Until is zero for a ban that does not end by itself.
	Until time.Time
}

var jailNameShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// parseAddress reads an address the way every part of this package does:
// no zone, IPv4-mapped addresses as IPv4.
func parseAddress(text string) (netip.Addr, bool) {
	if len(text) > 64 {
		return netip.Addr{}, false
	}
	address, err := netip.ParseAddr(text)
	if err != nil || address.Zone() != "" {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}

// parsePrefix reads a network ("203.0.113.0/24") or a single address, and
// returns it as a network with the host bits cleared.
func parsePrefix(text string) (netip.Prefix, bool) {
	if len(text) > 64 {
		return netip.Prefix{}, false
	}
	if strings.Contains(text, "/") {
		prefix, err := netip.ParsePrefix(text)
		if err != nil || prefix.Addr().Zone() != "" {
			return netip.Prefix{}, false
		}
		// an IPv4-mapped network is not something anybody means
		if prefix.Addr().Is4In6() {
			return netip.Prefix{}, false
		}
		return prefix.Masked(), true
	}
	address, ok := parseAddress(text)
	if !ok {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(address, address.BitLen()), true
}

// formatPrefix prints a network for a list: a single address without its
// "/32" or "/128".
func formatPrefix(prefix netip.Prefix) string {
	if prefix.IsSingleIP() {
		return prefix.Addr().String()
	}
	return prefix.String()
}

// parsePong reads the answer to "ping".
func parsePong(out string) bool {
	return strings.TrimSpace(out) == "Server replied: pong"
}

// parseNumber reads an answer that is one non-negative integer.
func parseNumber(out string) (int, bool) {
	text := strings.TrimSpace(out)
	if text == "" || len(text) > 13 {
		return 0, false
	}
	for _, c := range text {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	number, err := strconv.ParseInt(text, 10, 64)
	if err != nil || number > maxCount {
		return 0, false
	}
	return int(number), true
}

// after returns what follows label on the line that contains it.
func after(out string, label string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		if at := strings.Index(line, label); at >= 0 {
			return strings.TrimSpace(line[at+len(label):]), true
		}
	}
	return "", false
}

// parseJailList reads the jail names of "status":
//
//	`- Jail list:	cosmos-auth, recidive, sshd
func parseJailList(out string) []string {
	list, found := after(out, "Jail list:")
	if !found {
		return nil
	}
	names := []string{}
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if jailNameShape.MatchString(name) {
			names = append(names, name)
			if len(names) == maxJails {
				break
			}
		}
	}
	return names
}

// parseJailStatus reads the counters of "status <jail>". All four must be
// there.
func parseJailStatus(out string) (counts, error) {
	var read counts
	for _, field := range []struct {
		label string
		value *int
	}{
		{"Currently failed:", &read.CurrentlyFailed},
		{"Total failed:", &read.TotalFailed},
		{"Currently banned:", &read.CurrentlyBanned},
		{"Total banned:", &read.TotalBanned},
	} {
		text, found := after(out, field.label)
		if !found {
			return counts{}, errClient
		}
		number, ok := parseNumber(text)
		if !ok {
			return counts{}, errClient
		}
		*field.value = number
	}
	return read, nil
}

// One line of "get <jail> banip --with-time":
//
//	203.0.113.7 	2026-11-13 14:02:54 + 14400 = 2026-11-13 18:02:54
//
// The times are the server's local time without a zone. Only the start and
// the length are used; a length that is not positive is a ban without end.
var banLine = regexp.MustCompile(`^([0-9A-Fa-f.:]{2,45})\s+(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) \+ (-?\d{1,12}) = \S+ \S+$`)

func parseBanTimes(out string, zone *time.Location) []banTime {
	bans := []banTime{}
	for _, line := range strings.Split(out, "\n") {
		match := banLine.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		address, ok := parseAddress(match[1])
		if !ok {
			continue
		}
		since, err := time.ParseInLocation("2006-01-02 15:04:05", match[2], zone)
		if err != nil {
			continue
		}
		seconds, err := strconv.ParseInt(match[3], 10, 64)
		if err != nil {
			continue
		}
		ban := banTime{Address: address, Since: since}
		if seconds > 0 && seconds <= maxCount {
			ban.Until = since.Add(time.Duration(seconds) * time.Second)
		}
		bans = append(bans, ban)
		if len(bans) == maxBans {
			break
		}
	}
	return bans
}

// parseIgnoreList reads "get <jail> ignoreip":
//
//	These IP addresses/networks are ignored:
//	|- 127.0.0.0/8
//	`- ::1
//
// Entries that are not an address or a network are counted, not returned.
func parseIgnoreList(out string) (networks []netip.Prefix, others int) {
	networks = []netip.Prefix{}
	for _, line := range strings.Split(out, "\n") {
		entry, isEntry := strings.CutPrefix(line, "|- ")
		if !isEntry {
			if entry, isEntry = strings.CutPrefix(line, "`- "); !isEntry {
				continue
			}
		}
		prefix, ok := parsePrefix(strings.TrimSpace(entry))
		if !ok {
			others++
			continue
		}
		networks = append(networks, prefix)
		if len(networks) == 4*bounds.MaxIgnoreIP {
			break
		}
	}
	return networks, others
}

// The answer to "banned" is a Python list of one dictionary per jail:
//
//	[{'sshd': []}, {'cosmos-auth': ['203.0.113.7', '2001:db8::7']}]
var (
	bannedJail  = regexp.MustCompile(`\{'([^'{}\[\]]{1,64})': \[([^\[\]{}]*)\]\}`)
	bannedEntry = regexp.MustCompile(`'([0-9A-Fa-f.:]{2,45})'`)
)

// parseBanned reads the answer to "banned" and keeps the fork's jails.
func parseBanned(out string) map[string][]netip.Addr {
	banned := map[string][]netip.Addr{}
	for _, match := range bannedJail.FindAllStringSubmatch(out, maxJails) {
		known, ok := jailByName(match[1])
		if !ok {
			continue
		}
		addresses := []netip.Addr{}
		for _, entry := range bannedEntry.FindAllStringSubmatch(match[2], maxBans) {
			if address, ok := parseAddress(entry[1]); ok {
				addresses = append(addresses, address)
			}
		}
		banned[known.name] = addresses
	}
	return banned
}

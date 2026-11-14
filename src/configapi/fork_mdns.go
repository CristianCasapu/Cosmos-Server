package configapi

// Fork addition: the interfaces the .local names are published on
// (HTTPConfig.MDNSInterfaces, see proxy/fork_mdns.go). Empty means chosen
// automatically; otherwise a few interface names as the kernel spells them.

import (
	"regexp"
	"strconv"
)

const maxMDNSInterfaces = 8

var mdnsInterfaceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@-]{0,14}$`)

// validateMDNSInterfaces returns what is wrong with the list, "" if nothing.
func validateMDNSInterfaces(names []string) string {
	if len(names) > maxMDNSInterfaces {
		return "At most " + strconv.Itoa(maxMDNSInterfaces) + " interfaces can be named for the .local names"
	}
	seen := map[string]bool{}
	for _, name := range names {
		if !mdnsInterfaceName.MatchString(name) {
			return "Not an interface name: use names such as eno1 or eth0 (letters, digits, . _ - : @, at most 15 characters)"
		}
		if seen[name] {
			return "An interface is named twice for the .local names"
		}
		seen[name] = true
	}
	return ""
}

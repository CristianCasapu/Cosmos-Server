//go:build anyimage_testnet

package anyimage

// Only in a build made with -tags anyimage_testnet (the browser check's
// throwaway server, scripts/fork-ui-check-any-image.mjs): the stand-in
// registries share the test server's own network, which the guard refuses
// as on-link. Production builds (build.sh, the release workflow) never set
// this tag, so this file and the variable it reads are not in them.

import (
	"net/netip"
	"os"
	"strings"
)

func init() {
	for _, text := range strings.Split(os.Getenv("COSMOS_ANYIMAGE_TEST_ALLOW"), ",") {
		if prefix, err := netip.ParsePrefix(strings.TrimSpace(text)); err == nil {
			testAllowedPrefixes = append(testAllowedPrefixes, prefix)
		}
	}
}

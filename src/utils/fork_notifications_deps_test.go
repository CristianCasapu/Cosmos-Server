package utils

import (
	"strings"
	"testing"
)

// Fork addition: the links of the dependency update notices stay readable,
// and what looks like a secret around them is still hidden.
func TestGitHubLinksAreNotTakenForSecrets(t *testing.T) {
	secret := "A1b2C3d4E5f6G7h8I9j0" + "K1l2M3n4O5p6Q7r8"
	for in, want := range map[string]string{
		"Look at the run on GitHub: https://github.com/SomeoneLonger/Cosmos-Server-Fork/actions/runs/1234567890": "Look at the run on GitHub: https://github.com/SomeoneLonger/Cosmos-Server-Fork/actions/runs/1234567890",
		"https://github.com/SomeoneLonger/Cosmos-Server-Fork/issues/42 and " + secret:                            "https://github.com/SomeoneLonger/Cosmos-Server-Fork/issues/42 and [hidden]",
		"https://github.com/SomeoneLonger/Cosmos-Server-Fork/actions":                                            "https://github.com/SomeoneLonger/Cosmos-Server-Fork/actions",
	} {
		if got := MaskNotificationText(in); got != want {
			t.Errorf("%q:\n got %q\nwant %q", in, got, want)
		}
	}
	// only the exact shape is kept: a secret after it, or another host, is looked at as before
	for _, in := range []string{
		"https://github.com/SomeoneLonger/Cosmos-Server-Fork/actions/workflows/" + secret,
		"https://example.org/SomeoneLonger/Cosmos-Server-Fork/issues/42",
	} {
		if got := MaskNotificationText(in); !strings.Contains(got, "[hidden]") {
			t.Errorf("%q is shown as it is", in)
		}
	}
}

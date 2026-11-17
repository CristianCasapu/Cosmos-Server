package forkrelease

// Fork addition (plan 47, H1): the install scripts carry the release keys.
// install.sh in the repository holds placeholders that the release build
// (scripts/fork-release-package.sh) fills from keys.go with a grep; these
// tests make sure that grep finds exactly the keys this binary trusts, that
// nothing else in the script can pass for a key, and that the scripts'
// signed file can never be taken for a release's signature file.

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
)

const installScript = "../../fork/install/install.sh"

// keysByGrep is what the build's grep (the same in action.yml, the release
// workflow and fork-release-package.sh) finds in keys.go.
func keysByGrep(t *testing.T) []string {
	t.Helper()
	source, err := os.ReadFile("keys.go")
	if err != nil {
		t.Fatal(err)
	}
	line := regexp.MustCompile(`(?m)^[[:space:]]*"[A-Za-z0-9+/]{43}=",`)
	key := regexp.MustCompile(`[A-Za-z0-9+/]{43}=`)
	var keys []string
	for _, l := range line.FindAllString(string(source), -1) {
		keys = append(keys, key.FindString(l))
	}
	return keys
}

func TestTheStampedKeysAreTheCompiledKeys(t *testing.T) {
	grepped := keysByGrep(t)
	compiled := compiledKeys()
	if len(grepped) != len(compiled) {
		t.Fatalf("the build's grep finds %d keys in keys.go, the binary trusts %d: install.sh and the binary would differ", len(grepped), len(compiled))
	}
	for i, k := range grepped {
		decoded, err := decodeKeys([]string{k})
		if err != nil || !decoded[0].Equal(compiled[i]) {
			t.Errorf("key %d found by the grep is not the binary's key %d", i+1, i+1)
		}
	}
}

func TestInstallScriptHoldsOnlyPlaceholders(t *testing.T) {
	data, err := os.ReadFile(installScript)
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, line := range []string{
		"RELEASE_KEYS='@FORK_RELEASE_KEYS@'",
		"RELEASE_VERSION='@FORK_RELEASE_VERSION@'",
		"RELEASE_REPOSITORY='@FORK_RELEASE_REPOSITORY@'",
	} {
		if n := strings.Count(script, "\n"+line+"\n"); n != 1 {
			t.Errorf("install.sh holds the placeholder line %q %d times, want once", line, n)
		}
	}
	// the stamp's sed replaces only those lines: no other occurrence that a
	// stamp could turn into a key, and the test-mode check is split
	if n := strings.Count(script, "@FORK_RELEASE_KEYS@"); n != 1 {
		t.Errorf("@FORK_RELEASE_KEYS@ appears %d times", n)
	}
	// nothing in the script looks like a release key (outside Docker's
	// armoured key, whose lines are 64 characters long)
	quoted := regexp.MustCompile(`['" =][A-Za-z0-9+/]{43}=['" \n]`)
	if m := quoted.FindAllString(script, -1); len(m) != 0 {
		t.Errorf("install.sh holds something that looks like a release key: %q", m)
	}
	// a stamped script ignores the test variables: they are read only in
	// the branch where the keys are the placeholder
	test := regexp.MustCompile(`COSMOS_INSTALL_TEST_[A-Z]+`)
	block := script[strings.Index(script, "TEST_MODE=0"):strings.Index(script, "INSTALL_DIR=/opt/cosmos")]
	if len(test.FindAllString(script, -1)) != len(test.FindAllString(block, -1)) {
		t.Error("a COSMOS_INSTALL_TEST_ variable is read outside the unstamped branch")
	}
	if !strings.Contains(block, `if [ "$RELEASE_KEYS" = "@FORK_""RELEASE_KEYS@" ]; then`) {
		t.Error("the test mode is not tied to the unstamped placeholder")
	}
	for _, forbidden := range []string{"eval ", "--insecure", "curl -k", "| sh", "| bash", "\nsource ", "\tsource ", "; source "} {
		if strings.Contains(script, forbidden) {
			t.Errorf("install.sh contains %q", forbidden)
		}
	}
}

// migrate.sh (plan 47, D1) must refuse an installed fork exactly as
// install.sh does: the detection is one block, the same in both.
func TestScriptsShareTheDetection(t *testing.T) {
	block := func(path string) string {
		data, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		s := string(data)
		begin := strings.Index(s, "# BEGIN shared detection")
		end := strings.Index(s, "# END shared detection")
		if begin < 0 || end < begin {
			t.Fatalf("%s has no shared detection block", path)
		}
		return s[begin:end]
	}
	install := block(installScript)
	if install == "" {
		t.Fatal("install.sh is missing")
	}
	if migrate := block("../../fork/install/migrate.sh"); migrate != "" && migrate != install {
		t.Error("migrate.sh's detection differs from install.sh's")
	}
}

// The scripts' signed file (cosmos-cloud-<v>.scripts.sig) is signed with
// the same keys but can never be taken for a release's signature file: the
// servers' parser refuses its header and its file names.
func TestScriptsSignatureIsNotAReleaseSignature(t *testing.T) {
	public, private := testKey(t)
	body := "cosmos-fork-scripts-v1\nversion 0.24.0-fork.40\n" +
		"sha256 " + strings.Repeat("a", 64) + " install.sh\n" +
		"sha256 " + strings.Repeat("b", 64) + " uninstall.sh\n"
	file := body + "signature " + base64.StdEncoding.EncodeToString(ed25519.Sign(private, []byte(body))) + "\n"
	if _, err := verifyWith([]ed25519.PublicKey{public}, []byte(file)); !errors.Is(err, ErrMalformed) {
		t.Fatalf("the scripts' signature file was not refused as a release signature: %v", err)
	}
	// and with the release header it is still refused for its names
	renamed := strings.Replace(body, "cosmos-fork-scripts-v1", manifestHeader, 1)
	file = renamed + "signature " + base64.StdEncoding.EncodeToString(ed25519.Sign(private, []byte(renamed))) + "\n"
	if _, err := verifyWith([]ed25519.PublicKey{public}, []byte(file)); !errors.Is(err, ErrMalformed) {
		t.Fatalf("scripts listed in a release signature were accepted: %v", err)
	}
}

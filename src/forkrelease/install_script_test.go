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
	"os/exec"
	"path/filepath"
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
		"RELEASE_BASE='@FORK_RELEASE_BASE@'",
		"SCRIPT_ROLE=install",
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

// migrate.sh (plan 47, D1) is install.sh with its role: the release build
// writes it as the same stamped bytes with the one line SCRIPT_ROLE changed,
// so every check the two share (the refusal of an installed fork first) is
// the same code.
func TestMigrateIsInstallWithItsRole(t *testing.T) {
	out := t.TempDir()
	cmd := exec.Command("bash", packageScript, "--scripts-only", "--version", "0.24.0-fork.77",
		"--repository", "example-owner/cosmos-fork", "--base", "0.24.0", "--keys", "keys.go", "--out", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s --scripts-only: %v\n%s", packageScript, err, b)
	}
	read := func(name string) []string {
		data, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(string(data), "\n")
	}
	install, migrate := read("install.sh"), read("migrate.sh")
	if len(install) != len(migrate) {
		t.Fatalf("install.sh has %d lines, migrate.sh %d", len(install), len(migrate))
	}
	differ := 0
	for i := range install {
		if install[i] == migrate[i] {
			continue
		}
		differ++
		if install[i] != "SCRIPT_ROLE=install" || migrate[i] != "SCRIPT_ROLE=migrate" {
			t.Errorf("line %d differs beyond the role: %q / %q", i+1, install[i], migrate[i])
		}
	}
	if differ != 1 {
		t.Errorf("%d lines differ, want exactly the role line", differ)
	}
	for _, want := range []string{"RELEASE_VERSION='0.24.0-fork.77'", "RELEASE_BASE='0.24.0'", "RELEASE_REPOSITORY='example-owner/cosmos-fork'"} {
		found := false
		for _, l := range migrate {
			found = found || l == want
		}
		if !found {
			t.Errorf("the stamped migrate.sh lacks %q", want)
		}
	}
	// a base that is not x.y.z stops the packaging
	bad := exec.Command("bash", packageScript, "--scripts-only", "--version", "0.24.0-fork.77",
		"--repository", "example-owner/cosmos-fork", "--base", "0.24", "--keys", "keys.go", "--out", t.TempDir())
	if b, err := bad.CombinedOutput(); err == nil {
		t.Errorf("a base of 0.24 was packaged:\n%s", b)
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

// The shell's verifier (install.sh, verify_signed) and the servers' (Go,
// verifyWith) are fed the same signature files: they must agree on each.
func TestShellAndGoVerifiersAgree(t *testing.T) {
	for _, tool := range []string{"bash", "openssl", "base64"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not here")
		}
	}
	data, err := os.ReadFile(installScript)
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	begin, end := strings.Index(script, "# BEGIN verify_signed"), strings.Index(script, "# END verify_signed")
	if begin < 0 || end < begin {
		t.Fatal("install.sh has no verify_signed block")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "verify.sh"), []byte(script[begin:end]), 0o600)
	driver := `set -u
. "$1/verify.sh"
WORK=$(mktemp -d "$1/work.XXXXXX"); OSSL=(openssl); RELEASE_KEYS=$2
if verify_signed "$3" "$4" "$WORK/out"; then echo ACCEPT; else echo REFUSE; fi`
	os.WriteFile(filepath.Join(dir, "driver.sh"), []byte(driver), 0o600)

	public, private := testKey(t)
	_, stranger := testKey(t)
	key := base64.StdEncoding.EncodeToString(public)
	sign := func(signed string, keys ...ed25519.PrivateKey) string {
		out := signed
		for _, k := range keys {
			out += "signature " + base64.StdEncoding.EncodeToString(ed25519.Sign(k, []byte(signed))) + "\n"
		}
		return out
	}
	v := "0.24.0-fork.40"
	sum := strings.Repeat("ab", 32)
	zip := "cosmos-cloud-" + v + "-amd64.zip"
	arm := "cosmos-cloud-" + v + "-arm64.zip"
	head := manifestHeader + "\nversion " + v + "\n"
	good := head + "sha256 " + sum + " " + zip + "\n"
	sig := ed25519.Sign(private, []byte(good))
	nonCanonical := base64.StdEncoding.EncodeToString(sig)
	// the last character holds 2 bits of the signature and 4 bits that must
	// be zero: setting one of those gives the same bytes, not canonical
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	last := strings.IndexByte(alphabet, nonCanonical[len(nonCanonical)-3])
	nonCanonical = nonCanonical[:len(nonCanonical)-3] + string(alphabet[last|1]) + "=="
	if decoded, err := base64.StdEncoding.DecodeString(nonCanonical); err != nil || !ed25519.Verify(public, []byte(good), decoded) {
		t.Fatal("the non-canonical signature is not the same signature")
	}
	cases := []struct {
		name   string
		file   string
		accept bool
	}{
		{"one zip, one signature", sign(good, private), true},
		{"both zips", sign(good+"sha256 "+sum+" "+arm+"\n", private), true},
		{"a stranger's signature, then a good one", sign(good, stranger, private), true},
		{"a stranger's signature only", sign(good, stranger), false},
		{"no signature", good, false},
		{"five signatures", sign(good, private, private, private, private, private), false},
		{"the header of the scripts", sign(strings.Replace(good, manifestHeader, "cosmos-fork-scripts-v1", 1), private), false},
		{"a version with a leading zero", sign(strings.Replace(good, "version 0.24.0-fork.40", "version 0.024.0-fork.40", 1), private), false},
		{"the version line of another release", sign(strings.Replace(good, "version "+v, "version 0.24.0-fork.41", 1), private), false},
		{"a name that is not a zip of the version", sign(head+"sha256 "+sum+" install.sh\n", private), false},
		{"a name listed twice", sign(good+"sha256 "+sum+" "+zip+"\n", private), false},
		{"CRLF", strings.ReplaceAll(sign(good, private), "\n", "\r\n"), false},
		{"a line after the signatures", sign(good, private) + "trailing data\n", false},
		{"no final newline", strings.TrimSuffix(sign(good, private), "\n"), false},
		{"a control character", sign(strings.Replace(good, "version", "ver\x01sion", 1), private), false},
		{"larger than 4 KiB", sign(good, private) + strings.Repeat("signature "+base64.StdEncoding.EncodeToString(sig)+"\n", 60), false},
		{"a signature in non-canonical base64", good + "signature " + nonCanonical + "\n", false},
		{"an upper-case checksum", sign(head+"sha256 "+strings.ToUpper(sum)+" "+zip+"\n", private), false},
	}
	for _, c := range cases {
		path := filepath.Join(dir, "file.sig")
		os.WriteFile(path, []byte(c.file), 0o600)
		_, goErr := verifyWith([]ed25519.PublicKey{public}, []byte(c.file))
		out, err := exec.Command("bash", filepath.Join(dir, "driver.sh"), dir, key, path, manifestHeader).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: the shell verifier did not run: %v %s", c.name, err, out)
		}
		shell := strings.TrimSpace(string(out)) == "ACCEPT"
		if (goErr == nil) != c.accept {
			t.Errorf("%s: Go accepts %v, want %v (%v)", c.name, goErr == nil, c.accept, goErr)
		}
		if shell != (goErr == nil) {
			t.Errorf("%s: the shell accepts %v, Go %v", c.name, shell, goErr == nil)
		}
	}

	// the scripts' file: its own header and names, the same rules
	scripts := "cosmos-fork-scripts-v1\nversion " + v + "\nsha256 " + sum + " install.sh\nsha256 " + sum + " uninstall.sh\n"
	for name, c := range map[string]struct {
		file   string
		accept bool
	}{
		"the scripts, signed":        {sign(scripts, private), true},
		"the scripts, a stranger's":  {sign(scripts, stranger), false},
		"a zip in the scripts' file": {sign(scripts+"sha256 "+sum+" "+zip+"\n", private), false},
		"a script listed twice":      {sign(scripts+"sha256 "+sum+" install.sh\n", private), false},
	} {
		path := filepath.Join(dir, "scripts.sig")
		os.WriteFile(path, []byte(c.file), 0o600)
		out, _ := exec.Command("bash", filepath.Join(dir, "driver.sh"), dir, key, path, "cosmos-fork-scripts-v1").CombinedOutput()
		if got := strings.TrimSpace(string(out)) == "ACCEPT"; got != c.accept {
			t.Errorf("%s: the shell accepts %v, want %v (%s)", name, got, c.accept, out)
		}
	}
}

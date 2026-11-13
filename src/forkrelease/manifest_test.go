package forkrelease

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"sort"
	"strings"
	"testing"
)

// Fork addition: tests of the signature file. Every key here is made by the
// test; the real release key is never in the repository.

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return public, private
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// manifestText is the signed part of a signature file.
func manifestText(version string, files map[string]string) string {
	names := []string{}
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	text := manifestHeader + "\nversion " + version + "\n"
	for _, name := range names {
		text += "sha256 " + files[name] + " " + name + "\n"
	}
	return text
}

// signedFile is a signature file as the release workflow makes it.
func signedFile(version string, files map[string]string, signers ...ed25519.PrivateKey) []byte {
	text := manifestText(version, files)
	out := text
	for _, signer := range signers {
		out += "signature " + base64.StdEncoding.EncodeToString(ed25519.Sign(signer, []byte(text))) + "\n"
	}
	return []byte(out)
}

const testVersion = "0.24.0-fork.10"

func testFiles(amd string, arm string) map[string]string {
	return map[string]string{
		ZipName(testVersion, "amd64"): sha256Hex([]byte(amd)),
		ZipName(testVersion, "arm64"): sha256Hex([]byte(arm)),
	}
}

func TestVerifySignatureFile(t *testing.T) {
	current, currentPrivate := testKey(t)
	next, nextPrivate := testKey(t)
	stranger, strangerPrivate := testKey(t)
	files := testFiles("amd zip", "arm zip")
	good := signedFile(testVersion, files, currentPrivate)

	otherRelease := signedFile("0.24.0-fork.9", map[string]string{ZipName("0.24.0-fork.9", "amd64"): sha256Hex([]byte("old"))}, currentPrivate)
	signatureOf := func(file []byte) string {
		lines := strings.Split(strings.TrimSuffix(string(file), "\n"), "\n")
		return lines[len(lines)-1] + "\n"
	}

	cases := []struct {
		name string
		keys []ed25519.PublicKey
		file []byte
		want error
	}{
		{"signed with the current key", []ed25519.PublicKey{current}, good, nil},
		{"signed with the current key, two keys trusted", []ed25519.PublicKey{current, next}, good, nil},
		{"signed with the second key", []ed25519.PublicKey{current, next}, signedFile(testVersion, files, nextPrivate), nil},
		{"signed with both keys", []ed25519.PublicKey{next}, signedFile(testVersion, files, currentPrivate, nextPrivate), nil},
		{"one signature of a stranger, one of ours", []ed25519.PublicKey{current}, signedFile(testVersion, files, strangerPrivate, currentPrivate), nil},

		{"signed with a key that is not trusted", []ed25519.PublicKey{current, next}, signedFile(testVersion, files, strangerPrivate), ErrBadSignature},
		{"the trusted key is another one", []ed25519.PublicKey{stranger}, good, ErrBadSignature},
		{"the signature of another release's file", []ed25519.PublicKey{current}, []byte(manifestText(testVersion, files) + signatureOf(otherRelease)), ErrBadSignature},
		{"a checksum changed after signing", []ed25519.PublicKey{current}, []byte(strings.Replace(string(good), files[ZipName(testVersion, "amd64")], sha256Hex([]byte("evil")), 1)), ErrBadSignature},
		{"the version changed after signing", []ed25519.PublicKey{current}, []byte(strings.ReplaceAll(string(good), "fork.10", "fork.11")), ErrBadSignature},
		{"a file line removed after signing", []ed25519.PublicKey{current}, []byte(strings.Replace(string(good), "sha256 "+files[ZipName(testVersion, "arm64")]+" "+ZipName(testVersion, "arm64")+"\n", "", 1)), ErrBadSignature},
		{"signature of all zeroes", []ed25519.PublicKey{current}, []byte(manifestText(testVersion, files) + "signature " + base64.StdEncoding.EncodeToString(make([]byte, 64)) + "\n"), ErrBadSignature},

		{"no key in this build (placeholder)", nil, good, ErrNoReleaseKey},
		{"no key in this build, unsigned file", []ed25519.PublicKey{}, []byte(manifestText(testVersion, files)), ErrNoReleaseKey},

		{"no signature line", []ed25519.PublicKey{current}, []byte(manifestText(testVersion, files)), ErrMalformed},
		{"empty", []ed25519.PublicKey{current}, nil, ErrMalformed},
		{"a web page instead", []ed25519.PublicKey{current}, []byte("<html>Not Found</html>\n"), ErrMalformed},
		{"a zip instead", []ed25519.PublicKey{current}, []byte("PK\x03\x04\x00\x00"), ErrMalformed},
		{"no final line end", []ed25519.PublicKey{current}, good[:len(good)-1], ErrMalformed},
		{"cut in the middle", []ed25519.PublicKey{current}, good[:len(good)/2], ErrMalformed},
		{"Windows line ends", []ed25519.PublicKey{current}, []byte(strings.ReplaceAll(string(good), "\n", "\r\n")), ErrMalformed},
		{"another first line", []ed25519.PublicKey{current}, []byte(strings.Replace(string(good), manifestHeader, "cosmos-fork-release-v2", 1)), ErrMalformed},
		{"a line after the signature", []ed25519.PublicKey{current}, append(append([]byte{}, good...), []byte("sha256 "+sha256Hex([]byte("x"))+" "+ZipName(testVersion, "amd64")+"\n")...), ErrMalformed},
		{"an empty line", []ed25519.PublicKey{current}, append(append([]byte{}, good...), '\n'), ErrMalformed},
		{"larger than the limit", []ed25519.PublicKey{current}, append(append([]byte{}, good...), []byte(strings.Repeat("signature AAAA\n", 400))...), ErrMalformed},
		{"five signatures", []ed25519.PublicKey{current}, signedFile(testVersion, files, currentPrivate, currentPrivate, currentPrivate, currentPrivate, currentPrivate), ErrMalformed},
		{"a signature that is not base64", []ed25519.PublicKey{current}, []byte(manifestText(testVersion, files) + "signature !!!!\n"), ErrMalformed},
		{"a signature of the wrong length", []ed25519.PublicKey{current}, []byte(manifestText(testVersion, files) + "signature " + base64.StdEncoding.EncodeToString(make([]byte, 63)) + "\n"), ErrMalformed},
		{"a file of another version", []ed25519.PublicKey{current}, signedFile(testVersion, map[string]string{ZipName("0.24.0-fork.9", "amd64"): sha256Hex([]byte("x"))}, currentPrivate), ErrMalformed},
		{"a file name with a path", []ed25519.PublicKey{current}, signedFile(testVersion, map[string]string{"../" + ZipName(testVersion, "amd64"): sha256Hex([]byte("x"))}, currentPrivate), ErrMalformed},
		{"a checksum in capitals", []ed25519.PublicKey{current}, signedFile(testVersion, map[string]string{ZipName(testVersion, "amd64"): strings.ToUpper(sha256Hex([]byte("x")))}, currentPrivate), ErrMalformed},
		{"an MD5 instead of a SHA-256", []ed25519.PublicKey{current}, signedFile(testVersion, map[string]string{ZipName(testVersion, "amd64"): "d41d8cd98f00b204e9800998ecf8427e"}, currentPrivate), ErrMalformed},
		{"no file", []ed25519.PublicKey{current}, signedFile(testVersion, map[string]string{}, currentPrivate), ErrMalformed},
		{"the same file twice", []ed25519.PublicKey{current}, []byte(manifestHeader + "\nversion " + testVersion + "\n" + strings.Repeat("sha256 "+sha256Hex([]byte("x"))+" "+ZipName(testVersion, "amd64")+"\n", 2) + "signature " + base64.StdEncoding.EncodeToString(make([]byte, 64)) + "\n"), ErrMalformed},
		{"an upstream version", []ed25519.PublicKey{current}, signedFile("0.24.0", map[string]string{"cosmos-cloud-0.24.0-amd64.zip": sha256Hex([]byte("x"))}, currentPrivate), ErrMalformed},
		{"a version with a leading v", []ed25519.PublicKey{current}, signedFile("v"+testVersion, map[string]string{ZipName("v"+testVersion, "amd64"): sha256Hex([]byte("x"))}, currentPrivate), ErrMalformed},
	}
	for _, c := range cases {
		manifest, err := verifyWith(c.keys, c.file)
		if c.want == nil {
			if err != nil {
				t.Errorf("%s: refused: %v", c.name, err)
			} else if manifest.Version != testVersion || len(manifest.Files) != 2 || manifest.Files[ZipName(testVersion, "arm64")] != files[ZipName(testVersion, "arm64")] {
				t.Errorf("%s: read as %+v", c.name, manifest)
			}
			continue
		}
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
		if err == nil || manifest.Version != "" || manifest.Files != nil {
			t.Errorf("%s: a refused file still returned %+v", c.name, manifest)
		}
	}
}

func TestChecksumForVersionAndArchitecture(t *testing.T) {
	public, private := testKey(t)
	files := testFiles("amd zip", "arm zip")
	manifest, err := verifyWith([]ed25519.PublicKey{public}, signedFile(testVersion, files, private))
	if err != nil {
		t.Fatal(err)
	}
	if sum, err := manifest.checksumFor("v"+testVersion, "arm64"); err != nil || sum != sha256Hex([]byte("arm zip")) {
		t.Errorf("arm64: %q %v", sum, err)
	}
	if _, err := manifest.checksumFor("0.24.0-fork.11", "amd64"); !errors.Is(err, ErrWrongVersion) {
		t.Errorf("another version: %v", err)
	}
	if _, err := manifest.checksumFor(testVersion, "riscv64"); !errors.Is(err, ErrNoSuchFile) {
		t.Errorf("another architecture: %v", err)
	}
	onlyAMD, _ := verifyWith([]ed25519.PublicKey{public}, signedFile(testVersion, map[string]string{ZipName(testVersion, "amd64"): files[ZipName(testVersion, "amd64")]}, private))
	if _, err := onlyAMD.checksumFor(testVersion, "arm64"); !errors.Is(err, ErrNoSuchFile) {
		t.Errorf("a file the release does not list: %v", err)
	}
}

func TestBuildNumbers(t *testing.T) {
	for version, want := range map[string]int{"0.24.0-fork.10": 10, "v0.24.0-fork.9": 9, "1.2.3-fork.123456789": 123456789} {
		if build, ok := Build(version); !ok || build != want {
			t.Errorf("%s: %d %v", version, build, ok)
		}
	}
	for _, version := range []string{"", "0.24.0", "0.24.0-fork.", "0.24.0-fork.0", "0.24.0-fork.01", "0.24.0-fork.1234567890", "0.24.0-fork.-1",
		"0.24.0-fork.1 ", "0.24.0-fork.1\n", "0.24-fork.1", "0.24.0-fork.1-fork.2", "vv0.24.0-fork.1", "0.23.4003", "../0.24.0-fork.1", "0.24.0-fork.1/.."} {
		if build, ok := Build(version); ok {
			t.Errorf("%q is read as build %d", version, build)
		}
	}
	if SignatureName(testVersion) != "cosmos-cloud-0.24.0-fork.10.sig" {
		t.Error(SignatureName(testVersion))
	}
	// the updater of the releases before signing takes any asset with the
	// architecture in its name as the zip: the signature file must not be one
	for _, arch := range []string{"amd64", "arm64"} {
		if strings.Contains(strings.ToLower(SignatureName(testVersion)), arch) {
			t.Errorf("the signature file's name contains %s", arch)
		}
	}
}

func TestReleaseKeySlots(t *testing.T) {
	a, _ := testKey(t)
	b, _ := testKey(t)
	encode := func(key ed25519.PublicKey) string { return base64.StdEncoding.EncodeToString(key) }

	if keys, err := decodeKeys([]string{noReleaseKey, noReleaseKey}); err != nil || len(keys) != 0 {
		t.Errorf("two empty slots: %d keys, %v", len(keys), err)
	}
	if keys, err := decodeKeys([]string{encode(a), noReleaseKey}); err != nil || len(keys) != 1 || !keys[0].Equal(a) {
		t.Errorf("one key: %v %v", keys, err)
	}
	if keys, err := decodeKeys([]string{noReleaseKey, encode(b)}); err != nil || len(keys) != 1 || !keys[0].Equal(b) {
		t.Errorf("only the second slot: %v %v", keys, err)
	}
	if keys, err := decodeKeys([]string{encode(a), encode(b)}); err != nil || len(keys) != 2 {
		t.Errorf("two keys: %v %v", keys, err)
	}
	for name, slots := range map[string][]string{
		"a mistyped key":       {encode(a)[:43] + "!", noReleaseKey},
		"a shortened key":      {encode(a)[:40], noReleaseKey},
		"an empty slot":        {"", encode(b)},
		"a private key's size": {base64.StdEncoding.EncodeToString(make([]byte, 64)), noReleaseKey},
		"the same key twice":   {encode(a), encode(a)},
		"a PEM line":           {"-----BEGIN PUBLIC KEY-----", noReleaseKey},
	} {
		if keys, err := decodeKeys(slots); err == nil {
			t.Errorf("%s is accepted as %d key(s)", name, len(keys))
		}
	}
}

// The slots of this very build must be readable: a key pasted wrongly fails
// the checks instead of becoming a build that trusts no release. With
// FORK_REQUIRE_RELEASE_KEY=1 (the release workflow) a build without any key
// fails too.
func TestReleaseKeyIsCompiledIn(t *testing.T) {
	keys, err := decodeKeys(releaseKeys[:])
	if err != nil {
		t.Fatalf("src/forkrelease/keys.go: %v", err)
	}
	if len(keys) != KeyCount() {
		t.Fatalf("KeyCount says %d, the slots hold %d", KeyCount(), len(keys))
	}
	if os.Getenv("FORK_REQUIRE_RELEASE_KEY") == "1" && len(keys) == 0 {
		t.Fatal("src/forkrelease/keys.go holds no release key: a release built from it could never be updated. Run scripts/fork-signing-key.sh and paste the key it prints.")
	}
	if len(keys) == 0 {
		// the placeholder: nothing verifies
		_, private := testKey(t)
		if _, err := Verify(signedFile(testVersion, testFiles("a", "b"), private)); !errors.Is(err, ErrNoReleaseKey) {
			t.Fatalf("a build without a release key verified something: %v", err)
		}
	}
}

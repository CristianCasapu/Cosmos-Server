package forkrelease

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fork addition: the server's staging of a release against a stand-in for
// GitHub, and the launcher's decision in a folder made up like an install.

const testRepo = "someone/Cosmos-Server"

func testZip(t *testing.T, marker string) []byte {
	t.Helper()
	buffer := &bytes.Buffer{}
	writer := zip.NewWriter(buffer)
	for name, content := range map[string]string{"cosmos-cloud/cosmos": "binary " + marker, "cosmos-cloud/meta.json": `{"version":"x"}`} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		entry.Write([]byte(content))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// release is what the stand-in serves under /<repo>/releases/download/<tag>/.
type release map[string][]byte

func serveReleases(t *testing.T, releases map[string]release) (fetcher, *[]string) {
	t.Helper()
	asked := &[]string{}
	f, _ := fakeGitHub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*asked = append(*asked, r.Host+r.URL.Path)
		prefix := "/" + testRepo + "/releases/download/"
		rest, ok := strings.CutPrefix(r.URL.Path, prefix)
		parts := strings.SplitN(rest, "/", 2)
		if r.Host != "github.com" || !ok || len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		content, found := releases[parts[0]][parts[1]]
		if !found {
			http.NotFound(w, r)
			return
		}
		w.Write(content)
	}))
	return f, asked
}

func installFolder(t *testing.T, version string) string {
	t.Helper()
	folder := t.TempDir()
	if version != "" {
		if err := os.WriteFile(filepath.Join(folder, "meta.json"), []byte(`{"version": "`+version+`", "upstream": "0.24.0"}`), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return folder
}

func staged(folder string) (zipContent []byte, signature []byte, leftovers []string) {
	zipContent, _ = os.ReadFile(filepath.Join(folder, StagedZip))
	signature, _ = os.ReadFile(filepath.Join(folder, StagedSignature))
	entries, _ := os.ReadDir(folder)
	for _, entry := range entries {
		switch entry.Name() {
		case StagedZip, StagedSignature, "meta.json", pendingLauncher:
		default:
			leftovers = append(leftovers, entry.Name())
		}
	}
	return
}

func TestStageARelease(t *testing.T) {
	public, private := testKey(t)
	second, secondPrivate := testKey(t)
	_, strangerPrivate := testKey(t)
	amd, arm := testZip(t, "amd64"), testZip(t, "arm64")
	files := map[string]string{ZipName(testVersion, "amd64"): sha256Hex(amd), ZipName(testVersion, "arm64"): sha256Hex(arm)}
	tag := "v" + testVersion
	good := release{
		SignatureName(testVersion):    signedFile(testVersion, files, private),
		ZipName(testVersion, "amd64"): amd,
		ZipName(testVersion, "arm64"): arm,
	}
	with := func(change func(release)) release {
		copied := release{}
		for name, content := range good {
			copied[name] = content
		}
		change(copied)
		return copied
	}
	olderFiles := map[string]string{ZipName("0.24.0-fork.9", "amd64"): sha256Hex(amd)}

	cases := []struct {
		name    string
		release release
		keys    []ed25519.PublicKey
		current string
		arch    string
		tag     string
		pending bool
		want    error // nil: staged
		anyErr  bool  // refused for a reason without a named error
	}{
		{name: "a signed release", release: good},
		{name: "a signed release, for arm64", release: good, arch: "arm64"},
		{name: "signed with the second key", release: with(func(r release) { r[SignatureName(testVersion)] = signedFile(testVersion, files, secondPrivate) }), keys: []ed25519.PublicKey{public, second}},
		{name: "from a much older build", release: good, current: "0.23.4-fork.4"},

		{name: "no signature file in the release", release: with(func(r release) { delete(r, SignatureName(testVersion)) }), anyErr: true},
		{name: "signed by somebody else", release: with(func(r release) { r[SignatureName(testVersion)] = signedFile(testVersion, files, strangerPrivate) }), want: ErrBadSignature},
		{name: "this build has no release key", release: good, keys: []ed25519.PublicKey{}, want: ErrNoReleaseKey},
		{name: "the zip was replaced", release: with(func(r release) { r[ZipName(testVersion, "amd64")] = testZip(t, "evil") }), want: ErrWrongChecksum},
		{name: "the zip is cut short", release: with(func(r release) { r[ZipName(testVersion, "amd64")] = amd[:len(amd)-20] }), want: ErrWrongChecksum},
		{name: "the zip is the other architecture's", release: with(func(r release) { r[ZipName(testVersion, "amd64")] = arm }), want: ErrWrongChecksum},
		{name: "the zip is missing", release: with(func(r release) { delete(r, ZipName(testVersion, "amd64")) }), anyErr: true},
		{name: "the signature file of an older release", release: with(func(r release) {
			r[SignatureName(testVersion)] = signedFile("0.24.0-fork.9", olderFiles, private)
		}), want: ErrWrongVersion},
		{name: "the release lists no zip for this architecture", release: with(func(r release) {
			r[SignatureName(testVersion)] = signedFile(testVersion, map[string]string{ZipName(testVersion, "arm64"): sha256Hex(arm)}, private)
		}), want: ErrNoSuchFile},
		{name: "an unsigned text as signature file", release: with(func(r release) { r[SignatureName(testVersion)] = []byte(manifestText(testVersion, files)) }), want: ErrMalformed},

		{name: "the same build as the running one", release: good, current: testVersion, want: ErrNotNewer},
		{name: "an older build than the running one", release: good, current: "0.24.0-fork.11", want: ErrNotNewer},
		{name: "the running version is unknown", release: good, current: "unknown", anyErr: true},
		{name: "the running version is upstream's", release: good, current: "0.24.0", anyErr: true},
		{name: "an upstream tag", release: good, tag: "v0.25.0", want: ErrNotForkRelease},
		{name: "a tag without v", release: good, tag: testVersion, want: ErrNotForkRelease},
		{name: "a tag that is a path", release: good, tag: "v0.24.0-fork.10/../v0.24.0-fork.11", want: ErrNotForkRelease},
		{name: "a machine this fork does not build for", release: good, arch: "riscv64", anyErr: true},
		{name: "the launcher of the last update is not in place", release: good, pending: true, want: ErrLauncherPending},
	}
	for _, c := range cases {
		fetch, asked := serveReleases(t, map[string]release{tag: c.release})
		folder := installFolder(t, "")
		// what an earlier attempt may have left
		os.WriteFile(filepath.Join(folder, StagedZip), []byte("stale"), 0644)
		os.WriteFile(filepath.Join(folder, StagedSignature), []byte("stale"), 0644)
		if c.pending {
			os.WriteFile(filepath.Join(folder, pendingLauncher), []byte("launcher"), 0755)
		}
		stager := Stager{Repo: testRepo, Folder: folder, Arch: "amd64", Current: "0.24.0-fork.9", keys: []ed25519.PublicKey{public}, fetch: fetch}
		if c.keys != nil {
			stager.keys = c.keys
		}
		if c.current != "" {
			stager.Current = c.current
		}
		if c.arch != "" {
			stager.Arch = c.arch
		}
		askedTag := tag
		if c.tag != "" {
			askedTag = c.tag
		}

		err := stager.Stage(askedTag)
		zipContent, signature, leftovers := staged(folder)

		if c.want == nil && !c.anyErr {
			wantZip := c.release[ZipName(testVersion, stager.Arch)]
			if err != nil {
				t.Errorf("%s: refused: %v", c.name, err)
			} else if !bytes.Equal(zipContent, wantZip) || !bytes.Equal(signature, c.release[SignatureName(testVersion)]) {
				t.Errorf("%s: what was placed is not what the release holds", c.name)
			}
			if info, statErr := os.Stat(filepath.Join(folder, StagedZip)); statErr == nil && info.Mode().Perm() != 0600 {
				t.Errorf("%s: the staged zip has mode %v", c.name, info.Mode().Perm())
			}
		} else {
			if err == nil {
				t.Errorf("%s: staged", c.name)
			} else if c.want != nil && !errors.Is(err, c.want) {
				t.Errorf("%s: refused for %v, want %v", c.name, err, c.want)
			}
			early := c.pending || errors.Is(c.want, ErrNotNewer) || errors.Is(c.want, ErrNotForkRelease) || errors.Is(c.want, ErrNoReleaseKey) || isEarlyRefusal(c.name)
			switch {
			case early && (string(zipContent) != "stale" || len(*asked) != 0):
				// refused by its name or the state of the install: nothing is asked for, nothing touched
				t.Errorf("%s: the folder was changed or %d request(s) were sent", c.name, len(*asked))
			case !early && (zipContent != nil || signature != nil):
				t.Errorf("%s: an update file lies in the folder after the refusal (zip %d bytes, signature %d bytes)", c.name, len(zipContent), len(signature))
			}
		}
		if len(leftovers) != 0 {
			t.Errorf("%s: left behind %v", c.name, leftovers)
		}
		for _, address := range *asked {
			if !strings.HasPrefix(address, "github.com/"+testRepo+"/releases/download/"+tag+"/") {
				t.Errorf("%s: asked for %s", c.name, address)
			}
		}
	}
}

// refusals decided before the folder is cleared
func isEarlyRefusal(name string) bool {
	return strings.Contains(name, "running version") || strings.Contains(name, "machine this fork")
}

func TestStageUsesTheCompiledKeysWhenNoneAreGiven(t *testing.T) {
	_, private := testKey(t)
	amd := testZip(t, "amd64")
	files := map[string]string{ZipName(testVersion, "amd64"): sha256Hex(amd)}
	fetch, _ := serveReleases(t, map[string]release{"v" + testVersion: {
		SignatureName(testVersion): signedFile(testVersion, files, private), ZipName(testVersion, "amd64"): amd,
	}})
	folder := installFolder(t, "")
	err := Stager{Repo: testRepo, Folder: folder, Arch: "amd64", Current: "0.24.0-fork.9", fetch: fetch}.Stage("v" + testVersion)
	if err == nil {
		t.Fatal("a release signed with a key made in this test was staged by a build that does not hold it")
	}
	if zipContent, signature, _ := staged(folder); zipContent != nil || signature != nil {
		t.Error("update files were placed")
	}
}

// launcherFolder is an install on version installed with an update staged
// next to it.
func launcherFolder(t *testing.T, installed string, zipContent []byte, signature []byte) string {
	t.Helper()
	folder := installFolder(t, installed)
	if zipContent != nil {
		if err := os.WriteFile(filepath.Join(folder, StagedZip), zipContent, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if signature != nil {
		if err := os.WriteFile(filepath.Join(folder, StagedSignature), signature, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return folder
}

func TestTheLaunchersDecision(t *testing.T) {
	public, private := testKey(t)
	second, secondPrivate := testKey(t)
	_, strangerPrivate := testKey(t)
	amd, arm := testZip(t, "amd64"), testZip(t, "arm64")
	files := map[string]string{ZipName(testVersion, "amd64"): sha256Hex(amd), ZipName(testVersion, "arm64"): sha256Hex(arm)}
	good := signedFile(testVersion, files, private)

	cases := []struct {
		name      string
		installed string
		zip       []byte
		signature []byte
		keys      []ed25519.PublicKey
		arch      string
		accept    bool
		reason    string
	}{
		{name: "a signed update", installed: "0.24.0-fork.9", zip: amd, signature: good, accept: true},
		{name: "a signed update on arm64", installed: "0.24.0-fork.9", zip: arm, signature: good, arch: "arm64", accept: true},
		{name: "signed with the second key", installed: "0.24.0-fork.9", zip: amd, signature: signedFile(testVersion, files, secondPrivate), keys: []ed25519.PublicKey{public, second}, accept: true},
		{name: "the same build again (an unpacking that was interrupted)", installed: testVersion, zip: amd, signature: good, accept: true},

		{name: "no signature file (what the release before signing left)", installed: "0.24.0-fork.9", zip: amd, reason: "no signature file"},
		{name: "an empty signature file", installed: "0.24.0-fork.9", zip: amd, signature: []byte{}, reason: "expected form"},
		{name: "signed by somebody else", installed: "0.24.0-fork.9", zip: amd, signature: signedFile(testVersion, files, strangerPrivate), reason: "not signed with a release key"},
		{name: "a launcher without a release key", installed: "0.24.0-fork.9", zip: amd, signature: good, keys: []ed25519.PublicKey{}, reason: "no release key"},
		{name: "the zip was replaced", installed: "0.24.0-fork.9", zip: testZip(t, "evil"), signature: good, reason: "SHA-256"},
		{name: "the zip is cut short", installed: "0.24.0-fork.9", zip: amd[:len(amd)-1], signature: good, reason: "SHA-256"},
		{name: "the zip is empty", installed: "0.24.0-fork.9", zip: []byte{}, signature: good, reason: "SHA-256"},
		{name: "the other architecture's zip", installed: "0.24.0-fork.9", zip: arm, signature: good, reason: "SHA-256"},
		{name: "an older release than the installed one", installed: "0.24.0-fork.11", zip: amd, signature: good, reason: "not newer"},
		{name: "the installed version cannot be read", installed: "", zip: amd, signature: good, reason: "meta.json"},
		{name: "the installed version is not a fork release", installed: "0.24.0", zip: amd, signature: good, reason: "not a release of this fork"},
		{name: "a machine this fork does not build for", installed: "0.24.0-fork.9", zip: amd, signature: good, arch: "386", reason: "architecture"},
	}
	for _, c := range cases {
		folder := launcherFolder(t, c.installed, c.zip, c.signature)
		keys := []ed25519.PublicKey{public}
		if c.keys != nil {
			keys = c.keys
		}
		arch := "amd64"
		if c.arch != "" {
			arch = c.arch
		}
		output := &bytes.Buffer{}

		accepted := launcherAccepts(keys, folder, arch, output)
		zipContent, signature, _ := staged(folder)
		refusal := TakeLauncherRefusal(folder)

		if c.accept {
			if !accepted {
				t.Errorf("%s: refused: %s", c.name, output.String())
			}
			if !bytes.Equal(zipContent, c.zip) || signature == nil {
				t.Errorf("%s: the accepted update was changed or removed", c.name)
			}
			if refusal != "" {
				t.Errorf("%s: an accepted update left the note %q", c.name, refusal)
			}
			continue
		}
		if accepted {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if zipContent != nil || signature != nil {
			t.Errorf("%s: the refused update is still in the folder", c.name)
		}
		if !strings.Contains(output.String(), "[ERROR] Update refused and NOT installed") || !strings.Contains(output.String(), c.reason) {
			t.Errorf("%s: the launcher printed %q, want the reason %q", c.name, output.String(), c.reason)
		}
		if !strings.Contains(refusal, c.reason) {
			t.Errorf("%s: the note for the server says %q, want %q", c.name, refusal, c.reason)
		}
		if again := TakeLauncherRefusal(folder); again != "" {
			t.Errorf("%s: the note is reported twice", c.name)
		}
	}
}

func TestTheLauncherDoesNotFollowLinks(t *testing.T) {
	public, private := testKey(t)
	amd := testZip(t, "amd64")
	good := signedFile(testVersion, map[string]string{ZipName(testVersion, "amd64"): sha256Hex(amd)}, private)

	elsewhere := t.TempDir()
	os.WriteFile(filepath.Join(elsewhere, "zip"), amd, 0600)
	os.WriteFile(filepath.Join(elsewhere, "sig"), good, 0600)

	folder := launcherFolder(t, "0.24.0-fork.9", nil, good)
	os.Symlink(filepath.Join(elsewhere, "zip"), filepath.Join(folder, StagedZip))
	if launcherAccepts([]ed25519.PublicKey{public}, folder, "amd64", &bytes.Buffer{}) {
		t.Error("an update file that is a link was accepted")
	}

	folder = launcherFolder(t, "0.24.0-fork.9", amd, nil)
	os.Symlink(filepath.Join(elsewhere, "sig"), filepath.Join(folder, StagedSignature))
	if launcherAccepts([]ed25519.PublicKey{public}, folder, "amd64", &bytes.Buffer{}) {
		t.Error("a signature file that is a link was accepted")
	}
	for _, name := range []string{"zip", "sig"} {
		if _, err := os.Stat(filepath.Join(elsewhere, name)); err != nil {
			t.Errorf("the file a link pointed to was removed: %v", err)
		}
	}
}

func TestTheLaunchersNoteIsReadSafely(t *testing.T) {
	folder := t.TempDir()
	if got := TakeLauncherRefusal(folder); got != "" {
		t.Errorf("no note: %q", got)
	}
	os.WriteFile(filepath.Join(folder, rejectedReport), []byte("line one\nline two\x1b[31m \xff"+strings.Repeat("x", 600)), 0600)
	got := TakeLauncherRefusal(folder)
	if strings.ContainsAny(got, "\n\x1b\xff") || len(got) > 300 || !strings.HasPrefix(got, "line one?line two") {
		t.Errorf("the note is passed on as %q", got)
	}
	os.WriteFile(filepath.Join(folder, rejectedReport), []byte(strings.Repeat("x", 5000)), 0600)
	if got := TakeLauncherRefusal(folder); got != "" {
		t.Errorf("an oversized note is passed on (%d bytes)", len(got))
	}
	if _, err := os.Stat(filepath.Join(folder, rejectedReport)); err == nil {
		t.Error("an oversized note is left in place")
	}
}

// The compiled-in decision (no keys given): with the placeholder nothing is
// ever accepted.
func TestLauncherAcceptsNothingWithoutACompiledKey(t *testing.T) {
	if KeyCount() != 0 {
		t.Skip("this build holds a release key; the test keys cannot sign for it")
	}
	_, private := testKey(t)
	amd := testZip(t, "amd64")
	good := signedFile(testVersion, map[string]string{ZipName(testVersion, "amd64"): sha256Hex(amd)}, private)
	folder := launcherFolder(t, "0.24.0-fork.9", amd, good)
	if LauncherAccepts(folder, "amd64") {
		t.Fatal("a launcher without a release key accepted an update")
	}
	if reason := TakeLauncherRefusal(folder); !strings.Contains(reason, "no release key") {
		t.Errorf("reason: %q", reason)
	}
}

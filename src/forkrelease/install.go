package forkrelease

// Fork addition: the two places where a release is checked before it can
// replace the running one.
//
//   - Stage (the server): downloads the signature file and the zip of a
//     release from the fork's own repository, and only when the signature is
//     good, the version is the one asked for, it is newer than the running
//     one and the zip has the signed SHA-256, puts both next to the binary
//     under the names the launcher looks for.
//   - LauncherAccepts (the launcher, at the next start, as root, just before
//     it unpacks): checks the two files again with the keys compiled into the
//     launcher itself. It does not rely on the server having checked.
//
// Whatever fails, nothing is installed and the running version stays.

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

const (
	// The names next to the binaries.
	StagedZip       = "cosmos-update.zip"
	StagedSignature = "cosmos-update.sig"
	stagedPart      = "cosmos-update.zip.part"
	pendingLauncher = "cosmos-launcher.updated"
	rejectedReport  = "cosmos-update.rejected"
	installedMeta   = "meta.json"
)

var (
	ErrNotNewer        = errors.New("the release is not newer than the installed one")
	ErrNotForkRelease  = errors.New("not a release of this fork (expected v<x.y.z>-fork.<n>)")
	ErrLauncherPending = errors.New("the launcher of the last update has not been put in place yet (cosmos-launcher.updated is still there)")
	ErrNoSignature     = errors.New("there is no signature file for the update")
)

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$`)

// Stager stages a release for the launcher.
type Stager struct {
	Repo    string // owner/name on github.com
	Folder  string // where the binaries are
	Arch    string // amd64 or arm64
	Current string // the running version

	keys  []ed25519.PublicKey // nil: the keys compiled into this binary
	fetch fetcher
}

// Stage downloads, verifies and places the release with the given tag.
func (s Stager) Stage(tag string) error {
	version := strings.TrimPrefix(tag, "v")
	build, ok := Build(version)
	if !ok || !strings.HasPrefix(tag, "v") {
		return ErrNotForkRelease
	}
	currentBuild, ok := Build(s.Current)
	if !ok {
		return fmt.Errorf("the running version %q is not a release of this fork, so what is newer cannot be told", SafeText(s.Current))
	}
	if build <= currentBuild {
		return ErrNotNewer
	}
	if !repoPattern.MatchString(s.Repo) || !knownArch(s.Arch) {
		return errors.New("the update source or the architecture of this build is not usable")
	}
	if _, err := os.Lstat(filepath.Join(s.Folder, pendingLauncher)); err == nil {
		return ErrLauncherPending
	}
	if len(s.trustedKeys()) == 0 {
		return ErrNoReleaseKey // nothing could be verified: do not even ask
	}

	zip := filepath.Join(s.Folder, StagedZip)
	part := filepath.Join(s.Folder, stagedPart)
	signaturePath := filepath.Join(s.Folder, StagedSignature)
	for _, leftover := range []string{zip, part, signaturePath} {
		os.Remove(leftover)
	}

	// The addresses are built here; nothing the release list says is followed.
	base := "https://github.com/" + s.Repo + "/releases/download/" + tag + "/"

	signature, err := s.fetch.get(base+SignatureName(version), MaxSignatureFile, ListTimeout)
	if err != nil {
		return fmt.Errorf("the signature file of %s could not be fetched: %w", tag, err)
	}
	manifest, err := verifyWith(s.trustedKeys(), signature)
	if err != nil {
		return err
	}
	want, err := manifest.checksumFor(version, s.Arch)
	if err != nil {
		return err
	}

	got, err := s.fetch.download(part, base+ZipName(version, s.Arch), MaxZip, ZipTimeout)
	if err != nil {
		return fmt.Errorf("the zip of %s could not be fetched: %w", tag, err)
	}
	if got != want {
		os.Remove(part)
		return ErrWrongChecksum
	}

	if err := writeNew(signaturePath, signature); err != nil {
		os.Remove(part)
		return err
	}
	if err := os.Rename(part, zip); err != nil {
		os.Remove(part)
		os.Remove(signaturePath)
		return err
	}
	return nil
}

func (s Stager) trustedKeys() []ed25519.PublicKey {
	if s.keys != nil {
		return s.keys
	}
	return compiledKeys()
}

// writeNew writes a new file, 0600, never through a link.
func writeNew(path string, data []byte) error {
	os.Remove(path)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		os.Remove(path)
		return err
	}
	return file.Close()
}

// readRegular reads a regular file of at most limit bytes, not through a link.
func readRegular(path string, limit int64) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, ErrTooLarge
	}
	return io.ReadAll(io.LimitReader(file, limit))
}

// fileSHA256 is the SHA-256 of a regular file of at most limit bytes.
func fileSHA256(path string, limit int64) (string, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return "", ErrTooLarge
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, limit+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// installedVersion reads the version of the installed release from meta.json.
func installedVersion(folder string) (string, error) {
	data, err := readRegular(filepath.Join(folder, installedMeta), 64<<10)
	if err != nil {
		return "", errors.New("the installed version cannot be read from meta.json")
	}
	var meta struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return "", errors.New("the installed version cannot be read from meta.json")
	}
	return meta.Version, nil
}

// checkStaged is the launcher's decision: nil when the staged zip is a
// release signed with one of the keys, for this architecture, and not older
// than what is installed. The same build is let through: an unpacking that
// was interrupted is done again.
func checkStaged(keys []ed25519.PublicKey, folder string, arch string) (Manifest, error) {
	signature, err := readRegular(filepath.Join(folder, StagedSignature), MaxSignatureFile)
	if err != nil {
		if os.IsNotExist(err) {
			return Manifest{}, ErrNoSignature
		}
		return Manifest{}, fmt.Errorf("%w: %v", ErrNoSignature, pathless(err))
	}
	manifest, err := verifyWith(keys, signature)
	if err != nil {
		return Manifest{}, err
	}

	installed, err := installedVersion(folder)
	if err != nil {
		return Manifest{}, err
	}
	installedBuild, ok := Build(installed)
	if !ok {
		return Manifest{}, fmt.Errorf("the installed version %q is not a release of this fork, so what is newer cannot be told", SafeText(installed))
	}
	if build, _ := Build(manifest.Version); build < installedBuild {
		return Manifest{}, ErrNotNewer
	}

	want, err := manifest.checksumFor(manifest.Version, arch)
	if err != nil {
		return Manifest{}, err
	}
	got, err := fileSHA256(filepath.Join(folder, StagedZip), MaxZip)
	if err != nil {
		return Manifest{}, fmt.Errorf("the update file cannot be read: %v", pathless(err))
	}
	if got != want {
		return Manifest{}, ErrWrongChecksum
	}
	return manifest, nil
}

// LauncherAccepts is the hook of the launcher, called when an update file
// lies next to it and before anything is unpacked. False means the update
// was refused: the files are removed, the reason is printed and left in
// cosmos-update.rejected for the server to report.
func LauncherAccepts(folder string, arch string) bool {
	return launcherAccepts(compiledKeys(), folder, arch, os.Stdout)
}

func launcherAccepts(keys []ed25519.PublicKey, folder string, arch string, out io.Writer) bool {
	manifest, err := checkStaged(keys, folder, arch)
	if err == nil {
		fmt.Fprintln(out, "Update "+manifest.Version+" is signed with a release key of this fork.")
		return true
	}
	reason := SafeText(err.Error())
	fmt.Fprintln(out, "[ERROR] Update refused and NOT installed: "+reason)
	os.Remove(filepath.Join(folder, StagedZip))
	os.Remove(filepath.Join(folder, StagedSignature))
	os.Remove(filepath.Join(folder, stagedPart))
	if err := writeNew(filepath.Join(folder, rejectedReport), []byte(reason+"\n")); err != nil {
		fmt.Fprintln(out, "[ERROR] The refusal could not be noted for the server: "+SafeText(pathless(err).Error()))
	}
	return false
}

// TakeLauncherRefusal returns why the launcher refused an update at this
// start ("" when it did not), and removes the note.
func TakeLauncherRefusal(folder string) string {
	path := filepath.Join(folder, rejectedReport)
	data, err := readRegular(path, 1024)
	if err != nil {
		if !os.IsNotExist(err) {
			os.Remove(path)
		}
		return ""
	}
	os.Remove(path)
	reason := SafeText(strings.TrimSpace(string(data)))
	if reason == "" {
		reason = "no reason was noted"
	}
	return reason
}

// ClearStaged removes what staging leaves next to the binaries besides the
// zip: the signature file and an unfinished download. The server calls it at
// start, when the launcher has done its work and the zip itself is gone.
func ClearStaged(folder string) {
	os.Remove(filepath.Join(folder, StagedSignature))
	os.Remove(filepath.Join(folder, stagedPart))
}

// SafeText keeps a message printable on one line: it ends up in logs and in
// a notification.
func SafeText(text string) string {
	clean := make([]rune, 0, len(text))
	for _, r := range text {
		if r < 0x20 || r == 0x7f || r > 0x7e {
			r = '?'
		}
		clean = append(clean, r)
		if len(clean) >= 300 {
			break
		}
	}
	return string(clean)
}

// pathless drops the path from a file error.
func pathless(err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}

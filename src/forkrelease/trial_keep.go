package forkrelease

// Fork addition: what the launcher keeps before it unzips an update (see
// trial.go), and the plan of what the unzip writes.

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

type keptFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size"`
}

// keptManifest lists the copy kept. PreviousVersion is the version the
// copy is of (the restore point); NewVersion the update last installed
// over it.
type keptManifest struct {
	Format          int        `json:"format"`
	PreviousVersion string     `json:"previousVersion"`
	NewVersion      string     `json:"newVersion"`
	Replaced        []keptFile `json:"replaced"`
	Added           []string   `json:"added"`
	AddedDirs       []string   `json:"addedDirs"`
}

// ---------------------------------------------------------------- the zip

// InstallTargets lists what unzipping the archive into dest writes, relative
// to dest, with exactly the mapping of the launcher's unzip: the zip's root
// folder stripped, start.sh skipped, cosmos-launcher[-arm64] written as
// <name>.updated. files are written, dirs are created. Whatever unzip
// refuses is an error here too.
func InstallTargets(zipPath string, dest string) (files []string, dirs []string, err error) {
	files, dirs, _, err = installPlan(zipPath, dest)
	return files, dirs, err
}

// installPlan is InstallTargets with the uncompressed size of what is written.
func installPlan(zipPath string, dest string) (files []string, dirs []string, size uint64, err error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, nil, 0, err
	}
	defer reader.Close()
	files, dirs, err = zipTargets(reader.File, dest)
	if err != nil {
		return nil, nil, 0, err
	}
	for _, f := range reader.File {
		if !f.FileInfo().IsDir() {
			size += f.UncompressedSize64
		}
	}
	return files, dirs, size, nil
}

func zipTargets(entries []*zip.File, dest string) (files []string, dirs []string, err error) {
	dest, err = filepath.Abs(dest)
	if err != nil {
		return nil, nil, err
	}
	if len(entries) > maxKeptFiles {
		return nil, nil, errors.New("the update holds too many files")
	}
	var rootFolder string
	if len(entries) > 0 {
		rootFolder = strings.Split(entries[0].Name, "/")[0] + "/"
	}
	for _, f := range entries {
		if f.Mode()&os.ModeSymlink != 0 {
			return nil, nil, fmt.Errorf("refusing symlink entry in archive: %s", SafeText(f.Name))
		}
		fpath := filepath.Join(dest, strings.TrimPrefix(f.Name, rootFolder))
		if filepath.IsAbs(f.Name) || !within(dest, fpath) {
			return nil, nil, fmt.Errorf("refusing archive entry outside destination: %s", SafeText(f.Name))
		}
		baseName := filepath.Base(fpath)
		if baseName == "start.sh" {
			continue
		}
		if baseName == "cosmos-launcher" || baseName == "cosmos-launcher-arm64" {
			fpath = filepath.Join(filepath.Dir(fpath), baseName+".updated")
		}
		rel, err := filepath.Rel(dest, fpath)
		if err != nil {
			return nil, nil, err
		}
		if f.FileInfo().IsDir() {
			dirs = append(dirs, rel)
			continue
		}
		if rel == "." {
			return nil, nil, fmt.Errorf("refusing archive entry that is the destination: %s", SafeText(f.Name))
		}
		files = append(files, rel)
	}
	return files, dirs, nil
}

// within is the launcher's insideDir.
func within(dest, fpath string) bool {
	rel, err := filepath.Rel(dest, fpath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// stateEntry: a path the updater keeps its own state in.
func stateEntry(rel string) bool {
	first := strings.Split(rel, string(os.PathSeparator))[0]
	return strings.HasPrefix(first, "cosmos-update.")
}

// localPath: a clean relative path inside the folder.
func localPath(rel string) bool {
	return rel != "" && rel != "." && filepath.IsLocal(rel) && filepath.Clean(rel) == rel && !strings.ContainsRune(rel, '\\')
}

// ------------------------------------------------------------ free space

// noSpaceError is the refusal for want of room, in words.
type noSpaceError struct {
	folder     string
	need, free uint64
}

func (e *noSpaceError) Error() string {
	return fmt.Sprintf("not enough free space in %s: %s needed, %s free", e.folder, mebibytes(e.need), mebibytes(e.free))
}

func mebibytes(n uint64) string {
	return fmt.Sprintf("%d MiB", (n+(1<<20)-1)>>20)
}

// freeSpace is what an unprivileged writer may still use in the folder's
// file system. A variable for the tests.
var freeSpace = func(folder string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(folder, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// spaceMargin is added to what an update needs: 10 %, at least 64 MiB, and
// at least the largest single file, which a restore writes beside itself.
func spaceMargin(total uint64, largest uint64) uint64 {
	margin := total / 10
	if margin < 64<<20 {
		margin = 64 << 20
	}
	if margin < largest {
		margin = largest
	}
	return margin
}

// treeSize is the size of the regular files under path (no link followed).
func treeSize(path string) uint64 {
	var total uint64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			total += uint64(info.Size())
		}
		return nil
	})
	return total
}

// ------------------------------------------------------------ the backup

// backUp keeps what the staged zip overwrites. With an update on trial
// (installed by hand meanwhile), the copy kept stays the restore point: the
// version it is of is still the last proven one, and only the files it does
// not hold yet are added to it.
func backUp(folder string, newVersion string) error {
	if _, ok := Build(newVersion); !ok {
		return errors.New("the version of the update is not known")
	}
	trial, terr := readTrial(folder)
	onTrial := terr == nil
	switch {
	case terr != nil && !errors.Is(terr, os.ErrNotExist):
		return errors.New(SafeText(terr.Error()) + "; nothing is installed until the next start has dealt with it")
	case onTrial && trial.State != stateTrial:
		return errors.New("the last update is still being installed or undone; nothing is installed until that is finished")
	}

	previousVersion := trial.PreviousVersion
	manifest := keptManifest{Format: 1, Replaced: []keptFile{}, Added: []string{}, AddedDirs: []string{}}
	if onTrial {
		kept, err := readKept(folder)
		if err != nil {
			return err
		}
		if kept.PreviousVersion != previousVersion {
			return errors.New("the files kept are not of the version before the update on trial")
		}
		manifest = kept
	} else {
		installed, err := installedVersion(folder)
		if err != nil {
			return err
		}
		if _, ok := Build(installed); !ok {
			return errors.New("the installed version is not a release of this fork")
		}
		previousVersion = installed
	}
	manifest.PreviousVersion, manifest.NewVersion = previousVersion, newVersion

	files, dirs, unpacked, err := installPlan(filepath.Join(folder, StagedZip), folder)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, f := range manifest.Replaced {
		known[f.Path] = true
	}
	for _, rel := range manifest.Added {
		known[rel] = true
	}
	knownDir := map[string]bool{}
	for _, dir := range manifest.AddedDirs {
		knownDir[dir] = true
	}

	// what there is to do, checked before anything is touched
	addedDir := map[string]bool{}
	noteDirs := func(rel string) error {
		parts := strings.Split(rel, string(os.PathSeparator))
		for i := 1; i <= len(parts); i++ {
			dir := filepath.Join(parts[:i]...)
			info, err := os.Lstat(filepath.Join(folder, dir))
			switch {
			case os.IsNotExist(err):
				addedDir[dir] = true
			case err != nil:
				return err
			case !info.IsDir():
				return fmt.Errorf("%s is in the way of the update: it is not a folder", SafeText(dir))
			}
		}
		return nil
	}
	for _, dir := range dirs {
		if dir == "." {
			continue
		}
		if stateEntry(dir) {
			return errStateEntry
		}
		if err := noteDirs(dir); err != nil {
			return err
		}
	}
	type toKeep struct {
		rel  string
		info os.FileInfo
	}
	keep := []toKeep{}
	added := []string{}
	seen := map[string]bool{}
	var keepSize, largest uint64
	for _, rel := range files {
		if seen[rel] {
			continue
		}
		seen[rel] = true
		if stateEntry(rel) {
			return errStateEntry
		}
		if dir := filepath.Dir(rel); dir != "." {
			if err := noteDirs(dir); err != nil {
				return err
			}
		}
		info, err := os.Lstat(filepath.Join(folder, rel))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("%s is in the way of the update: it is not a plain file", SafeText(rel))
		}
		if err == nil && uint64(info.Size()) > largest {
			largest = uint64(info.Size())
		}
		if known[rel] {
			continue // already in the copy kept, as it was in the proven version
		}
		if os.IsNotExist(err) {
			added = append(added, rel)
			continue
		}
		keep = append(keep, toKeep{rel, info})
		keepSize += uint64(info.Size())
	}

	// room: the copy, everything the zip writes, and a margin
	need := keepSize + unpacked
	need += spaceMargin(need, largest)
	free, err := freeSpace(folder)
	if err != nil {
		return err
	}
	kept := filepath.Join(folder, previousFolder)
	if !onTrial {
		free += treeSize(kept) // the copy of the last update goes first
	}
	if free < need {
		return &noSpaceError{folder: folder, need: need, free: free}
	}

	// from here on files are written
	if !onTrial {
		if err := os.RemoveAll(kept); err != nil { // nothing is owed: no update pending
			return err
		}
		os.Remove(filepath.Join(folder, provenName))
		if err := os.Mkdir(kept, 0700); err != nil {
			return err
		}
	}
	fail := func(err error) error {
		if !onTrial {
			os.RemoveAll(kept)
		}
		return err
	}
	for _, k := range keep {
		if err := os.MkdirAll(filepath.Join(kept, filepath.Dir(k.rel)), 0700); err != nil {
			return fail(err)
		}
		sum, size, err := copyFile(filepath.Join(folder, k.rel), filepath.Join(kept, k.rel), 0600)
		if err != nil {
			return fail(err)
		}
		manifest.Replaced = append(manifest.Replaced, keptFile{Path: k.rel, SHA256: sum, Mode: uint32(k.info.Mode().Perm()), Size: size})
	}
	manifest.Added = append(manifest.Added, added...)
	for dir := range addedDir {
		if !knownDir[dir] {
			manifest.AddedDirs = append(manifest.AddedDirs, dir)
		}
	}
	sort.Strings(manifest.AddedDirs)
	raw, err := json.Marshal(manifest)
	if err != nil {
		return fail(err)
	}
	if err := writeAtomic(filepath.Join(kept, previousManifest), raw); err != nil {
		return fail(err)
	}
	syncFolder(kept)
	// from here on a start finds the state: an unzip cut short is undone
	installing := Trial{State: stateInstalling, NewVersion: newVersion, PreviousVersion: previousVersion}
	if err := changeTrial(folder, func(t *Trial, _ error) (bool, bool) { *t = installing; return true, false }); err != nil {
		return fail(err)
	}
	return nil
}

// copyFile copies a regular file (never through a link) to a new file with
// the given mode, synced, and returns the SHA-256 and size of what was copied.
func copyFile(from string, to string, mode os.FileMode) (string, int64, error) {
	source, err := os.OpenFile(from, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", 0, err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxZip {
		return "", 0, ErrTooLarge
	}
	os.Remove(to)
	target, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(target, hash), io.LimitReader(source, MaxZip+1))
	if err == nil {
		err = target.Chmod(mode)
	}
	if err == nil {
		err = target.Sync()
	}
	if closeErr := target.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(to)
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func readKept(folder string) (keptManifest, error) {
	var manifest keptManifest
	kept := filepath.Join(folder, previousFolder)
	if info, err := os.Lstat(kept); err != nil || !info.IsDir() {
		return manifest, errors.New("there are no kept files")
	}
	raw, err := readRegular(filepath.Join(kept, previousManifest), maxKeptManifest)
	if err != nil {
		return manifest, errors.New("the list of kept files cannot be read")
	}
	bad := errors.New("the list of kept files is not in the expected form")
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Format != 1 {
		return manifest, bad
	}
	if _, ok := Build(manifest.PreviousVersion); !ok {
		return manifest, bad
	}
	if _, ok := Build(manifest.NewVersion); !ok {
		return manifest, bad
	}
	if len(manifest.Replaced)+len(manifest.Added)+len(manifest.AddedDirs) > 3*maxKeptFiles {
		return manifest, errors.New("the list of kept files is too long")
	}
	for _, f := range manifest.Replaced {
		if !localPath(f.Path) || stateEntry(f.Path) || !sha256Pattern.MatchString(f.SHA256) || f.Mode > 0777 {
			return manifest, bad
		}
	}
	for _, list := range [][]string{manifest.Added, manifest.AddedDirs} {
		for _, rel := range list {
			if !localPath(rel) || stateEntry(rel) {
				return manifest, bad
			}
		}
	}
	return manifest, nil
}

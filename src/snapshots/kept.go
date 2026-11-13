package snapshots

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// A restore keeps the folder it replaced next to it, as
// "<folder>.before-restore-<time>". That is the admin's previous data, as
// large as the folder was: nothing here removes it on its own. It is listed
// with the folder it belongs to, and removed only when an admin asks for
// that one folder by name.

// keptMark is what a restore puts between the name of the folder and the time.
const keptMark = ".before-restore-"

// KeptFolder is one folder a restore moved aside.
type KeptFolder struct {
	Name string    `json:"name"` // its name; it lies in the folder that contains the source
	Date time.Time `json:"date"` // when the restore moved it aside
}

// keptStamp returns the time in name when name is what a restore of source
// calls the folder it moves aside: "<base of source>.before-restore-" and a
// time written as the snapshots' names are. Nothing else is ever listed or
// removed, and such a name has no room for a path.
func keptStamp(source string, name string) (stamp string, ok bool) {
	base := filepath.Base(filepath.Clean(source))
	if source == "" || base == "/" || base == "." || base == ".." {
		return "", false
	}
	prefix := base + keptMark
	if !strings.HasPrefix(name, prefix) {
		return "", false
	}
	stamp = name[len(prefix):]
	return stamp, idPattern.MatchString(stamp)
}

// listKept returns the folders that restores of source left next to it,
// newest first. Files and links with such a name are not listed.
func listKept(source string) []KeptFolder {
	list := []KeptFolder{}
	entries, err := os.ReadDir(filepath.Dir(filepath.Clean(hostPath(source))))
	if err != nil {
		return list
	}
	for _, entry := range entries {
		stamp, ok := keptStamp(source, entry.Name())
		if !ok || !entry.IsDir() {
			continue
		}
		kept := KeptFolder{Name: entry.Name()}
		kept.Date, _ = time.ParseInLocation(idLayout, stamp, time.Local)
		list = append(list, kept)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name > list[j].Name })
	return list
}

// checkKept checks what the client sent, without touching the disk: the
// folder must be one of the configured ones, word for word, and the name
// that of a folder a restore of it leaves.
func checkKept(cfg utils.SnapshotsConfig, source string, name string) error {
	known := false
	for _, wanted := range cfg.Sources {
		known = known || (source != "" && wanted == source)
	}
	if !known {
		return fmt.Errorf("%s is not one of the folders that are backed up", strconv.Quote(source))
	}
	if _, ok := keptStamp(source, name); !ok || filepath.Base(name) != name {
		return fmt.Errorf("%s is not the name of a folder that a restore of %s kept", strconv.Quote(name), source)
	}
	return nil
}

// removeKept deletes the folder called name next to source. The caller has
// checked both (checkKept) and holds the lock.
func removeKept(cfg utils.SnapshotsConfig, source string, name string) error {
	// what is saved is validated; the file may have been edited by hand since
	if err := ValidateConfig(&cfg); err != nil {
		return err
	}
	shown := filepath.Join(filepath.Dir(filepath.Clean(source)), name) // the folder, as the admin knows it

	// never a folder that is, or holds, something in use
	inUse := append([]string{cfg.Destination}, cfg.Sources...)
	if own := configFolder(); !utils.IsInsideContainer {
		inUse = append(inUse, own)
	}
	for _, used := range inUse {
		if used != "" && (used == shown || inside(used, shown)) {
			return fmt.Errorf("%s is in use: it is, or holds, %s", shown, used)
		}
	}

	// As everywhere here: the folder is reached by its name through a handle
	// on the one that contains it, so nothing outside it can be removed.
	parentDir := filepath.Dir(filepath.Clean(hostPath(source)))
	parent, err := os.OpenRoot(parentDir)
	if err != nil {
		return fmt.Errorf("the folder that contains %s cannot be opened (%s)", source, reason(err))
	}
	defer parent.Close()

	info, err := parent.Lstat(name)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("there is no folder %s", shown)
	case err != nil:
		return fmt.Errorf("%s cannot be read (%s)", shown, reason(err))
	case !info.IsDir(): // Lstat: a link to a folder is not a folder
		return fmt.Errorf("%s is not a folder", shown)
	case mountPoint(filepath.Join(parentDir, name)):
		return fmt.Errorf("%s is where a disk is mounted, so it is not deleted", shown)
	case mountBelow(filepath.Join(parentDir, name)) != "":
		// see mounts_below.go: deleting the folder would empty that disk
		return fmt.Errorf("%s has a disk mounted inside it (%s), so it is not deleted; unmount it or move it out first", shown, mountBelow(filepath.Join(parentDir, name)))
	}
	if err := parent.RemoveAll(name); err != nil {
		return fmt.Errorf("%s could not be deleted completely (%s)", shown, reason(err))
	}
	return nil
}

// RemoveKept deletes one folder that a restore of source kept next to it
// ("<folder>.before-restore-<time>") and returns when it is gone. source
// must be one of the configured folders and name the name of such a folder,
// without a path; a link, a file or a mount point with that name is refused.
// It does not start while a snapshot run or a restore is in progress
// (ErrBusy).
func RemoveKept(source string, name string) error {
	cfg := utils.GetMainConfig().Snapshots
	if err := checkKept(cfg, source, name); err != nil {
		return err
	}
	if !runMu.TryLock() {
		return ErrBusy
	}
	defer runMu.Unlock()
	setRunning("cleanup", source)
	defer setIdle()

	utils.Log("Snapshots: deleting " + name + ", kept by a restore of " + source)
	if err := removeKept(cfg, source, name); err != nil {
		utils.Error("Snapshots: "+name+" was not deleted: "+err.Error(), nil)
		return err
	}
	return nil
}

package snapshots

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

// A restore copies a snapshot next to the folder it was taken from
// ("<folder>.restoring-<snapshot>"), then moves the current folder aside
// ("<folder>.before-restore-<now>") and the copy into its place. Nothing is
// deleted. The copy is made of real files, never of links to the snapshot,
// so working in the restored folder cannot change the snapshot.

// restoreJob is a restore that passed every check.
type restoreJob struct {
	source   string // the folder, as the settings name it
	id       string
	snapshot string // the snapshot, as this process sees it
	target   string // the folder, as this process sees it
	staging  string // where the copy is built
}

// mountPoint reports whether a folder is where a file system is mounted. A
// variable so tests can pretend.
var mountPoint = isMountPoint

func isMountPoint(dir string) bool {
	info, err := os.Lstat(dir)
	if err != nil {
		return false
	}
	if parent, err := os.Lstat(filepath.Dir(dir)); err == nil {
		here, okHere := info.Sys().(*syscall.Stat_t)
		above, okAbove := parent.Sys().(*syscall.Stat_t)
		if okHere && okAbove && here.Dev != above.Dev {
			return true
		}
	}
	// a mount of the same disk (a bind mount) does not change the device
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	for _, point := range mountPoints(string(data)) {
		if point == dir {
			return true
		}
	}
	return false
}

// mountPoints lists the mount points named in the content of
// /proc/self/mountinfo (the fifth field of each line).
func mountPoints(mountinfo string) []string {
	points := []string{}
	for _, line := range strings.Split(mountinfo, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		points = append(points, unescapeMount(fields[4]))
	}
	return points
}

// unescapeMount undoes the kernel's octal escapes, such as \040 for a space.
func unescapeMount(text string) string {
	var plain strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' && i+3 < len(text) {
			if code, err := strconv.ParseUint(text[i+1:i+4], 8, 8); err == nil {
				plain.WriteByte(byte(code))
				i += 3
				continue
			}
		}
		plain.WriteByte(text[i])
	}
	return plain.String()
}

// isOwnFolder tells whether restoring source would move Cosmos' own folder,
// or part of it, from under the running server.
func isOwnFolder(source string, target string) bool {
	own := configFolder()
	if own == "" {
		return false
	}
	if !utils.IsInsideContainer && (source == own || inside(source, own) || inside(own, source)) {
		return true
	}
	// the same folder under another name: a container sees the host's elsewhere
	ownInfo, errOwn := os.Stat(own)
	targetInfo, errTarget := os.Stat(target)
	return errOwn == nil && errTarget == nil && os.SameFile(ownInfo, targetInfo)
}

// checkRequest checks what the client sent, without touching the disk: the
// folder must be one of the configured ones, word for word, and the snapshot
// must be named like one. Nothing else ever becomes part of a path.
func checkRequest(cfg utils.SnapshotsConfig, source string, id string) error {
	if cfg.Destination == "" {
		return errors.New("the incremental backup has no destination, so there is nothing to restore")
	}
	known := false
	for _, wanted := range cfg.Sources {
		known = known || (source != "" && wanted == source)
	}
	if !known {
		return fmt.Errorf("%s is not one of the folders that are backed up", strconv.Quote(source))
	}
	if !validID(id) {
		return fmt.Errorf("%s is not the name of a snapshot", strconv.Quote(id))
	}
	return nil
}

// planRestore checks that the snapshot exists and that the folder can be
// replaced.
func planRestore(cfg utils.SnapshotsConfig, source string, id string) (restoreJob, error) {
	job := restoreJob{source: source, id: id}
	if err := checkRequest(cfg, source, id); err != nil {
		return job, err
	}
	// what is saved is validated; the file may have been edited by hand since
	if err := ValidateConfig(&cfg); err != nil {
		return job, err
	}

	dir, err := slugDir(cfg.Destination, source)
	if err != nil {
		return job, err
	}
	job.snapshot = filepath.Join(dir, id)
	job.target = filepath.Clean(hostPath(source))
	job.staging = job.target + ".restoring-" + id
	shown := filepath.Join(filepath.Clean(cfg.Destination), Slug(source), id) // the snapshot, as the admin knows it

	if info, err := os.Lstat(job.snapshot); err != nil || !info.IsDir() {
		return job, fmt.Errorf("there is no snapshot %s of %s", id, source)
	}

	// the snapshots would be moved aside with the folder, and be gone from where they are looked for
	if clean := filepath.Clean(source); clean == cfg.Destination || inside(cfg.Destination, clean) {
		return job, fmt.Errorf("the snapshots are stored inside %s, so it cannot be moved aside and replaced; copy the files back by hand from %s", source, shown)
	}

	if isOwnFolder(source, job.target) {
		return job, fmt.Errorf("%s holds Cosmos' own files, which are in use while Cosmos runs, so it cannot be restored from here. Stop Cosmos, then copy the content of %s over it by hand", source, shown)
	}

	info, err := os.Lstat(job.target)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// the folder is gone: the snapshot simply takes its place
		if parent, err := os.Stat(filepath.Dir(job.target)); err != nil || !parent.IsDir() {
			return job, fmt.Errorf("the folder that contained %s does not exist any more; create it first", source)
		}
	case err != nil:
		return job, fmt.Errorf("%s cannot be read (%s)", source, reason(err))
	case info.Mode()&os.ModeSymlink != 0:
		return job, fmt.Errorf("%s is now a symbolic link and cannot be replaced; copy the files back by hand from %s", source, shown)
	case !info.IsDir():
		return job, fmt.Errorf("%s is not a folder any more; move it away first", source)
	case mountPoint(job.target):
		return job, fmt.Errorf("%s is where a disk is mounted, so it cannot be moved aside and replaced; copy the files back by hand from %s", source, shown)
	}

	if _, err := os.Lstat(job.staging); !errors.Is(err, os.ErrNotExist) {
		return job, fmt.Errorf("%s.restoring-%s already exists, left by a restore that was interrupted; remove it and try again", source, id)
	}
	return job, nil
}

// run does the restore. now names the folder the current version is kept in.
func (job restoreJob) run(now time.Time) RestoreResult {
	result := RestoreResult{Source: job.source, Snapshot: job.id}
	failed := func(message string) RestoreResult {
		result.Error = message
		result.Finished = time.Now()
		return result
	}

	info, err := os.Lstat(job.snapshot)
	if err != nil || !info.IsDir() {
		return failed(fmt.Sprintf("there is no snapshot %s of %s", job.id, job.source))
	}
	top, err := openTree(job.snapshot, info)
	if err != nil {
		return failed(fmt.Sprintf("the snapshot %s cannot be read (%s)", job.id, reason(err)))
	}
	defer top.Close()

	// Everything that is written or moved is named from a handle on the
	// folder that contains the one being restored (see walker): whoever else
	// can write there cannot send the copy somewhere else.
	parent, err := os.OpenRoot(filepath.Dir(job.target))
	if err != nil {
		return failed(fmt.Sprintf("the folder that contains %s cannot be opened (%s)", job.source, reason(err)))
	}
	defer parent.Close()
	name := filepath.Base(job.target)
	staging := filepath.Base(job.staging)

	// created here, and only removed again if it was
	if err := parent.Mkdir(staging, 0700); err != nil {
		return failed(fmt.Sprintf("cannot create a folder next to %s (%s)", job.source, reason(err)))
	}
	undo := func() { parent.RemoveAll(staging) }
	copyTo, err := parent.OpenRoot(staging)
	if err != nil {
		undo()
		return failed(fmt.Sprintf("cannot create a folder next to %s (%s)", job.source, reason(err)))
	}
	defer copyTo.Close()

	w := &walker{srcRoot: job.snapshot, dstRoot: job.staging, strict: true}
	if !stillThere(parent, staging, copyTo) {
		w.fatal = errReplaced
	} else {
		w.dir(top, copyTo, nil, "")
	}
	if w.fatal == nil {
		if err := finishDir(copyTo, info); err != nil {
			w.fatal = err
		}
	}
	// what is put in place is the folder that was written, nothing else
	if w.fatal == nil && !stillThere(parent, staging, copyTo) {
		w.fatal = errReplaced
	}
	if w.fatal != nil {
		undo()
		return failed("the snapshot could not be copied back, nothing was changed: " + w.fatal.Error())
	}
	result.Files = w.files
	syncDisks()

	keptAs := ""
	if _, err := parent.Lstat(name); err == nil {
		stamp := now
		for tries := 0; ; tries++ {
			keptAs = keptMark + stamp.Format(idLayout)
			if _, err := parent.Lstat(name + keptAs); errors.Is(err, os.ErrNotExist) {
				break
			}
			if tries > 600 {
				undo()
				return failed("no free name was found to keep the current folder under, nothing was changed")
			}
			stamp = stamp.Add(time.Second)
		}
		if err := parent.Rename(name, name+keptAs); err != nil {
			undo()
			return failed(fmt.Sprintf("the current folder could not be moved aside, nothing was changed (%s)", reason(err)))
		}
	}

	if err := parent.Rename(staging, name); err != nil {
		message := fmt.Sprintf("the restored copy could not be put in place (%s)", reason(err))
		if keptAs != "" {
			if errBack := parent.Rename(name+keptAs, name); errBack != nil {
				// both are still on the disk; say where
				return failed(fmt.Sprintf("%s. The current folder is now %s and the restored copy is %s.restoring-%s", message, job.source+keptAs, job.source, job.id))
			}
		}
		undo()
		return failed(message + ", nothing was changed")
	}

	if keptAs != "" {
		result.KeptAs = job.source + keptAs
	}
	result.Finished = time.Now()
	return result
}

// beginRestore checks the request and takes the lock. The caller must call
// finish, which releases it.
func beginRestore(source string, id string) (restoreJob, error) {
	cfg := utils.GetMainConfig().Snapshots
	if err := checkRequest(cfg, source, id); err != nil {
		return restoreJob{}, err
	}
	if !runMu.TryLock() {
		return restoreJob{}, ErrBusy
	}
	job, err := planRestore(cfg, source, id)
	if err != nil {
		runMu.Unlock()
		return job, err
	}
	setRunning("restore", source)
	return job, nil
}

func (job restoreJob) finish() RestoreResult {
	defer runMu.Unlock()
	defer setIdle()

	utils.Log("Snapshots: restoring " + job.source + " from the snapshot " + job.id)
	result := job.run(time.Now())
	if result.Error != "" {
		utils.Error("Snapshots: restoring "+job.source+" failed: "+result.Error, nil)
	} else {
		utils.Log(fmt.Sprintf("Snapshots: %s restored from the snapshot %s (%d files); the previous version is kept as %s", job.source, job.id, result.Files, result.KeptAs))
	}

	stateMu.Lock()
	lastRestore = &result
	stateMu.Unlock()
	return result
}

// Restore replaces the folder source by its snapshot snapshotID and returns
// when that is done. source must be one of the configured folders and
// snapshotID the name of one of its snapshots; the version of the folder
// that was there is kept next to it as "<folder>.before-restore-<time>".
// It refuses Cosmos' own folder, a folder a disk is mounted on, and to start
// while a snapshot run or another restore is in progress (ErrBusy).
func Restore(source string, snapshotID string) error {
	job, err := beginRestore(source, snapshotID)
	if err != nil {
		return err
	}
	if result := job.finish(); result.Error != "" {
		return errors.New(result.Error)
	}
	return nil
}

// StartRestore is Restore in the background: it returns as soon as the
// request passed the checks and the restore has started.
func StartRestore(source string, snapshotID string) error {
	job, err := beginRestore(source, snapshotID)
	if err != nil {
		return err
	}
	go job.finish()
	return nil
}

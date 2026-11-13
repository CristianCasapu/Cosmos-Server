// Package snapshots is a fork addition: incremental backups of folders as
// dated, hard-linked snapshots (the layout rsnapshot made familiar). It was
// written for this fork, shares no code with upstream's backup feature and
// does not use restic.
//
//	<Destination>/<slug of the source>/<2006-01-02_150405>/
//
// is one snapshot of one folder. To a file manager it is a complete copy; a
// file that did not change since the previous snapshot is a hard link to it,
// so only changes take space. Nothing is encrypted or compressed.
//
// A snapshot is built under "<timestamp>.partial" and renamed when it is
// complete, so a name without that ending is always a finished snapshot.
package snapshots

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	idLayout   = "2006-01-02_150405"
	partialExt = ".partial"
	// metaName is the record a finished snapshot carries at its top.
	metaName = ".cosmos-snapshot.json"
	// maxErrorMessages is how many of a run's error messages are kept.
	maxErrorMessages = 5
	// maxSummaryLength is how long the text of the admins' notice can get.
	maxSummaryLength = 600
)

var (
	idPattern      = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}_\d{6}$`)
	partialPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}_\d{6}\.partial$`)
)

// ErrBusy is returned while a snapshot run or a restore is in progress.
var ErrBusy = errors.New("a backup or a restore is already running; wait for it to finish")

// ErrNotConfigured is returned when the module is off or incomplete.
var ErrNotConfigured = errors.New("the incremental backup is not enabled: enable it, choose a destination and at least one folder, and save the settings")

// errUnusable is returned when the module is on but its saved settings do
// not pass ValidateConfig, which only a file edited by hand can cause.
var errUnusable = errors.New("the saved settings of the incremental backup cannot be used")

// Meta is the record written into a finished snapshot.
type Meta struct {
	Source        string    `json:"source"`
	Started       time.Time `json:"started"`
	Finished      time.Time `json:"finished"`
	Files         int64     `json:"files"`
	BytesCopied   int64     `json:"bytesCopied"`
	BytesLinked   int64     `json:"bytesLinked"`
	Errors        int       `json:"errors"`
	ErrorMessages []string  `json:"errorMessages,omitempty"`
}

// Snapshot is one finished snapshot as the API lists it.
type Snapshot struct {
	ID          string    `json:"id"`
	Finished    time.Time `json:"finished"`
	Files       int64     `json:"files"`
	BytesCopied int64     `json:"bytesCopied"`
	BytesLinked int64     `json:"bytesLinked"`
	Errors      int       `json:"errors"`
}

// SourceResult is how the last run went for one folder.
type SourceResult struct {
	Snapshot string    `json:"snapshot,omitempty"` // empty when no snapshot was made
	Finished time.Time `json:"finished"`
	Errors   int       `json:"errors"`
	Error    string    `json:"error,omitempty"`
}

// RestoreResult is how the last restore went.
type RestoreResult struct {
	Source   string    `json:"source"`
	Snapshot string    `json:"snapshot"`
	Finished time.Time `json:"finished"`
	Files    int64     `json:"files"`
	KeptAs   string    `json:"keptAs,omitempty"` // where the previous version of the folder went
	Error    string    `json:"error,omitempty"`
}

// Status is what is running now and how the last run went.
type Status struct {
	Running     bool                    `json:"running"`
	Action      string                  `json:"action,omitempty"` // "snapshot", "restore" or "cleanup"
	Current     string                  `json:"current"`
	FilesDone   int64                   `json:"filesDone"`
	StartedAt   *time.Time              `json:"startedAt,omitempty"`
	LastResults map[string]SourceResult `json:"lastResults"`
	LastRestore *RestoreResult          `json:"lastRestore,omitempty"`
}

var (
	runMu sync.Mutex // one snapshot run or restore at a time

	stateMu     sync.Mutex
	running     bool
	action      string
	current     string
	startedAt   time.Time
	lastResults = map[string]SourceResult{}
	lastRestore *RestoreResult

	filesDone atomic.Int64
)

// syncDisks flushes what was written before a snapshot is declared finished.
// A variable so tests do not flush the whole machine.
var syncDisks = syscall.Sync

// notifyFailure tells the admins, once per run, that a run did not go
// cleanly. A variable so tests can replace it.
var notifyFailure = func(summary string) {
	utils.WriteNotification(utils.Notification{
		Recipient: "admin",
		Title:     "header.notification.title.snapshotsFailed",
		Message:   "header.notification.message.snapshotsFailed",
		Vars:      summary,
		Level:     "error",
	})
}

// GetStatus returns what is running and the outcome of the last run.
func GetStatus() Status {
	stateMu.Lock()
	defer stateMu.Unlock()

	st := Status{
		Running:     running,
		Action:      action,
		Current:     current,
		LastResults: map[string]SourceResult{},
	}
	if running {
		started := startedAt
		st.StartedAt = &started
		st.FilesDone = filesDone.Load()
	}
	for source, result := range lastResults {
		st.LastResults[source] = result
	}
	if lastRestore != nil {
		result := *lastRestore
		st.LastRestore = &result
	}
	return st
}

func setRunning(what string, source string) {
	stateMu.Lock()
	defer stateMu.Unlock()
	if !running {
		startedAt = time.Now()
		filesDone.Store(0)
	}
	running, action, current = true, what, source
}

func setIdle() {
	stateMu.Lock()
	defer stateMu.Unlock()
	running, action, current = false, "", ""
}

// hostPath is where a folder of the host is found by this process: inside a
// container the host's files are mounted under /mnt/host.
func hostPath(dir string) string {
	if utils.IsInsideContainer {
		return "/mnt/host" + dir
	}
	return dir
}

// Slug is the name of the folder, inside the destination, that holds the
// snapshots of source: its path with "-" between the parts, so
// /var/lib/cosmos becomes var-lib-cosmos. A "-" that belongs to a name is
// written "~-" and a "~" is written "~~" (/var/lib-cosmos becomes
// var-lib~-cosmos), which makes the name reversible: two different folders
// can never share one.
func Slug(source string) string {
	parts := strings.Split(strings.Trim(filepath.Clean(source), "/"), "/")
	for i, part := range parts {
		part = strings.ReplaceAll(part, "~", "~~")
		parts[i] = strings.ReplaceAll(part, "-", "~-")
	}
	return strings.Join(parts, "-")
}

// slugDir returns <destination>/<slug>, as this process sees it, and refuses
// anything that would not be a direct child of the destination.
func slugDir(destination string, source string) (string, error) {
	if destination == "" || !filepath.IsAbs(destination) || filepath.Clean(destination) == "/" {
		return "", errors.New("no usable destination is set")
	}
	slug := Slug(source)
	if slug == "" || slug == "." || slug == ".." || strings.ContainsAny(slug, "/\x00") {
		return "", fmt.Errorf("%s cannot be backed up: no folder name can be made from it", source)
	}
	root := filepath.Clean(hostPath(destination))
	dir := filepath.Join(root, slug)
	if filepath.Dir(dir) != root || filepath.Base(dir) != slug {
		return "", fmt.Errorf("%s cannot be backed up: no folder name can be made from it", source)
	}
	return dir, nil
}

// validID tells whether name is the name of a finished snapshot.
func validID(name string) bool {
	if !idPattern.MatchString(name) {
		return false
	}
	_, err := time.ParseInLocation(idLayout, name, time.Local)
	return err == nil
}

// validPartial tells whether name is the name of a snapshot in the making.
func validPartial(name string) bool {
	return partialPattern.MatchString(name) && validID(strings.TrimSuffix(name, partialExt))
}

// readMeta reads the record of a snapshot; ok is false when there is none
// that can be read.
func readMeta(snapshotDir string) (meta Meta, ok bool) {
	file := filepath.Join(snapshotDir, metaName)
	info, err := os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return Meta{}, false
	}
	data, err := os.ReadFile(file)
	if err != nil || json.Unmarshal(data, &meta) != nil {
		return Meta{}, false
	}
	return meta, true
}

// listSnapshots returns the finished snapshots in dir (<destination>/<slug>),
// newest first. Anything else in there is ignored: unfinished snapshots,
// files, links, and folders with another name.
func listSnapshots(dir string) []Snapshot {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []Snapshot{}
	}
	list := []Snapshot{}
	for _, entry := range entries {
		if !entry.IsDir() || !validID(entry.Name()) {
			continue
		}
		snapshot := Snapshot{ID: entry.Name()}
		if meta, ok := readMeta(filepath.Join(dir, entry.Name())); ok {
			snapshot.Finished = meta.Finished
			snapshot.Files = meta.Files
			snapshot.BytesCopied = meta.BytesCopied
			snapshot.BytesLinked = meta.BytesLinked
			snapshot.Errors = meta.Errors
		} else {
			snapshot.Finished, _ = time.ParseInLocation(idLayout, entry.Name(), time.Local)
		}
		list = append(list, snapshot)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID > list[j].ID })
	return list
}

// List returns the finished snapshots of source, newest first.
func List(destination string, source string) []Snapshot {
	dir, err := slugDir(destination, source)
	if err != nil {
		return []Snapshot{}
	}
	return listSnapshots(dir)
}

// removeSnapshotDir deletes one snapshot, or what an interrupted run left
// behind. It is the only place where this package deletes anything in the
// destination, and it refuses whatever is not a real folder, named like a
// snapshot, directly inside <destination>/<slug>. The removal itself goes
// through a handle on that folder, so it cannot reach anything outside it.
func removeSnapshotDir(destination string, source string, name string) error {
	if !validID(name) && !validPartial(name) {
		return fmt.Errorf("%q is not the name of a snapshot", name)
	}
	dir, err := slugDir(destination, source)
	if err != nil {
		return err
	}
	if filepath.Base(name) != name || filepath.Dir(filepath.Join(dir, name)) != dir {
		return fmt.Errorf("%q is not the name of a snapshot", name)
	}
	parent, err := os.Lstat(dir)
	if err != nil || !parent.IsDir() { // Lstat: a link to a folder is not a folder
		return fmt.Errorf("%s is not a folder", dir)
	}
	store, err := openTree(dir, parent)
	if err != nil {
		return err
	}
	defer store.Close()

	info, err := store.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a folder", filepath.Join(dir, name))
	}
	return store.RemoveAll(name)
}

// cleanPartials removes what interrupted runs left in dir.
func cleanPartials(destination string, source string, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !validPartial(entry.Name()) {
			continue
		}
		if err := removeSnapshotDir(destination, source, entry.Name()); err != nil {
			utils.Warn("Snapshots: could not remove the unfinished snapshot " + filepath.Join(dir, entry.Name()) + ": " + reason(err))
		}
	}
}

// unused tells whether neither a snapshot nor an unfinished one has this name.
func unused(dir string, id string) bool {
	_, errDone := os.Lstat(filepath.Join(dir, id))
	_, errPartial := os.Lstat(filepath.Join(dir, id+partialExt))
	return errors.Is(errDone, os.ErrNotExist) && errors.Is(errPartial, os.ErrNotExist)
}

// reason is the operating system's explanation without the path, which the
// caller already names.
func reason(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return linkErr.Err.Error()
	}
	return err.Error()
}

// isEmpty tells whether a folder holds nothing at all.
func isEmpty(dir *os.Root) bool {
	list, err := dir.Open(".")
	if err != nil {
		return false
	}
	defer list.Close()
	entries, err := list.ReadDir(1)
	return len(entries) == 0 && err == io.EOF
}

// previousFiles is how many files a finished snapshot holds: what its record
// says or, without a record, how many entries it has at its top.
func previousFiles(snapshotDir string) int64 {
	if meta, ok := readMeta(snapshotDir); ok {
		return meta.Files
	}
	entries, _ := os.ReadDir(snapshotDir)
	count := int64(0)
	for _, entry := range entries {
		if entry.Name() != metaName {
			count++
		}
	}
	return count
}

// snapshotSource makes one snapshot of source and applies the retention. It
// returns the name of the new snapshot and its record. err is set when no
// snapshot could be made; files that could not be read are counted in the
// record instead and do not stop the run.
func snapshotSource(cfg utils.SnapshotsConfig, source string, now time.Time) (id string, meta Meta, err error) {
	meta = Meta{Source: source, Started: now}

	dir, err := slugDir(cfg.Destination, source)
	if err != nil {
		return "", meta, err
	}

	// The destination is created when the settings are saved. If it is gone
	// now, its disk is probably not mounted: do not fill another disk instead.
	destReal := filepath.Clean(hostPath(cfg.Destination))
	destInfo, err := os.Stat(destReal)
	if err != nil || !destInfo.IsDir() {
		return "", meta, fmt.Errorf("the destination %s does not exist (is its disk connected?)", cfg.Destination)
	}

	srcReal := filepath.Clean(hostPath(source))
	srcInfo, err := os.Lstat(srcReal)
	switch {
	case err != nil:
		return "", meta, fmt.Errorf("the folder cannot be read (%s)", reason(err))
	case srcInfo.Mode()&os.ModeSymlink != 0:
		return "", meta, errors.New("it is a symbolic link; add the folder it points to instead")
	case !srcInfo.IsDir():
		return "", meta, errors.New("it is not a folder")
	}

	// Owner only: the copies keep the owners and modes of the originals, but
	// not the protection the folders above the originals gave them.
	if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", meta, fmt.Errorf("cannot create %s (%s)", dir, reason(err))
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil || !dirInfo.IsDir() {
		return "", meta, fmt.Errorf("%s is not a folder", dir)
	}

	// from here on both trees are reached through handles, not paths (see walker)
	top, err := openTree(srcReal, srcInfo)
	if err != nil {
		return "", meta, fmt.Errorf("the folder cannot be read (%s)", reason(err))
	}
	defer top.Close()
	store, err := openTree(dir, dirInfo)
	if err != nil {
		return "", meta, fmt.Errorf("cannot write to the destination (%s)", reason(err))
	}
	defer store.Close()

	cleanPartials(cfg.Destination, source, dir)

	previousID := ""
	if list := listSnapshots(dir); len(list) > 0 {
		previousID = list[0].ID
	}

	// A folder that held files and now holds nothing at all is most likely a
	// disk that is not mounted. A snapshot of it would record that everything
	// was deleted, and the retention would then let the good snapshots age
	// out: neither is done. A folder that was empty before may stay empty.
	if previousID != "" && isEmpty(top) {
		if had := previousFiles(filepath.Join(dir, previousID)); had > 0 {
			files := "files"
			if had == 1 {
				files = "file"
			}
			return "", meta, fmt.Errorf("the folder is empty; the previous snapshot had %d %s — is the disk mounted?", had, files)
		}
	}

	// The name is the time of the run, and always later than the newest
	// snapshot's: names are what orders the snapshots, also when two runs
	// start within one second or the clock was set back.
	stamp := now
	if previousID != "" && stamp.Format(idLayout) <= previousID {
		if newest, err := time.ParseInLocation(idLayout, previousID, time.Local); err == nil {
			stamp = newest.Add(time.Second)
		}
	}
	for tries := 0; ; tries++ {
		id = stamp.Format(idLayout)
		if id > previousID && unused(dir, id) {
			break
		}
		if tries > 7200 {
			return "", meta, fmt.Errorf("cannot find a free name for the snapshot in %s", dir)
		}
		stamp = stamp.Add(time.Second)
	}

	workName := id + partialExt
	if err := store.Mkdir(workName, 0700); err != nil {
		return "", meta, fmt.Errorf("cannot write to the destination (%s)", reason(err))
	}
	discard := func(cause error) (string, Meta, error) {
		if err := removeSnapshotDir(cfg.Destination, source, workName); err != nil {
			utils.Warn("Snapshots: could not remove the unfinished snapshot " + filepath.Join(dir, workName) + ": " + reason(err))
		}
		return "", meta, cause
	}
	unwritable := func(err error) (string, Meta, error) {
		return discard(fmt.Errorf("cannot write to the destination (%s)", reason(err)))
	}
	work, err := store.OpenRoot(workName)
	if err != nil {
		return unwritable(err)
	}
	defer work.Close()
	if !stillThere(store, workName, work) {
		return unwritable(errReplaced)
	}

	var previous *os.Root
	if previousID != "" {
		if previous, err = store.OpenRoot(previousID); err != nil {
			previous = nil // nothing to link to: everything is copied
		} else {
			defer previous.Close()
		}
	}

	w := &walker{
		srcRoot:  srcReal,
		dstRoot:  filepath.Join(dir, workName),
		store:    store,
		prevName: previousID,
		workName: workName,
		skipPath: destReal,
		skipInfo: destInfo,
		excludes: cfg.Excludes,
	}
	w.dir(top, work, previous, "")
	if w.fatal != nil {
		return discard(w.fatal)
	}

	meta.Files = w.files
	meta.BytesCopied = w.bytesCopied
	meta.BytesLinked = w.bytesLinked
	meta.Errors = w.errors
	meta.ErrorMessages = w.messages
	meta.Finished = time.Now()

	record, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return discard(err)
	}
	file, err := work.OpenFile(metaName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return unwritable(err)
	}
	_, err = file.Write(record)
	if errClose := file.Close(); err == nil {
		err = errClose
	}
	if err != nil {
		return unwritable(err)
	}
	if err := finishDir(work, srcInfo); err != nil {
		return unwritable(err)
	}

	syncDisks()
	// what gets the snapshot's name is the folder that was written, nothing else
	if !stillThere(store, workName, work) {
		return unwritable(errReplaced)
	}
	if err := store.Rename(workName, id); err != nil {
		return unwritable(err)
	}

	recent, daily, weekly, monthly := keepCounts(cfg)
	removed, errPrune := prune(cfg.Destination, source, recent, daily, weekly, monthly)
	if len(removed) > 0 {
		utils.Log(fmt.Sprintf("Snapshots: %s: removed %d old snapshot(s)", source, len(removed)))
	}
	if errPrune != nil {
		utils.Warn("Snapshots: " + source + ": " + errPrune.Error())
		meta.Errors++
		meta.ErrorMessages = append(meta.ErrorMessages, errPrune.Error())
	}
	return id, meta, nil
}

// configured tells whether the settings ask for snapshots and are complete.
func configured(cfg utils.SnapshotsConfig) bool {
	return cfg.Enabled && cfg.Destination != "" && len(cfg.Sources) > 0
}

// RunAll makes a snapshot of every configured folder, one after the other,
// and returns when it is done. It returns ErrBusy when a run or a restore is
// in progress and ErrNotConfigured when there is nothing to do. Settings
// that are switched on but cannot be used are reported to the admins.
func RunAll() error {
	cfg, err := begin()
	if err != nil {
		// this is what the schedule calls: nobody is waiting for the answer
		utils.Error("Snapshots: the run did not start: "+err.Error(), nil)
		if errors.Is(err, errUnusable) {
			notifyFailure(err.Error())
		}
		return err
	}
	runAll(cfg)
	return nil
}

// Start is RunAll in the background: it returns as soon as the run has
// started, or the reason why it cannot.
func Start() error {
	cfg, err := begin()
	if err != nil {
		return err
	}
	go runAll(cfg)
	return nil
}

// begin takes the lock for a snapshot run. The caller must call runAll,
// which releases it.
func begin() (utils.SnapshotsConfig, error) {
	cfg := utils.GetMainConfig().Snapshots
	if !configured(cfg) {
		return cfg, ErrNotConfigured
	}
	// the file can have been edited by hand since it was last saved
	if err := ValidateConfig(&cfg); err != nil {
		return cfg, fmt.Errorf("%w: %s", errUnusable, err.Error())
	}
	if !runMu.TryLock() {
		return cfg, ErrBusy
	}
	setRunning("snapshot", "")
	return cfg, nil
}

func runAll(cfg utils.SnapshotsConfig) {
	defer runMu.Unlock()
	defer setIdle()

	problems := []string{}
	for _, source := range cfg.Sources {
		setRunning("snapshot", source)
		utils.Log("Snapshots: backing up " + source)

		id, meta, err := snapshotSource(cfg, source, time.Now())
		result := SourceResult{Snapshot: id, Finished: time.Now(), Errors: meta.Errors}
		switch {
		case err != nil:
			result.Error = err.Error()
			problems = append(problems, source+": "+err.Error())
			utils.Error("Snapshots: "+source+" was not backed up: "+err.Error(), nil)
		case meta.Errors > 0:
			result.Error = fmt.Sprintf("%d item(s) could not be backed up, for example %s", meta.Errors, meta.ErrorMessages[0])
			problems = append(problems, source+": "+result.Error)
			utils.Warn("Snapshots: " + source + ": " + result.Error)
		default:
			utils.Log(fmt.Sprintf("Snapshots: %s done: %d files, %d bytes copied, %d bytes unchanged", source, meta.Files, meta.BytesCopied, meta.BytesLinked))
		}

		stateMu.Lock()
		lastResults[source] = result
		stateMu.Unlock()
	}

	// forget the outcome of folders that are no longer configured
	stateMu.Lock()
	for source := range lastResults {
		found := false
		for _, wanted := range cfg.Sources {
			found = found || wanted == source
		}
		if !found {
			delete(lastResults, source)
		}
	}
	stateMu.Unlock()

	if len(problems) > 0 {
		summary := strings.Join(problems, "; ")
		if runes := []rune(summary); len(runes) > maxSummaryLength {
			summary = string(runes[:maxSummaryLength]) + "…"
		}
		notifyFailure(summary)
	}
}

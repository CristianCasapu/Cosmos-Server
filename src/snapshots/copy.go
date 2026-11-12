package snapshots

import (
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
)

// walker copies one folder tree into another without following symbolic
// links. It is used to make a snapshot (unchanged files are linked to the
// previous snapshot, a file that cannot be read is counted and the walk goes
// on) and to restore one (strict: real copies only, the first problem stops
// it).
//
// Cosmos runs as root, and the trees it walks here may be written to by
// others at the same time: the data of an app is, and so is whatever an app
// can reach of the place a folder is restored to. No file is therefore
// reached through a path. Every folder, on both sides, is opened once as an
// os.Root, and what it holds is reached through that handle by its own name.
// A folder swapped for a symbolic link in mid-walk cannot lead the copy out
// of the tree it reads, nor out of the tree it writes.
type walker struct {
	srcRoot  string      // the tree that is read, as a path: compared, never opened
	dstRoot  string      // the tree that is written, as a path: for messages only
	store    *os.Root    // the folder holding the snapshots of this source; nil when nothing is linked
	prevName string      // in store: the previous snapshot, "" when there is none
	workName string      // in store: the snapshot being written
	skipPath string      // a folder that is never entered: the destination, when it lies inside the source
	skipInfo os.FileInfo // the same folder, recognised under any name
	strict   bool        // a problem with the source is fatal too

	files       int64
	bytesCopied int64
	bytesLinked int64
	errors      int
	messages    []string // the first few problems, as "path: reason"
	fatal       error    // the walk stopped: the target cannot be written

	buffer []byte
}

// openTree opens the folder a walk starts from and makes sure it is the one
// that was looked at before (info), not something put in its place since.
func openTree(dir string, info os.FileInfo) (*os.Root, error) {
	top, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	opened, err := top.Stat(".")
	if err != nil || !os.SameFile(opened, info) {
		top.Close()
		return nil, errors.New("the folder was replaced while it was being opened")
	}
	return top, nil
}

// betweenLookAndOpen is called, by tests only, after an entry of the source
// was looked at and before it is opened: the moment at which somebody else
// may put something different in its place.
var betweenLookAndOpen func(rel string)

// fail records a problem with something in the source.
func (w *walker) fail(rel string, err error) {
	message := rel + ": " + reason(err)
	if w.strict {
		w.fatal = errors.New(message)
		return
	}
	w.errors++
	if len(w.messages) < maxErrorMessages {
		w.messages = append(w.messages, message)
	}
}

// stop records that the target cannot be written, which ends the walk.
func (w *walker) stop(rel string, err error) {
	w.fatal = fmt.Errorf("cannot write %s (%s)", filepath.Join(w.dstRoot, rel), reason(err))
}

func (w *walker) isSkipped(path string, info os.FileInfo) bool {
	if w.skipPath == "" {
		return false
	}
	return path == w.skipPath || (w.skipInfo != nil && os.SameFile(info, w.skipInfo))
}

// dir copies the content of one folder. src is that folder, dst the folder
// it is copied to (it exists), prev the same folder in the previous snapshot
// (nil when there is none) and rel its path below the top ("" for the top).
func (w *walker) dir(src *os.Root, dst *os.Root, prev *os.Root, rel string) {
	var entries []os.DirEntry
	list, err := src.Open(".")
	if err == nil {
		entries, err = list.ReadDir(-1)
		list.Close()
	}
	if err != nil {
		if rel == "" {
			w.fatal = fmt.Errorf("the folder cannot be read (%s)", reason(err))
		} else {
			w.fail(rel, err)
		}
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, entry := range entries {
		if w.fatal != nil {
			return
		}
		name := entry.Name()
		relPath := filepath.Join(rel, name)

		// the snapshot's own record lives under this name
		if rel == "" && name == metaName {
			if !w.strict {
				w.fail(relPath, errors.New("this name is used by the snapshot itself, the file is not backed up"))
			}
			continue
		}

		info, err := src.Lstat(name)
		if err != nil {
			w.fail(relPath, err) // it vanished since the folder was listed
			continue
		}
		if betweenLookAndOpen != nil {
			betweenLookAndOpen(relPath)
		}

		switch {
		case info.IsDir():
			if w.isSkipped(filepath.Join(w.srcRoot, relPath), info) {
				continue
			}
			w.folder(src, dst, prev, name, relPath, info)
		case info.Mode().IsRegular():
			w.file(src, dst, prev, name, relPath, info)
		case info.Mode()&os.ModeSymlink != 0:
			w.symlink(src, dst, name, relPath, info)
		default:
			// sockets, devices and FIFOs are not files to keep
		}
	}
}

// folder copies the folder called name, with all it holds.
func (w *walker) folder(src *os.Root, dst *os.Root, prev *os.Root, name string, rel string, info os.FileInfo) {
	// refused if the folder has become a link that leads out of its parent,
	// and not used if it is no longer the folder that was listed
	sub, err := src.OpenRoot(name)
	if err != nil {
		w.fail(rel, err)
		return
	}
	defer sub.Close()
	if opened, err := sub.Stat("."); err != nil || !os.SameFile(opened, info) {
		w.fail(rel, errors.New("it was replaced while it was being read"))
		return
	}

	if err := dst.Mkdir(name, 0700); err != nil {
		w.stop(rel, err)
		return
	}
	subDst, err := dst.OpenRoot(name)
	if err != nil {
		w.stop(rel, err)
		return
	}
	defer subDst.Close()
	if !stillThere(dst, name, subDst) {
		w.stop(rel, errReplaced)
		return
	}

	var subPrev *os.Root
	if prev != nil {
		if old, err := prev.Lstat(name); err == nil && old.IsDir() {
			if subPrev, err = prev.OpenRoot(name); err == nil {
				defer subPrev.Close()
			} else {
				subPrev = nil
			}
		}
	}

	w.dir(sub, subDst, subPrev, rel)
	if w.fatal != nil {
		return
	}
	// The copy went into the folder that was opened. If its name leads
	// somewhere else by now, the tree is not the copy it should be.
	if !stillThere(dst, name, subDst) {
		w.stop(rel, errReplaced)
		return
	}
	// once its content is in place: writing into it changed its time
	if err := finishDir(subDst, info); err != nil {
		w.stop(rel, err)
	}
}

// file links the file to the previous snapshot when it did not change, and
// copies it otherwise.
func (w *walker) file(src *os.Root, dst *os.Root, prev *os.Root, name string, rel string, info os.FileInfo) {
	if prev != nil && w.store != nil && w.prevName != "" {
		if old, err := prev.Lstat(name); err == nil && unchanged(old, info) {
			// both ends named from the folder that holds the snapshots: the
			// link cannot be made from or to anything outside it
			if err := w.store.Link(filepath.Join(w.prevName, rel), filepath.Join(w.workName, rel)); err == nil {
				w.files++
				w.bytesLinked += info.Size()
				filesDone.Add(1)
				return
			}
			// for example the file system's limit of links to one file: copy instead
		}
	}

	if w.buffer == nil {
		w.buffer = make([]byte, 1<<20)
	}
	written, readErr, writeErr := copyFile(src, dst, name, info, w.buffer)
	switch {
	case writeErr != nil:
		w.stop(rel, writeErr)
	case readErr != nil:
		w.fail(rel, readErr)
	default:
		w.files++
		w.bytesCopied += written
		filesDone.Add(1)
	}
}

func (w *walker) symlink(src *os.Root, dst *os.Root, name string, rel string, info os.FileInfo) {
	target, err := src.Readlink(name)
	if err != nil {
		w.fail(rel, err)
		return
	}
	if err := dst.Symlink(target, name); err != nil {
		w.stop(rel, err)
		return
	}
	if err := setOwner(dst, name, info); err != nil {
		w.stop(rel, err)
		return
	}
	w.files++
	filesDone.Add(1)
}

// unchanged tells whether a file is the one already held by the previous
// snapshot: same size, modification time, mode and owner. The owner counts
// because a link shares it with the file it links to.
func unchanged(old os.FileInfo, now os.FileInfo) bool {
	if !old.Mode().IsRegular() || !now.Mode().IsRegular() {
		return false
	}
	oldUID, oldGID := owner(old)
	uid, gid := owner(now)
	return old.Size() == now.Size() &&
		old.ModTime().Equal(now.ModTime()) &&
		old.Mode() == now.Mode() &&
		oldUID == uid && oldGID == gid
}

// permissions are the mode bits chmod sets.
func permissions(info os.FileInfo) os.FileMode {
	return info.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
}

// owner returns the user and group of a file, or -1 (leave as it is) when
// the system does not tell.
func owner(info os.FileInfo) (uid int, gid int) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(stat.Uid), int(stat.Gid)
	}
	return -1, -1
}

// setOwner gives the entry called name in dir (not what it may link to) the
// owner of info. Without root rights owners cannot be set, and that is not
// reported: there is nothing the admin could do about it.
func setOwner(dir *os.Root, name string, info os.FileInfo) error {
	uid, gid := owner(info)
	if err := dir.Lchown(name, uid, gid); err != nil && os.Geteuid() == 0 {
		return ownerError(err)
	}
	return nil
}

func ownerError(err error) error {
	return fmt.Errorf("the owner of a file cannot be stored there: %s", reason(err))
}

// finishDir gives a copied folder the owner, mode and modification time of
// the original. It is told by its own handle, not by its name: it is the
// folder that was written to, whatever its name points to by now.
func finishDir(dir *os.Root, info os.FileInfo) error {
	if err := setOwner(dir, ".", info); err != nil {
		return err
	}
	if err := dir.Chmod(".", permissions(info)); err != nil {
		return err
	}
	return dir.Chtimes(".", info.ModTime(), info.ModTime())
}

// stillThere tells whether name, in parent, is still the real folder that
// was opened as dir, and not something put in its place.
func stillThere(parent *os.Root, name string, dir *os.Root) bool {
	named, errNamed := parent.Lstat(name)
	opened, errOpened := dir.Stat(".")
	return errNamed == nil && errOpened == nil && named.IsDir() && os.SameFile(named, opened)
}

var errReplaced = errors.New("it was replaced while it was being written")

// createTemp makes a new, empty file for a copy under way in dir.
func createTemp(dir *os.Root) (file *os.File, name string, err error) {
	for tries := 0; tries < 10; tries++ {
		name = ".cosmos-copy-" + strconv.FormatUint(rand.Uint64(), 36)
		file, err = dir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if !errors.Is(err, os.ErrExist) {
			return file, name, err
		}
	}
	return nil, "", err
}

// copyFile copies the regular file called name from the folder src to the
// folder dst, with its mode, owner and modification time. looked is what the
// file was when the folder was listed. The copy is written under a temporary
// name and renamed, so it is either complete or absent. A problem reading
// the source and a problem writing the target are reported apart: the first
// concerns one file, the second the whole run.
func copyFile(src *os.Root, dst *os.Root, name string, looked os.FileInfo, buffer []byte) (written int64, readErr error, writeErr error) {
	// O_NONBLOCK: a FIFO put in the file's place must not hold up the run.
	in, err := src.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return 0, err, nil
	}
	defer in.Close()
	// What was opened must be the file that was listed: a symbolic link put
	// in its place meanwhile would have been followed (os.Root follows links
	// that stay inside the folder, whatever the flags). From here on the
	// attributes are those of the open file.
	info, err := in.Stat()
	if err != nil {
		return 0, err, nil
	}
	if !info.Mode().IsRegular() || !os.SameFile(info, looked) {
		return 0, errors.New("it was replaced while it was being read"), nil
	}

	out, tmp, err := createTemp(dst)
	if err != nil {
		return 0, nil, err
	}
	defer func() {
		if readErr != nil || writeErr != nil {
			out.Close()
			dst.Remove(tmp)
		}
	}()

	for {
		n, err := in.Read(buffer)
		if n > 0 {
			if _, err := out.Write(buffer[:n]); err != nil {
				return written, nil, err
			}
			written += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return written, err, nil
		}
	}

	// owner before mode: changing the owner clears the set-user-ID bits
	uid, gid := owner(info)
	if err := out.Chown(uid, gid); err != nil && os.Geteuid() == 0 {
		return written, nil, ownerError(err)
	}
	if err := out.Chmod(permissions(info)); err != nil {
		return written, nil, err
	}
	if err := out.Close(); err != nil {
		return written, nil, err
	}
	if err := dst.Chtimes(tmp, info.ModTime(), info.ModTime()); err != nil {
		return written, nil, err
	}
	if err := dst.Rename(tmp, name); err != nil {
		return written, nil, err
	}
	return written, nil, nil
}

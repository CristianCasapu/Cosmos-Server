package utils

// Fork addition: the configuration file was truncated and rewritten in place, so a crash
// or a full disk in the middle of a save left it empty and the server could not start
// any more (upstream issue #591). It is now written beside the original and renamed.

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// renameFile is os.Rename, replaceable in the tests: a rename that the system refuses
// cannot be provoked there.
var renameFile = os.Rename

// writeFileAtomic replaces the file at path with what write produces, or leaves it as it
// was. The content goes to "<file>.tmp" in the same folder, is flushed to disk and renamed
// over the original, and the folder is flushed too. An existing file keeps its permissions
// and owner; a new one gets mode. A symbolic link is followed, as writing in place did.
//
// Where a file cannot be replaced at all (it is itself a mount point, or the folder is not
// writable) the file is written in place as before, so such a setup keeps working.
func writeFileAtomic(path string, mode os.FileMode, write func(io.Writer) error) error {
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}

	info, err := os.Stat(target)
	exists := err == nil
	if exists {
		mode = info.Mode().Perm()
	}

	tmpName := target + ".tmp"
	os.Remove(tmpName) // what a crash in an earlier save left behind

	tmp, err := os.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		if cannotReplace(err) {
			Warn("Cannot create " + tmpName + " (" + err.Error() + "), writing " + target + " in place")
			return writeFileInPlace(target, mode, write)
		}
		return err
	}

	discard := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}

	// exactly the mode asked for, whatever the umask
	if err := tmp.Chmod(mode); err != nil {
		return discard(err)
	}
	if exists {
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			tmp.Chown(int(stat.Uid), int(stat.Gid)) // same owner when allowed to
		}
	}

	if err := write(tmp); err != nil {
		return discard(err)
	}
	if err := tmp.Sync(); err != nil {
		return discard(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}

	if err := renameFile(tmpName, target); err != nil {
		os.Remove(tmpName)
		if cannotReplace(err) {
			Warn("Cannot replace " + target + " (" + err.Error() + "), writing it in place")
			return writeFileInPlace(target, mode, write)
		}
		return err
	}

	// the rename itself has to reach the disk
	if dir, err := os.Open(filepath.Dir(target)); err == nil {
		if err := dir.Sync(); err != nil {
			Warn("Cannot flush the folder of " + target + ": " + err.Error())
		}
		dir.Close()
	}

	return nil
}

// cannotReplace tells an error that says "this file cannot be swapped here" from one that
// would make writing in place fail as well (a full disk, an I/O error): only the first
// kind may fall back to the in-place write, which starts by emptying the file.
func cannotReplace(err error) bool {
	return errors.Is(err, os.ErrPermission) ||
		errors.Is(err, syscall.EBUSY) ||
		errors.Is(err, syscall.EXDEV) ||
		errors.Is(err, syscall.EROFS)
}

func writeFileInPlace(path string, mode os.FileMode, write func(io.Writer) error) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if err := write(file); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

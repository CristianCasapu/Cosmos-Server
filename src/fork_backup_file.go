package main

// Fork addition: how the configuration backup (backup.go) writes its file.
//
// The backup is a zip of Cosmos' own folder: the configuration with its
// private keys, the databases with the password hashes, the logs. Upstream
// creates it with os.Create, which means readable by every account of the
// machine unless the folder happens to be closed, under a fixed temporary
// name that is opened whatever it is. Here:
//
//   - the file is created for root alone (0600, whatever the umask), so the
//     finished cosmos-backup.zip is too;
//   - the temporary name is removed first and then created new: something
//     put there under that name (a link to another file, say) is replaced,
//     never written through;
//   - an entry that disappears while the folder is listed (a log being
//     rotated, a file being saved) is left out instead of ending the server.

import (
	"errors"
	"os"
	"syscall"
)

// createBackupFile is the hook of RunBackup that replaces os.Create.
func createBackupFile(path string) (*os.File, error) {
	// Remove takes away a link itself, not what it points to
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		os.Remove(path)
		return nil, err
	}
	return file, nil
}

// skipBackupEntry is the hook of RunBackup's listing: true for an entry that
// could not be read (filepath.Walk then passes no file information).
func skipBackupEntry(info os.FileInfo, err error) bool {
	return err != nil || info == nil
}

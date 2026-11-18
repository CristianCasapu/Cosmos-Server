package utils

// Fork addition (plan 47): the SQLite stores are private to the server.
//
// auth.db holds the administrators' password hashes, database.db the
// events and notifications. SQLite creates a database with the process's
// umask (0644 under systemd) and gives its -wal and -shm files the mode of
// the database. So the database file is made 0600 before SQLite first opens
// it, and an existing one (and its -wal, -shm) is tightened at every open.

import (
	"errors"
	"os"
)

// privateDatabaseFile prepares file for SQLite: created empty with 0600 when
// missing (never through a link), and 0600 with its -wal and -shm when
// present. Errors are logged: the store opens all the same.
func privateDatabaseFile(file string) {
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		f.Close()
	} else if !errors.Is(err, os.ErrExist) {
		Warn("Store: cannot create " + file + " as a private file: " + err.Error())
	}
	for _, name := range []string{file, file + "-wal", file + "-shm"} {
		info, err := os.Lstat(name)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() == 0o600 {
			continue
		}
		if err := os.Chmod(name, 0o600); err != nil {
			Warn("Store: cannot make " + name + " private: " + err.Error())
		}
	}
}

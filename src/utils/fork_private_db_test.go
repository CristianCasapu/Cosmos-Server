package utils

import (
	"os"
	"syscall"
	"testing"
)

// The administrators' store is private from its first write, whatever the
// umask; an existing store and its -wal/-shm are tightened when opened.
func TestTheStoresArePrivate(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)
	prev := CONFIGFOLDER
	CONFIGFOLDER = t.TempDir() + "/"
	defer func() { CloseStore(); CONFIGFOLDER = prev }()

	if err := InitStore(); err != nil {
		t.Fatal(err)
	}
	if err := CreateUser(User{Nickname: "admin", Password: "x", Role: ADMIN}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"auth.db", "auth.db-wal", "auth.db-shm"} {
		info, err := os.Stat(CONFIGFOLDER + name)
		if err != nil {
			continue
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s was made %o", name, info.Mode().Perm())
		}
	}
	CloseStore()
	os.Chmod(CONFIGFOLDER+"auth.db", 0o644)
	if err := InitStore(); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(CONFIGFOLDER + "auth.db"); info.Mode().Perm() != 0o600 {
		t.Errorf("an existing auth.db stayed %o", info.Mode().Perm())
	}
	// a link in the store's place is not followed by the creation
	CloseStore()
	other := t.TempDir() + "/other"
	os.WriteFile(other, nil, 0o644)
	os.Remove(CONFIGFOLDER + "database.db")
	os.Symlink(other, CONFIGFOLDER+"database.db")
	privateDatabaseFile(CONFIGFOLDER + "database.db")
	if info, _ := os.Stat(other); info.Mode().Perm() != 0o644 {
		t.Error("the target of a link was changed")
	}
}

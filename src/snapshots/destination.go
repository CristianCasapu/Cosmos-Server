package snapshots

// The snapshots hold copies of the files of every folder that is backed up,
// with their owners and modes. Whoever can write in the folder they are kept
// in can rename and replace what is there: snapshots that the retention then
// deletes, or a tree that a restore puts in place. So the destination, and
// the folder each source has in it, must belong to the user Cosmos runs as
// and be writable by that user only.

import (
	"fmt"
	"os"
	"os/user"
	"strconv"

	"github.com/azukaar/cosmos-server/src/utils"
)

// CheckDestination tells whether the folder info, which the admin knows as
// shown, may hold snapshots: it belongs to the user Cosmos runs as and nobody
// else can write in it. The answer is a sentence for the admin.
func CheckDestination(shown string, info os.FileInfo) error {
	if err := checkOwner(shown, info); err != nil {
		return err
	}
	if perm := info.Mode().Perm(); perm&0022 != 0 {
		return fmt.Errorf("%s can be changed by other users of the server (its permissions are %s). The snapshots hold copies of your files, so only %s may write there: remove the write permission of the group and of others (chmod go-w %s), or choose another folder", shown, modeText(perm), userName(os.Geteuid()), shown)
	}
	return nil
}

// checkOwner refuses a folder that does not belong to the user Cosmos runs as.
func checkOwner(shown string, info os.FileInfo) error {
	uid, _ := owner(info)
	if uid < 0 || uid == os.Geteuid() {
		return nil
	}
	return fmt.Errorf("%s belongs to %s, not to %s, the user Cosmos runs as. The snapshots hold copies of your files, so they must be kept in a folder no other user controls: choose another folder, or make %s its owner", shown, userName(uid), userName(os.Geteuid()), userName(os.Geteuid()))
}

// secureStore checks the folder that holds the snapshots of one source
// (<destination>/<slug>, opened as store): it must belong to the user Cosmos
// runs as. Cosmos creates it for that user alone; if it has been opened up
// since, it is closed again, through the handle, so nothing else is changed.
func secureStore(store *os.Root, shown string) error {
	info, err := store.Stat(".")
	if err != nil {
		return fmt.Errorf("%s cannot be read (%s)", shown, reason(err))
	}
	if err := checkOwner(shown, info); err != nil {
		return err
	}
	if perm := info.Mode().Perm(); perm&0077 != 0 {
		if err := store.Chmod(".", 0700); err != nil {
			return fmt.Errorf("%s can be read or changed by other users of the server (its permissions are %s) and could not be closed to them (%s)", shown, modeText(perm), reason(err))
		}
		utils.Warn("Snapshots: " + shown + " could be reached by other users of the server (permissions " + modeText(perm) + "); it is now for Cosmos only (700)")
	}
	return nil
}

// checkStoreForRestore refuses to restore from a folder of snapshots that
// another user controls. Nothing is changed: a restore only reads there.
func checkStoreForRestore(shown string, info os.FileInfo) error {
	if err := checkOwner(shown, info); err != nil {
		return err
	}
	if perm := info.Mode().Perm(); perm&0022 != 0 {
		return fmt.Errorf("%s can be changed by other users of the server (its permissions are %s), so what it holds cannot be trusted; run a backup, which closes it to them, and check the snapshots before restoring", shown, modeText(perm))
	}
	return nil
}

func modeText(perm os.FileMode) string {
	return fmt.Sprintf("%03o", uint32(perm))
}

// userName is "root", or "user 1000 (alice)" for another account.
func userName(uid int) string {
	if uid == 0 {
		return "root"
	}
	text := "user " + strconv.Itoa(uid)
	if account, err := user.LookupId(strconv.Itoa(uid)); err == nil && account.Username != "" {
		text += " (" + shownName(account.Username) + ")"
	}
	return text
}

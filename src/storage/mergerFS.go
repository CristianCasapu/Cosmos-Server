package storage 

import (
	"os"
	"errors"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Mount mounts a filesystem located at 'path' to 'mountpoint'.
func MountMergerFS(paths []string, mountpoint string, opts string, permanent bool, chown string) (err error) { // Fork addition: named result, for the undo below
	// Fork addition: the request is checked before anything is created, run or written (fork_mount_safety.go)
	if err := checkMergeRequest(paths, mountpoint, opts, chown); err != nil {
		return err
	}
	if err := forkCheckMerge(paths, mountpoint, opts); err != nil { return err } // Fork addition: data disks only, no overlap, a known policy (fork_mount_guard.go)

	utils.Log("[STORAGE] Merging " + strings.Join(paths, ":") + " into " + mountpoint)

	// Check if mountpoint exists
	if _, err := os.Stat(mountpoint); os.IsNotExist(err) {
		utils.Log("[STORAGE] Mountpoint does not exist, creating " + mountpoint)
		// Create the mountpoint if it does not exist
		if err := makeMountFolder(mountpoint, 0750); err != nil { // Fork addition: a variable for the tests (fork_storage_errors.go)
			return err
		}
	} else {
		utils.Log("[STORAGE] Mountpoint exists, checking if it is empty")
		// Check if mountpoint is empty
		dir, _ := os.Open(mountpoint)
		defer dir.Close()

		files, _ := dir.Readdirnames(1) // Or use Readdir to get FileInfo
		if len(files) > 0 {
			return errors.New("mountpoint is not empty")
		}
	}

	if len(opts) > 0 && opts[0] != ',' {
		opts = "," + opts
	}

	// Execute the mount command
	_, err = forkRun("Merging into "+mountpoint, "mergerfs", "-o", "use_ino,cache.files=partial,dropcacheonclose=true,allow_other,category.create=mfs" + opts, strings.Join(paths, ":"), mountpoint) // Fork addition: through runStorageCommand (fork_storage_errors.go)
	if err != nil {
		return err
	}
	defer forkUndoMountOnError(&err, mountpoint, "The merged folder") // Fork addition: a failure after the mount unmounts again (fork_mount_guard.go)

	utils.Log("[STORAGE] command: mergerfs -o use_ino,cache.files=partial,dropcacheonclose=true,allow_other,category.create=mfs" + opts + " " + strings.Join(paths, ":") + " " + mountpoint)

	// chown the mountpoint
	if chown != "" {
		utils.Log("[STORAGE] Chowning " + mountpoint + " to " + chown)
		out, err := forkRun("Setting the owner of "+mountpoint+" to "+chown, "chown", "--", chown, mountpoint) // Fork addition: through runStorageCommand (fork_storage_errors.go)
		utils.Debug(out)
		if err != nil {
			return err
		}
	}

	if permanent {
		utils.Log("[STORAGE] Adding mountpoint to /etc/fstab")
		
		// Check if mountpoint is already in /etc/fstab
		exists, err := isMountPointInFstab(mountpoint)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}

		// Format the fstab entry
		// Fork addition: with nofail, see fork_mount_safety.go
		fstabEntry := mergeFstabEntry(paths, mountpoint, opts)

		// Append to /etc/fstab
		file, err := os.OpenFile(fstabFile, os.O_APPEND|os.O_WRONLY, 0644) // Fork addition: a variable for the tests
		if err != nil {
			return err
		}
		defer file.Close()

		if _, err := file.WriteString(fstabEntry); err != nil {
			return err
		}

		utils.Log("[STORAGE] Added mountpoint to /etc/fstab")
	}

	return nil
}

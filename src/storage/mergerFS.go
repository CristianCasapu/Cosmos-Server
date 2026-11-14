package storage 

import (
	"os"
	"errors"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Mount mounts a filesystem located at 'path' to 'mountpoint'.
func MountMergerFS(paths []string, mountpoint string, opts string, permanent bool, chown string) error {
	// Fork addition: the request is checked before anything is created, run or written (fork_mount_safety.go)
	if err := checkMergeRequest(paths, mountpoint, opts, chown); err != nil {
		return err
	}

	utils.Log("[STORAGE] Merging " + strings.Join(paths, ":") + " into " + mountpoint)

	// Check if mountpoint exists
	if _, err := os.Stat(mountpoint); os.IsNotExist(err) {
		utils.Log("[STORAGE] Mountpoint does not exist, creating " + mountpoint)
		// Create the mountpoint if it does not exist
		if err := os.Mkdir(mountpoint, 0750); err != nil {
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
	_, err := utils.Exec("mergerfs", "-o", "use_ino,cache.files=partial,dropcacheonclose=true,allow_other,category.create=mfs" + opts, strings.Join(paths, ":"), mountpoint)
	if err != nil {
		return err
	}

	utils.Log("[STORAGE] command: mergerfs -o use_ino,cache.files=partial,dropcacheonclose=true,allow_other,category.create=mfs" + opts + " " + strings.Join(paths, ":") + " " + mountpoint)

	// chown the mountpoint
	if chown != "" {
		utils.Log("[STORAGE] Chowning " + mountpoint + " to " + chown)
		out, err := utils.Exec("chown", chown, mountpoint)
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
		file, err := os.OpenFile("/etc/fstab", os.O_APPEND|os.O_WRONLY, 0644)
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

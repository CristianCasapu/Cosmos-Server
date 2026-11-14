package storage

import (
	"os"
	"errors"
	"strings"
	"bufio"
	"fmt"

	"github.com/moby/sys/mountinfo"
	"github.com/azukaar/cosmos-server/src/utils"
)

type MountPoint struct {
	Path string `json:"path"`
	Permenant bool `json:"permenant"`
	Device string `json:"device"`
	Type string `json:"type"`
	Opts []string `json:"opts"`
}

// ListMounts lists all the mount points on the system
func ListMounts() ([]MountPoint, error) {
	utils.Log("[STORAGE] Listing all mount points")

	// List all mounted filesystems using moby/sys/mountinfo
	mountPoints, err := mountinfo.GetMounts(nil)
	if err != nil {
		return nil, err
	}

	// filter out the mount points that are not disks in a Set
	finalMountPoints := map[string]MountPoint{}
	dataMounts := forkDataMountSet(mountPoints) // Fork addition: data disks are listed wherever they are mounted
	for _, mp := range mountPoints {
		path := mp.Mountpoint
		if strings.HasPrefix(path, "/mnt/host") && utils.IsInsideContainer {
			// remove the host path
			path = strings.Replace(path, "/mnt/host", "", 1)
		}

		utils.Debug("[STORAGE] Checking if " + path + " is a disk")

		// if not proc or sys or dev or run
		if !strings.HasPrefix(path, "/mnt") &&
			 !strings.HasPrefix(path, "/var/mnt") && !dataMounts[path] {
			continue
		}

		// Skip WSL internal mounts
		if strings.HasPrefix(path, "/mnt/wsl") {
			continue
		}

		isPermenant, err := isMountPointInFstab(path)
		if err != nil {
			return nil, err
		}

		// Parse options string into slice
		opts := strings.Split(mp.Options, ",")

		finalMountPoints[path] = MountPoint{
			Device: mp.Source,
			Path: path,
			Type: mp.FSType,
			Opts: opts,
			Permenant: isPermenant,
		}
	}

	// use df -h to get the disk usage
	// TODO

	return utils.Values(finalMountPoints), nil
}



// Mount mounts a filesystem located at 'path' to 'mountpoint'.
func Mount(path, mountpoint string, permanent bool, netDisk bool, chown string) (err error) { // Fork addition: named result, for the undo below
	// Fork addition: the request is checked before anything is created, run or written (fork_mount_safety.go)
	if err := checkMountRequest(path, mountpoint, chown, netDisk); err != nil {
		return err
	}
	if err := forkCheckMountTarget(path, mountpoint, netDisk); err != nil { return err } // Fork addition: no mount over a mount point, no device in use (fork_mount_guard.go)

	utils.Log("[STORAGE] Mounting " + path + " to " + mountpoint)

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

	// Execute the mount command
	out, err := forkRun("Mounting "+path+" on "+mountpoint, "mount", "--", path, mountpoint) // Fork addition: through runStorageCommand (fork_storage_errors.go)
	utils.Debug(out)
	if err != nil {
		return err
	}
	defer forkUndoMountOnError(&err, mountpoint, path) // Fork addition: a failure after the mount unmounts again (fork_mount_guard.go)
	
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
		var fstabEntry string
		if netDisk {
			fstabEntry = fmt.Sprintf("\n%s %s nfs defaults,nofail,_netdev 0 0\n", path, mountpoint)
		} else {
			// Fork addition: by UUID and with nofail, see fork_mount_safety.go (upstream: "<device> <mountpoint> auto defaults 0 0")
			fstabEntry = localFstabEntry(path, mountpoint)
		}

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


// Unmount unmounts the filesystem at 'mountpoint'.
func Unmount(mountpoint string, permanent bool) error {
	// Fork addition: the folder is checked before it reaches a command or /etc/fstab (fork_mount_safety.go)
	if err := checkMountPoint(mountpoint); err != nil {
		return err
	}

	utils.Log("[STORAGE] Unmounting " + mountpoint)

	// Execute the umount command
	_, err := forkRun("Unmounting "+mountpoint, "umount", "--", mountpoint) // Fork addition: through runStorageCommand (fork_storage_errors.go)
	if err != nil {
		return err
	}

	isPermanent, _ := isMountPointInFstab(mountpoint)
	if !isPermanent || permanent {
		utils.Log("[STORAGE] Mountpoint is not in /etc/fstab or unmount is permanent, removing mountpoint")
		// Remove the mountpoint
		if err := removeMountFolder(mountpoint); err != nil { // Fork addition: a variable for the tests (fork_storage_errors.go)
			return err
		}
	}

	if permanent {
		utils.Log("[STORAGE] Removing mountpoint from /etc/fstab")

		// Remove entry from /etc/fstab if it exists
		if err := removeFstabEntry(mountpoint); err != nil {
			return err
		}
	}

	return nil
}


// isMountPointInFstab checks if the given mountpoint is already in /etc/fstab.
func isMountPointInFstab(mountpoint string) (bool, error) {
	mountpoint = strings.Replace(mountpoint, "/var/mnt/", "/mnt/", 1)

	file, err := os.Open(fstabFile) // Fork addition: a variable for the tests
	if err != nil {
		return false, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), " " + mountpoint + " ") {
			return true, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return false, err
	}

	return false, nil
}

// removeFstabEntry removes an entry for the specified mountpoint from /etc/fstab.
func removeFstabEntry(mountpoint string) error {
	mountpoint = strings.Replace(mountpoint, "/var/mnt/", "/mnt/", 1)

	file, err := os.Open(fstabFile) // Fork addition: a variable for the tests
	if err != nil {
		return err
	}
	defer file.Close()

	lines := []string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		
		isComment := strings.HasPrefix(line, "#")
		isEmpty := len(strings.TrimSpace(line)) == 0

		if !isEmpty && (!strings.Contains(line, " " + mountpoint + " ") || isComment) {
			lines = append(lines, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	utils.Debug("[STORAGE] Writing new /etc/fstab")

	return os.WriteFile(fstabFile, []byte(strings.Join(lines, "\n")), 0644) // Fork addition: a variable for the tests
}

// check if disk is mounted
func IsDiskMounted(diskPath string) (bool, error) {
	utils.Log("[STORAGE] Checking if disk " + diskPath + " is mounted")

	// Fork addition: the device or one of its partitions is the source of a mount;
	// upstream compared the mount point with the device path and never found one, see fork_format.go
	return forkDiskMounted(diskPath)
}
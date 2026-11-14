package storage

// Fork addition: a permanent mount must not be able to stop the server from
// booting, and what is written to /etc/fstab must be exactly one entry.
//
// Upstream wrote "<device> <mountpoint> auto defaults 0 0" with the device
// path as given (/dev/sdb1). Device names are handed out at boot in the
// order the disks answer: after a disk is added, removed or slow, /dev/sdb1
// can be another disk or none, and without "nofail" a missing device drops
// the boot into the emergency shell. The entry now names the file system by
// its UUID when it has one, and carries "nofail" with a short wait.
//
// The device, the mount point and the owner come from the request and went
// into the mount command and into /etc/fstab as they were: a space or a line
// break in them made another field or another entry, and a leading "-" an
// option of the command. They are checked first now, on the server (the
// form's own check for /mnt is repeated here: a form is not a guard).

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

var (
	mountOwnerPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}(:[A-Za-z0-9_][A-Za-z0-9_.-]{0,63})?$`)
	uuidPattern       = regexp.MustCompile(`^[0-9A-Fa-f-]{4,40}$`)
)

// deviceUUID returns the UUID of the file system on a device, "" when it has
// none or cannot be read. A variable so that the tests need no disk.
var deviceUUID = func(device string) string {
	out, err := utils.Exec("blkid", "-s", "UUID", "-o", "value", "--", device)
	if err != nil {
		return ""
	}
	uuid := strings.TrimSpace(out)
	if !uuidPattern.MatchString(uuid) {
		return ""
	}
	return uuid
}

// oneField reports whether a value can stand as one field of a command line
// and of an fstab entry: not empty, no white space or control character, no
// comment sign, and nothing a command would read as an option.
func oneField(value string) bool {
	if value == "" || len(value) > 1024 || strings.HasPrefix(value, "-") {
		return false
	}
	for _, c := range value {
		if c <= 0x20 || c == 0x7f || c == '#' || c == '\\' {
			return false
		}
	}
	return true
}

// checkMountRequest refuses a mount whose device, mount point or owner is not
// what it claims to be. It is called before anything is created or run.
func checkMountRequest(path string, mountpoint string, chown string, netDisk bool) error {
	if !oneField(path) {
		return errors.New("the device or share to mount is not valid: it must not be empty, start with \"-\", or contain spaces, \"#\" or \"\\\"")
	}
	if !netDisk && !strings.HasPrefix(path, "/") {
		return errors.New("the device to mount must be a path such as /dev/sdb1")
	}
	if !oneField(mountpoint) || !filepath.IsAbs(mountpoint) || filepath.Clean(mountpoint) != strings.TrimSuffix(mountpoint, "/") {
		return errors.New("the folder to mount on is not valid: it must be a plain path such as /mnt/disk1, without spaces or \"..\"")
	}
	clean := filepath.Clean(mountpoint)
	if !(strings.HasPrefix(clean, "/mnt/") || strings.HasPrefix(clean, "/var/mnt/")) {
		return errors.New("the folder to mount on must be below /mnt, for example /mnt/disk1")
	}
	if chown != "" && !mountOwnerPattern.MatchString(chown) {
		return errors.New("the owner must be a user name, or user:group")
	}
	return nil
}

// localFstabEntry is the /etc/fstab entry of a disk of this machine: by UUID
// when the file system has one, never able to stop the boot.
func localFstabEntry(path string, mountpoint string) string {
	source := path
	if uuid := deviceUUID(path); uuid != "" {
		source = "UUID=" + uuid
	}
	return "\n" + source + " " + mountpoint + " auto defaults,nofail,x-systemd.device-timeout=10s 0 0\n"
}

var mergeOptionsPattern = regexp.MustCompile(`^[A-Za-z0-9_.=,:/+-]{0,512}$`)

// checkMountPoint is checkMountRequest's rule for the folder alone (unmount).
func checkMountPoint(mountpoint string) error {
	if !oneField(mountpoint) || !filepath.IsAbs(mountpoint) || filepath.Clean(mountpoint) != strings.TrimSuffix(mountpoint, "/") {
		return errors.New("the mounted folder is not valid: it must be a plain path such as /mnt/disk1, without spaces or \"..\"")
	}
	return nil
}

// checkMergeRequest is checkMountRequest for a merged folder (MergerFS): each
// branch is one plain absolute path, the options are option characters only.
func checkMergeRequest(paths []string, mountpoint string, opts string, chown string) error {
	if len(paths) == 0 || len(paths) > 64 {
		return errors.New("choose between 1 and 64 folders to merge")
	}
	for _, path := range paths {
		if !oneField(path) || !filepath.IsAbs(path) || strings.Contains(path, ":") || strings.Contains(path, ",") {
			return errors.New("a folder to merge is not valid: it must be a plain path such as /mnt/disk1, without spaces, \":\" or \",\"")
		}
	}
	if !mergeOptionsPattern.MatchString(opts) {
		return errors.New("the MergerFS options may only hold letters, digits and . = , : / + - _")
	}
	// the merged folder follows the same rule as any mount point; the first
	// branch stands in for the device, which a merge does not have
	return checkMountRequest(paths[0], mountpoint, chown, false)
}

// mergeFstabEntry is the /etc/fstab entry of a merged folder. "nofail": a
// branch that is missing at boot must not stop the server from starting.
func mergeFstabEntry(paths []string, mountpoint string, opts string) string {
	return "\n" + strings.Join(paths, ":") + " " + mountpoint + " mergerfs use_ino,cache.files=partial,dropcacheonclose=true,allow_other,category.create=mfs,nofail" + opts + " 0 0\n"
}

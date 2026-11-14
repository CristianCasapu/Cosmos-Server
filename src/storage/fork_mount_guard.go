package storage

// Fork addition: mounting and merging refuse what would hide or break
// something, and never stop half way.
//
// Upstream mounted over a folder that already was a mount point (hiding the
// disk mounted there), passed a RAID or LVM member, a swap or a disk mounted
// elsewhere to mount, and merged any folders at all: the merged folder itself,
// the same folder twice, a merge or a network share. When setting the owner
// or writing /etc/fstab failed after the mount had worked, the disk stayed
// mounted and the panel said it failed. Now these are refused before anything
// runs, and a failure after the mount unmounts again and says so.

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

// folderMountedOn returns what is mounted on a folder, "" when nothing is.
func folderMountedOn(mounts []mountEntry, folder string) (string, bool) {
	clean := filepath.Clean(folder)
	resolved := clean
	if target, err := filepath.EvalSymlinks(clean); err == nil {
		resolved = target
	}
	for _, m := range mounts {
		if m.Mountpoint == clean || m.Mountpoint == resolved {
			return m.Source, true
		}
	}
	return "", false
}

func readMountState() ([]mountEntry, []string, error) {
	mounts, err := readMounts()
	if err != nil {
		return nil, nil, refuse(http.StatusInternalServerError, "STO026", "The mount table could not be read ("+cleanText(err.Error(), 200)+"), so nothing was mounted. Try again.")
	}
	swaps, err := readSwaps()
	if err != nil {
		return nil, nil, refuse(http.StatusInternalServerError, "STO026", "The swap table could not be read ("+cleanText(err.Error(), 200)+"), so nothing was mounted. Try again.")
	}
	return mounts, swaps, nil
}

func refuseMountedFolder(folder string, source string) error {
	return refuse(http.StatusConflict, "STO020", filepath.Clean(folder)+" is already a mount point (of "+cleanText(source, 200)+"): choose another folder, or unmount what is mounted there first.")
}

// forkCheckMountTarget refuses a mount on a folder that already is a mount
// point, and of a device that is mounted, a swap, part of a RAID array or a
// volume, or a whole disk holding partitions.
func forkCheckMountTarget(path string, mountpoint string, netDisk bool) error {
	mounts, swaps, err := readMountState()
	if err != nil {
		return err
	}
	if source, mounted := folderMountedOn(mounts, mountpoint); mounted {
		return refuseMountedFolder(mountpoint, source)
	}
	if netDisk {
		return nil
	}
	use := useOfDevice(path, mounts, swaps)
	switch use.Code {
	case "mounted":
		return refuse(http.StatusConflict, "STO021", path+" is already mounted at "+use.Detail+": unmount it there first, or use that folder.")
	case "swap":
		return refuse(http.StatusConflict, "STO022", path+" is in use as swap: turn the swap off first.")
	case "raid":
		return refuse(http.StatusConflict, "STO022", path+" is part of the RAID array "+use.Detail+": it cannot be mounted on its own; mount the array (/dev/"+use.Detail+") instead.")
	case "lvm":
		return refuse(http.StatusConflict, "STO022", path+" belongs to the LVM or encrypted volume "+use.Detail+": mount that volume (/dev/mapper/"+use.Detail+") instead.")
	case "holder":
		return refuse(http.StatusConflict, "STO022", path+" is used by "+use.Detail+": release it first.")
	}
	if parts := readPartitions(filepath.Base(resolveDevice(path))); len(parts) > 0 {
		return refuse(http.StatusBadRequest, "STO023", path+" holds partitions: mount one of them (for example /dev/"+cleanText(parts[0], 64)+") instead of the whole disk.")
	}
	return nil
}

// forkUndoMountOnError unmounts again when a step after the mount failed, and
// says so in the error. It is deferred right after the mount worked.
func forkUndoMountOnError(err *error, mountpoint string, what string) {
	if *err == nil {
		return
	}
	reason := strings.TrimRight(cleanText((*err).Error(), 600), ". ")
	if _, undo := forkRun("Unmounting "+mountpoint+" again", "umount", "--", mountpoint); undo != nil {
		*err = refuse(http.StatusInternalServerError, "STO025", reason+". Unmounting "+mountpoint+" again failed too ("+cleanText(undo.Error(), 300)+"): it is still mounted. Unmount it from the list once the cause is fixed.")
		return
	}
	*err = refuse(http.StatusInternalServerError, "STO024", reason+". "+what+" was unmounted again, so nothing is left half done: fix the cause and try again.")
}

// --- merges --------------------------------------------------------------

var mergeCreatePolicies = map[string]bool{"epmfs": true, "mfs": true, "lfs": true, "ff": true}

// forkCheckMerge refuses a merge of anything but 2 to 32 distinct folders on
// data disks, a merged folder that overlaps one of them or already is a mount
// point, and a create policy other than epmfs, mfs, lfs or ff.
func forkCheckMerge(paths []string, mountpoint string, opts string) error {
	if len(paths) < 2 || len(paths) > 32 {
		return refuse(http.StatusBadRequest, "STO040", "Choose between 2 and 32 folders to merge (you chose "+strconv.Itoa(len(paths))+").")
	}
	for _, opt := range strings.Split(opts, ",") {
		if policy, ok := strings.CutPrefix(strings.TrimSpace(opt), "category.create="); ok && !mergeCreatePolicies[policy] {
			return refuse(http.StatusBadRequest, "STO045", "The create policy \""+cleanText(policy, 40)+"\" is not offered: choose epmfs, mfs, lfs or ff.")
		}
	}

	mounts, _, err := readMountState()
	if err != nil {
		return err
	}
	if source, mounted := folderMountedOn(mounts, mountpoint); mounted {
		return refuseMountedFolder(mountpoint, source)
	}

	listed, infos, err := listStorageMounts()
	if err != nil {
		return refuse(http.StatusInternalServerError, "STO026", "The mounts of this server could not be read ("+cleanText(err.Error(), 200)+"), so nothing was merged. Try again.")
	}
	target := filepath.Clean(mountpoint)
	inside := insideContainer()
	seen := map[string]bool{}
	for _, path := range paths {
		branch := filepath.Clean(path)
		if seen[branch] {
			return refuse(http.StatusBadRequest, "STO041", branch+" is chosen twice: each folder can be merged once.")
		}
		seen[branch] = true
		if isUnder(branch, target) || isUnder(target, branch) {
			return refuse(http.StatusBadRequest, "STO044", branch+" and the merged folder "+target+" overlap: the merged folder must not be one of the folders it merges, nor inside or above one of them.")
		}

		holder := holdingMount(infos, localPath(branch, inside))
		var on *storageMount
		if holder != nil {
			key := mountKey(holder.Major, holder.Minor, holder.Mountpoint)
			for i := range listed {
				if mountKey(listed[i].major, listed[i].minor, listed[i].local) == key {
					on = &listed[i]
					break
				}
			}
		}
		switch {
		case on == nil:
			return refuse(http.StatusBadRequest, "STO042", branch+" is not on one of the data disks listed on the Storage page: choose folders on mounted data disks.")
		case on.Kind == "merge":
			return refuse(http.StatusBadRequest, "STO043", branch+" is on the merged folder "+on.Path+": merge the disks themselves, not a merge.")
		case on.Kind == "network":
			return refuse(http.StatusBadRequest, "STO043", branch+" is on the network share "+on.Path+": only local data disks can be merged.")
		case on.Kind == "system":
			return refuse(http.StatusBadRequest, "STO043", branch+" is on the system disk ("+on.Path+"): only data disks can be merged.")
		}
	}
	return nil
}

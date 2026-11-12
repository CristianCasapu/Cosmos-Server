package storage

// Fork addition: which mounted filesystems are the server's data disks.
//
// Upstream only knows the disks mounted under /mnt (and /var/mnt): anything
// mounted elsewhere, such as /storage or /srv/media, is missing from the
// Storage pages and gets no usage graph and no "disk full" alert. The decision
// is made here, in one place, from the filesystem type and the source device
// instead of the path, and the mount list (mounts.go) and the disk metrics
// (metrics/system.go) both ask this file.
//
// A data mount is a real, persistent filesystem: one on a block device, a
// network filesystem or a FUSE data filesystem. Pseudo and system filesystems,
// the boot partitions, everything Docker and other container runtimes mount,
// and second mounts of a device that is already listed are left out.

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/moby/sys/mountinfo"
)

// HostMount is one line of /proc/self/mountinfo, reduced to what the decision
// needs. Path is the mount point as the host names it.
type HostMount struct {
	Path   string
	Root   string // the directory of the filesystem mounted there; "/" is all of it
	FSType string
	Source string
	Major  int
	Minor  int
}

// Filesystems that hold no data of their own: kernel interfaces, memory-backed
// and layered filesystems, and read-only images (always "100% full").
var pseudoFilesystems = map[string]bool{
	"autofs": true, "bdev": true, "binfmt_misc": true, "bpf": true,
	"cgroup": true, "cgroup2": true, "configfs": true, "cramfs": true,
	"debugfs": true, "devpts": true, "devtmpfs": true, "efivarfs": true,
	"erofs": true, "fusectl": true, "hugetlbfs": true, "iso9660": true,
	"mqueue": true, "nfsd": true, "nsfs": true, "overlay": true,
	"pipefs": true, "proc": true, "pstore": true, "ramfs": true,
	"romfs": true, "rootfs": true, "rpc_pipefs": true, "securityfs": true,
	"selinuxfs": true, "sockfs": true, "squashfs": true, "sysfs": true,
	"tmpfs": true, "tracefs": true, "udf": true,
}

// Network and cluster filesystems: their source is a server, not a device.
var networkFilesystems = map[string]bool{
	"9p": true, "afs": true, "ceph": true, "cifs": true, "davfs": true,
	"glusterfs": true, "lustre": true, "ncpfs": true, "nfs": true,
	"nfs4": true, "smb3": true, "smbfs": true, "virtiofs": true,
}

// Filesystems that span devices and name a pool, not a device, as source.
var poolFilesystems = map[string]bool{
	"bcachefs": true, "zfs": true,
}

// FUSE filesystems that are desktop or container plumbing, not data.
var fuseHelpers = map[string]bool{
	"fuse.gvfsd-fuse": true, "fuse.lxcfs": true, "fuse.portal": true,
	"fuse.snapfuse": true, "fuse.vmware-vmblock": true,
}

// Directories whose mounts belong to the system or to container runtimes.
// Docker's own data root is added to these when it was moved.
var systemMountDirs = []string{
	"/proc", "/sys", "/dev", "/run", "/snap", "/var/snap", "/boot", "/efi",
	"/var/lib/docker", "/var/lib/containerd", "/var/lib/containers", "/var/lib/kubelet",
}

func isDataFilesystem(fsType string, source string) bool {
	if pseudoFilesystems[fsType] || fuseHelpers[fsType] {
		return false
	}
	if networkFilesystems[fsType] || poolFilesystems[fsType] || strings.HasPrefix(fsType, "fuse.") {
		return true
	}
	// everything else has to sit on a block device (ext4, xfs, btrfs, vfat, fuseblk for NTFS...)
	return strings.HasPrefix(source, "/dev/")
}

func isBelow(path string, dir string) bool {
	return dir != "" && dir != "/" && strings.HasPrefix(path, strings.TrimRight(dir, "/")+"/")
}

func isSystemMountPath(path string, dockerRoot string) bool {
	for _, dir := range systemMountDirs {
		if path == dir || isBelow(path, dir) {
			return true
		}
	}
	// a disk mounted AT Docker's data root is a data disk; what Docker mounts inside it is not
	return isBelow(path, dockerRoot)
}

// DataMounts returns the data disks among the host's mounts, sorted by path.
// When a device is mounted more than once (bind mounts, a second mount point)
// only one mount is kept: the one of the whole filesystem, then the shortest
// path.
func DataMounts(mounts []HostMount, dockerRoot string) []HostMount {
	byDevice := map[string]HostMount{}

	for _, mount := range mounts {
		if mount.Path == "" || !isDataFilesystem(mount.FSType, mount.Source) || isSystemMountPath(mount.Path, dockerRoot) {
			continue
		}

		device := strconv.Itoa(mount.Major) + ":" + strconv.Itoa(mount.Minor)
		if mount.Major == 0 && mount.Minor == 0 {
			device = mount.FSType + ":" + mount.Source
		}

		if kept, ok := byDevice[device]; !ok || preferMount(mount, kept) {
			byDevice[device] = mount
		}
	}

	result := make([]HostMount, 0, len(byDevice))
	for _, mount := range byDevice {
		result = append(result, mount)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}

func preferMount(candidate HostMount, kept HostMount) bool {
	if (candidate.Root == "/") != (kept.Root == "/") {
		return candidate.Root == "/"
	}
	if len(candidate.Path) != len(kept.Path) {
		return len(candidate.Path) < len(kept.Path)
	}
	return candidate.Path < kept.Path
}

// hostMounts turns the mount table into host paths. In a container the host
// is mounted at /mnt/host: only what is below it belongs to the host, the rest
// are the container's own mounts.
func hostMounts(infos []*mountinfo.Info, insideContainer bool) []HostMount {
	result := make([]HostMount, 0, len(infos))
	for _, info := range infos {
		if info == nil {
			continue
		}
		path := info.Mountpoint
		if insideContainer {
			if path != "/mnt/host" && !strings.HasPrefix(path, "/mnt/host/") {
				continue
			}
			path = strings.TrimPrefix(path, "/mnt/host")
			if path == "" {
				path = "/"
			}
		}
		result = append(result, HostMount{
			Path:   path,
			Root:   info.Root,
			FSType: info.FSType,
			Source: info.Source,
			Major:  info.Major,
			Minor:  info.Minor,
		})
	}
	return result
}

// dockerDataRoot reads the "data-root" Docker was configured with ("graph" in
// old versions); empty when it was not moved or the file cannot be read.
func dockerDataRoot(daemonConfig []byte) string {
	var settings struct {
		DataRoot string `json:"data-root"`
		Graph    string `json:"graph"`
	}
	if json.Unmarshal(daemonConfig, &settings) != nil {
		return ""
	}
	root := settings.DataRoot
	if root == "" {
		root = settings.Graph
	}
	if !strings.HasPrefix(root, "/") {
		return ""
	}
	return root
}

func forkDataMountSet(infos []*mountinfo.Info) map[string]bool {
	daemonConfig := "/etc/docker/daemon.json"
	if utils.IsInsideContainer {
		daemonConfig = "/mnt/host" + daemonConfig
	}
	content, _ := os.ReadFile(daemonConfig)

	set := map[string]bool{}
	for _, mount := range DataMounts(hostMounts(infos, utils.IsInsideContainer), dockerDataRoot(content)) {
		set[mount.Path] = true
	}
	return set
}

// ForkDataMountPaths returns the mount points of the host's data disks, for
// the callers that do not have the mount table at hand.
func ForkDataMountPaths() map[string]bool {
	infos, err := mountinfo.GetMounts(nil)
	if err != nil {
		utils.Error("[STORAGE] Listing the data disks", err)
		return map[string]bool{}
	}
	return forkDataMountSet(infos)
}

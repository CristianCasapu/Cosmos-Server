package storage

// Fork addition: what the storage forms can offer, and why not the rest.
//
// GET /api/storage/candidates answers, from lsblk and the mount table, every
// block device with its size, file system, label and model and whether it can
// be mounted (and if not, what holds it, as a code the form puts in words),
// and the data mounts the Storage page lists, with their size, free space,
// physical disk and kind (a disk, a merged folder, a network share, or the
// system). The mount, merge, unmount and parity checks of the server use the
// same list, so the forms and the server agree on what is allowed.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/moby/sys/mountinfo"
)

// deviceUse says what keeps a device from being mounted; Code "" when nothing.
type deviceUse struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

type storageDevice struct {
	Device     string    `json:"device"`
	Disk       string    `json:"disk"`
	Type       string    `json:"type"`
	Size       int64     `json:"size"`
	FSType     string    `json:"fstype"`
	Label      string    `json:"label"`
	UUID       string    `json:"uuid"`
	Model      string    `json:"model"`
	MountPoint string    `json:"mountpoint"`
	Use        deviceUse `json:"use"`
	Mountable  bool      `json:"mountable"`
}

type storageMount struct {
	Path      string `json:"path"`
	Device    string `json:"device"`
	Disk      string `json:"disk"`
	FSType    string `json:"fstype"`
	Size      uint64 `json:"size"`
	Free      uint64 `json:"free"`
	Permanent bool   `json:"permanent"`
	Kind      string `json:"kind"`

	local        string // the mount point in this server's namespace
	major, minor int
	deviceSize   int64 // the size lsblk gives the device, 0 when unknown
}

type storageView struct {
	Devices     []storageDevice `json:"devices"`
	Mounts      []storageMount  `json:"mounts"`
	Base        string          `json:"base"`
	InContainer bool            `json:"inContainer"`
}

// File system types that are there but cannot be mounted as a data disk.
var unmountableFilesystems = map[string]bool{
	"linux_raid_member": true, "LVM2_member": true, "crypto_LUKS": true,
	"zfs_member": true, "swap": true, "bcache": true, "ddf_raid_member": true,
	"isw_raid_member": true, "ceph_bluestore": true, "VMFS_volume_member": true,
}

// Network file systems served through FUSE.
var networkFuseFilesystems = map[string]bool{
	"fuse.rclone": true, "fuse.sshfs": true, "fuse.s3fs": true, "fuse.davfs": true,
}

// The machine, behind variables for the tests.
var (
	readMountTable = func() ([]*mountinfo.Info, error) { return mountinfo.GetMounts(nil) }
	readDockerConf = func() []byte {
		file := "/etc/docker/daemon.json"
		if utils.IsInsideContainer {
			file = "/mnt/host" + file
		}
		content, _ := os.ReadFile(file)
		return content
	}
	statFS = func(path string) (size uint64, free uint64, err error) {
		var st syscall.Statfs_t
		if err := syscall.Statfs(path, &st); err != nil {
			return 0, 0, err
		}
		return st.Blocks * uint64(st.Bsize), st.Bavail * uint64(st.Bsize), nil
	}
	mountInFstab    = func(path string) (bool, error) { return isMountPointInFstab(path) }
	configFolder    = func() string { return utils.CONFIGFOLDER }
	insideContainer = func() bool { return utils.IsInsideContainer }
)

// --- lsblk ---------------------------------------------------------------

var lsblkArgs = []string{"--json", "--bytes", "--paths", "-o", "NAME,PKNAME,TYPE,SIZE,FSTYPE,LABEL,UUID,MOUNTPOINT,MODEL"}

// lsblkSize is a size lsblk writes as a number (util-linux 2.33 and later)
// or as a string (older).
type lsblkSize int64

func (s *lsblkSize) UnmarshalJSON(data []byte) error {
	text := strings.Trim(string(data), `"`)
	if text == "" || text == "null" {
		*s = 0
		return nil
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return fmt.Errorf("size %q is not a number of bytes", cleanText(text, 30))
	}
	*s = lsblkSize(n)
	return nil
}

type lsblkNode struct {
	Name       string      `json:"name"`
	PKName     string      `json:"pkname"`
	Type       string      `json:"type"`
	Size       lsblkSize   `json:"size"`
	FSType     string      `json:"fstype"`
	Label      string      `json:"label"`
	UUID       string      `json:"uuid"`
	MountPoint string      `json:"mountpoint"`
	Model      string      `json:"model"`
	Children   []lsblkNode `json:"children"`
}

// lsblkDevice is a node with what the tree says about it.
type lsblkDevice struct {
	lsblkNode
	disk          string // the top of the tree: the physical disk
	hasPartitions bool
}

func readLsblk() ([]lsblkDevice, error) {
	out, err := runStorageCommand("lsblk", lsblkArgs...)
	if err != nil {
		return nil, commandFailure("Listing the disks", "lsblk", out, err)
	}
	return parseLsblk([]byte(out))
}

// parseLsblk flattens lsblk's tree. A device that sits on several (a RAID
// array, a volume spanning disks) appears under each: the first one counts.
func parseLsblk(data []byte) ([]lsblkDevice, error) {
	var tree struct {
		BlockDevices []lsblkNode `json:"blockdevices"`
	}
	if err := json.Unmarshal(data, &tree); err != nil {
		return nil, fmt.Errorf("the answer of lsblk could not be read: %v", cleanText(err.Error(), 200))
	}
	seen := map[string]bool{}
	var result []lsblkDevice
	var walk func(nodes []lsblkNode, disk string, depth int)
	walk = func(nodes []lsblkNode, disk string, depth int) {
		if depth > 8 {
			return
		}
		for _, node := range nodes {
			top := disk
			if top == "" {
				top = node.Name
			}
			if !seen[node.Name] && len(result) < 4096 {
				seen[node.Name] = true
				device := lsblkDevice{lsblkNode: node, disk: top}
				device.Children = nil
				for _, child := range node.Children {
					if child.Type == "part" {
						device.hasPartitions = true
					}
				}
				result = append(result, device)
			}
			walk(node.Children, top, depth+1)
		}
	}
	walk(tree.BlockDevices, "", 0)
	return result, nil
}

var blkidArgs = []string{"-c", "/dev/null", "-o", "export"}

// fillFromBlkid completes what lsblk could not say: without udev (a
// container, a minimal system) lsblk has no file system, label or UUID. One
// blkid run, fixed arguments, probes them all; its failure leaves them empty.
func fillFromBlkid(devices []lsblkDevice) {
	missing := false
	for _, d := range devices {
		missing = missing || (d.FSType == "" && !d.hasPartitions)
	}
	if !missing {
		return
	}
	out, _ := runStorageCommand("blkid", blkidArgs...)
	found := map[string]map[string]string{}
	var current map[string]string
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			current = nil
			continue
		}
		value = strings.ReplaceAll(value, `\`, "")
		if key == "DEVNAME" {
			current = map[string]string{}
			found[value] = current
			continue
		}
		if current != nil {
			current[key] = value
		}
	}
	for i, d := range devices {
		probe, ok := found[d.Name]
		if !ok || d.FSType != "" || d.hasPartitions {
			continue
		}
		devices[i].FSType = probe["TYPE"]
		if devices[i].UUID == "" {
			devices[i].UUID = probe["UUID"]
		}
		if devices[i].Label == "" {
			devices[i].Label = probe["LABEL"]
		}
	}
}

// --- what holds a device -------------------------------------------------

// useOfDevice says what holds a device itself (not its partitions): mounted,
// swap, RAID, LVM or another holder. "" when nothing does.
func useOfDevice(device string, mounts []mountEntry, swaps []string) deviceUse {
	canonical := resolveDevice(device)
	for _, m := range mounts {
		if strings.HasPrefix(m.Source, "/") && (m.Source == device || resolveDevice(m.Source) == canonical) {
			return deviceUse{Code: "mounted", Detail: cleanText(m.Mountpoint, 200)}
		}
	}
	for _, s := range swaps {
		if s == device || resolveDevice(s) == canonical {
			return deviceUse{Code: "swap", Detail: ""}
		}
	}
	for _, holder := range readHolders(filepath.Base(canonical)) {
		switch {
		case strings.HasPrefix(holder, "md"):
			return deviceUse{Code: "raid", Detail: cleanText(holder, 64)}
		case strings.HasPrefix(holder, "dm-"):
			label := holder
			if mapped := readMappedName(holder); mapped != "" {
				label = mapped
			}
			return deviceUse{Code: "lvm", Detail: cleanText(label, 64)}
		default:
			return deviceUse{Code: "holder", Detail: cleanText(holder, 64)}
		}
	}
	return deviceUse{}
}

func classifyDevice(d lsblkDevice, mounts []mountEntry, swaps []string) deviceUse {
	if use := useOfDevice(d.Name, mounts, swaps); use.Code != "" {
		return use
	}
	switch {
	case d.MountPoint == "[SWAP]":
		return deviceUse{Code: "swap"}
	case d.MountPoint != "":
		return deviceUse{Code: "mounted", Detail: cleanText(d.MountPoint, 200)}
	case d.hasPartitions:
		return deviceUse{Code: "has-partitions"}
	case d.FSType == "":
		return deviceUse{Code: "nofs"}
	case unmountableFilesystems[d.FSType]:
		return deviceUse{Code: "nofs", Detail: cleanText(d.FSType, 40)}
	}
	return deviceUse{}
}

// --- the data mounts -----------------------------------------------------

func localPath(path string, inside bool) string {
	if !inside {
		return path
	}
	if path == "/" {
		return "/mnt/host"
	}
	return "/mnt/host" + path
}

// holdingMount returns the mount of the table that holds a path (the
// deepest mount point equal to or above it).
func holdingMount(infos []*mountinfo.Info, path string) *mountinfo.Info {
	var best *mountinfo.Info
	for _, info := range infos {
		if info == nil {
			continue
		}
		if info.Mountpoint == path || info.Mountpoint == "/" || strings.HasPrefix(path, strings.TrimRight(info.Mountpoint, "/")+"/") {
			if best == nil || len(info.Mountpoint) >= len(best.Mountpoint) {
				best = info
			}
		}
	}
	return best
}

func mountKind(m HostMount, system bool) string {
	switch {
	case m.Path == "/" || system:
		return "system"
	case m.FSType == "fuse.mergerfs":
		return "merge"
	case networkFilesystems[m.FSType] || networkFuseFilesystems[m.FSType]:
		return "network"
	}
	return "data"
}

// mountKey names the file system of a mount: its device number, or for the
// ones without a device of their own (FUSE, network) the mount point.
func mountKey(major int, minor int, local string) string {
	if major == 0 {
		return "path:" + local
	}
	return strconv.Itoa(major) + ":" + strconv.Itoa(minor)
}

// listStorageMounts returns the data mounts the Storage page lists, with
// their kind, size and free space, and the whole mount table.
func listStorageMounts() ([]storageMount, []*mountinfo.Info, error) {
	infos, err := readMountTable()
	if err != nil {
		return nil, nil, fmt.Errorf("the mount table could not be read: %v", cleanText(err.Error(), 200))
	}
	inside := insideContainer()
	data := DataMounts(hostMounts(infos, inside), dockerDataRoot(readDockerConf()))

	systemKey := ""
	if holder := holdingMount(infos, filepath.Clean(configFolder())); holder != nil {
		systemKey = mountKey(holder.Major, holder.Minor, holder.Mountpoint)
	}

	result := make([]storageMount, 0, len(data))
	for _, m := range data {
		kind := mountKind(m, systemKey != "" && mountKey(m.Major, m.Minor, localPath(m.Path, inside)) == systemKey)
		entry := storageMount{
			Path:   m.Path,
			Device: cleanText(m.Source, 300),
			FSType: m.FSType,
			Kind:   kind,
			local:  localPath(m.Path, inside),
			major:  m.Major,
			minor:  m.Minor,
		}
		// a network share that does not answer would hold statfs, and the page, forever
		if kind != "network" {
			if size, free, err := statFS(entry.local); err == nil {
				entry.Size, entry.Free = size, free
			}
		}
		if permanent, err := mountInFstab(m.Path); err == nil {
			entry.Permanent = permanent
		}
		result = append(result, entry)
	}
	return result, infos, nil
}

// gatherStorage is the whole answer of the candidates route.
func gatherStorage() (storageView, error) {
	view := storageView{Base: "/mnt", InContainer: insideContainer(), Devices: []storageDevice{}}
	mountsList, _, err := listStorageMounts()
	if err != nil {
		return view, err
	}
	devices, err := readLsblk()
	if err != nil {
		return view, err
	}
	fillFromBlkid(devices)
	mountTable, err := readMounts()
	if err != nil {
		return view, fmt.Errorf("the mount table could not be read: %v", cleanText(err.Error(), 200))
	}
	swaps, err := readSwaps()
	if err != nil {
		return view, fmt.Errorf("the swap table could not be read: %v", cleanText(err.Error(), 200))
	}

	byPath := map[string]lsblkDevice{}
	for _, d := range devices {
		byPath[d.Name] = d
		byPath[resolveDevice(d.Name)] = d
		use := classifyDevice(d, mountTable, swaps)
		view.Devices = append(view.Devices, storageDevice{
			Device:     cleanText(d.Name, 200),
			Disk:       cleanText(d.disk, 200),
			Type:       cleanText(d.Type, 20),
			Size:       int64(d.Size),
			FSType:     cleanText(d.FSType, 40),
			Label:      cleanText(d.Label, 100),
			UUID:       cleanText(d.UUID, 64),
			Model:      cleanText(d.Model, 100),
			MountPoint: cleanText(d.MountPoint, 300),
			Use:        use,
			Mountable:  use.Code == "",
		})
	}
	for i, m := range mountsList {
		if !strings.HasPrefix(m.Device, "/") {
			continue
		}
		d, ok := byPath[m.Device]
		if !ok {
			d, ok = byPath[resolveDevice(m.Device)]
		}
		if ok {
			mountsList[i].Disk = cleanText(d.disk, 200)
			mountsList[i].deviceSize = int64(d.Size)
		}
	}
	view.Mounts = mountsList
	return view, nil
}

// findListedMount returns the listed mount with this path.
func findListedMount(mounts []storageMount, path string) (storageMount, bool) {
	clean := filepath.Clean(path)
	for _, m := range mounts {
		if m.Path == clean {
			return m, true
		}
	}
	return storageMount{}, false
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// StorageCandidatesRoute godoc
// @Summary The devices that can be mounted and the data mounts, for the storage forms
// @Tags Storage
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 500 {object} utils.HTTPErrorResult
// @Router /api/storage/candidates [get]
func StorageCandidatesRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_RESOURCES_READ) != nil {
		return
	}
	if req.Method != "GET" {
		utils.Error("StorageCandidatesRoute: Method not allowed "+cleanText(req.Method, 20), nil)
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	view, err := gatherStorage()
	if err != nil {
		utils.Error("StorageCandidatesRoute: "+cleanText(err.Error(), 500), nil)
		utils.HTTPError(w, "The disks of this server could not be read ("+cleanText(err.Error(), 300)+"). Try again; if it keeps failing, check that lsblk (util-linux) is installed.", http.StatusInternalServerError, "STO010")
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
		"data":   view,
	})
}

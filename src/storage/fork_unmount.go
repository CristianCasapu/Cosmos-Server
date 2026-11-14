package storage

// Fork addition: an unmount that knows what it unmounts.
//
// Upstream unmounted any folder it was given and took it out of /etc/fstab,
// "/" and /boot included, without a question; a disk that containers use was
// unmounted under them: their bind mounts live in their own mount namespace,
// so umount succeeds and the apps go on writing to a disk nobody sees, or to
// the empty folder after a restart.
//
// Now only a data mount the Storage page lists can be unmounted, never the
// system disk nor the one holding Cosmos' configuration. What uses the mount
// is looked up in /proc (open files, working directories and roots of the
// processes of this namespace, and the mount tables of the other namespaces:
// that is where a container's bind mount shows), and when something uses it,
// or the unmount is permanent, the request has to name the folder a second
// time ("confirm"), as the administrator typed it.

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

var procRoot = "/proc"

const (
	maxScannedProcesses = 4096
	maxMountUsers       = 20
	maxFdsPerProcess    = 8192
	maxReadLinks        = 262144
	maxMountinfoBytes   = 4 << 20
)

type mountUser struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
	How  string `json:"how"` // files, cwd, root or mount
}

func isUnder(path string, mountpoint string) bool {
	if mountpoint == "/" {
		return strings.HasPrefix(path, "/")
	}
	return path == mountpoint || strings.HasPrefix(path, mountpoint+"/")
}

// mountinfoLines reads a mountinfo file and calls fn with the fields of each
// line: the device number, the mount point and the optional fields.
func mountinfoLines(file string, fn func(device string, mountpoint string, optional []string) bool) {
	f, err := os.Open(file)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(io.LimitReader(f, maxMountinfoBytes))
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		// id parent major:minor root mountpoint options [optional...] - type source super
		if len(fields) < 7 {
			continue
		}
		var optional []string
		for _, field := range fields[6:] {
			if field == "-" {
				break
			}
			optional = append(optional, field)
		}
		if !fn(fields[2], unescapeMountinfo(fields[4]), optional) {
			return
		}
	}
}

// unescapeMountinfo undoes the octal escapes of mountinfo (\040 for a space).
func unescapeMountinfo(text string) string {
	if !strings.Contains(text, `\`) {
		return text
	}
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' && i+4 <= len(text) {
			if n, err := strconv.ParseUint(text[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(text[i])
	}
	return b.String()
}

func tagValue(optional []string, tag string) string {
	for _, field := range optional {
		if strings.HasPrefix(field, tag+":") {
			return strings.TrimPrefix(field, tag+":")
		}
	}
	return ""
}

// findMountUsers looks in /proc for what uses a mount: processes of this
// namespace with a file open, their working directory or their root on it,
// and one process of every other mount namespace whose mount table holds the
// same file system without receiving the unmount (a container's bind mount).
func findMountUsers(local string, major int, minor int) []mountUser {
	device := strconv.Itoa(major) + ":" + strconv.Itoa(minor)
	ownNS, _ := os.Readlink(filepath.Join(procRoot, "self", "ns", "mnt"))

	// the peer group of the mount here: a namespace that is its slave or its
	// peer receives the unmount, it does not hold the disk
	shared := ""
	mountinfoLines(filepath.Join(procRoot, "self", "mountinfo"), func(dev string, mountpoint string, optional []string) bool {
		if dev == device && mountpoint == local {
			shared = tagValue(optional, "shared")
			return false
		}
		return true
	})

	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	var pids []int
	for _, entry := range entries {
		if pid, err := strconv.Atoi(entry.Name()); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)
	if len(pids) > maxScannedProcesses {
		pids = pids[:maxScannedProcesses]
	}

	users := []mountUser{}
	seenNS := map[string]bool{}
	links := 0
	add := func(pid int, how string) {
		comm, _ := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "comm"))
		users = append(users, mountUser{PID: pid, Name: cleanText(string(comm), 32), How: how})
	}
	readlink := func(path string) string {
		links++
		target, err := os.Readlink(path)
		if err != nil {
			return ""
		}
		return target
	}

	for _, pid := range pids {
		if len(users) >= maxMountUsers || links >= maxReadLinks {
			break
		}
		dir := filepath.Join(procRoot, strconv.Itoa(pid))
		ns := readlink(filepath.Join(dir, "ns", "mnt"))

		if ns != "" && ns != ownNS {
			if seenNS[ns] {
				continue
			}
			seenNS[ns] = true
			holds := false
			mountinfoLines(filepath.Join(dir, "mountinfo"), func(dev string, mountpoint string, optional []string) bool {
				if dev != device {
					return true
				}
				if shared != "" && (tagValue(optional, "master") == shared || tagValue(optional, "shared") == shared) {
					return true
				}
				holds = true
				return false
			})
			if holds {
				add(pid, "mount")
			}
			continue
		}

		if root := readlink(filepath.Join(dir, "root")); root != "" && root != "/" && isUnder(root, local) {
			add(pid, "root")
			continue
		}
		if cwd := readlink(filepath.Join(dir, "cwd")); cwd != "" && isUnder(cwd, local) {
			add(pid, "cwd")
			continue
		}
		fds, err := os.ReadDir(filepath.Join(dir, "fd"))
		if err != nil {
			continue
		}
		for i, fd := range fds {
			if i >= maxFdsPerProcess || links >= maxReadLinks {
				break
			}
			if target := readlink(filepath.Join(dir, "fd", fd.Name())); strings.HasPrefix(target, "/") && isUnder(target, local) {
				add(pid, "files")
				break
			}
		}
	}
	return users
}

// --- the check before an unmount -----------------------------------------

type unmountCheck struct {
	MountPoint string      `json:"mountPoint"`
	Device     string      `json:"device"`
	Permanent  bool        `json:"permanent"`
	System     bool        `json:"system"`
	Users      []mountUser `json:"users"`
}

// checkUnmount finds the mount among the listed ones and what uses it.
func checkUnmount(mountpoint string) (unmountCheck, error) {
	if err := checkMountPoint(mountpoint); err != nil && mountpoint != "/" {
		return unmountCheck{}, refuse(http.StatusBadRequest, "STO030", "The folder to unmount must be a plain path such as /mnt/disk1.")
	}
	mounts, _, err := listStorageMounts()
	if err != nil {
		return unmountCheck{}, refuse(http.StatusInternalServerError, "STO013", "The mounts of this server could not be read ("+err.Error()+"). Try again.")
	}
	m, ok := findListedMount(mounts, mountpoint)
	if !ok {
		return unmountCheck{}, refuse(http.StatusNotFound, "STO012", filepath.Clean(mountpoint)+" is not one of the data disks listed on the Storage page: refresh the list and choose one of them. System folders such as /boot or /usr cannot be unmounted here.")
	}
	return unmountCheck{
		MountPoint: m.Path,
		Device:     m.Device,
		Permanent:  m.Permanent,
		System:     m.Kind == "system",
		Users:      findMountUsers(m.local, m.major, m.minor),
	}, nil
}

func describeUsers(users []mountUser) string {
	var names []string
	for _, u := range users {
		if len(names) == 3 {
			break
		}
		name := u.Name
		if name == "" {
			name = "process " + strconv.Itoa(u.PID)
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// checkUnmountRequest refuses an unmount of anything but a listed data
// mount, and one that is in use or permanent unless confirm names the folder.
func checkUnmountRequest(mountpoint string, permanent bool, confirm string) error {
	check, err := checkUnmount(mountpoint)
	if err != nil {
		if storageErrorCode(err) == "STO012" {
			return refuse(http.StatusBadRequest, "STO031", filepath.Clean(mountpoint)+" is not one of the data disks listed on the Storage page, so it cannot be unmounted here. System folders such as /boot or /usr are never unmounted from the panel.")
		}
		return err
	}
	if check.System {
		return refuse(http.StatusBadRequest, "STO032", check.MountPoint+" holds the system or Cosmos' own configuration: it cannot be unmounted from the panel.")
	}
	typed := strings.TrimSpace(confirm)
	matches := typed == check.MountPoint || (typed != "" && filepath.Clean(typed) == check.MountPoint)
	if typed != "" && !matches {
		return refuse(http.StatusBadRequest, "STO035", "What you typed does not match "+check.MountPoint+": type the folder exactly as shown to confirm.")
	}
	if !matches {
		if len(check.Users) > 0 {
			return refuse(http.StatusConflict, "STO033", check.MountPoint+" is in use ("+describeUsers(check.Users)+"). Stop the apps that use it first; to unmount it anyway, type "+check.MountPoint+" to confirm.")
		}
		if permanent {
			return refuse(http.StatusConflict, "STO034", "Unmounting "+check.MountPoint+" permanently also removes it from /etc/fstab: type "+check.MountPoint+" to confirm.")
		}
	}
	return nil
}

// forkUnmountChecked is what the unmount route runs: the checks, then the
// unmount itself.
func forkUnmountChecked(mountpoint string, permanent bool, confirm string) error {
	if err := checkUnmountRequest(mountpoint, permanent, confirm); err != nil {
		return err
	}
	return Unmount(filepath.Clean(mountpoint), permanent)
}

// StorageUnmountCheckRoute godoc
// @Summary What uses a data mount, before unmounting it
// @Tags Storage
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 404 {object} utils.HTTPErrorResult
// @Router /api/storage/unmount-check [post]
func StorageUnmountCheckRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_RESOURCES) != nil {
		return
	}
	if req.Method != "POST" {
		utils.Error("StorageUnmountCheckRoute: Method not allowed "+cleanText(req.Method, 20), nil)
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	var request struct {
		MountPoint string `json:"mountPoint"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || decoder.More() {
		utils.HTTPError(w, "The request is not valid: send only the folder to check, as {\"mountPoint\": \"/mnt/disk1\"}.", http.StatusBadRequest, "STO011")
		return
	}
	check, err := checkUnmount(request.MountPoint)
	if err != nil {
		forkHTTPError(w, err, "", http.StatusInternalServerError, "STO013")
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
		"data":   check,
	})
}

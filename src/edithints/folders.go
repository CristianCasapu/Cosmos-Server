package edithints

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
	"syscall"
)

// Folder is what is known of a bind's folder on the server.
type Folder struct {
	Source string `json:"source"`
	// State: ok, missing (Docker creates it when the container is made again),
	// file (a file, not a folder), outside (not under a data place: not looked at),
	// link (a link leads out of the data place: not looked at), invalid (not a clean absolute path),
	// unknown (it could not be read)
	State string `json:"state"`
	// Place is the data place the folder is in
	Place string `json:"place,omitempty"`
	// Owner is "uid:gid", Mode the permission bits in octal ("0755"); of the
	// nearest existing parent when the folder is missing
	Owner string `json:"owner,omitempty"`
	Mode  string `json:"mode,omitempty"`
	// Writable for the user the container runs as: yes, no, unknown
	Writable string `json:"writable,omitempty"`
	Free     uint64 `json:"free,omitempty"`
	Total    uint64 `json:"total,omitempty"`
	// LinkTo is where the path leads when a link is on its way (state link, or a link inside the place)
	LinkTo string `json:"linkTo,omitempty"`
}

const (
	maxLinks = 8
	maxSteps = 256
)

// CleanSource is the clean form of an absolute host path without a ".."
// part, or "" for anything else.
func CleanSource(source string) string {
	if !strings.HasPrefix(source, "/") || len(source) > 4096 {
		return ""
	}
	for _, part := range strings.Split(source, "/") {
		if part == ".." {
			return ""
		}
	}
	for _, r := range source {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return path.Clean(source)
}

// placeOf is the longest place that holds p, "" when none does. "/" is never a place.
func placeOf(p string, places []string) string {
	best := ""
	for _, place := range places {
		place = CleanSource(place)
		if place == "" || place == "/" {
			continue
		}
		if within(p, place) && len(place) > len(best) {
			best = place
		}
	}
	return best
}

// walk resolves p inside place one part at a time with Lstat, following a
// link only while its target stays inside place. It returns the resolved
// path (as seen on the server, without root), the state ("ok", "missing",
// "link", "unknown") and the information of the last existing part.
func walk(root, place, p string) (string, string, fs.FileInfo) {
	info, err := os.Lstat(root + place)
	if os.IsNotExist(err) {
		// a data path that is made by the first installation
		return place, "missing", nil
	}
	if err != nil || info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		// the place itself must be a real folder
		return place, "unknown", nil
	}
	current := place
	rest := strings.Split(strings.TrimPrefix(strings.TrimPrefix(p, place), "/"), "/")
	links := 0
	for steps := 0; len(rest) > 0; steps++ {
		if steps > maxSteps {
			return current, "link", nil
		}
		part := rest[0]
		rest = rest[1:]
		if part == "" || part == "." {
			continue
		}
		next := path.Join(current, part)
		nextInfo, err := os.Lstat(root + next)
		if os.IsNotExist(err) {
			return current, "missing", info
		}
		if err != nil {
			return current, "unknown", info
		}
		if nextInfo.Mode()&fs.ModeSymlink != 0 {
			links++
			target, err := os.Readlink(root + next)
			if err != nil || links > maxLinks {
				return current, "link", info
			}
			if !strings.HasPrefix(target, "/") {
				target = path.Join(current, target)
			}
			target = path.Clean(target)
			if !within(target, place) {
				return current, "link", info
			}
			// start again from the place with the link's target, then what was left
			rest = append(strings.Split(strings.TrimPrefix(strings.TrimPrefix(target, place), "/"), "/"), rest...)
			current = place
			info, _ = os.Lstat(root + place)
			continue
		}
		if len(rest) > 0 && !nextInfo.IsDir() {
			return next, "unknown", nextInfo
		}
		current, info = next, nextInfo
	}
	return current, "ok", info
}

func owner(info fs.FileInfo) (uint32, uint32, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return stat.Uid, stat.Gid, true
}

// canWrite says whether user can create files in a folder of that owner and
// mode ("yes", "no", "unknown"). Supplementary groups are not known: a "no"
// for a user that is not the owner may be wrong when the user is in the group.
func canWrite(info fs.FileInfo, user RunAs) string {
	if info == nil || !user.Known {
		return "unknown"
	}
	if user.UID == 0 {
		return "yes"
	}
	uid, gid, ok := owner(info)
	if !ok {
		return "unknown"
	}
	mode := info.Mode().Perm()
	switch {
	case uint32(user.UID) == uid:
		return yesNo(mode&0300 == 0300)
	case user.GID >= 0 && uint32(user.GID) == gid:
		return yesNo(mode&0030 == 0030)
	default:
		return yesNo(mode&0003 == 0003)
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// InspectFolder tells what is known of a bind's source folder. root is
// where the server's files are seen ("" normally, "/mnt/host" for a Cosmos
// in a container); places are the data places (the data path, the data
// disks): nothing outside them is looked at.
func InspectFolder(root, source string, places []string, user RunAs) Folder {
	folder := Folder{Source: source}
	p := CleanSource(source)
	if p == "" {
		folder.State = "invalid"
		return folder
	}
	place := placeOf(p, places)
	if place == "" {
		folder.State = "outside"
		return folder
	}
	folder.Place = place
	folder.LinkTo = LinkTarget(root, p)
	resolved, state, info := walk(root, place, p)
	folder.State = state
	if state == "link" || state == "unknown" && info == nil {
		return folder
	}
	if info != nil {
		if uid, gid, ok := owner(info); ok {
			folder.Owner = strconv.FormatUint(uint64(uid), 10) + ":" + strconv.FormatUint(uint64(gid), 10)
		}
		folder.Mode = "0" + strconv.FormatUint(uint64(info.Mode().Perm()), 8)
		if state == "ok" && !info.IsDir() {
			folder.State = "file"
		}
		if folder.State == "ok" {
			folder.Writable = canWrite(info, user)
		}
	}
	var fsStat syscall.Statfs_t
	if syscall.Statfs(root+resolved, &fsStat) == nil {
		folder.Free = fsStat.Bavail * uint64(fsStat.Bsize)
		folder.Total = fsStat.Blocks * uint64(fsStat.Bsize)
	}
	return folder
}

// ResolvePath follows a host path the way the kernel will when Docker mounts
// it: from "/", one part at a time with Lstat, each link replaced by its
// target (at most 40 links). It returns the path without links and whether
// a link was passed; the parts that do not exist yet are kept as they are.
// root is where the server's files are seen ("" normally).
func ResolvePath(root, p string) (string, bool, error) {
	p = CleanSource(p)
	if p == "" {
		return "", false, errors.New("not a clean absolute path")
	}
	current := "/"
	rest := strings.Split(strings.TrimPrefix(p, "/"), "/")
	linked, links := false, 0
	for steps := 0; len(rest) > 0; steps++ {
		if steps > 4096 {
			return "", linked, errors.New("too many parts")
		}
		part := rest[0]
		rest = rest[1:]
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			current = path.Dir(current)
			continue
		}
		next := path.Join(current, part)
		info, err := os.Lstat(root + next)
		if os.IsNotExist(err) {
			return path.Join(append([]string{next}, rest...)...), linked, nil
		}
		if err != nil {
			return "", linked, err
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			current = next
			continue
		}
		links++
		if links > 40 {
			return "", true, errors.New("too many links")
		}
		target, err := os.Readlink(root + next)
		if err != nil {
			return "", true, err
		}
		linked = true
		if strings.HasPrefix(target, "/") {
			current = "/"
		}
		rest = append(strings.Split(strings.Trim(target, "/"), "/"), rest...)
	}
	return current, linked, nil
}

// LinkTarget is where a path leads when a link is on its way, "" when none is.
func LinkTarget(root, p string) string {
	resolved, linked, err := ResolvePath(root, p)
	if err != nil || !linked {
		return ""
	}
	return resolved
}

// Package edithints holds the detections behind the proposals of a
// container's edit tabs (plan 47, delivery I2c): the server's devices that
// make sense in a container, its DNS resolvers and router, the facts of a
// data folder, the user a container writes its files as, a free host port
// and a memory limit read from a recipe's hint. Fork addition, the fork's own
// code (FORK-LICENCE.md).
//
// Every function works on a root folder given by the caller, so that the
// tests run against temporary trees and never assert anything about the
// machine they run on. Nothing here opens a device, lists a data folder or
// follows a link out of the place it was asked about.
package edithints

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Device is one device of the server a container may be given.
type Device struct {
	// Path on the server, as /dev/…: the stable name (/dev/serial/by-id/…) when there is one
	Path string `json:"path"`
	// Node is the device node itself (/dev/ttyUSB0), the usual path inside the container
	Node string `json:"node"`
	// Kind: render (graphics, for video transcoding), serial (a USB serial dongle: Zigbee, Z-Wave, …), video (a camera or capture card)
	Kind string `json:"kind"`
	// Name says what it is, from sysfs; "" when not known
	Name string `json:"name"`
	// Problem: "" or "name" (its path holds ":" or ",", which Docker's device setting cannot carry)
	Problem string `json:"problem,omitempty"`
}

// printableName: printable ASCII, no "/", at most n characters (a udev by-id name can be long).
func printableName(name string, n int) bool {
	if name == "" || len(name) > n || strings.Contains(name, "/") {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r >= 0x7f {
			return false
		}
	}
	return true
}

// Roots are the folders the device detection reads: /dev and /sys normally.
type Roots struct {
	Dev string
	Sys string
}

const (
	maxPerKind   = 32
	maxAttribute = 256
	maxName      = 80
)

// isCharDevice tells a device node from anything else. Replaced by the
// tests, which cannot make device nodes as an ordinary user.
var isCharDevice = func(info fs.FileInfo) bool {
	return info.Mode()&fs.ModeDevice != 0 && info.Mode()&fs.ModeCharDevice != 0
}

// readAttribute reads at most maxAttribute bytes of a sysfs attribute,
// cleaned to one printable line.
func readAttribute(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	data, _ := io.ReadAll(io.LimitReader(file, maxAttribute))
	return cleanName(string(data))
}

// cleanName keeps one line of printable ASCII, at most maxName characters.
func cleanName(text string) string {
	text = strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
	out := strings.Builder{}
	for _, r := range text {
		if r >= 0x20 && r < 0x7f {
			out.WriteRune(r)
		}
		if out.Len() >= maxName {
			break
		}
	}
	return strings.TrimSpace(out.String())
}

// within says whether p is dir or below it (both clean and absolute).
func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/")
}

// sysDevice resolves <sys>/class/<class>/<name>/device, staying inside sys.
func sysDevice(sys, class, name string) string {
	resolved, err := filepath.EvalSymlinks(filepath.Join(sys, "class", class, name, "device"))
	if err != nil {
		return ""
	}
	root, err := filepath.EvalSymlinks(sys)
	if err != nil || !within(resolved, root) {
		return ""
	}
	return resolved
}

var gpuVendors = map[string]string{"0x8086": "Intel", "0x1002": "AMD", "0x10de": "NVIDIA"}

func renderName(sys, name string) string {
	device := sysDevice(sys, "drm", name)
	if device == "" {
		return ""
	}
	vendor := gpuVendors[strings.ToLower(readAttribute(filepath.Join(device, "vendor")))]
	driver := ""
	if link, err := os.Readlink(filepath.Join(device, "driver")); err == nil {
		driver = cleanName(filepath.Base(link))
	}
	switch {
	case vendor != "" && driver != "":
		return vendor + " graphics (" + driver + ")"
	case vendor != "":
		return vendor + " graphics"
	case driver != "":
		return "Graphics (" + driver + ")"
	}
	return ""
}

// serialName is the USB device's manufacturer and product: the attributes
// are on the USB device, one to three folders above the tty's interface.
func serialName(sys, name string) string {
	device := sysDevice(sys, "tty", name)
	if device == "" {
		return ""
	}
	root, _ := filepath.EvalSymlinks(sys)
	for i, dir := 0, device; i < 4 && within(dir, root) && dir != root; i, dir = i+1, filepath.Dir(dir) {
		product := readAttribute(filepath.Join(dir, "product"))
		if product == "" {
			continue
		}
		manufacturer := readAttribute(filepath.Join(dir, "manufacturer"))
		if manufacturer != "" && !strings.Contains(strings.ToLower(product), strings.ToLower(manufacturer)) {
			return cleanName(manufacturer + " " + product)
		}
		return product
	}
	return ""
}

func videoName(sys, name string) string {
	return readAttribute(filepath.Join(sys, "class", "video4linux", name, "name"))
}

// byID maps a serial node's name (ttyUSB0) to its stable name in
// /dev/serial/by-id. Only the links are read; none is followed.
func byID(dev string) map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir(filepath.Join(dev, "serial", "by-id"))
	if err != nil {
		return out
	}
	for i, entry := range entries {
		if i >= 4*maxPerKind {
			break
		}
		link, err := os.Readlink(filepath.Join(dev, "serial", "by-id", entry.Name()))
		if err != nil {
			continue
		}
		node := filepath.Base(link)
		if _, taken := out[node]; !taken && printableName(entry.Name(), 255) {
			out[node] = "/dev/serial/by-id/" + entry.Name()
		}
	}
	return out
}

// Devices lists the render nodes, serial dongles and video devices of the server.
func Devices(roots Roots) []Device {
	found := []Device{}
	add := func(folder, prefix, kind string, name func(string) string, stable map[string]string) {
		entries, err := os.ReadDir(filepath.Join(roots.Dev, folder))
		if err != nil {
			return
		}
		names := []string{}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), prefix) && cleanName(entry.Name()) == entry.Name() {
				names = append(names, entry.Name())
			}
		}
		sort.Strings(names)
		count := 0
		for _, n := range names {
			if count >= maxPerKind {
				break
			}
			info, err := os.Lstat(filepath.Join(roots.Dev, folder, n))
			if err != nil || !isCharDevice(info) {
				continue
			}
			node := "/dev/" + filepath.ToSlash(filepath.Join(folder, n))
			device := Device{Path: node, Node: node, Kind: kind, Name: name(n)}
			if stable != nil && stable[n] != "" {
				device.Path = stable[n]
			}
			if strings.ContainsAny(device.Path, ":,") || strings.ContainsAny(device.Node, ":,") {
				device.Problem = "name"
			}
			found = append(found, device)
			count++
		}
	}
	add("dri", "renderD", "render", func(n string) string { return renderName(roots.Sys, n) }, nil)
	stable := byID(roots.Dev)
	add(".", "ttyUSB", "serial", func(n string) string { return serialName(roots.Sys, n) }, stable)
	add(".", "ttyACM", "serial", func(n string) string { return serialName(roots.Sys, n) }, stable)
	add(".", "video", "video", func(n string) string { return videoName(roots.Sys, n) }, nil)
	return found
}

package docker

// Fork addition (plan 47, I2c, review finding 2): a named volume whose
// "local" driver options bind a host path ({type: none, o: bind, device:
// /path}) is a host bind in disguise: Docker creates the volume with those
// options and mounts the path. It is treated as the bind it is (the same
// sensitive-access rule on the device path, fork_sensitive_access.go), and a
// bind with shared propagation (shared, rshared: what the container mounts
// inside reaches the server) needs the tick too. Other shapes of the local
// driver's options than a host folder, tmpfs or an NFS/CIFS share are refused
// in words, on the creation routes and on the edit.

import (
	"fmt"
	"strings"

	"github.com/docker/docker/api/types/mount"
)

func localDriver(m mount.Mount) (map[string]string, bool) {
	if m.Type != mount.TypeVolume || m.VolumeOptions == nil || m.VolumeOptions.DriverConfig == nil {
		return nil, false
	}
	name := strings.TrimSpace(m.VolumeOptions.DriverConfig.Name)
	if name != "" && name != "local" {
		return nil, false
	}
	return m.VolumeOptions.DriverConfig.Options, true
}

func optionWords(o string) []string {
	words := []string{}
	for _, w := range strings.Split(o, ",") {
		if w = strings.TrimSpace(w); w != "" {
			words = append(words, w)
		}
	}
	return words
}

// volumeBindDevice is the host path a volume's local driver binds, "" for none.
func volumeBindDevice(m mount.Mount) string {
	options, ok := localDriver(m)
	if !ok || len(options) == 0 {
		return ""
	}
	device := strings.TrimSpace(options["device"])
	kind := strings.TrimSpace(options["type"])
	binds := false
	for _, w := range optionWords(options["o"]) {
		if w == "bind" || w == "rbind" {
			binds = true
		}
	}
	if binds || kind == "none" || (kind == "" && strings.HasPrefix(device, "/")) {
		if device == "" {
			return "/"
		}
		return device
	}
	return ""
}

func sharedPropagation(m mount.Mount) bool {
	if m.BindOptions == nil {
		return false
	}
	p := m.BindOptions.Propagation
	return p == mount.PropagationShared || p == mount.PropagationRShared
}

// bindModeShared says whether a bind string's mode ("rw,rshared") asks for shared propagation.
func bindModeShared(mode string) bool {
	for _, w := range optionWords(mode) {
		if w == "shared" || w == "rshared" {
			return true
		}
	}
	return false
}

var shareTypes = map[string]bool{"nfs": true, "nfs4": true, "cifs": true, "smb3": true}

// checkVolumeOptions refuses a local driver's options that are not a host
// folder, tmpfs or a network share, and a host folder that is not a clean
// absolute path.
func checkVolumeOptions(m mount.Mount) error {
	options, ok := localDriver(m)
	if !ok || len(options) == 0 {
		return nil
	}
	for key := range options {
		if key != "type" && key != "o" && key != "device" {
			return fmt.Errorf("the volume %q: the local driver's option %q is not one Cosmos knows (type, o, device)", m.Source, key)
		}
	}
	kind := strings.TrimSpace(options["type"])
	device := strings.TrimSpace(options["device"])
	if device := volumeBindDevice(m); device != "" {
		if cleanAbsolute(device) == "" {
			return fmt.Errorf("the volume %q binds %q: give a folder of the server as an absolute path without \"..\"", m.Source, device)
		}
		return nil
	}
	switch {
	case kind == "tmpfs" && (device == "tmpfs" || device == ""):
		return nil
	case shareTypes[kind] && device != "":
		return nil
	}
	return fmt.Errorf("the volume %q: its driver options (type %q) are not a folder of the server (type=none, o=bind, device=/path), tmpfs or an NFS/CIFS share, which is all Cosmos takes", m.Source, kind)
}

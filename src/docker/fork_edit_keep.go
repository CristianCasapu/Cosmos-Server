package docker

// Fork addition (plan 47, I2c, review): a save of a tab sends back only what
// the tab shows; whatever the administrator did not change keeps the value the
// container has. The server does this, not the panel: the panel would have to
// round-trip every option Docker knows (bind propagation, SELinux labels,
// tmpfs size and mode, volume options, device permissions…), and any field it
// missed would be lost again on the next Docker release; the server compares
// what was sent with what is stored and keeps the stored value whenever they
// mean the same thing.
//
//   - a mount sent with the type, source and target the container has keeps
//     its stored options (and stays a "binds" string when it was one); only
//     its read-only flag follows the form;
//   - a device sent with the host and container paths it has keeps its
//     permissions (upstream reset them to rwm);
//   - a published port keeps its stored host address when the form's means the
//     same ("" and 0.0.0.0); ports that are only exposed stay exposed unless
//     the Network tab removes them (it sends the list);
//   - the network mode sent unchanged leaves the labels and the MAC address
//     as they were (upstream added cosmos-force-network-mode and cleared the
//     MAC address on every save of the Network tab).

import (
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"
)

type parsedBind struct {
	raw            string
	source, target string
	mode           string
}

func parseBinds(binds []string) []parsedBind {
	out := []parsedBind{}
	for _, b := range binds {
		parts := strings.SplitN(b, ":", 3)
		if len(parts) < 2 {
			continue
		}
		p := parsedBind{raw: b, source: parts[0], target: parts[1]}
		if len(parts) == 3 {
			p.mode = parts[2]
		}
		out = append(out, p)
	}
	return out
}

// withReadOnly is a bind string's mode with ro or rw as asked, its other words kept.
func withReadOnly(mode string, readOnly bool) string {
	words := []string{}
	for _, w := range optionWords(mode) {
		if w != "ro" && w != "rw" {
			words = append(words, w)
		}
	}
	if readOnly {
		words = append([]string{"ro"}, words...)
	} else if hasWord(mode, "rw") {
		words = append([]string{"rw"}, words...)
	}
	return strings.Join(words, ",")
}

func cleanTarget(t string) string {
	if c := cleanAbsolute(t); c != "" {
		return c
	}
	return t
}

// keepMounts rebuilds the binds and mounts of a container from the form's
// list, keeping what each kept entry had.
func keepMounts(before types.ContainerJSON, sent []mount.Mount) (binds []string, mounts []mount.Mount) {
	binds, mounts = []string{}, []mount.Mount{}
	if before.HostConfig == nil {
		return binds, sent
	}
	storedBinds := parseBinds(before.HostConfig.Binds)
	usedBind := make([]bool, len(storedBinds))
	usedMount := make([]bool, len(before.HostConfig.Mounts))
	for _, m := range sent {
		kept := false
		for i, s := range before.HostConfig.Mounts {
			if usedMount[i] || s.Type != m.Type || cleanMountSource(s) != cleanMountSource(m) || cleanTarget(s.Target) != cleanTarget(m.Target) {
				continue
			}
			// options sent with it are the caller's own and are used (second review M7); none sent: the stored ones
			if m.BindOptions != nil || m.VolumeOptions != nil || m.TmpfsOptions != nil {
				mounts = append(mounts, m)
				usedMount[i], kept = true, true
				break
			}
			s.ReadOnly = m.ReadOnly
			mounts = append(mounts, s)
			usedMount[i], kept = true, true
			break
		}
		if kept {
			continue
		}
		for i, b := range storedBinds {
			kind := mount.TypeVolume
			if strings.HasPrefix(b.source, "/") {
				kind = mount.TypeBind
			}
			if usedBind[i] || kind != m.Type || cleanMountSource(mount.Mount{Type: kind, Source: b.source}) != cleanMountSource(m) || cleanTarget(b.target) != cleanTarget(m.Target) ||
				m.BindOptions != nil || m.VolumeOptions != nil || m.TmpfsOptions != nil {
				continue
			}
			if hasWord(b.mode, "ro") == m.ReadOnly {
				binds = append(binds, b.raw)
			} else {
				mode := withReadOnly(b.mode, m.ReadOnly)
				if mode == "" {
					binds = append(binds, b.source+":"+b.target)
				} else {
					binds = append(binds, b.source+":"+b.target+":"+mode)
				}
			}
			usedBind[i], kept = true, true
			break
		}
		if !kept {
			mounts = append(mounts, m)
		}
	}
	return binds, mounts
}

func sameHostIP(a, b string) bool {
	norm := func(ip string) string {
		ip = strings.Trim(strings.TrimSpace(ip), "[]")
		if ip == "0.0.0.0" {
			return ""
		}
		return ip
	}
	return norm(a) == norm(b)
}

// keepUnchanged puts back what the form sent unchanged in its stored form.
func keepUnchanged(before types.ContainerJSON, after *types.ContainerJSON, form ContainerForm) {
	if before.HostConfig == nil || after.HostConfig == nil {
		return
	}
	// devices: their permissions, also when only the path in the container changes (never
	// widened silently, second review M2)
	for i, d := range after.HostConfig.Devices {
		for _, old := range before.HostConfig.Devices {
			if old.PathOnHost == d.PathOnHost && old.PathInContainer == d.PathInContainer {
				after.HostConfig.Devices[i].CgroupPermissions = old.CgroupPermissions
				break
			}
		}
		if after.HostConfig.Devices[i].CgroupPermissions != "rwm" {
			continue
		}
		for _, old := range before.HostConfig.Devices {
			if old.PathOnHost == d.PathOnHost {
				after.HostConfig.Devices[i].CgroupPermissions = old.CgroupPermissions
				break
			}
		}
	}
	// mounts: their options, and binds stay binds
	if form.Volumes != nil {
		after.HostConfig.Binds, after.HostConfig.Mounts = keepMounts(before, form.Volumes)
	}
	// ports: the stored host address, and the ports that are only exposed
	if form.PortBindings != nil {
		for port, bindings := range after.HostConfig.PortBindings {
			for i, b := range bindings {
				for _, old := range before.HostConfig.PortBindings[port] {
					if old.HostPort == b.HostPort && sameHostIP(old.HostIP, b.HostIP) {
						after.HostConfig.PortBindings[port][i].HostIP = old.HostIP
						break
					}
				}
			}
		}
		exposed := nat.PortSet{}
		if form.ExposedPorts != nil {
			for _, p := range form.ExposedPorts {
				if port, err := nat.NewPort(nat.SplitProtoPort(p)); err == nil {
					exposed[port] = struct{}{}
				}
			}
		} else if before.Config != nil {
			for p := range before.Config.ExposedPorts {
				exposed[p] = struct{}{}
			}
		}
		for p := range after.HostConfig.PortBindings {
			exposed[p] = struct{}{}
		}
		if after.Config != nil {
			after.Config.ExposedPorts = exposed
		}
	}
	// the "interactive" switch sent as the tab showed it (Tty and OpenStdin both on): both as they were
	// (a container run with -i and no -t lost its OpenStdin)
	if form.Interactive != 0 && before.Config != nil && after.Config != nil {
		if was := before.Config.Tty && before.Config.OpenStdin; (form.Interactive == 2) == was {
			after.Config.Tty, after.Config.OpenStdin = before.Config.Tty, before.Config.OpenStdin
		}
	}
	// the network mode sent as it was: labels and MAC address as they were
	if form.NetworkMode != "" && form.NetworkMode == string(before.HostConfig.NetworkMode) && after.Config != nil && before.Config != nil {
		old, had := before.Config.Labels["cosmos-force-network-mode"]
		if had {
			after.Config.Labels["cosmos-force-network-mode"] = old
		} else {
			delete(after.Config.Labels, "cosmos-force-network-mode")
		}
		after.Config.MacAddress = before.Config.MacAddress //nolint:staticcheck // deprecated field, kept as it was
	}
}

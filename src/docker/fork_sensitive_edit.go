package docker

// Fork addition (plan 40, security review F3): the tick for sensitive access
// on the edit route too. POST /api/servapps/{id}/update changes devices,
// volumes (binds), the network mode, … of an existing container; what the
// container gets that it did not have before needs the same explicit yes as
// at creation (`confirm=<container>|<kind>|<value>`, fork_sensitive_access.go),
// and what it already had is not asked again.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/mount"
)

// containerSensitive lists the sensitive access of a container's settings.
func containerSensitive(service string, c types.ContainerJSON, extra []string) []SensitiveItem {
	if c.HostConfig == nil {
		return nil
	}
	as := ContainerCreateRequestContainer{
		Privileged:  c.HostConfig.Privileged,
		NetworkMode: string(c.HostConfig.NetworkMode),
		CapAdd:      c.HostConfig.CapAdd,
		SecurityOpt: c.HostConfig.SecurityOpt,
	}
	if c.Config != nil {
		as.Labels = c.Config.Labels
	}
	seen := map[string]bool{}
	addBind := func(source string) {
		if source != "" && !seen[source] {
			seen[source] = true
			as.Volumes = append(as.Volumes, mount.Mount{Type: mount.TypeBind, Source: source})
		}
	}
	for _, m := range c.HostConfig.Mounts {
		if m.Type == mount.TypeBind {
			addBind(m.Source)
		}
		// Fork addition (plan 47, I2c review): a volume that binds a host path, shared propagation
		if volumeDeviceOf(m) != "" || sharedPropagation(m) {
			as.Volumes = append(as.Volumes, mount.Mount{Type: m.Type, Source: m.Source, VolumeOptions: m.VolumeOptions, BindOptions: m.BindOptions})
		}
	}
	for _, bind := range c.HostConfig.Binds {
		parts := strings.Split(bind, ":")
		// a named volume that binds a host path (second review S1)
		if len(parts) >= 2 && parts[0] != "" && !strings.HasPrefix(parts[0], "/") && existingVolumeDevice(parts[0]) != "" {
			as.Volumes = append(as.Volumes, mount.Mount{Type: mount.TypeVolume, Source: parts[0]})
		}
		if source := parts[0]; strings.HasPrefix(source, "/") {
			addBind(source)
			if len(parts) > 2 && bindModeShared(parts[2]) {
				as.Volumes = append(as.Volumes, mount.Mount{Type: mount.TypeBind, Source: source, BindOptions: &mount.BindOptions{Propagation: mount.PropagationRShared}})
			}
		}
	}
	for _, m := range c.Mounts {
		if m.Type == mount.TypeBind {
			addBind(m.Source)
		}
	}
	for _, device := range c.HostConfig.Devices {
		as.Devices = append(as.Devices, device.PathOnHost+":"+device.PathInContainer)
	}
	items := serviceSensitive(service, as, extra)
	unique := []SensitiveItem{}
	seenItem := map[string]bool{}
	for _, item := range items {
		if !seenItem[item.Key()] {
			seenItem[item.Key()] = true
			unique = append(unique, item)
		}
	}
	return unique
}

// writableSources lists the host folders a container's settings mount
// writable (Fork addition, plan 47 I2c review: a confirmed read-only folder
// made writable is asked again).
func writableSources(c types.ContainerJSON) map[string]bool {
	out := map[string]bool{}
	if c.HostConfig == nil {
		return out
	}
	for _, m := range c.HostConfig.Mounts {
		if m.Type == mount.TypeBind && !m.ReadOnly {
			out[cleanHostPath(m.Source)] = true
		}
	}
	for _, bind := range c.HostConfig.Binds {
		parts := strings.Split(bind, ":")
		if len(parts) >= 2 && strings.HasPrefix(parts[0], "/") && (len(parts) < 3 || !hasWord(parts[2], "ro")) {
			out[cleanHostPath(parts[0])] = true
		}
		// a named volume that binds a host path (second review M1)
		if len(parts) >= 2 && !strings.HasPrefix(parts[0], "/") && (len(parts) < 3 || !hasWord(parts[2], "ro")) {
			if device := existingVolumeDevice(parts[0]); device != "" {
				out[cleanHostPath(realPath(device))] = true
			}
		}
	}
	for _, m := range c.HostConfig.Mounts {
		if device := volumeDeviceOf(m); device != "" && !m.ReadOnly {
			out[cleanHostPath(realPath(device))] = true
		}
	}
	return out
}

func hasWord(mode, word string) bool {
	for _, w := range strings.Split(mode, ",") {
		if strings.TrimSpace(w) == word {
			return true
		}
	}
	return false
}

// unconfirmedEdit lists what `after` has that `before` had not and the request did not confirm.
func unconfirmedEdit(service string, before, after types.ContainerJSON, query url.Values, extra []string, extraItems ...SensitiveItem) []SensitiveItem {
	had := map[string]bool{}
	for _, item := range containerSensitive(service, before, extra) {
		had[item.Key()] = true
	}
	confirmed := map[string]bool{}
	values := query["confirm"]
	if len(values) > 64 {
		values = values[:64]
	}
	for _, value := range values {
		if len(value) <= 512 {
			confirmed[value] = true
		}
	}
	missing := []SensitiveItem{}
	wasWritable, isWritable := writableSources(before), writableSources(after)
	for _, item := range containerSensitive(service, after, extra) {
		// a folder or socket it had read-only and now gets writable is new access
		madeWritable := (item.Kind == "bind" || item.Kind == "socket") && isWritable[item.Value] && !wasWritable[item.Value]
		if (!had[item.Key()] || madeWritable) && !confirmed[item.Key()] {
			missing = append(missing, item)
		}
	}
	for _, item := range extraItems {
		if !confirmed[item.Key()] {
			missing = append(missing, item)
		}
	}
	return missing
}

// forkRefuseUnconfirmedEdit answers 409 with the items to confirm, and true,
// when the edit gives the container sensitive access without a yes.
func forkRefuseUnconfirmedEdit(w http.ResponseWriter, req *http.Request, before, after types.ContainerJSON, extraItems ...SensitiveItem) bool {
	service := strings.TrimPrefix(before.Name, "/")
	missing := unconfirmedEdit(service, before, after, req.URL.Query(), extraSensitiveDirs(), extraItems...)
	if len(missing) == 0 {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "error",
		"code":    "DS010",
		"message": "This change gives the container access to the server that needs an explicit confirmation: " + sensitiveSentence(missing) + ". Nothing was changed.",
		"data":    map[string]interface{}{"sensitive": missing},
	})
	return true
}

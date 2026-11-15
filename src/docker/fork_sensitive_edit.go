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
	}
	for _, bind := range c.HostConfig.Binds {
		if source := strings.Split(bind, ":")[0]; strings.HasPrefix(source, "/") {
			addBind(source)
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
	return serviceSensitive(service, as, extra)
}

// unconfirmedEdit lists what `after` has that `before` had not and the request did not confirm.
func unconfirmedEdit(service string, before, after types.ContainerJSON, query url.Values, extra []string) []SensitiveItem {
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
	for _, item := range containerSensitive(service, after, extra) {
		if !had[item.Key()] && !confirmed[item.Key()] {
			missing = append(missing, item)
		}
	}
	return missing
}

// forkRefuseUnconfirmedEdit answers 409 with the items to confirm, and true,
// when the edit gives the container sensitive access without a yes.
func forkRefuseUnconfirmedEdit(w http.ResponseWriter, req *http.Request, before, after types.ContainerJSON) bool {
	service := strings.TrimPrefix(before.Name, "/")
	missing := unconfirmedEdit(service, before, after, req.URL.Query(), extraSensitiveDirs())
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

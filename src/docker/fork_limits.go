package docker

// Fork addition: a container update only changes the memory and CPU limits it
// carries.
//
// Upstream read "no memLimit" and "no cpus" as "remove the limit", so every
// update that sent something else (the Network, Volumes and URL forms send
// only their own fields) silently removed the limits of the container.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	containerType "github.com/docker/docker/api/types/container"
	"github.com/docker/go-units"
)

// maxContainerFormBytes bounds what is read of a container update.
const maxContainerFormBytes = 8 << 20

// sentLimits says which limits a container update carries.
type sentLimits struct {
	memory bool
	cpus   bool
}

// readSentLimits looks at the body of a container update for the limits it
// carries and leaves the body readable for the decoding that follows. A field
// that is missing or null is not carried.
func readSentLimits(req *http.Request) sentLimits {
	sent := sentLimits{}
	if req == nil || req.Body == nil {
		return sent
	}

	body, err := io.ReadAll(io.LimitReader(req.Body, maxContainerFormBytes))
	req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		return sent
	}

	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return sent
	}
	carried := func(name string) bool {
		value, found := fields[name]
		return found && string(bytes.TrimSpace(value)) != "null"
	}
	sent.memory = carried("memLimit")
	sent.cpus = carried("cpus")
	return sent
}

// applyResourceLimits writes the limits of the form into the container's
// resources: a limit the request carries is set, or removed when it is empty
// or zero, and a limit it does not carry stays as it is.
func applyResourceLimits(resources *containerType.Resources, form ContainerForm, sent sentLimits) error {
	if sent.memory {
		if form.MemLimit != "" {
			memory, err := units.RAMInBytes(form.MemLimit)
			if err != nil {
				return errors.New("Invalid memory limit: " + err.Error())
			}
			resources.Memory = memory
		} else {
			resources.Memory = 0
			resources.MemorySwap = 0
		}
	}

	if sent.cpus {
		if form.CPUs > 0 {
			resources.NanoCPUs = int64(form.CPUs * 1e9)
		} else {
			resources.NanoCPUs = 0
		}
	}

	return nil
}

package docker

// Fork addition (plan 43): where the reverse proxy may send a request for a
// container, or why it may not. Upstream's GetContainerIPByName logs every
// failure and answers "" with the error; the Director then dialed ":<port>",
// that is this server itself, and when that port is Cosmos' own the request
// came back to Cosmos for the same route, again and again (thousands of
// requests a minute from 127.0.0.1). This answers the container's state
// with the address, never logs, and remembers a failure for a moment so
// that a burst of requests to a stopped app asks Docker once.

import (
	"errors"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
)

// TargetState says why a container has no address to dial.
type TargetState string

const (
	TargetRunning    TargetState = "running"
	TargetRestarting TargetState = "restarting"
	TargetStopped    TargetState = "stopped" // created, exited, paused, dead
	TargetMissing    TargetState = "missing" // no such container
	TargetNoAddress  TargetState = "noAddress"
	TargetUnknown    TargetState = "unknown" // Docker could not be asked
)

// ErrTargetDown is answered with every state but running.
var ErrTargetDown = errors.New("the container has no address to dial")

const (
	targetGoodFor = 10 * time.Second // as upstream's cache of addresses
	targetBadFor  = 2 * time.Second
	targetCached  = 512
)

type targetEntry struct {
	ip      string
	state   TargetState
	expires time.Time
}

var (
	targetMu    sync.Mutex
	targetCache = map[string]targetEntry{}
)

// inspectForTarget is a variable so that tests answer for Docker.
var inspectForTarget = func(name string) (types.ContainerJSON, error) {
	if err := Connect(); err != nil {
		return types.ContainerJSON{}, err
	}
	return DockerClient.ContainerInspect(DockerContext, name)
}

// ContainerTarget answers the address to dial for a container and its state.
// The address is never empty, unspecified or a gateway when err is nil; it
// is "127.0.0.1" only for a container on the host's network.
func ContainerTarget(name string) (string, TargetState, error) {
	now := time.Now()
	targetMu.Lock()
	if e, ok := targetCache[name]; ok && now.Before(e.expires) {
		targetMu.Unlock()
		if e.state != TargetRunning {
			return "", e.state, ErrTargetDown
		}
		return e.ip, e.state, nil
	}
	targetMu.Unlock()

	ip, state := resolveTarget(name, 0)
	keep := targetGoodFor
	if state != TargetRunning {
		keep = targetBadFor
	}
	targetMu.Lock()
	if len(targetCache) >= targetCached {
		for k, e := range targetCache {
			if now.After(e.expires) {
				delete(targetCache, k)
			}
		}
		if len(targetCache) >= targetCached {
			targetCache = map[string]targetEntry{}
		}
	}
	targetCache[name] = targetEntry{ip: ip, state: state, expires: now.Add(keep)}
	targetMu.Unlock()
	if state != TargetRunning {
		return "", state, ErrTargetDown
	}
	return ip, state, nil
}

// ForgetTarget drops what is known about a container (it was started or recreated).
func ForgetTarget(name string) {
	targetMu.Lock()
	delete(targetCache, name)
	targetMu.Unlock()
}

func resolveTarget(name string, depth int) (string, TargetState) {
	if name == "" || depth > 3 {
		return "", TargetMissing
	}
	c, err := inspectForTarget(name)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such container") {
			return "", TargetMissing
		}
		return "", TargetUnknown
	}
	return targetOf(c, depth)
}

// targetOf reads the address from an inspected container.
func targetOf(c types.ContainerJSON, depth int) (string, TargetState) {
	if c.ContainerJSONBase == nil || c.State == nil {
		return "", TargetUnknown
	}
	if c.State.Restarting {
		return "", TargetRestarting
	}
	if !c.State.Running || c.State.Paused {
		return "", TargetStopped
	}
	mode := ""
	if c.HostConfig != nil {
		mode = string(c.HostConfig.NetworkMode)
	}
	if mode == "host" {
		return "127.0.0.1", TargetRunning
	}
	if strings.HasPrefix(mode, "container:") {
		return resolveTarget(strings.TrimPrefix(mode, "container:"), depth+1)
	}
	if c.NetworkSettings == nil {
		return "", TargetNoAddress
	}
	usable := func(text string) string {
		ip := net.ParseIP(text)
		if ip == nil || ip.IsUnspecified() || ip.IsLoopback() {
			return ""
		}
		return ip.String()
	}
	// the network the container is in by its mode first, then any, by name
	if n, ok := c.NetworkSettings.Networks[mode]; ok && n != nil {
		if ip := usable(n.IPAddress); ip != "" {
			return ip, TargetRunning
		}
	}
	names := make([]string, 0, len(c.NetworkSettings.Networks))
	for n := range c.NetworkSettings.Networks {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if s := c.NetworkSettings.Networks[n]; s != nil {
			if ip := usable(s.IPAddress); ip != "" {
				return ip, TargetRunning
			}
		}
	}
	return "", TargetNoAddress
}

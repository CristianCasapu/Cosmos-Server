package docker

// Fork addition: small corrections to container handling, kept in one file so
// the upstream files only carry one-line hooks.

import (
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/errdefs"
)

// stopTimeout turns a compose stop_grace_period (seconds) into Docker's
// StopTimeout. Upstream passed the value as is, so every container created
// without a grace period got a timeout of 0: Docker then kills it on stop
// instead of letting it shut down (upstream issue #626). No value now means
// Docker's default of 10 seconds.
func stopTimeout(seconds int) *int {
	if seconds <= 0 {
		return nil
	}
	return &seconds
}

// healStopTimeout drops a stop timeout of 0 from a container that is being
// recreated, so containers created before the fix above get Docker's default
// the next time they are edited or updated.
func healStopTimeout(config *types.ContainerJSON) {
	if config == nil || config.Config == nil || config.Config.StopTimeout == nil {
		return
	}
	if *config.Config.StopTimeout <= 0 {
		config.Config.StopTimeout = nil
	}
}

// containerGone reports that an operation failed because the container was
// removed meanwhile, as happens with short-lived containers started with
// --rm that exit while they are still listed in the UI.
func containerGone(err error) bool {
	if err == nil {
		return false
	}
	if errdefs.IsNotFound(err) {
		return true
	}
	message := err.Error()
	return strings.Contains(message, "is already in progress") || strings.Contains(message, "No such container")
}

package docker

// Fork addition: what the container code reports to the security record
// (utils/fork_security.go, package security).

import (
	"net/http"

	"github.com/azukaar/cosmos-server/src/utils"
)

// containerActions are the actions of the manage route that are recorded
// under "container.action"; "remove" has a kind of its own.
var containerActions = map[string]bool{
	"stop": true, "start": true, "restart": true, "kill": true,
	"pause": true, "unpause": true, "recreate": true, "update": true,
}

// auditContainerAction records what the manage route is asked to do to a
// container.
func auditContainerAction(req *http.Request, container string, action string) {
	switch {
	case action == "remove":
		utils.AuditSecurity(req, utils.SecContainerDeleted, container)
	case containerActions[action]:
		utils.AuditSecurity(req, utils.SecContainerAction, container, "action", action)
	}
}

// recordTerminal records that a terminal of a container is opened: a new
// shell, or the container's own output and input ("attach"), which may be
// read-only. Recorded when it opens, not when it closes: a session can last
// for hours.
func recordTerminal(req *http.Request, container string, action string, readOnly bool) {
	mode := "attach"
	if action == "new" {
		mode = "new"
	} else if readOnly {
		mode = "attach_readonly"
	}
	utils.RecordSecurity(req, utils.SecContainerTerminal, container, "mode", mode)
}

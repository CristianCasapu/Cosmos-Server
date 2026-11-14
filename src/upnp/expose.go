package upnp

// What the "expose to the internet" action (src/cloudflare/expose.go) needs
// from the router port forwarding: the same checks as POST /api/upnp/apps,
// the choice list changed the same way, and a pass afterwards. That action
// writes the configuration itself, in one go with the routes and the
// Cloudflare choices.

import (
	"fmt"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

// MaxAppPorts is how many app ports Cosmos keeps open on the router.
const MaxAppPorts = maxAppPorts

// ContainerNameValid applies Docker's rule for container names.
func ContainerNameValid(name string) bool { return containerNameRule.MatchString(name) }

// ListContainersNow lists the containers and what they publish.
func ListContainersNow() ([]Container, error) { return listContainers() }

// ChoiceKey identifies the choice of one published port.
func ChoiceKey(container string, port int, protocol string) string {
	return appKey(container, port, protocol)
}

// CheckOpen tells whether a published port may be opened on the router,
// with the HTTP status, code and sentence of a refusal; ok when it may.
func CheckOpen(container string, port int, protocol string, externalPort int, scheme string, cfg utils.HTTPConfig, containers []Container) (status int, code string, message string, ok bool) {
	request := appPortRequest{
		Container: container, Port: port, Protocol: strings.ToLower(protocol), Enabled: true,
		ExternalPort: externalPort, Scheme: scheme,
		Confirm: fmt.Sprintf("%s:%d/%s", container, port, strings.ToLower(protocol)),
	}
	if refused := checkAppPort(request, cfg, containers, cfg.UPnP.AppPorts); refused != nil {
		return refused.status, refused.code, refused.message, false
	}
	return 0, "", "", true
}

// IsReserved reports whether an external port is on the list Cosmos never
// asks a router for.
func IsReserved(cfg utils.HTTPConfig, port int) bool {
	reserved, _ := reservedPorts(cfg.UPnP.ReservedPorts)
	return reserved[port]
}

// WithChoice returns the list with the choice in it: an earlier choice of
// the same port is replaced, keeping since when it is open and, when the
// same external port is asked for, the port the router gave.
func WithChoice(list []utils.UPnPAppPort, choice utils.UPnPAppPort) []utils.UPnPAppPort {
	key := choiceKey(choice)
	out := []utils.UPnPAppPort{}
	for _, old := range list {
		if choiceKey(old) != key {
			out = append(out, old)
			continue
		}
		choice.Since = old.Since
		if old.ExternalPort == choice.ExternalPort {
			choice.AssignedPort = old.AssignedPort
		}
	}
	return append(out, choice)
}

// WithoutChoice returns the list without the choice of one port.
func WithoutChoice(list []utils.UPnPAppPort, container string, port int, protocol string) []utils.UPnPAppPort {
	key := appKey(container, port, protocol)
	out := []utils.UPnPAppPort{}
	for _, old := range list {
		if choiceKey(old) != key {
			out = append(out, old)
		}
	}
	return out
}

// ChoicesChanged makes the router follow changed choices now: a refusal or a
// pending "forget" of these ports is cleared first, as the route does.
func ChoicesChanged(keys []string) Status {
	runMu.Lock()
	defer runMu.Unlock()
	for _, key := range keys {
		delete(failed, key)
		delete(missing, key)
	}
	return run(makePlan(utils.GetMainConfig().HTTPConfig, clock()), false)
}

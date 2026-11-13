package configapi

import (
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: a settings save carries the list of app ports it read
// earlier, or one of its own making. Neither reaches the configuration.
func TestSettingsSaveKeepsTheAppPorts(t *testing.T) {
	stored := utils.Config{}
	stored.HTTPConfig.UPnP.AppPorts = []utils.UPnPAppPort{{Container: "jellyfin", Port: 8096, Protocol: "tcp", AssignedPort: 8097}}

	// the settings form rebuilds the UPnP settings from its own fields: no list
	request := utils.Config{}
	request.HTTPConfig.UPnP.Enabled = true
	request.HTTPConfig.UPnP.ReservedPorts = "80, 443"
	keepUPnPAppPorts(&request, stored)
	if got := request.HTTPConfig.UPnP.AppPorts; len(got) != 1 || got[0].Container != "jellyfin" || got[0].AssignedPort != 8097 {
		t.Errorf("the list was dropped by a save: %+v", got)
	}
	if !request.HTTPConfig.UPnP.Enabled || request.HTTPConfig.UPnP.ReservedPorts != "80, 443" {
		t.Error("the settings of the form were not kept")
	}

	// a save that carries a list of its own opens nothing
	request = utils.Config{}
	request.HTTPConfig.UPnP.AppPorts = []utils.UPnPAppPort{{Container: "db", Port: 5432, Protocol: "tcp"}, {Container: "jellyfin", Port: 8096, Protocol: "tcp"}}
	keepUPnPAppPorts(&request, utils.Config{})
	if len(request.HTTPConfig.UPnP.AppPorts) != 0 {
		t.Errorf("a settings save added app ports: %+v", request.HTTPConfig.UPnP.AppPorts)
	}
}

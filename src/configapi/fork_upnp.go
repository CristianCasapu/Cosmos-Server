package configapi

import (
	"github.com/azukaar/cosmos-server/src/utils"
)

// Fork addition: a settings save keeps the list of app ports that are open
// on the router.
//
// The list (HTTPConfig.UPnP.AppPorts) is changed only through
// POST /api/upnp/apps, which asks for a confirmation naming the port and the
// app and writes each change to the security record. The settings page saves
// the whole configuration it read earlier: without this, a page that was
// open while a port was closed would open it again, and any save that
// carried a list would open ports without that confirmation.
func keepUPnPAppPorts(request *utils.Config, stored utils.Config) {
	request.HTTPConfig.UPnP.AppPorts = stored.HTTPConfig.UPnP.AppPorts
}

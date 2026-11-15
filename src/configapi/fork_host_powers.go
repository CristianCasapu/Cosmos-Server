package configapi

// Fork addition: the configuration permission is not the host's root.
//
// Cosmos runs as root. A few parts of the settings make it run something, or
// read and write folders, on the host with those rights: the scheduled jobs
// (shell commands), SnapRAID, the backups and their folders, the remote
// storage shares, the incremental snapshots, and routes that serve a host
// folder (STATIC and SPA). Upstream lets PUT /api/config write them all with
// the configuration permission, while its own APIs for the same things ask
// for the resources permission. An API token or a custom role meant for
// "configuration" was therefore as strong as root.
//
// Now a caller without the resources permission may still save the settings,
// but those parts must come back as they are stored (compared by value, after
// the masked secrets are taken as the stored ones, an empty list the same as
// none). If one of them changed, the save is refused with 403 and a sentence
// that names it, and recorded; nothing is dropped without a word. Creating,
// changing, or turning a route into, a route that serves a host folder needs
// the resources permission too, whichever API is used; deleting one does not.

import (
	"net/http"
	"strings"

	"github.com/azukaar/cosmos-server/src/constellation"
	"github.com/azukaar/cosmos-server/src/utils"
)

// servesHostFolder reports whether a route serves a folder of the host
func servesHostFolder(route utils.ProxyRouteConfig) bool {
	return route.Mode == "STATIC" || route.Mode == "SPA"
}

// sameByValue compares two values by their JSON form, with an empty list or
// map the same as none (sameSetting of fork_security.go): the settings page
// sends [] or {} for what is stored as null and the other way round.
func sameByValue(a, b interface{}) bool {
	return sameSetting(a, b)
}

// backupAsSaved is the request's backup section with the passwords the save
// takes from the stored one (see ConfigApiSet), so that a section sent back
// with its passwords masked compares as unchanged.
func backupAsSaved(request utils.BackupConfig, stored utils.BackupConfig, canReadCredentials bool) utils.BackupConfig {
	if request.Backups == nil {
		return request
	}
	backups := make(map[string]utils.SingleBackupConfig, len(request.Backups))
	for name, b := range request.Backups {
		if !canReadCredentials || b.Password == "" || b.Password == "***" {
			if existing, ok := stored.Backups[name]; ok && existing.Password != "" {
				b.Password = existing.Password
			}
		}
		backups[name] = b
	}
	request.Backups = backups
	return request
}

// changedHostSections names the parts of the settings that act on the host
// as root and that the request changes.
func changedHostSections(request utils.Config, stored utils.Config, canReadCredentials bool) []string {
	changed := []string{}
	if !sameByValue(request.CRON, stored.CRON) {
		changed = append(changed, "scheduled jobs")
	}
	if !sameByValue(request.Storage, stored.Storage) {
		changed = append(changed, "storage (SnapRAID)")
	}
	if !sameByValue(backupAsSaved(request.Backup, stored.Backup, canReadCredentials), stored.Backup) ||
		request.BackupOutputDir != stored.BackupOutputDir || request.IncrBackupOutputDir != stored.IncrBackupOutputDir {
		changed = append(changed, "backups")
	}
	if !sameByValue(request.RemoteStorage, stored.RemoteStorage) {
		changed = append(changed, "remote storage")
	}
	if !sameByValue(request.Snapshots, stored.Snapshots) {
		changed = append(changed, "incremental backups")
	}
	return changed
}

// changedFolderRoutes names the routes of a whole list of routes that serve
// a host folder and are new or not as stored. Routes the server manages are
// left out: a save keeps the stored copies of those whatever it is sent.
func changedFolderRoutes(routes []utils.ProxyRouteConfig, stored []utils.ProxyRouteConfig) []string {
	byName := map[string]utils.ProxyRouteConfig{}
	for _, route := range stored {
		byName[route.Name] = route
	}
	changed := []string{}
	for _, route := range routes {
		if !servesHostFolder(route) || route.IsManaged() {
			continue
		}
		before, ok := byName[route.Name]
		if ok && before.IsManaged() {
			continue
		}
		if !ok || !sameByValue(route, before) {
			changed = append(changed, route.Name)
		}
	}
	return changed
}

// folderRouteChanged reports whether a single route edit creates or changes
// a route that serves a host folder; before is nil for a new route.
func folderRouteChanged(before *utils.ProxyRouteConfig, after utils.ProxyRouteConfig) bool {
	if !servesHostFolder(after) {
		return false
	}
	return before == nil || !sameByValue(*before, after)
}

func refuseWithoutResources(w http.ResponseWriter, req *http.Request, what string, message string, code string) {
	utils.RecordSecurity(req, utils.SecAccessDenied, "", "permission", utils.PermissionLabel(utils.PERM_RESOURCES), "what", what)
	utils.Error("Settings: refused without the resources permission: "+what, nil)
	utils.HTTPError(w, message, http.StatusForbidden, code)
}

const folderRouteMessage = "A URL that serves a folder of the server (static or single-page app) can read any file the server can, so creating or changing one needs the Resources permission as well."

// refuseHostPowers is the hook of ConfigApiSet, before anything is written.
// It answers 403 and returns true when a caller without the resources
// permission changes a part of the settings that acts on the host as root.
func refuseHostPowers(w http.ResponseWriter, req *http.Request, request utils.Config) bool {
	if utils.HasPermission(req, utils.PERM_RESOURCES) {
		return false
	}
	stored := utils.ReadConfigFromFile()
	if changed := changedHostSections(request, stored, utils.HasPermission(req, utils.PERM_CREDENTIALS_READ)); len(changed) > 0 {
		list := strings.Join(changed, ", ")
		refuseWithoutResources(w, req, list,
			"Scheduled jobs, storage, backups, remote storage and incremental backups run on the server with full rights, so changing them needs the Resources permission as well. Changed here: "+list+".", "UC021")
		return true
	}
	if changed := changedFolderRoutes(request.HTTPConfig.ProxyConfig.Routes, stored.HTTPConfig.ProxyConfig.Routes); len(changed) > 0 {
		refuseWithoutResources(w, req, "folder routes: "+strings.Join(changed, ", "), folderRouteMessage+" Changed here: "+strings.Join(changed, ", ")+".", "UC022")
		return true
	}
	return false
}

// refuseFolderRoute is the hook of the route APIs (create and update).
// before is the stored route, nil for a new one.
func refuseFolderRoute(w http.ResponseWriter, req *http.Request, before *utils.ProxyRouteConfig, after utils.ProxyRouteConfig, code string) bool {
	if !folderRouteChanged(before, after) || utils.HasPermission(req, utils.PERM_RESOURCES) {
		return false
	}
	refuseWithoutResources(w, req, "folder route: "+after.Name, folderRouteMessage, code)
	return true
}

// storedRoute finds a route by name among the stored routes, then among the
// routes other nodes tunnel to this one; nil when there is none.
func storedRoute(name string) *utils.ProxyRouteConfig {
	for _, route := range utils.ReadConfigFromFile().HTTPConfig.ProxyConfig.Routes {
		if route.Name == name {
			r := route
			return &r
		}
	}
	if tunnel, ok := constellation.FindLocalTunnel(name); ok {
		r := tunnel.Route
		return &r
	}
	return nil
}

// refusePatchedFolderRoute is the hook of PATCH /api/config: its add and
// replace operations, checked against the routes as stored.
func refusePatchedFolderRoute(w http.ResponseWriter, req *http.Request, operation string, routes []utils.ProxyRouteConfig, routeIndex int, newRoute *utils.ProxyRouteConfig) bool {
	if newRoute == nil {
		return false
	}
	switch operation {
	case "add":
		return refuseFolderRoute(w, req, nil, *newRoute, "UR008")
	case "replace":
		if routeIndex < 0 || routeIndex >= len(routes) {
			return false
		}
		before := routes[routeIndex]
		return refuseFolderRoute(w, req, &before, *newRoute, "UR008")
	}
	return false
}

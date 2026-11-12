// Community build stub of the Cosmos Pro feature set; handlers answer PRO001.

package pro

import (
	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/nats-io/nats.go"
	"net/http"
	"sync"
)

// RegistryStaticActivateRequest is the POST body of the activate endpoint.
type RegistryStaticActivateRequest struct {
	Version string `json:"version"`
}

// RegistryStaticSitesRoute godoc
// @Summary List the sites of a static registry
// @Description Returns every site with its deployments, which one is active, and its
// @Description route configuration (Pro feature)
// @Tags registry
// @Produce json
// @Security BearerAuth
// @Param name path string true "Registry name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/registries/{name}/sites [get]
func RegistryStaticSitesRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// RegistryStaticSiteIdRoute godoc
// @Summary Get one static site
// @Description Returns the site with its deployments (Pro feature)
// @Tags registry
// @Produce json
// @Security BearerAuth
// @Param name path string true "Registry name"
// @Param site path string true "Site name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/registries/{name}/sites/{site} [get]
func RegistryStaticSiteIdRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// RegistryStaticVersionsRoute godoc
// @Summary Upload a static-site deployment
// @Description Stores a zip as one immutable deployment. Accepts a multipart form
// @Description (field "file") or the raw zip as the request body. Query parameters:
// @Description version (default: a UTC timestamp), activate (default true for the
// @Description site's first deployment), host/internal/spa/tags to configure the
// @Description site's route on first upload. Accepts a Cosmos token with the
// @Description Resources permission OR a deploy token of this registry with push scope
// @Description (Pro feature).
// @Tags registry
// @Accept octet-stream
// @Produce json
// @Security BearerAuth
// @Param name path string true "Registry name"
// @Param site path string true "Site name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/registries/{name}/sites/{site}/versions [post]
func RegistryStaticVersionsRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// RegistryStaticActivateRoute godoc
// @Summary Activate a static-site deployment
// @Description Moves the site's active pointer. This is BOTH deploy and rollback: the
// @Description deployments are immutable, so switching between them is instant and
// @Description cannot half-apply. Accepts a Cosmos token with the Resources permission
// @Description OR a registry deploy token with push scope (Pro feature).
// @Tags registry
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Registry name"
// @Param site path string true "Site name"
// @Param body body RegistryStaticActivateRequest true "Deployment to activate"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/registries/{name}/sites/{site}/activate [post]
func RegistryStaticActivateRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// RegistryStaticVersionIdRoute godoc
// @Summary Delete one static-site deployment
// @Description Removes the deployment record; the stored zip is reclaimed by the next
// @Description GC pass. Refused for the deployment that is currently active (Pro feature).
// @Tags registry
// @Produce json
// @Security BearerAuth
// @Param name path string true "Registry name"
// @Param site path string true "Site name"
// @Param version path string true "Deployment version"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/registries/{name}/sites/{site}/versions/{version} [delete]
func RegistryStaticVersionIdRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// RegistryStaticVersionDownloadRoute godoc
// @Summary Download one static-site deployment archive
// @Description Streams the zip exactly as it was uploaded (the version may be "active"
// @Description for the one currently served). Accepts a Cosmos token with the Resources
// @Description read permission OR a registry deploy token with pull scope (Pro feature).
// @Tags registry
// @Produce application/zip
// @Security BearerAuth
// @Param name path string true "Registry name"
// @Param site path string true "Site name"
// @Param version path string true "Deployment version, or active"
// @Success 200 {file} binary
// @Router /api/constellation/registries/{name}/sites/{site}/versions/{version}/download [get]
func RegistryStaticVersionDownloadRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// registryStaticUpdateSite godoc
// @Summary Configure one static site
// @Description Replaces the site's route configuration (host, internal, spa, tags or the whole user-facing
// @Description route); absent fields keep their stored value. Admin only: a deploy token cannot move a site (Pro feature).
// @Tags registry
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Registry name"
// @Param site path string true "Site name"
// @Param body body RegistryStaticSiteSettings true "Settings to change"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/registries/{name}/sites/{site} [put]
func registryStaticUpdateSite(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// registryStaticDeleteSite godoc
// @Summary Delete one static site
// @Description Removes the site and all its deployments; the stored zips are reclaimed by the next GC pass (Pro feature)
// @Tags registry
// @Produce json
// @Security BearerAuth
// @Param name path string true "Registry name"
// @Param site path string true "Site name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/registries/{name}/sites/{site} [delete]
func registryStaticDeleteSite(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// RegistryStaticSiteSettings is the shape of a site's route configuration.
// Pointers so an absent field keeps its stored value: a CI upload that only
// sends a zip must not clear the hostname the site is published on.
type RegistryStaticSiteSettings struct {
	Host     *string   `json:"host,omitempty"`
	Internal *bool     `json:"internal,omitempty"`
	SPA      *bool     `json:"spa,omitempty"`
	Tags     *[]string `json:"tags,omitempty"`
	// Route replaces the user-facing half of the serving route; its Host / RestrictToConstellation win over the scalars above.
	Route *utils.ProxyRouteConfig `json:"route,omitempty"`
}

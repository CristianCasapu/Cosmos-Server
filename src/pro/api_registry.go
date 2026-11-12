// Community build stub of the Cosmos Pro feature set; handlers answer PRO001.

package pro

import (
	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/nats-io/nats.go"
	"net/http"
	"sync"
)

// RegistryCreateRequest is the POST body: storage plus endpoint. Static registries take no host (their sites do).
type RegistryCreateRequest struct {
	Name string `json:"name"`
	// Type is docker, npm, static, generic or pypi. Required and immutable afterwards.
	Type       string          `json:"type"`
	QuotaBytes int64           `json:"quotaBytes,omitempty"`
	Storage    RegistryStorage `json:"storage"`

	Host               string   `json:"host,omitempty"`
	Internal           bool     `json:"internal,omitempty"`
	AllowAnonymousPull bool     `json:"allowAnonymousPull,omitempty"`
	Tags               []string `json:"tags,omitempty"`
	// Route's Host / RestrictToConstellation win over the scalars above when set.
	Route *utils.ProxyRouteConfig `json:"route,omitempty"`
}

// RegistrySettingsRequest is the PUT body; pointers keep absent fields at their stored value. The type is
// deliberately absent: it is immutable.
type RegistrySettingsRequest struct {
	QuotaBytes         *int64    `json:"quotaBytes,omitempty"`
	Host               *string   `json:"host,omitempty"`
	Internal           *bool     `json:"internal,omitempty"`
	AllowAnonymousPull *bool     `json:"allowAnonymousPull,omitempty"`
	Tags               *[]string `json:"tags,omitempty"`
	// Route's Host / RestrictToConstellation win over the scalars above when both are sent.
	Route *utils.ProxyRouteConfig `json:"route,omitempty"`
}

// RegistryTokenCreateRequest mints a deploy token; the raw value only ever appears in the response.
type RegistryTokenCreateRequest struct {
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes,omitempty"`
	ExpiryDays int      `json:"expiryDays,omitempty"`
}

func RegistryRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext, nc *nats.Conn) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

func RegistryIdRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext, nc *nats.Conn) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// listRegistries godoc
// @Summary List package registries
// @Description Returns every registry with the nodes serving it and its stored-size rollup, secrets redacted (Pro feature)
// @Tags registry
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/registries [get]
func listRegistries(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// getRegistry godoc
// @Summary Get one package registry
// @Description Returns the registry with the nodes serving it and its stored-size rollup, secrets redacted (Pro feature)
// @Tags registry
// @Produce json
// @Security BearerAuth
// @Param name path string true "Registry name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/registries/{name} [get]
func getRegistry(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// createRegistry godoc
// @Summary Create a package registry
// @Description Claims the name, provisions the backing bucket and marks the registry ready.
// @Description Every type but static is served on the given host from then on; a static
// @Description registry publishes its sites instead (Pro feature).
// @Tags registry
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body RegistryCreateRequest true "Registry to create"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/registries [post]
func createRegistry(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// deleteRegistry godoc
// @Summary Delete a package registry
// @Description Removes the record and every metadata key; serving nodes withdraw
// @Description the endpoint. Stored blobs are PRESERVED unless purgeData=true,
// @Description which best-effort empties the backing bucket (the bucket itself is
// @Description left in place) (Pro feature).
// @Tags registry
// @Produce json
// @Security BearerAuth
// @Param name path string true "Registry name"
// @Param purgeData query boolean false "Also delete every stored blob"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/registries/{name} [delete]
func deleteRegistry(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// RegistrySettingsRoute godoc
// @Summary Update a registry's settings
// @Description Replaces the quota, the host, the visibility, the anonymous-pull policy, the
// @Description serving tags or the whole user-facing route. Absent fields keep their stored
// @Description value (Pro feature).
// @Tags registry
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Registry name"
// @Param body body RegistrySettingsRequest true "Settings to change"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/registries/{name}/settings [put]
func RegistrySettingsRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// RegistryTokensRoute godoc
// @Summary Mint a registry deploy token
// @Description Creates a deploy token on the registry. The raw token is returned ONCE, in this
// @Description response, and never stored. Scopes default to pull+push (Pro feature).
// @Tags registry
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Registry name"
// @Param body body RegistryTokenCreateRequest true "Token to mint"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/registries/{name}/tokens [post]
func RegistryTokensRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// RegistryTokenIdRoute godoc
// @Summary Revoke a registry deploy token
// @Description Removes the token; it stops working on this node at once and on the others
// @Description within their cache TTL (Pro feature).
// @Tags registry
// @Produce json
// @Security BearerAuth
// @Param name path string true "Registry name"
// @Param tokenName path string true "Token name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/registries/{name}/tokens/{tokenName} [delete]
func RegistryTokenIdRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

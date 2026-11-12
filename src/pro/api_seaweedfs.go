// Community build stub of the Cosmos Pro feature set; handlers answer PRO001.

package pro

import (
	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/nats-io/nats.go"
	"net/http"
	"sync"
)

// SeaweedFSCreateRequest is the POST body; RestrictToConstellation is a *bool
// so "absent" defaults to true.
type SeaweedFSCreateRequest struct {
	Name                    string   `json:"name"`
	Tags                    []string `json:"tags,omitempty"`
	Image                   string   `json:"image,omitempty"`
	FilerReplicas           int      `json:"filerReplicas,omitempty"`
	IndexMode               string   `json:"indexMode,omitempty"`
	DefaultReplication      string   `json:"defaultReplication,omitempty"`
	VolumeSizeLimitMB       int      `json:"volumeSizeLimitMB,omitempty"`
	MinFreeSpace            string   `json:"minFreeSpace,omitempty"`
	MaxStorageGBPerNode     int      `json:"maxStorageGBPerNode,omitempty"`
	RestrictToConstellation *bool    `json:"restrictToConstellation,omitempty"`
}

func SeaweedFSRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext, nc *nats.Conn) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

func SeaweedFSIdRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext, nc *nats.Conn) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// listSeaweedFS godoc
// @Summary List managed SeaweedFS instances
// @Description Returns every instance with heartbeat-derived status, secrets redacted (Pro feature)
// @Tags seaweedfs
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/seaweedfs [get]
func listSeaweedFS(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// SeaweedFSStatusRoute is the unredacted view: full status plus the S3
// credentials and endpoint URLs.
//
// SeaweedFSStatusRoute godoc
// @Summary Get a managed SeaweedFS instance with its S3 credentials
// @Description Returns the unredacted instance status (S3 access and secret keys included) and
// @Description the S3 endpoint URLs. Hands out a credential, so it needs the write permission (Pro feature).
// @Tags seaweedfs
// @Produce json
// @Security BearerAuth
// @Param name path string true "Instance name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/seaweedfs/{name}/status [get]
func SeaweedFSStatusRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// createSeaweedFS godoc
// @Summary Create a managed SeaweedFS instance
// @Description Provisions 3 pinned masters on the first three constellation managers,
// @Description a fill-mode volume-server deployment on the chosen tags, a filer+S3
// @Description deployment behind the tunnel LB, and a managed postgres for the filer
// @Description store (Pro feature). Refused when fewer than 3 managers are online.
// @Tags seaweedfs
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body SeaweedFSCreateRequest true "Instance to create"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/seaweedfs [post]
func createSeaweedFS(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext, nc *nats.Conn) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// deleteSeaweedFS godoc
// @Summary Delete a managed SeaweedFS instance
// @Description Tears down deployments, masters and (by default) the filer database.
// @Description Volume-server data volumes are PRESERVED unless purgeData=true;
// @Description keepFilerDB=true keeps the managed database entirely (Pro feature).
// @Tags seaweedfs
// @Produce json
// @Security BearerAuth
// @Param name path string true "Instance name"
// @Param purgeData query boolean false "Also remove the data volumes on every node"
// @Param keepFilerDB query boolean false "Keep the auto-provisioned filer database"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/seaweedfs/{name} [delete]
func deleteSeaweedFS(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext, nc *nats.Conn) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// SeaweedFSStorageRequest is the body of the storage endpoint.
type SeaweedFSStorageRequest struct {
	// MaxStorageGBPerNode is required; 0 means unlimited.
	MaxStorageGBPerNode *int `json:"maxStorageGBPerNode"`
}

// SeaweedFSDrainRequest is the body of the drain endpoint.
type SeaweedFSDrainRequest struct {
	// Device is the constellation device name of the volume server to evacuate.
	Device string `json:"device"`
}

// SeaweedFSUpgradeRequest is the body of the upgrade endpoint.
type SeaweedFSUpgradeRequest struct {
	Image string `json:"image"`
}

// SeaweedFSReplaceMasterRequest is the body of the replace-master endpoint.
type SeaweedFSReplaceMasterRequest struct {
	OldDevice string `json:"oldDevice"`
	// NewDevice is optional: the next live manager outside the master set is picked when absent.
	NewDevice string `json:"newDevice,omitempty"`
}

// SeaweedFSRouteRequest is the body of the S3 route endpoint.
type SeaweedFSRouteRequest struct {
	// Route is required: the user-facing settings of the S3 route.
	Route *utils.ProxyRouteConfig `json:"route"`
}

// SeaweedFSRestrictRequest is the body of the restrict endpoint.
type SeaweedFSRestrictRequest struct {
	// *bool, deliberately: with a plain bool an EMPTY body would decode as
	// false and silently publish the object store — the exact inversion of the
	// restrict-by-default posture. Absent means invalid, never "unrestrict".
	RestrictToConstellation *bool `json:"restrictToConstellation"`
}

// SeaweedFSRestrictRoute toggles constellation-only access (compose rewrite +
// version bump).
//
// SeaweedFSRestrictRoute godoc
// @Summary Restrict a managed SeaweedFS instance to the constellation
// @Description Toggles constellation-only access to the S3 endpoint. The filers are cycled one
// @Description node at a time to apply it (Pro feature).
// @Tags seaweedfs
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Instance name"
// @Param body body SeaweedFSRestrictRequest true "Restriction to apply"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/seaweedfs/{name}/restrict [post]
func SeaweedFSRestrictRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// SeaweedFSStorageRoute changes the per-node storage cap. Shrinking below what
// a node already holds deletes nothing; the node just stops receiving new
// volumes until it is back under the ceiling.
//
// SeaweedFSStorageRoute godoc
// @Summary Change the per-node storage cap of a managed SeaweedFS instance
// @Description Sets the storage cap per node (0 = unlimited) and rolls the volume servers one node at a
// @Description time. Lowering it below what a node holds deletes nothing: the node just stops receiving
// @Description new volumes until it is back under the cap (Pro feature).
// @Tags seaweedfs
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Instance name"
// @Param body body SeaweedFSStorageRequest true "Storage cap"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/seaweedfs/{name}/storage [put]
func SeaweedFSStorageRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// SeaweedFSJobsRoute replaces the instance's maintenance-job configuration.
//
// SeaweedFSJobsRoute godoc
// @Summary Configure the maintenance jobs of a managed SeaweedFS instance
// @Description Replaces the maintenance-job configuration; the schedules are re-registered within a minute (Pro feature)
// @Tags seaweedfs
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Instance name"
// @Param body body SwfsJobsConfig true "Job configuration"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/seaweedfs/{name}/jobs [put]
func SeaweedFSJobsRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// SeaweedFSRepairRoute starts the "this hardware is gone" repair job on the
// locus node.
//
// SeaweedFSRepairRoute godoc
// @Summary Repair a managed SeaweedFS instance after losing hardware
// @Description Starts the repair job that restores the replication of the volumes a lost node held (Pro feature)
// @Tags seaweedfs
// @Produce json
// @Security BearerAuth
// @Param name path string true "Instance name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/seaweedfs/{name}/repair [post]
func SeaweedFSRepairRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext, nc *nats.Conn) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// SeaweedFSDrainRoute evacuates one live volume server (body:
// {"device": "<device name>"}).
//
// SeaweedFSDrainRoute godoc
// @Summary Drain a volume server of a managed SeaweedFS instance
// @Description Starts the evacuation of one live volume server, so its node can be untagged afterwards
// @Description without ever being under-replicated (Pro feature).
// @Tags seaweedfs
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Instance name"
// @Param body body SeaweedFSDrainRequest true "Device to drain"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/seaweedfs/{name}/drain [post]
func SeaweedFSDrainRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext, nc *nats.Conn) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// SeaweedFSUpgradeRoute starts a rolling upgrade (body: {"image": "..."}).
//
// SeaweedFSUpgradeRoute godoc
// @Summary Upgrade a managed SeaweedFS instance
// @Description Starts a rolling upgrade to the given image: the masters one at a time, then the volume
// @Description and filer deployments one node at a time (Pro feature).
// @Tags seaweedfs
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Instance name"
// @Param body body SeaweedFSUpgradeRequest true "Image to upgrade to"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/seaweedfs/{name}/upgrade [post]
func SeaweedFSUpgradeRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext, nc *nats.Conn) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// SeaweedFSReplaceMasterRoute swaps one pinned master for another manager
// (body: {"oldDevice": "...", "newDevice": "..."} — newDevice optional).
//
// SeaweedFSReplaceMasterRoute godoc
// @Summary Replace a master of a managed SeaweedFS instance
// @Description Swaps one pinned master for another manager (the next live manager outside the set when
// @Description newDevice is absent), then starts the rolling master re-provision (Pro feature).
// @Tags seaweedfs
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Instance name"
// @Param body body SeaweedFSReplaceMasterRequest true "Master to replace"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/seaweedfs/{name}/replace-master [post]
func SeaweedFSReplaceMasterRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext, nc *nats.Conn) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// SeaweedFSS3RouteRoute godoc
// @Summary Update the S3 endpoint's proxy route
// @Description Replaces the user-facing settings of the instance's S3 route (auth,
// @Description shield, whitelist...). Name, mode, target, tunnel and owner are
// @Description forced server-side; the restriction flag is mirrored onto the record.
// @Description The route lives in the filer deployment's compose, so this is a compose
// @Description rewrite + version bump; nodes apply it without recreating the filers
// @Description (Pro feature).
// @Tags seaweedfs
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Instance name"
// @Param body body SeaweedFSRouteRequest true "Route to store"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/seaweedfs/{name}/route [put]
func SeaweedFSS3RouteRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// getSeaweedFS godoc
// @Summary Get one managed SeaweedFS instance
// @Description Returns the instance with heartbeat-derived status, secrets redacted (Pro feature)
// @Tags seaweedfs
// @Produce json
// @Security BearerAuth
// @Param name path string true "Instance name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/seaweedfs/{name} [get]
func getSeaweedFS(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

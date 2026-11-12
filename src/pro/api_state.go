// Community build stub of the Cosmos Pro feature set; handlers answer PRO001.

package pro

import (
	"net/http"
	"sync"

	"github.com/nats-io/nats.go"

	"github.com/azukaar/cosmos-server/src/utils"
)

// StateRoute godoc
// @Summary Get the Pro state mirror summary
// @Description Returns record counts per bucket as held by the on-disk state mirror (backup.cosmos-state.json),
// @Description when it was written, whether a reform reseed is pending and whether an import is marked in
// @Description progress (which freezes the scheduler). Pro feature.
// @Tags state
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/state [get]
func StateRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// StateImportRoute godoc
// @Summary Import a Pro state file
// @Description Body is a state file as produced by the export. Writes every record that does not already
// @Description exist and leaves existing ones untouched; never creates containers or touches quarantine
// @Description state. Returns created/present counts per bucket. Pro feature.
// @Tags state
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body ProState true "State file"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/state/import [post]
func StateImportRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// StateStartEmptyRoute godoc
// @Summary Clear a frozen state import
// @Description Removes the import-in-progress marker left by a failed import or reseed, accepting the
// @Description deployments bucket as it is and letting the scheduler reconcile again. Pro feature.
// @Tags state
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/state/start-empty [post]
func StateStartEmptyRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

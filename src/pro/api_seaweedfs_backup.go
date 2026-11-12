// Community build stub of the Cosmos Pro feature set; handlers answer PRO001.

package pro

import (
	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/nats-io/nats.go"
	"net/http"
	"sync"
)

func SeaweedFSBackupRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// SeaweedFSBackupRunRoute starts a metadata backup now, on the job-locus node.
//
// SeaweedFSBackupRunRoute godoc
// @Summary Run the metadata backup of a managed SeaweedFS instance now
// @Description Starts a metadata backup on the node running the instance's jobs (Pro feature)
// @Tags seaweedfs
// @Produce json
// @Security BearerAuth
// @Param name path string true "Instance name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/seaweedfs/{name}/backup/run [post]
func SeaweedFSBackupRunRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext, nc *nats.Conn) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// SeaweedFSBackupSnapshotsRoute lists metadata snapshots, resolved on the
// locus node.
//
// SeaweedFSBackupSnapshotsRoute godoc
// @Summary List the metadata snapshots of a managed SeaweedFS instance
// @Description Returns the snapshots of the metadata-backup repository (Pro feature)
// @Tags seaweedfs
// @Produce json
// @Security BearerAuth
// @Param name path string true "Instance name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/seaweedfs/{name}/backup/snapshots [get]
func SeaweedFSBackupSnapshotsRoute(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext, nc *nats.Conn) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// configureSeaweedFSBackup godoc
// @Summary Configure the metadata backup of a managed SeaweedFS instance
// @Description Sets the metadata-backup repository and schedules. The repository password is minted once
// @Description and never rotated: it is the only key to the snapshots already written (Pro feature).
// @Tags seaweedfs
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Instance name"
// @Param body body ManagedDBBackupRequest true "Backup configuration"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/seaweedfs/{name}/backup [put]
func configureSeaweedFSBackup(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

// removeSeaweedFSBackup godoc
// @Summary Remove the metadata backup of a managed SeaweedFS instance
// @Description Clears the backup configuration. The repository and its snapshots are left untouched (Pro feature).
// @Tags seaweedfs
// @Produce json
// @Security BearerAuth
// @Param name path string true "Instance name"
// @Success 200 {object} utils.APIResponse
// @Router /api/constellation/seaweedfs/{name}/backup [delete]
func removeSeaweedFSBackup(w http.ResponseWriter, req *http.Request, lock *sync.RWMutex, js nats.JetStreamContext) {
	utils.Error("This is a pro and is not currently available on your server. Please upgrade to Cosmos Pro to access this feature.", nil)
	utils.HTTPError(w, "This feature is only available in Cosmos Pro", http.StatusForbidden, "PRO001")
}

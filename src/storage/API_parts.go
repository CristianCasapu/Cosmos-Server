package storage

import (
	"net/http"
	"encoding/json"
	"fmt"
	"sync"
	"strings"

	"github.com/azukaar/cosmos-server/src/utils"
)

type FormatDiskJSON struct {
	Disk string `json:"disk" validate:"required"`
	Format string `json:"format" validate:"required"`
	Password string `json:"password" validate:"required"`
	Confirm string `json:"confirm"` // Fork addition: the device's name as the administrator typed it, see fork_format.go
}

func isDiskOrPartition(path string) (string, error) {
	// Fork addition: waited for (upstream read one line and left lsblk unreaped), see fork_format.go
	out, err := runStorageCommand("lsblk", "-dno", "TYPE", path)
	if err != nil {
		return "", commandFailure("Reading the type of "+path, "lsblk", out, err)
	}
	if fields := strings.Fields(out); len(fields) > 0 {
		return fields[0], nil
	}

	return "", fmt.Errorf("no output from lsblk")
}

var diskOpMutex sync.Mutex

// FormatDiskRoute godoc
// @Summary Format a disk or partition
// @Description Formats the specified disk with the given filesystem format. Requires password confirmation. Streams progress output.
// @Tags Storage
// @Accept json
// @Produce plain
// @Param body body FormatDiskJSON true "Format disk request"
// @Security BearerAuth
// @Success 200 {string} string "Streamed text output ending with [OPERATION SUCCEEDED]"
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 500 {object} utils.HTTPErrorResult
// @Router /api/disks/format [post]
func FormatDiskRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_RESOURCES) != nil {
		return
	}
	
	diskOpMutex.Lock()
	defer diskOpMutex.Unlock()

	if req.Method == "POST" {
		// Enable streaming of response by setting appropriate headers
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.Header().Set("Content-Type", "text/plain")

		flusher, ok := w.(http.Flusher)
    if !ok {
        http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
        return
    }

		var request FormatDiskJSON
		err := json.NewDecoder(req.Body).Decode(&request)
		if err != nil {
			utils.Error("FormatDiskRoute: Invalid User Request", err)
			fmt.Fprintf(w, "%s", utils.DoErr("[OPERATION FAILED] FormatDiskRoute  Syntax Error"))
			http.Error(w, "FormatDiskRoute  Syntax Error", http.StatusBadRequest)

			return
		}
			
		nickname := utils.GetAuthContext(req).Nickname

		errp := utils.CheckPassword(nickname, request.Password)
		if errp != nil {
			utils.Error("FormatDiskRoute: Invalid User Request", errp)
			fmt.Fprintf(w, "%s", utils.DoErr("[OPERATION FAILED] Wrong password supplied. Try again"))
			http.Error(w, "Wrong password supplied. Try again", http.StatusUnauthorized)
			return
		}

		// Fork addition: the request checked (device, file system, the typed name), the
		// disk refused when in use, a whole disk partitioned then formatted, and every
		// step's exit status reported: upstream reported success whatever mkfs did, see fork_format.go
		forkFormatStream(w, flusher.Flush, request.Disk, request.Format, request.Confirm)
		return
	} else {
		utils.Error("FormatDiskRoute: Method not allowed " + req.Method, nil)
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
}


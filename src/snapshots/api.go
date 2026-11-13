package snapshots

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/azukaar/cosmos-server/src/utils"
)

// sourceView is one folder that is backed up, with its snapshots.
type sourceView struct {
	Path      string     `json:"path"`
	Slug      string     `json:"slug"`
	Snapshots []Snapshot `json:"snapshots"` // newest first
	LastError string     `json:"lastError"` // why the last run did not go cleanly, if so
	// the folders restores kept next to this one, newest first; see kept.go
	BeforeRestore []KeptFolder `json:"beforeRestore"`
}

// statusView is what GET /api/snapshots answers.
type statusView struct {
	Enabled     bool           `json:"enabled"`
	Destination string         `json:"destination"`
	Running     bool           `json:"running"`
	Action      string         `json:"action,omitempty"` // "snapshot", "restore" or "cleanup", while running
	Current     string         `json:"current"`          // the folder being worked on
	FilesDone   int64          `json:"filesDone"`
	StartedAt   *time.Time     `json:"startedAt"`
	Sources     []sourceView   `json:"sources"`
	Suggested   []string       `json:"suggested"`
	LastRestore *RestoreResult `json:"lastRestore,omitempty"`
	Error       string         `json:"error,omitempty"` // the saved settings cannot be used
}

// sentence starts a message with a capital letter, for display.
func sentence(message string) string {
	first, size := utf8.DecodeRuneInString(message)
	if size == 0 || first == utf8.RuneError {
		return message
	}
	return string(unicode.ToUpper(first)) + message[size:]
}

// view gathers the saved settings, the snapshots on the disk and what is
// running.
func view() statusView {
	config := utils.GetMainConfig()
	cfg := config.Snapshots
	st := GetStatus()

	v := statusView{
		Enabled:     cfg.Enabled,
		Destination: cfg.Destination,
		Running:     st.Running,
		Action:      st.Action,
		Current:     st.Current,
		FilesDone:   st.FilesDone,
		StartedAt:   st.StartedAt,
		Sources:     []sourceView{},
		Suggested:   suggested(config),
		LastRestore: st.LastRestore,
	}
	if v.LastRestore != nil {
		v.LastRestore.Error = sentence(v.LastRestore.Error)
	}

	// settings edited by hand into something unusable: list nothing
	usable := true
	checked := cfg
	if err := ValidateConfig(&checked); err != nil {
		v.Error = err.Error()
		usable = false
	}

	for _, source := range cfg.Sources {
		sv := sourceView{Path: source, Slug: Slug(source), Snapshots: []Snapshot{}, BeforeRestore: []KeptFolder{}}
		if usable && cfg.Destination != "" {
			sv.Snapshots = List(cfg.Destination, source)
		}
		if usable {
			sv.BeforeRestore = listKept(source)
		}
		if result, ok := st.LastResults[source]; ok {
			sv.LastError = sentence(result.Error)
		}
		v.Sources = append(v.Sources, sv)
	}
	return v
}

// StatusRoute godoc
// @Summary Incremental backup: settings in use, snapshots and what is running
// @Description Lists the folders that are backed up with their snapshots (newest first), tells whether a run or a restore is in progress, and suggests folders worth backing up
// @Tags snapshots
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/snapshots [get]
func StatusRoute(w http.ResponseWriter, req *http.Request) {
	// For anyone who may read the configuration: the settings page loads it.
	if utils.CheckPermissions(w, req, utils.PERM_CONFIGURATION_READ) != nil {
		return
	}
	if req.Method != "GET" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}

	json.NewEncoder(w).Encode(utils.APIResponse{
		Status: "OK",
		Data:   view(),
	})
}

// RunRoute godoc
// @Summary Incremental backup: start a run now
// @Description Starts a snapshot of every configured folder in the background. Takes no input.
// @Tags snapshots
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Failure 409 {object} utils.HTTPErrorResult
// @Router /api/snapshots/run [post]
func RunRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}

	utils.Log("API: Start the incremental backup")
	// Takes no input: it only runs what the saved settings ask for.
	if err := Start(); err != nil {
		utils.HTTPError(w, sentence(err.Error()), http.StatusConflict, "SN001")
		return
	}

	json.NewEncoder(w).Encode(utils.APIResponse{
		Status: "OK",
		Data:   view(),
	})
}

// restoreRequest names a snapshot by the folder it was taken from and its
// name, both as GET /api/snapshots lists them.
type restoreRequest struct {
	Source   string
	Snapshot string
}

// RestoreRoute godoc
// @Summary Incremental backup: put a snapshot back in place of its folder
// @Description Starts, in the background, the restore of one snapshot. The folder as it is now is kept next to it as "<folder>.before-restore-<time>".
// @Tags snapshots
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body restoreRequest true "The folder and the snapshot"
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Failure 409 {object} utils.HTTPErrorResult
// @Router /api/snapshots/restore [post]
func RestoreRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}

	var request restoreRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 16<<10)).Decode(&request); err != nil {
		utils.HTTPError(w, "The request could not be read", http.StatusBadRequest, "SN002")
		return
	}

	utils.Log("API: Restore a snapshot")
	utils.AuditSecurity(req, utils.SecSnapshotRestore, request.Source, "snapshot", request.Snapshot) // security record
	// Source must be a configured folder and Snapshot the name of a snapshot:
	// StartRestore accepts nothing else, and no path is built from anything else.
	err := StartRestore(request.Source, request.Snapshot)
	switch {
	case errors.Is(err, ErrBusy):
		utils.HTTPError(w, sentence(err.Error()), http.StatusConflict, "SN001")
		return
	case err != nil:
		utils.HTTPError(w, sentence(err.Error()), http.StatusBadRequest, "SN003")
		return
	}

	json.NewEncoder(w).Encode(utils.APIResponse{
		Status: "OK",
		Data:   view(),
	})
}

// cleanupRequest names a folder that a restore kept: the folder that was
// restored and the name of the kept one, both as GET /api/snapshots lists
// them.
type cleanupRequest struct {
	Source string
	Name   string
}

// CleanupRoute godoc
// @Summary Incremental backup: delete a folder that a restore kept
// @Description Deletes one "<folder>.before-restore-<time>" folder, which a restore left next to the folder it replaced. Answers when the folder is gone.
// @Tags snapshots
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body cleanupRequest true "The folder that was restored and the name of the kept folder"
// @Success 200 {object} utils.APIResponse
// @Failure 400 {object} utils.HTTPErrorResult
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Failure 409 {object} utils.HTTPErrorResult
// @Router /api/snapshots/cleanup [post]
func CleanupRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}
	if req.Method != "POST" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}

	var request cleanupRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 16<<10)).Decode(&request); err != nil {
		utils.HTTPError(w, "The request could not be read", http.StatusBadRequest, "SN002")
		return
	}

	utils.Log("API: Delete a folder kept by a restore")
	// Source must be a configured folder and Name the name a restore gives the
	// folder it keeps: RemoveKept accepts nothing else, and the only path it
	// builds is that name inside the folder that contains Source.
	err := RemoveKept(request.Source, request.Name)
	switch {
	case errors.Is(err, ErrBusy):
		utils.HTTPError(w, sentence(err.Error()), http.StatusConflict, "SN001")
		return
	case err != nil:
		utils.HTTPError(w, sentence(err.Error()), http.StatusBadRequest, "SN004")
		return
	}

	json.NewEncoder(w).Encode(utils.APIResponse{
		Status: "OK",
		Data:   view(),
	})
}

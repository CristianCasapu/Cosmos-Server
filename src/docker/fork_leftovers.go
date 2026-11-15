package docker

// Fork addition: containers left behind by an interrupted edit.
//
// An edit sets the old container aside (stopped, renamed
// "<name>-cosmos-old-<first 12 hex of its ID>", fork_safe_edit.go) until its
// replacement runs. When Cosmos is stopped in between, nothing puts it back
// or removes it. This finds such leftovers, tells the administrators about
// them once, and lets them put the previous container back or remove the
// leftover, from the ServApps page. Nothing is ever deleted without being
// asked, and every action checks again that the container is a leftover (its
// name has the pattern and the hex are the start of its own ID), so it can
// never touch another container.

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/docker/docker/api/types"
	conttype "github.com/docker/docker/api/types/container"
)

// leftoverPattern is a set-aside name: the app's name (a Docker name), the
// marker and the first 12 hex of the container's own ID.
var leftoverPattern = regexp.MustCompile(`^([a-zA-Z0-9][a-zA-Z0-9_.-]*)` + regexp.QuoteMeta(asideMarker) + `([0-9a-f]{12})$`)

// maxLeftoverName bounds the names the route looks at (Docker's own limit is
// far beyond any real name).
const maxLeftoverName = 255

// maxLeftovers bounds what the list reports.
const maxLeftovers = 100

// maxLeftoverBody bounds the body of the actions.
const maxLeftoverBody = 1 << 10

// Notification keys (client/src/utils/locales/en/translation.json).
const (
	leftoverNotificationTitle   = "header.notification.title.containerLeftover"
	leftoverNotificationMessage = "header.notification.message.containerLeftover"
)

// namedContainer is a container's ID and its name without the leading slash.
type namedContainer struct {
	ID   string
	Name string
}

// leftoverInspector is what finding leftovers needs from Docker besides the
// swapper. The tests replace it with a fake.
type leftoverInspector interface {
	// Names lists every container, running or not.
	Names() ([]namedContainer, error)
	// Inspect returns the container with that exact name or ID.
	Inspect(nameOrID string) (types.ContainerJSON, error)
}

type dockerLeftoverInspector struct{}

func (dockerLeftoverInspector) Names() ([]namedContainer, error) {
	if err := Connect(); err != nil {
		return nil, err
	}
	containers, err := DockerClient.ContainerList(DockerContext, conttype.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	out := make([]namedContainer, 0, len(containers))
	for _, c := range containers {
		for _, name := range c.Names {
			out = append(out, namedContainer{ID: c.ID, Name: strings.TrimPrefix(name, "/")})
		}
	}
	return out, nil
}

func (dockerLeftoverInspector) Inspect(nameOrID string) (types.ContainerJSON, error) {
	if err := Connect(); err != nil {
		return types.ContainerJSON{}, err
	}
	return DockerClient.ContainerInspect(DockerContext, nameOrID)
}

var leftoverDocker leftoverInspector = dockerLeftoverInspector{}

// Leftover is one container left behind by an interrupted edit, as the list
// reports it.
type Leftover struct {
	Name       string `json:"name"`  // the leftover's name
	App        string `json:"app"`   // the name it had, the app's
	ID         string `json:"id"`    // its ID, short (12 hex)
	State      string `json:"state"` // Docker's state: exited, created, running...
	Running    bool   `json:"running"`
	Created    string `json:"created"`    // when the container was created (RFC 3339), "" when unknown
	Finished   string `json:"finished"`   // when it last stopped, roughly when the edit was made; "" when unknown
	AppExists  bool   `json:"appExists"`  // a container named App exists now
	AppRunning bool   `json:"appRunning"` // and it runs
}

// parseLeftoverName splits a leftover's name into the app's name and the
// short ID; ok is false for any other name.
func parseLeftoverName(name string) (app string, short string, ok bool) {
	if len(name) > maxLeftoverName {
		return "", "", false
	}
	m := leftoverPattern.FindStringSubmatch(name)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// isLeftover says whether a container with that name and ID is a leftover:
// the name has the pattern and its hex are the start of the ID.
func isLeftover(name string, id string) bool {
	_, short, ok := parseLeftoverName(name)
	return ok && len(id) >= 12 && strings.HasPrefix(id, short)
}

// dockerTime turns Docker's time into RFC 3339, "" for its "never".
func dockerTime(value string) string {
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || at.Year() < 2000 {
		return ""
	}
	return at.UTC().Format(time.RFC3339)
}

// inspectExact inspects a container by name and returns it only when that is
// its name: Docker also answers a name with a container whose ID starts with
// it.
func inspectExact(name string) (types.ContainerJSON, bool) {
	found, err := leftoverDocker.Inspect(name)
	if err != nil || found.ContainerJSONBase == nil || strings.TrimPrefix(found.Name, "/") != name {
		return types.ContainerJSON{}, false
	}
	return found, true
}

// leftoverByName inspects a leftover and checks again that it is one.
func leftoverByName(name string) (types.ContainerJSON, string, bool) {
	app, _, ok := parseLeftoverName(name)
	if !ok {
		return types.ContainerJSON{}, "", false
	}
	found, ok := inspectExact(name)
	if !ok || !isLeftover(name, found.ID) {
		return types.ContainerJSON{}, "", false
	}
	return found, app, true
}

// describeLeftover is what the list says about one leftover.
func describeLeftover(found types.ContainerJSON, app string) Leftover {
	name := strings.TrimPrefix(found.Name, "/")
	entry := Leftover{Name: name, App: app, ID: found.ID[:12], Created: dockerTime(found.Created)}
	if found.State != nil {
		entry.State = found.State.Status
		entry.Running = found.State.Running
		entry.Finished = dockerTime(found.State.FinishedAt)
	}
	if current, ok := inspectExact(app); ok {
		entry.AppExists = true
		entry.AppRunning = current.State != nil && current.State.Running
	}
	return entry
}

// ListLeftovers finds the containers left behind by interrupted edits, the
// most recent first.
func ListLeftovers() ([]Leftover, error) {
	names, err := leftoverDocker.Names()
	if err != nil {
		return nil, err
	}
	out := []Leftover{}
	for _, c := range names {
		if len(out) >= maxLeftovers {
			break
		}
		if !isLeftover(c.Name, c.ID) {
			continue
		}
		found, app, ok := leftoverByName(c.Name)
		if !ok || found.ID != c.ID {
			continue
		}
		out = append(out, describeLeftover(found, app))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Finished != out[j].Finished {
			return out[i].Finished > out[j].Finished
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// errNotLeftover is the answer for a name that is not a leftover (any more).
var errNotLeftover = errors.New("not a leftover")

// errLeftoverRunning refuses to remove a leftover that runs.
var errLeftoverRunning = errors.New("the leftover is running")

// restoreError is a restore that failed. undone says whether everything was
// put back as it was before the restore.
type restoreError struct {
	reason string
	undone bool
}

func (e *restoreError) Error() string { return e.reason }

// RestoreResult says what a restore did.
type RestoreResult struct {
	App      string `json:"app"`
	SetAside string `json:"setAside"` // the name the container that had the app's name now has, "" when there was none
}

// restoreLeftover puts a leftover back under its app's name and starts it.
// A container that has the app's name is set aside first, the way an edit
// does (stopped, renamed, never deleted). When a step fails, what was done is
// undone.
func restoreLeftover(name string) (RestoreResult, error) {
	DockerNetworkLock <- true
	defer func() { <-DockerNetworkLock }()

	leftover, app, ok := leftoverByName(name)
	if !ok {
		return RestoreResult{}, errNotLeftover
	}
	result := RestoreResult{App: app}

	var aside *asideContainer
	if current, ok := inspectExact(app); ok && current.ID != leftover.ID {
		var err error
		aside, err = setContainerAside(current)
		if err != nil {
			return result, &restoreError{reason: "The container now named " + app + " could not be set aside: " + err.Error(), undone: true}
		}
		result.SetAside = aside.asideName
	}

	undo := func(reason string, renamed bool) error {
		undone := true
		if renamed {
			if err := swapper.Rename(leftover.ID, name); err != nil {
				undone = false
				reason += ". The previous container could not get its leftover name back (" + err.Error() + ")"
			}
		}
		if aside != nil && undone {
			if err := aside.putBack(""); err != nil {
				undone = false
				reason += ". " + err.Error()
			}
		}
		return &restoreError{reason: reason, undone: undone}
	}

	var err error
	for attempt := 0; attempt < 10; attempt++ {
		err = swapper.Rename(leftover.ID, app)
		// the name that was just released can stay taken for a moment
		if err == nil || !strings.Contains(err.Error(), "already in use") {
			break
		}
		time.Sleep(renameRetryDelay)
	}
	if err != nil {
		return result, undo("The previous container could not get the name "+app+" back: "+err.Error(), false)
	}

	if leftover.State == nil || !leftover.State.Running {
		if err := swapper.Start(leftover.ID); err != nil {
			return result, undo("The previous container could not be started: "+err.Error(), true)
		}
	}

	utils.Log("Leftovers - Container " + app + " put back from " + name + leftoverAsideLog(result.SetAside))
	utils.ResolveThread(leftoverThread(logSafe(name)))
	return result, nil
}

func leftoverAsideLog(asideName string) string {
	if asideName == "" {
		return ""
	}
	return "; the container that had its name is kept, stopped, as " + asideName
}

// removeLeftover deletes a leftover, never while it runs; its volumes stay.
func removeLeftover(name string) error {
	DockerNetworkLock <- true
	defer func() { <-DockerNetworkLock }()

	leftover, _, ok := leftoverByName(name)
	if !ok {
		return errNotLeftover
	}
	if leftover.State != nil && (leftover.State.Running || leftover.State.Restarting) {
		return errLeftoverRunning
	}
	if err := swapper.Remove(leftover.ID, false); err != nil {
		if containerGone(err) {
			return errNotLeftover
		}
		return err
	}
	utils.Log("Leftovers - Container " + name + " removed")
	utils.ResolveThread(leftoverThread(logSafe(name)))
	return nil
}

// ---- at start ---------------------------------------------------------------

// logSafe keeps what a log line may carry of a name: the characters of a
// Docker name, bounded.
func logSafe(value string) string {
	out := make([]rune, 0, len(value))
	for _, r := range value {
		if len(out) >= 128 {
			break
		}
		if r == '_' || r == '.' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			out = append(out, r)
		} else {
			out = append(out, '?')
		}
	}
	return string(out)
}

// leftoverNotificationVars is what the notification about a leftover says;
// the same leftover always gives the same text (see notifiedBefore).
func leftoverNotificationVars(entry Leftover) string {
	return logSafe(entry.App) + " (kept as " + logSafe(entry.Name) + ")"
}

// notifiedBefore says whether the administrators were already told about a
// leftover (its notification is among their latest ones), so that a restart
// does not tell them again. The tests replace it.
var notifiedBefore = func(vars string) bool {
	for _, admin := range utils.ListAllUsers("admin") {
		notifications, err := utils.ListNotifications(admin.Nickname, 0, 200)
		if err != nil {
			continue
		}
		for _, n := range notifications {
			if n.Title == leftoverNotificationTitle && n.Vars == vars {
				return true
			}
		}
	}
	return false
}

// writeNotification is utils.WriteNotification; the tests replace it.
var writeNotification = utils.WriteNotification

// reportLeftovers logs one line per leftover and notifies the
// administrators about the ones they were not told about yet. It returns how
// many were found.
func reportLeftovers() int {
	found, err := ListLeftovers()
	if err != nil {
		utils.Error("Leftovers - Could not look for containers left behind by an interrupted change", err)
		return 0
	}
	for _, entry := range found {
		utils.Warn("Leftovers - A change of " + logSafe(entry.App) + " was interrupted: the previous container is kept, stopped, as " + logSafe(entry.Name) + ". Put it back or remove it on the ServApps page")
		vars := leftoverNotificationVars(entry)
		if notifiedBefore(vars) {
			continue
		}
		writeNotification(utils.Notification{
			Recipient: "admin",
			Title:     leftoverNotificationTitle,
			Message:   leftoverNotificationMessage,
			Vars:      vars,
			Level:     "warning",
			Link:      "/cosmos-ui/servapps",
			Fork:      leftoverInfo(entry),
		})
	}
	return len(found)
}

// ReportLeftoversAtStart is the hook of the start (index.go): once Docker
// answers, and the databases had time to open, it reports the leftovers.
// It runs in the background and gives up after a few minutes without Docker.
func ReportLeftoversAtStart() {
	time.Sleep(30 * time.Second)
	for attempt := 0; attempt < 20; attempt++ {
		if Connect() == nil {
			reportLeftovers()
			return
		}
		time.Sleep(15 * time.Second)
	}
}

// ---- route ------------------------------------------------------------------

type leftoverRequest struct {
	Action string `json:"action"`
	Name   string `json:"name"`
}

// LeftoversRoute is GET and POST /api/servapps-leftovers.
//
// GET lists the leftovers (PERM_RESOURCES_READ). POST {action, name} with
// action "restore" or "remove" acts on one (PERM_RESOURCES). Errors carry a
// code the UI words: LEFT001 unreadable request, LEFT002 not a leftover,
// LEFT003 refused while it runs, LEFT004 restore failed and undone, LEFT005
// restore failed and not fully undone, LEFT006 remove failed, LEFT007 Docker
// cannot be reached.
func LeftoversRoute(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case "GET":
		if utils.CheckPermissions(w, req, utils.PERM_RESOURCES_READ) != nil {
			return
		}
		found, err := ListLeftovers()
		if err != nil {
			utils.Error("Leftovers - list", err)
			utils.HTTPError(w, "Docker cannot be reached", http.StatusServiceUnavailable, "LEFT007")
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": found})
	case "POST":
		if utils.CheckPermissions(w, req, utils.PERM_RESOURCES) != nil {
			return
		}
		leftoverAction(w, req)
	default:
		if utils.CheckPermissions(w, req, utils.PERM_RESOURCES_READ) != nil {
			return
		}
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
	}
}

func leftoverAction(w http.ResponseWriter, req *http.Request) {
	var request leftoverRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxLeftoverBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || decoder.More() {
		utils.HTTPError(w, "The request could not be read", http.StatusBadRequest, "LEFT001")
		return
	}
	if request.Action != "restore" && request.Action != "remove" {
		utils.HTTPError(w, "The request could not be read", http.StatusBadRequest, "LEFT001")
		return
	}
	if _, _, ok := parseLeftoverName(request.Name); !ok {
		utils.HTTPError(w, "This container is not one left behind by an interrupted change", http.StatusNotFound, "LEFT002")
		return
	}

	switch request.Action {
	case "restore":
		utils.AuditSecurity(req, utils.SecContainerAction, request.Name, "action", "restore_leftover")
		result, err := restoreLeftover(request.Name)
		var failed *restoreError
		switch {
		case errors.Is(err, errNotLeftover):
			utils.HTTPError(w, "This container is not one left behind by an interrupted change", http.StatusNotFound, "LEFT002")
		case errors.As(err, &failed) && failed.undone:
			utils.Error("Leftovers - restore of "+logSafe(request.Name), err)
			utils.HTTPError(w, "The previous container was not put back, nothing was changed: "+failed.reason, http.StatusInternalServerError, "LEFT004")
		case err != nil:
			utils.Error("Leftovers - restore of "+logSafe(request.Name), err)
			utils.HTTPError(w, "The previous container was not put back, and what was done could not all be undone: "+err.Error(), http.StatusInternalServerError, "LEFT005")
		default:
			json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": map[string]string{
				"result": "restored", "name": request.Name, "app": result.App, "setAside": result.SetAside,
			}})
		}
	case "remove":
		utils.AuditSecurity(req, utils.SecContainerDeleted, request.Name)
		err := removeLeftover(request.Name)
		switch {
		case errors.Is(err, errNotLeftover):
			utils.HTTPError(w, "This container is not one left behind by an interrupted change", http.StatusNotFound, "LEFT002")
		case errors.Is(err, errLeftoverRunning):
			utils.HTTPError(w, "The leftover is running: stop it before removing it", http.StatusConflict, "LEFT003")
		case err != nil:
			utils.Error("Leftovers - remove of "+logSafe(request.Name), err)
			utils.HTTPError(w, "The leftover could not be removed: "+err.Error(), http.StatusInternalServerError, "LEFT006")
		default:
			json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": map[string]string{
				"result": "removed", "name": request.Name,
			}})
		}
	}
}

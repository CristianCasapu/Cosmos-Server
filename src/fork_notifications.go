package main

// Fork addition: the routes of the notifications page and of the details
// view (the store is in utils/fork_notifications*.go).
//
// Every route is for a logged-in user and reads or changes the caller's own
// notifications only; rows written for the administrators are left out for
// a caller who is not one. What the details add around a notification (the
// state of the thing now, the security record, the events, the log) is for
// administrators only. Reads are GET; changes are POST with a JSON body.

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/telegram"
	"github.com/azukaar/cosmos-server/src/utils"
)

const maxNotificationBody = 16 << 10

var refPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// registerNotificationRoutes adds the routes next to upstream's two.
func registerNotificationRoutes(api *mux.Router) {
	utils.NotificationSecretMasker = telegram.HideSecrets
	api.HandleFunc("/api/notifications/list", notificationsListRoute)
	api.HandleFunc("/api/notifications/item", notificationsItemRoute)
	api.HandleFunc("/api/notifications/context", notificationsContextRoute)
	api.HandleFunc("/api/notifications/mark", notificationsMarkRoute)
	api.HandleFunc("/api/notifications/delete", notificationsDeleteRoute)
}

// notificationReader is who asks: the nickname, and whether the rows of
// the administrators are theirs to see.
func notificationReader(w http.ResponseWriter, req *http.Request, method string) (string, bool, bool) {
	if utils.CheckPermissions(w, req, utils.PERM_LOGIN) != nil {
		return "", false, false
	}
	if req.Method != method {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return "", false, false
	}
	nickname := utils.GetAuthContext(req).Nickname
	if nickname == "" {
		utils.HTTPError(w, "Notifications belong to a user", http.StatusForbidden, "NOTIF001")
		return "", false, false
	}
	return nickname, utils.HasPermission(req, utils.PERM_ADMIN_READ), true
}

func answerNotifications(w http.ResponseWriter, data interface{}) {
	json.NewEncoder(w).Encode(utils.APIResponse{Status: "OK", Data: data})
}

// notificationsListRoute godoc
// @Summary The caller's notifications, newest first
// @Tags Notifications
// @Produce json
// @Param level query string false "error,warning,info,success"
// @Param source query string false "one source"
// @Param read query string false "read or unread"
// @Param q query string false "text, at most 100 characters"
// @Param cursor query string false "the next value of the previous page"
// @Param limit query int false "at most 100"
// @Security BearerAuth
// @Router /api/notifications/list [get]
func notificationsListRoute(w http.ResponseWriter, req *http.Request) {
	nickname, admin, ok := notificationReader(w, req, "GET")
	if !ok {
		return
	}
	query := req.URL.Query()
	q := utils.NotificationQuery{Recipient: nickname, Admin: admin, Source: query.Get("source"), Read: query.Get("read"),
		Search: strings.TrimSpace(query.Get("q")), Cursor: query.Get("cursor")}
	if levels := query.Get("level"); levels != "" {
		q.Levels = strings.Split(levels, ",")
		if len(q.Levels) > 4 {
			utils.HTTPError(w, "Too many levels", http.StatusBadRequest, "NOTIF002")
			return
		}
	}
	if limit := query.Get("limit"); limit != "" {
		n, err := strconv.Atoi(limit)
		if err != nil || n < 1 || n > utils.NotificationPageMax {
			utils.HTTPError(w, "limit is 1 to 100", http.StatusBadRequest, "NOTIF002")
			return
		}
		q.Limit = n
	}
	items, next, err := utils.ListNotificationViews(q)
	if err == utils.ErrBadNotificationQuery {
		utils.HTTPError(w, "This filter is not one the list takes", http.StatusBadRequest, "NOTIF002")
		return
	}
	if err != nil {
		utils.Error("Notifications: could not list", err)
		utils.HTTPError(w, "Could not read the notifications", http.StatusInternalServerError, "NOTIF003")
		return
	}
	unread, _ := utils.CountUnreadNotifications(nickname, admin)
	sources, _ := utils.NotificationSourcesOf(nickname, admin)
	answerNotifications(w, map[string]interface{}{"items": items, "next": next, "unread": unread, "sources": sources})
}

// notificationsItemRoute godoc
// @Summary One of the caller's notifications in full, with the others about the same thing
// @Tags Notifications
// @Produce json
// @Param id query int false "its id"
// @Param ref query string false "its address, the same for every recipient (16 hex)"
// @Security BearerAuth
// @Router /api/notifications/item [get]
func notificationsItemRoute(w http.ResponseWriter, req *http.Request) {
	nickname, admin, ok := notificationReader(w, req, "GET")
	if !ok {
		return
	}
	id, ref, valid := notificationTarget(req)
	if !valid {
		utils.HTTPError(w, "Give an id or a ref", http.StatusBadRequest, "NOTIF002")
		return
	}
	details, found, err := utils.GetNotificationDetails(nickname, admin, id, ref)
	if err != nil {
		utils.Error("Notifications: could not read one", err)
		utils.HTTPError(w, "Could not read the notification", http.StatusInternalServerError, "NOTIF003")
		return
	}
	if !found {
		utils.HTTPError(w, "There is no such notification of yours", http.StatusNotFound, "NOTIF004")
		return
	}
	answerNotifications(w, details)
}

func notificationTarget(req *http.Request) (int64, string, bool) {
	query := req.URL.Query()
	if raw := query.Get("id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		return id, "", err == nil && id > 0
	}
	ref := query.Get("ref")
	return 0, ref, refPattern.MatchString(ref)
}

// notificationsContextRoute godoc
// @Summary What is around one of the caller's notifications: the state now, the security record, the events, the log
// @Tags Notifications
// @Produce json
// @Param id query int true "its id"
// @Security BearerAuth
// @Router /api/notifications/context [get]
func notificationsContextRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN_READ) != nil {
		return
	}
	nickname, _, ok := notificationReader(w, req, "GET")
	if !ok {
		return
	}
	id, ref, valid := notificationTarget(req)
	if !valid {
		utils.HTTPError(w, "Give an id or a ref", http.StatusBadRequest, "NOTIF002")
		return
	}
	details, found, err := utils.GetNotificationDetails(nickname, true, id, ref)
	if err != nil || !found {
		utils.HTTPError(w, "There is no such notification of yours", http.StatusNotFound, "NOTIF004")
		return
	}
	answerNotifications(w, notificationContext(details))
}

type notificationChange struct {
	IDs  []int64 `json:"ids"`
	Read *bool   `json:"read"`
	All  bool    `json:"all"`
}

func readNotificationChange(w http.ResponseWriter, req *http.Request) (notificationChange, bool) {
	var change notificationChange
	decoder := json.NewDecoder(io.LimitReader(req.Body, maxNotificationBody+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&change); err != nil {
		utils.HTTPError(w, "The request is not one this route takes", http.StatusBadRequest, "NOTIF002")
		return change, false
	}
	if len(change.IDs) > utils.NotificationIDsMax {
		utils.HTTPError(w, "At most 200 notifications at once", http.StatusBadRequest, "NOTIF002")
		return change, false
	}
	for _, id := range change.IDs {
		if id <= 0 {
			utils.HTTPError(w, "Not a notification id", http.StatusBadRequest, "NOTIF002")
			return change, false
		}
	}
	return change, true
}

// notificationsMarkRoute godoc
// @Summary Mark some or all of the caller's notifications read or unread
// @Tags Notifications
// @Accept json
// @Produce json
// @Param body body notificationChange true "{ids, read} or {all: true} (all read)"
// @Security BearerAuth
// @Router /api/notifications/mark [post]
func notificationsMarkRoute(w http.ResponseWriter, req *http.Request) {
	nickname, admin, ok := notificationReader(w, req, "POST")
	if !ok {
		return
	}
	change, ok := readNotificationChange(w, req)
	if !ok {
		return
	}
	var n int64
	var err error
	switch {
	case change.All && len(change.IDs) == 0 && (change.Read == nil || *change.Read):
		n, err = utils.MarkNotifications(nickname, admin, nil, true)
	case !change.All && len(change.IDs) > 0 && change.Read != nil:
		n, err = utils.MarkNotifications(nickname, admin, change.IDs, *change.Read)
	default:
		utils.HTTPError(w, "Give ids and read, or all", http.StatusBadRequest, "NOTIF002")
		return
	}
	if err != nil {
		utils.Error("Notifications: could not mark", err)
		utils.HTTPError(w, "Could not change the notifications", http.StatusInternalServerError, "NOTIF003")
		return
	}
	answerNotifications(w, map[string]interface{}{"changed": n})
}

// notificationsDeleteRoute godoc
// @Summary Delete some of the caller's notifications, or all the read ones
// @Tags Notifications
// @Accept json
// @Produce json
// @Param body body notificationChange true "{ids} or {read: true}"
// @Security BearerAuth
// @Router /api/notifications/delete [post]
func notificationsDeleteRoute(w http.ResponseWriter, req *http.Request) {
	nickname, admin, ok := notificationReader(w, req, "POST")
	if !ok {
		return
	}
	change, ok := readNotificationChange(w, req)
	if !ok {
		return
	}
	var n int64
	var err error
	switch {
	case change.All:
		utils.HTTPError(w, "Unread notifications are deleted one by one", http.StatusBadRequest, "NOTIF002")
		return
	case len(change.IDs) > 0 && change.Read == nil:
		n, err = utils.DeleteNotifications(nickname, admin, change.IDs)
	case len(change.IDs) == 0 && change.Read != nil && *change.Read:
		n, err = utils.DeleteNotifications(nickname, admin, nil)
	default:
		utils.HTTPError(w, "Give ids, or read: true", http.StatusBadRequest, "NOTIF002")
		return
	}
	if err != nil {
		utils.Error("Notifications: could not delete", err)
		utils.HTTPError(w, "Could not delete the notifications", http.StatusInternalServerError, "NOTIF003")
		return
	}
	answerNotifications(w, map[string]interface{}{"deleted": n})
}

package reachwatch

// The routes, on the admin router (same-origin check, token and session
// checks as their neighbours):
//
//	GET  /cosmos/api/reach-watch           the state, the names, the history (admins)
//	POST /cosmos/api/reach-watch/check     test now, in the background (admin; at most every 15 s)
//	POST /cosmos/api/reach-watch/settings  {enabled, minutes} (configuration permission)

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const maxSettingsBody = 1024

// Status is what the panel is told.
type Status struct {
	Enabled    bool        `json:"enabled"`
	Minutes    int         `json:"minutes"`
	MinMinutes int         `json:"minMinutes"`
	MaxMinutes int         `json:"maxMinutes"`
	Reason     string      `json:"reason,omitempty"` // why nothing is tested: noHTTPS, port, none
	Port       int         `json:"port"`
	Names      []NameState `json:"names"`
	TooMany    []string    `json:"tooMany"`
	Direct     []string    `json:"direct"`
	Outage     *Outage     `json:"outage,omitempty"`
	History    []Outage    `json:"history"`
	LastRound  time.Time   `json:"lastRound,omitzero"`
	NextRound  time.Time   `json:"nextRound,omitzero"`
	Running    bool        `json:"running"`
	SeenAs     string      `json:"seenAs,omitempty"`
	Router     string      `json:"router,omitempty"`
	RouterAt   time.Time   `json:"routerAt,omitzero"`
}

// GetStatus returns the state of the watch.
func GetStatus() Status {
	p := currentPlan()
	w.mu.Lock()
	defer w.mu.Unlock()
	st := Status{
		Enabled: !p.Off, Minutes: int(p.Interval / time.Minute),
		MinMinutes: utils.ReachWatchMinMinutes, MaxMinutes: utils.ReachWatchMaxMinutes,
		Reason: p.Reason, Port: p.Port, Names: []NameState{}, TooMany: append([]string{}, p.TooMany...),
		Direct: append([]string{}, p.Direct...), History: []Outage{},
		LastRound: w.lastRound, Running: w.running, SeenAs: w.seenAs, Router: w.router, RouterAt: w.upnpAsked,
	}
	if st.Reason == "" && len(p.Names) == 0 {
		st.Reason = "none"
	}
	if !p.Off && len(p.Names) > 0 {
		st.NextRound = w.nextRound
	}
	for _, c := range p.Names {
		if n, ok := w.names[c.host]; ok {
			copy := *n
			copy.Panel = c.panel
			st.Names = append(st.Names, copy)
		} else {
			st.Names = append(st.Names, NameState{Host: c.host, Panel: c.panel, State: StatePending})
		}
	}
	sort.SliceStable(st.Names, func(i, j int) bool { return st.Names[i].Panel && !st.Names[j].Panel })
	if w.outage != nil {
		o := *w.outage
		st.Outage = &o
	}
	for i := len(w.history) - 1; i >= 0 && len(st.History) < 20; i-- {
		st.History = append(st.History, w.history[i])
	}
	return st
}

// OutsideInfo is what the reachability check (src/reach) is told about a
// name.
type OutsideInfo struct {
	Watched  bool // tested through Cloudflare
	Direct   bool // served directly: cannot be tested from inside
	Off      bool // the watch is switched off
	State    string
	Cause    string
	SeenAt   time.Time
	Since    time.Time
	Interval time.Duration
}

// Outside tells about one name.
func Outside(host string) OutsideInfo {
	host = utils.CloudflareHostname(host)
	p := currentPlan()
	info := OutsideInfo{Off: p.Off, Interval: p.Interval}
	for _, d := range p.Direct {
		if d == host {
			info.Direct = true
			return info
		}
	}
	for _, c := range p.Names {
		if c.host == host {
			info.Watched = true
		}
	}
	if !info.Watched {
		return info
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if st, ok := w.names[host]; ok {
		info.State, info.Cause, info.SeenAt, info.Since = st.State, st.Cause, st.SeenAt, st.Since
	} else {
		info.State = StatePending
	}
	return info
}

func answer(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": data})
}

// StatusRoute answers GET /api/reach-watch.
func StatusRoute(rw http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		utils.HTTPError(rw, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	if utils.CheckPermissions(rw, req, utils.PERM_ADMIN_READ) != nil {
		return
	}
	answer(rw, GetStatus())
}

// CheckRoute answers POST /api/reach-watch/check: a round now.
func CheckRoute(rw http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" {
		utils.HTTPError(rw, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	if utils.CheckPermissions(rw, req, utils.PERM_ADMIN) != nil {
		return
	}
	p := currentPlan()
	started := false
	if !p.Off && len(p.Names) > 0 {
		started = w.forceRound()
	}
	st := GetStatus()
	if started {
		st.Running = true
	}
	answer(rw, map[string]interface{}{"started": started, "status": st})
}

type settingsBody struct {
	Enabled *bool `json:"enabled"`
	Minutes *int  `json:"minutes"`
}

// SettingsRoute answers POST /api/reach-watch/settings.
func SettingsRoute(rw http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" {
		utils.HTTPError(rw, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	if utils.CheckPermissions(rw, req, utils.PERM_CONFIGURATION) != nil {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, maxSettingsBody+1))
	if err != nil || len(raw) > maxSettingsBody {
		utils.HTTPError(rw, "Request too large", http.StatusRequestEntityTooLarge, "RW001")
		return
	}
	var body settingsBody
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || (body.Enabled == nil && body.Minutes == nil) {
		utils.HTTPError(rw, "Invalid request", http.StatusBadRequest, "RW002")
		return
	}
	if body.Minutes != nil && (*body.Minutes < utils.ReachWatchMinMinutes || *body.Minutes > utils.ReachWatchMaxMinutes) {
		utils.HTTPError(rw, "The interval must be between 2 and 60 minutes", http.StatusBadRequest, "RW003")
		return
	}

	changed := []string{}
	utils.ConfigLock.Lock()
	config := utils.ReadConfigFromFile()
	watch := &config.HTTPConfig.Cloudflare.Watch
	if body.Enabled != nil && watch.Off == *body.Enabled {
		watch.Off = !*body.Enabled
		if watch.Off {
			changed = append(changed, "disabled")
		} else {
			changed = append(changed, "enabled")
		}
	}
	if body.Minutes != nil && utils.ReachWatchInterval(*watch) != time.Duration(*body.Minutes)*time.Minute {
		watch.Minutes = *body.Minutes
		changed = append(changed, "interval")
	}
	if len(changed) > 0 {
		utils.SetBaseMainConfig(config)
	}
	utils.ConfigLock.Unlock()

	if len(changed) > 0 {
		utils.AuditSecurity(req, utils.SecConfigSaved, "", "sections", "reach-watch", "change", strings.Join(changed, ","))
		w.mu.Lock()
		w.nextRound = time.Time{} // the new interval counts from now
		w.mu.Unlock()
	}
	answer(rw, GetStatus())
}

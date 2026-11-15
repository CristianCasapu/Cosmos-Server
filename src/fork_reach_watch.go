package main

// Fork addition: wires the watch of the server's reachability from the
// internet (src/reachwatch) into the server: its routes, its start, and the
// "state right now" of its notifications.

import (
	"strings"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/reachwatch"
	"github.com/azukaar/cosmos-server/src/utils"
)

// registerReachWatchRoutes adds the routes to the router of the admin API and
// starts the watch (once per process).
func registerReachWatchRoutes(router *mux.Router) {
	router.HandleFunc("/api/reach-watch", reachwatch.StatusRoute)
	router.HandleFunc("/api/reach-watch/check", reachwatch.CheckRoute)
	router.HandleFunc("/api/reach-watch/settings", reachwatch.SettingsRoute)
	reachwatch.Start()
}

// reachWatchState is the state right now of a notification of the watch;
// nil for any other notification.
func reachWatchState(d utils.NotificationDetails) *contextState {
	if !strings.HasPrefix(d.Kind, "reach.") {
		return nil
	}
	st := reachwatch.GetStatus()
	state := &contextState{Link: "/cosmos-ui/config-general/http-server", Facts: []utils.NotificationFactView{},
		Header: []contextLine{*line("notif.state.col.name"), *line("notif.state.col.state"), *line("notif.state.col.lastTest")}, Rows: []contextRow{}}
	last := ""
	for _, n := range st.Names {
		tested := ""
		if !n.LastTest.IsZero() {
			tested = n.LastTest.Local().Format("2006-01-02 15:04:05")
			if tested > last {
				last = tested
			}
		}
		words := utils.NotificationEnglish("notif.reach.cause." + n.Cause)
		switch n.State {
		case reachwatch.StateReachable:
			words = utils.NotificationEnglish("notif.reach.cause.seen")
		case reachwatch.StatePending:
			words = "Not tested yet"
		case reachwatch.StateSuspect:
			words += " (being confirmed)"
		}
		state.Rows = append(state.Rows, contextRow{Cells: []string{n.Host, words, tested}})
	}
	switch {
	case !st.Enabled:
		state.OK, state.Sentence = flag(false), line("notif.state.reach.off")
	case len(st.Names) == 0:
		state.Sentence = line("notif.state.reach.none")
	case st.Outage != nil:
		state.OK, state.Sentence = flag(false), line("notif.state.reach.down", "at", last)
	default:
		allOK := true
		for _, n := range st.Names {
			allOK = allOK && n.State == reachwatch.StateReachable
		}
		if allOK {
			state.OK, state.Sentence = flag(true), line("notif.state.reach.ok", "at", last)
		} else {
			state.Sentence = line("notif.state.reach.unknown", "at", last)
		}
	}
	if st.Router != "" {
		state.Facts = append(state.Facts, fact("notif.fact.reach.router", st.Router))
	}
	return state
}

package metrics

// Fork addition: resetting the metrics also deletes every recorded event, so
// the handler (API_ResetMetrics) looks at the method before it deletes
// anything (upstream deleted first and refused the method afterwards), and
// the reset itself is the first event of the emptied history: who asked for
// it, and from where.

import (
	"net/http"

	"github.com/azukaar/cosmos-server/src/utils"
)

// resetMethodAllowed answers 405 and returns false for anything but the
// method the panel uses.
func resetMethodAllowed(w http.ResponseWriter, req *http.Request) bool {
	if req.Method == "GET" {
		return true
	}
	utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
	return false
}

// recordReset writes the event that the history was emptied.
func recordReset(req *http.Request) {
	auth := utils.GetAuthContext(req)
	who := auth.Nickname
	if auth.APIToken != nil {
		who = "token@" + auth.APIToken.Name
	}
	clientID, _ := req.Context().Value("ClientID").(string)
	utils.TriggerEvent(
		"cosmos.metrics.reset",
		"Metrics and events reset",
		"warning",
		"",
		map[string]interface{}{
			"by":       who,
			"clientID": clientID,
		})
}

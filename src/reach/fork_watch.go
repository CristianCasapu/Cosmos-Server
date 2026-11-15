package reach

// Fork addition (reachability watch): what the watch of src/reachwatch saw
// from the internet is the strongest thing that can be said about a name. A
// name it saw answer from outside is "reachable, seen at HH:MM"; one it
// confirmed down is a problem with the cause; a name served directly gets
// the honest note that it cannot be tested from inside the house.

import (
	"strconv"
	"time"

	"github.com/azukaar/cosmos-server/src/reachwatch"
	"github.com/azukaar/cosmos-server/src/utils"
)

// StateReachable: the watch saw the name answer from the internet.
const StateReachable = "reachable"

// outside is what the watch knows about a name. Replaced by the tests.
var outside = reachwatch.Outside

// annotateOutside adds the watch's check to every result.
func annotateOutside(resp *Response) {
	for i := range resp.Routes {
		resp.Routes[i] = withOutside(resp.Routes[i])
	}
	if resp.Panel != nil {
		panel := withOutside(*resp.Panel)
		resp.Panel = &panel
	}
}

func withOutside(r RouteResult) RouteResult {
	if r.State == StateOff || r.State == StateLocal || r.State == StateLimited {
		return r
	}
	name, port := splitHost(r.Host)
	if port != 0 {
		return r // a port of its own: not on the public HTTPS port
	}
	info := outside(name)
	add := func(status, code string, params map[string]string) {
		r.Checks = append(r.Checks, Check{ID: "outside", Status: status, Code: code, Params: params})
	}
	at := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	}
	switch {
	case info.Direct:
		add(CheckNote, "direct", nil)
	case !info.Watched:
	case info.Off:
		add(CheckNote, "off", nil)
	case info.State == reachwatch.StateReachable:
		add(CheckOK, "seen", map[string]string{"at": at(info.SeenAt)})
		r.State = StateReachable
		if r.URL == "" {
			address := "https://" + name
			if p := utils.PublicHTTPSPort(utils.GetMainConfig().HTTPConfig); p != 0 && p != 443 {
				address += ":" + strconv.Itoa(p)
			}
			r.URL = address
		}
	case info.State == reachwatch.StateDown:
		add(CheckProblem, "down", map[string]string{"cause": info.Cause, "since": at(info.Since)})
		r.State = StateProblem
	case info.State == reachwatch.StateUnknown:
		add(CheckNote, "unknown", map[string]string{"cause": info.Cause})
	default:
		add(CheckNote, "pending", nil)
	}
	return r
}

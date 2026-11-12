package constellation

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/azukaar/cosmos-server/src/utils"
)

// SubjectRoutesOp is the per-node request/reply subject for proxy-route edits
// coming from another node. A tunneled route lives in the ORIGIN node's config
// and is only cached on the exit; editing it from the exit's URLs page means
// asking each advertising origin to change its own copy. Same addressing as
// cosmos.<node>.manageddb.op: the target is one specific node, never a
// broadcast, so an edit can only ever land on the nodes that advertised the
// route.
const SubjectRoutesOp = "cosmos.%s.routes.op"

const (
	RouteOpUpdate = "update"
	RouteOpDelete = "delete"
)

// routeOpTimeout bounds one origin's reply. The origin's work is a config
// rewrite; anything slower means the node is wedged.
const routeOpTimeout = 5 * time.Second

// RouteOpRequest is the exit→origin payload.
type RouteOpRequest struct {
	Op   string `json:"op"`
	Name string `json:"name"`
	// Route is the full new route for an update. Its Name may differ from
	// Name (a rename); the origin replaces the entry found by Name.
	Route *utils.ProxyRouteConfig `json:"route,omitempty"`
	// Origin is the device that issued the edit, for logs.
	Origin string `json:"origin,omitempty"`
}

// RouteOpReply is the origin→exit response.
type RouteOpReply struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// ErrRouteManaged is returned when the edit targets a route an owner
// materializes. Those are edited on the owner, never through this path: the
// owner would overwrite the change on its next apply.
type ErrRouteManaged struct {
	Name  string
	Owner string
}

func (e ErrRouteManaged) Error() string {
	return "route \"" + e.Name + "\" is managed by " + e.Owner + " and can only be edited from there"
}

// FindLocalTunnel returns the cached tunnel advertising the named route.
func FindLocalTunnel(name string) (utils.ConstellationTunnel, bool) {
	for _, t := range GetLocalTunnelCache() {
		if t.Route.Name == name {
			return t, true
		}
	}
	return utils.ConstellationTunnel{}, false
}

// TunnelAdvertisers lists the device names advertising a cached tunnel.
func TunnelAdvertisers(t utils.ConstellationTunnel) []string {
	out := make([]string, 0, len(t.Targets))
	for _, target := range t.Targets {
		if target.DeviceName != "" {
			out = append(out, target.DeviceName)
		}
	}
	return out
}

// ExecuteRouteOp applies one op to THIS node's config. Exported so the NATS
// responder and any in-process caller share exactly one implementation.
//
// The sequence is the one every route mutation uses: ConfigLock → read the
// file → mutate → SetBaseMainConfig → async restart.
func ExecuteRouteOp(req RouteOpRequest) RouteOpReply {
	if req.Name == "" {
		return RouteOpReply{Status: "failed", Error: "route name is required"}
	}

	utils.ConfigLock.Lock()
	config := utils.ReadConfigFromFile()
	routes := config.HTTPConfig.ProxyConfig.Routes
	idx := -1
	for i, r := range routes {
		if r.Name == req.Name {
			idx = i
			break
		}
	}
	if idx == -1 {
		utils.ConfigLock.Unlock()
		return RouteOpReply{Status: "failed", Error: "route \"" + req.Name + "\" not found on this node"}
	}
	if routes[idx].IsManaged() {
		utils.ConfigLock.Unlock()
		return RouteOpReply{Status: "failed", Error: ErrRouteManaged{Name: req.Name, Owner: routes[idx].ManagedBy()}.Error()}
	}

	switch req.Op {
	case RouteOpUpdate:
		if req.Route == nil {
			utils.ConfigLock.Unlock()
			return RouteOpReply{Status: "failed", Error: "update needs a route"}
		}
		next := *req.Route
		// An origin's route never acquires an owner through a remote edit.
		next.ManagedByKind, next.ManagedByName, next.ManagedByVersion = "", "", 0
		if !next.UseHost && !next.UsePathPrefix {
			utils.ConfigLock.Unlock()
			return RouteOpReply{Status: "failed", Error: "route must have at least one of UseHost or UsePathPrefix enabled"}
		}
		if !utils.IsValidLBMode(next.LBMode) {
			utils.ConfigLock.Unlock()
			return RouteOpReply{Status: "failed", Error: "unsupported load balancing mode \"" + next.LBMode + "\""}
		}
		if next.Name != req.Name {
			for _, r := range routes {
				if r.Name == next.Name {
					utils.ConfigLock.Unlock()
					return RouteOpReply{Status: "failed", Error: "a route named \"" + next.Name + "\" already exists on this node"}
				}
			}
		}
		routes[idx] = next
	case RouteOpDelete:
		routes = append(routes[:idx], routes[idx+1:]...)
	default:
		utils.ConfigLock.Unlock()
		return RouteOpReply{Status: "failed", Error: "unknown op: " + req.Op}
	}

	config.HTTPConfig.ProxyConfig.Routes = routes
	utils.SetBaseMainConfig(config)
	utils.ConfigLock.Unlock()

	utils.Log("[ROUTES] " + req.Op + " of tunneled route " + req.Name + " applied on request of " + req.Origin)
	utils.TriggerEvent(
		"cosmos.routes",
		"Tunneled route "+req.Op+"d remotely: "+req.Name,
		"success",
		"",
		map[string]interface{}{"origin": req.Origin})

	go utils.RestartHTTPServer()
	return RouteOpReply{Status: "ok"}
}

// RegisterRoutesOpResponder subscribes this node to its own routes-op subject.
func RegisterRoutesOpResponder(conn *nats.Conn, self string) (*nats.Subscription, error) {
	if conn == nil {
		return nil, errors.New("nil NATS connection")
	}
	subject := fmt.Sprintf(SubjectRoutesOp, self)
	sub, err := conn.Subscribe(subject, func(m *nats.Msg) {
		var req RouteOpRequest
		if err := json.Unmarshal(m.Data, &req); err != nil {
			utils.Error("[ROUTES] failed to decode RouteOpRequest", err)
			respondRouteOp(m, RouteOpReply{Status: "failed", Error: "decode: " + err.Error()})
			return
		}
		respondRouteOp(m, ExecuteRouteOp(req))
	})
	if err != nil {
		return nil, err
	}
	utils.Log("[ROUTES] listening on " + subject)
	return sub, nil
}

func respondRouteOp(m *nats.Msg, reply RouteOpReply) {
	if m.Reply == "" {
		return
	}
	payload, err := json.Marshal(reply)
	if err != nil {
		return
	}
	m.Respond(payload)
}

// RouteOpTargets decides which nodes an edit of a cached tunnel goes to:
// exactly its advertisers, minus this node (a local copy is edited locally by
// the caller, never through NATS). Refuses managed routes up front.
func RouteOpTargets(t utils.ConstellationTunnel, self string) ([]string, error) {
	if t.Route.IsManaged() {
		return nil, ErrRouteManaged{Name: t.Route.Name, Owner: t.Route.ManagedBy()}
	}
	targets := []string{}
	for _, dev := range TunnelAdvertisers(t) {
		if dev == self {
			continue
		}
		targets = append(targets, dev)
	}
	if len(targets) == 0 {
		return nil, errors.New("route \"" + t.Route.Name + "\" has no reachable advertiser")
	}
	return targets, nil
}

// DispatchTunnelRouteOp sends the op to every advertiser of the named tunnel
// and returns the nodes that acknowledged. A failure on one origin does not
// stop the others: the copies are meant to converge, and the operator is told
// exactly which node refused.
func DispatchTunnelRouteOp(req RouteOpRequest) ([]string, error) {
	tunnel, ok := FindLocalTunnel(req.Name)
	if !ok {
		return nil, errors.New("route \"" + req.Name + "\" is neither local nor tunneled here")
	}
	self := ""
	if dev, err := GetCurrentDevice(); err == nil {
		self = dev.DeviceName
	}
	req.Origin = self

	targets, err := RouteOpTargets(tunnel, self)
	if err != nil {
		return nil, err
	}
	if nc == nil || !IsClientConnected() {
		return nil, errors.New("NATS is not connected: cannot reach " + strings.Join(targets, ", "))
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	acked := []string{}
	failures := []string{}
	for _, dev := range targets {
		msg, rerr := nc.Request(fmt.Sprintf(SubjectRoutesOp, dev), payload, routeOpTimeout)
		if rerr != nil {
			if errors.Is(rerr, nats.ErrNoResponders) {
				failures = append(failures, dev+": not reachable")
			} else {
				failures = append(failures, dev+": "+rerr.Error())
			}
			continue
		}
		var reply RouteOpReply
		if uerr := json.Unmarshal(msg.Data, &reply); uerr != nil {
			failures = append(failures, dev+": bad reply")
			continue
		}
		if reply.Status != "ok" {
			failures = append(failures, dev+": "+reply.Error)
			continue
		}
		acked = append(acked, dev)
	}

	if len(failures) > 0 {
		return acked, errors.New(strings.Join(failures, "; "))
	}
	// The origins re-advertise within a heartbeat; refresh the cache now so
	// the URLs page the caller redirects to does not show the old shape.
	go UpdateLocalTunnelCache()
	return acked, nil
}

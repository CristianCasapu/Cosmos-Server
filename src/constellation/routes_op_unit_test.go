package constellation

import (
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func TestRouteOpTargets(t *testing.T) {
	tunnel := utils.ConstellationTunnel{
		Route: utils.ProxyRouteConfig{Name: "app"},
		Targets: []utils.TunnelTarget{
			{DeviceName: "node-1"}, {DeviceName: "node-2"}, {DeviceName: "exit"}, {DeviceName: ""},
		},
	}
	targets, err := RouteOpTargets(tunnel, "exit")
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0] != "node-1" || targets[1] != "node-2" {
		t.Fatalf("exactly the other advertisers, in order: %v", targets)
	}

	managed := tunnel
	managed.Route = utils.StampManagedBy(managed.Route, utils.ManagedByFunction, "app")
	if _, err := RouteOpTargets(managed, "exit"); err == nil {
		t.Fatal("a managed tunneled route must be refused")
	} else if _, ok := err.(ErrRouteManaged); !ok {
		t.Fatalf("want ErrRouteManaged, got %T", err)
	}

	alone := utils.ConstellationTunnel{Route: utils.ProxyRouteConfig{Name: "x"}, Targets: []utils.TunnelTarget{{DeviceName: "exit"}}}
	if _, err := RouteOpTargets(alone, "exit"); err == nil {
		t.Fatal("no remote advertiser must be an error, never a silent no-op")
	}
}

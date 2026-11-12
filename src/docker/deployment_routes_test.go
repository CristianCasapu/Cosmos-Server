package docker

import (
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

func specWith(routes []utils.ProxyRouteConfig, labels map[string]string) DockerServiceCreateRequest {
	return DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{
		"app": {Name: "app", Image: "img:1", Routes: routes, Labels: labels},
	}}
}

// The hash is the routes-only decision: it must ignore routes and the
// scheduler's labels, and nothing else.
func TestComposeSpecHash_IgnoresRoutesAndSchedulerLabels(t *testing.T) {
	base := specWith(nil, map[string]string{"cosmos-network-name": "auto"})
	withRoutes := specWith([]utils.ProxyRouteConfig{{Name: "r", UseHost: true, Host: "h"}}, map[string]string{"cosmos-network-name": "auto"})
	withLabels := specWith(nil, map[string]string{"cosmos-network-name": "auto", DeploymentLabel: "d", DeploymentVersionLabel: "3", DeploymentSpecHashLabel: "x"})
	if ComposeSpecHash(base) != ComposeSpecHash(withRoutes) {
		t.Fatal("routes must not move the hash")
	}
	if ComposeSpecHash(base) != ComposeSpecHash(withLabels) {
		t.Fatal("scheduler labels must not move the hash")
	}
	other := specWith(nil, map[string]string{"cosmos-network-name": "auto"})
	svc := other.Services["app"]
	svc.Image = "img:2"
	other.Services["app"] = svc
	if ComposeSpecHash(base) == ComposeSpecHash(other) {
		t.Fatal("an image change must move the hash")
	}
}

func TestMergeComposeRoutes_OwnedVsPlain(t *testing.T) {
	owned := utils.StampManagedBy(utils.ProxyRouteConfig{Name: "fn-a", UseHost: true, Host: "a", Target: "http://a"}, utils.ManagedByFunction, "a")
	plain := utils.ProxyRouteConfig{Name: "user", UseHost: true, Host: "u", Target: "http://u"}
	stale := utils.StampManagedBy(utils.ProxyRouteConfig{Name: "fn-a-old", UseHost: true, Host: "o", Target: "http://o"}, utils.ManagedByFunction, "a")
	existingUser := utils.ProxyRouteConfig{Name: "user", UseHost: true, Host: "old", Target: "http://old", AuthEnabled: true}

	routes := []utils.ProxyRouteConfig{stale, existingUser}
	merged, changed := MergeComposeRoutes(routes, specWith([]utils.ProxyRouteConfig{owned, plain}, nil), nil)
	if !changed {
		t.Fatal("must report the change")
	}
	names := map[string]utils.ProxyRouteConfig{}
	for _, r := range merged {
		names[r.Name] = r
	}
	if _, ok := names["fn-a-old"]; ok {
		t.Fatal("the owner's stale route must be pruned")
	}
	if !names["fn-a"].SameOwner(utils.ManagedByFunction, "a") {
		t.Fatal("owned route must land stamped")
	}
	if names["user"].AuthEnabled || names["user"].IsManaged() {
		t.Fatal("a plain compose route keeps the historical upsert-by-name and stays unowned")
	}
}

func TestDeploymentRoutesLabelEntries(t *testing.T) {
	a := utils.StampManagedBy(utils.ProxyRouteConfig{Name: "x"}, utils.ManagedByDeployment, "d")
	b := utils.StampManagedBy(utils.ProxyRouteConfig{Name: "y"}, utils.ManagedByDeployment, "d")
	c := utils.StampManagedBy(utils.ProxyRouteConfig{Name: "z"}, utils.ManagedBySeaweedFS, "m")
	entries := DeploymentRoutesLabelEntries(specWith([]utils.ProxyRouteConfig{a, b, c, {Name: "plain"}}, nil))
	if len(entries) != 2 {
		t.Fatalf("one entry per owner pair, unowned skipped: %v", entries)
	}
}

func TestEffectiveDeploymentVersion(t *testing.T) {
	routes := []utils.ProxyRouteConfig{
		{Name: "a", ManagedByKind: utils.ManagedByDeployment, ManagedByName: "dep", ManagedByVersion: 5},
		{Name: "b", ManagedByKind: utils.ManagedByDeployment, ManagedByName: "dep", ManagedByVersion: 4},
		{Name: "fn-x", ManagedByKind: utils.ManagedByFunction, ManagedByName: "x", ManagedByVersion: 7},
	}
	if EffectiveDeploymentVersion(routes, 3, nil) != 3 {
		t.Fatal("no routes label: label version")
	}
	if EffectiveDeploymentVersion(routes, 3, []string{"deployment:dep"}) != 4 {
		t.Fatal("lowest owned route version lifts the label")
	}
	if EffectiveDeploymentVersion(routes, 6, []string{"deployment:dep"}) != 6 {
		t.Fatal("a newer label wins")
	}
	if EffectiveDeploymentVersion(routes, 3, []string{"deployment:gone"}) != 3 {
		t.Fatal("owner without routes in config: label version")
	}
	if EffectiveDeploymentVersion(routes, 3, []string{"function:x"}) != 7 {
		t.Fatal("non-deployment owners are looked up by their own pair")
	}
	if EffectiveDeploymentVersion(routes, 3, []string{"garbage"}) != 3 {
		t.Fatal("malformed entry: label version")
	}
}

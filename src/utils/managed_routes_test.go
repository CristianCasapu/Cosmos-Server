package utils

import (
	"reflect"
	"testing"
)

func mr(name, kind, owner string) ProxyRouteConfig {
	return ProxyRouteConfig{Name: name, UseHost: true, Host: name + ".example.com", Target: "http://x", ManagedByKind: kind, ManagedByName: owner}
}

func TestValidateManagedBy(t *testing.T) {
	if err := ValidateManagedBy(ProxyRouteConfig{Name: "a"}); err != nil {
		t.Fatalf("unmanaged route must validate: %v", err)
	}
	if err := ValidateManagedBy(ProxyRouteConfig{Name: "a", ManagedByKind: "banana", ManagedByName: "x"}); err == nil {
		t.Fatal("unknown kind must be refused")
	}
	if err := ValidateManagedBy(ProxyRouteConfig{Name: "a", ManagedByKind: ManagedByFunction}); err == nil {
		t.Fatal("a kind without a name must be refused")
	}
	if err := ValidateManagedBy(mr("a", ManagedBySeaweedFS, "test")); err != nil {
		t.Fatalf("valid owner pair refused: %v", err)
	}
}

// Same name, different kinds: the typed pair is what keeps a function named
// "test" and an S3 instance named "test" apart.
func TestSameOwner_IsTyped(t *testing.T) {
	fn := mr("fn-test", ManagedByFunction, "test")
	if fn.SameOwner(ManagedBySeaweedFS, "test") {
		t.Fatal("function:test must not match seaweedfs:test")
	}
	if !fn.SameOwner(ManagedByFunction, "test") {
		t.Fatal("function:test must match itself")
	}
	if fn.ManagedBy() != "function:test" {
		t.Fatalf("ManagedBy() = %q", fn.ManagedBy())
	}
}

func TestMergeManagedRoutes_PruneUpsertPrepend(t *testing.T) {
	user := ProxyRouteConfig{Name: "mine", UseHost: true, Host: "mine.example.com", Target: "http://m"}
	stale := mr("cosmos-registry-old", ManagedByRegistry, "old")
	drifted := mr("cosmos-registry-pub", ManagedByRegistry, "pub")
	drifted.AuthEnabled = true
	other := mr("fn-test", ManagedByFunction, "test")

	routes := []ProxyRouteConfig{user, stale, drifted, other}

	// Owner "pub": its drifted copy is replaced in place; "old" (another
	// owner) and the function route are untouched.
	want := mr("cosmos-registry-pub", ManagedByRegistry, "pub")
	merged, changed := MergeManagedRoutes(routes, ManagedByRegistry, "pub", []ProxyRouteConfig{want})
	if !changed {
		t.Fatal("drift must be detected")
	}
	if len(merged) != 4 || merged[2].AuthEnabled {
		t.Fatalf("drifted route must be replaced in place: %+v", merged)
	}
	if !reflect.DeepEqual(merged[1], stale) || !reflect.DeepEqual(merged[3], other) {
		t.Fatal("other owners must not be touched")
	}

	// Identical desired set: no change.
	if _, changed := MergeManagedRoutes(merged, ManagedByRegistry, "pub", []ProxyRouteConfig{want}); changed {
		t.Fatal("identical render must be a no-op")
	}

	// Owner "old" withdraws (nil desired): its route goes, nothing else.
	merged, changed = MergeManagedRoutes(merged, ManagedByRegistry, "old", nil)
	if !changed || len(merged) != 3 {
		t.Fatalf("stale owner route must be pruned: %+v", merged)
	}

	// A new owner's route is prepended and stamped even if the caller forgot.
	fresh := ProxyRouteConfig{Name: "cosmos-swfs-media-s3", UseHost: true, Host: ":8603", Target: "http://f"}
	merged, changed = MergeManagedRoutes(merged, ManagedBySeaweedFS, "media", []ProxyRouteConfig{fresh})
	if !changed || merged[0].Name != fresh.Name {
		t.Fatalf("new route must be prepended: %+v", merged)
	}
	if !merged[0].SameOwner(ManagedBySeaweedFS, "media") {
		t.Fatal("desired routes must be stamped with the owner")
	}

	// A same-name route of a different owner is taken over by the desired
	// copy (the owner validated the name before writing its record).
	taken := mr("mine", ManagedByDeployment, "dep")
	merged, _ = MergeManagedRoutes(merged, ManagedByDeployment, "dep", []ProxyRouteConfig{taken})
	for _, r := range merged {
		if r.Name == "mine" && !r.SameOwner(ManagedByDeployment, "dep") {
			t.Fatal("desired copy must replace the same-name route")
		}
	}
}

func TestPreserveManagedRoutes(t *testing.T) {
	managed := mr("fn-test", ManagedByFunction, "test")
	user := ProxyRouteConfig{Name: "mine", UseHost: true, Host: "a", Target: "http://a"}
	stored := []ProxyRouteConfig{managed, user}

	// The payload edits the managed route, drops nothing.
	edited := managed
	edited.Disabled = true
	out := PreserveManagedRoutes([]ProxyRouteConfig{user, edited}, stored)
	if len(out) != 2 || out[1].Disabled || out[0].Name != "mine" {
		t.Fatalf("managed route must come back as stored, in payload order: %+v", out)
	}

	// The payload drops the managed route and claims a new owner on a user route.
	claimed := user
	claimed.ManagedByKind, claimed.ManagedByName = ManagedBySeaweedFS, "x"
	out = PreserveManagedRoutes([]ProxyRouteConfig{claimed}, stored)
	if len(out) != 1 || out[0].Name != "fn-test" {
		t.Fatalf("dropped managed route must be restored and a forged owner dropped: %+v", out)
	}
}

func TestFindRouteConflicts(t *testing.T) {
	saved := GetConstellationTunnelRoutes
	defer func() { GetConstellationTunnelRoutes = saved }()
	GetConstellationTunnelRoutes = func() []ProxyRouteConfig {
		return []ProxyRouteConfig{mr("cosmos-swfs-media-s3", ManagedBySeaweedFS, "media")}
	}
	cfg := GetMainConfig()
	cfg.HTTPConfig.ProxyConfig.Routes = []ProxyRouteConfig{
		{Name: "mine", UseHost: true, Host: "mine.example.com", Target: "http://m"},
		mr("fn-test", ManagedByFunction, "test"),
	}
	LoadBaseMainConfig(cfg)

	// Same owner re-materializing: fine.
	if err := FindRouteConflicts([]ProxyRouteConfig{mr("fn-test", ManagedByFunction, "test")}, ManagedByFunction, "test"); err != nil {
		t.Fatalf("same owner must not conflict: %v", err)
	}
	// Another owner reusing the name.
	if err := FindRouteConflicts([]ProxyRouteConfig{{Name: "fn-test", UseHost: true, Host: "z"}}, ManagedByDeployment, "d"); err == nil {
		t.Fatal("name reuse must conflict")
	}
	// Same host+path as a user route.
	if err := FindRouteConflicts([]ProxyRouteConfig{{Name: "new", UseHost: true, Host: "MINE.example.com"}}, ManagedByDeployment, "d"); err == nil {
		t.Fatal("matcher reuse must conflict (case-insensitively)")
	}
	// A tunnel advertisement counts too.
	if err := FindRouteConflicts([]ProxyRouteConfig{{Name: "cosmos-swfs-media-s3", UseHost: true, Host: ":1"}}, ManagedByDeployment, "d"); err == nil {
		t.Fatal("tunnel-cache routes must be checked")
	}
	// Route names are unique, full stop: an unowned route with the same name
	// is somebody else's even when it points at the same target.
	cfg = GetMainConfig()
	cfg.HTTPConfig.ProxyConfig.Routes = append(cfg.HTTPConfig.ProxyConfig.Routes,
		ProxyRouteConfig{Name: "app-web", UseHost: true, Host: "app.example.com", Target: "http://app:80"})
	LoadBaseMainConfig(cfg)
	if err := FindRouteConflicts([]ProxyRouteConfig{{Name: "app-web", UseHost: true, Host: "app.example.com", Target: "http://app:80"}}, ManagedByDeployment, "app"); err == nil {
		t.Fatal("an unowned route with the same name must conflict even with the same target")
	}
	// Candidate carrying its own owner is judged against that owner.
	if err := FindRouteConflicts([]ProxyRouteConfig{mr("fn-test", ManagedByFunction, "test")}, ManagedByDeployment, "fntest"); err != nil {
		t.Fatalf("candidate's own owner must win over the caller's: %v", err)
	}
}

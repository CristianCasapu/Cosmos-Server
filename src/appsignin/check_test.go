package appsignin

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

// check runs the pre-creation check; `networks` says which exist and who is on them.
func check(t *testing.T, request docker.DockerServiceCreateRequest, networks map[string][]string) (docker.DockerServiceCreateRequest, error) {
	t.Helper()
	oldMembers, oldRandom := networkMembers, randomPart
	t.Cleanup(func() { networkMembers, randomPart = oldMembers, oldRandom })
	networkMembers = func(name string) (bool, []string) {
		members, ok := networks[name]
		return ok, members
	}
	randomPart = func() string { return "r4nd0m" }
	err := beforeCreation(httptest.NewRequest("POST", "http://cosmos.example.org/", nil), &request)
	return request, err
}

func svc(image string, env []string, labels map[string]string) docker.ContainerCreateRequestContainer {
	return docker.ContainerCreateRequestContainer{Image: image, Environment: env, Labels: labels}
}

func one(s docker.ContainerCreateRequestContainer) docker.DockerServiceCreateRequest {
	return docker.DockerServiceCreateRequest{Services: map[string]docker.ContainerCreateRequestContainer{"s": s}}
}

// R1: the server writes the label it rebuilds, whatever came; nothing is refused.
func TestTheServerWritesTheSignInLabelItself(t *testing.T) {
	guess := []string{"ADMIN_EMAIL=owner@example.org", "ADMIN_PASSWORD=x", "SMTP_PASSWORD="}
	cases := []struct {
		s    docker.ContainerCreateRequestContainer
		want string
	}{
		{svc("registry.example.org/a/forms:1", guess, nil), "where=web;email=ADMIN_EMAIL;password=ADMIN_PASSWORD;from=guess"},
		// the review's reproduction: secrets named as user, e-mail, database: replaced
		{svc("registry.example.org/a/forms:1", []string{"ADMIN_PASSWORD=x", "DB_PASSWORD=y", "APP_SECRET_KEY=z"},
			map[string]string{LabelKey: "where=web;user=APP_SECRET_KEY;email=ADMIN_PASSWORD;database=DB_PASSWORD"}), "where=web;password=ADMIN_PASSWORD;from=guess"},
		{svc("postgres:18", nil, map[string]string{LabelKey: "where=database;user=POSTGRES_PASSWORD"}), ""},
		{svc("registry.example.org/a/plain:1", nil, map[string]string{LabelKey: "where=web;password=ANY"}), ""},
		{svc("jellyfin/jellyfin:10.11", nil, map[string]string{LabelKey: "where=web;first=owner_name;note=firstRunPage;from=review"}), "where=web;first=owner_name;note=firstRunPage;from=review"},
		{svc("jellyfin/jellyfin:10.11", nil, map[string]string{LabelKey: "where=web;first=x;password=API_KEY;note=logPassword"}), "where=web;first=x;note=firstRunPage;from=review"},
		{svc("jellyfin/jellyfin:10.11", nil, nil), "where=web;note=firstRunPage;from=review"},
	}
	for i, c := range cases {
		got, err := check(t, one(c.s), nil)
		if err != nil {
			t.Fatalf("%d refused: %v", i, err)
		}
		if label := got.Services["s"].Labels[LabelKey]; label != c.want {
			t.Errorf("%d: label %q, want %q", i, label, c.want)
		}
	}
}

// R1b: a start-problem repair that adds a variable changing the guess is created, with the new label.
func TestARepairThatChangesTheGuessIsCreated(t *testing.T) {
	first, err := check(t, one(svc("registry.example.org/a/forms:1", []string{"ADMIN_EMAIL=owner@example.org", "ADMIN_PASSWORD=x"}, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	repaired := first.Services["s"]
	repaired.Environment = append(repaired.Environment, "ADMIN_USER=owner")
	got, err := check(t, one(repaired), nil)
	if err != nil {
		t.Fatalf("the repair is refused: %v", err)
	}
	if label := got.Services["s"].Labels[LabelKey]; label != "where=web;user=ADMIN_USER;email=ADMIN_EMAIL;password=ADMIN_PASSWORD;from=guess" {
		t.Fatalf("label %q", label)
	}
}

func TestTheManagerLabelIsKeptOnlyWhereItIsTrue(t *testing.T) {
	for i, s := range []docker.ContainerCreateRequestContainer{
		svc("busybox:1.36", nil, map[string]string{ManagerLabel: "db"}),
		svc("adminer:5", nil, map[string]string{ManagerLabel: "elsewhere"}),
	} {
		got, err := check(t, one(s), nil)
		if err != nil || got.Services["s"].Labels[ManagerLabel] != "" {
			t.Errorf("%d: %v %v", i, err, got.Services["s"].Labels)
		}
	}
	m := svc("adminer:5", nil, map[string]string{ManagerLabel: "olddb"})
	m.Links = []string{"olddb"}
	got, _ := check(t, one(m), nil)
	if got.Services["s"].Labels[ManagerLabel] != "olddb" {
		t.Error("a linked database lost its manager label")
	}
}

func TestRoutesToManagersAndToolsWithoutSignInAreForAdministrators(t *testing.T) {
	route := utils.ProxyRouteConfig{Name: "r", Mode: "SERVAPP", UseHost: true, Host: "x.example.org", Target: "http://s:80"}
	for _, image := range []string{"adminer:5", "phpmyadmin:5", "redis/redisinsight:2", "redis/redis-stack:7.4.0-v1", "dpage/pgadmin4:9"} {
		s := svc(image, nil, nil)
		s.Routes = []utils.ProxyRouteConfig{route}
		got, err := check(t, one(s), nil)
		if err != nil || !got.Services["s"].Routes[0].AdminOnly || !got.Services["s"].Routes[0].AuthEnabled {
			t.Errorf("%s: %v %+v", image, err, got.Services["s"].Routes)
		}
	}
	// a route carried by another service of the same request
	carrier := svc("nginx:1", nil, nil)
	carrier.Routes = []utils.ProxyRouteConfig{{Name: "to-adminer", Mode: "SERVAPP", UseHost: true, Host: "y.example.org", Target: "http://tool:8080"}}
	tool := svc("adminer:5", nil, nil)
	tool.Name = "tool"
	got, _ := check(t, docker.DockerServiceCreateRequest{Services: map[string]docker.ContainerCreateRequestContainer{"web": carrier, "tool": tool}}, nil)
	if r := got.Services["web"].Routes[0]; !r.AdminOnly || !r.AuthEnabled {
		t.Errorf("a route of another service to the manager: %+v", r)
	}
	s := svc("nginx:1", nil, nil)
	s.Routes = []utils.ProxyRouteConfig{route}
	got, _ = check(t, one(s), nil)
	if got.Services["s"].Routes[0].AdminOnly {
		t.Error("an ordinary web app was changed")
	}
}

func managerRequest(mode string, nets ...string) docker.DockerServiceCreateRequest {
	m := svc("dpage/pgadmin4:9", nil, map[string]string{ManagerLabel: "db"})
	m.Name = "db-pgadmin"
	m.NetworkMode = mode
	m.Networks = map[string]docker.ContainerCreateRequestServiceNetwork{}
	db := svc("postgres:18", nil, nil)
	db.Name = "db"
	request := docker.DockerServiceCreateRequest{Services: map[string]docker.ContainerCreateRequestContainer{"db": db, "m": m}, Networks: map[string]docker.ContainerCreateRequestNetwork{}}
	for _, n := range nets {
		m.Networks[n] = docker.ContainerCreateRequestServiceNetwork{}
		request.Networks[n] = docker.ContainerCreateRequestNetwork{Driver: "bridge"}
	}
	request.Services["m"] = m
	return request
}

// R1a: the repair of a database with its manager (?replace=1) finds the manager's network made: accepted.
func TestAManagersOwnNetworkIsAcceptedAgainButNeverOneWithStrangers(t *testing.T) {
	if _, err := check(t, managerRequest("db-manager-k3x9ab", "db-manager-k3x9ab"), nil); err != nil {
		t.Fatalf("a new network: %v", err)
	}
	if _, err := check(t, managerRequest("db-manager-k3x9ab", "db-manager-k3x9ab"), map[string][]string{"db-manager-k3x9ab": {"db", "db-pgadmin"}}); err != nil {
		t.Fatalf("the repair (the network made for these two): %v", err)
	}
	_, err := check(t, managerRequest("db-manager-k3x9ab", "db-manager-k3x9ab"), map[string][]string{"db-manager-k3x9ab": {"db", "someone-else"}})
	if err == nil || !strings.Contains(err.Error(), "someone-else") {
		t.Fatalf("a network with a stranger: %v", err)
	}
}

// a manager without a network of its own gets one, from any door
func TestAManagerWithoutANetworkOfItsOwnGetsOne(t *testing.T) {
	for _, mode := range []string{"", "bridge", "default"} {
		s := svc("adminer:5", nil, nil)
		s.Name = "adminer"
		s.NetworkMode = mode
		got, err := check(t, one(s), nil)
		if err != nil {
			t.Fatal(err)
		}
		m := got.Services["s"]
		if m.NetworkMode != "adminer-r4nd0m" || got.Networks["adminer-r4nd0m"].Driver != "bridge" {
			t.Fatalf("%q: %q %+v", mode, m.NetworkMode, got.Networks)
		}
		if _, on := m.Networks["adminer-r4nd0m"]; !on {
			t.Fatalf("not joined: %+v", m.Networks)
		}
	}
	// a Market stack's own network is left as it is
	s := svc("adminer:5", nil, nil)
	s.NetworkMode = "cosmos-stack-default"
	got, _ := check(t, one(s), nil)
	if got.Services["s"].NetworkMode != "cosmos-stack-default" {
		t.Fatal("a stack's network was changed")
	}
}

// review 2, item 4: Start wires the creation's check (not only the e-mail) and the URL guard
func TestStartWiresTheChecksBeforeTheCreationAndWhereURLsAreSaved(t *testing.T) {
	oldBefore, oldAfter, oldGuard := docker.ForkBeforeCreation, docker.ForkAfterCreation, utils.ForkGuardRoutes
	t.Cleanup(func() {
		docker.ForkBeforeCreation, docker.ForkAfterCreation, utils.ForkGuardRoutes = oldBefore, oldAfter, oldGuard
	})
	docker.ForkBeforeCreation, utils.ForkGuardRoutes = nil, nil
	Start()
	request := one(svc("adminer:5", nil, nil))
	if err := docker.ForkBeforeCreation(httptest.NewRequest("POST", "http://cosmos.example.org/", nil), &request); err != nil || request.Services["s"].NetworkMode == "" {
		t.Fatalf("the creation's check is not wired: %v %+v", err, request.Services["s"])
	}
	if utils.ForkGuardRoutes == nil {
		t.Fatal("the URL guard is not wired")
	}
}

func TestAURLSavedLaterToAManagerIsForAdministrators(t *testing.T) {
	old, oldNotify := imageOf, notify
	t.Cleanup(func() { imageOf, notify = old, oldNotify })
	imageOf = func(name string) string {
		return map[string]string{"db-adminer": "adminer:5", "web": "nginx:1"}[name]
	}
	told := []utils.Notification{}
	notify = func(n utils.Notification) { told = append(told, n) }
	routes := []utils.ProxyRouteConfig{
		{Name: "tool", Mode: "SERVAPP", UseHost: true, Host: "tool.example.org", Target: "http://db-adminer:8080"},
		{Name: "site", Mode: "SERVAPP", UseHost: true, Host: "site.example.org", Target: "http://web:80"},
		{Name: "already", Mode: "SERVAPP", UseHost: true, Host: "a.example.org", Target: "http://db-adminer:8080", AuthEnabled: true, AdminOnly: true},
	}
	changed := guardRoutes(routes, "owner")
	if len(changed) != 1 || changed[0] != "tool" || !routes[0].AdminOnly || !routes[0].AuthEnabled || routes[1].AdminOnly {
		t.Fatalf("%v %+v", changed, routes)
	}
	if len(told) != 1 || told[0].Recipient != "owner" || told[0].Fork.Kind != "route.protected" {
		t.Fatalf("not told: %+v", told)
	}
}

// Second review, item 4: the panel's own definition (the node test writes the fixture from the
// builder) passes the check unchanged but for the protection, and is off the default bridge.
func TestThePanelsDefinitionPassesTheCheck(t *testing.T) {
	request := fixtureDefinition(t)
	got, err := check(t, request, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"credsdb", "credsdb-pgadmin"} {
		if got.Services[name].NetworkMode != "credsdb-manager-f1xt3r" {
			t.Errorf("%s: network mode %q", name, got.Services[name].NetworkMode)
		}
	}
	m := got.Services["credsdb-pgadmin"]
	if m.Labels[ManagerLabel] != "credsdb" || len(m.Routes) != 1 || !m.Routes[0].AdminOnly || !m.Routes[0].AuthEnabled {
		t.Fatalf("the manager: %+v %+v", m.Labels, m.Routes)
	}
	if _, more := got.Networks["credsdb-pgadmin-r4nd0m"]; more || len(got.Networks) != 1 {
		t.Fatalf("networks: %+v", got.Networks)
	}
}
